package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	gws "github.com/gorilla/websocket"

	"github.com/empires-online/empires-online/services/game-server/internal/auth"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// PresenceTracker registra la presencia caliente de un jugador.
type PresenceTracker interface {
	MarkOnline(ctx context.Context, playerID, sessionID uuid.UUID) error
	Heartbeat(ctx context.Context, playerID uuid.UUID) (bool, error)
	ClearIfSession(ctx context.Context, playerID, sessionID uuid.UUID) (bool, error)
}

// Deduper deduplica comandos por requestId.
type Deduper interface {
	Claim(ctx context.Context, playerID uuid.UUID, requestID string) (bool, string, error)
}

// Config parametriza el servidor de WebSocket.
type Config struct {
	MaxMessageBytes  int64
	RateLimitPerSec  int
	RateLimitBurst   int
	HandshakeTimeout time.Duration
	PingInterval     time.Duration
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	OutboundQueue    int

	InterestRadiusChunks int
	TickDurationMs       int64
	HeartbeatIntervalMs  int64

	WorldWidth  int32
	WorldHeight int32
	ChunkSize   int32

	// AllowedOrigins limita qué orígenes pueden abrir una conexión. Vacío en
	// desarrollo significa "cualquiera"; en producción debe estar poblado.
	AllowedOrigins []string
}

// Server acepta conexiones WebSocket y las traduce a comandos del dominio.
type Server struct {
	cfg      Config
	hub      *Hub
	auth     *auth.Authenticator
	presence PresenceTracker
	dedupe   Deduper
	commands chan<- simulation.Command
	metrics  *observability.Metrics
	log      *slog.Logger
	upgrader gws.Upgrader
}

// NewServer construye el servidor.
func NewServer(
	cfg Config,
	hub *Hub,
	authenticator *auth.Authenticator,
	presence PresenceTracker,
	dedupe Deduper,
	commands chan<- simulation.Command,
	metrics *observability.Metrics,
	log *slog.Logger,
) *Server {
	s := &Server{
		cfg: cfg, hub: hub, auth: authenticator, presence: presence,
		dedupe: dedupe, commands: commands, metrics: metrics, log: log,
	}
	s.upgrader = gws.Upgrader{
		HandshakeTimeout: cfg.HandshakeTimeout,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		CheckOrigin:      s.checkOrigin,
	}
	return s
}

func (s *Server) checkOrigin(r *http.Request) bool {
	if len(s.cfg.AllowedOrigins) == 0 {
		return true // desarrollo
	}
	origin := r.Header.Get("Origin")
	for _, allowed := range s.cfg.AllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	s.log.Warn("origen rechazado en el upgrade de WebSocket", "origin", origin)
	return false
}

// Handler devuelve el handler HTTP del endpoint /ws.
//
// `base` DEBE ser el contexto de vida del proceso, no el de la petición: el
// contexto de una petición HTTP se cancela en cuanto su handler retorna, y aquí
// el handler retorna de inmediato porque la sesión continúa en otra goroutine.
// Usar r.Context() mataría toda sesión justo después de su primer mensaje.
func (s *Server) Handler(base context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			s.log.Debug("upgrade a WebSocket fallido", "err", err, "remote", r.RemoteAddr)
			return
		}
		go s.serve(base, conn, r.RemoteAddr)
	}
}

