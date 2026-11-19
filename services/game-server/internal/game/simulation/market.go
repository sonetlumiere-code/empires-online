package simulation

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/caravan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/market"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El mercado en la simulación (SPEC-MARKETS, ADR-047).
//
// La RAM es la fuente de verdad de las ofertas abiertas. Publicar y comprar son
// órdenes en dos fases, como un encargo: el tick valida y reserva, la
// transacción escribe y el buzón confirma. Publicar reserva lo que da la oferta
// con su comisión en el almacén del vendedor; comprar, lo que paga el comprador
// con la suya y los lotes en la oferta, y su transacción crea las dos caravanas
// con las claves de la ciudad del comprador y de la oferta (§2 del ADR).
// Retirar y caducar son optimistas: la RAM cierra la oferta, acredita lo que
// vuelve y la transacción lo confirma después (§3).

// defaultMarketOfferTTL es la vida de una oferta (SPEC-MARKETS Q10).
const defaultMarketOfferTTL = 24 * time.Hour

// ErrMarketConflict: la base no admite la escritura porque la oferta cambió por
// otra vía. Es un rechazo de dominio: no se reintenta.
var ErrMarketConflict = errors.New("la oferta del mercado cambió por otra vía")

// MarketStore escribe el mercado. Lo implementa persistence.MarketStore. Es
// idempotente: la cola puede reintentar.
type MarketStore interface {
	// Post publica una oferta con su débito y devuelve su id. Un rechazo de
	// dominio es ErrStaleStock o economy.ErrInsufficientResources.
	Post(ctx context.Context, p MarketPostWrite) (int64, error)
	// Buy escribe una compra con el débito del comprador y sus dos caravanas, y
	// devuelve el id de la compra y sus caravanas en el orden de p.Caravans. Un
	// rechazo de dominio es ErrMarketConflict, ErrStaleStock o
	// economy.ErrInsufficientResources.
	Buy(ctx context.Context, p MarketBuyWrite) (int64, []CaravanCreated, error)
	// Close cierra una oferta y acredita lo que vuelve; si ya estaba cerrada, no
	// acredita nada (INV-TRADE-008).
	Close(ctx context.Context, c MarketCloseWrite) error
}

// MarketPostWrite es una publicación con su débito versionado.
type MarketPostWrite struct {
	Offer market.Offer
	Stock StockDelta
	Tick  uint64
	AtMs  int64
}

// MarketBuyWrite es una compra con el débito del comprador y sus caravanas.
// Offer es la oferta antes de la compra.
type MarketBuyWrite struct {
	Offer     market.Offer
	Buyer     uuid.UUID
	BuyerCity int64
	RequestID string
	Purchase  market.Purchase
	Stock     StockDelta
	Caravans  []CaravanSpawn
	Tick      uint64
	AtMs      int64
}

// MarketCloseWrite es la retirada o la caducidad de una oferta, con el crédito
// de lo que vuelve. Offer es la oferta ya cerrada.
type MarketCloseWrite struct {
	Offer    market.Offer
	Returned economy.Cost
	Stock    StockDelta
	Tick     uint64
	AtMs     int64
	By       uuid.UUID
}

// Nombres de los trabajos del mercado (catálogo JobNames).
const (
	JobMarketPost  = "market.post"
	JobMarketBuy   = "market.buy"
	JobMarketClose = "market.close"
)

// MarketKey es la clave de la cola de una oferta del mercado (ADR-047 §2).
func MarketKey(offerID int64) string { return "market:" + strconv.FormatInt(offerID, 10) }

// Comandos de los jugadores (protocolo v1).

// PostMarketOffer publica una oferta en el mercado.
type PostMarketOffer struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	Give      economy.Cost
	Want      economy.Cost
	Lots      int32
}

func (PostMarketOffer) commandType() string { return protocol.TypeMarketPost }

// BuyMarketOffer compra lotes de una oferta ajena.
type BuyMarketOffer struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	OfferID   int64
	Lots      int32
}

func (BuyMarketOffer) commandType() string { return protocol.TypeMarketBuy }

// CancelMarketOffer retira la oferta propia.
type CancelMarketOffer struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	OfferID   int64
}

func (CancelMarketOffer) commandType() string { return protocol.TypeMarketCancel }

