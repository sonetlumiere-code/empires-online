-- Retira los clanes y deja el esquema de 000019.
DROP TRIGGER IF EXISTS players_clan_faction_guard ON players;
DROP FUNCTION IF EXISTS players_clan_faction_guard();
DROP TRIGGER IF EXISTS clan_members_faction_guard ON clan_members;
DROP FUNCTION IF EXISTS clan_members_faction_guard();
DROP FUNCTION IF EXISTS clan_mixes_factions(bigint);
DROP TABLE clan_message_reads;
DROP TABLE clan_messages;
DROP TABLE clan_invites;
DROP TABLE clan_members;
DROP TABLE clans;
