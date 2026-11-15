package protocol_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de las facciones (SPEC-FACTIONS §6, §9 «contract»): faction.join, la
// facción y la entrada en la vista de ciudad y en city.update, y el bando del
// dueño en la vista de territorio, contra el JSON Schema exportado por
// packages/protocol.

// faction.join lleva sólo el bando, y el esquema no admite NEUTRAL: el enum
// compartido de la vista no abre el comando (RN-FAC-001).
func TestFactionJoinSoloNombraUnBandoAlQueSePuedeEntrar(t *testing.T) {
	require.True(t, protocol.ClientMessageTypes[protocol.TypeFactionJoin])
	root, variant := clientVariant(t, protocol.TypeFactionJoin)
	p := dig(t, root, variant, "properties", "payload").(map[string]any)
	props := dig(t, root, p, "properties").(map[string]any)
	assert.Equal(t, clavesDe(props), clavesDe(serializado(t, protocol.FactionJoinPayload{Faction: player.FactionOrder})))
	assert.Equal(t, []string{"faction"}, requeridas(t, p))
	bandos := enumOf(t, dig(t, root, props, "faction"))
	sort.Strings(bandos)
	assert.Equal(t, []string{player.FactionChaos, player.FactionOrder}, bandos)
}

// La vista de ciudad lleva la facción con los tres códigos del dominio y la
// entrada pendiente, que viaja como null si no la hay (RN-FAC-009).
func TestLaCiudadLlevaSuFaccionYSuEntrada(t *testing.T) {
	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	ciudad := dig(t, root, snapshot, "properties", "payload", "properties", "cities", "items").(map[string]any)
	props := dig(t, root, ciudad, "properties").(map[string]any)
	bandos := enumOf(t, dig(t, root, props, "faction"))
	sort.Strings(bandos)
	assert.Equal(t, []string{player.FactionChaos, player.FactionNeutral, player.FactionOrder}, bandos)
	assert.Contains(t, requeridas(t, ciudad), "faction")

	vista := serializado(t, protocol.CityView{ID: 1, Faction: player.FactionNeutral})
	assert.Contains(t, vista, "factionJoin")
	assert.Nil(t, vista["factionJoin"], "sin entrada pendiente, null")
	entrada := serializado(t, protocol.FactionJoinView{Faction: player.FactionChaos, AtMs: 5})
	join := dig(t, root, props, "factionJoin")
	assert.NotNil(t, join)
	assert.Equal(t, []string{"atMs", "faction"}, clavesDe(entrada))
}

// city.update sólo lleva la facción y la entrada cuando cambian; la entrada que
// cuenta viaja como null.
func TestCityUpdateLlevaLaFaccionSoloCuandoCambia(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeCityUpdate)
	props := dig(t, root, update, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, props, "faction")
	assert.Contains(t, props, "factionJoin")

	sin := serializado(t, protocol.CityUpdatePayload{ID: 1})
	assert.NotContains(t, sin, "faction")
	assert.NotContains(t, sin, "factionJoin")

	orden := player.FactionOrder
	var ninguna *protocol.FactionJoinView
	cuenta := serializado(t, protocol.CityUpdatePayload{ID: 1, Faction: &orden, FactionJoin: &ninguna})
	assert.Equal(t, player.FactionOrder, cuenta["faction"])
	assert.Contains(t, cuenta, "factionJoin")
	assert.Nil(t, cuenta["factionJoin"], "null: la entrada contó")
}

// La vista de territorio lleva el bando del dueño, null si no lo hay.
func TestElTerritorioLlevaElBandoDeSuDueno(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeTerritoryUpdate)
	territorio := dig(t, root, update, "properties", "payload", "properties", "territory").(map[string]any)
	props := dig(t, root, territorio, "properties").(map[string]any)
	assert.Contains(t, props, "ownerFaction")
	vista := serializado(t, protocol.TerritoryView{ID: 1, OwnerType: "NONE"})
	assert.Contains(t, vista, "ownerFaction")
	assert.Nil(t, vista["ownerFaction"])
}
