/**
 * La partida de asalto de los bots (SPEC-BOTS-ASSAULT §4.1 a §4.6, RN-BOT-014 a
 * RN-BOT-017): los arietes, el blanco, la reunión, la brecha, el estandarte, lo
 * que lo rompe y la caída.
 */
import { describe, expect, it } from 'vitest';

import type { TreatyView } from '../../web/src/state/diplomacy';
import type { CityView, UnitView } from '../../web/src/state/world';
import {
  EMPTY_MEMORY,
  RAID_ASSAULT_FIELDS,
  chooseAssaultTarget,
  decide,
  onOwnCityFell,
  onRaidLoss,
  onRejected,
  onTargetFell,
  villagerCount,
  type Command,
  type Memory,
  type Raid,
} from './decide';
import { ME, OTHER_BOT, botCity, building, city, stocks, unit, view } from './fixtures.test-helpers';
import { PROFILES } from './profiles';
import { createRng } from './random';

const NOW = 1_757_376_000_000;
const saqueador = PROFILES.saqueador;
const rng = () => createRng(42);
const solo = { contraHumanos: false, comercio: false } as const;
const OTHER_BOT_2 = '44444444-4444-4444-8444-444444444444';

/** La ciudad del saqueador en Hierro, con población de sobra para no entrenar aldeanos. */
const ironCity = (over: Partial<CityView> = {}) =>
  city({
    name: 'bot_saqueador_1polis',
    era: 'IRON_AGE',
    population: 16,
    populationLimit: 40,
    stocks: stocks(0),
    ...over,
  });

const warrior = (id: number, over: Partial<UnitView> = {}) => unit({ id, unitType: 'WARRIOR', hp: 60, maxHp: 60, ...over });
const ram = (id: number, over: Partial<UnitView> = {}) => unit({ id, unitType: 'RAM', hp: 250, maxHp: 250, ...over });

/** La víctima, a la vista: Bronce, de otro bot, en (140,100). */
const victim = (over: Partial<CityView> = {}) =>
  city({ id: 20, ownerPlayerId: OTHER_BOT, name: 'bot_constructor_2polis', centerX: 140, centerY: 100, era: 'BRONZE_AGE', ...over });
const WALL = building({ id: 50, cityId: 20, type: 'WALL', x: 140, y: 100, status: 'COMPLETE' });

/** Una partida de asalto ya reunida en (124,100), a 16 tiles del blanco. */
const assaultRaid = (over: Partial<Raid> = {}): Raid => ({
  targetCityId: 20,
  targetName: 'bot_constructor_2polis',
  target: { x: 140, y: 100 },
  warriors: [1, 2, 3, 4, 5, 6],
  startedAtMs: NOW,
  phase: 'strike',
  ordered: [],
  waypoint: null,
  waypointTries: 0,
  lost: 0,
  size: 6,
  departed: true,
  rally: { x: 124, y: 100 },
  rallyAtMs: NOW,
  gathered: 6,
  rallyWaitMs: 0,
  ...RAID_ASSAULT_FIELDS,
  kind: 'assault',
  rams: [5, 6],
  stage: 'breach',
  ...over,
});

const withRaid = (raid: Raid): Memory => ({ ...EMPTY_MEMORY, raid });

/** RN-BOT-014: un ariete sólo recibe órdenes de mover o derribar. */
function expectRamsOnlyMoveOrDemolish(cs: readonly Command[], rams: readonly number[] = [5, 6]): void {
  for (const c of cs) {
    if ('unitId' in c && rams.includes(c.unitId)) expect(['unit.move', 'unit.demolish']).toContain(c.type);
  }
}

function treaty(over: Partial<TreatyView> = {}): TreatyView {
  return {
    id: 7,
    otherPlayerId: OTHER_BOT,
    otherCityId: 20,
    otherCityName: 'bot_constructor_2polis',
    type: 'NON_AGGRESSION',
    status: 'ACTIVE',
    proposedByMe: false,
    breaksAtMs: null,
    ...over,
  };
}

