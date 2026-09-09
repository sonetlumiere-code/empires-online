package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/garrison"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

const treatyColumns = `id, player_a_id, player_b_id, treaty_type, status,
	allows_garrison, proposed_at, accepted_at, expires_at, broken_at`

// TreatyRepo persiste los tratados entre jugadores.
type TreatyRepo struct {
	store *Store
}

// NewTreatyRepo crea el repositorio de tratados.
func NewTreatyRepo(s *Store) *TreatyRepo { return &TreatyRepo{store: s} }

// Create inserta un tratado.
//
// En el MVP no hay negociación desde el cliente: los tratados nacen por vía
// administrativa o de semilla, y lo que se valida es su EFECTO sobre el dominio.
// Por eso este método existe y no hay ningún comando de red que lo invoque.
func (r *TreatyRepo) Create(ctx context.Context, db DB, t diplomacy.Treaty) (diplomacy.Treaty, error) {
	// El par se ordena antes de escribir: el CHECK `treaties_canonical_pair` lo
	// exige, y dejarlo en manos del llamante convertiría un descuido en un error
	// de base de datos en vez de en una normalización silenciosa.
	t.PlayerA, t.PlayerB = diplomacy.CanonicalPair(t.PlayerA, t.PlayerB)
	if err := t.Validate(); err != nil {
		return diplomacy.Treaty{}, err
	}

	row := db.QueryRow(ctx,
		`INSERT INTO treaties
		     (player_a_id, player_b_id, treaty_type, status, allows_garrison,
		      accepted_at, expires_at, broken_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING `+treatyColumns,
		t.PlayerA, t.PlayerB, string(t.Type), string(t.Status), t.AllowsGarrison,
		t.AcceptedAt, t.ExpiresAt, t.BrokenAt)

	return scanTreaty(row)
}

// ListAll devuelve todos los tratados en orden ascendente de id.
//
// El orden importa: `diplomacy.FindAuthorizing` recorre el slice y devuelve el
// primero que autoriza. Sin un orden estable, dos ejecuciones podrían atribuir la
// autorización a tratados distintos y los logs dejarían de ser reproducibles.
func (r *TreatyRepo) ListAll(ctx context.Context) ([]diplomacy.Treaty, error) {
	return r.query(ctx, `SELECT `+treatyColumns+` FROM treaties ORDER BY id`)
}

// ListActive devuelve los tratados vigentes, en orden ascendente de id.
func (r *TreatyRepo) ListActive(ctx context.Context) ([]diplomacy.Treaty, error) {
	return r.query(ctx,
		`SELECT `+treatyColumns+` FROM treaties WHERE status = 'ACTIVE' ORDER BY id`)
}

// ListExpirable devuelve los tratados activos cuya caducidad ya venció.
//
// Es la consulta de la fase 5 del tick. Filtra en SQL y no en Go a propósito: el
// tick no debe recorrer todos los tratados del mundo para descartar la inmensa
// mayoría.
func (r *TreatyRepo) ListExpirable(ctx context.Context, now time.Time) ([]diplomacy.Treaty, error) {
	return r.query(ctx,
		`SELECT `+treatyColumns+` FROM treaties
		  WHERE status = 'ACTIVE' AND expires_at IS NOT NULL AND expires_at <= $1
		  ORDER BY id`, now)
}

// FindAuthorizingGarrison busca el tratado que autoriza guarnición entre dos
// jugadores.
//
// La consulta ordena el par con LEAST/GREATEST en lugar de hacer un OR de las dos
// direcciones (RN-GARR-007): el índice está sobre el par canónico, y un OR ni lo
// usaría ni respetaría la unicidad que ese índice garantiza.
func (r *TreatyRepo) FindAuthorizingGarrison(ctx context.Context, db DB, x, y uuid.UUID) (diplomacy.Treaty, bool, error) {
	row := db.QueryRow(ctx,
		`SELECT `+treatyColumns+` FROM treaties
		  WHERE player_a_id = LEAST($1::uuid, $2::uuid)
		    AND player_b_id = GREATEST($1::uuid, $2::uuid)
		    AND status = 'ACTIVE'
		    AND allows_garrison
		  ORDER BY id
		  LIMIT 1`, x, y)

	t, err := scanTreaty(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return diplomacy.Treaty{}, false, nil
		}
		return diplomacy.Treaty{}, false, err
	}
	return t, true, nil
}

