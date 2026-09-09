package pathfinding

import (
	"context"
	"fmt"
	"sync"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// AStar es la implementación de referencia: A* sobre grid con 8 direcciones y
// heurística octile.
//
// Propiedades que el resto del sistema da por garantizadas:
//   - DETERMINISTA: ante costes empatados el desempate es estable por (f, h, y, x),
//     nunca por el orden de iteración de un map.
//   - ADMISIBLE: la heurística usa el coste MÍNIMO de terreno del mundo, así que
//     jamás sobreestima y A* devuelve una ruta óptima.
//   - SIN CORNER CUTTING: una diagonal sólo se permite si los dos tiles ortogonales
//     que la flanquean son transitables.
type AStar struct {
	defaultMaxNodes    int
	defaultMaxDistance int32
	pool               sync.Pool
}

// NewAStar crea el pathfinder con los límites por defecto de la configuración.
func NewAStar(maxNodes int, maxDistance int32) *AStar {
	if maxNodes <= 0 {
		maxNodes = 20000
	}
	if maxDistance <= 0 {
		maxDistance = 256
	}
	return &AStar{
		defaultMaxNodes:    maxNodes,
		defaultMaxDistance: maxDistance,
		pool:               sync.Pool{New: func() any { return &workspace{} }},
	}
}

// Vecindad en 8 direcciones. El orden es fijo y forma parte del determinismo:
// ante dos rutas de coste idéntico, se elige siempre la misma.
var neighbors = [8]struct{ dx, dy int32 }{
	{0, -1}, {1, 0}, {0, 1}, {-1, 0}, // ortogonales primero
	{1, -1}, {1, 1}, {-1, 1}, {-1, -1}, // diagonales después
}

// FindPath implementa Pathfinder.
func (a *AStar) FindPath(ctx context.Context, grid Grid, from, to world.Tile, opts Options) ([]world.Tile, error) {
	maxNodes := opts.MaxNodes
	if maxNodes <= 0 {
		maxNodes = a.defaultMaxNodes
	}
	maxDistance := opts.MaxDistance
	if maxDistance <= 0 {
		maxDistance = a.defaultMaxDistance
	}

	if !grid.InBounds(to.X, to.Y) {
		return nil, fmt.Errorf("%w: %s", ErrTargetOutOfBounds, to)
	}
	if !grid.InBounds(from.X, from.Y) {
		return nil, fmt.Errorf("%w: %s", ErrTargetOutOfBounds, from)
	}
	if !grid.IsWalkable(from.X, from.Y) {
		return nil, fmt.Errorf("%w: %s", ErrOriginNotWalkable, from)
	}
	if !grid.IsWalkable(to.X, to.Y) {
		return nil, fmt.Errorf("%w: %s", ErrTargetNotWalkable, to)
	}
	if chebyshev(from, to) > maxDistance {
		return nil, fmt.Errorf("%w: distancia %d > %d", ErrPathTooLong, chebyshev(from, to), maxDistance)
	}
	if from == to {
		return []world.Tile{from}, nil
	}

	width, height := grid.Width(), grid.Height()
	ws := a.pool.Get().(*workspace)
	defer a.pool.Put(ws)
	ws.reset(int(width) * int(height))

	minCost := grid.MinMoveCostUnits()
	if minCost <= 0 {
		minCost = 1
	}

	startIdx := int32(from.Y)*width + int32(from.X)
	goalIdx := int32(to.Y)*width + int32(to.X)

	ws.setG(startIdx, 0)
	ws.setFrom(startIdx, -1)
	ws.push(node{idx: startIdx, f: heuristic(from, to, minCost), h: heuristic(from, to, minCost), x: from.X, y: from.Y})

	expanded := 0
	for ws.heap.Len() > 0 {
		// Cancelación cooperativa: una consulta larga no debe sobrevivir a la
		// cancelación de su contexto.
		if expanded%512 == 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}

		cur := ws.pop()
		if ws.isClosed(cur.idx) {
			continue // entrada obsoleta del heap
		}
		ws.close(cur.idx)

		if cur.idx == goalIdx {
			return ws.reconstruct(startIdx, goalIdx, width), nil
		}

		expanded++
		if expanded > maxNodes {
			return nil, fmt.Errorf("%w: %d nodos expandidos supera el límite de %d", ErrPathTooLong, expanded, maxNodes)
		}

		curG := ws.g(cur.idx)
		for _, n := range neighbors {
			nx, ny := cur.x+n.dx, cur.y+n.dy
			if nx < 0 || ny < 0 || nx >= width || ny >= height {
				continue
			}
			nIdx := ny*width + nx
			if ws.isClosed(nIdx) {
				continue
			}
			if !grid.IsWalkable(nx, ny) {
				continue
			}

			diagonal := n.dx != 0 && n.dy != 0
			if diagonal {
				// Prohibido atajar esquinas: ambos tiles ortogonales adyacentes
				// deben ser transitables. Sin esto una unidad podría "colarse"
				// entre dos muros que se tocan en diagonal.
				if !grid.IsWalkable(cur.x+n.dx, cur.y) || !grid.IsWalkable(cur.x, cur.y+n.dy) {
					continue
				}
			}

			units := grid.MoveCostUnits(nx, ny)
			if units <= 0 {
				continue
			}
			step := units * costScaleOrtho
			if diagonal {
				step = units * costScaleDiag
			}

			tentative := curG + step
			if known, ok := ws.gIfSet(nIdx); ok && tentative >= known {
				continue
			}
			ws.setG(nIdx, tentative)
			ws.setFrom(nIdx, cur.idx)
			h := heuristic(world.Tile{X: nx, Y: ny}, to, minCost)
			ws.push(node{idx: nIdx, f: tentative + h, h: h, x: nx, y: ny})
		}
	}

	return nil, fmt.Errorf("%w: de %s a %s", ErrPathNotFound, from, to)
}

