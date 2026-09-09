package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/config"
)

// writeDotEnv crea un .env en un directorio temporal y sitúa el proceso allí.
func writeDotEnv(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(previous) })

	return path
}

func TestCargaVariablesDelArchivo(t *testing.T) {
	writeDotEnv(t, "EO_TEST_ALPHA=uno\nEO_TEST_BETA=dos\n")
	t.Setenv("EO_TEST_ALPHA", "")
	require.NoError(t, os.Unsetenv("EO_TEST_ALPHA"))
	require.NoError(t, os.Unsetenv("EO_TEST_BETA"))
	t.Cleanup(func() {
		_ = os.Unsetenv("EO_TEST_ALPHA")
		_ = os.Unsetenv("EO_TEST_BETA")
	})

	path, err := config.LoadDotEnv()
	require.NoError(t, err)
	require.NotEmpty(t, path)

	require.Equal(t, "uno", os.Getenv("EO_TEST_ALPHA"))
	require.Equal(t, "dos", os.Getenv("EO_TEST_BETA"))
}

// El entorno real siempre gana: quien exporta algo a mano para una ejecución
// concreta espera que su valor mande sobre el archivo.
func TestNoPisaVariablesYaDefinidas(t *testing.T) {
	writeDotEnv(t, "EO_TEST_GAMMA=del-archivo\n")
	t.Setenv("EO_TEST_GAMMA", "del-entorno")

	_, err := config.LoadDotEnv()
	require.NoError(t, err)
	require.Equal(t, "del-entorno", os.Getenv("EO_TEST_GAMMA"))
}

func TestSinArchivoNoEsError(t *testing.T) {
	dir := t.TempDir()
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(previous) })

	// En producción no hay .env: la configuración llega por el entorno.
	path, err := config.LoadDotEnv()
	require.NoError(t, err)
	require.Empty(t, path)
}

// Busca hacia arriba para que funcione igual desde la raíz del repositorio que
// desde services/game-server.
func TestBuscaEnDirectoriosSuperiores(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("EO_TEST_DELTA=encontrado\n"), 0o600))

	nested := filepath.Join(root, "services", "game-server")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(nested))
	t.Cleanup(func() { _ = os.Chdir(previous) })

	require.NoError(t, os.Unsetenv("EO_TEST_DELTA"))
	t.Cleanup(func() { _ = os.Unsetenv("EO_TEST_DELTA") })

	path, err := config.LoadDotEnv()
	require.NoError(t, err)
	require.NotEmpty(t, path)
	require.Equal(t, "encontrado", os.Getenv("EO_TEST_DELTA"))
}

func TestFormatosDeLinea(t *testing.T) {
	writeDotEnv(t, `
# un comentario suelto
EO_TEST_SIMPLE=valor

  EO_TEST_ESPACIOS  =   con espacios alrededor
export EO_TEST_EXPORT=con-prefijo-export
EO_TEST_COMILLAS_DOBLES="entre comillas"
EO_TEST_COMILLAS_SIMPLES='comillas simples'
EO_TEST_URL=postgres://u:p@localhost:5433/db?sslmode=disable
EO_TEST_COMENTARIO=valor # esto es un comentario
EO_TEST_VACIO=
linea-sin-igual
=sin-clave
`)

	claves := []string{
		"EO_TEST_SIMPLE", "EO_TEST_ESPACIOS", "EO_TEST_EXPORT",
		"EO_TEST_COMILLAS_DOBLES", "EO_TEST_COMILLAS_SIMPLES",
		"EO_TEST_URL", "EO_TEST_COMENTARIO", "EO_TEST_VACIO",
	}
	for _, k := range claves {
		require.NoError(t, os.Unsetenv(k))
	}
	t.Cleanup(func() {
		for _, k := range claves {
			_ = os.Unsetenv(k)
		}
	})

	_, err := config.LoadDotEnv()
	require.NoError(t, err)

	require.Equal(t, "valor", os.Getenv("EO_TEST_SIMPLE"))
	require.Equal(t, "con espacios alrededor", os.Getenv("EO_TEST_ESPACIOS"))
	require.Equal(t, "con-prefijo-export", os.Getenv("EO_TEST_EXPORT"))
	require.Equal(t, "entre comillas", os.Getenv("EO_TEST_COMILLAS_DOBLES"))
	require.Equal(t, "comillas simples", os.Getenv("EO_TEST_COMILLAS_SIMPLES"))
	require.Equal(t, "valor", os.Getenv("EO_TEST_COMENTARIO"))
	require.Equal(t, "", os.Getenv("EO_TEST_VACIO"))

	// Una URL con `#` no lo lleva, pero sí lleva `//`, `:` y `?`: no debe
	// recortarse nada.
	require.Equal(t, "postgres://u:p@localhost:5433/db?sslmode=disable", os.Getenv("EO_TEST_URL"))
}

// El archivo de configuración NO es un script: no expande variables ni procesa
// escapes. Un .env que se comporta como un shell es una fuente inagotable de
// sorpresas.
func TestNoExpandeVariables(t *testing.T) {
	writeDotEnv(t, "EO_TEST_LITERAL=$HOME/algo\n")
	require.NoError(t, os.Unsetenv("EO_TEST_LITERAL"))
	t.Cleanup(func() { _ = os.Unsetenv("EO_TEST_LITERAL") })

	_, err := config.LoadDotEnv()
	require.NoError(t, err)
	require.Equal(t, "$HOME/algo", os.Getenv("EO_TEST_LITERAL"))
}

// El cargador y Load() encajan: un .env completo basta para arrancar.
func TestUnDotEnvCompletoBastaParaArrancar(t *testing.T) {
	writeDotEnv(t, `
EO_POSTGRES_URL=postgres://empires:pass@localhost:5433/empires?sslmode=disable
EO_AUTH_JWT_SECRET=un-secreto-de-pruebas-suficientemente-largo
EO_TICK_RATE_HZ=20
`)
	for _, k := range []string{"EO_POSTGRES_URL", "EO_AUTH_JWT_SECRET", "EO_TICK_RATE_HZ", "EO_REDIS_URL", "EO_ENV"} {
		require.NoError(t, os.Unsetenv(k))
	}
	t.Cleanup(func() {
		for _, k := range []string{"EO_POSTGRES_URL", "EO_AUTH_JWT_SECRET", "EO_TICK_RATE_HZ"} {
			_ = os.Unsetenv(k)
		}
	})

	_, err := config.LoadDotEnv()
	require.NoError(t, err)

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, 20, cfg.TickRateHz)
	require.True(t, cfg.UsesInProcessHotState(), "sin EO_REDIS_URL se usa el estado en proceso")
}
