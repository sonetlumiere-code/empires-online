//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// La mitad DB de INV-UNIT-004 y de INV-UNIT-006, y el dominio cerrado de
// units.status: cada fila inválida la rechaza el motor con el nombre exacto de
// la restricción, y no deja rastro.
func TestLasRestriccionesDeUnidadesRechazanFilasInvalidas(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	db := store.Pool()

	alta, err := bootstrapper.Create(ctx, bootstrapRequest("propietaria", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	duena := alta.Player.ID

	insertar := func(owner any, hp, maxHP int32, status string) error {
		_, err := db.Exec(ctx,
			`INSERT INTO units (player_id, unit_type, x, y, hp, max_hp, status, chunk_x, chunk_y)
			 VALUES ($1, 'VILLAGER', 40, 40, $2, $3, $4, 1, 1)`,
			owner, hp, maxHP, status)
		return err
	}

	// INV-UNIT-006: sin dueño, o con un dueño que no existe.
	assert.Equal(t, "23502", codigoDe(t, insertar(nil, 40, 40, "IDLE")), "NOT NULL")
	assert.Equal(t, "units_player_id_fkey", constraintDe(t, insertar(uuid.New(), 40, 40, "IDLE")))

	// INV-UNIT-004: 0 <= hp <= max_hp, y max_hp positivo.
	assert.Equal(t, "units_hp_check", constraintDe(t, insertar(duena, -1, 40, "IDLE")))
	assert.Equal(t, "units_hp_within_max", constraintDe(t, insertar(duena, 41, 40, "IDLE")))
	assert.Equal(t, "units_max_hp_check", constraintDe(t, insertar(duena, 0, 0, "IDLE")))
	require.NoError(t, insertar(duena, 0, 40, "DEAD"), "los dos extremos del rango son válidos")
	require.NoError(t, insertar(duena, 40, 40, "IDLE"))

	// El dominio de status es cerrado, sin variantes de capitalización ni
	// cadena vacía.
	for _, invalido := range []string{"idle", "", "WALKING"} {
		assert.Equal(t, "units_status_valid", constraintDe(t, insertar(duena, 40, 40, invalido)), "%q", invalido)
	}

	var unidades int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM units`).Scan(&unidades))
	assert.Equal(t, 5, unidades, "los 3 aldeanos del alta y las 2 filas válidas; las rechazadas no dejan rastro")
}

// INV-UNIT-010 e INV-UNIT-011, barrido sobre las cuatro sentencias que escriben
// en `units` después del alta: el volcado por lotes (que es también la que usan
// el inicio y el cierre de un movimiento, ADR-013), el cambio de estado y la
// entrada y la salida de guarnición. Ninguna cambia el dueño ni el tipo de una
// unidad, y todas dejan el chunk de acuerdo con la posición.
func TestNingunaEscrituraCambiaElDuenoNiElTipoYTodasMantienenElChunk(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	db := store.Pool()

	huella := func() map[int64]string {
		rows, err := db.Query(ctx, `SELECT id, player_id, unit_type FROM units ORDER BY id`)
		require.NoError(t, err)
		defer rows.Close()
		out := make(map[int64]string)
		for rows.Next() {
			var id int64
			var owner uuid.UUID
			var tipo string
			require.NoError(t, rows.Scan(&id, &owner, &tipo))
			out[id] = owner.String() + "/" + tipo
		}
		require.NoError(t, rows.Err())
		return out
	}
	antes := huella()
	require.Len(t, antes, 6)

	u := e.visitante.Units[0]
	ciudad := e.visitante.City

	// Volcado a un tile pegado a su propia ciudad y en otro chunk que el de
	// origen no comparte con el destino del siguiente paso.
	minX, minY, _, _ := ciudad.UrbanBounds()
	require.NoError(t, e.units.FlushPositions(ctx, db, []postgres.PositionUpdate{
		{UnitID: u.ID, X: minX - 1, Y: minY - 1, Status: unit.StatusIdle},
		{UnitID: e.visitante.Units[1].ID, X: 33, Y: 34, Status: unit.StatusIdle},
	}))
	u.X, u.Y = minX-1, minY-1
	require.NoError(t, e.units.SetStatus(ctx, db, e.visitante.Units[2].ID, unit.StatusHidden))

	_, err := e.garrisons.Enter(ctx, u, ciudad, e.visitante.Player.ID, 1)
	require.NoError(t, err)
	require.NoError(t, e.garrisons.Leave(ctx, u, 35, 2, 35/32, 2/32, 2))

	assert.Equal(t, antes, huella(), "INV-UNIT-010: ni el dueño ni el tipo cambian")
	chunkCoherente(t, ctx, store)
}

// chunkCoherente comprueba INV-UNIT-011 sobre toda la tabla.
func chunkCoherente(t *testing.T, ctx context.Context, store *postgres.Store) {
	t.Helper()
	var descuadradas int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM units WHERE chunk_x <> x / 32 OR chunk_y <> y / 32`).Scan(&descuadradas))
	assert.Zero(t, descuadradas, "INV-UNIT-011: chunk_x/chunk_y son el chunk de (x, y)")
}

