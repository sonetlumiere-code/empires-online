/**
 * Los perfiles de los bots (SPEC-BOTS §4.3, Q8): datos, no ramas del código.
 * Ajustar el equilibrio de un bot es cambiar un número aquí, no el decisor.
 */

export type ProfileName = 'constructor' | 'saqueador';

export interface Profile {
  readonly name: ProfileName;
  /** Aldeanos que quiere tener en cada era. */
  readonly villagersByEra: Readonly<Record<string, number>>;
  /** Guerreros de guardia en casa (la defensa de E3 hace el resto). */
  readonly guards: number;
  /** Tope de unidades militares que entrena (saqueador). */
  readonly warriorsMax: number;
  /** Si sale a saquear y asediar a otros bots. */
  readonly raids: boolean;
  /** Guerreros parados en casa con los que sale una partida. */
  readonly raidMinParty: number;
  /** Espera antes de la siguiente partida si pierde más de la mitad de la anterior. */
  readonly defeatWaitMs: number;
  /** Una partida que dura más que esto vuelve a casa. */
  readonly raidTimeoutMs: number;
  readonly advance: boolean;
  readonly workshop: boolean;
  readonly craft: boolean;
  /** Elabora mientras tenga menos que esto de su bien. */
  readonly craftCap: number;
  readonly wall: boolean;
  /** Piedra que tiene que sobrar además de la de la muralla. */
  readonly wallStoneReserve: number;
  readonly research: boolean;
  /** Piedra que persigue en cada era antes de mandar más aldeanos a la colina. */
  readonly stoneTargetByEra: Readonly<Record<string, number>>;
  /** Fracción máxima de aldeanos en la piedra. */
  readonly stoneShare: number;
  /** Enfriamiento de una orden que el servidor rechazó (RN-BOT-002). */
  readonly rejectCooldownMs: number;
  /** Enfriamiento de un blanco protegido o inalcanzable. */
  readonly targetCooldownMs: number;
}

const COMMON = {
  defeatWaitMs: 5 * 60_000,
  raidTimeoutMs: 30 * 60_000,
  rejectCooldownMs: 30_000,
  targetCooldownMs: 10 * 60_000,
  stoneShare: 1 / 3,
} as const;

export const PROFILES: Readonly<Record<ProfileName, Profile>> = {
  constructor: {
    ...COMMON,
    name: 'constructor',
    villagersByEra: { STONE_AGE: 8, BRONZE_AGE: 12, IRON_AGE: 16, CASTLE_AGE: 16 },
    guards: 2,
    warriorsMax: 2,
    raids: false,
    raidMinParty: 0,
    advance: true,
    workshop: true,
    craft: true,
    craftCap: 60,
    wall: true,
    wallStoneReserve: 100,
    research: true,
    stoneTargetByEra: { STONE_AGE: 200, BRONZE_AGE: 400, IRON_AGE: 800, CASTLE_AGE: 800 },
  },
  saqueador: {
    ...COMMON,
    name: 'saqueador',
    villagersByEra: { STONE_AGE: 4, BRONZE_AGE: 6, IRON_AGE: 8, CASTLE_AGE: 8 },
    guards: 0,
    warriorsMax: 6,
    raids: true,
    raidMinParty: 3,
    advance: true,
    workshop: false,
    craft: false,
    craftCap: 0,
    wall: false,
    wallStoneReserve: 0,
    research: false,
    stoneTargetByEra: { STONE_AGE: 200, BRONZE_AGE: 200, IRON_AGE: 200, CASTLE_AGE: 200 },
  },
};

/** Lo que cuesta una casa (SPEC-ECON-WOOD): madera, en el primer hueco de la zona urbana. */
export const HOUSE_WOOD = 30;
/** Tope de comandos por despertar (RN-BOT-003). */
export const MAX_COMMANDS_PER_THINK = 2;
/** Una unidad a esta distancia de Chebyshev del centro está «en casa». */
export const HOME_RADIUS = 6;
/** Una partida a esta distancia del blanco ya no marcha por tramos: se le ordena saquear. */
export const STRIKE_RADIUS = 12;
/** Longitud de un tramo de marcha (por debajo de `EO_PATHFINDING_MAX_DISTANCE`). */
export const MARCH_STEP = 48;
