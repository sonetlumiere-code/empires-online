-- ADR-018: la identidad (nombre de acceso, hash de la contraseña, sesión web)
-- vive en el almacén propio de apps/web, no en el esquema del juego. El Game
-- Server deja de verificar contraseñas y este esquema deja de contener ninguna
-- credencial (INV-PLAYER-007).
--
-- Los hashes existentes NO se migran a ninguna parte: son de cuentas de pruebas
-- de humo y de la siembra de desarrollo. Sus jugadores siguen en el mundo.

ALTER TABLE players DROP COLUMN password_hash;