// MarketPostResolved lo devuelve la cola con el resultado de una publicación.
type MarketPostResolved struct {
	PlayerID  uuid.UUID
	RequestID string
	ID        int64
	Code      string
}

func (MarketPostResolved) commandType() string { return "market.post_resolved" }

// MarketBuyResolved lo devuelve la cola con el resultado de una compra.
type MarketBuyResolved struct {
	PlayerID   uuid.UUID
	RequestID  string
	PurchaseID int64
	Caravans   []CaravanCreated
	Code       string
}

func (MarketBuyResolved) commandType() string { return "market.buy_resolved" }

// MarketClosePersistFailed es la compensación de un cierre que no se escribió:
// se reintenta en el siguiente volcado con una versión nueva del stock.
type MarketClosePersistFailed struct{ Write MarketCloseWrite }

func (MarketClosePersistFailed) commandType() string { return "market.close_failed" }

// marketPostReservation es lo que apartó una publicación mientras su
// transacción no vuelve.
type marketPostReservation struct {
	offer   market.Offer
	costs   economy.Costs
	carried map[economy.Resource]int64
}

// marketBuyReservation es lo que apartó una compra mientras su transacción no
// vuelve: los lotes, lo que paga el comprador y las caravanas.
type marketBuyReservation struct {
	offerID   int64
	buyerCity int64
	purchase  market.Purchase
	costs     economy.Costs
	carried   map[economy.Resource]int64
	spawns    []CaravanSpawn
}

// marketState es lo que la simulación sabe del mercado.
type marketState struct {
	// open son las ofertas OPEN, por id.
	open map[int64]*market.Offer
	// posts son las publicaciones en vuelo, por jugador y requestId.
	posts map[string]*marketPostReservation
	// buys son las compras en vuelo, por comprador y requestId.
	buys map[string]*marketBuyReservation
	// reserved son los lotes apartados por compras en vuelo, por oferta.
	reserved map[int64]int32
	// failedCloses son los cierres cuya escritura se descartó, por oferta.
	failedCloses map[int64]MarketCloseWrite
}

func newMarketState() marketState {
	return marketState{
		open:         make(map[int64]*market.Offer),
		posts:        make(map[string]*marketPostReservation),
		buys:         make(map[string]*marketBuyReservation),
		reserved:     make(map[int64]int32),
		failedCloses: make(map[int64]MarketCloseWrite),
	}
}

func orderKey(player uuid.UUID, requestID string) string { return player.String() + "/" + requestID }

// SetMarketOffers instala las ofertas abiertas leídas al arrancar. Las que
// caducaron durante la caída se cierran en el primer barrido.
func (s *State) SetMarketOffers(offers []market.Offer) {
	s.market = newMarketState()
	for _, o := range offers {
		if o.Status != market.StatusOpen {
			continue
		}
		copia := o
		s.market.open[o.ID] = &copia
	}
}

// MarketOffer devuelve una oferta abierta (tests).
func (s *State) MarketOffer(id int64) (market.Offer, bool) {
	o, ok := s.market.open[id]
	if !ok {
		return market.Offer{}, false
	}
	return *o, true
}

// hasCompleteMarket dice si la ciudad tiene un mercado completo (RN-MARKET-001).
func (s *State) hasCompleteMarket(cityID int64) bool {
	for _, b := range s.BuildingsOf(cityID) {
		if b.Type == building.TypeMarket && b.IsComplete() {
			return true
		}
	}
	return false
}

// openOffersOf cuenta las ofertas abiertas y en vuelo de un jugador (Q10).
func (s *State) openOffersOf(player uuid.UUID) int {
	n := 0
	for _, o := range s.market.open {
		if o.Seller == player {
			n++
		}
	}
	for _, r := range s.market.posts {
		if r.offer.Seller == player {
			n++
		}
	}
	return n
}

func (s *Simulation) marketOfferTTL() time.Duration {
	if s.deps.MarketOfferTTL > 0 {
		return s.deps.MarketOfferTTL
	}
	return defaultMarketOfferTTL
}

// ─────────────────────────────────────────────────────────────
// market.post (dos fases)
// ─────────────────────────────────────────────────────────────

