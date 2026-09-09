package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
)

// CityRepo persiste ciudades y su estado de presencia.
type CityRepo struct{ store *Store }

// NewCityRepo crea el repositorio.
func NewCityRepo(s *Store) *CityRepo { return &CityRepo{store: s} }

const cityColumns = `id, owner_player_id, name, center_x, center_y, era, population,
	population_limit, presence_state, last_online_at, last_offline_at, protection_until, version`

// GetByID recupera una ciudad.
func (r *CityRepo) GetByID(ctx context.Context, id int64) (*city.City, error) {
	return scanCity(r.store.pool.QueryRow(ctx, `SELECT `+cityColumns+` FROM cities WHERE id = $1`, id))
}

// GetByOwner recupera la ciudad de un jugador. En el MVP cada jugador tiene una.
func (r *CityRepo) GetByOwner(ctx context.Context, playerID uuid.UUID) (*city.City, error) {
	return scanCity(r.store.pool.QueryRow(ctx,
		`SELECT `+cityColumns+` FROM cities WHERE owner_player_id = $1 ORDER BY id LIMIT 1`, playerID))
}

// ListAll carga todas las ciudades. El mundo del MVP cabe holgadamente en memoria;
// cuando deje de caber, esta consulta se sustituirá por una carga por región.
func (r *CityRepo) ListAll(ctx context.Context) ([]*city.City, error) {
	rows, err := r.store.pool.Query(ctx, `SELECT `+cityColumns+` FROM cities ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*city.City
	for rows.Next() {
		c, err := scanCity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Create inserta una ciudad y devuelve su identificador asignado.
func (r *CityRepo) Create(ctx context.Context, db DB, c *city.City) (int64, error) {
	var id int64
	err := db.QueryRow(ctx,
		`INSERT INTO cities (owner_player_id, name, center_x, center_y, era,
		                     population, population_limit, presence_state, last_online_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		 RETURNING id`,
		c.OwnerPlayerID, c.Name, c.CenterX, c.CenterY, string(c.Era),
		c.Population, c.PopulationLimit, string(c.PresenceState),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insertar ciudad: %w", normalize(err))
	}
	c.ID = id
	return id, nil
}

// SetPresence aplica una transición del autómata de presencia.
//
// La transición se valida ANTES en el dominio (city.CanTransition). Aquí sólo se
// escribe, junto a las marcas temporales que dependen del estado de destino.
func (r *CityRepo) SetPresence(ctx context.Context, db DB, cityID int64, state city.PresenceState, at time.Time) error {
	// Cada rama lleva su propia lista de argumentos: la de PROTECTED no usa el
	// instante, y pasar un parámetro de más hace que pgx rechace la consulta.
	var (
		query string
		args  []any
	)
	switch state {
	case city.PresenceOnline:
		// Volver a estar en línea limpia la protección: el mundo vuelve a poder tocarte.
		query = `UPDATE cities
		         SET presence_state = $2, last_online_at = $3, protection_until = NULL, version = version + 1
		         WHERE id = $1`
		args = []any{cityID, string(state), at}
	case city.PresenceOfflinePending:
		query = `UPDATE cities
		         SET presence_state = $2, last_offline_at = $3, version = version + 1
		         WHERE id = $1`
		args = []any{cityID, string(state), at}
	case city.PresenceProtected:
		// protection_until queda NULL: en el MVP la protección no caduca sola,
		// sólo termina cuando el dueño vuelve. El campo existe para poder
		// introducir un límite superior sin migrar. No se toca ninguna marca
		// temporal: last_offline_at debe conservar el instante de la desconexión,
		// que es desde donde se midió el cooldown.
		query = `UPDATE cities
		         SET presence_state = $2, version = version + 1
		         WHERE id = $1`
		args = []any{cityID, string(state)}
	default:
		return fmt.Errorf("%w: estado de presencia desconocido %q", city.ErrInvalidTransition, state)
	}

	tag, err := db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("actualizar presencia de la ciudad %d: %w", cityID, err)
	}
	if tag.RowsAffected() == 0 {
		return city.ErrNotFound
	}
	return nil
}

// ListPendingProtection devuelve las ciudades en OFFLINE_PENDING cuyo cooldown ya
// venció en el instante indicado.
//
// Se apoya en el índice parcial cities_offline_pending_idx: es una consulta del
// tick de timers y debe ser barata aunque haya cientos de miles de ciudades.
func (r *CityRepo) ListPendingProtection(ctx context.Context, cutoff time.Time) ([]int64, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id FROM cities
		 WHERE presence_state = 'OFFLINE_PENDING' AND last_offline_at IS NOT NULL AND last_offline_at <= $1
		 ORDER BY id`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UpdatePopulation recalcula la población de una ciudad a partir de sus unidades vivas.
func (r *CityRepo) UpdatePopulation(ctx context.Context, db DB, cityID int64) (int32, error) {
	var population int32
	err := db.QueryRow(ctx,
		`UPDATE cities c
		 SET population = COALESCE((
		         SELECT count(*) FROM units u WHERE u.city_id = c.id AND u.status <> 'DEAD'
		     ), 0),
		     version = version + 1
		 WHERE c.id = $1
		 RETURNING c.population`, cityID).Scan(&population)
	if err != nil {
		return 0, fmt.Errorf("recalcular población de la ciudad %d: %w", cityID, normalize(err))
	}
	return population, nil
}

// ListEras carga el catálogo de eras.
func (r *CityRepo) ListEras(ctx context.Context) ([]city.EraDefinition, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT code, name, ordinal, population_cap FROM eras ORDER BY ordinal`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []city.EraDefinition
	for rows.Next() {
		var e city.EraDefinition
		var code string
		if err := rows.Scan(&code, &e.Name, &e.Ordinal, &e.PopulationCap); err != nil {
			return nil, err
		}
		e.Code = city.Era(code)
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanCity(row rowScanner) (*city.City, error) {
	var c city.City
	var era, presence string
	err := row.Scan(&c.ID, &c.OwnerPlayerID, &c.Name, &c.CenterX, &c.CenterY, &era,
		&c.Population, &c.PopulationLimit, &presence,
		&c.LastOnlineAt, &c.LastOfflineAt, &c.ProtectionUntil, &c.Version)
	if err != nil {
		if err = normalize(err); err == ErrNotFound {
			return nil, city.ErrNotFound
		}
		return nil, err
	}
	c.Era = city.Era(era)
	c.PresenceState = city.PresenceState(presence)
	return &c, nil
}
