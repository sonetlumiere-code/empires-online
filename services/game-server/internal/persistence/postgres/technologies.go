package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/technology"
)

// Persistencia del árbol tecnológico (SPEC-TECHNOLOGIES §7, ADR-046 §3 y §4):
// el catálogo, la investigación en curso de cada ciudad y las tecnologías de
// cada jugador. Cada escritura es una transacción idempotente: la cola puede
// reintentarla.

// Eventos del árbol tecnológico. Son de un solo jugador, sin counterpart: no
// van al registro ni a los avisos (SPEC-TECHNOLOGIES Q13).
const (
	EventResearchOrdered      = "ResearchOrdered"
	EventTechnologyResearched = "TechnologyResearched"
)

// ListTechnologies lee el catálogo de tecnologías, en orden de id, con el código
// de la civilización de cada exclusiva. No lo valida: eso es de
// technology.NewCatalog.
func (r *EconomyRepo) ListTechnologies(ctx context.Context) ([]technology.Technology, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT t.id, t.code, t.name, t.era, coalesce(c.code, ''), t.costs, t.foreign_goods, t.own_good,
		        t.duration_ms, t.requires, t.effects
		   FROM technologies t
		   LEFT JOIN civilizations c ON c.id = t.civilization_id
		  ORDER BY t.id`)
	if err != nil {
		return nil, fmt.Errorf("leer el catálogo de tecnologías: %w", normalize(err))
	}
	defer rows.Close()
	var out []technology.Technology
	for rows.Next() {
		var (
			t                       technology.Technology
			era                     string
			costs, requires, effect []byte
			foreign, own, ms        int64
		)
		if err := rows.Scan(&t.ID, &t.Code, &t.Name, &era, &t.Civilization, &costs, &foreign, &own, &ms,
			&requires, &effect); err != nil {
			return nil, fmt.Errorf("leer una tecnología: %w", err)
		}
		t.Era, t.ForeignGoods, t.OwnGood, t.DurationMs = city.Era(era), foreign, own, ms
		var fijos map[string]int64
		if err := json.Unmarshal(costs, &fijos); err != nil {
			return nil, fmt.Errorf("los costes de %s: %w", t.Code, err)
		}
		recursos := make([]string, 0, len(fijos))
		for res := range fijos {
			recursos = append(recursos, res)
		}
		// El orden del catálogo de recursos, para que el coste sea siempre el
		// mismo slice (madera antes que piedra).
		sort.Slice(recursos, func(i, j int) bool { return resourceRank(recursos[i]) < resourceRank(recursos[j]) })
		for _, res := range recursos {
			t.Costs = append(t.Costs, economy.Cost{Resource: economy.Resource(res), Amount: fijos[res]})
		}
		if err := json.Unmarshal(requires, &t.Requires); err != nil {
			return nil, fmt.Errorf("los requisitos de %s: %w", t.Code, err)
		}
		if err := json.Unmarshal(effect, &t.Effects); err != nil {
			return nil, fmt.Errorf("los efectos de %s (¿un valor no entero?): %w", t.Code, err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// resourceRank es la posición de un recurso en el catálogo; uno desconocido va
// al final.
func resourceRank(r string) int {
	for i, x := range economy.Resources() {
		if string(x) == r {
			return i
		}
	}
	return len(economy.Resources())
}

// ListPlayerTechnologies lee las tecnologías de cada jugador, cada lista en
// orden de código.
func (r *EconomyRepo) ListPlayerTechnologies(ctx context.Context) (map[uuid.UUID][]string, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT pt.player_id, t.code
		   FROM player_technologies pt
		   JOIN technologies t ON t.id = pt.technology_id
		  ORDER BY pt.player_id, t.code`)
	if err != nil {
		return nil, fmt.Errorf("leer las tecnologías de los jugadores: %w", normalize(err))
	}
	defer rows.Close()
	out := map[uuid.UUID][]string{}
	for rows.Next() {
		var p uuid.UUID
		var code string
		if err := rows.Scan(&p, &code); err != nil {
			return nil, fmt.Errorf("leer una tecnología de un jugador: %w", err)
		}
		out[p] = append(out[p], code)
	}
	return out, rows.Err()
}

