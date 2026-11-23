/**
 * El decisor de un bot (SPEC-BOTS §4.3, ADR-049 §3).
 *
 * `decide` es una función pura: recibe lo que el bot ve (`View`), su perfil, su
 * memoria, la hora del servidor y un aleatorio con semilla, y devuelve como mucho
 * dos comandos y la memoria nueva. No lee la hora, ni `Math.random`, ni nada de
 * fuera. Lo que el servidor decide (rutas, costes, resultados) no se decide aquí:
 * el bot sólo elige qué intención enviar, con los mismos motivos con que el HUD
 * deshabilita un botón (RN-BOT-002).
 */
import type { BuildingView, CityView, ForestView, UnitView } from '../../web/src/state/world';
import type { TreatyView } from '../../web/src/state/diplomacy';
import type { TradeOfferView } from '../../web/src/state/trade';
import type { MarketState } from '../../web/src/state/market';
import { commerceRule } from './commerce';
import {
  TERRAIN_FOREST,
  advanceBlockReason,
  civilizationUnitBlockReason,
  ramBlockReason,
  craftBlockReason,
  firstFreeHouseTile,
  trainBlockReason,
  wallBlockReason,
  workshopBlockReason,
  BRONZE_AGE_COST,
  CASTLE_AGE_COST,
  CRAFT_COST,
  IRON_AGE_COST,
  WALL_COST,
  WORKSHOP_COST,
  nextEra,
} from '../../web/src/state/economy';
import { foreignGoods, goodOf } from '../../web/src/state/civilizations';
import { technologiesFor, researchBlockReason } from '../../web/src/state/technologies';
import { civilizationUnit, unitAttacks, unitInfoOf } from '../../web/src/state/units';
import { assaultBlockReason, walled } from '../../web/src/state/cityFall';
import {
  HOME_RADIUS,
  HOUSE_WOOD,
  MARCH_STEP,
  MAX_COMMANDS_PER_THINK,
  PILLAGE_FLOOR,
  RALLY_RADIUS,
  type Profile,
  type ProfileName,
} from './profiles';
import type { Rng } from './random';

// ─────────────────────────────────────────────────────────────
// Entradas y salidas
// ─────────────────────────────────────────────────────────────

export interface Tile {
  readonly x: number;
  readonly y: number;
}

/** La ciudad de otro bot de este proceso: dónde está, aunque no esté a la vista. */
export interface BotCity {
  readonly playerId: string;
  readonly cityId: number;
  readonly name: string;
  readonly centerX: number;
  readonly centerY: number;
  /** Su civilización: qué bien elabora (SPEC-BOTS-TRADE §4.2). */
  readonly civilization?: string;
  readonly profile?: ProfileName;
  /**
   * Lo que el bot dueño sabe de su ciudad al día, para elegir un blanco de asalto
   * aunque no esté a la vista (SPEC-BOTS-ASSAULT §4.2): su era, el fin de su
   * inmunidad y si tiene la muralla completa en pie.
   */
  readonly era?: string;
  readonly fallImmuneUntilMs?: number;
  readonly walled?: boolean;
}

/** Lo que el bot ve, ya filtrado y en orden estable (RN-BOT-005). */
export interface View {
  readonly playerId: string;
  readonly city: CityView;
  /** Los edificios de su ciudad. */
  readonly buildings: readonly BuildingView[];
  /** Sus unidades a la vista (casa y campaña), en orden de id. */
  readonly units: readonly UnitView[];
  /** Ciudades ajenas a la vista, en orden de id. */
  readonly foreignCities: readonly CityView[];
  /** Edificios de ciudades ajenas a la vista, en orden de id. */
  readonly foreignBuildings: readonly BuildingView[];
  /** El byte de terreno de un tile, o `undefined` si no se conoce. */
  readonly terrainAt: (x: number, y: number) => number | undefined;
  /** Bosque a medias a la vista. */
  readonly forests: ReadonlyMap<string, ForestView>;
  /** Los tiles de bosque conocidos alrededor de la ciudad, del más cercano al más lejano. */
  readonly forestTiles: readonly Tile[];
  /** Las colinas conocidas, de la más cercana a la más lejana (las encuentra la exploración). */
  readonly hills: readonly Tile[];
  /** Las ciudades de los otros bots del proceso, en orden de playerId. */
  readonly botCities: readonly BotCity[];
  /** Sus tratados abiertos (propuestos y vigentes), en orden de id. */
  readonly treaties: readonly TreatyView[];
  /** Sus ofertas de comercio pendientes, en orden de id. */
  readonly tradeOffers: readonly TradeOfferView[];
  /** Sus ofertas abiertas del mercado y el último tablón pedido. */
  readonly market: MarketState;
}

type ResourceLine = { readonly resource: string; readonly amount: number };

export type Command =
  | {
      readonly key: string;
      readonly type: 'unit.gather';
      readonly unitId: number;
      readonly target: Tile;
      readonly resource: 'WOOD' | 'STONE';
    }
  | { readonly key: string; readonly type: 'unit.move'; readonly unitId: number; readonly target: Tile }
  | { readonly key: string; readonly type: 'unit.pillage'; readonly unitId: number; readonly cityId: number }
  | { readonly key: string; readonly type: 'unit.demolish'; readonly unitId: number; readonly buildingId: number }
  | { readonly key: string; readonly type: 'unit.assault'; readonly unitId: number; readonly cityId: number }
  | {
      readonly key: string;
      readonly type: 'city.build';
      readonly cityId: number;
      readonly building: 'HOUSE' | 'WORKSHOP' | 'WALL' | 'MARKET';
      readonly tile: Tile;
    }
  | { readonly key: string; readonly type: 'city.train'; readonly cityId: number; readonly unitType: string }
  | { readonly key: string; readonly type: 'city.advance'; readonly cityId: number }
  | { readonly key: string; readonly type: 'city.craft'; readonly cityId: number }
  | { readonly key: string; readonly type: 'city.research'; readonly cityId: number; readonly technology: string }
  | {
      readonly key: string;
      readonly type: 'treaty.propose';
      readonly playerId: string;
      readonly treatyType: 'TRADE' | 'NON_AGGRESSION';
    }
  | {
      readonly key: string;
      readonly type: 'treaty.respond';
      readonly treatyId: number;
      readonly accept: boolean;
      readonly otherPlayerId: string;
      readonly treatyType: string;
    }
  | {
      readonly key: string;
      readonly type: 'trade.propose';
      readonly playerId: string;
      readonly give: ResourceLine;
      readonly want: ResourceLine;
    }
  | {
      readonly key: string;
      readonly type: 'trade.respond';
      readonly offerId: number;
      readonly accept: boolean;
      readonly otherPlayerId: string;
      readonly give: readonly ResourceLine[];
      readonly receive: readonly ResourceLine[];
    }
  | {
      readonly key: string;
      readonly type: 'market.post';
      readonly give: ResourceLine;
      readonly want: ResourceLine;
      readonly lots: number;
    }
  | {
      readonly key: string;
      readonly type: 'market.buy';
      readonly offerId: number;
      readonly lots: number;
      /** Lo que paga y lo que recibe cada lote (para el registro). */
      readonly give: ResourceLine;
      readonly receive: ResourceLine;
    }
  | { readonly key: string; readonly type: 'market.cancel'; readonly offerId: number }
  | { readonly key: string; readonly type: 'market.list'; readonly receive: string };

/**
 * `march`: camino del punto de reunión, por tramos; `strike`: reunida, con la
 * orden de saquear (SPEC-BOTS-TRADE §4.5, RN-BOT-012) o de asaltar
 * (SPEC-BOTS-ASSAULT §4.3).
 */
export type RaidPhase = 'strike' | 'march';

/** Una partida saquea (E28) o asalta (SPEC-BOTS-ASSAULT, RN-BOT-015). */
export type RaidKind = 'pillage' | 'assault';

/**
 * Dónde va un asalto ya reunido: `breach`, los arietes contra la muralla;
 * `plant`, un guerrero va a plantar el estandarte; `hold`, el estandarte está
 * plantado y la ciudad lleva el asalto (SPEC-BOTS-ASSAULT §4.3).
 */
