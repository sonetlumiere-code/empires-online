package websocket_test

import (
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

type caravanUpdate struct {
	Caravan protocol.CaravanView `json:"caravan"`
}

// avanzandoElReloj es avanzandoMientras con el reloj en marcha: cada paso del
// loop adelanta 100 ms de simulación, así que las caravanas llegan.
func (e *e2e) avanzandoElReloj(espera func()) {
	listo := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-listo:
				return
			default:
				e.clk.AdvanceMs(100)
				e.loop.Step(e.clk.NowMs())
				time.Sleep(time.Millisecond)
			}
		}
	}()
	defer func() {
		close(listo)
		wg.Wait()
	}()
	espera()
}

// SPEC-CARAVANS de punta a punta por la red: un trato aceptado manda a los dos
// jugadores un caravan.update por cada caravana, con su carga; las caravanas se
// ven en el mapa como unidades CARAVAN; al llegar, caravan.update DELIVERED y el
// almacén del destino sube.
func TestUnaCaravanaDePuntaAPunta(t *testing.T) {
	env := newE2E(t)
	otro := uuid.New()
	env.state.AddCity(&city.City{
		Civilization: "PERSIAN",
		ID:           2, OwnerPlayerID: otro, Name: "Vecinópolis",
		CenterX: 60, CenterY: 40, Era: city.EraStone,
		Population: 1, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	})
	a, b := diplomacy.CanonicalPair(env.playerID, otro)
	env.state.SetTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: a, PlayerB: b, Type: diplomacy.TypeTrade,
		Status: diplomacy.StatusActive, ProposedAt: time.UnixMilli(env.clk.NowMs()).UTC()}}, nil)
	env.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 300, Version: 1}},
		{CityID: 2, Stock: economy.Stock{Resource: economy.ResourceSilk, Amount: 30, Version: 1}},
	}})
	dueno, _ := conectar(t, env, env.playerID)
	vecino, _ := conectar(t, env, otro)
	linea := func(r string, n int64) []map[string]any { return []map[string]any{{"resource": r, "amount": n}} }

	dueno.send(protocol.TypeTradePropose, map[string]any{
		"playerId": otro.String(), "give": linea("WOOD", 300), "want": linea("SILK", 30)})
	var recibida envelope
	env.avanzandoMientras(func() {
		recibida, _ = vecino.awaitWhere(protocol.TypeTradeUpdate, func(raw json.RawMessage) bool {
			return decode[tradeUpdate](t, raw).Offer.Status == "PROPOSED"
		}, 5*time.Second)
	})
	vecino.send(protocol.TypeTradeRespond, map[string]any{"offerId": decode[tradeUpdate](t, recibida.Payload).Offer.ID, "accept": true})

	// Cada uno recibe las dos caravanas con su carga (RN-CARAVAN-009); la de seda
	// viene hacia la ciudad del dueño.
	var seda protocol.CaravanView
	var antes []envelope
	env.avanzandoMientras(func() {
		var m envelope
		m, antes = dueno.awaitWhere(protocol.TypeCaravanUpdate, func(raw json.RawMessage) bool {
			c := decode[caravanUpdate](t, raw).Caravan
			return c.Status == "IN_TRANSIT" && c.SenderPlayerID == otro.String()
		}, 5*time.Second)
		seda = decode[caravanUpdate](t, m.Payload).Caravan
		_, _ = vecino.awaitWhere(protocol.TypeCaravanUpdate, func(raw json.RawMessage) bool {
			return decode[caravanUpdate](t, raw).Caravan.UnitID == seda.UnitID
		}, 5*time.Second)
	})
	assert.Equal(t, int64(1), seda.ToCityID)
	assert.Equal(t, []protocol.ResourceAmount{{Resource: "SILK", Amount: 30}}, seda.Cargo)
	require.NotNil(t, seda.ArrivesAtMs)
	assert.Zero(t, env.state.StockOf(1, economy.ResourceSilk).Amount, "la seda viene en camino")
	assert.Zero(t, env.state.StockOf(1, economy.ResourceWood).Amount, "la madera ya salió")

	// En el mapa, una unidad CARAVAN: el dueño la ve nacer en la ciudad vecina,
	// justo antes de su caravan.update.
	var nace *envelope
	for i := range antes {
		var p protocol.EntitySpawnPayload
		if antes[i].Type == protocol.TypeEntitySpawn && json.Unmarshal(antes[i].Payload, &p) == nil && p.Unit.ID == seda.UnitID {
			nace = &antes[i]
		}
	}
	require.NotNil(t, nace, "el entity.spawn de la caravana de seda")
	var spawn protocol.EntitySpawnPayload
	require.NoError(t, json.Unmarshal(nace.Payload, &spawn))
	assert.Equal(t, "CARAVAN", spawn.Unit.UnitType)
	assert.NotContains(t, string(nace.Payload), "cargo", "la carga no viaja con la unidad")

	// La llegada.
	env.avanzandoElReloj(func() {
		_, _ = dueno.awaitWhere(protocol.TypeCaravanUpdate, func(raw json.RawMessage) bool {
			c := decode[caravanUpdate](t, raw).Caravan
			return c.UnitID == seda.UnitID && c.Status == "DELIVERED"
		}, 20*time.Second)
	})
	assert.Equal(t, int64(30), env.state.StockOf(1, economy.ResourceSilk).Amount)
}
