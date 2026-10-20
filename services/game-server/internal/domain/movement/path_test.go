package movement_test

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// villagerMsPerTile es la velocidad base del aldeano del MVP.
const villagerMsPerTile int64 = 600

// testGrid construye el mundo del ejemplo numérico canónico.
//
//	fila 0:  . . . . .
//	fila 1:  . . . f =
func testGrid(t *testing.T) *world.World {
	t.Helper()
	terrain := []byte{
		byte(world.Grassland), byte(world.Grassland), byte(world.Grassland), byte(world.Grassland), byte(world.Grassland),
		byte(world.Grassland), byte(world.Grassland), byte(world.Grassland), byte(world.Forest), byte(world.Road),
	}
	w, err := world.New(5, 2, 1, 1, terrain)
	require.NoError(t, err)
	return w
}

// TestEjemploNumericoCanonico verifica, paso a paso, la aritmética de la
// polilínea temporizada. Si este test cambia, el contrato de movimiento cambió.
//
//	(0,0) -> (1,0)  GRASSLAND ortogonal  600 * 10/10             =  600  acc  600
//	(1,0) -> (2,1)  GRASSLAND diagonal   600 * 10/10 * √2        =  849  acc 1449
//	(2,1) -> (3,1)  FOREST    ortogonal  600 * 16/10             =  960  acc 2409
//	(3,1) -> (4,1)  ROAD      ortogonal  600 *  6/10             =  360  acc 2769
func TestEjemploNumericoCanonico(t *testing.T) {
	w := testGrid(t)
	tiles := []world.Tile{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 2, Y: 1}, {X: 3, Y: 1}, {X: 4, Y: 1}}

	path, err := movement.BuildTimedPath(tiles, w, villagerMsPerTile)
	require.NoError(t, err)
	require.Len(t, path, 5)

	require.Equal(t, movement.Waypoint{X: 0, Y: 0, TMs: 0}, path[0], "el origen siempre lleva tMs = 0")
	require.Equal(t, movement.Waypoint{X: 1, Y: 0, TMs: 600}, path[1])
	require.Equal(t, movement.Waypoint{X: 2, Y: 1, TMs: 1449}, path[2])
	require.Equal(t, movement.Waypoint{X: 3, Y: 1, TMs: 2409}, path[3])
	require.Equal(t, movement.Waypoint{X: 4, Y: 1, TMs: 2769}, path[4])

	require.EqualValues(t, 2769, path.DurationMs())
	require.Equal(t, world.Tile{X: 0, Y: 0}, path.Origin())
	require.Equal(t, world.Tile{X: 4, Y: 1}, path.Destination())
}

func TestDuracionDeUnPaso(t *testing.T) {
	cases := []struct {
		name     string
		cost     int32
		diagonal bool
		want     int64
	}{
		{"hierba ortogonal", 10, false, 600},
		{"hierba diagonal", 10, true, 849},
		{"bosque ortogonal", 16, false, 960},
		{"bosque diagonal", 16, true, 1358},
		{"colina ortogonal", 18, false, 1080},
		{"camino ortogonal", 6, false, 360},
		{"camino diagonal", 6, true, 509},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, movement.StepDurationMs(villagerMsPerTile, c.cost, c.diagonal))
		})
	}
}

func TestNingunPasoEsInstantaneo(t *testing.T) {
	// Aunque la velocidad y el coste sean minúsculos, un paso nunca dura 0 ms:
	// un tMs repetido rompería la monotonía de la polilínea.
	require.EqualValues(t, 1, movement.StepDurationMs(1, 1, false))
}

// La posición autoritativa es una FUNCIÓN PURA de (polilínea, tiempo).
// Este test es la especificación ejecutable de esa función.
func TestPositionAt(t *testing.T) {
	w := testGrid(t)
	tiles := []world.Tile{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 2, Y: 1}, {X: 3, Y: 1}, {X: 4, Y: 1}}
	path, err := movement.BuildTimedPath(tiles, w, villagerMsPerTile)
	require.NoError(t, err)

	cases := []struct {
		elapsed int64
		want    world.Tile
		why     string
	}{
		{-1000, world.Tile{X: 0, Y: 0}, "antes de empezar, en el origen"},
		{0, world.Tile{X: 0, Y: 0}, "en el instante cero, en el origen"},
		{1, world.Tile{X: 0, Y: 0}, "aún no ha alcanzado el siguiente tile"},
		{599, world.Tile{X: 0, Y: 0}, "un milisegundo antes de llegar, sigue en el origen"},
		{600, world.Tile{X: 1, Y: 0}, "en el instante exacto del waypoint, ya está en él"},
		{1448, world.Tile{X: 1, Y: 0}, "entre waypoints se ocupa el último alcanzado"},
		{1449, world.Tile{X: 2, Y: 1}, "waypoint diagonal alcanzado"},
		{2408, world.Tile{X: 2, Y: 1}, ""},
		{2409, world.Tile{X: 3, Y: 1}, ""},
		{2769, world.Tile{X: 4, Y: 1}, "llegada exacta"},
		{999_999, world.Tile{X: 4, Y: 1}, "después de llegar se permanece en el destino"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, path.PositionAt(c.elapsed),
			"elapsed=%d: %s", c.elapsed, c.why)
	}
}

