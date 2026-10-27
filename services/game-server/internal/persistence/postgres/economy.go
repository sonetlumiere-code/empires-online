package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Persistencia de la economía (SPEC-ECON-WOOD, ADR-023). Cada escritura es una
// transacción idempotente: la cola de persistencia puede reintentarla, y un
// reintento tras perder la confirmación del COMMIT no cambia nada.

// Eventos de world_events de la economía.
const (
	EventBuildingOrdered   = "BuildingOrdered"
	EventBuildingCompleted = "BuildingCompleted"
	EventTrainingOrdered   = "TrainingOrdered"
	EventUnitTrained       = "UnitTrained"
)

// ErrStaleStock: la base tiene una versión del stock igual o posterior a la del
// encargo. El encargo no se aplica: insertar su efecto sin su débito rompería
// INV-ECON-002.
var ErrStaleStock = errors.New("el stock de la base va por delante del encargo")

// ErrNoProduction: el nacimiento de una ciudad sin producción.
var ErrNoProduction = errors.New("la ciudad no tiene producción")

// StockDelta es una escritura del stock por delta condicionado a la versión
// (ADR-023 §1).
type StockDelta struct {
	CityID   int64
	Resource economy.Resource
	Delta    int64
	Version  int64
}

// StockRow es una fila de city_stocks.
type StockRow struct {
	CityID int64
	Stock  economy.Stock
}

// EconomyRepo persiste stocks, tareas, edificios y producciones.
type EconomyRepo struct {
	store     *Store
	units     *UnitRepo
	movements *MovementRepo
}

// NewEconomyRepo crea el repositorio.
func NewEconomyRepo(s *Store, u *UnitRepo, m *MovementRepo) *EconomyRepo {
	return &EconomyRepo{store: s, units: u, movements: m}
}

