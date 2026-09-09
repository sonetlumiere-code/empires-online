package websocket

import (
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	gws "github.com/gorilla/websocket"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Session es una conexión WebSocket autenticada.
//
// Concurrencia: gorilla/websocket admite UN solo escritor. Por eso todo lo que
// sale pasa por el canal `out` y lo escribe exclusivamente la goroutine de
// escritura. Cualquier otra goroutine que quiera enviar algo usa Send.
type Session struct {
	ID       uuid.UUID
	PlayerID uuid.UUID

	conn *gws.Conn
	out  chan protocol.Outbound
	log  *slog.Logger

	seq      atomic.Uint64
	closed   atomic.Bool
	closeOne sync.Once
	done     chan struct{}

	// chunks son las suscripciones de esta sesión. Sólo el Hub las toca, siempre
	// con su propio lock tomado.
	chunks map[chunkKey]world.ChunkCoord

	// terrainSent recuerda qué chunks ya recibieron su terreno: es inmutable, así
	// que reenviarlo sería malgastar ancho de banda en cada movimiento de cámara.
	terrainMu   sync.Mutex
	terrainSent map[chunkKey]struct{}

	metrics      *observability.Metrics
	writeTimeout time.Duration
}

func newSession(id, playerID uuid.UUID, conn *gws.Conn, queueSize int, writeTimeout time.Duration,
	metrics *observability.Metrics, log *slog.Logger) *Session {
	return &Session{
		ID:           id,
		PlayerID:     playerID,
		conn:         conn,
		out:          make(chan protocol.Outbound, queueSize),
		done:         make(chan struct{}),
		chunks:       make(map[chunkKey]world.ChunkCoord),
		terrainSent:  make(map[chunkKey]struct{}),
		metrics:      metrics,
		writeTimeout: writeTimeout,
		log:          log,
	}
}

// Send encola un mensaje saliente.
//
// NUNCA bloquea. Si la cola de salida está llena, el cliente no consume al ritmo
// al que el mundo genera hechos: se cierra la conexión y que reconecte con un
// snapshot limpio. Bloquear aquí propagaría la lentitud de un solo cliente al
// game loop entero.
func (s *Session) Send(msgType, requestID string, payload any) {
	if s.closed.Load() {
		return
	}
	msg := protocol.NewOutbound(msgType, s.seq.Add(1), time.Now().UnixMilli(), requestID, payload)

	select {
	case s.out <- msg:
		if s.metrics != nil {
			s.metrics.WSMessages.WithLabelValues(observability.DirectionOutbound, msgType).Inc()
		}
	default:
		s.log.Warn("cola de salida saturada; se cierra la sesión",
			observability.FieldSessionID, s.ID, observability.FieldPlayerID, s.PlayerID)
		s.CloseWith(protocol.CloseInternalError, "cola de salida saturada")
	}
}

// SendError es un atajo para emitir un error de protocolo.
func (s *Session) SendError(requestID, code, message string) {
	if s.metrics != nil {
		s.metrics.ProtocolErrors.WithLabelValues(code).Inc()
	}
	s.Send(protocol.TypeSystemError, requestID, protocol.SystemErrorPayload{Code: code, Message: message})
}

// NeedsTerrain indica si hay que incluir el terreno de un chunk para esta sesión.
func (s *Session) NeedsTerrain(cx, cy int32) bool {
	s.terrainMu.Lock()
	defer s.terrainMu.Unlock()
	_, sent := s.terrainSent[makeChunkKey(cx, cy)]
	return !sent
}

// MarkTerrainSent registra que esta sesión ya tiene el terreno de esos chunks.
func (s *Session) MarkTerrainSent(chunks []world.ChunkCoord) {
	s.terrainMu.Lock()
	defer s.terrainMu.Unlock()
	for _, c := range chunks {
		s.terrainSent[makeChunkKey(c.CX, c.CY)] = struct{}{}
	}
}

// CloseWith cierra la conexión con un código de aplicación.
func (s *Session) CloseWith(code int, reason string) {
	s.closeOne.Do(func() {
		s.closed.Store(true)
		deadline := time.Now().Add(2 * time.Second)
		_ = s.conn.WriteControl(gws.CloseMessage,
			gws.FormatCloseMessage(code, reason), deadline)
		_ = s.conn.Close()
		close(s.done)
	})
}

// Done se cierra cuando la sesión termina.
func (s *Session) Done() <-chan struct{} { return s.done }

// writePump es la ÚNICA goroutine que escribe en el socket.
func (s *Session) writePump(pingInterval time.Duration) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return

		case msg := <-s.out:
			_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
			data, err := json.Marshal(msg)
			if err != nil {
				s.log.Error("no se pudo serializar un mensaje saliente",
					"type", msg.Type, "err", err)
				continue
			}
			if err := s.conn.WriteMessage(gws.TextMessage, data); err != nil {
				s.log.Debug("escritura fallida; se cierra la sesión",
					observability.FieldSessionID, s.ID, "err", err)
				s.CloseWith(protocol.CloseInternalError, "error de escritura")
				return
			}

		case <-ticker.C:
			_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
			if err := s.conn.WriteMessage(gws.PingMessage, nil); err != nil {
				s.CloseWith(protocol.CloseInternalError, "ping fallido")
				return
			}
		}
	}
}

// rateLimiter es un cubo de fichas por conexión.
//
// Se rellena con el tiempo, sin temporizadores: cada consulta calcula cuántas
// fichas se han acumulado desde la anterior. Un cliente que dispara mil mensajes
// por segundo agota su cubo y se le cierra la conexión.
type rateLimiter struct {
	tokens     float64
	maxTokens  float64
	refillRate float64 // fichas por segundo
	last       time.Time
}

func newRateLimiter(perSecond, burst int) *rateLimiter {
	return &rateLimiter{
		tokens:     float64(burst),
		maxTokens:  float64(burst),
		refillRate: float64(perSecond),
		last:       time.Now(),
	}
}

// allow consume una ficha si hay disponible.
func (r *rateLimiter) allow(now time.Time) bool {
	elapsed := now.Sub(r.last).Seconds()
	r.last = now
	r.tokens += elapsed * r.refillRate
	if r.tokens > r.maxTokens {
		r.tokens = r.maxTokens
	}
	if r.tokens < 1 {
		return false
	}
	r.tokens--
	return true
}
