package safezone_test

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// ─────────────────────────────────────────────────────────────
// Utilidades
// ─────────────────────────────────────────────────────────────

const lado = 64

// mundo crea un mundo de 64 × 64 de hierba y pinta encima las filas dadas a
// partir de (x0, y0). Cada carácter es un tile: G hierba, F bosque, M montaña,
// W agua, H colina, R camino.
func mundo(t *testing.T, x0, y0 int32, filas ...string) *world.World {
	t.Helper()
	terrain := make([]byte, lado*lado)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	codigos := map[rune]world.TerrainType{
		'G': world.Grassland, 'F': world.Forest, 'M': world.Mountain,
		'W': world.Water, 'H': world.Hill, 'R': world.Road,
	}
	for dy, fila := range filas {
		for dx, c := range []rune(fila) {
			tt, ok := codigos[c]
			require.True(t, ok, "carácter de terreno desconocido %q", c)
			terrain[(int(y0)+dy)*lado+int(x0)+dx] = byte(tt)
		}
	}
	w, err := world.New(lado, lado, 32, 1, terrain)
	require.NoError(t, err)
	return w
}

// miembros devuelve, fila a fila dentro del rectángulo, '#' para los tiles que
// pertenecen a alguna zona y '.' para los que no. Es el mismo formato que los
// ejemplos resueltos de la spec, para poder compararlos literalmente.
func miembros(idx *safezone.Index, minX, minY, maxX, maxY int32) []string {
	var out []string
	for y := minY; y <= maxY; y++ {
		fila := make([]rune, 0, maxX-minX+1)
		for x := minX; x <= maxX; x++ {
			if _, ok := idx.ZoneAt(x, y); ok {
				fila = append(fila, '#')
			} else {
				fila = append(fila, '.')
			}
		}
		out = append(out, string(fila))
	}
	return out
}

func construir(t *testing.T, w *world.World, zonas ...safezone.Zone) (*safezone.Index, safezone.Report) {
	t.Helper()
	idx, rep, err := safezone.BuildIndex(zonas, w)
	require.NoError(t, err)
	return idx, rep
}

// ─────────────────────────────────────────────────────────────
// Predicados: los ejemplos resueltos de la spec, literalmente
// ─────────────────────────────────────────────────────────────

// RN-SAFE-001. Ejemplo resuelto de safe-zones.md §6.2: la caja tiene 20 tiles
// y la zona 15. El hueco de GRASSLAND en (12,10) no oculta.
func TestDenseForestEjemploResueltoDeLaSpec(t *testing.T) {
	w := mundo(t, 10, 10,
		"FFGFF",
		"FFFFG",
		"GFFFF",
		"FGGFF",
	)
	z := safezone.Zone{ID: 1, Type: safezone.DenseForest, MinX: 10, MinY: 10, MaxX: 14, MaxY: 13}
	idx, rep := construir(t, w, z)

	assert.Equal(t, []string{
		"##.##",
		"####.",
		".####",
		"#..##",
	}, miembros(idx, 10, 10, 14, 13))
	assert.Equal(t, 15, idx.TileCount(1))
	assert.Empty(t, rep.Overlaps)
	assert.Empty(t, rep.Rejected)
}

// RN-SAFE-002 y RN-SAFE-004. Ejemplo resuelto de safe-zones.md §6.2, corregido:
// la spec original marcaba (41,22) como fuera, pero su vecino diagonal (40,21)
// es MOUNTAIN, así que por la regla de 8 direcciones pertenece. El test fija la
// regla, no el dibujo.
func TestCavernEjemploResueltoDeLaSpec(t *testing.T) {
	w := mundo(t, 40, 20,
		"MMGG",
		"MGGG",
		"GGGG",
	)
	z := safezone.Zone{ID: 1, Type: safezone.Cavern, MinX: 40, MinY: 20, MaxX: 43, MaxY: 22}
	idx, _ := construir(t, w, z)

	assert.Equal(t, []string{
		"..#.",
		".##.",
		"##..",
	}, miembros(idx, 40, 20, 43, 22))
}

// RN-SAFE-002: un caso por cada una de las 8 direcciones del vecino MOUNTAIN.
func TestCavernReconoceLaMontanaEnLasOchoDirecciones(t *testing.T) {
	dirs := []struct {
		nombre string
		dx, dy int32
	}{
		{"N", 0, -1}, {"NE", 1, -1}, {"E", 1, 0}, {"SE", 1, 1},
		{"S", 0, 1}, {"SO", -1, 1}, {"O", -1, 0}, {"NO", -1, -1},
	}
	for _, d := range dirs {
		t.Run(d.nombre, func(t *testing.T) {
			w := mundo(t, 30+d.dx, 30+d.dy, "M")
			z := safezone.Zone{ID: 1, Type: safezone.Cavern, MinX: 30, MinY: 30, MaxX: 30, MaxY: 30}
			idx, _ := construir(t, w, z)
			_, ok := idx.ZoneAt(30, 30)
			assert.True(t, ok, "(30,30) tiene montaña al %s y debería pertenecer", d.nombre)
		})
	}
}

