package simulation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Golden por rasgo de SPEC-CIVILIZATION (§9). El catálogo es el de la migración
// 000015; la civilización de un jugador es la de su ciudad. Los demás tests de
// la simulación no instalan catálogo y por eso sus golden no cambian: sin
// catálogo, los rasgos son neutros.

func catalogoDeCivilizaciones() []player.Civilization {
	return []player.Civilization{
		{ID: 1, Code: "ROMAN", Traits: map[string]int32{player.TraitMoveSpeed: 1000, player.TraitProductionSpeed: 1100}},
		{ID: 2, Code: "BYZANTINE", Traits: map[string]int32{player.TraitMoveSpeed: 950, player.TraitProductionSpeed: 1000, player.TraitHouseHP: 1200}},
		{ID: 3, Code: "PERSIAN", Traits: map[string]int32{player.TraitMoveSpeed: 1050, player.TraitProductionSpeed: 950, player.TraitGatherSpeed: 1100}},
		{ID: 4, Code: "NORSE", Traits: map[string]int32{player.TraitMoveSpeed: 1100, player.TraitProductionSpeed: 900, player.TraitLootCarry: 1150}},
	}
}

// conCivilizacion instala el catálogo y da a la ciudad del jugador su
// civilización.
func conCivilizacion(h *harness, codigo string) {
	h.t.Helper()
	h.state.SetCivilizations(catalogoDeCivilizaciones())
	h.ciudad().Civilization = codigo
}

// RN-CIV-004, RN-CIV-005: unit.move_speed fija la polilínea al dar la orden.
// Cinco pasos de hierba hacia el este: 600 ms cada uno sin rasgo, 545 nórdico,
// 571 persa y 632 bizantino. El nórdico llega antes que el bizantino por la
// misma ruta.
func TestLaVelocidadDeLaCivilizacionFijaLaPolilinea(t *testing.T) {
	casos := []struct {
		civilizacion string
		llegada      int64
	}{
		{"", 3_000}, {"ROMAN", 3_000}, {"NORSE", 2_725}, {"PERSIAN", 2_855}, {"BYZANTINE", 3_160},
	}
	for _, c := range casos {
		t.Run(c.civilizacion, func(t *testing.T) {
			h := newHarness(t)
			if c.civilizacion != "" {
				conCivilizacion(h, c.civilizacion)
			}
			h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: 1,
				Target: world.Tile{X: 17, Y: 10}})
			require.Empty(t, h.codigos())
			started := h.rec.byType(protocol.TypeUnitMovementStarted)
			require.Len(t, started, 1)
			mv := started[0].Payload.(protocol.UnitMovementStartedPayload).Movement
			assert.Equal(t, epoch+c.llegada, mv.ArrivalTimeMs)
			paso := c.llegada / 5
			for i, w := range mv.Path {
				assert.Equal(t, int64(i)*paso, w.TMs, "waypoint %d", i)
			}

			h.hasta(epoch + c.llegada + 100)
			assert.Equal(t, world.Tile{X: 17, Y: 10}, h.unitTile(1))
		})
	}
}

// production.speed fija el plazo de la obra y del entrenamiento: una casa
// romana se completa a los 13 636 ms y un aldeano romano a los 18 182; una casa
// nórdica, a los 16 667.
func TestLaProduccionDeLaCivilizacionFijaElPlazo(t *testing.T) {
	h := newHarness(t)
	conCivilizacion(h, "ROMAN")
	h.darMadera(80)
	hueco := world.Tile{X: 9, Y: 9}

	h.construir(hueco)
	require.Len(t, h.economy.builds, 1)
	assert.Equal(t, epoch+13_636, h.economy.builds[0].Building.CompletesAtMs)
	h.tick()

	inicio := h.clk.NowMs()
	h.entrenar()
	p, _, entrenando := h.state.Production(h.cityID)
	require.True(t, entrenando)
	assert.Equal(t, inicio+18_182, p.CompletesAtMs)

	h.hasta(epoch + 13_600)
	b, _ := h.state.BuildingAt(hueco)
	assert.Equal(t, building.StatusUnderConstruction, b.Status)
	h.hasta(epoch + 13_700)
	b, _ = h.state.BuildingAt(hueco)
	assert.Equal(t, building.StatusComplete, b.Status, "el tick de 13 700 ve vencidos los 13 636")

	n := newHarness(t)
	conCivilizacion(n, "NORSE")
	n.darMadera(30)
	n.construir(hueco)
	require.Len(t, n.economy.builds, 1)
	assert.Equal(t, epoch+16_667, n.economy.builds[0].Building.CompletesAtMs)
}

