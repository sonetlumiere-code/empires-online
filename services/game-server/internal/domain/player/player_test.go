package player

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

// INV-PLAYER-006, mitad del borde: la regla es ^[A-Za-z0-9_-]{3,24}$. La misma
// tabla se contrasta con el CHECK de PostgreSQL en la suite de integración.
func TestValidateUsernameAplicaLaReglaDelEsquema(t *testing.T) {
	validos := []string{
		"abc", "ABC", "a_b", "a-b", "jugador_uno", "Z9-_", strings.Repeat("x", 24),
	}
	invalidos := []string{
		"", "ab", strings.Repeat("x", 25),
		"con espacio", "punto.final", "arroba@", "barra/", "ñandú", "café",
		"emoji😀", "tab\tulador", "zero​width",
	}
	for _, n := range validos {
		assert.NoError(t, ValidateUsername(n), "%q debería ser válido", n)
	}
	for _, n := range invalidos {
		err := ValidateUsername(n)
		assert.True(t, errors.Is(err, ErrInvalidUsername), "%q debería rechazarse, llegó %v", n, err)
	}
}

// La longitud se cuenta en caracteres de la clase permitida, y un carácter
// multibyte nunca la completa: «ñño» tiene 3 runas pero no es válido.
func TestUnNombreConCaracteresMultibyteNoPasaPorLongitud(t *testing.T) {
	assert.Error(t, ValidateUsername("ñño"))
	assert.Error(t, ValidateUsername("abñ"))
}

// T-PLAYER-U-001/002: una clave ausente vale 1000, una presente su entero.
func TestTraitDevuelveElValorODelPorDefecto(t *testing.T) {
	c := Civilization{Traits: map[string]int32{TraitMoveSpeed: 1100}}
	assert.Equal(t, int32(1100), c.Trait(TraitMoveSpeed))
	assert.Equal(t, TraitNeutral, c.Trait(TraitHouseHP), "un rasgo ausente es neutro")
	assert.Equal(t, TraitNeutral, Civilization{}.Trait(TraitMoveSpeed), "sin rasgos, también")
}

// INV-PLAYER-012 y RN-CIV-004: las dos fórmulas con los valores de la tabla de
// SPEC-CIVILIZATION §4.2, al milisegundo y a la unidad.
func TestLasFormulasDeLosRasgosDanLosValoresDeLaSpec(t *testing.T) {
	duraciones := []struct {
		base     int64
		perMille int32
		want     int64
	}{
		{600, 1000, 600}, {600, 950, 632}, {600, 1050, 571}, {600, 1100, 545},
		{15_000, 1100, 13_636}, {20_000, 1100, 18_182}, {25_000, 1100, 22_727},
		{15_000, 950, 15_789}, {20_000, 950, 21_053}, {25_000, 950, 26_316},
		{15_000, 900, 16_667}, {20_000, 900, 22_222}, {25_000, 900, 27_778},
		{10_000, 1100, 9_091},
		{1, 1_000_000, 1}, // nunca baja de 1
	}
	for _, d := range duraciones {
		assert.Equal(t, d.want, ScaleDuration(d.base, d.perMille), "ScaleDuration(%d, %d)", d.base, d.perMille)
	}
	cantidades := []struct {
		base, perMille, want int32
	}{
		{150, 1200, 180}, {150, 1000, 150}, {20, 1150, 23}, {20, 1000, 20},
		{3, 1166, 3}, {3, 1167, 4}, // 3,498 baja y 3,501 sube: al más cercano
		{1, 1, 1}, // nunca baja de 1
	}
	for _, c := range cantidades {
		assert.Equal(t, c.want, ScaleAmount(c.base, c.perMille), "ScaleAmount(%d, %d)", c.base, c.perMille)
	}
}

