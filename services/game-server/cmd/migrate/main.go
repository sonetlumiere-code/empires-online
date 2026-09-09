// Command migrate aplica las migraciones de esquema de forma explícita.
//
// Existe porque en producción NO se migra al arrancar el servidor: si varias
// instancias se levantan a la vez, todas intentarían migrar el mismo esquema.
// El despliegue ejecuta este binario UNA vez y sólo después arranca los
// servidores. Ver ../../../../docs/operations/deployment.md y ADR-012.
//
//	migrate                 aplica todas las migraciones pendientes
//	migrate -version        muestra la versión actual del esquema y sale
//
// Lee EO_POSTGRES_URL del entorno, igual que el servidor: una única fuente de
// configuración evita migrar por error una base distinta de la que se sirve.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/empires-online/empires-online/services/game-server/internal/config"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

func main() {
	showVersion := flag.Bool("version", false, "muestra la versión actual del esquema y sale")
	flag.Parse()

	_, _ = config.LoadDotEnv()

	url := os.Getenv("EO_POSTGRES_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "EO_POSTGRES_URL es obligatoria")
		os.Exit(1)
	}

	log := observability.NewLogger(envOr("EO_LOG_LEVEL", "info"), envOr("EO_ENV", "development"))

	if *showVersion {
		version, dirty, err := postgres.SchemaVersion(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "no se pudo leer la versión del esquema: %v\n", err)
			os.Exit(1)
		}
		state := "limpio"
		if dirty {
			state = "DIRTY (una migración anterior falló a medias)"
		}
		fmt.Printf("versión %d — %s\n", version, state)
		if dirty {
			os.Exit(2)
		}
		return
	}

	if err := postgres.Migrate(url, log); err != nil {
		fmt.Fprintf(os.Stderr, "fallo al migrar: %v\n", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
