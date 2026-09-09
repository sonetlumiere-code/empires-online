// Package founding elige dónde nace la ciudad de un jugador nuevo.
//
// La elección es DETERMINISTA a partir de una semilla: el mismo jugador en el
// mismo mundo obtiene siempre el mismo emplazamiento. Eso hace el alta
// reproducible en tests y depurable en producción.
package founding

import (
	"errors"
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Site es un emplazamiento válido para una ciudad.
type Site struct {
	// Center es el tile central, donde se alza el Centro Urbano.
	Center world.Tile
	// Blocked es el rectángulo que ocupa la zona urbana amurallada.
	MinX, MinY, MaxX, MaxY int32
	// Spawns son los tiles donde nacen los aldeanos iniciales.
	Spawns []world.Tile
}

// Parámetros del asentamiento inicial.
const (
	// townCenterRadius: el Centro Urbano ocupa un cuadrado de 3x3 (radio 1).
	townCenterRadius int32 = 1
	// clearRadius: alrededor debe haber terreno despejado para la zona amurallada.
	clearRadius int32 = 3
	// spawnRadius: distancia a la que nacen los aldeanos, justo fuera de la muralla.
	spawnRadius int32 = 2
	// minCityDistance: separación mínima entre centros de ciudades, en tiles.
	minCityDistance int32 = 24
)

// ErrNoSite indica que no se encontró ningún emplazamiento válido.
var ErrNoSite = errors.New("no se encontró un emplazamiento válido para la ciudad")

// FindSite busca un emplazamiento válido para una ciudad nueva.
//
// Requisitos de un emplazamiento: estar dentro del mundo, tener todo su entorno
// despejado y transitable, y guardar la distancia mínima con cualquier ciudad ya
// existente. La búsqueda arranca en un punto derivado de `seed` y recorre el mundo
// en espiral, de modo que dos jugadores creados a la vez no compitan por el mismo
// tile.
func FindSite(w *world.World, existing []world.Tile, seed uint64, villagers int) (Site, error) {
	if villagers <= 0 {
		return Site{}, fmt.Errorf("un jugador nuevo necesita al menos un aldeano")
	}

	startX := int32(seed % uint64(w.Width()))
	startY := int32((seed / uint64(w.Width())) % uint64(w.Height()))

	// Recorrido en espiral cuadrada desde el punto de partida. Determinista y
	// sin asignaciones: recorre todo el mundo si hace falta.
	maxRing := max32(w.Width(), w.Height())
	for ring := int32(0); ring <= maxRing; ring++ {
		for _, candidate := range ringTiles(startX, startY, ring) {
			if !isViable(w, candidate, existing) {
				continue
			}
			spawns := findSpawns(w, candidate, villagers)
			if len(spawns) < villagers {
				continue
			}
			return Site{
				Center: candidate,
				MinX:   candidate.X - townCenterRadius,
				MinY:   candidate.Y - townCenterRadius,
				MaxX:   candidate.X + townCenterRadius,
				MaxY:   candidate.Y + townCenterRadius,
				Spawns: spawns,
			}, nil
		}
	}
	return Site{}, ErrNoSite
}

// isViable comprueba que un tile sirve como centro de ciudad.
func isViable(w *world.World, center world.Tile, existing []world.Tile) bool {
	for dy := -clearRadius; dy <= clearRadius; dy++ {
		for dx := -clearRadius; dx <= clearRadius; dx++ {
			if !w.IsWalkable(center.X+dx, center.Y+dy) {
				return false
			}
		}
	}
	for _, other := range existing {
		if chebyshev(center, other) < minCityDistance {
			return false
		}
	}
	return true
}

// findSpawns elige tiles de nacimiento justo fuera del Centro Urbano.
func findSpawns(w *world.World, center world.Tile, n int) []world.Tile {
	out := make([]world.Tile, 0, n)
	// Orden fijo alrededor del centro: el resultado no depende de ningún mapa.
	offsets := []world.Tile{
		{X: spawnRadius, Y: 0}, {X: -spawnRadius, Y: 0},
		{X: 0, Y: spawnRadius}, {X: 0, Y: -spawnRadius},
		{X: spawnRadius, Y: spawnRadius}, {X: -spawnRadius, Y: -spawnRadius},
		{X: spawnRadius, Y: -spawnRadius}, {X: -spawnRadius, Y: spawnRadius},
	}
	for _, off := range offsets {
		if len(out) == n {
			break
		}
		t := world.Tile{X: center.X + off.X, Y: center.Y + off.Y}
		if w.IsWalkable(t.X, t.Y) {
			out = append(out, t)
		}
	}
	return out
}

// ringTiles devuelve los tiles del perímetro de un anillo cuadrado, en orden fijo.
func ringTiles(cx, cy, ring int32) []world.Tile {
	if ring == 0 {
		return []world.Tile{{X: cx, Y: cy}}
	}
	// Se muestrea el anillo con un paso proporcional para no generar millones de
	// candidatos en los anillos grandes.
	step := ring/8 + 1
	tiles := make([]world.Tile, 0, int(8*ring/step)+8)

	for x := cx - ring; x <= cx+ring; x += step {
		tiles = append(tiles, world.Tile{X: x, Y: cy - ring}, world.Tile{X: x, Y: cy + ring})
	}
	for y := cy - ring + step; y < cy+ring; y += step {
		tiles = append(tiles, world.Tile{X: cx - ring, Y: y}, world.Tile{X: cx + ring, Y: y})
	}
	return tiles
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

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
