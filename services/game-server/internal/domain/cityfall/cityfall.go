// Package cityfall tiene las reglas puras de la caída de una ciudad
// (SPEC-CITY-FALL, ADR-050): qué ciudad se puede asaltar, el asalto como
// encargo con plazo y los valores por defecto. No tiene estado ni toca la base.
package cityfall

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Valores por defecto (SPEC-CITY-FALL Q1, Q6 y Q7).
const (
	// DefaultAssaultDuration es EO_CITY_ASSAULT_SECONDS.
	DefaultAssaultDuration = 15 * time.Minute
	// DefaultImmunity es EO_CITY_FALL_IMMUNITY_HOURS.
	DefaultImmunity = 72 * time.Hour
	// DefaultRefoundMinDistance es EO_CITY_REFOUND_MIN_DISTANCE, en tiles de
	// Chebyshev entre centros.
	DefaultRefoundMinDistance int32 = 64
)

// Motivos con los que termina un asalto sin caída (end_reason).
const (
	// EndUnit: la unidad murió, se movió o recibió otra orden.
	EndUnit = "UNIT"
	// EndBlocked: la paz, o el asaltante ausente o protegido.
	EndBlocked = "BLOCKED"
	// EndWalled: la ciudad volvió a estar amurallada.
	EndWalled = "WALLED"
	// EndConflict: la base rechazó la caída.
	EndConflict = "CONFLICT"
)

// Rechazos de RN-SIEGE-010, con el motivo que lee el jugador.
var (
	ErrOwnCity  = errors.New("no se asalta la ciudad propia")
	ErrWalled   = errors.New("la muralla está en pie: primero hay que abrirla")
	ErrStoneAge = errors.New("una ciudad de la Edad de Piedra no puede caer")
	ErrImmune   = errors.New("la ciudad es recién fundada: aún no se la puede asaltar")
	ErrBusy     = errors.New("ya hay un asalto sobre esa ciudad")
)

// Target es lo que la simulación sabe de la ciudad asaltada al decidir.
type Target struct {
	City *city.City
	// Walled: tiene una muralla COMPLETE con vida (RN-SIEGE-002).
	Walled bool
	// Busy: ya tiene un asalto activo, o está cayendo (INV-CITY-021).
	Busy bool
}

// CheckTarget aplica RN-SIEGE-010 y RN-CITY-029 en orden fijo: ciudad propia,
// amurallada, de la Edad de Piedra, inmune y ocupada. La paz y la protección
// son del predicado de agresión de la simulación (RN-COMBAT-041).
func CheckTarget(t Target, attacker uuid.UUID, nowMs int64, immunity time.Duration) error {
	switch {
	case t.City.OwnerPlayerID == attacker:
		return ErrOwnCity
	case t.Walled:
		return ErrWalled
	case t.City.Era == city.EraStone:
		return ErrStoneAge
	case t.City.FallImmuneAt(nowMs, immunity):
		return ErrImmune
	case t.Busy:
		return ErrBusy
	}
	return nil
}

// Assault es un asalto: una unidad sostiene el estandarte en un tile del anillo
// de una ciudad hasta EndsAtMs (SPEC-CITY-FALL §4.1).
type Assault struct {
	ID       int64
	CityID   int64
	UnitID   int64
	PlayerID uuid.UUID
	// FromCityID es la ciudad del asaltante: la otra clave de la caída.
	FromCityID  int64
	X, Y        int32
	StartedAtMs int64
	EndsAtMs    int64
	RequestID   string
}

// Tile es donde se sostiene el estandarte.
func (a Assault) Tile() world.Tile { return world.Tile{X: a.X, Y: a.Y} }

// Due dice si el plazo venció en nowMs.
func (a Assault) Due(nowMs int64) bool { return nowMs >= a.EndsAtMs }

// FarFromAll dice si candidate está a ≥ minDist de Chebyshev de cada tile de
// avoid: la condición extra de la refundación (RN-CITY-027).
func FarFromAll(candidate world.Tile, avoid []world.Tile, minDist int32) bool {
	for _, a := range avoid {
		if chebyshev(candidate, a) < minDist {
			return false
		}
	}
	return true
}

func chebyshev(a, b world.Tile) int32 {
	dx, dy := a.X-b.X, a.Y-b.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}

// Site es el emplazamiento de la ciudad refundada: su centro, dónde nacen sus
// tres aldeanos y el territorio que la contiene (0 si ninguno).
type Site struct {
	Center      world.Tile
	Spawns      []world.Tile
	TerritoryID int64
}

// PlaceFunc busca el sitio de la ciudad refundada a la vista de los centros de
// las ciudades en pie, sin la caída. Se llama fuera de la transacción.
type PlaceFunc func(existing []world.Tile) (Site, error)

