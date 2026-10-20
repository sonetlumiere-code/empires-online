package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loadcheck"
)

// EventInvariantRepaired es el evento de auditoría de una reparación de carga.
const EventInvariantRepaired = "InvariantRepaired"

// LoadCheckRepo lee lo que la validación de carga necesita además de lo que ya
// carga el arranque, y escribe sus reparaciones (EO-119).
type LoadCheckRepo struct{ store *Store }

// NewLoadCheckRepo crea el repositorio.
func NewLoadCheckRepo(s *Store) *LoadCheckRepo { return &LoadCheckRepo{store: s} }

// PlayerIDs devuelve el conjunto de players.id.
func (r *LoadCheckRepo) PlayerIDs(ctx context.Context) (map[uuid.UUID]struct{}, error) {
	rows, err := r.store.pool.Query(ctx, `SELECT id FROM players`)
	if err != nil {
		return nil, fmt.Errorf("listar jugadores: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID]struct{})
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// Garrisons devuelve todas las filas de garrisons: unidad → ciudad.
func (r *LoadCheckRepo) Garrisons(ctx context.Context) (map[int64]int64, error) {
	rows, err := r.store.pool.Query(ctx, `SELECT unit_id, city_id FROM garrisons`)
	if err != nil {
		return nil, fmt.Errorf("listar guarniciones: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]int64)
	for rows.Next() {
		var unitID, cityID int64
		if err := rows.Scan(&unitID, &cityID); err != nil {
			return nil, err
		}
		out[unitID] = cityID
	}
	return out, rows.Err()
}

// EraCaps devuelve el population_cap de cada era del catálogo.
func EraCaps(eras []city.EraDefinition) map[city.Era]int32 {
	out := make(map[city.Era]int32, len(eras))
	for _, e := range eras {
		out[e.Code] = e.PopulationCap
	}
	return out
}

// ApplyLoadRepairs escribe las reparaciones de la validación de carga en UNA
// transacción, con un evento InvariantRepaired por hallazgo REPAIR. O se
// escriben todas o ninguna: un arranque que cae a medias vuelve a encontrar lo
// mismo y lo repara igual.
//
// cities y units son los objetos que loadcheck.Check ya corrigió en memoria; de
// ellos sale el valor que se escribe. La versión de cada ciudad corregida sube
// también en memoria, para que la RAM y la fila no diverjan.
func (r *LoadCheckRepo) ApplyLoadRepairs(
	ctx context.Context,
	tick uint64,
	res loadcheck.Result,
	cities []*city.City,
	units []*unit.Unit,
) error {
	if !hasRepairs(res) {
		return nil
	}
	cityByID := make(map[int64]*city.City, len(cities))
	for _, c := range cities {
		cityByID[c.ID] = c
	}
	unitByID := make(map[int64]*unit.Unit, len(units))
	for _, u := range units {
		unitByID[u.ID] = u
	}
	controlByID := make(map[int64]territory.Control, len(res.Controls))
	for _, c := range res.Controls {
		controlByID[c.TerritoryID] = c
	}

	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		for _, id := range res.GarrisonDeletes {
			if _, err := tx.Exec(ctx, `DELETE FROM garrisons WHERE unit_id = $1`, id); err != nil {
				return fmt.Errorf("borrar la guarnición de la unidad %d: %w", id, err)
			}
		}
		for _, id := range res.UnitRepairs {
			u, ok := unitByID[id]
			if !ok {
				return fmt.Errorf("reparación de la unidad %d sin la unidad cargada", id)
			}
			tag, err := tx.Exec(ctx,
				`UPDATE units SET max_hp = $2, hp = $3, status = $4 WHERE id = $1`,
				u.ID, u.MaxHP, u.HP, string(u.Status))
			if err != nil {
				return fmt.Errorf("reparar la unidad %d: %w", id, normalize(err))
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("reparar la unidad %d: la fila no existe", id)
			}
		}
		for _, id := range res.CityRepairs {
			c, ok := cityByID[id]
			if !ok {
				return fmt.Errorf("reparación de la ciudad %d sin la ciudad cargada", id)
			}
			tag, err := tx.Exec(ctx,
				`UPDATE cities
				 SET population = $2, population_limit = $3, last_offline_at = $4,
				     protection_until = $5, version = version + 1
				 WHERE id = $1`,
				c.ID, c.Population, c.PopulationLimit, c.LastOfflineAt, c.ProtectionUntil)
			if err != nil {
				return fmt.Errorf("reparar la ciudad %d: %w", id, normalize(err))
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("reparar la ciudad %d: la fila no existe", id)
			}
			c.Version++
		}
		for _, id := range res.ControlRepairs {
			c, ok := controlByID[id]
			if !ok {
				return fmt.Errorf("reparación del territorio %d sin su control", id)
			}
			// La versión ya viene incrementada desde loadcheck: se escribe tal cual
			// y se condiciona a la anterior, como cualquier otro cambio de control.
			tag, err := tx.Exec(ctx,
				`UPDATE territory_control
				 SET owner_type = $2, owner_id = $3, captured_at = $4, version = $5
				 WHERE territory_id = $1 AND version = $6`,
				c.TerritoryID, string(c.OwnerType), c.OwnerID, c.CapturedAt, c.Version, c.Version-1)
			if err != nil {
				return fmt.Errorf("reparar el control del territorio %d: %w", id, normalize(err))
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("reparar el control del territorio %d: la versión cambió durante el arranque", id)
			}
		}
		for _, f := range res.Findings {
			if f.Policy != loadcheck.PolicyRepair {
				continue
			}
			if err := appendEvent(ctx, tx, worldEvent{
				EventType: EventInvariantRepaired,
				Tick:      tick,
				Payload: map[string]any{
					"inv_id": f.InvID, "severity": f.Severity, "entity": f.Entity, "detail": f.Detail,
				},
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func hasRepairs(res loadcheck.Result) bool {
	return len(res.CityRepairs)+len(res.UnitRepairs)+len(res.ControlRepairs)+len(res.GarrisonDeletes) > 0
}

// ErrLoadCheckFatal indica que el estado persistido viola un invariante que no
// se repara solo (política FAIL_FAST).
var ErrLoadCheckFatal = errors.New("el estado persistido viola invariantes que no se reparan solos")

// CheckAndRepair completa la entrada con los jugadores y las guarniciones,
// valida y, si nada es fatal, escribe las reparaciones (EO-119).
//
// Con un hallazgo FAIL_FAST devuelve ErrLoadCheckFatal SIN escribir nada:
// reparar lo reparable y luego abortar dejaría la base a medio corregir por un
// proceso que no llegó a servir. El resultado se devuelve siempre, también con
// error, para que quien llama emita la señal de cada hallazgo.
func (r *LoadCheckRepo) CheckAndRepair(ctx context.Context, in loadcheck.Input, tick uint64) (loadcheck.Result, error) {
	var err error
	if in.Players, err = r.PlayerIDs(ctx); err != nil {
		return loadcheck.Result{}, err
	}
	if in.Garrisons, err = r.Garrisons(ctx); err != nil {
		return loadcheck.Result{}, err
	}
	res := loadcheck.Check(in)
	if res.Fatal() {
		return res, ErrLoadCheckFatal
	}
	if err := r.ApplyLoadRepairs(ctx, tick, res, in.Cities, in.Units); err != nil {
		return res, fmt.Errorf("escribir las reparaciones de carga: %w", err)
	}
	return res, nil
}
