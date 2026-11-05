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

// La conquista de territorios (SPEC-TERRITORY-CONQUEST, ADR-034). La simulación
// decide; este repositorio escribe cada reclamación con su fila, y la captura
// con el control, el límite de las dos ciudades y su evento en una transacción.

// ConquestRepo escribe y lee las reclamaciones de territorio.
type ConquestRepo struct{ store *Store }

// NewConquestRepo crea el repositorio.
func NewConquestRepo(s *Store) *ConquestRepo { return &ConquestRepo{store: s} }

// ClaimStart es el comienzo de una reclamación.
type ClaimStart struct {
	Claim         territory.Claim
	TerritoryName string
	Tick          uint64
}

// ClaimEnd es el fin sin captura de una reclamación.
type ClaimEnd struct {
	ClaimID     int64
	TerritoryID int64
	Reason      string
}

// Capture es la captura de un territorio (ADR-034 §3).
type Capture struct {
	ClaimID         int64
	TerritoryID     int64
	TerritoryName   string
	NewOwner        uuid.UUID
	NewCityID       int64
	NewBonus        int32
	PrevOwner       uuid.UUID
	PrevCityID      int64
	PrevBonus       int32
	ExpectedVersion int32
	CapturedAt      time.Time
	Tick            uint64
	AtMs            int64
	X, Y            int32
}

// StartClaim escribe la reclamación ACTIVE, marca el territorio en disputa y,
// si tiene dueño, registra TerritoryClaimStarted (ADR-034 §1). Un reintento de
// la misma petición devuelve la fila que ya escribió. Otra reclamación activa
// sobre el territorio o el guerrero (INV-TERR-010) es ErrConflict.
func (r *ConquestRepo) StartClaim(ctx context.Context, s ClaimStart) (int64, error) {
	var id int64
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		c := s.Claim
		err := tx.QueryRow(ctx,
			`INSERT INTO territory_claims
			   (territory_id, unit_id, player_id, city_id, owner_player_id, x, y,
			    started_at_ms, completes_at_ms, status, request_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'ACTIVE', $10)
			 ON CONFLICT (player_id, request_id) DO NOTHING
			 RETURNING id`,
			c.TerritoryID, c.UnitID, c.PlayerID, c.CityID, c.OwnerPlayerID, c.X, c.Y,
			c.StartedAtMs, c.CompletesAtMs, c.RequestID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			// Ya estaba escrita: un reintento de la cola.
			return tx.QueryRow(ctx,
				`SELECT id FROM territory_claims WHERE player_id = $1 AND request_id = $2`,
				c.PlayerID, c.RequestID).Scan(&id)
		}
		if err != nil {
			if IsUniqueViolation(err) {
				return fmt.Errorf("%w: el territorio %d o el guerrero %d ya reclaman", ErrConflict, c.TerritoryID, c.UnitID)
			}
			return fmt.Errorf("escribir la reclamación del territorio %d: %w", c.TerritoryID, normalize(err))
		}
		if _, err := tx.Exec(ctx,
			`UPDATE territory_control SET contested = true WHERE territory_id = $1`, c.TerritoryID); err != nil {
			return fmt.Errorf("marcar en disputa el territorio %d: %w", c.TerritoryID, normalize(err))
		}
		if c.OwnerPlayerID == nil {
			return nil
		}
		owner := *c.OwnerPlayerID
		return appendEvent(ctx, tx, worldEvent{
			EventType: territory.EventClaimStarted, Tick: s.Tick, PlayerID: &owner,
			CounterpartID: counterpart(c.PlayerID),
			Payload: map[string]any{
				"claimId": id, "territoryId": c.TerritoryID, "territoryName": s.TerritoryName,
				"unitId": c.UnitID, "x": c.X, "y": c.Y, "atMs": c.StartedAtMs, "endsAtMs": c.CompletesAtMs,
			},
		})
	})
	return id, err
}

