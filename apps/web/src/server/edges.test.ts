/**
 * Bordes de la identidad: configuración, límite de tasa, cliente de la
 * fundación y capa HTTP con su cookie.
 */
import { createHmac } from 'node:crypto';

import { describe, expect, it } from 'vitest';

import { loadRootDotEnv, parseDotEnvLine } from '../../identity/dotenv.mjs';
import type { AuthResult, AuthService } from './authService';
import { ConfigError, loadAuthConfig } from './config';
import { createGameServerClient } from './gameServer';
import { SESSION_COOKIE, handleCredentials, handleLogout, handleTicket, readSessionCookie, type HttpDeps } from './http';
import {
  AuthRateLimits,
  RateLimiter,
  TICKET_IP_POLICY,
  TICKET_SESSION_POLICY,
  clientIp,
  type AuthRateLimitOptions,
  type ProxyTrust,
} from './rateLimit';

const SECRET = 'un-secreto-de-pruebas-suficientemente-largo';

describe('loadAuthConfig', () => {
  const base = { EO_AUTH_JWT_SECRET: SECRET, EO_IDENTITY_POSTGRES_URL: 'postgres://x@localhost/empires_identity' };

  it('aplica los valores por defecto', () => {
    expect(loadAuthConfig(base)).toMatchObject({
      production: false,
      gameServerInternalUrl: 'http://localhost:8080',
      rateLimits: {
        ip: { perMinute: 10, burst: 5 },
        user: { perMinute: 20, burst: 10 },
        global: { perMinute: 120, burst: 30 },
        ticket: { perMinute: 1200, burst: 600 },
      },
      proxy: { trustProxyHeaders: false, trustedProxyCount: 1 },
    });
  });

  it('lee cada cupo y el número de proxies de su variable', () => {
    const config = loadAuthConfig({
      ...base,
      EO_AUTH_RATE_LIMIT_PER_MINUTE: '6',
      EO_AUTH_RATE_LIMIT_USER_BURST: '7',
      EO_AUTH_RATE_LIMIT_GLOBAL_PER_MINUTE: '600',
      EO_AUTH_TICKET_RATE_LIMIT_BURST: '50',
      EO_TRUST_PROXY_HEADERS: 'true',
      EO_TRUSTED_PROXY_COUNT: '2',
    });
    expect(config.rateLimits.ip.perMinute).toBe(6);
    expect(config.rateLimits.user.burst).toBe(7);
    expect(config.rateLimits.global.perMinute).toBe(600);
    expect(config.rateLimits.ticket.burst).toBe(50);
    expect(config.proxy).toEqual({ trustProxyHeaders: true, trustedProxyCount: 2 });
  });

  it('reporta todos los errores juntos', () => {
    try {
      loadAuthConfig({ EO_AUTH_JWT_SECRET: 'corto', EO_AUTH_RATE_LIMIT_BURST: '0' });
      expect.unreachable();
    } catch (err) {
      expect(err).toBeInstanceOf(ConfigError);
      expect((err as ConfigError).problems).toHaveLength(3);
    }
  });

  it('un tope global más estrecho que el cupo por IP o por usuario es un error', () => {
    expect(() => loadAuthConfig({ ...base, EO_AUTH_RATE_LIMIT_GLOBAL_PER_MINUTE: '5' })).toThrow(/GLOBAL/);
    expect(() => loadAuthConfig({ ...base, EO_AUTH_RATE_LIMIT_GLOBAL_BURST: '9' })).toThrow(/GLOBAL/);
    expect(() => loadAuthConfig({ ...base, EO_TRUSTED_PROXY_COUNT: '0' })).toThrow(/EO_TRUSTED_PROXY_COUNT/);
  });

  it('rechaza el secreto de desarrollo en producción', () => {
    expect(() =>
      loadAuthConfig({ ...base, EO_ENV: 'production', EO_AUTH_JWT_SECRET: `dev-only-${SECRET}` }),
    ).toThrow(/producción|production/);
  });

  it('el mensaje de error no incluye el valor del secreto', () => {
    try {
      loadAuthConfig({ ...base, EO_ENV: 'production', EO_AUTH_JWT_SECRET: `dev-only-${SECRET}` });
    } catch (err) {
      expect(String(err)).not.toContain(SECRET);
    }
  });
});

