-- Retira la Edad de los Castillos y deja el esquema de 000024.
--
-- Se pierden los castillos (en pie o derribados) y los avances a CASTLE_AGE en
-- curso, que un servidor anterior no reconoce como paso de su catálogo. Las
-- ciudades que ya llegaron a CASTLE_AGE se quedan en ella: un servidor
-- anterior las carga con su population_cap y sin avance posible.

DELETE FROM city_era_advances WHERE to_era = 'CASTLE_AGE';

DROP INDEX buildings_one_castle_per_city;
DELETE FROM buildings WHERE type = 'CASTLE';
ALTER TABLE buildings DROP CONSTRAINT buildings_type_valid;
ALTER TABLE buildings ADD CONSTRAINT buildings_type_valid CHECK (type IN ('HOUSE', 'WORKSHOP'));
