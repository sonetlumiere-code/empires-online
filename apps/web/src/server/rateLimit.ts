/**
 * Límite de tasa de `/api/auth/*` (ADR-018 §2.6): cubos de fichas por clave.
 *
 * Era `httpapi.IPLimiter` del Game Server; ahora protege el proceso que calcula
 * scrypt, no el del tick. Es POR PROCESO: con varias instancias de Next.js cada
 * una lleva su cuenta (ADR-018 §4.2).
 *
 * El alta y el login llevan tres cubos, porque ninguno basta solo:
 *
 *   - Por IP, sólo cuando se conoce (detrás de un proxy de confianza): frena a
 *     un origen sin molestar a los demás.
 *   - Por nombre de usuario, siempre: es lo que separa UNA contraseña de la
 *     fuerza bruta aunque el atacante reparta las peticiones entre muchas
 *     direcciones o la IP no se conozca.
 *   - Global, siempre: el tope absoluto del coste de scrypt del proceso. Sin
 *     proxy es lo único que acota a un solo cliente; es más amplio que el de una
 *     IP porque lo comparten todos los jugadores.
 *
 * El ticket y el cierre de sesión no calculan scrypt pero sí consultan la base:
 * llevan un cubo por sesión, otro por IP si se conoce y un tope global propio.
 * Una petición sin cookie no consume nada: responde 401 sin tocar la base, y
 * ese 401 es lo que devuelve al cliente al formulario de entrada.
 *
 * Sobre el olvido de claves (`maxKeys`): las claves que un atacante puede
 * inventar —nombres de usuario, cookies falsas— entran como mucho al ritmo del
 * tope global, así que cuando la más antigua se olvida ya hace minutos que
 * habría recuperado su cubo entero; las IP no se inventan.
 */
import 'server-only';

import { createHash } from 'node:crypto';

export class RateLimiter {
  private readonly buckets = new Map<string, { tokens: number; at: number }>();
  private readonly perMs: number;

  constructor(
    perMinute: number,
    private readonly burst: number,
    private readonly maxKeys = 10_000,
  ) {
    this.perMs = perMinute / 60_000;
  }

  /** Consume un token de `key`; false si no quedaba ninguno. */
  allow(key: string, nowMs: number): boolean {
    const b = this.buckets.get(key) ?? { tokens: this.burst, at: nowMs };
    b.tokens = Math.min(this.burst, b.tokens + Math.max(0, nowMs - b.at) * this.perMs);
    b.at = nowMs;
    const ok = b.tokens >= 1;
    if (ok) b.tokens -= 1;
    this.buckets.delete(key);
    this.buckets.set(key, b);
    // Acotado: con demasiadas claves se olvidan las más antiguas, que son las
    // que ya habrían recuperado su cubo entero.
    if (this.buckets.size > this.maxKeys) {
      const oldest = this.buckets.keys().next().value;
      if (oldest !== undefined) this.buckets.delete(oldest);
    }
    return ok;
  }
}

/** Recarga por minuto y capacidad de un cubo. */
export interface RateLimitPolicy {
  readonly perMinute: number;
  readonly burst: number;
}

export interface ProxyTrust {
  /** Creer las cabeceras del proxy (EO_TRUST_PROXY_HEADERS). Falso por defecto: sin proxy delante se falsifican. */
  readonly trustProxyHeaders: boolean;
  /** Cuántos proxies de confianza añaden su salto a X-Forwarded-For (EO_TRUSTED_PROXY_COUNT). */
  readonly trustedProxyCount: number;
}

/**
 * IP del cliente, o null si no se puede saber.
 *
 * `X-Forwarded-For` es una lista `cliente, proxy1, proxy2, …` a la que cada
 * proxy AÑADE la dirección de quien le habló. El primer elemento lo escribe
 * quien quiera, el atacante incluido; el único fiable es el que añadió el último
 * proxy de confianza: el N-ésimo por la derecha, con N el número de proxies de
 * confianza. Si la lista es más corta, el proxy no añadió nada y no se cree
 * ninguno.
 *
 * `X-Real-IP` sólo se usa a falta de `X-Forwarded-For`: nginx la fija a
 * `$remote_addr` y la sobrescribe, pero un proxy que no la toque dejaría pasar
 * la que mande el cliente, y por eso nunca prevalece sobre la lista.
 */
export function clientIp(headers: Headers, trust: ProxyTrust): string | null {
  if (!trust.trustProxyHeaders) return null;
  const forwarded = headers.get('x-forwarded-for');
  if (forwarded !== null) {
    const hops = forwarded
      .split(',')
      .map((hop) => hop.trim())
      .filter((hop) => hop.length > 0);
    const hop = hops[hops.length - trust.trustedProxyCount];
    return hop === undefined ? null : normalizeIp(hop);
  }
  const real = headers.get('x-real-ip')?.trim();
  return real ? normalizeIp(real) : null;
}

/** Quita el puerto que algunos proxies añaden (`203.0.113.7:51234`, `[2001:db8::1]:443`) y unifica la caja. */
function normalizeIp(raw: string): string {
  const bracketed = /^\[([^\]]+)\](?::\d+)?$/.exec(raw);
  if (bracketed) return bracketed[1]!.toLowerCase();
  const v4WithPort = /^(\d{1,3}(?:\.\d{1,3}){3}):\d+$/.exec(raw);
  if (v4WithPort) return v4WithPort[1]!;
  return raw.toLowerCase();
}

