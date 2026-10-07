//go:build integration

// Entorno compartido de los tests de integración.
//
// Estos tests hablan con un PostgreSQL y un Redis REALES. No usan dobles: su
// razón de existir es precisamente comprobar lo que sólo la base de datos puede
// garantizar — constraints, índices únicos parciales, atomicidad transaccional y
// expiración por TTL.
//
// Se ejecutan con:
//
//	EO_INTEGRATION=1 go test -tags=integration ./...
//
// y necesitan la infraestructura levantada:
//
//	pnpm run db:up
//
// Ver ../../../../docs/testing/integration-tests.md
package postgres_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// La base por defecto es la de TESTS, nunca la de desarrollo: estos tests
// vacían todas las tablas. Antes apuntaba a `empires`, y una ejecución sin
// EO_TEST_POSTGRES_URL contra un PostgreSQL en 5432 borraba el mundo de
// desarrollo. La guarda de requireTestDatabase lo impide aunque alguien
// configure mal la variable.
const (
	defaultTestPostgresURL = "postgres://empires:empires_dev_password@localhost:5432/empires_test?sslmode=disable"
	defaultTestRedisURL    = "redis://localhost:6379/1"
)

// requireIntegration salta el test si el entorno de integración no está activado.
//
// El gate explícito evita que la suite falle en la máquina de quien no tenga
// Docker arrancado: un test que no puede ejecutarse debe SALTARSE de forma
// visible, nunca fallar como si el código estuviera roto.
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("EO_INTEGRATION") != "1" {
		t.Skip("tests de integración desactivados: exporta EO_INTEGRATION=1 y levanta la infraestructura con `pnpm run db:up`")
	}
}

func testPostgresURL() string {
	if v := os.Getenv("EO_TEST_POSTGRES_URL"); v != "" {
		return v
	}
	return defaultTestPostgresURL
}

func testRedisURL() string {
	if v := os.Getenv("EO_TEST_REDIS_URL"); v != "" {
		return v
	}
	return defaultTestRedisURL
}

// integrationLockKey es la clave del advisory lock que serializa los tests de
// integración entre PROCESOS. Dentro de un proceso los tests ya van uno detrás
// de otro; pero dos ejecuciones a la vez contra la misma base —una desde
// Windows y otra desde WSL, por ejemplo— se pisaban el TRUNCATE y PostgreSQL
// abortaba una con «deadlock detected». Con el lock se turnan test a test.
const integrationLockKey int64 = 0x454f5f494e54 // "EO_INT"

// requireTestDatabase aborta si la URL no apunta a una base de tests. El
// criterio es el nombre: tiene que terminar en "_test". Es la guarda que
// docs/testing/strategy.md (R10) promete, y lo que separa un fallo de
// configuración de un mundo de desarrollo borrado.
func requireTestDatabase(t *testing.T, url string) {
	t.Helper()
	cfg, err := pgx.ParseConfig(url)
	require.NoError(t, err, "EO_TEST_POSTGRES_URL no es una URL de PostgreSQL válida")
	if !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatalf("la base %q no termina en _test: estos tests vacían todas las tablas y se niegan a "+
			"ejecutarse contra una base que podría ser la de desarrollo. Revisa EO_TEST_POSTGRES_URL", cfg.Database)
	}
}

// lockTestDatabase toma el advisory lock de los tests de integración en una
// conexión propia y lo suelta al terminar el test.
func lockTestDatabase(t *testing.T, url string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, integrationLockKey)
	require.NoError(t, err, "no se pudo tomar el lock de los tests de integración")

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, integrationLockKey)
		_ = conn.Close(ctx)
	})
}

// newTestStore abre un store contra la base de datos de pruebas, aplica las
// migraciones y deja la base limpia.
func newTestStore(t *testing.T) (*postgres.Store, context.Context) {
	t.Helper()
	requireIntegration(t)
	requireTestDatabase(t, testPostgresURL())
	// El lock va antes que todo lo demás, migraciones incluidas, y su Cleanup
	// se ejecuta el último: lo suelta cuando el test ya cerró su store.
	lockTestDatabase(t, testPostgresURL())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	require.NoError(t, postgres.Migrate(testPostgresURL(), discardLogger()),
		"no se pudieron aplicar las migraciones")

	store, err := postgres.New(ctx, testPostgresURL())
	require.NoError(t, err)
	t.Cleanup(store.Close)

	truncateAll(t, ctx, store.Pool())
	return store, ctx
}

// truncateAll deja el mundo vacío conservando los catálogos.
//
// Se trunca en lugar de recrear el esquema: es órdenes de magnitud más rápido y
// mantiene intactas las semillas de civilizations, factions y eras, que son
// datos de juego y no datos de prueba.
func truncateAll(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		TRUNCATE TABLE
			world_events, garrisons, treaties, safe_zones,
			territory_control, territories, idempotency_keys, sessions,
			unit_movements, units, cities, world_chunks, world_state, players
		RESTART IDENTITY CASCADE`)
	require.NoError(t, err, "no se pudo limpiar la base de pruebas")
}
