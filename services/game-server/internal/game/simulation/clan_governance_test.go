package simulation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests del gobierno del clan (SPEC-CLAN-GOVERNANCE §9, simulation). El clan de
// clanConMiembros es «Los Lobos» [LOB], con el jugador del arnés de líder.

// nombrar da clan.promote en nombre de quien.
func (h *harness) nombrar(quien, a uuid.UUID, oficial bool) {
	h.t.Helper()
	h.send(simulation.PromoteInClan{PlayerID: quien, RequestID: uuid.NewString(), TargetID: a, Officer: oficial})
}

// ceder da clan.transfer en nombre de quien.
func (h *harness) ceder(quien, a uuid.UUID) {
	h.t.Helper()
	h.send(simulation.TransferClan{PlayerID: quien, RequestID: uuid.NewString(), TargetID: a})
}

// rangoDe es el rango de un jugador en su clan, o "" si no tiene clan.
func rangoDe(h *harness, p uuid.UUID) string {
	r, _ := h.state.ClanRole(p)
	return r
}

// rolesEnLaVista son los rangos de la vista del clan del último clan.update que
// recibió p, por jugador.
func rolesEnLaVista(h *harness, p uuid.UUID) map[string]string {
	h.t.Helper()
	ups := h.clanUpdatesPara(p)
	require.NotEmpty(h.t, ups)
	last := ups[len(ups)-1]
	require.NotNil(h.t, last.Clan)
	out := map[string]string{}
	for _, m := range last.Clan.Members {
		out[m.PlayerID] = m.Role
	}
	return out
}

// RN-CLANGOV-002, RN-CLANGOV-003 e INV-CLAN-005 en el tick: la tabla de §4.2,
// orden a orden y rango a rango, con su código y su motivo.
func TestLosPermisosPorRango(t *testing.T) {
	preparar := func(t *testing.T) (*harness, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
		h, _ := conClanes(t, nil)
		vs := conVecinos(h, 4)
		oficial, otroOficial, raso, fuera := vs[0], vs[1], vs[2], vs[3]
		h.clanConMiembros(oficial, otroOficial, raso)
		h.nombrar(h.playerID, oficial, true)
		h.nombrar(h.playerID, otroOficial, true)
		require.Empty(t, h.codigos())
		h.rec.reset()
		return h, oficial, otroOficial, raso, fuera
	}
	rechazo := func(t *testing.T, h *harness, motivo string) {
		t.Helper()
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Equal(t, motivo, h.ultimoMotivo())
	}

	t.Run("un oficial invita, retira la invitación del líder y expulsa a un raso", func(t *testing.T) {
		h, oficial, _, raso, fuera := preparar(t)
		h.send(simulation.InviteToClan{PlayerID: oficial, RequestID: uuid.NewString(), TargetID: fuera, Invited: true})
		h.tick()
		require.Empty(t, h.codigos())
		ups := h.clanUpdatesPara(oficial)
		require.NotEmpty(t, ups)
		require.Len(t, ups[len(ups)-1].Clan.SentInvitations, 1, "un oficial ve las invitaciones enviadas")
		require.NotEmpty(t, h.clanUpdatesPara(fuera), "al invitado le llega")

		h.send(simulation.InviteToClan{PlayerID: oficial, RequestID: uuid.NewString(), TargetID: fuera, Invited: false})
		require.Empty(t, h.codigos())
		h.send(simulation.ExpelFromClan{PlayerID: oficial, RequestID: uuid.NewString(), TargetID: raso})
		require.Empty(t, h.codigos())
		_, sigue := h.state.ClanOf(raso)
		assert.False(t, sigue)
	})
	t.Run("un oficial no expulsa a otro oficial ni al líder", func(t *testing.T) {
		h, oficial, otroOficial, _, _ := preparar(t)
		h.send(simulation.ExpelFromClan{PlayerID: oficial, RequestID: uuid.NewString(), TargetID: otroOficial})
		rechazo(t, h, "un oficial no puede expulsar a otro oficial")
		h.rec.reset()
		h.send(simulation.ExpelFromClan{PlayerID: oficial, RequestID: uuid.NewString(), TargetID: h.playerID})
		rechazo(t, h, "al líder no lo expulsa nadie")
	})
	t.Run("el líder expulsa a un oficial", func(t *testing.T) {
		h, oficial, _, _, _ := preparar(t)
		h.send(simulation.ExpelFromClan{PlayerID: h.playerID, RequestID: uuid.NewString(), TargetID: oficial})
		require.Empty(t, h.codigos())
		assert.Empty(t, rangoDe(h, oficial))
	})
	t.Run("un raso no invita ni expulsa", func(t *testing.T) {
		h, oficial, _, raso, fuera := preparar(t)
		h.send(simulation.InviteToClan{PlayerID: raso, RequestID: uuid.NewString(), TargetID: fuera, Invited: true})
		rechazo(t, h, "sólo el líder o un oficial invita")
		h.rec.reset()
		h.send(simulation.ExpelFromClan{PlayerID: raso, RequestID: uuid.NewString(), TargetID: oficial})
		rechazo(t, h, "sólo el líder o un oficial expulsa")
	})
	t.Run("sólo el líder nombra y cede", func(t *testing.T) {
		h, oficial, _, raso, fuera := preparar(t)
		for _, p := range []uuid.UUID{oficial, raso, fuera} {
			h.rec.reset()
			h.nombrar(p, raso, true)
			rechazo(t, h, "sólo el líder nombra oficiales y cede el liderazgo")
			h.rec.reset()
			h.ceder(p, raso)
			rechazo(t, h, "sólo el líder nombra oficiales y cede el liderazgo")
		}
		h.rec.reset()
		h.nombrar(h.playerID, h.playerID, true)
		rechazo(t, h, "no puedes hacerlo sobre ti mismo")
		h.rec.reset()
		h.ceder(h.playerID, fuera)
		rechazo(t, h, "ese jugador no es de tu clan")
		h.rec.reset()
		h.nombrar(h.playerID, oficial, true)
		rechazo(t, h, "ya es oficial")
		h.rec.reset()
		h.nombrar(h.playerID, raso, false)
		rechazo(t, h, "no es oficial")
	})
}