// CreateCityStocks crea a cero las filas de stock de una ciudad recién
// fundada (§4.1), en la transacción de la fundación.
func CreateCityStocks(ctx context.Context, db DB, cityID int64) error {
	for _, r := range economy.Resources() {
		if _, err := db.Exec(ctx,
			`INSERT INTO city_stocks (city_id, resource) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			cityID, string(r)); err != nil {
			return fmt.Errorf("crear el stock de %s de la ciudad %d: %w", r, cityID, normalize(err))
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// Lecturas de arranque
// ─────────────────────────────────────────────────────────────

// ListStocks lee todas las filas de city_stocks en orden estable.
func (r *EconomyRepo) ListStocks(ctx context.Context) ([]StockRow, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT city_id, resource, amount, version FROM city_stocks ORDER BY city_id, resource`)
	if err != nil {
		return nil, fmt.Errorf("leer stocks: %w", err)
	}
	defer rows.Close()
	var out []StockRow
	for rows.Next() {
		var row StockRow
		var res string
		if err := rows.Scan(&row.CityID, &res, &row.Stock.Amount, &row.Stock.Version); err != nil {
			return nil, err
		}
		row.Stock.Resource = economy.Resource(res)
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListTasks lee todas las tareas en orden de unidad.
func (r *EconomyRepo) ListTasks(ctx context.Context) ([]task.Task, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT unit_id, kind, target_x, target_y, phase, COALESCE(phase_until_ms, 0), carrying, trips_done
		   FROM unit_tasks ORDER BY unit_id`)
	if err != nil {
		return nil, fmt.Errorf("leer tareas: %w", err)
	}
	defer rows.Close()
	var out []task.Task
	for rows.Next() {
		var t task.Task
		var kind, phase string
		if err := rows.Scan(&t.UnitID, &kind, &t.Target.X, &t.Target.Y, &phase, &t.PhaseUntilMs,
			&t.Carrying, &t.TripsDone); err != nil {
			return nil, err
		}
		t.Kind, t.Phase = task.Kind(kind), task.Phase(phase)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListBuildings lee todos los edificios en orden de id.
func (r *EconomyRepo) ListBuildings(ctx context.Context) ([]building.Building, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id, city_id, type, x, y, status, completes_at_ms FROM buildings ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("leer edificios: %w", err)
	}
	defer rows.Close()
	var out []building.Building
	for rows.Next() {
		var b building.Building
		var tipo, estado string
		if err := rows.Scan(&b.ID, &b.CityID, &tipo, &b.X, &b.Y, &estado, &b.CompletesAtMs); err != nil {
			return nil, err
		}
		b.Type, b.Status = building.Type(tipo), building.Status(estado)
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListProductions lee todas las producciones, en curso y terminadas, por ciudad.
func (r *EconomyRepo) ListProductions(ctx context.Context) ([]production.Production, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT city_id, unit_type, completes_at_ms, request_id, COALESCE(unit_id, 0)
		   FROM city_productions ORDER BY city_id`)
	if err != nil {
		return nil, fmt.Errorf("leer producciones: %w", err)
	}
	defer rows.Close()
	var out []production.Production
	for rows.Next() {
		var p production.Production
		var tipo string
		if err := rows.Scan(&p.CityID, &tipo, &p.CompletesAtMs, &p.RequestID, &p.UnitID); err != nil {
			return nil, err
		}
		p.UnitType = unit.Type(tipo)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────
// Tareas
// ─────────────────────────────────────────────────────────────

// TaskTransition es una transición de tarea tal como se escribe.
type TaskTransition struct {
	UnitID int64
	// FinishMovementID y FinishStatus cierran el tramo que termina (0: ninguno).
	FinishMovementID int64
	FinishStatus     movement.Status
	// Start es el tramo que empieza, con su id ya asignado por el loop.
	Start *movement.Movement
	// WriteUnit, Status y At: el estado y el tile de la unidad tras la
	// transición. Sin WriteUnit sólo se escribe la tarea.
	WriteUnit bool
	Status    unit.Status
	At        world.Tile
	// Task es la tarea tal como queda; nil la borra.
	Task *task.Task
}

// PersistTask escribe una transición de tarea en una transacción: el cierre
// del tramo anterior, el tramo nuevo, la unidad y la tarea (ADR-023 §6). Es
// idempotente: cerrar un tramo ya cerrado no hace nada, el tramo nuevo cancela
// el activo antes de insertarse (y un reintento se encuentra su propio id), y
// la unidad y la tarea se escriben con su valor final.
func (r *EconomyRepo) PersistTask(ctx context.Context, tr TaskTransition) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		if tr.FinishMovementID != 0 {
			if err := r.movements.Finish(ctx, tx, tr.FinishMovementID, tr.FinishStatus); err != nil {
				return err
			}
		}
		if tr.Start != nil {
			var existe bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM unit_movements WHERE id = $1)`, tr.Start.ID).Scan(&existe); err != nil {
				return fmt.Errorf("comprobar el tramo %d: %w", tr.Start.ID, err)
			}
			if !existe {
				if _, _, err := r.movements.Start(ctx, tx, tr.Start); err != nil {
					return err
				}
			}
		}
		if tr.WriteUnit {
			if err := r.units.FlushPositions(ctx, tx, []PositionUpdate{
				{UnitID: tr.UnitID, X: tr.At.X, Y: tr.At.Y, Status: tr.Status},
			}); err != nil {
				return err
			}
		}
		if tr.Task == nil {
			return DeleteTask(ctx, tx, tr.UnitID)
		}
		return upsertTask(ctx, tx, *tr.Task)
	})
}

// DeleteTask borra la tarea de una unidad, si la tiene. La llaman las
// transacciones que cancelan un tramo (inicio y cierre de movimiento,
// guarnición): cancelar el tramo cancela la tarea (RN-ECON-003).
func DeleteTask(ctx context.Context, db DB, unitID int64) error {
	if _, err := db.Exec(ctx, `DELETE FROM unit_tasks WHERE unit_id = $1`, unitID); err != nil {
		return fmt.Errorf("borrar la tarea de la unidad %d: %w", unitID, normalize(err))
	}
	return nil
}

func upsertTask(ctx context.Context, db DB, t task.Task) error {
	var until *int64
	if t.Phase == task.PhaseWorking {
		v := t.PhaseUntilMs
		until = &v
	}
	_, err := db.Exec(ctx,
		`INSERT INTO unit_tasks (unit_id, kind, target_x, target_y, phase, phase_until_ms, carrying, trips_done)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (unit_id) DO UPDATE
		    SET kind = EXCLUDED.kind, target_x = EXCLUDED.target_x, target_y = EXCLUDED.target_y,
		        phase = EXCLUDED.phase, phase_until_ms = EXCLUDED.phase_until_ms,
		        carrying = EXCLUDED.carrying, trips_done = EXCLUDED.trips_done`,
		t.UnitID, string(t.Kind), t.Target.X, t.Target.Y, string(t.Phase), until, t.Carrying, t.TripsDone)
	if err != nil {
		return fmt.Errorf("escribir la tarea de la unidad %d: %w", t.UnitID, normalize(err))
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// Stock
// ─────────────────────────────────────────────────────────────

// applyStock aplica un delta condicionado a la versión. Devuelve si se aplicó:
// false si la base ya tenía esa versión o una posterior. Un delta que deje el
// stock negativo viola city_stocks_amount_non_negative (INV-ECON-001) y se
// devuelve como economy.ErrInsufficientResources.
//
// No es un único INSERT … ON CONFLICT DO UPDATE a propósito: PostgreSQL
// comprueba los CHECK sobre la fila PROPUESTA antes de resolver el conflicto,
// y la fila propuesta de un débito lleva el delta negativo como cantidad, así
// que todo débito violaría city_stocks_amount_non_negative aunque el stock lo
// cubriera.
func applyStock(ctx context.Context, db DB, d StockDelta) (bool, error) {
	insuficiente := func() error {
		return fmt.Errorf("%w: el stock de la ciudad %d no cubre %d", economy.ErrInsufficientResources, d.CityID, -d.Delta)
	}
	tag, err := db.Exec(ctx,
		`UPDATE city_stocks SET amount = amount + $3, version = $4
		  WHERE city_id = $1 AND resource = $2 AND version < $4`,
		d.CityID, string(d.Resource), d.Delta, d.Version)
	if err != nil {
		if IsCheckViolation(err) {
			return false, insuficiente()
		}
		return false, fmt.Errorf("escribir el stock de la ciudad %d: %w", d.CityID, normalize(err))
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}

	// Ninguna fila cambió: o la base ya tiene esta versión (un reintento, o
	// una escritura vieja), o la ciudad no tiene fila —una ciudad anterior a
	// 000006 que la migración no vio, o sembrada a mano—, que vale cero.
	var existe bool
	if err := db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM city_stocks WHERE city_id = $1 AND resource = $2)`,
		d.CityID, string(d.Resource)).Scan(&existe); err != nil {
		return false, fmt.Errorf("leer el stock de la ciudad %d: %w", d.CityID, normalize(err))
	}
	if existe {
		return false, nil
	}
	if d.Delta < 0 {
		return false, insuficiente()
	}
	tag, err = db.Exec(ctx,
		`INSERT INTO city_stocks (city_id, resource, amount, version) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (city_id, resource) DO NOTHING`,
		d.CityID, string(d.Resource), d.Delta, d.Version)
	if err != nil {
		return false, fmt.Errorf("crear el stock de la ciudad %d: %w", d.CityID, normalize(err))
	}
	return tag.RowsAffected() == 1, nil
}

// FlushStocks suma los depósitos pendientes de cada ciudad. Un delta cuya
// versión ya está escrita no se aplica otra vez: es un reintento.
func (r *EconomyRepo) FlushStocks(ctx context.Context, deltas []StockDelta) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		for _, d := range deltas {
			if _, err := applyStock(ctx, tx, d); err != nil {
				return err
			}
		}
		return nil
	})
}

