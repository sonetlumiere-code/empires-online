//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Integración de la economía (SPEC-ECON-WOOD §9 «integration»): los CHECK, la
// PK y los UNIQUE de 000006, cada débito con su efecto en una transacción y la
// idempotencia de los encargos, contra PostgreSQL real.

type escenarioEconomico struct {
	jugador  *postgres.BootstrapResult
	economia *postgres.EconomyRepo
	units    *postgres.UnitRepo
	moves    *postgres.MovementRepo
}

func nuevaEconomia(t *testing.T, ctx context.Context, store *postgres.Store) escenarioEconomico {
	t.Helper()
	_, _, units, moves, bootstrapper := newRepos(store)
	jugador, err := bootstrapper.Create(ctx, bootstrapRequest("lenador", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	return escenarioEconomico{
		jugador: jugador, economia: postgres.NewEconomyRepo(store, units, moves), units: units, moves: moves,
	}
}

func (e escenarioEconomico) ciudad() int64 { return e.jugador.City.ID }

func stockEnBase(t *testing.T, ctx context.Context, store *postgres.Store, cityID int64) (int64, int64) {
	t.Helper()
	var amount, version int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT amount, version FROM city_stocks WHERE city_id = $1 AND resource = 'WOOD'`, cityID).Scan(&amount, &version))
	return amount, version
}

func (e escenarioEconomico) depositar(t *testing.T, ctx context.Context, n, version int64) {
	t.Helper()
	require.NoError(t, e.economia.FlushStocks(ctx, []postgres.StockDelta{
		{CityID: e.ciudad(), Resource: economy.ResourceWood, Delta: n, Version: version},
	}))
}

func casa(cityID int64, x, y int32) building.Building {
	return building.Building{CityID: cityID, Type: building.TypeHouse, X: x, Y: y,
		Status: building.StatusUnderConstruction, CompletesAtMs: 1_757_376_015_000}
}

func debito(cityID, delta, version int64) postgres.StockDelta {
	return postgres.StockDelta{CityID: cityID, Resource: economy.ResourceWood, Delta: delta, Version: version}
}

// §4.1: la fundación crea el stock a cero.
func TestLaFundacionCreaElStockACero(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	amount, version := stockEnBase(t, ctx, store, e.ciudad())
	assert.Equal(t, int64(0), amount)
	assert.Equal(t, int64(0), version)
	filas, err := e.economia.ListStocks(ctx)
	require.NoError(t, err)
	require.Len(t, filas, 1)
}

// ADR-023 §1: el volcado suma deltas condicionados a la versión; un reintento
// de la misma versión no suma dos veces.
func TestElVolcadoSumaYUnReintentoNoDuplica(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.depositar(t, ctx, 10, 1)
	e.depositar(t, ctx, 10, 1) // reintento tras perder la confirmación
	e.depositar(t, ctx, 20, 3)
	e.depositar(t, ctx, 5, 2) // una escritura vieja no pisa a una nueva
	amount, version := stockEnBase(t, ctx, store, e.ciudad())
	assert.Equal(t, int64(30), amount)
	assert.Equal(t, int64(3), version)
}

// INV-ECON-001: el CHECK de la base impide un stock negativo, y un encargo que
// pide más de lo que hay se rechaza sin escribir nada.
func Test_INV_ECON_001_UnDebitoMayorQueElStockNoSeEscribe(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.depositar(t, ctx, 20, 1)

	_, err := store.Pool().Exec(ctx, `UPDATE city_stocks SET amount = -1 WHERE city_id = $1`, e.ciudad())
	require.Error(t, err)
	assert.True(t, postgres.IsCheckViolation(err), "city_stocks_amount_non_negative")

	_, err = e.economia.Build(ctx, "req-1", e.jugador.Player.ID, casa(e.ciudad(), 19, 19), debito(e.ciudad(), -30, 2), 1)
	require.True(t, errors.Is(err, economy.ErrInsufficientResources), "%v", err)
	amount, version := stockEnBase(t, ctx, store, e.ciudad())
	assert.Equal(t, int64(20), amount)
	assert.Equal(t, int64(1), version)
	edificios, err := e.economia.ListBuildings(ctx)
	require.NoError(t, err)
	assert.Empty(t, edificios)
}

// INV-ECON-002: un fallo al insertar el edificio deshace también el débito.
// El fallo se inyecta con un tipo fuera de buildings_type_valid: el UPDATE del
// stock ya se ha ejecutado cuando el INSERT revienta.
func Test_INV_ECON_002_UnFalloAlInsertarElEdificioNoGastaMadera(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.depositar(t, ctx, 40, 1)

	rota := casa(e.ciudad(), 19, 19)
	rota.Type = "CASTLE"
	_, err := e.economia.Build(ctx, "req-1", e.jugador.Player.ID, rota, debito(e.ciudad(), -30, 2), 1)
	require.Error(t, err)
	amount, version := stockEnBase(t, ctx, store, e.ciudad())
	assert.Equal(t, int64(40), amount, "la madera no se gastó")
	assert.Equal(t, int64(1), version)
	assert.Equal(t, 0, eventosDe(t, ctx, store, postgres.EventBuildingOrdered))
}

// RN-ECON-008 y ADR-014: el encargo debita y escribe en una transacción, y un
// reintento con el mismo requestId devuelve el mismo edificio sin debitar.
func TestUnEncargoDeCasaEsIdempotentePorRequestID(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.depositar(t, ctx, 40, 1)

	id, err := e.economia.Build(ctx, "req-1", e.jugador.Player.ID, casa(e.ciudad(), 19, 19), debito(e.ciudad(), -30, 2), 7)
	require.NoError(t, err)
	otra, err := e.economia.Build(ctx, "req-1", e.jugador.Player.ID, casa(e.ciudad(), 19, 19), debito(e.ciudad(), -30, 2), 7)
	require.NoError(t, err)
	assert.Equal(t, id, otra)
	amount, _ := stockEnBase(t, ctx, store, e.ciudad())
	assert.Equal(t, int64(10), amount, "un solo débito")
	assert.Equal(t, 1, eventosDe(t, ctx, store, postgres.EventBuildingOrdered))

	edificios, err := e.economia.ListBuildings(ctx)
	require.NoError(t, err)
	require.Len(t, edificios, 1)
	assert.Equal(t, building.StatusUnderConstruction, edificios[0].Status)
}

// RN-ECON-007: buildings_unique_tile; el segundo encargo del tile se rechaza
// como sitio inválido y su débito no se aplica.
func TestUnTileNoAdmiteDosEdificios(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.depositar(t, ctx, 100, 1)
	_, err := e.economia.Build(ctx, "req-1", e.jugador.Player.ID, casa(e.ciudad(), 19, 19), debito(e.ciudad(), -30, 2), 1)
	require.NoError(t, err)
	_, err = e.economia.Build(ctx, "req-2", e.jugador.Player.ID, casa(e.ciudad(), 19, 19), debito(e.ciudad(), -30, 3), 1)
	require.True(t, errors.Is(err, building.ErrInvalidSite), "%v", err)
	amount, _ := stockEnBase(t, ctx, store, e.ciudad())
	assert.Equal(t, int64(70), amount)
}

// Un encargo con una versión ya escrita no inserta su efecto sin débito.
func TestUnEncargoConElStockAdelantadoSeRechaza(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.depositar(t, ctx, 100, 5)
	_, err := e.economia.Build(ctx, "req-1", e.jugador.Player.ID, casa(e.ciudad(), 19, 19), debito(e.ciudad(), -30, 5), 1)
	require.True(t, errors.Is(err, postgres.ErrStaleStock), "%v", err)
	edificios, _ := e.economia.ListBuildings(ctx)
	assert.Empty(t, edificios)
}

// RN-ECON-009 e INV-ECON-004: completar suma cinco al límite, una sola vez.
func TestCompletarUnaCasaSumaSuBonoUnaVez(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.depositar(t, ctx, 30, 1)
	id, err := e.economia.Build(ctx, "req-1", e.jugador.Player.ID, casa(e.ciudad(), 19, 19), debito(e.ciudad(), -30, 2), 1)
	require.NoError(t, err)

	require.NoError(t, e.economia.CompleteBuilding(ctx, id, e.ciudad(), e.jugador.Player.ID, 5, 2))
	require.NoError(t, e.economia.CompleteBuilding(ctx, id, e.ciudad(), e.jugador.Player.ID, 5, 2))

	c, err := postgres.NewCityRepo(store).GetByID(ctx, e.ciudad())
	require.NoError(t, err)
	assert.EqualValues(t, 25, c.PopulationLimit)
	assert.Equal(t, 1, eventosDe(t, ctx, store, postgres.EventBuildingCompleted))
	edificios, _ := e.economia.ListBuildings(ctx)
	assert.Equal(t, building.StatusComplete, edificios[0].Status)
}

// RN-ECON-010 e INV-CITY-017: entrenar debita, reserva la plaza e inserta la
// producción juntos; con una en curso, CITY_BUSY; el reintento no duplica.
func TestEntrenarReservaLaPlazaYEsIdempotente(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.depositar(t, ctx, 120, 1)
	p := production.Production{CityID: e.ciudad(), UnitType: unit.TypeVillager, CompletesAtMs: 1_757_376_020_000}

	require.NoError(t, e.economia.Train(ctx, "req-1", e.jugador.Player.ID, p, 1, debito(e.ciudad(), -50, 2), 1))
	require.NoError(t, e.economia.Train(ctx, "req-1", e.jugador.Player.ID, p, 1, debito(e.ciudad(), -50, 2), 1))
	err := e.economia.Train(ctx, "req-2", e.jugador.Player.ID, p, 1, debito(e.ciudad(), -50, 3), 1)
	require.True(t, errors.Is(err, production.ErrCityBusy), "%v", err)

	amount, _ := stockEnBase(t, ctx, store, e.ciudad())
	assert.Equal(t, int64(70), amount, "un solo débito")
	c, err := postgres.NewCityRepo(store).GetByID(ctx, e.ciudad())
	require.NoError(t, err)
	assert.EqualValues(t, 4, c.Population, "tres aldeanos y una plaza reservada")
	prods, err := e.economia.ListProductions(ctx)
	require.NoError(t, err)
	require.Len(t, prods, 1)
	assert.True(t, prods[0].InProgress())
}

// El CHECK cities_population_within_limit es la última barrera de la reserva.
func TestEntrenarSinPlazaSeRechazaSinDebitar(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.depositar(t, ctx, 100, 1)
	_, err := store.Pool().Exec(ctx, `UPDATE cities SET population_limit = 3 WHERE id = $1`, e.ciudad())
	require.NoError(t, err)
	p := production.Production{CityID: e.ciudad(), UnitType: unit.TypeVillager, CompletesAtMs: 1}
	err = e.economia.Train(ctx, "req-1", e.jugador.Player.ID, p, 1, debito(e.ciudad(), -50, 2), 1)
	require.True(t, errors.Is(err, city.ErrPopulationLimit), "%v", err)
	amount, _ := stockEnBase(t, ctx, store, e.ciudad())
	assert.Equal(t, int64(100), amount)
}

// ADR-023 §3: el nacimiento inserta el aldeano y lo apunta en la producción;
// un reintento devuelve el mismo id. La población no cambia; el siguiente
// encargo sustituye la producción terminada.
func TestElNacimientoEsIdempotenteYLaPlazaPasaALaUnidad(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.depositar(t, ctx, 100, 1)
	p := production.Production{CityID: e.ciudad(), UnitType: unit.TypeVillager, CompletesAtMs: 1}
	require.NoError(t, e.economia.Train(ctx, "req-1", e.jugador.Player.ID, p, 1, debito(e.ciudad(), -50, 2), 1))

	at := world.Tile{X: 22, Y: 20}
	id, err := e.economia.BirthUnit(ctx, e.ciudad(), e.jugador.Player.ID, unit.TypeVillager, at, 40, 3)
	require.NoError(t, err)
	otra, err := e.economia.BirthUnit(ctx, e.ciudad(), e.jugador.Player.ID, unit.TypeVillager, at, 40, 3)
	require.NoError(t, err)
	assert.Equal(t, id, otra)
	assert.Equal(t, 1, eventosDe(t, ctx, store, postgres.EventUnitTrained))

	vivas, err := e.units.ListByPlayer(ctx, e.jugador.Player.ID)
	require.NoError(t, err)
	assert.Len(t, vivas, 4)
	c, _ := postgres.NewCityRepo(store).GetByID(ctx, e.ciudad())
	assert.EqualValues(t, 4, c.Population, "INV-CITY-017: la plaza pasó de la producción a la unidad")

	require.NoError(t, e.economia.Train(ctx, "req-2", e.jugador.Player.ID, p, 1, debito(e.ciudad(), -50, 3), 4))
	prods, _ := e.economia.ListProductions(ctx)
	require.Len(t, prods, 1)
	assert.Equal(t, "req-2", prods[0].RequestID)
	assert.True(t, prods[0].InProgress())
}

// INV-ECON-003: unit_tasks_pkey; y una transición escribe la tarea, el tramo y
// la unidad juntos, y la vuelve a escribir igual en un reintento.
func TestUnaTransicionDeTareaEscribeTramoUnidadYTarea(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	u := e.jugador.Units[0]
	bosque := world.Tile{X: u.X + 3, Y: u.Y}

	tiles := []world.Tile{u.Tile(), {X: u.X + 1, Y: u.Y}, {X: u.X + 2, Y: u.Y}, bosque}
	path, err := movement.BuildTimedPath(tiles, mundoDeCarga(t), 600)
	require.NoError(t, err)
	m := movement.New(u.ID, path, bosque, 1_757_376_000_000)
	m.ID = 9001
	tk := task.NewGather(u.ID, bosque)
	tr := postgres.TaskTransition{UnitID: u.ID, Start: m, WriteUnit: true, Status: unit.StatusMoving, At: u.Tile(), Task: &tk}
	require.NoError(t, e.economia.PersistTask(ctx, tr))
	require.NoError(t, e.economia.PersistTask(ctx, tr), "un reintento no duplica el tramo")

	activo, err := e.moves.GetActiveByUnit(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(9001), activo.ID)
	tareas, err := e.economia.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, tareas, 1)
	assert.Equal(t, task.PhaseToResource, tareas[0].Phase)

	_, err = store.Pool().Exec(ctx,
		`INSERT INTO unit_tasks (unit_id, kind, target_x, target_y, phase) VALUES ($1, 'GATHER_WOOD', 1, 1, 'TO_RESOURCE')`, u.ID)
	require.Error(t, err)
	assert.True(t, postgres.IsUniqueViolation(err), "unit_tasks_pkey: una tarea por unidad")

	// Llegada: cierra el tramo, la unidad WORKING y la tarea en WORKING.
	trabajando, err := tk.StartWork(m.ArrivalTimeMs, 10_000)
	require.NoError(t, err)
	require.NoError(t, e.economia.PersistTask(ctx, postgres.TaskTransition{
		UnitID: u.ID, FinishMovementID: m.ID, FinishStatus: movement.StatusCompleted,
		WriteUnit: true, Status: unit.StatusWorking, At: bosque, Task: &trabajando,
	}))
	tareas, _ = e.economia.ListTasks(ctx)
	assert.Equal(t, task.PhaseWorking, tareas[0].Phase)
	assert.Equal(t, m.ArrivalTimeMs+10_000, tareas[0].PhaseUntilMs)
	vivas, _ := e.units.ListByPlayer(ctx, e.jugador.Player.ID)
	for _, v := range vivas {
		if v.ID == u.ID {
			assert.Equal(t, unit.StatusWorking, v.Status)
			assert.Equal(t, bosque, v.Tile())
		}
	}

	// Una tarea en WORKING exige su vencimiento (unit_tasks_working_has_deadline).
	_, err = store.Pool().Exec(ctx, `UPDATE unit_tasks SET phase_until_ms = NULL WHERE unit_id = $1`, u.ID)
	require.Error(t, err)
	assert.True(t, postgres.IsCheckViolation(err))
}

// RN-ECON-003 en la base: una orden de movimiento borra la tarea en la misma
// transacción que sustituye el tramo, y un cierre (cancel_move, ruta bloqueada)
// también.
func TestUnaOrdenDeMovimientoBorraLaTareaEnSuTransaccion(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	_, cities, _, _, _ := newRepos(store)
	game := persistence.NewGameStore(store, e.units, cities, e.moves)
	u := e.jugador.Units[0]
	bosque := world.Tile{X: 30, Y: 30}

	tk := task.NewGather(u.ID, bosque)
	require.NoError(t, e.economia.PersistTask(ctx, postgres.TaskTransition{UnitID: u.ID, Task: &tk}))
	destino := world.Tile{X: u.X + 1, Y: u.Y}
	path, err := movement.BuildTimedPath([]world.Tile{u.Tile(), destino}, mundoDeCarga(t), 600)
	require.NoError(t, err)
	m := movement.New(u.ID, path, destino, 1_757_376_000_000)
	m.ID = 9100
	require.NoError(t, game.PersistMovementStart(ctx, *m, u.Tile()))
	tareas, _ := e.economia.ListTasks(ctx)
	assert.Empty(t, tareas, "el inicio del movimiento borra la tarea")

	require.NoError(t, e.economia.PersistTask(ctx, postgres.TaskTransition{UnitID: u.ID, Task: &tk}))
	require.NoError(t, game.PersistMovementFinish(ctx, simulation.MovementFinish{
		MovementID: m.ID, Status: movement.StatusCancelled, UnitID: u.ID, At: u.Tile(),
	}))
	tareas, _ = e.economia.ListTasks(ctx)
	assert.Empty(t, tareas, "el cierre también")
}
