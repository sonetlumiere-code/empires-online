package simulation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de las alianzas entre clanes (SPEC-CLAN-ALLIANCES §9, ADR-042). «Los
// Lobos» [LOB] es el clan del jugador del arnés (id 1); «Los Osos» [OSO], el
// de un vecino (id 2). Las ciudades de conVecinos son la 2, la 3…

// dosClanes funda los dos clanes con sus miembros y devuelve sus ids.
func (h *harness) dosClanes(lobos []uuid.UUID, liderOso uuid.UUID, osos ...uuid.UUID) (int64, int64) {
	h.t.Helper()
	lob := h.clanConMiembros(lobos...)
	h.fundarClan(liderOso, "Los Osos", "OSO")
	oso, ok := h.state.ClanOf(liderOso)
	require.True(h.t, ok)
	for _, m := range osos {
		h.invitarAlClan(liderOso, m)
		h.aceptarClan(m, oso)
		require.Empty(h.t, h.codigos())
	}
	return lob, oso
}

// proponerAlianza, responderAlianza y romperAlianza dan la orden y procesan su
// resolución en el tick siguiente.
func (h *harness) proponerAlianza(quien uuid.UUID, clanID int64) {
	h.t.Helper()
	h.send(simulation.ProposeClanAlliance{PlayerID: quien, RequestID: uuid.NewString(), ClanID: clanID})
	h.tick()
}

func (h *harness) responderAlianza(quien uuid.UUID, id int64, aceptar bool) {
	h.t.Helper()
	h.send(simulation.RespondClanAlliance{PlayerID: quien, RequestID: uuid.NewString(), AllianceID: id, Accept: aceptar})
	h.tick()
}

func (h *harness) romperAlianza(quien uuid.UUID, id int64) {
	h.t.Helper()
	h.send(simulation.CancelClanAlliance{PlayerID: quien, RequestID: uuid.NewString(), AllianceID: id})
	h.tick()
}

// alianzaEntre es la alianza abierta entre dos clanes, si la hay.
func alianzaEntre(h *harness, a, b int64) (clan.Alliance, bool) {
	for _, al := range h.state.ClanAlliancesOf(a) {
		if al.Has(b) {
			return al, true
		}
	}
	return clan.Alliance{}, false
}

// aliarse firma una alianza activa entre dos clanes y devuelve su id.
func (h *harness) aliarse(liderA uuid.UUID, a int64, liderB uuid.UUID, b int64) int64 {
	h.t.Helper()
	h.rec.reset()
	h.proponerAlianza(liderA, b)
	require.Empty(h.t, h.codigos())
	al, ok := alianzaEntre(h, a, b)
	require.True(h.t, ok)
	h.responderAlianza(liderB, al.ID, true)
	require.Empty(h.t, h.codigos())
	al, _ = h.state.ClanAlliance(al.ID)
	require.Equal(h.t, diplomacy.StatusActive, al.Status)
	return al.ID
}

// alianzasEnLaVista son las alianzas del último clan.update que recibió p.
func alianzasEnLaVista(h *harness, p uuid.UUID) []protocol.ClanAllianceView {
	h.t.Helper()
	ups := h.clanUpdatesPara(p)
	require.NotEmpty(h.t, ups)
	v := ups[len(ups)-1].Clan
	require.NotNil(h.t, v)
	require.NotNil(h.t, v.Alliances, "la lista va vacía, no null")
	return v.Alliances
}