// handlePostMarketOffer valida la oferta y reserva su custodia con la comisión
// del vendedor (RN-MARKET-002, RN-MARKET-003).
func (s *Simulation) handlePostMarketOffer(cmd PostMarketOffer) {
	if s.deps.Market == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "el mercado no está disponible")
		return
	}
	c, ok := s.state.CityOf(cmd.PlayerID)
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeCityNotFound, "no tienes ciudad")
		return
	}
	if _, enVuelo := s.state.market.posts[orderKey(cmd.PlayerID, cmd.RequestID)]; enVuelo {
		return // la misma orden repetida mientras la primera va en vuelo
	}
	o, err := market.New(cmd.PlayerID, c.ID, cmd.Give, cmd.Want, cmd.Lots, cmd.RequestID, s.treatyNow(), s.marketOfferTTL())
	if err != nil {
		code := protocol.CodeInvalidTarget
		if errors.Is(err, market.ErrTotalExcesivo) {
			code = protocol.CodeInvalidMessage
		}
		s.economyError(cmd.PlayerID, cmd.RequestID, code, err.Error())
		return
	}
	if !s.state.hasCompleteMarket(c.ID) {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "necesitas un mercado completo")
		return
	}
	if s.state.openOffersOf(cmd.PlayerID) >= market.MaxOpenPerPlayer {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "ya tienes 3 ofertas abiertas")
		return
	}
	costs := economy.Costs{o.PostCost()}
	eco := s.state.economyOf(c.ID)
	carried, deltas, ok := s.reserve(c, eco, costs)
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInsufficientResources, "no tienes lo que ofreces más la comisión")
		return
	}
	s.state.market.posts[orderKey(cmd.PlayerID, cmd.RequestID)] = &marketPostReservation{offer: o, costs: costs, carried: carried}

	w := MarketPostWrite{Offer: o, Stock: deltas[0], Tick: s.state.Tick(), AtMs: s.deps.Clock.NowMs()}
	resolved := MarketPostResolved{PlayerID: cmd.PlayerID, RequestID: cmd.RequestID}
	store := s.deps.Market
	s.deps.Persister.Submit(Job{
		Name: JobMarketPost,
		Key:  CityKey(c.ID),
		Run: func(ctx context.Context) error {
			id, err := store.Post(ctx, w)
			if err == nil {
				r := resolved
				r.ID = id
				s.reinject(r)
				return nil
			}
			if code := marketErrorCode(err); code != "" {
				r := resolved
				r.Code = code
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo publicar la oferta; vuelve lo reservado", "err", err, "request_id", cmd.RequestID)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// marketErrorCode traduce un rechazo de dominio del mercado; "" si no lo es (un
// fallo que se reintenta).
func marketErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrMarketConflict):
		return protocol.CodeInvalidTarget
	case errors.Is(err, economy.ErrInsufficientResources):
		return protocol.CodeInsufficientResources
	case errors.Is(err, ErrStaleStock):
		return protocol.CodeInternalError
	}
	return ""
}

// handleMarketPostResolved confirma la oferta o devuelve lo reservado.
func (s *Simulation) handleMarketPostResolved(cmd MarketPostResolved) {
	k := orderKey(cmd.PlayerID, cmd.RequestID)
	r, ok := s.state.market.posts[k]
	if !ok {
		return
	}
	delete(s.state.market.posts, k)
	c, hayCiudad := s.state.City(r.offer.CityID)
	if cmd.Code != "" {
		eco := s.state.economyOf(r.offer.CityID)
		s.refund(eco, r.costs, r.carried)
		s.state.markStockDirty(r.offer.CityID)
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo publicar la oferta")
		if hayCiudad {
			s.broadcastStocks(c)
		}
		return
	}
	o := r.offer
	o.ID = cmd.ID
	s.state.market.open[o.ID] = &o
	if hayCiudad {
		s.broadcastStocks(c)
	}
	s.broadcastMarket(o, cmd.PlayerID, cmd.RequestID)
}

// ─────────────────────────────────────────────────────────────
// market.buy (dos fases)
// ─────────────────────────────────────────────────────────────

// handleBuyMarketOffer valida la compra, busca la ruta, reserva lo que paga el
// comprador y los lotes, y encola la compra con las claves de su ciudad y de la
// oferta (RN-MARKET-004, RN-MARKET-009, ADR-047 §2).
func (s *Simulation) handleBuyMarketOffer(cmd BuyMarketOffer) {
	if s.deps.Market == nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "el mercado no está disponible")
		return
	}
	k := orderKey(cmd.PlayerID, cmd.RequestID)
	if _, enVuelo := s.state.market.buys[k]; enVuelo {
		return // la misma orden repetida mientras la primera va en vuelo
	}
	o, ok := s.state.market.open[cmd.OfferID]
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "esa oferta ya no está en el mercado")
		return
	}
	propia, ok := s.state.CityOf(cmd.PlayerID)
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeCityNotFound, "no tienes ciudad")
		return
	}
	if !s.state.hasCompleteMarket(propia.ID) {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "necesitas un mercado completo")
		return
	}
	// INV-TRADE-009 e INV-TRADE-010.
	p, err := o.CanBuy(cmd.PlayerID, cmd.Lots, o.LotsLeft-s.state.market.reserved[o.ID])
	if err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, err.Error())
		return
	}
	ajena, ok := s.state.City(o.CityID)
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "la ciudad del vendedor ya no existe")
		return
	}
	eco := s.state.economyOf(propia.ID)
	costs := economy.Costs{p.Debit()}
	if !costs.CanAfford(eco.stockOf) {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInsufficientResources, "no tienes lo que pagas más la comisión")
		return
	}
	// RN-CARAVAN-002 y SPEC-MARKETS §4.6: la ruta de las dos caravanas.
	route, err := s.tradeRoute(ajena, propia)
	if err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, pathErrorCode(err), "no hay camino para las caravanas entre las dos ciudades")
		return
	}
	nowMs := s.deps.Clock.NowMs()
	spawns, err := s.spawnsFor(caravan.PurchaseHalves(o.Seller, o.CityID, cmd.PlayerID, propia.ID, p.Receive, p.Pay), route, nowMs)
	if err != nil {
		s.deps.Log.Error("no se pudo construir la ruta de las caravanas", "err", err, "offer_id", o.ID)
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInternalError, "no se pudo construir la ruta de las caravanas")
		return
	}
	carried, deltas, ok := s.reserve(propia, eco, costs)
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInsufficientResources, "no tienes lo que pagas más la comisión")
		return
	}
	s.state.market.reserved[o.ID] += cmd.Lots
	s.state.market.buys[k] = &marketBuyReservation{
		offerID: o.ID, buyerCity: propia.ID, purchase: p, costs: costs, carried: carried, spawns: spawns,
	}

	w := MarketBuyWrite{
		Offer: *o, Buyer: cmd.PlayerID, BuyerCity: propia.ID, RequestID: cmd.RequestID, Purchase: p,
		Stock: deltas[0], Caravans: spawns, Tick: s.state.Tick(), AtMs: nowMs,
	}
	resolved := MarketBuyResolved{PlayerID: cmd.PlayerID, RequestID: cmd.RequestID}
	store := s.deps.Market
	s.deps.Persister.Submit(Job{
		Name: JobMarketBuy,
		Key:  CityKey(propia.ID),
		Key2: MarketKey(o.ID),
		Run: func(ctx context.Context) error {
			id, created, err := store.Buy(ctx, w)
			if err == nil {
				r := resolved
				r.PurchaseID, r.Caravans = id, created
				s.reinject(r)
				return nil
			}
			if code := marketErrorCode(err); code != "" {
				r := resolved
				r.Code = code
				s.reinject(r)
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la compra; vuelve lo reservado", "err", err, "offer_id", w.Offer.ID)
			r := resolved
			r.Code = protocol.CodeInternalError
			s.reinject(r)
		},
	})
}

