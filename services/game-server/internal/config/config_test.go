package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/config"
)

// setValidEnv deja el entorno en un estado mínimo válido.
func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("EO_POSTGRES_URL", "postgres://empires:pass@localhost:5432/empires?sslmode=disable")
	t.Setenv("EO_REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("EO_AUTH_JWT_SECRET", "un-secreto-de-pruebas-suficientemente-largo")
}

func TestValoresPorDefectoDelCanon(t *testing.T) {
	setValidEnv(t)

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "development", cfg.Env)
	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, ":9090", cfg.MetricsAddr)
	require.Equal(t, 10, cfg.TickRateHz)
	require.EqualValues(t, 512, cfg.WorldWidth)
	require.EqualValues(t, 512, cfg.WorldHeight)
	require.EqualValues(t, 32, cfg.ChunkSize)
	require.EqualValues(t, 20260909, cfg.WorldSeed)
	require.EqualValues(t, 2, cfg.InterestRadiusChunk)
	require.Equal(t, 30*time.Second, cfg.PresenceTTL)
	require.Equal(t, 10*time.Second, cfg.PresenceHeartbeat)
	require.Equal(t, 300*time.Second, cfg.CityOfflineProtectionCooldwn)
	require.Equal(t, 50, cfg.PersistenceFlushIntervalTicks)
	require.Equal(t, 20000, cfg.PathfindingMaxNodes)
	require.EqualValues(t, 256, cfg.PathfindingMaxDistance)
	require.EqualValues(t, 16384, cfg.WSMaxMessageBytes)
	require.Equal(t, 20, cfg.WSRateLimitPerSec)
	require.Equal(t, 40, cfg.WSRateLimitBurst)

	require.Equal(t, 100*time.Millisecond, cfg.TickDuration())
	require.EqualValues(t, 100, cfg.TickDurationMs())
	require.False(t, cfg.IsProduction())
}

// La migración automática es cómoda en desarrollo y peligrosa en producción:
// varias instancias arrancando a la vez competirían por el mismo esquema. Por eso
// el valor por defecto depende del entorno.
func TestMigrateOnStartDependeDelEntorno(t *testing.T) {
	t.Run("desarrollo: activada", func(t *testing.T) {
		setValidEnv(t)
		cfg, err := config.Load()
		require.NoError(t, err)
		require.True(t, cfg.MigrateOnStart)
	})

	t.Run("producción: desactivada", func(t *testing.T) {
		setValidEnv(t)
		t.Setenv("EO_ENV", "production")
		t.Setenv("EO_AUTH_JWT_SECRET", "un-secreto-de-produccion-suficientemente-largo")
		cfg, err := config.Load()
		require.NoError(t, err)
		require.False(t, cfg.MigrateOnStart, "en producción se migra con el binario `migrate`")
	})

	t.Run("se puede forzar explícitamente", func(t *testing.T) {
		setValidEnv(t)
		t.Setenv("EO_MIGRATE_ON_START", "false")
		cfg, err := config.Load()
		require.NoError(t, err)
		require.False(t, cfg.MigrateOnStart)
	})

	t.Run("acepta las formas habituales de booleano", func(t *testing.T) {
		for _, truthy := range []string{"1", "true", "TRUE", "yes", "on"} {
			setValidEnv(t)
			t.Setenv("EO_MIGRATE_ON_START", truthy)
			cfg, err := config.Load()
			require.NoError(t, err, "valor %q", truthy)
			require.True(t, cfg.MigrateOnStart, "valor %q", truthy)
		}
		for _, falsy := range []string{"0", "false", "FALSE", "no", "off"} {
			setValidEnv(t)
			t.Setenv("EO_MIGRATE_ON_START", falsy)
			cfg, err := config.Load()
			require.NoError(t, err, "valor %q", falsy)
			require.False(t, cfg.MigrateOnStart, "valor %q", falsy)
		}
	})

	t.Run("un booleano ilegible es un error, no un silencio", func(t *testing.T) {
		setValidEnv(t)
		t.Setenv("EO_MIGRATE_ON_START", "quizás")
		_, err := config.Load()
		require.ErrorContains(t, err, "no es un booleano válido")
	})
}

func TestObligatoriasAusentes(t *testing.T) {
	t.Setenv("EO_POSTGRES_URL", "")
	t.Setenv("EO_REDIS_URL", "")
	t.Setenv("EO_AUTH_JWT_SECRET", "")

	_, err := config.Load()
	require.Error(t, err)
	// Se reportan TODAS a la vez: arreglar la configuración de uno en uno es
	// una pérdida de tiempo en un despliegue.
	require.ErrorContains(t, err, "EO_POSTGRES_URL")
	require.ErrorContains(t, err, "EO_AUTH_JWT_SECRET")
}

