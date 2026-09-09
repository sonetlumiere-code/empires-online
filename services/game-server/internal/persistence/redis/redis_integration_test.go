//go:build integration

// Tests de integración contra un Redis REAL.
//
// Lo que se prueba aquí no puede probarse con un doble: la expiración por TTL, la
// atomicidad de SETNX y la del script Lua que retira la presencia sólo si
// pertenece a la sesión indicada. Un mock que "simule" TTL probaría el mock.
//
//	EO_INTEGRATION=1 go test -tags=integration ./internal/persistence/redis/...
//
// Ver ../../../../docs/testing/integration-tests.md
package redis_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	redisstore "github.com/empires-online/empires-online/services/game-server/internal/persistence/redis"
)

const defaultTestRedisURL = "redis://localhost:6379/1"

func newTestClient(t *testing.T) (*redisstore.Client, context.Context) {
	t.Helper()
	if os.Getenv("EO_INTEGRATION") != "1" {
		t.Skip("tests de integración desactivados: exporta EO_INTEGRATION=1 y levanta la infraestructura con `pnpm run db:up`")
	}

	url := os.Getenv("EO_TEST_REDIS_URL")
	if url == "" {
		url = defaultTestRedisURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	client, err := redisstore.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	// La base 1 es exclusiva de los tests: vaciarla es seguro.
	require.NoError(t, client.Raw().FlushDB(ctx).Err())
	return client, ctx
}

// ─────────────────────────────────────────────────────────────
// Presencia
// ─────────────────────────────────────────────────────────────

func TestPresenciaSeRegistraYExpira(t *testing.T) {
	client, ctx := newTestClient(t)
	// TTL corto para no alargar el test; el latido debe ser menor que el TTL.
	presence := redisstore.NewPresenceStore(client, 2*time.Second, 1*time.Second)

	playerID, sessionID := uuid.New(), uuid.New()

	online, err := presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.False(t, online, "un jugador desconocido no está presente")

	require.NoError(t, presence.MarkOnline(ctx, playerID, sessionID))

	online, err = presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.True(t, online)

	got, found, err := presence.SessionOf(ctx, playerID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, sessionID, got)

	// Sin latidos, la clave desaparece sola. Ésta es la razón de usar TTL: un
	// proceso que muere de golpe no ejecuta ningún "marcar como offline".
	time.Sleep(2500 * time.Millisecond)

	online, err = presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.False(t, online, "sin latidos la presencia debe expirar por sí sola")
}

func TestLatidoRenuevaLaPresencia(t *testing.T) {
	client, ctx := newTestClient(t)
	presence := redisstore.NewPresenceStore(client, 2*time.Second, 500*time.Millisecond)

	playerID, sessionID := uuid.New(), uuid.New()
	require.NoError(t, presence.MarkOnline(ctx, playerID, sessionID))

	// Cuatro latidos a lo largo de 2 segundos: más allá del TTL original.
	for i := 0; i < 4; i++ {
		time.Sleep(500 * time.Millisecond)
		renewed, err := presence.Heartbeat(ctx, playerID)
		require.NoError(t, err)
		require.True(t, renewed, "el latido %d debía renovar una clave viva", i)
	}

	online, err := presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.True(t, online, "con latidos regulares la presencia se mantiene")
}

func TestLatidoSobreClaveExpiradaDevuelveFalse(t *testing.T) {
	client, ctx := newTestClient(t)
	presence := redisstore.NewPresenceStore(client, 1*time.Second, 500*time.Millisecond)

	playerID := uuid.New()
	require.NoError(t, presence.MarkOnline(ctx, playerID, uuid.New()))

	time.Sleep(1500 * time.Millisecond)

	renewed, err := presence.Heartbeat(ctx, playerID)
	require.NoError(t, err)
	require.False(t, renewed, "no se puede renovar lo que ya expiró: la sesión debe re-registrarse")
}

// Sin la comprobación atómica del script Lua, cerrar una pestaña vieja marcaría
// offline a un jugador que acaba de reconectar desde otra.
func TestSoloLaSesionPropietariaRetiraLaPresencia(t *testing.T) {
	client, ctx := newTestClient(t)
	presence := redisstore.NewPresenceStore(client, 30*time.Second, 10*time.Second)

	playerID := uuid.New()
	vieja, nueva := uuid.New(), uuid.New()

	require.NoError(t, presence.MarkOnline(ctx, playerID, vieja))
	// El jugador reconecta desde otra pestaña: la presencia pasa a la sesión nueva.
	require.NoError(t, presence.MarkOnline(ctx, playerID, nueva))

	// Ahora se cierra la pestaña vieja. NO debe retirar la presencia.
	removed, err := presence.ClearIfSession(ctx, playerID, vieja)
	require.NoError(t, err)
	require.False(t, removed, "la sesión antigua no puede desalojar a la nueva")

	online, err := presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.True(t, online, "el jugador sigue conectado desde la sesión nueva")

	// Y la sesión propietaria sí la retira.
	removed, err = presence.ClearIfSession(ctx, playerID, nueva)
	require.NoError(t, err)
	require.True(t, removed)

	online, err = presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.False(t, online)
}

// ─────────────────────────────────────────────────────────────
// Tickets: protección contra replay
// ─────────────────────────────────────────────────────────────

// INV-SEC-003: un ticket sólo puede canjearse una vez.
func TestTicketSoloSeConsumeUnaVez(t *testing.T) {
	client, ctx := newTestClient(t)
	tickets := redisstore.NewTicketStore(client, 2*time.Minute)

	jti := uuid.NewString()

	first, err := tickets.Consume(ctx, jti)
	require.NoError(t, err)
	require.True(t, first, "el primer canje debe aceptarse")

	second, err := tickets.Consume(ctx, jti)
	require.NoError(t, err)
	require.False(t, second, "el segundo canje del mismo jti debe rechazarse")

	// Un jti distinto no se ve afectado.
	other, err := tickets.Consume(ctx, uuid.NewString())
	require.NoError(t, err)
	require.True(t, other)
}

// ─────────────────────────────────────────────────────────────
// Idempotencia
// ─────────────────────────────────────────────────────────────

// INV-SEC-007: un requestId repetido nunca produce un segundo efecto.
func TestIdempotenciaDeduplicaComandos(t *testing.T) {
	client, ctx := newTestClient(t)
	idem := redisstore.NewIdempotencyStore(client, 5*time.Minute)

	playerID := uuid.New()
	requestID := uuid.NewString()

	fresh, prev, err := idem.Claim(ctx, playerID, requestID)
	require.NoError(t, err)
	require.True(t, fresh, "la primera vez el comando debe ejecutarse")
	require.Empty(t, prev)

	require.NoError(t, idem.Complete(ctx, playerID, requestID, `{"movementId":900}`))

	// El reintento del cliente tras un corte de red devuelve la respuesta original.
	fresh, prev, err = idem.Claim(ctx, playerID, requestID)
	require.NoError(t, err)
	require.False(t, fresh, "un requestId repetido no puede volver a ejecutarse")
	require.Equal(t, `{"movementId":900}`, prev)
}

// El requestId se acota por jugador: dos jugadores distintos pueden usar el mismo
// identificador sin interferir.
func TestIdempotenciaEstaAcotadaPorJugador(t *testing.T) {
	client, ctx := newTestClient(t)
	idem := redisstore.NewIdempotencyStore(client, 5*time.Minute)

	requestID := uuid.NewString()
	a, b := uuid.New(), uuid.New()

	freshA, _, err := idem.Claim(ctx, a, requestID)
	require.NoError(t, err)
	require.True(t, freshA)

	freshB, _, err := idem.Claim(ctx, b, requestID)
	require.NoError(t, err)
	require.True(t, freshB, "el mismo requestId de otro jugador es un comando distinto")
}

// Un fallo transitorio libera la reserva para que el cliente pueda reintentar con
// el mismo requestId.
func TestReleaseLiberaLaReserva(t *testing.T) {
	client, ctx := newTestClient(t)
	idem := redisstore.NewIdempotencyStore(client, 5*time.Minute)

	playerID := uuid.New()
	requestID := uuid.NewString()

	fresh, _, err := idem.Claim(ctx, playerID, requestID)
	require.NoError(t, err)
	require.True(t, fresh)

	require.NoError(t, idem.Release(ctx, playerID, requestID))

	fresh, _, err = idem.Claim(ctx, playerID, requestID)
	require.NoError(t, err)
	require.True(t, fresh, "tras liberar la reserva el reintento debe poder ejecutarse")
}

func TestPingYCierre(t *testing.T) {
	client, ctx := newTestClient(t)
	require.NoError(t, client.Ping(ctx))
}
