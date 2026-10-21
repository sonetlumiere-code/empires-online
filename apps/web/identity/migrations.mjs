/**
 * Migraciones del almacén de identidad (ADR-018 §2.1).
 *
 * El almacén es de apps/web y tiene su propio ciclo de migración, separado del
 * esquema del juego, que sigue siendo exclusivo del Game Server (ADR-012). Mismo
 * modelo que allí con otro dueño: SQL versionado, aplicado en orden, una
 * transacción por versión, un registro de lo aplicado y un cerrojo para que dos
 * procesos no migren a la vez.
 *
 * El SQL vive aquí como texto, no en ficheros sueltos: así viaja dentro del
 * bundle del servidor de Next.js sin depender de rutas del disco.
 *
 * Una migración publicada NO se edita: se añade otra.
 */

/** «EO_IDNT» en ASCII. */
export const MIGRATION_LOCK_KEY = 0x454f5f49444e54n;

/** @type {ReadonlyArray<{ version: number, name: string, up: string }>} */
export const MIGRATIONS = [
  {
    version: 1,
    name: 'accounts_and_sessions',
    up: `
CREATE TABLE accounts (
    -- Es players.id del Game Server: lo genera apps/web en el alta y lo usa como
    -- sub del game ticket. No hay tabla de correspondencia.
    id             uuid        PRIMARY KEY,
    username       text        NOT NULL,
    password_hash  text        NOT NULL,
    -- PENDING hasta que el Game Server confirma el jugador; sólo una cuenta
    -- ACTIVE obtiene sesión y ticket (INV-PLAYER-009).
    status         text        NOT NULL DEFAULT 'PENDING',
    created_at     timestamptz NOT NULL DEFAULT now(),
    activated_at   timestamptz,
    CONSTRAINT accounts_username_unique UNIQUE (username),
    CONSTRAINT accounts_username_format CHECK (username ~ '^[A-Za-z0-9_-]{3,24}$'),
    CONSTRAINT accounts_status_valid CHECK (status IN ('PENDING', 'ACTIVE')),
    CONSTRAINT accounts_active_has_activation CHECK ((status = 'ACTIVE') = (activated_at IS NOT NULL)),
    CONSTRAINT accounts_password_hash_format CHECK (password_hash LIKE 'scrypt$%')
);

CREATE TABLE web_sessions (
    -- SHA-256 del token de la cookie. El token en claro sólo existe en la
    -- cookie del navegador: una copia de esta tabla no sirve para entrar.
    token_hash  bytea       PRIMARY KEY,
    account_id  uuid        NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    CONSTRAINT web_sessions_token_hash_length CHECK (length(token_hash) = 32),
    CONSTRAINT web_sessions_expiry_after_creation CHECK (expires_at > created_at)
);

CREATE INDEX web_sessions_account_idx ON web_sessions (account_id);
CREATE INDEX web_sessions_expiry_idx ON web_sessions (expires_at);
`,
  },
  {
    version: 2,
    name: 'accounts_founding_attempted_at',
    up: `
-- ADR-020: el último intento de fundar al jugador de una cuenta, en el reloj de
-- la base. Lo fija el alta y lo renueva el login de una cuenta PENDING antes de
-- llamar al Game Server; una PENDING cuyo intento tiene más de una hora es
-- abandonada y se puede resolver sin cruzarse con una fundación en vuelo.
ALTER TABLE accounts ADD COLUMN founding_attempted_at timestamptz;
UPDATE accounts SET founding_attempted_at = created_at;
ALTER TABLE accounts ALTER COLUMN founding_attempted_at SET DEFAULT now();
ALTER TABLE accounts ALTER COLUMN founding_attempted_at SET NOT NULL;

-- El barrido busca PENDING por antigüedad del intento.
CREATE INDEX accounts_pending_attempt_idx ON accounts (founding_attempted_at) WHERE status = 'PENDING';
`,
  },
];

/**
 * Aplica las migraciones pendientes. Idempotente.
 *
 * @param {{ query: (text: string, values?: unknown[]) => Promise<{ rows: any[] }> }} client
 *   una conexión dedicada (no un pool: el cerrojo es de sesión)
 * @returns {Promise<number[]>} las versiones aplicadas en esta llamada
 */
export async function migrateIdentity(client) {
  await client.query('SELECT pg_advisory_lock($1)', [MIGRATION_LOCK_KEY.toString()]);
  try {
    await client.query(`
      CREATE TABLE IF NOT EXISTS identity_schema_migrations (
          version     integer     PRIMARY KEY,
          name        text        NOT NULL,
          applied_at  timestamptz NOT NULL DEFAULT now()
      )`);
    const { rows } = await client.query('SELECT version FROM identity_schema_migrations');
    const applied = new Set(rows.map((r) => Number(r.version)));
    const done = [];
    for (const m of MIGRATIONS) {
      if (applied.has(m.version)) continue;
      await client.query('BEGIN');
      try {
        await client.query(m.up);
        await client.query('INSERT INTO identity_schema_migrations (version, name) VALUES ($1, $2)', [
          m.version,
          m.name,
        ]);
        await client.query('COMMIT');
      } catch (err) {
        await client.query('ROLLBACK');
        throw new Error(`migración de identidad ${m.version} (${m.name}) fallida: ${err instanceof Error ? err.message : err}`);
      }
      done.push(m.version);
    }
    return done;
  } finally {
    await client.query('SELECT pg_advisory_unlock($1)', [MIGRATION_LOCK_KEY.toString()]);
  }
}
