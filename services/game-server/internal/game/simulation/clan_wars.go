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

// Las guerras entre clanes (SPEC-CLAN-WARS, ADR-045).
//
// Una guerra es una relación entre dos clanes que vive en clanState, junto a la
// pertenencia y las alianzas. AtPeace no la lee: la guerra ni da paz ni la
// quita (§1). Compromete a los dos clanes: enforceWarTreaties abre el preaviso
// de los pactos entre sus miembros y expira sus ofertas (§3), y el tick rechaza
// pactos y alianzas nuevos entre los dos lados. Las órdenes de los líderes van
// en dos fases con las claves de los dos clanes (§2); el comienzo y el plazo son
// optimistas. El marcador lo decide la RAM en el tick de cada hecho y viaja en
// el evento que puntúa (§4); la disolución de un clan termina sus guerras en su
// transacción (§5).

// Nombres de los trabajos de las guerras (catálogo JobNames).
const (
	JobClanWar    = "clan.war"
	JobClanWarEnd = "clan.war_end"
)

// Mensajes de los rechazos de las guerras.
const (
	msgAtWar          = "vuestros clanes están en guerra"
	msgOnlyLeaderWars = "sólo el líder decide las guerras del clan"
	msgWarBusy        = "hay otra orden de guerra en curso con ese clan"
	msgWarNotExists   = "esa guerra no existe"
)

// warSweepInterval es cada cuánto mira la fase 2 los plazos de las guerras y el
// compromiso sobre los tratados (ADR-045 §2, §3).
const warSweepInterval = time.Second

// WarWrite es una orden o un vencimiento de una guerra ya decididos por la
// simulación.
type WarWrite struct {
	// Insert: una declaración, sin id todavía.
	Insert bool
	// War es la guerra tal como queda.
	War clan.War
	// From es el estado que debe tener la fila para aplicarla.
	From  clan.WarStatus
	Event string
	// By es el líder que dio la orden (uuid.Nil en un vencimiento); ByClan, su
	// clan, que la base comprueba que lidera (RN-CLANWAR-010).
	By     uuid.UUID
	ByClan int64
	// MaxWars y Cooldown son el tope de declaradas y la tregua que la base
	// vuelve a comprobar al declarar.
	MaxWars  int
	Cooldown time.Duration
	// Tolerant: un vencimiento; si la fila ya no está en From, no escribe nada
	// y no es un error.
	Tolerant bool
	Tick     uint64
	AtMs     int64
}

// DeclareClanWar declara la guerra a otro clan (clan.war.declare).
type DeclareClanWar struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	ClanID    int64
}

func (DeclareClanWar) commandType() string { return protocol.TypeClanWarDeclare }

// ClanWarPeace ofrece (o acepta) la paz, o retira la oferta propia o rechaza la
// ajena (clan.war.peace).
type ClanWarPeace struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	WarID     int64
	Offer     bool
}

func (ClanWarPeace) commandType() string { return protocol.TypeClanWarPeace }

// SurrenderClanWar rinde al clan propio (clan.war.surrender).
type SurrenderClanWar struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	WarID     int64
}

func (SurrenderClanWar) commandType() string { return protocol.TypeClanWarSurrender }

// ClanWarResolved lo devuelve la cola con el resultado de una orden.
type ClanWarResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	Write     WarWrite
	// ID es el que dio la base a una declaración.
	ID int64
	// Code, si no está vacío, es el rechazo: la transacción no escribió nada.
	Code    string
	Message string
}

func (ClanWarResolved) commandType() string { return "clan.war_resolved" }

// ClanWarEndPersistFailed es la compensación de un vencimiento que no se
// escribió: el barrido siguiente lo vuelve a encolar (ADR-022).
type ClanWarEndPersistFailed struct{ Write WarWrite }

func (ClanWarEndPersistFailed) commandType() string { return "clan.war_end_failed" }

// WarViolation es una guerra que la carga no instala, con el motivo
// (INV-CLAN-008).
type WarViolation struct {
	WarID  int64
	Detail string
}

// ─────────────────────────────────────────────────────────────
// Estado
// ─────────────────────────────────────────────────────────────

