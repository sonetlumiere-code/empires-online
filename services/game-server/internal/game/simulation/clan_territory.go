package simulation

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Los territorios de clan (SPEC-CLAN-TERRITORIES, ADR-041).
//
// Un miembro reclama para su clan con la orden de E12; el territorio pasa a un
// dueño CLAN, es tierra afín para los miembros abiertos del clan y no da
// población. Reclamar uno de un clan es hostil contra todos sus miembros. La
// última salida de un clan con territorios va en dos fases: la base libera los
// territorios y disuelve el clan en una transacción, y la RAM lo aplica al
// volver (ADR-041 §4).

// ClaimEndClan: quien reclamaba para su clan dejó de ser miembro de ese clan
// (RN-CLANTERR-008, INV-CLAN-004).
const ClaimEndClan = "CLAN"

// Mensajes de los rechazos de la reclamación con un clan de por medio.
const (
	msgNoClan          = "no estás en ningún clan"
	msgClanDissolving  = "tu clan se está disolviendo"
	msgClanTreatyPeace = "un tratado de paz con un miembro de ese clan impide reclamarlo"
	msgClanAllAsleep   = "todos los miembros del clan dueño están protegidos"
)

// ClanDissolved lo devuelve la cola con el resultado de la última salida de un
// clan con territorios (ADR-041 §4): los controles que la base liberó, o Failed
// si no se pudo escribir.
type ClanDissolved struct {
	Leave     ClanLeave
	Released  []territory.Control
	Failed    bool
	Actor     uuid.UUID
	RequestID string
}

func (ClanDissolved) commandType() string { return "clan.dissolved" }

// ClanTerritoriesReleased lo devuelve la cola cuando una disolución optimista
// liberó territorios que la RAM no conocía todavía como del clan (una captura
// que confirmó justo antes en la misma cola, ADR-041 §4).
type ClanTerritoriesReleased struct {
	Controls []territory.Control
}

func (ClanTerritoriesReleased) commandType() string { return "clan.territories_released" }

// ─────────────────────────────────────────────────────────────
// Estado
// ─────────────────────────────────────────────────────────────

