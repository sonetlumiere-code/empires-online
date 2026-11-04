package simulation_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/trade"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests del comercio (SPEC-TRADE §9). Mundo de newHarness: el jugador del arnés
// con su ciudad 1 en (10,10) y un vecino de conVecinos con la ciudad 2 en (20,40).

// fakeTrade hace de base: da ids a las ofertas y anota cada escritura, en orden.
type fakeTrade struct {
	mu        sync.Mutex
	nextID    int64
	writes    []simulation.TradeWrite
	accepts   []simulation.TradeAcceptance
	log       []string
	err       error
	acceptErr error
}

func (f *fakeTrade) ApplyOffer(_ context.Context, w simulation.TradeWrite) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.writes = append(f.writes, w)
	f.log = append(f.log, w.Event)
	if w.Insert {
		f.nextID++
		return f.nextID, nil
	}
	return w.Offer.ID, nil
}

func (f *fakeTrade) AcceptTrade(_ context.Context, a simulation.TradeAcceptance) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.acceptErr != nil {
		return f.acceptErr
	}
	f.accepts = append(f.accepts, a)
	f.log = append(f.log, simulation.EventTradeAccepted)
	return nil
}

func (f *fakeTrade) events() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

func madera(n int64) economy.Cost { return economy.Cost{Resource: economy.ResourceWood, Amount: n} }
func piedra(n int64) economy.Cost { return economy.Cost{Resource: economy.ResourceStone, Amount: n} }

// vigente es un tratado ACTIVE entre dos jugadores, con el par ya ordenado.
func vigente(id int64, x, y uuid.UUID, tipo diplomacy.Type) diplomacy.Treaty {
	a, b := diplomacy.CanonicalPair(x, y)
	return diplomacy.Treaty{
		ID: id, PlayerA: a, PlayerB: b, Type: tipo, Status: diplomacy.StatusActive,
		AllowsGarrison: tipo.GrantsGarrison(), ProposedAt: time.UnixMilli(epoch).UTC(),
	}
}

// conComercio añade un vecino con el que el jugador del arnés tiene un tratado
// vigente del tipo dado.
func conComercio(h *harness, tipo diplomacy.Type) uuid.UUID {
	h.t.Helper()
	vecino := conVecinos(h, 1)[0]
	h.state.SetTreaties([]diplomacy.Treaty{vigente(1, h.playerID, vecino, tipo)}, nil)
	return vecino
}

// almacen fija la madera y la piedra de una ciudad.
func (h *harness) almacen(cityID, wood, stone int64) {
	h.t.Helper()
	h.state.SetEconomy(simulation.EconomyLoad{Stocks: []simulation.StockRow{
		{CityID: cityID, Stock: economy.Stock{Resource: economy.ResourceWood, Amount: wood, Version: 1}},
		{CityID: cityID, Stock: economy.Stock{Resource: economy.ResourceStone, Amount: stone, Version: 1}},
	}})
}

func (h *harness) recursos(cityID int64) (wood, stone int64) {
	return h.state.StockOf(cityID, economy.ResourceWood).Amount, h.state.StockOf(cityID, economy.ResourceStone).Amount
}

func (h *harness) ofrecer(de, a uuid.UUID, give, want economy.Costs) {
	h.t.Helper()
	h.send(simulation.ProposeTrade{PlayerID: de, RequestID: uuid.NewString(), Target: a, Give: give, Want: want})
	h.tick() // la confirmación vuelve por el buzón
}

func (h *harness) responderTrato(quien uuid.UUID, id int64, acepta bool) {
	h.t.Helper()
	h.send(simulation.RespondTrade{PlayerID: quien, RequestID: uuid.NewString(), OfferID: id, Accept: acepta})
	h.tick()
}

func (h *harness) retirarTrato(quien uuid.UUID, id int64) {
	h.t.Helper()
	h.send(simulation.CancelTrade{PlayerID: quien, RequestID: uuid.NewString(), OfferID: id})
	h.tick()
}

