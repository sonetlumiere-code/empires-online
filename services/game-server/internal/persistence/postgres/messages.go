package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/message"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// messagePairLockSpace es el primer entero del candado por pareja
// (pg_advisory_xact_lock(int4, int4)). La forma de dos enteros no comparte
// espacio con la de un bigint, así que no choca con el candado de los tests de
// integración ni con ningún otro de un solo número. «MSGS» en ASCII.
const messagePairLockSpace int32 = 0x4D534753

// inboxMaxConversations acota la bandeja: las conversaciones más recientes.
const inboxMaxConversations = 200

// MessagesConfig parametriza los mensajes (SPEC-MESSAGES §10).
type MessagesConfig struct {
	// MaxAge es hasta cuándo atrás se muestran (EO_MESSAGE_MAX_DAYS, Q4).
	MaxAge time.Duration
	// PageSize es cuántos mensajes lleva una página (EO_MESSAGE_PAGE_SIZE).
	PageSize int
}

// MessageRepo guarda y lee los mensajes entre jugadores, lo leído y los
// bloqueos (SPEC-MESSAGES, ADR-038). Lo usa la conexión, nunca el loop.
type MessageRepo struct {
	store *Store
	cfg   MessagesConfig
}

// NewMessageRepo crea el repositorio.
func NewMessageRepo(s *Store, cfg MessagesConfig) *MessageRepo {
	if cfg.PageSize < 1 {
		cfg.PageSize = 50
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 30 * 24 * time.Hour
	}
	return &MessageRepo{store: s, cfg: cfg}
}

// lockPair toma el candado de transacción de una pareja: los envíos y los
// bloqueos de una conversación se turnan, y sus id se confirman en orden
// (ADR-038 §3). Dos parejas con el mismo hash se esperan sin más.
func lockPair(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) error {
	low, high := message.Pair(a, b)
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, hashtext($2::text || $3::text))`,
		messagePairLockSpace, low.String(), high.String())
	if err != nil {
		return fmt.Errorf("tomar el candado de la conversación: %w", normalize(err))
	}
	return nil
}

// playerExists dice si el jugador existe.
func playerExists(ctx context.Context, db DB, id uuid.UUID) (bool, error) {
	var ok bool
	err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM players WHERE id = $1)`, id).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("buscar al jugador %s: %w", id, normalize(err))
	}
	return ok, nil
}

// blockBetween dice si from tiene bloqueado a to o to a from, en ese orden de
// preferencia para el motivo.
func blockBetween(ctx context.Context, db DB, from, to uuid.UUID) error {
	var byMe, byOther bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM player_blocks WHERE player_id = $1 AND blocked_player_id = $2),
		        EXISTS (SELECT 1 FROM player_blocks WHERE player_id = $2 AND blocked_player_id = $1)`,
		from, to).Scan(&byMe, &byOther)
	if err != nil {
		return fmt.Errorf("leer los bloqueos: %w", normalize(err))
	}
	switch {
	case byMe:
		return message.ErrBlockedByMe
	case byOther:
		return message.ErrBlockedByOther
	}
	return nil
}

// Send guarda un mensaje de from a to con el texto ya normalizado (ADR-038 §3):
// con el candado de la pareja, comprueba que to existe, devuelve el mensaje que
// ya tuviera ese requestId, comprueba los bloqueos en los dos sentidos y, si la
// pareja no se había escrito nunca, pide a reserveNew una conversación nueva del
// límite de ritmo; si no la hay, ErrRateLimited y nada guardado. La reserva se
// deshace si la transacción no confirma.
func (r *MessageRepo) Send(ctx context.Context, from, to uuid.UUID, body string, requestID uuid.UUID,
	reserveNew func() (release func(), ok bool)) (message.Sent, error) {
	if from == to {
		return message.Sent{}, message.ErrSelf
	}
	low, high := message.Pair(from, to)
	sent := message.Sent{From: from, To: to, Body: body}
	var release func()
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		if err := lockPair(ctx, tx, from, to); err != nil {
			return err
		}
		ok, err := playerExists(ctx, tx, to)
		if err != nil {
			return err
		}
		if !ok {
			return message.ErrUnknownPlayer
		}
		done, err := r.existing(ctx, tx, from, requestID, &sent)
		if err != nil || done {
			return err
		}
		if err := blockBetween(ctx, tx, from, to); err != nil {
			return err
		}
		var before bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM player_messages WHERE player_low = $1 AND player_high = $2)`,
			low, high).Scan(&before); err != nil {
			return fmt.Errorf("mirar si la conversación es nueva: %w", normalize(err))
		}
		if !before && reserveNew != nil {
			rel, ok := reserveNew()
			if !ok {
				return message.ErrRateLimited
			}
			release = rel
		}
		var at time.Time
		err = tx.QueryRow(ctx,
			`INSERT INTO player_messages (sender_player_id, recipient_player_id, player_low, player_high, body, request_id)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (sender_player_id, request_id) DO NOTHING
			 RETURNING id, sent_at`,
			from, to, low, high, body, requestID).Scan(&sent.ID, &at)
		if errors.Is(normalize(err), ErrNotFound) {
			// El mismo requestId entró a la vez para otra pareja (otro candado):
			// el mensaje es el que ganó (INV-SEC-007).
			if release != nil {
				release()
				release = nil
			}
			done, err := r.existing(ctx, tx, from, requestID, &sent)
			if err == nil && !done {
				err = fmt.Errorf("el mensaje del requestId %s desapareció", requestID)
			}
			return err
		}
		if err != nil {
			return fmt.Errorf("guardar el mensaje: %w", normalize(err))
		}
		sent.SentAtMs = at.UnixMilli()
		return nil
	})
	if err != nil {
		if release != nil {
			release()
		}
		return message.Sent{}, err
	}
	cities, err := citiesOfPlayers(ctx, r.store.pool, []uuid.UUID{sent.From, sent.To})
	if err != nil {
		return message.Sent{}, err
	}
	sent.FromCity, sent.ToCity = cities[sent.From], cities[sent.To]
	return sent, nil
}

