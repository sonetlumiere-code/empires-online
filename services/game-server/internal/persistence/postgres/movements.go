package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
)

// MovementRepo persiste movimientos como polilíneas temporizadas.
type MovementRepo struct{ store *Store }

// NewMovementRepo crea el repositorio.
func NewMovementRepo(s *Store) *MovementRepo { return &MovementRepo{store: s} }

const movementColumns = `id, unit_id, path, target_x, target_y, start_time_ms, arrival_time_ms, status`

// Start crea un movimiento cancelando atómicamente el anterior de la misma unidad.
//
// Las dos operaciones van juntas por necesidad: si se hicieran por separado, entre
// una y otra la unidad podría tener dos movimientos ACTIVE, violando INV-MOVE-001.
// El índice único parcial de la tabla es la última línea de defensa si esto se
// intentara desde dos conexiones a la vez.
//
// Devuelve el id del movimiento creado y el id del cancelado (0 si no había).
func (r *MovementRepo) Start(ctx context.Context, db DB, m *movement.Movement) (newID int64, cancelledID int64, err error) {
	pathJSON, err := json.Marshal(m.Path)
	if err != nil {
		return 0, 0, fmt.Errorf("serializar la polilínea: %w", err)
	}

	// 1. Cancelar el movimiento activo previo, si lo hay.
	err = db.QueryRow(ctx,
		`UPDATE unit_movements
		 SET status = 'CANCELLED', finished_at = now()
		 WHERE unit_id = $1 AND status = 'ACTIVE'
		 RETURNING id`, m.UnitID).Scan(&cancelledID)
	if err != nil {
		if e := normalize(err); e != ErrNotFound {
			return 0, 0, fmt.Errorf("cancelar el movimiento anterior de la unidad %d: %w", m.UnitID, e)
		}
		cancelledID = 0 // no había movimiento previo: caso normal
	}

	// 2. Insertar el nuevo.
	err = db.QueryRow(ctx,
		`INSERT INTO unit_movements (unit_id, path, target_x, target_y, start_time_ms, arrival_time_ms, status)
		 VALUES ($1, $2, $3, $4, $5, $6, 'ACTIVE')
		 RETURNING id`,
		m.UnitID, pathJSON, m.Target.X, m.Target.Y, m.StartTimeMs, m.ArrivalTimeMs,
	).Scan(&newID)
	if err != nil {
		return 0, 0, fmt.Errorf("insertar movimiento de la unidad %d: %w", m.UnitID, normalize(err))
	}

	m.ID = newID
	return newID, cancelledID, nil
}

// ListActive carga todos los movimientos ACTIVE.
//
// Es la consulta de recuperación tras un reinicio: con ella, y sólo con ella, el
// servidor reconstruye dónde está cada unidad en movimiento — incluidas las que
// llegaron a destino mientras el proceso estaba caído.
func (r *MovementRepo) ListActive(ctx context.Context) ([]*movement.Movement, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT `+movementColumns+` FROM unit_movements WHERE status = 'ACTIVE' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*movement.Movement
	for rows.Next() {
		m, err := scanMovement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetActiveByUnit recupera el movimiento activo de una unidad, si existe.
func (r *MovementRepo) GetActiveByUnit(ctx context.Context, unitID int64) (*movement.Movement, error) {
	return scanMovement(r.store.pool.QueryRow(ctx,
		`SELECT `+movementColumns+` FROM unit_movements WHERE unit_id = $1 AND status = 'ACTIVE'`, unitID))
}

// Finish cierra un movimiento con un estado terminal.
func (r *MovementRepo) Finish(ctx context.Context, db DB, movementID int64, status movement.Status) error {
	if status == movement.StatusActive {
		return fmt.Errorf("ACTIVE no es un estado terminal")
	}
	tag, err := db.Exec(ctx,
		`UPDATE unit_movements SET status = $2, finished_at = now()
		 WHERE id = $1 AND status = 'ACTIVE'`, movementID, string(status))
	if err != nil {
		return fmt.Errorf("finalizar movimiento %d: %w", movementID, err)
	}
	if tag.RowsAffected() == 0 {
		// Ya estaba cerrado. Es idempotente a propósito: la finalización puede
		// llegar por el tick y por la recuperación, y no debe fallar por eso.
		return nil
	}
	return nil
}

// CancelActiveByUnit cancela el movimiento activo de una unidad, si lo hay.
// Devuelve el id cancelado, o 0 si no había ninguno.
func (r *MovementRepo) CancelActiveByUnit(ctx context.Context, db DB, unitID int64) (int64, error) {
	var id int64
	err := db.QueryRow(ctx,
		`UPDATE unit_movements SET status = 'CANCELLED', finished_at = now()
		 WHERE unit_id = $1 AND status = 'ACTIVE'
		 RETURNING id`, unitID).Scan(&id)
	if err != nil {
		if e := normalize(err); e == ErrNotFound {
			return 0, nil
		}
		return 0, fmt.Errorf("cancelar movimiento de la unidad %d: %w", unitID, err)
	}
	return id, nil
}

func scanMovement(row rowScanner) (*movement.Movement, error) {
	var m movement.Movement
	var pathJSON []byte
	var status string
	err := row.Scan(&m.ID, &m.UnitID, &pathJSON, &m.Target.X, &m.Target.Y,
		&m.StartTimeMs, &m.ArrivalTimeMs, &status)
	if err != nil {
		return nil, normalize(err)
	}
	if err := json.Unmarshal(pathJSON, &m.Path); err != nil {
		return nil, fmt.Errorf("polilínea del movimiento %d malformada: %w", m.ID, err)
	}
	if len(m.Path) == 0 {
		return nil, fmt.Errorf("movimiento %d con polilínea vacía: %w", m.ID, movement.ErrEmptyPath)
	}
	m.Status = movement.Status(status)
	return &m, nil
}
