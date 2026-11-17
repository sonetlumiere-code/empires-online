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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Tests de los clanes (SPEC-CLANS §9): fundar, invitar y entrar en dos fases, el
// tope, la regla de bandos, la paz del clan y la residual, el liderazgo, la
// disolución, la expulsión con su aviso, el reencolado de una salida y la
// carga. Mundo de newHarness: el jugador del arnés con su ciudad 1 en (10,10) y
// los vecinos de conVecinos en (20,40), (28,40)…

// fakeClans hace de base: anota cada escritura y puede rechazarla.
type fakeClans struct {
	mu        sync.Mutex
	nextID    int64
	creates   []simulation.ClanCreateOrder
	invites   []simulation.ClanInviteOrder
	joins     []simulation.ClanJoinOrder
	leaves    []simulation.ClanLeave
	createErr error
	joinErr   error
	leaveErr  error
	// released son los controles que devuelve una disolución, como la base al
	// liberar los territorios del clan (ADR-041 §4).
	released []territory.Control
	// ranks y leaders son los nombramientos y los cambios de líder escritos
	// (SPEC-CLAN-GOVERNANCE); rankErr y leaderErr, sus fallos.
	ranks     []simulation.ClanRankOrder
	leaders   []simulation.ClanLeaderOrder
	rankErr   error
	leaderErr error
	// alliances son las escrituras de alianzas (SPEC-CLAN-ALLIANCES), con el
	// id que se da a cada oferta nueva; allianceErr, su fallo. joinBreaks es
	// lo que devuelve la entrada siguiente como preavisos abiertos por la base.
	alliances      []simulation.AllianceWrite
	nextAllianceID int64
	allianceErr    error
	joinBreaks     []simulation.AllianceBreak
	// wars son las escrituras de guerras (SPEC-CLAN-WARS), con el id que se da
	// a cada declaración nueva; warErr, su fallo.
	wars      []simulation.WarWrite
	nextWarID int64
	warErr    error
}

func (f *fakeClans) ApplyWar(_ context.Context, w simulation.WarWrite) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.warErr != nil {
		return 0, f.warErr
	}
	f.wars = append(f.wars, w)
	if w.Insert {
		f.nextWarID++
		return f.nextWarID, nil
	}
	return w.War.ID, nil
}

func (f *fakeClans) ApplyAlliance(_ context.Context, w simulation.AllianceWrite) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.allianceErr != nil {
		return 0, f.allianceErr
	}
	f.alliances = append(f.alliances, w)
	if w.Insert {
		f.nextAllianceID++
		return f.nextAllianceID, nil
	}
	return w.Alliance.ID, nil
}

func (f *fakeClans) SetRank(_ context.Context, o simulation.ClanRankOrder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rankErr != nil {
		return f.rankErr
	}
	f.ranks = append(f.ranks, o)
	return nil
}

func (f *fakeClans) ChangeLeader(_ context.Context, o simulation.ClanLeaderOrder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leaderErr != nil {
		return f.leaderErr
	}
	f.leaders = append(f.leaders, o)
	return nil
}

func (f *fakeClans) Create(_ context.Context, o simulation.ClanCreateOrder) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return 0, f.createErr
	}
	f.nextID++
	f.creates = append(f.creates, o)
	return f.nextID, nil
}

func (f *fakeClans) Invite(_ context.Context, o simulation.ClanInviteOrder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invites = append(f.invites, o)
	return nil
}

func (f *fakeClans) Join(_ context.Context, o simulation.ClanJoinOrder) ([]simulation.AllianceBreak, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.joinErr != nil {
		return nil, f.joinErr
	}
	f.joins = append(f.joins, o)
	breaks := f.joinBreaks
	f.joinBreaks = nil
	return breaks, nil
}

func (f *fakeClans) Leave(_ context.Context, l simulation.ClanLeave) ([]territory.Control, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leaveErr != nil {
		return nil, f.leaveErr
	}
	f.leaves = append(f.leaves, l)
	if !l.Dissolve {
		return nil, nil
	}
	return f.released, nil
}

func (f *fakeClans) lastLeave(t *testing.T) simulation.ClanLeave {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.leaves)
	return f.leaves[len(f.leaves)-1]
}

