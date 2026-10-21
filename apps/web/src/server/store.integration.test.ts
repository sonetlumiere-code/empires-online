/**
 * Almacén de identidad contra PostgreSQL real (ADR-018).
 *
 * Como los tests de integración del Game Server: se activan con EO_INTEGRATION=1
 * y se niegan a correr contra una base cuyo nombre no termine en `_test`, porque
 * vacían las tablas antes de cada test.
 *
 *   EO_INTEGRATION=1 EO_TEST_IDENTITY_POSTGRES_URL=... pnpm --filter @empires-online/web test
 */
import { Client, Pool } from 'pg';
import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest';

import { hashPassword } from '../../identity/password.mjs';
import { PgIdentityStore } from './store';

const url = process.env.EO_TEST_IDENTITY_POSTGRES_URL ?? '';
const enabled = process.env.EO_INTEGRATION === '1';

if (enabled && !/\/[^/?]+_test(\?|$)/.test(url)) {
  throw new Error(`EO_TEST_IDENTITY_POSTGRES_URL debe apuntar a una base *_test (vacía sus tablas)`);
}

describe.skipIf(!enabled)('PgIdentityStore (integración)', () => {
  let pool: Pool;
  let store: PgIdentityStore;
  let hash: string;

  beforeAll(async () => {
    pool = new Pool({ connectionString: url, max: 3 });
    store = new PgIdentityStore(pool);
    await store.migrate();
    hash = await hashPassword('contrasena-larga');
  });

  afterAll(async () => {
    await store.close();
  });

  beforeEach(async () => {
    await pool.query('TRUNCATE accounts CASCADE');
  });

  const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
  const token = (n: number) => Buffer.alloc(32, n);

  it('las migraciones son idempotentes y quedan registradas', async () => {
    expect(await store.migrate()).toEqual([]);
    const { rows } = await pool.query('SELECT version, name FROM identity_schema_migrations ORDER BY version');
    expect(rows).toEqual([{ version: 1, name: 'accounts_and_sessions' }]);
  });

  // INV-PLAYER-007, mitad del almacén de identidad: las dos únicas columnas de
  // credencial son el hash de la contraseña y el hash del token de sesión.
  it('las únicas columnas de credencial son dos hashes', async () => {
    const { rows } = await pool.query(
      `SELECT table_name || '.' || column_name AS c FROM information_schema.columns
        WHERE table_schema = 'public' AND column_name ~* '(passw|secret|ticket|token|credential)'
        ORDER BY 1`,
    );
    expect(rows.map((r) => r.c)).toEqual(['accounts.password_hash', 'web_sessions.token_hash']);
  });

  it('una cuenta nace PENDING y el nombre es único', async () => {
    expect(await store.createAccount({ id: id(1), username: 'unica', passwordHash: hash })).toBe('created');
    expect(await store.createAccount({ id: id(2), username: 'unica', passwordHash: hash })).toBe('taken');
    expect(await store.findAccountByUsername('unica')).toEqual({
      id: id(1),
      username: 'unica',
      passwordHash: hash,
      status: 'PENDING',
    });
    expect(await store.findAccountByUsername('nadie')).toBeNull();
  });

  it('las restricciones rechazan lo que el dominio nunca escribe', async () => {
    const malas = [
      [`INSERT INTO accounts (id, username, password_hash) VALUES ($1, 'con espacio', $2)`, [id(3), hash]],
      [`INSERT INTO accounts (id, username, password_hash, status) VALUES ($1, 'rara', $2, 'BANEADA')`, [id(4), hash]],
      [`INSERT INTO accounts (id, username, password_hash, status) VALUES ($1, 'sinfecha', $2, 'ACTIVE')`, [id(5), hash]],
      [`INSERT INTO accounts (id, username, password_hash) VALUES ($1, 'enclaro', 'contrasena-larga')`, [id(6)]],
    ] as const;
    for (const [sql, values] of malas) {
      await expect(pool.query(sql, [...values])).rejects.toMatchObject({ code: '23514' });
    }
    await store.createAccount({ id: id(7), username: 'consesion', passwordHash: hash });
    await expect(
      pool.query(`INSERT INTO web_sessions (token_hash, account_id, expires_at) VALUES ($1, $2, now() + interval '1 day')`, [
        Buffer.alloc(16),
        id(7),
      ]),
    ).rejects.toMatchObject({ code: '23514' });
  });

  it('activar sólo mueve PENDING a ACTIVE, y una ACTIVE no se borra como pendiente', async () => {
    await store.createAccount({ id: id(1), username: 'activa', passwordHash: hash });
    await store.activateAccount(id(1));
    expect((await store.findAccountByUsername('activa'))?.status).toBe('ACTIVE');
    await store.deletePendingAccount(id(1));
    expect(await store.findAccountByUsername('activa')).not.toBeNull();

    await store.createAccount({ id: id(2), username: 'pendiente', passwordHash: hash });
    await store.deletePendingAccount(id(2));
    expect(await store.findAccountByUsername('pendiente')).toBeNull();
  });

  // INV-PLAYER-009: la sesión de una cuenta que no es ACTIVE no da cuenta.
  it('una sesión sólo resuelve si está vigente y su cuenta está ACTIVE', async () => {
    const ahora = new Date('2026-10-21T12:00:00Z');
    const despues = new Date(ahora.getTime() + 60_000);
    await store.createAccount({ id: id(1), username: 'conectada', passwordHash: hash });
    await store.createSession(token(1), id(1), despues);
    expect(await store.findSessionAccount(token(1), ahora)).toBeNull(); // PENDING

    await store.activateAccount(id(1));
    expect((await store.findSessionAccount(token(1), ahora))?.id).toBe(id(1));
    expect(await store.findSessionAccount(token(1), despues)).toBeNull(); // caducada
    expect(await store.findSessionAccount(token(9), ahora)).toBeNull();

    await store.createSession(token(2), id(1), despues);
    expect(await store.deleteExpiredSessions(despues)).toBe(2);
    await store.createSession(token(3), id(1), despues);
    await store.deleteSession(token(3));
    expect(await store.findSessionAccount(token(3), ahora)).toBeNull();
  });

  it('borrar la cuenta borra sus sesiones', async () => {
    await store.createAccount({ id: id(1), username: 'efimera', passwordHash: hash });
    await store.createSession(token(1), id(1), new Date(Date.now() + 60_000));
    await store.deletePendingAccount(id(1));
    const { rows } = await pool.query('SELECT count(*)::int AS n FROM web_sessions');
    expect(rows[0].n).toBe(0);
  });

  // ADR-018 §2.1: el rol de identidad no puede abrir la base del juego.
  it.skipIf(!process.env.EO_TEST_POSTGRES_URL)('el rol de identidad no puede conectarse a la base del juego', async () => {
    const juego = new URL(process.env.EO_TEST_POSTGRES_URL!);
    const intruso = new URL(url);
    intruso.pathname = juego.pathname;
    const client = new Client({ connectionString: intruso.toString() });
    await expect(client.connect()).rejects.toMatchObject({ code: '42501' });
    await client.end().catch(() => undefined);
  });
});
