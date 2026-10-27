package websocket_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	gws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/auth"
	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/garrison"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loop"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
	ws "github.com/empires-online/empires-online/services/game-server/internal/websocket"
)

// Test end-to-end del TRANSPORTE completo: HTTP upgrade, handshake con ticket,
// snapshot, comando de movimiento, deltas, desconexión y reconexión.
//
// Usa el servidor WebSocket, el hub, el game loop y la simulación REALES.
// Sólo se sustituyen PostgreSQL y Redis por implementaciones en memoria, porque
// lo que aquí se prueba es el camino de red, no la durabilidad — de eso se
// encargan los tests de integración.
//
// Es el test que demuestra el flujo del vertical slice:
//
//	login → mundo → ciudad → aldeanos → seleccionar → mover → A* → game loop
//	→ sincronización → desconectar → el movimiento continúa → reconectar
//	→ el estado correcto se restaura
//
// Ver ../../../../docs/product/mvp-scope.md

const (
	testSecret = "un-secreto-de-pruebas-suficientemente-largo"
	testEpoch  = int64(1_757_376_000_000)
)

// ─────────────────────────────────────────────────────────────
// Dobles en memoria
// ─────────────────────────────────────────────────────────────

type memoryTickets struct {
	mu   sync.Mutex
	seen map[string]bool
}

func newMemoryTickets() *memoryTickets { return &memoryTickets{seen: map[string]bool{}} }

func (m *memoryTickets) Consume(_ context.Context, jti string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen[jti] {
		return false, nil
	}
	m.seen[jti] = true
	return true, nil
}

type memoryPresence struct {
	mu     sync.Mutex
	online map[uuid.UUID]uuid.UUID
}

func newMemoryPresence() *memoryPresence {
	return &memoryPresence{online: map[uuid.UUID]uuid.UUID{}}
}

func (m *memoryPresence) MarkOnline(_ context.Context, playerID, sessionID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.online[playerID] = sessionID
	return nil
}

func (m *memoryPresence) Heartbeat(_ context.Context, playerID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.online[playerID]
	return ok, nil
}

func (m *memoryPresence) ClearIfSession(_ context.Context, playerID, sessionID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.online[playerID] == sessionID {
		delete(m.online, playerID)
		return true, nil
	}
	return false, nil
}

type memoryDeduper struct {
	mu   sync.Mutex
	seen map[string]string
}

func newMemoryDeduper() *memoryDeduper { return &memoryDeduper{seen: map[string]string{}} }

func (m *memoryDeduper) Claim(_ context.Context, playerID uuid.UUID, requestID string) (bool, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := playerID.String() + ":" + requestID
	if prev, ok := m.seen[key]; ok {
		return false, prev, nil
	}
	m.seen[key] = "pending"
	return true, "", nil
}

func (m *memoryDeduper) Complete(_ context.Context, playerID uuid.UUID, requestID, response string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen[playerID.String()+":"+requestID] = response
	return nil
}

func (m *memoryDeduper) Release(_ context.Context, playerID uuid.UUID, requestID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.seen, playerID.String()+":"+requestID)
	return nil
}

func (m *memoryDeduper) stored(playerID uuid.UUID, requestID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seen[playerID.String()+":"+requestID]
}

type memoryRepos struct{ mu sync.Mutex }

func (r *memoryRepos) PersistMovementStart(context.Context, movement.Movement, world.Tile) error {
	return nil
}
func (r *memoryRepos) PersistMovementFinish(context.Context, simulation.MovementFinish) error {
	return nil
}
func (r *memoryRepos) PersistUnitPositions(context.Context, []*unit.Unit) error { return nil }

// memoryGarrisons hace de postgres.GarrisonRepo sin base: acepta toda entrada
// en ciudad propia y deja la copia como la dejaría la transacción.
type memoryGarrisons struct{}

func (memoryGarrisons) Enter(_ context.Context, u *unit.Unit, c *city.City, requester uuid.UUID, _ uint64) (int64, error) {
	if c.OwnerPlayerID != requester {
		return 0, garrison.ErrTreatyRequired
	}
	u.Status = unit.StatusGarrisoned
	return 0, nil
}

func (memoryGarrisons) Leave(_ context.Context, u *unit.Unit, x, y, _, _ int32, _ uint64) error {
	u.X, u.Y, u.Status = x, y, unit.StatusIdle
	return nil
}
func (r *memoryRepos) PersistCityPresence(context.Context, int64, city.PresenceState, time.Time) error {
	return nil
}

// ─────────────────────────────────────────────────────────────
// Banco de pruebas
// ─────────────────────────────────────────────────────────────

