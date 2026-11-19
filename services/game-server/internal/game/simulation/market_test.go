package simulation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/market"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests del mercado (SPEC-MARKETS §9). Mundo de newHarness: el vendedor es el
// jugador del arnés, con su ciudad 1 en (10,10); los compradores, vecinos de
// conVecinos con las ciudades 2 y 3 en (20,40) y (28,40). Todos en Bronce y con
// un mercado completo.

// fakeMarket hace de base: da ids a las ofertas y a las compras y anota cada
// escritura, en orden.
type fakeMarket struct {
	mu           sync.Mutex
	nextID       int64
	nextPurchase int64
	posts        []simulation.MarketPostWrite
	buys         []simulation.MarketBuyWrite
	closes       []simulation.MarketCloseWrite
	caravans     int64
	buyErr       error
	closeErr     error
}

func (f *fakeMarket) Post(_ context.Context, p simulation.MarketPostWrite) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, p)
	f.nextID++
	return f.nextID, nil
}

func (f *fakeMarket) Buy(_ context.Context, b simulation.MarketBuyWrite) (int64, []simulation.CaravanCreated, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buyErr != nil {
		return 0, nil, f.buyErr
	}
	f.buys = append(f.buys, b)
	f.nextPurchase++
	out := make([]simulation.CaravanCreated, len(b.Caravans))
	for i := range b.Caravans {
		f.caravans++
		out[i] = simulation.CaravanCreated{CaravanID: f.caravans, UnitID: primeraCaravana - 1 + f.caravans}
	}
	return f.nextPurchase, out, nil
}

func (f *fakeMarket) Close(_ context.Context, c simulation.MarketCloseWrite) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closeErr != nil {
		return f.closeErr
	}
	f.closes = append(f.closes, c)
	return nil
}

func (f *fakeMarket) cierres() []simulation.MarketCloseWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]simulation.MarketCloseWrite(nil), f.closes...)
}

// primeraOfertaDelMercado es el id que el almacén falso da a la primera oferta.
const primeraOfertaDelMercado int64 = 4001

// mercadoEn da a una ciudad un mercado completo en un tile de su zona urbana.
func (h *harness) mercadoEn(cityID int64) world.Tile {
	h.t.Helper()
	c, ok := h.state.City(cityID)
	require.True(h.t, ok)
	at := world.Tile{X: c.CenterX + 1, Y: c.CenterY - 1}
	h.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{{
		ID: 800 + cityID, CityID: cityID, Type: building.TypeMarket, X: at.X, Y: at.Y,
		Status: building.StatusComplete, CompletesAtMs: h.clk.NowMs(),
	}}})
	return at
}

// mercadoDerribado deja en ruinas el mercado de una ciudad (el resultado de un
// asedio, sin el combate).
func (h *harness) mercadoDerribado(cityID int64) {
	h.t.Helper()
	at := world.Tile{}
	if c, ok := h.state.City(cityID); ok {
		at = world.Tile{X: c.CenterX + 1, Y: c.CenterY - 1}
	}
	h.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{{
		ID: 800 + cityID, CityID: cityID, Type: building.TypeMarket, X: at.X, Y: at.Y,
		Status: building.StatusDestroyed,
	}}})
}

// mercadoConCompradores prepara al vendedor y n compradores en Bronce con
// mercado; el vendedor con 100 de piedra, cada comprador con 100 de madera.
func mercadoConCompradores(t *testing.T, n int) (*harness, []uuid.UUID) {
	t.Helper()
	h := newHarness(t)
	compradores := conVecinos(h, n)
	h.ciudad().Era = city.EraBronze
	h.mercadoEn(h.cityID)
	h.almacen(h.cityID, 0, 100)
	for i := range compradores {
		id := int64(i + 2)
		c, _ := h.state.City(id)
		c.Era = city.EraBronze
		h.mercadoEn(id)
		h.almacen(id, 100, 0)
	}
	return h, compradores
}

func (h *harness) publicar(quien uuid.UUID, give, want economy.Cost, lots int32) {
	h.t.Helper()
	h.send(simulation.PostMarketOffer{PlayerID: quien, RequestID: uuid.NewString(), Give: give, Want: want, Lots: lots})
	h.tick() // la confirmación vuelve por el buzón
}

