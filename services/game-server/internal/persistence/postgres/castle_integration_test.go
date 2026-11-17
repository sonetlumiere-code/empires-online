//go:build integration

package postgres_test

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/advance"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Integración de la Edad de los Castillos (SPEC-CASTLE-AGE §9): el único parcial
// de 000025, el avance de Hierro a Castillos con bienes ajenos y la muerte que
// firma un castillo.

func castillo(cityID int64, x, y int32) building.Building {
	return building.Building{CityID: cityID, Type: building.TypeCastle, X: x, Y: y,
		Status: building.StatusUnderConstruction, CompletesAtMs: 1_757_376_180_000}
}

// INV-ECON-012: el único parcial impide un segundo castillo en pie; derribado el
// primero, se puede volver a construir. Las dos líneas del coste se debitan en la
// misma transacción, y un rechazo no debita.
func Test_INV_ECON_012_UnCastilloEnPiePorCiudad(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.recursos(t, ctx, 1_500, 2_400)

	id, err := e.economia.Build(ctx, "req-1", e.jugador.Player.ID, castillo(e.ciudad(), 21, 21), e.debitos(500, 800, 2), 1)
	require.NoError(t, err)
	madera, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	piedra, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceStone)
	assert.Equal(t, [2]int64{1_000, 1_600}, [2]int64{madera, piedra})

	_, err = e.economia.Build(ctx, "req-2", e.jugador.Player.ID, castillo(e.ciudad(), 19, 19), e.debitos(500, 800, 3), 1)
	assert.ErrorIs(t, err, building.ErrInvalidSite)
	madera, _ = stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	assert.Equal(t, int64(1_000), madera, "el rechazo no debita")

	// Un taller no choca con el castillo: cada único es de su tipo.
	_, err = e.economia.Build(ctx, "req-taller", e.jugador.Player.ID, taller(e.ciudad(), 19, 21), e.debitos(100, 50, 3), 1)
	require.NoError(t, err)

	require.NoError(t, e.economia.DestroyBuilding(ctx, postgres.BuildingDestruction{
		BuildingID: id, CityID: e.ciudad(), VictimPlayer: e.jugador.Player.ID, AttackerPlayer: e.jugador.Player.ID,
		Type: building.TypeCastle, Tick: 2,
	}))
	_, err = e.economia.Build(ctx, "req-3", e.jugador.Player.ID, castillo(e.ciudad(), 19, 19), e.debitos(500, 800, 4), 3)
	assert.NoError(t, err, "con el anterior derribado, otro castillo")

	var castillos int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM buildings WHERE city_id = $1 AND type = 'CASTLE' AND status <> 'DESTROYED'`, e.ciudad()).Scan(&castillos))
	assert.Equal(t, 1, castillos)
}

// RN-CASTLE-001 e INV-ECON-002: el avance de Hierro a Castillos debita madera,
// piedra y los bienes ajenos resueltos en una transacción con su fila, y su fin
// deja la ciudad en Castillos con 50 plazas más (de 100 a 150).
func TestElAvanceACastillosDebitaLosBienesYSubeElLimite(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	_, err := store.Pool().Exec(ctx,
		`UPDATE cities SET era = 'IRON_AGE', population_limit = population_limit + 80 WHERE id = $1`, e.ciudad())
	require.NoError(t, err)
	_, antes := eraYLimite(t, ctx, store, e.ciudad())
	require.NoError(t, e.economia.FlushStocks(ctx, []postgres.StockDelta{
		{CityID: e.ciudad(), Resource: economy.ResourceWood, Delta: 1_600, Version: 1},
		{CityID: e.ciudad(), Resource: economy.ResourceStone, Delta: 800, Version: 1},
		{CityID: e.ciudad(), Resource: economy.ResourceSilk, Delta: 70, Version: 1},
		{CityID: e.ciudad(), Resource: economy.ResourceSpices, Delta: 50, Version: 1},
	}))

	a := advance.Advance{CityID: e.ciudad(), From: city.EraIron, To: city.EraCastle,
		CompletesAtMs: 1_757_376_360_000, RequestID: "00000000-0000-4000-8000-0000000000c1"}
	require.NoError(t, a.Validate())
	pago := []postgres.StockDelta{
		{CityID: e.ciudad(), Resource: economy.ResourceWood, Delta: -1_600, Version: 2},
		{CityID: e.ciudad(), Resource: economy.ResourceStone, Delta: -800, Version: 2},
		{CityID: e.ciudad(), Resource: economy.ResourceSilk, Delta: -70, Version: 2},
		{CityID: e.ciudad(), Resource: economy.ResourceSpices, Delta: -50, Version: 2},
	}
	require.NoError(t, e.economia.Advance(ctx, a.RequestID, e.jugador.Player.ID, a, pago, 9))
	for _, r := range []economy.Resource{economy.ResourceWood, economy.ResourceStone, economy.ResourceSilk, economy.ResourceSpices} {
		n, _ := stockDe(t, ctx, store, e.ciudad(), r)
		assert.Zero(t, n, "%s debitado", r)
	}
	filas, err := e.economia.ListAdvances(ctx)
	require.NoError(t, err)
	require.Len(t, filas, 1)
	assert.Equal(t, city.EraCastle, filas[0].To)

	require.NoError(t, e.economia.CompleteAdvance(ctx, e.ciudad(), e.jugador.Player.ID, city.EraIron, city.EraCastle, 10))
	era, limite := eraYLimite(t, ctx, store, e.ciudad())
	assert.Equal(t, city.EraCastle, era)
	assert.Equal(t, antes+50, limite)
}

// ADR-044 §5 y §6: la muerte que causa un castillo se escribe con KillerID 0, el
// castillo en el payload y su dueño como contraparte; el registro la cuenta a la
// víctima como UNIT_LOST_TO_CASTLE y al dueño del castillo como CASTLE_KILL.
func TestLaMuerteDelCastilloSeEscribeYSeCuentaConSusTipos(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoSaqueo(t, ctx, store, 0)
	require.NoError(t, store.InTx(ctx, func(tx pgx.Tx) error {
		_, err := e.units.Kill(ctx, tx, postgres.Kill{
			VictimID: e.saqueador.Units[0].ID, VictimPlayer: e.saqueador.Player.ID,
			KillerPlayer: e.victima.Player.ID, KillerBuildingID: 950, KillerBuildingType: string(building.TypeCastle),
			At: world.Tile{X: 22, Y: 20}, Tick: 40, VictimType: unit.TypeVillager, AtMs: 1_757_376_010_000,
		})
		return err
	}))

	f := ultimoEvento(t, ctx, store, "UnitKilled")
	require.NotNil(t, f.player)
	require.NotNil(t, f.counterpart)
	assert.Equal(t, e.saqueador.Player.ID, *f.player)
	assert.Equal(t, e.victima.Player.ID, *f.counterpart)
	assert.Equal(t, float64(0), f.payload["killerId"])
	assert.Equal(t, float64(950), f.payload["killerBuildingId"])
	assert.Equal(t, "CASTLE", f.payload["killerBuildingType"])

	r := registro(store, 10)
	perdidas, _, err := r.Page(ctx, e.saqueador.Player.ID, 0)
	require.NoError(t, err)
	require.Len(t, perdidas.Entries, 1)
	assert.Equal(t, "UNIT_LOST_TO_CASTLE", perdidas.Entries[0].Kind)
	assert.Equal(t, string(unit.TypeVillager), perdidas.Entries[0].UnitType)
	muertes, _, err := r.Page(ctx, e.victima.Player.ID, 0)
	require.NoError(t, err)
	require.Len(t, muertes.Entries, 1)
	assert.Equal(t, "CASTLE_KILL", muertes.Entries[0].Kind)
}
