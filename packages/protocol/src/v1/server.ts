/**
 * Mensajes SERVIDOR → CLIENTE del protocolo v1.
 *
 * Todo mensaje lleva `seq`, un contador monótono por conexión: el cliente descarta
 * cualquier mensaje cuyo `seq` sea menor o igual al último procesado. `ts` es el
 * reloj del servidor en epoch ms y es la única referencia temporal fiable.
 */
import { z } from 'zod';
import {
  ActiveMovement,
  BuildingStatus,
  BuildingView,
  ChunkRef,
  ChunkTerrain,
  CityView,
  EntityId,
  EpochMs,
  EventLogEntry,
  EventLogNoticeKind,
  AdvanceView,
  CraftView,
  EraCode,
  ForestView,
  HillView,
  PROTOCOL_VERSION,
  ResourceType,
  TreatyView,
  TradeOfferView,
  CaravanView,
  MessageView,
  ConversationView,
  BlockedPlayerView,
  ResourceAmount,
  PresenceState,
  ProductionView,
  Stocks,
  TerritoryView,
  Tile,
  UnitStatus,
  UnitType,
  UnitView,
  Uuid,
  WorldCoordinate,
} from './common.js';
import { ErrorCode } from './errors.js';

const V = z.literal(PROTOCOL_VERSION);
/** Contador monótono por conexión. */
const Seq = z.number().int().min(0);

/** Campos comunes del envelope servidor → cliente. */
const serverEnvelope = {
  v: V,
  seq: Seq,
  ts: EpochMs,
  /** Presente si el mensaje responde a un comando concreto del cliente. */
  requestId: Uuid.optional(),
};

// ─────────────────────────────────────────────────────────────
// Sesión
// ─────────────────────────────────────────────────────────────

/** Confirma el handshake. A partir de aquí la sesión acepta comandos. */
export const SessionWelcome = z.object({
  ...serverEnvelope,
  type: z.literal('session.welcome'),
  payload: z.object({
    sessionId: Uuid,
    playerId: Uuid,
    serverTimeMs: EpochMs,
    /** Duración del tick en ms: el cliente la usa para dimensionar su interpolación. */
    tickDurationMs: z.number().int().min(1),
    /** Cadencia con la que el cliente debe enviar `session.ping`. */
    heartbeatIntervalMs: z.number().int().min(1000),
    world: z.object({
      width: z.number().int().min(1),
      height: z.number().int().min(1),
      chunkSize: z.number().int().min(1),
    }),
  }),
});

export const SessionPong = z.object({
  ...serverEnvelope,
  type: z.literal('session.pong'),
  payload: z.object({
    /** Eco exacto del `clientTimeMs` recibido, para estimar RTT y offset de reloj. */
    clientTimeMs: z.number().int().min(0),
    serverTimeMs: EpochMs,
  }),
});

/**
 * Error de aplicación. No implica cierre de la conexión: los cierres usan
 * los códigos WS_CLOSE. `code` es lo único sobre lo que se programa lógica.
 */
export const SystemError = z.object({
  ...serverEnvelope,
  type: z.literal('system.error'),
  payload: z.object({
    code: ErrorCode,
    message: z.string().max(512),
    details: z.record(z.string(), z.unknown()).optional(),
  }),
});

// ─────────────────────────────────────────────────────────────
// Sincronización del mundo: snapshot + deltas
// ─────────────────────────────────────────────────────────────

/**
 * Estado completo del ÁREA DE INTERÉS del jugador. Se envía al conectar y tras
 * cada reconexión. Nunca contiene el mundo entero.
 * Ver ../../../docs/architecture/networking.md
 */
