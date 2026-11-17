package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de SPEC-CIVILIZATION-UNITS §6 y ADR-043 §7.

// UnitType del esquema es exactamente el catálogo de Go, en el mismo orden, y
// city.train admite exactamente los tipos que se entrenan.
func TestLosTiposDeUnidadDelEsquemaSonLosDelCatalogo(t *testing.T) {
	root, spawn := serverVariant(t, protocol.TypeEntitySpawn)
	tipos := enumOf(t, dig(t, root, spawn, "properties", "payload", "properties", "unit", "properties", "unitType"))
	esperados := make([]string, 0, len(unit.Types()))
	entrenables := []string{}
	for _, tp := range unit.Types() {
		esperados = append(esperados, string(tp))
		def, err := unit.Lookup(tp)
		require.NoError(t, err)
		if def.CanTrain() {
			entrenables = append(entrenables, string(tp))
		}
	}
	assert.Equal(t, esperados, tipos)

	root, train := clientVariant(t, protocol.TypeCityTrain)
	assert.Equal(t, entrenables, enumOf(t, dig(t, root, train, "properties", "payload", "properties", "type")),
		"TrainableUnitType son los tipos con coste y plazo")
}

// El catálogo de presentación (units.json) es el de Go campo a campo: un cambio
// de balance que no lo actualice rompe aquí en lugar de mentir en el cliente.
func TestElCatalogoDeUnidadesEsElDePresentacion(t *testing.T) {
	exportado, err := protocol.ExportedUnits()
	require.NoError(t, err)
	require.Len(t, exportado, len(unit.Types()))
	for i, tp := range unit.Types() {
		def, err := unit.Lookup(tp)
		require.NoError(t, err)
		e := exportado[i]
		assert.Equal(t, string(tp), e.Code, "en el orden de unit.Types()")
		assert.NotEmpty(t, e.Name, "%s", tp)
		assert.Equal(t, def.CanAttack(), e.Attacks, "%s attacks", tp)
		assert.Equal(t, def.Exclusive, e.Exclusive, "%s exclusive", tp)
		costes := []protocol.UnitCost{}
		for _, c := range def.TrainCosts {
			costes = append(costes, protocol.UnitCost{Resource: string(c.Resource), Amount: c.Amount})
		}
		assert.Equal(t, costes, append([]protocol.UnitCost{}, e.TrainCosts...), "%s trainCosts", tp)
		assert.Equal(t, def.OwnGoodCost, e.OwnGoodCost, "%s ownGoodCost", tp)
		assert.Equal(t, def.TrainMs, e.TrainMs, "%s trainMs", tp)
		if def.RequiresEra == "" {
			assert.Nil(t, e.RequiresEra, "%s requiresEra", tp)
		} else if assert.NotNil(t, e.RequiresEra, "%s requiresEra", tp) {
			assert.Equal(t, string(def.RequiresEra), *e.RequiresEra, "%s requiresEra", tp)
		}
		assert.Equal(t, def.MaxHP, e.MaxHP, "%s maxHp", tp)
		assert.Equal(t, def.BaseMsPerTile, e.MsPerTile, "%s msPerTile", tp)
		assert.Equal(t, def.AttackDamage, e.AttackDamage, "%s attackDamage", tp)
		assert.Equal(t, def.AttackIntervalMs, e.AttackIntervalMs, "%s attackIntervalMs", tp)
		assert.Equal(t, def.AttackRange, e.AttackRange, "%s attackRange", tp)
		assert.Equal(t, def.LootCarry, e.LootCarry, "%s lootCarry", tp)
		assert.Equal(t, def.PopulationCost, e.PopulationCost, "%s populationCost", tp)
	}
}

// Cada civilización del catálogo de presentación nombra una unidad exclusiva
// distinta (INV-UNIT-013, en el lado del cliente).
func TestCadaCivilizacionDePresentacionTieneSuUnidad(t *testing.T) {
	civs, err := protocol.ExportedCivilizations()
	require.NoError(t, err)
	vistas := map[string]string{}
	for _, c := range civs {
		def, err := unit.Lookup(unit.Type(c.Unit))
		require.NoError(t, err, "%s", c.Code)
		assert.True(t, def.Exclusive, "%s: %s es exclusiva", c.Code, c.Unit)
		otra, dup := vistas[c.Unit]
		assert.False(t, dup, "%s y %s comparten %s", otra, c.Code, c.Unit)
		vistas[c.Unit] = c.Code
	}
}
