package simulation_test

import (
	"context"
	"sync"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
)

// fakeEconomy registra las escrituras de la economía sin base de datos. Cada
// error configurable simula un rechazo de la transacción o un fallo de la base.
type fakeEconomy struct {
	mu          sync.Mutex
	tasks       []simulation.TaskWrite
	flushes     []simulation.StockDelta
	builds      []simulation.BuildOrder
	trains      []simulation.TrainOrder
	completions []simulation.BuildingCompletion
	destroys    []simulation.BuildingDestruction
	births      []simulation.Birth
	advances    []simulation.AdvanceOrder
	advanceEnds []simulation.AdvanceCompletion
	crafts      []simulation.CraftOrder
	deliveries  []simulation.CraftDelivery

	buildErr error
	trainErr error
	birthErr error
	// advanceErr hace fallar el encargo del avance; advanceEndErr, su fin.
	advanceErr    error
	advanceEndErr error
	// craftErr hace fallar el encargo de una elaboración; deliverErr, su
	// entrega (ADR-036 §4).
	craftErr   error
	deliverErr error
	// destroyErr hace fallar la escritura de una destrucción (ADR-029 §4).
	destroyErr error
	flushErr   error
	taskErr    error

	nextBuildingID int64
	nextUnitID     int64
}

func newFakeEconomy() *fakeEconomy { return &fakeEconomy{nextBuildingID: 500, nextUnitID: 900} }

func (f *fakeEconomy) PersistTask(_ context.Context, w simulation.TaskWrite) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.taskErr != nil {
		return f.taskErr
	}
	f.tasks = append(f.tasks, w)
	return nil
}

func (f *fakeEconomy) FlushStocks(_ context.Context, deltas []simulation.StockDelta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.flushErr != nil {
		return f.flushErr
	}
	f.flushes = append(f.flushes, deltas...)
	return nil
}

func (f *fakeEconomy) Build(_ context.Context, o simulation.BuildOrder) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buildErr != nil {
		return 0, f.buildErr
	}
	f.builds = append(f.builds, o)
	f.nextBuildingID++
	return f.nextBuildingID, nil
}

func (f *fakeEconomy) Train(_ context.Context, o simulation.TrainOrder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.trainErr != nil {
		return f.trainErr
	}
	f.trains = append(f.trains, o)
	return nil
}

func (f *fakeEconomy) CompleteBuilding(_ context.Context, c simulation.BuildingCompletion) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completions = append(f.completions, c)
	return nil
}

func (f *fakeEconomy) DestroyBuilding(_ context.Context, d simulation.BuildingDestruction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.destroyErr != nil {
		return f.destroyErr
	}
	f.destroys = append(f.destroys, d)
	return nil
}

func (f *fakeEconomy) BirthUnit(_ context.Context, b simulation.Birth) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.birthErr != nil {
		return 0, f.birthErr
	}
	f.births = append(f.births, b)
	f.nextUnitID++
	return f.nextUnitID, nil
}

func (f *fakeEconomy) lastTask() simulation.TaskWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tasks[len(f.tasks)-1]
}

func (f *fakeEconomy) taskWrites() []simulation.TaskWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]simulation.TaskWrite(nil), f.tasks...)
}

func (f *fakeEconomy) Advance(_ context.Context, o simulation.AdvanceOrder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.advanceErr != nil {
		return f.advanceErr
	}
	f.advances = append(f.advances, o)
	return nil
}

func (f *fakeEconomy) CompleteAdvance(_ context.Context, c simulation.AdvanceCompletion) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.advanceEndErr != nil {
		return f.advanceEndErr
	}
	f.advanceEnds = append(f.advanceEnds, c)
	return nil
}

func (f *fakeEconomy) Craft(_ context.Context, o simulation.CraftOrder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.craftErr != nil {
		return f.craftErr
	}
	f.crafts = append(f.crafts, o)
	return nil
}

func (f *fakeEconomy) CompleteCraft(_ context.Context, d simulation.CraftDelivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deliverErr != nil {
		return f.deliverErr
	}
	f.deliveries = append(f.deliveries, d)
	return nil
}

func (f *fakeEconomy) craftDeliveries() []simulation.CraftDelivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]simulation.CraftDelivery(nil), f.deliveries...)
}
