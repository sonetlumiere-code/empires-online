//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/message"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Persistencia de los mensajes entre jugadores (SPEC-MESSAGES §7 y §9
// «integration», ADR-038).

// conversadores son tres jugadores con su ciudad: a y b se escriben, c es el
// tercero que no debe ver nada.
type conversadores struct {
	a, b, c      uuid.UUID
	cityA, cityB int64
	nameA, nameB string
	repo         *postgres.MessageRepo
}

func nuevosConversadores(t *testing.T, ctx context.Context, store *postgres.Store, porPagina int) conversadores {
	t.Helper()
	_, _, _, _, bootstrapper := newRepos(store)
	a, err := bootstrapper.Create(ctx, bootstrapRequest("ana", world.Tile{X: 10, Y: 10}))
	require.NoError(t, err)
	b, err := bootstrapper.Create(ctx, bootstrapRequest("bruno", world.Tile{X: 40, Y: 40}))
	require.NoError(t, err)
	c, err := bootstrapper.Create(ctx, bootstrapRequest("carla", world.Tile{X: 70, Y: 70}))
	require.NoError(t, err)
	return conversadores{
		a: a.Player.ID, b: b.Player.ID, c: c.Player.ID,
		cityA: a.City.ID, cityB: b.City.ID, nameA: a.City.Name, nameB: b.City.Name,
		repo: postgres.NewMessageRepo(store, postgres.MessagesConfig{MaxAge: 30 * 24 * time.Hour, PageSize: porPagina}),
	}
}

func sinLimite() (func(), bool) { return func() {}, true }

func (cv conversadores) escribir(t *testing.T, ctx context.Context, from, to uuid.UUID, body string) message.Sent {
	t.Helper()
	sent, err := cv.repo.Send(ctx, from, to, body, uuid.New(), sinLimite)
	require.NoError(t, err)
	return sent
}

