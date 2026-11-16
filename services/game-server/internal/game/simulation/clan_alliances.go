package simulation

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Las alianzas entre clanes (SPEC-CLAN-ALLIANCES, ADR-042).
//
// Una alianza es una relación entre dos clanes que vive en clanState, junto a
// la pertenencia. Da paz a todos los miembros de los dos (AllyPeace, la cuarta
// fuente de AtPeace). Las órdenes de los líderes van en dos fases, como los
// tratados (ADR-027 §2): el tick valida y reserva el par, un trabajo con las
// claves de los dos clanes escribe y el buzón confirma o deshace. La caducidad
// de una oferta y el fin de un preaviso son optimistas (ADR-027 §3). La mezcla
// de la Orden y el Caos que aparece después abre el preaviso en la transacción
// que la crea (ADR-042 §4), y la disolución de un clan expira sus alianzas en
// la suya (§5).

// Nombres de los trabajos de las alianzas (catálogo JobNames).
const (
	JobClanAlliance    = "clan.alliance"
	JobClanAllianceEnd = "clan.alliance_end"
)

// Mensajes de los rechazos de las alianzas.
const (
	msgAllyPeace         = "vuestros clanes están aliados"
	msgOnlyLeaderAllies  = "sólo el líder firma las alianzas del clan"
	msgAllianceMixes     = "una alianza no junta a la Orden y al Caos"
	msgAllianceBusy      = "hay otra orden en curso con ese clan"
	msgAllianceNotExists = "esa alianza no existe"
)

// allianceSweepInterval es cada cuánto mira la fase 2 los vencimientos de las
// alianzas, como los de los tratados.
const allianceSweepInterval = time.Second

// AllianceWrite es una orden o un vencimiento de una alianza ya decididos por
// la simulación.
type AllianceWrite struct {
	// Insert: una oferta nueva, sin id todavía.
	Insert bool
	// Alliance es la alianza tal como queda.
	Alliance clan.Alliance
	// From es el estado que debe tener la fila para aplicarla.
	From  diplomacy.Status
	Event string
	// By es el líder que dio la orden, o el jugador cuya entrada en un bando o
	// en un clan abrió el preaviso; uuid.Nil en un vencimiento. ByClan es el
	// clan del líder que da una orden: la base comprueba que By lo lidera
	// (RN-CLANALLY-009); cero si no es una orden.
	By     uuid.UUID
	ByClan int64
	// MaxAllies es el tope de alianzas activas que la base vuelve a comprobar
	// al aceptar.
	MaxAllies int
	// Tolerant: un vencimiento o una ruptura por bandos; si la fila ya no está
	// en From, no escribe nada y no es un error (ADR-042 §3, §4).
	Tolerant bool
	Tick     uint64
	AtMs     int64
}

// AllianceBreak es una alianza a la que la base abrió el preaviso al confirmar
// una entrada en un clan (ADR-042 §4).
type AllianceBreak struct {
	AllianceID         int64
	BreakEffectiveAtMs int64
}

// ProposeClanAlliance propone una alianza a otro clan (clan.alliance.propose).
type ProposeClanAlliance struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	ClanID    int64
}

func (ProposeClanAlliance) commandType() string { return protocol.TypeClanAlliancePropose }

// RespondClanAlliance acepta o rechaza una oferta de alianza
// (clan.alliance.respond).
type RespondClanAlliance struct {
	PlayerID   uuid.UUID
	SessionID  uuid.UUID
	RequestID  string
	AllianceID int64
	Accept     bool
}

func (RespondClanAlliance) commandType() string { return protocol.TypeClanAllianceRespond }

// CancelClanAlliance retira una oferta o rompe una alianza
// (clan.alliance.cancel).
type CancelClanAlliance struct {
	PlayerID   uuid.UUID
	SessionID  uuid.UUID
	RequestID  string
	AllianceID int64
}

func (CancelClanAlliance) commandType() string { return protocol.TypeClanAllianceCancel }

// ClanAllianceResolved lo devuelve la cola con el resultado de una orden.
type ClanAllianceResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	Write     AllianceWrite
	// ID es el que dio la base a una oferta nueva.
	ID int64
	// Code, si no está vacío, es el rechazo: la transacción no escribió nada.
	Code    string
	Message string
}