// INV-PLAYER-012: el catálogo sólo admite claves conocidas con enteros
// positivos. Un decimal ni siquiera se puede leer: lo prueba la carga.
func TestUnCatalogoConUnRasgoInvalidoSeRechaza(t *testing.T) {
	assert.NoError(t, Civilization{Code: "NORSE", Traits: map[string]int32{TraitMoveSpeed: 1100, TraitLootCarry: 1150}}.Validate())
	assert.NoError(t, Civilization{Code: "VACIA"}.Validate())
	for nombre, traits := range map[string]map[string]int32{
		"cero":        {TraitMoveSpeed: 0},
		"negativo":    {TraitHouseHP: -1200},
		"desconocida": {"economy.trade_yield": 1150},
	} {
		err := Civilization{Code: "X", Traits: traits}.Validate()
		assert.True(t, errors.Is(err, ErrInvalidTrait), "%s: se esperaba ErrInvalidTrait, llegó %v", nombre, err)
	}
}

// catalogoCompleto son las cuatro civilizaciones de 000016 y 000024, con su
// bien y su unidad.
func catalogoCompleto() []Civilization {
	return []Civilization{
		{Code: "ROMAN", Good: economy.ResourceWine, Unit: unit.TypeLegionary},
		{Code: "BYZANTINE", Good: economy.ResourceSilk, Unit: unit.TypeCataphract},
		{Code: "PERSIAN", Good: economy.ResourceSpices, Unit: unit.TypeHorseArcher},
		{Code: "NORSE", Good: economy.ResourceFurs, Unit: unit.TypeBerserker},
	}
}

// RN-GOODS-001: cada civilización tiene un bien de civilización, y ninguno se
// repite.
func TestElCatalogoTieneUnBienDistintoPorCivilizacion(t *testing.T) {
	assert.NoError(t, ValidateCatalog(catalogoCompleto()))
	for nombre, romper := range map[string]func([]Civilization){
		"un recurso que no es bien": func(cs []Civilization) { cs[0].Good = economy.ResourceWood },
		"sin bien":                  func(cs []Civilization) { cs[0].Good = "" },
		"bien repetido":             func(cs []Civilization) { cs[2].Good = economy.ResourceWine },
	} {
		cs := catalogoCompleto()
		romper(cs)
		assert.ErrorIs(t, ValidateCatalog(cs), ErrInvalidGood, nombre)
	}
	cs := catalogoCompleto()
	cs[3].Traits = map[string]int32{TraitMoveSpeed: 0}
	assert.ErrorIs(t, ValidateCatalog(cs), ErrInvalidTrait)
	// Un bien y una unidad mal a la vez: primero el bien.
	cs = catalogoCompleto()
	cs[0].Good, cs[0].Unit = "", ""
	assert.ErrorIs(t, ValidateCatalog(cs), ErrInvalidGood)
}

// INV-UNIT-013: cada civilización tiene una unidad exclusiva y entrenable del
// catálogo, ninguna se repite y ningún tipo exclusivo queda sin civilización.
func TestElCatalogoTieneUnaUnidadExclusivaPorCivilizacion(t *testing.T) {
	for nombre, romper := range map[string]func([]Civilization) []Civilization{
		"sin unidad":             func(cs []Civilization) []Civilization { cs[0].Unit = ""; return cs },
		"un tipo desconocido":    func(cs []Civilization) []Civilization { cs[0].Unit = "KNIGHT"; return cs },
		"una unidad común":       func(cs []Civilization) []Civilization { cs[0].Unit = unit.TypeWarrior; return cs },
		"la caravana":            func(cs []Civilization) []Civilization { cs[0].Unit = unit.TypeCaravan; return cs },
		"una unidad repetida":    func(cs []Civilization) []Civilization { cs[1].Unit = unit.TypeLegionary; return cs },
		"una exclusiva huérfana": func(cs []Civilization) []Civilization { return cs[:3] },
	} {
		err := ValidateCatalog(romper(catalogoCompleto()))
		assert.ErrorIs(t, err, ErrInvalidUnit, nombre)
	}
}
