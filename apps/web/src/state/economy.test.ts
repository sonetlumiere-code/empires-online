import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import {
  TERRAIN_FOREST,
  buildingAt,
  buildingsOf,
  decideTileOrder,
  decideUnitClick,
  firstFreeHouseTile,
  overPopulationLimit,
  pillageAlertActive,
  readableError,
  secondsUntil,
  siegeAlertActive,
  trainBlockReason,
  unitTypeLabel,
} from './economy';
import { useWorldStore } from './world';

// SPEC-ECON-WOOD en el cliente: lo puro (frontend.md §3.7) y su efecto en el
// store, que es una réplica de sólo lectura de lo que dice el servidor.

const PLAYER = '11111111-1111-4111-8111-111111111111';
const ciudad = { id: 10, centerX: 5, centerY: 5, ownerPlayerId: PLAYER };
const sinCiudad = () => undefined;
const conCiudad = (tile: { x: number; y: number }) =>
  Math.abs(tile.x - ciudad.centerX) <= 1 && Math.abs(tile.y - ciudad.centerY) <= 1 ? ciudad : undefined;

describe('decideTileOrder: recolectar, mover o guarnecer', () => {
  const aldeano = { unitType: 'VILLAGER' };

  it('un bosque con un aldeano es recolectar', () => {
    expect(decideTileOrder({ x: 20, y: 10 }, aldeano, TERRAIN_FOREST, sinCiudad, PLAYER)).toEqual({ kind: 'gather' });
  });

  it('cualquier otro terreno, o terreno aún sin cargar, es moverse', () => {
    expect(decideTileOrder({ x: 20, y: 10 }, aldeano, 0, sinCiudad, PLAYER)).toEqual({ kind: 'move' });
    expect(decideTileOrder({ x: 20, y: 10 }, aldeano, undefined, sinCiudad, PLAYER)).toEqual({ kind: 'move' });
  });

  it('un tipo que no recolecta va al bosque andando', () => {
    expect(decideTileOrder({ x: 20, y: 10 }, { unitType: 'KNIGHT' }, TERRAIN_FOREST, sinCiudad, PLAYER)).toEqual({ kind: 'move' });
  });

  it('la zona urbana manda sobre el terreno: guarnecerse', () => {
    expect(decideTileOrder({ x: 6, y: 4 }, aldeano, TERRAIN_FOREST, conCiudad, PLAYER)).toEqual({ kind: 'garrison', cityId: 10 });
  });

  it('un guerrero sobre una ciudad ajena la saquea; sobre la propia, se guarnece (SPEC-COMBAT-PILLAGE)', () => {
    const guerrero = { unitType: 'WARRIOR' };
    const ajena = () => ({ id: 20, ownerPlayerId: '22222222-2222-4222-8222-222222222222' });
    expect(decideTileOrder({ x: 6, y: 4 }, guerrero, 0, ajena, PLAYER)).toEqual({ kind: 'pillage', cityId: 20 });
    expect(decideTileOrder({ x: 6, y: 4 }, guerrero, 0, conCiudad, PLAYER)).toEqual({ kind: 'garrison', cityId: 10 });
    expect(decideTileOrder({ x: 6, y: 4 }, aldeano, 0, ajena, PLAYER)).toEqual({ kind: 'garrison', cityId: 20 });
  });
});