// EndClaim cierra una reclamación sin captura y quita la disputa. Idempotente:
// una reclamación ya cerrada no se toca.
func (r *ConquestRepo) EndClaim(ctx context.Context, e ClaimEnd) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE territory_claims SET status = 'FAILED', end_reason = $2, ended_at = now()
			  WHERE id = $1 AND status = 'ACTIVE'`, e.ClaimID, e.Reason)
		if err != nil {
			return fmt.Errorf("cerrar la reclamación %d: %w", e.ClaimID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx,
			`UPDATE territory_control SET contested = false WHERE territory_id = $1`, e.TerritoryID); err != nil {
			return fmt.Errorf("quitar la disputa del territorio %d: %w", e.TerritoryID, normalize(err))
		}
		return nil
	})
}

// CaptureTerritory es la transacción de ADR-034 §3: cierra la reclamación,
// comprueba la capital (INV-TERR-011), cambia el dueño con la versión que conocía
// la RAM, mueve el límite de las dos ciudades y registra TerritoryControlChanged.
// Un reintento de una captura ya escrita devuelve el control vigente. Lo que no
// casa —la capital, otra versión u otro dueño— es ErrConflict y no escribe nada.
func (r *ConquestRepo) CaptureTerritory(ctx context.Context, c Capture) (territory.Control, error) {
	var out territory.Control
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		// 1. La reclamación.
		tag, err := tx.Exec(ctx,
			`UPDATE territory_claims SET status = 'COMPLETED', end_reason = 'CAPTURED', ended_at = now()
			  WHERE id = $1 AND status = 'ACTIVE'`, c.ClaimID)
		if err != nil {
			return fmt.Errorf("cerrar la reclamación %d: %w", c.ClaimID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			var estado string
			if err := tx.QueryRow(ctx,
				`SELECT status FROM territory_claims WHERE id = $1`, c.ClaimID).Scan(&estado); err != nil {
				return fmt.Errorf("leer la reclamación %d: %w", c.ClaimID, normalize(err))
			}
			if territory.ClaimStatus(estado) != territory.ClaimCompleted {
				return fmt.Errorf("%w: la reclamación %d está %s", ErrConflict, c.ClaimID, estado)
			}
			// Ya escrita: un reintento.
			out, err = NewTerritoryRepo(r.store).GetControl(ctx, tx, c.TerritoryID)
			return err
		}

		// 2. INV-TERR-011: la capital del dueño actual no cambia de manos.
		if c.PrevOwner != uuid.Nil {
			var capital bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (
				   SELECT 1 FROM cities ci JOIN territories t ON t.id = $1
				    WHERE ci.owner_player_id = $2
				      AND ci.center_x BETWEEN t.min_x AND t.max_x
				      AND ci.center_y BETWEEN t.min_y AND t.max_y)`,
				c.TerritoryID, c.PrevOwner).Scan(&capital); err != nil {
				return fmt.Errorf("comprobar la capital del territorio %d: %w", c.TerritoryID, normalize(err))
			}
			if capital {
				return fmt.Errorf("%w: el territorio %d es la capital de su dueño (INV-TERR-011)", ErrConflict, c.TerritoryID)
			}
		}

		// 3. El dueño, con la versión y el dueño que conocía la RAM.
		var prev *string
		if c.PrevOwner != uuid.Nil {
			p := c.PrevOwner.String()
			prev = &p
		}
		row := tx.QueryRow(ctx,
			`UPDATE territory_control
			    SET owner_type = 'PLAYER', owner_id = $2, captured_at = $3, contested = false, version = version + 1
			  WHERE territory_id = $1 AND version = $4 AND owner_id IS NOT DISTINCT FROM $5
			RETURNING territory_id, owner_type, owner_id, control_points, contested, captured_at, version`,
			c.TerritoryID, c.NewOwner.String(), c.CapturedAt, c.ExpectedVersion, prev)
		out, err = scanControl(row)
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: el control del territorio %d cambió (versión %d)", ErrConflict, c.TerritoryID, c.ExpectedVersion)
		}
		if err != nil {
			return err
		}

		// 4. El límite de las dos ciudades, como deltas (INV-ECON-004).
		for _, d := range []struct {
			city  int64
			delta int32
		}{{c.NewCityID, c.NewBonus}, {c.PrevCityID, -c.PrevBonus}} {
			if d.city == 0 || d.delta == 0 {
				continue
			}
			if _, err := tx.Exec(ctx,
				`UPDATE cities SET population_limit = population_limit + $2, version = version + 1 WHERE id = $1`,
				d.city, d.delta); err != nil {
				return fmt.Errorf("mover el límite de la ciudad %d: %w", d.city, normalize(err))
			}
		}

		// 5. El evento: el anterior dueño y el nuevo, como la víctima y el autor
		// de un saqueo; sin anterior, el nuevo y nadie (ADR-034 §3).
		player, other := c.NewOwner, uuid.Nil
		if c.PrevOwner != uuid.Nil {
			player, other = c.PrevOwner, c.NewOwner
		}
		payload := map[string]any{
			"territoryId": c.TerritoryID, "territoryName": c.TerritoryName,
			"ownerType": string(territory.OwnerPlayer), "ownerId": c.NewOwner.String(), "version": out.Version,
			"claimId": c.ClaimID, "x": c.X, "y": c.Y, "atMs": c.AtMs,
		}
		if c.PrevOwner != uuid.Nil {
			payload["previousOwnerId"] = c.PrevOwner.String()
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: territory.EventControlChanged, Tick: c.Tick, PlayerID: &player,
			CounterpartID: counterpart(other), Payload: payload,
		})
	})
	return out, err
}

// ListActiveClaims devuelve las reclamaciones activas, por territorio, para la
// carga al arrancar (ADR-034 §5).
func (r *ConquestRepo) ListActiveClaims(ctx context.Context) ([]territory.Claim, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id, territory_id, unit_id, player_id, city_id, owner_player_id, x, y,
		        started_at_ms, completes_at_ms, request_id
		   FROM territory_claims WHERE status = 'ACTIVE' ORDER BY territory_id`)
	if err != nil {
		return nil, fmt.Errorf("leer las reclamaciones activas: %w", normalize(err))
	}
	defer rows.Close()
	var out []territory.Claim
	for rows.Next() {
		var c territory.Claim
		if err := rows.Scan(&c.ID, &c.TerritoryID, &c.UnitID, &c.PlayerID, &c.CityID, &c.OwnerPlayerID,
			&c.X, &c.Y, &c.StartedAtMs, &c.CompletesAtMs, &c.RequestID); err != nil {
			return nil, fmt.Errorf("leer una reclamación: %w", normalize(err))
		}
		out = append(out, c)
	}
	return out, normalize(rows.Err())
}
