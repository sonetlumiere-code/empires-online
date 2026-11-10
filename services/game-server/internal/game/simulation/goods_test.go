package simulation_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/craft"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Golden «bienes» de SPEC-CIVILIZATION-GOODS (§9). La ciudad del harness está en
// (10,10); su zona urbana va de (9,9) a (11,11).

const tallerID = int64(70)

var tileTaller = world.Tile{X: 11, Y: 11}

// almacen instala el stock de varios recursos de una ciudad.
func (h *harness) almacenDe(cityID int64, cantidades map[economy.Resource]int64) {
	h.t.Helper()
	var rows []simulation.StockRow
	for _, r := range economy.Resources() {
		if n, ok := cantidades[r]; ok {
			rows = append(rows, simulation.StockRow{CityID: cityID, Stock: economy.Stock{Resource: r, Amount: n, Version: 1}})
		}
	}
	h.state.SetEconomy(simulation.EconomyLoad{Stocks: rows})
}

// conTaller da a una ciudad un taller completo.
func (h *harness) conTaller(cityID, id int64, at world.Tile) {
	h.t.Helper()
	h.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{{
		ID: id, CityID: cityID, Type: building.TypeWorkshop, X: at.X, Y: at.Y,
		Status: building.StatusComplete, CompletesAtMs: h.clk.NowMs(),
	}}})
}

func (h *harness) elaborar() {
	h.t.Helper()
	h.send(simulation.CraftGoods{PlayerID: h.playerID, RequestID: uuid.NewString(), CityID: h.cityID})
}

func (h *harness) encargarTaller(at world.Tile) {
	h.t.Helper()
	h.send(simulation.BuildBuilding{PlayerID: h.playerID, RequestID: uuid.NewString(), CityID: h.cityID, Type: building.TypeWorkshop, Tile: at})
}

// bronce prepara una ciudad romana (o de la civilización dada) en Bronce.
func bronce(t *testing.T, civilizacion string) *harness {
	t.Helper()
	h := newHarness(t)
	h.darEras()
	conCivilizacion(h, civilizacion)
	h.ciudad().Era = city.EraBronze
	h.ciudad().PopulationLimit = 50
	return h
}

// RN-GOODS-003 y Q6: el taller pide Bronce, cuesta 100 de madera y 50 de
// piedra, tarda 45 s con el rasgo (40 909 ms un romano) y es único.
func TestElTallerPideBronceCuestaDosRecursosYEsUnico(t *testing.T) {
	piedra := newHarness(t)
	conCivilizacion(piedra, "ROMAN")
	piedra.almacenDe(piedra.cityID, map[economy.Resource]int64{economy.ResourceWood: 500, economy.ResourceStone: 500})
	piedra.encargarTaller(tileTaller)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, piedra.codigos(), "en Piedra no hay taller")

	h := bronce(t, "ROMAN")
	h.almacenDe(h.cityID, map[economy.Resource]int64{economy.ResourceWood: 99, economy.ResourceStone: 50})
	h.encargarTaller(tileTaller)
	assert.Equal(t, []string{protocol.CodeInsufficientResources}, h.codigos())

	h.almacenDe(h.cityID, map[economy.Resource]int64{economy.ResourceWood: 300, economy.ResourceStone: 100})
	h.encargarTaller(tileTaller)
	require.Len(t, h.economy.builds, 1)
	orden := h.economy.builds[0]
	assert.Equal(t, building.TypeWorkshop, orden.Building.Type)
	assert.Equal(t, epoch+40_909, orden.Building.CompletesAtMs)
	require.Len(t, orden.Stocks, 2)
	assert.Equal(t, int64(-100), orden.Stocks[0].Delta)
	assert.Equal(t, int64(-50), orden.Stocks[1].Delta)
	assert.Equal(t, int64(200), h.state.StockOf(h.cityID, economy.ResourceWood).Amount)
	assert.Equal(t, int64(50), h.state.StockOf(h.cityID, economy.ResourceStone).Amount)

	h.rec.reset()
	h.encargarTaller(world.Tile{X: 9, Y: 9})
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "INV-ECON-010: uno por ciudad")
	assert.Len(t, h.economy.builds, 1)

	h.hasta(epoch + 41_000)
	b, ok := h.state.BuildingAt(tileTaller)
	require.True(t, ok)
	assert.Equal(t, building.StatusComplete, b.Status)
	assert.Equal(t, int32(50), h.ciudad().PopulationLimit, "el taller no da población")
}

