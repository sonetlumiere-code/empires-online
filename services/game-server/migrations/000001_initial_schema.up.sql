-- Empires Online — esquema inicial.
--
-- Convenciones (docs/database/schema.md):
--   * Claves primarias: bigint GENERATED ALWAYS AS IDENTITY, salvo players.id (uuid).
--   * Los enums de dominio son text + CHECK, no tipos ENUM de PostgreSQL: así se
--     pueden ampliar sin un ALTER TYPE que bloquee la tabla.
--   * Los instantes de simulación se guardan como bigint en epoch milliseconds
--     (`*_time_ms`) además de timestamptz: la aritmética del juego debe ser entera
--     y exacta, y timestamptz existe para que un humano pueda leer la fila.

BEGIN;

-- ─────────────────────────────────────────────────────────────
-- Utilidades
-- ─────────────────────────────────────────────────────────────

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ─────────────────────────────────────────────────────────────
-- Catálogos
-- ─────────────────────────────────────────────────────────────

-- Identidad cultural del jugador. Ortogonal a la facción global.
CREATE TABLE civilizations (
    id          integer PRIMARY KEY,
    code        text        NOT NULL UNIQUE,
    name        text        NOT NULL,
    -- Rasgos data-driven. Existe para que NUNCA aparezca un `if civ == 'ROMAN'`
    -- disperso por el código: los sistemas consultan el rasgo que les concierne.
    traits      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Bando global. Ortogonal a la civilización: un Roman puede militar en Chaos.
CREATE TABLE factions (
    id          integer PRIMARY KEY,
    code        text        NOT NULL UNIQUE,
    name        text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- El límite de población depende de la era y se lee de aquí: jamás está en el código.
CREATE TABLE eras (
    id              integer PRIMARY KEY,
    code            text    NOT NULL UNIQUE,
    name            text    NOT NULL,
    ordinal         integer NOT NULL UNIQUE,
    population_cap  integer NOT NULL CHECK (population_cap > 0),
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- ─────────────────────────────────────────────────────────────
-- Jugadores
-- ─────────────────────────────────────────────────────────────

CREATE TABLE players (
    id                uuid        PRIMARY KEY,
    username          text        NOT NULL UNIQUE,
    -- Hash Argon2id/bcrypt. NUNCA una contraseña en claro.
    password_hash     text        NOT NULL,
    civilization_id   integer     NOT NULL REFERENCES civilizations (id),
    faction_id        integer     NOT NULL REFERENCES factions (id),
    last_seen_at      timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT players_username_format CHECK (username ~ '^[A-Za-z0-9_-]{3,24}$')
);

CREATE TRIGGER players_set_updated_at
    BEFORE UPDATE ON players
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ─────────────────────────────────────────────────────────────
-- Mundo
-- ─────────────────────────────────────────────────────────────

-- Fila única: el estado global del mundo. El CHECK garantiza que sólo exista una.
CREATE TABLE world_state (
    id           smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    seed         bigint      NOT NULL,
    width        integer     NOT NULL CHECK (width > 0),
    height       integer     NOT NULL CHECK (height > 0),
    chunk_size   integer     NOT NULL CHECK (chunk_size > 0),
    -- Origen temporal de la simulación: tickTime = epoch_ms + tick*tickDurationMs.
    epoch_ms     bigint      NOT NULL,
    current_tick bigint      NOT NULL DEFAULT 0 CHECK (current_tick >= 0),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER world_state_set_updated_at
    BEFORE UPDATE ON world_state
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Terreno persistido por chunk: chunk_size^2 bytes en orden fila-mayor.
-- Con chunk_size = 32 son exactamente 1024 bytes por fila.
CREATE TABLE world_chunks (
    chunk_x    integer     NOT NULL CHECK (chunk_x >= 0),
    chunk_y    integer     NOT NULL CHECK (chunk_y >= 0),
    terrain    bytea       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (chunk_x, chunk_y)
);

-- ─────────────────────────────────────────────────────────────
-- Ciudades
-- ─────────────────────────────────────────────────────────────

CREATE TABLE cities (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_player_id   uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    name              text        NOT NULL,
    center_x          integer     NOT NULL,
    center_y          integer     NOT NULL,
    era               text        NOT NULL REFERENCES eras (code),
    population        integer     NOT NULL DEFAULT 0 CHECK (population >= 0),
    population_limit  integer     NOT NULL CHECK (population_limit >= 0),
    presence_state    text        NOT NULL DEFAULT 'ONLINE',
    last_online_at    timestamptz,
    last_offline_at   timestamptz,
    protection_until  timestamptz,
    -- Concurrencia optimista.
    version           integer     NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    -- INV-CITY-004: el autómata de presencia sólo admite estos tres valores.
    CONSTRAINT cities_presence_state_valid
        CHECK (presence_state IN ('ONLINE', 'OFFLINE_PENDING', 'PROTECTED')),
    -- INV-CITY-002: la población nunca supera el límite.
    CONSTRAINT cities_population_within_limit
        CHECK (population <= population_limit),
    -- Dos ciudades no pueden compartir el mismo centro.
    CONSTRAINT cities_unique_center UNIQUE (center_x, center_y)
);

CREATE TRIGGER cities_set_updated_at
    BEFORE UPDATE ON cities
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX cities_owner_idx ON cities (owner_player_id);
-- Consulta caliente del tick de timers: ciudades cuyo cooldown puede haber vencido.
CREATE INDEX cities_offline_pending_idx
    ON cities (last_offline_at)
    WHERE presence_state = 'OFFLINE_PENDING';

-- ─────────────────────────────────────────────────────────────
-- Unidades
-- ─────────────────────────────────────────────────────────────

CREATE TABLE units (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    player_id  uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    city_id    bigint      REFERENCES cities (id) ON DELETE SET NULL,
    unit_type  text        NOT NULL,
    x          integer     NOT NULL,
    y          integer     NOT NULL,
    hp         integer     NOT NULL CHECK (hp >= 0),
    max_hp     integer     NOT NULL CHECK (max_hp > 0),
    status     text        NOT NULL DEFAULT 'IDLE',
    -- Chunk desnormalizado: es la clave del interest management y evita un
    -- índice espacial completo en el MVP. Se mantiene en cada escritura de x/y.
    chunk_x    integer     NOT NULL,
    chunk_y    integer     NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT units_status_valid
        CHECK (status IN ('IDLE', 'MOVING', 'GARRISONED', 'HIDDEN', 'DEAD')),
    -- INV-UNIT-004: hp nunca supera max_hp.
    CONSTRAINT units_hp_within_max CHECK (hp <= max_hp)
);

CREATE TRIGGER units_set_updated_at
    BEFORE UPDATE ON units
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX units_player_idx ON units (player_id) WHERE status <> 'DEAD';
CREATE INDEX units_city_idx ON units (city_id) WHERE city_id IS NOT NULL;
-- Consulta caliente del snapshot: todas las unidades vivas de un chunk.
CREATE INDEX units_chunk_idx ON units (chunk_x, chunk_y) WHERE status <> 'DEAD';

-- ─────────────────────────────────────────────────────────────
-- Movimientos
-- ─────────────────────────────────────────────────────────────

-- El movimiento se persiste UNA vez, como polilínea temporizada, y no se vuelve a
-- tocar hasta que termina. La posición en cualquier instante es una función pura
-- de (path, tiempo), lo que hace la recuperación tras un crash O(1). Ver ADR-011.
CREATE TABLE unit_movements (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    unit_id         bigint      NOT NULL REFERENCES units (id) ON DELETE CASCADE,
    -- [{"x":int,"y":int,"tMs":int}, ...] — el primer waypoint es el origen con tMs = 0.
    path            jsonb       NOT NULL,
    target_x        integer     NOT NULL,
    target_y        integer     NOT NULL,
    start_time_ms   bigint      NOT NULL,
    arrival_time_ms bigint      NOT NULL,
    status          text        NOT NULL DEFAULT 'ACTIVE',
    finished_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT unit_movements_status_valid
        CHECK (status IN ('ACTIVE', 'COMPLETED', 'CANCELLED', 'FAILED')),
    CONSTRAINT unit_movements_time_ordered
        CHECK (arrival_time_ms >= start_time_ms),
    CONSTRAINT unit_movements_path_is_array
        CHECK (jsonb_typeof(path) = 'array' AND jsonb_array_length(path) >= 1)
);

CREATE TRIGGER unit_movements_set_updated_at
    BEFORE UPDATE ON unit_movements
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- INV-MOVE-001, garantizado FÍSICAMENTE por la base de datos y no sólo por código:
-- una unidad tiene como máximo un movimiento ACTIVE. Si dos comandos concurrentes
-- intentaran crear dos, el segundo falla con violación de unicidad.
CREATE UNIQUE INDEX unit_movements_one_active_per_unit
    ON unit_movements (unit_id)
    WHERE status = 'ACTIVE';

-- Consulta de arranque: rehidratar todos los movimientos vivos, en orden estable.
CREATE INDEX unit_movements_active_idx
    ON unit_movements (arrival_time_ms, id)
    WHERE status = 'ACTIVE';

-- ─────────────────────────────────────────────────────────────
-- Sesiones e idempotencia
-- ─────────────────────────────────────────────────────────────

CREATE TABLE sessions (
    id             uuid        PRIMARY KEY,
    player_id      uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    connected_at   timestamptz NOT NULL DEFAULT now(),
    disconnected_at timestamptz,
    remote_addr    text,
    client_version text
);

CREATE INDEX sessions_player_idx ON sessions (player_id, connected_at DESC);

-- Deduplicación durable de comandos. Un requestId repetido devuelve la respuesta
-- original en lugar de producir un segundo efecto (INV-SEC-007).
CREATE TABLE idempotency_keys (
    player_id   uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    request_id  uuid        NOT NULL,
    message_type text       NOT NULL,
    response    jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    PRIMARY KEY (player_id, request_id)
);

CREATE INDEX idempotency_keys_expiry_idx ON idempotency_keys (expires_at);

-- ─────────────────────────────────────────────────────────────
-- Territorios (entidad creada en el MVP; mecánica de captura diferida)
-- ─────────────────────────────────────────────────────────────

CREATE TABLE territories (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name               text        NOT NULL,
    min_x              integer     NOT NULL,
    min_y              integer     NOT NULL,
    max_x              integer     NOT NULL,
    max_y              integer     NOT NULL,
    type               text        NOT NULL DEFAULT 'PLAINS',
    resource_modifiers jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at         timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT territories_bounds_ordered CHECK (min_x <= max_x AND min_y <= max_y)
);

-- El control es una entidad SEPARADA de la geometría, a propósito: permite
-- introducir después ownership de clan, influencia de facción, estado disputado
-- y tiempo de captura sin migrar destructivamente `territories`.
CREATE TABLE territory_control (
    territory_id   bigint      PRIMARY KEY REFERENCES territories (id) ON DELETE CASCADE,
    owner_type     text        NOT NULL DEFAULT 'NONE',
    owner_id       text,
    control_points integer     NOT NULL DEFAULT 0 CHECK (control_points >= 0),
    contested      boolean     NOT NULL DEFAULT false,
    captured_at    timestamptz,
    updated_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT territory_control_owner_type_valid
        CHECK (owner_type IN ('NONE', 'PLAYER', 'CLAN', 'FACTION')),
    -- Un dueño distinto de NONE exige identificador; NONE exige que no lo haya.
    CONSTRAINT territory_control_owner_consistency
        CHECK ((owner_type = 'NONE' AND owner_id IS NULL) OR (owner_type <> 'NONE' AND owner_id IS NOT NULL))
);

CREATE TRIGGER territory_control_set_updated_at
    BEFORE UPDATE ON territory_control
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ─────────────────────────────────────────────────────────────
-- Safe zones (entidad creada en el MVP; efectos diferidos)
-- ─────────────────────────────────────────────────────────────

CREATE TABLE safe_zones (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text        NOT NULL,
    zone_type  text        NOT NULL,
    min_x      integer     NOT NULL,
    min_y      integer     NOT NULL,
    max_x      integer     NOT NULL,
    max_y      integer     NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT safe_zones_type_valid CHECK (zone_type IN ('DENSE_FOREST', 'CAVERN')),
    CONSTRAINT safe_zones_bounds_ordered CHECK (min_x <= max_x AND min_y <= max_y)
);

-- ─────────────────────────────────────────────────────────────
-- Diplomacia (entidades creadas en el MVP; lógica completa diferida)
-- ─────────────────────────────────────────────────────────────

CREATE TABLE treaties (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    player_a_id      uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    player_b_id      uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    treaty_type      text        NOT NULL,
    status           text        NOT NULL DEFAULT 'PROPOSED',
    allows_garrison  boolean     NOT NULL DEFAULT false,
    proposed_at      timestamptz NOT NULL DEFAULT now(),
    accepted_at      timestamptz,
    expires_at       timestamptz,
    broken_at        timestamptz,

    CONSTRAINT treaties_type_valid CHECK (treaty_type IN ('NON_AGGRESSION', 'ALLIANCE', 'TRADE')),
    CONSTRAINT treaties_status_valid CHECK (status IN ('PROPOSED', 'ACTIVE', 'EXPIRED', 'BROKEN')),
    -- Un jugador no firma tratados consigo mismo.
    CONSTRAINT treaties_distinct_players CHECK (player_a_id <> player_b_id),
    -- Orden canónico del par: evita duplicados A-B / B-A.
    CONSTRAINT treaties_canonical_pair CHECK (player_a_id < player_b_id)
);

-- Como máximo un tratado vigente de cada tipo entre dos jugadores.
CREATE UNIQUE INDEX treaties_one_active_per_pair_and_type
    ON treaties (player_a_id, player_b_id, treaty_type)
    WHERE status = 'ACTIVE';

CREATE TABLE garrisons (
    unit_id    bigint      PRIMARY KEY REFERENCES units (id) ON DELETE CASCADE,
    city_id    bigint      NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    entered_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX garrisons_city_idx ON garrisons (city_id);

-- ─────────────────────────────────────────────────────────────
-- Eventos del mundo (log append-only)
-- ─────────────────────────────────────────────────────────────

CREATE TABLE world_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type  text        NOT NULL,
    tick        bigint      NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    player_id   uuid        REFERENCES players (id) ON DELETE SET NULL,
    payload     jsonb       NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX world_events_type_time_idx ON world_events (event_type, occurred_at DESC);
CREATE INDEX world_events_player_idx ON world_events (player_id, occurred_at DESC) WHERE player_id IS NOT NULL;

COMMIT;
