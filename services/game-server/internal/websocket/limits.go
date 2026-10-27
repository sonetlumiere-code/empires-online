package websocket

import (
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/empires-online/empires-online/services/game-server/internal/observability"
)

// connLimiter acota las conexiones abiertas de /ws, en total y por IP
// (ADR-021). Cuenta desde ANTES del upgrade hasta que la goroutine de la
// conexión termina, así que un socket que nunca se autentica también ocupa su
// plaza: sin esto, abrir sockets y no decir nada agotaría descriptores y memoria
// aunque el plazo de handshake los cerrase a los 5 s.
//
// Vive en el servidor y no en el Hub porque el Hub sólo conoce sesiones ya
// autenticadas, y el tope tiene que aplicarse antes del handshake.
type connLimiter struct {
	mu       sync.Mutex
	max      int // 0 = sin tope
	maxPerIP int // 0 = sin tope
	total    int
	byIP     map[string]int
}

func newConnLimiter(max, maxPerIP int) *connLimiter {
	return &connLimiter{max: max, maxPerIP: maxPerIP, byIP: map[string]int{}}
}

// acquire reserva una plaza para la IP. Si la concede, devuelve la función que
// la libera (idempotente) y motivo vacío; si no, el motivo del rechazo, que es
// la etiqueta `reason` de eo_ws_connections_rejected_total.
func (l *connLimiter) acquire(ip string) (release func(), reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// El tope global se mira antes que el de la IP: un servidor lleno responde
	// 503 a todos, sea cual sea su IP.
	if l.max > 0 && l.total >= l.max {
		return nil, rejectMaxConnections
	}
	if l.maxPerIP > 0 && l.byIP[ip] >= l.maxPerIP {
		return nil, rejectMaxConnectionsPerIP
	}
	l.total++
	l.byIP[ip]++

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.total--
			if l.byIP[ip] <= 1 {
				delete(l.byIP, ip) // el mapa no crece con cada IP que pasó
			} else {
				l.byIP[ip]--
			}
		})
	}, ""
}

// open devuelve las conexiones contadas ahora mismo. Sólo lo usan los tests.
func (l *connLimiter) open() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.total
}

// Motivos de rechazo de una conexión antes del upgrade: la etiqueta `reason` de
// eo_ws_connections_rejected_total, un catálogo cerrado.
const (
	rejectOrigin              = observability.RejectOrigin
	rejectMaxConnections      = observability.RejectMaxConnections
	rejectMaxConnectionsPerIP = observability.RejectMaxConnectionsPerIP
)

// rejectStatus traduce el motivo al código HTTP con el que se rechaza el
// upgrade: 503 cuando el servidor está lleno (el cliente no tiene la culpa y
// cualquier IP recibiría lo mismo), 429 cuando es esa IP la que se pasa.
func rejectStatus(reason string) int {
	if reason == rejectMaxConnectionsPerIP {
		return http.StatusTooManyRequests
	}
	return http.StatusServiceUnavailable
}

// retryAfterSeconds es la cabecera Retry-After de un rechazo por tope: lo que
// tarda en liberarse una plaza de un socket que no llegó a autenticarse
// (WSHandshakeTimeout, 5 s).
const retryAfterSeconds = "5"

// clientIP es la IP con la que se cuenta la conexión. Por defecto es la del
// socket (r.RemoteAddr). Sólo con trustProxy se creen las cabeceras del proxy,
// y se leen exactamente como hace apps/web (`clientIp` en
// apps/web/src/server/rateLimit.ts): la misma variable, la misma lectura.
// Sin proxy, creerlas dejaría a cualquiera elegir su IP y saltarse el tope.
// Cuando las cabeceras no dan una IP fiable, cuenta la del socket.
func clientIP(r *http.Request, trustProxy bool, trustedProxies int) string {
	if trustProxy {
		if ip, ok := forwardedClientIP(r.Header, trustedProxies); ok {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// forwardedClientIP saca la IP del cliente de las cabeceras del proxy.
//
// X-Forwarded-For es una lista «cliente, proxy1, proxy2, …» a la que cada
// proxy AÑADE la dirección de quien le habló. El primer elemento lo escribe
// quien quiera, el atacante incluido; el único fiable es el que añadió el
// último proxy de confianza: el N-ésimo por la derecha, con N el número de
// proxies de confianza (EO_TRUSTED_PROXY_COUNT). Si la lista es más corta,
// ningún proxy de confianza añadió nada y no se cree ninguno.
//
// X-Real-IP sólo se usa a falta de X-Forwarded-For: Nginx la fija a
// $remote_addr y la sobrescribe, pero un proxy que no la toque dejaría pasar
// la que mande el cliente, y por eso nunca prevalece sobre la lista.
//
// Un N menor que 1 —configuración sin rellenar— se trata como 1.
func forwardedClientIP(h http.Header, trustedProxies int) (string, bool) {
	if trustedProxies < 1 {
		trustedProxies = 1
	}
	// Varias cabeceras X-Forwarded-For equivalen a una sola lista, como en la
	// API Fetch que usa apps/web.
	if lines := h.Values("X-Forwarded-For"); len(lines) > 0 {
		var hops []string
		for _, hop := range strings.Split(strings.Join(lines, ","), ",") {
			if hop = strings.TrimSpace(hop); hop != "" {
				hops = append(hops, hop)
			}
		}
		idx := len(hops) - trustedProxies
		if idx < 0 {
			return "", false
		}
		return normalizeIP(hops[idx]), true
	}
	if real := strings.TrimSpace(h.Get("X-Real-IP")); real != "" {
		return normalizeIP(real), true
	}
	return "", false
}

// normalizeIP quita el puerto que algunos proxies añaden («203.0.113.7:51234»,
// «[2001:db8::1]:443») y unifica la caja, como hace apps/web.
func normalizeIP(raw string) string {
	if strings.HasPrefix(raw, "[") {
		if end := strings.IndexByte(raw, ']'); end > 0 {
			return strings.ToLower(raw[1:end])
		}
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		return strings.ToLower(host)
	}
	return strings.ToLower(raw)
}
