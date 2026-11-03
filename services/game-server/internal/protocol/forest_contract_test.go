package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// SPEC-ECON-FOREST §6: forest.update serializa exactamente la vista del
// esquema, con regrowsAtMs presente también cuando es null, y el snapshot trae
// la misma vista en forests, que el esquema declara opcional.
func TestLosMensajesDelBosqueSonLosDelEsquema(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeForestUpdate)
	payload := dig(t, root, update, "properties", "payload").(map[string]any)
	declaradas := clavesDe(dig(t, root, payload, "properties").(map[string]any))
	en := int64(5)
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.ForestView{X: 1, Y: 2, Remaining: 40, RegrowsAtMs: &en})))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.ForestView{X: 1, Y: 2, Remaining: 100})),
		"un tile lleno lleva regrowsAtMs a null, no lo omite")
	require.Equal(t, declaradas, requeridas(t, payload))

	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	props := dig(t, root, snapshot, "properties", "payload").(map[string]any)
	item := dig(t, root, props, "properties", "forests", "items").(map[string]any)
	require.Equal(t, declaradas, clavesDe(dig(t, root, item, "properties").(map[string]any)))
	require.NotContains(t, requeridas(t, props), "forests", "forests es aditivo: opcional en v1")
	require.NotContains(t, clavesDe(serializado(t, protocol.WorldSnapshotPayload{})), "forests",
		"sin tiles a medias, el snapshot no lo envía")
}