export type AssaultStage = 'breach' | 'plant' | 'hold';

/** Por qué se rompió un asalto plantado (SPEC-BOTS-ASSAULT §4.7). */
export type AssaultBreak = 'holder_died' | 'wall_completed' | 'peace' | 'protected';

/** Por qué vuelve una partida de asalto sin la caída (§4.4). */
export type AssaultAbandon = 'timeout' | 'no_rams' | 'too_few' | 'blocked' | 'fell';

/** Una partida de saqueo en curso. */
export interface Raid {
  readonly targetCityId: number;
  readonly targetName: string;
  readonly target: Tile;
  readonly warriors: readonly number[];
  readonly startedAtMs: number;
  readonly phase: RaidPhase;
  /** Guerreros a los que ya se les dio la orden de esta fase o de este tramo. */
  readonly ordered: readonly number[];
  readonly waypoint: Tile | null;
  /** Intentos de este tramo rechazados (el destino no era transitable). */
  readonly waypointTries: number;
  /** Guerreros que salieron y murieron. */
  readonly lost: number;
  readonly size: number;
  /** Algún guerrero llegó a salir de casa: desde entonces, volver a casa la termina. */
  readonly departed: boolean;
  /** El punto de reunión, a `rallyDistance` del blanco en el lado de casa. */
  readonly rally: Tile;
  /** Cuándo todos tuvieron la orden de ir al punto de reunión (desde ahí corre `rallyTimeoutMs`). */
  readonly rallyAtMs: number | null;
  /** Guerreros reunidos al dar la orden de saquear, y lo que se esperó (para el registro). */
  readonly gathered: number;
  readonly rallyWaitMs: number;

  // ── El asalto (SPEC-BOTS-ASSAULT §4.3, §4.4) ──
  readonly kind: RaidKind;
  /** Los arietes de la partida (también en `warriors`, que lleva a todos). */
  readonly rams: readonly number[];
  readonly stage: AssaultStage | null;
  /** El que lleva o va a llevar el estandarte. */
  readonly holder: number | null;
  /** Estandartes plantados (cada `unit.assault` que llegó a dar asalto). */
  readonly planted: number;
  /** Asaltos rotos y la causa del último. */
  readonly breaks: number;
  readonly lastBreak: AssaultBreak | null;
  /** La partida vuelve sin seguir: el motivo (o la caída). */
  readonly abandon: AssaultAbandon | null;
}

/** Lo de una partida que sólo usa el asalto, a su valor de partida (también la de saqueo lo lleva). */
export const RAID_ASSAULT_FIELDS = {
  kind: 'pillage',
  rams: [],
  stage: null,
  holder: null,
  planted: 0,
  breaks: 0,
  lastBreak: null,
  abandon: null,
} as const satisfies Pick<Raid, 'kind' | 'rams' | 'stage' | 'holder' | 'planted' | 'breaks' | 'lastBreak' | 'abandon'>;

export interface Memory {
  /** Clave de orden → hasta cuándo no se repite (RN-BOT-002). */
  readonly cooldowns: Readonly<Record<string, number>>;
  /** Tiles de recolección que el servidor rechazó, con su caducidad. */
  readonly badTiles: Readonly<Record<string, number>>;
  /** Aldeanos mandados a la piedra. */
  readonly stoneUnits: readonly number[];
  readonly raid: Raid | null;
  /** No sale otra partida antes de esto. */
  readonly raidWaitUntilMs: number;
  /** Blancos que no se eligen hasta su caducidad (protegidos, inalcanzables). */
  readonly targetCooldowns: Readonly<Record<number, number>>;
  /** Una colina no tuvo ruta: nadie va a la piedra hasta entonces. */
  readonly stoneBlockedUntilMs: number;
  /** Guerrero varado → vueltas a casa rechazadas (cada una desplaza el tramo en espiral). */
  readonly recallTries: Readonly<Record<number, number>>;
  /** Jugador → hasta cuándo no se le propone otro tratado (SPEC-BOTS-TRADE §4.1). */
  readonly treatyCooldowns: Readonly<Record<string, number>>;
  /** Jugador → hasta cuándo no se le propone otro trato (§4.2). */
  readonly tradeCooldowns: Readonly<Record<string, number>>;
  /** Oferta propia del mercado → cuándo se vio por primera vez (§4.3). */
  readonly marketSeen: Readonly<Record<number, number>>;
  /** La próxima consulta del tablón, y qué bien ajeno toca. */
  readonly marketScanAtMs: number;
  readonly marketScanIndex: number;
  /**
   * Jugadores sin ruta de caravana hasta su ciudad: una propuesta o una
   * aceptación volvió con `PATH_TOO_LONG` o `PATH_NOT_FOUND`. Ni las ciudades ni
   * el terreno se mueven, así que se apartan hasta el final de la ejecución
   * (RN-BOT-013, SPEC-TRADE-REACH §4.3).
   */
  readonly unreachablePlayers: readonly string[];
  /** Ofertas del mercado cuya compra volvió sin ruta (RN-BOT-013). */
  readonly unreachableOffers: readonly number[];
}

export const EMPTY_MEMORY: Memory = {
  cooldowns: {},
  badTiles: {},
  stoneUnits: [],
  raid: null,
  raidWaitUntilMs: 0,
  targetCooldowns: {},
  stoneBlockedUntilMs: 0,
  recallTries: {},
  treatyCooldowns: {},
  tradeCooldowns: {},
  marketSeen: {},
  marketScanAtMs: 0,
  marketScanIndex: 0,
  unreachablePlayers: [],
  unreachableOffers: [],
};

export interface Decision {
  readonly commands: readonly Command[];
  readonly memory: Memory;
}

// ─────────────────────────────────────────────────────────────
// Ayudantes
// ─────────────────────────────────────────────────────────────

export function chebyshev(a: Tile, b: Tile): number {
  return Math.max(Math.abs(a.x - b.x), Math.abs(a.y - b.y));
}

const tileKey = (t: Tile) => `${t.x},${t.y}`;

/** El terreno que no se pisa (`world.TerrainType`: agua y montaña). */
const UNWALKABLE = new Set([3, 4]);

/**
 * El anillo de nacimiento de una ciudad, en el orden de `city.SpawnTilesAround`
 * del Game Server: el primero transitable es el depósito (RN-ECON-004).
 */
const SPAWN_OFFSETS: readonly Tile[] = [
  { x: 2, y: 0 },
  { x: -2, y: 0 },
  { x: 0, y: 2 },
  { x: 0, y: -2 },
  { x: 2, y: 2 },
  { x: -2, y: -2 },
  { x: 2, y: -2 },
  { x: -2, y: 2 },
];

/** El tile de depósito de la ciudad: a donde vuelve un guerrero con botín. */
export function depositTile(city: Pick<CityView, 'centerX' | 'centerY'>, terrainAt: View['terrainAt']): Tile {
  for (const off of SPAWN_OFFSETS) {
    const t = { x: city.centerX + off.x, y: city.centerY + off.y };
    const terrain = terrainAt(t.x, t.y);
    if (terrain !== undefined && !UNWALKABLE.has(terrain)) return t;
  }
  return { x: city.centerX + 2, y: city.centerY };
}

/** Los tiles de bosque de un área, del más cercano al centro al más lejano, fuera de la zona urbana. */
export function forestTilesAround(
  center: Tile,
  radius: number,
  terrainAt: View['terrainAt'],
): Tile[] {
  const out: (Tile & { d: number })[] = [];
  for (let y = center.y - radius; y <= center.y + radius; y++) {
    for (let x = center.x - radius; x <= center.x + radius; x++) {
      if (terrainAt(x, y) !== TERRAIN_FOREST) continue;
      const d = chebyshev(center, { x, y });
      if (d <= 1) continue;
      out.push({ x, y, d });
    }
  }
  out.sort((a, b) => a.d - b.d || a.y - b.y || a.x - b.x);
  return out.map(({ x, y }) => ({ x, y }));
}

/** Si el tipo es militar (ataca): guerrero o unidad de civilización. */
export function isMilitary(u: Pick<UnitView, 'unitType'>): boolean {
  return unitAttacks(u.unitType);
}

