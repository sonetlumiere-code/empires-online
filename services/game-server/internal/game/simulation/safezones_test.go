package simulation_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de simulación de Safe Zones (docs/specs/safe-zones.md §13).
//
// Mundo: el de newHarness —64 × 64 de hierba, ciudad en (10,10) y tres aldeanos
// en (12,10), (8,10) y (10,12)— con una franja de bosque de x=20 a x=22 que
// cruza el mapa de arriba abajo. Esa franja es una zona DENSE_FOREST, así que
// cualquier ruta hacia el este tiene que atravesarla.

const (
	franjaMinX = 20
	franjaMaxX = 22
)

var otroJugador = uuid.MustParse("22222222-2222-4222-8222-222222222222")

func newHarnessConZona(t *testing.T) *harness {
	t.Helper()
	h := newHarnessConTerreno(t, func(terrain []byte) {
		for y := 0; y < 64; y++ {
			for x := franjaMinX; x <= franjaMaxX; x++ {
				terrain[y*64+x] = byte(world.Forest)
			}
		}
	})
	idx, rep, err := safezone.BuildIndex([]safezone.Zone{
		{ID: 1, Name: "Bosque de prueba", Type: safezone.DenseForest,
			MinX: franjaMinX, MinY: 0, MaxX: franjaMaxX, MaxY: 63},
	}, h.state.World())
	require.NoError(t, err)
	require.Empty(t, rep.Overlaps)
	h.state.SetSafeZones(idx)

	// El primer tick evalúa a los aldeanos iniciales, que están fuera de la
	// zona: no debe pasar nada. Se descarta lo que haya emitido.
	h.loop.Step(h.clk.NowMs())
	h.rec.reset()
	return h
}

// ponerUnidad incorpora una unidad en reposo. AddUnit la apunta a la siguiente
// evaluación, igual que hace la hidratación tras un reinicio.
func (h *harness) ponerUnidad(id int64, owner uuid.UUID, at world.Tile, status unit.Status, hp int32) *unit.Unit {
	h.t.Helper()
	u := &unit.Unit{
		ID: id, PlayerID: owner, Type: unit.TypeVillager,
		X: at.X, Y: at.Y, HP: hp, MaxHP: 40, Status: status,
	}
	h.state.AddUnit(u)
	return u
}

func (h *harness) mover(unitID int64, to world.Tile) {
	h.t.Helper()
	h.send(simulation.MoveUnit{
		PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: unitID, Target: to,
	})
}

// verificarOcultamiento comprueba INV-SAFE-003 / INV-UNIT-008 sobre TODO el
// mundo: toda unidad HIDDEN está en una zona y sin movimiento ACTIVE.
func (h *harness) verificarOcultamiento() {
	h.t.Helper()
	h.state.EachUnit(func(u *unit.Unit) bool {
		if u.Status != unit.StatusHidden {
			return true
		}
		_, enZona := h.state.SafeZoneAt(u.X, u.Y)
		_, moviendo := h.state.Movement(u.ID)
		require.True(h.t, enZona, "INV-SAFE-003: la unidad %d está HIDDEN fuera de zona en %v", u.ID, u.Tile())
		require.False(h.t, moviendo, "INV-SAFE-003: la unidad %d está HIDDEN con un movimiento ACTIVE", u.ID)
		return true
	})
}

func statusOf(t *testing.T, payload any) string {
	t.Helper()
	p, ok := payload.(protocol.EntityUpdatePayload)
	require.True(t, ok, "se esperaba EntityUpdatePayload, llegó %T", payload)
	require.NotNil(t, p.Status)
	return *p.Status
}

// ─────────────────────────────────────────────────────────────

// INV-SAFE-003 e INV-SAFE-004 en su forma más simple: una unidad en reposo
// dentro de la zona se oculta en el siguiente tick. Su dueño recibe el cambio de
// estado; los demás, un despawn que excluye explícitamente al dueño.
func TestUnaUnidadEnReposoDentroDeLaZonaSeOcultaTrasUnTick(t *testing.T) {
	h := newHarnessConZona(t)
	u := h.ponerUnidad(10, h.playerID, world.Tile{X: 21, Y: 10}, unit.StatusIdle, 40)

	h.loop.Step(h.clk.NowMs())

	assert.Equal(t, unit.StatusHidden, u.Status)
	h.verificarOcultamiento()

	updates := h.rec.byType(protocol.TypeEntityUpdate)
	require.Len(t, updates, 1)
	assert.Equal(t, "player", updates[0].Kind)
	assert.Equal(t, h.playerID, updates[0].PlayerID)
	assert.Equal(t, "HIDDEN", statusOf(t, updates[0].Payload))

	despawns := h.rec.byType(protocol.TypeEntityDespawn)
	require.Len(t, despawns, 1)
	assert.Equal(t, "chunk-except", despawns[0].Kind, "el despawn NO puede ir a todo el chunk")
	assert.Equal(t, h.playerID, despawns[0].PlayerID, "se excluye al propietario")
	assert.Equal(t, protocol.EntityDespawnPayload{ID: 10, Reason: protocol.DespawnHidden}, despawns[0].Payload)

	assert.Empty(t, h.rec.byType(protocol.TypeEntitySpawn))
	assert.Contains(t, h.state.DrainDirty(), u, "el estado HIDDEN se persiste por dirty-flag")
}

