//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// SPEC-CLAN-WARS §9, integración: la migración 000026 (la tabla, sus CHECK, el
// índice por par y los tres triggers), cada orden con su comprobación de líder,
// estado, alianza, tope y tregua bajo los dos candados (INV-CLAN-008), el final
// en la transacción de la disolución, el marcador sumado desde los eventos que
// puntúan (INV-CLAN-009) y el registro de cada lado (ADR-045).

// declarar es la declaración de by (líder de desde) al clan hacia.
func declarar(desde, hacia int64, by uuid.UUID, rid string, at time.Time) postgres.WarWrite {
	return postgres.WarWrite{
		Insert: true, From: clan.WarDeclared, Event: eventlog.EventClanWarDeclared, By: by, ByClan: desde,
		MaxWars: 1, Cooldown: 24 * time.Hour,
		War: clan.War{Attacker: desde, Defender: hacia, DeclaredBy: by, RequestID: rid, Status: clan.WarDeclared,
			DeclaredAtMs: at.UnixMilli(), StartsAtMs: at.Add(15 * time.Minute).UnixMilli()},
		Tick: 1, AtMs: at.UnixMilli(),
	}
}

// estadoDeGuerra lee de la base el estado, las ofertas, el motivo, el vencedor y
// el marcador de una guerra.
func estadoDeGuerra(t *testing.T, ctx context.Context, store *postgres.Store, id int64) (string, bool, bool, string, int64, int64, int64) {
	t.Helper()
	var (
		status, reason string
		porA, porD     bool
		winner, sa, sd int64
	)
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT status, peace_offered_by_attacker, peace_offered_by_defender, coalesce(end_reason, ''),
		        coalesce(winner_clan, 0), coalesce(attacker_score, 0), coalesce(defender_score, 0)
		   FROM clan_wars WHERE id = $1`, id).Scan(&status, &porA, &porD, &reason, &winner, &sa, &sd))
	return status, porA, porD, reason, winner, sa, sd
}

// filasDe cuenta los eventos de un tipo de un jugador.
func filasDe(t *testing.T, ctx context.Context, store *postgres.Store, tipo string, quien uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT count(*) FROM world_events WHERE event_type = $1 AND player_id = $2`, tipo, quien).Scan(&n))
	return n
}

