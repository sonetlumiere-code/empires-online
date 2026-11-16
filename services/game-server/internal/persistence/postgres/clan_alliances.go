package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/clan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Las alianzas entre clanes (SPEC-CLAN-ALLIANCES, ADR-042, migración 000023).
// Cada orden toma los candados de los dos clanes en orden de id y vuelve a
// comprobar lo que comprobó el tick: quien la da es el líder de su clan, el
// estado de la fila, el tope y la mezcla de bandos (INV-CLAN-006). Los
// vencimientos y las rupturas por bandos son tolerantes: sólo escriben sobre
// una fila que sigue en el estado esperado.

// AllianceWrite es una orden o un vencimiento de una alianza ya decididos por la
// simulación.
type AllianceWrite struct {
	Insert   bool
	Alliance clan.Alliance
	From     diplomacy.Status
	Event    string
	// By es quien dio la orden o provocó el preaviso; ByClan, el clan del
	// líder que da una orden (cero si no es una orden).
	By        uuid.UUID
	ByClan    int64
	MaxAllies int
	Tolerant  bool
	Tick      uint64
	AtMs      int64
}

// AllianceBreak es una alianza a la que una transacción abrió el preaviso por
// la mezcla de bandos.
type AllianceBreak struct {
	AllianceID       int64
	BreakEffectiveAt time.Time
}

func msToTime(ms int64) *time.Time {
	if ms == 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

func timeToMs(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixMilli()
}

// lockClanPair toma los candados de dos clanes en orden de id (ADR-042 §3).
func lockClanPair(ctx context.Context, tx pgx.Tx, a, b int64) error {
	x, y := clan.CanonicalClans(a, b)
	if err := lockClan(ctx, tx, x); err != nil {
		return err
	}
	return lockClan(ctx, tx, y)
}

// clanSide es lo que una transacción lee de un clan: si vive, su líder y su
// sigla.
type clanSide struct {
	alive  bool
	leader uuid.UUID
	tag    string
}

func readClanSide(ctx context.Context, tx pgx.Tx, clanID int64) (clanSide, error) {
	var (
		side      clanSide
		dissolved *time.Time
	)
	err := tx.QueryRow(ctx, `SELECT leader_player_id, tag, dissolved_at FROM clans WHERE id = $1`, clanID).
		Scan(&side.leader, &side.tag, &dissolved)
	if errors.Is(normalize(err), ErrNotFound) {
		return clanSide{}, nil
	}
	if err != nil {
		return clanSide{}, fmt.Errorf("leer el clan %d: %w", clanID, normalize(err))
	}
	side.alive = dissolved == nil
	return side, nil
}

// activeAlliancesOf cuenta las alianzas activas de un clan, salvo except.
func activeAlliancesOf(ctx context.Context, tx pgx.Tx, clanID, except int64) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		`SELECT count(*) FROM clan_alliances
		  WHERE (clan_a = $1 OR clan_b = $1) AND status = 'ACTIVE' AND id <> $2`, clanID, except).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("contar las alianzas del clan %d: %w", clanID, normalize(err))
	}
	return n, nil
}

func pairMixes(ctx context.Context, tx pgx.Tx, a, b int64) (bool, error) {
	var mixes bool
	if err := tx.QueryRow(ctx, `SELECT clan_pair_mixes_factions($1, $2)`, a, b).Scan(&mixes); err != nil {
		return false, fmt.Errorf("comprobar los bandos de %d y %d: %w", a, b, normalize(err))
	}
	return mixes, nil
}

// ApplyAlliance escribe una orden o un vencimiento de una alianza, con los
// candados de los dos clanes, y devuelve el id de la alianza.
func (r *ClanRepo) ApplyAlliance(ctx context.Context, w AllianceWrite) (int64, error) {
	var id int64
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		a := w.Alliance
		id = a.ID
		if err := lockClanPair(ctx, tx, a.ClanA, a.ClanB); err != nil {
			return err
		}
		sideA, err := readClanSide(ctx, tx, a.ClanA)
		if err != nil {
			return err
		}
		sideB, err := readClanSide(ctx, tx, a.ClanB)
		if err != nil {
			return err
		}
		sides := map[int64]clanSide{a.ClanA: sideA, a.ClanB: sideB}
		if w.Insert {
			return r.insertAlliance(ctx, tx, w, sides, &id)
		}
		return r.updateAlliance(ctx, tx, w, sides)
	})
	return id, err
}

