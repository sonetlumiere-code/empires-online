package simulation_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loop"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// epoch es un instante fijo. Todos los tests parten de aquí: nada depende del
// reloj real de la máquina.
const epoch int64 = 1_757_376_000_000

// ─────────────────────────────────────────────────────────────
// Dobles de prueba
// ─────────────────────────────────────────────────────────────

type capturedMessage struct {
	Kind     string // "chunk" o "player"
	Type     string
	PlayerID uuid.UUID
	CX, CY   int32
	Payload  any
}

// recorder captura todo lo que la simulación emite hacia la red.
type recorder struct {
	mu       sync.Mutex
	messages []capturedMessage
}

func (r *recorder) BroadcastChunk(cx, cy int32, msgType string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, capturedMessage{Kind: "chunk", Type: msgType, CX: cx, CY: cy, Payload: payload})
}

func (r *recorder) SendToPlayer(playerID uuid.UUID, msgType, _ string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, capturedMessage{Kind: "player", Type: msgType, PlayerID: playerID, Payload: payload})
}

func (r *recorder) byType(msgType string) []capturedMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []capturedMessage
	for _, m := range r.messages {
		if m.Type == msgType {
			out = append(out, m)
		}
	}
	return out
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = nil
}

// syncPersister ejecuta los trabajos al instante: los tests no deben depender de
// temporizaciones de workers.
type syncPersister struct {
	mu   sync.Mutex
	jobs []string
	errs []error
}

func (p *syncPersister) Submit(job simulation.Job) {
	p.mu.Lock()
	p.jobs = append(p.jobs, job.Name)
	p.mu.Unlock()
	if err := job.Run(context.Background()); err != nil {
		p.mu.Lock()
		p.errs = append(p.errs, err)
		p.mu.Unlock()
	}
}

func (p *syncPersister) Depth() int { return 0 }

func (p *syncPersister) names() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.jobs...)
}

// fakeRepos registra las escrituras durables sin tocar ninguna base de datos.
type fakeRepos struct {
	mu               sync.Mutex
	movementsStarted []*movement.Movement
	movementsFinish  map[int64]movement.Status
	presence         map[int64]city.PresenceState
	positionFlushes  int
	unitsFlushed     int
}

func newFakeRepos() *fakeRepos {
	return &fakeRepos{
		movementsFinish: make(map[int64]movement.Status),
		presence:        make(map[int64]city.PresenceState),
	}
}

func (f *fakeRepos) PersistMovementStart(_ context.Context, m *movement.Movement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Emula el identificador que asignaría PostgreSQL.
	m.ID = int64(len(f.movementsStarted) + 1)
	f.movementsStarted = append(f.movementsStarted, m)
	return nil
}

func (f *fakeRepos) PersistMovementFinish(_ context.Context, id int64, status movement.Status) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.movementsFinish[id] = status
	return nil
}

func (f *fakeRepos) PersistUnitPositions(_ context.Context, units []*unit.Unit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.positionFlushes++
	f.unitsFlushed += len(units)
	return nil
}

func (f *fakeRepos) PersistCityPresence(_ context.Context, cityID int64, state city.PresenceState, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.presence[cityID] = state
	return nil
}

// ─────────────────────────────────────────────────────────────
// Banco de pruebas
// ─────────────────────────────────────────────────────────────

type harness struct {
	t        *testing.T
	clk      *clock.FakeClock
	state    *simulation.State
	sim      *simulation.Simulation
	loop     *loop.Loop
	rec      *recorder
	repos    *fakeRepos
	commands chan simulation.Command
	playerID uuid.UUID
	cityID   int64
	units    []*unit.Unit
}

