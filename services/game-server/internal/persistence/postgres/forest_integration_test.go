//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/deposit"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Persistencia de la madera del bosque (SPEC-ECON-FOREST §7 y §9, ADR-025 §2).

var dia = deposit.Forest.RegrowAfterMs

// trabajando deja la tarea de una unidad en WORKING, con vencimiento `until`,
// como la deja la llegada al bosque.
func trabajando(t *testing.T, ctx context.Context, e escenarioEconomico, u *unit.Unit, tile world.Tile, until int64) task.Task {
	t.Helper()
	tk := task.NewGather(u.ID, tile)
	tk.Phase, tk.PhaseUntilMs = task.PhaseWorking, until
	require.NoError(t, e.economia.PersistTask(ctx, postgres.TaskTransition{
		UnitID: u.ID, WriteUnit: true, Status: unit.StatusWorking, At: tile, Task: &tk,
	}))
	return tk
}

// carga es la transición WORKING → TO_DEPOSIT con su carga de bosque.
func carga(e escenarioEconomico, u *unit.Unit, tk task.Task, amount int32, atMs int64, depleted bool) postgres.TaskTransition {
	next := tk
	next.Phase, next.PhaseUntilMs, next.Carrying = task.PhaseToDeposit, 0, amount
	return postgres.TaskTransition{
		UnitID: u.ID, WriteUnit: true, Status: unit.StatusWorking, At: tk.Target, Task: &next,
		Load: &postgres.DepositTake{
			Tile: tk.Target, Amount: amount, AtMs: atMs, RegrowsAtMs: atMs + dia,
			FromTrips: tk.TripsDone, FromUntilMs: tk.PhaseUntilMs,
			Depleted: depleted, PlayerID: e.jugador.Player.ID, CityID: &e.jugador.City.ID, Tick: 7,
		},
	}
}

func filaDeBosque(t *testing.T, ctx context.Context, store *postgres.Store, tile world.Tile) (int32, int64, bool) {
	t.Helper()
	var remaining int32
	var regrows int64
	err := store.Pool().QueryRow(ctx,
		`SELECT remaining, regrows_at_ms FROM forest_wood WHERE x = $1 AND y = $2`, tile.X, tile.Y).Scan(&remaining, &regrows)
	if err != nil {
		return 0, 0, false
	}
	return remaining, regrows, true
}

// INV-ECON-007, mitad DB: la madera de un tile está entre 0 y 100.
func Test_INV_ECON_007_ElCheckRechazaMaderaFueraDeRango(t *testing.T) {
	store, ctx := newTestStore(t)
	db := store.Pool()
	_, err := db.Exec(ctx, `INSERT INTO forest_wood (x, y, remaining, regrows_at_ms) VALUES (1, 1, -1, 5)`)
	assert.Equal(t, "forest_wood_remaining_range", constraintDe(t, err))
	_, err = db.Exec(ctx, `INSERT INTO forest_wood (x, y, remaining, regrows_at_ms) VALUES (1, 1, 101, 5)`)
	assert.Equal(t, "forest_wood_remaining_range", constraintDe(t, err))
	_, err = db.Exec(ctx, `INSERT INTO forest_wood (x, y, remaining, regrows_at_ms) VALUES (1, 1, 0, 5)`)
	assert.NoError(t, err)
}

