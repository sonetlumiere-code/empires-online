-- SPEC-CLAN-GOVERNANCE (docs/specs/clan-governance.md): los rangos de un clan.
--
-- El rango de oficial vive en la estancia abierta de cada miembro
-- (clan_members.rank). El líder sigue siendo clans.leader_player_id, y el rango
-- de su estancia es MEMBER: así hay una sola fuente de verdad de quién lidera.
-- Dos triggers lo vigilan como red de seguridad bajo la transacción de cada
-- orden (INV-PERSIST-005): la estancia abierta del líder no es OFFICER, y una
-- estancia cerrada no cambia de rango.

ALTER TABLE clan_members
    ADD COLUMN rank text NOT NULL DEFAULT 'MEMBER',
    ADD CONSTRAINT clan_members_rank_valid CHECK (rank IN ('MEMBER', 'OFFICER'));

-- Al escribir el rango de una estancia.
CREATE FUNCTION clan_members_rank_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.left_at IS NOT NULL AND NEW.rank IS DISTINCT FROM OLD.rank THEN
        RAISE EXCEPTION 'una estancia cerrada no cambia de rango: clan %, jugador %', NEW.clan_id, NEW.player_id
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.rank = 'OFFICER' AND NEW.left_at IS NULL
       AND EXISTS (SELECT 1 FROM clans WHERE id = NEW.clan_id AND leader_player_id = NEW.player_id) THEN
        RAISE EXCEPTION 'el líder de un clan no es oficial: clan %, jugador %', NEW.clan_id, NEW.player_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER clan_members_rank_guard
    BEFORE INSERT OR UPDATE OF rank ON clan_members
    FOR EACH ROW EXECUTE FUNCTION clan_members_rank_guard();

-- Al cambiar el líder: su estancia abierta tiene que tener ya rango MEMBER.
CREATE FUNCTION clans_leader_rank_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.leader_player_id IS NOT DISTINCT FROM OLD.leader_player_id THEN
        RETURN NEW;
    END IF;
    IF EXISTS (SELECT 1 FROM clan_members
                WHERE clan_id = NEW.id AND player_id = NEW.leader_player_id
                  AND left_at IS NULL AND rank = 'OFFICER') THEN
        RAISE EXCEPTION 'el líder de un clan no es oficial: clan %, jugador %', NEW.id, NEW.leader_player_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER clans_leader_rank_guard
    BEFORE UPDATE OF leader_player_id ON clans
    FOR EACH ROW EXECUTE FUNCTION clans_leader_rank_guard();
