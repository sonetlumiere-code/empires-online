package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// WriteAttemptTimeout es lo más que dura un intento de escritura de la cola de
// persistencia: su contexto lo cancela, y la transacción se deshace. El tope
// marcable del registro se calcula a partir de él (ADR-028 §6).
const WriteAttemptTimeout = 10 * time.Second

// EventLogSettle es la edad a partir de la cual una fila de world_events está
// asentada: ninguna transacción con un id menor puede seguir abierta. El doble
// de un intento de la cola más una holgura (ADR-028 §6).
const EventLogSettle = 2*WriteAttemptTimeout + 5*time.Second

const (
	// eventLogChunk es cuántas filas lee cada consulta de una página, y
	// eventLogMaxRows cuántas como mucho por página (ADR-028, consecuencias).
	eventLogChunk   = 500
	eventLogMaxRows = 5000
)

// EventLogConfig parametriza el registro (SPEC-EVENT-LOG §10).
type EventLogConfig struct {
	MaxAge   time.Duration
	PageSize int
	GroupGap time.Duration
}

// EventLogRepo lee el registro de un jugador de world_events y guarda su cursor
// de lo visto (SPEC-EVENT-LOG, ADR-028). Lo usa la conexión, no el loop.
type EventLogRepo struct {
	store *Store
	cfg   EventLogConfig
}

// NewEventLogRepo crea el repositorio.
func NewEventLogRepo(s *Store, cfg EventLogConfig) *EventLogRepo {
	if cfg.PageSize < 1 {
		cfg.PageSize = 50
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 7 * 24 * time.Hour
	}
	if cfg.GroupGap <= 0 {
		cfg.GroupGap = 10 * time.Minute
	}
	return &EventLogRepo{store: s, cfg: cfg}
}

// Page devuelve la página del registro de viewer con los eventos de id menor
// que beforeID (0: la más reciente) y su tope marcable: el mayor id que una
// marca posterior puede dar por visto (INV-PERSIST-006). El orden de las
// consultas importa: el tope se calcula antes de leer las filas, así que toda
// fila con un id menor que él estaba ya confirmada cuando se leyeron.
func (r *EventLogRepo) Page(ctx context.Context, viewer uuid.UUID, beforeID int64) (protocol.EventLogPagePayload, int64, error) {
	page := protocol.EventLogPagePayload{Entries: []protocol.EventLogEntry{}}
	db := r.store.pool

	if err := db.QueryRow(ctx, `SELECT event_log_seen_id FROM players WHERE id = $1`, viewer).
		Scan(&page.SeenUpToID); err != nil {
		return page, 0, fmt.Errorf("leer el cursor del registro de %s: %w", viewer, normalize(err))
	}

	var markable int64
	if err := db.QueryRow(ctx,
		`SELECT GREATEST(
		   COALESCE((SELECT id FROM world_events
		              WHERE player_id = $1 AND occurred_at <= now() - make_interval(secs => $2)
		              ORDER BY occurred_at DESC LIMIT 1), 0),
		   COALESCE((SELECT id FROM world_events
		              WHERE counterpart_player_id = $1 AND occurred_at <= now() - make_interval(secs => $2)
		              ORDER BY occurred_at DESC LIMIT 1), 0))`,
		viewer, EventLogSettle.Seconds()).Scan(&markable); err != nil {
		return page, 0, fmt.Errorf("calcular el tope marcable de %s: %w", viewer, normalize(err))
	}

	b := eventlog.NewBuilder(viewer, page.SeenUpToID, r.cfg.GroupGap, r.cfg.PageSize)
	before := beforeID
	if before <= 0 {
		before = math.MaxInt64
	}
	full, exhausted, read := false, false, 0
	for !full && !exhausted && read < eventLogMaxRows {
		rows, err := r.chunk(ctx, viewer, before)
		if err != nil {
			return page, 0, err
		}
		for _, e := range rows {
			read++
			if !b.Add(e) {
				full = true
				break
			}
			before = e.ID
		}
		if len(rows) < eventLogChunk {
			exhausted = true
		}
	}
	if !exhausted || full {
		if min := b.MinID(); min > 0 {
			page.NextBeforeID = &min
		}
	}

	entries := b.Entries()
	cities, err := r.citiesOf(ctx, entries)
	if err != nil {
		return page, 0, err
	}
	for _, e := range entries {
		page.Entries = append(page.Entries, toView(e, cities))
	}
	return page, markable, nil
}

