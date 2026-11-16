//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// SPEC-CLAN-ALLIANCES §9, integración: la migración 000023 (la tabla, sus CHECK,
// los índices por par y los dos triggers), cada orden con su comprobación de
// líder, estado, tope y bandos bajo los dos candados (INV-CLAN-006), los
// preavisos tolerantes por la mezcla de bandos y la expiración en la
// disolución, con los eventos y el registro de cada lado (ADR-042).

// clanOsos funda «Los Osos» [OSO] con lider y mete a los demás.
func clanOsos(t *testing.T, ctx context.Context, repo *postgres.ClanRepo, lider uuid.UUID, miembros ...uuid.UUID) int64 {
	t.Helper()
	id, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: lider, Name: "Los Osos", Tag: "OSO", At: clanEn, AtMs: clanEn.UnixMilli()})
	require.NoError(t, err)
	for i, p := range miembros {
		at := clanEn.Add(time.Duration(i+1) * time.Minute)
		require.NoError(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: p, InvitedBy: lider, At: at, ExpiresAt: at.Add(24 * time.Hour)}))
		require.NoError(t, repo.Join(ctx, postgres.ClanJoin{ClanID: id, PlayerID: p, MaxMembers: 20, At: at}))
	}
	return id
}

// enElBando fija la facción de un jugador neutral.
func enElBando(t *testing.T, ctx context.Context, store *postgres.Store, p uuid.UUID, code string) {
	t.Helper()
	_, err := store.Pool().Exec(ctx, `UPDATE players SET faction_id = (SELECT id FROM factions WHERE code = $2) WHERE id = $1`, p, code)
	require.NoError(t, err)
}

// proponer es la oferta de alianza de by (líder de desde) al clan hacia.
func proponer(desde, hacia int64, by uuid.UUID, rid string, at time.Time) postgres.AllianceWrite {
	a, b := clan.CanonicalClans(desde, hacia)
	return postgres.AllianceWrite{
		Insert: true, From: diplomacy.StatusProposed, Event: eventlog.EventClanAllianceProposed, By: by, ByClan: desde, MaxAllies: 2,
		Alliance: clan.Alliance{ClanA: a, ClanB: b, ProposedByClan: desde, ProposedBy: by, Status: diplomacy.StatusProposed,
			RequestID: rid, CreatedAtMs: at.UnixMilli()},
		Tick: 1, AtMs: at.UnixMilli(),
	}
}

// transicion es una orden de by (líder de suClan) que deja la alianza en next.
func transicion(al clan.Alliance, next diplomacy.Status, event string, by uuid.UUID, suClan int64, at time.Time) postgres.AllianceWrite {
	from := al.Status
	after := al
	if next == al.Status {
		after.BreakEffectiveAtMs, after.BreakReason = at.Add(15*time.Minute).UnixMilli(), clan.BreakReasonCancel
	} else {
		after, _ = al.TransitionTo(next, at.UnixMilli())
	}
	return postgres.AllianceWrite{Alliance: after, From: from, Event: event, By: by, ByClan: suClan, MaxAllies: 2, Tick: 2, AtMs: at.UnixMilli()}
}

// kindsDe son los tipos del registro de quien, con su sigla.
func kindsDe(t *testing.T, ctx context.Context, store *postgres.Store, quien uuid.UUID) map[string]string {
	t.Helper()
	page, _, err := registro(store, 50).Page(ctx, quien, 0)
	require.NoError(t, err)
	out := map[string]string{}
	for _, e := range page.Entries {
		out[e.Kind] = e.ClanTag
	}
	return out
}

