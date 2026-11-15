package simulation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de las facciones (SPEC-FACTIONS §9): la entrada en dos fases y su
// preaviso, la paz de la Orden, los tratados entre bandos, las rupturas al
// entrar, la tierra afín y la recuperación. Mundo de newHarness: el jugador del
// arnés con su ciudad en (10,10) y los vecinos de conVecinos en (20,40), (28,40)…

// fakeFactions hace de base: anota cada orden y cada vencimiento.
type fakeFactions struct {
	mu          sync.Mutex
	orders      []simulation.FactionJoinOrder
	completions []simulation.FactionJoinCompletion
	orderErr    error
	completeErr error
}

func (f *fakeFactions) RequestJoin(_ context.Context, o simulation.FactionJoinOrder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.orderErr != nil {
		return f.orderErr
	}
	f.orders = append(f.orders, o)
	return nil
}

func (f *fakeFactions) CompleteJoin(_ context.Context, c simulation.FactionJoinCompletion) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.completeErr != nil {
		return f.completeErr
	}
	f.completions = append(f.completions, c)
	return nil
}

func (f *fakeFactions) lastCompletion(t *testing.T) simulation.FactionJoinCompletion {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.completions)
	return f.completions[len(f.completions)-1]
}

// conFacciones es newHarness con el almacén de facciones.
func conFacciones(t *testing.T) (*harness, *fakeFactions) {
	t.Helper()
	ff := &fakeFactions{}
	h := newHarnessConDeps(t, nil, func(d *simulation.Deps) { d.Factions = ff })
	return h, ff
}

// enBando fija la facción efectiva de un jugador.
func enBando(h *harness, p uuid.UUID, faction string) {
	h.t.Helper()
	c, ok := h.state.CityOf(p)
	require.True(h.t, ok)
	c.Faction = faction
}

func (h *harness) unirse(p uuid.UUID, faction string) string {
	h.t.Helper()
	rid := uuid.NewString()
	h.send(simulation.JoinFaction{PlayerID: p, RequestID: rid, Faction: faction})
	return rid
}

// hastaVencer lleva el reloj hasta un tick antes de atMs y ejecuta ese tick.
func (h *harness) hastaVencer(atMs int64) {
	h.t.Helper()
	h.clk.AdvanceMs(atMs - 200 - h.clk.NowMs())
	h.tick() // now = atMs − 100
}

// RN-FAC-001, RN-FAC-002 y ADR-039 §3: la orden se reserva, se confirma al
// volver por el buzón y cuenta exactamente a los 15 minutos de darla.
func TestUnirseVaEnDosFasesYCuentaTrasElPreaviso(t *testing.T) {
	h, ff := conFacciones(t)
	ordenMs := h.clk.NowMs()
	rid := h.unirse(h.playerID, player.FactionOrder)
	require.Empty(t, h.codigos())
	assert.Nil(t, h.ciudad().FactionJoin, "reservada, todavía no la ve nadie")

	h.tick()
	join := h.ciudad().FactionJoin
	require.NotNil(t, join)
	assert.Equal(t, city.FactionJoin{Faction: player.FactionOrder, AtMs: ordenMs + 15*60*1000}, *join, "Q3: 15 minutos")
	require.Len(t, ff.orders, 1)
	assert.Equal(t, join.AtMs, ff.orders[0].At.UnixMilli())
	assert.Equal(t, player.FactionNeutral, h.state.FactionOf(h.playerID), "hasta que cuenta, sigue NEUTRAL")

	// El dueño recibe la ciudad con su requestId, esté donde esté su vista.
	var confirmada bool
	for _, m := range h.rec.byType(protocol.TypeCityUpdate) {
		p := m.Payload.(protocol.CityUpdatePayload)
		if m.Kind == "player" && m.PlayerID == h.playerID && m.RequestID == rid && p.FactionJoin != nil && *p.FactionJoin != nil {
			confirmada = true
			assert.Equal(t, join.AtMs, (*p.FactionJoin).AtMs)
		}
	}
	assert.True(t, confirmada)

	h.hastaVencer(join.AtMs)
	assert.Equal(t, player.FactionNeutral, h.state.FactionOf(h.playerID), "un tick antes, nada")
	h.rec.reset()
	h.tick()
	assert.Equal(t, player.FactionOrder, h.state.FactionOf(h.playerID), "en su instante, cuenta")
	assert.Nil(t, h.ciudad().FactionJoin)
	c := ff.lastCompletion(t)
	assert.Equal(t, player.FactionOrder, c.Faction)
	assert.Equal(t, join.AtMs, c.At.UnixMilli())
	assert.Empty(t, c.Breaks)

	var cuenta bool
	for _, m := range h.rec.byType(protocol.TypeCityUpdate) {
		p := m.Payload.(protocol.CityUpdatePayload)
		if m.Kind == "player" && p.Faction != nil && *p.Faction == player.FactionOrder && p.FactionJoin != nil && *p.FactionJoin == nil {
			cuenta = true
		}
	}
	assert.True(t, cuenta, "la ciudad viaja con la facción nueva y factionJoin: null")
	assert.Equal(t, player.FactionOrder, simulation.CityView(h.ciudad()).Faction)
}