// Existing devuelve el mensaje que from ya guardó con ese requestId, con las
// ciudades de los dos, si lo hay (INV-SEC-007).
func (r *MessageRepo) Existing(ctx context.Context, from, requestID uuid.UUID) (message.Sent, bool, error) {
	sent := message.Sent{From: from}
	found, err := r.existing(ctx, r.store.pool, from, requestID, &sent)
	if err != nil || !found {
		return message.Sent{}, false, err
	}
	cities, err := citiesOfPlayers(ctx, r.store.pool, []uuid.UUID{sent.From, sent.To})
	if err != nil {
		return message.Sent{}, false, err
	}
	sent.FromCity, sent.ToCity = cities[sent.From], cities[sent.To]
	return sent, true, nil
}

// existing rellena sent con el mensaje que from ya guardó con ese requestId,
// si lo hay.
func (r *MessageRepo) existing(ctx context.Context, db DB, from, requestID uuid.UUID, sent *message.Sent) (bool, error) {
	var at time.Time
	err := db.QueryRow(ctx,
		`SELECT id, recipient_player_id, body, sent_at FROM player_messages
		  WHERE sender_player_id = $1 AND request_id = $2`, from, requestID).
		Scan(&sent.ID, &sent.To, &sent.Body, &at)
	if errors.Is(normalize(err), ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("buscar el mensaje del requestId %s: %w", requestID, normalize(err))
	}
	sent.SentAtMs = at.UnixMilli()
	sent.Duplicate = true
	return true, nil
}

// Block bloquea (blocked) o desbloquea a other para me, con el candado de la
// pareja: un envío que corre a la vez confirma antes o ve el bloqueo
// (INV-SEC-011). Es idempotente.
func (r *MessageRepo) Block(ctx context.Context, me, other uuid.UUID, blocked bool) error {
	if me == other {
		return message.ErrSelf
	}
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		if err := lockPair(ctx, tx, me, other); err != nil {
			return err
		}
		ok, err := playerExists(ctx, tx, other)
		if err != nil {
			return err
		}
		if !ok {
			return message.ErrUnknownPlayer
		}
		if blocked {
			_, err = tx.Exec(ctx,
				`INSERT INTO player_blocks (player_id, blocked_player_id) VALUES ($1, $2)
				 ON CONFLICT DO NOTHING`, me, other)
		} else {
			_, err = tx.Exec(ctx,
				`DELETE FROM player_blocks WHERE player_id = $1 AND blocked_player_id = $2`, me, other)
		}
		if err != nil {
			return fmt.Errorf("guardar el bloqueo: %w", normalize(err))
		}
		return nil
	})
}

