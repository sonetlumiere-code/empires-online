-- Falla si alguna ciudad está en el hueco 3 o 4: se arrasa antes.
ALTER TABLE cities DROP CONSTRAINT cities_slot_valid;
ALTER TABLE cities ADD CONSTRAINT cities_slot_valid CHECK (slot IN (1, 2));
