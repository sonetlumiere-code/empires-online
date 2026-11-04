package simulation

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/garrison"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La diplomacia en la simulación (SPEC-DIPLOMACY, ADR-027).
//
// La RAM es la fuente de verdad de los tratados abiertos (PROPOSED y ACTIVE):
// cada orden de un jugador se valida contra ella y va en dos fases —el tick
// reserva el par, la transacción escribe, el buzón confirma—, y la paz que lee
// el combate sale de ella, evaluada por fecha. Los vencimientos (oferta
// caducada, preaviso vencido, expires_at) se aplican en la fase 5 de forma
// optimista y se escriben después, con compensación.

// treatySweepInterval es cada cuánto mira la fase 5 los vencimientos. Es un
// recorrido de la RAM, sin consultar la base; un tratado que vence un segundo
// tarde no cambia el juego, porque la paz ya se evalúa por fecha.
const treatySweepInterval = time.Second

// Valores por defecto de SPEC-DIPLOMACY §10.
const (
	defaultTreatyOfferTTL    = 24 * time.Hour
	defaultTreatyBreakNotice = 15 * time.Minute
	defaultTreatyRetry       = 10 * time.Minute
)

// ErrTreatyConflict: la base no admite la escritura porque el tratado o el par
// cambiaron por otra vía (un índice de INV-DIPLO-002, o un estado distinto del
// esperado). Es un rechazo de dominio: no se reintenta.
var ErrTreatyConflict = errors.New("el tratado cambió por otra vía")

// TreatyStore escribe las órdenes y los vencimientos de los tratados. Lo
// implementa persistence.TreatyStore. Es idempotente: la cola puede reintentar.
type TreatyStore interface {
	ApplyTreaty(ctx context.Context, w TreatyWrite) (int64, error)
}

// TreatyWrite es una orden o un vencimiento ya decididos por la simulación.
type TreatyWrite struct {
	// Insert: una oferta nueva, sin id todavía.
	Insert bool
	// Treaty es el tratado tal como queda.
	Treaty diplomacy.Treaty
	// From es el estado que debe tener la fila para aplicarla.
	From diplomacy.Status
	// Supersede es el pacto que cierra una alianza aceptada; nil si no hay.
	Supersede *diplomacy.Treaty
	Event     string
	Tick      uint64
	// AtMs es el instante de la transición en la simulación, y By quien dio la
	// orden (uuid.Nil en un vencimiento): los dos van al payload del evento
	// para el registro (ADR-028 §3).
	AtMs int64
	By   uuid.UUID
}

// Comandos de los jugadores (protocolo v1).

// ProposeTreaty propone un tratado a otro jugador.
type ProposeTreaty struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	Target    uuid.UUID
	Type      diplomacy.Type
}

func (ProposeTreaty) commandType() string { return protocol.TypeTreatyPropose }

// RespondTreaty acepta o rechaza una oferta recibida.
type RespondTreaty struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	TreatyID  int64
	Accept    bool
}

func (RespondTreaty) commandType() string { return protocol.TypeTreatyRespond }

// CancelTreaty retira la oferta propia o rompe un tratado vigente.
type CancelTreaty struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	TreatyID  int64
}

func (CancelTreaty) commandType() string { return protocol.TypeTreatyCancel }

// TreatyResolved lo devuelve la cola con el resultado de una orden.
type TreatyResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	Write     TreatyWrite
	// ID es el que dio la base a una oferta nueva.
	ID int64
	// Code, si no está vacío, es el rechazo: la transacción no escribió nada.
	Code string
}

func (TreatyResolved) commandType() string { return "treaty.resolved" }

// TreatyEndPersistFailed es la compensación de un vencimiento que no se
// escribió: el barrido siguiente lo vuelve a encolar (ADR-027 §3).
type TreatyEndPersistFailed struct{ Write TreatyWrite }

func (TreatyEndPersistFailed) commandType() string { return "treaty.end_failed" }

// Nombres de los trabajos de la diplomacia (catálogo JobNames).
const (
	JobTreatyOrder = "treaty.order"
	JobTreatyEnd   = "treaty.end"
)

