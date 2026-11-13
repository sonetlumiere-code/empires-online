package persistence

import (
	"context"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// CaravanStore implementa simulation.CaravanStore sobre postgres.CaravanRepo
// (ADR-037): traduce los tipos de la simulación a los de la base.
type CaravanStore struct {
	repo *postgres.CaravanRepo
}

// NewCaravanStore construye el adaptador.
func NewCaravanStore(r *postgres.CaravanRepo) *CaravanStore { return &CaravanStore{repo: r} }

// DeliverCaravan entrega una caravana en una transacción (RN-CARAVAN-004).
func (s *CaravanStore) DeliverCaravan(ctx context.Context, d simulation.CaravanDelivery) error {
	return stale(s.repo.Deliver(ctx, postgres.CaravanDelivery{
		CaravanID: d.CaravanID, UnitID: d.UnitID, CityID: d.CityID, Recipient: d.Recipient,
		Counterpart: d.Counterpart, Stocks: toDeltas(d.Stocks), Cargo: d.Cargo, At: d.At, Tick: d.Tick, AtMs: d.AtMs,
	}))
}

// CaptureCaravan escribe una captura, o una pérdida (RN-CARAVAN-006).
func (s *CaravanStore) CaptureCaravan(ctx context.Context, c simulation.CaravanCapture) error {
	return s.repo.Capture(ctx, postgres.CaravanCapture{
		CaravanID: c.CaravanID, UnitID: c.UnitID, PreviousOwner: c.PreviousOwner, Captor: c.Captor,
		CaptorCity: c.CaptorCity, OriginalRecipient: c.OriginalRecipient, HP: c.HP, At: c.At,
		Movement: c.Movement, Cargo: c.Cargo, Tick: c.Tick, AtMs: c.AtMs,
	})
}

// Comprobación del contrato con la simulación.
var _ simulation.CaravanStore = (*CaravanStore)(nil)
