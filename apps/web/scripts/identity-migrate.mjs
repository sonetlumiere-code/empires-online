#!/usr/bin/env node
/**
 * Aplica las migraciones del almacén de identidad (ADR-018 §2.1).
 *
 * El servidor de Next.js ya lo hace al arrancar (instrumentation.ts); esto existe
 * para hacerlo antes de un despliegue o para comprobar el estado.
 *
 *   pnpm run identity:migrate
 */
import pg from 'pg';

import { loadRootDotEnv } from '../identity/dotenv.mjs';
import { migrateIdentity } from '../identity/migrations.mjs';

loadRootDotEnv();
const url = process.env.EO_IDENTITY_POSTGRES_URL;
if (!url) {
  console.error('\n  ✗ falta EO_IDENTITY_POSTGRES_URL (en local: node scripts/pg-local.mjs identity)\n');
  process.exit(1);
}

const client = new pg.Client({ connectionString: url });
try {
  await client.connect();
  const applied = await migrateIdentity(client);
  console.log(applied.length ? `Migraciones aplicadas: ${applied.join(', ')}` : 'El almacén de identidad ya estaba al día.');
} catch (err) {
  console.error(`\n  ✗ ${err instanceof Error ? err.message : err}\n`);
  process.exitCode = 1;
} finally {
  await client.end().catch(() => undefined);
}
