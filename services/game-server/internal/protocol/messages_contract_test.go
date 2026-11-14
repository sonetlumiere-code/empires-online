package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/message"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de los mensajes entre jugadores (SPEC-MESSAGES §6, §9 «contract»): los
// cinco comandos y los tres mensajes del servidor contra el JSON Schema exportado
// por packages/protocol.

// Cada comando lleva exactamente los campos del esquema, y ninguno nombra al
// remitente ni a quien lee: es el jugador de la sesión (RN-MSG-001, INV-SEC-010).
func TestLosComandosDeMensajesSonLosDelEsquema(t *testing.T) {
	antes := int64(3)
	casos := []struct {
		tipo         string
		payload      any
		obligatorios []string
	}{
		{protocol.TypeMessageSend, protocol.MessageSendPayload{ToPlayerID: "x", Body: "hola"}, []string{"body", "toPlayerId"}},
		{protocol.TypeMessageList, protocol.MessageListPayload{}, nil},
		{protocol.TypeMessageFetch, protocol.MessageFetchPayload{OtherPlayerID: "x", BeforeID: &antes}, []string{"otherPlayerId"}},
		{protocol.TypeMessageRead, protocol.MessageReadPayload{OtherPlayerID: "x", UpToID: 1}, []string{"otherPlayerId", "upToId"}},
		{protocol.TypeMessageBlock, protocol.MessageBlockPayload{PlayerID: "x", Blocked: true}, []string{"blocked", "playerId"}},
	}
	for _, c := range casos {
		t.Run(c.tipo, func(t *testing.T) {
			require.True(t, protocol.ClientMessageTypes[c.tipo])
			root, variant := clientVariant(t, c.tipo)
			p := dig(t, root, variant, "properties", "payload").(map[string]any)
			declaradas := clavesDe(dig(t, root, p, "properties").(map[string]any))
			assert.Equal(t, declaradas, clavesDe(serializado(t, c.payload)))
			assert.NotContains(t, declaradas, "fromPlayerId")
			if c.obligatorios == nil {
				_, hay := p["required"]
				assert.False(t, hay)
			} else {
				assert.Equal(t, c.obligatorios, requeridas(t, p))
			}
		})
	}
	assert.Empty(t, serializado(t, protocol.MessageFetchPayload{OtherPlayerID: "x"})["beforeId"], "sin beforeId no se serializa")
}

// El texto: el esquema lo acota en unidades UTF-16, el doble del máximo en
// caracteres del dominio, para que quepa un texto de caracteres de dos unidades.
func TestElTextoDelEsquemaCubreElMaximoDelDominio(t *testing.T) {
	root, send := clientVariant(t, protocol.TypeMessageSend)
	body := dig(t, root, send, "properties", "payload", "properties", "body").(map[string]any)
	assert.EqualValues(t, 1, body["minLength"])
	assert.EqualValues(t, 2*message.MaxChars, body["maxLength"])
}

// Lo que serializa el servidor tiene exactamente los campos del esquema.
func TestLosMensajesDelServidorSonLosDelEsquema(t *testing.T) {
	id := int64(7)
	nombre := "Villa Norte"
	vista := protocol.MessageView{
		ID: 1, FromPlayerID: "a", ToPlayerID: "b", OtherPlayerID: "b", OtherCityID: &id, OtherCityName: &nombre,
		Body: "hola", SentAtMs: 1,
	}

	root, nuevo := serverVariant(t, protocol.TypeMessageNew)
	p := dig(t, root, nuevo, "properties", "payload").(map[string]any)
	m := dig(t, root, p, "properties", "message").(map[string]any)
	require.Equal(t, clavesDe(dig(t, root, m, "properties").(map[string]any)), clavesDe(serializado(t, vista)))
	require.Equal(t, requeridas(t, m), clavesDe(serializado(t, protocol.MessageView{})), "todos obligatorios, también los nulos")

	root, inbox := serverVariant(t, protocol.TypeMessageInbox)
	p = dig(t, root, inbox, "properties", "payload").(map[string]any)
	require.Equal(t, clavesDe(dig(t, root, p, "properties").(map[string]any)),
		clavesDe(serializado(t, protocol.MessageInboxPayload{Conversations: []protocol.ConversationView{}, Blocked: []protocol.BlockedPlayerView{}})))
	conv := dig(t, root, p, "properties", "conversations", "items").(map[string]any)
	require.Equal(t, clavesDe(dig(t, root, conv, "properties").(map[string]any)), clavesDe(serializado(t, protocol.ConversationView{})))
	require.Equal(t, requeridas(t, conv), clavesDe(serializado(t, protocol.ConversationView{})))
	bloq := dig(t, root, p, "properties", "blocked", "items").(map[string]any)
	require.Equal(t, clavesDe(dig(t, root, bloq, "properties").(map[string]any)), clavesDe(serializado(t, protocol.BlockedPlayerView{})))

	root, page := serverVariant(t, protocol.TypeMessagePage)
	p = dig(t, root, page, "properties", "payload").(map[string]any)
	pagina := protocol.MessagePagePayload{OtherPlayerID: "b", Messages: []protocol.MessageView{vista}}
	require.Equal(t, clavesDe(dig(t, root, p, "properties").(map[string]any)), clavesDe(serializado(t, pagina)))
	require.Equal(t, requeridas(t, p), clavesDe(serializado(t, pagina)))
}
