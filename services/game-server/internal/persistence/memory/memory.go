// Package memory implementa el estado caliente EN PROCESO, como alternativa a
// Redis para desarrollo local y tests.
//
// Por qué existe y por qué es legítimo: Redis aporta tres cosas al Game Server
// —presencia con expiración, unicidad de tickets y deduplicación de comandos—
// y las tres son, por definición, estado transitorio y reconstruible (ADR-004).
// Con UN SOLO proceso, un mapa con TTL da exactamente las mismas garantías.
//
// Qué NO da, y por eso está prohibido en producción:
//   - No se comparte entre procesos. Con dos instancias, un ticket podría
//     canjearse una vez en cada una y el anti-replay dejaría de serlo.
//   - No sobrevive a un reinicio. Al arrancar, nadie está presente y todos los
//     requestId vuelven a estar libres.
//
// La configuración rechaza este modo si EO_ENV=production. Ver
// ../../../../docs/decisions/ADR-004-redis-hot-state.md
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
)

// entry es un valor con caducidad.
type entry struct {
	value     string
	expiresAt time.Time
}

// store es un mapa con expiración perezosa.
//
// Perezosa y no con temporizadores: una clave expirada se considera ausente en
// cuanto se consulta, y un barrido periódico libera la memoria. Programar un
// timer por clave costaría más de lo que ahorra.
type store struct {
	mu    sync.Mutex
	items map[string]entry
	clk   clock.Clock
}

func newStore(clk clock.Clock) *store {
	return &store{items: make(map[string]entry), clk: clk}
}

func (s *store) get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.items[key]
	if !ok || !e.expiresAt.After(s.clk.Now()) {
		return "", false
	}
	return e.value, true
}

func (s *store) set(key, value string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[key] = entry{value: value, expiresAt: s.clk.Now().Add(ttl)}
}

// setNX escribe sólo si la clave no existe o ya expiró. Devuelve si escribió.
// Es la primitiva atómica sobre la que se apoyan el anti-replay y la idempotencia.
func (s *store) setNX(key, value string, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	if e, ok := s.items[key]; ok && e.expiresAt.After(now) {
		return false
	}
	s.items[key] = entry{value: value, expiresAt: now.Add(ttl)}
	return true
}

// expire renueva el TTL de una clave viva. Devuelve false si ya no existe.
func (s *store) expire(key string, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	e, ok := s.items[key]
	if !ok || !e.expiresAt.After(now) {
		return false
	}
	e.expiresAt = now.Add(ttl)
	s.items[key] = e
	return true
}

func (s *store) delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
}

// deleteIf borra la clave sólo si su valor coincide. Equivale al script Lua que
// usa la implementación de Redis, y por el mismo motivo: sin la comparación
// atómica, cerrar una pestaña vieja desalojaría a la sesión que acaba de entrar.
func (s *store) deleteIf(key, expected string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.items[key]
	if !ok || !e.expiresAt.After(s.clk.Now()) || e.value != expected {
		return false
	}
	delete(s.items, key)
	return true
}

func (s *store) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	n := 0
	for _, e := range s.items {
		if e.expiresAt.After(now) {
			n++
		}
	}
	return n
}

// sweep elimina las entradas caducadas para que el mapa no crezca sin límite.
func (s *store) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	for k, e := range s.items {
		if !e.expiresAt.After(now) {
			delete(s.items, k)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Almacén compuesto
// ─────────────────────────────────────────────────────────────

// HotState agrupa las tres piezas de estado caliente en un solo objeto, con la
// misma superficie que el paquete redis.
type HotState struct {
	presence *PresenceStore
	tickets  *TicketStore
	idem     *IdempotencyStore

	stopSweeper context.CancelFunc
}

// New construye el estado caliente en memoria y arranca su barrido periódico.
func New(clk clock.Clock, presenceTTL, presenceHeartbeat, ticketTTL, idemTTL time.Duration) *HotState {
	presence := &PresenceStore{store: newStore(clk), ttl: presenceTTL, heartbeat: presenceHeartbeat}
	tickets := &TicketStore{store: newStore(clk), ttl: ticketTTL}
	idem := &IdempotencyStore{store: newStore(clk), ttl: idemTTL}

	ctx, cancel := context.WithCancel(context.Background())
	h := &HotState{presence: presence, tickets: tickets, idem: idem, stopSweeper: cancel}

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				presence.store.sweep()
				tickets.store.sweep()
				idem.store.sweep()
			}
		}
	}()

	return h
}

