//go:build integration

package postgres_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/game/founding"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// codigoDe devuelve el SQLSTATE de un error de PostgreSQL.
func codigoDe(t *testing.T, err error) string {
	t.Helper()
	require.Error(t, err)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "se esperaba un error de PostgreSQL, llegó %v", err)
	return pgErr.Code
}

// La mitad DB de INV-CITY-001, 002, 003, 004 y 008: cada fila inválida la
// rechaza el motor con el nombre exacto de la restricción, y no deja rastro.
func TestLasRestriccionesDeCiudadesRechazanFilasInvalidas(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	db := store.Pool()

	alta, err := bootstrapper.Create(ctx, bootstrapRequest("fundadora", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	duena := alta.Player.ID

	insertar := func(owner any, x int32, era string, population, limit int32, presence string) error {
		_, err := db.Exec(ctx,
			`INSERT INTO cities (owner_player_id, name, center_x, center_y, era,
			                     population, population_limit, presence_state)
			 VALUES ($1, 'prueba', $2, 40, $3, $4, $5, $6)`,
			owner, x, era, population, limit, presence)
		return err
	}

	// INV-CITY-001: sin dueño, o con un dueño que no existe.
	assert.Equal(t, "23502", codigoDe(t, insertar(nil, 40, "STONE_AGE", 0, 20, "ONLINE")), "NOT NULL")
	assert.Equal(t, "cities_owner_player_id_fkey",
		constraintDe(t, insertar(uuid.New(), 40, "STONE_AGE", 0, 20, "ONLINE")))

	// INV-CITY-002: la población, entre 0 y el límite.
	assert.Equal(t, "cities_population_within_limit",
		constraintDe(t, insertar(duena, 40, "STONE_AGE", 21, 20, "ONLINE")))
	assert.Equal(t, "cities_population_check",
		constraintDe(t, insertar(duena, 40, "STONE_AGE", -1, 20, "ONLINE")))

	// INV-CITY-003, mitad DB: la era existe en el catálogo y el límite no es negativo.
	assert.Equal(t, "cities_era_fkey",
		constraintDe(t, insertar(duena, 40, "SPACE_AGE", 0, 20, "ONLINE")))
	assert.Equal(t, "cities_population_limit_check",
		constraintDe(t, insertar(duena, 40, "STONE_AGE", 0, -1, "ONLINE")))

	// INV-CITY-004: el dominio de presence_state es cerrado, sin variantes de
	// capitalización ni cadena vacía.
	for _, invalido := range []string{"online", "", "AWAY"} {
		assert.Equal(t, "cities_presence_state_valid",
			constraintDe(t, insertar(duena, 40, "STONE_AGE", 0, 20, invalido)), "%q", invalido)
	}

	// INV-CITY-008: dos ciudades no comparten centro.
	_, err = db.Exec(ctx,
		`INSERT INTO cities (owner_player_id, name, center_x, center_y, era, population_limit)
		 VALUES ($1, 'gemela', 20, 20, 'STONE_AGE', 20)`, duena)
	assert.Equal(t, "cities_unique_center", constraintDe(t, err))

	var ciudades int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM cities`).Scan(&ciudades))
	assert.Equal(t, 1, ciudades, "las filas rechazadas no dejan rastro")
}

// INV-CITY-003: el límite de población de una ciudad nueva lo fija su era del
// catálogo, dentro de la transacción del alta; la petición no lo trae.
func TestElAltaTomaElLimiteDePoblacionDeSuEra(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, _, _, bootstrapper := newRepos(store)

	eras, err := cities.ListEras(ctx)
	require.NoError(t, err)
	require.Len(t, eras, 4, "el catálogo sembrado por 000002_seed_catalogs")

	for i, era := range eras {
		req := bootstrapRequest("era_"+string(rune('a'+i)), world.Tile{X: int32(10 + 30*i), Y: 20})
		req.Era = era.Code
		alta, err := bootstrapper.Create(ctx, req)
		require.NoError(t, err, "era %s", era.Code)

		persistida, err := cities.GetByID(ctx, alta.City.ID)
		require.NoError(t, err)
		assert.Equal(t, era.PopulationCap, persistida.PopulationLimit, "era %s", era.Code)
		assert.Equal(t, era.PopulationCap, alta.City.PopulationLimit, "la RAM recibe el mismo límite")
	}
}

func TestUnAltaConUnaEraInexistenteNoEscribeNada(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, bootstrapper := newRepos(store)

	req := bootstrapRequest("anacronica", world.Tile{X: 20, Y: 20})
	req.Era = "SPACE_AGE"
	_, err := bootstrapper.Create(ctx, req)
	require.ErrorIs(t, err, postgres.ErrNotFound)

	_, _, err = players.GetByUsername(ctx, "anacronica")
	assert.Error(t, err, "el jugador tampoco existe: el alta es atómica")
}

// La primera marca de presencia la sella el reloj del llamante, no el now()
// del servidor de base de datos: la presencia se evalúa contra el reloj del
// game loop. Y la población es el recuento de unidades vivas (INV-CITY-009).
func TestLaCiudadNaceConLaMarcaDelRelojYSuPoblacionReal(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, _, _, bootstrapper := newRepos(store)

	alta, err := bootstrapper.Create(ctx, bootstrapRequest("puntual", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)

	persistida, err := cities.GetByID(ctx, alta.City.ID)
	require.NoError(t, err)
	require.NotNil(t, persistida.LastOnlineAt)
	assert.True(t, persistida.LastOnlineAt.Equal(instanteDeAlta), "llegó %s", persistida.LastOnlineAt)

	var vivas int32
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM units WHERE city_id = $1 AND status <> 'DEAD'`, alta.City.ID).Scan(&vivas))
	assert.Equal(t, vivas, persistida.Population)
}

