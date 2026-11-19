package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/market"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// El mercado (SPEC-MARKETS, ADR-047). La simulación decide; este repositorio
// escribe la publicación con su débito, la compra con el del comprador y sus dos
// caravanas, y el cierre con su devolución, cada uno en una transacción con su
// world_events. La conexión lee el tablón (§6 del ADR).

// Eventos del mercado. player_id es siempre el vendedor; la compra lleva al
// comprador como counterpart_player_id (ADR-047 §4).
const (
	EventMarketOfferPosted    = "MarketOfferPosted"
	EventMarketPurchase       = "MarketPurchase"
	EventMarketOfferWithdrawn = "MarketOfferWithdrawn"
	EventMarketOfferExpired   = "MarketOfferExpired"
)

// MarketBoardLimit es el máximo de ofertas de una lectura del tablón.
const MarketBoardLimit = 100

// MarketRepo escribe y lee el mercado.
type MarketRepo struct {
	store *Store
	// chunkSize es el de units.chunk_x/y, para las caravanas de una compra.
	chunkSize int32
}

// NewMarketRepo crea el repositorio. chunkSize es el del mundo.
func NewMarketRepo(s *Store, chunkSize int32) *MarketRepo {
	return &MarketRepo{store: s, chunkSize: chunkSize}
}

// MarketPost es una publicación con su débito versionado: la custodia de todos
// los lotes más la comisión del vendedor, con los depósitos sin volcar que
// viajan con ella (ADR-047 §1).
type MarketPost struct {
	Offer market.Offer
	Stock StockDelta
	Tick  uint64
	AtMs  int64
}

// MarketPurchase es una compra con el débito versionado del comprador y las dos
// caravanas (ADR-047 §2). Offer es la oferta antes de la compra.
type MarketPurchase struct {
	Offer     market.Offer
	Buyer     uuid.UUID
	BuyerCity int64
	RequestID string
	Purchase  market.Purchase
	Stock     StockDelta
	Caravans  []CaravanSpawn
	Tick      uint64
	AtMs      int64
}

// MarketClose es la retirada o la caducidad de una oferta, con el crédito
// versionado de lo que vuelve al almacén del vendedor (ADR-047 §3). Offer es la
// oferta ya cerrada: su estado de destino, ClosedAt y, en LotsLeft, los lotes
// devueltos.
type MarketClose struct {
	Offer    market.Offer
	Returned economy.Cost
	Stock    StockDelta
	Tick     uint64
	AtMs     int64
	// By es quien la retiró; uuid.Nil en una caducidad.
	By uuid.UUID
}

