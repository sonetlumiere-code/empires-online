/**
 * El alta, la entrada y los tickets de un bot por la web pública (SPEC-BOTS §4.2,
 * RN-BOT-001): las mismas rutas que el navegador, con la cookie `eo_session`
 * guardada en memoria en vez de en el navegador.
 *
 * Ni la contraseña, ni la cookie ni los tickets salen de este módulo hacia el
 * registro (RN-BOT-006): quien llama sólo ve el `playerId` y la ciudad.
 */

export interface CityInfo {
  readonly id: number;
  readonly name: string;
  readonly centerX: number;
  readonly centerY: number;
}

export interface Account {
  readonly playerId: string;
  readonly city: CityInfo | null;
  /** `true` si esta ejecución la dio de alta; `false` si ya existía. */
  readonly created: boolean;
}

export class WebAuthError extends Error {
  override readonly name = 'WebAuthError';
  constructor(
    readonly code: string,
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

type FetchLike = (url: string, init: { method: string; headers: Record<string, string>; body?: string }) => Promise<{
  status: number;
  headers: { get(name: string): string | null };
  json(): Promise<unknown>;
}>;

/**
 * El cupo de altas y entradas de la web: 10 por minuto con ráfaga de 5 por IP
 * (`EO_AUTH_RATE_LIMIT_PER_MINUTE`, `_BURST`). El proceso lo respeta escalonando
 * en vez de chocar con el 429 (SPEC-BOTS §4.4).
 */
export class AuthThrottle {
  private tokens: number;
  private lastMs: number;

  constructor(
    private readonly perMinute = 10,
    private readonly burst = 5,
    private readonly now: () => number = Date.now,
  ) {
    this.tokens = burst;
    this.lastMs = now();
  }

  /** Cuánto hay que esperar antes del siguiente intento; 0 si se puede ya. Consume el turno. */
  take(): number {
    const now = this.now();
    this.tokens = Math.min(this.burst, this.tokens + ((now - this.lastMs) * this.perMinute) / 60_000);
    this.lastMs = now;
    if (this.tokens >= 1) {
      this.tokens -= 1;
      return 0;
    }
    const waitMs = Math.ceil(((1 - this.tokens) * 60_000) / this.perMinute);
    this.tokens -= 1;
    return waitMs;
  }
}

/** Sólo `nombre=valor` de un Set-Cookie: lo que el navegador devolvería. */
export function sessionCookie(setCookie: string | null): string | null {
  if (!setCookie) return null;
  const first = setCookie.split(';')[0]?.trim() ?? '';
  return first.startsWith('eo_session=') ? first : null;
}

export class WebSession {
  private cookie: string | null = null;
  private firstTicket: string | null = null;

  constructor(
    private readonly web: string,
    private readonly username: string,
    private readonly password: string,
    private readonly civilization: string,
    private readonly fetchImpl: FetchLike = fetch as unknown as FetchLike,
  ) {}

  /** Entra o, si la cuenta no existe, la da de alta. */
  async open(): Promise<Account> {
    const body = JSON.stringify({ username: this.username, password: this.password, civilization: this.civilization });
    const login = await this.post('/api/auth/login', body);
    if (login.status === 200) return this.accept(login, false);
    if (login.status !== 401 && login.status !== 404) throw this.fail(login, 'login');

    const register = await this.post('/api/auth/register', body);
    if (register.status === 200 || register.status === 201) return this.accept(register, true);
    throw this.fail(register, 'alta');
  }

  /** Un ticket fresco para cada (re)conexión: el primero, el que trajo la entrada. */
  async ticket(): Promise<string> {
    if (this.firstTicket) {
      const t = this.firstTicket;
      this.firstTicket = null;
      return t;
    }
    if (!this.cookie) throw new WebAuthError('UNAUTHORIZED', 401, 'sin sesión web');
    const r = await this.fetchImpl(`${this.web}/api/auth/ticket`, { method: 'POST', headers: { cookie: this.cookie } });
    const payload = (await r.json().catch(() => null)) as { ticket?: string; code?: string } | null;
    if (r.status !== 200 || !payload?.ticket) {
      throw new WebAuthError(payload?.code ?? 'INTERNAL_ERROR', r.status, `la web no dio un ticket (${r.status})`);
    }
    return payload.ticket;
  }

  /** Vuelve a entrar con la contraseña: cuando la cookie caducó (SPEC-BOTS §5). */
  async relogin(): Promise<void> {
    const body = JSON.stringify({ username: this.username, password: this.password });
    const login = await this.post('/api/auth/login', body);
    if (login.status !== 200) throw this.fail(login, 'login');
    await this.accept(login, false);
  }

  private post(path: string, body: string) {
    return this.fetchImpl(`${this.web}${path}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body,
    });
  }

  private async accept(
    r: Awaited<ReturnType<FetchLike>>,
    created: boolean,
  ): Promise<Account> {
    const payload = (await r.json().catch(() => null)) as {
      playerId?: string;
      ticket?: string;
      city?: CityInfo;
    } | null;
    const cookie = sessionCookie(r.headers.get('set-cookie'));
    if (!payload?.playerId || !payload.ticket || !cookie) {
      throw new WebAuthError('INTERNAL_ERROR', r.status, 'la web no abrió la sesión (falta el ticket o la cookie)');
    }
    this.cookie = cookie;
    this.firstTicket = payload.ticket;
    return { playerId: payload.playerId, city: payload.city ?? null, created };
  }

  private fail(r: Awaited<ReturnType<FetchLike>>, what: string): WebAuthError {
    const retry = r.headers.get('retry-after');
    const err = new WebAuthError(
      r.status === 429 ? 'RATE_LIMITED' : 'AUTH_FAILED',
      r.status,
      `${what} de ${this.username} rechazado (${r.status})${retry ? `, reintentar en ${retry} s` : ''}`,
    );
    return err;
  }
}