// Golden «alianza de clanes» (RN-CLANALLY-003, RN-CLANALLY-004, INV-CLAN-007,
// ADR-042 §2 y §3): la oferta avisa al otro líder; la paz no nace hasta que la
// aceptación vuelve por el buzón, y entonces termina el enfrentamiento entre
// dos miembros; la ruptura avisa a cada miembro y la paz sigue hasta el último
// milisegundo del preaviso; en su fin el ataque vuelve a aceptarse y la
// alianza queda rota. Cada orden y cada fin va con las claves de los dos clanes.
func TestGoldenAlianzaDeClanes(t *testing.T) {
	h, fc := conClanes(t, nil)
	vs := conVecinos(h, 3)
	osoLider, osoMiembro, lobMiembro := vs[0], vs[1], vs[2]
	lob, oso := h.dosClanes([]uuid.UUID{lobMiembro}, osoLider, osoMiembro)
	c3, ok := h.state.City(3)
	require.True(t, ok)
	c3.Population = 3
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	v := ponerUnidad(h, enemigoID, osoMiembro, 3, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	h.atacar(g.ID, v.ID)
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(30, func() bool { return v.HP < 40 }), "el primer golpe cae")

	// La oferta: al líder del otro clan, con la sigla del que propone.
	h.rec.reset()
	h.proponerAlianza(h.playerID, oso)
	require.Empty(t, h.codigos())
	oferta, ok := alianzaEntre(h, lob, oso)
	require.True(t, ok)
	assert.Equal(t, diplomacy.StatusProposed, oferta.Status)
	assert.Equal(t, lob, oferta.ProposedByClan)
	ofrecidas := avisosDeTipo(h, eventlog.KindClanAllianceOffered)
	require.Len(t, ofrecidas, 1)
	assert.Equal(t, osoLider, ofrecidas[0].to)
	assert.Equal(t, "LOB", ofrecidas[0].p.ClanTag)
	assert.False(t, h.state.AtPeace(h.playerID, osoMiembro, h.clk.Now()), "una oferta no da paz")

	// La aceptación: la paz nace al volver por el buzón (dos fases).
	h.rec.reset()
	h.send(simulation.RespondClanAlliance{PlayerID: osoLider, RequestID: uuid.NewString(), AllianceID: oferta.ID, Accept: true})
	assert.False(t, h.state.AtPeace(h.playerID, osoMiembro, h.clk.Now()), "antes de la transacción, nada")
	h.tick()
	require.Empty(t, h.codigos())
	require.True(t, h.state.AtPeace(h.playerID, osoMiembro, h.clk.Now()))
	assert.True(t, h.state.AllyPeace(lobMiembro, osoLider, h.clk.Now()), "entre todos los miembros")
	aceptadas := avisosDeTipo(h, eventlog.KindClanAllianceAccepted)
	require.Len(t, aceptadas, 1)
	assert.Equal(t, h.playerID, aceptadas[0].to)
	assert.Equal(t, "OSO", aceptadas[0].p.ClanTag)
	for _, p := range []uuid.UUID{h.playerID, lobMiembro, osoLider, osoMiembro} {
		vista := alianzasEnLaVista(h, p)
		require.Len(t, vista, 1, "cada miembro de los dos clanes ve la alianza")
		assert.Equal(t, string(diplomacy.StatusActive), vista[0].Status)
	}
	vida := v.HP
	h.advance(10 * time.Second)
	assert.Equal(t, vida, v.HP, "ningún golpe más")
	_, sigue := h.state.Engaged(g.ID)
	assert.False(t, sigue, "el enfrentamiento terminó")
	h.rec.reset()
	h.atacar(g.ID, v.ID)
	require.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, "vuestros clanes están aliados", h.ultimoMotivo())

	// La ruptura: el preaviso, avisado a cada miembro de los dos clanes menos
	// a quien rompe, con la sigla del otro clan.
	h.rec.reset()
	h.romperAlianza(osoLider, oferta.ID)
	require.Empty(t, h.codigos())
	al, ok := h.state.ClanAlliance(oferta.ID)
	require.True(t, ok)
	require.True(t, al.InNotice())
	assert.Equal(t, clan.BreakReasonCancel, al.BreakReason)
	preavisos := avisosDeTipo(h, eventlog.KindClanAllianceBreakAnnounced)
	require.Len(t, preavisos, 3)
	tags := map[uuid.UUID]string{}
	for _, a := range preavisos {
		tags[a.to] = a.p.ClanTag
	}
	assert.Equal(t, map[uuid.UUID]string{h.playerID: "OSO", lobMiembro: "OSO", osoMiembro: "LOB"}, tags)
	vista := alianzasEnLaVista(h, lobMiembro)
	require.Len(t, vista, 1)
	require.NotNil(t, vista[0].BreaksAtMs)
	assert.Equal(t, al.BreakEffectiveAtMs, *vista[0].BreaksAtMs)

	h.clk.AdvanceMs(al.BreakEffectiveAtMs - 1 - h.clk.NowMs())
	assert.True(t, h.state.AtPeace(h.playerID, osoMiembro, h.clk.Now()), "hasta el último milisegundo del preaviso")
	h.clk.AdvanceMs(1)
	assert.False(t, h.state.AtPeace(h.playerID, osoMiembro, h.clk.Now()), "vence al cumplirse")
	h.rec.reset()
	h.atacar(g.ID, v.ID)
	assert.Empty(t, h.codigos(), "ya se le puede atacar")
	h.tick()
	_, abierta := h.state.ClanAlliance(oferta.ID)
	assert.False(t, abierta, "la alianza quedó rota")

	// Cada escritura con las claves de los dos clanes; el fin, tolerante.
	claves := func(nombre string) clavesDeTrabajo {
		return clavesDeTrabajo{Name: nombre, Key: simulation.ClanKey(lob), Key2: simulation.ClanKey(oso)}
	}
	assert.Equal(t, []clavesDeTrabajo{claves(simulation.JobClanAlliance), claves(simulation.JobClanAlliance), claves(simulation.JobClanAlliance)},
		h.persister.clavesDe(simulation.JobClanAlliance))
	assert.Equal(t, []clavesDeTrabajo{claves(simulation.JobClanAllianceEnd)}, h.persister.clavesDe(simulation.JobClanAllianceEnd))
	require.Len(t, fc.alliances, 4)
	fin := fc.alliances[3]
	assert.True(t, fin.Tolerant)
	assert.Equal(t, eventlog.EventClanAllianceBroken, fin.Event)
	assert.Equal(t, diplomacy.StatusBroken, fin.Alliance.Status)
	assert.Equal(t, al.BreakEffectiveAtMs, fin.Alliance.ClosedAtMs)
}

