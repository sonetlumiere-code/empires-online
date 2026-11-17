package advance_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/advance"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
)

var dueno = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")

func stocks(madera, piedra int64) func(economy.Resource) *economy.Stock {
	return almacen(map[economy.Resource]int64{economy.ResourceWood: madera, economy.ResourceStone: piedra})
}

func almacen(cantidades map[economy.Resource]int64) func(economy.Resource) *economy.Stock {
	m := map[economy.Resource]*economy.Stock{}
	for r, n := range cantidades {
		m[r] = &economy.Stock{Resource: r, Amount: n}
	}
	return func(r economy.Resource) *economy.Stock { return m[r] }
}

// El orden del catálogo de civilizaciones: vino, seda, especias, pieles.
var orden = economy.Goods()

// SPEC-ECON-STONE Q5: de Piedra a Bronce, por 400 de madera y 200 de piedra en
// 90 s; SPEC-CIVILIZATION-GOODS Q5: de Bronce a Hierro, por 800, 400 y 60
// bienes ajenos en 180 s; SPEC-CASTLE-AGE Q1, Q2 y Q10: de Hierro a Castillos,
// por 1 600, 800 y 120 bienes ajenos en 360 s; desde Castillos, nada.
func TestLosPasosDeEraSonLosTresDelCatalogo(t *testing.T) {
	s, ok := advance.Next(city.EraStone)
	require.True(t, ok)
	assert.Equal(t, city.EraBronze, s.To)
	assert.Equal(t, economy.Costs{
		{Resource: economy.ResourceWood, Amount: 400},
		{Resource: economy.ResourceStone, Amount: 200},
	}, s.Costs)
	assert.Equal(t, int64(90_000), s.DurationMs)
	assert.Zero(t, s.ForeignGoods)

	h, ok := advance.Next(city.EraBronze)
	require.True(t, ok)
	assert.Equal(t, city.EraIron, h.To)
	assert.Equal(t, economy.Costs{
		{Resource: economy.ResourceWood, Amount: 800},
		{Resource: economy.ResourceStone, Amount: 400},
	}, h.Costs)
	assert.Equal(t, int64(60), h.ForeignGoods)
	assert.Equal(t, int64(180_000), h.DurationMs)

	c, ok := advance.Next(city.EraIron)
	require.True(t, ok)
	assert.Equal(t, city.EraCastle, c.To)
	assert.Equal(t, economy.Costs{
		{Resource: economy.ResourceWood, Amount: 1_600},
		{Resource: economy.ResourceStone, Amount: 800},
	}, c.Costs)
	assert.Equal(t, int64(120), c.ForeignGoods)
	assert.Equal(t, int64(360_000), c.DurationMs)

	_, ok = advance.Next(city.EraCastle)
	assert.False(t, ok, "desde Castillos no hay avance")
}

// INV-ECON-011 y RN-CASTLE-001: el avance a Castillos pide 120 bienes ajenos en
// cualquier mezcla y nunca cuenta el propio, aunque sobre.
func TestCanAdvanceACastillosResuelveCientoVeinteBienesAjenos(t *testing.T) {
	c := &city.City{ID: 7, OwnerPlayerID: dueno, Era: city.EraIron}
	base := map[economy.Resource]int64{economy.ResourceWood: 1_600, economy.ResourceStone: 800}
	con := func(extra map[economy.Resource]int64) func(economy.Resource) *economy.Stock {
		m := map[economy.Resource]int64{}
		for r, n := range base {
			m[r] = n
		}
		for r, n := range extra {
			m[r] = n
		}
		return almacen(m)
	}

	_, err := advance.CanAdvance(c, dueno, false, economy.ResourceFurs, orden,
		con(map[economy.Resource]int64{economy.ResourceFurs: 500, economy.ResourceWine: 119}))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources, "119 ajenos no llegan y las pieles propias no cuentan")

	s, err := advance.CanAdvance(c, dueno, false, economy.ResourceFurs, orden,
		con(map[economy.Resource]int64{economy.ResourceFurs: 500, economy.ResourceWine: 30, economy.ResourceSilk: 40, economy.ResourceSpices: 90}))
	require.NoError(t, err)
	assert.Equal(t, city.EraCastle, s.To)
	assert.Equal(t, economy.Costs{
		{Resource: economy.ResourceWood, Amount: 1_600},
		{Resource: economy.ResourceStone, Amount: 800},
		{Resource: economy.ResourceWine, Amount: 30},
		{Resource: economy.ResourceSilk, Amount: 40},
		{Resource: economy.ResourceSpices, Amount: 50},
	}, s.Costs, "en el orden del catálogo, sin pieles")
	_, err = advance.CanAdvance(c, dueno, false, economy.ResourceFurs, orden,
		almacen(map[economy.Resource]int64{economy.ResourceWood: 1_599, economy.ResourceStone: 800, economy.ResourceWine: 120}))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)
}