// §4.3: nombrar y quitar un oficial es optimista, llega a todos los miembros
// con el rango en la vista y se escribe con la clave del clan.
func TestNombrarYQuitarUnOficial(t *testing.T) {
	h, fc := conClanes(t, nil)
	vs := conVecinos(h, 2)
	a, b := vs[0], vs[1]
	clanID := h.clanConMiembros(a, b)

	h.rec.reset()
	h.nombrar(h.playerID, a, true)
	require.Empty(t, h.codigos())
	assert.Equal(t, clan.RoleOfficer, rangoDe(h, a), "en el acto")
	for _, p := range []uuid.UUID{h.playerID, a, b} {
		roles := rolesEnLaVista(h, p)
		assert.Equal(t, clan.RoleOfficer, roles[a.String()], "cada miembro ve el rango")
		assert.Equal(t, clan.RoleLeader, roles[h.playerID.String()])
		assert.Equal(t, clan.RoleMember, roles[b.String()])
	}
	require.Len(t, fc.ranks, 1)
	assert.Equal(t, simulation.ClanRankOrder{ClanID: clanID, PlayerID: a, By: h.playerID, Officer: true,
		At: fc.ranks[0].At, Tick: fc.ranks[0].Tick, AtMs: fc.ranks[0].AtMs}, fc.ranks[0])
	assert.Contains(t, h.persister.names(), simulation.JobClanRank)

	h.nombrar(h.playerID, a, false)
	require.Empty(t, h.codigos())
	assert.Equal(t, clan.RoleMember, rangoDe(h, a))
	require.Len(t, fc.ranks, 2)
	assert.False(t, fc.ranks[1].Officer)
}

