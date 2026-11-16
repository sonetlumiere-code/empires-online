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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// SPEC-CLAN-TERRITORIES §9, integración (ADR-041): la migración 000021 (la
// reclamación con su clan y los dos triggers de INV-TERR-012), la captura para
// un clan y de un clan, con una fila de evento por miembro y el registro de cada
// lado, la estancia abierta de quien reclamó (INV-CLAN-004) y la disolución que
// libera los territorios en su transacción.

// clanDeLaConquista funda un clan de la dueña de nuevaConquista y mete a la
// rival si conRival.
func clanDeLaConquista(t *testing.T, ctx context.Context, store *postgres.Store, c conquista, conRival bool) (*postgres.ClanRepo, int64) {
	t.Helper()
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	id, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: c.duena.Player.ID, Name: "Los Lobos", Tag: "LOB", At: clanEn, AtMs: clanEn.UnixMilli()})
	require.NoError(t, err)
	if conRival {
		require.NoError(t, repo.Invite(ctx, postgres.ClanInvite{ClanID: id, PlayerID: c.rival.Player.ID,
			InvitedBy: c.duena.Player.ID, At: clanEn, ExpiresAt: clanEn.Add(24 * time.Hour)}))
		require.NoError(t, repo.Join(ctx, postgres.ClanJoin{ClanID: id, PlayerID: c.rival.Player.ID, MaxMembers: 20, At: clanEn.Add(time.Minute)}))
	}
	return repo, id
}

// ganarParaElClan escribe la reclamación de la dueña sobre el territorio libre
// para el clan y su captura.
func ganarParaElClan(t *testing.T, ctx context.Context, store *postgres.Store, c conquista, clanID int64) territory.Control {
	t.Helper()
	inicio := c.reclamacion(c.duena, c.libre, nil, "req-clan")
	inicio.Claim.ClanID = clanID
	id, err := c.repo.StartClaim(ctx, inicio)
	require.NoError(t, err)
	ctrl, err := c.repo.CaptureTerritory(ctx, postgres.Capture{
		ClaimID: id, TerritoryID: c.libre.ID, TerritoryName: "Este", NewOwner: c.duena.Player.ID,
		NewCityID: c.duena.City.ID, NewClanID: clanID, ForClanTag: "LOB",
		ExpectedVersion: controlEnBase(t, ctx, c.territorios, store, c.libre.ID).Version,
		CapturedAt:      c.instanteDeHoy, Tick: 9, AtMs: 301_000, X: 40, Y: 10,
	})
	require.NoError(t, err)
	return ctrl
}