// RN-GOODS-002 y RN-GOODS-004: un romano elabora 5 de vino por 30 de madera y
// 15 de piedra, en 27 273 ms; la entrega acredita el vino y lo escribe con su
// delta.
func TestUnRomanoElaboraVino(t *testing.T) {
	h := bronce(t, "ROMAN")
	h.conTaller(h.cityID, tallerID, tileTaller)
	h.almacenDe(h.cityID, map[economy.Resource]int64{economy.ResourceWood: 100, economy.ResourceStone: 40})

	h.elaborar()
	require.Empty(t, h.codigos())
	require.Len(t, h.economy.crafts, 1)
	orden := h.economy.crafts[0]
	assert.Equal(t, economy.ResourceWine, orden.Craft.Good)
	assert.Equal(t, int32(5), orden.Craft.Amount)
	assert.Equal(t, epoch+27_273, orden.Craft.CompletesAtMs)
	h.tick()
	cr, confirmada, ok := h.state.Craft(h.cityID)
	require.True(t, ok)
	assert.True(t, confirmada)
	assert.Equal(t, economy.ResourceWine, cr.Good)
	assert.Equal(t, int64(70), h.state.StockOf(h.cityID, economy.ResourceWood).Amount)
	assert.Equal(t, int64(25), h.state.StockOf(h.cityID, economy.ResourceStone).Amount)

	h.rec.reset()
	h.hasta(epoch + 27_200)
	assert.Zero(t, h.state.StockOf(h.cityID, economy.ResourceWine).Amount)
	h.hasta(epoch + 27_300)
	assert.Equal(t, int64(5), h.state.StockOf(h.cityID, economy.ResourceWine).Amount)
	_, _, sigue := h.state.Craft(h.cityID)
	assert.False(t, sigue)
	entregas := h.economy.craftDeliveries()
	require.Len(t, entregas, 1)
	assert.Equal(t, economy.ResourceWine, entregas[0].Stock.Resource)
	assert.Equal(t, int64(5), entregas[0].Stock.Delta)
	var fin bool
	for _, u := range h.actualizacionesDeCiudad() {
		if u.Craft != nil && *u.Craft == nil && u.Stocks != nil && u.Stocks.Wine == 5 {
			fin = true
		}
	}
	assert.True(t, fin, "un city.update con el vino y la elaboración en null")
}

// Q12: el nórdico elabora pieles, un 10 % más lento (33 333 ms).
func TestUnNordicoElaboraPieles(t *testing.T) {
	h := bronce(t, "NORSE")
	h.conTaller(h.cityID, tallerID, tileTaller)
	h.almacenDe(h.cityID, map[economy.Resource]int64{economy.ResourceWood: 30, economy.ResourceStone: 15})
	h.elaborar()
	require.Len(t, h.economy.crafts, 1)
	assert.Equal(t, economy.ResourceFurs, h.economy.crafts[0].Craft.Good)
	assert.Equal(t, epoch+33_333, h.economy.crafts[0].Craft.CompletesAtMs)
	h.hasta(epoch + 33_400)
	assert.Equal(t, int64(5), h.state.StockOf(h.cityID, economy.ResourceFurs).Amount)
	assert.Zero(t, h.state.StockOf(h.cityID, economy.ResourceWine).Amount, "INV-ECON-009: sólo su bien")
}

// RN-GOODS-004 y RN-GOODS-005: sin taller completo, INVALID_TARGET; con una en
// curso, CITY_BUSY; y elaborar no impide entrenar.
func TestRechazosDeLaElaboracionYQueNoBloqueaElEntrenamiento(t *testing.T) {
	h := bronce(t, "ROMAN")
	h.almacenDe(h.cityID, map[economy.Resource]int64{economy.ResourceWood: 200, economy.ResourceStone: 100})
	h.elaborar()
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "sin taller")

	h.state.SetEconomy(simulation.EconomyLoad{Buildings: []building.Building{{
		ID: tallerID, CityID: h.cityID, Type: building.TypeWorkshop, X: tileTaller.X, Y: tileTaller.Y,
		Status: building.StatusUnderConstruction, CompletesAtMs: epoch + 3_600_000,
	}}})
	h.rec.reset()
	h.elaborar()
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "con el taller en obra")

	g := bronce(t, "ROMAN")
	g.conTaller(g.cityID, tallerID, tileTaller)
	g.almacenDe(g.cityID, map[economy.Resource]int64{economy.ResourceWood: 200, economy.ResourceStone: 100})
	g.elaborar()
	g.tick()
	g.elaborar()
	assert.Equal(t, []string{protocol.CodeCityBusy}, g.codigos(), "INV-ECON-010: una a la vez")
	g.entrenar()
	assert.Equal(t, []string{protocol.CodeCityBusy}, g.codigos(), "el entrenamiento no se rechaza")
	require.Len(t, g.economy.trains, 1)
}

