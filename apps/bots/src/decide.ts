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
import {
  TERRAIN_FOREST,
  advanceBlockReason,
  civilizationUnitBlockReason,
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
import {
  HOME_RADIUS,
  HOUSE_WOOD,
  MARCH_STEP,
  MAX_COMMANDS_PER_THINK,
  STRIKE_RADIUS,
  type Profile,
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
}

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
  | {
      readonly key: string;
      readonly type: 'city.build';
      readonly cityId: number;
      readonly building: 'HOUSE' | 'WORKSHOP' | 'WALL';
      readonly tile: Tile;
    }
  | { readonly key: string; readonly type: 'city.train'; readonly cityId: number; readonly unitType: string }
  | { readonly key: string; readonly type: 'city.advance'; readonly cityId: number }
  | { readonly key: string; readonly type: 'city.craft'; readonly cityId: number }
  | { readonly key: string; readonly type: 'city.research'; readonly cityId: number; readonly technology: string };

export type RaidPhase = 'strike' | 'march';

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
}

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
}

export const EMPTY_MEMORY: Memory = {
  cooldowns: {},
  badTiles: {},
  stoneUnits: [],
  raid: null,
  raidWaitUntilMs: 0,
  targetCooldowns: {},
  stoneBlockedUntilMs: 0,
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

  get full(): boolean {
    return this.commands.length >= MAX_COMMANDS_PER_THINK;
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
  const military = ownUnits(view).filter(isMilitary).length;
  return Math.max(0, view.city.population - military);
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
  const reserve = profile.workshop && !standing.some((b) => b.type === 'WORKSHOP') ? 1 : 0;
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
  if (militaryCount(view) >= want) return;
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

function raidRule(view: View, profile: Profile, memory: Memory, plan: Plan, nowMs: number, contraHumanos: boolean, rng: Rng): Memory {
  if (!profile.raids) return memory;
  const units = new Map(ownUnits(view).map((u) => [u.id, u]));
  const home = depositTile(view.city, view.terrainAt);
  let raid = memory.raid;

  if (raid === null) {
    if (plan.full || nowMs < memory.raidWaitUntilMs) return memory;
    const party = [...units.values()].filter(
      (u) => isMilitary(u) && u.status === 'IDLE' && atHome(view, u) && u.carrying === 0,
    );
    if (party.length < profile.raidMinParty) return memory;
    const target = chooseTarget(view, memory, nowMs, contraHumanos, rng);
    if (!target) return memory;
    raid = {
      targetCityId: target.cityId,
      targetName: target.name,
      target: target.center,
      warriors: party.map((u) => u.id),
      startedAtMs: nowMs,
      phase: 'strike',
      ordered: [],
      waypoint: null,
      waypointTries: 0,
      lost: 0,
      size: party.length,
      departed: false,
    };
  }

  // Los que la memoria da por vivos; los muertos los quita el bot al recibir UNIT_LOST.
  const alive = raid.warriors.filter((id) => units.get(id)?.status !== 'DEAD');
  const visible = alive.map((id) => units.get(id)).filter((u): u is UnitView => u !== undefined);
  const timedOut = nowMs - raid.startedAtMs > profile.raidTimeoutMs;

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
  for (const u of visible) {
    if (plan.full) break;
    if (u.status !== 'IDLE' || (!raid.ordered.includes(u.id) && !timedOut)) continue;
    const nearTarget = chebyshev(u, raid.target) <= 3;
    const full = u.carrying >= lootCarry(u.unitType);
    if (atHome(view, u)) continue;
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
      plan.add({ key: `raid:home:${u.id}`, type: 'unit.move', unitId: u.id, target: home });
    }
  }

  if (timedOut) return { ...memory, raid };

  if (raid.phase === 'strike') {
    for (const id of alive) {
      if (plan.full) break;
      if (raid.ordered.includes(id)) continue;
      const key = `raid:pillage:${id}`;
      if (!plan.free(key)) continue;
      plan.add({ key, type: 'unit.pillage', unitId: id, cityId: raid.targetCityId });
      raid = { ...raid, ordered: [...raid.ordered, id] };
    }
    return { ...memory, raid };
  }

  // En marcha: cuando todos los que se ven están parados, el tramo siguiente.
  if (visible.length === 0 || visible.some((u) => u.status === 'MOVING')) return { ...memory, raid };
  const party = {
    x: Math.round(visible.reduce((s, u) => s + u.x, 0) / visible.length),
    y: Math.round(visible.reduce((s, u) => s + u.y, 0) / visible.length),
  };
  if (chebyshev(party, raid.target) <= STRIKE_RADIUS) {
    return { ...memory, raid: { ...raid, phase: 'strike', ordered: [], waypoint: null, waypointTries: 0 } };
  }
  if (raid.waypoint === null || raid.ordered.length >= alive.length) {
    raid = { ...raid, waypoint: marchStep(party, raid.target, raid.waypointTries), ordered: [] };
  }
  for (const id of alive) {
    if (plan.full) break;
    if (raid.ordered.includes(id) || !raid.waypoint) continue;
    const key = `raid:march:${id}:${raid.waypoint.x},${raid.waypoint.y}`;
    if (!plan.free(key)) continue;
    plan.add({ key, type: 'unit.move', unitId: id, target: raid.waypoint });
    raid = { ...raid, ordered: [...raid.ordered, id] };
  }
  return { ...memory, raid };
}

// ─────────────────────────────────────────────────────────────
// La función
// ─────────────────────────────────────────────────────────────

export interface DecideOptions {
  readonly contraHumanos: boolean;
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
  if (!profile.raids) militaryRule(view, profile, plan);
  if (profile.raids && mem.raid === null) mem = raidRule(view, profile, mem, plan, nowMs, opts.contraHumanos, rng);

  return { commands: plan.commands, memory: mem };
}

// ─────────────────────────────────────────────────────────────
// Los rechazos
// ─────────────────────────────────────────────────────────────

/** Los códigos con que un blanco no se puede alcanzar o atacar. */
const TARGET_CODES = new Set(['CITY_PROTECTED', 'PATH_TOO_LONG', 'PATH_NOT_FOUND', 'TREATY_REQUIRED']);

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

  const raid = mem.raid;
  if (raid === null) return mem;

  if (command.type === 'unit.pillage' && command.key.startsWith('raid:')) {
    if (code === 'PATH_TOO_LONG' || code === 'PATH_NOT_FOUND') {
      // Demasiado lejos para una sola ruta: por tramos.
      const { [command.key]: _drop, ...cooldowns } = mem.cooldowns;
      return { ...mem, cooldowns, raid: { ...raid, phase: 'march', ordered: [], waypoint: null, waypointTries: 0 } };
    }
    if (TARGET_CODES.has(code) || code === 'INVALID_TARGET') {
      return {
        ...mem,
        targetCooldowns: { ...mem.targetCooldowns, [raid.targetCityId]: nowMs + profile.targetCooldownMs },
        raid: { ...raid, startedAtMs: nowMs - profile.raidTimeoutMs - 1 },
      };
    }
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
      lost: raid.lost + 1,
    },
  };
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