export const WorldSnapshot = z.object({
  ...serverEnvelope,
  type: z.literal('world.snapshot'),
  payload: z.object({
    serverTimeMs: EpochMs,
    tick: z.number().int().min(0),
    /** Chunks a los que la sesión queda suscrita. */
    chunks: z.array(ChunkRef),
    /** Terreno de esos chunks. Puede omitirse si el cliente ya los tiene cacheados. */
    terrain: z.array(ChunkTerrain),
    units: z.array(UnitView),
    cities: z.array(CityView),
    territories: z.array(TerritoryView),
    /** Edificios de esos chunks. */
    buildings: z.array(BuildingView),
    /**
     * Tiles de bosque de esos chunks que no están llenos (SPEC-ECON-FOREST). Opcional:
     * aditivo en v1 (R-VER-2); sin él, todo el bosque está lleno.
     */
    forests: z.array(ForestView).optional(),
    /**
     * Tiles de colina de esos chunks que no están llenos (SPEC-ECON-STONE). Opcional,
     * como `forests`: sin él, toda la colina está llena.
     */
    hills: z.array(HillView).optional(),
    /**
     * Tratados PROPOSED y ACTIVE del jugador que recibe el snapshot (SPEC-DIPLOMACY).
     * Opcional: aditivo en v1; como todo el snapshot, reemplaza la lista del cliente.
     */
    treaties: z.array(TreatyView).optional(),
    /**
     * Ofertas de comercio PROPOSED del jugador que recibe el snapshot (SPEC-TRADE §6).
     * Opcional: aditivo en v1; como los tratados, reemplaza la lista del cliente.
     */
    tradeOffers: z.array(TradeOfferView).optional(),
    /**
     * Caravanas en camino que salen de la ciudad del jugador, van a ella o son suyas
     * (SPEC-CARAVANS §6). Opcional: aditivo en v1; reemplaza la lista del cliente.
     */
    caravans: z.array(CaravanView).optional(),
  }),
});

/** Una entidad entra en el área de interés (o acaba de crearse). */
export const EntitySpawn = z.object({
  ...serverEnvelope,
  type: z.literal('entity.spawn'),
  payload: z.object({
    unit: UnitView,
  }),
});

/**
 * Cambio parcial de una entidad ya conocida. Todos los campos salvo `id` son
 * opcionales: sólo se transmite lo que cambió.
 */
export const EntityUpdate = z.object({
  ...serverEnvelope,
  type: z.literal('entity.update'),
  payload: z.object({
    id: EntityId,
    x: WorldCoordinate.optional(),
    y: WorldCoordinate.optional(),
    hp: z.number().int().min(0).optional(),
    status: UnitStatus.optional(),
    cityId: EntityId.nullable().optional(),
    movement: ActiveMovement.nullable().optional(),
    garrisonCityId: EntityId.nullable().optional(),
    /** Lo que lleva encima; 0 cuando lo deposita o lo pierde. */
    carrying: z.number().int().min(0).optional(),
    /** El recurso de `carrying` cuando lo lleva una recolección (SPEC-ECON-STONE §6). */
    carryingResource: ResourceType.optional(),
  }),
});

/** Una entidad sale del área de interés, muere o deja de existir. */
export const EntityDespawn = z.object({
  ...serverEnvelope,
  type: z.literal('entity.despawn'),
  payload: z.object({
    id: EntityId,
    reason: z.enum(['OUT_OF_INTEREST', 'DEAD', 'GARRISONED', 'HIDDEN', 'REMOVED']),
  }),
});

/** Cambio en una ciudad visible (incluye transiciones de presencia y protección). */
export const CityUpdate = z.object({
  ...serverEnvelope,
  type: z.literal('city.update'),
  payload: z.object({
    id: EntityId,
    name: z.string().min(1).max(64).optional(),
    population: z.number().int().min(0).optional(),
    populationLimit: z.number().int().min(0).optional(),
    presenceState: PresenceState.optional(),
    protectionUntilMs: EpochMs.nullable().optional(),
    /** Stock nuevo de la ciudad: tras un depósito, un encargo o una devolución. */
    stocks: Stocks.optional(),
    /** Entrenamiento en curso; `null` cuando termina. */
    production: ProductionView.nullable().optional(),
    /** Era nueva de la ciudad al terminar un avance (SPEC-ECON-STONE RN-ECON-027). */
    era: EraCode.optional(),
    /** Avance de era: empieza (vista) o termina (`null`). */
    advance: AdvanceView.nullable().optional(),
    /** Elaboración del taller: empieza (vista) o termina (`null`) (SPEC-CIVILIZATION-GOODS). */
    craft: CraftView.nullable().optional(),
    /**
     * Instante del último golpe de saqueo sobre la ciudad. Sólo lo recibe su
     * dueño, para avisarle (SPEC-COMBAT-PILLAGE RN-COMBAT-028).
     */
    pillagedAtMs: EpochMs.optional(),
    /**
     * Instante del último golpe de asedio sobre una casa de la ciudad. Sólo lo
     * recibe su dueño, para avisarle (SPEC-COMBAT-SIEGE RN-COMBAT-038).
     */
    besiegedAtMs: EpochMs.optional(),
  }),
});