describe('dotenv', () => {
  it('interpreta líneas y nunca pisa el entorno', () => {
    expect(parseDotEnvLine('# comentario')).toBeNull();
    expect(parseDotEnvLine('A="b c"')).toEqual(['A', 'b c']);
    expect(parseDotEnvLine("A='b'")).toEqual(['A', 'b']);
    expect(parseDotEnvLine('=x')).toBeNull();
    const env: Record<string, string | undefined> = { EO_ENV: 'ya-definido' };
    loadRootDotEnv('/no/existe/en/ninguna/parte', env);
    expect(env).toEqual({ EO_ENV: 'ya-definido' });
  });
});

describe('RateLimiter', () => {
  it('deja pasar la ráfaga, corta y recupera al ritmo configurado', () => {
    const l = new RateLimiter(60, 3); // un token por segundo
    expect([1, 2, 3, 4].map(() => l.allow('a', 0))).toEqual([true, true, true, false]);
    expect(l.allow('b', 0)).toBe(true); // otra clave, otro cubo
    expect(l.allow('a', 999)).toBe(false);
    expect(l.allow('a', 1000)).toBe(true);
  });
});

describe('clientIp', () => {
  const trust = (trustedProxyCount = 1): ProxyTrust => ({ trustProxyHeaders: true, trustedProxyCount });
  const h = (xff?: string, real?: string) =>
    new Headers({ ...(xff !== undefined ? { 'x-forwarded-for': xff } : {}), ...(real ? { 'x-real-ip': real } : {}) });

  it('sin proxy de confianza no cree a ninguna cabecera', () => {
    expect(clientIp(h('203.0.113.7', '203.0.113.7'), { trustProxyHeaders: false, trustedProxyCount: 1 })).toBeNull();
  });

  it('toma el salto que añadió el último proxy, nunca el primero, que lo escribe el cliente', () => {
    // El cliente mandó «198.51.100.1» y el proxy añadió la dirección real.
    expect(clientIp(h('198.51.100.1, 203.0.113.7'), trust())).toBe('203.0.113.7');
    // Con dos proxies de confianza, el penúltimo salto es el del cliente.
    expect(clientIp(h('198.51.100.1, 203.0.113.7, 10.0.0.2'), trust(2))).toBe('203.0.113.7');
  });

  it('una lista más corta que los proxies declarados no se cree', () => {
    expect(clientIp(h('203.0.113.7'), trust(2))).toBeNull();
    expect(clientIp(h(' , '), trust())).toBeNull();
  });

  it('X-Real-IP sólo vale a falta de X-Forwarded-For', () => {
    expect(clientIp(h(undefined, '203.0.113.7'), trust())).toBe('203.0.113.7');
    expect(clientIp(h('203.0.113.9', '198.51.100.1'), trust())).toBe('203.0.113.9');
    expect(clientIp(new Headers(), trust())).toBeNull();
  });

  it('quita el puerto y unifica la caja de IPv6', () => {
    expect(clientIp(h('203.0.113.7:51234'), trust())).toBe('203.0.113.7');
    expect(clientIp(h('[2001:DB8::1]:443'), trust())).toBe('2001:db8::1');
    expect(clientIp(h('2001:DB8::1'), trust())).toBe('2001:db8::1');
  });
});

