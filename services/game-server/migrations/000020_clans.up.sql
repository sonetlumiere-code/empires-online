-- SPEC-CLANS (docs/specs/clans.md), ADR-040: los clanes, su historia de
-- pertenencia, sus invitaciones y su canal.
--
-- Un clan vivo tiene dissolved_at NULL; su nombre (sin distinguir mayúsculas) y
-- su sigla son únicos entre los vivos. La pertenencia es una fila por estancia
-- en clan_members: abierta mientras left_at es NULL. De la historia salen la paz
-- residual (RN-CLAN-005) y lo que cada miembro lee del canal: first_message_id
-- es el mayor id del canal al entrar (RN-CLAN-009).
--
-- INV-CLAN-001: un jugador tiene como mucho una estancia abierta (índice
-- parcial). Que el líder sea un miembro abierto lo garantiza la transacción que
-- lo cambia (ADR-040 §2).
-- INV-CLAN-002: ningún clan junta a la Orden y al Caos, contando las entradas
-- pendientes en un bando. Dos triggers lo comprueban en los dos sentidos —al
-- abrir una estancia y al cambiar la facción o la entrada de un miembro— y los
-- dos toman la fila del clan antes de mirar, para que dos escrituras
-- concurrentes no se crucen (ADR-040 §4).

CREATE TABLE clans (
    id               bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name             text        NOT NULL,
    tag              text        NOT NULL,
    leader_player_id uuid        NOT NULL REFERENCES players (id),
    founded_at       timestamptz NOT NULL DEFAULT now(),
    dissolved_at     timestamptz NULL,

    -- RN-CLAN-002: de 3 a 24 caracteres, sin espacios en los extremos ni dobles.
    -- Qué caracteres admite lo decide el dominio (clan.ValidateName).
    CONSTRAINT clans_name_format CHECK (
        char_length(name) BETWEEN 3 AND 24 AND name = btrim(name) AND position('  ' IN name) = 0),
    CONSTRAINT clans_tag_format CHECK (tag ~ '^[A-Z0-9]{2,4}$')
);

CREATE UNIQUE INDEX clans_name_live_key ON clans (lower(name)) WHERE dissolved_at IS NULL;
CREATE UNIQUE INDEX clans_tag_live_key ON clans (tag) WHERE dissolved_at IS NULL;

CREATE TABLE clan_members (
    id               bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    clan_id          bigint      NOT NULL REFERENCES clans (id),
    player_id        uuid        NOT NULL REFERENCES players (id),
    joined_at        timestamptz NOT NULL,
    first_message_id bigint      NOT NULL DEFAULT 0,
    left_at          timestamptz NULL,
    left_reason      text        NULL,

    CONSTRAINT clan_members_left_pair CHECK ((left_at IS NULL) = (left_reason IS NULL)),
    CONSTRAINT clan_members_left_reason CHECK (left_reason IN ('LEFT', 'EXPELLED')),
    CONSTRAINT clan_members_left_after_join CHECK (left_at IS NULL OR left_at >= joined_at),
    CONSTRAINT clan_members_first_message CHECK (first_message_id >= 0)
);

-- INV-CLAN-001: una estancia abierta por jugador.
CREATE UNIQUE INDEX clan_members_one_open ON clan_members (player_id) WHERE left_at IS NULL;
-- Los miembros de un clan, y las salidas recientes que carga el arranque.
CREATE INDEX clan_members_open_idx ON clan_members (clan_id) WHERE left_at IS NULL;
CREATE INDEX clan_members_left_idx ON clan_members (left_at) WHERE left_at IS NOT NULL;

CREATE TABLE clan_invites (
    clan_id    bigint      NOT NULL REFERENCES clans (id),
    player_id  uuid        NOT NULL REFERENCES players (id),
    invited_by uuid        NOT NULL REFERENCES players (id),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,

    PRIMARY KEY (clan_id, player_id),
    CONSTRAINT clan_invites_distinct CHECK (player_id <> invited_by),
    CONSTRAINT clan_invites_expiry CHECK (expires_at > created_at)
);

CREATE INDEX clan_invites_player_idx ON clan_invites (player_id);

-- El canal de un clan (RN-CLAN-009). El envío toma el candado de transacción
-- del clan, el mismo que la entrada y la salida, así que los id de un canal se
-- confirman en orden y nadie escribe tras su salida (ADR-040 §5).
CREATE TABLE clan_messages (
    id               bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    clan_id          bigint      NOT NULL REFERENCES clans (id),
    sender_player_id uuid        NOT NULL REFERENCES players (id),
    body             text        NOT NULL,
    request_id       uuid        NOT NULL,
    sent_at          timestamptz NOT NULL DEFAULT now(),

    -- RN-MSG-003: de 1 a 500 caracteres, ya recortado.
    CONSTRAINT clan_messages_body_length CHECK (char_length(body) BETWEEN 1 AND 500),
    -- INV-SEC-007: un requestId repetido no guarda un segundo mensaje.
    CONSTRAINT clan_messages_request_once UNIQUE (sender_player_id, request_id)
);

CREATE INDEX clan_messages_clan_idx ON clan_messages (clan_id, id DESC);

-- Lo leído del canal por cada miembro. Sólo avanza (GREATEST).
CREATE TABLE clan_message_reads (
    player_id     uuid   NOT NULL REFERENCES players (id),
    clan_id       bigint NOT NULL REFERENCES clans (id),
    read_up_to_id bigint NOT NULL DEFAULT 0,

    PRIMARY KEY (player_id, clan_id),
    CONSTRAINT clan_message_reads_non_negative CHECK (read_up_to_id >= 0)
);

-- INV-CLAN-002: si las estancias abiertas de un clan juntan a la Orden y al
-- Caos, contando la facción efectiva y la entrada pendiente de cada miembro.
CREATE FUNCTION clan_mixes_factions(p_clan_id bigint) RETURNS boolean
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
     WHERE m.clan_id = p_clan_id AND m.left_at IS NULL;
    RETURN order_n > 0 AND chaos_n > 0;
END;
$$;

-- Al abrir una estancia.
CREATE FUNCTION clan_members_faction_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.left_at IS NOT NULL THEN
        RETURN NULL;
    END IF;
    PERFORM 1 FROM clans WHERE id = NEW.clan_id FOR UPDATE;
    IF clan_mixes_factions(NEW.clan_id) THEN
        RAISE EXCEPTION 'un clan no junta a la Orden y al Caos (INV-CLAN-002)'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER clan_members_faction_guard
    AFTER INSERT ON clan_members
    FOR EACH ROW EXECUTE FUNCTION clan_members_faction_guard();

-- Al cambiar la facción o la entrada pendiente de un miembro.
CREATE FUNCTION players_clan_faction_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    open_clan bigint;
BEGIN
    SELECT clan_id INTO open_clan FROM clan_members WHERE player_id = NEW.id AND left_at IS NULL;
    IF open_clan IS NULL THEN
        RETURN NULL;
    END IF;
    PERFORM 1 FROM clans WHERE id = open_clan FOR UPDATE;
    IF clan_mixes_factions(open_clan) THEN
        RAISE EXCEPTION 'un clan no junta a la Orden y al Caos (INV-CLAN-002)'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER players_clan_faction_guard
    AFTER UPDATE OF faction_id, faction_join_id ON players
    FOR EACH ROW
    WHEN (NEW.faction_id IS DISTINCT FROM OLD.faction_id OR NEW.faction_join_id IS DISTINCT FROM OLD.faction_join_id)
    EXECUTE FUNCTION players_clan_faction_guard();
