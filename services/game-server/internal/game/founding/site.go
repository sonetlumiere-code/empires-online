// Package founding elige dónde nace la ciudad de un jugador nuevo.
//
// La elección es DETERMINISTA a partir de una semilla: el mismo jugador en el
// mismo mundo obtiene siempre el mismo emplazamiento. Eso hace el alta
// reproducible en tests y depurable en producción.
package founding

import (
	"errors"
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
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
	// townCenterRadius: el radio de la zona urbana. NO se define aquí: es una
	// propiedad de la ciudad y vive en su dominio, para que la fundación, la capa
	// de ocupación y las condiciones de guarnición no puedan discrepar.
	townCenterRadius = city.UrbanRadius
	// clearRadius: alrededor debe haber terreno despejado para la zona amurallada.
	clearRadius int32 = 3
	// spawnRadius: distancia a la que nacen los aldeanos, justo fuera de la
	// muralla. Vive en la ciudad por la misma razón que el radio urbano: el
	// depósito de la madera y el nacimiento de un aldeano entrenado usan el
	// mismo anillo y en el mismo orden (city.SpawnTilesAround).
	spawnRadius = city.SpawnRadius
	// minCityDistance: separación mínima entre centros de ciudades, en tiles.
	minCityDistance int32 = 24
	// forestRadius: a esta distancia Chebyshev del centro, como mucho, tiene que
	// haber un bosque alcanzable a pie (RN-CITY-023). Sin madera no hay casa ni
	// aldeano. Con 8, el bosque cae dentro de la vista inicial incluso en una
	// pantalla de 1024 px: en isométrico, R tiles en diagonal son 64·R px.
	forestRadius int32 = 8
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
	return FarEnough(center, existing) && hasReachableForest(w, center)
}

// fourNeighbours: la búsqueda de bosque sólo avanza en vertical y horizontal.
// Es conservadora: lo que se alcanza así lo alcanza también el pathfinder de
// ocho direcciones, que además no corta esquinas.
var fourNeighbours = [4][2]int32{{0, -1}, {-1, 0}, {1, 0}, {0, 1}}

// hasReachableForest indica si desde la zona urbana se llega a pie a un tile
// FOREST sin salir del cuadrado de semilado forestRadius (RN-CITY-023).
//
// Búsqueda en anchura desde el anillo de nacimiento —que el entorno despejado
// ya garantiza transitable— por tiles que pasan IsWalkable, sin entrar en el
// 3 × 3 de la zona urbana. Recorre como mucho 17 × 17 tiles, en orden fijo.
func hasReachableForest(w *world.World, center world.Tile) bool {
	side := 2*forestRadius + 1
	seen := make([]bool, side*side)
	index := func(t world.Tile) int {
		return int((t.Y-center.Y+forestRadius)*side + (t.X - center.X + forestRadius))
	}

	queue := make([]world.Tile, 0, 64)
	for _, t := range city.SpawnTilesAround(center) {
		if w.IsWalkable(t.X, t.Y) && !seen[index(t)] {
			seen[index(t)] = true
			queue = append(queue, t)
		}
	}
	for i := 0; i < len(queue); i++ {
		t := queue[i]
		if w.TerrainAt(t.X, t.Y) == world.Forest {
			return true
		}
		for _, d := range fourNeighbours {
			n := world.Tile{X: t.X + d[0], Y: t.Y + d[1]}
			dist := chebyshev(center, n)
			if dist > forestRadius || dist <= townCenterRadius || seen[index(n)] || !w.IsWalkable(n.X, n.Y) {
				continue
			}
			seen[index(n)] = true
			queue = append(queue, n)
		}
	}
	return false
}

// FarEnough indica si un centro guarda la separación mínima con todos los
// centros existentes (INV-CITY-008). Es la comprobación barata que el alta
// repite dentro de su transacción, bajo el cerrojo de fundación, contra las
// ciudades confirmadas mientras se buscaba el sitio.
func FarEnough(center world.Tile, existing []world.Tile) bool {
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
	for _, t := range city.SpawnTilesAround(center) {
		if len(out) == n {
			break
		}
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
