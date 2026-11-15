package player

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Las reglas del bando (SPEC-FACTIONS §4.1, ADR-039).

func TestSoloSeEntraEnUnBandoDesdeNeutral(t *testing.T) {
	// RN-FAC-001 e INV-PLAYER-013.
	assert.True(t, CanJoin(FactionNeutral, FactionOrder))
	assert.True(t, CanJoin(FactionNeutral, FactionChaos))
	assert.True(t, CanJoin("", FactionOrder), "vacío es NEUTRAL")
	assert.False(t, CanJoin(FactionNeutral, FactionNeutral))
	assert.False(t, CanJoin(FactionOrder, FactionChaos))
	assert.False(t, CanJoin(FactionChaos, FactionOrder))
	assert.False(t, CanJoin(FactionOrder, FactionOrder))
	assert.False(t, CanJoin(FactionNeutral, "GUILD"))
}

func TestLaPazDeLaOrdenYLosBandosContrarios(t *testing.T) {
	bandos := []string{FactionOrder, FactionChaos, FactionNeutral}
	for _, a := range bandos {
		for _, b := range bandos {
			assert.Equal(t, a == FactionOrder && b == FactionOrder, OrderPeace(a, b), "%s-%s", a, b)
			contrarios := (a == FactionOrder && b == FactionChaos) || (a == FactionChaos && b == FactionOrder)
			assert.Equal(t, contrarios, Opposed(a, b), "%s-%s", a, b)
		}
	}
	assert.True(t, KnownFaction(FactionNeutral))
	assert.False(t, KnownFaction(""))
	assert.Equal(t, FactionNeutral, EffectiveFaction(""))
}

func TestLaTierraAfin(t *testing.T) {
	// RN-FAC-007: la propia siempre; la del bando, si no es NEUTRAL.
	assert.True(t, Affine(true, FactionNeutral, FactionNeutral))
	assert.True(t, Affine(true, FactionOrder, FactionOrder))
	assert.True(t, Affine(false, FactionOrder, FactionOrder))
	assert.True(t, Affine(false, FactionChaos, FactionChaos))
	assert.False(t, Affine(false, FactionNeutral, FactionNeutral))
	assert.False(t, Affine(false, FactionOrder, FactionChaos))
	assert.False(t, Affine(false, "", ""))
}

func TestElDanoEnTierraAfinEsEnEnteros(t *testing.T) {
	// RN-FAC-008 e INV-MOVE-009: (daño × r + 500) / 1000, mínimo 1.
	assert.Equal(t, int32(6), AffineDamage(8, DefaultAffineDamagePerMille), "6,4 → 6")
	assert.Equal(t, int32(7), AffineDamage(9, DefaultAffineDamagePerMille), "7,2 → 7")
	assert.Equal(t, int32(2), AffineDamage(3, DefaultAffineDamagePerMille), "2,4 → 2")
	assert.Equal(t, int32(1), AffineDamage(1, DefaultAffineDamagePerMille), "nunca menos de 1")
	assert.Equal(t, int32(1), AffineDamage(8, 1))
	assert.Equal(t, int32(8), AffineDamage(8, 1000), "1000 es sin efecto")
}