// handleMarketBuyResolved confirma la compra —descuenta los lotes y pone las
// caravanas en el mapa— o devuelve lo reservado al comprador y a la oferta.
func (s *Simulation) handleMarketBuyResolved(cmd MarketBuyResolved) {
	k := orderKey(cmd.PlayerID, cmd.RequestID)
	r, ok := s.state.market.buys[k]
	if !ok {
		return
	}
	delete(s.state.market.buys, k)
	if s.state.market.reserved[r.offerID] -= r.purchase.Lots; s.state.market.reserved[r.offerID] <= 0 {
		delete(s.state.market.reserved, r.offerID)
	}
	buyerCity, hayCiudad := s.state.City(r.buyerCity)
	if cmd.Code != "" {
		s.refund(s.state.economyOf(r.buyerCity), r.costs, r.carried)
		s.state.markStockDirty(r.buyerCity)
		s.economyError(cmd.PlayerID, cmd.RequestID, cmd.Code, "no se pudo cerrar la compra")
		if hayCiudad {
			s.broadcastStocks(buyerCity)
		}
		return
	}
	o, ok := s.state.market.open[r.offerID]
	if ok {
		// INV-TRADE-009: la base ya descontó los lotes con la misma condición.
		if err := o.Sell(r.purchase.Lots, s.treatyNow()); err != nil {
			s.deps.Log.Error("invariant_violation", "inv_id", "INV-TRADE-009", "severity", "CRITICO",
				"policy", "REJECT", "entity", MarketKey(r.offerID), "detail", err.Error())
		}
		vista := *o
		if o.Status != market.StatusOpen {
			delete(s.state.market.open, r.offerID)
		}
		s.broadcastMarket(vista, cmd.PlayerID, cmd.RequestID)
	}
	if hayCiudad {
		s.broadcastStocks(buyerCity)
	}
	s.spawnCaravans(r.spawns, cmd.Caravans, caravanOrigin{purchaseID: cmd.PurchaseID})
}

