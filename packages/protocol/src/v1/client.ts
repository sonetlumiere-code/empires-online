/**
 * Mensajes CLIENTE → SERVIDOR del protocolo v1.
 *
 * Principio rector: el cliente envía INTENCIONES, nunca estado. Ningún mensaje de
 * este archivo puede transportar posición final, HP, recursos, ownership, cooldowns,
 * ETAs ni rutas: todo eso lo determina el servidor.
 * Ver ../../../docs/decisions/ADR-002-authoritative-server.md
 */
import { z } from 'zod';
import { EntityId, PROTOCOL_VERSION, Tile, Uuid, WorldCoordinate } from './common.js';

/** Versión del protocolo presente en todo mensaje. */
const V = z.literal(PROTOCOL_VERSION);

/**
 * Handshake. DEBE ser el primer mensaje de la conexión, dentro del plazo de
 * handshake, o el servidor cierra con 4408. El `ticket` es un JWT efímero emitido
 * por el frontend; el servidor lo verifica y consume su `jti` una única vez.
 * Ver ../../../docs/decisions/ADR-010-authentication-game-ticket.md
 */
export const SessionHello = z.object({
  v: V,
  type: z.literal('session.hello'),
  requestId: Uuid,
  payload: z.object({
    ticket: z.string().min(1).max(4096),
    /** Versión del cliente, sólo para diagnóstico y telemetría. */
    clientVersion: z.string().max(64).optional(),
  }).strict(),
}).strict();

/** Latido de la conexión. El servidor responde con `session.pong`. */
export const SessionPing = z.object({
  v: V,
  type: z.literal('session.ping'),
  requestId: Uuid,
  payload: z.object({
    /** Reloj del cliente, para que éste estime su offset contra el servidor. */
    clientTimeMs: z.number().int().min(0),
  }).strict(),
}).strict();

/**
 * Actualiza el centro del área de interés del jugador (la cámara).
 * Sujeto a rate limit. No es una orden de juego: no muta el mundo.
 */
export const SessionView = z.object({
  v: V,
  type: z.literal('session.view'),
  requestId: Uuid,
  payload: z.object({
    center: Tile,
  }).strict(),
}).strict();

/**
 * Ordena a una unidad propia moverse a un tile destino.
 *
 * El cliente envía ÚNICAMENTE el destino. No envía ruta, ni waypoints, ni tiempo
 * estimado: el pathfinding y la temporización son responsabilidad exclusiva del servidor.
 */
export const UnitMove = z.object({
  v: V,
  type: z.literal('unit.move'),
  requestId: Uuid,
  payload: z.object({
    unitId: EntityId,
    target: Tile,
  }).strict(),
}).strict();

/** Cancela el movimiento activo de una unidad propia. */
export const UnitCancelMove = z.object({
  v: V,
  type: z.literal('unit.cancel_move'),
  requestId: Uuid,
  payload: z.object({
    unitId: EntityId,
  }).strict(),
}).strict();

/** Unión discriminada de todos los mensajes cliente → servidor de la v1. */
export const ClientMessage = z.discriminatedUnion('type', [
  SessionHello,
  SessionPing,
  SessionView,
  UnitMove,
  UnitCancelMove,
]);
export type ClientMessage = z.infer<typeof ClientMessage>;

export type SessionHello = z.infer<typeof SessionHello>;
export type SessionPing = z.infer<typeof SessionPing>;
export type SessionView = z.infer<typeof SessionView>;
export type UnitMove = z.infer<typeof UnitMove>;
export type UnitCancelMove = z.infer<typeof UnitCancelMove>;

export const CLIENT_MESSAGE_TYPES = [
  'session.hello',
  'session.ping',
  'session.view',
  'unit.move',
  'unit.cancel_move',
] as const;

export type ClientMessageType = (typeof CLIENT_MESSAGE_TYPES)[number];

/** Mensajes que el servidor acepta ANTES de que la sesión esté autenticada. */
export const PRE_AUTH_MESSAGE_TYPES: readonly ClientMessageType[] = ['session.hello'];

/** Referencia no usada directamente aquí, exportada para consumidores del paquete. */
export { WorldCoordinate };