// La candado por pareja de ADR-038 §3, tomado a mano desde el test: la misma
// expresión que lockPair.
func candadoDePareja(t *testing.T, ctx context.Context, tx pgx.Tx, a, b uuid.UUID) {
	t.Helper()
	low, high := message.Pair(a, b)
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1297303379, hashtext($1::text || $2::text))`, low.String(), high.String())
	require.NoError(t, err)
}

func filasDeMensajes(t *testing.T, ctx context.Context, store *postgres.Store) int {
	t.Helper()
	var n int
	require.NoError(t, store.Pool().QueryRow(ctx, `SELECT count(*) FROM player_messages`).Scan(&n))
	return n
}

// Un mensaje se guarda una vez y cada lado lo ve con «el otro» nombrado por su
// ciudad; la bandeja cuenta los no leídos de quien recibe y lo leído no
// retrocede (RN-MSG-008, INV-PERSIST-007).
func TestUnMensajeSeVeDesdeLosDosLadosYLoLeidoSoloAvanza(t *testing.T) {
	store, ctx := newTestStore(t)
	cv := nuevosConversadores(t, ctx, store, 50)

	m1 := cv.escribir(t, ctx, cv.a, cv.b, "¿60 de vino por 200 de piedra?")
	require.Positive(t, m1.ID)
	assert.False(t, m1.Duplicate)
	require.NotNil(t, m1.FromCity)
	require.NotNil(t, m1.ToCity)
	assert.Equal(t, cv.nameA, m1.FromCity.Name)
	assert.Equal(t, cv.nameB, m1.ToCity.Name)
	m2 := cv.escribir(t, ctx, cv.b, cv.a, "hecho")
	m3 := cv.escribir(t, ctx, cv.a, cv.b, "te mando la caravana")
	require.Greater(t, m2.ID, m1.ID)
	require.Greater(t, m3.ID, m2.ID)

	desdeB, err := cv.repo.Page(ctx, cv.b, cv.a, 0)
	require.NoError(t, err)
	require.Len(t, desdeB.Messages, 3)
	assert.Equal(t, []int64{m1.ID, m2.ID, m3.ID}, []int64{desdeB.Messages[0].ID, desdeB.Messages[1].ID, desdeB.Messages[2].ID},
		"de más antiguo a más reciente")
	for _, m := range desdeB.Messages {
		assert.Equal(t, cv.a.String(), m.OtherPlayerID, "el otro de b es a")
		require.NotNil(t, m.OtherCityName)
		assert.Equal(t, cv.nameA, *m.OtherCityName)
	}
	assert.Equal(t, cv.b.String(), desdeB.Messages[0].ToPlayerID)
	assert.Equal(t, cv.a.String(), desdeB.Messages[1].ToPlayerID)
	assert.Zero(t, desdeB.ReadUpToID)
	assert.Nil(t, desdeB.NextBeforeID)

	bandejaB, err := cv.repo.Inbox(ctx, cv.b)
	require.NoError(t, err)
	require.Len(t, bandejaB.Conversations, 1)
	conv := bandejaB.Conversations[0]
	assert.Equal(t, cv.a.String(), conv.OtherPlayerID)
	assert.Equal(t, 2, conv.Unread, "los dos de a; el suyo no cuenta")
	assert.Equal(t, "te mando la caravana", conv.LastBody)
	assert.False(t, conv.LastFromMe)
	require.NotNil(t, conv.OtherCityID)
	assert.Equal(t, cv.cityA, *conv.OtherCityID)

	bandejaA, err := cv.repo.Inbox(ctx, cv.a)
	require.NoError(t, err)
	require.Len(t, bandejaA.Conversations, 1)
	assert.Equal(t, 1, bandejaA.Conversations[0].Unread)
	assert.True(t, bandejaA.Conversations[0].LastFromMe)

	require.NoError(t, cv.repo.MarkRead(ctx, cv.b, cv.a, m3.ID))
	require.NoError(t, cv.repo.MarkRead(ctx, cv.b, cv.a, m1.ID))
	bandejaB, err = cv.repo.Inbox(ctx, cv.b)
	require.NoError(t, err)
	assert.Zero(t, bandejaB.Conversations[0].Unread)
	desdeB, err = cv.repo.Page(ctx, cv.b, cv.a, 0)
	require.NoError(t, err)
	assert.Equal(t, m3.ID, desdeB.ReadUpToID, "una marca menor no lo hace retroceder")
}

// INV-SEC-010: un tercero no ve nada de la conversación de los otros dos.
func Test_INV_SEC_010_UnTerceroNoVeLaConversacionDeOtros(t *testing.T) {
	store, ctx := newTestStore(t)
	cv := nuevosConversadores(t, ctx, store, 50)
	cv.escribir(t, ctx, cv.a, cv.b, "secreto")

	bandeja, err := cv.repo.Inbox(ctx, cv.c)
	require.NoError(t, err)
	assert.Empty(t, bandeja.Conversations)
	for _, otro := range []uuid.UUID{cv.a, cv.b} {
		page, err := cv.repo.Page(ctx, cv.c, otro, 0)
		require.NoError(t, err)
		assert.Empty(t, page.Messages, "c con %s no tiene conversación", otro)
	}
}

// INV-SEC-007: un requestId repetido devuelve el mensaje que ya existía.
func TestUnRequestIdRepetidoNoGuardaOtroMensaje(t *testing.T) {
	store, ctx := newTestStore(t)
	cv := nuevosConversadores(t, ctx, store, 50)
	req := uuid.New()

	primero, err := cv.repo.Send(ctx, cv.a, cv.b, "hola", req, sinLimite)
	require.NoError(t, err)
	otraVez, err := cv.repo.Send(ctx, cv.a, cv.b, "hola", req, sinLimite)
	require.NoError(t, err)
	assert.True(t, otraVez.Duplicate)
	assert.Equal(t, primero.ID, otraVez.ID)
	assert.Equal(t, primero.SentAtMs, otraVez.SentAtMs)
	aOtro, err := cv.repo.Send(ctx, cv.a, cv.c, "otro texto", req, sinLimite)
	require.NoError(t, err)
	assert.Equal(t, primero.ID, aOtro.ID, "el requestId es del remitente: el reintento es el mensaje original")
	assert.Equal(t, cv.b, aOtro.To)
	assert.Equal(t, 1, filasDeMensajes(t, ctx, store))

	ex, found, err := cv.repo.Existing(ctx, cv.a, req)
	require.NoError(t, err)
	require.True(t, found, "el reintento sin ritmo encuentra el original")
	assert.Equal(t, primero.ID, ex.ID)
	require.NotNil(t, ex.ToCity)
	assert.Equal(t, cv.nameB, ex.ToCity.Name)
	_, found, err = cv.repo.Existing(ctx, cv.a, uuid.New())
	require.NoError(t, err)
	assert.False(t, found)
	_, found, err = cv.repo.Existing(ctx, cv.b, req)
	require.NoError(t, err)
	assert.False(t, found, "el requestId es de cada remitente")

	_, err = cv.repo.Send(ctx, cv.b, cv.a, "de b, con el mismo requestId", req, sinLimite)
	require.NoError(t, err, "otro remitente puede usar el mismo requestId")
	assert.Equal(t, 2, filasDeMensajes(t, ctx, store))
}

// INV-SEC-011 y RN-MSG-004: con un bloqueo en cualquier sentido no se guarda
// nada, y el motivo dice de qué lado está.
func Test_INV_SEC_011_NingunMensajeCruzaUnBloqueo(t *testing.T) {
	store, ctx := newTestStore(t)
	cv := nuevosConversadores(t, ctx, store, 50)

	require.NoError(t, cv.repo.Block(ctx, cv.b, cv.a, true))
	require.NoError(t, cv.repo.Block(ctx, cv.b, cv.a, true), "idempotente")
	_, err := cv.repo.Send(ctx, cv.a, cv.b, "hola", uuid.New(), sinLimite)
	assert.ErrorIs(t, err, message.ErrBlockedByOther)
	_, err = cv.repo.Send(ctx, cv.b, cv.a, "hola", uuid.New(), sinLimite)
	assert.ErrorIs(t, err, message.ErrBlockedByMe)
	assert.Zero(t, filasDeMensajes(t, ctx, store))

	bandeja, err := cv.repo.Inbox(ctx, cv.b)
	require.NoError(t, err)
	require.Len(t, bandeja.Blocked, 1)
	assert.Equal(t, cv.a.String(), bandeja.Blocked[0].PlayerID)
	require.NotNil(t, bandeja.Blocked[0].CityName)
	assert.Equal(t, cv.nameA, *bandeja.Blocked[0].CityName)
	page, err := cv.repo.Page(ctx, cv.b, cv.a, 0)
	require.NoError(t, err)
	assert.True(t, page.BlockedByMe)
	page, err = cv.repo.Page(ctx, cv.a, cv.b, 0)
	require.NoError(t, err)
	assert.False(t, page.BlockedByMe, "a no sabe por la página que b le bloqueó")

	require.NoError(t, cv.repo.Block(ctx, cv.b, cv.a, false))
	require.NoError(t, cv.repo.Block(ctx, cv.b, cv.a, false), "idempotente")
	cv.escribir(t, ctx, cv.a, cv.b, "hola otra vez")
	assert.Equal(t, 1, filasDeMensajes(t, ctx, store))
}

// INV-SEC-011: un envío que corre a la vez que un bloqueo espera al candado de
// la pareja y ve el bloqueo; no se cuela con el bloqueo a medio confirmar.
func TestUnEnvioQueEsperaAlCandadoVeElBloqueo(t *testing.T) {
	store, ctx := newTestStore(t)
	cv := nuevosConversadores(t, ctx, store, 50)

	tx, err := store.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	candadoDePareja(t, ctx, tx, cv.a, cv.b)

	resultado := make(chan error, 1)
	go func() {
		_, err := cv.repo.Send(ctx, cv.a, cv.b, "¿me oyes?", uuid.New(), sinLimite)
		resultado <- err
	}()
	select {
	case err := <-resultado:
		t.Fatalf("el envío no esperó al candado de la pareja: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	_, err = tx.Exec(ctx, `INSERT INTO player_blocks (player_id, blocked_player_id) VALUES ($1, $2)`, cv.b, cv.a)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case err := <-resultado:
		assert.ErrorIs(t, err, message.ErrBlockedByOther)
	case <-time.After(10 * time.Second):
		t.Fatal("el envío no terminó al soltar el candado")
	}
	assert.Zero(t, filasDeMensajes(t, ctx, store))
}

// INV-PERSIST-007: con el candado de la pareja tomado por un envío que aún no
// confirmó, otro envío de la misma conversación no puede confirmar antes: su id,
// mayor, no se sirve nunca por delante de uno menor sin confirmar. Otra pareja
// no espera.
func Test_INV_PERSIST_007_LosIdDeUnaConversacionSeConfirmanEnOrden(t *testing.T) {
	store, ctx := newTestStore(t)
	cv := nuevosConversadores(t, ctx, store, 50)

	tx, err := store.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	candadoDePareja(t, ctx, tx, cv.a, cv.b)
	low, high := message.Pair(cv.a, cv.b)
	var lento int64
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO player_messages (sender_player_id, recipient_player_id, player_low, player_high, body, request_id)
		 VALUES ($1, $2, $3, $4, 'el lento', $5) RETURNING id`, cv.a, cv.b, low, high, uuid.New()).Scan(&lento))

	otraPareja := cv.escribir(t, ctx, cv.a, cv.c, "otra conversación no espera")
	assert.Greater(t, otraPareja.ID, lento)

	resultado := make(chan message.Sent, 1)
	go func() {
		sent, err := cv.repo.Send(ctx, cv.b, cv.a, "el rápido", uuid.New(), sinLimite)
		if err != nil {
			t.Errorf("envío: %v", err)
		}
		resultado <- sent
	}()
	select {
	case <-resultado:
		t.Fatal("el segundo envío confirmó con el primero de su pareja sin confirmar")
	case <-time.After(300 * time.Millisecond):
	}
	page, err := cv.repo.Page(ctx, cv.b, cv.a, 0)
	require.NoError(t, err)
	assert.Empty(t, page.Messages, "nada servido mientras el lento no confirma")

	require.NoError(t, tx.Commit(ctx))
	var rapido message.Sent
	select {
	case rapido = <-resultado:
	case <-time.After(10 * time.Second):
		t.Fatal("el segundo envío no terminó")
	}
	assert.Greater(t, rapido.ID, lento)
	page, err = cv.repo.Page(ctx, cv.b, cv.a, 0)
	require.NoError(t, err)
	require.Len(t, page.Messages, 2)
	assert.Equal(t, lento, page.Messages[0].ID)
}

