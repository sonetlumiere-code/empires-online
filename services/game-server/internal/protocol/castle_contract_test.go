package protocol_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de la Edad de los Castillos (SPEC-CASTLE-AGE §6, §9 «contract»).

// ptrTo envuelve un puntero para los campos opcionales y anulables (`**T`).
func ptrTo[T any](p *T) **T { return &p }

// BuildingType es el catálogo de edificios del dominio, con CASTLE, en city.build
// y en la vista del edificio.
func TestElTipoDeEdificioIncluyeElCastillo(t *testing.T) {
	// Desde E26, también MARKET (SPEC-MARKETS §6), y desde E27 WALL
	// (SPEC-SIEGE-ENGINES §6).
	esperados := []string{string(building.TypeCastle), string(building.TypeHouse), string(building.TypeMarket), string(building.TypeWall), string(building.TypeWorkshop)}
	for _, tipo := range esperados {
		_, err := building.Lookup(building.Type(tipo))
		require.NoError(t, err, "%s está en el catálogo de Go", tipo)
	}

	root, build := clientVariant(t, protocol.TypeCityBuild)
	tipos := enumOf(t, dig(t, root, build, "properties", "payload", "properties", "type"))
	sort.Strings(tipos)
	assert.Equal(t, esperados, tipos, "city.build")

	root, spawn := serverVariant(t, protocol.TypeBuildingSpawn)
	tipos = enumOf(t, dig(t, root, spawn, "properties", "payload", "properties", "building", "properties", "type"))
	sort.Strings(tipos)
	assert.Equal(t, esperados, tipos, "building.spawn")
}

// targetUnitId es opcional y anulable en la vista y en building.update: la vista
// lo omite sin objetivo, y building.update lo manda null cuando el castillo deja
// de apuntar y lo omite cuando no cambia.
func TestElObjetivoDelCastilloEsOpcionalYAnulable(t *testing.T) {
	root, spawn := serverVariant(t, protocol.TypeBuildingSpawn)
	vista := dig(t, root, spawn, "properties", "payload", "properties", "building").(map[string]any)
	assert.Contains(t, dig(t, root, vista, "properties").(map[string]any), "targetUnitId")
	assert.NotContains(t, requeridas(t, vista), "targetUnitId")
	assert.NotContains(t, serializado(t, protocol.BuildingView{ID: 1}), "targetUnitId", "sin objetivo, la vista no lo lleva")

	root, update := serverVariant(t, protocol.TypeBuildingUpdate)
	payload := dig(t, root, update, "properties", "payload").(map[string]any)
	assert.Contains(t, dig(t, root, payload, "properties").(map[string]any), "targetUnitId")
	assert.NotContains(t, requeridas(t, payload), "targetUnitId")

	objetivo := int64(9)
	con := serializado(t, protocol.BuildingUpdatePayload{ID: 1, Status: "COMPLETE", TargetUnitID: ptrTo(&objetivo)})
	assert.Equal(t, float64(9), con["targetUnitId"])
	sin := serializado(t, protocol.BuildingUpdatePayload{ID: 1, Status: "COMPLETE", TargetUnitID: ptrTo[int64](nil)})
	v, ok := sin["targetUnitId"]
	require.True(t, ok, "null cuando deja de apuntar")
	assert.Nil(t, v)
	assert.NotContains(t, serializado(t, protocol.BuildingUpdatePayload{ID: 1, Status: "COMPLETE"}), "targetUnitId")
}