// Transition aplica un cambio de estado, comprobando antes que la máquina de
// estados lo permite.
//
// La condición `status = $2` del WHERE no es redundante con esa comprobación:
// entre que se leyó el tratado y se escribe, otra transacción pudo cambiarlo. Sin
// ella, una caducidad podría pisar una ruptura que ya se había registrado.
func (r *TreatyRepo) Transition(
	ctx context.Context, db DB, t diplomacy.Treaty, next diplomacy.Status, at time.Time,
) (diplomacy.Treaty, error) {
	actualizado, err := t.TransitionTo(next, at)
	if err != nil {
		return diplomacy.Treaty{}, err
	}

	row := db.QueryRow(ctx,
		`UPDATE treaties
		    SET status = $3, accepted_at = $4, broken_at = $5
		  WHERE id = $1 AND status = $2
		RETURNING `+treatyColumns,
		t.ID, string(t.Status), string(actualizado.Status),
		actualizado.AcceptedAt, actualizado.BrokenAt)

	escrito, err := scanTreaty(row)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return diplomacy.Treaty{}, fmt.Errorf(
				"%w: el tratado %d ya no estaba en %s", ErrVersionConflict, t.ID, t.Status)
		}
		return diplomacy.Treaty{}, err
	}
	return escrito, nil
}

func (r *TreatyRepo) query(ctx context.Context, sql string, args ...any) ([]diplomacy.Treaty, error) {
	rows, err := r.store.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("leer tratados: %w", normalize(err))
	}
	defer rows.Close()

	out := make([]diplomacy.Treaty, 0, 8)
	for rows.Next() {
		t, err := scanTreaty(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanTreaty(s scanner) (diplomacy.Treaty, error) {
	var t diplomacy.Treaty
	var tipo, estado string

	if err := s.Scan(
		&t.ID, &t.PlayerA, &t.PlayerB, &tipo, &estado,
		&t.AllowsGarrison, &t.ProposedAt, &t.AcceptedAt, &t.ExpiresAt, &t.BrokenAt,
	); err != nil {
		return diplomacy.Treaty{}, fmt.Errorf("leer tratado: %w", normalize(err))
	}
	t.Type = diplomacy.Type(tipo)
	t.Status = diplomacy.Status(estado)
	return t, nil
}

// ─────────────────────────────────────────────────────────────
// Guarniciones
// ─────────────────────────────────────────────────────────────

// GarrisonRepo persiste las guarniciones abiertas.
//
// `garrisons` modela SÓLO guarniciones abiertas: al salir, la fila se borra
// (RN-GARR-013). El historial auditable de guarniciones cerradas está fuera de
// MVP y su sitio natural es `world_events`.
type GarrisonRepo struct {
	store     *Store
	units     *UnitRepo
	movements *MovementRepo
	treaties  *TreatyRepo
}

// NewGarrisonRepo crea el repositorio de guarniciones.
func NewGarrisonRepo(s *Store, u *UnitRepo, m *MovementRepo, t *TreatyRepo) *GarrisonRepo {
	return &GarrisonRepo{store: s, units: u, movements: m, treaties: t}
}

// Enter guarnece una unidad en una ciudad, TODO en una transacción.
//
// Las tres escrituras —estado de la unidad, cancelación de su movimiento activo y
// fila de guarnición— son un solo hecho. Una unidad `GARRISONED` con un
// movimiento todavía `ACTIVE` sería un estado que ninguna regla sabe interpretar:
// el bucle la seguiría moviendo mientras el resto del sistema la cree dentro de
// una ciudad.
//
// La autorización se recalcula aquí contra el estado ACTUAL de `treaties`
// (RN-GARR-018), no contra lo que el llamante creyera saber: entre su lectura y
// esta transacción el tratado pudo romperse.
func (r *GarrisonRepo) Enter(
	ctx context.Context, u *unit.Unit, c *city.City, requester uuid.UUID, tick uint64,
) (int64, error) {
	var cancelado int64

	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		tratados, err := r.autorizantes(ctx, tx, u, c)
		if err != nil {
			return err
		}
		if err := garrison.CanEnter(u, c, requester, tratados); err != nil {
			return err
		}

		// El movimiento se cancela ANTES de cambiar el estado: si algo falla
		// después, la transacción revierte las dos cosas, pero el orden deja el
		// código legible como la secuencia de hechos que es.
		cancelado, err = r.movements.CancelActiveByUnit(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		if err := r.units.SetStatus(ctx, tx, u.ID, unit.StatusGarrisoned); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO garrisons (unit_id, city_id) VALUES ($1, $2)`, u.ID, c.ID,
		); err != nil {
			return fmt.Errorf("registrar la guarnición de la unidad %d: %w", u.ID, normalize(err))
		}

		return appendEvent(ctx, tx, worldEvent{
			EventType: "UnitGarrisoned",
			Tick:      tick,
			PlayerID:  &u.PlayerID,
			Payload: map[string]any{
				"unitId":            u.ID,
				"cityId":            c.ID,
				"cancelledMovement": cancelado,
			},
		})
	})
	if err != nil {
		return 0, err
	}

	u.Status = unit.StatusGarrisoned
	return cancelado, nil
}

// autorizantes devuelve el tratado que habilita la entrada, o ninguno.
//
// En ciudad propia no se consulta nada: no hay tratado que buscar y la consulta
// sería trabajo tirado en el camino más frecuente.
func (r *GarrisonRepo) autorizantes(ctx context.Context, db DB, u *unit.Unit, c *city.City) ([]diplomacy.Treaty, error) {
	if c == nil || c.OwnerPlayerID == u.PlayerID {
		return nil, nil
	}
	t, ok, err := r.treaties.FindAuthorizingGarrison(ctx, db, u.PlayerID, c.OwnerPlayerID)
	if err != nil || !ok {
		return nil, err
	}
	return []diplomacy.Treaty{t}, nil
}

// Leave saca una unidad de la guarnición y la deja en el tile indicado.
//
// El tile lo elige el llamante con `garrison.ReentryTile`, que necesita el mundo
// y la capa de ocupación —ninguna de las dos cosas vive aquí—.
func (r *GarrisonRepo) Leave(
	ctx context.Context, u *unit.Unit, x, y int32, chunkX, chunkY int32, tick uint64,
) error {
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE units SET x = $2, y = $3, chunk_x = $4, chunk_y = $5, status = 'IDLE'
			  WHERE id = $1`, u.ID, x, y, chunkX, chunkY,
		); err != nil {
			return fmt.Errorf("devolver la unidad %d al mapa: %w", u.ID, normalize(err))
		}

		// RN-GARR-013: la fila se BORRA. La tabla del MVP no tiene columna de
		// cierre; sólo modela guarniciones abiertas.
		if _, err := tx.Exec(ctx, `DELETE FROM garrisons WHERE unit_id = $1`, u.ID); err != nil {
			return fmt.Errorf("cerrar la guarnición de la unidad %d: %w", u.ID, normalize(err))
		}

		return appendEvent(ctx, tx, worldEvent{
			EventType: "UnitUngarrisoned",
			Tick:      tick,
			PlayerID:  &u.PlayerID,
			Payload:   map[string]any{"unitId": u.ID, "x": x, "y": y},
		})
	})
	if err != nil {
		return err
	}

	u.X, u.Y, u.Status = x, y, unit.StatusIdle
	return nil
}

