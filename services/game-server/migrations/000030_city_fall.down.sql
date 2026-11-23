-- Retira la caída de una ciudad y deja el esquema de 000029.
--
-- Se pierden los asaltos y su historia. Las ciudades caídas no pueden volver al
-- esquema anterior, que exige un centro único entre todas las filas y no sabe
-- distinguir una ciudad caída: se borran, con lo que cuelga de ellas en
-- cascada (edificios, almacén, encargos) y lo que las referencia sin cascada
-- (tratos, caravanas, ofertas del mercado, reclamaciones), que es sólo historia.
-- Sus jugadores conservan la ciudad refundada.

DROP TABLE city_assaults;

CREATE TEMPORARY TABLE fallen_cities AS SELECT id FROM cities WHERE fallen_at IS NOT NULL;
CREATE TEMPORARY TABLE fallen_purchases AS
    SELECT p.id FROM market_purchases p JOIN market_offers o ON o.id = p.offer_id
     WHERE o.city_id IN (SELECT id FROM fallen_cities) OR p.buyer_city_id IN (SELECT id FROM fallen_cities);

DELETE FROM territory_claims WHERE city_id IN (SELECT id FROM fallen_cities);
DELETE FROM caravans
 WHERE from_city_id IN (SELECT id FROM fallen_cities)
    OR to_city_id IN (SELECT id FROM fallen_cities)
    OR original_to_city_id IN (SELECT id FROM fallen_cities)
    OR market_purchase_id IN (SELECT id FROM fallen_purchases);
DELETE FROM market_purchases WHERE id IN (SELECT id FROM fallen_purchases);
DELETE FROM market_offers WHERE city_id IN (SELECT id FROM fallen_cities);
DELETE FROM trade_offers
 WHERE proposer_city_id IN (SELECT id FROM fallen_cities) OR target_city_id IN (SELECT id FROM fallen_cities);
DELETE FROM cities WHERE fallen_at IS NOT NULL;
DROP TABLE fallen_purchases;
DROP TABLE fallen_cities;

DROP TRIGGER cities_fallen_is_final ON cities;
DROP FUNCTION cities_fallen_is_final();
DROP INDEX cities_one_standing_per_owner;
DROP INDEX cities_unique_center;
ALTER TABLE cities ADD CONSTRAINT cities_unique_center UNIQUE (center_x, center_y);
ALTER TABLE cities DROP COLUMN fallen_at;
