-- La Edad de los Castillos (SPEC-CASTLE-AGE, ADR-044 §8).
--
-- El avance de IRON_AGE a CASTLE_AGE no necesita esquema: city_era_advances
-- admite cualquier par de eras del catálogo, y eras tiene CASTLE_AGE con su
-- population_cap desde 000002. Lo nuevo es el castillo, un tipo más de
-- edificio, como el taller de 000016.

ALTER TABLE buildings DROP CONSTRAINT buildings_type_valid;
ALTER TABLE buildings ADD CONSTRAINT buildings_type_valid CHECK (type IN ('HOUSE', 'WORKSHOP', 'CASTLE'));

-- INV-ECON-012: como mucho un castillo en pie por ciudad. Que sólo lo tenga una
-- ciudad en CASTLE_AGE lo garantiza el dominio al encargarlo, y la era no
-- retrocede (INV-CITY-018).
CREATE UNIQUE INDEX buildings_one_castle_per_city ON buildings (city_id)
    WHERE type = 'CASTLE' AND status <> 'DESTROYED';