// TreatyKey es la clave de la cola de un par de jugadores: todas las
// escrituras de sus tratados van en orden (ADR-027 §2).
func TreatyKey(x, y uuid.UUID) string {
	a, b := diplomacy.CanonicalPair(x, y)
	return "treaty:" + a.String() + ":" + b.String()
}

// diplomacyState es lo que la simulación sabe de los tratados.
type diplomacyState struct {
	// open son los tratados PROPOSED y ACTIVE, por id.
	open map[int64]*diplomacy.Treaty
	// busy son los pares con una orden esperando su transacción.
	busy map[[2]uuid.UUID]struct{}
	// rejected es, por (quien propuso, a quién), cuándo se rechazó su última
	// oferta (RN-DIPLO-008).
	rejected map[[2]uuid.UUID]time.Time
	// failedEnds son los vencimientos cuya escritura se descartó (ADR-022).
	failedEnds []TreatyWrite
}

func newDiplomacyState() diplomacyState {
	return diplomacyState{
		open:     make(map[int64]*diplomacy.Treaty),
		busy:     make(map[[2]uuid.UUID]struct{}),
		rejected: make(map[[2]uuid.UUID]time.Time),
	}
}

func pairKey(x, y uuid.UUID) [2]uuid.UUID {
	a, b := diplomacy.CanonicalPair(x, y)
	return [2]uuid.UUID{a, b}
}

// SetTreaties instala los tratados abiertos y los rechazos recientes leídos al
// arrancar (ADR-027 §1).
func (s *State) SetTreaties(open, rejected []diplomacy.Treaty) {
	s.diplo = newDiplomacyState()
	for _, t := range open {
		if t.Status != diplomacy.StatusProposed && t.Status != diplomacy.StatusActive {
			continue
		}
		copia := t
		// El par se guarda canónico, que es como lo busca openOf: la base ya lo
		// garantiza (treaties_canonical_pair), pero un llamante que no lo
		// ordene no debe quedarse sin paz (regresión de la CI de 800a269).
		copia.PlayerA, copia.PlayerB = diplomacy.CanonicalPair(t.PlayerA, t.PlayerB)
		s.diplo.open[t.ID] = &copia
	}
	for _, t := range rejected {
		if t.ProposedBy == nil || t.ClosedAt == nil {
			continue
		}
		s.noteRejection(t)
	}
}

// SetPeaceTreaties instala tratados vigentes sin rechazos: el atajo de los
// tests que sólo miran la paz.
func (s *State) SetPeaceTreaties(ts []diplomacy.Treaty) { s.SetTreaties(ts, nil) }

func (s *State) noteRejection(t diplomacy.Treaty) {
	k := [2]uuid.UUID{*t.ProposedBy, t.Other(*t.ProposedBy)}
	if prev, ok := s.diplo.rejected[k]; !ok || t.ClosedAt.After(prev) {
		s.diplo.rejected[k] = *t.ClosedAt
	}
}

// AtPeace indica si dos jugadores tienen un tratado vigente que impide atacar
// (RN-COMBAT-006, RN-COMBAT-020), también durante un preaviso.
func (s *State) AtPeace(x, y uuid.UUID, now time.Time) bool {
	for _, t := range s.openOf(x, y) {
		if t.GivesPeaceAt(now) {
			return true
		}
	}
	return false
}

// Treaty devuelve un tratado abierto (tests).
func (s *State) Treaty(id int64) (diplomacy.Treaty, bool) {
	t, ok := s.diplo.open[id]
	if !ok {
		return diplomacy.Treaty{}, false
	}
	return *t, true
}

// TreatiesBetween devuelve los tratados abiertos de un par, por id (tests).
func (s *State) TreatiesBetween(x, y uuid.UUID) []diplomacy.Treaty {
	out := make([]diplomacy.Treaty, 0, 2)
	for _, t := range s.openOf(x, y) {
		out = append(out, *t)
	}
	return out
}

