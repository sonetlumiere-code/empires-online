/**
 * Tipos primitivos compartidos del protocolo v1.
 *
 * Regla no negociable: TODAS las coordenadas de este protocolo son coordenadas
 * LÓGICAS DE MUNDO (tiles). El protocolo jamás transporta coordenadas de pantalla
 * ni de proyección isométrica: esa transformación vive exclusivamente en el cliente.
 * Ver ../../../docs/architecture/frontend.md
 */
import { z } from 'zod';

export const PROTOCOL_VERSION = 1 as const;

/** Coordenada de mundo. int32 con signo; los límites reales del mundo los valida el servidor. */
export const WorldCoordinate = z.number().int().min(-2_147_483_648).max(2_147_483_647);

/** Instante absoluto en epoch milliseconds. Entero: nunca punto flotante. */
export const EpochMs = z.number().int().min(0);

/** Offset temporal en milisegundos, relativo a un instante base. */
export const OffsetMs = z.number().int().min(0);

export const Uuid = z.string().uuid();

/** Identificador numérico durable (bigint en PostgreSQL, seguro en JS hasta 2^53). */
export const EntityId = z.number().int().min(1).max(Number.MAX_SAFE_INTEGER);

/** Un tile del mundo. */
export const Tile = z.object({
  x: WorldCoordinate,
  y: WorldCoordinate,
});
export type Tile = z.infer<typeof Tile>;

/**
 * Un waypoint de una polilínea temporizada.
 *
 * `tMs` es el offset en milisegundos, desde `startTimeMs` del movimiento, en el que
 * la unidad ALCANZA este tile. El primer waypoint es siempre el origen con `tMs = 0`.
 * Ver ../../../docs/specs/movement.md y ADR-011.
 */
export const Waypoint = z.object({
  x: WorldCoordinate,
  y: WorldCoordinate,
  tMs: OffsetMs,
});
export type Waypoint = z.infer<typeof Waypoint>;

/** Polilínea temporizada completa de un movimiento. Nunca vacía. */
export const TimedPath = z.array(Waypoint).min(1);
export type TimedPath = z.infer<typeof TimedPath>;

export const UnitStatus = z.enum(['IDLE', 'MOVING', 'GARRISONED', 'HIDDEN', 'DEAD']);
export type UnitStatus = z.infer<typeof UnitStatus>;

export const UnitType = z.enum(['VILLAGER']);
export type UnitType = z.infer<typeof UnitType>;

export const PresenceState = z.enum(['ONLINE', 'OFFLINE_PENDING', 'PROTECTED']);
export type PresenceState = z.infer<typeof PresenceState>;

export const TerrainType = z.enum(['GRASSLAND', 'FOREST', 'HILL', 'MOUNTAIN', 'WATER', 'ROAD']);
export type TerrainType = z.infer<typeof TerrainType>;

export const EraCode = z.enum(['STONE_AGE', 'BRONZE_AGE', 'IRON_AGE', 'CASTLE_AGE']);
export type EraCode = z.infer<typeof EraCode>;

export const TerritoryOwnerType = z.enum(['NONE', 'PLAYER', 'CLAN', 'FACTION']);
export type TerritoryOwnerType = z.infer<typeof TerritoryOwnerType>;

/** Referencia a un chunk del mundo (unidad de interest management). */
export const ChunkRef = z.object({
  cx: z.number().int().min(0),
  cy: z.number().int().min(0),
});
export type ChunkRef = z.infer<typeof ChunkRef>;

/**
 * Movimiento activo de una unidad, tal y como lo ve el cliente.
 *
 * Se envía la polilínea COMPLETA para que el cliente pueda interpolar la posición
 * visual en cada frame sin volver a consultar al servidor. La posición autoritativa
 * sigue siendo, siempre, la que deriva el servidor de estos mismos datos.
 */
export const ActiveMovement = z.object({
  movementId: EntityId,
  path: TimedPath,
  startTimeMs: EpochMs,
  arrivalTimeMs: EpochMs,
  target: Tile,
});
export type ActiveMovement = z.infer<typeof ActiveMovement>;

/** Estado observable de una unidad. */
export const UnitView = z.object({
  id: EntityId,
  playerId: Uuid,
  cityId: EntityId.nullable(),
  unitType: UnitType,
  x: WorldCoordinate,
  y: WorldCoordinate,
  hp: z.number().int().min(0),
  maxHp: z.number().int().min(1),
  status: UnitStatus,
  movement: ActiveMovement.nullable(),
});
export type UnitView = z.infer<typeof UnitView>;

/** Estado observable de una ciudad. */
export const CityView = z.object({
  id: EntityId,
  ownerPlayerId: Uuid,
  name: z.string().min(1).max(64),
  centerX: WorldCoordinate,
  centerY: WorldCoordinate,
  era: EraCode,
  population: z.number().int().min(0),
  populationLimit: z.number().int().min(0),
  presenceState: PresenceState,
  protectionUntilMs: EpochMs.nullable(),
});
export type CityView = z.infer<typeof CityView>;

/** Estado observable de un territorio y su control. */
export const TerritoryView = z.object({
  id: EntityId,
  name: z.string().min(1).max(64),
  minX: WorldCoordinate,
  minY: WorldCoordinate,
  maxX: WorldCoordinate,
  maxY: WorldCoordinate,
  ownerType: TerritoryOwnerType,
  ownerId: z.string().nullable(),
  contested: z.boolean(),
});
export type TerritoryView = z.infer<typeof TerritoryView>;

/** Terreno de un chunk: `size*size` bytes en base64, en orden fila-mayor. */
export const ChunkTerrain = z.object({
  cx: z.number().int().min(0),
  cy: z.number().int().min(0),
  size: z.number().int().min(1).max(256),
  /** base64 de `size*size` bytes; cada byte es el valor numérico de TerrainType. */
  terrain: z.string(),
});
export type ChunkTerrain = z.infer<typeof ChunkTerrain>;
