//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/founding"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// Integración del multiciudad (SPEC-MULTI-CITY §9 «integration», ADR-051): el
// hueco único de la migración 000031, la transacción de la fundación y la caída
// de una de dos ciudades, contra PostgreSQL real. Usa el mundo de nuevaCaida: el
// vencido en (10,10), el vencedor en (50,50) y el suroeste (10,50) libre.

var sitioSegunda = world.Tile{X: 10, Y: 50}

// conAlmacen deja en el almacén de la ciudad madera y piedra, y devuelve las
// versiones con que quedan. La deja además en Bronce: la que paga lo pide
// (RN-CITY-030) y, capital, su era da el tope de dos (SPEC-MORE-CITIES RN-CITY-040).
func conAlmacen(t *testing.T, ctx context.Context, store *postgres.Store, cityID int64, madera, piedra int64) (int64, int64) {
	t.Helper()
	_, err := store.Pool().Exec(ctx, `UPDATE cities SET era = 'BRONZE_AGE' WHERE id = $1`, cityID)
	require.NoError(t, err)
	var vm, vp int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`UPDATE city_stocks SET amount = $2, version = version + 1 WHERE city_id = $1 AND resource = 'WOOD' RETURNING version`,
		cityID, madera).Scan(&vm))
	require.NoError(t, store.Pool().QueryRow(ctx,
		`UPDATE city_stocks SET amount = $2, version = version + 1 WHERE city_id = $1 AND resource = 'STONE' RETURNING version`,
		cityID, piedra).Scan(&vp))
	return vm, vp
}

func (c caidaDePrueba) fundacion(t *testing.T) cityfall.Founding {
	return cityfall.Founding{
		OwnerID: c.vencido.Player.ID, FromCityID: c.vencido.City.ID, UnitID: c.vencido.Units[0].ID,
		Name: city.SecondCityName(c.vencido.City.Name), Era: city.EraBronze, Tick: 11, Now: c.ahora,
		AtMs: c.ahora.UnixMilli(),
		Place: func(existing []world.Tile) (cityfall.Site, error) {
			assert.Contains(t, existing, c.vencido.City.Center(), "las ciudades en pie, para la distancia")
			return cityfall.Site{Center: sitioSegunda, TerritoryID: territorioEn(t, c.sembrados, sitioSegunda.X, sitioSegunda.Y).ID,
				Spawns: []world.Tile{{X: sitioSegunda.X + 2, Y: sitioSegunda.Y}, {X: sitioSegunda.X - 2, Y: sitioSegunda.Y}}}, nil
		},
		Verify: founding.FarEnough,
	}
}

func cobro(cityID, vm, vp int64) []postgres.StockDelta {
	return []postgres.StockDelta{
		{CityID: cityID, Resource: economy.ResourceWood, Delta: -800, Version: vm + 1},
		{CityID: cityID, Resource: economy.ResourceStone, Delta: -400, Version: vp + 1},
	}
}

// INV-CITY-022 en la base: dos ciudades en pie del mismo dueño caben en dos
// huecos; la tercera, no.
func TestLaBaseDejaUnaCiudadPorHuecoYCuatroHuecos(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	db := store.Pool()

	_, err := db.Exec(ctx,
		`INSERT INTO cities (owner_player_id, name, center_x, center_y, era, population_limit, last_online_at, slot)
		 VALUES ($1, 'segunda', 40, 10, 'STONE_AGE', 20, now(), 2)`, c.vencido.Player.ID)
	require.NoError(t, err, "el hueco 2 está libre")
	_, err = db.Exec(ctx,
		`INSERT INTO cities (owner_player_id, name, center_x, center_y, era, population_limit, last_online_at, slot)
		 VALUES ($1, 'tercera', 40, 40, 'STONE_AGE', 20, now(), 2)`, c.vencido.Player.ID)
	assert.Equal(t, "cities_one_standing_per_owner", constraintDe(t, err), "el hueco 2 ya está en pie")
	// SPEC-MORE-CITIES: los huecos 3 y 4 existen; el 5, no (INV-CITY-022).
	for i, slot := range []int{3, 4} {
		_, err = db.Exec(ctx,
			`INSERT INTO cities (owner_player_id, name, center_x, center_y, era, population_limit, last_online_at, slot)
			 VALUES ($1, 'otra', 40, $2, 'STONE_AGE', 20, now(), $3)`, c.vencido.Player.ID, 40+24*i, slot)
		require.NoError(t, err, "el hueco %d está libre", slot)
	}
	_, err = db.Exec(ctx,
		`INSERT INTO cities (owner_player_id, name, center_x, center_y, era, population_limit, last_online_at, slot)
		 VALUES ($1, 'quinta', 20, 40, 'STONE_AGE', 20, now(), 5)`, c.vencido.Player.ID)
	assert.Equal(t, "cities_slot_valid", constraintDe(t, err), "no hay hueco 5")

	capital, err := c.cities.GetByOwner(ctx, c.vencido.Player.ID)
	require.NoError(t, err)
	assert.Equal(t, c.vencido.City.ID, capital.ID, "RN-CITY-033: GetByOwner es la capital, la de id menor")
	assert.Equal(t, int16(1), capital.Slot)
}

