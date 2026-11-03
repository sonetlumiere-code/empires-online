package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato del combate (SPEC-COMBAT-RAID §6, §9 «contract»): el comando
// nuevo, el estado ATTACKING y el tipo WARRIOR contra el JSON Schema exportado
// por packages/protocol.

// unit.attack lleva exactamente atacante y objetivo, los dos obligatorios: el
// cliente no aporta daño, vida, ruta ni botín (INV-SEC-001).
func TestElComandoDeAtaqueEsElDelEsquema(t *testing.T) {
	root, variant := clientVariant(t, protocol.TypeUnitAttack)
	payload := dig(t, root, variant, "properties", "payload").(map[string]any)
	declaradas := clavesDe(dig(t, root, payload, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.UnitAttackPayload{UnitID: 1, TargetUnitID: 2})))
	require.Equal(t, declaradas, requeridas(t, payload), "ningún campo es opcional")
	require.True(t, protocol.ClientMessageTypes[protocol.TypeUnitAttack])
}

// ATTACKING y WARRIOR existen en el dominio y en el esquema, en el snapshot y
// en entity.update.
func TestElEstadoAttackingYElGuerreroEstanEnElEsquema(t *testing.T) {
	assert.True(t, unit.StatusAttacking.Valid())
	def, err := unit.Lookup(unit.TypeWarrior)
	require.NoError(t, err)
	assert.True(t, def.CanAttack())
	assert.True(t, def.CanTrain())
	assert.False(t, def.CanGather(), "el guerrero no recolecta")
	aldeano, err := unit.Lookup(unit.TypeVillager)
	require.NoError(t, err)
	assert.False(t, aldeano.CanAttack(), "el aldeano no ataca")

	root, update := serverVariant(t, protocol.TypeEntityUpdate)
	props := dig(t, root, update, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, enumOf(t, dig(t, root, props, "status")), string(unit.StatusAttacking))
	assert.Contains(t, props, "hp")

	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	item := dig(t, root, snapshot, "properties", "payload", "properties", "units", "items").(map[string]any)
	assert.Contains(t, enumOf(t, dig(t, root, item, "properties", "status")), string(unit.StatusAttacking))
	assert.Contains(t, enumOf(t, dig(t, root, item, "properties", "unitType")), string(unit.TypeWarrior))

	root, despawn := serverVariant(t, protocol.TypeEntityDespawn)
	assert.Contains(t, enumOf(t, dig(t, root, despawn, "properties", "payload", "properties", "reason")), "DEAD")
}

// Contrato del saqueo (SPEC-COMBAT-PILLAGE §6, §9 «contract»).

// unit.pillage lleva exactamente guerrero y ciudad, los dos obligatorios: el
// cliente no aporta tile, cantidad ni suelo (INV-SEC-001).
func TestElComandoDeSaqueoEsElDelEsquema(t *testing.T) {
	root, variant := clientVariant(t, protocol.TypeUnitPillage)
	payload := dig(t, root, variant, "properties", "payload").(map[string]any)
	declaradas := clavesDe(dig(t, root, payload, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.UnitPillagePayload{UnitID: 1, CityID: 2})))
	require.Equal(t, declaradas, requeridas(t, payload), "ningún campo es opcional")
	require.True(t, protocol.ClientMessageTypes[protocol.TypeUnitPillage])
}

// city.update declara pillagedAtMs, y el servidor lo serializa con ese nombre.
func TestElAvisoDeSaqueoEstaEnElEsquemaDeCityUpdate(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeCityUpdate)
	props := dig(t, root, update, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, props, "pillagedAtMs")
	at := int64(1_757_376_000_500)
	assert.Contains(t, serializado(t, protocol.CityUpdatePayload{ID: 7, PillagedAtMs: &at}), "pillagedAtMs")
}
