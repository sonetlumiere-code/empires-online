package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/auth"
	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/memory"
	ws "github.com/empires-online/empires-online/services/game-server/internal/websocket"
)

const epoch int64 = 1_757_376_000_000

func newHot(t *testing.T) (*memory.HotState, *clock.FakeClock, context.Context) {
	t.Helper()
	clk := clock.NewFakeClock(epoch)
	hot := memory.New(clk, 30*time.Second, 10*time.Second, 120*time.Second, 300*time.Second)
	t.Cleanup(func() { _ = hot.Close() })
	return hot, clk, context.Background()
}

// El estado en memoria debe satisfacer EXACTAMENTE los mismos contratos que la
// implementación de Redis. Si alguno divergiera, el servidor se comportaría
// distinto en desarrollo y en producción, que es la peor clase de bug.
func TestCumpleLosContratosDelServidor(t *testing.T) {
	hot, _, _ := newHot(t)

	var _ ws.PresenceTracker = hot.Presence()
	var _ auth.TicketConsumer = hot.Tickets()
	var _ ws.Deduper = hot.Idempotency()
}

// ─────────────────────────────────────────────────────────────
// Presencia
// ─────────────────────────────────────────────────────────────

func TestPresenciaExpiraSinLatidos(t *testing.T) {
	hot, clk, ctx := newHot(t)
	presence := hot.Presence()

	playerID, sessionID := uuid.New(), uuid.New()

	online, err := presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.False(t, online, "un jugador desconocido no está presente")

	require.NoError(t, presence.MarkOnline(ctx, playerID, sessionID))

	online, err = presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.True(t, online)

	// Justo antes del TTL sigue presente.
	clk.Advance(29 * time.Second)
	online, err = presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.True(t, online)

	// Pasado el TTL, desaparece sola. Ésa es la razón de usar expiración: un
	// proceso que muere de golpe no ejecuta ningún "marcar como offline".
	clk.Advance(2 * time.Second)
	online, err = presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.False(t, online)
}

func TestLatidoRenuevaIndefinidamente(t *testing.T) {
	hot, clk, ctx := newHot(t)
	presence := hot.Presence()

	playerID := uuid.New()
	require.NoError(t, presence.MarkOnline(ctx, playerID, uuid.New()))

	// Diez latidos a 10 s: 100 segundos, muy por encima del TTL de 30 s.
	for i := 0; i < 10; i++ {
		clk.Advance(10 * time.Second)
		renewed, err := presence.Heartbeat(ctx, playerID)
		require.NoError(t, err)
		require.True(t, renewed, "el latido %d debía renovar una clave viva", i)
	}

	online, err := presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.True(t, online)
}

func TestLatidoSobreClaveExpiradaDevuelveFalse(t *testing.T) {
	hot, clk, ctx := newHot(t)
	presence := hot.Presence()

	playerID := uuid.New()
	require.NoError(t, presence.MarkOnline(ctx, playerID, uuid.New()))

	clk.Advance(31 * time.Second)

	renewed, err := presence.Heartbeat(ctx, playerID)
	require.NoError(t, err)
	require.False(t, renewed, "no se puede renovar lo que ya expiró")
}

// Sin la comprobación atómica del valor, cerrar una pestaña vieja marcaría
// offline a un jugador que acaba de reconectar desde otra.
func TestSoloLaSesionPropietariaRetiraLaPresencia(t *testing.T) {
	hot, _, ctx := newHot(t)
	presence := hot.Presence()

	playerID := uuid.New()
	vieja, nueva := uuid.New(), uuid.New()

	require.NoError(t, presence.MarkOnline(ctx, playerID, vieja))
	require.NoError(t, presence.MarkOnline(ctx, playerID, nueva))

	removed, err := presence.ClearIfSession(ctx, playerID, vieja)
	require.NoError(t, err)
	require.False(t, removed, "la sesión antigua no puede desalojar a la nueva")

	online, err := presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.True(t, online)

	removed, err = presence.ClearIfSession(ctx, playerID, nueva)
	require.NoError(t, err)
	require.True(t, removed)

	online, err = presence.IsOnline(ctx, playerID)
	require.NoError(t, err)
	require.False(t, online)
}

func TestSessionOfYCountOnline(t *testing.T) {
	hot, clk, ctx := newHot(t)
	presence := hot.Presence()

	a, b := uuid.New(), uuid.New()
	sessionA := uuid.New()

	require.NoError(t, presence.MarkOnline(ctx, a, sessionA))
	require.NoError(t, presence.MarkOnline(ctx, b, uuid.New()))

	got, found, err := presence.SessionOf(ctx, a)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, sessionA, got)

	n, err := presence.CountOnline(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)

	// Las claves expiradas no se cuentan aunque sigan en el mapa.
	clk.Advance(31 * time.Second)
	n, err = presence.CountOnline(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, n)

	_, found, err = presence.SessionOf(ctx, a)
	require.NoError(t, err)
	require.False(t, found)
}

// ─────────────────────────────────────────────────────────────
// Tickets
// ─────────────────────────────────────────────────────────────

