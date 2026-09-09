package world_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// newTestWorld crea un mundo pequeño y totalmente controlado.
func newTestWorld(t *testing.T, width, height, chunk int32, fill world.TerrainType) *world.World {
	t.Helper()
	terrain := make([]byte, int(width)*int(height))
	for i := range terrain {
		terrain[i] = byte(fill)
	}
	w, err := world.New(width, height, chunk, 1, terrain)
	require.NoError(t, err)
	return w
}

func TestNewRechazaDimensionesIncoherentes(t *testing.T) {
	t.Run("chunk que no divide el mundo", func(t *testing.T) {
		terrain := make([]byte, 30*30)
		_, err := world.New(30, 30, 32, 1, terrain)
		require.Error(t, err)
	})

	t.Run("terreno con longitud incorrecta", func(t *testing.T) {
		_, err := world.New(32, 32, 32, 1, make([]byte, 10))
		require.Error(t, err)
	})

	t.Run("byte de terreno desconocido", func(t *testing.T) {
		terrain := make([]byte, 32*32)
		terrain[5] = 99
		_, err := world.New(32, 32, 32, 1, terrain)
		require.ErrorContains(t, err, "terreno inválido")
	})
}

// INV-WORLD-001: toda coordenada válida está dentro de [0,width) x [0,height).
func TestLimitesDelMundo(t *testing.T) {
	w := newTestWorld(t, 64, 64, 32, world.Grassland)

	require.True(t, w.InBounds(0, 0))
	require.True(t, w.InBounds(63, 63))
	require.False(t, w.InBounds(-1, 0))
	require.False(t, w.InBounds(0, -1))
	require.False(t, w.InBounds(64, 0))
	require.False(t, w.InBounds(0, 64))
}

// Los límites del mundo se comportan como un muro, no como un pánico.
func TestFueraDeLimitesEsIntransitable(t *testing.T) {
	w := newTestWorld(t, 64, 64, 32, world.Grassland)

	require.Equal(t, world.Water, w.TerrainAt(-1, 0))
	require.False(t, w.IsWalkable(-1, 0))
	require.False(t, w.IsWalkable(64, 64))
	require.True(t, w.IsBlocked(-1, -1))
}

// INV-WORLD-006: cada tile pertenece exactamente a un chunk.
func TestConversionTileChunk(t *testing.T) {
	w := newTestWorld(t, 128, 128, 32, world.Grassland)

	cases := []struct {
		x, y           int32
		wantCX, wantCY int32
	}{
		{0, 0, 0, 0},
		{31, 31, 0, 0},
		{32, 0, 1, 0},
		{0, 32, 0, 1},
		{127, 127, 3, 3},
		{64, 96, 2, 3},
	}
	for _, c := range cases {
		cx, cy := w.ChunkOf(c.x, c.y)
		require.Equal(t, c.wantCX, cx, "chunkX de (%d,%d)", c.x, c.y)
		require.Equal(t, c.wantCY, cy, "chunkY de (%d,%d)", c.x, c.y)
	}

	require.EqualValues(t, 4, w.ChunksPerRow())
	require.EqualValues(t, 4, w.ChunksPerColumn())
	require.EqualValues(t, 0, w.ChunkIndex(0, 0))
	require.EqualValues(t, 5, w.ChunkIndex(1, 1))
	require.EqualValues(t, 15, w.ChunkIndex(3, 3))
}

func TestCostesDeTerreno(t *testing.T) {
	// Los multiplicadores del canon expresados en décimas.
	require.EqualValues(t, 10, world.Grassland.CostUnits())
	require.EqualValues(t, 16, world.Forest.CostUnits())
	require.EqualValues(t, 18, world.Hill.CostUnits())
	require.EqualValues(t, 6, world.Road.CostUnits())
	require.EqualValues(t, 0, world.Mountain.CostUnits())
	require.EqualValues(t, 0, world.Water.CostUnits())

	require.True(t, world.Grassland.Walkable())
	require.True(t, world.Forest.Walkable())
	require.True(t, world.Road.Walkable())
	require.False(t, world.Mountain.Walkable())
	require.False(t, world.Water.Walkable())

	// El coste mínimo es el del camino: la heurística de A* depende de esto para
	// seguir siendo admisible.
	require.EqualValues(t, 6, world.MinTerrainCostUnits)
}