// INV-ECON-008 y ADR-025 §2: la carga descuenta el tile en la transacción de la
// tarea, y un reintento de una transacción ya confirmada no descuenta dos veces.
// Al agotarse, queda ForestDepleted.
func TestLaCargaDescuentaElBosqueUnaSolaVezAunqueSeReintente(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	u := e.jugador.Units[0]
	tile := world.Tile{X: 40, Y: 40}

	tk := trabajando(t, ctx, e, u, tile, 1_000)
	tr := carga(e, u, tk, 10, 1_000, false)
	require.NoError(t, e.economia.PersistTask(ctx, tr))
	require.NoError(t, e.economia.PersistTask(ctx, tr), "el reintento")
	remaining, regrows, ok := filaDeBosque(t, ctx, store, tile)
	require.True(t, ok)
	assert.Equal(t, int32(90), remaining, "una carga, una vez")
	assert.Equal(t, 1_000+dia, regrows)
	tareas, err := e.economia.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, tareas, 1)
	assert.Equal(t, int32(10), tareas[0].Carrying, "lo que gana la carga es lo que pierde el tile")

	// Segundo viaje: agota el tile.
	tk2 := trabajando(t, ctx, e, u, tile, 2_000)
	require.NoError(t, e.economia.PersistTask(ctx, carga(e, u, tk2, 90, 2_000, true)))
	remaining, _, _ = filaDeBosque(t, ctx, store, tile)
	assert.Equal(t, int32(0), remaining)
	var eventos int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM world_events WHERE event_type = 'ForestDepleted' AND (payload->>'x')::int = 40`).Scan(&eventos))
	assert.Equal(t, 1, eventos)

	agotados, err := e.economia.DepletedForests(ctx, 2_001)
	require.NoError(t, err)
	assert.Contains(t, agotados, tile)
	agotados, err = e.economia.DepletedForests(ctx, 2_000+dia)
	require.NoError(t, err)
	assert.NotContains(t, agotados, tile, "una vez regenerado ya no está agotado")
}

// ADR-025 §2: dos cargas del mismo tile desde colas distintas conmutan. Llegan
// al revés que en el tick y el resultado es el mismo, con la regeneración de
// la última.
func TestDosCargasDelMismoTileConmutanAunqueLleguenAlReves(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	a, b := e.jugador.Units[0], e.jugador.Units[1]
	tile := world.Tile{X: 41, Y: 40}

	ta := trabajando(t, ctx, e, a, tile, 1_000)
	tb := trabajando(t, ctx, e, b, tile, 3_000)
	require.NoError(t, e.economia.PersistTask(ctx, carga(e, b, tb, 10, 3_000, false)))
	require.NoError(t, e.economia.PersistTask(ctx, carga(e, a, ta, 10, 1_000, false)))

	remaining, regrows, _ := filaDeBosque(t, ctx, store, tile)
	assert.Equal(t, int32(80), remaining)
	assert.Equal(t, 3_000+dia, regrows, "GREATEST: la carga vieja no adelanta la regeneración")
}

// ADR-025 §2: una fila vencida cuenta como llena; la carga siguiente parte de
// 100 sin que nadie la haya borrado.
func TestUnaCargaSobreUnTileRegeneradoPartedeLleno(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	u := e.jugador.Units[0]
	tile := world.Tile{X: 42, Y: 40}
	_, err := store.Pool().Exec(ctx,
		`INSERT INTO forest_wood (x, y, remaining, regrows_at_ms) VALUES ($1, $2, 0, 500)`, tile.X, tile.Y)
	require.NoError(t, err)

	tk := trabajando(t, ctx, e, u, tile, 1_000)
	require.NoError(t, e.economia.PersistTask(ctx, carga(e, u, tk, 10, 1_000, false)))
	remaining, regrows, _ := filaDeBosque(t, ctx, store, tile)
	assert.Equal(t, int32(90), remaining)
	assert.Equal(t, 1_000+dia, regrows)

	borradas, err := e.economia.DeleteRegrownForests(ctx, 1_000+dia)
	require.NoError(t, err)
	assert.Equal(t, int64(1), borradas)
	filas, err := e.economia.ListForests(ctx)
	require.NoError(t, err)
	assert.Empty(t, filas)
}

// §9 «integration», INV-ECON-008: con un fallo dentro de la transacción —una
// carga que dejaría el tile en negativo—, no queda nada a medias: ni el tile
// descontado ni la tarea en TO_DEPOSIT.
func TestUnaCargaQueFallaNoDejaNadaAMedias(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	u := e.jugador.Units[0]
	tile := world.Tile{X: 43, Y: 40}
	_, err := store.Pool().Exec(ctx,
		`INSERT INTO forest_wood (x, y, remaining, regrows_at_ms) VALUES ($1, $2, 5, $3)`, tile.X, tile.Y, int64(1)<<50)
	require.NoError(t, err)

	tk := trabajando(t, ctx, e, u, tile, 1_000)
	err = e.economia.PersistTask(ctx, carga(e, u, tk, 10, 1_000, false))
	require.Error(t, err)
	assert.True(t, postgres.IsCheckViolation(err))

	remaining, _, _ := filaDeBosque(t, ctx, store, tile)
	assert.Equal(t, int32(5), remaining, "el tile no se tocó")
	tareas, err := e.economia.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, tareas, 1)
	assert.Equal(t, task.PhaseWorking, tareas[0].Phase, "la tarea sigue en WORKING")
	assert.Zero(t, tareas[0].Carrying)
}
