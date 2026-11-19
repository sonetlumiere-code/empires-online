package websocket_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El mercado de extremo a extremo (SPEC-MARKETS §9, «e2e»): servidor WebSocket,
// hub, loop y simulación reales, con la base en memoria. Cada mensaje recibido
// pasa por la validación contra el JSON Schema, también market.update y
// market.listing.

// memoryMarket hace de base del mercado: da ids a las ofertas y a las compras.
type memoryMarket struct {
	mu        sync.Mutex
	nextID    int64
	purchases int64
	caravans  int64
}

func (m *memoryMarket) Post(_ context.Context, _ simulation.MarketPostWrite) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	return m.nextID, nil
}

// Buy acepta la compra y da ids a sus caravanas, desde la unidad 950.
func (m *memoryMarket) Buy(_ context.Context, b simulation.MarketBuyWrite) (int64, []simulation.CaravanCreated, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purchases++
	out := make([]simulation.CaravanCreated, len(b.Caravans))
	for i := range b.Caravans {
		m.caravans++
		out[i] = simulation.CaravanCreated{CaravanID: 500 + m.caravans, UnitID: 949 + m.caravans}
	}
	return m.purchases, out, nil
}

func (m *memoryMarket) Close(context.Context, simulation.MarketCloseWrite) error { return nil }

// tablonDePrueba hace de tablón leído de la base: devuelve lo que se le da y
// anota quién lo pidió.
type tablonDePrueba struct {
	mu      sync.Mutex
	offers  []protocol.MarketOfferView
	viewers []uuid.UUID
	filtros [][2]string
}

func (b *tablonDePrueba) Board(_ context.Context, viewer uuid.UUID, receive, pay string) ([]protocol.MarketOfferView, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.viewers = append(b.viewers, viewer)
	b.filtros = append(b.filtros, [2]string{receive, pay})
	return b.offers, nil
}

type marketUpdate struct {
	Offer protocol.MarketOfferView `json:"offer"`
}

// conMercado deja una ciudad en Bronce con un mercado completo.
func conMercado(env *e2e, cityID int64) {
	c, ok := env.state.City(cityID)
	require.True(env.t, ok)
	c.Era = city.EraBronze
	env.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{{
		ID: 700 + cityID, CityID: cityID, Type: building.TypeMarket, X: c.CenterX + 1, Y: c.CenterY - 1,
		Status: building.StatusComplete, CompletesAtMs: env.clk.NowMs(),
	}}})
}