/** Cambio en el control de un territorio visible. */
export const TerritoryUpdate = z.object({
  ...serverEnvelope,
  type: z.literal('territory.update'),
  payload: z.object({
    territory: TerritoryView,
  }),
});

// ─────────────────────────────────────────────────────────────
// Edificios
// ─────────────────────────────────────────────────────────────

/** Un edificio aparece en el área de interés: recién encargado o al entrar en vista. */
export const BuildingSpawn = z.object({
  ...serverEnvelope,
  type: z.literal('building.spawn'),
  payload: z.object({
    building: BuildingView,
  }),
});

/**
 * Cambio de un edificio conocido: que se completa, o su vida en un asedio
 * (`hp`, SPEC-COMBAT-SIEGE), con su estado de siempre.
 */
export const BuildingUpdate = z.object({
  ...serverEnvelope,
  type: z.literal('building.update'),
  payload: z.object({
    id: EntityId,
    status: BuildingStatus,
    hp: z.number().int().min(0).optional(),
  }),
});

/**
 * Un edificio derribado por un asedio: el cliente lo quita, como `entity.despawn`
 * quita una unidad muerta (SPEC-COMBAT-SIEGE §6). Al chunk de la casa.
 */
export const BuildingDestroyed = z.object({
  ...serverEnvelope,
  type: z.literal('building.destroyed'),
  payload: z.object({
    id: EntityId,
    cityId: EntityId,
  }),
});

// ─────────────────────────────────────────────────────────────
// Bosque
// ─────────────────────────────────────────────────────────────

/**
 * Cambió la madera de un tile de bosque: una carga lo vació un poco, o volvió a crecer.
 * Con `remaining` igual a la capacidad y `regrowsAtMs` nulo, el tile está lleno.
 */
export const ForestUpdate = z.object({
  ...serverEnvelope,
  type: z.literal('forest.update'),
  payload: ForestView,
});

/**
 * Cambió la piedra de un tile de colina: una carga la gastó, o volvió a crecer
 * (SPEC-ECON-STONE §6). Con `remaining` igual a la capacidad y `regrowsAtMs` nulo, el
 * tile está lleno.
 */
export const HillUpdate = z.object({
  ...serverEnvelope,
  type: z.literal('hill.update'),
  payload: HillView,
});

/**
 * Cambió un tratado del jugador: una oferta nueva, una respuesta, un preaviso o su
 * fin. Lo reciben sólo los dos firmantes, cada uno con su vista (SPEC-DIPLOMACY §6).
 */
export const TreatyUpdate = z.object({
  ...serverEnvelope,
  type: z.literal('treaty.update'),
  payload: z.object({ treaty: TreatyView }),
});

/**
 * Cambió una oferta de comercio del jugador: una nueva, una respuesta, una retirada o
 * su caducidad. La reciben sólo sus dos jugadores, cada uno con su vista (SPEC-TRADE §6).
 */
export const TradeUpdate = z.object({
  ...serverEnvelope,
  type: z.literal('trade.update'),
  payload: z.object({ offer: TradeOfferView }),
});

/**
 * Cambió una caravana del jugador: salió, la capturaron, llegó o se perdió. La reciben
 * sólo sus implicados —dueño, quien la mandó, el destinatario original y el captor—,
 * con la carga (SPEC-CARAVANS §6, RN-CARAVAN-009).
 */
export const CaravanUpdate = z.object({
  ...serverEnvelope,
  type: z.literal('caravan.update'),
  payload: z.object({ caravan: CaravanView }),
});

/**
 * Una página del registro, en respuesta a `event_log.fetch` y sólo para la sesión que
 * la pidió (SPEC-EVENT-LOG §6). `seenUpToId` es el cursor de lo visto al servirla;
 * `nextBeforeId`, el `beforeId` de la página siguiente, `null` si no hay más.
 */
export const EventLogPage = z.object({
  ...serverEnvelope,
  type: z.literal('event_log.page'),
  payload: z.object({
    entries: z.array(EventLogEntry),
    seenUpToId: z.number().int().min(0).max(Number.MAX_SAFE_INTEGER),
    nextBeforeId: EntityId.nullable(),
  }),
});

