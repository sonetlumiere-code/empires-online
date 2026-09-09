package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

// UnitRepo persiste unidades.
type UnitRepo struct {
	store     *Store
	chunkSize int32
}

// NewUnitRepo crea el repositorio. Necesita el tamaño de chunk porque `units`
// guarda su chunk desnormalizado: es la clave del interest management.
func NewUnitRepo(s *Store, chunkSize int32) *UnitRepo {
	return &UnitRepo{store: s, chunkSize: chunkSize}
}

const unitColumns = `id, player_id, city_id, unit_type, x, y, hp, max_hp, status`

// ListAlive carga todas las unidades vivas del mundo.
//
// Se ejecuta una vez, al arrancar, para hidratar la simulación en memoria.
// Cuando el mundo deje de caber en RAM esta carga pasará a ser por región.
func (r *UnitRepo) ListAlive(ctx context.Context) ([]*unit.Unit, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT `+unitColumns+` FROM units WHERE status <> 'DEAD' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*unit.Unit
	for rows.Next() {
		u, err := scanUnit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ListByPlayer carga las unidades vivas de un jugador.
func (r *UnitRepo) ListByPlayer(ctx context.Context, playerID uuid.UUID) ([]*unit.Unit, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT `+unitColumns+` FROM units WHERE player_id = $1 AND status <> 'DEAD' ORDER BY id`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*unit.Unit
	for rows.Next() {
		u, err := scanUnit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Create inserta una unidad y devuelve su identificador asignado.
func (r *UnitRepo) Create(ctx context.Context, db DB, u *unit.Unit) (int64, error) {
	var id int64
	err := db.QueryRow(ctx,
		`INSERT INTO units (player_id, city_id, unit_type, x, y, hp, max_hp, status, chunk_x, chunk_y)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id`,
		u.PlayerID, u.CityID, string(u.Type), u.X, u.Y, u.HP, u.MaxHP, string(u.Status),
		u.X/r.chunkSize, u.Y/r.chunkSize,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insertar unidad: %w", normalize(err))
	}
	u.ID = id
	return id, nil
}

// PositionUpdate es una posición consolidada pendiente de escribir.
type PositionUpdate struct {
	UnitID int64
	X      int32
	Y      int32
	Status unit.Status
}

// FlushPositions escribe un lote de posiciones en UNA sola consulta.
//
// Es el corazón de la estrategia de persistencia: jamás se hace un UPDATE por
// unidad y por tick. Las posiciones se marcan sucias en memoria y se vuelcan
// cada EO_PERSISTENCE_FLUSH_INTERVAL_TICKS en un único statement.
// Ver ../../../../docs/database/persistence-strategy.md
func (r *UnitRepo) FlushPositions(ctx context.Context, db DB, updates []PositionUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	ids := make([]int64, len(updates))
	xs := make([]int32, len(updates))
	ys := make([]int32, len(updates))
	statuses := make([]string, len(updates))
	chunkXs := make([]int32, len(updates))
	chunkYs := make([]int32, len(updates))

	for i, u := range updates {
		ids[i] = u.UnitID
		xs[i] = u.X
		ys[i] = u.Y
		statuses[i] = string(u.Status)
		chunkXs[i] = u.X / r.chunkSize
		chunkYs[i] = u.Y / r.chunkSize
	}

	// UPDATE ... FROM unnest(...) es la forma barata de aplicar N filas de una vez:
	// una única ida y vuelta y un único plan de ejecución.
	_, err := db.Exec(ctx,
		`UPDATE units u
		 SET x = v.x, y = v.y, status = v.status, chunk_x = v.chunk_x, chunk_y = v.chunk_y
		 FROM (
		     SELECT * FROM unnest($1::bigint[], $2::int[], $3::int[], $4::text[], $5::int[], $6::int[])
		         AS t(id, x, y, status, chunk_x, chunk_y)
		 ) AS v
		 WHERE u.id = v.id`,
		ids, xs, ys, statuses, chunkXs, chunkYs)
	if err != nil {
		return fmt.Errorf("volcar %d posiciones: %w", len(updates), err)
	}
	return nil
}

// SetStatus cambia el estado de una unidad.
func (r *UnitRepo) SetStatus(ctx context.Context, db DB, unitID int64, status unit.Status) error {
	tag, err := db.Exec(ctx, `UPDATE units SET status = $2 WHERE id = $1`, unitID, string(status))
	if err != nil {
		return fmt.Errorf("actualizar estado de la unidad %d: %w", unitID, err)
	}
	if tag.RowsAffected() == 0 {
		return unit.ErrNotFound
	}
	return nil
}

// CountAlive devuelve el número de unidades vivas del mundo (para métricas).
func (r *UnitRepo) CountAlive(ctx context.Context) (int64, error) {
	var n int64
	err := r.store.pool.QueryRow(ctx, `SELECT count(*) FROM units WHERE status <> 'DEAD'`).Scan(&n)
	return n, err
}

func scanUnit(row rowScanner) (*unit.Unit, error) {
	var u unit.Unit
	var unitType, status string
	if err := row.Scan(&u.ID, &u.PlayerID, &u.CityID, &unitType, &u.X, &u.Y, &u.HP, &u.MaxHP, &status); err != nil {
		if err = normalize(err); err == ErrNotFound {
			return nil, unit.ErrNotFound
		}
		return nil, err
	}
	u.Type = unit.Type(unitType)
	u.Status = unit.Status(status)
	return &u, nil
}
