import { describe, expect, it } from 'vitest';

import { AuthThrottle, WebSession, sessionCookie } from './auth';
import { botEnvFromDotenv, rosterFor } from './main';
import { DEFAULT_THINK_MS, OptionsError, botPassword, parseDuration, parseOptions } from './options';
import { createRng, seedFor } from './random';

const parse = (argv: string[], env: Record<string, string> = {}) => parseOptions(argv, env, 1_000, '/repo');

describe('las opciones', () => {
  it('por defecto: tres constructores y un saqueador, sin límite, contra el desarrollo local', () => {
    expect(parse([])).toEqual({
      web: 'http://localhost:3001',
      server: 'ws://localhost:8080/ws',
      constructores: 3,
      saqueadores: 1,
      duracionMs: null,
      semilla: 1_000,
      contraHumanos: false,
      comercio: true,
      conHumanos: false,
      sufijo: null,
      thinkMs: DEFAULT_THINK_MS,
      runsDir: '/repo/bots/runs',
    });
  });

  it('lee cada opción, también tras un «--» de pnpm', () => {
    const o = parse([
      '--',
      '--web',
      'http://x:3001/',
      '--server',
      'ws://y/ws',
      '--constructores',
      '2',
      '--saqueadores',
      '0',
      '--duracion',
      '30m',
      '--semilla',
      '42',
      '--contra-humanos',
      '--sin-comercio',
      '--con-humanos',
    ]);
    expect(o).toMatchObject({
      web: 'http://x:3001',
      server: 'ws://y/ws',
      constructores: 2,
      saqueadores: 0,
      duracionMs: 1_800_000,
      semilla: 42,
      contraHumanos: true,
      comercio: false,
      conHumanos: true,
    });
  });

  it('las duraciones', () => {
    expect(parseDuration('90s')).toBe(90_000);
    expect(parseDuration('2h')).toBe(7_200_000);
    expect(parseDuration('1500ms')).toBe(1_500);
    expect(parseDuration('45')).toBe(45_000);
    expect(() => parseDuration('dos horas')).toThrow(OptionsError);
  });

  it('rechaza lo que no cabe: ningún bot, más de veinte, una opción desconocida o un ritmo fuera de rango', () => {
    expect(() => parse(['--constructores', '0', '--saqueadores', '0'])).toThrow(/ningún bot/);
    expect(() => parse(['--constructores', '15', '--saqueadores', '6'])).toThrow(/como mucho 20/);
    expect(() => parse(['--rapido'])).toThrow(/desconocida/);
    expect(() => parse([], { EO_BOT_THINK_MS: '100' })).toThrow(/EO_BOT_THINK_MS/);
    expect(parse([], { EO_BOT_THINK_MS: '5000' }).thinkMs).toBe(5_000);
  });

  it('sin EO_BOT_PASSWORD no arranca: no hay contraseña por defecto', () => {
    expect(() => botPassword({})).toThrow(/EO_BOT_PASSWORD/);
    expect(() => botPassword({ EO_BOT_PASSWORD: 'corta' })).toThrow(/12/);
    expect(botPassword({ EO_BOT_PASSWORD: 'una-contraseña-larga' })).toBe('una-contraseña-larga');
  });

  it('del .env sólo se leen las EO_BOT_*', () => {
    const text = 'EO_POSTGRES_URL=postgres://x\nEO_BOT_PASSWORD="entre-comillas-123"\n# EO_BOT_X=no\nEO_BOT_THINK_MS=3000\n';
    expect(botEnvFromDotenv(text)).toEqual({ EO_BOT_PASSWORD: 'entre-comillas-123', EO_BOT_THINK_MS: '3000' });
  });

  it('los nombres se numeran por perfil y las civilizaciones van por turno', () => {
    expect(rosterFor(3, 2)).toEqual([
      { name: 'bot_constructor_1', profile: 'constructor', civilization: 'ROMAN' },
      { name: 'bot_constructor_2', profile: 'constructor', civilization: 'BYZANTINE' },
      { name: 'bot_constructor_3', profile: 'constructor', civilization: 'PERSIAN' },
      { name: 'bot_saqueador_1', profile: 'saqueador', civilization: 'NORSE' },
      { name: 'bot_saqueador_2', profile: 'saqueador', civilization: 'ROMAN' },
    ]);
  });

  it('--sufijo da cuentas nuevas, con nombres que la web admite', () => {
    expect(parse(['--sufijo', 'a1']).sufijo).toBe('a1');
    expect(() => parse(['--sufijo', 'A-1'])).toThrow(OptionsError);
    expect(() => parse(['--sufijo', 'abcdef'])).toThrow(OptionsError);
    const roster = rosterFor(20, 0, 'abcde');
    expect(roster[0]?.name).toBe('bot_constructor_1_abcde');
    expect(Math.max(...roster.map((r) => r.name.length))).toBeLessThanOrEqual(24);
    expect(rosterFor(0, 1, 'a1')).toEqual([{ name: 'bot_saqueador_1_a1', profile: 'saqueador', civilization: 'ROMAN' }]);
  });
});

