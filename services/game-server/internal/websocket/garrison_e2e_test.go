package websocket_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La guarnición de extremo a extremo (ADR-017, INV-GARR-006): servidor
// WebSocket, hub, loop y simulación reales, y dos jugadores mirando el mismo
// chunk. Como el de Safe Zones, verifica a qué conexión llega cada mensaje; la
// base de datos no participa.

type unitUpdate struct {
	ID             int64   `json:"id"`
	Status         *string `json:"status"`
	X              *int32  `json:"x"`
	Y              *int32  `json:"y"`
	GarrisonCityID *int64  `json:"garrisonCityId"`
}

func TestLaGuarnicionPorLaRedSoloLaVeSuDueno(t *testing.T) {
	env := newE2E(t)
	observador := uuid.New()
	dueno, _ := conectar(t, env, env.playerID)
	tercero, snapTercero := conectar(t, env, observador)

	unitID := env.unitIDs[0] // en (42,40), junto a la muralla de la ciudad 1 (39..41)
	_, visible := contieneUnidad(snapTercero, unitID)
	require.True(t, visible)

	// ── Entrar ──
	entrar := uuid.NewString()
	dueno.sendID(entrar, protocol.TypeUnitGarrison, map[string]any{"unitId": unitID, "cityId": 1})
	env.pump()

	respuesta, _ := dueno.awaitWhere(protocol.TypeEntityUpdate, func(raw json.RawMessage) bool {
		return decode[unitUpdate](t, raw).ID == unitID
	}, 3*time.Second)
	assert.Equal(t, entrar, respuesta.RequestID, "la respuesta va correlacionada con la orden")
	u := decode[unitUpdate](t, respuesta.Payload)
	require.NotNil(t, u.Status)
	assert.Equal(t, string(unit.StatusGarrisoned), *u.Status)
	require.NotNil(t, u.GarrisonCityID)
	assert.EqualValues(t, 1, *u.GarrisonCityID)

	despawn, _ := tercero.awaitWhere(protocol.TypeEntityDespawn, func(raw json.RawMessage) bool {
		return decode[protocol.EntityDespawnPayload](t, raw).ID == unitID
	}, 3*time.Second)
	assert.Equal(t, protocol.DespawnGarrisoned, decode[protocol.EntityDespawnPayload](t, despawn.Payload).Reason)

	// Un duplicado tras un corte recibe la respuesta original, sin una segunda
	// entrada (I-5).
	dueno.sendID(entrar, protocol.TypeUnitGarrison, map[string]any{"unitId": unitID, "cityId": 1})
	env.pump()
	repetida, _ := dueno.awaitWhere(protocol.TypeEntityUpdate, func(raw json.RawMessage) bool {
		return decode[unitUpdate](t, raw).ID == unitID
	}, 3*time.Second)
	assert.Equal(t, entrar, repetida.RequestID)

	// Al reconectar, el tercero no la recibe en su snapshot y el dueño sí.
	_, otraVez := conectar(t, env, observador)
	_, visible = contieneUnidad(otraVez, unitID)
	assert.False(t, visible, "un snapshot es un delta completo: tampoco ahí")
	_, propio := conectar(t, env, env.playerID)
	vista, visible := contieneUnidad(propio, unitID)
	require.True(t, visible, "su dueño la encuentra")
	require.NotNil(t, vista.GarrisonCityID)
	assert.EqualValues(t, 1, *vista.GarrisonCityID)

	// ── Salir ──
	salir := dueno.send(protocol.TypeUnitUngarrison, map[string]any{"unitId": unitID})
	env.pump()
	fuera, _ := dueno.awaitWhere(protocol.TypeEntityUpdate, func(raw json.RawMessage) bool {
		p := decode[unitUpdate](t, raw)
		return p.ID == unitID && p.Status != nil && *p.Status == string(unit.StatusIdle)
	}, 3*time.Second)
	assert.Equal(t, salir, fuera.RequestID)
	var crudo map[string]any
	require.NoError(t, json.Unmarshal(fuera.Payload, &crudo))
	require.Contains(t, crudo, "garrisonCityId")
	assert.Nil(t, crudo["garrisonCityId"], "sale de la ciudad: null explícito")

	spawn, _ := tercero.awaitWhere(protocol.TypeEntitySpawn, func(raw json.RawMessage) bool {
		return decode[protocol.EntitySpawnPayload](t, raw).Unit.ID == unitID
	}, 3*time.Second)
	assert.Equal(t, string(unit.StatusIdle), decode[protocol.EntitySpawnPayload](t, spawn.Payload).Unit.Status)
}

func TestGuarnecerEnUnaCiudadAjenaSinTratadoLlegaComoTreatyRequired(t *testing.T) {
	env := newE2E(t)
	ajeno := uuid.New()
	// Una unidad del otro jugador, pegada a la muralla de la ciudad 1.
	env.state.AddUnit(&unit.Unit{ID: 100, PlayerID: ajeno, Type: unit.TypeVillager,
		X: 42, Y: 41, HP: 40, MaxHP: 40, Status: unit.StatusIdle})
	visitante, _ := conectar(t, env, ajeno)

	req := visitante.send(protocol.TypeUnitGarrison, map[string]any{"unitId": 100, "cityId": 1})
	env.pump()
	errEnv := visitante.await(protocol.TypeSystemError, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeTreatyRequired, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)
	u, _ := env.state.Unit(100)
	assert.Equal(t, unit.StatusIdle, u.Status)

	// La unidad de otro jugador: el ownership va antes que el tratado.
	req = visitante.send(protocol.TypeUnitGarrison, map[string]any{"unitId": env.unitIDs[0], "cityId": 1})
	env.pump()
	errEnv = visitante.await(protocol.TypeSystemError, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeUnitNotOwned, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)
}