// Post publica una oferta en UNA transacción (RN-MARKET-003, INV-TRADE-008):
// aplica el débito, inserta la oferta OPEN y escribe MarketOfferPosted.
// Devuelve el id. Una publicación repetida con el mismo (player_id, request_id)
// devuelve la que ya existe sin debitar otra vez (INV-SEC-007). Un delta ya
// superado es ErrStaleStock; uno que dejaría el stock en negativo,
// economy.ErrInsufficientResources.
func (r *MarketRepo) Post(ctx context.Context, p MarketPost) (int64, error) {
	var id int64
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		o := p.Offer
		switch err := tx.QueryRow(ctx,
			`SELECT id FROM market_offers WHERE player_id = $1 AND request_id = $2`, o.Seller, o.RequestID,
		).Scan(&id); {
		case err == nil:
			return nil // ya escrita: un reintento
		case !errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("buscar la oferta %s: %w", o.RequestID, normalize(err))
		}
		aplicado, err := applyStock(ctx, tx, p.Stock)
		if err != nil {
			return err
		}
		if !aplicado {
			return ErrStaleStock
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO market_offers (player_id, city_id, give_resource, give_per_lot, want_resource, want_per_lot,
			                            lots_total, lots_left, seller_commission, status, request_id, created_at, expires_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8, 'OPEN', $9, $10, $11) RETURNING id`,
			o.Seller, o.CityID, string(o.Give.Resource), o.Give.Amount, string(o.Want.Resource), o.Want.Amount,
			o.LotsTotal, o.SellerCommission, o.RequestID, o.CreatedAt, o.ExpiresAt,
		).Scan(&id); err != nil {
			return fmt.Errorf("insertar la oferta: %w", normalize(err))
		}
		o.ID = id
		seller := o.Seller
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventMarketOfferPosted, Tick: p.Tick, PlayerID: &seller,
			Payload: marketPayload(o, p.AtMs, seller, map[string]any{
				"lots":             o.LotsTotal,
				"give":             []map[string]any{line(o.PostCost().Resource, int64(o.LotsTotal)*o.Give.Amount)},
				"sellerCommission": o.SellerCommission,
			}),
		})
	})
	return id, err
}

// Buy escribe una compra en UNA transacción (RN-MARKET-005, INV-TRADE-008,
// INV-TRADE-009): descuenta los lotes de la oferta condicionada a que siga
// abierta con lotes suficientes, aplica el débito del comprador, inserta la
// compra, crea las dos caravanas y escribe MarketPurchase. Devuelve el id de la
// compra y sus caravanas en el orden de b.Caravans. Una compra repetida con el
// mismo (buyer_player_id, request_id) devuelve lo que ya escribió. Una oferta
// que ya no admite la compra es ErrVersionConflict.
func (r *MarketRepo) Buy(ctx context.Context, b MarketPurchase) (int64, []CaravanCreated, error) {
	var (
		purchaseID int64
		created    []CaravanCreated
	)
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		created = nil
		switch err := tx.QueryRow(ctx,
			`SELECT id FROM market_purchases WHERE buyer_player_id = $1 AND request_id = $2`, b.Buyer, b.RequestID,
		).Scan(&purchaseID); {
		case err == nil:
			var err error
			created, err = caravansOfPurchase(ctx, tx, purchaseID)
			return err // ya escrita: un reintento
		case !errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("buscar la compra %s: %w", b.RequestID, normalize(err))
		}

		o, p := b.Offer, b.Purchase
		closedAt := time.UnixMilli(b.AtMs).UTC()
		tag, err := tx.Exec(ctx,
			`UPDATE market_offers
			    SET lots_left = lots_left - $2,
			        status = CASE WHEN lots_left - $2 = 0 THEN 'SOLD_OUT' ELSE status END,
			        closed_at = CASE WHEN lots_left - $2 = 0 THEN $3::timestamptz ELSE closed_at END
			  WHERE id = $1 AND status = 'OPEN' AND lots_left >= $2`,
			o.ID, p.Lots, closedAt)
		if err != nil {
			return fmt.Errorf("descontar los lotes de la oferta %d: %w", o.ID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: la oferta %d ya no admite %d lotes", ErrVersionConflict, o.ID, p.Lots)
		}
		aplicado, err := applyStock(ctx, tx, b.Stock)
		if err != nil {
			return err
		}
		if !aplicado {
			return ErrStaleStock
		}
		if err := tx.QueryRow(ctx,
			`INSERT INTO market_purchases (offer_id, buyer_player_id, buyer_city_id, lots, paid_per_lot,
			                               buyer_commission, request_id, occurred_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			o.ID, b.Buyer, b.BuyerCity, p.Lots, o.Want.Amount, p.Commission, b.RequestID, closedAt,
		).Scan(&purchaseID); err != nil {
			return fmt.Errorf("insertar la compra: %w", normalize(err))
		}
		ids := make([]int64, 0, len(b.Caravans))
		for _, sp := range b.Caravans {
			c, err := insertCaravan(ctx, tx, r.chunkSize, caravanSource{purchaseID: purchaseID}, closedAt, sp)
			if err != nil {
				return err
			}
			created = append(created, c)
			ids = append(ids, c.CaravanID)
		}
		seller, buyer := o.Seller, b.Buyer
		return appendEvent(ctx, tx, worldEvent{
			EventType: EventMarketPurchase, Tick: b.Tick, PlayerID: &seller, CounterpartID: &buyer,
			Payload: marketPayload(o, b.AtMs, buyer, map[string]any{
				"purchaseId":      purchaseID,
				"lots":            p.Lots,
				"give":            []map[string]any{line(p.Receive.Resource, p.Receive.Amount)},
				"want":            []map[string]any{line(p.Pay.Resource, p.Pay.Amount)},
				"buyerCommission": p.Commission,
				"buyerCityId":     b.BuyerCity,
				"caravanIds":      ids,
			}),
		})
	})
	if err != nil {
		return 0, nil, err
	}
	return purchaseID, created, nil
}