describe('createGameServerClient', () => {
  const fixed = { baseUrl: 'http://gs', secret: SECRET, nowMs: () => 1_760_000_000_000, randomUUID: () => 'jti-1' };

  function fakeFetch(status: number, body: unknown, seen: Request[] = []): typeof fetch {
    return (async (url: string, init: RequestInit) => {
      seen.push(new Request(url, init));
      return new Response(JSON.stringify(body), { status });
    }) as unknown as typeof fetch;
  }

  it('envía un token de fundación para ese jugador, con su nombre en el cuerpo', async () => {
    const seen: Request[] = [];
    const city = { id: 1, name: 'xpolis', centerX: 2, centerY: 3 };
    const c = createGameServerClient({ ...fixed, fetch: fakeFetch(201, { playerId: 'p', city }, seen) });
    expect(await c.found('p-1', 'x')).toEqual({ kind: 'founded', city });

    const req = seen[0]!;
    expect(req.url).toBe('http://gs/internal/players');
    expect(await req.json()).toEqual({ playerId: 'p-1', username: 'x' });
    const token = req.headers.get('authorization')!.replace('Bearer ', '');
    const [h, p, s] = token.split('.');
    expect(s).toBe(createHmac('sha256', SECRET).update(`${h}.${p}`).digest('base64url'));
    expect(JSON.parse(Buffer.from(p!, 'base64url').toString())).toMatchObject({
      sub: 'p-1',
      aud: 'game-server-internal',
      jti: 'jti-1',
    });
  });

  it('200 de un jugador que ya existía también es fundado', async () => {
    const city = { id: 1, name: 'x', centerX: 0, centerY: 0 };
    const c = createGameServerClient({ ...fixed, fetch: fakeFetch(200, { city }) });
    expect((await c.found('p', 'x')).kind).toBe('founded');
  });

  it.each([
    [409, 'USERNAME_TAKEN'],
    [409, 'NO_SITE_AVAILABLE'],
    [503, 'SITE_CONTENDED'],
    [400, 'INVALID_USERNAME'],
  ])('%i %s es un rechazo definitivo', async (status, code) => {
    const c = createGameServerClient({ ...fixed, fetch: fakeFetch(status, { code, message: 'm' }) });
    expect(await c.found('p', 'x')).toEqual({ kind: 'rejected', status, code, message: 'm' });
  });

  it.each([
    [500, 'INTERNAL_ERROR'],
    [401, 'UNAUTHORIZED'],
    [403, 'FORBIDDEN'],
    [503, 'INTERNAL_ERROR'],
  ])('%i %s deja la cuenta pendiente', async (status, code) => {
    const c = createGameServerClient({ ...fixed, fetch: fakeFetch(status, { code }) });
    expect((await c.found('p', 'x')).kind).toBe('unavailable');
  });

  it('sin red, indisponible', async () => {
    const c = createGameServerClient({
      ...fixed,
      fetch: (async () => {
        throw new TypeError('fetch failed');
      }) as unknown as typeof fetch,
    });
    expect((await c.found('p', 'x')).kind).toBe('unavailable');
  });
});

/** Cupos tan amplios que ningún test los roza, salvo el que se estrecha a propósito. */
const WIDE = { perMinute: 100000, burst: 10000 };
const NO_PROXY: ProxyTrust = { trustProxyHeaders: false, trustedProxyCount: 1 };

function limits(over: Partial<AuthRateLimitOptions> = {}): AuthRateLimits {
  return new AuthRateLimits({ trust: NO_PROXY, ip: WIDE, user: WIDE, global: WIDE, ticket: WIDE, ...over });
}

