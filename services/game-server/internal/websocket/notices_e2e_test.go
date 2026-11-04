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

// Los avisos en vivo de extremo a extremo (SPEC-LIVE-NOTICES §9 «e2e»):
// servidor WebSocket, hub, loop y simulación reales. Cada mensaje recibido pasa
// por la validación contra el JSON Schema, también event_log.notice.

// Un aldeano propio muere en el chunk (6,6) mientras su dueño mira su ciudad, en
// el (1,1): no recibe el despawn, porque no está suscrito a ese chunk, pero sí el
// aviso UNIT_LOST con el tile. El asesino recibe ENEMY_KILLED; un testigo que ve
// la muerte en su área no recibe aviso (RN-NOTICE-002, INV-SEC-009).
func TestUnAvisoLlegaConLaVistaEnOtraParteDelMapa(t *testing.T) {
	env := newE2EDeLado(t, 256, nil, nil) // 8 × 8 chunks: el área de 5 × 5 no lo cubre todo

	const aldeano, guerrero int64 = 4, 600
	cityID := int64(1)
	ajeno := uuid.New()
	env.state.AddUnit(&unit.Unit{ID: aldeano, PlayerID: env.playerID, CityID: &cityID, Type: unit.TypeVillager,
		X: 200, Y: 200, HP: 40, MaxHP: 40, Status: unit.StatusIdle})
	env.state.AddUnit(&unit.Unit{ID: guerrero, PlayerID: ajeno, Type: unit.TypeWarrior,
		X: 201, Y: 200, HP: 60, MaxHP: 60, Status: unit.StatusIdle})

	dueno, _ := conectar(t, env, env.playerID)
	snap := recentrar(t, dueno, env, 40, 40) // chunk (1,1): columnas y filas 0..3
	_, ve := contieneUnidad(snap, aldeano)
	require.False(t, ve, "el dueño mira su ciudad, lejos del aldeano")
	asesino, _ := conectar(t, env, ajeno)
	testigo, _ := conectar(t, env, uuid.New())
	snap = recentrar(t, testigo, env, 200, 200)
	_, ve = contieneUnidad(snap, aldeano)
	require.True(t, ve, "el testigo tiene la muerte en su área")

	asesino.send(protocol.TypeUnitAttack, map[string]any{"unitId": guerrero, "targetUnitId": aldeano})
	env.pump()
	env.tick(100)

	esAviso := func(kind string) func(json.RawMessage) bool {
		return func(raw json.RawMessage) bool {
			return decode[protocol.EventLogNoticePayload](t, raw).Kind == kind
		}
	}
	llegado, antes := dueno.awaitWhere(protocol.TypeEventLogNotice, esAviso("UNIT_LOST"), 3*time.Second)
	assert.Empty(t, llegado.RequestID, "el aviso no responde a ningún comando")
	for _, m := range antes {
		require.False(t, m.Type == protocol.TypeEntityDespawn && mencionaUnidad(t, m, aldeano),
			"el despawn va al chunk, y el dueño no lo mira")
	}
	perdida := decode[protocol.EventLogNoticePayload](t, llegado.Payload)
	assert.Equal(t, ajeno.String(), perdida.OtherPlayerID)
	assert.Nil(t, perdida.OtherCityID, "el asesino no tiene ciudad")
	assert.Nil(t, perdida.OtherCityName)
	assert.Equal(t, string(unit.TypeVillager), perdida.UnitType)
	require.NotNil(t, perdida.X)
	require.NotNil(t, perdida.Y)
	assert.Equal(t, [2]int32{200, 200}, [2]int32{*perdida.X, *perdida.Y})
	_, sigue := env.state.Unit(aldeano)
	require.False(t, sigue, "el aldeano murió")

	baja, _ := asesino.awaitWhere(protocol.TypeEventLogNotice, esAviso("ENEMY_KILLED"), 3*time.Second)
	p := decode[protocol.EventLogNoticePayload](t, baja.Payload)
	assert.Equal(t, env.playerID.String(), p.OtherPlayerID)
	require.NotNil(t, p.OtherCityName)
	assert.Equal(t, "Testópolis", *p.OtherCityName)

	_, previos := testigo.awaitWhere(protocol.TypeEntityDespawn, func(raw json.RawMessage) bool {
		d := decode[protocol.EntityDespawnPayload](t, raw)
		return d.ID == aldeano && d.Reason == protocol.DespawnDead
	}, 3*time.Second)
	testigo.send(protocol.TypeSessionPing, map[string]any{"clientTimeMs": 1})
	_, despues := testigo.awaitWhere(protocol.TypeSessionPong, func(json.RawMessage) bool { return true }, 3*time.Second)
	for _, m := range append(append(testigo.historial(), previos...), despues...) {
		require.NotEqual(t, protocol.TypeEventLogNotice, m.Type, "un tercero no recibe avisos")
	}
}
