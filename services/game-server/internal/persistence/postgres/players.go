package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
)

// PlayerRepo persiste jugadores y los catálogos de civilizaciones y facciones.
type PlayerRepo struct{ store *Store }

// NewPlayerRepo crea el repositorio.
func NewPlayerRepo(s *Store) *PlayerRepo { return &PlayerRepo{store: s} }

const playerColumns = `id, username, civilization_id, faction_id, last_seen_at, created_at`

// GetByID recupera un jugador.
func (r *PlayerRepo) GetByID(ctx context.Context, id uuid.UUID) (*player.Player, error) {
	return scanPlayer(r.store.pool.QueryRow(ctx,
		`SELECT `+playerColumns+` FROM players WHERE id = $1`, id))
}

// GetByUsername recupera un jugador por su nombre.
//
// El esquema del juego no guarda credenciales (ADR-018): el nombre es aquí un
// dato visible y la semilla del emplazamiento, no un identificador de acceso.
func (r *PlayerRepo) GetByUsername(ctx context.Context, username string) (*player.Player, error) {
	return scanPlayer(r.store.pool.QueryRow(ctx,
		`SELECT `+playerColumns+` FROM players WHERE username = $1`, username))
}

// Create inserta un jugador. Recibe la DB por parámetro para poder participar en
// la transacción de bootstrap junto a la ciudad y las unidades iniciales.
//
// Devuelve player.ErrUsernameTaken ante cualquier violación de unicidad: la del
// nombre y también la del id. En la fundación idempotente de ADR-018 las dos son
// la misma pregunta —¿existe ya este jugador?— y la resuelve el llamante.
func (r *PlayerRepo) Create(ctx context.Context, db DB, p *player.Player) error {
	_, err := db.Exec(ctx,
		`INSERT INTO players (id, username, civilization_id, faction_id)
		 VALUES ($1, $2, $3, $4)`,
		p.ID, p.Username, p.CivilizationID, p.FactionID)
	if err != nil {
		if IsUniqueViolation(err) {
			return player.ErrUsernameTaken
		}
		return fmt.Errorf("insertar jugador: %w", err)
	}
	return nil
}

// TouchLastSeen actualiza la marca de última actividad.
func (r *PlayerRepo) TouchLastSeen(ctx context.Context, id uuid.UUID) error {
	_, err := r.store.pool.Exec(ctx, `UPDATE players SET last_seen_at = now() WHERE id = $1`, id)
	return err
}

// ListCivilizations carga el catálogo completo de civilizaciones.
func (r *PlayerRepo) ListCivilizations(ctx context.Context) ([]player.Civilization, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id, code, name, traits FROM civilizations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []player.Civilization
	for rows.Next() {
		var c player.Civilization
		var traits []byte
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &traits); err != nil {
			return nil, err
		}
		if len(traits) > 0 {
			if err := json.Unmarshal(traits, &c.Traits); err != nil {
				return nil, fmt.Errorf("traits de la civilización %s malformados: %w", c.Code, err)
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListFactions carga el catálogo de facciones globales.
func (r *PlayerRepo) ListFactions(ctx context.Context) ([]player.Faction, error) {
	rows, err := r.store.pool.Query(ctx, `SELECT id, code, name FROM factions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []player.Faction
	for rows.Next() {
		var f player.Faction
		if err := rows.Scan(&f.ID, &f.Code, &f.Name); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanPlayer(row rowScanner) (*player.Player, error) {
	var p player.Player
	if err := row.Scan(&p.ID, &p.Username, &p.CivilizationID, &p.FactionID, &p.LastSeenAt, &p.CreatedAt); err != nil {
		if err = normalize(err); err == ErrNotFound {
			return nil, player.ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}
