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

/**
 * `WORKING`: parada sobre el tile de un recurso hasta que vence el temporizador de
 * su tarea. Ver ../../../docs/specs/economy-wood.md (RN-ECON-003).
 */
export const UnitStatus = z.enum(['IDLE', 'MOVING', 'GARRISONED', 'HIDDEN', 'WORKING', 'ATTACKING', 'DEAD']);
export type UnitStatus = z.infer<typeof UnitStatus>;

export const UnitType = z.enum(['VILLAGER', 'WARRIOR']);
export type UnitType = z.infer<typeof UnitType>;

export const PresenceState = z.enum(['ONLINE', 'OFFLINE_PENDING', 'PROTECTED']);
export type PresenceState = z.infer<typeof PresenceState>;

/**
 * Recursos de la economía: la madera del bosque y la piedra de la colina. Ver
 * ../../../docs/specs/economy-wood.md y economy-stone.md
 */
export const ResourceType = z.enum(['WOOD', 'STONE']);
export type ResourceType = z.infer<typeof ResourceType>;

/**
 * Cota técnica de una línea de comercio (ADR-032 §7): no es una regla de juego.
 */
export const TRADE_MAX_AMOUNT = 1_000_000;

/**
 * Una línea de una oferta de comercio: un recurso y una cantidad (SPEC-TRADE §4.2).
 * Nada de regalos: la cantidad es al menos 1 (Q3).
 */
export const ResourceAmount = z.object({
  resource: ResourceType,
  amount: z.number().int().min(1).max(TRADE_MAX_AMOUNT),
});
export type ResourceAmount = z.infer<typeof ResourceAmount>;

/**
 * Stock de una ciudad, por recurso. Lo calcula y lo envía el servidor; el cliente
 * jamás lo propone.
 */
export const Stocks = z.object({
  WOOD: z.number().int().min(0),
  /**
   * Piedra (SPEC-ECON-STONE). Opcional: aditivo en v1 (R-VER-2); el servidor de
   * E9 en adelante siempre la manda.
   */
  STONE: z.number().int().min(0).optional(),
});
export type Stocks = z.infer<typeof Stocks>;

export const BuildingType = z.enum(['HOUSE']);
export type BuildingType = z.infer<typeof BuildingType>;

export const BuildingStatus = z.enum(['UNDER_CONSTRUCTION', 'COMPLETE']);
export type BuildingStatus = z.infer<typeof BuildingStatus>;

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
  /** Ciudad que aloja a la unidad mientras está GARRISONED; `null` fuera de una guarnición. `cityId` es siempre la de origen. */
  garrisonCityId: EntityId.nullable(),
  /** Lo que lleva encima una unidad que recolecta. Ausente si no lleva nada. */
  carrying: z.number().int().min(0).optional(),
  /** El recurso de `carrying` cuando lo lleva una recolección (SPEC-ECON-STONE §6). */
  carryingResource: ResourceType.optional(),
});
export type UnitView = z.infer<typeof UnitView>;

/** Entrenamiento en curso de una ciudad: qué y cuándo termina. */
export const ProductionView = z.object({
  unitType: UnitType,
  completesAtMs: EpochMs,
});
export type ProductionView = z.infer<typeof ProductionView>;

/** Avance de era en curso de una ciudad: a qué era va y cuándo llega (SPEC-ECON-STONE §6). */
export const AdvanceView = z.object({
  era: EraCode,
  completesAtMs: EpochMs,
});
export type AdvanceView = z.infer<typeof AdvanceView>;

/** Estado observable de un edificio. */
export const BuildingView = z.object({
  id: EntityId,
  cityId: EntityId,
  type: BuildingType,
  x: WorldCoordinate,
  y: WorldCoordinate,
  status: BuildingStatus,
  /** Instante absoluto en que la obra termina (o terminó). */
  completesAtMs: EpochMs,
  /**
   * Vida frente a un asedio y su máximo (SPEC-COMBAT-SIEGE §4.2). El daño se olvida
   * a los minutos sin golpes: con `hp < maxHp` la casa está dañada.
   */
  hp: z.number().int().min(0).optional(),
  maxHp: z.number().int().min(1).optional(),
});
export type BuildingView = z.infer<typeof BuildingView>;

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
  /** Stock de la ciudad. */
  stocks: Stocks.optional(),
  /** Entrenamiento en curso; `null` si no entrena nada. */
  production: ProductionView.nullable().optional(),
  /** Avance de era en curso; `null` si no avanza (SPEC-ECON-STONE). */
  advance: AdvanceView.nullable().optional(),
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

/**
 * Madera de un tile de bosque que no está lleno (SPEC-ECON-FOREST). Un tile `FOREST`
 * que no aparece está lleno. `regrowsAtMs` es cuándo vuelve a estarlo; `null` cuando
 * un `forest.update` anuncia que ya lo está.
 */
export const ForestView = z.object({
  x: WorldCoordinate,
  y: WorldCoordinate,
  remaining: z.number().int().min(0),
  regrowsAtMs: EpochMs.nullable(),
});
export type ForestView = z.infer<typeof ForestView>;

/**
 * Piedra de un tile de colina que no está lleno (SPEC-ECON-STONE). Un tile `HILL` que
 * no aparece está lleno. La misma forma que `ForestView`: `regrowsAtMs` nulo en un
 * `hill.update` anuncia que el tile vuelve a estar lleno.
 */
export const HillView = z.object({
  x: WorldCoordinate,
  y: WorldCoordinate,
  remaining: z.number().int().min(0),
  regrowsAtMs: EpochMs.nullable(),
});
export type HillView = z.infer<typeof HillView>;

