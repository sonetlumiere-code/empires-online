import { describe, expect, it } from 'vitest';

import {
  capitalOf,
  commerceCityId,
  foundCityBlockReason,
  maxOwnCities,
  homesOf,
  nextHome,
  ownCitiesOf,
  shownCity,
} from './multiCity';
import type { CityView, OwnCityHome, UnitView } from './world';

const YO = '11111111-1111-4111-8111-111111111111';
const OTRO = '22222222-2222-4222-8222-222222222222';

function ciudad(over: Partial<CityView> = {}): CityView {
  return {
    id: 10,
    ownerPlayerId: YO,
    name: 'Testópolis',
    centerX: 10,
    centerY: 10,
    era: 'BRONZE_AGE',
    stocks: { WOOD: 800, STONE: 400, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 },
    ...over,
  } as CityView;
}

function aldeano(over: Partial<UnitView> = {}): UnitView {
  return {
    id: 1,
    playerId: YO,
    cityId: 10,
    unitType: 'VILLAGER',
    x: 40,
    y: 40,
    hp: 40,
    maxHp: 40,
    status: 'IDLE',
    movement: null,
    garrisonCityId: null,
    ...over,
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

  it('«H» también lleva a las recordadas fuera de la vista', () => {
    const homes = homesOf(
      new Map([
        [30, { x: 90, y: 90 }],
        [10, { x: 10, y: 10 }],
      ]),
    );
    expect(homes.map((h) => h.id)).toEqual([10, 30]);
    expect(nextHome(homes, 10)).toEqual({ id: 30, x: 90, y: 90 });
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
    expect(foundCityBlockReason(aldeano(), [ciudad(), ciudad({ id: 30 })])).toBe(
      'Ya tienes 2 ciudades: el tope de tu capital es 2',
    );
    // SPEC-MORE-CITIES RN-CITY-040: con la capital en Hierro, la tercera cabe; en Castillos, la cuarta.
    expect(foundCityBlockReason(aldeano(), [ciudad({ era: 'IRON_AGE' }), ciudad({ id: 30 })])).toBeNull();
    expect(foundCityBlockReason(aldeano(), [ciudad({ era: 'IRON_AGE' }), ciudad({ id: 30 }), ciudad({ id: 31 })])).toBe(
      'Ya tienes 3 ciudades: el tope de tu capital es 3',
    );
    expect(maxOwnCities('STONE_AGE')).toBe(1);
    expect(maxOwnCities('CASTLE_AGE')).toBe(4);
    expect(foundCityBlockReason(aldeano({ garrisonCityId: 10 }), own)).toBe('El aldeano está guarnecido');
    expect(foundCityBlockReason(aldeano({ status: 'MOVING' }), own)).toBe('El aldeano tiene que estar parado');
    expect(foundCityBlockReason(aldeano({ cityId: 77 }), own)).toBe('El aldeano no es de una ciudad tuya a la vista');
    expect(foundCityBlockReason(aldeano(), [ciudad({ era: 'STONE_AGE' })])).toBe(
      'Testópolis tiene que estar en la Edad de Bronce',
    );
    const pobre = ciudad({
      stocks: { WOOD: 799, STONE: 400, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 },
    });
    expect(foundCityBlockReason(aldeano(), [pobre])).toBe('Fundar cuesta 800 de madera y 400 de piedra de Testópolis');
  });

  // Encontrado en el navegador (2026-11-26): smoke_multi, con la cámara en su segunda
  // ciudad y la capital fuera del área, veía «Fundar cuesta…» en lugar del tope.
  it('cuenta el tope con las ciudades de fuera de la vista y la era de la capital (SPEC-MULTI-CITY Q9)', () => {
    const segunda = ciudad({ id: 30, name: 'Testópolis II', stocks: { WOOD: 0, STONE: 0, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 } });
    const homes = new Map<number, OwnCityHome>([
      [10, { x: 90, y: 90, era: 'BRONZE_AGE' }],
      [30, { x: 10, y: 10, era: 'BRONZE_AGE' }],
    ]);
    expect(foundCityBlockReason(aldeano({ cityId: 30 }), [segunda], homes)).toBe(
      'Ya tienes 2 ciudades: el tope de tu capital es 2',
    );
    // La capital en Hierro, fuera de la vista: la tercera cabe; y lo que cuenta es lo de la que funda.
    homes.set(10, { x: 90, y: 90, era: 'IRON_AGE' });
    expect(foundCityBlockReason(aldeano({ cityId: 30 }), [segunda], homes)).toBe(
      'Fundar cuesta 800 de madera y 400 de piedra de Testópolis II',
    );
    // La era de una capital a la vista manda sobre la recordada.
    expect(foundCityBlockReason(aldeano(), [ciudad({ era: 'IRON_AGE' })], new Map([[10, { x: 10, y: 10, era: 'BRONZE_AGE' }]]))).toBeNull();
  });
});

describe('la capital con las ciudades de fuera de la vista (SPEC-MULTI-CITY Q9)', () => {
  it('es la de id menor de todas, con su era y cuántas hay', () => {
    const homes = new Map<number, OwnCityHome>([[10, { x: 90, y: 90, era: 'CASTLE_AGE' }]]);
    expect(capitalOf([ciudad({ id: 30 })], homes)).toEqual({ id: 10, era: 'CASTLE_AGE', count: 2 });
    expect(capitalOf([ciudad({ id: 30 })])).toEqual({ id: 30, era: 'BRONZE_AGE', count: 1 });
    expect(capitalOf([])).toBeNull();
  });
});

describe('de qué ciudad sale el comercio (SPEC-TRADE-FROM-CITY Q7)', () => {
  it('la del panel si no es la capital; la capital no se dice', () => {
    const capital = ciudad();
    const segunda = ciudad({ id: 30 });
    expect(commerceCityId(segunda, [capital, segunda])).toBe(30);
    expect(commerceCityId(capital, [capital, segunda])).toBeUndefined();
    expect(commerceCityId(null, [capital])).toBeUndefined();
    expect(commerceCityId(ciudad({ id: 99 }), [capital, segunda])).toBeUndefined();
  });

  // Encontrado en el navegador (2026-11-26): con la capital fuera del área, el panel
  // muestra la segunda y la orden salía sin cityId, de la capital.
  it('con la capital fuera de la vista, sale de la del panel', () => {
    const segunda = ciudad({ id: 30 });
    const homes = new Map<number, OwnCityHome>([
      [10, { x: 90, y: 90, era: 'BRONZE_AGE' }],
      [30, { x: 10, y: 10, era: 'BRONZE_AGE' }],
    ]);
    expect(commerceCityId(segunda, [segunda], homes)).toBe(30);
    expect(commerceCityId(segunda, [segunda])).toBeUndefined();
  });
});