describe('los arietes (SPEC-BOTS-ASSAULT §4.1)', () => {
  const six = [1, 2, 3, 4, 7, 8].map((id) => warrior(id));

  it('en Hierro, con los militares completos, entrena arietes hasta los de la partida', () => {
    const v = view({ city: ironCity({ stocks: stocks(1000, 500) }), units: six });
    const { commands } = decide(v, saqueador, EMPTY_MEMORY, NOW, rng(), solo);
    expect(commands).toContainEqual(expect.objectContaining({ type: 'city.train', unitType: 'RAM' }));

    const withRams = view({ city: ironCity({ stocks: stocks(1000, 500) }), units: [...six, ram(5), ram(6)] });
    expect(decide(withRams, saqueador, EMPTY_MEMORY, NOW, rng(), solo).commands).not.toContainEqual(
      expect.objectContaining({ unitType: 'RAM' }),
    );
  });

  it('antes de Hierro, o sin piedra, no lo pide (lo que el HUD deshabilita, RN-BOT-014)', () => {
    const bronze = view({ city: ironCity({ era: 'BRONZE_AGE', stocks: stocks(1000, 500) }), units: six });
    expect(decide(bronze, saqueador, EMPTY_MEMORY, NOW, rng(), solo).commands).not.toContainEqual(
      expect.objectContaining({ unitType: 'RAM' }),
    );
    const poor = view({ city: ironCity({ stocks: stocks(1000, 10) }), units: six });
    expect(decide(poor, saqueador, EMPTY_MEMORY, NOW, rng(), solo).commands).not.toContainEqual(
      expect.objectContaining({ unitType: 'RAM' }),
    );
  });

  it('el constructor no entrena arietes (Q1)', () => {
    const v = view({ city: ironCity({ stocks: stocks(1000, 500) }), units: [warrior(1), warrior(2)] });
    expect(decide(v, PROFILES.constructor, EMPTY_MEMORY, NOW, rng(), solo).commands).not.toContainEqual(
      expect.objectContaining({ unitType: 'RAM' }),
    );
  });

  it('un ariete ocupa dos plazas: no cuenta como aldeano', () => {
    const v = view({ city: city({ population: 10 }), units: [warrior(1), warrior(2), ram(5)] });
    expect(villagerCount(v)).toBe(6);
  });
});

describe('el blanco del asalto (SPEC-BOTS-ASSAULT §4.2)', () => {
  const base = { city: ironCity(), units: [warrior(1)] };
  const walledBot = botCity({ era: 'BRONZE_AGE', walled: true });
  const openBot = botCity({ playerId: OTHER_BOT_2, cityId: 21, name: 'bot_constructor_3polis', centerX: 140, centerY: 110, era: 'IRON_AGE', walled: false });

  it('a igual distancia, la que está en brecha', () => {
    const v = view({ ...base, botCities: [walledBot, openBot] });
    expect(chooseAssaultTarget(v, EMPTY_MEMORY, NOW, false, rng())?.cityId).toBe(21);
  });

  it('descarta la inmune, la de Piedra, la de era desconocida y la que está en paz', () => {
    const immune = { ...openBot, fallImmuneUntilMs: NOW + 1 };
    expect(chooseAssaultTarget(view({ ...base, botCities: [walledBot, immune] }), EMPTY_MEMORY, NOW, false, rng())?.cityId).toBe(20);
    const stone = { ...openBot, era: 'STONE_AGE' };
    const unknown = { ...walledBot, era: undefined };
    expect(chooseAssaultTarget(view({ ...base, botCities: [stone, unknown] }), EMPTY_MEMORY, NOW, false, rng())).toBeNull();
    const peace = view({ ...base, botCities: [walledBot], treaties: [treaty()] });
    expect(chooseAssaultTarget(peace, EMPTY_MEMORY, NOW, false, rng())).toBeNull();
  });

  it('una ciudad a la vista se juzga con el motivo del HUD: con otro asalto no se elige', () => {
    const other = victim({ assault: { playerId: OTHER_BOT_2, unitId: 99, startedAtMs: NOW, endsAtMs: NOW + 900_000 } });
    const v = view({ ...base, botCities: [walledBot], foreignCities: [other] });
    expect(chooseAssaultTarget(v, EMPTY_MEMORY, NOW, false, rng())).toBeNull();
    // Amurallada pero asaltable: el motivo de la muralla sólo pide abrir la brecha.
    const w = view({ ...base, botCities: [walledBot], foreignCities: [victim()], foreignBuildings: [WALL] });
    expect(chooseAssaultTarget(w, EMPTY_MEMORY, NOW, false, rng())?.cityId).toBe(20);
  });

  it('sin --contra-humanos, sólo bots (RN-BOT-004, Q6)', () => {
    const human = victim({ id: 30, ownerPlayerId: '33333333-3333-4333-8333-333333333333', name: 'smoke_caidapolis' });
    expect(chooseAssaultTarget(view({ ...base, foreignCities: [human] }), EMPTY_MEMORY, NOW, false, rng())).toBeNull();
    expect(chooseAssaultTarget(view({ ...base, foreignCities: [human] }), EMPTY_MEMORY, NOW, true, rng())?.cityId).toBe(30);
  });
});

