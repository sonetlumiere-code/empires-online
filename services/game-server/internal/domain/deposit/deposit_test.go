package deposit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/deposit"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// RN-ECON-013, RN-ECON-021 e INV-ECON-008: la carga es min(carry, remaining) y
// el tile pierde exactamente eso; con el tile vacío no hay carga. Igual en las
// dos capas.
func TestUnaCargaNuncaCreaMadera(t *testing.T) {
	for _, l := range deposit.Layers() {
		for _, c := range []struct {
			remaining, carry, taken int32
		}{{100, 10, 10}, {7, 10, 7}, {0, 10, 0}, {10, 10, 10}} {
			taken, after := l.Take(c.remaining, c.carry, 1_000)
			assert.Equal(t, c.taken, taken, "%s: quedan %d, carga %d", l.Name, c.remaining, c.carry)
			assert.Equal(t, c.remaining, taken+after.Remaining, "%s: lo que se lleva es lo que pierde el tile", l.Name)
			assert.Equal(t, int64(1_000)+l.RegrowAfterMs, after.RegrowsAtMs, "%s: vuelve a crecer desde esta carga", l.Name)
		}
	}
}

// RN-ECON-016: la regeneración se ancla a su instante: un milisegundo antes
// queda lo que quedaba, en el instante exacto está lleno.
func TestLaRegeneracionVenceEnSuInstante(t *testing.T) {
	s := deposit.State{Remaining: 0, RegrowsAtMs: 5_000}
	assert.Equal(t, int32(0), deposit.Forest.RemainingAt(s, 4_999))
	assert.False(t, s.Regrown(4_999))
	assert.Equal(t, deposit.Forest.Capacity, deposit.Forest.RemainingAt(s, 5_000))
	assert.Equal(t, deposit.Hill.Capacity, deposit.Hill.RemainingAt(s, 5_000))
	assert.True(t, s.Regrown(5_000))
}

// INV-ECON-007: una entrada fuera de rango se rechaza, en cada capa con su
// capacidad.
func TestUnaEntradaFueraDeRangoEsInvalida(t *testing.T) {
	for _, l := range deposit.Layers() {
		require.NoError(t, l.Validate(deposit.State{Remaining: 0}), l.Name)
		require.NoError(t, l.Validate(deposit.State{Remaining: l.Capacity}), l.Name)
		require.ErrorIs(t, l.Validate(deposit.State{Remaining: -1}), deposit.ErrInvalid, l.Name)
		require.ErrorIs(t, l.Validate(deposit.State{Remaining: l.Capacity + 1}), deposit.ErrInvalid, l.Name)
	}
}

// ADR-030 §1: el terreno decide la capa, y cada capa da su recurso. Sólo el
// bosque y la colina dan algo.
func TestElTerrenoDecideLaCapa(t *testing.T) {
	l, ok := deposit.ForTerrain(world.Forest)
	require.True(t, ok)
	assert.Equal(t, economy.ResourceWood, l.Resource)
	l, ok = deposit.ForTerrain(world.Hill)
	require.True(t, ok)
	assert.Equal(t, economy.ResourceStone, l.Resource)
	for _, tt := range []world.TerrainType{world.Grassland, world.Mountain, world.Water, world.Road} {
		_, ok := deposit.ForTerrain(tt)
		assert.False(t, ok, "%s no da recurso", tt)
	}
	l, ok = deposit.ForResource(economy.ResourceStone)
	require.True(t, ok)
	assert.Equal(t, world.Hill, l.Terrain)
}

// SPEC-ECON-STONE Q7: la colina tiene los números del bosque.
func TestLaColinaTieneLosNumerosDelBosque(t *testing.T) {
	assert.Equal(t, int32(100), deposit.Hill.Capacity)
	assert.Equal(t, int64(24*60*60*1000), deposit.Hill.RegrowAfterMs)
	assert.Equal(t, deposit.Forest.SearchRadius, deposit.Hill.SearchRadius)
}
