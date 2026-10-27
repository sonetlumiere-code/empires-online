package simulation_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loop"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// ADR-013 §2.1: el id del movimiento lo asigna el loop al aceptar la orden, y
// es el mismo en unit.move.accepted, en unit.movement.started y en la
// escritura durable. Los ids crecen desde el suelo que se fija al arrancar.
func TestElIdDelMovimientoLoAsignaElLoop(t *testing.T) {
	h := newHarness(t)
	h.state.SetMovementIDFloor(41)

	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: h.units[0].ID, Target: world.Tile{X: 17, Y: 10}})
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: h.units[1].ID, Target: world.Tile{X: 3, Y: 10}})

	aceptados := h.rec.byType(protocol.TypeUnitMoveAccepted)
	iniciados := h.rec.byType(protocol.TypeUnitMovementStarted)
	require.Len(t, aceptados, 2)
	require.Len(t, iniciados, 2)
	require.Len(t, h.repos.movementsStarted, 2)
	for i, want := range []int64{42, 43} {
		assert.Equal(t, want, aceptados[i].Payload.(protocol.UnitMoveAcceptedPayload).MovementID)
		assert.Equal(t, want, iniciados[i].Payload.(protocol.UnitMovementStartedPayload).Movement.MovementID)
		assert.Equal(t, want, h.repos.movementsStarted[i].ID, "la escritura durable lleva el mismo id")
	}
}

// INV-PERSIST-004: el volcado periódico no escribe ninguna unidad en marcha,
// y el inicio deja la unidad MOVING en su origen (ADR-013 §2.3).
func TestUnaUnidadEnMarchaNoSeVuelca(t *testing.T) {
	h := newHarness(t)
	u := h.units[0]
	origen := u.Tile()
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: u.ID, Target: world.Tile{X: 40, Y: 10}}) // 28 tiles: 16,8 s

	h.advance(11 * time.Second) // dos volcados con la unidad en marcha
	require.Equal(t, unit.StatusMoving, u.Status)
	assert.Zero(t, h.repos.unitsFlushed, "una unidad en marcha no se vuelca")
	require.Len(t, h.repos.startOrigins, 1)
	assert.Equal(t, origen, h.repos.startOrigins[0])
}

// ADR-013 §2.3: el cierre de un movimiento, por llegada o por cancelación,
// lleva la posición final de la unidad para escribirla en su transacción.
func TestElCierreDelMovimientoLlevaLaPosicionFinal(t *testing.T) {
	h := newHarness(t)
	llega, cancela := h.units[0], h.units[1]
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: llega.ID, Target: world.Tile{X: 14, Y: 10}})
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: cancela.ID, Target: world.Tile{X: 2, Y: 10}})
	h.advance(1300 * time.Millisecond)
	h.send(simulation.CancelMovement{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: cancela.ID})

	porUnidad := map[int64]simulation.MovementFinish{}
	for _, f := range h.repos.finishes {
		porUnidad[f.UnitID] = f
	}
	require.Contains(t, porUnidad, llega.ID)
	assert.Equal(t, movement.StatusCompleted, porUnidad[llega.ID].Status)
	assert.Equal(t, world.Tile{X: 14, Y: 10}, porUnidad[llega.ID].At)
	require.Contains(t, porUnidad, cancela.ID)
	assert.Equal(t, movement.StatusCancelled, porUnidad[cancela.ID].Status)
	assert.Equal(t, cancela.Tile(), porUnidad[cancela.ID].At, "donde se detuvo")
	assert.NotZero(t, porUnidad[cancela.ID].MovementID)
}

// falloDeInicio es un persistidor que ejecuta en el acto y, si el trabajo
// falla, agota los reintentos y llama a OnPermanentFailure como la cola real.
type falloDeInicio struct{ jobs []string }

func (p *falloDeInicio) Submit(job simulation.Job) {
	p.jobs = append(p.jobs, job.Name)
	if err := job.Run(context.Background()); err != nil && job.OnPermanentFailure != nil {
		job.OnPermanentFailure(err)
	}
}
func (p *falloDeInicio) Depth() int      { return 0 }
func (p *falloDeInicio) Partitions() int { return 1 }

type reposQueFallanAlIniciar struct{ *fakeRepos }

func (r reposQueFallanAlIniciar) PersistMovementStart(context.Context, movement.Movement, world.Tile) error {
	return errors.New("PostgreSQL no responde")
}

