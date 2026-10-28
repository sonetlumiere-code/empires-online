-- SPEC-ECON-WOOD (docs/specs/economy-wood.md), ADR-023: el primer slice de
-- economía. Un recurso por ciudad, la tarea de recolección, la casa y el
-- entrenamiento de aldeanos.

-- RN-ECON-003: estado nuevo de unidad, parada sobre el tile de recurso hasta
-- que vence su temporizador. INV-ECON-003 lo ata a la fase de su tarea.
ALTER TABLE units DROP CONSTRAINT units_status_valid;
ALTER TABLE units ADD CONSTRAINT units_status_valid
    CHECK (status IN ('IDLE', 'MOVING', 'GARRISONED', 'HIDDEN', 'WORKING', 'DEAD'));

-- §4.1: una fila por ciudad y recurso. Se escribe por deltas condicionados a
-- `version` (ADR-023): el volcado suma depósitos y sólo un encargo resta, en
-- la transacción de su efecto (INV-ECON-002).
CREATE TABLE city_stocks (
    city_id   bigint NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    resource  text   NOT NULL,
    amount    bigint NOT NULL DEFAULT 0,
    version   bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (city_id, resource),
    CONSTRAINT city_stocks_resource_valid CHECK (resource IN ('WOOD')),
    -- INV-ECON-001: el stock nunca es negativo.
    CONSTRAINT city_stocks_amount_non_negative CHECK (amount >= 0),
    CONSTRAINT city_stocks_version_non_negative CHECK (version >= 0)
);

-- Las ciudades que ya existen nacen con su fila a cero, igual que las nuevas.
INSERT INTO city_stocks (city_id, resource) SELECT id, 'WOOD' FROM cities;

-- §4.2: lo que una unidad está haciendo a lo largo de varios tramos. La clave
-- primaria materializa «una unidad, una tarea» (INV-ECON-003). Los tramos son
-- movimientos normales en unit_movements.
CREATE TABLE unit_tasks (
    unit_id        bigint      PRIMARY KEY REFERENCES units (id) ON DELETE CASCADE,
    kind           text        NOT NULL,
    target_x       integer     NOT NULL,
    target_y       integer     NOT NULL,
    phase          text        NOT NULL,
    phase_until_ms bigint,
    carrying       integer     NOT NULL DEFAULT 0,
    trips_done     integer     NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT unit_tasks_kind_valid CHECK (kind IN ('GATHER_WOOD')),
    CONSTRAINT unit_tasks_phase_valid CHECK (phase IN ('TO_RESOURCE', 'WORKING', 'TO_DEPOSIT')),
    -- Sólo la fase WORKING tiene vencimiento: es el que despierta la fase 5.
    CONSTRAINT unit_tasks_working_has_deadline
        CHECK ((phase = 'WORKING') = (phase_until_ms IS NOT NULL)),
    CONSTRAINT unit_tasks_carrying_non_negative CHECK (carrying >= 0),
    CONSTRAINT unit_tasks_trips_non_negative CHECK (trips_done >= 0)
);

-- §4.3: edificios dentro de la zona urbana. `request_id` hace idempotente el
-- encargo: un reintento tras perder el COMMIT no inserta ni debita dos veces
-- (ADR-014, ADR-023).
CREATE TABLE buildings (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    city_id         bigint      NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    type            text        NOT NULL,
    x               integer     NOT NULL,
    y               integer     NOT NULL,
    status          text        NOT NULL,
    completes_at_ms bigint      NOT NULL,
    request_id      text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT buildings_type_valid CHECK (type IN ('HOUSE')),
    CONSTRAINT buildings_status_valid CHECK (status IN ('UNDER_CONSTRUCTION', 'COMPLETE')),
    -- RN-ECON-007: un edificio por tile.
    CONSTRAINT buildings_unique_tile UNIQUE (x, y),
    -- Su índice, que empieza por city_id, sirve también para listar los
    -- edificios de una ciudad: no hace falta otro.
    CONSTRAINT buildings_unique_request UNIQUE (city_id, request_id)
);

-- §4.4: una producción por ciudad. `unit_id` es el aldeano que produjo: NULL
-- mientras está en curso, y el id de la unidad desde la transacción que la
-- inserta. Es lo que hace idempotente el nacimiento (ADR-023 §3), y lo que
-- distingue lo reservado en cities.population (INV-CITY-017).
CREATE TABLE city_productions (
    city_id         bigint      PRIMARY KEY REFERENCES cities (id) ON DELETE CASCADE,
    unit_type       text        NOT NULL,
    completes_at_ms bigint      NOT NULL,
    request_id      text        NOT NULL,
    unit_id         bigint      REFERENCES units (id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT city_productions_unit_type_valid CHECK (unit_type IN ('VILLAGER'))
);