// tratosPara son los trade.update recibidos por un jugador.
func (h *harness) tratosPara(p uuid.UUID) []protocol.TradeOfferView {
	var out []protocol.TradeOfferView
	for _, m := range h.rec.byType(protocol.TypeTradeUpdate) {
		if m.Kind == "player" && m.PlayerID == p {
			out = append(out, m.Payload.(protocol.TradeUpdatePayload).Offer)
		}
	}
	return out
}

// primeraOferta es el id que el almacén falso da a la primera oferta.
const primeraOferta int64 = 2001

// Golden «comercio» (RN-TRADE-001 a RN-TRADE-004, INV-TRADE-001): con un tratado
// TRADE, una oferta de 300 de madera por 100 de piedra, su aceptación, y los dos
// almacenes en el tick exacto. Lo recibido no se puede gastar hasta que la base
// confirma; lo que se mueve se conserva.
func TestGoldenComercioOfertaAceptacionYLosDosAlmacenes(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 0)
	h.almacen(2, 0, 100)

	h.ofrecer(h.playerID, vecino, economy.Costs{madera(300)}, economy.Costs{piedra(100)})
	require.Empty(t, h.codigos())
	o, ok := h.state.TradeOffer(primeraOferta)
	require.True(t, ok, "la oferta queda pendiente en la RAM")
	assert.Equal(t, trade.StatusProposed, o.Status)
	assert.Equal(t, time.UnixMilli(h.clk.NowMs()-100).Add(24*time.Hour).UTC(), o.ExpiresAt, "Q5: 24 h")
	w, s := h.recursos(1)
	assert.Equal(t, [2]int64{300, 0}, [2]int64{w, s}, "Q2: proponer no reserva nada")

	propia := h.tratosPara(h.playerID)
	ajena := h.tratosPara(vecino)
	require.Len(t, propia, 1)
	require.Len(t, ajena, 1)
	assert.True(t, propia[0].ProposedByMe)
	assert.False(t, ajena[0].ProposedByMe)
	assert.Equal(t, []protocol.ResourceAmount{{Resource: "WOOD", Amount: 300}}, propia[0].Give)
	assert.Equal(t, []protocol.ResourceAmount{{Resource: "STONE", Amount: 100}}, propia[0].Get)
	assert.Equal(t, []protocol.ResourceAmount{{Resource: "STONE", Amount: 100}}, ajena[0].Give, "la vista desde cada lado")
	assert.Equal(t, []protocol.ResourceAmount{{Resource: "WOOD", Amount: 300}}, ajena[0].Get)

	// Las versiones de los cuatro stocks antes de aceptar: cada delta del trato
	// tiene que llevar una mayor, también el recurso que una ciudad sólo recibe,
	// o la base lo daría por escrito (ADR-032 §2).
	antes := map[[2]any]int64{}
	for _, c := range []int64{1, 2} {
		for _, r := range economy.Resources() {
			antes[[2]any{c, r}] = h.state.StockOf(c, r).Version
		}
	}

	// La aceptación: en su tick, cada parte ya no tiene lo que da, y lo que
	// recibe todavía no ha llegado.
	h.send(simulation.RespondTrade{PlayerID: vecino, RequestID: uuid.NewString(), OfferID: primeraOferta, Accept: true})
	w, s = h.recursos(1)
	assert.Equal(t, [2]int64{0, 0}, [2]int64{w, s}, "la madera reservada; la piedra, sin confirmar")
	w, s = h.recursos(2)
	assert.Equal(t, [2]int64{0, 0}, [2]int64{w, s})

	h.tick()
	require.Empty(t, h.codigos())
	w, s = h.recursos(1)
	assert.Equal(t, [2]int64{0, 100}, [2]int64{w, s})
	w, s = h.recursos(2)
	assert.Equal(t, [2]int64{300, 0}, [2]int64{w, s})
	_, sigue := h.state.TradeOffer(primeraOferta)
	assert.False(t, sigue, "la oferta aceptada sale de la RAM")
	assert.Equal(t, []string{simulation.EventTradeProposed, simulation.EventTradeAccepted}, h.trade.events())

	// INV-TRADE-001: los deltas del trato conservan cada recurso.
	require.Len(t, h.trade.accepts, 1)
	a := h.trade.accepts[0]
	assert.Equal(t, trade.StatusAccepted, a.Offer.Status)
	assert.NotEmpty(t, a.Offer.AcceptedRequestID)
	assert.Equal(t, vecino, a.By)
	neto := map[economy.Resource]int64{}
	for _, d := range a.Stocks {
		neto[d.Resource] += d.Delta
	}
	assert.Equal(t, map[economy.Resource]int64{economy.ResourceWood: 0, economy.ResourceStone: 0}, neto)
	assert.Len(t, a.Stocks, 4, "dos recursos en dos ciudades")
	for _, d := range a.Stocks {
		assert.Greater(t, d.Version, antes[[2]any{d.CityID, d.Resource}],
			"la escritura de %s en la ciudad %d va por delante de la última", d.Resource, d.CityID)
	}

	ultima := h.tratosPara(vecino)
	assert.Equal(t, "ACCEPTED", ultima[len(ultima)-1].Status)
	ultima = h.tratosPara(h.playerID)
	assert.Equal(t, "ACCEPTED", ultima[len(ultima)-1].Status)
}