// RN-GOODS-006, INV-ECON-011 y Q5: un romano con 100 de vino, 25 de seda y 50 de
// pieles avanza a Hierro pagando 800, 400, 25 de seda y 35 de pieles; nunca vino.
// A los 180 s, Hierro y límite 100.
func TestElAvanceAHierroCobraBienesAjenosYNuncaElPropio(t *testing.T) {
	h := bronce(t, "ROMAN")
	h.almacenDe(h.cityID, map[economy.Resource]int64{
		economy.ResourceWood: 800, economy.ResourceStone: 400,
		economy.ResourceWine: 100, economy.ResourceSilk: 25, economy.ResourceFurs: 50,
	})
	h.avanzar()
	require.Empty(t, h.codigos())
	require.Len(t, h.economy.advances, 1)
	pagado := map[economy.Resource]int64{}
	for _, d := range h.economy.advances[0].Stocks {
		pagado[d.Resource] = -d.Delta
	}
	assert.Equal(t, map[economy.Resource]int64{
		economy.ResourceWood: 800, economy.ResourceStone: 400, economy.ResourceSilk: 25, economy.ResourceFurs: 35,
	}, pagado)
	assert.Equal(t, int64(100), h.state.StockOf(h.cityID, economy.ResourceWine).Amount)
	assert.Equal(t, int64(15), h.state.StockOf(h.cityID, economy.ResourceFurs).Amount)

	h.hasta(epoch + 180_100)
	assert.Equal(t, city.EraIron, h.ciudad().Era)
	assert.Equal(t, int32(100), h.ciudad().PopulationLimit)

	sin := bronce(t, "ROMAN")
	sin.almacenDe(sin.cityID, map[economy.Resource]int64{
		economy.ResourceWood: 800, economy.ResourceStone: 400, economy.ResourceWine: 500, economy.ResourceSpices: 59,
	})
	sin.avanzar()
	assert.Equal(t, []string{protocol.CodeInsufficientResources}, sin.codigos(), "el vino propio no cuenta")
	assert.Empty(t, sin.economy.advances)
}

// RN-GOODS-007 y Q10: derribar el taller de un vecino pierde su elaboración
// en curso, sin entregarla nunca.
func TestDerribarElTallerPierdeLaElaboracion(t *testing.T) {
	h := newHarness(t)
	_, vecina, _ := conAsedio(h, world.Tile{X: 20, Y: 30})
	h.state.SetCivilizations(catalogoDeCivilizaciones())
	vecina.Civilization = "BYZANTINE"
	vecina.Era = city.EraBronze
	tile := world.Tile{X: 21, Y: 39}
	h.conTaller(vecina.ID, tallerID, tile)
	h.state.SetEconomy(simulation.EconomyLoad{Crafts: []craft.Craft{{
		CityID: vecina.ID, Good: economy.ResourceSilk, Amount: 5, CompletesAtMs: epoch + 3_600_000, RequestID: "r",
	}}})
	_, _, ok := h.state.Craft(vecina.ID)
	require.True(t, ok)
	hp, maxHP, ok := h.state.BuildingHP(tallerID)
	require.True(t, ok)
	assert.Equal(t, int32(200), maxHP, "el taller aguanta 200, sin rasgo de casa")
	assert.Equal(t, int32(200), hp)

	h.derribar(guerreroID, tallerID)
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(900, func() bool {
		_, enPie := h.state.Building(tallerID)
		return !enPie
	}))
	_, _, sigue := h.state.Craft(vecina.ID)
	assert.False(t, sigue, "la elaboración se pierde con el taller")
	h.hasta(epoch + 3_700_000)
	assert.Zero(t, h.state.StockOf(vecina.ID, economy.ResourceSilk).Amount)
	assert.Empty(t, h.economy.craftDeliveries())
}

// ADR-036 §4: una entrega que no se escribe se reintenta en el siguiente
// volcado con una versión nueva, y el bien sigue en la RAM.
func TestUnaEntregaQueFallaSeReintentaConVersionNueva(t *testing.T) {
	h := bronce(t, "ROMAN")
	h.persister.permanent = true
	h.conTaller(h.cityID, tallerID, tileTaller)
	h.almacenDe(h.cityID, map[economy.Resource]int64{economy.ResourceWood: 30, economy.ResourceStone: 15})
	h.elaborar()
	h.economy.deliverErr = errors.New("base caída")
	h.hasta(epoch + 27_300)
	assert.Equal(t, int64(5), h.state.StockOf(h.cityID, economy.ResourceWine).Amount, "optimista: el vino ya está")
	assert.Empty(t, h.economy.craftDeliveries())

	version := h.state.StockOf(h.cityID, economy.ResourceWine).Version
	h.economy.deliverErr = nil
	h.hasta(epoch + 27_300 + 6_000) // un volcado (50 ticks)
	entregas := h.economy.craftDeliveries()
	require.Len(t, entregas, 1)
	assert.Equal(t, int64(5), entregas[0].Stock.Delta)
	assert.Greater(t, entregas[0].Stock.Version, version, "con una versión nueva")
}
