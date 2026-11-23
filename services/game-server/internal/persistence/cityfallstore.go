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

func fallConflict(err error) error {
	if errors.Is(err, postgres.ErrConflict) {
		return fmt.Errorf("%w: %v", simulation.ErrFallConflict, err)
	}
	return err
}

// Comprobación del contrato con la simulación.
var _ simulation.CityFallStore = (*CityFallStore)(nil)
