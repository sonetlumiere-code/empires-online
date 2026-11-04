package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato del asedio (SPEC-COMBAT-SIEGE §6, §9 «contract»).

// unit.demolish lleva exactamente guerrero y casa, los dos obligatorios: el
// cliente no aporta tile, vida ni daño (INV-SEC-001).
func TestElComandoDeAsedioEsElDelEsquema(t *testing.T) {
	root, variant := clientVariant(t, protocol.TypeUnitDemolish)
	payload := dig(t, root, variant, "properties", "payload").(map[string]any)
	declaradas := clavesDe(dig(t, root, payload, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.UnitDemolishPayload{UnitID: 1, BuildingID: 2})))
	require.Equal(t, declaradas, requeridas(t, payload), "ningún campo es opcional")
	require.True(t, protocol.ClientMessageTypes[protocol.TypeUnitDemolish])
}

// building.destroyed lleva la casa y su ciudad, las dos obligatorias.
func TestLaCasaDerribadaEsLaDelEsquema(t *testing.T) {
	root, variant := serverVariant(t, protocol.TypeBuildingDestroyed)
	payload := dig(t, root, variant, "properties", "payload").(map[string]any)
	declaradas := clavesDe(dig(t, root, payload, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.BuildingDestroyedPayload{ID: 1, CityID: 7})))
	require.Equal(t, declaradas, requeridas(t, payload))
}

// La vida viaja en la vista del edificio y en building.update, opcional en el
// esquema; el aviso besiegedAtMs, en city.update.
func TestLaVidaDeLosEdificiosYElAvisoEstanEnElEsquema(t *testing.T) {
	root, spawn := serverVariant(t, protocol.TypeBuildingSpawn)
	vista := dig(t, root, spawn, "properties", "payload", "properties", "building").(map[string]any)
	declaradas := clavesDe(dig(t, root, vista, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.BuildingView{HP: 150, MaxHP: 150})))
	assert.NotContains(t, requeridas(t, vista), "hp", "opcional: un cliente sin asedio no la necesita")

	root, update := serverVariant(t, protocol.TypeBuildingUpdate)
	props := dig(t, root, update, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, props, "hp")
	hp := int32(142)
	assert.Contains(t, serializado(t, protocol.BuildingUpdatePayload{ID: 1, Status: "COMPLETE", HP: &hp}), "hp")
	assert.NotContains(t, serializado(t, protocol.BuildingUpdatePayload{ID: 1, Status: "COMPLETE"}), "hp",
		"la compleción no la manda")

	root, city := serverVariant(t, protocol.TypeCityUpdate)
	props = dig(t, root, city, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, props, "besiegedAtMs")
	at := int64(1_757_376_000_500)
	assert.Contains(t, serializado(t, protocol.CityUpdatePayload{ID: 7, BesiegedAtMs: &at}), "besiegedAtMs")
}