// RN-MSG-005: sólo una conversación nueva pide una plaza del límite por hora, y
// sin plaza no se guarda nada.
func TestSoloUnaConversacionNuevaGastaElLimiteDeConversaciones(t *testing.T) {
	store, ctx := newTestStore(t)
	cv := nuevosConversadores(t, ctx, store, 50)

	llamadas := 0
	sinPlaza := func() (func(), bool) { llamadas++; return nil, false }
	_, err := cv.repo.Send(ctx, cv.a, cv.b, "hola", uuid.New(), sinPlaza)
	assert.ErrorIs(t, err, message.ErrRateLimited)
	assert.Equal(t, 1, llamadas)
	assert.Zero(t, filasDeMensajes(t, ctx, store))

	cv.escribir(t, ctx, cv.b, cv.a, "te escribo yo")
	_, err = cv.repo.Send(ctx, cv.a, cv.b, "contesto", uuid.New(), sinPlaza)
	require.NoError(t, err, "contestar a una conversación que existe no es nueva, para ninguno de los dos")
	assert.Equal(t, 1, llamadas)

	deshechas := 0
	conPlaza := func() (func(), bool) { return func() { deshechas++ }, true }
	require.NoError(t, cv.repo.Block(ctx, cv.c, cv.a, true))
	_, err = cv.repo.Send(ctx, cv.a, cv.c, "hola", uuid.New(), conPlaza)
	assert.ErrorIs(t, err, message.ErrBlockedByOther)
	assert.Zero(t, deshechas, "el bloqueo se mira antes de reservar: no hay nada que deshacer")
}

