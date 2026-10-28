package economy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func madera(n int64) Cost { return Cost{Resource: ResourceWood, Amount: n} }

// INV-ECON-001: un débito exacto deja cero; uno mayor que el stock se rechaza
// sin mutar ni la cantidad ni la versión.
func TestUnDebitoNuncaDejaElStockNegativo(t *testing.T) {
	s := Stock{Resource: ResourceWood, Amount: 30, Version: 4}

	err := s.Debit(madera(31))
	require.True(t, errors.Is(err, ErrInsufficientResources), "%v", err)
	assert.Equal(t, int64(30), s.Amount)
	assert.Equal(t, int64(4), s.Version, "un rechazo no cuenta como cambio")

	require.NoError(t, s.Debit(madera(30)))
	assert.Equal(t, int64(0), s.Amount)
	assert.Equal(t, int64(5), s.Version)

	require.True(t, errors.Is(s.Debit(madera(1)), ErrInsufficientResources))
	assert.Equal(t, int64(0), s.Amount)
}

func TestCadaCambioSubeLaVersion(t *testing.T) {
	s := NewStock(ResourceWood)
	require.NoError(t, s.Credit(10))
	require.NoError(t, s.Credit(10))
	require.NoError(t, s.Debit(madera(5)))
	assert.Equal(t, int64(15), s.Amount)
	assert.Equal(t, int64(3), s.Version)
}

func TestCantidadesInvalidasNoMutan(t *testing.T) {
	s := Stock{Resource: ResourceWood, Amount: 10}
	assert.True(t, errors.Is(s.Credit(-1), ErrInvalidAmount))
	assert.True(t, errors.Is(s.Debit(madera(-1)), ErrInvalidAmount))
	assert.True(t, errors.Is(s.Debit(Cost{Resource: "STONE", Amount: 1}), ErrInvalidAmount))
	assert.Equal(t, Stock{Resource: ResourceWood, Amount: 10}, s)
}

func TestCanAfford(t *testing.T) {
	s := Stock{Resource: ResourceWood, Amount: 50}
	assert.True(t, s.CanAfford(madera(50)))
	assert.False(t, s.CanAfford(madera(51)))
	assert.False(t, s.CanAfford(madera(-1)))
	assert.False(t, s.CanAfford(Cost{Resource: "STONE", Amount: 1}))
}

// El catálogo de recursos es el mismo conjunto que el CHECK de la migración.
func TestElCatalogoDeRecursosEsElDelEsquema(t *testing.T) {
	assert.Equal(t, []Resource{ResourceWood}, Resources())
	for _, r := range Resources() {
		assert.True(t, r.Valid())
	}
	assert.False(t, Resource("GOLD").Valid())
}
