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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La conquista de extremo a extremo (SPEC-TERRITORY-CONQUEST §9, «e2e»): el
// servidor real con un almacén en memoria. Cada mensaje recibido pasa por la
// validación contra el JSON Schema, también la reclamación de TerritoryView,
// CLAIMING y los avisos de territorio.

type memoryConquest struct {
	mu     sync.Mutex
	nextID int64
}

func (m *memoryConquest) StartClaim(context.Context, simulation.ClaimStart) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	return m.nextID, nil
}

func (m *memoryConquest) EndClaim(context.Context, simulation.ClaimEnd) error { return nil }

func (m *memoryConquest) CaptureTerritory(_ context.Context, c simulation.Capture) (territory.Control, error) {
	owner, at := c.NewOwner.String(), c.CapturedAt
	return territory.Control{TerritoryID: c.TerritoryID, OwnerType: territory.OwnerPlayer, OwnerID: &owner,
		CapturedAt: &at, Version: c.ExpectedVersion + 1}, nil
}

// rejillaDe256 divide el mundo de 256 × 256 en 16 territorios de 64 × 64, con
// ids por filas, y da los de duenos a sus jugadores.
func rejillaDe256(t *testing.T, env *e2e, duenos map[int64]uuid.UUID) {
	t.Helper()
	var ts []territory.Territory
	var controls []territory.Control
	for fila := int32(0); fila < 4; fila++ {
		for col := int32(0); col < 4; col++ {
			id := int64(fila*4 + col + 1)
			ts = append(ts, territory.Territory{ID: id, Name: "Comarca", MinX: col * 64, MinY: fila * 64,
				MaxX: col*64 + 63, MaxY: fila*64 + 63})
			c := territory.Control{TerritoryID: id, OwnerType: territory.OwnerNone}
			if p, ok := duenos[id]; ok {
				owner, at := p.String(), time.Unix(1_800_000_000, 0).UTC()
				c.OwnerType, c.OwnerID, c.CapturedAt, c.Version = territory.OwnerPlayer, &owner, &at, 1
			}
			controls = append(controls, c)
		}
	}
	set, _, err := territory.BuildSet(ts, 256, 256, 32)
	require.NoError(t, err)
	env.state.SetTerritories(set, controls)
}

// Un jugador ajeno reclama el territorio 16 (192-255, 192-255), conquistado por
// el dueño, mientras éste mira su ciudad en el 1. El dueño no recibe el
// territory.update —no mira ese chunk— pero sí TERRITORY_CLAIMED y, al vencer
// el plazo, TERRITORY_LOST y su límite rebajado; el conquistador recibe
// TERRITORY_TAKEN (RN-CONQ-010, INV-SEC-009).
func TestUnaConquistaAvisaAlDuenoConLaVistaEnSuCiudad(t *testing.T) {
	env := newE2EDeLado(t, 256, nil, nil)
	ajeno := uuid.New()
	rejillaDe256(t, env, map[int64]uuid.UUID{1: env.playerID, 16: env.playerID})
	env.state.AddCity(&city.City{Civilization: "NORSE", ID: 2, OwnerPlayerID: ajeno, Name: "Ajenópolis", CenterX: 150, CenterY: 40,
		Era: city.EraStone, Population: 1, PopulationLimit: 20, PresenceState: city.PresenceOnline})
	propia, ok := env.state.City(1)
	require.True(t, ok)
	propia.PopulationLimit = 25 // la era y el territorio 16
	const guerrero int64 = 600
	env.state.AddUnit(&unit.Unit{ID: guerrero, PlayerID: ajeno, CityID: &[]int64{2}[0], Type: unit.TypeWarrior,
		X: 200, Y: 200, HP: 60, MaxHP: 60, Status: unit.StatusIdle})

	dueno, _ := conectar(t, env, env.playerID)
	recentrar(t, dueno, env, 40, 40)
	conquistador, _ := conectar(t, env, ajeno)
	recentrar(t, conquistador, env, 200, 200)

	conquistador.send(protocol.TypeUnitClaim, map[string]any{"unitId": guerrero})
	env.pump()

	esAviso := func(kind string) func(json.RawMessage) bool {
		return func(raw json.RawMessage) bool { return decode[protocol.EventLogNoticePayload](t, raw).Kind == kind }
	}
	reclamado, antes := dueno.awaitWhere(protocol.TypeEventLogNotice, esAviso("TERRITORY_CLAIMED"), 3*time.Second)
	p := decode[protocol.EventLogNoticePayload](t, reclamado.Payload)
	assert.Equal(t, ajeno.String(), p.OtherPlayerID)
	require.NotNil(t, p.TerritoryID)
	assert.Equal(t, int64(16), *p.TerritoryID)
	require.NotNil(t, p.OtherCityName)
	assert.Equal(t, "Ajenópolis", *p.OtherCityName)
	for _, m := range antes {
		require.NotEqual(t, protocol.TypeTerritoryUpdate, m.Type, "el dueño no mira el territorio 16")
	}
	disputa, _ := conquistador.awaitWhere(protocol.TypeTerritoryUpdate, func(raw json.RawMessage) bool {
		v := decode[protocol.TerritoryUpdatePayload](t, raw).Territory
		return v.ID == 16 && v.Contested && v.Claim != nil
	}, 3*time.Second)
	assert.Equal(t, ajeno.String(), decode[protocol.TerritoryUpdatePayload](t, disputa.Payload).Territory.Claim.PlayerID)

	env.tick(25)
	perdido, previos := dueno.awaitWhere(protocol.TypeEventLogNotice, esAviso("TERRITORY_LOST"), 3*time.Second)
	assert.Equal(t, ajeno.String(), decode[protocol.EventLogNoticePayload](t, perdido.Payload).OtherPlayerID)
	limite := false
	for _, m := range previos {
		if m.Type == protocol.TypeCityUpdate {
			if l := decode[protocol.CityUpdatePayload](t, m.Payload).PopulationLimit; l != nil && *l == 20 {
				limite = true
			}
		}
	}
	if !limite {
		_, _ = dueno.awaitWhere(protocol.TypeCityUpdate, func(raw json.RawMessage) bool {
			l := decode[protocol.CityUpdatePayload](t, raw).PopulationLimit
			return l != nil && *l == 20
		}, 3*time.Second)
	}
	_, _ = conquistador.awaitWhere(protocol.TypeEventLogNotice, esAviso("TERRITORY_TAKEN"), 3*time.Second)
	c, _ := env.state.TerritoryControl(16)
	require.NotNil(t, c.OwnerID)
	assert.Equal(t, ajeno.String(), *c.OwnerID)
}
