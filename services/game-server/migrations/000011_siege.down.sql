-- Retira el asedio y deja el esquema de 000010.
--
-- Las casas destruidas se borran: 000010 no las admite, y su rastro queda en
-- world_events (BuildingDestroyed). El CHECK de población vuelve; si una ciudad
-- quedó por encima de su límite al perder casas, el límite queda en la
-- población, como reparaba la carga antes de ADR-029 (INV-CITY-002): no se
-- destruye ninguna unidad.

DROP INDEX buildings_unique_tile;
DELETE FROM buildings WHERE status = 'DESTROYED';
ALTER TABLE buildings ADD CONSTRAINT buildings_unique_tile UNIQUE (x, y);

ALTER TABLE buildings DROP CONSTRAINT buildings_status_valid;
ALTER TABLE buildings ADD CONSTRAINT buildings_status_valid
    CHECK (status IN ('UNDER_CONSTRUCTION', 'COMPLETE'));

DROP TRIGGER cities_population_not_raised_over_limit ON cities;
DROP FUNCTION cities_population_not_raised_over_limit();

UPDATE cities SET population_limit = population, version = version + 1
 WHERE population > population_limit;

ALTER TABLE cities ADD CONSTRAINT cities_population_within_limit
    CHECK (population <= population_limit);