// Close retira o caduca una oferta en UNA transacción (RN-MARKET-007,
// RN-MARKET-008, INV-TRADE-008): la cierra condicionada a que siga abierta con
// los lotes que la simulación devuelve, acredita lo devuelto y escribe su
// evento. Un cierre que encuentra la oferta ya en su estado de destino —un
// reintento cuya primera vez se confirmó— no acredita otra vez. Un delta ya
// superado es ErrStaleStock: quien llama lo reintenta con una versión nueva.
func (r *MarketRepo) Close(ctx context.Context, c MarketClose) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		o := c.Offer
		tag, err := tx.Exec(ctx,
			`UPDATE market_offers SET status = $2, closed_at = $3 WHERE id = $1 AND status = 'OPEN' AND lots_left = $4`,
			o.ID, string(o.Status), o.ClosedAt, o.LotsLeft)
		if err != nil {
			return fmt.Errorf("cerrar la oferta %d: %w", o.ID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			var estado string
			if err := tx.QueryRow(ctx, `SELECT status FROM market_offers WHERE id = $1`, o.ID).Scan(&estado); err != nil {
				return fmt.Errorf("leer la oferta %d: %w", o.ID, normalize(err))
			}
			if market.Status(estado) == o.Status {
				return nil // ya escrita: un reintento
			}
			return fmt.Errorf("%w: la oferta %d está en %s", ErrVersionConflict, o.ID, estado)
		}
		if c.Returned.Amount > 0 {
			aplicado, err := applyStock(ctx, tx, c.Stock)
			if err != nil {
				return err
			}
			if !aplicado {
				return ErrStaleStock
			}
		}
		event := EventMarketOfferWithdrawn
		if o.Status == market.StatusExpired {
			event = EventMarketOfferExpired
		}
		seller := o.Seller
		return appendEvent(ctx, tx, worldEvent{
			EventType: event, Tick: c.Tick, PlayerID: &seller,
			Payload: marketPayload(o, c.AtMs, c.By, map[string]any{
				"lots": o.LotsLeft,
				"give": []map[string]any{line(c.Returned.Resource, c.Returned.Amount)},
			}),
		})
	})
}

// ListOpen devuelve las ofertas abiertas, en orden de id: lo que la simulación
// carga al arrancar.
func (r *MarketRepo) ListOpen(ctx context.Context) ([]market.Offer, error) {
	return r.list(ctx, `WHERE status = 'OPEN'`)
}

// Get devuelve una oferta (tests y diagnóstico).
func (r *MarketRepo) Get(ctx context.Context, id int64) (market.Offer, error) {
	os, err := r.list(ctx, `WHERE id = $1`, id)
	if err != nil {
		return market.Offer{}, err
	}
	if len(os) == 0 {
		return market.Offer{}, ErrNotFound
	}
	return os[0], nil
}

func (r *MarketRepo) list(ctx context.Context, where string, args ...any) ([]market.Offer, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id, player_id, city_id, give_resource, give_per_lot, want_resource, want_per_lot,
		        lots_total, lots_left, seller_commission, status, request_id, created_at, expires_at, closed_at
		   FROM market_offers `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("leer las ofertas del mercado: %w", normalize(err))
	}
	defer rows.Close()
	var out []market.Offer
	for rows.Next() {
		var (
			o            market.Offer
			give, want   string
			estado       string
			closedAt     *time.Time
			created, exp time.Time
		)
		if err := rows.Scan(&o.ID, &o.Seller, &o.CityID, &give, &o.Give.Amount, &want, &o.Want.Amount,
			&o.LotsTotal, &o.LotsLeft, &o.SellerCommission, &estado, &o.RequestID, &created, &exp, &closedAt); err != nil {
			return nil, normalize(err)
		}
		o.Give.Resource, o.Want.Resource, o.Status = economy.Resource(give), economy.Resource(want), market.Status(estado)
		o.CreatedAt, o.ExpiresAt = created.UTC(), exp.UTC()
		if closedAt != nil {
			o.ClosedAt = closedAt.UTC()
		}
		out = append(out, o)
	}
	return out, normalize(rows.Err())
}

// Board lee el tablón para viewer (ADR-047 §6): hasta MarketBoardLimit ofertas
// abiertas de otros jugadores, las más recientes primero, con la ciudad, su
// civilización y su posición. receive filtra por lo que da la oferta (lo que
// recibe quien mira) y pay por lo que pide; "" no filtra.
func (r *MarketRepo) Board(ctx context.Context, viewer uuid.UUID, receive, pay string) ([]protocol.MarketOfferView, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT o.id, o.player_id, o.city_id, c.name, cv.code, c.center_x, c.center_y,
		        o.give_resource, o.give_per_lot, o.want_resource, o.want_per_lot,
		        o.lots_total, o.lots_left, o.status, o.expires_at
		   FROM market_offers o
		   LEFT JOIN cities c ON c.id = o.city_id
		   LEFT JOIN players p ON p.id = o.player_id
		   LEFT JOIN civilizations cv ON cv.id = p.civilization_id
		  WHERE o.status = 'OPEN' AND o.player_id <> $1
		    AND ($2 = '' OR o.give_resource = $2)
		    AND ($3 = '' OR o.want_resource = $3)
		  ORDER BY o.created_at DESC, o.id DESC
		  LIMIT $4`,
		viewer, receive, pay, MarketBoardLimit)
	if err != nil {
		return nil, fmt.Errorf("leer el tablón del mercado: %w", normalize(err))
	}
	defer rows.Close()
	out := make([]protocol.MarketOfferView, 0)
	for rows.Next() {
		var (
			v          protocol.MarketOfferView
			seller     uuid.UUID
			name, civ  *string
			cx, cy     *int32
			give, want string
			exp        time.Time
		)
		if err := rows.Scan(&v.ID, &seller, &v.CityID, &name, &civ, &cx, &cy, &give, &v.Give.Amount, &want,
			&v.Want.Amount, &v.LotsTotal, &v.LotsLeft, &v.Status, &exp); err != nil {
			return nil, normalize(err)
		}
		v.PlayerID, v.CityName, v.Civilization, v.CityX, v.CityY = seller.String(), name, civ, cx, cy
		v.Give.Resource, v.Want.Resource = give, want
		v.LotsSold = v.LotsTotal - v.LotsLeft
		v.ExpiresAtMs = exp.UnixMilli()
		out = append(out, v)
	}
	return out, normalize(rows.Err())
}

