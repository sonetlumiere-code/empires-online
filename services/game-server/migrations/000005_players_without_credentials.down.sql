-- Recrea la columna, pero NO los hashes, que se perdieron con el up: vuelve
-- NULLABLE porque las filas existentes no tienen ningún valor que poner. El
-- código anterior a ADR-018 no puede autenticar a nadie con este esquema; esta
-- bajada sólo existe para poder deshacer la estructura.

ALTER TABLE players ADD COLUMN password_hash text;
