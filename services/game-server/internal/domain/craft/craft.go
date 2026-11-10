// Package craft modela la elaboración del taller: pagar madera y piedra y, al
// cabo de un plazo, recibir el bien de la civilización de la ciudad.
//
// Es dominio puro: decide si se puede elaborar y qué cuesta; la reserva, la
// escritura y la entrega son de la simulación (ADR-036 §4). Qué bien sale lo
// decide el catálogo de civilizaciones (player.Civilization.Good), no este
// paquete.
//
// Spec: ../../../../../docs/specs/civilization-goods.md §4.3.
package craft

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
)

// Parámetros de equilibrio (SPEC-CIVILIZATION-GOODS Q7). La duración es la base:
// el rasgo production.speed de la civilización la escala al ordenar.
const (
	// Amount es lo que entrega una elaboración.
	Amount int32 = 5
	// DurationMs es lo que tarda, antes del rasgo.
	DurationMs int64 = 30_000
)

// Costs es lo que cuesta una elaboración.
func Costs() economy.Costs {
	return economy.Costs{
		{Resource: economy.ResourceWood, Amount: 30},
		{Resource: economy.ResourceStone, Amount: 15},
	}
}

// Errores de dominio. CITY_NOT_FOUND y CITY_BUSY son los del entrenamiento.
var (
	// ErrCityNotFound: la ciudad no existe o no es del jugador.
	ErrCityNotFound = production.ErrCityNotFound
	// ErrBusy: el taller ya elabora (RN-GOODS-004).
	ErrBusy = production.ErrCityBusy
	// ErrNoWorkshop: la ciudad no tiene un taller completo (INVALID_TARGET).
	ErrNoWorkshop = errors.New("la ciudad no tiene un taller completo")
	// ErrInvalid: una fila de elaboración que no vale.
	ErrInvalid = errors.New("elaboración inválida")
)

// Craft es la elaboración en curso de una ciudad.
type Craft struct {
	CityID        int64
	Good          economy.Resource
	Amount        int32
	CompletesAtMs int64
	RequestID     string
}

// Due indica si la elaboración venció en `nowMs`.
func (c Craft) Due(nowMs int64) bool { return c.CompletesAtMs <= nowMs }

// Validate comprueba una fila leída de la base: un bien y una cantidad
// positiva (INV-ECON-009).
func (c Craft) Validate() error {
	if !c.Good.IsGood() {
		return fmt.Errorf("%w: %q no es un bien", ErrInvalid, c.Good)
	}
	if c.Amount <= 0 {
		return fmt.Errorf("%w: cantidad %d", ErrInvalid, c.Amount)
	}
	return nil
}

// CanCraft aplica RN-GOODS-004 en el orden de la spec: ciudad propia, taller
// completo, taller libre y stock suficiente en cada recurso del coste.
func CanCraft(c *city.City, requester uuid.UUID, hasWorkshop, busy bool, stock func(economy.Resource) *economy.Stock) (economy.Costs, error) {
	if c == nil || c.OwnerPlayerID != requester {
		return nil, ErrCityNotFound
	}
	if !hasWorkshop {
		return nil, ErrNoWorkshop
	}
	if busy {
		return nil, ErrBusy
	}
	costs := Costs()
	if !costs.CanAfford(stock) {
		return nil, economy.ErrInsufficientResources
	}
	return costs, nil
}