// RN-SAFE-010: atravesar la zona sin detenerse no oculta en ningún tick.
func TestAtravesarLaZonaSinDetenerseNoOcultaNunca(t *testing.T) {
	h := newHarnessConZona(t)
	u := h.units[0] // (12,10)
	destino := world.Tile{X: 30, Y: 10}
	h.mover(u.ID, destino)
	require.Equal(t, unit.StatusMoving, u.Status)

	pisoLaZona := false
	for i := 0; i < 400 && u.Status == unit.StatusMoving; i++ {
		h.advance(100 * time.Millisecond) // un tick
		h.verificarOcultamiento()
		require.NotEqual(t, unit.StatusHidden, u.Status, "tick %d: oculta en tránsito en %v", i, u.Tile())
		if _, ok := h.state.SafeZoneAt(u.X, u.Y); ok {
			pisoLaZona = true
		}
	}
	require.True(t, pisoLaZona, "el escenario no prueba nada si la ruta no cruza la zona")
	assert.Equal(t, unit.StatusIdle, u.Status)
	assert.Equal(t, destino, u.Tile())
	assert.Empty(t, h.rec.byType(protocol.TypeEntityDespawn))
}

// Terminar el movimiento dentro de la zona: en el tick de llegada la fase 3 la
// deja IDLE y la fase 5 la oculta. Nunca hay un salto MOVING → HIDDEN: el
// completed se emite antes que el cambio a HIDDEN.
func TestTerminarElMovimientoDentroDeLaZonaPasaPorIdleYLaOculta(t *testing.T) {
	h := newHarnessConZona(t)
	u := h.units[0]
	h.mover(u.ID, world.Tile{X: 21, Y: 10})

	for i := 0; i < 200 && u.Status == unit.StatusMoving; i++ {
		h.advance(100 * time.Millisecond)
		h.verificarOcultamiento()
	}
	require.Equal(t, unit.StatusHidden, u.Status)

	orden := make([]string, 0)
	for _, m := range h.rec.messages {
		switch m.Type {
		case protocol.TypeUnitMovementCompleted, protocol.TypeEntityDespawn:
			orden = append(orden, m.Type)
		case protocol.TypeEntityUpdate:
			if p, ok := m.Payload.(protocol.EntityUpdatePayload); ok && p.Status != nil {
				orden = append(orden, m.Type+":"+*p.Status)
			}
		}
	}
	assert.Equal(t, []string{
		protocol.TypeUnitMovementCompleted,
		protocol.TypeEntityUpdate + ":HIDDEN",
		protocol.TypeEntityDespawn,
	}, orden)
}

// RN-SAFE-013: una unidad oculta que recibe unit.move pasa a MOVING en ese
// mismo tick, sin IDLE observable, y los terceros la reciben ya en movimiento
// antes que su unit.movement.started.
func TestUnaUnidadOcultaQueRecibeUnMoveSeRevelaEnElMismoTick(t *testing.T) {
	h := newHarnessConZona(t)
	u := h.ponerUnidad(10, h.playerID, world.Tile{X: 21, Y: 10}, unit.StatusIdle, 40)
	h.loop.Step(h.clk.NowMs())
	require.Equal(t, unit.StatusHidden, u.Status)
	h.rec.reset()

	h.mover(u.ID, world.Tile{X: 30, Y: 10})

	assert.Equal(t, unit.StatusMoving, u.Status)
	h.verificarOcultamiento()

	var tipos []string
	for _, m := range h.rec.messages {
		tipos = append(tipos, m.Kind+":"+m.Type)
		if m.Type == protocol.TypeEntityUpdate {
			p := m.Payload.(protocol.EntityUpdatePayload)
			assert.Nil(t, p.Status, "no hay paso por IDLE observable")
		}
	}
	assert.Equal(t, []string{
		"player:" + protocol.TypeUnitMoveAccepted,
		"chunk-except:" + protocol.TypeEntitySpawn,
		"chunk:" + protocol.TypeUnitMovementStarted,
	}, tipos)

	spawn := h.rec.byType(protocol.TypeEntitySpawn)[0]
	assert.Equal(t, h.playerID, spawn.PlayerID, "el spawn excluye al propietario, que nunca dejó de verla")
	vista := spawn.Payload.(protocol.EntitySpawnPayload).Unit
	assert.Equal(t, "MOVING", vista.Status)
	require.NotNil(t, vista.Movement, "el tercero recibe la polilínea con la unidad")
}