// ADR-013 §2.4: si el inicio de un movimiento no se puede persistir, la
// compensación vuelve al loop por el buzón y la unidad se detiene, con un
// unit.movement.cancelled de motivo SERVER.
func TestUnInicioQueNoSePersisteDetieneLaUnidad(t *testing.T) {
	h := newHarness(t)
	h.sim = simulation.New(h.state, simulation.Deps{
		Clock:              h.clk,
		Pathfinder:         pathfinding.NewAStar(20000, 256),
		Broadcaster:        h.rec,
		Persister:          &falloDeInicio{},
		Repos:              reposQueFallanAlIniciar{h.repos},
		Inbox:              h.inbox.Offer,
		Log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		ProtectionCooldown: 300 * time.Second,
		DisconnectGrace:    30 * time.Second,
	})
	h.loop = loop.New(h.sim, h.clk, h.commands, h.inbox, loop.Config{
		TickDuration: 100 * time.Millisecond, FlushIntervalTicks: 50,
		MaxCommandsPerTick: 256, EpochMs: epoch,
	}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	u := h.units[0]
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
		UnitID: u.ID, Target: world.Tile{X: 20, Y: 10}})
	h.advance(100 * time.Millisecond) // el tick siguiente aplica la compensación

	_, enMarcha := h.state.Movement(u.ID)
	assert.False(t, enMarcha, "la unidad ya no tiene un movimiento que la base de datos no conoce")
	assert.Equal(t, unit.StatusIdle, u.Status)
	cancelados := h.rec.byType(protocol.TypeUnitMovementCancelled)
	require.Len(t, cancelados, 1)
	assert.Equal(t, string(movement.ReasonServer), cancelados[0].Payload.(protocol.UnitMovementCancelledPayload).Reason)
}

// ─────────────────────────────────────────────────────────────
// Compensaciones por volcado (ADR-022)
// ─────────────────────────────────────────────────────────────

var errBaseCaida = errors.New("PostgreSQL no responde")

// presenciaEscrita es una presencia que llegó a la base, con su marca.
type presenciaEscrita struct {
	cityID int64
	state  city.PresenceState
	at     time.Time
}

// reposFragiles envuelve fakeRepos y hace fallar las escrituras que se le
// indiquen, como una base de datos caída; registra además la marca temporal de
// cada presencia que sí se escribió.
type reposFragiles struct {
	*fakeRepos
	fallaPresencia, fallaVolcado, fallaCierre bool

	presenciasEscritas []presenciaEscrita
}

func (r *reposFragiles) PersistCityPresence(ctx context.Context, cityID int64, state city.PresenceState, at time.Time) error {
	if r.fallaPresencia {
		return errBaseCaida
	}
	r.presenciasEscritas = append(r.presenciasEscritas, presenciaEscrita{cityID: cityID, state: state, at: at})
	return r.fakeRepos.PersistCityPresence(ctx, cityID, state, at)
}

func (r *reposFragiles) PersistUnitPositions(ctx context.Context, units []*unit.Unit) error {
	if r.fallaVolcado {
		return errBaseCaida
	}
	return r.fakeRepos.PersistUnitPositions(ctx, units)
}

func (r *reposFragiles) PersistMovementFinish(ctx context.Context, f simulation.MovementFinish) error {
	if r.fallaCierre {
		return errBaseCaida
	}
	return r.fakeRepos.PersistMovementFinish(ctx, f)
}

// conPersistenciaFragil reconstruye la simulación y el loop del banco con
// repositorios que fallan a voluntad y con el persistidor síncrono en modo
// permanente: un trabajo que falla agota sus reintentos en el acto y llama a su
// compensación, como la cola real al rendirse.
func conPersistenciaFragil(h *harness) *reposFragiles {
	repos := &reposFragiles{fakeRepos: h.repos}
	h.persister.permanent = true
	silencio := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.sim = simulation.New(h.state, simulation.Deps{
		Clock: h.clk, Pathfinder: h.rutas, Broadcaster: h.rec,
		Persister: h.persister, Repos: repos, Garrisons: h.garrisons,
		Inbox:              h.inbox.Offer,
		Log:                silencio,
		ProtectionCooldown: 300 * time.Second,
		DisconnectGrace:    30 * time.Second,
		PathMaxNodes:       20000,
		PathMaxDistance:    256,
	})
	h.loop = loop.New(h.sim, h.clk, h.commands, h.inbox, loop.Config{
		TickDuration: 100 * time.Millisecond, FlushIntervalTicks: 50,
		MaxCommandsPerTick: 256, EpochMs: epoch,
	}, nil, nil, silencio)
	return repos
}