describe('la partida de asalto (RN-BOT-015)', () => {
  const party = [warrior(1), warrior(2), warrior(3), warrior(4), ram(5), ram(6)];

  it('en Hierro, con 4 militares y 2 arietes en casa y un blanco, sale la de asalto, primero a reunirse', () => {
    const v = view({ city: ironCity(), units: party, botCities: [botCity({ era: 'BRONZE_AGE', walled: true })] });
    const { commands, memory } = decide(v, saqueador, EMPTY_MEMORY, NOW, rng(), solo);
    expect(memory.raid).toMatchObject({ kind: 'assault', warriors: [1, 2, 3, 4, 5, 6], rams: [5, 6], phase: 'march' });
    expect(types(commands)).toEqual(['unit.move', 'unit.move']);
  });

  it('sin los arietes, o antes de Hierro, sale la de saqueo (Q2)', () => {
    const noRams = view({ city: ironCity(), units: party.slice(0, 4), botCities: [botCity({ era: 'BRONZE_AGE' })] });
    expect(decide(noRams, saqueador, EMPTY_MEMORY, NOW, rng(), solo).memory.raid).toMatchObject({ kind: 'pillage', warriors: [1, 2, 3, 4] });
    const bronze = view({ city: ironCity({ era: 'BRONZE_AGE' }), units: party, botCities: [botCity({ era: 'BRONZE_AGE' })] });
    expect(decide(bronze, saqueador, EMPTY_MEMORY, NOW, rng(), solo).memory.raid).toMatchObject({ kind: 'pillage', warriors: [1, 2, 3, 4] });
  });

  it('la reunión espera a los arietes hasta su plazo, más largo que el del saqueo', () => {
    const marching = assaultRaid({ phase: 'march', stage: null, waypoint: { x: 124, y: 100 }, ordered: [1, 2, 3, 4, 5, 6], rallyAtMs: NOW });
    const atRally = [1, 2, 3, 4].map((id) => warrior(id, { x: 124, y: 100 }));
    const lateRams = [ram(5, { x: 110, y: 100 }), ram(6, { x: 110, y: 100 })];
    const v = view({ city: ironCity(), units: [...atRally, ...lateRams], foreignCities: [victim()], foreignBuildings: [WALL] });
    // A los 5 min (más que el plazo de saqueo, menos que el de asalto), se sigue esperando.
    const waiting = decide(v, saqueador, withRaid(marching), NOW + 5 * 60_000, rng(), solo);
    expect(waiting.memory.raid?.phase).toBe('march');
    // Vencido el plazo de asalto, sale con los que hay si son bastantes.
    const late = decide(v, saqueador, withRaid(marching), NOW + 11 * 60_000, rng(), solo);
    expect(late.memory.raid).toMatchObject({ phase: 'strike', gathered: 4 });
  });
});

