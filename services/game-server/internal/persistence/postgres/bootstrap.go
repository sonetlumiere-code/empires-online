package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// BootstrapRequest describe el alta completa de un jugador nuevo.
type BootstrapRequest struct {
	Username       string
	PasswordHash   string
	CivilizationID int32
	FactionID      int32
	CityName       string
	CityCenter     world.Tile
	Era            city.Era
	PopulationCap  int32
	// VillagerSpawns son los tiles donde nacen los aldeanos iniciales.
	VillagerSpawns []world.Tile
}

// BootstrapResult es el mundo recién creado para ese jugador.
type BootstrapResult struct {
	Player *player.Player
	City   *city.City
	Units  []*unit.Unit
}

// Bootstrapper crea jugadores completos de forma atómica.
type Bootstrapper struct {
	store   *Store
	players *PlayerRepo
	cities  *CityRepo
	units   *UnitRepo
}

// NewBootstrapper crea el servicio de alta.
func NewBootstrapper(s *Store, p *PlayerRepo, c *CityRepo, u *UnitRepo) *Bootstrapper {
	return &Bootstrapper{store: s, players: p, cities: c, units: u}
}

// Create da de alta un jugador con su ciudad y sus aldeanos iniciales, TODO dentro
// de una única transacción.
//
// La atomicidad no es un lujo: un jugador sin ciudad, o una ciudad sin aldeanos,
// es un estado que ninguna regla del juego sabe interpretar. O se crea el mundo
// entero del jugador, o no se crea nada. Ver INV-PLAYER-003.
func (b *Bootstrapper) Create(ctx context.Context, req BootstrapRequest) (*BootstrapResult, error) {
	if err := player.ValidateUsername(req.Username); err != nil {
		return nil, err
	}
	if len(req.VillagerSpawns) == 0 {
		return nil, fmt.Errorf("un jugador nuevo necesita al menos un aldeano")
	}
	def, err := unit.Lookup(unit.TypeVillager)
	if err != nil {
		return nil, err
	}

	result := &BootstrapResult{}

	err = b.store.InTx(ctx, func(tx pgx.Tx) error {
		p := &player.Player{
			ID:             uuid.New(),
			Username:       req.Username,
			CivilizationID: req.CivilizationID,
			FactionID:      req.FactionID,
		}
		if err := b.players.Create(ctx, tx, p, req.PasswordHash); err != nil {
			return err
		}

		c := &city.City{
			OwnerPlayerID:   p.ID,
			Name:            req.CityName,
			CenterX:         req.CityCenter.X,
			CenterY:         req.CityCenter.Y,
			Era:             req.Era,
			Population:      0,
			PopulationLimit: req.PopulationCap,
			PresenceState:   city.PresenceOnline,
		}
		if _, err := b.cities.Create(ctx, tx, c); err != nil {
			return err
		}

		units := make([]*unit.Unit, 0, len(req.VillagerSpawns))
		for _, spawn := range req.VillagerSpawns {
			u := &unit.Unit{
				PlayerID: p.ID,
				CityID:   &c.ID,
				Type:     unit.TypeVillager,
				X:        spawn.X,
				Y:        spawn.Y,
				HP:       def.MaxHP,
				MaxHP:    def.MaxHP,
				Status:   unit.StatusIdle,
			}
			if _, err := b.units.Create(ctx, tx, u); err != nil {
				return err
			}
			units = append(units, u)
		}

		// La población se deriva de las unidades, no se lleva a mano: así no puede
		// quedar desincronizada del mundo real.
		population, err := b.cities.UpdatePopulation(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		c.Population = population

		if err := appendEvent(ctx, tx, worldEvent{
			EventType: "PlayerBootstrapped",
			PlayerID:  &p.ID,
			Payload: map[string]any{
				"cityId":    c.ID,
				"villagers": len(units),
				"center":    map[string]int32{"x": c.CenterX, "y": c.CenterY},
			},
		}); err != nil {
			return err
		}

		result.Player, result.City, result.Units = p, c, units
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
