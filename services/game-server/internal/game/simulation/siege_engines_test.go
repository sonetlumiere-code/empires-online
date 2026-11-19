package simulation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de arietes y murallas (SPEC-SIEGE-ENGINES §9). Mundo de conAsedio: la
// ciudad enemiga en (20,40), con su 3 × 3 de (19,39) a (21,41), la casa en
// (20,39) y la muralla, si la hay, en el centro. El anillo de fuera (distancia
// 2 del centro) es desde donde se golpea la muralla.

const (
	murallaID = int64(60)
	arieteID  = int64(300)
)

var centroEnemigo = world.Tile{X: 20, Y: 40}

// conMuralla añade a una ciudad su muralla en el centro, completa o en obra
// hasta `terminaMs`.
func conMuralla(h *harness, c *city.City, completa bool, terminaMs int64) {
	h.t.Helper()
	estado := building.StatusUnderConstruction
	if completa {
		estado, terminaMs = building.StatusComplete, h.clk.NowMs()
	}
	h.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{{
		ID: murallaID, CityID: c.ID, Type: building.TypeWall, X: c.CenterX, Y: c.CenterY,
		Status: estado, CompletesAtMs: terminaMs,
	}}})
}

func ponerAriete(h *harness, id int64, at world.Tile) *unit.Unit {
	h.t.Helper()
	return ponerUnidad(h, id, h.playerID, h.cityID, unit.TypeRam, at, 250)
}

func (h *harness) derribarCon(atacante, edificio int64) simulation.Command {
	return simulation.DemolishBuilding{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: atacante, BuildingID: edificio}
}

func (h *harness) mensajes() []string {
	var out []string
	for _, m := range h.rec.byType(protocol.TypeSystemError) {
		out = append(out, m.Payload.(protocol.SystemErrorPayload).Message)
	}
	return out
}

// RN-SIEGE-001: la muralla se encarga en el centro, desde Bronce, cobra 200 de
// piedra y 100 de madera al encargarse y se completa a los 120 s exactos; con
// ella completa, la ciudad está amurallada. Una segunda no cabe, ni una fuera
// del centro, ni una antes de Bronce.
func TestLaMurallaSeLevantaEnElCentro(t *testing.T) {
	h := newHarness(t)
	c := h.ciudad()
	centro := c.Center()
	construir := func(at world.Tile) {
		h.send(simulation.BuildBuilding{PlayerID: h.playerID, RequestID: uuid.NewString(), CityID: c.ID, Type: building.TypeWall, Tile: at})
		h.tick()
	}
	h.almacen(c.ID, 100, 200)
	construir(centro)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "en la Edad de Piedra no hay muralla")

	h.rec.reset()
	c.Era = city.EraBronze
	construir(world.Tile{X: centro.X + 1, Y: centro.Y})
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "sólo en el centro")

	h.rec.reset()
	encargo := h.clk.NowMs()
	construir(centro)
	require.Empty(t, h.codigos())
	w, s := h.recursos(c.ID)
	assert.Equal(t, [2]int64{0, 0}, [2]int64{w, s}, "cobra al encargar")
	require.Len(t, h.economy.builds, 1)
	o := h.economy.builds[0]
	assert.Equal(t, building.TypeWall, o.Building.Type)
	assert.Equal(t, centro, o.Building.Tile())
	assert.Equal(t, encargo+120_000, o.Building.CompletesAtMs)
	assert.False(t, h.state.Walled(c.ID), "en obra no protege")

	h.rec.reset()
	h.almacen(c.ID, 100, 200)
	construir(centro)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "una por ciudad")

	h.hasta(o.Building.CompletesAtMs - 100)
	assert.False(t, h.state.Walled(c.ID))
	h.hasta(o.Building.CompletesAtMs)
	assert.True(t, h.state.Walled(c.ID), "completa, la ciudad está amurallada")
	assert.EqualValues(t, 20, c.PopulationLimit, "la muralla no da población")
}

