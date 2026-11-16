-- Retira los territorios de clan y deja el esquema de 000020. Un territorio que
-- siga a nombre de un clan se libera antes: sin los triggers, nada lo vigilaría.
DROP TRIGGER IF EXISTS clans_dissolve_territory_guard ON clans;
DROP FUNCTION IF EXISTS clans_dissolve_territory_guard();
DROP TRIGGER IF EXISTS territory_control_clan_owner_guard ON territory_control;
DROP FUNCTION IF EXISTS territory_control_clan_owner_guard();
UPDATE territory_control
   SET owner_type = 'NONE', owner_id = NULL, captured_at = NULL, version = version + 1
 WHERE owner_type = 'CLAN';
ALTER TABLE territory_claims DROP COLUMN IF EXISTS clan_id;
