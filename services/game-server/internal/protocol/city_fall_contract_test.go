package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de la caída de una ciudad (SPEC-CITY-FALL §6, §9 «contract»).

// unit.assault lleva exactamente la unidad y la ciudad, las dos obligatorias:
// el plazo y si se puede son del servidor (INV-SEC-001).
func TestElComandoDeAsaltoEsElDelEsquema(t *testing.T) {
	root, variant := clientVariant(t, protocol.TypeUnitAssault)
	payload := dig(t, root, variant, "properties", "payload").(map[string]any)
	declaradas := clavesDe(dig(t, root, payload, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.UnitAssaultPayload{UnitID: 1, CityID: 7})))
	require.Equal(t, declaradas, requeridas(t, payload), "ningún campo es opcional")
	require.True(t, protocol.ClientMessageTypes[protocol.TypeUnitAssault])
}

// city.fallen lleva la ciudad, su dueño, el vencedor y el instante, todos
// obligatorios.
func TestLaCiudadCaidaEsLaDelEsquema(t *testing.T) {
	root, variant := serverVariant(t, protocol.TypeCityFallen)
	payload := dig(t, root, variant, "properties", "payload").(map[string]any)
	declaradas := clavesDe(dig(t, root, payload, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.CityFallenPayload{
		CityID: 7, OwnerPlayerID: "a", ByPlayerID: "b", AtMs: 1,
	})))
	require.Equal(t, declaradas, requeridas(t, payload))
}

// El asalto viaja en la vista de la ciudad (opcional, con todas sus claves) y en
// city.update, donde null lo cierra; la inmunidad, en la vista.
func TestElAsaltoYLaInmunidadEstanEnElEsquema(t *testing.T) {
	asalto := &protocol.CityAssaultView{PlayerID: "a", UnitID: 42, StartedAtMs: 1, EndsAtMs: 2}
	root, snap := serverVariant(t, protocol.TypeWorldSnapshot)
	ciudad := dig(t, root, snap, "properties", "payload", "properties", "cities", "items").(map[string]any)
	props := dig(t, root, ciudad, "properties").(map[string]any)
	assert.Contains(t, props, "assault")
	assert.Contains(t, props, "fallImmuneUntilMs")
	assert.NotContains(t, requeridas(t, ciudad), "assault", "opcional: aditivo en v1")
	assert.NotContains(t, requeridas(t, ciudad), "fallImmuneUntilMs")
	vista := dig(t, root, props["assault"].(map[string]any)).(map[string]any)
	require.Equal(t, clavesDe(dig(t, root, vista, "properties").(map[string]any)), clavesDe(serializado(t, *asalto)))
	require.Equal(t, clavesDe(dig(t, root, vista, "properties").(map[string]any)), requeridas(t, vista))

	root, upd := serverVariant(t, protocol.TypeCityUpdate)
	assert.Contains(t, dig(t, root, upd, "properties", "payload", "properties").(map[string]any), "assault")
	assert.Contains(t, serializado(t, protocol.CityUpdatePayload{ID: 7, Assault: &asalto}), "assault")
	var ninguno *protocol.CityAssaultView
	cerrado := serializado(t, protocol.CityUpdatePayload{ID: 7, Assault: &ninguno})
	require.Contains(t, cerrado, "assault")
	assert.Nil(t, cerrado["assault"], "null cierra el asalto")
	assert.NotContains(t, serializado(t, protocol.CityUpdatePayload{ID: 7}), "assault")
}