function lootCarry(type: string): number {
  return unitInfoOf(type)?.lootCarry ?? 20;
}

type Stocks = Record<string, number>;

function costOfUnit(type: string, civilization: string): Stocks {
  const info = unitInfoOf(type);
  const cost: Stocks = {};
  for (const c of info?.trainCosts ?? []) cost[c.resource] = (cost[c.resource] ?? 0) + c.amount;
  const good = goodOf(civilization);
  if (info && info.ownGoodCost > 0 && good) cost[good] = (cost[good] ?? 0) + info.ownGoodCost;
  return cost;
}

function costOfAdvance(era: string): Stocks {
  const next = nextEra(era);
  if (next === 'BRONZE_AGE') return { ...BRONZE_AGE_COST };
  if (next === 'IRON_AGE') return { WOOD: IRON_AGE_COST.WOOD, STONE: IRON_AGE_COST.STONE };
  if (next === 'CASTLE_AGE') return { WOOD: CASTLE_AGE_COST.WOOD, STONE: CASTLE_AGE_COST.STONE };
  return {};
}

// ─────────────────────────────────────────────────────────────
// El plan de un despertar
// ─────────────────────────────────────────────────────────────

/**
 * Lo que el decisor va gastando dentro de un mismo despertar: una copia de la
 * ciudad a la que se le resta cada encargo elegido, para que el segundo comando
 * no cuente con la madera que ya se llevó el primero.
 */
class Plan {
  readonly commands: Command[] = [];
  city: CityView;
  private readonly keys = new Set<string>();

  constructor(
    city: CityView,
    private readonly cooldowns: Readonly<Record<string, number>>,
    private readonly nowMs: number,
  ) {
    this.city = { ...city, stocks: { ...city.stocks } };
  }

  private closed = false;

  get full(): boolean {
    return this.closed || this.commands.length >= MAX_COMMANDS_PER_THINK;
  }

  /** Nada más en este despertar (la orden de saquear no se intercala, RN-BOT-012). */
  close(): void {
    this.closed = true;
  }

  /** Si la orden está libre: ni en enfriamiento ni elegida ya en este despertar. */
  free(key: string): boolean {
    return !this.keys.has(key) && (this.cooldowns[key] ?? 0) <= this.nowMs;
  }

  affords(cost: Stocks): boolean {
    const s = this.city.stocks as unknown as Stocks;
    return Object.entries(cost).every(([r, n]) => (s[r] ?? 0) >= n);
  }

  add(cmd: Command, cost: Stocks = {}, cityPatch: Partial<CityView> = {}): void {
    if (this.full || !this.free(cmd.key)) return;
    const stocks = { ...(this.city.stocks as unknown as Stocks) };
    for (const [r, n] of Object.entries(cost)) stocks[r] = (stocks[r] ?? 0) - n;
    this.city = { ...this.city, ...cityPatch, stocks: stocks as unknown as CityView['stocks'] };
    this.commands.push(cmd);
    this.keys.add(cmd.key);
  }
}

// ─────────────────────────────────────────────────────────────
// Las reglas
// ─────────────────────────────────────────────────────────────

function ownUnits(view: View): UnitView[] {
  return view.units.filter((u) => u.playerId === view.playerId && u.status !== 'DEAD');
}

function center(view: View): Tile {
  return { x: view.city.centerX, y: view.city.centerY };
}

function atHome(view: View, u: Tile): boolean {
  return chebyshev(center(view), u) <= HOME_RADIUS;
}

/**
 * Aldeanos que tiene la ciudad: su población menos las unidades militares que
 * ve. Los que recogen piedra lejos no se ven, pero siguen contando.
 */
export function villagerCount(view: View): number {
  const own = ownUnits(view);
  const military = own.filter(isMilitary).length;
  // El ariete no es militar (no ataca) pero ocupa dos plazas (SPEC-BOTS-ASSAULT §4.1).
  const rams = own.filter(isRam).length * RAM_POPULATION;
  return Math.max(0, view.city.population - military - rams);
}

/** Si la unidad es un ariete. */
export function isRam(u: Pick<UnitView, 'unitType'>): boolean {
  return u.unitType === 'RAM';
}

/** Lo que va de campaña y vuelve a casa: militares y arietes. */
function isFieldUnit(u: Pick<UnitView, 'unitType'>): boolean {
  return isMilitary(u) || isRam(u);
}

const RAM_POPULATION = unitInfoOf('RAM')?.populationCost ?? 2;

const ERA_ORDER = ['STONE_AGE', 'BRONZE_AGE', 'IRON_AGE', 'CASTLE_AGE'];

/** Si la era es `era` o una posterior. */
export function eraAtLeast(current: string, era: string): boolean {
  return ERA_ORDER.indexOf(current) >= ERA_ORDER.indexOf(era);
}

function militaryCount(view: View): number {
  return ownUnits(view).filter(isMilitary).length;
}

/** Regla 1: los aldeanos parados, a recolectar. */
function gatherRule(view: View, profile: Profile, memory: Memory, plan: Plan, nowMs: number): Memory {
  const idle = ownUnits(view).filter(
    (u) => u.unitType === 'VILLAGER' && u.status === 'IDLE' && u.garrisonCityId === null,
  );
  if (idle.length === 0) return memory;

  let stoneUnits = memory.stoneUnits.filter((id) => !idle.some((u) => u.id === id));
  const villagers = villagerCount(view);
  const needStone =
    plan.city.stocks.STONE < (profile.stoneTargetByEra[view.city.era] ?? 0) && memory.stoneBlockedUntilMs <= nowMs;
  const stoneSlots = Math.ceil(villagers * profile.stoneShare);
  const okTile = (t: Tile) => (memory.badTiles[tileKey(t)] ?? 0) <= nowMs;
  const forests = view.forestTiles.filter((t) => okTile(t) && view.forests.get(tileKey(t))?.remaining !== 0);
  const hills = view.hills.filter(okTile);

  for (const u of idle) {
    if (plan.full) break;
    const toStone = needStone && hills.length > 0 && stoneUnits.length < stoneSlots;
    const options = toStone ? hills : forests;
    if (options.length === 0) continue;
    // Repartidos entre los tres más cercanos, para no amontonarlos en un tile.
    const target = options[u.id % Math.min(3, options.length)] as Tile;
    const key = `gather:${u.id}`;
    if (!plan.free(key)) continue;
    plan.add({ key, type: 'unit.gather', unitId: u.id, target, resource: toStone ? 'STONE' : 'WOOD' });
    if (toStone) stoneUnits = [...stoneUnits, u.id];
  }
  return { ...memory, stoneUnits };
}

/** Regla 2: con la población al tope, una casa en el primer hueco (dejando el del taller). */
function houseRule(view: View, profile: Profile, plan: Plan): void {
  if (plan.full) return;
  const city = plan.city;
  if (city.population + 1 < city.populationLimit) return;
  if (view.buildings.some((b) => b.type === 'HOUSE' && b.status !== 'COMPLETE' && b.status !== 'DESTROYED')) return;
  if (!plan.affords({ WOOD: HOUSE_WOOD })) return;
  const standing = view.buildings.filter((b) => b.status !== 'DESTROYED');
  const tile = firstFreeHouseTile(city, standing);
  if (!tile) return;
  const freeSlots = 8 - standing.filter((b) => b.type !== 'WALL').length;
  const reserve =
    (profile.workshop && !standing.some((b) => b.type === 'WORKSHOP') ? 1 : 0) +
    (profile.market && !standing.some((b) => b.type === 'MARKET') ? 1 : 0);
  if (freeSlots <= reserve) return;
  plan.add({ key: 'build:HOUSE', type: 'city.build', cityId: city.id, building: 'HOUSE', tile }, { WOOD: HOUSE_WOOD });
}

function train(plan: Plan, unitType: string): void {
  const city = plan.city;
  if (trainBlockReason(city) !== null) return;
  const cost = costOfUnit(unitType, city.civilization);
  if (!plan.affords(cost)) return;
  plan.add(
    { key: `train:${unitType}`, type: 'city.train', cityId: city.id, unitType },
    cost,
    { production: { unitType, completesAtMs: Number.MAX_SAFE_INTEGER } },
  );
}

