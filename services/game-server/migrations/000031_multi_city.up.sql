-- SPEC-MULTI-CITY (E34), ADR-051 §2: un jugador puede tener dos ciudades en pie.
-- Cada ciudad ocupa un hueco (1 o 2) entre las de su dueño, y el índice único
-- parcial que guardaba «una en pie por dueño» (INV-CITY-020, migración 000030)
-- pasa a ser por dueño y hueco: dos en pie sí, tres no (INV-CITY-022). Las
-- ciudades de hoy quedan en el hueco 1. world_events.event_type no tiene
-- CHECK: CityFounded no necesita migración.

ALTER TABLE cities ADD COLUMN slot smallint NOT NULL DEFAULT 1;
ALTER TABLE cities ADD CONSTRAINT cities_slot_valid CHECK (slot IN (1, 2));

DROP INDEX cities_one_standing_per_owner;
CREATE UNIQUE INDEX cities_one_standing_per_owner ON cities (owner_player_id, slot) WHERE fallen_at IS NULL;
