package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Las guerras entre clanes (SPEC-CLAN-WARS, ADR-045, migración 000026). Cada
// orden toma los candados de los dos clanes en orden de id y vuelve a comprobar
// lo que comprobó el tick: quien la da es el líder de su clan, los dos clanes
// viven, el estado de la fila, la alianza, el tope de declaradas y la tregua
// (INV-CLAN-008). El comienzo y el plazo son tolerantes: sólo escriben sobre
// una fila que sigue en el estado esperado. El marcador de una guerra abierta
// no vive en la fila: viaja en el payload de los eventos que puntúan
// (INV-CLAN-009), y la fila guarda el congelado al terminar.

// WarWrite es una orden o un vencimiento de una guerra ya decididos por la
// simulación.
type WarWrite struct {
	// Insert: una declaración, sin id todavía.
	Insert bool
	// War es la guerra tal como queda.
	War clan.War
	// From es el estado que debe tener la fila para aplicarla.
	From  clan.WarStatus
	Event string
	// By es el líder que dio la orden; ByClan, su clan (cero si no es una
	// orden).
	By     uuid.UUID
	ByClan int64
	// MaxWars y Cooldown son el tope de declaradas y la tregua que la base
	// vuelve a comprobar al declarar.
	MaxWars  int
	Cooldown time.Duration
	// Tolerant: un vencimiento; si la fila ya no está en From, no escribe nada
	// y no es un error.
	Tolerant bool
	Tick     uint64
	AtMs     int64
}

// ApplyWar escribe una orden o un vencimiento de una guerra, con los candados
// de los dos clanes, y devuelve el id de la guerra.
func (r *ClanRepo) ApplyWar(ctx context.Context, w WarWrite) (int64, error) {
	var id int64
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		g := w.War
		id = g.ID
		if err := lockClanPair(ctx, tx, g.Attacker, g.Defender); err != nil {
			return err
		}
		sides := make(map[int64]clanSide, 2)
		for _, c := range []int64{g.Attacker, g.Defender} {
			side, err := readClanSide(ctx, tx, c)
			if err != nil {
				return err
			}
			sides[c] = side
		}
		if w.Insert {
			return r.insertWar(ctx, tx, w, sides, &id)
		}
		return r.updateWar(ctx, tx, w, sides)
	})
	return id, err
}

// checkWarOrder vuelve a comprobar, para una orden de un líder, que By lidera
// ByClan y que los dos clanes viven (RN-CLANWAR-010).
func checkWarOrder(w WarWrite, sides map[int64]clanSide) error {
	if w.Tolerant {
		return nil
	}
	own := sides[w.ByClan]
	if !w.War.Has(w.ByClan) || !own.alive || own.leader != w.By {
		return ClanRejected{Reason: "sólo el líder decide las guerras del clan"}
	}
	if !sides[w.War.Other(w.ByClan)].alive {
		return ClanRejected{Reason: "ese clan ya no existe"}
	}
	return nil
}

