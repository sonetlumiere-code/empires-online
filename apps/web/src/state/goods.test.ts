import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import { foreignGoods, GOODS, goodLabel, goodOf } from './civilizations';
import { advanceBlockReason, craftBlockReason, nextEra, workshopBlockReason } from './economy';
import { useWorldStore, type CityStocks } from './world';

// SPEC-CIVILIZATION-GOODS en el cliente: los bienes en el almacén, el taller y la
// elaboración con sus motivos, y la Edad de Hierro con los bienes ajenos. El
// cliente sólo explica: el bien, el coste y los plazos los decide el servidor.

const vacio: CityStocks = { WOOD: 0, STONE: 0, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 };
const ciudad = (over: { era?: string; stocks?: Partial<CityStocks>; craft?: { good: string; amount: number; completesAtMs: number } | null } = {}) => ({
  id: 10, centerX: 5, centerY: 5, era: over.era ?? 'BRONZE_AGE', civilization: 'ROMAN',
  production: null, advance: null, craft: over.craft ?? null,
  stocks: { ...vacio, WOOD: 1000, STONE: 500, ...over.stocks },
});

describe('los bienes de civilización', () => {
  it('cada civilización tiene su bien, y el propio no cuenta para Hierro', () => {
    expect(GOODS).toEqual(['WINE', 'SILK', 'SPICES', 'FURS']);
    expect(goodOf('ROMAN')).toBe('WINE');
    expect(goodOf('NORSE')).toBe('FURS');
    expect(goodLabel('SPICES')).toBe('especias');
    expect(foreignGoods({ ...vacio, WINE: 100, SILK: 25, FURS: 35 }, 'ROMAN')).toBe(60);
    expect(foreignGoods({ ...vacio, WINE: 100 }, 'ROMAN')).toBe(0);
  });

  it('el botón «Taller» dice por qué no responde (RN-GOODS-003)', () => {
    expect(workshopBlockReason(ciudad({ era: 'STONE_AGE' }), [])).toMatch(/Bronce/);
    expect(workshopBlockReason(ciudad(), [{ type: 'WORKSHOP', x: 4, y: 4 }])).toMatch(/ya tiene un taller/);
    expect(workshopBlockReason(ciudad({ stocks: { WOOD: 90, STONE: 50 } }), [])).toBe('Faltan 10 de madera.');
    expect(workshopBlockReason(ciudad(), [])).toBeNull();
  });

  it('el botón «Elaborar» dice por qué no responde (RN-GOODS-004)', () => {
    const completo = [{ type: 'WORKSHOP', status: 'COMPLETE' }];
    expect(craftBlockReason(ciudad(), [])).toMatch(/Construye antes un taller/);
    expect(craftBlockReason(ciudad(), [{ type: 'WORKSHOP', status: 'UNDER_CONSTRUCTION' }])).toMatch(/obras/);
    expect(craftBlockReason(ciudad({ craft: { good: 'WINE', amount: 5, completesAtMs: 1 } }), completo)).toMatch(/elaborando/);
    expect(craftBlockReason(ciudad({ stocks: { WOOD: 30, STONE: 10 } }), completo)).toBe('Faltan 5 de piedra.');
    expect(craftBlockReason(ciudad(), completo)).toBeNull();
  });

  it('desde Bronce se avanza a Hierro con 60 bienes ajenos; desde Hierro, nada (Q5, Q11)', () => {
    expect(nextEra('STONE_AGE')).toBe('BRONZE_AGE');
    expect(nextEra('BRONZE_AGE')).toBe('IRON_AGE');
    expect(nextEra('IRON_AGE')).toBeNull();
    expect(advanceBlockReason(ciudad(), 60)).toBeNull();
    expect(advanceBlockReason(ciudad(), 59)).toBe('Faltan 1 bienes de otras civilizaciones: cómpralos con un trato.');
    expect(advanceBlockReason(ciudad({ stocks: { WOOD: 700 } }), 0)).toBe(
      'Faltan 100 de madera y 60 bienes de otras civilizaciones: cómpralos con un trato.',
    );
    expect(advanceBlockReason(ciudad({ era: 'IRON_AGE' }), 60)).toMatch(/más eras/);
  });
});

describe('el store y los bienes', () => {
  const state = () => useWorldStore.getState();
  const server = (type: string, seq: number, payload: unknown) =>
    ({ v: 1, type, seq, ts: 1_757_376_000_000, payload }) as v1.ServerMessage;

  beforeEach(() => {
    state().reset();
    state().applySnapshot({
      serverTimeMs: 1_757_376_000_000, tick: 1, chunks: [{ cx: 0, cy: 0 }], terrain: [], units: [], territories: [],
      buildings: [],
      cities: [{
        id: 10, ownerPlayerId: '11111111-1111-4111-8111-111111111111', name: 'Roma', centerX: 5, centerY: 5,
        era: 'BRONZE_AGE', civilization: 'ROMAN', population: 3, populationLimit: 50, presenceState: 'ONLINE',
        protectionUntilMs: null, stocks: { WOOD: 40, STONE: 15 }, production: null, advance: null,
      }],
    } as v1.WorldSnapshot['payload']);
  });

  it('un servidor que no manda bienes los deja a 0, y sin craft no hay elaboración', () => {
    expect(state().cities.get(10)!.stocks).toEqual({ ...vacio, WOOD: 40, STONE: 15 });
    expect(state().cities.get(10)!.craft).toBeNull();
  });

  it('city.update empieza la elaboración, la termina en null y lleva los bienes', () => {
    state().applyServerMessage(server('city.update', 2, {
      id: 10, stocks: { WOOD: 10, STONE: 0, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 },
      craft: { good: 'WINE', amount: 5, completesAtMs: 99 },
    }));
    expect(state().cities.get(10)!.craft).toEqual({ good: 'WINE', amount: 5, completesAtMs: 99 });
    state().applyServerMessage(server('city.update', 3, { id: 10, stocks: { WOOD: 10, STONE: 0, WINE: 5 } }));
    expect(state().cities.get(10)!.craft, 'sin craft en el mensaje, se conserva').not.toBeNull();
    state().applyServerMessage(server('city.update', 4, { id: 10, craft: null }));
    expect(state().cities.get(10)!.craft).toBeNull();
    expect(state().cities.get(10)!.stocks.WINE).toBe(5);
  });
});
