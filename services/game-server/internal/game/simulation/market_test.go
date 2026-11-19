package simulation_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/caravan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/market"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
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

// La carga sólo instala las abiertas. (Que una publicación en vuelo cuente para
// las 3 abiertas lo prueba TestUnaPublicacionEnVueloCuentaParaElTope.)
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

// Q10 y RN-MARKET-002: una publicación en vuelo —reservada en el tick, sin la
// confirmación de su transacción— cuenta para las 3 abiertas. Cuatro en el mismo
// tick: la cuarta se rechaza sin apartar nada, aunque ninguna esté aún en el
// tablón.
func TestUnaPublicacionEnVueloCuentaParaElTope(t *testing.T) {
	h, _ := mercadoConCompradores(t, 1)
	orden := func() simulation.Command {
		return simulation.PostMarketOffer{PlayerID: h.playerID, RequestID: uuid.NewString(), Give: piedra(1), Want: madera(1), Lots: 1}
	}

	h.commands <- orden()
	h.commands <- orden()
	h.commands <- orden()
	h.commands <- orden()
	h.loop.Step(h.clk.NowMs())
	_, enElTablon := h.state.MarketOffer(primeraOfertaDelMercado)
	require.False(t, enElTablon, "en este tick las tres van en vuelo")
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "la cuarta, por las tres en vuelo")
	_, s := h.recursos(1)
	assert.Equal(t, int64(100-3*2), s, "sólo las tres primeras apartan su lote y su comisión")

	h.tick() // las confirmaciones vuelven por el buzón
	assert.Len(t, h.market.posts, 3)
	assert.Len(t, h.sim.BuildSnapshot(h.playerID, nil, h.clk.NowMs(), false).MarketOffers, 3)
}

// ADR-047 §2 y §3: el cierre de una oferta —retirada o caducidad— va con las
// claves de la oferta y de la ciudad del vendedor, para que no se cruce con una
// compra de la misma oferta ni con otra escritura de ese almacén; la publicación,
// sólo con la de su ciudad.
func TestLasClavesDelCierreDelMercado(t *testing.T) {
	h, _ := mercadoConCompradores(t, 1)
	h.publicar(h.playerID, piedra(10), madera(10), 1)
	h.publicar(h.playerID, piedra(10), madera(10), 1)
	retirada, caducada := primeraOfertaDelMercado, primeraOfertaDelMercado+1

	h.retirar(h.playerID, retirada)
	h.advance(24*time.Hour + time.Second)
	_, sigue := h.state.MarketOffer(caducada)
	require.False(t, sigue)

	cierre := func(id int64) clavesDeTrabajo {
		return clavesDeTrabajo{Name: simulation.JobMarketClose, Key: simulation.MarketKey(id), Key2: simulation.CityKey(1)}
	}
	assert.Equal(t, []clavesDeTrabajo{cierre(retirada), cierre(caducada)}, h.persister.clavesDe(simulation.JobMarketClose))
	publicacion := clavesDeTrabajo{Name: simulation.JobMarketPost, Key: simulation.CityKey(1)}
	assert.Equal(t, []clavesDeTrabajo{publicacion, publicacion}, h.persister.clavesDe(simulation.JobMarketPost))
	cierres := h.market.cierres()
	require.Len(t, cierres, 2)
	assert.Equal(t, market.StatusWithdrawn, cierres[0].Offer.Status)
	assert.Equal(t, market.StatusExpired, cierres[1].Offer.Status)
}

