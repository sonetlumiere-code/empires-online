import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import { decodeTerrain, terrainAt, useWorldStore } from './world';

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
    units: [unitView()],
    cities: [cityView()],
    territories: [],
    ...over,
  } as v1.WorldSnapshot['payload'];
}

function server<T extends v1.ServerMessage['type']>(type: T, seq: number, payload: unknown): v1.ServerMessage {
  return { v: 1, type, seq, ts: 1_757_376_000_000, payload } as v1.ServerMessage;
}

beforeEach(() => {
  useWorldStore.getState().reset();
  useWorldStore.setState({ playerId: PLAYER, world: { width: 64, height: 64, chunkSize: 32 } });
});

describe('snapshot', () => {
  it('reemplaza el estado observable en lugar de mezclarlo', () => {
    const store = useWorldStore.getState();

    store.applySnapshot(snapshot({ units: [unitView({ id: 1 }), unitView({ id: 2 })] }));
    expect(useWorldStore.getState().units.size).toBe(2);

    // Un snapshot posterior con menos unidades significa que las demás salieron
    // del área de interés: NO deben quedarse pegadas de la foto anterior.
    useWorldStore.getState().applySnapshot(snapshot({ units: [unitView({ id: 2 })] }));
    const units = useWorldStore.getState().units;
    expect(units.size).toBe(1);
    expect(units.has(1)).toBe(false);
    expect(units.has(2)).toBe(true);
  });

  it('conserva el terreno ya conocido: es inmutable', () => {
    const tiles = new Uint8Array([0, 1, 2, 3]);
    const base64 = Buffer.from(tiles).toString('base64');

    useWorldStore.getState().applySnapshot(
      snapshot({ terrain: [{ cx: 0, cy: 0, size: 2, terrain: base64 }] }),
    );
    expect(useWorldStore.getState().terrain.size).toBe(1);

    // Un snapshot sin terreno (porque el cliente ya lo tiene) no debe borrarlo.
    useWorldStore.getState().applySnapshot(snapshot({ terrain: [] }));
    expect(useWorldStore.getState().terrain.size).toBe(1);
  });

  it('deselecciona la unidad si ya no está en el área de interés', () => {
    useWorldStore.getState().applySnapshot(snapshot({ units: [unitView({ id: 1 })] }));
    useWorldStore.getState().selectUnit(1);
    expect(useWorldStore.getState().selectedUnitId).toBe(1);

    useWorldStore.getState().applySnapshot(snapshot({ units: [unitView({ id: 9 })] }));
    expect(useWorldStore.getState().selectedUnitId).toBeNull();
  });
});

describe('deltas de entidad', () => {
  beforeEach(() => {
    useWorldStore.getState().applySnapshot(snapshot());
  });

  it('entity.update aplica sólo los campos presentes', () => {
    useWorldStore.getState().applyServerMessage(server('entity.update', 2, { id: 1, x: 9 }));

    const unit = useWorldStore.getState().units.get(1)!;
    expect(unit.x).toBe(9);
    expect(unit.y, 'un campo ausente del delta no debe tocarse').toBe(5);
    expect(unit.hp).toBe(40);
    expect(unit.status).toBe('IDLE');
  });

  it('ignora un delta sobre una entidad desconocida', () => {
    const before = useWorldStore.getState().units.size;
    useWorldStore.getState().applyServerMessage(server('entity.update', 3, { id: 999, x: 1 }));
    // Inventar una unidad a partir de un delta parcial la dejaría con campos
    // fantasma; se espera a su spawn o al próximo snapshot.
    expect(useWorldStore.getState().units.size).toBe(before);
    expect(useWorldStore.getState().units.has(999)).toBe(false);
  });

  it('entity.spawn añade la unidad completa', () => {
    useWorldStore.getState().applyServerMessage(
      server('entity.spawn', 4, { unit: unitView({ id: 7, playerId: OTHER, x: 20, y: 21 }) }),
    );
    const unit = useWorldStore.getState().units.get(7)!;
    expect(unit.playerId).toBe(OTHER);
    expect(unit.x).toBe(20);
  });

  it('entity.despawn la retira y la deselecciona', () => {
    useWorldStore.getState().selectUnit(1);
    useWorldStore.getState().applyServerMessage(
      server('entity.despawn', 5, { id: 1, reason: 'OUT_OF_INTEREST' }),
    );
    expect(useWorldStore.getState().units.has(1)).toBe(false);
    expect(useWorldStore.getState().selectedUnitId).toBeNull();
  });
});

