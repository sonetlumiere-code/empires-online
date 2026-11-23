import { describe, expect, it } from 'vitest';

import {
  EMPTY_MEMORY,
  chooseTarget,
  decide,
  depositTile,
  fieldFocus,
  marchStep,
  onRaidLoss,
  onRejected,
  rallyPoint,
  villagerCount,
  type Command,
  type Memory,
  type Raid,
} from './decide';
import {
  HILLS,
  HUMAN,
  ME,
  OTHER_BOT,
  botCity,
  building,
  city,
  stocks,
  terrain,
  unit,
  view,
} from './fixtures.test-helpers';
import { PROFILES } from './profiles';
import { createRng } from './random';

const NOW = 1_757_376_000_000;
const constructor = PROFILES.constructor;
const saqueador = PROFILES.saqueador;
const rng = () => createRng(42);

const types = (cs: readonly Command[]) => cs.map((c) => c.type);
/** Las pruebas de la partida, sin el comercio de SPEC-BOTS-TRADE de por medio. */
const solo = { contraHumanos: false, comercio: false } as const;

describe('regla 1: los aldeanos parados, a recolectar', () => {
  it('manda a cada aldeano parado a un bosque cercano, repartidos', () => {
    const v = view({ units: [unit({ id: 1 }), unit({ id: 2 })] });
    const { commands } = decide(v, constructor, EMPTY_MEMORY, NOW, rng());
    expect(commands).toEqual([
      { key: 'gather:1', type: 'unit.gather', unitId: 1, target: { x: 105, y: 100 }, resource: 'WOOD' },
      { key: 'gather:2', type: 'unit.gather', unitId: 2, target: { x: 106, y: 100 }, resource: 'WOOD' },
    ]);
  });

  it('no ordena nada a un aldeano que ya se mueve, trabaja o está guarnecido', () => {
    const v = view({
      units: [unit({ id: 1, status: 'MOVING' }), unit({ id: 2, status: 'WORKING' }), unit({ id: 3, garrisonCityId: 10 })],
    });
    expect(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands).toEqual([]);
  });

  it('con piedra por debajo de la meta y una colina conocida, manda a uno a la piedra', () => {
    const v = view({ units: [unit({ id: 3 })], hills: HILLS, city: city({ population: 3 }) });
    const { commands, memory } = decide(v, constructor, EMPTY_MEMORY, NOW, rng());
    expect(commands[0]).toMatchObject({ type: 'unit.gather', unitId: 3, target: HILLS[0] });
    expect(memory.stoneUnits).toEqual([3]);
  });

  it('no pasa de un tercio de los aldeanos en la piedra', () => {
    const mem: Memory = { ...EMPTY_MEMORY, stoneUnits: [7] };
    const v = view({ units: [unit({ id: 3 })], hills: HILLS, city: city({ population: 3 }) });
    const { commands } = decide(v, constructor, mem, NOW, rng());
    expect(commands[0]).toMatchObject({ unitId: 3, target: { y: 100 } });
    expect((commands[0] as { target: { x: number } }).target.x).toBeLessThan(200);
  });

  it('no vuelve a un tile que el servidor rechazó hasta que caduque', () => {
    const mem: Memory = { ...EMPTY_MEMORY, badTiles: { '104,100': NOW + 1, '105,100': NOW + 1 } };
    const v = view({ units: [unit({ id: 4 })] });
    expect(decide(v, constructor, mem, NOW, rng()).commands[0]).toMatchObject({ target: { x: 106, y: 100 } });
  });
});

describe('RN-BOT-003: como mucho dos comandos por despertar', () => {
  it('con cinco aldeanos parados y madera de sobra sólo salen dos', () => {
    const v = view({
      units: [1, 2, 3, 4, 5].map((id) => unit({ id })),
      city: city({ stocks: stocks(1_000, 1_000), population: 9, populationLimit: 10 }),
    });
    expect(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands).toHaveLength(2);
  });
});

