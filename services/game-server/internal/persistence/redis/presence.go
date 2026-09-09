package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// PresenceStore mantiene la presencia de los jugadores con expiración automática.
//
// Por qué con TTL y latido, y no con un simple booleano: un proceso que muere de
// golpe no ejecuta ningún "marcar como offline". Con TTL, la ausencia de latidos
// hace desaparecer la clave sola, y eso es exactamente lo que significa "este
// jugador ya no está". Ver ../../../../docs/specs/presence.md
type PresenceStore struct {
	client    *Client
	ttl       time.Duration
	heartbeat time.Duration
}

// NewPresenceStore crea el almacén de presencia.
func NewPresenceStore(c *Client, ttl, heartbeat time.Duration) *PresenceStore {
	return &PresenceStore{client: c, ttl: ttl, heartbeat: heartbeat}
}

// TTL es la vida de una clave de presencia sin latidos.
func (p *PresenceStore) TTL() time.Duration { return p.ttl }

// HeartbeatInterval es la cadencia con la que debe renovarse la presencia.
func (p *PresenceStore) HeartbeatInterval() time.Duration { return p.heartbeat }

func presenceKey(playerID uuid.UUID) string { return "presence:player:" + playerID.String() }

// MarkOnline registra al jugador como presente y renueva su TTL.
//
// Guarda el identificador de sesión: si el jugador abre una segunda pestaña, la
// clave apunta a la sesión más reciente, y sólo esa puede después retirarla.
func (p *PresenceStore) MarkOnline(ctx context.Context, playerID uuid.UUID, sessionID uuid.UUID) error {
	if err := p.client.rdb.Set(ctx, presenceKey(playerID), sessionID.String(), p.ttl).Err(); err != nil {
		return fmt.Errorf("marcar presencia de %s: %w", playerID, err)
	}
	return nil
}

// Heartbeat renueva el TTL sin reescribir el valor.
//
// Devuelve false si la clave ya había expirado: eso significa que el servidor ya
// consideró offline al jugador y la sesión debe re-registrarse.
func (p *PresenceStore) Heartbeat(ctx context.Context, playerID uuid.UUID) (bool, error) {
	ok, err := p.client.rdb.Expire(ctx, presenceKey(playerID), p.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("renovar presencia de %s: %w", playerID, err)
	}
	return ok, nil
}

// IsOnline indica si el jugador tiene presencia vigente.
func (p *PresenceStore) IsOnline(ctx context.Context, playerID uuid.UUID) (bool, error) {
	n, err := p.client.rdb.Exists(ctx, presenceKey(playerID)).Result()
	if err != nil {
		return false, fmt.Errorf("consultar presencia de %s: %w", playerID, err)
	}
	return n > 0, nil
}

// SessionOf devuelve la sesión que ostenta la presencia, si la hay.
func (p *PresenceStore) SessionOf(ctx context.Context, playerID uuid.UUID) (uuid.UUID, bool, error) {
	val, err := p.client.rdb.Get(ctx, presenceKey(playerID)).Result()
	if errors.Is(err, goredis.Nil) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	id, err := uuid.Parse(val)
	if err != nil {
		return uuid.Nil, false, nil
	}
	return id, true, nil
}

// clearIfSessionScript retira la presencia SÓLO si la ostenta la sesión indicada.
//
// Sin esta comprobación atómica, cerrar una pestaña vieja marcaría offline a un
// jugador que acaba de reconectar desde otra: una condición de carrera real y
// fácil de provocar.
var clearIfSessionScript = goredis.NewScript(`
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("DEL", KEYS[1])
	end
	return 0
`)

// ClearIfSession retira la presencia si pertenece a esa sesión.
func (p *PresenceStore) ClearIfSession(ctx context.Context, playerID, sessionID uuid.UUID) (bool, error) {
	res, err := clearIfSessionScript.Run(ctx, p.client.rdb, []string{presenceKey(playerID)}, sessionID.String()).Int64()
	if err != nil {
		return false, fmt.Errorf("retirar presencia de %s: %w", playerID, err)
	}
	return res == 1, nil
}

// CountOnline cuenta los jugadores presentes. Es una métrica, no una consulta
// caliente: usa SCAN para no bloquear Redis.
func (p *PresenceStore) CountOnline(ctx context.Context) (int64, error) {
	var count int64
	iter := p.client.rdb.Scan(ctx, 0, "presence:player:*", 256).Iterator()
	for iter.Next(ctx) {
		count++
	}
	return count, iter.Err()
}
