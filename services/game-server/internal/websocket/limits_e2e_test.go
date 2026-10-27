package websocket_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
	ws "github.com/empires-online/empires-online/services/game-server/internal/websocket"
)

// codigoDeCierre lee hasta que el servidor cierra y devuelve el código.
func codigoDeCierre(t *testing.T, c *conn, plazo time.Duration) int {
	t.Helper()
	require.NoError(t, c.c.SetReadDeadline(time.Now().Add(plazo)))
	for {
		_, err := c.read()
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

// ADR-019: sin sesión no hay mensajes. Un primer mensaje ilegible, o de una
// versión no soportada, cierra con 4400 y la PRIMERA lectura es ese cierre: un
// system.error previo haría fallar el test.
func TestUnHandshakeIlegibleCierraCon4400SinMensaje(t *testing.T) {
	casos := map[string]string{
		"JSON malformado":      `{esto no es json`,
		"sin ticket":           `{"v":1,"type":"session.hello","requestId":"00000000-0000-4000-8000-000000000001","payload":{}}`,
		"versión no soportada": `{"v":2,"type":"session.hello","requestId":"00000000-0000-4000-8000-000000000001","payload":{"ticket":"x"}}`,
	}
	for nombre, primero := range casos {
		t.Run(nombre, func(t *testing.T) {
			env := newE2E(t)
			c := env.dial()
			require.NoError(t, c.c.WriteMessage(gws.TextMessage, []byte(primero)))

			require.NoError(t, c.c.SetReadDeadline(time.Now().Add(3*time.Second)))
			_, err := c.read()
			var cierre *gws.CloseError
			require.ErrorAs(t, err, &cierre, "lo primero que llega debe ser el cierre, no un mensaje")
			require.Equal(t, protocol.CloseInvalidMessage, cierre.Code)
		})
	}
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

// EO-121: el tipo de un mensaje entrante lo escribe el cliente. Contado tal
// cual, cada tipo inventado sería una serie nueva de eo_ws_messages_total, para
// siempre. Fuera del catálogo, todos cuentan como `unknown`.
func TestLosTiposInventadosNoCreanSeriesDeMetricas(t *testing.T) {
	env := newE2ECon(t, nil, func(c *ws.Config) {
		c.RateLimitBurst = 500
		c.RateLimitPerSec = 500
	})
	ticket, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)
	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
	env.pump()
	c.await(protocol.TypeWorldSnapshot, 3*time.Second)

	const inventados = 100
	for i := 0; i < inventados; i++ {
		c.send(fmt.Sprintf("inventado.%d", i), map[string]any{})
	}
	for i := 0; i < inventados; i++ {
		rechazo := c.await(protocol.TypeSystemError, 3*time.Second)
		require.Equal(t, protocol.CodeInvalidMessage,
			decode[protocol.SystemErrorPayload](t, rechazo.Payload).Code)
	}
	// Un tipo del catálogo conserva su nombre.
	c.send(protocol.TypeSessionPing, map[string]any{"clientTimeMs": 1})
	c.await(protocol.TypeSessionPong, 3*time.Second)

	entrantes := map[string]float64{}
	familias, err := env.metrics.Registry().Gather()
	require.NoError(t, err)
	for _, f := range familias {
		if f.GetName() != "eo_ws_messages_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["direction"] == observability.DirectionInbound {
				entrantes[labels["type"]] = m.GetCounter().GetValue()
			}
		}
	}
	require.Equal(t, map[string]float64{
		observability.TypeUnknown: inventados,
		protocol.TypeSessionPing:  1,
	}, entrantes)
}

func uuidN(i int) string {
	const base = "00000000-0000-4000-8000-000000000000"
	s := []byte(base)
	for p, n := len(s)-1, i; n > 0; p, n = p-1, n/10 {
		s[p] = byte('0' + n%10)
	}
	return string(s)
}

// ─────────────────────────────────────────────────────────────
// Tope de conexiones (ADR-021)
// ─────────────────────────────────────────────────────────────

// rechazosPorMotivo lee eo_ws_connections_rejected_total{reason}.
func rechazosPorMotivo(t *testing.T, env *e2e) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	familias, err := env.metrics.Registry().Gather()
	require.NoError(t, err)
	for _, f := range familias {
		if f.GetName() != "eo_ws_connections_rejected_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "reason" {
					out[l.GetValue()] = m.GetCounter().GetValue()
				}
			}
		}
	}
	return out
}

