package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	gws "github.com/gorilla/websocket"

	"github.com/empires-online/empires-online/services/game-server/internal/auth"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
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

// Deduper deduplica comandos por requestId (INV-SEC-007, ADR-014).
type Deduper interface {
	// Claim reserva el requestId. Si ya estaba reservado devuelve false y lo
	// guardado: el marcador "pending" o la respuesta original.
	Claim(ctx context.Context, playerID uuid.UUID, requestID string) (bool, string, error)
	// Complete sustituye la reserva por la respuesta original.
	Complete(ctx context.Context, playerID uuid.UUID, requestID, response string) error
	// Release borra la reserva de un comando que no llegó a ejecutarse.
	Release(ctx context.Context, playerID uuid.UUID, requestID string) error
}

// pendingReply es el marcador que Claim escribe antes de ejecutar.
const pendingReply = "pending"

// storedReply es la respuesta original de un comando, tal como se guarda para
// reproducirla ante un duplicado (I-5 de docs/specs/websocket-protocol.md).
type storedReply struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// replyStoreTimeout acota la escritura de una respuesta en el almacén.
const replyStoreTimeout = 2 * time.Second

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

	// AllowedOrigins limita desde qué orígenes (`scheme://host[:port]`) puede un
	// navegador abrir una conexión; la comparación ignora mayúsculas. Vacío
	// significa «cualquiera», y la configuración sólo lo admite fuera de
	// producción (ADR-021). Una petición SIN cabecera Origin se acepta siempre:
	// no viene de un navegador, y el riesgo cross-site sólo existe en ellos.
	AllowedOrigins []string

	// MaxConnections y MaxConnectionsPerIP acotan los sockets abiertos, contados
	// desde antes del upgrade (ADR-021); 0 es «sin tope». Por encima, el upgrade
	// responde 503 (tope global) o 429 (tope de esa IP) y no abre socket.
	MaxConnections      int
	MaxConnectionsPerIP int
	// TrustProxyHeaders hace que la IP de una conexión se tome de las cabeceras
	// del proxy (X-Forwarded-For, y X-Real-IP a falta de ella) en lugar del
	// socket, con la misma lectura que apps/web: el elemento de X-Forwarded-For
	// que está TrustedProxyCount posiciones por la derecha. Sólo con un proxy
	// inverso delante; sin él, cualquiera elige su IP.
	TrustProxyHeaders bool
	TrustedProxyCount int

	// DispatchTimeout es cuánto espera una conexión recién autenticada a que su
	// PlayerConnected quepa en el canal de comandos del loop antes de darse por
	// no admitida y cerrarse (ADR-022). 0 es el valor por defecto, 2 s: con el
	// loop vivo, el canal entero se drena en menos de un segundo.
	DispatchTimeout time.Duration
}

// defaultDispatchTimeout es el DispatchTimeout cuando la configuración no lo fija.
const defaultDispatchTimeout = 2 * time.Second

// Server acepta conexiones WebSocket y las traduce a comandos del dominio.
type Server struct {
	cfg      Config
	hub      *Hub
	auth     *auth.Authenticator
	presence PresenceTracker
	dedupe   Deduper
	commands chan<- simulation.Command
	// inbox es el buzón del loop (loop.Inbox.Offer): la baja de una sesión va
	// por él cuando el canal de comandos está lleno, porque no puede perderse
	// (ADR-022). Opcional en tests.
	inbox    func(simulation.Command) bool
	metrics  *observability.Metrics
	log      *slog.Logger
	upgrader gws.Upgrader
	limits   *connLimiter
}

// NewServer construye el servidor.
func NewServer(
	cfg Config,
	hub *Hub,
	authenticator *auth.Authenticator,
	presence PresenceTracker,
	dedupe Deduper,
	commands chan<- simulation.Command,
	inbox func(simulation.Command) bool,
	metrics *observability.Metrics,
	log *slog.Logger,
) *Server {
	if cfg.DispatchTimeout <= 0 {
		cfg.DispatchTimeout = defaultDispatchTimeout
	}
	s := &Server{
		cfg: cfg, hub: hub, auth: authenticator, presence: presence,
		dedupe: dedupe, commands: commands, inbox: inbox, metrics: metrics, log: log,
		limits: newConnLimiter(cfg.MaxConnections, cfg.MaxConnectionsPerIP),
	}
	if dedupe != nil {
		hub.RecordReplies(s.recordReply)
	}
	s.upgrader = gws.Upgrader{
		HandshakeTimeout: cfg.HandshakeTimeout,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		CheckOrigin:      s.checkOrigin,
	}
	return s
}

