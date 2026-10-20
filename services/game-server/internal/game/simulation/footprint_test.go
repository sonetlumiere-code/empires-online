package simulation_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// fundar incorpora una ciudad ajena con su 3×3 amurallado, como hace el alta.
func (h *harness) fundar(id int64, center world.Tile) {
	h.t.Helper()
	h.send(simulation.IntroducePlayer{
		City: &city.City{
			ID: id, OwnerPlayerID: uuid.New(), Name: "Recién fundada",
			CenterX: center.X, CenterY: center.Y, Era: city.EraStone, PopulationLimit: 20,
			PresenceState: city.PresenceOnline,
		},
		BlockedMinX: center.X - 1, BlockedMinY: center.Y - 1,
		BlockedMaxX: center.X + 1, BlockedMaxY: center.Y + 1,
	})
}

// RN-CITY-022 / INV-UNIT-001: una unidad dentro de la huella no se queda
// emparedada; se aparta al tile libre más cercano, y lo hace de forma
// determinista.
func TestFundarApartaLaUnidadQueQuedaDentroDeLaMuralla(t *testing.T) {
	h := newHarness(t)
	ajena := h.ponerUnidad(40, otroJugador, world.Tile{X: 31, Y: 30}, unit.StatusIdle, 40)
	h.rec.reset()

	h.fundar(99, world.Tile{X: 30, Y: 30})

	// Desde (31,30), a distancia 1 están (32,29), (32,30) y (32,31); gana el
	// primero en orden (y, x).
	assert.Equal(t, world.Tile{X: 32, Y: 29}, ajena.Tile())
	assert.Equal(t, unit.StatusIdle, ajena.Status)
	assert.True(t, h.state.World().IsWalkable(ajena.X, ajena.Y), "INV-UNIT-001")

	updates := h.rec.byType(protocol.TypeEntityUpdate)
	require.Len(t, updates, 1)
	p := updates[0].Payload.(protocol.EntityUpdatePayload)
	assert.Equal(t, ajena.ID, p.ID)
	assert.Equal(t, int32(32), *p.X)
	assert.Equal(t, int32(29), *p.Y)
	assert.Equal(t, "chunk", updates[0].Kind, "una unidad visible se anuncia a su chunk")

	sucias := h.state.DrainDirty()
	require.Len(t, sucias, 1, "la posición nueva se vuelca")
	assert.Equal(t, ajena.ID, sucias[0].ID)

	// Y sigue pudiendo moverse: el objeto de la regla.
	h.send(simulation.MoveUnit{
		PlayerID: otroJugador, RequestID: uuid.NewString(), UnitID: ajena.ID,
		Target: world.Tile{X: 40, Y: 40},
	})
	assert.Equal(t, unit.StatusMoving, ajena.Status)
}

// RN-CITY-022 / INV-MOVE-004: un movimiento cuya ruta pendiente atraviesa la
// muralla nueva se detiene en el tile actual con PATH_BLOCKED, y el cierre se
// persiste con la posición donde se detuvo.
func TestUnaRutaQueCruzaLaMurallaNuevaSeDetieneConPathBlocked(t *testing.T) {
	h := newHarness(t)
	aldeano := h.units[0] // en (12,10)
	h.mover(aldeano.ID, world.Tile{X: 40, Y: 10})
	m, ok := h.state.Movement(aldeano.ID)
	require.True(t, ok)
	movementID := m.ID
	h.advance(time.Second)
	parada := h.unitTile(aldeano.ID)
	require.Less(t, parada.X, int32(29), "todavía no ha llegado a la muralla")
	h.rec.reset()

	h.fundar(99, world.Tile{X: 30, Y: 10})

	_, sigue := h.state.Movement(aldeano.ID)
	assert.False(t, sigue)
	assert.Equal(t, unit.StatusIdle, aldeano.Status)
	assert.Equal(t, parada, aldeano.Tile(), "se detiene donde estaba, no salta a ningún sitio")

	canceladas := h.rec.byType(protocol.TypeUnitMovementCancelled)
	require.Len(t, canceladas, 1)
	p := canceladas[0].Payload.(protocol.UnitMovementCancelledPayload)
	assert.Equal(t, string(movement.ReasonPathBlocked), p.Reason)
	assert.Equal(t, movementID, p.MovementID)
	assert.Equal(t, protocol.Tile{X: parada.X, Y: parada.Y}, p.StoppedAt)

	assert.Equal(t, movement.StatusCancelled, h.repos.movementsFinish[movementID])
	ultimo := h.repos.finishes[len(h.repos.finishes)-1]
	assert.Equal(t, aldeano.ID, ultimo.UnitID)
	assert.Equal(t, parada, ultimo.At)
}

