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
export const UnitStatus = z.enum(['IDLE', 'MOVING', 'GARRISONED', 'HIDDEN', 'WORKING', 'ATTACKING', 'DEAD', 'CLAIMING']);
export type UnitStatus = z.infer<typeof UnitStatus>;

/**
 * `CARAVAN` lleva la mercancía de un trato (SPEC-CARAVANS §4.1): no se entrena ni admite órdenes.
 * `LEGIONARY`, `CATAPHRACT`, `HORSE_ARCHER` y `BERSERKER` son las unidades exclusivas de cada
 * civilización (SPEC-CIVILIZATION-UNITS §4.1): sólo las entrena la suya.
 */
export const UnitType = z.enum([
  'VILLAGER',
  'WARRIOR',
  'CARAVAN',
  'LEGIONARY',
  'CATAPHRACT',
  'HORSE_ARCHER',
  'BERSERKER',
]);
export type UnitType = z.infer<typeof UnitType>;

/**
 * Los tipos que se entrenan en una ciudad (`city.train`): la caravana no (RN-CARAVAN-001). Una
 * unidad exclusiva sólo la acepta la ciudad de su civilización, desde la Edad de Bronce
 * (RN-CIVUNIT-002): eso lo decide el servidor, no el esquema.
 */
export const TrainableUnitType = UnitType.extract([
  'VILLAGER',
  'WARRIOR',
  'LEGIONARY',
  'CATAPHRACT',
  'HORSE_ARCHER',
  'BERSERKER',
]);
export type TrainableUnitType = z.infer<typeof TrainableUnitType>;

export const PresenceState = z.enum(['ONLINE', 'OFFLINE_PENDING', 'PROTECTED']);
export type PresenceState = z.infer<typeof PresenceState>;

/**
 * Recursos de la economía: la madera del bosque y la piedra de la colina, y desde
 * E14 los cuatro bienes de civilización. Ver ../../../docs/specs/economy-wood.md,
 * economy-stone.md y civilization-goods.md
 */
export const ResourceType = z.enum(['WOOD', 'STONE', 'WINE', 'SILK', 'SPICES', 'FURS']);
export type ResourceType = z.infer<typeof ResourceType>;

/**
 * Los bienes de civilización (SPEC-CIVILIZATION-GOODS §4.1): cada uno lo elabora
 * sólo el taller de una civilización. Qué bien es de quién lo dice el catálogo.
 */
export const GoodType = z.enum(['WINE', 'SILK', 'SPICES', 'FURS']);
export type GoodType = z.infer<typeof GoodType>;

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
  /** Bienes de civilización (SPEC-CIVILIZATION-GOODS). Opcionales: aditivo (R-VER-2). */
  WINE: z.number().int().min(0).optional(),
  SILK: z.number().int().min(0).optional(),
  SPICES: z.number().int().min(0).optional(),
  FURS: z.number().int().min(0).optional(),
});
export type Stocks = z.infer<typeof Stocks>;

/** El taller (`WORKSHOP`) llega con SPEC-CIVILIZATION-GOODS §4.2. */
/** `CASTLE` es el castillo de la Edad de los Castillos, que golpea a quien ataca a su dueño (SPEC-CASTLE-AGE §4.2). */
export const BuildingType = z.enum(['HOUSE', 'WORKSHOP', 'CASTLE']);
export type BuildingType = z.infer<typeof BuildingType>;

export const BuildingStatus = z.enum(['UNDER_CONSTRUCTION', 'COMPLETE']);
export type BuildingStatus = z.infer<typeof BuildingStatus>;

export const TerrainType = z.enum(['GRASSLAND', 'FOREST', 'HILL', 'MOUNTAIN', 'WATER', 'ROAD']);
export type TerrainType = z.infer<typeof TerrainType>;

export const EraCode = z.enum(['STONE_AGE', 'BRONZE_AGE', 'IRON_AGE', 'CASTLE_AGE']);
export type EraCode = z.infer<typeof EraCode>;

