import { createHmac } from 'node:crypto';

import { describe, expect, it } from 'vitest';

import { lookupPlayer, settleStalePending, sweepStalePending } from './pending.mjs';

const SECRET = 'secreto-de-los-tests-de-las-cuentas-pendientes';
const ID = '11111111-1111-4111-8111-111111111111';

type Reply = { status: number; body?: unknown } | Error;

/** fetch falso: anota cada petición y responde lo programado. */
function fakeFetch(reply: Reply) {
  const calls: Array<{ url: string; init: RequestInit }> = [];
  const fn = async (url: string, init: RequestInit) => {
    calls.push({ url, init });
    if (reply instanceof Error) throw reply;
    return new Response(reply.body === undefined ? 'no es json' : JSON.stringify(reply.body), { status: reply.status });
  };
  return { fn: fn as unknown as typeof fetch, calls };
}

function lookup(reply: Reply) {
  const f = fakeFetch(reply);
  const result = lookupPlayer(
    { baseUrl: 'http://mundo:8080', secret: SECRET, nowMs: () => 1_760_000_000_000, randomUUID: () => 'jti-1', fetch: f.fn },
    ID,
  );
  return { result, calls: f.calls };
}

describe('lookupPlayer', () => {
  it('pregunta por el jugador con un token interno cuyo sub es él', async () => {
    const { result, calls } = lookup({ status: 200, body: { playerId: ID, username: 'ana' } });
    expect(await result).toEqual({ kind: 'exists', username: 'ana' });
    expect(calls).toHaveLength(1);
    expect(calls[0]!.url).toBe(`http://mundo:8080/internal/players/${ID}`);
    expect(calls[0]!.init.method).toBe('GET');
    const token = String((calls[0]!.init.headers as Record<string, string>).Authorization).replace(/^Bearer /, '');
    const [h, p, s] = token.split('.');
    expect(s).toBe(createHmac('sha256', SECRET).update(`${h}.${p}`).digest('base64url'));
    expect(JSON.parse(Buffer.from(p!, 'base64url').toString())).toMatchObject({ sub: ID, aud: 'game-server-internal' });
  });

  it('sólo un 404 PLAYER_NOT_FOUND significa «no existe»', async () => {
    expect(await lookup({ status: 404, body: { code: 'PLAYER_NOT_FOUND' } }).result).toEqual({ kind: 'absent' });
  });

  // Con duda, la cuenta no se toca: todo lo que no es una respuesta clara es «no se sabe».
  it.each<[string, Reply]>([
    ['un 404 de otra cosa (ruta inexistente)', { status: 404, body: { code: 'NOT_FOUND' } }],
    ['un 404 sin cuerpo JSON', { status: 404 }],
    ['un 401 por secretos desincronizados', { status: 401, body: { code: 'UNAUTHORIZED' } }],
    ['un 500', { status: 500, body: { code: 'INTERNAL_ERROR' } }],
    ['un 200 de otro jugador', { status: 200, body: { playerId: 'otro', username: 'ana' } }],
    ['un 200 sin nombre', { status: 200, body: { playerId: ID } }],
    ['un error de red', new TypeError('fetch failed')],
  ])('%s es «no se sabe»', async (_, reply) => {
    expect((await lookup(reply).result).kind).toBe('unavailable');
  });
});

describe('settleStalePending', () => {
  function deps(found: Awaited<ReturnType<typeof lookupPlayer>>, removed = true) {
    const did: string[] = [];
    return {
      did,
      deps: {
        lookup: async (id: string) => (did.push(`lookup:${id}`), found),
        activate: async (id: string) => void did.push(`activate:${id}`),
        removeIfStale: async (id: string) => (did.push(`remove:${id}`), removed),
        log: { info: () => undefined, warn: () => undefined },
      },
    };
  }
  const account = { id: ID, username: 'ana' };

  it('si el jugador existe, activa y no borra', async () => {
    const d = deps({ kind: 'exists', username: 'ana' });
    expect(await settleStalePending(d.deps, account)).toBe('activated');
    expect(d.did).toEqual([`lookup:${ID}`, `activate:${ID}`]);
  });

  it('si no existe, borra con la guarda', async () => {
    const d = deps({ kind: 'absent' });
    expect(await settleStalePending(d.deps, account)).toBe('deleted');
    expect(d.did).toEqual([`lookup:${ID}`, `remove:${ID}`]);
  });

  it('si la guarda impide el borrado, la cuenta cambió entretanto', async () => {
    expect(await settleStalePending(deps({ kind: 'absent' }, false).deps, account)).toBe('kept');
  });

  it('si el mundo no contesta, no toca nada', async () => {
    const d = deps({ kind: 'unavailable', reason: 'red' });
    expect(await settleStalePending(d.deps, account)).toBe('unavailable');
    expect(d.did).toEqual([`lookup:${ID}`]);
  });

  it('el barrido pide su límite y resuelve cada cuenta', async () => {
    const d = deps({ kind: 'absent' });
    let asked = 0;
    const report = await sweepStalePending(
      { ...d.deps, list: async (n: number) => ((asked = n), [account, { id: 'b', username: 'b' }]) },
      7,
    );
    expect(asked).toBe(7);
    expect(report).toEqual({ activated: 0, deleted: 2, kept: 0, unavailable: 0 });
  });
});