// economy.gather_speed fija el trabajo en el tile al llegar: un aldeano persa
// llega al bosque en 6 053 (nueve pasos de hierba a 571 y uno de bosque a 914)
// y trabaja 9 091, así que su trabajo vence en 15 144.
func TestElTrabajoDeRecoleccionPersaDuraMenos(t *testing.T) {
	h := newHarnessMadera(t)
	conCivilizacion(h, "PERSIAN")
	h.conectar()
	h.recolectar(1, bosque)

	started := h.rec.byType(protocol.TypeUnitMovementStarted)
	require.Len(t, started, 1)
	assert.Equal(t, epoch+6_053, started[0].Payload.(protocol.UnitMovementStartedPayload).Movement.ArrivalTimeMs)

	h.hasta(epoch + 6_100)
	tk, ok := h.state.Task(1)
	require.True(t, ok)
	require.Equal(t, task.PhaseWorking, tk.Phase)
	assert.Equal(t, epoch+6_053+9_091, tk.PhaseUntilMs)
}

// RN-CIV-006: una casa bizantina aguanta 180, en la vista y frente al asedio:
// el primer golpe de 8 la deja en 172 y hacen falta 23.
func TestUnaCasaBizantinaAguanta180(t *testing.T) {
	h := newHarness(t)
	_, ciudad, _ := conAsedio(h, world.Tile{X: 20, Y: 30})
	h.state.SetCivilizations(catalogoDeCivilizaciones())
	ciudad.Civilization = "BYZANTINE"

	hp, maxHP, ok := h.state.BuildingHP(casaID)
	require.True(t, ok)
	assert.Equal(t, int32(180), hp)
	assert.Equal(t, int32(180), maxHP)

	h.derribar(guerreroID, casaID)
	require.Empty(t, h.codigos())
	var vidas []int32
	vida := int32(180)
	require.True(t, h.tickHasta(900, func() bool {
		hp, enPie := vidaDe(h, casaID)
		if !enPie {
			hp = 0
		}
		if hp != vida {
			vida = hp
			vidas = append(vidas, hp)
		}
		return !enPie
	}))
	require.Len(t, vidas, 23, "180 de vida a 8 por golpe")
	assert.Equal(t, int32(172), vidas[0])
	assert.Equal(t, int32(0), vidas[22])
}

// RN-CIV-007: un guerrero nórdico carga 23. El golden del saqueo con su
// civilización: cuatro golpes de 5 y uno de 3, y deposita los 23 robados.
func TestUnGuerreroNordicoSaquea23(t *testing.T) {
	h := newHarness(t)
	_, ciudad, g := conSaqueo(h, 200, world.Tile{X: 20, Y: 30})
	conCivilizacion(h, "NORSE")
	h.darMadera(0)

	h.saquear(guerreroID, ciudad.ID)
	require.Empty(t, h.codigos())
	var stocks []int64
	stock := h.state.Stock(ciudad.ID).Amount
	require.True(t, h.tickHasta(400, func() bool {
		if s := h.state.Stock(ciudad.ID).Amount; s != stock {
			stock = s
			stocks = append(stocks, s)
		}
		_, sigue := h.state.Pillaging(guerreroID)
		return !sigue
	}))
	h.tick()
	assert.Equal(t, []int64{195, 190, 185, 180, 177}, stocks)
	assert.Equal(t, int32(23), g.Loot)
	assert.Equal(t, unit.StatusIdle, g.Status)

	h.send(simulation.MoveUnit{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: guerreroID,
		Target: world.Tile{X: 12, Y: 10}})
	h.advance(40 * time.Second)
	assert.Equal(t, int64(23), h.madera(), "INV-ECON-008: deposita exactamente lo robado")
}

// RN-CIV-009: la vista de la ciudad lleva la civilización de su dueño.
func TestLaVistaDeLaCiudadLlevaSuCivilizacion(t *testing.T) {
	h := newHarness(t)
	conCivilizacion(h, "PERSIAN")
	assert.Equal(t, "PERSIAN", simulation.CityView(h.ciudad()).Civilization)
}
