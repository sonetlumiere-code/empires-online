-- Revierte el esquema inicial. El orden es el inverso de las dependencias.
BEGIN;

DROP TABLE IF EXISTS world_events;
DROP TABLE IF EXISTS garrisons;
DROP TABLE IF EXISTS treaties;
DROP TABLE IF EXISTS safe_zones;
DROP TABLE IF EXISTS territory_control;
DROP TABLE IF EXISTS territories;
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS unit_movements;
DROP TABLE IF EXISTS units;
DROP TABLE IF EXISTS cities;
DROP TABLE IF EXISTS world_chunks;
DROP TABLE IF EXISTS world_state;
DROP TABLE IF EXISTS players;
DROP TABLE IF EXISTS eras;
DROP TABLE IF EXISTS factions;
DROP TABLE IF EXISTS civilizations;

DROP FUNCTION IF EXISTS set_updated_at();

COMMIT;