// chunk lee el siguiente bloque de filas del registro, del id más alto al más
// bajo. INV-SEC-009: sólo las que nombran a viewer en alguna de las dos columnas.
func (r *EventLogRepo) chunk(ctx context.Context, viewer uuid.UUID, before int64) ([]eventlog.Event, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id, event_type, player_id, counterpart_player_id, occurred_at, payload
		   FROM world_events
		  WHERE (player_id = $1 OR counterpart_player_id = $1)
		    AND event_type = ANY($2)
		    AND occurred_at >= now() - make_interval(secs => $3)
		    AND id < $4
		  ORDER BY id DESC
		  LIMIT $5`,
		viewer, eventlog.EventTypes(), r.cfg.MaxAge.Seconds(), before, eventLogChunk)
	if err != nil {
		return nil, fmt.Errorf("leer el registro de %s: %w", viewer, normalize(err))
	}
	defer rows.Close()

	var out []eventlog.Event
	for rows.Next() {
		var (
			e         eventlog.Event
			player    *uuid.UUID
			other     *uuid.UUID
			occurred  time.Time
			rawFields []byte
		)
		if err := rows.Scan(&e.ID, &e.Type, &player, &other, &occurred, &rawFields); err != nil {
			return nil, fmt.Errorf("leer una fila del registro: %w", normalize(err))
		}
		if player != nil {
			e.PlayerID = *player
		}
		if other != nil {
			e.Counterpart = *other
		}
		if err := fillFromPayload(&e, rawFields, other == nil); err != nil {
			return nil, fmt.Errorf("el payload del evento %d: %w", e.ID, err)
		}
		if e.AtMs == 0 {
			e.AtMs = occurred.UnixMilli()
		}
		out = append(out, e)
	}
	return out, normalize(rows.Err())
}

// eventPayload es lo que el registro lee del payload de cada evento; lo que una
// fila no tiene se queda en cero.
type eventPayload struct {
	AtMs             int64      `json:"atMs"`
	UnitType         string     `json:"unitType"`
	Amount           int64      `json:"amount"`
	X                *int32     `json:"x"`
	Y                *int32     `json:"y"`
	TreatyType       string     `json:"treatyType"`
	From             string     `json:"from"`
	ByPlayerID       string     `json:"byPlayerId"`
	BreakEffectiveAt *time.Time `json:"breakEffectiveAt"`
	// Las filas de muertes y saqueos anteriores a ADR-028 no tienen
	// counterpart_player_id, pero su payload ya nombraba al autor.
	KillerPlayerID   string `json:"killerPlayerId"`
	AttackerPlayerID string `json:"attackerPlayerId"`
	// Los tratos (ADR-032 §7).
	OfferID int64         `json:"offerId"`
	Give    []payloadLine `json:"give"`
	Want    []payloadLine `json:"want"`
	// La conquista (SPEC-TERRITORY-CONQUEST §6).
	TerritoryID   int64  `json:"territoryId"`
	TerritoryName string `json:"territoryName"`
	// Las caravanas (SPEC-CARAVANS §6): lo que llevaba.
	Cargo []payloadLine `json:"cargo"`
}

// payloadLine es una línea de un trato en el payload.
type payloadLine struct {
	Resource string `json:"resource"`
	Amount   int64  `json:"amount"`
}

func toLines(ls []payloadLine) []eventlog.Line {
	if len(ls) == 0 {
		return nil
	}
	out := make([]eventlog.Line, len(ls))
	for i, l := range ls {
		out[i] = eventlog.Line{Resource: l.Resource, Amount: l.Amount}
	}
	return out
}

// fillFromPayload copia del payload lo que el registro usa. Con legacy (la fila
// no tiene counterpart_player_id), toma el autor de la muerte o del saqueo del
// payload. No amplía lo que se lee: la fila ya era del jugador por player_id.
func fillFromPayload(e *eventlog.Event, raw []byte, legacy bool) error {
	var p eventPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if legacy {
		autor := p.KillerPlayerID
		if e.Type == eventlog.EventCityPillaged {
			autor = p.AttackerPlayerID
		}
		if id, err := uuid.Parse(autor); err == nil && id != uuid.Nil {
			e.Counterpart = id
		}
	}
	e.AtMs, e.UnitType, e.Amount, e.TreatyType, e.From = p.AtMs, p.UnitType, p.Amount, p.TreatyType, p.From
	if p.X != nil && p.Y != nil {
		e.X, e.Y, e.HasTile = *p.X, *p.Y, true
	}
	if p.ByPlayerID != "" {
		by, err := uuid.Parse(p.ByPlayerID)
		if err != nil {
			return fmt.Errorf("byPlayerId: %w", err)
		}
		e.By = by
	}
	if p.BreakEffectiveAt != nil {
		e.BreaksAtMs = p.BreakEffectiveAt.UnixMilli()
	}
	e.OfferID, e.Give, e.Want = p.OfferID, toLines(p.Give), toLines(p.Want)
	e.TerritoryID, e.TerritoryName = p.TerritoryID, p.TerritoryName
	e.Cargo = toLines(p.Cargo)
	return nil
}

type cityRef struct {
	id   int64
	name string
}

// citiesOf busca la ciudad de cada otro jugador de la página, para nombrarlo
// como los tratados: por su ciudad (SPEC-EVENT-LOG Q6).
func (r *EventLogRepo) citiesOf(ctx context.Context, entries []eventlog.Entry) (map[uuid.UUID]cityRef, error) {
	out := make(map[uuid.UUID]cityRef)
	if len(entries) == 0 {
		return out, nil
	}
	seen := make(map[uuid.UUID]bool)
	var others []uuid.UUID
	for _, e := range entries {
		if !seen[e.Other] {
			seen[e.Other] = true
			others = append(others, e.Other)
		}
	}
	rows, err := r.store.pool.Query(ctx,
		`SELECT DISTINCT ON (owner_player_id) owner_player_id, id, name
		   FROM cities WHERE owner_player_id = ANY($1)
		  ORDER BY owner_player_id, id`, others)
	if err != nil {
		return nil, fmt.Errorf("leer las ciudades del registro: %w", normalize(err))
	}
	defer rows.Close()
	for rows.Next() {
		var (
			owner uuid.UUID
			c     cityRef
		)
		if err := rows.Scan(&owner, &c.id, &c.name); err != nil {
			return nil, fmt.Errorf("leer una ciudad del registro: %w", normalize(err))
		}
		out[owner] = c
	}
	return out, normalize(rows.Err())
}

func fromLines(ls []eventlog.Line) []protocol.ResourceAmount {
	out := make([]protocol.ResourceAmount, len(ls))
	for i, l := range ls {
		out[i] = protocol.ResourceAmount{Resource: l.Resource, Amount: l.Amount}
	}
	return out
}

func toView(e eventlog.Entry, cities map[uuid.UUID]cityRef) protocol.EventLogEntry {
	v := protocol.EventLogEntry{
		ID: e.ID, Kind: string(e.Kind), FirstAtMs: e.FirstAtMs, LastAtMs: e.LastAtMs, Count: e.Count,
		OtherPlayerID: e.Other.String(),
	}
	if c, ok := cities[e.Other]; ok {
		id, name := c.id, c.name
		v.OtherCityID, v.OtherCityName = &id, &name
	}
	switch e.Kind {
	case eventlog.KindUnitLost, eventlog.KindEnemyKilled:
		v.UnitType = e.UnitType
	case eventlog.KindPillaged, eventlog.KindPillageDone:
		amount := e.Amount
		v.Amount = &amount
	case eventlog.KindTreatyOfferExpired:
		mine := e.ProposedByMe
		v.ProposedByMe = &mine
	case eventlog.KindTreatyBreakAnnounced:
		if e.BreaksAtMs > 0 {
			at := e.BreaksAtMs
			v.BreaksAtMs = &at
		}
	case eventlog.KindTradeOfferExpired:
		mine := e.ProposedByMe
		v.ProposedByMe = &mine
	}
	if len(e.TradeGive) > 0 || len(e.TradeGet) > 0 {
		v.TradeGive, v.TradeGet = fromLines(e.TradeGive), fromLines(e.TradeGet)
	}
	switch e.Kind {
	case eventlog.KindUnitLost, eventlog.KindEnemyKilled, eventlog.KindPillaged, eventlog.KindPillageDone,
		eventlog.KindHouseLost, eventlog.KindHouseDestroyed,
		eventlog.KindCaravanArrived, eventlog.KindCaravanCaptured, eventlog.KindCaravanIntercepted, eventlog.KindCaravanLost:
	default:
		v.TreatyType = e.TreatyType
	}
	if e.HasTile {
		x, y := e.X, e.Y
		v.X, v.Y = &x, &y
	}
	if e.TerritoryID != 0 {
		id := e.TerritoryID
		v.TerritoryID, v.TerritoryName = &id, e.TerritoryName
	}
	if len(e.Cargo) > 0 {
		v.Cargo = fromLines(e.Cargo)
	}
	return v
}

// MarkSeen deja el cursor de lo visto de viewer en upToID si es mayor que el
// que tiene: nunca retrocede (INV-PERSIST-006). Quien llama ya lo acotó al
// tope marcable de la última página.
func (r *EventLogRepo) MarkSeen(ctx context.Context, viewer uuid.UUID, upToID int64) error {
	if _, err := r.store.pool.Exec(ctx,
		`UPDATE players SET event_log_seen_id = GREATEST(event_log_seen_id, $2) WHERE id = $1`,
		viewer, upToID); err != nil {
		return fmt.Errorf("marcar el registro de %s: %w", viewer, normalize(err))
	}
	return nil
}