// conClanes es newHarness con el almacén de clanes; ajustar fija el tope o la
// paz residual.
func conClanes(t *testing.T, ajustar func(*simulation.Deps)) (*harness, *fakeClans) {
	t.Helper()
	fc := &fakeClans{}
	h := newHarnessConDeps(t, nil, func(d *simulation.Deps) {
		d.Clans = fc
		if ajustar != nil {
			ajustar(d)
		}
	})
	return h, fc
}

func (h *harness) fundarClan(p uuid.UUID, name, tag string) string {
	h.t.Helper()
	rid := uuid.NewString()
	h.send(simulation.CreateClan{PlayerID: p, RequestID: rid, Name: name, Tag: tag})
	h.tick()
	return rid
}

func (h *harness) invitarAlClan(lider, invitado uuid.UUID) {
	h.t.Helper()
	h.send(simulation.InviteToClan{PlayerID: lider, RequestID: uuid.NewString(), TargetID: invitado, Invited: true})
	h.tick()
}

func (h *harness) aceptarClan(p uuid.UUID, clanID int64) {
	h.t.Helper()
	h.send(simulation.RespondClan{PlayerID: p, RequestID: uuid.NewString(), ClanID: clanID, Accept: true})
	h.tick()
}

// clanConMiembros funda un clan del jugador del arnés y mete en él a los
// jugadores dados; devuelve su id.
func (h *harness) clanConMiembros(miembros ...uuid.UUID) int64 {
	h.t.Helper()
	h.fundarClan(h.playerID, "Los Lobos", "LOB")
	id, ok := h.state.ClanOf(h.playerID)
	require.True(h.t, ok)
	for _, m := range miembros {
		h.invitarAlClan(h.playerID, m)
		h.aceptarClan(m, id)
		require.Empty(h.t, h.codigos())
	}
	return id
}

// clanUpdatesPara son los clan.update que recibió un jugador.
func (h *harness) clanUpdatesPara(p uuid.UUID) []protocol.ClanUpdatePayload {
	var out []protocol.ClanUpdatePayload
	for _, m := range h.rec.byType(protocol.TypeClanUpdate) {
		if m.Kind == "player" && m.PlayerID == p {
			out = append(out, m.Payload.(protocol.ClanUpdatePayload))
		}
	}
	return out
}

func (h *harness) ultimoMotivo() string {
	h.t.Helper()
	errs := h.rec.byType(protocol.TypeSystemError)
	require.NotEmpty(h.t, errs)
	return errs[len(errs)-1].Payload.(protocol.SystemErrorPayload).Message
}

// RN-CLAN-001 y ADR-040 §2: fundar se reserva, se confirma al volver por el
// buzón, y entonces el fundador es líder, su ciudad lleva la sigla y recibe su
// clan con el requestId.
func TestFundarUnClanVaEnDosFases(t *testing.T) {
	h, fc := conClanes(t, nil)
	rid := uuid.NewString()
	h.send(simulation.CreateClan{PlayerID: h.playerID, RequestID: rid, Name: "Los Lobos", Tag: "LOB"})
	require.Empty(t, h.codigos())
	_, ok := h.state.ClanOf(h.playerID)
	assert.False(t, ok, "reservado, todavía no existe")
	assert.Nil(t, h.ciudad().Clan)

	h.tick()
	id, ok := h.state.ClanOf(h.playerID)
	require.True(t, ok)
	lider, _ := h.state.ClanLeader(id)
	assert.Equal(t, h.playerID, lider)
	require.NotNil(t, h.ciudad().Clan)
	assert.Equal(t, city.ClanTag{ID: id, Tag: "LOB"}, *h.ciudad().Clan)
	require.Len(t, fc.creates, 1)
	assert.Equal(t, "Los Lobos", fc.creates[0].Name)

	var confirmado bool
	for _, m := range h.rec.byType(protocol.TypeClanUpdate) {
		p := m.Payload.(protocol.ClanUpdatePayload)
		if m.PlayerID == h.playerID && m.RequestID == rid && p.Clan != nil {
			confirmado = true
			assert.Equal(t, "LOB", p.Clan.Tag)
			require.Len(t, p.Clan.Members, 1)
			assert.Equal(t, clan.RoleLeader, p.Clan.Members[0].Role)
		}
	}
	assert.True(t, confirmado)
	var sigla bool
	for _, u := range h.actualizacionesDeCiudad() {
		if u.Clan != nil && *u.Clan != nil && (*u.Clan).Tag == "LOB" {
			sigla = true
		}
	}
	assert.True(t, sigla, "la ciudad se difunde con la sigla")

	// Un segundo clan no: ya está en uno.
	h.rec.reset()
	h.send(simulation.CreateClan{PlayerID: h.playerID, RequestID: uuid.NewString(), Name: "Los Osos", Tag: "OSO"})
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, "ya estás en un clan", h.ultimoMotivo())
}

