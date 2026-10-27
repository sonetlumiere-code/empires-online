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
// socket (r.RemoteAddr). Sólo con trustProxy se cree a X-Forwarded-For, y se
// toma su PRIMERA entrada, como hace apps/web: con un proxy que la sobrescribe
// (Caddy por defecto, Nginx con $remote_addr) es la IP del cliente; con uno
// que la añade a lo que trajo el cliente, sería falsificable, y por eso el
// despliegue exige la primera forma. Sin proxy, creerla dejaría a cualquiera
// elegir su IP y saltarse el tope.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