// Una ruta que no toca la muralla sigue su curso: la regla no detiene a nadie
// por precaución.
func TestUnaRutaAjenaALaMurallaNuevaSigueSuCurso(t *testing.T) {
	h := newHarness(t)
	aldeano := h.units[0]
	h.mover(aldeano.ID, world.Tile{X: 20, Y: 10})
	h.advance(time.Second)
	h.rec.reset()

	h.fundar(99, world.Tile{X: 40, Y: 40})

	_, sigue := h.state.Movement(aldeano.ID)
	assert.True(t, sigue)
	assert.Equal(t, unit.StatusMoving, aldeano.Status)
	assert.Empty(t, h.rec.byType(protocol.TypeUnitMovementCancelled))
}

// Una unidad que va de camino y está pisando la huella en el instante de la
// fundación pasa por los dos pasos: se detiene y después se aparta.
func TestUnaUnidadQueSeDetieneDentroDeLaMurallaSeAparta(t *testing.T) {
	h := newHarness(t)
	aldeano := h.units[0]
	h.mover(aldeano.ID, world.Tile{X: 40, Y: 10})
	for h.unitTile(aldeano.ID).X < 30 {
		h.advance(100 * time.Millisecond)
	}
	dentro := h.unitTile(aldeano.ID)
	require.Equal(t, world.Tile{X: 30, Y: 10}, dentro)

	h.fundar(99, world.Tile{X: 30, Y: 10})

	_, sigue := h.state.Movement(aldeano.ID)
	assert.False(t, sigue)
	assert.Equal(t, unit.StatusIdle, aldeano.Status)
	assert.True(t, h.state.World().IsWalkable(aldeano.X, aldeano.Y), "INV-UNIT-001")
	// Desde el centro de la huella todo el anillo está a distancia 2; el primer
	// tile en orden (y, x) es la esquina superior izquierda.
	assert.Equal(t, world.Tile{X: 28, Y: 8}, aldeano.Tile())
}

// Las guarnecidas no están en el mapa: la muralla no las mueve.
func TestLaMurallaNuevaNoTocaUnidadesGuarnecidas(t *testing.T) {
	h := newHarness(t)
	dentro := h.ponerUnidad(40, otroJugador, world.Tile{X: 30, Y: 30}, unit.StatusGarrisoned, 40)

	h.fundar(99, world.Tile{X: 30, Y: 30})

	assert.Equal(t, world.Tile{X: 30, Y: 30}, dentro.Tile())
	assert.Equal(t, unit.StatusGarrisoned, dentro.Status)
}

