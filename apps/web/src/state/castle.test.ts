import { describe, expect, it } from 'vitest';

import { advanceBlockReason, CASTLE_AGE_COST, CASTLE_COST, castleBlockReason, eraLabel, nextEra } from './economy';
import { isLoss } from './eventLog';
import type { CityStocks } from './world';

// SPEC-CASTLE-AGE en el cliente: el avance de Hierro a Castillos con sus 120
// bienes ajenos, el botón «Castillo» con sus motivos y las muertes del castillo
// como pérdida. El cliente sólo explica: el coste, el plazo y el disparo los
// decide el servidor.

const vacio: CityStocks = { WOOD: 0, STONE: 0, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 };
const ciudad = (over: { era?: string; stocks?: Partial<CityStocks> } = {}) => ({
  id: 10, centerX: 5, centerY: 5, era: over.era ?? 'CASTLE_AGE', civilization: 'NORSE',
  production: null, advance: null,
  stocks: { ...vacio, WOOD: 2_000, STONE: 1_000, ...over.stocks },
});

describe('la Edad de los Castillos', () => {
  it('desde Hierro se avanza a Castillos, que es la última era (RN-CASTLE-001, Q10)', () => {
    expect(nextEra('IRON_AGE')).toBe('CASTLE_AGE');
    expect(nextEra('CASTLE_AGE')).toBeNull();
    expect(eraLabel('CASTLE_AGE')).toBe('Edad de los Castillos');
    expect(CASTLE_AGE_COST).toEqual({ WOOD: 1_600, STONE: 800, FOREIGN_GOODS: 120 });
  });

  it('el avance a Castillos dice qué falta, también los bienes ajenos', () => {
    const hierro = ciudad({ era: 'IRON_AGE' });
    expect(advanceBlockReason(hierro, 120)).toBeNull();
    expect(advanceBlockReason(hierro, 60)).toBe('Faltan 60 bienes de otras civilizaciones: cómpralos con un trato.');
    expect(advanceBlockReason(ciudad({ era: 'IRON_AGE', stocks: { WOOD: 1_500, STONE: 800 } }), 0)).toBe(
      'Faltan 100 de madera y 120 bienes de otras civilizaciones: cómpralos con un trato.',
    );
    expect(advanceBlockReason(ciudad(), 500)).toMatch(/más eras/);
  });

  it('el botón «Castillo» dice por qué no responde (RN-CASTLE-002)', () => {
    expect(CASTLE_COST).toEqual({ WOOD: 500, STONE: 800 });
    expect(castleBlockReason(ciudad({ era: 'IRON_AGE' }), [])).toMatch(/Edad de los Castillos/);
    expect(castleBlockReason(ciudad(), [{ type: 'CASTLE', x: 4, y: 4 }])).toMatch(/ya tiene un castillo/);
    expect(castleBlockReason(ciudad({ stocks: { WOOD: 500, STONE: 700 } }), [])).toBe('Faltan 100 de piedra.');
    const llena = [
      { x: 4, y: 4 }, { x: 5, y: 4 }, { x: 6, y: 4 }, { x: 4, y: 5 }, { x: 6, y: 5 }, { x: 4, y: 6 }, { x: 5, y: 6 }, { x: 6, y: 6 },
    ].map((t) => ({ type: 'HOUSE', ...t }));
    expect(castleBlockReason(ciudad(), llena)).toMatch(/hueco/);
    expect(castleBlockReason(ciudad(), [{ type: 'WORKSHOP', x: 4, y: 4 }])).toBeNull();
  });

  it('una unidad que mató un castillo es una pérdida; lo que mató el propio, no', () => {
    const entrada = (kind: string) =>
      ({ id: 1, kind, firstAtMs: 1, lastAtMs: 1, count: 1, otherPlayerId: 'x', otherCityId: 2, otherCityName: 'Vecina' }) as never;
    expect(isLoss(entrada('UNIT_LOST_TO_CASTLE'))).toBe(true);
    expect(isLoss(entrada('CASTLE_KILL'))).toBe(false);
  });
});
