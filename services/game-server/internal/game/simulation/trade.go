package simulation

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/trade"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El comercio en la simulación (SPEC-TRADE, ADR-032).
//
// La RAM es la fuente de verdad de las ofertas pendientes, como de los
// tratados: cada orden se valida contra ella y va en dos fases —el tick
// reserva el par, la transacción escribe, el buzón confirma—. Aceptar reserva
// además lo que da cada parte en su almacén, y su transacción mueve los dos
// almacenes a la vez, con un trabajo de dos claves que la cola ordena respecto a
// las escrituras de las dos ciudades. La caducidad se aplica en la fase 5, de
// forma optimista, justo después de la de los tratados.

// defaultTradeOfferTTL es la vida de una oferta sin respuesta (SPEC-TRADE Q5).
const defaultTradeOfferTTL = 24 * time.Hour

// ErrTradeConflict: la base no admite la escritura porque la oferta o el par
// cambiaron por otra vía (el índice de INV-TRADE-003, o un estado distinto del
// esperado). Es un rechazo de dominio: no se reintenta.
var ErrTradeConflict = errors.New("la oferta cambió por otra vía")

// TradeStore escribe las ofertas y los tratos. Lo implementa
// persistence.TradeStore. Es idempotente: la cola puede reintentar.
type TradeStore interface {
	// ApplyOffer escribe una transición que no toca almacenes y devuelve el id
	// de la oferta.
	ApplyOffer(ctx context.Context, w TradeWrite) (int64, error)
	// AcceptTrade cierra un trato aceptado en una transacción: debita lo que da
	// cada parte y crea sus dos caravanas (RN-CARAVAN-001). Devuelve sus ids en
	// el orden de a.Caravans, también en un reintento. Un rechazo de dominio es
	// ErrTradeConflict, ErrStaleStock o economy.ErrInsufficientResources.
	AcceptTrade(ctx context.Context, a TradeAcceptance) ([]CaravanCreated, error)
}

// TradeWrite es una transición de una oferta ya decidida por la simulación:
// la propuesta, el rechazo, la retirada o la caducidad.
type TradeWrite struct {
	// Insert: una oferta nueva, sin id todavía.
	Insert bool
	// Offer es la oferta tal como queda.
	Offer trade.Offer
	Event string
	Tick  uint64
	// AtMs es el instante en la simulación y By quien dio la orden (uuid.Nil
	// en una caducidad): van al payload del evento (ADR-028 §3).
	AtMs int64
	By   uuid.UUID
	// Reason es el motivo de una caducidad.
	Reason string
}

// TradeAcceptance es un trato aceptado con sus débitos, uno por ciudad y
// recurso que da (ADR-032 §2), y las dos caravanas que se llevan lo debitado
// (ADR-037 §3).
type TradeAcceptance struct {
	Offer    trade.Offer
	Stocks   []StockDelta
	Caravans []CaravanSpawn
	Tick     uint64
	AtMs     int64
	By       uuid.UUID
}

// Eventos del comercio en world_events.
const (
	EventTradeProposed  = "TradeProposed"
	EventTradeAccepted  = "TradeAccepted"
	EventTradeRejected  = "TradeRejected"
	EventTradeWithdrawn = "TradeWithdrawn"
	EventTradeExpired   = "TradeExpired"
)

// Nombres de los trabajos del comercio (catálogo JobNames).
const (
	JobTradeOrder  = "trade.order"
	JobTradeAccept = "trade.accept"
	JobTradeEnd    = "trade.end"
)

// Comandos de los jugadores (protocolo v1).

// ProposeTrade ofrece un trato a otro jugador.
type ProposeTrade struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	Target    uuid.UUID
	Give      economy.Costs
	Want      economy.Costs
}

func (ProposeTrade) commandType() string { return protocol.TypeTradePropose }

// RespondTrade acepta o rechaza una oferta recibida.
type RespondTrade struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	OfferID   int64
	Accept    bool
}

