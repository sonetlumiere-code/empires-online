-- SPEC-ECON-FOREST (docs/specs/economy-forest.md), ADR-025: la madera del
-- bosque, en una capa aparte del terreno, que no cambia.
--
-- Una fila por tile FOREST que alguien ha empezado a talar; un tile sin fila
-- está lleno. regrows_at_ms es el instante absoluto en que vuelve a estarlo:
-- una fila vencida cuenta como llena y la siguiente carga parte de 100.

CREATE TABLE forest_wood (
    x             integer NOT NULL,
    y             integer NOT NULL,
    remaining     integer NOT NULL,
    regrows_at_ms bigint  NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT forest_wood_pkey PRIMARY KEY (x, y),
    -- INV-ECON-007: entre 0 y la capacidad de un tile (forest.Capacity).
    CONSTRAINT forest_wood_remaining_range CHECK (remaining BETWEEN 0 AND 100)
);
