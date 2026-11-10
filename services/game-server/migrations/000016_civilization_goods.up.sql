-- SPEC-CIVILIZATION-GOODS (docs/specs/civilization-goods.md), ADR-036: el bien
-- de cada civilización, el taller que lo elabora y la elaboración.
--
-- 1. WINE, SILK, SPICES y FURS son recursos más de city_stocks y de las líneas
--    de un trato. Las ciudades que ya existen nacen con su fila a cero de cada
--    uno, igual que las nuevas (CreateCityStocks recorre economy.Resources()).
-- 2. civilizations.good dice qué bien es de quién: el taller pregunta por él,
--    nunca por el código de la civilización (RN-PLAYER-012, RN-GOODS-001).
-- 3. WORKSHOP es un tipo de edificio más, con un único parcial de un taller en
--    pie por ciudad (INV-ECON-010).
-- 4. city_crafts guarda la elaboración en curso: una por ciudad. No es una fila
--    de city_productions porque convive con el entrenamiento y el avance
--    (RN-GOODS-005, ADR-036 §4). La entrega la borra.
--
-- El avance a IRON_AGE no necesita esquema: city_era_advances admite cualquier
-- era del catálogo y no guarda costes.

ALTER TABLE city_stocks DROP CONSTRAINT city_stocks_resource_valid;
ALTER TABLE city_stocks ADD CONSTRAINT city_stocks_resource_valid
    CHECK (resource IN ('WOOD', 'STONE', 'WINE', 'SILK', 'SPICES', 'FURS'));
INSERT INTO city_stocks (city_id, resource)
    SELECT c.id, r.resource FROM cities c CROSS JOIN (VALUES ('WINE'), ('SILK'), ('SPICES'), ('FURS')) AS r (resource)
    ON CONFLICT (city_id, resource) DO NOTHING;

ALTER TABLE trade_offer_lines DROP CONSTRAINT trade_offer_lines_resource_valid;
ALTER TABLE trade_offer_lines ADD CONSTRAINT trade_offer_lines_resource_valid
    CHECK (resource IN ('WOOD', 'STONE', 'WINE', 'SILK', 'SPICES', 'FURS'));

ALTER TABLE civilizations ADD COLUMN good text;
UPDATE civilizations SET good = 'WINE'   WHERE code = 'ROMAN';
UPDATE civilizations SET good = 'SILK'   WHERE code = 'BYZANTINE';
UPDATE civilizations SET good = 'SPICES' WHERE code = 'PERSIAN';
UPDATE civilizations SET good = 'FURS'   WHERE code = 'NORSE';
ALTER TABLE civilizations ALTER COLUMN good SET NOT NULL;
ALTER TABLE civilizations ADD CONSTRAINT civilizations_good_unique UNIQUE (good);
ALTER TABLE civilizations ADD CONSTRAINT civilizations_good_valid
    CHECK (good IN ('WINE', 'SILK', 'SPICES', 'FURS'));

ALTER TABLE buildings DROP CONSTRAINT buildings_type_valid;
ALTER TABLE buildings ADD CONSTRAINT buildings_type_valid CHECK (type IN ('HOUSE', 'WORKSHOP'));
-- INV-ECON-010: como mucho un taller en pie por ciudad.
CREATE UNIQUE INDEX buildings_one_workshop_per_city ON buildings (city_id)
    WHERE type = 'WORKSHOP' AND status <> 'DESTROYED';

CREATE TABLE city_crafts (
    city_id         bigint      PRIMARY KEY REFERENCES cities (id) ON DELETE CASCADE,
    good            text        NOT NULL,
    amount          integer     NOT NULL,
    completes_at_ms bigint      NOT NULL,
    request_id      text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    -- INV-ECON-009: sólo un bien, y una cantidad positiva.
    CONSTRAINT city_crafts_good_valid CHECK (good IN ('WINE', 'SILK', 'SPICES', 'FURS')),
    CONSTRAINT city_crafts_amount_positive CHECK (amount > 0)
);