// heuristic es la distancia octile ponderada por el coste MÍNIMO de terreno.
//
// Usar el mínimo (y no el coste de la hierba) es lo que la mantiene admisible en
// un mundo con caminos más baratos que la hierba. Ver ADR-008 y pathfinding.md.
func heuristic(from, to world.Tile, minCostUnits int32) int32 {
	dx := abs32(to.X - from.X)
	dy := abs32(to.Y - from.Y)
	diag := dy
	if dx < dy {
		diag = dx
	}
	straight := dx + dy - 2*diag
	return minCostUnits*costScaleOrtho*straight + minCostUnits*costScaleDiag*diag
}

func chebyshev(a, b world.Tile) int32 {
	dx, dy := abs32(a.X-b.X), abs32(a.Y-b.Y)
	if dx > dy {
		return dx
	}
	return dy
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// ─────────────────────────────────────────────────────────────
// Espacio de trabajo reutilizable
// ─────────────────────────────────────────────────────────────

// workspace mantiene los arrays de A* entre consultas para evitar reasignar
// varios megabytes por cada petición. En lugar de limpiarlos, se marca cada
// entrada con el número de "generación" de la consulta actual: una entrada de
// una generación anterior se considera no visitada.
type workspace struct {
	generation uint32
	stamp      []uint32
	gScore     []int32
	cameFrom   []int32
	closed     []uint32
	heap       nodeHeap
}

func (w *workspace) reset(size int) {
	if cap(w.stamp) < size {
		w.stamp = make([]uint32, size)
		w.gScore = make([]int32, size)
		w.cameFrom = make([]int32, size)
		w.closed = make([]uint32, size)
		w.generation = 0
	}
	w.stamp = w.stamp[:size]
	w.gScore = w.gScore[:size]
	w.cameFrom = w.cameFrom[:size]
	w.closed = w.closed[:size]

	w.generation++
	if w.generation == 0 { // desbordamiento: limpieza completa, una vez cada 4.000 millones
		for i := range w.stamp {
			w.stamp[i] = 0
			w.closed[i] = 0
		}
		w.generation = 1
	}
	w.heap = w.heap[:0]
}

func (w *workspace) setG(idx, v int32) {
	w.stamp[idx] = w.generation
	w.gScore[idx] = v
}

func (w *workspace) g(idx int32) int32 { return w.gScore[idx] }

func (w *workspace) gIfSet(idx int32) (int32, bool) {
	if w.stamp[idx] != w.generation {
		return 0, false
	}
	return w.gScore[idx], true
}

func (w *workspace) setFrom(idx, from int32) { w.cameFrom[idx] = from }

func (w *workspace) close(idx int32) { w.closed[idx] = w.generation }

func (w *workspace) isClosed(idx int32) bool { return w.closed[idx] == w.generation }

func (w *workspace) push(n node) { w.heap.push(n) }

func (w *workspace) pop() node { return w.heap.pop() }

func (w *workspace) reconstruct(startIdx, goalIdx, width int32) []world.Tile {
	// Se recorre hacia atrás y se invierte: el resultado empieza SIEMPRE en el origen.
	var reversed []world.Tile
	for idx := goalIdx; ; idx = w.cameFrom[idx] {
		reversed = append(reversed, world.Tile{X: idx % width, Y: idx / width})
		if idx == startIdx {
			break
		}
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed
}

// ─────────────────────────────────────────────────────────────
// Heap binario con desempate determinista
// ─────────────────────────────────────────────────────────────

type node struct {
	idx  int32
	f    int32
	h    int32
	x, y int32
}

// less define el orden TOTAL del heap. El desempate por (h, y, x) tras el coste f
// es lo que hace que dos ejecuciones idénticas devuelvan exactamente la misma ruta.
func less(a, b node) bool {
	if a.f != b.f {
		return a.f < b.f
	}
	if a.h != b.h {
		return a.h < b.h // ante igual coste total, preferir lo más cercano al objetivo
	}
	if a.y != b.y {
		return a.y < b.y
	}
	return a.x < b.x
}

type nodeHeap []node

func (h nodeHeap) Len() int { return len(h) }

func (h *nodeHeap) push(n node) {
	*h = append(*h, n)
	i := len(*h) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if !less((*h)[i], (*h)[parent]) {
			break
		}
		(*h)[i], (*h)[parent] = (*h)[parent], (*h)[i]
		i = parent
	}
}

func (h *nodeHeap) pop() node {
	old := *h
	top := old[0]
	last := len(old) - 1
	old[0] = old[last]
	*h = old[:last]

	i, n := 0, last
	for {
		l, r := 2*i+1, 2*i+2
		smallest := i
		if l < n && less((*h)[l], (*h)[smallest]) {
			smallest = l
		}
		if r < n && less((*h)[r], (*h)[smallest]) {
			smallest = r
		}
		if smallest == i {
			break
		}
		(*h)[i], (*h)[smallest] = (*h)[smallest], (*h)[i]
		i = smallest
	}
	return top
}

// Comprobación en tiempo de compilación de que AStar cumple el contrato.
var _ Pathfinder = (*AStar)(nil)