// Q4 y Q8: ceder el liderazgo es inmediato y sin aceptación; el anterior queda
// como oficial, el nuevo deja de serlo, todos lo ven y sólo el nuevo recibe el
// aviso, con la sigla.
func TestCederElLiderazgo(t *testing.T) {
	h, fc := conClanes(t, nil)
	vs := conVecinos(h, 2)
	a, b := vs[0], vs[1]
	clanID := h.clanConMiembros(a, b)
	h.nombrar(h.playerID, a, true)

	h.rec.reset()
	h.ceder(h.playerID, a)
	require.Empty(t, h.codigos())
	lider, _ := h.state.ClanLeader(clanID)
	assert.Equal(t, a, lider)
	assert.Equal(t, clan.RoleLeader, rangoDe(h, a), "el oficial deja de serlo al liderar")
	assert.Equal(t, clan.RoleOfficer, rangoDe(h, h.playerID), "Q4: el anterior queda como oficial")
	for _, p := range []uuid.UUID{h.playerID, a, b} {
		ups := h.clanUpdatesPara(p)
		require.NotEmpty(t, ups)
		assert.Equal(t, a.String(), ups[len(ups)-1].Clan.LeaderID)
	}
	recibido := avisosDeTipo(h, eventlog.KindClanLeadershipReceived)
	require.Len(t, recibido, 1)
	assert.Equal(t, a, recibido[0].to)
	assert.Equal(t, "LOB", recibido[0].p.ClanTag)
	assert.Equal(t, h.playerID.String(), recibido[0].p.OtherPlayerID)
	assert.Len(t, avisosDe(h), 1, "a quien lo cede, nada")

	require.Len(t, fc.leaders, 1)
	o := fc.leaders[0]
	assert.Equal(t, h.playerID, o.From)
	assert.Equal(t, a, o.To)
	assert.Equal(t, h.playerID, o.By)
	assert.Equal(t, eventlog.LeaderReasonTransfer, o.Reason)
	assert.Equal(t, "LOB", o.Tag)
	assert.Contains(t, h.persister.names(), simulation.JobClanLeader)

	// El nuevo líder ya gobierna; el anterior, no.
	h.rec.reset()
	h.nombrar(h.playerID, b, true)
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	h.rec.reset()
	h.ceder(a, h.playerID)
	require.Empty(t, h.codigos())
	assert.Equal(t, clan.RoleLeader, rangoDe(h, h.playerID))
	assert.Equal(t, clan.RoleOfficer, rangoDe(h, a))
}

// §7 e INV-CLAN-005: si la base no admite un traspaso o un nombramiento, o no
// responde, la RAM lo deshace y quien lo dio recibe el motivo.
func TestLoQueLaBaseNoEscribeSeDeshace(t *testing.T) {
	t.Run("un traspaso rechazado", func(t *testing.T) {
		h, fc := conClanes(t, nil)
		vs := conVecinos(h, 2)
		clanID := h.clanConMiembros(vs[0], vs[1])
		h.nombrar(h.playerID, vs[0], true)
		fc.leaderErr = simulation.ClanRejection{Reason: "quien cede el liderazgo ya no es el líder"}
		h.ceder(h.playerID, vs[1])
		lider, _ := h.state.ClanLeader(clanID)
		require.Equal(t, vs[1], lider, "optimista")
		h.rec.reset()
		h.tick()
		lider, _ = h.state.ClanLeader(clanID)
		assert.Equal(t, h.playerID, lider)
		assert.Equal(t, clan.RoleMember, rangoDe(h, vs[1]))
		assert.Equal(t, clan.RoleOfficer, rangoDe(h, vs[0]), "el otro oficial sigue")
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Equal(t, "quien cede el liderazgo ya no es el líder", h.ultimoMotivo())
		assert.Equal(t, h.playerID.String(), rolesEnLaVistaLider(h, vs[0]))
	})
	t.Run("un traspaso que no llega a la base", func(t *testing.T) {
		h, fc := conClanes(t, nil)
		vs := conVecinos(h, 1)
		clanID := h.clanConMiembros(vs[0])
		h.nombrar(h.playerID, vs[0], true)
		fc.leaderErr = assert.AnError
		h.persister.permanent = true
		h.ceder(h.playerID, vs[0])
		h.rec.reset()
		h.tick()
		lider, _ := h.state.ClanLeader(clanID)
		assert.Equal(t, h.playerID, lider)
		assert.Equal(t, clan.RoleOfficer, rangoDe(h, vs[0]), "vuelve a su rango de antes")
		assert.Equal(t, []string{protocol.CodeInternalError}, h.codigos())
	})
	t.Run("un nombramiento rechazado", func(t *testing.T) {
		h, fc := conClanes(t, nil)
		vs := conVecinos(h, 1)
		h.clanConMiembros(vs[0])
		fc.rankErr = simulation.ClanRejection{Reason: "sólo el líder nombra oficiales"}
		h.nombrar(h.playerID, vs[0], true)
		require.Equal(t, clan.RoleOfficer, rangoDe(h, vs[0]))
		h.rec.reset()
		h.tick()
		assert.Equal(t, clan.RoleMember, rangoDe(h, vs[0]))
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	})
	t.Run("una expulsión que la base no admite", func(t *testing.T) {
		h, fc := conClanes(t, nil)
		vs := conVecinos(h, 2)
		oficial, raso := vs[0], vs[1]
		clanID := h.clanConMiembros(oficial, raso)
		h.nombrar(h.playerID, oficial, true)
		h.nombrar(h.playerID, raso, true)
		h.nombrar(h.playerID, raso, false)
		fc.leaveErr = simulation.ClanRejection{Reason: "quien expulsa no tiene el rango para hacerlo"}
		h.send(simulation.ExpelFromClan{PlayerID: oficial, RequestID: uuid.NewString(), TargetID: raso})
		_, sigue := h.state.ClanOf(raso)
		require.False(t, sigue, "optimista")
		h.rec.reset()
		h.tick()
		id, sigue := h.state.ClanOf(raso)
		require.True(t, sigue, "vuelve al clan")
		assert.Equal(t, clanID, id)
		assert.Equal(t, clan.RoleMember, rangoDe(h, raso))
		c, ok := h.state.CityOf(raso)
		require.True(t, ok)
		require.NotNil(t, c.Clan)
		assert.Equal(t, "LOB", c.Clan.Tag)
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.False(t, h.state.ClanPeace(raso, uuid.New(), h.clk.Now()))
	})
}

