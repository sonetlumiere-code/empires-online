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

  // ── Comercio (SPEC-BOTS-TRADE §4.1 a §4.4) ──
  /** Si firma tratados `TRADE` y cierra tratos (Q1: los dos perfiles). */
  readonly trades: boolean;
  /** Si sube a `NON_AGGRESSION` con otros constructores; el saqueador nunca (RN-BOT-011). */
  readonly pact: boolean;
  /** Si construye el mercado y lo usa (Q4: sólo el constructor). */
  readonly market: boolean;
  /** Bien propio que guarda (la unidad de civilización lo pide). */
  readonly ownGoodReserve: number;
  /** Lote de un trato o de una oferta de bienes. */
  readonly tradeLot: number;
  /** Lote de piedra de un trato madera por piedra. */
  readonly stoneLot: number;
  /** La tabla de precios (Q5): cuántos de lo que pide por cada uno de lo que da. */
  readonly prices: { readonly goodPerGood: number; readonly woodPerStone: number };
  /** Lotes de una oferta del mercado. */
  readonly marketLots: number;
  /** Cada cuánto consulta el tablón. */
  readonly marketScanMs: number;
  /** Una oferta propia más vieja que esto se retira. */
  readonly marketStaleMs: number;
  /** No vuelve a proponer un tratado al mismo jugador antes de esto (`EO_TREATY_RETRY_MINUTES`). */
  readonly treatyRetryMs: number;
  /** No vuelve a proponer un trato al mismo jugador antes de esto. */
  readonly tradeRetryMs: number;

  // ── La partida reunida (SPEC-BOTS-TRADE §4.5) ──
  /** Distancia del punto de reunión al blanco. */
  readonly rallyDistance: number;
  /** Plazo para reunirse, desde que todos tienen la orden del último tramo. */
  readonly rallyTimeoutMs: number;

  // ── El asalto (SPEC-BOTS-ASSAULT §4.1 a §4.4) ──
  /** Si entrena arietes y sale a asaltar desde Hierro (Q1: sólo el saqueador). */
  readonly assaults: boolean;
  /** Militares que derriban (guerreros o unidades de civilización) de una partida de asalto (Q3). */
  readonly assaultWarriors: number;
  /** Arietes de una partida de asalto (Q3). */
  readonly assaultRams: number;
  /** Con menos militares vivos que esto, la partida de asalto vuelve (Q3). */
  readonly assaultMinWarriors: number;
  /** Plazo de reunión de la partida de asalto: el ariete camina al triple. */
  readonly assaultRallyTimeoutMs: number;
  /** Una partida de asalto que dura más que esto vuelve a casa. */
  readonly assaultTimeoutMs: number;
}

const COMMON = {
  defeatWaitMs: 5 * 60_000,
  raidTimeoutMs: 30 * 60_000,
  rejectCooldownMs: 30_000,
  targetCooldownMs: 10 * 60_000,
  stoneShare: 1 / 3,
  trades: true,
  tradeLot: 10,
  stoneLot: 20,
  prices: { goodPerGood: 1, woodPerStone: 2 },
  marketLots: 3,
  marketScanMs: 60_000,
  marketStaleMs: 30 * 60_000,
  treatyRetryMs: 10 * 60_000,
  tradeRetryMs: 2 * 60_000,
  rallyDistance: 16,
  rallyTimeoutMs: 3 * 60_000,
  assaultWarriors: 4,
  assaultRams: 2,
  assaultMinWarriors: 2,
  assaultRallyTimeoutMs: 10 * 60_000,
  assaultTimeoutMs: 45 * 60_000,
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
    pact: true,
    market: true,
    ownGoodReserve: 0,
    assaults: false,
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
    // Con E29 elabora su bien: sin él no tiene nada que cambiar por bienes ajenos (Q1).
    workshop: true,
    craft: true,
    craftCap: 40,
    wall: false,
    wallStoneReserve: 0,
    research: false,
    // En Hierro, 350: dos arietes (75 cada uno) y un margen (SPEC-BOTS-ASSAULT §4.8).
    stoneTargetByEra: { STONE_AGE: 200, BRONZE_AGE: 200, IRON_AGE: 350, CASTLE_AGE: 350 },
    pact: false,
    market: false,
    // Tres unidades de civilización (10 de su bien cada una).
    ownGoodReserve: 30,
    assaults: true,
  },
};

/** Lo que cuesta una casa (SPEC-ECON-WOOD): madera, en el primer hueco de la zona urbana. */
export const HOUSE_WOOD = 30;
/** Tope de comandos por despertar (RN-BOT-003). */
export const MAX_COMMANDS_PER_THINK = 2;
/** Una unidad a esta distancia de Chebyshev del centro está «en casa». */
export const HOME_RADIUS = 6;
/** Longitud de un tramo de marcha (por debajo de `EO_PATHFINDING_MAX_DISTANCE`). */
export const MARCH_STEP = 48;
/** El suelo del saqueo (`EO_PILLAGE_STOCK_FLOOR`, SPEC-COMBAT-PILLAGE Q3): por debajo, no se roba. */
export const PILLAGE_FLOOR = 50;
/** A esta distancia del punto de reunión, un guerrero está reunido (SPEC-BOTS-TRADE §4.5). */
export const RALLY_RADIUS = 2;