// RN-MARKET-003 y SPEC-MARKETS §4.4: la custodia no está en el almacén, así que
// el saqueo no la ve. El vendedor publica 3 lotes de 10 de madera (30 en custodia
// y 3 de comisión: le quedan 67); un saqueo lo deja en el suelo, 50, y la oferta
// conserva sus 3 lotes, que vuelven enteros al retirarla.
func TestUnSaqueoNoTocaLaCustodia(t *testing.T) {
	h := newHarness(t)
	victima, ciudad, _ := conSaqueo(h, 100, world.Tile{X: 20, Y: 37})
	ciudad.Era = city.EraBronze
	h.mercadoEn(ciudad.ID)
	h.publicar(victima, madera(10), piedra(1), 3)
	require.Empty(t, h.codigos())
	require.Equal(t, int64(67), h.state.Stock(ciudad.ID).Amount)

	h.saquear(guerreroID, ciudad.ID)
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(200, func() bool {
		_, sigue := h.state.Pillaging(guerreroID)
		return !sigue
	}))
	h.tick()
	assert.Equal(t, int64(50), h.state.Stock(ciudad.ID).Amount, "el saqueo baja el almacén hasta el suelo")
	robado := int32(0)
	for _, p := range h.combat.pillages {
		robado += p.Amount
	}
	assert.Equal(t, int32(17), robado, "sólo lo que había por encima del suelo, sin la custodia")
	o, ok := h.state.MarketOffer(primeraOfertaDelMercado)
	require.True(t, ok)
	assert.Equal(t, int32(3), o.LotsLeft, "la oferta conserva sus lotes")

	h.retirar(victima, primeraOfertaDelMercado)
	require.Empty(t, h.codigos())
	assert.Equal(t, int64(80), h.state.Stock(ciudad.ID).Amount, "vuelven los 30 de la custodia")
}

// Recuperación (SPEC-MARKETS §9 «recovery», RN-MARKET-008, INV-TRADE-008): tras
// un reinicio, una oferta abierta caduca en su instante absoluto —el de la base,
// no el del arranque— y devuelve su custodia una sola vez; la que caducó con el
// servidor parado se cierra en el primer barrido.
func TestTrasUnReinicioLaOfertaCaducaEnSuInstanteYDevuelveUnaVez(t *testing.T) {
	h, _ := mercadoConCompradores(t, 1)
	ahora := time.UnixMilli(h.clk.NowMs()).UTC()
	h.almacen(1, 0, 34) // lo que dejó en la base la publicación del ejemplo
	abierta := market.Offer{ID: 7, Seller: h.playerID, CityID: 1, Give: piedra(20), Want: madera(30),
		LotsTotal: 3, LotsLeft: 3, SellerCommission: 6, Status: market.StatusOpen, RequestID: "a",
		CreatedAt: ahora.Add(-24*time.Hour + 10*time.Second), ExpiresAt: ahora.Add(10 * time.Second)}
	vencida := market.Offer{ID: 8, Seller: h.playerID, CityID: 1, Give: piedra(5), Want: madera(5),
		LotsTotal: 2, LotsLeft: 1, SellerCommission: 2, Status: market.StatusOpen, RequestID: "b",
		CreatedAt: ahora.Add(-25 * time.Hour), ExpiresAt: ahora.Add(-time.Hour)}
	h.state.SetMarketOffers([]market.Offer{abierta, vencida})

	h.hasta(ahora.UnixMilli() + 1_000)
	_, sigue := h.state.MarketOffer(8)
	assert.False(t, sigue, "la vencida durante la caída se cierra en el primer barrido")
	_, s := h.recursos(1)
	assert.Equal(t, int64(39), s, "34 + el lote que le quedaba")

	h.hasta(abierta.ExpiresAt.UnixMilli() - 100)
	_, sigue = h.state.MarketOffer(7)
	assert.True(t, sigue, "antes de su instante sigue abierta")
	h.hasta(abierta.ExpiresAt.UnixMilli() + 1_000)
	_, sigue = h.state.MarketOffer(7)
	assert.False(t, sigue, "caduca en el barrido de su instante")
	_, s = h.recursos(1)
	assert.Equal(t, int64(99), s, "vuelven los 60 de la custodia; las comisiones no")

	h.advance(time.Minute)
	_, s = h.recursos(1)
	assert.Equal(t, int64(99), s, "una sola vez")
	cierres := h.market.cierres()
	require.Len(t, cierres, 2)
	assert.Equal(t, [2]int64{8, 7}, [2]int64{cierres[0].Offer.ID, cierres[1].Offer.ID})
	assert.Equal(t, piedra(60), cierres[1].Returned)
	assert.Equal(t, int64(60), cierres[1].Stock.Delta)
}