type e2e struct {
	t        *testing.T
	server   *httptest.Server
	loop     *loop.Loop
	clk      *clock.FakeClock
	issuer   *auth.Issuer
	state    *simulation.State
	playerID uuid.UUID
	unitIDs  []int64
	stop     func()
	// dedupe, commands e inbox se exponen para los tests de idempotencia y de
	// saturación de la cola.
	dedupe   *memoryDeduper
	commands chan simulation.Command
	inbox    *loop.Inbox
	sim      *simulation.Simulation
	// logs guarda todo lo que el servidor registra, en debug, para los tests
	// que comprueban qué NO se registra (INV-PLAYER-007).
	logs *safeBuffer
	// metrics son las del servidor WebSocket, en un registro propio del test.
	metrics *observability.Metrics
}

// safeBuffer es un bytes.Buffer que admite escrituras desde varias goroutines:
// la sesión, el hub y el loop registran a la vez.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newE2E(t *testing.T) *e2e {
	t.Helper()
	return newE2EConTerreno(t, nil)
}

// newE2EConTerreno permite pintar terreno sobre la hierba antes de construir el
// mundo de 128 × 128 (slice fila-mayor).
func newE2EConTerreno(t *testing.T, pintar func(terrain []byte)) *e2e {
	t.Helper()
	return newE2ECon(t, pintar, nil)
}

// newE2ECon es el constructor completo: `ajustar`, si no es nil, retoca la
// configuración del servidor WebSocket antes de construirlo.
func newE2ECon(t *testing.T, pintar func(terrain []byte), ajustar func(*ws.Config)) *e2e {
	t.Helper()
	return newE2EDeLado(t, 128, pintar, ajustar)
}

// newE2EDeLado permite un mundo mayor: con 128 × 128 y chunks de 32, el radio de
// interés de 2 chunks ya cubre el mundo entero, y mover la vista no cambiaría
// nada.
func newE2EDeLado(t *testing.T, lado int32, pintar func(terrain []byte), ajustar func(*ws.Config)) *e2e {
	t.Helper()

	terrain := make([]byte, int(lado)*int(lado))
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	if pintar != nil {
		pintar(terrain)
	}
	gameWorld, err := world.New(lado, lado, 32, 1, terrain)
	require.NoError(t, err)

	clk := clock.NewFakeClock(testEpoch)
	logs := &safeBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	state := simulation.NewState(gameWorld)
	hub := ws.NewHub(nil, log)
	commands := make(chan simulation.Command, 256)
	inbox := loop.NewInbox(256, nil, log)

	sim := simulation.New(state, simulation.Deps{
		Clock:              clk,
		Pathfinder:         pathfinding.NewAStar(20000, 256),
		Broadcaster:        hub,
		Persister:          syncPersister{},
		Repos:              &memoryRepos{},
		Garrisons:          memoryGarrisons{},
		Inbox:              inbox.Offer,
		Log:                log,
		ProtectionCooldown: 300 * time.Second,
		DisconnectGrace:    30 * time.Second,
	})

	playerID := uuid.New()
	cityID := int64(1)
	state.AddCity(&city.City{
		ID: cityID, OwnerPlayerID: playerID, Name: "Testópolis",
		CenterX: 40, CenterY: 40, Era: city.EraStone,
		Population: 3, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	})

	var unitIDs []int64
	for i, spawn := range []world.Tile{{X: 42, Y: 40}, {X: 38, Y: 40}, {X: 40, Y: 42}} {
		u := &unit.Unit{
			ID: int64(i + 1), PlayerID: playerID, CityID: &cityID,
			Type: unit.TypeVillager, X: spawn.X, Y: spawn.Y,
			HP: 40, MaxHP: 40, Status: unit.StatusIdle,
		}
		state.AddUnit(u)
		unitIDs = append(unitIDs, u.ID)
	}

	dedupe := newMemoryDeduper()
	gameLoop := loop.New(sim, clk, commands, inbox, loop.Config{
		TickDuration:       100 * time.Millisecond,
		FlushIntervalTicks: 50,
		MaxCommandsPerTick: 256,
		EpochMs:            testEpoch,
	}, nil, nil, log)

	authenticator := auth.NewAuthenticator(auth.NewVerifier(testSecret, clk), newMemoryTickets())

	wsCfg := ws.Config{
		MaxMessageBytes:      16384,
		RateLimitPerSec:      20,
		RateLimitBurst:       40,
		HandshakeTimeout:     5 * time.Second,
		PingInterval:         15 * time.Second,
		ReadTimeout:          45 * time.Second,
		WriteTimeout:         10 * time.Second,
		OutboundQueue:        256,
		InterestRadiusChunks: 2,
		TickDurationMs:       100,
		HeartbeatIntervalMs:  10_000,
		WorldWidth:           lado,
		WorldHeight:          lado,
		ChunkSize:            32,
	}
	if ajustar != nil {
		ajustar(&wsCfg)
	}
	metrics := observability.NewMetrics()
	wsServer := ws.NewServer(wsCfg, hub, authenticator, newMemoryPresence(), dedupe, commands, inbox.Offer, metrics, log)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsServer.Handler(context.Background()))
	httpServer := httptest.NewServer(mux)

	// El loop se conduce a mano desde el test: nada depende de temporizadores
	// reales ni de la velocidad de la máquina.
	env := &e2e{
		t: t, server: httpServer, loop: gameLoop, clk: clk,
		issuer:   auth.NewIssuer(testSecret, 60*time.Second, clk),
		state:    state,
		playerID: playerID, unitIDs: unitIDs,
		dedupe: dedupe, commands: commands, inbox: inbox, sim: sim,
		logs: logs, metrics: metrics,
	}
	env.stop = func() { httpServer.Close() }
	t.Cleanup(env.stop)
	return env
}

