package unit_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

// INV-UNIT-002 e INV-UNIT-003: el estado decide, en un único punto, si una
// unidad acepta una orden de movimiento. Tabla exhaustiva sobre los cinco
// estados del esquema y uno que no existe.
func TestEnsureCanMoveDecidePorEstado(t *testing.T) {
	cases := []struct {
		status unit.Status
		want   error
	}{
		{unit.StatusIdle, nil},
		{unit.StatusMoving, nil}, // la orden nueva reemplaza a la anterior
		{unit.StatusHidden, nil}, // moverse revela, no está prohibido
		{unit.StatusDead, unit.ErrDead},
		{unit.StatusGarrisoned, unit.ErrGarrisoned},
		{unit.Status("WALKING"), unit.ErrNotMovable},
	}
	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			u := &unit.Unit{Status: c.status, HP: 40, MaxHP: 40}
			err := u.EnsureCanMove()
			if c.want == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, c.want)
		})
	}
}

// INV-UNIT-004: una unidad sin vida no cuenta como viva aunque su estado se
// haya quedado atrás, y una DEAD no lo está aunque conserve vida.
func TestIsAliveExigeEstadoYVida(t *testing.T) {
	assert.True(t, (&unit.Unit{Status: unit.StatusIdle, HP: 1}).IsAlive())
	assert.False(t, (&unit.Unit{Status: unit.StatusIdle, HP: 0}).IsAlive())
	assert.False(t, (&unit.Unit{Status: unit.StatusDead, HP: 40}).IsAlive())
}

// INV-UNIT-006: la propiedad se compara por valor; ni el uuid nulo ni otro
// jugador pasan por el dueño.
func TestEnsureOwnedByComparaElDueno(t *testing.T) {
	dueno := uuid.New()
	u := &unit.Unit{PlayerID: dueno}
	assert.NoError(t, u.EnsureOwnedBy(dueno))
	assert.ErrorIs(t, u.EnsureOwnedBy(uuid.New()), unit.ErrNotOwned)
	assert.ErrorIs(t, u.EnsureOwnedBy(uuid.Nil), unit.ErrNotOwned)
}

// INV-UNIT-009: el catálogo sólo conoce sus tipos. Un tipo inventado no
// resuelve a ninguna definición por defecto.
func TestLookupSoloConoceElCatalogo(t *testing.T) {
	def, err := unit.Lookup(unit.TypeVillager)
	require.NoError(t, err)
	assert.Equal(t, unit.Definition{Type: unit.TypeVillager, MaxHP: 40, BaseMsPerTile: 600, PopulationCost: 1}, def)

	for _, raro := range []unit.Type{"villager", "", "TOWN_CENTER", "KNIGHT"} {
		_, err := unit.Lookup(raro)
		assert.ErrorIs(t, err, unit.ErrUnknownType, "%q", raro)
	}
}

func TestLosEstadosDelDominioSonLosDelEsquema(t *testing.T) {
	// El CHECK units_status_valid admite exactamente estos cinco.
	for _, s := range []unit.Status{unit.StatusIdle, unit.StatusMoving, unit.StatusGarrisoned, unit.StatusHidden, unit.StatusDead} {
		assert.True(t, s.Valid(), "%s", s)
	}
	for _, s := range []unit.Status{"idle", "", "WALKING"} {
		assert.False(t, s.Valid(), "%q", s)
	}
}
