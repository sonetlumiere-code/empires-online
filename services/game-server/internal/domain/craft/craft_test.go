package craft_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/craft"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
)

var dueno = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")

func stocks(madera, piedra int64) func(economy.Resource) *economy.Stock {
	m := map[economy.Resource]*economy.Stock{
		economy.ResourceWood:  {Resource: economy.ResourceWood, Amount: madera},
		economy.ResourceStone: {Resource: economy.ResourceStone, Amount: piedra},
	}
	return func(r economy.Resource) *economy.Stock { return m[r] }
}

// SPEC-CIVILIZATION-GOODS Q7: 30 de madera y 15 de piedra por 5 del bien, 30 s.
func TestLaElaboracionTieneLosValoresDeLaSpec(t *testing.T) {
	assert.Equal(t, economy.Costs{
		{Resource: economy.ResourceWood, Amount: 30},
		{Resource: economy.ResourceStone, Amount: 15},
	}, craft.Costs())
	assert.Equal(t, int32(5), craft.Amount)
	assert.Equal(t, int64(30_000), craft.DurationMs)
}

// RN-GOODS-004, en su orden: ciudad propia, taller completo, taller libre y
// los dos recursos.
func TestRechazosDeLaElaboracion(t *testing.T) {
	c := &city.City{ID: 7, OwnerPlayerID: dueno, Era: city.EraBronze}

	_, err := craft.CanCraft(nil, dueno, true, false, stocks(30, 15))
	assert.ErrorIs(t, err, craft.ErrCityNotFound)
	_, err = craft.CanCraft(c, uuid.New(), true, false, stocks(30, 15))
	assert.ErrorIs(t, err, craft.ErrCityNotFound, "una ciudad ajena responde como una inexistente")
	_, err = craft.CanCraft(c, dueno, false, true, stocks(0, 0))
	assert.ErrorIs(t, err, craft.ErrNoWorkshop, "el taller va antes que la plaza y el stock")
	_, err = craft.CanCraft(c, dueno, true, true, stocks(30, 15))
	assert.ErrorIs(t, err, craft.ErrBusy)
	_, err = craft.CanCraft(c, dueno, true, false, stocks(29, 15))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)
	_, err = craft.CanCraft(c, dueno, true, false, stocks(30, 14))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)

	costes, err := craft.CanCraft(c, dueno, true, false, stocks(30, 15))
	require.NoError(t, err)
	assert.Equal(t, craft.Costs(), costes)
}

// INV-ECON-009: una fila de elaboración sólo vale con un bien y una cantidad.
func TestUnaElaboracionLeidaTieneQueSerDeUnBien(t *testing.T) {
	require.NoError(t, craft.Craft{Good: economy.ResourceFurs, Amount: 5}.Validate())
	assert.ErrorIs(t, craft.Craft{Good: economy.ResourceWood, Amount: 5}.Validate(), craft.ErrInvalid)
	assert.ErrorIs(t, craft.Craft{Good: economy.ResourceWine, Amount: 0}.Validate(), craft.ErrInvalid)
}