// ADR-022: una transición de presencia que la cola descarta no se pierde: la
// compensación marca la ciudad y el siguiente volcado vuelve a encolar su
// presencia ACTUAL con su marca, no la del trabajo perdido. Aquí se descartan
// T1 (OFFLINE_PENDING) y T3 (ONLINE), y la base recibe una sola escritura: la
// del estado vigente, ONLINE con last_online_at.
func TestUnaPresenciaQueNoSePersisteSeReencolaConElEstadoActual(t *testing.T) {
	h := newHarness(t)
	repos := conPersistenciaFragil(h)
	c, _ := h.state.City(h.cityID)
	sesion := uuid.New()
	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: sesion}) // ya ONLINE: sin transición
	repos.fallaPresencia = true

	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: sesion})
	h.advance(31 * time.Second) // T1, y su escritura se descarta
	require.Equal(t, city.PresenceOfflinePending, c.PresenceState)
	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: uuid.New()}) // T3, también descartada
	require.Equal(t, city.PresenceOnline, c.PresenceState)
	require.Empty(t, repos.presenciasEscritas, "mientras la base falla no llega nada")
	require.NotEmpty(t, h.persister.errs)

	repos.fallaPresencia = false
	h.advance(5 * time.Second) // incluye un volcado periódico

	require.Len(t, repos.presenciasEscritas, 1, "una sola escritura, la del estado vigente")
	escrita := repos.presenciasEscritas[0]
	assert.Equal(t, h.cityID, escrita.cityID)
	assert.Equal(t, city.PresenceOnline, escrita.state)
	require.NotNil(t, c.LastOnlineAt)
	assert.True(t, escrita.at.Equal(*c.LastOnlineAt), "la marca es la de la conexión vigente, no la del trabajo perdido")
	assert.Equal(t, 0, h.state.PresenceDirtyCount(), "escrita: ya no está pendiente")
}

// ADR-022: si un lote del volcado periódico no se puede escribir, sus unidades
// vuelven al conjunto sucio y salen en el siguiente volcado. Antes, FlushDirty
// las sacaba del conjunto antes de saber si se habían escrito.
func TestUnVolcadoQueNoSePersisteDevuelveLasUnidadesAlConjuntoSucio(t *testing.T) {
	h := newHarness(t)
	repos := conPersistenciaFragil(h)
	for _, u := range h.units {
		h.state.MarkDirty(u.ID)
	}
	repos.fallaVolcado = true

	require.Equal(t, 3, h.sim.FlushDirty())
	require.Equal(t, 0, h.state.DirtyCount(), "el volcado drena el conjunto")
	h.loop.Step(h.clk.NowMs()) // la compensación vuelve por el buzón y se aplica
	assert.Equal(t, 3, h.state.DirtyCount(), "las tres vuelven al conjunto sucio")
	assert.Zero(t, h.repos.unitsFlushed)

	repos.fallaVolcado = false
	require.Equal(t, 3, h.sim.FlushDirty())
	assert.Equal(t, 3, h.repos.unitsFlushed)
	h.loop.Step(h.clk.NowMs())
	assert.Equal(t, 0, h.state.DirtyCount(), "escritas: no vuelven")
}