// MarkRead avanza lo leído de me en su conversación con other hasta upTo, sin
// retroceder nunca (INV-PERSIST-007). El tope lo pone quien llama: lo que esa
// sesión recibió de la conversación.
func (r *MessageRepo) MarkRead(ctx context.Context, me, other uuid.UUID, upTo int64) error {
	if me == other || upTo < 1 {
		return nil
	}
	_, err := r.store.pool.Exec(ctx,
		`INSERT INTO message_reads (player_id, other_player_id, read_up_to_id) VALUES ($1, $2, $3)
		 ON CONFLICT (player_id, other_player_id)
		 DO UPDATE SET read_up_to_id = GREATEST(message_reads.read_up_to_id, EXCLUDED.read_up_to_id)`,
		me, other, upTo)
	if err != nil {
		return fmt.Errorf("guardar lo leído: %w", normalize(err))
	}
	return nil
}

// Page devuelve la página de la conversación de me con other con los mensajes
// de id menor que beforeID (0: la más reciente), de más antiguo a más reciente,
// de los últimos MaxAge (Q4).
func (r *MessageRepo) Page(ctx context.Context, me, other uuid.UUID, beforeID int64) (protocol.MessagePagePayload, error) {
	page := protocol.MessagePagePayload{OtherPlayerID: other.String(), Messages: []protocol.MessageView{}}
	if me == other {
		return page, message.ErrSelf
	}
	db := r.store.pool
	ok, err := playerExists(ctx, db, other)
	if err != nil {
		return page, err
	}
	if !ok {
		return page, message.ErrUnknownPlayer
	}
	if err := db.QueryRow(ctx,
		`SELECT COALESCE((SELECT read_up_to_id FROM message_reads WHERE player_id = $1 AND other_player_id = $2), 0),
		        EXISTS (SELECT 1 FROM player_blocks WHERE player_id = $1 AND blocked_player_id = $2)`,
		me, other).Scan(&page.ReadUpToID, &page.BlockedByMe); err != nil {
		return page, fmt.Errorf("leer lo leído de la conversación: %w", normalize(err))
	}

	low, high := message.Pair(me, other)
	before := beforeID
	if before < 1 {
		before = 1<<63 - 1
	}
	// Uno más que la página, para saber si hay otra detrás.
	rows, err := db.Query(ctx,
		`SELECT id, sender_player_id, body, sent_at FROM player_messages
		  WHERE player_low = $1 AND player_high = $2 AND id < $3
		    AND sent_at > now() - make_interval(secs => $4)
		  ORDER BY id DESC LIMIT $5`,
		low, high, before, r.cfg.MaxAge.Seconds(), r.cfg.PageSize+1)
	if err != nil {
		return page, fmt.Errorf("leer la conversación: %w", normalize(err))
	}
	type fila struct {
		id     int64
		sender uuid.UUID
		body   string
		at     time.Time
	}
	var filas []fila
	for rows.Next() {
		var f fila
		if err := rows.Scan(&f.id, &f.sender, &f.body, &f.at); err != nil {
			rows.Close()
			return page, fmt.Errorf("leer un mensaje: %w", normalize(err))
		}
		filas = append(filas, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("leer la conversación: %w", normalize(err))
	}
	if len(filas) > r.cfg.PageSize {
		filas = filas[:r.cfg.PageSize]
		next := filas[len(filas)-1].id
		page.NextBeforeID = &next
	}

	cities, err := citiesOfPlayers(ctx, db, []uuid.UUID{other})
	if err != nil {
		return page, err
	}
	page.OtherCityID, page.OtherCityName = cityFields(cities[other])
	for i := len(filas) - 1; i >= 0; i-- {
		f := filas[i]
		to := other
		if f.sender == other {
			to = me
		}
		page.Messages = append(page.Messages, protocol.MessageView{
			ID: f.id, FromPlayerID: f.sender.String(), ToPlayerID: to.String(), OtherPlayerID: other.String(),
			OtherCityID: page.OtherCityID, OtherCityName: page.OtherCityName,
			Body: f.body, SentAtMs: f.at.UnixMilli(),
		})
	}
	return page, nil
}

// Inbox devuelve la bandeja de me: sus conversaciones con algún mensaje de los
// últimos MaxAge, de la más reciente a la más antigua, con sus no leídos, y los
// jugadores que tiene bloqueados.
func (r *MessageRepo) Inbox(ctx context.Context, me uuid.UUID) (protocol.MessageInboxPayload, error) {
	inbox := protocol.MessageInboxPayload{
		Conversations: []protocol.ConversationView{},
		Blocked:       []protocol.BlockedPlayerView{},
	}
	db := r.store.pool
	maxAge := r.cfg.MaxAge.Seconds()
	rows, err := db.Query(ctx,
		`WITH ultimos AS (
		   SELECT DISTINCT ON (other) other, id, sender_player_id, body, sent_at
		     FROM (SELECT CASE WHEN player_low = $1 THEN player_high ELSE player_low END AS other,
		                  id, sender_player_id, body, sent_at
		             FROM player_messages
		            WHERE (player_low = $1 OR player_high = $1)
		              AND sent_at > now() - make_interval(secs => $2)) m
		    ORDER BY other, id DESC)
		 SELECT u.other, u.sender_player_id = $1, u.body, u.sent_at,
		        (SELECT count(*) FROM player_messages n
		          WHERE n.player_low = LEAST($1::uuid, u.other) AND n.player_high = GREATEST($1::uuid, u.other)
		            AND n.sender_player_id = u.other
		            AND n.sent_at > now() - make_interval(secs => $2)
		            AND n.id > COALESCE((SELECT read_up_to_id FROM message_reads
		                                  WHERE player_id = $1 AND other_player_id = u.other), 0)),
		        EXISTS (SELECT 1 FROM player_blocks b WHERE b.player_id = $1 AND b.blocked_player_id = u.other)
		   FROM ultimos u
		  ORDER BY u.id DESC
		  LIMIT $3`,
		me, maxAge, inboxMaxConversations)
	if err != nil {
		return inbox, fmt.Errorf("leer la bandeja: %w", normalize(err))
	}
	var others []uuid.UUID
	for rows.Next() {
		var (
			other uuid.UUID
			c     protocol.ConversationView
			at    time.Time
		)
		if err := rows.Scan(&other, &c.LastFromMe, &c.LastBody, &at, &c.Unread, &c.BlockedByMe); err != nil {
			rows.Close()
			return inbox, fmt.Errorf("leer una conversación: %w", normalize(err))
		}
		c.OtherPlayerID = other.String()
		c.LastBody = message.Preview(c.LastBody)
		c.LastAtMs = at.UnixMilli()
		inbox.Conversations = append(inbox.Conversations, c)
		others = append(others, other)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return inbox, fmt.Errorf("leer la bandeja: %w", normalize(err))
	}

	rows, err = db.Query(ctx,
		`SELECT blocked_player_id FROM player_blocks WHERE player_id = $1 ORDER BY created_at DESC, blocked_player_id`, me)
	if err != nil {
		return inbox, fmt.Errorf("leer los bloqueados: %w", normalize(err))
	}
	var blocked []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return inbox, fmt.Errorf("leer un bloqueado: %w", normalize(err))
		}
		blocked = append(blocked, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return inbox, fmt.Errorf("leer los bloqueados: %w", normalize(err))
	}

	cities, err := citiesOfPlayers(ctx, db, append(others, blocked...))
	if err != nil {
		return inbox, err
	}
	for i := range inbox.Conversations {
		inbox.Conversations[i].OtherCityID, inbox.Conversations[i].OtherCityName = cityFields(cities[others[i]])
	}
	for _, id := range blocked {
		b := protocol.BlockedPlayerView{PlayerID: id.String()}
		b.CityID, b.CityName = cityFields(cities[id])
		inbox.Blocked = append(inbox.Blocked, b)
	}
	return inbox, nil
}

