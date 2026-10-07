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
	// Era es la era inicial. El límite de población NO viaja en la petición: el
	// Bootstrapper lo lee de la tabla eras dentro de la transacción, para que
	// population_limit sólo pueda valer el population_cap de su era
	// (INV-CITY-003).
	Era city.Era
	// TerritoryID es el territorio que contiene el tile central, o 0 si ninguno.
	// Lo resuelve el llamante contra el índice, que es inmutable tras la
	// hidratación y por tanto seguro de leer fuera del game loop.
	TerritoryID int64
	// Tick es el tick del game loop en el que ocurre el alta. Fecha los eventos
	// de dominio. Lo aporta el llamante porque el Bootstrapper no conoce el loop.
	Tick uint64
	// Now es el instante del alta según el Clock inyectado del llamante. Sella
	// captured_at si la fundación reclama el territorio (INV-TERR-007) y
	// last_online_at de la ciudad recién fundada. Es
	// obligatorio: el Bootstrapper no lee el reloj del sistema, por la misma razón
	// que no conoce el tick.
	Now time.Time
	// VillagerSpawns son los tiles donde nacen los aldeanos iniciales.
	VillagerSpawns []world.Tile
	// Place, si no es nil, elige el emplazamiento y sustituye a CityCenter,
	// VillagerSpawns y TerritoryID. Es la forma de dar de alta a un jugador que
	// pueda competir con otra alta simultánea: ver Create.
	Place PlaceFunc
	// Verify comprueba, DENTRO de la transacción y bajo el cerrojo de
	// fundación, que el centro elegido sigue siendo válido a la vista de todas
	// las ciudades ya confirmadas. Sólo se usa junto con Place.
	Verify func(center world.Tile, existing []world.Tile) bool
}

// Placement es el emplazamiento que elige una PlaceFunc.
type Placement struct {
	Center      world.Tile
	Spawns      []world.Tile
	TerritoryID int64
}

// PlaceFunc elige el emplazamiento de la ciudad a la vista de los centros de
// las ciudades ya fundadas. Se llama FUERA de la transacción (RN-PLAYER-014):
// la búsqueda puede recorrer medio mundo y no debe hacerse con la transacción
// abierta.
type PlaceFunc func(existing []world.Tile) (Placement, error)

// foundingLockKey serializa las altas entre sí: `pg_advisory_xact_lock` se
// libera solo al terminar la transacción. Es «EO_FUND» en ASCII.
const foundingLockKey int64 = 0x454f5f46554e44

// maxPlacementAttempts acota cuántas veces se repite la búsqueda cuando otra
// alta confirma un sitio incompatible entre la búsqueda y la transacción.
const maxPlacementAttempts = 3

// ErrPlacementContended indica que, en cada intento, otra alta confirmó antes
// un sitio incompatible con el elegido. No es que el mundo esté lleno: eso es
// founding.ErrNoSite.
var ErrPlacementContended = errors.New("el emplazamiento elegido quedó ocupado por otra alta en cada intento")

// errPlacementStale aborta una transacción cuyo emplazamiento ya no vale.
var errPlacementStale = errors.New("emplazamiento desactualizado")

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
//
// Con req.Place, el emplazamiento se elige en dos tiempos. La búsqueda ocurre
// fuera de la transacción, con los centros leídos en ese momento. Dentro, tras
// tomar el cerrojo de fundación, req.Verify comprueba el centro elegido contra
// los centros ya confirmados, que es una pasada barata por la lista. Si otra
// alta se adelantó con un sitio incompatible, la transacción se deshace y la
// búsqueda se repite con la lista nueva. Sin esa comprobación, dos altas
// simultáneas podían fundar a menos de la distancia mínima, o con las murallas
// solapadas: el UNIQUE de la base sólo impide compartir el centro exacto
// (INV-CITY-008).
func (b *Bootstrapper) Create(ctx context.Context, req BootstrapRequest) (*BootstrapResult, error) {
	if err := player.ValidateUsername(req.Username); err != nil {
		return nil, err
	}
	if req.Now.IsZero() {
		return nil, fmt.Errorf("el alta necesita el instante del reloj inyectado")
	}
	if req.Place == nil && len(req.VillagerSpawns) == 0 {
		return nil, fmt.Errorf("un jugador nuevo necesita al menos un aldeano")
	}
	def, err := unit.Lookup(unit.TypeVillager)
	if err != nil {
		return nil, err
	}
	if req.Place == nil {
		return b.create(ctx, req, def)
	}

	for intento := 0; intento < maxPlacementAttempts; intento++ {
		existing, err := b.cities.ListCenters(ctx, b.store.pool)
		if err != nil {
			return nil, err
		}
		placement, err := req.Place(existing)
		if err != nil {
			return nil, err
		}
		if len(placement.Spawns) == 0 {
			return nil, fmt.Errorf("un jugador nuevo necesita al menos un aldeano")
		}
		elegido := req
		elegido.CityCenter, elegido.VillagerSpawns, elegido.TerritoryID =
			placement.Center, placement.Spawns, placement.TerritoryID

		result, err := b.create(ctx, elegido, def)
		if !errors.Is(err, errPlacementStale) {
			return result, err
		}
	}
	return nil, ErrPlacementContended
}

// create es una transacción de alta con el emplazamiento ya decidido.
func (b *Bootstrapper) create(ctx context.Context, req BootstrapRequest, def unit.Definition) (*BootstrapResult, error) {
	result := &BootstrapResult{}

	err := b.store.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, foundingLockKey); err != nil {
			return fmt.Errorf("serializar el alta: %w", err)
		}
		if req.Verify != nil {
			existing, err := b.cities.ListCenters(ctx, tx)
			if err != nil {
				return err
			}
			if !req.Verify(req.CityCenter, existing) {
				return errPlacementStale
			}
		}

		p := &player.Player{
			ID:             uuid.New(),
			Username:       req.Username,
			CivilizationID: req.CivilizationID,
			FactionID:      req.FactionID,
		}
		if err := b.players.Create(ctx, tx, p, req.PasswordHash); err != nil {
			return err
		}

		populationCap, err := b.cities.PopulationCapOf(ctx, tx, req.Era)
		if err != nil {
			return err
		}
		fundada := req.Now
		c := &city.City{
			OwnerPlayerID:   p.ID,
			Name:            req.CityName,
			CenterX:         req.CityCenter.X,
			CenterY:         req.CityCenter.Y,
			Era:             req.Era,
			Population:      0,
			PopulationLimit: populationCap,
			PresenceState:   city.PresenceOnline,
			LastOnlineAt:    &fundada,
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
			control, err := b.claimTerritory(ctx, tx, req.TerritoryID, p.ID, req.Tick, req.Now)
			if err != nil {
				return err
			}
			result.TerritoryControl = control
		}

		if err := appendEvent(ctx, tx, worldEvent{
			EventType: "PlayerBootstrapped",
			Tick:      req.Tick,
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
	ctx context.Context, tx pgx.Tx, territoryID int64, playerID uuid.UUID, tick uint64, now time.Time,
) (*territory.Control, error) {
	actual, err := b.territories.GetControl(ctx, tx, territoryID)
	if err != nil {
		return nil, fmt.Errorf("leer el control del territorio %d: %w", territoryID, err)
	}
	if actual.OwnerType != territory.OwnerNone {
		return nil, nil
	}

	control, err := b.territories.ClaimForPlayer(ctx, tx, territoryID, playerID, now.UTC(), actual.Version)
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
