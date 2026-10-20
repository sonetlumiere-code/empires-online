package websocket_test

import (
	"strings"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
	ws "github.com/empires-online/empires-online/services/game-server/internal/websocket"
)

// codigoDeCierre lee hasta que el servidor cierra y devuelve el código.
func codigoDeCierre(t *testing.T, c *conn, plazo time.Duration) int {
	t.Helper()
	require.NoError(t, c.c.SetReadDeadline(time.Now().Add(plazo)))
	for {
		_, _, err := c.c.ReadMessage()
		if err == nil {
			continue
		}
		var cierre *gws.CloseError
		require.ErrorAs(t, err, &cierre, "se esperaba un cierre del servidor, llegó %v", err)
		return cierre.Code
	}
}

// INV-SEC-002: un socket que no se autentica a tiempo se cierra con 4408.
func TestSinHandshakeEnPlazoSeCierraCon4408(t *testing.T) {
	env := newE2ECon(t, nil, func(c *ws.Config) { c.HandshakeTimeout = 200 * time.Millisecond })
	c := env.dial()

	require.Equal(t, protocol.CloseHandshakeTimeout, codigoDeCierre(t, c, 3*time.Second))
}

// INV-SEC-005: un frame por encima de EO_WS_MAX_MESSAGE_BYTES lo corta el
// transporte con 1009, antes y después del handshake. No hay system.error: el
// frame no se llega a leer.
func TestUnMensajeDemasiadoGrandeCierraCon1009(t *testing.T) {
	const limite = 1024
	grande := `{"v":1,"type":"session.ping","requestId":"r","payload":{"pad":"` + strings.Repeat("x", limite) + `"}}`

	t.Run("antes del handshake", func(t *testing.T) {
		env := newE2ECon(t, nil, func(c *ws.Config) { c.MaxMessageBytes = limite })
		c := env.dial()
		require.NoError(t, c.c.WriteMessage(gws.TextMessage, []byte(grande)))
		require.Equal(t, gws.CloseMessageTooBig, codigoDeCierre(t, c, 3*time.Second))
	})

	t.Run("con la sesión establecida", func(t *testing.T) {
		env := newE2ECon(t, nil, func(c *ws.Config) { c.MaxMessageBytes = limite })
		ticket, err := env.issuer.Issue(env.playerID)
		require.NoError(t, err)
		c := env.dial()
		c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
		c.await(protocol.TypeSessionWelcome, 3*time.Second)
		env.pump()
		c.await(protocol.TypeWorldSnapshot, 3*time.Second)

		require.NoError(t, c.c.WriteMessage(gws.TextMessage, []byte(grande)))
		require.Equal(t, gws.CloseMessageTooBig, codigoDeCierre(t, c, 3*time.Second))
	})
}

// INV-SEC-005: el token bucket absorbe el burst y, agotado, responde
// RATE_LIMITED y cierra con 4429. No hay modo «descartar y seguir».
func TestUnaRafagaPorEncimaDelBurstCierraCon4429(t *testing.T) {
	env := newE2E(t)
	ticket, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)
	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
	env.pump()
	c.await(protocol.TypeWorldSnapshot, 3*time.Second)

	// 40 de burst más lo que rellene la tasa mientras se envían: 60 en ráfaga
	// bastan con margen en cualquier máquina.
	for i := 0; i < 60; i++ {
		if err := c.c.WriteMessage(gws.TextMessage, []byte(
			`{"v":1,"type":"session.ping","requestId":"`+uuidN(i)+`","payload":{"clientTimeMs":1}}`)); err != nil {
			break // el servidor ya cerró
		}
	}

	limitado := c.await(protocol.TypeSystemError, 3*time.Second)
	for decode[protocol.SystemErrorPayload](t, limitado.Payload).Code != protocol.CodeRateLimited {
		limitado = c.await(protocol.TypeSystemError, 3*time.Second)
	}
	require.Equal(t, protocol.CloseRateLimited, codigoDeCierre(t, c, 3*time.Second))
}

func uuidN(i int) string {
	const base = "00000000-0000-4000-8000-000000000000"
	s := []byte(base)
	for p, n := len(s)-1, i; n > 0; p, n = p-1, n/10 {
		s[p] = byte('0' + n%10)
	}
	return string(s)
}
