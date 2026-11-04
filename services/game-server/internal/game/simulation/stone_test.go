package simulation_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/deposit"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de simulación de la piedra (SPEC-ECON-STONE §9).
//
// Mundo de newHarnessPiedra: la columna x = 22 es colina y la x = 23 bosque; la
// ciudad 1 está en (10, 10) y su depósito en (12, 10). El viaje del aldeano 1
// a la colina (22, 10):
//
//	ida      9 pasos de hierba (600 ms) + 1 de colina (1 080 ms) = 6 480 ms
//	trabajo  10 000 ms                                            → 16 480
//	vuelta   10 pasos de hierba                       = 6 000 ms  → 22 480

var colina = world.Tile{X: 22, Y: 10}

func pintarColina(terrain []byte) {
	for y := 0; y < 64; y++ {
		terrain[y*64+22] = byte(world.Hill)
		terrain[y*64+23] = byte(world.Forest)
	}
}

func newHarnessPiedra(t *testing.T) *harness {
	t.Helper()
	return newHarnessConTerreno(t, pintarColina)
}

func (h *harness) piedra() int64 { return h.state.StockOf(h.cityID, economy.ResourceStone).Amount }

func (h *harness) avisosDeColina() []protocol.ForestView {
	var out []protocol.ForestView
	for _, m := range h.rec.byType(protocol.TypeHillUpdate) {
		out = append(out, m.Payload.(protocol.ForestView))
	}
	return out
}

// RN-ECON-019 y RN-ECON-021: la misma orden sobre una colina recoge piedra. La
// tarea es GATHER_STONE, la carga sale de la capa de la colina y viaja en la
// transacción con su capa, el aldeano la lleva como STONE y el depósito la suma
// a la piedra de la ciudad, no a la madera.
func TestRecogerPiedraEnUnaColina(t *testing.T) {
	h := newHarnessPiedra(t)
	h.conectar()
	require.Equal(t, deposit.Hill.Capacity, h.state.HillAt(colina, epoch), "una colina sin tocar está llena")
	h.recolectar(1, colina)
	require.Empty(t, h.codigos())
	tk, ok := h.state.Task(1)
	require.True(t, ok)
	assert.Equal(t, task.KindGatherStone, tk.Kind, "el terreno decide el recurso")

	h.hasta(epoch + 16_500)
	tk, _ = h.state.Task(1)
	require.Equal(t, int32(10), tk.Carrying)
	assert.Equal(t, deposit.Hill.Capacity-10, h.state.HillAt(colina, h.clk.NowMs()))
	assert.Equal(t, deposit.Forest.Capacity, h.state.ForestAt(world.Tile{X: 23, Y: 10}, h.clk.NowMs()),
		"el bosque de al lado no se toca")

	cargas := h.cargas()
	require.Len(t, cargas, 1)
	assert.Equal(t, deposit.Hill.Terrain, cargas[0].Layer.Terrain, "la carga viaja con su capa")
	assert.Equal(t, int32(10), cargas[0].Amount)
	assert.Equal(t, epoch+16_480, cargas[0].AtMs)

	avisos := h.avisosDeColina()
	require.Len(t, avisos, 1)
	assert.Equal(t, int32(90), avisos[0].Remaining)
	assert.Empty(t, h.avisosDeBosque(), "una colina no se anuncia como bosque")

	var recurso string
	for _, m := range h.rec.byType(protocol.TypeEntityUpdate) {
		if p := m.Payload.(protocol.EntityUpdatePayload); p.ID == 1 && p.Carrying != nil && *p.Carrying == 10 {
			recurso = p.CarryingResource
		}
	}
	assert.Equal(t, "STONE", recurso, "la carga se anuncia con su recurso")

	h.hasta(epoch + 22_500)
	assert.Equal(t, int64(10), h.piedra(), "deposita piedra")
	assert.Equal(t, int64(0), h.madera(), "y no madera")
	assert.Equal(t, int64(10), h.state.UnflushedOf(h.cityID, economy.ResourceStone))
}

// ADR-030 §3: el volcado escribe un delta por recurso con depósitos
// pendientes, los dos de la ciudad en el mismo trabajo.
func TestElVolcadoEscribeUnDeltaPorRecurso(t *testing.T) {
	h := newHarnessPiedra(t)
	h.conectar()
	h.recolectar(1, colina)
	h.recolectar(2, world.Tile{X: 23, Y: 12})
	h.hasta(epoch + 30_000)
	require.Positive(t, h.piedra())
	require.Positive(t, h.madera())

	h.sim.FlushDirty()
	h.economy.mu.Lock()
	defer h.economy.mu.Unlock()
	porRecurso := map[economy.Resource]int64{}
	for _, d := range h.economy.flushes {
		porRecurso[d.Resource] += d.Delta
	}
	assert.Equal(t, h.piedra(), porRecurso[economy.ResourceStone])
	assert.Equal(t, h.madera(), porRecurso[economy.ResourceWood])
}

