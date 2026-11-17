package persistence

import (
	"context"
	"errors"
	"log/slog"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
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

// Join abre la estancia del invitado y los preavisos de las alianzas que su
// entrada mezcla con la Orden y el Caos (ADR-042 §4).
func (s *ClanStore) Join(ctx context.Context, o simulation.ClanJoinOrder) ([]simulation.AllianceBreak, error) {
	breaks, err := s.clans.JoinBreaking(ctx, postgres.ClanJoin{
		ClanID: o.ClanID, PlayerID: o.PlayerID, MaxMembers: o.MaxMembers, BreakNotice: o.BreakNotice,
		At: o.At, Tick: o.Tick, AtMs: o.AtMs,
	})
	if err != nil {
		return nil, rejection(err)
	}
	s.count(observability.ClanChangeJoined)
	out := make([]simulation.AllianceBreak, 0, len(breaks))
	for _, b := range breaks {
		out = append(out, simulation.AllianceBreak{AllianceID: b.AllianceID, BreakEffectiveAtMs: b.BreakEffectiveAt.UnixMilli()})
	}
	return out, nil
}

// Leave cierra la estancia. Es tolerante: un reintento no escribe dos veces.
func (s *ClanStore) Leave(ctx context.Context, l simulation.ClanLeave) ([]territory.Control, error) {
	applied, released, err := s.clans.Leave(ctx, postgres.ClanLeave{
		ClanID: l.ClanID, PlayerID: l.PlayerID, Reason: l.Reason, By: l.By, At: l.At,
		NewLeader: l.NewLeader, Dissolve: l.Dissolve, WarEnds: warEnds(l.WarEnds), Tick: l.Tick, AtMs: l.AtMs,
	})
	if err != nil || !applied {
		return nil, err
	}
	if l.Reason == clan.ReasonExpelled {
		s.count(observability.ClanChangeExpelled)
	} else {
		s.count(observability.ClanChangeLeft)
	}
	if l.Dissolve {
		s.count(observability.ClanChangeDissolved)
		for range l.WarEnds {
			s.count(observability.ClanChangeWarEnded)
		}
	}
	return released, nil
}

// SetRank nombra o quita un oficial (SPEC-CLAN-GOVERNANCE §4.3).
func (s *ClanStore) SetRank(ctx context.Context, o simulation.ClanRankOrder) error {
	err := s.clans.SetRank(ctx, postgres.ClanRank{
		ClanID: o.ClanID, PlayerID: o.PlayerID, By: o.By, Officer: o.Officer, At: o.At, Tick: o.Tick, AtMs: o.AtMs,
	})
	if err != nil {
		return rejection(err)
	}
	if o.Officer {
		s.count(observability.ClanChangePromoted)
	} else {
		s.count(observability.ClanChangeDemoted)
	}
	return nil
}

// ChangeLeader pasa el liderazgo (SPEC-CLAN-GOVERNANCE §4.3, §4.4).
func (s *ClanStore) ChangeLeader(ctx context.Context, o simulation.ClanLeaderOrder) error {
	err := s.clans.ChangeLeader(ctx, postgres.ClanLeaderChange{
		ClanID: o.ClanID, Tag: o.Tag, From: o.From, To: o.To, Reason: o.Reason, By: o.By,
		At: o.At, Tick: o.Tick, AtMs: o.AtMs,
	})
	if err != nil {
		return rejection(err)
	}
	if o.Reason == eventlog.LeaderReasonInactive {
		s.count(observability.ClanChangeSucceeded)
	} else {
		s.count(observability.ClanChangeTransferred)
	}
	return nil
}

// ApplyAlliance escribe una orden o un vencimiento de una alianza entre clanes
// (SPEC-CLAN-ALLIANCES, ADR-042 §3).
func (s *ClanStore) ApplyAlliance(ctx context.Context, w simulation.AllianceWrite) (int64, error) {
	id, err := s.clans.ApplyAlliance(ctx, allianceWrite(w))
	if err != nil {
		return 0, rejection(err)
	}
	switch {
	case w.Insert:
		s.count(observability.ClanChangeAllianceProposed)
	case w.Event == eventlog.EventClanAllianceAccepted:
		s.count(observability.ClanChangeAllianceAccepted)
	case w.Event == eventlog.EventClanAllianceBroken:
		s.count(observability.ClanChangeAllianceBroken)
	case w.Event == eventlog.EventClanAllianceExpired:
		s.count(observability.ClanChangeAllianceExpired)
	}
	return id, nil
}

func allianceWrite(w simulation.AllianceWrite) postgres.AllianceWrite {
	return postgres.AllianceWrite{
		Insert: w.Insert, Alliance: w.Alliance, From: w.From, Event: w.Event, By: w.By, ByClan: w.ByClan,
		MaxAllies: w.MaxAllies, Tolerant: w.Tolerant, Tick: w.Tick, AtMs: w.AtMs,
	}
}

func allianceWrites(ws []simulation.AllianceWrite) []postgres.AllianceWrite {
	out := make([]postgres.AllianceWrite, 0, len(ws))
	for _, w := range ws {
		out = append(out, allianceWrite(w))
	}
	return out
}

// ApplyWar escribe una orden o un vencimiento de una guerra entre clanes
// (SPEC-CLAN-WARS, ADR-045 §2).
func (s *ClanStore) ApplyWar(ctx context.Context, w simulation.WarWrite) (int64, error) {
	id, err := s.clans.ApplyWar(ctx, postgres.WarWrite{
		Insert: w.Insert, War: w.War, From: w.From, Event: w.Event, By: w.By, ByClan: w.ByClan,
		MaxWars: w.MaxWars, Cooldown: w.Cooldown, Tolerant: w.Tolerant, Tick: w.Tick, AtMs: w.AtMs,
	})
	if err != nil {
		return 0, rejection(err)
	}
	switch {
	case w.Insert:
		s.count(observability.ClanChangeWarDeclared)
	case w.Event == eventlog.EventClanWarEnded:
		s.count(observability.ClanChangeWarEnded)
	}
	return id, nil
}

func warEnds(ws []clan.War) []postgres.WarEnd {
	out := make([]postgres.WarEnd, 0, len(ws))
	for _, w := range ws {
		out = append(out, postgres.WarEnd{War: w})
	}
	return out
}
