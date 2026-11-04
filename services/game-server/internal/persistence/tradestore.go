package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// TradeStore implementa simulation.TradeStore sobre postgres.TradeRepo
// (ADR-032): traduce los tipos de la simulación a los de la base y los
// rechazos de la base a los errores que la simulación conoce.
type TradeStore struct {
	repo *postgres.TradeRepo
}

// NewTradeStore construye el adaptador.
func NewTradeStore(r *postgres.TradeRepo) *TradeStore { return &TradeStore{repo: r} }

// ApplyOffer escribe una transición que no toca almacenes. El índice de
// INV-TRADE-003 violado, o una oferta en otro estado, es
// simulation.ErrTradeConflict.
func (s *TradeStore) ApplyOffer(ctx context.Context, w simulation.TradeWrite) (int64, error) {
	id, err := s.repo.ApplyOffer(ctx, postgres.TradeWrite{
		Insert: w.Insert, Offer: w.Offer, Event: w.Event, Tick: w.Tick, AtMs: w.AtMs, By: w.By, Reason: w.Reason,
	})
	return id, tradeConflict(err)
}

// AcceptTrade mueve un trato aceptado en una transacción.
func (s *TradeStore) AcceptTrade(ctx context.Context, a simulation.TradeAcceptance) error {
	deltas := make([]postgres.StockDelta, len(a.Stocks))
	for i, d := range a.Stocks {
		deltas[i] = toDelta(d)
	}
	err := s.repo.Accept(ctx, postgres.TradeAcceptance{Offer: a.Offer, Stocks: deltas, Tick: a.Tick, AtMs: a.AtMs, By: a.By})
	return stale(tradeConflict(err))
}

func tradeConflict(err error) error {
	if errors.Is(err, postgres.ErrConflict) || errors.Is(err, postgres.ErrVersionConflict) {
		return fmt.Errorf("%w: %v", simulation.ErrTradeConflict, err)
	}
	return err
}

// Comprobación del contrato con la simulación.
var _ simulation.TradeStore = (*TradeStore)(nil)
