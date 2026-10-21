/**
 * Hash de contraseñas con scrypt (ADR-018 §2.1), sin dependencias: `node:crypto`.
 *
 * Formato autodescrito, para poder subir el coste sin migrar:
 *
 *   scrypt$<log2 N>$<r>$<p>$<sal base64url>$<hash base64url>
 *
 * Coste: N = 2^15, r = 8, p = 3, uno de los perfiles equivalentes que recomienda
 * OWASP. Unos 32 MiB por cálculo.
 *
 * JavaScript plano a propósito: lo usan el servidor de Next.js y la siembra de
 * desarrollo, que es un script de Node.
 */
import { randomBytes, scrypt, timingSafeEqual } from 'node:crypto';

export const SCRYPT_LOG_N = 15;
export const SCRYPT_R = 8;
export const SCRYPT_P = 3;
const KEY_LEN = 32;
const SALT_LEN = 16;
// 128·N·r son exactamente 32 MiB, el límite por defecto de Node: se da margen.
const MAX_MEM = 64 * 1024 * 1024;

/**
 * @param {string} password
 * @param {Buffer} salt
 * @param {number} logN
 * @param {number} r
 * @param {number} p
 * @param {number} keyLen
 * @returns {Promise<Buffer>}
 */
function derive(password, salt, logN, r, p, keyLen) {
  return new Promise((resolve, reject) => {
    scrypt(password, salt, keyLen, { N: 2 ** logN, r, p, maxmem: MAX_MEM }, (err, key) =>
      err ? reject(err) : resolve(key),
    );
  });
}

/**
 * @param {string} password
 * @returns {Promise<string>}
 */
export async function hashPassword(password) {
  const salt = randomBytes(SALT_LEN);
  const key = await derive(password, salt, SCRYPT_LOG_N, SCRYPT_R, SCRYPT_P, KEY_LEN);
  return ['scrypt', SCRYPT_LOG_N, SCRYPT_R, SCRYPT_P, salt.toString('base64url'), key.toString('base64url')].join('$');
}

/**
 * Compara en tiempo constante. Un hash mal formado nunca verifica.
 *
 * @param {string} password
 * @param {string} stored
 * @returns {Promise<boolean>}
 */
export async function verifyPassword(password, stored) {
  const parts = stored.split('$');
  if (parts.length !== 6 || parts[0] !== 'scrypt') return false;
  const [, logN, r, p, saltText, keyText] = parts;
  const n = Number(logN);
  const rr = Number(r);
  const pp = Number(p);
  if (![n, rr, pp].every((v) => Number.isInteger(v) && v > 0) || n > 20) return false;
  const expected = Buffer.from(/** @type {string} */ (keyText), 'base64url');
  if (expected.length === 0) return false;
  const actual = await derive(password, Buffer.from(/** @type {string} */ (saltText), 'base64url'), n, rr, pp, expected.length);
  return timingSafeEqual(actual, expected);
}

/** @type {Promise<string> | null} */
let dummy = null;

/**
 * Un hash con el que comparar cuando el usuario no existe: el login tarda lo
 * mismo y responde lo mismo, y no revela qué nombres existen.
 *
 * @returns {Promise<string>}
 */
export function dummyHash() {
  dummy ??= hashPassword(randomBytes(24).toString('base64url'));
  return dummy;
}
