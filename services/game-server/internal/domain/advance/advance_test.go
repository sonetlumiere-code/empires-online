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
	m := map[economy.Resource]*economy.Stock{
		economy.ResourceWood:  {Resource: economy.ResourceWood, Amount: madera},
		economy.ResourceStone: {Resource: economy.ResourceStone, Amount: piedra},
	}
	return func(r economy.Resource) *economy.Stock { return m[r] }
}

// SPEC-ECON-STONE Q5 y Q6: el único paso es de Piedra a Bronce, por 400 de
// madera y 200 de piedra en 90 s.
func TestElUnicoPasoEsDePiedraABronce(t *testing.T) {
	s, ok := advance.Next(city.EraStone)
	require.True(t, ok)
	assert.Equal(t, city.EraBronze, s.To)
	assert.Equal(t, economy.Costs{
		{Resource: economy.ResourceWood, Amount: 400},
		{Resource: economy.ResourceStone, Amount: 200},
	}, s.Costs)
	assert.Equal(t, int64(90_000), s.DurationMs)
	for _, e := range []city.Era{city.EraBronze, city.EraIron, city.EraCastle} {
		_, ok := advance.Next(e)
		assert.False(t, ok, "desde %s no hay avance", e)
	}
}

// RN-ECON-024, en su orden: ciudad propia, avance disponible, ciudad libre y
// los dos recursos.
func TestRechazosDelAvance(t *testing.T) {
	c := &city.City{ID: 7, OwnerPlayerID: dueno, Era: city.EraStone}

	_, err := advance.CanAdvance(nil, dueno, false, stocks(400, 200))
	assert.ErrorIs(t, err, advance.ErrCityNotFound)
	_, err = advance.CanAdvance(c, uuid.New(), false, stocks(400, 200))
	assert.ErrorIs(t, err, advance.ErrCityNotFound, "una ciudad ajena responde como una inexistente")

	bronce := *c
	bronce.Era = city.EraBronze
	_, err = advance.CanAdvance(&bronce, dueno, true, stocks(0, 0))
	assert.ErrorIs(t, err, advance.ErrUnavailable, "la era va antes que la plaza y el stock")

	_, err = advance.CanAdvance(c, dueno, true, stocks(400, 200))
	assert.ErrorIs(t, err, advance.ErrCityBusy)
	_, err = advance.CanAdvance(c, dueno, false, stocks(399, 200))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)
	_, err = advance.CanAdvance(c, dueno, false, stocks(400, 199))
	assert.ErrorIs(t, err, economy.ErrInsufficientResources)

	s, err := advance.CanAdvance(c, dueno, false, stocks(400, 200))
	require.NoError(t, err)
	assert.Equal(t, city.EraBronze, s.To)
}

// INV-CITY-018: una fila de avance que no es un paso del catálogo es inválida:
// ni hacia atrás ni saltando eras.
func TestUnAvanceFueraDelCatalogoEsInvalido(t *testing.T) {
	require.NoError(t, advance.Advance{From: city.EraStone, To: city.EraBronze}.Validate())
	assert.ErrorIs(t, advance.Advance{From: city.EraBronze, To: city.EraStone}.Validate(), advance.ErrInvalid)
	assert.ErrorIs(t, advance.Advance{From: city.EraStone, To: city.EraIron}.Validate(), advance.ErrInvalid)
}

// Next devuelve una copia: quien la recibe no puede cambiar el catálogo.
func TestElCatalogoNoSeCambiaDesdeFuera(t *testing.T) {
	s, _ := advance.Next(city.EraStone)
	s.Costs[0].Amount = 1
	otra, _ := advance.Next(city.EraStone)
	assert.Equal(t, int64(400), otra.Costs[0].Amount)
}
