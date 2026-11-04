package simulation_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/deposit"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de simulación del agotamiento del bosque (SPEC-ECON-FOREST §9).
//
// Mundo de newHarnessMadera: las columnas x = 22 y x = 23 son bosque, la
// ciudad 1 está en (10, 10) y su depósito en (12, 10). El aldeano 1 hace el
// viaje del golden «madera»: carga en 16 360, deposita en 22 360, vuelve a
// llegar al bosque en 28 720 y carga otra vez en 38 720.

const lejos = int64(1) << 50 // una regeneración que no vence durante el test

// cargas son las cargas de bosque que viajaron en transiciones de tarea.
func (h *harness) cargas() []simulation.ForestLoad {
	h.t.Helper()
	h.economy.mu.Lock()
	defer h.economy.mu.Unlock()
	var out []simulation.ForestLoad
	for _, w := range h.economy.tasks {
		if w.Load != nil {
			out = append(out, *w.Load)
		}
	}
	return out
}

func (h *harness) avisosDeBosque() []protocol.ForestView {
	var out []protocol.ForestView
	for _, m := range h.rec.byType(protocol.TypeForestUpdate) {
		out = append(out, m.Payload.(protocol.ForestView))
	}
	return out
}

// RN-ECON-013 e INV-ECON-008: la carga descuenta del tile lo mismo que gana el
// aldeano, se anuncia, y viaja en la transacción de la transición que sigue,
// con la fase WORKING que consume.
func TestLaCargaDescuentaElBosqueYViajaEnLaTransaccion(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	require.Equal(t, deposit.Forest.Capacity, h.state.ForestAt(bosque, epoch), "un bosque sin tocar está lleno")
	h.recolectar(1, bosque)
	h.hasta(epoch + 16_400)

	tk, _ := h.state.Task(1)
	require.Equal(t, int32(10), tk.Carrying)
	assert.Equal(t, deposit.Forest.Capacity-10, h.state.ForestAt(bosque, h.clk.NowMs()))

	cargas := h.cargas()
	require.Len(t, cargas, 1)
	c := cargas[0]
	assert.Equal(t, bosque, c.Tile)
	assert.Equal(t, int32(10), c.Amount, "lo que pierde el tile es lo que gana el aldeano")
	assert.Equal(t, epoch+16_360, c.AtMs)
	assert.Equal(t, epoch+16_360+deposit.Forest.RegrowAfterMs, c.RegrowsAtMs, "vuelve a crecer 24 h después de la carga")
	assert.Equal(t, int32(0), c.FromTrips)
	assert.Equal(t, epoch+16_360, c.FromUntilMs, "la fase WORKING que consume")
	assert.False(t, c.Depleted)

	avisos := h.avisosDeBosque()
	require.Len(t, avisos, 1)
	assert.Equal(t, int32(90), avisos[0].Remaining)
	require.NotNil(t, avisos[0].RegrowsAtMs)
}

// RN-ECON-013 y RN-ECON-015: con cuatro de madera, el aldeano carga cuatro, el
// tile queda agotado (ForestDepleted) y el viaje siguiente va al tile con
// madera más cercano: a distancia 1, el de menor y, (22, 9).
func TestUnaCargaParcialAgotaElTileYElViajeSiguienteVaAlMasCercano(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	h.state.SetForest(bosque, deposit.State{Remaining: 4, RegrowsAtMs: lejos})
	h.recolectar(1, bosque)
	h.hasta(epoch + 16_400)

	tk, _ := h.state.Task(1)
	require.Equal(t, int32(4), tk.Carrying)
	assert.Equal(t, int32(0), h.state.ForestAt(bosque, h.clk.NowMs()))
	require.Len(t, h.cargas(), 1)
	assert.True(t, h.cargas()[0].Depleted)

	h.hasta(epoch + 22_400)
	assert.Equal(t, int64(4), h.madera(), "deposita lo que cargó")
	tk, ok := h.state.Task(1)
	require.True(t, ok, "la tarea sigue")
	assert.Equal(t, world.Tile{X: 22, Y: 9}, tk.Target, "el tile con madera más cercano")
	assert.Equal(t, task.PhaseToResource, tk.Phase)
	m, ok := h.state.Movement(1)
	require.True(t, ok)
	assert.Equal(t, world.Tile{X: 22, Y: 9}, m.Target)
	assert.Equal(t, epoch+22_360, m.StartTimeMs, "el viaje nuevo sale en el instante del depósito")
}