// newHarness monta un mundo determinista de 64x64 de hierba con un jugador,
// su ciudad y tres aldeanos.
func newHarness(t *testing.T) *harness {
	t.Helper()

	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)

	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	repos := newFakeRepos()
	state := simulation.NewState(w)

	sim := simulation.New(state, simulation.Deps{
		Clock:              clk,
		Pathfinder:         pathfinding.NewAStar(20000, 256),
		Broadcaster:        rec,
		Persister:          &syncPersister{},
		Repos:              repos,
		Log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		ProtectionCooldown: 300 * time.Second,
		DisconnectGrace:    30 * time.Second,
		PathMaxNodes:       20000,
		PathMaxDistance:    256,
	})

	commands := make(chan simulation.Command, 256)
	gameLoop := loop.New(sim, clk, commands, loop.Config{
		TickDuration:       100 * time.Millisecond,
		FlushIntervalTicks: 50,
		MaxCommandsPerTick: 256,
		EpochMs:            epoch,
	}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	h := &harness{
		t: t, clk: clk, state: state, sim: sim, loop: gameLoop,
		rec: rec, repos: repos, commands: commands,
		playerID: uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		cityID:   1,
	}

	c := &city.City{
		ID: h.cityID, OwnerPlayerID: h.playerID, Name: "Testópolis",
		CenterX: 10, CenterY: 10, Era: city.EraStone,
		Population: 3, PopulationLimit: 20, PresenceState: city.PresenceOnline,
	}
	state.AddCity(c)

	spawns := []world.Tile{{X: 12, Y: 10}, {X: 8, Y: 10}, {X: 10, Y: 12}}
	for i, spawn := range spawns {
		u := &unit.Unit{
			ID: int64(i + 1), PlayerID: h.playerID, CityID: &c.ID,
			Type: unit.TypeVillager, X: spawn.X, Y: spawn.Y,
			HP: 40, MaxHP: 40, Status: unit.StatusIdle,
		}
		state.AddUnit(u)
		h.units = append(h.units, u)
	}
	return h
}

// advance adelanta el reloj y ejecuta los ticks correspondientes.
func (h *harness) advance(d time.Duration) {
	h.t.Helper()
	ticks := int(d.Milliseconds() / 100)
	for i := 0; i < ticks; i++ {
		h.clk.AdvanceMs(100)
		h.loop.Step(h.clk.NowMs())
	}
}

// send encola un comando y ejecuta un tick para que se aplique.
func (h *harness) send(cmd simulation.Command) {
	h.t.Helper()
	h.commands <- cmd
	h.loop.Step(h.clk.NowMs())
}

func (h *harness) unitTile(id int64) world.Tile {
	h.t.Helper()
	u, ok := h.state.Unit(id)
	require.True(h.t, ok, "la unidad %d debería existir", id)
	return u.Tile()
}

// ─────────────────────────────────────────────────────────────
// Movimiento: el núcleo del vertical slice
// ─────────────────────────────────────────────────────────────

// TestVerticalSliceMovimiento es el test de simulación canónico:
// el mundo arranca en T0, la unidad se mueve de A a B, se avanza el tiempo, y se
// asserta el estado EXACTO en cada instante.
func TestVerticalSliceMovimiento(t *testing.T) {
	h := newHarness(t)
	u := h.units[0] // en (12,10)

	// 5 tiles de hierba en ortogonal: 5 * 600 ms = 3000 ms exactos.
	h.send(simulation.MoveUnit{
		PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: u.ID, Target: world.Tile{X: 17, Y: 10},
	})

	accepted := h.rec.byType(protocol.TypeUnitMoveAccepted)
	require.Len(t, accepted, 1, "el comando debe aceptarse")

	started := h.rec.byType(protocol.TypeUnitMovementStarted)
	require.Len(t, started, 1)
	payload := started[0].Payload.(protocol.UnitMovementStartedPayload)
	require.Len(t, payload.Movement.Path, 6, "origen más cinco pasos")
	require.EqualValues(t, 0, payload.Movement.Path[0].TMs)
	require.EqualValues(t, 3000, payload.Movement.Path[5].TMs)
	require.Equal(t, epoch+3000, payload.Movement.ArrivalTimeMs)

	m, ok := h.state.Movement(u.ID)
	require.True(t, ok)
	require.Equal(t, unit.StatusMoving, u.Status)

	// Instante a instante, la posición autoritativa es exacta.
	require.Equal(t, world.Tile{X: 12, Y: 10}, h.unitTile(u.ID))

	h.advance(600 * time.Millisecond)
	require.Equal(t, world.Tile{X: 13, Y: 10}, h.unitTile(u.ID))

	h.advance(600 * time.Millisecond)
	require.Equal(t, world.Tile{X: 14, Y: 10}, h.unitTile(u.ID))

	h.advance(1200 * time.Millisecond) // t = 2400
	require.Equal(t, world.Tile{X: 16, Y: 10}, h.unitTile(u.ID))
	require.Equal(t, unit.StatusMoving, u.Status, "a 2400 ms aún no ha llegado")

	h.advance(600 * time.Millisecond) // t = 3000: llegada exacta
	require.Equal(t, world.Tile{X: 17, Y: 10}, h.unitTile(u.ID))
	require.Equal(t, unit.StatusIdle, u.Status)

	_, stillMoving := h.state.Movement(u.ID)
	require.False(t, stillMoving, "el movimiento terminado se retira del mundo")

	completed := h.rec.byType(protocol.TypeUnitMovementCompleted)
	require.Len(t, completed, 1)
	done := completed[0].Payload.(protocol.UnitMovementCompletedPayload)
	require.Equal(t, protocol.Tile{X: 17, Y: 10}, done.FinalPosition)
	require.Equal(t, m.ID, done.MovementID)
}

