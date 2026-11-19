//go:build integration

package postgres_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Integración de arietes y murallas (SPEC-SIEGE-ENGINES §9): el único parcial y
// el trigger del centro de 000029, el derribo de la muralla con su tipo en el
// registro y el ariete en city_productions.

func muralla(cityID int64, at world.Tile) building.Building {
	return building.Building{CityID: cityID, Type: building.TypeWall, X: at.X, Y: at.Y,
		Status: building.StatusUnderConstruction, CompletesAtMs: 1_757_376_120_000}
}

func centroDe(e escenarioEconomico) world.Tile {
	return world.Tile{X: e.jugador.City.CenterX, Y: e.jugador.City.CenterY}
}

// RN-SIEGE-001: una muralla en pie por ciudad (el único parcial) y sólo en el
// centro (el trigger); derribada, se levanta otra. Los rechazos no debitan.
func TestLasRestriccionesDeLaMuralla(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	e.recursos(t, ctx, 1_000, 1_000)
	centro := centroDe(e)

	_, err := e.economia.Build(ctx, "req-fuera", e.jugador.Player.ID,
		muralla(e.ciudad(), world.Tile{X: centro.X + 1, Y: centro.Y}), e.debitos(100, 200, 2), 1)
	assert.ErrorIs(t, err, building.ErrInvalidSite, "fuera del centro: el trigger")
	madera, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	assert.Equal(t, int64(1_000), madera, "el rechazo no debita")

	id, err := e.economia.Build(ctx, "req-1", e.jugador.Player.ID, muralla(e.ciudad(), centro), e.debitos(100, 200, 2), 1)
	require.NoError(t, err)
	piedra, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceStone)
	assert.Equal(t, int64(800), piedra)

	_, err = e.economia.Build(ctx, "req-2", e.jugador.Player.ID, muralla(e.ciudad(), centro), e.debitos(100, 200, 3), 1)
	assert.ErrorIs(t, err, building.ErrInvalidSite, "una en pie por ciudad")

	// Por SQL, sin pasar por el repositorio: el trigger también.
	_, err = store.Pool().Exec(ctx,
		`UPDATE buildings SET x = x + 1 WHERE id = $1`, id)
	assert.True(t, postgres.IsCheckViolation(err), "%v", err)

	require.NoError(t, e.economia.DestroyBuilding(ctx, postgres.BuildingDestruction{
		BuildingID: id, CityID: e.ciudad(), VictimPlayer: e.jugador.Player.ID, AttackerPlayer: e.jugador.Player.ID,
		Type: building.TypeWall, Tick: 2,
	}))
	_, err = e.economia.Build(ctx, "req-3", e.jugador.Player.ID, muralla(e.ciudad(), centro), e.debitos(100, 200, 4), 3)
	assert.NoError(t, err, "con la anterior derribada, otra muralla")
}

// RN-SIEGE-007: el derribo de una muralla completa la deja DESTROYED sin tocar
// population_limit, con su BuildingDestroyed de tipo WALL, y el registro dice a
// los dos lados qué edificio cayó (SPEC-SIEGE-ENGINES Q10).
func TestElDerriboDeLaMurallaNoTocaElLimiteYSeRegistraConSuTipo(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoEscenario(t, ctx, store)
	economia := postgres.NewEconomyRepo(store, e.units, e.movements)
	victima := e.anfitrion
	require.NoError(t, economia.FlushStocks(ctx, []postgres.StockDelta{
		{CityID: victima.City.ID, Resource: economy.ResourceWood, Delta: 100, Version: 1},
		{CityID: victima.City.ID, Resource: economy.ResourceStone, Delta: 200, Version: 1},
	}))
	centro := world.Tile{X: victima.City.CenterX, Y: victima.City.CenterY}
	id, err := economia.Build(ctx, "req-muralla", victima.Player.ID, muralla(victima.City.ID, centro), []postgres.StockDelta{
		{CityID: victima.City.ID, Resource: economy.ResourceWood, Delta: -100, Version: 2},
		{CityID: victima.City.ID, Resource: economy.ResourceStone, Delta: -200, Version: 2},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, economia.CompleteBuilding(ctx, id, victima.City.ID, victima.Player.ID, 0, 2))
	var antes int32
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT population_limit FROM cities WHERE id = $1`, victima.City.ID).Scan(&antes))

	require.NoError(t, economia.DestroyBuilding(ctx, postgres.BuildingDestruction{
		BuildingID: id, CityID: victima.City.ID, VictimPlayer: victima.Player.ID,
		AttackerPlayer: e.visitante.Player.ID, AttackerID: 1, Type: building.TypeWall, At: centro,
		Tick: 3, AtMs: 1_800_000_000_000,
	}))
	var estado string
	var despues int32
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT status FROM buildings WHERE id = $1`, id).Scan(&estado))
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT population_limit FROM cities WHERE id = $1`, victima.City.ID).Scan(&despues))
	assert.Equal(t, "DESTROYED", estado)
	assert.Equal(t, antes, despues, "la muralla no daba población")

	r := registro(store, 50)
	suyo, _, err := r.Page(ctx, victima.Player.ID, 0)
	require.NoError(t, err)
	require.Len(t, suyo.Entries, 1)
	assert.Equal(t, "HOUSE_LOST", suyo.Entries[0].Kind)
	assert.Equal(t, "WALL", suyo.Entries[0].BuildingType)
	autor, _, err := r.Page(ctx, e.visitante.Player.ID, 0)
	require.NoError(t, err)
	require.Len(t, autor.Entries, 1)
	assert.Equal(t, "HOUSE_DESTROYED", autor.Entries[0].Kind)
	assert.Equal(t, "WALL", autor.Entries[0].BuildingType)
}

// RN-SIEGE-003: city_productions admite el ariete, y su encargo debita la madera
// y la piedra en una transacción.
func TestElEncargoDeUnArieteDebitaMaderaYPiedra(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevaEconomia(t, ctx, store)
	_, err := store.Pool().Exec(ctx, `UPDATE cities SET era = 'IRON_AGE' WHERE id = $1`, e.ciudad())
	require.NoError(t, err)
	e.recursos(t, ctx, 150, 75)

	require.NoError(t, e.economia.Train(ctx, "00000000-0000-4000-8000-00000000a1e7", e.jugador.Player.ID,
		e.produccion(unit.TypeRam), 2, e.debitos(150, 75, 2), 9))
	madera, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceWood)
	piedra, _ := stockDe(t, ctx, store, e.ciudad(), economy.ResourceStone)
	assert.Equal(t, [2]int64{0, 0}, [2]int64{madera, piedra})
	var tipo string
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT unit_type FROM city_productions WHERE city_id = $1`,
		e.ciudad()).Scan(&tipo))
	assert.Equal(t, "RAM", tipo)
}
