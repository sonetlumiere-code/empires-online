//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/advance"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/deposit"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Persistencia de la piedra y del avance de era (SPEC-ECON-STONE §7 y §9,
// ADR-030), contra PostgreSQL real.

func stockDe(t *testing.T, ctx context.Context, store *postgres.Store, cityID int64, r economy.Resource) (int64, int64) {
	t.Helper()
	var amount, version int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT amount, version FROM city_stocks WHERE city_id = $1 AND resource = $2`, cityID, string(r)).
		Scan(&amount, &version))
	return amount, version
}

func (e escenarioEconomico) llenar(t *testing.T, ctx context.Context, madera, piedra int64) {
	t.Helper()
	require.NoError(t, e.economia.FlushStocks(ctx, []postgres.StockDelta{
		{CityID: e.ciudad(), Resource: economy.ResourceWood, Delta: madera, Version: 1},
		{CityID: e.ciudad(), Resource: economy.ResourceStone, Delta: piedra, Version: 1},
	}))
}

func (e escenarioEconomico) avance(requestID string) advance.Advance {
	return advance.Advance{CityID: e.ciudad(), From: city.EraStone, To: city.EraBronze,
		CompletesAtMs: 1_757_376_090_000, RequestID: requestID}
}

// pago es el débito del avance: 400 de madera y 200 de piedra, cada uno con la
// versión que tendría en la RAM tras la reserva.
func (e escenarioEconomico) pago(versionMadera, versionPiedra int64) []postgres.StockDelta {
	return []postgres.StockDelta{
		{CityID: e.ciudad(), Resource: economy.ResourceWood, Delta: -400, Version: versionMadera},
		{CityID: e.ciudad(), Resource: economy.ResourceStone, Delta: -200, Version: versionPiedra},
	}
}

func eraYLimite(t *testing.T, ctx context.Context, store *postgres.Store, cityID int64) (city.Era, int32) {
	t.Helper()
	var era string
	var limit int32
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT era, population_limit FROM cities WHERE id = $1`, cityID).Scan(&era, &limit))
	return city.Era(era), limit
}

// ─────────────────────────────────────────────────────────────
// La piedra de la colina
// ─────────────────────────────────────────────────────────────

// INV-ECON-007, mitad DB: la piedra de un tile está entre 0 y 100.
func Test_INV_ECON_007_ElCheckRechazaPiedraFueraDeRango(t *testing.T) {
	store, ctx := newTestStore(t)
	db := store.Pool()
	_, err := db.Exec(ctx, `INSERT INTO hill_stone (x, y, remaining, regrows_at_ms) VALUES (1, 1, -1, 5)`)
	assert.Equal(t, "hill_stone_remaining_range", constraintDe(t, err))
	_, err = db.Exec(ctx, `INSERT INTO hill_stone (x, y, remaining, regrows_at_ms) VALUES (1, 1, 101, 5)`)
	assert.Equal(t, "hill_stone_remaining_range", constraintDe(t, err))
	_, err = db.Exec(ctx, `INSERT INTO hill_stone (x, y, remaining, regrows_at_ms) VALUES (1, 1, 0, 5)`)
	assert.NoError(t, err)
}

// cargaDePiedra es la transición WORKING → TO_DEPOSIT de una tarea de piedra.
func cargaDePiedra(e escenarioEconomico, u *unit.Unit, tk task.Task, amount int32, atMs int64, depleted bool) postgres.TaskTransition {
	tr := carga(e, u, tk, amount, atMs, depleted)
	tr.Load.Layer = deposit.Hill
	return tr
}

func trabajandoEnColina(t *testing.T, ctx context.Context, e escenarioEconomico, u *unit.Unit, tile world.Tile, until int64) task.Task {
	t.Helper()
	tk := task.NewGatherOf(u.ID, task.KindGatherStone, tile)
	tk.Phase, tk.PhaseUntilMs = task.PhaseWorking, until
	require.NoError(t, e.economia.PersistTask(ctx, postgres.TaskTransition{
		UnitID: u.ID, WriteUnit: true, Status: unit.StatusWorking, At: tile, Task: &tk,
	}))
	return tk
}