// INVALID_TARGET: jugador inexistente o uno mismo, en cada operación.
func TestNadieSeEscribeASiMismoNiAUnJugadorQueNoExiste(t *testing.T) {
	store, ctx := newTestStore(t)
	cv := nuevosConversadores(t, ctx, store, 50)
	nadie := uuid.New()

	_, err := cv.repo.Send(ctx, cv.a, nadie, "hola", uuid.New(), sinLimite)
	assert.ErrorIs(t, err, message.ErrUnknownPlayer)
	_, err = cv.repo.Send(ctx, cv.a, cv.a, "hola", uuid.New(), sinLimite)
	assert.ErrorIs(t, err, message.ErrSelf)
	assert.ErrorIs(t, cv.repo.Block(ctx, cv.a, nadie, true), message.ErrUnknownPlayer)
	assert.ErrorIs(t, cv.repo.Block(ctx, cv.a, cv.a, true), message.ErrSelf)
	_, err = cv.repo.Page(ctx, cv.a, nadie, 0)
	assert.ErrorIs(t, err, message.ErrUnknownPlayer)
	assert.Zero(t, filasDeMensajes(t, ctx, store))
}

// Q4: la página y la bandeja muestran los últimos 30 días, de 50 en 50 (aquí,
// de 2 en 2).
func TestLaConversacionSePaginaYOlvidaLoDeHaceMasDeTreintaDias(t *testing.T) {
	store, ctx := newTestStore(t)
	cv := nuevosConversadores(t, ctx, store, 2)

	var ids []int64
	for _, texto := range []string{"uno", "dos", "tres", "cuatro", "cinco"} {
		ids = append(ids, cv.escribir(t, ctx, cv.a, cv.b, texto).ID)
	}
	_, err := store.Pool().Exec(ctx, `UPDATE player_messages SET sent_at = now() - interval '31 days' WHERE id = $1`, ids[0])
	require.NoError(t, err)

	page, err := cv.repo.Page(ctx, cv.b, cv.a, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"cuatro", "cinco"}, cuerpos(page.Messages))
	require.NotNil(t, page.NextBeforeID)
	page, err = cv.repo.Page(ctx, cv.b, cv.a, *page.NextBeforeID)
	require.NoError(t, err)
	assert.Equal(t, []string{"dos", "tres"}, cuerpos(page.Messages))
	assert.Nil(t, page.NextBeforeID, "«uno» tiene 31 días: no hay página anterior")

	bandeja, err := cv.repo.Inbox(ctx, cv.b)
	require.NoError(t, err)
	require.Len(t, bandeja.Conversations, 1)
	assert.Equal(t, 4, bandeja.Conversations[0].Unread, "el viejo no cuenta")

	_, err = store.Pool().Exec(ctx, `UPDATE player_messages SET sent_at = now() - interval '31 days'`)
	require.NoError(t, err)
	bandeja, err = cv.repo.Inbox(ctx, cv.b)
	require.NoError(t, err)
	assert.Empty(t, bandeja.Conversations, "una conversación sin nada de los últimos 30 días no sale")
}