func (r *ClanRepo) insertWar(ctx context.Context, tx pgx.Tx, w WarWrite, sides map[int64]clanSide, id *int64) error {
	g := w.War
	switch err := tx.QueryRow(ctx,
		`SELECT id FROM clan_wars WHERE declared_by_player = $1 AND request_id = $2`, g.DeclaredBy, g.RequestID).Scan(id); {
	case err == nil:
		return nil // ya escrita: un reintento
	case !errors.Is(normalize(err), ErrNotFound):
		return fmt.Errorf("buscar la declaración %s: %w", g.RequestID, normalize(err))
	}
	if err := checkWarOrder(w, sides); err != nil {
		return err
	}
	a, b := clan.CanonicalClans(g.Attacker, g.Defender)
	var abierta, aliados, tregua bool
	var declaradas int
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM clan_wars
		                 WHERE LEAST(attacker_clan, defender_clan) = $1 AND GREATEST(attacker_clan, defender_clan) = $2
		                   AND status IN ('DECLARED', 'ACTIVE')),
		        EXISTS (SELECT 1 FROM clan_alliances WHERE clan_a = $1 AND clan_b = $2 AND status = 'ACTIVE'),
		        EXISTS (SELECT 1 FROM clan_wars
		                 WHERE LEAST(attacker_clan, defender_clan) = $1 AND GREATEST(attacker_clan, defender_clan) = $2
		                   AND status = 'ENDED' AND ended_at > $4),
		        (SELECT count(*) FROM clan_wars WHERE attacker_clan = $3 AND status IN ('DECLARED', 'ACTIVE'))`,
		a, b, g.Attacker, time.UnixMilli(g.DeclaredAtMs).UTC().Add(-w.Cooldown),
	).Scan(&abierta, &aliados, &tregua, &declaradas); err != nil {
		return fmt.Errorf("comprobar la declaración: %w", normalize(err))
	}
	switch {
	case abierta:
		return ClanRejected{Reason: "ya hay una guerra abierta con ese clan"}
	case aliados:
		return ClanRejected{Reason: "vuestros clanes están aliados: rompe antes la alianza"}
	case tregua:
		return ClanRejected{Reason: "vuestros clanes terminaron una guerra hace poco"}
	case w.MaxWars > 0 && declaradas >= w.MaxWars:
		return ClanRejected{Reason: "tu clan ya tiene el máximo de guerras declaradas"}
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO clan_wars (attacker_clan, defender_clan, declared_by_player, request_id, status, declared_at, starts_at)
		 VALUES ($1, $2, $3, $4, 'DECLARED', $5, $6) RETURNING id`,
		g.Attacker, g.Defender, g.DeclaredBy, g.RequestID,
		time.UnixMilli(g.DeclaredAtMs).UTC(), time.UnixMilli(g.StartsAtMs).UTC(),
	).Scan(id); err != nil {
		if IsCheckViolation(err) {
			return ClanRejected{Reason: "vuestros clanes están aliados o uno se disolvió"}
		}
		if IsUniqueViolation(err) {
			return ClanRejected{Reason: "ya hay una guerra abierta con ese clan"}
		}
		return fmt.Errorf("insertar la guerra: %w", normalize(err))
	}
	g.ID = *id
	// RN-CLANWAR-003: la oferta de alianza pendiente entre los dos caduca.
	if err := expirePairAllianceOffer(ctx, tx, a, b, w); err != nil {
		return err
	}
	return appendWarMemberEvents(ctx, tx, w, g, sides, []int64{g.Attacker, g.Defender})
}

