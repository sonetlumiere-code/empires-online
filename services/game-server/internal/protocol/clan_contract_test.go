package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/message"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de los clanes (SPEC-CLANS §6, §9 «contract»; SPEC-CLAN-GOVERNANCE §6):
// los once comandos, los cuatro mensajes del servidor y el clan en la vista de
// ciudad, contra el JSON Schema exportado por packages/protocol.

// Cada comando lleva exactamente los campos del esquema, y ninguno nombra el clan
// propio ni al remitente: son los del jugador de la sesión (INV-SEC-004).
func TestLosComandosDeClanesSonLosDelEsquema(t *testing.T) {
	antes := int64(3)
	casos := []struct {
		tipo         string
		payload      any
		obligatorios []string
	}{
		{protocol.TypeClanCreate, protocol.ClanCreatePayload{Name: "Los Lobos", Tag: "LOB"}, []string{"name", "tag"}},
		{protocol.TypeClanInvite, protocol.ClanInvitePayload{PlayerID: "x", Invited: true}, []string{"invited", "playerId"}},
		{protocol.TypeClanRespond, protocol.ClanRespondPayload{ClanID: 1, Accept: true}, []string{"accept", "clanId"}},
		{protocol.TypeClanLeave, protocol.ClanLeavePayload{}, nil},
		{protocol.TypeClanExpel, protocol.ClanExpelPayload{PlayerID: "x"}, []string{"playerId"}},
		{protocol.TypeClanLookup, protocol.ClanLookupPayload{ClanID: 1}, []string{"clanId"}},
		{protocol.TypeClanChatSend, protocol.ClanChatSendPayload{Body: "hola"}, []string{"body"}},
		{protocol.TypeClanChatFetch, protocol.ClanChatFetchPayload{BeforeID: &antes}, nil},
		{protocol.TypeClanChatRead, protocol.ClanChatReadPayload{UpToID: 1}, []string{"upToId"}},
		{protocol.TypeClanPromote, protocol.ClanPromotePayload{PlayerID: "x", Officer: true}, []string{"officer", "playerId"}},
		{protocol.TypeClanTransfer, protocol.ClanTransferPayload{PlayerID: "x"}, []string{"playerId"}},
	}
	for _, c := range casos {
		t.Run(c.tipo, func(t *testing.T) {
			require.True(t, protocol.ClientMessageTypes[c.tipo])
			root, variant := clientVariant(t, c.tipo)
			p := dig(t, root, variant, "properties", "payload").(map[string]any)
			declaradas := clavesDe(dig(t, root, p, "properties").(map[string]any))
			assert.Equal(t, declaradas, clavesDe(serializado(t, c.payload)))
			assert.NotContains(t, declaradas, "fromPlayerId")
			assert.NotContains(t, declaradas, "leaderId")
			if c.obligatorios == nil {
				_, hay := p["required"]
				assert.False(t, hay)
			} else {
				assert.Equal(t, c.obligatorios, requeridas(t, p))
			}
		})
	}
	assert.Empty(t, serializado(t, protocol.ClanChatFetchPayload{})["beforeId"], "sin beforeId no se serializa")
}

// El nombre y la sigla: el esquema acota lo mismo que el dominio, y el texto del
// canal, lo mismo que el de los mensajes directos.
func TestLosLimitesDelClanCoincidenConElDominio(t *testing.T) {
	root, create := clientVariant(t, protocol.TypeClanCreate)
	props := dig(t, root, create, "properties", "payload", "properties").(map[string]any)
	nombre := dig(t, root, props, "name").(map[string]any)
	assert.EqualValues(t, clan.NameMinChars, nombre["minLength"])
	assert.EqualValues(t, clan.NameMaxChars, nombre["maxLength"])
	sigla := dig(t, root, props, "tag").(map[string]any)
	assert.Equal(t, "^[A-Z0-9]{2,4}$", sigla["pattern"])

	root, send := clientVariant(t, protocol.TypeClanChatSend)
	body := dig(t, root, send, "properties", "payload", "properties", "body").(map[string]any)
	assert.EqualValues(t, 2*message.MaxChars, body["maxLength"])
}