// RN-CLANALLY-001, RN-CLANALLY-002, RN-CLANALLY-004, RN-CLANALLY-005 e
// INV-CLAN-006 en el tick: cada fila de §5 de la spec.
func TestLasOrdenesDeUnaAlianza(t *testing.T) {
	preparar := func(t *testing.T, ajustar func(*simulation.Deps)) (*harness, *fakeClans, []uuid.UUID, int64, int64) {
		h, fc := conClanes(t, ajustar)
		vs := conVecinos(h, 4)
		lob, oso := h.dosClanes([]uuid.UUID{vs[2]}, vs[0], vs[1])
		h.rec.reset()
		return h, fc, vs, lob, oso
	}
	rechazo := func(t *testing.T, h *harness, motivo string) {
		t.Helper()
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Equal(t, motivo, h.ultimoMotivo())
		h.rec.reset()
	}

	t.Run("sólo el líder firma, y no con su propio clan", func(t *testing.T) {
		h, _, vs, lob, oso := preparar(t, nil)
		h.proponerAlianza(vs[2], oso)
		rechazo(t, h, "sólo el líder firma las alianzas del clan")
		h.nombrar(h.playerID, vs[2], true)
		h.rec.reset()
		h.proponerAlianza(vs[2], oso)
		rechazo(t, h, "sólo el líder firma las alianzas del clan")
		h.proponerAlianza(vs[3], oso)
		rechazo(t, h, "sólo el líder firma las alianzas del clan")
		h.proponerAlianza(h.playerID, lob)
		rechazo(t, h, "no puedes aliarte con tu propio clan")
		h.proponerAlianza(h.playerID, 99)
		rechazo(t, h, "ese clan no existe")
		h.responderAlianza(h.playerID, 77, true)
		rechazo(t, h, "esa alianza no existe")
	})
	t.Run("una oferta por par, y quién responde y retira", func(t *testing.T) {
		h, _, vs, lob, oso := preparar(t, nil)
		h.proponerAlianza(h.playerID, oso)
		require.Empty(t, h.codigos())
		oferta, _ := alianzaEntre(h, lob, oso)
		h.proponerAlianza(h.playerID, oso)
		rechazo(t, h, "ya hay una oferta pendiente con ese clan")
		h.proponerAlianza(vs[0], lob)
		rechazo(t, h, "ya hay una oferta pendiente con ese clan")
		h.responderAlianza(h.playerID, oferta.ID, true)
		rechazo(t, h, "no puedes responder a esa oferta")
		h.responderAlianza(vs[1], oferta.ID, true)
		rechazo(t, h, "sólo el líder firma las alianzas del clan")
		h.romperAlianza(vs[0], oferta.ID)
		rechazo(t, h, "sólo el clan que propuso puede retirar la oferta")

		// Dos órdenes sobre el mismo par en el mismo tick: la segunda espera.
		h.commands <- simulation.RespondClanAlliance{PlayerID: vs[0], RequestID: uuid.NewString(), AllianceID: oferta.ID, Accept: true}
		h.commands <- simulation.CancelClanAlliance{PlayerID: h.playerID, RequestID: uuid.NewString(), AllianceID: oferta.ID}
		h.loop.Step(h.clk.NowMs())
		rechazo(t, h, "hay otra orden en curso con ese clan")
		h.tick()
		al, _ := h.state.ClanAlliance(oferta.ID)
		require.Equal(t, diplomacy.StatusActive, al.Status)
		h.proponerAlianza(h.playerID, oso)
		rechazo(t, h, "vuestros clanes ya están aliados")

		// Romper, y no dos veces.
		h.romperAlianza(h.playerID, oferta.ID)
		require.Empty(t, h.codigos())
		h.romperAlianza(vs[0], oferta.ID)
		rechazo(t, h, "esa alianza ya está en preaviso")
	})
	t.Run("retirar la oferta propia", func(t *testing.T) {
		h, fc, _, lob, oso := preparar(t, nil)
		h.proponerAlianza(h.playerID, oso)
		oferta, _ := alianzaEntre(h, lob, oso)
		h.romperAlianza(h.playerID, oferta.ID)
		require.Empty(t, h.codigos())
		_, abierta := alianzaEntre(h, lob, oso)
		assert.False(t, abierta)
		require.Len(t, fc.alliances, 2)
		assert.Equal(t, eventlog.EventClanAllianceWithdrawn, fc.alliances[1].Event)
		assert.Equal(t, lob, fc.alliances[1].ByClan)
	})
	t.Run("el rechazo y el plazo para insistir", func(t *testing.T) {
		h, _, vs, lob, oso := preparar(t, nil)
		h.proponerAlianza(h.playerID, oso)
		oferta, _ := alianzaEntre(h, lob, oso)
		h.responderAlianza(vs[0], oferta.ID, false)
		require.Empty(t, h.codigos())
		_, abierta := alianzaEntre(h, lob, oso)
		require.False(t, abierta)
		h.proponerAlianza(h.playerID, oso)
		rechazo(t, h, "ese clan rechazó tu oferta hace poco: espera antes de insistir")
		h.proponerAlianza(vs[0], lob)
		require.Empty(t, h.codigos(), "el otro clan sí puede proponer")
		otra, _ := alianzaEntre(h, lob, oso)
		h.romperAlianza(vs[0], otra.ID)
		require.Empty(t, h.codigos())
		h.advance(10 * time.Minute)
		h.rec.reset()
		h.proponerAlianza(h.playerID, oso)
		assert.Empty(t, h.codigos(), "pasados los 10 minutos")
	})
	t.Run("el tope de alianzas activas", func(t *testing.T) {
		h, _, vs, lob, oso := preparar(t, func(d *simulation.Deps) { d.ClanMaxAllies = 1 })
		h.aliarse(h.playerID, lob, vs[0], oso)
		h.fundarClan(vs[3], "Los Ciervos", "CIE")
		cie, ok := h.state.ClanOf(vs[3])
		require.True(t, ok)
		h.rec.reset()
		h.proponerAlianza(h.playerID, cie)
		rechazo(t, h, "tu clan ya tiene el máximo de alianzas")
		h.proponerAlianza(vs[3], lob)
		rechazo(t, h, "ese clan ya tiene el máximo de alianzas")
	})
	t.Run("una alianza no junta a la Orden y al Caos", func(t *testing.T) {
		h, _, vs, lob, oso := preparar(t, nil)
		enBando(h, vs[2], player.FactionOrder)
		enBando(h, vs[1], player.FactionChaos)
		h.proponerAlianza(h.playerID, oso)
		rechazo(t, h, "una alianza no junta a la Orden y al Caos")

		// También con una entrada pendiente; y si la mezcla aparece con la
		// oferta en pie, no se acepta.
		enBando(h, vs[1], player.FactionNeutral)
		h.proponerAlianza(h.playerID, oso)
		require.Empty(t, h.codigos())
		oferta, _ := alianzaEntre(h, lob, oso)
		c, ok := h.state.CityOf(vs[1])
		require.True(t, ok)
		c.FactionJoin = &city.FactionJoin{Faction: player.FactionChaos, AtMs: h.clk.NowMs() + 60_000}
		h.responderAlianza(vs[0], oferta.ID, true)
		rechazo(t, h, "una alianza no junta a la Orden y al Caos")
	})
}