// warState son las guerras en la RAM (ADR-045 §1).
type warState struct {
	// open son las guerras en preparación y activas, por id.
	open map[int64]*clan.War
	// recent son las terminadas hace menos de la tregua.
	recent []clan.War
	// records es el historial de cada clan vivo.
	records map[int64]clan.WarRecord
	// busy son los pares con una orden esperando su transacción; declaring, las
	// declaraciones en vuelo de cada clan, que cuentan para el tope.
	busy      map[[2]int64]bool
	declaring map[int64]int
	// failedEnds son los vencimientos cuya escritura se descartó (ADR-022).
	failedEnds  []WarWrite
	nextSweepMs int64
	// scoreDirty son los clanes cuyo marcador cambió desde la última difusión.
	scoreDirty      map[int64]bool
	nextScorePushMs int64
}

func newWarState() warState {
	return warState{
		open:       make(map[int64]*clan.War),
		records:    make(map[int64]clan.WarRecord),
		busy:       make(map[[2]int64]bool),
		declaring:  make(map[int64]int),
		scoreDirty: make(map[int64]bool),
	}
}

// SetClanWars instala las guerras abiertas (con el marcador sumado de sus
// eventos), las terminadas recientes (para la tregua) y el historial de cada
// clan, al arrancar. Va después de SetClans y de SetClanAlliances. Devuelve las
// guerras que no instala porque no cumplen INV-CLAN-008: un clan que no está
// vivo, un par con dos abiertas o una abierta entre dos clanes aliados.
func (s *State) SetClanWars(open, recent []clan.War, records map[int64]clan.WarRecord) []WarViolation {
	s.wars = newWarState()
	var bad []WarViolation
	sort.Slice(open, func(i, j int) bool { return open[i].ID < open[j].ID })
	seen := make(map[[2]int64]bool)
	for _, g := range open {
		if !g.Open() {
			continue
		}
		_, okA := s.clans.clans[g.Attacker]
		_, okD := s.clans.clans[g.Defender]
		switch {
		case !okA || !okD:
			bad = append(bad, WarViolation{g.ID, "enfrenta a un clan que no está vivo"})
			continue
		case seen[g.Pair()]:
			bad = append(bad, WarViolation{g.ID, "el par ya tiene otra guerra abierta"})
			continue
		case s.openAlliance(g.Attacker, g.Defender, diplomacy.StatusActive) != nil:
			bad = append(bad, WarViolation{g.ID, "abierta entre dos clanes aliados"})
			continue
		}
		seen[g.Pair()] = true
		copia := g
		s.wars.open[g.ID] = &copia
	}
	for _, g := range recent {
		if g.Status == clan.WarEnded {
			s.wars.recent = append(s.wars.recent, g)
		}
	}
	for id, r := range records {
		s.wars.records[id] = r
	}
	return bad
}

// ClanWar es una guerra abierta (tests).
func (s *State) ClanWar(id int64) (clan.War, bool) {
	g, ok := s.wars.open[id]
	if !ok {
		return clan.War{}, false
	}
	return *g, true
}

