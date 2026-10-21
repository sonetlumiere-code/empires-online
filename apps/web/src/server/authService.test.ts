import { createHmac } from 'node:crypto';

import { beforeEach, describe, expect, it } from 'vitest';

import { AuthService, SESSION_TTL_MS, hashSessionToken, type Logger } from './authService';
import type { FoundResult, GameServerClient } from './gameServer';
import type { Account, IdentityStore } from './store';

const SECRET = 'secreto-de-los-tests-del-servicio-de-identidad';
const T0 = 1_760_000_000_000;

/** Almacén en memoria con la misma semántica que PgIdentityStore. */
class MemoryStore implements IdentityStore {
  accounts = new Map<string, Account>();
  sessions = new Map<string, { accountId: string; expiresAt: Date }>();

  async createAccount(a: { id: string; username: string; passwordHash: string }) {
    if ([...this.accounts.values()].some((x) => x.username === a.username)) return 'taken' as const;
    this.accounts.set(a.id, { ...a, status: 'PENDING' });
    return 'created' as const;
  }
  async findAccountByUsername(username: string) {
    return [...this.accounts.values()].find((a) => a.username === username) ?? null;
  }
  async activateAccount(id: string) {
    const a = this.accounts.get(id);
    if (a?.status === 'PENDING') this.accounts.set(id, { ...a, status: 'ACTIVE' });
  }
  async deletePendingAccount(id: string) {
    if (this.accounts.get(id)?.status === 'PENDING') this.accounts.delete(id);
  }
  async createSession(tokenHash: Buffer, accountId: string, expiresAt: Date) {
    this.sessions.set(tokenHash.toString('hex'), { accountId, expiresAt });
  }
  async findSessionAccount(tokenHash: Buffer, now: Date) {
    const s = this.sessions.get(tokenHash.toString('hex'));
    if (!s || s.expiresAt <= now) return null;
    const a = this.accounts.get(s.accountId);
    return a?.status === 'ACTIVE' ? a : null;
  }
  async deleteSession(tokenHash: Buffer) {
    this.sessions.delete(tokenHash.toString('hex'));
  }
  async deleteExpiredSessions(now: Date) {
    let n = 0;
    for (const [k, s] of this.sessions) if (s.expiresAt <= now && this.sessions.delete(k)) n++;
    return n;
  }
}

/** Game Server falso: responde lo que se le programe y anota cada llamada. */
class FakeGame implements GameServerClient {
  calls: Array<{ playerId: string; username: string; accountStatusAtCall?: string }> = [];
  next: FoundResult[] = [];
  constructor(private readonly store: MemoryStore) {}
  async found(playerId: string, username: string): Promise<FoundResult> {
    this.calls.push({ playerId, username, accountStatusAtCall: this.store.accounts.get(playerId)?.status });
    return this.next.shift() ?? { kind: 'founded', city: { id: 7, name: `${username}polis`, centerX: 40, centerY: 50 } };
  }
}

class CapturingLog implements Logger {
  lines: string[] = [];
  info = (msg: string, f?: Record<string, unknown>) => void this.lines.push(JSON.stringify({ msg, ...f }));
  warn = this.info;
  error = this.info;
}

let store: MemoryStore;
let game: FakeGame;
let log: CapturingLog;
let now: number;
let service: AuthService;
let seq: number;

beforeEach(() => {
  store = new MemoryStore();
  game = new FakeGame(store);
  log = new CapturingLog();
  now = T0;
  seq = 0;
  service = new AuthService({
    store,
    game,
    secret: SECRET,
    nowMs: () => now,
    randomUUID: () => `00000000-0000-4000-8000-${String(++seq).padStart(12, '0')}`,
    newSessionToken: () => `token-de-sesion-${++seq}`,
    log,
  });
});

function payloadOf(ticket: string): Record<string, unknown> {
  const [h, p, s] = ticket.split('.');
  expect(s).toBe(createHmac('sha256', SECRET).update(`${h}.${p}`).digest('base64url'));
  return JSON.parse(Buffer.from(p!, 'base64url').toString()) as Record<string, unknown>;
}