// checkOrder vuelve a comprobar, para una orden de un líder, que By lidera ByClan
// y que los dos clanes viven (RN-CLANALLY-009).
func checkOrder(w AllianceWrite, sides map[int64]clanSide) error {
	if w.Tolerant {
		return nil
	}
	own := sides[w.ByClan]
	if !own.alive || own.leader != w.By {
		return ClanRejected{Reason: "sólo el líder firma las alianzas del clan"}
	}
	if !sides[w.Alliance.Other(w.ByClan)].alive {
		return ClanRejected{Reason: "ese clan ya no existe"}
	}
	return nil
}

// checkCapAndFactions vuelve a comprobar el tope de cada clan y la mezcla de
// bandos antes de abrir o aceptar una alianza (INV-CLAN-006).
func checkCapAndFactions(ctx context.Context, tx pgx.Tx, w AllianceWrite) error {
	a := w.Alliance
	if w.MaxAllies > 0 {
		for _, c := range []int64{a.ClanA, a.ClanB} {
			n, err := activeAlliancesOf(ctx, tx, c, a.ID)
			if err != nil {
				return err
			}
			if n >= w.MaxAllies {
				return ClanRejected{Reason: "un clan ya tiene el máximo de alianzas"}
			}
		}
	}
	mixes, err := pairMixes(ctx, tx, a.ClanA, a.ClanB)
	if err != nil {
		return err
	}
	if mixes {
		return ClanRejected{Reason: "una alianza no junta a la Orden y al Caos"}
	}
	return nil
}

