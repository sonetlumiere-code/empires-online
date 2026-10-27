package websocket

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// BroadcastChunkTransition es el reparto por diferencia de conjuntos del cruce
// de frontera (docs/architecture/networking.md §4.3): quien gana el chunk
// recibe el spawn, quien lo pierde el despawn, y quien tiene los dos —o
// ninguno— nada.

var (
	chunkA = world.ChunkCoord{CX: 1, CY: 1}
	chunkB = world.ChunkCoord{CX: 2, CY: 1}
)

func transicion(hub *Hub, onlyPlayer uuid.UUID) {
	hub.BroadcastChunkTransition(chunkA, chunkB, onlyPlayer,
		protocol.TypeEntitySpawn, protocol.EntitySpawnPayload{Unit: protocol.UnitView{ID: 7}},
		protocol.TypeEntityDespawn, protocol.EntityDespawnPayload{ID: 7, Reason: protocol.DespawnOutOfInterest})
}

func TestBroadcastChunkTransitionRepartePorDiferenciaDeConjuntos(t *testing.T) {
	hub := NewHub(nil, quietLogger())
	soloNuevo := nuevaSesionDePrueba(t)
	soloViejo := nuevaSesionDePrueba(t)
	ambos := nuevaSesionDePrueba(t)
	ninguno := nuevaSesionDePrueba(t)
	for s, chunks := range map[*Session][]world.ChunkCoord{
		soloNuevo: {chunkB},
		soloViejo: {chunkA},
		ambos:     {chunkA, chunkB},
		ninguno:   {{CX: 7, CY: 7}},
	} {
		hub.Register(s)
		hub.Subscribe(s, chunks)
	}

	transicion(hub, uuid.Nil)

	assert.Equal(t, []string{protocol.TypeEntitySpawn}, recibidos(soloNuevo), "gana la unidad")
	assert.Equal(t, []string{protocol.TypeEntityDespawn}, recibidos(soloViejo), "la pierde")
	assert.Empty(t, recibidos(ambos), "la sigue viendo por sus deltas: nada duplicado")
	assert.Empty(t, recibidos(ninguno))
}

// Con onlyPlayer, sólo las sesiones de ese jugador —todas sus pestañas— cuentan,
// a cada lado de la frontera (INV-SAFE-004).
func TestBroadcastChunkTransitionConOnlyPlayerSoloAlcanzaAEseJugador(t *testing.T) {
	hub := NewHub(nil, quietLogger())
	dueno := uuid.New()
	pestanaNueva := newSession(uuid.New(), dueno, nil, 16, time.Second, nil, quietLogger())
	pestanaVieja := newSession(uuid.New(), dueno, nil, 16, time.Second, nil, quietLogger())
	terceroNuevo := nuevaSesionDePrueba(t)
	terceroViejo := nuevaSesionDePrueba(t)
	for s, chunks := range map[*Session][]world.ChunkCoord{
		pestanaNueva: {chunkB},
		pestanaVieja: {chunkA},
		terceroNuevo: {chunkB},
		terceroViejo: {chunkA},
	} {
		hub.Register(s)
		hub.Subscribe(s, chunks)
	}

	transicion(hub, dueno)

	assert.Equal(t, []string{protocol.TypeEntitySpawn}, recibidos(pestanaNueva))
	assert.Equal(t, []string{protocol.TypeEntityDespawn}, recibidos(pestanaVieja))
	assert.Empty(t, recibidos(terceroNuevo), "un tercero no gana una unidad que no puede ver")
	assert.Empty(t, recibidos(terceroViejo), "ni pierde una que nunca tuvo")
}

func TestBroadcastChunkTransitionEntreElMismoChunkNoEmiteNada(t *testing.T) {
	hub := NewHub(nil, quietLogger())
	sesion := nuevaSesionDePrueba(t)
	hub.Register(sesion)
	hub.Subscribe(sesion, []world.ChunkCoord{chunkA})

	hub.BroadcastChunkTransition(chunkA, chunkA, uuid.Nil,
		protocol.TypeEntitySpawn, protocol.EntitySpawnPayload{},
		protocol.TypeEntityDespawn, protocol.EntityDespawnPayload{})

	assert.Empty(t, recibidos(sesion))
}

// Una sesión que acaba de irse del hub no recibe nada aunque siga mirando el
// chunk desde el punto de vista de la simulación.
func TestBroadcastChunkTransitionIgnoraSesionesRetiradas(t *testing.T) {
	hub := NewHub(nil, quietLogger())
	sesion := nuevaSesionDePrueba(t)
	hub.Register(sesion)
	hub.Subscribe(sesion, []world.ChunkCoord{chunkB})
	hub.Unregister(sesion)

	transicion(hub, uuid.Nil)

	assert.Empty(t, recibidos(sesion))
}
