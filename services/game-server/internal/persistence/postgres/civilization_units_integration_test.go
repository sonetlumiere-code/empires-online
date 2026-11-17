//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Integración de SPEC-CIVILIZATION-UNITS (§9) y ADR-043: la columna
// civilizations.unit, el trigger de city_productions (INV-UNIT-012) y el
// encargo con tres débitos, contra PostgreSQL real. El jugador de nuevaEconomia
// es ROMAN (civilización 1).

// enBronceConTodo pone la ciudad en la Edad de Bronce con 80 de madera, 40 de
// piedra y 10 de vino, todo con versión 1.
func (e escenarioEconomico) enBronceConTodo(t *testing.T, ctx context.Context, store *postgres.Store) {
	t.Helper()
	_, err := store.Pool().Exec(ctx, `UPDATE cities SET era = 'BRONZE_AGE' WHERE id = $1`, e.ciudad())
	require.NoError(t, err)
	require.NoError(t, e.economia.FlushStocks(ctx, []postgres.StockDelta{
		{CityID: e.ciudad(), Resource: economy.ResourceWood, Delta: 80, Version: 1},
		{CityID: e.ciudad(), Resource: economy.ResourceStone, Delta: 40, Version: 1},
		{CityID: e.ciudad(), Resource: economy.ResourceWine, Delta: 10, Version: 1},
	}))
}

// pagoDeExclusiva son los tres débitos de una unidad exclusiva romana.
func (e escenarioEconomico) pagoDeExclusiva(vino int64) []postgres.StockDelta {
	return []postgres.StockDelta{
		{CityID: e.ciudad(), Resource: economy.ResourceWood, Delta: -80, Version: 2},
		{CityID: e.ciudad(), Resource: economy.ResourceStone, Delta: -40, Version: 2},
		{CityID: e.ciudad(), Resource: economy.ResourceWine, Delta: -vino, Version: 2},
	}
}

func (e escenarioEconomico) produccion(tipo unit.Type) production.Production {
	return production.Production{CityID: e.ciudad(), UnitType: tipo, CompletesAtMs: 1_757_376_035_000}
}

// INV-UNIT-013: 000024 da a cada civilización su unidad, NOT NULL, UNIQUE y del
// CHECK de los cuatro códigos; la carga la lee.
func TestCadaCivilizacionTieneSuUnidadEnLaBase(t *testing.T) {
	store, ctx := newTestStore(t)
	players, _, _, _, _ := newRepos(store)
	civs, err := players.ListCivilizations(ctx)
	require.NoError(t, err)
	unidades := map[string]unit.Type{}
	for _, c := range civs {
		unidades[c.Code] = c.Unit
	}
	assert.Equal(t, map[string]unit.Type{
		"ROMAN": unit.TypeLegionary, "BYZANTINE": unit.TypeCataphract,
		"PERSIAN": unit.TypeHorseArcher, "NORSE": unit.TypeBerserker,
	}, unidades)

	for nombre, sql := range map[string]string{
		"repetida":    `UPDATE civilizations SET unit = 'LEGIONARY' WHERE code = 'NORSE'`,
		"desconocida": `UPDATE civilizations SET unit = 'KNIGHT' WHERE code = 'NORSE'`,
		"una común":   `UPDATE civilizations SET unit = 'WARRIOR' WHERE code = 'NORSE'`,
		"sin unidad":  `UPDATE civilizations SET unit = NULL WHERE code = 'NORSE'`,
	} {
		_, err := store.Pool().Exec(ctx, sql)
		require.Error(t, err, nombre)
	}
}

// RN-CIVUNIT-003 e INV-ECON-002: el encargo de un legionario debita madera,
// piedra y vino con la producción y la plaza, en una transacción; si el vino
// no alcanza, no se debita nada.
func TestElEncargoDeUnaExclusivaDebitaLosTresRecursosJuntos(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.enBronceConTodo(t, ctx, store)

	err := e.economia.Train(ctx, "00000000-0000-4000-8000-0000000000e1", e.jugador.Player.ID,
		e.produccion(unit.TypeLegionary), 1, e.pagoDeExclusiva(11), 9)
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)
	for _, r := range []economy.Resource{economy.ResourceWood, economy.ResourceStone, economy.ResourceWine} {
		n, _ := stockDe(t, ctx, store, e.ciudad(), r)
		assert.Positive(t, n, "%s no se debitó", r)
	}
	var producciones int
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT count(*) FROM city_productions WHERE city_id = $1`,
		e.ciudad()).Scan(&producciones))
	assert.Zero(t, producciones)

	require.NoError(t, e.economia.Train(ctx, "00000000-0000-4000-8000-0000000000e2", e.jugador.Player.ID,
		e.produccion(unit.TypeLegionary), 1, e.pagoDeExclusiva(10), 9))
	for _, r := range []economy.Resource{economy.ResourceWood, economy.ResourceStone, economy.ResourceWine} {
		n, _ := stockDe(t, ctx, store, e.ciudad(), r)
		assert.Zero(t, n, "%s", r)
	}
	var tipo string
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT unit_type FROM city_productions WHERE city_id = $1`,
		e.ciudad()).Scan(&tipo))
	assert.Equal(t, string(unit.TypeLegionary), tipo)
}

// INV-UNIT-012 en la base: el trigger de 000024 rechaza la unidad de otra
// civilización y la propia antes de Bronce, con check_violation; Train lo
// devuelve como ErrNotTrainable (INVALID_TARGET) y no deja nada debitado.
func TestLaBaseRechazaLaUnidadDeOtraCivilizacionOAntesDeBronce(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.enBronceConTodo(t, ctx, store)

	err := e.economia.Train(ctx, "00000000-0000-4000-8000-0000000000e3", e.jugador.Player.ID,
		e.produccion(unit.TypeBerserker), 1, e.pagoDeExclusiva(10), 9)
	assert.ErrorIs(t, err, production.ErrNotTrainable, "un romano no entrena berserkers")
	madera, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	assert.Equal(t, int64(80), madera, "el rechazo no gastó")

	_, err = store.Pool().Exec(ctx, `UPDATE cities SET era = 'STONE_AGE' WHERE id = $1`, e.ciudad())
	require.NoError(t, err)
	err = e.economia.Train(ctx, "00000000-0000-4000-8000-0000000000e4", e.jugador.Player.ID,
		e.produccion(unit.TypeLegionary), 1, e.pagoDeExclusiva(10), 9)
	assert.ErrorIs(t, err, production.ErrNotTrainable, "ni el legionario antes de Bronce")

	// Un INSERT directo, sin pasar por Train: el trigger es la última barrera.
	_, err = store.Pool().Exec(ctx,
		`INSERT INTO city_productions (city_id, unit_type, completes_at_ms, request_id) VALUES ($1, 'CATAPHRACT', 1, 'x')`,
		e.ciudad())
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "%v", err)
	assert.Equal(t, "23514", pgErr.Code, "check_violation")

	// Las comunes no las mira: un guerrero en la Edad de Piedra se encarga.
	_, err = store.Pool().Exec(ctx,
		`INSERT INTO city_productions (city_id, unit_type, completes_at_ms, request_id) VALUES ($1, 'WARRIOR', 1, 'y')`,
		e.ciudad())
	require.NoError(t, err)
}
