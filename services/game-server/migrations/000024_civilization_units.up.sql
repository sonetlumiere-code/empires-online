-- SPEC-CIVILIZATION-UNITS (docs/specs/civilization-units.md), ADR-043: la
-- unidad exclusiva de cada civilización.
--
-- 1. civilizations.unit dice qué unidad es de quién, como civilizations.good
--    dice qué bien (000016): el entrenamiento pregunta por ella, nunca por el
--    código de la civilización (RN-PLAYER-012). Una por civilización y sin
--    repetir (INV-UNIT-013). Las estadísticas viven en el catálogo de Go.
-- 2. city_productions admite los cuatro tipos nuevos.
-- 3. Un trigger repite en la base la regla que la RAM ya aplica: una ciudad sólo
--    encarga la unidad exclusiva de la civilización de su dueño, y desde la
--    Edad de Bronce (INV-UNIT-012). Responde con check_violation, que
--    EconomyRepo.Train traduce a INVALID_TARGET.
--
-- units.unit_type sigue sin CHECK de tipo (lo valida la carga, INV-UNIT-009).

ALTER TABLE civilizations ADD COLUMN unit text;
UPDATE civilizations SET unit = 'LEGIONARY'    WHERE code = 'ROMAN';
UPDATE civilizations SET unit = 'CATAPHRACT'   WHERE code = 'BYZANTINE';
UPDATE civilizations SET unit = 'HORSE_ARCHER' WHERE code = 'PERSIAN';
UPDATE civilizations SET unit = 'BERSERKER'    WHERE code = 'NORSE';
ALTER TABLE civilizations ALTER COLUMN unit SET NOT NULL;
ALTER TABLE civilizations ADD CONSTRAINT civilizations_unit_unique UNIQUE (unit);
ALTER TABLE civilizations ADD CONSTRAINT civilizations_unit_valid
    CHECK (unit IN ('LEGIONARY', 'CATAPHRACT', 'HORSE_ARCHER', 'BERSERKER'));

ALTER TABLE city_productions DROP CONSTRAINT city_productions_unit_type_valid;
ALTER TABLE city_productions ADD CONSTRAINT city_productions_unit_type_valid
    CHECK (unit_type IN ('VILLAGER', 'WARRIOR', 'LEGIONARY', 'CATAPHRACT', 'HORSE_ARCHER', 'BERSERKER'));

CREATE FUNCTION city_productions_civilization_unit() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    propia     text;
    ordinal_ciudad integer;
    ordinal_bronce integer;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM civilizations WHERE unit = NEW.unit_type) THEN
        RETURN NEW;
    END IF;
    SELECT cv.unit, e.ordinal INTO propia, ordinal_ciudad
      FROM cities c
      JOIN players p ON p.id = c.owner_player_id
      JOIN civilizations cv ON cv.id = p.civilization_id
      JOIN eras e ON e.code = c.era
     WHERE c.id = NEW.city_id;
    SELECT ordinal INTO ordinal_bronce FROM eras WHERE code = 'BRONZE_AGE';
    IF propia IS DISTINCT FROM NEW.unit_type THEN
        RAISE EXCEPTION 'la ciudad % no entrena %: no es la unidad de su civilización (INV-UNIT-012)',
            NEW.city_id, NEW.unit_type
            USING ERRCODE = 'check_violation';
    END IF;
    IF ordinal_ciudad < ordinal_bronce THEN
        RAISE EXCEPTION 'la ciudad % no entrena % antes de la Edad de Bronce (INV-UNIT-012)',
            NEW.city_id, NEW.unit_type
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER city_productions_civilization_unit
    BEFORE INSERT ON city_productions
    FOR EACH ROW EXECUTE FUNCTION city_productions_civilization_unit();
