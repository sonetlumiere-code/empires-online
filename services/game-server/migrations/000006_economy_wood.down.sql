-- Retira el primer slice de economía y deja el esquema de 000005.
--
-- Sin las casas y sin las producciones, la población y el límite vuelven a
-- sus fuentes de antes: el recuento de unidades vivas y el population_cap de
-- la era. Si una ciudad tiene más unidades vivas que el límite de su era (las
-- casas lo permitían), el límite queda en el recuento, como repara la carga
-- (INV-CITY-002): no se destruye ninguna unidad.

UPDATE cities c
   SET population = COALESCE((
           SELECT count(*) FROM units u WHERE u.city_id = c.id AND u.status <> 'DEAD'
       ), 0),
       population_limit = GREATEST(
           (SELECT e.population_cap FROM eras e WHERE e.code = c.era),
           COALESCE((
               SELECT count(*) FROM units u WHERE u.city_id = c.id AND u.status <> 'DEAD'
           ), 0)
       ),
       version = version + 1;

DROP TABLE city_productions;
DROP TABLE buildings;
DROP TABLE unit_tasks;
DROP TABLE city_stocks;

-- Una unidad que trabajaba queda parada donde está.
UPDATE units SET status = 'IDLE' WHERE status = 'WORKING';
ALTER TABLE units DROP CONSTRAINT units_status_valid;
ALTER TABLE units ADD CONSTRAINT units_status_valid
    CHECK (status IN ('IDLE', 'MOVING', 'GARRISONED', 'HIDDEN', 'DEAD'));