// ADR-051 §4, RN-CITY-030 a RN-CITY-032: la fundación cobra, crea la ciudad en
// el hueco 2 con el fundador y dos aldeanos nuevos, gana su territorio libre y
// escribe CityFounded, todo en una transacción. Sin almacén no escribe nada, y
// una tercera ya no cabe.
func TestLaFundacionCreaLaSegundaCiudadEnUnaTransaccion(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	db := store.Pool()
	desde := c.vencido.City.ID

	vm, vp := conAlmacen(t, ctx, store, desde, 799, 400)
	_, err := c.repo.FoundCity(ctx, c.fundacion(t), cobro(desde, vm, vp))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)
	var n int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM cities WHERE owner_player_id = $1`, c.vencido.Player.ID).Scan(&n))
	assert.Equal(t, 1, n, "sin almacén no se escribe nada")

	vm, vp = conAlmacen(t, ctx, store, desde, 800, 400)
	res, err := c.repo.FoundCity(ctx, c.fundacion(t), cobro(desde, vm, vp))
	require.NoError(t, err)
	nueva := res.City
	require.NotNil(t, nueva)
	assert.Equal(t, int16(2), nueva.Slot)
	assert.Equal(t, "vencidopolis II", nueva.Name)
	assert.Equal(t, city.EraBronze, nueva.Era)
	assert.Equal(t, sitioSegunda, nueva.Center())
	assert.Equal(t, int32(3), nueva.Population, "el fundador y dos aldeanos")
	assert.Equal(t, int32(2), res.FromPopulation, "la que paga pierde al fundador")
	require.Len(t, res.Villagers, 2)
	require.NotNil(t, res.Claimed, "RN-TERR-007: gana su territorio libre")
	assert.Equal(t, territory.OwnerPlayer, res.Claimed.OwnerType)

	var ciudadFundador int64
	require.NoError(t, db.QueryRow(ctx, `SELECT city_id FROM units WHERE id = $1`, c.vencido.Units[0].ID).Scan(&ciudadFundador))
	assert.Equal(t, nueva.ID, ciudadFundador)
	var madera, piedra int64
	require.NoError(t, db.QueryRow(ctx,
		`SELECT (SELECT amount FROM city_stocks WHERE city_id = $1 AND resource = 'WOOD'),
		        (SELECT amount FROM city_stocks WHERE city_id = $1 AND resource = 'STONE')`, desde).Scan(&madera, &piedra))
	assert.Zero(t, madera)
	assert.Zero(t, piedra)
	require.NoError(t, db.QueryRow(ctx,
		`SELECT count(*) FROM world_events WHERE event_type = 'CityFounded' AND player_id = $1
		   AND counterpart_player_id IS NULL AND (payload->>'cityId')::bigint = $2`, c.vencido.Player.ID, nueva.ID).Scan(&n))
	assert.Equal(t, 1, n)

	capital, err := c.cities.GetByOwner(ctx, c.vencido.Player.ID)
	require.NoError(t, err)
	assert.Equal(t, desde, capital.ID, "la capital sigue siendo la primera")

	// RN-CITY-031: con dos, otra fundación no cabe y no escribe nada.
	otra := c.fundacion(t)
	otra.UnitID = c.vencido.Units[1].ID
	_, err = c.repo.FoundCity(ctx, otra, nil)
	assert.ErrorIs(t, err, cityfall.ErrFoundRejected)
	require.NoError(t, db.QueryRow(ctx,
		`SELECT count(*) FROM cities WHERE owner_player_id = $1 AND fallen_at IS NULL`, c.vencido.Player.ID).Scan(&n))
	assert.Equal(t, 2, n)
}

// RN-CITY-035, INV-CITY-023: con otra ciudad en pie, la capital cae sin
// refundar, lo suyo pasa a la que queda y ésa es la capital; cuando cae la
// última, se refunda.
func TestUnaCaidaConOtraEnPieNoRefunda(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaCaida(t, ctx, store)
	db := store.Pool()
	desde := c.vencido.City.ID
	vm, vp := conAlmacen(t, ctx, store, desde, 800, 400)
	fundada, err := c.repo.FoundCity(ctx, c.fundacion(t), cobro(desde, vm, vp))
	require.NoError(t, err)
	segunda := fundada.City

	id, err := c.repo.StartAssault(ctx, c.asalto("r1"))
	require.NoError(t, err)
	caida := c.caida(t, id, world.Tile{X: 40, Y: 20})
	caida.Place = func([]world.Tile) (cityfall.Site, error) {
		t.Fatal("con otra ciudad en pie no se busca sitio de refundación")
		return cityfall.Site{}, nil
	}
	res, err := c.repo.FallCity(ctx, caida)
	require.NoError(t, err)
	assert.Nil(t, res.City, "no hay refundada")
	assert.Equal(t, segunda.ID, res.KeptCityID)
	assert.Equal(t, int32(5), res.KeptPopulation, "sus tres y los dos de la caída")

	var enPie []int64
	rows, err := db.Query(ctx, `SELECT id FROM cities WHERE owner_player_id = $1 AND fallen_at IS NULL ORDER BY id`, c.vencido.Player.ID)
	require.NoError(t, err)
	for rows.Next() {
		var x int64
		require.NoError(t, rows.Scan(&x))
		enPie = append(enPie, x)
	}
	rows.Close()
	assert.Equal(t, []int64{segunda.ID}, enPie, "INV-CITY-020: le queda una")
	var n int
	require.NoError(t, db.QueryRow(ctx,
		`SELECT count(*) FROM units WHERE city_id = $1 AND status <> 'DEAD'`, desde).Scan(&n))
	assert.Zero(t, n, "las unidades vivas de la caída pasan a la que queda")
	require.NoError(t, db.QueryRow(ctx,
		`SELECT count(*) FROM world_events WHERE event_type = 'CityFell' AND (payload->>'newCityId')::bigint = $1`,
		segunda.ID).Scan(&n))
	assert.Equal(t, 1, n)
	capital, err := c.cities.GetByOwner(ctx, c.vencido.Player.ID)
	require.NoError(t, err)
	assert.Equal(t, segunda.ID, capital.ID, "la que queda es la capital")

	// Cae la última: se refunda.
	ultimo := c.asalto("r2")
	ultimo.Assault.CityID, ultimo.Assault.X, ultimo.Assault.Y = segunda.ID, sitioSegunda.X+2, sitioSegunda.Y
	id2, err := c.repo.StartAssault(ctx, ultimo)
	require.NoError(t, err)
	fin := c.caida(t, id2, world.Tile{X: 40, Y: 20})
	fin.CityID, fin.Center, fin.Name = segunda.ID, segunda.Center(), segunda.Name
	fin.Place = func([]world.Tile) (cityfall.Site, error) {
		s := world.Tile{X: 40, Y: 20}
		return cityfall.Site{Center: s, Spawns: []world.Tile{{X: 42, Y: 20}, {X: 38, Y: 20}, {X: 40, Y: 22}}}, nil
	}
	res, err = c.repo.FallCity(ctx, fin)
	require.NoError(t, err)
	require.NotNil(t, res.City, "la última refunda")
	assert.Zero(t, res.KeptCityID)
	assert.Equal(t, int16(1), res.City.Slot)
}
