/**
 * Alta, login, ticket y cierre de sesión (ADR-018 §2.3–2.5).
 *
 * Toda la orquestación entre el almacén de identidad y el Game Server vive aquí,
 * sin HTTP y con las dependencias inyectadas, para poder probar cada forma en que
 * la fundación puede fallar.
 *
 * La regla que no se negocia (INV-PLAYER-009): una cuenta sólo pasa a ACTIVE
 * DESPUÉS de que el Game Server confirme su jugador, y sólo una cuenta ACTIVE
 * obtiene sesión y ticket.
 *
 * Lo que nunca se registra: la contraseña, su hash, el token de la sesión y los
 * tickets (INV-PLAYER-007).
 */
import 'server-only';

import { createHash } from 'node:crypto';

import { dummyHash, hashPassword, verifyPassword } from '../../identity/password.mjs';
import { settleStalePending, sweepStalePending } from '../../identity/pending.mjs';
import type { CityInfo, GameServerClient } from './gameServer';
import type { Account, IdentityStore } from './store';
import { TOKEN_TTL_SECONDS, issueGameTicket } from './tokens';

/** Vida absoluta de una sesión web. */
export const SESSION_TTL_MS = 7 * 24 * 60 * 60 * 1000;

const USERNAME_RE = /^[A-Za-z0-9_-]{3,24}$/;
const MIN_PASSWORD = 8;
/** Acota el trabajo de scrypt sobre entradas absurdas. */
const MAX_PASSWORD = 256;

export interface Logger {
  info(msg: string, fields?: Record<string, unknown>): void;
  warn(msg: string, fields?: Record<string, unknown>): void;
  error(msg: string, fields?: Record<string, unknown>): void;
}

export interface AuthDeps {
  readonly store: IdentityStore;
  readonly game: GameServerClient;
  readonly secret: string;
  readonly nowMs: () => number;
  readonly randomUUID: () => string;
  /** Token opaco de sesión: 32 bytes aleatorios en base64url. */
  readonly newSessionToken: () => string;
  readonly log: Logger;
}

export interface TicketBody {
  readonly playerId: string;
  readonly ticket: string;
  readonly expiresInSeconds: number;
  readonly city?: CityInfo;
}

export type AuthResult =
  | { readonly ok: true; readonly status: number; readonly body: TicketBody; readonly sessionToken?: string }
  | { readonly ok: false; readonly status: number; readonly code: string; readonly message: string };

const fail = (status: number, code: string, message: string): AuthResult => ({ ok: false, status, code, message });

/** Cómo terminó la resolución de una cuenta PENDING abandonada (ADR-020). */
export type SettleOutcome = 'activated' | 'deleted' | 'kept' | 'unavailable';

/** Recuento de un barrido de cuentas abandonadas. */
export type SweepReport = Readonly<Record<SettleOutcome, number>>;

/** SHA-256 del token de sesión: lo único que se guarda de él. */
export function hashSessionToken(token: string): Buffer {
  return createHash('sha256').update(token).digest();
}

export class AuthService {
  constructor(private readonly deps: AuthDeps) {}

  async register(username: string, password: string): Promise<AuthResult> {
    if (!USERNAME_RE.test(username)) {
      return fail(400, 'INVALID_USERNAME', 'el nombre debe tener entre 3 y 24 caracteres alfanuméricos, guion o guion bajo');
    }
    if (password.length < MIN_PASSWORD) {
      return fail(400, 'WEAK_PASSWORD', `la contraseña debe tener al menos ${MIN_PASSWORD} caracteres`);
    }
    if (password.length > MAX_PASSWORD) {
      return fail(400, 'INVALID_MESSAGE', `la contraseña no puede pasar de ${MAX_PASSWORD} caracteres`);
    }

    const { store, log } = this.deps;
    const id = this.deps.randomUUID();
    const passwordHash = await hashPassword(password);
    if ((await store.createAccount({ id, username, passwordHash })) === 'taken') {
      // Un alta abandonada no retiene el nombre para siempre: si lo ocupa una
      // cuenta PENDING sin intento en la última hora, se resuelve ahora (ADR-020).
      const freed = await this.freeAbandonedName(username);
      if (freed === 'unavailable') {
        return fail(503, 'GAME_SERVER_UNAVAILABLE', 'ese nombre lo retiene un alta sin terminar y el mundo no responde: vuelve a intentarlo en un momento');
      }
      // Un solo reintento: si otra alta lo tomó entretanto, el nombre está en uso.
      if (freed !== 'deleted' || (await store.createAccount({ id, username, passwordHash })) === 'taken') {
        return fail(409, 'USERNAME_TAKEN', 'ese nombre ya está en uso');
      }
    }
    log.info('cuenta creada, pendiente de fundación', { player_id: id, username });

    const founded = await this.found(id, username);
    if (!founded.ok) return founded.result;
    return this.signIn(id, 201, founded.city);
  }

