import { describe, expect, it } from 'vitest';

import { SCRYPT_LOG_N, SCRYPT_P, SCRYPT_R, dummyHash, hashPassword, verifyPassword } from './password.mjs';

describe('password', () => {
  it('verifica la contraseña correcta y rechaza cualquier otra', async () => {
    const hash = await hashPassword('contrasena-larga');
    expect(await verifyPassword('contrasena-larga', hash)).toBe(true);
    expect(await verifyPassword('contrasena-largA', hash)).toBe(false);
    expect(await verifyPassword('', hash)).toBe(false);
  });

  it('el hash es autodescrito, lleva el coste de ADR-018 y no contiene la contraseña', async () => {
    const hash = await hashPassword('contrasena-larga');
    const parts = hash.split('$');
    expect(parts.slice(0, 4)).toEqual(['scrypt', String(SCRYPT_LOG_N), String(SCRYPT_R), String(SCRYPT_P)]);
    expect([SCRYPT_LOG_N, SCRYPT_R, SCRYPT_P]).toEqual([15, 8, 3]);
    expect(Buffer.from(parts[4]!, 'base64url')).toHaveLength(16);
    expect(Buffer.from(parts[5]!, 'base64url')).toHaveLength(32);
    expect(hash).not.toContain('contrasena');
  });

  it('dos hashes de la misma contraseña difieren por la sal', async () => {
    expect(await hashPassword('igual-igual')).not.toBe(await hashPassword('igual-igual'));
  });

  it('un hash mal formado nunca verifica', async () => {
    for (const malo of ['', 'scrypt$15$8$3$', '$2a$10$bcrypt', 'scrypt$99$8$3$c2Fs$aGFzaA', 'scrypt$x$8$3$c2Fs$aGFzaA']) {
      expect(await verifyPassword('lo-que-sea', malo)).toBe(false);
    }
  });

  it('el hash de relleno es estable y no verifica contraseñas reales', async () => {
    const a = await dummyHash();
    expect(await dummyHash()).toBe(a);
    expect(await verifyPassword('contrasena-larga', a)).toBe(false);
  });
});