/**
 * Un aviso en vivo (SPEC-LIVE-NOTICES §6): algo que les pasó a las unidades del jugador
 * fuera de su vista, en el tick en que pasó. Lo reciben todas sus sesiones, sin
 * `requestId`. No es una entrada del registro: no tiene `id`, no mueve el cursor y el
 * cliente no lo mete en la lista (RN-NOTICE-004, ADR-033).
 */
export const EventLogNotice = z.object({
  ...serverEnvelope,
  type: z.literal('event_log.notice'),
  payload: z.object({
    kind: EventLogNoticeKind,
    /** El instante del suceso en la simulación. */
    atMs: EpochMs,
    otherPlayerId: Uuid,
    /** La ciudad del otro jugador, si la tiene. */
    otherCityId: EntityId.nullable(),
    otherCityName: z.string().min(1).max(64).nullable(),
    /** En las muertes: qué tipo de unidad murió. */
    unitType: UnitType.optional(),
    /** En el saqueo: la madera de ese golpe. */
    amount: z.number().int().min(0).optional(),
    /** Dónde ocurrió. */
    x: WorldCoordinate.optional(),
    y: WorldCoordinate.optional(),
    /** En la conquista: el territorio (SPEC-TERRITORY-CONQUEST §6). */
    territoryId: EntityId.optional(),
    territoryName: z.string().min(1).max(64).optional(),
    /** En las caravanas (SPEC-CARAVANS §6): lo que llevaba. */
    cargo: z.array(ResourceAmount).optional(),
  }),
});

// ─────────────────────────────────────────────────────────────
// Movimiento
// ─────────────────────────────────────────────────────────────

/** El comando `unit.move` fue aceptado. El movimiento real llega en `unit.movement.started`. */
export const UnitMoveAccepted = z.object({
  ...serverEnvelope,
  type: z.literal('unit.move.accepted'),
  payload: z.object({
    unitId: EntityId,
    movementId: EntityId,
  }),
});

/** El comando `unit.move` fue rechazado. `code` explica por qué. */
export const UnitMoveRejected = z.object({
  ...serverEnvelope,
  type: z.literal('unit.move.rejected'),
  payload: z.object({
    unitId: EntityId,
    code: ErrorCode,
    message: z.string().max(512),
  }),
});

/**
 * Un movimiento comenzó. Transporta la polilínea temporizada COMPLETA para que el
 * cliente interpole sin sondear al servidor. Es un hecho consumado, no una promesa:
 * la posición autoritativa en cualquier instante deriva exactamente de estos datos.
 */
export const UnitMovementStarted = z.object({
  ...serverEnvelope,
  type: z.literal('unit.movement.started'),
  payload: z.object({
    unitId: EntityId,
    movement: ActiveMovement,
  }),
});

/** El movimiento llegó a su destino. */
export const UnitMovementCompleted = z.object({
  ...serverEnvelope,
  type: z.literal('unit.movement.completed'),
  payload: z.object({
    unitId: EntityId,
    movementId: EntityId,
    finalPosition: Tile,
  }),
});

/**
 * El movimiento se interrumpió: por una orden nueva, por cancelación explícita,
 * o porque la ruta dejó de ser válida. La unidad queda SIEMPRE sobre un tile
 * completo, nunca entre dos.
 */
export const UnitMovementCancelled = z.object({
  ...serverEnvelope,
  type: z.literal('unit.movement.cancelled'),
  payload: z.object({
    unitId: EntityId,
    movementId: EntityId,
    stoppedAt: Tile,
    reason: z.enum(['REPLACED', 'CANCELLED_BY_PLAYER', 'PATH_BLOCKED', 'UNIT_DEAD', 'SERVER']),
  }),
});

/**
 * Un mensaje nuevo entre dos jugadores (SPEC-MESSAGES §6), tras guardarse: lo reciben
 * todas las sesiones de los dos; la que lo envió, con su `requestId` como confirmación.
 */
export const MessageNew = z.object({
  ...serverEnvelope,
  type: z.literal('message.new'),
  payload: z.object({ message: MessageView }),
});

/**
 * La bandeja, en respuesta a `message.list` o a `message.block` y sólo para la sesión que
 * la pidió: las conversaciones, de la más reciente a la más antigua, y los bloqueados.
 */
export const MessageInbox = z.object({
  ...serverEnvelope,
  type: z.literal('message.inbox'),
  payload: z.object({
    conversations: z.array(ConversationView),
    blocked: z.array(BlockedPlayerView),
  }),
});

