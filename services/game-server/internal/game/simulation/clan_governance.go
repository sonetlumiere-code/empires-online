package simulation

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El gobierno del clan (SPEC-CLAN-GOVERNANCE).
//
// Los rangos viven con la pertenencia (ADR-040 §1). Nombrar o quitar un
// oficial, ceder el liderazgo y la sucesión de un líder ausente son optimistas,
// como la salida: la RAM los aplica en el tick y un trabajo con la clave del
// clan los escribe, así que las órdenes del mismo clan que vienen detrás llegan
// a la base después. La base vuelve a comprobar el rango con el candado del
// clan (INV-CLAN-005); si no lo admite, o si la escritura no llega, la RAM lo
// deshace.

// Mensajes de los rechazos por rango (RN-CLANGOV-002, RN-CLANGOV-003).
const (
	msgOnlyManagersInvite = "sólo el líder o un oficial invita"
	msgOnlyManagersExpel  = "sólo el líder o un oficial expulsa"
	msgOnlyLeaderGoverns  = "sólo el líder nombra oficiales y cede el liderazgo"
)

// Nombres de los trabajos del gobierno del clan (catálogo JobNames).
const (
	JobClanRank   = "clan.rank"
	JobClanLeader = "clan.leader"
)

// ClanRankOrder es un nombramiento (o su retirada) ya aplicado en la RAM.
type ClanRankOrder struct {
	ClanID   int64
	PlayerID uuid.UUID
	// By es el líder que lo ordenó.
	By      uuid.UUID
	Officer bool
	At      time.Time
	Tick    uint64
	AtMs    int64
}

// ClanLeaderOrder es un cambio de líder ya aplicado en la RAM: un traspaso
// (Reason eventlog.LeaderReasonTransfer, By = From) o la sucesión de un líder
// ausente (eventlog.LeaderReasonInactive, By = uuid.Nil).
type ClanLeaderOrder struct {
	ClanID int64
	Tag    string
	From   uuid.UUID
	To     uuid.UUID
	Reason string
	By     uuid.UUID
	// ToWasOfficer: el nuevo líder era oficial. Sólo lo usa la RAM para
	// deshacer el cambio.
	ToWasOfficer bool
	At           time.Time
	Tick         uint64
	AtMs         int64
}

// PromoteInClan nombra oficial a un miembro o le quita el rango
// (clan.promote).
type PromoteInClan struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	TargetID  uuid.UUID
	Officer   bool
}

func (PromoteInClan) commandType() string { return protocol.TypeClanPromote }

// TransferClan cede el liderazgo a otro miembro (clan.transfer).
type TransferClan struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	TargetID  uuid.UUID
}

func (TransferClan) commandType() string { return protocol.TypeClanTransfer }

// ClanRankFailed lo devuelve la cola cuando un nombramiento no se escribió:
// la base no lo admitió (Code INVALID_TARGET) o no respondió (INTERNAL_ERROR).
type ClanRankFailed struct {
	Order     ClanRankOrder
	RequestID string
	Code      string
	Message   string
}

func (ClanRankFailed) commandType() string { return "clan.rank_failed" }

// ClanLeaderFailed lo devuelve la cola cuando un cambio de líder no se
// escribió.
type ClanLeaderFailed struct {
	Order     ClanLeaderOrder
	RequestID string
	Code      string
	Message   string
}

func (ClanLeaderFailed) commandType() string { return "clan.leader_failed" }

// ClanExpelRejected lo devuelve la cola cuando la base no admite una expulsión
// con el rango de quien la dio (INV-CLAN-005): la RAM la deshace.
type ClanExpelRejected struct {
	Leave   ClanLeave
	Message string
}

func (ClanExpelRejected) commandType() string { return "clan.expel_rejected" }

// ─────────────────────────────────────────────────────────────
// Rangos
// ─────────────────────────────────────────────────────────────

// roleOf es el rango de un miembro del clan (RN-CLANGOV-001).
func (r *clanRecord) roleOf(playerID uuid.UUID) string {
	switch {
	case playerID == r.LeaderID:
		return clan.RoleLeader
	case r.Officers[playerID]:
		return clan.RoleOfficer
	}
	return clan.RoleMember
}

