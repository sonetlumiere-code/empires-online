-- SPEC-CLAN-ALLIANCES (docs/specs/clan-alliances.md), ADR-042: las alianzas
-- entre clanes.
--
-- Una fila por alianza (o por oferta) entre dos clanes, con el par en orden
-- canónico y los estados de un tratado. La alianza la escriben las órdenes de
-- los líderes con los candados de los dos clanes; los dos triggers son la red
-- de seguridad bajo esa transacción (INV-CLAN-006, INV-PERSIST-005): no se abre
-- ni se acepta una alianza con un clan disuelto ni una que junte a la Orden y
-- al Caos. La mezcla que aparece después no la rechazan: abre el preaviso
-- (ADR-042 §4).

CREATE TABLE clan_alliances (
    id                 bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    clan_a             bigint      NOT NULL REFERENCES clans (id),
    clan_b             bigint      NOT NULL REFERENCES clans (id),
    proposed_by_clan   bigint      NOT NULL,
    proposed_by_player uuid        NOT NULL REFERENCES players (id),
    status             text        NOT NULL,
    request_id         text        NOT NULL,
    created_at         timestamptz NOT NULL,
    accepted_at        timestamptz NULL,
    break_effective_at timestamptz NULL,
    break_reason       text        NULL,
    closed_at          timestamptz NULL,

    CONSTRAINT clan_alliances_canonical_pair CHECK (clan_a < clan_b),
    CONSTRAINT clan_alliances_proposer CHECK (proposed_by_clan IN (clan_a, clan_b)),
    CONSTRAINT clan_alliances_status_valid
        CHECK (status IN ('PROPOSED', 'ACTIVE', 'REJECTED', 'WITHDRAWN', 'EXPIRED', 'BROKEN')),
    CONSTRAINT clan_alliances_break_reason_valid CHECK (break_reason IN ('CANCEL', 'FACTIONS')),
    CONSTRAINT clan_alliances_break_pair CHECK ((break_effective_at IS NULL) = (break_reason IS NULL)),
    CONSTRAINT clan_alliances_accepted CHECK (status NOT IN ('ACTIVE', 'BROKEN') OR accepted_at IS NOT NULL),
    CONSTRAINT clan_alliances_closed CHECK ((status IN ('PROPOSED', 'ACTIVE')) = (closed_at IS NULL)),
    CONSTRAINT clan_alliances_request UNIQUE (proposed_by_player, request_id)
);

-- INV-CLAN-006: como mucho una alianza activa y una oferta pendiente por par.
CREATE UNIQUE INDEX clan_alliances_one_active ON clan_alliances (clan_a, clan_b) WHERE status = 'ACTIVE';
CREATE UNIQUE INDEX clan_alliances_one_proposed ON clan_alliances (clan_a, clan_b) WHERE status = 'PROPOSED';
-- La carga y el tope de cada clan.
CREATE INDEX clan_alliances_open_a ON clan_alliances (clan_a) WHERE status IN ('PROPOSED', 'ACTIVE');
CREATE INDEX clan_alliances_open_b ON clan_alliances (clan_b) WHERE status IN ('PROPOSED', 'ACTIVE');

-- Si los miembros abiertos de dos clanes, con su facción efectiva y su entrada
-- pendiente, juntan a la Orden y al Caos (RN-CLANALLY-005).
CREATE FUNCTION clan_pair_mixes_factions(p_a bigint, p_b bigint) RETURNS boolean
LANGUAGE plpgsql AS $$
DECLARE
    order_n integer;
    chaos_n integer;
BEGIN
    SELECT count(*) FILTER (WHERE f.code = 'ORDER' OR j.code = 'ORDER'),
           count(*) FILTER (WHERE f.code = 'CHAOS' OR j.code = 'CHAOS')
      INTO order_n, chaos_n
      FROM clan_members m
      JOIN players p ON p.id = m.player_id
      JOIN factions f ON f.id = p.faction_id
      LEFT JOIN factions j ON j.id = p.faction_join_id
     WHERE m.clan_id IN (p_a, p_b) AND m.left_at IS NULL;
    RETURN order_n > 0 AND chaos_n > 0;
END;
$$;

-- Al abrir o aceptar una alianza: los dos clanes están vivos.
CREATE FUNCTION clan_alliances_dissolved_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status NOT IN ('PROPOSED', 'ACTIVE')
       OR (TG_OP = 'UPDATE' AND NEW.status = OLD.status) THEN
        RETURN NEW;
    END IF;
    IF EXISTS (SELECT 1 FROM clans WHERE id IN (NEW.clan_a, NEW.clan_b) AND dissolved_at IS NOT NULL) THEN
        RAISE EXCEPTION 'una alianza no une a un clan disuelto (INV-CLAN-006): %, %', NEW.clan_a, NEW.clan_b
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER clan_alliances_dissolved_guard
    BEFORE INSERT OR UPDATE OF status ON clan_alliances
    FOR EACH ROW EXECUTE FUNCTION clan_alliances_dissolved_guard();

-- Al abrir o aceptar una alianza: no junta a la Orden y al Caos. Toma las filas
-- de los dos clanes en orden de id, como los triggers de bandos de 000020.
CREATE FUNCTION clan_alliances_faction_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status NOT IN ('PROPOSED', 'ACTIVE')
       OR (TG_OP = 'UPDATE' AND NEW.status = OLD.status) THEN
        RETURN NEW;
    END IF;
    PERFORM 1 FROM clans WHERE id IN (NEW.clan_a, NEW.clan_b) ORDER BY id FOR UPDATE;
    IF clan_pair_mixes_factions(NEW.clan_a, NEW.clan_b) THEN
        RAISE EXCEPTION 'una alianza no junta a la Orden y al Caos (INV-CLAN-006): %, %', NEW.clan_a, NEW.clan_b
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER clan_alliances_faction_guard
    BEFORE INSERT OR UPDATE OF status ON clan_alliances
    FOR EACH ROW EXECUTE FUNCTION clan_alliances_faction_guard();
