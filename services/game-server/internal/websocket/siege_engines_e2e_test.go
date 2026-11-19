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

// Arietes y murallas de extremo a extremo (SPEC-SIEGE-ENGINES §9, «e2e»):
// servidor WebSocket, hub, loop y simulación reales, sin base. Cada mensaje
// recibido pasa por la validación contra el JSON Schema, con WALL y RAM.

// Con la muralla en pie, el guerrero no derriba la casa (INVALID_TARGET); el
// ariete golpea la muralla desde el anillo, los dos jugadores ven su vida en
// building.update y su caída en building.destroyed, y el atacante recibe el
// aviso del derribo con buildingType WALL. El snapshot trae la muralla y el
// ariete con sus tipos.
func TestElArieteAbreLaMurallaPorLaRed(t *testing.T) {
	env := newE2E(t)
	sitiador := uuid.New()
	propia := int64(2)
	env.state.AddCity(&city.City{
		Civilization: "BYZANTINE",
		ID:           propia, OwnerPlayerID: sitiador, Name: "Sitiópolis",
		CenterX: 80, CenterY: 40, Era: city.EraIron,
		Population: 3, PopulationLimit: 25, PresenceState: city.PresenceOnline,
	})
	env.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{
		{ID: 910, CityID: 1, Type: building.TypeWall, X: 40, Y: 40, Status: building.StatusComplete, CompletesAtMs: 1},
		{ID: 911, CityID: 1, Type: building.TypeHouse, X: 40, Y: 41, Status: building.StatusComplete, CompletesAtMs: 1},
	}})
	env.state.AddUnit(&unit.Unit{ID: 710, PlayerID: sitiador, CityID: &propia, Type: unit.TypeWarrior,
		X: 41, Y: 38, HP: 60, MaxHP: 60, Status: unit.StatusIdle})
	env.state.AddUnit(&unit.Unit{ID: 711, PlayerID: sitiador, CityID: &propia, Type: unit.TypeRam,
		X: 40, Y: 38, HP: 250, MaxHP: 250, Status: unit.StatusIdle})
	dueno, snap := conectar(t, env, env.playerID)
	atacante, snapAtacante := conectar(t, env, sitiador)

	var muralla *protocol.BuildingView
	for i := range snap.Buildings {
		if snap.Buildings[i].ID == 910 {
			muralla = &snap.Buildings[i]
		}
	}
	require.NotNil(t, muralla, "el snapshot trae la muralla")
	assert.Equal(t, "WALL", muralla.Type)
	assert.Equal(t, int32(1_500), muralla.HP)
	var ariete bool
	for _, u := range snapAtacante.Units {
		ariete = ariete || (u.ID == 711 && u.UnitType == "RAM")
	}
	assert.True(t, ariete, "el snapshot trae el ariete")

	req := atacante.send(protocol.TypeUnitDemolish, map[string]any{"unitId": 710, "buildingId": 911})
	env.pump()
	errEnv := atacante.await(protocol.TypeSystemError, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInvalidTarget, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	atacante.send(protocol.TypeUnitDemolish, map[string]any{"unitId": 711, "buildingId": 910})
	env.pump()
	env.tick(2)
	_, _ = atacante.awaitWhere(protocol.TypeBuildingUpdate, func(raw json.RawMessage) bool {
		p := decode[protocol.BuildingUpdatePayload](t, raw)
		return p.ID == 910 && p.HP != nil && *p.HP == 1_460
	}, 3*time.Second)
	_, _ = dueno.awaitWhere(protocol.TypeBuildingUpdate, func(raw json.RawMessage) bool {
		p := decode[protocol.BuildingUpdatePayload](t, raw)
		return p.ID == 910 && p.HP != nil && *p.HP == 1_460
	}, 3*time.Second)

	env.tick(1_200) // 38 golpes cada 3 s
	_, _ = atacante.awaitWhere(protocol.TypeBuildingDestroyed, func(raw json.RawMessage) bool {
		return decode[protocol.BuildingDestroyedPayload](t, raw).ID == 910
	}, 3*time.Second)
	aviso, _ := atacante.awaitWhere(protocol.TypeEventLogNotice, func(raw json.RawMessage) bool {
		return decode[protocol.EventLogNoticePayload](t, raw).Kind == "HOUSE_DESTROYED"
	}, 3*time.Second)
	assert.Equal(t, "WALL", decode[protocol.EventLogNoticePayload](t, aviso.Payload).BuildingType)
	_, _ = dueno.awaitWhere(protocol.TypeBuildingDestroyed, func(raw json.RawMessage) bool {
		return decode[protocol.BuildingDestroyedPayload](t, raw).ID == 910
	}, 3*time.Second)
	assert.False(t, env.state.Walled(1), "en brecha")
}