// RN-SAFE-014, en la forma verificable en que ha quedado la regla: ocultar no
// toca la capa de ocupación ni la transitabilidad, así que las rutas que
// calcula el pathfinder son las mismas antes y después. Se filtra el canal de
// visibilidad, nunca el de simulación.
func TestOcultarNoAlteraLaTransitabilidadNiLasRutas(t *testing.T) {
	h := newHarnessConZona(t)
	w := h.state.World()
	astar := pathfinding.NewAStar(20000, 256)

	fotografia := func() ([]bool, []world.Tile) {
		walk := make([]bool, 0, 64*64)
		for y := int32(0); y < 64; y++ {
			for x := int32(0); x < 64; x++ {
				walk = append(walk, w.IsWalkable(x, y))
			}
		}
		ruta, err := astar.FindPath(context.Background(), w,
			world.Tile{X: 12, Y: 20}, world.Tile{X: 30, Y: 20}, pathfinding.Options{MaxNodes: 20000, MaxDistance: 256})
		require.NoError(t, err)
		return walk, ruta
	}

	antesWalk, antesRuta := fotografia()
	u := h.ponerUnidad(10, otroJugador, world.Tile{X: 21, Y: 20}, unit.StatusIdle, 40)
	h.loop.Step(h.clk.NowMs())
	require.Equal(t, unit.StatusHidden, u.Status)
	despuesWalk, despuesRuta := fotografia()

	assert.Equal(t, antesWalk, despuesWalk)
	assert.Equal(t, antesRuta, despuesRuta)
	assert.Contains(t, h.state.UnitsInChunk(w.ChunkOf(21, 20)), u,
		"la unidad oculta sigue en el índice espacial de la simulación")
}

// INV-SAFE-004 en el snapshot: un snapshot es un delta completo y aplica el
// mismo filtro. El tercero no recibe la unidad oculta; su dueño sí, con su
// estado real.
func TestElSnapshotDeUnTerceroNoIncluyeUnidadesOcultas(t *testing.T) {
	h := newHarnessConZona(t)
	h.ponerUnidad(10, h.playerID, world.Tile{X: 21, Y: 10}, unit.StatusIdle, 40)
	h.ponerUnidad(11, otroJugador, world.Tile{X: 25, Y: 10}, unit.StatusIdle, 40)
	h.loop.Step(h.clk.NowMs())

	chunks := h.state.World().ChunksInRadius(world.Tile{X: 21, Y: 10}, 2)
	ids := func(viewer uuid.UUID) map[int64]string {
		out := map[int64]string{}
		for _, v := range h.sim.BuildSnapshot(viewer, chunks, h.clk.NowMs(), false).Units {
			out[v.ID] = v.Status
		}
		return out
	}

	propio := ids(h.playerID)
	assert.Equal(t, "HIDDEN", propio[10])
	assert.Contains(t, propio, int64(11))

	ajeno := ids(otroJugador)
	assert.NotContains(t, ajeno, int64(10), "una unidad oculta no puede viajar a un tercero")
	assert.Contains(t, ajeno, int64(11))
	assert.Contains(t, ajeno, int64(1), "las unidades visibles del primer jugador sí viajan")
}

