package simulation_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

const segundaCiudadID = int64(50)

// conSegundaCiudad da al jugador del arnés una segunda ciudad en Bronce.
func conSegundaCiudad(h *harness) *city.City {
	h.t.Helper()
	c := &city.City{
		ID: segundaCiudadID, OwnerPlayerID: h.playerID, Name: "Testópolis II", CenterX: 50, CenterY: 50,
		Era: city.EraBronze, Population: 3, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	}
	h.state.AddCity(c)
	return c
}

// SPEC-TRADE-FROM-CITY RN-TRADE-010: market.post con cityId reserva de esa ciudad,
// con su mercado; con una ciudad ajena, CITY_NOT_FOUND; sin cityId, la capital.
func TestElMercadoDesdeLaSegundaCiudad(t *testing.T) {
	h, compradores := mercadoConCompradores(t, 1)
	conSegundaCiudad(h)
	h.mercadoEn(segundaCiudadID)
	h.almacen(segundaCiudadID, 0, 100)

	h.send(simulation.PostMarketOffer{PlayerID: h.playerID, RequestID: uuid.NewString(), Give: piedra(20), Want: madera(30),
		Lots: 3, CityID: segundaCiudadID})
	_, piedraSegunda := h.recursos(segundaCiudadID)
	_, piedraCapital := h.recursos(h.cityID)
	assert.Equal(t, int64(34), piedraSegunda, "la custodia y la comisión salen de la segunda")
	assert.Equal(t, int64(100), piedraCapital, "la capital no paga")
	h.tick()
	require.Empty(t, h.codigos())
	o, ok := h.state.MarketOffer(primeraOfertaDelMercado)
	require.True(t, ok)
	assert.Equal(t, segundaCiudadID, o.CityID, "la oferta es de la segunda")

	h.rec.reset()
	ajena, _ := h.state.CityOf(compradores[0])
	h.send(simulation.PostMarketOffer{PlayerID: h.playerID, RequestID: uuid.NewString(), Give: piedra(20), Want: madera(30),
		Lots: 1, CityID: ajena.ID})
	assert.Equal(t, []string{protocol.CodeCityNotFound}, h.codigos(), "una ciudad ajena no es de dónde publicar")

	h.rec.reset()
	h.send(simulation.BuyMarketOffer{PlayerID: compradores[0], RequestID: uuid.NewString(), OfferID: primeraOfertaDelMercado,
		Lots: 1, CityID: h.cityID})
	assert.Equal(t, []string{protocol.CodeCityNotFound}, h.codigos(), "nadie compra desde una ciudad que no es suya")
}

// RN-TRADE-010: trade.propose con cityId comprueba el almacén de esa ciudad: la
// segunda, vacía, no puede dar lo que la capital sí tiene.
func TestElTratoDesdeLaSegundaCiudad(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	conSegundaCiudad(h)
	h.almacen(h.cityID, 100, 0)
	h.almacen(segundaCiudadID, 0, 0)
	dar := economy.Costs{madera(10)}
	pedir := economy.Costs{piedra(5)}

	h.send(simulation.ProposeTrade{PlayerID: h.playerID, RequestID: uuid.NewString(), Target: vecino,
		Give: dar, Want: pedir, CityID: segundaCiudadID})
	assert.Equal(t, []string{protocol.CodeInsufficientResources}, h.codigos(), "lo que da sale de la segunda")

	h.rec.reset()
	h.send(simulation.ProposeTrade{PlayerID: h.playerID, RequestID: uuid.NewString(), Target: vecino,
		Give: dar, Want: pedir, CityID: 999})
	assert.Equal(t, []string{protocol.CodeCityNotFound}, h.codigos())

	h.rec.reset()
	h.ofrecer(h.playerID, vecino, dar, pedir)
	assert.Empty(t, h.codigos(), "sin cityId, desde la capital, que sí tiene")
}