// rechazadoAntesDelUpgrade comprueba que el dial no abrió socket y devolvió el
// código HTTP esperado con Retry-After.
func rechazadoAntesDelUpgrade(t *testing.T, env *e2e, h http.Header, status int) {
	t.Helper()
	c, resp, err := env.dialConCabeceras(h)
	require.ErrorIs(t, err, gws.ErrBadHandshake)
	require.Nil(t, c)
	require.NotNil(t, resp)
	require.Equal(t, status, resp.StatusCode)
	require.Equal(t, "5", resp.Header.Get("Retry-After"))
}

// Las series de rechazo existen a cero desde el arranque: una alerta sobre
// rate() de una serie inexistente no dispararía nunca.
func TestLasSeriesDeRechazoExistenACeroDesdeElArranque(t *testing.T) {
	env := newE2E(t)
	require.Equal(t, map[string]float64{
		observability.RejectOrigin:              0,
		observability.RejectMaxConnections:      0,
		observability.RejectMaxConnectionsPerIP: 0,
	}, rechazosPorMotivo(t, env))
}

// Por encima de MaxConnections el upgrade responde 503 y no abre socket. La
// plaza se ocupa desde ANTES del handshake —un socket mudo también cuenta— y se
// libera al cerrarse, momento en que vuelve a caber uno.
func TestPorEncimaDelTopeDeConexionesElUpgradeResponde503(t *testing.T) {
	env := newE2ECon(t, nil, func(c *ws.Config) { c.MaxConnections = 2 })

	primera := env.dial() // sin handshake: ocupa plaza igualmente
	env.dial()
	rechazadoAntesDelUpgrade(t, env, nil, http.StatusServiceUnavailable)
	require.Equal(t, 1.0, rechazosPorMotivo(t, env)[observability.RejectMaxConnections])
	require.Contains(t, env.logs.String(), `"reason":"max_connections"`)

	// Cerrar una libera su plaza: la goroutine de la conexión termina y suelta
	// la reserva poco después.
	require.NoError(t, primera.c.Close())
	require.Eventually(t, func() bool {
		c, _, err := env.dialConCabeceras(nil)
		return err == nil && c != nil
	}, 3*time.Second, 20*time.Millisecond, "al cerrar una conexión debe caber otra")
}

// Por encima de MaxConnectionsPerIP el upgrade responde 429: es esa IP la que
// se pasa, no el servidor el que está lleno. Todas las conexiones del test
// salen de 127.0.0.1.
func TestPorEncimaDelTopePorIPElUpgradeResponde429(t *testing.T) {
	env := newE2ECon(t, nil, func(c *ws.Config) { c.MaxConnectionsPerIP = 2 })

	env.dial()
	env.dial()
	rechazadoAntesDelUpgrade(t, env, nil, http.StatusTooManyRequests)
	require.Equal(t, 1.0, rechazosPorMotivo(t, env)[observability.RejectMaxConnectionsPerIP])
	require.Contains(t, env.logs.String(), `"reason":"max_connections_per_ip"`)
}

