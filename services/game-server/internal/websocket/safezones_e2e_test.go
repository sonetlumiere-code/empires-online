package websocket_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// INV-SAFE-004 de extremo a extremo, con el servidor WebSocket, el hub, el game
// loop y la simulación REALES y dos jugadores conectados mirando el mismo chunk.
//
// Este test vive en el nivel e2e de transporte y no en el de integración contra
// PostgreSQL a propósito: lo que verifica es a qué conexión llega cada mensaje,
// y en eso la base de datos no participa. Así corre en el job `game-server` de
// la CI, con el detector de carreras activo.

// awaitWhere espera un mensaje del tipo dado que además cumpla `match`,
// descartando los demás. Devuelve todo lo recibido por el camino, para que el
// llamante pueda afirmar qué NO llegó.
func (c *conn) awaitWhere(msgType string, match func(json.RawMessage) bool, timeout time.Duration) (envelope, []envelope) {
	c.t.Helper()
	var seen []envelope
	require.NoError(c.t, c.c.SetReadDeadline(time.Now().Add(timeout)))
	for {
		_, data, err := c.c.ReadMessage()
		if err != nil {
			c.t.Fatalf("esperando %q: %v (recibidos: %d)", msgType, err, len(seen))
		}
		var env envelope
		require.NoError(c.t, json.Unmarshal(data, &env))
		if env.Type == msgType && match(env.Payload) {
			return env, seen
		}
		seen = append(seen, env)
	}
}

func mencionaUnidad(t *testing.T, env envelope, unitID int64) bool {
	t.Helper()
	var p map[string]any
	if json.Unmarshal(env.Payload, &p) != nil {
		return false
	}
	for _, k := range []string{"id", "unitId"} {
		if v, ok := p[k].(float64); ok && int64(v) == unitID {
			return true
		}
	}
	if u, ok := p["unit"].(map[string]any); ok {
		if v, ok := u["id"].(float64); ok && int64(v) == unitID {
			return true
		}
	}
	return false
}

func conectar(t *testing.T, env *e2e, playerID uuid.UUID) (*conn, protocol.WorldSnapshotPayload) {
	t.Helper()
	ticket, err := env.issuer.Issue(playerID)
	require.NoError(t, err)
	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
	env.pump()
	snap := decode[protocol.WorldSnapshotPayload](t, c.await(protocol.TypeWorldSnapshot, 3*time.Second).Payload)
	return c, snap
}

func contieneUnidad(snap protocol.WorldSnapshotPayload, unitID int64) (protocol.UnitView, bool) {
	for _, u := range snap.Units {
		if u.ID == unitID {
			return u, true
		}
	}
	return protocol.UnitView{}, false
}

