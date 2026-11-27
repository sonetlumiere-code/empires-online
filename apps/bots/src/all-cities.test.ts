/**
 * Los bots que juegan todas sus ciudades, en el decisor (SPEC-BOTS-ALL-CITIES §4.2,
 * §4.6; RN-BOT-023, RN-BOT-025): la fundación hasta el tope de la era, el plazo de la
 * vuelta, cómo termina cada varado, lo que falta de la población y los cuadros del
 * barrido.
 */
import { describe, expect, it } from 'vitest';

import type { CityView } from '../../web/src/state/world';
import {
  EMPTY_MEMORY,
  RAID_ASSAULT_FIELDS,
  addStrays,
  decide,
  missingPopulation,
  onRejected,
  strayWatchMs,
  sweepCenters,
  type Command,
  type Memory,
  type Tile,
  type View,
} from './decide';
import { building, city, stocks, unit, view } from './fixtures.test-helpers';
import { PROFILES } from './profiles';
import { createRng } from './random';

const NOW = 1_757_376_000_000;
const constructor = PROFILES.constructor;
const solo = { contraHumanos: false, comercio: false } as const;
const run = (v: View, mem: Memory = EMPTY_MEMORY, nowMs = NOW, profile = constructor) => decide(v, profile, mem, nowMs, createRng(7), solo);

/** El bosque de found.test.ts: un 4 × 4 a 28 tiles al este de la capital (100, 100). */
const PATCH = new Set<string>();
for (let y = 96; y <= 99; y++) for (let x = 128; x <= 131; x++) PATCH.add(`${x},${y}`);
const SITE = { x: 128, y: 90 };

const capital = (era: string, over: Partial<CityView> = {}) =>
  city({ era, population: 8, populationLimit: 50, stocks: stocks(800, 400), ...over });

function foundView(era: string, ownCityCount: number, over: Partial<View> = {}): View {
  return view({
    city: capital(era),
    terrainAt: (x, y) => (PATCH.has(`${x},${y}`) ? 1 : 0),
    units: [unit({ id: 1 })],
    forestTiles: [],
    ownCityCount,
    ...over,
  });
}

describe('RN-BOT-023: funda hasta el tope de la era de su capital', () => {
  it('en Hierro con dos ciudades funda la tercera, y apunta cuántas había al empezar', () => {
    const { commands, memory } = run(foundView('IRON_AGE', 2));
    expect(commands[0]).toEqual({ key: 'found:move:1:128,90', type: 'unit.move', unitId: 1, target: SITE });
    expect(memory.founding).toMatchObject({ site: SITE, unitId: 1, cities: 2 });
  });

  it('en Castillos con tres funda la cuarta; no con el tope lleno ni con otra fundación en curso', () => {
    expect(run(foundView('CASTLE_AGE', 3)).memory.founding).toMatchObject({ cities: 3 });
    expect(run(foundView('BRONZE_AGE', 2)).memory.founding).toBeNull();
    expect(run(foundView('IRON_AGE', 3)).memory.founding).toBeNull();
    expect(run(foundView('CASTLE_AGE', 4)).memory.founding).toBeNull();
    // Con una en curso no empieza otra: sigue la misma.
    const walking: Memory = {
      ...EMPTY_MEMORY,
      founding: { site: { x: 1, y: 1 }, unitId: 9, distance: 50, startedAtMs: NOW, tries: 0, lastPos: { x: 2, y: 2 }, orderedAtMs: null, cities: 2 },
    };
    expect(run(foundView('IRON_AGE', 2, { units: [unit({ id: 9, status: 'MOVING' })] }), walking).memory.founding?.site).toEqual({ x: 1, y: 1 });
  });

  it('termina bien al ver una ciudad más que al empezar, no antes', () => {
    const ordered: Memory = {
      ...EMPTY_MEMORY,
      founding: { site: SITE, unitId: 1, distance: 28, startedAtMs: NOW, tries: 0, lastPos: null, orderedAtMs: NOW, cities: 2 },
    };
    expect(run(foundView('IRON_AGE', 2), ordered, NOW + 1_000).memory.founding).not.toBeNull();
    const done = run(foundView('IRON_AGE', 3), ordered, NOW + 2_000).memory;
    expect(done.founding).toBeNull();
    expect(done.foundResult).toMatchObject({ status: 'founded', ms: 2_000 });
  });
});

