package migrations

import (
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var nombreDeMigracion = regexp.MustCompile(`^(\d{6})_([a-z0-9_]+)\.(up|down)\.sql$`)

// INV-PERSIST-005: los prefijos son correlativos desde 000001, sin huecos ni
// repetidos, y cada .up.sql tiene su .down.sql con el mismo nombre. Se lee el
// FS embebido, que es exactamente lo que viaja en el binario.
func TestLasMigracionesSonCorrelativasYCompletas(t *testing.T) {
	entradas, err := FS.ReadDir(".")
	require.NoError(t, err)

	nombres := map[int]string{}
	sentidos := map[int]map[string]bool{}
	for _, e := range entradas {
		m := nombreDeMigracion.FindStringSubmatch(e.Name())
		require.NotNil(t, m, "%q no sigue el formato NNNNNN_nombre.(up|down).sql", e.Name())
		n, err := strconv.Atoi(m[1])
		require.NoError(t, err)
		if previo, ok := nombres[n]; ok {
			assert.Equal(t, previo, m[2], "el prefijo %06d tiene dos nombres", n)
		}
		nombres[n] = m[2]
		if sentidos[n] == nil {
			sentidos[n] = map[string]bool{}
		}
		sentidos[n][m[3]] = true
	}

	var prefijos []int
	for n := range nombres {
		prefijos = append(prefijos, n)
	}
	sort.Ints(prefijos)
	require.NotEmpty(t, prefijos)
	for i, n := range prefijos {
		assert.Equal(t, i+1, n, "hueco o salto en la numeración: se esperaba %06d", i+1)
		assert.True(t, sentidos[n]["up"], "%06d_%s no tiene .up.sql", n, nombres[n])
		assert.True(t, sentidos[n]["down"], "%06d_%s no tiene .down.sql", n, nombres[n])
	}
	t.Logf("%d migraciones", len(prefijos))
}