// citiesOfPlayers busca la ciudad con la que se nombra a cada jugador: la de id
// menor, que es la capital (RN-MSG-010). Un jugador sin ciudad no sale.
func citiesOfPlayers(ctx context.Context, db DB, players []uuid.UUID) (map[uuid.UUID]*message.City, error) {
	out := make(map[uuid.UUID]*message.City, len(players))
	if len(players) == 0 {
		return out, nil
	}
	rows, err := db.Query(ctx,
		`SELECT DISTINCT ON (owner_player_id) owner_player_id, id, name
		   FROM cities WHERE owner_player_id = ANY($1)
		  ORDER BY owner_player_id, id`, players)
	if err != nil {
		return nil, fmt.Errorf("leer las ciudades de los jugadores: %w", normalize(err))
	}
	defer rows.Close()
	for rows.Next() {
		var (
			owner uuid.UUID
			c     message.City
		)
		if err := rows.Scan(&owner, &c.ID, &c.Name); err != nil {
			return nil, fmt.Errorf("leer una ciudad: %w", normalize(err))
		}
		out[owner] = &c
	}
	return out, normalize(rows.Err())
}

// cityFields traduce la ciudad de un jugador a los dos campos de la vista.
func cityFields(c *message.City) (*int64, *string) {
	if c == nil {
		return nil, nil
	}
	id, name := c.ID, c.Name
	return &id, &name
}