// ADR-022: un cierre de movimiento que la cola descarta —por llegada o por
// cancelación— se reintenta en el siguiente volcado SÓLO sobre la fila de
// unit_movements; la unidad la escribe el propio volcado con su posición
// actual. Sin esto, la fila quedaba ACTIVE y la unidad MOVING en la base hasta
// el reinicio, y una cancelación perdida se deshacía al rehidratar.
func TestUnCierreQueNoSePersisteSeReintentaEnElSiguienteVolcado(t *testing.T) {
	casos := []struct {
		nombre string
		cerrar func(h *harness, u *unit.Unit)
		status movement.Status
	}{
		{"llegada", func(h *harness, _ *unit.Unit) { h.advance(700 * time.Millisecond) }, movement.StatusCompleted},
		{"cancelación", func(h *harness, u *unit.Unit) {
			h.send(simulation.CancelMovement{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: u.ID})
		}, movement.StatusCancelled},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			h := newHarness(t)
			repos := conPersistenciaFragil(h)
			u := h.units[0]
			h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(),
				UnitID: u.ID, Target: world.Tile{X: u.X + 1, Y: u.Y}}) // un tile: 600 ms
			m, ok := h.state.Movement(u.ID)
			require.True(t, ok)
			repos.fallaCierre = true

			caso.cerrar(h, u)
			require.Equal(t, unit.StatusIdle, u.Status)
			h.loop.Step(h.clk.NowMs()) // la compensación se aplica
			require.Equal(t, 1, h.state.PendingFinishCount())
			require.NotContains(t, h.repos.movementsFinish, m.ID, "la base no conoce el cierre")

			repos.fallaCierre = false
			antes := h.repos.unitsFlushed
			h.advance(5 * time.Second) // incluye un volcado periódico

			assert.Equal(t, caso.status, h.repos.movementsFinish[m.ID], "la fila se cierra con su estado")
			ultimo := h.repos.finishes[len(h.repos.finishes)-1]
			assert.Equal(t, m.ID, ultimo.MovementID)
			assert.Zero(t, ultimo.UnitID, "el reintento no reescribe la unidad con la posición del cierre perdido")
			assert.Greater(t, h.repos.unitsFlushed, antes, "la unidad la escribe el volcado, con su posición actual")
			assert.Equal(t, 0, h.state.PendingFinishCount())
		})
	}
}

// particiones es un persistidor que sólo registra los trabajos, con cuatro
// particiones como la cola real.
type particiones struct {
	mu   sync.Mutex
	jobs []simulation.Job
}

func (p *particiones) Submit(job simulation.Job) {
	p.mu.Lock()
	p.jobs = append(p.jobs, job)
	p.mu.Unlock()
}
func (p *particiones) Depth() int      { return 0 }
func (p *particiones) Partitions() int { return 4 }

// loteCapturado registra qué unidades escribe cada volcado.
type loteCapturado struct {
	*fakeRepos
	lotes [][]int64
}

func (r *loteCapturado) PersistUnitPositions(_ context.Context, units []*unit.Unit) error {
	ids := make([]int64, 0, len(units))
	for _, u := range units {
		ids = append(ids, u.ID)
	}
	r.lotes = append(r.lotes, ids)
	return nil
}

// ADR-013 §2.2: el volcado se parte en un trabajo por partición, y cada unidad
// del lote cae en la partición de la clave de su trabajo. Así una unidad sigue
// en orden con sus demás escrituras aunque el volcado sea por lotes.
func TestElVolcadoSeParteEnUnTrabajoPorParticion(t *testing.T) {
	h := newHarness(t)
	p := &particiones{}
	repos := &loteCapturado{fakeRepos: h.repos}
	h.sim = simulation.New(h.state, simulation.Deps{
		Clock: h.clk, Pathfinder: pathfinding.NewAStar(20000, 256), Broadcaster: h.rec,
		Persister: p, Repos: repos, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ProtectionCooldown: 300 * time.Second, DisconnectGrace: 30 * time.Second,
	})
	for i := int64(10); i < 30; i++ {
		h.state.AddUnit(&unit.Unit{ID: i, PlayerID: h.playerID, Type: unit.TypeVillager,
			X: int32(i), Y: 30, HP: 40, MaxHP: 40, Status: unit.StatusIdle})
		h.state.MarkDirty(i)
	}

	total := h.sim.FlushDirty()
	require.Equal(t, 20, total, "sólo las veinte marcadas como sucias")
	require.Greater(t, len(p.jobs), 1, "veinte unidades se reparten en varias particiones")

	usadas := map[int]bool{}
	escritas := 0
	for _, job := range p.jobs {
		require.Equal(t, "units.flush", job.Name)
		part := simulation.PartitionOf(job.Key, 4)
		assert.False(t, usadas[part], "un solo trabajo por partición")
		usadas[part] = true

		require.NoError(t, job.Run(context.Background()))
		lote := repos.lotes[len(repos.lotes)-1]
		for _, id := range lote {
			assert.Equal(t, part, simulation.PartitionOf(simulation.UnitKey(id), 4),
				"la unidad %d va en un lote de otra partición", id)
		}
		escritas += len(lote)
	}
	assert.Equal(t, total, escritas, "ninguna unidad se pierde al partir el lote")
}