// ADR-041 §1, §3 y §5: la reclamación para el clan se escribe con su clan y
// vuelve con él al arrancar; la captura deja el territorio a nombre del clan sin
// bono para nadie y la vista pública lo lista. Un tercero que lo reclama escribe
// el comienzo a cada miembro; al capturarlo, cada miembro lo pierde en su
// registro con la sigla y el tercero lo gana una sola vez.
func TestUnTerritorioDeClanEnLaBase(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaConquista(t, ctx, store)
	clanes, clanID := clanDeLaConquista(t, ctx, store, c, true)
	limiteAntes, _ := limiteYPoblacion(t, ctx, store, c.duena.City.ID)

	inicio := c.reclamacion(c.duena, c.libre, nil, "req-clan")
	inicio.Claim.ClanID = clanID
	id, err := c.repo.StartClaim(ctx, inicio)
	require.NoError(t, err)
	activas, err := c.repo.ListActiveClaims(ctx)
	require.NoError(t, err)
	require.Len(t, activas, 1)
	assert.Equal(t, clanID, activas[0].ClanID, "vuelve con su clan al arrancar")

	ctrl, err := c.repo.CaptureTerritory(ctx, postgres.Capture{
		ClaimID: id, TerritoryID: c.libre.ID, TerritoryName: "Este", NewOwner: c.duena.Player.ID,
		NewCityID: c.duena.City.ID, NewClanID: clanID, ForClanTag: "LOB",
		ExpectedVersion: controlEnBase(t, ctx, c.territorios, store, c.libre.ID).Version,
		CapturedAt:      c.instanteDeHoy, Tick: 9, AtMs: 301_000, X: 40, Y: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, territory.OwnerClan, ctrl.OwnerType)
	got, ok := ctrl.ClanOwner()
	require.True(t, ok)
	assert.Equal(t, clanID, got)
	limite, _ := limiteYPoblacion(t, ctx, store, c.duena.City.ID)
	assert.Equal(t, limiteAntes, limite, "Q3: sin bono")
	info, err := clanes.Lookup(ctx, clanID)
	require.NoError(t, err)
	assert.Equal(t, []protocol.ClanTerritoryView{{ID: c.libre.ID, Name: c.libre.Name,
		X: (c.libre.MinX + c.libre.MaxX) / 2, Y: (c.libre.MinY + c.libre.MaxY) / 2}}, info.Territories)

	// Un tercero lo reclama y lo gana.
	_, _, _, _, bootstrapper := newRepos(store)
	tercero, err := bootstrapper.Create(ctx, bootstrapRequest("tercero", world.Tile{X: 20, Y: 50}))
	require.NoError(t, err)
	reclamo := c.reclamacion(tercero, c.libre, nil, "req-tercero")
	inicioTercero := postgres.ClaimStart{Claim: reclamo.Claim, TerritoryName: "Este", Tick: 10,
		OwnerClanID: clanID, OwnerClanTag: "LOB"}
	id2, err := c.repo.StartClaim(ctx, inicioTercero)
	require.NoError(t, err)
	empieza := eventosDeConquista(t, ctx, store, territory.EventClaimStarted)
	require.Len(t, empieza, 2, "una fila por miembro abierto")
	assert.ElementsMatch(t, []uuid.UUID{c.duena.Player.ID, c.rival.Player.ID}, []uuid.UUID{*empieza[0].player, *empieza[1].player})
	for _, e := range empieza {
		assert.Equal(t, tercero.Player.ID, *e.other)
	}

	_, err = c.repo.CaptureTerritory(ctx, postgres.Capture{
		ClaimID: id2, TerritoryID: c.libre.ID, TerritoryName: "Este", NewOwner: tercero.Player.ID,
		NewCityID: tercero.City.ID, NewBonus: 5, PrevClanID: clanID, PrevClanTag: "LOB",
		ExpectedVersion: ctrl.Version, CapturedAt: c.instanteDeHoy, Tick: 12, AtMs: 1_000_000, X: 40, Y: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, tercero.Player.ID.String(), *controlEnBase(t, ctx, c.territorios, store, c.libre.ID).OwnerID)
	info, err = clanes.Lookup(ctx, clanID)
	require.NoError(t, err)
	assert.Empty(t, info.Territories)

	paginaDe := func(quien uuid.UUID) []protocol.EventLogEntry {
		page, _, err := registro(store, 50).Page(ctx, quien, 0)
		require.NoError(t, err)
		return page.Entries
	}
	for _, miembro := range []uuid.UUID{c.duena.Player.ID, c.rival.Player.ID} {
		kinds := map[string]string{}
		for _, e := range paginaDe(miembro) {
			kinds[e.Kind] = e.ClanTag
		}
		assert.Equal(t, map[string]string{"TERRITORY_CLAIMED": "LOB", "TERRITORY_LOST": "LOB"}, kinds,
			"cada miembro, con la sigla de su clan")
	}
	delTercero := paginaDe(tercero.Player.ID)
	require.Len(t, delTercero, 1, "lo gana una sola vez, aunque el clan tuviera dos miembros")
	assert.Equal(t, "TERRITORY_TAKEN", delTercero[0].Kind)
	assert.Empty(t, delTercero[0].ClanTag, "lo ganó para sí")
	assert.Equal(t, c.duena.Player.ID.String(), delTercero[0].OtherPlayerID, "el miembro más antiguo")
}

// INV-TERR-012: la base no admite un dueño CLAN que no sea un clan vivo, ni
// disolver un clan que aún posee un territorio. Y la disolución de E18 los
// libera antes, en su misma transacción, con un evento por territorio que no va
// al registro de nadie.
func TestLaBaseNoDejaTerritoriosAClanesMuertos(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaConquista(t, ctx, store)
	clanes, clanID := clanDeLaConquista(t, ctx, store, c, false)

	_, err := store.Pool().Exec(ctx,
		`UPDATE territory_control SET owner_type = 'CLAN', owner_id = '999999', captured_at = now() WHERE territory_id = $1`, c.libre.ID)
	assert.True(t, postgres.IsCheckViolation(err), "un clan que no existe: %v", err)
	_, err = store.Pool().Exec(ctx,
		`UPDATE territory_control SET owner_type = 'CLAN', owner_id = 'lobos', captured_at = now() WHERE territory_id = $1`, c.libre.ID)
	assert.True(t, postgres.IsCheckViolation(err), "un owner_id que no es un id: %v", err)

	ctrl := ganarParaElClan(t, ctx, store, c, clanID)
	_, err = store.Pool().Exec(ctx, `UPDATE clans SET dissolved_at = now() WHERE id = $1`, clanID)
	assert.True(t, postgres.IsCheckViolation(err), "no se disuelve un clan con territorios: %v", err)

	aplicada, liberados, err := clanes.Leave(ctx, postgres.ClanLeave{ClanID: clanID, PlayerID: c.duena.Player.ID,
		Reason: clan.ReasonLeft, At: clanEn.Add(time.Hour), Dissolve: true, Tick: 20, AtMs: clanEn.Add(time.Hour).UnixMilli()})
	require.NoError(t, err)
	assert.True(t, aplicada)
	require.Len(t, liberados, 1)
	assert.Equal(t, c.libre.ID, liberados[0].TerritoryID)
	assert.Equal(t, territory.OwnerNone, liberados[0].OwnerType)
	assert.Equal(t, ctrl.Version+1, liberados[0].Version)
	enBase := controlEnBase(t, ctx, c.territorios, store, c.libre.ID)
	assert.Equal(t, territory.OwnerNone, enBase.OwnerType)
	assert.Nil(t, enBase.OwnerID)
	vivos, err := clanes.ListLive(ctx)
	require.NoError(t, err)
	assert.Empty(t, vivos)
	page, _, err := registro(store, 50).Page(ctx, c.duena.Player.ID, 0)
	require.NoError(t, err)
	assert.Empty(t, page.Entries, "liberar no va al registro")

	_, err = store.Pool().Exec(ctx,
		`UPDATE territory_control SET owner_type = 'CLAN', owner_id = $2, captured_at = now() WHERE territory_id = $1`,
		c.libre.ID, territory.ClanOwnerID(clanID))
	assert.True(t, postgres.IsCheckViolation(err), "un clan disuelto: %v", err)
}

// INV-CLAN-004: la captura para un clan comprueba en su transacción que quien
// reclamó tiene una estancia abierta en él; si salió, no escribe nada y vuelve
// como conflicto.
func TestLaCapturaParaUnClanExigeSerDeEl(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaConquista(t, ctx, store)
	clanes, clanID := clanDeLaConquista(t, ctx, store, c, true)

	inicio := c.reclamacion(c.duena, c.libre, nil, "req-clan")
	inicio.Claim.ClanID = clanID
	id, err := c.repo.StartClaim(ctx, inicio)
	require.NoError(t, err)
	_, _, err = clanes.Leave(ctx, postgres.ClanLeave{ClanID: clanID, PlayerID: c.duena.Player.ID,
		Reason: clan.ReasonLeft, At: clanEn.Add(time.Hour), NewLeader: c.rival.Player.ID})
	require.NoError(t, err)

	version := controlEnBase(t, ctx, c.territorios, store, c.libre.ID).Version
	_, err = c.repo.CaptureTerritory(ctx, postgres.Capture{
		ClaimID: id, TerritoryID: c.libre.ID, TerritoryName: "Este", NewOwner: c.duena.Player.ID,
		NewCityID: c.duena.City.ID, NewClanID: clanID, ForClanTag: "LOB",
		ExpectedVersion: version, CapturedAt: c.instanteDeHoy, AtMs: 301_000,
	})
	assert.ErrorIs(t, err, postgres.ErrConflict)
	enBase := controlEnBase(t, ctx, c.territorios, store, c.libre.ID)
	assert.Equal(t, territory.OwnerNone, enBase.OwnerType, "nada escrito")
	assert.Equal(t, version, enBase.Version)
	activas, err := c.repo.ListActiveClaims(ctx)
	require.NoError(t, err)
	assert.Len(t, activas, 1, "la reclamación sigue activa: la cierra la simulación como conflicto")
}

// ADR-041 §3 y §5: la captura de un clan a otro clan. La transacción toma los dos
// candados de clan en orden de id —el del clan que pierde, menor, antes que el
// del que gana—, aunque el que gana sea su primera clave; escribe una fila por
// miembro abierto del clan que lo pierde con las dos siglas y una sola para quien
// lo gana, a nombre del miembro más antiguo; y no da bono a nadie.
func TestLaCapturaDeUnClanAOtroClan(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaConquista(t, ctx, store)
	clanes, lobos := clanDeLaConquista(t, ctx, store, c, false)
	_, _, _, _, bootstrapper := newRepos(store)
	segundo, err := bootstrapper.Create(ctx, bootstrapRequest("segundo", world.Tile{X: 20, Y: 50}))
	require.NoError(t, err)
	require.NoError(t, clanes.Invite(ctx, postgres.ClanInvite{ClanID: lobos, PlayerID: segundo.Player.ID,
		InvitedBy: c.duena.Player.ID, At: clanEn, ExpiresAt: clanEn.Add(24 * time.Hour)}))
	require.NoError(t, clanes.Join(ctx, postgres.ClanJoin{ClanID: lobos, PlayerID: segundo.Player.ID, MaxMembers: 20, At: clanEn.Add(time.Minute)}))
	ctrl := ganarParaElClan(t, ctx, store, c, lobos)
	osos, err := clanes.Create(ctx, postgres.ClanCreate{PlayerID: c.rival.Player.ID, Name: "Los Osos", Tag: "OSO",
		At: clanEn.Add(time.Hour), AtMs: clanEn.Add(time.Hour).UnixMilli()})
	require.NoError(t, err)
	require.Greater(t, osos, lobos, "el clan que gana tiene el id mayor: su candado va el segundo")
	limiteDuena, _ := limiteYPoblacion(t, ctx, store, c.duena.City.ID)
	limiteRival, _ := limiteYPoblacion(t, ctx, store, c.rival.City.ID)

	inicio := c.reclamacion(c.rival, c.libre, nil, "req-osos")
	inicio.Claim.ClanID, inicio.OwnerClanID, inicio.OwnerClanTag, inicio.Tick = osos, lobos, "LOB", 10
	id, err := c.repo.StartClaim(ctx, inicio)
	require.NoError(t, err)
	assert.Len(t, eventosDeConquista(t, ctx, store, territory.EventClaimStarted), 2, "una fila por miembro de [LOB]")

	// Otro tiene el candado del clan que gana: la captura tiene que esperar en
	// él con el del clan que pierde ya tomado.
	tx, err := store.Pool().Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	var bloqueador int32
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&bloqueador))
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2::int4)`, int32(0x434C414E), int32(osos))
	require.NoError(t, err)

	type resultado struct {
		ctrl territory.Control
		err  error
	}
	hecho := make(chan resultado, 1)
	go func() {
		got, err := c.repo.CaptureTerritory(ctx, postgres.Capture{
			ClaimID: id, TerritoryID: c.libre.ID, TerritoryName: "Este", NewOwner: c.rival.Player.ID,
			NewCityID: c.rival.City.ID, NewClanID: osos, ForClanTag: "OSO", PrevClanID: lobos, PrevClanTag: "LOB",
			ExpectedVersion: ctrl.Version, CapturedAt: c.instanteDeHoy, Tick: 12, AtMs: 1_000_000, X: 40, Y: 10,
		})
		hecho <- resultado{got, err}
	}()
	type candado struct {
		clan    int64
		granted bool
	}
	candadosDeClan := func() map[int32][]candado {
		rows, err := store.Pool().Query(ctx,
			`SELECT pid, objid::bigint, granted FROM pg_locks
			  WHERE locktype = 'advisory' AND classid = $1 AND objsubid = 2 AND pid <> $2`,
			int64(0x434C414E), bloqueador)
		require.NoError(t, err)
		defer rows.Close()
		out := map[int32][]candado{}
		for rows.Next() {
			var pid int32
			var k candado
			require.NoError(t, rows.Scan(&pid, &k.clan, &k.granted))
			out[pid] = append(out[pid], k)
		}
		return out
	}
	var deLaCaptura []candado
	require.Eventually(t, func() bool {
		for _, ks := range candadosDeClan() {
			for _, k := range ks {
				if k.clan == osos && !k.granted {
					deLaCaptura = ks
					return true
				}
			}
		}
		return false
	}, 10*time.Second, 20*time.Millisecond, "la captura tiene que esperar en el candado del clan que gana")
	assert.ElementsMatch(t, []candado{{lobos, true}, {osos, false}}, deLaCaptura,
		"espera en el de [OSO] con el de [LOB], de id menor, ya tomado")
	select {
	case r := <-hecho:
		t.Fatalf("la captura no esperó al candado: %+v", r)
	default:
	}
	require.NoError(t, tx.Rollback(ctx))

	var r resultado
	select {
	case r = <-hecho:
	case <-time.After(10 * time.Second):
		t.Fatal("la captura no terminó al soltar el candado")
	}
	require.NoError(t, r.err)
	assert.Equal(t, territory.OwnerClan, r.ctrl.OwnerType)
	got, ok := r.ctrl.ClanOwner()
	require.True(t, ok)
	assert.Equal(t, osos, got)
	assert.Equal(t, ctrl.Version+1, r.ctrl.Version)
	l, _ := limiteYPoblacion(t, ctx, store, c.duena.City.ID)
	assert.Equal(t, limiteDuena, l, "Q3: nada que quitar")
	l, _ = limiteYPoblacion(t, ctx, store, c.rival.City.ID)
	assert.Equal(t, limiteRival, l, "Q3: nada que sumar")
	info, err := clanes.Lookup(ctx, lobos)
	require.NoError(t, err)
	assert.Empty(t, info.Territories)
	info, err = clanes.Lookup(ctx, osos)
	require.NoError(t, err)
	require.Len(t, info.Territories, 1)
	assert.Equal(t, c.libre.ID, info.Territories[0].ID)

	type fila struct {
		player, other            uuid.UUID
		only, ownerClan, forClan string
	}
	rows, err := store.Pool().Query(ctx,
		`SELECT player_id, counterpart_player_id, coalesce(payload->>'only', ''),
		        coalesce(payload->>'ownerClanTag', ''), coalesce(payload->>'forClanTag', '')
		   FROM world_events WHERE event_type = $1 AND counterpart_player_id IS NOT NULL ORDER BY id`,
		territory.EventControlChanged)
	require.NoError(t, err)
	var filas []fila
	for rows.Next() {
		var f fila
		require.NoError(t, rows.Scan(&f.player, &f.other, &f.only, &f.ownerClan, &f.forClan))
		filas = append(filas, f)
	}
	rows.Close()
	require.NoError(t, rows.Err())
	rival := c.rival.Player.ID
	assert.Equal(t, []fila{
		{c.duena.Player.ID, rival, "player", "LOB", "OSO"},
		{segundo.Player.ID, rival, "player", "LOB", "OSO"},
		{c.duena.Player.ID, rival, "counterpart", "", "OSO"},
	}, filas, "una por miembro de [LOB] y una para quien lo gana, a nombre del más antiguo")

	paginaDe := func(quien uuid.UUID) []protocol.EventLogEntry {
		page, _, err := registro(store, 50).Page(ctx, quien, 0)
		require.NoError(t, err)
		return page.Entries
	}
	for _, miembro := range []uuid.UUID{c.duena.Player.ID, segundo.Player.ID} {
		perdido := 0
		for _, e := range paginaDe(miembro) {
			if e.Kind == "TERRITORY_LOST" {
				perdido++
				assert.Equal(t, "LOB", e.ClanTag)
			}
		}
		assert.Equal(t, 1, perdido, "cada miembro de [LOB] lo pierde una vez")
	}
	var ganado []protocol.EventLogEntry
	for _, e := range paginaDe(rival) {
		if e.Kind == "TERRITORY_TAKEN" {
			ganado = append(ganado, e)
		}
	}
	require.Len(t, ganado, 1, "lo gana una sola vez, aunque [LOB] tuviera dos miembros")
	assert.Equal(t, "OSO", ganado[0].ClanTag, "lo ganó para su clan")
	assert.Equal(t, c.duena.Player.ID.String(), ganado[0].OtherPlayerID, "el miembro más antiguo de [LOB]")
}
