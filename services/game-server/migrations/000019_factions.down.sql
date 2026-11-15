DROP TRIGGER IF EXISTS players_faction_transition ON players;
DROP FUNCTION IF EXISTS players_faction_transition();
ALTER TABLE players
    DROP CONSTRAINT IF EXISTS players_faction_join_pair,
    DROP COLUMN IF EXISTS faction_join_at,
    DROP COLUMN IF EXISTS faction_join_id;
