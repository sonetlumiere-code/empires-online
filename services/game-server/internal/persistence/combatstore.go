package persistence

import (
	"context"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// CombatStore implementa simulation.CombatStore sobre PostgreSQL.
type CombatStore struct {
	store *postgres.Store
	units *postgres.UnitRepo
}

// NewCombatStore construye el adaptador.
func NewCombatStore(s *postgres.Store, u *postgres.UnitRepo) *CombatStore {
	return &CombatStore{store: s, units: u}
}

// PersistDeath escribe una muerte en una transacción (ADR-024 §2).
func (c *CombatStore) PersistDeath(ctx context.Context, d simulation.Death) error {
	return c.store.InTx(ctx, func(tx pgxTx) error {
		_, err := c.units.Kill(ctx, tx, postgres.Kill{
			VictimID: d.VictimID, VictimPlayer: d.VictimPlayer,
			KillerID: d.KillerID, KillerPlayer: d.KillerPlayer,
			CityID: d.CityID, PopulationCost: d.PopulationCost,
			At: d.At, Tick: d.Tick, Loot: d.Loot, Lost: d.Lost,
		})
		return err
	})
}
