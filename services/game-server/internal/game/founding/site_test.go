package founding

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// mundoLiso devuelve un mundo de hierba con los tiles indicados pintados.
func mundoLiso(t *testing.T, w, h int32, pintar map[world.Tile]world.TerrainType) *world.World {
	t.Helper()
	terrain := make([]byte, int(w)*int(h))
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	for tile, tt := range pintar {
		terrain[int(tile.Y)*int(w)+int(tile.X)] = byte(tt)
	}
	mundo, err := world.New(w, h, 32, 1, terrain)
	require.NoError(t, err)
	return mundo
}

// semillaEn devuelve la semilla cuyo punto de partida es (x, y).
func semillaEn(w *world.World, x, y int32) uint64 {
	return uint64(y)*uint64(w.Width()) + uint64(x)
}

// verificarSitio comprueba las propiedades que INV-CITY-007 exige a un
// emplazamiento recién encontrado, ANTES de bloquear su zona urbana.
func verificarSitio(t *testing.T, w *world.World, s Site, villagers int) {
	t.Helper()
	c := s.Center
	require.True(t, w.InBounds(c.X, c.Y), "el centro %v cae fuera del mundo", c)
	for dy := -clearRadius; dy <= clearRadius; dy++ {
		for dx := -clearRadius; dx <= clearRadius; dx++ {
			assert.True(t, w.IsWalkable(c.X+dx, c.Y+dy),
				"el entorno de %v debe ser transitable y libre: falla (%d,%d)", c, c.X+dx, c.Y+dy)
		}
	}
	assert.Equal(t, [4]int32{c.X - 1, c.Y - 1, c.X + 1, c.Y + 1}, [4]int32{s.MinX, s.MinY, s.MaxX, s.MaxY},
		"la zona urbana es exactamente el 3 × 3 alrededor del centro")
	require.Len(t, s.Spawns, villagers)
	for _, sp := range s.Spawns {
		assert.True(t, w.IsWalkable(sp.X, sp.Y), "el aldeano nace en %v, que no es transitable", sp)
		assert.Equal(t, spawnRadius, chebyshev(c, sp), "los aldeanos nacen justo fuera de la muralla")
	}
}

// INV-CITY-007: sobre el generador real, el centro cae dentro del mundo y
// sobre terreno transitable, con todo su entorno despejado.
func TestElSitioCaeDentroDelMundoYSobreTerrenoTransitable(t *testing.T) {
	terrain := world.Generate(128, 128, 20260909)
	w, err := world.New(128, 128, 32, 20260909, terrain)
	require.NoError(t, err)

	for _, semilla := range []uint64{0, 1, 77, 4_096, 16_383, 1 << 40} {
		s, err := FindSite(w, nil, semilla, 3)
		require.NoError(t, err, "semilla %d", semilla)
		verificarSitio(t, w, s, 3)
	}
}

// INV-CITY-007: si no hay ningún emplazamiento válido, la fundación falla en
// lugar de colocar la ciudad donde sea.
func TestSinTerrenoTransitableNoHaySitio(t *testing.T) {
	agua := make(map[world.Tile]world.TerrainType, 32*32)
	for y := int32(0); y < 32; y++ {
		for x := int32(0); x < 32; x++ {
			agua[world.Tile{X: x, Y: y}] = world.Water
		}
	}
	w := mundoLiso(t, 32, 32, agua)

	_, err := FindSite(w, nil, 0, 3)
	assert.True(t, errors.Is(err, ErrNoSite), "se esperaba ErrNoSite, llegó %v", err)
}

// El entorno despejado no puede salirse del mundo: un candidato pegado al
// borde se descarta aunque la semilla apunte a él.
func TestUnCandidatoJuntoAlBordeSeDescarta(t *testing.T) {
	w := mundoLiso(t, 32, 32, nil)

	s, err := FindSite(w, nil, semillaEn(w, 0, 0), 3)
	require.NoError(t, err)
	verificarSitio(t, w, s, 3)
	assert.GreaterOrEqual(t, s.Center.X, clearRadius)
	assert.GreaterOrEqual(t, s.Center.Y, clearRadius)
}