type syncPersister struct{}

func (syncPersister) Submit(job simulation.Job) { _ = job.Run(context.Background()) }
func (syncPersister) Depth() int                { return 0 }
func (syncPersister) Partitions() int           { return 1 }

// tick ejecuta N ticks del loop avanzando el reloj falso.
func (e *e2e) tick(n int) {
	for i := 0; i < n; i++ {
		e.clk.AdvanceMs(100)
		e.loop.Step(e.clk.NowMs())
	}
}

// pump ejecuta ticks sin avanzar el reloj, para que el loop consuma los
// comandos que acaban de encolarse.
func (e *e2e) pump() {
	for i := 0; i < 20; i++ {
		e.loop.Step(e.clk.NowMs())
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *e2e) wsURL() string {
	return "ws" + strings.TrimPrefix(e.server.URL, "http") + "/ws"
}

// conn es un cliente WebSocket de pruebas.
type conn struct {
	t  *testing.T
	c  *gws.Conn
	mu sync.Mutex
	// recibidos guarda todo lo que ha leído await, también lo que descartó.
	recibidos []envelope
}

// historial devuelve, en orden de llegada, todo lo leído hasta ahora.
func (c *conn) historial() []envelope { return append([]envelope(nil), c.recibidos...) }

func (e *e2e) dial() *conn {
	e.t.Helper()
	c, _, err := gws.DefaultDialer.Dial(e.wsURL(), nil)
	require.NoError(e.t, err)
	e.t.Cleanup(func() { _ = c.Close() })
	return &conn{t: e.t, c: c}
}

func (c *conn) send(msgType string, payload any) string {
	c.t.Helper()
	requestID := uuid.NewString()
	data, err := json.Marshal(map[string]any{
		"v": 1, "type": msgType, "requestId": requestID, "payload": payload,
	})
	require.NoError(c.t, err)

	c.mu.Lock()
	defer c.mu.Unlock()
	require.NoError(c.t, c.c.WriteMessage(gws.TextMessage, data))
	return requestID
}

type envelope struct {
	V         int             `json:"v"`
	Type      string          `json:"type"`
	Seq       uint64          `json:"seq"`
	TS        int64           `json:"ts"`
	RequestID string          `json:"requestId"`
	Payload   json.RawMessage `json:"payload"`
}

// await espera un mensaje de un tipo concreto, descartando los demás.
//
// Distingue un timeout de lectura (se reintenta) de un error real de la conexión
// (se aborta): insistir sobre un socket ya roto hace que gorilla entre en pánico,
// y el mensaje de ese pánico oculta la causa verdadera del fallo.
func (c *conn) await(msgType string, timeout time.Duration) envelope {
	c.t.Helper()
	var seen []string

	// UNA sola fecha límite para toda la espera, no un sondeo con timeouts
	// cortos: gorilla marca la conexión como rota ante CUALQUIER error de
	// lectura —incluido un timeout— y entra en pánico en la lectura siguiente.
	require.NoError(c.t, c.c.SetReadDeadline(time.Now().Add(timeout)))

	for {
		data, err := c.read()
		if err != nil {
			c.t.Fatalf("esperando %q: %v (recibidos: %v)", msgType, err, seen)
		}
		var env envelope
		require.NoError(c.t, json.Unmarshal(data, &env))
		c.recibidos = append(c.recibidos, env)
		seen = append(seen, env.Type)
		if env.Type == msgType {
			return env
		}
	}
}

func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// ─────────────────────────────────────────────────────────────
// El flujo completo
// ─────────────────────────────────────────────────────────────

func TestVerticalSliceEndToEnd(t *testing.T) {
	env := newE2E(t)

	// ── 1. Conectar y autenticarse ──
	ticket, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)

	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})

	welcome := decode[protocol.SessionWelcomePayload](t, c.await(protocol.TypeSessionWelcome, 3*time.Second).Payload)
	require.Equal(t, env.playerID.String(), welcome.PlayerID)
	require.EqualValues(t, 128, welcome.World.Width)
	require.EqualValues(t, 32, welcome.World.ChunkSize)
	require.EqualValues(t, 100, welcome.TickDurationMs)

	// ── 2. Snapshot inicial centrado en la ciudad ──
	env.pump()
	snapshot := decode[protocol.WorldSnapshotPayload](t, c.await(protocol.TypeWorldSnapshot, 3*time.Second).Payload)

	require.Len(t, snapshot.Units, 3, "los tres aldeanos iniciales")
	require.Len(t, snapshot.Cities, 1)
	require.NotEmpty(t, snapshot.Chunks, "la sesión queda suscrita a su área de interés")
	require.NotEmpty(t, snapshot.Terrain, "el terreno llega la primera vez")
	require.Equal(t, "Testópolis", snapshot.Cities[0].Name)

	unitID := env.unitIDs[0] // en (42,40)

	// ── 3. Ordenar un movimiento ──
	// 6 tiles de hierba en ortogonal: 6 × 600 ms = 3600 ms exactos.
	c.send(protocol.TypeUnitMove, map[string]any{
		"unitId": unitID,
		"target": map[string]int{"x": 48, "y": 40},
	})
	env.pump()

	accepted := decode[protocol.UnitMoveAcceptedPayload](t, c.await(protocol.TypeUnitMoveAccepted, 3*time.Second).Payload)
	require.Equal(t, unitID, accepted.UnitID)

	started := decode[protocol.UnitMovementStartedPayload](t, c.await(protocol.TypeUnitMovementStarted, 3*time.Second).Payload)
	require.Equal(t, unitID, started.UnitID)
	require.Len(t, started.Movement.Path, 7, "origen más seis pasos")
	require.EqualValues(t, 0, started.Movement.Path[0].TMs, "el origen siempre lleva tMs = 0")
	require.EqualValues(t, 3600, started.Movement.Path[6].TMs)
	require.Equal(t, protocol.Tile{X: 48, Y: 40}, started.Movement.Target)
	// La polilínea COMPLETA viaja al cliente: con ella interpola sin sondear.
	require.Equal(t, started.Movement.StartTimeMs+3600, started.Movement.ArrivalTimeMs)

	// ── 4. La simulación avanza y llegan deltas ──
	env.tick(6) // 600 ms: un tile
	update := decode[protocol.EntityUpdatePayload](t, c.await(protocol.TypeEntityUpdate, 3*time.Second).Payload)
	require.Equal(t, unitID, update.ID)
	require.NotNil(t, update.X)
	require.EqualValues(t, 43, *update.X, "a los 600 ms la unidad avanzó exactamente un tile")

	// ── 5. El jugador se desconecta a mitad de camino ──
	require.NoError(t, c.c.Close())
	time.Sleep(50 * time.Millisecond)
	env.pump()

	// ── 6. El mundo NO se detiene ──
	env.tick(30) // 3000 ms más: el recorrido se completa mientras nadie mira

	// ── 7. Reconexión ──
	ticket2, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)

	c2 := env.dial()
	c2.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket2})
	c2.await(protocol.TypeSessionWelcome, 3*time.Second)
	env.pump()

	// ── 8. El snapshot refleja el estado autoritativo ──
	snapshot2 := decode[protocol.WorldSnapshotPayload](t, c2.await(protocol.TypeWorldSnapshot, 3*time.Second).Payload)

	var moved *protocol.UnitView
	for i := range snapshot2.Units {
		if snapshot2.Units[i].ID == unitID {
			moved = &snapshot2.Units[i]
		}
	}
	require.NotNil(t, moved, "la unidad debe seguir en el mundo")
	require.EqualValues(t, 48, moved.X, "la unidad llegó a su destino con el jugador desconectado")
	require.EqualValues(t, 40, moved.Y)
	require.Equal(t, string(unit.StatusIdle), moved.Status)
	require.Nil(t, moved.Movement, "el movimiento terminó")
}

