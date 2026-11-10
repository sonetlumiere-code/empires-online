package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/craft"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
)

// Persistencia de la elaboración del taller (SPEC-CIVILIZATION-GOODS §4.3,
// ADR-036 §4). Cada escritura es una transacción idempotente: la cola puede
// reintentarla.

// Eventos de la elaboración. No implican a otro jugador: no entran en el
// registro ni en los avisos.
const (
	EventCraftOrdered   = "CraftOrdered"
	EventCraftCompleted = "CraftCompleted"
)

// ListCrafts lee las elaboraciones en curso, por ciudad.
func (r *EconomyRepo) ListCrafts(ctx context.Context) ([]craft.Craft, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT city_id, good, amount, completes_at_ms, request_id FROM city_crafts ORDER BY city_id`)
	if err != nil {
		return nil, fmt.Errorf("leer elaboraciones: %w", err)
	}
	defer rows.Close()
	var out []craft.Craft
	for rows.Next() {
		var c craft.Craft
		var good string
		if err := rows.Scan(&c.CityID, &good, &c.Amount, &c.CompletesAtMs, &c.RequestID); err != nil {
			return nil, err
		}
		c.Good = economy.Resource(good)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Craft debita el coste y encarga la elaboración en UNA transacción
// (RN-GOODS-004, INV-ECON-002): un delta versionado por recurso; si alguno no
// se aplica, no se aplica ninguno. Idempotente por el request_id de la fila.
// Rechazos de dominio: craft.ErrBusy (otra elaboración en curso),
// craft.ErrNoWorkshop (sin taller completo), economy.ErrInsufficientResources y
// ErrStaleStock.
func (r *EconomyRepo) Craft(
	ctx context.Context, requestID string, playerID uuid.UUID, c craft.Craft, deltas []StockDelta, tick uint64,
) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		var previa string
		switch err := tx.QueryRow(ctx,
			`SELECT request_id FROM city_crafts WHERE city_id = $1 FOR UPDATE`, c.CityID).Scan(&previa); {
		case err == nil:
			if previa == requestID {
				return nil // ya escrito: un reintento
			}
			return craft.ErrBusy
		case !errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("leer la elaboración de la ciudad %d: %w", c.CityID, normalize(err))
		}

		// La RAM decide que hay taller completo; la base lo repite como última
		// barrera, por si un asedio lo derribó entretanto (RN-GOODS-007).
		var taller bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM buildings WHERE city_id = $1 AND type = 'WORKSHOP' AND status = 'COMPLETE')`,
			c.CityID).Scan(&taller); err != nil {
			return fmt.Errorf("leer el taller de la ciudad %d: %w", c.CityID, normalize(err))
		}
		if !taller {
			return craft.ErrNoWorkshop
		}

		for _, d := range deltas {
			aplicado, err := applyStock(ctx, tx, d)
			if err != nil {
				return err
			}
			if !aplicado {
				return ErrStaleStock
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO city_crafts (city_id, good, amount, completes_at_ms, request_id) VALUES ($1, $2, $3, $4, $5)`,
			c.CityID, string(c.Good), c.Amount, c.CompletesAtMs, requestID); err != nil {
			return fmt.Errorf("insertar la elaboración: %w", normalize(err))
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventCraftOrdered, Tick: tick, PlayerID: &playerID,
			Payload: map[string]any{
				"cityId": c.CityID, "good": string(c.Good), "amount": c.Amount,
				"completesAtMs": c.CompletesAtMs, "requestId": requestID,
			},
		})
	})
}

// CompleteCraft entrega una elaboración: borra su fila y acredita el bien con
// su delta versionado, en UNA transacción (ADR-036 §4). Si la fila ya no está
// —un reintento cuya primera vez se confirmó, o un taller derribado—, no
// acredita nada: es lo que impide entregar dos veces (INV-ECON-009).
func (r *EconomyRepo) CompleteCraft(
	ctx context.Context, cityID int64, requestID string, playerID uuid.UUID, d StockDelta, tick uint64,
) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		var good string
		var amount int32
		switch err := tx.QueryRow(ctx,
			`DELETE FROM city_crafts WHERE city_id = $1 AND request_id = $2 RETURNING good, amount`,
			cityID, requestID).Scan(&good, &amount); {
		case errors.Is(normalize(err), ErrNotFound):
			return nil
		case err != nil:
			return fmt.Errorf("cerrar la elaboración de la ciudad %d: %w", cityID, normalize(err))
		}
		if economy.Resource(good) != d.Resource {
			return fmt.Errorf("la elaboración de la ciudad %d es de %s y se entrega %s", cityID, good, d.Resource)
		}
		aplicado, err := applyStock(ctx, tx, d)
		if err != nil {
			return err
		}
		if !aplicado {
			return ErrStaleStock
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventCraftCompleted, Tick: tick, PlayerID: &playerID,
			Payload: map[string]any{"cityId": cityID, "good": good, "amount": amount, "requestId": requestID},
		})
	})
}
