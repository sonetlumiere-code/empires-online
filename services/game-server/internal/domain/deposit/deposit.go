// Package deposit modela el recurso que le queda a un tile: la madera de un
// bosque o la piedra de una colina.
//
// El terreno no cambia nunca: un tile agotado sigue siendo FOREST o HILL. El
// recurso vive en una capa aparte y dispersa, sólo para los tiles que alguien
// ha empezado a gastar; un tile sin entrada está lleno (ADR-025 §1). Hay una
// capa por terreno, y el terreno del tile decide cuál: un tile es bosque o
// colina, nunca las dos cosas (ADR-030 §1). Este paquete es puro: valores y
// reglas, sin RAM ni base de datos.
//
// Specs: ../../../../../docs/specs/economy-forest.md y economy-stone.md.
package deposit

import (
	"errors"
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Layer describe la capa de un terreno: qué recurso da, cuánto tiene un tile
// lleno, cuándo vuelve a crecer y hasta dónde se busca el tile siguiente. Sus
// números son parámetros de catálogo (SPEC-ECON-FOREST Q7, SPEC-ECON-STONE Q7):
// se ajustan probando sin tocar la spec, como los de unit.Definition.
type Layer struct {
	// Name nombra la capa en logs y entidades ("forest", "hill").
	Name     string
	Terrain  world.TerrainType
	Resource economy.Resource
	// Capacity es el recurso de un tile lleno.
	Capacity int32
	// RegrowAfterMs es cuánto tarda un tile en volver a estar lleno desde su
	// última carga.
	RegrowAfterMs int64
	// SearchRadius es hasta dónde busca un aldeano el tile siguiente cuando el
	// suyo se agota (RN-ECON-015, RN-ECON-022).
	SearchRadius int32
	// SearchAttempts acota cuántos candidatos prueba el pathfinding en esa
	// búsqueda: los más cercanos primero.
	SearchAttempts int
}

const dayMs int64 = 24 * 60 * 60 * 1000

var (
	// Forest es la madera del bosque (SPEC-ECON-FOREST §4.1).
	Forest = Layer{
		Name: "forest", Terrain: world.Forest, Resource: economy.ResourceWood,
		Capacity: 100, RegrowAfterMs: dayMs, SearchRadius: 6, SearchAttempts: 8,
	}
	// Hill es la piedra de la colina (SPEC-ECON-STONE §4.3): los mismos
	// números que el bosque, porque la escasez viene de dónde está la colina y
	// no de cuánto da un tile (Q7).
	Hill = Layer{
		Name: "hill", Terrain: world.Hill, Resource: economy.ResourceStone,
		Capacity: 100, RegrowAfterMs: dayMs, SearchRadius: 6, SearchAttempts: 8,
	}
)

// FoundingForestTiles es cuántos tiles de bosque con madera exige la fundación
// (RN-ECON-017). La piedra no se garantiza (SPEC-ECON-STONE Q9).
const FoundingForestTiles = 10

// Layers son las capas, en orden estable.
func Layers() []Layer { return []Layer{Forest, Hill} }

// ForTerrain es la capa de un terreno; false si el terreno no da recurso.
func ForTerrain(t world.TerrainType) (Layer, bool) {
	for _, l := range Layers() {
		if l.Terrain == t {
			return l, true
		}
	}
	return Layer{}, false
}

// ForResource es la capa que da un recurso; false si ninguna lo da.
func ForResource(r economy.Resource) (Layer, bool) {
	for _, l := range Layers() {
		if l.Resource == r {
			return l, true
		}
	}
	return Layer{}, false
}

// ErrInvalid es una entrada de la capa fuera de rango (INV-ECON-007).
var ErrInvalid = errors.New("recurso de tile inválido")

// State es el recurso de un tile tocado.
type State struct {
	// Remaining es lo que le queda, entre 0 y la capacidad de su capa.
	Remaining int32 `json:"remaining"`
	// RegrowsAtMs es el instante absoluto en que vuelve a estar lleno.
	RegrowsAtMs int64 `json:"regrowsAtMs"`
}

// RemainingAt es el recurso del tile en `nowMs`: lleno si la regeneración ya
// venció, aunque nadie haya limpiado todavía la entrada (RN-ECON-016).
func (l Layer) RemainingAt(s State, nowMs int64) int32 {
	if s.Regrown(nowMs) {
		return l.Capacity
	}
	return s.Remaining
}

// Regrown indica si la entrada ya no hace falta: el tile volvió a estar lleno.
func (s State) Regrown(nowMs int64) bool { return s.RegrowsAtMs <= nowMs }

// Take es una carga de hasta `carry` sobre un tile con `remaining` en `atMs`:
// devuelve lo que se lleva y cómo queda el tile (RN-ECON-013, RN-ECON-021). Lo
// que se lleva es exactamente lo que pierde el tile (INV-ECON-008).
func (l Layer) Take(remaining, carry int32, atMs int64) (taken int32, after State) {
	taken = min(max(carry, 0), max(remaining, 0))
	return taken, State{Remaining: remaining - taken, RegrowsAtMs: atMs + l.RegrowAfterMs}
}

// Validate comprueba una entrada leída de la base (INV-ECON-007).
func (l Layer) Validate(s State) error {
	if s.Remaining < 0 || s.Remaining > l.Capacity {
		return fmt.Errorf("%w: quedan %d de %d en la capa %s", ErrInvalid, s.Remaining, l.Capacity, l.Name)
	}
	return nil
}
