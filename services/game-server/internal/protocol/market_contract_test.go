package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/market"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato del mercado (SPEC-MARKETS §6, §9 «contract»): los cuatro comandos,
// market.update, market.listing, marketOffers[] del snapshot y los dos tipos de
// entrada del registro contra el JSON Schema exportado por packages/protocol.

// Las tres órdenes llevan exactamente los campos del esquema, todos
// obligatorios: el cliente dice qué da, qué pide y cuántos lotes, nunca la
// comisión ni la caducidad (INV-SEC-001). market.list sólo lleva filtros
// opcionales.
func TestLosComandosDelMercadoSonLosDelEsquema(t *testing.T) {
	lote := protocol.ResourceAmount{Resource: "STONE", Amount: 20}
	casos := map[string]any{
		protocol.TypeMarketPost:   protocol.MarketPostPayload{Give: lote, Want: lote, Lots: 3},
		protocol.TypeMarketBuy:    protocol.MarketBuyPayload{OfferID: 1, Lots: 2},
		protocol.TypeMarketCancel: protocol.MarketCancelPayload{OfferID: 1},
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

	root, list := clientVariant(t, protocol.TypeMarketList)
	p := dig(t, root, list, "properties", "payload").(map[string]any)
	declaradas := clavesDe(dig(t, root, p, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.MarketListPayload{Receive: "SILK", Pay: "WOOD"})))
	if req, ok := p["required"].([]any); ok {
		require.Empty(t, req, "los dos filtros son opcionales")
	}
	require.Empty(t, clavesDe(serializado(t, protocol.MarketListPayload{})))
	require.True(t, protocol.ClientMessageTypes[protocol.TypeMarketList])

	// Los lotes, de 1 al máximo del dominio.
	root, post := clientVariant(t, protocol.TypeMarketPost)
	lots := dig(t, root, post, "properties", "payload", "properties", "lots").(map[string]any)
	assert.EqualValues(t, 1, lots["minimum"])
	assert.EqualValues(t, market.MaxLots, lots["maximum"])
}

// La vista de una oferta tiene exactamente los campos del esquema, en
// market.update, en market.listing y en el snapshot; sus estados son los del
// dominio.
func TestLaVistaDeUnaOfertaDelMercadoEsLaDelEsquema(t *testing.T) {
	lote := protocol.ResourceAmount{Resource: "STONE", Amount: 20}
	vista := protocol.MarketOfferView{ID: 1, PlayerID: "x", CityID: 2, Give: lote, Want: lote, LotsTotal: 3, LotsLeft: 3,
		Status: "OPEN", ExpiresAtMs: 1}

	root, update := serverVariant(t, protocol.TypeMarketUpdate)
	esquema := dig(t, root, update, "properties", "payload", "properties", "offer").(map[string]any)
	declaradas := clavesDe(dig(t, root, esquema, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, vista)))
	require.Equal(t, declaradas, requeridas(t, esquema), "todos los campos van siempre, aunque sean null")
	estados := make([]string, 0, len(market.Statuses()))
	for _, s := range market.Statuses() {
		estados = append(estados, string(s))
	}
	assert.ElementsMatch(t, estados, enumOf(t, dig(t, root, esquema, "properties", "status")))

	root, listing := serverVariant(t, protocol.TypeMarketListing)
	ofertas := dig(t, root, listing, "properties", "payload", "properties", "offers").(map[string]any)
	assert.EqualValues(t, 100, ofertas["maxItems"])
	esquema = dig(t, root, ofertas, "items").(map[string]any)
	require.Equal(t, declaradas, clavesDe(dig(t, root, esquema, "properties").(map[string]any)))
	require.Equal(t, []string{"offers"}, clavesDe(serializado(t, protocol.MarketListingPayload{Offers: []protocol.MarketOfferView{}})))

	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	props := dig(t, root, snapshot, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, props, "marketOffers")
}

// El registro conoce los dos tipos del mercado y sus lotes; otherPlayerId es
// obligatorio pero admite null, que sólo manda MARKET_EXPIRED.
func TestLasEntradasDelMercadoSonLasDelEsquema(t *testing.T) {
	root, page := serverVariant(t, protocol.TypeEventLogPage)
	entrada := dig(t, root, page, "properties", "payload", "properties", "entries", "items").(map[string]any)
	props := dig(t, root, entrada, "properties").(map[string]any)
	assert.Contains(t, props, "lots")
	tipos := enumOf(t, dig(t, root, entrada, "properties", "kind"))
	for _, k := range []eventlog.Kind{eventlog.KindMarketSold, eventlog.KindMarketExpired} {
		assert.Contains(t, tipos, string(k))
	}
	assert.Contains(t, requeridas(t, entrada), "otherPlayerId")
	sinOtro := serializado(t, protocol.EventLogEntry{ID: 1, Kind: string(eventlog.KindMarketExpired), Count: 1})
	assert.Contains(t, sinOtro, "otherPlayerId")
	assert.Nil(t, sinOtro["otherPlayerId"])
}