// openOf devuelve los tratados abiertos de un par, en orden de id.
func (s *State) openOf(x, y uuid.UUID) []*diplomacy.Treaty {
	k := pairKey(x, y)
	var out []*diplomacy.Treaty
	for _, t := range s.diplo.open {
		if t.PlayerA == k[0] && t.PlayerB == k[1] {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Simulation) offerTTL() time.Duration {
	if s.deps.TreatyOfferTTL > 0 {
		return s.deps.TreatyOfferTTL
	}
	return defaultTreatyOfferTTL
}

func (s *Simulation) breakNotice() time.Duration {
	if s.deps.TreatyBreakNotice > 0 {
		return s.deps.TreatyBreakNotice
	}
	return defaultTreatyBreakNotice
}

func (s *Simulation) treatyRetry() time.Duration {
	if s.deps.TreatyRetry > 0 {
		return s.deps.TreatyRetry
	}
	return defaultTreatyRetry
}

// now del tick con precisión de milisegundo: la base guarda microsegundos, y un
// instante con nanosegundos no se reconocería al reintentar (ADR-027 §2).
func (s *Simulation) treatyNow() time.Time { return time.UnixMilli(s.deps.Clock.NowMs()).UTC() }

// ─────────────────────────────────────────────────────────────
// Órdenes (dos fases)
// ─────────────────────────────────────────────────────────────

func (s *Simulation) treatyReject(playerID uuid.UUID, requestID, msg string) {
	s.economyError(playerID, requestID, protocol.CodeInvalidTarget, msg)
}

func (s *Simulation) handleProposeTreaty(cmd ProposeTreaty) {
	if s.deps.Treaties == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "la diplomacia no está disponible")
		return
	}
	if !cmd.Type.Proposable() {
		s.treatyReject(cmd.PlayerID, cmd.RequestID, "ese tipo de tratado no se puede proponer")
		return
	}
	if cmd.Target == cmd.PlayerID {
		s.treatyReject(cmd.PlayerID, cmd.RequestID, "no puedes firmar un tratado contigo mismo")
		return
	}
	if _, ok := s.state.CityOf(cmd.Target); !ok {
		s.treatyReject(cmd.PlayerID, cmd.RequestID, "ese jugador no existe")
		return
	}
	k := pairKey(cmd.PlayerID, cmd.Target)
	if _, ocupado := s.state.diplo.busy[k]; ocupado {
		s.treatyReject(cmd.PlayerID, cmd.RequestID, "hay otra orden en curso con ese jugador")
		return
	}
	// RN-DIPLO-001 e INV-DIPLO-002: ninguna oferta pendiente, y como vigente
	// sólo un pacto que se quiera convertir en alianza.
	for _, t := range s.state.openOf(cmd.PlayerID, cmd.Target) {
		if t.Status == diplomacy.StatusProposed {
			s.treatyReject(cmd.PlayerID, cmd.RequestID, "ya hay una oferta pendiente con ese jugador")
			return
		}
		mejora := t.Type == diplomacy.TypeNonAggression && cmd.Type == diplomacy.TypeAlliance && t.BreakEffectiveAt == nil
		if !mejora {
			s.treatyReject(cmd.PlayerID, cmd.RequestID, "ya tenéis un tratado vigente")
			return
		}
	}
	now := s.treatyNow()
	if cuando, ok := s.state.diplo.rejected[[2]uuid.UUID{cmd.PlayerID, cmd.Target}]; ok && now.Before(cuando.Add(s.treatyRetry())) {
		s.treatyReject(cmd.PlayerID, cmd.RequestID, "ese jugador rechazó tu oferta hace poco: espera antes de insistir")
		return
	}

	quien := cmd.PlayerID
	t := diplomacy.Treaty{
		PlayerA: k[0], PlayerB: k[1], Type: cmd.Type, Status: diplomacy.StatusProposed,
		AllowsGarrison: cmd.Type.GrantsGarrison(), ProposedAt: now, ProposedBy: &quien, RequestID: cmd.RequestID,
	}
	s.submitTreatyOrder(TreatyWrite{Insert: true, Treaty: t, From: diplomacy.StatusProposed, Event: "TreatyProposed"},
		cmd.PlayerID, cmd.RequestID)
}

