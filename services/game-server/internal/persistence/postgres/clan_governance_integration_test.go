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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// SPEC-CLAN-GOVERNANCE §9, integración: la migración 000022 (rank y sus dos
// triggers), el rango de quien ordena comprobado en cada transacción con el
// candado del clan (INV-CLAN-005), el traspaso y la herencia que dejan
// leader_player_id y los rangos coherentes, y ClanLeaderChanged en el registro
// de los dos lados.

// clanDeCuatro funda «Los Lobos» [LOB] con ps[0] de líder y mete a los demás.
func clanDeCuatro(t *testing.T, ctx context.Context, repo *postgres.ClanRepo, ps []uuid.UUID) int64 {
	t.Helper()
	id, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: ps[0], Name: "Los Lobos", Tag: "LOB", At: clanEn, AtMs: clanEn.UnixMilli()})
	require.NoError(t, err)
	for i, p := range ps[1:] {
		at := clanEn.Add(time.Duration(i+1) * time.Minute)
		require.NoError(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: p, InvitedBy: ps[0], At: at, ExpiresAt: at.Add(24 * time.Hour)}))
		require.NoError(t, repo.Join(ctx, postgres.ClanJoin{ClanID: id, PlayerID: p, MaxMembers: 20, At: at}))
	}
	return id
}

// rangosEnLaBase son los rangos de las estancias abiertas de un clan, por
// jugador, y su líder.
func rangosEnLaBase(t *testing.T, ctx context.Context, store *postgres.Store, clanID int64) (map[uuid.UUID]string, uuid.UUID) {
	t.Helper()
	rows, err := store.Pool().Query(ctx, `SELECT player_id, rank FROM clan_members WHERE clan_id = $1 AND left_at IS NULL`, clanID)
	require.NoError(t, err)
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var (
			p uuid.UUID
			r string
		)
		require.NoError(t, rows.Scan(&p, &r))
		out[p] = r
	}
	var lider uuid.UUID
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT leader_player_id FROM clans WHERE id = $1`, clanID).Scan(&lider))
	return out, lider
}

// §4.3 e INV-CLAN-005: nombrar y quitar un oficial lo hace sólo el líder, es
// idempotente y vuelve con la carga y la vista pública; un oficial invita y
// expulsa a un raso pero no a otro oficial; un ex oficial ya no invita.
func TestElRangoSeCompruebaEnLaBase(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 5)
	lider, oficial, otro, raso, fuera := ps[0], ps[1], ps[2], ps[3], ps[4]
	id := clanDeCuatro(t, ctx, repo, ps[:4])

	nombrar := func(by, p uuid.UUID, officer bool) error {
		return repo.SetRank(ctx, postgres.ClanRank{ClanID: id, PlayerID: p, By: by, Officer: officer, At: clanEn.Add(time.Hour)})
	}
	rechazoDeClan(t, nombrar(oficial, raso, true), "sólo el líder nombra oficiales")
	require.NoError(t, nombrar(lider, oficial, true))
	require.NoError(t, nombrar(lider, oficial, true), "un reintento no escribe dos veces")
	require.NoError(t, nombrar(lider, otro, true))
	rechazoDeClan(t, nombrar(oficial, raso, true), "sólo el líder nombra oficiales")
	rechazoDeClan(t, nombrar(lider, lider, true), "el líder no tiene otro rango")
	rechazoDeClan(t, nombrar(lider, fuera, true), "ese jugador ya no es de tu clan")
	assert.Equal(t, 2, contarEventos(t, ctx, store, "ClanRankChanged"))

	rangos, _ := rangosEnLaBase(t, ctx, store, id)
	assert.Equal(t, map[uuid.UUID]string{lider: "MEMBER", oficial: "OFFICER", otro: "OFFICER", raso: "MEMBER"}, rangos)
	estancias, err := repo.ListStays(ctx, clanEn)
	require.NoError(t, err)
	oficiales := map[uuid.UUID]bool{}
	for _, st := range estancias {
		oficiales[st.PlayerID] = st.Officer
	}
	assert.Equal(t, map[uuid.UUID]bool{lider: false, oficial: true, otro: true, raso: false}, oficiales, "la carga trae los rangos")
	info, err := repo.Lookup(ctx, id)
	require.NoError(t, err)
	roles := map[string]string{}
	for _, m := range info.Members {
		roles[m.PlayerID] = m.Role
	}
	assert.Equal(t, map[string]string{lider.String(): clan.RoleLeader, oficial.String(): clan.RoleOfficer,
		otro.String(): clan.RoleOfficer, raso.String(): clan.RoleMember}, roles, "la vista pública lleva el rango")

	// Un oficial invita; un raso, no.
	at := clanEn.Add(2 * time.Hour)
	require.NoError(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: fuera, InvitedBy: oficial, At: at, ExpiresAt: at.Add(time.Hour)}))
	rechazoDeClan(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: fuera, InvitedBy: raso, At: at, ExpiresAt: at.Add(time.Hour)}),
		"sólo el líder o un oficial invita")

	// Un oficial no expulsa a otro oficial: nada escrito.
	expulsar := func(by, p uuid.UUID, at time.Time) (bool, error) {
		ok, _, err := repo.Leave(ctx, postgres.ClanLeave{ClanID: id, PlayerID: p, Reason: clan.ReasonExpelled, By: by, At: at})
		return ok, err
	}
	_, err = expulsar(oficial, otro, at)
	rechazoDeClan(t, err, "quien expulsa no tiene el rango para hacerlo")
	rangos, _ = rangosEnLaBase(t, ctx, store, id)
	assert.Contains(t, rangos, otro, "sigue en el clan")

	// Un ex oficial ya no invita ni expulsa.
	require.NoError(t, nombrar(lider, oficial, false))
	rechazoDeClan(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: fuera, InvitedBy: oficial, At: at.Add(time.Minute), ExpiresAt: at.Add(time.Hour)}),
		"sólo el líder o un oficial invita")
	_, err = expulsar(oficial, raso, at)
	rechazoDeClan(t, err, "quien expulsa no tiene el rango para hacerlo")

	// El otro oficial expulsa al raso; un reintento no se vuelve a mirar.
	ok, err := expulsar(otro, raso, at)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = expulsar(otro, raso, at)
	require.NoError(t, err)
	assert.False(t, ok, "ya escrita: un reintento")

	// Una estancia cerrada no cambia de rango (el trigger).
	_, err = store.Pool().Exec(ctx, `UPDATE clan_members SET rank = 'OFFICER' WHERE clan_id = $1 AND player_id = $2`, id, raso)
	assert.True(t, postgres.IsCheckViolation(err), "una estancia cerrada: %v", err)
}

// contarEventos cuenta las filas de un tipo de world_events.
func contarEventos(t *testing.T, ctx context.Context, store *postgres.Store, tipo string) int {
	t.Helper()
	var n int
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT count(*) FROM world_events WHERE event_type = $1`, tipo).Scan(&n))
	return n
}

