-- SPEC-TECHNOLOGIES (docs/specs/technologies.md), ADR-046: el árbol tecnológico.
--
-- `technologies` es el catálogo: sus efectos son datos (rasgos por mil) que el
-- Game Server valida al arrancar y compone con los de la civilización
-- (INV-TECH-002). Un cambio de balance es una migración de catálogo que también
-- actualiza technologies.json (packages/protocol), o la CI falla.
--
-- `city_research` es la investigación en curso de una ciudad, como mucho una;
-- `player_technologies`, las que ya tiene cada jugador, para siempre. Los
-- triggers son la red de seguridad bajo la transacción del encargo
-- (INV-TECH-001, INV-PERSIST-005): una investigación no convive con un avance de
-- era, es de la era de la ciudad o anterior, con sus requisitos, de la
-- civilización del dueño si es exclusiva y de una tecnología que no tiene.

CREATE TABLE technologies (
    id              integer     PRIMARY KEY,
    code            text        NOT NULL UNIQUE,
    name            text        NOT NULL,
    era             text        NOT NULL REFERENCES eras (code),
    civilization_id integer     NULL REFERENCES civilizations (id),
    costs           jsonb       NOT NULL DEFAULT '{}'::jsonb,
    foreign_goods   integer     NOT NULL DEFAULT 0,
    own_good        integer     NOT NULL DEFAULT 0,
    duration_ms     integer     NOT NULL,
    requires        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    effects         jsonb       NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT technologies_duration CHECK (duration_ms > 0),
    CONSTRAINT technologies_goods_amounts CHECK (foreign_goods >= 0 AND own_good >= 0),
    -- SPEC-TECHNOLOGIES Q5: una exclusiva cobra su bien propio y ningún ajeno;
    -- una común, ningún bien propio.
    CONSTRAINT technologies_exclusive_goods CHECK (
        (civilization_id IS NULL AND own_good = 0)
        OR (civilization_id IS NOT NULL AND own_good > 0 AND foreign_goods = 0)
    ),
    CONSTRAINT technologies_json_shapes CHECK (
        jsonb_typeof(costs) = 'object' AND jsonb_typeof(requires) = 'array' AND jsonb_typeof(effects) = 'object'
    )
);

-- El catálogo de SPEC-TECHNOLOGIES §4.1: ocho comunes, dos por era, y una
-- exclusiva por civilización desde la Edad de Hierro.
INSERT INTO technologies (id, code, name, era, civilization_id, costs, foreign_goods, own_good, duration_ms, requires, effects) VALUES
    (1,  'STONE_TOOLS',      'Herramientas de piedra', 'STONE_AGE',  NULL, '{"WOOD": 150, "STONE": 50}',  0,  0, 60000,  '[]',                 '{"economy.gather_speed": 1100}'),
    (2,  'BASKETRY',         'Cestería',               'STONE_AGE',  NULL, '{"WOOD": 120, "STONE": 30}',  0,  0, 60000,  '[]',                 '{"economy.gather_carry": 1200}'),
    (3,  'BRONZE_WEAPONS',   'Armas de bronce',        'BRONZE_AGE', NULL, '{"WOOD": 250, "STONE": 150}', 0,  0, 120000, '[]',                 '{"combat.attack_damage": 1100}'),
    (4,  'MASONRY',          'Albañilería',            'BRONZE_AGE', NULL, '{"WOOD": 200, "STONE": 200}', 0,  0, 120000, '[]',                 '{"building.house_hp": 1200}'),
    (5,  'WHEEL',            'La rueda',               'IRON_AGE',   NULL, '{"WOOD": 300, "STONE": 200}', 30, 0, 180000, '[]',                 '{"unit.move_speed": 1050}'),
    (6,  'GUILDS',           'Gremios',                'IRON_AGE',   NULL, '{"WOOD": 300, "STONE": 300}', 30, 0, 180000, '[]',                 '{"production.speed": 1100}'),
    (7,  'IRON_WEAPONS',     'Armas de hierro',        'CASTLE_AGE', NULL, '{"WOOD": 400, "STONE": 300}', 60, 0, 300000, '["BRONZE_WEAPONS"]', '{"combat.attack_damage": 1100}'),
    (8,  'ARCHITECTURE',     'Arquitectura',           'CASTLE_AGE', NULL, '{"WOOD": 400, "STONE": 400}', 60, 0, 300000, '["GUILDS"]',         '{"production.speed": 1100}');