// ─────────────────────────────────────────────────────────────
// Seguridad del transporte
// ─────────────────────────────────────────────────────────────

// INV-SEC-002: ninguna conexión ejecuta comandos antes de autenticarse.
func TestNoSeAceptanComandosSinAutenticar(t *testing.T) {
	env := newE2E(t)
	c := env.dial()

	// Un comando como PRIMER mensaje, sin handshake previo.
	c.send(protocol.TypeUnitMove, map[string]any{
		"unitId": env.unitIDs[0],
		"target": map[string]int{"x": 50, "y": 50},
	})

	_ = c.c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err := c.read()
	require.Error(t, err, "el servidor debe cerrar la conexión")

	var closeErr *gws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, protocol.CloseUnauthenticated, closeErr.Code)
}

func TestTicketInvalidoCierraLaConexion(t *testing.T) {
	env := newE2E(t)
	c := env.dial()

	c.send(protocol.TypeSessionHello, map[string]any{"ticket": "esto-no-es-un-jwt"})

	_ = c.c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err := c.read()
	require.Error(t, err)

	var closeErr *gws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, protocol.CloseUnauthenticated, closeErr.Code)
}

// INV-SEC-003: un ticket sólo se canjea una vez.
func TestTicketNoSePuedeReutilizarEnOtraConexion(t *testing.T) {
	env := newE2E(t)

	ticket, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)

	c1 := env.dial()
	c1.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c1.await(protocol.TypeSessionWelcome, 3*time.Second)

	// El MISMO ticket en una segunda conexión.
	c2 := env.dial()
	c2.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})

	_ = c2.c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = c2.read()
	require.Error(t, err, "el ticket reutilizado debe rechazarse")

	// ADR-019: con el mismo cierre que cualquier otro ticket rechazado, y sin
	// mensaje previo (la primera lectura ya es el cierre).
	var closeErr *gws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, protocol.CloseUnauthenticated, closeErr.Code)
}

