package websocket

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/message"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Messages son los mensajes entre jugadores (SPEC-MESSAGES, ADR-038): los
// guarda y los lee la conexión, sin pasar por el loop. postgres.MessageRepo lo
// implementa.
type Messages interface {
	// Send guarda un mensaje ya normalizado; reserveNew se pide sólo si la
	// pareja no se había escrito nunca (RN-MSG-005).
	Send(ctx context.Context, from, to uuid.UUID, body string, requestID uuid.UUID,
		reserveNew func() (release func(), ok bool)) (message.Sent, error)
	// Existing devuelve el mensaje que from ya guardó con ese requestId, si lo
	// hay: el reintento de un envío confirmado no choca con el límite de ritmo.
	Existing(ctx context.Context, from, requestID uuid.UUID) (message.Sent, bool, error)
	Inbox(ctx context.Context, me uuid.UUID) (protocol.MessageInboxPayload, error)
	Page(ctx context.Context, me, other uuid.UUID, beforeID int64) (protocol.MessagePagePayload, error)
	MarkRead(ctx context.Context, me, other uuid.UUID, upTo int64) error
	Block(ctx context.Context, me, other uuid.UUID, blocked bool) error
}

// messagesTimeout acota cada operación de los mensajes contra la base: si no
// responde, el comando falla con INTERNAL_ERROR y la sesión sigue (RN-MSG-009).
const messagesTimeout = 3 * time.Second

// SetMessages conecta los mensajes y su límite de ritmo. Sin ellos, los cinco
// comandos responden NOT_IMPLEMENTED; los tests que no los usan no los montan.
func (s *Server) SetMessages(m Messages, limiter *message.Limiter) {
	s.messages = m
	s.messageLimiter = limiter
}

// rejectMessage cuenta un envío no guardado y lo responde.
func (s *Server) rejectMessage(session *Session, requestID, reason, code, text string) {
	if s.metrics != nil {
		s.metrics.MessagesRejected.WithLabelValues(reason).Inc()
	}
	session.SendError(requestID, code, text)
}

// messageErrorCode traduce un error de los mensajes al código y al motivo de la
// métrica. El texto de los errores del dominio es el que ve el jugador.
func messageErrorCode(err error) (code, reason, text string) {
	switch {
	case errors.Is(err, message.ErrUnknownPlayer), errors.Is(err, message.ErrSelf):
		return protocol.CodeInvalidTarget, observability.MessageRejectTarget, err.Error()
	case errors.Is(err, message.ErrBlockedByOther), errors.Is(err, message.ErrBlockedByMe):
		return protocol.CodeInvalidTarget, observability.MessageRejectBlocked, err.Error()
	case errors.Is(err, message.ErrRateLimited):
		return protocol.CodeRateLimited, observability.MessageRejectRate, err.Error()
	}
	return protocol.CodeInternalError, observability.MessageRejectInternal, "no se pudo completar la operación"
}

// parseOther lee el uuid del otro jugador de un comando: un uuid ilegible es
// INVALID_MESSAGE y el propio jugador, INVALID_TARGET.
func parseOther(session *Session, requestID, raw string) (uuid.UUID, bool) {
	other, err := uuid.Parse(raw)
	if err != nil {
		session.SendError(requestID, protocol.CodeInvalidMessage, "payload inválido")
		return uuid.Nil, false
	}
	if other == session.PlayerID {
		session.SendError(requestID, protocol.CodeInvalidTarget, message.ErrSelf.Error())
		return uuid.Nil, false
	}
	return other, true
}