/**
 * Civilización de un jugador: la elige al registrarse y no cambia nunca
 * (SPEC-CIVILIZATION RN-CIV-001, RN-CIV-002). Viaja el código; los rasgos los
 * aplica el servidor (ADR-035 §7).
 */
export const CivilizationCode = z.enum(['ROMAN', 'BYZANTINE', 'PERSIAN', 'NORSE']);
export type CivilizationCode = z.infer<typeof CivilizationCode>;

/**
 * Facción (Global Faction) de un jugador: todos nacen `NEUTRAL` y pueden unirse
 * una vez, en partida, a `ORDER` o a `CHAOS` (SPEC-FACTIONS RN-FAC-001).
 */
export const FactionCode = z.enum(['ORDER', 'CHAOS', 'NEUTRAL']);
export type FactionCode = z.infer<typeof FactionCode>;

/** Los bandos a los que se puede unir un `NEUTRAL` (`faction.join`). */
export const JoinableFactionCode = z.enum(['ORDER', 'CHAOS']);
export type JoinableFactionCode = z.infer<typeof JoinableFactionCode>;

/** Entrada pendiente de un jugador en un bando, que cuenta en `atMs` (SPEC-FACTIONS §6). */
export const FactionJoinView = z.object({
  faction: JoinableFactionCode,
  atMs: z.number().int().min(0),
});
export type FactionJoinView = z.infer<typeof FactionJoinView>;

/** Longitud del nombre de un clan, en caracteres (SPEC-CLANS §4.1). */
export const CLAN_NAME_MIN_CHARS = 3;
export const CLAN_NAME_MAX_CHARS = 24;
/**
 * Nombre de un clan. El esquema acota la longitud; qué caracteres admite (letras,
 * dígitos, espacios y guiones, sin espacios en los extremos ni dobles) lo decide el
 * servidor (`INVALID_MESSAGE`).
 */
export const ClanName = z.string().min(CLAN_NAME_MIN_CHARS).max(CLAN_NAME_MAX_CHARS);
/** Sigla de un clan: de 2 a 4 caracteres de `A`–`Z` y `0`–`9` (SPEC-CLANS §4.1). */
export const ClanTag = z.string().regex(/^[A-Z0-9]{2,4}$/);

/** El clan de una ciudad: su `id` y su sigla, públicos (SPEC-CLANS RN-CLAN-008). */
export const CityClanView = z.object({
  id: EntityId,
  tag: ClanTag,
});
export type CityClanView = z.infer<typeof CityClanView>;

/** El clan dueño de un territorio: su `id`, nombre y sigla, públicos (SPEC-CLAN-TERRITORIES Q11). */
export const TerritoryOwnerClanView = z.object({
  id: EntityId,
  name: ClanName,
  tag: ClanTag,
});
export type TerritoryOwnerClanView = z.infer<typeof TerritoryOwnerClanView>;

/**
 * Un territorio de un clan, en las vistas del clan (SPEC-CLAN-TERRITORIES §6), con su tile
 * central para llevar la cámara aunque no esté en el área cargada.
 */
export const ClanTerritoryView = z.object({
  id: EntityId,
  name: z.string().min(1).max(64),
  x: WorldCoordinate,
  y: WorldCoordinate,
});
export type ClanTerritoryView = z.infer<typeof ClanTerritoryView>;

/** Rango de un miembro de un clan (SPEC-CLANS Q4, SPEC-CLAN-GOVERNANCE Q1). */
export const ClanRole = z.enum(['LEADER', 'OFFICER', 'MEMBER']);
export type ClanRole = z.infer<typeof ClanRole>;

/** Un miembro de un clan, nombrado por su ciudad (SPEC-CLANS §6). */
export const ClanMemberView = z.object({
  playerId: Uuid,
  cityId: EntityId.nullable(),
  cityName: z.string().min(1).max(64).nullable(),
  faction: FactionCode,
  role: ClanRole,
  joinedAtMs: EpochMs,
});
export type ClanMemberView = z.infer<typeof ClanMemberView>;