// La capa de ocupación bloquea el paso SIN destruir el terreno de debajo.
func TestCapaDeOcupacionNoMutaElTerreno(t *testing.T) {
	w := newTestWorld(t, 64, 64, 32, world.Forest)

	require.True(t, w.IsWalkable(10, 10))
	w.SetBlocked(9, 9, 11, 11, true)

	require.False(t, w.IsWalkable(10, 10))
	require.True(t, w.IsBlocked(10, 10))
	require.Equal(t, world.Forest, w.TerrainAt(10, 10), "el terreno bajo la construcción no cambia")
	require.True(t, w.IsWalkable(12, 12), "fuera del rectángulo nada se bloquea")

	w.SetBlocked(9, 9, 11, 11, false)
	require.True(t, w.IsWalkable(10, 10), "demoler devuelve el tile a su estado original")
}

func TestChunkTerrainDevuelveUnaCopia(t *testing.T) {
	w := newTestWorld(t, 64, 64, 32, world.Grassland)

	chunk, err := w.ChunkTerrain(1, 1)
	require.NoError(t, err)
	require.Len(t, chunk, 32*32, "un chunk de 32x32 son exactamente 1024 bytes")

	chunk[0] = byte(world.Mountain)
	require.Equal(t, world.Grassland, w.TerrainAt(32, 32), "mutar la copia no debe tocar el mundo")

	_, err = w.ChunkTerrain(99, 99)
	require.ErrorIs(t, err, world.ErrOutOfBounds)
}

func TestChunkTerrainRespetaLaDisposicionFilaMayor(t *testing.T) {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	// Marca reconocible dentro del chunk (1,1): tile global (33, 34).
	terrain[34*64+33] = byte(world.Road)

	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)

	chunk, err := w.ChunkTerrain(1, 1)
	require.NoError(t, err)

	localX, localY := 33-32, 34-32
	require.Equal(t, byte(world.Road), chunk[localY*32+localX])
}

func TestChunksEnRadioSeRecortanYVanOrdenados(t *testing.T) {
	w := newTestWorld(t, 128, 128, 32, world.Grassland) // 4x4 chunks

	t.Run("centro interior", func(t *testing.T) {
		got := w.ChunksInRadius(world.Tile{X: 64, Y: 64}, 1) // chunk (2,2)
		require.Len(t, got, 9)
		require.Equal(t, world.ChunkCoord{CX: 1, CY: 1}, got[0])
		require.Equal(t, world.ChunkCoord{CX: 3, CY: 3}, got[len(got)-1])
	})

	t.Run("esquina: se recorta al mundo", func(t *testing.T) {
		got := w.ChunksInRadius(world.Tile{X: 0, Y: 0}, 1)
		require.Len(t, got, 4, "en la esquina sólo existen 4 de los 9 chunks")
	})

	t.Run("orden determinista", func(t *testing.T) {
		a := w.ChunksInRadius(world.Tile{X: 64, Y: 64}, 2)
		b := w.ChunksInRadius(world.Tile{X: 64, Y: 64}, 2)
		require.Equal(t, a, b)
	})
}

// INV-WORLD-005: la misma semilla produce siempre el mismo mapa, byte a byte.
func TestGeneracionEsDeterminista(t *testing.T) {
	a := world.Generate(128, 128, 20260909)
	b := world.Generate(128, 128, 20260909)
	require.Equal(t, a, b, "la misma semilla debe producir un mapa idéntico")

	c := world.Generate(128, 128, 20260910)
	require.NotEqual(t, a, c, "semillas distintas deben producir mapas distintos")
}

func TestGeneracionProduceTerrenoValidoYVariado(t *testing.T) {
	terrain := world.Generate(256, 256, 20260909)
	require.Len(t, terrain, 256*256)

	counts := map[world.TerrainType]int{}
	for _, b := range terrain {
		tt := world.TerrainType(b)
		require.True(t, tt.Valid(), "todo byte generado debe ser un terreno conocido")
		counts[tt]++
	}

	require.Greater(t, counts[world.Grassland], 0, "debe haber hierba")
	require.Greater(t, counts[world.Road], 0, "el generador traza caminos")

	walkable := counts[world.Grassland] + counts[world.Forest] + counts[world.Hill] + counts[world.Road]
	ratio := float64(walkable) / float64(len(terrain))
	require.Greater(t, ratio, 0.5, "un mundo jugable necesita mayoría de terreno transitable, hay %.2f", ratio)
}

func TestAdyacenciaYDiagonal(t *testing.T) {
	origin := world.Tile{X: 10, Y: 10}

	require.True(t, origin.IsAdjacent(world.Tile{X: 11, Y: 10}))
	require.True(t, origin.IsAdjacent(world.Tile{X: 11, Y: 11}))
	require.False(t, origin.IsAdjacent(world.Tile{X: 12, Y: 10}))
	require.False(t, origin.IsAdjacent(origin), "un tile no es adyacente a sí mismo")

	require.False(t, origin.IsDiagonalTo(world.Tile{X: 11, Y: 10}))
	require.True(t, origin.IsDiagonalTo(world.Tile{X: 11, Y: 11}))
}
