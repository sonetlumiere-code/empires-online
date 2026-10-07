package protocol_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// INV-CITY-004 en la frontera: los tres estados de presencia del dominio son
// exactamente el enum del esquema exportado, en city.update y en el snapshot.
// Ni el servidor puede emitir un estado que el cliente rechace, ni el esquema
// admite uno que el dominio no conozca.
func TestLosEstadosDePresenciaDelDominioSonLosDelEsquema(t *testing.T) {
	dominio := []string{
		string(city.PresenceOnline), string(city.PresenceOfflinePending), string(city.PresenceProtected),
	}
	sort.Strings(dominio)
	for _, p := range dominio {
		assert.True(t, city.PresenceState(p).Valid())
	}

	root, update := serverVariant(t, protocol.TypeCityUpdate)
	enUpdate := enumOf(t, dig(t, root, update, "properties", "payload", "properties", "presenceState"))
	sort.Strings(enUpdate)
	assert.Equal(t, dominio, enUpdate, "city.update")

	root, snapshot := serverVariant(t, protocol.TypeWorldSnapshot)
	enSnapshot := enumOf(t, dig(t, root, snapshot,
		"properties", "payload", "properties", "cities", "items", "properties", "presenceState"))
	sort.Strings(enSnapshot)
	assert.Equal(t, dominio, enSnapshot, "world.snapshot")
}