// clanTerritories son los territorios de un clan, por id. Se derivan del
// control de la RAM: son como mucho tantos como territorios tiene el mundo.
func (s *State) clanTerritories(clanID int64) []territory.Territory {
	var out []territory.Territory
	for id, c := range s.control {
		if owner, ok := c.ClanOwner(); ok && owner == clanID {
			if t, ok := s.territories.ByID(id); ok {
				out = append(out, t)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ClanTerritoryIDs son los ids de los territorios de un clan (tests).
func (s *State) ClanTerritoryIDs(clanID int64) []int64 {
	ts := s.clanTerritories(clanID)
	out := make([]int64, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

// clanTerritoryViews son los territorios de un clan para sus vistas
// (SPEC-CLAN-TERRITORIES Q11).
func (s *State) clanTerritoryViews(clanID int64) []protocol.ClanTerritoryView {
	out := []protocol.ClanTerritoryView{}
	for _, t := range s.clanTerritories(clanID) {
		out = append(out, protocol.ClanTerritoryView{ID: t.ID, Name: t.Name, X: (t.MinX + t.MaxX) / 2, Y: (t.MinY + t.MaxY) / 2})
	}
	return out
}

// ownerClanView es el clan dueño de un control para la red, o nil.
func (s *State) ownerClanView(c territory.Control) *protocol.TerritoryOwnerClanView {
	id, ok := c.ClanOwner()
	if !ok {
		return nil
	}
	rec, ok := s.clans.clans[id]
	if !ok {
		return nil
	}
	return &protocol.TerritoryOwnerClanView{ID: rec.ID, Name: rec.Name, Tag: rec.Tag}
}

// clanTag es la sigla de un clan vivo, o "".
func (s *State) clanTag(clanID int64) string {
	if rec, ok := s.clans.clans[clanID]; ok {
		return rec.Tag
	}
	return ""
}

// clanDissolving dice si un clan vivo espera la transacción de su disolución.
func (s *State) clanDissolving(clanID int64) bool {
	rec, ok := s.clans.clans[clanID]
	return ok && rec.dissolving
}

// territoryAffine dice si el territorio de ese control es tierra afín a un
// jugador (RN-FAC-007 con su precisión de E19): es suyo, es de un jugador de su
// bando (ORDER o CHAOS) o es de su clan (RN-CLANTERR-003).
func (s *State) territoryAffine(c territory.Control, playerID uuid.UUID) bool {
	if owner, ok := c.ClanOwner(); ok {
		mine, member := s.clans.memberOf[playerID]
		return member && mine == owner
	}
	owner, ok := c.PlayerOwner()
	if !ok {
		return false
	}
	return player.Affine(owner == playerID, s.FactionOf(playerID), s.FactionOf(owner))
}

// ─────────────────────────────────────────────────────────────
// La hostilidad contra un clan (ADR-041 §2)
// ─────────────────────────────────────────────────────────────

// clanClaimBlocked es la rama de claimBlocked para un territorio de un clan:
// la paz con cualquiera de sus miembros abiertos lo impide, con el motivo de la
// primera fuente que la dé; la protección, sólo si lo están todos (Q5).
func (s *Simulation) clanClaimBlocked(claimer uuid.UUID, clanID int64, now time.Time) (string, string) {
	members := s.state.ClanMembers(clanID)
	for _, m := range members {
		if s.state.OrderPeace(claimer, m) {
			return protocol.CodeInvalidTarget, msgOrderPeace
		}
	}
	for _, m := range members {
		if s.state.ClanPeace(claimer, m, now) {
			return protocol.CodeInvalidTarget, msgClanPeace
		}
	}
	for _, m := range members {
		if s.state.AllyPeace(claimer, m, now) {
			return protocol.CodeInvalidTarget, msgAllyPeace
		}
	}
	for _, m := range members {
		if s.state.AtPeace(claimer, m, now) {
			return protocol.CodeInvalidTarget, msgClanTreatyPeace
		}
	}
	if len(members) == 0 {
		return "", ""
	}
	for _, m := range members {
		c, ok := s.state.CityOf(m)
		if !ok || !c.ProtectedAt(now) {
			return "", ""
		}
	}
	return protocol.CodeCityProtected, msgClanAllAsleep
}

// atPeaceWithOwner dice si quien reclama está en paz con el dueño del control:
// con el jugador, o con algún miembro del clan dueño.
func (s *State) atPeaceWithOwner(claimer uuid.UUID, c territory.Control, now time.Time) bool {
	if owner, ok := c.PlayerOwner(); ok {
		return s.AtPeace(claimer, owner, now)
	}
	if clanID, ok := c.ClanOwner(); ok {
		for _, m := range s.ClanMembers(clanID) {
			if s.AtPeace(claimer, m, now) {
				return true
			}
		}
	}
	return false
}

// clanGuardsNear son los guerreros de los miembros de un clan cerca de un tile,
// en orden de id de unidad (ADR-041 §2): la defensa de RN-CLANTERR-005.
func (s *Simulation) clanGuardsNear(clanID int64, cs *claimState) []*unit.Unit {
	var out []*unit.Unit
	for _, m := range s.state.ClanMembers(clanID) {
		out = append(out, s.guardsNear(m, cs.Tile())...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ─────────────────────────────────────────────────────────────
// Avisos (ADR-041 §5)
// ─────────────────────────────────────────────────────────────

// noticeClanClaimStarted avisa a cada miembro abierto del clan dueño de que
// reclaman su territorio (RN-CLANTERR-006).
func (s *Simulation) noticeClanClaimStarted(clanID int64, cs *claimState, t territory.Territory) {
	tag := s.state.clanTag(clanID)
	for _, m := range s.state.ClanMembers(clanID) {
		s.sendNotices(eventlog.Event{
			Type: territory.EventClaimStarted, PlayerID: m, Counterpart: cs.PlayerID, AtMs: cs.StartedAtMs,
			X: cs.X, Y: cs.Y, HasTile: true, TerritoryID: t.ID, TerritoryName: t.Name,
			OwnerClanTag: tag, Only: eventlog.OnlyPlayer,
		})
	}
}

// noticeCapture avisa de una captura como la escriben las filas de la base: al
// anterior dueño y al que reclamó, o a cada miembro del clan que lo pierde y,
// una vez, al que reclamó.
func (s *Simulation) noticeCapture(c Capture) {
	base := eventlog.Event{
		Type: territory.EventControlChanged, Counterpart: c.NewOwner, AtMs: c.AtMs,
		X: c.X, Y: c.Y, HasTile: true, TerritoryID: c.TerritoryID, TerritoryName: c.TerritoryName,
	}
	switch {
	case c.PrevOwner != uuid.Nil:
		e := base
		e.PlayerID, e.ForClanTag = c.PrevOwner, c.ForClanTag
		s.sendNotices(e)
	case c.PrevClanID != 0:
		for _, m := range c.PrevClanMembers {
			e := base
			e.PlayerID, e.OwnerClanTag, e.Only = m, c.PrevClanTag, eventlog.OnlyPlayer
			s.sendNotices(e)
		}
		if len(c.PrevClanMembers) > 0 {
			e := base
			e.PlayerID, e.ForClanTag, e.Only = c.PrevClanMembers[0], c.ForClanTag, eventlog.OnlyCounterpart
			s.sendNotices(e)
		}
	}
}

// sendClanTerritoryUpdates manda clan.update a los miembros de los clanes cuyos
// territorios cambiaron.
func (s *Simulation) sendClanTerritoryUpdates(clanIDs ...int64) {
	var players []uuid.UUID
	for _, id := range clanIDs {
		if id != 0 {
			players = append(players, s.state.ClanMembers(id)...)
		}
	}
	if len(players) > 0 {
		s.sendClanUpdates(players, uuid.Nil, "")
	}
}

// ─────────────────────────────────────────────────────────────
// La última salida de un clan con territorios (ADR-041 §4)
// ─────────────────────────────────────────────────────────────

// startDissolution marca el clan en disolución y encola la transacción que
// cierra la estancia, libera sus territorios y lo disuelve. Nada cambia en la
// RAM hasta la confirmación, salvo que el clan rechaza entradas y las
// reclamaciones para él se rompen (su dueño ya no cuenta como miembro).
func (s *Simulation) startDissolution(rec *clanRecord, playerID uuid.UUID, reason string, by, actor uuid.UUID, requestID string) {
	nowMs := s.deps.Clock.NowMs()
	rec.dissolving = true
	leave := ClanLeave{
		ClanID: rec.ID, PlayerID: playerID, Reason: reason, By: by,
		At: time.UnixMilli(nowMs).UTC(), Dissolve: true, Tick: s.state.Tick(), AtMs: nowMs,
	}
	s.deps.Log.Info("disolución de un clan con territorios en vuelo", "clan_id", rec.ID,
		"territories", len(s.state.clanTerritories(rec.ID)))
	if s.deps.Clans == nil {
		return
	}
	store := s.deps.Clans
	s.deps.Persister.Submit(Job{
		Name: JobClanLeave,
		Key:  ClanKey(rec.ID),
		Run: func(ctx context.Context) error {
			released, err := store.Leave(ctx, leave)
			if err != nil {
				return err
			}
			s.reinject(ClanDissolved{Leave: leave, Released: released, Actor: actor, RequestID: requestID})
			return nil
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la disolución de un clan con territorios", "err", err, "clan_id", leave.ClanID)
			s.reinject(ClanDissolved{Leave: leave, Failed: true, Actor: actor, RequestID: requestID})
		},
	})
}

// handleClanDissolved aplica la disolución confirmada —la salida, como en E18,
// y los territorios liberados— o la deshace si no se pudo escribir.
func (s *Simulation) handleClanDissolved(cmd ClanDissolved) {
	rec, ok := s.state.clans.clans[cmd.Leave.ClanID]
	if !ok || !rec.dissolving {
		return
	}
	rec.dissolving = false
	if cmd.Failed {
		s.economyError(cmd.Actor, cmd.RequestID, protocol.CodeInternalError, "no se pudo salir del clan")
		return
	}
	l := cmd.Leave
	s.applyLeave(rec, l.PlayerID, l.Reason, l.By, cmd.Actor, cmd.RequestID, l.AtMs)
	s.applyReleased(cmd.Released)
}

func (s *Simulation) handleClanTerritoriesReleased(cmd ClanTerritoriesReleased) {
	s.applyReleased(cmd.Controls)
}

// applyReleased aplica los controles liberados por una disolución, salvo los
// que la RAM ya tiene con una versión igual o más nueva.
func (s *Simulation) applyReleased(controls []territory.Control) {
	for _, c := range controls {
		if cur, ok := s.state.TerritoryControl(c.TerritoryID); ok && cur.Version >= c.Version {
			continue
		}
		s.applyTerritoryControl(c)
	}
}
