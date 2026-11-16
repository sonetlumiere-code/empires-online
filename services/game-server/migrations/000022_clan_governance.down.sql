-- Retira los rangos de clan y deja el esquema de 000021. Los oficiales vuelven
-- a ser miembros: sin la columna, sólo queda el líder.
DROP TRIGGER IF EXISTS clans_leader_rank_guard ON clans;
DROP FUNCTION IF EXISTS clans_leader_rank_guard();
DROP TRIGGER IF EXISTS clan_members_rank_guard ON clan_members;
DROP FUNCTION IF EXISTS clan_members_rank_guard();
ALTER TABLE clan_members DROP CONSTRAINT IF EXISTS clan_members_rank_valid;
ALTER TABLE clan_members DROP COLUMN IF EXISTS rank;
