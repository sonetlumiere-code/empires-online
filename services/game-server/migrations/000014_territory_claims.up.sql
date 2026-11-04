-- SPEC-TERRITORY-CONQUEST (docs/specs/territory-conquest.md), ADR-034: la
-- conquista de territorios.
--
-- territory_claims guarda cada reclamación de un guerrero sobre el territorio
-- en que se plantó y su ciclo de vida. La RAM de la simulación decide; la base
-- es su registro durable, lo que la recupera tras un reinicio (ADR-034 §5) y la
-- última barrera de INV-TERR-010.
--
-- territory_control no cambia: contested existe desde 000001 y desde E12 es «hay
-- una reclamación activa» (INV-TERR-006). units.status tampoco: CLAIMING sólo
-- vive en la RAM y se vuelca como IDLE, como ATTACKING. world_events.event_type
-- no tiene CHECK, así que TerritoryClaimStarted no necesita migración.

CREATE TABLE territory_claims (
    id               bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    territory_id     bigint      NOT NULL REFERENCES territories (id) ON DELETE CASCADE,
    unit_id          bigint      NOT NULL REFERENCES units (id),
    player_id        uuid        NOT NULL REFERENCES players (id),
    city_id          bigint      NOT NULL REFERENCES cities (id),
    -- El dueño del territorio al empezar; NULL si era libre.
    owner_player_id  uuid        NULL REFERENCES players (id) ON DELETE SET NULL,
    x                integer     NOT NULL,
    y                integer     NOT NULL,
    started_at_ms    bigint      NOT NULL,
    completes_at_ms  bigint      NOT NULL,
    status           text        NOT NULL,
    end_reason       text        NULL,
    request_id       text        NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    ended_at         timestamptz NULL,
    CONSTRAINT territory_claims_status_valid CHECK (status IN ('ACTIVE', 'COMPLETED', 'FAILED')),
    CONSTRAINT territory_claims_after_start CHECK (completes_at_ms > started_at_ms),
    -- Una reclamación terminada sabe cuándo terminó.
    CONSTRAINT territory_claims_ended_has_time CHECK ((status = 'ACTIVE') = (ended_at IS NULL)),
    CONSTRAINT territory_claims_not_own CHECK (owner_player_id IS NULL OR owner_player_id <> player_id)
);

-- INV-TERR-010: como mucho una reclamación activa por territorio.
CREATE UNIQUE INDEX territory_claims_one_active_per_territory
    ON territory_claims (territory_id) WHERE status = 'ACTIVE';

-- Un guerrero sostiene como mucho un estandarte.
CREATE UNIQUE INDEX territory_claims_one_active_per_unit
    ON territory_claims (unit_id) WHERE status = 'ACTIVE';

-- Idempotencia del comienzo: un reintento de la cola no crea otra fila.
CREATE UNIQUE INDEX territory_claims_unique_request ON territory_claims (player_id, request_id);
