-- Retira el ariete y la muralla y deja el esquema de 000028.
--
-- Se pierden las murallas y los arietes: un servidor anterior no los conoce. Las
-- producciones de arietes se borran (la última de cada ciudad es sólo historia),
-- y los arietes, con su movimiento (ON DELETE CASCADE).

DELETE FROM city_productions WHERE unit_type = 'RAM';
ALTER TABLE city_productions DROP CONSTRAINT city_productions_unit_type_valid;
ALTER TABLE city_productions ADD CONSTRAINT city_productions_unit_type_valid
    CHECK (unit_type IN ('VILLAGER', 'WARRIOR', 'LEGIONARY', 'CATAPHRACT', 'HORSE_ARCHER', 'BERSERKER'));
DELETE FROM units WHERE unit_type = 'RAM';

DROP TRIGGER buildings_wall_at_center ON buildings;
DROP FUNCTION buildings_wall_at_center();

DELETE FROM buildings WHERE type = 'WALL';
DROP INDEX buildings_one_wall_per_city;
ALTER TABLE buildings DROP CONSTRAINT buildings_type_valid;
ALTER TABLE buildings ADD CONSTRAINT buildings_type_valid CHECK (type IN ('HOUSE', 'WORKSHOP', 'CASTLE', 'MARKET'));