// checkOrigin decide el upgrade según la cabecera Origin. Devolver false hace
// que gorilla responda 403 antes del handshake: no se abre ningún socket.
//
// Un navegador envía Origin en TODO upgrade y no deja al script cambiarla, así
// que es lo único que separa a la web del juego de cualquier otra página abierta
// en el mismo navegador (cross-site WebSocket hijacking). Un cliente que no es
// navegador —scripts/smoke.mjs, curl, otro servidor— no la envía, y tampoco
// tiene cookies ajenas que explotar: se admite sin más.
func (s *Server) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if len(s.cfg.AllowedOrigins) == 0 {
		return true // desarrollo y test: la configuración exige la lista en producción
	}
	for _, allowed := range s.cfg.AllowedOrigins {
		if strings.EqualFold(origin, allowed) {
			return true
		}
	}
	s.log.Warn("origen rechazado en el upgrade de WebSocket", "origin", origin, "remote", r.RemoteAddr)
	s.countRejected(rejectOrigin)
	return false
}

func (s *Server) countRejected(reason string) {
	if s.metrics != nil {
		s.metrics.WSConnectionsRejected.WithLabelValues(reason).Inc()
	}
}

// Handler devuelve el handler HTTP del endpoint /ws.
//
// `base` DEBE ser el contexto de vida del proceso, no el de la petición: el
// contexto de una petición HTTP se cancela en cuanto su handler retorna, y aquí
// el handler retorna de inmediato porque la sesión continúa en otra goroutine.
// Usar r.Context() mataría toda sesión justo después de su primer mensaje.
func (s *Server) Handler(base context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// La plaza se reserva ANTES del upgrade y se conserva hasta que la
		// goroutine de la conexión termina: un socket que nunca se autentica
		// también cuenta (ADR-021).
		ip := clientIP(r, s.cfg.TrustProxyHeaders, s.cfg.TrustedProxyCount)
		release, reason := s.limits.acquire(ip)
		if reason != "" {
			status := rejectStatus(reason)
			s.log.Warn("conexión rechazada por tope antes del upgrade",
				"reason", reason, "status", status, "ip", ip, "remote", r.RemoteAddr)
			s.countRejected(reason)
			w.Header().Set("Retry-After", retryAfterSeconds)
			http.Error(w, "demasiadas conexiones", status)
			return
		}

		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			// gorilla ya respondió: 403 por Origin, 400 por cabeceras.
			release()
			s.log.Debug("upgrade a WebSocket fallido", "err", err, "remote", r.RemoteAddr)
			return
		}
		go func() {
			defer release()
			s.serve(base, conn, r.RemoteAddr)
		}()
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
	if !s.dispatchWithin(simulation.PlayerConnected{PlayerID: session.PlayerID, SessionID: session.ID}, s.cfg.DispatchTimeout) {
		// Sin alta no hay sesión (ADR-022): una sesión que el loop no cuenta
		// dejaría la presencia de su ciudad a medias, y su baja de después
		// descontaría una sesión que nunca sumó. Se cierra sin baja, y el
		// cliente reconecta con su política de siempre.
		log.Error("el loop no admitió la sesión a tiempo; se cierra", "wait", s.cfg.DispatchTimeout)
		s.hub.Unregister(session)
		session.CloseWith(protocol.CloseInternalError, "el servidor está saturado")
		session.drainAndClose()
		if _, err := s.presence.ClearIfSession(context.WithoutCancel(ctx), session.PlayerID, session.ID); err != nil {
			log.Warn("no se pudo retirar la presencia caliente", "err", err)
		}
		return
	}

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
		// Un cierre ya pedido (4429 tras su RATE_LIMITED) manda sobre el normal.
		session.awaitPendingClose(s.cfg.WriteTimeout)
		session.CloseWith(gws.CloseNormalClosure, "sesión finalizada")
		session.drainAndClose()
		if _, err := s.presence.ClearIfSession(context.WithoutCancel(ctx), session.PlayerID, session.ID); err != nil {
			log.Warn("no se pudo retirar la presencia caliente", "err", err)
		}
		s.dispatchDisconnect(simulation.PlayerDisconnected{PlayerID: session.PlayerID, SessionID: session.ID})
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
		if errors.Is(err, gws.ErrReadLimit) {
			return auth.Claims{}, "", errMessageTooLarge
		}
		return auth.Claims{}, "", errHandshakeTimeout
	}

	var msg protocol.Inbound
	if err := protocol.DecodeStrict(data, &msg); err != nil {
		return auth.Claims{}, "", errInvalidMessage
	}
	if msg.V != protocol.Version {
		return auth.Claims{}, "", errUnsupportedVersion
	}
	if msg.Type != protocol.TypeSessionHello {
		return auth.Claims{}, "", errUnauthenticated
	}

	var payload protocol.SessionHelloPayload
	if err := protocol.DecodeStrict(msg.Payload, &payload); err != nil || payload.Ticket == "" {
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
			if errors.Is(err, gws.ErrReadLimit) {
				// gorilla ya cerró con 1009 sin leer el frame (INV-SEC-005).
				log.Info("mensaje por encima del límite de tamaño; se cierra la sesión",
					"limit_bytes", s.cfg.MaxMessageBytes)
				return
			}
			if gws.IsUnexpectedCloseError(err, gws.CloseNormalClosure, gws.CloseGoingAway) {
				log.Debug("lectura interrumpida", "err", err)
			}
			return
		}
		_ = session.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))

		if !limiter.allow(time.Now()) {
			log.Warn("límite de tasa superado; se cierra la sesión")
			session.SendError("", protocol.CodeRateLimited, "demasiados mensajes")
			session.CloseAfterFlush(protocol.CloseRateLimited, "rate limited")
			return
		}

		var msg protocol.Inbound
		if err := protocol.DecodeStrict(data, &msg); err != nil {
			session.SendError("", protocol.CodeInvalidMessage, "mensaje malformado")
			continue
		}
		if s.metrics != nil {
			s.metrics.WSMessages.WithLabelValues(observability.DirectionInbound, inboundTypeLabel(msg.Type)).Inc()
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

// inboundTypeLabel acota la etiqueta `type` de eo_ws_messages_total al catálogo
// cerrado del protocolo: cualquier otro tipo cuenta como `unknown`. El tipo lo
// elige el cliente, y cada valor distinto de una etiqueta es una serie nueva
// que vive en memoria hasta que el proceso termina (EO-121).
func inboundTypeLabel(msgType string) string {
	if protocol.ClientMessageTypes[msgType] {
		return msgType
	}
	return observability.TypeUnknown
}

func (s *Server) handle(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	switch msg.Type {
	case protocol.TypeSessionPing:
		var p protocol.SessionPingPayload
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
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
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		center := world.Tile{X: p.Center.X, Y: p.Center.Y}
		s.sendSnapshot(session, &center, msg.RequestID)

	case protocol.TypeUnitMove:
		var p protocol.UnitMovePayload
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		if !s.claimRequest(ctx, session, msg.RequestID) {
			return
		}
		s.dispatchClaimed(ctx, session, msg.RequestID, simulation.MoveUnit{
			PlayerID:  session.PlayerID,
			SessionID: session.ID,
			RequestID: msg.RequestID,
			UnitID:    p.UnitID,
			Target:    world.Tile{X: p.Target.X, Y: p.Target.Y},
		})

	case protocol.TypeUnitCancelMove:
		var p protocol.UnitCancelMovePayload
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		if !s.claimRequest(ctx, session, msg.RequestID) {
			return
		}
		s.dispatchClaimed(ctx, session, msg.RequestID, simulation.CancelMovement{
			PlayerID:  session.PlayerID,
			SessionID: session.ID,
			RequestID: msg.RequestID,
			UnitID:    p.UnitID,
		})

	case protocol.TypeUnitGarrison:
		var p protocol.UnitGarrisonPayload
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		if !s.claimRequest(ctx, session, msg.RequestID) {
			return
		}
		s.dispatchClaimed(ctx, session, msg.RequestID, simulation.GarrisonUnit{
			PlayerID:  session.PlayerID,
			SessionID: session.ID,
			RequestID: msg.RequestID,
			UnitID:    p.UnitID,
			CityID:    p.CityID,
		})

	case protocol.TypeUnitUngarrison:
		var p protocol.UnitUngarrisonPayload
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		if !s.claimRequest(ctx, session, msg.RequestID) {
			return
		}
		s.dispatchClaimed(ctx, session, msg.RequestID, simulation.UngarrisonUnit{
			PlayerID:  session.PlayerID,
			SessionID: session.ID,
			RequestID: msg.RequestID,
			UnitID:    p.UnitID,
		})

	// Economía (SPEC-ECON-WOOD). Los tres comandos llevan sólo la intención:
	// el bosque, el tipo y el tile. Cantidades, costes y tiempos son del
	// servidor (INV-SEC-001); los rechazos llegan por system.error.
	case protocol.TypeUnitGather:
		var p protocol.UnitGatherPayload
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		if !s.claimRequest(ctx, session, msg.RequestID) {
			return
		}
		s.dispatchClaimed(ctx, session, msg.RequestID, simulation.GatherWood{
			PlayerID:  session.PlayerID,
			SessionID: session.ID,
			RequestID: msg.RequestID,
			UnitID:    p.UnitID,
			Target:    world.Tile{X: p.Target.X, Y: p.Target.Y},
		})

	case protocol.TypeCityBuild:
		var p protocol.CityBuildPayload
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		if !s.claimRequest(ctx, session, msg.RequestID) {
			return
		}
		s.dispatchClaimed(ctx, session, msg.RequestID, simulation.BuildBuilding{
			PlayerID:  session.PlayerID,
			SessionID: session.ID,
			RequestID: msg.RequestID,
			CityID:    p.CityID,
			Type:      building.Type(p.Type),
			Tile:      world.Tile{X: p.X, Y: p.Y},
		})

	case protocol.TypeCityTrain:
		var p protocol.CityTrainPayload
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		if !s.claimRequest(ctx, session, msg.RequestID) {
			return
		}
		s.dispatchClaimed(ctx, session, msg.RequestID, simulation.TrainUnit{
			PlayerID:  session.PlayerID,
			SessionID: session.ID,
			RequestID: msg.RequestID,
			CityID:    p.CityID,
			Type:      unit.Type(p.Type),
		})
	}
}

// dispatchClaimed encola un comando cuyo requestId ya se reservó. Si la cola del
// loop está llena, el comando no se ejecutó: se dice al cliente y se libera la
// reserva, o el reintento con el mismo requestId se descartaría como duplicado
// de algo que nunca ocurrió (I-10).
func (s *Server) dispatchClaimed(ctx context.Context, session *Session, requestID string, cmd simulation.Command) {
	if s.dispatch(cmd) {
		return
	}
	if s.dedupe != nil {
		if err := s.dedupe.Release(context.WithoutCancel(ctx), session.PlayerID, requestID); err != nil {
			s.log.Warn("no se pudo liberar la reserva de un comando descartado",
				observability.FieldRequestID, requestID, "err", err)
		}
	}
	session.SendError(requestID, protocol.CodeInternalError, "el servidor está saturado; reintenta")
}

// recordReply guarda la respuesta directa a un comando para reproducirla ante
// un duplicado (I-5). La llama el Hub desde el game loop, así que la escritura
// en el almacén va en su propia goroutine: el tick nunca espera a Redis. Un
// duplicado que llegue antes de que termine verá todavía "pending" y no
// recibirá nada, igual que si el original siguiera en curso.
func (s *Server) recordReply(playerID uuid.UUID, requestID, msgType string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("no se pudo serializar una respuesta para la idempotencia", "type", msgType, "err", err)
		return
	}
	stored, err := json.Marshal(storedReply{Type: msgType, Payload: raw})
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), replyStoreTimeout)
		defer cancel()
		if err := s.dedupe.Complete(ctx, playerID, requestID, string(stored)); err != nil {
			s.log.Debug("no se pudo guardar la respuesta de un comando",
				observability.FieldRequestID, requestID, "err", err)
		}
	}()
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
	fresh, prev, err := s.dedupe.Claim(ctx, session.PlayerID, requestID)
	if err != nil {
		// Si Redis no responde no se bloquea el juego: se prefiere ejecutar el
		// comando a dejar al jugador sin poder jugar. La decisión está documentada
		// en docs/architecture/networking.md.
		s.log.Warn("no se pudo verificar la idempotencia; se ejecuta igualmente", "err", err)
		return true
	}
	if fresh {
		return true
	}

	// Duplicado: nunca se reejecuta. Si el original ya tuvo respuesta, se
	// reproduce tal cual (I-5); si sigue en curso, no se dice nada.
	var reply storedReply
	if prev != pendingReply && json.Unmarshal([]byte(prev), &reply) == nil && reply.Type != "" {
		session.Send(reply.Type, requestID, reply.Payload)
		s.log.Debug("comando duplicado; se reproduce la respuesta original",
			observability.FieldRequestID, requestID,
			observability.FieldPlayerID, session.PlayerID, "type", reply.Type)
		return false
	}
	s.log.Debug("comando duplicado ignorado",
		observability.FieldRequestID, requestID,
		observability.FieldPlayerID, session.PlayerID)
	return false
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
		s.countDropped(cmd)
		return false
	}
}