describe('asedio en el cliente (SPEC-COMBAT-SIEGE)', () => {
  const guerrero = { unitType: 'WARRIOR' };
  const aldeano = { unitType: 'VILLAGER' };
  const ajena = () => ({ id: 20, ownerPlayerId: '22222222-2222-4222-8222-222222222222' });

  it('un guerrero sobre una casa ajena la derriba; en el resto de la ciudad, saquea (Q10)', () => {
    const casaAjena = (t: { x: number; y: number }) => (t.x === 6 && t.y === 4 ? { id: 77, cityId: 20 } : undefined);
    expect(decideTileOrder({ x: 6, y: 4 }, guerrero, 0, ajena, PLAYER, casaAjena)).toEqual({ kind: 'demolish', buildingId: 77 });
    expect(decideTileOrder({ x: 5, y: 5 }, guerrero, 0, ajena, PLAYER, casaAjena)).toEqual({ kind: 'pillage', cityId: 20 });
    expect(decideTileOrder({ x: 6, y: 4 }, aldeano, 0, ajena, PLAYER, casaAjena), 'el aldeano no derriba').toEqual({ kind: 'garrison', cityId: 20 });
  });

  it('sobre una casa de su propia ciudad, o de otra ciudad, no derriba', () => {
    const casaPropia = () => ({ id: 78, cityId: 10 });
    expect(decideTileOrder({ x: 6, y: 4 }, guerrero, 0, conCiudad, PLAYER, casaPropia)).toEqual({ kind: 'garrison', cityId: 10 });
    const deOtra = () => ({ id: 79, cityId: 99 });
    expect(decideTileOrder({ x: 6, y: 4 }, guerrero, 0, ajena, PLAYER, deOtra)).toEqual({ kind: 'pillage', cityId: 20 });
  });

  it('buildingAt encuentra el edificio de un tile', () => {
    const edificios = new Map([[4, { id: 4, cityId: 10, type: 'HOUSE', x: 5, y: 4, status: 'COMPLETE', completesAtMs: 1 }]]);
    expect(buildingAt(edificios, { x: 5, y: 4 })?.id).toBe(4);
    expect(buildingAt(edificios, { x: 4, y: 4 })).toBeUndefined();
  });

  it('el aviso de asedio vale cinco segundos desde el último golpe', () => {
    expect(siegeAlertActive({ besiegedAtMs: 1_000 }, 5_999)).toBe(true);
    expect(siegeAlertActive({ besiegedAtMs: 1_000 }, 6_000)).toBe(false);
    expect(siegeAlertActive({}, 1_000)).toBe(false);
    expect(siegeAlertActive(null, 1_000)).toBe(false);
  });

  it('una ciudad por encima de su límite no entrena, y el motivo lo dice (RN-COMBAT-034)', () => {
    expect(overPopulationLimit({ population: 23, populationLimit: 20 })).toBe(true);
    expect(overPopulationLimit({ population: 20, populationLimit: 20 })).toBe(false);
    expect(trainBlockReason({ population: 23, populationLimit: 20, production: null, advance: null })).toMatch(/perdió casas/);
    expect(trainBlockReason({ population: 20, populationLimit: 20, production: null, advance: null })).toMatch(/al tope/);
    expect(trainBlockReason({ population: 3, populationLimit: 20, production: { unitType: 'VILLAGER', completesAtMs: 1 }, advance: null }))
      .toMatch(/entrenando/);
    expect(trainBlockReason({ population: 3, populationLimit: 20, production: null, advance: null })).toBeNull();
  });
});

describe('pillageAlertActive: el aviso de saqueo', () => {
  it('vale cinco segundos desde el último golpe, y sin golpe no hay aviso', () => {
    expect(pillageAlertActive({ pillagedAtMs: 1_000 }, 1_000)).toBe(true);
    expect(pillageAlertActive({ pillagedAtMs: 1_000 }, 5_999)).toBe(true);
    expect(pillageAlertActive({ pillagedAtMs: 1_000 }, 6_000)).toBe(false);
    expect(pillageAlertActive({}, 1_000)).toBe(false);
    expect(pillageAlertActive(null, 1_000)).toBe(false);
  });
});

describe('firstFreeHouseTile: el botón «Casa»', () => {
  it('el primer hueco del 3 × 3 en orden (y, x), nunca el centro', () => {
    expect(firstFreeHouseTile(ciudad, [])).toEqual({ x: 4, y: 4 });
    expect(firstFreeHouseTile(ciudad, [{ x: 4, y: 4 }, { x: 5, y: 4 }])).toEqual({ x: 6, y: 4 });
  });

  it('salta el centro y devuelve null con los ocho huecos ocupados', () => {
    const fila = [{ x: 4, y: 4 }, { x: 5, y: 4 }, { x: 6, y: 4 }, { x: 4, y: 5 }];
    expect(firstFreeHouseTile(ciudad, fila)).toEqual({ x: 6, y: 5 });
    const todos = [...fila, { x: 6, y: 5 }, { x: 4, y: 6 }, { x: 5, y: 6 }, { x: 6, y: 6 }];
    expect(firstFreeHouseTile(ciudad, todos)).toBeNull();
  });
});

