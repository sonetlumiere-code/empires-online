package websocket_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El registro por WebSocket (SPEC-EVENT-LOG §6 y §9 «contract / e2e», ADR-028 §1
// y §6), con un registro falso: lo que se prueba aquí es la conexión. La
// consulta y el cursor contra PostgreSQL están en eventlog_integration_test.go.

type registroFalso struct {
	mu       sync.Mutex
	page     protocol.EventLogPagePayload
	markable int64
	err      error
	pedidos  []pedidoDePagina
	marcas   []int64
	quien    []uuid.UUID
}

type pedidoDePagina struct {
	player uuid.UUID
	before int64
}

func (r *registroFalso) Page(_ context.Context, playerID uuid.UUID, beforeID int64) (protocol.EventLogPagePayload, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pedidos = append(r.pedidos, pedidoDePagina{playerID, beforeID})
	return r.page, r.markable, r.err
}

func (r *registroFalso) MarkSeen(_ context.Context, playerID uuid.UUID, upToID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.marcas = append(r.marcas, upToID)
	r.quien = append(r.quien, playerID)
	return r.err
}

func (r *registroFalso) estado() ([]pedidoDePagina, []int64, []uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pedidoDePagina(nil), r.pedidos...), append([]int64(nil), r.marcas...), append([]uuid.UUID(nil), r.quien...)
}

// sincronizar espera a que la conexión haya procesado todo lo anterior: la
// goroutine de lectura atiende los mensajes en orden, y event_log.seen no
// responde cuando todo va bien.
func sincronizar(c *conn) {
	c.send(protocol.TypeSessionPing, map[string]any{"clientTimeMs": 1})
	c.await(protocol.TypeSessionPong, 3*time.Second)
}

func paginaDePrueba() protocol.EventLogPagePayload {
	nombre, ciudad, amount, x, y := "Villa Norte", int64(7), int64(85), int32(451), int32(179)
	next := int64(12)
	return protocol.EventLogPagePayload{
		Entries: []protocol.EventLogEntry{{
			ID: 41, Kind: "PILLAGED", FirstAtMs: 1_800_000_000_000, LastAtMs: 1_800_000_060_000, Count: 17,
			OtherPlayerID: uuid.NewString(), OtherCityID: &ciudad, OtherCityName: &nombre, Amount: &amount, X: &x, Y: &y,
		}},
		SeenUpToID: 3, NextBeforeID: &next,
	}
}

// INV-SEC-009: la página es siempre la del jugador de la sesión, viaja sólo a
// quien la pidió y cumple el esquema (el arnés valida cada mensaje).
func TestElRegistroSeSirveAlJugadorDeLaSesion(t *testing.T) {
	env := newE2E(t)
	falso := &registroFalso{page: paginaDePrueba(), markable: 40}
	env.ws.SetEventLog(falso)
	c := env.conectar()

	id := c.send(protocol.TypeEventLogFetch, map[string]any{})
	msg := c.await(protocol.TypeEventLogPage, 3*time.Second)
	assert.Equal(t, id, msg.RequestID)
	page := decode[protocol.EventLogPagePayload](t, msg.Payload)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "PILLAGED", page.Entries[0].Kind)
	assert.Equal(t, int64(3), page.SeenUpToID)
	require.NotNil(t, page.NextBeforeID)

	c.send(protocol.TypeEventLogFetch, map[string]any{"beforeId": 12})
	c.await(protocol.TypeEventLogPage, 3*time.Second)
	pedidos, _, _ := falso.estado()
	require.Len(t, pedidos, 2)
	for _, p := range pedidos {
		assert.Equal(t, env.playerID, p.player)
	}
	assert.Equal(t, int64(0), pedidos[0].before)
	assert.Equal(t, int64(12), pedidos[1].before)
}

// RN-LOG-004 e INV-PERSIST-006: sin página servida, marcar no hace nada; después,
// la marca no pasa del tope de la última página; y el jugador es el de la sesión.
func TestLoVistoNoPasaDelTopeDeLaUltimaPagina(t *testing.T) {
	env := newE2E(t)
	falso := &registroFalso{page: paginaDePrueba(), markable: 40}
	env.ws.SetEventLog(falso)
	c := env.conectar()

	c.send(protocol.TypeEventLogSeen, map[string]any{"upToId": 99})
	sincronizar(c)
	_, marcas, _ := falso.estado()
	assert.Empty(t, marcas, "sin página servida no se marca nada")

	c.send(protocol.TypeEventLogFetch, map[string]any{})
	c.await(protocol.TypeEventLogPage, 3*time.Second)
	c.send(protocol.TypeEventLogSeen, map[string]any{"upToId": 99})
	c.send(protocol.TypeEventLogSeen, map[string]any{"upToId": 10})
	sincronizar(c)
	_, marcas, quien := falso.estado()
	assert.Equal(t, []int64{40, 10}, marcas, "99 se acota al tope; 10 llega tal cual y es la base quien no retrocede")
	for _, q := range quien {
		assert.Equal(t, env.playerID, q)
	}
}

// RN-LOG-006: si la base falla, el comando responde INTERNAL_ERROR y la sesión
// sigue; sin registro montado, NOT_IMPLEMENTED.
func TestElRegistroSinBaseNoTumbaLaSesion(t *testing.T) {
	env := newE2E(t)
	falso := &registroFalso{err: errors.New("la base no responde")}
	env.ws.SetEventLog(falso)
	c := env.conectar()

	c.send(protocol.TypeEventLogFetch, map[string]any{})
	e := decode[protocol.SystemErrorPayload](t, c.await(protocol.TypeSystemError, 3*time.Second).Payload)
	assert.Equal(t, protocol.CodeInternalError, e.Code)
	sincronizar(c)

	sin := newE2E(t)
	c2 := sin.conectar()
	c2.send(protocol.TypeEventLogFetch, map[string]any{})
	e = decode[protocol.SystemErrorPayload](t, c2.await(protocol.TypeSystemError, 3*time.Second).Payload)
	assert.Equal(t, protocol.CodeNotImplemented, e.Code)
}

// §5: un id que no es positivo, o un campo de más (como un playerId que elija de
// quién leer), es INVALID_MESSAGE y no llega al registro.
func TestElRegistroRechazaLoQueElEsquemaRechaza(t *testing.T) {
	env := newE2E(t)
	falso := &registroFalso{page: paginaDePrueba(), markable: 40}
	env.ws.SetEventLog(falso)
	c := env.conectar()

	for _, caso := range []struct {
		tipo    string
		payload map[string]any
	}{
		{protocol.TypeEventLogFetch, map[string]any{"beforeId": 0}},
		{protocol.TypeEventLogFetch, map[string]any{"playerId": uuid.NewString()}},
		{protocol.TypeEventLogSeen, map[string]any{"upToId": 0}},
		{protocol.TypeEventLogSeen, map[string]any{"upToId": 5, "playerId": uuid.NewString()}},
	} {
		c.send(caso.tipo, caso.payload)
		e := decode[protocol.SystemErrorPayload](t, c.await(protocol.TypeSystemError, 3*time.Second).Payload)
		assert.Equal(t, protocol.CodeInvalidMessage, e.Code, "%s %v", caso.tipo, caso.payload)
	}
	pedidos, marcas, _ := falso.estado()
	assert.Empty(t, pedidos)
	assert.Empty(t, marcas)
}
