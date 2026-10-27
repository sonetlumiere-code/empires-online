package simulation_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/production"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Simulación de la economía (SPEC-ECON-WOOD §9, «simulation»). La ciudad de
// newHarness está en (10,10); su depósito es el primer tile del anillo de
// nacimiento, (12,10), donde empieza el aldeano 1. El bosque es la franja
// x = 22..23: el tile (22,10) está a diez pasos del depósito.
//
// Línea temporal de un viaje, desde una orden en `epoch`:
//
//	ida      9 pasos de hierba (600 ms) + 1 de bosque (960 ms) = 6 360 ms
//	trabajo  10 000 ms                                          → 16 360
//	vuelta   10 pasos de hierba                     = 6 000 ms  → 22 360
//
// Los vencimientos son instantes absolutos; el tick que los observa es el
// primero en o tras ellos (múltiplos de 100 ms desde `epoch`).

var (
	bosque   = world.Tile{X: 22, Y: 10}
	deposito = world.Tile{X: 12, Y: 10}
	sesion   = uuid.MustParse("22222222-2222-4222-8222-222222222222")
)

func newHarnessMadera(t *testing.T) *harness {
	t.Helper()
	return newHarnessConTerreno(t, func(terrain []byte) {
		for y := 0; y < 64; y++ {
			terrain[y*64+22] = byte(world.Forest)
			terrain[y*64+23] = byte(world.Forest)
		}
	})
}

func (h *harness) conectar() {
	h.t.Helper()
	h.send(simulation.PlayerConnected{PlayerID: h.playerID, SessionID: sesion})
}

func (h *harness) darMadera(n int64) {
	h.t.Helper()
	h.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: h.cityID, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: n}},
	}})
}

// hasta ejecuta ticks hasta que el reloj alcance `ms` (incluido).
func (h *harness) hasta(ms int64) {
	h.t.Helper()
	for h.clk.NowMs() < ms {
		h.clk.AdvanceMs(100)
		h.loop.Step(h.clk.NowMs())
	}
}

// tick ejecuta un tick más: lo que la cola devolvió por el buzón se aplica.
func (h *harness) tick() {
	h.t.Helper()
	h.clk.AdvanceMs(100)
	h.loop.Step(h.clk.NowMs())
}

func (h *harness) recolectar(unitID int64, target world.Tile) {
	h.t.Helper()
	h.send(simulation.GatherWood{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: unitID, Target: target})
}

func (h *harness) codigos() []string {
	var out []string
	for _, m := range h.rec.byType(protocol.TypeSystemError) {
		out = append(out, m.Payload.(protocol.SystemErrorPayload).Code)
	}
	return out
}

func (h *harness) madera() int64 { return h.state.Stock(h.cityID).Amount }

func (h *harness) ciudad() *city.City {
	c, ok := h.state.City(h.cityID)
	require.True(h.t, ok)
	return c
}

