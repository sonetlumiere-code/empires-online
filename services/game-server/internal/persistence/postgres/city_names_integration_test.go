//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Integración del nombre de las ciudades (SPEC-CITY-NAMES §9 «integration»,
// INV-CITY-025): la fundación, la toma y la refundación eligen el nombre en su
// transacción con las ciudades en pie del dueño. Usa el mundo de nuevaCaida.

// nombresEnPie son los nombres de las ciudades en pie de owner, por id.
func nombresEnPie(t *testing.T, ctx context.Context, store *postgres.Store, owner uuid.UUID) []string {
	t.Helper()
	rows, err := store.Pool().Query(ctx,
		`SELECT name FROM cities WHERE owner_player_id = $1 AND fallen_at IS NULL ORDER BY id`, owner)
	require.NoError(t, err)
	defer rows.Close()
	var nombres []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		nombres = append(nombres, n)
	}
	require.NoError(t, rows.Err())
	return nombres
}

// RN-CITY-044: la capital se llama «vencidopolis II» (cayó la original y la
// segunda pasó a capital). La fundada recupera la base, no «… II II».
func TestLaFundacionTrasCaerLaCapitalRecuperaElNombreBase(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	desde := c.vencido.City.ID
	_, err := store.Pool().Exec(ctx, `UPDATE cities SET name = 'vencidopolis II' WHERE id = $1`, desde)
	require.NoError(t, err)

	vm, vp := conAlmacen(t, ctx, store, desde, 800, 400)
	res, err := c.repo.FoundCity(ctx, c.fundacion(t), cobro(desde, vm, vp))
	require.NoError(t, err)
	require.NotNil(t, res.City)
	assert.Equal(t, "vencidopolis", res.City.Name)
	assert.Equal(t, []string{"vencidopolis II", "vencidopolis"}, nombresEnPie(t, ctx, store, c.vencido.Player.ID),
		"INV-CITY-025: la base guarda el nombre elegido")
}

// RN-CITY-044, INV-CITY-025: con «vencidopolis» y «vencidopolis III» en pie
// (cayó la II), la fundada es «vencidopolis II», no otra «III».
func TestLaFundacionTrasCaerLaDelMedioTomaSuNombre(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	pool := store.Pool()
	desde := c.vencido.City.ID
	vm, vp := conAlmacen(t, ctx, store, desde, 800, 400)
	_, err := pool.Exec(ctx, `UPDATE cities SET era = 'IRON_AGE' WHERE id = $1`, desde)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO cities (owner_player_id, name, center_x, center_y, era, population, population_limit,
	                           presence_state, slot) VALUES ($1, 'vencidopolis III', 40, 10, 'BRONZE_AGE', 0, 20, 'ONLINE', 3)`,
		c.vencido.Player.ID)
	require.NoError(t, err)

	res, err := c.repo.FoundCity(ctx, c.fundacion(t), cobro(desde, vm, vp))
	require.NoError(t, err)
	require.NotNil(t, res.City)
	assert.Equal(t, "vencidopolis II", res.City.Name)
	assert.Equal(t, int16(2), res.City.Slot)
}

// RN-CITY-045, INV-CITY-025: el vencedor ya tiene en pie una «vencidopolis»
// (se la tomó antes): la tomada es «vencidopolis II». Sin repetirse conserva el
// nombre de la caída (TestLaCaidaConHuecoTomaLaCiudad).
func TestLaTomadaConUnNombreRepetidoTomaElSiguiente(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	pool := store.Pool()
	_, err := pool.Exec(ctx, `UPDATE cities SET era = 'IRON_AGE' WHERE id = $1`, c.vencedor.City.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO cities (owner_player_id, name, center_x, center_y, era, population, population_limit,
	                           presence_state, slot) VALUES ($1, 'vencidopolis', 50, 10, 'BRONZE_AGE', 0, 20, 'ONLINE', 2)`,
		c.vencedor.Player.ID)
	require.NoError(t, err)
	id, err := c.repo.StartAssault(ctx, c.asalto("r1"))
	require.NoError(t, err)
	caida := c.caida(t, id, world.Tile{X: 10, Y: 50})
	caida.TakeSpawns = []world.Tile{{X: 12, Y: 10}, {X: 8, Y: 10}, {X: 10, Y: 12}}

	res, err := c.repo.FallCity(ctx, caida)
	require.NoError(t, err)
	require.NotNil(t, res.Taken)
	assert.Equal(t, "vencidopolis II", res.Taken.Name)
	assert.Equal(t, []string{"vencedorpolis", "vencidopolis", "vencidopolis II"},
		nombresEnPie(t, ctx, store, c.vencedor.Player.ID))
}

// RN-CITY-046: la última ciudad en pie del vencido se llama «vencidopolis III»;
// al caer, la refundada lleva su nombre base.
func TestLaRefundadaLlevaElNombreBase(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	_, err := store.Pool().Exec(ctx, `UPDATE cities SET name = 'vencidopolis III' WHERE id = $1`, c.vencido.City.ID)
	require.NoError(t, err)
	id, err := c.repo.StartAssault(ctx, c.asalto("r1"))
	require.NoError(t, err)
	caida := c.caida(t, id, world.Tile{X: 10, Y: 50})
	caida.Name = "vencidopolis III"

	res, err := c.repo.FallCity(ctx, caida)
	require.NoError(t, err)
	require.NotNil(t, res.City, "refunda")
	assert.Equal(t, "vencidopolis", res.City.Name)
	assert.Equal(t, []string{"vencidopolis"}, nombresEnPie(t, ctx, store, c.vencido.Player.ID))
}
