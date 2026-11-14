/**
 * Mensajes CLIENTE → SERVIDOR del protocolo v1.
 *
 * Principio rector: el cliente envía INTENCIONES, nunca estado. Ningún mensaje de
 * este archivo puede transportar posición final, HP, recursos, ownership, cooldowns,
 * ETAs ni rutas: todo eso lo determina el servidor.
 * Ver ../../../docs/decisions/ADR-002-authoritative-server.md
 */
import { z } from 'zod';
import {
  BuildingType,
  EntityId,
  MESSAGE_MAX_CHARS,
  PROTOCOL_VERSION,
  ResourceAmount,
  ResourceType,
  Tile,
  TreatyType,
  TrainableUnitType,
  Uuid,
  WorldCoordinate,
} from './common.js';

/**
 * Los tipos que un jugador puede proponer: los tres desde el comercio (SPEC-DIPLOMACY
 * Q1, precisada por SPEC-TRADE Q1).
 */
const ProposableTreatyType = TreatyType;

/**
 * Las líneas de un lado de una oferta: de una a tantas como recursos haya. Que un
 * recurso no se repita ni esté en los dos lados lo decide el servidor (INVALID_TARGET).
 */
const TradeLines = z.array(ResourceAmount.strict()).min(1).max(ResourceType.options.length);

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
    type: TrainableUnitType,
  }).strict(),
}).strict();

/**
 * Encarga el avance de una ciudad propia a la era siguiente (SPEC-ECON-STONE §4.4).
 * Sólo la ciudad: la era, el coste en madera y piedra y el tiempo los decide el
 * servidor.
 */
export const CityAdvance = z.object({
  v: V,
  type: z.literal('city.advance'),
  requestId: Uuid,
  payload: z.object({
    cityId: EntityId,
  }).strict(),
}).strict();

/**
 * Encarga al taller de una ciudad propia una elaboración del bien de su
 * civilización (SPEC-CIVILIZATION-GOODS §4.3). Sólo la ciudad: el bien, la
 * cantidad, el coste y el tiempo los decide el servidor.
 */
export const CityCraft = z.object({
  v: V,
  type: z.literal('city.craft'),
  requestId: Uuid,
  payload: z.object({
    cityId: EntityId,
  }).strict(),
}).strict();

/**
 * Ordena a una unidad militar propia atacar a una unidad de otro jugador: la
 * persigue y la golpea hasta que muera o el enfrentamiento termine. Es la primera
 * acción hostil (ADR-016). El daño, el alcance y el botín los decide el servidor.
 * Ver ../../../docs/specs/combat-raid.md
 */
export const UnitAttack = z.object({
  v: V,
  type: z.literal('unit.attack'),
  requestId: Uuid,
  payload: z.object({
    unitId: EntityId,
    targetUnitId: EntityId,
  }).strict(),
}).strict();

/**
 * Ordena a un guerrero propio saquear una ciudad de otro jugador: va hasta su
 * muralla y roba madera del almacén golpe a golpe. Sólo quién y qué ciudad: el
 * tile de llegada, lo robado y el suelo los decide el servidor.
 * Ver ../../../docs/specs/combat-pillage.md
 */
export const UnitPillage = z.object({
  v: V,
  type: z.literal('unit.pillage'),
  requestId: Uuid,
  payload: z.object({
    unitId: EntityId,
    cityId: EntityId,
  }).strict(),
}).strict();

/**
 * Ordena a un guerrero propio derribar una casa de una ciudad de otro jugador: va
 * hasta un tile junto a ella y la golpea hasta destruirla. Sólo quién y qué casa:
 * el tile de llegada, la vida y el daño los decide el servidor.
 * Ver ../../../docs/specs/combat-siege.md
 */
export const UnitDemolish = z.object({
  v: V,
  type: z.literal('unit.demolish'),
  requestId: Uuid,
  payload: z.object({
    unitId: EntityId,
    buildingId: EntityId,
  }).strict(),
}).strict();

