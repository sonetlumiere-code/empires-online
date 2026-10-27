package simulation_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loop"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Recuperación de la economía (SPEC-ECON-WOOD §9 «recovery», INV-ECON-005).
// Cada test parte de lo que la base tendría escrito en una fase concreta de la
// línea temporal de economy_test.go, arranca en un instante posterior y
// comprueba que la fase en curso termina en su instante absoluto, haya vencido
// antes o después del arranque.

// persistido es el estado durable del que arranca un test.
type persistido struct {
	unidad     *unit.Unit
	movimiento *movement.Movement
	economia   simulation.EconomyLoad
	poblacion  int32
}

func mundoMadera(t *testing.T) *world.World {
	t.Helper()
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	for y := 0; y < 64; y++ {
		terrain[y*64+22] = byte(world.Forest)
		terrain[y*64+23] = byte(world.Forest)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	return w
}

// tramo es la polilínea en línea recta de `desde` a `hasta` en la fila y=10,
// tal como la calcularía el A*.
func tramo(t *testing.T, w *world.World, desdeX, hastaX int32, startMs int64) *movement.Movement {
	t.Helper()
	paso := int32(1)
	if hastaX < desdeX {
		paso = -1
	}
	var tiles []world.Tile
	for x := desdeX; ; x += paso {
		tiles = append(tiles, world.Tile{X: x, Y: 10})
		if x == hastaX {
			break
		}
	}
	path, err := movement.BuildTimedPath(tiles, w, 600)
	require.NoError(t, err)
	m := movement.New(1, path, world.Tile{X: hastaX, Y: 10}, startMs)
	m.ID = 40
	return m
}

// arrancar hidrata el mundo en `nowMs` como lo hace cmd/server —economía,
// Hydrate y ReconcileTasks— y monta la simulación encima.
func arrancar(t *testing.T, p persistido, nowMs int64) (*harness, []simulation.TaskWrite) {
	t.Helper()
	w := mundoMadera(t)
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	playerID := uuid.MustParse("11111111-1111-4111-8111-111111111111")

	state := simulation.NewState(w)
	state.SetEconomy(p.economia)
	poblacion := p.poblacion
	if poblacion == 0 {
		poblacion = 1
	}
	c := &city.City{ID: 1, OwnerPlayerID: playerID, Name: "Testópolis", CenterX: 10, CenterY: 10,
		Era: city.EraStone, Population: poblacion, PopulationLimit: 20, PresenceState: city.PresenceOnline}
	var movs []*movement.Movement
	if p.movimiento != nil {
		movs = append(movs, p.movimiento)
		state.SetMovementIDFloor(p.movimiento.ID)
	}
	var units []*unit.Unit
	if p.unidad != nil {
		units = append(units, p.unidad)
	}
	res := simulation.Hydrate(state, units, []*city.City{c}, movs, nowMs, discard)
	for _, f := range res.FinishedMovements {
		t.Logf("movimiento cerrado al rehidratar: %+v", f)
	}
	writes := simulation.ReconcileTasks(state, discard)

	clk := clock.NewFakeClock(nowMs)
	h := &harness{
		t: t, clk: clk, state: state, rec: &recorder{}, repos: newFakeRepos(),
		commands: make(chan simulation.Command, 256), persister: &syncPersister{},
		garrisons: &fakeGarrisons{}, economy: newFakeEconomy(), playerID: playerID, cityID: 1,
	}
	h.inbox = loop.NewInbox(256, nil, discard)
	h.sim = simulation.New(state, simulation.Deps{
		Clock: clk, Pathfinder: pathfinding.NewAStar(20000, 256), Broadcaster: h.rec,
		Persister: h.persister, Repos: h.repos, Garrisons: h.garrisons, Economy: h.economy,
		Inbox: h.inbox.Offer, Log: discard,
		ProtectionCooldown: 300 * time.Second, DisconnectGrace: 30 * time.Second,
		PathMaxNodes: 20000, PathMaxDistance: 256,
	})
	h.loop = loop.New(h.sim, clk, h.commands, h.inbox, loop.Config{
		TickDuration: 100 * time.Millisecond, FlushIntervalTicks: 50, MaxCommandsPerTick: 256, EpochMs: epoch,
	}, nil, nil, discard)
	return h, writes
}

func aldeano(st unit.Status, at world.Tile) *unit.Unit {
	ciudad := int64(1)
	return &unit.Unit{ID: 1, PlayerID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), CityID: &ciudad,
		Type: unit.TypeVillager, X: at.X, Y: at.Y, HP: 40, MaxHP: 40, Status: st}
}

// La ida al bosque escrita en `epoch`, con su tarea en TO_RESOURCE.
func enIda(t *testing.T) persistido {
	ida := tramo(t, mundoMadera(t), 12, 22, epoch)
	require.Equal(t, epoch+6_360, ida.ArrivalTimeMs)
	return persistido{
		unidad:     aldeano(unit.StatusMoving, deposito),
		movimiento: ida,
		economia:   simulation.EconomyLoad{Tasks: []task.Task{task.NewGather(1, bosque)}},
	}
}

