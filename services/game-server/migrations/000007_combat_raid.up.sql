-- SPEC-COMBAT-RAID (docs/specs/combat-raid.md), ADR-024: el primer slice de
-- combate. El guerrero, su botín y la regla que ata la muerte a la vida.

-- RN-COMBAT-010: el botín que carga un WARRIOR hasta depositarlo. La carga de
-- un aldeano sigue en su tarea (unit_tasks.carrying).
ALTER TABLE units ADD COLUMN loot integer NOT NULL DEFAULT 0;
ALTER TABLE units ADD CONSTRAINT units_loot_non_negative CHECK (loot >= 0);

-- INV-COMBAT-001: una unidad está DEAD si y sólo si su hp es 0. Las filas que
-- ya existen la cumplen: hasta este slice nada producía DEAD ni hp = 0.
ALTER TABLE units ADD CONSTRAINT units_dead_iff_zero_hp CHECK ((status = 'DEAD') = (hp = 0));

-- RN-ECON-010 con el catálogo nuevo: también se entrena el guerrero.
ALTER TABLE city_productions DROP CONSTRAINT city_productions_unit_type_valid;
ALTER TABLE city_productions ADD CONSTRAINT city_productions_unit_type_valid
    CHECK (unit_type IN ('VILLAGER', 'WARRIOR'));
