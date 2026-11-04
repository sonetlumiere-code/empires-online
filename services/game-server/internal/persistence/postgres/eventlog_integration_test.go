//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Persistencia del registro (SPEC-EVENT-LOG §7 y §9 «integration», ADR-028).

func registro(store *postgres.Store, porPagina int) *postgres.EventLogRepo {
	return postgres.NewEventLogRepo(store, postgres.EventLogConfig{
		MaxAge: 7 * 24 * time.Hour, PageSize: porPagina, GroupGap: 10 * time.Minute,
	})
}

func (e escenarioSaqueo) matar(t *testing.T, ctx context.Context, store *postgres.Store, atMs int64) {
	t.Helper()
	require.NoError(t, store.InTx(ctx, func(tx pgx.Tx) error {
		_, err := e.units.Kill(ctx, tx, postgres.Kill{
			VictimID: e.victima.Units[0].ID, VictimPlayer: e.victima.Player.ID,
			KillerID: e.saqueador.Units[0].ID, KillerPlayer: e.saqueador.Player.ID,
			At: world.Tile{X: 22, Y: 20}, Tick: 40, VictimType: unit.TypeVillager, AtMs: atMs,
		})
		return err
	}))
}

type filaDeEvento struct {
	player, counterpart *uuid.UUID
	payload             map[string]any
}

