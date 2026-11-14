package websocket_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/message"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Los mensajes entre jugadores por WebSocket (SPEC-MESSAGES §6 y §9 «contract /
// e2e», ADR-038), con un almacén en memoria: lo que se prueba aquí es la
// conexión —quién recibe qué, el tope de lo leído, los límites y los errores—.
// La base, el candado por pareja y la idempotencia en PostgreSQL están en
// messages_integration_test.go.

type mensajeGuardado struct {
	id       int64
	from, to uuid.UUID
	body     string
	req      uuid.UUID
}

type buzonFalso struct {
	mu       sync.Mutex
	players  map[uuid.UUID]string // jugador → nombre de su ciudad
	mensajes []mensajeGuardado
	bloqueos map[[2]uuid.UUID]bool
	leidos   map[[2]uuid.UUID]int64
	marcas   []int64
	err      error
}

func nuevoBuzon(players map[uuid.UUID]string) *buzonFalso {
	return &buzonFalso{players: players, bloqueos: map[[2]uuid.UUID]bool{}, leidos: map[[2]uuid.UUID]int64{}}
}

func (b *buzonFalso) ciudad(p uuid.UUID) *message.City {
	name, ok := b.players[p]
	if !ok || name == "" {
		return nil
	}
	return &message.City{ID: int64(len(name)), Name: name}
}

func (b *buzonFalso) Send(_ context.Context, from, to uuid.UUID, body string, req uuid.UUID,
	reserveNew func() (func(), bool)) (message.Sent, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return message.Sent{}, b.err
	}
	if _, ok := b.players[to]; !ok {
		return message.Sent{}, message.ErrUnknownPlayer
	}
	for _, m := range b.mensajes {
		if m.from == from && m.req == req {
			return message.Sent{ID: m.id, From: m.from, To: m.to, Body: m.body, SentAtMs: 1,
				FromCity: b.ciudad(m.from), ToCity: b.ciudad(m.to), Duplicate: true}, nil
		}
	}
	if b.bloqueos[[2]uuid.UUID{from, to}] {
		return message.Sent{}, message.ErrBlockedByMe
	}
	if b.bloqueos[[2]uuid.UUID{to, from}] {
		return message.Sent{}, message.ErrBlockedByOther
	}
	nueva := true
	for _, m := range b.mensajes {
		if (m.from == from && m.to == to) || (m.from == to && m.to == from) {
			nueva = false
		}
	}
	if nueva {
		if _, ok := reserveNew(); !ok {
			return message.Sent{}, message.ErrRateLimited
		}
	}
	m := mensajeGuardado{id: int64(len(b.mensajes) + 1), from: from, to: to, body: body, req: req}
	b.mensajes = append(b.mensajes, m)
	return message.Sent{ID: m.id, From: from, To: to, Body: body, SentAtMs: 1,
		FromCity: b.ciudad(from), ToCity: b.ciudad(to)}, nil
}

func (b *buzonFalso) Existing(_ context.Context, from, req uuid.UUID) (message.Sent, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.mensajes {
		if m.from == from && m.req == req {
			return message.Sent{ID: m.id, From: m.from, To: m.to, Body: m.body, SentAtMs: 1,
				FromCity: b.ciudad(m.from), ToCity: b.ciudad(m.to), Duplicate: true}, true, nil
		}
	}
	return message.Sent{}, false, b.err
}

func (b *buzonFalso) vista(me uuid.UUID, m mensajeGuardado, other uuid.UUID) protocol.MessageView {
	v := protocol.MessageView{ID: m.id, FromPlayerID: m.from.String(), ToPlayerID: m.to.String(),
		OtherPlayerID: other.String(), Body: m.body, SentAtMs: 1}
	if c := b.ciudad(other); c != nil {
		id, name := c.ID, c.Name
		v.OtherCityID, v.OtherCityName = &id, &name
	}
	return v
}

