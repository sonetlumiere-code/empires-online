//go:build integration

package postgres_test

import (
	"context"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/loadcheck"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// instanteDeCarga es el reloj del arranque que valida: una hora después del alta.
var instanteDeCarga = instanteDeAlta.Add(time.Hour)

// mundoDeCarga es el mundo de pruebas (64 × 64) de pradera con una franja de
// agua en x = 50.
func mundoDeCarga(t *testing.T) *world.World {
	t.Helper()
	terrain := make([]byte, 64*64)
	for y := 0; y < 64; y++ {
		terrain[y*64+50] = byte(world.Water)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	return w
}

// cargar lee lo que lee cmd/server y lo pasa por la validación de carga.
func cargar(t *testing.T, ctx context.Context, store *postgres.Store, tick uint64) (loadcheck.Result, error) {
	t.Helper()
	_, cities, units, _, _ := newRepos(store)
	vivas, err := units.ListAlive(ctx)
	require.NoError(t, err)
	ciudades, err := cities.ListAll(ctx)
	require.NoError(t, err)
	controles, err := postgres.NewTerritoryRepo(store).LoadControls(ctx)
	require.NoError(t, err)
	eras, err := cities.ListEras(ctx)
	require.NoError(t, err)
	return postgres.NewLoadCheckRepo(store).CheckAndRepair(ctx, loadcheck.Input{
		World:    mundoDeCarga(t),
		EraCaps:  postgres.EraCaps(eras),
		Cities:   ciudades,
		Units:    vivas,
		Controls: controles,
		Now:      instanteDeCarga,
	}, tick)
}

func reparados(t *testing.T, ctx context.Context, store *postgres.Store) []string {
	t.Helper()
	rows, err := store.Pool().Query(ctx,
		`SELECT (payload->>'inv_id') || '@' || (payload->>'entity') || '#' || tick
		 FROM world_events WHERE event_type = $1 ORDER BY id`, postgres.EventInvariantRepaired)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	require.NoError(t, rows.Err())
	return out
}

// EO-119: cada violación que el esquema admite y la ficha declara REPAIR se
// corrige en la base, en una transacción, con un evento de auditoría por
// hallazgo; el arranque siguiente ya no encuentra nada.
func TestLaCargaReparaYAuditaLoQueElEsquemaNoGarantiza(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	seedTerritories(t, ctx, store)
	db := store.Pool()

	alta, err := bootstrapper.Create(ctx, bootstrapRequest("reparable", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	ciudad := alta.City.ID
	ids := make([]int64, 0, len(alta.Units))
	for _, u := range alta.Units {
		ids = append(ids, u.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := db.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	// INV-CITY-003, 017 y 011.
	exec(`UPDATE cities SET population = 1, population_limit = 99,
	      presence_state = 'OFFLINE_PENDING', last_offline_at = NULL WHERE id = $1`, ciudad)
	// INV-UNIT-004.
	exec(`UPDATE units SET max_hp = 60, hp = 55 WHERE id = $1`, ids[0])
	// INV-UNIT-007, en los dos sentidos y con una fila de una unidad muerta.
	exec(`UPDATE units SET status = 'GARRISONED' WHERE id = $1`, ids[1])
	exec(`INSERT INTO garrisons (unit_id, city_id) VALUES ($1, $2)`, ids[2], ciudad)
	var muerta int64
	require.NoError(t, db.QueryRow(ctx,
		`INSERT INTO units (player_id, city_id, unit_type, x, y, hp, max_hp, status, chunk_x, chunk_y)
		 VALUES ($1, $2, 'VILLAGER', 30, 30, 0, 40, 'DEAD', 0, 0) RETURNING id`,
		alta.Player.ID, ciudad).Scan(&muerta))
	exec(`INSERT INTO garrisons (unit_id, city_id) VALUES ($1, $2)`, muerta, ciudad)
	// INV-TERR-005 e INV-TERR-007.
	exec(`UPDATE territory_control SET owner_type = 'PLAYER', owner_id = $1, captured_at = $2
	      WHERE territory_id = 1`, uuid.NewString(), instanteDeAlta)
	exec(`UPDATE territory_control SET owner_type = 'CLAN', owner_id = 'clan-1', captured_at = NULL
	      WHERE territory_id = 2`)

	res, err := cargar(t, ctx, store, 77)
	require.NoError(t, err)
	var hallazgos []string
	for _, f := range res.Findings {
		assert.Equal(t, loadcheck.PolicyRepair, f.Policy, "%+v", f)
		hallazgos = append(hallazgos, f.InvID+"@"+f.Entity)
	}
	u := func(id int64) string { return "unit:" + itoa(id) }
	assert.Equal(t, []string{
		"INV-UNIT-004@" + u(ids[0]),
		"INV-UNIT-007@" + u(ids[1]),
		"INV-UNIT-007@" + u(ids[2]),
		"INV-UNIT-007@" + u(muerta),
		"INV-CITY-017@city:" + itoa(ciudad),
		"INV-CITY-003@city:" + itoa(ciudad),
		"INV-CITY-011@city:" + itoa(ciudad),
		"INV-TERR-005@territory:1",
		"INV-TERR-007@territory:2",
	}, hallazgos)

	// Lo escrito.
	var poblacion, limite int32
	var desconexion *time.Time
	require.NoError(t, db.QueryRow(ctx,
		`SELECT population, population_limit, last_offline_at FROM cities WHERE id = $1`, ciudad).
		Scan(&poblacion, &limite, &desconexion))
	assert.EqualValues(t, 3, poblacion)
	assert.EqualValues(t, 20, limite, "el population_cap de STONE_AGE")
	require.NotNil(t, desconexion)
	assert.True(t, desconexion.Equal(instanteDeCarga))

	var hp, maxHP int32
	require.NoError(t, db.QueryRow(ctx, `SELECT hp, max_hp FROM units WHERE id = $1`, ids[0]).Scan(&hp, &maxHP))
	assert.EqualValues(t, 40, hp)
	assert.EqualValues(t, 40, maxHP)
	estado := func(id int64) string {
		var s string
		require.NoError(t, db.QueryRow(ctx, `SELECT status FROM units WHERE id = $1`, id).Scan(&s))
		return s
	}
	assert.Equal(t, "IDLE", estado(ids[1]))
	assert.Equal(t, "GARRISONED", estado(ids[2]))
	var guarniciones []int64
	rows, err := db.Query(ctx, `SELECT unit_id FROM garrisons ORDER BY unit_id`)
	require.NoError(t, err)
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		guarniciones = append(guarniciones, id)
	}
	rows.Close()
	assert.Equal(t, []int64{ids[2]}, guarniciones, "la fila de la muerta se borra")

	var dueno string
	var duenoID *string
	var captura *time.Time
	var version int32
	require.NoError(t, db.QueryRow(ctx,
		`SELECT owner_type, owner_id, captured_at, version FROM territory_control WHERE territory_id = 1`).
		Scan(&dueno, &duenoID, &captura, &version))
	assert.Equal(t, "NONE", dueno)
	assert.Nil(t, duenoID)
	assert.Nil(t, captura)
	assert.EqualValues(t, 1, version)
	require.NoError(t, db.QueryRow(ctx,
		`SELECT captured_at FROM territory_control WHERE territory_id = 2`).Scan(&captura))
	require.NotNil(t, captura)
	assert.True(t, captura.Equal(instanteDeCarga))

	// La auditoría: un evento por hallazgo, con el tick del arranque.
	eventos := reparados(t, ctx, store)
	require.Len(t, eventos, len(hallazgos))
	for i, h := range hallazgos {
		assert.Equal(t, h+"#77", eventos[i])
	}

	// El arranque siguiente no encuentra nada ni escribe nada.
	res, err = cargar(t, ctx, store, 78)
	require.NoError(t, err)
	assert.Empty(t, res.Findings)
	assert.Len(t, reparados(t, ctx, store), len(hallazgos))
}

// Con un hallazgo FAIL_FAST, ni siquiera lo reparable se escribe.
func TestUnFatalAbortaLaCargaSinEscribirNada(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	db := store.Pool()

	alta, err := bootstrapper.Create(ctx, bootstrapRequest("varada", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE cities SET population_limit = 99 WHERE id = $1`, alta.City.ID)
	require.NoError(t, err)
	// INV-UNIT-001: una unidad en el agua.
	_, err = db.Exec(ctx, `UPDATE units SET x = 50, chunk_x = 1 WHERE id = $1`, alta.Units[0].ID)
	require.NoError(t, err)

	res, err := cargar(t, ctx, store, 5)
	require.ErrorIs(t, err, postgres.ErrLoadCheckFatal)
	assert.True(t, res.Fatal())

	var limite int32
	require.NoError(t, db.QueryRow(ctx, `SELECT population_limit FROM cities WHERE id = $1`, alta.City.ID).Scan(&limite))
	assert.EqualValues(t, 99, limite, "lo reparable tampoco se escribe")
	assert.Empty(t, reparados(t, ctx, store))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