func (h *HotState) Presence() *PresenceStore       { return h.presence }
func (h *HotState) Tickets() *TicketStore          { return h.tickets }
func (h *HotState) Idempotency() *IdempotencyStore { return h.idem }

// Close detiene el barrido.
func (h *HotState) Close() error {
	h.stopSweeper()
	return nil
}

// Ping siempre responde: no hay red de por medio. Satisface observability.Checker
// para que /ready trate a los dos backends igual.
func (h *HotState) Ping(context.Context) error { return nil }

// ─────────────────────────────────────────────────────────────
// Presencia
// ─────────────────────────────────────────────────────────────

// PresenceStore replica el contrato de redis.PresenceStore.
type PresenceStore struct {
	store     *store
	ttl       time.Duration
	heartbeat time.Duration
}

func (p *PresenceStore) TTL() time.Duration               { return p.ttl }
func (p *PresenceStore) HeartbeatInterval() time.Duration { return p.heartbeat }

func presenceKey(playerID uuid.UUID) string { return "presence:player:" + playerID.String() }

func (p *PresenceStore) MarkOnline(_ context.Context, playerID, sessionID uuid.UUID) error {
	p.store.set(presenceKey(playerID), sessionID.String(), p.ttl)
	return nil
}

func (p *PresenceStore) Heartbeat(_ context.Context, playerID uuid.UUID) (bool, error) {
	return p.store.expire(presenceKey(playerID), p.ttl), nil
}

func (p *PresenceStore) IsOnline(_ context.Context, playerID uuid.UUID) (bool, error) {
	_, ok := p.store.get(presenceKey(playerID))
	return ok, nil
}

func (p *PresenceStore) SessionOf(_ context.Context, playerID uuid.UUID) (uuid.UUID, bool, error) {
	raw, ok := p.store.get(presenceKey(playerID))
	if !ok {
		return uuid.Nil, false, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false, nil
	}
	return id, true, nil
}

func (p *PresenceStore) ClearIfSession(_ context.Context, playerID, sessionID uuid.UUID) (bool, error) {
	return p.store.deleteIf(presenceKey(playerID), sessionID.String()), nil
}

func (p *PresenceStore) CountOnline(context.Context) (int64, error) {
	return int64(p.store.count()), nil
}

// ─────────────────────────────────────────────────────────────
// Tickets
// ─────────────────────────────────────────────────────────────

// TicketStore replica el contrato de redis.TicketStore.
type TicketStore struct {
	store *store
	ttl   time.Duration
}

func (t *TicketStore) Consume(_ context.Context, jti string) (bool, error) {
	return t.store.setNX("ticket:jti:"+jti, "1", t.ttl), nil
}

// ─────────────────────────────────────────────────────────────
// Idempotencia
// ─────────────────────────────────────────────────────────────

// IdempotencyStore replica el contrato de redis.IdempotencyStore.
type IdempotencyStore struct {
	store *store
	ttl   time.Duration
}

func idemKey(playerID uuid.UUID, requestID string) string {
	return "idem:" + playerID.String() + ":" + requestID
}

func (i *IdempotencyStore) Claim(_ context.Context, playerID uuid.UUID, requestID string) (bool, string, error) {
	key := idemKey(playerID, requestID)
	if i.store.setNX(key, "pending", i.ttl) {
		return true, "", nil
	}
	prev, ok := i.store.get(key)
	if !ok {
		// Expiró entre la reserva y la lectura: se trata como primera vez.
		return true, "", nil
	}
	return false, prev, nil
}

func (i *IdempotencyStore) Complete(_ context.Context, playerID uuid.UUID, requestID, response string) error {
	i.store.set(idemKey(playerID, requestID), response, i.ttl)
	return nil
}

func (i *IdempotencyStore) Release(_ context.Context, playerID uuid.UUID, requestID string) error {
	i.store.delete(idemKey(playerID, requestID))
	return nil
}