func (h *harness) comprar(quien uuid.UUID, id int64, lots int32) {
	h.t.Helper()
	h.send(simulation.BuyMarketOffer{PlayerID: quien, RequestID: uuid.NewString(), OfferID: id, Lots: lots})
	h.tick()
}

func (h *harness) retirar(quien uuid.UUID, id int64) {
	h.t.Helper()
	h.send(simulation.CancelMarketOffer{PlayerID: quien, RequestID: uuid.NewString(), OfferID: id})
	h.tick()
}

// mismoTick manda varios comandos que el loop aplica en el mismo tick, en orden.
func (h *harness) mismoTick(cmds ...simulation.Command) {
	h.t.Helper()
	for _, c := range cmds {
		h.commands <- c
	}
	h.loop.Step(h.clk.NowMs())
	h.tick()
}

// ofertasPara son los market.update recibidos por un jugador.
func (h *harness) ofertasPara(p uuid.UUID) []protocol.MarketOfferView {
	var out []protocol.MarketOfferView
	for _, m := range h.rec.byType(protocol.TypeMarketUpdate) {
		if m.Kind == "player" && m.PlayerID == p {
			out = append(out, m.Payload.(protocol.MarketUpdatePayload).Offer)
		}
	}
	return out
}

// Golden «mercado» (SPEC-MARKETS §4.2 a §4.6, INV-TRADE-008): el ejemplo de la
// spec, 3 lotes de 20 de piedra por 30 de madera. Publicarlo aparta 66 de piedra
// (60 en custodia y 6 de comisión); comprar 2 lotes cuesta 66 de madera (60 y 6)
// y saca dos caravanas, una con 40 de piedra y otra con 60 de madera; las dos
// llegan, y a las 24 h el lote que queda vuelve al vendedor sin la comisión.
func TestGoldenMercadoPublicarComprarLlegarYCaducar(t *testing.T) {
	h, compradores := mercadoConCompradores(t, 1)
	comprador := compradores[0]

	// La publicación: en su tick, la piedra ya está reservada.
	h.send(simulation.PostMarketOffer{PlayerID: h.playerID, RequestID: uuid.NewString(), Give: piedra(20), Want: madera(30), Lots: 3})
	_, s := h.recursos(1)
	assert.Equal(t, int64(34), s, "100 − 60 de custodia − 6 de comisión")
	h.tick()
	require.Empty(t, h.codigos())
	o, ok := h.state.MarketOffer(primeraOfertaDelMercado)
	require.True(t, ok, "la oferta está en el tablón")
	assert.Equal(t, market.StatusOpen, o.Status)
	assert.Equal(t, int32(3), o.LotsLeft)
	assert.Equal(t, 24*time.Hour, o.ExpiresAt.Sub(o.CreatedAt), "Q10: 24 h")
	require.Len(t, h.market.posts, 1)
	assert.Equal(t, int64(-66), h.market.posts[0].Stock.Delta)
	vistas := h.ofertasPara(h.playerID)
	require.Len(t, vistas, 1)
	assert.Equal(t, "OPEN", vistas[0].Status)
	assert.Equal(t, protocol.ResourceAmount{Resource: "STONE", Amount: 20}, vistas[0].Give)
	require.NotNil(t, vistas[0].CityName)
	assert.Equal(t, "Testópolis", *vistas[0].CityName)
	assert.Empty(t, h.ofertasPara(comprador), "publicar no avisa a nadie más")

	// La compra de 2 lotes: en su tick, la madera del comprador ya no está.
	h.send(simulation.BuyMarketOffer{PlayerID: comprador, RequestID: uuid.NewString(), OfferID: primeraOfertaDelMercado, Lots: 2})
	w, _ := h.recursos(2)
	assert.Equal(t, int64(34), w, "100 − 60 que paga − 6 de comisión")
	h.tick()
	require.Empty(t, h.codigos())
	o, _ = h.state.MarketOffer(primeraOfertaDelMercado)
	assert.Equal(t, int32(1), o.LotsLeft)
	assert.Equal(t, 2, h.state.CaravanCount(), "dos caravanas, una por sentido")
	require.Len(t, h.market.buys, 1)
	b := h.market.buys[0]
	assert.Equal(t, int64(-66), b.Stock.Delta)
	assert.Equal(t, piedra(40), b.Purchase.Receive)
	assert.Equal(t, madera(60), b.Purchase.Pay)
	require.Len(t, b.Caravans, 2)
	assert.Equal(t, economy.Costs{piedra(40)}, b.Caravans[0].Half.Cargo, "la del vendedor, con lo comprado")
	assert.Equal(t, int64(1), b.Caravans[0].Half.FromCity)
	assert.Equal(t, economy.Costs{madera(60)}, b.Caravans[1].Half.Cargo, "la del comprador, sin su comisión")
	assert.Equal(t, int64(2), b.Caravans[1].Half.FromCity)
	assert.Len(t, h.ofertasPara(comprador), 1, "el comprador recibe la oferta con sus lotes")
	ultima := h.ofertasPara(h.playerID)
	assert.Equal(t, int32(2), ultima[len(ultima)-1].LotsSold)

	// ADR-047 §2: la compra, con las claves de la ciudad del comprador y de la
	// oferta, nunca la del vendedor.
	var claves []clavesDeTrabajo
	for _, c := range h.persister.claves {
		if c.Name == simulation.JobMarketBuy {
			claves = append(claves, c)
		}
	}
	require.Len(t, claves, 1)
	assert.Equal(t, simulation.CityKey(2), claves[0].Key)
	assert.Equal(t, simulation.MarketKey(primeraOfertaDelMercado), claves[0].Key2)

	// Las dos llegan: 40 de piedra al comprador y 60 de madera al vendedor.
	h.advance(2 * time.Minute)
	w, s = h.recursos(1)
	assert.Equal(t, [2]int64{60, 34}, [2]int64{w, s})
	w, s = h.recursos(2)
	assert.Equal(t, [2]int64{34, 40}, [2]int64{w, s})
	assert.Equal(t, 0, h.state.CaravanCount())

	// La caducidad: vuelve el lote que queda, 20 de piedra, y la comisión no.
	h.advance(24 * time.Hour)
	_, sigue := h.state.MarketOffer(primeraOfertaDelMercado)
	assert.False(t, sigue, "la oferta caducada sale del tablón")
	_, s = h.recursos(1)
	assert.Equal(t, int64(54), s, "34 + 20 devueltos")
	cierres := h.market.cierres()
	require.Len(t, cierres, 1)
	assert.Equal(t, market.StatusExpired, cierres[0].Offer.Status)
	assert.Equal(t, piedra(20), cierres[0].Returned)
	assert.Equal(t, int32(1), cierres[0].Offer.LotsLeft, "los lotes devueltos")
	assert.Equal(t, uuid.Nil, cierres[0].By)
	ultima = h.ofertasPara(h.playerID)
	assert.Equal(t, "EXPIRED", ultima[len(ultima)-1].Status)
}

