//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mustJSON serializa la polilínea para las inserciones directas de estos tests.
func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

const testEpoch int64 = 1_757_376_000_000

// testWorld construye un mundo pequeño y totalmente transitable.
func testWorld(t *testing.T) *world.World {
	t.Helper()
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	return w
}

func newRepos(s *postgres.Store) (*postgres.PlayerRepo, *postgres.CityRepo, *postgres.UnitRepo, *postgres.MovementRepo, *postgres.Bootstrapper) {
	p := postgres.NewPlayerRepo(s)
	c := postgres.NewCityRepo(s)
	u := postgres.NewUnitRepo(s, 32)
	m := postgres.NewMovementRepo(s)
	tr := postgres.NewTerritoryRepo(s)
	return p, c, u, m, postgres.NewBootstrapper(s, p, c, u, tr)
}

func bootstrapRequest(username string, center world.Tile) postgres.BootstrapRequest {
	return postgres.BootstrapRequest{
		Username:       username,
		PasswordHash:   "$2a$10$hashdepruebahashdepruebahashdepruebahashdepru",
		CivilizationID: 1,
		FactionID:      3,
		CityName:       username + "polis",
		CityCenter:     center,
		Era:            city.EraStone,
		PopulationCap:  20,
		VillagerSpawns: []world.Tile{
			{X: center.X + 2, Y: center.Y},
			{X: center.X - 2, Y: center.Y},
			{X: center.X, Y: center.Y + 2},
		},
	}
}

// ─────────────────────────────────────────────────────────────
// Alta atómica del jugador
// ─────────────────────────────────────────────────────────────

// INV-PLAYER-003: un jugador recién creado tiene exactamente una ciudad y 3 aldeanos.
func TestBootstrapCreaMundoCompletoDelJugador(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, units, _, bootstrapper := newRepos(store)

	result, err := bootstrapper.Create(ctx, bootstrapRequest("jugador_uno", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)

	require.NotEqual(t, uuid.Nil, result.Player.ID)
	require.NotZero(t, result.City.ID)
	require.Len(t, result.Units, 3, "exactamente tres aldeanos")
	require.EqualValues(t, 3, result.City.Population, "la población se deriva de las unidades")

	// Y todo está realmente en la base de datos.
	persisted, err := cities.GetByOwner(ctx, result.Player.ID)
	require.NoError(t, err)
	require.Equal(t, result.City.ID, persisted.ID)
	require.Equal(t, city.PresenceOnline, persisted.PresenceState)
	require.EqualValues(t, 20, persisted.PopulationLimit)

	owned, err := units.ListByPlayer(ctx, result.Player.ID)
	require.NoError(t, err)
	require.Len(t, owned, 3)
	for _, u := range owned {
		require.Equal(t, unit.TypeVillager, u.Type)
		require.Equal(t, unit.StatusIdle, u.Status)
		require.EqualValues(t, 40, u.HP)
		require.NotNil(t, u.CityID)
		require.Equal(t, result.City.ID, *u.CityID)
	}
}

// La atomicidad no es un lujo: un jugador sin ciudad es un estado que ninguna
// regla del juego sabe interpretar.
func TestBootstrapFallidoNoDejaNadaAMedias(t *testing.T) {
	store, ctx := newTestStore(t)
	players, cities, _, _, bootstrapper := newRepos(store)

	_, err := bootstrapper.Create(ctx, bootstrapRequest("jugador_dos", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)

	// Mismo nombre de usuario: la transacción entera debe abortar.
	_, err = bootstrapper.Create(ctx, bootstrapRequest("jugador_dos", world.Tile{X: 40, Y: 40}))
	require.Error(t, err)

	var cityCount int
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT count(*) FROM cities`).Scan(&cityCount))
	require.Equal(t, 1, cityCount, "el alta fallida no pudo dejar una segunda ciudad")

	var unitCount int
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT count(*) FROM units`).Scan(&unitCount))
	require.Equal(t, 3, unitCount, "ni aldeanos huérfanos")

	// El primer jugador sigue intacto.
	p, _, err := players.GetByUsername(ctx, "jugador_dos")
	require.NoError(t, err)
	_, err = cities.GetByOwner(ctx, p.ID)
	require.NoError(t, err)
}

