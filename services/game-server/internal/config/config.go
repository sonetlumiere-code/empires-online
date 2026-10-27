// Package config centraliza TODA la configuración del Game Server.
//
// Dos reglas duras:
//  1. Ningún valor de gameplay se hardcodea fuera de este paquete. Si aparece un
//     número mágico en el dominio, es un bug.
//  2. Fail-fast: el proceso no arranca si falta un valor obligatorio o si alguno
//     está fuera de rango. Es preferible no arrancar a arrancar mal.
//
// Referencia completa: ../../../../docs/operations/configuration.md
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config es la configuración completa y ya validada del proceso.
type Config struct {
	Env         string
	LogLevel    string
	HTTPAddr    string
	MetricsAddr string
	// InternalAddr es el listener de la API interna (`/internal/players`), que
	// sólo llama apps/web con un token de fundación (ADR-018). Es un servidor
	// aparte del público para que el reverse proxy no lo publique: por defecto
	// escucha sólo en loopback (ADR-021).
	InternalAddr string

	PostgresURL   Secret
	RedisURL      Secret
	AuthJWTSecret Secret

	TickRateHz          int
	WorldWidth          int32
	WorldHeight         int32
	WorldSeed           uint64
	ChunkSize           int32
	InterestRadiusChunk int32

	PresenceTTL                  time.Duration
	PresenceHeartbeat            time.Duration
	CityOfflineProtectionCooldwn time.Duration

	PersistenceFlushIntervalTicks int

	// MigrateOnStart indica si el servidor aplica las migraciones pendientes al
	// arrancar. Es cómodo en desarrollo y peligroso en producción: con varias
	// instancias arrancando a la vez, todas intentarían migrar el mismo esquema.
	// En producción se usa el binario `cmd/migrate` como paso previo al despliegue.
	MigrateOnStart bool

	PathfindingMaxNodes    int
	PathfindingMaxDistance int32

	WSMaxMessageBytes int64
	WSRateLimitPerSec int
	WSRateLimitBurst  int
	// EO_AUTH_RATE_LIMIT_* ya no son del Game Server: desde ADR-018 el alta y
	// el login viven en apps/web, que es quien las lee.
	WSHandshakeTimeout  time.Duration
	WSPingInterval      time.Duration
	WSReadTimeout       time.Duration
	WSWriteTimeout      time.Duration
	WSOutboundQueueSize int

	// WSAllowedOrigins son los orígenes (`scheme://host[:port]`, normalizados a
	// minúsculas) desde los que un navegador puede abrir /ws. Vacía fuera de
	// producción significa «cualquiera»; en producción es obligatoria, porque sin
	// ella cualquier página web abriría sesiones desde el navegador del jugador
	// (ADR-021). Un cliente que no envía Origin no es un navegador y no se filtra.
	WSAllowedOrigins []string

	// WSMaxConnections y WSMaxConnectionsPerIP acotan los sockets de /ws
	// abiertos, contados desde antes del upgrade (ADR-021). 0 = sin tope.
	WSMaxConnections      int
	WSMaxConnectionsPerIP int
	// TrustProxyHeaders: creer a X-Forwarded-For para identificar la IP de una
	// conexión. La misma variable y el mismo significado que en apps/web
	// (EO_TRUST_PROXY_HEADERS): sólo con un proxy inverso delante.
	TrustProxyHeaders bool
}

// Valores admitidos de EO_ENV. La lista es cerrada a propósito: `prod` o un
// error de tecleo activaría en silencio la semántica de desarrollo (migración
// automática, estado caliente en proceso, secreto de ejemplo admitido).
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"
)

// environments es la lista cerrada de EO_ENV, en el orden en que se enumera
// en el mensaje de error.
var environments = []string{EnvDevelopment, EnvTest, EnvProduction}

// TickDuration es el período de un tick del game loop.
func (c Config) TickDuration() time.Duration {
	return time.Second / time.Duration(c.TickRateHz)
}

// TickDurationMs es el período de un tick en milisegundos enteros.
func (c Config) TickDurationMs() int64 {
	return int64(1000 / c.TickRateHz)
}

// IsProduction indica si el proceso corre en un entorno productivo.
func (c Config) IsProduction() bool { return c.Env == EnvProduction }