// Reinicio con la ida en curso: la llegada y el trabajo caen en el mismo
// instante absoluto que sin reinicio.
func TestReinicioDuranteLaIdaAlBosque(t *testing.T) {
	h, writes := arrancar(t, enIda(t), epoch+3_000)
	require.Empty(t, writes)
	_, enMarcha := h.state.Movement(1)
	require.True(t, enMarcha, "el tramo se reanuda")

	h.hasta(epoch + 6_400)
	tk, _ := h.state.Task(1)
	assert.Equal(t, task.PhaseWorking, tk.Phase)
	assert.Equal(t, epoch+16_360, tk.PhaseUntilMs)
}

// INV-ECON-005: la ida llegó durante la caída. Hydrate no cierra el tramo: el
// primer tick procesa la llegada con su instante exacto (6 360), no con el del
// arranque.
func TestReinicioConLaLlegadaAlBosqueVencidaEnLaCaida(t *testing.T) {
	h, writes := arrancar(t, enIda(t), epoch+8_000)
	require.Empty(t, writes)
	u, _ := h.state.Unit(1)
	assert.Equal(t, bosque, u.Tile(), "la unidad ya está en el destino")

	h.tick()
	tk, _ := h.state.Task(1)
	assert.Equal(t, task.PhaseWorking, tk.Phase)
	assert.Equal(t, epoch+16_360, tk.PhaseUntilMs, "anclado a la llegada, no al arranque (8 000)")
	assert.Equal(t, unit.StatusWorking, u.Status)
	fin := h.economy.lastTask()
	require.NotNil(t, fin.Finish)
	assert.Equal(t, int64(40), fin.Finish.MovementID, "el tramo se cierra con la transición")
}

// La llegada y el fin del trabajo vencieron los dos en la caída: el primer
// tick encadena las dos fases, y la vuelta empieza en 16 360.
func TestReinicioConLaLlegadaYElTrabajoVencidos(t *testing.T) {
	h, _ := arrancar(t, enIda(t), epoch+18_000)
	h.tick()
	tk, _ := h.state.Task(1)
	assert.Equal(t, task.PhaseToDeposit, tk.Phase)
	vuelta, ok := h.state.Movement(1)
	require.True(t, ok)
	assert.Equal(t, epoch+16_360, vuelta.StartTimeMs)
	assert.Equal(t, epoch+22_360, vuelta.ArrivalTimeMs)
}

// Reinicio en WORKING con el vencimiento ya pasado.
func TestReinicioDuranteElTrabajo(t *testing.T) {
	trabajando, err := task.NewGather(1, bosque).StartWork(epoch+6_360, 10_000)
	require.NoError(t, err)
	p := persistido{
		unidad:   aldeano(unit.StatusWorking, bosque),
		economia: simulation.EconomyLoad{Tasks: []task.Task{trabajando}},
	}

	// Antes del vencimiento: sale en 16 360 exactos.
	h, _ := arrancar(t, p, epoch+10_000)
	h.hasta(epoch + 16_400)
	vuelta, ok := h.state.Movement(1)
	require.True(t, ok)
	assert.Equal(t, epoch+16_360, vuelta.StartTimeMs)

	// Después: el primer tick sale desde 16 360, no desde el arranque.
	h2, _ := arrancar(t, persistido{unidad: aldeano(unit.StatusWorking, bosque),
		economia: simulation.EconomyLoad{Tasks: []task.Task{trabajando}}}, epoch+17_000)
	h2.tick()
	vuelta, ok = h2.state.Movement(1)
	require.True(t, ok)
	assert.Equal(t, epoch+16_360, vuelta.StartTimeMs)
}

func enVuelta(t *testing.T) persistido {
	trabajando, _ := task.NewGather(1, bosque).StartWork(epoch+6_360, 10_000)
	volviendo, _ := trabajando.FinishWork(10)
	vuelta := tramo(t, mundoMadera(t), 22, 12, epoch+16_360)
	require.Equal(t, epoch+22_360, vuelta.ArrivalTimeMs)
	return persistido{
		unidad:     aldeano(unit.StatusMoving, bosque),
		movimiento: vuelta,
		economia:   simulation.EconomyLoad{Tasks: []task.Task{volviendo}},
	}
}

// INV-ECON-005 e INV-ECON-006: la vuelta llegó durante la caída. El viaje en
// curso se termina —deposita— y, sin el dueño conectado, ninguno empieza.
func TestReinicioConLaVueltaVencidaTerminaElViajeSinEmpezarOtro(t *testing.T) {
	h, _ := arrancar(t, enVuelta(t), epoch+30_000)
	h.tick()
	assert.Equal(t, int64(10), h.madera())
	_, ok := h.state.Task(1)
	assert.False(t, ok)
	u, _ := h.state.Unit(1)
	assert.Equal(t, unit.StatusIdle, u.Status)
	assert.Equal(t, deposito, u.Tile())
	_, enMarcha := h.state.Movement(1)
	assert.False(t, enMarcha)
}