// ─────────────────────────────────────────────────────────────
// market.cancel y caducidad (optimistas)
// ─────────────────────────────────────────────────────────────

// handleCancelMarketOffer retira la oferta propia (RN-MARKET-007): vuelve lo que
// queda en custodia. No pide mercado en pie.
func (s *Simulation) handleCancelMarketOffer(cmd CancelMarketOffer) {
	o, ok := s.state.market.open[cmd.OfferID]
	if !ok {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "esa oferta ya no está en el mercado")
		return
	}
	if err := o.CanWithdraw(cmd.PlayerID); err != nil {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, err.Error())
		return
	}
	// RN-MARKET-009: una compra sin confirmar sobre ella la deja como está.
	if s.state.market.reserved[o.ID] > 0 {
		s.economyError(cmd.PlayerID, cmd.RequestID, protocol.CodeInvalidTarget, "hay una compra de esa oferta en curso")
		return
	}
	s.closeMarketOffer(o, market.StatusWithdrawn, cmd.PlayerID, cmd.RequestID)
}

// closeMarketOffer cierra una oferta en la RAM, acredita lo que queda en
// custodia al almacén del vendedor, lo difunde y encola la escritura
// (ADR-047 §3).
func (s *Simulation) closeMarketOffer(o *market.Offer, to market.Status, by uuid.UUID, requestID string) {
	cerrada := *o
	back, err := cerrada.Close(to, s.treatyNow())
	if err != nil {
		s.economyError(by, requestID, protocol.CodeInvalidTarget, err.Error())
		return
	}
	delete(s.state.market.open, o.ID)
	eco := s.state.economyOf(cerrada.CityID)
	rs := eco.of(back.Resource)
	_ = rs.stock.Credit(back.Amount)
	carried := rs.unflushed
	rs.unflushed = 0
	s.state.markStockDirty(cerrada.CityID)
	if c, ok := s.state.City(cerrada.CityID); ok {
		s.broadcastStocks(c)
	}
	s.broadcastMarket(cerrada, by, requestID)
	s.submitMarketClose(MarketCloseWrite{
		Offer: cerrada, Returned: back, Tick: s.state.Tick(), AtMs: s.deps.Clock.NowMs(), By: by,
		Stock: StockDelta{CityID: cerrada.CityID, Resource: back.Resource, Delta: back.Amount + carried, Version: rs.stock.Version},
	})
}

func (s *Simulation) submitMarketClose(w MarketCloseWrite) {
	if s.deps.Market == nil {
		return
	}
	store := s.deps.Market
	s.deps.Persister.Submit(Job{
		Name: JobMarketClose,
		Key:  MarketKey(w.Offer.ID),
		Key2: CityKey(w.Offer.CityID),
		Run:  func(ctx context.Context) error { return store.Close(ctx, w) },
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir el cierre de una oferta; se reintentará en el siguiente volcado",
				"err", err, "offer_id", w.Offer.ID)
			s.reinject(MarketClosePersistFailed{Write: w})
		},
	})
}

func (s *Simulation) handleMarketClosePersistFailed(cmd MarketClosePersistFailed) {
	s.state.market.failedCloses[cmd.Write.Offer.ID] = cmd.Write
}