// RN-ECON-021: una colina agotada no se recoge; RN-ECON-019: la hierba no da
// nada.
func TestUnaColinaAgotadaSeRechaza(t *testing.T) {
	h := newHarnessPiedra(t)
	h.state.SetHill(colina, deposit.State{Remaining: 0, RegrowsAtMs: lejos})
	h.recolectar(1, colina)
	h.recolectar(1, world.Tile{X: 30, Y: 10})
	assert.Equal(t, []string{protocol.CodeInvalidTarget, protocol.CodeInvalidTarget}, h.codigos())
	_, ok := h.state.Task(1)
	assert.False(t, ok)
}

// RN-ECON-022: con su colina agotada, el aldeano busca otra colina cerca y
// nunca el bosque, aunque esté al lado; sin colina cerca, la tarea termina en el
// depósito.
func TestLaBusquedaNoCambiaDeRecurso(t *testing.T) {
	h := newHarnessConTerreno(t, func(terrain []byte) {
		terrain[10*64+22] = byte(world.Hill)
		for y := 0; y < 64; y++ {
			terrain[y*64+23] = byte(world.Forest)
		}
	})
	h.conectar()
	h.state.SetHill(colina, deposit.State{Remaining: 4, RegrowsAtMs: lejos})
	h.recolectar(1, colina)
	h.hasta(epoch + 16_500)
	tk, _ := h.state.Task(1)
	require.Equal(t, int32(4), tk.Carrying)
	require.True(t, h.cargas()[0].Depleted)

	h.hasta(epoch + 22_500)
	assert.Equal(t, int64(4), h.piedra())
	_, ok := h.state.Task(1)
	assert.False(t, ok, "sin otra colina cerca, la tarea termina en el depósito")
	assert.Equal(t, deposito, h.unitTile(1))
	assert.Equal(t, deposit.Forest.Capacity, h.state.ForestAt(world.Tile{X: 23, Y: 10}, h.clk.NowMs()))
}

// RN-ECON-020: si el tile de trabajo queda más lejos del depósito que el tope
// del pathfinding, la orden se rechaza aunque la unidad esté al lado: cargaría
// y no podría volver. Dentro del tope, se acepta.
func TestElDepositoTieneQueQuedarAlAlcance(t *testing.T) {
	h := newHarnessConDeps(t, pintarColina, func(d *simulation.Deps) { d.PathMaxDistance = 8 })
	h.conectar()
	u, _ := h.state.Unit(1)
	u.SetTile(world.Tile{X: 21, Y: 10})
	h.recolectar(1, colina)
	assert.Equal(t, []string{protocol.CodePathTooLong}, h.codigos(), "el depósito está a 10 de la colina")
	_, ok := h.state.Task(1)
	assert.False(t, ok)

	cerca := newHarnessConDeps(t, func(terrain []byte) { terrain[10*64+19] = byte(world.Hill) },
		func(d *simulation.Deps) { d.PathMaxDistance = 8 })
	cerca.conectar()
	cerca.recolectar(1, world.Tile{X: 19, Y: 10})
	assert.Empty(t, cerca.codigos(), "a 7 del depósito, dentro del tope")
}

// RN-ECON-021: la colina vuelve a crecer en su instante y se anuncia llena.
func TestLaColinaVuelveACrecer(t *testing.T) {
	h := newHarnessPiedra(t)
	vence := epoch + 2_000
	h.state.SetHill(colina, deposit.State{Remaining: 0, RegrowsAtMs: vence})
	assert.Equal(t, int32(0), h.state.HillAt(colina, vence-1))
	assert.Equal(t, deposit.Hill.Capacity, h.state.HillAt(colina, vence), "llena desde el instante exacto")
	h.hasta(vence + 1_100)
	assert.Zero(t, h.state.HillCount(), "el barrido la saca de la capa")
	avisos := h.avisosDeColina()
	require.Len(t, avisos, 1)
	assert.Equal(t, protocol.ForestView{X: colina.X, Y: colina.Y, Remaining: deposit.Hill.Capacity}, avisos[0])
}

// INV-ECON-007: al arrancar, una fila de piedra fuera de rango o sobre un tile
// que no es colina se descarta y se señala; una vencida está llena.
func TestLaCapaDePiedraDelArranqueDescartaLoQueNoVale(t *testing.T) {
	terrain := make([]byte, 64*64)
	pintarColina(terrain)
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	state := simulation.NewState(w)
	var malas []world.Tile
	n := state.SetHills([]simulation.DepositRow{
		{Tile: world.Tile{X: 22, Y: 10}, State: deposit.State{Remaining: 40, RegrowsAtMs: lejos}},
		{Tile: world.Tile{X: 22, Y: 11}, State: deposit.State{Remaining: 0, RegrowsAtMs: 5}},
		{Tile: world.Tile{X: 23, Y: 10}, State: deposit.State{Remaining: 10, RegrowsAtMs: lejos}},
		{Tile: world.Tile{X: 22, Y: 12}, State: deposit.State{Remaining: 101, RegrowsAtMs: lejos}},
	}, 10, func(t world.Tile, _ string) { malas = append(malas, t) })
	assert.Equal(t, 1, n)
	assert.Equal(t, []world.Tile{{X: 23, Y: 10}, {X: 22, Y: 12}}, malas, "el bosque y el rango")
	assert.Equal(t, int32(40), state.HillAt(world.Tile{X: 22, Y: 10}, 10))
	assert.Equal(t, deposit.Hill.Capacity, state.HillAt(world.Tile{X: 22, Y: 11}, 10), "la vencida está llena")
}

