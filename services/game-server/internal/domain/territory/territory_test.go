package territory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Territorio de referencia para las pruebas de pertenencia: (10,20)-(14,24).
var ref = Territory{ID: 1, Name: "Referencia", MinX: 10, MinY: 20, MaxX: 14, MaxY: 24}

func TestPertenenciaIncluyeLosCuatroBordes(t *testing.T) {
	// RN-TERR-001: los límites son inclusivos. Se prueba borde a borde y no con
	// un punto interior porque el error clásico es un `<` donde debe ir un `<=`,
	// y un punto interior no lo detecta.
	casos := []struct {
		nombre string
		x, y   int32
	}{
		{"borde izquierdo", ref.MinX, 22},
		{"borde derecho", ref.MaxX, 22},
		{"borde superior", 12, ref.MinY},
		{"borde inferior", 12, ref.MaxY},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			assert.True(t, ref.Contains(c.x, c.y), "(%d,%d) debe pertenecer", c.x, c.y)
		})
	}
}

func TestPertenenciaIncluyeLasCuatroEsquinas(t *testing.T) {
	esquinas := [][2]int32{
		{ref.MinX, ref.MinY},
		{ref.MaxX, ref.MinY},
		{ref.MinX, ref.MaxY},
		{ref.MaxX, ref.MaxY},
	}
	for _, e := range esquinas {
		assert.True(t, ref.Contains(e[0], e[1]), "la esquina (%d,%d) debe pertenecer", e[0], e[1])
	}
}

func TestPertenenciaExcluyeElTileSiguienteACadaBorde(t *testing.T) {
	// El criterio de aceptación 1 de M6, literal: un tile en max_x + 1 no
	// pertenece.
	casos := []struct {
		nombre string
		x, y   int32
	}{
		{"uno a la izquierda de min_x", ref.MinX - 1, 22},
		{"uno a la derecha de max_x", ref.MaxX + 1, 22},
		{"uno por encima de min_y", 12, ref.MinY - 1},
		{"uno por debajo de max_y", 12, ref.MaxY + 1},
		{"esquina diagonal exterior", ref.MaxX + 1, ref.MaxY + 1},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			assert.False(t, ref.Contains(c.x, c.y), "(%d,%d) NO debe pertenecer", c.x, c.y)
		})
	}
}

func TestUnTerritorioDeUnSoloTileEsValido(t *testing.T) {
	punto := Territory{ID: 1, Name: "Punto", MinX: 5, MinY: 5, MaxX: 5, MaxY: 5}
	assert.True(t, punto.Contains(5, 5))
	assert.False(t, punto.Contains(6, 5))
}

// ─────────────────────────────────────────────────────────────
// Índice
// ─────────────────────────────────────────────────────────────

func TestElIndiceResuelveElEjemploDeLaSpec(t *testing.T) {
	// El mismo mapa dibujado en docs/specs/territory.md §6.3, tile a tile.
	// Si la spec y el código divergen, este test es el que lo dice.
	set, solapes, err := BuildSet([]Territory{
		{ID: 1, Name: "T1", MinX: 0, MinY: 0, MaxX: 2, MaxY: 2},
		{ID: 2, Name: "T2", MinX: 5, MinY: 0, MaxX: 7, MaxY: 1},
		{ID: 3, Name: "T3", MinX: 4, MinY: 3, MaxX: 7, MaxY: 4},
	}, 8, 5, 32)
	require.NoError(t, err)
	require.Empty(t, solapes)

	esperado := []string{
		"111..222",
		"111..222",
		"111.....",
		"....3333",
		"....3333",
	}
	for y, fila := range esperado {
		for x, r := range fila {
			terr, ok := set.TerritoryAt(int32(x), int32(y))
			if r == '.' {
				assert.False(t, ok, "(%d,%d) no debe tener territorio", x, y)
				continue
			}
			if assert.True(t, ok, "(%d,%d) debe tener territorio", x, y) {
				assert.EqualValues(t, r-'0', terr.ID, "en (%d,%d)", x, y)
			}
		}
	}
}

func TestTierraDeNadieNoEsLoMismoQueDueñoNONE(t *testing.T) {
	// RN-TERR-002: un tile puede no pertenecer a NINGÚN territorio, y eso es
	// distinto de pertenecer a uno cuyo owner_type es 'NONE'.
	set, _, err := BuildSet([]Territory{
		{ID: 1, Name: "T1", MinX: 0, MinY: 0, MaxX: 1, MaxY: 1},
	}, 8, 8, 32)
	require.NoError(t, err)

	_, dentro := set.TerritoryAt(0, 0)
	assert.True(t, dentro)

	terr, fuera := set.TerritoryAt(5, 5)
	assert.False(t, fuera, "tierra de nadie")
	assert.Zero(t, terr.ID)
}

