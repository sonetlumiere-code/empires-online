package protocol_test

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de Safe Zones (docs/specs/safe-zones.md §12): el ocultamiento no
// añade ningún mensaje al protocolo v1. Reutiliza `entity.despawn` con
// `reason: "HIDDEN"` y `entity.update` con `status: "HIDDEN"`. Estos tests leen
// los enums DEL ESQUEMA EXPORTADO por packages/protocol —no de una copia en Go—
// y comprueban que lo que el servidor emite cabe en ellos.

// serverVariant devuelve el esquema de la variante de mensaje con ese `type`.
func serverVariant(t *testing.T, msgType string) (root map[string]any, variant map[string]any) {
	t.Helper()
	raw, err := protocol.SchemaFile("server-message.schema.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &root))

	anyOf := walk(t, root, root, []string{"definitions", "ServerMessage", "anyOf"}).([]any)
	for _, v := range anyOf {
		m := v.(map[string]any)
		if dig(t, root, m, "properties", "type", "const") == msgType {
			return root, m
		}
	}
	t.Fatalf("el esquema exportado no tiene la variante %q", msgType)
	return nil, nil
}

// dig recorre el esquema siguiendo las claves y resolviendo `$ref` locales
// (`#/a/b/0/c`) por el camino, que es como zod-to-json-schema deduplica.
func dig(t *testing.T, root map[string]any, node any, path ...string) any {
	t.Helper()
	return walk(t, root, resolve(t, root, node), path)
}

// walk desciende sin resolver el nodo de partida. Hace falta para seguir un
// `$ref` desde la raíz, que a su vez lleva un `$ref` propio.
func walk(t *testing.T, root map[string]any, cur any, path []string) any {
	t.Helper()
	for _, key := range path {
		switch n := cur.(type) {
		case map[string]any:
			next, ok := n[key]
			require.True(t, ok, "el esquema no tiene la clave %q", key)
			cur = resolve(t, root, next)
		case []any:
			i, err := strconv.Atoi(key)
			require.NoError(t, err)
			cur = resolve(t, root, n[i])
		default:
			t.Fatalf("no se puede descender por %q", key)
		}
	}
	return cur
}

func resolve(t *testing.T, root map[string]any, node any) any {
	t.Helper()
	for {
		m, ok := node.(map[string]any)
		if !ok {
			return node
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return node
		}
		require.True(t, strings.HasPrefix(ref, "#/"), "sólo se admiten referencias locales: %s", ref)
		node = walk(t, root, root, strings.Split(strings.TrimPrefix(ref, "#/"), "/"))
	}
}

func enumOf(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.(map[string]any)["enum"].([]any)
	require.True(t, ok, "se esperaba un enum")
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		out = append(out, e.(string))
	}
	return out
}

func TestEntityDespawnAdmiteElMotivoHidden(t *testing.T) {
	root, despawn := serverVariant(t, protocol.TypeEntityDespawn)
	payload := dig(t, root, despawn, "properties", "payload").(map[string]any)

	require.Contains(t, enumOf(t, dig(t, root, payload, "properties", "reason")), protocol.DespawnHidden)

	// El payload que emite el servidor tiene exactamente las claves del
	// contrato: el esquema declara additionalProperties: false.
	data, err := json.Marshal(protocol.EntityDespawnPayload{ID: 7, Reason: protocol.DespawnHidden})
	require.NoError(t, err)
	var emitido map[string]any
	require.NoError(t, json.Unmarshal(data, &emitido))

	declaradas := dig(t, root, payload, "properties").(map[string]any)
	claves := func(m map[string]any) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	require.Equal(t, claves(declaradas), claves(emitido))
	require.Equal(t, false, payload["additionalProperties"])
}

func TestEntityUpdateYUnitViewAdmitenElEstadoHidden(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeEntityUpdate)
	require.Contains(t,
		enumOf(t, dig(t, root, update, "properties", "payload", "properties", "status")),
		string(unit.StatusHidden))

	// El propietario recibe su unidad oculta en el snapshot con status HIDDEN.
	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	require.Contains(t,
		enumOf(t, dig(t, root, snapshot, "properties", "payload", "properties", "units", "items", "properties", "status")),
		string(unit.StatusHidden))
}

// Ningún mensaje cliente→servidor menciona zonas ni ocultamiento (RN-SAFE-008).
func TestNingunComandoDelClienteMencionaZonasNiOcultamiento(t *testing.T) {
	raw, err := protocol.SchemaFile("client-message.schema.json")
	require.NoError(t, err)
	texto := strings.ToLower(string(raw))
	for _, prohibido := range []string{"hidden", "safe", "zone", "conceal"} {
		require.NotContains(t, texto, prohibido)
	}
}
