// Package founding elige dónde nace la ciudad de un jugador nuevo.
//
// La elección es DETERMINISTA a partir de una semilla: el mismo jugador en el
// mismo mundo obtiene siempre el mismo emplazamiento. Eso hace el alta
// reproducible en tests y depurable en producción.
package founding

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/deposit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
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

// HasWood dice si a un tile FOREST le queda madera (SPEC-ECON-FOREST). nil
// trata todo el bosque como lleno.
type HasWood func(world.Tile) bool

// Reach es la condición de alcance de la fundación (RN-CITY-024, RN-CITY-025,
// INV-CITY-019): si ya hay ciudades, la nueva nace a ≤ MaxNeighborTiles de
// Chebyshev de alguna y con una ruta entre los dos depósitos que el pathfinder
// encuentra con los mismos topes que una caravana (RN-CARAVAN-002). El valor
// cero (MaxNeighborTiles 0 o sin Pathfinder) la desactiva: es la búsqueda de
// antes de E30.
type Reach struct {
	// MaxNeighborTiles es EO_FOUNDING_MAX_NEIGHBOR_TILES.
	MaxNeighborTiles int32
	// Pathfinder es el A* del servidor; MaxNodes y MaxDistance, sus topes
	// (EO_PATHFINDING_MAX_NODES y EO_PATHFINDING_MAX_DISTANCE).
	Pathfinder  pathfinding.Pathfinder
	MaxNodes    int
	MaxDistance int32
}

// DefaultMaxNeighborTiles es el valor por defecto de
// EO_FOUNDING_MAX_NEIGHBOR_TILES (SPEC-TRADE-REACH Q2).
const DefaultMaxNeighborTiles int32 = 96

func (r Reach) enabled() bool { return r.MaxNeighborTiles > 0 && r.Pathfinder != nil }

// FindSite busca un emplazamiento válido para una ciudad nueva.
//
// Requisitos de un emplazamiento: estar dentro del mundo, tener todo su entorno
// despejado y transitable, y guardar la distancia mínima con cualquier ciudad ya
// existente. La búsqueda arranca en un punto derivado de `seed` y recorre el mundo
// en espiral, de modo que dos jugadores creados a la vez no compitan por el mismo
// tile. hasWood es la capa de madera del bosque: sólo cuentan los tiles a los
// que les queda (RN-ECON-017); nil, todos.
//
// existing va en orden de id de ciudad (es el desempate de RN-CITY-025). Con
// reach activo y alguna ciudad en el mundo, la espiral exige además el alcance;
// si el mundo entero no da ningún sitio así, se repite sin él: el alta nunca
// falla por esa regla (RN-CITY-024, RN-CITY-006).
func FindSite(w *world.World, existing []world.Tile, seed uint64, villagers int, hasWood HasWood, reach Reach) (Site, error) {
	if villagers <= 0 {
		return Site{}, fmt.Errorf("un jugador nuevo necesita al menos un aldeano")
	}
	if reach.enabled() && len(existing) > 0 {
		r := newReachCheck(w, existing, reach)
		if site, ok := spiral(w, existing, seed, villagers, hasWood, r.ok); ok {
			return site, nil
		}
	}
	if site, ok := spiral(w, existing, seed, villagers, hasWood, nil); ok {
		return site, nil
	}
	return Site{}, ErrNoSite
}

// FindRefoundSite busca el sitio de la ciudad nueva de un jugador cuya ciudad
// cayó (SPEC-CITY-FALL RN-CITY-027, ADR-050 §3): las reglas de FindSite, con el
// alcance de E30, y además a ≥ minDist de Chebyshev de cada tile de avoid (la
// ciudad caída y la del vencedor). Si ningún sitio del mundo cumple la
// distancia, se repite la búsqueda de FindSite sin ella: el vencido nunca se
// queda sin ciudad. existing son las ciudades en pie, sin la caída.
func FindRefoundSite(w *world.World, existing, avoid []world.Tile, minDist int32, seed uint64, villagers int,
	hasWood HasWood, reach Reach) (Site, error) {
	if villagers <= 0 {
		return Site{}, fmt.Errorf("una ciudad nueva necesita al menos un aldeano")
	}
	far := func(t world.Tile) bool { return farFromAll(t, avoid, minDist) }
	if minDist > 0 && len(avoid) > 0 {
		extra := far
		if reach.enabled() && len(existing) > 0 {
			r := newReachCheck(w, existing, reach)
			extra = func(t world.Tile) bool { return far(t) && r.ok(t) }
		}
		if site, ok := spiral(w, existing, seed, villagers, hasWood, extra); ok {
			return site, nil
		}
		if reach.enabled() && len(existing) > 0 {
			if site, ok := spiral(w, existing, seed, villagers, hasWood, far); ok {
				return site, nil
			}
		}
	}
	return FindSite(w, existing, seed, villagers, hasWood, reach)
}

