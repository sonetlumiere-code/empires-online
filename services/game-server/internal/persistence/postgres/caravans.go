package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/caravan"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Las caravanas (SPEC-CARAVANS, ADR-037). La simulación decide; este archivo
// escribe: la salida, dentro de la transacción del trato (TradeRepo.Accept);
// la llegada, que acredita la carga y borra la unidad; la captura, que cambia
// el dueño de la unidad y le da otro movimiento; y la pérdida.

// CaravanRepo escribe y lee las caravanas que no nacen con un trato.
type CaravanRepo struct {
	store     *Store
	chunkSize int32
}

// NewCaravanRepo crea el repositorio. chunkSize es el de units.chunk_x/y.
func NewCaravanRepo(s *Store, chunkSize int32) *CaravanRepo {
	return &CaravanRepo{store: s, chunkSize: chunkSize}
}

// CaravanSpawn es una de las dos caravanas que crea un trato aceptado: quién la
// manda, de dónde a dónde, qué lleva y el movimiento que ya le asignó la
// simulación, con su id y sin unidad todavía.
type CaravanSpawn struct {
	Sender   uuid.UUID
	FromCity int64
	ToCity   int64
	Cargo    economy.Costs
	Origin   world.Tile
	MaxHP    int32
	Movement movement.Movement
}

// CaravanCreated son los ids que la base dio a una caravana y a su unidad.
type CaravanCreated struct {
	CaravanID int64
	UnitID    int64
}

// insertCaravan crea una caravana en la transacción de su trato: su unidad
// MOVING en el depósito de origen, su movimiento ACTIVE, su fila y su carga
// (RN-CARAVAN-001, INV-UNIT-005, INV-TRADE-007).
func insertCaravan(ctx context.Context, tx DB, chunkSize int32, offerID int64, departedAt time.Time, sp CaravanSpawn) (CaravanCreated, error) {
	if err := caravan.ValidateCargo(sp.Cargo); err != nil {
		return CaravanCreated{}, err
	}
	var out CaravanCreated
	if err := tx.QueryRow(ctx,
		`INSERT INTO units (player_id, city_id, unit_type, x, y, hp, max_hp, status, chunk_x, chunk_y)
		 VALUES ($1, $2, $3, $4, $5, $6, $6, 'MOVING', $7, $8)
		 RETURNING id`,
		sp.Sender, sp.FromCity, string(unit.TypeCaravan), sp.Origin.X, sp.Origin.Y, sp.MaxHP,
		sp.Origin.X/chunkSize, sp.Origin.Y/chunkSize,
	).Scan(&out.UnitID); err != nil {
		return CaravanCreated{}, fmt.Errorf("insertar la unidad de la caravana: %w", normalize(err))
	}
	m := sp.Movement
	m.UnitID = out.UnitID
	if _, _, err := (&MovementRepo{}).Start(ctx, tx, &m); err != nil {
		return CaravanCreated{}, err
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO caravans (unit_id, offer_id, sender_player_id, from_city_id, to_city_id, original_to_city_id, departed_at)
		 VALUES ($1, $2, $3, $4, $5, $5, $6)
		 RETURNING id`,
		out.UnitID, offerID, sp.Sender, sp.FromCity, sp.ToCity, departedAt,
	).Scan(&out.CaravanID); err != nil {
		return CaravanCreated{}, fmt.Errorf("insertar la caravana del trato %d: %w", offerID, normalize(err))
	}
	for _, c := range sp.Cargo {
		if _, err := tx.Exec(ctx,
			`INSERT INTO caravan_cargo (caravan_id, resource, amount) VALUES ($1, $2, $3)`,
			out.CaravanID, string(c.Resource), c.Amount); err != nil {
			return CaravanCreated{}, fmt.Errorf("insertar la carga de la caravana %d: %w", out.CaravanID, normalize(err))
		}
	}
	return out, nil
}

// caravansOfOffer son las caravanas de un trato, en orden de id (la de quien
// propuso, primero): lo que devuelve un reintento de la aceptación.
func caravansOfOffer(ctx context.Context, db DB, offerID int64) ([]CaravanCreated, error) {
	rows, err := db.Query(ctx,
		`SELECT id, COALESCE(unit_id, 0) FROM caravans WHERE offer_id = $1 ORDER BY id`, offerID)
	if err != nil {
		return nil, fmt.Errorf("leer las caravanas del trato %d: %w", offerID, normalize(err))
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

// CaravanDelivery es la llegada de una caravana a su destino.
type CaravanDelivery struct {
	CaravanID int64
	UnitID    int64
	CityID    int64
	// Recipient es el dueño de la ciudad de destino; Counterpart, el dueño de la
	// caravana si es otro (uuid.Nil cuando una caravana capturada llega a la
	// ciudad de su captor).
	Recipient   uuid.UUID
	Counterpart uuid.UUID
	// Stocks son los deltas versionados del almacén del destino: la carga más
	// los depósitos sin volcar de cada recurso.
	Stocks []StockDelta
	Cargo  economy.Costs
	At     world.Tile
	Tick   uint64
	AtMs   int64
}

// Deliver entrega una caravana en UNA transacción (RN-CARAVAN-004,
// INV-TRADE-006): la cierra, borra su unidad, acredita su carga y escribe
// CaravanArrived. Una caravana que ya no está en camino —un reintento cuya
// primera vez se confirmó— no acredita nada. Un delta que la base ya superó es
// ErrStaleStock: la simulación lo reintenta con una versión nueva.
func (r *CaravanRepo) Deliver(ctx context.Context, d CaravanDelivery) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE caravans SET status = 'DELIVERED', closed_at = now(), unit_id = NULL
			  WHERE id = $1 AND status = 'IN_TRANSIT'`, d.CaravanID)
		if err != nil {
			return fmt.Errorf("cerrar la caravana %d: %w", d.CaravanID, normalize(err))
		}
		if tag.RowsAffected() == 0 {
			return nil // ya entregada: un reintento
		}
		if _, err := tx.Exec(ctx, `DELETE FROM units WHERE id = $1`, d.UnitID); err != nil {
			return fmt.Errorf("borrar la unidad de la caravana %d: %w", d.CaravanID, normalize(err))
		}
		for _, s := range d.Stocks {
			aplicado, err := applyStock(ctx, tx, s)
			if err != nil {
				return err
			}
			if !aplicado {
				return ErrStaleStock
			}
		}
		recipient := d.Recipient
		return appendEvent(ctx, tx, worldEvent{
			EventType: eventlog.EventCaravanArrived, Tick: d.Tick, PlayerID: &recipient,
			CounterpartID: counterpart(d.Counterpart),
			Payload:       caravanPayload(d.CaravanID, d.CityID, d.Cargo, d.At, d.AtMs),
		})
	})
}