func TestCavernSinMontanaVecinaNoPertenece(t *testing.T) {
	// La montaña está a distancia 2: no es vecina.
	w := mundo(t, 32, 30, "M")
	z := safezone.Zone{ID: 1, Type: safezone.Cavern, MinX: 30, MinY: 30, MaxX: 30, MaxY: 30}
	idx, _ := construir(t, w, z)
	_, ok := idx.ZoneAt(30, 30)
	assert.False(t, ok)
}

// RN-SAFE-004: ni la montaña ni el agua pertenecen nunca, aunque estén dentro de
// la caja y rodeados de montaña.
func TestMontanaYAguaNuncaPertenecen(t *testing.T) {
	w := mundo(t, 20, 20,
		"MMM",
		"MWM",
		"MMM",
	)
	for _, tipo := range []safezone.Type{safezone.Cavern, safezone.DenseForest} {
		z := safezone.Zone{ID: 1, Type: tipo, MinX: 20, MinY: 20, MaxX: 22, MaxY: 22}
		idx, _ := construir(t, w, z)
		assert.Equal(t, 0, idx.TileCount(1), "tipo %s", tipo)
	}
}

// RN-SAFE-003: los cuatro bordes son inclusivos. Las cuatro esquinas pertenecen
// y el tile siguiente a cada borde no.
func TestBordesInclusivos(t *testing.T) {
	filas := make([]string, 12)
	for i := range filas {
		filas[i] = "FFFFFFFFFFFF"
	}
	w := mundo(t, 9, 9, filas...)
	z := safezone.Zone{ID: 1, Type: safezone.DenseForest, MinX: 10, MinY: 10, MaxX: 19, MaxY: 19}
	idx, _ := construir(t, w, z)

	for _, esquina := range [][2]int32{{10, 10}, {19, 10}, {10, 19}, {19, 19}} {
		_, ok := idx.ZoneAt(esquina[0], esquina[1])
		assert.True(t, ok, "la esquina %v debe pertenecer", esquina)
	}
	for _, fuera := range [][2]int32{{9, 15}, {20, 15}, {15, 9}, {15, 20}} {
		_, ok := idx.ZoneAt(fuera[0], fuera[1])
		assert.False(t, ok, "%v está fuera de la caja aunque sea bosque", fuera)
	}
}

// RN-SAFE-005: consultar fuera del mundo no entra en pánico. Tampoco un índice
// nil, que es el de un servidor arrancado sin zonas.
func TestConsultaFueraDelMundoNoEntraEnPanico(t *testing.T) {
	idx, _ := construir(t, mundo(t, 0, 0))
	for _, c := range [][2]int32{{-1, 0}, {0, -1}, {lado, 0}, {0, lado}, {-1 << 30, 1 << 30}} {
		assert.NotPanics(t, func() {
			_, ok := idx.ZoneAt(c[0], c[1])
			assert.False(t, ok)
		})
	}
	var nulo *safezone.Index
	assert.NotPanics(t, func() { _, _ = nulo.ZoneAt(1, 1) })
}

// ─────────────────────────────────────────────────────────────
// Invariantes del índice
// ─────────────────────────────────────────────────────────────

// INV-SAFE-001 y RN-SAFE-007: dos zonas que comparten tiles. Gana el id menor,
// sea cual sea el orden de entrada, y el solape se informa con su tamaño.
// Zonas adyacentes por una arista NO son un solape: control negativo.
func TestDosZonasSolapadasResuelvenElIDMenorYSeReportan(t *testing.T) {
	filas := make([]string, 10)
	for i := range filas {
		filas[i] = "FFFFFFFFFF"
	}
	w := mundo(t, 0, 0, filas...)

	a := safezone.Zone{ID: 7, Type: safezone.DenseForest, MinX: 0, MinY: 0, MaxX: 4, MaxY: 4}
	b := safezone.Zone{ID: 3, Type: safezone.DenseForest, MinX: 3, MinY: 3, MaxX: 6, MaxY: 6}
	adyacente := safezone.Zone{ID: 9, Type: safezone.DenseForest, MinX: 7, MinY: 0, MaxX: 9, MaxY: 2}

	idx, rep := construir(t, w, a, adyacente, b)

	z, ok := idx.ZoneAt(4, 4)
	require.True(t, ok)
	assert.EqualValues(t, 3, z.ID, "el tile compartido es del id menor")

	require.Len(t, rep.Overlaps, 1, "la zona adyacente no cuenta como solape")
	assert.Equal(t, safezone.Overlap{Kept: 3, Discarded: 7, X: 3, Y: 3, Tiles: 4}, rep.Overlaps[0])
}

