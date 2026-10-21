/**
 * Firma del game ticket y del token de fundación (ADR-010, ADR-018 §2.4).
 *
 * JWT HS256 firmado con `node:crypto`, sin librería: el formato es diminuto y
 * lo fija, byte a byte, un fixture que verifica también el Game Server
 * (`src/server/tokens.test.ts` y `internal/auth`).
 *
 * Vive aquí, fuera de `src/server`, para que lo usen también los comandos de
 * operación (`scripts/identity-sweep.mjs`), que corren sin Next.js. El servidor
 * lo importa a través de `src/server/tokens.ts`, que lo protege con
 * `server-only`.
 *
 * Ninguna función de este módulo registra nada: un ticket en un log es una
 * sesión regalada durante 60 s (INV-PLAYER-007).
 */
import { createHmac } from 'node:crypto';

/** Destinatario del ticket de juego. */
export const GAME_AUDIENCE = 'game-server';
/** Destinatario del token de fundación. Disjunta de la anterior (INV-SEC-008). */
export const INTERNAL_AUDIENCE = 'game-server-internal';
/** Vida de ambos tokens. */
export const TOKEN_TTL_SECONDS = 60;

const HEADER = Buffer.from(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).toString('base64url');

/**
 * @typedef {object} TokenInput
 * @property {string} playerId
 * @property {string} secret
 * @property {number} nowMs
 * @property {string} jti identificador de un solo uso: aleatorio en producción, fijo en el fixture
 */

/**
 * @param {string} audience
 * @param {TokenInput} input
 * @returns {string}
 */
function sign(audience, input) {
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

/**
 * Ticket para `session.hello`.
 * @param {TokenInput} input
 */
export function issueGameTicket(input) {
  return sign(GAME_AUDIENCE, input);
}

/**
 * Token con el que apps/web autoriza al Game Server a fundar —o consultar— `playerId`.
 * @param {TokenInput} input
 */
export function issueFoundingToken(input) {
  return sign(INTERNAL_AUDIENCE, input);
}
