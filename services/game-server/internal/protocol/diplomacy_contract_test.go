package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de la diplomacia (SPEC-DIPLOMACY §6, §9 «contract»): los tres
// comandos, treaty.update y treaties[] del snapshot contra el JSON Schema
// exportado por packages/protocol.

// Cada comando lleva exactamente los campos del esquema, todos obligatorios: el
// cliente no aporta la guarnición, el estado ni los plazos (INV-SEC-001).
func TestLosComandosDeDiplomaciaSonLosDelEsquema(t *testing.T) {
	casos := map[string]any{
		protocol.TypeTreatyPropose: protocol.TreatyProposePayload{PlayerID: "x", Type: "ALLIANCE"},
		protocol.TypeTreatyRespond: protocol.TreatyRespondPayload{TreatyID: 1, Accept: true},
		protocol.TypeTreatyCancel:  protocol.TreatyCancelPayload{TreatyID: 1},
	}
	for tipo, payload := range casos {
		t.Run(tipo, func(t *testing.T) {
			root, variant := clientVariant(t, tipo)
			p := dig(t, root, variant, "properties", "payload").(map[string]any)
			declaradas := clavesDe(dig(t, root, p, "properties").(map[string]any))
			require.Equal(t, declaradas, clavesDe(serializado(t, payload)))
			require.Equal(t, declaradas, requeridas(t, p), "ningún campo es opcional")
			require.True(t, protocol.ClientMessageTypes[tipo])
		})
	}
}

// La vista de un tratado que serializa el servidor tiene exactamente los campos
// del esquema, en treaty.update y en el snapshot.
func TestLaVistaDeUnTratadoEsLaDelEsquema(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeTreatyUpdate)
	vista := dig(t, root, update, "properties", "payload", "properties", "treaty").(map[string]any)
	declaradas := clavesDe(dig(t, root, vista, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.TreatyView{ID: 1, OtherPlayerID: "x", Type: "ALLIANCE", Status: "ACTIVE"})))
	assert.Contains(t, enumOf(t, dig(t, root, vista, "properties", "status")), "WITHDRAWN")

	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	props := dig(t, root, snapshot, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, props, "treaties")
}