// Recuperación (SPEC-MARKETS §9 «recovery», RN-MARKET-005, INV-TRADE-008): el
// servidor cae con una compra reservada en la RAM y sin confirmar. La compra es
// una transacción, así que la base guarda o nada o todo, y la carga parte de ahí
// sin reservas fantasma: con nada, la oferta tiene sus lotes, el comprador su
// madera y no hay caravanas; con todo, la oferta tiene los lotes de menos, el
// comprador ha pagado y salen las dos caravanas, que llegan. En los dos casos la
// oferta se puede volver a comprar o retirar en el acto.
func TestTrasUnReinicioUnaCompraSinConfirmarNoQuedaAMedias(t *testing.T) {
	// La primera vida: el ejemplo de §4.3, 2 de 3 lotes de 20 de piedra por 30
	// de madera. Lo que escribió la compra es lo que la base tendría con «todo».
	vida, compradores := mercadoConCompradores(t, 1)
	vida.publicar(vida.playerID, piedra(20), madera(30), 3)
	publicada, ok := vida.state.MarketOffer(primeraOfertaDelMercado)
	require.True(t, ok)
	vida.comprar(compradores[0], primeraOfertaDelMercado, 2)
	require.Empty(t, vida.codigos())
	vendida, ok := vida.state.MarketOffer(primeraOfertaDelMercado)
	require.True(t, ok)
	require.Len(t, vida.market.buys, 1)
	escrita := vida.market.buys[0]
	require.Len(t, escrita.Caravans, 2)

	reiniciar := func(t *testing.T, oferta market.Offer, maderaComprador int64) (*harness, uuid.UUID) {
		h, cs := mercadoConCompradores(t, 1)
		h.hasta(vida.clk.NowMs())
		h.almacen(1, 0, 34)
		h.almacen(2, maderaComprador, 0)
		h.state.SetMarketOffers([]market.Offer{oferta})
		return h, cs[0]
	}

	t.Run("nada", func(t *testing.T) {
		h, comprador := reiniciar(t, publicada, 100)
		assert.Zero(t, h.state.CaravanCount())
		h.comprar(comprador, primeraOfertaDelMercado, 3)
		require.Empty(t, h.codigos(), "los 3 lotes siguen a la venta: ninguna reserva sobrevive")
		w, _ := h.recursos(2)
		assert.Equal(t, int64(100-90-9), w)
		_, sigue := h.state.MarketOffer(primeraOfertaDelMercado)
		assert.False(t, sigue, "agotada")
	})

	t.Run("todo", func(t *testing.T) {
		h, _ := reiniciar(t, vendida, 34)
		var unidades []*unit.Unit
		var movimientos []*movement.Movement
		var caravanas []caravan.Caravan
		for i, sp := range escrita.Caravans {
			id := primeraCaravana + int64(i)
			from := sp.Half.FromCity
			unidades = append(unidades, &unit.Unit{ID: id, PlayerID: sp.Half.Sender, CityID: &from, Type: unit.TypeCaravan,
				X: sp.Origin.X, Y: sp.Origin.Y, HP: sp.MaxHP, MaxHP: sp.MaxHP, Status: unit.StatusMoving})
			m := sp.Movement
			m.ID, m.UnitID = 9000+int64(i), id
			movimientos = append(movimientos, &m)
			caravanas = append(caravanas, caravan.Caravan{ID: int64(i + 1), UnitID: id, PurchaseID: 5001,
				Sender: sp.Half.Sender, FromCity: sp.Half.FromCity, ToCity: sp.Half.ToCity, OriginalToCity: sp.Half.ToCity,
				Cargo: sp.Half.Cargo, Status: caravan.StatusInTransit})
		}
		simulation.Hydrate(h.state, unidades, nil, movimientos, h.clk.NowMs(), slog.New(slog.NewTextHandler(io.Discard, nil)))
		h.state.SetCaravans(caravanas)
		assert.Equal(t, 2, h.state.CaravanCount(), "salieron las dos")

		h.retirar(h.playerID, primeraOfertaDelMercado)
		require.Empty(t, h.codigos(), "sin una compra en curso fantasma, se retira")
		_, s := h.recursos(1)
		assert.Equal(t, int64(54), s, "vuelve el lote que quedaba, no los vendidos")

		h.advance(2 * time.Minute)
		assert.Zero(t, h.state.CaravanCount())
		w, s := h.recursos(1)
		assert.Equal(t, [2]int64{60, 54}, [2]int64{w, s}, "el vendedor recibe lo pagado")
		w, s = h.recursos(2)
		assert.Equal(t, [2]int64{34, 40}, [2]int64{w, s}, "el comprador, lo comprado, una vez")
	})
}