/** Regla 3: aldeanos hasta el número de su era. */
function villagerRule(view: View, profile: Profile, plan: Plan): void {
  if (plan.full) return;
  if (villagerCount(view) >= (profile.villagersByEra[view.city.era] ?? 0)) return;
  train(plan, 'VILLAGER');
}

/** Regla 4: el avance de era. */
function advanceRule(profile: Profile, plan: Plan): void {
  if (plan.full || !profile.advance) return;
  const city = plan.city;
  if (advanceBlockReason(city, foreignGoods(city.stocks, city.civilization)) !== null) return;
  plan.add(
    { key: 'advance', type: 'city.advance', cityId: city.id },
    costOfAdvance(city.era),
    { advance: { era: nextEra(city.era) ?? city.era, completesAtMs: Number.MAX_SAFE_INTEGER } },
  );
}

/** Regla 5: taller, bien propio y muralla. */
function bronzeRule(view: View, profile: Profile, plan: Plan): void {
  const city = plan.city;
  const standing = view.buildings.filter((b) => b.status !== 'DESTROYED');
  if (!plan.full && profile.workshop && workshopBlockReason(city, standing) === null) {
    const tile = firstFreeHouseTile(city, standing);
    if (tile) {
      plan.add({ key: 'build:WORKSHOP', type: 'city.build', cityId: city.id, building: 'WORKSHOP', tile }, { ...WORKSHOP_COST });
    }
  }
  const good = goodOf(city.civilization);
  const own = good ? ((plan.city.stocks as unknown as Stocks)[good] ?? 0) : 0;
  if (!plan.full && profile.craft && own < profile.craftCap && craftBlockReason(plan.city, standing) === null) {
    plan.add({ key: 'craft', type: 'city.craft', cityId: city.id }, { ...CRAFT_COST }, {
      craft: { good: good ?? '', amount: 0, completesAtMs: Number.MAX_SAFE_INTEGER },
    });
  }
  if (
    !plan.full &&
    profile.wall &&
    wallBlockReason(plan.city, standing) === null &&
    plan.affords({ STONE: WALL_COST.STONE + profile.wallStoneReserve })
  ) {
    plan.add(
      { key: 'build:WALL', type: 'city.build', cityId: city.id, building: 'WALL', tile: center(view) },
      { ...WALL_COST },
    );
  }
}

/** Regla 6: la primera tecnología que pueda pagar, en el orden del catálogo. */
function researchRule(profile: Profile, plan: Plan): void {
  if (plan.full || !profile.research) return;
  const city = plan.city;
  for (const group of technologiesFor(city.civilization)) {
    for (const t of group.technologies) {
      if (researchBlockReason(city, t) !== null) continue;
      const cost: Stocks = {};
      if (t.costs.WOOD) cost.WOOD = t.costs.WOOD;
      if (t.costs.STONE) cost.STONE = t.costs.STONE;
      plan.add(
        { key: `research:${t.code}`, type: 'city.research', cityId: city.id, technology: t.code },
        cost,
        { research: { technology: t.code, completesAtMs: Number.MAX_SAFE_INTEGER } },
      );
      return;
    }
  }
}

/** Regla 7 (constructor) y 1 del saqueador: las unidades militares hasta su número. */
function militaryRule(view: View, profile: Profile, plan: Plan): void {
  if (plan.full) return;
  const want = profile.raids ? profile.warriorsMax : profile.guards;
  if (militaryCount(view) >= want) {
    // Con los militares completos, los arietes de la partida de asalto, desde Hierro
    // (SPEC-BOTS-ASSAULT §4.1). Lo que el HUD deshabilitaría no se pide (RN-BOT-014).
    const rams = ownUnits(view).filter(isRam).length;
    if (profile.assaults && rams < profile.assaultRams && ramBlockReason(plan.city) === null) {
      train(plan, 'RAM');
    }
    return;
  }
  const own = civilizationUnit(plan.city.civilization);
  if (profile.raids && own && civilizationUnitBlockReason(plan.city) === null) {
    train(plan, own);
    return;
  }
  train(plan, 'WARRIOR');
}

// ─────────────────────────────────────────────────────────────
// El saqueo
// ─────────────────────────────────────────────────────────────

/** Si un blanco es de un bot (RN-BOT-004): las ciudades de bots se llaman `bot_*polis`. */
export function isBotCity(name: string): boolean {
  return name.startsWith('bot_');
}

interface Target {
  readonly cityId: number;
  readonly name: string;
  readonly center: Tile;
}

/**
 * El blanco de una partida: la ciudad de otro bot más cercana que no esté
 * protegida ni en enfriamiento; con `contraHumanos`, también las ciudades ajenas
 * a la vista (RN-BOT-004).
 */
export function chooseTarget(
  view: View,
  memory: Memory,
  nowMs: number,
  contraHumanos: boolean,
  rng: Rng,
): Target | null {
  const candidates: (Target & { d: number })[] = [];
  const home = center(view);
  const seen = new Set<number>();
  const protectedIds = new Set(view.foreignCities.filter((c) => c.presenceState === 'PROTECTED').map((c) => c.id));
  const usable = (id: number) => (memory.targetCooldowns[id] ?? 0) <= nowMs && !protectedIds.has(id);

  for (const b of view.botCities) {
    if (b.playerId === view.playerId || !isBotCity(b.name) || !usable(b.cityId)) continue;
    const c = { x: b.centerX, y: b.centerY };
    candidates.push({ cityId: b.cityId, name: b.name, center: c, d: chebyshev(home, c) });
    seen.add(b.cityId);
  }
  if (contraHumanos) {
    for (const c of view.foreignCities) {
      if (c.ownerPlayerId === view.playerId || seen.has(c.id) || !usable(c.id)) continue;
      const t = { x: c.centerX, y: c.centerY };
      candidates.push({ cityId: c.id, name: c.name, center: t, d: chebyshev(home, t) });
    }
  }
  if (candidates.length === 0) return null;
  candidates.sort((a, b) => a.d - b.d || a.cityId - b.cityId);
  const best = candidates.filter((c) => c.d === (candidates[0] as { d: number }).d);
  const pick = best[rng.int(best.length)] as Target & { d: number };
  return { cityId: pick.cityId, name: pick.name, center: pick.center };
}

/** Un paso de `MARCH_STEP` tiles desde `from` hacia `to`. */
export function marchStep(from: Tile, to: Tile, tries = 0): Tile {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  const d = Math.max(Math.abs(dx), Math.abs(dy));
  const k = d <= MARCH_STEP ? 1 : MARCH_STEP / d;
  // Cada intento rechazado desplaza el destino en espiral (el tile no era transitable).
  const spiral: readonly Tile[] = [
    { x: 0, y: 0 },
    { x: 2, y: 0 },
    { x: 0, y: 2 },
    { x: -2, y: 0 },
    { x: 0, y: -2 },
    { x: 3, y: 3 },
    { x: -3, y: 3 },
    { x: 3, y: -3 },
    { x: -3, y: -3 },
  ];
  const off = spiral[tries % spiral.length] as Tile;
  const r = 1 + Math.floor(tries / spiral.length);
  return { x: Math.round(from.x + dx * k) + off.x * r, y: Math.round(from.y + dy * k) + off.y * r };
}

/**
 * El punto de reunión de una partida (SPEC-BOTS-TRADE §4.5): a `distance` tiles
 * del blanco, en la línea hacia casa; si ese tile no se pisa, el transitable más
 * cercano del espiral. Un terreno que no se conoce se da por bueno: si no lo es,
 * el servidor rechaza el tramo y se desplaza, como cualquier tramo.
 */