// managers son el líder y los oficiales: quienes ven y retiran las
// invitaciones del clan.
func (r *clanRecord) managers() []uuid.UUID {
	out := []uuid.UUID{r.LeaderID}
	for id := range r.Officers {
		out = append(out, id)
	}
	sort.Slice(out[1:], func(i, j int) bool { return out[1+i].String() < out[1+j].String() })
	return out
}

// clanRole es el clan de un jugador y su rango en él.
func (s *State) clanRole(playerID uuid.UUID) (*clanRecord, string, bool) {
	id, ok := s.clans.memberOf[playerID]
	if !ok {
		return nil, "", false
	}
	rec := s.clans.clans[id]
	if rec == nil {
		return nil, "", false
	}
	return rec, rec.roleOf(playerID), true
}

// ClanRole es el rango de un jugador en su clan (tests).
func (s *State) ClanRole(playerID uuid.UUID) (string, bool) {
	_, role, ok := s.clanRole(playerID)
	return role, ok
}

func (s *Simulation) clanLeaderInactive() time.Duration {
	if s.deps.ClanLeaderInactive > 0 {
		return s.deps.ClanLeaderInactive
	}
	return clan.DefaultLeaderInactive
}

// ─────────────────────────────────────────────────────────────
// clan.promote (optimista)
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handlePromoteInClan(cmd PromoteInClan) {
	rec, ok := s.governedClan(cmd.PlayerID, cmd.RequestID, cmd.TargetID)
	if !ok {
		return
	}
	if rec.Officers[cmd.TargetID] == cmd.Officer {
		msg := "ya es oficial"
		if !cmd.Officer {
			msg = "no es oficial"
		}
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, msg)
		return
	}
	s.setOfficer(rec, cmd.TargetID, cmd.Officer)
	nowMs := s.deps.Clock.NowMs()
	order := ClanRankOrder{
		ClanID: rec.ID, PlayerID: cmd.TargetID, By: cmd.PlayerID, Officer: cmd.Officer,
		At: time.UnixMilli(nowMs).UTC(), Tick: s.state.Tick(), AtMs: nowMs,
	}
	s.sendClanUpdates(s.state.ClanMembers(rec.ID), cmd.PlayerID, cmd.RequestID)
	s.deps.Log.Info("rango de clan cambiado", "clan_id", rec.ID, "player_id", cmd.TargetID.String(), "officer", cmd.Officer)
	if s.deps.Clans == nil {
		return
	}
	store := s.deps.Clans
	requestID := cmd.RequestID
	s.deps.Persister.Submit(Job{
		Name: JobClanRank,
		Key:  ClanKey(rec.ID),
		Run: func(ctx context.Context) error {
			err := store.SetRank(ctx, order)
			if errors.Is(err, ErrClanConflict) {
				s.reinject(ClanRankFailed{Order: order, RequestID: requestID, Code: protocol.CodeInvalidTarget, Message: err.Error()})
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir un rango de clan; se deshace", "err", err, "clan_id", order.ClanID)
			s.reinject(ClanRankFailed{Order: order, RequestID: requestID, Code: protocol.CodeInternalError, Message: "no se pudo cambiar el rango"})
		},
	})
}

// governedClan valida una orden que sólo da el líder sobre otro miembro
// abierto de su clan (RN-CLANGOV-003) y devuelve el clan, o responde el
// rechazo.
func (s *Simulation) governedClan(actor uuid.UUID, requestID string, target uuid.UUID) (*clanRecord, bool) {
	if s.deps.Clans == nil {
		s.economyError(actor, requestID, protocol.CodeInternalError, "los clanes no están disponibles")
		return nil, false
	}
	rec, role, ok := s.state.clanRole(actor)
	if !ok || !clan.CanGovern(role) {
		s.economyError(actor, requestID, protocol.CodeInvalidTarget, msgOnlyLeaderGoverns)
		return nil, false
	}
	if rec.dissolving {
		s.economyError(actor, requestID, protocol.CodeInvalidTarget, "el clan se está disolviendo")
		return nil, false
	}
	if target == actor {
		s.economyError(actor, requestID, protocol.CodeInvalidTarget, "no puedes hacerlo sobre ti mismo")
		return nil, false
	}
	if _, miembro := rec.Members[target]; !miembro {
		s.economyError(actor, requestID, protocol.CodeInvalidTarget, "ese jugador no es de tu clan")
		return nil, false
	}
	return rec, true
}