// dispatchWithin encola un comando esperando, como mucho, `wait` a que haya
// sitio en el canal. Sólo para el alta de la propia conexión, en su goroutine,
// que todavía no tiene nada mejor que hacer: nunca desde el writePump, desde
// el hub ni desde el loop. El alta va por el canal de comandos y no por el
// buzón porque debe aplicarse ANTES que el snapshot inicial de la sesión
// (INV-CITY-014), que viaja por ese canal: compartir la cola es lo que conserva
// el orden.
func (s *Server) dispatchWithin(cmd simulation.Command, wait time.Duration) bool {
	select {
	case s.commands <- cmd:
		return true
	default:
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case s.commands <- cmd:
		return true
	case <-timer.C:
		s.countDropped(cmd)
		return false
	}
}

// dispatchDisconnect encola la baja de una sesión. No puede perderse: sin ella
// el loop contaría la sesión para siempre y la ciudad no saldría nunca de
// ONLINE, y el latido de presencia no lo cubre porque el loop decide en RAM sin
// consultar Redis (RN-PRESENCE-004). Si el canal de comandos está lleno va al
// buzón del loop, que se drena con prioridad; la simulación tolera que la baja
// adelante al alta (ADR-022). Sólo con los dos llenos se cuenta como descartada.
func (s *Server) dispatchDisconnect(cmd simulation.PlayerDisconnected) {
	select {
	case s.commands <- cmd:
		return
	default:
	}
	if s.inbox != nil {
		s.inbox(cmd) // si tampoco cabe, el buzón lo cuenta y lo registra
		return
	}
	s.countDropped(cmd)
}