func (s *Server) serve(ctx context.Context, conn *gws.Conn, remote string) {
	conn.SetReadLimit(s.cfg.MaxMessageBytes)

	claims, helloReqID, err := s.handshake(ctx, conn)
	if err != nil {
		code, reason := handshakeCloseCode(err)
		s.log.Info("handshake rechazado", "err", err, "remote", remote, "close_code", code)
		_ = conn.WriteControl(gws.CloseMessage, gws.FormatCloseMessage(code, reason),
			time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}

	session := newSession(uuid.New(), claims.PlayerID, conn,
		s.cfg.OutboundQueue, s.cfg.WriteTimeout, s.metrics, s.log)

	log := s.log.With(
		observability.FieldSessionID, session.ID,
		observability.FieldPlayerID, session.PlayerID)

	s.hub.Register(session)
	go session.writePump(s.cfg.PingInterval)

	if err := s.presence.MarkOnline(ctx, session.PlayerID, session.ID); err != nil {
		// La presencia caliente es una optimización, no la verdad: si Redis falla,
		// la sesión sigue siendo válida y el estado durable no se ve afectado.
		log.Warn("no se pudo registrar la presencia caliente", "err", err)
	}
	s.dispatch(simulation.PlayerConnected{PlayerID: session.PlayerID, SessionID: session.ID})

	session.Send(protocol.TypeSessionWelcome, helloReqID, protocol.SessionWelcomePayload{
		SessionID:           session.ID.String(),
		PlayerID:            session.PlayerID.String(),
		ServerTimeMs:        time.Now().UnixMilli(),
		TickDurationMs:      s.cfg.TickDurationMs,
		HeartbeatIntervalMs: s.cfg.HeartbeatIntervalMs,
		World: protocol.WorldInfo{
			Width:     s.cfg.WorldWidth,
			Height:    s.cfg.WorldHeight,
			ChunkSize: s.cfg.ChunkSize,
		},
	})

	// Snapshot inicial centrado en la ciudad del jugador.
	s.sendSnapshot(session, nil, "")

	log.Info("sesión establecida", "remote", remote)

	defer func() {
		s.hub.Unregister(session)
		session.CloseWith(gws.CloseNormalClosure, "sesión finalizada")
		if _, err := s.presence.ClearIfSession(context.WithoutCancel(ctx), session.PlayerID, session.ID); err != nil {
			log.Warn("no se pudo retirar la presencia caliente", "err", err)
		}
		s.dispatch(simulation.PlayerDisconnected{PlayerID: session.PlayerID, SessionID: session.ID})
		log.Info("sesión finalizada")
	}()

	s.readPump(ctx, session, log)
}

// handshake exige que el PRIMER mensaje sea un session.hello válido.
//
// Nada más se acepta antes de conocer la identidad: un socket sin autenticar no
// puede ejecutar ni consultar nada (INV-SEC-002).
func (s *Server) handshake(ctx context.Context, conn *gws.Conn) (auth.Claims, string, error) {
	_ = conn.SetReadDeadline(time.Now().Add(s.cfg.HandshakeTimeout))

	_, data, err := conn.ReadMessage()
	if err != nil {
		return auth.Claims{}, "", errHandshakeTimeout
	}

	var msg protocol.Inbound
	if err := json.Unmarshal(data, &msg); err != nil {
		return auth.Claims{}, "", errInvalidMessage
	}
	if msg.V != protocol.Version {
		return auth.Claims{}, "", errUnsupportedVersion
	}
	if msg.Type != protocol.TypeSessionHello {
		return auth.Claims{}, "", errUnauthenticated
	}

	var payload protocol.SessionHelloPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload.Ticket == "" {
		return auth.Claims{}, "", errInvalidMessage
	}

	claims, err := s.auth.Authenticate(ctx, payload.Ticket)
	if err != nil {
		return auth.Claims{}, "", errUnauthenticated
	}
	return claims, msg.RequestID, nil
}

// readPump procesa los mensajes entrantes de una sesión ya autenticada.
func (s *Server) readPump(ctx context.Context, session *Session, log *slog.Logger) {
	limiter := newRateLimiter(s.cfg.RateLimitPerSec, s.cfg.RateLimitBurst)

	_ = session.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
	session.conn.SetPongHandler(func(string) error {
		return session.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
	})

	for {
		_, data, err := session.conn.ReadMessage()
		if err != nil {
			if gws.IsUnexpectedCloseError(err, gws.CloseNormalClosure, gws.CloseGoingAway) {
				log.Debug("lectura interrumpida", "err", err)
			}
			return
		}
		_ = session.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))

		if !limiter.allow(time.Now()) {
			log.Warn("límite de tasa superado; se cierra la sesión")
			session.SendError("", protocol.CodeRateLimited, "demasiados mensajes")
			session.CloseWith(protocol.CloseRateLimited, "rate limited")
			return
		}

		var msg protocol.Inbound
		if err := json.Unmarshal(data, &msg); err != nil {
			session.SendError("", protocol.CodeInvalidMessage, "mensaje malformado")
			continue
		}
		if s.metrics != nil {
			s.metrics.WSMessages.WithLabelValues(observability.DirectionInbound, msg.Type).Inc()
		}
		if msg.V != protocol.Version {
			session.SendError(msg.RequestID, protocol.CodeUnsupportedVersion,
				"versión de protocolo no soportada")
			continue
		}
		if !protocol.ClientMessageTypes[msg.Type] {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "tipo de mensaje desconocido")
			continue
		}

		s.handle(ctx, session, msg, log)

		select {
		case <-ctx.Done():
			return
		case <-session.Done():
			return
		default:
		}
	}
}

func (s *Server) handle(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	switch msg.Type {
	case protocol.TypeSessionPing:
		var p protocol.SessionPingPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		if _, err := s.presence.Heartbeat(ctx, session.PlayerID); err != nil {
			log.Debug("latido de presencia fallido", "err", err)
		}
		session.Send(protocol.TypeSessionPong, msg.RequestID, protocol.SessionPongPayload{
			ClientTimeMs: p.ClientTimeMs,
			ServerTimeMs: time.Now().UnixMilli(),
		})

	case protocol.TypeSessionView:
		var p protocol.SessionViewPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		center := world.Tile{X: p.Center.X, Y: p.Center.Y}
		s.sendSnapshot(session, &center, msg.RequestID)

	case protocol.TypeUnitMove:
		var p protocol.UnitMovePayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		if !s.claimRequest(ctx, session, msg.RequestID) {
			return
		}
		s.dispatch(simulation.MoveUnit{
			PlayerID:  session.PlayerID,
			SessionID: session.ID,
			RequestID: msg.RequestID,
			UnitID:    p.UnitID,
			Target:    world.Tile{X: p.Target.X, Y: p.Target.Y},
		})

	case protocol.TypeUnitCancelMove:
		var p protocol.UnitCancelMovePayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		if !s.claimRequest(ctx, session, msg.RequestID) {
			return
		}
		s.dispatch(simulation.CancelMovement{
			PlayerID:  session.PlayerID,
			SessionID: session.ID,
			RequestID: msg.RequestID,
			UnitID:    p.UnitID,
		})
	}
}