// rolesEnLaVistaLider es el líder del último clan.update que recibió p.
func rolesEnLaVistaLider(h *harness, p uuid.UUID) string {
	h.t.Helper()
	ups := h.clanUpdatesPara(p)
	require.NotEmpty(h.t, ups)
	require.NotNil(h.t, ups[len(ups)-1].Clan)
	return ups[len(ups)-1].Clan.LeaderID
}

// RN-CLANGOV-004: al salir el líder hereda el oficial más antiguo, aunque haya
// rasos más antiguos, y deja de ser oficial.
func TestAlSalirElLiderHeredaElOficialMasAntiguo(t *testing.T) {
	h, fc := conClanes(t, nil)
	vs := conVecinos(h, 3)
	clanID := h.clanConMiembros(vs[0], vs[1], vs[2])
	h.nombrar(h.playerID, vs[2], true)
	h.nombrar(h.playerID, vs[1], true)

	h.send(simulation.LeaveClan{PlayerID: h.playerID, RequestID: uuid.NewString()})
	require.Empty(t, h.codigos())
	lider, _ := h.state.ClanLeader(clanID)
	assert.Equal(t, vs[1], lider, "el oficial más antiguo, no el raso que entró antes")
	assert.Equal(t, clan.RoleLeader, rangoDe(h, vs[1]))
	assert.Equal(t, clan.RoleOfficer, rangoDe(h, vs[2]))
	assert.Equal(t, vs[1], fc.lastLeave(t).NewLeader)
}

// ausente deja la ciudad de p desconectada desde at.
func ausente(h *harness, p uuid.UUID, at time.Time) {
	h.t.Helper()
	c, ok := h.state.CityOf(p)
	require.True(h.t, ok)
	c.PresenceState, c.LastOfflineAt = city.PresenceProtected, &at
}