// SPEC-ECON-STONE §6: el snapshot lleva la colina tocada y la piedra de la
// ciudad; la unidad que carga piedra la lleva con su recurso.
func TestElSnapshotLlevaLaColinaYLaPiedra(t *testing.T) {
	h := newHarnessPiedra(t)
	h.conectar()
	h.state.SetHill(world.Tile{X: 22, Y: 20}, deposit.State{Remaining: 30, RegrowsAtMs: lejos})
	h.recolectar(1, colina)
	h.hasta(epoch + 16_500)
	p := h.sim.BuildSnapshot(h.playerID, []world.ChunkCoord{{CX: 0, CY: 0}}, h.clk.NowMs(), false)
	require.Len(t, p.Hills, 2)
	assert.Equal(t, int32(90), p.Hills[0].Remaining, "(22, 10), en orden de y")
	assert.Equal(t, int32(30), p.Hills[1].Remaining)
	require.Len(t, p.Cities, 1)
	assert.Equal(t, int64(0), p.Cities[0].Stocks.Stone)
	for _, u := range p.Units {
		if u.ID == 1 {
			assert.Equal(t, int32(10), u.Carrying)
			assert.Equal(t, "STONE", u.CarryingResource)
		}
	}
}

// INV-COMBAT-003 y RN-COMBAT-010: el botín de una muerte es madera; la piedra
// que llevaba la víctima se pierde con ella, porque convertirla en madera la
// crearía (SPEC-ECON-STONE, precisión de §4.2).
func TestLaPiedraDeUnAldeanoMuertoNoEsBotin(t *testing.T) {
	h := newHarness(t)
	enemigo, _ := conEnemigo(h)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 15, Y: 20}, 60)
	ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 16, Y: 20}, 8)
	carga := task.NewGatherOf(enemigoID, task.KindGatherStone, world.Tile{X: 30, Y: 20})
	carga.Phase, carga.Carrying = task.PhaseToDeposit, 10
	h.state.SetEconomy(simulation.EconomyLoad{Tasks: []task.Task{carga}})

	h.atacar(guerreroID, enemigoID)
	h.tick()
	_, vive := h.state.Unit(enemigoID)
	require.False(t, vive)
	assert.Equal(t, int32(0), g.Loot, "la piedra no pasa al asesino como madera")
	require.Len(t, h.combat.deaths, 1)
	assert.Equal(t, int32(0), h.combat.deaths[0].Loot)
	assert.Equal(t, int32(0), h.combat.deaths[0].Lost, "lo perdido se cuenta en madera")
}

// SPEC-ECON-STONE Q8: el saqueo se lleva madera y nunca piedra.
func TestElSaqueoNoSeLlevaPiedra(t *testing.T) {
	h := newHarness(t)
	_, c := conEnemigo(h)
	h.maderaDe(c.ID, 100)
	h.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: c.ID, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: 500}},
	}})
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: c.CenterX + 2, Y: c.CenterY}, 60)
	h.conectar()
	h.saquear(guerreroID, c.ID)
	h.advance(10 * time.Second)
	assert.Less(t, h.state.Stock(c.ID).Amount, int64(100), "el saqueo se llevó madera")
	assert.Equal(t, int64(500), h.state.StockOf(c.ID, economy.ResourceStone).Amount, "y nada de piedra")
}

// RN-ECON-020, precisión del 2026-11-04: lo que tiene que quedar al alcance es
// la RUTA de vuelta, no sólo la distancia. Con el tope de nodos del A* corto,
// la vuelta de 28 tiles no cabe aunque la distancia sí; un aldeano llevado
// junto a la colina cargaría y perdería la carga al no poder volver. La orden
// se rechaza al darla.
func TestLaVueltaAlDepositoTieneQueTenerRuta(t *testing.T) {
	pintar := func(terrain []byte) { terrain[10*64+40] = byte(world.Hill) }
	h := newHarnessConDeps(t, pintar, func(d *simulation.Deps) { d.PathMaxNodes = 20 })
	h.conectar()
	u, _ := h.state.Unit(1)
	u.SetTile(world.Tile{X: 39, Y: 10})
	h.recolectar(1, world.Tile{X: 40, Y: 10})
	assert.Equal(t, []string{protocol.CodePathTooLong}, h.codigos(), "la vuelta de 28 tiles no cabe en 20 nodos")
	_, ok := h.state.Task(1)
	assert.False(t, ok, "no empieza una tarea que no podría volver")

	cerca := newHarnessConDeps(t, func(terrain []byte) { terrain[10*64+16] = byte(world.Hill) },
		func(d *simulation.Deps) { d.PathMaxNodes = 20 })
	cerca.conectar()
	cerca.recolectar(1, world.Tile{X: 16, Y: 10})
	assert.Empty(t, cerca.codigos(), "a 4 del depósito la vuelta cabe")
}
