//go:build integration

package postgres_test

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Persistencia del combate (SPEC-COMBAT-RAID §7 y §9 «integration», ADR-024 §2).

// INV-COMBAT-001, mitad DB: una unidad está DEAD si y sólo si su hp es 0. Las
// dos mitades del bicondicional las rechaza el motor por su nombre.
func Test_INV_COMBAT_001_LaConstraintRechazaUnaMuertaConVida(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	alta, err := bootstrapper.Create(ctx, bootstrapRequest("mortal", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	id := alta.Units[0].ID
	db := store.Pool()

	_, err = db.Exec(ctx, `UPDATE units SET status = 'DEAD' WHERE id = $1`, id)
	assert.Equal(t, "units_dead_iff_zero_hp", constraintDe(t, err), "muerta con vida")
	_, err = db.Exec(ctx, `UPDATE units SET hp = 0 WHERE id = $1`, id)
	assert.Equal(t, "units_dead_iff_zero_hp", constraintDe(t, err), "viva sin vida")
	_, err = db.Exec(ctx, `UPDATE units SET loot = -1 WHERE id = $1`, id)
	assert.Equal(t, "units_loot_non_negative", constraintDe(t, err))
	_, err = db.Exec(ctx, `UPDATE units SET status = 'DEAD', hp = 0 WHERE id = $1`, id)
	assert.NoError(t, err, "las dos a la vez sí")
}

// RN-COMBAT-009: la muerte es una transacción. La víctima queda DEAD, sin vida
// ni botín, en su último tile; su ciudad pierde la población; queda el evento.
// Repetirla no descuenta dos veces: es idempotente.
func TestLaMuerteSeEscribeEnUnaTransaccionIdempotente(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, units, _, bootstrapper := newRepos(store)
	victima, err := bootstrapper.Create(ctx, bootstrapRequest("victima", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	asesino, err := bootstrapper.Create(ctx, bootstrapRequest("asesino", world.Tile{X: 50, Y: 20}))
	require.NoError(t, err)
	u := victima.Units[0]
	db := store.Pool()
	_, err = db.Exec(ctx, `UPDATE units SET loot = 4 WHERE id = $1`, u.ID)
	require.NoError(t, err)

	k := postgres.Kill{
		VictimID: u.ID, VictimPlayer: victima.Player.ID,
		KillerID: asesino.Units[0].ID, KillerPlayer: asesino.Player.ID,
		CityID: &victima.City.ID, PopulationCost: 1, At: world.Tile{X: 33, Y: 34}, Tick: 77, Loot: 4,
	}
	for vez := 0; vez < 2; vez++ {
		require.NoError(t, store.InTx(ctx, func(tx pgx.Tx) error {
			_, err := units.Kill(ctx, tx, k)
			return err
		}))
	}

	var status string
	var hp, loot, x, y, cx, cy int32
	require.NoError(t, db.QueryRow(ctx,
		`SELECT status, hp, loot, x, y, chunk_x, chunk_y FROM units WHERE id = $1`, u.ID).
		Scan(&status, &hp, &loot, &x, &y, &cx, &cy))
	assert.Equal(t, "DEAD", status)
	assert.Zero(t, hp)
	assert.Zero(t, loot)
	assert.Equal(t, [4]int32{33, 34, 1, 1}, [4]int32{x, y, cx, cy})

	var poblacion int32
	require.NoError(t, db.QueryRow(ctx, `SELECT population FROM cities WHERE id = $1`, victima.City.ID).Scan(&poblacion))
	assert.Equal(t, int32(2), poblacion, "un punto menos, una sola vez")

	var eventos int
	require.NoError(t, db.QueryRow(ctx,
		`SELECT count(*) FROM world_events WHERE event_type = 'UnitKilled' AND (payload->>'unitId')::bigint = $1`, u.ID).
		Scan(&eventos))
	assert.Equal(t, 1, eventos)

	vivas, err := units.ListAlive(ctx)
	require.NoError(t, err)
	for _, v := range vivas {
		assert.NotEqual(t, u.ID, v.ID, "una muerta no se carga al arrancar")
	}
}

// §9 «integration»: con un fallo inyectado al final de la transacción —el
// evento apunta a un jugador que no existe—, no queda nada a medias: ni la
// víctima muerta ni la población descontada.
func TestUnaMuerteQueFallaNoDejaNadaAMedias(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, units, _, bootstrapper := newRepos(store)
	victima, err := bootstrapper.Create(ctx, bootstrapRequest("intacta", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	u := victima.Units[0]

	err = store.InTx(ctx, func(tx pgx.Tx) error {
		_, err := units.Kill(ctx, tx, postgres.Kill{
			VictimID: u.ID, VictimPlayer: uuid.New(), // FK de world_events: falla
			CityID: &victima.City.ID, PopulationCost: 1, At: world.Tile{X: 21, Y: 20},
		})
		return err
	})
	require.Error(t, err)

	db := store.Pool()
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM units WHERE id = $1`, u.ID).Scan(&status))
	assert.Equal(t, "IDLE", status)
	var poblacion int32
	require.NoError(t, db.QueryRow(ctx, `SELECT population FROM cities WHERE id = $1`, victima.City.ID).Scan(&poblacion))
	assert.Equal(t, int32(3), poblacion)
}

// ADR-024 §2: el volcado escribe la vida y el botín cuando los trae y los deja
// como están cuando no; y nunca toca una unidad ya muerta.
func TestElVolcadoEscribeVidaYBotinYNoResucita(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, units, _, bootstrapper := newRepos(store)
	alta, err := bootstrapper.Create(ctx, bootstrapRequest("volcada", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	db := store.Pool()
	a, b := alta.Units[0], alta.Units[1]

	hp, loot := int32(17), int32(6)
	require.NoError(t, units.FlushPositions(ctx, db, []postgres.PositionUpdate{
		{UnitID: a.ID, X: a.X, Y: a.Y, Status: "IDLE", HP: &hp, Loot: &loot},
		{UnitID: b.ID, X: b.X, Y: b.Y, Status: "IDLE"},
	}))
	var gotHP, gotLoot int32
	require.NoError(t, db.QueryRow(ctx, `SELECT hp, loot FROM units WHERE id = $1`, a.ID).Scan(&gotHP, &gotLoot))
	assert.Equal(t, [2]int32{17, 6}, [2]int32{gotHP, gotLoot})
	require.NoError(t, db.QueryRow(ctx, `SELECT hp, loot FROM units WHERE id = $1`, b.ID).Scan(&gotHP, &gotLoot))
	assert.Equal(t, [2]int32{40, 0}, [2]int32{gotHP, gotLoot}, "sin HP ni Loot, no cambian")

	_, err = db.Exec(ctx, `UPDATE units SET status = 'DEAD', hp = 0 WHERE id = $1`, b.ID)
	require.NoError(t, err)
	vivo := int32(40)
	require.NoError(t, units.FlushPositions(ctx, db, []postgres.PositionUpdate{
		{UnitID: b.ID, X: b.X, Y: b.Y, Status: "IDLE", HP: &vivo},
	}))
	var status string
	require.NoError(t, db.QueryRow(ctx, `SELECT status FROM units WHERE id = $1`, b.ID).Scan(&status))
	assert.Equal(t, "DEAD", status, "un volcado tardío no resucita a nadie")
}

// SPEC-COMBAT-RESPONSE Q8: el primer golpe de un enfrentamiento deja un
// UnitAttacked a nombre del atacado, con auto cuando es una respuesta.
func TestElPrimerGolpeDejaUnitAttacked(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, units, _, bootstrapper := newRepos(store)
	atacado, err := bootstrapper.Create(ctx, bootstrapRequest("atacado", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	atacante, err := bootstrapper.Create(ctx, bootstrapRequest("atacante", world.Tile{X: 50, Y: 20}))
	require.NoError(t, err)
	db := store.Pool()

	for _, auto := range []bool{false, true} {
		require.NoError(t, units.Attacked(ctx, db, postgres.Attacked{
			AttackerID: atacante.Units[0].ID, AttackerPlayer: atacante.Player.ID,
			TargetID: atacado.Units[0].ID, TargetPlayer: atacado.Player.ID,
			At: world.Tile{X: 21, Y: 22}, Tick: 9, Auto: auto,
		}))
	}

	rows, err := db.Query(ctx,
		`SELECT player_id, tick, payload->>'attackerId', payload->>'x', payload->>'y', (payload->>'auto')::boolean
		 FROM world_events WHERE event_type = 'UnitAttacked' AND (payload->>'unitId')::bigint = $1 ORDER BY id`,
		atacado.Units[0].ID)
	require.NoError(t, err)
	defer rows.Close()
	var autos []bool
	for rows.Next() {
		var player uuid.UUID
		var tick int64
		var attacker, x, y string
		var auto bool
		require.NoError(t, rows.Scan(&player, &tick, &attacker, &x, &y, &auto))
		assert.Equal(t, atacado.Player.ID, player)
		assert.Equal(t, int64(9), tick)
		assert.Equal(t, [3]string{fmt.Sprint(atacante.Units[0].ID), "21", "22"}, [3]string{attacker, x, y})
		autos = append(autos, auto)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []bool{false, true}, autos)
}