func stocksDe(t *testing.T, msgs []capturedMessage) []int64 {
	t.Helper()
	var out []int64
	for _, m := range msgs {
		if p := m.Payload.(protocol.CityUpdatePayload); p.Stocks != nil {
			out = append(out, p.Stocks.Wood)
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────
// Recolección
// ─────────────────────────────────────────────────────────────

// RN-ECON-002, RN-ECON-003 y RN-ECON-004: un viaje completo, con cada fase
// anclada al instante exacto del hecho anterior y escrita en su transacción.
func TestUnViajeDeRecoleccionDepositaDiezEnElInstantePrevisto(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	h.recolectar(1, bosque)

	started := h.rec.byType(protocol.TypeUnitMovementStarted)
	require.Len(t, started, 1)
	ida := started[0].Payload.(protocol.UnitMovementStartedPayload).Movement
	require.Equal(t, epoch+6_360, ida.ArrivalTimeMs, "nueve pasos de hierba y uno de bosque")
	tk, ok := h.state.Task(1)
	require.True(t, ok)
	assert.Equal(t, task.PhaseToResource, tk.Phase)
	escrita := h.economy.lastTask()
	require.NotNil(t, escrita.Start, "el tramo se escribe con la tarea")
	assert.Equal(t, unit.StatusMoving, escrita.Status)

	// Llega en 6 360: el tick de 6 400 lo observa, y el trabajo vence en
	// 16 360, no en 16 400.
	h.hasta(epoch + 6_400)
	u, _ := h.state.Unit(1)
	require.Equal(t, unit.StatusWorking, u.Status)
	require.Equal(t, bosque, u.Tile())
	tk, _ = h.state.Task(1)
	require.Equal(t, task.PhaseWorking, tk.Phase)
	require.Equal(t, epoch+16_360, tk.PhaseUntilMs, "INV-ECON-005: anclado a la llegada exacta")
	escrita = h.economy.lastTask()
	require.NotNil(t, escrita.Finish, "el cierre del tramo va en la misma transacción")
	assert.Equal(t, movement.StatusCompleted, escrita.Finish.Status)
	assert.Equal(t, unit.StatusWorking, escrita.Status)

	// 16 360 → sale con diez de madera, tramo que empieza en 16 360.
	h.hasta(epoch + 16_300)
	tk, _ = h.state.Task(1)
	require.Equal(t, task.PhaseWorking, tk.Phase, "a 16 300 sigue trabajando")
	h.hasta(epoch + 16_400)
	tk, _ = h.state.Task(1)
	require.Equal(t, task.PhaseToDeposit, tk.Phase)
	require.Equal(t, int32(10), tk.Carrying)
	vuelta, ok := h.state.Movement(1)
	require.True(t, ok)
	require.Equal(t, epoch+16_360, vuelta.StartTimeMs)
	require.Equal(t, epoch+22_360, vuelta.ArrivalTimeMs)
	require.Equal(t, deposito, vuelta.Target)

	// 22 360 → deposita y, con el dueño presente, vuelve al bosque desde ese
	// mismo instante.
	h.rec.reset()
	h.hasta(epoch + 22_400)
	assert.Equal(t, int64(10), h.madera())
	assert.Equal(t, []int64{10}, stocksDe(t, h.rec.byType(protocol.TypeCityUpdate)))
	tk, _ = h.state.Task(1)
	assert.Equal(t, task.PhaseToResource, tk.Phase)
	assert.Equal(t, int32(0), tk.Carrying)
	assert.Equal(t, int32(1), tk.TripsDone)
	siguiente, ok := h.state.Movement(1)
	require.True(t, ok)
	assert.Equal(t, epoch+22_360, siguiente.StartTimeMs)
	assert.Equal(t, int64(10), h.state.UnflushedDeposits(h.cityID), "el depósito se vuelca por dirty-flag")
}

// El depósito se vuelca en la fase 8 como delta versionado (ADR-023 §1); si la
// escritura se pierde, el delta vuelve a la ciudad y sale en el siguiente.
func TestLosDepositosSeVuelcanYUnVolcadoPerdidoSeReintenta(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	h.recolectar(1, bosque)
	h.hasta(epoch + 22_400) // primer depósito; el volcado de 50 ticks ya pasó varias veces
	h.hasta(epoch + 25_000) // tick 250: hay volcado
	require.Len(t, h.economy.flushes, 1)
	d := h.economy.flushes[0]
	assert.Equal(t, int64(10), d.Delta)
	assert.Equal(t, h.state.Stock(h.cityID).Version, d.Version)
	assert.Equal(t, int64(0), h.state.UnflushedDeposits(h.cityID))

	// Una escritura de depósitos que falla vuelve a la ciudad. El segundo
	// depósito llega en 44 720 y el volcado del mismo tick falla.
	h.persister.permanent = true
	h.economy.flushErr = errors.New("base caída")
	h.hasta(epoch + 45_000)
	assert.Equal(t, int64(10), h.state.UnflushedDeposits(h.cityID), "el delta perdido vuelve a la ciudad")
	h.economy.flushErr = nil
	h.hasta(epoch + 50_000)
	require.Len(t, h.economy.flushes, 2)
	assert.Equal(t, int64(10), h.economy.flushes[1].Delta)
	assert.Greater(t, h.economy.flushes[1].Version, d.Version+1,
		"la compensación sube la versión: el reintento supera a cualquier escritura intermedia")
}

// INV-ECON-006 y RN-ECON-005: con el dueño desconectado, el viaje en curso se
// termina —un depósito más— y la unidad queda IDLE en el depósito, sin
// empezar otro. El margen de reconexión (30 s) aún cuenta como presente.
func TestConElDuenoAusenteElViajeTerminaYNoEmpiezaOtro(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	h.recolectar(1, bosque)
	h.hasta(epoch + 1_000)
	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: sesion})

	// Primer depósito en 22 360: dentro del margen, el viaje siguiente empieza.
	h.hasta(epoch + 22_400)
	tk, ok := h.state.Task(1)
	require.True(t, ok, "dentro del margen de reconexión el dueño sigue presente")
	require.Equal(t, task.PhaseToResource, tk.Phase)

	// En 31 s la ciudad pasa a OFFLINE_PENDING; el viaje en curso termina con
	// su depósito en 44 720, y ahí se acaba.
	h.hasta(epoch + 31_100)
	require.Equal(t, city.PresenceOfflinePending, h.ciudad().PresenceState)
	h.rec.reset()
	h.hasta(epoch + 44_800)
	assert.Equal(t, int64(20), h.madera(), "un depósito más")
	_, ok = h.state.Task(1)
	assert.False(t, ok, "la tarea terminó")
	u, _ := h.state.Unit(1)
	assert.Equal(t, unit.StatusIdle, u.Status)
	assert.Equal(t, deposito, u.Tile())
	for _, m := range h.rec.byType(protocol.TypeUnitMovementStarted) {
		destino := m.Payload.(protocol.UnitMovementStartedPayload).Movement.Target
		assert.Equal(t, protocol.Tile{X: deposito.X, Y: deposito.Y}, destino,
			"sólo la vuelta del viaje en curso: ningún tramo nuevo al bosque")
	}
	fin := h.economy.lastTask()
	assert.Nil(t, fin.Task, "la transacción borra la tarea")
	assert.Equal(t, unit.StatusIdle, fin.Status)

	h.hasta(epoch + 90_000)
	assert.Equal(t, int64(20), h.madera(), "la ausencia no acumula recursos")
}

// INV-ECON-006, segunda mitad: una ciudad ONLINE sin nadie conectado —lo que
// deja un reinicio, presence.md §7.6— tampoco empieza viajes nuevos.
func TestUnaCiudadOnlineSinSesionNoEmpiezaViajesNuevos(t *testing.T) {
	h := newHarnessMadera(t)
	require.Equal(t, city.PresenceOnline, h.ciudad().PresenceState)
	h.recolectar(1, bosque)
	h.hasta(epoch + 22_400)
	assert.Equal(t, int64(10), h.madera())
	_, ok := h.state.Task(1)
	assert.False(t, ok)
	u, _ := h.state.Unit(1)
	assert.Equal(t, unit.StatusIdle, u.Status)
}

// RN-ECON-003: una orden de movimiento cancela la tarea y pierde lo cargado.
func TestUnaOrdenDeMovimientoCancelaLaTareaYPierdeLaCarga(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	h.recolectar(1, bosque)
	h.hasta(epoch + 17_000) // de vuelta, con diez encima
	tk, _ := h.state.Task(1)
	require.Equal(t, int32(10), tk.Carrying)

	h.rec.reset()
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: 1, Target: world.Tile{X: 15, Y: 20}})
	_, ok := h.state.Task(1)
	assert.False(t, ok)
	updates := h.rec.byType(protocol.TypeEntityUpdate)
	require.NotEmpty(t, updates)
	carga := updates[0].Payload.(protocol.EntityUpdatePayload).Carrying
	require.NotNil(t, carga)
	assert.Equal(t, int32(0), *carga, "la carga se pierde a la vista de todos")

	h.hasta(epoch + 60_000)
	assert.Equal(t, int64(0), h.madera(), "lo cargado no llegó a la ciudad")
}

