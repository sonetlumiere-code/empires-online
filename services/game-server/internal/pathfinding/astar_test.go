package pathfinding_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
)

// buildWorld construye un mundo a partir de un mapa ASCII.
//
//	. = GRASSLAND   f = FOREST   h = HILL
//	# = MOUNTAIN    ~ = WATER    = = ROAD
func buildWorld(t *testing.T, rows []string) *world.World {
	t.Helper()
	height := int32(len(rows))
	width := int32(len(rows[0]))

	terrain := make([]byte, int(width)*int(height))
	for y, row := range rows {
		require.Len(t, row, int(width), "todas las filas deben medir lo mismo")
		for x, ch := range row {
			var tt world.TerrainType
			switch ch {
			case '.':
				tt = world.Grassland
			case 'f':
				tt = world.Forest
			case 'h':
				tt = world.Hill
			case '#':
				tt = world.Mountain
			case '~':
				tt = world.Water
			case '=':
				tt = world.Road
			default:
				t.Fatalf("carácter de terreno desconocido: %q", ch)
			}
			terrain[y*int(width)+x] = byte(tt)
		}
	}

	w, err := world.New(width, height, 1, 1, terrain)
	require.NoError(t, err)
	return w
}

func find(t *testing.T, w *world.World, from, to world.Tile) ([]world.Tile, error) {
	t.Helper()
	pf := pathfinding.NewAStar(20000, 256)
	return pf.FindPath(context.Background(), w, from, to, pathfinding.Options{})
}

func TestRutaTrivialEnLineaRecta(t *testing.T) {
	w := buildWorld(t, []string{
		"......",
		"......",
		"......",
	})

	path, err := find(t, w, world.Tile{X: 0, Y: 1}, world.Tile{X: 5, Y: 1})
	require.NoError(t, err)
	require.Equal(t, world.Tile{X: 0, Y: 1}, path[0], "la ruta empieza SIEMPRE en el origen")
	require.Equal(t, world.Tile{X: 5, Y: 1}, path[len(path)-1])
	require.Len(t, path, 6)
}

func TestOrigenIgualADestino(t *testing.T) {
	w := buildWorld(t, []string{"...", "...", "..."})

	path, err := find(t, w, world.Tile{X: 1, Y: 1}, world.Tile{X: 1, Y: 1})
	require.NoError(t, err)
	require.Len(t, path, 1, "una ruta a donde ya estás es un único tile")
	require.Equal(t, world.Tile{X: 1, Y: 1}, path[0])
}

func TestDestinoIntransitableSeRechaza(t *testing.T) {
	w := buildWorld(t, []string{
		"...",
		".#.",
		"...",
	})

	_, err := find(t, w, world.Tile{X: 0, Y: 0}, world.Tile{X: 1, Y: 1})
	require.ErrorIs(t, err, pathfinding.ErrTargetNotWalkable)
}

func TestDestinoFueraDeLimites(t *testing.T) {
	w := buildWorld(t, []string{"...", "...", "..."})

	_, err := find(t, w, world.Tile{X: 0, Y: 0}, world.Tile{X: 99, Y: 0})
	require.ErrorIs(t, err, pathfinding.ErrTargetOutOfBounds)
}

func TestSinRutaPosible(t *testing.T) {
	// Muro de montañas que parte el mundo en dos.
	w := buildWorld(t, []string{
		"..#..",
		"..#..",
		"..#..",
	})

	_, err := find(t, w, world.Tile{X: 0, Y: 1}, world.Tile{X: 4, Y: 1})
	require.ErrorIs(t, err, pathfinding.ErrPathNotFound)
}

func TestRodeaElObstaculo(t *testing.T) {
	w := buildWorld(t, []string{
		".....",
		"..#..",
		".....",
	})

	path, err := find(t, w, world.Tile{X: 0, Y: 1}, world.Tile{X: 4, Y: 1})
	require.NoError(t, err)

	for _, tile := range path {
		require.False(t, tile.X == 2 && tile.Y == 1, "la ruta no puede atravesar la montaña")
		require.True(t, w.IsWalkable(tile.X, tile.Y), "ningún waypoint puede ser intransitable")
	}
	// Los waypoints deben ser contiguos en 8-vecindad.
	for i := 1; i < len(path); i++ {
		require.True(t, path[i-1].IsAdjacent(path[i]),
			"waypoints %d y %d no son adyacentes: %s -> %s", i-1, i, path[i-1], path[i])
	}
}

// El camino cuesta 0.6 y el bosque 1.6: A* debe preferir dar un rodeo por camino
// antes que cruzar el bosque en línea recta.
func TestPrefiereElCaminoAlBosque(t *testing.T) {
	w := buildWorld(t, []string{
		"=====",
		"fffff",
		"fffff",
	})

	path, err := find(t, w, world.Tile{X: 0, Y: 1}, world.Tile{X: 4, Y: 1})
	require.NoError(t, err)

	usedRoad := false
	for _, tile := range path {
		if w.TerrainAt(tile.X, tile.Y) == world.Road {
			usedRoad = true
			break
		}
	}
	require.True(t, usedRoad, "con el bosque a 1.6 y el camino a 0.6, la ruta óptima sube al camino")
}