// Golden «asedio» de E27 (RN-SIEGE-004, RN-SIEGE-005, RN-SIEGE-007,
// INV-COMBAT-008): con la muralla en pie, el guerrero no derriba la casa y el
// ariete no la toca; tres arietes en el anillo golpean la muralla 40 cada
// 3 000 ms, por id, y la abren en el golpe 38 —doce rondas de 120 y dos golpes
// más—, a los 36 s del primero. La muralla cae sin tocar el límite, se escribe
// con su tipo, el atacante recibe el aviso con la muralla y la ciudad queda en
// brecha: el guerrero ya derriba la casa.
func TestGoldenTresArietesAbrenLaMuralla(t *testing.T) {
	h := newHarness(t)
	_, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 37})
	conMuralla(h, ciudad, true, 0)
	require.True(t, h.state.Walled(ciudad.ID))
	limite := ciudad.PopulationLimit
	arietes := []int64{arieteID, arieteID + 1, arieteID + 2}
	ponerAriete(h, arietes[0], world.Tile{X: 19, Y: 38})
	ponerAriete(h, arietes[1], world.Tile{X: 20, Y: 38})
	ponerAriete(h, arietes[2], world.Tile{X: 21, Y: 38})

	h.derribar(guerreroID, casaID)
	h.send(h.derribarCon(arietes[0], casaID))
	assert.Equal(t, []string{protocol.CodeInvalidTarget, protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, []string{"la muralla está en pie: sólo un ariete la derriba", "primero, la muralla"}, h.mensajes())

	h.rec.reset()
	for _, id := range arietes {
		h.commands <- h.derribarCon(id, murallaID)
	}
	h.loop.Step(h.clk.NowMs())
	require.Empty(t, h.codigos())
	var golpes []int64
	var vidas []int32
	vida := int32(1_500)
	mirar := func() bool {
		hp, enPie := vidaDe(h, murallaID)
		if !enPie {
			hp = 0
		}
		if hp != vida {
			vida = hp
			golpes = append(golpes, h.clk.NowMs())
			vidas = append(vidas, hp)
		}
		return !enPie
	}
	mirar() // la primera ronda cae en el tick de la orden
	require.True(t, h.tickHasta(600, mirar))

	require.Len(t, golpes, 13, "doce rondas de tres y una de dos")
	for i := 1; i < len(golpes); i++ {
		assert.Equal(t, int64(3_000), golpes[i]-golpes[i-1], "ronda %d", i)
	}
	assert.Equal(t, int32(1_500-120), vidas[0])
	assert.Equal(t, int32(60), vidas[11])
	assert.Equal(t, int32(0), vidas[12])

	assert.False(t, h.state.Walled(ciudad.ID), "en brecha")
	assert.Equal(t, limite, ciudad.PopulationLimit, "la muralla no daba población")
	require.Len(t, h.economy.destroys, 1)
	d := h.economy.destroys[0]
	assert.Equal(t, murallaID, d.BuildingID)
	assert.Equal(t, building.TypeWall, d.Type)
	assert.Equal(t, arietes[1], d.AttackerID, "el segundo de la última ronda la derriba")
	assert.Zero(t, d.Bonus)
	h.tick() // el que golpeó antes en la última ronda ve la muralla caída en el siguiente
	for _, id := range arietes {
		u, _ := h.state.Unit(id)
		assert.Equal(t, unit.StatusIdle, u.Status, "el ariete %d termina su asedio", id)
	}
	var aviso *protocol.EventLogNoticePayload
	for _, m := range h.rec.byType(protocol.TypeEventLogNotice) {
		if p := m.Payload.(protocol.EventLogNoticePayload); m.PlayerID == h.playerID && p.Kind == "HOUSE_DESTROYED" {
			aviso = &p
		}
	}
	require.NotNil(t, aviso, "el aviso del derribo, al atacante")
	assert.Equal(t, "WALL", aviso.BuildingType)

	// En brecha, el guerrero derriba la casa como en E8.
	h.rec.reset()
	h.derribar(guerreroID, casaID)
	require.Empty(t, h.codigos())
	objetivo, ok := h.state.Demolishing(guerreroID)
	require.True(t, ok)
	assert.Equal(t, casaID, objetivo)
}

