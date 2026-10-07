package postgres

import (
	"context"
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
)

// SafeZoneRepo lee la geometría de `safe_zones`.
//
// En el MVP la tabla no tiene escritor en runtime: es inmutable tras el
// arranque y su siembra en el mundo canónico es TBD (docs/specs/safe-zones.md
// §2). Insert existe para los tests y para una futura siembra de desarrollo.
type SafeZoneRepo struct {
	store *Store
}

// NewSafeZoneRepo crea el repositorio de zonas seguras.
func NewSafeZoneRepo(s *Store) *SafeZoneRepo { return &SafeZoneRepo{store: s} }

// LoadAll devuelve todas las zonas en orden ascendente de id.
//
// El ORDER BY no es cosmético: el índice se pinta en ese orden y el id menor
// gana los solapes (RN-SAFE-006, RN-SAFE-007, INV-SAFE-007). BuildIndex ordena
// por su cuenta, pero leerlas ya ordenadas hace que el log de carga salga en el
// mismo orden en cada arranque.
func (r *SafeZoneRepo) LoadAll(ctx context.Context) ([]safezone.Zone, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id, name, zone_type, min_x, min_y, max_x, max_y FROM safe_zones ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("leer zonas seguras: %w", normalize(err))
	}
	defer rows.Close()

	out := make([]safezone.Zone, 0, 16)
	for rows.Next() {
		var z safezone.Zone
		var tipo string
		if err := rows.Scan(&z.ID, &z.Name, &tipo, &z.MinX, &z.MinY, &z.MaxX, &z.MaxY); err != nil {
			return nil, fmt.Errorf("leer zona segura: %w", err)
		}
		z.Type = safezone.Type(tipo)
		out = append(out, z)
	}
	return out, rows.Err()
}

// Insert crea una zona y devuelve su id. Los CHECK safe_zones_type_valid y
// safe_zones_bounds_ordered rechazan lo que el dominio también rechazaría.
func (r *SafeZoneRepo) Insert(ctx context.Context, db DB, z safezone.Zone) (int64, error) {
	var id int64
	err := db.QueryRow(ctx,
		`INSERT INTO safe_zones (name, zone_type, min_x, min_y, max_x, max_y)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		z.Name, string(z.Type), z.MinX, z.MinY, z.MaxX, z.MaxY,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insertar zona segura %q: %w", z.Name, normalize(err))
	}
	return id, nil
}
