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

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El comercio de extremo a extremo (SPEC-TRADE §9, «e2e»): servidor WebSocket,
// hub, loop y simulación reales, con la base en memoria. Cada mensaje recibido
// pasa por la validación contra el JSON Schema, también trade.update.

// memoryTrade hace de base del comercio: da ids a las ofertas y acepta los
// tratos.
type memoryTrade struct {
	mu     sync.Mutex
	nextID int64
}

func (m *memoryTrade) ApplyOffer(_ context.Context, w simulation.TradeWrite) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w.Insert {
		m.nextID++
		return m.nextID, nil
	}
	return w.Offer.ID, nil
}

func (m *memoryTrade) AcceptTrade(context.Context, simulation.TradeAcceptance) error { return nil }

type tradeUpdate struct {
	Offer protocol.TradeOfferView `json:"offer"`
}

// Con un tratado TRADE: trade.propose → trade.update a los dos, cada uno con su
// vista → trade.respond → trade.update ACCEPTED a los dos y los dos almacenes
// movidos. Unas líneas inválidas son INVALID_MESSAGE; ofrecer lo que no se tiene,
// INSUFFICIENT_RESOURCES.
func TestUnTratoDePuntaAPuntaConDosJugadores(t *testing.T) {
	env := newE2E(t)
	otro := uuid.New()
	env.state.AddCity(&city.City{
		ID: 2, OwnerPlayerID: otro, Name: "Vecinópolis",
		CenterX: 80, CenterY: 40, Era: city.EraStone,
		Population: 1, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	})
	a, b := diplomacy.CanonicalPair(env.playerID, otro)
	env.state.SetTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: a, PlayerB: b, Type: diplomacy.TypeTrade,
		Status: diplomacy.StatusActive, ProposedAt: time.UnixMilli(env.clk.NowMs()).UTC()}}, nil)
	env.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 300, Version: 1}},
		{CityID: 2, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 100, Version: 1}},
	}})
	dueno, _ := conectar(t, env, env.playerID)
	vecino, _ := conectar(t, env, otro)

	linea := func(r string, n int64) []map[string]any { return []map[string]any{{"resource": r, "amount": n}} }

	// El esquema rechaza un regalo antes de llegar al tick.
	req := dueno.send(protocol.TypeTradePropose, map[string]any{
		"playerId": otro.String(), "give": linea("WOOD", 10), "want": linea("STONE", 0)})
	var errEnv envelope
	env.avanzandoMientras(func() { errEnv = dueno.await(protocol.TypeSystemError, 5*time.Second) })
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInvalidMessage, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	req = dueno.send(protocol.TypeTradePropose, map[string]any{
		"playerId": otro.String(), "give": linea("WOOD", 301), "want": linea("STONE", 100)})
	env.avanzandoMientras(func() { errEnv = dueno.await(protocol.TypeSystemError, 5*time.Second) })
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInsufficientResources, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	dueno.send(protocol.TypeTradePropose, map[string]any{
		"playerId": otro.String(), "give": linea("WOOD", 300), "want": linea("STONE", 100)})
	var propia, recibida envelope
	env.avanzandoMientras(func() {
		propia, _ = dueno.awaitWhere(protocol.TypeTradeUpdate, func(raw json.RawMessage) bool {
			return decode[tradeUpdate](t, raw).Offer.Status == "PROPOSED"
		}, 5*time.Second)
		recibida, _ = vecino.awaitWhere(protocol.TypeTradeUpdate, func(raw json.RawMessage) bool {
			return decode[tradeUpdate](t, raw).Offer.Status == "PROPOSED"
		}, 5*time.Second)
	})
	vp, vr := decode[tradeUpdate](t, propia.Payload).Offer, decode[tradeUpdate](t, recibida.Payload).Offer
	assert.True(t, vp.ProposedByMe)
	assert.False(t, vr.ProposedByMe)
	assert.Equal(t, []protocol.ResourceAmount{{Resource: "WOOD", Amount: 300}}, vp.Give)
	assert.Equal(t, []protocol.ResourceAmount{{Resource: "STONE", Amount: 100}}, vr.Give, "cada uno ve lo que saldría de su almacén")
	require.NotNil(t, vr.OtherCityName)
	assert.Equal(t, "Testópolis", *vr.OtherCityName)

	vecino.send(protocol.TypeTradeRespond, map[string]any{"offerId": vr.ID, "accept": true})
	env.avanzandoMientras(func() {
		for _, c := range []*conn{dueno, vecino} {
			_, _ = c.awaitWhere(protocol.TypeTradeUpdate, func(raw json.RawMessage) bool {
				v := decode[tradeUpdate](t, raw).Offer
				return v.ID == vr.ID && v.Status == "ACCEPTED"
			}, 5*time.Second)
		}
	})
	assert.Equal(t, int64(100), env.state.StockOf(1, economy.ResourceStone).Amount)
	assert.Equal(t, int64(0), env.state.StockOf(1, economy.ResourceWood).Amount)
	assert.Equal(t, int64(300), env.state.StockOf(2, economy.ResourceWood).Amount)
	assert.Equal(t, int64(0), env.state.StockOf(2, economy.ResourceStone).Amount)
}

// trade.cancel retira la oferta propia: los dos reciben WITHDRAWN.
func TestRetirarUnaOfertaPorLaRed(t *testing.T) {
	env := newE2E(t)
	otro := uuid.New()
	env.state.AddCity(&city.City{
		ID: 2, OwnerPlayerID: otro, Name: "Vecinópolis",
		CenterX: 80, CenterY: 40, Era: city.EraStone,
		Population: 1, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	})
	a, b := diplomacy.CanonicalPair(env.playerID, otro)
	env.state.SetTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: a, PlayerB: b, Type: diplomacy.TypeAlliance,
		Status: diplomacy.StatusActive, AllowsGarrison: true, ProposedAt: time.UnixMilli(env.clk.NowMs()).UTC()}}, nil)
	env.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 50, Version: 1}},
	}})
	dueno, _ := conectar(t, env, env.playerID)
	vecino, _ := conectar(t, env, otro)

	dueno.send(protocol.TypeTradePropose, map[string]any{"playerId": otro.String(),
		"give": []map[string]any{{"resource": "STONE", "amount": 50}},
		"want": []map[string]any{{"resource": "WOOD", "amount": 120}}})
	var propia envelope
	env.avanzandoMientras(func() {
		propia, _ = dueno.awaitWhere(protocol.TypeTradeUpdate, func(raw json.RawMessage) bool {
			return decode[tradeUpdate](t, raw).Offer.Status == "PROPOSED"
		}, 5*time.Second)
	})
	id := decode[tradeUpdate](t, propia.Payload).Offer.ID

	dueno.send(protocol.TypeTradeCancel, map[string]any{"offerId": id})
	env.avanzandoMientras(func() {
		_, _ = vecino.awaitWhere(protocol.TypeTradeUpdate, func(raw json.RawMessage) bool {
			v := decode[tradeUpdate](t, raw).Offer
			return v.ID == id && v.Status == "WITHDRAWN"
		}, 5*time.Second)
	})
	_, sigue := env.state.TradeOffer(id)
	assert.False(t, sigue)
}
