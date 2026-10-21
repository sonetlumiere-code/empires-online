/**
 * Almacén de identidad (ADR-018 §2.1): cuentas y sesiones web.
 *
 * Es de apps/web y vive en su propia base, separada del esquema del juego. La
 * interfaz existe para que la orquestación del alta y del login se pruebe sin
 * base de datos; `PgIdentityStore` es la única implementación de producción.
 */
import 'server-only';

import { Pool } from 'pg';

import { migrateIdentity } from '../../identity/migrations.mjs';

export type AccountStatus = 'PENDING' | 'ACTIVE';

export interface Account {
  readonly id: string;
  readonly username: string;
  readonly passwordHash: string;
  readonly status: AccountStatus;
}

export interface IdentityStore {
  /** Crea una cuenta PENDING. `taken` si el nombre ya existe. */
  createAccount(account: { id: string; username: string; passwordHash: string }): Promise<'created' | 'taken'>;
  findAccountByUsername(username: string): Promise<Account | null>;
  /** PENDING → ACTIVE. Sólo después de que el Game Server confirme el jugador. */
  activateAccount(id: string): Promise<void>;
  /** Borra la cuenta sólo si sigue PENDING: una ACTIVE nunca se borra por aquí. */
  deletePendingAccount(id: string): Promise<void>;
  createSession(tokenHash: Buffer, accountId: string, expiresAt: Date): Promise<void>;
  /** La cuenta ACTIVE de una sesión vigente, o null (INV-PLAYER-009). */
  findSessionAccount(tokenHash: Buffer, now: Date): Promise<Account | null>;
  deleteSession(tokenHash: Buffer): Promise<void>;
  deleteExpiredSessions(now: Date): Promise<number>;
}

interface AccountRow {
  id: string;
  username: string;
  password_hash: string;
  status: AccountStatus;
}

const toAccount = (r: AccountRow): Account => ({
  id: r.id,
  username: r.username,
  passwordHash: r.password_hash,
  status: r.status,
});

export class PgIdentityStore implements IdentityStore {
  constructor(private readonly pool: Pool) {}

  /** Pool pequeño: pedir un ticket es una operación por conexión, no por mensaje. */
  static connect(url: string): PgIdentityStore {
    return new PgIdentityStore(new Pool({ connectionString: url, max: 5 }));
  }

  /** Aplica las migraciones pendientes del almacén con una conexión dedicada. */
  async migrate(): Promise<number[]> {
    const client = await this.pool.connect();
    try {
      return await migrateIdentity(client);
    } finally {
      client.release();
    }
  }

  async close(): Promise<void> {
    await this.pool.end();
  }

  async createAccount(a: { id: string; username: string; passwordHash: string }): Promise<'created' | 'taken'> {
    try {
      await this.pool.query('INSERT INTO accounts (id, username, password_hash) VALUES ($1, $2, $3)', [
        a.id,
        a.username,
        a.passwordHash,
      ]);
      return 'created';
    } catch (err) {
      if ((err as { code?: string }).code === '23505') return 'taken';
      throw err;
    }
  }

  async findAccountByUsername(username: string): Promise<Account | null> {
    const { rows } = await this.pool.query<AccountRow>(
      'SELECT id, username, password_hash, status FROM accounts WHERE username = $1',
      [username],
    );
    return rows[0] ? toAccount(rows[0]) : null;
  }

  async activateAccount(id: string): Promise<void> {
    await this.pool.query(
      `UPDATE accounts SET status = 'ACTIVE', activated_at = now() WHERE id = $1 AND status = 'PENDING'`,
      [id],
    );
  }

  async deletePendingAccount(id: string): Promise<void> {
    await this.pool.query(`DELETE FROM accounts WHERE id = $1 AND status = 'PENDING'`, [id]);
  }

  async createSession(tokenHash: Buffer, accountId: string, expiresAt: Date): Promise<void> {
    await this.pool.query('INSERT INTO web_sessions (token_hash, account_id, expires_at) VALUES ($1, $2, $3)', [
      tokenHash,
      accountId,
      expiresAt,
    ]);
  }

  async findSessionAccount(tokenHash: Buffer, now: Date): Promise<Account | null> {
    const { rows } = await this.pool.query<AccountRow>(
      `SELECT a.id, a.username, a.password_hash, a.status
         FROM web_sessions s JOIN accounts a ON a.id = s.account_id
        WHERE s.token_hash = $1 AND s.expires_at > $2 AND a.status = 'ACTIVE'`,
      [tokenHash, now],
    );
    return rows[0] ? toAccount(rows[0]) : null;
  }

  async deleteSession(tokenHash: Buffer): Promise<void> {
    await this.pool.query('DELETE FROM web_sessions WHERE token_hash = $1', [tokenHash]);
  }

  async deleteExpiredSessions(now: Date): Promise<number> {
    const result = await this.pool.query('DELETE FROM web_sessions WHERE expires_at <= $1', [now]);
    return result.rowCount ?? 0;
  }
}