// §4.3, §4.4, Q4, Q7 y Q8: el traspaso y la sucesión dejan al nuevo líder sin
// rango de oficial y al anterior de oficial en una transacción; si quien cede
// ya no es el líder, nada; un reintento no repite nada. El evento lleva al
// nuevo de player_id y al anterior de counterpart, y el registro lo cuenta a
// los dos con la sigla. La herencia al salir el líder quita el rango al que
// hereda. Los triggers no dejan a un líder con rango de oficial.
func TestElCambioDeLiderEnLaBase(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 4)
	a, b, c, d := ps[0], ps[1], ps[2], ps[3]
	id := clanDeCuatro(t, ctx, repo, ps)
	require.NoError(t, repo.SetRank(ctx, postgres.ClanRank{ClanID: id, PlayerID: b, By: a, Officer: true, At: clanEn.Add(time.Hour)}))

	// Los triggers: el líder no puede ser oficial.
	_, err := store.Pool().Exec(ctx, `UPDATE clan_members SET rank = 'OFFICER' WHERE clan_id = $1 AND player_id = $2 AND left_at IS NULL`, id, a)
	assert.True(t, postgres.IsCheckViolation(err), "rango de oficial al líder: %v", err)
	_, err = store.Pool().Exec(ctx, `UPDATE clans SET leader_player_id = $2 WHERE id = $1`, id, b)
	assert.True(t, postgres.IsCheckViolation(err), "líder con rango de oficial: %v", err)

	traspaso := postgres.ClanLeaderChange{ClanID: id, Tag: "LOB", From: a, To: b, Reason: eventlog.LeaderReasonTransfer, By: a,
		At: clanEn.Add(2 * time.Hour), Tick: 7, AtMs: clanEn.Add(2 * time.Hour).UnixMilli()}
	require.NoError(t, repo.ChangeLeader(ctx, traspaso))
	require.NoError(t, repo.ChangeLeader(ctx, traspaso), "un reintento no escribe dos veces")
	rangos, lider := rangosEnLaBase(t, ctx, store, id)
	assert.Equal(t, b, lider)
	assert.Equal(t, "MEMBER", rangos[b], "el nuevo líder deja de ser oficial")
	assert.Equal(t, "OFFICER", rangos[a], "Q4: el anterior queda como oficial")
	assert.Equal(t, 1, contarEventos(t, ctx, store, "ClanLeaderChanged"))

	rechazoDeClan(t, repo.ChangeLeader(ctx, postgres.ClanLeaderChange{ClanID: id, Tag: "LOB", From: a, To: c,
		Reason: eventlog.LeaderReasonTransfer, By: a, At: clanEn.Add(3 * time.Hour)}), "quien cede el liderazgo ya no es el líder")
	rechazoDeClan(t, repo.ChangeLeader(ctx, postgres.ClanLeaderChange{ClanID: id, Tag: "LOB", From: b, To: uuid.New(),
		Reason: eventlog.LeaderReasonTransfer, By: b, At: clanEn.Add(3 * time.Hour)}), "ese jugador ya no es de tu clan")

	// La sucesión por ausencia, de b a c.
	require.NoError(t, repo.ChangeLeader(ctx, postgres.ClanLeaderChange{ClanID: id, Tag: "LOB", From: b, To: c,
		Reason: eventlog.LeaderReasonInactive, At: clanEn.Add(4 * time.Hour), Tick: 9, AtMs: clanEn.Add(4 * time.Hour).UnixMilli()}))

	paginaDe := func(quien uuid.UUID) []protocol.EventLogEntry {
		page, _, err := registro(store, 50).Page(ctx, quien, 0)
		require.NoError(t, err)
		return page.Entries
	}
	tipos := func(quien uuid.UUID) map[string]string {
		out := map[string]string{}
		for _, e := range paginaDe(quien) {
			out[e.Kind] = e.ClanTag
		}
		return out
	}
	assert.Equal(t, map[string]string{"CLAN_LEADERSHIP_GIVEN": "LOB"}, tipos(a))
	assert.Equal(t, map[string]string{"CLAN_LEADERSHIP_RECEIVED": "LOB", "CLAN_LEADERSHIP_LOST": "LOB"}, tipos(b))
	assert.Equal(t, map[string]string{"CLAN_LEADERSHIP_INHERITED": "LOB"}, tipos(c))
	assert.Empty(t, paginaDe(d), "a un tercero, nada")

	// Al salir el líder hereda un oficial, que deja de serlo en la misma
	// transacción.
	_, _, err = repo.Leave(ctx, postgres.ClanLeave{ClanID: id, PlayerID: c, Reason: clan.ReasonLeft, At: clanEn.Add(5 * time.Hour), NewLeader: a})
	require.NoError(t, err)
	rangos, lider = rangosEnLaBase(t, ctx, store, id)
	assert.Equal(t, a, lider)
	assert.Equal(t, "MEMBER", rangos[a])
	var nuevo string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT payload->>'newLeaderId' FROM world_events WHERE event_type = 'ClanLeft' ORDER BY id DESC LIMIT 1`).Scan(&nuevo))
	assert.Equal(t, a.String(), nuevo)
}

// §9, integración de la migración 000022: el CHECK clan_members_rank_valid sólo
// admite MEMBER u OFFICER. El líder se guarda en clans.leader_player_id, no
// como rango: ni «LEADER» ni otra grafía entran, tampoco en la estancia del
// líder (donde el trigger no lo frena porque no es OFFICER), y nada cambia.
func TestElRangoDeUnaEstanciaSoloEsMiembroUOficial(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 3)
	lider, raso, fuera := ps[0], ps[1], ps[2]
	id := clanDeCuatro(t, ctx, repo, ps[:2])

	for _, p := range []uuid.UUID{lider, raso} {
		for _, rango := range []string{"LEADER", "officer", ""} {
			_, err := store.Pool().Exec(ctx,
				`UPDATE clan_members SET rank = $3 WHERE clan_id = $1 AND player_id = $2 AND left_at IS NULL`, id, p, rango)
			assert.Equal(t, "clan_members_rank_valid", constraintDe(t, err), "rango %q", rango)
		}
	}
	rangos, l := rangosEnLaBase(t, ctx, store, id)
	assert.Equal(t, lider, l)
	assert.Equal(t, map[uuid.UUID]string{lider: "MEMBER", raso: "MEMBER"}, rangos, "nada cambió")

	// Una estancia nueva tampoco nace con otro rango.
	_, err := store.Pool().Exec(ctx,
		`INSERT INTO clan_members (clan_id, player_id, joined_at, rank) VALUES ($1, $2, now(), 'LEADER')`, id, fuera)
	assert.Equal(t, "clan_members_rank_valid", constraintDe(t, err))
}
