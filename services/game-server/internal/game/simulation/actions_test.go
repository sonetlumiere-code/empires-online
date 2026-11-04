package simulation_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// INV-CITY-006 y ADR-016: todo mensaje cliente→servidor es de sesión o está
// clasificado como acción de mundo, y no las dos cosas. Un comando nuevo sin
// clasificar hace fallar este test.
func TestTodaAccionDelCatalogoEstaClasificada(t *testing.T) {
	require.NotEmpty(t, protocol.ClientMessageTypes)
	for msgType := range protocol.ClientMessageTypes {
		_, accion := simulation.ActionPolicyOf(msgType)
		sesion := simulation.IsSessionMessage(msgType)
		assert.True(t, accion != sesion,
			"%s debe ser de sesión o una acción clasificada, y sólo una de las dos", msgType)
	}
	_, ok := simulation.ActionPolicyOf("unit.teleport")
	assert.False(t, ok, "lo que no está en el catálogo no tiene clasificación")
}

// ADR-016 §2.1, SPEC-COMBAT-RAID RN-COMBAT-002, SPEC-COMBAT-PILLAGE
// RN-COMBAT-020 y SPEC-COMBAT-SIEGE RN-COMBAT-030: ninguna acción se restringe
// por territorio, y las únicas hostiles son unit.attack, unit.pillage y
// unit.demolish. Cuando cambie, este test cambia con su decisión.
func TestSoloAtaqueSaqueoYAsedioSonHostilesYNingunaSeRestringePorTerritorio(t *testing.T) {
	for msgType := range protocol.ClientMessageTypes {
		p, ok := simulation.ActionPolicyOf(msgType)
		if !ok {
			continue
		}
		hostil := msgType == protocol.TypeUnitAttack || msgType == protocol.TypeUnitPillage ||
			msgType == protocol.TypeUnitDemolish
		assert.Equal(t, hostil, p.Hostile, "%s", msgType)
		assert.Equal(t, simulation.TerritoryFree, p.Territory, "%s", msgType)
	}
}