func (b *buzonFalso) Inbox(_ context.Context, me uuid.UUID) (protocol.MessageInboxPayload, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := protocol.MessageInboxPayload{Conversations: []protocol.ConversationView{}, Blocked: []protocol.BlockedPlayerView{}}
	if b.err != nil {
		return out, b.err
	}
	ultimos := map[uuid.UUID]mensajeGuardado{}
	unread := map[uuid.UUID]int{}
	for _, m := range b.mensajes {
		var other uuid.UUID
		switch me {
		case m.from:
			other = m.to
		case m.to:
			other = m.from
			if m.id > b.leidos[[2]uuid.UUID{me, other}] {
				unread[other]++
			}
		default:
			continue
		}
		ultimos[other] = m
	}
	for other, m := range ultimos {
		v := b.vista(me, m, other)
		out.Conversations = append(out.Conversations, protocol.ConversationView{
			OtherPlayerID: other.String(), OtherCityID: v.OtherCityID, OtherCityName: v.OtherCityName,
			LastBody: m.body, LastFromMe: m.from == me, LastAtMs: 1, Unread: unread[other],
			BlockedByMe: b.bloqueos[[2]uuid.UUID{me, other}],
		})
	}
	for par := range b.bloqueos {
		if par[0] == me {
			out.Blocked = append(out.Blocked, protocol.BlockedPlayerView{PlayerID: par[1].String()})
		}
	}
	return out, nil
}

func (b *buzonFalso) Page(_ context.Context, me, other uuid.UUID, _ int64) (protocol.MessagePagePayload, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	page := protocol.MessagePagePayload{OtherPlayerID: other.String(), Messages: []protocol.MessageView{}}
	if b.err != nil {
		return page, b.err
	}
	if _, ok := b.players[other]; !ok {
		return page, message.ErrUnknownPlayer
	}
	for _, m := range b.mensajes {
		if (m.from == me && m.to == other) || (m.from == other && m.to == me) {
			page.Messages = append(page.Messages, b.vista(me, m, other))
		}
	}
	page.ReadUpToID = b.leidos[[2]uuid.UUID{me, other}]
	page.BlockedByMe = b.bloqueos[[2]uuid.UUID{me, other}]
	return page, nil
}

func (b *buzonFalso) MarkRead(_ context.Context, me, other uuid.UUID, upTo int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.marcas = append(b.marcas, upTo)
	if upTo > b.leidos[[2]uuid.UUID{me, other}] {
		b.leidos[[2]uuid.UUID{me, other}] = upTo
	}
	return b.err
}

func (b *buzonFalso) Block(_ context.Context, me, other uuid.UUID, blocked bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.players[other]; !ok {
		return message.ErrUnknownPlayer
	}
	if blocked {
		b.bloqueos[[2]uuid.UUID{me, other}] = true
	} else {
		delete(b.bloqueos, [2]uuid.UUID{me, other})
	}
	return nil
}

func (b *buzonFalso) fallar(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = err
}

func (b *buzonFalso) estado() (int, []int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.mensajes), append([]int64(nil), b.marcas...)
}

// escena monta el buzón con el jugador del arnés y dos más.
type escenaDeMensajes struct {
	env         *e2e
	buzon       *buzonFalso
	yo, bea, ce uuid.UUID
}

func nuevaEscenaDeMensajes(t *testing.T, porMinuto, nuevasPorHora int) escenaDeMensajes {
	env := newE2E(t)
	e := escenaDeMensajes{env: env, yo: env.playerID, bea: uuid.New(), ce: uuid.New()}
	e.buzon = nuevoBuzon(map[uuid.UUID]string{e.yo: "Roma", e.bea: "Bizancio", e.ce: "Persépolis"})
	env.ws.SetMessages(e.buzon, message.NewLimiter(porMinuto, nuevasPorHora))
	return e
}

func nuevoMensaje(t *testing.T, raw json.RawMessage) protocol.MessageView {
	t.Helper()
	return decode[protocol.MessageNewPayload](t, raw).Message
}

// errorDe espera el system.error de un comando y comprueba su requestId.
func errorDe(t *testing.T, c *conn, req string) protocol.SystemErrorPayload {
	t.Helper()
	env := c.await(protocol.TypeSystemError, 3*time.Second)
	assert.Equal(t, req, env.RequestID)
	return decode[protocol.SystemErrorPayload](t, env.Payload)
}