// UsesInProcessHotState indica si el estado caliente vive en el proceso en lugar
// de en Redis. Sólo puede ocurrir fuera de producción.
func (c Config) UsesInProcessHotState() bool { return c.RedisURL.IsZero() }

// Load lee la configuración del entorno, aplica los valores por defecto y valida.
// Devuelve un error agregado con TODOS los problemas encontrados, no sólo el primero:
// arreglar la configuración de un despliegue de uno en uno es una pérdida de tiempo.
func Load() (Config, error) {
	v := &collector{}

	// El entorno se resuelve primero porque otros valores por defecto dependen
	// de él. Un valor fuera de la lista es un error, no un entorno de desarrollo.
	env := v.oneOf("EO_ENV", EnvDevelopment, environments)
	origins, originsSet := v.origins("EO_WS_ALLOWED_ORIGINS")

	cfg := Config{
		Env:          env,
		LogLevel:     v.str("EO_LOG_LEVEL", "info"),
		HTTPAddr:     v.addr("EO_HTTP_ADDR", ":8080"),
		MetricsAddr:  v.addr("EO_METRICS_ADDR", ":9090"),
		InternalAddr: v.addr("EO_INTERNAL_ADDR", "127.0.0.1:8081"),

		PostgresURL:   NewSecret(v.str("EO_POSTGRES_URL", "")),
		RedisURL:      NewSecret(v.str("EO_REDIS_URL", "")),
		AuthJWTSecret: NewSecret(v.str("EO_AUTH_JWT_SECRET", "")),

		TickRateHz:          v.intRange("EO_TICK_RATE_HZ", 10, 1, 1000),
		WorldWidth:          int32(v.intRange("EO_WORLD_WIDTH", 512, 16, 65536)),
		WorldHeight:         int32(v.intRange("EO_WORLD_HEIGHT", 512, 16, 65536)),
		WorldSeed:           uint64(v.int64("EO_WORLD_SEED", 20260909)),
		ChunkSize:           int32(v.intRange("EO_CHUNK_SIZE", 32, 1, 256)),
		InterestRadiusChunk: int32(v.intRange("EO_INTEREST_RADIUS_CHUNKS", 2, 0, 64)),

		PresenceTTL:                  v.seconds("EO_PRESENCE_TTL_SECONDS", 30, 1, 3600),
		PresenceHeartbeat:            v.seconds("EO_PRESENCE_HEARTBEAT_SECONDS", 10, 1, 3600),
		CityOfflineProtectionCooldwn: v.seconds("EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS", 300, 0, 86400),

		PersistenceFlushIntervalTicks: v.intRange("EO_PERSISTENCE_FLUSH_INTERVAL_TICKS", 50, 1, 100000),

		// El valor por defecto depende del entorno: se activa en desarrollo y se
		// desactiva en producción. Un default único obligaría a recordar ponerlo
		// en el sitio donde olvidarlo hace daño.
		MigrateOnStart: v.boolean("EO_MIGRATE_ON_START", env != EnvProduction),

		PathfindingMaxNodes:    v.intRange("EO_PATHFINDING_MAX_NODES", 20000, 100, 10_000_000),
		PathfindingMaxDistance: int32(v.intRange("EO_PATHFINDING_MAX_DISTANCE", 256, 1, 65536)),

		WSMaxMessageBytes:   int64(v.intRange("EO_WS_MAX_MESSAGE_BYTES", 16384, 256, 1<<22)),
		WSRateLimitPerSec:   v.intRange("EO_WS_RATE_LIMIT_PER_SECOND", 20, 1, 10000),
		WSRateLimitBurst:    v.intRange("EO_WS_RATE_LIMIT_BURST", 40, 1, 20000),
		WSHandshakeTimeout:  5 * time.Second,
		WSPingInterval:      15 * time.Second,
		WSReadTimeout:       45 * time.Second,
		WSWriteTimeout:      10 * time.Second,
		WSOutboundQueueSize: v.intRange("EO_WS_OUTBOUND_QUEUE_SIZE", 256, 8, 65536),
		WSAllowedOrigins:    origins,

		WSMaxConnections:      v.intRange("EO_WS_MAX_CONNECTIONS", 5000, 0, 1_000_000),
		WSMaxConnectionsPerIP: v.intRange("EO_WS_MAX_CONNECTIONS_PER_IP", 32, 0, 100_000),
		TrustProxyHeaders:     v.boolean("EO_TRUST_PROXY_HEADERS", false),
	}

	// --- Validaciones cruzadas: las que un rango por sí solo no puede expresar ---

	if cfg.PostgresURL.IsZero() {
		v.fail("EO_POSTGRES_URL es obligatoria")
	}
	// EO_REDIS_URL es opcional en desarrollo: sin ella se usa el estado caliente
	// en proceso (internal/persistence/memory). En producción NO se admite,
	// porque un almacén en proceso no se comparte entre instancias y el
	// anti-replay de tickets dejaría de valer en cuanto hubiera dos.
	if cfg.RedisURL.IsZero() && cfg.IsProduction() {
		v.fail("EO_REDIS_URL es obligatoria en producción: el estado caliente en memoria no se comparte entre instancias")
	}
	if cfg.AuthJWTSecret.IsZero() {
		v.fail("EO_AUTH_JWT_SECRET es obligatoria")
	} else if len(cfg.AuthJWTSecret.Reveal()) < 32 {
		v.fail("EO_AUTH_JWT_SECRET debe tener al menos 32 caracteres")
	}
	if cfg.IsProduction() && strings.Contains(cfg.AuthJWTSecret.Reveal(), "dev-only") {
		v.fail("EO_AUTH_JWT_SECRET tiene el valor de desarrollo en un entorno de producción")
	}
	if 1000%cfg.TickRateHz != 0 {
		v.fail(fmt.Sprintf("EO_TICK_RATE_HZ=%d debe dividir exactamente a 1000 para que el tick dure un número entero de milisegundos", cfg.TickRateHz))
	}
	if cfg.WorldWidth%cfg.ChunkSize != 0 || cfg.WorldHeight%cfg.ChunkSize != 0 {
		v.fail(fmt.Sprintf("EO_WORLD_WIDTH (%d) y EO_WORLD_HEIGHT (%d) deben ser múltiplos exactos de EO_CHUNK_SIZE (%d)", cfg.WorldWidth, cfg.WorldHeight, cfg.ChunkSize))
	}
	if cfg.PresenceHeartbeat >= cfg.PresenceTTL {
		v.fail("EO_PRESENCE_HEARTBEAT_SECONDS debe ser estrictamente menor que EO_PRESENCE_TTL_SECONDS, o la presencia expiraría entre latidos")
	}
	if cfg.WSRateLimitBurst < cfg.WSRateLimitPerSec {
		v.fail("EO_WS_RATE_LIMIT_BURST no puede ser menor que EO_WS_RATE_LIMIT_PER_SECOND")
	}
	// Sin lista, el upgrade acepta cualquier Origin. Eso sólo vale en desarrollo:
	// en producción, cualquier página web podría abrir sesiones de juego desde el
	// navegador de un jugador que tenga la web abierta (ADR-021).
	if cfg.IsProduction() && !originsSet {
		v.fail("EO_WS_ALLOWED_ORIGINS es obligatoria en producción: sin ella el upgrade de /ws acepta cualquier Origin")
	}
	// Tres listeners, tres puertos. Dos direcciones con el mismo puerto fallan
	// al abrir el segundo listener, ya en marcha; mejor aquí, antes de arrancar.
	v.distinctPorts(
		listenAddr{"EO_HTTP_ADDR", cfg.HTTPAddr},
		listenAddr{"EO_METRICS_ADDR", cfg.MetricsAddr},
		listenAddr{"EO_INTERNAL_ADDR", cfg.InternalAddr},
	)

	if err := v.err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// collector acumula errores de configuración para reportarlos todos juntos.
type collector struct{ problems []string }

func (c *collector) fail(msg string) { c.problems = append(c.problems, msg) }

func (c *collector) err() error {
	if len(c.problems) == 0 {
		return nil
	}
	return fmt.Errorf("configuración inválida:\n  - %s", strings.Join(c.problems, "\n  - "))
}

func (c *collector) str(key, def string) string {
	if raw, ok := os.LookupEnv(key); ok && raw != "" {
		return raw
	}
	return def
}

// addr lee una dirección de escucha `host:port` tal y como la acepta net.Listen,
// con el puerto explícito y distinto de 0: un puerto 0 sería «uno al azar», y
// nadie sabría a dónde conectar.
func (c *collector) addr(key, def string) string {
	raw := c.str(key, def)
	if _, port, err := net.SplitHostPort(raw); err != nil || !validPort(port) {
		c.fail(fmt.Sprintf("%s=%q no es una dirección de escucha válida (usa host:port o :port, con puerto entre 1 y 65535)", key, raw))
		return def
	}
	return raw
}

func validPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

// listenAddr es una dirección de escucha con el nombre de su variable, para
// los mensajes de error.
type listenAddr struct{ key, value string }

// distinctPorts exige que ninguna dirección de escucha repita puerto. Se
// compara sólo el puerto porque `:8080` escucha en todas las interfaces y
// choca con cualquier `host:8080`; dos hosts concretos con el mismo puerto
// podrían convivir, pero es una rareza que no merece un listener roto.
func (c *collector) distinctPorts(addrs ...listenAddr) {
	seen := map[string]listenAddr{}
	for _, a := range addrs {
		_, port, err := net.SplitHostPort(a.value)
		if err != nil {
			continue // ya reportado por addr
		}
		if prev, dup := seen[port]; dup {
			c.fail(fmt.Sprintf("%s=%q repite el puerto %s de %s: cada listener necesita el suyo", a.key, a.value, port, prev.key))
			continue
		}
		seen[port] = a
	}
}

// origins lee una lista separada por comas de orígenes exactos
// `scheme://host[:port]` (http o https) y la devuelve normalizada a minúsculas,
// que es como se compara con la cabecera Origin. Una entrada con ruta, query,
// credenciales o sin esquema es un error: `https://game.example.com/` no
// casaría nunca con lo que envía un navegador y en producción dejaría fuera a
// todos los jugadores. El segundo valor dice si la variable tenía contenido.
func (c *collector) origins(key string) ([]string, bool) {
	raw := strings.TrimSpace(c.str(key, ""))
	if raw == "" {
		return nil, false
	}
	var out []string
	for _, item := range strings.Split(raw, ",") {
		o := strings.TrimSpace(item)
		if o == "" {
			continue
		}
		u, err := url.Parse(o)
		if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" ||
			u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
			(u.Scheme != "http" && u.Scheme != "https") {
			c.fail(fmt.Sprintf("%s: %q no es un origen válido (usa scheme://host[:port], con http o https y sin ruta)", key, o))
			continue
		}
		out = append(out, strings.ToLower(u.Scheme)+"://"+strings.ToLower(u.Host))
	}
	return out, true
}

// oneOf lee una variable que sólo admite los valores de `allowed`, tal cual,
// sin normalizar mayúsculas: `Production` no es `production`.
func (c *collector) oneOf(key, def string, allowed []string) string {
	raw := c.str(key, def)
	for _, a := range allowed {
		if raw == a {
			return raw
		}
	}
	c.fail(fmt.Sprintf("%s=%q no es un entorno válido (usa %s)", key, raw, strings.Join(allowed, ", ")))
	return def
}

func (c *collector) int64(key string, def int64) int64 {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		c.fail(fmt.Sprintf("%s=%q no es un entero válido", key, raw))
		return def
	}
	return n
}

func (c *collector) intRange(key string, def, min, max int) int {
	n := int(c.int64(key, int64(def)))
	if n < min || n > max {
		c.fail(fmt.Sprintf("%s=%d fuera del rango permitido [%d, %d]", key, n, min, max))
		return def
	}
	return n
}

func (c *collector) seconds(key string, def, min, max int) time.Duration {
	return time.Duration(c.intRange(key, def, min, max)) * time.Second
}

func (c *collector) boolean(key string, def bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		c.fail(fmt.Sprintf("%s=%q no es un booleano válido (usa true/false)", key, raw))
		return def
	}
}

// ErrMissing se devuelve cuando falta configuración obligatoria.
var ErrMissing = errors.New("configuración obligatoria ausente")
