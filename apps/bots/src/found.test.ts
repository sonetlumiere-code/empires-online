/**
 * La fundación de los bots (SPEC-BOTS-FOUND §4.1 a §4.4, RN-BOT-018 a RN-BOT-021):
 * el sitio, la caminata, la orden, cómo termina y la segunda ciudad.
 */
import { describe, expect, it } from 'vitest';

import type { CityView, TerritoryView } from '../../web/src/state/world';
import {
  EMPTY_MEMORY,
  addStrays,
  chooseFoundSite,
  decide,
  fieldFocus,
  foundSiteOk,
  onFounderLost,
  onRejected,
  type Command,
  type Founding,
  type Memory,
  type Tile,
  type View,
} from './decide';
import { OTHER_BOT, botCity, building, city, stocks, unit, view } from './fixtures.test-helpers';
import { PROFILES, colonyProfile } from './profiles';
import { createRng } from './random';

const NOW = 1_757_376_000_000;
const constructor = PROFILES.constructor;
const rng = () => createRng(7);
const solo = { contraHumanos: false, comercio: false } as const;

/** Un bosque de 4 × 4 a 28 tiles al este de la capital (100, 100). */
const PATCH: Tile[] = [];
for (let y = 96; y <= 99; y++) for (let x = 128; x <= 131; x++) PATCH.push({ x, y });
const patchSet = new Set(PATCH.map((t) => `${t.x},${t.y}`));
const grassAndPatch: View['terrainAt'] = (x, y) => (patchSet.has(`${x},${y}`) ? 1 : 0);
/** El primer sitio del anillo de 28: la columna x = 128, la primera fila con 10 tiles de bosque a 8. */
const SITE = { x: 128, y: 90 };

const bronze = (over: Partial<CityView> = {}) =>
  city({ era: 'BRONZE_AGE', population: 8, populationLimit: 20, stocks: stocks(800, 400), ...over });

function foundView(over: Partial<View> = {}): View {
  return view({
    city: bronze(),
    terrainAt: grassAndPatch,
    // Aldeanos de sobra (8 de 12) parados en casa; sin bosque cerca de la capital.
    units: [unit({ id: 1 }), unit({ id: 2, status: 'WORKING' })],
    forestTiles: [],
    ...over,
  });
}

const run = (v: View, mem: Memory = EMPTY_MEMORY, nowMs = NOW, profile = constructor) => decide(v, profile, mem, nowMs, rng(), solo);

const walking = (over: Partial<Founding> = {}): Memory => ({
  ...EMPTY_MEMORY,
  founding: { site: SITE, unitId: 1, distance: 28, startedAtMs: NOW, tries: 0, lastPos: null, orderedAtMs: null, ...over },
});

describe('el sitio (SPEC-BOTS-FOUND §4.2)', () => {
  it('elige el más cercano del anillo, con la fila y la columna de menor valor primero', () => {
    expect(chooseFoundSite(foundView(), constructor, EMPTY_MEMORY, NOW)).toEqual(SITE);
  });

  it('RN-BOT-021: salta el sitio apartado hasta que vence', () => {
    const mem = { ...EMPTY_MEMORY, foundSiteCooldowns: { '128,90': NOW + 1 } };
    expect(chooseFoundSite(foundView(), constructor, mem, NOW)).toEqual({ x: 128, y: 91 });
    expect(chooseFoundSite(foundView(), constructor, mem, NOW + 1)).toEqual(SITE);
  });

  it('descarta el terreno desconocido o que no se pisa, los centros a menos de 24, el poco bosque y el territorio ajeno', () => {
    const v = foundView();
    expect(foundSiteOk(v, SITE)).toBe(true);
    const unknown = foundView({ terrainAt: (x, y) => (x === 130 && y === 92 ? undefined : grassAndPatch(x, y)) });
    expect(foundSiteOk(unknown, SITE)).toBe(false);
    const water = foundView({ terrainAt: (x, y) => (x === 126 && y === 88 ? 3 : grassAndPatch(x, y)) });
    expect(foundSiteOk(water, SITE)).toBe(false);
    const near = foundView({ botCities: [botCity({ centerX: 150, centerY: 90 })] });
    expect(foundSiteOk(near, SITE)).toBe(false);
    const own = foundView({ otherOwnCenters: [{ x: 140, y: 100 }] });
    expect(foundSiteOk(own, SITE)).toBe(false);
    // A 8 filas del bosque sólo llega su primera fila: 4 tiles.
    expect(foundSiteOk(v, { x: 128, y: 88 })).toBe(false);
    const depleted = foundView({ forests: new Map(PATCH.slice(0, 8).map((t) => [`${t.x},${t.y}`, { x: t.x, y: t.y, remaining: 0, regrowsAtMs: NOW + 1 }])) });
    expect(foundSiteOk(depleted, SITE)).toBe(false);
    const t = (over: Partial<TerritoryView>): TerritoryView => ({
      id: 1, name: 't', minX: 120, minY: 80, maxX: 140, maxY: 100, ownerType: 'NONE', ownerId: null, ownerFaction: null,
      ownerClan: null, contested: false, ...over,
    });
    expect(foundSiteOk(foundView({ territories: [t({})] }), SITE)).toBe(true);
    expect(foundSiteOk(foundView({ territories: [t({ ownerType: 'PLAYER', ownerId: OTHER_BOT })] }), SITE)).toBe(false);
    const claimed = t({ claim: { playerId: OTHER_BOT, cityId: 1, cityName: 'x', startedAtMs: NOW, endsAtMs: NOW + 1 } });
    expect(foundSiteOk(foundView({ territories: [claimed] }), SITE)).toBe(false);
  });
});

