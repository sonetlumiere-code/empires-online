/**
 * Límite de tasa del alta y del login (ADR-018 §2.6): un token bucket por clave.
 *
 * Era `httpapi.IPLimiter` del Game Server; ahora protege el proceso que calcula
 * scrypt, no el del tick. Es POR PROCESO: con varias instancias de Next.js cada
 * una lleva su cuenta (ADR-018 §4.2).
 */
import 'server-only';

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

/**
 * Clave de origen de una petición. Sin proxy de confianza delante, X-Forwarded-For
 * la escribe el cliente y no vale: todas las peticiones comparten la clave
 * `direct`, que sigue acotando el coste total de scrypt.
 */
export function clientKey(headers: Headers, trustProxyHeaders: boolean): string {
  if (trustProxyHeaders) {
    const first = headers.get('x-forwarded-for')?.split(',')[0]?.trim();
    if (first) return `ip:${first}`;
  }
  return 'direct';
}
