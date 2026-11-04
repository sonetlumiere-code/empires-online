package websocket

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// EventLog es el registro de los jugadores (SPEC-EVENT-LOG, ADR-028 §1): lo lee y
// lo marca la conexión, sin pasar por el loop. postgres.EventLogRepo lo implementa.
type EventLog interface {
	// Page devuelve una página del registro de playerID y su tope marcable.
	Page(ctx context.Context, playerID uuid.UUID, beforeID int64) (protocol.EventLogPagePayload, int64, error)
	// MarkSeen avanza el cursor de lo visto sin retroceder nunca.
	MarkSeen(ctx context.Context, playerID uuid.UUID, upToID int64) error
}

// eventLogTimeout acota cada consulta del registro: si la base no responde, el
// comando falla y la sesión sigue (RN-LOG-006).
const eventLogTimeout = 3 * time.Second

// SetEventLog conecta el registro. Sin él, los dos comandos responden
// NOT_IMPLEMENTED; los tests que no lo usan no lo montan.
func (s *Server) SetEventLog(l EventLog) { s.eventLog = l }

// handleEventLogFetch sirve una página del registro del jugador de la sesión,
// nunca de otro: el comando no tiene ningún campo que lo nombre (INV-SEC-009), y
// guarda en la sesión su tope marcable (INV-PERSIST-006).
func (s *Server) handleEventLogFetch(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.EventLogFetchPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil || (p.BeforeID != nil && *p.BeforeID < 1) {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	if s.eventLog == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "el registro no está disponible")
		return
	}
	var before int64
	if p.BeforeID != nil {
		before = *p.BeforeID
	}
	qctx, cancel := context.WithTimeout(ctx, eventLogTimeout)
	defer cancel()
	page, markable, err := s.eventLog.Page(qctx, session.PlayerID, before)
	if err != nil {
		log.Warn("no se pudo leer el registro", "err", err)
		session.SendError(msg.RequestID, protocol.CodeInternalError, "no se pudo leer el registro")
		return
	}
	session.eventLogMarkable = markable
	session.Send(protocol.TypeEventLogPage, msg.RequestID, page)
}

// handleEventLogSeen da por visto el registro hasta upToId, acotado al tope de la
// última página que esta sesión sirvió (RN-LOG-004). Sin página servida no hace
// nada. Sin respuesta si todo va bien: la siguiente página trae el cursor.
func (s *Server) handleEventLogSeen(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.EventLogSeenPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil || p.UpToID < 1 {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	if s.eventLog == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "el registro no está disponible")
		return
	}
	upTo := min(p.UpToID, session.eventLogMarkable)
	if upTo < 1 {
		return
	}
	qctx, cancel := context.WithTimeout(ctx, eventLogTimeout)
	defer cancel()
	if err := s.eventLog.MarkSeen(qctx, session.PlayerID, upTo); err != nil {
		log.Warn("no se pudo marcar el registro", "err", err)
		session.SendError(msg.RequestID, protocol.CodeInternalError, "no se pudo guardar lo visto")
	}
}
