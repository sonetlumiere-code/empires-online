-- SPEC-CITY-FALL (docs/specs/city-fall.md), ADR-050: la caída de una ciudad.
--
-- Una ciudad caída no se borra ni cambia de dueño: se queda como fila con
-- fallen_at, porque trade_offers, caravans, market_offers y territory_claims la
-- referencian sin cascada (ADR-050 §1). Las ciudades en pie son las de
-- fallen_at IS NULL, y de ellas cada jugador tiene exactamente una
-- (INV-CITY-020): el índice parcial lo garantiza en la base, y la transacción
-- de la caída arrasa antes de refundar.
--
-- city_assaults guarda cada asalto y su ciclo de vida, como territory_claims
-- guarda las reclamaciones (ADR-050 §2): la RAM decide, la base es su registro
-- durable, lo que lo recupera tras un reinicio y la última barrera de
-- INV-CITY-021. units.status no cambia: la unidad que asalta está CLAIMING en
-- la RAM y se vuelca IDLE. world_events.event_type no tiene CHECK, así que
-- CityAssaultStarted y CityFell no necesitan migración.

ALTER TABLE cities ADD COLUMN fallen_at timestamptz NULL;

-- Otra ciudad puede fundar donde estuvo una caída.
ALTER TABLE cities DROP CONSTRAINT cities_unique_center;
CREATE UNIQUE INDEX cities_unique_center ON cities (center_x, center_y) WHERE fallen_at IS NULL;

-- INV-CITY-020: una ciudad en pie por jugador.
CREATE UNIQUE INDEX cities_one_standing_per_owner ON cities (owner_player_id) WHERE fallen_at IS NULL;

-- Una ciudad caída no vuelve a estar en pie.
CREATE FUNCTION cities_fallen_is_final() RETURNS trigger AS $$
BEGIN
    IF OLD.fallen_at IS NOT NULL AND NEW.fallen_at IS DISTINCT FROM OLD.fallen_at THEN
        RAISE EXCEPTION 'la ciudad % ya cayó: no vuelve a estar en pie (INV-CITY-020)', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER cities_fallen_is_final
    BEFORE UPDATE OF fallen_at ON cities
    FOR EACH ROW EXECUTE FUNCTION cities_fallen_is_final();

CREATE TABLE city_assaults (
    id             bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    city_id        bigint      NOT NULL REFERENCES cities (id),
    unit_id        bigint      NOT NULL REFERENCES units (id),
    player_id      uuid        NOT NULL REFERENCES players (id),
    -- La ciudad desde la que asalta, para la clave de la cola del vencedor.
    from_city_id   bigint      NOT NULL REFERENCES cities (id),
    x              integer     NOT NULL,
    y              integer     NOT NULL,
    started_at_ms  bigint      NOT NULL,
    ends_at_ms     bigint      NOT NULL,
    status         text        NOT NULL,
    end_reason     text        NULL,
    request_id     text        NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    ended_at       timestamptz NULL,
    CONSTRAINT city_assaults_status_valid CHECK (status IN ('ACTIVE', 'FELL', 'BROKEN')),
    CONSTRAINT city_assaults_after_start CHECK (ends_at_ms > started_at_ms),
    CONSTRAINT city_assaults_ended_has_time CHECK ((status = 'ACTIVE') = (ended_at IS NULL)),
    CONSTRAINT city_assaults_not_own CHECK (from_city_id <> city_id)
);

-- INV-CITY-021: como mucho un asalto activo por ciudad.
CREATE UNIQUE INDEX city_assaults_one_active_per_city ON city_assaults (city_id) WHERE status = 'ACTIVE';

-- Una unidad sostiene como mucho un estandarte.
CREATE UNIQUE INDEX city_assaults_one_active_per_unit ON city_assaults (unit_id) WHERE status = 'ACTIVE';

-- Idempotencia del comienzo: un reintento de la cola no crea otra fila.
CREATE UNIQUE INDEX city_assaults_unique_request ON city_assaults (player_id, request_id);
