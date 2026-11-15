package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FactionRepo escribe la entrada de un jugador en un bando y su vencimiento
// (SPEC-FACTIONS, ADR-039 §3 y §4).
type FactionRepo struct {
	store *Store
}

// NewFactionRepo construye el repositorio.
func NewFactionRepo(s *Store) *FactionRepo { return &FactionRepo{store: s} }

// FactionJoinOrder es una entrada en un bando que el tick ya validó.
type FactionJoinOrder struct {
	PlayerID  uuid.UUID
	RequestID string
	Faction   string
	// At es el instante en que la entrada cuenta.
	At   time.Time
	Tick uint64
	AtMs int64
}

// FactionJoinCompletion es el vencimiento de una entrada con los pactos y
// alianzas que rompe.
type FactionJoinCompletion struct {
	PlayerID uuid.UUID
	Faction  string
	// At es el instante de la entrada que vence.
	At     time.Time
	Breaks []TreatyWrite
	Tick   uint64
	AtMs   int64
}

// RequestJoin apunta la entrada pendiente con su evento FactionJoinOrdered,
// sólo si el jugador sigue NEUTRAL sin entrada (INV-PLAYER-013). Un reintento
// que encuentra la misma entrada termina sin escribir; cualquier otro estado es
// ErrConflict.
func (r *FactionRepo) RequestJoin(ctx context.Context, o FactionJoinOrder) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE players
			    SET faction_join_id = (SELECT id FROM factions WHERE code = $2), faction_join_at = $3
			  WHERE id = $1
			    AND faction_id = (SELECT id FROM factions WHERE code = 'NEUTRAL')
			    AND faction_join_id IS NULL`,
			o.PlayerID, o.Faction, o.At)
		if err != nil {
			return fmt.Errorf("apuntar la entrada de %s en %s: %w", o.PlayerID, o.Faction, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			join, at, err := currentJoin(ctx, tx, o.PlayerID)
			if err != nil {
				return err
			}
			if join == o.Faction && at != nil && at.Equal(o.At) {
				return nil // ya escrita: un reintento
			}
			return fmt.Errorf("%w: el jugador %s ya no puede unirse a %s", ErrConflict, o.PlayerID, o.Faction)
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: "FactionJoinOrdered", Tick: o.Tick, PlayerID: &o.PlayerID,
			Payload: map[string]any{"faction": o.Faction, "joinsAtMs": o.At.UnixMilli(), "atMs": o.AtMs},
		})
	})
}

// CompleteJoin escribe en una transacción la facción nueva, el borrado de la
// entrada, cada ruptura con su TreatyBreakAnnounced y FactionJoined (ADR-039
// §4). Es tolerante: si la facción ya estaba escrita (un reintento), termina sin
// escribir; una ruptura sobre un tratado que ya no está ACTIVE sin preaviso se
// omite sin evento. Devuelve si escribió la facción. Una entrada que no es la
// esperada y una facción que no es la de la entrada son ErrConflict.
func (r *FactionRepo) CompleteJoin(ctx context.Context, c FactionJoinCompletion) (bool, error) {
	applied := false
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE players
			    SET faction_id = (SELECT id FROM factions WHERE code = $2),
			        faction_join_id = NULL, faction_join_at = NULL
			  WHERE id = $1
			    AND faction_join_id = (SELECT id FROM factions WHERE code = $2)
			    AND faction_join_at = $3`,
			c.PlayerID, c.Faction, c.At)
		if err != nil {
			return fmt.Errorf("escribir la facción de %s: %w", c.PlayerID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			var actual string
			if err := tx.QueryRow(ctx,
				`SELECT f.code FROM players p JOIN factions f ON f.id = p.faction_id WHERE p.id = $1`,
				c.PlayerID).Scan(&actual); err != nil {
				return fmt.Errorf("leer la facción de %s: %w", c.PlayerID, normalize(err))
			}
			if actual == c.Faction {
				return nil // ya escrita, con sus rupturas: un reintento
			}
			return fmt.Errorf("%w: el jugador %s es %s y no tiene la entrada en %s", ErrConflict, c.PlayerID, actual, c.Faction)
		}
		applied = true
		for _, w := range c.Breaks {
			t := w.Treaty
			tag, err := tx.Exec(ctx,
				`UPDATE treaties SET break_effective_at = $2
				  WHERE id = $1 AND status = 'ACTIVE' AND break_effective_at IS NULL`,
				t.ID, t.BreakEffectiveAt)
			if err != nil {
				return fmt.Errorf("romper el tratado %d: %w", t.ID, normalize(err))
			}
			if tag.RowsAffected() == 0 {
				continue // otra escritura lo cerró o lo rompió antes
			}
			if err := appendEvent(ctx, tx, treatyEvent(w, t)); err != nil {
				return err
			}
		}
		return appendEvent(ctx, tx, worldEvent{
			EventType: "FactionJoined", Tick: c.Tick, PlayerID: &c.PlayerID,
			Payload: map[string]any{"faction": c.Faction, "atMs": c.AtMs},
		})
	})
	return applied, err
}

// currentJoin lee la entrada pendiente de un jugador: su bando y su instante,
// o vacío y nil si no la tiene.
func currentJoin(ctx context.Context, db DB, playerID uuid.UUID) (string, *time.Time, error) {
	var join *string
	var at *time.Time
	err := db.QueryRow(ctx,
		`SELECT f.code, p.faction_join_at
		   FROM players p LEFT JOIN factions f ON f.id = p.faction_join_id
		  WHERE p.id = $1`, playerID).Scan(&join, &at)
	if errors.Is(normalize(err), ErrNotFound) {
		return "", nil, fmt.Errorf("%w: el jugador %s no existe", ErrConflict, playerID)
	}
	if err != nil {
		return "", nil, fmt.Errorf("leer la entrada de %s: %w", playerID, normalize(err))
	}
	if join == nil {
		return "", at, nil
	}
	return *join, at, nil
}
