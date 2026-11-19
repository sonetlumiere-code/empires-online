-- Retira el mercado y deja el esquema de 000027.
--
-- Se pierden las ofertas, su custodia y las compras: un servidor anterior no las
-- conoce. Las caravanas de una compra se borran antes, con su carga y su unidad, y
-- los mercados construidos, con sus filas de buildings.

-- Sus unidades CARAVAN primero, con su movimiento (ON DELETE CASCADE); la
-- caravana se queda sin unidad (ON DELETE SET NULL) y se borra después.
DELETE FROM units WHERE id IN (SELECT unit_id FROM caravans WHERE market_purchase_id IS NOT NULL);
DELETE FROM caravan_cargo WHERE caravan_id IN (SELECT id FROM caravans WHERE market_purchase_id IS NOT NULL);
DELETE FROM caravans WHERE market_purchase_id IS NOT NULL;
DROP INDEX caravans_two_per_purchase;
ALTER TABLE caravans DROP CONSTRAINT caravans_one_source;
ALTER TABLE caravans DROP COLUMN market_purchase_id;
ALTER TABLE caravans ALTER COLUMN offer_id SET NOT NULL;

DROP TRIGGER market_purchases_not_own ON market_purchases;
DROP FUNCTION market_purchases_not_own();
DROP TABLE market_purchases;
DROP TABLE market_offers;

DELETE FROM buildings WHERE type = 'MARKET';
DROP INDEX buildings_one_market_per_city;
ALTER TABLE buildings DROP CONSTRAINT buildings_type_valid;
ALTER TABLE buildings ADD CONSTRAINT buildings_type_valid CHECK (type IN ('HOUSE', 'WORKSHOP', 'CASTLE'));
