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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de las guerras entre clanes (SPEC-CLAN-WARS §9, ADR-045). «Los Lobos»
// [LOB] es el clan del jugador del arnés; «Los Osos» [OSO], el de un vecino.
// Con conVecinos(h, 3) y dosClanes: vs[0] lidera OSO (ciudad 2), vs[1] es de OSO
// (ciudad 3) y vs[2] es de LOB (ciudad 4).

// conGuerra prepara el arnés con una preparación de 10 s y una guerra de una
// hora (ajustables), los dos clanes y sus miembros.
func conGuerra(t *testing.T, ajustar func(*simulation.Deps)) (*harness, *fakeClans, []uuid.UUID, int64, int64) {
	t.Helper()
	h, fc := conClanes(t, func(d *simulation.Deps) {
		d.TreatyBreakNotice = 10 * time.Second
		d.ClanWarDuration = time.Hour
		if ajustar != nil {
			ajustar(d)
		}
	})
	vs := conVecinos(h, 3)
	lob, oso := h.dosClanes([]uuid.UUID{vs[2]}, vs[0], vs[1])
	h.rec.reset()
	return h, fc, vs, lob, oso
}

func (h *harness) declararGuerra(quien uuid.UUID, clanID int64) {
	h.t.Helper()
	h.send(simulation.DeclareClanWar{PlayerID: quien, RequestID: uuid.NewString(), ClanID: clanID})
	h.tick()
}

func (h *harness) ofrecerPaz(quien uuid.UUID, warID int64, offer bool) {
	h.t.Helper()
	h.send(simulation.ClanWarPeace{PlayerID: quien, RequestID: uuid.NewString(), WarID: warID, Offer: offer})
	h.tick()
}

func (h *harness) rendirse(quien uuid.UUID, warID int64) {
	h.t.Helper()
	h.send(simulation.SurrenderClanWar{PlayerID: quien, RequestID: uuid.NewString(), WarID: warID})
	h.tick()
}

// guerraEntre es la guerra abierta entre dos clanes.
func guerraEntre(h *harness, a, b int64) (clan.War, bool) {
	for _, g := range h.state.ClanWarsOf(a) {
		if g.Has(b) {
			return g, true
		}
	}
	return clan.War{}, false
}

// enGuerra declara la guerra de LOB a OSO y la deja activa.
func (h *harness) enGuerra(lob, oso int64) clan.War {
	h.t.Helper()
	h.declararGuerra(h.playerID, oso)
	require.Empty(h.t, h.codigos())
	g, ok := guerraEntre(h, lob, oso)
	require.True(h.t, ok)
	h.clk.AdvanceMs(g.StartsAtMs - h.clk.NowMs())
	h.tick()
	g, _ = h.state.ClanWar(g.ID)
	require.Equal(h.t, clan.WarActive, g.Status)
	h.rec.reset()
	return g
}

// guerrasEnLaVista son las guerras del último clan.update que recibió p.
func guerrasEnLaVista(h *harness, p uuid.UUID) []protocol.ClanWarView {
	h.t.Helper()
	ups := h.clanUpdatesPara(p)
	require.NotEmpty(h.t, ups)
	v := ups[len(ups)-1].Clan
	require.NotNil(h.t, v)
	require.NotNil(h.t, v.Wars, "la lista va vacía, no null")
	return v.Wars
}

// otroVecino añade un jugador con su ciudad, con un id de ciudad libre.
func otroVecino(h *harness, cityID int64) uuid.UUID {
	h.t.Helper()
	p := uuid.New()
	h.state.AddCity(&city.City{
		ID: cityID, OwnerPlayerID: p, Name: "Lejana", CenterX: int32(20 + 8*cityID), CenterY: 40, Era: city.EraStone,
		PopulationLimit: 20, PresenceState: city.PresenceOnline,
	})
	return p
}

func destinatarios(as []aviso) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	for _, a := range as {
		out[a.to] = a.p.ClanTag
	}
	return out
}

