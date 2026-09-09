/**
 * Códigos de error estables del protocolo.
 *
 * Regla: la lógica de control (cliente y servidor) se basa SIEMPRE en `code`,
 * nunca en el texto de `message`, que es únicamente para humanos y logs.
 * Añadir un código es aditivo; renombrar o eliminar uno es un cambio incompatible
 * que exige una nueva versión de protocolo.
 */
import { z } from 'zod';

export const ErrorCode = z.enum([
  // --- Autenticación / autorización / transporte ---
  'UNAUTHORIZED',
  'FORBIDDEN',
  'INVALID_MESSAGE',
  'UNSUPPORTED_VERSION',
  'RATE_LIMITED',
  'MESSAGE_TOO_LARGE',

  // --- Unidades ---
  'UNIT_NOT_FOUND',
  'UNIT_NOT_OWNED',
  'UNIT_NOT_MOVABLE',
  'UNIT_DEAD',
  'UNIT_GARRISONED',

  // --- Movimiento / pathfinding ---
  'INVALID_TARGET',
  'TARGET_OUT_OF_BOUNDS',
  'TARGET_NOT_WALKABLE',
  'PATH_NOT_FOUND',
  'PATH_TOO_LONG',

  // --- Ciudad / diplomacia / economía ---
  'CITY_NOT_FOUND',
  'CITY_PROTECTED',
  'TREATY_REQUIRED',
  'POPULATION_LIMIT_REACHED',

  // --- Genéricos ---
  'INTERNAL_ERROR',
  'NOT_IMPLEMENTED',
]);
export type ErrorCode = z.infer<typeof ErrorCode>;

export const ERROR_CODES = ErrorCode.options;

/**
 * Códigos de cierre de WebSocket específicos de la aplicación (rango privado 4000-4999).
 * Ver ../../../docs/specs/websocket-protocol.md
 */
export const WS_CLOSE = {
  /** Mensaje malformado o que no valida contra el esquema. */
  INVALID_MESSAGE: 4400,
  /** No autenticado: se recibió un comando antes de un `session.hello` válido. */
  UNAUTHENTICATED: 4401,
  /** Autenticado pero sin permiso sobre el recurso. */
  FORBIDDEN: 4403,
  /** No se completó el handshake dentro del plazo. */
  HANDSHAKE_TIMEOUT: 4408,
  /** Se excedió el límite de tasa de mensajes. */
  RATE_LIMITED: 4429,
  /** Error interno del servidor; el cliente debe reconectar con backoff. */
  INTERNAL_ERROR: 4500,
} as const;

export type WsCloseCode = (typeof WS_CLOSE)[keyof typeof WS_CLOSE];
