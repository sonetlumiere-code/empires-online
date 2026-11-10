package websocket_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La economía de extremo a extremo (SPEC-ECON-WOOD §9, «e2e»): servidor
// WebSocket, hub, loop y simulación reales, sin base de datos. Cada mensaje
// recibido pasa por la validación contra el JSON Schema de schema_e2e_test.go.

// memoryEconomy hace de postgres.EconomyRepo sin base: acepta todo y da ids.
type memoryEconomy struct {
	mu         sync.Mutex
	edificios  int64
	unidades   int64
	encargos   int
	nacimiento int
}

func (m *memoryEconomy) PersistTask(context.Context, simulation.TaskWrite) error    { return nil }
func (m *memoryEconomy) FlushStocks(context.Context, []simulation.StockDelta) error { return nil }
func (m *memoryEconomy) CompleteBuilding(context.Context, simulation.BuildingCompletion) error {
	return nil
}

func (m *memoryEconomy) DestroyBuilding(context.Context, simulation.BuildingDestruction) error {
	return nil
}

func (m *memoryEconomy) Build(context.Context, simulation.BuildOrder) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edificios++
	m.encargos++
	return m.edificios, nil
}

func (m *memoryEconomy) Train(context.Context, simulation.TrainOrder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.encargos++
	return nil
}

func (m *memoryEconomy) Advance(context.Context, simulation.AdvanceOrder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.encargos++
	return nil
}

func (m *memoryEconomy) CompleteAdvance(context.Context, simulation.AdvanceCompletion) error {
	return nil
}

func (m *memoryEconomy) Craft(context.Context, simulation.CraftOrder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.encargos++
	return nil
}

func (m *memoryEconomy) CompleteCraft(context.Context, simulation.CraftDelivery) error { return nil }

func (m *memoryEconomy) BirthUnit(context.Context, simulation.Birth) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nacimiento++
	m.unidades++
	return 1000 + m.unidades, nil
}

// bosqueE2E es la columna x = 52: el tile (52,40) está a diez pasos del
// depósito de la ciudad de pruebas, (42,40).
func newE2EBosque(t *testing.T) *e2e {
	t.Helper()
	return newE2EConTerreno(t, func(terrain []byte) {
		for y := 0; y < 128; y++ {
			terrain[y*128+52] = byte(world.Forest)
		}
	})
}

func darMaderaE2E(env *e2e, n int64) {
	env.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: n}},
	}})
}

// unit.gather → unit.movement.started → entity.update { WORKING } →
// city.update { stocks }.
func TestRecolectarPorLaRedLlegaAlStockDeLaCiudad(t *testing.T) {
	env := newE2EBosque(t)
	dueno, snap := conectar(t, env, env.playerID)
	require.NotNil(t, snap.Buildings, "el snapshot trae buildings[], aunque esté vacío")
	require.Len(t, snap.Cities, 1)
	assert.Equal(t, int64(0), snap.Cities[0].Stocks.Wood)

	unitID := env.unitIDs[0]
	dueno.send(protocol.TypeUnitGather, map[string]any{"unitId": unitID, "target": map[string]any{"x": 52, "y": 40}})
	env.pump()
	inicio, _ := dueno.awaitWhere(protocol.TypeUnitMovementStarted, func(raw json.RawMessage) bool {
		return decode[protocol.UnitMovementStartedPayload](t, raw).UnitID == unitID
	}, 3*time.Second)
	ida := decode[protocol.UnitMovementStartedPayload](t, inicio.Payload).Movement
	assert.Equal(t, protocol.Tile{X: 52, Y: 40}, ida.Target)

	env.tick(64) // 6 360 ms de ida
	_, _ = dueno.awaitWhere(protocol.TypeEntityUpdate, func(raw json.RawMessage) bool {
		p := decode[unitUpdate](t, raw)
		return p.ID == unitID && p.Status != nil && *p.Status == string(unit.StatusWorking)
	}, 3*time.Second)

	env.tick(100 + 60) // trabajo y vuelta
	actualizacion, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		return decode[protocol.CityUpdatePayload](t, raw).Stocks != nil
	}, 3*time.Second)
	assert.Equal(t, int64(10), decode[protocol.CityUpdatePayload](t, actualizacion.Payload).Stocks.Wood)

	// Un tile que no es bosque se rechaza por system.error.
	req := dueno.send(protocol.TypeUnitGather, map[string]any{"unitId": unitID, "target": map[string]any{"x": 50, "y": 40}})
	env.pump()
	errEnv, _ := dueno.awaitWhere(protocol.TypeSystemError, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInvalidTarget, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)
}

