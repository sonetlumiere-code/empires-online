package siege_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/siege"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

func def(t *testing.T, tp unit.Type) unit.Definition {
	t.Helper()
	d, err := unit.Lookup(tp)
	require.NoError(t, err)
	return d
}

// INV-COMBAT-008, RN-SIEGE-004: la tabla de SPEC-SIEGE-ENGINES §4.3 completa.
// Amurallada: el guerrero y las unidades de civilización no golpean nada; el
// ariete, sólo la muralla. En brecha: todos golpean cualquier edificio.
func TestQuienDanaQue(t *testing.T) {
	combate := []unit.Type{unit.TypeWarrior, unit.TypeLegionary, unit.TypeCataphract, unit.TypeHorseArcher, unit.TypeBerserker}
	for _, b := range building.Types() {
		for _, tp := range combate {
			assert.ErrorIs(t, siege.Check(def(t, tp), b, true), siege.ErrWallStanding, "%s contra %s amurallada", tp, b)
			assert.NoError(t, siege.Check(def(t, tp), b, false), "%s contra %s en brecha", tp, b)
		}
		ariete := siege.Check(def(t, unit.TypeRam), b, true)
		if b == building.TypeWall {
			assert.NoError(t, ariete, "el ariete golpea la muralla")
		} else {
			assert.ErrorIs(t, ariete, siege.ErrWallFirst, "el ariete contra %s amurallada", b)
		}
		assert.NoError(t, siege.Check(def(t, unit.TypeRam), b, false), "el ariete contra %s en brecha", b)
		for _, tp := range []unit.Type{unit.TypeVillager, unit.TypeCaravan} {
			assert.ErrorIs(t, siege.Check(def(t, tp), b, false), siege.ErrCannotDemolish, "%s no derriba", tp)
			assert.False(t, siege.CanDamage(def(t, tp), b, true))
		}
	}
}