// RN-CLAN-002: el formato es INVALID_MESSAGE; un nombre o una sigla ocupados,
// en la RAM o en la base, INVALID_TARGET, y el rechazo de la base deshace la
// reserva.
func TestElNombreYLaSiglaDelClan(t *testing.T) {
	h, fc := conClanes(t, nil)
	otro := conVecinos(h, 1)[0]

	h.send(simulation.CreateClan{PlayerID: h.playerID, RequestID: uuid.NewString(), Name: " Lobos", Tag: "LOB"})
	assert.Equal(t, []string{protocol.CodeInvalidMessage}, h.codigos())
	h.rec.reset()
	h.send(simulation.CreateClan{PlayerID: h.playerID, RequestID: uuid.NewString(), Name: "Los Lobos", Tag: "lob"})
	assert.Equal(t, []string{protocol.CodeInvalidMessage}, h.codigos())

	h.rec.reset()
	h.fundarClan(otro, "Los Lobos", "LOB")
	require.Empty(t, h.codigos())
	h.send(simulation.CreateClan{PlayerID: h.playerID, RequestID: uuid.NewString(), Name: "LOS LOBOS", Tag: "XYZ"})
	assert.Equal(t, "ese nombre ya existe", h.ultimoMotivo(), "sin distinguir mayúsculas")
	h.send(simulation.CreateClan{PlayerID: h.playerID, RequestID: uuid.NewString(), Name: "Los Osos", Tag: "LOB"})
	assert.Equal(t, "esa sigla ya existe", h.ultimoMotivo())

	// La base lo rechaza (otra carrera): la reserva se deshace y se puede volver
	// a intentar.
	fc.createErr = simulation.ClanRejection{Reason: "ese nombre ya existe"}
	h.rec.reset()
	h.fundarClan(h.playerID, "Los Osos", "OSO")
	assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, "ese nombre ya existe", h.ultimoMotivo())
	fc.createErr = nil
	h.rec.reset()
	h.fundarClan(h.playerID, "Los Osos", "OSO")
	assert.Empty(t, h.codigos())
	_, ok := h.state.ClanOf(h.playerID)
	assert.True(t, ok)
}

// RN-CLAN-003 y RN-CLAN-007: sólo el líder invita; la invitación llega al
// invitado, al aceptarla entra y todos ven el clan con él.
func TestInvitarYAceptar(t *testing.T) {
	h, fc := conClanes(t, nil)
	vecinos := conVecinos(h, 2)
	b, c := vecinos[0], vecinos[1]
	id := h.clanConMiembros()

	h.rec.reset()
	h.invitarAlClan(h.playerID, b)
	require.Empty(t, h.codigos())
	recibidas := h.clanUpdatesPara(b)
	require.NotEmpty(t, recibidas)
	require.Len(t, recibidas[len(recibidas)-1].Invitations, 1)
	assert.Equal(t, id, recibidas[len(recibidas)-1].Invitations[0].ClanID)
	lider := h.clanUpdatesPara(h.playerID)
	require.Len(t, lider[len(lider)-1].Clan.SentInvitations, 1, "el líder ve su invitación")

	// Un miembro que no es el líder no invita.
	h.rec.reset()
	h.aceptarClan(b, id)
	require.Empty(t, h.codigos())
	assert.ElementsMatch(t, []uuid.UUID{h.playerID, b}, h.state.ClanMembers(id))
	require.Len(t, fc.joins, 1)
	assert.NotEmpty(t, h.clanUpdatesPara(h.playerID), "el líder ve al nuevo")
	h.send(simulation.InviteToClan{PlayerID: b, RequestID: uuid.NewString(), TargetID: c, Invited: true})
	assert.Equal(t, "sólo el líder o un oficial invita", h.ultimoMotivo())

	// Sin invitación no se entra; retirada, tampoco.
	h.send(simulation.RespondClan{PlayerID: c, RequestID: uuid.NewString(), ClanID: id, Accept: true})
	assert.Equal(t, "la invitación no existe o caducó", h.ultimoMotivo())
	h.invitarAlClan(h.playerID, c)
	h.send(simulation.InviteToClan{PlayerID: h.playerID, RequestID: uuid.NewString(), TargetID: c, Invited: false})
	h.send(simulation.RespondClan{PlayerID: c, RequestID: uuid.NewString(), ClanID: id, Accept: true})
	assert.Equal(t, "la invitación no existe o caducó", h.ultimoMotivo())
}

