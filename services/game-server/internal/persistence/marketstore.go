package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// MarketStore implementa simulation.MarketStore sobre postgres.MarketRepo
// (ADR-047): traduce los tipos de la simulación a los de la base y los rechazos
// de la base a los errores que la simulación conoce.
type MarketStore struct {
	repo *postgres.MarketRepo
}

// NewMarketStore construye el adaptador.
func NewMarketStore(r *postgres.MarketRepo) *MarketStore { return &MarketStore{repo: r} }

// Post publica una oferta con su débito.
func (s *MarketStore) Post(ctx context.Context, p simulation.MarketPostWrite) (int64, error) {
	id, err := s.repo.Post(ctx, postgres.MarketPost{Offer: p.Offer, Stock: toDelta(p.Stock), Tick: p.Tick, AtMs: p.AtMs})
	return id, stale(err)
}

// Buy escribe una compra con sus dos caravanas (ADR-047 §2).
func (s *MarketStore) Buy(ctx context.Context, p simulation.MarketBuyWrite) (int64, []simulation.CaravanCreated, error) {
	spawns := make([]postgres.CaravanSpawn, len(p.Caravans))
	for i, c := range p.Caravans {
		spawns[i] = postgres.CaravanSpawn{
			Sender: c.Half.Sender, FromCity: c.Half.FromCity, ToCity: c.Half.ToCity, Cargo: c.Half.Cargo,
			Origin: c.Origin, MaxHP: c.MaxHP, Movement: c.Movement,
		}
	}
	id, created, err := s.repo.Buy(ctx, postgres.MarketPurchase{
		Offer: p.Offer, Buyer: p.Buyer, BuyerCity: p.BuyerCity, RequestID: p.RequestID, Purchase: p.Purchase,
		Stock: toDelta(p.Stock), Caravans: spawns, Tick: p.Tick, AtMs: p.AtMs,
	})
	if err != nil {
		return 0, nil, stale(marketConflict(err))
	}
	out := make([]simulation.CaravanCreated, len(created))
	for i, c := range created {
		out[i] = simulation.CaravanCreated{CaravanID: c.CaravanID, UnitID: c.UnitID}
	}
	return id, out, nil
}

// Close cierra una oferta y acredita lo que vuelve (ADR-047 §3).
func (s *MarketStore) Close(ctx context.Context, c simulation.MarketCloseWrite) error {
	return s.repo.Close(ctx, postgres.MarketClose{
		Offer: c.Offer, Returned: c.Returned, Stock: toDelta(c.Stock), Tick: c.Tick, AtMs: c.AtMs, By: c.By,
	})
}

func marketConflict(err error) error {
	if errors.Is(err, postgres.ErrConflict) || errors.Is(err, postgres.ErrVersionConflict) {
		return fmt.Errorf("%w: %v", simulation.ErrMarketConflict, err)
	}
	return err
}

// Comprobación del contrato con la simulación.
var _ simulation.MarketStore = (*MarketStore)(nil)
