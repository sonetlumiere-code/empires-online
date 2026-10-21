package websocket_test

// Los mensajes del contrato que ningún otro test e2e provocaba. Existen para que
// TestMain pueda exigir que cada tipo de mensaje servidor→cliente salga del
// servidor real y se valide contra el JSON Schema al menos una vez (M3,
// aceptación 10).

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// unit.cancel_move de una unidad en marcha llega a su dueño como
// unit.movement.cancelled, con el último tile alcanzado.
func TestLaCancelacionLlegaComoUnitMovementCancelled(t *testing.T) {
	env := newE2E(t)
	c := env.conectar()

	unitID := env.unitIDs[0]
	c.send(protocol.TypeUnitMove, map[string]any{"unitId": unitID, "target": map[string]int{"x": 60, "y": 40}})
	env.pump()
	c.await(protocol.TypeUnitMoveAccepted, 3*time.Second)
	env.tick(10) // un segundo de marcha: algo más de un tile

	c.send(protocol.TypeUnitCancelMove, map[string]any{"unitId": unitID})
	env.pump()
	got := decode[protocol.UnitMovementCancelledPayload](t, c.await(protocol.TypeUnitMovementCancelled, 3*time.Second).Payload)
	require.Equal(t, unitID, got.UnitID)
	require.Equal(t, "CANCELLED_BY_PLAYER", got.Reason)
	require.Greater(t, got.StoppedAt.X, int32(42), "se detiene donde llegó, no en el origen")
}

// Una ciudad fundada al lado de un jugador conectado le llega como city.update,
// y el territorio que reclama, como territory.update (INV-TERR-009).
func TestUnaFundacionVecinaLlegaComoCityUpdateYTerritoryUpdate(t *testing.T) {
	env := newE2E(t)
	set, solapes, err := territory.BuildSet([]territory.Territory{
		{ID: 9, Name: "Vecindad", MinX: 48, MinY: 48, MaxX: 63, MaxY: 63},
	}, 128, 128, 32)
	require.NoError(t, err)
	require.Empty(t, solapes)
	env.state.SetTerritories(set, []territory.Control{{TerritoryID: 9, OwnerType: territory.OwnerNone}})

	c := env.conectar()

	dueño := uuid.New()
	idDueño := dueño.String()
	capturado := time.UnixMilli(testEpoch).UTC()
	env.commands <- simulation.IntroducePlayer{
		City: &city.City{
			ID: 2, OwnerPlayerID: dueño, Name: "Vecinópolis",
			CenterX: 55, CenterY: 55, Era: city.EraStone,
			PopulationLimit: 20, PresenceState: city.PresenceOnline,
		},
		BlockedMinX: 54, BlockedMinY: 54, BlockedMaxX: 56, BlockedMaxY: 56,
		TerritoryControl: &territory.Control{
			TerritoryID: 9, OwnerType: territory.OwnerPlayer, OwnerID: &idDueño,
			CapturedAt: &capturado, Version: 1,
		},
	}
	env.pump()

	ciudad := decode[protocol.CityUpdatePayload](t, c.await(protocol.TypeCityUpdate, 3*time.Second).Payload)
	require.Equal(t, int64(2), ciudad.ID)
	terr := decode[protocol.TerritoryUpdatePayload](t, c.await(protocol.TypeTerritoryUpdate, 3*time.Second).Payload)
	require.Equal(t, int64(9), terr.Territory.ID)
	require.Equal(t, "PLAYER", terr.Territory.OwnerType)
}

// session.view que desplaza el área 2 chunks (websocket-protocol §7.3): la
// respuesta es un world.snapshot nuevo del área, con el requestId del comando,
// y el terreno sólo de los chunks que la sesión todavía no tenía (§8.4).
func TestSessionViewRecentraConUnSnapshotDelAreaNueva(t *testing.T) {
	env := newE2EDeLado(t, 256, nil, nil) // 8 × 8 chunks: el área de 5 × 5 ya no lo cubre todo
	c := env.conectar()

	// La ciudad está en (40,40), chunk (1,1): el área inicial es x 0..3, y 0..3.
	// Dos chunks al este, el centro pasa al chunk (3,1): x 1..5, y 0..3.
	id := c.send(protocol.TypeSessionView, map[string]any{"center": map[string]int{"x": 40 + 64, "y": 40}})
	env.pump()
	snap := c.await(protocol.TypeWorldSnapshot, 3*time.Second)
	require.Equal(t, id, snap.RequestID, "la respuesta lleva el requestId del session.view")

	p := decode[protocol.WorldSnapshotPayload](t, snap.Payload)
	var area, terreno []string
	for _, ch := range p.Chunks {
		area = append(area, fmt.Sprintf("%d,%d", ch.CX, ch.CY))
	}
	for _, ch := range p.Terrain {
		terreno = append(terreno, fmt.Sprintf("%d,%d", ch.CX, ch.CY))
	}
	var esperadaArea, nuevos []string
	for cy := 0; cy <= 3; cy++ {
		for cx := 1; cx <= 5; cx++ {
			esperadaArea = append(esperadaArea, fmt.Sprintf("%d,%d", cx, cy))
			if cx >= 4 {
				nuevos = append(nuevos, fmt.Sprintf("%d,%d", cx, cy))
			}
		}
	}
	require.ElementsMatch(t, esperadaArea, area)
	require.ElementsMatch(t, nuevos, terreno, "el terreno ya enviado no se repite")
}
