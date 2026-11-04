-- Retira la piedra y el avance de era, y deja el esquema de 000011.
--
-- Se pierden la piedra de las ciudades, las colinas picadas (todas vuelven a
-- estar llenas), las tareas de piedra (la validación de carga deja IDLE a sus
-- unidades, INV-ECON-003) y los avances en curso con lo que costaron. La era
-- de una ciudad que ya avanzó se queda: BRONZE_AGE es una era válida del
-- catálogo desde 000002 y su límite sigue saliendo de ella (INV-CITY-003).

DROP TABLE city_era_advances;
DROP TABLE hill_stone;

DELETE FROM unit_tasks WHERE kind = 'GATHER_STONE';
ALTER TABLE unit_tasks DROP CONSTRAINT unit_tasks_kind_valid;
ALTER TABLE unit_tasks ADD CONSTRAINT unit_tasks_kind_valid
    CHECK (kind IN ('GATHER_WOOD'));

DELETE FROM city_stocks WHERE resource = 'STONE';
ALTER TABLE city_stocks DROP CONSTRAINT city_stocks_resource_valid;
ALTER TABLE city_stocks ADD CONSTRAINT city_stocks_resource_valid
    CHECK (resource IN ('WOOD'));