// SPEC-FACTIONS §5: cada rechazo de faction.join, y que ninguno apunta nada.
func TestRechazosDeUnirseAUnBando(t *testing.T) {
	t.Run("NEUTRAL no es un bando al que unirse", func(t *testing.T) {
		h, ff := conFacciones(t)
		h.unirse(h.playerID, player.FactionNeutral)
		assert.Equal(t, []string{protocol.CodeInvalidMessage}, h.codigos())
		assert.Empty(t, ff.orders)
	})
	t.Run("sin almacén", func(t *testing.T) {
		h := newHarness(t)
		h.unirse(h.playerID, player.FactionChaos)
		assert.Equal(t, []string{protocol.CodeInternalError}, h.codigos())
	})
	t.Run("ya es de un bando (INV-PLAYER-013)", func(t *testing.T) {
		h, ff := conFacciones(t)
		enBando(h, h.playerID, player.FactionOrder)
		h.unirse(h.playerID, player.FactionChaos)
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Empty(t, ff.orders)
	})
	t.Run("una entrada en vuelo", func(t *testing.T) {
		h, ff := conFacciones(t)
		h.commands <- simulation.JoinFaction{PlayerID: h.playerID, RequestID: uuid.NewString(), Faction: player.FactionOrder}
		h.commands <- simulation.JoinFaction{PlayerID: h.playerID, RequestID: uuid.NewString(), Faction: player.FactionChaos}
		h.loop.Step(h.clk.NowMs())
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Len(t, ff.orders, 1)
	})
	t.Run("una entrada confirmada", func(t *testing.T) {
		h, ff := conFacciones(t)
		h.unirse(h.playerID, player.FactionOrder)
		h.tick()
		h.unirse(h.playerID, player.FactionOrder)
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Len(t, ff.orders, 1)
	})
	t.Run("la base no la admite", func(t *testing.T) {
		h, ff := conFacciones(t)
		ff.orderErr = simulation.ErrFactionConflict
		h.unirse(h.playerID, player.FactionOrder)
		h.tick()
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Nil(t, h.ciudad().FactionJoin, "el rechazo deshace la reserva")
		ff.orderErr = nil
		h.unirse(h.playerID, player.FactionOrder)
		h.tick()
		assert.NotNil(t, h.ciudad().FactionJoin, "y se puede volver a pedir")
	})
	t.Run("sin ciudad", func(t *testing.T) {
		h, _ := conFacciones(t)
		h.unirse(uuid.New(), player.FactionOrder)
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	})
	t.Run("no es hostil: la protección no lo frena", func(t *testing.T) {
		h, ff := conFacciones(t)
		hasta := h.clk.Now().Add(time.Hour)
		h.ciudad().PresenceState = city.PresenceProtected
		h.ciudad().ProtectionUntil = &hasta
		h.unirse(h.playerID, player.FactionChaos)
		h.tick()
		assert.Empty(t, h.codigos())
		assert.Len(t, ff.orders, 1)
	})
}