// setOfficer cambia el rango de un miembro en la RAM.
func (s *Simulation) setOfficer(rec *clanRecord, playerID uuid.UUID, officer bool) {
	if officer {
		rec.Officers[playerID] = true
	} else {
		delete(rec.Officers, playerID)
	}
}

// handleClanRankFailed deshace un nombramiento que la base no escribió, si
// nada lo cambió después.
func (s *Simulation) handleClanRankFailed(cmd ClanRankFailed) {
	o := cmd.Order
	s.economyError(o.By, cmd.RequestID, cmd.Code, cmd.Message)
	rec, ok := s.state.clans.clans[o.ClanID]
	if !ok {
		return
	}
	if _, miembro := rec.Members[o.PlayerID]; !miembro || rec.LeaderID == o.PlayerID || rec.Officers[o.PlayerID] != o.Officer {
		return
	}
	s.setOfficer(rec, o.PlayerID, !o.Officer)
	s.sendClanUpdates(s.state.ClanMembers(rec.ID), uuid.Nil, "")
}

// ─────────────────────────────────────────────────────────────
// clan.transfer y la sucesión (optimistas)
// ─────────────────────────────────────────────────────────────

func (s *Simulation) handleTransferClan(cmd TransferClan) {
	rec, ok := s.governedClan(cmd.PlayerID, cmd.RequestID, cmd.TargetID)
	if !ok {
		return
	}
	s.changeLeader(rec, cmd.TargetID, eventlog.LeaderReasonTransfer, cmd.PlayerID, cmd.RequestID)
}