// X-Forwarded-For sólo cuenta con TrustProxyHeaders: sin un proxy de confianza
// delante, cualquiera elegiría su IP y se saltaría el tope. Con él, la IP se
// lee como en apps/web (ADR-021): el elemento que está TrustedProxyCount
// posiciones por la derecha, el que añadió el último proxy de confianza. El
// primero lo escribe quien quiera y nunca cuenta.
func TestLaIPDeXForwardedForSoloCuentaConProxyDeConfianza(t *testing.T) {
	t.Run("sin confianza se ignora", func(t *testing.T) {
		env := newE2ECon(t, nil, func(c *ws.Config) { c.MaxConnectionsPerIP = 1 })
		c, _, err := env.dialConCabeceras(http.Header{"X-Forwarded-For": {"10.0.0.1"}})
		require.NoError(t, err)
		require.NotNil(t, c)
		// Otra IP «declarada» sigue siendo 127.0.0.1 para el servidor.
		rechazadoAntesDelUpgrade(t, env, http.Header{"X-Forwarded-For": {"10.0.0.2"}}, http.StatusTooManyRequests)
	})

	t.Run("con un proxy de confianza cuenta el último elemento, no el primero", func(t *testing.T) {
		env := newE2ECon(t, nil, func(c *ws.Config) {
			c.MaxConnectionsPerIP = 1
			c.TrustProxyHeaders = true
			c.TrustedProxyCount = 1
		})
		c, _, err := env.dialConCabeceras(http.Header{"X-Forwarded-For": {"10.0.0.1, 192.168.0.9"}})
		require.NoError(t, err)
		require.NotNil(t, c)
		// El atacante cambia el primer elemento y sigue siendo 192.168.0.9.
		rechazadoAntesDelUpgrade(t, env, http.Header{"X-Forwarded-For": {"203.0.113.5, 192.168.0.9"}}, http.StatusTooManyRequests)

		otra, _, err := env.dialConCabeceras(http.Header{"X-Forwarded-For": {"10.0.0.2"}})
		require.NoError(t, err, "otra IP tiene su propio cupo")
		require.NotNil(t, otra)
		// Sin cabecera, la IP vuelve a ser la del socket, que aún no ha gastado
		// su plaza.
		sinCabecera, _, err := env.dialConCabeceras(nil)
		require.NoError(t, err)
		require.NotNil(t, sinCabecera)
	})

	t.Run("con dos proxies cuenta el penúltimo, y una lista corta no se cree", func(t *testing.T) {
		env := newE2ECon(t, nil, func(c *ws.Config) {
			c.MaxConnectionsPerIP = 1
			c.TrustProxyHeaders = true
			c.TrustedProxyCount = 2
		})
		c, _, err := env.dialConCabeceras(http.Header{"X-Forwarded-For": {"10.0.0.1, 192.168.0.9, 172.16.0.1"}})
		require.NoError(t, err)
		require.NotNil(t, c)
		rechazadoAntesDelUpgrade(t, env, http.Header{"X-Forwarded-For": {"10.0.0.7, 192.168.0.9, 172.16.0.2"}}, http.StatusTooManyRequests)

		// Un solo elemento con dos proxies declarados: ningún proxy de confianza
		// lo añadió, así que cuenta la IP del socket, que tiene su plaza libre.
		corta, _, err := env.dialConCabeceras(http.Header{"X-Forwarded-For": {"192.168.0.9"}})
		require.NoError(t, err)
		require.NotNil(t, corta)
		rechazadoAntesDelUpgrade(t, env, nil, http.StatusTooManyRequests)
	})
}

// Un tope a cero es «sin tope»: la configuración lo admite para desactivar el
// tope por IP detrás de un proxy que no se puede hacer de confianza.
func TestUnTopeACeroNoLimita(t *testing.T) {
	env := newE2ECon(t, nil, func(c *ws.Config) {
		c.MaxConnections = 0
		c.MaxConnectionsPerIP = 0
	})
	for i := 0; i < 40; i++ {
		env.dial()
	}
	require.Equal(t, 0.0, rechazosPorMotivo(t, env)[observability.RejectMaxConnections])
	require.Equal(t, 0.0, rechazosPorMotivo(t, env)[observability.RejectMaxConnectionsPerIP])
}

// Criterio 5 de M3: seq es estrictamente creciente, sin huecos, en todos los
// mensajes de una conexión —respuestas, errores y deltas del loop—.
func TestElSeqCreceDeUnoEnUnoEnUnaConexion(t *testing.T) {
	env := newE2E(t)
	c := env.conectar()

	for i := 0; i < 5; i++ {
		c.send(protocol.TypeSessionPing, map[string]any{"clientTimeMs": 1})
	}
	c.send(protocol.TypeUnitMove, map[string]any{"unitId": env.unitIDs[0], "target": map[string]int{"x": 50, "y": 40}})
	require.NoError(t, c.c.WriteMessage(gws.TextMessage, []byte("{roto")))
	env.pump()
	c.await(protocol.TypeUnitMovementStarted, 3*time.Second)
	env.pump()

	// Todo lo recibido desde el welcome, en orden de llegada.
	require.NoError(t, c.c.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
	var seqs []uint64
	for _, m := range c.historial() {
		seqs = append(seqs, m.Seq)
	}
	for {
		data, err := c.read()
		if err != nil {
			break
		}
		var m envelope
		require.NoError(t, json.Unmarshal(data, &m))
		seqs = append(seqs, m.Seq)
	}
	require.GreaterOrEqual(t, len(seqs), 10)
	for i := 1; i < len(seqs); i++ {
		require.Equal(t, seqs[i-1]+1, seqs[i], "seq %d tras %d", seqs[i], seqs[i-1])
	}
}
