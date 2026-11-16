//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// SPEC-CLANS §9, integración: la migración 000020 (índices, triggers), cada
// escritura de la pertenencia, el canal con su candado y lo leído, la vista
// pública y la validación de carga.

// clanEn es el instante base de los tests de clanes, a milisegundos.
var clanEn = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// conversadoresDeClan da de alta n jugadores con su ciudad.
func conversadoresDeClan(t *testing.T, store *postgres.Store, ctx context.Context, n int) []uuid.UUID {
	t.Helper()
	_, _, _, _, bootstrapper := newRepos(store)
	out := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		r, err := bootstrapper.Create(ctx, bootstrapRequest(fmt.Sprintf("clan%d", i), world.Tile{X: int32(20 + 30*i), Y: 20}))
		require.NoError(t, err)
		out = append(out, r.Player.ID)
	}
	return out
}

func eventosDeClan(t *testing.T, store *postgres.Store, ctx context.Context) []string {
	t.Helper()
	rows, err := store.Pool().Query(ctx, `SELECT event_type FROM world_events WHERE event_type LIKE 'Clan%' ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		require.NoError(t, rows.Scan(&e))
		out = append(out, e)
	}
	return out
}

func rechazoDeClan(t *testing.T, err error, motivo string) {
	t.Helper()
	var r postgres.ClanRejected
	require.True(t, errors.As(err, &r), "se esperaba un rechazo, llegó %v", err)
	assert.Equal(t, motivo, r.Reason)
}

// ADR-040 §2 y §6: fundar, invitar, entrar y salir, cada uno en su transacción,
// idempotentes, con sus eventos; el liderazgo cambia y la salida del último
// disuelve el clan, borra sus invitaciones y libera el nombre.
func TestLaPertenenciaAUnClanEnLaBase(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 3)
	a, b, c := ps[0], ps[1], ps[2]

	id, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: a, Name: "Los Lobos", Tag: "LOB", At: clanEn, AtMs: clanEn.UnixMilli()})
	require.NoError(t, err)
	again, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: a, Name: "Los Lobos", Tag: "LOB", At: clanEn, AtMs: clanEn.UnixMilli()})
	require.NoError(t, err)
	assert.Equal(t, id, again, "un reintento devuelve el mismo clan")
	_, err = repo.Create(ctx, postgres.ClanCreate{PlayerID: b, Name: "los lobos", Tag: "OSO", At: clanEn})
	rechazoDeClan(t, err, "ese nombre ya existe")
	_, err = repo.Create(ctx, postgres.ClanCreate{PlayerID: b, Name: "Los Osos", Tag: "LOB", At: clanEn})
	rechazoDeClan(t, err, "esa sigla ya existe")

	inv := postgres.ClanInvite{ClanID: id, PlayerID: b, InvitedBy: a, At: clanEn, ExpiresAt: clanEn.Add(24 * time.Hour)}
	require.NoError(t, repo.Invite(ctx, inv))
	require.NoError(t, repo.Invite(ctx, inv), "un reintento no la duplica")
	rechazoDeClan(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: c, InvitedBy: b, At: clanEn, ExpiresAt: clanEn.Add(time.Hour)}),
		"sólo el líder de un clan invita")
	vivas, err := repo.ListInvites(ctx, clanEn)
	require.NoError(t, err)
	require.Len(t, vivas, 1)

	rechazoDeClan(t, repo.Join(ctx, postgres.ClanJoin{ClanID: id, PlayerID: c, MaxMembers: 20, At: clanEn}), "la invitación no existe o caducó")
	rechazoDeClan(t, repo.Join(ctx, postgres.ClanJoin{ClanID: id, PlayerID: b, MaxMembers: 20, At: clanEn.Add(25 * time.Hour)}),
		"la invitación no existe o caducó")
	rechazoDeClan(t, repo.Join(ctx, postgres.ClanJoin{ClanID: id, PlayerID: b, MaxMembers: 1, At: clanEn.Add(time.Minute)}),
		"el clan está lleno")
	join := postgres.ClanJoin{ClanID: id, PlayerID: b, MaxMembers: 20, At: clanEn.Add(time.Minute)}
	require.NoError(t, repo.Join(ctx, join))
	require.NoError(t, repo.Join(ctx, join), "un reintento no abre otra estancia")
	vivas, err = repo.ListInvites(ctx, clanEn)
	require.NoError(t, err)
	assert.Empty(t, vivas, "al entrar se borran sus invitaciones")

	clanes, err := repo.ListLive(ctx)
	require.NoError(t, err)
	require.Len(t, clanes, 1)
	assert.Equal(t, clan.Clan{ID: id, Name: "Los Lobos", Tag: "LOB", LeaderID: a}, clanes[0])

	// El líder sale: hereda b.
	salida := clanEn.Add(time.Hour)
	l := postgres.ClanLeave{ClanID: id, PlayerID: a, Reason: clan.ReasonLeft, At: salida, NewLeader: b}
	aplicada, _, err := repo.Leave(ctx, l)
	require.NoError(t, err)
	assert.True(t, aplicada)
	aplicada, _, err = repo.Leave(ctx, l)
	require.NoError(t, err)
	assert.False(t, aplicada, "un reintento no escribe dos veces")
	clanes, _ = repo.ListLive(ctx)
	assert.Equal(t, b, clanes[0].LeaderID)
	estancias, err := repo.ListStays(ctx, salida.Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, estancias, 2, "la abierta de b y la cerrada reciente de a")
	estancias, _ = repo.ListStays(ctx, salida)
	assert.Len(t, estancias, 1, "la salida anterior al plazo no se carga")

	// b invita a c y sale el último: el clan se disuelve con sus invitaciones.
	require.NoError(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: c, InvitedBy: b, At: salida, ExpiresAt: salida.Add(time.Hour)}))
	_, _, err = repo.Leave(ctx, postgres.ClanLeave{ClanID: id, PlayerID: b, Reason: clan.ReasonLeft, At: salida.Add(time.Minute), Dissolve: true})
	require.NoError(t, err)
	clanes, _ = repo.ListLive(ctx)
	assert.Empty(t, clanes)
	vivas, _ = repo.ListInvites(ctx, salida)
	assert.Empty(t, vivas)
	_, err = repo.Create(ctx, postgres.ClanCreate{PlayerID: c, Name: "Los Lobos", Tag: "LOB", At: salida})
	require.NoError(t, err, "el nombre y la sigla de un clan disuelto quedan libres")

	assert.Equal(t, []string{"ClanFounded", "ClanJoined", "ClanLeft", "ClanLeft", "ClanFounded"}, eventosDeClan(t, store, ctx))
}

// INV-CLAN-001: una estancia abierta por jugador; y la validación de carga
// encuentra un clan cuyo líder no es miembro abierto.
func TestUnaEstanciaAbiertaPorJugador(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 2)
	a, b := ps[0], ps[1]
	id1, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: a, Name: "Los Lobos", Tag: "LOB", At: clanEn})
	require.NoError(t, err)
	id2, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: b, Name: "Los Osos", Tag: "OSO", At: clanEn})
	require.NoError(t, err)

	_, err = store.Pool().Exec(ctx, `INSERT INTO clan_members (clan_id, player_id, joined_at) VALUES ($1, $2, $3)`, id2, a, clanEn)
	require.Error(t, err)
	assert.True(t, postgres.IsUniqueViolation(err), "INV-CLAN-001: %v", err)

	malos, err := repo.Violations(ctx)
	require.NoError(t, err)
	assert.Empty(t, malos)
	_, err = store.Pool().Exec(ctx, `UPDATE clans SET leader_player_id = $2 WHERE id = $1`, id1, b)
	require.NoError(t, err)
	malos, err = repo.Violations(ctx)
	require.NoError(t, err)
	require.Len(t, malos, 1)
	assert.Equal(t, "INV-CLAN-001", malos[0].InvID)
	assert.Equal(t, id1, malos[0].ClanID)
}

// INV-CLAN-002: los dos triggers de 000020 impiden juntar a la Orden y al Caos,
// al entrar y al apuntar o cambiar la facción de un miembro.
func TestLosTriggersNoJuntanALaOrdenYAlCaos(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 3)
	orden, caos, neutral := ps[0], ps[1], ps[2]
	exec := func(sql string, args ...any) error {
		_, err := store.Pool().Exec(ctx, sql, args...)
		return err
	}
	require.NoError(t, exec(`UPDATE players SET faction_id = $2 WHERE id = $1`, orden, idOrden))
	require.NoError(t, exec(`UPDATE players SET faction_id = $2 WHERE id = $1`, caos, idCaos))
	id, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: orden, Name: "Los Lobos", Tag: "LOB", At: clanEn})
	require.NoError(t, err)

	// Al entrar, por el repositorio y a mano.
	require.NoError(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: caos, InvitedBy: orden, At: clanEn, ExpiresAt: clanEn.Add(time.Hour)}))
	rechazoDeClan(t, repo.Join(ctx, postgres.ClanJoin{ClanID: id, PlayerID: caos, MaxMembers: 20, At: clanEn.Add(time.Second)}),
		"un clan no junta a la Orden y al Caos")
	rechazoDelTrigger(t, exec(`INSERT INTO clan_members (clan_id, player_id, joined_at) VALUES ($1, $2, $3)`, id, caos, clanEn), "INV-CLAN-002")

	// Un neutral dentro no puede apuntarse al Caos, ni entrar en él sin entrada.
	require.NoError(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: neutral, InvitedBy: orden, At: clanEn, ExpiresAt: clanEn.Add(time.Hour)}))
	require.NoError(t, repo.Join(ctx, postgres.ClanJoin{ClanID: id, PlayerID: neutral, MaxMembers: 20, At: clanEn.Add(time.Second)}))
	rechazoDelTrigger(t, exec(`UPDATE players SET faction_join_id = $2, faction_join_at = $3 WHERE id = $1`, neutral, idCaos, clanEn), "INV-CLAN-002")
	rechazoDelTrigger(t, exec(`UPDATE players SET faction_id = $2 WHERE id = $1`, neutral, idCaos), "INV-CLAN-002")
	require.NoError(t, exec(`UPDATE players SET faction_join_id = $2, faction_join_at = $3 WHERE id = $1`, neutral, idOrden, clanEn), "a la Orden, sí")
}

// RN-CLAN-009 e INV-SEC-012: sólo un miembro abierto escribe y lee el canal,
// lee sólo lo posterior a su entrada, un requestId repetido no guarda otro
// mensaje y lo leído sólo avanza.
func TestElCanalDeUnClan(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{PageSize: 2})
	ps := conversadoresDeClan(t, store, ctx, 3)
	a, b, c := ps[0], ps[1], ps[2]
	id, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: a, Name: "Los Lobos", Tag: "LOB", At: clanEn})
	require.NoError(t, err)

	_, err = repo.ChatSend(ctx, b, "hola", uuid.New())
	assert.ErrorIs(t, err, clan.ErrNotMember)
	_, err = repo.ChatPage(ctx, b, 0)
	assert.ErrorIs(t, err, clan.ErrNotMember)

	req := uuid.New()
	m1, err := repo.ChatSend(ctx, a, "antes de que entres", req)
	require.NoError(t, err)
	assert.Equal(t, id, m1.ClanID)
	assert.Equal(t, []uuid.UUID{a}, m1.Members)
	assert.NotEmpty(t, m1.FromCityName)
	dup, err := repo.ChatSend(ctx, a, "antes de que entres", req)
	require.NoError(t, err)
	assert.True(t, dup.Duplicate)
	assert.Equal(t, m1.ID, dup.ID, "INV-SEC-007")

	require.NoError(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: b, InvitedBy: a, At: clanEn, ExpiresAt: clanEn.Add(time.Hour)}))
	require.NoError(t, repo.Join(ctx, postgres.ClanJoin{ClanID: id, PlayerID: b, MaxMembers: 20, At: clanEn.Add(time.Second)}))
	var ultimo int64
	for i := 0; i < 3; i++ {
		s, err := repo.ChatSend(ctx, b, fmt.Sprintf("mensaje %d", i), uuid.New())
		require.NoError(t, err)
		assert.ElementsMatch(t, []uuid.UUID{a, b}, s.Members)
		ultimo = s.ID
	}

	page, err := repo.ChatPage(ctx, b, 0)
	require.NoError(t, err)
	require.Len(t, page.Messages, 2)
	assert.Equal(t, ultimo, page.Messages[1].ID, "de más antiguo a más reciente")
	require.NotNil(t, page.NextBeforeID)
	resto, err := repo.ChatPage(ctx, b, *page.NextBeforeID)
	require.NoError(t, err)
	require.Len(t, resto.Messages, 1)
	assert.Nil(t, resto.NextBeforeID, "lo anterior a su entrada no existe para b")
	for _, m := range append(page.Messages, resto.Messages...) {
		assert.NotEqual(t, m1.ID, m.ID)
	}
	todo, err := repo.ChatPage(ctx, a, 0)
	require.NoError(t, err)
	assert.Len(t, todo.Messages, 2)

	require.NoError(t, repo.ChatMarkRead(ctx, b, func(int64) int64 { return ultimo }))
	require.NoError(t, repo.ChatMarkRead(ctx, b, func(int64) int64 { return 1 }))
	page, _ = repo.ChatPage(ctx, b, 0)
	assert.Equal(t, ultimo, page.ReadUpToID, "sólo avanza")

	info, err := repo.Lookup(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "LOB", info.Tag)
	require.Len(t, info.Members, 2)
	assert.Equal(t, clan.RoleLeader, info.Members[0].Role)
	_, err = repo.Lookup(ctx, id+100)
	assert.ErrorIs(t, err, clan.ErrUnknownClan)
	_ = c
}

// INV-SEC-012: una salida espera al candado de un envío en curso y, confirmada,
// el que salió no escribe ni un mensaje más.
func TestUnaSalidaEsperaAlCandadoDelCanal(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 2)
	a, b := ps[0], ps[1]
	id, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: a, Name: "Los Lobos", Tag: "LOB", At: clanEn})
	require.NoError(t, err)
	require.NoError(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: b, InvitedBy: a, At: clanEn, ExpiresAt: clanEn.Add(time.Hour)}))
	require.NoError(t, repo.Join(ctx, postgres.ClanJoin{ClanID: id, PlayerID: b, MaxMembers: 20, At: clanEn.Add(time.Second)}))

	tx, err := store.Pool().Begin(ctx)
	require.NoError(t, err)
	// Si el test falla con la transacción abierta, se suelta antes de cerrar el
	// pool: un Cleanup posterior corre antes que el de newTestStore.
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2::int4)`, int32(0x434C414E), int32(id))
	require.NoError(t, err)

	hecho := make(chan error, 1)
	go func() {
		_, _, err := repo.Leave(context.Background(), postgres.ClanLeave{
			ClanID: id, PlayerID: b, Reason: clan.ReasonExpelled, By: a, At: clanEn.Add(time.Minute),
		})
		hecho <- err
	}()
	select {
	case err := <-hecho:
		t.Fatalf("la salida no esperó al candado del clan: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, tx.Commit(ctx))
	require.NoError(t, <-hecho)

	_, err = repo.ChatSend(ctx, b, "sigo aquí", uuid.New())
	assert.ErrorIs(t, err, clan.ErrNotMember)
	assert.Equal(t, []string{"ClanFounded", "ClanJoined", "ClanMemberExpelled"}, eventosDeClan(t, store, ctx))
	var counterpart uuid.UUID
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT counterpart_player_id FROM world_events WHERE event_type = 'ClanMemberExpelled'`).Scan(&counterpart))
	assert.Equal(t, a, counterpart, "ADR-028: el líder, como el otro implicado")
}