export function rallyPoint(target: Tile, home: Tile, distance: number, terrainAt: View['terrainAt']): Tile {
  const dx = home.x - target.x;
  const dy = home.y - target.y;
  const d = Math.max(Math.abs(dx), Math.abs(dy));
  const k = d === 0 ? 0 : Math.min(1, distance / d);
  const base = { x: Math.round(target.x + dx * k), y: Math.round(target.y + dy * k) };
  for (let r = 0; r <= 4; r++) {
    for (let oy = -r; oy <= r; oy++) {
      for (let ox = -r; ox <= r; ox++) {
        if (Math.max(Math.abs(ox), Math.abs(oy)) !== r) continue;
        const t = { x: base.x + ox, y: base.y + oy };
        const terrain = terrainAt(t.x, t.y);
        if (terrain === undefined || !UNWALKABLE.has(terrain)) return t;
      }
    }
  }
  return base;
}

function raidRule(view: View, profile: Profile, memory: Memory, plan: Plan, nowMs: number, contraHumanos: boolean, rng: Rng): Memory {
  if (!profile.raids) return memory;
  const units = new Map(ownUnits(view).map((u) => [u.id, u]));
  const home = depositTile(view.city, view.terrainAt);
  let raid = memory.raid;

  if (raid === null) {
    // Sin partida, un militar parado fuera de casa está varado (una partida que
    // cortó el fin de la ejecución anterior, o una respuesta de E3 sin ruta de
    // vuelta): vuelve para poder formar la siguiente. Primero derecho a casa; si
    // el servidor lo rechaza (sin ruta, demasiado lejos, destino que no se pisa),
    // por tramos de la marcha desplazados en espiral, como la partida.
    const tries: Record<number, number> = {};
    for (const u of units.values()) {
      if (!isFieldUnit(u) || u.status === 'DEAD' || atHome(view, u)) continue;
      const n = memory.recallTries[u.id];
      if (n !== undefined) tries[u.id] = n;
      if (plan.full || u.status !== 'IDLE') continue;
      const key = `raid:recall:${u.id}`;
      if (!plan.free(key)) continue;
      const t = n ?? 0;
      plan.add({ key, type: 'unit.move', unitId: u.id, target: t === 0 ? home : marchStep(u, home, t - 1) });
    }
    memory = { ...memory, recallTries: tries };
    if (plan.full || nowMs < memory.raidWaitUntilMs) return memory;
    const idleHome = [...units.values()].filter((u) => u.status === 'IDLE' && atHome(view, u) && u.carrying === 0);
    const fighters = idleHome.filter(isMilitary);
    const rams = idleHome.filter(isRam);
    // RN-BOT-015: desde Hierro, con la partida completa y un blanco asaltable, sale la
    // de asalto; si no, la de saqueo (SPEC-BOTS-ASSAULT Q2).
    const assaultTarget =
      profile.assaults &&
      eraAtLeast(view.city.era, 'IRON_AGE') &&
      fighters.length >= profile.assaultWarriors &&
      rams.length >= profile.assaultRams
        ? chooseAssaultTarget(view, memory, nowMs, contraHumanos, rng)
        : null;
    let kind: RaidKind = 'pillage';
    let party = fighters;
    let ramIds: number[] = [];
    let target: Target | null;
    if (assaultTarget) {
      kind = 'assault';
      party = [...fighters.slice(0, profile.assaultWarriors), ...rams.slice(0, profile.assaultRams)];
      ramIds = rams.slice(0, profile.assaultRams).map((u) => u.id);
      target = assaultTarget;
    } else {
      if (party.length < profile.raidMinParty) return memory;
      target = chooseTarget(view, memory, nowMs, contraHumanos, rng);
    }
    if (!target) return memory;
    // RN-BOT-012: primero al punto de reunión; la orden de saquear, cuando estén todos.
    raid = {
      targetCityId: target.cityId,
      targetName: target.name,
      target: target.center,
      warriors: party.map((u) => u.id),
      startedAtMs: nowMs,
      phase: 'march',
      ordered: [],
      waypoint: null,
      waypointTries: 0,
      lost: 0,
      size: party.length,
      departed: false,
      rally: rallyPoint(target.center, center(view), profile.rallyDistance, view.terrainAt),
      rallyAtMs: null,
      gathered: 0,
      rallyWaitMs: 0,
      ...RAID_ASSAULT_FIELDS,
      kind,
      rams: ramIds,
    };
  }

  // Los que la memoria da por vivos; los muertos los quita el bot al recibir UNIT_LOST.
  const alive = raid.warriors.filter((id) => units.get(id)?.status !== 'DEAD');
  const visible = alive.map((id) => units.get(id)).filter((u): u is UnitView => u !== undefined);
  const assault = raid.kind === 'assault';
  if (assault && raid.abandon === null && nowMs - raid.startedAtMs > profile.assaultTimeoutMs) {
    raid = { ...raid, abandon: 'timeout' };
  }
  const timedOut = raid.abandon !== null || (!assault && nowMs - raid.startedAtMs > profile.raidTimeoutMs);

  if (!raid.departed && visible.some((u) => !atHome(view, u))) raid = { ...raid, departed: true };

  // Terminada: todos de vuelta en casa, parados y sin carga, o muertos.
  const back =
    raid.departed &&
    visible.length === alive.length &&
    visible.every((u) => atHome(view, u) && u.status === 'IDLE' && u.carrying === 0);
  if (alive.length === 0 || back) {
    const lostMost = raid.lost * 2 > raid.size;
    return {
      ...memory,
      raid: null,
      raidWaitUntilMs: lostMost ? nowMs + profile.defeatWaitMs : nowMs,
    };
  }

  // Los que vuelven: con botín, sin nada que hacer junto al blanco, o al vencer el plazo.
  // Mientras falte alguno por recibir la orden de saquear, ninguna otra (RN-BOT-012):
  // los que ya la tienen pueden seguir apareciendo parados un despertar más.
  // Un asalto reunido sigue en su regla mientras no vuelva (SPEC-BOTS-ASSAULT §4.3).
  if (assault && raid.phase === 'strike' && !timedOut) {
    return { ...memory, raid: assaultStrike(view, profile, raid, plan, nowMs, units) };
  }

  const striking = raid.phase === 'strike' && !timedOut && alive.some((id) => !raid?.ordered.includes(id));
  for (const u of striking ? [] : visible) {
    if (plan.full) break;
    // Al volver un asalto, también los que derriban o sostienen el estandarte: la orden
    // de mover sustituye su tarea (SPEC-BOTS-ASSAULT §4.4).
    const busy = u.status !== 'IDLE' && !(assault && timedOut && u.status !== 'MOVING');
    if (busy || (!raid.ordered.includes(u.id) && !timedOut)) continue;
    const nearTarget = chebyshev(u, raid.target) <= 3;
    const full = u.carrying >= lootCarry(u.unitType);
    if (atHome(view, u)) continue;
    if (nearTarget && !full && !timedOut && raid.phase === 'strike') {
      // Paró sin la carga llena. Si el almacén de la víctima sigue por encima del
      // suelo, fue una respuesta (RN-COMBAT-040: no reanuda sola): otra vez a saquear.
      const victim = view.foreignCities.find((c) => c.id === raid?.targetCityId);
      const pillageKey = `raid:pillage:${u.id}`;
      if (victim && victim.stocks.WOOD > PILLAGE_FLOOR && plan.free(pillageKey)) {
        plan.add({ key: pillageKey, type: 'unit.pillage', unitId: u.id, cityId: raid.targetCityId });
        continue;
      }
    }
    if (nearTarget && !full && !timedOut) {
      // El saqueo terminó en el suelo del almacén: una casa de la víctima, si se ve.
      const house = view.foreignBuildings.find(
        (b) => b.cityId === raid?.targetCityId && b.type === 'HOUSE' && b.status !== 'DESTROYED',
      );
      const key = `raid:demolish:${u.id}`;
      if (house && plan.free(key)) {
        plan.add({ key, type: 'unit.demolish', unitId: u.id, buildingId: house.id });
        continue;
      }
    }
    if (nearTarget || full || timedOut || raid.phase === 'strike') {
      // Derecho a casa; si el servidor lo rechaza (una partida a cientos de tiles), por
      // tramos desplazados en espiral, como la vuelta de un guerrero varado.
      const t = memory.recallTries[u.id] ?? 0;
      plan.add({ key: `raid:home:${u.id}`, type: 'unit.move', unitId: u.id, target: t === 0 ? home : marchStep(u, home, t - 1) });
    }
  }

  if (timedOut) return { ...memory, raid };

  if (raid.phase === 'strike') {
    // RN-BOT-012: la orden de saquear a todos, en despertares seguidos y sin
    // intercalar otras órdenes mientras falte alguno.
    for (const id of alive) {
      if (plan.full) break;
      if (raid.ordered.includes(id)) continue;
      const key = `raid:pillage:${id}`;
      if (!plan.free(key)) continue;
      plan.add({ key, type: 'unit.pillage', unitId: id, cityId: raid.targetCityId });
      raid = { ...raid, ordered: [...raid.ordered, id] };
    }
    if (alive.some((id) => !raid?.ordered.includes(id))) plan.close();
    return { ...memory, raid };
  }

  // En marcha hacia el punto de reunión. Primero, la orden del tramo en curso a todos
  // los que aún no la tienen, aunque los primeros ya caminen.
  const orderLeg = (r: Raid): Raid => {
    let next = r;
    for (const id of alive) {
      if (plan.full) break;
      if (next.ordered.includes(id) || !next.waypoint) continue;
      const key = `raid:march:${id}:${next.waypoint.x},${next.waypoint.y}`;
      if (!plan.free(key)) continue;
      plan.add({ key, type: 'unit.move', unitId: id, target: next.waypoint });
      next = { ...next, ordered: [...next.ordered, id] };
    }
    if (
      next.rallyAtMs === null &&
      next.waypoint &&
      chebyshev(next.waypoint, next.rally) <= 4 &&
      next.ordered.length >= alive.length
    ) {
      next = { ...next, rallyAtMs: nowMs };
    }
    return next;
  };
  if (raid.waypoint !== null && raid.ordered.length < alive.length) {
    return { ...memory, raid: orderLeg(raid) };
  }
  // Con todas las órdenes dadas, cuando todos los que se ven están parados, el tramo siguiente.
  if (visible.length === 0 || visible.some((u) => u.status === 'MOVING')) return { ...memory, raid };
  const party = {
    x: Math.round(visible.reduce((s, u) => s + u.x, 0) / visible.length),
    y: Math.round(visible.reduce((s, u) => s + u.y, 0) / visible.length),
  };
  const allOrdered = raid.ordered.length >= alive.length;
  // El último tramo es el del punto de reunión (o uno desplazado en espiral a su lado).
  const finalLeg = raid.waypoint !== null && chebyshev(raid.waypoint, raid.rally) <= 4;
  if (finalLeg && allOrdered && raid.waypoint) {
    const at = raid.waypoint;
    const gathered = visible.filter((u) => chebyshev(u, at) <= RALLY_RADIUS).length;
    const waited = raid.rallyAtMs === null ? 0 : nowMs - raid.rallyAtMs;
    const everyone = gathered === alive.length;
    // El ariete camina al triple: la partida de asalto espera más (SPEC-BOTS-ASSAULT §4.3).
    const rallyTimeout = assault ? profile.assaultRallyTimeoutMs : profile.rallyTimeoutMs;
    const gatheredFighters = visible.filter((u) => isMilitary(u) && chebyshev(u, at) <= RALLY_RADIUS).length;
    const enough = assault ? gatheredFighters >= profile.assaultMinWarriors : gathered >= profile.raidMinParty;
    if (everyone || (waited > rallyTimeout && enough)) {
      raid = {
        ...raid,
        phase: 'strike',
        ordered: [],
        waypoint: null,
        waypointTries: 0,
        gathered,
        rallyWaitMs: waited,
        stage: assault ? 'breach' : null,
      };
      return raidRule(view, profile, { ...memory, raid }, plan, nowMs, contraHumanos, rng);
    }
    if (waited > rallyTimeout) {
      // No se reunieron bastantes: la partida vuelve.
      return { ...memory, raid: forceReturn(raid, nowMs, profile, 'too_few') };
    }
    return { ...memory, raid };
  }
  raid = { ...raid, waypoint: marchStep(party, raid.rally, raid.waypointTries), ordered: [] };
  return { ...memory, raid: orderLeg(raid) };
}