/** Una invitación que envió el clan, sólo para su líder y sus oficiales. */
export const ClanSentInvitationView = z.object({
  playerId: Uuid,
  cityId: EntityId.nullable(),
  cityName: z.string().min(1).max(64).nullable(),
  expiresAtMs: EpochMs,
});
export type ClanSentInvitationView = z.infer<typeof ClanSentInvitationView>;

/** Estado de una alianza entre clanes en las vistas: una oferta o una alianza activa. */
export const ClanAllianceStatus = z.enum(['PROPOSED', 'ACTIVE']);
export type ClanAllianceStatus = z.infer<typeof ClanAllianceStatus>;

/**
 * Una alianza (o una oferta) del propio clan con otro (SPEC-CLAN-ALLIANCES §6): las activas
 * para todos los miembros y las ofertas sólo para el líder y los oficiales. `clanId`,
 * `name` y `tag` son los del otro clan; `breaksAtMs`, el fin del preaviso de ruptura.
 */
export const ClanAllianceView = z.object({
  id: EntityId,
  clanId: EntityId,
  name: ClanName,
  tag: ClanTag,
  status: ClanAllianceStatus,
  proposedByUs: z.boolean(),
  breaksAtMs: EpochMs.nullable(),
});
export type ClanAllianceView = z.infer<typeof ClanAllianceView>;

/** Una alianza activa de un clan en su vista pública (SPEC-CLAN-ALLIANCES Q8). */
export const ClanPublicAllianceView = z.object({
  clanId: EntityId,
  name: ClanName,
  tag: ClanTag,
  breaksAtMs: EpochMs.nullable(),
});
export type ClanPublicAllianceView = z.infer<typeof ClanPublicAllianceView>;

/** Estado de una guerra abierta en las vistas: en preparación o activa (SPEC-CLAN-WARS §4.1). */
export const ClanWarStatus = z.enum(['DECLARED', 'ACTIVE']);
export type ClanWarStatus = z.infer<typeof ClanWarStatus>;

/** Un marcador de guerra: los puntos de un lado (SPEC-CLAN-WARS §4.4). */
export const ClanWarScore = z.number().int().min(0);

/**
 * Una guerra abierta del propio clan con otro (SPEC-CLAN-WARS §6). `clanId`, `name` y `tag`
 * son los del otro clan; `endsAtMs`, el plazo, `null` en la preparación. Las dos marcas de
 * la oferta de paz llegan siempre en `false` salvo al líder y los oficiales.
 */
export const ClanWarView = z.object({
  id: EntityId,
  clanId: EntityId,
  name: ClanName,
  tag: ClanTag,
  declaredByUs: z.boolean(),
  status: ClanWarStatus,
  startsAtMs: EpochMs,
  endsAtMs: EpochMs.nullable(),
  ourScore: ClanWarScore,
  theirScore: ClanWarScore,
  peaceOfferedByUs: z.boolean(),
  peaceOfferedByThem: z.boolean(),
});
export type ClanWarView = z.infer<typeof ClanWarView>;

/** Una guerra abierta de un clan en su vista pública, vista desde ese clan (SPEC-CLAN-WARS Q10). */
export const ClanPublicWarView = z.object({
  clanId: EntityId,
  name: ClanName,
  tag: ClanTag,
  declaredByUs: z.boolean(),
  status: ClanWarStatus,
  startsAtMs: EpochMs,
  endsAtMs: EpochMs.nullable(),
  ourScore: ClanWarScore,
  theirScore: ClanWarScore,
});
export type ClanPublicWarView = z.infer<typeof ClanPublicWarView>;

/** El historial de guerras terminadas de un clan (SPEC-CLAN-WARS Q5). */
export const ClanWarRecordView = z.object({
  won: z.number().int().min(0),
  lost: z.number().int().min(0),
  drawn: z.number().int().min(0),
});
export type ClanWarRecordView = z.infer<typeof ClanWarRecordView>;

/** Resultado de una guerra terminada para quien lee (SPEC-CLAN-WARS §4.5). */
export const ClanWarResult = z.enum(['WON', 'LOST', 'DRAWN']);
export type ClanWarResult = z.infer<typeof ClanWarResult>;