// MarketViolation es una oferta abierta que no se puede incorporar al arrancar.
type MarketViolation struct {
	OfferID int64
	InvID   string
	Detail  string
}

// Violations busca, al arrancar, las ofertas abiertas cuyos lotes no cuadran con
// sus compras (INV-TRADE-009). En orden de oferta.
func (r *MarketRepo) Violations(ctx context.Context) ([]MarketViolation, error) {
	rows, err := r.store.pool.Query(ctx, `
		SELECT o.id, 'INV-TRADE-009',
		       'publicó ' || o.lots_total || ' lotes, le quedan ' || o.lots_left || ' y vendió ' || COALESCE(SUM(p.lots), 0)
		  FROM market_offers o LEFT JOIN market_purchases p ON p.offer_id = o.id
		 WHERE o.status = 'OPEN'
		 GROUP BY o.id, o.lots_total, o.lots_left
		HAVING COALESCE(SUM(p.lots), 0) + o.lots_left <> o.lots_total
		 ORDER BY o.id`)
	if err != nil {
		return nil, fmt.Errorf("comprobar las ofertas del mercado: %w", normalize(err))
	}
	defer rows.Close()
	var out []MarketViolation
	for rows.Next() {
		var v MarketViolation
		if err := rows.Scan(&v.OfferID, &v.InvID, &v.Detail); err != nil {
			return nil, normalize(err)
		}
		out = append(out, v)
	}
	return out, normalize(rows.Err())
}

// caravansOfPurchase son las caravanas de una compra, en orden de id (la del
// vendedor, primero): lo que devuelve un reintento.
func caravansOfPurchase(ctx context.Context, db DB, purchaseID int64) ([]CaravanCreated, error) {
	rows, err := db.Query(ctx,
		`SELECT id, COALESCE(unit_id, 0) FROM caravans WHERE market_purchase_id = $1 ORDER BY id`, purchaseID)
	if err != nil {
		return nil, fmt.Errorf("leer las caravanas de la compra %d: %w", purchaseID, normalize(err))
	}
	defer rows.Close()
	var out []CaravanCreated
	for rows.Next() {
		var c CaravanCreated
		if err := rows.Scan(&c.CaravanID, &c.UnitID); err != nil {
			return nil, normalize(err)
		}
		out = append(out, c)
	}
	return out, normalize(rows.Err())
}

func line(r economy.Resource, amount int64) map[string]any {
	return map[string]any{"resource": string(r), "amount": amount}
}

// marketPayload es lo común del payload de un evento del mercado.
func marketPayload(o market.Offer, atMs int64, by uuid.UUID, extra map[string]any) map[string]any {
	p := map[string]any{
		"offerId": o.ID, "cityId": o.CityID, "status": string(o.Status),
		"giveLot": line(o.Give.Resource, o.Give.Amount), "wantLot": line(o.Want.Resource, o.Want.Amount),
	}
	if atMs != 0 {
		p["atMs"] = atMs
	}
	if by != uuid.Nil {
		p["byPlayerId"] = by.String()
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}