describe('textos', () => {
  it('los dos códigos nuevos tienen un mensaje para personas; el resto conserva el código', () => {
    expect(readableError('INSUFFICIENT_RESOURCES', 'x')).toMatch(/madera/);
    expect(readableError('CITY_BUSY', 'x')).toMatch(/entrenando/);
    expect(readableError('PATH_NOT_FOUND', 'sin ruta')).toBe('PATH_NOT_FOUND: sin ruta');
  });

  it('la cuenta atrás redondea hacia arriba y no baja de cero', () => {
    expect(secondsUntil(10_000, 8_500)).toBe(2);
    expect(secondsUntil(10_000, 10_000)).toBe(0);
    expect(secondsUntil(10_000, 12_000)).toBe(0);
  });
});

// ─────────────────────────────────────────────────────────────
// El store
// ─────────────────────────────────────────────────────────────

function server<T extends v1.ServerMessage['type']>(type: T, seq: number, payload: unknown): v1.ServerMessage {
  return { v: 1, type, seq, ts: 1_757_376_000_000, payload } as v1.ServerMessage;
}

function snapshot(): v1.WorldSnapshot['payload'] {
  return {
    serverTimeMs: 1_757_376_000_000,
    tick: 1,
    chunks: [{ cx: 0, cy: 0 }],
    terrain: [],
    units: [
      {
        id: 1, playerId: PLAYER, cityId: 10, unitType: 'VILLAGER', x: 7, y: 5, hp: 40, maxHp: 40,
        status: 'IDLE', movement: null, garrisonCityId: null,
      },
    ],
    cities: [
      {
        id: 10, ownerPlayerId: PLAYER, name: 'Testópolis', centerX: 5, centerY: 5, era: 'STONE_AGE',
        population: 3, populationLimit: 20, presenceState: 'ONLINE', protectionUntilMs: null,
        stocks: { WOOD: 40 }, production: null,
      },
    ],
    territories: [],
    buildings: [
      { id: 3, cityId: 10, type: 'HOUSE', x: 4, y: 4, status: 'COMPLETE', completesAtMs: 1 },
    ],
  } as v1.WorldSnapshot['payload'];
}

const state = () => useWorldStore.getState();