// INV-MOVE-001: una unidad tiene como máximo un movimiento activo.
func TestNuevaOrdenReemplazaLaAnterior(t *testing.T) {
	h := newHarness(t)
	u := h.units[0]

	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: u.ID, Target: world.Tile{X: 20, Y: 10}})
	first, ok := h.state.Movement(u.ID)
	require.True(t, ok)

	h.advance(1200 * time.Millisecond) // dos tiles recorridos
	require.Equal(t, world.Tile{X: 14, Y: 10}, h.unitTile(u.ID))

	h.rec.reset()
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: u.ID, Target: world.Tile{X: 14, Y: 20}})

	cancelled := h.rec.byType(protocol.TypeUnitMovementCancelled)
	require.Len(t, cancelled, 1, "el movimiento anterior debe cancelarse")
	c := cancelled[0].Payload.(protocol.UnitMovementCancelledPayload)
	require.Equal(t, first.ID, c.MovementID)
	require.Equal(t, string(movement.ReasonReplaced), c.Reason)
	require.Equal(t, protocol.Tile{X: 14, Y: 10}, c.StoppedAt,
		"la unidad se detiene sobre un tile completo, jamás entre dos")

	second, ok := h.state.Movement(u.ID)
	require.True(t, ok)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, world.Tile{X: 14, Y: 10}, second.Path.Origin(),
		"la ruta nueva parte de donde está realmente la unidad")

	// La unidad llega al nuevo destino, no al viejo.
	h.advance(6100 * time.Millisecond)
	require.Equal(t, world.Tile{X: 14, Y: 20}, h.unitTile(u.ID))
	require.Equal(t, unit.StatusIdle, u.Status)
}

func TestCancelacionExplicita(t *testing.T) {
	h := newHarness(t)
	u := h.units[0]

	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: u.ID, Target: world.Tile{X: 22, Y: 10}})
	h.advance(1800 * time.Millisecond) // tres tiles

	h.rec.reset()
	h.send(simulation.CancelMovement{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: u.ID})

	cancelled := h.rec.byType(protocol.TypeUnitMovementCancelled)
	require.Len(t, cancelled, 1)
	c := cancelled[0].Payload.(protocol.UnitMovementCancelledPayload)
	require.Equal(t, string(movement.ReasonCancelledByPlayer), c.Reason)
	require.Equal(t, protocol.Tile{X: 15, Y: 10}, c.StoppedAt)

	require.Equal(t, unit.StatusIdle, u.Status)
	_, moving := h.state.Movement(u.ID)
	require.False(t, moving)

	// Y no sigue avanzando después de cancelar.
	h.advance(5 * time.Second)
	require.Equal(t, world.Tile{X: 15, Y: 10}, h.unitTile(u.ID))
}