// RN-MSG-006: un mensaje guardado llega a todas las sesiones de los dos; la que
// lo envió lo recibe con su requestId, y cada lado ve como «el otro» al otro,
// nombrado por su ciudad (RN-MSG-010). Lo leído no pasa de lo recibido
// (INV-PERSIST-007).
func TestUnMensajeLlegaATodasLasSesionesDeLosDos(t *testing.T) {
	e := nuevaEscenaDeMensajes(t, 10, 10)
	yo1, _ := conectar(t, e.env, e.yo)
	yo2, _ := conectar(t, e.env, e.yo)
	bea, _ := conectar(t, e.env, e.bea)
	ce, _ := conectar(t, e.env, e.ce)

	req := yo1.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "  ¿60 de vino por 200 de piedra?\n"})
	confirmado := yo1.await(protocol.TypeMessageNew, 3*time.Second)
	assert.Equal(t, req, confirmado.RequestID)
	propio := nuevoMensaje(t, confirmado.Payload)
	assert.Equal(t, "¿60 de vino por 200 de piedra?", propio.Body, "recortado en el servidor")
	assert.Equal(t, e.yo.String(), propio.FromPlayerID)
	assert.Equal(t, e.bea.String(), propio.OtherPlayerID)
	require.NotNil(t, propio.OtherCityName)
	assert.Equal(t, "Bizancio", *propio.OtherCityName)

	otraPestana := yo2.await(protocol.TypeMessageNew, 3*time.Second)
	assert.Empty(t, otraPestana.RequestID, "la otra sesión del remitente no lo pidió")
	assert.Equal(t, propio, nuevoMensaje(t, otraPestana.Payload))

	recibido := bea.await(protocol.TypeMessageNew, 3*time.Second)
	assert.Empty(t, recibido.RequestID)
	suyo := nuevoMensaje(t, recibido.Payload)
	assert.Equal(t, propio.ID, suyo.ID)
	assert.Equal(t, e.yo.String(), suyo.OtherPlayerID, "para Bea, el otro es el remitente")
	require.NotNil(t, suyo.OtherCityName)
	assert.Equal(t, "Roma", *suyo.OtherCityName)

	// INV-SEC-010: un tercero conectado no recibe nada.
	ce.send(protocol.TypeSessionPing, map[string]any{"clientTimeMs": 1})
	ce.await(protocol.TypeSessionPong, 3*time.Second)
	for _, m := range ce.historial() {
		assert.NotEqual(t, protocol.TypeMessageNew, m.Type)
	}

	bea.send(protocol.TypeMessageRead, map[string]any{"otherPlayerId": e.yo.String(), "upToId": propio.ID + 100})
	bea.send(protocol.TypeMessageRead, map[string]any{"otherPlayerId": e.ce.String(), "upToId": 5})
	sincronizar(bea)
	_, marcas := e.buzon.estado()
	assert.Equal(t, []int64{propio.ID}, marcas, "acotado a lo recibido; de una conversación sin nada recibido, nada")
	assert.Equal(t, float64(1), testutil.ToFloat64(e.env.metrics.MessagesSent))
}