describe('reglas 2 y 3: casas y aldeanos', () => {
  it('con la población al tope encarga una casa en el primer hueco', () => {
    const v = view({ city: city({ population: 10, populationLimit: 10, stocks: stocks(30) }) });
    expect(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands).toEqual([
      { key: 'build:HOUSE', type: 'city.build', cityId: 10, building: 'HOUSE', tile: { x: 99, y: 99 } },
    ]);
  });

  it('no encarga otra casa mientras una está en obra', () => {
    const v = view({
      city: city({ population: 10, populationLimit: 10, stocks: stocks(100) }),
      buildings: [building({ id: 1, status: 'UNDER_CONSTRUCTION' })],
    });
    expect(types(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands)).not.toContain('city.build');
  });

  it('el constructor deja libre el hueco del taller y el del mercado; el saqueador, sólo el del taller', () => {
    const seven = [
      [99, 99],
      [100, 99],
      [101, 99],
      [99, 100],
      [101, 100],
      [99, 101],
      [100, 101],
    ].map(([x, y], i) => building({ id: i + 1, x: x as number, y: y as number }));
    const v = view({ city: city({ population: 40, populationLimit: 40, stocks: stocks(100) }), buildings: seven });
    expect(types(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands)).not.toContain('city.build');
    // Desde E29 el saqueador también elabora su bien, así que guarda el hueco del taller.
    expect(types(decide(v, saqueador, EMPTY_MEMORY, NOW, rng()).commands)).not.toContain('city.build');
    const withWorkshop = { ...v, buildings: [...seven.slice(1), building({ id: 1, type: 'WORKSHOP', x: 99, y: 99 })] };
    expect(types(decide(withWorkshop, saqueador, EMPTY_MEMORY, NOW, rng()).commands)).toContain('city.build');
    expect(types(decide(withWorkshop, constructor, EMPTY_MEMORY, NOW, rng()).commands)).not.toContain('city.build');
  });

  it('entrena aldeanos hasta los de su era y lleva la cuenta del presupuesto', () => {
    // 79 de madera: la casa (30) va antes que el aldeano (50), que ya no cabe.
    const v = view({ city: city({ population: 9, populationLimit: 10, stocks: stocks(79) }) });
    expect(types(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands)).toEqual(['city.build']);
    const rich = view({ city: city({ population: 3, populationLimit: 10, stocks: stocks(80) }) });
    expect(decide(rich, constructor, EMPTY_MEMORY, NOW, rng()).commands).toEqual([
      { key: 'train:VILLAGER', type: 'city.train', cityId: 10, unitType: 'VILLAGER' },
    ]);
  });

  it('cuenta como aldeanos la población menos los militares que ve', () => {
    const v = view({ city: city({ population: 8 }), units: [unit({ id: 1, unitType: 'WARRIOR' })] });
    expect(villagerCount(v)).toBe(7);
  });
});

describe('RN-BOT-002: no envía lo que el HUD tendría deshabilitado', () => {
  it('no entrena mientras la ciudad entrena, ni avanza mientras entrena', () => {
    const v = view({
      city: city({ stocks: stocks(1_000, 1_000), production: { unitType: 'VILLAGER', completesAtMs: NOW + 1 } }),
    });
    const t = types(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands);
    expect(t).not.toContain('city.train');
    expect(t).not.toContain('city.advance');
  });

  it('una orden rechazada no se repite hasta su enfriamiento', () => {
    const v = view({ city: city({ stocks: stocks(80) }) });
    const first = decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands[0] as Command;
    const mem = onRejected(EMPTY_MEMORY, first, 'INSUFFICIENT_RESOURCES', NOW, constructor);
    expect(first.key).toBe('train:VILLAGER');
    // Enfriado el aldeano, el despertar siguiente pasa a la regla siguiente (el guardia).
    const keys = (n: number) => decide(v, constructor, mem, n, rng()).commands.map((c) => c.key);
    expect(keys(NOW + 1_000)).not.toContain('train:VILLAGER');
    expect(keys(NOW + constructor.rejectCooldownMs)).toEqual(['train:VILLAGER']);
  });

  it('una colina sin ruta aparta la piedra entera y el aldeano va a la madera', () => {
    const v = view({ units: [unit({ id: 3 })], hills: HILLS, city: city({ population: 3 }) });
    const first = decide(v, constructor, EMPTY_MEMORY, NOW, rng());
    const cmd = first.commands[0] as Command;
    expect(cmd).toMatchObject({ type: 'unit.gather', resource: 'STONE' });
    const mem = onRejected(first.memory, cmd, 'PATH_TOO_LONG', NOW, constructor);
    expect(mem.stoneBlockedUntilMs).toBe(NOW + constructor.targetCooldownMs);
    const other = view({ units: [unit({ id: 4 })], hills: HILLS, city: city({ population: 3 }) });
    expect(decide(other, constructor, mem, NOW + 1, rng()).commands[0]).toMatchObject({ resource: 'WOOD', target: { y: 100 } });
  });

  it('un rechazo de recolección aparta el tile y saca al aldeano de la piedra', () => {
    const cmd: Command = { key: 'gather:3', type: 'unit.gather', unitId: 3, target: { x: 260, y: 100 }, resource: 'STONE' };
    const mem = onRejected({ ...EMPTY_MEMORY, stoneUnits: [3] }, cmd, 'PATH_TOO_LONG', NOW, constructor);
    expect(mem.badTiles['260,100']).toBe(NOW + constructor.targetCooldownMs);
    expect(mem.stoneUnits).toEqual([]);
  });
});

