package protocol_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato del registro (SPEC-EVENT-LOG §6, §9 «contract»): los dos comandos y
// event_log.page contra el JSON Schema exportado por packages/protocol.

// Los comandos llevan sólo el id: ninguno nombra a un jugador (INV-SEC-009).
// beforeId es opcional; upToId, obligatorio.
func TestLosComandosDelRegistroSonLosDelEsquema(t *testing.T) {
	antes := int64(5)
	root, fetch := clientVariant(t, protocol.TypeEventLogFetch)
	p := dig(t, root, fetch, "properties", "payload").(map[string]any)
	require.Equal(t, []string{"beforeId"}, clavesDe(dig(t, root, p, "properties").(map[string]any)))
	require.Equal(t, []string{"beforeId"}, clavesDe(serializado(t, protocol.EventLogFetchPayload{BeforeID: &antes})))
	_, obligatorio := p["required"]
	assert.False(t, obligatorio, "beforeId es opcional")
	assert.Empty(t, serializado(t, protocol.EventLogFetchPayload{}), "sin beforeId no se serializa")

	root, seen := clientVariant(t, protocol.TypeEventLogSeen)
	p = dig(t, root, seen, "properties", "payload").(map[string]any)
	require.Equal(t, clavesDe(dig(t, root, p, "properties").(map[string]any)), clavesDe(serializado(t, protocol.EventLogSeenPayload{UpToID: 1})))
	require.Equal(t, []string{"upToId"}, requeridas(t, p))

	for _, tipo := range []string{protocol.TypeEventLogFetch, protocol.TypeEventLogSeen} {
		require.True(t, protocol.ClientMessageTypes[tipo], tipo)
	}
}

// La página y cada entrada que serializa el servidor tienen exactamente los
// campos del esquema: los obligatorios siempre, y los opcionales sólo cuando
// están (omitempty). El enum de tipos es el catálogo del dominio.
func TestLaPaginaDelRegistroEsLaDelEsquema(t *testing.T) {
	root, page := serverVariant(t, protocol.TypeEventLogPage)
	payload := dig(t, root, page, "properties", "payload").(map[string]any)
	require.Equal(t, clavesDe(dig(t, root, payload, "properties").(map[string]any)),
		clavesDe(serializado(t, protocol.EventLogPagePayload{Entries: []protocol.EventLogEntry{}})))
	assert.Equal(t, []string{"entries", "nextBeforeId", "seenUpToId"}, requeridas(t, payload))

	entrada := dig(t, root, payload, "properties", "entries", "items").(map[string]any)
	declaradas := clavesDe(dig(t, root, entrada, "properties").(map[string]any))

	var (
		id, n  = int64(7), int64(85)
		x, y   = int32(1), int32(2)
		nombre = "Villa Norte"
		mia    = true
	)
	completa := protocol.EventLogEntry{
		ID: 1, Kind: "PILLAGED", Count: 1, OtherPlayerID: "x", OtherCityID: &id, OtherCityName: &nombre,
		UnitType: "VILLAGER", Amount: &n, TreatyType: "ALLIANCE", ProposedByMe: &mia, X: &x, Y: &y, BreaksAtMs: &n,
	}
	require.Equal(t, declaradas, clavesDe(serializado(t, completa)), "con todo, los campos del esquema")
	minima := clavesDe(serializado(t, protocol.EventLogEntry{ID: 1, Kind: "PILLAGED", Count: 1, OtherPlayerID: "x"}))
	require.Equal(t, requeridas(t, entrada), minima, "sin opcionales, sólo los obligatorios")

	kinds := enumOf(t, dig(t, root, entrada, "properties", "kind"))
	var dominio []string
	for _, k := range eventlog.Kinds() {
		dominio = append(dominio, string(k))
	}
	sort.Strings(kinds)
	sort.Strings(dominio)
	assert.Equal(t, dominio, kinds)
}
