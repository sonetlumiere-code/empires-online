// Package persistence conecta la simulación con el almacenamiento durable.
//
// La simulación declara la interfaz que necesita (simulation.Repositories) y este
// paquete la implementa sobre PostgreSQL. La dependencia apunta hacia adentro: el
// dominio nunca importa este paquete.
package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// GameStore implementa simulation.Repositories sobre PostgreSQL.
type GameStore struct {
	store     *postgres.Store
	units     *postgres.UnitRepo
	cities    *postgres.CityRepo
	movements *postgres.MovementRepo
}

// NewGameStore construye el adaptador.
func NewGameStore(s *postgres.Store, u *postgres.UnitRepo, c *postgres.CityRepo, m *postgres.MovementRepo) *GameStore {
	return &GameStore{store: s, units: u, cities: c, movements: m}
}

// PersistMovementStart escribe el inicio de un movimiento.
//
// Cancelar el movimiento anterior, crear el nuevo y dejar la unidad MOVING en su
// origen van en la MISMA transacción: entre una operación y otra no puede existir
// un instante con dos movimientos activos de la misma unidad (INV-MOVE-001), ni
// una unidad MOVING sin movimiento que la respalde (ADR-013 §2.3).
func (g *GameStore) PersistMovementStart(ctx context.Context, m movement.Movement, origin world.Tile) error {
	return g.store.InTx(ctx, func(tx pgxTx) error {
		if _, _, err := g.movements.Start(ctx, tx, &m); err != nil {
			return err
		}
		return g.units.FlushPositions(ctx, tx, []postgres.PositionUpdate{
			{UnitID: m.UnitID, X: origin.X, Y: origin.Y, Status: unit.StatusMoving},
		})
	})
}

// PersistMovementFinish cierra un movimiento y deja su unidad IDLE en el tile
// final, en una transacción (ADR-013 §2.3). Sin esto, la posición final llegaba
// con el siguiente volcado periódico, y una caída en esos 5 s dejaba en disco el
// movimiento cerrado y la unidad a medio camino.
//
// Es idempotente: el cierre puede llegar dos veces (por el tick y por la
// recuperación) y la segunda no cambia nada.
func (g *GameStore) PersistMovementFinish(ctx context.Context, f simulation.MovementFinish) error {
	return g.store.InTx(ctx, func(tx pgxTx) error {
		if f.MovementID != 0 {
			if err := g.movements.Finish(ctx, tx, f.MovementID, f.Status); err != nil {
				return err
			}
		}
		if f.UnitID == 0 {
			return nil
		}
		return g.units.FlushPositions(ctx, tx, []postgres.PositionUpdate{
			{UnitID: f.UnitID, X: f.At.X, Y: f.At.Y, Status: unit.StatusIdle},
		})
	})
}

// PersistUnitPositions vuelca un lote de posiciones consolidadas.
func (g *GameStore) PersistUnitPositions(ctx context.Context, units []*unit.Unit) error {
	if len(units) == 0 {
		return nil
	}
	updates := make([]postgres.PositionUpdate, len(units))
	for i, u := range units {
		updates[i] = postgres.PositionUpdate{UnitID: u.ID, X: u.X, Y: u.Y, Status: u.Status}
	}
	return g.units.FlushPositions(ctx, g.store.Pool(), updates)
}

// PersistCityPresence escribe una transición de presencia.
func (g *GameStore) PersistCityPresence(ctx context.Context, cityID int64, state city.PresenceState, at time.Time) error {
	return g.cities.SetPresence(ctx, g.store.Pool(), cityID, state, at)
}

// FinishRecoveredMovements cierra en bloque los movimientos que la recuperación
// determinó que ya no siguen activos.
func (g *GameStore) FinishRecoveredMovements(ctx context.Context, finished []simulation.FinishedMovement) error {
	for _, f := range finished {
		err := g.PersistMovementFinish(ctx, simulation.MovementFinish{
			MovementID: f.MovementID, Status: f.Status, UnitID: f.UnitID, At: f.At,
		})
		if err != nil {
			return fmt.Errorf("cerrar el movimiento recuperado %d: %w", f.MovementID, err)
		}
	}
	return nil
}

// Comprobación en tiempo de compilación del contrato con la simulación.
var _ simulation.Repositories = (*GameStore)(nil)