// ─────────────────────────────────────────────────────────────
// Encargos
// ─────────────────────────────────────────────────────────────

// Build debita el coste y encarga el edificio en UNA transacción
// (RN-ECON-008, INV-ECON-002), y devuelve su id. Idempotente por
// (city_id, request_id): un reintento encuentra su fila y no debita otra vez.
func (r *EconomyRepo) Build(
	ctx context.Context, requestID string, playerID uuid.UUID, b building.Building, d StockDelta, tick uint64,
) (int64, error) {
	var id int64
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		switch err := tx.QueryRow(ctx,
			`SELECT id FROM buildings WHERE city_id = $1 AND request_id = $2`, b.CityID, requestID).Scan(&id); {
		case err == nil:
			return nil // ya escrito: un reintento
		case !errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("buscar el encargo %s: %w", requestID, normalize(err))
		}

		aplicado, err := applyStock(ctx, tx, d)
		if err != nil {
			return err
		}
		if !aplicado {
			return ErrStaleStock
		}
		err = tx.QueryRow(ctx,
			`INSERT INTO buildings (city_id, type, x, y, status, completes_at_ms, request_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			b.CityID, string(b.Type), b.X, b.Y, string(building.StatusUnderConstruction), b.CompletesAtMs, requestID,
		).Scan(&id)
		if err != nil {
			if IsUniqueViolation(err) {
				return fmt.Errorf("%w: (%d,%d) ya tiene un edificio", building.ErrInvalidSite, b.X, b.Y)
			}
			return fmt.Errorf("insertar el edificio: %w", normalize(err))
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventBuildingOrdered, Tick: tick, PlayerID: &playerID,
			Payload: map[string]any{
				"buildingId": id, "cityId": b.CityID, "type": string(b.Type),
				"x": b.X, "y": b.Y, "requestId": requestID,
			},
		})
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// Train debita el coste, reserva la plaza de población y encarga el
// entrenamiento en UNA transacción (RN-ECON-010). Idempotente por el
// request_id de la fila de la producción.
func (r *EconomyRepo) Train(
	ctx context.Context, requestID string, playerID uuid.UUID, p production.Production,
	popCost int32, d StockDelta, tick uint64,
) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		var previa string
		var nacida *int64
		switch err := tx.QueryRow(ctx,
			`SELECT request_id, unit_id FROM city_productions WHERE city_id = $1 FOR UPDATE`,
			p.CityID).Scan(&previa, &nacida); {
		case err == nil:
			if previa == requestID {
				return nil // ya escrito: un reintento
			}
			if nacida == nil {
				return production.ErrCityBusy
			}
			// La producción anterior terminó: su fila sólo esperaba a ésta.
			if _, err := tx.Exec(ctx, `DELETE FROM city_productions WHERE city_id = $1`, p.CityID); err != nil {
				return fmt.Errorf("retirar la producción terminada: %w", normalize(err))
			}
		case !errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("leer la producción de la ciudad %d: %w", p.CityID, normalize(err))
		}

		aplicado, err := applyStock(ctx, tx, d)
		if err != nil {
			return err
		}
		if !aplicado {
			return ErrStaleStock
		}
		// RN-ECON-010: la plaza se reserva en la misma transacción. El CHECK
		// cities_population_within_limit es la última barrera.
		if _, err := tx.Exec(ctx,
			`UPDATE cities SET population = population + $2, version = version + 1 WHERE id = $1`,
			p.CityID, popCost); err != nil {
			if IsCheckViolation(err) {
				return fmt.Errorf("%w: la ciudad %d", city.ErrPopulationLimit, p.CityID)
			}
			return fmt.Errorf("reservar la población de la ciudad %d: %w", p.CityID, normalize(err))
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO city_productions (city_id, unit_type, completes_at_ms, request_id) VALUES ($1, $2, $3, $4)`,
			p.CityID, string(p.UnitType), p.CompletesAtMs, requestID); err != nil {
			return fmt.Errorf("insertar la producción: %w", normalize(err))
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventTrainingOrdered, Tick: tick, PlayerID: &playerID,
			Payload: map[string]any{"cityId": p.CityID, "unitType": string(p.UnitType), "requestId": requestID},
		})
	})
}

