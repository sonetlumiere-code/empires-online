package websocket_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Cruce de frontera de chunk por la red (docs/architecture/networking.md §4.3),
// con el servidor WebSocket, el hub, el game loop y la simulación REALES y tres
// sesiones con áreas de interés distintas, colocadas con session.view:
//
//   - el dueño observa el chunk de origen y no el de destino;
//   - un observador, el de destino y no el de origen;
//   - un testigo, los dos.
//
// Mundo de 256 × 256 (8 × 8 chunks) con radio de interés 2: las áreas son de
// 5 × 5 chunks, y así hay sitio para que dos no se solapen. La frontera que se
// cruza es x = 160, entre los chunks (4,1) y (5,1). Cada mensaje que el servidor
// emite se valida contra el JSON Schema al leerlo (schema_e2e_test.go).

// recentrar envía un session.view y devuelve el snapshot del área nueva.
func recentrar(t *testing.T, c *conn, env *e2e, x, y int) protocol.WorldSnapshotPayload {
	t.Helper()
	id := c.send(protocol.TypeSessionView, map[string]any{"center": map[string]int{"x": x, "y": y}})
	env.pump()
	snap := c.await(protocol.TypeWorldSnapshot, 3*time.Second)
	require.Equal(t, id, snap.RequestID)
	return decode[protocol.WorldSnapshotPayload](t, snap.Payload)
}

// sinSpawnNiDespawnDe exige que ninguno de los mensajes sea un spawn o un
// despawn de la unidad.
func sinSpawnNiDespawnDe(t *testing.T, msgs []envelope, unitID int64) {
	t.Helper()
	for _, m := range msgs {
		if m.Type == protocol.TypeEntitySpawn || m.Type == protocol.TypeEntityDespawn {
			require.False(t, mencionaUnidad(t, m, unitID), "%s inesperado para la unidad %d", m.Type, unitID)
		}
	}
}

// posicionesDe devuelve las x de los entity.update de la unidad, en orden.
func posicionesDe(t *testing.T, msgs []envelope, unitID int64) []int32 {
	t.Helper()
	var xs []int32
	for _, m := range msgs {
		if m.Type != protocol.TypeEntityUpdate {
			continue
		}
		p := decode[protocol.EntityUpdatePayload](t, m.Payload)
		if p.ID == unitID && p.X != nil {
			xs = append(xs, *p.X)
		}
	}
	return xs
}

// nadaMasSobre comprueba que, hasta el siguiente pong, la sesión no recibe
// ningún mensaje que mencione la unidad: es la forma de afirmar «nada más».
func nadaMasSobre(t *testing.T, c *conn, unitID int64) {
	t.Helper()
	c.send(protocol.TypeSessionPing, map[string]any{"clientTimeMs": 1})
	_, entre := c.awaitWhere(protocol.TypeSessionPong, func(json.RawMessage) bool { return true }, 3*time.Second)
	for _, m := range entre {
		require.False(t, mencionaUnidad(t, m, unitID), "tras perderla de vista no llega nada más: %s", m.Type)
	}
}