// INV-SAFE-002: todo tile del índice es transitable. Una caja que incluye agua
// produce un índice recortado, no un error ni una zona rechazada.
func TestTodoTileDelIndiceEsTransitable(t *testing.T) {
	w := mundo(t, 10, 10,
		"FFWW",
		"FFWW",
		"MMFF",
	)
	z := safezone.Zone{ID: 1, Type: safezone.DenseForest, MinX: 10, MinY: 10, MaxX: 13, MaxY: 12}
	idx, rep := construir(t, w, z)

	assert.Equal(t, 6, idx.TileCount(1))
	assert.Empty(t, rep.Rejected)
	for y := int32(0); y < lado; y++ {
		for x := int32(0); x < lado; x++ {
			if _, ok := idx.ZoneAt(x, y); ok {
				assert.True(t, w.IsWalkable(x, y), "(%d,%d) está en el índice y no es transitable", x, y)
			}
		}
	}
}

// INV-SAFE-005: una zona que se sale del mundo, con límites desordenados, con
// un tipo desconocido o con un id que no cabe en el índice no se incorpora. Las
// demás sí: el rechazo es de esa zona, no de la carga entera.
func TestZonasInvalidasSeRechazanSinArrastrarALasDemas(t *testing.T) {
	filas := make([]string, 4)
	for i := range filas {
		filas[i] = "FFFF"
	}
	w := mundo(t, 0, 0, filas...)

	valida := safezone.Zone{ID: 1, Type: safezone.DenseForest, MinX: 0, MinY: 0, MaxX: 3, MaxY: 3}
	fuera := safezone.Zone{ID: 2, Type: safezone.DenseForest, MinX: 60, MinY: 60, MaxX: lado, MaxY: 63}
	negativa := safezone.Zone{ID: 3, Type: safezone.DenseForest, MinX: -1, MinY: 0, MaxX: 2, MaxY: 2}
	desordenada := safezone.Zone{ID: 4, Type: safezone.DenseForest, MinX: 5, MinY: 0, MaxX: 2, MaxY: 2}
	tipo := safezone.Zone{ID: 5, Type: "SWAMP", MinX: 0, MinY: 0, MaxX: 1, MaxY: 1}
	enorme := safezone.Zone{ID: int64(safezone.MaxZoneID) + 1, Type: safezone.DenseForest, MinX: 0, MinY: 0, MaxX: 1, MaxY: 1}

	idx, rep := construir(t, w, enorme, tipo, desordenada, negativa, fuera, valida)

	assert.Equal(t, 1, idx.Len())
	assert.Equal(t, 16, idx.TileCount(1))
	rechazadas := make([]int64, 0, len(rep.Rejected))
	for _, r := range rep.Rejected {
		assert.NotEmpty(t, r.Reason)
		rechazadas = append(rechazadas, r.ZoneID)
	}
	assert.Equal(t, []int64{2, 3, 4, 5, int64(safezone.MaxZoneID) + 1}, rechazadas,
		"en orden ascendente de id")
}

// INV-SAFE-006: una zona colocada sobre una zona urbana pierde los tiles
// ocupados y el solape se informa. El resto de la zona sigue funcionando.
func TestUnaZonaSobreUnaCiudadSeRecortaYSeInforma(t *testing.T) {
	filas := make([]string, 7)
	for i := range filas {
		filas[i] = "FFFFFFF"
	}
	w := mundo(t, 20, 20, filas...)
	// Muralla 3 × 3 centrada en (23,23), como la que marca la fundación.
	w.SetBlocked(22, 22, 24, 24, true)

	z := safezone.Zone{ID: 1, Type: safezone.DenseForest, MinX: 20, MinY: 20, MaxX: 26, MaxY: 26}
	idx, rep := construir(t, w, z)

	assert.Equal(t, 49-9, idx.TileCount(1))
	require.Len(t, rep.Urban, 1)
	assert.Equal(t, safezone.UrbanOverlap{ZoneID: 1, X: 22, Y: 22, Tiles: 9}, rep.Urban[0])
	_, ok := idx.ZoneAt(23, 23)
	assert.False(t, ok, "el centro de la ciudad no es refugio")
}

