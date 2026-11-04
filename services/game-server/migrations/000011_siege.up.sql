-- SPEC-COMBAT-SIEGE (docs/specs/combat-siege.md), ADR-029: el asedio destruye
-- casas, y una ciudad puede quedar por encima de su límite de población.
--
-- 1. INV-CITY-002 pasa a «nada se crea por encima del límite». El CHECK
--    cities_population_within_limit prohibía cualquier fila con
--    population > population_limit; el trigger que lo sustituye sólo rechaza
--    las escrituras que SUBEN la población por encima del límite. Bajar el
--    límite (una casa destruida) o la población (una muerte) siempre pasa.
--    Rechaza con check_violation y el nombre de la restricción de antes, para
--    que quien lo traducía a city.ErrPopulationLimit no cambie.
--
--    La única excepción es el recuento de la validación de carga
--    (INV-CITY-017): recuenta unidades que ya existen, no crea ninguna, y lo
--    declara con SET LOCAL eo.population_recount = 'on' (ADR-029 §3).
--
-- 2. Las casas destruidas conservan su fila con status DESTROYED, como las
--    unidades DEAD, y no se cargan al arrancar. El único por tile pasa a valer
--    sólo para las no destruidas: se puede reconstruir en el mismo sitio.

ALTER TABLE cities DROP CONSTRAINT cities_population_within_limit;

CREATE FUNCTION cities_population_not_raised_over_limit() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    -- Un límite negativo lo rechaza su propio CHECK (cities_population_limit_check):
    -- el trigger, que va antes, no le quita el error.
    IF NEW.population_limit >= 0
       AND NEW.population > NEW.population_limit
       AND (TG_OP = 'INSERT' OR NEW.population > OLD.population)
       AND COALESCE(current_setting('eo.population_recount', true), '') <> 'on' THEN
        RAISE EXCEPTION 'la población de la ciudad % (%) no puede subir por encima de su límite (%)',
            NEW.id, NEW.population, NEW.population_limit
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'cities_population_within_limit',
                  TABLE = 'cities';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cities_population_not_raised_over_limit
    BEFORE INSERT OR UPDATE OF population, population_limit ON cities
    FOR EACH ROW EXECUTE FUNCTION cities_population_not_raised_over_limit();

ALTER TABLE buildings DROP CONSTRAINT buildings_status_valid;
ALTER TABLE buildings ADD CONSTRAINT buildings_status_valid
    CHECK (status IN ('UNDER_CONSTRUCTION', 'COMPLETE', 'DESTROYED'));

-- RN-ECON-007: un edificio en pie por tile.
ALTER TABLE buildings DROP CONSTRAINT buildings_unique_tile;
CREATE UNIQUE INDEX buildings_unique_tile ON buildings (x, y) WHERE status <> 'DESTROYED';