// CheckSite comprueba un centro que eligió el jugador para su segunda ciudad
// (SPEC-MULTI-CITY RN-CITY-030): las reglas de fundación de siempre —el entorno
// despejado y transitable, la distancia mínima a toda ciudad en pie y el
// bosque al alcance (RN-CITY-001 a RN-CITY-006, RN-CITY-023)— en ese tile, sin
// espiral y sin el alcance de vecinos de E30 (Q4). Devuelve el sitio con
// villagers tiles de nacimiento, o ErrNoSite.
func CheckSite(w *world.World, center world.Tile, existing []world.Tile, villagers int, hasWood HasWood) (Site, error) {
	if villagers < 0 {
		return Site{}, fmt.Errorf("los aldeanos nuevos no pueden ser negativos")
	}
	if !w.InBounds(center.X, center.Y) || !isViable(w, center, existing, hasWood) {
		return Site{}, ErrNoSite
	}
	spawns := findSpawns(w, center, villagers)
	if len(spawns) < villagers {
		return Site{}, ErrNoSite
	}
	return Site{
		Center: center,
		MinX:   center.X - townCenterRadius,
		MinY:   center.Y - townCenterRadius,
		MaxX:   center.X + townCenterRadius,
		MaxY:   center.Y + townCenterRadius,
		Spawns: spawns,
	}, nil
}

func farFromAll(t world.Tile, avoid []world.Tile, minDist int32) bool {
	for _, a := range avoid {
		if chebyshev(t, a) < minDist {
			return false
		}
	}
	return true
}

// spiral recorre el mundo en espiral cuadrada desde el punto de partida y
// devuelve el primer candidato que cumple las reglas de siempre y, si lo hay,
// el filtro extra, que se comprueba el último (RN-CITY-025). Determinista.
func spiral(w *world.World, existing []world.Tile, seed uint64, villagers int, hasWood HasWood,
	extra func(world.Tile) bool) (Site, bool) {
	startX := int32(seed % uint64(w.Width()))
	startY := int32((seed / uint64(w.Width())) % uint64(w.Height()))

	maxRing := max32(w.Width(), w.Height())
	for ring := int32(0); ring <= maxRing; ring++ {
		for _, candidate := range ringTiles(startX, startY, ring) {
			if !isViable(w, candidate, existing, hasWood) {
				continue
			}
			spawns := findSpawns(w, candidate, villagers)
			if len(spawns) < villagers {
				continue
			}
			if extra != nil && !extra(candidate) {
				continue
			}
			return Site{
				Center: candidate,
				MinX:   candidate.X - townCenterRadius,
				MinY:   candidate.Y - townCenterRadius,
				MaxX:   candidate.X + townCenterRadius,
				MaxY:   candidate.Y + townCenterRadius,
				Spawns: spawns,
			}, true
		}
	}
	return Site{}, false
}

// reachCheck comprueba la condición de alcance de un candidato. Etiqueta una
// vez por búsqueda las componentes conexas del mundo en cuatro direcciones,
// que son las del A*: sin atajar esquinas, toda diagonal tiene un camino
// ortogonal de dos pasos, así que dos tiles se unen en ocho direcciones si y
// sólo si se unen en cuatro. Una vecina de otra componente no tiene ruta y se
// descarta sin lanzar el A*; las demás se prueban con él, que es quien aplica
// los topes de la caravana.
type reachCheck struct {
	w        *world.World
	existing []world.Tile
	reach    Reach
	// deposits es el depósito de cada ciudad existente (el primer tile libre de
	// su anillo, como lo calcula la simulación); hasDeposit, si lo tiene.
	deposits   []world.Tile
	hasDeposit []bool
	labels     []int32
}

func newReachCheck(w *world.World, existing []world.Tile, reach Reach) *reachCheck {
	r := &reachCheck{w: w, existing: existing, reach: reach,
		deposits: make([]world.Tile, len(existing)), hasDeposit: make([]bool, len(existing))}
	for i, c := range existing {
		r.deposits[i], r.hasDeposit[i] = depositOf(w, c)
	}
	r.labels = components(w)
	return r
}

// ok dice si el candidato tiene una vecina a ≤ MaxNeighborTiles con ruta entre
// los depósitos. Las vecinas, de la más cercana a la más lejana y, a igual
// distancia, en el orden de existing (menor id primero); basta una.
func (r *reachCheck) ok(candidate world.Tile) bool {
	from, ok := depositOf(r.w, candidate)
	if !ok {
		return false
	}
	near := make([]int, 0, 8)
	for i, c := range r.existing {
		if chebyshev(candidate, c) <= r.reach.MaxNeighborTiles && r.hasDeposit[i] {
			near = append(near, i)
		}
	}
	sort.SliceStable(near, func(a, b int) bool {
		return chebyshev(candidate, r.existing[near[a]]) < chebyshev(candidate, r.existing[near[b]])
	})
	grid := townGrid{World: r.w, center: candidate}
	for _, i := range near {
		to := r.deposits[i]
		if r.label(from) == 0 || r.label(from) != r.label(to) {
			continue
		}
		_, err := r.reach.Pathfinder.FindPath(context.Background(), grid, from, to, pathfinding.Options{
			MaxNodes: r.reach.MaxNodes, MaxDistance: r.reach.MaxDistance,
		})
		if err == nil {
			return true
		}
	}
	return false
}

