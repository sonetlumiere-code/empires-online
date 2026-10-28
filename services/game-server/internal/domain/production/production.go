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
)

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

// CanTrain aplica RN-ECON-010 en el orden de la spec: ciudad propia, tipo
// entrenable, sin producción en curso, plaza de población y madera. Devuelve la
// definición, que lleva el coste y el tiempo.
//
// `busy` dice si la ciudad tiene una producción en curso o encargada.
func CanTrain(c *city.City, requester uuid.UUID, t unit.Type, busy bool, stock *economy.Stock) (unit.Definition, error) {
	if c == nil || c.OwnerPlayerID != requester {
		return unit.Definition{}, ErrCityNotFound
	}
	def, err := unit.Lookup(t)
	if err != nil {
		return unit.Definition{}, err
	}
	if !def.CanTrain() {
		return unit.Definition{}, fmt.Errorf("%w: %s", ErrNotTrainable, t)
	}
	if busy {
		return unit.Definition{}, ErrCityBusy
	}
	// La plaza se reserva al ordenar (RN-ECON-010): por eso se pregunta aquí y
	// no al nacer.
	if !c.HasPopulationRoom(def.PopulationCost) {
		return unit.Definition{}, city.ErrPopulationLimit
	}
	if stock == nil || !stock.CanAfford(def.TrainCost) {
		return unit.Definition{}, economy.ErrInsufficientResources
	}
	return def, nil
}