// La mitad DB de INV-MOVE-002, INV-MOVE-005 e INV-MOVE-007: una polilínea
// vacía o que no es un array, una llegada anterior a la salida y un estado
// fuera del dominio los rechaza el motor con el nombre exacto de la
// restricción.
func TestLasRestriccionesDeMovimientosRechazanFilasInvalidas(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	db := store.Pool()

	alta, err := bootstrapper.Create(ctx, bootstrapRequest("andarina", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	unidad := alta.Units[0].ID

	insertar := func(path string, start, arrival int64, status string) error {
		_, err := db.Exec(ctx,
			`INSERT INTO unit_movements (unit_id, path, target_x, target_y, start_time_ms, arrival_time_ms, status)
			 VALUES ($1, $2::jsonb, 23, 20, $3, $4, $5)`,
			unidad, path, start, arrival, status)
		return err
	}
	const valido = `[{"x":22,"y":20,"tMs":0},{"x":23,"y":20,"tMs":600}]`

	assert.Equal(t, "unit_movements_path_is_array", constraintDe(t, insertar(`[]`, 1000, 1600, "COMPLETED")))
	assert.Equal(t, "unit_movements_path_is_array", constraintDe(t, insertar(`{"x":1}`, 1000, 1600, "COMPLETED")))
	assert.Equal(t, "unit_movements_time_ordered", constraintDe(t, insertar(valido, 1000, 999, "COMPLETED")))
	for _, invalido := range []string{"active", "", "PAUSED"} {
		assert.Equal(t, "unit_movements_status_valid", constraintDe(t, insertar(valido, 1000, 1600, invalido)), "%q", invalido)
	}
	require.NoError(t, insertar(valido, 1000, 1000, "COMPLETED"), "el esquema admite duración cero; el dominio nunca la crea")

	var filas int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM unit_movements`).Scan(&filas))
	assert.Equal(t, 1, filas, "las filas rechazadas no dejan rastro")
}

// Criterios 1 y 2 de M2: el alta deja un único evento agregado
// PlayerBootstrapped con la ciudad y los tres aldeanos, y repetirla con el
// mismo nombre falla sin duplicar nada (UNIQUE de players.username).
func TestElAltaRegistraUnEventoYNoSeDuplica(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	db := store.Pool()

	alta, err := bootstrapper.Create(ctx, bootstrapRequest("unica", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)

	var tipo string
	var aldeanos int
	require.NoError(t, db.QueryRow(ctx,
		`SELECT event_type, (payload->>'villagers')::int FROM world_events WHERE player_id = $1`,
		alta.Player.ID).Scan(&tipo, &aldeanos), "un único evento para el jugador")
	assert.Equal(t, "PlayerBootstrapped", tipo)
	assert.Equal(t, 3, aldeanos)

	_, err = bootstrapper.Create(ctx, bootstrapRequest("unica", world.Tile{X: 60, Y: 60}))
	require.Error(t, err, "el mismo nombre no da de alta a un segundo jugador")

	var jugadores, ciudades, unidades, eventos int
	require.NoError(t, db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM players), (SELECT count(*) FROM cities),
		(SELECT count(*) FROM units), (SELECT count(*) FROM world_events)`).
		Scan(&jugadores, &ciudades, &unidades, &eventos))
	assert.Equal(t, [4]int{1, 1, 3, 1}, [4]int{jugadores, ciudades, unidades, eventos})
}

// El trigger set_updated_at() sella updated_at en cada UPDATE, aunque la
// sentencia intente escribir otro valor.
func TestElTriggerSellaUpdatedAtEnCadaUpdate(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	db := store.Pool()

	alta, err := bootstrapper.Create(ctx, bootstrapRequest("sellada", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)

	for _, tabla := range []string{"units", "cities"} {
		var id int64
		if tabla == "units" {
			id = alta.Units[0].ID
		} else {
			id = alta.City.ID
		}
		var antes, despues, ahora time.Time
		require.NoError(t, db.QueryRow(ctx, `SELECT updated_at, now() FROM `+tabla+` WHERE id = $1`, id).Scan(&antes, &ahora))
		_, err := db.Exec(ctx, `UPDATE `+tabla+` SET updated_at = '2000-01-01' WHERE id = $1`, id)
		require.NoError(t, err)
		require.NoError(t, db.QueryRow(ctx, `SELECT updated_at FROM `+tabla+` WHERE id = $1`, id).Scan(&despues))
		assert.True(t, despues.Year() > 2000, "%s: el trigger impone now(), no el valor de la sentencia", tabla)
		assert.False(t, despues.Before(antes), "%s: updated_at no retrocede", tabla)
	}
}