func (r *ClanRepo) updateWar(ctx context.Context, tx pgx.Tx, w WarWrite, sides map[int64]clanSide) error {
	g := w.War
	var (
		status      string
		porAtacante bool
		porDefensor bool
		atacante    int64
		defensor    int64
	)
	err := tx.QueryRow(ctx,
		`SELECT status, peace_offered_by_attacker, peace_offered_by_defender, attacker_clan, defender_clan
		   FROM clan_wars WHERE id = $1 FOR UPDATE`, g.ID).
		Scan(&status, &porAtacante, &porDefensor, &atacante, &defensor)
	if errors.Is(normalize(err), ErrNotFound) {
		if w.Tolerant {
			return nil
		}
		return ClanRejected{Reason: "esa guerra no existe"}
	}
	if err != nil {
		return fmt.Errorf("leer la guerra %d: %w", g.ID, normalize(err))
	}
	if atacante != g.Attacker || defensor != g.Defender {
		return fmt.Errorf("%w: la guerra %d no es entre %d y %d", ErrConflict, g.ID, g.Attacker, g.Defender)
	}
	if clan.WarStatus(status) == g.Status && porAtacante == g.PeaceByAttacker && porDefensor == g.PeaceByDefender {
		return nil // ya escrita: un reintento
	}
	if clan.WarStatus(status) != w.From {
		if w.Tolerant {
			return nil // otra escritura la cambió antes
		}
		return ClanRejected{Reason: "esa guerra cambió"}
	}
	if err := checkWarOrder(w, sides); err != nil {
		return err
	}
	var scoreA, scoreD *int64
	var reason *string
	var winner *int64
	if g.Status == clan.WarEnded {
		a, d, mot := g.AttackerScore, g.DefenderScore, g.EndReason
		scoreA, scoreD, reason = &a, &d, &mot
		if g.Winner != 0 {
			v := g.Winner
			winner = &v
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE clan_wars
		    SET status = $2, ends_at = $3, peace_offered_by_attacker = $4, peace_offered_by_defender = $5,
		        ended_at = $6, end_reason = $7, winner_clan = $8, attacker_score = $9, defender_score = $10
		  WHERE id = $1`,
		g.ID, string(g.Status), msToTime(g.EndsAtMs), g.PeaceByAttacker, g.PeaceByDefender,
		msToTime(g.EndedAtMs), reason, winner, scoreA, scoreD); err != nil {
		return fmt.Errorf("escribir la guerra %d: %w", g.ID, normalize(err))
	}
	switch w.Event {
	case eventlog.EventClanWarEnded:
		return appendWarMemberEvents(ctx, tx, w, g, sides, []int64{g.Attacker, g.Defender})
	case eventlog.EventClanWarPeaceOffered:
		return appendPeaceOfferEvents(ctx, tx, w, g, sides)
	}
	return appendEvent(ctx, tx, warEvent(w, g, sides))
}

// warPayload es lo común del payload de los eventos de una guerra.
func warPayload(w WarWrite, g clan.War, sides map[int64]clanSide) map[string]any {
	p := map[string]any{
		"warId": g.ID, "attackerClan": g.Attacker, "defenderClan": g.Defender, "status": string(g.Status),
		"atMs": w.AtMs, "startsAtMs": g.StartsAtMs,
		"attackerTag": sides[g.Attacker].tag, "defenderTag": sides[g.Defender].tag,
	}
	if g.Status == clan.WarEnded {
		p["reason"] = g.EndReason
		p["winnerClan"] = g.Winner
		p["attackerScore"] = g.AttackerScore
		p["defenderScore"] = g.DefenderScore
	}
	return p
}

// warEvent es el world_events de una transición de una sola fila: player_id es
// quien dio la orden (o el líder del clan que declaró, en un vencimiento) y
// counterpart_player_id el líder del otro clan (ADR-045 §6). No va al registro.
func warEvent(w WarWrite, g clan.War, sides map[int64]clanSide) worldEvent {
	ev := worldEvent{EventType: w.Event, Tick: w.Tick, Payload: warPayload(w, g, sides)}
	if w.ByClan == 0 {
		owner := sides[g.Attacker].leader
		ev.PlayerID = &owner
		ev.CounterpartID = counterpart(sides[g.Defender].leader)
		return ev
	}
	by := w.By
	ev.PlayerID = &by
	ev.CounterpartID = counterpart(sides[g.Other(w.ByClan)].leader)
	return ev
}

// appendWarMemberEvents escribe la declaración o el final de una guerra una vez
// por miembro abierto de cada clan dado (Only = OnlyPlayer), con la sigla del
// otro clan (ownerClanTag) y la del propio (forClanTag), y el lado de quien lee
// en el marcador (ADR-045 §6).
func appendWarMemberEvents(ctx context.Context, tx pgx.Tx, w WarWrite, g clan.War, sides map[int64]clanSide, clans []int64) error {
	for _, c := range clans {
		members, err := openMembers(ctx, tx, c)
		if err != nil {
			return err
		}
		otro := g.Other(c)
		for _, m := range members {
			if m == w.By {
				continue // a quien la dio no se le cuenta lo que ordenó él
			}
			member := m
			p := warPayload(w, g, sides)
			p["ownerClanTag"] = sides[otro].tag
			p["forClanTag"] = sides[c].tag
			p["only"] = eventlog.OnlyPlayer
			p["ourScore"] = g.ScoreOf(c)
			p["theirScore"] = g.ScoreOf(otro)
			if g.Status == clan.WarEnded {
				p["result"] = g.ResultFor(c)
			}
			p["declaredByUs"] = g.Attacker == c
			by := w.By
			if by == uuid.Nil {
				by = sides[otro].leader
			}
			if err := appendEvent(ctx, tx, worldEvent{
				EventType: w.Event, Tick: w.Tick, PlayerID: &member, CounterpartID: counterpart(by), Payload: p,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// appendPeaceOfferEvents escribe la oferta de paz una vez por líder y oficial
// del clan que la recibe, con quien la ofrece de counterpart (Q11).
func appendPeaceOfferEvents(ctx context.Context, tx pgx.Tx, w WarWrite, g clan.War, sides map[int64]clanSide) error {
	otro := g.Other(w.ByClan)
	rows, err := tx.Query(ctx,
		`SELECT m.player_id FROM clan_members m JOIN clans c ON c.id = m.clan_id
		  WHERE m.clan_id = $1 AND m.left_at IS NULL AND (m.rank = 'OFFICER' OR m.player_id = c.leader_player_id)
		  ORDER BY m.joined_at, m.player_id`, otro)
	if err != nil {
		return fmt.Errorf("leer los mandos del clan %d: %w", otro, normalize(err))
	}
	var mandos []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return normalize(err)
		}
		mandos = append(mandos, id)
	}
	rows.Close()
	if err := normalize(rows.Err()); err != nil {
		return err
	}
	for _, m := range mandos {
		mando := m
		p := warPayload(w, g, sides)
		p["ownerClanTag"] = sides[w.ByClan].tag
		p["forClanTag"] = sides[otro].tag
		p["only"] = eventlog.OnlyPlayer
		if err := appendEvent(ctx, tx, worldEvent{
			EventType: w.Event, Tick: w.Tick, PlayerID: &mando, CounterpartID: counterpart(w.By), Payload: p,
		}); err != nil {
			return err
		}
	}
	return nil
}

// expirePairAllianceOffer caduca, en la transacción de una declaración, la
// oferta de alianza pendiente entre los dos clanes (RN-CLANWAR-003).
func expirePairAllianceOffer(ctx context.Context, tx pgx.Tx, a, b int64, w WarWrite) error {
	var (
		id    int64
		owner uuid.UUID
	)
	err := tx.QueryRow(ctx,
		`UPDATE clan_alliances SET status = 'EXPIRED', closed_at = $3
		  WHERE clan_a = $1 AND clan_b = $2 AND status = 'PROPOSED'
		  RETURNING id, proposed_by_player`, a, b, time.UnixMilli(w.AtMs).UTC()).Scan(&id, &owner)
	if errors.Is(normalize(err), ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("caducar la oferta de alianza: %w", normalize(err))
	}
	return appendEvent(ctx, tx, worldEvent{
		EventType: eventlog.EventClanAllianceExpired, Tick: w.Tick, PlayerID: &owner,
		Payload: map[string]any{"allianceId": id, "clanA": a, "clanB": b, "status": "EXPIRED", "atMs": w.AtMs, "war": false},
	})
}

// WarEnd es una guerra que la RAM cerró al disolverse uno de sus clanes, con el
// marcador y el vencedor ya puestos (ADR-045 §5).
type WarEnd struct {
	War clan.War
}

// endClanWars cierra, en la transacción de la disolución de un clan, sus guerras
// abiertas, de forma tolerante, con una fila de evento por miembro del otro
// clan (RN-CLANWAR-008, ADR-045 §5).
func endClanWars(ctx context.Context, tx pgx.Tx, l ClanLeave) error {
	for _, e := range l.WarEnds {
		g := e.War
		tag, err := tx.Exec(ctx,
			`UPDATE clan_wars
			    SET status = 'ENDED', ended_at = $2, end_reason = $3, winner_clan = $4,
			        attacker_score = $5, defender_score = $6,
			        peace_offered_by_attacker = false, peace_offered_by_defender = false
			  WHERE id = $1 AND status IN ('DECLARED', 'ACTIVE')`,
			g.ID, time.UnixMilli(g.EndedAtMs).UTC(), g.EndReason, nullableClan(g.Winner), g.AttackerScore, g.DefenderScore)
		if err != nil {
			return fmt.Errorf("terminar la guerra %d: %w", g.ID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			continue // ya terminada por otra vía
		}
		sides := make(map[int64]clanSide, 2)
		for _, c := range []int64{g.Attacker, g.Defender} {
			side, err := readClanSide(ctx, tx, c)
			if err != nil {
				return err
			}
			sides[c] = side
		}
		w := WarWrite{War: g, From: clan.WarActive, Event: eventlog.EventClanWarEnded, By: l.PlayerID, Tick: l.Tick, AtMs: l.AtMs}
		if err := appendWarMemberEvents(ctx, tx, w, g, sides, []int64{g.Other(l.ClanID)}); err != nil {
			return err
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// Carga, marcador y vista pública
// ─────────────────────────────────────────────────────────────

const warColumns = `id, attacker_clan, defender_clan, declared_by_player, request_id, status,
       declared_at, starts_at, ends_at, peace_offered_by_attacker, peace_offered_by_defender,
       ended_at, coalesce(end_reason, ''), coalesce(winner_clan, 0), coalesce(attacker_score, 0), coalesce(defender_score, 0)`

func scanWar(row pgx.Row) (clan.War, error) {
	var (
		g                clan.War
		status           string
		declared, starts time.Time
		endsAt, endedAt  *time.Time
	)
	if err := row.Scan(&g.ID, &g.Attacker, &g.Defender, &g.DeclaredBy, &g.RequestID, &status,
		&declared, &starts, &endsAt, &g.PeaceByAttacker, &g.PeaceByDefender,
		&endedAt, &g.EndReason, &g.Winner, &g.AttackerScore, &g.DefenderScore); err != nil {
		return clan.War{}, err
	}
	g.Status = clan.WarStatus(status)
	g.DeclaredAtMs, g.StartsAtMs = declared.UnixMilli(), starts.UnixMilli()
	g.EndsAtMs, g.EndedAtMs = timeToMs(endsAt), timeToMs(endedAt)
	return g, nil
}

func queryWars(ctx context.Context, db DB, sql string, args ...any) ([]clan.War, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, normalize(err)
	}
	defer rows.Close()
	var out []clan.War
	for rows.Next() {
		g, err := scanWar(rows)
		if err != nil {
			return nil, normalize(err)
		}
		out = append(out, g)
	}
	return out, normalize(rows.Err())
}

// WarScores suma, por guerra y por clan, los puntos que llevan los eventos que
// puntúan de las guerras dadas (ADR-045 §4, INV-CLAN-009).
func WarScores(ctx context.Context, db DB, warIDs []int64) (map[int64]map[int64]int64, error) {
	out := make(map[int64]map[int64]int64, len(warIDs))
	if len(warIDs) == 0 {
		return out, nil
	}
	rows, err := db.Query(ctx,
		`SELECT (payload -> 'war' ->> 'warId')::bigint, (payload -> 'war' ->> 'clanId')::bigint,
		        sum((payload -> 'war' ->> 'points')::bigint)
		   FROM world_events
		  WHERE payload ? 'war' AND (payload -> 'war' ->> 'warId')::bigint = ANY($1)
		  GROUP BY 1, 2`, warIDs)
	if err != nil {
		return nil, fmt.Errorf("sumar el marcador de las guerras: %w", normalize(err))
	}
	defer rows.Close()
	for rows.Next() {
		var war, clanID, points int64
		if err := rows.Scan(&war, &clanID, &points); err != nil {
			return nil, normalize(err)
		}
		if out[war] == nil {
			out[war] = make(map[int64]int64, 2)
		}
		out[war][clanID] = points
	}
	return out, normalize(rows.Err())
}

func withScores(wars []clan.War, scores map[int64]map[int64]int64) []clan.War {
	for i := range wars {
		s := scores[wars[i].ID]
		wars[i].AttackerScore, wars[i].DefenderScore = s[wars[i].Attacker], s[wars[i].Defender]
	}
	return wars
}

// ListWars carga las guerras al arrancar (ADR-045 §1): las abiertas, con el
// marcador sumado de sus eventos; las terminadas desde endedSince, para la
// tregua; y el historial de cada clan vivo.
func (r *ClanRepo) ListWars(ctx context.Context, endedSince time.Time) (open, recent []clan.War, records map[int64]clan.WarRecord, err error) {
	db := r.store.Pool()
	if open, err = queryWars(ctx, db,
		`SELECT `+warColumns+` FROM clan_wars WHERE status IN ('DECLARED', 'ACTIVE') ORDER BY id`); err != nil {
		return nil, nil, nil, fmt.Errorf("cargar las guerras abiertas: %w", err)
	}
	ids := make([]int64, 0, len(open))
	for _, g := range open {
		ids = append(ids, g.ID)
	}
	scores, err := WarScores(ctx, db, ids)
	if err != nil {
		return nil, nil, nil, err
	}
	open = withScores(open, scores)
	if recent, err = queryWars(ctx, db,
		`SELECT `+warColumns+` FROM clan_wars WHERE status = 'ENDED' AND ended_at >= $1 ORDER BY id`, endedSince); err != nil {
		return nil, nil, nil, fmt.Errorf("cargar las guerras recientes: %w", err)
	}
	if records, err = warRecords(ctx, db, 0); err != nil {
		return nil, nil, nil, err
	}
	return open, recent, records, nil
}

// warRecords es el historial de guerras terminadas de los clanes vivos, o sólo
// del clan dado si no es cero.
func warRecords(ctx context.Context, db DB, clanID int64) (map[int64]clan.WarRecord, error) {
	rows, err := db.Query(ctx,
		`SELECT c.id, coalesce(w.winner_clan, 0)
		   FROM clans c
		   JOIN clan_wars w ON w.status = 'ENDED' AND c.id IN (w.attacker_clan, w.defender_clan)
		  WHERE c.dissolved_at IS NULL AND ($1 = 0 OR c.id = $1)`, clanID)
	if err != nil {
		return nil, fmt.Errorf("leer el historial de guerras: %w", normalize(err))
	}
	defer rows.Close()
	out := make(map[int64]clan.WarRecord)
	for rows.Next() {
		var id, winner int64
		if err := rows.Scan(&id, &winner); err != nil {
			return nil, normalize(err)
		}
		out[id] = out[id].Add(clan.War{Winner: winner}.ResultFor(id))
	}
	return out, normalize(rows.Err())
}

// publicWars son las guerras abiertas de un clan para su vista pública, vistas
// desde ese clan, por id del otro clan, con el marcador sumado de sus eventos
// (RN-CLANWAR-009, ADR-045 §4), y su historial.
func publicWars(ctx context.Context, db DB, clanID int64) ([]protocol.ClanPublicWarView, protocol.ClanWarRecordView, error) {
	out := []protocol.ClanPublicWarView{}
	wars, err := queryWars(ctx, db,
		`SELECT `+warColumns+` FROM clan_wars
		  WHERE (attacker_clan = $1 OR defender_clan = $1) AND status IN ('DECLARED', 'ACTIVE') ORDER BY id`, clanID)
	if err != nil {
		return nil, protocol.ClanWarRecordView{}, fmt.Errorf("leer las guerras del clan %d: %w", clanID, err)
	}
	ids := make([]int64, 0, len(wars))
	for _, g := range wars {
		ids = append(ids, g.ID)
	}
	scores, err := WarScores(ctx, db, ids)
	if err != nil {
		return nil, protocol.ClanWarRecordView{}, err
	}
	for _, g := range withScores(wars, scores) {
		otro := g.Other(clanID)
		v := protocol.ClanPublicWarView{
			ClanID: otro, DeclaredByUs: g.Attacker == clanID, Status: string(g.Status), StartsAtMs: g.StartsAtMs,
			OurScore: g.ScoreOf(clanID), TheirScore: g.ScoreOf(otro),
		}
		if g.EndsAtMs > 0 {
			fin := g.EndsAtMs
			v.EndsAtMs = &fin
		}
		if err := db.QueryRow(ctx, `SELECT name, tag FROM clans WHERE id = $1`, otro).Scan(&v.Name, &v.Tag); err != nil {
			return nil, protocol.ClanWarRecordView{}, fmt.Errorf("leer el clan %d: %w", otro, normalize(err))
		}
		out = append(out, v)
	}
	sortPublicWars(out)
	recs, err := warRecords(ctx, db, clanID)
	if err != nil {
		return nil, protocol.ClanWarRecordView{}, err
	}
	rec := recs[clanID]
	return out, protocol.ClanWarRecordView{Won: rec.Won, Lost: rec.Lost, Drawn: rec.Drawn}, nil
}

func sortPublicWars(ws []protocol.ClanPublicWarView) {
	for i := 1; i < len(ws); i++ {
		for j := i; j > 0 && ws[j].ClanID < ws[j-1].ClanID; j-- {
			ws[j], ws[j-1] = ws[j-1], ws[j]
		}
	}
}

func nullableClan(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// warPointsPayload es el campo war del payload de un evento que puntúa
// (ADR-045 §4).
func warPointsPayload(p *clan.WarPoints) map[string]any {
	return map[string]any{"warId": p.WarID, "clanId": p.ClanID, "points": p.Points}
}
