package simulation_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

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

// INV-MOVE-005: una fila cuyo arrival_time_ms o destino no cuadran con su
// polilínea se corrige desde la polilínea al rehidratar. Con la llegada
// adelantada, la unidad aparecería ya en el destino; aquí sigue en camino.
func TestRecuperacionRecomputaLaLlegadaDeUnaFilaQueNoCuadra(t *testing.T) {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	u := &unit.Unit{ID: 1, PlayerID: uuid.New(), Type: unit.TypeVillager,
		X: 10, Y: 10, HP: 40, MaxHP: 40, Status: unit.StatusMoving}
	path, err := movement.BuildTimedPath(
		[]world.Tile{{X: 10, Y: 10}, {X: 11, Y: 10}, {X: 12, Y: 10}, {X: 13, Y: 10}}, w, 600)
	require.NoError(t, err)
	m := movement.New(u.ID, path, world.Tile{X: 13, Y: 10}, epoch)
	m.ID = 3
	m.ArrivalTimeMs = epoch + 700       // la fila dice que llegó a los 700 ms
	m.Target = world.Tile{X: 40, Y: 40} // y que iba a otra parte

	state := simulation.NewState(w)
	res := simulation.Hydrate(state, []*unit.Unit{u}, nil, []*movement.Movement{m},
		epoch+1000, slog.New(slog.NewTextHandler(io.Discard, nil)))

	require.Equal(t, 1, res.Resumed, "a los 1000 ms sigue en camino: la polilínea dura 1800")
	assert.Equal(t, epoch+1800, m.ArrivalTimeMs)
	assert.Equal(t, world.Tile{X: 13, Y: 10}, m.Target)
	assert.Equal(t, world.Tile{X: 11, Y: 10}, u.Tile())
	assert.Equal(t, unit.StatusMoving, u.Status)
}