// El centro de la ciudad es único: dos ciudades no pueden solaparse.
func TestDosCiudadesNoPuedenCompartirCentro(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)

	_, err := bootstrapper.Create(ctx, bootstrapRequest("ciudad_a", world.Tile{X: 30, Y: 30}))
	require.NoError(t, err)

	_, err = bootstrapper.Create(ctx, bootstrapRequest("ciudad_b", world.Tile{X: 30, Y: 30}))
	require.Error(t, err, "el UNIQUE (center_x, center_y) debe rechazarlo")
}

// ─────────────────────────────────────────────────────────────
// Movimientos
// ─────────────────────────────────────────────────────────────

// INV-MOVE-001 garantizado FÍSICAMENTE por el índice único parcial.
//
// Este test es el que demuestra que el invariante no depende de que el código de
// aplicación se acuerde de comprobarlo: aunque dos conexiones lo intentaran a la
// vez, PostgreSQL rechaza la segunda.
func TestIndiceUnicoImpideDosMovimientosActivos(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, movements, bootstrapper := newRepos(store)
	w := testWorld(t)

	result, err := bootstrapper.Create(ctx, bootstrapRequest("mover_uno", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	u := result.Units[0]

	path, err := movement.BuildTimedPath(
		[]world.Tile{{X: u.X, Y: u.Y}, {X: u.X + 1, Y: u.Y}}, w, 600)
	require.NoError(t, err)

	// El primero entra.
	m1 := movement.New(u.ID, path, world.Tile{X: u.X + 1, Y: u.Y}, testEpoch)
	_, err = insertActiveDirectly(ctx, store, m1)
	require.NoError(t, err)

	// El segundo, insertado SIN cancelar el anterior, debe ser rechazado por la
	// base de datos.
	m2 := movement.New(u.ID, path, world.Tile{X: u.X + 1, Y: u.Y}, testEpoch+1000)
	_, err = insertActiveDirectly(ctx, store, m2)
	require.Error(t, err, "el índice único parcial debe impedir dos movimientos ACTIVE")
	require.True(t, postgres.IsUniqueViolation(err), "y debe ser precisamente una violación de unicidad")

	// La vía correcta —Start, que cancela el anterior en la misma transacción— sí funciona.
	m3 := movement.New(u.ID, path, world.Tile{X: u.X + 1, Y: u.Y}, testEpoch+2000)
	newID, cancelledID, err := movements.Start(ctx, store.Pool(), m3)
	require.NoError(t, err)
	require.NotZero(t, newID)
	require.Equal(t, m1.ID, cancelledID, "Start cancela el movimiento anterior")

	active, err := movements.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1, "sólo puede quedar uno activo")
}

// insertActiveDirectly esquiva la lógica de aplicación para probar la garantía
// de la base de datos por sí sola.
func insertActiveDirectly(ctx context.Context, store *postgres.Store, m *movement.Movement) (int64, error) {
	var id int64
	pathJSON := mustJSON(m.Path)
	err := store.Pool().QueryRow(ctx,
		`INSERT INTO unit_movements (unit_id, path, target_x, target_y, start_time_ms, arrival_time_ms, status)
		 VALUES ($1, $2, $3, $4, $5, $6, 'ACTIVE') RETURNING id`,
		m.UnitID, pathJSON, m.Target.X, m.Target.Y, m.StartTimeMs, m.ArrivalTimeMs).Scan(&id)
	if err == nil {
		m.ID = id
	}
	return id, err
}

func TestMovimientoSobreviveAlCicloDePersistencia(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, movements, bootstrapper := newRepos(store)
	w := testWorld(t)

	result, err := bootstrapper.Create(ctx, bootstrapRequest("persistente", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	u := result.Units[0]

	tiles := []world.Tile{
		{X: u.X, Y: u.Y}, {X: u.X + 1, Y: u.Y}, {X: u.X + 2, Y: u.Y}, {X: u.X + 3, Y: u.Y},
	}
	path, err := movement.BuildTimedPath(tiles, w, 600)
	require.NoError(t, err)

	original := movement.New(u.ID, path, tiles[3], testEpoch)
	_, _, err = movements.Start(ctx, store.Pool(), original)
	require.NoError(t, err)

	// Se relee desde cero, como haría el servidor al arrancar.
	loaded, err := movements.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, loaded, 1)

	got := loaded[0]
	require.Equal(t, original.ID, got.ID)
	require.Equal(t, original.UnitID, got.UnitID)
	require.Equal(t, original.StartTimeMs, got.StartTimeMs)
	require.Equal(t, original.ArrivalTimeMs, got.ArrivalTimeMs)
	require.Equal(t, original.Target, got.Target)
	require.Equal(t, original.Path, got.Path, "la polilínea debe sobrevivir intacta al viaje por jsonb")

	// Y la posición derivada sigue siendo exacta.
	require.Equal(t, tiles[1], got.PositionAt(testEpoch+600))
	require.Equal(t, tiles[3], got.PositionAt(testEpoch+1800))
}

func TestFinishEsIdempotente(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, movements, bootstrapper := newRepos(store)
	w := testWorld(t)

	result, err := bootstrapper.Create(ctx, bootstrapRequest("idem_finish", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	u := result.Units[0]

	path, _ := movement.BuildTimedPath([]world.Tile{{X: u.X, Y: u.Y}, {X: u.X + 1, Y: u.Y}}, w, 600)
	m := movement.New(u.ID, path, world.Tile{X: u.X + 1, Y: u.Y}, testEpoch)
	_, _, err = movements.Start(ctx, store.Pool(), m)
	require.NoError(t, err)

	// La finalización puede llegar por el tick y por la recuperación: la segunda
	// vez no debe fallar.
	require.NoError(t, movements.Finish(ctx, store.Pool(), m.ID, movement.StatusCompleted))
	require.NoError(t, movements.Finish(ctx, store.Pool(), m.ID, movement.StatusCompleted))

	active, err := movements.ListActive(ctx)
	require.NoError(t, err)
	require.Empty(t, active)
}

// ─────────────────────────────────────────────────────────────
// Recuperación tras reinicio: el test que justifica ADR-011
// ─────────────────────────────────────────────────────────────

func TestRecuperacionCompletaTrasReinicio(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, units, movements, bootstrapper := newRepos(store)
	w := testWorld(t)

	result, err := bootstrapper.Create(ctx, bootstrapRequest("superviviente", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	u := result.Units[0]
	origin := world.Tile{X: u.X, Y: u.Y}

	tiles := []world.Tile{
		origin,
		{X: origin.X + 1, Y: origin.Y},
		{X: origin.X + 2, Y: origin.Y},
		{X: origin.X + 3, Y: origin.Y},
		{X: origin.X + 4, Y: origin.Y},
	}
	path, err := movement.BuildTimedPath(tiles, w, 600) // 2400 ms en total
	require.NoError(t, err)

	m := movement.New(u.ID, path, tiles[4], testEpoch)
	_, _, err = movements.Start(ctx, store.Pool(), m)
	require.NoError(t, err)
	require.NoError(t, units.SetStatus(ctx, store.Pool(), u.ID, unit.StatusMoving))

	t.Run("el servidor vuelve a mitad del recorrido", func(t *testing.T) {
		loadedUnits, err := units.ListAlive(ctx)
		require.NoError(t, err)
		loadedCities, err := cities.ListAll(ctx)
		require.NoError(t, err)
		loadedMovements, err := movements.ListActive(ctx)
		require.NoError(t, err)

		state := simulation.NewState(testWorld(t))
		res := simulation.Hydrate(state, loadedUnits, loadedCities, loadedMovements,
			testEpoch+1200, discardLogger())

		require.Equal(t, 1, res.Resumed)
		require.Equal(t, 3, res.Units)
		require.Equal(t, 1, res.Cities)

		recovered, ok := state.Unit(u.ID)
		require.True(t, ok)
		require.Equal(t, tiles[2], recovered.Tile(),
			"a los 1200 ms la unidad debe estar en el tercer tile, reconstruido de la polilínea")
		require.Equal(t, unit.StatusMoving, recovered.Status)
	})

	t.Run("el servidor vuelve cuando el movimiento ya venció", func(t *testing.T) {
		loadedUnits, err := units.ListAlive(ctx)
		require.NoError(t, err)
		loadedMovements, err := movements.ListActive(ctx)
		require.NoError(t, err)

		state := simulation.NewState(testWorld(t))
		res := simulation.Hydrate(state, loadedUnits, nil, loadedMovements,
			testEpoch+3_600_000, discardLogger())

		require.Equal(t, 1, res.Arrived, "el movimiento terminó mientras el servidor estaba caído")
		require.Len(t, res.FinishedMovements, 1)
		require.Equal(t, movement.StatusCompleted, res.FinishedMovements[0].Status)

		recovered, ok := state.Unit(u.ID)
		require.True(t, ok)
		require.Equal(t, tiles[4], recovered.Tile(), "la unidad aparece en su destino")
		require.Equal(t, unit.StatusIdle, recovered.Status)

		// Y el cierre se propaga a la base de datos.
		for _, f := range res.FinishedMovements {
			require.NoError(t, movements.Finish(ctx, store.Pool(), f.MovementID, f.Status))
		}

		stillActive, err := movements.ListActive(ctx)
		require.NoError(t, err)
		require.Empty(t, stillActive)
	})
}

// ─────────────────────────────────────────────────────────────
// Presencia y protección
// ─────────────────────────────────────────────────────────────

func TestTransicionesDePresenciaSePersisten(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, _, _, bootstrapper := newRepos(store)

	result, err := bootstrapper.Create(ctx, bootstrapRequest("presente", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	cityID := result.City.ID

	clk := clock.NewFakeClock(testEpoch)
	base := clk.Now()

	require.NoError(t, cities.SetPresence(ctx, store.Pool(), cityID, city.PresenceOfflinePending, base))
	got, err := cities.GetByID(ctx, cityID)
	require.NoError(t, err)
	require.Equal(t, city.PresenceOfflinePending, got.PresenceState)
	require.NotNil(t, got.LastOfflineAt)

	// La consulta del tick de timers encuentra la ciudad cuando vence el cooldown.
	pending, err := cities.ListPendingProtection(ctx, base.Add(300*time.Second))
	require.NoError(t, err)
	require.Contains(t, pending, cityID)

	// Pero no antes.
	tooEarly, err := cities.ListPendingProtection(ctx, base.Add(-time.Second))
	require.NoError(t, err)
	require.NotContains(t, tooEarly, cityID)

	require.NoError(t, cities.SetPresence(ctx, store.Pool(), cityID, city.PresenceProtected, base.Add(301*time.Second)))
	got, err = cities.GetByID(ctx, cityID)
	require.NoError(t, err)
	require.True(t, got.IsProtected())

	// Volver a estar en línea limpia la protección.
	require.NoError(t, cities.SetPresence(ctx, store.Pool(), cityID, city.PresenceOnline, base.Add(400*time.Second)))
	got, err = cities.GetByID(ctx, cityID)
	require.NoError(t, err)
	require.Equal(t, city.PresenceOnline, got.PresenceState)
	require.Nil(t, got.ProtectionUntil)
}

// ─────────────────────────────────────────────────────────────
// Volcado por lotes
// ─────────────────────────────────────────────────────────────

func TestFlushPositionsEscribeElLoteYMantieneElChunk(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, units, _, bootstrapper := newRepos(store)

	result, err := bootstrapper.Create(ctx, bootstrapRequest("flusher", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)

	updates := make([]postgres.PositionUpdate, 0, len(result.Units))
	for i, u := range result.Units {
		updates = append(updates, postgres.PositionUpdate{
			UnitID: u.ID,
			X:      int32(40 + i),
			Y:      int32(50 + i),
			Status: unit.StatusIdle,
		})
	}
	require.NoError(t, units.FlushPositions(ctx, store.Pool(), updates))

	reloaded, err := units.ListByPlayer(ctx, result.Player.ID)
	require.NoError(t, err)
	require.Len(t, reloaded, 3)
	for i, u := range reloaded {
		require.EqualValues(t, 40+i, u.X)
		require.EqualValues(t, 50+i, u.Y)
	}

	// El chunk desnormalizado, del que depende el interest management, se
	// mantiene coherente con la posición.
	rows, err := store.Pool().Query(ctx, `SELECT x, y, chunk_x, chunk_y FROM units ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var x, y, cx, cy int32
		require.NoError(t, rows.Scan(&x, &y, &cx, &cy))
		require.Equal(t, x/32, cx, "chunk_x debe derivar de x")
		require.Equal(t, y/32, cy, "chunk_y debe derivar de y")
	}
	require.NoError(t, rows.Err())
}

func TestFlushVacioNoHaceNada(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, units, _, _ := newRepos(store)
	require.NoError(t, units.FlushPositions(ctx, store.Pool(), nil))
}

// ─────────────────────────────────────────────────────────────
// Mundo
// ─────────────────────────────────────────────────────────────

func TestMundoPersistidoCoincideByteAByteConLaSemilla(t *testing.T) {
	store, ctx := newTestStore(t)
	worldRepo := postgres.NewWorldRepo(store)

	want := postgres.State{Seed: 20260909, Width: 64, Height: 64, ChunkSize: 32, EpochMs: testEpoch}
	state, created, err := worldRepo.LoadOrInit(ctx, want)
	require.NoError(t, err)
	require.True(t, created, "la primera vez el mundo se crea")

	terrain := world.Generate(state.Width, state.Height, state.Seed)
	w, err := world.New(state.Width, state.Height, state.ChunkSize, state.Seed, terrain)
	require.NoError(t, err)
	require.NoError(t, worldRepo.SaveChunks(ctx, w))

	count, err := worldRepo.CountChunks(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 4, count, "64/32 = 2 -> 2x2 chunks")

	// INV-WORLD-005: lo persistido coincide exactamente con lo que genera la semilla.
	loaded, err := worldRepo.LoadTerrain(ctx, state.Width, state.Height, state.ChunkSize)
	require.NoError(t, err)
	require.Equal(t, terrain, loaded, "el terreno persistido debe coincidir byte a byte")

	// Segundo arranque: el mundo existente se carga, no se recrea.
	again, created, err := worldRepo.LoadOrInit(ctx, want)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, state.Seed, again.Seed)
}

// Arrancar contra un mundo persistido con otros parámetros pondría unidades sobre
// terreno que no existe: debe fallar en el arranque, no más tarde.
func TestMundoConParametrosDistintosFalla(t *testing.T) {
	store, ctx := newTestStore(t)
	worldRepo := postgres.NewWorldRepo(store)

	_, _, err := worldRepo.LoadOrInit(ctx, postgres.State{
		Seed: 111, Width: 64, Height: 64, ChunkSize: 32, EpochMs: testEpoch,
	})
	require.NoError(t, err)

	_, _, err = worldRepo.LoadOrInit(ctx, postgres.State{
		Seed: 222, Width: 64, Height: 64, ChunkSize: 32, EpochMs: testEpoch,
	})
	require.ErrorContains(t, err, "no coincide con la configuración")
}