// §7: lo que la base no admite vuelve con su motivo y no cambia nada; lo que no
// llega a escribirse vuelve como INTERNAL_ERROR. En los dos casos el par queda
// libre para otra orden.
func TestLoQueLaBaseNoAdmiteDeUnaAlianza(t *testing.T) {
	h, fc := conClanes(t, nil)
	vs := conVecinos(h, 1)
	lob, oso := h.dosClanes(nil, vs[0])
	h.rec.reset()
	fc.allianceErr = simulation.ClanRejection{Reason: "sólo el líder firma las alianzas del clan"}
	h.proponerAlianza(h.playerID, oso)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, "sólo el líder firma las alianzas del clan", h.ultimoMotivo())
	_, abierta := alianzaEntre(h, lob, oso)
	assert.False(t, abierta)

	h.rec.reset()
	fc.allianceErr = assert.AnError
	h.persister.permanent = true
	h.proponerAlianza(h.playerID, oso)
	assert.Equal(t, []string{protocol.CodeInternalError}, h.codigos())

	h.rec.reset()
	fc.allianceErr = nil
	h.persister.permanent = false
	h.proponerAlianza(h.playerID, oso)
	assert.Empty(t, h.codigos(), "el par quedó libre")
	_, abierta = alianzaEntre(h, lob, oso)
	assert.True(t, abierta)
}