describe('reglas 4 a 7 del constructor', () => {
  const villagersDone = { population: 8, populationLimit: 15 };

  it('avanza a Bronce con 400 de madera y 200 de piedra', () => {
    const v = view({ city: city({ ...villagersDone, stocks: stocks(400, 200) }) });
    expect(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands[0]).toEqual({
      key: 'advance',
      type: 'city.advance',
      cityId: 10,
    });
  });

  it('en Bronce: taller primero, y muralla sólo si sobra piedra', () => {
    const v = view({ city: city({ ...villagersDone, era: 'BRONZE_AGE', population: 12, stocks: stocks(400, 340) }) });
    const cs = decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands;
    expect(cs[0]).toMatchObject({ type: 'city.build', building: 'WORKSHOP' });
    // Quedan 290 de piedra: no llega a 200 + 100 de reserva para la muralla.
    expect(cs.map((c) => (c.type === 'city.build' ? c.building : c.type))).not.toContain('WALL');
  });

  it('con taller elabora su bien, y con piedra de sobra levanta la muralla', () => {
    const v = view({
      city: city({ era: 'BRONZE_AGE', population: 12, populationLimit: 15, stocks: stocks(400, 400) }),
      buildings: [building({ id: 5, type: 'WORKSHOP' })],
    });
    expect(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands).toEqual([
      { key: 'craft', type: 'city.craft', cityId: 10 },
      { key: 'build:WALL', type: 'city.build', cityId: 10, building: 'WALL', tile: { x: 100, y: 100 } },
    ]);
  });

  it('investiga la primera tecnología que puede pagar y entrena sus dos guardias', () => {
    const v = view({ city: city({ population: 8, populationLimit: 15, stocks: stocks(399, 60) }) });
    const cs = decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands;
    expect(cs[0]).toEqual({ key: 'research:STONE_TOOLS', type: 'city.research', cityId: 10, technology: 'STONE_TOOLS' });
    expect(cs[1]).toMatchObject({ type: 'city.train', unitType: 'WARRIOR' });
  });
});

describe('RN-BOT-004: a quién ataca un saqueador', () => {
  const human = city({ id: 30, ownerPlayerId: HUMAN, name: 'smoke_runnerpolis', centerX: 110, centerY: 100 });

  it('sin --contra-humanos, sólo ciudades de bots, aunque haya una humana más cerca', () => {
    const v = view({ foreignCities: [human], botCities: [botCity()] });
    expect(chooseTarget(v, EMPTY_MEMORY, NOW, false, rng())?.cityId).toBe(20);
  });

  it('sin bots, nadie', () => {
    const v = view({ foreignCities: [human] });
    expect(chooseTarget(v, EMPTY_MEMORY, NOW, false, rng())).toBeNull();
  });

  it('una entrada del directorio que no es de un bot no se elige', () => {
    const v = view({ botCities: [botCity({ name: 'smoke_victimpolis' })] });
    expect(chooseTarget(v, EMPTY_MEMORY, NOW, false, rng())).toBeNull();
  });

  it('con --contra-humanos, también la humana, y gana la más cercana', () => {
    const v = view({ foreignCities: [human], botCities: [botCity()] });
    expect(chooseTarget(v, EMPTY_MEMORY, NOW, true, rng())?.cityId).toBe(30);
  });

  it('nunca su propia ciudad, ni una protegida o en enfriamiento', () => {
    const v = view({
      foreignCities: [city({ id: 20, ownerPlayerId: OTHER_BOT, name: 'bot_constructor_2polis', presenceState: 'PROTECTED' })],
      botCities: [botCity({ playerId: ME, cityId: 10, name: 'bot_constructor_1polis' }), botCity(), botCity({ playerId: 'x', cityId: 21, name: 'bot_saqueador_9polis', centerX: 300 })],
    });
    expect(chooseTarget(v, EMPTY_MEMORY, NOW, false, rng())?.cityId).toBe(21);
    expect(chooseTarget(v, { ...EMPTY_MEMORY, targetCooldowns: { 21: NOW + 1 } }, NOW, false, rng())).toBeNull();
  });
});