// INV-PLAYER-001: sólo puedes comandar lo que es tuyo.
func TestNoSePuedeComandarUnaUnidadAjena(t *testing.T) {
	env := newE2E(t)

	intruso := uuid.New()
	ticket, err := env.issuer.Issue(intruso)
	require.NoError(t, err)

	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
	env.pump()

	c.send(protocol.TypeUnitMove, map[string]any{
		"unitId": env.unitIDs[0],
		"target": map[string]int{"x": 50, "y": 50},
	})
	env.pump()

	rejected := decode[protocol.UnitMoveRejectedPayload](t, c.await(protocol.TypeUnitMoveRejected, 3*time.Second).Payload)
	require.Equal(t, protocol.CodeUnitNotOwned, rejected.Code)
}

func TestMensajeMalformadoNoTumbaLaSesion(t *testing.T) {
	env := newE2E(t)

	ticket, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)

	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
	env.pump()
	c.await(protocol.TypeWorldSnapshot, 3*time.Second)

	require.NoError(t, c.c.WriteMessage(gws.TextMessage, []byte("{esto no es json")))

	systemErr := decode[protocol.SystemErrorPayload](t, c.await(protocol.TypeSystemError, 3*time.Second).Payload)
	require.Equal(t, protocol.CodeInvalidMessage, systemErr.Code)

	// Y la sesión sigue viva: se responde al ping.
	c.send(protocol.TypeSessionPing, map[string]any{"clientTimeMs": testEpoch})
	c.await(protocol.TypeSessionPong, 3*time.Second)
}

func TestTipoDeMensajeDesconocidoSeRechaza(t *testing.T) {
	env := newE2E(t)

	ticket, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)

	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
	env.pump()
	c.await(protocol.TypeWorldSnapshot, 3*time.Second)

	c.send("unit.teleport", map[string]any{"unitId": 1})

	systemErr := decode[protocol.SystemErrorPayload](t, c.await(protocol.TypeSystemError, 3*time.Second).Payload)
	require.Equal(t, protocol.CodeInvalidMessage, systemErr.Code)
}

func TestVersionNoSoportada(t *testing.T) {
	env := newE2E(t)

	ticket, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)

	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
	env.pump()
	c.await(protocol.TypeWorldSnapshot, 3*time.Second)

	data, _ := json.Marshal(map[string]any{
		"v": 99, "type": protocol.TypeSessionPing, "requestId": uuid.NewString(),
		"payload": map[string]any{"clientTimeMs": testEpoch},
	})
	require.NoError(t, c.c.WriteMessage(gws.TextMessage, data))

	systemErr := decode[protocol.SystemErrorPayload](t, c.await(protocol.TypeSystemError, 3*time.Second).Payload)
	require.Equal(t, protocol.CodeUnsupportedVersion, systemErr.Code,
		"la versión no soportada debe distinguirse de un mensaje inválido")
}

// INV-SEC-007: un requestId repetido no produce un segundo efecto.
func TestComandoDuplicadoSeIgnora(t *testing.T) {
	env := newE2E(t)

	ticket, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)

	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
	env.pump()
	c.await(protocol.TypeWorldSnapshot, 3*time.Second)

	requestID := uuid.NewString()
	payload := map[string]any{
		"unitId": env.unitIDs[0],
		"target": map[string]int{"x": 46, "y": 40},
	}
	raw, _ := json.Marshal(map[string]any{
		"v": 1, "type": protocol.TypeUnitMove, "requestId": requestID, "payload": payload,
	})

	// El mismo comando, con el MISMO requestId, dos veces: es lo que hace un
	// cliente que reintenta tras un corte de red.
	require.NoError(t, c.c.WriteMessage(gws.TextMessage, raw))
	require.NoError(t, c.c.WriteMessage(gws.TextMessage, raw))
	env.pump()

	primera := decode[protocol.UnitMoveAcceptedPayload](t, c.await(protocol.TypeUnitMoveAccepted, 3*time.Second).Payload)

	// INV-SEC-007 prohíbe un segundo EFECTO, no un segundo mensaje: si el
	// duplicado se lee cuando el original ya tiene respuesta, la recibe otra vez
	// (I-5), con el mismo movementId. Lo que no puede haber es un segundo
	// movimiento.
	env.pump()
	_ = c.c.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
	iniciados := 0
	for {
		data, err := c.read()
		if err != nil {
			break
		}
		var env2 envelope
		require.NoError(t, json.Unmarshal(data, &env2))
		switch env2.Type {
		case protocol.TypeUnitMovementStarted:
			iniciados++
		case protocol.TypeUnitMoveAccepted:
			require.Equal(t, primera, decode[protocol.UnitMoveAcceptedPayload](t, env2.Payload),
				"un duplicado sólo puede recibir la respuesta original")
		}
	}
	require.Equal(t, 1, iniciados, "el comando duplicado no puede iniciar un segundo movimiento")
	m, ok := env.state.Movement(env.unitIDs[0])
	require.True(t, ok)
	require.Equal(t, primera.MovementID, m.ID)
}