describe('AuthService.register', () => {
  it('funda el jugador, activa la cuenta DESPUÉS y entrega sesión y ticket', async () => {
    const r = await service.register('nueva', 'contrasena-larga');
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.status).toBe(201);
    expect(r.body.city).toEqual({ id: 7, name: 'nuevapolis', centerX: 40, centerY: 50 });

    // INV-PLAYER-009: la cuenta estaba PENDING mientras el Game Server fundaba.
    expect(game.calls).toEqual([{ playerId: r.body.playerId, username: 'nueva', accountStatusAtCall: 'PENDING' }]);
    expect(store.accounts.get(r.body.playerId)?.status).toBe('ACTIVE');

    // El ticket es de ADR-010 y su sub es el id de la cuenta, que es players.id.
    const claims = payloadOf(r.body.ticket);
    expect(claims).toMatchObject({ sub: r.body.playerId, aud: 'game-server', exp: T0 / 1000 + 60 });
    expect(r.body.expiresInSeconds).toBe(60);

    // La sesión guarda el hash del token, nunca el token.
    expect(r.sessionToken).toBeDefined();
    expect(store.sessions.has(hashSessionToken(r.sessionToken!).toString('hex'))).toBe(true);
    expect([...store.sessions.keys()].some((k) => k.includes(r.sessionToken!))).toBe(false);
  });

  it('valida nombre y contraseña antes de tocar nada', async () => {
    for (const [u, p, code] of [
      ['ab', 'contrasena-larga', 'INVALID_USERNAME'],
      ['con espacio', 'contrasena-larga', 'INVALID_USERNAME'],
      ['valido', 'corta', 'WEAK_PASSWORD'],
      ['valido', 'x'.repeat(257), 'INVALID_MESSAGE'],
    ] as const) {
      const r = await service.register(u, p);
      expect(r).toMatchObject({ ok: false, status: 400, code });
    }
    expect(store.accounts.size).toBe(0);
    expect(game.calls).toHaveLength(0);
  });

  it('un nombre ya tomado no llega al Game Server', async () => {
    await service.register('tomado', 'contrasena-larga');
    const r = await service.register('tomado', 'otra-contrasena');
    expect(r).toMatchObject({ ok: false, status: 409, code: 'USERNAME_TAKEN' });
    expect(game.calls).toHaveLength(1);
  });

  it('un rechazo definitivo del mundo borra la cuenta pendiente y libera el nombre', async () => {
    game.next.push({ kind: 'rejected', status: 409, code: 'NO_SITE_AVAILABLE', message: 'sin sitio' });
    const r = await service.register('sinsitio', 'contrasena-larga');
    expect(r).toMatchObject({ ok: false, status: 409, code: 'NO_SITE_AVAILABLE' });
    expect(store.accounts.size).toBe(0);
    expect(store.sessions.size).toBe(0);

    const otra = await service.register('sinsitio', 'contrasena-larga');
    expect(otra.ok).toBe(true);
  });

  it('sin respuesta del mundo la cuenta queda PENDING, sin sesión, y el login la termina', async () => {
    game.next.push({ kind: 'unavailable', reason: 'red' });
    const r = await service.register('paciente', 'contrasena-larga');
    expect(r).toMatchObject({ ok: false, status: 503, code: 'GAME_SERVER_UNAVAILABLE' });
    const cuenta = await store.findAccountByUsername('paciente');
    expect(cuenta?.status).toBe('PENDING');
    expect(store.sessions.size).toBe(0);

    // INV-PLAYER-009: una cuenta PENDING no entra sin pasar por la fundación.
    game.next.push({ kind: 'unavailable', reason: 'red' });
    expect(await service.login('paciente', 'contrasena-larga')).toMatchObject({ ok: false, status: 503 });
    expect(store.sessions.size).toBe(0);

    const ok = await service.login('paciente', 'contrasena-larga');
    expect(ok).toMatchObject({ ok: true, status: 200 });
    expect(game.calls.map((c) => c.playerId)).toEqual([cuenta!.id, cuenta!.id, cuenta!.id]);
    expect((await store.findAccountByUsername('paciente'))?.status).toBe('ACTIVE');
  });
});

