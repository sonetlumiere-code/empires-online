import { describe, expect, it } from 'vitest';

import { applyCaravanUpdate, caravanLabel, formatEta, type CaravanView } from './caravans';

const yo = '11111111-1111-4111-8111-111111111111';
const otro = '22222222-2222-4222-8222-222222222222';
const tercero = '33333333-3333-4333-8333-333333333333';

const base: CaravanView = {
  id: 1,
  unitId: 9001,
  ownerPlayerId: yo,
  senderPlayerId: yo,
  fromCityId: 1,
  fromCityName: 'Roma',
  toCityId: 2,
  toCityName: 'Bizancio',
  originalToCityId: 2,
  cargo: [{ resource: 'WOOD', amount: 300 }],
  status: 'IN_TRANSIT',
  arrivesAtMs: 120_000,
};

describe('caravanas (SPEC-CARAVANS)', () => {
  it('la lista guarda las que van en camino y quita las que llegan o se pierden', () => {
    const otra = { ...base, id: 2, unitId: 9002, ownerPlayerId: otro, senderPlayerId: otro };
    let lista = applyCaravanUpdate([], otra);
    lista = applyCaravanUpdate(lista, base);
    expect(lista.map((c) => c.unitId)).toEqual([9001, 9002]);
    lista = applyCaravanUpdate(lista, { ...base, status: 'DELIVERED', arrivesAtMs: null });
    expect(lista.map((c) => c.unitId)).toEqual([9002]);
    lista = applyCaravanUpdate(lista, { ...otra, status: 'LOST', arrivesAtMs: null });
    expect(lista).toEqual([]);
  });

  it('una captura sustituye la caravana: el mismo id de unidad, otro destino', () => {
    const capturada = { ...base, ownerPlayerId: tercero, toCityId: 3, toCityName: 'Tercera' };
    const lista = applyCaravanUpdate(applyCaravanUpdate([], base), capturada);
    expect(lista).toHaveLength(1);
    expect(lista[0]!.toCityId).toBe(3);
  });

  it('el texto, desde cada lado', () => {
    expect(caravanLabel(base, yo, 0)).toBe('Tu caravana con 300 de madera hacia Bizancio (llega en 2 min)');
    expect(caravanLabel(base, otro, 90_000)).toBe('Caravana de Roma con 300 de madera hacia tu ciudad (llega en 30 s)');
    const capturada = { ...base, ownerPlayerId: tercero, toCityId: 3, toCityName: 'Tercera' };
    expect(caravanLabel(capturada, yo, 0)).toBe('Te capturaron una caravana con 300 de madera: va hacia Tercera (llega en 2 min)');
    expect(caravanLabel(capturada, otro, 0)).toBe('Te capturaron una caravana con 300 de madera: va hacia Tercera (llega en 2 min)');
    expect(caravanLabel(capturada, tercero, 0)).toBe('Caravana capturada con 300 de madera hacia Tercera (llega en 2 min)');
    expect(caravanLabel({ ...base, arrivesAtMs: null }, yo, 0)).toBe('Tu caravana con 300 de madera hacia Bizancio');
  });

  it('la llegada nunca es negativa', () => {
    expect(formatEta(-5_000)).toBe('0 s');
    expect(formatEta(59_000)).toBe('59 s');
    expect(formatEta(59_001)).toBe('1 min');
    expect(formatEta(61_000)).toBe('2 min');
  });
});
