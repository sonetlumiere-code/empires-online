package production

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

var dueno = uuid.MustParse("11111111-1111-4111-8111-111111111111")

func ciudad(poblacion, limite int32) *city.City {
	return &city.City{ID: 1, OwnerPlayerID: dueno, Population: poblacion, PopulationLimit: limite, Era: city.EraStone}
}

// almacen devuelve un stock por recurso con las cantidades dadas; lo que no
// está, la ciudad no lo tiene.
func almacen(cantidades map[economy.Resource]int64) func(economy.Resource) *economy.Stock {
	return func(r economy.Resource) *economy.Stock {
		n, ok := cantidades[r]
		if !ok {
			return nil
		}
		return &economy.Stock{Resource: r, Amount: n}
	}
}

func madera(n int64) func(economy.Resource) *economy.Stock {
	return almacen(map[economy.Resource]int64{economy.ResourceWood: n})
}

// romana es la civilización de la ciudad de los tests: su unidad es el
// legionario y su bien el vino.
var romana = Civilization{Unit: unit.TypeLegionary, Good: economy.ResourceWine}

// RN-ECON-010: el orden de las respuestas es el de la spec —ciudad,
// producción en curso, población, madera—, y la ciudad ajena no revela nada.
func TestCanTrainRespondeEnElOrdenDeLaSpec(t *testing.T) {
	_, _, err := CanTrain(nil, dueno, unit.TypeVillager, romana, false, madera(100))
	assert.True(t, errors.Is(err, ErrCityNotFound))
	_, _, err = CanTrain(ciudad(3, 20), uuid.New(), unit.TypeVillager, romana, true, madera(0))
	assert.True(t, errors.Is(err, ErrCityNotFound))

	_, _, err = CanTrain(ciudad(20, 20), dueno, unit.TypeVillager, romana, true, madera(0))
	assert.True(t, errors.Is(err, ErrCityBusy), "CITY_BUSY antes que la población")

	_, _, err = CanTrain(ciudad(20, 20), dueno, unit.TypeVillager, romana, false, madera(0))
	assert.True(t, errors.Is(err, city.ErrPopulationLimit), "la población antes que la madera")

	_, _, err = CanTrain(ciudad(19, 20), dueno, unit.TypeVillager, romana, false, madera(49))
	assert.True(t, errors.Is(err, economy.ErrInsufficientResources))

	def, costes, err := CanTrain(ciudad(19, 20), dueno, unit.TypeVillager, romana, false, madera(50))
	require.NoError(t, err)
	assert.Equal(t, economy.Costs{{Resource: economy.ResourceWood, Amount: 50}}, costes, "el aldeano no paga bien")
	assert.Equal(t, int64(20_000), def.TrainMs)
}

func TestUnTipoFueraDelCatalogoNoSeEntrena(t *testing.T) {
	_, _, err := CanTrain(ciudad(3, 20), dueno, "KNIGHT", romana, false, madera(1000))
	assert.True(t, errors.Is(err, unit.ErrUnknownType))
	_, _, err = CanTrain(ciudad(3, 20), dueno, unit.TypeCaravan, romana, false, madera(1000))
	assert.True(t, errors.Is(err, ErrNotTrainable), "la caravana no se entrena")
}

// SPEC-CIVILIZATION-UNITS §5 e INV-UNIT-012: después del tipo van la
// civilización y la era, antes que la producción en curso, la población y los
// recursos; y el coste es madera, piedra y el bien PROPIO.
func TestLaUnidadExclusivaPideSuCivilizacionSuEraYSuBien(t *testing.T) {
	bronce := func(poblacion, limite int32) *city.City {
		c := ciudad(poblacion, limite)
		c.Era = city.EraBronze
		return c
	}
	todo := almacen(map[economy.Resource]int64{
		economy.ResourceWood: 80, economy.ResourceStone: 40, economy.ResourceWine: 10, economy.ResourceFurs: 10,
	})

	// Otra civilización: INVALID_TARGET aunque la ciudad esté ocupada y llena.
	_, _, err := CanTrain(bronce(20, 20), dueno, unit.TypeBerserker, romana, true, madera(0))
	assert.True(t, errors.Is(err, ErrNotYourCivilization), "la civilización antes que todo lo demás: %v", err)

	// La propia, en la Edad de Piedra: la era antes que la producción en curso.
	_, _, err = CanTrain(ciudad(20, 20), dueno, unit.TypeLegionary, romana, true, todo)
	assert.True(t, errors.Is(err, ErrEraRequired), "la era antes que la producción: %v", err)

	_, _, err = CanTrain(bronce(20, 20), dueno, unit.TypeLegionary, romana, true, todo)
	assert.True(t, errors.Is(err, ErrCityBusy))
	_, _, err = CanTrain(bronce(20, 20), dueno, unit.TypeLegionary, romana, false, todo)
	assert.True(t, errors.Is(err, city.ErrPopulationLimit))

	// Cada recurso que falta, por separado; las pieles (un bien ajeno) no pagan.
	for nombre, falta := range map[string]map[economy.Resource]int64{
		"madera": {economy.ResourceWood: 79, economy.ResourceStone: 40, economy.ResourceWine: 10},
		"piedra": {economy.ResourceWood: 80, economy.ResourceStone: 39, economy.ResourceWine: 10},
		"vino":   {economy.ResourceWood: 80, economy.ResourceStone: 40, economy.ResourceWine: 9, economy.ResourceFurs: 99},
		"nada":   {},
	} {
		_, _, err = CanTrain(bronce(3, 20), dueno, unit.TypeLegionary, romana, false, almacen(falta))
		assert.True(t, errors.Is(err, economy.ErrInsufficientResources), "%s: %v", nombre, err)
	}

	def, costes, err := CanTrain(bronce(3, 20), dueno, unit.TypeLegionary, romana, false, todo)
	require.NoError(t, err)
	assert.Equal(t, unit.TypeLegionary, def.Type)
	assert.Equal(t, economy.Costs{
		{Resource: economy.ResourceWood, Amount: 80},
		{Resource: economy.ResourceStone, Amount: 40},
		{Resource: economy.ResourceWine, Amount: 10},
	}, costes)
	assert.Equal(t, int64(35_000), def.TrainMs)

	// En una era posterior también vale (AtLeast), y el guerrero no pide nada.
	hierro := bronce(3, 20)
	hierro.Era = city.EraIron
	_, _, err = CanTrain(hierro, dueno, unit.TypeLegionary, romana, false, todo)
	assert.NoError(t, err)
	_, _, err = CanTrain(ciudad(3, 20), dueno, unit.TypeWarrior, Civilization{}, false, madera(60))
	assert.NoError(t, err, "el guerrero no pide civilización ni era")

	// Sin catálogo de civilizaciones no hay bien con que pagar.
	_, _, err = CanTrain(bronce(3, 20), dueno, unit.TypeLegionary, Civilization{Unit: unit.TypeLegionary}, false, todo)
	assert.True(t, errors.Is(err, economy.ErrInsufficientResources))
}

// Una producción reserva plaza hasta que nace su unidad, y vence una sola vez.
func TestUnaProduccionEstaEnCursoHastaQueNaceSuUnidad(t *testing.T) {
	p := Production{CityID: 1, UnitType: unit.TypeVillager, CompletesAtMs: 2000}
	assert.True(t, p.InProgress())
	assert.False(t, p.Due(1999))
	assert.True(t, p.Due(2000))
	p.UnitID = 42
	assert.False(t, p.InProgress())
	assert.False(t, p.Due(9999), "nacida, no vuelve a vencer")
}
