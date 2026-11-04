-- SPEC-TRADE (docs/specs/trade.md), ADR-032: el comercio entre dos ciudades.
--
-- 1. trade_offers guarda cada oferta «doy esto, pido aquello» de un jugador a
--    otro y su ciclo de vida. La RAM de la simulación decide las órdenes; la
--    base es su registro durable y la última barrera de INV-TRADE-003.
-- 2. trade_offer_lines guarda lo que da (GIVE) y lo que pide (WANT). La PK
--    (offer_id, resource) impide un recurso en los dos lados (SPEC-TRADE Q4).
--
-- treaties no cambia: admite TRADE desde 000001. world_events.event_type no
-- tiene CHECK, así que los eventos Trade* no necesitan migración.

CREATE TABLE trade_offers (
    id                  bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    proposer_id         uuid        NOT NULL REFERENCES players (id),
    target_id           uuid        NOT NULL REFERENCES players (id),
    proposer_city_id    bigint      NOT NULL REFERENCES cities (id),
    target_city_id      bigint      NOT NULL REFERENCES cities (id),
    status              text        NOT NULL,
    request_id          text        NOT NULL,
    accepted_request_id text        NULL,
    created_at          timestamptz NOT NULL,
    expires_at          timestamptz NOT NULL,
    closed_at           timestamptz NULL,
    CONSTRAINT trade_offers_status_valid
        CHECK (status IN ('PROPOSED', 'ACCEPTED', 'REJECTED', 'WITHDRAWN', 'EXPIRED')),
    CONSTRAINT trade_offers_distinct_players CHECK (proposer_id <> target_id),
    CONSTRAINT trade_offers_expiry_after_creation CHECK (expires_at > created_at),
    -- Una oferta cerrada sabe cuándo se cerró; una aceptada, con qué orden.
    CONSTRAINT trade_offers_closed_has_time
        CHECK ((status = 'PROPOSED') = (closed_at IS NULL)),
    CONSTRAINT trade_offers_accepted_has_request
        CHECK ((status = 'ACCEPTED') = (accepted_request_id IS NOT NULL))
);

-- INV-TRADE-003: como mucho una oferta pendiente por par, la proponga quien la
-- proponga.
CREATE UNIQUE INDEX trade_offers_one_pending_per_pair
    ON trade_offers (least(proposer_id, target_id), greatest(proposer_id, target_id))
    WHERE status = 'PROPOSED';

-- Idempotencia de trade.propose (ADR-032 §1).
CREATE UNIQUE INDEX trade_offers_unique_request ON trade_offers (proposer_id, request_id);

-- La carga al arrancar lee las pendientes.
CREATE INDEX trade_offers_pending_idx ON trade_offers (id) WHERE status = 'PROPOSED';

CREATE TABLE trade_offer_lines (
    offer_id bigint NOT NULL REFERENCES trade_offers (id) ON DELETE CASCADE,
    side     text   NOT NULL,
    resource text   NOT NULL,
    amount   bigint NOT NULL,
    PRIMARY KEY (offer_id, resource),
    CONSTRAINT trade_offer_lines_side_valid CHECK (side IN ('GIVE', 'WANT')),
    -- El mismo catálogo que city_stocks_resource_valid: un recurso nuevo toca
    -- los dos.
    CONSTRAINT trade_offer_lines_resource_valid CHECK (resource IN ('WOOD', 'STONE')),
    -- SPEC-TRADE Q3: nada de regalos; la cota es técnica (ADR-032 §7).
    CONSTRAINT trade_offer_lines_amount_positive CHECK (amount > 0 AND amount <= 1000000)
);
