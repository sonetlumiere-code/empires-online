package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato del comercio (SPEC-TRADE §6, §9 «contract»): los tres comandos,
// trade.update, tradeOffers[] del snapshot y las cantidades del registro contra
// el JSON Schema exportado por packages/protocol.

// Cada comando lleva exactamente los campos del esquema, todos obligatorios: el
// cliente dice a quién, qué da y qué pide, nunca qué se mueve ni cuándo caduca
// (INV-SEC-001).
func TestLosComandosDeComercioSonLosDelEsquema(t *testing.T) {
	linea := []protocol.ResourceAmount{{Resource: "WOOD", Amount: 1}}
	casos := map[string]any{
		protocol.TypeTradePropose: protocol.TradeProposePayload{PlayerID: "x", Give: linea, Want: linea},
		protocol.TypeTradeRespond: protocol.TradeRespondPayload{OfferID: 1, Accept: true},
		protocol.TypeTradeCancel:  protocol.TradeCancelPayload{OfferID: 1},
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

// Una línea de la oferta: recurso y cantidad, los dos obligatorios; la cantidad,
// de 1 a la cota técnica (Q3, ADR-032 §7).
func TestUnaLineaDeComercioEsLaDelEsquema(t *testing.T) {
	root, variant := clientVariant(t, protocol.TypeTradePropose)
	give := dig(t, root, variant, "properties", "payload", "properties", "give").(map[string]any)
	assert.EqualValues(t, 1, give["minItems"])
	linea := dig(t, root, give, "items").(map[string]any)
	declaradas := clavesDe(dig(t, root, linea, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.ResourceAmount{Resource: "STONE", Amount: 5})))
	require.Equal(t, declaradas, requeridas(t, linea))
	cantidad := dig(t, root, linea, "properties", "amount").(map[string]any)
	assert.EqualValues(t, 1, cantidad["minimum"])
	assert.EqualValues(t, 1_000_000, cantidad["maximum"])
	assert.ElementsMatch(t, []string{"WOOD", "STONE"}, enumOf(t, dig(t, root, linea, "properties", "resource")))
}

// La vista de una oferta que serializa el servidor tiene exactamente los campos
// del esquema, en trade.update y en el snapshot.
func TestLaVistaDeUnaOfertaEsLaDelEsquema(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeTradeUpdate)
	vista := dig(t, root, update, "properties", "payload", "properties", "offer").(map[string]any)
	declaradas := clavesDe(dig(t, root, vista, "properties").(map[string]any))
	linea := []protocol.ResourceAmount{{Resource: "WOOD", Amount: 1}}
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.TradeOfferView{
		ID: 1, OtherPlayerID: "x", Give: linea, Get: linea, Status: "PROPOSED", ExpiresAtMs: 1,
	})))
	assert.ElementsMatch(t, []string{"PROPOSED", "ACCEPTED", "REJECTED", "WITHDRAWN", "EXPIRED"},
		enumOf(t, dig(t, root, vista, "properties", "status")))

	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	props := dig(t, root, snapshot, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, props, "tradeOffers")
}

// Las entradas del registro conocen los cinco tipos de trato y las cantidades
// vistas desde quien lee.
func TestLasEntradasDeTratoSonLasDelEsquema(t *testing.T) {
	root, page := serverVariant(t, protocol.TypeEventLogPage)
	entrada := dig(t, root, page, "properties", "payload", "properties", "entries", "items").(map[string]any)
	props := dig(t, root, entrada, "properties").(map[string]any)
	assert.Contains(t, props, "tradeGive")
	assert.Contains(t, props, "tradeGet")
	tipos := enumOf(t, dig(t, root, entrada, "properties", "kind"))
	for _, k := range []string{"TRADE_OFFERED", "TRADE_ACCEPTED", "TRADE_REJECTED", "TRADE_WITHDRAWN", "TRADE_OFFER_EXPIRED"} {
		assert.Contains(t, tipos, k)
	}
}
