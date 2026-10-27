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
	return &city.City{ID: 1, OwnerPlayerID: dueno, Population: poblacion, PopulationLimit: limite}
}

func madera(n int64) *economy.Stock { return &economy.Stock{Resource: economy.ResourceWood, Amount: n} }

// RN-ECON-010: el orden de las respuestas es el de la spec —ciudad,
// producción en curso, población, madera—, y la ciudad ajena no revela nada.
func TestCanTrainRespondeEnElOrdenDeLaSpec(t *testing.T) {
	_, err := CanTrain(nil, dueno, unit.TypeVillager, false, madera(100))
	assert.True(t, errors.Is(err, ErrCityNotFound))
	_, err = CanTrain(ciudad(3, 20), uuid.New(), unit.TypeVillager, true, madera(0))
	assert.True(t, errors.Is(err, ErrCityNotFound))

	_, err = CanTrain(ciudad(20, 20), dueno, unit.TypeVillager, true, madera(0))
	assert.True(t, errors.Is(err, ErrCityBusy), "CITY_BUSY antes que la población")

	_, err = CanTrain(ciudad(20, 20), dueno, unit.TypeVillager, false, madera(0))
	assert.True(t, errors.Is(err, city.ErrPopulationLimit), "la población antes que la madera")

	_, err = CanTrain(ciudad(19, 20), dueno, unit.TypeVillager, false, madera(49))
	assert.True(t, errors.Is(err, economy.ErrInsufficientResources))

	def, err := CanTrain(ciudad(19, 20), dueno, unit.TypeVillager, false, madera(50))
	require.NoError(t, err)
	assert.Equal(t, int64(50), def.TrainCost.Amount)
	assert.Equal(t, int64(20_000), def.TrainMs)
}

func TestUnTipoFueraDelCatalogoNoSeEntrena(t *testing.T) {
	_, err := CanTrain(ciudad(3, 20), dueno, "KNIGHT", false, madera(1000))
	assert.True(t, errors.Is(err, unit.ErrUnknownType))
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
