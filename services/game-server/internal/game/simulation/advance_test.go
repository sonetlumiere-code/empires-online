package simulation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/advance"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de simulación del avance de era (SPEC-ECON-STONE §4.4, ADR-030 §4 y §5).
// La ciudad 1 del arnés está en STONE_AGE con límite 20.

// darEras instala el catálogo de eras del canon (§10), como el arranque.
func (h *harness) darEras() {
	h.state.SetEras([]city.EraDefinition{
		{Code: city.EraStone, PopulationCap: 20}, {Code: city.EraBronze, PopulationCap: 50},
		{Code: city.EraIron, PopulationCap: 100}, {Code: city.EraCastle, PopulationCap: 150},
	})
}

func (h *harness) darRecursos(madera, piedra int64) {
	h.t.Helper()
	h.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: h.cityID, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: madera, Version: 1}},
		{CityID: h.cityID, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: piedra, Version: 1}},
	}})
}

func (h *harness) avanzar() {
	h.t.Helper()
	h.send(simulation.AdvanceEra{PlayerID: h.playerID, RequestID: uuid.NewString(), CityID: h.cityID})
}

func (h *harness) actualizacionesDeCiudad() []protocol.CityUpdatePayload {
	var out []protocol.CityUpdatePayload
	for _, m := range h.rec.byType(protocol.TypeCityUpdate) {
		out = append(out, m.Payload.(protocol.CityUpdatePayload))
	}
	return out
}

// RN-ECON-024 a RN-ECON-027: el avance reserva los dos recursos al ordenar, los
// debita en una transacción con un delta por recurso, se anuncia al
// confirmarse, deja la ciudad sin entrenar mientras dura y, a los 90 s, la
// ciudad pasa a BRONZE_AGE con el límite de 20 a 50.
func TestAvanzarALaEdadDeBronce(t *testing.T) {
	h := newHarness(t)
	h.darEras()
	h.darRecursos(450, 200)
	h.avanzar()
	require.Empty(t, h.codigos())
	assert.Equal(t, int64(50), h.madera(), "la madera sale al ordenar")
	assert.Equal(t, int64(0), h.state.StockOf(h.cityID, economy.ResourceStone).Amount, "y la piedra también")
	require.Len(t, h.economy.advances, 1)
	o := h.economy.advances[0]
	assert.Equal(t, city.EraStone, o.Advance.From)
	assert.Equal(t, city.EraBronze, o.Advance.To)
	assert.Equal(t, epoch+90_000, o.Advance.CompletesAtMs)
	require.Len(t, o.Stocks, 2, "un delta por recurso, en la misma transacción")
	assert.Equal(t, economy.ResourceWood, o.Stocks[0].Resource)
	assert.Equal(t, int64(-400), o.Stocks[0].Delta)
	assert.Equal(t, economy.ResourceStone, o.Stocks[1].Resource)
	assert.Equal(t, int64(-200), o.Stocks[1].Delta)

	_, confirmado, ok := h.state.Advance(h.cityID)
	require.True(t, ok)
	assert.False(t, confirmado, "hasta que vuelve la transacción, está pendiente")
	h.tick()
	_, confirmado, _ = h.state.Advance(h.cityID)
	assert.True(t, confirmado)
	ups := h.actualizacionesDeCiudad()
	require.NotEmpty(t, ups)
	ultimo := ups[len(ups)-1]
	require.NotNil(t, ultimo.Advance)
	require.NotNil(t, *ultimo.Advance)
	assert.Equal(t, "BRONZE_AGE", (*ultimo.Advance).Era)
	assert.Equal(t, epoch+90_000, (*ultimo.Advance).CompletesAtMs)

	// RN-ECON-026: mientras avanza, no entrena; un segundo avance tampoco.
	h.entrenar()
	h.avanzar()
	assert.Equal(t, []string{protocol.CodeCityBusy, protocol.CodeCityBusy}, h.codigos())
	assert.Equal(t, int64(50), h.madera(), "un rechazo no gasta nada")

	h.hasta(epoch + 89_900)
	assert.Equal(t, city.EraStone, h.ciudad().Era, "un tick antes, sigue en Piedra")
	h.hasta(epoch + 90_000)
	assert.Equal(t, city.EraBronze, h.ciudad().Era)
	assert.Equal(t, int32(50), h.ciudad().PopulationLimit, "20 + 30: el límite de la era nueva")
	_, _, ok = h.state.Advance(h.cityID)
	assert.False(t, ok, "el avance terminó")
	ups = h.actualizacionesDeCiudad()
	fin := ups[len(ups)-1]
	require.NotNil(t, fin.Era)
	assert.Equal(t, "BRONZE_AGE", *fin.Era)
	require.NotNil(t, fin.PopulationLimit)
	assert.Equal(t, int32(50), *fin.PopulationLimit)
	require.NotNil(t, fin.Advance)
	assert.Nil(t, *fin.Advance, "advance: null")
	require.Len(t, h.economy.advanceEnds, 1)
	assert.Equal(t, simulation.AdvanceCompletion{
		CityID: h.cityID, PlayerID: h.playerID, From: city.EraStone, To: city.EraBronze,
		Tick: h.economy.advanceEnds[0].Tick,
	}, h.economy.advanceEnds[0])

	// Terminado el avance, la ciudad vuelve a entrenar.
	h.entrenar()
	assert.Len(t, h.codigos(), 2, "sin rechazo nuevo")
}