func TestConsultaFueraDelMundoNoEntraEnPanico(t *testing.T) {
	// RN-TERR-005. Las coordenadas llegan de mensajes del cliente: un índice
	// fuera de rango aquí sería una forma trivial de tirar el servidor.
	set, _, err := BuildSet([]Territory{
		{ID: 1, Name: "T1", MinX: 0, MinY: 0, MaxX: 1, MaxY: 1},
	}, 8, 8, 32)
	require.NoError(t, err)

	for _, c := range [][2]int32{{-1, 0}, {0, -1}, {8, 0}, {0, 8}, {-99, -99}, {1 << 30, 1 << 30}} {
		assert.NotPanics(t, func() {
			_, ok := set.TerritoryAt(c[0], c[1])
			assert.False(t, ok, "(%d,%d) está fuera del mundo", c[0], c[1])
		})
	}
}

func TestElIndiceEsFuncionPuraDeLosTerritorios(t *testing.T) {
	// INV-TERR-008: reconstruirlo produce el mismo resultado. Se construye con
	// los territorios en orden INVERSO para comprobar que el orden de entrada no
	// influye: BuildSet ordena por id antes de pintar (RN-TERR-003).
	entrada := []Territory{
		{ID: 1, Name: "A", MinX: 0, MinY: 0, MaxX: 3, MaxY: 3},
		{ID: 7, Name: "B", MinX: 10, MinY: 10, MaxX: 12, MaxY: 15},
		{ID: 4, Name: "C", MinX: 5, MinY: 0, MaxX: 6, MaxY: 9},
	}
	invertida := []Territory{entrada[2], entrada[1], entrada[0]}

	uno, _, err := BuildSet(entrada, 32, 32, 32)
	require.NoError(t, err)
	otro, _, err := BuildSet(invertida, 32, 32, 32)
	require.NoError(t, err)

	assert.Equal(t, uno.tiles, otro.tiles, "el índice debe ser idéntico byte a byte")
	assert.Equal(t, uno.All(), otro.All(), "y la iteración debe dar el mismo orden")
}

func TestBuildSetNoReordenaElSliceDelLlamante(t *testing.T) {
	entrada := []Territory{
		{ID: 9, Name: "Z", MinX: 0, MinY: 0, MaxX: 1, MaxY: 1},
		{ID: 2, Name: "A", MinX: 4, MinY: 4, MaxX: 5, MaxY: 5},
	}
	_, _, err := BuildSet(entrada, 32, 32, 32)
	require.NoError(t, err)

	assert.EqualValues(t, 9, entrada[0].ID, "BuildSet no debe ordenar el slice recibido")
}

// ─────────────────────────────────────────────────────────────
// Solapamientos
// ─────────────────────────────────────────────────────────────

func TestDosTerritoriosSolapadosResuelvenElIDMenorYSeReportan(t *testing.T) {
	// RN-TERR-004: gana el id menor, se reporta, y el servidor arranca igual.
	set, solapes, err := BuildSet([]Territory{
		{ID: 3, Name: "Tarde", MinX: 2, MinY: 2, MaxX: 5, MaxY: 5},
		{ID: 1, Name: "Pronto", MinX: 0, MinY: 0, MaxX: 3, MaxY: 3},
	}, 16, 16, 32)
	require.NoError(t, err, "un solapamiento NO debe impedir construir")

	// La intersección es (2,2)-(3,3): 4 tiles, todos del id menor.
	for _, c := range [][2]int32{{2, 2}, {3, 2}, {2, 3}, {3, 3}} {
		terr, ok := set.TerritoryAt(c[0], c[1])
		require.True(t, ok)
		assert.EqualValues(t, 1, terr.ID, "en (%d,%d) debe ganar el id menor", c[0], c[1])
	}
	// Y fuera de la intersección cada uno conserva lo suyo.
	terr, ok := set.TerritoryAt(5, 5)
	require.True(t, ok)
	assert.EqualValues(t, 3, terr.ID)

	require.Len(t, solapes, 1)
	assert.EqualValues(t, 1, solapes[0].Kept)
	assert.EqualValues(t, 3, solapes[0].Discarded)
	assert.Equal(t, 4, solapes[0].Tiles, "debe contar los 4 tiles compartidos")
}

