/**
 * Configuración del servidor de Next.js para la identidad (ADR-018 §2.2).
 *
 * Ninguna de estas variables lleva el prefijo NEXT_PUBLIC_: nunca llegan al
 * navegador. Los errores se reportan todos juntos, como hace el Game Server.
 */
import 'server-only';

export interface AuthConfig {
  readonly production: boolean;
  /** Secreto HS256 compartido con el Game Server (EO_AUTH_JWT_SECRET). */
  readonly jwtSecret: string;
  /** Almacén de identidad propio de apps/web (EO_IDENTITY_POSTGRES_URL). */
  readonly identityPostgresUrl: string;
  /** Base HTTP del Game Server para la fundación interna. */
  readonly gameServerInternalUrl: string;
  readonly rateLimitPerMinute: number;
  readonly rateLimitBurst: number;
  /** Creer X-Forwarded-For. Falso por defecto: sin proxy delante se falsifica. */
  readonly trustProxyHeaders: boolean;
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

  const config: AuthConfig = {
    production,
    jwtSecret,
    identityPostgresUrl,
    gameServerInternalUrl,
    rateLimitPerMinute: intIn(env, 'EO_AUTH_RATE_LIMIT_PER_MINUTE', 10, 1, 100000, problems),
    rateLimitBurst: intIn(env, 'EO_AUTH_RATE_LIMIT_BURST', 5, 1, 10000, problems),
    trustProxyHeaders: env.EO_TRUST_PROXY_HEADERS === 'true',
  };
  if (problems.length > 0) throw new ConfigError(problems);
  return config;
}