// SPEC-MARKETS §5: cada rechazo de publicar, comprar y retirar.
func TestRechazosDelMercado(t *testing.T) {
	h, compradores := mercadoConCompradores(t, 1)
	comprador := compradores[0]

	h.publicar(h.playerID, piedra(10), piedra(10), 1)
	h.publicar(h.playerID, piedra(10_001), madera(1), 100)
	h.publicar(h.playerID, piedra(60), madera(1), 2) // 120 + 12 > 100
	assert.Equal(t, []string{protocol.CodeInvalidTarget, protocol.CodeInvalidMessage, protocol.CodeInsufficientResources}, h.codigos())
	_, s := h.recursos(1)
	assert.Equal(t, int64(100), s, "ningún rechazo aparta nada")

	h.rec.reset()
	for i := 0; i < 3; i++ {
		h.publicar(h.playerID, piedra(1), madera(1), 1)
	}
	h.publicar(h.playerID, piedra(1), madera(1), 1)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "Q10: 3 abiertas por jugador")

	h.rec.reset()
	h.comprar(h.playerID, primeraOfertaDelMercado, 1)
	h.comprar(comprador, primeraOfertaDelMercado, 2)
	h.comprar(comprador, 9999, 1)
	h.retirar(comprador, primeraOfertaDelMercado)
	assert.Equal(t, []string{protocol.CodeInvalidTarget, protocol.CodeInvalidTarget, protocol.CodeInvalidTarget, protocol.CodeInvalidTarget}, h.codigos(),
		"INV-TRADE-010, sin tantos lotes, inexistente y ajena")

	h.rec.reset()
	h.almacen(2, 1, 0) // 1 de madera + 1 de comisión
	h.comprar(comprador, primeraOfertaDelMercado, 1)
	assert.Equal(t, []string{protocol.CodeInsufficientResources}, h.codigos())

	h.rec.reset()
	h.mercadoDerribado(2)
	h.almacen(2, 100, 0)
	h.comprar(comprador, primeraOfertaDelMercado, 1)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "sin mercado no se compra")
	assert.Empty(t, h.market.buys)

	// Sin mercado no se publica.
	sinMercado := newHarness(t)
	sinMercado.ciudad().Era = city.EraBronze
	sinMercado.almacen(1, 0, 100)
	sinMercado.publicar(sinMercado.playerID, piedra(1), madera(1), 1)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, sinMercado.codigos())
}

