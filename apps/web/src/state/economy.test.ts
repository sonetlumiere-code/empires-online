import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import {
  TERRAIN_FOREST,
  buildingsOf,
  decideTileOrder,
  firstFreeHouseTile,
  readableError,
  secondsUntil,
} from './economy';
import { useWorldStore } from './world';

// SPEC-ECON-WOOD en el cliente: lo puro (frontend.md §3.7) y su efecto en el
// store, que es una réplica de sólo lectura de lo que dice el servidor.

const PLAYER = '11111111-1111-4111-8111-111111111111';
const ciudad = { id: 10, centerX: 5, centerY: 5 };
const sinCiudad = () => undefined;
const conCiudad = (tile: { x: number; y: number }) =>
  Math.abs(tile.x - ciudad.centerX) <= 1 && Math.abs(tile.y - ciudad.centerY) <= 1 ? ciudad : undefined;

describe('decideTileOrder: recolectar, mover o guarnecer', () => {
  const aldeano = { unitType: 'VILLAGER' };

  it('un bosque con un aldeano es recolectar', () => {
    expect(decideTileOrder({ x: 20, y: 10 }, aldeano, TERRAIN_FOREST, sinCiudad)).toEqual({ kind: 'gather' });
  });

  it('cualquier otro terreno, o terreno aún sin cargar, es moverse', () => {
    expect(decideTileOrder({ x: 20, y: 10 }, aldeano, 0, sinCiudad)).toEqual({ kind: 'move' });
    expect(decideTileOrder({ x: 20, y: 10 }, aldeano, undefined, sinCiudad)).toEqual({ kind: 'move' });
  });

  it('un tipo que no recolecta va al bosque andando', () => {
    expect(decideTileOrder({ x: 20, y: 10 }, { unitType: 'KNIGHT' }, TERRAIN_FOREST, sinCiudad)).toEqual({ kind: 'move' });
  });

  it('la zona urbana manda sobre el terreno: guarnecerse', () => {
    expect(decideTileOrder({ x: 6, y: 4 }, aldeano, TERRAIN_FOREST, conCiudad)).toEqual({ kind: 'garrison', cityId: 10 });
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
