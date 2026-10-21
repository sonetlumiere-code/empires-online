-- Prepara una instancia de PostgreSQL YA EXISTENTE para Empires Online.
--
-- Sólo hace falta si NO usas Docker (`pnpm run db:up`), sino un PostgreSQL
-- instalado de forma nativa. Se ejecuta una única vez, como superusuario:
--
--   psql -U postgres -f scripts/setup-local-db.sql
--
-- psql pedirá la contraseña de `postgres` de forma interactiva: no hay que
-- escribirla en ningún archivo ni pasarla por la línea de órdenes, donde
-- quedaría registrada en el historial del shell.
--
-- Crea un rol y dos bases separadas: la de desarrollo y la de tests. Tenerlas
-- separadas evita que el TRUNCATE de la suite de integración se lleve por
-- delante la partida que estabas probando.

-- El rol de la aplicación. NO es superusuario: el Game Server no necesita serlo,
-- y limitarlo acota el daño de un despiste o de una inyección.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'empires') THEN
        CREATE ROLE empires LOGIN PASSWORD 'empires_dev_password';
        RAISE NOTICE 'rol "empires" creado';
    ELSE
        RAISE NOTICE 'rol "empires" ya existía: no se toca su contraseña';
    END IF;
END
$$;

-- CREATE DATABASE no admite IF NOT EXISTS y tampoco puede ir dentro de un
-- bloque DO, así que se generan las sentencias sólo si faltan y se ejecutan
-- con \gexec.
--
-- Se crean con OWNER empires a propósito: desde PostgreSQL 15 el esquema
-- `public` pertenece a `pg_database_owner`, así que el dueño de la base ya tiene
-- permiso para crear objetos y no hace falta ningún GRANT adicional (que además
-- obligaría a reconectar y a volver a escribir la contraseña).
SELECT 'CREATE DATABASE empires OWNER empires'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'empires')
\gexec

SELECT 'CREATE DATABASE empires_test OWNER empires'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'empires_test')
\gexec

SELECT
    'Listo. Bases disponibles: ' || string_agg(datname, ', ' ORDER BY datname)
    AS resultado
FROM pg_database
WHERE datname IN ('empires', 'empires_test');

-- ─────────────────────────────────────────────────────────────
-- Almacén de identidad de apps/web (ADR-018): su propio rol y sus dos bases.
--
-- La contraseña del rol se pide aquí, de forma interactiva, y no se escribe en
-- ningún fichero del repositorio: después hay que ponerla en el .env local, en
-- EO_IDENTITY_POSTGRES_URL y EO_TEST_IDENTITY_POSTGRES_URL. (Con el cluster
-- propio, `pnpm run pg:identity` hace todo esto y genera la contraseña al azar.)
-- ─────────────────────────────────────────────────────────────

\prompt 'Contraseña para el rol empires_identity (no se guarda en ningún fichero): ' identity_password

SELECT format('CREATE ROLE empires_identity LOGIN PASSWORD %L', :'identity_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'empires_identity')
\gexec

SELECT format('ALTER ROLE empires_identity PASSWORD %L', :'identity_password')
WHERE EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'empires_identity')
\gexec

SELECT 'CREATE DATABASE empires_identity OWNER empires_identity'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'empires_identity')
\gexec

SELECT 'CREATE DATABASE empires_identity_test OWNER empires_identity'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'empires_identity_test')
\gexec

-- Sin CONNECT público: el rol de identidad no abre las bases del juego.
REVOKE CONNECT ON DATABASE empires, empires_test, empires_identity, empires_identity_test FROM PUBLIC;