func (ClanAllianceResolved) commandType() string { return "clan.alliance_resolved" }

// ClanAllianceEndPersistFailed es la compensación de un vencimiento que no se
// escribió: el barrido siguiente lo vuelve a encolar (ADR-022).
type ClanAllianceEndPersistFailed struct{ Write AllianceWrite }

func (ClanAllianceEndPersistFailed) commandType() string { return "clan.alliance_end_failed" }

// AllianceViolation es una alianza que la carga no instala, con el motivo
// (INV-CLAN-006).
type AllianceViolation struct {
	AllianceID int64
	Detail     string
}

// ─────────────────────────────────────────────────────────────
// Estado
// ─────────────────────────────────────────────────────────────

// allianceState son las alianzas en la RAM (ADR-042 §1).
type allianceState struct {
	// open son las ofertas y las alianzas activas, por id.
	open map[int64]*clan.Alliance
	// closed son las alianzas que estuvieron activas y se cerraron hace menos de
	// la paz residual: de ellas sale la paz residual con los aliados.
	closed []clan.Alliance
	// busy son los pares con una orden esperando su transacción; accepting, los
	// que esperan una aceptación, que cuentan para el tope.
	busy      map[[2]int64]bool
	accepting map[[2]int64]bool
	// rejected es, por (clan que propuso, clan que rechazó), cuándo se rechazó
	// su última oferta (RN-CLANALLY-004).
	rejected map[[2]int64]int64
	// failedEnds son los vencimientos cuya escritura se descartó (ADR-022).
	failedEnds  []AllianceWrite
	nextSweepMs int64
}

func newAllianceState() allianceState {
	return allianceState{
		open:      make(map[int64]*clan.Alliance),
		busy:      make(map[[2]int64]bool),
		accepting: make(map[[2]int64]bool),
		rejected:  make(map[[2]int64]int64),
	}
}

// SetClanAlliances instala las alianzas abiertas, las cerradas recientes que
// estuvieron activas y los rechazos recientes, al arrancar. Va después de
// SetClans. Devuelve las alianzas que no instala porque no cumplen
// INV-CLAN-006: un clan que no está vivo, un par con dos abiertas del mismo
// estado o una activa sin preaviso que junta a la Orden y al Caos.
func (s *State) SetClanAlliances(open, closed, rejected []clan.Alliance) []AllianceViolation {
	s.allies = newAllianceState()
	var bad []AllianceViolation
	sort.Slice(open, func(i, j int) bool { return open[i].ID < open[j].ID })
	seen := make(map[[2]int64]map[diplomacy.Status]bool)
	for _, a := range open {
		if !a.Open() {
			continue
		}
		_, okA := s.clans.clans[a.ClanA]
		_, okB := s.clans.clans[a.ClanB]
		if !okA || !okB {
			bad = append(bad, AllianceViolation{a.ID, "une a un clan que no está vivo"})
			continue
		}
		if seen[a.Pair()] == nil {
			seen[a.Pair()] = make(map[diplomacy.Status]bool)
		}
		if seen[a.Pair()][a.Status] {
			bad = append(bad, AllianceViolation{a.ID, "el par ya tiene otra alianza " + string(a.Status)})
			continue
		}
		if a.Active() && !a.InNotice() && s.pairMixes(a.ClanA, a.ClanB) {
			bad = append(bad, AllianceViolation{a.ID, "activa sin preaviso entre la Orden y el Caos"})
			continue
		}
		seen[a.Pair()][a.Status] = true
		copia := a
		s.allies.open[a.ID] = &copia
	}
	for _, a := range closed {
		if a.AcceptedAtMs > 0 && a.ClosedAtMs > 0 {
			s.allies.closed = append(s.allies.closed, a)
		}
	}
	for _, a := range rejected {
		if a.Status == diplomacy.StatusRejected && a.ClosedAtMs > 0 {
			s.noteAllianceRejection(a)
		}
	}
	return bad
}

