/**
 * Emisión del game ticket y del token de fundación (ADR-010, ADR-018 §2.4).
 *
 * JWT HS256 firmado con `node:crypto`, sin librería: el formato es diminuto y
 * lo fija, byte a byte, un fixture que verifica también el Game Server
 * (`tokens.test.ts` y `internal/auth`).
 *
 * Ninguna función de este módulo registra nada: un ticket en un log es una
 * sesión regalada durante 60 s (INV-PLAYER-007).
 */
import 'server-only';

import { createHmac } from 'node:crypto';

/** Destinatario del ticket de juego. */
export const GAME_AUDIENCE = 'game-server';
/** Destinatario del token de fundación. Disjunta de la anterior (INV-SEC-008). */
export const INTERNAL_AUDIENCE = 'game-server-internal';
/** Vida de ambos tokens. */
export const TOKEN_TTL_SECONDS = 60;

export interface TokenInput {
  readonly playerId: string;
  readonly secret: string;
  readonly nowMs: number;
  /** Identificador de un solo uso. Aleatorio en producción; fijo en el fixture. */
  readonly jti: string;
}

const HEADER = Buffer.from(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).toString('base64url');

function sign(audience: string, input: TokenInput): string {
  const iat = Math.floor(input.nowMs / 1000);
  // El orden de las claves es parte del fixture: no reordenar.
  const payload = Buffer.from(
    JSON.stringify({
      sub: input.playerId,
      jti: input.jti,
      aud: audience,
      iat,
      exp: iat + TOKEN_TTL_SECONDS,
    }),
  ).toString('base64url');
  const signature = createHmac('sha256', input.secret).update(`${HEADER}.${payload}`).digest('base64url');
  return `${HEADER}.${payload}.${signature}`;
}

/** Ticket para `session.hello`. */
export function issueGameTicket(input: TokenInput): string {
  return sign(GAME_AUDIENCE, input);
}

/** Token con el que apps/web autoriza al Game Server a fundar `playerId`. */
export function issueFoundingToken(input: TokenInput): string {
  return sign(INTERNAL_AUDIENCE, input);
}
