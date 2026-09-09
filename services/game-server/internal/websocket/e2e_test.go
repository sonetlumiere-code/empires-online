package websocket_test

import (
	"context"
	"encoding/json"
	"io"
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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loop"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
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
	seen map[string]bool
}

func newMemoryDeduper() *memoryDeduper { return &memoryDeduper{seen: map[string]bool{}} }

func (m *memoryDeduper) Claim(_ context.Context, playerID uuid.UUID, requestID string) (bool, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := playerID.String() + ":" + requestID
	if m.seen[key] {
		return false, "", nil
	}
	m.seen[key] = true
	return true, "", nil
}

type memoryRepos struct{ mu sync.Mutex }

func (r *memoryRepos) PersistMovementStart(_ context.Context, m *movement.Movement) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m.ID == 0 {
		m.ID = 1
	}
	return nil
}
func (r *memoryRepos) PersistMovementFinish(context.Context, int64, movement.Status) error {
	return nil
}
func (r *memoryRepos) PersistUnitPositions(context.Context, []*unit.Unit) error { return nil }
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
	playerID uuid.UUID
	unitIDs  []int64
	stop     func()
}

func newE2E(t *testing.T) *e2e {
	t.Helper()

	terrain := make([]byte, 128*128)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	gameWorld, err := world.New(128, 128, 32, 1, terrain)
	require.NoError(t, err)

	clk := clock.NewFakeClock(testEpoch)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	state := simulation.NewState(gameWorld)
	hub := ws.NewHub(nil, log)

	sim := simulation.New(state, simulation.Deps{
		Clock:              clk,
		Pathfinder:         pathfinding.NewAStar(20000, 256),
		Broadcaster:        hub,
		Persister:          syncPersister{},
		Repos:              &memoryRepos{},
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

	commands := make(chan simulation.Command, 256)
	gameLoop := loop.New(sim, clk, commands, loop.Config{
		TickDuration:       100 * time.Millisecond,
		FlushIntervalTicks: 50,
		MaxCommandsPerTick: 256,
		EpochMs:            testEpoch,
	}, nil, nil, log)

	authenticator := auth.NewAuthenticator(auth.NewVerifier(testSecret, clk), newMemoryTickets())

	wsServer := ws.NewServer(ws.Config{
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
		WorldWidth:           128,
		WorldHeight:          128,
		ChunkSize:            32,
	}, hub, authenticator, newMemoryPresence(), newMemoryDeduper(), commands, nil, log)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsServer.Handler(context.Background()))
	httpServer := httptest.NewServer(mux)

	// El loop se conduce a mano desde el test: nada depende de temporizadores
	// reales ni de la velocidad de la máquina.
	env := &e2e{
		t: t, server: httpServer, loop: gameLoop, clk: clk,
		issuer:   auth.NewIssuer(testSecret, 60*time.Second, clk),
		playerID: playerID, unitIDs: unitIDs,
	}
	env.stop = func() { httpServer.Close() }
	t.Cleanup(env.stop)
	return env
}

type syncPersister struct{}

func (syncPersister) Submit(job simulation.Job) { _ = job.Run(context.Background()) }
func (syncPersister) Depth() int                { return 0 }

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
}

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
		_, data, err := c.c.ReadMessage()
		if err != nil {
			c.t.Fatalf("esperando %q: %v (recibidos: %v)", msgType, err, seen)
		}
		var env envelope
		require.NoError(c.t, json.Unmarshal(data, &env))
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
	_, _, err := c.c.ReadMessage()
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
	_, _, err := c.c.ReadMessage()
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
	_, _, err = c2.c.ReadMessage()
	require.Error(t, err, "el ticket reutilizado debe rechazarse")
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

	c.await(protocol.TypeUnitMoveAccepted, 3*time.Second)

	// No debe haber una segunda aceptación.
	_ = c.c.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
	second := false
	_ = second
	for {
		_, data, err := c.c.ReadMessage()
		if err != nil {
			break
		}
		var env2 envelope
		if json.Unmarshal(data, &env2) == nil && env2.Type == protocol.TypeUnitMoveAccepted {
			second = true
			break
		}
	}
	require.False(t, second, "el comando duplicado no puede aceptarse dos veces")
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

	// Se intenta enviar una ruta y un HP fabricados junto al comando legítimo.
	c.send(protocol.TypeUnitMove, map[string]any{
		"unitId": env.unitIDs[0],
		"target": map[string]int{"x": 44, "y": 40},
		"path":   []map[string]int{{"x": 99, "y": 99, "tMs": 0}},
		"hp":     9999,
	})
	env.pump()

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
