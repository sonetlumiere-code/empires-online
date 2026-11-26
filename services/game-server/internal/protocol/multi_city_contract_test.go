package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato del multiciudad (SPEC-MULTI-CITY §6, §9 «contract»).

// ownCities viaja en el snapshot, opcional (aditivo en v1), y cada ciudad lleva
// exactamente el id, el centro y la era, todos obligatorios (Q9, precisión del
// 2026-11-26). Sin ciudades, la clave no viaja.
func TestLasCiudadesPropiasDelSnapshotSonLasDelEsquema(t *testing.T) {
	root, snap := serverVariant(t, protocol.TypeWorldSnapshot)
	payload := dig(t, root, snap, "properties", "payload").(map[string]any)
	props := dig(t, root, payload, "properties").(map[string]any)
	require.Contains(t, props, "ownCities")
	assert.NotContains(t, requeridas(t, payload), "ownCities", "opcional: aditivo en v1")

	item := dig(t, root, props["ownCities"].(map[string]any), "items").(map[string]any)
	declaradas := clavesDe(dig(t, root, item, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.OwnCityView{ID: 1, CenterX: 2, CenterY: 3, Era: "BRONZE_AGE"})))
	require.Equal(t, declaradas, requeridas(t, item), "ningún campo es opcional")

	assert.NotContains(t, serializado(t, protocol.WorldSnapshotPayload{}), "ownCities")
}
