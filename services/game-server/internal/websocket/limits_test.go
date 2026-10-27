package websocket

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// La IP con la que se cuenta una conexión se lee exactamente como en apps/web
// (ADR-021): sin confianza en el proxy es la del socket; con ella, el elemento
// de X-Forwarded-For que está N posiciones por la derecha, con N los proxies de
// confianza, porque el primero lo escribe quien quiera. Cuando las cabeceras
// no dan una IP fiable, vuelve a contar la del socket.
func TestLaIPDelClienteSeLeeComoEnAppsWeb(t *testing.T) {
	peticion := func(remoto string, h http.Header) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/ws", nil)
		r.RemoteAddr = remoto
		for k, vs := range h {
			for _, v := range vs {
				r.Header.Add(k, v)
			}
		}
		return r
	}

	casos := []struct {
		nombre   string
		remoto   string
		cabecera http.Header
		confiar  bool
		proxies  int
		quiere   string
	}{
		{"sin confianza manda el socket aunque haya cabecera", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"10.0.0.1"}}, false, 1, "127.0.0.1"},
		{"sin cabeceras, la IP del socket sin su puerto", "203.0.113.7:5000", nil, true, 1, "203.0.113.7"},
		{"con un proxy, el último elemento: el que añadió el proxy", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"10.0.0.1, 192.168.0.9"}}, true, 1, "192.168.0.9"},
		{"el primer elemento nunca cuenta, aunque sea el único falso", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"203.0.113.5, 192.168.0.9"}}, true, 1, "192.168.0.9"},
		{"con dos proxies, el penúltimo", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"10.0.0.1, 192.168.0.9, 172.16.0.1"}}, true, 2, "192.168.0.9"},
		{"lista más corta que los proxies declarados: no se cree ninguno", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"10.0.0.1"}}, true, 2, "127.0.0.1"},
		{"cabecera presente pero vacía: el socket", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {""}}, true, 1, "127.0.0.1"},
		{"varias cabeceras forman una sola lista", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"10.0.0.1", "192.168.0.9"}}, true, 1, "192.168.0.9"},
		{"X-Real-IP sólo a falta de X-Forwarded-For", "127.0.0.1:5000", http.Header{"X-Real-IP": {"198.51.100.4"}}, true, 1, "198.51.100.4"},
		{"con las dos cabeceras manda la lista", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"192.168.0.9"}, "X-Real-IP": {"198.51.100.4"}}, true, 1, "192.168.0.9"},
		{"se quita el puerto que añaden algunos proxies", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"203.0.113.7:51234"}}, true, 1, "203.0.113.7"},
		{"IPv6 con corchetes y puerto", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"[2001:DB8::1]:443"}}, true, 1, "2001:db8::1"},
		{"IPv6 sin corchetes se conserva entera", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"2001:db8::1"}}, true, 1, "2001:db8::1"},
		{"un recuento sin rellenar equivale a un proxy", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"10.0.0.1, 192.168.0.9"}}, true, 0, "192.168.0.9"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			require.Equal(t, c.quiere, clientIP(peticion(c.remoto, c.cabecera), c.confiar, c.proxies))
		})
	}
}