// ClanWarsOf son las guerras abiertas de un clan, por id.
func (s *State) ClanWarsOf(clanID int64) []clan.War {
	var out []clan.War
	for _, g := range s.wars.open {
		if g.Has(clanID) {
			out = append(out, *g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ClanWarRecord es el historial de un clan (tests y vistas).
func (s *State) ClanWarRecord(clanID int64) clan.WarRecord { return s.wars.records[clanID] }

// openWarBetween es la guerra abierta entre dos clanes, si la hay.
func (s *State) openWarBetween(a, b int64) *clan.War {
	if a == b {
		return nil
	}
	k := clan.WarPair(a, b)
	for _, g := range s.wars.open {
		if g.Pair() == k {
			return g
		}
	}
	return nil
}

// clansAtWar dice si dos clanes tienen una guerra abierta o una declaración en
// vuelo (RN-CLANWAR-003, RN-CLANWAR-004).
func (s *State) clansAtWar(a, b int64) bool {
	if a == 0 || b == 0 || a == b {
		return false
	}
	return s.openWarBetween(a, b) != nil || s.wars.busy[clan.WarPair(a, b)]
}

// AtWar dice si dos jugadores son miembros de dos clanes con una guerra abierta
// o una declaración en vuelo: entre ellos no nace un pacto ni una alianza
// (RN-CLANWAR-004, INV-DIPLO-004).
func (s *State) AtWar(x, y uuid.UUID) bool {
	cx, okx := s.clans.memberOf[x]
	cy, oky := s.clans.memberOf[y]
	return okx && oky && s.clansAtWar(cx, cy)
}

// warBetweenPlayers es la guerra abierta entre los clanes de dos jugadores, con
// el clan del primero; nil si no la hay.
func (s *State) warBetweenPlayers(x, y uuid.UUID) (*clan.War, int64) {
	cx, okx := s.clans.memberOf[x]
	cy, oky := s.clans.memberOf[y]
	if !okx || !oky || cx == cy {
		return nil, 0
	}
	return s.openWarBetween(cx, cy), cx
}

// warPoints son los puntos que suma un hecho de author contra victim en nowMs:
// nil si no son miembros de los dos lados de una guerra activa
// (RN-CLANWAR-007).
func (s *State) warPoints(author, victim uuid.UUID, nowMs int64, points int64) *clan.WarPoints {
	if author == uuid.Nil || victim == uuid.Nil || author == victim {
		return nil
	}
	g, side := s.warBetweenPlayers(author, victim)
	if g == nil || !g.Scoring(nowMs) {
		return nil
	}
	return &clan.WarPoints{WarID: g.ID, ClanID: side, Points: points}
}

// warPointsVsClan son los puntos de un hecho de author contra el clan owner (un
// territorio de clan).
func (s *State) warPointsVsClan(author uuid.UUID, owner int64, nowMs int64, points int64) *clan.WarPoints {
	side, ok := s.clans.memberOf[author]
	if !ok || side == owner {
		return nil
	}
	g := s.openWarBetween(side, owner)
	if g == nil || !g.Scoring(nowMs) {
		return nil
	}
	return &clan.WarPoints{WarID: g.ID, ClanID: side, Points: points}
}

// addWarPoints suma al marcador de la RAM los puntos de un hecho, si su guerra
// sigue abierta, y apunta los dos clanes para la difusión.
func (s *State) addWarPoints(p *clan.WarPoints) {
	if p == nil {
		return
	}
	g, ok := s.wars.open[p.WarID]
	if !ok {
		return
	}
	*g = g.AddPoints(p.ClanID, p.Points)
	s.wars.scoreDirty[g.Attacker] = true
	s.wars.scoreDirty[g.Defender] = true
}

// openDeclaredBy cuenta las guerras abiertas que declaró un clan y sus
// declaraciones en vuelo: lo que cuenta para el tope (Q7).
func (s *State) openDeclaredBy(clanID int64) int {
	n := s.wars.declaring[clanID]
	for _, g := range s.wars.open {
		if g.Attacker == clanID {
			n++
		}
	}
	return n
}

// inWarCooldown dice si dos clanes terminaron una guerra hace menos de la
// tregua (RN-CLANWAR-006).
func (s *State) inWarCooldown(a, b int64, nowMs int64, cooldown time.Duration) bool {
	k := clan.WarPair(a, b)
	for _, g := range s.wars.recent {
		if g.Pair() == k && g.InCooldown(nowMs, cooldown) {
			return true
		}
	}
	return false
}

// storeWar deja en la RAM una guerra abierta, o la saca si terminó y apunta su
// resultado en el historial de los dos clanes y en la tregua.
func (s *State) storeWar(g clan.War) {
	if g.Open() {
		copia := g
		s.wars.open[g.ID] = &copia
		return
	}
	if _, abierta := s.wars.open[g.ID]; !abierta {
		return
	}
	delete(s.wars.open, g.ID)
	s.wars.recent = append(s.wars.recent, g)
	for _, c := range []int64{g.Attacker, g.Defender} {
		if _, vivo := s.clans.clans[c]; vivo {
			s.wars.records[c] = s.wars.records[c].Add(g.ResultFor(c))
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Configuración
// ─────────────────────────────────────────────────────────────

func (s *Simulation) clanMaxWars() int {
	if s.deps.ClanMaxWars > 0 {
		return s.deps.ClanMaxWars
	}
	return clan.DefaultMaxWars
}

func (s *Simulation) clanWarDuration() time.Duration {
	if s.deps.ClanWarDuration > 0 {
		return s.deps.ClanWarDuration
	}
	return clan.DefaultWarDuration
}

func (s *Simulation) clanWarCooldown() time.Duration {
	if s.deps.ClanWarCooldown > 0 {
		return s.deps.ClanWarCooldown
	}
	return clan.DefaultWarCooldown
}

func (s *Simulation) clanWarScorePush() time.Duration {
	if s.deps.ClanWarScorePush > 0 {
		return s.deps.ClanWarScorePush
	}
	return clan.DefaultWarScorePush
}

// ─────────────────────────────────────────────────────────────
// Vistas y difusión
// ─────────────────────────────────────────────────────────────

// warViews son las guerras abiertas de un clan vistas por uno de sus miembros;
// las ofertas de paz, sólo para el líder y los oficiales (RN-CLANWAR-009).
func (s *State) warViews(rec *clanRecord, viewer uuid.UUID) []protocol.ClanWarView {
	out := []protocol.ClanWarView{}
	managers := clan.CanInvite(rec.roleOf(viewer))
	for _, g := range s.ClanWarsOf(rec.ID) {
		otro, ok := s.clans.clans[g.Other(rec.ID)]
		if !ok {
			continue
		}
		v := protocol.ClanWarView{
			ID: g.ID, ClanID: otro.ID, Name: otro.Name, Tag: otro.Tag,
			DeclaredByUs: g.Attacker == rec.ID, Status: string(g.Status), StartsAtMs: g.StartsAtMs,
			OurScore: g.ScoreOf(rec.ID), TheirScore: g.ScoreOf(otro.ID),
		}
		if g.EndsAtMs > 0 {
			fin := g.EndsAtMs
			v.EndsAtMs = &fin
		}
		if managers {
			v.PeaceOfferedByUs, v.PeaceOfferedByThem = g.PeaceOfferedBy(rec.ID), g.PeaceOfferedBy(otro.ID)
		}
		out = append(out, v)
	}
	return out
}

// warRecordView es el historial de un clan.
func (s *State) warRecordView(clanID int64) protocol.ClanWarRecordView {
	r := s.wars.records[clanID]
	return protocol.ClanWarRecordView{Won: r.Won, Lost: r.Lost, Drawn: r.Drawn}
}

// sendWarUpdates manda clan.update a los miembros de los dos clanes.
func (s *Simulation) sendWarUpdates(g clan.War, actor uuid.UUID, requestID string) {
	players := append(s.state.ClanMembers(g.Attacker), s.state.ClanMembers(g.Defender)...)
	if actor != uuid.Nil {
		players = append(players, actor)
	}
	s.sendClanUpdates(players, actor, requestID)
}

// noticeWarMembers avisa de la declaración o del final a cada miembro de los
// clanes dados, con la sigla del otro clan y su lado del marcador (Q11). by es
// quien la dio, que no recibe aviso.
func (s *Simulation) noticeWarMembers(g clan.War, event string, by uuid.UUID, atMs int64, clans []int64) {
	for _, side := range clans {
		otro, ok := s.state.clans.clans[g.Other(side)]
		if !ok {
			continue
		}
		counterpart := by
		if counterpart == uuid.Nil {
			counterpart = otro.LeaderID
		}
		for _, m := range s.state.ClanMembers(side) {
			if m == by {
				continue
			}
			e := eventlog.Event{
				Type: event, PlayerID: m, Counterpart: counterpart, AtMs: atMs,
				OwnerClanTag: otro.Tag, Only: eventlog.OnlyPlayer, StartsAtMs: g.StartsAtMs,
				OurScore: g.ScoreOf(side), TheirScore: g.ScoreOf(otro.ID),
			}
			if g.Status == clan.WarEnded {
				e.WarResult, e.Reason = g.ResultFor(side), g.EndReason
			}
			s.sendNotices(e)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Órdenes (dos fases)
// ─────────────────────────────────────────────────────────────

// warringClan valida que quien da una orden de guerra es el líder de un clan
// que no se está disolviendo (RN-CLANWAR-002) y devuelve su clan, o responde el
// rechazo.
func (s *Simulation) warringClan(actor uuid.UUID, requestID string) (*clanRecord, bool) {
	if s.deps.Clans == nil {
		s.economyError(actor, requestID, protocol.CodeInternalError, "los clanes no están disponibles")
		return nil, false
	}
	rec, role, ok := s.state.clanRole(actor)
	if !ok || !clan.CanGovern(role) {
		s.economyError(actor, requestID, protocol.CodeInvalidTarget, msgOnlyLeaderWars)
		return nil, false
	}
	if rec.dissolving {
		s.economyError(actor, requestID, protocol.CodeInvalidTarget, "el clan se está disolviendo")
		return nil, false
	}
	return rec, true
}

func (s *Simulation) handleDeclareClanWar(cmd DeclareClanWar) {
	rec, ok := s.warringClan(cmd.PlayerID, cmd.RequestID)
	if !ok {
		return
	}
	reject := func(msg string) { s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, msg) }
	if cmd.ClanID == rec.ID {
		reject("no puedes declarar la guerra a tu propio clan")
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
	k := clan.WarPair(rec.ID, other.ID)
	if s.state.wars.busy[k] {
		reject(msgWarBusy)
		return
	}
	if s.state.openWarBetween(rec.ID, other.ID) != nil {
		reject("ya hay una guerra abierta con ese clan")
		return
	}
	// RN-CLANWAR-003: primero hay que romper la alianza; una aceptación en vuelo
	// cuenta como alianza.
	if s.state.openAlliance(rec.ID, other.ID, diplomacy.StatusActive) != nil || s.state.allies.accepting[k] {
		reject("vuestros clanes están aliados: rompe antes la alianza")
		return
	}
	if s.state.allies.busy[k] {
		reject("hay una orden de alianza en curso con ese clan")
		return
	}
	if s.state.openDeclaredBy(rec.ID) >= s.clanMaxWars() {
		reject("tu clan ya tiene el máximo de guerras declaradas")
		return
	}
	nowMs := s.deps.Clock.NowMs()
	if s.state.inWarCooldown(rec.ID, other.ID, nowMs, s.clanWarCooldown()) {
		reject("vuestros clanes terminaron una guerra hace poco: espera a que acabe la tregua")
		return
	}
	g := clan.War{
		Attacker: rec.ID, Defender: other.ID, DeclaredBy: cmd.PlayerID, RequestID: cmd.RequestID,
		Status: clan.WarDeclared, DeclaredAtMs: nowMs, StartsAtMs: nowMs + s.breakNotice().Milliseconds(),
	}
	s.state.wars.declaring[rec.ID]++
	s.submitWarOrder(WarWrite{
		Insert: true, War: g, From: clan.WarDeclared, Event: eventlog.EventClanWarDeclared, ByClan: rec.ID,
		MaxWars: s.clanMaxWars(), Cooldown: s.clanWarCooldown(),
	}, cmd.PlayerID, cmd.RequestID)
}

func (s *Simulation) handleClanWarPeace(cmd ClanWarPeace) {
	rec, ok := s.warringClan(cmd.PlayerID, cmd.RequestID)
	if !ok {
		return
	}
	reject := func(msg string) { s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, msg) }
	g, ok := s.state.wars.open[cmd.WarID]
	if !ok || !g.Has(rec.ID) {
		reject(msgWarNotExists)
		return
	}
	if s.state.wars.busy[g.Pair()] {
		reject(msgWarBusy)
		return
	}
	otro := g.Other(rec.ID)
	nowMs := s.deps.Clock.NowMs()
	var w WarWrite
	switch {
	case cmd.Offer && g.PeaceOfferedBy(otro):
		// La otra parte ya la ofreció: es una aceptación, y la guerra termina
		// en empate (RN-CLANWAR-008).
		after, err := g.End(clan.WarEndPeace, 0, nowMs)
		if err != nil {
			reject("esa guerra ya terminó")
			return
		}
		w = WarWrite{War: after, From: g.Status, Event: eventlog.EventClanWarEnded}
	case cmd.Offer:
		if g.PeaceOfferedBy(rec.ID) {
			reject("tu clan ya ofreció la paz")
			return
		}
		w = WarWrite{War: g.WithPeaceOffer(rec.ID, true), From: g.Status, Event: eventlog.EventClanWarPeaceOffered}
	case g.PeaceOfferedBy(rec.ID):
		w = WarWrite{War: g.WithPeaceOffer(rec.ID, false), From: g.Status, Event: eventlog.EventClanWarPeaceWithdrawn}
	case g.PeaceOfferedBy(otro):
		// Rechazar la oferta ajena la retira (SPEC-CLAN-WARS §4.3).
		w = WarWrite{War: g.WithPeaceOffer(otro, false), From: g.Status, Event: eventlog.EventClanWarPeaceWithdrawn}
	default:
		reject("no hay ninguna oferta de paz que retirar")
		return
	}
	w.ByClan = rec.ID
	s.submitWarOrder(w, cmd.PlayerID, cmd.RequestID)
}

func (s *Simulation) handleSurrenderClanWar(cmd SurrenderClanWar) {
	rec, ok := s.warringClan(cmd.PlayerID, cmd.RequestID)
	if !ok {
		return
	}
	reject := func(msg string) { s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, msg) }
	g, ok := s.state.wars.open[cmd.WarID]
	if !ok || !g.Has(rec.ID) {
		reject(msgWarNotExists)
		return
	}
	if s.state.wars.busy[g.Pair()] {
		reject(msgWarBusy)
		return
	}
	after, err := g.End(clan.WarEndSurrender, g.Other(rec.ID), s.deps.Clock.NowMs())
	if err != nil {
		reject("esa guerra ya terminó")
		return
	}
	s.submitWarOrder(WarWrite{War: after, From: g.Status, Event: eventlog.EventClanWarEnded, ByClan: rec.ID},
		cmd.PlayerID, cmd.RequestID)
}

// submitWarOrder reserva el par y encola la transacción de una orden, con las
// claves de los dos clanes en orden de id (ADR-045 §2).
func (s *Simulation) submitWarOrder(w WarWrite, playerID uuid.UUID, requestID string) {
	w.Tick = s.state.Tick()
	w.AtMs, w.By = s.deps.Clock.NowMs(), playerID
	k := w.War.Pair()
	s.state.wars.busy[k] = true
	resolved := ClanWarResolved{PlayerID: playerID, RequestID: requestID, Write: w}
	store := s.deps.Clans
	s.deps.Persister.Submit(Job{
		Name: JobClanWar,
		Key:  ClanKey(k[0]),
		Key2: ClanKey(k[1]),
		Run: func(ctx context.Context) error {
			id, err := store.ApplyWar(ctx, w)
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
			s.deps.Log.Error("no se pudo escribir la orden de guerra", "err", err, "event", w.Event)
			r := resolved
			r.Code, r.Message = protocol.CodeInternalError, "no se pudo aplicar la orden de guerra"
			s.reinject(r)
		},
	})
}

// handleClanWarResolved confirma una orden en la RAM y la difunde a los dos
// clanes, con los avisos, o libera la reserva y responde el rechazo.
func (s *Simulation) handleClanWarResolved(cmd ClanWarResolved) {
	w := cmd.Write
	k := w.War.Pair()
	delete(s.state.wars.busy, k)
	if w.Insert {
		if s.state.wars.declaring[w.War.Attacker]--; s.state.wars.declaring[w.War.Attacker] <= 0 {
			delete(s.state.wars.declaring, w.War.Attacker)
		}
	}
	if cmd.Code != "" {
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, cmd.Message)
		return
	}
	g := w.War
	if w.Insert {
		g.ID = cmd.ID
	} else if cur, ok := s.state.wars.open[g.ID]; ok {
		// El marcador siguió corriendo mientras la orden esperaba la base: la
		// fila lleva el del tick de la orden, y la RAM se queda con el suyo en
		// una guerra que sigue abierta.
		if g.Open() {
			g.AttackerScore, g.DefenderScore = cur.AttackerScore, cur.DefenderScore
		}
	} else {
		return // otra vía la cerró (la disolución, ADR-045 §5)
	}
	if _, okA := s.state.clans.clans[g.Attacker]; !okA {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ese clan ya no existe")
		return
	}
	if _, okD := s.state.clans.clans[g.Defender]; !okD {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ese clan ya no existe")
		return
	}
	s.state.storeWar(g)
	nowMs := s.deps.Clock.NowMs()
	if w.Insert {
		// RN-CLANWAR-003: la oferta de alianza entre los dos caducó en la misma
		// transacción.
		if a := s.state.openAlliance(g.Attacker, g.Defender, diplomacy.StatusProposed); a != nil {
			if after, err := a.TransitionTo(diplomacy.StatusExpired, w.AtMs); err == nil {
				s.state.storeAlliance(after)
			}
		}
	}
	s.sendWarUpdates(g, cmd.PlayerID, cmd.RequestID)
	switch w.Event {
	case eventlog.EventClanWarDeclared, eventlog.EventClanWarEnded:
		s.noticeWarMembers(g, w.Event, cmd.PlayerID, w.AtMs, []int64{g.Attacker, g.Defender})
	case eventlog.EventClanWarPeaceOffered:
		// Q11: al líder del otro clan.
		if otro, ok := s.state.clans.clans[g.Other(w.ByClan)]; ok {
			s.sendNotices(eventlog.Event{
				Type: w.Event, PlayerID: otro.LeaderID, Counterpart: cmd.PlayerID, AtMs: w.AtMs,
				OwnerClanTag: s.state.clanTag(w.ByClan), Only: eventlog.OnlyPlayer, StartsAtMs: g.StartsAtMs,
			})
		}
	}
	if w.Insert {
		// ADR-045 §3: los pactos entre los dos lados empiezan su preaviso en el
		// tick en que vuelve la declaración.
		s.enforceWarTreaties(nowMs)
	}
	s.deps.Log.Info("guerra de clan", "event", w.Event, "war_id", g.ID, "attacker", g.Attacker, "defender", g.Defender)
}

// ─────────────────────────────────────────────────────────────
// El compromiso sobre los tratados (ADR-045 §3)
// ─────────────────────────────────────────────────────────────

// enforceWarTreaties abre el preaviso de cada pacto o alianza vigente sin
// preaviso entre miembros de dos clanes en guerra abierta, y expira sus ofertas
// (RN-CLANWAR-004, INV-DIPLO-004). Un tratado con una orden en vuelo se mira en
// la pasada siguiente. La ruptura es como si la hubiera dado el miembro del
// clan que declaró.
func (s *Simulation) enforceWarTreaties(nowMs int64) {
	if len(s.state.wars.open) == 0 || len(s.state.diplo.open) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.diplo.open))
	for id := range s.state.diplo.open {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	now := time.UnixMilli(nowMs).UTC()
	for _, id := range ids {
		t := s.state.diplo.open[id]
		if t.Type != diplomacy.TypeNonAggression && t.Type != diplomacy.TypeAlliance {
			continue
		}
		g, sideA := s.state.warBetweenPlayers(t.PlayerA, t.PlayerB)
		if g == nil {
			continue
		}
		if _, ocupado := s.state.diplo.busy[pairKey(t.PlayerA, t.PlayerB)]; ocupado {
			continue // una orden en vuelo: la pasada siguiente
		}
		by := t.PlayerB
		if sideA == g.Attacker {
			by = t.PlayerA
		}
		var w TreatyWrite
		switch {
		case t.Status == diplomacy.StatusActive && t.BreakEffectiveAt == nil:
			after := *t
			fin := now.Add(s.breakNotice())
			after.BreakEffectiveAt = &fin
			w = TreatyWrite{Treaty: after, From: diplomacy.StatusActive, Event: "TreatyBreakAnnounced"}
		case t.Status == diplomacy.StatusProposed:
			after, err := t.TransitionTo(diplomacy.StatusExpired, now)
			if err != nil {
				continue
			}
			w = TreatyWrite{Treaty: after, From: diplomacy.StatusProposed, Event: "TreatyExpired"}
		default:
			continue
		}
		w.Tick, w.AtMs, w.By, w.Reason = s.state.Tick(), nowMs, by, eventlog.BreakReasonWar
		s.storeTreaty(w.Treaty)
		s.broadcastTreaty(w.Treaty, uuid.Nil, "")
		s.submitTreatyEnd(w)
		s.deps.Log.Info("tratado roto por una guerra", "treaty_id", t.ID, "war_id", g.ID, "event", w.Event)
	}
}

// ─────────────────────────────────────────────────────────────
// La disolución (ADR-045 §5)
// ─────────────────────────────────────────────────────────────

// warEndsOf son las guerras abiertas de un clan que se disuelve, terminadas en
// nowMs con la victoria del otro y el marcador de la RAM (RN-CLANWAR-008).
func (s *State) warEndsOf(clanID int64, nowMs int64) []clan.War {
	var out []clan.War
	for _, g := range s.ClanWarsOf(clanID) {
		if after, err := g.End(clan.WarEndDissolved, g.Other(clanID), nowMs); err == nil {
			out = append(out, after)
		}
	}
	return out
}

// applyWarEnds cierra en la RAM las guerras que terminó una disolución, en el
// mismo tick que la disolución, y avisa al otro clan.
func (s *Simulation) applyWarEnds(clanID int64, ends []clan.War, by uuid.UUID) {
	for _, g := range ends {
		delete(s.state.wars.busy, g.Pair())
		s.state.storeWar(g)
		otro := g.Other(clanID)
		s.sendClanUpdates(s.state.ClanMembers(otro), uuid.Nil, "")
		s.noticeWarMembers(g, eventlog.EventClanWarEnded, by, g.EndedAtMs, []int64{otro})
	}
}

// ─────────────────────────────────────────────────────────────
// Plazos (fase 2, optimistas)
// ─────────────────────────────────────────────────────────────

// sweepClanWars aplica, una vez por segundo, el comienzo de las guerras cuya
// preparación venció y el final por plazo, olvida las treguas vencidas, aplica
// el compromiso sobre los tratados y difunde los marcadores que cambiaron
// (ADR-045 §2, §3).
func (s *Simulation) sweepClanWars(nowMs int64) {
	st := &s.state.wars
	if nowMs < st.nextSweepMs {
		return
	}
	st.nextSweepMs = nowMs + warSweepInterval.Milliseconds()

	if len(st.failedEnds) > 0 {
		pendientes := st.failedEnds
		st.failedEnds = nil
		for _, w := range pendientes {
			s.submitWarEnd(w)
		}
	}
	if len(st.recent) > 0 {
		cooldown := s.clanWarCooldown()
		vivas := st.recent[:0]
		for _, g := range st.recent {
			if g.InCooldown(nowMs, cooldown) {
				vivas = append(vivas, g)
			}
		}
		st.recent = vivas
	}

	ids := make([]int64, 0, len(st.open))
	for id := range st.open {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		g := st.open[id]
		if st.busy[g.Pair()] {
			continue // una orden en vuelo: el barrido siguiente
		}
		var w WarWrite
		switch {
		case g.Status == clan.WarDeclared && nowMs >= g.StartsAtMs:
			after, err := g.Start(g.StartsAtMs, s.clanWarDuration())
			if err != nil {
				continue
			}
			w = WarWrite{War: after, From: clan.WarDeclared, Event: eventlog.EventClanWarStarted, AtMs: g.StartsAtMs}
		case g.Status == clan.WarActive && nowMs >= g.EndsAtMs:
			after, err := g.End(clan.WarEndTimeout, g.LeaderByScore(), g.EndsAtMs)
			if err != nil {
				continue
			}
			w = WarWrite{War: after, From: clan.WarActive, Event: eventlog.EventClanWarEnded, AtMs: g.EndsAtMs}
		default:
			continue
		}
		s.state.storeWar(w.War)
		s.sendWarUpdates(w.War, uuid.Nil, "")
		if w.Event == eventlog.EventClanWarEnded {
			s.noticeWarMembers(w.War, w.Event, uuid.Nil, w.AtMs, []int64{w.War.Attacker, w.War.Defender})
		}
		w.Tolerant, w.Tick = true, s.state.Tick()
		s.submitWarEnd(w)
	}

	s.enforceWarTreaties(nowMs)

	if len(st.scoreDirty) > 0 && nowMs >= st.nextScorePushMs {
		st.nextScorePushMs = nowMs + s.clanWarScorePush().Milliseconds()
		clans := make([]int64, 0, len(st.scoreDirty))
		for c := range st.scoreDirty {
			clans = append(clans, c)
		}
		sort.Slice(clans, func(i, j int) bool { return clans[i] < clans[j] })
		st.scoreDirty = make(map[int64]bool)
		var players []uuid.UUID
		for _, c := range clans {
			players = append(players, s.state.ClanMembers(c)...)
		}
		s.sendClanUpdates(players, uuid.Nil, "")
	}
}

func (s *Simulation) submitWarEnd(w WarWrite) {
	if s.deps.Clans == nil {
		return
	}
	store := s.deps.Clans
	k := w.War.Pair()
	s.deps.Persister.Submit(Job{
		Name: JobClanWarEnd,
		Key:  ClanKey(k[0]),
		Key2: ClanKey(k[1]),
		Run: func(ctx context.Context) error {
			if _, err := store.ApplyWar(ctx, w); err != nil && !errors.Is(err, ErrClanConflict) {
				return err
			}
			return nil
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir un plazo de una guerra; se reintentará",
				"err", err, "war_id", w.War.ID)
			s.reinject(ClanWarEndPersistFailed{Write: w})
		},
	})
}

func (s *Simulation) handleClanWarEndPersistFailed(cmd ClanWarEndPersistFailed) {
	s.state.wars.failedEnds = append(s.state.wars.failedEnds, cmd.Write)
}