INSERT INTO technologies (id, code, name, era, civilization_id, costs, foreign_goods, own_good, duration_ms, requires, effects)
SELECT v.id, v.code, v.name, 'IRON_AGE', c.id, '{"WOOD": 200, "STONE": 200}', 0, 40, 180000, '[]', v.effects::jsonb
  FROM (VALUES
    (9,  'ROMAN_ROADS',      'Calzadas',          'ROMAN',     '{"unit.move_speed": 1100}'),
    (10, 'THEODOSIAN_WALLS', 'Muros de Teodosio', 'BYZANTINE', '{"building.house_hp": 1200}'),
    (11, 'CARAVANSERAI',     'Caravasares',       'PERSIAN',   '{"economy.gather_carry": 1200}'),
    (12, 'RAIDING',          'Incursiones',       'NORSE',     '{"combat.loot_carry": 1200}')
  ) AS v (id, code, name, civ, effects)
  JOIN civilizations c ON c.code = v.civ;

CREATE TABLE city_research (
    city_id         bigint      PRIMARY KEY REFERENCES cities (id) ON DELETE CASCADE,
    technology_id   integer     NOT NULL REFERENCES technologies (id),
    completes_at_ms bigint      NOT NULL,
    request_id      text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE player_technologies (
    player_id     uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    technology_id integer     NOT NULL REFERENCES technologies (id),
    researched_at timestamptz NOT NULL,
    PRIMARY KEY (player_id, technology_id)
);

-- Al nacer una investigación (INV-TECH-001): la ciudad no avanza de era; la
-- tecnología es de su era o anterior y, si es exclusiva, de la civilización del
-- dueño; el dueño no la tiene y tiene sus requisitos.
CREATE FUNCTION city_research_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_owner     uuid;
    v_civ       integer;
    v_city_ord  integer;
    v_tech_ord  integer;
    v_tech_civ  integer;
    v_requires  jsonb;
BEGIN
    IF EXISTS (SELECT 1 FROM city_era_advances WHERE city_id = NEW.city_id) THEN
        RAISE EXCEPTION 'una ciudad que avanza de era no investiga (INV-TECH-001): %', NEW.city_id
            USING ERRCODE = 'check_violation';
    END IF;
    SELECT c.owner_player_id, p.civilization_id, e.ordinal
      INTO v_owner, v_civ, v_city_ord
      FROM cities c
      JOIN players p ON p.id = c.owner_player_id
      JOIN eras e ON e.code = c.era
     WHERE c.id = NEW.city_id;
    SELECT e.ordinal, t.civilization_id, t.requires
      INTO v_tech_ord, v_tech_civ, v_requires
      FROM technologies t
      JOIN eras e ON e.code = t.era
     WHERE t.id = NEW.technology_id;
    IF v_city_ord < v_tech_ord THEN
        RAISE EXCEPTION 'la tecnología % pide una era posterior a la de la ciudad % (INV-TECH-001)', NEW.technology_id, NEW.city_id
            USING ERRCODE = 'check_violation';
    END IF;
    IF v_tech_civ IS NOT NULL AND v_tech_civ <> v_civ THEN
        RAISE EXCEPTION 'la tecnología % es de otra civilización (INV-TECH-001)', NEW.technology_id
            USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (SELECT 1 FROM player_technologies WHERE player_id = v_owner AND technology_id = NEW.technology_id) THEN
        RAISE EXCEPTION 'el dueño de la ciudad % ya tiene la tecnología % (INV-TECH-001)', NEW.city_id, NEW.technology_id
            USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (
        SELECT 1
          FROM jsonb_array_elements_text(v_requires) AS r (code)
         WHERE NOT EXISTS (
            SELECT 1
              FROM player_technologies pt
              JOIN technologies q ON q.id = pt.technology_id
             WHERE pt.player_id = v_owner AND q.code = r.code
         )
    ) THEN
        RAISE EXCEPTION 'al dueño de la ciudad % le falta un requisito de la tecnología % (INV-TECH-001)', NEW.city_id, NEW.technology_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER city_research_guard
    BEFORE INSERT ON city_research
    FOR EACH ROW EXECUTE FUNCTION city_research_guard();

-- Al nacer un avance de era: la ciudad no investiga (RN-ECON-026, INV-TECH-001).
CREATE FUNCTION city_era_advances_research_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM city_research WHERE city_id = NEW.city_id) THEN
        RAISE EXCEPTION 'una ciudad que investiga no avanza de era (INV-TECH-001): %', NEW.city_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER city_era_advances_research_guard
    BEFORE INSERT ON city_era_advances
    FOR EACH ROW EXECUTE FUNCTION city_era_advances_research_guard();

-- Al ganar una tecnología: una exclusiva sólo para un jugador de su civilización.
CREATE FUNCTION player_technologies_civ_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM technologies t
          JOIN players p ON p.id = NEW.player_id
         WHERE t.id = NEW.technology_id
           AND t.civilization_id IS NOT NULL
           AND t.civilization_id <> p.civilization_id
    ) THEN
        RAISE EXCEPTION 'la tecnología % es de otra civilización que la del jugador % (INV-TECH-001)', NEW.technology_id, NEW.player_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER player_technologies_civ_guard
    BEFORE INSERT ON player_technologies
    FOR EACH ROW EXECUTE FUNCTION player_technologies_civ_guard();
