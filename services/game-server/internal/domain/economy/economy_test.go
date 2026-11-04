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

// El catálogo de recursos es el mismo conjunto que el CHECK de las migraciones
// 000006 y 000012.
func TestElCatalogoDeRecursosEsElDelEsquema(t *testing.T) {
	assert.Equal(t, []Resource{ResourceWood, ResourceStone}, Resources())
	for _, r := range Resources() {
		assert.True(t, r.Valid())
	}
	assert.False(t, Resource("GOLD").Valid())
}

func piedra(n int64) Cost { return Cost{Resource: ResourceStone, Amount: n} }

// ADR-030 §3, INV-ECON-002: un coste en dos recursos sólo se cubre si alcanzan
// los dos; con uno solo, no.
func TestUnCosteEnDosRecursosExigeLosDos(t *testing.T) {
	cs := Costs{madera(400), piedra(200)}
	stocks := map[Resource]*Stock{
		ResourceWood:  {Resource: ResourceWood, Amount: 400},
		ResourceStone: {Resource: ResourceStone, Amount: 199},
	}
	of := func(r Resource) *Stock { return stocks[r] }
	assert.False(t, cs.CanAfford(of), "falta una piedra")
	stocks[ResourceStone].Amount = 200
	assert.True(t, cs.CanAfford(of))
	delete(stocks, ResourceStone)
	assert.False(t, cs.CanAfford(of), "sin stock de piedra no se cubre")
}

// Un coste no puede nombrar dos veces el mismo recurso ni uno inventado.
func TestUnCosteInvalidoNoSeCubre(t *testing.T) {
	require.ErrorIs(t, Costs{madera(1), madera(2)}.Validate(), ErrInvalidAmount)
	require.ErrorIs(t, Costs{{Resource: "GOLD", Amount: 1}}.Validate(), ErrInvalidAmount)
	require.ErrorIs(t, Costs{madera(-1)}.Validate(), ErrInvalidAmount)
	require.NoError(t, Costs{madera(400), piedra(200)}.Validate())
	lleno := func(Resource) *Stock { return &Stock{Resource: ResourceWood, Amount: 1_000} }
	assert.False(t, Costs{madera(1), madera(2)}.CanAfford(lleno))
}