// Fall es la caída de una ciudad tal como la decide la simulación al vencer el
// plazo de su asalto (ADR-050 §3).
type Fall struct {
	AssaultID int64
	// CityID es la ciudad caída, de OwnerID; VictorID y VictorCityID, quien la
	// asaltó y su ciudad.
	CityID       int64
	OwnerID      uuid.UUID
	VictorID     uuid.UUID
	VictorCityID int64
	// Name y Era son los de la caída: la refundada nace en su era con el nombre
	// base de su dueño (RN-CITY-027, RN-CITY-046) y la tomada conserva Name
	// salvo que el vencedor ya tenga otra que lo lleve (RN-CITY-045). Name es
	// también la semilla de la espiral de la refundación.
	Name   string
	Era    city.Era
	Center world.Tile
	Tick   uint64
	Now    time.Time
	AtMs   int64
	// War son los puntos que la caída suma a una guerra entre clanes, o nil
	// (ADR-045 §2): viajan en el evento de la caída.
	War *clan.WarPoints
	// Place busca el sitio y Verify lo vuelve a comprobar bajo el cerrojo de
	// fundación, como en el alta.
	Place  PlaceFunc
	Verify func(center world.Tile, existing []world.Tile) bool
	// TakeSpawns son los tiles de los tres aldeanos de la ciudad tomada, si el
	// vencedor tiene hueco (SPEC-CITY-TAKEN RN-CITY-037); vacío, no se toma.
	TakeSpawns []world.Tile
}

// FallResult es lo que la transacción de la caída escribió y la RAM tiene que
// aplicar en un solo comando.
type FallResult struct {
	// City es la ciudad nueva del vencido y Villagers sus tres aldeanos.
	City      *city.City
	Villagers []*unit.Unit
	// Freed es el control del territorio capital de la caída, ya libre; nil si
	// la caída no era dueña de su territorio. Claimed es el del territorio de
	// la ciudad nueva si la fundación lo ganó (RN-TERR-007).
	Freed   *territory.Control
	Claimed *territory.Control
	// ExpiredTrades son los tratos pendientes que caducaron, y
	// WithdrawnMarket las ofertas del mercado que se retiraron (su custodia la
	// acredita la RAM a la ciudad nueva); RedirectedCaravans, las caravanas
	// que iban a la caída y ahora van a la nueva.
	ExpiredTrades      []int64
	WithdrawnMarket    []int64
	RedirectedCaravans []int64
	// KeptCityID es la otra ciudad en pie del vencido cuando la caída no refunda
	// (SPEC-MULTI-CITY RN-CITY-035, INV-CITY-023): lo de la caída pasa a ella, y
	// City y Villagers quedan vacíos. KeptPopulation y KeptPopulationLimit son su
	// recuento tras recibirlo (el bono de territorio, si pasa a ser la capital).
	KeptCityID          int64
	KeptPopulation      int32
	KeptPopulationLimit int32

	// SPEC-CITY-TAKEN (ADR-052): la ciudad que tomó el vencedor, sus aldeanos,
	// los edificios que heredó de la caída y el territorio que ganó; Taken nil si
	// se arrasó.
	Taken          *city.City
	TakenVillagers []*unit.Unit
	TakenBuildings []int64
	TakenClaimed   *territory.Control
}

// ─────────────────────────────────────────────────────────────
// La fundación de una segunda ciudad (SPEC-MULTI-CITY, ADR-051 §4)
// ─────────────────────────────────────────────────────────────

// FoundVillagers son los aldeanos que nacen con la ciudad fundada, además del
// fundador (RN-CITY-032).
const FoundVillagers = 2

// Founding es la fundación de una segunda ciudad por un aldeano.
type Founding struct {
	OwnerID    uuid.UUID
	FromCityID int64
	UnitID     int64
	// Era es la de la ciudad que paga. El nombre lo elige la transacción con
	// las ciudades en pie del dueño (SPEC-CITY-NAMES RN-CITY-044).
	Era  city.Era
	Tick uint64
	Now  time.Time
	AtMs int64
	// Place comprueba el sitio elegido contra las ciudades en pie (fuera de la
	// transacción, con la capa de madera) y Verify lo vuelve a comprobar bajo el
	// cerrojo de fundación.
	Place  PlaceFunc
	Verify func(center world.Tile, existing []world.Tile) bool
}

// FoundingResult es lo que la transacción de la fundación escribió.
type FoundingResult struct {
	// City es la ciudad nueva y Villagers los dos aldeanos que nacieron con ella.
	City      *city.City
	Villagers []*unit.Unit
	// FromPopulation es la población de la ciudad que pagó, sin el fundador.
	FromPopulation int32
	// Claimed es el control de su territorio, si lo ganó (RN-TERR-007).
	Claimed *territory.Control
}

// ErrFoundRejected: la fundación ya no cabe al confirmarse (el tope, el sitio,
// el territorio o el aldeano cambiaron mientras iba en vuelo).
var ErrFoundRejected = errors.New("la fundación ya no es posible")