// CityOf devuelve la ciudad en la que está guarnecida una unidad.
func (r *GarrisonRepo) CityOf(ctx context.Context, unitID int64) (int64, bool, error) {
	var cityID int64
	err := r.store.pool.QueryRow(ctx,
		`SELECT city_id FROM garrisons WHERE unit_id = $1`, unitID).Scan(&cityID)
	if err != nil {
		if errors.Is(normalize(err), ErrNotFound) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("leer la guarnición de la unidad %d: %w", unitID, normalize(err))
	}
	return cityID, true, nil
}

// ListByCity devuelve las unidades guarnecidas en una ciudad, en orden estable.
func (r *GarrisonRepo) ListByCity(ctx context.Context, cityID int64) ([]int64, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT unit_id FROM garrisons WHERE city_id = $1 ORDER BY unit_id`, cityID)
	if err != nil {
		return nil, fmt.Errorf("listar guarnición de la ciudad %d: %w", cityID, normalize(err))
	}
	defer rows.Close()

	out := make([]int64, 0, 8)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ExpireDue caduca todos los tratados vencidos y devuelve los que cambiaron.
//
// Cada tratado va en SU PROPIA transacción, no todos en una. Un tratado cuya
// transición falle —porque otra vía lo rompió entre la consulta y la escritura—
// no debe impedir que caduquen los demás: son hechos independientes y agruparlos
// sólo crearía un fallo compartido.
func (r *TreatyRepo) ExpireDue(ctx context.Context, now time.Time, tick uint64) ([]diplomacy.Treaty, error) {
	vencidos, err := r.ListExpirable(ctx, now)
	if err != nil {
		return nil, err
	}

	caducados := make([]diplomacy.Treaty, 0, len(vencidos))
	for _, t := range vencidos {
		var escrito diplomacy.Treaty
		err := r.store.InTx(ctx, func(tx pgx.Tx) error {
			var err error
			escrito, err = r.Transition(ctx, tx, t, diplomacy.StatusExpired, now)
			if err != nil {
				return err
			}
			return appendEvent(ctx, tx, worldEvent{
				EventType: "TreatyExpired",
				Tick:      tick,
				Payload: map[string]any{
					"treatyId":   escrito.ID,
					"playerA":    escrito.PlayerA,
					"playerB":    escrito.PlayerB,
					"treatyType": string(escrito.Type),
				},
			})
		})
		if err != nil {
			// Un conflicto aquí es esperable y no es un fallo: significa que otra
			// vía ya terminó ese tratado. Se omite y se sigue con el resto.
			if errors.Is(err, ErrVersionConflict) {
				continue
			}
			return caducados, fmt.Errorf("caducar el tratado %d: %w", t.ID, err)
		}
		caducados = append(caducados, escrito)
	}
	return caducados, nil
}
