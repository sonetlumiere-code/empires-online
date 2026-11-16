-- Retira las alianzas entre clanes y deja el esquema de 000022.
DROP TRIGGER IF EXISTS clan_alliances_faction_guard ON clan_alliances;
DROP FUNCTION IF EXISTS clan_alliances_faction_guard();
DROP TRIGGER IF EXISTS clan_alliances_dissolved_guard ON clan_alliances;
DROP FUNCTION IF EXISTS clan_alliances_dissolved_guard();
DROP FUNCTION IF EXISTS clan_pair_mixes_factions(bigint, bigint);
DROP TABLE IF EXISTS clan_alliances;