// Golden «guerra de clanes» (RN-CLANWAR-003 a RN-CLANWAR-008, INV-DIPLO-004,
// ADR-045 §2 y §3): la declaración no compromete hasta que vuelve por el buzón;
// entonces el pacto entre dos miembros empieza su preaviso, la oferta de pacto
// caduca y el comercio sigue, y cada miembro menos quien declaró recibe el
// aviso; la guerra empieza al último milisegundo de la preparación, cuando el
// pacto termina; una muerte suma 1; la oferta de paz avisa al otro líder y su
// aceptación termina en empate. Cada orden con las claves de los dos clanes.
func TestGoldenGuerraDeClanes(t *testing.T) {
	h, fc, vs, lob, oso := conGuerra(t, nil)
	osoLider, osoMiembro, lobMiembro := vs[0], vs[1], vs[2]
	quien := osoLider
	pa, pb := diplomacy.CanonicalPair(h.playerID, osoLider)
	h.state.SetTreaties([]diplomacy.Treaty{
		vigente(101, lobMiembro, osoMiembro, diplomacy.TypeNonAggression),
		vigente(103, lobMiembro, osoLider, diplomacy.TypeTrade),
		{ID: 102, PlayerA: pa, PlayerB: pb,
			Type: diplomacy.TypeNonAggression, Status: diplomacy.StatusProposed, ProposedAt: h.clk.Now(), ProposedBy: &quien},
	}, nil)

	// La declaración: en vuelo ya impide firmar, pero no rompe nada todavía.
	h.send(simulation.DeclareClanWar{PlayerID: h.playerID, RequestID: uuid.NewString(), ClanID: oso})
	assert.True(t, h.state.AtWar(lobMiembro, osoMiembro), "la declaración en vuelo ya impide firmar")
	_, abierta := guerraEntre(h, lob, oso)
	assert.False(t, abierta, "antes de la transacción, nada")
	pacto, _ := h.state.Treaty(101)
	assert.Nil(t, pacto.BreakEffectiveAt)
	declarada := h.clk.NowMs()
	h.tick()
	require.Empty(t, h.codigos())
	g, ok := guerraEntre(h, lob, oso)
	require.True(t, ok)
	assert.Equal(t, clan.WarDeclared, g.Status)
	assert.Equal(t, lob, g.Attacker)
	assert.Equal(t, declarada+10_000, g.StartsAtMs, "la preparación es el preaviso de E6")

	// El compromiso, en el tick en que vuelve la declaración.
	pacto, ok = h.state.Treaty(101)
	require.True(t, ok)
	require.NotNil(t, pacto.BreakEffectiveAt)
	assert.Equal(t, h.clk.NowMs()+10_000, pacto.BreakEffectiveAt.UnixMilli())
	_, oferta := h.state.Treaty(102)
	assert.False(t, oferta, "la oferta de pacto caduca")
	comercio, _ := h.state.Treaty(103)
	assert.Nil(t, comercio.BreakEffectiveAt, "el comercio no da paz y sigue")
	var rotas []simulation.TreatyWrite
	for _, w := range h.treaties.writes {
		if w.Reason == eventlog.BreakReasonWar {
			rotas = append(rotas, w)
		}
	}
	require.Len(t, rotas, 2)
	assert.Equal(t, "TreatyBreakAnnounced", rotas[0].Event)
	assert.Equal(t, lobMiembro, rotas[0].By, "como si lo rompiera el miembro del clan que declaró")
	assert.Equal(t, "TreatyExpired", rotas[1].Event)

	// Los avisos: a cada miembro de los dos clanes menos a quien declaró.
	declaraciones := avisosDeTipo(h, eventlog.KindClanWarDeclared)
	assert.Equal(t, map[uuid.UUID]string{lobMiembro: "OSO", osoLider: "LOB", osoMiembro: "LOB"}, destinatarios(declaraciones))
	require.NotNil(t, declaraciones[0].p.StartsAtMs)
	assert.Equal(t, g.StartsAtMs, *declaraciones[0].p.StartsAtMs)
	vista := guerrasEnLaVista(h, osoMiembro)
	require.Len(t, vista, 1)
	assert.Equal(t, string(clan.WarDeclared), vista[0].Status)
	assert.False(t, vista[0].DeclaredByUs)
	assert.Nil(t, vista[0].EndsAtMs)
	assert.Equal(t, "LOB", vista[0].Tag)

	// Sin paz por separado: ni pacto ni alianza entre los dos lados.
	h.rec.reset()
	h.proponer(h.playerID, osoMiembro, diplomacy.TypeNonAggression)
	require.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, "vuestros clanes están en guerra", h.ultimoMotivo())
	h.rec.reset()
	h.proponerAlianza(h.playerID, oso)
	require.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, "vuestros clanes están en guerra", h.ultimoMotivo())

	// El comienzo, al cumplirse la preparación (en el barrido de cada segundo),
	// con el plazo contado desde el fin de la preparación; el pacto termina a la
	// vez que ella.
	h.clk.AdvanceMs(g.StartsAtMs - 1 - h.clk.NowMs())
	h.loop.Step(h.clk.NowMs())
	g, _ = h.state.ClanWar(g.ID)
	assert.Equal(t, clan.WarDeclared, g.Status, "hasta el último milisegundo de la preparación")
	require.True(t, h.tickHasta(20, func() bool { w, _ := h.state.ClanWar(g.ID); return w.Status == clan.WarActive }))
	g, _ = h.state.ClanWar(g.ID)
	assert.Equal(t, g.StartsAtMs+3_600_000, g.EndsAtMs)
	require.True(t, h.tickHasta(20, func() bool { _, sigue := h.state.Treaty(101); return !sigue }), "el pacto termina con la preparación")
	h.rec.reset()

	// Una muerte entre los dos lados suma 1 al marcador del autor.
	c3, _ := h.state.City(3)
	c3.Population = 3
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	v := ponerUnidad(h, enemigoID, osoMiembro, 3, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)
	h.atacar(guerreroID, v.ID)
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(100, func() bool { _, vivo := h.state.Unit(v.ID); return !vivo }))
	g, _ = h.state.ClanWar(g.ID)
	assert.Equal(t, int64(1), g.ScoreOf(lob))
	assert.Zero(t, g.ScoreOf(oso))
	require.NotEmpty(t, h.combat.deaths)
	assert.Equal(t, &clan.WarPoints{WarID: g.ID, ClanID: lob, Points: 1}, h.combat.deaths[len(h.combat.deaths)-1].War,
		"los puntos viajan en el evento de la muerte")
	h.advance(2 * time.Second) // el barrido siguiente difunde el marcador
	vista = guerrasEnLaVista(h, lobMiembro)
	require.Len(t, vista, 1)
	assert.Equal(t, int64(1), vista[0].OurScore, "el marcador se difunde")

	// La oferta de paz: al líder del otro clan; las marcas, sólo a los mandos.
	h.rec.reset()
	h.ofrecerPaz(osoLider, g.ID, true)
	require.Empty(t, h.codigos())
	g, _ = h.state.ClanWar(g.ID)
	assert.True(t, g.PeaceByDefender)
	ofertas := avisosDeTipo(h, eventlog.KindClanWarPeaceOffered)
	assert.Equal(t, map[uuid.UUID]string{h.playerID: "OSO"}, destinatarios(ofertas))
	assert.True(t, guerrasEnLaVista(h, h.playerID)[0].PeaceOfferedByThem)
	assert.False(t, guerrasEnLaVista(h, lobMiembro)[0].PeaceOfferedByThem, "un raso no ve las ofertas")

	// La aceptación: empate, el historial de los dos y el aviso a todos.
	h.rec.reset()
	h.ofrecerPaz(h.playerID, g.ID, true)
	require.Empty(t, h.codigos())
	_, abierta = h.state.ClanWar(g.ID)
	assert.False(t, abierta)
	assert.Equal(t, clan.WarRecord{Drawn: 1}, h.state.ClanWarRecord(lob))
	assert.Equal(t, clan.WarRecord{Drawn: 1}, h.state.ClanWarRecord(oso))
	finales := avisosDeTipo(h, eventlog.KindClanWarEnded)
	assert.Equal(t, map[uuid.UUID]string{lobMiembro: "OSO", osoLider: "LOB", osoMiembro: "LOB"}, destinatarios(finales))
	for _, a := range finales {
		assert.Equal(t, clan.WarDrawn, a.p.WarResult)
		assert.Equal(t, clan.WarEndPeace, a.p.WarReason)
		if a.to == osoMiembro {
			require.NotNil(t, a.p.OurScore)
			assert.Equal(t, int64(0), *a.p.OurScore)
			assert.Equal(t, int64(1), *a.p.TheirScore)
		}
	}
	assert.Empty(t, guerrasEnLaVista(h, lobMiembro))

	// Cada orden con las claves de los dos clanes; el comienzo, tolerante.
	claves := func(nombre string) clavesDeTrabajo {
		return clavesDeTrabajo{Name: nombre, Key: simulation.ClanKey(lob), Key2: simulation.ClanKey(oso)}
	}
	assert.Equal(t, []clavesDeTrabajo{claves(simulation.JobClanWar), claves(simulation.JobClanWar), claves(simulation.JobClanWar)},
		h.persister.clavesDe(simulation.JobClanWar))
	assert.Equal(t, []clavesDeTrabajo{claves(simulation.JobClanWarEnd)}, h.persister.clavesDe(simulation.JobClanWarEnd))
	require.Len(t, fc.wars, 4)
	assert.True(t, fc.wars[0].Insert)
	assert.Equal(t, eventlog.EventClanWarStarted, fc.wars[1].Event)
	assert.True(t, fc.wars[1].Tolerant)
	assert.Equal(t, eventlog.EventClanWarPeaceOffered, fc.wars[2].Event)
	fin := fc.wars[3]
	assert.Equal(t, eventlog.EventClanWarEnded, fin.Event)
	assert.Equal(t, clan.WarEndPeace, fin.War.EndReason)
	assert.Equal(t, int64(1), fin.War.AttackerScore, "el marcador congelado")
}

