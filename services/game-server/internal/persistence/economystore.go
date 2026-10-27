package persistence

import (
	"context"
	"errors"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// EconomyStore implementa simulation.EconomyStore sobre postgres.EconomyRepo:
// traduce los tipos de la simulación a los de la base, y el rechazo por stock
// adelantado al error que la simulación conoce. El repositorio no importa la
// simulación, igual que GarrisonRepo.
type EconomyStore struct {
	repo *postgres.EconomyRepo
}

// NewEconomyStore construye el adaptador.
func NewEconomyStore(r *postgres.EconomyRepo) *EconomyStore { return &EconomyStore{repo: r} }

// PersistTask escribe una transición de tarea (ADR-023 §6).
func (e *EconomyStore) PersistTask(ctx context.Context, w simulation.TaskWrite) error {
	return e.repo.PersistTask(ctx, toTransition(w))
}

func toTransition(w simulation.TaskWrite) postgres.TaskTransition {
	tr := postgres.TaskTransition{
		UnitID: w.UnitID, Start: w.Start, Task: w.Task,
		WriteUnit: !w.KeepUnit, Status: w.Status, At: w.At,
	}
	if w.Finish != nil {
		tr.FinishMovementID, tr.FinishStatus = w.Finish.MovementID, w.Finish.Status
	}
	return tr
}

// FlushStocks suma los depósitos pendientes.
func (e *EconomyStore) FlushStocks(ctx context.Context, deltas []simulation.StockDelta) error {
	out := make([]postgres.StockDelta, len(deltas))
	for i, d := range deltas {
		out[i] = toDelta(d)
	}
	return e.repo.FlushStocks(ctx, out)
}

func toDelta(d simulation.StockDelta) postgres.StockDelta {
	return postgres.StockDelta{CityID: d.CityID, Resource: d.Resource, Delta: d.Delta, Version: d.Version}
}

// stale traduce el rechazo por stock adelantado.
func stale(err error) error {
	if errors.Is(err, postgres.ErrStaleStock) {
		return simulation.ErrStaleStock
	}
	return err
}

// Build debita y encarga el edificio (RN-ECON-008).
func (e *EconomyStore) Build(ctx context.Context, o simulation.BuildOrder) (int64, error) {
	id, err := e.repo.Build(ctx, o.RequestID, o.PlayerID, o.Building, toDelta(o.Stock), o.Tick)
	return id, stale(err)
}

// Train debita, reserva y encarga el entrenamiento (RN-ECON-010).
func (e *EconomyStore) Train(ctx context.Context, o simulation.TrainOrder) error {
	return stale(e.repo.Train(ctx, o.RequestID, o.PlayerID, o.Production, o.PopulationCost, toDelta(o.Stock), o.Tick))
}

// CompleteBuilding completa el edificio y sube el límite (RN-ECON-009).
func (e *EconomyStore) CompleteBuilding(ctx context.Context, c simulation.BuildingCompletion) error {
	return e.repo.CompleteBuilding(ctx, c.BuildingID, c.CityID, c.PlayerID, c.Bonus, c.Tick)
}

// BirthUnit inserta el aldeano entrenado (ADR-023 §3).
func (e *EconomyStore) BirthUnit(ctx context.Context, b simulation.Birth) (int64, error) {
	return e.repo.BirthUnit(ctx, b.CityID, b.PlayerID, b.Type, b.At, b.HP, b.Tick)
}

// FinishRecoveredTasks escribe, al arrancar, las tareas que la recuperación
// terminó (simulation.ReconcileTasks).
func (e *EconomyStore) FinishRecoveredTasks(ctx context.Context, writes []simulation.TaskWrite) error {
	for _, w := range writes {
		if err := e.PersistTask(ctx, w); err != nil {
			return err
		}
	}
	return nil
}

// Comprobación en tiempo de compilación del contrato con la simulación.
var _ simulation.EconomyStore = (*EconomyStore)(nil)