func cuerpos(ms []protocol.MessageView) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Body)
	}
	return out
}

// La vista previa de la bandeja es el texto recortado.
func TestLaBandejaRecortaElUltimoMensaje(t *testing.T) {
	store, ctx := newTestStore(t)
	cv := nuevosConversadores(t, ctx, store, 50)
	cv.escribir(t, ctx, cv.a, cv.b, strings.Repeat("ñ", 200))
	bandeja, err := cv.repo.Inbox(ctx, cv.b)
	require.NoError(t, err)
	require.Len(t, bandeja.Conversations, 1)
	assert.Equal(t, message.PreviewChars, len([]rune(bandeja.Conversations[0].LastBody)))
}

// Las restricciones de la base: el texto de 1 a 500 caracteres, la pareja
// ordenada y de los dos jugadores, nadie se bloquea a sí mismo.
func TestLasRestriccionesDeLosMensajes(t *testing.T) {
	store, ctx := newTestStore(t)
	cv := nuevosConversadores(t, ctx, store, 50)
	low, high := message.Pair(cv.a, cv.b)
	insertar := func(from, to, lo, hi uuid.UUID, body string) error {
		_, err := store.Pool().Exec(ctx,
			`INSERT INTO player_messages (sender_player_id, recipient_player_id, player_low, player_high, body, request_id)
			 VALUES ($1, $2, $3, $4, $5, $6)`, from, to, lo, hi, body, uuid.New())
		return err
	}
	require.NoError(t, insertar(cv.a, cv.b, low, high, strings.Repeat("ñ", 500)), "500 caracteres de dos bytes")
	for nombre, err := range map[string]error{
		"501 caracteres":  insertar(cv.a, cv.b, low, high, strings.Repeat("a", 501)),
		"vacío":           insertar(cv.a, cv.b, low, high, ""),
		"pareja al revés": insertar(cv.a, cv.b, high, low, "hola"),
		"pareja de otros": insertar(cv.a, cv.c, low, high, "hola"),
		"a sí mismo":      insertar(cv.a, cv.a, cv.a, cv.a, "hola"),
	} {
		assert.True(t, postgres.IsCheckViolation(err), "%s: %v", nombre, err)
	}
	_, err := store.Pool().Exec(ctx, `INSERT INTO player_blocks (player_id, blocked_player_id) VALUES ($1, $1)`, cv.a)
	assert.True(t, postgres.IsCheckViolation(err), "bloquearse a sí mismo: %v", err)
	_, err = store.Pool().Exec(ctx, `INSERT INTO message_reads (player_id, other_player_id, read_up_to_id) VALUES ($1, $2, -1)`, cv.a, cv.b)
	assert.True(t, postgres.IsCheckViolation(err), "lo leído negativo: %v", err)
}
