/**
 * El pool del almacén de identidad, sin base de datos: sus plazos y, sobre
 * todo, que un error de una conexión ociosa no tumbe el proceso. La semántica
 * contra PostgreSQL real está en store.integration.test.ts.
 */
import { Pool } from 'pg';
import { describe, expect, it } from 'vitest';

import type { Logger } from './authService';
import { IDENTITY_POOL_OPTIONS, PgIdentityStore, createIdentityPool } from './store';

// Sin contraseña a propósito: el escaneo de secretos no tiene nada que ver aquí.
const URL = 'postgres://empires_identity@db.interna:5432/empires_identity';

class CapturingLog implements Logger {
  lines: Array<Record<string, unknown>> = [];
  info = (msg: string, f?: Record<string, unknown>) => void this.lines.push({ level: 'info', msg, ...f });
  warn = (msg: string, f?: Record<string, unknown>) => void this.lines.push({ level: 'warn', msg, ...f });
  error = (msg: string, f?: Record<string, unknown>) => void this.lines.push({ level: 'error', msg, ...f });
}

describe('createIdentityPool', () => {
  it('un error de una conexión ociosa se registra y el proceso sigue en pie', async () => {
    const log = new CapturingLog();
    const pool = createIdentityPool(URL, log);
    try {
      // Así es como pg-pool avisa de que el servidor cerró una conexión ociosa.
      const perdida = Object.assign(new Error('connection terminated unexpectedly'), { code: '57P01' });
      expect(pool.listenerCount('error')).toBe(1);
      expect(() => pool.emit('error', perdida)).not.toThrow();
      expect(log.lines).toEqual([
        { level: 'error', msg: 'conexión ociosa del almacén de identidad perdida', err: 'connection terminated unexpectedly', code: '57P01' },
      ]);
      // INV-SEC-006: la URL no va al log.
      expect(JSON.stringify(log.lines)).not.toContain('db.interna');
    } finally {
      await pool.end();
    }
  });

  it('sin el oyente, ese mismo evento sería una excepción sin capturar', async () => {
    const sinOyente = new Pool({ connectionString: URL });
    try {
      expect(() => sinOyente.emit('error', new Error('connection terminated unexpectedly'))).toThrow(
        'connection terminated unexpectedly',
      );
    } finally {
      await sinOyente.end();
    }
  });

  it('el pool lleva sus plazos: conexión, inactividad, consulta en servidor y en cliente', async () => {
    const pool = createIdentityPool(URL, new CapturingLog());
    try {
      expect(pool.options).toMatchObject({
        connectionString: URL,
        max: 5,
        connectionTimeoutMillis: 5_000,
        idleTimeoutMillis: 30_000,
        statement_timeout: 5_000,
        query_timeout: 10_000,
        keepAlive: true,
      });
      expect(pool.options).toMatchObject(IDENTITY_POOL_OPTIONS);
    } finally {
      await pool.end();
    }
  });

  it('PgIdentityStore.connect construye el pool con el oyente y recuerda la URL para migrar', async () => {
    const log = new CapturingLog();
    const store = PgIdentityStore.connect(URL, log);
    const pool = (store as unknown as { pool: Pool }).pool;
    try {
      expect(pool.listenerCount('error')).toBe(1);
      expect((store as unknown as { url: string }).url).toBe(URL);
      // Construido sobre un pool ajeno (como hace el test de integración), la URL sale del pool.
      expect((new PgIdentityStore(pool) as unknown as { url: string }).url).toBe(URL);
    } finally {
      await store.close();
    }
  });
});