// RN-ECON-003: unit.cancel_move detiene a una unidad que trabaja; la base lo
// sabe con un cierre sin movimiento, que borra la tarea.
func TestCancelarDetieneAUnaUnidadQueTrabaja(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	h.recolectar(1, bosque)
	h.hasta(epoch + 7_000)
	u, _ := h.state.Unit(1)
	require.Equal(t, unit.StatusWorking, u.Status)

	h.send(simulation.CancelMovement{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: 1})
	assert.Equal(t, unit.StatusIdle, u.Status)
	_, ok := h.state.Task(1)
	assert.False(t, ok)
	fin := h.repos.finishes[len(h.repos.finishes)-1]
	assert.Equal(t, int64(0), fin.MovementID)
	assert.Equal(t, bosque, fin.At)

	h.hasta(epoch + 30_000)
	assert.Equal(t, unit.StatusIdle, u.Status, "el temporizador de una tarea cancelada no la despierta")
}

// RN-ECON-006: un unit.gather nuevo sustituye la tarea, con su tramo y su carga.
func TestUnGatherNuevoSustituyeLaTarea(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	h.recolectar(1, bosque)
	h.hasta(epoch + 17_000)
	otro := world.Tile{X: 23, Y: 20}
	h.recolectar(1, otro)
	tk, ok := h.state.Task(1)
	require.True(t, ok)
	assert.Equal(t, otro, tk.Target)
	assert.Equal(t, int32(0), tk.Carrying)
	assert.Equal(t, task.PhaseToResource, tk.Phase)
	assert.Equal(t, 1, h.state.TaskCount(), "INV-ECON-003: una tarea por unidad")
	m, ok := h.state.Movement(1)
	require.True(t, ok)
	assert.Equal(t, otro, m.Target)
}

