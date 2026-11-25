-- SPEC-CITY-TAKEN (E36), ADR-052: la ciudad tomada es una fila nueva del
-- vencedor en el centro de la caída que hereda sus edificios completos. Apunta
-- a la caída, y una caída se toma como mucho una vez (INV-CITY-024).

ALTER TABLE cities ADD COLUMN taken_from_city_id bigint NULL REFERENCES cities (id);
ALTER TABLE cities ADD CONSTRAINT cities_taken_from_other CHECK (taken_from_city_id <> id);
CREATE UNIQUE INDEX cities_taken_once ON cities (taken_from_city_id) WHERE taken_from_city_id IS NOT NULL;
