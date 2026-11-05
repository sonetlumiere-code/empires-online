import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import { projectAll, projectChanges } from '../render/projection';
import {
  advanceBlockReason,
  BRONZE_AGE_COST,
  decideTileOrder,
  eraLabel,
  readableError,
  TERRAIN_FOREST,
  TERRAIN_HILL,
  trainBlockReason,
} from './economy';
import { forestKey, useWorldStore } from './world';

// SPEC-ECON-STONE en el cliente: la colina se recolecta con la misma orden que
// el bosque, la piedra viaja en el stock, la colina picada es una capa aparte y
// el avance de era tiene su botón, su motivo y su cuenta atrás. El cliente sólo
// elige la intención: coste, era y tiempos los decide el servidor.

const PLAYER = '11111111-1111-4111-8111-111111111111';
const state = () => useWorldStore.getState();

function server<T extends v1.ServerMessage['type']>(type: T, seq: number, payload: unknown): v1.ServerMessage {
  return { v: 1, type, seq, ts: 1_757_376_000_000, payload } as v1.ServerMessage;
}

function snapshot(extra: Partial<v1.WorldSnapshot['payload']> = {}): v1.WorldSnapshot['payload'] {
  return {
    serverTimeMs: 1_757_376_000_000,
    tick: 1,
    chunks: [{ cx: 0, cy: 0 }],
    terrain: [],
    units: [
      {
        id: 1, playerId: PLAYER, cityId: 10, unitType: 'VILLAGER', x: 7, y: 5, hp: 40, maxHp: 40,
        status: 'MOVING', movement: null, garrisonCityId: null, carrying: 10, carryingResource: 'STONE',
      },
    ],
    cities: [
      {
        id: 10, ownerPlayerId: PLAYER, name: 'Testópolis', centerX: 5, centerY: 5, era: 'STONE_AGE', civilization: 'ROMAN',
        population: 3, populationLimit: 20, presenceState: 'ONLINE', protectionUntilMs: null,
        stocks: { WOOD: 40, STONE: 15 }, production: null, advance: null,
      },
    ],
    territories: [],
    buildings: [],
    ...extra,
  } as v1.WorldSnapshot['payload'];
}

const ciudad = (over: Partial<{ era: string; stocks: { WOOD: number; STONE: number } }> = {}) => ({
  era: 'STONE_AGE', civilization: 'ROMAN',
  production: null as { unitType: string; completesAtMs: number } | null,
  advance: null as { era: string; completesAtMs: number } | null,
  stocks: { WOOD: 400, STONE: 200 },
  ...over,
});

describe('decisiones de la piedra y del avance', () => {
  it('un clic con un aldeano sobre una colina es recolectar, como sobre un bosque (RN-ECON-019)', () => {
    const sinCiudad = () => undefined;
    expect(decideTileOrder({ x: 1, y: 1 }, { unitType: 'VILLAGER' }, TERRAIN_HILL, sinCiudad, PLAYER))
      .toEqual({ kind: 'gather' });
    expect(decideTileOrder({ x: 1, y: 1 }, { unitType: 'VILLAGER' }, TERRAIN_FOREST, sinCiudad, PLAYER))
      .toEqual({ kind: 'gather' });
    expect(decideTileOrder({ x: 1, y: 1 }, { unitType: 'WARRIOR' }, TERRAIN_HILL, sinCiudad, PLAYER),
      'un guerrero no recolecta: se mueve').toEqual({ kind: 'move' });
    expect(decideTileOrder({ x: 1, y: 1 }, { unitType: 'VILLAGER' }, 0, sinCiudad, PLAYER)).toEqual({ kind: 'move' });
  });

  it('el botón «Edad de Bronce» dice por qué no responde, en el orden de RN-ECON-024', () => {
    expect(advanceBlockReason(ciudad())).toBeNull();
    expect(advanceBlockReason(ciudad({ era: 'BRONZE_AGE' }))).toMatch(/más eras/);
    expect(advanceBlockReason({ ...ciudad(), advance: { era: 'BRONZE_AGE', completesAtMs: 1 } })).toMatch(/avanzando/);
    expect(advanceBlockReason({ ...ciudad(), production: { unitType: 'VILLAGER', completesAtMs: 1 } }))
      .toMatch(/entrenando/);
    expect(advanceBlockReason(ciudad({ stocks: { WOOD: 400, STONE: 150 } }))).toBe(
      'Faltan 50 de piedra. La piedra está en las colinas.',
    );
    expect(advanceBlockReason(ciudad({ stocks: { WOOD: 100, STONE: 0 } }))).toBe(
      'Faltan 300 de madera y 200 de piedra. La piedra está en las colinas.',
    );
    expect(BRONZE_AGE_COST).toEqual({ WOOD: 400, STONE: 200 });
  });

  it('mientras avanza de era, la ciudad no entrena (RN-ECON-026)', () => {
    expect(trainBlockReason({
      population: 3, populationLimit: 20, production: null, advance: { era: 'BRONZE_AGE', completesAtMs: 1 },
    })).toMatch(/avanzando de era/);
  });

  it('las eras se nombran en castellano y los códigos nuevos se explican', () => {
    expect(eraLabel('STONE_AGE')).toBe('Edad de Piedra');
    expect(eraLabel('BRONZE_AGE')).toBe('Edad de Bronce');
    expect(readableError('INSUFFICIENT_RESOURCES', '')).toMatch(/colina/);
    expect(readableError('CITY_BUSY', '')).toMatch(/avanzando de era/);
    expect(readableError('NOT_IMPLEMENTED', '')).not.toMatch(/^NOT_IMPLEMENTED/);
    expect(readableError('PATH_TOO_LONG', '')).toMatch(/vuelta a la ciudad/);
  });
});