/**
 * Un `WARRIOR` reclama el territorio del tile en que está (SPEC-TERRITORY-CONQUEST
 * §4.1): se planta y, si aguanta el plazo, el territorio pasa a su dueño. Sólo qué
 * guerrero: el territorio, el plazo y si se puede los decide el servidor.
 * Ver ../../../docs/specs/territory-conquest.md
 */
export const UnitClaim = z.object({
  v: V,
  type: z.literal('unit.claim'),
  requestId: Uuid,
  payload: z.object({
    unitId: EntityId,
  }).strict(),
}).strict();

/**
 * Propone un tratado a otro jugador (SPEC-DIPLOMACY). La guarnición la fija el
 * tipo y el servidor decide si se puede. Ver ../../../docs/specs/diplomacy.md
 */
export const TreatyPropose = z.object({
  v: V,
  type: z.literal('treaty.propose'),
  requestId: Uuid,
  payload: z.object({
    playerId: Uuid,
    type: ProposableTreatyType,
  }).strict(),
}).strict();

/** Acepta o rechaza una oferta recibida. */
export const TreatyRespond = z.object({
  v: V,
  type: z.literal('treaty.respond'),
  requestId: Uuid,
  payload: z.object({
    treatyId: EntityId,
    accept: z.boolean(),
  }).strict(),
}).strict();

/** Retira la oferta propia o rompe un tratado vigente, que entra en preaviso. */
export const TreatyCancel = z.object({
  v: V,
  type: z.literal('treaty.cancel'),
  requestId: Uuid,
  payload: z.object({
    treatyId: EntityId,
  }).strict(),
}).strict();

/**
 * Ofrece un trato a otro jugador (SPEC-TRADE): «doy esto, pido aquello». El servidor
 * decide si hay tratado, si alcanza el almacén y qué se mueve. Ver
 * ../../../docs/specs/trade.md
 */
export const TradePropose = z.object({
  v: V,
  type: z.literal('trade.propose'),
  requestId: Uuid,
  payload: z.object({
    playerId: Uuid,
    give: TradeLines,
    want: TradeLines,
  }).strict(),
}).strict();

/** Acepta o rechaza una oferta de comercio recibida. */
export const TradeRespond = z.object({
  v: V,
  type: z.literal('trade.respond'),
  requestId: Uuid,
  payload: z.object({
    offerId: EntityId,
    accept: z.boolean(),
  }).strict(),
}).strict();

/** Retira la oferta de comercio propia. */
export const TradeCancel = z.object({
  v: V,
  type: z.literal('trade.cancel'),
  requestId: Uuid,
  payload: z.object({
    offerId: EntityId,
  }).strict(),
}).strict();

/**
 * Pide una página del registro (SPEC-EVENT-LOG): la más reciente, o la de los
 * eventos anteriores a `beforeId`. El jugador es siempre el de la sesión
 * (INV-SEC-009). Ver ../../../docs/specs/event-log.md
 */
export const EventLogFetch = z.object({
  v: V,
  type: z.literal('event_log.fetch'),
  requestId: Uuid,
  payload: z.object({
    beforeId: EntityId.optional(),
  }).strict(),
}).strict();

/**
 * Da por visto el registro hasta `upToId`. El servidor no pasa del tope marcable de
 * la última página que sirvió a esta sesión, y el cursor nunca retrocede
 * (RN-LOG-004, INV-PERSIST-006).
 */
export const EventLogSeen = z.object({
  v: V,
  type: z.literal('event_log.seen'),
  requestId: Uuid,
  payload: z.object({
    upToId: EntityId,
  }).strict(),
}).strict();

/**
 * Escribe a otro jugador (SPEC-MESSAGES §4.2). El remitente es siempre el jugador de la
 * sesión (RN-MSG-001). El esquema acota el texto en unidades UTF-16; el servidor lo
 * recorta y lo cuenta en caracteres (RN-MSG-003). Ver ../../../docs/specs/messages.md
 */
export const MessageSend = z.object({
  v: V,
  type: z.literal('message.send'),
  requestId: Uuid,
  payload: z.object({
    toPlayerId: Uuid,
    body: z.string().min(1).max(2 * MESSAGE_MAX_CHARS),
  }).strict(),
}).strict();