// Con la vuelta en curso y el dueño que vuelve a conectarse, el depósito cae en
// 22 360 y el viaje siguiente empieza en ese instante.
func TestReinicioDuranteLaVueltaConElDuenoConectado(t *testing.T) {
	h, _ := arrancar(t, enVuelta(t), epoch+20_000)
	h.conectar()
	h.hasta(epoch + 22_400)
	assert.Equal(t, int64(10), h.madera())
	tk, ok := h.state.Task(1)
	require.True(t, ok)
	assert.Equal(t, task.PhaseToResource, tk.Phase)
	ida, ok := h.state.Movement(1)
	require.True(t, ok)
	assert.Equal(t, epoch+22_360, ida.StartTimeMs)
}

// Una tarea en un tramo sin movimiento activo no se puede reanudar: termina al
// arrancar, con su escritura.
func TestUnaTareaSinSuTramoTerminaAlArrancar(t *testing.T) {
	p := persistido{
		unidad:   aldeano(unit.StatusIdle, deposito),
		economia: simulation.EconomyLoad{Tasks: []task.Task{task.NewGather(1, bosque)}},
	}
	h, writes := arrancar(t, p, epoch)
	require.Len(t, writes, 1)
	assert.Nil(t, writes[0].Task)
	assert.Equal(t, unit.StatusIdle, writes[0].Status)
	_, ok := h.state.Task(1)
	assert.False(t, ok)
}

// INV-ECON-003 al arrancar: una tarea en WORKING lejos de su bosque termina.
func TestUnaTareaEnWorkingFueraDeSuBosqueTerminaAlArrancar(t *testing.T) {
	trabajando, _ := task.NewGather(1, bosque).StartWork(epoch, 10_000)
	p := persistido{
		unidad:   aldeano(unit.StatusWorking, deposito),
		economia: simulation.EconomyLoad{Tasks: []task.Task{trabajando}},
	}
	_, writes := arrancar(t, p, epoch+1_000)
	require.Len(t, writes, 1)
	assert.Equal(t, unit.StatusIdle, writes[0].Status)
}

// Una casa en obras sobrevive al reinicio y se completa en su instante; si
// venció durante la caída, en el primer tick.
func TestReinicioDuranteUnaConstruccion(t *testing.T) {
	casa := building.Building{ID: 7, CityID: 1, Type: building.TypeHouse, X: 9, Y: 9,
		Status: building.StatusUnderConstruction, CompletesAtMs: epoch + 15_000}
	load := simulation.EconomyLoad{Buildings: []building.Building{casa}}

	h, _ := arrancar(t, persistido{economia: load}, epoch+10_000)
	h.hasta(epoch + 14_900)
	b, _ := h.state.Building(7)
	assert.Equal(t, building.StatusUnderConstruction, b.Status)
	h.hasta(epoch + 15_000)
	b, _ = h.state.Building(7)
	assert.Equal(t, building.StatusComplete, b.Status)
	assert.Equal(t, int32(25), h.ciudad().PopulationLimit)

	h2, _ := arrancar(t, persistido{economia: load}, epoch+40_000)
	h2.tick()
	b, _ = h2.state.Building(7)
	assert.Equal(t, building.StatusComplete, b.Status)
	assert.Equal(t, int32(25), h2.ciudad().PopulationLimit)
	require.Len(t, h2.economy.completions, 1)
}

// Un entrenamiento a medias sobrevive al reinicio: la plaza sigue reservada y
// el aldeano nace en el tick de su vencimiento.
func TestReinicioDuranteUnEntrenamiento(t *testing.T) {
	p := persistido{
		poblacion: 2, // una unidad... y la plaza reservada
		unidad:    aldeano(unit.StatusIdle, deposito),
		economia: simulation.EconomyLoad{Productions: []production.Production{
			{CityID: 1, UnitType: unit.TypeVillager, CompletesAtMs: epoch + 20_000, RequestID: "r"},
		}},
	}
	h, _ := arrancar(t, p, epoch+12_000)
	_, confirmada, ok := h.state.Production(1)
	require.True(t, ok)
	assert.True(t, confirmada, "lo que se leyó de la base está confirmado")

	h.hasta(epoch + 19_900)
	assert.Empty(t, h.economy.births)
	h.hasta(epoch + 20_000)
	require.Len(t, h.economy.births, 1)
	h.tick()
	assert.Len(t, h.rec.byType(protocol.TypeEntitySpawn), 1)
	assert.Equal(t, int32(2), h.ciudad().Population)

	// Vencido en la caída: nace en el primer tick.
	h2, _ := arrancar(t, p, epoch+60_000)
	h2.tick()
	require.Len(t, h2.economy.births, 1)
}

// Una producción terminada (con su unidad) no se carga como en curso.
func TestUnaProduccionTerminadaNoReservaNada(t *testing.T) {
	p := persistido{economia: simulation.EconomyLoad{Productions: []production.Production{
		{CityID: 1, UnitType: unit.TypeVillager, CompletesAtMs: epoch, RequestID: "r", UnitID: 77},
	}}}
	h, _ := arrancar(t, p, epoch+1_000)
	_, _, ok := h.state.Production(1)
	assert.False(t, ok)
}
