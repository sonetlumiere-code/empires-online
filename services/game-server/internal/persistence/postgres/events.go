package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// worldEvent es una fila del log append-only del mundo.
//
// Los eventos son HECHOS CONSUMADOS, en pasado. No son comandos ni estado: son la
// traza de lo que ocurrió, y la base para auditoría, depuración de incidentes y,
// más adelante, replays. Ver ../../../../docs/architecture/overview.md
type worldEvent struct {
	EventType string
	Tick      uint64
	PlayerID  *uuid.UUID
	// CounterpartID es el otro jugador implicado (ADR-028 §2): el autor de una
	// muerte o de un saqueo, el otro firmante de un tratado. nil si no hay.
	CounterpartID *uuid.UUID
	Payload       map[string]any
}

// counterpart devuelve el jugador como otro implicado, o nil si no lo hay.
func counterpart(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func appendEvent(ctx context.Context, db DB, ev worldEvent) error {
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("serializar el evento %s: %w", ev.EventType, err)
	}
	_, err = db.Exec(ctx,
		`INSERT INTO world_events (event_type, tick, player_id, counterpart_player_id, payload)
		 VALUES ($1, $2, $3, $4, $5)`,
		ev.EventType, ev.Tick, ev.PlayerID, ev.CounterpartID, payload)
	if err != nil {
		return fmt.Errorf("registrar el evento %s: %w", ev.EventType, err)
	}
	return nil
}

// EventRepo escribe en el log de eventos del mundo.
type EventRepo struct{ store *Store }

// NewEventRepo crea el repositorio.
func NewEventRepo(s *Store) *EventRepo { return &EventRepo{store: s} }

// Append registra un evento del mundo.
func (r *EventRepo) Append(ctx context.Context, eventType string, tick uint64, playerID *uuid.UUID, payload map[string]any) error {
	return appendEvent(ctx, r.store.pool, worldEvent{
		EventType: eventType,
		Tick:      tick,
		PlayerID:  playerID,
		Payload:   payload,
	})
}
