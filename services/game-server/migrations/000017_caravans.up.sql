-- SPEC-CARAVANS (docs/specs/caravans.md), ADR-037: un trato aceptado sale en dos
-- caravanas, una por sentido, que recorren el mapa hasta la otra ciudad.
--
-- La caravana es una unidad (units.unit_type = 'CARAVAN', sin CHECK de tipo en
-- units desde 000001): su posición, su movimiento y su vida viven en units y
-- unit_movements. Esta fila guarda lo demás: de qué trato nace, quién la mandó,
-- de dónde sale, adónde va ahora (to_city_id, la del captor tras una captura) y
-- adónde iba al salir. La carga va en caravan_cargo, una fila por recurso, y no
-- cambia en el camino (INV-TRADE-005).
--
-- Al llegar o perderse, la fila se cierra y su unidad se borra en la misma
-- transacción: unit_id queda a NULL. CHECK caravans_unit_while_in_transit:
-- tiene unidad si y sólo si está en camino.

CREATE TABLE caravans (
    id                  bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    unit_id             bigint      REFERENCES units (id) ON DELETE SET NULL,
    offer_id            bigint      NOT NULL REFERENCES trade_offers (id),
    sender_player_id    uuid        NOT NULL REFERENCES players (id),
    from_city_id        bigint      NOT NULL REFERENCES cities (id),
    to_city_id          bigint      NOT NULL REFERENCES cities (id),
    original_to_city_id bigint      NOT NULL REFERENCES cities (id),
    status              text        NOT NULL DEFAULT 'IN_TRANSIT',
    departed_at         timestamptz NOT NULL,
    closed_at           timestamptz,

    CONSTRAINT caravans_status_valid CHECK (status IN ('IN_TRANSIT', 'DELIVERED', 'LOST')),
    CONSTRAINT caravans_unit_while_in_transit CHECK ((status = 'IN_TRANSIT') = (unit_id IS NOT NULL)),
    CONSTRAINT caravans_closed_when_done CHECK ((status = 'IN_TRANSIT') = (closed_at IS NULL))
);

-- Una unidad lleva como mucho una caravana en camino.
CREATE UNIQUE INDEX caravans_one_per_unit ON caravans (unit_id) WHERE status = 'IN_TRANSIT';
-- INV-TRADE-007: como mucho una caravana por sentido de cada trato.
CREATE UNIQUE INDEX caravans_two_per_offer ON caravans (offer_id, from_city_id);
-- La carga del arranque: las que siguen en camino.
CREATE INDEX caravans_in_transit_idx ON caravans (id) WHERE status = 'IN_TRANSIT';

CREATE TABLE caravan_cargo (
    caravan_id bigint  NOT NULL REFERENCES caravans (id) ON DELETE CASCADE,
    resource   text    NOT NULL,
    amount     integer NOT NULL,

    PRIMARY KEY (caravan_id, resource),
    CONSTRAINT caravan_cargo_amount_positive CHECK (amount > 0),
    -- El mismo catálogo que city_stocks y trade_offer_lines (000016).
    CONSTRAINT caravan_cargo_resource_valid
        CHECK (resource IN ('WOOD', 'STONE', 'WINE', 'SILK', 'SPICES', 'FURS'))
);
