/**
 * Cableado de producción de la identidad: configuración, almacén, cliente del
 * Game Server, servicio y capa HTTP, construidos una vez por proceso y la
 * primera vez que se necesitan (nunca durante `next build`).
 */
import 'server-only';

import { randomBytes, randomUUID } from 'node:crypto';

import { loadRootDotEnv } from '../../identity/dotenv.mjs';
import { AuthService, type Logger } from './authService';
import { loadAuthConfig } from './config';
import { createGameServerClient } from './gameServer';
import type { HttpDeps } from './http';
import { RateLimiter } from './rateLimit';
import { PgIdentityStore } from './store';

/** Logs estructurados en JSON por la salida estándar, como el Game Server. */
export const jsonLogger: Logger = {
  info: (msg, fields) => console.log(JSON.stringify({ ts: new Date().toISOString(), level: 'INFO', msg, ...fields })),
  warn: (msg, fields) => console.warn(JSON.stringify({ ts: new Date().toISOString(), level: 'WARN', msg, ...fields })),
  error: (msg, fields) => console.error(JSON.stringify({ ts: new Date().toISOString(), level: 'ERROR', msg, ...fields })),
};

let built: { deps: HttpDeps; store: PgIdentityStore } | null = null;

function build() {
  // En desarrollo el .env vive en la raíz del monorepo; Next.js sólo mira el de
  // apps/web. Nunca pisa lo que ya venga del entorno.
  loadRootDotEnv();
  const config = loadAuthConfig();
  const store = PgIdentityStore.connect(config.identityPostgresUrl);
  const nowMs = () => Date.now();
  const service = new AuthService({
    store,
    game: createGameServerClient({
      baseUrl: config.gameServerInternalUrl,
      secret: config.jwtSecret,
      nowMs,
      randomUUID,
    }),
    secret: config.jwtSecret,
    nowMs,
    randomUUID,
    newSessionToken: () => randomBytes(32).toString('base64url'),
    log: jsonLogger,
  });
  return {
    store,
    deps: {
      service,
      limiter: new RateLimiter(config.rateLimitPerMinute, config.rateLimitBurst),
      trustProxyHeaders: config.trustProxyHeaders,
      secureCookies: config.production,
      nowMs,
      log: jsonLogger,
    },
  };
}

export function httpDeps(): HttpDeps {
  built ??= build();
  return built.deps;
}

/**
 * Aplica las migraciones del almacén al arrancar el servidor, como hace el Game
 * Server con las suyas. Si falla, el servidor no arranca: servir el alta contra
 * un esquema a medias sería peor.
 */
export async function migrateOnStartup(): Promise<void> {
  built ??= build();
  const applied = await built.store.migrate();
  jsonLogger.info('almacén de identidad al día', { applied });
}