func (RespondTrade) commandType() string { return protocol.TypeTradeRespond }

// CancelTrade retira la oferta propia.
type CancelTrade struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	OfferID   int64
}

func (CancelTrade) commandType() string { return protocol.TypeTradeCancel }

// TradeResolved lo devuelve la cola con el resultado de una orden que no toca
// almacenes.
type TradeResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	Write     TradeWrite
	// ID es el que dio la base a una oferta nueva.
	ID int64
	// Code, si no está vacío, es el rechazo: la transacción no escribió nada.
	Code string
}

func (TradeResolved) commandType() string { return "trade.resolved" }

// TradeAcceptResolved lo devuelve la cola con el resultado de un trato y los
// ids de sus caravanas.
type TradeAcceptResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	OfferID   int64
	Caravans  []CaravanCreated
	Code      string
}

func (TradeAcceptResolved) commandType() string { return "trade.accept_resolved" }

// TradeEndPersistFailed es la compensación de una caducidad que no se
// escribió: el barrido siguiente la vuelve a encolar (ADR-032 §4).
type TradeEndPersistFailed struct{ Write TradeWrite }

func (TradeEndPersistFailed) commandType() string { return "trade.end_failed" }

// TradeKey es la clave de la cola de las ofertas de un par de jugadores.
func TradeKey(x, y uuid.UUID) string {
	a, b := diplomacy.CanonicalPair(x, y)
	return "trade:" + a.String() + ":" + b.String()
}

// tradeReservation es lo que un trato aceptado apartó mientras su transacción
// no vuelve: las patas que pagan, los depósitos sin volcar que viajan con cada
// una y las caravanas que se llevarán lo pagado.
type tradeReservation struct {
	offer   trade.Offer
	legs    []trade.Leg
	carried []int64 // por pata, en el orden de legs
	spawns  []CaravanSpawn
}

// tradeState es lo que la simulación sabe del comercio.
type tradeState struct {
	// open son las ofertas PROPOSED, por id.
	open map[int64]*trade.Offer
	// busy son los pares con una orden esperando su transacción.
	busy map[[2]uuid.UUID]struct{}
	// reserved son los tratos aceptados esperando su transacción, por oferta.
	reserved map[int64]*tradeReservation
	// failedEnds son las caducidades cuya escritura se descartó (ADR-022).
	failedEnds []TradeWrite
}

func newTradeState() tradeState {
	return tradeState{
		open:     make(map[int64]*trade.Offer),
		busy:     make(map[[2]uuid.UUID]struct{}),
		reserved: make(map[int64]*tradeReservation),
	}
}

// SetTradeOffers instala las ofertas pendientes leídas al arrancar (ADR-032 §1).
// Las que caducaron durante la caída se cierran en el primer barrido.
func (s *State) SetTradeOffers(offers []trade.Offer) {
	s.trade = newTradeState()
	for _, o := range offers {
		if o.Status != trade.StatusProposed {
			continue
		}
		copia := o
		s.trade.open[o.ID] = &copia
	}
}

// TradeOffer devuelve una oferta pendiente (tests).
func (s *State) TradeOffer(id int64) (trade.Offer, bool) {
	o, ok := s.trade.open[id]
	if !ok {
		return trade.Offer{}, false
	}
	return *o, true
}

// pendingOfferOf devuelve la oferta pendiente de un par, si la hay.
func (s *State) pendingOfferOf(x, y uuid.UUID) (*trade.Offer, bool) {
	for _, o := range s.trade.open {
		if o.Involves(x, y) {
			return o, true
		}
	}
	return nil, false
}

// tradeAllowed indica si el par tiene un tratado que deja comerciar en `now`
// (INV-TRADE-002).
func (s *State) tradeAllowed(x, y uuid.UUID, now time.Time) bool {
	for _, t := range s.openOf(x, y) {
		if t.AllowsTradeAt(now) {
			return true
		}
	}
	return false
}