// INV-PLAYER-001: un jugador sólo puede comandar unidades propias.
func TestRechazosDeMovimiento(t *testing.T) {
	otro := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	cases := []struct {
		name     string
		build    func(h *harness) simulation.MoveUnit
		wantCode string
	}{
		{
			name: "unidad inexistente",
			build: func(h *harness) simulation.MoveUnit {
				return simulation.MoveUnit{PlayerID: h.playerID, UnitID: 9999, Target: world.Tile{X: 20, Y: 20}}
			},
			wantCode: protocol.CodeUnitNotFound,
		},
		{
			name: "unidad ajena",
			build: func(h *harness) simulation.MoveUnit {
				return simulation.MoveUnit{PlayerID: otro, UnitID: h.units[0].ID, Target: world.Tile{X: 20, Y: 20}}
			},
			wantCode: protocol.CodeUnitNotOwned,
		},
		{
			name: "destino fuera del mundo",
			build: func(h *harness) simulation.MoveUnit {
				return simulation.MoveUnit{PlayerID: h.playerID, UnitID: h.units[0].ID, Target: world.Tile{X: 500, Y: 500}}
			},
			wantCode: protocol.CodeTargetOutOfBounds,
		},
		{
			name: "destino intransitable",
			build: func(h *harness) simulation.MoveUnit {
				h.state.World().SetBlocked(30, 30, 30, 30, true)
				return simulation.MoveUnit{PlayerID: h.playerID, UnitID: h.units[0].ID, Target: world.Tile{X: 30, Y: 30}}
			},
			wantCode: protocol.CodeTargetNotWalkable,
		},
		{
			name: "unidad muerta",
			build: func(h *harness) simulation.MoveUnit {
				h.units[0].Status = unit.StatusDead
				h.units[0].HP = 0
				return simulation.MoveUnit{PlayerID: h.playerID, UnitID: h.units[0].ID, Target: world.Tile{X: 20, Y: 20}}
			},
			wantCode: protocol.CodeUnitDead,
		},
		{
			name: "unidad guarnecida",
			build: func(h *harness) simulation.MoveUnit {
				h.units[0].Status = unit.StatusGarrisoned
				return simulation.MoveUnit{PlayerID: h.playerID, UnitID: h.units[0].ID, Target: world.Tile{X: 20, Y: 20}}
			},
			wantCode: protocol.CodeUnitGarrisoned,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			cmd := c.build(h)
			cmd.RequestID = uuid.NewString()
			h.send(cmd)

			rejected := h.rec.byType(protocol.TypeUnitMoveRejected)
			require.Len(t, rejected, 1, "debía rechazarse")
			payload := rejected[0].Payload.(protocol.UnitMoveRejectedPayload)
			require.Equal(t, c.wantCode, payload.Code)

			require.Empty(t, h.rec.byType(protocol.TypeUnitMovementStarted),
				"un comando rechazado no puede producir ningún movimiento")
		})
	}
}

func TestSinRutaPosibleSeRechaza(t *testing.T) {
	h := newHarness(t)
	// Se amuralla por completo un destino accesible.
	h.state.World().SetBlocked(39, 39, 41, 41, true)
	h.state.World().SetBlocked(40, 40, 40, 40, false) // el centro queda libre pero aislado

	h.send(simulation.MoveUnit{
		PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: h.units[0].ID, Target: world.Tile{X: 40, Y: 40},
	})

	rejected := h.rec.byType(protocol.TypeUnitMoveRejected)
	require.Len(t, rejected, 1)
	require.Equal(t, protocol.CodePathNotFound,
		rejected[0].Payload.(protocol.UnitMoveRejectedPayload).Code)
}

func TestMoverseAlSitioDondeYaEstas(t *testing.T) {
	h := newHarness(t)
	u := h.units[0]

	h.send(simulation.MoveUnit{
		PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: u.ID, Target: u.Tile(),
	})

	require.Len(t, h.rec.byType(protocol.TypeUnitMoveAccepted), 1)
	require.Empty(t, h.rec.byType(protocol.TypeUnitMovementStarted),
		"no se crea una polilínea degenerada de un solo punto")
	require.Equal(t, unit.StatusIdle, u.Status)
}

// ─────────────────────────────────────────────────────────────
// Presencia y protección offline
// ─────────────────────────────────────────────────────────────