// RN-CLAN-003: la invitación caduca a las EO_CLAN_INVITE_HOURS.
func TestLaInvitacionCaduca(t *testing.T) {
	h, _ := conClanes(t, func(d *simulation.Deps) { d.ClanInviteTTL = time.Minute })
	b := conVecinos(h, 1)[0]
	id := h.clanConMiembros()
	h.invitarAlClan(h.playerID, b)
	h.advance(61 * time.Second)
	h.rec.reset()
	h.send(simulation.RespondClan{PlayerID: b, RequestID: uuid.NewString(), ClanID: id, Accept: true})
	assert.Equal(t, "la invitación no existe o caducó", h.ultimoMotivo())
}

// RN-CLAN-001 (Q5): el tope cuenta las invitaciones vivas al invitar.
func TestElTopeDelClanCuentaLasInvitaciones(t *testing.T) {
	h, _ := conClanes(t, func(d *simulation.Deps) { d.ClanMaxMembers = 2 })
	vecinos := conVecinos(h, 2)
	h.clanConMiembros()
	h.invitarAlClan(h.playerID, vecinos[0])
	require.Empty(t, h.codigos())
	h.send(simulation.InviteToClan{PlayerID: h.playerID, RequestID: uuid.NewString(), TargetID: vecinos[1], Invited: true})
	assert.Equal(t, "el clan está lleno", h.ultimoMotivo())
}

// RN-CLAN-006 e INV-CLAN-002: un clan no junta a la Orden y al Caos, por
// ningún camino, contando las entradas pendientes.
func TestUnClanNoJuntaALaOrdenYAlCaos(t *testing.T) {
	h, _ := conClanes(t, func(d *simulation.Deps) { d.Factions = &fakeFactions{} })
	vecinos := conVecinos(h, 3)
	orden, caos, neutral := vecinos[0], vecinos[1], vecinos[2]
	enBando(h, h.playerID, player.FactionOrder)
	enBando(h, caos, player.FactionChaos)
	id := h.clanConMiembros(orden, neutral)

	t.Run("invitar", func(t *testing.T) {
		h.rec.reset()
		h.send(simulation.InviteToClan{PlayerID: h.playerID, RequestID: uuid.NewString(), TargetID: caos, Invited: true})
		assert.Equal(t, "un clan no junta a la Orden y al Caos", h.ultimoMotivo())
	})
	t.Run("faction.join de un miembro", func(t *testing.T) {
		h.rec.reset()
		h.unirse(neutral, player.FactionChaos)
		assert.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
		assert.Equal(t, "tu clan tiene miembros de la Orden", h.ultimoMotivo())
		h.rec.reset()
		h.unirse(neutral, player.FactionOrder)
		assert.Empty(t, h.codigos(), "al mismo bando, sí")
	})
	t.Run("aceptar con una entrada pendiente", func(t *testing.T) {
		// Un neutral con una entrada pendiente en el Caos, invitado antes de
		// pedirla: al aceptar, la entrada cuenta.
		h2, _ := conClanes(t, nil)
		otro := conVecinos(h2, 1)[0]
		enBando(h2, h2.playerID, player.FactionOrder)
		id2 := h2.clanConMiembros()
		h2.invitarAlClan(h2.playerID, otro)
		vecina, _ := h2.state.City(2)
		vecina.FactionJoin = &city.FactionJoin{Faction: player.FactionChaos, AtMs: h2.clk.NowMs() + time.Hour.Milliseconds()}
		h2.rec.reset()
		h2.send(simulation.RespondClan{PlayerID: otro, RequestID: uuid.NewString(), ClanID: id2, Accept: true})
		assert.Equal(t, "un clan no junta a la Orden y al Caos", h2.ultimoMotivo())
	})
	_ = id
}