/** Motivo del final de una guerra (SPEC-CLAN-WARS §4.5). */
export const ClanWarEndReason = z.enum(['SURRENDER', 'PEACE', 'TIMEOUT', 'DISSOLVED']);
export type ClanWarEndReason = z.infer<typeof ClanWarEndReason>;

/** El propio clan, visto por uno de sus miembros (SPEC-CLANS §6). */
export const ClanView = z.object({
  id: EntityId,
  name: ClanName,
  tag: ClanTag,
  leaderId: Uuid,
  members: z.array(ClanMemberView).min(1),
  /** Las invitaciones vivas que envió el clan; vacía salvo para el líder. */
  sentInvitations: z.array(ClanSentInvitationView),
  /** Los territorios del clan, por `id` (SPEC-CLAN-TERRITORIES Q11). */
  territories: z.array(ClanTerritoryView),
  /**
   * Las alianzas activas del clan y, para el líder y los oficiales, también sus ofertas,
   * por `id` (SPEC-CLAN-ALLIANCES §6).
   */
  alliances: z.array(ClanAllianceView),
  /** Las guerras abiertas del clan, por `id`, y su historial (SPEC-CLAN-WARS §6). */
  wars: z.array(ClanWarView),
  warRecord: ClanWarRecordView,
});
export type ClanView = z.infer<typeof ClanView>;

/** Una invitación recibida y viva (SPEC-CLANS §4.2). */
export const ClanInvitationView = z.object({
  clanId: EntityId,
  name: ClanName,
  tag: ClanTag,
  expiresAtMs: EpochMs,
});
export type ClanInvitationView = z.infer<typeof ClanInvitationView>;

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

/**
 * Elaboración en curso del taller de una ciudad: qué bien, cuánto y cuándo se
 * entrega (SPEC-CIVILIZATION-GOODS §4.3).
 */
export const CraftView = z.object({
  good: GoodType,
  amount: z.number().int().min(1),
  completesAtMs: EpochMs,
});
export type CraftView = z.infer<typeof CraftView>;

/** Avance de era en curso de una ciudad: a qué era va y cuándo llega (SPEC-ECON-STONE §6). */
export const AdvanceView = z.object({
  era: EraCode,
  completesAtMs: EpochMs,
});
export type AdvanceView = z.infer<typeof AdvanceView>;

/**
 * Código de una tecnología del catálogo (SPEC-TECHNOLOGIES §4.1). No es un enum
 * estricto: el catálogo vive en la base y el servidor valida el código
 * (ADR-046 §5); el cliente lo traduce con `technologies.json`.
 */
export const TechnologyCode = z.string().regex(/^[A-Z][A-Z0-9_]{0,47}$/);
export type TechnologyCode = z.infer<typeof TechnologyCode>;

/** Investigación en curso de una ciudad: qué tecnología y cuándo termina (SPEC-TECHNOLOGIES §6). */
export const ResearchView = z.object({
  technology: TechnologyCode,
  completesAtMs: EpochMs,
});
export type ResearchView = z.infer<typeof ResearchView>;

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
  /** La unidad a la que apunta un castillo, si apunta a alguna (SPEC-CASTLE-AGE §6). */
  targetUnitId: EntityId.nullable().optional(),
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
  /** Civilización de su dueño, pública para todo el que ve la ciudad (RN-CIV-009). No cambia: no va en `city.update`. */
  civilization: CivilizationCode,
  /** Facción de su dueño, pública (SPEC-FACTIONS RN-FAC-009). */
  faction: FactionCode,
  /** Entrada pendiente de su dueño en un bando; `null` si no la tiene. Pública. */
  factionJoin: FactionJoinView.nullable().optional(),
  /** Clan de su dueño (su `id` y su sigla), o `null`; público (SPEC-CLANS RN-CLAN-008). */
  clan: CityClanView.nullable(),
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
  /** Elaboración en curso del taller; `null` si no elabora (SPEC-CIVILIZATION-GOODS). */
  craft: CraftView.nullable().optional(),
  /**
   * Investigación en curso; `null` si no investiga. Sólo en la vista de su dueño:
   * en una ciudad ajena no viaja (SPEC-TECHNOLOGIES RN-TECH-008).
   */
  research: ResearchView.nullable().optional(),
  /** Tecnologías de su dueño, en orden de código. Sólo en la vista de su dueño (RN-TECH-008). */
  technologies: z.array(TechnologyCode).optional(),
});
export type CityView = z.infer<typeof CityView>;

