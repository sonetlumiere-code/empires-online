-- SPEC-ECON-STONE (docs/specs/economy-stone.md), ADR-030: la piedra de las
-- colinas y el avance de una ciudad a la Edad de Bronce.
--
-- 1. La piedra es un recurso más de city_stocks. Las ciudades que ya existen
--    nacen con su fila a cero, igual que las nuevas (CreateCityStocks recorre
--    economy.Resources()).
-- 2. GATHER_STONE es la tarea de recoger piedra; el terreno del tile decide el
--    tipo (RN-ECON-019).
-- 3. hill_stone es la hermana de forest_wood (000008): una fila por tile HILL
--    que alguien ha empezado a picar; un tile sin fila está lleno, y una fila
--    vencida cuenta como llena (ADR-025 §2, ADR-030 §1).
-- 4. city_era_advances guarda los avances confirmados y en curso: una fila
--    por ciudad. No es una fila de city_productions porque el avance no
--    reserva población (INV-CITY-017, ADR-030 §4).

ALTER TABLE city_stocks DROP CONSTRAINT city_stocks_resource_valid;
ALTER TABLE city_stocks ADD CONSTRAINT city_stocks_resource_valid
    CHECK (resource IN ('WOOD', 'STONE'));
INSERT INTO city_stocks (city_id, resource) SELECT id, 'STONE' FROM cities
    ON CONFLICT (city_id, resource) DO NOTHING;

ALTER TABLE unit_tasks DROP CONSTRAINT unit_tasks_kind_valid;
ALTER TABLE unit_tasks ADD CONSTRAINT unit_tasks_kind_valid
    CHECK (kind IN ('GATHER_WOOD', 'GATHER_STONE'));

CREATE TABLE hill_stone (
    x             integer NOT NULL,
    y             integer NOT NULL,
    remaining     integer NOT NULL,
    regrows_at_ms bigint  NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT hill_stone_pkey PRIMARY KEY (x, y),
    -- INV-ECON-007: entre 0 y la capacidad de un tile (deposit.Hill.Capacity).
    CONSTRAINT hill_stone_remaining_range CHECK (remaining BETWEEN 0 AND 100)
);

CREATE TABLE city_era_advances (
    city_id         bigint      PRIMARY KEY REFERENCES cities (id) ON DELETE CASCADE,
    from_era        text        NOT NULL REFERENCES eras (code),
    to_era          text        NOT NULL REFERENCES eras (code),
    completes_at_ms bigint      NOT NULL,
    request_id      text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    -- INV-CITY-018: un avance nunca va hacia atrás ni se queda en el sitio.
    CONSTRAINT city_era_advances_forward CHECK (from_era <> to_era)
);
