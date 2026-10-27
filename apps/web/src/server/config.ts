/**
 * Configuración del servidor de Next.js para la identidad (ADR-018 §2.2).
 *
 * Ninguna de estas variables lleva el prefijo NEXT_PUBLIC_: nunca llegan al
 * navegador. Los errores se reportan todos juntos, como hace el Game Server.
 */
import 'server-only';

import type { ProxyTrust, RateLimitPolicy } from './rateLimit';

export interface AuthRateLimitConfig {
  /** Alta y login por IP, sólo detrás de un proxy de confianza (EO_AUTH_RATE_LIMIT_PER_MINUTE / _BURST). */
  readonly ip: RateLimitPolicy;
  /** Alta y login por nombre de usuario, siempre (EO_AUTH_RATE_LIMIT_USER_PER_MINUTE / _BURST). */
  readonly user: RateLimitPolicy;
  /** Alta y login de todo el proceso: el tope del coste de scrypt (EO_AUTH_RATE_LIMIT_GLOBAL_PER_MINUTE / _BURST). */
  readonly global: RateLimitPolicy;
  /** Ticket y cierre de sesión de todo el proceso (EO_AUTH_TICKET_RATE_LIMIT_PER_MINUTE / _BURST). */
  readonly ticket: RateLimitPolicy;
}

export interface AuthConfig {
  readonly production: boolean;
  /** Secreto HS256 compartido con el Game Server (EO_AUTH_JWT_SECRET). */
  readonly jwtSecret: string;
  /** Almacén de identidad propio de apps/web (EO_IDENTITY_POSTGRES_URL). */
  readonly identityPostgresUrl: string;
  /** Base HTTP del Game Server para la fundación interna. */
  readonly gameServerInternalUrl: string;
  readonly rateLimits: AuthRateLimitConfig;
  /** Qué cabeceras del proxy creer para conocer la IP (EO_TRUST_PROXY_HEADERS, EO_TRUSTED_PROXY_COUNT). */
  readonly proxy: ProxyTrust;
}

export class ConfigError extends Error {
  constructor(readonly problems: readonly string[]) {
    super(`configuración inválida:\n  - ${problems.join('\n  - ')}`);
    this.name = 'ConfigError';
  }
}

function intIn(
  env: Record<string, string | undefined>,
  key: string,
  def: number,
  min: number,
  max: number,
  problems: string[],
): number {
  const raw = env[key];
  if (raw === undefined || raw === '') return def;
  const n = Number(raw);
  if (!Number.isInteger(n) || n < min || n > max) {
    problems.push(`${key} debe ser un entero entre ${min} y ${max}`);
    return def;
  }
  return n;
}

function policy(
  env: Record<string, string | undefined>,
  prefix: string,
  def: RateLimitPolicy,
  problems: string[],
): RateLimitPolicy {
  return {
    perMinute: intIn(env, `${prefix}_PER_MINUTE`, def.perMinute, 1, 100000, problems),
    burst: intIn(env, `${prefix}_BURST`, def.burst, 1, 10000, problems),
  };
}

const wider = (a: RateLimitPolicy, b: RateLimitPolicy) => a.perMinute >= b.perMinute && a.burst >= b.burst;

export function loadAuthConfig(env: Record<string, string | undefined> = process.env): AuthConfig {
  const problems: string[] = [];
  const production = env.EO_ENV === 'production';

  const jwtSecret = env.EO_AUTH_JWT_SECRET ?? '';
  if (!jwtSecret) problems.push('falta EO_AUTH_JWT_SECRET');
  else if (jwtSecret.length < 32) problems.push('EO_AUTH_JWT_SECRET debe tener al menos 32 caracteres');
  else if (production && jwtSecret.includes('dev-only')) {
    problems.push('EO_AUTH_JWT_SECRET es el valor de desarrollo y EO_ENV=production');
  }

  const identityPostgresUrl = env.EO_IDENTITY_POSTGRES_URL ?? '';
  if (!identityPostgresUrl) problems.push('falta EO_IDENTITY_POSTGRES_URL');

  const gameServerInternalUrl = (env.EO_GAME_SERVER_INTERNAL_URL || 'http://localhost:8080').replace(/\/+$/, '');

  // Los valores por defecto y su porqué están en docs/specs/player.md §4.3.
  const rateLimits: AuthRateLimitConfig = {
    ip: policy(env, 'EO_AUTH_RATE_LIMIT', { perMinute: 10, burst: 5 }, problems),
    user: policy(env, 'EO_AUTH_RATE_LIMIT_USER', { perMinute: 20, burst: 10 }, problems),
    global: policy(env, 'EO_AUTH_RATE_LIMIT_GLOBAL', { perMinute: 120, burst: 30 }, problems),
    ticket: policy(env, 'EO_AUTH_TICKET_RATE_LIMIT', { perMinute: 1200, burst: 600 }, problems),
  };
  // Un tope global más estrecho que el cupo de una IP o de un usuario dejaría
  // esos cupos sin efecto: el global cortaría antes a todo el mundo.
  if (!wider(rateLimits.global, rateLimits.ip) || !wider(rateLimits.global, rateLimits.user)) {
    problems.push('EO_AUTH_RATE_LIMIT_GLOBAL_* debe ser al menos tan amplio como el cupo por IP y el cupo por usuario');
  }

  const config: AuthConfig = {
    production,
    jwtSecret,
    identityPostgresUrl,
    gameServerInternalUrl,
    rateLimits,
    proxy: {
      trustProxyHeaders: env.EO_TRUST_PROXY_HEADERS === 'true',
      trustedProxyCount: intIn(env, 'EO_TRUSTED_PROXY_COUNT', 1, 1, 10, problems),
    },
  };
  if (problems.length > 0) throw new ConfigError(problems);
  return config;
}