func TestUnaUnidadQueCruzaDeChunkApareceYDesapareceEnElAreaDeOtraSesion(t *testing.T) {
	env := newE2EDeLado(t, 256, nil, nil)

	// Una unidad propia junto a la frontera, en (158,40): chunk (4,1).
	const unitID int64 = 4
	cityID := int64(1)
	env.state.AddUnit(&unit.Unit{
		ID: unitID, PlayerID: env.playerID, CityID: &cityID, Type: unit.TypeVillager,
		X: 158, Y: 40, HP: 40, MaxHP: 40, Status: unit.StatusIdle,
	})

	dueno, _ := conectar(t, env, env.playerID)
	observador, _ := conectar(t, env, uuid.New())
	testigo, _ := conectar(t, env, uuid.New())

	// Centro (80,40) → chunk (2,1): columnas 0..4. Tiene el (4,1), no el (5,1).
	snapDueno := recentrar(t, dueno, env, 80, 40)
	_, ok := contieneUnidad(snapDueno, unitID)
	require.True(t, ok, "el dueño parte viendo la unidad")
	// Centro (230,40) → chunk (7,1): columnas 5..7. Tiene el (5,1), no el (4,1).
	snapObservador := recentrar(t, observador, env, 230, 40)
	_, ok = contieneUnidad(snapObservador, unitID)
	require.False(t, ok, "el observador parte sin verla")
	// Centro (150,40) → chunk (4,1): columnas 2..6. Tiene los dos.
	snapTestigo := recentrar(t, testigo, env, 150, 40)
	_, ok = contieneUnidad(snapTestigo, unitID)
	require.True(t, ok)

	// ── Ida: de (158,40) a (162,40). Cruza x = 160 en su segundo paso. ──
	dueno.send(protocol.TypeUnitMove, map[string]any{"unitId": unitID, "target": map[string]int{"x": 162, "y": 40}})
	env.pump()
	ida := decode[protocol.UnitMoveAcceptedPayload](t, dueno.await(protocol.TypeUnitMoveAccepted, 3*time.Second).Payload)
	env.tick(30)

	// El observador la gana: entity.spawn con su polilínea, y sólo después los
	// entity.update, empezando por el del tile en que cruzó.
	spawn, antes := observador.awaitWhere(protocol.TypeEntitySpawn, func(raw json.RawMessage) bool {
		return decode[protocol.EntitySpawnPayload](t, raw).Unit.ID == unitID
	}, 3*time.Second)
	require.Empty(t, posicionesDe(t, antes, unitID), "ningún delta antes del spawn")
	vista := decode[protocol.EntitySpawnPayload](t, spawn.Payload).Unit
	require.Equal(t, string(unit.StatusMoving), vista.Status)
	require.Equal(t, [2]int32{160, 40}, [2]int32{vista.X, vista.Y})
	require.NotNil(t, vista.Movement, "el spawn lleva el movimiento activo")
	require.Equal(t, ida.MovementID, vista.Movement.MovementID)
	completado, deltas := observador.awaitWhere(protocol.TypeUnitMovementCompleted, func(raw json.RawMessage) bool {
		return decode[protocol.UnitMovementCompletedPayload](t, raw).UnitID == unitID
	}, 3*time.Second)
	require.Equal(t, []int32{160, 161, 162}, posicionesDe(t, deltas, unitID))
	sinSpawnNiDespawnDe(t, deltas, unitID)
	require.Equal(t, protocol.Tile{X: 162, Y: 40},
		decode[protocol.UnitMovementCompletedPayload](t, completado.Payload).FinalPosition)

	// El dueño la pierde: entity.despawn OUT_OF_INTEREST tras el último delta
	// dentro de su área, y nada más después.
	despawn, previos := dueno.awaitWhere(protocol.TypeEntityDespawn, func(raw json.RawMessage) bool {
		return decode[protocol.EntityDespawnPayload](t, raw).ID == unitID
	}, 3*time.Second)
	require.Equal(t, protocol.DespawnOutOfInterest, decode[protocol.EntityDespawnPayload](t, despawn.Payload).Reason)
	require.Equal(t, []int32{159}, posicionesDe(t, previos, unitID), "sólo el paso que seguía en su área")
	nadaMasSobre(t, dueno, unitID)

	// El testigo lo ve todo como deltas: ni spawn ni despawn.
	_, vistoPorTestigo := testigo.awaitWhere(protocol.TypeUnitMovementCompleted, func(raw json.RawMessage) bool {
		return decode[protocol.UnitMovementCompletedPayload](t, raw).UnitID == unitID
	}, 3*time.Second)
	require.Equal(t, []int32{159, 160, 161, 162}, posicionesDe(t, vistoPorTestigo, unitID))
	sinSpawnNiDespawnDe(t, vistoPorTestigo, unitID)

	// ── Vuelta: de (162,40) a (158,40). La orden es por id, aunque la unidad ya
	// no esté en el área del dueño. ──
	dueno.send(protocol.TypeUnitMove, map[string]any{"unitId": unitID, "target": map[string]int{"x": 158, "y": 40}})
	env.pump()
	vuelta := decode[protocol.UnitMoveAcceptedPayload](t, dueno.await(protocol.TypeUnitMoveAccepted, 3*time.Second).Payload)
	env.tick(30)

	// Ahora es el observador quien la pierde…
	despawn, previos = observador.awaitWhere(protocol.TypeEntityDespawn, func(raw json.RawMessage) bool {
		return decode[protocol.EntityDespawnPayload](t, raw).ID == unitID
	}, 3*time.Second)
	require.Equal(t, protocol.DespawnOutOfInterest, decode[protocol.EntityDespawnPayload](t, despawn.Payload).Reason)
	require.Equal(t, []int32{161, 160}, posicionesDe(t, previos, unitID))
	nadaMasSobre(t, observador, unitID)

	// …y el dueño quien la recupera, ya en marcha y con la polilínea nueva.
	spawn, antes = dueno.awaitWhere(protocol.TypeEntitySpawn, func(raw json.RawMessage) bool {
		return decode[protocol.EntitySpawnPayload](t, raw).Unit.ID == unitID
	}, 3*time.Second)
	require.Empty(t, posicionesDe(t, antes, unitID))
	vista = decode[protocol.EntitySpawnPayload](t, spawn.Payload).Unit
	require.Equal(t, string(unit.StatusMoving), vista.Status)
	require.Equal(t, [2]int32{159, 40}, [2]int32{vista.X, vista.Y})
	require.NotNil(t, vista.Movement)
	require.Equal(t, vuelta.MovementID, vista.Movement.MovementID)
	_, deltas = dueno.awaitWhere(protocol.TypeUnitMovementCompleted, func(raw json.RawMessage) bool {
		return decode[protocol.UnitMovementCompletedPayload](t, raw).UnitID == unitID
	}, 3*time.Second)
	require.Equal(t, []int32{159, 158}, posicionesDe(t, deltas, unitID))
	sinSpawnNiDespawnDe(t, deltas, unitID)

	// Y el testigo sigue sin recibir ni spawn ni despawn.
	_, vistoPorTestigo = testigo.awaitWhere(protocol.TypeUnitMovementCompleted, func(raw json.RawMessage) bool {
		return decode[protocol.UnitMovementCompletedPayload](t, raw).UnitID == unitID
	}, 3*time.Second)
	require.Equal(t, []int32{161, 160, 159, 158}, posicionesDe(t, vistoPorTestigo, unitID))
	sinSpawnNiDespawnDe(t, vistoPorTestigo, unitID)
}
