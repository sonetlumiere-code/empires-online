package websocket

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/message"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Clans es el canal y la vista pública de los clanes (SPEC-CLANS §4.6,
// ADR-040 §5): los atiende la conexión, sin pasar por el loop, y la pertenencia
// se comprueba en la base. postgres.ClanRepo lo implementa.
type Clans interface {
	ChatSend(ctx context.Context, from uuid.UUID, body string, requestID uuid.UUID) (clan.Sent, error)
	ChatExisting(ctx context.Context, from, requestID uuid.UUID) (clan.Sent, bool, error)
	ChatPage(ctx context.Context, me uuid.UUID, beforeID int64) (protocol.ClanChatPagePayload, error)
	ChatMarkRead(ctx context.Context, me uuid.UUID, upTo func(clanID int64) int64) error
	Lookup(ctx context.Context, clanID int64) (protocol.ClanInfoPayload, error)
}

// SetClans conecta el canal de los clanes. Sin él, sus cuatro comandos
// responden NOT_IMPLEMENTED. El ritmo es el de los mensajes directos
// (SetMessages): un mensaje del canal cuenta como uno más (SPEC-CLANS Q9).
func (s *Server) SetClans(c Clans) { s.clans = c }

// clanErrorCode traduce un error del canal al código y al texto que ve el
// jugador.
func clanErrorCode(err error) (code, text string) {
	switch {
	case errors.Is(err, clan.ErrNotMember), errors.Is(err, clan.ErrUnknownClan):
		return protocol.CodeInvalidTarget, err.Error()
	}
	return protocol.CodeInternalError, "no se pudo completar la operación"
}

// clanMessageView es un mensaje del canal para la red.
func clanMessageView(sent clan.Sent) protocol.ClanMessageView {
	v := protocol.ClanMessageView{
		ID: sent.ID, ClanID: sent.ClanID, FromPlayerID: sent.From.String(), Body: sent.Body, SentAtMs: sent.SentAtMs,
	}
	if sent.FromCityID != 0 {
		id, name := sent.FromCityID, sent.FromCityName
		v.FromCityID, v.FromCityName = &id, &name
	}
	return v
}

// handleClanChatSend escribe en el canal del clan del jugador de la sesión
// (INV-SEC-012): valida el texto como un mensaje directo, gasta ritmo del
// mismo cubo, guarda con la pertenencia comprobada bajo el candado del clan y,
// tras el COMMIT, entrega clan.chat.new a las sesiones de los miembros. El
// texto no va a los logs.
func (s *Server) handleClanChatSend(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.ClanChatSendPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	requestID, err := uuid.Parse(msg.RequestID)
	if err != nil {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "requestId inválido")
		return
	}
	body, err := message.NormalizeBody(p.Body)
	if err != nil {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, err.Error())
		return
	}
	if s.clans == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "los clanes no están disponibles")
		return
	}

	release := func() {}
	if s.messageLimiter != nil {
		rel, ok := s.messageLimiter.ReserveSend(session.PlayerID, time.Now())
		if !ok {
			qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
			sent, found, err := s.clans.ChatExisting(qctx, session.PlayerID, requestID)
			cancel()
			if err == nil && found {
				s.confirmClanDuplicate(session, msg.RequestID, sent)
				return
			}
			session.SendError(msg.RequestID, protocol.CodeRateLimited, "demasiados mensajes seguidos; espera un poco")
			return
		}
		release = rel
	}

	qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
	defer cancel()
	sent, err := s.clans.ChatSend(qctx, session.PlayerID, body, requestID)
	if err != nil {
		release()
		code, text := clanErrorCode(err)
		if code == protocol.CodeInternalError {
			log.Warn("no se pudo guardar un mensaje del canal", "err", err)
		}
		session.SendError(msg.RequestID, code, text)
		return
	}
	if sent.Duplicate {
		release()
		s.confirmClanDuplicate(session, msg.RequestID, sent)
		return
	}
	if s.metrics != nil {
		s.metrics.ClanMessagesSent.Inc()
	}
	log.Debug("mensaje del canal guardado", "message_id", sent.ID, "clan_id", sent.ClanID, "chars", len([]rune(body)))
	view := protocol.ClanChatNewPayload{Message: clanMessageView(sent)}
	for _, member := range sent.Members {
		s.hub.forEachSessionOf(member, func(target *Session) {
			target.noteClanServed(sent.ClanID, sent.ID)
			req := ""
			if target == session {
				req = msg.RequestID
			}
			target.Send(protocol.TypeClanChatNew, req, view)
		})
	}
}

// confirmClanDuplicate responde a un reintento con el mensaje que ya existía,
// sólo a esta sesión (INV-SEC-007).
func (s *Server) confirmClanDuplicate(session *Session, requestID string, sent clan.Sent) {
	session.noteClanServed(sent.ClanID, sent.ID)
	session.Send(protocol.TypeClanChatNew, requestID, protocol.ClanChatNewPayload{Message: clanMessageView(sent)})
}

// handleClanChatFetch sirve una página del canal del clan del jugador de la
// sesión, sólo lo posterior a su entrada (INV-SEC-012).
func (s *Server) handleClanChatFetch(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.ClanChatFetchPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil || (p.BeforeID != nil && *p.BeforeID < 1) {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	if s.clans == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "los clanes no están disponibles")
		return
	}
	var before int64
	if p.BeforeID != nil {
		before = *p.BeforeID
	}
	qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
	defer cancel()
	page, err := s.clans.ChatPage(qctx, session.PlayerID, before)
	if err != nil {
		code, text := clanErrorCode(err)
		if code == protocol.CodeInternalError {
			log.Warn("no se pudo leer el canal", "err", err)
			text = "no se pudo leer el canal"
		}
		session.SendError(msg.RequestID, code, text)
		return
	}
	if n := len(page.Messages); n > 0 {
		session.noteClanServed(page.ClanID, page.Messages[n-1].ID)
	}
	session.Send(protocol.TypeClanChatPage, msg.RequestID, page)
}

// handleClanChatRead da por leído el canal hasta upToId, acotado a lo que esta
// sesión recibió de él. Sin respuesta si todo va bien.
func (s *Server) handleClanChatRead(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.ClanChatReadPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil || p.UpToID < 1 {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	if s.clans == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "los clanes no están disponibles")
		return
	}
	qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
	defer cancel()
	err := s.clans.ChatMarkRead(qctx, session.PlayerID, func(clanID int64) int64 {
		return min(p.UpToID, session.clanServedUpTo(clanID))
	})
	if err != nil {
		code, text := clanErrorCode(err)
		if code == protocol.CodeInternalError {
			log.Warn("no se pudo guardar lo leído del canal", "err", err)
			text = "no se pudo guardar lo leído"
		}
		session.SendError(msg.RequestID, code, text)
	}
}

// handleClanLookup sirve la vista pública de un clan (RN-CLAN-008).
func (s *Server) handleClanLookup(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.ClanLookupPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil || p.ClanID < 1 {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	if s.clans == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "los clanes no están disponibles")
		return
	}
	qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
	defer cancel()
	info, err := s.clans.Lookup(qctx, p.ClanID)
	if err != nil {
		code, text := clanErrorCode(err)
		if code == protocol.CodeInternalError {
			log.Warn("no se pudo leer un clan", "err", err)
			text = "no se pudo leer el clan"
		}
		session.SendError(msg.RequestID, code, text)
		return
	}
	session.Send(protocol.TypeClanInfo, msg.RequestID, info)
}
