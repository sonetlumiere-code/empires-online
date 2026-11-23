//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/founding"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Integración de la caída de una ciudad (SPEC-CITY-FALL §9 «integration»,
// ADR-050): el asalto con su fila y su único, la caída y la refundación en una
// transacción, lo que cuelga de la ciudad, el índice de una ciudad en pie por
// dueño y el trigger que no deja levantar una caída, contra PostgreSQL real.
//
// Mundo de 64 × 64 con cuatro territorios de 32 × 32. El vencido funda en
// (10,10) y el vencedor en (50,50), cada uno en su capital; la ciudad nueva del
// vencido nace en (10,50), en el territorio libre del suroeste.

type caidaDePrueba struct {
	repo              *postgres.CityFallRepo
	cities            *postgres.CityRepo
	territorios       *postgres.TerritoryRepo
	vencido, vencedor *postgres.BootstrapResult
	sembrados         []territory.Territory
	ahora             time.Time
}

func nuevaCaida(t *testing.T, ctx context.Context, store *postgres.Store) caidaDePrueba {
	t.Helper()
	terr, sembrados := seedTerritories(t, ctx, store)
	_, cities, units, _, bootstrapper := newRepos(store)
	alta := func(nombre string, centro world.Tile) *postgres.BootstrapResult {
		req := bootstrapRequest(nombre, centro)
		req.TerritoryID = territorioEn(t, sembrados, centro.X, centro.Y).ID
		r, err := bootstrapper.Create(ctx, req)
		require.NoError(t, err)
		return r
	}
	return caidaDePrueba{
		repo: postgres.NewCityFallRepo(store, cities, units, terr), cities: cities, territorios: terr,
		vencido: alta("vencido", world.Tile{X: 10, Y: 10}), vencedor: alta("vencedor", world.Tile{X: 50, Y: 50}),
		sembrados: sembrados, ahora: time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC),
	}
}

func (c caidaDePrueba) asalto(req string) postgres.AssaultStart {
	return postgres.AssaultStart{
		Assault: cityfall.Assault{
			CityID: c.vencido.City.ID, UnitID: c.vencedor.Units[0].ID, PlayerID: c.vencedor.Player.ID,
			FromCityID: c.vencedor.City.ID, X: 12, Y: 10, StartedAtMs: 1_000, EndsAtMs: 901_000, RequestID: req,
		},
		Owner: c.vencido.Player.ID, Tick: 7,
	}
}

func (c caidaDePrueba) caida(t *testing.T, assaultID int64, sitio world.Tile) cityfall.Fall {
	return cityfall.Fall{
		AssaultID: assaultID, CityID: c.vencido.City.ID, OwnerID: c.vencido.Player.ID,
		VictorID: c.vencedor.Player.ID, VictorCityID: c.vencedor.City.ID, Name: c.vencido.City.Name,
		Era: c.vencido.City.Era, Center: c.vencido.City.Center(), Tick: 9, Now: c.ahora, AtMs: c.ahora.UnixMilli(),
		Place: func(existing []world.Tile) (cityfall.Site, error) {
			assert.NotContains(t, existing, c.vencido.City.Center(), "la caída no cuenta entre las ciudades en pie")
			return cityfall.Site{Center: sitio, TerritoryID: territorioEn(t, c.sembrados, sitio.X, sitio.Y).ID,
				Spawns: []world.Tile{{X: sitio.X + 2, Y: sitio.Y}, {X: sitio.X - 2, Y: sitio.Y}, {X: sitio.X, Y: sitio.Y + 2}}}, nil
		},
		Verify: founding.FarEnough,
	}
}