// El arranque aplica la misma regla: una caída entre el alta y el volcado deja
// en disco la unidad dentro de la muralla, o en marcha a través de ella.
func TestAlArrancarLaMurallaApartaYDetieneComoEnElAlta(t *testing.T) {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	jugador := uuid.New()

	emparedada := &unit.Unit{ID: 1, PlayerID: jugador, Type: unit.TypeVillager,
		X: 31, Y: 30, HP: 40, MaxHP: 40, Status: unit.StatusIdle}
	enCamino := &unit.Unit{ID: 2, PlayerID: jugador, Type: unit.TypeVillager,
		X: 25, Y: 30, HP: 40, MaxHP: 40, Status: unit.StatusMoving}
	tiles := make([]world.Tile, 0, 16)
	for x := int32(25); x <= 36; x++ {
		tiles = append(tiles, world.Tile{X: x, Y: 30})
	}
	path, err := movement.BuildTimedPath(tiles, w, 600)
	require.NoError(t, err)
	m := movement.New(enCamino.ID, path, world.Tile{X: 36, Y: 30}, epoch)
	m.ID = 7

	ciudad := &city.City{ID: 99, OwnerPlayerID: uuid.New(), CenterX: 30, CenterY: 30,
		Era: city.EraStone, PopulationLimit: 20, PresenceState: city.PresenceOnline}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	state := simulation.NewState(w)
	// Dos pasos después del inicio: la unidad está en (27,30), a punto de
	// cruzar.
	now := epoch + 1200
	res := simulation.Hydrate(state, []*unit.Unit{emparedada, enCamino}, []*city.City{ciudad},
		[]*movement.Movement{m}, now, log)
	require.Equal(t, 1, res.Resumed)

	// Lo que hace main: murallas, y después la regla.
	minX, minY, maxX, maxY := ciudad.UrbanBounds()
	w.SetBlocked(minX, minY, maxX, maxY, true)
	finished := simulation.ClearFootprints(state, []*city.City{ciudad}, now, log)

	assert.Equal(t, world.Tile{X: 32, Y: 29}, emparedada.Tile())
	assert.Equal(t, unit.StatusIdle, emparedada.Status)

	_, sigue := state.Movement(enCamino.ID)
	assert.False(t, sigue)
	assert.Equal(t, unit.StatusIdle, enCamino.Status)
	assert.Equal(t, world.Tile{X: 27, Y: 30}, enCamino.Tile())
	require.Equal(t, []simulation.FinishedMovement{{
		MovementID: 7, Status: movement.StatusCancelled, UnitID: enCamino.ID, At: world.Tile{X: 27, Y: 30},
	}}, finished)

	sucias := state.DrainDirty()
	require.Len(t, sucias, 2, "las dos posiciones nuevas se vuelcan")
}

// INV-UNIT-005: una unidad GARRISONED con un movimiento ACTIVE es una fila
// corrupta. La rehidratación no la saca de la ciudad: cierra el movimiento
// FAILED sin tocar la unidad.
func TestRecuperacionNoReanudaElMovimientoDeUnaUnidadGuarnecida(t *testing.T) {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	guarnecida := &unit.Unit{ID: 1, PlayerID: uuid.New(), Type: unit.TypeVillager,
		X: 10, Y: 10, HP: 40, MaxHP: 40, Status: unit.StatusGarrisoned}
	path, err := movement.BuildTimedPath(
		[]world.Tile{{X: 10, Y: 10}, {X: 11, Y: 10}, {X: 12, Y: 10}}, w, 600)
	require.NoError(t, err)
	m := movement.New(guarnecida.ID, path, world.Tile{X: 12, Y: 10}, epoch)
	m.ID = 5

	state := simulation.NewState(w)
	res := simulation.Hydrate(state, []*unit.Unit{guarnecida}, nil,
		[]*movement.Movement{m}, epoch+600, slog.New(slog.NewTextHandler(io.Discard, nil)))

	assert.Equal(t, 0, res.Resumed)
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, unit.StatusGarrisoned, guarnecida.Status)
	assert.Equal(t, world.Tile{X: 10, Y: 10}, guarnecida.Tile())
	_, activo := state.Movement(guarnecida.ID)
	assert.False(t, activo)
	require.Equal(t, []simulation.FinishedMovement{{MovementID: 5, Status: movement.StatusFailed}},
		res.FinishedMovements, "UnitID 0: el cierre no escribe la unidad")
}