// RN-CLANWAR-001 a RN-CLANWAR-003 y RN-CLANWAR-006 e INV-CLAN-008 en el tick:
// cada fila de §5 de la spec.
func TestLasOrdenesDeUnaGuerra(t *testing.T) {
	rechazo := func(t *testing.T, h *harness, motivo string) {
		t.Helper()
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Equal(t, motivo, h.ultimoMotivo())
		h.rec.reset()
	}

	t.Run("sólo el líder, y no a su propio clan", func(t *testing.T) {
		h, _, vs, lob, oso := conGuerra(t, nil)
		h.declararGuerra(vs[2], oso)
		rechazo(t, h, "sólo el líder decide las guerras del clan")
		h.nombrar(h.playerID, vs[2], true)
		h.rec.reset()
		h.declararGuerra(vs[2], oso)
		rechazo(t, h, "sólo el líder decide las guerras del clan")
		h.declararGuerra(h.playerID, lob)
		rechazo(t, h, "no puedes declarar la guerra a tu propio clan")
		h.declararGuerra(h.playerID, 99)
		rechazo(t, h, "ese clan no existe")
		h.ofrecerPaz(h.playerID, 77, true)
		rechazo(t, h, "esa guerra no existe")
		h.rendirse(h.playerID, 77)
		rechazo(t, h, "esa guerra no existe")
	})
	t.Run("una guerra por par, en cualquier sentido, y las órdenes en vuelo", func(t *testing.T) {
		h, _, vs, lob, oso := conGuerra(t, nil)
		h.commands <- simulation.DeclareClanWar{PlayerID: h.playerID, RequestID: uuid.NewString(), ClanID: oso}
		h.commands <- simulation.DeclareClanWar{PlayerID: vs[0], RequestID: uuid.NewString(), ClanID: lob}
		h.loop.Step(h.clk.NowMs())
		rechazo(t, h, "hay otra orden de guerra en curso con ese clan")
		h.tick()
		require.Empty(t, h.codigos())
		h.declararGuerra(vs[0], lob)
		rechazo(t, h, "ya hay una guerra abierta con ese clan")
		h.ofrecerPaz(vs[1], 1, true)
		rechazo(t, h, "sólo el líder decide las guerras del clan")
		g, _ := guerraEntre(h, lob, oso)
		h.ofrecerPaz(h.playerID, g.ID, false)
		rechazo(t, h, "no hay ninguna oferta de paz que retirar")
		h.ofrecerPaz(h.playerID, g.ID, true)
		require.Empty(t, h.codigos())
		h.ofrecerPaz(h.playerID, g.ID, true)
		rechazo(t, h, "tu clan ya ofreció la paz")
		// Rechazar la oferta ajena la retira.
		h.ofrecerPaz(vs[0], g.ID, false)
		require.Empty(t, h.codigos())
		g, _ = h.state.ClanWar(g.ID)
		assert.False(t, g.PeaceByAttacker)
	})
	t.Run("no se declara a un aliado ni con una alianza en vuelo", func(t *testing.T) {
		h, _, vs, lob, oso := conGuerra(t, nil)
		id := h.aliarse(h.playerID, lob, vs[0], oso)
		h.rec.reset()
		h.declararGuerra(h.playerID, oso)
		rechazo(t, h, "vuestros clanes están aliados: rompe antes la alianza")
		h.romperAlianza(vs[0], id)
		h.rec.reset()
		h.declararGuerra(h.playerID, oso)
		rechazo(t, h, "vuestros clanes están aliados: rompe antes la alianza")
	})
	t.Run("una guerra declarada abierta por clan; las recibidas no cuentan", func(t *testing.T) {
		h, _, vs, lob, oso := conGuerra(t, nil)
		tercero := otroVecino(h, 5)
		h.fundarClan(tercero, "Los Ciervos", "CIE")
		cie, _ := h.state.ClanOf(tercero)
		h.declararGuerra(h.playerID, oso)
		require.Empty(t, h.codigos())
		h.declararGuerra(h.playerID, cie)
		rechazo(t, h, "tu clan ya tiene el máximo de guerras declaradas")
		h.declararGuerra(tercero, lob)
		require.Empty(t, h.codigos(), "las recibidas no cuentan para el tope de LOB")
		_ = vs
	})
	t.Run("la tregua tras una guerra, en los dos sentidos", func(t *testing.T) {
		h, _, vs, lob, oso := conGuerra(t, func(d *simulation.Deps) { d.ClanWarCooldown = time.Hour })
		g := h.enGuerra(lob, oso)
		h.rendirse(vs[0], g.ID)
		require.Empty(t, h.codigos())
		h.declararGuerra(h.playerID, oso)
		rechazo(t, h, "vuestros clanes terminaron una guerra hace poco: espera a que acabe la tregua")
		h.declararGuerra(vs[0], lob)
		rechazo(t, h, "vuestros clanes terminaron una guerra hace poco: espera a que acabe la tregua")
		assert.False(t, h.state.AtWar(h.playerID, vs[0]), "la tregua no es guerra")
		h.advance(time.Hour + time.Second)
		h.declararGuerra(vs[0], lob)
		require.Empty(t, h.codigos(), "pasada la tregua")
	})
	t.Run("la base que no lo admite deshace la reserva", func(t *testing.T) {
		h, fc, _, lob, oso := conGuerra(t, nil)
		fc.warErr = simulation.ClanRejection{Reason: "vuestros clanes están aliados o uno se disolvió"}
		h.declararGuerra(h.playerID, oso)
		rechazo(t, h, "vuestros clanes están aliados o uno se disolvió")
		_, abierta := guerraEntre(h, lob, oso)
		assert.False(t, abierta)
		assert.False(t, h.state.AtWar(h.playerID, h.playerID))
		fc.warErr = nil
		h.declararGuerra(h.playerID, oso)
		require.Empty(t, h.codigos(), "la reserva se liberó")
	})
}

