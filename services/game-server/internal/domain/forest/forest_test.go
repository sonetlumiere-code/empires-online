package forest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/forest"
)

// RN-ECON-013 e INV-ECON-008: la carga es min(carry, remaining) y el tile
// pierde exactamente eso; con el tile vacío no hay carga.
func TestUnaCargaNuncaCreaMadera(t *testing.T) {
	for _, c := range []struct {
		remaining, carry, taken int32
	}{{100, 10, 10}, {7, 10, 7}, {0, 10, 0}, {10, 10, 10}} {
		taken, after := forest.Take(c.remaining, c.carry, 1_000)
		assert.Equal(t, c.taken, taken, "quedan %d, carga %d", c.remaining, c.carry)
		assert.Equal(t, c.remaining, taken+after.Remaining, "lo que se lleva es lo que pierde el tile")
		assert.Equal(t, int64(1_000)+forest.RegrowAfterMs, after.RegrowsAtMs, "vuelve a crecer desde esta carga")
	}
}

// RN-ECON-016: la regeneración se ancla a su instante: un milisegundo antes
// queda lo que quedaba, en el instante exacto está lleno.
func TestLaRegeneracionVenceEnSuInstante(t *testing.T) {
	s := forest.State{Remaining: 0, RegrowsAtMs: 5_000}
	assert.Equal(t, int32(0), s.RemainingAt(4_999))
	assert.False(t, s.Regrown(4_999))
	assert.Equal(t, forest.Capacity, s.RemainingAt(5_000))
	assert.True(t, s.Regrown(5_000))
}

// INV-ECON-007: una entrada fuera de rango se rechaza.
func TestUnaEntradaFueraDeRangoEsInvalida(t *testing.T) {
	require.NoError(t, forest.State{Remaining: 0}.Validate())
	require.NoError(t, forest.State{Remaining: forest.Capacity}.Validate())
	require.ErrorIs(t, forest.State{Remaining: -1}.Validate(), forest.ErrInvalid)
	require.ErrorIs(t, forest.State{Remaining: forest.Capacity + 1}.Validate(), forest.ErrInvalid)
}