// El cliente no puede colar campos que no existen en el contrato.
func TestPayloadConCamposExtraNoAportaEstado(t *testing.T) {
	env := newE2E(t)

	ticket, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)

	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
	env.pump()
	c.await(protocol.TypeWorldSnapshot, 3*time.Second)

	// S-7 / INV-SEC-001: el esquema cliente → servidor es estricto a cualquier
	// profundidad. Una ruta y un HP fabricados junto al comando legítimo, una
	// propiedad de más en el destino o en el envelope: los tres se rechazan con
	// INVALID_MESSAGE sin ejecutarse, y la sesión sigue.
	conRuta := c.send(protocol.TypeUnitMove, map[string]any{
		"unitId": env.unitIDs[0],
		"target": map[string]int{"x": 44, "y": 40},
		"path":   []map[string]int{{"x": 99, "y": 99, "tMs": 0}},
		"hp":     9999,
	})
	env.pump()
	rechazo := c.await(protocol.TypeSystemError, 3*time.Second)
	require.Equal(t, conRuta, rechazo.RequestID)
	require.Equal(t, protocol.CodeInvalidMessage, decode[protocol.SystemErrorPayload](t, rechazo.Payload).Code)

	c.send(protocol.TypeUnitMove, map[string]any{
		"unitId": env.unitIDs[0],
		"target": map[string]int{"x": 44, "y": 40, "z": 1},
	})
	env.pump()
	rechazo = c.await(protocol.TypeSystemError, 3*time.Second)
	require.Equal(t, protocol.CodeInvalidMessage, decode[protocol.SystemErrorPayload](t, rechazo.Payload).Code)

	require.NoError(t, c.c.WriteMessage(gws.TextMessage, []byte(fmt.Sprintf(
		`{"v":1,"type":"unit.move","requestId":%q,"seq":7,"payload":{"unitId":%d,"target":{"x":44,"y":40}}}`,
		uuid.NewString(), env.unitIDs[0]))))
	env.pump()
	rechazo = c.await(protocol.TypeSystemError, 3*time.Second)
	require.Equal(t, protocol.CodeInvalidMessage, decode[protocol.SystemErrorPayload](t, rechazo.Payload).Code)

	// El comando limpio sí se ejecuta, y es el PRIMERO que se acepta: ninguno
	// de los anteriores llegó a la simulación.
	limpio := c.send(protocol.TypeUnitMove, map[string]any{
		"unitId": env.unitIDs[0],
		"target": map[string]int{"x": 44, "y": 40},
	})
	env.pump()
	require.Equal(t, limpio, c.await(protocol.TypeUnitMoveAccepted, 3*time.Second).RequestID)

	started := decode[protocol.UnitMovementStartedPayload](t, c.await(protocol.TypeUnitMovementStarted, 3*time.Second).Payload)

	// La ruta la calculó el SERVIDOR: empieza donde está la unidad de verdad,
	// no donde el cliente pretendía.
	require.Equal(t, int32(42), started.Movement.Path[0].X)
	require.Equal(t, int32(40), started.Movement.Path[0].Y)
	require.Equal(t, protocol.Tile{X: 44, Y: 40}, started.Movement.Target)
	for _, wp := range started.Movement.Path {
		require.NotEqual(t, int32(99), wp.X, "ningún waypoint fabricado por el cliente entró en la ruta")
	}
}

func TestPingPongDevuelveElRelojDelServidor(t *testing.T) {
	env := newE2E(t)

	ticket, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)

	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
	env.pump()
	c.await(protocol.TypeWorldSnapshot, 3*time.Second)

	c.send(protocol.TypeSessionPing, map[string]any{"clientTimeMs": int64(12345)})
	pong := decode[protocol.SessionPongPayload](t, c.await(protocol.TypeSessionPong, 3*time.Second).Payload)

	require.EqualValues(t, 12345, pong.ClientTimeMs, "el eco debe ser exacto para estimar el RTT")
	require.Greater(t, pong.ServerTimeMs, int64(0))
}

// sendID envía un mensaje con un requestId elegido: lo que hace un cliente que
// reintenta.
func (c *conn) sendID(requestID, msgType string, payload any) {
	c.t.Helper()
	data, err := json.Marshal(map[string]any{
		"v": 1, "type": msgType, "requestId": requestID, "payload": payload,
	})
	require.NoError(c.t, err)
	c.mu.Lock()
	defer c.mu.Unlock()
	require.NoError(c.t, c.c.WriteMessage(gws.TextMessage, data))
}

