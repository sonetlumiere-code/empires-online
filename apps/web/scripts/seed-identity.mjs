#!/usr/bin/env node
/**
 * Cuentas de los jugadores de desarrollo en el almacén de identidad (ADR-018).
 *
 * `cmd/devseed` funda en el mundo a dev_norte, dev_sur, dev_este y dev_oeste con
 * un id determinista —UUID v5 de su nombre—; este script crea sus cuentas con ese
 * mismo id, ya ACTIVE, para que puedan entrar. `pnpm run dev:seed` ejecuta los
 * dos, uno tras otro.
 *
 * Sólo en desarrollo: se niega si EO_ENV no es `development`. Idempotente.
 */
import { createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';

import pg from 'pg';

import { loadRootDotEnv } from '../identity/dotenv.mjs';
import { migrateIdentity } from '../identity/migrations.mjs';
import { hashPassword } from '../identity/password.mjs';

/** Los de internal/devseed.Players. */
export const DEV_PLAYERS = ['dev_norte', 'dev_sur', 'dev_este', 'dev_oeste'];

/** El de internal/devseed.Namespace. */
export const DEV_NAMESPACE = '5eed0114-0000-4000-8000-000000000000';

/**
 * Contraseña de las cuentas de desarrollo. No es un secreto: es un valor de
 * prueba para una base local, y este script se niega a ejecutarse fuera de
 * EO_ENV=development.
 */
export const DEV_PASSWORD = 'semilla-de-desarrollo';

/** UUID v5 (RFC 9562 §5.5), el mismo que `uuid.NewSHA1` de Go. */
export function uuidV5(name, namespace = DEV_NAMESPACE) {
  const ns = Buffer.from(namespace.replace(/-/g, ''), 'hex');
  const bytes = createHash('sha1').update(Buffer.concat([ns, Buffer.from(name, 'utf8')])).digest().subarray(0, 16);
  bytes[6] = (bytes[6] & 0x0f) | 0x50;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const h = bytes.toString('hex');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

async function main() {
  loadRootDotEnv();
  if (process.env.EO_ENV !== 'development') {
    throw new Error(`EO_ENV=${JSON.stringify(process.env.EO_ENV)}: las cuentas de desarrollo sólo se crean con EO_ENV=development`);
  }
  const url = process.env.EO_IDENTITY_POSTGRES_URL;
  if (!url) throw new Error('falta EO_IDENTITY_POSTGRES_URL (créala con: node scripts/pg-local.mjs identity)');

  const client = new pg.Client({ connectionString: url });
  await client.connect();
  try {
    await migrateIdentity(client);
    const hash = await hashPassword(DEV_PASSWORD);
    console.log('\nCuentas de desarrollo (almacén de identidad)\n');
    for (const username of DEV_PLAYERS) {
      const id = uuidV5(username);
      const { rows } = await client.query('SELECT id FROM accounts WHERE username = $1', [username]);
      if (rows[0]) {
        console.log(rows[0].id === id ? `  = ${username} ya existía` : `  ! ${username} existe con otro id (${rows[0].id}): no se toca`);
        continue;
      }
      await client.query(
        `INSERT INTO accounts (id, username, password_hash, status, activated_at) VALUES ($1, $2, $3, 'ACTIVE', now())`,
        [id, username, hash],
      );
      console.log(`  + ${username}`);
    }
    console.log(`\n  Contraseña de todas: ${DEV_PASSWORD}\n`);
  } finally {
    await client.end();
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((err) => {
    console.error(`\n  ✗ ${err instanceof Error ? err.message : err}\n`);
    process.exit(1);
  });
}