// Recuperación (spec §10): HIDDEN se persiste sólo por dirty-flag, así que tras
// una caída la base puede no reflejarlo. La primera fase 5 tras el arranque
// restablece el estado desde la geometría, en los dos sentidos.
func TestTrasUnReinicioLaPrimeraFase5RestableceElOcultamiento(t *testing.T) {
	h := newHarnessConZona(t)

	desdeBase := []*unit.Unit{
		// En zona pero persistida IDLE: el HIDDEN no llegó a volcarse.
		{ID: 20, PlayerID: h.playerID, Type: unit.TypeVillager, X: 21, Y: 30, HP: 40, MaxHP: 40, Status: unit.StatusIdle},
		// HIDDEN fuera de toda zona: la zona dejó de existir (transición T8).
		{ID: 21, PlayerID: h.playerID, Type: unit.TypeVillager, X: 40, Y: 30, HP: 40, MaxHP: 40, Status: unit.StatusHidden},
		// HIDDEN y en zona: nada que cambiar, nada que emitir.
		{ID: 22, PlayerID: h.playerID, Type: unit.TypeVillager, X: 20, Y: 30, HP: 40, MaxHP: 40, Status: unit.StatusHidden},
	}
	simulation.Hydrate(h.state, desdeBase, nil, nil, h.clk.NowMs(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	h.loop.Step(h.clk.NowMs())
	h.verificarOcultamiento()

	estado := func(id int64) unit.Status { u, _ := h.state.Unit(id); return u.Status }
	assert.Equal(t, unit.StatusHidden, estado(20))
	assert.Equal(t, unit.StatusIdle, estado(21))
	assert.Equal(t, unit.StatusHidden, estado(22))

	revelada := h.rec.byType(protocol.TypeEntitySpawn)
	require.Len(t, revelada, 1, "sólo la 21 se revela ante terceros")
	assert.EqualValues(t, 21, revelada[0].Payload.(protocol.EntitySpawnPayload).Unit.ID)
	for _, m := range h.rec.messages {
		if p, ok := m.Payload.(protocol.EntityUpdatePayload); ok {
			assert.NotEqual(t, int64(22), p.ID, "la 22 ya estaba bien y no genera tráfico")
		}
	}
}

// Una unidad guarnecida, una muerta y una sin vida nunca se ocultan aunque
// estén sobre la zona (P5 de unit.md, y la condición hp > 0 de §6.4).
func TestGuarnecidasYSinVidaNoSeOcultan(t *testing.T) {
	h := newHarnessConZona(t)
	guarnecida := h.ponerUnidad(30, h.playerID, world.Tile{X: 21, Y: 40}, unit.StatusGarrisoned, 40)
	muerta := h.ponerUnidad(31, h.playerID, world.Tile{X: 21, Y: 41}, unit.StatusDead, 0)
	sinVida := h.ponerUnidad(32, h.playerID, world.Tile{X: 21, Y: 42}, unit.StatusIdle, 0)

	h.loop.Step(h.clk.NowMs())

	assert.Equal(t, unit.StatusGarrisoned, guarnecida.Status)
	assert.Equal(t, unit.StatusDead, muerta.Status)
	assert.Equal(t, unit.StatusIdle, sinVida.Status)
	assert.Empty(t, h.rec.messages)
}

// Fundar una ciudad sobre la zona: su muralla deja de ser refugio
// (INV-SAFE-002 en runtime) y una unidad oculta en un tile amurallado se revela
// en la fase 5 de ese mismo tick.
func TestFundarSobreLaZonaRetiraLaMurallaYRevelaLoQueQuedaDentro(t *testing.T) {
	h := newHarnessConZona(t)
	atrapada := h.ponerUnidad(40, otroJugador, world.Tile{X: 21, Y: 51}, unit.StatusIdle, 40)
	h.loop.Step(h.clk.NowMs())
	require.Equal(t, unit.StatusHidden, atrapada.Status)
	h.rec.reset()

	h.send(simulation.IntroducePlayer{
		City: &city.City{
			ID: 99, OwnerPlayerID: uuid.New(), Name: "Sobre el bosque",
			CenterX: 21, CenterY: 50, Era: city.EraStone, PopulationLimit: 20,
			PresenceState: city.PresenceOnline,
		},
		BlockedMinX: 20, BlockedMinY: 49, BlockedMaxX: 22, BlockedMaxY: 51,
	})

	for y := int32(49); y <= 51; y++ {
		for x := int32(20); x <= 22; x++ {
			_, ok := h.state.SafeZoneAt(x, y)
			assert.False(t, ok, "(%d,%d) está amurallado y no puede seguir en el índice", x, y)
		}
	}
	_, sigue := h.state.SafeZoneAt(21, 40)
	assert.True(t, sigue, "el resto de la zona sigue funcionando")

	assert.Equal(t, unit.StatusIdle, atrapada.Status)
	h.verificarOcultamiento()
}

// Comprobación de humo sobre el harness: sin zonas instaladas —el caso del
// mundo canónico mientras la tabla esté vacía— nada cambia de estado.
func TestSinZonasNadieSeOculta(t *testing.T) {
	h := newHarness(t)
	h.loop.Step(h.clk.NowMs())
	h.state.EachUnit(func(u *unit.Unit) bool {
		assert.Equal(t, unit.StatusIdle, u.Status)
		return true
	})
}