func (s *State) noteAllianceRejection(a clan.Alliance) {
	k := [2]int64{a.ProposedByClan, a.Other(a.ProposedByClan)}
	if prev, ok := s.allies.rejected[k]; !ok || a.ClosedAtMs > prev {
		s.allies.rejected[k] = a.ClosedAtMs
	}
}

// ClanAlliance es una alianza abierta (tests).
func (s *State) ClanAlliance(id int64) (clan.Alliance, bool) {
	a, ok := s.allies.open[id]
	if !ok {
		return clan.Alliance{}, false
	}
	return *a, true
}

// ClanAlliancesOf son las alianzas abiertas de un clan, por id (tests y vistas).
func (s *State) ClanAlliancesOf(clanID int64) []clan.Alliance {
	var out []clan.Alliance
	for _, a := range s.allies.open {
		if a.Has(clanID) {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// openAlliance es la alianza abierta de un par en ese estado, si la hay.
func (s *State) openAlliance(a, b int64, status diplomacy.Status) *clan.Alliance {
	x, y := clan.CanonicalClans(a, b)
	for _, al := range s.allies.open {
		if al.ClanA == x && al.ClanB == y && al.Status == status {
			return al
		}
	}
	return nil
}

// activeAllies cuenta las alianzas activas de un clan y las aceptaciones en
// vuelo que lo tocan: lo que cuenta para el tope (Q4).
func (s *State) activeAllies(clanID int64) int {
	n := 0
	for _, a := range s.allies.open {
		if a.Active() && a.Has(clanID) {
			n++
		}
	}
	for k := range s.allies.accepting {
		if k[0] == clanID || k[1] == clanID {
			n++
		}
	}
	return n
}

// pairMixes dice si los miembros de dos clanes, con quienes están entrando y
// sus entradas pendientes en un bando, juntan a la Orden y al Caos
// (RN-CLANALLY-005).
func (s *State) pairMixes(a, b int64) bool {
	return clan.Mixes(append(s.clanStances(a, uuid.Nil), s.clanStances(b, uuid.Nil)...)...)
}

// AllyPeace dice si dos jugadores están en paz por una alianza entre sus clanes
// (RN-CLANALLY-003, RN-CLANALLY-006, ADR-042 §2): son miembros de dos clanes con
// una alianza activa (también en preaviso, hasta su fin), o a uno le queda paz
// residual con el clan del otro.
func (s *State) AllyPeace(x, y uuid.UUID, now time.Time) bool {
	if x == y || len(s.allies.open) == 0 && len(s.allies.closed) == 0 {
		return false
	}
	nowMs := now.UnixMilli()
	cx, okx := s.clans.memberOf[x]
	cy, oky := s.clans.memberOf[y]
	if okx && oky && cx != cy {
		if a := s.openAlliance(cx, cy, diplomacy.StatusActive); a != nil &&
			(a.BreakEffectiveAtMs == 0 || nowMs < a.BreakEffectiveAtMs) {
			return true
		}
	}
	if len(s.clans.departed) == 0 {
		return false
	}
	stays := make([]clan.Stay, 0, 4)
	for _, p := range []uuid.UUID{x, y} {
		if id, ok := s.clans.memberOf[p]; ok {
			stays = append(stays, clan.Stay{ClanID: id, PlayerID: p, JoinedAtMs: s.clans.clans[id].Members[p]})
		}
	}
	tieneSalida := false
	for _, d := range s.clans.departed {
		if d.PlayerID == x || d.PlayerID == y {
			stays = append(stays, d)
			tieneSalida = true
		}
	}
	if !tieneSalida {
		return false
	}
	alliances := make([]clan.Alliance, 0, len(s.allies.open)+len(s.allies.closed))
	for _, a := range s.allies.open {
		alliances = append(alliances, *a)
	}
	alliances = append(alliances, s.allies.closed...)
	return clan.AllyResidualPeace(x, y, stays, alliances, nowMs, s.clans.grace)
}

// ─────────────────────────────────────────────────────────────
// Configuración
// ─────────────────────────────────────────────────────────────

func (s *Simulation) clanMaxAllies() int {
	if s.deps.ClanMaxAllies > 0 {
		return s.deps.ClanMaxAllies
	}
	return clan.DefaultMaxAllies
}

// ─────────────────────────────────────────────────────────────
// Vistas y difusión
// ─────────────────────────────────────────────────────────────

// allianceViews son las alianzas de un clan vistas por uno de sus miembros: las
// activas para todos y las ofertas para el líder y los oficiales
// (RN-CLANALLY-007), por id.
func (s *State) allianceViews(rec *clanRecord, viewer uuid.UUID) []protocol.ClanAllianceView {
	out := []protocol.ClanAllianceView{}
	managers := clan.CanInvite(rec.roleOf(viewer))
	for _, a := range s.ClanAlliancesOf(rec.ID) {
		if !a.Active() && !managers {
			continue
		}
		other, ok := s.clans.clans[a.Other(rec.ID)]
		if !ok {
			continue
		}
		v := protocol.ClanAllianceView{
			ID: a.ID, ClanID: other.ID, Name: other.Name, Tag: other.Tag,
			Status: string(a.Status), ProposedByUs: a.ProposedByClan == rec.ID,
		}
		if a.BreakEffectiveAtMs > 0 {
			fin := a.BreakEffectiveAtMs
			v.BreaksAtMs = &fin
		}
		out = append(out, v)
	}
	return out
}

// sendAllianceUpdates manda clan.update a los miembros de los dos clanes.
func (s *Simulation) sendAllianceUpdates(a clan.Alliance, actor uuid.UUID, requestID string) {
	players := append(s.state.ClanMembers(a.ClanA), s.state.ClanMembers(a.ClanB)...)
	if actor != uuid.Nil {
		players = append(players, actor)
	}
	s.sendClanUpdates(players, actor, requestID)
}

// storeAlliance deja en la RAM una alianza abierta, o la saca si terminó y
// guarda la que estuvo activa para la paz residual.
func (s *State) storeAlliance(a clan.Alliance) {
	if a.Open() {
		copia := a
		s.allies.open[a.ID] = &copia
		return
	}
	delete(s.allies.open, a.ID)
	if a.AcceptedAtMs > 0 && a.ClosedAtMs > 0 {
		s.allies.closed = append(s.allies.closed, a)
	}
}

// ─────────────────────────────────────────────────────────────
// Órdenes (dos fases)
// ─────────────────────────────────────────────────────────────

// allyingClan valida que quien da una orden de alianza es el líder de un clan
// que no se está disolviendo (RN-CLANALLY-002) y devuelve su clan, o responde
// el rechazo.
func (s *Simulation) allyingClan(actor uuid.UUID, requestID string) (*clanRecord, bool) {
	if s.deps.Clans == nil {
		s.economyError(actor, requestID, protocol.CodeInternalError, "los clanes no están disponibles")
		return nil, false
	}
	rec, role, ok := s.state.clanRole(actor)
	if !ok || !clan.CanGovern(role) {
		s.economyError(actor, requestID, protocol.CodeInvalidTarget, msgOnlyLeaderAllies)
		return nil, false
	}
	if rec.dissolving {
		s.economyError(actor, requestID, protocol.CodeInvalidTarget, "el clan se está disolviendo")
		return nil, false
	}
	return rec, true
}

func (s *Simulation) handleProposeClanAlliance(cmd ProposeClanAlliance) {
	rec, ok := s.allyingClan(cmd.PlayerID, cmd.RequestID)
	if !ok {
		return
	}
	reject := func(msg string) { s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, msg) }
	if cmd.ClanID == rec.ID {
		reject("no puedes aliarte con tu propio clan")
		return
	}
	other, ok := s.state.clans.clans[cmd.ClanID]
	if !ok {
		reject("ese clan no existe")
		return
	}
	if other.dissolving {
		reject("ese clan se está disolviendo")
		return
	}
	a, b := clan.CanonicalClans(rec.ID, other.ID)
	if s.state.allies.busy[[2]int64{a, b}] {
		reject(msgAllianceBusy)
		return
	}
	if s.state.openAlliance(a, b, diplomacy.StatusActive) != nil {
		reject("vuestros clanes ya están aliados")
		return
	}
	if s.state.openAlliance(a, b, diplomacy.StatusProposed) != nil {
		reject("ya hay una oferta pendiente con ese clan")
		return
	}
	if s.state.activeAllies(rec.ID) >= s.clanMaxAllies() {
		reject("tu clan ya tiene el máximo de alianzas")
		return
	}
	if s.state.activeAllies(other.ID) >= s.clanMaxAllies() {
		reject("ese clan ya tiene el máximo de alianzas")
		return
	}
	if s.state.pairMixes(rec.ID, other.ID) {
		reject(msgAllianceMixes)
		return
	}
	nowMs := s.deps.Clock.NowMs()
	if cuando, ok := s.state.allies.rejected[[2]int64{rec.ID, other.ID}]; ok && nowMs < cuando+s.treatyRetry().Milliseconds() {
		reject("ese clan rechazó tu oferta hace poco: espera antes de insistir")
		return
	}
	al := clan.Alliance{
		ClanA: a, ClanB: b, ProposedByClan: rec.ID, ProposedBy: cmd.PlayerID,
		Status: diplomacy.StatusProposed, RequestID: cmd.RequestID, CreatedAtMs: nowMs,
	}
	s.submitAllianceOrder(AllianceWrite{
		Insert: true, Alliance: al, From: diplomacy.StatusProposed, Event: eventlog.EventClanAllianceProposed, ByClan: rec.ID,
	}, cmd.PlayerID, cmd.RequestID)
}

func (s *Simulation) handleRespondClanAlliance(cmd RespondClanAlliance) {
	rec, ok := s.allyingClan(cmd.PlayerID, cmd.RequestID)
	if !ok {
		return
	}
	reject := func(msg string) { s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, msg) }
	al, ok := s.state.allies.open[cmd.AllianceID]
	if !ok {
		reject(msgAllianceNotExists)
		return
	}
	// RN-CLANALLY-002: sólo responde el líder del clan que no propuso.
	if al.Status != diplomacy.StatusProposed || !al.Has(rec.ID) || al.ProposedByClan == rec.ID {
		reject("no puedes responder a esa oferta")
		return
	}
	if s.state.allies.busy[al.Pair()] {
		reject(msgAllianceBusy)
		return
	}
	other := s.state.clans.clans[al.Other(rec.ID)]
	if other == nil || other.dissolving {
		reject("ese clan se está disolviendo")
		return
	}
	next, event := diplomacy.StatusRejected, eventlog.EventClanAllianceRejected
	if cmd.Accept {
		next, event = diplomacy.StatusActive, eventlog.EventClanAllianceAccepted
		if s.state.openAlliance(al.ClanA, al.ClanB, diplomacy.StatusActive) != nil {
			reject("vuestros clanes ya están aliados")
			return
		}
		if s.state.activeAllies(rec.ID) >= s.clanMaxAllies() {
			reject("tu clan ya tiene el máximo de alianzas")
			return
		}
		if s.state.activeAllies(other.ID) >= s.clanMaxAllies() {
			reject("ese clan ya tiene el máximo de alianzas")
			return
		}
		if s.state.pairMixes(al.ClanA, al.ClanB) {
			reject(msgAllianceMixes)
			return
		}
	}
	after, err := al.TransitionTo(next, s.deps.Clock.NowMs())
	if err != nil {
		reject("esa oferta ya no se puede responder")
		return
	}
	s.submitAllianceOrder(AllianceWrite{
		Alliance: after, From: diplomacy.StatusProposed, Event: event, ByClan: rec.ID, MaxAllies: s.clanMaxAllies(),
	}, cmd.PlayerID, cmd.RequestID)
}

func (s *Simulation) handleCancelClanAlliance(cmd CancelClanAlliance) {
	rec, ok := s.allyingClan(cmd.PlayerID, cmd.RequestID)
	if !ok {
		return
	}
	reject := func(msg string) { s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, msg) }
	al, ok := s.state.allies.open[cmd.AllianceID]
	if !ok || !al.Has(rec.ID) {
		reject(msgAllianceNotExists)
		return
	}
	if s.state.allies.busy[al.Pair()] {
		reject(msgAllianceBusy)
		return
	}
	nowMs := s.deps.Clock.NowMs()
	switch al.Status {
	case diplomacy.StatusProposed:
		if al.ProposedByClan != rec.ID {
			reject("sólo el clan que propuso puede retirar la oferta")
			return
		}
		after, _ := al.TransitionTo(diplomacy.StatusWithdrawn, nowMs)
		s.submitAllianceOrder(AllianceWrite{
			Alliance: after, From: diplomacy.StatusProposed, Event: eventlog.EventClanAllianceWithdrawn, ByClan: rec.ID,
		}, cmd.PlayerID, cmd.RequestID)
	default:
		// RN-CLANALLY-004: romper abre el preaviso; la alianza sigue ACTIVE.
		if al.InNotice() {
			reject("esa alianza ya está en preaviso")
			return
		}
		after := *al
		after.BreakEffectiveAtMs = nowMs + s.breakNotice().Milliseconds()
		after.BreakReason = clan.BreakReasonCancel
		s.submitAllianceOrder(AllianceWrite{
			Alliance: after, From: diplomacy.StatusActive, Event: eventlog.EventClanAllianceBreakAnnounced, ByClan: rec.ID,
		}, cmd.PlayerID, cmd.RequestID)
	}
}

