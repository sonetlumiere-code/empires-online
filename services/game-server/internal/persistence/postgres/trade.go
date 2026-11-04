package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/trade"
)

// El comercio entre dos ciudades (SPEC-TRADE, ADR-032). La simulación decide;
// este repositorio escribe cada transición de una oferta con su world_events y,
// al aceptar, mueve los dos almacenes en la misma transacción.

// Eventos del comercio. Todos llevan player_id de quien propuso y
// counterpart_player_id de quien recibió la oferta (ADR-032 §7).
const (
	EventTradeProposed  = "TradeProposed"
	EventTradeAccepted  = "TradeAccepted"
	EventTradeRejected  = "TradeRejected"
	EventTradeWithdrawn = "TradeWithdrawn"
	EventTradeExpired   = "TradeExpired"
)

// TradeRepo escribe y lee las ofertas de comercio.
type TradeRepo struct{ store *Store }

// NewTradeRepo crea el repositorio.
func NewTradeRepo(s *Store) *TradeRepo { return &TradeRepo{store: s} }

// TradeWrite es una transición de una oferta que no toca almacenes: la
// propuesta, el rechazo, la retirada o la caducidad.
type TradeWrite struct {
	// Insert: una oferta nueva, sin id todavía.
	Insert bool
	// Offer es la oferta tal como queda.
	Offer trade.Offer
	Event string
	Tick  uint64
	// AtMs y By van al payload (ADR-028 §3): el instante en la simulación y
	// quien dio la orden (uuid.Nil en una caducidad).
	AtMs int64
	By   uuid.UUID
	// Reason es el motivo de una caducidad (trade.ExpiryTimeout o
	// trade.ExpiryTreatyEnded).
	Reason string
}

// TradeAcceptance es un trato aceptado con sus deltas versionados, uno por
// ciudad y recurso que toca (ADR-032 §2).
type TradeAcceptance struct {
	// Offer es la oferta ya ACCEPTED, con AcceptedRequestID y ClosedAt.
	Offer  trade.Offer
	Stocks []StockDelta
	Tick   uint64
	AtMs   int64
	By     uuid.UUID
}

// ApplyOffer escribe una transición con su evento y devuelve el id de la
// oferta. Es idempotente: una propuesta repetida con el mismo
// (proposer_id, request_id) devuelve la que ya existe, y un cierre que
// encuentra la fila en el estado de destino termina sin escribir el evento otra
// vez. Un índice de INV-TRADE-003 violado es ErrConflict; una fila en otro
// estado, ErrVersionConflict.
func (r *TradeRepo) ApplyOffer(ctx context.Context, w TradeWrite) (int64, error) {
	var id int64
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		o := w.Offer
		if w.Insert {
			switch err := tx.QueryRow(ctx,
				`SELECT id FROM trade_offers WHERE proposer_id = $1 AND request_id = $2`,
				o.Proposer, o.RequestID).Scan(&id); {
			case err == nil:
				return nil // ya escrita: un reintento
			case !errors.Is(normalize(err), ErrNotFound):
				return fmt.Errorf("buscar la oferta %s: %w", o.RequestID, normalize(err))
			}
			if err := tx.QueryRow(ctx,
				`INSERT INTO trade_offers (proposer_id, target_id, proposer_city_id, target_city_id, status,
				                           request_id, created_at, expires_at)
				 VALUES ($1, $2, $3, $4, 'PROPOSED', $5, $6, $7) RETURNING id`,
				o.Proposer, o.Target, o.ProposerCity, o.TargetCity, o.RequestID, o.CreatedAt, o.ExpiresAt,
			).Scan(&id); err != nil {
				return fmt.Errorf("insertar la oferta: %w", normalize(err))
			}
			for _, side := range []struct {
				side  trade.Side
				lines economy.Costs
			}{{trade.SideGive, o.Give}, {trade.SideWant, o.Want}} {
				for _, l := range side.lines {
					if _, err := tx.Exec(ctx,
						`INSERT INTO trade_offer_lines (offer_id, side, resource, amount) VALUES ($1, $2, $3, $4)`,
						id, string(side.side), string(l.Resource), l.Amount); err != nil {
						return fmt.Errorf("insertar una línea de la oferta %d: %w", id, normalize(err))
					}
				}
			}
			o.ID = id
			return appendEvent(ctx, tx, tradeEvent(w.Event, o, w.Tick, w.AtMs, w.By, w.Reason))
		}

		id = o.ID
		tag, err := tx.Exec(ctx,
			`UPDATE trade_offers SET status = $2, closed_at = $3 WHERE id = $1 AND status = 'PROPOSED'`,
			o.ID, string(o.Status), o.ClosedAt)
		if err != nil {
			return fmt.Errorf("escribir la oferta %d: %w", o.ID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			// O ya estaba escrita (un reintento), o la fila cambió por otra vía.
			var estado string
			if err := tx.QueryRow(ctx, `SELECT status FROM trade_offers WHERE id = $1`, o.ID).Scan(&estado); err != nil {
				return fmt.Errorf("leer la oferta %d: %w", o.ID, normalize(err))
			}
			if trade.Status(estado) == o.Status {
				return nil
			}
			return fmt.Errorf("%w: la oferta %d está en %s", ErrVersionConflict, o.ID, estado)
		}
		return appendEvent(ctx, tx, tradeEvent(w.Event, o, w.Tick, w.AtMs, w.By, w.Reason))
	})
	return id, err
}