// SPEC-SIEGE-ENGINES §9 (precisión): la muralla se golpea desde el anillo de
// fuera del 3 × 3, como cualquier edificio de la ciudad. Lejos, el ariete va al
// tile del anillo más cercano —(18,38): empate en distancia y en y, menor x— a
// 1 800 ms por tile (seis pasos rectos y dos en diagonal, de 2 546), y empieza a
// golpear al llegar.
func TestElArieteVaAlAnilloYGolpeaLaMuralla(t *testing.T) {
	h := newHarness(t)
	_, ciudad, _ := conAsedio(h, world.Tile{X: 5, Y: 5})
	conMuralla(h, ciudad, true, 0)
	ponerAriete(h, arieteID, world.Tile{X: 20, Y: 30})

	h.send(h.derribarCon(arieteID, murallaID))
	require.Empty(t, h.codigos())
	m, va := h.state.Movement(arieteID)
	require.True(t, va)
	assert.Equal(t, world.Tile{X: 18, Y: 38}, m.Target)
	assert.Equal(t, int64(6*1_800+2*2_546), m.ArrivalTimeMs-m.StartTimeMs)

	h.hasta(m.ArrivalTimeMs + 100)
	hp, _ := vidaDe(h, murallaID)
	assert.Equal(t, int32(1_460), hp, "un golpe de 40 al llegar")
}

// RN-SIEGE-006 e INV-COMBAT-008: una muralla que se completa a mitad de un
// derribo lo termina: el golpe de la fase 4 del tick en que se completa aún cae
// (la obra se completa en la fase 5, ADR-048 §2), y desde el siguiente el
// guerrero queda IDLE y la casa no recibe más. En obra no protegía nada.
func TestLaMurallaQueSeCompletaAMitadDetieneElDerribo(t *testing.T) {
	h := newHarness(t)
	_, ciudad, g := conAsedio(h, world.Tile{X: 20, Y: 38})
	termina := h.clk.NowMs() + 6_000
	conMuralla(h, ciudad, false, termina)

	h.derribar(guerreroID, casaID)
	require.Empty(t, h.codigos(), "en obra, la muralla no protege")
	h.hasta(termina - 100)
	enObra, _ := vidaDe(h, casaID)
	assert.Equal(t, int32(150-4*8), enObra, "cuatro golpes con la muralla en obra")
	assert.Equal(t, unit.StatusAttacking, g.Status)

	h.hasta(termina)
	require.True(t, h.state.Walled(ciudad.ID))
	antes, _ := vidaDe(h, casaID)
	assert.Equal(t, int32(150-5*8), antes, "el golpe del tick en que se completa")
	h.tick()
	_, asedia := h.state.Demolishing(guerreroID)
	assert.False(t, asedia, "el asedio termina")
	assert.Equal(t, unit.StatusIdle, g.Status)
	h.advance(10 * time.Second)
	despues, _ := vidaDe(h, casaID)
	assert.Equal(t, antes, despues, "ni un golpe más")
}

// Una muralla en obra se derriba como una casa en obra: no protege, su encargo
// se pierde y la ciudad sigue en brecha.
func TestUnaMurallaEnObraSeDerribaComoUnaCasa(t *testing.T) {
	h := newHarness(t)
	_, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 38})
	conMuralla(h, ciudad, false, h.clk.NowMs()+3_600_000)
	ponerAriete(h, arieteID, world.Tile{X: 20, Y: 38})

	h.send(h.derribarCon(arieteID, murallaID))
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(2_000, func() bool { _, enPie := vidaDe(h, murallaID); return !enPie }))
	require.Len(t, h.economy.destroys, 1)
	assert.Equal(t, building.TypeWall, h.economy.destroys[0].Type)
	assert.False(t, h.state.Walled(ciudad.ID))
}

