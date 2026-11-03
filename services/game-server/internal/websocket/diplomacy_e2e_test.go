package websocket_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La diplomacia de extremo a extremo (SPEC-DIPLOMACY §9, «e2e»): servidor
// WebSocket, hub, loop y simulación reales, con la base en memoria. Cada
// mensaje recibido pasa por la validación contra el JSON Schema.

// memoryTreaties hace de base de tratados: da ids a las ofertas.
type memoryTreaties struct {
	mu     sync.Mutex
	nextID int64
}

func (m *memoryTreaties) ApplyTreaty(_ context.Context, w simulation.TreatyWrite) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w.Insert {
		m.nextID++
		return m.nextID, nil
	}
	return w.Treaty.ID, nil
}

type treatyUpdate struct {
	Treaty protocol.TreatyView `json:"treaty"`
}

// treaty.propose → treaty.update a los dos (con su vista) → treaty.respond →
// treaty.update ACTIVE a los dos; proponerse a sí mismo es INVALID_TARGET.
func TestUnTratadoDePuntaAPuntaConDosJugadores(t *testing.T) {
	env := newE2E(t)
	otro := uuid.New()
	env.state.AddCity(&city.City{
		ID: 2, OwnerPlayerID: otro, Name: "Vecinópolis",
		CenterX: 80, CenterY: 40, Era: city.EraStone,
		Population: 1, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	})
	dueno, _ := conectar(t, env, env.playerID)
	vecino, _ := conectar(t, env, otro)

	req := dueno.send(protocol.TypeTreatyPropose, map[string]any{"playerId": env.playerID.String(), "type": "ALLIANCE"})
	env.pump()
	errEnv := dueno.await(protocol.TypeSystemError, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInvalidTarget, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	dueno.send(protocol.TypeTreatyPropose, map[string]any{"playerId": otro.String(), "type": "ALLIANCE"})
	env.pump()
	propia, _ := dueno.awaitWhere(protocol.TypeTreatyUpdate, func(raw json.RawMessage) bool {
		return decode[treatyUpdate](t, raw).Treaty.Status == "PROPOSED"
	}, 3*time.Second)
	recibida, _ := vecino.awaitWhere(protocol.TypeTreatyUpdate, func(raw json.RawMessage) bool {
		return decode[treatyUpdate](t, raw).Treaty.Status == "PROPOSED"
	}, 3*time.Second)
	vp, vr := decode[treatyUpdate](t, propia.Payload).Treaty, decode[treatyUpdate](t, recibida.Payload).Treaty
	assert.True(t, vp.ProposedByMe)
	assert.False(t, vr.ProposedByMe)
	assert.Equal(t, otro.String(), vp.OtherPlayerID)
	require.NotNil(t, vr.OtherCityName)
	assert.Equal(t, "Testópolis", *vr.OtherCityName)

	vecino.send(protocol.TypeTreatyRespond, map[string]any{"treatyId": vr.ID, "accept": true})
	env.pump()
	for _, c := range []*conn{dueno, vecino} {
		_, _ = c.awaitWhere(protocol.TypeTreatyUpdate, func(raw json.RawMessage) bool {
			v := decode[treatyUpdate](t, raw).Treaty
			return v.ID == vr.ID && v.Status == "ACTIVE"
		}, 3*time.Second)
	}
	assert.True(t, env.state.AtPeace(env.playerID, otro, env.clk.Now()))
}