describe('cuándo funda (SPEC-BOTS-FOUND §4.1, RN-BOT-018)', () => {
  it('en Bronce con 800/400 y una sola ciudad, el aldeano parado camina al sitio, y el avance no gasta lo apartado', () => {
    const { commands, memory } = run(foundView());
    expect(commands[0]).toEqual({ key: 'found:move:1:128,90', type: 'unit.move', unitId: 1, target: SITE });
    expect(commands.some((c) => c.type === 'city.advance')).toBe(false);
    expect(memory.founding).toMatchObject({ site: SITE, unitId: 1, distance: 28, startedAtMs: NOW, lastPos: { x: 102, y: 100 } });
  });

  it('no empieza antes de Bronce, sin lo que cuesta, con dos ciudades, avanzando de era, ni siendo saqueador o colonia', () => {
    const none = (v: View, profile = constructor) => expect(run(v, EMPTY_MEMORY, NOW, profile).memory.founding).toBeNull();
    none(foundView({ city: bronze({ era: 'STONE_AGE' }) }));
    none(foundView({ city: bronze({ stocks: stocks(800, 399) }) }));
    none(foundView({ ownCityCount: 2 }));
    none(foundView({ city: bronze({ advance: { era: 'IRON_AGE', completesAtMs: NOW + 1 } }) }));
    none(foundView(), PROFILES.saqueador);
    none(foundView(), colonyProfile(constructor));
  });

  it('busca hasta 96 tiles: el anillo de 28 a 96', () => {
    expect([constructor.foundMinDistance, constructor.foundMaxDistance]).toEqual([28, 96]);
    const far = new Set<string>();
    for (let y = 96; y <= 99; y++) for (let x = 190; x <= 193; x++) far.add(`${x},${y}`);
    const v = foundView({ terrainAt: (x, y) => (far.has(`${x},${y}`) ? 1 : 0) });
    expect(chooseFoundSite(v, constructor, EMPTY_MEMORY, NOW)).toEqual({ x: 184, y: 91 });
  });

  it('sin sitio a la vista no vuelve a buscar en cinco minutos', () => {
    const { memory } = run(foundView({ terrainAt: () => 0 }));
    expect(memory.founding).toBeNull();
    expect(memory.foundSearchAtMs).toBe(NOW + 5 * 60_000);
    expect(run(foundView(), memory, NOW + 1).memory.founding).toBeNull();
    expect(run(foundView(), memory, NOW + 5 * 60_000).memory.founding).not.toBeNull();
  });

  it('a falta de uno parado, elige al que trabaja sin carga', () => {
    const v = foundView({ units: [unit({ id: 1, status: 'WORKING', carrying: 5 }), unit({ id: 2, status: 'WORKING' })] });
    expect(run(v).memory.founding?.unitId).toBe(2);
  });
});