  async login(username: string, password: string): Promise<AuthResult> {
    const { store, log } = this.deps;
    const account = USERNAME_RE.test(username) ? await store.findAccountByUsername(username) : null;
    // Con el usuario inexistente se compara igualmente contra un hash: mismo
    // tiempo, misma respuesta, y no se revela qué nombres existen.
    const valid = await verifyPassword(password.slice(0, MAX_PASSWORD), account?.passwordHash ?? (await dummyHash()));
    if (!account || !valid || password.length > MAX_PASSWORD) {
      log.warn('login rechazado', { username, reason: account ? 'contraseña' : 'usuario inexistente' });
      return fail(401, 'UNAUTHORIZED', 'credenciales inválidas');
    }

    if (account.status === 'ACTIVE') return this.signIn(account.id, 200);

    // Una cuenta PENDING es un alta que no terminó: la fundación es idempotente
    // por playerId, así que se repite y, si confirma, la cuenta se activa.
    // Antes se renueva el intento: un barrido concurrente ya no puede borrarla, y
    // si la borró o la activó antes, se sabe aquí, sin llamar al mundo (ADR-020).
    const state = await store.markFoundingAttempt(account.id);
    if (state === 'active') return this.signIn(account.id, 200);
    if (state === 'gone') {
      log.warn('login rechazado: la cuenta pendiente se borró mientras tanto', { player_id: account.id, username });
      return fail(401, 'UNAUTHORIZED', 'credenciales inválidas');
    }
    log.info('login de una cuenta pendiente: se completa la fundación', { player_id: account.id, username });
    const founded = await this.found(account.id, account.username);
    if (!founded.ok) return founded.result;
    return this.signIn(account.id, 200, founded.city);
  }

  /** Ticket nuevo para una sesión vigente. Lo pide el cliente en cada (re)conexión. */
  async ticket(sessionToken: string | undefined): Promise<AuthResult> {
    const account = await this.sessionAccount(sessionToken);
    if (!account) return fail(401, 'UNAUTHORIZED', 'no hay sesión');
    return { ok: true, status: 200, body: this.ticketBody(account.id) };
  }

  /**
   * Resuelve las cuentas PENDING abandonadas, de la más antigua a la más
   * reciente y de una en una (ADR-020 §2.3). Lo usa `pnpm run identity:sweep`.
   */
  sweepStalePending(limit = 500): Promise<SweepReport> {
    return sweepStalePending({ ...this.settleDeps(), list: (n) => this.deps.store.listStalePending(n) }, limit);
  }

  async logout(sessionToken: string | undefined): Promise<void> {
    if (sessionToken) await this.deps.store.deleteSession(hashSessionToken(sessionToken));
  }

  /** Si el nombre lo ocupa una cuenta PENDING abandonada, la resuelve. */
  private async freeAbandonedName(username: string): Promise<SettleOutcome | 'none'> {
    const stale = await this.deps.store.findStalePending(username);
    return stale ? settleStalePending(this.settleDeps(), stale) : 'none';
  }

  private settleDeps() {
    const { store, game, log } = this.deps;
    return {
      lookup: (id: string) => game.lookup(id),
      activate: (id: string) => store.activateAccount(id),
      removeIfStale: (id: string) => store.deleteStalePending(id),
      log,
    };
  }

  private async sessionAccount(sessionToken: string | undefined): Promise<Account | null> {
    if (!sessionToken) return null;
    return this.deps.store.findSessionAccount(hashSessionToken(sessionToken), new Date(this.deps.nowMs()));
  }

  /**
   * Funda el jugador en el Game Server y gobierna el estado de la cuenta:
   * ACTIVE si el jugador existe, borrada si el rechazo es definitivo, PENDING
   * si no se sabe.
   */
  private async found(
    id: string,
    username: string,
  ): Promise<{ ok: true; city: CityInfo } | { ok: false; result: AuthResult }> {
    const { store, game, log } = this.deps;
    const outcome = await game.found(id, username);

    if (outcome.kind === 'founded') {
      await store.activateAccount(id);
      log.info('cuenta activada', { player_id: id, username, city_id: outcome.city.id });
      return { ok: true, city: outcome.city };
    }
    if (outcome.kind === 'rejected') {
      // El nombre queda libre y el alta se puede repetir.
      await store.deletePendingAccount(id);
      log.warn('fundación rechazada; se borra la cuenta pendiente', { player_id: id, username, code: outcome.code });
      return { ok: false, result: fail(outcome.status, outcome.code, outcome.message) };
    }
    log.error('fundación sin respuesta; la cuenta queda pendiente', { player_id: id, username, reason: outcome.reason });
    return {
      ok: false,
      result: fail(503, 'GAME_SERVER_UNAVAILABLE', 'la cuenta está creada, pero el mundo no respondió: vuelve a entrar en un momento'),
    };
  }

  /** Crea la sesión web y emite el primer ticket. Sólo para cuentas ACTIVE. */
  private async signIn(accountId: string, status: number, city?: CityInfo): Promise<AuthResult> {
    const { store, nowMs } = this.deps;
    const now = nowMs();
    await store.deleteExpiredSessions(new Date(now));
    const sessionToken = this.deps.newSessionToken();
    await store.createSession(hashSessionToken(sessionToken), accountId, new Date(now + SESSION_TTL_MS));
    return { ok: true, status, body: this.ticketBody(accountId, city), sessionToken };
  }

  private ticketBody(playerId: string, city?: CityInfo): TicketBody {
    const ticket = issueGameTicket({
      playerId,
      secret: this.deps.secret,
      nowMs: this.deps.nowMs(),
      jti: this.deps.randomUUID(),
    });
    return { playerId, ticket, expiresInSeconds: TOKEN_TTL_SECONDS, ...(city ? { city } : {}) };
  }
}
