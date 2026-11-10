// Package building modela los edificios de una ciudad.
//
// En el primer slice de economía hay uno, la casa, y vive dentro de la zona
// urbana 3×3: no toca el pathfinding, porque esa zona ya es muralla en la capa
// de ocupación. Es dominio puro: decide si se puede construir y qué aporta, sin
// escribir ni conocer el protocolo.
//
// Spec: ../../../../../docs/specs/economy-wood.md §4.3.
package building

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Type identifica un tipo de edificio del catálogo.
type Type string

// Tipos de edificio del catálogo.
const (
	// TypeHouse es la casa del primer slice (SPEC-ECON-WOOD).
	TypeHouse Type = "HOUSE"
	// TypeWorkshop es el taller, que elabora el bien de la civilización de su
	// dueño (SPEC-CIVILIZATION-GOODS §4.2).
	TypeWorkshop Type = "WORKSHOP"
)

// Status es el estado de un edificio.
type Status string

const (
	StatusUnderConstruction Status = "UNDER_CONSTRUCTION"
	StatusComplete          Status = "COMPLETE"
	// StatusDestroyed es una casa derribada por un asedio (SPEC-COMBAT-SIEGE).
	// Conserva su fila, no aporta y no se carga al arrancar (ADR-029 §6).
	StatusDestroyed Status = "DESTROYED"
)

// Valid indica si el estado pertenece al conjunto del esquema
// (buildings_status_valid).
func (s Status) Valid() bool {
	return s == StatusUnderConstruction || s == StatusComplete || s == StatusDestroyed
}

// Definition son las propiedades inmutables de un tipo de edificio.
type Definition struct {
	Type Type
	// Costs se debita en la transacción que inserta el edificio (RN-ECON-008),
	// en uno o varios recursos (ADR-036 §3).
	Costs economy.Costs
	// BuildMs es lo que tarda en completarse desde el encargo.
	BuildMs int64
	// PopulationBonus es lo que suma al population_limit de su ciudad una vez
	// completo (RN-ECON-009).
	PopulationBonus int32
	// MaxHP es su vida entera frente a un asedio (SPEC-COMBAT-SIEGE §4.2).
	MaxHP int32
	// RequiresEra es la era mínima de la ciudad; "" si no pide ninguna.
	RequiresEra city.Era
	// UniquePerCity: como mucho uno en pie por ciudad (INV-ECON-010).
	UniquePerCity bool
}

// catalog vive en código, como el de unidades (SPEC-ECON-WOOD §4.5). Los
// valores son parámetros de equilibrio.
var catalog = map[Type]Definition{
	TypeHouse: {
		Type:            TypeHouse,
		Costs:           economy.Costs{{Resource: economy.ResourceWood, Amount: 30}},
		BuildMs:         15_000,
		PopulationBonus: 5,
		MaxHP:           150,
	},
	// SPEC-CIVILIZATION-GOODS Q6.
	TypeWorkshop: {
		Type: TypeWorkshop,
		Costs: economy.Costs{
			{Resource: economy.ResourceWood, Amount: 100},
			{Resource: economy.ResourceStone, Amount: 50},
		},
		BuildMs:       45_000,
		MaxHP:         200,
		RequiresEra:   city.EraBronze,
		UniquePerCity: true,
	},
}

// Errores de dominio.
var (
	ErrUnknownType = errors.New("tipo de edificio desconocido")
	// ErrCityNotFound: la ciudad no existe o no es del jugador. Misma respuesta
	// en los dos casos, para no revelar ciudades ajenas (CITY_NOT_FOUND).
	ErrCityNotFound = errors.New("ciudad no encontrada")
	// ErrInvalidSite: el tile no admite el edificio (INVALID_TARGET).
	ErrInvalidSite     = errors.New("el tile no admite el edificio")
	ErrAlreadyDone     = errors.New("el edificio ya está completo")
	ErrInvalidBuilding = errors.New("edificio inválido")
)

// Lookup devuelve la definición de un tipo de edificio.
func Lookup(t Type) (Definition, error) {
	d, ok := catalog[t]
	if !ok {
		return Definition{}, fmt.Errorf("%w: %q", ErrUnknownType, t)
	}
	d.Costs = append(economy.Costs(nil), d.Costs...)
	return d, nil
}

// Building es un edificio del mundo.
type Building struct {
	ID            int64
	CityID        int64
	Type          Type
	X, Y          int32
	Status        Status
	CompletesAtMs int64
}

// Tile devuelve el tile que ocupa.
func (b *Building) Tile() world.Tile { return world.Tile{X: b.X, Y: b.Y} }

// IsComplete indica si ya aporta su bono.
func (b *Building) IsComplete() bool { return b.Status == StatusComplete }

// Due indica si su construcción venció en `nowMs` y falta completarlo.
func (b *Building) Due(nowMs int64) bool {
	return b.Status == StatusUnderConstruction && b.CompletesAtMs <= nowMs
}

