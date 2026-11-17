-- Retira las unidades de civilización y deja el esquema de 000023.
--
-- Se pierden los entrenamientos de unidades exclusivas (en curso o el último
-- terminado). Las unidades exclusivas ya nacidas se quedan en units, que no
-- tiene CHECK de tipo: un servidor anterior no las conoce y su validación de
-- carga las excluye (INV-UNIT-009) y recalcula la población (INV-CITY-017).

DROP TRIGGER city_productions_civilization_unit ON city_productions;
DROP FUNCTION city_productions_civilization_unit();

DELETE FROM city_productions WHERE unit_type IN ('LEGIONARY', 'CATAPHRACT', 'HORSE_ARCHER', 'BERSERKER');
ALTER TABLE city_productions DROP CONSTRAINT city_productions_unit_type_valid;
ALTER TABLE city_productions ADD CONSTRAINT city_productions_unit_type_valid
    CHECK (unit_type IN ('VILLAGER', 'WARRIOR'));

ALTER TABLE civilizations DROP CONSTRAINT civilizations_unit_valid;
ALTER TABLE civilizations DROP CONSTRAINT civilizations_unit_unique;
ALTER TABLE civilizations DROP COLUMN unit;
