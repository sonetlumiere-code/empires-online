-- Retira los bienes de civilización y deja el esquema de 000015.
--
-- Se pierden los bienes de todos los almacenes, los talleres (también su
-- historia) y las elaboraciones en curso, y las ofertas que daban o pedían
-- bienes, con sus líneas. Las ciudades que llegaron a la Edad de Hierro se
-- quedan en ella: IRON_AGE existe desde 000002. Un avance a Hierro en curso se
-- quedaría sin paso en un servidor anterior, así que se borra.

DROP TABLE city_crafts;

DELETE FROM city_era_advances WHERE to_era = 'IRON_AGE';

DROP INDEX buildings_one_workshop_per_city;
DELETE FROM buildings WHERE type = 'WORKSHOP';
ALTER TABLE buildings DROP CONSTRAINT buildings_type_valid;
ALTER TABLE buildings ADD CONSTRAINT buildings_type_valid CHECK (type IN ('HOUSE'));

ALTER TABLE civilizations DROP CONSTRAINT civilizations_good_valid;
ALTER TABLE civilizations DROP CONSTRAINT civilizations_good_unique;
ALTER TABLE civilizations DROP COLUMN good;

DELETE FROM trade_offers WHERE id IN (
    SELECT offer_id FROM trade_offer_lines WHERE resource IN ('WINE', 'SILK', 'SPICES', 'FURS'));
ALTER TABLE trade_offer_lines DROP CONSTRAINT trade_offer_lines_resource_valid;
ALTER TABLE trade_offer_lines ADD CONSTRAINT trade_offer_lines_resource_valid CHECK (resource IN ('WOOD', 'STONE'));

DELETE FROM city_stocks WHERE resource IN ('WINE', 'SILK', 'SPICES', 'FURS');
ALTER TABLE city_stocks DROP CONSTRAINT city_stocks_resource_valid;
ALTER TABLE city_stocks ADD CONSTRAINT city_stocks_resource_valid CHECK (resource IN ('WOOD', 'STONE'));
