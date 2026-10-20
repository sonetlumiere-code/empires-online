//go:build integration

package postgres_test

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
	"github.com/empires-online/empires-online/services/game-server/migrations"
)

// ultimaMigracion es el prefijo más alto del FS embebido: la versión a la que
// tiene que quedar el esquema.
func ultimaMigracion(t *testing.T) uint {
	t.Helper()
	entradas, err := fs.ReadDir(migrations.FS, ".")
	require.NoError(t, err)
	var max uint
	for _, e := range entradas {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		n, err := strconv.Atoi(e.Name()[:6])
		require.NoError(t, err)
		if uint(n) > max {
			max = uint(n)
		}
	}
	return max
}

// INV-PERSIST-005: volver a ejecutar el migrador no aplica nada ni cambia el
// esquema, y la versión registrada es la última migración embebida.
func TestElMigradorEsIdempotente(t *testing.T) {
	store, ctx := newTestStore(t) // ya migró una vez
	url := testPostgresURL()

	huella := func() string {
		var h string
		require.NoError(t, store.Pool().QueryRow(ctx,
			`SELECT string_agg(table_name || '.' || column_name || ':' || data_type, ',' ORDER BY table_name, column_name)
			   FROM information_schema.columns WHERE table_schema = 'public'`).Scan(&h))
		return h
	}
	antes := huella()
	v1, dirty, err := postgres.SchemaVersion(url)
	require.NoError(t, err)
	require.False(t, dirty)
	assert.Equal(t, ultimaMigracion(t), v1)

	require.NoError(t, postgres.Migrate(url, discardLogger()))
	v2, dirty, err := postgres.SchemaVersion(url)
	require.NoError(t, err)
	assert.False(t, dirty)
	assert.Equal(t, v1, v2, "la segunda ejecución no aplica nada")
	assert.Equal(t, antes, huella(), "ni cambia el esquema")
}

// Un esquema marcado dirty —una migración que falló a medias— hace fallar el
// migrador y pide intervención manual en lugar de adivinar qué quedó aplicado.
func TestUnEsquemaDirtyAbortaElMigrador(t *testing.T) {
	store, ctx := newTestStore(t)
	url := testPostgresURL()

	// La base de tests es compartida: el Cleanup se registra ANTES de marcarla,
	// para que el esquema vuelva a estar limpio aunque el test falle.
	t.Cleanup(func() {
		_, err := store.Pool().Exec(ctx, `UPDATE schema_migrations SET dirty = false`)
		require.NoError(t, err)
	})
	_, err := store.Pool().Exec(ctx, `UPDATE schema_migrations SET dirty = true`)
	require.NoError(t, err)

	err = postgres.Migrate(url, discardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dirty")
	assert.Contains(t, err.Error(), "intervención manual")
}