// RN-SIEGE-003, Q7 e INV-COMBAT-009: el ariete sólo derriba. Cada otra orden de
// unidad responde UNIT_NOT_MOVABLE, y no responde cuando atacan a su ciudad.
func TestElArieteSoloDerriba(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 36})
	ponerAriete(h, arieteID, world.Tile{X: 19, Y: 37})
	victima := ponerUnidad(h, 400, enemigo, ciudad.ID, unit.TypeVillager, world.Tile{X: 20, Y: 37}, 40)

	h.send(simulation.AttackUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: arieteID, TargetID: victima.ID})
	h.send(simulation.PillageCity{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: arieteID, CityID: ciudad.ID})
	h.send(simulation.ClaimTerritory{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: arieteID})
	h.send(simulation.Gather{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: arieteID, Target: world.Tile{X: 5, Y: 5}})
	h.send(simulation.GarrisonUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: arieteID, CityID: h.cityID})
	h.tick()
	assert.Equal(t, []string{
		protocol.CodeUnitNotMovable, protocol.CodeUnitNotMovable, protocol.CodeUnitNotMovable,
		protocol.CodeUnitNotMovable, protocol.CodeUnitNotMovable,
	}, h.codigos())
	assert.Equal(t, int32(40), victima.HP)

	// El ariete del enemigo, parado junto a su ciudad, no responde al saqueo.
	h.rec.reset()
	defensa := ponerUnidad(h, arieteID+1, enemigo, ciudad.ID, unit.TypeRam, world.Tile{X: 22, Y: 37}, 250)
	h.maderaDe(ciudad.ID, 100)
	h.saquear(guerreroID, ciudad.ID)
	require.Empty(t, h.codigos())
	h.advance(5 * time.Second)
	_, responde := h.state.Engaged(defensa.ID)
	assert.False(t, responde, "el ariete no toma objetivos de unidades")
	assert.Equal(t, unit.StatusIdle, defensa.Status)
}

// RN-SIEGE-008: el ariete que golpea la muralla despierta a los guerreros de la
// ciudad, que lo toman por objetivo, y el castillo le dispara.
func TestLosGuerrerosYElCastilloDefiendenLaMuralla(t *testing.T) {
	h := newHarness(t)
	enemigo, ciudad, _ := conAsedio(h, world.Tile{X: 5, Y: 5})
	conMuralla(h, ciudad, true, 0)
	h.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{{
		ID: 70, CityID: ciudad.ID, Type: building.TypeCastle, X: 21, Y: 41,
		Status: building.StatusComplete, CompletesAtMs: h.clk.NowMs(),
	}}})
	guardia := ponerUnidad(h, 401, enemigo, ciudad.ID, unit.TypeWarrior, world.Tile{X: 23, Y: 40}, 60)
	a := ponerAriete(h, arieteID, world.Tile{X: 20, Y: 38})

	h.send(h.derribarCon(arieteID, murallaID))
	require.Empty(t, h.codigos())
	h.tick()
	objetivo, responde := h.state.Engaged(guardia.ID)
	require.True(t, responde, "el guardia responde al golpe en la muralla")
	assert.Equal(t, arieteID, objetivo)
	h.advance(3 * time.Second)
	assert.Less(t, a.HP, int32(250), "el castillo y el guardia le quitan vida")
}

// RN-SIEGE-003: el ariete se entrena desde la Edad de Hierro, con su coste en
// madera y piedra, y ocupa dos plazas.
func TestElArieteSeEntrenaDesdeHierro(t *testing.T) {
	h := newHarness(t)
	c := h.ciudad()
	c.Era = city.EraBronze
	h.almacen(c.ID, 150, 75)
	h.entrenarTipo(unit.TypeRam)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "en Bronce, todavía no")

	h.rec.reset()
	c.Era = city.EraIron
	h.entrenarTipo(unit.TypeRam)
	require.Empty(t, h.codigos())
	assert.Zero(t, h.stockDe(economy.ResourceWood))
	assert.Zero(t, h.stockDe(economy.ResourceStone))
	assert.Equal(t, int32(5), c.Population, "tres más dos plazas")
	require.Len(t, h.economy.trains, 1)
	assert.Equal(t, unit.TypeRam, h.economy.trains[0].Production.UnitType)
}

// RN-COMBAT-032 aplicado a la muralla (Q5): su daño se olvida a los 5 minutos
// sin golpes, como el de una casa.
func TestElDanoDeLaMurallaSeOlvida(t *testing.T) {
	h := newHarness(t)
	_, ciudad, _ := conAsedio(h, world.Tile{X: 5, Y: 5})
	conMuralla(h, ciudad, true, 0)
	ponerAriete(h, arieteID, world.Tile{X: 20, Y: 38})

	h.send(h.derribarCon(arieteID, murallaID))
	h.tick()
	hp, _ := vidaDe(h, murallaID)
	require.Equal(t, int32(1_460), hp)
	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: arieteID, Target: world.Tile{X: 20, Y: 30}})
	h.advance(5*time.Minute + time.Second)
	hp, _ = vidaDe(h, murallaID)
	assert.Equal(t, int32(1_500), hp)
}
