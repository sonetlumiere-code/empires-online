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

// AcceptTrade cierra un trato aceptado en una transacción: los débitos y las
// dos caravanas (ADR-037 §3).
func (s *TradeStore) AcceptTrade(ctx context.Context, a simulation.TradeAcceptance) ([]simulation.CaravanCreated, error) {
	spawns := make([]postgres.CaravanSpawn, len(a.Caravans))
	for i, c := range a.Caravans {
		spawns[i] = postgres.CaravanSpawn{
			Sender: c.Half.Sender, FromCity: c.Half.FromCity, ToCity: c.Half.ToCity, Cargo: c.Half.Cargo,
			Origin: c.Origin, MaxHP: c.MaxHP, Movement: c.Movement,
		}
	}
	created, err := s.repo.Accept(ctx, postgres.TradeAcceptance{
		Offer: a.Offer, Stocks: toDeltas(a.Stocks), Caravans: spawns, Tick: a.Tick, AtMs: a.AtMs, By: a.By,
	})
	if err != nil {
		return nil, stale(tradeConflict(err))
	}
	out := make([]simulation.CaravanCreated, len(created))
	for i, c := range created {
		out[i] = simulation.CaravanCreated{CaravanID: c.CaravanID, UnitID: c.UnitID}
	}
	return out, nil
}

func tradeConflict(err error) error {
	if errors.Is(err, postgres.ErrConflict) || errors.Is(err, postgres.ErrVersionConflict) {
		return fmt.Errorf("%w: %v", simulation.ErrTradeConflict, err)
	}
	return err
}

// Comprobación del contrato con la simulación.
var _ simulation.TradeStore = (*TradeStore)(nil)
