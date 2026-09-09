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

	PostgresURL   string
	RedisURL      string
	AuthJWTSecret string

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

	WSMaxMessageBytes   int64
	WSRateLimitPerSec   int
	WSRateLimitBurst    int
	WSHandshakeTimeout  time.Duration
	WSPingInterval      time.Duration
	WSReadTimeout       time.Duration
	WSWriteTimeout      time.Duration
	WSOutboundQueueSize int
}

// TickDuration es el período de un tick del game loop.
func (c Config) TickDuration() time.Duration {
	return time.Second / time.Duration(c.TickRateHz)
}

// TickDurationMs es el período de un tick en milisegundos enteros.
func (c Config) TickDurationMs() int64 {
	return int64(1000 / c.TickRateHz)
}

// IsProduction indica si el proceso corre en un entorno productivo.
func (c Config) IsProduction() bool { return c.Env == "production" }

// UsesInProcessHotState indica si el estado caliente vive en el proceso en lugar
// de en Redis. Sólo puede ocurrir fuera de producción.
func (c Config) UsesInProcessHotState() bool { return c.RedisURL == "" }

// Load lee la configuración del entorno, aplica los valores por defecto y valida.
// Devuelve un error agregado con TODOS los problemas encontrados, no sólo el primero:
// arreglar la configuración de un despliegue de uno en uno es una pérdida de tiempo.
func Load() (Config, error) {
	v := &collector{}

	cfg := Config{
		Env:         v.str("EO_ENV", "development"),
		LogLevel:    v.str("EO_LOG_LEVEL", "info"),
		HTTPAddr:    v.str("EO_HTTP_ADDR", ":8080"),
		MetricsAddr: v.str("EO_METRICS_ADDR", ":9090"),

		PostgresURL:   v.str("EO_POSTGRES_URL", ""),
		RedisURL:      v.str("EO_REDIS_URL", ""),
		AuthJWTSecret: v.str("EO_AUTH_JWT_SECRET", ""),

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
		MigrateOnStart: v.boolean("EO_MIGRATE_ON_START", os.Getenv("EO_ENV") != "production"),

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
	}

	// --- Validaciones cruzadas: las que un rango por sí solo no puede expresar ---

	if cfg.PostgresURL == "" {
		v.fail("EO_POSTGRES_URL es obligatoria")
	}
	// EO_REDIS_URL es opcional en desarrollo: sin ella se usa el estado caliente
	// en proceso (internal/persistence/memory). En producción NO se admite,
	// porque un almacén en proceso no se comparte entre instancias y el
	// anti-replay de tickets dejaría de valer en cuanto hubiera dos.
	if cfg.RedisURL == "" && cfg.IsProduction() {
		v.fail("EO_REDIS_URL es obligatoria en producción: el estado caliente en memoria no se comparte entre instancias")
	}
	if cfg.AuthJWTSecret == "" {
		v.fail("EO_AUTH_JWT_SECRET es obligatoria")
	} else if len(cfg.AuthJWTSecret) < 32 {
		v.fail("EO_AUTH_JWT_SECRET debe tener al menos 32 caracteres")
	}
	if cfg.IsProduction() && strings.Contains(cfg.AuthJWTSecret, "dev-only") {
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