// Sin esta regla una unidad podría "colarse" entre dos muros que se tocan en
// diagonal, atravesando una pared que visualmente está cerrada.
func TestProhibidoAtajarEsquinas(t *testing.T) {
	w := buildWorld(t, []string{
		"..#",
		".#.",
		"...",
	})

	path, err := find(t, w, world.Tile{X: 0, Y: 0}, world.Tile{X: 2, Y: 2})
	require.NoError(t, err)

	// El paso (0,0)->(1,1) sería diagonal entre dos montañas: debe evitarse.
	for i := 1; i < len(path); i++ {
		prev, cur := path[i-1], path[i]
		if !prev.IsDiagonalTo(cur) {
			continue
		}
		require.True(t, w.IsWalkable(cur.X, prev.Y) && w.IsWalkable(prev.X, cur.Y),
			"diagonal %s -> %s atajó una esquina cerrada", prev, cur)
	}
}

func TestLimiteDeDistanciaSeAplica(t *testing.T) {
	rows := make([]string, 8)
	for i := range rows {
		rows[i] = "................................"
	}
	w := buildWorld(t, rows)

	pf := pathfinding.NewAStar(20000, 256)
	_, err := pf.FindPath(context.Background(), w,
		world.Tile{X: 0, Y: 0}, world.Tile{X: 31, Y: 0},
		pathfinding.Options{MaxDistance: 10})
	require.ErrorIs(t, err, pathfinding.ErrPathTooLong)
}

func TestLimiteDeNodosSeAplica(t *testing.T) {
	rows := make([]string, 40)
	for i := range rows {
		rows[i] = "........................................"
	}
	w := buildWorld(t, rows)

	pf := pathfinding.NewAStar(20000, 256)
	_, err := pf.FindPath(context.Background(), w,
		world.Tile{X: 0, Y: 0}, world.Tile{X: 39, Y: 39},
		pathfinding.Options{MaxNodes: 3})
	require.ErrorIs(t, err, pathfinding.ErrPathTooLong)
}

// El determinismo del pathfinding es la base de los tests de simulación
// reproducibles: la misma consulta debe devolver SIEMPRE la misma ruta.
func TestRutaEsDeterminista(t *testing.T) {
	w := buildWorld(t, []string{
		"..........",
		"..##..##..",
		"..........",
		"..##..##..",
		"..........",
	})

	pf := pathfinding.NewAStar(20000, 256)
	from, to := world.Tile{X: 0, Y: 0}, world.Tile{X: 9, Y: 4}

	first, err := pf.FindPath(context.Background(), w, from, to, pathfinding.Options{})
	require.NoError(t, err)

	for i := 0; i < 20; i++ {
		again, err := pf.FindPath(context.Background(), w, from, to, pathfinding.Options{})
		require.NoError(t, err)
		require.Equal(t, first, again, "la ejecución %d difirió de la primera", i)
	}
}

// El espacio de trabajo se reutiliza entre consultas: hay que asegurarse de que
// no queda contaminado por la anterior.
func TestConsultasSucesivasNoSeContaminan(t *testing.T) {
	w := buildWorld(t, []string{
		".....",
		".....",
		".....",
	})
	pf := pathfinding.NewAStar(20000, 256)

	a, err := pf.FindPath(context.Background(), w, world.Tile{X: 0, Y: 0}, world.Tile{X: 4, Y: 0}, pathfinding.Options{})
	require.NoError(t, err)
	b, err := pf.FindPath(context.Background(), w, world.Tile{X: 0, Y: 2}, world.Tile{X: 4, Y: 2}, pathfinding.Options{})
	require.NoError(t, err)
	c, err := pf.FindPath(context.Background(), w, world.Tile{X: 0, Y: 0}, world.Tile{X: 4, Y: 0}, pathfinding.Options{})
	require.NoError(t, err)

	require.Equal(t, a, c, "la consulta repetida debe dar el mismo resultado")
	require.NotEqual(t, a, b)
}

func TestOrigenBloqueadoSeDetecta(t *testing.T) {
	w := buildWorld(t, []string{
		"...",
		".#.",
		"...",
	})

	_, err := find(t, w, world.Tile{X: 1, Y: 1}, world.Tile{X: 0, Y: 0})
	require.ErrorIs(t, err, pathfinding.ErrOriginNotWalkable)
}

func TestConstruccionesBloqueanLaRuta(t *testing.T) {
	w := buildWorld(t, []string{
		".....",
		".....",
		".....",
	})
	// Una ciudad ocupa la columna central.
	w.SetBlocked(2, 0, 2, 2, true)

	_, err := find(t, w, world.Tile{X: 0, Y: 1}, world.Tile{X: 4, Y: 1})
	require.ErrorIs(t, err, pathfinding.ErrPathNotFound,
		"la capa de ocupación debe cortar el paso igual que la montaña")
}

func TestCancelacionPorContexto(t *testing.T) {
	rows := make([]string, 200)
	for i := range rows {
		rows[i] = string(make([]byte, 0))
	}
	for i := range rows {
		b := make([]byte, 200)
		for j := range b {
			b[j] = '.'
		}
		rows[i] = string(b)
	}
	w := buildWorld(t, rows)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pf := pathfinding.NewAStar(1_000_000, 512)
	_, err := pf.FindPath(ctx, w, world.Tile{X: 0, Y: 0}, world.Tile{X: 199, Y: 199}, pathfinding.Options{})
	require.ErrorIs(t, err, context.Canceled)
}