/**
 * Ticket y cierre de sesión por sesión. Constante, no variable de entorno: la
 * fija la política de reconexión del cliente, que también está en código
 * (frontend.md §3.3). Un cliente correcto pide como mucho siete tickets en su
 * primer minuto de reconexión (0,5 s, 1 s, 2 s, 4 s, 8 s, 16 s, 30 s) y dos por
 * minuto después; la ráfaga de 30 deja sitio a varias pestañas a la vez.
 */
export const TICKET_SESSION_POLICY: RateLimitPolicy = { perMinute: 60, burst: 30 };

/**
 * Ticket y cierre de sesión por IP, cuando se conoce: cuatro veces el cupo de
 * una sesión, porque detrás de una misma dirección (una red doméstica, un aula)
 * suele haber varios jugadores. Es lo que corta una lluvia de cookies inventadas
 * desde un origen sin que el tope global tenga que hacerlo.
 */
export const TICKET_IP_POLICY: RateLimitPolicy = { perMinute: 240, burst: 120 };

/** Qué cubo rechazó la petición y con qué clave, para el log. */
export interface Denied {
  readonly bucket: 'ip' | 'user' | 'global' | 'session';
  readonly key: string;
}

export interface AuthRateLimitOptions {
  readonly trust: ProxyTrust;
  /** Alta y login por IP (EO_AUTH_RATE_LIMIT_PER_MINUTE / EO_AUTH_RATE_LIMIT_BURST). */
  readonly ip: RateLimitPolicy;
  /** Alta y login por nombre de usuario (EO_AUTH_RATE_LIMIT_USER_PER_MINUTE / EO_AUTH_RATE_LIMIT_USER_BURST). */
  readonly user: RateLimitPolicy;
  /** Alta y login de todo el proceso (EO_AUTH_RATE_LIMIT_GLOBAL_PER_MINUTE / EO_AUTH_RATE_LIMIT_GLOBAL_BURST). */
  readonly global: RateLimitPolicy;
  /** Ticket y cierre de sesión de todo el proceso (EO_AUTH_TICKET_RATE_LIMIT_PER_MINUTE / EO_AUTH_TICKET_RATE_LIMIT_BURST). */
  readonly ticket: RateLimitPolicy;
  /** Sólo para tests: por defecto, las constantes de arriba. */
  readonly ticketSession?: RateLimitPolicy;
  readonly ticketIp?: RateLimitPolicy;
}

const limiter = (p: RateLimitPolicy) => new RateLimiter(p.perMinute, p.burst);

export class AuthRateLimits {
  private readonly ip: RateLimiter;
  private readonly user: RateLimiter;
  private readonly global: RateLimiter;
  private readonly session: RateLimiter;
  private readonly sessionIp: RateLimiter;
  private readonly ticket: RateLimiter;

  constructor(private readonly opts: AuthRateLimitOptions) {
    this.ip = limiter(opts.ip);
    this.user = limiter(opts.user);
    this.global = limiter(opts.global);
    this.session = limiter(opts.ticketSession ?? TICKET_SESSION_POLICY);
    this.sessionIp = limiter(opts.ticketIp ?? TICKET_IP_POLICY);
    this.ticket = limiter(opts.ticket);
  }

  /** Alta y login, ANTES de leer el cuerpo: la IP si se conoce y el tope global. */
  admitCredentials(headers: Headers, nowMs: number): Denied | null {
    const ip = clientIp(headers, this.opts.trust);
    if (ip !== null && !this.ip.allow(ip, nowMs)) return { bucket: 'ip', key: ip };
    if (!this.global.allow('global', nowMs)) return { bucket: 'global', key: 'global' };
    return null;
  }

  /**
   * Alta y login, con el cuerpo leído y ANTES de scrypt: el nombre de usuario sin
   * distinguir mayúsculas, para que `Alice` y `alice` gasten el mismo cupo.
   */
  admitUser(username: string, nowMs: number): Denied | null {
    const key = username.trim().toLowerCase();
    return this.user.allow(key, nowMs) ? null : { bucket: 'user', key };
  }

  /**
   * Ticket y cierre de sesión: por sesión, por IP si se conoce y el tope global.
   * Sin cookie no se consume nada: la respuesta es un 401 que no toca la base.
   */
  admitSession(headers: Headers, sessionToken: string | undefined, nowMs: number): Denied | null {
    if (!sessionToken) return null;
    const key = sessionKey(sessionToken);
    if (!this.session.allow(key, nowMs)) return { bucket: 'session', key };
    const ip = clientIp(headers, this.opts.trust);
    if (ip !== null && !this.sessionIp.allow(ip, nowMs)) return { bucket: 'ip', key: ip };
    if (!this.ticket.allow('global', nowMs)) return { bucket: 'global', key: 'global' };
    return null;
  }
}

/**
 * La clave de una sesión nunca es su token: acaba en el log del 429. Su SHA-256
 * truncado basta para distinguirla y no sirve para entrar (INV-PLAYER-007).
 */
function sessionKey(token: string): string {
  return `session:${createHash('sha256').update(token).digest('hex').slice(0, 16)}`;
}