// Recolectar sobre el propio tile de bosque empieza a trabajar sin tramo.
func TestRecolectarDondeYaEstaEmpiezaATrabajar(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	u, _ := h.state.Unit(1)
	h.state.MoveUnitTo(u, bosque)
	h.recolectar(1, bosque)
	assert.Equal(t, unit.StatusWorking, u.Status)
	tk, _ := h.state.Task(1)
	assert.Equal(t, epoch+10_000, tk.PhaseUntilMs)
}

// RN-ECON-001 y RN-ECON-002: los rechazos de unit.gather, con los códigos de
// unit.move y sin gastar pathfinding en lo que se rechaza antes.
func TestRechazosDeRecoleccion(t *testing.T) {
	h := newHarnessConTerreno(t, func(terrain []byte) {
		terrain[30*64+40] = byte(world.Forest)
		for _, d := range [][2]int{{-1, -1}, {0, -1}, {1, -1}, {-1, 0}, {1, 0}, {-1, 1}, {0, 1}, {1, 1}} {
			terrain[(30+d[1])*64+40+d[0]] = byte(world.Water)
		}
		terrain[10*64+22] = byte(world.Forest)
	})
	ajeno := uuid.New()
	h.ponerUnidad(50, ajeno, world.Tile{X: 5, Y: 5}, unit.StatusIdle, 40)
	h.ponerUnidad(51, h.playerID, world.Tile{X: 6, Y: 5}, unit.StatusDead, 0)
	h.ponerUnidad(52, h.playerID, world.Tile{X: 7, Y: 5}, unit.StatusGarrisoned, 40)

	casos := []struct {
		nombre string
		unidad int64
		target world.Tile
		code   string
	}{
		{"no existe", 999, world.Tile{X: 22, Y: 10}, protocol.CodeUnitNotFound},
		{"ajena", 50, world.Tile{X: 22, Y: 10}, protocol.CodeUnitNotOwned},
		{"muerta", 51, world.Tile{X: 22, Y: 10}, protocol.CodeUnitDead},
		{"guarnecida", 52, world.Tile{X: 22, Y: 10}, protocol.CodeUnitGarrisoned},
		{"hierba", 1, world.Tile{X: 21, Y: 10}, protocol.CodeInvalidTarget},
		{"fuera del mundo", 1, world.Tile{X: 64, Y: 10}, protocol.CodeTargetOutOfBounds},
		{"bosque sin ruta", 1, world.Tile{X: 40, Y: 30}, protocol.CodePathNotFound},
	}
	for _, c := range casos {
		h.rec.reset()
		h.send(simulation.GatherWood{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: c.unidad, Target: c.target})
		assert.Equal(t, []string{c.code}, h.codigos(), c.nombre)
	}
	assert.Equal(t, 0, h.state.TaskCount())
}

// ─────────────────────────────────────────────────────────────
// Casa
// ─────────────────────────────────────────────────────────────

