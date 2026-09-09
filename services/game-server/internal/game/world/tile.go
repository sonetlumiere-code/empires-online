// Package world modela el mundo lógico: tiles, terreno y chunks.
//
// Todo en este paquete son COORDENADAS LÓGICAS DE MAPA. El servidor no conoce
// píxeles, proyección isométrica ni cámaras: eso vive exclusivamente en el cliente.
// Ver ../../../../../docs/decisions/ADR-008-grid-coordinate-system.md
package world

import "fmt"

// Tile es una posición lógica del mundo.
type Tile struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
}

func (t Tile) String() string { return fmt.Sprintf("(%d,%d)", t.X, t.Y) }

// IsAdjacent indica si otro tile es vecino en 8-vecindad (y distinto de éste).
func (t Tile) IsAdjacent(o Tile) bool {
	dx := abs32(t.X - o.X)
	dy := abs32(t.Y - o.Y)
	return dx <= 1 && dy <= 1 && (dx|dy) != 0
}

// IsDiagonalTo indica si el paso hacia otro tile adyacente es diagonal.
func (t Tile) IsDiagonalTo(o Tile) bool {
	return t.X != o.X && t.Y != o.Y
}

// TerrainType es el tipo de terreno de un tile. Se persiste como un único byte.
type TerrainType uint8

const (
	Grassland TerrainType = 0
	Forest    TerrainType = 1
	Hill      TerrainType = 2
	Mountain  TerrainType = 3
	Water     TerrainType = 4
	Road      TerrainType = 5

	terrainCount = 6
)

// terrainNames debe mantenerse alineado con el enum TerrainType del protocolo.
var terrainNames = [terrainCount]string{"GRASSLAND", "FOREST", "HILL", "MOUNTAIN", "WATER", "ROAD"}

func (t TerrainType) String() string {
	if int(t) >= terrainCount {
		return "UNKNOWN"
	}
	return terrainNames[t]
}

// Valid indica si el byte corresponde a un terreno conocido.
func (t TerrainType) Valid() bool { return int(t) < terrainCount }

// terrainWalkable define qué terrenos se pueden atravesar por tierra.
var terrainWalkable = [terrainCount]bool{
	Grassland: true,
	Forest:    true,
	Hill:      true,
	Mountain:  false,
	Water:     false,
	Road:      true,
}

// terrainCostUnits es el coste de movimiento en DÉCIMAS del coste base.
// Multiplicadores del canon: GRASSLAND 1.00, FOREST 1.60, HILL 1.80, ROAD 0.60.
//
// Se expresan como enteros a propósito: la aritmética de coste debe ser exacta y
// reproducible en cualquier plataforma. El punto flotante queda fuera del núcleo
// determinista. Ver ../../../../../docs/architecture/pathfinding.md
var terrainCostUnits = [terrainCount]int32{
	Grassland: 10,
	Forest:    16,
	Hill:      18,
	Mountain:  0, // intransitable
	Water:     0, // intransitable
	Road:      6,
}

// CostBase es el denominador de terrainCostUnits: coste 10 == multiplicador 1.0.
const CostBase int32 = 10

// MinTerrainCostUnits es el menor coste de un terreno transitable.
//
// Es el valor que debe usar la heurística de A* para seguir siendo ADMISIBLE:
// si la heurística asumiera el coste de GRASSLAND (10) mientras existe ROAD (6),
// sobreestimaría la distancia restante y A* dejaría de garantizar el camino óptimo.
var MinTerrainCostUnits = computeMinTerrainCost()

func computeMinTerrainCost() int32 {
	min := int32(0)
	for i := 0; i < terrainCount; i++ {
		if !terrainWalkable[i] {
			continue
		}
		c := terrainCostUnits[i]
		if min == 0 || c < min {
			min = c
		}
	}
	return min
}

// Walkable indica si el terreno es transitable por unidades terrestres.
func (t TerrainType) Walkable() bool {
	if !t.Valid() {
		return false
	}
	return terrainWalkable[t]
}

// CostUnits devuelve el coste de entrar en este terreno, en décimas del coste base.
// Devuelve 0 para terrenos intransitables.
func (t TerrainType) CostUnits() int32 {
	if !t.Valid() {
		return 0
	}
	return terrainCostUnits[t]
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
