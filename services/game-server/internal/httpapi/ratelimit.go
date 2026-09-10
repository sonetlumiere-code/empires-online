package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// maxTrackedClients acota el número de cubos vivos.
//
// Sin tope, un atacante con muchas direcciones de origen convierte el propio
// limitador en el agotamiento de memoria que venía a evitar. Al llegar al tope
// se barren los inactivos; si aun así sigue lleno, se deniega. Denegar de más
// bajo un ataque distribuido es preferible a quedarse sin memoria.
const maxTrackedClients = 20000

// idleEviction es cuánto tiempo se conserva el cubo de un cliente que no vuelve.
//
// Tiene que superar de largo el tiempo que tarda un cubo en rellenarse: retirar
// antes equivale a regalar el cupo entero a quien espere lo justo.
const idleEviction = 15 * time.Minute

// bucket es un cubo de fichas, igual que el del WebSocket pero con su propio
// instante de último uso para poder desalojarlo.
type bucket struct {
	tokens float64
	last   time.Time
}

// IPLimiter limita peticiones por dirección de origen.
//
// Existe por una razón concreta: `POST /api/auth/register` ejecuta bcrypt, que
// es deliberadamente caro, **sin autenticación previa**. Sin límite, cualquiera
// puede obligar al proceso que corre el game loop a quemar CPU a voluntad. El
// límite de login, además, es lo único que separa una contraseña de un ataque
// de fuerza bruta.
type IPLimiter struct {
	mu       sync.Mutex
	clients  map[string]*bucket
	rate     float64 // fichas por segundo
	burst    float64
	trustXFF bool
}

// NewIPLimiter crea el limitador.
//
// `perMinute` y no `perSecond` porque la unidad natural de un intento de login
// es el minuto: expresarlo por segundo obligaría a escribir fracciones para
// cualquier valor razonable.
//
// `trustProxyHeaders` decide si se cree a `X-Forwarded-For`. Por defecto NO, y
// es importante: detrás de un proxy inverso todas las peticiones parecen venir
// de él y el límite se aplicaría al conjunto; pero creer la cabecera sin proxy
// delante permite a cualquiera falsificarla y saltarse el límite por completo.
// La opción segura es la que no confía, y la activa quien sepa que hay un proxy.
func NewIPLimiter(perMinute, burst int, trustProxyHeaders bool) *IPLimiter {
	return &IPLimiter{
		clients:  make(map[string]*bucket),
		rate:     float64(perMinute) / 60.0,
		burst:    float64(burst),
		trustXFF: trustProxyHeaders,
	}
}

// Allow indica si la petición puede seguir adelante.
func (l *IPLimiter) Allow(r *http.Request, now time.Time) bool {
	key := l.clientKey(r)

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.clients[key]
	if !ok {
		if len(l.clients) >= maxTrackedClients {
			l.evict(now)
		}
		if len(l.clients) >= maxTrackedClients {
			// Sigue lleno tras el barrido: se deniega. Es el modo degradado
			// deliberado, no un fallo.
			return false
		}
		b = &bucket{tokens: l.burst, last: now}
		l.clients[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// evict retira los cubos que llevan demasiado sin usarse. Se llama con el lock
// tomado.
func (l *IPLimiter) evict(now time.Time) {
	for k, b := range l.clients {
		if now.Sub(b.last) > idleEviction {
			delete(l.clients, k)
		}
	}
}

// clientKey identifica al cliente.
func (l *IPLimiter) clientKey(r *http.Request) string {
	if l.trustXFF {
		// El primer valor de X-Forwarded-For es el cliente original; el resto son
		// los proxies por los que pasó.
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, found := strings.Cut(xff, ","); found {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(xff)
		}
		if real := r.Header.Get("X-Real-IP"); real != "" {
			return strings.TrimSpace(real)
		}
	}

	// Se descarta el puerto: cada petición llega desde uno distinto, así que
	// conservarlo daría un cubo nuevo por petición y ningún límite en absoluto.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Tracked devuelve cuántos clientes se están siguiendo. Sólo para tests y
// diagnóstico.
func (l *IPLimiter) Tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.clients)
}