describe('la fundación en curso (SPEC-BOTS-FOUND §4.3, RN-BOT-019)', () => {
  it('mientras camina, nadie gasta lo apartado ni da otra orden al aldeano', () => {
    const v = foundView({ units: [unit({ id: 1, status: 'MOVING', x: 110 })] });
    const { commands, memory } = run(v, walking({ lastPos: { x: 102, y: 100 } }));
    expect(commands).toEqual([]);
    expect(memory.founding?.unitId).toBe(1);
  });

  it('parado en el sitio, funda; parado a su lado en un tile que también sirve, funda ahí', () => {
    const at = foundView({ units: [unit({ id: 1, x: 128, y: 90 })] });
    const { commands, memory } = run(at, walking({ lastPos: { x: 102, y: 100 } }));
    expect(commands[0]).toEqual({ key: 'found:city:1', type: 'unit.found_city', unitId: 1 });
    expect(memory.founding?.orderedAtMs).toBe(NOW);
    const beside = foundView({ units: [unit({ id: 1, x: 128, y: 92 })] });
    expect(run(beside, walking({ lastPos: { x: 102, y: 100 } })).commands[0]?.type).toBe('unit.found_city');
  });

  it('con la orden en vuelo espera, y sin respuesta en un minuto termina mal', () => {
    const at = foundView({ units: [unit({ id: 1, x: 128, y: 90 })] });
    const mem = walking({ orderedAtMs: NOW });
    expect(run(at, mem, NOW + 30_000).commands).toEqual([]);
    const late = run(at, mem, NOW + 61_000).memory;
    expect(late.founding).toBeNull();
    expect(late.foundResult).toMatchObject({ status: 'failed', cause: 'timeout' });
  });

  it('termina bien al ver dos ciudades propias', () => {
    const { memory } = run(foundView({ ownCityCount: 2 }), walking({ orderedAtMs: NOW }), NOW + 2_000);
    expect(memory.founding).toBeNull();
    expect(memory.foundResult).toEqual({ status: 'founded', cause: null, code: null, site: SITE, ms: 2_000 });
    expect(memory.foundSiteCooldowns).toEqual({});
  });

  it('un tramo que no lo mueve cuenta; a los nueve termina mal y aparta el sitio', () => {
    const still = foundView({ units: [unit({ id: 1, x: 110, y: 100 })] });
    const once = run(still, walking({ lastPos: { x: 110, y: 100 }, tries: 0 }));
    expect(once.memory.founding?.tries).toBe(1);
    expect(once.commands[0]?.type).toBe('unit.move');
    const stuck = run(still, walking({ lastPos: { x: 110, y: 100 }, tries: 8 })).memory;
    expect(stuck.founding).toBeNull();
    expect(stuck.foundResult).toMatchObject({ status: 'failed', cause: 'stuck' });
    expect(stuck.foundSiteCooldowns['128,90']).toBe(NOW + constructor.foundSiteCooldownMs);
  });

  it('el plazo de la caminata y la muerte del aldeano la terminan', () => {
    const late = run(foundView(), walking(), NOW + constructor.foundWalkTimeoutMs + 1).memory;
    expect(late.foundResult).toMatchObject({ status: 'failed', cause: 'timeout' });
    const dead = onFounderLost(walking(), 1, NOW, constructor);
    expect(dead.founding).toBeNull();
    expect(dead.foundResult).toMatchObject({ status: 'failed', cause: 'unit_died' });
    expect(onFounderLost(walking(), 2, NOW, constructor)).toEqual(walking());
  });

  it('el rechazo de la orden aparta el sitio; si el aldeano no estaba parado, se repite', () => {
    const cmd: Command = { key: 'found:city:1', type: 'unit.found_city', unitId: 1 };
    const rejected = onRejected(walking({ orderedAtMs: NOW }), cmd, 'INVALID_TARGET', NOW, constructor);
    expect(rejected.founding).toBeNull();
    expect(rejected.foundResult).toMatchObject({ status: 'failed', cause: 'rejected', code: 'INVALID_TARGET' });
    expect(rejected.foundSiteCooldowns['128,90']).toBe(NOW + constructor.foundSiteCooldownMs);
    const busy = onRejected(walking({ orderedAtMs: NOW }), cmd, 'UNIT_NOT_MOVABLE', NOW, constructor);
    expect(busy.founding?.orderedAtMs).toBeNull();
  });

  it('un tramo rechazado se desplaza en espiral sin enfriar la orden', () => {
    const cmd: Command = { key: 'found:move:1:128,90', type: 'unit.move', unitId: 1, target: SITE };
    const mem = onRejected(walking({ lastPos: { x: 102, y: 100 } }), cmd, 'TARGET_NOT_WALKABLE', NOW, constructor);
    expect(mem.founding).toMatchObject({ tries: 1, lastPos: null });
    expect(mem.cooldowns[cmd.key]).toBeUndefined();
  });

  it('la recolección no reasigna al aldeano que va a fundar', () => {
    const v = foundView({ forestTiles: [{ x: 104, y: 100 }], units: [unit({ id: 1, x: 128, y: 90 }), unit({ id: 3 })] });
    const { commands } = run(v, walking({ orderedAtMs: NOW }));
    expect(commands.filter((c) => c.type === 'unit.gather').map((c) => (c as { unitId: number }).unitId)).toEqual([3]);
  });
});

