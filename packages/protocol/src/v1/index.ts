/**
 * Protocolo WebSocket de Empires Online — versión 1.
 *
 * Este paquete es la FUENTE ÚNICA DE VERDAD del contrato cliente ↔ servidor.
 * El Game Server (Go) no redefine el contrato: consume el JSON Schema exportado
 * desde aquí en sus contract tests, de modo que una divergencia rompe la CI en
 * lugar de romper a los jugadores.
 * Ver ../../../docs/decisions/ADR-009-shared-protocol-package.md
 */
import { z } from 'zod';
import { ClientMessage } from './client.js';
import { ServerMessage } from './server.js';

export * from './common.js';
export * from './errors.js';
export * from './client.js';
export * from './server.js';

/** Límites del transporte. Deben coincidir con la configuración `EO_WS_*` del servidor. */
export const TRANSPORT_LIMITS = {
  /** Tamaño máximo de un mensaje entrante, en bytes (`EO_WS_MAX_MESSAGE_BYTES`). */
  maxMessageBytes: 16_384,
  /** Mensajes por segundo por conexión (`EO_WS_RATE_LIMIT_PER_SECOND`). */
  rateLimitPerSecond: 20,
  /** Ráfaga admitida (`EO_WS_RATE_LIMIT_BURST`). */
  rateLimitBurst: 40,
  /** Plazo para completar el handshake antes del cierre 4408. */
  handshakeTimeoutMs: 5_000,
  /** Cadencia de ping del servidor. */
  pingIntervalMs: 15_000,
  /** Silencio máximo tolerado antes de cerrar la conexión. */
  readTimeoutMs: 45_000,
} as const;

export type ParseResult<T> =
  | { ok: true; message: T }
  | { ok: false; code: 'INVALID_MESSAGE' | 'UNSUPPORTED_VERSION'; issues: string[] };

/** Extrae el campo `v` de un objeto desconocido sin asumir su forma. */
function extractVersion(value: unknown): number | undefined {
  if (typeof value !== 'object' || value === null) return undefined;
  const v = (value as Record<string, unknown>).v;
  return typeof v === 'number' ? v : undefined;
}

function formatIssues(error: z.ZodError): string[] {
  return error.issues.map((i) => `${i.path.join('.') || '<root>'}: ${i.message}`);
}

/**
 * Valida un mensaje entrante cliente → servidor.
 * Distingue explícitamente "versión no soportada" de "mensaje inválido" porque
 * el protocolo debe poder evolucionar sin que un cliente antiguo reciba un error
 * genérico e indepurable.
 */
export function parseClientMessage(value: unknown): ParseResult<ClientMessage> {
  const version = extractVersion(value);
  if (version !== undefined && version !== 1) {
    return { ok: false, code: 'UNSUPPORTED_VERSION', issues: [`v: versión ${version} no soportada`] };
  }
  const result = ClientMessage.safeParse(value);
  if (!result.success) {
    return { ok: false, code: 'INVALID_MESSAGE', issues: formatIssues(result.error) };
  }
  return { ok: true, message: result.data };
}

/** Valida un mensaje entrante servidor → cliente. */
export function parseServerMessage(value: unknown): ParseResult<ServerMessage> {
  const version = extractVersion(value);
  if (version !== undefined && version !== 1) {
    return { ok: false, code: 'UNSUPPORTED_VERSION', issues: [`v: versión ${version} no soportada`] };
  }
  const result = ServerMessage.safeParse(value);
  if (!result.success) {
    return { ok: false, code: 'INVALID_MESSAGE', issues: formatIssues(result.error) };
  }
  return { ok: true, message: result.data };
}
