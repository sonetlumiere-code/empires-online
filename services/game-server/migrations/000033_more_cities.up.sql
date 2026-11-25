-- SPEC-MORE-CITIES (E37): el tope de ciudades en pie depende de la era de la
-- capital (1 a 4, RN-CITY-040); la base no deja más de cuatro: los huecos 1 a 4
-- con el índice por dueño y hueco de 000031 (INV-CITY-022).

ALTER TABLE cities DROP CONSTRAINT cities_slot_valid;
ALTER TABLE cities ADD CONSTRAINT cities_slot_valid CHECK (slot BETWEEN 1 AND 4);
