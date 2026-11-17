-- Retira las guerras entre clanes y deja el esquema de 000025.
DROP TRIGGER IF EXISTS clan_alliances_war_guard ON clan_alliances;
DROP FUNCTION IF EXISTS clan_alliances_war_guard();
DROP TRIGGER IF EXISTS clan_wars_alliance_guard ON clan_wars;
DROP FUNCTION IF EXISTS clan_wars_alliance_guard();
DROP TRIGGER IF EXISTS clan_wars_dissolved_guard ON clan_wars;
DROP FUNCTION IF EXISTS clan_wars_dissolved_guard();
DROP INDEX IF EXISTS world_events_war;
DROP TABLE IF EXISTS clan_wars;