// INV-ECON-011 y RN-GOODS-006: los 60 bienes ajenos se toman en el orden del
// catálogo, saltando el propio, de cada uno lo que haya hasta completar.
func TestResolveTomaBienesAjenosEnOrdenYNuncaElPropio(t *testing.T) {
	paso, _ := advance.Next(city.EraBronze)
	fijos := economy.Costs{{Resource: economy.ResourceWood, Amount: 800}, {Resource: economy.ResourceStone, Amount: 400}}

	// Un romano con 100 de vino propio, 25 de seda y 50 de pieles: 25 de seda y 35 de pieles.
	costes, err := advance.Resolve(paso, economy.ResourceWine, orden, almacen(map[economy.Resource]int64{
		economy.ResourceWine: 100, economy.ResourceSilk: 25, economy.ResourceFurs: 50,
	}))
	require.NoError(t, err)
	assert.Equal(t, append(append(economy.Costs(nil), fijos...),
		economy.Cost{Resource: economy.ResourceSilk, Amount: 25},
		economy.Cost{Resource: economy.ResourceFurs, Amount: 35},
	), costes)

	// Un nórdico con 60 de vino: todo de vino.
	costes, err = advance.Resolve(paso, economy.ResourceFurs, orden, almacen(map[economy.Resource]int64{
		economy.ResourceWine: 60, economy.ResourceFurs: 500,
	}))
	require.NoError(t, err)
	assert.Equal(t, economy.Cost{Resource: economy.ResourceWine, Amount: 60}, costes[len(costes)-1])
	assert.Len(t, costes, 3)

	// El propio no cuenta: 500 de pieles y 59 ajenos no llegan.
	_, err = advance.Resolve(paso, economy.ResourceFurs, orden, almacen(map[economy.Resource]int64{
		economy.ResourceFurs: 500, economy.ResourceSpices: 59,
	}))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)
}

// CanAdvance desde Bronce devuelve el paso con los bienes ya resueltos, y
// rechaza sin los 60 ajenos aunque sobren madera y piedra.
func TestCanAdvanceAHierroResuelveLosBienes(t *testing.T) {
	c := &city.City{ID: 7, OwnerPlayerID: dueno, Era: city.EraBronze}
	sin := almacen(map[economy.Resource]int64{economy.ResourceWood: 800, economy.ResourceStone: 400, economy.ResourceWine: 999})
	_, err := advance.CanAdvance(c, dueno, false, economy.ResourceWine, orden, sin)
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)

	con := almacen(map[economy.Resource]int64{economy.ResourceWood: 800, economy.ResourceStone: 400, economy.ResourceSpices: 60})
	s, err := advance.CanAdvance(c, dueno, false, economy.ResourceWine, orden, con)
	require.NoError(t, err)
	assert.Equal(t, city.EraIron, s.To)
	assert.Equal(t, economy.Cost{Resource: economy.ResourceSpices, Amount: 60}, s.Costs[2])
}

// RN-ECON-024, en su orden: ciudad propia, avance disponible, ciudad libre y
// los dos recursos.
func TestRechazosDelAvance(t *testing.T) {
	c := &city.City{ID: 7, OwnerPlayerID: dueno, Era: city.EraStone}

	_, err := advance.CanAdvance(nil, dueno, false, economy.ResourceWine, orden, stocks(400, 200))
	assert.ErrorIs(t, err, advance.ErrCityNotFound)
	_, err = advance.CanAdvance(c, uuid.New(), false, economy.ResourceWine, orden, stocks(400, 200))
	assert.ErrorIs(t, err, advance.ErrCityNotFound, "una ciudad ajena responde como una inexistente")

	castillos := *c
	castillos.Era = city.EraCastle
	_, err = advance.CanAdvance(&castillos, dueno, true, economy.ResourceWine, orden, stocks(0, 0))
	assert.ErrorIs(t, err, advance.ErrUnavailable, "la era va antes que la plaza y el stock")

	_, err = advance.CanAdvance(c, dueno, true, economy.ResourceWine, orden, stocks(400, 200))
	assert.ErrorIs(t, err, advance.ErrCityBusy)
	_, err = advance.CanAdvance(c, dueno, false, economy.ResourceWine, orden, stocks(399, 200))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)
	_, err = advance.CanAdvance(c, dueno, false, economy.ResourceWine, orden, stocks(400, 199))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)

	s, err := advance.CanAdvance(c, dueno, false, economy.ResourceWine, orden, stocks(400, 200))
	require.NoError(t, err)
	assert.Equal(t, city.EraBronze, s.To)
}

// INV-CITY-018: una fila de avance que no es un paso del catálogo es inválida:
// ni hacia atrás ni saltando eras.
func TestUnAvanceFueraDelCatalogoEsInvalido(t *testing.T) {
	require.NoError(t, advance.Advance{From: city.EraStone, To: city.EraBronze}.Validate())
	assert.ErrorIs(t, advance.Advance{From: city.EraBronze, To: city.EraStone}.Validate(), advance.ErrInvalid)
	assert.ErrorIs(t, advance.Advance{From: city.EraStone, To: city.EraIron}.Validate(), advance.ErrInvalid)
	require.NoError(t, advance.Advance{From: city.EraBronze, To: city.EraIron}.Validate())
	require.NoError(t, advance.Advance{From: city.EraIron, To: city.EraCastle}.Validate())
	assert.ErrorIs(t, advance.Advance{From: city.EraBronze, To: city.EraCastle}.Validate(), advance.ErrInvalid)
	assert.ErrorIs(t, advance.Advance{From: city.EraCastle, To: city.EraIron}.Validate(), advance.ErrInvalid)
}

// Next devuelve una copia: quien la recibe no puede cambiar el catálogo.
func TestElCatalogoNoSeCambiaDesdeFuera(t *testing.T) {
	s, _ := advance.Next(city.EraStone)
	s.Costs[0].Amount = 1
	otra, _ := advance.Next(city.EraStone)
	assert.Equal(t, int64(400), otra.Costs[0].Amount)
}
