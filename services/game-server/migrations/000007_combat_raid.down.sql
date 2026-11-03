-- Retira el primer slice de combate y deja el esquema de 000006.
--
-- Una producción de guerrero en curso no cabe en el CHECK de 000006: se borra
-- y su población reservada vuelve a la ciudad (INV-CITY-017). Los guerreros
-- que existan se quedan como filas: unit_type no tiene CHECK, y la validación
-- de carga los señalará (INV-UNIT-009) si el código ya no los conoce.

UPDATE cities c
   SET population = population - 1, version = version + 1
  FROM city_productions p
 WHERE p.city_id = c.id AND p.unit_type = 'WARRIOR';
DELETE FROM city_productions WHERE unit_type = 'WARRIOR';

ALTER TABLE city_productions DROP CONSTRAINT city_productions_unit_type_valid;
ALTER TABLE city_productions ADD CONSTRAINT city_productions_unit_type_valid
    CHECK (unit_type IN ('VILLAGER'));

ALTER TABLE units DROP CONSTRAINT units_dead_iff_zero_hp;
ALTER TABLE units DROP CONSTRAINT units_loot_non_negative;
ALTER TABLE units DROP COLUMN loot;