// Q7: quien no estaba conectado lo ve en la bandeja al entrar; al leer la
// conversación, el no leído desaparece.
func TestElDesconectadoVeElNoLeidoAlEntrar(t *testing.T) {
	e := nuevaEscenaDeMensajes(t, 10, 10)
	yo, _ := conectar(t, e.env, e.yo)
	yo.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "¿estás?"})
	enviado := nuevoMensaje(t, yo.await(protocol.TypeMessageNew, 3*time.Second).Payload)

	bea, _ := conectar(t, e.env, e.bea)
	req := bea.send(protocol.TypeMessageList, map[string]any{})
	env := bea.await(protocol.TypeMessageInbox, 3*time.Second)
	assert.Equal(t, req, env.RequestID)
	inbox := decode[protocol.MessageInboxPayload](t, env.Payload)
	require.Len(t, inbox.Conversations, 1)
	assert.Equal(t, 1, inbox.Conversations[0].Unread)
	assert.Equal(t, e.yo.String(), inbox.Conversations[0].OtherPlayerID)

	bea.send(protocol.TypeMessageRead, map[string]any{"otherPlayerId": e.yo.String(), "upToId": enviado.ID})
	sincronizar(bea)
	_, marcas := e.buzon.estado()
	assert.Empty(t, marcas, "la bandeja no sirve mensajes: sin la conversación abierta no se marca")

	req = bea.send(protocol.TypeMessageFetch, map[string]any{"otherPlayerId": e.yo.String()})
	env = bea.await(protocol.TypeMessagePage, 3*time.Second)
	assert.Equal(t, req, env.RequestID)
	page := decode[protocol.MessagePagePayload](t, env.Payload)
	require.Len(t, page.Messages, 1)
	assert.Equal(t, enviado.ID, page.Messages[0].ID)
	bea.send(protocol.TypeMessageRead, map[string]any{"otherPlayerId": e.yo.String(), "upToId": enviado.ID})
	bea.send(protocol.TypeMessageList, map[string]any{})
	inbox = decode[protocol.MessageInboxPayload](t, bea.await(protocol.TypeMessageInbox, 3*time.Second).Payload)
	assert.Zero(t, inbox.Conversations[0].Unread)
}

// RN-MSG-004 y Q5: con un bloqueo nadie escribe a nadie, y quien escribe sabe
// por qué. Bloquear responde con la bandeja.
func TestElBloqueoCortaLosMensajesEnLosDosSentidos(t *testing.T) {
	e := nuevaEscenaDeMensajes(t, 10, 10)
	yo, _ := conectar(t, e.env, e.yo)
	bea, _ := conectar(t, e.env, e.bea)

	req := bea.send(protocol.TypeMessageBlock, map[string]any{"playerId": e.yo.String(), "blocked": true})
	env := bea.await(protocol.TypeMessageInbox, 3*time.Second)
	assert.Equal(t, req, env.RequestID)
	inbox := decode[protocol.MessageInboxPayload](t, env.Payload)
	require.Len(t, inbox.Blocked, 1)
	assert.Equal(t, e.yo.String(), inbox.Blocked[0].PlayerID)

	req = yo.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "hola"})
	err := errorDe(t, yo, req)
	assert.Equal(t, protocol.CodeInvalidTarget, err.Code)
	assert.Equal(t, message.ErrBlockedByOther.Error(), err.Message)
	req = bea.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.yo.String(), "body": "hola"})
	err = errorDe(t, bea, req)
	assert.Equal(t, protocol.CodeInvalidTarget, err.Code)
	assert.Equal(t, message.ErrBlockedByMe.Error(), err.Message)
	n, _ := e.buzon.estado()
	assert.Zero(t, n)
	assert.Equal(t, float64(2), testutil.ToFloat64(e.env.metrics.MessagesRejected.WithLabelValues(observability.MessageRejectBlocked)))

	bea.send(protocol.TypeMessageBlock, map[string]any{"playerId": e.yo.String(), "blocked": false})
	inbox = decode[protocol.MessageInboxPayload](t, bea.await(protocol.TypeMessageInbox, 3*time.Second).Payload)
	assert.Empty(t, inbox.Blocked)
	yo.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "¿ahora sí?"})
	bea.await(protocol.TypeMessageNew, 3*time.Second)
}

