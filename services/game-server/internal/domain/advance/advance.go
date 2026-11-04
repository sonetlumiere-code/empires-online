// Package advance modela el avance de era de una ciudad: pagar y esperar para
// pasar a la era siguiente, que sube el límite de población.
//
// Es dominio puro: decide si se puede avanzar y cuánto cuesta; la reserva, la
// escritura y el fin son de la simulación (ADR-030 §4 y §5). Los
// `population_cap` de cada era NO están aquí: viven en la tabla `eras`
// (RN-CITY-015); aquí sólo está qué cuesta pasar de una a otra.
//
// Spec: ../../../../../docs/specs/economy-stone.md §4.4.
package advance

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
)

// Step es un paso de era: de dónde a dónde, qué cuesta y cuánto dura.
type Step struct {
	From       city.Era
	To         city.Era
	Costs      economy.Costs
	DurationMs int64
}

// catalog son los pasos disponibles. Parámetros de equilibrio
// (SPEC-ECON-STONE Q5): se ajustan aquí sin tocar la spec. Sólo existe el
// primero (Q6): desde BRONZE_AGE no hay avance.
var catalog = map[city.Era]Step{
	city.EraStone: {
		From: city.EraStone, To: city.EraBronze,
		Costs: economy.Costs{
			{Resource: economy.ResourceWood, Amount: 400},
			{Resource: economy.ResourceStone, Amount: 200},
		},
		DurationMs: 90_000,
	},
}

// Next es el paso que sale de una era; false si no hay ninguno.
func Next(from city.Era) (Step, bool) {
	s, ok := catalog[from]
	if !ok {
		return Step{}, false
	}
	s.Costs = append(economy.Costs(nil), s.Costs...)
	return s, true
}

// Errores de dominio. CITY_NOT_FOUND y CITY_BUSY son los mismos que los del
// entrenamiento: la misma ciudad, la misma plaza ocupada.
var (
	// ErrCityNotFound: la ciudad no existe o no es del jugador.
	ErrCityNotFound = production.ErrCityNotFound
	// ErrCityBusy: la ciudad entrena algo o ya está avanzando (RN-ECON-026).
	ErrCityBusy = production.ErrCityBusy
	// ErrUnavailable: no hay avance desde la era de la ciudad
	// (NOT_IMPLEMENTED, RN-ECON-024).
	ErrUnavailable = errors.New("no hay avance de era disponible desde esta era")
	// ErrInvalid: una fila de avance que no es un paso del catálogo.
	ErrInvalid = errors.New("avance de era inválido")
)

// Advance es el avance en curso de una ciudad.
type Advance struct {
	CityID        int64
	From          city.Era
	To            city.Era
	CompletesAtMs int64
	RequestID     string
}

// Due indica si el avance venció en `nowMs`.
func (a Advance) Due(nowMs int64) bool { return a.CompletesAtMs <= nowMs }

// Validate comprueba una fila leída de la base: tiene que ser un paso del
// catálogo (INV-CITY-018: nunca hacia atrás ni saltando).
func (a Advance) Validate() error {
	s, ok := catalog[a.From]
	if !ok || s.To != a.To {
		return fmt.Errorf("%w: de %s a %s", ErrInvalid, a.From, a.To)
	}
	return nil
}

// CanAdvance aplica RN-ECON-024 en el orden de la spec: ciudad propia, avance
// disponible desde su era, ciudad libre y stock suficiente en CADA recurso del
// coste. `busy` dice si la ciudad entrena o avanza ya; `stock` da el stock de
// un recurso de la ciudad.
func CanAdvance(c *city.City, requester uuid.UUID, busy bool, stock func(economy.Resource) *economy.Stock) (Step, error) {
	if c == nil || c.OwnerPlayerID != requester {
		return Step{}, ErrCityNotFound
	}
	step, ok := Next(c.Era)
	if !ok {
		return Step{}, fmt.Errorf("%w: %s", ErrUnavailable, c.Era)
	}
	if busy {
		return Step{}, ErrCityBusy
	}
	if !step.Costs.CanAfford(stock) {
		return Step{}, economy.ErrInsufficientResources
	}
	return step, nil
}
