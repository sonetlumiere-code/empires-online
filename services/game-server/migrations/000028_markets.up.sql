-- SPEC-MARKETS (docs/specs/markets.md), ADR-047 §7: el mercado.
--
-- 1. MARKET es un tipo más de edificio, como el taller de 000016 y el castillo
--    de 000025, con un único parcial de uno en pie por ciudad.
-- 2. market_offers guarda cada oferta pública con sus lotes. La custodia no se
--    guarda aparte: es lots_left × give_per_lot mientras está OPEN, y cero
--    cerrada (INV-TRADE-008). Al retirarse o caducar, lots_left guarda los
--    lotes devueltos.
-- 3. market_purchases guarda cada compra; un trigger rechaza la del dueño de la
--    oferta (INV-TRADE-010).
-- 4. Una caravana nace de un trato (offer_id) o de una compra
--    (market_purchase_id), exactamente de uno (INV-TRADE-007).
--
-- world_events.event_type no tiene CHECK: los eventos Market* no necesitan
-- migración. Las tablas de diseño markets y trade_transactions no se crean.

ALTER TABLE buildings DROP CONSTRAINT buildings_type_valid;
ALTER TABLE buildings ADD CONSTRAINT buildings_type_valid
    CHECK (type IN ('HOUSE', 'WORKSHOP', 'CASTLE', 'MARKET'));

-- RN-MARKET-001: como mucho un mercado en pie por ciudad.
CREATE UNIQUE INDEX buildings_one_market_per_city ON buildings (city_id)
    WHERE type = 'MARKET' AND status <> 'DESTROYED';

CREATE TABLE market_offers (
    id                bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    player_id         uuid        NOT NULL REFERENCES players (id),
    city_id           bigint      NOT NULL REFERENCES cities (id),
    give_resource     text        NOT NULL,
    give_per_lot      bigint      NOT NULL,
    want_resource     text        NOT NULL,
    want_per_lot      bigint      NOT NULL,
    lots_total        integer     NOT NULL,
    lots_left         integer     NOT NULL,
    seller_commission bigint      NOT NULL,
    status            text        NOT NULL,
    request_id        text        NOT NULL,
    created_at        timestamptz NOT NULL,
    expires_at        timestamptz NOT NULL,
    closed_at         timestamptz NULL,
    CONSTRAINT market_offers_status_valid
        CHECK (status IN ('OPEN', 'SOLD_OUT', 'WITHDRAWN', 'EXPIRED')),
    -- El mismo catálogo que city_stocks (000016).
    CONSTRAINT market_offers_give_resource_valid
        CHECK (give_resource IN ('WOOD', 'STONE', 'WINE', 'SILK', 'SPICES', 'FURS')),
    CONSTRAINT market_offers_want_resource_valid
        CHECK (want_resource IN ('WOOD', 'STONE', 'WINE', 'SILK', 'SPICES', 'FURS')),
    CONSTRAINT market_offers_distinct_resources CHECK (give_resource <> want_resource),
    CONSTRAINT market_offers_amounts_positive CHECK (give_per_lot > 0 AND want_per_lot > 0),
    -- INV-TRADE-009: de 1 a 100 lotes, y nunca más restantes que publicados.
    CONSTRAINT market_offers_lots_valid
        CHECK (lots_total BETWEEN 1 AND 100 AND lots_left BETWEEN 0 AND lots_total),
    CONSTRAINT market_offers_open_has_lots CHECK (status <> 'OPEN' OR lots_left > 0),
    CONSTRAINT market_offers_sold_out_empty CHECK (status <> 'SOLD_OUT' OR lots_left = 0),
    -- La cota técnica de un lado (ADR-032 §7).
    CONSTRAINT market_offers_totals_capped
        CHECK (give_per_lot * lots_total <= 1000000 AND want_per_lot * lots_total <= 1000000),
    -- RN-MARKET-006: la décima parte de cada lote, redondeada hacia arriba.
    CONSTRAINT market_offers_commission_valid
        CHECK (seller_commission = lots_total * ((give_per_lot + 9) / 10)),
    CONSTRAINT market_offers_closed_has_time CHECK ((status = 'OPEN') = (closed_at IS NULL)),
    CONSTRAINT market_offers_expiry_after_creation CHECK (expires_at > created_at),
    CONSTRAINT market_offers_unique_request UNIQUE (player_id, request_id)
);

-- El tablón y la carga del arranque: las abiertas, las más recientes primero.
CREATE INDEX market_offers_open_idx ON market_offers (created_at DESC, id DESC) WHERE status = 'OPEN';

CREATE TABLE market_purchases (
    id               bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    offer_id         bigint      NOT NULL REFERENCES market_offers (id),
    buyer_player_id  uuid        NOT NULL REFERENCES players (id),
    buyer_city_id    bigint      NOT NULL REFERENCES cities (id),
    lots             integer     NOT NULL,
    paid_per_lot     bigint      NOT NULL,
    buyer_commission bigint      NOT NULL,
    request_id       text        NOT NULL,
    occurred_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT market_purchases_lots_valid CHECK (lots BETWEEN 1 AND 100),
    CONSTRAINT market_purchases_paid_positive CHECK (paid_per_lot > 0),
    CONSTRAINT market_purchases_commission_valid CHECK (buyer_commission = lots * ((paid_per_lot + 9) / 10)),
    CONSTRAINT market_purchases_unique_request UNIQUE (buyer_player_id, request_id)
);
CREATE INDEX market_purchases_offer_idx ON market_purchases (offer_id);

-- INV-TRADE-010: nadie se compra a sí mismo.
CREATE FUNCTION market_purchases_not_own() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM market_offers WHERE id = NEW.offer_id AND player_id = NEW.buyer_player_id) THEN
        RAISE EXCEPTION 'el jugador % no puede comprar su propia oferta % (INV-TRADE-010)', NEW.buyer_player_id, NEW.offer_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER market_purchases_not_own
    BEFORE INSERT OR UPDATE OF offer_id, buyer_player_id ON market_purchases
    FOR EACH ROW EXECUTE FUNCTION market_purchases_not_own();

-- INV-TRADE-007: una caravana nace de un trato o de una compra, y cada uno
-- tiene como mucho una por ciudad de salida (dos en total).
ALTER TABLE caravans ALTER COLUMN offer_id DROP NOT NULL;
ALTER TABLE caravans ADD COLUMN market_purchase_id bigint NULL REFERENCES market_purchases (id);
ALTER TABLE caravans ADD CONSTRAINT caravans_one_source
    CHECK (num_nonnulls(offer_id, market_purchase_id) = 1);
CREATE UNIQUE INDEX caravans_two_per_purchase ON caravans (market_purchase_id, from_city_id)
    WHERE market_purchase_id IS NOT NULL;