describe('ciclo de vida del movimiento', () => {
  beforeEach(() => {
    useWorldStore.getState().applySnapshot(snapshot());
  });

  const movement = {
    movementId: 900,
    path: [
      { x: 5, y: 5, tMs: 0 },
      { x: 6, y: 5, tMs: 600 },
    ],
    startTimeMs: 1_757_376_000_000,
    arrivalTimeMs: 1_757_376_000_600,
    target: { x: 6, y: 5 },
  };

  it('movement.started guarda la polilínea y pone la unidad en MOVING', () => {
    useWorldStore.getState().applyServerMessage(
      server('unit.movement.started', 2, { unitId: 1, movement }),
    );
    const unit = useWorldStore.getState().units.get(1)!;
    expect(unit.status).toBe('MOVING');
    expect(unit.movement?.movementId).toBe(900);
    expect(unit.movement?.path).toHaveLength(2);
  });

  it('movement.completed fija la posición final y limpia el movimiento', () => {
    useWorldStore.getState().applyServerMessage(
      server('unit.movement.started', 2, { unitId: 1, movement }),
    );
    useWorldStore.getState().applyServerMessage(
      server('unit.movement.completed', 3, { unitId: 1, movementId: 900, finalPosition: { x: 6, y: 5 } }),
    );

    const unit = useWorldStore.getState().units.get(1)!;
    expect(unit.movement).toBeNull();
    expect(unit.status).toBe('IDLE');
    expect(unit.x).toBe(6);
    expect(unit.y).toBe(5);
  });

  it('movement.cancelled deja la unidad en el último tile alcanzado', () => {
    useWorldStore.getState().applyServerMessage(
      server('unit.movement.started', 2, { unitId: 1, movement }),
    );
    useWorldStore.getState().applyServerMessage(
      server('unit.movement.cancelled', 3, {
        unitId: 1,
        movementId: 900,
        stoppedAt: { x: 5, y: 5 },
        reason: 'CANCELLED_BY_PLAYER',
      }),
    );

    const unit = useWorldStore.getState().units.get(1)!;
    expect(unit.movement).toBeNull();
    expect(unit.status).toBe('IDLE');
    expect(unit.x).toBe(5);
    expect(unit.y).toBe(5);
  });
});

describe('ciudad y errores', () => {
  beforeEach(() => {
    useWorldStore.getState().applySnapshot(snapshot());
  });

  it('city.update refleja la transición de presencia', () => {
    useWorldStore.getState().applyServerMessage(
      server('city.update', 2, { id: 10, presenceState: 'PROTECTED' }),
    );
    expect(useWorldStore.getState().cities.get(10)!.presenceState).toBe('PROTECTED');
    // Y no toca lo que el delta no menciona.
    expect(useWorldStore.getState().cities.get(10)!.name).toBe('Testópolis');
  });

  it('un rechazo de movimiento se expone al jugador con su código estable', () => {
    useWorldStore.getState().applyServerMessage(
      server('unit.move.rejected', 2, {
        unitId: 1,
        code: 'TARGET_NOT_WALKABLE',
        message: 'el destino no es transitable',
      }),
    );
    expect(useWorldStore.getState().lastError?.code).toBe('TARGET_NOT_WALKABLE');
  });
});

describe('terreno', () => {
  it('decodifica el base64 del servidor', () => {
    const bytes = new Uint8Array([0, 1, 5, 3]);
    const decoded = decodeTerrain(Buffer.from(bytes).toString('base64'));
    expect(Array.from(decoded)).toEqual([0, 1, 5, 3]);
  });

  it('resuelve el tile dentro de su chunk', () => {
    const size = 2;
    const tiles = new Uint8Array([0, 1, 2, 3]); // fila-mayor
    const terrain = new Map([['0:0', { cx: 0, cy: 0, size, tiles }]]);

    expect(terrainAt(terrain, size, 0, 0)).toBe(0);
    expect(terrainAt(terrain, size, 1, 0)).toBe(1);
    expect(terrainAt(terrain, size, 0, 1)).toBe(2);
    expect(terrainAt(terrain, size, 1, 1)).toBe(3);
  });

  it('devuelve undefined si el chunk no está cargado', () => {
    expect(terrainAt(new Map(), 32, 100, 100)).toBeUndefined();
  });
});