// RN-CLANALLY-001, RN-CLANALLY-002, RN-CLANALLY-009 e INV-CLAN-006: proponer,
// aceptar, romper y el fin del preaviso en la base, idempotentes, con quién
// puede cada cosa, el tope y los bandos comprobados en la transacción; los
// eventos y el registro de cada lado.
func TestLaAlianzaEnLaBase(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 6)
	lobLider, lobMiembro, osoLider, osoMiembro, ciervo, fuera := ps[0], ps[1], ps[2], ps[3], ps[4], ps[5]
	lob := clanDeCuatro(t, ctx, repo, ps[:2])
	oso := clanOsos(t, ctx, repo, osoLider, osoMiembro)
	at := clanEn.Add(time.Hour)

	// Quién propone, y una oferta por par.
	_, err := repo.ApplyAlliance(ctx, proponer(lob, oso, lobMiembro, "r0", at))
	rechazoDeClan(t, err, "sólo el líder firma las alianzas del clan")
	w := proponer(lob, oso, lobLider, "r1", at)
	id, err := repo.ApplyAlliance(ctx, w)
	require.NoError(t, err)
	otra, err := repo.ApplyAlliance(ctx, w)
	require.NoError(t, err, "un reintento no escribe dos veces")
	assert.Equal(t, id, otra)
	_, err = repo.ApplyAlliance(ctx, proponer(oso, lob, osoLider, "r2", at))
	rechazoDeClan(t, err, "ya hay una alianza o una oferta con ese clan")
	assert.Equal(t, 1, contarEventos(t, ctx, store, eventlog.EventClanAllianceProposed))

	// Responde el clan que no propuso.
	al := w.Alliance
	al.ID = id
	_, err = repo.ApplyAlliance(ctx, transicion(al, diplomacy.StatusActive, eventlog.EventClanAllianceAccepted, lobLider, lob, at.Add(time.Minute)))
	rechazoDeClan(t, err, "ese clan no puede hacer eso con la oferta")
	acepta := transicion(al, diplomacy.StatusActive, eventlog.EventClanAllianceAccepted, osoLider, oso, at.Add(time.Minute))
	_, err = repo.ApplyAlliance(ctx, acepta)
	require.NoError(t, err)
	_, err = repo.ApplyAlliance(ctx, acepta)
	require.NoError(t, err, "un reintento no escribe dos veces")
	assert.Equal(t, 1, contarEventos(t, ctx, store, eventlog.EventClanAllianceAccepted))
	activa := acepta.Alliance

	// El tope y los bandos, en la transacción.
	cie, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: ciervo, Name: "Los Ciervos", Tag: "CIE", At: clanEn, AtMs: clanEn.UnixMilli()})
	require.NoError(t, err)
	tope := proponer(lob, cie, lobLider, "r3", at)
	tope.MaxAllies = 1
	_, err = repo.ApplyAlliance(ctx, tope)
	rechazoDeClan(t, err, "un clan ya tiene el máximo de alianzas")
	enElBando(t, ctx, store, lobMiembro, "ORDER")
	enElBando(t, ctx, store, ciervo, "CHAOS")
	_, err = repo.ApplyAlliance(ctx, proponer(lob, cie, lobLider, "r4", at))
	rechazoDeClan(t, err, "una alianza no junta a la Orden y al Caos")

	// La ruptura: una fila por miembro de los dos clanes, menos quien rompe.
	rompe := transicion(activa, diplomacy.StatusActive, eventlog.EventClanAllianceBreakAnnounced, osoLider, oso, at.Add(2*time.Minute))
	_, err = repo.ApplyAlliance(ctx, rompe)
	require.NoError(t, err)
	assert.Equal(t, 3, contarEventos(t, ctx, store, eventlog.EventClanAllianceBreakAnnounced))
	_, err = repo.ApplyAlliance(ctx, transicion(activa, diplomacy.StatusActive, eventlog.EventClanAllianceBreakAnnounced, lobLider, lob, at.Add(3*time.Minute)))
	rechazoDeClan(t, err, "esa alianza cambió")

	// El fin del preaviso, tolerante: un reintento no escribe nada.
	enPreaviso := rompe.Alliance
	rota, err := enPreaviso.TransitionTo(diplomacy.StatusBroken, enPreaviso.BreakEffectiveAtMs)
	require.NoError(t, err)
	fin := postgres.AllianceWrite{Alliance: rota, From: diplomacy.StatusActive, Event: eventlog.EventClanAllianceBroken, Tolerant: true, Tick: 3, AtMs: rota.ClosedAtMs}
	for range 2 {
		_, err = repo.ApplyAlliance(ctx, fin)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, contarEventos(t, ctx, store, eventlog.EventClanAllianceBroken))
	var estado string
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT status FROM clan_alliances WHERE id = $1`, id).Scan(&estado))
	assert.Equal(t, "BROKEN", estado)

	// El registro de cada lado, con la sigla del otro clan.
	assert.Equal(t, map[string]string{"CLAN_ALLIANCE_ACCEPTED": "OSO", "CLAN_ALLIANCE_BREAK_ANNOUNCED": "OSO"}, kindsDe(t, ctx, store, lobLider))
	assert.Equal(t, map[string]string{"CLAN_ALLIANCE_BREAK_ANNOUNCED": "OSO"}, kindsDe(t, ctx, store, lobMiembro))
	assert.Equal(t, map[string]string{"CLAN_ALLIANCE_OFFERED": "LOB"}, kindsDe(t, ctx, store, osoLider))
	assert.Equal(t, map[string]string{"CLAN_ALLIANCE_BREAK_ANNOUNCED": "LOB"}, kindsDe(t, ctx, store, osoMiembro))
	assert.Empty(t, kindsDe(t, ctx, store, fuera), "a un tercero, nada")
}

// La migración 000023: el par canónico, el proponente, el estado, una fila
// abierta de cada estado por par, y los dos triggers (un clan disuelto, la
// Orden con el Caos).
func TestLasRestriccionesDeLasAlianzas(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 4)
	lob := clanDeCuatro(t, ctx, repo, ps[:1])
	oso := clanOsos(t, ctx, repo, ps[1])
	cie, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: ps[2], Name: "Los Ciervos", Tag: "CIE", At: clanEn, AtMs: clanEn.UnixMilli()})
	require.NoError(t, err)
	a, b := clan.CanonicalClans(lob, oso)
	insertar := func(x, y, por int64, status, rid string) error {
		var accepted, closed *time.Time
		if status == "ACTIVE" {
			accepted = &clanEn
		}
		if status != "ACTIVE" && status != "PROPOSED" {
			closed = &clanEn
		}
		_, err := store.Pool().Exec(ctx,
			`INSERT INTO clan_alliances (clan_a, clan_b, proposed_by_clan, proposed_by_player, status, request_id, created_at, accepted_at, closed_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, x, y, por, ps[0], status, rid, clanEn, accepted, closed)
		return err
	}
	assert.Equal(t, "clan_alliances_canonical_pair", constraintDe(t, insertar(b, a, a, "PROPOSED", "c1")))
	assert.Equal(t, "clan_alliances_proposer", constraintDe(t, insertar(a, b, cie, "PROPOSED", "c2")))
	assert.Equal(t, "clan_alliances_status_valid", constraintDe(t, insertar(a, b, a, "LIVE", "c3")))
	require.NoError(t, insertar(a, b, a, "ACTIVE", "c4"))
	assert.Equal(t, "clan_alliances_one_active", constraintDe(t, insertar(a, b, a, "ACTIVE", "c5")))
	require.NoError(t, insertar(a, b, a, "PROPOSED", "c6"))
	assert.Equal(t, "clan_alliances_one_proposed", constraintDe(t, insertar(a, b, a, "PROPOSED", "c7")))

	// INV-CLAN-006: el trigger de bandos y el del clan disuelto.
	enElBando(t, ctx, store, ps[0], "ORDER")
	enElBando(t, ctx, store, ps[2], "CHAOS")
	x, y := clan.CanonicalClans(lob, cie)
	err = insertar(x, y, x, "PROPOSED", "c8")
	assert.True(t, postgres.IsCheckViolation(err), "la Orden con el Caos: %v", err)
	_, _, err = repo.Leave(ctx, postgres.ClanLeave{ClanID: cie, PlayerID: ps[2], Reason: clan.ReasonLeft, At: clanEn.Add(time.Hour), Dissolve: true})
	require.NoError(t, err)
	o, z := clan.CanonicalClans(oso, cie)
	err = insertar(o, z, o, "PROPOSED", "c9")
	assert.True(t, postgres.IsCheckViolation(err), "un clan disuelto: %v", err)
}

