-- Concurrencia optimista para `territory_control`.
--
-- La spec de territorio (docs/specs/territory.md §10) y el entregable 4 de M6
-- exigen que todo cambio de ownership se persista con concurrencia optimista
-- sobre una columna `version`, y su criterio de aceptación es explícito: «un
-- UPDATE con `version` desactualizada es rechazado». La migración 000001 creó
-- la tabla sin esa columna.
--
-- Es una omisión, no una decisión: `cities` sí la tiene, con el mismo tipo y el
-- mismo DEFAULT, comentada como «concurrencia optimista». Se corrige aquí en
-- lugar de reescribir 000001 porque una migración ya aplicada no se toca: la
-- CI, cualquier entorno y el propio historial de esquema esperan que 000001
-- produzca exactamente lo que produjo la primera vez.
--
-- Sin esta columna, dos cambios simultáneos sobre el mismo territorio se
-- pisarían en silencio y el último en escribir ganaría sin que nadie se
-- enterase. Con ella, el segundo afecta a 0 filas y el servicio puede
-- reintentar sobre el estado nuevo.

BEGIN;

ALTER TABLE territory_control
    ADD COLUMN version integer NOT NULL DEFAULT 0;

COMMIT;