// RN-CLAN-004 e INV-CLAN-003: un enfrentamiento en curso termina en el tick en
// que se confirma la entrada, sin un golpe más, y después atacar a un
// compañero se rechaza con su motivo.
func TestDosDelMismoClanDejanDePelear(t *testing.T) {
	h, _ := conClanes(t, nil)
	enemigo, _ := conEnemigo(h)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	v := ponerUnidad(h, enemigoID, enemigo, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)

	h.atacar(g.ID, v.ID)
	require.Empty(t, h.codigos())
	require.True(t, h.tickHasta(30, func() bool { return v.HP < 40 }), "el primer golpe cae")
	h.clanConMiembros(enemigo)
	require.True(t, h.state.AtPeace(h.playerID, enemigo, h.clk.Now()))
	vida := v.HP
	h.advance(10 * time.Second)
	assert.Equal(t, vida, v.HP, "ningún golpe más")
	_, sigue := h.state.Engaged(g.ID)
	assert.False(t, sigue, "el enfrentamiento terminó")

	h.rec.reset()
	h.atacar(g.ID, v.ID)
	require.Equal(t, []string{protocol.CodeInvalidTarget}, h.codigos())
	assert.Equal(t, "sois del mismo clan", h.ultimoMotivo())
}

// RN-CLAN-005 e INV-CLAN-003: quien sale sigue en paz con sus antiguos
// compañeros durante la paz residual, y en el tick en que vence ya se le puede
// atacar; a quien entró después de su salida no le alcanza.
func TestLaPazResidualTrasSalir(t *testing.T) {
	h, fc := conClanes(t, func(d *simulation.Deps) { d.ClanPeaceGrace = time.Minute })
	vecinos := conVecinos(h, 2)
	b, c := vecinos[0], vecinos[1]
	id := h.clanConMiembros(b)
	g := ponerUnidad(h, guerreroID, h.playerID, h.cityID, unit.TypeWarrior, world.Tile{X: 19, Y: 20}, 60)
	v := ponerUnidad(h, enemigoID, b, 2, unit.TypeVillager, world.Tile{X: 20, Y: 20}, 40)

	rid := uuid.NewString()
	h.send(simulation.LeaveClan{PlayerID: b, RequestID: rid})
	require.Empty(t, h.codigos())
	_, sigue := h.state.ClanOf(b)
	assert.False(t, sigue, "la pertenencia termina en el acto")
	vecina, _ := h.state.City(2)
	assert.Nil(t, vecina.Clan, "y la sigla")
	l := fc.lastLeave(t)
	assert.Equal(t, clan.ReasonLeft, l.Reason)
	assert.False(t, l.Dissolve)
	assert.Equal(t, uuid.Nil, l.NewLeader, "el líder no cambia")

	salida := h.clk.Now()
	assert.True(t, h.state.ClanPeace(h.playerID, b, salida), "paz residual")
	h.rec.reset()
	h.atacar(g.ID, v.ID)
	assert.Equal(t, "sois del mismo clan", h.ultimoMotivo())

	// Entra c después de la salida: con b no hay paz residual.
	h.invitarAlClan(h.playerID, c)
	h.aceptarClan(c, id)
	assert.False(t, h.state.ClanPeace(c, b, h.clk.Now()))

	h.clk.AdvanceMs(salida.Add(time.Minute).UnixMilli() - h.clk.NowMs() - 1)
	assert.True(t, h.state.ClanPeace(h.playerID, b, h.clk.Now()), "hasta el último ms")
	h.clk.AdvanceMs(1)
	assert.False(t, h.state.ClanPeace(h.playerID, b, h.clk.Now()), "vence al cumplirse el plazo")
	h.rec.reset()
	h.atacar(g.ID, v.ID)
	assert.Empty(t, h.codigos(), "ya se le puede atacar")
}