func (s *Simulation) handleRespondTreaty(cmd RespondTreaty) {
	t, ok := s.state.diplo.open[cmd.TreatyID]
	if !ok {
		s.treatyReject(cmd.PlayerID, cmd.RequestID, "ese tratado no existe")
		return
	}
	// INV-DIPLO-001: sólo responde el firmante que no propuso.
	if err := t.CanRespond(cmd.PlayerID); err != nil {
		s.treatyReject(cmd.PlayerID, cmd.RequestID, "no puedes responder a esa oferta")
		return
	}
	if _, ocupado := s.state.diplo.busy[pairKey(t.PlayerA, t.PlayerB)]; ocupado {
		s.treatyReject(cmd.PlayerID, cmd.RequestID, "hay otra orden en curso sobre ese tratado")
		return
	}
	next, event := diplomacy.StatusRejected, "TreatyRejected"
	if cmd.Accept {
		next, event = diplomacy.StatusActive, "TreatyAccepted"
	}
	after, err := t.TransitionTo(next, s.treatyNow())
	if err != nil {
		s.treatyReject(cmd.PlayerID, cmd.RequestID, "esa oferta ya no se puede responder")
		return
	}
	w := TreatyWrite{Treaty: after, From: diplomacy.StatusProposed, Event: event}
	if cmd.Accept {
		// Una alianza aceptada sustituye al pacto vigente (§4.1).
		for _, o := range s.state.openOf(t.PlayerA, t.PlayerB) {
			if o.ID != t.ID && o.Status == diplomacy.StatusActive {
				pacto := *o
				w.Supersede = &pacto
			}
		}
	}
	s.submitTreatyOrder(w, cmd.PlayerID, cmd.RequestID)
}

func (s *Simulation) handleCancelTreaty(cmd CancelTreaty) {
	t, ok := s.state.diplo.open[cmd.TreatyID]
	if !ok {
		s.treatyReject(cmd.PlayerID, cmd.RequestID, "ese tratado no existe")
		return
	}
	if _, ocupado := s.state.diplo.busy[pairKey(t.PlayerA, t.PlayerB)]; ocupado {
		s.treatyReject(cmd.PlayerID, cmd.RequestID, "hay otra orden en curso sobre ese tratado")
		return
	}
	now := s.treatyNow()
	switch t.Status {
	case diplomacy.StatusProposed:
		if err := t.CanWithdraw(cmd.PlayerID); err != nil {
			s.treatyReject(cmd.PlayerID, cmd.RequestID, "sólo quien propuso puede retirar la oferta")
			return
		}
		after, _ := t.TransitionTo(diplomacy.StatusWithdrawn, now)
		s.submitTreatyOrder(TreatyWrite{Treaty: after, From: diplomacy.StatusProposed, Event: "TreatyWithdrawn"},
			cmd.PlayerID, cmd.RequestID)
	default:
		// RN-DIPLO-003 y RN-DIPLO-004: romper abre el preaviso; el tratado
		// sigue ACTIVE hasta su fin.
		if err := t.CanBreak(cmd.PlayerID); err != nil {
			s.treatyReject(cmd.PlayerID, cmd.RequestID, "ese tratado no se puede romper")
			return
		}
		after := *t
		fin := now.Add(s.breakNotice())
		after.BreakEffectiveAt = &fin
		s.submitTreatyOrder(TreatyWrite{Treaty: after, From: diplomacy.StatusActive, Event: "TreatyBreakAnnounced"},
			cmd.PlayerID, cmd.RequestID)
	}
}