// Un solo tile intransitable dentro del radio despejado descarta el
// candidato: la muralla y los aldeanos necesitan el entorno entero.
func TestUnObstaculoEnElEntornoDescartaElCandidato(t *testing.T) {
	inicio := world.Tile{X: 20, Y: 20}
	w := mundoLiso(t, 64, 64, map[world.Tile]world.TerrainType{
		{X: inicio.X + clearRadius, Y: inicio.Y}: world.Water,
	})

	s, err := FindSite(w, nil, semillaEn(w, inicio.X, inicio.Y), 3)
	require.NoError(t, err)
	assert.NotEqual(t, inicio, s.Center)
	verificarSitio(t, w, s, 3)
}

// separacionDeLaSpec es la distancia mínima entre centros que fija
// docs/invariants/city.md (INV-CITY-008). Se escribe aquí como literal y no se
// toma de minCityDistance: un test que compara la constante consigo misma no
// detecta que alguien la cambie.
const separacionDeLaSpec int32 = 24

// INV-CITY-008: fundar varias ciudades seguidas, bloqueando cada zona urbana
// como hace el servidor, deja los centros a 24 o más tiles Chebyshev y las
// zonas urbanas sin ningún tile en común.
func TestVariasFundacionesGuardanLaDistanciaYNoSeSolapan(t *testing.T) {
	terrain := world.Generate(256, 256, 20260909)
	w, err := world.New(256, 256, 32, 20260909, terrain)
	require.NoError(t, err)

	var fundados []Site
	var centros []world.Tile
	for i := 0; i < 12; i++ {
		// Semillas cercanas a propósito: todas empiezan a buscar en la misma
		// región, que es el caso en que la separación tiene que actuar.
		s, err := FindSite(w, centros, semillaEn(w, 128, 128)+uint64(i), 3)
		require.NoError(t, err, "fundación %d", i)
		verificarSitio(t, w, s, 3)
		w.SetBlocked(s.MinX, s.MinY, s.MaxX, s.MaxY, true)
		fundados = append(fundados, s)
		centros = append(centros, s.Center)
	}

	for i := range fundados {
		for j := i + 1; j < len(fundados); j++ {
			a, b := fundados[i], fundados[j]
			assert.GreaterOrEqual(t, chebyshev(a.Center, b.Center), separacionDeLaSpec,
				"ciudades %d y %d demasiado cerca", i, j)
			solapan := a.MinX <= b.MaxX && b.MinX <= a.MaxX && a.MinY <= b.MaxY && b.MinY <= a.MaxY
			assert.False(t, solapan, "las zonas urbanas %d y %d comparten tiles", i, j)
		}
	}
}

// La búsqueda es determinista: mismo mundo, mismas ciudades y misma semilla
// dan el mismo emplazamiento.
func TestLaBusquedaEsDeterminista(t *testing.T) {
	terrain := world.Generate(128, 128, 7)
	w, err := world.New(128, 128, 32, 7, terrain)
	require.NoError(t, err)
	existentes := []world.Tile{{X: 64, Y: 64}}

	a, err := FindSite(w, existentes, 12345, 3)
	require.NoError(t, err)
	b, err := FindSite(w, existentes, 12345, 3)
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestSinAldeanosNoSeFunda(t *testing.T) {
	w := mundoLiso(t, 32, 32, nil)
	_, err := FindSite(w, nil, 0, 0)
	assert.Error(t, err)
}

// FarEnough es la comprobación que el alta repite bajo el cerrojo: 23 tiles
// Chebyshev no bastan y 24 sí, en cualquier dirección.
func TestFarEnoughEsLaSeparacionDeLaSpec(t *testing.T) {
	centro := world.Tile{X: 100, Y: 100}
	for _, d := range []world.Tile{{X: 1, Y: 0}, {X: 0, Y: -1}, {X: 1, Y: 1}, {X: -1, Y: 1}} {
		cerca := world.Tile{X: centro.X + d.X*(separacionDeLaSpec-1), Y: centro.Y + d.Y*(separacionDeLaSpec-1)}
		lejos := world.Tile{X: centro.X + d.X*separacionDeLaSpec, Y: centro.Y + d.Y*separacionDeLaSpec}
		assert.False(t, FarEnough(centro, []world.Tile{cerca}), "a %v", cerca)
		assert.True(t, FarEnough(centro, []world.Tile{lejos}), "a %v", lejos)
	}
	assert.True(t, FarEnough(centro, nil), "sin ciudades, cualquier sitio vale")
}
