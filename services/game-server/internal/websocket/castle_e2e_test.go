package websocket_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La Edad de los Castillos de extremo a extremo (SPEC-CASTLE-AGE §9 «contract /
// e2e»): servidor WebSocket, hub, loop y simulación reales, sin base de datos.
// La ciudad de pruebas está en (40,40), con su zona urbana de (39,39) a (41,41).
// Cada mensaje recibido pasa por la validación contra el JSON Schema.

func errorCode(t *testing.T, env *e2e, c *conn, tipo string, payload map[string]any) string {
	t.Helper()
	req := c.send(tipo, payload)
	env.pump()
	errEnv, _ := c.awaitWhere(protocol.TypeSystemError, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	return decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code
}

// city.build CASTLE en Hierro → INVALID_TARGET; city.advance desde Hierro →
// city.update { advance: CASTLE_AGE } con los 120 bienes ajenos debitados →
// (360 s) → city.update { era: CASTLE_AGE, populationLimit: 150 }; otra vez,
// NOT_IMPLEMENTED; y en Castillos, city.build CASTLE → building.spawn del
// castillo con su vida.
func TestLaEdadDeLosCastillosPorLaRed(t *testing.T) {
	env := newE2E(t)
	env.state.SetEras([]city.EraDefinition{
		{Code: city.EraStone, PopulationCap: 20}, {Code: city.EraBronze, PopulationCap: 50},
		{Code: city.EraIron, PopulationCap: 100}, {Code: city.EraCastle, PopulationCap: 150},
	})
	env.state.SetCivilizations([]player.Civilization{
		{ID: 1, Code: "ROMAN", Good: economy.ResourceWine, Unit: unit.TypeLegionary},
		{ID: 2, Code: "BYZANTINE", Good: economy.ResourceSilk, Unit: unit.TypeCataphract},
	})
	c, ok := env.state.City(1)
	require.True(t, ok)
	c.Era = city.EraIron
	c.PopulationLimit = 100
	env.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 2_100}},
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 1_600}},
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceWine, Amount: 300}},
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceSilk, Amount: 120}},
	}})
	dueno, _ := conectar(t, env, env.playerID)

	castillo := map[string]any{"cityId": 1, "type": "CASTLE", "x": 39, "y": 39}
	assert.Equal(t, protocol.CodeInvalidTarget, errorCode(t, env, dueno, protocol.TypeCityBuild, castillo), "en Hierro")

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
	assert.Equal(t, "CASTLE_AGE", (*p.Advance).Era)
	require.NotNil(t, p.Stocks)
	assert.Equal(t, int64(500), p.Stocks.Wood)
	assert.Equal(t, int64(800), p.Stocks.Stone)
	assert.Equal(t, int64(0), p.Stocks.Silk)
	assert.Equal(t, int64(300), p.Stocks.Wine, "el vino propio no se toca")

	env.tick(3_600)
	fin, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		_, ok := decode[map[string]any](t, raw)["era"]
		return ok
	}, 3*time.Second)
	crudo := decode[map[string]any](t, fin.Payload)
	assert.Equal(t, "CASTLE_AGE", crudo["era"])
	assert.Equal(t, float64(150), crudo["populationLimit"])

	assert.Equal(t, protocol.CodeNotImplemented,
		errorCode(t, env, dueno, protocol.TypeCityAdvance, map[string]any{"cityId": 1}), "no hay más eras")

	dueno.send(protocol.TypeCityBuild, castillo)
	env.pump()
	alta, _ := dueno.awaitWhere(protocol.TypeBuildingSpawn, func(json.RawMessage) bool { return true }, 3*time.Second)
	b := decode[protocol.BuildingSpawnPayload](t, alta.Payload).Building
	assert.Equal(t, "CASTLE", b.Type)
	assert.Equal(t, int32(1_000), b.MaxHP)
	assert.Nil(t, b.TargetUnitID)
}