// INV-CITY-016: rehidratar no escribe presencia. Dos arranques seguidos sobre
// la misma base dejan presence_state y las dos marcas exactamente como estaban.
func TestRehidratarDosVecesNoEscribePresencia(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, _, _, bootstrapper := newRepos(store)
	clk := clock.NewFakeClock(testEpoch)

	alta, err := bootstrapper.Create(ctx, bootstrapRequest("dormida", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	require.NoError(t, cities.SetPresence(ctx, store.Pool(), alta.City.ID, city.PresenceOfflinePending, clk.Now()))
	antes, err := cities.GetByID(ctx, alta.City.ID)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		clk.AdvanceMs(60_000)
		_, p := arrancar(t, ctx, store, testWorld(t), clk)
		assert.Empty(t, p.jobs, "el arranque no encola ninguna escritura")
	}

	despues, err := cities.GetByID(ctx, alta.City.ID)
	require.NoError(t, err)
	assert.Equal(t, antes.PresenceState, despues.PresenceState)
	assert.Equal(t, antes.LastOnlineAt, despues.LastOnlineAt)
	assert.Equal(t, antes.LastOfflineAt, despues.LastOfflineAt)
	assert.Equal(t, antes.Version, despues.Version, "ninguna escritura sobre cities")
}

// INV-CITY-008 bajo concurrencia. Seis altas simultáneas con la MISMA semilla,
// el peor caso posible: todas buscan el mismo sitio a la vez. Cada una busca fuera de la transacción y vuelve a
// comprobar su centro dentro, bajo el cerrojo de fundación; si otra se adelantó,
// repite la búsqueda. Sin esa comprobación, varias elegirían el mismo centro (y
// fallarían por cities_unique_center) o centros a menos de 24 tiles, que el
// esquema no puede detectar.
//
// Con la misma semilla, en cada ronda sólo gana una, así que algunas agotan sus
// intentos. Ese es el fallo aceptable: ErrPlacementContended, sin escribir
// nada. Lo que no puede ocurrir es otro error ni una ciudad demasiado cerca.
func TestAltasSimultaneasGuardanLaDistanciaMinima(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, _, _, bootstrapper := newRepos(store)

	terrain := make([]byte, 128*128)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	mundo, err := world.New(128, 128, 32, 1, terrain)
	require.NoError(t, err)
	const semilla = 64*128 + 64 // todas empiezan a buscar en (64,64)

	const altas = 6
	var wg sync.WaitGroup
	errs := make([]error, altas)
	inicio := make(chan struct{})
	for i := 0; i < altas; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := bootstrapRequest(fmt.Sprintf("simultanea_%d", i), world.Tile{})
			req.CityCenter, req.VillagerSpawns = world.Tile{}, nil
			req.Place = func(existing []world.Tile) (postgres.Placement, error) {
				s, err := founding.FindSite(mundo, existing, semilla, 3)
				if err != nil {
					return postgres.Placement{}, err
				}
				return postgres.Placement{Center: s.Center, Spawns: s.Spawns}, nil
			}
			req.Verify = founding.FarEnough
			<-inicio
			_, errs[i] = bootstrapper.Create(ctx, req)
		}(i)
	}
	close(inicio)
	wg.Wait()
	confirmadas := 0
	for i, err := range errs {
		if err == nil {
			confirmadas++
			continue
		}
		require.ErrorIs(t, err, postgres.ErrPlacementContended, "alta %d", i)
	}
	require.GreaterOrEqual(t, confirmadas, 3, "cada ronda confirma al menos una")

	todas, err := cities.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, todas, confirmadas, "las altas fallidas no dejan ciudad")
	for i := range todas {
		for j := i + 1; j < len(todas); j++ {
			a, b := todas[i], todas[j]
			dx, dy := a.CenterX-b.CenterX, a.CenterY-b.CenterY
			if dx < 0 {
				dx = -dx
			}
			if dy < 0 {
				dy = -dy
			}
			assert.GreaterOrEqual(t, max(dx, dy), int32(24),
				"ciudades %d y %d a distancia Chebyshev %d", a.ID, b.ID, max(dx, dy))
		}
	}
}

