package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
)

var (
	// ErrVersionConflict: la fila cambió entre la lectura y la escritura. El
	// llamante debe releer y decidir, nunca reintentar a ciegas con los mismos
	// datos: la premisa sobre la que calculó su cambio ya no se cumple.
	ErrVersionConflict = errors.New("conflicto de versión: la fila cambió entre la lectura y la escritura")
	// ErrAlreadyOwned: el territorio ya tiene dueño. No es un error de
	// concurrencia sino una regla de juego (RN-TERR-008): no existe la pérdida
	// de control, así que fundar dentro del territorio de otro no lo cambia de
	// manos. Se distingue de ErrVersionConflict precisamente para que el
	// llamante no reintente algo que nunca va a funcionar.
	ErrAlreadyOwned = errors.New("el territorio ya tiene dueño")
)

// TerritoryRepo persiste la geometría y el control de los territorios.
type TerritoryRepo struct {
	store *Store
}

// NewTerritoryRepo crea el repositorio de territorios.
func NewTerritoryRepo(s *Store) *TerritoryRepo { return &TerritoryRepo{store: s} }

// Seed inserta la geometría y su fila de control inicial.
//
// Los ids NO se envían: los genera la identidad de PostgreSQL, en el orden en
// que llegan las filas. Ese orden es el que produce SeedGrid, así que la
// correspondencia geometría ↔ id es reproducible sin tener que manipular la
// secuencia con `OVERRIDING SYSTEM VALUE`, que además dejaría la secuencia
// desincronizada para la siguiente inserción.
//
// Cada territorio nace con su fila en `territory_control` (RN-TERR-006), en la
// MISMA transacción: un territorio sin control es un estado que ninguna regla
// sabe interpretar, igual que un jugador sin ciudad.
func (r *TerritoryRepo) Seed(ctx context.Context, db DB, seeds []territory.Territory) ([]territory.Territory, error) {
	out := make([]territory.Territory, 0, len(seeds))

	for _, s := range seeds {
		var id int64
		err := db.QueryRow(ctx,
			`INSERT INTO territories (name, min_x, min_y, max_x, max_y)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING id`,
			s.Name, s.MinX, s.MinY, s.MaxX, s.MaxY,
		).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("sembrar territorio %q: %w", s.Name, normalize(err))
		}

		// Los DEFAULT del DDL ya producen NONE / NULL / 0 / false: se insertan
		// sólo la clave y se deja que el esquema fije el estado inicial, para que
		// haya un único sitio donde ese estado esté definido.
		if _, err := db.Exec(ctx,
			`INSERT INTO territory_control (territory_id) VALUES ($1)`, id,
		); err != nil {
			return nil, fmt.Errorf("crear control del territorio %d: %w", id, normalize(err))
		}

		s.ID = id
		out = append(out, s)
	}
	return out, nil
}

// Count devuelve cuántos territorios hay sembrados.
func (r *TerritoryRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.store.pool.QueryRow(ctx, `SELECT count(*) FROM territories`).Scan(&n); err != nil {
		return 0, fmt.Errorf("contar territorios: %w", normalize(err))
	}
	return n, nil
}

