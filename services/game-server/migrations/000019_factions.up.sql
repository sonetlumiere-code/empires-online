-- SPEC-FACTIONS (docs/specs/factions.md), ADR-039: la facción cuenta.
--
-- Todo jugador nace NEUTRAL y puede unirse una vez, en partida, a ORDER o a
-- CHAOS. La entrada pendiente vive en players hasta que cuenta: el bando
-- elegido y el instante en que cuenta, las dos columnas o ninguna. Al contar,
-- la misma transacción escribe la facción y borra la entrada.
--
-- INV-PLAYER-013: la facción sólo cambia de NEUTRAL a ORDER o a CHAOS (y, con
-- una entrada pendiente, sólo al bando elegido), y una entrada pendiente sólo
-- existe sobre un NEUTRAL, hacia ORDER o CHAOS, y no cambia ni se retira sin
-- contar. El trigger compara por código del catálogo, no por id.

ALTER TABLE players
    ADD COLUMN faction_join_id integer NULL REFERENCES factions (id),
    ADD COLUMN faction_join_at timestamptz NULL,
    ADD CONSTRAINT players_faction_join_pair
        CHECK ((faction_join_id IS NULL) = (faction_join_at IS NULL));

CREATE FUNCTION players_faction_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    old_code  text;
    new_code  text;
    join_code text;
BEGIN
    SELECT code INTO new_code FROM factions WHERE id = NEW.faction_id;
    IF TG_OP = 'UPDATE' THEN
        SELECT code INTO old_code FROM factions WHERE id = OLD.faction_id;
        IF NEW.faction_id IS DISTINCT FROM OLD.faction_id THEN
            IF old_code IS DISTINCT FROM 'NEUTRAL' OR new_code NOT IN ('ORDER', 'CHAOS') THEN
                RAISE EXCEPTION 'la facción sólo cambia de NEUTRAL a ORDER o a CHAOS (INV-PLAYER-013)'
                    USING ERRCODE = 'check_violation';
            END IF;
            IF OLD.faction_join_id IS NOT NULL AND NEW.faction_id <> OLD.faction_join_id THEN
                RAISE EXCEPTION 'la facción sólo cambia al bando de su entrada pendiente (INV-PLAYER-013)'
                    USING ERRCODE = 'check_violation';
            END IF;
        ELSIF OLD.faction_join_id IS NOT NULL AND NEW.faction_join_id IS NULL THEN
            RAISE EXCEPTION 'una entrada en un bando no se retira (INV-PLAYER-013)'
                USING ERRCODE = 'check_violation';
        END IF;
        IF OLD.faction_join_id IS NOT NULL AND NEW.faction_join_id IS NOT NULL
           AND (NEW.faction_join_id <> OLD.faction_join_id OR NEW.faction_join_at <> OLD.faction_join_at) THEN
            RAISE EXCEPTION 'una entrada en un bando no cambia (INV-PLAYER-013)'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    IF NEW.faction_join_id IS NOT NULL THEN
        SELECT code INTO join_code FROM factions WHERE id = NEW.faction_join_id;
        IF new_code IS DISTINCT FROM 'NEUTRAL' OR join_code NOT IN ('ORDER', 'CHAOS') THEN
            RAISE EXCEPTION 'una entrada pendiente sólo existe sobre un NEUTRAL, hacia ORDER o CHAOS (INV-PLAYER-013)'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER players_faction_transition
    BEFORE INSERT OR UPDATE OF faction_id, faction_join_id, faction_join_at ON players
    FOR EACH ROW EXECUTE FUNCTION players_faction_transition();