describe('RN-BOT-005: la misma semilla y el mismo espejo dan los mismos comandos', () => {
  it('dos decisiones con la misma semilla son idénticas', () => {
    const v = view({
      units: [1, 2, 3].map((id) => unit({ id, unitType: 'WARRIOR', hp: 60, maxHp: 60 })),
      city: city({ name: 'bot_saqueador_1polis', population: 7, stocks: stocks(500, 100) }),
      botCities: [botCity(), botCity({ playerId: 'y', cityId: 22, centerX: 60 })],
    });
    const a = decide(v, saqueador, EMPTY_MEMORY, NOW, createRng(7));
    const b = decide(v, saqueador, EMPTY_MEMORY, NOW, createRng(7));
    expect(a).toEqual(b);
  });
});

describe('el saqueo', () => {
  const warriors = [1, 2, 3].map((id) => unit({ id, unitType: 'WARRIOR', hp: 60, maxHp: 60 }));
  const base = { city: city({ name: 'bot_saqueador_1polis', population: 4, populationLimit: 5, stocks: stocks(0) }) };

  it('RN-BOT-012: con tres guerreros parados en casa sale una partida, primero al punto de reunión', () => {
    const v = view({ ...base, units: warriors, botCities: [botCity()] });
    const { commands, memory } = decide(v, saqueador, EMPTY_MEMORY, NOW, rng(), solo);
    // A 16 tiles del blanco (140,100), en la línea hacia casa (100,100).
    expect(commands).toEqual([
      { key: 'raid:march:1:124,100', type: 'unit.move', unitId: 1, target: { x: 124, y: 100 } },
      { key: 'raid:march:2:124,100', type: 'unit.move', unitId: 2, target: { x: 124, y: 100 } },
    ]);
    expect(memory.raid).toMatchObject({
      targetCityId: 20,
      warriors: [1, 2, 3],
      ordered: [1, 2],
      phase: 'march',
      rally: { x: 124, y: 100 },
      rallyAtMs: null,
    });
    expect(fieldFocus(memory)).toEqual({ x: 124, y: 100 });

    // El despertar siguiente manda al tercero aunque los dos primeros ya caminen (lo
    // encontró la prueba en vivo del 2026-11-21: esperaba a que se pararan); desde
    // ahí corre el plazo de reunión.
    const walkingFirst = view({
      ...base,
      units: [unit({ id: 1, unitType: 'WARRIOR', status: 'MOVING' }), unit({ id: 2, unitType: 'WARRIOR', status: 'MOVING' }),
        unit({ id: 3, unitType: 'WARRIOR' })],
      botCities: [botCity()],
    });
    const next = decide(walkingFirst, saqueador, memory, NOW + 2_000, rng(), solo);
    expect(next.commands[0]).toEqual({ key: 'raid:march:3:124,100', type: 'unit.move', unitId: 3, target: { x: 124, y: 100 } });
    expect(next.memory.raid).toMatchObject({ ordered: [1, 2, 3], rallyAtMs: NOW + 2_000 });

    // Mientras alguno camina, nadie saquea.
    const walking = view({
      ...base,
      units: [unit({ id: 1, unitType: 'WARRIOR', x: 124, y: 100 }), unit({ id: 2, unitType: 'WARRIOR', x: 124, y: 101 }),
        unit({ id: 3, unitType: 'WARRIOR', x: 115, y: 100, status: 'MOVING' })],
    });
    expect(decide(walking, saqueador, next.memory, NOW + 20_000, rng(), solo).commands.some((c) => c.type === 'unit.pillage')).toBe(false);

    // Reunidos los tres: la orden de saquear a todos, sin intercalar nada.
    const gathered = view({
      ...base,
      units: [1, 2, 3].map((id) => unit({ id, unitType: 'WARRIOR', x: 124, y: 99 + id })),
      city: city({ name: 'bot_saqueador_1polis', population: 4, populationLimit: 5, stocks: stocks(500) }),
    });
    const strike = decide(gathered, saqueador, next.memory, NOW + 30_000, rng(), solo);
    expect(strike.commands).toEqual([
      { key: 'raid:pillage:1', type: 'unit.pillage', unitId: 1, cityId: 20 },
      { key: 'raid:pillage:2', type: 'unit.pillage', unitId: 2, cityId: 20 },
    ]);
    expect(strike.memory.raid).toMatchObject({ phase: 'strike', gathered: 3, rallyWaitMs: 28_000 });
    expect(fieldFocus(strike.memory)).toEqual({ x: 140, y: 100 });
    // El despertar siguiente, el tercero.
    const withVillager = view({ ...gathered, units: [...gathered.units, unit({ id: 9 })] });
    const third = decide(withVillager, saqueador, strike.memory, NOW + 32_000, rng(), solo);
    expect(third.commands[0]).toEqual({ key: 'raid:pillage:3', type: 'unit.pillage', unitId: 3, cityId: 20 });
    // Si la orden del tercero está en enfriamiento, nada se intercala mientras tanto:
    // ni el aldeano parado recibe la suya.
    const cooling = { ...strike.memory, cooldowns: { 'raid:pillage:3': NOW + 40_000 } };
    expect(decide(withVillager, saqueador, cooling, NOW + 32_000, rng(), solo).commands).toEqual([]);
  });

  it('RN-BOT-012: vencido el plazo de reunión, sale con los que haya si son bastantes; si no, vuelve', () => {
    const four = [1, 2, 3, 4];
    const mem = { ...EMPTY_MEMORY, raid: raid({ phase: 'march', warriors: four, size: 4, ordered: four, waypoint: { x: 124, y: 100 },
      rallyAtMs: NOW }) };
    const late = NOW + saqueador.rallyTimeoutMs + 1;
    const three = view({
      ...base,
      units: [...[1, 2, 3].map((id) => unit({ id, unitType: 'WARRIOR', x: 124, y: 100 })),
        unit({ id: 4, unitType: 'WARRIOR', x: 110, y: 100 })],
    });
    expect(decide(three, saqueador, mem, NOW + 10_000, rng(), solo).memory.raid?.phase).toBe('march');
    const out = decide(three, saqueador, mem, late, rng(), solo);
    expect(out.memory.raid).toMatchObject({ phase: 'strike', gathered: 3 });
    expect(types(out.commands)).toEqual(['unit.pillage', 'unit.pillage']);

    const two = view({
      ...base,
      units: [...[1, 2].map((id) => unit({ id, unitType: 'WARRIOR', x: 124, y: 100 })),
        ...[3, 4].map((id) => unit({ id, unitType: 'WARRIOR', x: 110, y: 100 }))],
    });
    const back = decide(two, saqueador, mem, late, rng(), solo);
    expect(back.memory.raid?.phase).toBe('march');
    expect(back.commands.some((c) => c.type === 'unit.pillage')).toBe(false);
    // Al despertar siguiente, la partida vencida vuelve a casa.
    const home = decide(two, saqueador, back.memory, late + 2_000, rng(), solo);
    expect(home.commands.every((c) => c.key.startsWith('raid:home:'))).toBe(true);
  });

  it('sin partida, los guerreros parados lejos de casa vuelven (varados al parar el proceso)', () => {
    // Lo encontró la prueba en vivo del 2026-11-20: los seis guerreros de una
    // partida cortada por el fin de la ejecución anterior se quedaron a 50 tiles,
    // fuera del radio de casa; con el máximo de guerreros ya cubierto, el
    // saqueador ni entrenaba ni salía.
    const stranded = [1, 2, 3].map((id) => unit({ id, unitType: 'WARRIOR', x: 150, y: 120 + id }));
    const v = view({ ...base, units: stranded, botCities: [botCity()] });
    const home = depositTile(v.city, v.terrainAt);
    const { commands, memory } = decide(v, saqueador, EMPTY_MEMORY, NOW, rng(), solo);
    expect(memory.raid).toBeNull();
    expect(commands).toEqual([
      { key: 'raid:recall:1', type: 'unit.move', unitId: 1, target: home },
      { key: 'raid:recall:2', type: 'unit.move', unitId: 2, target: home },
    ]);

    // Si el servidor rechaza la vuelta (sin ruta, o un destino que no se pisa),
    // la siguiente va por tramos de la marcha, y cada rechazo desplaza el tramo.
    const far = view({ ...base, units: [unit({ id: 1, unitType: 'WARRIOR', x: 300, y: 100 })], botCities: [botCity()] });
    const farHome = depositTile(far.city, far.terrainAt);
    const first = decide(far, saqueador, EMPTY_MEMORY, NOW, rng(), solo);
    const recall = first.commands[0] as Command;
    expect(recall).toEqual({ key: 'raid:recall:1', type: 'unit.move', unitId: 1, target: farHome });
    let mem = onRejected(first.memory, recall, 'PATH_TOO_LONG', NOW, saqueador);
    const second = decide(far, saqueador, mem, NOW + 2_000, rng(), solo);
    expect(second.commands[0]).toMatchObject({ key: 'raid:recall:1', target: marchStep({ x: 300, y: 100 }, farHome, 0) });
    mem = onRejected(second.memory, second.commands[0] as Command, 'TARGET_NOT_WALKABLE', NOW + 2_000, saqueador);
    const third = decide(far, saqueador, mem, NOW + 4_000, rng(), solo);
    expect(third.commands[0]).toMatchObject({ key: 'raid:recall:1', target: marchStep({ x: 300, y: 100 }, farHome, 1) });
    expect(marchStep({ x: 300, y: 100 }, farHome, 1)).not.toEqual(marchStep({ x: 300, y: 100 }, farHome, 0));

    // En casa, la cuenta de rechazos se olvida.
    const back = view({ ...base, units: [unit({ id: 1, unitType: 'WARRIOR' })], botCities: [botCity()] });
    expect(decide(back, saqueador, third.memory, NOW + 6_000, rng(), solo).memory.recallTries).toEqual({});
  });

  it('con menos de tres, o en la espera tras una derrota, no sale', () => {
    const two = view({ ...base, units: warriors.slice(0, 2), botCities: [botCity()] });
    expect(decide(two, saqueador, EMPTY_MEMORY, NOW, rng(), solo).memory.raid).toBeNull();
    const three = view({ ...base, units: warriors, botCities: [botCity()] });
    expect(decide(three, saqueador, { ...EMPTY_MEMORY, raidWaitUntilMs: NOW + 1 }, NOW, rng(), solo).memory.raid).toBeNull();
  });

  const raid = (over: Partial<Raid> = {}): Raid => ({
    targetCityId: 20,
    targetName: 'bot_constructor_2polis',
    target: { x: 140, y: 100 },
    warriors: [1, 2, 3],
    startedAtMs: NOW,
    phase: 'strike',
    ordered: [1, 2, 3],
    waypoint: null,
    waypointTries: 0,
    lost: 0,
    size: 3,
    departed: true,
    rally: { x: 124, y: 100 },
    rallyAtMs: null,
    gathered: 0,
    rallyWaitMs: 0,
    ...over,
  });

  it('un blanco lejano: marcha por tramos de 48 tiles hacia el punto de reunión', () => {
    const far = { x: 300, y: 100 };
    const v = view({ ...base, units: warriors, botCities: [botCity({ centerX: far.x })] });
    const { commands, memory } = decide(v, saqueador, EMPTY_MEMORY, NOW, rng(), solo);
    expect(memory.raid?.rally).toEqual({ x: 284, y: 100 });
    expect(memory.raid?.waypoint).toEqual({ x: 150, y: 100 });
    expect(commands).toEqual([
      { key: 'raid:march:1:150,100', type: 'unit.move', unitId: 1, target: { x: 150, y: 100 } },
      { key: 'raid:march:2:150,100', type: 'unit.move', unitId: 2, target: { x: 150, y: 100 } },
    ]);
    expect(fieldFocus(memory)).toEqual({ x: 150, y: 100 });
  });

  it('sin ruta desde el punto de reunión, el blanco se enfría y la partida vuelve', () => {
    const pillage: Command = { key: 'raid:pillage:1', type: 'unit.pillage', unitId: 1, cityId: 20 };
    const mem = onRejected({ ...EMPTY_MEMORY, raid: raid({ ordered: [1] }) }, pillage, 'PATH_TOO_LONG', NOW, saqueador);
    expect(mem.targetCooldowns[20]).toBe(NOW + saqueador.targetCooldownMs);
    expect(NOW - (mem.raid as Raid).startedAtMs).toBeGreaterThan(saqueador.raidTimeoutMs);
  });

  it('el punto de reunión evita el agua y la montaña', () => {
    const water = (x: number, y: number) => (x === 124 && y === 100 ? 3 : 0);
    expect(rallyPoint({ x: 140, y: 100 }, { x: 100, y: 100 }, 16, water)).toEqual({ x: 123, y: 99 });
    expect(rallyPoint({ x: 140, y: 100 }, { x: 130, y: 100 }, 16, terrain())).toEqual({ x: 130, y: 100 });
  });

  it('con la carga llena, a casa; sin carga junto a la víctima, a derribar una casa', () => {
    const units = [
      unit({ id: 1, unitType: 'WARRIOR', x: 138, y: 100, carrying: 20 }),
      unit({ id: 2, unitType: 'WARRIOR', x: 138, y: 101, carrying: 5 }),
      unit({ id: 3, unitType: 'WARRIOR', x: 138, y: 102, status: 'ATTACKING' }),
    ];
    const v = view({
      ...base,
      units,
      foreignBuildings: [building({ id: 77, cityId: 20, x: 139, y: 99 })],
      terrainAt: terrain(),
    });
    const { commands } = decide(v, saqueador, { ...EMPTY_MEMORY, raid: raid() }, NOW, rng(), solo);
    expect(commands).toEqual([
      { key: 'raid:home:1', type: 'unit.move', unitId: 1, target: depositTile(base.city, terrain()) },
      { key: 'raid:demolish:2', type: 'unit.demolish', unitId: 2, buildingId: 77 },
    ]);

    // RN-COMBAT-040: tras responder, el saqueador queda parado sin la carga llena y
    // no reanuda solo. Si el almacén de la víctima sigue por encima del suelo, el bot
    // vuelve a ordenar el saqueo en el despertar siguiente.
    const victim = city({ id: 20, ownerPlayerId: OTHER_BOT, name: 'bot_constructor_2polis', centerX: 140, stocks: stocks(300) });
    const again = decide({ ...v, foreignCities: [victim] }, saqueador, { ...EMPTY_MEMORY, raid: raid() }, NOW, rng(), solo);
    expect(again.commands[1]).toEqual({ key: 'raid:pillage:2', type: 'unit.pillage', unitId: 2, cityId: 20 });
    const floor = { ...victim, stocks: stocks(50) };
    const done = decide({ ...v, foreignCities: [floor] }, saqueador, { ...EMPTY_MEMORY, raid: raid() }, NOW, rng(), solo);
    expect(done.commands[1]).toMatchObject({ type: 'unit.demolish', unitId: 2 });
  });

  it('termina cuando todos vuelven a casa; si murió más de la mitad, espera', () => {
    const home = [unit({ id: 1, unitType: 'WARRIOR' })];
    const v = view({ ...base, units: home });
    const lost = onRaidLoss(onRaidLoss({ ...EMPTY_MEMORY, raid: raid() }, 2), 3);
    expect(lost.raid).toMatchObject({ warriors: [1], lost: 2 });
    const { memory } = decide(v, saqueador, lost, NOW + 5_000, rng(), solo);
    expect(memory.raid).toBeNull();
    expect(memory.raidWaitUntilMs).toBe(NOW + 5_000 + saqueador.defeatWaitMs);
  });

  it('la vuelta a casa de una partida lejana va por tramos si el servidor rechaza la recta', () => {
    // Lo encontró la ejecución larga del 2026-11-22: una partida a 375 tiles de casa
    // pedía la vuelta en línea recta, el servidor la rechazaba (PATH_TOO_LONG) y la
    // partida no terminaba nunca (652 rechazos en dos horas).
    const far = [unit({ id: 1, unitType: 'WARRIOR', x: 470, y: 100, carrying: 20 })];
    const v = view({ ...base, units: far, terrainAt: terrain() });
    const home = depositTile(base.city, terrain());
    const mem = { ...EMPTY_MEMORY, raid: raid({ warriors: [1], ordered: [1], size: 1, target: { x: 472, y: 100 } }) };
    const first = decide(v, saqueador, mem, NOW, rng(), solo);
    const back = first.commands[0] as Command;
    expect(back).toEqual({ key: 'raid:home:1', type: 'unit.move', unitId: 1, target: home });
    const after = onRejected(first.memory, back, 'PATH_TOO_LONG', NOW, saqueador);
    expect(after.cooldowns[back.key]).toBeUndefined();
    const second = decide(v, saqueador, after, NOW + 2_000, rng(), solo);
    expect(second.commands[0]).toEqual({ key: 'raid:home:1', type: 'unit.move', unitId: 1, target: marchStep({ x: 470, y: 100 }, home, 0) });
  });

  it('un blanco protegido se enfría y la partida vuelve', () => {
    const pillage: Command = { key: 'raid:pillage:1', type: 'unit.pillage', unitId: 1, cityId: 20 };
    const mem = onRejected({ ...EMPTY_MEMORY, raid: raid() }, pillage, 'CITY_PROTECTED', NOW, saqueador);
    expect(mem.targetCooldowns[20]).toBe(NOW + saqueador.targetCooldownMs);
    const away = [1, 2, 3].map((id) => unit({ id, unitType: 'WARRIOR', x: 120, y: 100 }));
    const { commands } = decide(view({ ...base, units: away }), saqueador, mem, NOW, rng(), solo);
    expect(commands.every((c) => c.type === 'unit.move' && c.key.startsWith('raid:home:'))).toBe(true);
  });
});

