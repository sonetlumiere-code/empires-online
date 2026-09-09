-- Revierte los datos semilla de los catálogos.
BEGIN;

DELETE FROM eras WHERE code IN ('STONE_AGE', 'BRONZE_AGE', 'IRON_AGE', 'CASTLE_AGE');
DELETE FROM factions WHERE code IN ('ORDER', 'CHAOS', 'NEUTRAL');
DELETE FROM civilizations WHERE code IN ('ROMAN', 'BYZANTINE', 'PERSIAN', 'NORSE');

COMMIT;