// submitAllianceOrder reserva el par y encola la transacción de una orden, con
// las claves de los dos clanes en orden de id (ADR-042 §3).
func (s *Simulation) submitAllianceOrder(w AllianceWrite, playerID uuid.UUID, requestID string) {
	w.Tick = s.state.Tick()
	w.AtMs, w.By = s.deps.Clock.NowMs(), playerID
	k := w.Alliance.Pair()
	s.state.allies.busy[k] = true
	if w.Alliance.Status == diplomacy.StatusActive && w.From == diplomacy.StatusProposed {
		s.state.allies.accepting[k] = true
	}
	resolved := ClanAllianceResolved{PlayerID: playerID, RequestID: requestID, Write: w}
	store := s.deps.Clans
	s.deps.Persister.Submit(Job{
		Name: JobClanAlliance,
		Key:  ClanKey(k[0]),
		Key2: ClanKey(k[1]),
		Run: func(ctx context.Context) error {
			id, err := store.ApplyAlliance(ctx, w)
			if err == nil {
				r := resolved
				r.ID = id
				s.reinject(r)
				return nil
			}
			if errors.Is(err, ErrClanConflict) {
				r := resolved
				r.Code, r.Message = protocol.CodeInvalidTarget, err.Error()
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la orden de alianza", "err", err, "event", w.Event)
			r := resolved
			r.Code, r.Message = protocol.CodeInternalError, "no se pudo aplicar la orden de alianza"
			s.reinject(r)
		},
	})
}

// handleClanAllianceResolved confirma una orden en la RAM y la difunde a los
// dos clanes, con los avisos, o libera la reserva y responde el rechazo.
func (s *Simulation) handleClanAllianceResolved(cmd ClanAllianceResolved) {
	w := cmd.Write
	k := w.Alliance.Pair()
	delete(s.state.allies.busy, k)
	delete(s.state.allies.accepting, k)
	if cmd.Code != "" {
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, cmd.Message)
		return
	}
	a := w.Alliance
	if w.Insert {
		a.ID = cmd.ID
	}
	recA, okA := s.state.clans.clans[a.ClanA]
	recB, okB := s.state.clans.clans[a.ClanB]
	if !okA || !okB {
		// Un clan se disolvió mientras tanto: su transacción expiró la alianza
		// en la base (ADR-042 §5).
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ese clan ya no existe")
		return
	}
	s.state.storeAlliance(a)
	if a.Status == diplomacy.StatusRejected {
		s.state.noteAllianceRejection(a)
	}
	s.sendAllianceUpdates(a, cmd.PlayerID, cmd.RequestID)

	actorClan, otherClan := recA, recB
	if w.ByClan == recB.ID {
		actorClan, otherClan = recB, recA
	}
	switch w.Event {
	case eventlog.EventClanAllianceProposed, eventlog.EventClanAllianceAccepted:
		// Q9: la oferta al líder que la recibe; la aceptación al que propuso.
		s.sendNotices(eventlog.Event{
			Type: w.Event, PlayerID: cmd.PlayerID, Counterpart: otherClan.LeaderID, AtMs: w.AtMs,
			OwnerClanTag: otherClan.Tag, ForClanTag: actorClan.Tag,
		})
	case eventlog.EventClanAllianceBreakAnnounced:
		s.noticeAllianceBreak(a, cmd.PlayerID, w.AtMs)
	}
	s.deps.Log.Info("alianza de clan", "event", w.Event, "alliance_id", a.ID, "clan_a", a.ClanA, "clan_b", a.ClanB)
}

