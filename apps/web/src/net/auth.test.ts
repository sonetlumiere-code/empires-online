import { afterEach, describe, expect, it, vi } from 'vitest';

import { AuthError, logout, resumeSession } from './auth';

/** Sustituye `fetch` por uno que responde siempre lo mismo y anota a qué ruta se llamó. */
function fakeFetch(status: number, body?: unknown) {
  const calls: { path: string; method?: string; credentials?: string }[] = [];
  vi.stubGlobal('fetch', async (path: string, init?: RequestInit) => {
    calls.push({ path, method: init?.method, credentials: init?.credentials });
    return new Response(body === undefined ? null : JSON.stringify(body), { status });
  });
  return calls;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('resumeSession', () => {
  it('con la cookie viva devuelve un ticket para conectar sin formulario', async () => {
    const calls = fakeFetch(200, { playerId: 'p1', ticket: 't-1', expiresInSeconds: 60 });

    await expect(resumeSession()).resolves.toBe('t-1');
    expect(calls).toEqual([{ path: '/api/auth/ticket', method: 'POST', credentials: 'same-origin' }]);
  });

  it('sin sesión (401 UNAUTHORIZED) devuelve null: toca el formulario', async () => {
    fakeFetch(401, { code: 'UNAUTHORIZED', message: 'no hay sesión' });

    await expect(resumeSession()).resolves.toBeNull();
  });

  it('un fallo que no dice nada de la sesión se lanza en vez de tomarse por «sin sesión»', async () => {
    fakeFetch(429, { code: 'RATE_LIMITED', message: 'demasiadas peticiones' });
    await expect(resumeSession()).rejects.toMatchObject({ code: 'RATE_LIMITED' });

    fakeFetch(500, { code: 'INTERNAL_ERROR', message: 'error interno' });
    await expect(resumeSession()).rejects.toBeInstanceOf(AuthError);
  });

  it('un error de red también se lanza', async () => {
    vi.stubGlobal('fetch', async () => {
      throw new TypeError('Failed to fetch');
    });

    await expect(resumeSession()).rejects.toThrow('Failed to fetch');
  });
});

describe('logout', () => {
  it('con el 204 del servidor termina sin error', async () => {
    const calls = fakeFetch(204);

    await expect(logout()).resolves.toBeUndefined();
    expect(calls).toEqual([{ path: '/api/auth/logout', method: 'POST', credentials: 'same-origin' }]);
  });

  it('si el servidor no lo confirma, lanza: la cookie podría seguir viva', async () => {
    fakeFetch(429, { code: 'RATE_LIMITED', message: 'demasiadas peticiones' });
    await expect(logout()).rejects.toMatchObject({ code: 'RATE_LIMITED' });

    fakeFetch(502);
    await expect(logout()).rejects.toMatchObject({ code: 'INTERNAL_ERROR' });
  });
});