// Redis es opcional en desarrollo (se usa estado caliente en proceso) y
// obligatorio en producción, donde varias instancias romperían el anti-replay
// de tickets si cada una llevara su propio registro.
func TestRedisEsOpcionalSoloFueraDeProduccion(t *testing.T) {
	t.Run("desarrollo sin Redis: válido, estado en proceso", func(t *testing.T) {
		setValidEnv(t)
		t.Setenv("EO_REDIS_URL", "")

		cfg, err := config.Load()
		require.NoError(t, err)
		require.True(t, cfg.UsesInProcessHotState())
	})

	t.Run("desarrollo con Redis: se usa Redis", func(t *testing.T) {
		setValidEnv(t)
		cfg, err := config.Load()
		require.NoError(t, err)
		require.False(t, cfg.UsesInProcessHotState())
	})

	t.Run("producción sin Redis: se rechaza", func(t *testing.T) {
		setValidEnv(t)
		t.Setenv("EO_ENV", "production")
		t.Setenv("EO_AUTH_JWT_SECRET", "un-secreto-de-produccion-suficientemente-largo")
		t.Setenv("EO_REDIS_URL", "")

		_, err := config.Load()
		require.ErrorContains(t, err, "EO_REDIS_URL es obligatoria en producción")
	})

	t.Run("producción con Redis: válido", func(t *testing.T) {
		setValidEnv(t)
		t.Setenv("EO_ENV", "production")
		t.Setenv("EO_AUTH_JWT_SECRET", "un-secreto-de-produccion-suficientemente-largo")

		cfg, err := config.Load()
		require.NoError(t, err)
		require.False(t, cfg.UsesInProcessHotState())
	})
}

func TestSecretoDemasiadoCorto(t *testing.T) {
	setValidEnv(t)
	t.Setenv("EO_AUTH_JWT_SECRET", "corto")

	_, err := config.Load()
	require.ErrorContains(t, err, "al menos 32 caracteres")
}

func TestSecretoDeDesarrolloEnProduccion(t *testing.T) {
	setValidEnv(t)
	t.Setenv("EO_ENV", "production")
	t.Setenv("EO_AUTH_JWT_SECRET", "dev-only-insecure-secret-change-me-before-any-deployment")

	_, err := config.Load()
	require.ErrorContains(t, err, "valor de desarrollo en un entorno de producción")
}

// Un tick que no dura un número entero de milisegundos rompería la aritmética
// entera de la simulación.
func TestTickDebeDividirAMil(t *testing.T) {
	setValidEnv(t)
	t.Setenv("EO_TICK_RATE_HZ", "3")

	_, err := config.Load()
	require.ErrorContains(t, err, "debe dividir exactamente a 1000")
}

func TestTicksValidos(t *testing.T) {
	for _, hz := range []string{"1", "2", "4", "5", "10", "20", "25", "50", "100"} {
		t.Run("hz="+hz, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv("EO_TICK_RATE_HZ", hz)
			_, err := config.Load()
			require.NoError(t, err)
		})
	}
}

// El mundo debe dividirse en chunks exactos: un chunk a medias no existe.
func TestMundoDebeSerMultiploDelChunk(t *testing.T) {
	setValidEnv(t)
	t.Setenv("EO_WORLD_WIDTH", "500")
	t.Setenv("EO_CHUNK_SIZE", "32")

	_, err := config.Load()
	require.ErrorContains(t, err, "múltiplos exactos")
}

// Si el latido fuera más lento que el TTL, la presencia expiraría entre latidos y
// todos los jugadores parpadearían entre conectado y desconectado.
func TestLatidoDebeSerMenorQueElTTL(t *testing.T) {
	setValidEnv(t)
	t.Setenv("EO_PRESENCE_TTL_SECONDS", "10")
	t.Setenv("EO_PRESENCE_HEARTBEAT_SECONDS", "10")

	_, err := config.Load()
	require.ErrorContains(t, err, "estrictamente menor")
}

func TestBurstNoPuedeSerMenorQueLaTasa(t *testing.T) {
	setValidEnv(t)
	t.Setenv("EO_WS_RATE_LIMIT_PER_SECOND", "20")
	t.Setenv("EO_WS_RATE_LIMIT_BURST", "5")

	_, err := config.Load()
	require.ErrorContains(t, err, "no puede ser menor")
}

func TestValorFueraDeRango(t *testing.T) {
	setValidEnv(t)
	t.Setenv("EO_TICK_RATE_HZ", "0")

	_, err := config.Load()
	require.ErrorContains(t, err, "fuera del rango permitido")
}

func TestValorNoNumerico(t *testing.T) {
	setValidEnv(t)
	t.Setenv("EO_WORLD_SEED", "no-es-un-numero")

	_, err := config.Load()
	require.ErrorContains(t, err, "no es un entero válido")
}

func TestSobrescrituraPorEntorno(t *testing.T) {
	setValidEnv(t)
	t.Setenv("EO_ENV", "production")
	t.Setenv("EO_AUTH_JWT_SECRET", "un-secreto-de-produccion-suficientemente-largo")
	t.Setenv("EO_TICK_RATE_HZ", "20")
	t.Setenv("EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS", "600")
	t.Setenv("EO_WORLD_WIDTH", "256")
	t.Setenv("EO_WORLD_HEIGHT", "256")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.True(t, cfg.IsProduction())
	require.Equal(t, 20, cfg.TickRateHz)
	require.Equal(t, 50*time.Millisecond, cfg.TickDuration())
	require.Equal(t, 600*time.Second, cfg.CityOfflineProtectionCooldwn)
	require.EqualValues(t, 256, cfg.WorldWidth)
}