// submitTreatyOrder reserva el par y encola la transacción de una orden.
func (s *Simulation) submitTreatyOrder(w TreatyWrite, playerID uuid.UUID, requestID string) {
	w.Tick = s.state.Tick()
	w.AtMs, w.By = s.deps.Clock.NowMs(), playerID
	k := pairKey(w.Treaty.PlayerA, w.Treaty.PlayerB)
	s.state.diplo.busy[k] = struct{}{}
	resolved := TreatyResolved{PlayerID: playerID, RequestID: requestID, Write: w}
	store := s.deps.Treaties
	s.deps.Persister.Submit(Job{
		Name: JobTreatyOrder,
		Key:  TreatyKey(k[0], k[1]),
		Run: func(ctx context.Context) error {
			id, err := store.ApplyTreaty(ctx, w)
			if err == nil {
				r := resolved
				r.ID = id
				s.reinject(r)
				return nil
			}
			if errors.Is(err, ErrTreatyConflict) {
				r := resolved
				r.Code = protocol.CodeInvalidTarget
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la orden de tratado", "err", err, "event", w.Event)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// handleTreatyResolved confirma una orden en la RAM, la difunde a los dos
// firmantes, o libera la reserva y responde el rechazo.
func (s *Simulation) handleTreatyResolved(cmd TreatyResolved) {
	w := cmd.Write
	delete(s.state.diplo.busy, pairKey(w.Treaty.PlayerA, w.Treaty.PlayerB))
	if cmd.Code != "" {
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo aplicar la orden de tratado")
		return
	}
	t := w.Treaty
	if w.Insert {
		t.ID = cmd.ID
	}
	if w.Supersede != nil {
		delete(s.state.diplo.open, w.Supersede.ID)
		cerrado := *w.Supersede
		cerrado.Status = diplomacy.StatusExpired
		s.broadcastTreaty(cerrado, uuid.Nil, "")
	}
	s.storeTreaty(t)
	if t.Status == diplomacy.StatusRejected {
		s.state.noteRejection(t)
	}
	s.broadcastTreaty(t, cmd.PlayerID, cmd.RequestID)
}

// storeTreaty deja en la RAM un tratado abierto, o lo saca si terminó.
func (s *Simulation) storeTreaty(t diplomacy.Treaty) {
	if t.Status == diplomacy.StatusProposed || t.Status == diplomacy.StatusActive {
		copia := t
		s.state.diplo.open[t.ID] = &copia
		return
	}
	delete(s.state.diplo.open, t.ID)
}

// ─────────────────────────────────────────────────────────────
// Vencimientos (fase 5, optimistas)
// ─────────────────────────────────────────────────────────────

// sweepTreaties aplica, una vez por segundo, las ofertas caducadas, los
// preavisos vencidos y los expires_at alcanzados (ADR-027 §3).
func (s *Simulation) sweepTreaties(now time.Time) {
	if now.Sub(s.lastTreatySweep) < treatySweepInterval {
		return
	}
	s.lastTreatySweep = now
	now = time.UnixMilli(now.UnixMilli()).UTC()

	if len(s.state.diplo.failedEnds) > 0 {
		pendientes := s.state.diplo.failedEnds
		s.state.diplo.failedEnds = nil
		for _, w := range pendientes {
			s.submitTreatyEnd(w)
		}
	}

	ids := make([]int64, 0, len(s.state.diplo.open))
	for id := range s.state.diplo.open {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		t := s.state.diplo.open[id]
		if _, ocupado := s.state.diplo.busy[pairKey(t.PlayerA, t.PlayerB)]; ocupado {
			continue // una orden en vuelo: se mira en el barrido siguiente
		}
		var w TreatyWrite
		switch t.Status {
		case diplomacy.StatusProposed:
			// RN-DIPLO-007: la oferta caduca.
			if now.Before(t.ProposedAt.Add(s.offerTTL())) {
				continue
			}
			after, err := t.TransitionTo(diplomacy.StatusExpired, now)
			if err != nil {
				continue
			}
			w = TreatyWrite{Treaty: after, From: diplomacy.StatusProposed, Event: "TreatyExpired", AtMs: now.UnixMilli()}
		case diplomacy.StatusActive:
			fin, estado, ok := t.EndsAt()
			if !ok || fin.After(now) {
				continue
			}
			after, err := t.TransitionTo(estado, fin)
			if err != nil {
				continue
			}
			event := "TreatyExpired"
			if estado == diplomacy.StatusBroken {
				event = "TreatyBroken"
			}
			w = TreatyWrite{Treaty: after, From: diplomacy.StatusActive, Event: event, AtMs: fin.UnixMilli()}
		default:
			continue
		}
		s.storeTreaty(w.Treaty)
		s.broadcastTreaty(w.Treaty, uuid.Nil, "")
		// RN-DIPLO-005 e INV-GARR-008: al terminar una alianza, sus guarniciones salen.
		if t.Status == diplomacy.StatusActive && t.AllowsGarrison {
			s.expelGarrisons(t.PlayerA, t.PlayerB)
		}
		w.Tick = s.state.Tick()
		s.submitTreatyEnd(w)
	}
}

func (s *Simulation) submitTreatyEnd(w TreatyWrite) {
	if s.deps.Treaties == nil {
		return
	}
	store := s.deps.Treaties
	s.deps.Persister.Submit(Job{
		Name: JobTreatyEnd,
		Key:  TreatyKey(w.Treaty.PlayerA, w.Treaty.PlayerB),
		Run: func(ctx context.Context) error {
			if _, err := store.ApplyTreaty(ctx, w); err != nil && !errors.Is(err, ErrTreatyConflict) {
				return err
			}
			return nil
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el fin de un tratado; se reintentará",
				"err", err, "treaty_id", w.Treaty.ID)
			s.reinject(TreatyEndPersistFailed{Write: w})
		},
	})
}

func (s *Simulation) handleTreatyEndPersistFailed(cmd TreatyEndPersistFailed) {
	s.state.diplo.failedEnds = append(s.state.diplo.failedEnds, cmd.Write)
}

// expelGarrisons saca, en orden de unidad, a cada unidad de uno guarnecida en la
// ciudad del otro, por la salida normal (ADR-027 §6). La que no encuentra tile
// queda pendiente y se reintenta cada tick (RN-GARR-014).
func (s *Simulation) expelGarrisons(a, b uuid.UUID) {
	ids := make([]int64, 0)
	for id := range s.state.garrisonedIn {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		u, ok := s.state.Unit(id)
		host, hostOK := s.state.City(s.state.garrisonedIn[id])
		if !ok || !hostOK {
			continue
		}
		cruzada := (u.PlayerID == a && host.OwnerPlayerID == b) || (u.PlayerID == b && host.OwnerPlayerID == a)
		if !cruzada {
			continue // la ciudad propia no depende de ningún tratado (RN-GARR-019)
		}
		if _, saliendo := s.state.pendingExits[id]; saliendo {
			continue
		}
		req := exitRequest{PlayerID: u.PlayerID, CityID: host.ID}
		if err := s.exit(u, host, req); errors.Is(err, garrison.ErrNoReentryTile) {
			s.state.pendingExits[id] = req
		}
		s.deps.Log.Info("unidad expulsada al terminar la alianza", "unit_id", id, "city_id", host.ID)
	}
}

// ─────────────────────────────────────────────────────────────
// Vistas
// ─────────────────────────────────────────────────────────────

// treatyView es el tratado visto por uno de sus firmantes.
func (s *Simulation) treatyView(t diplomacy.Treaty, viewer uuid.UUID) protocol.TreatyView {
	other := t.Other(viewer)
	v := protocol.TreatyView{
		ID: t.ID, OtherPlayerID: other.String(), Type: string(t.Type), Status: string(t.Status),
		ProposedByMe: t.ProposedBy != nil && *t.ProposedBy == viewer,
	}
	if c, ok := s.state.CityOf(other); ok {
		id, name := c.ID, c.Name
		v.OtherCityID, v.OtherCityName = &id, &name
	}
	if t.BreakEffectiveAt != nil {
		ms := t.BreakEffectiveAt.UnixMilli()
		v.BreaksAtMs = &ms
	}
	return v
}

// broadcastTreaty lleva un tratado a los dos firmantes y a nadie más (Q8). El
// que dio la orden lo recibe con su requestId.
func (s *Simulation) broadcastTreaty(t diplomacy.Treaty, orderedBy uuid.UUID, requestID string) {
	for _, p := range []uuid.UUID{t.PlayerA, t.PlayerB} {
		rid := ""
		if p == orderedBy {
			rid = requestID
		}
		s.deps.Broadcaster.SendToPlayer(p, protocol.TypeTreatyUpdate, rid,
			protocol.TreatyUpdatePayload{Treaty: s.treatyView(t, p)})
	}
}

// treatiesOf son los tratados abiertos de un jugador, por id, para su snapshot.
func (s *Simulation) treatiesOf(viewer uuid.UUID) []protocol.TreatyView {
	var ts []*diplomacy.Treaty
	for _, t := range s.state.diplo.open {
		if t.HasParty(viewer) {
			ts = append(ts, t)
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
	out := make([]protocol.TreatyView, 0, len(ts))
	for _, t := range ts {
		out = append(out, s.treatyView(*t, viewer))
	}
	return out
}
