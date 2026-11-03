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

// El combate de extremo a extremo (SPEC-COMBAT-RAID §9, «e2e»): servidor
// WebSocket, hub, loop y simulación reales, sin base de datos. Cada mensaje
// recibido pasa por la validación contra el JSON Schema.

// unit.attack → unit.movement.started → entity.update { ATTACKING } →
// entity.update { hp } … → entity.despawn { DEAD }; un objetivo propio llega
// como INVALID_TARGET.
func TestAtacarPorLaRedMataAlObjetivo(t *testing.T) {
	env := newE2E(t)
	ajeno := uuid.New()
	cityID := int64(1)
	env.state.AddUnit(&unit.Unit{ID: 500, PlayerID: env.playerID, CityID: &cityID, Type: unit.TypeWarrior,
		X: 60, Y: 40, HP: 60, MaxHP: 60, Status: unit.StatusIdle})
	env.state.AddUnit(&unit.Unit{ID: 600, PlayerID: ajeno, Type: unit.TypeVillager,
		X: 62, Y: 40, HP: 40, MaxHP: 40, Status: unit.StatusIdle})
	dueno, _ := conectar(t, env, env.playerID)

	req := dueno.send(protocol.TypeUnitAttack, map[string]any{"unitId": 500, "targetUnitId": env.unitIDs[0]})
	env.pump()
	errEnv := dueno.await(protocol.TypeSystemError, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInvalidTarget, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	dueno.send(protocol.TypeUnitAttack, map[string]any{"unitId": 500, "targetUnitId": 600})
	env.pump()
	_, _ = dueno.awaitWhere(protocol.TypeUnitMovementStarted, func(raw json.RawMessage) bool {
		return decode[protocol.UnitMovementStartedPayload](t, raw).UnitID == 500
	}, 3*time.Second)

	env.tick(10)
	_, _ = dueno.awaitWhere(protocol.TypeEntityUpdate, func(raw json.RawMessage) bool {
		p := decode[unitUpdate](t, raw)
		return p.ID == 500 && p.Status != nil && *p.Status == string(unit.StatusAttacking)
	}, 3*time.Second)

	env.tick(70)
	muerte, _ := dueno.awaitWhere(protocol.TypeEntityDespawn, func(raw json.RawMessage) bool {
		return decode[protocol.EntityDespawnPayload](t, raw).ID == 600
	}, 3*time.Second)
	assert.Equal(t, protocol.DespawnDead, decode[protocol.EntityDespawnPayload](t, muerte.Payload).Reason)
	_, vive := env.state.Unit(600)
	require.False(t, vive)
}