/** Tipo de tratado (SPEC-DIPLOMACY §4.1). `TRADE` sólo existe por vía administrativa. */
export const TreatyType = z.enum(['NON_AGGRESSION', 'ALLIANCE', 'TRADE']);
export type TreatyType = z.infer<typeof TreatyType>;

/** Estado de un tratado (SPEC-DIPLOMACY §4.2). */
export const TreatyStatus = z.enum(['PROPOSED', 'ACTIVE', 'EXPIRED', 'BROKEN', 'REJECTED', 'WITHDRAWN']);
export type TreatyStatus = z.infer<typeof TreatyStatus>;

/**
 * Un tratado visto por uno de sus dos firmantes: el «otro» es relativo a quien lo
 * recibe. Nadie más los recibe (SPEC-DIPLOMACY Q8).
 */
export const TreatyView = z.object({
  id: EntityId,
  otherPlayerId: Uuid,
  /** La ciudad del otro firmante, si la tiene. */
  otherCityId: EntityId.nullable(),
  otherCityName: z.string().min(1).max(64).nullable(),
  type: TreatyType,
  status: TreatyStatus,
  proposedByMe: z.boolean(),
  /** Fin del preaviso de una ruptura; `null` si no la hay (RN-DIPLO-004). */
  breaksAtMs: EpochMs.nullable(),
});
export type TreatyView = z.infer<typeof TreatyView>;

/**
 * Tipo de una entrada del registro (SPEC-EVENT-LOG §4.1), siempre desde el punto de
 * vista de quien la recibe: `UNIT_LOST` es una unidad suya muerta; `ENEMY_KILLED`,
 * una ajena que mataron las suyas.
 */
export const EventLogKind = z.enum([
  'UNIT_LOST',
  'ENEMY_KILLED',
  'PILLAGED',
  'PILLAGE_DONE',
  'HOUSE_LOST',
  'HOUSE_DESTROYED',
  'TREATY_OFFERED',
  'TREATY_WITHDRAWN',
  'TREATY_ACCEPTED',
  'TREATY_REJECTED',
  'TREATY_OFFER_EXPIRED',
  'TREATY_BREAK_ANNOUNCED',
  'TREATY_ENDED',
  // Comercio (SPEC-TRADE §6): una oferta recibida, la propia aceptada o rechazada, la
  // recibida que el otro retiró y la que caducó.
  'TRADE_OFFERED',
  'TRADE_ACCEPTED',
  'TRADE_REJECTED',
  'TRADE_WITHDRAWN',
  'TRADE_OFFER_EXPIRED',
]);
export type EventLogKind = z.infer<typeof EventLogKind>;

/**
 * Una entrada del registro: uno o varios sucesos del mismo tipo con el mismo otro
 * jugador, agrupados al leer (SPEC-EVENT-LOG §4.2). `id` es el del último evento del
 * grupo; las entradas con `id` mayor que `seenUpToId` son nuevas.
 */
export const EventLogEntry = z.object({
  id: EntityId,
  kind: EventLogKind,
  /** Primer y último suceso del grupo, en la simulación (RN-LOG-005). */
  firstAtMs: EpochMs,
  lastAtMs: EpochMs,
  count: z.number().int().min(1),
  otherPlayerId: Uuid,
  /** La ciudad del otro jugador, si la tiene. */
  otherCityId: EntityId.nullable(),
  otherCityName: z.string().min(1).max(64).nullable(),
  /** En las muertes: qué tipo de unidad murió. */
  unitType: UnitType.optional(),
  /** En los saqueos: la madera del grupo. */
  amount: z.number().int().min(0).optional(),
  /** En los tratados. */
  treatyType: TreatyType.optional(),
  /** En una oferta que caducó: si la había hecho quien recibe la entrada. */
  proposedByMe: z.boolean().optional(),
  /** Dónde ocurrió el último suceso del grupo, en las muertes y los saqueos. */
  x: WorldCoordinate.optional(),
  y: WorldCoordinate.optional(),
  /** En una ruptura anunciada: cuándo termina la paz (RN-DIPLO-004). */
  breaksAtMs: EpochMs.optional(),
  /**
   * En los tratos (SPEC-TRADE §6): lo que la oferta sacaba del almacén de quien recibe
   * la entrada y lo que metía en él. Opcionales: aditivos en v1 (R-VER-2).
   */
  tradeGive: z.array(ResourceAmount).optional(),
  tradeGet: z.array(ResourceAmount).optional(),
});
export type EventLogEntry = z.infer<typeof EventLogEntry>;

/** Estado de una oferta de comercio (SPEC-TRADE §4.2). */
export const TradeOfferStatus = z.enum(['PROPOSED', 'ACCEPTED', 'REJECTED', 'WITHDRAWN', 'EXPIRED']);
export type TradeOfferStatus = z.infer<typeof TradeOfferStatus>;

/**
 * Una oferta de comercio vista por uno de sus dos jugadores (SPEC-TRADE §6): `give`
 * es lo que saldría de su almacén y `get` lo que entraría, sea quien sea el que la
 * propuso.
 */
export const TradeOfferView = z.object({
  id: EntityId,
  otherPlayerId: Uuid,
  /** La ciudad del otro jugador, si la tiene. */
  otherCityId: EntityId.nullable(),
  otherCityName: z.string().min(1).max(64).nullable(),
  give: z.array(ResourceAmount).min(1),
  get: z.array(ResourceAmount).min(1),
  status: TradeOfferStatus,
  proposedByMe: z.boolean(),
  /** Cuándo caduca sin respuesta (RN-TRADE-006). */
  expiresAtMs: EpochMs,
});
export type TradeOfferView = z.infer<typeof TradeOfferView>;