describe('la piedra de la siguiente ciudad (precisión de RN-BOT-019, repaso del 2026-11-27)', () => {
  // La prueba en vivo de E41: con la capital en Castillos, la muralla rehecha tras cada
  // asalto y la investigación se llevaban la piedra y el constructor no juntaba los 400.
  const keys = (r: { commands: readonly Command[] }) => r.commands.map((c) => c.key);
  const castle = (stone: number, wood = 5_000) =>
    capital('CASTLE_AGE', { population: 16, populationLimit: 50, stocks: stocks(wood, stone) });
  const withWorkshop = { buildings: [building({ id: 5, type: 'WORKSHOP' })] };

  it('mientras falta una ciudad, la muralla y la elaboración sólo gastan lo que pasa de 400', () => {
    // 350 de piedra: todo apartado, ni muralla (200 + 100) ni elaboración (15).
    const short = run(foundView('CASTLE_AGE', 3, { city: castle(350), ...withWorkshop }));
    expect(keys(short)).not.toContain('build:WALL');
    expect(keys(short)).not.toContain('craft');
    // 750 sin la madera de fundar: sobran 350, llega para la muralla.
    const spare = run(foundView('CASTLE_AGE', 3, { city: castle(750, 700), ...withWorkshop }));
    expect(keys(spare)).toContain('build:WALL');
  });

  it('con el tope lleno o sin sitio, no aparta nada', () => {
    expect(keys(run(foundView('CASTLE_AGE', 4, { city: castle(350), ...withWorkshop })))).toContain('build:WALL');
    const noSite: Memory = { ...EMPTY_MEMORY, foundNoSite: true };
    expect(keys(run(foundView('CASTLE_AGE', 3, { city: castle(350), ...withWorkshop }), noSite))).toContain('build:WALL');
  });

  it('una búsqueda sin sitio deja de apartarla; la siguiente que lo encuentra vuelve a apartarla', () => {
    // Sin bosque no hay sitio: se apunta y la piedra queda libre.
    const bare = foundView('CASTLE_AGE', 3, { city: castle(400, 800), terrainAt: () => 0, ...withWorkshop });
    const first = run(bare);
    expect(first.memory).toMatchObject({ founding: null, foundNoSite: true, foundSearchAtMs: NOW + 5 * 60_000 });
    expect(keys(first)).toContain('build:WALL');
    // Pasados los cinco minutos, con bosque, el sitio aparece y la fundación empieza.
    const later = run(foundView('CASTLE_AGE', 3, { city: castle(400, 800), ...withWorkshop }), first.memory, NOW + 5 * 60_000);
    expect(later.memory).toMatchObject({ foundNoSite: false, founding: { site: SITE, cities: 3 } });
    expect(keys(later)).not.toContain('build:WALL');
  });
});

