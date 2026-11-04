package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// TreatyStore implementa simulation.TreatyStore sobre PostgreSQL (ADR-027).
type TreatyStore struct {
	treaties *postgres.TreatyRepo
}

// NewTreatyStore construye el adaptador.
func NewTreatyStore(t *postgres.TreatyRepo) *TreatyStore { return &TreatyStore{treaties: t} }

// ApplyTreaty escribe una orden o un vencimiento. Un índice de INV-DIPLO-002
// violado, o una fila en un estado distinto del esperado, es un rechazo de
// dominio: simulation.ErrTreatyConflict.
func (s *TreatyStore) ApplyTreaty(ctx context.Context, w simulation.TreatyWrite) (int64, error) {
	id, err := s.treaties.ApplyTreaty(ctx, postgres.TreatyWrite{
		Insert: w.Insert, Treaty: w.Treaty, From: w.From, Supersede: w.Supersede, Event: w.Event, Tick: w.Tick,
		AtMs: w.AtMs, By: w.By,
	})
	if errors.Is(err, postgres.ErrConflict) || errors.Is(err, postgres.ErrVersionConflict) {
		return 0, fmt.Errorf("%w: %v", simulation.ErrTreatyConflict, err)
	}
	return id, err
}