// LoadAll devuelve la geometría de todos los territorios en orden ascendente de
// id, que es el orden que exige la construcción del índice (RN-TERR-003).
func (r *TerritoryRepo) LoadAll(ctx context.Context) ([]territory.Territory, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id, name, min_x, min_y, max_x, max_y FROM territories ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("leer territorios: %w", normalize(err))
	}
	defer rows.Close()

	out := make([]territory.Territory, 0, 64)
	for rows.Next() {
		var t territory.Territory
		if err := rows.Scan(&t.ID, &t.Name, &t.MinX, &t.MinY, &t.MaxX, &t.MaxY); err != nil {
			return nil, fmt.Errorf("leer territorio: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// LoadControls devuelve el control vigente de cada territorio, en orden
// ascendente de territory_id.
func (r *TerritoryRepo) LoadControls(ctx context.Context) ([]territory.Control, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT territory_id, owner_type, owner_id, control_points, contested, captured_at, version
		   FROM territory_control ORDER BY territory_id`)
	if err != nil {
		return nil, fmt.Errorf("leer control de territorios: %w", normalize(err))
	}
	defer rows.Close()

	out := make([]territory.Control, 0, 64)
	for rows.Next() {
		c, err := scanControl(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetControl lee el control vigente de un territorio.
func (r *TerritoryRepo) GetControl(ctx context.Context, db DB, territoryID int64) (territory.Control, error) {
	row := db.QueryRow(ctx,
		`SELECT territory_id, owner_type, owner_id, control_points, contested, captured_at, version
		   FROM territory_control WHERE territory_id = $1`, territoryID)

	c, err := scanControl(row)
	if err != nil {
		return territory.Control{}, err
	}
	return c, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanControl(s scanner) (territory.Control, error) {
	var c territory.Control
	var ownerType string
	var ownerID *string
	var capturedAt *time.Time

	if err := s.Scan(
		&c.TerritoryID, &ownerType, &ownerID, &c.ControlPoints, &c.Contested, &capturedAt, &c.Version,
	); err != nil {
		return territory.Control{}, fmt.Errorf("leer control de territorio: %w", normalize(err))
	}
	c.OwnerType = territory.OwnerType(ownerType)
	c.OwnerID = ownerID
	c.CapturedAt = capturedAt
	return c, nil
}

// ClaimForPlayer entrega un territorio sin dueño a un jugador.
//
// Es el ÚNICO productor de cambio de dueño del MVP (RN-TERR-007), y se ejecuta
// dentro de la transacción de la fundación de la ciudad: el cambio de ownership
// es write-through transaccional (spec §10), no puede quedar pendiente de un
// flush posterior.
//
// La concurrencia es optimista sobre `version`. El `WHERE` exige a la vez la
// versión esperada y que el territorio siga sin dueño: dos fundaciones
// simultáneas sobre el mismo territorio no pueden ganar las dos, y la que pierde
// se entera en lugar de sobrescribir en silencio.
func (r *TerritoryRepo) ClaimForPlayer(
	ctx context.Context,
	db DB,
	territoryID int64,
	playerID uuid.UUID,
	capturedAt time.Time,
	expectedVersion int32,
) (territory.Control, error) {
	owner := playerID.String()

	row := db.QueryRow(ctx,
		`UPDATE territory_control
		    SET owner_type = 'PLAYER', owner_id = $2, captured_at = $3, version = version + 1
		  WHERE territory_id = $1 AND version = $4 AND owner_type = 'NONE'
		RETURNING territory_id, owner_type, owner_id, control_points, contested, captured_at, version`,
		territoryID, owner, capturedAt, expectedVersion)

	c, err := scanControl(row)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return territory.Control{}, err
	}

	// No actualizó nada. Releer es lo único que distingue «alguien se me
	// adelantó» de «ya tenía dueño desde antes», y esa distinción decide si al
	// llamante le sirve reintentar.
	actual, errLectura := r.GetControl(ctx, db, territoryID)
	if errLectura != nil {
		return territory.Control{}, fmt.Errorf(
			"el territorio %d no aceptó la reclamación y tampoco se pudo releer: %w", territoryID, errLectura)
	}
	if actual.OwnerType != territory.OwnerNone {
		return actual, fmt.Errorf("%w: territorio %d", ErrAlreadyOwned, territoryID)
	}
	return actual, fmt.Errorf(
		"%w: territorio %d esperaba versión %d y tiene %d",
		ErrVersionConflict, territoryID, expectedVersion, actual.Version)
}

// SeedInTx siembra la rejilla completa dentro de una única transacción.
//
// La atomicidad importa: una rejilla a medias dejaría tiles sin territorio sin
// que nada lo indicase, y el arranque siguiente encontraría `territories` no
// vacía y no volvería a sembrar. El hueco sería permanente y silencioso.
func (r *TerritoryRepo) SeedInTx(ctx context.Context, seeds []territory.Territory) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		_, err := r.Seed(ctx, tx, seeds)
		return err
	})
}
