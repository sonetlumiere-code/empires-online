package simulation

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/founding"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// La fundación de una segunda ciudad (SPEC-MULTI-CITY, ADR-051 §4): un aldeano
// propio y parado la funda en su tile. La orden se valida en el tick con lo que
// la RAM sabe, reserva lo pagado y encola city.found; la transacción vuelve a
// comprobar el tope, el aldeano y el sitio con la capa de madera, y al volver
// la ciudad entra en el mundo por el camino del alta.

// JobCityFound es el trabajo de la fundación (catálogo JobNames).
const JobCityFound = "city.found"

// Lo que cuesta fundar si la configuración no dice otra cosa (Q3): lo de pasar
// de Bronce a Hierro.
const (
	DefaultCityFoundWood  int64 = 800
	DefaultCityFoundStone int64 = 400
)

// FoundCity es unit.found_city.
type FoundCity struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	RequestID string
	UnitID    int64
}

func (FoundCity) commandType() string { return protocol.TypeUnitFoundCity }

// FoundResolved lo devuelve la cola: el resultado, o el código del rechazo.
type FoundResolved struct {
	PlayerID uuid.UUID
	Result   *cityfall.FoundingResult
	Code     string
}

func (FoundResolved) commandType() string { return "city.found_resolved" }

// CityFoundWrite es la fundación con el débito de la ciudad que paga.
type CityFoundWrite struct {
	Founding cityfall.Founding
	Stock    []StockDelta
}

// FoundSiteFunc comprueba fuera del tick el sitio elegido contra las ciudades
// en pie, con la capa de madera (RN-CITY-030), y devuelve sus tiles de
// nacimiento y su territorio.
type FoundSiteFunc func(ctx context.Context, center world.Tile, existing []world.Tile) (cityfall.Site, error)

// foundReservation es una fundación en vuelo: una por jugador.
type foundReservation struct {
	requestID  string
	fromCityID int64
	unitID     int64
	costs      economy.Costs
	carried    map[economy.Resource]int64
}

func (s *Simulation) cityFoundCosts() economy.Costs {
	wood, stone := s.deps.CityFoundWood, s.deps.CityFoundStone
	if wood <= 0 {
		wood = DefaultCityFoundWood
	}
	if stone <= 0 {
		stone = DefaultCityFoundStone
	}
	return economy.Costs{{Resource: economy.ResourceWood, Amount: wood}, {Resource: economy.ResourceStone, Amount: stone}}
}