// RN-CLANALLY-006 e INV-CLAN-007: quien sale de un clan aliado sigue en paz con
// los miembros del otro clan durante la paz residual, y al cumplirse ya no; a
// quien entró en el otro clan después de la salida no le alcanza; y tras la
// ruptura, una salida ya no da paz residual con el que fue aliado.
func TestLaPazResidualConLosAliados(t *testing.T) {
	h, _ := conClanes(t, func(d *simulation.Deps) { d.ClanPeaceGrace = time.Minute })
	vs := conVecinos(h, 4)
	osoLider, osoMiembro, lobMiembro, tardio := vs[0], vs[1], vs[2], vs[3]
	lob, oso := h.dosClanes([]uuid.UUID{lobMiembro}, osoLider, osoMiembro)
	id := h.aliarse(h.playerID, lob, osoLider, oso)

	h.send(simulation.LeaveClan{PlayerID: lobMiembro, RequestID: uuid.NewString()})
	require.Empty(t, h.codigos())
	salida := h.clk.Now()
	assert.True(t, h.state.AllyPeace(lobMiembro, osoMiembro, salida), "paz residual con el aliado")
	assert.False(t, h.state.ClanPeace(lobMiembro, osoMiembro, salida), "no es la del clan")

	h.invitarAlClan(osoLider, tardio)
	h.aceptarClan(tardio, oso)
	assert.False(t, h.state.AllyPeace(lobMiembro, tardio, h.clk.Now()), "entró después de la salida")

	h.clk.AdvanceMs(salida.Add(time.Minute).UnixMilli() - h.clk.NowMs() - 1)
	assert.True(t, h.state.AllyPeace(lobMiembro, osoMiembro, h.clk.Now()), "hasta el último ms")
	h.clk.AdvanceMs(1)
	assert.False(t, h.state.AllyPeace(lobMiembro, osoMiembro, h.clk.Now()), "vence al cumplirse el plazo")

	// Tras la ruptura, salir no deja paz residual con el antiguo aliado.
	h.romperAlianza(h.playerID, id)
	al, _ := h.state.ClanAlliance(id)
	h.clk.AdvanceMs(al.BreakEffectiveAtMs - h.clk.NowMs())
	h.tick()
	h.send(simulation.LeaveClan{PlayerID: osoMiembro, RequestID: uuid.NewString()})
	assert.False(t, h.state.AllyPeace(osoMiembro, h.playerID, h.clk.Now()))
}