// SPEC-FACTIONS §4.1, celda a celda: la paz sólo entre dos de la Orden
// (RN-FAC-003, RN-FAC-004) y pacto o alianza prohibidos sólo entre la Orden y el
// Caos (RN-FAC-005); el comercio, siempre.
func TestLaTablaDeLosBandos(t *testing.T) {
	bandos := []string{player.FactionOrder, player.FactionChaos, player.FactionNeutral}
	for _, a := range bandos {
		for _, b := range bandos {
			t.Run(a+"-"+b, func(t *testing.T) {
				h, _ := conFacciones(t)
				otro := conVecinos(h, 1)[0]
				enBando(h, h.playerID, a)
				enBando(h, otro, b)
				ambosOrden := a == player.FactionOrder && b == player.FactionOrder
				contrarios := (a == player.FactionOrder && b == player.FactionChaos) || (a == player.FactionChaos && b == player.FactionOrder)
				assert.Equal(t, ambosOrden, h.state.AtPeace(h.playerID, otro, h.clk.Now()), "paz")
				assert.Equal(t, ambosOrden, h.state.AtPeace(otro, h.playerID, h.clk.Now()), "simétrica")

				for _, tipo := range []diplomacy.Type{diplomacy.TypeNonAggression, diplomacy.TypeAlliance, diplomacy.TypeTrade} {
					h.rec.reset()
					h.proponer(h.playerID, otro, tipo)
					prohibido := contrarios && tipo != diplomacy.TypeTrade
					if prohibido {
						assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos(), "%s", tipo)
						continue
					}
					assert.Empty(t, h.codigos(), "%s", tipo)
					// Se retira para probar el tipo siguiente.
					h.cancelar(h.playerID, h.unicoTratado(h.playerID, otro).ID)
				}
			})
		}
	}
}

// RN-FAC-005 e INV-DIPLO-003: una oferta de paz hecha cuando uno era NEUTRAL no
// se puede aceptar si después quedan en bandos contrarios; rechazarla, sí.
func TestUnaPazEntreBandosContrariosNoSeAcepta(t *testing.T) {
	h, _ := conFacciones(t)
	otro := conVecinos(h, 1)[0]
	h.proponer(h.playerID, otro, diplomacy.TypeNonAggression)
	oferta := h.unicoTratado(h.playerID, otro)
	enBando(h, h.playerID, player.FactionOrder)
	enBando(h, otro, player.FactionChaos)

	h.responder(otro, oferta.ID, true)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, diplomacy.StatusProposed, h.unicoTratado(h.playerID, otro).Status)

	h.rec.reset()
	h.responder(otro, oferta.ID, false)
	assert.Empty(t, h.codigos())
	assert.Empty(t, h.state.TreatiesBetween(h.playerID, otro))
}

