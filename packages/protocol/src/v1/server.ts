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
  ChunkRef,
  ChunkTerrain,
  CityView,
  EntityId,
  EpochMs,
  PROTOCOL_VERSION,
  PresenceState,
  TerritoryView,
  Tile,
  UnitStatus,
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
] as const;

export type ServerMessageType = (typeof SERVER_MESSAGE_TYPES)[number];