// noticeAllianceBreak avisa del preaviso a cada miembro de los dos clanes, con
// la sigla del otro clan (Q9). by es quien lo provocó, que no recibe aviso.
func (s *Simulation) noticeAllianceBreak(a clan.Alliance, by uuid.UUID, atMs int64) {
	for _, side := range [2]int64{a.ClanA, a.ClanB} {
		other, ok := s.state.clans.clans[a.Other(side)]
		if !ok {
			continue
		}
		for _, m := range s.state.ClanMembers(side) {
			if m == by {
				continue
			}
			s.sendNotices(eventlog.Event{
				Type: eventlog.EventClanAllianceBreakAnnounced, PlayerID: m, Counterpart: by, AtMs: atMs,
				OwnerClanTag: other.Tag, BreaksAtMs: a.BreakEffectiveAtMs, Only: eventlog.OnlyPlayer,
			})
		}
	}
}

// ─────────────────────────────────────────────────────────────
// La mezcla de bandos (ADR-042 §4) y la disolución (§5)
// ─────────────────────────────────────────────────────────────

// alliancesMixedBy son las alianzas activas sin preaviso de un clan cuyo otro
// clan juntaría a la Orden y al Caos con la postura dada, en orden de id; ok es
// false si alguna tiene una orden en vuelo.
func (s *State) alliancesMixedBy(clanID int64, st clan.Stance) ([]*clan.Alliance, bool) {
	var out []*clan.Alliance
	for _, a := range s.allies.open {
		if !a.Active() || a.InNotice() || !a.Has(clanID) {
			continue
		}
		if !clan.Mixes(append(s.clanStances(a.Other(clanID), uuid.Nil), st)...) {
			continue
		}
		if s.allies.busy[a.Pair()] {
			return nil, false
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, true
}

// openAllianceNotice abre el preaviso de una alianza por la mezcla de bandos en
// la RAM, lo difunde y lo avisa, y devuelve su escritura tolerante para la
// transacción que la provocó.
func (s *Simulation) openAllianceNotice(a *clan.Alliance, by uuid.UUID, fin, nowMs int64) AllianceWrite {
	after := *a
	after.BreakEffectiveAtMs = fin
	after.BreakReason = clan.BreakReasonFactions
	s.state.storeAlliance(after)
	s.sendAllianceUpdates(after, uuid.Nil, "")
	s.noticeAllianceBreak(after, by, nowMs)
	return AllianceWrite{
		Alliance: after, From: diplomacy.StatusActive, Event: eventlog.EventClanAllianceBreakAnnounced,
		By: by, Tolerant: true, Tick: s.state.Tick(), AtMs: nowMs,
	}
}

// applyJoinBreaks aplica en la RAM los preavisos que la base abrió al confirmar
// una entrada en un clan (ADR-042 §4).
func (s *Simulation) applyJoinBreaks(breaks []AllianceBreak, by uuid.UUID, nowMs int64) {
	for _, b := range breaks {
		a, ok := s.state.allies.open[b.AllianceID]
		if !ok || !a.Active() || a.InNotice() {
			continue
		}
		after := *a
		after.BreakEffectiveAtMs = b.BreakEffectiveAtMs
		after.BreakReason = clan.BreakReasonFactions
		s.state.storeAlliance(after)
		s.sendAllianceUpdates(after, uuid.Nil, "")
		s.noticeAllianceBreak(after, by, nowMs)
	}
}

// expireAlliancesOf cierra en la RAM las ofertas y alianzas de un clan que se
// disuelve, en el mismo tick: su transacción las expira en la base
// (RN-CLANALLY-008, ADR-042 §5).
func (s *Simulation) expireAlliancesOf(clanID int64, nowMs int64) {
	for _, a := range s.state.ClanAlliancesOf(clanID) {
		after, err := a.TransitionTo(diplomacy.StatusExpired, nowMs)
		if err != nil {
			continue
		}
		s.state.storeAlliance(after)
		delete(s.state.allies.busy, a.Pair())
		delete(s.state.allies.accepting, a.Pair())
		s.sendClanUpdates(s.state.ClanMembers(a.Other(clanID)), uuid.Nil, "")
	}
}

// ─────────────────────────────────────────────────────────────
// Vencimientos (fase 2, optimistas)
// ─────────────────────────────────────────────────────────────

// sweepClanAlliances aplica, una vez por segundo, las ofertas caducadas y los
// preavisos vencidos, y olvida las alianzas cerradas cuya paz residual ya no
// cuenta y los rechazos viejos (ADR-042 §3).
func (s *Simulation) sweepClanAlliances(nowMs int64) {
	st := &s.state.allies
	if nowMs < st.nextSweepMs {
		return
	}
	st.nextSweepMs = nowMs + allianceSweepInterval.Milliseconds()

	if len(st.failedEnds) > 0 {
		pendientes := st.failedEnds
		st.failedEnds = nil
		for _, w := range pendientes {
			s.submitAllianceEnd(w)
		}
	}
	if len(st.closed) > 0 {
		graceMs := s.state.clans.grace.Milliseconds()
		vivas := st.closed[:0]
		for _, a := range st.closed {
			if nowMs-a.ClosedAtMs < graceMs {
				vivas = append(vivas, a)
			}
		}
		st.closed = vivas
	}
	for k, at := range st.rejected {
		if nowMs-at >= s.treatyRetry().Milliseconds() {
			delete(st.rejected, k)
		}
	}

	ids := make([]int64, 0, len(st.open))
	for id := range st.open {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		a := st.open[id]
		if st.busy[a.Pair()] {
			continue // una orden en vuelo: se mira en el barrido siguiente
		}
		var w AllianceWrite
		switch {
		case a.Status == diplomacy.StatusProposed && nowMs >= a.CreatedAtMs+s.offerTTL().Milliseconds():
			after, err := a.TransitionTo(diplomacy.StatusExpired, nowMs)
			if err != nil {
				continue
			}
			w = AllianceWrite{Alliance: after, From: diplomacy.StatusProposed, Event: eventlog.EventClanAllianceExpired, AtMs: nowMs}
		case a.InNotice() && nowMs >= a.BreakEffectiveAtMs:
			after, err := a.TransitionTo(diplomacy.StatusBroken, a.BreakEffectiveAtMs)
			if err != nil {
				continue
			}
			w = AllianceWrite{Alliance: after, From: diplomacy.StatusActive, Event: eventlog.EventClanAllianceBroken, AtMs: a.BreakEffectiveAtMs}
		default:
			continue
		}
		s.state.storeAlliance(w.Alliance)
		s.sendAllianceUpdates(w.Alliance, uuid.Nil, "")
		w.Tolerant, w.Tick = true, s.state.Tick()
		s.submitAllianceEnd(w)
	}
}

func (s *Simulation) submitAllianceEnd(w AllianceWrite) {
	if s.deps.Clans == nil {
		return
	}
	store := s.deps.Clans
	k := w.Alliance.Pair()
	s.deps.Persister.Submit(Job{
		Name: JobClanAllianceEnd,
		Key:  ClanKey(k[0]),
		Key2: ClanKey(k[1]),
		Run: func(ctx context.Context) error {
			if _, err := store.ApplyAlliance(ctx, w); err != nil && !errors.Is(err, ErrClanConflict) {
				return err
			}
			return nil
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el fin de una alianza; se reintentará",
				"err", err, "alliance_id", w.Alliance.ID)
			s.reinject(ClanAllianceEndPersistFailed{Write: w})
		},
	})
}

func (s *Simulation) handleClanAllianceEndPersistFailed(cmd ClanAllianceEndPersistFailed) {
	s.state.allies.failedEnds = append(s.state.allies.failedEnds, cmd.Write)
}