// INV-ECON-004: las casas siguen sumando sobre el límite de la era nueva.
func TestElAvanceSumaSobreLasCasas(t *testing.T) {
	h := newHarness(t)
	h.darEras()
	h.ciudad().PopulationLimit = 30 // 20 de la era + dos casas completas
	h.darRecursos(400, 200)
	h.avanzar()
	h.hasta(epoch + 90_000)
	assert.Equal(t, int32(60), h.ciudad().PopulationLimit, "50 + 10")
}

// RN-ECON-024 en la simulación: cada rechazo con su código y sin gastar nada.
func TestRechazosDelAvanceEnLaSimulacion(t *testing.T) {
	h := newHarness(t)
	h.darEras()
	h.darRecursos(450, 199)

	h.send(simulation.AdvanceEra{PlayerID: h.playerID, RequestID: uuid.NewString(), CityID: 99})
	h.avanzar()
	assert.Equal(t, []string{protocol.CodeCityNotFound, protocol.CodeInsufficientResources}, h.codigos())
	assert.Equal(t, int64(450), h.madera(), "sin piedra suficiente no se gasta madera")

	h.darRecursos(450, 200)
	h.entrenar()
	h.avanzar()
	assert.Equal(t, protocol.CodeCityBusy, h.codigos()[2], "con un entrenamiento en curso")

	b := newHarness(t)
	b.darEras()
	b.darRecursos(400, 200)
	b.ciudad().Era = city.EraBronze
	b.avanzar()
	assert.Equal(t, []string{protocol.CodeNotImplemented}, b.codigos(), "desde Bronce no hay avance")
	assert.Empty(t, b.economy.advances)
}

// INV-ECON-002 y RN-ECON-025: si la transacción rechaza el avance, los dos
// recursos vuelven a la ciudad, y nadie ve un avance que no existe.
func TestUnAvanceRechazadoDevuelveLosDosRecursos(t *testing.T) {
	h := newHarness(t)
	h.darEras()
	h.darRecursos(400, 200)
	h.economy.advanceErr = advance.ErrCityBusy
	h.avanzar()
	h.tick()
	assert.Equal(t, []string{protocol.CodeCityBusy}, h.codigos())
	assert.Equal(t, int64(400), h.madera())
	assert.Equal(t, int64(200), h.state.StockOf(h.cityID, economy.ResourceStone).Amount)
	_, _, ok := h.state.Advance(h.cityID)
	assert.False(t, ok)
	for _, u := range h.actualizacionesDeCiudad() {
		assert.Nil(t, u.Advance, "ningún city.update anuncia el avance rechazado")
	}
}

