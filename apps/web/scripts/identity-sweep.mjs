#!/usr/bin/env node
/**
 * Resuelve las cuentas PENDING abandonadas del almacén de identidad (ADR-020).
 *
 * Una cuenta PENDING cuyo último intento de fundación tiene más de una hora se
 * resuelve preguntando al Game Server si su jugador existe: si existe, la
 * cuenta se activa; si no, se borra; si no contesta, se deja para la próxima
 * pasada (INV-PLAYER-010). El alta ya resuelve la que le ocupa el nombre; esto
 * limpia las demás. Se puede ejecutar a mano o desde un cron, y repetir: cada
 * pasada sólo toca lo que sigue abandonado.
 *
 *   pnpm run identity:sweep            # hasta 500 cuentas
 *   pnpm run identity:sweep -- 50      # hasta 50
 *
 * Termina con código 2 si alguna cuenta quedó sin resolver porque el Game
 * Server no contestó, para que un cron lo vea.
 */
import { randomUUID } from 'node:crypto';
import { pathToFileURL } from 'node:url';

import pg from 'pg';

import { loadRootDotEnv } from '../identity/dotenv.mjs';
import {
  activatePending,
  deleteStalePending,
  listStalePending,
  lookupPlayer,
  sweepStalePending,
} from '../identity/pending.mjs';

/** Cuántas cuentas resuelve una pasada si no se dice otra cosa. */
export const DEFAULT_LIMIT = 500;

/** @param {string | undefined} raw */
export function parseLimit(raw) {
  if (raw === undefined) return DEFAULT_LIMIT;
  const n = Number(raw);
  if (!Number.isInteger(n) || n < 1 || n > 100_000) throw new Error(`límite inválido: ${JSON.stringify(raw)} (entero entre 1 y 100000)`);
  return n;
}

/**
 * Base del listener INTERNO del Game Server (EO_INTERNAL_ADDR, 127.0.0.1:8081
 * por defecto), no el público de :8080, que ya no sirve /internal.
 *
 * @param {Record<string, string | undefined>} [env]
 */
export function internalBaseUrl(env = process.env) {
  return (env.EO_GAME_SERVER_INTERNAL_URL || 'http://localhost:8081').replace(/\/+$/, '');
}

/** Una línea JSON por evento, como los logs del servidor. */
const log = {
  info: (msg, fields) => console.log(JSON.stringify({ level: 'info', msg, ...fields })),
  warn: (msg, fields) => console.log(JSON.stringify({ level: 'warn', msg, ...fields })),
};

async function main() {
  loadRootDotEnv();
  const limit = parseLimit(process.argv[2]);
  const url = process.env.EO_IDENTITY_POSTGRES_URL;
  if (!url) throw new Error('falta EO_IDENTITY_POSTGRES_URL (en local: node scripts/pg-local.mjs identity)');
  const secret = process.env.EO_AUTH_JWT_SECRET ?? '';
  if (secret.length < 32) throw new Error('falta EO_AUTH_JWT_SECRET o tiene menos de 32 caracteres');
  const baseUrl = internalBaseUrl();

  const pool = new pg.Pool({ connectionString: url, max: 2 });
  try {
    const report = await sweepStalePending(
      {
        list: (n) => listStalePending(pool, n),
        lookup: (id) => lookupPlayer({ baseUrl, secret, nowMs: Date.now, randomUUID }, id),
        activate: (id) => activatePending(pool, id),
        removeIfStale: (id) => deleteStalePending(pool, id),
        log,
      },
      limit,
    );
    console.log(
      `\nCuentas PENDING abandonadas: ${report.activated} activadas, ${report.deleted} borradas, ` +
        `${report.kept} cambiaron mientras se consultaban, ${report.unavailable} sin respuesta del Game Server\n`,
    );
    if (report.unavailable > 0) process.exitCode = 2;
  } finally {
    await pool.end();
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((err) => {
    console.error(`\n  ✗ ${err instanceof Error ? err.message : err}\n`);
    process.exit(1);
  });
}
