package protocol_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de la economía (SPEC-ECON-WOOD §6, §9 «contract»): los tres
// comandos, los dos mensajes nuevos y los campos nuevos de los existentes,
// contra el JSON Schema exportado por packages/protocol.

// Los payloads Go de los tres comandos tienen exactamente los campos del
// esquema, todos obligatorios: ninguno admite que el cliente aporte carga,
// coste, tiempo ni ruta (INV-SEC-001).
func TestLosComandosDeEconomiaSonLosDelEsquema(t *testing.T) {
	casos := []struct {
		tipo    string
		payload any
	}{
		{protocol.TypeUnitGather, protocol.UnitGatherPayload{UnitID: 1, Target: protocol.Tile{X: 2, Y: 3}}},
		{protocol.TypeCityBuild, protocol.CityBuildPayload{CityID: 1, Type: string(building.TypeHouse), X: 2, Y: 3}},
		{protocol.TypeCityTrain, protocol.CityTrainPayload{CityID: 1, Type: string(unit.TypeVillager)}},
	}
	for _, c := range casos {
		t.Run(c.tipo, func(t *testing.T) {
			root, variant := clientVariant(t, c.tipo)
			payload := dig(t, root, variant, "properties", "payload").(map[string]any)
			declaradas := clavesDe(dig(t, root, payload, "properties").(map[string]any))
			require.Equal(t, declaradas, clavesDe(serializado(t, c.payload)))
			require.Equal(t, declaradas, requeridas(t, payload), "ningún campo es opcional")
		})
	}
}

// Los tipos de edificio y de unidad entrenable del esquema son los catálogos
// del dominio.
func TestLosCatalogosDeLosComandosSonLosDelDominio(t *testing.T) {
	root, build := clientVariant(t, protocol.TypeCityBuild)
	tipos := enumOf(t, dig(t, root, build, "properties", "payload", "properties", "type"))
	require.Equal(t, []string{string(building.TypeHouse)}, tipos)
	for _, tipo := range tipos {
		_, err := building.Lookup(building.Type(tipo))
		assert.NoError(t, err, "%s", tipo)
	}

	root, train := clientVariant(t, protocol.TypeCityTrain)
	for _, tipo := range enumOf(t, dig(t, root, train, "properties", "payload", "properties", "type")) {
		def, err := unit.Lookup(unit.Type(tipo))
		require.NoError(t, err, "%s", tipo)
		assert.True(t, def.CanTrain(), "%s debe poder entrenarse", tipo)
	}
}

// building.spawn serializa exactamente la vista del esquema, y building.update
// sólo su id y su estado; los estados son los del dominio.
func TestLosMensajesDeEdificioSonLosDelEsquema(t *testing.T) {
	root, spawn := serverVariant(t, protocol.TypeBuildingSpawn)
	vista := dig(t, root, spawn, "properties", "payload", "properties", "building").(map[string]any)
	declaradas := clavesDe(dig(t, root, vista, "properties").(map[string]any))
	b := protocol.BuildingView{ID: 1, CityID: 2, Type: string(building.TypeHouse), X: 3, Y: 4,
		Status: string(building.StatusUnderConstruction), CompletesAtMs: 5}
	require.Equal(t, declaradas, clavesDe(serializado(t, b)))
	require.Equal(t, declaradas, requeridas(t, vista))

	estados := enumOf(t, dig(t, root, vista, "properties", "status"))
	sort.Strings(estados)
	require.Equal(t, []string{string(building.StatusComplete), string(building.StatusUnderConstruction)}, estados)
	for _, e := range estados {
		assert.True(t, building.Status(e).Valid())
	}

	root, update := serverVariant(t, protocol.TypeBuildingUpdate)
	payload := dig(t, root, update, "properties", "payload").(map[string]any)
	require.Equal(t, clavesDe(dig(t, root, payload, "properties").(map[string]any)),
		clavesDe(serializado(t, protocol.BuildingUpdatePayload{ID: 1, Status: string(building.StatusComplete)})))

	// El snapshot trae la misma vista de edificio.
	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	item := dig(t, root, snapshot, "properties", "payload", "properties", "buildings", "items").(map[string]any)
	require.Equal(t, declaradas, clavesDe(dig(t, root, item, "properties").(map[string]any)))
}

// El snapshot y city.update transportan el stock con los recursos del
// catálogo, y la ciudad serializa exactamente las claves del esquema.
func TestLaCiudadLlevaSuStockYSuProduccion(t *testing.T) {
	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	ciudad := dig(t, root, snapshot, "properties", "payload", "properties", "cities", "items").(map[string]any)
	declaradas := clavesDe(dig(t, root, ciudad, "properties").(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.CityView{ID: 1})))

	stocks := dig(t, root, ciudad, "properties", "stocks").(map[string]any)
	recursos := clavesDe(dig(t, root, stocks, "properties").(map[string]any))
	esperados := make([]string, 0)
	for _, r := range economy.Resources() {
		esperados = append(esperados, string(r))
	}
	sort.Strings(esperados)
	require.Equal(t, esperados, recursos, "los recursos del esquema son el catálogo del dominio")

	update := serializado(t, protocol.CityUpdatePayload{ID: 1, Stocks: &protocol.Stocks{Wood: 40}})
	require.Equal(t, map[string]any{"WOOD": float64(40)}, update["stocks"])
	assert.NotContains(t, update, "production", "sin cambio de producción, la clave no viaja")

	var ninguna *protocol.ProductionView
	termina := serializado(t, protocol.CityUpdatePayload{ID: 1, Production: &ninguna})
	assert.Contains(t, termina, "production")
	assert.Nil(t, termina["production"], "null: la ciudad deja de entrenar")
}

// WORKING es un estado del dominio y del esquema, en el snapshot y en
// entity.update; la carga viaja en los dos.
func TestElEstadoWorkingYLaCargaEstanEnElEsquema(t *testing.T) {
	assert.True(t, unit.StatusWorking.Valid())

	root, update := serverVariant(t, protocol.TypeEntityUpdate)
	props := dig(t, root, update, "properties", "payload", "properties").(map[string]any)
	assert.Contains(t, enumOf(t, dig(t, root, props, "status")), string(unit.StatusWorking))
	assert.Contains(t, props, "carrying")

	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	item := dig(t, root, snapshot, "properties", "payload", "properties", "units", "items").(map[string]any)
	assert.Contains(t, enumOf(t, dig(t, root, item, "properties", "status")), string(unit.StatusWorking))

	carga := int32(10)
	delta := serializado(t, protocol.EntityUpdatePayload{ID: 1, Carrying: &carga})
	assert.EqualValues(t, 10, delta["carrying"])
	assert.NotContains(t, serializado(t, protocol.EntityUpdatePayload{ID: 1}), "carrying")
}

// SPEC-ECON-WOOD §5: los dos códigos nuevos están en los dos catálogos.
func TestLosCodigosDeLaEconomiaEstanEnElCatalogo(t *testing.T) {
	exportados, err := protocol.ExportedErrorCodes()
	require.NoError(t, err)
	for _, code := range []string{protocol.CodeInsufficientResources, protocol.CodeCityBusy} {
		assert.Contains(t, protocol.AllErrorCodes, code)
		assert.Contains(t, exportados, code)
	}
}