describe('la fundación fuera de la vista (SPEC-BOTS-FOUND §9.1, la ejecución larga de E32)', () => {
  it('la campaña mira el sitio hasta el primer tramo y luego adónde va cada tramo', () => {
    expect(fieldFocus(walking())).toEqual(SITE);
    const leg = { x: 120, y: 95 };
    expect(fieldFocus(walking({ waypoint: leg }))).toEqual(leg);
    // El tramo que se manda es el que se mira.
    const v = foundView({ units: [unit({ id: 1, x: 110, y: 100 })] });
    const { commands, memory } = run(v, walking({ lastPos: { x: 102, y: 100 } }));
    const move = commands.find((c) => c.type === 'unit.move') as { target: Tile } | undefined;
    expect(move?.target).toEqual(SITE);
    expect(memory.founding?.waypoint).toEqual(SITE);
    expect(fieldFocus(memory)).toEqual(SITE);
  });

  it('la partida manda sobre la fundación; sin nada fuera de casa no hay foco', () => {
    expect(fieldFocus(EMPTY_MEMORY)).toBeNull();
  });

  it('el aldeano de una fundación fallida se apunta como fuera de la vista y la campaña lo mira', () => {
    const leg = { x: 170, y: 100 };
    // El aldeano no se ve (está lejos, fuera de la vista): queda apuntado.
    const late = run(foundView({ units: [unit({ id: 2, status: 'WORKING' })] }), walking({ waypoint: leg }), NOW + constructor.foundWalkTimeoutMs + 1).memory;
    expect(late.foundResult).toMatchObject({ status: 'failed', cause: 'timeout' });
    expect(late.strays).toEqual([{ unitId: 1, at: leg, untilMs: expect.any(Number), tries: 0 }]);
    expect(fieldFocus(late)).toEqual(leg);
  });

  it('la vuelta: visto parado lejos camina un tramo hacia casa, y la recolección no lo toca', () => {
    const away = addStrays(EMPTY_MEMORY, [{ id: 1, x: 170, y: 100 }], NOW);
    const v = foundView({ city: city(), forestTiles: [{ x: 104, y: 100 }], units: [unit({ id: 1, x: 170, y: 100 }), unit({ id: 3 })] });
    const { commands, memory } = run(v, away);
    expect(commands).toContainEqual({ key: 'stray:move:1:122,100', type: 'unit.move', unitId: 1, target: { x: 122, y: 100 } });
    expect(commands.filter((c) => c.type === 'unit.gather').map((c) => (c as { unitId: number }).unitId)).toEqual([3]);
    expect(memory.strays?.[0]?.at).toEqual({ x: 122, y: 100 });
    expect(fieldFocus(memory)).toEqual({ x: 122, y: 100 });
  });

  it('se le olvida cerca de casa, trabajando, muerto o pasado el plazo; sin verlo, se le espera', () => {
    const away = addStrays(EMPTY_MEMORY, [{ id: 1, x: 170, y: 100 }], NOW);
    const at = (u: Partial<Parameters<typeof unit>[0]>) => run(foundView({ city: city(), units: [unit({ id: 1, ...u })] }), away).memory.strays;
    expect(at({ x: 110, y: 100 })).toEqual([]);
    expect(at({ x: 170, y: 100, status: 'WORKING' })).toEqual([]);
    expect(at({ x: 170, y: 100, status: 'DEAD' })).toEqual([]);
    expect(run(foundView({ city: city(), units: [] }), away).memory.strays).toHaveLength(1);
    expect(run(foundView({ city: city(), units: [] }), away, NOW + 20 * 60_000 + 1).memory.strays).toEqual([]);
  });

  it('addStrays no repite ni apunta al que va a fundar; un tramo rechazado se desplaza en espiral', () => {
    const once = addStrays(walking(), [{ id: 1, x: 170, y: 100 }, { id: 4, x: 170, y: 100 }, { id: 4, x: 171, y: 100 }], NOW);
    expect(once.strays?.map((s) => s.unitId)).toEqual([4]);
    const cmd: Command = { key: 'stray:move:4:114,100', type: 'unit.move', unitId: 4, target: { x: 114, y: 100 } };
    const rejected = onRejected(once, cmd, 'TARGET_NOT_WALKABLE', NOW, constructor);
    expect(rejected.strays?.[0]?.tries).toBe(1);
    expect(rejected.cooldowns[cmd.key]).toBeUndefined();
  });

  it('el aldeano muerto o la ciudad caída no dejan a nadie que buscar', () => {
    expect(onFounderLost(walking({ waypoint: SITE }), 1, NOW, constructor).strays).toEqual([]);
  });

  it('el sitio que va a fundar otro bot cuenta como un centro: no se eligen dos a menos de 24', () => {
    expect(foundSiteOk(foundView({ claimedSites: [{ x: 140, y: 90 }] }), SITE)).toBe(false);
    // El único bosque del ayudante es el del sitio: con él apartado por otro bot, no queda ninguno.
    expect(chooseFoundSite(foundView(), constructor, EMPTY_MEMORY, NOW)).toEqual(SITE);
    expect(chooseFoundSite(foundView({ claimedSites: [SITE] }), constructor, EMPTY_MEMORY, NOW)).toBeNull();
    expect(chooseFoundSite(foundView({ claimedSites: [{ x: 128, y: 160 }] }), constructor, EMPTY_MEMORY, NOW)).toEqual(SITE);
  });
});