func TestCicloDePresenciaYProteccion(t *testing.T) {
	h := newHarness(t)
	c, ok := h.state.City(h.cityID)
	require.True(t, ok)

	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: uuid.New()})
	require.Equal(t, city.PresenceOnline, c.PresenceState)

	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: uuid.New()})
	require.Equal(t, city.PresenceOnline, c.PresenceState,
		"un corte breve no degrada nada: primero corre el margen de reconexión")

	h.advance(29 * time.Second)
	require.Equal(t, city.PresenceOnline, c.PresenceState, "aún dentro del margen")

	h.advance(2 * time.Second) // total 31 s > 30 s de margen
	require.Equal(t, city.PresenceOfflinePending, c.PresenceState)
	require.NotNil(t, c.LastOfflineAt)

	h.advance(290 * time.Second)
	require.Equal(t, city.PresenceOfflinePending, c.PresenceState, "el cooldown aún no venció")

	h.advance(20 * time.Second) // supera con holgura los 300 s de cooldown
	require.Equal(t, city.PresenceProtected, c.PresenceState)
	require.True(t, c.IsProtected())

	// Volver a conectarse levanta la protección.
	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: uuid.New()})
	require.Equal(t, city.PresenceOnline, c.PresenceState)
	require.Nil(t, c.ProtectionUntil)
}

func TestReconexionDentroDelMargenNoDegradaLaCiudad(t *testing.T) {
	h := newHarness(t)
	c, _ := h.state.City(h.cityID)

	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: uuid.New()})
	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: uuid.New()})
	h.advance(10 * time.Second)

	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: uuid.New()})
	h.advance(60 * time.Second)

	require.Equal(t, city.PresenceOnline, c.PresenceState,
		"reconectar dentro del margen cancela la degradación")
}

func TestVariasSesionesDelMismoJugador(t *testing.T) {
	h := newHarness(t)
	c, _ := h.state.City(h.cityID)
	s1, s2 := uuid.New(), uuid.New()

	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: s1})
	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: s2})

	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: s1})
	h.advance(120 * time.Second)
	require.Equal(t, city.PresenceOnline, c.PresenceState,
		"con una pestaña abierta el jugador sigue presente")

	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: s2})
	h.advance(60 * time.Second)
	require.Equal(t, city.PresenceOfflinePending, c.PresenceState)
}

// El mundo no se detiene porque el jugador se desconecte.
func TestElMovimientoContinuaConElJugadorDesconectado(t *testing.T) {
	h := newHarness(t)
	u := h.units[0]

	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: uuid.New()})
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: u.ID, Target: world.Tile{X: 22, Y: 10}})

	h.advance(600 * time.Millisecond)
	require.Equal(t, world.Tile{X: 13, Y: 10}, h.unitTile(u.ID))

	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: uuid.New()})

	h.advance(5400 * time.Millisecond) // completa los 6000 ms del recorrido
	require.Equal(t, world.Tile{X: 22, Y: 10}, h.unitTile(u.ID),
		"la unidad llegó a su destino con el jugador desconectado")
	require.Equal(t, unit.StatusIdle, u.Status)
}

// ─────────────────────────────────────────────────────────────
// Recuperación tras reinicio
// ─────────────────────────────────────────────────────────────

func TestRecuperacionMovimientoEnCurso(t *testing.T) {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)

	playerID := uuid.New()
	cityID := int64(1)
	u := &unit.Unit{ID: 1, PlayerID: playerID, CityID: &cityID, Type: unit.TypeVillager,
		X: 10, Y: 10, HP: 40, MaxHP: 40, Status: unit.StatusMoving}
	c := &city.City{ID: cityID, OwnerPlayerID: playerID, Name: "X", CenterX: 10, CenterY: 10,
		Era: city.EraStone, PopulationLimit: 20, PresenceState: city.PresenceOnline}

	path, err := movement.BuildTimedPath(
		[]world.Tile{{X: 10, Y: 10}, {X: 11, Y: 10}, {X: 12, Y: 10}, {X: 13, Y: 10}, {X: 14, Y: 10}},
		w, 600)
	require.NoError(t, err)
	m := movement.New(u.ID, path, world.Tile{X: 14, Y: 10}, epoch)

	// El servidor "cae" y vuelve a los 1500 ms: la unidad iba por el tile 2.
	state := simulation.NewState(w)
	res := simulation.Hydrate(state, []*unit.Unit{u}, []*city.City{c},
		[]*movement.Movement{m}, epoch+1500, slog.New(slog.NewTextHandler(io.Discard, nil)))

	require.Equal(t, 1, res.Resumed)
	require.Equal(t, 0, res.Arrived)
	require.Equal(t, world.Tile{X: 12, Y: 10}, u.Tile(),
		"la posición se reconstruye de la polilínea, sin recalcular la ruta")
	require.Equal(t, unit.StatusMoving, u.Status)

	_, stillActive := state.Movement(u.ID)
	require.True(t, stillActive, "el movimiento continúa desde donde le tocaba")
}

