package config_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/config"
)

// INV-SEC-006: volcar la configuración —el modo de fuga más común— no saca
// ninguna credencial, por ninguna vía: fmt con cualquier verbo, slog en texto y
// en JSON, y json.Marshal. El valor sigue disponible donde se usa.
func TestLosSecretosNoSeImprimenPorNingunaVia(t *testing.T) {
	setValidEnv(t)
	t.Setenv("EO_POSTGRES_URL", "postgres://empires:contraseña-de-postgres@db:5432/empires")
	t.Setenv("EO_REDIS_URL", "redis://:contraseña-de-redis@cache:6379/0")
	t.Setenv("EO_AUTH_JWT_SECRET", "secreto-jwt-de-al-menos-treinta-y-dos-caracteres")
	cfg, err := config.Load()
	require.NoError(t, err)

	secretos := []string{"contraseña-de-postgres", "contraseña-de-redis", "secreto-jwt-de-al-menos"}

	var salidas []string
	for _, verbo := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		salidas = append(salidas, fmt.Sprintf(verbo, cfg), fmt.Sprintf(verbo, cfg.AuthJWTSecret))
	}
	var texto, jsonLog bytes.Buffer
	slog.New(slog.NewTextHandler(&texto, nil)).Info("config", "cfg", cfg, "secret", cfg.PostgresURL)
	slog.New(slog.NewJSONHandler(&jsonLog, nil)).Info("config", "cfg", cfg, "secret", cfg.RedisURL)
	marshalled, err := json.Marshal(cfg)
	require.NoError(t, err)
	salidas = append(salidas, texto.String(), jsonLog.String(), string(marshalled))

	for _, s := range salidas {
		for _, secreto := range secretos {
			assert.NotContains(t, s, secreto)
		}
	}
	assert.Contains(t, fmt.Sprint(cfg.AuthJWTSecret), "[REDACTED]")

	assert.Equal(t, "postgres://empires:contraseña-de-postgres@db:5432/empires", cfg.PostgresURL.Reveal())
	assert.Equal(t, "", config.NewSecret("").String(), "vacío se distingue de configurado")
}
