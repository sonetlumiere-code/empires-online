package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de la guarnición (ADR-017 §2.1, docs/specs/garrison.md §12). Como
// los de Safe Zones, estos tests leen el esquema EXPORTADO por packages/protocol
// y comprueban que lo que el servidor decodifica y emite cabe en él.

// clientVariant devuelve el esquema de la variante cliente→servidor con ese `type`.
func clientVariant(t *testing.T, msgType string) (root map[string]any, variant map[string]any) {
	t.Helper()
	raw, err := protocol.SchemaFile("client-message.schema.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &root))

	anyOf := walk(t, root, root, []string{"definitions", "ClientMessage", "anyOf"}).([]any)
	for _, v := range anyOf {
		m := v.(map[string]any)
		if dig(t, root, m, "properties", "type", "const") == msgType {
			return root, m
		}
	}
	t.Fatalf("el esquema exportado no tiene la variante %q", msgType)
	return nil, nil
}

func serializado(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

// Los payloads Go de los dos comandos tienen exactamente los campos del
// esquema, todos obligatorios.
func TestLosComandosDeGuarnicionSonLosDelEsquema(t *testing.T) {
	casos := []struct {
		tipo    string
		payload any
	}{
		{protocol.TypeUnitGarrison, protocol.UnitGarrisonPayload{UnitID: 1, CityID: 2}},
		{protocol.TypeUnitUngarrison, protocol.UnitUngarrisonPayload{UnitID: 1}},
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

// UnitView lleva garrisonCityId siempre —null fuera de una guarnición— y con
// él, exactamente las claves del esquema; cityId sigue siendo la de origen.
func TestUnitViewLlevaLaCiudadAnfitrionaSiempre(t *testing.T) {
	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	item := dig(t, root, snapshot, "properties", "payload", "properties", "units", "items").(map[string]any)
	declaradas := clavesDe(dig(t, root, item, "properties").(map[string]any))

	origen, anfitriona := int64(3), int64(9)
	fuera := serializado(t, protocol.UnitView{ID: 1, CityID: &origen, Status: string(unit.StatusIdle)})
	// Todo lo obligatorio viaja y nada viaja sin declarar. La única clave
	// opcional es `carrying` (SPEC-ECON-WOOD), que sólo viaja si hay carga.
	require.Equal(t, requeridas(t, item), clavesDe(fuera))
	require.Subset(t, declaradas, clavesDe(fuera))
	conCarga := serializado(t, protocol.UnitView{ID: 1, CityID: &origen, Status: string(unit.StatusMoving), Carrying: 10})
	require.Equal(t, declaradas, clavesDe(conCarga))
	require.Contains(t, requeridas(t, item), "garrisonCityId")
	require.Nil(t, fuera["garrisonCityId"])

	dentro := serializado(t, protocol.UnitView{ID: 1, CityID: &origen, GarrisonCityID: &anfitriona,
		Status: string(unit.StatusGarrisoned)})
	require.EqualValues(t, 9, dentro["garrisonCityId"])
	require.EqualValues(t, 3, dentro["cityId"])
	require.Contains(t, enumOf(t, dig(t, root, item, "properties", "status")), string(unit.StatusGarrisoned))
}

// entity.update distingue los tres casos: sin cambio (la clave no viaja), salida
// (null) y entrada (el id).
func TestEntityUpdateDistingueEntradaSalidaYSinCambio(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeEntityUpdate)
	props := dig(t, root, update, "properties", "payload", "properties").(map[string]any)
	require.Contains(t, props, "garrisonCityId")

	sinCambio := serializado(t, protocol.EntityUpdatePayload{ID: 1})
	require.NotContains(t, sinCambio, "garrisonCityId")

	var ninguna *int64
	salida := serializado(t, protocol.EntityUpdatePayload{ID: 1, GarrisonCityID: &ninguna})
	require.Contains(t, salida, "garrisonCityId")
	require.Nil(t, salida["garrisonCityId"])

	anfitriona := int64(9)
	ptr := &anfitriona
	entrada := serializado(t, protocol.EntityUpdatePayload{ID: 1, GarrisonCityID: &ptr})
	require.EqualValues(t, 9, entrada["garrisonCityId"])

	for _, emitido := range []map[string]any{sinCambio, salida, entrada} {
		for k := range emitido {
			require.Contains(t, props, k, "el servidor emite %q y el esquema no lo declara", k)
		}
	}
}

func TestEntityDespawnAdmiteElMotivoGarrisoned(t *testing.T) {
	root, despawn := serverVariant(t, protocol.TypeEntityDespawn)
	require.Contains(t,
		enumOf(t, dig(t, root, despawn, "properties", "payload", "properties", "reason")),
		protocol.DespawnGarrisoned)
}
