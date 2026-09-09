package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadDotEnv carga un archivo .env en el entorno del proceso.
//
// Tres reglas que lo hacen seguro de usar:
//
//  1. NUNCA pisa una variable que ya esté definida. El entorno real siempre gana
//     sobre el archivo, que es lo que espera cualquiera que exporte algo a mano
//     para una ejecución concreta.
//  2. Si el archivo no existe, no pasa nada. En producción no hay .env: la
//     configuración llega por el entorno del contenedor.
//  3. Busca hacia arriba desde el directorio actual, de modo que funcione tanto
//     desde la raíz del repositorio como desde services/game-server.
//
// Devuelve la ruta del archivo cargado, o cadena vacía si no encontró ninguno.
func LoadDotEnv() (string, error) {
	path, ok := findDotEnv()
	if !ok {
		return "", nil
	}

	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := parseDotEnvLine(scanner.Text())
		if !ok {
			continue
		}
		// El entorno real manda sobre el archivo.
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return "", err
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return path, nil
}

// findDotEnv sube por el árbol de directorios buscando un .env.
func findDotEnv() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	// Cinco niveles bastan para cubrir services/game-server/cmd/server y de sobra.
	for i := 0; i < 5; i++ {
		candidate := filepath.Join(dir, ".env")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// parseDotEnvLine interpreta una línea `CLAVE=valor`.
//
// Admite comentarios con `#`, el prefijo `export` y comillas simples o dobles
// alrededor del valor. No intenta ser un intérprete de shell: no expande
// variables ni procesa escapes, porque un archivo de configuración que se
// comporta como un script es una fuente inagotable de sorpresas.
func parseDotEnvLine(raw string) (key, value string, ok bool) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")

	name, rest, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(name)
	if key == "" {
		return "", "", false
	}

	value = strings.TrimSpace(rest)
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return key, value[1 : len(value)-1], true
		}
	}
	// Sin comillas, un `#` inicia un comentario al final de la línea.
	if idx := strings.Index(value, " #"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return key, value, true
}