// ─────────────────────────────────────────────────────────────
// El asalto (SPEC-BOTS-ASSAULT)
// ─────────────────────────────────────────────────────────────

/** El motivo de `assaultBlockReason` que sólo pide abrir la brecha (RN-BOT-014). */
const WALL_REASON = 'La muralla está en pie';

/** Los escoltas se quedan a esta distancia del centro, en el lado del punto de reunión. */
const ESCORT_DISTANCE = 3;

/** Si un motivo del HUD deja asaltar (ninguno) o sólo pide abrir antes la brecha. */
function assaultableReason(reason: string | null): boolean {
  return reason === null || reason.startsWith(WALL_REASON);
}

/** Si hay paz con ese jugador: pacto o alianza en vigor (RN-COMBAT-030). */
function atPeaceWith(view: View, playerId: string): boolean {
  return view.treaties.some(
    (t) => t.otherPlayerId === playerId && t.status === 'ACTIVE' && (t.type === 'NON_AGGRESSION' || t.type === 'ALLIANCE'),
  );
}

/**
 * El blanco de una partida de asalto (SPEC-BOTS-ASSAULT §4.2): la ciudad
 * asaltable más cercana, en brecha antes que amurallada a igual distancia. Las de
 * los bots, con lo que su dueño apunta en el directorio (era, inmunidad, muralla)
 * aunque no estén a la vista; si están a la vista, con `assaultBlockReason`, el
 * motivo del HUD. Con `contraHumanos`, también las ciudades ajenas a la vista
 * (RN-BOT-004). La paz de clan o una Safe Zone las dice el servidor al plantar.
 */
export function chooseAssaultTarget(
  view: View,
  memory: Memory,
  nowMs: number,
  contraHumanos: boolean,
  rng: Rng,
): Target | null {
  const home = center(view);
  const fighter = ownUnits(view).find(isMilitary);
  const usable = (id: number) => (memory.targetCooldowns[id] ?? 0) <= nowMs;
  const visible = new Map(view.foreignCities.map((c) => [c.id, c]));
  const reasonFor = (c: CityView) =>
    assaultBlockReason({
      unit: fighter,
      playerId: view.playerId,
      city: c,
      ownCity: view.city,
      buildings: view.foreignBuildings,
      treaties: view.treaties,
      nowMs,
    });
  const candidates: (Target & { d: number; walled: number })[] = [];
  const seen = new Set<number>();

  for (const b of view.botCities) {
    if (b.playerId === view.playerId || !isBotCity(b.name) || !usable(b.cityId)) continue;
    seen.add(b.cityId);
    if (atPeaceWith(view, b.playerId)) continue;
    const live = visible.get(b.cityId);
    let isWalled: boolean;
    if (live) {
      const reason = reasonFor(live);
      if (!assaultableReason(reason)) continue;
      isWalled = reason !== null;
    } else {
      if (!b.era || !eraAtLeast(b.era, 'BRONZE_AGE')) continue;
      if (b.fallImmuneUntilMs !== undefined && b.fallImmuneUntilMs > nowMs) continue;
      isWalled = b.walled !== false;
    }
    const c = { x: b.centerX, y: b.centerY };
    candidates.push({ cityId: b.cityId, name: b.name, center: c, d: chebyshev(home, c), walled: isWalled ? 1 : 0 });
  }
  if (contraHumanos) {
    for (const c of view.foreignCities) {
      if (c.ownerPlayerId === view.playerId || seen.has(c.id) || !usable(c.id)) continue;
      const reason = reasonFor(c);
      if (!assaultableReason(reason)) continue;
      const t = { x: c.centerX, y: c.centerY };
      candidates.push({ cityId: c.id, name: c.name, center: t, d: chebyshev(home, t), walled: reason === null ? 0 : 1 });
    }
  }
  if (candidates.length === 0) return null;
  candidates.sort((a, b) => a.d - b.d || a.walled - b.walled || a.cityId - b.cityId);
  const first = candidates[0] as (typeof candidates)[number];
  const best = candidates.filter((c) => c.d === first.d && c.walled === first.walled);
  const pick = best[rng.int(best.length)] as (typeof candidates)[number];
  return { cityId: pick.cityId, name: pick.name, center: pick.center };
}