func (s *Simulation) tradeOfferTTL() time.Duration {
	if s.deps.TradeOfferTTL > 0 {
		return s.deps.TradeOfferTTL
	}
	return defaultTradeOfferTTL
}

func (s *Simulation) tradeReject(playerID uuid.UUID, requestID, code, msg string) {
	s.economyError(playerID, requestID, code, msg)
}

// ─────────────────────────────────────────────────────────────
// Órdenes (dos fases)
// ─────────────────────────────────────────────────────────────

// handleProposeTrade valida la oferta contra la RAM y encola su inserción
// (RN-TRADE-001). No reserva stock (SPEC-TRADE Q2).
func (s *Simulation) handleProposeTrade(cmd ProposeTrade) {
	if s.deps.Trade == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "el comercio no está disponible")
		return
	}
	if cmd.Target == cmd.PlayerID {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "no puedes comerciar contigo mismo")
		return
	}
	propia, ok := s.state.CityOf(cmd.PlayerID)
	if !ok {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeCityNotFound, "no tienes ciudad")
		return
	}
	ajena, ok := s.state.CityOf(cmd.Target)
	if !ok {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ese jugador no existe")
		return
	}
	if err := trade.ValidateLines(cmd.Give, cmd.Want); err != nil {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, err.Error())
		return
	}
	k := pairKey(cmd.PlayerID, cmd.Target)
	if _, ocupado := s.state.trade.busy[k]; ocupado {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "hay otra orden de comercio en curso con ese jugador")
		return
	}
	if _, hay := s.state.pendingOfferOf(cmd.PlayerID, cmd.Target); hay {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ya hay una oferta pendiente con ese jugador")
		return
	}
	now := s.treatyNow()
	if !s.state.tradeAllowed(cmd.PlayerID, cmd.Target, now) {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeTreatyRequired, "hace falta un tratado vigente para comerciar")
		return
	}
	if !cmd.Give.CanAfford(s.state.economyOf(propia.ID).stockOf) {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInsufficientResources, "no tienes lo que ofreces")
		return
	}
	// RN-CARAVAN-002: sin ruta entre las dos ciudades no hay caravanas, y una
	// oferta que nunca se podría aceptar no se admite.
	if _, err := s.tradeRoute(propia, ajena); err != nil {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, pathErrorCode(err), "no hay camino para las caravanas entre las dos ciudades")
		return
	}

	o := trade.Offer{
		Proposer: cmd.PlayerID, Target: cmd.Target, ProposerCity: propia.ID, TargetCity: ajena.ID,
		Give: append(economy.Costs(nil), cmd.Give...), Want: append(economy.Costs(nil), cmd.Want...),
		Status: trade.StatusProposed, RequestID: cmd.RequestID,
		CreatedAt: now, ExpiresAt: now.Add(s.tradeOfferTTL()),
	}
	s.submitTradeOrder(TradeWrite{Insert: true, Offer: o, Event: EventTradeProposed}, cmd.PlayerID, cmd.RequestID)
}

// handleRespondTrade rechaza la oferta, o la acepta reservando los dos
// almacenes (RN-TRADE-002, RN-TRADE-003).
func (s *Simulation) handleRespondTrade(cmd RespondTrade) {
	o, ok := s.state.trade.open[cmd.OfferID]
	if !ok {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "esa oferta no existe")
		return
	}
	// INV-TRADE-004: sólo responde el destinatario.
	if err := o.CanRespond(cmd.PlayerID); err != nil {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "no puedes responder a esa oferta")
		return
	}
	k := pairKey(o.Proposer, o.Target)
	if _, ocupado := s.state.trade.busy[k]; ocupado {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "hay otra orden en curso sobre esa oferta")
		return
	}
	now := s.treatyNow()
	if !cmd.Accept {
		after, err := o.TransitionTo(trade.StatusRejected, now)
		if err != nil {
			s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "esa oferta ya no se puede responder")
			return
		}
		s.submitTradeOrder(TradeWrite{Offer: after, Event: EventTradeRejected}, cmd.PlayerID, cmd.RequestID)
		return
	}
	s.acceptTrade(cmd, *o, now)
}

