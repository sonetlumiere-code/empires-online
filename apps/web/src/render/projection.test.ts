import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import { projectAll, projectChanges } from './projection';
import { useWorldStore } from '../state/world';

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

function territoryView(over: Partial<v1.TerritoryView> = {}): v1.TerritoryView {
  return {
    id: 30,
    name: 'Llanura',
    minX: 0,
    minY: 0,
    maxX: 15,
    maxY: 15,
    ownerType: 'NONE',
    ownerId: null,
    contested: false,
    ...over,
  } as v1.TerritoryView;
}

function snapshot(over: Partial<v1.WorldSnapshot['payload']> = {}): v1.WorldSnapshot['payload'] {
  return {
    serverTimeMs: 1_757_376_000_000,
    tick: 42,
    chunks: [{ cx: 0, cy: 0 }],
    terrain: [],
    units: [unitView({ id: 1 }), unitView({ id: 2, playerId: OTHER }), unitView({ id: 3, garrisonCityId: 10 })],
    cities: [cityView()],
    territories: [territoryView(), territoryView({ id: 31, ownerType: 'PLAYER', ownerId: PLAYER })],
    ...over,
  } as v1.WorldSnapshot['payload'];
}

function server<T extends v1.ServerMessage['type']>(type: T, seq: number, payload: unknown): v1.ServerMessage {
  return { v: 1, type, seq, ts: 1_757_376_000_000, payload } as v1.ServerMessage;
}

const state = () => useWorldStore.getState();
/** Proyecta el último cambio del store, como hace GameCanvas en cada suscripción. */
const projectLast = () => projectChanges(state(), state().changes)!;

beforeEach(() => {
  state().reset();
  useWorldStore.setState({ playerId: PLAYER, world: { width: 64, height: 64, chunkSize: 32 } });
  state().applySnapshot(snapshot());
});

describe('projectAll', () => {
  it('sin mundo no hay nada que dibujar', () => {
    state().reset();
    expect(projectAll(state())).toBeNull();
  });

  it('lleva el conjunto completo, sin las unidades guarnecidas, y marca propio con el jugador de la sesión', () => {
    const update = projectAll(state())!;
    expect(update.complete).toBe(true);
    expect(update.units.map((u) => u.id)).toEqual([1, 2]);
    expect(update.units.map((u) => u.isOwn)).toEqual([true, false]);
    expect(update.cities.map((c) => [c.id, c.isOwn, c.name])).toEqual([[10, true, 'Testópolis']]);
    // Un territorio sin dueño nunca es propio; uno del jugador, sí.
    expect(update.territories.map((t) => [t.id, t.isOwn])).toEqual([
      [30, false],
      [31, true],
    ]);
    expect(update.terrain?.chunkSize).toBe(32);
  });

  it('un snapshot también se proyecta completo, con el terreno si llegó', () => {
    const base64 = Buffer.from(new Uint8Array(4)).toString('base64');
    state().applySnapshot(snapshot({ terrain: [{ cx: 0, cy: 0, size: 2, terrain: base64 }] }));
    const update = projectLast();
    expect(update.complete).toBe(true);
    expect(update.terrain?.chunkAt(0, 0)?.size).toBe(2);
    expect(update.terrain?.chunkAt(1, 1)).toBeUndefined();
  });
});