describe('la segunda ciudad (SPEC-BOTS-FOUND §4.4, RN-BOT-020)', () => {
  const colony = colonyProfile(constructor);

  it('el perfil de colonia: 8 aldeanos, toda la madera, sin partidas, era, muralla, taller, comercio ni fundación', () => {
    expect(colony).toMatchObject({
      raids: false, assaults: false, advance: false, workshop: false, craft: false, wall: false, research: false,
      trades: false, market: false, founds: false, guards: 0,
    });
    expect(colony.villagersByEra).toEqual({ STONE_AGE: 8, BRONZE_AGE: 8, IRON_AGE: 8, CASTLE_AGE: 8 });
    expect(Object.values(colony.stoneTargetByEra)).toEqual([0, 0, 0, 0]);
  });

  it('entrena aldeanos, los manda a la madera y levanta casas, con su tope de dos órdenes', () => {
    const rich = { stocks: stocks(5_000, 5_000), id: 30, name: 'bot_constructor_1polis II' };
    const v = view({
      city: city({ ...rich, era: 'BRONZE_AGE', population: 3, populationLimit: 10 }),
      units: [unit({ id: 7, cityId: 30 })],
      hills: [{ x: 260, y: 100 }],
    });
    const { commands } = decide(v, colony, EMPTY_MEMORY, NOW, rng(), solo);
    expect(commands.map((c) => c.type)).toEqual(['unit.gather', 'city.train']);
    expect(commands[0]).toMatchObject({ resource: 'WOOD' });
    const full = view({ city: city({ ...rich, era: 'BRONZE_AGE', population: 8, populationLimit: 9 }) });
    const types = decide(full, colony, EMPTY_MEMORY, NOW, rng(), solo).commands.map((c) => c.type);
    expect(types).toEqual(['city.build']);
    // Con todo de sobra, ni avanza, ni muralla, ni taller, ni guardias.
    const done = view({
      city: city({ ...rich, era: 'BRONZE_AGE', population: 8, populationLimit: 20 }),
      buildings: [building({ id: 1, cityId: 30 })],
    });
    expect(decide(done, colony, EMPTY_MEMORY, NOW, rng(), solo).commands).toEqual([]);
  });
});