// Golden «líder ausente» (RN-CLANGOV-005, Q6, Q7, Q8): el barrido de cada
// minuto no hace nada mientras al líder le falta un instante para los 7 días;
// en el barrido en que se cumplen, el liderazgo pasa al oficial activo más
// antiguo —saltando al oficial ausente—, el líder ausente queda como oficial,
// cada miembro recibe su clan.update y el nuevo líder el aviso INHERITED.
func TestGoldenLiderAusente(t *testing.T) {
	h, fc := conClanes(t, nil)
	vs := conVecinos(h, 3)
	oficialAusente, oficial, raso := vs[0], vs[1], vs[2]
	clanID := h.clanConMiembros(oficialAusente, oficial, raso)
	h.nombrar(h.playerID, oficialAusente, true)
	h.nombrar(h.playerID, oficial, true)

	t0 := h.clk.Now()
	ausente(h, h.playerID, t0)
	ausente(h, oficialAusente, t0.Add(-time.Hour))
	h.tick() // un barrido con el líder recién desconectado
	siete := clan.DefaultLeaderInactive

	h.clk.SetMs(t0.Add(siete - time.Minute).UnixMilli())
	h.loop.Step(h.clk.NowMs())
	lider, _ := h.state.ClanLeader(clanID)
	require.Equal(t, h.playerID, lider, "un minuto antes, nada")
	h.clk.SetMs(t0.Add(siete).UnixMilli() - 100)
	h.loop.Step(h.clk.NowMs())
	lider, _ = h.state.ClanLeader(clanID)
	require.Equal(t, h.playerID, lider, "entre dos barridos, nada")

	h.rec.reset()
	h.clk.SetMs(t0.Add(siete).UnixMilli())
	h.loop.Step(h.clk.NowMs())
	lider, _ = h.state.ClanLeader(clanID)
	assert.Equal(t, oficial, lider, "el oficial activo más antiguo, saltando al ausente")
	assert.Equal(t, clan.RoleOfficer, rangoDe(h, h.playerID), "Q7: el ausente queda como oficial")
	assert.Equal(t, clan.RoleOfficer, rangoDe(h, oficialAusente))
	for _, p := range []uuid.UUID{h.playerID, oficialAusente, oficial, raso} {
		assert.Equal(t, oficial.String(), rolesEnLaVistaLider(h, p))
	}
	heredado := avisosDeTipo(h, eventlog.KindClanLeadershipInherited)
	require.Len(t, heredado, 1)
	assert.Equal(t, oficial, heredado[0].to)
	assert.Equal(t, "LOB", heredado[0].p.ClanTag)
	require.Len(t, fc.leaders, 1)
	assert.Equal(t, eventlog.LeaderReasonInactive, fc.leaders[0].Reason)
	assert.Equal(t, uuid.Nil, fc.leaders[0].By, "no lo ordenó nadie")
	assert.Equal(t, h.playerID, fc.leaders[0].From)

	// Al volver, no lo recupera solo.
	h.ciudad().PresenceState = city.PresenceOnline
	h.clk.Advance(2 * time.Minute)
	h.loop.Step(h.clk.NowMs())
	lider, _ = h.state.ClanLeader(clanID)
	assert.Equal(t, oficial, lider)
}

// RN-CLANGOV-005: sin oficiales activos, hereda el raso activo más antiguo; sin
// nadie activo, el liderazgo no cambia.
func TestLaSucesionSinOficialesYSinNadie(t *testing.T) {
	t.Run("el raso activo más antiguo", func(t *testing.T) {
		h, _ := conClanes(t, func(d *simulation.Deps) { d.ClanLeaderInactive = time.Hour })
		vs := conVecinos(h, 2)
		clanID := h.clanConMiembros(vs[0], vs[1])
		ausente(h, h.playerID, h.clk.Now())
		ausente(h, vs[0], h.clk.Now())
		h.clk.Advance(time.Hour)
		h.loop.Step(h.clk.NowMs())
		lider, _ := h.state.ClanLeader(clanID)
		assert.Equal(t, vs[1], lider)
	})
	t.Run("nadie activo", func(t *testing.T) {
		h, fc := conClanes(t, func(d *simulation.Deps) { d.ClanLeaderInactive = time.Hour })
		vs := conVecinos(h, 1)
		clanID := h.clanConMiembros(vs[0])
		ausente(h, h.playerID, h.clk.Now())
		ausente(h, vs[0], h.clk.Now())
		h.clk.Advance(2 * time.Hour)
		h.loop.Step(h.clk.NowMs())
		lider, _ := h.state.ClanLeader(clanID)
		assert.Equal(t, h.playerID, lider)
		assert.Empty(t, fc.leaders)
	})
}