// RN-MARKET-009 e INV-TRADE-009: dos compras en el mismo tick caben mientras
// queden lotes sin reservar; la que no cabe se rechaza, y la oferta nunca vende
// más de lo que publicó.
func TestDosComprasEnElMismoTick(t *testing.T) {
	h, compradores := mercadoConCompradores(t, 2)
	h.publicar(h.playerID, piedra(10), madera(10), 3)

	h.mismoTick(
		simulation.BuyMarketOffer{PlayerID: compradores[0], RequestID: uuid.NewString(), OfferID: primeraOfertaDelMercado, Lots: 2},
		simulation.BuyMarketOffer{PlayerID: compradores[1], RequestID: uuid.NewString(), OfferID: primeraOfertaDelMercado, Lots: 2},
	)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "la segunda ya no cabe")
	w, _ := h.recursos(3)
	assert.Equal(t, int64(100), w, "la rechazada no aparta nada")

	h.rec.reset()
	h.mismoTick(
		simulation.BuyMarketOffer{PlayerID: compradores[1], RequestID: uuid.NewString(), OfferID: primeraOfertaDelMercado, Lots: 1},
	)
	require.Empty(t, h.codigos())
	_, sigue := h.state.MarketOffer(primeraOfertaDelMercado)
	assert.False(t, sigue, "el último lote la agota")
	require.Len(t, h.market.buys, 2)
	total := int32(0)
	for _, b := range h.market.buys {
		total += b.Purchase.Lots
	}
	assert.Equal(t, int32(3), total)
	ultima := h.ofertasPara(h.playerID)
	assert.Equal(t, "SOLD_OUT", ultima[len(ultima)-1].Status)
}

// RN-MARKET-009: con una compra sin confirmar, la oferta no se retira; después
// sí, y vuelve lo que queda sin la comisión (RN-MARKET-007).
func TestRetirarConUnaCompraEnCursoSeRechaza(t *testing.T) {
	h, compradores := mercadoConCompradores(t, 1)
	h.publicar(h.playerID, piedra(10), madera(10), 3)

	h.mismoTick(
		simulation.BuyMarketOffer{PlayerID: compradores[0], RequestID: uuid.NewString(), OfferID: primeraOfertaDelMercado, Lots: 1},
		simulation.CancelMarketOffer{PlayerID: h.playerID, RequestID: uuid.NewString(), OfferID: primeraOfertaDelMercado},
	)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	o, ok := h.state.MarketOffer(primeraOfertaDelMercado)
	require.True(t, ok)
	assert.Equal(t, int32(2), o.LotsLeft)

	h.rec.reset()
	_, antes := h.recursos(1)
	h.retirar(h.playerID, primeraOfertaDelMercado)
	require.Empty(t, h.codigos())
	_, despues := h.recursos(1)
	assert.Equal(t, int64(20), despues-antes, "vuelven los 2 lotes que quedan")
	cierres := h.market.cierres()
	require.Len(t, cierres, 1)
	assert.Equal(t, market.StatusWithdrawn, cierres[0].Offer.Status)
	assert.Equal(t, h.playerID, cierres[0].By)
}