// conectar abre una sesión autenticada y consume el welcome y el snapshot.
func (e *e2e) conectar() *conn {
	e.t.Helper()
	ticket, err := e.issuer.Issue(e.playerID)
	require.NoError(e.t, err)
	c := e.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)
	e.pump()
	c.await(protocol.TypeWorldSnapshot, 3*time.Second)
	return c
}

// INV-SEC-007 / I-5: el cliente pierde la conexión justo cuando el servidor
// contesta y reintenta con el mismo requestId desde una sesión nueva. No hay
// segundo efecto, y recibe la respuesta original: el mismo movementId.
func TestUnDuplicadoTrasReconectarRecibeLaRespuestaOriginal(t *testing.T) {
	env := newE2E(t)
	a := env.conectar()

	orden := uuid.NewString()
	a.sendID(orden, protocol.TypeUnitMove, map[string]any{
		"unitId": env.unitIDs[0], "target": map[string]int{"x": 50, "y": 40},
	})
	env.pump()
	original := decode[protocol.UnitMoveAcceptedPayload](t, a.await(protocol.TypeUnitMoveAccepted, 3*time.Second).Payload)
	require.NotZero(t, original.MovementID)
	require.Eventually(t, func() bool { return env.dedupe.stored(env.playerID, orden) != "pending" },
		2*time.Second, 10*time.Millisecond, "la respuesta se guarda fuera del loop")
	require.NoError(t, a.c.Close())

	b := env.conectar()
	b.sendID(orden, protocol.TypeUnitMove, map[string]any{
		"unitId": env.unitIDs[0], "target": map[string]int{"x": 50, "y": 40},
	})
	env.pump()
	repetida := b.await(protocol.TypeUnitMoveAccepted, 3*time.Second)
	require.Equal(t, orden, repetida.RequestID)
	require.Equal(t, original, decode[protocol.UnitMoveAcceptedPayload](t, repetida.Payload))

	m, ok := env.state.Movement(env.unitIDs[0])
	require.True(t, ok)
	require.Equal(t, original.MovementID, m.ID, "el duplicado no creó un segundo movimiento")
}

// I-10: un comando que la cola del loop no admite no se ejecutó. El cliente
// recibe INTERNAL_ERROR con su requestId y el reintento con ese mismo
// requestId se ejecuta, en lugar de descartarse como duplicado.
func TestConLaColaLlenaElComandoRespondeYSePuedeReintentar(t *testing.T) {
	env := newE2E(t)
	c := env.conectar()

	for len(env.commands) < cap(env.commands) {
		env.commands <- simulation.CancelMovement{PlayerID: env.playerID, UnitID: 9999}
	}

	orden := uuid.NewString()
	c.sendID(orden, protocol.TypeUnitMove, map[string]any{
		"unitId": env.unitIDs[0], "target": map[string]int{"x": 50, "y": 40},
	})
	saturado := c.await(protocol.TypeSystemError, 3*time.Second)
	require.Equal(t, orden, saturado.RequestID)
	require.Equal(t, protocol.CodeInternalError, decode[protocol.SystemErrorPayload](t, saturado.Payload).Code)

	env.pump() // vacía la cola
	c.sendID(orden, protocol.TypeUnitMove, map[string]any{
		"unitId": env.unitIDs[0], "target": map[string]int{"x": 50, "y": 40},
	})
	env.pump()
	require.Equal(t, orden, c.await(protocol.TypeUnitMoveAccepted, 3*time.Second).RequestID)
}

// ADR-022: un comando que la cola del loop no admite se cuenta en
// eo_commands_dropped_total con su tipo, y los demás tipos siguen a cero.
func TestUnComandoDescartadoPorColaLlenaSeCuentaPorTipo(t *testing.T) {
	env := newE2E(t)
	c := env.conectar()
	for len(env.commands) < cap(env.commands) {
		env.commands <- simulation.CancelMovement{PlayerID: env.playerID, UnitID: 9999}
	}

	c.send(protocol.TypeUnitMove, map[string]any{
		"unitId": env.unitIDs[0], "target": map[string]int{"x": 50, "y": 40},
	})
	require.Equal(t, protocol.CodeInternalError,
		decode[protocol.SystemErrorPayload](t, c.await(protocol.TypeSystemError, 3*time.Second).Payload).Code)

	descartados := comandosDescartados(t, env)
	require.EqualValues(t, 1, descartados[protocol.TypeUnitMove])
	for tipo, n := range descartados {
		if tipo != protocol.TypeUnitMove {
			require.Zero(t, n, "%s no se descartó", tipo)
		}
	}
	require.Len(t, descartados, len(simulation.CommandTypes()), "todas las series existen desde el arranque")
}