// RN-FAC-006 e INV-DIPLO-003: al contar la entrada, los pactos y alianzas con el
// bando contrario empiezan su preaviso como si los rompiera quien entra; los que
// son con un neutral, el comercio y los que ya estaban en preaviso no cambian.
func TestAlEntrarSeRompenLosPactosConElBandoContrario(t *testing.T) {
	h, ff := conFacciones(t)
	vecinos := conVecinos(h, 4)
	caos, neutral, comercio, yaRoto := vecinos[0], vecinos[1], vecinos[2], vecinos[3]
	enBando(h, caos, player.FactionChaos)
	enBando(h, comercio, player.FactionChaos)
	enBando(h, yaRoto, player.FactionChaos)
	firmado := time.UnixMilli(epoch).UTC()
	fin := firmado.Add(time.Hour)
	par := func(id int64, otro uuid.UUID, tipo diplomacy.Type) diplomacy.Treaty {
		a, b := diplomacy.CanonicalPair(h.playerID, otro)
		return diplomacy.Treaty{ID: id, PlayerA: a, PlayerB: b, Type: tipo, Status: diplomacy.StatusActive,
			AllowsGarrison: tipo.GrantsGarrison(), ProposedAt: firmado, AcceptedAt: &firmado}
	}
	roto := par(4, yaRoto, diplomacy.TypeNonAggression)
	roto.BreakEffectiveAt = &fin
	h.state.SetPeaceTreaties([]diplomacy.Treaty{
		par(1, caos, diplomacy.TypeAlliance),
		par(2, neutral, diplomacy.TypeNonAggression),
		par(3, comercio, diplomacy.TypeTrade),
		roto,
	})

	h.unirse(h.playerID, player.FactionOrder)
	h.tick()
	join := *h.ciudad().FactionJoin
	h.hastaVencer(join.AtMs)
	h.rec.reset()
	h.tick()

	require.Equal(t, player.FactionOrder, h.state.FactionOf(h.playerID))
	alianza := h.unicoTratado(h.playerID, caos)
	require.NotNil(t, alianza.BreakEffectiveAt, "la alianza con el Caos empieza su preaviso")
	assert.Equal(t, h.clk.NowMs()+15*60*1000, alianza.BreakEffectiveAt.UnixMilli())
	assert.True(t, h.state.AtPeace(h.playerID, caos, h.clk.Now()), "durante el preaviso, la paz sigue (RN-DIPLO-004)")
	assert.Nil(t, h.unicoTratado(h.playerID, neutral).BreakEffectiveAt, "con un neutral, nada")
	assert.Nil(t, h.unicoTratado(h.playerID, comercio).BreakEffectiveAt, "el comercio, nada")
	assert.Equal(t, fin.UnixMilli(), h.unicoTratado(h.playerID, yaRoto).BreakEffectiveAt.UnixMilli(), "el que ya se rompía, igual")
	assert.NotEmpty(t, h.actualizacionesPara(caos), "el otro firmante lo recibe")

	c := ff.lastCompletion(t)
	require.Len(t, c.Breaks, 1, "la ruptura va en la misma escritura que la facción (ADR-039 §4)")
	assert.Equal(t, int64(1), c.Breaks[0].Treaty.ID)
	assert.Equal(t, "TreatyBreakAnnounced", c.Breaks[0].Event)
	assert.Equal(t, h.playerID, c.Breaks[0].By, "la rompe quien entra (ADR-028 §3)")
	assert.Equal(t, diplomacy.StatusActive, c.Breaks[0].From)

	// Al vencer el preaviso, la alianza termina como cualquier ruptura.
	h.advance(15*time.Minute + time.Second)
	assert.Empty(t, h.state.TreatiesBetween(h.playerID, caos))
	assert.False(t, h.state.AtPeace(h.playerID, caos, h.clk.Now()))
}

// ADR-039 §4: si un tratado que la entrada va a romper tiene una orden en
// vuelo, la entrada espera al tick siguiente y no rompe lo que la orden ya
// rompió.
func TestLaEntradaEsperaAUnaOrdenDeTratadoEnVuelo(t *testing.T) {
	h, ff := conFacciones(t)
	caos := conVecinos(h, 1)[0]
	enBando(h, caos, player.FactionChaos)
	firmado := time.UnixMilli(epoch).UTC()
	a, b := diplomacy.CanonicalPair(h.playerID, caos)
	h.state.SetPeaceTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: a, PlayerB: b, Type: diplomacy.TypeNonAggression,
		Status: diplomacy.StatusActive, ProposedAt: firmado, AcceptedAt: &firmado}})

	h.unirse(h.playerID, player.FactionOrder)
	h.tick()
	join := *h.ciudad().FactionJoin
	h.hastaVencer(join.AtMs)
	// En el tick en que vence, el otro rompe el pacto: su orden queda en vuelo.
	h.clk.AdvanceMs(100)
	h.commands <- simulation.CancelTreaty{PlayerID: caos, RequestID: uuid.NewString(), TreatyID: 1}
	h.loop.Step(h.clk.NowMs())
	assert.Equal(t, player.FactionNeutral, h.state.FactionOf(h.playerID), "espera a la orden en vuelo")

	h.tick()
	assert.Equal(t, player.FactionOrder, h.state.FactionOf(h.playerID), "al tick siguiente, cuenta")
	assert.Empty(t, ff.lastCompletion(t).Breaks, "el pacto ya lo rompió el otro")
	assert.Equal(t, []string{"TreatyBreakAnnounced"}, h.treaties.events(), "una sola ruptura, la del otro")
}