// §9 recuperación: los rangos vuelven al arrancar; un líder cuya ausencia se
// cumplió con el servidor parado lo pierde en el primer barrido, y uno que se
// desconectó hace poco, no.
func TestLosRangosYLaAusenciaTrasUnReinicio(t *testing.T) {
	cargar := func(t *testing.T, desconectado time.Duration) (*harness, uuid.UUID, uuid.UUID) {
		h, _ := conClanes(t, nil)
		vs := conVecinos(h, 2)
		oficial, raso := vs[0], vs[1]
		h.state.SetClans([]clan.Clan{{ID: 7, Name: "Los Osos", Tag: "OSO", LeaderID: h.playerID}}, []clan.Stay{
			{ClanID: 7, PlayerID: h.playerID, JoinedAtMs: 1},
			{ClanID: 7, PlayerID: oficial, JoinedAtMs: 2, Officer: true},
			{ClanID: 7, PlayerID: raso, JoinedAtMs: 3},
		}, nil, 0)
		ausente(h, h.playerID, h.clk.Now().Add(-desconectado))
		return h, oficial, raso
	}
	t.Run("los rangos vuelven", func(t *testing.T) {
		h, oficial, raso := cargar(t, time.Hour)
		assert.Equal(t, clan.RoleLeader, rangoDe(h, h.playerID))
		assert.Equal(t, clan.RoleOfficer, rangoDe(h, oficial))
		assert.Equal(t, clan.RoleMember, rangoDe(h, raso))
		h.tick()
		lider, _ := h.state.ClanLeader(7)
		assert.Equal(t, h.playerID, lider, "desconectado hace poco: el reinicio no lo cuenta")
	})
	t.Run("la ausencia cumplida con el servidor parado", func(t *testing.T) {
		h, oficial, _ := cargar(t, 8*24*time.Hour)
		h.tick()
		lider, _ := h.state.ClanLeader(7)
		assert.Equal(t, oficial, lider, "en el primer barrido")
	})
}

// §4.2: mientras el clan se disuelve (E19), no se nombra ni se cede.
func TestUnClanQueSeDisuelveNoCambiaDeRangos(t *testing.T) {
	h, _ := conClanes(t, nil)
	conCuatroTerritorios(h, map[int64]uuid.UUID{1: h.playerID})
	vs := conVecinos(h, 1)
	clanID := h.clanConMiembros(vs[0])
	territorioDelClan(h, 2, clanID)
	h.send(simulation.ExpelFromClan{PlayerID: h.playerID, RequestID: uuid.NewString(), TargetID: vs[0]})
	h.commands <- simulation.LeaveClan{PlayerID: h.playerID, RequestID: uuid.NewString()}
	h.commands <- simulation.TransferClan{PlayerID: h.playerID, RequestID: uuid.NewString(), TargetID: vs[0]}
	h.rec.reset()
	h.loop.Step(h.clk.NowMs())
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, "el clan se está disolviendo", h.ultimoMotivo())
}

// §9 y la cola: nombrar o quitar un oficial, ceder el liderazgo y la sucesión
// por ausencia se escriben con la clave del clan (clan:<id>) y sin segunda
// clave, para ir en orden con las demás escrituras del mismo clan.
func TestLasClavesDelGobiernoEnLaCola(t *testing.T) {
	soloDelClan := func(nombre string, id int64, n int) []clavesDeTrabajo {
		out := make([]clavesDeTrabajo, n)
		for i := range out {
			out[i] = clavesDeTrabajo{Name: nombre, Key: simulation.ClanKey(id)}
		}
		return out
	}
	t.Run("nombrar, quitar y ceder", func(t *testing.T) {
		h, _ := conClanes(t, nil)
		vs := conVecinos(h, 1)
		clanID := h.clanConMiembros(vs[0])
		h.nombrar(h.playerID, vs[0], true)
		h.nombrar(h.playerID, vs[0], false)
		h.ceder(h.playerID, vs[0])
		require.Empty(t, h.codigos())
		assert.Equal(t, soloDelClan(simulation.JobClanRank, clanID, 2), h.persister.clavesDe(simulation.JobClanRank))
		assert.Equal(t, soloDelClan(simulation.JobClanLeader, clanID, 1), h.persister.clavesDe(simulation.JobClanLeader))
	})
	t.Run("la sucesión por ausencia", func(t *testing.T) {
		h, _ := conClanes(t, func(d *simulation.Deps) { d.ClanLeaderInactive = time.Hour })
		vs := conVecinos(h, 1)
		clanID := h.clanConMiembros(vs[0])
		ausente(h, h.playerID, h.clk.Now())
		h.clk.Advance(time.Hour)
		h.loop.Step(h.clk.NowMs())
		lider, _ := h.state.ClanLeader(clanID)
		require.Equal(t, vs[0], lider)
		assert.Equal(t, soloDelClan(simulation.JobClanLeader, clanID, 1), h.persister.clavesDe(simulation.JobClanLeader))
	})
}

