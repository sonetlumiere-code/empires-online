package persistence

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// FactionStore implementa simulation.FactionStore sobre PostgreSQL (ADR-039).
type FactionStore struct {
	factions *postgres.FactionRepo
	metrics  *observability.Metrics
	log      *slog.Logger
}

// NewFactionStore construye el adaptador. metrics y log son opcionales.
func NewFactionStore(f *postgres.FactionRepo, metrics *observability.Metrics, log *slog.Logger) *FactionStore {
	if log == nil {
		log = slog.Default()
	}
	return &FactionStore{factions: f, metrics: metrics, log: log}
}

// RequestJoin apunta la entrada. Un jugador que ya no es NEUTRAL o que ya tiene
// otra entrada es un rechazo de dominio: simulation.ErrFactionConflict.
func (s *FactionStore) RequestJoin(ctx context.Context, o simulation.FactionJoinOrder) error {
	err := s.factions.RequestJoin(ctx, postgres.FactionJoinOrder{
		PlayerID: o.PlayerID, RequestID: o.RequestID, Faction: o.Faction, At: o.At, Tick: o.Tick, AtMs: o.AtMs,
	})
	if errors.Is(err, postgres.ErrConflict) {
		return fmt.Errorf("%w: %v", simulation.ErrFactionConflict, err)
	}
	return err
}

// CompleteJoin escribe el vencimiento. Un conflicto —la base no tiene la entrada
// que la RAM venció— no se arregla reintentando: se registra y se da por
// terminado, y la carga siguiente leerá lo que diga la base.
func (s *FactionStore) CompleteJoin(ctx context.Context, c simulation.FactionJoinCompletion) error {
	breaks := make([]postgres.TreatyWrite, 0, len(c.Breaks))
	for _, w := range c.Breaks {
		breaks = append(breaks, postgres.TreatyWrite{
			Treaty: w.Treaty, From: w.From, Event: w.Event, Tick: w.Tick, AtMs: w.AtMs, By: w.By,
		})
	}
	applied, err := s.factions.CompleteJoin(ctx, postgres.FactionJoinCompletion{
		PlayerID: c.PlayerID, Faction: c.Faction, At: c.At, Breaks: breaks, Tick: c.Tick, AtMs: c.AtMs,
	})
	if errors.Is(err, postgres.ErrConflict) {
		s.log.Error("invariant_violation", "inv_id", "INV-PLAYER-013", "severity", "ALTO", "policy", "REJECT",
			"entity", "player:"+c.PlayerID.String(), "detail", err.Error())
		return nil
	}
	if err != nil {
		return err
	}
	if applied && s.metrics != nil {
		s.metrics.FactionJoins.WithLabelValues(c.Faction).Inc()
	}
	return nil
}
