// Package production modela el entrenamiento de unidades en una ciudad.
//
// Una producción por ciudad, sin cola ni cancelación (RN-ECON-012). Es dominio
// puro: decide si se puede entrenar, nada más.
//
// Spec: ../../../../../docs/specs/economy-wood.md §4.4 · ADR-023 §3.
package production

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

// Errores de dominio.
var (
	// ErrCityNotFound: la ciudad no existe o no es del jugador (CITY_NOT_FOUND).
	ErrCityNotFound = errors.New("ciudad no encontrada")
	// ErrCityBusy: la ciudad ya entrena algo (CITY_BUSY).
	ErrCityBusy = errors.New("la ciudad ya tiene una producción en curso")
	// ErrNotTrainable: el tipo no se entrena en una ciudad.
	ErrNotTrainable = errors.New("el tipo de unidad no se entrena")
	// ErrNotYourCivilization: una unidad exclusiva que no es la de la
	// civilización del dueño de la ciudad (INVALID_TARGET, RN-CIVUNIT-002).
	ErrNotYourCivilization = errors.New("la unidad es de otra civilización")
	// ErrEraRequired: la ciudad no ha llegado a la era que pide el tipo
	// (INVALID_TARGET, RN-CIVUNIT-002).
	ErrEraRequired = errors.New("la ciudad no ha llegado a la era que pide la unidad")
)

// Civilization es lo que el entrenamiento sabe de la civilización del dueño de
// la ciudad: su unidad exclusiva y su bien (civilizations.unit y .good). El
// dominio no pregunta por su código (RN-PLAYER-012).
type Civilization struct {
	Unit unit.Type
	Good economy.Resource
}

// Production es el entrenamiento de una ciudad.
type Production struct {
	CityID        int64
	UnitType      unit.Type
	CompletesAtMs int64
	RequestID     string
	// UnitID es la unidad que produjo: 0 mientras está en curso. La
	// transacción del nacimiento la apunta, y es lo que la hace idempotente.
	UnitID int64
}

// InProgress indica si todavía no ha nacido su unidad: es lo que cuenta como
// población reservada (INV-CITY-017).
func (p *Production) InProgress() bool { return p.UnitID == 0 }

// Due indica si su entrenamiento venció en `nowMs` y falta el nacimiento.
func (p *Production) Due(nowMs int64) bool { return p.InProgress() && p.CompletesAtMs <= nowMs }

// CanTrain aplica RN-ECON-010 y RN-CIVUNIT-002 en el orden de la spec
// (SPEC-CIVILIZATION-UNITS §5): ciudad propia, tipo entrenable, la civilización
// si es exclusivo, la era, sin producción en curso, plaza de población y
// recursos. Devuelve la definición, que lleva el tiempo, y el coste completo con
// el bien de la civilización (ADR-043 §3).
//
// `busy` dice si la ciudad tiene una producción en curso o encargada o avanza
// de era; `stock` devuelve el stock de un recurso, o nil si no lo tiene.
func CanTrain(
	c *city.City, requester uuid.UUID, t unit.Type, civ Civilization, busy bool, stock func(economy.Resource) *economy.Stock,
) (unit.Definition, economy.Costs, error) {
	if c == nil || c.OwnerPlayerID != requester {
		return unit.Definition{}, nil, ErrCityNotFound
	}
	def, err := unit.Lookup(t)
	if err != nil {
		return unit.Definition{}, nil, err
	}
	if !def.CanTrain() {
		return unit.Definition{}, nil, fmt.Errorf("%w: %s", ErrNotTrainable, t)
	}
	// INV-UNIT-012: una exclusiva, sólo la de la civilización del dueño.
	if def.Exclusive && def.Type != civ.Unit {
		return unit.Definition{}, nil, fmt.Errorf("%w: %s", ErrNotYourCivilization, t)
	}
	if def.RequiresEra != "" && !c.Era.AtLeast(def.RequiresEra) {
		return unit.Definition{}, nil, fmt.Errorf("%w: %s pide %s", ErrEraRequired, t, def.RequiresEra)
	}
	if busy {
		return unit.Definition{}, nil, ErrCityBusy
	}
	// La plaza se reserva al ordenar (RN-ECON-010): por eso se pregunta aquí y
	// no al nacer.
	if !c.HasPopulationRoom(def.PopulationCost) {
		return unit.Definition{}, nil, city.ErrPopulationLimit
	}
	costs := def.CostsWith(civ.Good)
	if def.OwnGoodCost > 0 && !civ.Good.IsGood() {
		// Sin bien conocido no hay con qué pagar: sólo pasa sin catálogo de
		// civilizaciones, que la carga no permite en producción.
		return unit.Definition{}, nil, economy.ErrInsufficientResources
	}
	if stock == nil || !costs.CanAfford(stock) {
		return unit.Definition{}, nil, economy.ErrInsufficientResources
	}
	return def, costs, nil
}
