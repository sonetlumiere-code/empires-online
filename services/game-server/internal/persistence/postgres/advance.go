package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/advance"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
)

// Persistencia del avance de era (SPEC-ECON-STONE §4.4, ADR-030 §4 y §5). Cada
// escritura es una transacción idempotente: la cola puede reintentarla.

// advanceInProgress dice si una ciudad tiene un avance en curso.
func advanceInProgress(ctx context.Context, db DB, cityID int64) (bool, error) {
	var existe bool
	if err := db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM city_era_advances WHERE city_id = $1)`, cityID).Scan(&existe); err != nil {
		return false, fmt.Errorf("leer el avance de la ciudad %d: %w", cityID, normalize(err))
	}
	return existe, nil
}

// Advance debita todo el coste y registra el avance en UNA transacción
// (RN-ECON-025, INV-ECON-002): un delta versionado por recurso; si alguno no
// se aplica, no se aplica ninguno. Idempotente por el request_id de la fila
// del avance. Rechazos de dominio: advance.ErrCityBusy (otro avance o una
// producción en curso), advance.ErrUnavailable (la ciudad ya no está en la era
// de partida), advance.ErrCityNotFound, economy.ErrInsufficientResources y
// ErrStaleStock.
func (r *EconomyRepo) Advance(
	ctx context.Context, requestID string, playerID uuid.UUID, a advance.Advance, deltas []StockDelta, tick uint64,
) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		var previa string
		switch err := tx.QueryRow(ctx,
			`SELECT request_id FROM city_era_advances WHERE city_id = $1 FOR UPDATE`, a.CityID).Scan(&previa); {
		case err == nil:
			if previa == requestID {
				return nil // ya escrito: un reintento
			}
			return advance.ErrCityBusy
		case !errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("leer el avance de la ciudad %d: %w", a.CityID, normalize(err))
		}

		var era string
		switch err := tx.QueryRow(ctx, `SELECT era FROM cities WHERE id = $1 FOR UPDATE`, a.CityID).Scan(&era); {
		case errors.Is(normalize(err), ErrNotFound):
			return advance.ErrCityNotFound
		case err != nil:
			return fmt.Errorf("leer la era de la ciudad %d: %w", a.CityID, normalize(err))
		}
		if city.Era(era) != a.From {
			// INV-CITY-018: nunca se encarga un avance que no sale de la era
			// actual. Un reintento tardío, con el avance ya terminado, acaba aquí
			// sin debitar nada.
			return fmt.Errorf("%w: la ciudad %d está en %s", advance.ErrUnavailable, a.CityID, era)
		}

		// RN-ECON-026: con un entrenamiento en curso, la ciudad no avanza.
		var entrenando bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM city_productions WHERE city_id = $1 AND unit_id IS NULL)`,
			a.CityID).Scan(&entrenando); err != nil {
			return fmt.Errorf("leer la producción de la ciudad %d: %w", a.CityID, normalize(err))
		}
		if entrenando {
			return advance.ErrCityBusy
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
			`INSERT INTO city_era_advances (city_id, from_era, to_era, completes_at_ms, request_id)
			 VALUES ($1, $2, $3, $4, $5)`,
			a.CityID, string(a.From), string(a.To), a.CompletesAtMs, requestID); err != nil {
			return fmt.Errorf("insertar el avance: %w", normalize(err))
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventEraAdvanceOrdered, Tick: tick, PlayerID: &playerID,
			Payload: map[string]any{
				"cityId": a.CityID, "from": string(a.From), "to": string(a.To),
				"completesAtMs": a.CompletesAtMs, "requestId": requestID,
			},
		})
	})
}

// CompleteAdvance termina el avance de una ciudad en UNA transacción
// (RN-ECON-027): cambia la era, sube el límite en cap(to) − cap(from) leídos
// de la tabla eras, borra la fila del avance y escribe EraAdvanced. Sólo si la
// ciudad seguía en `from`: un reintento no suma dos veces y la era nunca
// retrocede (INV-CITY-018).
func (r *EconomyRepo) CompleteAdvance(
	ctx context.Context, cityID int64, playerID uuid.UUID, from, to city.Era, tick uint64,
) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE cities
			    SET era = $3,
			        population_limit = population_limit
			            + (SELECT population_cap FROM eras WHERE code = $3)
			            - (SELECT population_cap FROM eras WHERE code = $2),
			        version = version + 1
			  WHERE id = $1 AND era = $2`,
			cityID, string(from), string(to))
		if err != nil {
			return fmt.Errorf("cambiar la era de la ciudad %d: %w", cityID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			return nil // ya escrito, o la ciudad no está en la era de partida
		}
		if _, err := tx.Exec(ctx, `DELETE FROM city_era_advances WHERE city_id = $1`, cityID); err != nil {
			return fmt.Errorf("retirar el avance de la ciudad %d: %w", cityID, normalize(err))
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventEraAdvanced, Tick: tick, PlayerID: &playerID,
			Payload: map[string]any{"cityId": cityID, "from": string(from), "to": string(to)},
		})
	})
}

// ListAdvances lee los avances en curso, en orden de ciudad.
func (r *EconomyRepo) ListAdvances(ctx context.Context) ([]advance.Advance, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT city_id, from_era, to_era, completes_at_ms, request_id FROM city_era_advances ORDER BY city_id`)
	if err != nil {
		return nil, fmt.Errorf("leer los avances: %w", normalize(err))
	}
	defer rows.Close()
	var out []advance.Advance
	for rows.Next() {
		var a advance.Advance
		var from, to string
		if err := rows.Scan(&a.CityID, &from, &to, &a.CompletesAtMs, &a.RequestID); err != nil {
			return nil, fmt.Errorf("leer un avance: %w", err)
		}
		a.From, a.To = city.Era(from), city.Era(to)
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAdvance retira el avance de una ciudad sin aplicarlo: la validación de
// carga lo usa con uno que no sale de la era de su ciudad (INV-CITY-018).
func (r *EconomyRepo) DeleteAdvance(ctx context.Context, cityID int64) error {
	if _, err := r.store.pool.Exec(ctx, `DELETE FROM city_era_advances WHERE city_id = $1`, cityID); err != nil {
		return fmt.Errorf("retirar el avance de la ciudad %d: %w", cityID, normalize(err))
	}
	return nil
}