func TestElReporteDeSolapamientosEsDeterminista(t *testing.T) {
	territorios := []Territory{
		{ID: 1, Name: "A", MinX: 0, MinY: 0, MaxX: 4, MaxY: 4},
		{ID: 2, Name: "B", MinX: 3, MinY: 0, MaxX: 6, MaxY: 2},
		{ID: 3, Name: "C", MinX: 4, MinY: 3, MaxX: 8, MaxY: 6},
	}
	primero, solapesA, err := BuildSet(territorios, 16, 16, 32)
	require.NoError(t, err)
	_, solapesB, err := BuildSet([]Territory{territorios[2], territorios[0], territorios[1]}, 16, 16, 32)
	require.NoError(t, err)

	assert.Equal(t, solapesA, solapesB, "el orden de entrada no debe alterar el informe")
	assert.NotEmpty(t, solapesA)
	assert.NotNil(t, primero)
}

// ─────────────────────────────────────────────────────────────
// Huella de chunks
// ─────────────────────────────────────────────────────────────

func TestUnTerritorioQueCruzaFronterasDeChunkMapeaATodosLosSolapados(t *testing.T) {
	// Con chunkSize 32, (30,30)-(65,33) toca los chunks x∈{0,1,2}, y∈{0,1}.
	huella := ChunkFootprint(Territory{ID: 1, MinX: 30, MinY: 30, MaxX: 65, MaxY: 33}, 32)

	assert.ElementsMatch(t, []world.ChunkCoord{
		{CX: 0, CY: 0}, {CX: 1, CY: 0}, {CX: 2, CY: 0},
		{CX: 0, CY: 1}, {CX: 1, CY: 1}, {CX: 2, CY: 1},
	}, huella)
}

func TestUnTerritorioDentroDeUnSoloChunkTieneHuellaDeUno(t *testing.T) {
	huella := ChunkFootprint(Territory{ID: 1, MinX: 1, MinY: 1, MaxX: 30, MaxY: 30}, 32)
	assert.Equal(t, []world.ChunkCoord{{CX: 0, CY: 0}}, huella)
}

func TestLaHuellaUsaDivisionEnteraYNoDesplazamientoDeBits(t *testing.T) {
	// EO_CHUNK_SIZE va de 1 a 256 y no tiene por qué ser potencia de dos. Con
	// chunkSize 10, un `>> 5` daría (0,0) para todo esto y el bug pasaría
	// desapercibido con el 32 por defecto.
	huella := ChunkFootprint(Territory{ID: 1, MinX: 9, MinY: 0, MaxX: 21, MaxY: 9}, 10)

	assert.ElementsMatch(t, []world.ChunkCoord{
		{CX: 0, CY: 0}, {CX: 1, CY: 0}, {CX: 2, CY: 0},
	}, huella)
}

func TestInChunksDevuelveSoloLosTerritoriosQueIntersectan(t *testing.T) {
	set, _, err := BuildSet([]Territory{
		{ID: 1, Name: "Cerca", MinX: 0, MinY: 0, MaxX: 10, MaxY: 10},
		{ID: 2, Name: "Lejos", MinX: 200, MinY: 200, MaxX: 210, MaxY: 210},
	}, 512, 512, 32)
	require.NoError(t, err)

	cerca := set.InChunks([]world.ChunkCoord{{CX: 0, CY: 0}})
	require.Len(t, cerca, 1)
	assert.EqualValues(t, 1, cerca[0].ID)

	// El criterio de aceptación 5: quien no está suscrito a esos chunks no lo ve.
	ninguno := set.InChunks([]world.ChunkCoord{{CX: 9, CY: 9}})
	assert.Empty(t, ninguno)
}

func TestInChunksDevuelveEnOrdenAscendenteDeID(t *testing.T) {
	set, _, err := BuildSet([]Territory{
		{ID: 5, Name: "E", MinX: 0, MinY: 0, MaxX: 1, MaxY: 1},
		{ID: 2, Name: "B", MinX: 4, MinY: 4, MaxX: 5, MaxY: 5},
		{ID: 9, Name: "I", MinX: 8, MinY: 8, MaxX: 9, MaxY: 9},
	}, 64, 64, 32)
	require.NoError(t, err)

	got := set.InChunks([]world.ChunkCoord{{CX: 0, CY: 0}})
	require.Len(t, got, 3)
	assert.EqualValues(t, 2, got[0].ID)
	assert.EqualValues(t, 5, got[1].ID)
	assert.EqualValues(t, 9, got[2].ID)
}

// ─────────────────────────────────────────────────────────────
// Validación de construcción
// ─────────────────────────────────────────────────────────────