// RN-CLANWAR-008: la rendición da la victoria al otro; el plazo, al de más
// puntos o empate; la disolución, al otro, en la transacción de la disolución
// (ADR-045 §5).
func TestLosFinalesDeUnaGuerra(t *testing.T) {
	t.Run("rendición", func(t *testing.T) {
		h, _, vs, lob, oso := conGuerra(t, nil)
		g := h.enGuerra(lob, oso)
		h.rendirse(vs[2], g.ID)
		assert.Equal(t, "sólo el líder decide las guerras del clan", h.ultimoMotivo())
		h.rec.reset()
		h.rendirse(h.playerID, g.ID)
		require.Empty(t, h.codigos())
		assert.Equal(t, clan.WarRecord{Lost: 1}, h.state.ClanWarRecord(lob))
		assert.Equal(t, clan.WarRecord{Won: 1}, h.state.ClanWarRecord(oso))
		finales := avisosDeTipo(h, eventlog.KindClanWarEnded)
		require.Len(t, finales, 3, "a todos menos a quien se rindió")
		for _, a := range finales {
			want := clan.WarWon
			if a.to == vs[2] {
				want = clan.WarLost
			}
			assert.Equal(t, want, a.p.WarResult)
			assert.Equal(t, clan.WarEndSurrender, a.p.WarReason)
		}
	})
	t.Run("el plazo, a puntos y con empate", func(t *testing.T) {
		h, fc, vs, lob, oso := conGuerra(t, nil)
		g := h.enGuerra(lob, oso)
		h.state.SetClanWars([]clan.War{g.AddPoints(oso, 3)}, nil, nil)
		h.clk.AdvanceMs(g.EndsAtMs - 1 - h.clk.NowMs())
		h.loop.Step(h.clk.NowMs())
		_, abierta := h.state.ClanWar(g.ID)
		assert.True(t, abierta, "hasta el último milisegundo del plazo")
		require.True(t, h.tickHasta(20, func() bool { _, abierta := h.state.ClanWar(g.ID); return !abierta }))
		assert.Equal(t, clan.WarRecord{Won: 1}, h.state.ClanWarRecord(oso))
		fin := fc.wars[len(fc.wars)-1]
		assert.True(t, fin.Tolerant)
		assert.Equal(t, clan.WarEndTimeout, fin.War.EndReason)
		assert.Equal(t, oso, fin.War.Winner)
		assert.Equal(t, g.EndsAtMs, fin.War.EndedAtMs)
		finales := avisosDeTipo(h, eventlog.KindClanWarEnded)
		require.Len(t, finales, 4, "un plazo no lo dio nadie: avisa a todos")
		_ = vs

		h2, _, _, lob2, oso2 := conGuerra(t, nil)
		g2 := h2.enGuerra(lob2, oso2)
		h2.clk.AdvanceMs(g2.EndsAtMs - h2.clk.NowMs())
		h2.tick()
		assert.Equal(t, clan.WarRecord{Drawn: 1}, h2.state.ClanWarRecord(lob2), "con los mismos puntos, empate")
	})
	t.Run("la disolución da la victoria al otro", func(t *testing.T) {
		h, fc, _, lob, _ := conGuerra(t, nil)
		solo := otroVecino(h, 5)
		h.fundarClan(solo, "Los Ciervos", "CIE")
		cie, _ := h.state.ClanOf(solo)
		g := h.enGuerra(lob, cie)
		h.send(simulation.LeaveClan{PlayerID: solo, RequestID: uuid.NewString()})
		h.tick()
		require.Empty(t, h.codigos())
		_, abierta := h.state.ClanWar(g.ID)
		assert.False(t, abierta, "la guerra termina en el tick de la disolución")
		assert.Equal(t, clan.WarRecord{Won: 1}, h.state.ClanWarRecord(lob))
		l := fc.lastLeave(t)
		require.True(t, l.Dissolve)
		require.Len(t, l.WarEnds, 1, "la disolución lleva el final a su transacción")
		assert.Equal(t, clan.WarEndDissolved, l.WarEnds[0].EndReason)
		assert.Equal(t, lob, l.WarEnds[0].Winner)
		finales := avisosDeTipo(h, eventlog.KindClanWarEnded)
		require.NotEmpty(t, finales)
		for _, a := range finales {
			assert.NotEqual(t, solo, a.to)
			assert.Equal(t, clan.WarWon, a.p.WarResult)
		}
	})
}