// El caso que justifica todo el diseño: el movimiento terminó MIENTRAS el
// servidor estaba caído. El mundo siguió existiendo aunque el proceso no.
func TestRecuperacionMovimientoVencidoDuranteLaCaida(t *testing.T) {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, _ := world.New(64, 64, 32, 1, terrain)

	playerID := uuid.New()
	cityID := int64(1)
	u := &unit.Unit{ID: 1, PlayerID: playerID, CityID: &cityID, Type: unit.TypeVillager,
		X: 10, Y: 10, HP: 40, MaxHP: 40, Status: unit.StatusMoving}
	c := &city.City{ID: cityID, OwnerPlayerID: playerID, Name: "X", CenterX: 10, CenterY: 10,
		Era: city.EraStone, PopulationLimit: 20, PresenceState: city.PresenceOnline}

	path, _ := movement.BuildTimedPath(
		[]world.Tile{{X: 10, Y: 10}, {X: 11, Y: 10}, {X: 12, Y: 10}}, w, 600)
	m := movement.New(u.ID, path, world.Tile{X: 12, Y: 10}, epoch)
	m.ID = 77

	state := simulation.NewState(w)
	// Vuelve una hora más tarde.
	res := simulation.Hydrate(state, []*unit.Unit{u}, []*city.City{c},
		[]*movement.Movement{m}, epoch+3_600_000, slog.New(slog.NewTextHandler(io.Discard, nil)))

	require.Equal(t, 1, res.Arrived)
	require.Equal(t, 0, res.Resumed)
	require.Equal(t, world.Tile{X: 12, Y: 10}, u.Tile(), "la unidad aparece en su destino")
	require.Equal(t, unit.StatusIdle, u.Status)

	_, stillActive := state.Movement(u.ID)
	require.False(t, stillActive)

	require.Len(t, res.FinishedMovements, 1)
	require.Equal(t, int64(77), res.FinishedMovements[0].MovementID)
	require.Equal(t, movement.StatusCompleted, res.FinishedMovements[0].Status)
}

// Ante un dato dudoso NO se teletransporta a nadie.
func TestRecuperacionConPolilineaInvalida(t *testing.T) {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, _ := world.New(64, 64, 32, 1, terrain)
	// El mapa cambió: ahora hay una construcción sobre la ruta.
	w.SetBlocked(11, 10, 11, 10, true)

	playerID := uuid.New()
	u := &unit.Unit{ID: 1, PlayerID: playerID, Type: unit.TypeVillager,
		X: 10, Y: 10, HP: 40, MaxHP: 40, Status: unit.StatusMoving}

	m := &movement.Movement{
		ID: 5, UnitID: 1, Status: movement.StatusActive,
		Path: movement.TimedPath{
			{X: 10, Y: 10, TMs: 0},
			{X: 11, Y: 10, TMs: 600},
		},
		Target: world.Tile{X: 11, Y: 10}, StartTimeMs: epoch, ArrivalTimeMs: epoch + 600,
	}

	state := simulation.NewState(w)
	res := simulation.Hydrate(state, []*unit.Unit{u}, nil, []*movement.Movement{m},
		epoch+300, slog.New(slog.NewTextHandler(io.Discard, nil)))

	require.Equal(t, 1, res.Failed)
	require.Equal(t, world.Tile{X: 10, Y: 10}, u.Tile(), "se queda donde estaba")
	require.Equal(t, unit.StatusIdle, u.Status)
	require.Equal(t, movement.StatusFailed, res.FinishedMovements[0].Status)
}

// ─────────────────────────────────────────────────────────────
// Determinismo y persistencia
// ─────────────────────────────────────────────────────────────