// Lo que serializa el servidor tiene exactamente los campos del esquema, y todos
// son obligatorios, también los nulos.
func TestLosMensajesDeClanesDelServidorSonLosDelEsquema(t *testing.T) {
	id := int64(7)
	nombre := "Roma"
	miembro := protocol.ClanMemberView{
		PlayerID: "a", CityID: &id, CityName: &nombre, Faction: "NEUTRAL", Role: clan.RoleLeader, JoinedAtMs: 1,
	}
	enviada := protocol.ClanSentInvitationView{PlayerID: "b", ExpiresAtMs: 2}
	vista := protocol.ClanView{
		ID: 3, Name: "Los Lobos", Tag: "LOB", LeaderID: "a",
		Members: []protocol.ClanMemberView{miembro}, SentInvitations: []protocol.ClanSentInvitationView{enviada},
	}
	invitacion := protocol.ClanInvitationView{ClanID: 4, Name: "Los Osos", Tag: "OSO", ExpiresAtMs: 3}

	root, update := serverVariant(t, protocol.TypeClanUpdate)
	p := dig(t, root, update, "properties", "payload").(map[string]any)
	carga := protocol.ClanUpdatePayload{Clan: &vista, Invitations: []protocol.ClanInvitationView{invitacion}}
	require.Equal(t, clavesDe(dig(t, root, p, "properties").(map[string]any)), clavesDe(serializado(t, carga)))
	require.Equal(t, requeridas(t, p), clavesDe(serializado(t, carga)))
	c := dig(t, root, p, "properties", "clan").(map[string]any)
	require.Equal(t, clavesDe(serializado(t, vista)), clavesDe(dig(t, root, objetoDe(t, root, c), "properties").(map[string]any)))
	require.Nil(t, serializado(t, protocol.ClanUpdatePayload{Invitations: []protocol.ClanInvitationView{}})["clan"], "sin clan, null")

	root, info := serverVariant(t, protocol.TypeClanInfo)
	p = dig(t, root, info, "properties", "payload").(map[string]any)
	publica := protocol.ClanInfoPayload{ClanID: 3, Name: "Los Lobos", Tag: "LOB", Members: []protocol.ClanMemberView{miembro}}
	require.Equal(t, clavesDe(dig(t, root, p, "properties").(map[string]any)), clavesDe(serializado(t, publica)))
	m := dig(t, root, p, "properties", "members", "items").(map[string]any)
	require.Equal(t, requeridas(t, m), clavesDe(serializado(t, protocol.ClanMemberView{})))
	// SPEC-CLAN-GOVERNANCE Q1: los tres rangos del dominio, y ninguno más.
	rol := dig(t, root, objetoDe(t, root, m), "properties", "role").(map[string]any)
	assert.ElementsMatch(t, []any{clan.RoleLeader, clan.RoleOfficer, clan.RoleMember}, rol["enum"])

	mensaje := protocol.ClanMessageView{ID: 1, ClanID: 3, FromPlayerID: "a", FromCityID: &id, FromCityName: &nombre, Body: "al norte", SentAtMs: 1}
	root, nuevo := serverVariant(t, protocol.TypeClanChatNew)
	p = dig(t, root, nuevo, "properties", "payload").(map[string]any)
	mm := dig(t, root, p, "properties", "message").(map[string]any)
	require.Equal(t, clavesDe(dig(t, root, mm, "properties").(map[string]any)), clavesDe(serializado(t, mensaje)))
	require.Equal(t, requeridas(t, mm), clavesDe(serializado(t, protocol.ClanMessageView{})))

	root, page := serverVariant(t, protocol.TypeClanChatPage)
	p = dig(t, root, page, "properties", "payload").(map[string]any)
	pagina := protocol.ClanChatPagePayload{ClanID: 3, Messages: []protocol.ClanMessageView{mensaje}}
	require.Equal(t, clavesDe(dig(t, root, p, "properties").(map[string]any)), clavesDe(serializado(t, pagina)))
	require.Equal(t, requeridas(t, p), clavesDe(serializado(t, pagina)))
}

// La vista de ciudad lleva siempre el clan de su dueño, null si no lo tiene, y
// city.update sólo cuando cambia (RN-CLAN-008).
func TestLaCiudadLlevaSuClan(t *testing.T) {
	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	ciudad := dig(t, root, snapshot, "properties", "payload", "properties", "cities", "items").(map[string]any)
	assert.Contains(t, requeridas(t, ciudad), "clan")
	vista := serializado(t, protocol.CityView{ID: 1, Faction: "NEUTRAL"})
	assert.Contains(t, vista, "clan")
	assert.Nil(t, vista["clan"], "sin clan, null")

	root, update := serverVariant(t, protocol.TypeCityUpdate)
	props := dig(t, root, update, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, props, "clan")
	assert.NotContains(t, serializado(t, protocol.CityUpdatePayload{ID: 1}), "clan")
	var ninguno *protocol.CityClanView
	sale := serializado(t, protocol.CityUpdatePayload{ID: 1, Clan: &ninguno})
	assert.Contains(t, sale, "clan")
	assert.Nil(t, sale["clan"], "null: el dueño salió de su clan")
}

// objetoDe es la variante objeto de un nodo que admite null (anyOf con
// {type: null}), o el propio nodo si no es una unión.
func objetoDe(t *testing.T, root map[string]any, node map[string]any) map[string]any {
	t.Helper()
	opciones, ok := node["anyOf"].([]any)
	if !ok {
		return node
	}
	for _, o := range opciones {
		m := resolve(t, root, o).(map[string]any)
		if _, tiene := m["properties"]; tiene {
			return m
		}
	}
	t.Fatalf("la unión no tiene una variante objeto")
	return nil
}
