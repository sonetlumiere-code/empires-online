-- SPEC-SIEGE-ENGINES (docs/specs/siege-engines.md), ADR-048: el ariete y la
-- muralla.
--
-- 1. WALL es un tipo más de edificio, como el mercado de 000028, con un único
--    parcial de una en pie por ciudad (RN-SIEGE-001).
-- 2. Un trigger repite en la base la regla que la RAM ya aplica: la muralla va
--    en el tile del centro de su ciudad (ADR-048 §1). Cruza buildings con
--    cities, así que no cabe en un CHECK.
-- 3. city_productions admite el ariete (RAM). units.unit_type sigue sin CHECK
--    de tipo: lo valida la carga contra el catálogo de Go (INV-UNIT-009).
--
-- world_events.event_type no tiene CHECK: el derribo de una muralla es un
-- BuildingDestroyed más, con su tipo en el payload.

ALTER TABLE buildings DROP CONSTRAINT buildings_type_valid;
ALTER TABLE buildings ADD CONSTRAINT buildings_type_valid
    CHECK (type IN ('HOUSE', 'WORKSHOP', 'CASTLE', 'MARKET', 'WALL'));

-- RN-SIEGE-001: como mucho una muralla en pie por ciudad.
CREATE UNIQUE INDEX buildings_one_wall_per_city ON buildings (city_id)
    WHERE type = 'WALL' AND status <> 'DESTROYED';

-- RN-SIEGE-001, ADR-048 §1: la muralla, sólo en el centro de su ciudad.
CREATE FUNCTION buildings_wall_at_center() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.type = 'WALL' AND NOT EXISTS (
        SELECT 1 FROM cities WHERE id = NEW.city_id AND center_x = NEW.x AND center_y = NEW.y
    ) THEN
        RAISE EXCEPTION 'la muralla del edificio % no está en el centro de la ciudad % (RN-SIEGE-001)', NEW.id, NEW.city_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER buildings_wall_at_center
    BEFORE INSERT OR UPDATE OF type, x, y, city_id ON buildings
    FOR EACH ROW EXECUTE FUNCTION buildings_wall_at_center();

ALTER TABLE city_productions DROP CONSTRAINT city_productions_unit_type_valid;
ALTER TABLE city_productions ADD CONSTRAINT city_productions_unit_type_valid
    CHECK (unit_type IN ('VILLAGER', 'WARRIOR', 'LEGIONARY', 'CATAPHRACT', 'HORSE_ARCHER', 'BERSERKER', 'RAM'));