func (h *harness) construir(tile world.Tile) string {
	h.t.Helper()
	req := uuid.NewString()
	h.send(simulation.BuildBuilding{PlayerID: h.playerID, RequestID: req, CityID: h.cityID, Type: building.TypeHouse, Tile: tile})
	return req
}

// RN-ECON-007 y RN-ECON-008: el tick valida y reserva; la transacción debita y
// escribe; la respuesta vuelve por el buzón y sólo entonces se anuncia.
func TestConstruirUnaCasaEnDosFases(t *testing.T) {
	h := newHarness(t)
	h.darMadera(40)
	hueco := world.Tile{X: 9, Y: 9}

	h.construir(hueco)
	assert.Equal(t, int64(10), h.madera(), "la madera queda reservada en el tick")
	assert.Empty(t, h.rec.byType(protocol.TypeBuildingSpawn), "nada se anuncia antes de existir")
	require.Len(t, h.economy.builds, 1)
	orden := h.economy.builds[0]
	assert.Equal(t, int64(-30), orden.Stock.Delta)
	assert.Equal(t, building.StatusUnderConstruction, orden.Building.Status)
	assert.Equal(t, epoch+15_000, orden.Building.CompletesAtMs)

	h.tick()
	spawns := h.rec.byType(protocol.TypeBuildingSpawn)
	require.Len(t, spawns, 1)
	b := spawns[0].Payload.(protocol.BuildingSpawnPayload).Building
	assert.Equal(t, int64(501), b.ID)
	assert.Equal(t, string(building.TypeHouse), b.Type)
	assert.Equal(t, []int64{10}, stocksDe(t, h.rec.byType(protocol.TypeCityUpdate)))
	_, ok := h.state.BuildingAt(hueco)
	assert.True(t, ok)
}

// ADR-023 §1: los depósitos sin volcar viajan con el encargo, en su
// transacción; el delta que se escribe es «depósitos − coste».
func TestElEncargoLlevaLosDepositosSinVolcar(t *testing.T) {
	h := newHarnessMadera(t)
	h.darMadera(25)
	h.conectar()
	h.recolectar(1, bosque)
	h.hasta(epoch + 22_400) // +10 sin volcar todavía: el volcado ya pasó en 20 000
	require.Equal(t, int64(10), h.state.UnflushedDeposits(h.cityID))
	h.construir(world.Tile{X: 11, Y: 11})
	require.Len(t, h.economy.builds, 1)
	assert.Equal(t, int64(10-30), h.economy.builds[0].Stock.Delta)
	assert.Equal(t, int64(0), h.state.UnflushedDeposits(h.cityID))
	assert.Equal(t, int64(5), h.madera())
}

// INV-ECON-002 en la simulación: si la transacción rechaza el encargo, la
// madera reservada y los depósitos que viajaban con él vuelven a la ciudad.
func TestUnEncargoRechazadoDevuelveLaMadera(t *testing.T) {
	h := newHarness(t)
	h.darMadera(40)
	h.economy.buildErr = economy.ErrInsufficientResources
	h.construir(world.Tile{X: 9, Y: 9})
	assert.Equal(t, int64(10), h.madera())
	h.tick()
	assert.Equal(t, int64(40), h.madera())
	assert.Equal(t, []string{protocol.CodeInsufficientResources}, h.codigos())
	assert.Equal(t, []int64{40}, stocksDe(t, h.rec.byType(protocol.TypeCityUpdate)))

	// El tile vuelve a estar libre: el mismo encargo, ya sin rechazo, entra.
	h.economy.buildErr = nil
	h.rec.reset()
	h.construir(world.Tile{X: 9, Y: 9})
	h.tick()
	assert.Len(t, h.rec.byType(protocol.TypeBuildingSpawn), 1)
}

// Un fallo definitivo de la base se responde INTERNAL_ERROR y también devuelve
// la reserva (ADR-022).
func TestUnFalloDefinitivoDelEncargoDevuelveLaReserva(t *testing.T) {
	h := newHarness(t)
	h.darMadera(40)
	h.persister.permanent = true
	h.economy.buildErr = errors.New("conexión perdida")
	h.construir(world.Tile{X: 9, Y: 9})
	h.tick()
	assert.Equal(t, int64(40), h.madera())
	assert.Equal(t, []string{protocol.CodeInternalError}, h.codigos())
}