// city.build → building.spawn → building.update { COMPLETE } +
// city.update { populationLimit }; sin madera, INSUFFICIENT_RESOURCES.
func TestConstruirUnaCasaPorLaRed(t *testing.T) {
	env := newE2E(t)
	dueno, _ := conectar(t, env, env.playerID)

	req := dueno.send(protocol.TypeCityBuild, map[string]any{"cityId": 1, "type": "HOUSE", "x": 39, "y": 39})
	env.pump()
	errEnv, _ := dueno.awaitWhere(protocol.TypeSystemError, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInsufficientResources, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	darMaderaE2E(env, 30)
	dueno.send(protocol.TypeCityBuild, map[string]any{"cityId": 1, "type": "HOUSE", "x": 39, "y": 39})
	env.pump()
	spawn, _ := dueno.awaitWhere(protocol.TypeBuildingSpawn, func(json.RawMessage) bool { return true }, 3*time.Second)
	casa := decode[protocol.BuildingSpawnPayload](t, spawn.Payload).Building
	assert.Equal(t, "UNDER_CONSTRUCTION", casa.Status)
	assert.Equal(t, int32(39), casa.X)

	env.tick(151)
	update, _ := dueno.awaitWhere(protocol.TypeBuildingUpdate, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, "COMPLETE", decode[protocol.BuildingUpdatePayload](t, update.Payload).Status)
	limite, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		return decode[protocol.CityUpdatePayload](t, raw).PopulationLimit != nil
	}, 3*time.Second)
	assert.Equal(t, int32(25), *decode[protocol.CityUpdatePayload](t, limite.Payload).PopulationLimit)

	// Un snapshot nuevo trae la casa completa.
	_, snap := conectar(t, env, env.playerID)
	require.Len(t, snap.Buildings, 1)
	assert.Equal(t, "COMPLETE", snap.Buildings[0].Status)
}

// city.train → city.update { production } → entity.spawn; un segundo encargo
// con la producción en curso responde CITY_BUSY.
func TestEntrenarUnAldeanoPorLaRed(t *testing.T) {
	env := newE2E(t)
	darMaderaE2E(env, 100)
	dueno, _ := conectar(t, env, env.playerID)

	dueno.send(protocol.TypeCityTrain, map[string]any{"cityId": 1, "type": "VILLAGER"})
	env.pump()
	encargo, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		return decode[protocol.CityUpdatePayload](t, raw).Production != nil
	}, 3*time.Second)
	assert.Equal(t, int64(50), decode[protocol.CityUpdatePayload](t, encargo.Payload).Stocks.Wood)

	req := dueno.send(protocol.TypeCityTrain, map[string]any{"cityId": 1, "type": "VILLAGER"})
	env.pump()
	errEnv, _ := dueno.awaitWhere(protocol.TypeSystemError, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeCityBusy, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	env.tick(202)
	spawn, _ := dueno.awaitWhere(protocol.TypeEntitySpawn, func(raw json.RawMessage) bool {
		return decode[protocol.EntitySpawnPayload](t, raw).Unit.ID == 1001
	}, 3*time.Second)
	nacido := decode[protocol.EntitySpawnPayload](t, spawn.Payload).Unit
	assert.Equal(t, string(unit.StatusIdle), nacido.Status)
	assert.Equal(t, int32(42), nacido.X, "el primer tile del anillo, como los iniciales")
}

// SPEC-CIVILIZATION-GOODS §6: city.craft por la red. El encargo llega como
// city.update con su craft; uno segundo, como CITY_BUSY; y al vencer, otro
// city.update con el vino en el almacén y craft en null.
func TestElaborarVinoPorLaRed(t *testing.T) {
	env := newE2E(t)
	env.state.SetCivilizations([]player.Civilization{{ID: 1, Code: "ROMAN", Good: economy.ResourceWine}})
	c, ok := env.state.City(1)
	require.True(t, ok)
	c.Era = city.EraBronze
	env.state.SetEconomy(simulation.EconomyLoad{
		Stocks: []simulation.StockRow{
			{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 100}},
			{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 40}},
		},
		Buildings: []building.Building{{ID: 77, CityID: 1, Type: building.TypeWorkshop, X: 41, Y: 41, Status: building.StatusComplete}},
	})
	dueno, _ := conectar(t, env, env.playerID)

	dueno.send(protocol.TypeCityCraft, map[string]any{"cityId": 1})
	env.pump()
	encargo, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		p := decode[protocol.CityUpdatePayload](t, raw)
		return p.Craft != nil && *p.Craft != nil
	}, 3*time.Second)
	p := decode[protocol.CityUpdatePayload](t, encargo.Payload)
	assert.Equal(t, "WINE", (*p.Craft).Good)
	assert.Equal(t, int32(5), (*p.Craft).Amount)
	assert.Equal(t, int64(70), p.Stocks.Wood)
	assert.Equal(t, int64(25), p.Stocks.Stone)

	req := dueno.send(protocol.TypeCityCraft, map[string]any{"cityId": 1})
	env.pump()
	errEnv, _ := dueno.awaitWhere(protocol.TypeSystemError, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeCityBusy, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	env.tick(300) // 30 s sin rasgo
	// «craft: null» no se distingue de «sin craft» al decodificar en **CraftView:
	// se mira el JSON.
	fin, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		v, ok := m["craft"]
		return ok && v == nil
	}, 3*time.Second)
	assert.Equal(t, int64(5), decode[protocol.CityUpdatePayload](t, fin.Payload).Stocks.Wine)
}