/** Estado observable de un territorio y su control. */
/** Una reclamación activa sobre un territorio (SPEC-TERRITORY-CONQUEST §6). */
export const TerritoryClaimView = z.object({
  playerId: Uuid,
  cityId: EntityId.nullable(),
  cityName: z.string().min(1).max(64).nullable(),
  startedAtMs: EpochMs,
  endsAtMs: EpochMs,
  /** El clan para el que se reclama, si es para un clan (SPEC-CLAN-TERRITORIES §6). Aditivo en v1. */
  clan: CityClanView.optional(),
});
export type TerritoryClaimView = z.infer<typeof TerritoryClaimView>;

export const TerritoryView = z.object({
  id: EntityId,
  name: z.string().min(1).max(64),
  minX: WorldCoordinate,
  minY: WorldCoordinate,
  maxX: WorldCoordinate,
  maxY: WorldCoordinate,
  ownerType: TerritoryOwnerType,
  ownerId: z.string().nullable(),
  /** Facción del jugador dueño; `null` si no tiene un jugador por dueño (SPEC-FACTIONS §6). */
  ownerFaction: FactionCode.nullable(),
  /** El clan dueño cuando `ownerType` es `CLAN`; `null` en otro caso (SPEC-CLAN-TERRITORIES §6). */
  ownerClan: TerritoryOwnerClanView.nullable(),
  /** `true` exactamente mientras hay una reclamación activa (SPEC-TERRITORY-CONQUEST §6). */
  contested: z.boolean(),
  /** La reclamación activa, si la hay: quién reclama y cuándo termina. Aditivo en v1 (R-VER-2). */
  claim: TerritoryClaimView.optional(),
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
  // Conquista (SPEC-TERRITORY-CONQUEST §6): reclaman el territorio de quien lee, se
  // lo quitan, o sus guerreros conquistan el de otro.
  'TERRITORY_CLAIMED',
  'TERRITORY_LOST',
  'TERRITORY_TAKEN',
  // Caravanas (SPEC-CARAVANS §6): la que llegó a quien lee, la suya que le capturaron,
  // la que iba a él y capturaron, y la suya que se perdió al capturarla.
  'CARAVAN_ARRIVED',
  'CARAVAN_CAPTURED',
  'CARAVAN_INTERCEPTED',
  'CARAVAN_LOST',
  // Clanes (SPEC-CLANS Q10): al expulsado, que le expulsaron; al líder, que expulsó.
  'CLAN_EXPELLED',
  'CLAN_MEMBER_EXPELLED',
  // Gobierno del clan (SPEC-CLAN-GOVERNANCE Q8): a quien recibe el liderazgo, por
  // traspaso o porque el líder llevaba días sin jugar, y a quien lo cede o lo pierde.
  'CLAN_LEADERSHIP_RECEIVED',
  'CLAN_LEADERSHIP_GIVEN',
  'CLAN_LEADERSHIP_INHERITED',
  'CLAN_LEADERSHIP_LOST',
  // Alianzas entre clanes (SPEC-CLAN-ALLIANCES Q9): la oferta al líder que la recibe, la
  // aceptación y el rechazo al que propuso, y el preaviso de ruptura a cada miembro.
  'CLAN_ALLIANCE_OFFERED',
  'CLAN_ALLIANCE_ACCEPTED',
  'CLAN_ALLIANCE_REJECTED',
  'CLAN_ALLIANCE_BREAK_ANNOUNCED',
  // El castillo (SPEC-CASTLE-AGE §6): una unidad de quien lee que mató el castillo de
  // otro, y una unidad ajena que mató el castillo de quien lee.
  'UNIT_LOST_TO_CASTLE',
  'CASTLE_KILL',
  // Las guerras entre clanes (SPEC-CLAN-WARS Q11): la declaración y el final, a cada
  // miembro de los dos clanes, y la oferta de paz, al líder y los oficiales del otro.
  'CLAN_WAR_DECLARED',
  'CLAN_WAR_PEACE_OFFERED',
  'CLAN_WAR_ENDED',
]);
export type EventLogKind = z.infer<typeof EventLogKind>;