// RN-ECON-015: sin otro tile con madera a 6 o menos, el aldeano deposita y la
// tarea termina en el depósito.
func TestSinBosqueCercaLaTareaTerminaEnElDeposito(t *testing.T) {
	h := newHarnessConTerreno(t, func(terrain []byte) { terrain[10*64+22] = byte(world.Forest) })
	h.conectar()
	h.state.SetForest(bosque, deposit.State{Remaining: 10, RegrowsAtMs: lejos})
	h.recolectar(1, bosque)
	h.hasta(epoch + 22_400)

	assert.Equal(t, int64(10), h.madera())
	_, ok := h.state.Task(1)
	assert.False(t, ok, "la tarea terminó")
	u, _ := h.state.Unit(1)
	assert.Equal(t, unit.StatusIdle, u.Status)
	assert.Equal(t, deposito, u.Tile(), "queda IDLE en el depósito")
	_, moving := h.state.Movement(1)
	assert.False(t, moving)
}

// RN-ECON-014: un tile sin madera no se recolecta.
func TestRecolectarUnBosqueAgotadoSeRechaza(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	h.state.SetForest(bosque, deposit.State{Remaining: 0, RegrowsAtMs: lejos})
	h.recolectar(1, bosque)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	_, ok := h.state.Task(1)
	assert.False(t, ok)
}

// RN-ECON-013: dos aldeanos que terminan de trabajar en el mismo tick sobre el
// mismo tile cargan en orden de id: con quince, el primero se lleva diez y el
// segundo cinco, y nadie crea madera.
func TestDosAldeanosEnElMismoTileCarganEnOrdenDeId(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	h.state.SetForest(bosque, deposit.State{Remaining: 15, RegrowsAtMs: lejos})
	ponerUnidad(h, 302, h.playerID, h.cityID, unit.TypeVillager, bosque, 40)
	ponerUnidad(h, 301, h.playerID, h.cityID, unit.TypeVillager, bosque, 40)
	h.commands <- simulation.Gather{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: 302, Target: bosque}
	h.commands <- simulation.Gather{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: 301, Target: bosque}
	h.tick()
	h.hasta(h.clk.NowMs() + 10_100)

	primero, _ := h.state.Task(301)
	segundo, _ := h.state.Task(302)
	assert.Equal(t, int32(10), primero.Carrying)
	assert.Equal(t, int32(5), segundo.Carrying)
	assert.Equal(t, int32(0), h.state.ForestAt(bosque, h.clk.NowMs()))
	total := int32(0)
	for _, c := range h.cargas() {
		total += c.Amount
	}
	assert.Equal(t, int32(15), total, "INV-ECON-008: las cargas suman lo que tenía el tile")
}

// RN-ECON-015: si el tile se agota mientras el aldeano va hacia él, al llegar
// no trabaja en él: va directo al más cercano con madera.
func TestSiElBosqueSeAgotaPorElCaminoVaAlSiguiente(t *testing.T) {
	h := newHarnessMadera(t)
	h.conectar()
	h.recolectar(1, bosque)
	h.hasta(epoch + 3_000)
	h.state.SetForest(bosque, deposit.State{Remaining: 0, RegrowsAtMs: lejos})
	h.hasta(epoch + 6_400)

	tk, ok := h.state.Task(1)
	require.True(t, ok)
	assert.Equal(t, world.Tile{X: 22, Y: 9}, tk.Target)
	assert.Equal(t, task.PhaseToResource, tk.Phase, "va hacia el tile nuevo, no trabaja en el agotado")
	h.hasta(epoch + 8_000)
	u, _ := h.state.Unit(1)
	assert.Equal(t, unit.StatusWorking, u.Status)
	assert.Equal(t, world.Tile{X: 22, Y: 9}, u.Tile())
}