describe('capa HTTP', () => {
  const ok: AuthResult = {
    ok: true,
    status: 201,
    body: { playerId: 'p', ticket: 't', expiresInSeconds: 60 },
    sessionToken: 'tok',
  };

  function deps(result: AuthResult = ok, over: Partial<HttpDeps> = {}) {
    const calls: string[] = [];
    const warnings: Array<Record<string, unknown>> = [];
    const service = {
      register: async (u: string) => (calls.push(`register:${u}`), result),
      login: async (u: string) => (calls.push(`login:${u}`), result),
      ticket: async (t?: string) => (calls.push(`ticket:${t}`), result),
      logout: async (t?: string) => void calls.push(`logout:${t}`),
    } as unknown as AuthService;
    const d: HttpDeps = {
      service,
      limits: limits(),
      secureCookies: false,
      nowMs: () => 0,
      log: { info() {}, warn: (msg, fields) => void warnings.push({ msg, ...fields }), error() {} },
      ...over,
    };
    return { d, calls, warnings };
  }

  const post = (path: string, body?: unknown, headers: Record<string, string> = {}) =>
    new Request(`http://localhost:3000${path}`, {
      method: 'POST',
      headers: { host: 'localhost:3000', ...(body !== undefined ? { 'content-type': 'application/json' } : {}), ...headers },
      ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
    });

  const login = (d: HttpDeps, username: string, headers: Record<string, string> = {}) =>
    handleCredentials(post('/api/auth/login', { username, password: 'b' }, headers), d, 'login');

  it('el alta deja una cookie HttpOnly, SameSite=Strict y limitada a /api/auth', async () => {
    const { d } = deps();
    const res = await handleCredentials(post('/api/auth/register', { username: 'a', password: 'b' }), d, 'register');
    expect(res.status).toBe(201);
    const cookie = res.headers.get('set-cookie')!;
    expect(cookie).toContain(`${SESSION_COOKIE}=tok`);
    expect(cookie).toContain('HttpOnly');
    expect(cookie).toContain('SameSite=Strict');
    expect(cookie).toContain('Path=/api/auth');
    expect(cookie).not.toContain('Secure');
    expect(res.headers.get('cache-control')).toBe('no-store');
    expect(await res.json()).toEqual(ok.body);
  });

  it('en producción la cookie es Secure', async () => {
    const { d } = deps(ok, { secureCookies: true });
    const res = await handleCredentials(post('/api/auth/login', { username: 'a', password: 'b' }), d, 'login');
    expect(res.headers.get('set-cookie')).toContain('Secure');
  });

  it('el límite de tasa corta ANTES de leer el cuerpo y de llamar al servicio', async () => {
    const { d, calls } = deps(ok, { limits: limits({ global: { perMinute: 1, burst: 2 } }) });
    const codes = [];
    for (let i = 0; i < 3; i++) {
      codes.push((await handleCredentials(post('/api/auth/login', { username: 'a', password: 'b' }), d, 'login')).status);
    }
    expect(codes).toEqual([201, 201, 429]);
    expect(calls).toHaveLength(2);
  });

  it('un X-Forwarded-For falsificado no evita el límite por IP', async () => {
    const trust: ProxyTrust = { trustProxyHeaders: true, trustedProxyCount: 1 };
    const { d, warnings } = deps(ok, { limits: limits({ trust, ip: { perMinute: 1, burst: 2 } }) });
    // El atacante cambia el primer elemento en cada petición; el proxy añade
    // siempre su dirección real detrás.
    const codes = [];
    for (let i = 0; i < 3; i++) {
      codes.push((await login(d, `u${i}`, { 'x-forwarded-for': `198.51.100.${i}, 203.0.113.7` })).status);
    }
    expect(codes).toEqual([201, 201, 429]);
    expect(warnings.at(-1)).toMatchObject({ endpoint: 'login', bucket: 'ip', key: '203.0.113.7' });
    // Otra dirección real sigue teniendo su cupo.
    expect((await login(d, 'u9', { 'x-forwarded-for': '198.51.100.9, 203.0.113.8' })).status).toBe(201);
  });

  it('sin proxy de confianza, X-Forwarded-For se ignora y manda el tope global', async () => {
    const { d, warnings } = deps(ok, { limits: limits({ ip: { perMinute: 1, burst: 1 }, global: { perMinute: 1, burst: 2 } }) });
    const codes = [];
    for (let i = 0; i < 3; i++) codes.push((await login(d, `u${i}`, { 'x-forwarded-for': `203.0.113.${i}` })).status);
    expect(codes).toEqual([201, 201, 429]);
    expect(warnings.at(-1)).toMatchObject({ bucket: 'global' });
  });

  it('un usuario no puede agotar el cupo de otro, y las mayúsculas no abren otro cupo', async () => {
    const { d, calls, warnings } = deps(ok, { limits: limits({ user: { perMinute: 1, burst: 2 } }) });
    expect([(await login(d, 'alice')).status, (await login(d, 'Alice')).status, (await login(d, 'ALICE')).status]).toEqual([
      201, 201, 429,
    ]);
    expect(warnings.at(-1)).toMatchObject({ bucket: 'user', key: 'alice' });
    expect((await login(d, 'bob')).status).toBe(201);
    // El cupo por usuario se evalúa antes de llamar al servicio: scrypt no se gasta.
    expect(calls).toEqual(['login:alice', 'login:Alice', 'login:bob']);
  });

  it('el tope global corta aunque cambien la IP y el usuario', async () => {
    const trust: ProxyTrust = { trustProxyHeaders: true, trustedProxyCount: 1 };
    const { d, warnings } = deps(ok, { limits: limits({ trust, global: { perMinute: 1, burst: 3 } }) });
    const codes = [];
    for (let i = 0; i < 4; i++) codes.push((await login(d, `u${i}`, { 'x-forwarded-for': `203.0.113.${i}` })).status);
    expect(codes).toEqual([201, 201, 201, 429]);
    expect(warnings.at(-1)).toMatchObject({ bucket: 'global', key: 'global' });
  });

  it('el ticket y el cierre de sesión se limitan por sesión, con la clave en el log como hash', async () => {
    const { d, warnings } = deps(
      { ok: true, status: 200, body: ok.body },
      { limits: limits({ ticketSession: { perMinute: 1, burst: 2 } }) },
    );
    const cookie = { cookie: `${SESSION_COOKIE}=token-secreto-de-la-sesion` };
    const codes = [
      (await handleTicket(post('/api/auth/ticket', undefined, cookie), d)).status,
      (await handleLogout(post('/api/auth/logout', undefined, cookie), d)).status,
      (await handleTicket(post('/api/auth/ticket', undefined, cookie), d)).status,
    ];
    expect(codes).toEqual([200, 204, 429]);
    const warning = warnings.at(-1)!;
    expect(warning).toMatchObject({ endpoint: 'ticket', bucket: 'session' });
    expect(JSON.stringify(warning)).not.toContain('token-secreto');
    // Otra sesión tiene su propio cupo.
    expect((await handleTicket(post('/api/auth/ticket', undefined, { cookie: `${SESSION_COOKIE}=otra` }), d)).status).toBe(200);
  });

  it('el ticket se limita también por IP y por el tope global', async () => {
    const trust: ProxyTrust = { trustProxyHeaders: true, trustedProxyCount: 1 };
    const body: AuthResult = { ok: true, status: 200, body: ok.body };
    const porIp = deps(body, { limits: limits({ trust, ticketIp: { perMinute: 1, burst: 2 } }) });
    const ticket = (d: HttpDeps, session: string, ip: string) =>
      handleTicket(post('/api/auth/ticket', undefined, { cookie: `${SESSION_COOKIE}=${session}`, 'x-forwarded-for': ip }), d);
    expect([
      (await ticket(porIp.d, 's1', '203.0.113.7')).status,
      (await ticket(porIp.d, 's2', '203.0.113.7')).status,
      (await ticket(porIp.d, 's3', '203.0.113.7')).status,
    ]).toEqual([200, 200, 429]);
    expect(porIp.warnings.at(-1)).toMatchObject({ bucket: 'ip', key: '203.0.113.7' });

    const global = deps(body, { limits: limits({ trust, ticket: { perMinute: 1, burst: 2 } }) });
    expect([
      (await ticket(global.d, 's1', '203.0.113.1')).status,
      (await ticket(global.d, 's2', '203.0.113.2')).status,
      (await ticket(global.d, 's3', '203.0.113.3')).status,
    ]).toEqual([200, 200, 429]);
    expect(global.warnings.at(-1)).toMatchObject({ bucket: 'global' });
  });

  it('sin cookie, el ticket responde 401 y no gasta ningún cupo', async () => {
    const { d, calls } = deps(
      { ok: false, status: 401, code: 'UNAUTHORIZED', message: 'no hay sesión' },
      { limits: limits({ ticketSession: { perMinute: 1, burst: 1 }, ticket: { perMinute: 1, burst: 1 } }) },
    );
    for (let i = 0; i < 3; i++) expect((await handleTicket(post('/api/auth/ticket'), d)).status).toBe(401);
    expect(calls).toHaveLength(3);
  });

  it('las constantes del ticket son generosas con un cliente que reconecta', () => {
    // Siete tickets en el primer minuto de reconexión (frontend.md §3.3), con margen para varias pestañas.
    expect(TICKET_SESSION_POLICY.burst).toBeGreaterThanOrEqual(4 * 7);
    expect(TICKET_IP_POLICY.burst).toBeGreaterThanOrEqual(4 * TICKET_SESSION_POLICY.burst);
    expect(TICKET_IP_POLICY.perMinute).toBeGreaterThanOrEqual(4 * TICKET_SESSION_POLICY.perMinute);
  });

  it('cuerpo inválido, sin JSON o con campos que no son texto: 400 sin llamar al servicio', async () => {
    const { d, calls } = deps();
    const bad = [
      new Request('http://localhost:3000/api/auth/login', { method: 'POST', body: 'username=a&password=b', headers: { 'content-type': 'application/x-www-form-urlencoded' } }),
      // Un formulario de otro sitio puede mandar JSON válido como text/plain sin
      // preflight de CORS: se exige application/json.
      new Request('http://localhost:3000/api/auth/login', { method: 'POST', body: '{"username":"a","password":"b"}', headers: { 'content-type': 'text/plain' } }),
      post('/api/auth/login', { username: 1, password: 'b' }),
      post('/api/auth/login', 'texto'),
      post('/api/auth/login', { username: 'a', password: 'x'.repeat(5000) }),
    ];
    for (const req of bad) expect((await handleCredentials(req, d, 'login')).status).toBe(400);
    expect(calls).toHaveLength(0);
  });

  /** Cuerpo por trozos de 1 KiB, sin Content-Length, que anota cuántos llegaron a leerse. */
  function chunkedBody(totalBytes: number) {
    const pulled = { chunks: 0, cancelled: false };
    let sent = 0;
    const stream = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (sent >= totalBytes) return controller.close();
        const size = Math.min(1024, totalBytes - sent);
        controller.enqueue(new Uint8Array(size).fill(0x61));
        sent += size;
        pulled.chunks++;
      },
      cancel() {
        pulled.cancelled = true;
      },
      // Sin cola interna: cada `pull` es una lectura real de quien consume, no
      // un adelanto del propio stream.
    }, { highWaterMark: 0 });
    return { stream, pulled };
  }

  const chunkedPost = (stream: ReadableStream<Uint8Array>, headers: Record<string, string> = {}) =>
    new Request('http://localhost:3000/api/auth/login', {
      method: 'POST',
      headers: { host: 'localhost:3000', 'content-type': 'application/json', ...headers },
      body: stream,
      // undici exige declararlo para un cuerpo en streaming; el DOM no lo tipa.
      duplex: 'half',
    } as RequestInit);

  it('un cuerpo que declara más de 4096 bytes se rechaza sin leer ni un byte', async () => {
    const { d, calls } = deps();
    const { stream, pulled } = chunkedBody(8192);
    const res = await handleCredentials(chunkedPost(stream, { 'content-length': '8192' }), d, 'login');
    expect(res.status).toBe(400);
    expect(pulled.chunks).toBe(0);
    expect(calls).toHaveLength(0);
  });

  it('sin Content-Length, se deja de leer al pasar el tope y se descarta el resto', async () => {
    const { d, calls } = deps();
    const { stream, pulled } = chunkedBody(64 * 1024);
    const res = await handleCredentials(chunkedPost(stream), d, 'login');
    expect(res.status).toBe(400);
    // 4096 bytes son cuatro trozos; el quinto es el que se pasa. Nunca los 64.
    expect(pulled.chunks).toBeLessThanOrEqual(5);
    expect(pulled.cancelled).toBe(true);
    expect(calls).toHaveLength(0);
  });

  it('un cuerpo por trozos que cabe se lee entero y llega al servicio', async () => {
    const { d, calls } = deps();
    const body = new TextEncoder().encode(JSON.stringify({ username: 'a', password: 'b' }));
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(body.slice(0, 10));
        controller.enqueue(body.slice(10));
        controller.close();
      },
    });
    expect((await handleCredentials(chunkedPost(stream), d, 'login')).status).toBe(201);
    expect(calls).toEqual(['login:a']);
  });

  it('el tope es en bytes: 4096 caracteres multibyte no caben', async () => {
    const { d, calls } = deps();
    const res = await handleCredentials(post('/api/auth/login', { username: 'a', password: 'ñ'.repeat(2100) }), d, 'login');
    expect(res.status).toBe(400);
    expect(calls).toHaveLength(0);
  });

  it('una petición de otro origen se rechaza', async () => {
    const { d, calls } = deps();
    const res = await handleCredentials(
      post('/api/auth/login', { username: 'a', password: 'b' }, { origin: 'https://malicioso.example' }),
      d,
      'login',
    );
    expect(res.status).toBe(403);
    expect((await handleTicket(post('/api/auth/ticket', undefined, { origin: 'https://malicioso.example' }), d)).status).toBe(403);
    expect(calls).toHaveLength(0);
  });

  it('el ticket se pide con la cookie y no la renueva', async () => {
    const { d, calls } = deps({ ok: true, status: 200, body: ok.body });
    const res = await handleTicket(post('/api/auth/ticket', undefined, { cookie: `otra=1; ${SESSION_COOKIE}=abc=d` }), d);
    expect(res.status).toBe(200);
    expect(res.headers.get('set-cookie')).toBeNull();
    expect(calls).toEqual(['ticket:abc=d']);
  });

  it('un error del servicio se traduce, con su código', async () => {
    const { d } = deps({ ok: false, status: 401, code: 'UNAUTHORIZED', message: 'no' });
    const res = await handleTicket(post('/api/auth/ticket'), d);
    expect(res.status).toBe(401);
    expect(await res.json()).toEqual({ code: 'UNAUTHORIZED', message: 'no' });
  });

  // INV-SEC-006: el texto de un error de infraestructura —que puede llevar la
  // cadena de conexión— va al log del servidor, nunca a la respuesta.
  it('un error interno responde 500 con un mensaje fijo, sin el texto del error', async () => {
    const conexion = ['postgres://identidad:', 'clave-del-almacen', '@db.interna:5432/empires_identity'].join('');
    const lanza = async () => {
      throw new Error(`connect ECONNREFUSED ${conexion}`);
    };
    const { d } = deps();
    const service = { register: lanza, login: lanza, ticket: lanza, logout: lanza } as unknown as AuthService;
    const d2 = { ...d, service };
    for (const res of [
      await handleCredentials(post('/api/auth/login', { username: 'a', password: 'b' }), d2, 'login'),
      await handleTicket(post('/api/auth/ticket'), d2),
    ]) {
      expect(res.status).toBe(500);
      const texto = await res.text();
      expect(texto).not.toContain('clave-del-almacen');
      expect(texto).not.toContain('db.interna');
      expect(JSON.parse(texto)).toEqual({ code: 'INTERNAL_ERROR', message: 'error interno' });
    }
  });

  it('cerrar sesión borra la sesión y caduca la cookie', async () => {
    const { d, calls } = deps();
    const res = await handleLogout(post('/api/auth/logout', undefined, { cookie: `${SESSION_COOKIE}=abc` }), d);
    expect(res.status).toBe(204);
    expect(res.headers.get('set-cookie')).toContain('Max-Age=0');
    expect(calls).toEqual(['logout:abc']);
    expect(readSessionCookie(post('/x'))).toBeUndefined();
  });
});
