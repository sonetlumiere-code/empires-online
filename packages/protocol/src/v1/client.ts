/**
 * Mensajes CLIENTE → SERVIDOR del protocolo v1.
 *
 * Principio rector: el cliente envía INTENCIONES, nunca estado. Ningún mensaje de
 * este archivo puede transportar posición final, HP, recursos, ownership, cooldowns,
 * ETAs ni rutas: todo eso lo determina el servidor.
 * Ver ../../../docs/decisions/ADR-002-authoritative-server.md
 */
import { z } from 'zod';
import { BuildingType, EntityId, PROTOCOL_VERSION, Tile, UnitType, Uuid, WorldCoordinate } from './common.js';

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

/**
 * Guarnece una unidad propia en una ciudad: la propia, o la de un jugador con el
 * que haya un tratado ACTIVE que lo permita. La unidad tiene que estar IDLE y
 * junto a la zona urbana; el servidor decide si se puede.
 * Ver ../../../docs/specs/garrison.md
 */
export const UnitGarrison = z.object({
  v: V,
  type: z.literal('unit.garrison'),
  requestId: Uuid,
  payload: z.object({
    unitId: EntityId,
    cityId: EntityId,
  }).strict(),
}).strict();

/**
 * Saca una unidad propia de la ciudad en la que está guarnecida. El tile de
 * salida lo elige el servidor.
 */
export const UnitUngarrison = z.object({
  v: V,
  type: z.literal('unit.ungarrison'),
  requestId: Uuid,
  payload: z.object({
    unitId: EntityId,
  }).strict(),
}).strict();

/**
 * Pone a un aldeano propio a recolectar madera en un tile de bosque: viajes
 * bosque → ciudad hasta que otra orden lo interrumpa. El cliente dice dónde; el
 * servidor decide la ruta, los tiempos y las cantidades.
 * Ver ../../../docs/specs/economy-wood.md
 */
export const UnitGather = z.object({
  v: V,
  type: z.literal('unit.gather'),
  requestId: Uuid,
  payload: z.object({
    unitId: EntityId,
    target: Tile,
  }).strict(),
}).strict();

/**
 * Encarga un edificio en un tile de la zona urbana de una ciudad propia. El coste
 * y el tiempo los decide el servidor.
 */
export const CityBuild = z.object({
  v: V,
  type: z.literal('city.build'),
  requestId: Uuid,
  payload: z.object({
    cityId: EntityId,
    type: BuildingType,
    x: WorldCoordinate,
    y: WorldCoordinate,
  }).strict(),
}).strict();

/** Encarga el entrenamiento de una unidad en una ciudad propia. */
export const CityTrain = z.object({
  v: V,
  type: z.literal('city.train'),
  requestId: Uuid,
  payload: z.object({
    cityId: EntityId,
    type: UnitType,
  }).strict(),
}).strict();

/** Unión discriminada de todos los mensajes cliente → servidor de la v1. */
export const ClientMessage = z.discriminatedUnion('type', [
  SessionHello,
  SessionPing,
  SessionView,
  UnitMove,
  UnitCancelMove,
  UnitGarrison,
  UnitUngarrison,
  UnitGather,
  CityBuild,
  CityTrain,
]);
export type ClientMessage = z.infer<typeof ClientMessage>;

export type SessionHello = z.infer<typeof SessionHello>;
export type SessionPing = z.infer<typeof SessionPing>;
export type SessionView = z.infer<typeof SessionView>;
export type UnitMove = z.infer<typeof UnitMove>;
export type UnitCancelMove = z.infer<typeof UnitCancelMove>;
export type UnitGarrison = z.infer<typeof UnitGarrison>;
export type UnitUngarrison = z.infer<typeof UnitUngarrison>;
export type UnitGather = z.infer<typeof UnitGather>;
export type CityBuild = z.infer<typeof CityBuild>;
export type CityTrain = z.infer<typeof CityTrain>;

export const CLIENT_MESSAGE_TYPES = [
  'session.hello',
  'session.ping',
  'session.view',
  'unit.move',
  'unit.cancel_move',
  'unit.garrison',
  'unit.ungarrison',
  'unit.gather',
  'city.build',
  'city.train',
] as const;

export type ClientMessageType = (typeof CLIENT_MESSAGE_TYPES)[number];

/** Mensajes que el servidor acepta ANTES de que la sesión esté autenticada. */
export const PRE_AUTH_MESSAGE_TYPES: readonly ClientMessageType[] = ['session.hello'];

/** Referencia no usada directamente aquí, exportada para consumidores del paquete. */
export { WorldCoordinate };
