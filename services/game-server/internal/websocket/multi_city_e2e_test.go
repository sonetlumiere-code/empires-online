package websocket_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La segunda ciudad de extremo a extremo (SPEC-MULTI-CITY §9, «e2e»): el
// servidor real con el almacén en memoria. unit.found_city pasa el esquema y
// llega a la simulación; al confirmarse, el fundador recibe un snapshot con sus
// dos ciudades, y cada mensaje recibido pasa por la validación del JSON Schema.

// FoundCity arma la ciudad como la transacción real: sin civilización ni bando,
// que los pone la RAM.
func (m *memoryCityFall) FoundCity(_ context.Context, w simulation.CityFoundWrite) (*cityfall.FoundingResult, error) {
	site, err := w.Founding.Place(nil)
	if err != nil {
		return nil, err
	}
	now := w.Founding.Now
	c := &city.City{
		ID: 70, OwnerPlayerID: w.Founding.OwnerID, Name: w.Founding.Name, CenterX: site.Center.X,
		CenterY: site.Center.Y, Era: w.Founding.Era, Population: 3, PopulationLimit: 40,
		PresenceState: city.PresenceOnline, LastOnlineAt: &now, FoundedAtMs: now.UnixMilli(), Slot: 2,
	}
	res := &cityfall.FoundingResult{City: c}
	for i, sp := range site.Spawns {
		id := c.ID
		res.Villagers = append(res.Villagers, &unit.Unit{ID: int64(850 + i), PlayerID: w.Founding.OwnerID, CityID: &id,
			Type: unit.TypeVillager, X: sp.X, Y: sp.Y, HP: 40, MaxHP: 40, Status: unit.StatusIdle})
	}
	return res, nil
}

// sitioDeLaFundacion: la comprobación real la prueban los tests de founding.
func sitioDeLaFundacion(_ context.Context, center world.Tile, _ []world.Tile) (cityfall.Site, error) {
	return cityfall.Site{Center: center,
		Spawns: []world.Tile{{X: center.X + 2, Y: center.Y}, {X: center.X - 2, Y: center.Y}}}, nil
}

func TestUnaSegundaCiudadSeFundaPorLaRed(t *testing.T) {
	bosque := func(terrain []byte) {
		for y := 98; y <= 102; y++ {
			for x := 104; x <= 107; x++ {
				terrain[y*256+x] = byte(world.Forest)
			}
		}
	}
	env := newE2EDeLado(t, 256, bosque, nil)
	capital, ok := env.state.City(1)
	require.True(t, ok)
	capital.Era = city.EraBronze
	env.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: 800, Version: 1}},
		{CityID: 1, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 400, Version: 1}},
	}})
	const aldeano int64 = 610
	env.state.AddUnit(&unit.Unit{ID: aldeano, PlayerID: env.playerID, CityID: &[]int64{1}[0], Type: unit.TypeVillager,
		X: 100, Y: 100, HP: 40, MaxHP: 40, Status: unit.StatusIdle})

	c, _ := conectar(t, env, env.playerID)

	// Un payload con campos de más no pasa el esquema.
	c.send(protocol.TypeUnitFoundCity, map[string]any{"unitId": aldeano, "x": 3})
	env.pump()
	malo, _ := c.awaitWhere(protocol.TypeSystemError, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, protocol.CodeInvalidMessage, decode[protocol.SystemErrorPayload](t, malo.Payload).Code)

	c.send(protocol.TypeUnitFoundCity, map[string]any{"unitId": aldeano})
	env.pump()
	go env.pump()
	snap, _ := c.awaitWhere(protocol.TypeWorldSnapshot, func(raw json.RawMessage) bool {
		for _, v := range decode[protocol.WorldSnapshotPayload](t, raw).Cities {
			if v.ID == 70 {
				return true
			}
		}
		return false
	}, 5*time.Second)
	require.NotNil(t, snap)
	propias := 0
	for _, v := range decode[protocol.WorldSnapshotPayload](t, snap.Payload).Cities {
		if v.OwnerPlayerID == env.playerID.String() {
			propias++
		}
	}
	assert.Equal(t, 2, propias, "el snapshot del fundador trae sus dos ciudades")
	assert.Len(t, env.state.CitiesOf(env.playerID), 2)
}