// SPEC-MARKETS §4.1: derribar el mercado no cierra la oferta, que se sigue
// comprando; retirarla no pide mercado en pie.
func TestDerribarElMercadoNoCierraLaOferta(t *testing.T) {
	h, compradores := mercadoConCompradores(t, 1)
	h.publicar(h.playerID, piedra(10), madera(10), 2)
	h.mercadoDerribado(1)

	h.comprar(compradores[0], primeraOfertaDelMercado, 1)
	require.Empty(t, h.codigos())
	h.retirar(h.playerID, primeraOfertaDelMercado)
	require.Empty(t, h.codigos())
	_, sigue := h.state.MarketOffer(primeraOfertaDelMercado)
	assert.False(t, sigue)
}

// Una compra que no se escribe devuelve al comprador lo que pagaba y a la oferta
// sus lotes; una que choca con la base responde INVALID_TARGET.
func TestUnaCompraQueNoSeEscribeDevuelveLoReservado(t *testing.T) {
	h, compradores := mercadoConCompradores(t, 1)
	h.publicar(h.playerID, piedra(10), madera(10), 3)
	h.market.buyErr = simulation.ErrMarketConflict

	h.comprar(compradores[0], primeraOfertaDelMercado, 2)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	w, _ := h.recursos(2)
	assert.Equal(t, int64(100), w)
	assert.Equal(t, 0, h.state.CaravanCount())

	h.rec.reset()
	h.market.buyErr = nil
	h.comprar(compradores[0], primeraOfertaDelMercado, 3)
	require.Empty(t, h.codigos(), "los lotes volvieron a la oferta")
}

// ADR-047 §3: un cierre cuya escritura se descarta se reencola en el volcado
// con una versión del stock mayor; el crédito de la RAM no se repite.
func TestUnCierreDescartadoSeReintentaConVersionNueva(t *testing.T) {
	h, _ := mercadoConCompradores(t, 1)
	h.publicar(h.playerID, piedra(10), madera(10), 1)
	h.market.closeErr = errors.New("la base no responde")
	h.persister.permanent = true

	h.retirar(h.playerID, primeraOfertaDelMercado)
	_, s := h.recursos(1)
	assert.Equal(t, int64(100-11+10), s, "la RAM acreditó una vez")

	h.persister.permanent = false
	h.market.closeErr = nil
	version := h.state.StockOf(1, economy.ResourceStone).Version
	h.advance(6 * time.Second) // un volcado
	cierres := h.market.cierres()
	require.Len(t, cierres, 1)
	assert.Greater(t, cierres[0].Stock.Version, version)
	_, s = h.recursos(1)
	assert.Equal(t, int64(99), s, "sin otro crédito")
}

// El snapshot lleva las ofertas abiertas de quien lo recibe, y sólo las suyas.
func TestElSnapshotLlevaLasOfertasPropias(t *testing.T) {
	h, compradores := mercadoConCompradores(t, 1)
	h.publicar(h.playerID, piedra(10), madera(10), 1)

	propio := h.sim.BuildSnapshot(h.playerID, nil, h.clk.NowMs(), false)
	require.Len(t, propio.MarketOffers, 1)
	assert.Equal(t, primeraOfertaDelMercado, propio.MarketOffers[0].ID)
	ajeno := h.sim.BuildSnapshot(compradores[0], nil, h.clk.NowMs(), false)
	assert.Empty(t, ajeno.MarketOffers)
}

// Una oferta en vuelo cuenta para las 3 abiertas, y la carga sólo instala las
// abiertas.
func TestLaCargaInstalaSoloLasAbiertas(t *testing.T) {
	h := newHarness(t)
	abierta := market.Offer{ID: 7, Seller: h.playerID, CityID: 1, Give: piedra(1), Want: madera(1), LotsTotal: 1,
		LotsLeft: 1, Status: market.StatusOpen}
	cerrada := abierta
	cerrada.ID, cerrada.Status = 8, market.StatusWithdrawn
	h.state.SetMarketOffers([]market.Offer{abierta, cerrada})
	_, ok := h.state.MarketOffer(7)
	assert.True(t, ok)
	_, ok = h.state.MarketOffer(8)
	assert.False(t, ok)
}
