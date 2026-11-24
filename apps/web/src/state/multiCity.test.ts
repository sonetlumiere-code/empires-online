import { describe, expect, it } from 'vitest';

import { foundCityBlockReason, nextHome, ownCitiesOf, shownCity } from './multiCity';
import type { CityView, UnitView } from './world';

const YO = '11111111-1111-4111-8111-111111111111';
const OTRO = '22222222-2222-4222-8222-222222222222';

function ciudad(over: Partial<CityView> = {}): CityView {
  return {
    id: 10, ownerPlayerId: YO, name: 'Testópolis', centerX: 10, centerY: 10, era: 'BRONZE_AGE',
    stocks: { WOOD: 800, STONE: 400, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 },
    ...over,
  } as CityView;
}

function aldeano(over: Partial<UnitView> = {}): UnitView {
  return {
    id: 1, playerId: YO, cityId: 10, unitType: 'VILLAGER', x: 40, y: 40, hp: 40, maxHp: 40,
    status: 'IDLE', movement: null, garrisonCityId: null, ...over,
  } as UnitView;
}

describe('las ciudades propias (SPEC-MULTI-CITY)', () => {
  it('van por id, la capital primero, sin las ajenas', () => {
    const cs = [ciudad({ id: 30 }), ciudad({ id: 5, ownerPlayerId: OTRO }), ciudad({ id: 10 })];
    expect(ownCitiesOf(cs, YO).map((c) => c.id)).toEqual([10, 30]);
    expect(ownCitiesOf(cs, null)).toEqual([]);
  });

  it('«H» va a la capital, luego a la otra y vuelve (Q9)', () => {
    const own = [ciudad({ id: 10 }), ciudad({ id: 30 })];
    expect(nextHome(own, null)?.id).toBe(10);
    expect(nextHome(own, 10)?.id).toBe(30);
    expect(nextHome(own, 30)?.id).toBe(10);
    expect(nextHome(own, 99)?.id).toBe(10);
    expect(nextHome([], null)).toBeUndefined();
    expect(shownCity(own, 30)?.id).toBe(30);
    expect(shownCity(own, null)?.id).toBe(10);
    expect(shownCity([], null)).toBeNull();
  });
});

describe('«Fundar ciudad» (RN-CITY-030, RN-CITY-031)', () => {
  it('se ofrece con un aldeano parado de una ciudad en Bronce con 800 y 400', () => {
    expect(foundCityBlockReason(aldeano(), [ciudad()])).toBeNull();
  });

  it('dice por qué no', () => {
    const own = [ciudad()];
    expect(foundCityBlockReason(undefined, own)).toBe('Selecciona un aldeano');
    expect(foundCityBlockReason(aldeano({ unitType: 'WARRIOR' }), own)).toBe('Sólo un aldeano funda una ciudad');
    expect(foundCityBlockReason(aldeano(), [ciudad(), ciudad({ id: 30 })])).toBe('Ya tienes dos ciudades');
    expect(foundCityBlockReason(aldeano({ garrisonCityId: 10 }), own)).toBe('El aldeano está guarnecido');
    expect(foundCityBlockReason(aldeano({ status: 'MOVING' }), own)).toBe('El aldeano tiene que estar parado');
    expect(foundCityBlockReason(aldeano({ cityId: 77 }), own)).toBe('El aldeano no es de una ciudad tuya a la vista');
    expect(foundCityBlockReason(aldeano(), [ciudad({ era: 'STONE_AGE' })])).toBe('Testópolis tiene que estar en la Edad de Bronce');
    const pobre = ciudad({ stocks: { WOOD: 799, STONE: 400, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 } });
    expect(foundCityBlockReason(aldeano(), [pobre])).toBe('Fundar cuesta 800 de madera y 400 de piedra de Testópolis');
  });
});