// Dos ejecuciones idénticas deben producir un estado final idéntico.
func TestSimulacionEsReproducible(t *testing.T) {
	run := func() []world.Tile {
		h := newHarness(t)
		for _, u := range h.units {
			h.send(simulation.MoveUnit{
				PlayerID: h.playerID, RequestID: uuid.NewString(),
				UnitID: u.ID, Target: world.Tile{X: 30, Y: 30},
			})
		}
		h.advance(30 * time.Second)

		var out []world.Tile
		h.state.EachUnit(func(u *unit.Unit) bool {
			out = append(out, u.Tile())
			return true
		})
		return out
	}

	require.Equal(t, run(), run(), "la misma secuencia de comandos debe dar el mismo estado final")
}

func TestVolcadoPeriodicoNoOcurreEnCadaTick(t *testing.T) {
	h := newHarness(t)
	repos := newFakeRepos()
	persister := &syncPersister{}

	// Se reconstruye la simulación para poder observar el persister.
	h.sim = simulation.New(h.state, simulation.Deps{
		Clock:              h.clk,
		Pathfinder:         pathfinding.NewAStar(20000, 256),
		Broadcaster:        h.rec,
		Persister:          persister,
		Repos:              repos,
		Log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		ProtectionCooldown: 300 * time.Second,
		DisconnectGrace:    30 * time.Second,
	})
	h.loop = loop.New(h.sim, h.clk, h.commands, loop.Config{
		TickDuration: 100 * time.Millisecond, FlushIntervalTicks: 50,
		MaxCommandsPerTick: 256, EpochMs: epoch,
	}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: h.units[0].ID, Target: world.Tile{X: 22, Y: 10}})

	// 60 ticks: con un volcado cada 50 ticks debe haber exactamente uno.
	h.advance(6 * time.Second)

	flushes := 0
	for _, name := range persister.names() {
		if name == "units.flush" {
			flushes++
		}
	}
	require.Equal(t, 1, flushes,
		"se vuelca por lotes cada 50 ticks, jamás una escritura por unidad y por tick")
	require.Equal(t, 1, repos.positionFlushes)
}

func TestSnapshotDelAreaDeInteres(t *testing.T) {
	h := newHarness(t)

	chunks := h.state.World().ChunksInRadius(world.Tile{X: 10, Y: 10}, 2)
	snap := h.sim.BuildSnapshot(chunks, h.clk.NowMs(), true)

	require.Equal(t, h.clk.NowMs(), snap.ServerTimeMs)
	require.Len(t, snap.Units, 3, "los tres aldeanos están en el área de interés")
	require.Len(t, snap.Cities, 1)
	require.NotEmpty(t, snap.Terrain)
	for _, terrain := range snap.Terrain {
		require.EqualValues(t, 32, terrain.Size)
		require.NotEmpty(t, terrain.Terrain, "el terreno viaja en base64")
	}

	// Un área lejana no ve nada de este jugador.
	lejos := h.state.World().ChunksInRadius(world.Tile{X: 60, Y: 60}, 0)
	vacio := h.sim.BuildSnapshot(lejos, h.clk.NowMs(), false)
	require.Empty(t, vacio.Units)
	require.Empty(t, vacio.Cities)
}

func TestSnapshotIncluyeElMovimientoEnCurso(t *testing.T) {
	h := newHarness(t)
	u := h.units[0]

	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: u.ID, Target: world.Tile{X: 20, Y: 10}})
	h.advance(1200 * time.Millisecond)

	chunks := h.state.World().ChunksInRadius(world.Tile{X: 14, Y: 10}, 1)
	snap := h.sim.BuildSnapshot(chunks, h.clk.NowMs(), false)

	var found *protocol.UnitView
	for i := range snap.Units {
		if snap.Units[i].ID == u.ID {
			found = &snap.Units[i]
		}
	}
	require.NotNil(t, found, "la unidad en movimiento debe aparecer en el snapshot")
	require.NotNil(t, found.Movement, "y debe traer su polilínea para que el cliente interpole")
	require.Equal(t, protocol.Tile{X: 20, Y: 10}, found.Movement.Target)
	require.EqualValues(t, 14, found.X, "la posición del snapshot es la autoritativa de ahora mismo")
}