/**
 * Los tipos de entrada que también se avisan en vivo (SPEC-LIVE-NOTICES §4.1): lo que
 * hoy no llega a quien mira otra parte del mapa. Un subconjunto de `EventLogKind`, para
 * que el aviso y su entrada se lean igual.
 */
export const EventLogNoticeKind = EventLogKind.extract([
  'UNIT_LOST',
  'ENEMY_KILLED',
  'PILLAGE_DONE',
  'HOUSE_DESTROYED',
  // SPEC-TERRITORY-CONQUEST Q8: lo que pasa con un territorio, esté donde esté quien lee.
  'TERRITORY_CLAIMED',
  'TERRITORY_LOST',
  'TERRITORY_TAKEN',
  // SPEC-CARAVANS Q11: las capturas que le tocan a quien lee; la llegada no se avisa.
  'CARAVAN_CAPTURED',
  'CARAVAN_INTERCEPTED',
  'CARAVAN_LOST',
  // SPEC-CLANS Q10: la expulsión se avisa al expulsado.
  'CLAN_EXPELLED',
  // SPEC-CLAN-GOVERNANCE Q8: el liderazgo se avisa a quien lo recibe.
  'CLAN_LEADERSHIP_RECEIVED',
  'CLAN_LEADERSHIP_INHERITED',
  // SPEC-CLAN-ALLIANCES Q9: la oferta, la aceptación y el preaviso de ruptura.
  'CLAN_ALLIANCE_OFFERED',
  'CLAN_ALLIANCE_ACCEPTED',
  'CLAN_ALLIANCE_BREAK_ANNOUNCED',
  // SPEC-CASTLE-AGE §6: lo que mata un castillo, a los dos lados.
  'UNIT_LOST_TO_CASTLE',
  'CASTLE_KILL',
  // SPEC-CLAN-WARS Q11: la declaración, la oferta de paz y el final.
  'CLAN_WAR_DECLARED',
  'CLAN_WAR_PEACE_OFFERED',
  'CLAN_WAR_ENDED',
]);
export type EventLogNoticeKind = z.infer<typeof EventLogNoticeKind>;

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
  /** En la conquista (SPEC-TERRITORY-CONQUEST §6): el territorio. Opcionales: aditivos en v1. */
  territoryId: EntityId.optional(),
  territoryName: z.string().min(1).max(64).optional(),
  /**
   * En la conquista con un clan de por medio (SPEC-CLAN-TERRITORIES §6): en
   * `TERRITORY_CLAIMED` y `TERRITORY_LOST`, el clan de quien recibe la entrada, dueño del
   * territorio; en `TERRITORY_TAKEN`, el clan para el que lo ganó. Aditivo en v1.
   */
  clanTag: ClanTag.optional(),
  /** En las caravanas (SPEC-CARAVANS §6): lo que llevaba. Opcional: aditivo en v1. */
  cargo: z.array(ResourceAmount).optional(),
  /** En una ruptura que abrió una guerra entre clanes: `WAR` (SPEC-CLAN-WARS §6). */
  breakReason: z.literal('WAR').optional(),
  /** En la declaración y la oferta de paz: cuándo empieza la guerra (SPEC-CLAN-WARS §6). */
  startsAtMs: EpochMs.optional(),
  /** En el final de una guerra: el resultado para quien lee, el motivo y el marcador desde su clan. */
  warResult: ClanWarResult.optional(),
  warReason: ClanWarEndReason.optional(),
  ourScore: ClanWarScore.optional(),
  theirScore: ClanWarScore.optional(),
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