describe('ayudantes', () => {
  it('el depósito es el primer tile transitable del anillo, en el orden del servidor', () => {
    const water = (x: number, y: number) => (x === 102 && y === 100 ? 3 : 0);
    expect(depositTile(city(), water)).toEqual({ x: 98, y: 100 });
  });

  it('un tramo de marcha no pasa de 48 tiles y se desplaza al reintentar', () => {
    expect(marchStep({ x: 0, y: 0 }, { x: 200, y: 100 })).toEqual({ x: 48, y: 24 });
    expect(marchStep({ x: 0, y: 0 }, { x: 10, y: 0 })).toEqual({ x: 10, y: 0 });
    expect(marchStep({ x: 0, y: 0 }, { x: 200, y: 0 }, 1)).toEqual({ x: 50, y: 0 });
  });
});

describe('el saqueo: plazos y tramos rechazados', () => {
  const base = { city: city({ name: 'bot_saqueador_1polis', population: 4, populationLimit: 5, stocks: stocks(0) }) };
  const raid = (over: Partial<Raid> = {}): Raid => ({
    targetCityId: 20,
    targetName: 'bot_constructor_2polis',
    target: { x: 140, y: 100 },
    warriors: [1, 2],
    startedAtMs: NOW,
    phase: 'march',
    ordered: [],
    waypoint: { x: 130, y: 100 },
    waypointTries: 0,
    lost: 0,
    size: 2,
    departed: true,
    rally: { x: 124, y: 100 },
    rallyAtMs: null,
    gathered: 0,
    rallyWaitMs: 0,
    ...over,
  });

  it('vencido el plazo, todos vuelven a casa aunque no tuvieran orden en este tramo', () => {
    const away = [1, 2].map((id) => unit({ id, unitType: 'WARRIOR', x: 125, y: 100 }));
    const v = view({ ...base, units: away, terrainAt: terrain() });
    const late = NOW + saqueador.raidTimeoutMs + 1;
    const { commands } = decide(v, saqueador, { ...EMPTY_MEMORY, raid: raid() }, late, rng(), solo);
    const home = depositTile(base.city, terrain());
    expect(commands).toEqual([
      { key: 'raid:home:1', type: 'unit.move', unitId: 1, target: home },
      { key: 'raid:home:2', type: 'unit.move', unitId: 2, target: home },
    ]);
  });

  it('un tramo que no se puede pisar se desplaza en espiral y no se enfría', () => {
    const cmd: Command = { key: 'raid:march:1:130,100', type: 'unit.move', unitId: 1, target: { x: 130, y: 100 } };
    const mem = onRejected({ ...EMPTY_MEMORY, raid: raid({ ordered: [1] }) }, cmd, 'TARGET_NOT_WALKABLE', NOW, saqueador);
    expect(mem.raid).toMatchObject({ waypoint: null, ordered: [], waypointTries: 1 });
    expect(mem.cooldowns[cmd.key]).toBeUndefined();
    expect(fieldFocus({ ...EMPTY_MEMORY, raid: null })).toBeNull();
  });
});