describe('el asalto reunido (SPEC-BOTS-ASSAULT §4.3, §4.4)', () => {
  const atRally = [1, 2, 3, 4].map((id) => warrior(id, { x: 124, y: 100 }));
  const rams = [ram(5, { x: 124, y: 100 }), ram(6, { x: 124, y: 100 })];
  const near = [warrior(1, { x: 137, y: 100 }), warrior(2, { x: 137, y: 101 }), warrior(3, { x: 136, y: 99 }), warrior(4, { x: 124, y: 100 })];

  it('amurallada: los arietes golpean la muralla y nadie planta el estandarte', () => {
    const v = view({ city: ironCity(), units: [...atRally, ...rams], foreignCities: [victim()], foreignBuildings: [WALL] });
    const { commands, memory } = decide(v, saqueador, withRaid(assaultRaid()), NOW, rng(), solo);
    expect(commands).toEqual([
      { key: 'assault:demolish:5:50', type: 'unit.demolish', unitId: 5, buildingId: 50 },
      { key: 'assault:demolish:6:50', type: 'unit.demolish', unitId: 6, buildingId: 50 },
    ]);
    expect(memory.raid?.stage).toBe('breach');

    // Con los arietes golpeando, la escolta se acerca al anillo (a 3 tiles, en el lado de la reunión).
    const busy = view({
      city: ironCity(),
      units: [...atRally, ram(5, { status: 'ATTACKING' }), ram(6, { status: 'ATTACKING' })],
      foreignCities: [victim()],
      foreignBuildings: [WALL],
    });
    const next = decide(busy, saqueador, withRaid(assaultRaid()), NOW + 2_000, rng(), solo);
    expect(next.commands).toEqual([
      { key: 'assault:escort:1', type: 'unit.move', unitId: 1, target: { x: 137, y: 100 } },
      { key: 'assault:escort:2', type: 'unit.move', unitId: 2, target: { x: 137, y: 100 } },
    ]);
    expectRamsOnlyMoveOrDemolish([...commands, ...next.commands]);
  });

  it('en brecha: el militar parado más cercano planta el estandarte y la escolta se acerca', () => {
    const v = view({ city: ironCity(), units: [...near, ...rams], foreignCities: [victim()] });
    const { commands, memory } = decide(v, saqueador, withRaid(assaultRaid()), NOW, rng(), solo);
    expect(commands).toEqual([
      { key: 'assault:plant:1', type: 'unit.assault', unitId: 1, cityId: 20 },
      { key: 'assault:escort:4', type: 'unit.move', unitId: 4, target: { x: 137, y: 100 } },
    ]);
    expect(memory.raid).toMatchObject({ stage: 'plant', holder: 1 });
  });

  it('mientras el portaestandarte camina o sostiene sin que llegue el delta, no se planta otro', () => {
    const walking = view({ city: ironCity(), units: [warrior(1, { status: 'MOVING' }), ...near.slice(1), ...rams], foreignCities: [victim()] });
    const { commands } = decide(walking, saqueador, withRaid(assaultRaid({ stage: 'plant', holder: 1 })), NOW, rng(), solo);
    expect(types(commands)).not.toContain('unit.assault');
  });

  it('con el asalto en la ciudad, la partida lo sostiene y lo cuenta una vez', () => {
    const held = victim({ assault: { playerId: ME, unitId: 1, startedAtMs: NOW, endsAtMs: NOW + 900_000 } });
    const v = view({ city: ironCity(), units: [warrior(1, { status: 'CLAIMING' }), ...near.slice(1), ...rams], foreignCities: [held] });
    const first = decide(v, saqueador, withRaid(assaultRaid({ stage: 'plant', holder: 1 })), NOW, rng(), solo);
    expect(first.memory.raid).toMatchObject({ stage: 'hold', holder: 1, planted: 1 });
    expect(types(first.commands)).not.toContain('unit.assault');
    const again = decide(v, saqueador, first.memory, NOW + 2_000, rng(), solo);
    expect(again.memory.raid?.planted).toBe(1);
  });

  it('RN-BOT-016: muere el portaestandarte y otro planta el estandarte', () => {
    const memory = onRaidLoss(withRaid(assaultRaid({ stage: 'hold', holder: 1, planted: 1 })), 1);
    const v = view({ city: ironCity(), units: [...near.slice(1), ...rams], foreignCities: [victim()] });
    const { commands, memory: after } = decide(v, saqueador, memory, NOW, rng(), solo);
    expect(after.raid).toMatchObject({ breaks: 1, lastBreak: 'holder_died', stage: 'plant', holder: 2 });
    expect(commands[0]).toEqual({ key: 'assault:plant:2', type: 'unit.assault', unitId: 2, cityId: 20 });
  });

  it('RN-BOT-016: una muralla en obra se derriba antes de completarse', () => {
    const held = victim({ assault: { playerId: ME, unitId: 1, startedAtMs: NOW, endsAtMs: NOW + 900_000 } });
    const rising = building({ id: 51, cityId: 20, type: 'WALL', x: 140, y: 100, status: 'UNDER_CONSTRUCTION' });
    const v = view({ city: ironCity(), units: [warrior(1, { status: 'CLAIMING' }), ...near.slice(1), ...rams], foreignCities: [held], foreignBuildings: [rising] });
    const { commands } = decide(v, saqueador, withRaid(assaultRaid({ stage: 'hold', holder: 1, planted: 1 })), NOW, rng(), solo);
    expect(commands).toEqual([
      { key: 'assault:demolish:5:51', type: 'unit.demolish', unitId: 5, buildingId: 51 },
      { key: 'assault:demolish:6:51', type: 'unit.demolish', unitId: 6, buildingId: 51 },
    ]);
    // Sin arietes, la escolta (nunca el portaestandarte).
    const noRams = decide(
      view({ city: ironCity(), units: [warrior(1, { status: 'CLAIMING' }), ...near.slice(1)], foreignCities: [held], foreignBuildings: [rising] }),
      saqueador,
      withRaid(assaultRaid({ stage: 'hold', holder: 1, planted: 1, warriors: [1, 2, 3, 4], rams: [] })),
      NOW,
      rng(),
      solo,
    );
    expect(noRams.commands.map((c) => ('unitId' in c ? c.unitId : null))).toEqual([2, 3]);
    expect(types(noRams.commands)).toEqual(['unit.demolish', 'unit.demolish']);
  });

  it('la muralla se completa: el asalto se rompe y los arietes vuelven a por ella', () => {
    const v = view({ city: ironCity(), units: [warrior(1, { x: 137, y: 100 }), ...near.slice(1), ...rams], foreignCities: [victim()], foreignBuildings: [WALL] });
    const { commands, memory } = decide(v, saqueador, withRaid(assaultRaid({ stage: 'hold', holder: 1, planted: 1 })), NOW, rng(), solo);
    expect(memory.raid).toMatchObject({ breaks: 1, lastBreak: 'wall_completed', stage: 'breach', holder: null });
    expect(types(commands)).toEqual(['unit.demolish', 'unit.demolish']);
  });

  it('vuelve con menos de 2 militares, sin arietes contra una amurallada, o al vencer su plazo', () => {
    const few = decide(
      view({ city: ironCity(), units: [warrior(1, { x: 137, y: 100 }), ...rams], foreignCities: [victim()] }),
      saqueador,
      withRaid(assaultRaid({ warriors: [1, 5, 6] })),
      NOW,
      rng(),
      solo,
    );
    expect(few.memory.raid?.abandon).toBe('too_few');
    const noRams = decide(
      view({ city: ironCity(), units: atRally, foreignCities: [victim()], foreignBuildings: [WALL] }),
      saqueador,
      withRaid(assaultRaid({ warriors: [1, 2, 3, 4], rams: [] })),
      NOW,
      rng(),
      solo,
    );
    expect(noRams.memory.raid?.abandon).toBe('no_rams');
    // Al vencer el plazo, todos a casa, también los que derriban.
    const late = decide(
      view({ city: ironCity(), units: [...atRally, ram(5, { status: 'ATTACKING' }), ram(6, { status: 'ATTACKING' })], foreignCities: [victim()], foreignBuildings: [WALL] }),
      saqueador,
      withRaid(assaultRaid({ startedAtMs: NOW - 46 * 60_000 })),
      NOW,
      rng(),
      solo,
    );
    expect(late.memory.raid?.abandon).toBe('timeout');
    expect(late.commands.every((c) => c.type === 'unit.move' && c.key.startsWith('raid:home:'))).toBe(true);
    expect(late.commands).toHaveLength(2);
  });

  it('lo que el HUD deshabilitaría no se envía: con paz, la partida vuelve sin plantar', () => {
    const v = view({ city: ironCity(), units: [...near, ...rams], foreignCities: [victim()], treaties: [treaty()] });
    const { commands, memory } = decide(v, saqueador, withRaid(assaultRaid()), NOW, rng(), solo);
    expect(types(commands)).not.toContain('unit.assault');
    expect(memory.raid?.abandon).toBe('blocked');
  });
});