// ADR-022: la baja de una sesión no puede perderse —sin ella la ciudad quedaría
// ONLINE para siempre—, así que con el canal de comandos lleno va al buzón del
// loop, que se drena con prioridad, y el loop deja de contar la sesión.
func TestConElCanalLlenoLaBajaDeLaSesionVaAlBuzonDelLoop(t *testing.T) {
	env := newE2E(t)
	c := env.conectar()
	require.Equal(t, 1, env.sim.SessionCount(env.playerID))
	for len(env.commands) < cap(env.commands) {
		env.commands <- simulation.CancelMovement{PlayerID: env.playerID, UnitID: 9999}
	}

	require.NoError(t, c.c.Close())
	require.Eventually(t, func() bool { return env.inbox.Len() == 1 }, 3*time.Second, 10*time.Millisecond,
		"la baja espera en el buzón, no se descarta")
	require.Zero(t, comandosDescartados(t, env)[simulation.CommandType(simulation.PlayerDisconnected{})])

	env.pump()
	require.Equal(t, 0, env.sim.SessionCount(env.playerID), "el loop aplicó la baja")
}

// ADR-022: si el alta de una sesión no cabe en el canal de comandos dentro del
// plazo, la sesión se cierra con 4500 sin dar de baja nada —nunca llegó a
// contar— y el descarte se cuenta. El alta no va por el buzón porque debe
// aplicarse antes que el snapshot inicial, que viaja por el canal (INV-CITY-014).
func TestSinSitioParaElAltaLaSesionSeCierraSinBaja(t *testing.T) {
	env := newE2ECon(t, nil, func(cfg *ws.Config) { cfg.DispatchTimeout = 100 * time.Millisecond })
	for len(env.commands) < cap(env.commands) {
		env.commands <- simulation.CancelMovement{PlayerID: env.playerID, UnitID: 9999}
	}

	ticket, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)
	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
	_ = c.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = c.read()
	require.True(t, gws.IsCloseError(err, protocol.CloseInternalError), "se esperaba el cierre 4500, llegó %v", err)

	require.Eventually(t, func() bool {
		return strings.Contains(env.logs.String(), "el loop no admitió la sesión a tiempo")
	}, 3*time.Second, 10*time.Millisecond, "logs:\n%s", env.logs.String())
	require.Zero(t, env.inbox.Len(), "sin alta no hay baja")
	require.EqualValues(t, 1, comandosDescartados(t, env)[simulation.CommandType(simulation.PlayerConnected{})])
	env.pump()
	require.Equal(t, 0, env.sim.SessionCount(env.playerID), "el loop nunca contó la sesión")
}

// comandosDescartados lee eo_commands_dropped_total{type}.
func comandosDescartados(t *testing.T, env *e2e) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	familias, err := env.metrics.Registry().Gather()
	require.NoError(t, err)
	for _, f := range familias {
		if f.GetName() != "eo_commands_dropped_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "type" {
					out[l.GetValue()] = m.GetCounter().GetValue()
				}
			}
		}
	}
	return out
}

// INV-PLAYER-007, mitad del handshake: el servidor registra el motivo de cada
// rechazo de un ticket, y nunca el ticket. Se recorren un handshake válido, un
// ticket basura, uno firmado con otro secreto, uno caducado y uno reutilizado,
// y ninguna línea de log contiene ninguno de esos tickets ni el secreto.
func TestLosLogsDelHandshakeNoContienenTicketsNiSecreto(t *testing.T) {
	env := newE2E(t)
	rechazado := func(ticket string) {
		c := env.dial()
		c.send(protocol.TypeSessionHello, map[string]any{"ticket": ticket})
		_ = c.c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err := c.read()
		require.Error(t, err)
	}

	bueno, err := env.issuer.Issue(env.playerID)
	require.NoError(t, err)
	c := env.dial()
	c.send(protocol.TypeSessionHello, map[string]any{"ticket": bueno})
	c.await(protocol.TypeSessionWelcome, 3*time.Second)

	ajeno, err := auth.NewIssuer("otro-secreto-igual-de-largo-que-el-bueno-0123", time.Minute, env.clk).Issue(env.playerID)
	require.NoError(t, err)
	caducado, err := auth.NewIssuer(testSecret, time.Second, clock.NewFakeClock(testEpoch-60_000)).Issue(env.playerID)
	require.NoError(t, err)
	const basura = "esto-no-es-un-jwt-pero-podria-parecerlo"

	rechazado(basura)
	rechazado(ajeno)
	rechazado(caducado)
	rechazado(bueno) // reutilizado

	// Los cuatro rechazos se registran, con su motivo: si no se registrara nada,
	// el test pasaría sin comprobar nada.
	require.Eventually(t, func() bool { return strings.Count(env.logs.String(), "handshake rechazado") == 4 },
		2*time.Second, 20*time.Millisecond, "logs:\n%s", env.logs.String())
	todo := env.logs.String()
	for nombre, prohibido := range map[string]string{
		"ticket válido": bueno, "ticket ajeno": ajeno, "ticket caducado": caducado,
		"ticket basura": basura, "secreto": testSecret,
	} {
		require.False(t, strings.Contains(todo, prohibido), "los logs contienen el %s", nombre)
	}
	// Y la firma por separado: un log que truncara el ticket la dejaría ver.
	firma := bueno[strings.LastIndex(bueno, ".")+1:]
	require.False(t, strings.Contains(todo, firma), "los logs contienen la firma del ticket")
}
