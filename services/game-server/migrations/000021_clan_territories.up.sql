-- SPEC-CLAN-TERRITORIES (docs/specs/clan-territories.md), ADR-041: los
-- territorios de clan.
--
-- El dueño CLAN ya cabía en territory_control desde 000001 (owner_type con su
-- CHECK y owner_id como texto polimórfico, sin clave foránea): owner_id es el id
-- del clan en decimal. Lo que falta es la reclamación para un clan y que la base
-- garantice que un dueño CLAN es un clan vivo.
--
-- INV-TERR-012: si owner_type = 'CLAN', owner_id es el id de un clan con
-- dissolved_at NULL, y ningún clan disuelto posee un territorio. Dos triggers lo
-- comprueban en los dos sentidos —al escribir el dueño de un territorio y al
-- disolver un clan— y los dos miran con la fila del clan tomada, para que una
-- captura y una disolución concurrentes no se crucen (ADR-041 §6).

-- La reclamación para un clan: NULL si es para el propio jugador (ADR-041 §1).
ALTER TABLE territory_claims ADD COLUMN clan_id bigint NULL REFERENCES clans (id);

-- Al escribir el dueño de un territorio.
CREATE FUNCTION territory_control_clan_owner_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.owner_type <> 'CLAN' THEN
        RETURN NEW;
    END IF;
    IF NEW.owner_id IS NULL OR NEW.owner_id !~ '^[1-9][0-9]{0,18}$' THEN
        RAISE EXCEPTION 'un territorio de clan es de un clan vivo (INV-TERR-012): owner_id %', NEW.owner_id
            USING ERRCODE = 'check_violation';
    END IF;
    PERFORM 1 FROM clans WHERE id = NEW.owner_id::bigint AND dissolved_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'un territorio de clan es de un clan vivo (INV-TERR-012): clan %', NEW.owner_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER territory_control_clan_owner_guard
    BEFORE INSERT OR UPDATE OF owner_type, owner_id ON territory_control
    FOR EACH ROW EXECUTE FUNCTION territory_control_clan_owner_guard();

-- Al disolver un clan: antes hay que liberar sus territorios, en la misma
-- transacción (ADR-041 §4).
CREATE FUNCTION clans_dissolve_territory_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.dissolved_at IS NULL OR OLD.dissolved_at IS NOT NULL THEN
        RETURN NEW;
    END IF;
    IF EXISTS (SELECT 1 FROM territory_control WHERE owner_type = 'CLAN' AND owner_id = NEW.id::text) THEN
        RAISE EXCEPTION 'un clan disuelto no posee territorios (INV-TERR-012): clan %', NEW.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER clans_dissolve_territory_guard
    BEFORE UPDATE OF dissolved_at ON clans
    FOR EACH ROW EXECUTE FUNCTION clans_dissolve_territory_guard();
