// Package forest modela la madera que le queda a un tile de bosque.
//
// El terreno no cambia nunca: un tile agotado sigue siendo FOREST. La madera
// vive en una capa aparte y dispersa, sólo para los tiles que alguien ha
// empezado a talar; un tile sin entrada está lleno (ADR-025 §1). Este paquete
// es puro: valores y reglas, sin RAM ni base de datos.
//
// Spec: ../../../../../docs/specs/economy-forest.md.
package forest

import (
	"errors"
	"fmt"
)

// Parámetros de catálogo (SPEC-ECON-FOREST §4.1 y Q7). Son de equilibrio: se
// ajustan probando sin tocar la spec, como los de unit.Definition.
const (
	// Capacity es la madera de un tile lleno.
	Capacity int32 = 100
	// RegrowAfterMs es cuánto tarda un tile en volver a estar lleno desde su
	// última carga (24 h).
	RegrowAfterMs int64 = 24 * 60 * 60 * 1000
	// SearchRadius es hasta dónde busca un aldeano el tile siguiente cuando el
	// suyo se agota (RN-ECON-015).
	SearchRadius int32 = 6
	// SearchAttempts acota cuántos candidatos prueba el pathfinding en esa
	// búsqueda: los más cercanos primero.
	SearchAttempts = 8
	// FoundingTiles es cuántos tiles con madera exige la fundación
	// (RN-ECON-017).
	FoundingTiles = 10
)

// ErrInvalid es una entrada de la capa fuera de rango (INV-ECON-007).
var ErrInvalid = errors.New("madera de bosque inválida")

// State es la madera de un tile tocado.
type State struct {
	// Remaining es la madera que le queda, entre 0 y Capacity.
	Remaining int32 `json:"remaining"`
	// RegrowsAtMs es el instante absoluto en que vuelve a estar lleno.
	RegrowsAtMs int64 `json:"regrowsAtMs"`
}

// RemainingAt es la madera del tile en `nowMs`: llena si la regeneración ya
// venció, aunque nadie haya limpiado todavía la entrada (RN-ECON-016).
func (s State) RemainingAt(nowMs int64) int32 {
	if s.RegrowsAtMs <= nowMs {
		return Capacity
	}
	return s.Remaining
}

// Regrown indica si la entrada ya no hace falta: el tile volvió a estar lleno.
func (s State) Regrown(nowMs int64) bool { return s.RegrowsAtMs <= nowMs }

// Take es una carga de hasta `carry` sobre un tile con `remaining` en `atMs`:
// devuelve lo que se lleva y cómo queda el tile (RN-ECON-013). Lo que se lleva
// es exactamente lo que pierde el tile (INV-ECON-008).
func Take(remaining, carry int32, atMs int64) (taken int32, after State) {
	taken = min(max(carry, 0), max(remaining, 0))
	return taken, State{Remaining: remaining - taken, RegrowsAtMs: atMs + RegrowAfterMs}
}

// Validate comprueba una entrada leída de la base (INV-ECON-007).
func (s State) Validate() error {
	if s.Remaining < 0 || s.Remaining > Capacity {
		return fmt.Errorf("%w: quedan %d de %d", ErrInvalid, s.Remaining, Capacity)
	}
	return nil
}