describe('AuthService.login', () => {
  it('una cuenta activa entra sin volver a fundar', async () => {
    await service.register('activa', 'contrasena-larga');
    const r = await service.login('activa', 'contrasena-larga');
    expect(r).toMatchObject({ ok: true, status: 200 });
    expect(game.calls).toHaveLength(1);
    if (r.ok) expect(r.body.city).toBeUndefined();
  });

  it('contraseña equivocada y usuario inexistente responden exactamente lo mismo', async () => {
    await service.register('alguien', 'contrasena-larga');
    const mala = await service.login('alguien', 'equivocada-del-todo');
    const nadie = await service.login('nadie_existe', 'contrasena-larga');
    const raro = await service.login('no válido!', 'contrasena-larga');
    expect(mala).toEqual(nadie);
    expect(raro).toEqual(nadie);
    expect(mala).toMatchObject({ ok: false, status: 401, code: 'UNAUTHORIZED' });
  });

  it('una cuenta pendiente cuyo nombre ya es de otro jugador se borra', async () => {
    game.next.push({ kind: 'unavailable', reason: 'red' });
    await service.register('heredado', 'contrasena-larga');
    game.next.push({ kind: 'rejected', status: 409, code: 'USERNAME_TAKEN', message: 'ocupado' });
    expect(await service.login('heredado', 'contrasena-larga')).toMatchObject({ status: 409, code: 'USERNAME_TAKEN' });
    expect(store.accounts.size).toBe(0);
  });
});

describe('AuthService.ticket y logout', () => {
  it('la cookie de sesión da un ticket nuevo en cada (re)conexión', async () => {
    const r = await service.register('conectada', 'contrasena-larga');
    if (!r.ok) throw new Error('alta');
    const a = await service.ticket(r.sessionToken);
    const b = await service.ticket(r.sessionToken);
    if (!a.ok || !b.ok) throw new Error('ticket');
    expect(a.body.ticket).not.toBe(b.body.ticket);
    expect(payloadOf(a.body.ticket).jti).not.toBe(payloadOf(b.body.ticket).jti);
    expect(payloadOf(a.body.ticket).sub).toBe(r.body.playerId);
    expect(a.sessionToken).toBeUndefined(); // no renueva la cookie
  });

  it('sin sesión, con una caducada o tras cerrarla, no hay ticket', async () => {
    expect(await service.ticket(undefined)).toMatchObject({ ok: false, status: 401 });
    expect(await service.ticket('inventado')).toMatchObject({ ok: false, status: 401 });

    const r = await service.register('efimera', 'contrasena-larga');
    if (!r.ok) throw new Error('alta');
    now = T0 + SESSION_TTL_MS;
    expect(await service.ticket(r.sessionToken)).toMatchObject({ ok: false, status: 401 });

    now = T0;
    const l = await service.login('efimera', 'contrasena-larga');
    if (!l.ok) throw new Error('login');
    await service.logout(l.sessionToken);
    expect(await service.ticket(l.sessionToken)).toMatchObject({ ok: false, status: 401 });
  });

  it('abrir una sesión barre las caducadas', async () => {
    await service.register('barrendera', 'contrasena-larga');
    now = T0 + SESSION_TTL_MS + 1;
    await service.login('barrendera', 'contrasena-larga');
    expect(store.sessions.size).toBe(1);
  });
});

// INV-PLAYER-007: ninguna línea de log contiene la contraseña, su hash, el token
// de la sesión, un ticket ni el secreto, en ninguna de las rutas que escriben.
describe('los logs de la identidad', () => {
  it('no contienen secretos', async () => {
    const contrasena = 'contrasena-que-no-debe-salir';
    const alta = await service.register('discreta', contrasena);
    if (!alta.ok) throw new Error('alta');
    await service.login('discreta', 'equivocada-del-todo');
    await service.login('nadie_existe', contrasena);
    const login = await service.login('discreta', contrasena);
    if (!login.ok) throw new Error('login');
    game.next.push({ kind: 'rejected', status: 409, code: 'NO_SITE_AVAILABLE', message: 'x' });
    await service.register('rechazada', contrasena);
    game.next.push({ kind: 'unavailable', reason: 'red' });
    await service.register('colgada', contrasena);
    const ticket = await service.ticket(login.sessionToken);
    if (!ticket.ok) throw new Error('ticket');

    const todo = log.lines.join('\n');
    expect(log.lines.length).toBeGreaterThan(5);
    const hash = (await store.findAccountByUsername('discreta'))!.passwordHash;
    for (const prohibido of [
      contrasena,
      hash,
      'scrypt$',
      SECRET,
      alta.sessionToken!,
      login.sessionToken!,
      alta.body.ticket,
      login.body.ticket,
      ticket.body.ticket,
    ]) {
      expect(todo.includes(prohibido), `los logs contienen ${prohibido.slice(0, 12)}…`).toBe(false);
    }
  });
});