// claimRequest aplica la idempotencia por requestId.
//
// Un cliente que reintenta tras un corte de red reenvía el mismo requestId. Sin
// esto, ese reintento produciría un SEGUNDO movimiento (INV-SEC-007).
func (s *Server) claimRequest(ctx context.Context, session *Session, requestID string) bool {
	if requestID == "" {
		session.SendError("", protocol.CodeInvalidMessage, "requestId obligatorio")
		return false
	}
	if s.dedupe == nil {
		return true
	}
	fresh, _, err := s.dedupe.Claim(ctx, session.PlayerID, requestID)
	if err != nil {
		// Si Redis no responde no se bloquea el juego: se prefiere ejecutar el
		// comando a dejar al jugador sin poder jugar. La decisión está documentada
		// en docs/architecture/networking.md.
		s.log.Warn("no se pudo verificar la idempotencia; se ejecuta igualmente", "err", err)
		return true
	}
	if !fresh {
		s.log.Debug("comando duplicado ignorado",
			observability.FieldRequestID, requestID,
			observability.FieldPlayerID, session.PlayerID)
		return false
	}
	return true
}

// sendSnapshot pide el estado del área de interés al game loop y lo envía.
func (s *Server) sendSnapshot(session *Session, center *world.Tile, requestID string) {
	reply := make(chan simulation.SnapshotResult, 1)

	cmd := simulation.RequestSnapshot{
		PlayerID:       session.PlayerID,
		SessionID:      session.ID,
		Center:         center,
		IncludeTerrain: true,
		RadiusChunks:   int32(s.cfg.InterestRadiusChunks),
		Reply:          reply,
	}
	if !s.dispatch(cmd) {
		session.SendError(requestID, protocol.CodeInternalError, "el servidor está saturado")
		return
	}

	select {
	case result := <-reply:
		entered, left := s.hub.Subscribe(session, result.Chunks)

		// El terreno es inmutable: sólo se envía el de los chunks que esta sesión
		// aún no tiene.
		filtered := result.Payload.Terrain[:0]
		for _, t := range result.Payload.Terrain {
			if session.NeedsTerrain(t.CX, t.CY) {
				filtered = append(filtered, t)
			}
		}
		result.Payload.Terrain = filtered
		session.MarkTerrainSent(result.Chunks)

		session.Send(protocol.TypeWorldSnapshot, requestID, result.Payload)
		s.log.Debug("snapshot enviado",
			observability.FieldSessionID, session.ID,
			"chunks", len(result.Chunks), "entered", len(entered), "left", len(left),
			"units", len(result.Payload.Units))

	case <-time.After(2 * time.Second):
		session.SendError(requestID, protocol.CodeInternalError, "el mundo no respondió a tiempo")
	}
}

// dispatch encola un comando sin bloquear.
//
// Si el game loop va tan por detrás que su cola está llena, es preferible
// rechazar el comando —y decírselo al jugador— que bloquear la goroutine de la
// conexión y arrastrar todo el servidor.
func (s *Server) dispatch(cmd simulation.Command) bool {
	select {
	case s.commands <- cmd:
		return true
	default:
		s.log.Error("cola de comandos saturada; comando descartado")
		return false
	}
}

// Errores del handshake. Todos los que impliquen credenciales se traducen al
// MISMO código de cierre: distinguirlos ayudaría a afinar un ataque.
var (
	errHandshakeTimeout   = errors.New("handshake no completado a tiempo")
	errInvalidMessage     = errors.New("mensaje de handshake inválido")
	errUnsupportedVersion = errors.New("versión de protocolo no soportada")
	errUnauthenticated    = errors.New("autenticación fallida")
)

func handshakeCloseCode(err error) (int, string) {
	switch {
	case errors.Is(err, errHandshakeTimeout):
		return protocol.CloseHandshakeTimeout, "handshake timeout"
	case errors.Is(err, errInvalidMessage):
		return protocol.CloseInvalidMessage, "invalid message"
	case errors.Is(err, errUnsupportedVersion):
		return protocol.CloseInvalidMessage, "unsupported version"
	default:
		return protocol.CloseUnauthenticated, "unauthorized"
	}
}
