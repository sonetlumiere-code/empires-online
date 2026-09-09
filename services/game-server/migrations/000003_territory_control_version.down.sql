-- Revierte la columna de concurrencia optimista de `territory_control`.
--
-- Revertir esto deja la tabla sin protección contra escrituras perdidas. Sólo
-- tiene sentido al deshacer M6 entero.

BEGIN;

ALTER TABLE territory_control
    DROP COLUMN version;

COMMIT;