/**
 * La partida vuelve: la de asalto apunta por qué (SPEC-BOTS-ASSAULT §4.4); la de
 * saqueo, como en E28, con el plazo vencido.
 */
function forceReturn(raid: Raid, nowMs: number, profile: Profile, reason: AssaultAbandon): Raid {
  if (raid.kind === 'assault') return { ...raid, abandon: raid.abandon ?? reason };
  return { ...raid, startedAtMs: nowMs - profile.raidTimeoutMs - 1 };
}

/**
 * Un asalto reunido (SPEC-BOTS-ASSAULT §4.3, §4.4; RN-BOT-014, RN-BOT-016): los
 * arietes contra la muralla completa, el estandarte con el militar más cercano
 * cuando la ciudad está en brecha, la escolta parada junto al anillo, la muralla
 * en obra derribada antes de completarse, y otro estandarte si el asalto se rompe.
 * Lo que pasa lo dice el espejo (`CityView.assault`): el bot no lo adivina.
 */
function assaultStrike(
  view: View,
  profile: Profile,
  raid: Raid,
  plan: Plan,
  nowMs: number,
  units: ReadonlyMap<number, UnitView>,
): Raid {
  const fighterIds = raid.warriors.filter((id) => !raid.rams.includes(id));
  const ramIds = raid.rams.filter((id) => raid.warriors.includes(id));
  if (fighterIds.length < profile.assaultMinWarriors) return { ...raid, abandon: 'too_few' };

  const victim = view.foreignCities.find((c) => c.id === raid.targetCityId);
  // La conexión de campaña mira al blanco: hasta que lo vea, se espera.
  if (!victim) return raid;

  const isWalled = walled(victim, view.foreignBuildings);
  const ours = victim.assault && victim.assault.playerId === view.playerId ? victim.assault : null;
  let r = raid;

  if (ours) {
    if (r.stage !== 'hold') r = { ...r, stage: 'hold', holder: ours.unitId, planted: r.planted + 1 };
  } else if (r.stage === 'hold') {
    // Se rompió (RN-SIEGE-011): por qué, con lo que se ve.
    const holder = r.holder === null ? undefined : units.get(r.holder);
    const dead = r.holder === null || !r.warriors.includes(r.holder) || holder?.status === 'DEAD';
    const lastBreak: AssaultBreak = dead
      ? 'holder_died'
      : isWalled
        ? 'wall_completed'
        : view.city.presenceState === 'PROTECTED'
          ? 'protected'
          : 'peace';
    r = { ...r, stage: isWalled ? 'breach' : 'plant', holder: null, breaks: r.breaks + 1, lastBreak };
    if (lastBreak === 'peace' || lastBreak === 'protected') return { ...r, abandon: 'blocked' };
  }

  const visibleIdle = (ids: readonly number[]) =>
    ids.map((id) => units.get(id)).filter((u): u is UnitView => u !== undefined && u.status === 'IDLE');
  const escortTile = rallyPoint(raid.target, raid.rally, ESCORT_DISTANCE, view.terrainAt);
  const escort = (exclude: number | null) => {
    for (const u of visibleIdle(fighterIds)) {
      if (plan.full) return;
      if (u.id === exclude || chebyshev(u, raid.target) <= ESCORT_DISTANCE + 1) continue;
      plan.add({ key: `assault:escort:${u.id}`, type: 'unit.move', unitId: u.id, target: escortTile });
    }
  };

  if (isWalled) {
    // La brecha: sólo el ariete golpea la muralla completa (RN-SIEGE-004, RN-BOT-014).
    if (ramIds.length === 0) return { ...r, abandon: 'no_rams' };
    r = { ...r, stage: 'breach', holder: null };
    const wall = view.foreignBuildings.find((b) => b.cityId === victim.id && b.type === 'WALL' && b.status === 'COMPLETE');
    if (wall) {
      for (const ram of visibleIdle(ramIds)) {
        if (plan.full) break;
        plan.add({ key: `assault:demolish:${ram.id}:${wall.id}`, type: 'unit.demolish', unitId: ram.id, buildingId: wall.id });
      }
    }
    escort(null);
    return r;
  }

  // En brecha. Una muralla en obra se derriba antes de que se complete (RN-BOT-016):
  // con los arietes y, si no hay, con la escolta.
  const rising = view.foreignBuildings.find(
    (b) => b.cityId === victim.id && b.type === 'WALL' && b.status !== 'COMPLETE' && b.status !== 'DESTROYED',
  );
  if (rising) {
    const breakers = ramIds.length > 0 ? visibleIdle(ramIds) : visibleIdle(fighterIds).filter((u) => u.id !== r.holder);
    for (const u of breakers) {
      if (plan.full) break;
      plan.add({ key: `assault:demolish:${u.id}:${rising.id}`, type: 'unit.demolish', unitId: u.id, buildingId: rising.id });
    }
  }

  if (ours) {
    escort(ours.unitId);
    return r;
  }
  // Otro jugador ya la asalta: no se puede (RN-SIEGE-010).
  if (victim.assault) return { ...r, abandon: 'blocked' };

  // El portaestandarte va de camino o ya lo sostiene y el delta aún no llegó.
  if (r.stage === 'plant' && r.holder !== null && r.warriors.includes(r.holder)) {
    const h = units.get(r.holder);
    if (h && (h.status === 'MOVING' || h.status === 'CLAIMING')) {
      escort(r.holder);
      return r;
    }
  }

  // El estandarte: el militar parado más cercano al centro (empate: menor id).
  const candidates = visibleIdle(fighterIds).sort(
    (a, b) => chebyshev(a, raid.target) - chebyshev(b, raid.target) || a.id - b.id,
  );
  for (const u of candidates) {
    if (plan.full) break;
    const reason = assaultBlockReason({
      unit: u,
      playerId: view.playerId,
      city: victim,
      ownCity: view.city,
      buildings: view.foreignBuildings,
      treaties: view.treaties,
      nowMs,
    });
    // Lo que el HUD tendría deshabilitado no se envía (RN-BOT-014): la partida vuelve.
    if (reason !== null) return { ...r, abandon: 'blocked' };
    const key = `assault:plant:${u.id}`;
    if (!plan.free(key)) continue;
    plan.add({ key, type: 'unit.assault', unitId: u.id, cityId: victim.id });
    escort(u.id);
    return { ...r, stage: 'plant', holder: u.id };
  }
  return r;
}

// ─────────────────────────────────────────────────────────────
// La función
// ─────────────────────────────────────────────────────────────

export interface DecideOptions {
  readonly contraHumanos: boolean;
  /** `--sin-comercio` lo apaga: la ejecución de E28, para comparar (SPEC-BOTS-TRADE §4.8). */
  readonly comercio?: boolean;
  /** `--con-humanos` (RN-BOT-008). */
  readonly conHumanos?: boolean;
}

/** Hasta dos comandos para este despertar, y la memoria nueva (RN-BOT-003, RN-BOT-005). */
export function decide(
  view: View,
  profile: Profile,
  memory: Memory,
  nowMs: number,
  rng: Rng,
  opts: DecideOptions = { contraHumanos: false },
): Decision {
  const plan = new Plan(view.city, memory.cooldowns, nowMs);
  let mem = memory;

  // La partida en curso va primero: sus órdenes tienen plazo (el saqueo acaba solo).
  if (profile.raids && mem.raid !== null) mem = raidRule(view, profile, mem, plan, nowMs, opts.contraHumanos, rng);
  mem = gatherRule(view, profile, mem, plan, nowMs);
  houseRule(view, profile, plan);
  villagerRule(view, profile, plan);
  if (profile.raids) militaryRule(view, profile, plan);
  advanceRule(profile, plan);
  bronzeRule(view, profile, plan);
  researchRule(profile, plan);
  if (opts.comercio !== false) {
    mem = commerceRule(view, profile, mem, plan, nowMs, { conHumanos: opts.conHumanos === true });
  }
  if (!profile.raids) militaryRule(view, profile, plan);
  if (profile.raids && mem.raid === null) mem = raidRule(view, profile, mem, plan, nowMs, opts.contraHumanos, rng);

  return { commands: plan.commands, memory: mem };
}

