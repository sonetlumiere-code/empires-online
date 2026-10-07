package devseed_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/devseed"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
	"github.com/empires-online/empires-online/services/game-server/internal/game/founding"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// mundoCanonico genera el mundo de la semilla por defecto. La siembra de
// desarrollo trabaja sobre él, así que es contra él donde tiene que funcionar.
func mundoCanonico(t *testing.T) *world.World {
	t.Helper()
	const lado, semilla = 512, 20260909
	w, err := world.New(lado, lado, 32, semilla, world.Generate(lado, lado, semilla))
	require.NoError(t, err)
	return w
}

// sembrar reproduce lo que hace cmd/devseed, sin base de datos: funda las
// ciudades de desarrollo y coloca sus dos zonas.
func sembrar(t *testing.T, w *world.World) ([]world.Tile, []safezone.Zone) {
	t.Helper()
	var centros []world.Tile
	for range devseed.Players {
		site, err := founding.FindSite(w, centros, devseed.AnchorSeed, 3)
		require.NoError(t, err)
		w.SetBlocked(site.MinX, site.MinY, site.MaxX, site.MaxY, true)
		centros = append(centros, site.Center)
	}

	var ocupados []devseed.Rect
	for _, c := range centros {
		ocupados = append(ocupados, devseed.CityRect(c))
	}
	var zonas []safezone.Zone
	for i, c := range centros {
		for _, tipo := range []safezone.Type{safezone.DenseForest, safezone.Cavern} {
			z, _, ok := devseed.PlaceZone(w, c, tipo, ocupados)
			if !ok {
				continue
			}
			z.ID = int64(len(zonas) + 1)
			z.Name = devseed.ZoneName(tipo, devseed.Players[i])
			zonas = append(zonas, z)
			ocupados = append(ocupados, devseed.ZoneRect(z))
		}
	}
	return centros, zonas
}

// Sobre el mundo canónico, cada ciudad de desarrollo recibe un bosque, y el
// índice que construirá el servidor con ellos no tiene ninguna anomalía: ni
// solapes, ni zonas rechazadas, ni tiles urbanos.
//
// No recibe ninguna caverna, y el test lo fija a propósito. Con la semilla
// canónica el generador produce 24 tiles MOUNTAIN en 512 × 512 (un 0,01 %):
// el umbral de montaña está por encima de casi todo el ruido. La caverna
// posible más cercana a estas ciudades queda a unos 470 tiles. Si este test
// empieza a fallar porque aparecen cavernas, alguien cambió el generador o su
// umbral, y la siembra de zonas del mundo canónico merece revisarse.
func TestLaSiembraProduceZonasValidasSobreElMundoCanonico(t *testing.T) {
	w := mundoCanonico(t)
	centros, zonas := sembrar(t, w)

	require.Len(t, centros, len(devseed.Players))
	require.Len(t, zonas, len(devseed.Players), "un bosque por ciudad y ninguna caverna")
	for _, z := range zonas {
		assert.Equal(t, safezone.DenseForest, z.Type)
	}

	idx, rep, err := safezone.BuildIndex(zonas, w)
	require.NoError(t, err)
	assert.Empty(t, rep.Overlaps, "INV-SAFE-001")
	assert.Empty(t, rep.Rejected, "INV-SAFE-005")
	assert.Empty(t, rep.Urban, "INV-SAFE-006")
	for _, z := range zonas {
		assert.GreaterOrEqual(t, idx.TileCount(z.ID), devseed.MinZoneTiles, "zona %s", z.Name)
	}
}

// Las ciudades de desarrollo nacen juntas: todas caben en el área de interés
// por defecto (radio 2 chunks de 32 tiles alrededor de cada una), que es lo
// que permite ver desde un jugador cómo se ocultan las unidades de otro.
func TestLasCiudadesDeDesarrolloSeVenEntreSi(t *testing.T) {
	w := mundoCanonico(t)
	centros, _ := sembrar(t, w)

	for _, a := range centros {
		visibles := map[world.ChunkCoord]bool{}
		for _, c := range w.ChunksInRadius(a, 2) {
			visibles[c] = true
		}
		for _, b := range centros {
			cx, cy := w.ChunkOf(b.X, b.Y)
			assert.True(t, visibles[world.ChunkCoord{CX: cx, CY: cy}], "%v no ve a %v", a, b)
		}
	}
}

// Determinismo: sembrar dos veces el mismo mundo da las mismas zonas.
func TestLaSiembraEsDeterminista(t *testing.T) {
	_, a := sembrar(t, mundoCanonico(t))
	_, b := sembrar(t, mundoCanonico(t))
	assert.Equal(t, a, b)
}

// Una caja nunca toca un rectángulo ocupado ni se sale del mundo.
func TestPlaceZoneRespetaLoOcupadoYLosBordes(t *testing.T) {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Forest)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)

	cerca := world.Tile{X: 2, Y: 2}
	ocupado := devseed.Rect{MinX: 0, MinY: 0, MaxX: 20, MaxY: 20}
	z, n, ok := devseed.PlaceZone(w, cerca, safezone.DenseForest, []devseed.Rect{ocupado})
	require.True(t, ok)
	assert.Equal(t, 49, n)
	assert.GreaterOrEqual(t, z.MinX, int32(0))
	assert.GreaterOrEqual(t, z.MinY, int32(0))
	assert.False(t, z.MinX <= ocupado.MaxX && z.MinY <= ocupado.MaxY, "no puede tocar lo ocupado: %+v", z)

	_, _, ok = devseed.PlaceZone(w, cerca, safezone.Cavern, nil)
	assert.False(t, ok, "sin montañas no hay caverna posible")
}