// handleFoundCity valida unit.found_city (RN-CITY-030, RN-CITY-031), reserva lo
// pagado y encola la fundación.
func (s *Simulation) handleFoundCity(cmd FoundCity) {
	reject := func(code, msg string) { s.economyError(cmd.PlayerID, cmd.RequestID, code, msg) }

	u, ok := s.state.Unit(cmd.UnitID)
	if !ok {
		reject(protocol.CodeUnitNotFound, "la unidad no existe")
		return
	}
	if err := u.EnsureOwnedBy(cmd.PlayerID); err != nil {
		reject(protocol.CodeUnitNotOwned, "la unidad no te pertenece")
		return
	}
	switch u.Status {
	case unit.StatusDead:
		reject(protocol.CodeUnitDead, "la unidad está muerta")
		return
	case unit.StatusGarrisoned:
		reject(protocol.CodeUnitGarrisoned, "la unidad está guarnecida")
		return
	case unit.StatusIdle:
	default:
		reject(protocol.CodeUnitNotMovable, "el aldeano tiene que estar parado")
		return
	}
	if s.state.IsEntering(u.ID) {
		reject(protocol.CodeUnitNotMovable, "el aldeano está entrando en una ciudad")
		return
	}
	if u.Type != unit.TypeVillager {
		reject(protocol.CodeInvalidTarget, "sólo un aldeano funda una ciudad")
		return
	}
	if s.deps.CityFall == nil || s.deps.FoundSite == nil {
		reject(protocol.CodeInternalError, "la fundación no está disponible")
		return
	}
	if u.CityID == nil {
		reject(protocol.CodeInvalidTarget, "el aldeano no es de ninguna ciudad")
		return
	}
	from, ok := s.state.City(*u.CityID)
	if !ok {
		reject(protocol.CodeInvalidTarget, "la ciudad del aldeano no está en pie")
		return
	}
	if _, enVuelo := s.state.foundings[cmd.PlayerID]; enVuelo {
		reject(protocol.CodeInvalidTarget, "ya hay una fundación en vuelo")
		return
	}
	if n := len(s.state.CitiesOf(cmd.PlayerID)); n >= city.MaxStandingPerOwner {
		reject(protocol.CodeInvalidTarget, "ya tienes dos ciudades")
		return
	}
	if !from.Era.AtLeast(city.EraBronze) {
		reject(protocol.CodeInvalidTarget, "la ciudad del aldeano tiene que estar en la Edad de Bronce")
		return
	}
	if as, ok := s.state.cityFall.assaults[from.ID]; ok && as.falling {
		reject(protocol.CodeInvalidTarget, "la ciudad del aldeano está cayendo")
		return
	}

	// El sitio, con lo que sabe la RAM: el tile del aldeano, las reglas de
	// fundación, el territorio libre y fuera de una Safe Zone (§4.1). El bosque
	// agotado lo mira la cola.
	center := u.Tile()
	if code, msg := s.foundSiteBlocked(center); code != "" {
		reject(code, msg)
		return
	}

	costs := s.cityFoundCosts()
	eco := s.state.economyOf(from.ID)
	carried, deltas, ok := s.reserve(from, eco, costs)
	if !ok {
		reject(protocol.CodeInsufficientResources, "fundar cuesta 800 de madera y 400 de piedra de la ciudad del aldeano")
		return
	}
	s.state.foundings[cmd.PlayerID] = &foundReservation{
		requestID: cmd.RequestID, fromCityID: from.ID, unitID: u.ID, costs: costs, carried: carried,
	}
	s.broadcastStocks(from)

	now := s.deps.Clock.Now()
	capital, _ := s.state.CityOf(cmd.PlayerID)
	f := cityfall.Founding{
		OwnerID: cmd.PlayerID, FromCityID: from.ID, UnitID: u.ID,
		Name: city.SecondCityName(capital.Name), Era: from.Era,
		Tick: s.state.Tick(), Now: now, AtMs: now.UnixMilli(),
		Verify: founding.FarEnough,
	}
	w := CityFoundWrite{Founding: f, Stock: deltas}
	store, site, player := s.deps.CityFall, s.deps.FoundSite, cmd.PlayerID
	s.deps.Persister.Submit(Job{
		Name: JobCityFound,
		Key:  CityKey(from.ID),
		Key2: PlayerKey(cmd.PlayerID),
		Run: func(ctx context.Context) error {
			ww := w
			ww.Founding.Place = func(existing []world.Tile) (cityfall.Site, error) {
				return site(ctx, center, existing)
			}
			res, err := store.FoundCity(ctx, ww)
			switch {
			case err == nil:
				s.reinject(FoundResolved{PlayerID: player, Result: res})
				return nil
			case errors.Is(err, cityfall.ErrFoundRejected):
				s.reinject(FoundResolved{PlayerID: player, Code: protocol.CodeInvalidTarget})
				return nil
			case errors.Is(err, economy.ErrInsufficientResources):
				s.reinject(FoundResolved{PlayerID: player, Code: protocol.CodeInsufficientResources})
				return nil
			case errors.Is(err, ErrStaleStock):
				s.reinject(FoundResolved{PlayerID: player, Code: protocol.CodeInternalError})
				return nil
			}
			return err
		},
		OnPermanentFailure: func(err error) {
			s.deps.Log.Error("no se pudo escribir la fundación; vuelve lo reservado", "err", err, "player", player)
			s.reinject(FoundResolved{PlayerID: player, Code: protocol.CodeInternalError})
		},
	})
}

