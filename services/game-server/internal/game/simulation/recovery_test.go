package simulation_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loop"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
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

// INV-WORLD-004 / ADR-015: dentro de una ejecución el contador avanza en
// exactamente uno por tick ejecutado, con el reloj quieto o dando saltos. Los
// ticks que el loop descarta por ir por detrás no se cuentan, y ninguno se
// repite.
func TestElTickEsEstrictamenteCreciente(t *testing.T) {
	h := newHarness(t)
	anterior := h.state.Tick()
	for i, salto := range []int64{0, 0, 100, 100, 5_000, 0, 100, 3_600_000, 100} {
		h.clk.AdvanceMs(salto)
		h.loop.Step(h.clk.NowMs())
		require.Equal(t, anterior+1, h.state.Tick(), "paso %d (salto de %d ms)", i, salto)
		require.Equal(t, h.state.Tick(), h.loop.Tick())
		anterior = h.state.Tick()
	}
}

// Criterio 9 de M4: con diez unidades moviéndose a la vez —las diez órdenes y
// sus A* en el mismo tick—, ningún tick supera su período y
// eo_game_tick_overruns_total sigue a cero.
func TestDiezUnidadesMoviendoseNoPasanDelPresupuestoDelTick(t *testing.T) {
	if conDetectorDeCarreras {
		// Con -race el primer tick, el de los diez A*, ya pasa de 100 ms: se
		// mediría el detector, no la simulación. El criterio se mide en las
		// ejecuciones sin -race: el paso «Cobertura» de la CI y la suite local.
		t.Skip("un presupuesto de tiempo de pared no se mide con el detector de carreras")
	}
	h := newHarness(t)
	for i := int64(0); i < 7; i++ {
		h.ponerUnidad(10+i, h.playerID, world.Tile{X: int32(4 + i), Y: 20}, unit.StatusIdle, 40)
	}
	metrics := observability.NewMetrics()
	l := loop.New(h.sim, h.clk, h.commands, loop.Config{
		TickDuration: 100 * time.Millisecond, FlushIntervalTicks: 50,
		MaxCommandsPerTick: 256, EpochMs: epoch,
	}, metrics, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	var moviendo int
	h.state.EachUnit(func(u *unit.Unit) bool {
		h.commands <- simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
			UnitID: u.ID, Target: world.Tile{X: 60 - int32(moviendo), Y: 55}}
		moviendo++
		return true
	})
	require.Equal(t, 10, moviendo)
	l.Step(h.clk.NowMs())
	require.Equal(t, 10, h.state.MovementCount(), "las diez en marcha")

	for i := 0; i < 600; i++ { // 60 s de simulación
		h.clk.AdvanceMs(100)
		l.Step(h.clk.NowMs())
	}
	assert.Zero(t, testutil.ToFloat64(metrics.TickOverruns))
	assert.Zero(t, h.state.MovementCount(), "y todas han llegado")
}

// T-CITY-C-002: city.update sólo sale cuando algo cambia. Conectado y quieto,
// ninguno; al desconectarse, uno al degradar a OFFLINE_PENDING y otro al pasar
// a PROTECTED, y nada entre medias ni después.
func TestCityUpdateSoloSeEmiteAnteUnCambio(t *testing.T) {
	h := newHarness(t)
	sesion := uuid.New()
	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: sesion})
	h.rec.reset()

	h.advance(60 * time.Second)
	assert.Empty(t, h.rec.byType(protocol.TypeCityUpdate), "nada cambia, nada se emite")

	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: sesion})
	h.advance(31 * time.Second) // margen de desconexión: 30 s
	require.Len(t, h.rec.byType(protocol.TypeCityUpdate), 1, "OFFLINE_PENDING")

	h.advance(300 * time.Second) // cooldown de protección
	require.Len(t, h.rec.byType(protocol.TypeCityUpdate), 2, "PROTECTED")

	h.advance(120 * time.Second)
	assert.Len(t, h.rec.byType(protocol.TypeCityUpdate), 2, "y nada más mientras no cambie")
}
