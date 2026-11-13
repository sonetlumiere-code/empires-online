package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/caravan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de las caravanas (SPEC-CARAVANS §6, §9 «contract»): CARAVAN como tipo
// de unidad, caravan.update, caravans[] del snapshot y la carga en el registro y
// en los avisos, contra el JSON Schema exportado por packages/protocol.

// La vista de una caravana que serializa el servidor tiene exactamente los
// campos del esquema, todos obligatorios; los estados son los del dominio.
func TestLaVistaDeUnaCaravanaEsLaDelEsquema(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeCaravanUpdate)
	vista := dig(t, root, update, "properties", "payload", "properties", "caravan").(map[string]any)
	declaradas := clavesDe(dig(t, root, vista, "properties").(map[string]any))
	linea := []protocol.ResourceAmount{{Resource: "WINE", Amount: 30}}
	llegada := int64(1)
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.CaravanView{
		ID: 1, UnitID: 2, OwnerPlayerID: "x", SenderPlayerID: "x", FromCityID: 1, ToCityID: 2, OriginalToCityID: 2,
		Cargo: linea, Status: string(caravan.StatusInTransit), ArrivesAtMs: &llegada,
	})))
	require.Equal(t, declaradas, requeridas(t, vista), "ningún campo es opcional")
	carga := dig(t, root, vista, "properties", "cargo").(map[string]any)
	assert.EqualValues(t, 1, carga["minItems"])

	estados := make([]string, 0, 3)
	for _, s := range caravan.Statuses() {
		estados = append(estados, string(s))
	}
	assert.ElementsMatch(t, estados, enumOf(t, dig(t, root, vista, "properties", "status")))

	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	props := dig(t, root, snapshot, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, props, "caravans")
	assert.NotContains(t, requeridas(t, dig(t, root, snapshot, "properties", "payload").(map[string]any)), "caravans",
		"aditivo en v1 (R-VER-2)")
}

// CARAVAN es un tipo de unidad del esquema, y el catálogo del dominio lo conoce.
func TestCaravanEsUnTipoDeUnidadDelEsquema(t *testing.T) {
	root, spawn := serverVariant(t, protocol.TypeEntitySpawn)
	tipos := enumOf(t, dig(t, root, spawn, "properties", "payload", "properties", "unit", "properties", "unitType"))
	assert.Contains(t, tipos, string(unit.TypeCaravan))
	for _, tipo := range tipos {
		_, err := unit.Lookup(unit.Type(tipo))
		assert.NoError(t, err, "el tipo %s del esquema está en el catálogo", tipo)
	}
}

// La carga viaja en las entradas del registro y en los avisos, opcional.
func TestLaCargaDeUnaCaravanaEstaEnElRegistroYEnLosAvisos(t *testing.T) {
	root, page := serverVariant(t, protocol.TypeEventLogPage)
	entrada := dig(t, root, page, "properties", "payload", "properties", "entries", "items").(map[string]any)
	assert.Contains(t, dig(t, root, entrada, "properties").(map[string]any), "cargo")
	assert.NotContains(t, requeridas(t, entrada), "cargo")

	root, notice := serverVariant(t, protocol.TypeEventLogNotice)
	payload := dig(t, root, notice, "properties", "payload").(map[string]any)
	assert.Contains(t, dig(t, root, payload, "properties").(map[string]any), "cargo")
	assert.NotContains(t, requeridas(t, payload), "cargo")
}