// RN-CLANALLY-005 y ADR-042 §4: si la mezcla de bandos aparece con la alianza
// activa, empieza su preaviso, con su motivo, y se avisa a cada miembro.
func TestLaMezclaDeBandosAbreElPreaviso(t *testing.T) {
	t.Run("al vencer la entrada de un miembro en un bando", func(t *testing.T) {
		ff := &fakeFactions{}
		h, _ := conClanes(t, func(d *simulation.Deps) { d.Factions = ff })
		vs := conVecinos(h, 3)
		osoLider, osoMiembro, lobMiembro := vs[0], vs[1], vs[2]
		lob, oso := h.dosClanes([]uuid.UUID{lobMiembro}, osoLider, osoMiembro)
		id := h.aliarse(h.playerID, lob, osoLider, oso)
		enBando(h, osoMiembro, player.FactionOrder)

		h.unirse(lobMiembro, player.FactionChaos)
		h.tick()
		require.Empty(t, h.codigos(), "un clan no veta lo que hace otro")
		c, ok := h.state.CityOf(lobMiembro)
		require.True(t, ok)
		require.NotNil(t, c.FactionJoin)
		h.hastaVencer(c.FactionJoin.AtMs)
		h.rec.reset()
		h.tick()
		require.Equal(t, player.FactionChaos, h.state.FactionOf(lobMiembro))
		al, ok := h.state.ClanAlliance(id)
		require.True(t, ok)
		require.True(t, al.InNotice())
		assert.Equal(t, clan.BreakReasonFactions, al.BreakReason)
		assert.Len(t, avisosDeTipo(h, eventlog.KindClanAllianceBreakAnnounced), 3, "a todos menos a quien la provocó")
		require.Len(t, ff.completions, 1)
		require.Len(t, ff.completions[0].AllianceBreaks, 1)
		w := ff.completions[0].AllianceBreaks[0]
		assert.True(t, w.Tolerant)
		assert.Equal(t, lobMiembro, w.By)
		assert.Equal(t, al.BreakEffectiveAtMs, w.Alliance.BreakEffectiveAtMs)
	})
	t.Run("al entrar en el clan, con el preaviso que abre la base", func(t *testing.T) {
		h, fc := conClanes(t, nil)
		vs := conVecinos(h, 3)
		osoLider, nuevo := vs[0], vs[1]
		lob, oso := h.dosClanes(nil, osoLider)
		id := h.aliarse(h.playerID, lob, osoLider, oso)
		fin := h.clk.NowMs() + 15*60_000
		fc.joinBreaks = []simulation.AllianceBreak{{AllianceID: id, BreakEffectiveAtMs: fin}}
		h.invitarAlClan(h.playerID, nuevo)
		h.rec.reset()
		h.aceptarClan(nuevo, lob)
		require.Empty(t, h.codigos())
		al, _ := h.state.ClanAlliance(id)
		assert.Equal(t, fin, al.BreakEffectiveAtMs)
		assert.Equal(t, clan.BreakReasonFactions, al.BreakReason)
		require.NotEmpty(t, fc.joins)
		assert.Equal(t, 15*time.Minute, fc.joins[len(fc.joins)-1].BreakNotice)
	})
}