// acceptTrade reserva lo que da cada parte y encola el trato con las dos
// ciudades como claves (ADR-032 §2 y §3).
func (s *Simulation) acceptTrade(cmd RespondTrade, o trade.Offer, now time.Time) {
	if s.deps.Trade == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "el comercio no está disponible")
		return
	}
	// INV-TRADE-002: con un tratado vigente entre los dos.
	if !s.state.tradeAllowed(o.Proposer, o.Target, now) {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeTreatyRequired, "ya no tenéis un tratado que deje comerciar")
		return
	}
	origen, ok := s.state.City(o.ProposerCity)
	if !ok {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "la ciudad de quien ofrece ya no existe")
		return
	}
	destino, ok := s.state.City(o.TargetCity)
	if !ok {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeCityNotFound, "no tienes ciudad")
		return
	}
	// RN-TRADE-003: las dos partes, en este tick. Si no alcanza, la oferta sigue
	// pendiente.
	if !o.Give.CanAfford(s.state.economyOf(o.ProposerCity).stockOf) {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInsufficientResources, "a quien ofrece ya no le alcanza lo que da")
		return
	}
	if !o.Want.CanAfford(s.state.economyOf(o.TargetCity).stockOf) {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInsufficientResources, "no tienes lo que te piden")
		return
	}
	// RN-CARAVAN-002: la ruta de las dos caravanas, buscada una vez.
	route, err := s.tradeRoute(origen, destino)
	if err != nil {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, pathErrorCode(err), "no hay camino para las caravanas entre las dos ciudades")
		return
	}
	nowMs := s.deps.Clock.NowMs()
	spawns, err := s.caravanSpawns(o, route, nowMs)
	if err != nil {
		s.deps.Log.Error("no se pudo construir la ruta de las caravanas", "err", err, "offer_id", o.ID)
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "no se pudo construir la ruta de las caravanas")
		return
	}

	// La reserva: lo que da cada parte sale ya de la RAM, con los depósitos sin
	// volcar de ese recurso (ADR-032 §2). Lo que recibe no se toca: llega con
	// su caravana (ADR-037 §3).
	var legs []trade.Leg
	for _, l := range o.Legs() {
		if l.Pays > 0 {
			legs = append(legs, l)
		}
	}
	r := &tradeReservation{offer: o, legs: legs, carried: make([]int64, len(legs)), spawns: spawns}
	deltas := make([]StockDelta, 0, len(legs))
	for i, l := range legs {
		rs := s.state.economyOf(l.CityID).of(l.Resource)
		if err := rs.stock.Debit(economy.Cost{Resource: l.Resource, Amount: l.Pays}); err != nil {
			// No hay ruta que llegue aquí: CanAfford lo comprobó. Se deshace lo
			// ya reservado.
			s.releaseTrade(r, i)
			s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInsufficientResources, err.Error())
			return
		}
		r.carried[i] = rs.unflushed
		rs.unflushed = 0
		deltas = append(deltas, StockDelta{
			CityID: l.CityID, Resource: l.Resource, Delta: r.carried[i] - l.Pays, Version: rs.stock.Version,
		})
	}
	s.state.markStockDirty(o.ProposerCity)
	s.state.markStockDirty(o.TargetCity)

	accepted, err := o.TransitionTo(trade.StatusAccepted, now)
	if err != nil {
		s.releaseTrade(r, len(legs))
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "esa oferta ya no se puede aceptar")
		return
	}
	accepted.AcceptedRequestID = cmd.RequestID
	r.offer = accepted

	k := pairKey(o.Proposer, o.Target)
	s.state.trade.busy[k] = struct{}{}
	s.state.trade.reserved[o.ID] = r

	a := TradeAcceptance{Offer: accepted, Stocks: deltas, Caravans: spawns, Tick: s.state.Tick(), AtMs: nowMs, By: cmd.PlayerID}
	resolved := TradeAcceptResolved{PlayerID: cmd.PlayerID, RequestID: cmd.RequestID, OfferID: o.ID}
	store := s.deps.Trade
	s.deps.Persister.Submit(Job{
		Name: JobTradeAccept,
		Key:  CityKey(o.ProposerCity),
		Key2: CityKey(o.TargetCity),
		Run: func(ctx context.Context) error {
			created, err := store.AcceptTrade(ctx, a)
			if err == nil {
				r := resolved
				r.Caravans = created
				s.reinject(r)
				return nil
			}
			if code := tradeErrorCode(err); code != "" {
				// Un rechazo de dominio no se reintenta: es la respuesta.
				r := resolved
				r.Code = code
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el trato; lo reservado vuelve a las dos ciudades",
				"err", err, "offer_id", resolved.OfferID)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// tradeErrorCode traduce un rechazo de dominio del trato; "" si no lo es (un
// fallo que se reintenta).
func tradeErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrTradeConflict):
		return protocol.CodeInvalidTarget
	case errors.Is(err, economy.ErrInsufficientResources):
		return protocol.CodeInsufficientResources
	case errors.Is(err, ErrStaleStock):
		return protocol.CodeInternalError
	}
	return ""
}