// foundSiteBlocked es la parte del sitio que la RAM puede decidir: las reglas de
// fundación sin la capa de madera, el territorio libre y fuera de una Safe Zone.
func (s *Simulation) foundSiteBlocked(center world.Tile) (string, string) {
	var centers []world.Tile
	s.state.EachCity(func(c *city.City) bool {
		centers = append(centers, c.Center())
		return true
	})
	if _, err := founding.CheckSite(s.state.World(), center, centers, cityfall.FoundVillagers, nil); err != nil {
		return protocol.CodeInvalidTarget, "ese sitio no sirve para fundar: el terreno, otra ciudad a menos de 24 tiles o sin bosque cerca"
	}
	if t, ok := s.state.TerritoryAt(center.X, center.Y); ok {
		if ctrl, ok := s.state.TerritoryControl(t.ID); ok && ctrl.OwnerType != territory.OwnerNone && ctrl.OwnerType != "" {
			return protocol.CodeInvalidTarget, "sólo se funda en un territorio libre"
		}
	}
	if _, ok := s.state.SafeZoneAt(center.X, center.Y); ok {
		return protocol.CodeInvalidTarget, "no se funda en una zona segura"
	}
	return "", ""
}

// handleFoundResolved incorpora la ciudad fundada al mundo o devuelve lo
// reservado (ADR-051 §4).
func (s *Simulation) handleFoundResolved(cmd FoundResolved) {
	r, ok := s.state.foundings[cmd.PlayerID]
	if !ok {
		return
	}
	delete(s.state.foundings, cmd.PlayerID)
	from, hayCiudad := s.state.City(r.fromCityID)
	if cmd.Code != "" || cmd.Result == nil || cmd.Result.City == nil {
		code := cmd.Code
		if code == "" {
			code = protocol.CodeInternalError
		}
		if hayCiudad {
			s.refund(s.state.economyOf(r.fromCityID), r.costs, r.carried)
			s.state.markStockDirty(r.fromCityID)
			s.broadcastStocks(from)
		}
		s.economyError(cmd.PlayerID, r.requestID, code, "no se pudo fundar la ciudad")
		return
	}

	res := cmd.Result
	nueva := res.City
	if capital, ok := s.state.CityOf(cmd.PlayerID); ok {
		// Lo del jugador que la RAM copia en cada ciudad (ADR-050, precisión de
		// 2026-11-23): la transacción no lo lee.
		nueva.Civilization, nueva.Faction = capital.Civilization, capital.Faction
		nueva.FactionJoin, nueva.Clan = capital.FactionJoin, capital.Clan
	}
	// El fundador pasa a la ciudad nueva (RN-CITY-032).
	if u, ok := s.state.Unit(r.unitID); ok {
		id := nueva.ID
		u.CityID = &id
	}
	if hayCiudad {
		from.Population = res.FromPopulation
	}
	s.handleIntroducePlayer(IntroducePlayer{
		City: nueva, Units: res.Villagers,
		BlockedMinX: nueva.CenterX - 1, BlockedMinY: nueva.CenterY - 1,
		BlockedMaxX: nueva.CenterX + 1, BlockedMaxY: nueva.CenterY + 1,
		TerritoryControl: res.Claimed,
	})
	if hayCiudad {
		pop := from.Population
		s.broadcastCity(from, protocol.CityUpdatePayload{Population: &pop})
	}
	// Lo ve en el acto, sin reconectar (como el vencido, ADR-050 §5).
	if s.deps.Resnapshot != nil {
		s.deps.Resnapshot(cmd.PlayerID)
	}
	s.deps.Log.Info("ciudad fundada", "city_id", nueva.ID, "owner", cmd.PlayerID, "from_city_id", r.fromCityID,
		"x", nueva.CenterX, "y", nueva.CenterY)
}
