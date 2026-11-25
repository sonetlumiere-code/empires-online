//go:build integration

package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// SPEC-CITY-TAKEN (ADR-052, INV-CITY-024, RN-CITY-036 a RN-CITY-039): con el
// vencedor en una sola ciudad, la caída inserta su tomada en el mismo centro, le
// pasa la casa completa (y su población) y destruye la obra, deja el almacén a
// cero, le da tres aldeanos y el territorio libre del centro, y el vencido
// refunda como siempre. Una segunda tomada de la misma caída la impide el índice.
func TestLaCaidaConHuecoTomaLaCiudad(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	pool := store.Pool()
	cid := c.vencido.City.ID
	var casa, obra int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO buildings (city_id, type, x, y, status, completes_at_ms, request_id)
	                          VALUES ($1, 'HOUSE', 9, 9, 'COMPLETE', 1, 'casa') RETURNING id`, cid).Scan(&casa))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO buildings (city_id, type, x, y, status, completes_at_ms, request_id)
	                          VALUES ($1, 'HOUSE', 11, 9, 'UNDER_CONSTRUCTION', 999999, 'obra') RETURNING id`, cid).Scan(&obra))
	_, err := pool.Exec(ctx, `UPDATE city_stocks SET amount = 80 WHERE city_id = $1 AND resource = 'WOOD'`, cid)
	require.NoError(t, err)

	id, err := c.repo.StartAssault(ctx, c.asalto("r1"))
	require.NoError(t, err)
	caida := c.caida(t, id, world.Tile{X: 10, Y: 50})
	caida.TakeSpawns = []world.Tile{{X: 12, Y: 10}, {X: 8, Y: 10}, {X: 10, Y: 12}}
	res, err := c.repo.FallCity(ctx, caida)
	require.NoError(t, err)
	require.NotNil(t, res.Taken, "con hueco, el vencedor la toma")
	require.NotNil(t, res.City, "el vencido refunda como siempre")
	tomada := res.Taken
	assert.Equal(t, c.vencedor.Player.ID, tomada.OwnerPlayerID)
	assert.Equal(t, c.vencido.City.Center(), tomada.Center())
	assert.Equal(t, c.vencido.City.Name, tomada.Name)
	assert.Equal(t, int16(2), tomada.Slot)
	assert.Equal(t, []int64{casa}, res.TakenBuildings)
	assert.Len(t, res.TakenVillagers, 3)
	assert.Equal(t, int32(3), tomada.Population)
	require.NotNil(t, res.TakenClaimed, "el territorio del centro, libre tras la caída, es de la tomada")
	assert.Equal(t, c.vencedor.Player.ID.String(), *res.TakenClaimed.OwnerID)

	var cap, limite int32
	require.NoError(t, pool.QueryRow(ctx, `SELECT population_cap FROM eras WHERE code = $1`, string(tomada.Era)).Scan(&cap))
	require.NoError(t, pool.QueryRow(ctx, `SELECT population_limit FROM cities WHERE id = $1`, tomada.ID).Scan(&limite))
	assert.Equal(t, cap+5, limite, "el límite de su era más su casa (INV-ECON-004)")

	var ciudadCasa int64
	var estadoObra string
	require.NoError(t, pool.QueryRow(ctx, `SELECT city_id FROM buildings WHERE id = $1`, casa).Scan(&ciudadCasa))
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM buildings WHERE id = $1`, obra).Scan(&estadoObra))
	assert.Equal(t, tomada.ID, ciudadCasa)
	assert.Equal(t, "DESTROYED", estadoObra)
	var enPie int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM buildings WHERE city_id = $1 AND status <> 'DESTROYED'`, cid).Scan(&enPie))
	assert.Zero(t, enPie, "INV-CITY-024: la caída no conserva edificios en pie")
	var madera int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT coalesce(sum(amount), 0) FROM city_stocks WHERE city_id = $1`, tomada.ID).Scan(&madera))
	assert.Zero(t, madera, "el almacén arde (Q3)")
	var desde *int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT taken_from_city_id FROM cities WHERE id = $1`, tomada.ID).Scan(&desde))
	require.NotNil(t, desde)
	assert.Equal(t, cid, *desde)
	var tomadaEnEvento int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT (payload->>'takenCityId')::bigint FROM world_events WHERE event_type = 'CityFell' AND (payload->>'cityId')::bigint = $1`,
		cid).Scan(&tomadaEnEvento))
	assert.Equal(t, tomada.ID, tomadaEnEvento)

	// INV-CITY-024: una caída se toma como mucho una vez.
	_, err = pool.Exec(ctx, `UPDATE cities SET taken_from_city_id = $2 WHERE id = $1`, c.vencedor.City.ID, cid)
	assert.Error(t, err, "el índice único de taken_from_city_id")
}

// RN-CITY-036: con el vencedor en dos ciudades, la caída arrasa como siempre.
func TestSinHuecoLaCaidaNoToma(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	pool := store.Pool()
	_, err := pool.Exec(ctx, `INSERT INTO cities (owner_player_id, name, center_x, center_y, era, population, population_limit,
	                           presence_state, slot) VALUES ($1, 'vencedor II', 50, 10, 'BRONZE_AGE', 0, 20, 'ONLINE', 2)`,
		c.vencedor.Player.ID)
	require.NoError(t, err)
	id, err := c.repo.StartAssault(ctx, c.asalto("r1"))
	require.NoError(t, err)
	caida := c.caida(t, id, world.Tile{X: 10, Y: 50})
	caida.TakeSpawns = []world.Tile{{X: 12, Y: 10}, {X: 8, Y: 10}, {X: 10, Y: 12}}
	res, err := c.repo.FallCity(ctx, caida)
	require.NoError(t, err)
	assert.Nil(t, res.Taken)
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM cities WHERE owner_player_id = $1 AND fallen_at IS NULL`, c.vencedor.Player.ID).Scan(&n))
	assert.Equal(t, 2, n)
}