// Fundar una ciudad después de construir el índice retira sus tiles: la muralla
// no puede seguir siendo refugio (INV-SAFE-002 en runtime).
func TestExcluirRetiraLosTilesYDiceDeQueZona(t *testing.T) {
	filas := make([]string, 6)
	for i := range filas {
		filas[i] = "FFFFFF"
	}
	w := mundo(t, 0, 0, filas...)
	a := safezone.Zone{ID: 1, Type: safezone.DenseForest, MinX: 0, MinY: 0, MaxX: 2, MaxY: 5}
	b := safezone.Zone{ID: 2, Type: safezone.DenseForest, MinX: 3, MinY: 0, MaxX: 5, MaxY: 5}
	idx, _ := construir(t, w, a, b)

	perdidos := idx.Exclude(2, 2, 4, 4)

	assert.Equal(t, []safezone.UrbanOverlap{
		{ZoneID: 1, X: 2, Y: 2, Tiles: 3},
		{ZoneID: 2, X: 3, Y: 2, Tiles: 6},
	}, perdidos)
	assert.Equal(t, 18-3, idx.TileCount(1))
	assert.Empty(t, idx.Exclude(2, 2, 4, 4), "excluir dos veces no retira nada más")
	assert.NotPanics(t, func() { idx.Exclude(-5, -5, -1, -1) })
}

// INV-SAFE-007: el índice es función pura de (zonas, terreno). Dos
// construcciones son idénticas byte a byte, y barajar la entrada no cambia nada.
func TestElIndiceEsDeterminista(t *testing.T) {
	w := world.Generate(lado, lado, 20260909)
	gw, err := world.New(lado, lado, 32, 20260909, w)
	require.NoError(t, err)

	zonas := []safezone.Zone{
		{ID: 1, Type: safezone.DenseForest, MinX: 0, MinY: 0, MaxX: 40, MaxY: 40},
		{ID: 2, Type: safezone.Cavern, MinX: 10, MinY: 10, MaxX: 63, MaxY: 63},
		{ID: 3, Type: safezone.DenseForest, MinX: 20, MinY: 0, MaxX: 63, MaxY: 30},
		{ID: 4, Type: safezone.Cavern, MinX: 0, MinY: 30, MaxX: 30, MaxY: 63},
	}
	primero, rep1 := construir(t, gw, zonas...)
	require.NotZero(t, primero.TileCount(1)+primero.TileCount(2), "el mundo de prueba debe producir algún tile")

	for i := 0; i < 20; i++ {
		barajadas := append([]safezone.Zone(nil), zonas...)
		rand.New(rand.NewSource(int64(i))).Shuffle(len(barajadas), func(a, b int) {
			barajadas[a], barajadas[b] = barajadas[b], barajadas[a]
		})
		otro, rep := construir(t, gw, barajadas...)
		require.Equal(t, primero.Tiles(), otro.Tiles(), "iteración %d", i)
		require.Equal(t, rep1, rep, "el informe también debe ser estable")
	}
}

// ─────────────────────────────────────────────────────────────
// Regla de ocultamiento
// ─────────────────────────────────────────────────────────────

// RN-SAFE-010, §7 e INV-SAFE-003: la tabla de transiciones completa.
func TestReglaDeOcultamiento(t *testing.T) {
	casos := []struct {
		nombre string
		status unit.Status
		hp     int32
		moving bool
		inZone bool
		quiere unit.Status
	}{
		{"IDLE en zona se oculta", unit.StatusIdle, 40, false, true, unit.StatusHidden},
		{"IDLE fuera de zona sigue IDLE", unit.StatusIdle, 40, false, false, unit.StatusIdle},
		{"IDLE sin vida no se oculta", unit.StatusIdle, 0, false, true, unit.StatusIdle},
		{"IDLE con movimiento no se oculta", unit.StatusIdle, 40, true, true, unit.StatusIdle},
		{"MOVING en zona NUNCA se oculta", unit.StatusMoving, 40, true, true, unit.StatusMoving},
		{"GARRISONED en zona NUNCA se oculta", unit.StatusGarrisoned, 40, false, true, unit.StatusGarrisoned},
		{"DEAD no se toca", unit.StatusDead, 0, false, true, unit.StatusDead},
		{"HIDDEN en zona sigue HIDDEN", unit.StatusHidden, 40, false, true, unit.StatusHidden},
		{"HIDDEN fuera de zona vuelve a IDLE", unit.StatusHidden, 40, false, false, unit.StatusIdle},
		{"HIDDEN con movimiento se repara a MOVING", unit.StatusHidden, 40, true, true, unit.StatusMoving},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			assert.Equal(t, c.quiere, safezone.NextStatus(c.status, c.hp, c.moving, c.inZone))
		})
	}
}