// RN-MSG-005 y Q6: pasado un límite, RATE_LIMITED con el requestId y la
// conexión sigue abierta; un rechazo no gasta ritmo.
func TestLosLimitesDeMensajesNoCierranLaConexion(t *testing.T) {
	e := nuevaEscenaDeMensajes(t, 3, 1)
	yo, _ := conectar(t, e.env, e.yo)

	yo.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "uno"})
	yo.await(protocol.TypeMessageNew, 3*time.Second)
	req := yo.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.ce.String(), "body": "a otra"})
	err := errorDe(t, yo, req)
	assert.Equal(t, protocol.CodeRateLimited, err.Code, "la segunda conversación nueva en la hora")
	yo.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "dos"})
	yo.await(protocol.TypeMessageNew, 3*time.Second)
	yo.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "tres"})
	yo.await(protocol.TypeMessageNew, 3*time.Second)
	req = yo.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "cuatro"})
	err = errorDe(t, yo, req)
	assert.Equal(t, protocol.CodeRateLimited, err.Code, "el cuarto en el minuto: el rechazado a Ce no contó")

	sincronizar(yo)
	n, _ := e.buzon.estado()
	assert.Equal(t, 3, n)
	assert.Equal(t, float64(2), testutil.ToFloat64(e.env.metrics.MessagesRejected.WithLabelValues(observability.MessageRejectRate)))
}

// INV-SEC-007: el mismo requestId otra vez recibe el mensaje original, sólo en
// la sesión que lo pidió, y no gasta ritmo.
func TestUnEnvioRepetidoRecibeElMensajeOriginal(t *testing.T) {
	e := nuevaEscenaDeMensajes(t, 1, 10)
	yo, _ := conectar(t, e.env, e.yo)
	bea, _ := conectar(t, e.env, e.bea)

	req := uuid.NewString()
	yo.sendID(req, protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "hola"})
	original := nuevoMensaje(t, yo.await(protocol.TypeMessageNew, 3*time.Second).Payload)
	yo.sendID(req, protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "hola"})
	otraVez := yo.await(protocol.TypeMessageNew, 3*time.Second)
	assert.Equal(t, req, otraVez.RequestID)
	assert.Equal(t, original.ID, nuevoMensaje(t, otraVez.Payload).ID)

	bea.await(protocol.TypeMessageNew, 3*time.Second)
	sincronizar(bea)
	recibidos := 0
	for _, m := range bea.historial() {
		if m.Type == protocol.TypeMessageNew {
			recibidos++
		}
	}
	assert.Equal(t, 1, recibidos, "el destinatario no lo recibe dos veces")
	n, _ := e.buzon.estado()
	assert.Equal(t, 1, n)
}

// §5: lo que el esquema o el dominio rechazan no llega al almacén, y nadie se
// escribe a sí mismo ni a quien no existe.
func TestLosMensajesRechazanLoInvalido(t *testing.T) {
	e := nuevaEscenaDeMensajes(t, 100, 100)
	yo, _ := conectar(t, e.env, e.yo)
	otro := e.bea.String()

	for _, caso := range []struct {
		tipo    string
		payload map[string]any
		code    string
	}{
		{protocol.TypeMessageSend, map[string]any{"toPlayerId": otro, "body": ""}, protocol.CodeInvalidMessage},
		{protocol.TypeMessageSend, map[string]any{"toPlayerId": otro, "body": "   \n "}, protocol.CodeInvalidMessage},
		{protocol.TypeMessageSend, map[string]any{"toPlayerId": otro, "body": "a‮b"}, protocol.CodeInvalidMessage},
		{protocol.TypeMessageSend, map[string]any{"toPlayerId": otro, "body": strings.Repeat("a", 501)}, protocol.CodeInvalidMessage},
		{protocol.TypeMessageSend, map[string]any{"toPlayerId": "no-es-uuid", "body": "hola"}, protocol.CodeInvalidMessage},
		{protocol.TypeMessageSend, map[string]any{"toPlayerId": otro, "body": "hola", "fromPlayerId": otro}, protocol.CodeInvalidMessage},
		{protocol.TypeMessageSend, map[string]any{"toPlayerId": e.yo.String(), "body": "hola"}, protocol.CodeInvalidTarget},
		{protocol.TypeMessageSend, map[string]any{"toPlayerId": uuid.NewString(), "body": "hola"}, protocol.CodeInvalidTarget},
		{protocol.TypeMessageList, map[string]any{"playerId": otro}, protocol.CodeInvalidMessage},
		{protocol.TypeMessageFetch, map[string]any{"otherPlayerId": otro, "beforeId": 0}, protocol.CodeInvalidMessage},
		{protocol.TypeMessageFetch, map[string]any{"otherPlayerId": e.yo.String()}, protocol.CodeInvalidTarget},
		{protocol.TypeMessageFetch, map[string]any{"otherPlayerId": uuid.NewString()}, protocol.CodeInvalidTarget},
		{protocol.TypeMessageRead, map[string]any{"otherPlayerId": otro, "upToId": 0}, protocol.CodeInvalidMessage},
		{protocol.TypeMessageBlock, map[string]any{"playerId": e.yo.String(), "blocked": true}, protocol.CodeInvalidTarget},
		{protocol.TypeMessageBlock, map[string]any{"playerId": uuid.NewString(), "blocked": true}, protocol.CodeInvalidTarget},
	} {
		req := yo.send(caso.tipo, caso.payload)
		assert.Equal(t, caso.code, errorDe(t, yo, req).Code, "%s %v", caso.tipo, caso.payload)
	}
	n, marcas := e.buzon.estado()
	assert.Zero(t, n)
	assert.Empty(t, marcas)
}