// RN-ECON-007: los rechazos en el tick, en el orden de la spec.
func TestRechazosDeConstruccion(t *testing.T) {
	h := newHarness(t)
	h.darMadera(100)
	ajeno := uuid.New()
	casos := []struct {
		nombre string
		player uuid.UUID
		city   int64
		tile   world.Tile
		code   string
	}{
		{"ciudad ajena", ajeno, h.cityID, world.Tile{X: 9, Y: 9}, protocol.CodeCityNotFound},
		{"ciudad inexistente", h.playerID, 77, world.Tile{X: 9, Y: 9}, protocol.CodeCityNotFound},
		{"centro", h.playerID, h.cityID, world.Tile{X: 10, Y: 10}, protocol.CodeInvalidTarget},
		{"fuera del 3×3", h.playerID, h.cityID, world.Tile{X: 12, Y: 10}, protocol.CodeInvalidTarget},
	}
	for _, c := range casos {
		h.rec.reset()
		h.send(simulation.BuildBuilding{PlayerID: c.player, RequestID: uuid.NewString(), CityID: c.city, Type: building.TypeHouse, Tile: c.tile})
		assert.Equal(t, []string{c.code}, h.codigos(), c.nombre)
	}

	// Un tile con encargo pendiente ya está ocupado.
	h.rec.reset()
	h.construir(world.Tile{X: 9, Y: 9})
	h.construir(world.Tile{X: 9, Y: 9})
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())

	h.rec.reset()
	h.darMadera(29)
	h.construir(world.Tile{X: 11, Y: 9})
	assert.Equal(t, []string{protocol.CodeInsufficientResources}, h.codigos())
	assert.Equal(t, int64(29), h.madera(), "un rechazo no toca el stock")
}

// RN-ECON-009 e INV-ECON-004: la casa se completa en el tick de su vencimiento,
// sube el límite en cinco una sola vez, y la escritura lleva el bono.
func TestLaCasaSeCompletaEnElTickPrevistoYSubeElLimite(t *testing.T) {
	h := newHarness(t)
	h.darMadera(30)
	h.construir(world.Tile{X: 9, Y: 9})
	h.tick()
	require.Equal(t, int32(20), h.ciudad().PopulationLimit)

	h.hasta(epoch + 14_900)
	b, _ := h.state.BuildingAt(world.Tile{X: 9, Y: 9})
	assert.Equal(t, building.StatusUnderConstruction, b.Status)

	h.rec.reset()
	h.hasta(epoch + 15_000)
	b, _ = h.state.BuildingAt(world.Tile{X: 9, Y: 9})
	assert.Equal(t, building.StatusComplete, b.Status)
	assert.Equal(t, int32(25), h.ciudad().PopulationLimit)
	assert.Equal(t, building.PopulationLimit(20, h.state.BuildingsOf(h.cityID)), h.ciudad().PopulationLimit,
		"INV-ECON-004: el límite es la fórmula")
	updates := h.rec.byType(protocol.TypeBuildingUpdate)
	require.Len(t, updates, 1)
	assert.Equal(t, string(building.StatusComplete), updates[0].Payload.(protocol.BuildingUpdatePayload).Status)
	limites := h.rec.byType(protocol.TypeCityUpdate)
	require.Len(t, limites, 1)
	assert.Equal(t, int32(25), *limites[0].Payload.(protocol.CityUpdatePayload).PopulationLimit)
	require.Len(t, h.economy.completions, 1)
	assert.Equal(t, int32(5), h.economy.completions[0].Bonus)

	h.hasta(epoch + 30_000)
	assert.Equal(t, int32(25), h.ciudad().PopulationLimit, "no se completa dos veces")
	assert.Len(t, h.economy.completions, 1)
}

// ─────────────────────────────────────────────────────────────
// Entrenamiento
// ─────────────────────────────────────────────────────────────

func (h *harness) entrenar() {
	h.t.Helper()
	h.send(simulation.TrainUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), CityID: h.cityID, Type: unit.TypeVillager})
}