// RN-CLANWAR-001, RN-CLANWAR-002, RN-CLANWAR-006, RN-CLANWAR-010 e
// INV-CLAN-008: declarar, empezar, ofrecer la paz y aceptarla en la base,
// idempotentes, con quién puede cada cosa, el tope y la tregua comprobados en la
// transacción; los eventos, el registro de cada lado y la carga.
func TestLaGuerraEnLaBase(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 5)
	lobLider, lobMiembro, osoLider, osoMiembro, ciervo := ps[0], ps[1], ps[2], ps[3], ps[4]
	lob := clanDeCuatro(t, ctx, repo, ps[:2])
	oso := clanOsos(t, ctx, repo, osoLider, osoMiembro)
	cie, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: ciervo, Name: "Los Ciervos", Tag: "CIE", At: clanEn, AtMs: clanEn.UnixMilli()})
	require.NoError(t, err)
	at := clanEn.Add(time.Hour)

	// Sólo el líder declara; un reintento no escribe dos veces.
	_, err = repo.ApplyWar(ctx, declarar(lob, oso, lobMiembro, "w0", at))
	rechazoDeClan(t, err, "sólo el líder decide las guerras del clan")
	w := declarar(lob, oso, lobLider, "w1", at)
	id, err := repo.ApplyWar(ctx, w)
	require.NoError(t, err)
	otra, err := repo.ApplyWar(ctx, w)
	require.NoError(t, err, "un reintento no escribe dos veces")
	assert.Equal(t, id, otra)
	// ADR-045 §6: la declaración, una fila por miembro de los dos clanes salvo
	// quien la dio, con la sigla del otro clan.
	assert.Zero(t, filasDe(t, ctx, store, eventlog.EventClanWarDeclared, lobLider))
	for _, m := range []uuid.UUID{lobMiembro, osoLider, osoMiembro} {
		assert.Equal(t, 1, filasDe(t, ctx, store, eventlog.EventClanWarDeclared, m), "uno por miembro")
	}
	assert.Equal(t, "LOB", kindsDe(t, ctx, store, osoMiembro)["CLAN_WAR_DECLARED"])

	// Una guerra abierta por par, en cualquier sentido, y el tope de declaradas.
	_, err = repo.ApplyWar(ctx, declarar(oso, lob, osoLider, "w2", at))
	rechazoDeClan(t, err, "ya hay una guerra abierta con ese clan")
	_, err = repo.ApplyWar(ctx, declarar(lob, cie, lobLider, "w3", at))
	rechazoDeClan(t, err, "tu clan ya tiene el máximo de guerras declaradas")
	recibida := declarar(cie, lob, ciervo, "w4", at)
	idCie, err := repo.ApplyWar(ctx, recibida)
	require.NoError(t, err, "las recibidas no cuentan para el tope de lob")

	// El comienzo, tolerante e idempotente.
	g := w.War
	g.ID = id
	activa, err := g.Start(g.StartsAtMs, 72*time.Hour)
	require.NoError(t, err)
	comienzo := postgres.WarWrite{War: activa, From: clan.WarDeclared, Event: eventlog.EventClanWarStarted, Tolerant: true, Tick: 2, AtMs: g.StartsAtMs}
	for vez := 0; vez < 2; vez++ {
		_, err = repo.ApplyWar(ctx, comienzo)
		require.NoError(t, err)
	}
	assert.Equal(t, 1, contarEventos(t, ctx, store, eventlog.EventClanWarStarted))
	status, _, _, _, _, _, _ := estadoDeGuerra(t, ctx, store, id)
	assert.Equal(t, "ACTIVE", status)

	// La oferta de paz del defensor: una fila para el líder del otro clan.
	ofrece := postgres.WarWrite{War: activa.WithPeaceOffer(oso, true), From: clan.WarActive,
		Event: eventlog.EventClanWarPeaceOffered, By: osoMiembro, ByClan: oso, Tick: 3, AtMs: at.Add(30 * time.Minute).UnixMilli()}
	_, err = repo.ApplyWar(ctx, ofrece)
	rechazoDeClan(t, err, "sólo el líder decide las guerras del clan")
	ofrece.By = osoLider
	_, err = repo.ApplyWar(ctx, ofrece)
	require.NoError(t, err)
	_, porA, porD, _, _, _, _ := estadoDeGuerra(t, ctx, store, id)
	assert.False(t, porA)
	assert.True(t, porD)
	assert.Equal(t, 1, filasDe(t, ctx, store, eventlog.EventClanWarPeaceOffered, lobLider))
	assert.Zero(t, filasDe(t, ctx, store, eventlog.EventClanWarPeaceOffered, lobMiembro), "sólo a los mandos")
	assert.Equal(t, "OSO", kindsDe(t, ctx, store, lobLider)["CLAN_WAR_PEACE_OFFERED"])

	// La aceptación: empate, con el marcador congelado.
	conOferta := activa.WithPeaceOffer(oso, true).AddPoints(lob, 4).AddPoints(oso, 7)
	fin, err := conOferta.End(clan.WarEndPeace, 0, at.Add(40*time.Minute).UnixMilli())
	require.NoError(t, err)
	acepta := postgres.WarWrite{War: fin, From: clan.WarActive, Event: eventlog.EventClanWarEnded, By: lobLider, ByClan: lob, Tick: 4, AtMs: fin.EndedAtMs}
	for vez := 0; vez < 2; vez++ {
		_, err = repo.ApplyWar(ctx, acepta)
		require.NoError(t, err, "un reintento no escribe dos veces")
	}
	status, porA, porD, reason, winner, sa, sd := estadoDeGuerra(t, ctx, store, id)
	assert.Equal(t, [3]string{"ENDED", "PEACE", ""}, [3]string{status, reason, ""})
	assert.False(t, porA || porD, "el final borra las ofertas")
	assert.Zero(t, winner, "empate")
	assert.Equal(t, [2]int64{4, 7}, [2]int64{sa, sd})
	assert.Zero(t, filasDe(t, ctx, store, eventlog.EventClanWarEnded, lobLider), "a quien la dio, nada")
	for _, m := range []uuid.UUID{lobMiembro, osoLider, osoMiembro} {
		assert.Equal(t, 1, filasDe(t, ctx, store, eventlog.EventClanWarEnded, m))
	}
	page, _, err := registro(store, 50).Page(ctx, osoMiembro, 0)
	require.NoError(t, err)
	var final bool
	for _, e := range page.Entries {
		if e.Kind == "CLAN_WAR_ENDED" {
			final = true
			assert.Equal(t, "LOB", e.ClanTag)
			assert.Equal(t, "DRAWN", e.WarResult)
			assert.Equal(t, "PEACE", e.WarReason)
			require.NotNil(t, e.OurScore)
			assert.Equal(t, int64(7), *e.OurScore, "el marcador visto desde su clan")
			assert.Equal(t, int64(4), *e.TheirScore)
		}
	}
	assert.True(t, final, "el final está en el registro del defensor")

	// RN-CLANWAR-006: la tregua, comprobada en la transacción.
	_, err = repo.ApplyWar(ctx, declarar(oso, lob, osoLider, "w5", at.Add(2*time.Hour)))
	rechazoDeClan(t, err, "vuestros clanes terminaron una guerra hace poco")
	sinTregua := declarar(oso, lob, osoLider, "w6", at.Add(2*time.Hour))
	sinTregua.Cooldown = time.Minute
	_, err = repo.ApplyWar(ctx, sinTregua)
	require.NoError(t, err, "pasada la tregua se puede volver a declarar")

	// La carga (ADR-045 §1): las abiertas, la reciente y el historial.
	open, recent, records, err := repo.ListWars(ctx, at)
	require.NoError(t, err)
	require.Len(t, open, 2)
	assert.Equal(t, idCie, open[0].ID)
	assert.Equal(t, clan.WarDeclared, open[1].Status)
	require.Len(t, recent, 1)
	assert.Equal(t, id, recent[0].ID)
	assert.Equal(t, clan.WarRecord{Drawn: 1}, records[lob])
	assert.Equal(t, clan.WarRecord{Drawn: 1}, records[oso])

	// La vista pública (RN-CLANWAR-009): las abiertas y el historial.
	info, err := repo.Lookup(ctx, lob)
	require.NoError(t, err)
	require.Len(t, info.Wars, 2)
	assert.Equal(t, "OSO", info.Wars[0].Tag, "por id de clan")
	assert.False(t, info.Wars[0].DeclaredByUs)
	assert.Equal(t, "CIE", info.Wars[1].Tag)
	assert.Equal(t, 1, info.WarRecord.Drawn)
}

