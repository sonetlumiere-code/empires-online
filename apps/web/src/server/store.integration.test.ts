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
    expect(rows).toEqual([
      { version: 1, name: 'accounts_and_sessions' },
      { version: 2, name: 'accounts_founding_attempted_at' },
    ]);
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
    // Relativa al reloj real, nunca una fecha fija: la base exige que la sesión
    // caduque después de `created_at`, que es su now(). Con una fecha fija el
    // test caducaba él solo el día que esa fecha quedaba en el pasado.
    const ahora = new Date(Date.now() + 60 * 60_000);
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

  // ADR-020: antigüedad del último intento de fundación, en el reloj de la base.
  const age = (accountId: string, interval: string) =>
    pool.query(`UPDATE accounts SET founding_attempted_at = now() - $2::interval WHERE id = $1`, [accountId, interval]);

  it('el alta fija el intento de fundación y el login lo renueva sólo si sigue PENDING', async () => {
    await store.createAccount({ id: id(1), username: 'intento', passwordHash: hash });
    const { rows } = await pool.query(`SELECT founding_attempted_at > now() - interval '1 minute' AS reciente FROM accounts`);
    expect(rows[0].reciente).toBe(true);

    await age(id(1), '2 hours');
    expect(await store.listStalePending(10)).toEqual([{ id: id(1), username: 'intento' }]);
    expect(await store.markFoundingAttempt(id(1))).toBe('pending');
    expect(await store.listStalePending(10)).toEqual([]);

    await store.activateAccount(id(1));
    await age(id(1), '2 hours');
    expect(await store.markFoundingAttempt(id(1))).toBe('active');
    const after = await pool.query(`SELECT founding_attempted_at < now() - interval '1 hour' AS vieja FROM accounts`);
    expect(after.rows[0].vieja).toBe(true); // a una ACTIVE no se le toca nada
    expect(await store.markFoundingAttempt(id(9))).toBe('gone');
  });

  it('abandonada es PENDING con el intento de hace más de una hora, por orden de antigüedad', async () => {
    await store.createAccount({ id: id(1), username: 'vieja', passwordHash: hash });
    await store.createAccount({ id: id(2), username: 'mas_vieja', passwordHash: hash });
    await store.createAccount({ id: id(3), username: 'reciente', passwordHash: hash });
    await store.createAccount({ id: id(4), username: 'activa', passwordHash: hash });
    await age(id(1), '61 minutes');
    await age(id(2), '3 hours');
    await age(id(3), '59 minutes');
    await store.activateAccount(id(4));
    await age(id(4), '3 hours');

    expect(await store.listStalePending(10)).toEqual([
      { id: id(2), username: 'mas_vieja' },
      { id: id(1), username: 'vieja' },
    ]);
    expect(await store.listStalePending(1)).toEqual([{ id: id(2), username: 'mas_vieja' }]);
    expect(await store.findStalePending('vieja')).toEqual({ id: id(1), username: 'vieja' });
    expect(await store.findStalePending('reciente')).toBeNull();
    expect(await store.findStalePending('activa')).toBeNull();
  });

  it('a igual antigüedad, el barrido desempata por id', async () => {
    // Insertadas al revés del orden por id, y con el mismo instante: sólo el
    // ORDER BY decide, no el índice ni el orden físico.
    await store.createAccount({ id: id(6), username: 'seis', passwordHash: hash });
    await store.createAccount({ id: id(5), username: 'cinco', passwordHash: hash });
    await pool.query(`UPDATE accounts SET founding_attempted_at = '2026-01-01T00:00:00Z'`);
    expect(await store.listStalePending(1)).toEqual([{ id: id(5), username: 'cinco' }]);
  });

  it('el borrado de una abandonada lleva la guarda en la sentencia', async () => {
    await store.createAccount({ id: id(1), username: 'reciente', passwordHash: hash });
    await store.createAccount({ id: id(2), username: 'activa', passwordHash: hash });
    await store.createAccount({ id: id(3), username: 'abandonada', passwordHash: hash });
    await store.createSession(token(3), id(3), new Date(Date.now() + 60_000));
    await store.activateAccount(id(2));
    await age(id(2), '3 hours');
    await age(id(3), '3 hours');

    expect(await store.deleteStalePending(id(1))).toBe(false);
    expect(await store.deleteStalePending(id(2))).toBe(false);
    expect(await store.deleteStalePending(id(3))).toBe(true);
    expect(await store.deleteStalePending(id(3))).toBe(false);
    const { rows } = await pool.query('SELECT username FROM accounts ORDER BY username');
    expect(rows.map((r) => r.username)).toEqual(['activa', 'reciente']);
    const sessions = await pool.query('SELECT count(*)::int AS n FROM web_sessions');
    expect(sessions.rows[0].n).toBe(0);
  });

  it('un intento renovado entre la consulta y el borrado lo impide', async () => {
    await store.createAccount({ id: id(1), username: 'disputada', passwordHash: hash });
    await age(id(1), '3 hours');
    expect(await store.findStalePending('disputada')).not.toBeNull();
    await store.markFoundingAttempt(id(1)); // el login llega mientras se pregunta al mundo
    expect(await store.deleteStalePending(id(1))).toBe(false);
    expect((await store.findAccountByUsername('disputada'))?.status).toBe('PENDING');
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