// RN-TRADE-003 y Q2: si al aceptar a alguna de las dos partes no le alcanza, la
// aceptación se rechaza, no se mueve nada y la oferta sigue pendiente.
func TestAceptarSinAlcanzarDejaLaOfertaPendiente(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 0)
	h.almacen(2, 0, 100)
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(300)}, economy.Costs{piedra(100)})

	h.almacen(1, 200, 0) // quien ofrece gastó madera
	h.responderTrato(vecino, primeraOferta, true)
	assert.Equal(t, []string{protocol.CodeInsufficientResources}, h.codigos())
	_, sigue := h.state.TradeOffer(primeraOferta)
	assert.True(t, sigue, "la oferta sigue pendiente")

	h.almacen(1, 300, 0)
	h.almacen(2, 0, 99) // y ahora a quien acepta le falta piedra
	h.responderTrato(vecino, primeraOferta, true)
	assert.Equal(t, []string{protocol.CodeInsufficientResources, protocol.CodeInsufficientResources}, h.codigos())
	_, sigue = h.state.TradeOffer(primeraOferta)
	assert.True(t, sigue)

	assert.Empty(t, h.trade.accepts, "no se escribió ningún trato")
	w, s := h.recursos(1)
	assert.Equal(t, [2]int64{300, 0}, [2]int64{w, s})
	w, s = h.recursos(2)
	assert.Equal(t, [2]int64{0, 99}, [2]int64{w, s})
}