/** Pide la bandeja: las conversaciones del jugador y sus bloqueados (SPEC-MESSAGES §4.3). */
export const MessageList = z.object({
  v: V,
  type: z.literal('message.list'),
  requestId: Uuid,
  payload: z.object({}).strict(),
}).strict();

/**
 * Pide una página de la conversación con `otherPlayerId`: la más reciente, o la de los
 * mensajes anteriores a `beforeId` (SPEC-MESSAGES §4.3).
 */
export const MessageFetch = z.object({
  v: V,
  type: z.literal('message.fetch'),
  requestId: Uuid,
  payload: z.object({
    otherPlayerId: Uuid,
    beforeId: EntityId.optional(),
  }).strict(),
}).strict();

/**
 * Marca leída la conversación hasta `upToId`, sin pasar de lo que esta sesión recibió de
 * ella ni retroceder nunca (RN-MSG-008, INV-PERSIST-007).
 */
export const MessageRead = z.object({
  v: V,
  type: z.literal('message.read'),
  requestId: Uuid,
  payload: z.object({
    otherPlayerId: Uuid,
    upToId: EntityId,
  }).strict(),
}).strict();

/** Bloquea o desbloquea los mensajes con otro jugador (SPEC-MESSAGES §4.4). */
export const MessageBlock = z.object({
  v: V,
  type: z.literal('message.block'),
  requestId: Uuid,
  payload: z.object({
    playerId: Uuid,
    blocked: z.boolean(),
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
  CityAdvance,
  UnitAttack,
  UnitPillage,
  UnitDemolish,
  UnitClaim,
  CityCraft,
  TreatyPropose,
  TreatyRespond,
  TreatyCancel,
  TradePropose,
  TradeRespond,
  TradeCancel,
  EventLogFetch,
  EventLogSeen,
  MessageSend,
  MessageList,
  MessageFetch,
  MessageRead,
  MessageBlock,
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
export type CityAdvance = z.infer<typeof CityAdvance>;
export type CityCraft = z.infer<typeof CityCraft>;
export type UnitAttack = z.infer<typeof UnitAttack>;
export type UnitPillage = z.infer<typeof UnitPillage>;
export type UnitDemolish = z.infer<typeof UnitDemolish>;
export type UnitClaim = z.infer<typeof UnitClaim>;
export type TreatyPropose = z.infer<typeof TreatyPropose>;
export type TreatyRespond = z.infer<typeof TreatyRespond>;
export type TreatyCancel = z.infer<typeof TreatyCancel>;
export type TradePropose = z.infer<typeof TradePropose>;
export type TradeRespond = z.infer<typeof TradeRespond>;
export type TradeCancel = z.infer<typeof TradeCancel>;
export type EventLogFetch = z.infer<typeof EventLogFetch>;
export type EventLogSeen = z.infer<typeof EventLogSeen>;
export type MessageSend = z.infer<typeof MessageSend>;
export type MessageList = z.infer<typeof MessageList>;
export type MessageFetch = z.infer<typeof MessageFetch>;
export type MessageRead = z.infer<typeof MessageRead>;
export type MessageBlock = z.infer<typeof MessageBlock>;

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
  'city.advance',
  'unit.attack',
  'unit.pillage',
  'unit.demolish',
  'treaty.propose',
  'treaty.respond',
  'treaty.cancel',
  'trade.propose',
  'trade.respond',
  'trade.cancel',
  'event_log.fetch',
  'event_log.seen',
  'unit.claim',
  'city.craft',
  'message.send',
  'message.list',
  'message.fetch',
  'message.read',
  'message.block',
] as const;

export type ClientMessageType = (typeof CLIENT_MESSAGE_TYPES)[number];

/** Mensajes que el servidor acepta ANTES de que la sesión esté autenticada. */
export const PRE_AUTH_MESSAGE_TYPES: readonly ClientMessageType[] = ['session.hello'];

/** Referencia no usada directamente aquí, exportada para consumidores del paquete. */
export { WorldCoordinate };