func filaDeColina(t *testing.T, ctx context.Context, store *postgres.Store, tile world.Tile) (int32, bool) {
	t.Helper()
	var remaining int32
	err := store.Pool().QueryRow(ctx,
		`SELECT remaining FROM hill_stone WHERE x = $1 AND y = $2`, tile.X, tile.Y).Scan(&remaining)
	if err != nil {
		return 0, false
	}
	return remaining, true
}

// INV-ECON-008 y ADR-030 §1: la carga de piedra descuenta la colina —y no el
// bosque— en la transacción de la tarea, una sola vez aunque se reintente, y
// al agotarla escribe HillDepleted. La tarea GATHER_STONE se guarda con su tipo.
func TestLaCargaDePiedraDescuentaLaColinaUnaSolaVez(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	u := e.jugador.Units[0]
	tile := world.Tile{X: 40, Y: 40}

	tk := trabajandoEnColina(t, ctx, e, u, tile, 1_000)
	tr := cargaDePiedra(e, u, tk, 10, 1_000, false)
	require.NoError(t, e.economia.PersistTask(ctx, tr))
	require.NoError(t, e.economia.PersistTask(ctx, tr), "un reintento")
	remaining, ok := filaDeColina(t, ctx, store, tile)
	require.True(t, ok)
	assert.Equal(t, int32(90), remaining, "descuenta una vez")
	_, _, enBosque := filaDeBosque(t, ctx, store, tile)
	assert.False(t, enBosque, "el bosque no se toca")
	tareas, err := e.economia.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, tareas, 1)
	assert.Equal(t, task.KindGatherStone, tareas[0].Kind)

	// Un segundo aldeano agota el tile con una carga de 90.
	v := e.jugador.Units[1]
	tk2 := trabajandoEnColina(t, ctx, e, v, tile, 2_000)
	require.NoError(t, e.economia.PersistTask(ctx, cargaDePiedra(e, v, tk2, 90, 2_000, true)))
	remaining, _ = filaDeColina(t, ctx, store, tile)
	assert.Zero(t, remaining)
	assert.Equal(t, 1, eventosDe(t, ctx, store, "HillDepleted"))
	assert.Zero(t, eventosDe(t, ctx, store, "ForestDepleted"))

	filas, err := e.economia.ListHills(ctx)
	require.NoError(t, err)
	require.Len(t, filas, 1)
	assert.Equal(t, tile, filas[0].Tile)
}

// INV-ECON-008: una carga de piedra que dejaría la colina en negativo revierte
// entera: ni la colina ni la tarea cambian.
func TestUnaCargaDePiedraQueFallaNoDejaNadaAMedias(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	u := e.jugador.Units[0]
	tile := world.Tile{X: 43, Y: 40}
	_, err := store.Pool().Exec(ctx,
		`INSERT INTO hill_stone (x, y, remaining, regrows_at_ms) VALUES ($1, $2, 5, $3)`, tile.X, tile.Y, int64(1)<<50)
	require.NoError(t, err)

	tk := trabajandoEnColina(t, ctx, e, u, tile, 1_000)
	err = e.economia.PersistTask(ctx, cargaDePiedra(e, u, tk, 10, 1_000, false))
	require.Error(t, err)
	assert.True(t, postgres.IsCheckViolation(err))
	remaining, _ := filaDeColina(t, ctx, store, tile)
	assert.Equal(t, int32(5), remaining)
	tareas, err := e.economia.ListTasks(ctx)
	require.NoError(t, err)
	assert.Equal(t, task.PhaseWorking, tareas[0].Phase)
}

// La limpieza de arranque borra las filas de colina que ya volvieron a crecer.
func TestLaLimpiezaBorraLaColinaRegenerada(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	_, err := store.Pool().Exec(ctx,
		`INSERT INTO hill_stone (x, y, remaining, regrows_at_ms) VALUES (1, 1, 0, 100), (2, 2, 0, 500)`)
	require.NoError(t, err)
	n, err := e.economia.DeleteRegrownHills(ctx, 200)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	filas, err := e.economia.ListHills(ctx)
	require.NoError(t, err)
	require.Len(t, filas, 1)
	assert.Equal(t, world.Tile{X: 2, Y: 2}, filas[0].Tile)
}

