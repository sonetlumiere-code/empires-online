package websocket_test

import (
	"net/http"
	"testing"
	"time"

	gws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
	ws "github.com/empires-online/empires-online/services/game-server/internal/websocket"
)

// Comprobación de Origin en el upgrade (ADR-021).
//
// Un navegador envía la cabecera Origin en todo upgrade y no deja al script
// cambiarla: es lo único que separa a la web del juego de cualquier otra página
// abierta en el mismo navegador (cross-site WebSocket hijacking). La lista
// blanca vive en EO_WS_ALLOWED_ORIGINS; hasta la auditoría del 2026-10-26 nunca
// se rellenaba, la comprobación estaba apagada en producción y una mutación que
// la anulaba sobrevivía a la suite entera.

// conOrigenes configura la lista blanca del banco de pruebas.
func conOrigenes(origenes ...string) func(*ws.Config) {
	return func(c *ws.Config) { c.AllowedOrigins = origenes }
}

// dialConCabeceras abre el socket con cabeceras propias y devuelve también la
// respuesta HTTP, que es lo único que llega cuando el upgrade se rechaza.
func (e *e2e) dialConCabeceras(h http.Header) (*conn, *http.Response, error) {
	e.t.Helper()
	c, resp, err := gws.DefaultDialer.Dial(e.wsURL(), h)
	if err != nil {
		return nil, resp, err
	}
	e.t.Cleanup(func() { _ = c.Close() })
	return &conn{t: e.t, c: c}, resp, nil
}

// abreSesion completa el handshake sobre una conexión ya abierta.
func (e *e2e) abreSesion(c *conn) {
	e.t.Helper()
	ticket, err := e.issuer.Issue(e.playerID)
	require.NoError(e.t, err)
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
}

// Un Origin que no está en la lista se rechaza con 403 ANTES del handshake: no
// hay socket, ni plazo de 5 s, ni código de cierre. Un esquema o un puerto
// distintos son otro origen; `null` (iframe aislado, file://) no casa nunca.
func TestUnOrigenNoListadoSeRechazaCon403AntesDelHandshake(t *testing.T) {
	env := newE2ECon(t, nil, conOrigenes("http://localhost:3000"))

	for _, origen := range []string{"http://evil.example", "https://localhost:3000", "http://localhost:3001", "null"} {
		t.Run(origen, func(t *testing.T) {
			c, resp, err := env.dialConCabeceras(http.Header{"Origin": {origen}})
			require.ErrorIs(t, err, gws.ErrBadHandshake)
			require.Nil(t, c)
			require.NotNil(t, resp)
			require.Equal(t, http.StatusForbidden, resp.StatusCode)
		})
	}
	require.Contains(t, env.logs.String(), "origen rechazado en el upgrade de WebSocket")
}

// Un Origin listado abre sesión con normalidad. La comparación no distingue
// mayúsculas en el esquema ni en el host (RFC 6454).
func TestUnOrigenListadoAbreSesion(t *testing.T) {
	env := newE2ECon(t, nil, conOrigenes("http://localhost:3000", "https://play.example.com"))

	for _, origen := range []string{"http://localhost:3000", "HTTP://LOCALHOST:3000", "https://play.example.com"} {
		t.Run(origen, func(t *testing.T) {
			c, _, err := env.dialConCabeceras(http.Header{"Origin": {origen}})
			require.NoError(t, err)
			env.abreSesion(c)
		})
	}
}

// Sin cabecera Origin el cliente no es un navegador (scripts/smoke.mjs, curl,
// otro servidor): se admite aunque haya lista. El riesgo cross-site sólo existe
// en los navegadores, que la envían siempre; un cliente que la omite tampoco
// tiene las cookies de nadie que explotar.
func TestSinOrigenSeAdmiteAunqueHayaLista(t *testing.T) {
	env := newE2ECon(t, nil, conOrigenes("http://localhost:3000"))

	c := env.dial() // gorilla no añade Origin por su cuenta
	env.abreSesion(c)
}

// Con la lista vacía —que la configuración sólo admite fuera de producción— se
// acepta cualquier Origin.
func TestConLaListaVaciaSeAdmiteCualquierOrigen(t *testing.T) {
	env := newE2E(t)

	c, _, err := env.dialConCabeceras(http.Header{"Origin": {"http://evil.example"}})
	require.NoError(t, err)
	env.abreSesion(c)
}