describe('la caída y los rechazos', () => {
  it('la ciudad asaltada cae: la partida vuelve, y un city.fallen repetido no cambia nada', () => {
    const m = onTargetFell(withRaid(assaultRaid({ stage: 'hold' })), 20);
    expect(m.raid?.abandon).toBe('fell');
    expect(onTargetFell(m, 20)).toBe(m);
    expect(onTargetFell(withRaid(assaultRaid()), 99).raid?.abandon).toBeNull();
  });

  it('RN-BOT-017: el bot que cae olvida lo de la ciudad y conserva lo del jugador', () => {
    const m: Memory = {
      ...withRaid(assaultRaid()),
      cooldowns: { 'build:HOUSE': NOW + 1, 'raid:march:1:1,1': NOW + 1, 'assault:plant:1': NOW + 1 },
      badTiles: { '1,1': NOW + 1 },
      stoneUnits: [7],
      stoneBlockedUntilMs: NOW + 1,
      marketSeen: { 3: NOW },
      targetCooldowns: { 20: NOW + 1 },
      unreachablePlayers: [OTHER_BOT],
    };
    const after = onOwnCityFell(m);
    expect(after.cooldowns).toEqual({ 'raid:march:1:1,1': NOW + 1, 'assault:plant:1': NOW + 1 });
    expect(after).toMatchObject({ badTiles: {}, stoneUnits: [], stoneBlockedUntilMs: 0, marketSeen: {} });
    expect(after.targetCooldowns).toEqual({ 20: NOW + 1 });
    expect(after.unreachablePlayers).toEqual([OTHER_BOT]);
    expect(after.raid).toBe(m.raid);
  });

  it('un estandarte rechazado enfría el blanco y la partida vuelve', () => {
    const cmd: Command = { key: 'assault:plant:1', type: 'unit.assault', unitId: 1, cityId: 20 };
    const m = onRejected(withRaid(assaultRaid()), cmd, 'INVALID_TARGET', NOW, saqueador);
    expect(m.targetCooldowns[20]).toBe(NOW + saqueador.targetCooldownMs);
    expect(m.raid?.abandon).toBe('blocked');
  });
});

function types(cs: readonly Command[]): string[] {
  return cs.map((c) => c.type);
}
