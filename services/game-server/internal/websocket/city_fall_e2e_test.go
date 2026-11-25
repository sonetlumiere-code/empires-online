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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La caída de una ciudad de extremo a extremo (SPEC-CITY-FALL §9, «e2e»): el
// servidor real con un almacén en memoria. Cada mensaje recibido pasa por la
// validación contra el JSON Schema: CityView.assault, city.update con el asalto,
// city.fallen, los tres avisos y el snapshot nuevo del vencido.

type memoryCityFall struct {
	mu     sync.Mutex
	nextID int64
}

func (m *memoryCityFall) StartAssault(context.Context, simulation.AssaultStart) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	return m.nextID, nil
}

func (m *memoryCityFall) EndAssault(context.Context, simulation.AssaultEnd) error { return nil }

func (m *memoryCityFall) FallCity(_ context.Context, f cityfall.Fall) (*cityfall.FallResult, error) {
	site, err := f.Place(nil)
	if err != nil {
		return nil, err
	}
	now := f.Now
	// Como la transacción real, la ciudad nueva vuelve sin civilización ni bando:
	// los pone la RAM, que los conoce del jugador (precisión de 2026-11-23).
	c := &city.City{
		ID: 50, OwnerPlayerID: f.OwnerID, Name: f.Name, CenterX: site.Center.X,
		CenterY: site.Center.Y, Era: f.Era, PopulationLimit: 40, PresenceState: city.PresenceOnline,
		LastOnlineAt: &now, FoundedAtMs: now.UnixMilli(),
	}
	res := &cityfall.FallResult{City: c}
	for i, sp := range site.Spawns {
		id := c.ID
		res.Villagers = append(res.Villagers, &unit.Unit{ID: int64(800 + i), PlayerID: f.OwnerID, CityID: &id,
			Type: unit.TypeVillager, X: sp.X, Y: sp.Y, HP: 40, MaxHP: 40, Status: unit.StatusIdle})
	}
	// SPEC-CITY-TAKEN: con hueco, el vencedor la toma (la 51, en el mismo centro).
	if len(f.TakeSpawns) > 0 {
		t := &city.City{
			ID: 51, OwnerPlayerID: f.VictorID, Name: f.Name, CenterX: f.Center.X, CenterY: f.Center.Y, Era: f.Era,
			Population: 3, PopulationLimit: 40, PresenceState: city.PresenceOnline, LastOnlineAt: &now,
			FoundedAtMs: now.UnixMilli(), Slot: 2,
		}
		res.Taken = t
		for i, sp := range f.TakeSpawns {
			id := t.ID
			res.TakenVillagers = append(res.TakenVillagers, &unit.Unit{ID: int64(850 + i), PlayerID: f.VictorID, CityID: &id,
				Type: unit.TypeVillager, X: sp.X, Y: sp.Y, HP: 40, MaxHP: 40, Status: unit.StatusIdle})
		}
	}
	return res, nil
}

// sitioFijoDeRefundacion: la búsqueda real la prueban los tests de founding.
func sitioFijoDeRefundacion(_ context.Context, _, _ []world.Tile, _ int32, _ uint64) (cityfall.Site, error) {
	return cityfall.Site{Center: world.Tile{X: 200, Y: 200},
		Spawns: []world.Tile{{X: 202, Y: 200}, {X: 198, Y: 200}, {X: 200, Y: 202}}}, nil
}

// Un guerrero propio asalta la ciudad ajena en brecha: el vencido recibe el
// aviso del asalto, ve la ciudad con su asalto en city.update y, al caer,
// city.fallen, CITY_LOST y un snapshot nuevo con su ciudad refundada; el
// vencedor recibe city.fallen con la ciudad que tomó y CITY_TAKEN (RN-CITY-026,
// ADR-050 §5, SPEC-CITY-TAKEN §6).
func TestUnaCiudadCaePorLaRed(t *testing.T) {
	env := newE2EDeLado(t, 256, nil, nil)
	ajeno := uuid.New()
	env.state.AddCity(&city.City{Civilization: "NORSE", ID: 2, OwnerPlayerID: ajeno, Name: "Ajenópolis",
		CenterX: 60, CenterY: 40, Era: city.EraBronze, Population: 0, PopulationLimit: 20,
		PresenceState: city.PresenceOnline})
	const guerrero int64 = 600
	env.state.AddUnit(&unit.Unit{ID: guerrero, PlayerID: env.playerID, CityID: &[]int64{1}[0], Type: unit.TypeWarrior,
		X: 58, Y: 40, HP: 60, MaxHP: 60, Status: unit.StatusIdle})

	vencedor, _ := conectar(t, env, env.playerID)
	vencido, _ := conectar(t, env, ajeno)

	vencedor.send(protocol.TypeUnitAssault, map[string]any{"unitId": guerrero, "cityId": 2})
	env.pump()

	esAviso := func(kind string) func(json.RawMessage) bool {
		return func(raw json.RawMessage) bool { return decode[protocol.EventLogNoticePayload](t, raw).Kind == kind }
	}
	asalto, _ := vencido.awaitWhere(protocol.TypeEventLogNotice, esAviso("CITY_ASSAULTED"), 3*time.Second)
	p := decode[protocol.EventLogNoticePayload](t, asalto.Payload)
	assert.Equal(t, env.playerID.String(), p.OtherPlayerID)
	require.NotNil(t, p.FallsAtMs)
	abierto, _ := vencedor.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
		u := decode[protocol.CityUpdatePayload](t, raw)
		return u.ID == 2 && u.Assault != nil && *u.Assault != nil
	}, 3*time.Second)
	assert.Equal(t, guerrero, (*decode[protocol.CityUpdatePayload](t, abierto.Payload).Assault).UnitID)

	env.tick(25)
	caida, _ := vencedor.awaitWhere(protocol.TypeCityFallen, func(json.RawMessage) bool { return true }, 3*time.Second)
	assert.Equal(t, int64(2), decode[protocol.CityFallenPayload](t, caida.Payload).CityID)
	// SPEC-CITY-TAKEN §6: el vencedor, con una sola ciudad, la toma; el campo viaja crudo.
	assert.Contains(t, string(caida.Payload), `"takenCityId":51`)
	_, _ = vencedor.awaitWhere(protocol.TypeEventLogNotice, esAviso("CITY_TAKEN"), 3*time.Second)

	_, _ = vencido.awaitWhere(protocol.TypeCityFallen, func(json.RawMessage) bool { return true }, 3*time.Second)
	_, _ = vencido.awaitWhere(protocol.TypeEventLogNotice, esAviso("CITY_LOST"), 3*time.Second)
	// El snapshot nuevo pasa por el canal de comandos: el loop tiene que girar
	// mientras el test espera (nadie más lo conduce en este rato).
	go env.pump()
	snap, _ := vencido.awaitWhere(protocol.TypeWorldSnapshot, func(raw json.RawMessage) bool {
		for _, c := range decode[protocol.WorldSnapshotPayload](t, raw).Cities {
			if c.ID == 50 {
				return true
			}
		}
		return false
	}, 5*time.Second)
	require.NotNil(t, snap)
	nueva, ok := env.state.CityOf(ajeno)
	require.True(t, ok)
	assert.Equal(t, int64(50), nueva.ID)
	_, sigue := env.state.City(2)
	assert.False(t, sigue)
	tomada, ok := env.state.City(51)
	require.True(t, ok, "la tomada está en el mundo")
	assert.Equal(t, env.playerID, tomada.OwnerPlayerID)
}