// ─────────────────────────────────────────────────────────────
// El avance de era
// ─────────────────────────────────────────────────────────────

// RN-ECON-025 e INV-ECON-002: el encargo debita los dos recursos, inserta el
// avance y escribe EraAdvanceOrdered en una transacción; un reintento con el
// mismo requestId no debita otra vez.
func TestElAvanceDebitaLosDosRecursosYEsIdempotente(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.llenar(t, ctx, 450, 200)

	a := e.avance("00000000-0000-4000-8000-0000000000a1")
	require.NoError(t, e.economia.Advance(ctx, a.RequestID, e.jugador.Player.ID, a, e.pago(2, 2), 9))
	require.NoError(t, e.economia.Advance(ctx, a.RequestID, e.jugador.Player.ID, a, e.pago(2, 2), 9), "un reintento")

	madera, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	piedra, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceStone)
	assert.Equal(t, int64(50), madera)
	assert.Equal(t, int64(0), piedra)
	assert.Equal(t, 1, eventosDe(t, ctx, store, postgres.EventEraAdvanceOrdered))
	avances, err := e.economia.ListAdvances(ctx)
	require.NoError(t, err)
	require.Len(t, avances, 1)
	assert.Equal(t, a, avances[0])

	otro := e.avance("00000000-0000-4000-8000-0000000000a2")
	err = e.economia.Advance(ctx, otro.RequestID, e.jugador.Player.ID, otro, e.pago(3, 3), 9)
	assert.ErrorIs(t, err, advance.ErrCityBusy, "un segundo avance encuentra la ciudad ocupada")
}

// INV-ECON-002: sin piedra suficiente el avance no gasta tampoco la madera; con
// la versión de uno de los dos ya escrita, no se aplica ninguno.
func Test_INV_ECON_002_UnAvanceSinPiedraNoGastaMadera(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.llenar(t, ctx, 400, 100)

	a := e.avance("00000000-0000-4000-8000-0000000000b1")
	err := e.economia.Advance(ctx, a.RequestID, e.jugador.Player.ID, a, e.pago(2, 2), 9)
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)
	madera, version := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	assert.Equal(t, int64(400), madera, "la madera no se gastó")
	assert.Equal(t, int64(1), version)

	require.NoError(t, e.economia.FlushStocks(ctx, []postgres.StockDelta{
		{CityID: e.ciudad(), Resource: economy.ResourceStone, Delta: 100, Version: 5},
	}))
	err = e.economia.Advance(ctx, a.RequestID, e.jugador.Player.ID, a, e.pago(2, 5), 9)
	assert.ErrorIs(t, err, postgres.ErrStaleStock, "la piedra de la base va por delante")
	madera, _ = stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	assert.Equal(t, int64(400), madera)
	avances, err := e.economia.ListAdvances(ctx)
	require.NoError(t, err)
	assert.Empty(t, avances)
	assert.Zero(t, eventosDe(t, ctx, store, postgres.EventEraAdvanceOrdered))
}

// RN-ECON-026: con un entrenamiento en curso la ciudad no avanza; la base lo
// repite aunque la RAM ya lo decidió.
func TestConUnEntrenamientoEnCursoNoSeAvanza(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.llenar(t, ctx, 500, 200)
	p := production.Production{CityID: e.ciudad(), UnitType: unit.TypeVillager, CompletesAtMs: 1_757_376_020_000}
	require.NoError(t, e.economia.Train(ctx, "00000000-0000-4000-8000-0000000000c1", e.jugador.Player.ID, p, 1,
		debito(e.ciudad(), -50, 2), 9))
	a := e.avance("00000000-0000-4000-8000-0000000000c2")
	err := e.economia.Advance(ctx, a.RequestID, e.jugador.Player.ID, a, e.pago(3, 2), 9)
	assert.ErrorIs(t, err, advance.ErrCityBusy)
	madera, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	assert.Equal(t, int64(450), madera, "el avance rechazado no gastó")
}