describe('projectChanges: un delta de X no toca Y', () => {
  it('entity.update sólo lleva la unidad tocada y no el terreno', () => {
    state().applyServerMessage(server('entity.update', 2, { id: 2, x: 9 }));
    const update = projectLast();
    expect(update.complete).toBe(false);
    expect(update.units.map((u) => [u.id, u.x, u.isOwn])).toEqual([[2, 9, false]]);
    expect(update.removedUnits).toEqual([]);
    expect(update.cities).toEqual([]);
    expect(update.territories).toEqual([]);
    expect(update.terrain).toBeNull();
  });

  it('una unidad despawneada o guarnecida se retira; al salir de la guarnición vuelve', () => {
    state().applyServerMessage(server('entity.despawn', 2, { id: 2, reason: 'DEAD' }));
    expect(projectLast().removedUnits).toEqual([2]);

    state().applyServerMessage(server('entity.update', 3, { id: 1, status: 'GARRISONED', garrisonCityId: 10 }));
    let update = projectLast();
    expect(update.units).toEqual([]);
    expect(update.removedUnits).toEqual([1]);

    state().applyServerMessage(server('entity.update', 4, { id: 3, status: 'IDLE', garrisonCityId: null }));
    update = projectLast();
    expect(update.units.map((u) => u.id)).toEqual([3]);
    expect(update.removedUnits).toEqual([]);
  });

  it('la selección lleva la unidad que la gana y la que la pierde, con su estado', () => {
    state().selectUnit(1);
    expect(projectLast().units.map((u) => [u.id, u.isSelected])).toEqual([[1, true]]);

    state().selectUnit(2);
    const update = projectLast();
    expect(update.units.map((u) => [u.id, u.isSelected]).sort()).toEqual([
      [1, false],
      [2, true],
    ]);
  });

  it('city.update y territory.update sólo llevan la suya', () => {
    state().applyServerMessage(server('city.update', 2, { id: 10, presenceState: 'PROTECTED' }));
    let update = projectLast();
    expect(update.cities.map((c) => [c.id, c.presenceState])).toEqual([[10, 'PROTECTED']]);
    expect(update.units).toEqual([]);

    // El nombre viaja con la ciudad: la escena lo compara con el último aplicado y redibuja la etiqueta.
    state().applyServerMessage(server('city.update', 3, { id: 10, name: 'Nueva Testópolis' }));
    update = projectLast();
    expect(update.cities.map((c) => [c.id, c.name, c.presenceState])).toEqual([[10, 'Nueva Testópolis', 'PROTECTED']]);

    state().applyServerMessage(
      server('territory.update', 4, { territory: territoryView({ ownerType: 'PLAYER', ownerId: OTHER }) }),
    );
    update = projectLast();
    expect(update.territories.map((t) => [t.id, t.ownerType, t.isOwn])).toEqual([[30, 'PLAYER', false]]);
    expect(update.cities).toEqual([]);
  });

  it('un cambio completo del store se proyecta completo aunque se pida por cambios', () => {
    state().applySnapshot(snapshot({ units: [unitView({ id: 9 })] }));
    const update = projectLast();
    expect(update.complete).toBe(true);
    expect(update.units.map((u) => u.id)).toEqual([9]);
  });
});

describe('economía en la escena (SPEC-ECON-WOOD)', () => {
  const casa = (over: Partial<v1.BuildingView> = {}): v1.BuildingView => ({
    id: 40, cityId: 10, type: 'HOUSE', x: 4, y: 4, status: 'UNDER_CONSTRUCTION', completesAtMs: 1, ...over,
  });

  it('el snapshot proyecta los edificios; propio es el de una ciudad propia', () => {
    state().applySnapshot(snapshot({ buildings: [casa(), casa({ id: 41, cityId: 99, x: 50, y: 50 })] }));
    const update = projectLast();
    expect(update.buildings.map((b) => [b.id, b.status, b.isOwn])).toEqual([
      [40, 'UNDER_CONSTRUCTION', true],
      [41, 'UNDER_CONSTRUCTION', false],
    ]);
  });

  it('building.update sólo lleva el edificio, y una unidad WORKING con carga se marca', () => {
    state().applySnapshot(snapshot({ buildings: [casa()] }));
    projectLast();
    state().applyServerMessage(server('building.update', 3, { id: 40, status: 'COMPLETE' }));
    let update = projectLast();
    expect(update.buildings.map((b) => [b.id, b.status])).toEqual([[40, 'COMPLETE']]);
    expect(update.units).toEqual([]);

    state().applyServerMessage(server('entity.update', 4, { id: 1, status: 'WORKING' }));
    update = projectLast();
    expect(update.units.map((u) => [u.id, u.working, u.carrying])).toEqual([[1, true, 0]]);
    expect(update.buildings).toEqual([]);
    state().applyServerMessage(server('entity.update', 5, { id: 1, status: 'MOVING', carrying: 10 }));
    expect(projectLast().units.map((u) => [u.working, u.carrying])).toEqual([[false, 10]]);
  });
});
