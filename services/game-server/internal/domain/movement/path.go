// Package movement modela el movimiento de unidades como una POLILÍNEA TEMPORIZADA.
//
// Ésta es la decisión central del proyecto (ADR-011). En lugar de persistir la
// posición en cada tick, se persiste una sola vez la ruta completa con el instante
// exacto en que la unidad alcanza cada tile. La posición pasa a ser una función pura
// del par (polilínea, tiempo):
//
//	posición(T) = último waypoint cuyo tMs <= T - startTimeMs
//
// De ahí salen tres propiedades que el juego necesita:
//   - Recuperación tras un crash en O(1) por movimiento, sin replay de ticks.
//   - Cero escrituras por tick: la posición no se persiste mientras hay movimiento.
//   - El cliente puede interpolar con exactitud sin volver a preguntar al servidor.
package movement

import (
	"errors"
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

var (
	// ErrEmptyPath indica una polilínea vacía: un movimiento siempre tiene al menos el origen.
	ErrEmptyPath = errors.New("la polilínea está vacía")
	// ErrPathNotContiguous indica que dos waypoints consecutivos no son adyacentes.
	ErrPathNotContiguous = errors.New("la polilínea no es contigua en 8-vecindad")
	// ErrPathNotMonotonic indica que los tiempos de la polilínea no son crecientes.
	ErrPathNotMonotonic = errors.New("los tiempos de la polilínea no son estrictamente crecientes")
	// ErrPathNotWalkable indica que la polilínea atraviesa un tile intransitable.
	ErrPathNotWalkable = errors.New("la polilínea atraviesa un tile intransitable")
	// ErrInvalidOrigin indica que el primer waypoint no tiene tMs == 0.
	ErrInvalidOrigin = errors.New("el primer waypoint debe tener tMs = 0")
)

// Waypoint es un tile de la ruta junto al instante, relativo al inicio del
// movimiento, en el que la unidad LO ALCANZA.
type Waypoint struct {
	X   int32 `json:"x"`
	Y   int32 `json:"y"`
	TMs int64 `json:"tMs"`
}

// Tile devuelve la posición del waypoint.
func (w Waypoint) Tile() world.Tile { return world.Tile{X: w.X, Y: w.Y} }

// TimedPath es la polilínea temporizada completa. Nunca está vacía y su primer
// elemento es siempre el origen con TMs = 0.
type TimedPath []Waypoint

// DurationMs es el tiempo total del recorrido.
func (p TimedPath) DurationMs() int64 {
	if len(p) == 0 {
		return 0
	}
	return p[len(p)-1].TMs
}

// Origin devuelve el tile de partida.
func (p TimedPath) Origin() world.Tile { return p[0].Tile() }

// Destination devuelve el tile de llegada.
func (p TimedPath) Destination() world.Tile { return p[len(p)-1].Tile() }

// PositionAt devuelve la posición AUTORITATIVA en el instante indicado, expresado
// como offset en milisegundos desde el inicio del movimiento.
//
// El servidor razona siempre en tiles completos: una unidad nunca está "entre"
// dos tiles. La interpolación sub-tile es un asunto exclusivamente visual del
// cliente. Ver ../../../../../docs/architecture/frontend.md
func (p TimedPath) PositionAt(elapsedMs int64) world.Tile {
	if len(p) == 0 {
		return world.Tile{}
	}
	if elapsedMs <= 0 {
		return p[0].Tile()
	}
	if elapsedMs >= p[len(p)-1].TMs {
		return p[len(p)-1].Tile()
	}
	// Búsqueda binaria del último waypoint con TMs <= elapsedMs.
	lo, hi := 0, len(p)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if p[mid].TMs <= elapsedMs {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return p[lo].Tile()
}

// IndexAt devuelve el índice del waypoint alcanzado en el instante indicado.
func (p TimedPath) IndexAt(elapsedMs int64) int {
	if len(p) == 0 {
		return -1
	}
	if elapsedMs <= 0 {
		return 0
	}
	if elapsedMs >= p[len(p)-1].TMs {
		return len(p) - 1
	}
	lo, hi := 0, len(p)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if p[mid].TMs <= elapsedMs {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// CostGrid es lo mínimo que necesita el constructor de polilíneas.
type CostGrid interface {
	IsWalkable(x, y int32) bool
	MoveCostUnits(x, y int32) int32
}

// Numerador y denominador de √2 en punto fijo.
//
// La temporización usa aritmética entera para que dos ejecuciones idénticas
// produzcan exactamente los mismos milisegundos en cualquier plataforma. Con
// punto flotante el resultado sería casi siempre igual — y "casi siempre" no
// sirve para una simulación que debe ser reproducible.
const (
	sqrt2Num int64 = 1414214
	sqrt2Den int64 = 1000000
)

// StepDurationMs calcula lo que tarda una unidad en entrar en un tile.
//
//	duración = baseMsPerTile * (costUnits / 10) * (diagonal ? √2 : 1)
//
// Cada segmento se REDONDEA al milisegundo más cercano y sólo después se acumula.
// Redondear (y no truncar) evita que una ruta larga acumule un sesgo sistemático
// a favor de la unidad: mil pasos truncados regalarían casi un segundo.
// El redondeo es aritmética entera pura, así que sigue siendo determinista.
func StepDurationMs(baseMsPerTile int64, costUnits int32, diagonal bool) int64 {
	if costUnits <= 0 {
		return 0
	}
	base := int64(world.CostBase)
	ms := (baseMsPerTile*int64(costUnits) + base/2) / base
	if diagonal {
		ms = (ms*sqrt2Num + sqrt2Den/2) / sqrt2Den
	}
	if ms < 1 {
		ms = 1 // ningún paso puede ser instantáneo
	}
	return ms
}

// BuildTimedPath convierte una ruta de tiles en una polilínea temporizada.
//
// `tiles` debe empezar en el origen y ser contigua en 8-vecindad, tal y como la
// devuelve el pathfinder.
func BuildTimedPath(tiles []world.Tile, grid CostGrid, baseMsPerTile int64) (TimedPath, error) {
	if len(tiles) == 0 {
		return nil, ErrEmptyPath
	}
	if baseMsPerTile <= 0 {
		return nil, fmt.Errorf("baseMsPerTile debe ser positivo, recibido %d", baseMsPerTile)
	}

	path := make(TimedPath, 0, len(tiles))
	path = append(path, Waypoint{X: tiles[0].X, Y: tiles[0].Y, TMs: 0})

	var acc int64
	for i := 1; i < len(tiles); i++ {
		prev, cur := tiles[i-1], tiles[i]
		if !prev.IsAdjacent(cur) {
			return nil, fmt.Errorf("%w: %s -> %s", ErrPathNotContiguous, prev, cur)
		}
		if !grid.IsWalkable(cur.X, cur.Y) {
			return nil, fmt.Errorf("%w: %s", ErrPathNotWalkable, cur)
		}
		units := grid.MoveCostUnits(cur.X, cur.Y)
		acc += StepDurationMs(baseMsPerTile, units, prev.IsDiagonalTo(cur))
		path = append(path, Waypoint{X: cur.X, Y: cur.Y, TMs: acc})
	}
	return path, nil
}

// Validate comprueba los invariantes estructurales de una polilínea (INV-MOVE-002).
//
// Se ejecuta al construirla y también al REHIDRATARLA desde la base de datos: un
// movimiento corrupto en disco no debe poder teletransportar una unidad al arrancar.
func Validate(p TimedPath, grid CostGrid) error {
	if len(p) == 0 {
		return ErrEmptyPath
	}
	if p[0].TMs != 0 {
		return fmt.Errorf("%w: tMs = %d", ErrInvalidOrigin, p[0].TMs)
	}
	for i := range p {
		if grid != nil && !grid.IsWalkable(p[i].X, p[i].Y) {
			return fmt.Errorf("%w: waypoint %d en (%d,%d)", ErrPathNotWalkable, i, p[i].X, p[i].Y)
		}
		if i == 0 {
			continue
		}
		if p[i].TMs <= p[i-1].TMs {
			return fmt.Errorf("%w: waypoint %d tMs=%d no supera a %d", ErrPathNotMonotonic, i, p[i].TMs, p[i-1].TMs)
		}
		if !p[i-1].Tile().IsAdjacent(p[i].Tile()) {
			return fmt.Errorf("%w: waypoint %d %s -> %s", ErrPathNotContiguous, i, p[i-1].Tile(), p[i].Tile())
		}
	}
	return nil
}