describe('el aleatorio con semilla', () => {
  it('la misma semilla da la misma serie; cada bot tiene la suya', () => {
    const a = createRng(5);
    const b = createRng(5);
    expect([a.next(), a.int(10), a.next()]).toEqual([b.next(), b.int(10), b.next()]);
    expect(seedFor(5, 'bot_constructor_1')).toBe(seedFor(5, 'bot_constructor_1'));
    expect(seedFor(5, 'bot_constructor_1')).not.toBe(seedFor(5, 'bot_constructor_2'));
  });
});

describe('las altas escalonadas frente al límite de la web', () => {
  it('cinco seguidas pasan; la sexta espera lo que pide el cupo de diez por minuto', () => {
    let now = 0;
    const t = new AuthThrottle(10, 5, () => now);
    expect([t.take(), t.take(), t.take(), t.take(), t.take()]).toEqual([0, 0, 0, 0, 0]);
    expect(t.take()).toBe(6_000);
    now = 12_000;
    expect(t.take()).toBe(0);
  });
});

describe('la sesión web', () => {
  type Call = { url: string; method: string; headers: Record<string, string>; body?: string };
  const fakeFetch = (routes: Record<string, { status: number; body: unknown; cookie?: string }>, calls: Call[]) =>
    async (url: string, init: { method: string; headers: Record<string, string>; body?: string }) => {
      calls.push({ url, ...init });
      const path = new URL(url).pathname;
      const r = routes[path] ?? { status: 404, body: {} };
      return {
        status: r.status,
        headers: { get: (n: string) => (n === 'set-cookie' ? (r.cookie ?? null) : null) },
        json: async () => r.body,
      };
    };

  it('entra si la cuenta existe y guarda la cookie para los tickets siguientes', async () => {
    const calls: Call[] = [];
    const s = new WebSession('http://w', 'bot_constructor_1', 'clave-larga-123', 'ROMAN', fakeFetch({
      '/api/auth/login': { status: 200, body: { playerId: 'p1', ticket: 't1', city: { id: 1, name: 'bot_constructor_1polis', centerX: 2, centerY: 3 } }, cookie: 'eo_session=abc; Path=/; HttpOnly' },
      '/api/auth/ticket': { status: 200, body: { ticket: 't2' } },
    }, calls));
    expect(await s.open()).toEqual({ playerId: 'p1', city: { id: 1, name: 'bot_constructor_1polis', centerX: 2, centerY: 3 }, created: false });
    expect(await s.ticket()).toBe('t1');
    expect(await s.ticket()).toBe('t2');
    expect(calls[1]?.headers.cookie).toBe('eo_session=abc');
    expect(calls.map((c) => new URL(c.url).pathname)).toEqual(['/api/auth/login', '/api/auth/ticket']);
  });

  it('si no existe (401), se da de alta con su civilización', async () => {
    const calls: Call[] = [];
    const s = new WebSession('http://w', 'bot_saqueador_1', 'clave-larga-123', 'NORSE', fakeFetch({
      '/api/auth/login': { status: 401, body: { code: 'UNAUTHORIZED' } },
      '/api/auth/register': { status: 201, body: { playerId: 'p2', ticket: 't1' }, cookie: 'eo_session=xyz' },
    }, calls));
    expect((await s.open()).created).toBe(true);
    expect(JSON.parse(calls[1]?.body ?? '{}')).toMatchObject({ username: 'bot_saqueador_1', civilization: 'NORSE' });
  });

  it('un 429 se dice como límite, sin la contraseña en el mensaje', async () => {
    const s = new WebSession('http://w', 'bot_x', 'clave-larga-123', 'ROMAN', fakeFetch({
      '/api/auth/login': { status: 429, body: { code: 'RATE_LIMITED' } },
    }, []));
    await expect(s.open()).rejects.toMatchObject({ code: 'RATE_LIMITED', status: 429 });
    await expect(s.open()).rejects.not.toThrow(/clave-larga/);
  });

  it('sólo la cookie de sesión, sin atributos', () => {
    expect(sessionCookie('eo_session=abc; Path=/; HttpOnly')).toBe('eo_session=abc');
    expect(sessionCookie('otra=1')).toBeNull();
    expect(sessionCookie(null)).toBeNull();
  });
});