func TestIndexAt(t *testing.T) {
	w := testGrid(t)
	tiles := []world.Tile{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 2, Y: 1}}
	path, err := movement.BuildTimedPath(tiles, w, villagerMsPerTile)
	require.NoError(t, err)

	require.Equal(t, 0, path.IndexAt(-5))
	require.Equal(t, 0, path.IndexAt(0))
	require.Equal(t, 0, path.IndexAt(599))
	require.Equal(t, 1, path.IndexAt(600))
	require.Equal(t, 2, path.IndexAt(1449))
	require.Equal(t, 2, path.IndexAt(50_000))
}

func TestBuildTimedPathRechazaEntradasInvalidas(t *testing.T) {
	w := testGrid(t)

	t.Run("ruta vacía", func(t *testing.T) {
		_, err := movement.BuildTimedPath(nil, w, villagerMsPerTile)
		require.ErrorIs(t, err, movement.ErrEmptyPath)
	})

	t.Run("velocidad no positiva", func(t *testing.T) {
		_, err := movement.BuildTimedPath([]world.Tile{{X: 0, Y: 0}, {X: 1, Y: 0}}, w, 0)
		require.Error(t, err)
	})

	t.Run("salto no contiguo", func(t *testing.T) {
		_, err := movement.BuildTimedPath([]world.Tile{{X: 0, Y: 0}, {X: 4, Y: 1}}, w, villagerMsPerTile)
		require.ErrorIs(t, err, movement.ErrPathNotContiguous)
	})
}

func TestValidateComprobaciones(t *testing.T) {
	w := testGrid(t)

	t.Run("polilínea correcta", func(t *testing.T) {
		path, err := movement.BuildTimedPath(
			[]world.Tile{{X: 0, Y: 0}, {X: 1, Y: 0}}, w, villagerMsPerTile)
		require.NoError(t, err)
		require.NoError(t, movement.Validate(path, w))
	})

	t.Run("vacía", func(t *testing.T) {
		require.ErrorIs(t, movement.Validate(movement.TimedPath{}, w), movement.ErrEmptyPath)
	})

	t.Run("el origen no empieza en cero", func(t *testing.T) {
		bad := movement.TimedPath{{X: 0, Y: 0, TMs: 5}}
		require.ErrorIs(t, movement.Validate(bad, w), movement.ErrInvalidOrigin)
	})

	t.Run("tiempos no crecientes", func(t *testing.T) {
		bad := movement.TimedPath{{X: 0, Y: 0, TMs: 0}, {X: 1, Y: 0, TMs: 0}}
		require.ErrorIs(t, movement.Validate(bad, w), movement.ErrPathNotMonotonic)
	})

	t.Run("waypoints no contiguos", func(t *testing.T) {
		bad := movement.TimedPath{{X: 0, Y: 0, TMs: 0}, {X: 4, Y: 1, TMs: 600}}
		require.ErrorIs(t, movement.Validate(bad, w), movement.ErrPathNotContiguous)
	})

	t.Run("atraviesa un tile bloqueado", func(t *testing.T) {
		path, err := movement.BuildTimedPath(
			[]world.Tile{{X: 0, Y: 0}, {X: 1, Y: 0}}, w, villagerMsPerTile)
		require.NoError(t, err)

		// El mapa cambia bajo un movimiento ya calculado: al rehidratar debe
		// detectarse en lugar de teletransportar a la unidad.
		w.SetBlocked(1, 0, 1, 0, true)
		require.ErrorIs(t, movement.Validate(path, w), movement.ErrPathNotWalkable)
	})

	t.Run("ataja una esquina", func(t *testing.T) {
		// INV-MOVE-004: el A* nunca produce este paso, pero una fila de la base
		// de datos sí puede traerlo. Los dos extremos son transitables; lo que
		// falla es el ortogonal (1,0).
		w := testGrid(t)
		path, err := movement.BuildTimedPath(
			[]world.Tile{{X: 0, Y: 0}, {X: 1, Y: 1}}, w, villagerMsPerTile)
		require.NoError(t, err)
		require.NoError(t, movement.Validate(path, w))

		w.SetBlocked(1, 0, 1, 0, true)
		require.ErrorIs(t, movement.Validate(path, w), movement.ErrPathCutsCorner)
	})
}

