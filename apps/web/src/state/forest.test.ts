import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import { projectAll, projectChanges } from '../render/projection';
import { forestKey, useWorldStore } from './world';

// SPEC-ECON-FOREST en el cliente: la madera del bosque es una capa aparte del
// terreno. El snapshot la reemplaza; forest.update la cambia tile a tile y,
// sin instante de regeneración, saca el tile de la capa porque está lleno.

const state = () => useWorldStore.getState();

function server(seq: number, payload: v1.ForestUpdate['payload']): v1.ServerMessage {
  return { v: 1, type: 'forest.update', seq, ts: 1_757_376_000_000, payload } as v1.ServerMessage;
}

function snapshot(forests?: v1.WorldSnapshot['payload']['forests']): v1.WorldSnapshot['payload'] {
  return {
    serverTimeMs: 1_757_376_000_000,
    tick: 1,
    chunks: [{ cx: 0, cy: 0 }],
    terrain: [],
    units: [],
    cities: [],
    territories: [],
    buildings: [],
    ...(forests ? { forests } : {}),
  };
}

describe('la capa de bosque del store', () => {
  beforeEach(() => {
    state().reset();
    state().applyWelcome({
      playerId: '11111111-1111-4111-8111-111111111111',
      sessionId: '22222222-2222-4222-8222-222222222222',
      world: { width: 64, height: 64, chunkSize: 32 },
      heartbeatIntervalMs: 10_000,
    } as v1.SessionWelcome['payload']);
  });

  it('el snapshot trae los tiles a medias y reemplaza los anteriores', () => {
    state().applySnapshot(snapshot([{ x: 3, y: 4, remaining: 40, regrowsAtMs: 9 }]));
    expect(state().forests.get(forestKey(3, 4))).toEqual({ x: 3, y: 4, remaining: 40, regrowsAtMs: 9 });
    expect(state().changes.forests).toBe(true);

    state().applySnapshot(snapshot([{ x: 5, y: 5, remaining: 0, regrowsAtMs: 9 }]));
    expect(state().forests.has(forestKey(3, 4)), 'lo que no viene está lleno').toBe(false);
    expect(state().forests.get(forestKey(5, 5))!.remaining).toBe(0);
  });

  it('un servidor anterior a E4, sin forests, deja todo el bosque lleno', () => {
    state().applySnapshot(snapshot([{ x: 3, y: 4, remaining: 40, regrowsAtMs: 9 }]));
    state().applySnapshot(snapshot());
    expect(state().forests.size).toBe(0);
  });

  it('forest.update talla un tile, y sin regrowsAtMs lo devuelve a lleno', () => {
    state().applySnapshot(snapshot());
    state().applyServerMessage(server(2, { x: 7, y: 8, remaining: 90, regrowsAtMs: 100 }));
    expect(state().forests.get(forestKey(7, 8))!.remaining).toBe(90);
    expect(state().changes.forests).toBe(true);
    expect(state().changes.all, 'un delta, no el mundo entero').toBe(false);

    state().applyServerMessage(server(3, { x: 7, y: 8, remaining: 100, regrowsAtMs: null }));
    expect(state().forests.has(forestKey(7, 8))).toBe(false);
  });

  it('la escena recibe la capa entera cuando cambia, y nada cuando no', () => {
    state().applySnapshot(snapshot([{ x: 1, y: 1, remaining: 0, regrowsAtMs: 9 }]));
    expect(projectAll(state())!.forests).toEqual([{ x: 1, y: 1, remaining: 0 }]);

    state().applyServerMessage(server(2, { x: 2, y: 1, remaining: 50, regrowsAtMs: 9 }));
    const cambio = projectChanges(state(), state().changes)!;
    expect(cambio.forests).toEqual([{ x: 1, y: 1, remaining: 0 }, { x: 2, y: 1, remaining: 50 }]);

    state().applyServerMessage({
      v: 1, type: 'entity.spawn', seq: 3, ts: 1,
      payload: {
        unit: {
          id: 9, playerId: '33333333-3333-4333-8333-333333333333', cityId: 1, unitType: 'VILLAGER', x: 1, y: 2,
          hp: 40, maxHp: 40, status: 'IDLE', movement: null, garrisonCityId: null,
        },
      },
    } as v1.ServerMessage);
    expect(state().changes.units.has(9)).toBe(true);
    expect(projectChanges(state(), state().changes)!.forests, 'otro cambio: el bosque no viaja').toBeNull();
  });
});