// flushMarketCloses reencola los cierres descartados con una versión nueva del
// stock: escrituras posteriores de la ciudad pueden haber llevado la base más
// allá de la original. Son idempotentes: una oferta ya cerrada no acredita.
func (s *Simulation) flushMarketCloses() {
	if len(s.state.market.failedCloses) == 0 {
		return
	}
	ids := make([]int64, 0, len(s.state.market.failedCloses))
	for id := range s.state.market.failedCloses {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	pending := s.state.market.failedCloses
	s.state.market.failedCloses = make(map[int64]MarketCloseWrite)
	for _, id := range ids {
		w := pending[id]
		rs := s.state.economyOf(w.Stock.CityID).of(w.Stock.Resource)
		rs.stock.Version++
		w.Stock.Version = rs.stock.Version
		w.Tick = s.state.Tick()
		s.submitMarketClose(w)
	}
}

// sweepMarket caduca, en orden de id, las ofertas que llegaron a su plazo
// (RN-MARKET-008). La llama sweepTreaties, una vez por segundo. Una oferta con
// compras sin confirmar espera al barrido siguiente (RN-MARKET-009).
func (s *Simulation) sweepMarket(now time.Time) {
	ids := make([]int64, 0, len(s.state.market.open))
	for id, o := range s.state.market.open {
		if o.ExpiredAt(now) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if s.state.market.reserved[id] > 0 {
			continue
		}
		s.closeMarketOffer(s.state.market.open[id], market.StatusExpired, uuid.Nil, "")
	}
}

// ─────────────────────────────────────────────────────────────
// Vistas
// ─────────────────────────────────────────────────────────────

// marketView es una oferta con su ciudad (SPEC-MARKETS §6).
func (s *Simulation) marketView(o market.Offer) protocol.MarketOfferView {
	v := protocol.MarketOfferView{
		ID: o.ID, PlayerID: o.Seller.String(), CityID: o.CityID,
		Give:      protocol.ResourceAmount{Resource: string(o.Give.Resource), Amount: o.Give.Amount},
		Want:      protocol.ResourceAmount{Resource: string(o.Want.Resource), Amount: o.Want.Amount},
		LotsTotal: o.LotsTotal, LotsLeft: o.LotsLeft, Status: string(o.Status), ExpiresAtMs: o.ExpiresAt.UnixMilli(),
	}
	// En una retirada o una caducidad, LotsLeft son los devueltos: tampoco se
	// vendieron.
	v.LotsSold = o.LotsTotal - o.LotsLeft
	if c, ok := s.state.City(o.CityID); ok {
		name, civ, x, y := c.Name, string(c.Civilization), c.CenterX, c.CenterY
		v.CityName, v.CityX, v.CityY = &name, &x, &y
		if civ != "" {
			v.Civilization = &civ
		}
	}
	return v
}

// broadcastMarket lleva una oferta a las sesiones de su vendedor y, con su
// requestId, a quien dio la orden (SPEC-MARKETS §6).
func (s *Simulation) broadcastMarket(o market.Offer, orderedBy uuid.UUID, requestID string) {
	v := s.marketView(o)
	rid := ""
	if orderedBy == o.Seller {
		rid = requestID
	}
	s.deps.Broadcaster.SendToPlayer(o.Seller, protocol.TypeMarketUpdate, rid, protocol.MarketUpdatePayload{Offer: v})
	if orderedBy != uuid.Nil && orderedBy != o.Seller {
		s.deps.Broadcaster.SendToPlayer(orderedBy, protocol.TypeMarketUpdate, requestID, protocol.MarketUpdatePayload{Offer: v})
	}
}

// marketOffersOf son las ofertas abiertas de un jugador, por id, para su
// snapshot.
func (s *Simulation) marketOffersOf(viewer uuid.UUID) []protocol.MarketOfferView {
	var os []*market.Offer
	for _, o := range s.state.market.open {
		if o.Seller == viewer {
			os = append(os, o)
		}
	}
	sort.Slice(os, func(i, j int) bool { return os[i].ID < os[j].ID })
	out := make([]protocol.MarketOfferView, 0, len(os))
	for _, o := range os {
		out = append(out, s.marketView(*o))
	}
	return out
}
