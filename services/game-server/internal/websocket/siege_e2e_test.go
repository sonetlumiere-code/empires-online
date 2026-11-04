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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El asedio de extremo a extremo (SPEC-COMBAT-SIEGE §9, «e2e»): servidor
// WebSocket, hub, loop y simulación reales, sin base de datos. Cada mensaje
// recibido pasa por la validación contra el JSON Schema.

// asedioUpdate es city.update con los campos que mira este test.
type asedioUpdate struct {
	ID              int64  `json:"id"`
	PopulationLimit *int32 `json:"populationLimit"`
	BesiegedAtMs    *int64 `json:"besiegedAtMs"`
}

// unit.demolish sobre una casa propia → INVALID_TARGET; sobre la ajena →
// unit.movement.started, la vida en building.update para el chunk, el aviso
// besiegedAtMs sólo para el dueño y, al caer, building.destroyed y el límite
// rebajado. El snapshot trae la vida de cada casa.
func TestDerribarPorLaRedQuitaLaCasaYAvisaAlDueno(t *testing.T) {
	env := newE2E(t)
	sitiador := uuid.New()
	propia := int64(2)
	env.state.AddCity(&city.City{
		ID: propia, OwnerPlayerID: sitiador, Name: "Sitiópolis",
		CenterX: 80, CenterY: 40, Era: city.EraStone,
		Population: 1, PopulationLimit: 25, PresenceState: city.PresenceOnline,
	})
	victima, ok := env.state.City(1)
	require.True(t, ok)
	victima.PopulationLimit = 25
	env.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{
		{ID: 900, CityID: 1, Type: building.TypeHouse, X: 40, Y: 41, Status: building.StatusComplete, CompletesAtMs: 1},
		{ID: 901, CityID: propia, Type: building.TypeHouse, X: 81, Y: 41, Status: building.StatusComplete, CompletesAtMs: 1},
	}})
	env.state.AddUnit(&unit.Unit{ID: 700, PlayerID: sitiador, CityID: &propia, Type: unit.TypeWarrior,
		X: 40, Y: 46, HP: 60, MaxHP: 60, Status: unit.StatusIdle})
	dueno, snap := conectar(t, env, env.playerID)
	atacante, _ := conectar(t, env, sitiador)

	var casa *protocol.BuildingView
	for i := range snap.Buildings {
		if snap.Buildings[i].ID == 900 {
			casa = &snap.Buildings[i]
		}
	}
	require.NotNil(t, casa, "el snapshot trae la casa")
	assert.Equal(t, int32(150), casa.HP)
	assert.Equal(t, int32(150), casa.MaxHP)

	req := atacante.send(protocol.TypeUnitDemolish, map[string]any{"unitId": 700, "buildingId": 901})
	env.pump()
	errEnv := atacante.await(protocol.TypeSystemError, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInvalidTarget, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	atacante.send(protocol.TypeUnitDemolish, map[string]any{"unitId": 700, "buildingId": 900})
	env.pump()
	_, _ = atacante.awaitWhere(protocol.TypeUnitMovementStarted, func(raw json.RawMessage) bool {
		return decode[protocol.UnitMovementStartedPayload](t, raw).UnitID == 700
	}, 3*time.Second)

	env.tick(40)
	_, _ = atacante.awaitWhere(protocol.TypeBuildingUpdate, func(raw json.RawMessage) bool {
		p := decode[protocol.BuildingUpdatePayload](t, raw)
		return p.ID == 900 && p.HP != nil && *p.HP < 150
	}, 3*time.Second)
	aviso, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		return decode[asedioUpdate](t, raw).BesiegedAtMs != nil
	}, 3*time.Second)
	assert.Equal(t, int64(1), decode[asedioUpdate](t, aviso.Payload).ID)

	env.tick(300)
	caida, vistos := atacante.awaitWhere(protocol.TypeBuildingDestroyed, func(raw json.RawMessage) bool {
		return decode[protocol.BuildingDestroyedPayload](t, raw).ID == 900
	}, 3*time.Second)
	assert.Equal(t, int64(1), decode[protocol.BuildingDestroyedPayload](t, caida.Payload).CityID)
	for _, v := range vistos {
		if v.Type == protocol.TypeCityUpdate {
			require.Nil(t, decode[asedioUpdate](t, v.Payload).BesiegedAtMs, "el aviso no llega al atacante")
		}
	}
	_, _ = dueno.awaitWhere(protocol.TypeBuildingDestroyed, func(raw json.RawMessage) bool {
		return decode[protocol.BuildingDestroyedPayload](t, raw).ID == 900
	}, 3*time.Second)
	limite, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		p := decode[asedioUpdate](t, raw)
		return p.ID == 1 && p.PopulationLimit != nil
	}, 3*time.Second)
	assert.EqualValues(t, 20, *decode[asedioUpdate](t, limite.Payload).PopulationLimit)

	_, sigue := env.state.Building(900)
	assert.False(t, sigue)
	assert.EqualValues(t, 20, victima.PopulationLimit)
}
