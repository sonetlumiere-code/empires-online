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
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

const (
	defaultTestPostgresURL = "postgres://empires:empires_dev_password@localhost:5432/empires?sslmode=disable"
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

// newTestStore abre un store contra la base de datos de pruebas, aplica las
// migraciones y deja la base limpia.
func newTestStore(t *testing.T) (*postgres.Store, context.Context) {
	t.Helper()
	requireIntegration(t)

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
