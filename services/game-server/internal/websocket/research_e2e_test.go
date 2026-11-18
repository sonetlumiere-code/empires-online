package websocket_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/technology"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// llevaClave dice si el payload JSON lleva la clave, aunque sea null.
func llevaClave(t *testing.T, raw json.RawMessage, clave string) bool {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	_, ok := m[clave]
	return ok
}

// SPEC-TECHNOLOGIES §9 «e2e» y RN-TECH-008: city.research por la red. El dueño
// ve en el snapshot su investigación (null) y sus tecnologías ([]); el vecino ve
// la ciudad sin esas claves. Al encargar, el dueño recibe la investigación y el
// vecino sólo el stock; al vencer, el dueño recibe la investigación a null y la
// tecnología, y el vecino nada de eso. Una tecnología de otra era vuelve como
// INVALID_TARGET con su requestId.
func TestInvestigarPorLaRed(t *testing.T) {
	env := newE2E(t)
	cat, err := technology.NewCatalog([]technology.Technology{{
		Code: "STONE_TOOLS", Name: "Herramientas de piedra", Era: city.EraStone,
		Costs:      economy.Costs{{Resource: economy.ResourceWood, Amount: 150}, {Resource: economy.ResourceStone, Amount: 50}},
		DurationMs: 60_000, Effects: map[string]int32{player.TraitGatherSpeed: 1100},
	}, {
		Code: "MASONRY", Name: "Albañilería", Era: city.EraBronze,
		Costs:      economy.Costs{{Resource: economy.ResourceWood, Amount: 200}},
		DurationMs: 120_000, Effects: map[string]int32{player.TraitHouseHP: 1200},
	}}, nil)
	require.NoError(t, err)
	env.state.SetTechnologies(cat, nil)
	env.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 500}},
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 500}},
	}})
	vecino := uuid.New()
	env.state.AddCity(&city.City{ID: 2, OwnerPlayerID: vecino, Name: "Vecinópolis", CenterX: 46, CenterY: 40, Civilization: "NORSE",
		Era: city.EraStone, Population: 1, PopulationLimit: 20, PresenceState: city.PresenceOnline})

	dueno, snap := conectar(t, env, env.playerID)
	propia := ciudadDe(t, snap, 1)
	// «research: null» se decodifica igual que su ausencia: lo distingue
	// technologies, que en la vista del dueño viaja siempre (el JSON con su null
	// lo fija el contract test de la vista).
	assert.Nil(t, propia.Research, "sin investigación")
	require.NotNil(t, propia.Technologies, "la vista del dueño lleva sus tecnologías")
	assert.Empty(t, *propia.Technologies)

	otro, snapVecino := conectar(t, env, vecino)
	ajena := ciudadDe(t, snapVecino, 1)
	assert.Nil(t, ajena.Research, "la ciudad ajena no lleva la investigación")
	assert.Nil(t, ajena.Technologies)

	req := dueno.send(protocol.TypeCityResearch, map[string]any{"cityId": 1, "technology": "MASONRY"})
	env.pump()
	errEnv, _ := dueno.awaitWhere(protocol.TypeSystemError, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInvalidTarget, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	dueno.send(protocol.TypeCityResearch, map[string]any{"cityId": 1, "technology": "STONE_TOOLS"})
	env.pump()
	encargo, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		p := decode[protocol.CityUpdatePayload](t, raw)
		return p.Research != nil && *p.Research != nil
	}, 3*time.Second)
	r := *decode[protocol.CityUpdatePayload](t, encargo.Payload).Research
	assert.Equal(t, "STONE_TOOLS", r.Technology)
	assert.Equal(t, testEpoch+60_000, r.CompletesAtMs)
	stockVecino, _ := otro.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		return decode[protocol.CityUpdatePayload](t, raw).Stocks != nil
	}, 3*time.Second)
	assert.Equal(t, int64(350), decode[protocol.CityUpdatePayload](t, stockVecino.Payload).Stocks.Wood, "el stock es público")

	env.tick(601)
	fin, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		return llevaClave(t, raw, "technologies")
	}, 3*time.Second)
	p := decode[protocol.CityUpdatePayload](t, fin.Payload)
	assert.Equal(t, []string{"STONE_TOOLS"}, *p.Technologies)
	assert.True(t, llevaClave(t, fin.Payload, "research"), "research: null viaja")
	assert.Nil(t, p.Research, "y es null")

	otro.send(protocol.TypeSessionPing, map[string]any{"clientTimeMs": testEpoch})
	_, antes := otro.awaitWhere(protocol.TypeSessionPong, func(json.RawMessage) bool { return true }, 3*time.Second)
	for _, m := range append(antes, stockVecino) {
		if m.Type == protocol.TypeCityUpdate {
			assert.False(t, llevaClave(t, m.Payload, "research"), "el vecino no ve la investigación")
			assert.False(t, llevaClave(t, m.Payload, "technologies"), "ni las tecnologías")
		}
	}
}

func ciudadDe(t *testing.T, snap protocol.WorldSnapshotPayload, id int64) protocol.CityView {
	t.Helper()
	for _, c := range snap.Cities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("el snapshot no trae la ciudad %d", id)
	return protocol.CityView{}
}
