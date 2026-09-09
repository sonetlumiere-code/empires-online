package websocket

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// nuevaSesionDePrueba crea una sesión sin conexión de red.
//
// `conn` sólo lo usa la goroutine de escritura, que aquí no se arranca: los
// mensajes se leen directamente de la cola de salida, que es exactamente lo que
// el hub decide llenar o no.
func nuevaSesionDePrueba(t *testing.T) *Session {
	t.Helper()
	return newSession(uuid.New(), uuid.New(), nil, 16, time.Second, nil, quietLogger())
}

// recibidos vacía la cola de salida de una sesión y devuelve los tipos emitidos.
func recibidos(s *Session) []string {
	var out []string
	for {
		select {
		case msg := <-s.out:
			out = append(out, msg.Type)
		default:
			return out
		}
	}
}

func TestBroadcastChunksEntregaUnaSolaVezAQuienEstaSuscritoAVarios(t *testing.T) {
	// Es la razón de existir de BroadcastChunks. Un territorio abarca varios
	// chunks; quien mire dos de ellos debe enterarse UNA vez del mismo hecho, no
	// dos con `seq` distinto y sin forma de saber que era el mismo.
	hub := NewHub(nil, quietLogger())

	sesion := nuevaSesionDePrueba(t)
	hub.Register(sesion)
	huella := []world.ChunkCoord{{CX: 0, CY: 0}, {CX: 1, CY: 0}, {CX: 0, CY: 1}}
	hub.Subscribe(sesion, huella)

	hub.BroadcastChunks(huella, protocol.TypeTerritoryUpdate, protocol.TerritoryUpdatePayload{})

	assert.Equal(t, []string{protocol.TypeTerritoryUpdate}, recibidos(sesion),
		"tres chunks de la misma huella, un solo mensaje")
}

func TestBroadcastChunkEnBucleSiDuplicaria(t *testing.T) {
	// Control negativo del test anterior: demuestra que el problema que
	// BroadcastChunks resuelve es real y no una precaución imaginaria. Si algún
	// día alguien "simplifica" sustituyéndolo por un bucle, este test explica
	// por qué no.
	hub := NewHub(nil, quietLogger())

	sesion := nuevaSesionDePrueba(t)
	hub.Register(sesion)
	huella := []world.ChunkCoord{{CX: 0, CY: 0}, {CX: 1, CY: 0}, {CX: 0, CY: 1}}
	hub.Subscribe(sesion, huella)

	for _, c := range huella {
		hub.BroadcastChunk(c.CX, c.CY, protocol.TypeTerritoryUpdate, protocol.TerritoryUpdatePayload{})
	}

	assert.Len(t, recibidos(sesion), 3, "el bucle entrega el mismo hecho una vez por chunk")
}

func TestBroadcastChunksNoAlcanzaASesionesNoSuscritas(t *testing.T) {
	// Criterio de aceptación 5 de M6: un jugador no suscrito a los chunks del
	// territorio no recibe su territory.update.
	hub := NewHub(nil, quietLogger())

	dentro := nuevaSesionDePrueba(t)
	hub.Register(dentro)
	hub.Subscribe(dentro, []world.ChunkCoord{{CX: 5, CY: 5}})

	fuera := nuevaSesionDePrueba(t)
	hub.Register(fuera)
	hub.Subscribe(fuera, []world.ChunkCoord{{CX: 40, CY: 40}})

	// Huella de un territorio que toca (5,5) pero no (40,40).
	hub.BroadcastChunks(
		[]world.ChunkCoord{{CX: 4, CY: 5}, {CX: 5, CY: 5}, {CX: 6, CY: 5}},
		protocol.TypeTerritoryUpdate, protocol.TerritoryUpdatePayload{})

	assert.Equal(t, []string{protocol.TypeTerritoryUpdate}, recibidos(dentro))
	assert.Empty(t, recibidos(fuera), "quien no mira ese territorio no debe enterarse")
}

func TestBroadcastChunksAlcanzaATodasLasSesionesSolapadas(t *testing.T) {
	hub := NewHub(nil, quietLogger())

	// Dos sesiones distintas, cada una suscrita a un chunk DISTINTO de la misma
	// huella: las dos deben recibirlo, una vez cada una.
	norte := nuevaSesionDePrueba(t)
	hub.Register(norte)
	hub.Subscribe(norte, []world.ChunkCoord{{CX: 1, CY: 0}})

	sur := nuevaSesionDePrueba(t)
	hub.Register(sur)
	hub.Subscribe(sur, []world.ChunkCoord{{CX: 1, CY: 3}})

	hub.BroadcastChunks(
		[]world.ChunkCoord{{CX: 1, CY: 0}, {CX: 1, CY: 1}, {CX: 1, CY: 2}, {CX: 1, CY: 3}},
		protocol.TypeTerritoryUpdate, protocol.TerritoryUpdatePayload{})

	assert.Len(t, recibidos(norte), 1)
	assert.Len(t, recibidos(sur), 1)
}

func TestBroadcastChunksConHuellaVaciaNoHaceNada(t *testing.T) {
	hub := NewHub(nil, quietLogger())
	sesion := nuevaSesionDePrueba(t)
	hub.Register(sesion)
	hub.Subscribe(sesion, []world.ChunkCoord{{CX: 0, CY: 0}})

	require.NotPanics(t, func() {
		hub.BroadcastChunks(nil, protocol.TypeTerritoryUpdate, protocol.TerritoryUpdatePayload{})
	})
	assert.Empty(t, recibidos(sesion))
}

func TestUnaSesionQueDejaDeMirarUnChunkYaNoRecibeSuTerritorio(t *testing.T) {
	// Subscribe REEMPLAZA el conjunto de chunks, no lo amplía. Si no retirara los
	// anteriores, un jugador seguiría recibiendo deltas de una zona que ya no
	// mira, y el interest management dejaría de acotar nada.
	hub := NewHub(nil, quietLogger())
	sesion := nuevaSesionDePrueba(t)
	hub.Register(sesion)

	hub.Subscribe(sesion, []world.ChunkCoord{{CX: 0, CY: 0}})
	hub.Subscribe(sesion, []world.ChunkCoord{{CX: 9, CY: 9}})

	hub.BroadcastChunks([]world.ChunkCoord{{CX: 0, CY: 0}},
		protocol.TypeTerritoryUpdate, protocol.TerritoryUpdatePayload{})

	assert.Empty(t, recibidos(sesion))
}