// RN-ECON-010 y RN-ECON-011: la plaza y la madera se reservan al ordenar; el
// aldeano nace al vencer, en el primer tile del anillo, cuando la base le da
// su id; la población ya lo contaba.
func TestEntrenarUnAldeanoReservaYNaceAlVencer(t *testing.T) {
	h := newHarness(t)
	h.darMadera(60)
	h.entrenar()
	assert.Equal(t, int64(10), h.madera())
	assert.Equal(t, int32(4), h.ciudad().Population, "la plaza se reserva al ordenar")
	require.Len(t, h.economy.trains, 1)
	assert.Equal(t, int64(-50), h.economy.trains[0].Stock.Delta)

	h.tick()
	updates := h.rec.byType(protocol.TypeCityUpdate)
	require.Len(t, updates, 1)
	p := updates[0].Payload.(protocol.CityUpdatePayload)
	require.NotNil(t, p.Production)
	require.NotNil(t, *p.Production)
	assert.Equal(t, epoch+20_000, (*p.Production).CompletesAtMs)
	assert.Equal(t, int32(4), *p.Population)

	// CITY_BUSY mientras entrena.
	h.rec.reset()
	h.darMadera(100)
	h.entrenar()
	assert.Equal(t, []string{protocol.CodeCityBusy}, h.codigos())

	h.rec.reset()
	h.hasta(epoch + 20_000) // vence: el nacimiento se escribe
	require.Len(t, h.economy.births, 1)
	assert.Equal(t, world.Tile{X: 12, Y: 10}, h.economy.births[0].At, "el primer tile del anillo, como los iniciales")
	assert.Empty(t, h.rec.byType(protocol.TypeEntitySpawn), "nace cuando la base le da su id")
	h.tick()
	spawns := h.rec.byType(protocol.TypeEntitySpawn)
	require.Len(t, spawns, 1)
	nacido := spawns[0].Payload.(protocol.EntitySpawnPayload).Unit
	assert.Equal(t, int64(901), nacido.ID)
	assert.Equal(t, string(unit.StatusIdle), nacido.Status)
	assert.Equal(t, int32(4), h.ciudad().Population, "INV-CITY-017: la plaza pasa de la producción a la unidad")
	_, _, entrenando := h.state.Production(h.cityID)
	assert.False(t, entrenando)
	assert.Equal(t, 4, h.state.UnitCount())
}

// RN-ECON-010: el límite de población y la madera, en ese orden.
func TestRechazosDeEntrenamiento(t *testing.T) {
	h := newHarness(t)
	h.ciudad().Population = 20
	h.darMadera(0)
	h.entrenar()
	assert.Equal(t, []string{protocol.CodePopulationLimitReached}, h.codigos())

	h.rec.reset()
	h.ciudad().Population = 3
	h.darMadera(49)
	h.entrenar()
	assert.Equal(t, []string{protocol.CodeInsufficientResources}, h.codigos())
	assert.Equal(t, int32(3), h.ciudad().Population)
}

// Un entrenamiento que la transacción rechaza deshace la reserva entera.
func TestUnEntrenamientoRechazadoDeshaceLaReserva(t *testing.T) {
	h := newHarness(t)
	h.darMadera(50)
	h.economy.trainErr = production.ErrCityBusy
	h.entrenar()
	h.tick()
	assert.Equal(t, []string{protocol.CodeCityBusy}, h.codigos())
	assert.Equal(t, int64(50), h.madera())
	assert.Equal(t, int32(3), h.ciudad().Population)
	_, _, entrenando := h.state.Production(h.cityID)
	assert.False(t, entrenando)
}

// RN-ECON-011: un nacimiento que no se escribe se reintenta sin perder la
// producción ni la plaza.
func TestUnNacimientoQueFallaSeReintenta(t *testing.T) {
	h := newHarness(t)
	h.darMadera(50)
	h.entrenar()
	h.tick()
	h.persister.permanent = true
	h.economy.birthErr = errors.New("base caída")
	h.hasta(epoch + 20_100)
	h.tick()
	_, _, entrenando := h.state.Production(h.cityID)
	assert.True(t, entrenando, "la producción sigue esperando su aldeano")
	h.economy.birthErr = nil
	h.hasta(epoch + 26_000)
	require.Len(t, h.economy.births, 1)
	h.tick()
	assert.Equal(t, 4, h.state.UnitCount())
}
