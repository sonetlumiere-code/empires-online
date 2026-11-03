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
	allows_garrison, proposed_at, accepted_at, expires_at, broken_at,
	proposed_by, request_id, break_effective_at, closed_at`

// TreatyRepo persiste los tratados entre jugadores.
type TreatyRepo struct {
	store *Store
}

// NewTreatyRepo crea el repositorio de tratados.
func NewTreatyRepo(s *Store) *TreatyRepo { return &TreatyRepo{store: s} }

// Create inserta un tratado.
//
// Es la vía administrativa o de semilla, sin orden de un jugador. Los que
// proponen los jugadores entran por ApplyTreaty (SPEC-DIPLOMACY, ADR-027).
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
		      accepted_at, expires_at, broken_at, proposed_by, break_effective_at, closed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING `+treatyColumns,
		t.PlayerA, t.PlayerB, string(t.Type), string(t.Status), t.AllowsGarrison,
		t.AcceptedAt, t.ExpiresAt, t.BrokenAt, t.ProposedBy, t.BreakEffectiveAt, t.ClosedAt)

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
		    SET status = $3, accepted_at = $4, broken_at = $5, closed_at = $6
		  WHERE id = $1 AND status = $2
		RETURNING `+treatyColumns,
		t.ID, string(t.Status), string(actualizado.Status),
		actualizado.AcceptedAt, actualizado.BrokenAt, actualizado.ClosedAt)

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
	var requestID *string

	if err := s.Scan(
		&t.ID, &t.PlayerA, &t.PlayerB, &tipo, &estado,
		&t.AllowsGarrison, &t.ProposedAt, &t.AcceptedAt, &t.ExpiresAt, &t.BrokenAt,
		&t.ProposedBy, &requestID, &t.BreakEffectiveAt, &t.ClosedAt,
	); err != nil {
		return diplomacy.Treaty{}, fmt.Errorf("leer tratado: %w", normalize(err))
	}
	t.Type = diplomacy.Type(tipo)
	t.Status = diplomacy.Status(estado)
	if requestID != nil {
		t.RequestID = *requestID
	}
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
//
// Es idempotente por construcción (ADR-017 §2.2): si la unidad ya tiene su fila
// en ESA ciudad, la entrada ya está escrita —un reintento tras perder la
// confirmación del COMMIT— y se devuelve éxito sin un segundo evento. Una fila
// en otra ciudad es ErrAlreadyGarrisoned.
func (r *GarrisonRepo) Enter(
	ctx context.Context, u *unit.Unit, c *city.City, requester uuid.UUID, tick uint64,
) (int64, error) {
	var cancelado int64

	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		var existente int64
		switch err := tx.QueryRow(ctx,
			`SELECT city_id FROM garrisons WHERE unit_id = $1 FOR UPDATE`, u.ID).Scan(&existente); {
		case err == nil:
			if c != nil && existente == c.ID {
				return nil
			}
			return garrison.ErrAlreadyGarrisoned
		case !errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("leer la guarnición de la unidad %d: %w", u.ID, normalize(err))
		}

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
		// RN-ECON-003: guarnecer cancela la tarea. CanEnter exige IDLE, y una
		// unidad IDLE no tiene tarea (INV-ECON-003): es defensa en profundidad.
		if err := DeleteTask(ctx, tx, u.ID); err != nil {
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

		payload := map[string]any{
			"unitId":            u.ID,
			"cityId":            c.ID,
			"cancelledMovement": cancelado,
		}
		// INV-GARR-004: el tratado que autorizó la entrada queda en el evento, que es
		// lo que hace auditable «en el instante de la entrada». En ciudad propia no
		// hay ninguno.
		if len(tratados) > 0 {
			payload["treatyId"] = tratados[0].ID
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: "UnitGarrisoned",
			Tick:      tick,
			PlayerID:  &u.PlayerID,
			Payload:   payload,
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
		tag, err := tx.Exec(ctx, `DELETE FROM garrisons WHERE unit_id = $1`, u.ID)
		if err != nil {
			return fmt.Errorf("cerrar la guarnición de la unidad %d: %w", u.ID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			// Ya estaba fuera: un reintento de una salida escrita. Idempotente,
			// sin un segundo evento (ADR-017 §2.3).
			return nil
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

// ─────────────────────────────────────────────────────────────
// Tratados en caliente (SPEC-DIPLOMACY, ADR-027)
// ─────────────────────────────────────────────────────────────

// ListOpen devuelve los tratados PROPOSED y ACTIVE, en orden de id: lo que la
// simulación carga al arrancar (ADR-027 §1).
func (r *TreatyRepo) ListOpen(ctx context.Context) ([]diplomacy.Treaty, error) {
	return r.query(ctx,
		`SELECT `+treatyColumns+` FROM treaties WHERE status IN ('PROPOSED', 'ACTIVE') ORDER BY id`)
}

// ListRejectedSince devuelve las ofertas rechazadas desde `since`: las que aún
// frenan una oferta repetida (RN-DIPLO-008).
func (r *TreatyRepo) ListRejectedSince(ctx context.Context, since time.Time) ([]diplomacy.Treaty, error) {
	return r.query(ctx,
		`SELECT `+treatyColumns+` FROM treaties
		  WHERE status = 'REJECTED' AND closed_at >= $1 ORDER BY id`, since)
}

// TreatyWrite es una orden o un vencimiento ya decididos por la simulación.
type TreatyWrite struct {
	// Insert: es una oferta nueva; Treaty no tiene id todavía.
	Insert bool
	// Treaty es el tratado tal como queda.
	Treaty diplomacy.Treaty
	// From es el estado que debe tener la fila para aplicar la escritura.
	From diplomacy.Status
	// Supersede es el pacto que una alianza aceptada cierra (EXPIRED) en la
	// misma transacción; nil si no hay.
	Supersede *diplomacy.Treaty
	// Event es el world_events de la escritura.
	Event string
	Tick  uint64
}

// ApplyTreaty escribe una orden o un vencimiento con su evento, en una
// transacción, y devuelve el id del tratado. Es idempotente (ADR-027 §2):
// una oferta repetida con el mismo (proposed_by, request_id) devuelve la que
// ya existe, y una actualización que encuentra la fila en el estado de destino
// termina sin escribir el evento otra vez. Un índice de INV-DIPLO-002 violado
// es ErrConflict.
func (r *TreatyRepo) ApplyTreaty(ctx context.Context, w TreatyWrite) (int64, error) {
	var id int64
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		t := w.Treaty
		if w.Insert {
			switch err := tx.QueryRow(ctx,
				`SELECT id FROM treaties WHERE proposed_by = $1 AND request_id = $2`,
				t.ProposedBy, t.RequestID).Scan(&id); {
			case err == nil:
				return nil // ya escrita: un reintento
			case !errors.Is(normalize(err), ErrNotFound):
				return fmt.Errorf("buscar la oferta %s: %w", t.RequestID, normalize(err))
			}
			if err := tx.QueryRow(ctx,
				`INSERT INTO treaties (player_a_id, player_b_id, treaty_type, status, allows_garrison,
				                       proposed_at, proposed_by, request_id)
				 VALUES ($1, $2, $3, 'PROPOSED', $4, $5, $6, $7) RETURNING id`,
				t.PlayerA, t.PlayerB, string(t.Type), t.AllowsGarrison, t.ProposedAt, t.ProposedBy, t.RequestID,
			).Scan(&id); err != nil {
				return fmt.Errorf("insertar la oferta: %w", normalize(err))
			}
			t.ID = id
			return appendEvent(ctx, tx, treatyEvent(w.Event, w.Tick, t))
		}

		id = t.ID
		if w.Supersede != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE treaties SET status = 'EXPIRED' WHERE id = $1 AND status = 'ACTIVE'`,
				w.Supersede.ID); err != nil {
				return fmt.Errorf("cerrar el pacto %d: %w", w.Supersede.ID, normalize(err))
			}
		}
		tag, err := tx.Exec(ctx,
			`UPDATE treaties
			    SET status = $3, accepted_at = $4, broken_at = $5, closed_at = $6, break_effective_at = $7
			  WHERE id = $1 AND status = $2
			    AND (status <> $3 OR break_effective_at IS DISTINCT FROM $7)`,
			t.ID, string(w.From), string(t.Status), t.AcceptedAt, t.BrokenAt, t.ClosedAt, t.BreakEffectiveAt)
		if err != nil {
			return fmt.Errorf("escribir el tratado %d: %w", t.ID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			// O ya estaba escrito (un reintento), o la fila cambió por otra vía.
			var estado string
			var ruptura *time.Time
			if err := tx.QueryRow(ctx, `SELECT status, break_effective_at FROM treaties WHERE id = $1`, t.ID).
				Scan(&estado, &ruptura); err != nil {
				return fmt.Errorf("leer el tratado %d: %w", t.ID, normalize(err))
			}
			if diplomacy.Status(estado) == t.Status && sameInstant(ruptura, t.BreakEffectiveAt) {
				return nil
			}
			return fmt.Errorf("%w: el tratado %d está en %s", ErrVersionConflict, t.ID, estado)
		}
		return appendEvent(ctx, tx, treatyEvent(w.Event, w.Tick, t))
	})
	return id, err
}

func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func treatyEvent(name string, tick uint64, t diplomacy.Treaty) worldEvent {
	payload := map[string]any{
		"treatyId": t.ID, "playerA": t.PlayerA, "playerB": t.PlayerB,
		"treatyType": string(t.Type), "status": string(t.Status),
	}
	if t.BreakEffectiveAt != nil {
		payload["breakEffectiveAt"] = t.BreakEffectiveAt.UTC()
	}
	return worldEvent{EventType: name, Tick: tick, PlayerID: t.ProposedBy, Payload: payload}
}