// ADR-050 §2: el asalto se escribe una vez por petición, con su evento al
// asaltado, y el índice parcial no deja un segundo asalto activo sobre la
// misma ciudad (INV-CITY-021).
func TestElAsaltoSeEscribeUnaVezYUnoPorCiudad(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)

	id, err := c.repo.StartAssault(ctx, c.asalto("r1"))
	require.NoError(t, err)
	otra, err := c.repo.StartAssault(ctx, c.asalto("r1"))
	require.NoError(t, err)
	assert.Equal(t, id, otra, "un reintento de la cola devuelve la misma fila")

	segundo := c.asalto("r2")
	segundo.Assault.UnitID = c.vencedor.Units[1].ID
	_, err = c.repo.StartAssault(ctx, segundo)
	assert.ErrorIs(t, err, postgres.ErrConflict, "INV-CITY-021")

	activos, err := c.repo.ListActiveAssaults(ctx)
	require.NoError(t, err)
	require.Len(t, activos, 1)
	assert.Equal(t, int64(901_000), activos[0].EndsAtMs)

	var n int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM world_events WHERE event_type = 'CityAssaultStarted'
		   AND player_id = $1 AND counterpart_player_id = $2`, c.vencido.Player.ID, c.vencedor.Player.ID).Scan(&n))
	assert.Equal(t, 1, n)

	require.NoError(t, c.repo.EndAssault(ctx, postgres.AssaultEnd{AssaultID: id, Reason: cityfall.EndUnit}))
	activos, err = c.repo.ListActiveAssaults(ctx)
	require.NoError(t, err)
	assert.Empty(t, activos)
}

// ADR-050 §3, RN-CITY-026 a RN-CITY-028: la caída arrasa la ciudad (fallen_at,
// edificios derribados, encargos borrados, almacén vacío), libera su
// territorio capital, funda la nueva con tres aldeanos, le pasa las unidades
// vivas y escribe CityFell a los dos, todo en una transacción. Después, la
// ciudad del vencido es la nueva para todas las consultas (INV-CITY-020).
func TestLaCaidaArrasaYRefundaEnUnaTransaccion(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	pool := store.Pool()
	cid := c.vencido.City.ID
	_, err := pool.Exec(ctx, `INSERT INTO buildings (city_id, type, x, y, status, completes_at_ms, request_id)
	                          VALUES ($1, 'HOUSE', 9, 9, 'COMPLETE', 1, 'casa')`, cid)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE city_stocks SET amount = 80 WHERE city_id = $1 AND resource = 'WOOD'`, cid)
	require.NoError(t, err)

	id, err := c.repo.StartAssault(ctx, c.asalto("r1"))
	require.NoError(t, err)
	res, err := c.repo.FallCity(ctx, c.caida(t, id, world.Tile{X: 10, Y: 50}))
	require.NoError(t, err)

	require.NotNil(t, res.City)
	assert.Equal(t, world.Tile{X: 10, Y: 50}, res.City.Center())
	assert.Equal(t, c.vencido.City.Name, res.City.Name)
	assert.Equal(t, c.ahora.UnixMilli(), res.City.FoundedAtMs, "la inmunidad empieza con la refundación")
	assert.Len(t, res.Villagers, 3)
	assert.Equal(t, int32(6), res.City.Population, "tres aldeanos nuevos y los tres de la caída")
	require.NotNil(t, res.Freed, "su capital queda libre")
	assert.Equal(t, territory.OwnerNone, res.Freed.OwnerType)
	require.NotNil(t, res.Claimed, "el territorio libre de la nueva pasa a ser suyo")

	var caida *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT fallen_at FROM cities WHERE id = $1`, cid).Scan(&caida))
	require.NotNil(t, caida)
	var enPie, madera, encargo int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM buildings WHERE city_id = $1 AND status <> 'DESTROYED'`, cid).Scan(&enPie))
	assert.Zero(t, enPie)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT amount FROM city_stocks WHERE city_id = $1 AND resource = 'WOOD'`, cid).Scan(&madera))
	assert.Zero(t, madera, "el almacén arde (Q5)")
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM units WHERE city_id = $1 AND status <> 'DEAD'`, cid).Scan(&encargo))
	assert.Zero(t, encargo, "ninguna unidad viva se queda en la caída")
	var estado string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM city_assaults WHERE id = $1`, id).Scan(&estado))
	assert.Equal(t, "FELL", estado)

	suya, err := c.cities.GetByOwner(ctx, c.vencido.Player.ID)
	require.NoError(t, err)
	assert.Equal(t, res.City.ID, suya.ID, "INV-CITY-020: la ciudad del jugador es la nueva")
	centros, err := c.cities.ListCenters(ctx, pool)
	require.NoError(t, err)
	assert.NotContains(t, centros, world.Tile{X: 10, Y: 10})
	todas, err := c.cities.ListAll(ctx)
	require.NoError(t, err)
	assert.Len(t, todas, 2, "la caída no vuelve a la RAM")

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM world_events WHERE event_type = 'CityFell'
		   AND player_id = $1 AND counterpart_player_id = $2 AND (payload->>'newCityId')::bigint = $3`,
		c.vencido.Player.ID, c.vencedor.Player.ID, res.City.ID).Scan(&n))
	assert.Equal(t, 1, n)

	// Repetirla es un conflicto y no escribe nada (el asalto ya no está activo).
	_, err = c.repo.FallCity(ctx, c.caida(t, id, world.Tile{X: 40, Y: 10}))
	assert.ErrorIs(t, err, postgres.ErrConflict)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM cities WHERE owner_player_id = $1`, c.vencido.Player.ID).Scan(&n))
	assert.Equal(t, 2, n)
}

// INV-CITY-020 en la base: el índice parcial no deja dos ciudades en pie del
// mismo dueño, y el trigger no deja levantar una caída.
func TestLaBaseNoDejaDosCiudadesEnPieNiLevantarUnaCaida(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	pool := store.Pool()

	_, err := pool.Exec(ctx,
		`INSERT INTO cities (owner_player_id, name, center_x, center_y, era, population_limit, last_online_at)
		 VALUES ($1, 'otra', 40, 40, 'STONE_AGE', 20, now())`, c.vencido.Player.ID)
	require.Error(t, err, "dos ciudades en pie del mismo dueño")
	assert.True(t, postgres.IsUniqueViolation(err))

	id, err := c.repo.StartAssault(ctx, c.asalto("r1"))
	require.NoError(t, err)
	_, err = c.repo.FallCity(ctx, c.caida(t, id, world.Tile{X: 10, Y: 50}))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE cities SET fallen_at = NULL WHERE id = $1`, c.vencido.City.ID)
	require.Error(t, err, "una caída no vuelve a estar en pie")
}

