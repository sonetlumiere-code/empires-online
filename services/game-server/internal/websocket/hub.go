// Package websocket implementa el transporte en tiempo real.
//
// Aquí NO hay lógica de juego. Este paquete traduce entre bytes del socket y
// comandos del dominio, y reparte los hechos que emite la simulación a las
// sesiones que deben verlos. Toda decisión sobre el mundo ocurre en el game loop.
package websocket

import (
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
)

type chunkKey uint64

func makeChunkKey(cx, cy int32) chunkKey {
	return chunkKey(uint64(uint32(cx))<<32 | uint64(uint32(cy)))
}

// Hub mantiene el registro de sesiones y sus suscripciones por chunk.
//
// El enrutado por chunk es el interest management: un mensaje sobre una unidad
// llega sólo a quien tiene ese trozo de mundo a la vista, no a los mil jugadores
// conectados. Sin esto, el coste de red crecería con el cuadrado de la población.
type Hub struct {
	mu       sync.RWMutex
	sessions map[uuid.UUID]*Session
	byPlayer map[uuid.UUID]map[uuid.UUID]*Session
	byChunk  map[chunkKey]map[uuid.UUID]*Session

	metrics *observability.Metrics
	log     *slog.Logger
}

// NewHub crea el hub.
func NewHub(metrics *observability.Metrics, log *slog.Logger) *Hub {
	return &Hub{
		sessions: make(map[uuid.UUID]*Session),
		byPlayer: make(map[uuid.UUID]map[uuid.UUID]*Session),
		byChunk:  make(map[chunkKey]map[uuid.UUID]*Session),
		metrics:  metrics,
		log:      log,
	}
}

// Register incorpora una sesión ya autenticada.
func (h *Hub) Register(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.sessions[s.ID] = s
	if h.byPlayer[s.PlayerID] == nil {
		h.byPlayer[s.PlayerID] = make(map[uuid.UUID]*Session)
	}
	h.byPlayer[s.PlayerID][s.ID] = s
	h.updateGauges()
}

// Unregister retira una sesión y todas sus suscripciones.
func (h *Hub) Unregister(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.sessions, s.ID)
	if set, ok := h.byPlayer[s.PlayerID]; ok {
		delete(set, s.ID)
		if len(set) == 0 {
			delete(h.byPlayer, s.PlayerID)
		}
	}
	for key := range s.chunks {
		if set, ok := h.byChunk[key]; ok {
			delete(set, s.ID)
			if len(set) == 0 {
				delete(h.byChunk, key)
			}
		}
	}
	s.chunks = nil
	h.updateGauges()
}

// Subscribe reemplaza el conjunto de chunks que observa una sesión.
//
// Devuelve qué chunks entraron y cuáles salieron, para que quien llama pueda
// emitir los spawn y despawn correspondientes: mover la cámara no debe obligar a
// reenviar el mundo entero.
func (h *Hub) Subscribe(s *Session, chunks []world.ChunkCoord) (entered, left []world.ChunkCoord) {
	h.mu.Lock()
	defer h.mu.Unlock()

	next := make(map[chunkKey]world.ChunkCoord, len(chunks))
	for _, c := range chunks {
		next[makeChunkKey(c.CX, c.CY)] = c
	}

	for key, coord := range next {
		if _, had := s.chunks[key]; !had {
			entered = append(entered, coord)
			if h.byChunk[key] == nil {
				h.byChunk[key] = make(map[uuid.UUID]*Session)
			}
			h.byChunk[key][s.ID] = s
		}
	}
	for key, coord := range s.chunks {
		if _, keeps := next[key]; !keeps {
			left = append(left, coord)
			if set, ok := h.byChunk[key]; ok {
				delete(set, s.ID)
				if len(set) == 0 {
					delete(h.byChunk, key)
				}
			}
		}
	}

	s.chunks = next
	return entered, left
}

// BroadcastChunk entrega un mensaje a todas las sesiones suscritas a un chunk.
func (h *Hub) BroadcastChunk(cx, cy int32, msgType string, payload any) {
	h.mu.RLock()
	set := h.byChunk[makeChunkKey(cx, cy)]
	targets := make([]*Session, 0, len(set))
	for _, s := range set {
		targets = append(targets, s)
	}
	h.mu.RUnlock()

	for _, s := range targets {
		s.Send(msgType, "", payload)
	}
}

// BroadcastChunks entrega el mensaje UNA sola vez a cada sesión suscrita a
// alguno de esos chunks.
//
// No es azúcar sobre BroadcastChunk en bucle: una entidad que abarca varios
// chunks —un territorio, por ejemplo— haría que quien esté suscrito a más de
// uno recibiera el mismo mensaje repetido, con `seq` distinto cada vez y sin
// forma de saber que era el mismo hecho. La deduplicación es por sesión, que es
// la unidad a la que el protocolo promete entregar.
func (h *Hub) BroadcastChunks(chunks []world.ChunkCoord, msgType string, payload any) {
	if len(chunks) == 0 {
		return
	}

	h.mu.RLock()
	targets := make(map[uuid.UUID]*Session)
	for _, c := range chunks {
		for id, s := range h.byChunk[makeChunkKey(c.CX, c.CY)] {
			targets[id] = s
		}
	}
	h.mu.RUnlock()

	for _, s := range targets {
		s.Send(msgType, "", payload)
	}
}

// SendToPlayer entrega un mensaje a todas las sesiones de un jugador.
func (h *Hub) SendToPlayer(playerID uuid.UUID, msgType, requestID string, payload any) {
	h.mu.RLock()
	set := h.byPlayer[playerID]
	targets := make([]*Session, 0, len(set))
	for _, s := range set {
		targets = append(targets, s)
	}
	h.mu.RUnlock()

	for _, s := range targets {
		s.Send(msgType, requestID, payload)
	}
}

// SessionCount es el número de conexiones abiertas.
func (h *Hub) SessionCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

// PlayerCount es el número de jugadores distintos conectados.
func (h *Hub) PlayerCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.byPlayer)
}

// CloseAll cierra todas las sesiones. Se usa en el apagado ordenado.
func (h *Hub) CloseAll(code int, reason string) {
	h.mu.RLock()
	targets := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		targets = append(targets, s)
	}
	h.mu.RUnlock()

	for _, s := range targets {
		s.CloseWith(code, reason)
	}
}

// updateGauges refresca las métricas de conexión. Debe llamarse con el lock tomado.
func (h *Hub) updateGauges() {
	if h.metrics == nil {
		return
	}
	h.metrics.ConnectedWebsockets.Set(float64(len(h.sessions)))
	h.metrics.ConnectedPlayers.Set(float64(len(h.byPlayer)))
}

// Comprobación del contrato con la simulación.
var _ simulation.Broadcaster = (*Hub)(nil)
