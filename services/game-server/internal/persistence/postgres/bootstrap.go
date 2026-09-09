package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
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
	// TerritoryID es el territorio que contiene el tile central, o 0 si ninguno.
	// Lo resuelve el llamante contra el índice, que es inmutable tras la
	// hidratación y por tanto seguro de leer fuera del game loop.
	TerritoryID int64
	// Tick es el tick del game loop en el que ocurre el alta. Fecha los eventos
	// de dominio. Lo aporta el llamante porque el Bootstrapper no conoce el loop.
	Tick uint64
	// VillagerSpawns son los tiles donde nacen los aldeanos iniciales.
	VillagerSpawns []world.Tile
}

// BootstrapResult es el mundo recién creado para ese jugador.
type BootstrapResult struct {
	Player *player.Player
	City   *city.City
	Units  []*unit.Unit
	// TerritoryControl es el control recién adquirido, o nil si no hubo cambio.
	TerritoryControl *territory.Control
}

// Bootstrapper crea jugadores completos de forma atómica.
type Bootstrapper struct {
	store       *Store
	players     *PlayerRepo
	cities      *CityRepo
	units       *UnitRepo
	territories *TerritoryRepo
}

// NewBootstrapper crea el servicio de alta.
func NewBootstrapper(s *Store, p *PlayerRepo, c *CityRepo, u *UnitRepo, t *TerritoryRepo) *Bootstrapper {
	return &Bootstrapper{store: s, players: p, cities: c, units: u, territories: t}
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

		// RN-TERR-007: fundar la ciudad inicial es el ÚNICO productor de cambio de
		// dueño del MVP. Va en esta misma transacción porque el cambio de
		// ownership es write-through (spec §10): o se funda la ciudad Y cambia el
		// territorio, o no ocurre ninguna de las dos cosas.
		if req.TerritoryID != 0 {
			control, err := b.claimTerritory(ctx, tx, req.TerritoryID, p.ID, req.Tick)
			if err != nil {
				return err
			}
			result.TerritoryControl = control
		}

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

// claimTerritory intenta entregar el territorio al fundador.
//
// Devuelve nil SIN error cuando el territorio ya tenía dueño: fundar dentro del
// territorio de otro jugador es válido y no lo cambia de manos (RN-TERR-008).
// Tratarlo como error abortaría la fundación entera por una regla que dice
// justamente lo contrario.
//
// Un conflicto de versión sí aborta: significa que otra transacción tocó la fila
// entre la lectura y la escritura, y la premisa sobre la que se decidió reclamar
// ya no se cumple. Reintentar es cosa de quien vuelva a intentar el alta, no de
// esta transacción, que debe fallar entera para no dejar medio mundo escrito.
func (b *Bootstrapper) claimTerritory(
	ctx context.Context, tx pgx.Tx, territoryID int64, playerID uuid.UUID, tick uint64,
) (*territory.Control, error) {
	actual, err := b.territories.GetControl(ctx, tx, territoryID)
	if err != nil {
		return nil, fmt.Errorf("leer el control del territorio %d: %w", territoryID, err)
	}
	if actual.OwnerType != territory.OwnerNone {
		return nil, nil
	}

	capturedAt := time.Now().UTC()
	control, err := b.territories.ClaimForPlayer(ctx, tx, territoryID, playerID, capturedAt, actual.Version)
	if err != nil {
		if errors.Is(err, ErrAlreadyOwned) {
			// Alguien lo reclamó entre nuestra lectura y nuestra escritura. No es
			// un fallo: el resultado es el mismo que si hubiera tenido dueño desde
			// el principio.
			return nil, nil
		}
		return nil, err
	}

	// INV-TERR-007 y INV-TERR-004, comprobados antes de dar el cambio por bueno:
	// el CHECK del esquema cubre uno de los dos y este es el único sitio que
	// cubre el otro.
	if err := control.Validate(); err != nil {
		return nil, fmt.Errorf("el control resultante es inválido: %w", err)
	}

	if err := appendEvent(ctx, tx, worldEvent{
		EventType: territory.EventControlChanged,
		Tick:      tick,
		PlayerID:  &playerID,
		Payload: map[string]any{
			"territoryId": control.TerritoryID,
			"ownerType":   string(control.OwnerType),
			"ownerId":     control.OwnerID,
			"version":     control.Version,
		},
	}); err != nil {
		return nil, err
	}

	return &control, nil
}
