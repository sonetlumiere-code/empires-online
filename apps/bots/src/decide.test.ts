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

  it('el constructor deja libre el hueco del taller', () => {
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
    expect(types(decide(v, saqueador, EMPTY_MEMORY, NOW, rng()).commands)).toContain('city.build');
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

  it('con tres guerreros parados en casa sale una partida contra el bot más cercano', () => {
    const v = view({ ...base, units: warriors, botCities: [botCity()] });
    const { commands, memory } = decide(v, saqueador, EMPTY_MEMORY, NOW, rng());
    expect(commands).toEqual([
      { key: 'raid:pillage:1', type: 'unit.pillage', unitId: 1, cityId: 20 },
      { key: 'raid:pillage:2', type: 'unit.pillage', unitId: 2, cityId: 20 },
    ]);
    expect(memory.raid).toMatchObject({ targetCityId: 20, warriors: [1, 2, 3], ordered: [1, 2], phase: 'strike' });
    expect(fieldFocus(memory)).toEqual({ x: 140, y: 100 });

    // El despertar siguiente ordena al tercero.
    const next = decide(v, saqueador, memory, NOW + 2_000, rng());
    expect(next.commands).toEqual([{ key: 'raid:pillage:3', type: 'unit.pillage', unitId: 3, cityId: 20 }]);
  });

  it('con menos de tres, o en la espera tras una derrota, no sale', () => {
    const two = view({ ...base, units: warriors.slice(0, 2), botCities: [botCity()] });
    expect(decide(two, saqueador, EMPTY_MEMORY, NOW, rng()).memory.raid).toBeNull();
    const three = view({ ...base, units: warriors, botCities: [botCity()] });
    expect(decide(three, saqueador, { ...EMPTY_MEMORY, raidWaitUntilMs: NOW + 1 }, NOW, rng()).memory.raid).toBeNull();
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
    ...over,
  });

  it('demasiado lejos para una ruta: marcha por tramos de 48 tiles', () => {
    const pillage: Command = { key: 'raid:pillage:1', type: 'unit.pillage', unitId: 1, cityId: 20 };
    const mem = onRejected({ ...EMPTY_MEMORY, raid: raid({ ordered: [1], departed: false }) }, pillage, 'PATH_TOO_LONG', NOW, saqueador);
    expect(mem.raid?.phase).toBe('march');
    const far = { x: 300, y: 100 };
    const v = view({ ...base, units: warriors, botCities: [botCity({ centerX: far.x })] });
    const { commands, memory } = decide(v, saqueador, { ...mem, raid: { ...(mem.raid as Raid), target: far } }, NOW, rng());
    expect(memory.raid?.waypoint).toEqual({ x: 150, y: 100 });
    expect(commands).toEqual([
      { key: 'raid:march:1:150,100', type: 'unit.move', unitId: 1, target: { x: 150, y: 100 } },
      { key: 'raid:march:2:150,100', type: 'unit.move', unitId: 2, target: { x: 150, y: 100 } },
    ]);
    expect(fieldFocus(memory)).toEqual({ x: 150, y: 100 });
  });

  it('cerca del blanco, la marcha vuelve a la orden de saquear', () => {
    const near = [1, 2, 3].map((id) => unit({ id, unitType: 'WARRIOR', x: 132, y: 100 }));
    const v = view({ ...base, units: near });
    const { memory } = decide(v, saqueador, { ...EMPTY_MEMORY, raid: raid({ phase: 'march', waypoint: { x: 132, y: 100 } }) }, NOW, rng());
    expect(memory.raid).toMatchObject({ phase: 'strike', ordered: [] });
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
    const { commands } = decide(v, saqueador, { ...EMPTY_MEMORY, raid: raid() }, NOW, rng());
    expect(commands).toEqual([
      { key: 'raid:home:1', type: 'unit.move', unitId: 1, target: depositTile(base.city, terrain()) },
      { key: 'raid:demolish:2', type: 'unit.demolish', unitId: 2, buildingId: 77 },
    ]);
  });

  it('termina cuando todos vuelven a casa; si murió más de la mitad, espera', () => {
    const home = [unit({ id: 1, unitType: 'WARRIOR' })];
    const v = view({ ...base, units: home });
    const lost = onRaidLoss(onRaidLoss({ ...EMPTY_MEMORY, raid: raid() }, 2), 3);
    expect(lost.raid).toMatchObject({ warriors: [1], lost: 2 });
    const { memory } = decide(v, saqueador, lost, NOW + 5_000, rng());
    expect(memory.raid).toBeNull();
    expect(memory.raidWaitUntilMs).toBe(NOW + 5_000 + saqueador.defeatWaitMs);
  });

  it('un blanco protegido se enfría y la partida vuelve', () => {
    const pillage: Command = { key: 'raid:pillage:1', type: 'unit.pillage', unitId: 1, cityId: 20 };
    const mem = onRejected({ ...EMPTY_MEMORY, raid: raid() }, pillage, 'CITY_PROTECTED', NOW, saqueador);
    expect(mem.targetCooldowns[20]).toBe(NOW + saqueador.targetCooldownMs);
    const away = [1, 2, 3].map((id) => unit({ id, unitType: 'WARRIOR', x: 120, y: 100 }));
    const { commands } = decide(view({ ...base, units: away }), saqueador, mem, NOW, rng());
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