// ADR-050 §3, con un fallo inyectado: si la búsqueda del sitio falla, o si
// cada intento lo pisa otra fundación, no se escribe nada: la ciudad sigue en
// pie, con su asalto activo y su dueño con una sola ciudad.
func TestUnaCaidaQueFallaNoDejaNadaAMedias(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	pool := store.Pool()
	id, err := c.repo.StartAssault(ctx, c.asalto("r1"))
	require.NoError(t, err)

	sinSitio := c.caida(t, id, world.Tile{X: 10, Y: 50})
	sinSitio.Place = func([]world.Tile) (cityfall.Site, error) { return cityfall.Site{}, founding.ErrNoSite }
	_, err = c.repo.FallCity(ctx, sinSitio)
	assert.True(t, errors.Is(err, founding.ErrNoSite))

	pisado := c.caida(t, id, world.Tile{X: 10, Y: 50})
	pisado.Verify = func(world.Tile, []world.Tile) bool { return false }
	_, err = c.repo.FallCity(ctx, pisado)
	assert.ErrorIs(t, err, postgres.ErrPlacementContended)

	var caida *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT fallen_at FROM cities WHERE id = $1`, c.vencido.City.ID).Scan(&caida))
	assert.Nil(t, caida)
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM cities WHERE owner_player_id = $1`, c.vencido.Player.ID).Scan(&n))
	assert.Equal(t, 1, n)
	activos, err := c.repo.ListActiveAssaults(ctx)
	require.NoError(t, err)
	assert.Len(t, activos, 1)
}