// RN-CLANALLY-008: al disolverse un clan, sus alianzas y ofertas expiran en el
// mismo tick y el otro clan lo ve; la paz residual del último en salir cuenta.
// Un cambio de líder no las toca.
func TestLaDisolucionExpiraLasAlianzas(t *testing.T) {
	h, _ := conClanes(t, nil)
	vs := conVecinos(h, 3)
	osoLider, lobMiembro, ciervo := vs[0], vs[1], vs[2]
	lob, oso := h.dosClanes([]uuid.UUID{lobMiembro}, osoLider)
	id := h.aliarse(h.playerID, lob, osoLider, oso)
	h.fundarClan(ciervo, "Los Ciervos", "CIE")
	cie, _ := h.state.ClanOf(ciervo)
	h.proponerAlianza(ciervo, oso)
	require.Empty(t, h.codigos())

	h.ceder(h.playerID, lobMiembro)
	_, abierta := h.state.ClanAlliance(id)
	require.True(t, abierta, "un cambio de líder no la toca")

	h.rec.reset()
	h.send(simulation.LeaveClan{PlayerID: osoLider, RequestID: uuid.NewString()})
	require.Empty(t, h.codigos())
	assert.Empty(t, h.state.ClanAlliancesOf(oso))
	_, abierta = alianzaEntre(h, cie, oso)
	assert.False(t, abierta, "la oferta también")
	assert.Empty(t, alianzasEnLaVista(h, lobMiembro), "el otro clan lo ve en el acto")
	assert.True(t, h.state.AllyPeace(osoLider, h.playerID, h.clk.Now()), "la paz residual del último en salir")
}

// RN-CLANALLY-007: las alianzas activas las ve todo el clan; las ofertas, sólo
// el líder y los oficiales, con quién propuso.
func TestLaVistaDeLasAlianzasSegunElRango(t *testing.T) {
	h, _ := conClanes(t, nil)
	vs := conVecinos(h, 4)
	osoLider, oficial, raso, ciervo := vs[0], vs[1], vs[2], vs[3]
	lob, oso := h.dosClanes([]uuid.UUID{oficial, raso}, osoLider)
	h.nombrar(h.playerID, oficial, true)
	h.aliarse(h.playerID, lob, osoLider, oso)
	h.fundarClan(ciervo, "Los Ciervos", "CIE")
	h.rec.reset()
	h.proponerAlianza(ciervo, lob)
	require.Empty(t, h.codigos())
	h.nombrar(h.playerID, raso, true)
	h.nombrar(h.playerID, raso, false) // un clan.update a todos con la oferta en pie

	estados := func(p uuid.UUID) map[string]bool {
		out := map[string]bool{}
		for _, a := range alianzasEnLaVista(h, p) {
			out[a.Tag+":"+a.Status] = a.ProposedByUs
		}
		return out
	}
	assert.Equal(t, map[string]bool{"OSO:ACTIVE": true, "CIE:PROPOSED": false}, estados(h.playerID))
	assert.Equal(t, map[string]bool{"OSO:ACTIVE": true, "CIE:PROPOSED": false}, estados(oficial))
	assert.Equal(t, map[string]bool{"OSO:ACTIVE": true}, estados(raso), "un raso no ve las ofertas")
}

// INV-CLAN-007 y la hostilidad contra un clan (ADR-041 §2): reclamar un
// territorio de un clan aliado se rechaza con su motivo.
func TestUnaReclamacionContraUnAliadoSeRechaza(t *testing.T) {
	h, _ := conClanes(t, nil)
	conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID})
	vs := conVecinos(h, 1)
	lob, oso := h.dosClanes(nil, vs[0])
	territorioDelClan(h, 2, oso)
	h.aliarse(h.playerID, lob, vs[0], oso)
	ponerUnidad(h, 300, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 40, Y: 10}, 60)
	h.rec.reset()
	h.reclamarComo(h.playerID, 300, false)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, "vuestros clanes están aliados", h.ultimoMotivo())
}