// invitacionesQueVe son los invitados del último clan.update que recibió p.
func invitacionesQueVe(h *harness, p uuid.UUID) []string {
	h.t.Helper()
	ups := h.clanUpdatesPara(p)
	require.NotEmpty(h.t, ups)
	v := ups[len(ups)-1].Clan
	require.NotNil(h.t, v)
	require.NotNil(h.t, v.SentInvitations, "la lista va vacía, no null")
	out := []string{}
	for _, inv := range v.SentInvitations {
		out = append(out, inv.PlayerID)
	}
	return out
}

// §4.2: las invitaciones enviadas sólo las ven el líder y los oficiales. Una
// invitación de un oficial no le llega a un raso; cuando el clan entero recibe
// su vista (alguien entra), el líder y el oficial ven la que sigue viva y los
// rasos una lista vacía; un oficial al que le quitan el rango deja de verlas.
func TestLasInvitacionesEnviadasSegunElRango(t *testing.T) {
	h, _ := conClanes(t, nil)
	vs := conVecinos(h, 4)
	oficial, raso, dani, eva := vs[0], vs[1], vs[2], vs[3]
	clanID := h.clanConMiembros(oficial, raso)
	h.nombrar(h.playerID, oficial, true)

	h.rec.reset()
	h.invitarAlClan(oficial, dani)
	h.invitarAlClan(oficial, eva)
	require.Empty(t, h.codigos())
	assert.Empty(t, h.clanUpdatesPara(raso), "a un raso no le llega una invitación enviada")
	assert.ElementsMatch(t, []string{dani.String(), eva.String()}, invitacionesQueVe(h, oficial))

	h.rec.reset()
	h.aceptarClan(eva, clanID)
	require.Empty(t, h.codigos())
	assert.Equal(t, []string{dani.String()}, invitacionesQueVe(h, h.playerID))
	assert.Equal(t, []string{dani.String()}, invitacionesQueVe(h, oficial))
	assert.Empty(t, invitacionesQueVe(h, raso), "un raso no ve las enviadas")
	assert.Empty(t, invitacionesQueVe(h, eva), "ni quien acaba de entrar")

	h.rec.reset()
	h.nombrar(h.playerID, oficial, false)
	require.Empty(t, h.codigos())
	assert.Empty(t, invitacionesQueVe(h, oficial), "un ex oficial deja de verlas")
	assert.Equal(t, []string{dani.String()}, invitacionesQueVe(h, h.playerID))
}

// RN-CLANGOV-005: el barrido de la ausencia va una vez por minuto. Tras una
// sucesión, el nuevo líder también ausente no la pierde en los ticks
// siguientes del mismo minuto (ni se encola otro cambio); en el barrido del
// minuto siguiente, sí.
func TestElBarridoDeLaAusenciaUnaVezPorMinuto(t *testing.T) {
	h, fc := conClanes(t, func(d *simulation.Deps) { d.ClanLeaderInactive = time.Hour })
	vs := conVecinos(h, 2)
	heredero, raso := vs[0], vs[1]
	clanID := h.clanConMiembros(heredero, raso)
	h.nombrar(h.playerID, heredero, true)
	t0 := h.clk.Now()
	ausente(h, h.playerID, t0)

	h.clk.Advance(time.Hour)
	h.loop.Step(h.clk.NowMs())
	lider, _ := h.state.ClanLeader(clanID)
	require.Equal(t, heredero, lider)
	ausente(h, heredero, t0) // el heredero también lleva una hora fuera

	for range 5 {
		h.tick()
	}
	lider, _ = h.state.ClanLeader(clanID)
	assert.Equal(t, heredero, lider, "dentro del mismo minuto, nada")
	assert.Len(t, fc.leaders, 1)
	assert.Len(t, h.persister.clavesDe(simulation.JobClanLeader), 1, "un solo cambio encolado")

	h.clk.SetMs(t0.Add(time.Hour + time.Minute).UnixMilli())
	h.loop.Step(h.clk.NowMs())
	lider, _ = h.state.ClanLeader(clanID)
	assert.Equal(t, raso, lider, "en el barrido siguiente")
	assert.Len(t, fc.leaders, 2)
}
