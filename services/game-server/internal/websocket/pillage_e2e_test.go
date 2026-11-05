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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El saqueo de extremo a extremo (SPEC-COMBAT-PILLAGE §9, «e2e»): servidor
// WebSocket, hub, loop y simulación reales, sin base de datos. Cada mensaje
// recibido pasa por la validación contra el JSON Schema.

// cityUpdate es city.update con los campos que mira este test.
type cityUpdate struct {
	ID           int64            `json:"id"`
	Stocks       *protocol.Stocks `json:"stocks"`
	PillagedAtMs *int64           `json:"pillagedAtMs"`
}

// unit.pillage sobre la ciudad propia → INVALID_TARGET; sobre la ajena →
// unit.movement.started, entity.update { ATTACKING }, city.update { stocks }
// para el chunk y city.update { pillagedAtMs } sólo para el dueño.
func TestSaquearPorLaRedRobaYAvisaAlDueno(t *testing.T) {
	env := newE2E(t)
	saqueador := uuid.New()
	propia := int64(2)
	env.state.AddCity(&city.City{
		Civilization: "NORSE",
		ID:           propia, OwnerPlayerID: saqueador, Name: "Saqueópolis",
		CenterX: 80, CenterY: 40, Era: city.EraStone,
		Population: 1, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	})
	env.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 200, Version: 1}},
	}})
	env.state.AddUnit(&unit.Unit{ID: 700, PlayerID: saqueador, CityID: &propia, Type: unit.TypeWarrior,
		X: 40, Y: 46, HP: 60, MaxHP: 60, Status: unit.StatusIdle})
	dueno, _ := conectar(t, env, env.playerID)
	ladron, _ := conectar(t, env, saqueador)

	req := ladron.send(protocol.TypeUnitPillage, map[string]any{"unitId": 700, "cityId": propia})
	env.pump()
	errEnv := ladron.await(protocol.TypeSystemError, 3*time.Second)
	assert.Equal(t, req, errEnv.RequestID)
	assert.Equal(t, protocol.CodeInvalidTarget, decode[protocol.SystemErrorPayload](t, errEnv.Payload).Code)

	ladron.send(protocol.TypeUnitPillage, map[string]any{"unitId": 700, "cityId": 1})
	env.pump()
	_, _ = ladron.awaitWhere(protocol.TypeUnitMovementStarted, func(raw json.RawMessage) bool {
		return decode[protocol.UnitMovementStartedPayload](t, raw).UnitID == 700
	}, 3*time.Second)

	env.tick(40)
	_, _ = ladron.awaitWhere(protocol.TypeEntityUpdate, func(raw json.RawMessage) bool {
		p := decode[unitUpdate](t, raw)
		return p.ID == 700 && p.Status != nil && *p.Status == string(unit.StatusAttacking)
	}, 3*time.Second)
	aviso, _ := dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		return decode[cityUpdate](t, raw).PillagedAtMs != nil
	}, 3*time.Second)
	assert.Equal(t, int64(1), decode[cityUpdate](t, aviso.Payload).ID)
	_, _ = ladron.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		p := decode[cityUpdate](t, raw)
		require.Nil(t, p.PillagedAtMs, "el aviso no llega al saqueador")
		return p.ID == 1 && p.Stocks != nil && p.Stocks.Wood < 200
	}, 3*time.Second)

	g, ok := env.state.Unit(700)
	require.True(t, ok)
	assert.Positive(t, g.Loot, "el golpe se confirmó")
	assert.Equal(t, int64(200)-int64(g.Loot), env.state.Stock(1).Amount)
}