// RN-CLANWAR-007 e INV-CLAN-009: qué puntúa y qué no. La preparación no puntúa,
// ni quien salió del clan; un derribo suma 3. (Una caravana que mata una unidad
// se captura, no muere: no pasa por la muerte que puntúa.)
func TestQuePuntuaEnUnaGuerra(t *testing.T) {
	h, _, vs, lob, oso := conGuerra(t, nil)
	osoMiembro := vs[1]
	h.declararGuerra(h.playerID, oso)
	g, _ := guerraEntre(h, lob, oso)
	c3, _ := h.state.City(3)
	c3.Population = 5

	// En preparación, una muerte no puntúa.
	ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	v := ponerUnidad(h, enemigoID, osoMiembro, 3, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 8)
	h.atacar(guerreroID, v.ID)
	require.True(t, h.tickHasta(30, func() bool { _, vivo := h.state.Unit(v.ID); return !vivo }))
	g, _ = h.state.ClanWar(g.ID)
	assert.Zero(t, g.ScoreOf(lob), "la preparación no puntúa")
	assert.Nil(t, h.combat.deaths[len(h.combat.deaths)-1].War)

	h.clk.AdvanceMs(g.StartsAtMs - h.clk.NowMs())
	h.tick()

	// Un derribo de una casa del enemigo suma 3.
	h.conectar()
	conCasa(h, c3, casaID, world.Tile{X: c3.CenterX, Y: c3.CenterY - 1}, false)
	ponerUnidad(h, guerreroID+1, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: c3.CenterX - 1, Y: c3.CenterY - 2}, 60)
	h.derribar(guerreroID+1, casaID)
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(600, func() bool { _, enPie := vidaDe(h, casaID); return !enPie }))
	g, _ = h.state.ClanWar(g.ID)
	assert.Equal(t, int64(clan.PointsBuilding), g.ScoreOf(lob))
	require.NotEmpty(t, h.economy.destroys)
	assert.Equal(t, &clan.WarPoints{WarID: g.ID, ClanID: lob, Points: 3}, h.economy.destroys[len(h.economy.destroys)-1].War)

	// Quien salió del clan ya no está en guerra: su muerte no puntúa.
	h.send(simulation.LeaveClan{PlayerID: osoMiembro, RequestID: uuid.NewString()})
	h.tick()
	v2 := ponerUnidad(h, enemigoID+2, osoMiembro, 3, unit.TypeVillager, world.Tile{X: 20, Y: 19}, 8)
	h.advance(16 * time.Minute) // pasa su paz residual con los suyos
	h.atacar(guerreroID, v2.ID)
	require.True(t, h.tickHasta(30, func() bool { _, vivo := h.state.Unit(v2.ID); return !vivo }))
	g, _ = h.state.ClanWar(g.ID)
	assert.Equal(t, int64(clan.PointsBuilding), g.ScoreOf(lob), "fuera del clan, fuera de la guerra")
	assert.False(t, h.state.AtWar(h.playerID, osoMiembro))
}