// RN-ECON-016: el tile vuelve a estar lleno en su instante exacto; el barrido
// lo saca de la capa y lo anuncia lleno, con regrowsAtMs a null.
func TestElBosqueVuelveACrecerEnSuInstante(t *testing.T) {
	h := newHarnessMadera(t)
	vence := epoch + 1_000
	h.state.SetForest(bosque, deposit.State{Remaining: 0, RegrowsAtMs: vence})
	assert.Equal(t, int32(0), h.state.ForestAt(bosque, vence-1))
	assert.Equal(t, deposit.Forest.Capacity, h.state.ForestAt(bosque, vence), "lleno desde el instante exacto")

	h.hasta(vence + 1_100)
	assert.Equal(t, 0, h.state.ForestCount(), "el barrido lo saca de la capa")
	avisos := h.avisosDeBosque()
	require.Len(t, avisos, 1)
	assert.Equal(t, protocol.ForestView{X: bosque.X, Y: bosque.Y, Remaining: deposit.Forest.Capacity}, avisos[0])
}

// RN-ECON-018: el snapshot trae los tiles a medias de su área, de cualquiera.
func TestElSnapshotTraeElBosqueAMedias(t *testing.T) {
	h := newHarnessMadera(t)
	h.state.SetForest(bosque, deposit.State{Remaining: 30, RegrowsAtMs: lejos})
	h.state.SetForest(world.Tile{X: 23, Y: 40}, deposit.State{Remaining: 0, RegrowsAtMs: epoch}) // ya volvió a crecer
	snap := h.sim.BuildSnapshot(uuid.New(), []world.ChunkCoord{{CX: 0, CY: 0}, {CX: 0, CY: 1}}, epoch+1, false)
	at := lejos
	assert.Equal(t, []protocol.ForestView{{X: bosque.X, Y: bosque.Y, Remaining: 30, RegrowsAtMs: &at}}, snap.Forests)
}

// INV-ECON-007 al arrancar: una fila vencida no entra (está llena), y una fila
// fuera de rango o sobre un tile que no es bosque se descarta y se señala.
func TestLaCapaDelArranqueDescartaLoQueNoVale(t *testing.T) {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	terrain[10*64+22] = byte(world.Forest)
	terrain[11*64+22] = byte(world.Forest)
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	state := simulation.NewState(w)
	var malas []world.Tile
	n := state.SetForests([]simulation.ForestRow{
		{Tile: world.Tile{X: 22, Y: 10}, State: deposit.State{Remaining: 40, RegrowsAtMs: lejos}},
		{Tile: world.Tile{X: 22, Y: 11}, State: deposit.State{Remaining: 0, RegrowsAtMs: 5}},
		{Tile: world.Tile{X: 5, Y: 5}, State: deposit.State{Remaining: 10, RegrowsAtMs: lejos}},
		{Tile: world.Tile{X: 22, Y: 10}, State: deposit.State{Remaining: 101, RegrowsAtMs: lejos}},
	}, 10, func(t world.Tile, _ string) { malas = append(malas, t) })
	assert.Equal(t, 1, n)
	assert.Equal(t, int32(40), state.ForestAt(world.Tile{X: 22, Y: 10}, 10))
	assert.Equal(t, deposit.Forest.Capacity, state.ForestAt(world.Tile{X: 22, Y: 11}, 10), "la vencida está llena")
	assert.Equal(t, []world.Tile{{X: 5, Y: 5}, {X: 22, Y: 10}}, malas)
}