func ultimoEvento(t *testing.T, ctx context.Context, store *postgres.Store, tipo string) filaDeEvento {
	t.Helper()
	var (
		f   filaDeEvento
		raw []byte
	)
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT player_id, counterpart_player_id, payload FROM world_events
		  WHERE event_type = $1 ORDER BY id DESC LIMIT 1`, tipo).Scan(&f.player, &f.counterpart, &raw))
	require.NoError(t, json.Unmarshal(raw, &f.payload))
	return f
}

// ADR-028 §2 y §3: cada suceso del registro nombra a los dos jugadores sin
// cambiar lo que significa player_id, y lleva el instante de la simulación.
func TestCadaSucesoNombraALosDosJugadores(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoSaqueo(t, ctx, store, 100)
	victima, saqueador := e.victima.Player.ID, e.saqueador.Player.ID

	p := e.golpe(5, 0, 2)
	p.AtMs = 1_800_000_000_123
	_, err := e.escribir(t, ctx, store, p)
	require.NoError(t, err)
	e.matar(t, ctx, store, 1_800_000_000_456)
	require.NoError(t, e.units.Attacked(ctx, store.Pool(), postgres.Attacked{
		AttackerID: e.saqueador.Units[0].ID, AttackerPlayer: saqueador,
		TargetID: e.victima.Units[1].ID, TargetPlayer: victima, AtMs: 1_800_000_000_789,
	}))

	for tipo, at := range map[string]float64{"CityPillaged": 1_800_000_000_123, "UnitKilled": 1_800_000_000_456, "UnitAttacked": 1_800_000_000_789} {
		f := ultimoEvento(t, ctx, store, tipo)
		require.NotNil(t, f.player, tipo)
		require.NotNil(t, f.counterpart, tipo)
		assert.Equal(t, victima, *f.player, "%s: player_id sigue siendo la víctima", tipo)
		assert.Equal(t, saqueador, *f.counterpart, "%s: el otro es el autor", tipo)
		assert.Equal(t, at, f.payload["atMs"], tipo)
	}
	assert.Equal(t, "VILLAGER", ultimoEvento(t, ctx, store, "UnitKilled").payload["unitType"])

	// Un tratado: quien propuso y el otro firmante; la ruptura dice quién rompió.
	treaties := postgres.NewTreatyRepo(store)
	a, b := diplomacy.CanonicalPair(victima, saqueador)
	quien := saqueador
	o := diplomacy.Treaty{
		PlayerA: a, PlayerB: b, Type: diplomacy.TypeNonAggression, Status: diplomacy.StatusProposed,
		ProposedAt: time.UnixMilli(time.Now().UnixMilli()).UTC(), ProposedBy: &quien, RequestID: uuid.NewString(),
	}
	o.ID, err = treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Insert: true, Treaty: o, From: diplomacy.StatusProposed, Event: "TreatyProposed", AtMs: 1, By: saqueador})
	require.NoError(t, err)
	ahora := time.UnixMilli(time.Now().UnixMilli()).UTC()
	activo, err := o.TransitionTo(diplomacy.StatusActive, ahora)
	require.NoError(t, err)
	_, err = treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Treaty: activo, From: diplomacy.StatusProposed, Event: "TreatyAccepted", AtMs: 2, By: victima})
	require.NoError(t, err)
	fin := ahora.Add(15 * time.Minute)
	roto := activo
	roto.BreakEffectiveAt = &fin
	_, err = treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Treaty: roto, From: diplomacy.StatusActive, Event: "TreatyBreakAnnounced", AtMs: 3, By: victima})
	require.NoError(t, err)

	for _, tipo := range []string{"TreatyProposed", "TreatyAccepted", "TreatyBreakAnnounced"} {
		f := ultimoEvento(t, ctx, store, tipo)
		assert.Equal(t, saqueador, *f.player, "%s: player_id sigue siendo quien propuso", tipo)
		assert.Equal(t, victima, *f.counterpart, tipo)
	}
	ruptura := ultimoEvento(t, ctx, store, "TreatyBreakAnnounced")
	assert.Equal(t, victima.String(), ruptura.payload["byPlayerId"])
	assert.Equal(t, "ACTIVE", ruptura.payload["from"])
	_, tieneFrom := ultimoEvento(t, ctx, store, "TreatyProposed").payload["from"]
	assert.False(t, tieneFrom, "una oferta nueva no viene de ningún estado")
}

func entradaDe(t *testing.T, page protocol.EventLogPagePayload, kind string) protocol.EventLogEntry {
	t.Helper()
	for _, e := range page.Entries {
		if e.Kind == kind {
			return e
		}
	}
	require.Failf(t, "falta la entrada", "%s en %+v", kind, page.Entries)
	return protocol.EventLogEntry{}
}

// SPEC-EVENT-LOG §4.1 e INV-SEC-009: la víctima ve lo que perdió, el saqueador
// lo que hizo, cada uno con la ciudad del otro, y un tercero no ve nada; ni el
// saqueo ni el tratado de los otros dos.
func TestElRegistroCuentaCadaLadoYNadaAUnTercero(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoSaqueo(t, ctx, store, 100)
	_, _, _, _, bootstrapper := newRepos(store)
	tercero, err := bootstrapper.Create(ctx, bootstrapRequest("tercero", world.Tile{X: 20, Y: 60}))
	require.NoError(t, err)

	for i, version := range []int64{2, 3, 4} {
		p := e.golpe(5, 0, version)
		p.AtMs = 1_800_000_000_000 + int64(i)*1_500
		_, err := e.escribir(t, ctx, store, p)
		require.NoError(t, err)
	}
	e.matar(t, ctx, store, 1_800_000_010_000)
	treaties := postgres.NewTreatyRepo(store)
	a, b := diplomacy.CanonicalPair(e.victima.Player.ID, e.saqueador.Player.ID)
	quien := e.saqueador.Player.ID
	_, err = treaties.ApplyTreaty(ctx, postgres.TreatyWrite{Insert: true, Event: "TreatyProposed", From: diplomacy.StatusProposed, By: quien, AtMs: 1_800_000_020_000,
		Treaty: diplomacy.Treaty{PlayerA: a, PlayerB: b, Type: diplomacy.TypeNonAggression, Status: diplomacy.StatusProposed,
			ProposedAt: time.UnixMilli(time.Now().UnixMilli()).UTC(), ProposedBy: &quien, RequestID: uuid.NewString()}})
	require.NoError(t, err)

	r := registro(store, 50)
	victima, _, err := r.Page(ctx, e.victima.Player.ID, 0)
	require.NoError(t, err)
	require.Len(t, victima.Entries, 3)
	saqueo := entradaDe(t, victima, "PILLAGED")
	assert.Equal(t, 3, saqueo.Count)
	require.NotNil(t, saqueo.Amount)
	assert.Equal(t, int64(15), *saqueo.Amount)
	assert.Equal(t, int64(1_800_000_000_000), saqueo.FirstAtMs)
	assert.Equal(t, int64(1_800_000_003_000), saqueo.LastAtMs)
	require.NotNil(t, saqueo.OtherCityName)
	assert.Equal(t, "saqueadorpolis", *saqueo.OtherCityName)
	assert.Equal(t, e.saqueador.Player.ID.String(), saqueo.OtherPlayerID)
	assert.Equal(t, "VILLAGER", entradaDe(t, victima, "UNIT_LOST").UnitType)
	assert.Equal(t, "NON_AGGRESSION", entradaDe(t, victima, "TREATY_OFFERED").TreatyType)
	assert.Zero(t, victima.SeenUpToID)
	assert.Nil(t, victima.NextBeforeID, "no hay más páginas")

	saqueador, _, err := r.Page(ctx, e.saqueador.Player.ID, 0)
	require.NoError(t, err)
	require.Len(t, saqueador.Entries, 2, "su propia oferta no le cuenta nada")
	assert.Equal(t, int64(15), *entradaDe(t, saqueador, "PILLAGE_DONE").Amount)
	assert.Equal(t, "saqueadapolis", *entradaDe(t, saqueador, "ENEMY_KILLED").OtherCityName)

	ajeno, _, err := r.Page(ctx, tercero.Player.ID, 0)
	require.NoError(t, err)
	assert.Empty(t, ajeno.Entries)
}

// insertarEvento escribe una fila del registro a mano, con su instante de
// escritura, para fijar lo asentado y la ventana de días.
func insertarEvento(t *testing.T, ctx context.Context, db postgres.DB, victima, autor uuid.UUID, hace time.Duration) int64 {
	t.Helper()
	var id int64
	require.NoError(t, db.QueryRow(ctx,
		`INSERT INTO world_events (event_type, tick, player_id, counterpart_player_id, payload, occurred_at)
		 VALUES ('CityPillaged', 1, $1, $2, '{"amount": 5}', now() - make_interval(secs => $3))
		 RETURNING id`, victima, autor, hace.Seconds()).Scan(&id))
	return id
}

// ADR-028 §6: sólo lo asentado sube el tope marcable, y el cursor nunca
// retrocede.
func TestElTopeMarcableSoloCubreLoAsentado(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoSaqueo(t, ctx, store, 100)
	victima, autor := e.victima.Player.ID, e.saqueador.Player.ID
	r := registro(store, 50)

	viejo := insertarEvento(t, ctx, store.Pool(), victima, autor, time.Minute)
	insertarEvento(t, ctx, store.Pool(), victima, autor, time.Second)
	_, tope, err := r.Page(ctx, victima, 0)
	require.NoError(t, err)
	assert.Equal(t, viejo, tope, "la fila de hace un segundo no está asentada")
	_, tope, err = r.Page(ctx, autor, 0)
	require.NoError(t, err)
	assert.Equal(t, viejo, tope, "el autor la tiene por counterpart_player_id")

	require.NoError(t, r.MarkSeen(ctx, victima, viejo))
	require.NoError(t, r.MarkSeen(ctx, victima, viejo-1))
	page, _, err := r.Page(ctx, victima, 0)
	require.NoError(t, err)
	assert.Equal(t, viejo, page.SeenUpToID, "una marca menor no lo hace retroceder")
}

// INV-PERSIST-006: dos transacciones confirman en orden inverso al de sus id.
// La de id mayor ya se ve al servir la página; marcar hasta ella la escondería
// para siempre, y el tope marcable lo impide: al confirmarse la otra, sale como
// nueva.
func TestUnaFilaQueSeConfirmaTardeNoQuedaEscondida(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoSaqueo(t, ctx, store, 100)
	victima, autor := e.victima.Player.ID, e.saqueador.Player.ID
	r := registro(store, 50)
	insertarEvento(t, ctx, store.Pool(), victima, autor, time.Hour)

	lenta, err := store.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = lenta.Rollback(ctx) }()
	tarde := insertarEvento(t, ctx, lenta, victima, autor, 0)
	rapida := insertarEvento(t, ctx, store.Pool(), victima, autor, 0)
	require.Less(t, tarde, rapida)

	page, tope, err := r.Page(ctx, victima, 0)
	require.NoError(t, err)
	assert.Equal(t, rapida, page.Entries[0].ID, "la rápida ya se ve")
	require.Less(t, tope, tarde, "el tope no llega a lo que aún puede confirmarse por debajo")
	require.NoError(t, r.MarkSeen(ctx, victima, min(page.Entries[0].ID, tope)))

	require.NoError(t, lenta.Commit(ctx))
	page, _, err = r.Page(ctx, victima, 0)
	require.NoError(t, err)
	assert.Greater(t, tarde, page.SeenUpToID, "la fila tardía sigue sin ver")
	var nuevos int
	for _, en := range page.Entries {
		if en.ID > page.SeenUpToID {
			nuevos += en.Count
		}
	}
	assert.Equal(t, 2, nuevos, "salen las dos filas recientes, la tardía incluida")
}

// ADR-028 §5: la página se corta por filas; la siguiente sigue justo donde quedó
// y la última no tiene nextBeforeId. Lo más viejo que la ventana no sale.
func TestLaPaginaSeCortaYLaSiguienteSigueDondeQuedo(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoSaqueo(t, ctx, store, 100)
	victima, autor := e.victima.Player.ID, e.saqueador.Player.ID
	r := registro(store, 2)

	insertarEvento(t, ctx, store.Pool(), victima, autor, 8*24*time.Hour) // fuera de la ventana
	var ids []int64
	for _, hace := range []time.Duration{3 * time.Hour, 2 * time.Hour, time.Hour} {
		ids = append(ids, insertarEvento(t, ctx, store.Pool(), victima, autor, hace))
	}

	primera, _, err := r.Page(ctx, victima, 0)
	require.NoError(t, err)
	require.Len(t, primera.Entries, 2, "tres horas separadas son tres entradas; caben dos")
	assert.Equal(t, ids[2], primera.Entries[0].ID)
	require.NotNil(t, primera.NextBeforeID)
	assert.Equal(t, ids[1], *primera.NextBeforeID)

	segunda, _, err := r.Page(ctx, victima, *primera.NextBeforeID)
	require.NoError(t, err)
	require.Len(t, segunda.Entries, 1)
	assert.Equal(t, ids[0], segunda.Entries[0].ID)
	assert.Nil(t, segunda.NextBeforeID, "la de hace ocho días no cuenta")
}

// Las filas de muertes y saqueos anteriores a la migración 000010 no tienen
// counterpart_player_id: la víctima las ve igual, con el autor que ya nombraba
// su payload; el autor no, porque nada en la fila le apunta (INV-SEC-009).
func TestUnaFilaAnteriorAlRegistroSeLeeConElAutorDeSuPayload(t *testing.T) {
	store, ctx := newTestStore(t)
	e := nuevoSaqueo(t, ctx, store, 100)
	victima, autor := e.victima.Player.ID, e.saqueador.Player.ID
	_, err := store.Pool().Exec(ctx,
		`INSERT INTO world_events (event_type, tick, player_id, payload)
		 VALUES ('CityPillaged', 1, $1, jsonb_build_object('amount', 5, 'attackerPlayerId', $2::text))`,
		victima, autor.String())
	require.NoError(t, err)

	r := registro(store, 50)
	page, _, err := r.Page(ctx, victima, 0)
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "PILLAGED", page.Entries[0].Kind)
	assert.Equal(t, autor.String(), page.Entries[0].OtherPlayerID)
	assert.Equal(t, "saqueadorpolis", *page.Entries[0].OtherCityName)

	ajena, _, err := r.Page(ctx, autor, 0)
	require.NoError(t, err)
	assert.Empty(t, ajena.Entries)
}