// handleMessageSend escribe al jugador toPlayerId en nombre del de la sesión
// (RN-MSG-001): valida el texto, reserva un envío del límite por minuto, guarda
// y, tras el COMMIT, entrega message.new a las sesiones de los dos (ADR-038 §3,
// §5 y §6). El texto no va a los logs (RN-MSG-007).
func (s *Server) handleMessageSend(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.MessageSendPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
		s.rejectMessage(session, msg.RequestID, observability.MessageRejectInvalid, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	requestID, err := uuid.Parse(msg.RequestID)
	if err != nil {
		s.rejectMessage(session, msg.RequestID, observability.MessageRejectInvalid, protocol.CodeInvalidMessage, "requestId inválido")
		return
	}
	body, err := message.NormalizeBody(p.Body)
	if err != nil {
		s.rejectMessage(session, msg.RequestID, observability.MessageRejectInvalid, protocol.CodeInvalidMessage, err.Error())
		return
	}
	to, err := uuid.Parse(p.ToPlayerID)
	if err != nil {
		s.rejectMessage(session, msg.RequestID, observability.MessageRejectInvalid, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	if to == session.PlayerID {
		s.rejectMessage(session, msg.RequestID, observability.MessageRejectTarget, protocol.CodeInvalidTarget, message.ErrSelf.Error())
		return
	}
	if s.messages == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "los mensajes no están disponibles")
		return
	}

	now := time.Now()
	release := func() {}
	if s.messageLimiter != nil {
		rel, ok := s.messageLimiter.ReserveSend(session.PlayerID, now)
		if !ok {
			// Sin ritmo, un reintento de un mensaje ya guardado sigue recibiendo
			// su confirmación (INV-SEC-007); sólo lo nuevo se rechaza.
			qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
			sent, found, err := s.messages.Existing(qctx, session.PlayerID, requestID)
			cancel()
			if err == nil && found {
				s.confirmDuplicate(session, msg.RequestID, sent)
				return
			}
			s.rejectMessage(session, msg.RequestID, observability.MessageRejectRate, protocol.CodeRateLimited,
				"demasiados mensajes seguidos; espera un poco")
			return
		}
		release = rel
	}
	reserveNew := func() (func(), bool) {
		if s.messageLimiter == nil {
			return func() {}, true
		}
		return s.messageLimiter.ReserveNewConversation(session.PlayerID, now)
	}

	qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
	defer cancel()
	sent, err := s.messages.Send(qctx, session.PlayerID, to, body, requestID, reserveNew)
	if err != nil {
		release()
		code, reason, text := messageErrorCode(err)
		if reason == observability.MessageRejectInternal {
			log.Warn("no se pudo guardar un mensaje", "err", err, "to", to)
		}
		s.rejectMessage(session, msg.RequestID, reason, code, text)
		return
	}
	if sent.Duplicate {
		// El reintento de un mensaje que ya existe no gasta ritmo.
		release()
		s.confirmDuplicate(session, msg.RequestID, sent)
		return
	}
	if s.metrics != nil {
		s.metrics.MessagesSent.Inc()
	}
	log.Debug("mensaje guardado", "message_id", sent.ID, "to", to, "chars", len([]rune(body)))
	s.deliverMessage(sent, session, msg.RequestID)
}

// confirmDuplicate responde a un reintento con el mensaje que ya existía, sólo
// a esta sesión: las demás ya lo recibieron (INV-SEC-007).
func (s *Server) confirmDuplicate(session *Session, requestID string, sent message.Sent) {
	session.noteMessageServed(otherOf(sent, session.PlayerID), sent.ID)
	session.Send(protocol.TypeMessageNew, requestID, protocol.MessageNewPayload{Message: messageView(sent, session.PlayerID)})
}

// deliverMessage manda message.new a todas las sesiones de los dos jugadores de
// este proceso (ADR-038 §6); la que lo envió lo recibe con su requestId. Cada
// sesión apunta el id como servido de esa conversación, que es lo que permite
// darlo por leído (INV-PERSIST-007).
func (s *Server) deliverMessage(sent message.Sent, origin *Session, requestID string) {
	for _, player := range []uuid.UUID{sent.From, sent.To} {
		view := protocol.MessageNewPayload{Message: messageView(sent, player)}
		other := otherOf(sent, player)
		s.hub.forEachSessionOf(player, func(target *Session) {
			target.noteMessageServed(other, sent.ID)
			req := ""
			if target == origin {
				req = requestID
			}
			target.Send(protocol.TypeMessageNew, req, view)
		})
	}
}

// otherOf es el jugador del mensaje que no es player.
func otherOf(sent message.Sent, player uuid.UUID) uuid.UUID {
	if sent.From == player {
		return sent.To
	}
	return sent.From
}

// messageView es el mensaje visto por player: «el otro» y su ciudad son los del
// jugador que no es él (RN-MSG-010).
func messageView(sent message.Sent, player uuid.UUID) protocol.MessageView {
	other, city := sent.To, sent.ToCity
	if sent.To == player {
		other, city = sent.From, sent.FromCity
	}
	v := protocol.MessageView{
		ID: sent.ID, FromPlayerID: sent.From.String(), ToPlayerID: sent.To.String(),
		OtherPlayerID: other.String(), Body: sent.Body, SentAtMs: sent.SentAtMs,
	}
	if city != nil {
		id, name := city.ID, city.Name
		v.OtherCityID, v.OtherCityName = &id, &name
	}
	return v
}

// handleMessageList sirve la bandeja del jugador de la sesión, nunca la de otro
// (INV-SEC-010).
func (s *Server) handleMessageList(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.MessageListPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	if s.messages == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "los mensajes no están disponibles")
		return
	}
	s.sendInbox(ctx, session, msg.RequestID, log)
}