// INV-SEC-003: un ticket sólo puede canjearse una vez.
func TestTicketSoloSeConsumeUnaVez(t *testing.T) {
	hot, clk, ctx := newHot(t)
	tickets := hot.Tickets()

	jti := uuid.NewString()

	first, err := tickets.Consume(ctx, jti)
	require.NoError(t, err)
	require.True(t, first)

	second, err := tickets.Consume(ctx, jti)
	require.NoError(t, err)
	require.False(t, second, "el segundo canje del mismo jti debe rechazarse")

	other, err := tickets.Consume(ctx, uuid.NewString())
	require.NoError(t, err)
	require.True(t, other, "otro jti no se ve afectado")

	// Pasado el TTL del registro, el jti podría reutilizarse — pero para entonces
	// el propio ticket lleva mucho tiempo caducado, así que no abre ninguna puerta.
	clk.Advance(121 * time.Second)
	again, err := tickets.Consume(ctx, jti)
	require.NoError(t, err)
	require.True(t, again)
}

// ─────────────────────────────────────────────────────────────
// Idempotencia
// ─────────────────────────────────────────────────────────────

// INV-SEC-007: un requestId repetido nunca produce un segundo efecto.
func TestIdempotenciaDeduplicaComandos(t *testing.T) {
	hot, _, ctx := newHot(t)
	idem := hot.Idempotency()

	playerID := uuid.New()
	requestID := uuid.NewString()

	fresh, prev, err := idem.Claim(ctx, playerID, requestID)
	require.NoError(t, err)
	require.True(t, fresh)
	require.Empty(t, prev)

	require.NoError(t, idem.Complete(ctx, playerID, requestID, `{"movementId":900}`))

	fresh, prev, err = idem.Claim(ctx, playerID, requestID)
	require.NoError(t, err)
	require.False(t, fresh, "un requestId repetido no puede volver a ejecutarse")
	require.Equal(t, `{"movementId":900}`, prev)
}

func TestIdempotenciaEstaAcotadaPorJugador(t *testing.T) {
	hot, _, ctx := newHot(t)
	idem := hot.Idempotency()

	requestID := uuid.NewString()
	a, b := uuid.New(), uuid.New()

	freshA, _, err := idem.Claim(ctx, a, requestID)
	require.NoError(t, err)
	require.True(t, freshA)

	freshB, _, err := idem.Claim(ctx, b, requestID)
	require.NoError(t, err)
	require.True(t, freshB, "el mismo requestId de otro jugador es un comando distinto")
}

func TestReleaseLiberaLaReserva(t *testing.T) {
	hot, _, ctx := newHot(t)
	idem := hot.Idempotency()

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

func TestIdempotenciaCaducaConSuTTL(t *testing.T) {
	hot, clk, ctx := newHot(t)
	idem := hot.Idempotency()

	playerID := uuid.New()
	requestID := uuid.NewString()

	_, _, err := idem.Claim(ctx, playerID, requestID)
	require.NoError(t, err)

	clk.Advance(301 * time.Second)

	fresh, _, err := idem.Claim(ctx, playerID, requestID)
	require.NoError(t, err)
	require.True(t, fresh, "pasada la ventana, el requestId vuelve a estar libre")
}

// ─────────────────────────────────────────────────────────────
// Concurrencia
// ─────────────────────────────────────────────────────────────

// La reserva debe ser atómica: si N goroutines intentan el mismo requestId a la
// vez, exactamente una puede ganar. Sin esa garantía, dos copias del mismo
// comando reintentado producirían dos movimientos.
func TestReservaConcurrenteSoloLaGanaUna(t *testing.T) {
	hot, _, ctx := newHot(t)
	idem := hot.Idempotency()

	playerID := uuid.New()
	requestID := uuid.NewString()

	const goroutines = 64
	results := make(chan bool, goroutines)
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func() {
			<-start
			fresh, _, err := idem.Claim(ctx, playerID, requestID)
			if err != nil {
				results <- false
				return
			}
			results <- fresh
		}()
	}
	close(start)

	winners := 0
	for i := 0; i < goroutines; i++ {
		if <-results {
			winners++
		}
	}
	require.Equal(t, 1, winners, "exactamente una goroutine debe ganar la reserva")
}

func TestConsumoConcurrenteDeTicketSoloUnaVez(t *testing.T) {
	hot, _, ctx := newHot(t)
	tickets := hot.Tickets()

	jti := uuid.NewString()

	const goroutines = 64
	results := make(chan bool, goroutines)
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func() {
			<-start
			ok, err := tickets.Consume(ctx, jti)
			if err != nil {
				results <- false
				return
			}
			results <- ok
		}()
	}
	close(start)

	winners := 0
	for i := 0; i < goroutines; i++ {
		if <-results {
			winners++
		}
	}
	require.Equal(t, 1, winners, "un ticket no puede canjearse dos veces ni bajo carrera")
}

func TestPingSiempreResponde(t *testing.T) {
	hot, _, ctx := newHot(t)
	require.NoError(t, hot.Ping(ctx))
}