func TestValidateFromSoloMiraElTramoPendiente(t *testing.T) {
	w := testGrid(t)
	path, err := movement.BuildTimedPath(
		[]world.Tile{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 2, Y: 0}, {X: 3, Y: 1}}, w, villagerMsPerTile)
	require.NoError(t, err)

	// Se bloquea un tile ya recorrido: lo pendiente desde el waypoint 2 sigue
	// siendo válido, la polilínea entera no.
	w.SetBlocked(1, 0, 1, 0, true)
	require.NoError(t, path.ValidateFrom(2, w))
	require.ErrorIs(t, path.ValidateFrom(0, w), movement.ErrPathNotWalkable)

	// El ortogonal (3,0) del último paso diagonal invalida el tramo pendiente.
	w.SetBlocked(3, 0, 3, 0, true)
	require.ErrorIs(t, path.ValidateFrom(2, w), movement.ErrPathCutsCorner)
	require.NoError(t, path.ValidateFrom(3, w), "desde el destino ya no queda ningún paso")
}

func TestMovementCicloDeVida(t *testing.T) {
	w := testGrid(t)
	path, err := movement.BuildTimedPath(
		[]world.Tile{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 2, Y: 1}}, w, villagerMsPerTile)
	require.NoError(t, err)

	const start int64 = 1_757_376_000_000
	m := movement.New(42, path, world.Tile{X: 2, Y: 1}, start)

	require.EqualValues(t, 42, m.UnitID)
	require.Equal(t, movement.StatusActive, m.Status)
	require.Equal(t, start+1449, m.ArrivalTimeMs, "la llegada es el inicio más la duración total")

	require.Equal(t, world.Tile{X: 0, Y: 0}, m.PositionAt(start))
	require.Equal(t, world.Tile{X: 1, Y: 0}, m.PositionAt(start+600))
	require.Equal(t, world.Tile{X: 2, Y: 1}, m.PositionAt(start+1449))

	require.False(t, m.HasArrived(start))
	require.False(t, m.HasArrived(start+1448))
	require.True(t, m.HasArrived(start+1449))
	require.True(t, m.HasArrived(start+999_999), "un movimiento vencido hace mucho sigue estando vencido")

	require.EqualValues(t, 1449, m.RemainingMs(start))
	require.EqualValues(t, 0, m.RemainingMs(start+5000), "el tiempo restante nunca es negativo")
	require.Equal(t, world.Tile{X: 2, Y: 1}, m.Destination())
}

func TestEstadosDeMovimiento(t *testing.T) {
	require.True(t, movement.StatusActive.Valid())
	require.True(t, movement.StatusCompleted.Valid())
	require.True(t, movement.StatusCancelled.Valid())
	require.True(t, movement.StatusFailed.Valid())
	require.False(t, movement.Status("PAUSED").Valid())
}

// INV-MOVE-006 como propiedad: sobre polilíneas y tiempos generados con una
// semilla fija, la posición derivada es siempre el último waypoint alcanzado y
// nunca uno posterior; antes del inicio es el origen y después de la llegada
// el destino, sin extrapolar.
func TestLaPosicionNuncaAdelantaAlReloj(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261020, 1))
	for caso := 0; caso < 2000; caso++ {
		n := 1 + rng.IntN(40)
		path := make(movement.TimedPath, n)
		var tMs int64
		for i := range path {
			if i > 0 {
				tMs += 1 + rng.Int64N(1600)
			}
			path[i] = movement.Waypoint{X: int32(i), Y: 0, TMs: tMs}
		}
		for k := 0; k < 10; k++ {
			elapsed := rng.Int64N(tMs+2000) - 1000
			i := path.IndexAt(elapsed)
			got := path.PositionAt(elapsed)
			require.Equal(t, path[i].Tile(), got, "PositionAt e IndexAt dicen lo mismo")
			switch {
			case elapsed <= 0:
				require.Equal(t, 0, i, "antes del inicio, el origen")
			default:
				require.LessOrEqual(t, path[i].TMs, elapsed, "nunca un waypoint aún no alcanzado")
				if i+1 < n {
					require.Greater(t, path[i+1].TMs, elapsed, "y siempre el último alcanzado")
				}
			}
		}
	}
}
