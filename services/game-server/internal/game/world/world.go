package world

import (
	"errors"
	"fmt"
	"sync"
)

// ErrOutOfBounds se devuelve al referenciar un tile fuera del mundo.
var ErrOutOfBounds = errors.New("coordenada fuera de los límites del mundo")

// World es el mapa lógico completo, residente en memoria.
//
// El terreno es inmutable tras la generación. Lo que sí cambia es la capa de
// OCUPACIÓN (`blocked`), que representa edificios, murallas y ciudades. Se
// mantienen separadas a propósito: fundar una ciudad no debe destruir la
// información del terreno que hay debajo.
//
// La capa de ocupación se protege con un mutex porque el bootstrap de jugadores
// puede ocurrir fuera del hilo del game loop.
type World struct {
	width     int32
	height    int32
	chunkSize int32
	seed      uint64

	// terrain tiene width*height bytes, en orden fila-mayor: idx = y*width + x.
	terrain []byte

	mu      sync.RWMutex
	blocked []bool
}

// New crea un mundo con el terreno indicado. `terrain` debe tener exactamente
// width*height bytes y todos sus valores deben ser terrenos válidos.
func New(width, height, chunkSize int32, seed uint64, terrain []byte) (*World, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("dimensiones inválidas: %dx%d", width, height)
	}
	if chunkSize <= 0 || width%chunkSize != 0 || height%chunkSize != 0 {
		return nil, fmt.Errorf("chunkSize %d no divide exactamente a %dx%d", chunkSize, width, height)
	}
	if int64(len(terrain)) != int64(width)*int64(height) {
		return nil, fmt.Errorf("el terreno tiene %d bytes; se esperaban %d", len(terrain), int64(width)*int64(height))
	}
	for i, b := range terrain {
		if !TerrainType(b).Valid() {
			return nil, fmt.Errorf("terreno inválido %d en el índice %d", b, i)
		}
	}
	return &World{
		width:     width,
		height:    height,
		chunkSize: chunkSize,
		seed:      seed,
		terrain:   terrain,
		blocked:   make([]bool, int(width)*int(height)),
	}, nil
}

func (w *World) Width() int32     { return w.width }
func (w *World) Height() int32    { return w.height }
func (w *World) ChunkSize() int32 { return w.chunkSize }
func (w *World) Seed() uint64     { return w.seed }

// ChunksPerRow es el número de chunks a lo ancho del mundo.
func (w *World) ChunksPerRow() int32 { return w.width / w.chunkSize }

// ChunksPerColumn es el número de chunks a lo alto del mundo.
func (w *World) ChunksPerColumn() int32 { return w.height / w.chunkSize }

// InBounds indica si la coordenada pertenece al mundo.
func (w *World) InBounds(x, y int32) bool {
	return x >= 0 && y >= 0 && x < w.width && y < w.height
}

// TileInBounds indica si el tile pertenece al mundo.
func (w *World) TileInBounds(t Tile) bool { return w.InBounds(t.X, t.Y) }

func (w *World) index(x, y int32) int { return int(y)*int(w.width) + int(x) }

// TerrainAt devuelve el terreno del tile. Fuera de límites devuelve Water,
// que es intransitable: los límites del mundo se comportan como un muro.
func (w *World) TerrainAt(x, y int32) TerrainType {
	if !w.InBounds(x, y) {
		return Water
	}
	return TerrainType(w.terrain[w.index(x, y)])
}

// IsBlocked indica si el tile está ocupado por una construcción.
func (w *World) IsBlocked(x, y int32) bool {
	if !w.InBounds(x, y) {
		return true
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.blocked[w.index(x, y)]
}

// IsWalkable indica si una unidad terrestre puede ocupar el tile: terreno
// transitable Y sin construcción encima.
func (w *World) IsWalkable(x, y int32) bool {
	if !w.InBounds(x, y) {
		return false
	}
	if !TerrainType(w.terrain[w.index(x, y)]).Walkable() {
		return false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return !w.blocked[w.index(x, y)]
}

// MoveCostUnits devuelve el coste de entrar en el tile, en décimas del coste base.
// Devuelve 0 si el tile no es transitable.
func (w *World) MoveCostUnits(x, y int32) int32 {
	if !w.IsWalkable(x, y) {
		return 0
	}
	return TerrainType(w.terrain[w.index(x, y)]).CostUnits()
}

// MinMoveCostUnits es el menor coste posible de un paso ortogonal en este mundo.
// La heurística de A* lo necesita para mantenerse admisible.
func (w *World) MinMoveCostUnits() int32 { return MinTerrainCostUnits }

// SetBlocked marca o desmarca un rectángulo como ocupado por construcciones.
// Es la operación que usa la fundación de una ciudad.
func (w *World) SetBlocked(minX, minY, maxX, maxY int32, blocked bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if !w.InBounds(x, y) {
				continue
			}
			w.blocked[w.index(x, y)] = blocked
		}
	}
}

// ChunkOf devuelve las coordenadas del chunk que contiene el tile.
func (w *World) ChunkOf(x, y int32) (cx, cy int32) {
	return x / w.chunkSize, y / w.chunkSize
}

// ChunkIndex devuelve el identificador lineal de un chunk.
func (w *World) ChunkIndex(cx, cy int32) uint32 {
	return uint32(cy)*uint32(w.ChunksPerRow()) + uint32(cx)
}

// ChunkTerrain devuelve una COPIA del terreno de un chunk, en orden fila-mayor.
// Es lo que se serializa hacia el cliente y hacia la tabla world_chunks.
func (w *World) ChunkTerrain(cx, cy int32) ([]byte, error) {
	if cx < 0 || cy < 0 || cx >= w.ChunksPerRow() || cy >= w.ChunksPerColumn() {
		return nil, fmt.Errorf("%w: chunk (%d,%d)", ErrOutOfBounds, cx, cy)
	}
	size := int(w.chunkSize)
	out := make([]byte, size*size)
	baseX, baseY := cx*w.chunkSize, cy*w.chunkSize
	for row := 0; row < size; row++ {
		src := w.index(baseX, baseY+int32(row))
		copy(out[row*size:(row+1)*size], w.terrain[src:src+size])
	}
	return out, nil
}

// ChunksInRadius devuelve los chunks dentro de un radio de Chebyshev alrededor
// del chunk que contiene el tile central, recortados a los límites del mundo.
//
// El resultado va SIEMPRE ordenado (por cy y luego cx): el determinismo del
// orden importa para que los snapshots sean reproducibles y comparables en tests.
func (w *World) ChunksInRadius(center Tile, radius int32) []ChunkCoord {
	ccx, ccy := w.ChunkOf(clamp32(center.X, 0, w.width-1), clamp32(center.Y, 0, w.height-1))
	minCX := max32(0, ccx-radius)
	maxCX := min32(w.ChunksPerRow()-1, ccx+radius)
	minCY := max32(0, ccy-radius)
	maxCY := min32(w.ChunksPerColumn()-1, ccy+radius)

	out := make([]ChunkCoord, 0, int(maxCX-minCX+1)*int(maxCY-minCY+1))
	for cy := minCY; cy <= maxCY; cy++ {
		for cx := minCX; cx <= maxCX; cx++ {
			out = append(out, ChunkCoord{CX: cx, CY: cy})
		}
	}
	return out
}

// ChunkCoord identifica un chunk del mundo.
type ChunkCoord struct {
	CX int32 `json:"cx"`
	CY int32 `json:"cy"`
}

func clamp32(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