// RN-FAC-003 e INV-COMBAT-006: un enfrentamiento en curso termina en el tick en
// que el segundo jugador entra en la Orden, sin un golpe más; y después, la
// orden de atacar se rechaza con su motivo.
func TestDosDeLaOrdenDejanDePelearAlEntrarElSegundo(t *testing.T) {
	h, _ := conFacciones(t)
	enemigo, _ := conEnemigo(h)
	enBando(h, h.playerID, player.FactionOrder)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	v := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	vecina, _ := h.state.City(2)

	h.atacar(g.ID, v.ID)
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(30, func() bool { return v.HP < 40 }), "el primer golpe cae")
	// La entrada del enemigo en la Orden, confirmada y a punto de contar.
	vecina.FactionJoin = &city.FactionJoin{Faction: player.FactionOrder, AtMs: h.clk.NowMs() + 100}
	h.tick()
	require.Equal(t, player.FactionOrder, h.state.FactionOf(enemigo))
	vida := v.HP
	h.advance(10 * time.Second)
	assert.Equal(t, vida, v.HP, "ningún golpe más")
	_, sigue := h.state.Engaged(g.ID)
	assert.False(t, sigue, "el enfrentamiento terminó")

	h.rec.reset()
	h.atacar(g.ID, v.ID)
	require.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	msg := h.rec.byType(protocol.TypeSystemError)[0].Payload.(protocol.SystemErrorPayload).Message
	assert.Equal(t, "los dos sois de la Orden", msg)
}

// RN-FAC-003: el saqueo, el asedio y la conquista contra otro de la Orden se
// rechazan con el motivo del bando, no con el de un tratado.
func TestLaOrdenNoSaqueaNiConquistaALosSuyos(t *testing.T) {
	ultimoMotivo := func(h *harness) string {
		errs := h.rec.byType(protocol.TypeSystemError)
		require.NotEmpty(h.t, errs)
		return errs[len(errs)-1].Payload.(protocol.SystemErrorPayload).Message
	}
	h, _ := conFacciones(t)
	enemigo, vecina := conEnemigo(h)
	enBando(h, h.playerID, player.FactionOrder)
	enBando(h, enemigo, player.FactionOrder)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 18, Y: 40}, 60)

	t.Run("saqueo", func(t *testing.T) {
		h.rec.reset()
		h.send(simulation.PillageCity{PlayerID: h.playerID, RequestID: uuid.NewString(), UnitID: g.ID, CityID: vecina.ID})
		require.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Equal(t, msgOrden, ultimoMotivo(h))
	})
	t.Run("asedio", func(t *testing.T) {
		h.rec.reset()
		conCasa(h, vecina, 500, world.Tile{X: 20, Y: 39}, true)
		h.derribar(g.ID, 500)
		require.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Equal(t, msgOrden, ultimoMotivo(h))
	})
	t.Run("conquista", func(t *testing.T) {
		h.rec.reset()
		// Un territorio del enemigo sin su capital, con el guerrero dentro.
		set, solapes, err := territory.BuildSet([]territory.Territory{
			{ID: 3, Name: "Llano", MinX: 0, MinY: 30, MaxX: 18, MaxY: 50},
		}, 64, 64, 32)
		require.NoError(t, err)
		require.Empty(t, solapes)
		id := enemigo.String()
		h.state.SetTerritories(set, []territory.Control{{TerritoryID: 3, OwnerType: territory.OwnerPlayer, OwnerID: &id, Version: 1}})
		h.reclamar(g.ID)
		require.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Equal(t, msgOrden, ultimoMotivo(h))
	})
}

