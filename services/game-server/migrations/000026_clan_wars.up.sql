-- SPEC-CLAN-WARS (docs/specs/clan-wars.md), ADR-045: las guerras entre clanes.
--
-- Una fila por guerra entre dos clanes, con quien la declaró (attacker) y quien
-- la recibió (defender). La escriben las órdenes de los líderes con los
-- candados de los dos clanes; los triggers son la red de seguridad bajo esa
-- transacción (INV-CLAN-008, INV-PERSIST-005): no nace una guerra con un clan
-- disuelto ni entre dos clanes aliados, y no nace ni se acepta una alianza
-- entre dos clanes en guerra. El marcador de una guerra abierta no vive aquí:
-- viaja en el payload de los eventos que puntúan (ADR-045 §4), y esta tabla
-- guarda el congelado al terminar.

CREATE TABLE clan_wars (
    id                        bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    attacker_clan             bigint      NOT NULL REFERENCES clans (id),
    defender_clan             bigint      NOT NULL REFERENCES clans (id),
    declared_by_player        uuid        NOT NULL REFERENCES players (id),
    request_id                text        NOT NULL,
    status                    text        NOT NULL,
    declared_at               timestamptz NOT NULL,
    starts_at                 timestamptz NOT NULL,
    ends_at                   timestamptz NULL,
    peace_offered_by_attacker boolean     NOT NULL DEFAULT false,
    peace_offered_by_defender boolean     NOT NULL DEFAULT false,
    ended_at                  timestamptz NULL,
    end_reason                text        NULL,
    winner_clan               bigint      NULL REFERENCES clans (id),
    attacker_score            bigint      NULL,
    defender_score            bigint      NULL,

    CONSTRAINT clan_wars_two_clans CHECK (attacker_clan <> defender_clan),
    CONSTRAINT clan_wars_status_valid CHECK (status IN ('DECLARED', 'ACTIVE', 'ENDED')),
    CONSTRAINT clan_wars_end_reason_valid CHECK (end_reason IN ('SURRENDER', 'PEACE', 'TIMEOUT', 'DISSOLVED')),
    CONSTRAINT clan_wars_winner_is_party CHECK (winner_clan IS NULL OR winner_clan IN (attacker_clan, defender_clan)),
    CONSTRAINT clan_wars_starts_after CHECK (starts_at >= declared_at),
    -- El plazo se fija al empezar: no lo tiene una guerra en preparación.
    CONSTRAINT clan_wars_ends_at CHECK (status = 'DECLARED' OR ends_at IS NOT NULL),
    -- El final, su motivo y el marcador congelado van juntos, y sólo en ENDED.
    CONSTRAINT clan_wars_ended CHECK (
        (status = 'ENDED') = (ended_at IS NOT NULL)
        AND (status = 'ENDED') = (end_reason IS NOT NULL)
        AND (status = 'ENDED') = (attacker_score IS NOT NULL)
        AND (status = 'ENDED') = (defender_score IS NOT NULL)
    ),
    CONSTRAINT clan_wars_no_offers_when_ended CHECK (
        status <> 'ENDED' OR NOT (peace_offered_by_attacker OR peace_offered_by_defender)
    ),
    CONSTRAINT clan_wars_request UNIQUE (declared_by_player, request_id)
);

-- INV-CLAN-008: como mucho una guerra abierta por par, en cualquier sentido.
CREATE UNIQUE INDEX clan_wars_one_open
    ON clan_wars (LEAST(attacker_clan, defender_clan), GREATEST(attacker_clan, defender_clan))
    WHERE status IN ('DECLARED', 'ACTIVE');
-- La carga, el tope de declaradas, la tregua y el historial de cada clan.
CREATE INDEX clan_wars_attacker ON clan_wars (attacker_clan, status);
CREATE INDEX clan_wars_defender ON clan_wars (defender_clan, status);

-- ADR-045 §4: la suma del marcador de una guerra desde los eventos que puntúan.
CREATE INDEX world_events_war ON world_events (((payload -> 'war' ->> 'warId')::bigint))
    WHERE payload ? 'war';

-- Al nacer una guerra: los dos clanes están vivos.
CREATE FUNCTION clan_wars_dissolved_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM clans WHERE id IN (NEW.attacker_clan, NEW.defender_clan) AND dissolved_at IS NOT NULL) THEN
        RAISE EXCEPTION 'una guerra no enfrenta a un clan disuelto (INV-CLAN-008): %, %', NEW.attacker_clan, NEW.defender_clan
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER clan_wars_dissolved_guard
    BEFORE INSERT ON clan_wars
    FOR EACH ROW EXECUTE FUNCTION clan_wars_dissolved_guard();

-- Al nacer una guerra: los dos clanes no tienen una alianza activa (RN-CLANWAR-003).
CREATE FUNCTION clan_wars_alliance_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM clan_alliances
                WHERE clan_a = LEAST(NEW.attacker_clan, NEW.defender_clan)
                  AND clan_b = GREATEST(NEW.attacker_clan, NEW.defender_clan)
                  AND status = 'ACTIVE') THEN
        RAISE EXCEPTION 'una guerra no enfrenta a dos clanes aliados (INV-CLAN-008): %, %', NEW.attacker_clan, NEW.defender_clan
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER clan_wars_alliance_guard
    BEFORE INSERT ON clan_wars
    FOR EACH ROW EXECUTE FUNCTION clan_wars_alliance_guard();

-- Al abrir o aceptar una alianza: los dos clanes no están en guerra (RN-CLANWAR-003).
CREATE FUNCTION clan_alliances_war_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status NOT IN ('PROPOSED', 'ACTIVE')
       OR (TG_OP = 'UPDATE' AND NEW.status = OLD.status) THEN
        RETURN NEW;
    END IF;
    IF EXISTS (SELECT 1 FROM clan_wars
                WHERE LEAST(attacker_clan, defender_clan) = NEW.clan_a
                  AND GREATEST(attacker_clan, defender_clan) = NEW.clan_b
                  AND status IN ('DECLARED', 'ACTIVE')) THEN
        RAISE EXCEPTION 'una alianza no une a dos clanes en guerra (INV-CLAN-008): %, %', NEW.clan_a, NEW.clan_b
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER clan_alliances_war_guard
    BEFORE INSERT OR UPDATE OF status ON clan_alliances
    FOR EACH ROW EXECUTE FUNCTION clan_alliances_war_guard();