// RN-MSG-009: si la base falla, INTERNAL_ERROR y la sesión sigue; sin
// mensajes montados, NOT_IMPLEMENTED.
func TestLosMensajesSinBaseNoTumbanLaSesion(t *testing.T) {
	e := nuevaEscenaDeMensajes(t, 10, 10)
	e.buzon.fallar(errors.New("la base no responde"))
	yo, _ := conectar(t, e.env, e.yo)
	for _, caso := range []struct {
		tipo    string
		payload map[string]any
	}{
		{protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "hola"}},
		{protocol.TypeMessageList, map[string]any{}},
		{protocol.TypeMessageFetch, map[string]any{"otherPlayerId": e.bea.String()}},
	} {
		req := yo.send(caso.tipo, caso.payload)
		assert.Equal(t, protocol.CodeInternalError, errorDe(t, yo, req).Code, caso.tipo)
	}
	sincronizar(yo)
	assert.Equal(t, float64(1), testutil.ToFloat64(e.env.metrics.MessagesRejected.WithLabelValues(observability.MessageRejectInternal)))

	sin := newE2E(t)
	c := sin.conectar()
	req := c.send(protocol.TypeMessageList, map[string]any{})
	assert.Equal(t, protocol.CodeNotImplemented, errorDe(t, c, req).Code)
}

// RN-MSG-007: el texto no va a los logs, ni cuando se guarda ni cuando falla.
func TestElTextoDeUnMensajeNoSaleEnLosLogs(t *testing.T) {
	e := nuevaEscenaDeMensajes(t, 10, 10)
	yo, _ := conectar(t, e.env, e.yo)
	secreto := "la-contraseña-del-almacén-es-ñandú-42"
	yo.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": secreto})
	yo.await(protocol.TypeMessageNew, 3*time.Second)
	e.buzon.fallar(errors.New("la base no responde"))
	req := yo.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": secreto})
	errorDe(t, yo, req)
	sincronizar(yo)
	assert.NotContains(t, e.env.logs.String(), secreto)
	assert.False(t, bytes.Contains([]byte(e.env.logs.String()), []byte("ñandú")))
}

// Los cinco comandos son de sesión: ninguno llega al loop (RN-MSG-009).
func TestLosMensajesNoPasanPorLaSimulacion(t *testing.T) {
	e := nuevaEscenaDeMensajes(t, 10, 10)
	yo, _ := conectar(t, e.env, e.yo)
	antes := len(e.env.commands)
	yo.send(protocol.TypeMessageSend, map[string]any{"toPlayerId": e.bea.String(), "body": "hola"})
	yo.await(protocol.TypeMessageNew, 3*time.Second)
	yo.send(protocol.TypeMessageList, map[string]any{})
	yo.await(protocol.TypeMessageInbox, 3*time.Second)
	assert.Equal(t, antes, len(e.env.commands), "ningún comando en la cola del loop")
	tipos := []string{}
	for _, m := range yo.historial() {
		tipos = append(tipos, m.Type)
	}
	sort.Strings(tipos)
	assert.Contains(t, tipos, protocol.TypeMessageInbox)
}