// msgOrden es el motivo de los rechazos por la paz de la Orden.
const msgOrden = "los dos sois de la Orden"

// conTierra pone el territorio 2 (20..50, 20..40) en manos de un jugador.
func conTierra(t *testing.T, h *harness, dueno uuid.UUID) {
	t.Helper()
	set, solapes, err := territory.BuildSet([]territory.Territory{
		{ID: 1, Name: "Rincón", MinX: 0, MinY: 0, MaxX: 15, MaxY: 15},
		{ID: 2, Name: "Encrucijada", MinX: 20, MinY: 20, MaxX: 50, MaxY: 40},
	}, 64, 64, 32)
	require.NoError(t, err)
	require.Empty(t, solapes)
	id := dueno.String()
	h.state.SetTerritories(set, []territory.Control{
		{TerritoryID: 1, OwnerType: territory.OwnerNone},
		{TerritoryID: 2, OwnerType: territory.OwnerPlayer, OwnerID: &id, Version: 1},
	})
}

// primerGolpe ataca al aldeano y devuelve el daño del primer golpe.
func primerGolpe(t *testing.T, h *harness, v *unit.Unit) int32 {
	t.Helper()
	antes := v.HP
	h.atacar(guerreroID, v.ID)
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(30, func() bool { return v.HP < antes }))
	return antes - v.HP
}

// RN-FAC-007 y RN-FAC-008, golden «tierra afín»: el golpe de 8 de un guerrero
// quita 6 (8 × 800 / 1000, redondeado) a una unidad en tierra de su dueño, o en
// la de alguien de su bando si no es NEUTRAL; en cualquier otra tierra, 8.
func TestElDanoEnTierraAfin(t *testing.T) {
	casos := []struct {
		nombre         string
		victima, dueno string // bando de la víctima y del dueño de la tierra
		tierraPropia   bool
		dano           int32
	}{
		{"tierra propia de un neutral", player.FactionNeutral, player.FactionNeutral, true, 6},
		{"tierra de otro de la Orden", player.FactionOrder, player.FactionOrder, false, 6},
		{"tierra de otro del Caos", player.FactionChaos, player.FactionChaos, false, 6},
		{"tierra de otro neutral", player.FactionNeutral, player.FactionNeutral, false, 8},
		{"tierra del bando contrario", player.FactionOrder, player.FactionChaos, false, 8},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			h, _ := conFacciones(t)
			vecinos := conVecinos(h, 2)
			victima, tercero := vecinos[0], vecinos[1]
			enBando(h, victima, c.victima)
			dueno := tercero
			if c.tierraPropia {
				dueno = victima
			} else {
				enBando(h, tercero, c.dueno)
			}
			conTierra(t, h, dueno)
			ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 24, Y: 25}, 60)
			v := ponerUnidad(h, enemigoID, victima, 2, unit.TypeVillager, world.Tile{X: 25, Y: 25}, 40)
			assert.Equal(t, c.dano, primerGolpe(t, h, v))
		})
	}

	t.Run("fuera de todo territorio", func(t *testing.T) {
		h, _ := conFacciones(t)
		victima := conVecinos(h, 1)[0]
		conTierra(t, h, victima)
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 54, Y: 50}, 60)
		v := ponerUnidad(h, enemigoID, victima, 2, unit.TypeVillager, world.Tile{X: 55, Y: 50}, 40)
		assert.Equal(t, int32(8), primerGolpe(t, h, v))
	})
	t.Run("el valor configurado y su mínimo de 1", func(t *testing.T) {
		ff := &fakeFactions{}
		h := newHarnessConDeps(t, nil, func(d *simulation.Deps) { d.Factions = ff; d.FactionAffineDamagePerMille = 1 })
		victima := conVecinos(h, 1)[0]
		conTierra(t, h, victima)
		ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 24, Y: 25}, 60)
		v := ponerUnidad(h, enemigoID, victima, 2, unit.TypeVillager, world.Tile{X: 25, Y: 25}, 40)
		assert.Equal(t, int32(1), primerGolpe(t, h, v))
	})
}