// changeLeader pasa el liderazgo del líder actual a to en la RAM, lo difunde,
// avisa al nuevo líder y encola su escritura (RN-CLANGOV-003, RN-CLANGOV-005).
// El líder anterior queda como oficial (Q4, Q7).
func (s *Simulation) changeLeader(rec *clanRecord, to uuid.UUID, reason string, by uuid.UUID, requestID string) {
	nowMs := s.deps.Clock.NowMs()
	from := rec.LeaderID
	order := ClanLeaderOrder{
		ClanID: rec.ID, Tag: rec.Tag, From: from, To: to, Reason: reason, By: by,
		ToWasOfficer: rec.Officers[to],
		At:           time.UnixMilli(nowMs).UTC(), Tick: s.state.Tick(), AtMs: nowMs,
	}
	rec.LeaderID = to
	delete(rec.Officers, to)
	rec.Officers[from] = true

	s.sendClanUpdates(s.state.ClanMembers(rec.ID), by, requestID)
	// SPEC-CLAN-GOVERNANCE Q8: se avisa a quien lo recibe.
	s.sendNotices(eventlog.Event{
		Type: eventlog.EventClanLeaderChanged, PlayerID: to, Counterpart: from, AtMs: nowMs,
		Reason: reason, OwnerClanTag: rec.Tag, ForClanTag: rec.Tag,
	})
	s.deps.Log.Info("líder de clan cambiado", "clan_id", rec.ID, "from", from.String(), "to", to.String(), "reason", reason)
	if s.deps.Clans == nil {
		return
	}
	store := s.deps.Clans
	s.deps.Persister.Submit(Job{
		Name: JobClanLeader,
		Key:  ClanKey(rec.ID),
		Run: func(ctx context.Context) error {
			err := store.ChangeLeader(ctx, order)
			if errors.Is(err, ErrClanConflict) {
				s.reinject(ClanLeaderFailed{Order: order, RequestID: requestID, Code: protocol.CodeInvalidTarget, Message: err.Error()})
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir un cambio de líder; se deshace", "err", err, "clan_id", order.ClanID)
			s.reinject(ClanLeaderFailed{Order: order, RequestID: requestID, Code: protocol.CodeInternalError, Message: "no se pudo cambiar el líder"})
		},
	})
}

// handleClanLeaderFailed deshace un cambio de líder que la base no escribió, si
// nada lo cambió después: vuelve el líder anterior y cada uno a su rango.
func (s *Simulation) handleClanLeaderFailed(cmd ClanLeaderFailed) {
	o := cmd.Order
	if o.By != uuid.Nil {
		s.economyError(o.By, cmd.RequestID, cmd.Code, cmd.Message)
	}
	rec, ok := s.state.clans.clans[o.ClanID]
	if !ok || rec.LeaderID != o.To {
		return
	}
	if _, sigue := rec.Members[o.From]; !sigue {
		return
	}
	rec.LeaderID = o.From
	delete(rec.Officers, o.From)
	if o.ToWasOfficer {
		rec.Officers[o.To] = true
	} else {
		delete(rec.Officers, o.To)
	}
	s.sendClanUpdates(s.state.ClanMembers(rec.ID), uuid.Nil, "")
	s.deps.Log.Warn("cambio de líder deshecho", "clan_id", rec.ID, "from", o.From.String(), "to", o.To.String())
}

// sweepClanLeaders pasa el liderazgo de cada clan cuyo líder lleva
// EO_CLAN_LEADER_INACTIVE_SECONDS sin conectarse al oficial activo más
// antiguo, o al miembro activo más antiguo (RN-CLANGOV-005). Barre una vez por
// minuto, los clanes por id. La ausencia se deriva de la presencia de la
// ciudad: no se guarda nada.
func (s *Simulation) sweepClanLeaders(nowMs int64) {
	if nowMs < s.state.clans.nextLeaderSweepMs {
		return
	}
	s.state.clans.nextLeaderSweepMs = nowMs + time.Minute.Milliseconds()
	if len(s.state.clans.clans) == 0 {
		return
	}
	threshold := s.clanLeaderInactive()
	absent := func(p uuid.UUID) bool {
		c, ok := s.state.CityOf(p)
		if !ok {
			return false
		}
		return clan.Absent(c.PresenceState == city.PresenceOnline, lastOfflineMs(c), nowMs, threshold)
	}
	ids := make([]int64, 0, len(s.state.clans.clans))
	for id := range s.state.clans.clans {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		rec := s.state.clans.clans[id]
		if rec.dissolving || !absent(rec.LeaderID) {
			continue
		}
		others := make([]clan.Member, 0, len(rec.Members))
		for _, m := range rec.members() {
			if m.PlayerID != rec.LeaderID {
				others = append(others, m)
			}
		}
		to, ok := clan.Successor(others, func(p uuid.UUID) bool {
			_, hasCity := s.state.CityOf(p)
			return hasCity && !absent(p)
		})
		if !ok {
			continue
		}
		s.changeLeader(rec, to, eventlog.LeaderReasonInactive, uuid.Nil, "")
	}
}

// lastOfflineMs es la marca de desconexión de una ciudad, o 0 si no la tiene.
func lastOfflineMs(c *city.City) int64 {
	if c.LastOfflineAt == nil {
		return 0
	}
	return c.LastOfflineAt.UnixMilli()
}

// ─────────────────────────────────────────────────────────────
// La expulsión que la base no admite
// ─────────────────────────────────────────────────────────────

// handleClanExpelRejected deshace en la RAM una expulsión que la base no
// escribió porque quien la dio ya no tenía el rango (INV-CLAN-005): el
// expulsado vuelve al clan con su entrada y su rango, si el clan vive y él no
// está en otro.
func (s *Simulation) handleClanExpelRejected(cmd ClanExpelRejected) {
	l := cmd.Leave
	s.deps.Log.Error("invariant_violation", "inv_id", "INV-CLAN-005", "severity", "ALTO", "policy", "REJECT",
		"entity", "player:"+l.PlayerID.String(), "tick", s.state.Tick(), "detail", "la base no admitió la expulsión: "+cmd.Message)
	s.economyError(l.By, "", protocol.CodeInvalidTarget, "no se pudo expulsar: "+cmd.Message)
	rec, ok := s.state.clans.clans[l.ClanID]
	if !ok {
		return
	}
	if busy, _ := s.state.inClanOrPending(l.PlayerID); busy {
		return
	}
	var joinedAt int64 = -1
	kept := s.state.clans.departed[:0]
	for _, d := range s.state.clans.departed {
		if d.ClanID == l.ClanID && d.PlayerID == l.PlayerID && d.LeftAtMs == l.AtMs {
			joinedAt = d.JoinedAtMs
			continue
		}
		kept = append(kept, d)
	}
	s.state.clans.departed = kept
	if joinedAt < 0 {
		return
	}
	rec.Members[l.PlayerID] = joinedAt
	if l.TargetOfficer {
		rec.Officers[l.PlayerID] = true
	}
	s.state.clans.memberOf[l.PlayerID] = rec.ID
	s.setCityClan(l.PlayerID, &city.ClanTag{ID: rec.ID, Tag: rec.Tag})
	s.sendClanUpdates(s.state.ClanMembers(rec.ID), uuid.Nil, "")
}
