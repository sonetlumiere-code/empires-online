import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import { computeHud, createHudSelector, hudEquals } from './selectors';
import { useWorldStore } from './world';

const PLAYER = '11111111-1111-4111-8111-111111111111';
const OTHER = '22222222-2222-4222-8222-222222222222';

function unitView(over: Partial<v1.UnitView> = {}): v1.UnitView {
  return {
    id: 1,
    playerId: PLAYER,
    cityId: 10,
    unitType: 'VILLAGER',
    x: 5,
    y: 5,
    hp: 40,
    maxHp: 40,
    status: 'IDLE',
    movement: null,
    garrisonCityId: null,
    ...over,
  } as v1.UnitView;
}

function cityView(over: Partial<v1.CityView> = {}): v1.CityView {
  return {
    id: 10,
    ownerPlayerId: PLAYER,
    name: 'Testópolis',
    centerX: 5,
    centerY: 5,
    era: 'STONE_AGE',
    population: 3,
    populationLimit: 20,
    presenceState: 'ONLINE',
    protectionUntilMs: null,
    ...over,
  } as v1.CityView;
}

function snapshot(over: Partial<v1.WorldSnapshot['payload']> = {}): v1.WorldSnapshot['payload'] {
  return {
    serverTimeMs: 1_757_376_000_000,
    tick: 42,
    chunks: [{ cx: 0, cy: 0 }],
    terrain: [],
    units: [unitView({ id: 1 }), unitView({ id: 2, playerId: OTHER }), unitView({ id: 3, garrisonCityId: 10 })],
    cities: [cityView(), cityView({ id: 11, ownerPlayerId: OTHER, name: 'Ajena' })],
    territories: [],
    ...over,
  } as v1.WorldSnapshot['payload'];
}

function server<T extends v1.ServerMessage['type']>(type: T, seq: number, payload: unknown): v1.ServerMessage {
  return { v: 1, type, seq, ts: 1_757_376_000_000, payload } as v1.ServerMessage;
}

const state = () => useWorldStore.getState();

beforeEach(() => {
  state().reset();
  useWorldStore.setState({ playerId: PLAYER, world: { width: 64, height: 64, chunkSize: 32 } });
  state().applySnapshot(snapshot());
});

describe('computeHud', () => {
  it('cuenta las unidades propias, lista las guarnecidas y encuentra la ciudad propia', () => {
    const hud = computeHud(state());
    expect(hud.ownUnitCount).toBe(2);
    expect(hud.garrisoned.map((u) => u.id)).toEqual([3]);
    expect(hud.ownCity?.name).toBe('Testópolis');
  });

  it('sin ciudad propia en el área, ownCity es null', () => {
    state().applySnapshot(snapshot({ cities: [cityView({ id: 11, ownerPlayerId: OTHER })] }));
    expect(computeHud(state()).ownCity).toBeNull();
  });
});

describe('createHudSelector: misma salida, misma referencia', () => {
  it('un entity.update de una unidad ajena, o de una propia que no está guarnecida, no cambia la referencia', () => {
    const select = createHudSelector();
    const first = select(state());

    state().applyServerMessage(server('entity.update', 2, { id: 2, x: 9, hp: 12 }));
    expect(select(state())).toBe(first);

    state().applyServerMessage(server('entity.update', 3, { id: 1, x: 7 }));
    expect(select(state())).toBe(first);

    // Ni la ciudad ajena ni un error del servidor son asunto del HUD.
    state().applyServerMessage(server('city.update', 4, { id: 11, population: 9 }));
    state().applyServerMessage(server('unit.move.rejected', 5, { unitId: 1, code: 'PATH_NOT_FOUND', message: 'x' }));
    expect(select(state())).toBe(first);
  });

  it('cambia la referencia cuando cambia lo que el HUD muestra, y vuelve a estabilizarse', () => {
    const select = createHudSelector();
    const first = select(state());

    state().applyServerMessage(server('entity.spawn', 2, { unit: unitView({ id: 7 }) }));
    const spawned = select(state());
    expect(spawned).not.toBe(first);
    expect(spawned.ownUnitCount).toBe(3);
    expect(select(state()), 'sin cambios, se devuelve lo mismo').toBe(spawned);

    state().applyServerMessage(server('entity.update', 3, { id: 1, status: 'GARRISONED', garrisonCityId: 10 }));
    const garrisoned = select(state());
    expect(garrisoned).not.toBe(spawned);
    expect(garrisoned.garrisoned.map((u) => u.id)).toEqual([1, 3]);

    state().applyServerMessage(server('city.update', 4, { id: 10, population: 4 }));
    const city = select(state());
    expect(city).not.toBe(garrisoned);
    expect(city.ownCity?.population).toBe(4);

    state().applyServerMessage(server('entity.despawn', 5, { id: 7, reason: 'DEAD' }));
    expect(select(state()).ownUnitCount).toBe(2);
  });

  it('cada selector memoriza por su cuenta', () => {
    const a = createHudSelector();
    const b = createHudSelector();
    expect(a(state())).not.toBe(b(state()));
    expect(hudEquals(a(state()), b(state()))).toBe(true);
  });
});
