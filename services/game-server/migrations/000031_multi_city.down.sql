-- Vuelta a «una ciudad en pie por dueño» (000030). Falla si algún jugador
-- tiene dos ciudades en pie: hay que arrasar una antes.

DROP INDEX cities_one_standing_per_owner;
CREATE UNIQUE INDEX cities_one_standing_per_owner ON cities (owner_player_id) WHERE fallen_at IS NULL;

ALTER TABLE cities DROP CONSTRAINT cities_slot_valid;
ALTER TABLE cities DROP COLUMN slot;
