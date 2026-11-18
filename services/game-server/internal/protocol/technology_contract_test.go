package protocol_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contrato del árbol tecnológico (SPEC-TECHNOLOGIES §6, §9 «contract»).

// city.research lleva la ciudad y la tecnología: el coste, el plazo y si está a
// su alcance son del servidor (INV-SEC-001).
func TestElComandoDeInvestigarEsElDelEsquema(t *testing.T) {
	root, variant := clientVariant(t, protocol.TypeCityResearch)
	payload := dig(t, root, variant, "properties", "payload").(map[string]any)
	declaradas := clavesDe(dig(t, root, payload, "properties").(map[string]any))
	require.Equal(t, []string{"cityId", "technology"}, declaradas)
	require.Equal(t, declaradas, clavesDe(serializado(t, protocol.CityResearchPayload{CityID: 7, Technology: "WHEEL"})))
	require.Equal(t, declaradas, requeridas(t, payload))
	require.True(t, protocol.ClientMessageTypes[protocol.TypeCityResearch])
	assert.Nil(t, dig(t, root, payload, "properties", "technology").(map[string]any)["enum"],
		"no es un enum estricto: el catálogo vive en la base (ADR-046 §5)")
}

// city.update y la vista de la ciudad llevan la investigación y las
// tecnologías, opcionales; la vista de la investigación es la del esquema.
func TestLaInvestigacionViajaEnLaCiudad(t *testing.T) {
	root, update := serverVariant(t, protocol.TypeCityUpdate)
	payload := dig(t, root, update, "properties", "payload").(map[string]any)
	props := dig(t, root, payload, "properties").(map[string]any)
	require.Contains(t, props, "research")
	require.Contains(t, props, "technologies")
	assert.NotContains(t, requeridas(t, payload), "research")
	assert.NotContains(t, requeridas(t, payload), "technologies")

	vista := protocol.ResearchView{Technology: "WHEEL", CompletesAtMs: 9}
	ptr := &vista
	techs := []string{"WHEEL"}
	serial := clavesDe(serializado(t, protocol.CityUpdatePayload{ID: 1, Research: &ptr, Technologies: &techs}))
	assert.Contains(t, serial, "research")
	assert.Contains(t, serial, "technologies")
	assert.NotContains(t, clavesDe(serializado(t, protocol.CityUpdatePayload{ID: 1})), "research", "sin cambio, no viaja")

	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	ciudad := dig(t, root, snapshot, "properties", "payload", "properties", "cities", "items").(map[string]any)
	rv := resolve(t, root, dig(t, root, ciudad, "properties", "research").(map[string]any)).(map[string]any)
	var objeto map[string]any
	for _, alt := range rv["anyOf"].([]any) {
		if m := resolve(t, root, alt.(map[string]any)).(map[string]any); m["type"] == "object" {
			objeto = m
		}
	}
	require.NotNil(t, objeto, "research es una vista o null")
	declaradas := clavesDe(objeto["properties"].(map[string]any))
	require.Equal(t, declaradas, clavesDe(serializado(t, vista)))
	require.Equal(t, declaradas, requeridas(t, objeto))
}

// El catálogo de presentación sólo usa claves de rasgo que el servidor aplica,
// con valores positivos y códigos que pasan el patrón del esquema. Que coincida
// con la tabla lo comprueba un test de integración.
func TestElCatalogoDeTecnologiasExportado(t *testing.T) {
	catalogo, err := protocol.ExportedTechnologies()
	require.NoError(t, err)
	require.Len(t, catalogo, 12, "SPEC-TECHNOLOGIES §4.1")
	patron := regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,47}$`)
	claves := player.TraitKeys()
	exclusivas := 0
	for _, tec := range catalogo {
		assert.Regexp(t, patron, tec.Code)
		assert.NotEmpty(t, tec.Effects, tec.Code)
		for k, v := range tec.Effects {
			assert.Contains(t, claves, k, "%s declara una clave que el servidor no aplica", tec.Code)
			assert.Positive(t, v, "%s.%s", tec.Code, k)
		}
		if tec.Civilization != nil {
			exclusivas++
			assert.Positive(t, tec.OwnGood, tec.Code)
		}
	}
	assert.Equal(t, 4, exclusivas, "una por civilización")
}