// RN-CLAN-007: el liderazgo pasa al más antiguo y la salida del último disuelve
// el clan con sus invitaciones.
func TestElLiderazgoYLaDisolucion(t *testing.T) {
	h, fc := conClanes(t, nil)
	vecinos := conVecinos(h, 3)
	b, c, d := vecinos[0], vecinos[1], vecinos[2]
	id := h.clanConMiembros(b)
	h.advance(time.Second)
	h.invitarAlClan(h.playerID, c)
	h.aceptarClan(c, id)
	h.invitarAlClan(h.playerID, d)

	h.send(simulation.LeaveClan{PlayerID: h.playerID, RequestID: uuid.NewString()})
	lider, _ := h.state.ClanLeader(id)
	assert.Equal(t, b, lider, "el más antiguo")
	assert.Equal(t, b, fc.lastLeave(t).NewLeader)

	h.send(simulation.LeaveClan{PlayerID: c, RequestID: uuid.NewString()})
	h.rec.reset()
	h.send(simulation.LeaveClan{PlayerID: b, RequestID: uuid.NewString()})
	assert.True(t, fc.lastLeave(t).Dissolve)
	_, vivo := h.state.ClanLeader(id)
	assert.False(t, vivo, "disuelto")
	paraD := h.clanUpdatesPara(d)
	require.NotEmpty(t, paraD)
	assert.Empty(t, paraD[len(paraD)-1].Invitations, "su invitación se fue con el clan")

	// El nombre queda libre.
	h.rec.reset()
	h.fundarClan(d, "Los Lobos", "LOB")
	assert.Empty(t, h.codigos())
}

// Q10: la expulsión la decide sólo el líder y se avisa en vivo al expulsado.
func TestExpulsarAvisaAlExpulsado(t *testing.T) {
	h, fc := conClanes(t, nil)
	vecinos := conVecinos(h, 2)
	b, c := vecinos[0], vecinos[1]
	h.clanConMiembros(b, c)

	h.rec.reset()
	h.send(simulation.ExpelFromClan{PlayerID: b, RequestID: uuid.NewString(), TargetID: c})
	assert.Equal(t, "sólo el líder o un oficial expulsa", h.ultimoMotivo())
	h.send(simulation.ExpelFromClan{PlayerID: h.playerID, RequestID: uuid.NewString(), TargetID: h.playerID})
	assert.Equal(t, "para irte, sal del clan", h.ultimoMotivo())

	h.rec.reset()
	h.send(simulation.ExpelFromClan{PlayerID: h.playerID, RequestID: uuid.NewString(), TargetID: c})
	require.Empty(t, h.codigos())
	l := fc.lastLeave(t)
	assert.Equal(t, clan.ReasonExpelled, l.Reason)
	assert.Equal(t, h.playerID, l.By)
	avisos := h.avisosPara(c)
	require.Len(t, avisos, 1)
	assert.Equal(t, "CLAN_EXPELLED", avisos[0].Kind)
	assert.Equal(t, h.playerID.String(), avisos[0].OtherPlayerID)
	assert.Empty(t, h.avisosPara(h.playerID), "el líder no recibe aviso, sólo registro")
	paraC := h.clanUpdatesPara(c)
	require.NotEmpty(t, paraC)
	assert.Nil(t, paraC[len(paraC)-1].Clan)
}

// ADR-040 §2: el último no sale mientras hay una entrada en vuelo al clan.
func TestLaDisolucionEsperaALasEntradasEnVuelo(t *testing.T) {
	h, _ := conClanes(t, nil)
	b := conVecinos(h, 1)[0]
	id := h.clanConMiembros()
	h.invitarAlClan(h.playerID, b)
	// Los dos en el mismo tick: la entrada sigue en vuelo (su confirmación
	// vuelve por el buzón en el siguiente) cuando el líder pide salir.
	h.rec.reset()
	h.commands <- simulation.RespondClan{PlayerID: b, RequestID: uuid.NewString(), ClanID: id, Accept: true}
	h.commands <- simulation.LeaveClan{PlayerID: h.playerID, RequestID: uuid.NewString()}
	h.loop.Step(h.clk.NowMs())
	assert.Contains(t, h.ultimoMotivo(), "alguien está entrando en el clan")
	h.tick()
	assert.ElementsMatch(t, []uuid.UUID{h.playerID, b}, h.state.ClanMembers(id))
}