// ADR-030 §3: los depósitos sin volcar de cada recurso del coste viajan con el
// encargo, en su delta.
func TestElAvanceLlevaLosDepositosSinVolcar(t *testing.T) {
	h := newHarnessPiedra(t)
	h.darEras()
	h.darRecursos(400, 190)
	h.conectar()
	h.recolectar(1, colina)
	h.hasta(epoch + 22_500)
	require.Equal(t, int64(200), h.state.StockOf(h.cityID, economy.ResourceStone).Amount)
	require.Equal(t, int64(10), h.state.UnflushedOf(h.cityID, economy.ResourceStone))

	h.avanzar()
	require.Len(t, h.economy.advances, 1)
	st := h.economy.advances[0].Stocks
	assert.Equal(t, int64(-400), st[0].Delta)
	assert.Equal(t, int64(10-200), st[1].Delta, "los 10 depositados viajan con el débito")
	assert.Zero(t, h.state.UnflushedOf(h.cityID, economy.ResourceStone))
}

// ADR-030 §5: un fin de avance que la cola descarta se reencola en el volcado
// siguiente; la RAM no deshace la era.
func TestUnFinDeAvanceDescartadoSeReencola(t *testing.T) {
	h := newHarness(t)
	h.darEras()
	h.darRecursos(400, 200)
	h.avanzar()
	h.tick()
	h.persister.permanent = true
	h.economy.advanceEndErr = errBaseCaida
	h.hasta(epoch + 90_100)
	assert.Equal(t, city.EraBronze, h.ciudad().Era, "la RAM no deshace el avance")
	assert.Empty(t, h.economy.advanceEnds)

	h.economy.advanceEndErr = nil
	h.sim.FlushDirty()
	require.Len(t, h.economy.advanceEnds, 1, "el volcado lo reencola")
	assert.Equal(t, city.EraBronze, h.economy.advanceEnds[0].To)
}

// INV-ECON-005: un avance a medias termina tras un reinicio en su instante; uno
// que venció con el servidor caído termina en el primer tick.
func TestReinicioConUnAvanceAMedias(t *testing.T) {
	cargar := func(t *testing.T, vence int64) *harness {
		h := newHarness(t)
		h.darEras()
		h.state.SetEconomy(simulation.EconomyLoad{Advances: []advance.Advance{{
			CityID: h.cityID, From: city.EraStone, To: city.EraBronze, CompletesAtMs: vence, RequestID: "r",
		}}})
		_, confirmado, ok := h.state.Advance(h.cityID)
		require.True(t, ok)
		require.True(t, confirmado, "lo leído de la base está confirmado")
		return h
	}

	t.Run("vence después del arranque", func(t *testing.T) {
		h := cargar(t, epoch+5_000)
		h.entrenar()
		assert.Equal(t, []string{protocol.CodeCityBusy}, h.codigos(), "ocupa la ciudad")
		h.hasta(epoch + 4_900)
		assert.Equal(t, city.EraStone, h.ciudad().Era)
		h.hasta(epoch + 5_000)
		assert.Equal(t, city.EraBronze, h.ciudad().Era)
		assert.Equal(t, int32(50), h.ciudad().PopulationLimit)
	})

	t.Run("venció durante la caída", func(t *testing.T) {
		h := cargar(t, epoch-1_000)
		h.tick()
		assert.Equal(t, city.EraBronze, h.ciudad().Era, "en el primer tick")
		assert.Equal(t, int32(50), h.ciudad().PopulationLimit)
		require.Len(t, h.economy.advanceEnds, 1)
	})
}

// INV-CITY-018: la era no retrocede por la ausencia ni por la protección.
func TestLaEraNoRetrocede(t *testing.T) {
	h := newHarness(t)
	h.darEras()
	h.darRecursos(400, 200)
	h.conectar()
	h.avanzar()
	h.hasta(epoch + 90_000)
	require.Equal(t, city.EraBronze, h.ciudad().Era)

	h.send(simulation.PlayerDisconnected{PlayerID: h.playerID, SessionID: sesion})
	h.advance(400 * time.Second)
	require.Equal(t, city.PresenceProtected, h.ciudad().PresenceState)
	assert.Equal(t, city.EraBronze, h.ciudad().Era)
	assert.Equal(t, int32(50), h.ciudad().PopulationLimit)
}