func (r *ClanRepo) insertAlliance(ctx context.Context, tx pgx.Tx, w AllianceWrite, sides map[int64]clanSide, id *int64) error {
	a := w.Alliance
	switch err := tx.QueryRow(ctx,
		`SELECT id FROM clan_alliances WHERE proposed_by_player = $1 AND request_id = $2`, a.ProposedBy, a.RequestID).Scan(id); {
	case err == nil:
		return nil // ya escrita: un reintento
	case !errors.Is(normalize(err), ErrNotFound):
		return fmt.Errorf("buscar la oferta %s: %w", a.RequestID, normalize(err))
	}
	if err := checkOrder(w, sides); err != nil {
		return err
	}
	var open []string
	rows, err := tx.Query(ctx,
		`SELECT status FROM clan_alliances WHERE clan_a = $1 AND clan_b = $2 AND status IN ('PROPOSED', 'ACTIVE')`,
		a.ClanA, a.ClanB)
	if err != nil {
		return fmt.Errorf("leer las alianzas del par: %w", normalize(err))
	}
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			rows.Close()
			return normalize(err)
		}
		open = append(open, st)
	}
	rows.Close()
	if err := normalize(rows.Err()); err != nil {
		return err
	}
	if len(open) > 0 {
		return ClanRejected{Reason: "ya hay una alianza o una oferta con ese clan"}
	}
	if err := checkCapAndFactions(ctx, tx, w); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO clan_alliances (clan_a, clan_b, proposed_by_clan, proposed_by_player, status, request_id, created_at)
		 VALUES ($1, $2, $3, $4, 'PROPOSED', $5, $6) RETURNING id`,
		a.ClanA, a.ClanB, a.ProposedByClan, a.ProposedBy, a.RequestID, time.UnixMilli(a.CreatedAtMs).UTC(),
	).Scan(id); err != nil {
		if IsCheckViolation(err) {
			return ClanRejected{Reason: "una alianza no junta a la Orden y al Caos"}
		}
		if IsUniqueViolation(err) {
			return ClanRejected{Reason: "ya hay una alianza o una oferta con ese clan"}
		}
		return fmt.Errorf("insertar la oferta de alianza: %w", normalize(err))
	}
	a.ID = *id
	return appendEvent(ctx, tx, allianceEvent(w, a, sides))
}

func (r *ClanRepo) updateAlliance(ctx context.Context, tx pgx.Tx, w AllianceWrite, sides map[int64]clanSide) error {
	a := w.Alliance
	var (
		status   string
		ruptura  *time.Time
		proposer int64
	)
	err := tx.QueryRow(ctx,
		`SELECT status, break_effective_at, proposed_by_clan FROM clan_alliances WHERE id = $1 FOR UPDATE`, a.ID).
		Scan(&status, &ruptura, &proposer)
	if errors.Is(normalize(err), ErrNotFound) {
		if w.Tolerant {
			return nil
		}
		return ClanRejected{Reason: "esa alianza no existe"}
	}
	if err != nil {
		return fmt.Errorf("leer la alianza %d: %w", a.ID, normalize(err))
	}
	if diplomacy.Status(status) == a.Status && timeToMs(ruptura) == a.BreakEffectiveAtMs {
		return nil // ya escrita: un reintento
	}
	if diplomacy.Status(status) != w.From || (w.From == diplomacy.StatusActive && a.Status == diplomacy.StatusActive && ruptura != nil) {
		if w.Tolerant {
			return nil // otra escritura la cambió antes
		}
		return ClanRejected{Reason: "esa alianza cambió"}
	}
	if err := checkOrder(w, sides); err != nil {
		return err
	}
	// RN-CLANALLY-002: responde el clan que no propuso; retira el que propuso.
	if !w.Tolerant && w.From == diplomacy.StatusProposed {
		responde := a.Status == diplomacy.StatusActive || a.Status == diplomacy.StatusRejected
		if responde == (w.ByClan == proposer) {
			return ClanRejected{Reason: "ese clan no puede hacer eso con la oferta"}
		}
	}
	if a.Status == diplomacy.StatusActive && w.From == diplomacy.StatusProposed {
		if err := checkCapAndFactions(ctx, tx, w); err != nil {
			return err
		}
	}
	var reason *string
	if a.BreakReason != "" {
		reason = &a.BreakReason
	}
	if _, err := tx.Exec(ctx,
		`UPDATE clan_alliances
		    SET status = $2, accepted_at = $3, break_effective_at = $4, break_reason = $5, closed_at = $6
		  WHERE id = $1`,
		a.ID, string(a.Status), msToTime(a.AcceptedAtMs), msToTime(a.BreakEffectiveAtMs), reason, msToTime(a.ClosedAtMs)); err != nil {
		if IsCheckViolation(err) && !w.Tolerant {
			return ClanRejected{Reason: "una alianza no junta a la Orden y al Caos"}
		}
		return fmt.Errorf("escribir la alianza %d: %w", a.ID, normalize(err))
	}
	if w.Event == eventlog.EventClanAllianceBreakAnnounced {
		return appendBreakEvents(ctx, tx, w, a, sides)
	}
	return appendEvent(ctx, tx, allianceEvent(w, a, sides))
}

// allianceEvent es el world_events de una transición de un solo lado:
// player_id es quien dio la orden (o el líder que propuso, en un vencimiento) y
// counterpart_player_id el líder del otro clan; ownerClanTag es la sigla del
// otro clan y forClanTag la del clan de player_id (ADR-042 §6).
func allianceEvent(w AllianceWrite, a clan.Alliance, sides map[int64]clanSide) worldEvent {
	payload := map[string]any{
		"allianceId": a.ID, "clanA": a.ClanA, "clanB": a.ClanB, "status": string(a.Status), "atMs": w.AtMs,
	}
	if !w.Insert {
		payload["from"] = string(w.From)
	}
	ev := worldEvent{EventType: w.Event, Tick: w.Tick, Payload: payload}
	if w.ByClan == 0 {
		// Un vencimiento: no lo hizo nadie y no va al registro.
		owner := a.ProposedBy
		ev.PlayerID = &owner
		return ev
	}
	by := w.By
	other := sides[a.Other(w.ByClan)]
	payload["ownerClanTag"] = other.tag
	payload["forClanTag"] = sides[w.ByClan].tag
	ev.PlayerID = &by
	ev.CounterpartID = counterpart(other.leader)
	return ev
}

// appendBreakEvents escribe el preaviso de una alianza una vez por miembro
// abierto de los dos clanes (Only = OnlyPlayer), con la sigla del otro clan y
// quien lo provocó de counterpart (ADR-042 §6). Quien lo provocó no recibe
// fila: lo hizo él.
func appendBreakEvents(ctx context.Context, tx pgx.Tx, w AllianceWrite, a clan.Alliance, sides map[int64]clanSide) error {
	for _, c := range []int64{a.ClanA, a.ClanB} {
		members, err := openMembers(ctx, tx, c)
		if err != nil {
			return err
		}
		otherTag := sides[a.Other(c)].tag
		for _, m := range members {
			if m == w.By {
				continue
			}
			member := m
			if err := appendEvent(ctx, tx, worldEvent{
				EventType: eventlog.EventClanAllianceBreakAnnounced, Tick: w.Tick, PlayerID: &member,
				CounterpartID: counterpart(w.By),
				Payload: map[string]any{
					"allianceId": a.ID, "clanA": a.ClanA, "clanB": a.ClanB, "atMs": w.AtMs,
					"breakEffectiveAt": time.UnixMilli(a.BreakEffectiveAtMs).UTC(), "reason": a.BreakReason,
					"ownerClanTag": otherTag, "only": eventlog.OnlyPlayer,
				},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// breakAllianceTolerant abre el preaviso de una alianza por la mezcla de bandos
// dentro de otra transacción (la entrada en un bando o en un clan), sólo si
// sigue activa sin preaviso, con sus eventos (ADR-042 §4).
func breakAllianceTolerant(ctx context.Context, tx pgx.Tx, w AllianceWrite) error {
	a := w.Alliance
	tag, err := tx.Exec(ctx,
		`UPDATE clan_alliances SET break_effective_at = $2, break_reason = $3
		  WHERE id = $1 AND status = 'ACTIVE' AND break_effective_at IS NULL`,
		a.ID, time.UnixMilli(a.BreakEffectiveAtMs).UTC(), clan.BreakReasonFactions)
	if err != nil {
		return fmt.Errorf("abrir el preaviso de la alianza %d: %w", a.ID, normalize(err))
	}
	if tag.RowsAffected() == 0 {
		return nil // otra escritura la cerró o la rompió antes
	}
	sides := make(map[int64]clanSide, 2)
	for _, c := range []int64{a.ClanA, a.ClanB} {
		side, err := readClanSide(ctx, tx, c)
		if err != nil {
			return err
		}
		sides[c] = side
	}
	return appendBreakEvents(ctx, tx, w, a, sides)
}

// breakMixedAlliances abre, dentro de la transacción de una entrada en un clan,
// el preaviso de cada alianza activa sin preaviso del clan que ahora junta a la
// Orden y al Caos, y las devuelve (ADR-042 §4).
func breakMixedAlliances(ctx context.Context, tx pgx.Tx, o ClanJoin) ([]AllianceBreak, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, clan_a, clan_b FROM clan_alliances
		  WHERE (clan_a = $1 OR clan_b = $1) AND status = 'ACTIVE' AND break_effective_at IS NULL
		  ORDER BY id`, o.ClanID)
	if err != nil {
		return nil, fmt.Errorf("leer las alianzas del clan %d: %w", o.ClanID, normalize(err))
	}
	var candidates []clan.Alliance
	for rows.Next() {
		var a clan.Alliance
		if err := rows.Scan(&a.ID, &a.ClanA, &a.ClanB); err != nil {
			rows.Close()
			return nil, normalize(err)
		}
		candidates = append(candidates, a)
	}
	rows.Close()
	if err := normalize(rows.Err()); err != nil {
		return nil, err
	}
	var out []AllianceBreak
	for _, a := range candidates {
		mixes, err := pairMixes(ctx, tx, a.ClanA, a.ClanB)
		if err != nil {
			return nil, err
		}
		if !mixes {
			continue
		}
		fin := o.At.Add(o.BreakNotice)
		a.Status, a.BreakEffectiveAtMs, a.BreakReason = diplomacy.StatusActive, fin.UnixMilli(), clan.BreakReasonFactions
		w := AllianceWrite{Alliance: a, From: diplomacy.StatusActive, Event: eventlog.EventClanAllianceBreakAnnounced,
			By: o.PlayerID, Tolerant: true, Tick: o.Tick, AtMs: o.AtMs}
		if err := breakAllianceTolerant(ctx, tx, w); err != nil {
			return nil, err
		}
		out = append(out, AllianceBreak{AllianceID: a.ID, BreakEffectiveAt: fin})
	}
	return out, nil
}