func (s *Server) sendInbox(ctx context.Context, session *Session, requestID string, log *slog.Logger) {
	qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
	defer cancel()
	inbox, err := s.messages.Inbox(qctx, session.PlayerID)
	if err != nil {
		log.Warn("no se pudo leer la bandeja", "err", err)
		session.SendError(requestID, protocol.CodeInternalError, "no se pudo leer la bandeja")
		return
	}
	session.Send(protocol.TypeMessageInbox, requestID, inbox)
}

// handleMessageFetch sirve una página de la conversación del jugador de la
// sesión con otherPlayerId, y apunta su último id como servido.
func (s *Server) handleMessageFetch(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.MessageFetchPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil || (p.BeforeID != nil && *p.BeforeID < 1) {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	other, ok := parseOther(session, msg.RequestID, p.OtherPlayerID)
	if !ok {
		return
	}
	if s.messages == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "los mensajes no están disponibles")
		return
	}
	var before int64
	if p.BeforeID != nil {
		before = *p.BeforeID
	}
	qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
	defer cancel()
	page, err := s.messages.Page(qctx, session.PlayerID, other, before)
	if err != nil {
		code, _, text := messageErrorCode(err)
		if code == protocol.CodeInternalError {
			log.Warn("no se pudo leer una conversación", "err", err)
			text = "no se pudo leer la conversación"
		}
		session.SendError(msg.RequestID, code, text)
		return
	}
	if n := len(page.Messages); n > 0 {
		session.noteMessageServed(other, page.Messages[n-1].ID)
	}
	session.Send(protocol.TypeMessagePage, msg.RequestID, page)
}

// handleMessageRead da por leída la conversación hasta upToId, acotado a lo que
// esta sesión recibió de ella (RN-MSG-008, INV-PERSIST-007). Sin nada recibido,
// no hace nada; sin respuesta si todo va bien.
func (s *Server) handleMessageRead(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.MessageReadPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil || p.UpToID < 1 {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	other, ok := parseOther(session, msg.RequestID, p.OtherPlayerID)
	if !ok {
		return
	}
	if s.messages == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "los mensajes no están disponibles")
		return
	}
	upTo := min(p.UpToID, session.messageServedUpTo(other))
	if upTo < 1 {
		return
	}
	qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
	defer cancel()
	if err := s.messages.MarkRead(qctx, session.PlayerID, other, upTo); err != nil {
		log.Warn("no se pudo guardar lo leído", "err", err)
		session.SendError(msg.RequestID, protocol.CodeInternalError, "no se pudo guardar lo leído")
	}
}

// handleMessageBlock bloquea o desbloquea a playerId y responde con la bandeja.
func (s *Server) handleMessageBlock(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.MessageBlockPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	other, ok := parseOther(session, msg.RequestID, p.PlayerID)
	if !ok {
		return
	}
	if s.messages == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "los mensajes no están disponibles")
		return
	}
	qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
	defer cancel()
	if err := s.messages.Block(qctx, session.PlayerID, other, p.Blocked); err != nil {
		code, _, text := messageErrorCode(err)
		if code == protocol.CodeInternalError {
			log.Warn("no se pudo guardar un bloqueo", "err", err)
			text = "no se pudo guardar el bloqueo"
		}
		session.SendError(msg.RequestID, code, text)
		return
	}
	s.sendInbox(ctx, session, msg.RequestID, log)
}
