package main

import (
	"context"
	"log/slog"

	"github.com/empires-online/empires-online/services/game-server/internal/auth"
	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/config"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/memory"
	redisstore "github.com/empires-online/empires-online/services/game-server/internal/persistence/redis"
	ws "github.com/empires-online/empires-online/services/game-server/internal/websocket"
)

// hotState es el estado caliente del servidor: presencia, unicidad de tickets e
// idempotencia de comandos.
//
// Existen dos implementaciones y el resto del programa no distingue cuál usa:
//
//	Redis      — obligatoria en producción; compartida entre instancias.
//	En proceso — sólo desarrollo y tests; mismas garantías con UN solo proceso.
//
// Ver ../../../../docs/decisions/ADR-004-redis-hot-state.md
type hotState interface {
	Presence() ws.PresenceTracker
	Tickets() auth.TicketConsumer
	Idempotency() ws.Deduper
	Ping(ctx context.Context) error
	Close() error
}

// newHotState elige la implementación según la configuración.
func newHotState(ctx context.Context, cfg config.Config, log *slog.Logger) (hotState, error) {
	if cfg.UsesInProcessHotState() {
		// El aviso es deliberadamente ruidoso: alguien que vea esta línea en un
		// entorno compartido tiene que saber que no hay Redis detrás.
		log.Warn("estado caliente EN PROCESO (sin Redis): válido sólo para desarrollo de una sola instancia",
			"presencia", "se pierde al reiniciar",
			"anti_replay", "no se comparte entre procesos",
			"hint", "define EO_REDIS_URL para usar Redis")

		return &memoryHotState{
			HotState: memory.New(
				clock.NewSystemClock(),
				cfg.PresenceTTL, cfg.PresenceHeartbeat,
				ticketReplayTTL, idempotencyTTL,
			),
		}, nil
	}

	client, err := redisstore.New(ctx, cfg.RedisURL)
	if err != nil {
		return nil, err
	}
	log.Info("estado caliente en Redis")

	return &redisHotState{
		client:   client,
		presence: redisstore.NewPresenceStore(client, cfg.PresenceTTL, cfg.PresenceHeartbeat),
		tickets:  redisstore.NewTicketStore(client, ticketReplayTTL),
		idem:     redisstore.NewIdempotencyStore(client, idempotencyTTL),
	}, nil
}

// ─────────────────────────────────────────────────────────────

type redisHotState struct {
	client   *redisstore.Client
	presence *redisstore.PresenceStore
	tickets  *redisstore.TicketStore
	idem     *redisstore.IdempotencyStore
}

func (r *redisHotState) Presence() ws.PresenceTracker   { return r.presence }
func (r *redisHotState) Tickets() auth.TicketConsumer   { return r.tickets }
func (r *redisHotState) Idempotency() ws.Deduper        { return r.idem }
func (r *redisHotState) Ping(ctx context.Context) error { return r.client.Ping(ctx) }
func (r *redisHotState) Close() error                   { return r.client.Close() }

// ─────────────────────────────────────────────────────────────

type memoryHotState struct{ *memory.HotState }

func (m *memoryHotState) Presence() ws.PresenceTracker { return m.HotState.Presence() }
func (m *memoryHotState) Tickets() auth.TicketConsumer { return m.HotState.Tickets() }
func (m *memoryHotState) Idempotency() ws.Deduper      { return m.HotState.Idempotency() }