// ListResearch lee las investigaciones en curso, en orden de ciudad.
func (r *EconomyRepo) ListResearch(ctx context.Context) ([]technology.Research, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT cr.city_id, t.code, cr.completes_at_ms, cr.request_id
		   FROM city_research cr
		   JOIN technologies t ON t.id = cr.technology_id
		  ORDER BY cr.city_id`)
	if err != nil {
		return nil, fmt.Errorf("leer las investigaciones: %w", normalize(err))
	}
	defer rows.Close()
	var out []technology.Research
	for rows.Next() {
		var x technology.Research
		if err := rows.Scan(&x.CityID, &x.Technology, &x.CompletesAtMs, &x.RequestID); err != nil {
			return nil, fmt.Errorf("leer una investigación: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Research debita todo el coste y registra la investigación en UNA transacción
// (RN-TECH-004, INV-ECON-002): un delta versionado por recurso; si alguno no se
// aplica, no se aplica ninguno. Idempotente por el request_id de la fila.
// Rechazos de dominio: technology.ErrCityBusy (otra investigación o un avance
// de era en curso), technology.ErrCityNotFound, technology.ErrUnavailable (lo
// que rechazan los triggers de INV-TECH-001), economy.ErrInsufficientResources
// y ErrStaleStock.
func (r *EconomyRepo) Research(
	ctx context.Context, requestID string, playerID uuid.UUID, x technology.Research, deltas []StockDelta, tick uint64,
) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		var previa string
		switch err := tx.QueryRow(ctx,
			`SELECT request_id FROM city_research WHERE city_id = $1 FOR UPDATE`, x.CityID).Scan(&previa); {
		case err == nil:
			if previa == requestID {
				return nil // ya escrito: un reintento
			}
			return technology.ErrCityBusy
		case !errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("leer la investigación de la ciudad %d: %w", x.CityID, normalize(err))
		}
		var dueno uuid.UUID
		switch err := tx.QueryRow(ctx, `SELECT owner_player_id FROM cities WHERE id = $1 FOR UPDATE`, x.CityID).Scan(&dueno); {
		case errors.Is(normalize(err), ErrNotFound):
			return technology.ErrCityNotFound
		case err != nil:
			return fmt.Errorf("leer la ciudad %d: %w", x.CityID, normalize(err))
		}
		if dueno != playerID {
			return technology.ErrCityNotFound
		}
		// RN-TECH-003: con un avance de era en curso, la ciudad no investiga.
		avanzando, err := advanceInProgress(ctx, tx, x.CityID)
		if err != nil {
			return err
		}
		if avanzando {
			return technology.ErrCityBusy
		}
		var techID int32
		switch err := tx.QueryRow(ctx, `SELECT id FROM technologies WHERE code = $1`, x.Technology).Scan(&techID); {
		case errors.Is(normalize(err), ErrNotFound):
			return technology.ErrUnavailable
		case err != nil:
			return fmt.Errorf("leer la tecnología %s: %w", x.Technology, normalize(err))
		}

		for _, d := range deltas {
			aplicado, err := applyStock(ctx, tx, d)
			if err != nil {
				return err
			}
			if !aplicado {
				return ErrStaleStock
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO city_research (city_id, technology_id, completes_at_ms, request_id) VALUES ($1, $2, $3, $4)`,
			x.CityID, techID, x.CompletesAtMs, requestID); err != nil {
			if IsCheckViolation(err) {
				// INV-TECH-001: un trigger la rechaza (la era, los requisitos, la
				// civilización o la tecnología ya tenida).
				return fmt.Errorf("%w: %v", technology.ErrUnavailable, err)
			}
			return fmt.Errorf("insertar la investigación: %w", normalize(err))
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventResearchOrdered, Tick: tick, PlayerID: &playerID,
			Payload: map[string]any{
				"cityId": x.CityID, "technology": x.Technology, "completesAtMs": x.CompletesAtMs, "requestId": requestID,
			},
		})
	})
}

// CompleteResearch termina la investigación de una ciudad en UNA transacción
// (RN-TECH-005): la tecnología pasa a ser del jugador, la fila de la
// investigación se borra y se escribe TechnologyResearched. Idempotente: un
// reintento con la tecnología ya escrita no hace nada.
func (r *EconomyRepo) CompleteResearch(
	ctx context.Context, cityID int64, playerID uuid.UUID, code string, at time.Time, tick uint64,
) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO player_technologies (player_id, technology_id, researched_at)
			 SELECT $1, id, $3 FROM technologies WHERE code = $2
			 ON CONFLICT (player_id, technology_id) DO NOTHING`,
			playerID, code, at.UTC())
		if err != nil {
			return fmt.Errorf("dar la tecnología %s al jugador %s: %w", code, playerID, normalize(err))
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM city_research
			  WHERE city_id = $1 AND technology_id = (SELECT id FROM technologies WHERE code = $2)`,
			cityID, code); err != nil {
			return fmt.Errorf("retirar la investigación de la ciudad %d: %w", cityID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			return nil // ya escrita: un reintento
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventTechnologyResearched, Tick: tick, PlayerID: &playerID,
			Payload: map[string]any{"cityId": cityID, "technology": code, "atMs": at.UnixMilli()},
		})
	})
}

// DeleteResearch retira la investigación de una ciudad sin aplicarla: la
// validación de carga la usa con una que no cumple INV-TECH-001.
func (r *EconomyRepo) DeleteResearch(ctx context.Context, cityID int64) error {
	if _, err := r.store.pool.Exec(ctx, `DELETE FROM city_research WHERE city_id = $1`, cityID); err != nil {
		return fmt.Errorf("retirar la investigación de la ciudad %d: %w", cityID, normalize(err))
	}
	return nil
}