// RN-ECON-026: con un avance en curso la ciudad no entrena.
func TestConUnAvanceEnCursoNoSeEntrena(t *testing.T) {
	store, ctx := newTestStore(t)
	f := nuevaEconomia(t, ctx, store)
	f.llenar(t, ctx, 500, 200)
	b := f.avance("00000000-0000-4000-8000-0000000000c3")
	require.NoError(t, f.economia.Advance(ctx, b.RequestID, f.jugador.Player.ID, b, f.pago(2, 2), 9))
	q := production.Production{CityID: f.ciudad(), UnitType: unit.TypeVillager, CompletesAtMs: 1_757_376_020_000}
	err := f.economia.Train(ctx, "00000000-0000-4000-8000-0000000000c4", f.jugador.Player.ID, q, 1,
		debito(f.ciudad(), -50, 3), 9)
	assert.ErrorIs(t, err, production.ErrCityBusy)
	madera, _ := stockDe(t, ctx, store, f.ciudad(), economy.ResourceWood)
	assert.Equal(t, int64(100), madera, "el entrenamiento rechazado no gastó")
}

// INV-CITY-018: un avance que no sale de la era de la ciudad se rechaza sin
// debitar; la tabla no admite un avance que se quede en el sitio.
func TestUnAvanceDesdeOtraEraSeRechaza(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.llenar(t, ctx, 400, 200)
	_, err := store.Pool().Exec(ctx, `UPDATE cities SET era = 'BRONZE_AGE' WHERE id = $1`, e.ciudad())
	require.NoError(t, err)
	a := e.avance("00000000-0000-4000-8000-0000000000d1")
	err = e.economia.Advance(ctx, a.RequestID, e.jugador.Player.ID, a, e.pago(2, 2), 9)
	assert.ErrorIs(t, err, advance.ErrUnavailable)
	madera, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	assert.Equal(t, int64(400), madera)

	_, err = store.Pool().Exec(ctx,
		`INSERT INTO city_era_advances (city_id, from_era, to_era, completes_at_ms, request_id)
		 VALUES ($1, 'BRONZE_AGE', 'BRONZE_AGE', 1, 'x')`, e.ciudad())
	assert.Equal(t, "city_era_advances_forward", constraintDe(t, err))
}

// RN-ECON-027 e INV-CITY-018: el fin del avance cambia la era, sube el límite
// en cap(BRONZE) − cap(STONE) sobre lo que ya tenía —las casas incluidas—,
// borra la fila y escribe EraAdvanced; repetido, no cambia nada.
func TestElFinDelAvanceSubeElLimiteUnaVez(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.llenar(t, ctx, 400, 200)
	_, err := store.Pool().Exec(ctx,
		`UPDATE cities SET population_limit = population_limit + 10, version = version + 1 WHERE id = $1`, e.ciudad())
	require.NoError(t, err)
	era, limite := eraYLimite(t, ctx, store, e.ciudad())
	require.Equal(t, city.EraStone, era)
	require.Equal(t, int32(30), limite, "20 de la era y dos casas")

	a := e.avance("00000000-0000-4000-8000-0000000000e1")
	require.NoError(t, e.economia.Advance(ctx, a.RequestID, e.jugador.Player.ID, a, e.pago(2, 2), 9))
	require.NoError(t, e.economia.CompleteAdvance(ctx, e.ciudad(), e.jugador.Player.ID, city.EraStone, city.EraBronze, 10))
	require.NoError(t, e.economia.CompleteAdvance(ctx, e.ciudad(), e.jugador.Player.ID, city.EraStone, city.EraBronze, 11),
		"un reintento")

	era, limite = eraYLimite(t, ctx, store, e.ciudad())
	assert.Equal(t, city.EraBronze, era)
	assert.Equal(t, int32(60), limite, "50 de Bronce y las dos casas, sumado una vez")
	avances, err := e.economia.ListAdvances(ctx)
	require.NoError(t, err)
	assert.Empty(t, avances, "la fila del avance se va con su fin")
	assert.Equal(t, 1, eventosDe(t, ctx, store, postgres.EventEraAdvanced))

	// Un reintento tardío del encargo, con el avance ya terminado, no debita.
	madera, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	err = e.economia.Advance(ctx, a.RequestID, e.jugador.Player.ID, a, e.pago(2, 2), 9)
	assert.ErrorIs(t, err, advance.ErrUnavailable)
	despues, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	assert.Equal(t, madera, despues)
}