func (r *reachCheck) label(t world.Tile) int32 {
	return r.labels[int(t.Y)*int(r.w.Width())+int(t.X)]
}

// depositOf es el depósito de una ciudad con centro en c: el primer tile
// transitable de su anillo de nacimiento, en el orden de city.SpawnTilesAround
// (el de City.FirstSpawnTile, que usan la recolección y las caravanas).
func depositOf(w *world.World, c world.Tile) (world.Tile, bool) {
	for _, t := range city.SpawnTilesAround(c) {
		if w.IsWalkable(t.X, t.Y) {
			return t, true
		}
	}
	return world.Tile{}, false
}

// townGrid es el mundo con la zona urbana del candidato ya bloqueada: la ruta
// de la caravana no podrá cruzarla cuando la ciudad exista.
type townGrid struct {
	*world.World
	center world.Tile
}

func (g townGrid) IsWalkable(x, y int32) bool {
	if chebyshev(g.center, world.Tile{X: x, Y: y}) <= townCenterRadius {
		return false
	}
	return g.World.IsWalkable(x, y)
}

// components etiqueta las componentes conexas en cuatro direcciones de los
// tiles transitables (0 = no transitable), en orden fijo de filas.
func components(w *world.World) []int32 {
	width, height := w.Width(), w.Height()
	labels := make([]int32, int(width)*int(height))
	queue := make([]int32, 0, 1024)
	next := int32(0)
	for y := int32(0); y < height; y++ {
		for x := int32(0); x < width; x++ {
			i := y*width + x
			if labels[i] != 0 || !w.IsWalkable(x, y) {
				continue
			}
			next++
			labels[i] = next
			queue = append(queue[:0], i)
			for k := 0; k < len(queue); k++ {
				cx, cy := queue[k]%width, queue[k]/width
				for _, d := range fourNeighbours {
					nx, ny := cx+d[0], cy+d[1]
					if nx < 0 || ny < 0 || nx >= width || ny >= height {
						continue
					}
					j := ny*width + nx
					if labels[j] != 0 || !w.IsWalkable(nx, ny) {
						continue
					}
					labels[j] = next
					queue = append(queue, j)
				}
			}
		}
	}
	return labels
}

// isViable comprueba que un tile sirve como centro de ciudad.
func isViable(w *world.World, center world.Tile, existing []world.Tile, hasWood HasWood) bool {
	for dy := -clearRadius; dy <= clearRadius; dy++ {
		for dx := -clearRadius; dx <= clearRadius; dx++ {
			if !w.IsWalkable(center.X+dx, center.Y+dy) {
				return false
			}
		}
	}
	return FarEnough(center, existing) && hasReachableForest(w, center, hasWood)
}

// fourNeighbours: la búsqueda de bosque sólo avanza en vertical y horizontal.
// Es conservadora: lo que se alcanza así lo alcanza también el pathfinder de
// ocho direcciones, que además no corta esquinas.
var fourNeighbours = [4][2]int32{{0, -1}, {-1, 0}, {1, 0}, {0, 1}}

// hasReachableForest indica si desde la zona urbana se llega a pie a al menos
// deposit.FoundingForestTiles tiles FOREST con madera sin salir del cuadrado de
// semilado forestRadius (RN-CITY-023, RN-ECON-017).
//
// Búsqueda en anchura desde el anillo de nacimiento —que el entorno despejado
// ya garantiza transitable— por tiles que pasan IsWalkable, sin entrar en el
// 3 × 3 de la zona urbana. Recorre como mucho 17 × 17 tiles, en orden fijo.
func hasReachableForest(w *world.World, center world.Tile, hasWood HasWood) bool {
	found := 0
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
		if w.TerrainAt(t.X, t.Y) == world.Forest && (hasWood == nil || hasWood(t)) {
			found++
			if found >= deposit.FoundingForestTiles {
				return true
			}
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

// SpawnsAround son los n primeros tiles transitables del anillo de nacimiento
// de un centro: los aldeanos de una ciudad tomada (SPEC-CITY-TAKEN RN-CITY-037).
func SpawnsAround(w *world.World, center world.Tile, n int) []world.Tile {
	return findSpawns(w, center, n)
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
