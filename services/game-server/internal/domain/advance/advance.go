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
	// ForeignGoods son los bienes ajenos que pide además, en cualquier mezcla
	// (SPEC-CIVILIZATION-GOODS §4.4); 0 si ninguno. Resolve los convierte en
	// costes concretos.
	ForeignGoods int64
}

// catalog son los pasos disponibles. Parámetros de equilibrio
// (SPEC-ECON-STONE Q5, SPEC-CIVILIZATION-GOODS Q5): se ajustan aquí sin tocar
// la spec. Desde IRON_AGE no hay avance (Q11).
var catalog = map[city.Era]Step{
	city.EraStone: {
		From: city.EraStone, To: city.EraBronze,
		Costs: economy.Costs{
			{Resource: economy.ResourceWood, Amount: 400},
			{Resource: economy.ResourceStone, Amount: 200},
		},
		DurationMs: 90_000,
	},
	city.EraBronze: {
		From: city.EraBronze, To: city.EraIron,
		Costs: economy.Costs{
			{Resource: economy.ResourceWood, Amount: 800},
			{Resource: economy.ResourceStone, Amount: 400},
		},
		ForeignGoods: 60,
		DurationMs:   180_000,
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

// Resolve convierte los bienes ajenos de un paso en costes concretos
// (ADR-036 §5): los bienes de `order` que no son `own`, en ese orden, tomando
// de cada uno lo que haya hasta completar ForeignGoods. El propio nunca entra
// (INV-ECON-011). Devuelve los costes fijos más los de los bienes, o
// ErrInsufficientResources si no llega.
func Resolve(step Step, own economy.Resource, order []economy.Resource, stock func(economy.Resource) *economy.Stock) (economy.Costs, error) {
	out := append(economy.Costs(nil), step.Costs...)
	left := step.ForeignGoods
	for _, g := range order {
		if left <= 0 {
			break
		}
		if g == own || !g.IsGood() {
			continue
		}
		var have int64
		if st := stock(g); st != nil {
			have = st.Amount
		}
		take := min(have, left)
		if take <= 0 {
			continue
		}
		out = append(out, economy.Cost{Resource: g, Amount: take})
		left -= take
	}
	if left > 0 {
		return nil, fmt.Errorf("%w: faltan %d bienes de otras civilizaciones", economy.ErrInsufficientResources, left)
	}
	return out, nil
}

// CanAdvance aplica RN-ECON-024 en el orden de la spec: ciudad propia, avance
// disponible desde su era, ciudad libre y stock suficiente en CADA recurso del
// coste. `busy` dice si la ciudad entrena o avanza ya; `stock` da el stock de
// un recurso de la ciudad. `own` es el bien de la civilización de la ciudad y
// `order` el de todas en el orden del catálogo: el paso devuelto lleva los
// bienes ajenos ya resueltos en Costs (RN-GOODS-006).
func CanAdvance(c *city.City, requester uuid.UUID, busy bool, own economy.Resource, order []economy.Resource, stock func(economy.Resource) *economy.Stock) (Step, error) {
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
	if step.ForeignGoods > 0 {
		costs, err := Resolve(step, own, order, stock)
		if err != nil {
			return Step{}, err
		}
		step.Costs = costs
	}
	return step, nil
}