// La migración 000026: los CHECK, el índice por par sin orden y los tres
// triggers (INV-CLAN-008, INV-PERSIST-005).
func TestLasRestriccionesDeLasGuerras(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 3)
	lob := clanDeCuatro(t, ctx, repo, ps[:1])
	oso := clanOsos(t, ctx, repo, ps[1])
	cie, err := repo.Create(ctx, postgres.ClanCreate{PlayerID: ps[2], Name: "Los Ciervos", Tag: "CIE", At: clanEn, AtMs: clanEn.UnixMilli()})
	require.NoError(t, err)
	insertar := func(a, d int64, status, rid string) error {
		var ends *time.Time
		if status != "DECLARED" {
			ends = &clanEn
		}
		_, err := store.Pool().Exec(ctx,
			`INSERT INTO clan_wars (attacker_clan, defender_clan, declared_by_player, request_id, status, declared_at, starts_at, ends_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $6, $7)`, a, d, ps[0], rid, status, clanEn, ends)
		return err
	}
	assert.Equal(t, "clan_wars_two_clans", constraintDe(t, insertar(lob, lob, "DECLARED", "c1")))
	assert.Equal(t, "clan_wars_status_valid", constraintDe(t, insertar(lob, oso, "LIVE", "c2")))
	assert.Equal(t, "clan_wars_ends_at", constraintDe(t, func() error {
		_, err := store.Pool().Exec(ctx,
			`INSERT INTO clan_wars (attacker_clan, defender_clan, declared_by_player, request_id, status, declared_at, starts_at)
			 VALUES ($1, $2, $3, 'c3', 'ACTIVE', $4, $4)`, lob, oso, ps[0], clanEn)
		return err
	}()))
	assert.Equal(t, "clan_wars_ended", constraintDe(t, insertar(lob, oso, "ENDED", "c4")), "un final sin motivo ni marcador")
	require.NoError(t, insertar(lob, oso, "DECLARED", "c5"))
	assert.Equal(t, "clan_wars_one_open", constraintDe(t, insertar(oso, lob, "ACTIVE", "c6")), "una abierta por par, en cualquier sentido")

	// RN-CLANWAR-003: una alianza no nace entre dos clanes en guerra.
	a, b := clan.CanonicalClans(lob, oso)
	_, err = store.Pool().Exec(ctx,
		`INSERT INTO clan_alliances (clan_a, clan_b, proposed_by_clan, proposed_by_player, status, request_id, created_at)
		 VALUES ($1, $2, $1, $3, 'PROPOSED', 'a1', $4)`, a, b, ps[0], clanEn)
	assert.True(t, postgres.IsCheckViolation(err), "una oferta de alianza entre dos clanes en guerra: %v", err)
	// …y una guerra no nace entre dos clanes aliados.
	x, y := clan.CanonicalClans(oso, cie)
	_, err = store.Pool().Exec(ctx,
		`INSERT INTO clan_alliances (clan_a, clan_b, proposed_by_clan, proposed_by_player, status, request_id, created_at, accepted_at)
		 VALUES ($1, $2, $1, $3, 'ACTIVE', 'a2', $4, $4)`, x, y, ps[1], clanEn)
	require.NoError(t, err)
	assert.True(t, postgres.IsCheckViolation(insertar(oso, cie, "DECLARED", "c7")), "una guerra entre aliados")
	// Por la orden, con su motivo.
	_, err = repo.ApplyWar(ctx, declarar(cie, oso, ps[2], "c8", clanEn.Add(time.Hour)))
	rechazoDeClan(t, err, "vuestros clanes están aliados: rompe antes la alianza")

	// Un clan disuelto no entra en guerra.
	_, _, err = repo.Leave(ctx, postgres.ClanLeave{ClanID: cie, PlayerID: ps[2], Reason: clan.ReasonLeft, At: clanEn.Add(2 * time.Hour), Dissolve: true})
	require.NoError(t, err)
	_, err = store.Pool().Exec(ctx, `DELETE FROM clan_wars`)
	require.NoError(t, err)
	assert.True(t, postgres.IsCheckViolation(insertar(lob, cie, "DECLARED", "c9")), "un clan disuelto")
}