// ADR-045 §3 e INV-DIPLO-004: quien entra en un clan en guerra entra en ella, y
// una aceptación que estaba en vuelo al declararse empieza su preaviso al
// volver. La guerra no cambia la paz: dos de la Orden siguen en paz.
func TestElCompromisoAlcanzaATodos(t *testing.T) {
	t.Run("quien entra en un clan en guerra", func(t *testing.T) {
		h, _, vs, lob, oso := conGuerra(t, nil)
		nuevo := otroVecino(h, 5)
		h.state.SetTreaties([]diplomacy.Treaty{vigente(101, nuevo, vs[1], diplomacy.TypeAlliance)}, nil)
		h.enGuerra(lob, oso)
		pacto, _ := h.state.Treaty(101)
		assert.Nil(t, pacto.BreakEffectiveAt, "todavía no es de ningún clan")
		h.invitarAlClan(h.playerID, nuevo)
		h.aceptarClan(nuevo, lob)
		require.Empty(t, h.codigos())
		pacto, _ = h.state.Treaty(101)
		require.NotNil(t, pacto.BreakEffectiveAt, "en el tick en que vuelve su entrada")
		assert.Equal(t, h.clk.NowMs()+10_000, pacto.BreakEffectiveAt.UnixMilli())
	})
	t.Run("una aceptación en vuelo al declarar", func(t *testing.T) {
		h, _, vs, _, oso := conGuerra(t, nil)
		h.proponer(vs[1], vs[2], diplomacy.TypeNonAggression)
		require.Empty(t, h.codigos())
		var oferta diplomacy.Treaty
		for _, tr := range h.state.TreatiesBetween(vs[1], vs[2]) {
			oferta = tr
		}
		require.Equal(t, diplomacy.StatusProposed, oferta.Status)
		h.commands <- simulation.RespondTreaty{PlayerID: vs[2], RequestID: uuid.NewString(), TreatyID: oferta.ID, Accept: true}
		h.commands <- simulation.DeclareClanWar{PlayerID: h.playerID, RequestID: uuid.NewString(), ClanID: oso}
		h.loop.Step(h.clk.NowMs())
		require.Empty(t, h.codigos(), "la aceptación se validó antes de la declaración")
		h.tick()
		pacto, ok := h.state.Treaty(oferta.ID)
		require.True(t, ok)
		assert.Equal(t, diplomacy.StatusActive, pacto.Status)
		require.NotNil(t, pacto.BreakEffectiveAt, "nadie se queda con un pacto vivo dentro de la guerra")
	})
	t.Run("la paz de la Orden sigue", func(t *testing.T) {
		h, _, vs, lob, oso := conGuerra(t, nil)
		c1 := h.ciudad()
		c1.Faction = "ORDER"
		c2, _ := h.state.City(2)
		c2.Faction = "ORDER"
		h.enGuerra(lob, oso)
		assert.True(t, h.state.AtPeace(h.playerID, vs[0], h.clk.Now()), "RN-CLANWAR-005: la guerra no levanta la paz de la Orden")
		assert.False(t, h.state.AtPeace(h.playerID, vs[1], h.clk.Now()))
	})
}

