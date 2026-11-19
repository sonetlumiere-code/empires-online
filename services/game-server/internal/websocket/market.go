package websocket

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/market"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Market es el tablón del mercado (SPEC-MARKETS §6, ADR-047 §6): lo lee la
// conexión de la base, sin pasar por el loop. postgres.MarketRepo lo implementa.
type Market interface {
	Board(ctx context.Context, viewer uuid.UUID, receive, pay string) ([]protocol.MarketOfferView, error)
}

// SetMarket conecta el tablón. Sin él, market.list responde NOT_IMPLEMENTED; las
// órdenes del mercado pasan por la simulación igualmente.
func (s *Server) SetMarket(m Market) { s.market = m }

// marketLot traduce el lote de una orden; false si no es del catálogo o se sale
// de la cota (el esquema ya lo rechaza; esto es la segunda barrera).
func marketLot(l protocol.ResourceAmount) (economy.Cost, bool) {
	r := economy.Resource(l.Resource)
	if !r.Valid() || l.Amount < 1 || l.Amount > market.MaxTotal {
		return economy.Cost{}, false
	}
	return economy.Cost{Resource: r, Amount: l.Amount}, true
}

func validLots(n int32) bool { return n >= 1 && n <= market.MaxLots }

// handleMarketOrder decodifica una de las tres órdenes del mercado y la manda al
// loop. El cliente dice qué da, qué pide y cuántos lotes, o sobre qué oferta; la
// comisión, si alcanza y la ruta son del servidor (INV-SEC-001).
func (s *Server) handleMarketOrder(ctx context.Context, session *Session, msg protocol.Inbound) {
	var cmd simulation.Command
	switch msg.Type {
	case protocol.TypeMarketPost:
		var p protocol.MarketPostPayload
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		give, okGive := marketLot(p.Give)
		want, okWant := marketLot(p.Want)
		if !okGive || !okWant || !validLots(p.Lots) {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "lotes de la oferta inválidos")
			return
		}
		cmd = simulation.PostMarketOffer{
			PlayerID: session.PlayerID, SessionID: session.ID, RequestID: msg.RequestID,
			Give: give, Want: want, Lots: p.Lots,
		}
	case protocol.TypeMarketBuy:
		var p protocol.MarketBuyPayload
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil || p.OfferID < 1 || !validLots(p.Lots) {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		cmd = simulation.BuyMarketOffer{
			PlayerID: session.PlayerID, SessionID: session.ID, RequestID: msg.RequestID,
			OfferID: p.OfferID, Lots: p.Lots,
		}
	case protocol.TypeMarketCancel:
		var p protocol.MarketCancelPayload
		if err := protocol.DecodeStrict(msg.Payload, &p); err != nil || p.OfferID < 1 {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
			return
		}
		cmd = simulation.CancelMarketOffer{
			PlayerID: session.PlayerID, SessionID: session.ID, RequestID: msg.RequestID, OfferID: p.OfferID,
		}
	default:
		return
	}
	if !s.claimRequest(ctx, session, msg.RequestID) {
		return
	}
	s.dispatchClaimed(ctx, session, msg.RequestID, cmd)
}

// handleMarketList sirve el tablón al jugador de la sesión: las ofertas abiertas
// de los demás (ADR-047 §6). No pasa por el loop ni reclama el requestId: leer
// no tiene efecto.
func (s *Server) handleMarketList(ctx context.Context, session *Session, msg protocol.Inbound, log *slog.Logger) {
	var p protocol.MarketListPayload
	if err := protocol.DecodeStrict(msg.Payload, &p); err != nil {
		session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "payload inválido")
		return
	}
	for _, r := range []string{p.Receive, p.Pay} {
		if r != "" && !economy.Resource(r).Valid() {
			session.SendError(msg.RequestID, protocol.CodeInvalidMessage, "recurso desconocido")
			return
		}
	}
	if s.market == nil {
		session.SendError(msg.RequestID, protocol.CodeNotImplemented, "el mercado no está disponible")
		return
	}
	qctx, cancel := context.WithTimeout(ctx, messagesTimeout)
	defer cancel()
	offers, err := s.market.Board(qctx, session.PlayerID, p.Receive, p.Pay)
	if err != nil {
		log.Warn("no se pudo leer el tablón del mercado", "err", err)
		session.SendError(msg.RequestID, protocol.CodeInternalError, "no se pudo leer el mercado")
		return
	}
	session.Send(protocol.TypeMarketListing, msg.RequestID, protocol.MarketListingPayload{Offers: offers})
}