describe('el store refleja la economía que cuenta el servidor', () => {
  beforeEach(() => {
    state().reset();
    state().applySnapshot(snapshot());
  });

  it('el snapshot trae el stock, la producción y los edificios', () => {
    expect(state().cities.get(10)!.stocks.WOOD).toBe(40);
    expect(state().cities.get(10)!.production).toBeNull();
    expect(state().buildings.get(3)!.status).toBe('COMPLETE');
    expect(buildingsOf(state().buildings, 10)).toHaveLength(1);
    expect(state().units.get(1)!.carrying, 'sin carga, 0').toBe(0);
  });

  it('una ciudad de un servidor anterior a E1, sin stocks, vale cero', () => {
    const viejo = snapshot();
    viejo.cities = viejo.cities.map(({ stocks: _s, production: _p, ...c }) => c as v1.CityView);
    state().applySnapshot(viejo);
    expect(state().cities.get(10)!.stocks.WOOD).toBe(0);
  });

  it('city.update cambia el stock y la producción sin tocar lo demás', () => {
    state().applyServerMessage(server('city.update', 3, { id: 10, stocks: { WOOD: 10 } }));
    expect(state().cities.get(10)!.stocks.WOOD).toBe(10);
    expect(state().cities.get(10)!.population).toBe(3);
    expect(state().changes.cities.has(10)).toBe(true);

    state().applyServerMessage(server('city.update', 4, {
      id: 10, population: 4, production: { unitType: 'VILLAGER', completesAtMs: 99 },
    }));
    expect(state().cities.get(10)!.production).toEqual({ unitType: 'VILLAGER', completesAtMs: 99 });
    state().applyServerMessage(server('city.update', 5, { id: 10, production: null }));
    expect(state().cities.get(10)!.production, 'null: deja de entrenar').toBeNull();
    expect(state().cities.get(10)!.stocks.WOOD, 'sin stocks, no cambia').toBe(10);
  });

  it('building.spawn añade, building.update cambia el estado, y cada uno señala su edificio', () => {
    state().applyServerMessage(server('building.spawn', 3, {
      building: { id: 4, cityId: 10, type: 'HOUSE', x: 5, y: 4, status: 'UNDER_CONSTRUCTION', completesAtMs: 2 },
    }));
    expect(state().buildings.get(4)!.status).toBe('UNDER_CONSTRUCTION');
    expect(state().changes.buildings.has(4)).toBe(true);
    expect(state().changes.units.size).toBe(0);

    state().applyServerMessage(server('building.update', 4, { id: 4, status: 'COMPLETE' }));
    expect(state().buildings.get(4)!.status).toBe('COMPLETE');
    state().applyServerMessage(server('building.update', 5, { id: 99, status: 'COMPLETE' }));
    expect(state().buildings.has(99), 'un update de un edificio desconocido se ignora').toBe(false);
  });

  it('building.update trae la vida, building.destroyed lo quita y city.update el aviso (SPEC-COMBAT-SIEGE)', () => {
    state().applyServerMessage(server('building.spawn', 3, {
      building: { id: 4, cityId: 10, type: 'HOUSE', x: 5, y: 4, status: 'COMPLETE', completesAtMs: 2, hp: 150, maxHp: 150 },
    }));
    state().applyServerMessage(server('building.update', 4, { id: 4, status: 'COMPLETE', hp: 142 }));
    expect(state().buildings.get(4)!.hp).toBe(142);
    expect(state().buildings.get(4)!.maxHp).toBe(150);
    state().applyServerMessage(server('building.update', 5, { id: 4, status: 'COMPLETE' }));
    expect(state().buildings.get(4)!.hp, 'sin hp, la vida no cambia').toBe(142);

    state().applyServerMessage(server('building.destroyed', 6, { id: 4, cityId: 10 }));
    expect(state().buildings.has(4)).toBe(false);
    expect(state().changes.buildings.has(4), 'la escena lo retira por su id').toBe(true);

    state().applyServerMessage(server('city.update', 7, { id: 10, besiegedAtMs: 1_234, populationLimit: 20 }));
    expect(state().cities.get(10)!.besiegedAtMs).toBe(1_234);
    expect(state().cities.get(10)!.populationLimit).toBe(20);
  });

  it('entity.update trae WORKING y la carga; un depósito la deja en cero', () => {
    state().applyServerMessage(server('entity.update', 3, { id: 1, status: 'WORKING' }));
    expect(state().units.get(1)!.status).toBe('WORKING');
    state().applyServerMessage(server('entity.update', 4, { id: 1, carrying: 10 }));
    expect(state().units.get(1)!.carrying).toBe(10);
    expect(state().units.get(1)!.status, 'la carga no toca el estado').toBe('WORKING');
    state().applyServerMessage(server('entity.update', 5, { id: 1, carrying: 0 }));
    expect(state().units.get(1)!.carrying).toBe(0);
  });

  it('un snapshot nuevo reemplaza los edificios', () => {
    const otro = snapshot();
    otro.buildings = [];
    state().applySnapshot(otro);
    expect(state().buildings.size).toBe(0);
  });
});

describe('clic sobre una unidad (SPEC-COMBAT-RAID)', () => {
  const ENEMIGO = '22222222-2222-4222-8222-222222222222';
  const guerrero = { playerId: PLAYER, unitType: 'WARRIOR' };
  const aldeano = { playerId: PLAYER, unitType: 'VILLAGER' };

  it('una propia se selecciona, con o sin guerrero', () => {
    expect(decideUnitClick({ playerId: PLAYER }, guerrero, PLAYER)).toEqual({ kind: 'select' });
    expect(decideUnitClick({ playerId: PLAYER }, undefined, PLAYER)).toEqual({ kind: 'select' });
  });

  it('una ajena con un guerrero propio seleccionado es la orden de atacarla', () => {
    expect(decideUnitClick({ playerId: ENEMIGO }, guerrero, PLAYER)).toEqual({ kind: 'attack' });
  });

  it('una ajena sin guerrero no hace nada: ni se selecciona ni se ataca', () => {
    expect(decideUnitClick({ playerId: ENEMIGO }, aldeano, PLAYER)).toEqual({ kind: 'none' });
    expect(decideUnitClick({ playerId: ENEMIGO }, undefined, PLAYER)).toEqual({ kind: 'none' });
    expect(decideUnitClick({ playerId: ENEMIGO }, { playerId: ENEMIGO, unitType: 'WARRIOR' }, PLAYER)).toEqual({ kind: 'none' });
  });

  it('nombres y mensajes del combate', () => {
    expect(unitTypeLabel('WARRIOR')).toBe('guerrero');
    expect(unitTypeLabel('VILLAGER')).toBe('aldeano');
    expect(readableError('CITY_PROTECTED', 'x')).toMatch(/protegido/);
  });
});