describe('la vuelta de los varados (SPEC-BOTS-ALL-CITIES §4.6, RN-BOT-025)', () => {
  it('el plazo crece con la distancia a su ciudad: 20 min por cada 160 tiles, al menos 20', () => {
    const home = { x: 100, y: 100 };
    expect(strayWatchMs({ x: 100, y: 100 }, home)).toBe(20 * 60_000);
    expect(strayWatchMs({ x: 260, y: 100 }, home)).toBe(20 * 60_000);
    expect(strayWatchMs({ x: 261, y: 100 }, home)).toBe(40 * 60_000);
    // Los 12 de `_3_e29`: a unos 340 tiles de la refundada.
    expect(strayWatchMs({ x: 120, y: 445 }, { x: 68, y: 104 })).toBe(60 * 60_000);
    expect(strayWatchMs({ x: 0, y: 0, distance: 94 }, null)).toBe(20 * 60_000);
    const mem = addStrays(EMPTY_MEMORY, [{ id: 4, x: 120, y: 445 }], NOW, { x: 68, y: 104 }, constructor);
    expect(mem.strays).toEqual([{ unitId: 4, at: { x: 120, y: 445 }, untilMs: NOW + 60 * 60_000, tries: 0, seenAtMs: NOW }]);
  });

  it('no apunta a las unidades de la partida en curso', () => {
    const raid = {
      ...RAID_ASSAULT_FIELDS,
      targetCityId: 5,
      targetName: 'x',
      target: { x: 300, y: 300 },
      warriors: [7],
      startedAtMs: NOW,
      phase: 'march',
      ordered: [],
      waypoint: null,
      waypointTries: 0,
      lost: 0,
      size: 1,
      departed: true,
    } as unknown as NonNullable<Memory['raid']>;
    const mem = addStrays({ ...EMPTY_MEMORY, raid }, [{ id: 7, x: 300, y: 300 }, { id: 8, x: 300, y: 300 }], NOW);
    expect(mem.strays?.map((s) => s.unitId)).toEqual([8]);
  });

  it('apunta cómo termina cada uno: de vuelta, muerto, por el plazo o trabajando', () => {
    const away = addStrays(EMPTY_MEMORY, [{ id: 1, x: 170, y: 100 }], NOW);
    const results = (u: Parameters<typeof unit>[0] | null, nowMs = NOW + 60_000) =>
      run(view({ city: city(), units: u ? [unit(u)] : [] }), away, nowMs).memory.strayResults;
    expect(results({ id: 1, x: 110, y: 100 })).toEqual([{ unitId: 1, status: 'home', cause: null, ms: 60_000 }]);
    expect(results({ id: 1, x: 170, y: 100, status: 'DEAD' })).toEqual([{ unitId: 1, status: 'lost', cause: 'died', ms: 60_000 }]);
    expect(results({ id: 1, x: 170, y: 100, status: 'WORKING' })).toEqual([{ unitId: 1, status: 'lost', cause: 'busy', ms: 60_000 }]);
    expect(results(null, NOW + 20 * 60_000 + 1)).toEqual([{ unitId: 1, status: 'lost', cause: 'timeout', ms: 20 * 60_000 + 1 }]);
    // Sin verlo y en plazo, se le espera: nada que apuntar.
    expect(results(null)).toEqual([]);
  });

  it('a los nueve tramos rechazados se le olvida y se apunta como atascado', () => {
    let mem = addStrays(EMPTY_MEMORY, [{ id: 4, x: 170, y: 100 }], NOW);
    const cmd: Command = { key: 'stray:move:4:122,100', type: 'unit.move', unitId: 4, target: { x: 122, y: 100 } };
    for (let i = 0; i < 8; i++) mem = onRejected(mem, cmd, 'TARGET_NOT_WALKABLE', NOW + 1_000, constructor);
    expect(mem.strays).toHaveLength(1);
    expect(mem.strayResults).toEqual([]);
    mem = onRejected(mem, cmd, 'TARGET_NOT_WALKABLE', NOW + 1_000, constructor);
    expect(mem.strays).toEqual([]);
    expect(mem.strayResults).toEqual([{ unitId: 4, status: 'lost', cause: 'stuck', ms: 1_000 }]);
  });

  it('un militar varado vuelve a casa, y el saqueador no lo llama además desde la regla de la partida', () => {
    const warrior = unit({ id: 6, unitType: 'WARRIOR', hp: 60, maxHp: 60, x: 300, y: 100 });
    const away = addStrays(EMPTY_MEMORY, [{ id: 6, x: 300, y: 100 }], NOW);
    const v = view({ city: city(), units: [warrior] });
    for (const profile of [constructor, PROFILES.saqueador]) {
      const moves = run(v, away, NOW, profile).commands.filter((c) => c.type === 'unit.move' && c.unitId === 6);
      expect(moves.map((c) => c.key)).toEqual(['stray:move:6:252,100']);
    }
  });
});

describe('lo que falta y el barrido (SPEC-BOTS-ALL-CITIES §4.6)', () => {
  it('la población menos lo que ocupan las unidades vistas y la que está entrenando', () => {
    const c = city({ population: 10, production: { unitType: 'VILLAGER', completesAtMs: NOW + 1 } });
    const seen = [{ unitType: 'VILLAGER' }, { unitType: 'VILLAGER' }, { unitType: 'WARRIOR' }, { unitType: 'RAM' }];
    expect(missingPopulation(c, seen)).toBe(10 - 5 - 1);
    expect(missingPopulation(city({ population: 3 }), [{ unitType: 'VILLAGER' }, { unitType: 'VILLAGER' }, { unitType: 'VILLAGER' }])).toBe(0);
  });

  it('cubre el mapa de 512 × 512 en 16 vistas, de la más cercana a la más lejana', () => {
    const from: Tile = { x: 100, y: 100 };
    const centers = sweepCenters({ width: 512, height: 512, chunkSize: 32 }, 2, from);
    expect(centers).toHaveLength(16);
    expect(new Set(centers.map((c) => c.x))).toEqual(new Set([80, 240, 400, 496]));
    expect(centers[0]).toEqual({ x: 80, y: 80 });
    expect(centers.at(-1)).toEqual({ x: 496, y: 496 });
    // Cada chunk queda a 2 o menos del chunk de algún centro.
    for (let cy = 0; cy < 16; cy++) {
      for (let cx = 0; cx < 16; cx++) {
        expect(centers.some((c) => Math.abs(Math.floor(c.x / 32) - cx) <= 2 && Math.abs(Math.floor(c.y / 32) - cy) <= 2)).toBe(true);
      }
    }
  });
});