// CaravanCapture es la captura de una caravana: su nuevo dueño y su nueva
// ruta, o su pérdida si no hay ruta (Movement nil).
type CaravanCapture struct {
	CaravanID int64
	UnitID    int64
	// PreviousOwner la pierde; Captor se la queda, con su ciudad.
	PreviousOwner uuid.UUID
	Captor        uuid.UUID
	CaptorCity    int64
	// OriginalRecipient es el dueño del destino original: si no es ninguno de
	// los dos, se le escribe CaravanIntercepted para que le llegue al registro.
	OriginalRecipient uuid.UUID
	HP                int32
	At                world.Tile
	Movement          *movement.Movement
	Cargo             economy.Costs
	Tick              uint64
	AtMs              int64
}

// Capture escribe una captura en UNA transacción (RN-CARAVAN-006): la unidad
// cambia de dueño, de ciudad y de vida y empieza su movimiento hacia la ciudad
// del captor; la caravana cambia de destino. Sin movimiento, la caravana se
// pierde: se cierra LOST y su unidad se borra. La carga no se toca nunca
// (INV-TRADE-005). Es idempotente: una caravana que ya no está en camino, o un
// movimiento ya escrito, no escriben nada otra vez.
func (r *CaravanRepo) Capture(ctx context.Context, c CaravanCapture) error {
	return r.store.InTx(ctx, func(tx pgx.Tx) error {
		var estado string
		switch err := tx.QueryRow(ctx,
			`SELECT status FROM caravans WHERE id = $1 FOR UPDATE`, c.CaravanID).Scan(&estado); {
		case errors.Is(normalize(err), ErrNotFound):
			return fmt.Errorf("la caravana %d no existe", c.CaravanID)
		case err != nil:
			return fmt.Errorf("leer la caravana %d: %w", c.CaravanID, normalize(err))
		}
		if caravan.Status(estado) != caravan.StatusInTransit {
			return nil
		}

		payload := caravanPayload(c.CaravanID, c.CaptorCity, c.Cargo, c.At, c.AtMs)
		previous, recipient := c.PreviousOwner, c.OriginalRecipient
		event := eventlog.EventCaravanCaptured
		if c.Movement == nil {
			event = eventlog.EventCaravanLost
			if _, err := tx.Exec(ctx,
				`UPDATE caravans SET status = 'LOST', closed_at = now(), unit_id = NULL WHERE id = $1`, c.CaravanID); err != nil {
				return fmt.Errorf("perder la caravana %d: %w", c.CaravanID, normalize(err))
			}
			if _, err := tx.Exec(ctx, `DELETE FROM units WHERE id = $1`, c.UnitID); err != nil {
				return fmt.Errorf("borrar la unidad de la caravana %d: %w", c.CaravanID, normalize(err))
			}
		} else {
			var ya bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM unit_movements WHERE id = $1)`, c.Movement.ID).Scan(&ya); err != nil {
				return fmt.Errorf("leer el movimiento %d: %w", c.Movement.ID, normalize(err))
			}
			if ya {
				return nil // la captura ya se escribió: un reintento
			}
			if _, err := tx.Exec(ctx,
				`UPDATE units SET player_id = $2, city_id = $3, hp = $4, x = $5, y = $6, chunk_x = $7, chunk_y = $8,
				        status = 'MOVING'
				  WHERE id = $1`,
				c.UnitID, c.Captor, c.CaptorCity, c.HP, c.At.X, c.At.Y, c.At.X/r.chunkSize, c.At.Y/r.chunkSize); err != nil {
				return fmt.Errorf("cambiar el dueño de la caravana %d: %w", c.CaravanID, normalize(err))
			}
			m := *c.Movement
			if _, _, err := (&MovementRepo{}).Start(ctx, tx, &m); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`UPDATE caravans SET to_city_id = $2 WHERE id = $1`, c.CaravanID, c.CaptorCity); err != nil {
				return fmt.Errorf("desviar la caravana %d: %w", c.CaravanID, normalize(err))
			}
		}
		if err := appendEvent(ctx, tx, worldEvent{
			EventType: event, Tick: c.Tick, PlayerID: &previous, CounterpartID: counterpart(c.Captor), Payload: payload,
		}); err != nil {
			return err
		}
		if recipient != uuid.Nil && recipient != c.PreviousOwner && recipient != c.Captor {
			return appendEvent(ctx, tx, worldEvent{
				EventType: eventlog.EventCaravanIntercepted, Tick: c.Tick, PlayerID: &recipient,
				CounterpartID: counterpart(c.Captor), Payload: payload,
			})
		}
		return nil
	})
}

// caravanPayload es el payload de los eventos de una caravana (ADR-037 §7):
// qué llevaba, adónde iba y dónde pasó.
func caravanPayload(caravanID, cityID int64, cargo economy.Costs, at world.Tile, atMs int64) map[string]any {
	lineas := make([]map[string]any, 0, len(cargo))
	for _, c := range cargo {
		lineas = append(lineas, map[string]any{"resource": string(c.Resource), "amount": c.Amount})
	}
	p := map[string]any{"caravanId": caravanID, "cityId": cityID, "cargo": lineas, "x": at.X, "y": at.Y}
	if atMs != 0 {
		p["atMs"] = atMs
	}
	return p
}

// ListInTransit carga las caravanas en camino con su carga, en orden de id:
// lo que la simulación recupera al arrancar (ADR-037 §8).
func (r *CaravanRepo) ListInTransit(ctx context.Context) ([]caravan.Caravan, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id, unit_id, offer_id, sender_player_id, from_city_id, to_city_id, original_to_city_id, departed_at
		   FROM caravans WHERE status = 'IN_TRANSIT' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("leer las caravanas en camino: %w", normalize(err))
	}
	var out []caravan.Caravan
	index := make(map[int64]int)
	for rows.Next() {
		c := caravan.Caravan{Status: caravan.StatusInTransit}
		if err := rows.Scan(&c.ID, &c.UnitID, &c.OfferID, &c.Sender, &c.FromCity, &c.ToCity, &c.OriginalToCity,
			&c.DepartedAt); err != nil {
			rows.Close()
			return nil, normalize(err)
		}
		index[c.ID] = len(out)
		out = append(out, c)
	}
	rows.Close()
	if err := normalize(rows.Err()); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	cargo, err := r.store.pool.Query(ctx,
		`SELECT g.caravan_id, g.resource, g.amount
		   FROM caravan_cargo g JOIN caravans k ON k.id = g.caravan_id
		  WHERE k.status = 'IN_TRANSIT'
		  ORDER BY g.caravan_id, g.resource`)
	if err != nil {
		return nil, fmt.Errorf("leer la carga de las caravanas: %w", normalize(err))
	}
	defer cargo.Close()
	for cargo.Next() {
		var (
			id       int64
			resource string
			amount   int64
		)
		if err := cargo.Scan(&id, &resource, &amount); err != nil {
			return nil, normalize(err)
		}
		if i, ok := index[id]; ok {
			out[i].Cargo = append(out[i].Cargo, economy.Cost{Resource: economy.Resource(resource), Amount: amount})
		}
	}
	return out, normalize(cargo.Err())
}

// CaravanViolation es una caravana en camino que no cuadra con su trato o con
// su unidad: el invariante que rompe y por qué.
type CaravanViolation struct {
	CaravanID int64
	InvID     string
	Detail    string
}

// Violations busca, al arrancar, las caravanas en camino que no se pueden
// incorporar (ADR-037 §8): una oferta que no está ACCEPTED o una unidad que no
// es una CARAVAN viva (INV-TRADE-007), y una carga distinta de las líneas de su
// lado del trato (INV-TRADE-005). En orden de caravana.
func (r *CaravanRepo) Violations(ctx context.Context) ([]CaravanViolation, error) {
	rows, err := r.store.pool.Query(ctx, `
		WITH k AS (
		    SELECT c.id, c.unit_id, c.offer_id, o.status AS offer_status,
		           CASE WHEN c.from_city_id = o.proposer_city_id THEN 'GIVE' ELSE 'WANT' END AS side
		      FROM caravans c JOIN trade_offers o ON o.id = c.offer_id
		     WHERE c.status = 'IN_TRANSIT'
		)
		SELECT k.id, 'INV-TRADE-007', 'su oferta está ' || k.offer_status
		  FROM k WHERE k.offer_status <> 'ACCEPTED'
		UNION ALL
		SELECT k.id, 'INV-TRADE-007', 'su unidad no es una CARAVAN viva'
		  FROM k LEFT JOIN units u ON u.id = k.unit_id
		 WHERE u.id IS NULL OR u.unit_type <> 'CARAVAN' OR u.status = 'DEAD'
		UNION ALL
		SELECT k.id, 'INV-TRADE-005', 'su carga no es la de su lado del trato'
		  FROM k
		 WHERE EXISTS (
		       (SELECT resource, amount::bigint FROM caravan_cargo WHERE caravan_id = k.id
		        EXCEPT
		        SELECT resource, amount FROM trade_offer_lines WHERE offer_id = k.offer_id AND side = k.side)
		       UNION ALL
		       (SELECT resource, amount FROM trade_offer_lines WHERE offer_id = k.offer_id AND side = k.side
		        EXCEPT
		        SELECT resource, amount::bigint FROM caravan_cargo WHERE caravan_id = k.id))
		ORDER BY 1, 2`)
	if err != nil {
		return nil, fmt.Errorf("comprobar las caravanas en camino: %w", normalize(err))
	}
	defer rows.Close()
	var out []CaravanViolation
	for rows.Next() {
		var v CaravanViolation
		if err := rows.Scan(&v.CaravanID, &v.InvID, &v.Detail); err != nil {
			return nil, normalize(err)
		}
		out = append(out, v)
	}
	return out, normalize(rows.Err())
}