// countDropped registra y cuenta un comando que no se aplicará nunca
// (eo_commands_dropped_total, ADR-022). Nunca es normal: el loop va tan por
// detrás que ni su canal de comandos tiene sitio.
func (s *Server) countDropped(cmd simulation.Command) {
	tipo := simulation.CommandType(cmd)
	s.log.Error("cola de comandos saturada; comando descartado", "type", tipo)
	if s.metrics != nil {
		s.metrics.CommandsDropped.WithLabelValues(tipo).Inc()
	}
}

// Errores del handshake. Todos los que impliquen credenciales se traducen al
// MISMO código de cierre: distinguirlos ayudaría a afinar un ataque.
var (
	errHandshakeTimeout   = errors.New("handshake no completado a tiempo")
	errMessageTooLarge    = errors.New("mensaje por encima del límite de tamaño")
	errInvalidMessage     = errors.New("mensaje de handshake inválido")
	errUnsupportedVersion = errors.New("versión de protocolo no soportada")
	errUnauthenticated    = errors.New("autenticación fallida")
)

func handshakeCloseCode(err error) (int, string) {
	switch {
	case errors.Is(err, errHandshakeTimeout):
		return protocol.CloseHandshakeTimeout, "handshake timeout"
	case errors.Is(err, errMessageTooLarge):
		// gorilla ya envió este mismo cierre al cortar la lectura; repetirlo
		// falla sin consecuencias, y el log dice la causa verdadera.
		return gws.CloseMessageTooBig, "message too big"
	case errors.Is(err, errInvalidMessage):
		return protocol.CloseInvalidMessage, "invalid message"
	case errors.Is(err, errUnsupportedVersion):
		return protocol.CloseInvalidMessage, "unsupported version"
	default:
		return protocol.CloseUnauthenticated, "unauthorized"
	}
}
