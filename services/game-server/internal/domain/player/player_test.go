package player

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestTraitDevuelveElValorODelPorDefecto(t *testing.T) {
	c := Civilization{Traits: map[string]float64{"gather_wood": 1.1}}
	assert.Equal(t, 1.1, c.Trait("gather_wood", 1))
	assert.Equal(t, 1.0, c.Trait("gather_stone", 1), "un rasgo ausente da el valor por defecto")
	assert.Equal(t, 2.0, Civilization{}.Trait("x", 2), "sin rasgos, también")
}
