//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// SPEC-FACTIONS §9, integración: el trigger de transición de 000019, la entrada
// y su vencimiento con las rupturas en una transacción (ADR-039 §4), la carga de
// la ciudad con su facción y su entrada, y un fallo inyectado que no deja nada.

// Los id fijos del catálogo de 000002.
const (
	idOrden   = 1
	idCaos    = 2
	idNeutral = 3
)

// rechazoDelTrigger comprueba que un UPDATE lo rechaza el trigger o el CHECK.
func rechazoDelTrigger(t *testing.T, err error, contiene string) {
	t.Helper()
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "se esperaba un rechazo, llegó %v", err)
	assert.Equal(t, "23514", pgErr.Code, "check_violation")
	assert.Contains(t, pgErr.Message+pgErr.ConstraintName, contiene)
}

// INV-PLAYER-013: la facción sólo cambia de NEUTRAL a ORDER o CHAOS, al bando
// de su entrada si la hay, y una entrada pendiente sólo existe sobre un NEUTRAL,
// no cambia y no se retira sin contar.
func TestElTriggerSoloDejaEntrarEnUnBandoDesdeNeutral(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	a, err := bootstrapper.Create(ctx, bootstrapRequest("bandoa", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	b, err := bootstrapper.Create(ctx, bootstrapRequest("bandob", world.Tile{X: 60, Y: 20}))
	require.NoError(t, err)
	pa, pb := a.Player.ID, b.Player.ID
	exec := func(sql string, args ...any) error {
		_, err := store.Pool().Exec(ctx, sql, args...)
		return err
	}
	cuando := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

	rechazoDelTrigger(t, exec(`UPDATE players SET faction_join_id = $2 WHERE id = $1`, pa, idOrden), "players_faction_join_pair")
	rechazoDelTrigger(t, exec(`UPDATE players SET faction_join_id = $2, faction_join_at = $3 WHERE id = $1`, pa, idNeutral, cuando), "INV-PLAYER-013")
	require.NoError(t, exec(`UPDATE players SET faction_join_id = $2, faction_join_at = $3 WHERE id = $1`, pa, idOrden, cuando), "NEUTRAL → entrada en ORDER")
	rechazoDelTrigger(t, exec(`UPDATE players SET faction_join_id = $2 WHERE id = $1`, pa, idCaos), "INV-PLAYER-013")
	rechazoDelTrigger(t, exec(`UPDATE players SET faction_join_at = $2 WHERE id = $1`, pa, cuando.Add(time.Minute)), "INV-PLAYER-013")
	rechazoDelTrigger(t, exec(`UPDATE players SET faction_join_id = NULL, faction_join_at = NULL WHERE id = $1`, pa), "INV-PLAYER-013")
	rechazoDelTrigger(t, exec(`UPDATE players SET faction_id = $2, faction_join_id = NULL, faction_join_at = NULL WHERE id = $1`, pa, idCaos), "INV-PLAYER-013")
	require.NoError(t, exec(`UPDATE players SET faction_id = $2, faction_join_id = NULL, faction_join_at = NULL WHERE id = $1`, pa, idOrden), "la entrada cuenta")
	rechazoDelTrigger(t, exec(`UPDATE players SET faction_id = $2 WHERE id = $1`, pa, idCaos), "INV-PLAYER-013")
	rechazoDelTrigger(t, exec(`UPDATE players SET faction_id = $2 WHERE id = $1`, pa, idNeutral), "INV-PLAYER-013")
	rechazoDelTrigger(t, exec(`UPDATE players SET faction_join_id = $2, faction_join_at = $3 WHERE id = $1`, pa, idCaos, cuando), "INV-PLAYER-013")
	require.NoError(t, exec(`UPDATE players SET last_seen_at = now() WHERE id = $1`, pa), "las demás columnas, sí")

	require.NoError(t, exec(`UPDATE players SET faction_id = $2 WHERE id = $1`, pb, idCaos), "NEUTRAL → CHAOS sin entrada (el trigger no exige el preaviso)")
	rechazoDelTrigger(t, exec(`UPDATE players SET faction_id = $2 WHERE id = $1`, pb, idOrden), "INV-PLAYER-013")
}

// alianzaActiva inserta una alianza vigente entre dos jugadores, con el par
// ordenado, y devuelve el tratado.
func alianzaActiva(t *testing.T, store *postgres.Store, x, y uuid.UUID, tipo diplomacy.Type) diplomacy.Treaty {
	t.Helper()
	ctx := t.Context()
	a, b := diplomacy.CanonicalPair(x, y)
	firmado := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	var id int64
	require.NoError(t, store.Pool().QueryRow(ctx,
		`INSERT INTO treaties (player_a_id, player_b_id, treaty_type, status, allows_garrison, proposed_at, accepted_at)
		 VALUES ($1, $2, $3, 'ACTIVE', $4, $5, $5) RETURNING id`,
		a, b, string(tipo), tipo.GrantsGarrison(), firmado).Scan(&id))
	return diplomacy.Treaty{ID: id, PlayerA: a, PlayerB: b, Type: tipo, Status: diplomacy.StatusActive,
		AllowsGarrison: tipo.GrantsGarrison(), ProposedAt: firmado, AcceptedAt: &firmado}
}

// ADR-039 §3 y §4, INV-DIPLO-003: la entrada se apunta con su evento y es
// idempotente; el vencimiento escribe la facción, borra la entrada y rompe la
// alianza con el bando contrario en una transacción, y un reintento no escribe
// nada más. La ciudad se carga con su facción y su entrada.
func TestLaEntradaYSuVencimientoConLasRupturas(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, _, _, bootstrapper := newRepos(store)
	factions := postgres.NewFactionRepo(store)
	a, err := bootstrapper.Create(ctx, bootstrapRequest("ordenado", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	b, err := bootstrapper.Create(ctx, bootstrapRequest("caotico", world.Tile{X: 60, Y: 20}))
	require.NoError(t, err)
	pa, pb := a.Player.ID, b.Player.ID
	_, err = store.Pool().Exec(ctx, `UPDATE players SET faction_id = $2 WHERE id = $1`, pb, idCaos)
	require.NoError(t, err)
	alianza := alianzaActiva(t, store, pa, pb, diplomacy.TypeAlliance)

	cuenta := time.Date(2026, 11, 15, 12, 15, 0, 0, time.UTC)
	orden := postgres.FactionJoinOrder{PlayerID: pa, RequestID: uuid.NewString(), Faction: player.FactionOrder, At: cuenta, Tick: 7, AtMs: 1}
	require.NoError(t, factions.RequestJoin(ctx, orden))
	require.NoError(t, factions.RequestJoin(ctx, orden), "un reintento con la misma entrada termina sin error")
	assert.Equal(t, 1, eventosDe(t, ctx, store, "FactionJoinOrdered"), "y sin un segundo evento")
	otra := orden
	otra.At = cuenta.Add(time.Minute)
	assert.ErrorIs(t, factions.RequestJoin(ctx, otra), postgres.ErrConflict)

	c, err := cities.GetByOwner(ctx, pa)
	require.NoError(t, err)
	assert.Equal(t, player.FactionNeutral, c.Faction)
	require.NotNil(t, c.FactionJoin)
	assert.Equal(t, player.FactionOrder, c.FactionJoin.Faction)
	assert.Equal(t, cuenta.UnixMilli(), c.FactionJoin.AtMs)

	fin := cuenta.Add(15 * time.Minute)
	rota := alianza
	rota.BreakEffectiveAt = &fin
	fc := postgres.FactionJoinCompletion{
		PlayerID: pa, Faction: player.FactionOrder, At: cuenta, Tick: 9, AtMs: cuenta.UnixMilli(),
		Breaks: []postgres.TreatyWrite{{Treaty: rota, From: diplomacy.StatusActive, Event: "TreatyBreakAnnounced",
			Tick: 9, AtMs: cuenta.UnixMilli(), By: pa}},
	}
	aplicada, err := factions.CompleteJoin(ctx, fc)
	require.NoError(t, err)
	assert.True(t, aplicada)

	c, err = cities.GetByOwner(ctx, pa)
	require.NoError(t, err)
	assert.Equal(t, player.FactionOrder, c.Faction)
	assert.Nil(t, c.FactionJoin)
	var ruptura *time.Time
	var estado string
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT status, break_effective_at FROM treaties WHERE id = $1`, alianza.ID).
		Scan(&estado, &ruptura))
	assert.Equal(t, "ACTIVE", estado, "sigue vigente durante su preaviso")
	require.NotNil(t, ruptura)
	assert.True(t, ruptura.Equal(fin))
	var autor string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT payload->>'byPlayerId' FROM world_events WHERE event_type = 'TreatyBreakAnnounced'`).Scan(&autor))
	assert.Equal(t, pa.String(), autor, "la rompe quien entra (ADR-028 §3)")
	assert.Equal(t, 1, eventosDe(t, ctx, store, "FactionJoined"))

	aplicada, err = factions.CompleteJoin(ctx, fc)
	require.NoError(t, err, "un reintento encuentra la facción escrita")
	assert.False(t, aplicada)
	assert.Equal(t, 1, eventosDe(t, ctx, store, "TreatyBreakAnnounced"), "sin escribir nada más")
	assert.Equal(t, 1, eventosDe(t, ctx, store, "FactionJoined"))
}

// ADR-039 §4: una ruptura sobre un tratado que ya no está vigente sin preaviso se
// omite sin error ni evento; y un vencimiento sin la entrada esperada es
// ErrConflict y no escribe nada.
func TestElVencimientoEsTolerante(t *testing.T) {
	store, ctx := newTestStore(t)
	_, _, _, _, bootstrapper := newRepos(store)
	factions := postgres.NewFactionRepo(store)
	a, err := bootstrapper.Create(ctx, bootstrapRequest("tolerante", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	b, err := bootstrapper.Create(ctx, bootstrapRequest("rompedor", world.Tile{X: 60, Y: 20}))
	require.NoError(t, err)
	pa, pb := a.Player.ID, b.Player.ID
	pacto := alianzaActiva(t, store, pa, pb, diplomacy.TypeNonAggression)
	ya := time.Date(2026, 11, 15, 12, 20, 0, 0, time.UTC)
	_, err = store.Pool().Exec(ctx, `UPDATE treaties SET break_effective_at = $2 WHERE id = $1`, pacto.ID, ya)
	require.NoError(t, err)

	cuenta := time.Date(2026, 11, 15, 12, 15, 0, 0, time.UTC)
	fc := postgres.FactionJoinCompletion{PlayerID: pa, Faction: player.FactionChaos, At: cuenta, Tick: 1, AtMs: 1}
	_, err = factions.CompleteJoin(ctx, fc)
	assert.ErrorIs(t, err, postgres.ErrConflict, "sin la entrada, no hay nada que vencer")

	require.NoError(t, factions.RequestJoin(ctx, postgres.FactionJoinOrder{PlayerID: pa, Faction: player.FactionChaos, At: cuenta, Tick: 1, AtMs: 1}))
	otra := ya.Add(time.Hour)
	rota := pacto
	rota.BreakEffectiveAt = &otra
	fc.Breaks = []postgres.TreatyWrite{{Treaty: rota, From: diplomacy.StatusActive, Event: "TreatyBreakAnnounced", Tick: 1, AtMs: 1, By: pa}}
	aplicada, err := factions.CompleteJoin(ctx, fc)
	require.NoError(t, err)
	assert.True(t, aplicada)
	var ruptura time.Time
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT break_effective_at FROM treaties WHERE id = $1`, pacto.ID).Scan(&ruptura))
	assert.True(t, ruptura.Equal(ya), "el preaviso que ya tenía no cambia")
	assert.Equal(t, 0, eventosDe(t, ctx, store, "TreatyBreakAnnounced"))
}

// INV-DIPLO-003 y ADR-039 §4: si la transacción del vencimiento falla, no queda
// ni la facción escrita ni la ruptura.
func TestUnFalloEnElVencimientoNoDejaNadaAMedias(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, _, _, bootstrapper := newRepos(store)
	factions := postgres.NewFactionRepo(store)
	a, err := bootstrapper.Create(ctx, bootstrapRequest("fallido", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	b, err := bootstrapper.Create(ctx, bootstrapRequest("contrario", world.Tile{X: 60, Y: 20}))
	require.NoError(t, err)
	pa, pb := a.Player.ID, b.Player.ID
	_, err = store.Pool().Exec(ctx, `UPDATE players SET faction_id = $2 WHERE id = $1`, pb, idOrden)
	require.NoError(t, err)
	alianza := alianzaActiva(t, store, pa, pb, diplomacy.TypeAlliance)
	cuenta := time.Date(2026, 11, 15, 12, 15, 0, 0, time.UTC)
	require.NoError(t, factions.RequestJoin(ctx, postgres.FactionJoinOrder{PlayerID: pa, Faction: player.FactionChaos, At: cuenta, Tick: 1, AtMs: 1}))

	// El fallo: el último INSERT de la transacción, FactionJoined, no cabe.
	_, err = store.Pool().Exec(ctx, `ALTER TABLE world_events ADD CONSTRAINT test_sin_faction_joined
		CHECK (event_type <> 'FactionJoined') NOT VALID`)
	require.NoError(t, err)
	// t.Context() ya está cancelado cuando corre la limpieza: con él el DROP no
	// llegaba a la base y la restricción contaminaba la pasada siguiente.
	t.Cleanup(func() {
		_, err := store.Pool().Exec(context.Background(), `ALTER TABLE world_events DROP CONSTRAINT IF EXISTS test_sin_faction_joined`)
		require.NoError(t, err)
	})

	fin := cuenta.Add(15 * time.Minute)
	rota := alianza
	rota.BreakEffectiveAt = &fin
	_, err = factions.CompleteJoin(ctx, postgres.FactionJoinCompletion{
		PlayerID: pa, Faction: player.FactionChaos, At: cuenta, Tick: 2, AtMs: 2,
		Breaks: []postgres.TreatyWrite{{Treaty: rota, From: diplomacy.StatusActive, Event: "TreatyBreakAnnounced", Tick: 2, AtMs: 2, By: pa}},
	})
	require.Error(t, err)

	c, err := cities.GetByOwner(ctx, pa)
	require.NoError(t, err)
	assert.Equal(t, player.FactionNeutral, c.Faction, "la facción no se escribió")
	assert.NotNil(t, c.FactionJoin, "la entrada sigue pendiente: el primer tick tras un reinicio la vuelve a aplicar")
	var ruptura *time.Time
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT break_effective_at FROM treaties WHERE id = $1`, alianza.ID).Scan(&ruptura))
	assert.Nil(t, ruptura, "ni la ruptura")
	assert.Equal(t, 0, eventosDe(t, ctx, store, "TreatyBreakAnnounced"))
}