// Accept mueve un trato aceptado en UNA transacción (RN-TRADE-004,
// INV-TRADE-001): bloquea la oferta, aplica todos los deltas de los dos
// almacenes, pasa la oferta a ACCEPTED y escribe TradeAccepted. Si un delta no
// se aplica (ErrStaleStock) o dejaría un stock negativo
// (economy.ErrInsufficientResources), no se escribe nada. Un reintento que
// encuentra la oferta ya aceptada por la misma orden termina con éxito; una
// oferta en otro estado es ErrVersionConflict.
func (r *TradeRepo) Accept(ctx context.Context, a TradeAcceptance) error {
	o := a.Offer
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		var estado string
		var aceptadaPor *string
		switch err := tx.QueryRow(ctx,
			`SELECT status, accepted_request_id FROM trade_offers WHERE id = $1 FOR UPDATE`, o.ID,
		).Scan(&estado, &aceptadaPor); {
		case errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("%w: la oferta %d no existe", ErrVersionConflict, o.ID)
		case err != nil:
			return fmt.Errorf("leer la oferta %d: %w", o.ID, normalize(err))
		}
		if trade.Status(estado) == trade.StatusAccepted && aceptadaPor != nil && *aceptadaPor == o.AcceptedRequestID {
			return nil // ya escrito: un reintento
		}
		if trade.Status(estado) != trade.StatusProposed {
			return fmt.Errorf("%w: la oferta %d está en %s", ErrVersionConflict, o.ID, estado)
		}

		for _, d := range a.Stocks {
			aplicado, err := applyStock(ctx, tx, d)
			if err != nil {
				return err
			}
			if !aplicado {
				return ErrStaleStock
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE trade_offers SET status = 'ACCEPTED', accepted_request_id = $2, closed_at = $3 WHERE id = $1`,
			o.ID, o.AcceptedRequestID, o.ClosedAt); err != nil {
			return fmt.Errorf("aceptar la oferta %d: %w", o.ID, normalize(err))
		}
		return appendEvent(ctx, tx, tradeEvent(EventTradeAccepted, o, a.Tick, a.AtMs, a.By, ""))
	})
}

// ListPending devuelve las ofertas PROPOSED con sus líneas, en orden de id: lo
// que la simulación carga al arrancar (ADR-032 §1).
func (r *TradeRepo) ListPending(ctx context.Context) ([]trade.Offer, error) {
	return r.list(ctx, `WHERE status = 'PROPOSED'`)
}

// Get devuelve una oferta con sus líneas (tests y diagnóstico).
func (r *TradeRepo) Get(ctx context.Context, id int64) (trade.Offer, error) {
	os, err := r.list(ctx, `WHERE id = $1`, id)
	if err != nil {
		return trade.Offer{}, err
	}
	if len(os) == 0 {
		return trade.Offer{}, ErrNotFound
	}
	return os[0], nil
}

func (r *TradeRepo) list(ctx context.Context, where string, args ...any) ([]trade.Offer, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id, proposer_id, target_id, proposer_city_id, target_city_id, status, request_id,
		        accepted_request_id, created_at, expires_at, closed_at
		   FROM trade_offers `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("leer las ofertas: %w", normalize(err))
	}
	var out []trade.Offer
	index := map[int64]int{}
	for rows.Next() {
		var o trade.Offer
		var estado string
		var aceptadaPor *string
		var cerrada *time.Time
		if err := rows.Scan(&o.ID, &o.Proposer, &o.Target, &o.ProposerCity, &o.TargetCity, &estado, &o.RequestID,
			&aceptadaPor, &o.CreatedAt, &o.ExpiresAt, &cerrada); err != nil {
			rows.Close()
			return nil, fmt.Errorf("leer una oferta: %w", normalize(err))
		}
		o.Status = trade.Status(estado)
		if aceptadaPor != nil {
			o.AcceptedRequestID = *aceptadaPor
		}
		if cerrada != nil {
			c := cerrada.UTC()
			o.ClosedAt = &c
		}
		o.CreatedAt, o.ExpiresAt = o.CreatedAt.UTC(), o.ExpiresAt.UTC()
		index[o.ID] = len(out)
		out = append(out, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("leer las ofertas: %w", normalize(err))
	}
	if len(out) == 0 {
		return out, nil
	}

	ids := make([]int64, 0, len(out))
	for _, o := range out {
		ids = append(ids, o.ID)
	}
	lines, err := r.store.pool.Query(ctx,
		`SELECT offer_id, side, resource, amount FROM trade_offer_lines
		  WHERE offer_id = ANY($1) ORDER BY offer_id, resource`, ids)
	if err != nil {
		return nil, fmt.Errorf("leer las líneas de las ofertas: %w", normalize(err))
	}
	defer lines.Close()
	for lines.Next() {
		var id, amount int64
		var side, resource string
		if err := lines.Scan(&id, &side, &resource, &amount); err != nil {
			return nil, fmt.Errorf("leer una línea: %w", normalize(err))
		}
		o := &out[index[id]]
		l := economy.Cost{Resource: economy.Resource(resource), Amount: amount}
		if trade.Side(side) == trade.SideGive {
			o.Give = append(o.Give, l)
		} else {
			o.Want = append(o.Want, l)
		}
	}
	return out, lines.Err()
}

// tradeEvent es el world_events de una transición (ADR-032 §7). player_id es
// quien propuso; counterpart_player_id, quien recibió la oferta.
func tradeEvent(eventType string, o trade.Offer, tick uint64, atMs int64, by uuid.UUID, reason string) worldEvent {
	lineas := func(cs economy.Costs) []map[string]any {
		out := make([]map[string]any, 0, len(cs))
		for _, c := range cs {
			out = append(out, map[string]any{"resource": string(c.Resource), "amount": c.Amount})
		}
		return out
	}
	payload := map[string]any{
		"offerId": o.ID, "proposerCityId": o.ProposerCity, "targetCityId": o.TargetCity,
		"give": lineas(o.Give), "want": lineas(o.Want), "status": string(o.Status),
	}
	if atMs != 0 {
		payload["atMs"] = atMs
	}
	if by != uuid.Nil {
		payload["byPlayerId"] = by.String()
	}
	if reason != "" {
		payload["reason"] = reason
	}
	proposer, target := o.Proposer, o.Target
	return worldEvent{EventType: eventType, Tick: tick, PlayerID: &proposer, CounterpartID: &target, Payload: payload}
}