// RN-CLANWAR-003: la declaración caduca en su transacción la oferta de alianza
// pendiente entre los dos clanes.
func TestLaDeclaracionCaducaLaOfertaDeAlianza(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 2)
	lob := clanDeCuatro(t, ctx, repo, ps[:1])
	oso := clanOsos(t, ctx, repo, ps[1])
	at := clanEn.Add(time.Hour)
	oferta, err := repo.ApplyAlliance(ctx, proponer(lob, oso, ps[0], "o1", at))
	require.NoError(t, err)
	_, err = repo.ApplyWar(ctx, declarar(oso, lob, ps[1], "g1", at.Add(time.Minute)))
	require.NoError(t, err)
	var status string
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT status FROM clan_alliances WHERE id = $1`, oferta).Scan(&status))
	assert.Equal(t, string(diplomacy.StatusExpired), status)
}

// RN-CLANWAR-008 y ADR-045 §5: la disolución de un clan termina sus guerras en
// su transacción, con la victoria del otro, el marcador de la RAM y una fila del
// final para cada miembro del otro clan.
func TestLaDisolucionTerminaLasGuerras(t *testing.T) {
	store, ctx := newTestStore(t)
	repo := postgres.NewClanRepo(store, postgres.ClansConfig{})
	ps := conversadoresDeClan(t, store, ctx, 3)
	lob := clanDeCuatro(t, ctx, repo, ps[:2])
	oso := clanOsos(t, ctx, repo, ps[2])
	at := clanEn.Add(time.Hour)
	w := declarar(lob, oso, ps[0], "d1", at)
	id, err := repo.ApplyWar(ctx, w)
	require.NoError(t, err)
	g := w.War
	g.ID = id
	g, err = g.Start(g.StartsAtMs, 72*time.Hour)
	require.NoError(t, err)
	_, err = repo.ApplyWar(ctx, postgres.WarWrite{War: g, From: clan.WarDeclared, Event: eventlog.EventClanWarStarted, Tolerant: true})
	require.NoError(t, err)

	disuelve := at.Add(time.Hour)
	fin, err := g.AddPoints(oso, 2).End(clan.WarEndDissolved, lob, disuelve.UnixMilli())
	require.NoError(t, err)
	leave := postgres.ClanLeave{ClanID: oso, PlayerID: ps[2], Reason: clan.ReasonLeft, At: disuelve, Dissolve: true,
		WarEnds: []postgres.WarEnd{{War: fin}}, Tick: 9, AtMs: disuelve.UnixMilli()}
	for vez := 0; vez < 2; vez++ {
		_, _, err = repo.Leave(ctx, leave)
		require.NoError(t, err, "tolerante")
	}
	status, _, _, reason, winner, sa, sd := estadoDeGuerra(t, ctx, store, id)
	assert.Equal(t, [2]string{"ENDED", "DISSOLVED"}, [2]string{status, reason})
	assert.Equal(t, lob, winner)
	assert.Equal(t, [2]int64{0, 2}, [2]int64{sa, sd})
	for _, m := range ps[:2] {
		assert.Equal(t, 1, filasDe(t, ctx, store, eventlog.EventClanWarEnded, m), "cada miembro del otro clan")
	}
	assert.Zero(t, filasDe(t, ctx, store, eventlog.EventClanWarEnded, ps[2]), "el clan disuelto ya no tiene a nadie")
	assert.Equal(t, "OSO", kindsDe(t, ctx, store, ps[1])["CLAN_WAR_ENDED"])
}

// INV-CLAN-009 y ADR-045 §4: una muerte escribe sus puntos en el payload de su
// UnitKilled, y el marcador de una guerra abierta es la suma de los eventos que
// la nombran, por clan; las otras guerras y los eventos sin guerra no cuentan.
func TestElMarcadorEsLaSumaDeLosEventos(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, units, _, bootstrapper := newRepos(store)
	victima, err := bootstrapper.Create(ctx, bootstrapRequest("victimaguerra", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	asesino, err := bootstrapper.Create(ctx, bootstrapRequest("asesinoguerra", world.Tile{X: 50, Y: 20}))
	require.NoError(t, err)
	k := postgres.Kill{
		VictimID: victima.Units[0].ID, VictimPlayer: victima.Player.ID,
		KillerID: asesino.Units[0].ID, KillerPlayer: asesino.Player.ID,
		CityID: &victima.City.ID, PopulationCost: 1, At: world.Tile{X: 21, Y: 20}, Tick: 5,
		War: &clan.WarPoints{WarID: 77, ClanID: 3, Points: clan.PointsKill},
	}
	require.NoError(t, store.InTx(ctx, func(tx pgx.Tx) error {
		_, err := units.Kill(ctx, tx, k)
		return err
	}))
	// Más hechos, escritos como los escriben el derribo y la captura.
	for _, ev := range []struct {
		war, clan, points int64
	}{{77, 3, clan.PointsBuilding}, {77, 5, clan.PointsTerritory}, {78, 3, 100}} {
		_, err := store.Pool().Exec(ctx,
			`INSERT INTO world_events (event_type, tick, player_id, payload)
			 VALUES ('BuildingDestroyed', 6, $1, jsonb_build_object('war', jsonb_build_object('warId', $2::bigint, 'clanId', $3::bigint, 'points', $4::bigint)))`,
			victima.Player.ID, ev.war, ev.clan, ev.points)
		require.NoError(t, err)
	}
	_, err = store.Pool().Exec(ctx, `INSERT INTO world_events (event_type, tick, player_id, payload) VALUES ('UnitKilled', 7, $1, '{}')`, victima.Player.ID)
	require.NoError(t, err)

	scores, err := postgres.WarScores(ctx, store.Pool(), []int64{77})
	require.NoError(t, err)
	assert.Equal(t, map[int64]map[int64]int64{77: {3: 4, 5: 10}}, scores)
	vacio, err := postgres.WarScores(ctx, store.Pool(), nil)
	require.NoError(t, err)
	assert.Empty(t, vacio)
}

// filaConGuerra es una fila de world_events de un tipo: su jugador y el campo
// war de su payload («null» si no lo lleva).
type filaConGuerra struct {
	player uuid.UUID
	war    *clan.WarPoints
}

// guerraEnLosEventos son las filas de world_events del tipo, en orden, con el
// campo war de su payload.
func guerraEnLosEventos(t *testing.T, ctx context.Context, store *postgres.Store, tipo string) []filaConGuerra {
	t.Helper()
	rows, err := store.Pool().Query(ctx,
		`SELECT player_id, (payload->'war'->>'warId')::bigint, (payload->'war'->>'clanId')::bigint,
		        (payload->'war'->>'points')::bigint
		   FROM world_events WHERE event_type = $1 ORDER BY id`, tipo)
	require.NoError(t, err)
	defer rows.Close()
	var out []filaConGuerra
	for rows.Next() {
		var f filaConGuerra
		var warID, clanID, points *int64
		require.NoError(t, rows.Scan(&f.player, &warID, &clanID, &points))
		if warID != nil {
			require.NotNil(t, clanID)
			require.NotNil(t, points)
			f.war = &clan.WarPoints{WarID: *warID, ClanID: *clanID, Points: *points}
		}
		out = append(out, f)
	}
	require.NoError(t, rows.Err())
	return out
}

// conGuerraEnSuPayload son las filas que llevan war.
func conGuerraEnSuPayload(fs []filaConGuerra) []filaConGuerra {
	var out []filaConGuerra
	for _, f := range fs {
		if f.war != nil {
			out = append(out, f)
		}
	}
	return out
}

// INV-CLAN-009 y ADR-045 §4: la captura que puntúa escribe war en una sola fila
// de su evento, la del anterior dueño, y la carga suma esos 10 puntos.
func TestLaCapturaEscribeLaGuerraEnUnaFila(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaConquista(t, ctx, store)
	id, err := c.repo.StartClaim(ctx, c.reclamacion(c.duena, c.libre, nil, "req-1"))
	require.NoError(t, err)
	_, err = c.repo.CaptureTerritory(ctx, postgres.Capture{
		ClaimID: id, TerritoryID: c.libre.ID, TerritoryName: "Este", NewOwner: c.duena.Player.ID,
		NewCityID: c.duena.City.ID, NewBonus: 5, ExpectedVersion: controlEnBase(t, ctx, c.territorios, store, c.libre.ID).Version,
		CapturedAt: c.instanteDeHoy, Tick: 9, AtMs: 301_000,
	})
	require.NoError(t, err)
	antes := len(guerraEnLosEventos(t, ctx, store, "TerritoryControlChanged"))

	duena := c.duena.Player.ID
	id2, err := c.repo.StartClaim(ctx, c.reclamacion(c.rival, c.libre, &duena, "req-2"))
	require.NoError(t, err)
	puntos := &clan.WarPoints{WarID: 77, ClanID: 5, Points: clan.PointsTerritory}
	_, err = c.repo.CaptureTerritory(ctx, postgres.Capture{
		ClaimID: id2, TerritoryID: c.libre.ID, TerritoryName: "Este", NewOwner: c.rival.Player.ID,
		NewCityID: c.rival.City.ID, NewBonus: 5, PrevOwner: duena, PrevCityID: c.duena.City.ID, PrevBonus: 5,
		ExpectedVersion: controlEnBase(t, ctx, c.territorios, store, c.libre.ID).Version,
		CapturedAt:      c.instanteDeHoy, Tick: 12, AtMs: 1_000_000, X: 40, Y: 10, War: puntos,
	})
	require.NoError(t, err)

	filas := guerraEnLosEventos(t, ctx, store, "TerritoryControlChanged")
	require.Len(t, filas, antes+1, "una fila: el anterior dueño y el nuevo")
	conGuerra := conGuerraEnSuPayload(filas)
	require.Len(t, conGuerra, 1)
	assert.Equal(t, duena, conGuerra[0].player)
	assert.Equal(t, puntos, conGuerra[0].war)

	scores, err := postgres.WarScores(ctx, store.Pool(), []int64{77})
	require.NoError(t, err)
	assert.Equal(t, map[int64]map[int64]int64{77: {5: 10}}, scores)
}

// INV-CLAN-009 y ADR-041 §5: la captura de un territorio de clan escribe una
// fila por miembro y otra para quien lo gana; war va sólo en esa última, así que
// los 10 puntos se suman una vez por mucho que tenga el clan.
func TestLaCapturaDeUnClanEscribeLaGuerraEnUnaSolaFila(t *testing.T) {
	store, ctx := newTestStore(t)
	c := nuevaConquista(t, ctx, store)
	clanes, lobos := clanDeLaConquista(t, ctx, store, c, false)
	_, _, _, _, bootstrapper := newRepos(store)
	segundo, err := bootstrapper.Create(ctx, bootstrapRequest("segundoguerra", world.Tile{X: 20, Y: 50}))
	require.NoError(t, err)
	require.NoError(t, clanes.Invite(ctx, postgres.ClanInvite{ClanID: lobos, PlayerID: segundo.Player.ID,
		InvitedBy: c.duena.Player.ID, At: clanEn, ExpiresAt: clanEn.Add(24 * time.Hour)}))
	require.NoError(t, clanes.Join(ctx, postgres.ClanJoin{ClanID: lobos, PlayerID: segundo.Player.ID, MaxMembers: 20, At: clanEn.Add(time.Minute)}))
	ctrl := ganarParaElClan(t, ctx, store, c, lobos)
	antes := len(guerraEnLosEventos(t, ctx, store, "TerritoryControlChanged"))

	inicio := c.reclamacion(c.rival, c.libre, nil, "req-rival")
	inicio.OwnerClanID, inicio.OwnerClanTag, inicio.Tick = lobos, "LOB", 10
	id, err := c.repo.StartClaim(ctx, inicio)
	require.NoError(t, err)
	puntos := &clan.WarPoints{WarID: 78, ClanID: 9, Points: clan.PointsTerritory}
	_, err = c.repo.CaptureTerritory(ctx, postgres.Capture{
		ClaimID: id, TerritoryID: c.libre.ID, TerritoryName: "Este", NewOwner: c.rival.Player.ID,
		NewCityID: c.rival.City.ID, NewBonus: 5, PrevClanID: lobos, PrevClanTag: "LOB",
		ExpectedVersion: ctrl.Version, CapturedAt: c.instanteDeHoy, Tick: 12, AtMs: 1_000_000, X: 40, Y: 10,
		War: puntos,
	})
	require.NoError(t, err)

	filas := guerraEnLosEventos(t, ctx, store, "TerritoryControlChanged")[antes:]
	require.Len(t, filas, 3, "una por miembro de [LOB] y otra para quien lo gana")
	conGuerra := conGuerraEnSuPayload(filas)
	require.Len(t, conGuerra, 1, "war, en una sola fila")
	assert.Equal(t, puntos, conGuerra[0].war)
	assert.Equal(t, filas[2], conGuerra[0], "la fila de quien lo gana (only: counterpart)")

	scores, err := postgres.WarScores(ctx, store.Pool(), []int64{78})
	require.NoError(t, err)
	assert.Equal(t, map[int64]map[int64]int64{78: {9: 10}}, scores)
}

// INV-CLAN-009 y ADR-045 §4: el derribo que puntúa escribe war en el payload de
// su evento, y la carga suma esos 3 puntos; uno sin guerra no lleva el campo.
func TestElDerriboEscribeLaGuerraEnSuPayload(t *testing.T) {
	store, ctx := newTestStore(t)
	a := nuevoAsedio(t, ctx, store)
	sinGuerra := a.casaEn(t, ctx, true)
	require.NoError(t, a.economia.DestroyBuilding(ctx, a.derribo(sinGuerra)))
	// La segunda, con otra clave: casaEn repite «req-casa» y devolvería la primera.
	a.depositar(t, ctx, 30, 3)
	conPuntos, err := a.economia.Build(ctx, "req-casa-guerra", a.jugador.Player.ID, casa(a.ciudad(), 19, 19),
		[]postgres.StockDelta{debito(a.ciudad(), -30, 4)}, 4)
	require.NoError(t, err)
	require.NotEqual(t, sinGuerra, conPuntos)
	require.NoError(t, a.economia.CompleteBuilding(ctx, conPuntos, a.ciudad(), a.jugador.Player.ID, 5, 5))
	d := a.derribo(conPuntos)
	d.War = &clan.WarPoints{WarID: 79, ClanID: 4, Points: clan.PointsBuilding}
	require.NoError(t, a.economia.DestroyBuilding(ctx, d))

	filas := guerraEnLosEventos(t, ctx, store, postgres.EventBuildingDestroyed)
	require.Len(t, filas, 2)
	assert.Nil(t, filas[0].war, "un derribo fuera de una guerra no lleva war")
	assert.Equal(t, d.War, filas[1].war)
	assert.Equal(t, a.jugador.Player.ID, filas[1].player)

	scores, err := postgres.WarScores(ctx, store.Pool(), []int64{79})
	require.NoError(t, err)
	assert.Equal(t, map[int64]map[int64]int64{79: {4: 3}}, scores)
}