// releaseTrade devuelve las primeras n patas de una reserva: lo debitado y los
// depósitos que viajaban con cada una (INV-ECON-002).
func (s *Simulation) releaseTrade(r *tradeReservation, n int) {
	for i := 0; i < n; i++ {
		l := r.legs[i]
		rs := s.state.economyOf(l.CityID).of(l.Resource)
		if l.Pays > 0 {
			_ = rs.stock.Credit(l.Pays)
		}
		rs.unflushed += r.carried[i]
	}
	s.state.markStockDirty(r.offer.ProposerCity)
	s.state.markStockDirty(r.offer.TargetCity)
}

// handleTradeAcceptResolved confirma el trato —acredita a cada ciudad lo que
// recibe y lo difunde— o devuelve toda la reserva.
func (s *Simulation) handleTradeAcceptResolved(cmd TradeAcceptResolved) {
	r, ok := s.state.trade.reserved[cmd.OfferID]
	if !ok {
		return
	}
	delete(s.state.trade.reserved, cmd.OfferID)
	delete(s.state.trade.busy, pairKey(r.offer.Proposer, r.offer.Target))

	if cmd.Code != "" {
		s.releaseTrade(r, len(r.legs))
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo cerrar el trato")
		s.broadcastTradeStocks(r.offer)
		return
	}

	// INV-TRADE-001: lo que salió de cada ciudad va en su caravana; nada entra
	// en ningún almacén hasta que llegue (RN-CARAVAN-004).
	delete(s.state.trade.open, r.offer.ID)
	s.broadcastTrade(r.offer, cmd.PlayerID, cmd.RequestID)
	s.broadcastTradeStocks(r.offer)
	s.spawnCaravans(r.spawns, cmd.Caravans, r.offer.ID)
}

// broadcastTradeStocks difunde el almacén de las dos ciudades de un trato.
func (s *Simulation) broadcastTradeStocks(o trade.Offer) {
	for _, id := range []int64{o.ProposerCity, o.TargetCity} {
		if c, ok := s.state.City(id); ok {
			s.broadcastStocks(c)
		}
	}
}