// ADR-022: una salida que no se escribe se reencola en el volcado siguiente.
func TestUnaSalidaQueNoSeEscribeSeReintenta(t *testing.T) {
	h, fc := conClanes(t, nil)
	b := conVecinos(h, 1)[0]
	h.clanConMiembros(b)
	fc.leaveErr = errors.New("la base no responde")
	h.persister.permanent = true
	h.send(simulation.LeaveClan{PlayerID: b, RequestID: uuid.NewString()})
	h.tick()
	fc.mu.Lock()
	assert.Empty(t, fc.leaves)
	fc.leaveErr = nil
	fc.mu.Unlock()
	h.persister.permanent = false
	h.sim.FlushDirty()
	l := fc.lastLeave(t)
	assert.Equal(t, b, l.PlayerID)
}

// SPEC-CLANS §6: el snapshot de la conexión lleva el clan y las invitaciones;
// el de un cambio de vista, no.
func TestElSnapshotDeLaConexionLlevaElClan(t *testing.T) {
	h, _ := conClanes(t, nil)
	b := conVecinos(h, 1)[0]
	id := h.clanConMiembros()
	h.invitarAlClan(h.playerID, b)

	pedir := func(p uuid.UUID, center *world.Tile) simulation.SnapshotResult {
		reply := make(chan simulation.SnapshotResult, 1)
		h.send(simulation.RequestSnapshot{PlayerID: p, Center: center, RadiusChunks: 1, Reply: reply})
		return <-reply
	}
	r := pedir(h.playerID, nil)
	require.NotNil(t, r.Clan)
	require.NotNil(t, r.Clan.Clan)
	assert.Equal(t, id, r.Clan.Clan.ID)
	r = pedir(b, nil)
	require.NotNil(t, r.Clan)
	assert.Nil(t, r.Clan.Clan)
	require.Len(t, r.Clan.Invitations, 1)
	assert.Nil(t, pedir(h.playerID, &world.Tile{X: 5, Y: 5}).Clan)
	for _, c := range pedir(h.playerID, nil).Payload.Cities {
		if c.ID == h.cityID {
			require.NotNil(t, c.Clan)
			assert.Equal(t, "LOB", c.Clan.Tag)
		}
	}
}

// Recuperación (§9): la pertenencia y la paz residual vuelven de la base; una
// salida cuya paz venció con el servidor parado no se carga.
func TestLosClanesVuelvenAlArrancar(t *testing.T) {
	h, _ := conClanes(t, func(d *simulation.Deps) { d.ClanPeaceGrace = time.Minute })
	vecinos := conVecinos(h, 2)
	b, c := vecinos[0], vecinos[1]
	now := h.clk.NowMs()
	h.state.SetClans(
		[]clan.Clan{{ID: 7, Name: "Los Lobos", Tag: "LOB", LeaderID: h.playerID}},
		[]clan.Stay{
			{ClanID: 7, PlayerID: h.playerID, JoinedAtMs: now - 10_000},
			{ClanID: 7, PlayerID: b, JoinedAtMs: now - 9_000, LeftAtMs: now - 1_000},
		},
		[]clan.Invite{{ClanID: 7, PlayerID: c, InvitedBy: h.playerID, ExpiresAtMs: now + 10*60_000}},
		time.Minute,
	)
	id, ok := h.state.ClanOf(h.playerID)
	require.True(t, ok)
	assert.Equal(t, int64(7), id)
	require.NotNil(t, h.ciudad().Clan)
	assert.True(t, h.state.ClanPeace(h.playerID, b, h.clk.Now()), "la paz residual vuelve")
	h.advance(time.Minute)
	assert.False(t, h.state.ClanPeace(h.playerID, b, h.clk.Now()))
	h.rec.reset()
	h.aceptarClan(c, 7)
	assert.Empty(t, h.codigos(), "la invitación viva vuelve")
}