// market.post → market.update OPEN al vendedor; market.list → market.listing sólo
// a quien lo pidió; market.buy → market.update al vendedor y al comprador, con
// los lotes vendidos, y las dos caravanas en camino; market.cancel → WITHDRAWN.
// Un lote fuera de rango es INVALID_MESSAGE; comprar la propia, INVALID_TARGET.
func TestElMercadoDePuntaAPuntaConDosJugadores(t *testing.T) {
	env := newE2E(t)
	otro := uuid.New()
	env.state.AddCity(&city.City{
		Civilization: "PERSIAN",
		ID:           2, OwnerPlayerID: otro, Name: "Vecinópolis",
		CenterX: 80, CenterY: 40, Era: city.EraStone,
		Population: 1, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	})
	conMercado(env, 1)
	conMercado(env, 2)
	env.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 100, Version: 1}},
		{CityID: 2, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 100, Version: 1}},
	}})
	tablon := &tablonDePrueba{}
	env.ws.SetMarket(tablon)
	vendedor, _ := conectar(t, env, env.playerID)
	comprador, _ := conectar(t, env, otro)

	lote := func(r string, n int64) map[string]any { return map[string]any{"resource": r, "amount": n} }

	// El esquema rechaza 101 lotes antes de llegar al tick.
	req := vendedor.send(protocol.TypeMarketPost, map[string]any{"give": lote("STONE", 20), "want": lote("WOOD", 30), "lots": 101})
	var errEnv envelope
	env.avanzandoMientras(func() { errEnv = vendedor.await(protocol.TypeSystemError, 5*time.Second) })
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInvalidMessage, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	req = vendedor.send(protocol.TypeMarketPost, map[string]any{"give": lote("STONE", 20), "want": lote("WOOD", 30), "lots": 3})
	var publicada envelope
	env.avanzandoMientras(func() {
		publicada, _ = vendedor.awaitWhere(protocol.TypeMarketUpdate, func(raw json.RawMessage) bool {
			return decode[marketUpdate](t, raw).Offer.Status == "OPEN"
		}, 5*time.Second)
	})
	assert.Equal(t, req, publicada.RequestID)
	oferta := decode[marketUpdate](t, publicada.Payload).Offer
	assert.Equal(t, int32(3), oferta.LotsLeft)
	assert.Equal(t, int64(34), env.state.StockOf(1, economy.ResourceStone).Amount, "60 en custodia y 6 de comisión")

	// El tablón: lo lee la conexión, sólo para quien lo pidió.
	tablon.offers = []protocol.MarketOfferView{oferta}
	req = comprador.send(protocol.TypeMarketList, map[string]any{"receive": "STONE"})
	var listado envelope
	env.avanzandoMientras(func() { listado = comprador.await(protocol.TypeMarketListing, 5*time.Second) })
	assert.Equal(t, req, listado.RequestID)
	lista := decode[protocol.MarketListingPayload](t, listado.Payload).Offers
	require.Len(t, lista, 1)
	assert.Equal(t, oferta.ID, lista[0].ID)
	require.Len(t, tablon.viewers, 1)
	assert.Equal(t, otro, tablon.viewers[0], "INV-SEC-001: el tablón es el de quien mira, nunca el que diga el cliente")
	assert.Equal(t, [2]string{"STONE", ""}, tablon.filtros[0])

	// Comprar la propia no se puede.
	req = vendedor.send(protocol.TypeMarketBuy, map[string]any{"offerId": oferta.ID, "lots": 1})
	env.avanzandoMientras(func() { errEnv = vendedor.await(protocol.TypeSystemError, 5*time.Second) })
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInvalidTarget, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	// La compra de 2 lotes.
	req = comprador.send(protocol.TypeMarketBuy, map[string]any{"offerId": oferta.ID, "lots": 2})
	var vista, delComprador envelope
	env.avanzandoMientras(func() {
		vista, _ = vendedor.awaitWhere(protocol.TypeMarketUpdate, func(raw json.RawMessage) bool {
			return decode[marketUpdate](t, raw).Offer.LotsSold == 2
		}, 5*time.Second)
		delComprador = comprador.await(protocol.TypeMarketUpdate, 5*time.Second)
	})
	assert.Empty(t, vista.RequestID, "al vendedor, sin el requestId ajeno")
	assert.Equal(t, req, delComprador.RequestID)
	assert.Equal(t, int32(1), decode[marketUpdate](t, delComprador.Payload).Offer.LotsLeft)
	assert.Equal(t, int64(34), env.state.StockOf(2, economy.ResourceWood).Amount, "60 que paga y 6 de comisión")
	assert.Equal(t, 2, env.state.CaravanCount())

	// La retirada del lote que queda.
	vendedor.send(protocol.TypeMarketCancel, map[string]any{"offerId": oferta.ID})
	env.avanzandoMientras(func() {
		_, _ = vendedor.awaitWhere(protocol.TypeMarketUpdate, func(raw json.RawMessage) bool {
			v := decode[marketUpdate](t, raw).Offer
			return v.ID == oferta.ID && v.Status == "WITHDRAWN"
		}, 5*time.Second)
	})
	assert.Equal(t, int64(54), env.state.StockOf(1, economy.ResourceStone).Amount, "vuelven 20; la comisión, no")
}
