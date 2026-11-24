package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// CityFallStore implementa simulation.CityFallStore sobre postgres.CityFallRepo
// (ADR-050): traduce los tipos de la simulación a los de la base y los rechazos
// de la base a los errores que la simulación conoce.
type CityFallStore struct {
	repo *postgres.CityFallRepo
}

// NewCityFallStore construye el adaptador.
func NewCityFallStore(r *postgres.CityFallRepo) *CityFallStore { return &CityFallStore{repo: r} }

// StartAssault escribe el comienzo de un asalto.
func (s *CityFallStore) StartAssault(ctx context.Context, a simulation.AssaultStart) (int64, error) {
	id, err := s.repo.StartAssault(ctx, postgres.AssaultStart{Assault: a.Assault, Owner: a.Owner, Tick: a.Tick})
	return id, fallConflict(err)
}

// EndAssault escribe el fin sin caída de un asalto.
func (s *CityFallStore) EndAssault(ctx context.Context, e simulation.AssaultEnd) error {
	return s.repo.EndAssault(ctx, postgres.AssaultEnd{AssaultID: e.AssaultID, Reason: e.Reason})
}

// FallCity escribe una caída con su refundación.
func (s *CityFallStore) FallCity(ctx context.Context, f cityfall.Fall) (*cityfall.FallResult, error) {
	res, err := s.repo.FallCity(ctx, f)
	return res, fallConflict(err)
}

// FoundCity escribe la fundación de una segunda ciudad (SPEC-MULTI-CITY).
func (s *CityFallStore) FoundCity(ctx context.Context, w simulation.CityFoundWrite) (*cityfall.FoundingResult, error) {
	stock := make([]postgres.StockDelta, 0, len(w.Stock))
	for _, d := range w.Stock {
		stock = append(stock, postgres.StockDelta{CityID: d.CityID, Resource: d.Resource, Delta: d.Delta, Version: d.Version})
	}
	res, err := s.repo.FoundCity(ctx, w.Founding, stock)
	if errors.Is(err, postgres.ErrStaleStock) {
		return nil, fmt.Errorf("%w: %v", simulation.ErrStaleStock, err)
	}
	return res, err
}

func fallConflict(err error) error {
	if errors.Is(err, postgres.ErrConflict) {
		return fmt.Errorf("%w: %v", simulation.ErrFallConflict, err)
	}
	return err
}

// Comprobación del contrato con la simulación.
var _ simulation.CityFallStore = (*CityFallStore)(nil)