// Un castillo completo dispara a quien asedia su ciudad: building.update con el
// objetivo para el chunk, la vida del sitiador en entity.update, el aviso de la
// muerte a los dos lados con los tipos del castillo, y building.update con el
// objetivo a null cuando no queda nadie.
func TestElCastilloDisparaPorLaRed(t *testing.T) {
	env := newE2E(t)
	sitiador := uuid.New()
	propia := int64(2)
	env.state.AddCity(&city.City{
		Civilization: "BYZANTINE",
		ID:           propia, OwnerPlayerID: sitiador, Name: "Sitiópolis",
		CenterX: 80, CenterY: 40, Era: city.EraStone,
		Population: 1, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	})
	victima, ok := env.state.City(1)
	require.True(t, ok)
	victima.Era = city.EraCastle
	victima.PopulationLimit = 25
	env.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{
		{ID: 900, CityID: 1, Type: building.TypeHouse, X: 40, Y: 41, Status: building.StatusComplete, CompletesAtMs: 1},
		{ID: 950, CityID: 1, Type: building.TypeCastle, X: 39, Y: 39, Status: building.StatusComplete, CompletesAtMs: 1},
	}})
	env.state.AddUnit(&unit.Unit{ID: 700, PlayerID: sitiador, CityID: &propia, Type: unit.TypeWarrior,
		X: 40, Y: 46, HP: 60, MaxHP: 60, Status: unit.StatusIdle})
	dueno, snap := conectar(t, env, env.playerID)
	atacante, _ := conectar(t, env, sitiador)

	var vista *protocol.BuildingView
	for i := range snap.Buildings {
		if snap.Buildings[i].ID == 950 {
			vista = &snap.Buildings[i]
		}
	}
	require.NotNil(t, vista, "el snapshot trae el castillo")
	assert.Equal(t, "CASTLE", vista.Type)
	assert.Nil(t, vista.TargetUnitID, "sin nadie a quien disparar")

	atacante.send(protocol.TypeUnitDemolish, map[string]any{"unitId": 700, "buildingId": 900})
	env.pump()
	env.tick(40)

	apunta := func(raw json.RawMessage) bool {
		p := decode[protocol.BuildingUpdatePayload](t, raw)
		return p.ID == 950 && p.TargetUnitID != nil && *p.TargetUnitID != nil && **p.TargetUnitID == 700
	}
	_, _ = atacante.awaitWhere(protocol.TypeBuildingUpdate, apunta, 3*time.Second)
	golpe, _ := atacante.awaitWhere(protocol.TypeEntityUpdate, func(raw json.RawMessage) bool {
		p := decode[protocol.EntityUpdatePayload](t, raw)
		return p.ID == 700 && p.HP != nil
	}, 3*time.Second)
	assert.EqualValues(t, 50, *decode[protocol.EntityUpdatePayload](t, golpe.Payload).HP)

	env.tick(120)
	perdida, _ := atacante.awaitWhere(protocol.TypeEventLogNotice, func(json.RawMessage) bool { return true }, 3*time.Second)
	aviso := decode[protocol.EventLogNoticePayload](t, perdida.Payload)
	assert.Equal(t, "UNIT_LOST_TO_CASTLE", aviso.Kind)
	assert.Equal(t, "WARRIOR", aviso.UnitType)
	assert.Equal(t, env.playerID.String(), aviso.OtherPlayerID)

	_, _ = dueno.awaitWhere(protocol.TypeBuildingUpdate, apunta, 3*time.Second)
	muerte, _ := dueno.awaitWhere(protocol.TypeEventLogNotice, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, "CASTLE_KILL", decode[protocol.EventLogNoticePayload](t, muerte.Payload).Kind)
	_, _ = dueno.awaitWhere(protocol.TypeBuildingUpdate, func(raw json.RawMessage) bool {
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		v, ok := m["targetUnitId"]
		return m["id"] == float64(950) && ok && v == nil
	}, 3*time.Second)
	_, vivo := env.state.Unit(700)
	assert.False(t, vivo)
}