func TestUnTerritorioFueraDelMundoNoSePuedeConstruir(t *testing.T) {
	// INV-TERR-001, la mitad que ningún CHECK puede expresar: las dimensiones
	// del mundo son configuración y la migración no las conoce.
	casos := map[string]Territory{
		"se sale por la derecha": {ID: 1, MinX: 0, MinY: 0, MaxX: 32, MaxY: 4},
		"se sale por abajo":      {ID: 1, MinX: 0, MinY: 0, MaxX: 4, MaxY: 32},
		"x negativa":             {ID: 1, MinX: -1, MinY: 0, MaxX: 4, MaxY: 4},
		"y negativa":             {ID: 1, MinX: 0, MinY: -1, MaxX: 4, MaxY: 4},
	}
	for nombre, terr := range casos {
		t.Run(nombre, func(t *testing.T) {
			_, _, err := BuildSet([]Territory{terr}, 32, 32, 32)
			assert.ErrorIs(t, err, ErrRectanguloInvalido)
		})
	}
}

func TestUnRectanguloDesordenadoNoSePuedeConstruir(t *testing.T) {
	_, _, err := BuildSet([]Territory{
		{ID: 1, MinX: 10, MinY: 0, MaxX: 2, MaxY: 4},
	}, 32, 32, 32)
	assert.ErrorIs(t, err, ErrRectanguloInvalido)
}

func TestUnIDQueNoCabeEnElIndiceSeRechazaAlConstruir(t *testing.T) {
	// El índice guarda el id en uint16 y la base lo declara bigint: la
	// restricción no la puede expresar el esquema, así que se comprueba aquí.
	_, _, err := BuildSet([]Territory{
		{ID: MaxTerritories + 1, MinX: 0, MinY: 0, MaxX: 1, MaxY: 1},
	}, 32, 32, 32)
	assert.ErrorIs(t, err, ErrIDFueraDeRango)

	// El id 0 tampoco vale: en el índice denso, 0 significa "ningún territorio".
	_, _, err = BuildSet([]Territory{
		{ID: 0, MinX: 0, MinY: 0, MaxX: 1, MaxY: 1},
	}, 32, 32, 32)
	assert.ErrorIs(t, err, ErrIDFueraDeRango)
}

func TestElIDMaximoSiCabe(t *testing.T) {
	set, _, err := BuildSet([]Territory{
		{ID: MaxTerritories, Name: "Último", MinX: 0, MinY: 0, MaxX: 1, MaxY: 1},
	}, 32, 32, 32)
	require.NoError(t, err)

	terr, ok := set.TerritoryAt(0, 0)
	require.True(t, ok)
	assert.EqualValues(t, MaxTerritories, terr.ID)
}

// ─────────────────────────────────────────────────────────────
// Control
// ─────────────────────────────────────────────────────────────

func TestControlSinDueñoEsValidoSinIDNiFecha(t *testing.T) {
	// RN-TERR-006: el estado que producen los DEFAULT del DDL.
	require.NoError(t, Control{TerritoryID: 1, OwnerType: OwnerNone}.Validate())
}

func TestUnDueñoExigeIdentificadorYFechaDeCaptura(t *testing.T) {
	id := "6d2a1f8e-1c7b-4d1e-9a2f-0b6a5c3d4e5f"
	ahora := time.Now()

	t.Run("PLAYER sin owner_id viola INV-TERR-004", func(t *testing.T) {
		err := Control{TerritoryID: 1, OwnerType: OwnerPlayer, CapturedAt: &ahora}.Validate()
		assert.ErrorIs(t, err, ErrControlInconsistente)
	})

	t.Run("NONE con owner_id viola INV-TERR-004", func(t *testing.T) {
		err := Control{TerritoryID: 1, OwnerType: OwnerNone, OwnerID: &id}.Validate()
		assert.ErrorIs(t, err, ErrControlInconsistente)
	})

	t.Run("PLAYER sin captured_at viola INV-TERR-007", func(t *testing.T) {
		err := Control{TerritoryID: 1, OwnerType: OwnerPlayer, OwnerID: &id}.Validate()
		assert.ErrorIs(t, err, ErrControlInconsistente)
	})

	t.Run("PLAYER completo es válido", func(t *testing.T) {
		err := Control{
			TerritoryID: 1, OwnerType: OwnerPlayer, OwnerID: &id, CapturedAt: &ahora,
		}.Validate()
		assert.NoError(t, err)
	})
}

func TestUnOwnerTypeDesconocidoSeRechaza(t *testing.T) {
	id := "x"
	err := Control{TerritoryID: 1, OwnerType: OwnerType("EMPIRE"), OwnerID: &id}.Validate()
	assert.ErrorIs(t, err, ErrControlInconsistente)
}