describe('el store refleja la piedra y el avance que cuenta el servidor', () => {
  beforeEach(() => {
    state().reset();
    state().applyWelcome({
      playerId: PLAYER,
      sessionId: '22222222-2222-4222-8222-222222222222',
      world: { width: 64, height: 64, chunkSize: 32 },
      heartbeatIntervalMs: 10_000,
    } as v1.SessionWelcome['payload']);
    state().applySnapshot(snapshot());
  });

  it('el snapshot trae la piedra, el avance y la carga con su recurso', () => {
    const c = state().cities.get(10)!;
    expect(c.stocks).toEqual({ WOOD: 40, STONE: 15 });
    expect(c.advance).toBeNull();
    expect(state().units.get(1)!.carryingResource).toBe('STONE');
  });

  it('una ciudad de un servidor anterior a la piedra vale cero de piedra y no avanza', () => {
    const viejo = snapshot();
    viejo.cities = viejo.cities.map(({ advance: _a, ...c }) => ({ ...c, stocks: { WOOD: 40 } }) as v1.CityView);
    state().applySnapshot(viejo);
    expect(state().cities.get(10)!.stocks).toEqual({ WOOD: 40, STONE: 0 });
    expect(state().cities.get(10)!.advance).toBeNull();
  });

  it('city.update empieza el avance, lo termina con la era nueva y conserva la piedra si no viene', () => {
    state().applyServerMessage(server('city.update', 2, {
      id: 10, stocks: { WOOD: 0, STONE: 0 }, advance: { era: 'BRONZE_AGE', completesAtMs: 99 },
    }));
    expect(state().cities.get(10)!.advance).toEqual({ era: 'BRONZE_AGE', completesAtMs: 99 });
    expect(state().cities.get(10)!.stocks.STONE).toBe(0);

    state().applyServerMessage(server('city.update', 3, { id: 10, stocks: { WOOD: 5 } }));
    expect(state().cities.get(10)!.stocks, 'un servidor anterior no manda STONE').toEqual({ WOOD: 5, STONE: 0 });

    state().applyServerMessage(server('city.update', 4, {
      id: 10, era: 'BRONZE_AGE', populationLimit: 50, advance: null,
    }));
    const c = state().cities.get(10)!;
    expect(c.era).toBe('BRONZE_AGE');
    expect(c.populationLimit).toBe(50);
    expect(c.advance, 'null: el avance terminó').toBeNull();
  });

  it('entity.update cambia la carga y su recurso', () => {
    state().applyServerMessage(server('entity.update', 2, { id: 1, carrying: 0 }));
    expect(state().units.get(1)!.carrying).toBe(0);
    state().applyServerMessage(server('entity.update', 3, { id: 1, carrying: 10, carryingResource: 'WOOD' }));
    expect(state().units.get(1)!.carryingResource).toBe('WOOD');
  });

  it('la colina picada es su propia capa: el snapshot la reemplaza y hill.update la cambia', () => {
    state().applySnapshot(snapshot({ hills: [{ x: 3, y: 4, remaining: 40, regrowsAtMs: 9 }] }));
    expect(state().hills.get(forestKey(3, 4))).toEqual({ x: 3, y: 4, remaining: 40, regrowsAtMs: 9 });
    expect(state().forests.size, 'el bosque no se toca').toBe(0);
    expect(state().changes.hills).toBe(true);

    state().applyServerMessage(server('hill.update', 2, { x: 3, y: 4, remaining: 30, regrowsAtMs: 10 }));
    expect(state().hills.get(forestKey(3, 4))!.remaining).toBe(30);
    expect(state().changes.hills).toBe(true);
    expect(state().changes.forests).toBe(false);

    state().applyServerMessage(server('hill.update', 3, { x: 3, y: 4, remaining: 100, regrowsAtMs: null }));
    expect(state().hills.has(forestKey(3, 4)), 'sin regrowsAtMs vuelve a estar llena').toBe(false);
  });

  it('la escena recibe la capa de colina entera cuando cambia, y nada cuando no', () => {
    state().applySnapshot(snapshot({ hills: [{ x: 1, y: 1, remaining: 0, regrowsAtMs: 9 }] }));
    expect(projectAll(state())!.hills).toEqual([{ x: 1, y: 1, remaining: 0 }]);
    state().applyServerMessage(server('hill.update', 2, { x: 2, y: 1, remaining: 50, regrowsAtMs: 9 }));
    expect(projectChanges(state(), state().changes)!.hills).toEqual([
      { x: 1, y: 1, remaining: 0 },
      { x: 2, y: 1, remaining: 50 },
    ]);
    state().applyServerMessage(server('city.update', 3, { id: 10, population: 4 }));
    expect(projectChanges(state(), state().changes)!.hills, 'otro cambio no la redibuja').toBeNull();
  });
});