// SPEC-TRADE §5, INV-TRADE-003 e INV-TRADE-004: cada rechazo, con su código.
func TestRechazosDelComercio(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 300)
	h.almacen(2, 300, 300)

	casos := []struct {
		nombre string
		cmd    simulation.ProposeTrade
		code   string
	}{
		{"consigo mismo", simulation.ProposeTrade{PlayerID: h.playerID, Target: h.playerID,
			Give: economy.Costs{madera(1)}, Want: economy.Costs{piedra(1)}}, protocol.CodeInvalidTarget},
		{"con alguien que no existe", simulation.ProposeTrade{PlayerID: h.playerID, Target: uuid.New(),
			Give: economy.Costs{madera(1)}, Want: economy.Costs{piedra(1)}}, protocol.CodeInvalidTarget},
		{"el mismo recurso en los dos lados", simulation.ProposeTrade{PlayerID: h.playerID, Target: vecino,
			Give: economy.Costs{madera(1)}, Want: economy.Costs{madera(2)}}, protocol.CodeInvalidTarget},
		{"sin lo que se ofrece", simulation.ProposeTrade{PlayerID: h.playerID, Target: vecino,
			Give: economy.Costs{madera(301)}, Want: economy.Costs{piedra(1)}}, protocol.CodeInsufficientResources},
	}
	for _, c := range casos {
		antes := len(h.codigos())
		cmd := c.cmd
		cmd.RequestID = uuid.NewString()
		h.send(cmd)
		require.Len(t, h.codigos(), antes+1, c.nombre)
		assert.Equal(t, c.code, h.codigos()[antes], c.nombre)
	}
	assert.Empty(t, h.trade.events(), "ningún rechazo escribe nada")

	// INV-TRADE-003: con una oferta pendiente, ninguna otra en ninguna dirección.
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(10)}, economy.Costs{piedra(5)})
	require.Len(t, h.trade.events(), 1)
	for _, de := range [][2]uuid.UUID{{h.playerID, vecino}, {vecino, h.playerID}} {
		antes := len(h.codigos())
		h.ofrecer(de[0], de[1], economy.Costs{madera(1)}, economy.Costs{piedra(1)})
		require.Len(t, h.codigos(), antes+1)
		assert.Equal(t, protocol.CodeInvalidTarget, h.codigos()[antes])
	}

	// INV-TRADE-004: quien ofrece no acepta su oferta, ni el otro la retira.
	antes := len(h.codigos())
	h.responderTrato(h.playerID, primeraOferta, true)
	h.retirarTrato(vecino, primeraOferta)
	h.responderTrato(vecino, 9999, true)
	assert.Equal(t, []string{protocol.CodeInvalidTarget, protocol.CodeInvalidTarget, protocol.CodeInvalidTarget},
		h.codigos()[antes:])
	_, sigue := h.state.TradeOffer(primeraOferta)
	assert.True(t, sigue)
	assert.Len(t, h.trade.events(), 1)
}

// INV-TRADE-002: sin un tratado vigente no se ofrece, y un pacto o una alianza
// dejan comerciar igual que TRADE.
func TestSinTratadoNoSeComercia(t *testing.T) {
	h := newHarness(t)
	vecino := conVecinos(h, 1)[0]
	h.almacen(1, 300, 0)
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(10)}, economy.Costs{piedra(5)})
	assert.Equal(t, []string{protocol.CodeTreatyRequired}, h.codigos())

	for _, tipo := range []diplomacy.Type{diplomacy.TypeNonAggression, diplomacy.TypeAlliance} {
		g := newHarness(t)
		otro := conComercio(g, tipo)
		g.almacen(1, 300, 0)
		g.ofrecer(g.playerID, otro, economy.Costs{madera(10)}, economy.Costs{piedra(5)})
		assert.Empty(t, g.codigos(), "%s deja comerciar", tipo)
	}
}

// RN-TRADE-002 y RN-TRADE-005: rechazar y retirar cierran la oferta sin mover
// nada y avisan a los dos.
func TestRechazarYRetirarCierranLaOferta(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 0)

	h.ofrecer(h.playerID, vecino, economy.Costs{madera(10)}, economy.Costs{piedra(5)})
	h.responderTrato(vecino, primeraOferta, false)
	_, sigue := h.state.TradeOffer(primeraOferta)
	assert.False(t, sigue)
	ultima := h.tratosPara(h.playerID)
	assert.Equal(t, "REJECTED", ultima[len(ultima)-1].Status)

	h.ofrecer(h.playerID, vecino, economy.Costs{madera(10)}, economy.Costs{piedra(5)})
	h.retirarTrato(h.playerID, primeraOferta+1)
	_, sigue = h.state.TradeOffer(primeraOferta + 1)
	assert.False(t, sigue)
	ultima = h.tratosPara(vecino)
	assert.Equal(t, "WITHDRAWN", ultima[len(ultima)-1].Status)

	assert.Equal(t, []string{simulation.EventTradeProposed, simulation.EventTradeRejected,
		simulation.EventTradeProposed, simulation.EventTradeWithdrawn}, h.trade.events())
	assert.Empty(t, h.codigos())
	w, _ := h.recursos(1)
	assert.Equal(t, int64(300), w)
}