func TestUnaUnidadOcultaEsInvisibleParaTercerosYVisibleParaSuDueno(t *testing.T) {
	// Franja de bosque de x=50 a x=52 en todo el alto del mundo: una zona
	// DENSE_FOREST dentro del mismo chunk (1,1) que la ciudad de pruebas.
	env := newE2EConTerreno(t, func(terrain []byte) {
		for y := 0; y < 128; y++ {
			for x := 50; x <= 52; x++ {
				terrain[y*128+x] = byte(world.Forest)
			}
		}
	})
	idx, _, err := safezone.BuildIndex([]safezone.Zone{
		{ID: 1, Type: safezone.DenseForest, MinX: 50, MinY: 0, MaxX: 52, MaxY: 127},
	}, env.state.World())
	require.NoError(t, err)
	env.state.SetSafeZones(idx)

	// El observador es un segundo jugador sin ciudad: su área de interés se
	// centra en el centro del mundo, que en 128 × 128 con radio 2 lo cubre todo.
	observador := uuid.New()
	env.state.AddUnit(&unit.Unit{
		ID: 100, PlayerID: observador, Type: unit.TypeVillager,
		X: 60, Y: 40, HP: 40, MaxHP: 40, Status: unit.StatusIdle,
	})

	dueno, _ := conectar(t, env, env.playerID)
	tercero, snapTercero := conectar(t, env, observador)

	unitID := env.unitIDs[0] // en (42,40)
	_, visible := contieneUnidad(snapTercero, unitID)
	require.True(t, visible, "antes de ocultarse, el tercero la ve")

	// ── El dueño la lleva al bosque y se detiene allí ──
	dueno.send(protocol.TypeUnitMove, map[string]any{
		"unitId": unitID, "target": map[string]int{"x": 51, "y": 40},
	})
	env.pump()
	env.tick(100) // de sobra para 9 tiles

	esHidden := func(raw json.RawMessage) bool {
		p := decode[protocol.EntityUpdatePayload](t, raw)
		return p.ID == unitID && p.Status != nil && *p.Status == string(unit.StatusHidden)
	}
	dueno.awaitWhere(protocol.TypeEntityUpdate, esHidden, 3*time.Second)

	despawn, previos := tercero.awaitWhere(protocol.TypeEntityDespawn, func(raw json.RawMessage) bool {
		return decode[protocol.EntityDespawnPayload](t, raw).ID == unitID
	}, 3*time.Second)
	require.Equal(t, protocol.DespawnHidden, decode[protocol.EntityDespawnPayload](t, despawn.Payload).Reason)
	for _, m := range previos {
		if m.Type == protocol.TypeEntityUpdate {
			p := decode[protocol.EntityUpdatePayload](t, m.Payload)
			require.False(t, p.ID == unitID && p.Status != nil, "el tercero no debe recibir el cambio de estado")
		}
	}

	// ── Un snapshot posterior del tercero tampoco la trae ──
	env.tick(5)
	tercero.send(protocol.TypeSessionView, map[string]any{"center": map[string]int{"x": 64, "y": 64}})
	env.pump()
	snap, intermedios := tercero.awaitWhere(protocol.TypeWorldSnapshot, func(json.RawMessage) bool { return true }, 3*time.Second)
	for _, m := range intermedios {
		require.False(t, mencionaUnidad(t, m, unitID), "ningún mensaje posterior al despawn menciona la unidad: %s", m.Type)
	}
	_, visible = contieneUnidad(decode[protocol.WorldSnapshotPayload](t, snap.Payload), unitID)
	require.False(t, visible, "una unidad oculta no puede volver por el snapshot")

	// ── El dueño, en cambio, la sigue viendo con su estado real ──
	dueno.send(protocol.TypeSessionView, map[string]any{"center": map[string]int{"x": 51, "y": 40}})
	env.pump()
	snapDueno, intermediosDueno := dueno.awaitWhere(protocol.TypeWorldSnapshot, func(json.RawMessage) bool { return true }, 3*time.Second)
	for _, m := range intermediosDueno {
		require.False(t, m.Type == protocol.TypeEntityDespawn && mencionaUnidad(t, m, unitID),
			"el dueño nunca recibe el despawn de su propia unidad")
	}
	propia, ok := contieneUnidad(decode[protocol.WorldSnapshotPayload](t, snapDueno.Payload), unitID)
	require.True(t, ok)
	require.Equal(t, string(unit.StatusHidden), propia.Status)

	// ── Moverse la revela: el tercero la recibe ya en movimiento ──
	dueno.send(protocol.TypeUnitMove, map[string]any{
		"unitId": unitID, "target": map[string]int{"x": 56, "y": 40},
	})
	env.pump()
	spawn, _ := tercero.awaitWhere(protocol.TypeEntitySpawn, func(raw json.RawMessage) bool {
		return decode[protocol.EntitySpawnPayload](t, raw).Unit.ID == unitID
	}, 3*time.Second)
	vista := decode[protocol.EntitySpawnPayload](t, spawn.Payload).Unit
	require.Equal(t, string(unit.StatusMoving), vista.Status)
	require.NotNil(t, vista.Movement)
	tercero.awaitWhere(protocol.TypeUnitMovementStarted, func(raw json.RawMessage) bool {
		return decode[protocol.UnitMovementStartedPayload](t, raw).UnitID == unitID
	}, 3*time.Second)
}
