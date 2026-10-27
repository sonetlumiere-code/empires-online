/**
 * Micro-benchmark del store: lo que cuesta un delta con muchas entidades.
 *
 * Es INFORMATIVO: imprime las cifras y sólo comprueba el resultado funcional y
 * una cota muy holgada, para que nunca falle por la carga de la máquina. Las
 * cifras de referencia están en el mensaje del commit que lo introdujo.
 *
 * Escenario: 2 000 unidades en el área y 1 000 `entity.update` seguidos, que
 * es la forma de los contadores frecuentes de la economía y del combate.
 */
import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import { useWorldStore } from './world';

const PLAYER = '11111111-1111-4111-8111-111111111111';
const OTHER = '22222222-2222-4222-8222-222222222222';

const UNITS = 2_000;
const DELTAS = 1_000;

function unitView(id: number): v1.UnitView {
  return {
    id,
    playerId: id % 4 === 0 ? PLAYER : OTHER,
    cityId: 10,
    unitType: 'VILLAGER',
    x: id % 160,
    y: Math.floor(id / 160),
    hp: 40,
    maxHp: 40,
    status: 'IDLE',
    movement: null,
    garrisonCityId: null,
  } as v1.UnitView;
}

function snapshot(): v1.WorldSnapshot['payload'] {
  const units: v1.UnitView[] = [];
  for (let id = 1; id <= UNITS; id++) units.push(unitView(id));
  return {
    serverTimeMs: 1_757_376_000_000,
    tick: 42,
    chunks: [{ cx: 0, cy: 0 }],
    terrain: [],
    units,
    cities: [],
    territories: [],
  } as v1.WorldSnapshot['payload'];
}

function update(seq: number, id: number, hp: number): v1.ServerMessage {
  return {
    v: 1,
    type: 'entity.update',
    seq,
    ts: 1_757_376_000_000 + seq,
    payload: { id, hp },
  } as v1.ServerMessage;
}

/** Generador determinista (LCG) para que el reparto de deltas sea el mismo en cada ejecución. */
function lcg(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (Math.imul(s, 1_664_525) + 1_013_904_223) >>> 0;
    return s;
  };
}

beforeEach(() => {
  useWorldStore.getState().reset();
  useWorldStore.setState({ playerId: PLAYER, world: { width: 512, height: 512, chunkSize: 32 } });
});

describe('coste de un delta con 2 000 unidades (informativo)', () => {
  it('1 000 entity.update aplican sus campos y se miden', () => {
    const next = lcg(7);
    const deltas: v1.ServerMessage[] = [];
    const lastHp = new Map<number, number>();
    for (let i = 0; i < DELTAS; i++) {
      const id = 1 + (next() % UNITS);
      const hp = next() % 41;
      deltas.push(update(100 + i, id, hp));
      lastHp.set(id, hp);
    }

    const { applySnapshot, applyServerMessage } = useWorldStore.getState();

    const t0 = performance.now();
    applySnapshot(snapshot());
    const t1 = performance.now();
    for (const d of deltas) applyServerMessage(d);
    const t2 = performance.now();

    const snapshotMs = t1 - t0;
    const deltasMs = t2 - t1;
    console.info(
      `[perf store] snapshot de ${UNITS} unidades: ${snapshotMs.toFixed(2)} ms · ` +
        `${DELTAS} entity.update: ${deltasMs.toFixed(2)} ms (${((deltasMs / DELTAS) * 1000).toFixed(1)} µs/delta)`,
    );

    const units = useWorldStore.getState().units;
    expect(units.size).toBe(UNITS);
    for (const [id, hp] of lastHp) expect(units.get(id)!.hp).toBe(hp);

    // Cota de seguridad, no de rendimiento: 20 µs por unidad y delta sería ya
    // el síntoma de volver a copiar el mapa entero en cada mensaje.
    expect(deltasMs).toBeLessThan(DELTAS * UNITS * 0.02);
  });
});
