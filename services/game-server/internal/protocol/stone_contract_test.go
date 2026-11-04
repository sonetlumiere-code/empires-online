package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de la piedra y del avance de era (SPEC-ECON-STONE §6, §9 «contract»).

// city.advance lleva sólo la ciudad: la era, el coste y el tiempo son del
// servidor (INV-SEC-001).
func TestElComandoDeAvanceEsElDelEsquema(t *testing.T) {
	root, variant := clientVariant(t, protocol.TypeCityAdvance)
	payload := dig(t, root, variant, "properties", "payload").(map[string]any)
	declaradas := clavesDe(dig(t, root, payload, "properties").(map[string]any))
	require.Equal(t, []string{"cityId"}, declaradas)
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.CityAdvancePayload{CityID: 7})))
	require.Equal(t, declaradas, requeridas(t, payload))
	require.True(t, protocol.ClientMessageTypes[protocol.TypeCityAdvance])
}

// hill.update serializa exactamente la vista del esquema, como forest.update;
// el snapshot la trae en hills, opcional.
func TestLosMensajesDeLaColinaSonLosDelEsquema(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeHillUpdate)
	payload := dig(t, root, update, "properties", "payload").(map[string]any)
	declaradas := clavesDe(dig(t, root, payload, "properties").(map[string]any))
	en := int64(5)
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.HillView{X: 1, Y: 2, Remaining: 40, RegrowsAtMs: &en})))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.HillView{X: 1, Y: 2, Remaining: 100})),
		"un tile lleno lleva regrowsAtMs a null, no lo omite")
	require.Equal(t, declaradas, requeridas(t, payload))

	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	props := dig(t, root, snapshot, "properties", "payload").(map[string]any)
	item := dig(t, root, props, "properties", "hills", "items").(map[string]any)
	require.Equal(t, declaradas, clavesDe(dig(t, root, item, "properties").(map[string]any)))
	require.NotContains(t, requeridas(t, props), "hills", "hills es aditivo: opcional en v1")
	require.NotContains(t, clavesDe(serializado(t, protocol.WorldSnapshotPayload{})), "hills")
}

// El stock lleva STONE, opcional en el esquema para un cliente nuevo contra un
// servidor anterior; este servidor siempre la manda.
func TestElStockLlevaLaPiedra(t *testing.T) {
	root, city := serverVariant(t, protocol.TypeCityUpdate)
	stocks := dig(t, root, city, "properties", "payload", "properties", "stocks").(map[string]any)
	declaradas := clavesDe(dig(t, root, stocks, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.Stocks{Wood: 1, Stone: 2})))
	assert.NotContains(t, requeridas(t, stocks), "STONE")
	assert.Contains(t, requeridas(t, stocks), "WOOD")
}

// La era y el avance viajan en city.update y en la vista de la ciudad; la
// carga de una unidad, con su recurso.
func TestLaEraElAvanceYElRecursoDeLaCargaEstanEnElEsquema(t *testing.T) {
	root, city := serverVariant(t, protocol.TypeCityUpdate)
	props := dig(t, root, city, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, props, "era")
	assert.Contains(t, props, "advance")
	era := "BRONZE_AGE"
	vista := &protocol.AdvanceView{Era: "BRONZE_AGE", CompletesAtMs: 1_757_376_090_000}
	var ninguno *protocol.AdvanceView
	conAvance := serializado(t, protocol.CityUpdatePayload{ID: 7, Advance: &vista})
	assert.Contains(t, conAvance, "advance")
	assert.Equal(t, []string{"completesAtMs", "era"}, clavesDe(conAvance["advance"].(map[string]any)))
	fin := serializado(t, protocol.CityUpdatePayload{ID: 7, Era: &era, Advance: &ninguno})
	assert.Nil(t, fin["advance"], "advance: null al terminar")
	assert.Equal(t, "BRONZE_AGE", fin["era"])
	assert.NotContains(t, serializado(t, protocol.CityUpdatePayload{ID: 7}), "advance", "sin cambio no se manda")

	root, spawn := serverVariant(t, protocol.TypeEntitySpawn)
	unidad := dig(t, root, spawn, "properties", "payload", "properties", "unit", "properties").(map[string]any)
	assert.Contains(t, unidad, "carryingResource")
	root, update := serverVariant(t, protocol.TypeEntityUpdate)
	assert.Contains(t, dig(t, root, update, "properties", "payload", "properties").(map[string]any), "carryingResource")
	carga := int32(10)
	assert.Equal(t, "STONE", serializado(t, protocol.EntityUpdatePayload{ID: 1, Carrying: &carga, CarryingResource: "STONE"})["carryingResource"])
	assert.NotContains(t, serializado(t, protocol.EntityUpdatePayload{ID: 1}), "carryingResource")
}