// RN-TRADE-006: una oferta sin respuesta caduca a las 24 h, en el barrido de un
// segundo, y avisa a los dos.
func TestLaOfertaCaducaASuPlazo(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 0)
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(10)}, economy.Costs{piedra(5)})
	o, _ := h.state.TradeOffer(primeraOferta)

	h.hasta(o.ExpiresAt.UnixMilli() - 1000)
	_, sigue := h.state.TradeOffer(primeraOferta)
	require.True(t, sigue, "un segundo antes, sigue")

	h.hasta(o.ExpiresAt.UnixMilli() + 1000)
	_, sigue = h.state.TradeOffer(primeraOferta)
	assert.False(t, sigue, "caducó")
	require.Len(t, h.trade.writes, 2)
	fin := h.trade.writes[1]
	assert.Equal(t, simulation.EventTradeExpired, fin.Event)
	assert.Equal(t, trade.ExpiryTimeout, fin.Reason)
	assert.Equal(t, uuid.Nil, fin.By)
	ultima := h.tratosPara(vecino)
	assert.Equal(t, "EXPIRED", ultima[len(ultima)-1].Status)
}

// RN-TRADE-006 y Q6: durante el preaviso de una ruptura se sigue comerciando; al
// vencer, la oferta cae en el mismo tick que el tratado.
func TestElFinDelPreavisoCierraLasOfertas(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 0)
	h.almacen(2, 0, 100)
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(10)}, economy.Costs{piedra(5)})

	h.cancelar(vecino, 1) // rompe el tratado: 15 minutos de preaviso
	h.clk.AdvanceMs(14 * 60 * 1000)
	h.tick()
	_, sigue := h.state.TradeOffer(primeraOferta)
	require.True(t, sigue, "en el preaviso, la oferta sigue")

	h.clk.AdvanceMs(60 * 1000)
	h.tick()
	assert.Empty(t, h.state.TreatiesBetween(h.playerID, vecino), "el tratado terminó")
	_, sigue = h.state.TradeOffer(primeraOferta)
	assert.False(t, sigue, "y la oferta con él")
	require.NotEmpty(t, h.trade.writes)
	fin := h.trade.writes[len(h.trade.writes)-1]
	assert.Equal(t, simulation.EventTradeExpired, fin.Event)
	assert.Equal(t, trade.ExpiryTreatyEnded, fin.Reason)
}

// SPEC-TRADE §4.1: subir de escalón (TRADE a pacto) sustituye al tratado vigente y
// no toca la oferta, que se puede aceptar después.
func TestSubirDeEscalonConservaLaOferta(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 0)
	h.almacen(2, 0, 100)
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(10)}, economy.Costs{piedra(5)})

	h.proponer(h.playerID, vecino, diplomacy.TypeNonAggression)
	require.Empty(t, h.codigos())
	var oferta diplomacy.Treaty
	for _, tr := range h.state.TreatiesBetween(h.playerID, vecino) {
		if tr.Status == diplomacy.StatusProposed {
			oferta = tr
		}
	}
	require.NotZero(t, oferta.ID)
	h.responder(vecino, oferta.ID, true)
	pacto := h.unicoTratado(h.playerID, vecino)
	assert.Equal(t, diplomacy.TypeNonAggression, pacto.Type, "el pacto sustituye al TRADE")
	assert.True(t, h.state.AtPeace(h.playerID, vecino, h.clk.Now()), "el pacto da paz")

	h.tick() // el barrido de un segundo no cierra la oferta: hay tratado
	_, sigue := h.state.TradeOffer(primeraOferta)
	require.True(t, sigue, "subir de escalón no toca la oferta")
	h.responderTrato(vecino, primeraOferta, true)
	assert.Empty(t, h.codigos())
	_, s := h.recursos(2)
	assert.Equal(t, int64(95), s, "y se acepta con el pacto")
}

