package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/auth"
	"github.com/empires-online/empires-online/services/game-server/internal/clock"
)

const (
	testSecret = "un-secreto-de-pruebas-suficientemente-largo"
	testEpoch  = int64(1_757_376_000_000)
)

// memoryConsumer emula el registro de tickets canjeados de Redis.
type memoryConsumer struct{ seen map[string]bool }

func newMemoryConsumer() *memoryConsumer { return &memoryConsumer{seen: map[string]bool{}} }

func (m *memoryConsumer) Consume(_ context.Context, jti string) (bool, error) {
	if m.seen[jti] {
		return false, nil
	}
	m.seen[jti] = true
	return true, nil
}

type failingConsumer struct{}

func (failingConsumer) Consume(context.Context, string) (bool, error) {
	return false, errors.New("redis caído")
}

func TestTicketValidoSeVerifica(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	issuer := auth.NewIssuer(testSecret, 60*time.Second, clk)
	verifier := auth.NewVerifier(testSecret, clk)

	playerID := uuid.New()
	ticket, err := issuer.Issue(playerID)
	require.NoError(t, err)

	claims, err := verifier.Verify(ticket)
	require.NoError(t, err)
	require.Equal(t, playerID, claims.PlayerID)
	require.NotEmpty(t, claims.JTI, "sin jti no se puede impedir el replay")
}

func TestTicketCaducado(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	issuer := auth.NewIssuer(testSecret, 60*time.Second, clk)
	verifier := auth.NewVerifier(testSecret, clk)

	ticket, err := issuer.Issue(uuid.New())
	require.NoError(t, err)

	// Justo antes de caducar sigue valiendo.
	clk.Advance(59 * time.Second)
	_, err = verifier.Verify(ticket)
	require.NoError(t, err)

	// Pasado el minuto, no.
	clk.Advance(2 * time.Second)
	_, err = verifier.Verify(ticket)
	require.ErrorIs(t, err, auth.ErrExpiredTicket)
}

func TestTicketFirmadoConOtroSecreto(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	otro := auth.NewIssuer("un-secreto-distinto-igualmente-largo-xx", 60*time.Second, clk)
	verifier := auth.NewVerifier(testSecret, clk)

	ticket, err := otro.Issue(uuid.New())
	require.NoError(t, err)

	_, err = verifier.Verify(ticket)
	require.ErrorIs(t, err, auth.ErrInvalidTicket)
}

// Un atacante no puede eludir la firma declarando alg=none.
func TestAlgoritmoNoneSeRechaza(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	verifier := auth.NewVerifier(testSecret, clk)

	token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": uuid.NewString(),
		"jti": uuid.NewString(),
		"aud": auth.Audience,
		"exp": clk.Now().Add(time.Minute).Unix(),
		"iat": clk.Now().Unix(),
	})
	unsigned, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = verifier.Verify(unsigned)
	require.ErrorIs(t, err, auth.ErrInvalidTicket)
}

// Un ticket emitido para otro servicio no sirve aquí.
func TestAudienciaIncorrecta(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	verifier := auth.NewVerifier(testSecret, clk)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uuid.NewString(),
		"jti": uuid.NewString(),
		"aud": "otro-servicio",
		"exp": clk.Now().Add(time.Minute).Unix(),
		"iat": clk.Now().Unix(),
	})
	signed, err := token.SignedString([]byte(testSecret))
	require.NoError(t, err)

	_, err = verifier.Verify(signed)
	require.ErrorIs(t, err, auth.ErrInvalidTicket)
}

func TestClaimsIncompletos(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	verifier := auth.NewVerifier(testSecret, clk)

	cases := map[string]jwt.MapClaims{
		"sin subject": {
			"jti": uuid.NewString(), "aud": auth.Audience,
			"exp": clk.Now().Add(time.Minute).Unix(), "iat": clk.Now().Unix(),
		},
		"sin jti": {
			"sub": uuid.NewString(), "aud": auth.Audience,
			"exp": clk.Now().Add(time.Minute).Unix(), "iat": clk.Now().Unix(),
		},
		"subject que no es uuid": {
			"sub": "pepe", "jti": uuid.NewString(), "aud": auth.Audience,
			"exp": clk.Now().Add(time.Minute).Unix(), "iat": clk.Now().Unix(),
		},
		"sin caducidad": {
			"sub": uuid.NewString(), "jti": uuid.NewString(), "aud": auth.Audience,
			"iat": clk.Now().Unix(),
		},
	}

	for name, claims := range cases {
		t.Run(name, func(t *testing.T) {
			signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
			require.NoError(t, err)
			_, err = verifier.Verify(signed)
			require.Error(t, err, "un ticket sin %s debe rechazarse", name)
		})
	}
}

// INV-SEC-003: un ticket sólo puede canjearse una vez.
func TestTicketNoSePuedeReutilizar(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	issuer := auth.NewIssuer(testSecret, 60*time.Second, clk)
	authenticator := auth.NewAuthenticator(auth.NewVerifier(testSecret, clk), newMemoryConsumer())

	playerID := uuid.New()
	ticket, err := issuer.Issue(playerID)
	require.NoError(t, err)

	claims, err := authenticator.Authenticate(context.Background(), ticket)
	require.NoError(t, err)
	require.Equal(t, playerID, claims.PlayerID)

	_, err = authenticator.Authenticate(context.Background(), ticket)
	require.ErrorIs(t, err, auth.ErrReplayedTicket,
		"el segundo canje del mismo ticket debe fallar")
}

func TestCadaTicketTieneUnJtiDistinto(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	issuer := auth.NewIssuer(testSecret, 60*time.Second, clk)
	verifier := auth.NewVerifier(testSecret, clk)

	playerID := uuid.New()
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		ticket, err := issuer.Issue(playerID)
		require.NoError(t, err)
		claims, err := verifier.Verify(ticket)
		require.NoError(t, err)
		require.False(t, seen[claims.JTI], "el jti se repitió en la iteración %d", i)
		seen[claims.JTI] = true
	}
}

// Si no se puede comprobar la unicidad, se falla cerrado: sin garantía anti-replay
// no se abre la sesión.
func TestFalloDelRegistroDeTicketsFallaCerrado(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	issuer := auth.NewIssuer(testSecret, 60*time.Second, clk)
	authenticator := auth.NewAuthenticator(auth.NewVerifier(testSecret, clk), failingConsumer{})

	ticket, err := issuer.Issue(uuid.New())
	require.NoError(t, err)

	_, err = authenticator.Authenticate(context.Background(), ticket)
	require.Error(t, err)
}

func TestTicketBasuraSeRechaza(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	verifier := auth.NewVerifier(testSecret, clk)

	for _, garbage := range []string{"", "no-es-un-jwt", "a.b.c", "Bearer xyz"} {
		_, err := verifier.Verify(garbage)
		require.Error(t, err, "debía rechazarse: %q", garbage)
	}
}