// Si otra alta confirma un sitio incompatible entre la búsqueda y la
// transacción, la comprobación de dentro lo detecta y la búsqueda se repite
// con los centros nuevos. Aquí la primera búsqueda ignora a propósito la
// ciudad que ya existe, que es lo que vería una alta que buscó un instante
// antes de que la otra confirmara.
func TestUnSitioQueOtraAltaOcupoSeVuelveABuscar(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, _, _, bootstrapper := newRepos(store)

	_, err := bootstrapper.Create(ctx, bootstrapRequest("primera", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)

	var vistos [][]world.Tile
	req := bootstrapRequest("rezagada", world.Tile{})
	req.Place = func(existing []world.Tile) (postgres.Placement, error) {
		vistos = append(vistos, existing)
		centro := world.Tile{X: 25, Y: 25} // a 5 tiles de la primera
		if len(vistos) > 1 {
			centro = world.Tile{X: 50, Y: 50}
		}
		return postgres.Placement{Center: centro, Spawns: []world.Tile{{X: centro.X + 2, Y: centro.Y}}}, nil
	}
	req.Verify = founding.FarEnough

	alta, err := bootstrapper.Create(ctx, req)
	require.NoError(t, err)
	assert.Len(t, vistos, 2, "un reintento")
	assert.Equal(t, int32(50), alta.City.CenterX)

	todas, err := cities.ListAll(ctx)
	require.NoError(t, err)
	assert.Len(t, todas, 2, "el intento rechazado no dejó nada")
}

// Si el sitio sigue ocupado en cada intento, el alta falla con
// ErrPlacementContended y no escribe nada: nunca funda a menos de la distancia
// mínima por haberse cansado de buscar.
func TestUnSitioSiempreOcupadoAgotaLosIntentosSinEscribir(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, bootstrapper := newRepos(store)

	_, err := bootstrapper.Create(ctx, bootstrapRequest("ocupante", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)

	llamadas := 0
	req := bootstrapRequest("insistente", world.Tile{})
	req.Place = func([]world.Tile) (postgres.Placement, error) {
		llamadas++
		return postgres.Placement{Center: world.Tile{X: 22, Y: 22}, Spawns: []world.Tile{{X: 24, Y: 22}}}, nil
	}
	req.Verify = founding.FarEnough

	_, err = bootstrapper.Create(ctx, req)
	require.ErrorIs(t, err, postgres.ErrPlacementContended)
	assert.Equal(t, 3, llamadas)
	_, _, err = players.GetByUsername(ctx, "insistente")
	assert.Error(t, err, "ni rastro del jugador")
}
