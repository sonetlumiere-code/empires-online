package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// ConquestStore implementa simulation.ConquestStore sobre postgres.ConquestRepo
// (ADR-034): traduce los tipos de la simulación a los de la base y los rechazos
// de la base a los errores que la simulación conoce.
type ConquestStore struct {
	repo *postgres.ConquestRepo
}

// NewConquestStore construye el adaptador.
func NewConquestStore(r *postgres.ConquestRepo) *ConquestStore { return &ConquestStore{repo: r} }

// StartClaim escribe el comienzo de una reclamación.
func (s *ConquestStore) StartClaim(ctx context.Context, c simulation.ClaimStart) (int64, error) {
	id, err := s.repo.StartClaim(ctx, postgres.ClaimStart{Claim: c.Claim, TerritoryName: c.TerritoryName, Tick: c.Tick})
	return id, claimConflict(err)
}

// EndClaim escribe el fin sin captura de una reclamación.
func (s *ConquestStore) EndClaim(ctx context.Context, e simulation.ClaimEnd) error {
	return s.repo.EndClaim(ctx, postgres.ClaimEnd{ClaimID: e.ClaimID, TerritoryID: e.TerritoryID, Reason: e.Reason})
}

// CaptureTerritory escribe una captura.
func (s *ConquestStore) CaptureTerritory(ctx context.Context, c simulation.Capture) (territory.Control, error) {
	ctrl, err := s.repo.CaptureTerritory(ctx, postgres.Capture{
		ClaimID: c.ClaimID, TerritoryID: c.TerritoryID, TerritoryName: c.TerritoryName,
		NewOwner: c.NewOwner, NewCityID: c.NewCityID, NewBonus: c.NewBonus,
		PrevOwner: c.PrevOwner, PrevCityID: c.PrevCityID, PrevBonus: c.PrevBonus,
		ExpectedVersion: c.ExpectedVersion, CapturedAt: c.CapturedAt, Tick: c.Tick, AtMs: c.AtMs, X: c.X, Y: c.Y,
	})
	return ctrl, claimConflict(err)
}

func claimConflict(err error) error {
	if errors.Is(err, postgres.ErrConflict) {
		return fmt.Errorf("%w: %v", simulation.ErrClaimConflict, err)
	}
	return err
}

// Comprobación del contrato con la simulación.
var _ simulation.ConquestStore = (*ConquestStore)(nil)