// CompleteBuilding marca el edificio COMPLETE y suma su bono al límite de
// población de su ciudad, juntos (RN-ECON-009). Sólo si seguía en obras: un
// reintento no suma dos veces (INV-ECON-004).
func (r *EconomyRepo) CompleteBuilding(
	ctx context.Context, buildingID, cityID int64, playerID uuid.UUID, bonus int32, tick uint64,
) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE buildings SET status = 'COMPLETE' WHERE id = $1 AND status = 'UNDER_CONSTRUCTION'`, buildingID)
		if err != nil {
			return fmt.Errorf("completar el edificio %d: %w", buildingID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx,
			`UPDATE cities SET population_limit = population_limit + $2, version = version + 1 WHERE id = $1`,
			cityID, bonus); err != nil {
			return fmt.Errorf("subir el límite de la ciudad %d: %w", cityID, normalize(err))
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventBuildingCompleted, Tick: tick, PlayerID: &playerID,
			Payload: map[string]any{"buildingId": buildingID, "cityId": cityID},
		})
	})
}

// BirthUnit inserta la unidad de la producción en curso de una ciudad y apunta
// su id en la producción, en una transacción (ADR-023 §3). La población no
// cambia: la plaza estaba reservada (INV-CITY-017). Idempotente: si la
// producción ya tiene unidad, la devuelve.
func (r *EconomyRepo) BirthUnit(
	ctx context.Context, cityID int64, playerID uuid.UUID, t unit.Type, at world.Tile, hp int32, tick uint64,
) (int64, error) {
	var id int64
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		var nacida *int64
		if err := tx.QueryRow(ctx,
			`SELECT unit_id FROM city_productions WHERE city_id = $1 FOR UPDATE`, cityID).Scan(&nacida); err != nil {
			if errors.Is(normalize(err), ErrNotFound) {
				return fmt.Errorf("%w: ciudad %d", ErrNoProduction, cityID)
			}
			return fmt.Errorf("leer la producción de la ciudad %d: %w", cityID, normalize(err))
		}
		if nacida != nil {
			id = *nacida
			return nil
		}
		ciudad := cityID
		u := &unit.Unit{PlayerID: playerID, CityID: &ciudad, Type: t, X: at.X, Y: at.Y, HP: hp, MaxHP: hp,
			Status: unit.StatusIdle}
		var err error
		if id, err = r.units.Create(ctx, tx, u); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE city_productions SET unit_id = $2 WHERE city_id = $1`, cityID, id); err != nil {
			return fmt.Errorf("apuntar el aldeano en la producción: %w", normalize(err))
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventUnitTrained, Tick: tick, PlayerID: &playerID,
			Payload: map[string]any{"cityId": cityID, "unitId": id},
		})
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}