// ADR-042 §4 y §5: una entrada en un clan que mezcla la Orden y el Caos con un
// aliado abre el preaviso en su transacción; la entrada en un bando, también,
// de forma tolerante; y la disolución de un clan expira sus alianzas. La carga y
// la vista pública.
func TestLaMezclaYLaDisolucionEnLaBase(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	factions := postgres.NewFactionRepo(store)
	ps := conversadoresDeClan(t, store, ctx, 5)
	lobLider, osoLider, osoMiembro, nuevo, ciervo := ps[0], ps[1], ps[2], ps[3], ps[4]
	lob := clanDeCuatro(t, ctx, repo, ps[:1])
	oso := clanOsos(t, ctx, repo, osoLider, osoMiembro)
	cie, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: ciervo, Name: "Los Ciervos", Tag: "CIE", At: clanEn, AtMs: clanEn.UnixMilli()})
	require.NoError(t, err)
	at := clanEn.Add(time.Hour)
	aliar := func(desde, hacia int64, by, acepta uuid.UUID, rid string) clan.Alliance {
		w := proponer(desde, hacia, by, rid, at)
		id, err := repo.ApplyAlliance(ctx, w)
		require.NoError(t, err)
		al := w.Alliance
		al.ID = id
		ok := transicion(al, diplomacy.StatusActive, eventlog.EventClanAllianceAccepted, acepta, hacia, at)
		_, err = repo.ApplyAlliance(ctx, ok)
		require.NoError(t, err)
		return ok.Alliance
	}
	conOsos := aliar(lob, oso, lobLider, osoLider, "m1")
	conCiervos := aliar(lob, cie, lobLider, ciervo, "m2")
	enElBando(t, ctx, store, osoMiembro, "ORDER")
	enElBando(t, ctx, store, nuevo, "CHAOS")

	// La entrada de un jugador del Caos en [LOB] mezcla con [OSO].
	entrada := clanEn.Add(2 * time.Hour)
	require.NoError(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: lob, PlayerID: nuevo, InvitedBy: lobLider, At: entrada, ExpiresAt: entrada.Add(time.Hour)}))
	join := postgres.ClanJoin{ClanID: lob, PlayerID: nuevo, MaxMembers: 20, BreakNotice: 15 * time.Minute, At: entrada, Tick: 5, AtMs: entrada.UnixMilli()}
	breaks, err := repo.JoinBreaking(ctx, join)
	require.NoError(t, err)
	assert.Equal(t, []postgres.AllianceBreak{{AllianceID: conOsos.ID, BreakEffectiveAt: entrada.Add(15 * time.Minute)}}, breaks,
		"sólo la de [OSO]; la de [CIE] no mezcla")
	again, err := repo.JoinBreaking(ctx, join)
	require.NoError(t, err)
	assert.Empty(t, again, "un reintento no abre nada")
	var motivo string
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT break_reason FROM clan_alliances WHERE id = $1`, conOsos.ID).Scan(&motivo))
	assert.Equal(t, clan.BreakReasonFactions, motivo)
	assert.Equal(t, 3, contarEventos(t, ctx, store, eventlog.EventClanAllianceBreakAnnounced), "a todos menos a quien entró")

	// La entrada en un bando abre, tolerante, el preaviso que la RAM decidió.
	require.NoError(t, factions.RequestJoin(ctx, postgres.FactionJoinOrder{PlayerID: ciervo, RequestID: "f1", Faction: "ORDER", At: entrada}))
	cieAfter := conCiervos
	cieAfter.BreakEffectiveAtMs, cieAfter.BreakReason = entrada.Add(15*time.Minute).UnixMilli(), clan.BreakReasonFactions
	brk := postgres.AllianceWrite{Alliance: cieAfter, From: diplomacy.StatusActive, Event: eventlog.EventClanAllianceBreakAnnounced,
		By: ciervo, Tolerant: true, Tick: 6, AtMs: entrada.UnixMilli()}
	_, err = factions.CompleteJoin(ctx, postgres.FactionJoinCompletion{PlayerID: ciervo, Faction: "ORDER", At: entrada,
		AllianceBreaks: []postgres.AllianceWrite{brk, brk}, Tick: 6, AtMs: entrada.UnixMilli()})
	require.NoError(t, err)
	assert.Equal(t, 5, contarEventos(t, ctx, store, eventlog.EventClanAllianceBreakAnnounced), "dos filas más: el líder y el nuevo de [LOB]")

	// La carga y la vista pública.
	abiertas, cerradas, rechazadas, err := repo.ListAlliances(ctx, clanEn, clanEn)
	require.NoError(t, err)
	require.Len(t, abiertas, 2)
	assert.Empty(t, cerradas)
	assert.Empty(t, rechazadas)
	assert.Equal(t, entrada.Add(15*time.Minute).UnixMilli(), abiertas[0].BreakEffectiveAtMs)
	info, err := repo.Lookup(ctx, lob)
	require.NoError(t, err)
	require.Len(t, info.Alliances, 2)
	assert.Equal(t, "OSO", info.Alliances[0].Tag)
	require.NotNil(t, info.Alliances[0].BreaksAtMs)

	// La disolución de [CIE] expira su alianza en la misma transacción.
	disuelve := clanEn.Add(3 * time.Hour)
	_, _, err = repo.Leave(ctx, postgres.ClanLeave{ClanID: cie, PlayerID: ciervo, Reason: clan.ReasonLeft, At: disuelve, Dissolve: true})
	require.NoError(t, err)
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT status FROM clan_alliances WHERE id = $1`, conCiervos.ID).Scan(&motivo))
	assert.Equal(t, "EXPIRED", motivo)
	assert.Equal(t, 1, contarEventos(t, ctx, store, eventlog.EventClanAllianceExpired))
	abiertas, cerradas, _, err = repo.ListAlliances(ctx, clanEn, clanEn)
	require.NoError(t, err)
	assert.Len(t, abiertas, 1)
	require.Len(t, cerradas, 1, "la que estuvo activa vuelve para la paz residual")
	assert.Equal(t, disuelve.UnixMilli(), cerradas[0].ClosedAtMs)
}
