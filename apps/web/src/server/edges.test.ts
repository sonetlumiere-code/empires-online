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
import { RateLimiter, clientKey } from './rateLimit';

const SECRET = 'un-secreto-de-pruebas-suficientemente-largo';

describe('loadAuthConfig', () => {
  const base = { EO_AUTH_JWT_SECRET: SECRET, EO_IDENTITY_POSTGRES_URL: 'postgres://x@localhost/empires_identity' };

  it('aplica los valores por defecto', () => {
    expect(loadAuthConfig(base)).toMatchObject({
      production: false,
      gameServerInternalUrl: 'http://localhost:8080',
      rateLimitPerMinute: 10,
      rateLimitBurst: 5,
      trustProxyHeaders: false,
    });
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

  it('sólo cree a X-Forwarded-For si se le dice', () => {
    const h = new Headers({ 'x-forwarded-for': '203.0.113.7, 10.0.0.1' });
    expect(clientKey(h, false)).toBe('direct');
    expect(clientKey(h, true)).toBe('ip:203.0.113.7');
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

describe('capa HTTP', () => {
  const ok: AuthResult = {
    ok: true,
    status: 201,
    body: { playerId: 'p', ticket: 't', expiresInSeconds: 60 },
    sessionToken: 'tok',
  };

  function deps(result: AuthResult = ok, over: Partial<HttpDeps> = {}) {
    const calls: string[] = [];
    const service = {
      register: async (u: string) => (calls.push(`register:${u}`), result),
      login: async (u: string) => (calls.push(`login:${u}`), result),
      ticket: async (t?: string) => (calls.push(`ticket:${t}`), result),
      logout: async (t?: string) => void calls.push(`logout:${t}`),
    } as unknown as AuthService;
    const d: HttpDeps = {
      service,
      limiter: new RateLimiter(10, 5),
      trustProxyHeaders: false,
      secureCookies: false,
      nowMs: () => 0,
      log: { info() {}, warn() {}, error() {} },
      ...over,
    };
    return { d, calls };
  }

  const post = (path: string, body?: unknown, headers: Record<string, string> = {}) =>
    new Request(`http://localhost:3000${path}`, {
      method: 'POST',
      headers: { host: 'localhost:3000', ...(body !== undefined ? { 'content-type': 'application/json' } : {}), ...headers },
      ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
    });

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
    const { d, calls } = deps(ok, { limiter: new RateLimiter(1, 2) });
    const codes = [];
    for (let i = 0; i < 3; i++) {
      codes.push((await handleCredentials(post('/api/auth/login', { username: 'a', password: 'b' }), d, 'login')).status);
    }
    expect(codes).toEqual([201, 201, 429]);
    expect(calls).toHaveLength(2);
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

  it('cerrar sesión borra la sesión y caduca la cookie', async () => {
    const { d, calls } = deps();
    const res = await handleLogout(post('/api/auth/logout', undefined, { cookie: `${SESSION_COOKIE}=abc` }), d);
    expect(res.status).toBe(204);
    expect(res.headers.get('set-cookie')).toContain('Max-Age=0');
    expect(calls).toEqual(['logout:abc']);
    expect(readSessionCookie(post('/x'))).toBeUndefined();
  });
});