// expireClanAlliances expira, en la transacción de la disolución de un clan, sus
// ofertas y alianzas abiertas (RN-CLANALLY-008, ADR-042 §5).
func expireClanAlliances(ctx context.Context, tx pgx.Tx, l ClanLeave) error {
	rows, err := tx.Query(ctx,
		`UPDATE clan_alliances SET status = 'EXPIRED', closed_at = $2
		  WHERE (clan_a = $1 OR clan_b = $1) AND status IN ('PROPOSED', 'ACTIVE')
		  RETURNING id, clan_a, clan_b, proposed_by_player`, l.ClanID, l.At)
	if err != nil {
		return fmt.Errorf("expirar las alianzas del clan %d: %w", l.ClanID, normalize(err))
	}
	type expirada struct {
		id, a, b int64
		owner    uuid.UUID
	}
	var out []expirada
	for rows.Next() {
		var e expirada
		if err := rows.Scan(&e.id, &e.a, &e.b, &e.owner); err != nil {
			rows.Close()
			return normalize(err)
		}
		out = append(out, e)
	}
	rows.Close()
	if err := normalize(rows.Err()); err != nil {
		return err
	}
	for _, e := range out {
		owner := e.owner
		if err := appendEvent(ctx, tx, worldEvent{
			EventType: eventlog.EventClanAllianceExpired, Tick: l.Tick, PlayerID: &owner,
			Payload: map[string]any{"allianceId": e.id, "clanA": e.a, "clanB": e.b, "status": "EXPIRED", "atMs": l.AtMs, "dissolved": true},
		}); err != nil {
			return err
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// Carga y vista pública
// ─────────────────────────────────────────────────────────────

const allianceColumns = `id, clan_a, clan_b, proposed_by_clan, proposed_by_player, status, request_id,
       created_at, accepted_at, break_effective_at, coalesce(break_reason, ''), closed_at`

func scanAlliance(row pgx.Row) (clan.Alliance, error) {
	var (
		a                         clan.Alliance
		status                    string
		created                   time.Time
		accepted, breakAt, closed *time.Time
	)
	if err := row.Scan(&a.ID, &a.ClanA, &a.ClanB, &a.ProposedByClan, &a.ProposedBy, &status, &a.RequestID,
		&created, &accepted, &breakAt, &a.BreakReason, &closed); err != nil {
		return clan.Alliance{}, err
	}
	a.Status = diplomacy.Status(status)
	a.CreatedAtMs = created.UnixMilli()
	a.AcceptedAtMs, a.BreakEffectiveAtMs, a.ClosedAtMs = timeToMs(accepted), timeToMs(breakAt), timeToMs(closed)
	return a, nil
}

func (r *ClanRepo) queryAlliances(ctx context.Context, sql string, args ...any) ([]clan.Alliance, error) {
	rows, err := r.store.Pool().Query(ctx, sql, args...)
	if err != nil {
		return nil, normalize(err)
	}
	defer rows.Close()
	var out []clan.Alliance
	for rows.Next() {
		a, err := scanAlliance(rows)
		if err != nil {
			return nil, normalize(err)
		}
		out = append(out, a)
	}
	return out, normalize(rows.Err())
}

// ListAlliances carga las alianzas al arrancar (ADR-042 §1): las abiertas; las
// que estuvieron activas y se cerraron desde closedSince, para la paz residual;
// y los rechazos desde rejectedSince, para el plazo de insistir.
func (r *ClanRepo) ListAlliances(ctx context.Context, closedSince, rejectedSince time.Time) (open, closed, rejected []clan.Alliance, err error) {
	if open, err = r.queryAlliances(ctx,
		`SELECT `+allianceColumns+` FROM clan_alliances WHERE status IN ('PROPOSED', 'ACTIVE') ORDER BY id`); err != nil {
		return nil, nil, nil, fmt.Errorf("cargar las alianzas abiertas: %w", err)
	}
	if closed, err = r.queryAlliances(ctx,
		`SELECT `+allianceColumns+` FROM clan_alliances
		  WHERE accepted_at IS NOT NULL AND closed_at IS NOT NULL AND closed_at >= $1 ORDER BY id`, closedSince); err != nil {
		return nil, nil, nil, fmt.Errorf("cargar las alianzas cerradas: %w", err)
	}
	if rejected, err = r.queryAlliances(ctx,
		`SELECT `+allianceColumns+` FROM clan_alliances
		  WHERE status = 'REJECTED' AND closed_at >= $1 ORDER BY id`, rejectedSince); err != nil {
		return nil, nil, nil, fmt.Errorf("cargar los rechazos de alianzas: %w", err)
	}
	return open, closed, rejected, nil
}

// publicAlliances son las alianzas activas de un clan para su vista pública,
// por id del otro clan (RN-CLANALLY-007).
func publicAlliances(ctx context.Context, db DB, clanID int64) ([]protocol.ClanPublicAllianceView, error) {
	rows, err := db.Query(ctx,
		`SELECT c.id, c.name, c.tag, a.break_effective_at
		   FROM clan_alliances a
		   JOIN clans c ON c.id = CASE WHEN a.clan_a = $1 THEN a.clan_b ELSE a.clan_a END
		  WHERE (a.clan_a = $1 OR a.clan_b = $1) AND a.status = 'ACTIVE'
		  ORDER BY c.id`, clanID)
	if err != nil {
		return nil, fmt.Errorf("leer las alianzas del clan %d: %w", clanID, normalize(err))
	}
	defer rows.Close()
	out := []protocol.ClanPublicAllianceView{}
	for rows.Next() {
		var (
			v       protocol.ClanPublicAllianceView
			breakAt *time.Time
		)
		if err := rows.Scan(&v.ClanID, &v.Name, &v.Tag, &breakAt); err != nil {
			return nil, normalize(err)
		}
		if breakAt != nil {
			ms := breakAt.UnixMilli()
			v.BreaksAtMs = &ms
		}
		out = append(out, v)
	}
	return out, normalize(rows.Err())
}
