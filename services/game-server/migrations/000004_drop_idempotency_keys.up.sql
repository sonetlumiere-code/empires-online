-- ADR-014: la deduplicación de comandos es de corto plazo y vive en Redis
-- (idem:{playerId}:{requestId}, 300 s). La tabla idempotency_keys nunca se
-- escribió; un comando cuyo efecto durable no se pueda repetir guardará su
-- requestId en la fila de su efecto, con UNIQUE, en la misma transacción.

DROP TABLE idempotency_keys;
