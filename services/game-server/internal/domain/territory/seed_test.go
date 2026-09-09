package territory

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLaRejillaCubreElMundoSinHuecosNiSolapes(t *testing.T) {
	const w, h = 100, 60

	sembrados, err := SeedGrid(w, h, 32)
	require.NoError(t, err)

	// Se les asignan los ids que daría la base para poder construir el índice.
	conID := make([]Territory, len(sembrados))
	for i, s := range sembrados {
		s.ID = int64(i + 1)
		conID[i] = s
	}
	set, solapes, err := BuildSet(conID, w, h, 32)
	require.NoError(t, err)
	assert.Empty(t, solapes, "la rejilla no debe solaparse consigo misma")

	// Ni un solo tile del mundo puede quedar sin territorio.
	for y := int32(0); y < h; y++ {
		for x := int32(0); x < w; x++ {
			if _, ok := set.TerritoryAt(x, y); !ok {
				t.Fatalf("el tile (%d,%d) quedó sin territorio", x, y)
			}
		}
	}
}

func TestLaUltimaColumnaYFilaSeEstrechanEnVezDeDesbordar(t *testing.T) {
	// 100 no es múltiplo de 32: la última columna cubre 96..99 y la última fila
	// 32..59. Desbordar violaría INV-TERR-001 y el servidor no arrancaría.
	sembrados, err := SeedGrid(100, 60, 32)
	require.NoError(t, err)

	for _, s := range sembrados {
		assert.Less(t, s.MaxX, int32(100), "%s se sale por la derecha", s.Name)
		assert.Less(t, s.MaxY, int32(60), "%s se sale por abajo", s.Name)
	}

	ultimo := sembrados[len(sembrados)-1]
	assert.EqualValues(t, 99, ultimo.MaxX)
	assert.EqualValues(t, 59, ultimo.MaxY)
}

func TestLaRejillaDelMundoPorDefectoDa64Territorios(t *testing.T) {
	sembrados, err := SeedGrid(512, 512, DefaultSeedSize)
	require.NoError(t, err)
	assert.Len(t, sembrados, 64, "8 × 8 con el lado por defecto")
}

func TestElOrdenDeSiembraEsPorFilas(t *testing.T) {
	// El orden del slice es el orden de inserción, y por tanto el orden en que
	// la identidad de PostgreSQL reparte los ids. Si esto cambiara, dos siembras
	// del mismo mundo darían ids distintos para la misma geometría.
	sembrados, err := SeedGrid(64, 64, 32)
	require.NoError(t, err)
	require.Len(t, sembrados, 4)

	assert.Equal(t, []string{"Región 0-0", "Región 1-0", "Región 0-1", "Región 1-1"},
		[]string{sembrados[0].Name, sembrados[1].Name, sembrados[2].Name, sembrados[3].Name})
}

func TestLaSiembraEsDeterminista(t *testing.T) {
	uno, err := SeedGrid(300, 200, 64)
	require.NoError(t, err)
	otro, err := SeedGrid(300, 200, 64)
	require.NoError(t, err)
	assert.Equal(t, uno, otro)
}

func TestUnMundoMasPequeñoQueUnTerritorioDaUnoSolo(t *testing.T) {
	sembrados, err := SeedGrid(10, 10, 64)
	require.NoError(t, err)
	require.Len(t, sembrados, 1)
	assert.Equal(t, Territory{Name: "Región 0-0", MinX: 0, MinY: 0, MaxX: 9, MaxY: 9}, sembrados[0])
}

func TestUnaRejillaQueNoCabeEnElIndiceSeRechaza(t *testing.T) {
	// Con lado 1 sobre 512 × 512 saldrían 262144 territorios y el índice denso
	// admite 65535. Falla al sembrar, no en el arranque siguiente: un error lejos
	// de su causa es mucho más caro de diagnosticar.
	_, err := SeedGrid(512, 512, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprint(MaxTerritories))
}

func TestParametrosInvalidosSeRechazan(t *testing.T) {
	for nombre, args := range map[string][3]int32{
		"ancho cero":    {0, 10, 32},
		"alto negativo": {10, -1, 32},
		"lado cero":     {10, 10, 0},
	} {
		t.Run(nombre, func(t *testing.T) {
			_, err := SeedGrid(args[0], args[1], args[2])
			assert.Error(t, err)
		})
	}
}