// Complete lo da por terminado. Un edificio completo no vuelve a completarse:
// es lo que impide sumar su bono dos veces (INV-ECON-004).
func (b *Building) Complete() error {
	if b.Status != StatusUnderConstruction {
		return fmt.Errorf("%w: edificio %d en %s", ErrAlreadyDone, b.ID, b.Status)
	}
	b.Status = StatusComplete
	return nil
}

// IsStanding indica si sigue en pie: en obra o completo.
func (b *Building) IsStanding() bool { return b.Status != StatusDestroyed }

// Destroy la derriba (SPEC-COMBAT-SIEGE RN-COMBAT-033) y dice si estaba
// completa, que es lo que decide si su bono sale del límite. DESTROYED es
// terminal: una casa derribada no vuelve a completarse ni se derriba dos veces.
func (b *Building) Destroy() (wasComplete bool, err error) {
	if b.Status == StatusDestroyed {
		return false, fmt.Errorf("%w: edificio %d ya derribado", ErrInvalidBuilding, b.ID)
	}
	wasComplete = b.Status == StatusComplete
	b.Status = StatusDestroyed
	return wasComplete, nil
}

// Validate comprueba una fila leída de la base contra el catálogo.
func (b *Building) Validate() error {
	if _, err := Lookup(b.Type); err != nil {
		return err
	}
	if !b.Status.Valid() {
		return fmt.Errorf("%w: estado %q", ErrInvalidBuilding, b.Status)
	}
	return nil
}

// CanPlace aplica la parte de RN-ECON-007 que depende del sitio: el tile está
// dentro de la zona urbana 3×3 de la ciudad, no es su centro y no tiene ya un
// edificio. `occupied` dice si un tile ya tiene edificio, confirmado o
// encargado.
func CanPlace(c *city.City, t world.Tile, occupied func(world.Tile) bool) error {
	if c == nil {
		return ErrCityNotFound
	}
	if !c.Occupies(t.X, t.Y) {
		return fmt.Errorf("%w: %s está fuera de la zona urbana", ErrInvalidSite, t)
	}
	if t == c.Center() {
		return fmt.Errorf("%w: el centro es del Centro Urbano", ErrInvalidSite)
	}
	if occupied != nil && occupied(t) {
		return fmt.Errorf("%w: %s ya tiene un edificio", ErrInvalidSite, t)
	}
	return nil
}

// CanBuild aplica RN-ECON-007 completa, en el orden en que responde: ciudad
// propia, tipo del catálogo, era y único por ciudad (RN-GOODS-003), sitio y
// recursos. Devuelve la definición, que lleva los costes y el tiempo.
// `standing` dice si la ciudad tiene ya en pie, o encargado, un edificio del
// tipo; `stock` da el stock de un recurso.
//
// La propiedad va primero: un jugador no puede deducir nada de una ciudad
// ajena por el error que recibe.
func CanBuild(
	c *city.City, requester uuid.UUID, t Type, tile world.Tile,
	occupied func(world.Tile) bool, standing func(Type) bool, stock func(economy.Resource) *economy.Stock,
) (Definition, error) {
	if c == nil || c.OwnerPlayerID != requester {
		return Definition{}, ErrCityNotFound
	}
	def, err := Lookup(t)
	if err != nil {
		return Definition{}, err
	}
	if def.RequiresEra != "" && !c.Era.AtLeast(def.RequiresEra) {
		return Definition{}, fmt.Errorf("%w: %s pide %s", ErrInvalidSite, t, def.RequiresEra)
	}
	if def.UniquePerCity && standing != nil && standing(t) {
		return Definition{}, fmt.Errorf("%w: la ciudad ya tiene %s", ErrInvalidSite, t)
	}
	if err := CanPlace(c, tile, occupied); err != nil {
		return Definition{}, err
	}
	if stock == nil || !def.Costs.CanAfford(stock) {
		return Definition{}, economy.ErrInsufficientResources
	}
	return def, nil
}

// PopulationBonus es lo que suman al límite de población los edificios
// completos de la lista.
func PopulationBonus(bs []Building) int32 {
	var bonus int32
	for i := range bs {
		if !bs[i].IsComplete() {
			continue
		}
		if def, err := Lookup(bs[i].Type); err == nil {
			bonus += def.PopulationBonus
		}
	}
	return bonus
}

// PopulationLimit es RN-ECON-009 e INV-ECON-004: el population_cap de la era
// más el bono de los edificios completos y el de los territorios conquistados
// (SPEC-TERRITORY-CONQUEST §4.2, territory.ConqueredBonus). Es el único sitio
// donde se calcula.
func PopulationLimit(eraCap int32, bs []Building, territoryBonus int32) int32 {
	return eraCap + PopulationBonus(bs) + territoryBonus
}