/**
 * Una página de una conversación, en respuesta a `message.fetch` y sólo para la sesión
 * que la pidió (SPEC-MESSAGES §6): los mensajes de más antiguo a más reciente,
 * `readUpToId` (lo leído por quien mira al servirla) y `nextBeforeId`, el `beforeId` de
 * la página anterior, `null` si no hay más.
 */
export const MessagePage = z.object({
  ...serverEnvelope,
  type: z.literal('message.page'),
  payload: z.object({
    otherPlayerId: Uuid,
    otherCityId: EntityId.nullable(),
    otherCityName: z.string().min(1).max(64).nullable(),
    messages: z.array(MessageView),
    readUpToId: z.number().int().min(0).max(Number.MAX_SAFE_INTEGER),
    nextBeforeId: EntityId.nullable(),
    blockedByMe: z.boolean(),
  }),
});

/** Unión discriminada de todos los mensajes servidor → cliente de la v1. */
export const ServerMessage = z.discriminatedUnion('type', [
  SessionWelcome,
  SessionPong,
  SystemError,
  WorldSnapshot,
  EntitySpawn,
  EntityUpdate,
  EntityDespawn,
  CityUpdate,
  TerritoryUpdate,
  UnitMoveAccepted,
  UnitMoveRejected,
  UnitMovementStarted,
  UnitMovementCompleted,
  UnitMovementCancelled,
  BuildingSpawn,
  BuildingUpdate,
  BuildingDestroyed,
  ForestUpdate,
  HillUpdate,
  TreatyUpdate,
  EventLogPage,
  TradeUpdate,
  EventLogNotice,
  CaravanUpdate,
  MessageNew,
  MessageInbox,
  MessagePage,
]);
export type ServerMessage = z.infer<typeof ServerMessage>;

export type SessionWelcome = z.infer<typeof SessionWelcome>;
export type SessionPong = z.infer<typeof SessionPong>;
export type SystemError = z.infer<typeof SystemError>;
export type WorldSnapshot = z.infer<typeof WorldSnapshot>;
export type EntitySpawn = z.infer<typeof EntitySpawn>;
export type EntityUpdate = z.infer<typeof EntityUpdate>;
export type EntityDespawn = z.infer<typeof EntityDespawn>;
export type CityUpdate = z.infer<typeof CityUpdate>;
export type TerritoryUpdate = z.infer<typeof TerritoryUpdate>;
export type UnitMoveAccepted = z.infer<typeof UnitMoveAccepted>;
export type UnitMoveRejected = z.infer<typeof UnitMoveRejected>;
export type UnitMovementStarted = z.infer<typeof UnitMovementStarted>;
export type UnitMovementCompleted = z.infer<typeof UnitMovementCompleted>;
export type UnitMovementCancelled = z.infer<typeof UnitMovementCancelled>;
export type BuildingSpawn = z.infer<typeof BuildingSpawn>;
export type BuildingUpdate = z.infer<typeof BuildingUpdate>;
export type BuildingDestroyed = z.infer<typeof BuildingDestroyed>;
export type ForestUpdate = z.infer<typeof ForestUpdate>;
export type HillUpdate = z.infer<typeof HillUpdate>;
export type TreatyUpdate = z.infer<typeof TreatyUpdate>;
export type EventLogPage = z.infer<typeof EventLogPage>;
export type TradeUpdate = z.infer<typeof TradeUpdate>;
export type EventLogNotice = z.infer<typeof EventLogNotice>;
export type CaravanUpdate = z.infer<typeof CaravanUpdate>;
export type MessageNew = z.infer<typeof MessageNew>;
export type MessageInbox = z.infer<typeof MessageInbox>;
export type MessagePage = z.infer<typeof MessagePage>;

export const SERVER_MESSAGE_TYPES = [
  'session.welcome',
  'session.pong',
  'system.error',
  'world.snapshot',
  'entity.spawn',
  'entity.update',
  'entity.despawn',
  'city.update',
  'territory.update',
  'unit.move.accepted',
  'unit.move.rejected',
  'unit.movement.started',
  'unit.movement.completed',
  'unit.movement.cancelled',
  'building.spawn',
  'building.update',
  'building.destroyed',
  'forest.update',
  'hill.update',
  'treaty.update',
  'event_log.page',
  'trade.update',
  'event_log.notice',
  'caravan.update',
  'message.new',
  'message.inbox',
  'message.page',
] as const;

export type ServerMessageType = (typeof SERVER_MESSAGE_TYPES)[number];