// SPEC-FACTIONS §6: la vista de territorio lleva el bando del dueño, y al contar
// la entrada sus territorios se vuelven a difundir con el bando nuevo.
func TestLosTerritoriosDelQueEntraSeDifundenConSuBando(t *testing.T) {
	h, _ := conFacciones(t)
	conTierra(t, h, h.playerID)
	h.unirse(h.playerID, player.FactionChaos)
	h.tick()
	join := *h.ciudad().FactionJoin
	h.hastaVencer(join.AtMs)
	h.rec.reset()
	h.tick()
	var vista *protocol.TerritoryView
	for _, m := range h.rec.byType(protocol.TypeTerritoryUpdate) {
		v := m.Payload.(protocol.TerritoryUpdatePayload).Territory
		if v.ID == 2 {
			vista = &v
		}
	}
	require.NotNil(t, vista, "su territorio se vuelve a difundir")
	require.NotNil(t, vista.OwnerFaction)
	assert.Equal(t, player.FactionChaos, *vista.OwnerFaction)
	for _, m := range h.rec.byType(protocol.TypeTerritoryUpdate) {
		assert.NotEqual(t, int64(1), m.Payload.(protocol.TerritoryUpdatePayload).Territory.ID, "el libre, no")
	}
}

// Recuperación (ADR-039 §4): una entrada que venció con el servidor parado se
// aplica en el primer tick, con sus rupturas.
func TestUnaEntradaVencidaConElServidorParadoCuentaEnElPrimerTick(t *testing.T) {
	h, ff := conFacciones(t)
	caos := conVecinos(h, 1)[0]
	enBando(h, caos, player.FactionChaos)
	firmado := time.UnixMilli(epoch).UTC()
	a, b := diplomacy.CanonicalPair(h.playerID, caos)
	h.state.SetPeaceTreaties([]diplomacy.Treaty{{ID: 1, PlayerA: a, PlayerB: b, Type: diplomacy.TypeAlliance,
		Status: diplomacy.StatusActive, AllowsGarrison: true, ProposedAt: firmado, AcceptedAt: &firmado}})
	h.ciudad().FactionJoin = &city.FactionJoin{Faction: player.FactionOrder, AtMs: h.clk.NowMs() - time.Hour.Milliseconds()}

	h.tick()
	assert.Equal(t, player.FactionOrder, h.state.FactionOf(h.playerID))
	assert.NotNil(t, h.unicoTratado(h.playerID, caos).BreakEffectiveAt)
	assert.Len(t, ff.lastCompletion(t).Breaks, 1)
}

// ADR-022: un vencimiento que no se escribe vuelve a la cola en el volcado
// siguiente, y la RAM no se deshace.
func TestUnVencimientoQueNoSeEscribeSeReintenta(t *testing.T) {
	h, ff := conFacciones(t)
	h.persister.permanent = true
	ff.completeErr = errors.New("la base no responde")
	h.ciudad().FactionJoin = &city.FactionJoin{Faction: player.FactionChaos, AtMs: h.clk.NowMs()}
	h.tick()
	require.Equal(t, player.FactionChaos, h.state.FactionOf(h.playerID), "optimista: la RAM ya cambió")
	assert.Empty(t, ff.completions)

	ff.mu.Lock()
	ff.completeErr = nil
	ff.mu.Unlock()
	h.advance(6 * time.Second) // un volcado
	c := ff.lastCompletion(t)
	assert.Equal(t, player.FactionChaos, c.Faction)
	assert.Equal(t, player.FactionChaos, h.state.FactionOf(h.playerID))
}