/** Estado de una caravana (SPEC-CARAVANS §7). */
export const CaravanStatus = z.enum(['IN_TRANSIT', 'DELIVERED', 'LOST']);
export type CaravanStatus = z.infer<typeof CaravanStatus>;

/**
 * Una caravana vista por uno de sus implicados (SPEC-CARAVANS §6): su dueño, quien la
 * mandó, el destinatario original y quien la capturó. Los demás sólo ven su unidad, sin
 * la carga (RN-CARAVAN-009). `toCityId` es adonde va ahora; `originalToCityId`, adonde
 * iba al salir. `arrivesAtMs` es `null` mientras no tiene movimiento.
 */
export const CaravanView = z.object({
  id: EntityId,
  unitId: EntityId,
  ownerPlayerId: Uuid,
  senderPlayerId: Uuid,
  fromCityId: EntityId,
  fromCityName: z.string().min(1).max(64).nullable(),
  toCityId: EntityId,
  toCityName: z.string().min(1).max(64).nullable(),
  originalToCityId: EntityId,
  cargo: z.array(ResourceAmount).min(1),
  status: CaravanStatus,
  arrivesAtMs: EpochMs.nullable(),
});
export type CaravanView = z.infer<typeof CaravanView>;

/**
 * Máximo de caracteres (puntos de código) de un mensaje entre jugadores tras recortar los
 * extremos (SPEC-MESSAGES RN-MSG-003, Q3). El servidor lo cuenta en caracteres; el
 * esquema acota el texto en unidades UTF-16, el doble, porque un carácter fuera del plano
 * básico ocupa dos.
 */
export const MESSAGE_MAX_CHARS = 500;
const MessageBody = z.string().min(1).max(2 * MESSAGE_MAX_CHARS);

/**
 * Un mensaje visto por uno de sus dos jugadores (SPEC-MESSAGES §6). «El otro» es siempre
 * el jugador que no es el de la sesión, se llame remitente o destinatario; se le nombra
 * por su ciudad, nunca por su usuario (RN-MSG-010).
 */
export const MessageView = z.object({
  id: EntityId,
  fromPlayerId: Uuid,
  toPlayerId: Uuid,
  otherPlayerId: Uuid,
  otherCityId: EntityId.nullable(),
  otherCityName: z.string().min(1).max(64).nullable(),
  body: MessageBody,
  sentAtMs: EpochMs,
});
export type MessageView = z.infer<typeof MessageView>;

/**
 * Una conversación de la bandeja (SPEC-MESSAGES §4.3): el otro jugador, el último
 * mensaje recortado, cuántos no leídos tiene quien mira y si lo tiene bloqueado.
 */
export const ConversationView = z.object({
  otherPlayerId: Uuid,
  otherCityId: EntityId.nullable(),
  otherCityName: z.string().min(1).max(64).nullable(),
  lastBody: MessageBody,
  lastFromMe: z.boolean(),
  lastAtMs: EpochMs,
  unread: z.number().int().min(0),
  blockedByMe: z.boolean(),
});
export type ConversationView = z.infer<typeof ConversationView>;

/** Un jugador bloqueado por quien mira (SPEC-MESSAGES §4.4). */
export const BlockedPlayerView = z.object({
  playerId: Uuid,
  cityId: EntityId.nullable(),
  cityName: z.string().min(1).max(64).nullable(),
});
export type BlockedPlayerView = z.infer<typeof BlockedPlayerView>;

/**
 * Un mensaje del canal de un clan (SPEC-CLANS §4.6). El remitente se nombra por su
 * ciudad, como en los mensajes directos.
 */
export const ClanMessageView = z.object({
  id: EntityId,
  clanId: EntityId,
  fromPlayerId: Uuid,
  fromCityId: EntityId.nullable(),
  fromCityName: z.string().min(1).max(64).nullable(),
  body: MessageBody,
  sentAtMs: EpochMs,
});
export type ClanMessageView = z.infer<typeof ClanMessageView>;