// ADR-045 §1 y §4: las guerras abiertas, con el marcador sumado de sus eventos,
// las recientes (la tregua) y el historial vuelven al arrancar; una preparación
// que venció con el servidor parado empieza en el primer barrido; la carga no
// instala una guerra entre aliados ni una con un clan muerto.
func TestLasGuerrasTrasUnReinicio(t *testing.T) {
	h, _, vs, lob, oso := conGuerra(t, func(d *simulation.Deps) { d.ClanWarCooldown = time.Hour })
	now := h.clk.NowMs()
	abierta := clan.War{ID: 7, Attacker: oso, Defender: lob, DeclaredBy: vs[0], Status: clan.WarDeclared,
		DeclaredAtMs: now - 60_000, StartsAtMs: now - 1, AttackerScore: 0, DefenderScore: 2}
	reciente := clan.War{ID: 5, Attacker: lob, Defender: 99, Status: clan.WarEnded, EndedAtMs: now - 1000}
	malas := h.state.SetClanWars([]clan.War{abierta, {ID: 8, Attacker: lob, Defender: 99, Status: clan.WarActive}},
		[]clan.War{reciente}, map[int64]clan.WarRecord{lob: {Won: 2}})
	require.Len(t, malas, 1)
	assert.Equal(t, int64(8), malas[0].WarID)
	g, ok := h.state.ClanWar(7)
	require.True(t, ok)
	assert.Equal(t, int64(2), g.ScoreOf(lob), "el marcador de la carga")
	assert.Equal(t, clan.WarRecord{Won: 2}, h.state.ClanWarRecord(lob))
	assert.True(t, h.state.AtWar(h.playerID, vs[1]))

	h.tick()
	g, _ = h.state.ClanWar(7)
	assert.Equal(t, clan.WarActive, g.Status, "la preparación que venció parado empieza en el primer barrido")
	assert.Equal(t, abierta.StartsAtMs+3_600_000, g.EndsAtMs, "con el plazo desde su comienzo")

	// Una abierta entre dos aliados no se instala.
	h2, _, vs2, lob2, oso2 := conGuerra(t, nil)
	h2.aliarse(h2.playerID, lob2, vs2[0], oso2)
	malas = h2.state.SetClanWars([]clan.War{{ID: 9, Attacker: lob2, Defender: oso2, Status: clan.WarActive}}, nil, nil)
	require.Len(t, malas, 1)
	assert.Equal(t, "abierta entre dos clanes aliados", malas[0].Detail)
}
