package protocol_test

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de territorios (docs/specs/territory.md §12): lo que el servidor
// serializa en territory.update y en world.snapshot.territories[] cabe en el
// esquema EXPORTADO por packages/protocol, que es lo que valida el cliente.

func clavesDe(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func requeridas(t *testing.T, schema map[string]any) []string {
	t.Helper()
	raw, ok := schema["required"].([]any)
	require.True(t, ok, "se esperaba una lista required")
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(string))
	}
	sort.Strings(out)
	return out
}

func TestTerritoryUpdateSerializaExactamenteLaVistaDelContrato(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeTerritoryUpdate)
	vista := dig(t, root, update, "properties", "payload", "properties", "territory").(map[string]any)
	declaradas := dig(t, root, vista, "properties").(map[string]any)
	require.Equal(t, false, vista["additionalProperties"])
	require.Equal(t, clavesDe(declaradas), requeridas(t, vista),
		"todas las claves de la vista son obligatorias")

	dueño := "11111111-1111-4111-8111-111111111111"
	casos := map[string]protocol.TerritoryView{
		// RN-TERR-011: sin dueño, ownerId viaja como null explícito, no se omite.
		"sin dueño": {ID: 1, Name: "Llanura", MinX: 0, MinY: 0, MaxX: 31, MaxY: 31, OwnerType: "NONE"},
		"con dueño": {ID: 2, Name: "Colina", MinX: 32, MinY: 0, MaxX: 63, MaxY: 31,
			OwnerType: "PLAYER", OwnerID: &dueño},
	}
	for nombre, v := range casos {
		t.Run(nombre, func(t *testing.T) {
			data, err := json.Marshal(protocol.TerritoryUpdatePayload{Territory: v})
			require.NoError(t, err)
			var emitido struct {
				Territory map[string]any `json:"territory"`
			}
			require.NoError(t, json.Unmarshal(data, &emitido))

			assert.Equal(t, clavesDe(declaradas), clavesDe(emitido.Territory),
				"ni una clave de más ni una de menos: el esquema es additionalProperties: false")
			_, presente := emitido.Territory["ownerId"]
			assert.True(t, presente, "ownerId es obligatorio aunque sea null")
		})
	}
}

// Los cuatro tipos de dueño del dominio son exactamente los del enum del
// esquema: ni el servidor puede emitir uno que el cliente rechace, ni el
// esquema admite uno que el dominio no conozca.
func TestLosTiposDeDuenoDelDominioSonLosDelEsquema(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeTerritoryUpdate)
	enum := enumOf(t, dig(t, root, update,
		"properties", "payload", "properties", "territory", "properties", "ownerType"))
	sort.Strings(enum)

	dominio := []string{
		string(territory.OwnerNone), string(territory.OwnerPlayer),
		string(territory.OwnerClan), string(territory.OwnerFaction),
	}
	sort.Strings(dominio)
	assert.Equal(t, dominio, enum)
	for _, o := range dominio {
		assert.True(t, territory.OwnerType(o).Valid())
	}

	tipos := dig(t, root, update,
		"properties", "payload", "properties", "territory", "properties", "ownerId", "type").([]any)
	assert.Contains(t, tipos, "null", "un territorio sin dueño viaja con ownerId: null")
}

// world.snapshot transporta la misma vista: el cliente no tiene dos formas de
// un territorio que reconciliar.
func TestElSnapshotUsaLaMismaVistaDeTerritorio(t *testing.T) {
	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	payload := dig(t, root, snapshot, "properties", "payload").(map[string]any)
	assert.Contains(t, requeridas(t, payload), "territories")

	enSnapshot := dig(t, root, payload, "properties", "territories", "items").(map[string]any)
	_, update := serverVariant(t, protocol.TypeTerritoryUpdate)
	enUpdate := dig(t, root, update, "properties", "payload", "properties", "territory").(map[string]any)
	assert.Equal(t, clavesDe(dig(t, root, enUpdate, "properties").(map[string]any)),
		clavesDe(dig(t, root, enSnapshot, "properties").(map[string]any)))
}