// §9 recuperación y la carga (ADR-042 §1): las alianzas, las ofertas, los
// rechazos y la paz residual vuelven al arrancar; una oferta caducada o un
// preaviso vencido con el servidor parado se aplican en el primer barrido; y la
// carga no instala lo que viola INV-CLAN-006.
func TestLasAlianzasTrasUnReinicio(t *testing.T) {
	h, fc := conClanes(t, func(d *simulation.Deps) { d.ClanMaxAllies = 3 })
	vs := conVecinos(h, 4)
	osoLider, ciervo, salio, orden := vs[0], vs[1], vs[2], vs[3]
	ahora := h.clk.NowMs()
	h.state.SetClans([]clan.Clan{
		{ID: 1, Name: "Los Lobos", Tag: "LOB", LeaderID: h.playerID},
		{ID: 2, Name: "Los Osos", Tag: "OSO", LeaderID: osoLider},
		{ID: 3, Name: "Los Ciervos", Tag: "CIE", LeaderID: ciervo},
		{ID: 4, Name: "La Orden", Tag: "ORD", LeaderID: orden},
	}, []clan.Stay{
		{ClanID: 1, PlayerID: h.playerID, JoinedAtMs: 1},
		{ClanID: 2, PlayerID: osoLider, JoinedAtMs: 1},
		{ClanID: 3, PlayerID: ciervo, JoinedAtMs: 1},
		{ClanID: 4, PlayerID: orden, JoinedAtMs: 1},
		{ClanID: 2, PlayerID: salio, JoinedAtMs: 1, LeftAtMs: ahora - 60_000},
	}, nil, 0)
	enBando(h, orden, player.FactionOrder)
	enBando(h, ciervo, player.FactionChaos)
	activa := clan.Alliance{ID: 10, ClanA: 1, ClanB: 2, ProposedByClan: 1, ProposedBy: h.playerID,
		Status: diplomacy.StatusActive, CreatedAtMs: 1, AcceptedAtMs: 2}
	vencida := clan.Alliance{ID: 11, ClanA: 1, ClanB: 3, ProposedByClan: 1, ProposedBy: h.playerID,
		Status: diplomacy.StatusActive, CreatedAtMs: 1, AcceptedAtMs: 2, BreakEffectiveAtMs: ahora - 1000, BreakReason: clan.BreakReasonCancel}
	caducada := clan.Alliance{ID: 12, ClanA: 2, ClanB: 3, ProposedByClan: 2, ProposedBy: osoLider,
		Status: diplomacy.StatusProposed, CreatedAtMs: ahora - 25*3600_000}
	malas := []clan.Alliance{
		{ID: 13, ClanA: 1, ClanB: 9, ProposedByClan: 1, ProposedBy: h.playerID, Status: diplomacy.StatusProposed, CreatedAtMs: 1},
		{ID: 14, ClanA: 1, ClanB: 2, ProposedByClan: 1, ProposedBy: h.playerID, Status: diplomacy.StatusActive, CreatedAtMs: 1, AcceptedAtMs: 2},
		{ID: 15, ClanA: 3, ClanB: 4, ProposedByClan: 3, ProposedBy: ciervo, Status: diplomacy.StatusActive, CreatedAtMs: 1, AcceptedAtMs: 2},
	}
	rechazo := clan.Alliance{ID: 5, ClanA: 1, ClanB: 4, ProposedByClan: 1, ProposedBy: h.playerID,
		Status: diplomacy.StatusRejected, CreatedAtMs: 1, ClosedAtMs: ahora - 1000}
	bad := h.state.SetClanAlliances(append([]clan.Alliance{activa, vencida, caducada}, malas...), nil, []clan.Alliance{rechazo})
	ids := map[int64]bool{}
	for _, b := range bad {
		ids[b.AllianceID] = true
	}
	assert.Equal(t, map[int64]bool{13: true, 14: true, 15: true}, ids, "un clan muerto, un par repetido y la Orden con el Caos")

	assert.True(t, h.state.AllyPeace(h.playerID, osoLider, h.clk.Now()))
	assert.True(t, h.state.AllyPeace(salio, h.playerID, h.clk.Now()), "la paz residual con el aliado sobrevive")
	h.rec.reset()
	h.proponerAlianza(h.playerID, 4)
	assert.Equal(t, "ese clan rechazó tu oferta hace poco: espera antes de insistir", h.ultimoMotivo())

	h.tick()
	_, sigue := h.state.ClanAlliance(11)
	assert.False(t, sigue, "el preaviso vencido con el servidor parado, en el primer barrido")
	_, sigue = h.state.ClanAlliance(12)
	assert.False(t, sigue, "la oferta caducada también")
	_, sigue = h.state.ClanAlliance(10)
	assert.True(t, sigue)
	eventos := map[string]bool{}
	for _, w := range fc.alliances {
		eventos[w.Event] = true
	}
	assert.True(t, eventos[eventlog.EventClanAllianceBroken])
	assert.True(t, eventos[eventlog.EventClanAllianceExpired])
}