// SPEC-TRADE §4.1: la escalera sólo sube; TRADE sobre un pacto, o repetido, se
// rechaza, y TRADE solo no da paz.
func TestLaEscaleraDeTratados(t *testing.T) {
	h := newHarness(t)
	vecino := conVecinos(h, 1)[0]
	h.proponer(h.playerID, vecino, diplomacy.TypeTrade)
	require.Empty(t, h.codigos(), "TRADE se propone")
	oferta := h.unicoTratado(h.playerID, vecino)
	h.responder(vecino, oferta.ID, true)
	assert.False(t, h.state.AtPeace(h.playerID, vecino, h.clk.Now()), "TRADE no da paz")

	h.proponer(vecino, h.playerID, diplomacy.TypeTrade)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "repetir no es subir")

	g := newHarness(t)
	otro := conComercio(g, diplomacy.TypeNonAggression)
	g.proponer(g.playerID, otro, diplomacy.TypeTrade)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, g.codigos(), "bajar no existe")
}

// ADR-032 §2: dos aceptaciones en el mismo tick; la segunda encuentra el par
// ocupado y no reserva nada.
func TestDosAceptacionesEnElMismoTick(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 0)
	h.almacen(2, 0, 200)
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(300)}, economy.Costs{piedra(100)})

	h.commands <- simulation.RespondTrade{PlayerID: vecino, RequestID: uuid.NewString(), OfferID: primeraOferta, Accept: true}
	h.commands <- simulation.RespondTrade{PlayerID: vecino, RequestID: uuid.NewString(), OfferID: primeraOferta, Accept: true}
	h.loop.Step(h.clk.NowMs())
	h.tick()
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	require.Len(t, h.trade.accepts, 1)
	_, s := h.recursos(2)
	assert.Equal(t, int64(100), s, "sólo se pagó una vez")
}

// INV-ECON-002 e INV-TRADE-001: un trato que la cola no consigue escribir
// devuelve lo reservado a las dos ciudades y deja la oferta pendiente.
func TestUnTratoQueNoSeEscribeDevuelveLoReservado(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 0)
	h.almacen(2, 0, 100)
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(300)}, economy.Costs{piedra(100)})

	h.persister.permanent = true
	h.trade.acceptErr = errBaseCaida
	h.responderTrato(vecino, primeraOferta, true)
	assert.Equal(t, []string{protocol.CodeInternalError}, h.codigos())
	w, s := h.recursos(1)
	assert.Equal(t, [2]int64{300, 0}, [2]int64{w, s})
	w, s = h.recursos(2)
	assert.Equal(t, [2]int64{0, 100}, [2]int64{w, s})
	_, sigue := h.state.TradeOffer(primeraOferta)
	assert.True(t, sigue)

	// Y con la base de vuelta, se acepta.
	h.trade.acceptErr = nil
	h.responderTrato(vecino, primeraOferta, true)
	w, s = h.recursos(1)
	assert.Equal(t, [2]int64{0, 100}, [2]int64{w, s})
}

// ADR-032 §2: los depósitos sin volcar de cada recurso tocado viajan con el
// trato; la ciudad que sólo recibe ese recurso también los lleva.
func TestElTratoLlevaLosDepositosSinVolcar(t *testing.T) {
	h := newHarnessPiedra(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 190)
	h.almacen(2, 0, 100)
	h.conectar()
	h.recolectar(1, colina)
	h.hasta(epoch + 22_500)
	require.Equal(t, int64(10), h.state.UnflushedOf(h.cityID, economy.ResourceStone))

	h.ofrecer(h.playerID, vecino, economy.Costs{madera(300)}, economy.Costs{piedra(100)})
	h.responderTrato(vecino, primeraOferta, true)
	require.Len(t, h.trade.accepts, 1)
	var piedraPropia simulation.StockDelta
	for _, d := range h.trade.accepts[0].Stocks {
		if d.CityID == h.cityID && d.Resource == economy.ResourceStone {
			piedraPropia = d
		}
	}
	assert.Equal(t, int64(10+100), piedraPropia.Delta, "los 10 depositados viajan con los 100 recibidos")
	assert.Zero(t, h.state.UnflushedOf(h.cityID, economy.ResourceStone))
	assert.Equal(t, int64(200+100), h.state.StockOf(h.cityID, economy.ResourceStone).Amount)
}

