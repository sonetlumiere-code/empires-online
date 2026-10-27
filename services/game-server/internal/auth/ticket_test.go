package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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

	// Pasado el minuto sigue valiendo mientras quede margen de desfase de
	// reloj (ADR-021): 60 s de TTL más 30 s de ClockSkew.
	clk.Advance(30 * time.Second) // 89 s
	_, err = verifier.Verify(ticket)
	require.NoError(t, err)

	// Agotado el margen, no.
	clk.Advance(2 * time.Second) // 91 s
	_, err = verifier.Verify(ticket)
	require.ErrorIs(t, err, auth.ErrExpiredTicket)
}

// ADR-021: el emisor corre en otra máquina y trunca `iat` a segundos; sin
// margen, un reloj del emisor un segundo por delante rechazaría todos los
// tickets con 4401, indistinguible de un ticket falso. El verificador tolera
// ClockSkew (30 s) en ambos sentidos, y ni un segundo más.
func TestElVerificadorToleraElDesfaseDeReloj(t *testing.T) {
	require.Equal(t, 30*time.Second, auth.ClockSkew)

	firmado := func(t *testing.T, iat, exp time.Time) string {
		t.Helper()
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": uuid.NewString(),
			"jti": uuid.NewString(),
			"aud": auth.Audience,
			"iat": iat.Unix(),
			"exp": exp.Unix(),
		}).SignedString([]byte(testSecret))
		require.NoError(t, err)
		return signed
	}

	clk := clock.NewFakeClock(testEpoch)
	verifier := auth.NewVerifier(testSecret, clk)
	ahora := clk.Now()

	t.Run("iat 20 s en el futuro se acepta", func(t *testing.T) {
		_, err := verifier.Verify(firmado(t, ahora.Add(20*time.Second), ahora.Add(80*time.Second)))
		require.NoError(t, err)
	})

	t.Run("iat 60 s en el futuro se rechaza", func(t *testing.T) {
		_, err := verifier.Verify(firmado(t, ahora.Add(60*time.Second), ahora.Add(120*time.Second)))
		require.ErrorIs(t, err, auth.ErrInvalidTicket)
	})

	t.Run("exp vencido hace 20 s se acepta", func(t *testing.T) {
		_, err := verifier.Verify(firmado(t, ahora.Add(-80*time.Second), ahora.Add(-20*time.Second)))
		require.NoError(t, err)
	})

	t.Run("exp vencido hace 60 s se rechaza", func(t *testing.T) {
		_, err := verifier.Verify(firmado(t, ahora.Add(-120*time.Second), ahora.Add(-60*time.Second)))
		require.ErrorIs(t, err, auth.ErrExpiredTicket)
	})

	t.Run("el margen vale igual para el token de fundación", func(t *testing.T) {
		interno := auth.NewIssuerFor(testSecret, 60*time.Second, clock.NewFakeClock(testEpoch+20_000), auth.InternalAudience)
		token, err := interno.Issue(uuid.New())
		require.NoError(t, err)
		_, err = auth.NewVerifierFor(testSecret, clk, auth.InternalAudience).Verify(token)
		require.NoError(t, err, "emitido 20 s en el futuro por un reloj adelantado")
	})
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

// INV-SEC-008: los dos tokens se firman con el mismo secreto (ADR-018), así que
// sólo la audiencia los separa. Un ticket de juego no lo acepta el verificador
// de la fundación, y un token de fundación no lo acepta el del handshake.
func TestLasAudienciasSonDisjuntas(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	juego := auth.NewIssuer(testSecret, 60*time.Second, clk)
	interno := auth.NewIssuerFor(testSecret, 60*time.Second, clk, auth.InternalAudience)
	verificaJuego := auth.NewVerifier(testSecret, clk)
	verificaInterno := auth.NewVerifierFor(testSecret, clk, auth.InternalAudience)

	id := uuid.New()
	ticket, err := juego.Issue(id)
	require.NoError(t, err)
	fundacion, err := interno.Issue(id)
	require.NoError(t, err)

	_, err = verificaJuego.Verify(ticket)
	require.NoError(t, err)
	_, err = verificaInterno.Verify(fundacion)
	require.NoError(t, err)

	_, err = verificaInterno.Verify(ticket)
	require.ErrorIs(t, err, auth.ErrInvalidTicket, "un ticket de juego no funda")
	_, err = verificaJuego.Verify(fundacion)
	require.ErrorIs(t, err, auth.ErrInvalidTicket, "un token de fundación no abre una sesión")
}

// Contrato entre lenguajes (ADR-018 §2.4): el emisor de apps/web, escrito en
// TypeScript, produce testdata/web-tokens.json, y su propio test exige que siga
// produciendo esos bytes exactos. Aquí se exige que el verificador los acepte:
// el ticket con la audiencia de juego, el token de fundación con la interna, y
// ninguno de los dos con la del otro.
func TestLosTokensDeAppsWebSeVerificanEnGo(t *testing.T) {
	raw, err := os.ReadFile("testdata/web-tokens.json")
	require.NoError(t, err)
	var f struct {
		Secret        string `json:"secret"`
		NowMs         int64  `json:"nowMs"`
		PlayerID      string `json:"playerId"`
		JTI           string `json:"jti"`
		GameTicket    string `json:"gameTicket"`
		FoundingToken string `json:"foundingToken"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	clk := clock.NewFakeClock(f.NowMs + 30_000) // a mitad de su vida

	claims, err := auth.NewVerifier(f.Secret, clk).Verify(f.GameTicket)
	require.NoError(t, err)
	require.Equal(t, f.PlayerID, claims.PlayerID.String())
	require.Equal(t, f.JTI, claims.JTI)
	require.Equal(t, time.Duration(60)*time.Second, claims.Expires.Sub(claims.IssuedAt))

	claims, err = auth.NewVerifierFor(f.Secret, clk, auth.InternalAudience).Verify(f.FoundingToken)
	require.NoError(t, err)
	require.Equal(t, f.PlayerID, claims.PlayerID.String())

	_, err = auth.NewVerifier(f.Secret, clk).Verify(f.FoundingToken)
	require.Error(t, err)
	_, err = auth.NewVerifierFor(f.Secret, clk, auth.InternalAudience).Verify(f.GameTicket)
	require.Error(t, err)

	// Caduca a los 60 s de su emisión, más los 30 s de margen de desfase de
	// reloj que concede el verificador (ADR-021): a los 61 s todavía vale, a
	// los 91 s no.
	_, err = auth.NewVerifier(f.Secret, clock.NewFakeClock(f.NowMs+61_000)).Verify(f.GameTicket)
	require.NoError(t, err)
	_, err = auth.NewVerifier(f.Secret, clock.NewFakeClock(f.NowMs+91_000)).Verify(f.GameTicket)
	require.ErrorIs(t, err, auth.ErrExpiredTicket)
}
