import { createHmac } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { GAME_AUDIENCE, INTERNAL_AUDIENCE, TOKEN_TTL_SECONDS, issueFoundingToken, issueGameTicket } from './tokens';

/**
 * Contrato entre lenguajes (ADR-018 §2.4). Este fixture lo produce el emisor de
 * TypeScript y lo verifica `TestLosTokensDeAppsWebSeVerificanEnGo` del Game
 * Server. Si cualquiera de los dos lados cambia el formato, uno de los dos
 * tests falla. Para regenerarlo a propósito: EO_UPDATE_FIXTURES=1.
 */
const FIXTURE = fileURLToPath(
  new URL('../../../../services/game-server/internal/auth/testdata/web-tokens.json', import.meta.url),
);

const INPUT = {
  secret: 'secreto-del-fixture-entre-lenguajes-0123456789',
  nowMs: 1_757_376_000_000,
  playerId: '5802b8b1-cdb1-5e7a-83f0-f32e243688bd',
  jti: '9b7a6c2e-1f3d-4e5a-8b9c-0d1e2f3a4b5c',
};

function decode(token: string): { header: unknown; payload: Record<string, unknown>; signature: string } {
  const [h, p, s] = token.split('.');
  return {
    header: JSON.parse(Buffer.from(h!, 'base64url').toString()),
    payload: JSON.parse(Buffer.from(p!, 'base64url').toString()) as Record<string, unknown>,
    signature: s!,
  };
}

describe('tokens', () => {
  it('el ticket de juego lleva exactamente los claims de ADR-010', () => {
    const t = decode(issueGameTicket(INPUT));
    expect(t.header).toEqual({ alg: 'HS256', typ: 'JWT' });
    expect(t.payload).toEqual({
      sub: INPUT.playerId,
      jti: INPUT.jti,
      aud: GAME_AUDIENCE,
      iat: INPUT.nowMs / 1000,
      exp: INPUT.nowMs / 1000 + TOKEN_TTL_SECONDS,
    });
    expect(TOKEN_TTL_SECONDS).toBe(60);
  });

  it('el token de fundación sólo cambia la audiencia', () => {
    const juego = decode(issueGameTicket(INPUT)).payload;
    const interno = decode(issueFoundingToken(INPUT)).payload;
    expect(interno.aud).toBe(INTERNAL_AUDIENCE);
    expect(INTERNAL_AUDIENCE).not.toBe(GAME_AUDIENCE);
    expect({ ...interno, aud: juego.aud }).toEqual(juego);
  });

  it('la firma es HMAC-SHA256 del secreto sobre cabecera y cuerpo', () => {
    const token = issueGameTicket(INPUT);
    const [h, p, s] = token.split('.');
    expect(s).toBe(createHmac('sha256', INPUT.secret).update(`${h}.${p}`).digest('base64url'));
    expect(issueGameTicket({ ...INPUT, secret: `${INPUT.secret}x` })).not.toBe(token);
  });

  it('produce los bytes exactos del fixture que verifica el Game Server', () => {
    const actual = {
      ...INPUT,
      gameTicket: issueGameTicket(INPUT),
      foundingToken: issueFoundingToken(INPUT),
    };
    if (process.env.EO_UPDATE_FIXTURES === '1' || !existsSync(FIXTURE)) {
      if (process.env.CI) throw new Error(`falta el fixture ${FIXTURE}`);
      mkdirSync(dirname(FIXTURE), { recursive: true });
      writeFileSync(FIXTURE, `${JSON.stringify(actual, null, 2)}\n`);
    }
    expect(JSON.parse(readFileSync(FIXTURE, 'utf8'))).toEqual(actual);
  });
});