// El snapshot trae las ofertas pendientes de quien lo recibe, y de nadie más.
func TestElSnapshotTraeLasOfertasPropias(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	tercero := uuid.New()
	h.almacen(1, 300, 0)
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(10)}, economy.Costs{piedra(5)})

	for _, c := range []struct {
		quien uuid.UUID
		n     int
	}{{h.playerID, 1}, {vecino, 1}, {tercero, 0}} {
		p := h.sim.BuildSnapshot(c.quien, nil, h.clk.NowMs(), false)
		assert.Len(t, p.TradeOffers, c.n)
	}
}

// INV-ECON-005: al arrancar, una oferta que caducó con el servidor parado se
// cierra en el primer barrido; una vigente sigue.
func TestReinicioConOfertasPendientes(t *testing.T) {
	h := newHarness(t)
	vecinos := conVecinos(h, 2)
	h.state.SetTreaties([]diplomacy.Treaty{
		vigente(1, h.playerID, vecinos[0], diplomacy.TypeTrade),
		vigente(2, h.playerID, vecinos[1], diplomacy.TypeTrade),
	}, nil)
	ahora := time.UnixMilli(h.clk.NowMs()).UTC()
	h.state.SetTradeOffers([]trade.Offer{
		{ID: 7, Proposer: h.playerID, Target: vecinos[0], ProposerCity: 1, TargetCity: 2,
			Give: economy.Costs{madera(10)}, Want: economy.Costs{piedra(5)}, Status: trade.StatusProposed,
			RequestID: "a", CreatedAt: ahora.Add(-25 * time.Hour), ExpiresAt: ahora.Add(-time.Hour)},
		{ID: 8, Proposer: vecinos[1], Target: h.playerID, ProposerCity: 3, TargetCity: 1,
			Give: economy.Costs{piedra(5)}, Want: economy.Costs{madera(10)}, Status: trade.StatusProposed,
			RequestID: "b", CreatedAt: ahora, ExpiresAt: ahora.Add(24 * time.Hour)},
	})
	h.hasta(epoch + 1500)
	_, caducada := h.state.TradeOffer(7)
	_, vigenteSigue := h.state.TradeOffer(8)
	assert.False(t, caducada, "la vencida en la caída se cierra")
	assert.True(t, vigenteSigue)
}

// INV-TRADE-002: si el tratado ya no está cuando llega la aceptación (antes del
// barrido que cerraría la oferta), se rechaza y no se reserva nada.
func TestAceptarSinTratadoSeRechaza(t *testing.T) {
	h := newHarness(t)
	vecino := conComercio(h, diplomacy.TypeTrade)
	h.almacen(1, 300, 0)
	h.almacen(2, 0, 100)
	h.ofrecer(h.playerID, vecino, economy.Costs{madera(300)}, economy.Costs{piedra(100)})

	h.state.SetTreaties(nil, nil)
	h.send(simulation.RespondTrade{PlayerID: vecino, RequestID: uuid.NewString(), OfferID: primeraOferta, Accept: true})
	assert.Equal(t, []string{protocol.CodeTreatyRequired}, h.codigos())
	assert.Empty(t, h.trade.accepts)
	w, _ := h.recursos(1)
	_, s := h.recursos(2)
	assert.Equal(t, [2]int64{300, 100}, [2]int64{w, s}, "no se reservó nada")
}
