package persistence

import (
	"context"
	"errors"
	"log/slog"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// ClanStore implementa simulation.ClanStore sobre PostgreSQL (ADR-040).
type ClanStore struct {
	clans   *postgres.ClanRepo
	metrics *observability.Metrics
	log     *slog.Logger
}

// NewClanStore construye el adaptador. metrics y log son opcionales.
func NewClanStore(c *postgres.ClanRepo, metrics *observability.Metrics, log *slog.Logger) *ClanStore {
	if log == nil {
		log = slog.Default()
	}
	return &ClanStore{clans: c, metrics: metrics, log: log}
}

// rejection traduce un rechazo de la base al de la simulación, con su motivo.
func rejection(err error) error {
	var r postgres.ClanRejected
	if errors.As(err, &r) {
		return simulation.ClanRejection{Reason: r.Reason}
	}
	return err
}

func (s *ClanStore) count(kind string) {
	if s.metrics != nil {
		s.metrics.ClanChanges.WithLabelValues(kind).Inc()
	}
}

// Create funda el clan.
func (s *ClanStore) Create(ctx context.Context, o simulation.ClanCreateOrder) (int64, error) {
	id, err := s.clans.Create(ctx, postgres.ClanCreate{
		PlayerID: o.PlayerID, Name: o.Name, Tag: o.Tag, At: o.At, Tick: o.Tick, AtMs: o.AtMs,
	})
	if err != nil {
		return 0, rejection(err)
	}
	s.count(observability.ClanChangeFounded)
	return id, nil
}

// Invite escribe o borra una invitación.
func (s *ClanStore) Invite(ctx context.Context, o simulation.ClanInviteOrder) error {
	return rejection(s.clans.Invite(ctx, postgres.ClanInvite{
		ClanID: o.ClanID, PlayerID: o.PlayerID, InvitedBy: o.InvitedBy, Remove: o.Remove,
		At: o.At, ExpiresAt: o.ExpiresAt,
	}))
}

// Join abre la estancia del invitado.
func (s *ClanStore) Join(ctx context.Context, o simulation.ClanJoinOrder) error {
	err := s.clans.Join(ctx, postgres.ClanJoin{
		ClanID: o.ClanID, PlayerID: o.PlayerID, MaxMembers: o.MaxMembers, At: o.At, Tick: o.Tick, AtMs: o.AtMs,
	})
	if err != nil {
		return rejection(err)
	}
	s.count(observability.ClanChangeJoined)
	return nil
}

// Leave cierra la estancia. Es tolerante: un reintento no escribe dos veces.
func (s *ClanStore) Leave(ctx context.Context, l simulation.ClanLeave) error {
	applied, err := s.clans.Leave(ctx, postgres.ClanLeave{
		ClanID: l.ClanID, PlayerID: l.PlayerID, Reason: l.Reason, By: l.By, At: l.At,
		NewLeader: l.NewLeader, Dissolve: l.Dissolve, Tick: l.Tick, AtMs: l.AtMs,
	})
	if err != nil || !applied {
		return err
	}
	if l.Reason == clan.ReasonExpelled {
		s.count(observability.ClanChangeExpelled)
	} else {
		s.count(observability.ClanChangeLeft)
	}
	if l.Dissolve {
		s.count(observability.ClanChangeDissolved)
	}
	return nil
}