// handleCancelTrade retira la oferta propia (RN-TRADE-005).
func (s *Simulation) handleCancelTrade(cmd CancelTrade) {
	o, ok := s.state.trade.open[cmd.OfferID]
	if !ok {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "esa oferta no existe")
		return
	}
	// INV-TRADE-004: sólo retira quien ofreció.
	if err := o.CanWithdraw(cmd.PlayerID); err != nil {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "sólo quien ofrece puede retirar la oferta")
		return
	}
	if _, ocupado := s.state.trade.busy[pairKey(o.Proposer, o.Target)]; ocupado {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "hay otra orden en curso sobre esa oferta")
		return
	}
	after, err := o.TransitionTo(trade.StatusWithdrawn, s.treatyNow())
	if err != nil {
		s.tradeReject(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "esa oferta ya no se puede retirar")
		return
	}
	s.submitTradeOrder(TradeWrite{Offer: after, Event: EventTradeWithdrawn}, cmd.PlayerID, cmd.RequestID)
}

// submitTradeOrder reserva el par y encola una transición que no toca
// almacenes.
func (s *Simulation) submitTradeOrder(w TradeWrite, playerID uuid.UUID, requestID string) {
	w.Tick = s.state.Tick()
	w.AtMs, w.By = s.deps.Clock.NowMs(), playerID
	k := pairKey(w.Offer.Proposer, w.Offer.Target)
	s.state.trade.busy[k] = struct{}{}
	resolved := TradeResolved{PlayerID: playerID, RequestID: requestID, Write: w}
	store := s.deps.Trade
	s.deps.Persister.Submit(Job{
		Name: JobTradeOrder,
		Key:  TradeKey(k[0], k[1]),
		Run: func(ctx context.Context) error {
			id, err := store.ApplyOffer(ctx, w)
			if err == nil {
				r := resolved
				r.ID = id
				s.reinject(r)
				return nil
			}
			if errors.Is(err, ErrTradeConflict) {
				r := resolved
				r.Code = protocol.CodeInvalidTarget
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la orden de comercio", "err", err, "event", w.Event)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// handleTradeResolved confirma una orden en la RAM y la difunde a los dos, o
// libera el par y responde el rechazo.
func (s *Simulation) handleTradeResolved(cmd TradeResolved) {
	w := cmd.Write
	delete(s.state.trade.busy, pairKey(w.Offer.Proposer, w.Offer.Target))
	if cmd.Code != "" {
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo aplicar la orden de comercio")
		return
	}
	o := w.Offer
	if w.Insert {
		o.ID = cmd.ID
	}
	s.storeTradeOffer(o)
	s.broadcastTrade(o, cmd.PlayerID, cmd.RequestID)
}

// storeTradeOffer deja en la RAM una oferta pendiente, o la saca si se cerró.
func (s *Simulation) storeTradeOffer(o trade.Offer) {
	if o.Status == trade.StatusProposed {
		copia := o
		s.state.trade.open[o.ID] = &copia
		return
	}
	delete(s.state.trade.open, o.ID)
}

// ─────────────────────────────────────────────────────────────
// Caducidad (fase 5, optimista)
// ─────────────────────────────────────────────────────────────

// sweepTrades cierra las ofertas que llegaron a su plazo y las de un par que
// ya no tiene tratado (RN-TRADE-006). La llama sweepTreaties, una vez por
// segundo y en el mismo tick: una oferta cae en el tick en que cae su tratado.
func (s *Simulation) sweepTrades(now time.Time) {
	if len(s.state.trade.failedEnds) > 0 {
		pendientes := s.state.trade.failedEnds
		s.state.trade.failedEnds = nil
		for _, w := range pendientes {
			s.submitTradeEnd(w)
		}
	}

	ids := make([]int64, 0, len(s.state.trade.open))
	for id := range s.state.trade.open {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		o := s.state.trade.open[id]
		if _, ocupado := s.state.trade.busy[pairKey(o.Proposer, o.Target)]; ocupado {
			continue // una orden en vuelo: se mira en el barrido siguiente
		}
		var reason string
		switch {
		case o.ExpiredAt(now):
			reason = trade.ExpiryTimeout
		case !s.state.tradeAllowed(o.Proposer, o.Target, now):
			reason = trade.ExpiryTreatyEnded
		default:
			continue
		}
		after, err := o.TransitionTo(trade.StatusExpired, now)
		if err != nil {
			continue
		}
		s.storeTradeOffer(after)
		s.broadcastTrade(after, uuid.Nil, "")
		s.submitTradeEnd(TradeWrite{Offer: after, Event: EventTradeExpired, Tick: s.state.Tick(),
			AtMs: now.UnixMilli(), Reason: reason})
	}
}

func (s *Simulation) submitTradeEnd(w TradeWrite) {
	if s.deps.Trade == nil {
		return
	}
	store := s.deps.Trade
	s.deps.Persister.Submit(Job{
		Name: JobTradeEnd,
		Key:  TradeKey(w.Offer.Proposer, w.Offer.Target),
		Run: func(ctx context.Context) error {
			if _, err := store.ApplyOffer(ctx, w); err != nil && !errors.Is(err, ErrTradeConflict) {
				return err
			}
			return nil
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la caducidad de una oferta; se reintentará",
				"err", err, "offer_id", w.Offer.ID)
			s.reinject(TradeEndPersistFailed{Write: w})
		},
	})
}

func (s *Simulation) handleTradeEndPersistFailed(cmd TradeEndPersistFailed) {
	s.state.trade.failedEnds = append(s.state.trade.failedEnds, cmd.Write)
}

// ─────────────────────────────────────────────────────────────
// Vistas
// ─────────────────────────────────────────────────────────────

func resourceAmounts(cs economy.Costs) []protocol.ResourceAmount {
	out := make([]protocol.ResourceAmount, 0, len(cs))
	for _, c := range cs {
		out = append(out, protocol.ResourceAmount{Resource: string(c.Resource), Amount: c.Amount})
	}
	return out
}

// tradeView es la oferta vista por uno de sus dos jugadores.
func (s *Simulation) tradeView(o trade.Offer, viewer uuid.UUID) protocol.TradeOfferView {
	other := o.Other(viewer)
	give, get := o.For(viewer)
	v := protocol.TradeOfferView{
		ID: o.ID, OtherPlayerID: other.String(), Give: resourceAmounts(give), Get: resourceAmounts(get),
		Status: string(o.Status), ProposedByMe: o.Proposer == viewer, ExpiresAtMs: o.ExpiresAt.UnixMilli(),
	}
	if c, ok := s.state.CityOf(other); ok {
		id, name := c.ID, c.Name
		v.OtherCityID, v.OtherCityName = &id, &name
	}
	return v
}

// broadcastTrade lleva una oferta a sus dos jugadores y a nadie más (Q9). El
// que dio la orden la recibe con su requestId.
func (s *Simulation) broadcastTrade(o trade.Offer, orderedBy uuid.UUID, requestID string) {
	for _, p := range []uuid.UUID{o.Proposer, o.Target} {
		rid := ""
		if p == orderedBy {
			rid = requestID
		}
		s.deps.Broadcaster.SendToPlayer(p, protocol.TypeTradeUpdate, rid,
			protocol.TradeUpdatePayload{Offer: s.tradeView(o, p)})
	}
}

// tradeOffersOf son las ofertas pendientes de un jugador, por id, para su
// snapshot.
func (s *Simulation) tradeOffersOf(viewer uuid.UUID) []protocol.TradeOfferView {
	var os []*trade.Offer
	for _, o := range s.state.trade.open {
		if o.HasParty(viewer) {
			os = append(os, o)
		}
	}
	sort.Slice(os, func(i, j int) bool { return os[i].ID < os[j].ID })
	out := make([]protocol.TradeOfferView, 0, len(os))
	for _, o := range os {
		out = append(out, s.tradeView(*o, viewer))
	}
	return out
}
