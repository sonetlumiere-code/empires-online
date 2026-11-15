package websocket_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// memoryFactions hace de base de las facciones: acepta toda escritura.
type memoryFactions struct{}

func (memoryFactions) RequestJoin(context.Context, simulation.FactionJoinOrder) error { return nil }
func (memoryFactions) CompleteJoin(context.Context, simulation.FactionJoinCompletion) error {
	return nil
}

// claveNula dice si un payload JSON lleva la clave con valor null («campo:
// null» no se distingue de «sin campo» al decodificar en **T).
func claveNula(t *testing.T, raw json.RawMessage, clave string) bool {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	v, ok := m[clave]
	return ok && v == nil
}

// SPEC-FACTIONS §9 «e2e»: faction.join por la red. La entrada vuelve al dueño
// como city.update con su requestId; una segunda, NEUTRAL o un bando
// desconocido se rechazan; al vencer el preaviso llega la facción con
// factionJoin: null; y entre dos de la Orden el ataque se rechaza con su motivo.
func TestUnirseALaOrdenPorLaRed(t *testing.T) {
	env := newE2E(t)
	dueno, snap := conectar(t, env, env.playerID)
	require.NotEmpty(t, snap.Cities)
	assert.Equal(t, player.FactionNeutral, snap.Cities[0].Faction, "todos nacen NEUTRAL")
	assert.Nil(t, snap.Cities[0].FactionJoin)

	req := dueno.send(protocol.TypeFactionJoin, map[string]any{"faction": player.FactionOrder})
	env.pump()
	entrada, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		p := decode[protocol.CityUpdatePayload](t, raw)
		return p.FactionJoin != nil && *p.FactionJoin != nil
	}, 3*time.Second)
	assert.Equal(t, req, entrada.RequestID, "el dueño la recibe con su requestId")
	join := *decode[protocol.CityUpdatePayload](t, entrada.Payload).FactionJoin
	assert.Equal(t, player.FactionOrder, join.Faction)

	for _, payload := range []map[string]any{
		{"faction": player.FactionNeutral}, {"faction": "GUILD"}, {"faction": player.FactionOrder, "atMs": 1},
	} {
		r := dueno.send(protocol.TypeFactionJoin, payload)
		env.pump()
		e, _ := dueno.awaitWhere(protocol.TypeSystemError, func(json.RawMessage) bool { return true }, 3*time.Second)
		assert.Equal(t, r, e.RequestID)
		assert.Equal(t, protocol.CodeInvalidMessage, decode[protocol.SystemErrorPayload](t, e.Payload).Code, "%v", payload)
	}
	r := dueno.send(protocol.TypeFactionJoin, map[string]any{"faction": player.FactionChaos})
	env.pump()
	e, _ := dueno.awaitWhere(protocol.TypeSystemError, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, r, e.RequestID)
	assert.Equal(t, protocol.CodeInvalidTarget, decode[protocol.SystemErrorPayload](t, e.Payload).Code, "ya se está uniendo")

	env.tick(21) // 2 s de preaviso
	cuenta, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		return claveNula(t, raw, "factionJoin")
	}, 3*time.Second)
	f := decode[protocol.CityUpdatePayload](t, cuenta.Payload).Faction
	require.NotNil(t, f)
	assert.Equal(t, player.FactionOrder, *f)

	// Otro jugador de la Orden, con un guerrero junto a un aldeano del dueño.
	otro := uuid.New()
	ciudadOtro := int64(2)
	env.state.AddCity(&city.City{ID: ciudadOtro, OwnerPlayerID: otro, Name: "Ordenia", CenterX: 70, CenterY: 70,
		Era: city.EraStone, PopulationLimit: 20, PresenceState: city.PresenceOnline, Civilization: "ROMAN",
		Faction: player.FactionOrder})
	env.state.AddUnit(&unit.Unit{ID: 900, PlayerID: otro, CityID: &ciudadOtro, Type: unit.TypeWarrior,
		X: 43, Y: 40, HP: 60, MaxHP: 60, Status: unit.StatusIdle})
	atacante, _ := conectar(t, env, otro)
	r = atacante.send(protocol.TypeUnitAttack, map[string]any{"unitId": 900, "targetUnitId": env.unitIDs[0]})
	env.pump()
	e, _ = atacante.awaitWhere(protocol.TypeSystemError, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, r, e.RequestID)
	errp := decode[protocol.SystemErrorPayload](t, e.Payload)
	assert.Equal(t, protocol.CodeInvalidTarget, errp.Code)
	assert.Equal(t, "los dos sois de la Orden", errp.Message)
}
