package postgres

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registra el driver "pgx5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/empires-online/empires-online/services/game-server/migrations"
)

// Migrate aplica todas las migraciones pendientes.
//
// Reglas (ADR-012): las migraciones publicadas son inmutables, siempre llevan su
// `down`, y el esquema tiene un único dueño: este servicio. El frontend nunca
// migra la base de datos.
func Migrate(dbURL string, log *slog.Logger) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("no se pudieron leer las migraciones embebidas: %w", err)
	}
	defer func() { _ = src.Close() }()

	dsn, err := toMigrateDSN(dbURL)
	if err != nil {
		return err
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("no se pudo inicializar el migrador: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	before, dirty, verr := m.Version()
	if verr != nil && !errors.Is(verr, migrate.ErrNilVersion) {
		return fmt.Errorf("no se pudo leer la versión del esquema: %w", verr)
	}
	if dirty {
		// Un esquema "dirty" significa que una migración anterior falló a medias.
		// Continuar automáticamente sería adivinar: es un incidente que exige una
		// persona. Ver docs/operations/disaster-recovery.md
		return fmt.Errorf("el esquema está marcado como dirty en la versión %d: requiere intervención manual", before)
	}

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			log.Info("esquema ya al día", "version", before)
			return nil
		}
		return fmt.Errorf("fallo al aplicar migraciones: %w", err)
	}

	after, _, _ := m.Version()
	log.Info("migraciones aplicadas", "from", before, "to", after)
	return nil
}

// SchemaVersion devuelve la versión aplicada y si el esquema quedó marcado como
// dirty por una migración fallida.
//
// Un esquema dirty significa que una migración se interrumpió a medias: no se
// puede seguir automáticamente sin adivinar qué quedó aplicado. Es un incidente
// que exige una persona. Ver docs/operations/disaster-recovery.md
func SchemaVersion(dbURL string) (uint, bool, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return 0, false, fmt.Errorf("no se pudieron leer las migraciones embebidas: %w", err)
	}
	defer func() { _ = src.Close() }()

	dsn, err := toMigrateDSN(dbURL)
	if err != nil {
		return 0, false, err
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return 0, false, fmt.Errorf("no se pudo inicializar el migrador: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		// Base sin migrar todavía: versión 0, y no es un error.
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return version, dirty, nil
}

// toMigrateDSN adapta la URL de conexión al esquema que espera el driver pgx/v5
// de golang-migrate, que se registra bajo el nombre "pgx5".
func toMigrateDSN(dbURL string) (string, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return "", fmt.Errorf("EO_POSTGRES_URL inválida: %w", err)
	}
	switch u.Scheme {
	case "postgres", "postgresql", "pgx", "pgx5":
		u.Scheme = "pgx5"
	default:
		return "", fmt.Errorf("esquema de conexión no soportado: %q", u.Scheme)
	}
	return u.String(), nil
}