// ─────────────────────────────────────────────────────────────
// Los rechazos
// ─────────────────────────────────────────────────────────────

/** Los códigos con que un blanco no se puede alcanzar o atacar. */
const TARGET_CODES = new Set(['CITY_PROTECTED', 'PATH_TOO_LONG', 'PATH_NOT_FOUND', 'TREATY_REQUIRED']);

/** Los códigos de una ruta que no existe o no cabe en los topes del A* (RN-CARAVAN-002). */
const NO_ROUTE_CODES = new Set(['PATH_TOO_LONG', 'PATH_NOT_FOUND']);

function withItem<T>(list: readonly T[], item: T): readonly T[] {
  return list.includes(item) ? list : [...list, item];
}

/**
 * Lo que la memoria aprende de un rechazo (RN-BOT-002): la orden se enfría, el
 * tile que no admitió la recolección se aparta y la partida cambia de fase o de
 * blanco. Pura, como `decide`.
 */
export function onRejected(
  memory: Memory,
  command: Command,
  code: string,
  nowMs: number,
  profile: Profile,
): Memory {
  let mem: Memory = {
    ...memory,
    cooldowns: { ...memory.cooldowns, [command.key]: nowMs + profile.rejectCooldownMs },
  };

  if (command.type === 'unit.gather') {
    mem = {
      ...mem,
      badTiles: { ...mem.badTiles, [tileKey(command.target)]: nowMs + profile.targetCooldownMs },
      stoneUnits: mem.stoneUnits.filter((id) => id !== command.unitId),
    };
    // Las colinas quedan lejos de las ciudades: si una no tiene ruta, las demás tampoco
    // suelen tenerla. La piedra se aparta un rato y los aldeanos van a la madera.
    if (command.resource === 'STONE' && (code === 'PATH_TOO_LONG' || code === 'PATH_NOT_FOUND')) {
      mem = { ...mem, stoneBlockedUntilMs: nowMs + profile.targetCooldownMs };
    }
  }

  if (command.type === 'treaty.propose') {
    return { ...mem, treatyCooldowns: { ...mem.treatyCooldowns, [command.playerId]: nowMs + profile.treatyRetryMs } };
  }
  if (command.type === 'trade.propose') {
    // Sin ruta entre depósitos, la pareja se aparta hasta el final (RN-BOT-013); un
    // blanco protegido o sin tratado, como un blanco inalcanzable (§4.2, §5).
    if (NO_ROUTE_CODES.has(code)) {
      return { ...mem, unreachablePlayers: withItem(mem.unreachablePlayers, command.playerId) };
    }
    const wait = TARGET_CODES.has(code) ? profile.targetCooldownMs : profile.tradeRetryMs;
    return { ...mem, tradeCooldowns: { ...mem.tradeCooldowns, [command.playerId]: nowMs + wait } };
  }
  if (command.type === 'trade.respond' && command.accept && NO_ROUTE_CODES.has(code)) {
    return { ...mem, unreachablePlayers: withItem(mem.unreachablePlayers, command.otherPlayerId) };
  }
  if (command.type === 'market.buy' && NO_ROUTE_CODES.has(code)) {
    return { ...mem, unreachableOffers: withItem(mem.unreachableOffers, command.offerId) };
  }
  if (command.type === 'market.buy' && TARGET_CODES.has(code)) {
    return { ...mem, cooldowns: { ...mem.cooldowns, [command.key]: nowMs + profile.targetCooldownMs } };
  }

  if (command.type === 'unit.move' && (command.key.startsWith('raid:recall:') || command.key.startsWith('raid:home:'))) {
    const { [command.key]: _drop, ...cooldowns } = mem.cooldowns;
    const n = mem.recallTries[command.unitId] ?? 0;
    return { ...mem, cooldowns, recallTries: { ...mem.recallTries, [command.unitId]: n + 1 } };
  }

  const raid = mem.raid;
  if (raid === null) return mem;

  if (command.type === 'unit.pillage' && command.key.startsWith('raid:')) {
    // Desde el punto de reunión, a 16 tiles: sin ruta, el blanco no se alcanza.
    if (TARGET_CODES.has(code) || code === 'INVALID_TARGET') {
      return {
        ...mem,
        targetCooldowns: { ...mem.targetCooldowns, [raid.targetCityId]: nowMs + profile.targetCooldownMs },
        raid: forceReturn(raid, nowMs, profile, 'blocked'),
      };
    }
  }

  if (command.type === 'unit.assault' || (command.type === 'unit.demolish' && command.key.startsWith('assault:'))) {
    // El servidor no deja plantar el estandarte o golpear la muralla (inmune, en paz,
    // protegida, sin ruta al anillo): el blanco se enfría y la partida vuelve (§5).
    if (TARGET_CODES.has(code) || code === 'INVALID_TARGET' || code === 'CITY_NOT_FOUND') {
      return {
        ...mem,
        targetCooldowns: { ...mem.targetCooldowns, [raid.targetCityId]: nowMs + profile.targetCooldownMs },
        raid: forceReturn(raid, nowMs, profile, 'blocked'),
      };
    }
    return mem;
  }

  if (command.type === 'unit.move' && command.key.startsWith('raid:march:')) {
    // El destino del tramo no se pisa o no hay ruta: se desplaza en espiral.
    const { [command.key]: _drop, ...cooldowns } = mem.cooldowns;
    return { ...mem, cooldowns, raid: { ...raid, waypoint: null, ordered: [], waypointTries: raid.waypointTries + 1 } };
  }

  return mem;
}

/** Un guerrero de la partida murió (aviso `UNIT_LOST`). */
export function onRaidLoss(memory: Memory, unitId: number): Memory {
  const raid = memory.raid;
  if (raid === null || !raid.warriors.includes(unitId)) return memory;
  return {
    ...memory,
    raid: {
      ...raid,
      warriors: raid.warriors.filter((id) => id !== unitId),
      ordered: raid.ordered.filter((id) => id !== unitId),
      rams: raid.rams.filter((id) => id !== unitId),
      lost: raid.lost + 1,
    },
  };
}

/**
 * La ciudad que asaltaba la partida cayó (`city.fallen`): vuelve a casa
 * (SPEC-BOTS-ASSAULT §4.4). Un `city.fallen` repetido no cambia nada.
 */
export function onTargetFell(memory: Memory, cityId: number): Memory {
  const raid = memory.raid;
  if (raid === null || raid.kind !== 'assault' || raid.targetCityId !== cityId || raid.abandon === 'fell') return memory;
  return { ...memory, raid: { ...raid, abandon: 'fell' } };
}

/**
 * La ciudad propia cayó y el bot sigue en la refundada (RN-BOT-017): olvida lo de
 * la ciudad (los rechazos de obras y encargos, los tiles de recolección, las
 * ofertas del mercado, que se retiraron) y conserva lo del jugador (blancos,
 * tratados, parejas sin ruta, la partida en curso, cuyas unidades pasan a la
 * ciudad nueva).
 */
export function onOwnCityFell(memory: Memory): Memory {
  const cooldowns: Record<string, number> = {};
  for (const [k, v] of Object.entries(memory.cooldowns)) {
    if (k.startsWith('raid:') || k.startsWith('assault:')) cooldowns[k] = v;
  }
  return { ...memory, cooldowns, badTiles: {}, stoneUnits: [], stoneBlockedUntilMs: 0, marketSeen: {} };
}

/** Dónde tiene que mirar la conexión de campaña, o `null` si no hay partida. */
export function fieldFocus(memory: Memory): Tile | null {
  const raid = memory.raid;
  if (raid === null) return null;
  if (raid.phase === 'march' && raid.waypoint) return raid.waypoint;
  return raid.target;
}

/** Las costas que el HUD mostraría, reexportadas para los tests. */
export const COSTS = { HOUSE_WOOD, WALL_COST, WORKSHOP_COST, CRAFT_COST };
export type { Profile };
