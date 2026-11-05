package protocol_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato de SPEC-CIVILIZATION §6: la ciudad del snapshot lleva su
// civilización como uno de los códigos del catálogo exportado, y el catálogo
// de presentación sólo usa claves que el dominio aplica.
func TestLaCiudadLlevaUnaCivilizacionDelCatalogo(t *testing.T) {
	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	ciudad := dig(t, root, snapshot, "properties", "payload", "properties", "cities", "items").(map[string]any)
	assert.Contains(t, requeridas(t, resolve(t, root, ciudad).(map[string]any)), "civilization", "es obligatoria")
	codigos := enumOf(t, dig(t, root, ciudad, "properties", "civilization"))

	catalogo, err := protocol.ExportedCivilizations()
	require.NoError(t, err)
	exportados := make([]string, 0, len(catalogo))
	claves := player.TraitKeys()
	for _, c := range catalogo {
		exportados = append(exportados, c.Code)
		for k, v := range c.Traits {
			assert.Contains(t, claves, k, "%s declara una clave que el servidor no aplica", c.Code)
			assert.Positive(t, v, "%s.%s", c.Code, k)
		}
	}
	assert.Equal(t, codigos, exportados, "el enum y el catálogo, en el mismo orden")

	sort.Strings(codigos)
	assert.Equal(t, []string{"BYZANTINE", "NORSE", "PERSIAN", "ROMAN"}, codigos)
}
