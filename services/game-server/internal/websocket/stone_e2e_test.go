package websocket_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// E2E de la piedra y del avance de era (SPEC-ECON-STONE §9 «contract / e2e»).
// La columna x = 52 es colina: el tile (52,40) está a diez pasos del depósito
// de la ciudad de pruebas, (42,40). Cada mensaje se valida contra el JSON
// Schema en conn.read.

func newE2EColina(t *testing.T) *e2e {
	t.Helper()
	env := newE2EConTerreno(t, func(terrain []byte) {
		for y := 0; y < 128; y++ {
			terrain[y*128+52] = byte(world.Hill)
		}
	})
	env.state.SetEras([]city.EraDefinition{
		{Code: city.EraStone, PopulationCap: 20}, {Code: city.EraBronze, PopulationCap: 50},
		{Code: city.EraIron, PopulationCap: 100}, {Code: city.EraCastle, PopulationCap: 150},
	})
	return env
}

// unit.gather sobre una colina → entity.update { carrying, carryingResource:
// STONE } + hill.update → city.update { stocks.STONE }.
func TestRecogerPiedraPorLaRed(t *testing.T) {
	env := newE2EColina(t)
	dueno, snap := conectar(t, env, env.playerID)
	require.Len(t, snap.Cities, 1)
	assert.Equal(t, int64(0), snap.Cities[0].Stocks.Stone)

	unitID := env.unitIDs[0]
	dueno.send(protocol.TypeUnitGather, map[string]any{"unitId": unitID, "target": map[string]any{"x": 52, "y": 40}})
	env.pump()
	env.tick(66 + 100) // 6 480 ms de ida y el trabajo
	// La carga descuenta la colina y lo anuncia antes de que el aldeano salga
	// con ella (deposit.go, takeDeposit).
	colina, _ := dueno.awaitWhere(protocol.TypeHillUpdate, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, int32(90), decode[protocol.HillView](t, colina.Payload).Remaining)
	carga, _ := dueno.awaitWhere(protocol.TypeEntityUpdate, func(raw json.RawMessage) bool {
		p := decode[protocol.EntityUpdatePayload](t, raw)
		return p.ID == unitID && p.Carrying != nil && *p.Carrying > 0
	}, 3*time.Second)
	assert.Equal(t, "STONE", decode[protocol.EntityUpdatePayload](t, carga.Payload).CarryingResource)

	env.tick(60) // la vuelta
	stock, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		p := decode[protocol.CityUpdatePayload](t, raw)
		return p.Stocks != nil && p.Stocks.Stone > 0
	}, 3*time.Second)
	assert.Equal(t, int64(10), decode[protocol.CityUpdatePayload](t, stock.Payload).Stocks.Stone)
}

// city.advance → city.update { advance } → (90 s) → city.update { era,
// populationLimit, advance: null }; sin piedra, INSUFFICIENT_RESOURCES; un
// payload con campos de más, INVALID_MESSAGE.
func TestAvanzarDeEraPorLaRed(t *testing.T) {
	env := newE2EColina(t)
	dueno, _ := conectar(t, env, env.playerID)

	req := dueno.send(protocol.TypeCityAdvance, map[string]any{"cityId": 1})
	env.pump()
	errEnv, _ := dueno.awaitWhere(protocol.TypeSystemError, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInsufficientResources, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	req = dueno.send(protocol.TypeCityAdvance, map[string]any{"cityId": 1, "era": "BRONZE_AGE"})
	env.pump()
	errEnv, _ = dueno.awaitWhere(protocol.TypeSystemError, func(raw json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInvalidMessage, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code,
		"el cliente no elige la era")

	env.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 400}},
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 200}},
	}})
	dueno.send(protocol.TypeCityAdvance, map[string]any{"cityId": 1})
	env.pump()
	env.tick(1)
	inicio, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		_, ok := decode[map[string]any](t, raw)["advance"]
		return ok
	}, 3*time.Second)
	p := decode[protocol.CityUpdatePayload](t, inicio.Payload)
	require.NotNil(t, p.Advance)
	require.NotNil(t, *p.Advance)
	assert.Equal(t, "BRONZE_AGE", (*p.Advance).Era)
	require.NotNil(t, p.Stocks)
	assert.Equal(t, int64(0), p.Stocks.Wood)
	assert.Equal(t, int64(0), p.Stocks.Stone)

	env.tick(900)
	fin, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		_, ok := decode[map[string]any](t, raw)["era"]
		return ok
	}, 3*time.Second)
	crudo := decode[map[string]any](t, fin.Payload)
	assert.Equal(t, "BRONZE_AGE", crudo["era"])
	assert.Equal(t, float64(50), crudo["populationLimit"])
	avance, ok := crudo["advance"]
	assert.True(t, ok, "el fin manda advance")
	assert.Nil(t, avance, "a null")
}
