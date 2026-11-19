import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import {
  applyMarketListing,
  applyMarketUpdate,
  buyBlockReason,
  buyCost,
  commission,
  EMPTY_MARKET,
  lotLabel,
  marketBuildBlockReason,
  marketMissingReason,
  offerDistance,
  ownOfferLabel,
  postBlockReason,
  postCost,
  type MarketDraft,
  type MarketOfferView,
} from './market';
import { useWorldStore, type CityStocks } from './world';

// SPEC-MARKETS en el cliente: lo puro. El servidor decide si hay mercado, si
// alcanza el almacén, la comisión y la ruta.

const yo = '11111111-1111-4111-8111-111111111111';
const otro = '7c9e6679-7425-40de-944b-e07fc1f90ae7';
const vacio: CityStocks = { WOOD: 0, STONE: 0, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 };
// El ejemplo de la spec: 3 lotes de 20 de piedra por 30 de madera.
const oferta: MarketOfferView = {
  id: 9, playerId: otro, cityId: 7, cityName: 'Villa Norte', civilization: 'PERSIAN', cityX: 100, cityY: 40,
  give: { resource: 'STONE', amount: 20 }, want: { resource: 'WOOD', amount: 30 },
  lotsTotal: 3, lotsLeft: 3, lotsSold: 0, status: 'OPEN', expiresAtMs: 10 * 3_600_000,
};
const mercado = [{ type: 'MARKET', status: 'COMPLETE' }];
const ciudad = { centerX: 40, centerY: 40, ownerPlayerId: yo, stocks: { ...vacio, WOOD: 100, STONE: 100 } };

describe('la comisión y los costes (RN-MARKET-006, SPEC-MARKETS §4.3)', () => {
  it('la décima parte de cada lote, redondeada hacia arriba', () => {
    expect([1, 9, 10, 11, 20, 30, 100].map(commission)).toEqual([1, 1, 1, 2, 2, 3, 10]);
    expect(commission(0)).toBe(0);
  });

  it('el ejemplo de la spec: publicar cuesta 66 de piedra y comprar 2 lotes, 66 de madera', () => {
    expect(postCost(20, 3)).toEqual({ custody: 60, commission: 6, total: 66 });
    expect(buyCost(oferta, 2)).toEqual({ pay: 60, commission: 6, total: 66, receive: 40 });
  });
});

describe('el estado del mercado', () => {
  it('market.update de una oferta propia la añade mientras está abierta y la quita al cerrarse', () => {
    const propia = { ...oferta, playerId: yo };
    let s = applyMarketUpdate(EMPTY_MARKET, { ...propia, id: 12 }, yo);
    s = applyMarketUpdate(s, propia, yo);
    expect(s.own.map((o) => o.id)).toEqual([9, 12]);
    for (const fin of ['SOLD_OUT', 'WITHDRAWN', 'EXPIRED'] as const) {
      expect(applyMarketUpdate(s, { ...propia, status: fin }, yo).own.map((o) => o.id)).toEqual([12]);
    }
  });

  it('market.update de una ajena actualiza su fila del tablón, o la quita si se agotó', () => {
    let s = applyMarketListing(EMPTY_MARKET, [oferta, { ...oferta, id: 10 }]);
    s = applyMarketUpdate(s, { ...oferta, lotsLeft: 1, lotsSold: 2 }, yo);
    expect(s.board!.find((o) => o.id === 9)!.lotsLeft).toBe(1);
    s = applyMarketUpdate(s, { ...oferta, lotsLeft: 0, lotsSold: 3, status: 'SOLD_OUT' }, yo);
    expect(s.board!.map((o) => o.id)).toEqual([10]);
    expect(s.own).toEqual([]);
    expect(applyMarketUpdate(EMPTY_MARKET, oferta, yo), 'sin tablón pedido no hay nada que tocar').toBe(EMPTY_MARKET);
  });
});

describe('los motivos', () => {
  it('el mercado se construye desde Bronce, uno por ciudad, con 150 de madera y 100 de piedra', () => {
    const base = { id: 1, centerX: 40, centerY: 40, era: 'BRONZE_AGE', stocks: { ...vacio, WOOD: 150, STONE: 100 } };
    expect(marketBuildBlockReason({ ...base, era: 'STONE_AGE' }, [])).toBe('El mercado pide la Edad de Bronce.');
    expect(marketBuildBlockReason(base, [{ type: 'MARKET', x: 41, y: 39 }])).toBe('La ciudad ya tiene un mercado.');
    expect(marketBuildBlockReason({ ...base, stocks: { ...vacio, WOOD: 100, STONE: 90 } }, [])).toBe('Faltan 50 de madera y 10 de piedra.');
    expect(marketBuildBlockReason(base, [])).toBeNull();
  });

  it('sin mercado completo no se publica ni se compra', () => {
    expect(marketMissingReason([])).toBe('Sin mercado');
    expect(marketMissingReason([{ type: 'MARKET', status: 'UNDER_CONSTRUCTION' }])).toBe('El mercado está en obras');
    expect(marketMissingReason(mercado)).toBeNull();
  });

  it('publicar: 3 abiertas, recursos distintos, enteros en rango y el almacén con la comisión', () => {
    const d: MarketDraft = { giveResource: 'STONE', giveAmount: 20, wantResource: 'WOOD', wantAmount: 30, lots: 3 };
    const stocks = { ...vacio, STONE: 66 };
    expect(postBlockReason(d, stocks, [], 0)).toBe('Sin mercado');
    expect(postBlockReason(d, stocks, mercado, 3)).toBe('Tienes 3 ofertas abiertas');
    expect(postBlockReason({ ...d, wantResource: 'STONE' }, stocks, mercado, 0)).toBe('Das y pides lo mismo');
    expect(postBlockReason({ ...d, giveAmount: 0 }, stocks, mercado, 0)).toBe('Cada lote da y pide al menos 1');
    expect(postBlockReason({ ...d, lots: 101 }, stocks, mercado, 0)).toBe('De 1 a 100 lotes');
    expect(postBlockReason({ ...d, giveAmount: 10_001, lots: 100 }, { ...vacio, STONE: 2_000_000 }, mercado, 0)).toBe('Como mucho 1000000 por lado');
    expect(postBlockReason(d, { ...vacio, STONE: 65 }, mercado, 0)).toBe('Falta piedra');
    expect(postBlockReason(d, stocks, mercado, 2)).toBeNull();
  });

  it('comprar: la propia, los lotes que quedan, el alcance y el almacén', () => {
    expect(buyBlockReason(oferta, 1, ciudad, [])).toBe('Sin mercado');
    expect(buyBlockReason({ ...oferta, playerId: yo }, 1, ciudad, mercado)).toBe('Es tu oferta');
    expect(buyBlockReason(oferta, 4, ciudad, mercado)).toBe('Quedan 3 lotes');
    expect(buyBlockReason({ ...oferta, lotsLeft: 1 }, 2, ciudad, mercado)).toBe('Queda 1 lote');
    expect(buyBlockReason({ ...oferta, cityX: 400 }, 1, ciudad, mercado)).toBe('Fuera de alcance');
    expect(buyBlockReason(oferta, 3, ciudad, mercado), '99 de madera y 100 que hay').toBeNull();
    expect(buyBlockReason(oferta, 3, { ...ciudad, stocks: { ...vacio, WOOD: 98 } }, mercado)).toBe('Falta madera');
  });

  it('la distancia y los textos', () => {
    expect(offerDistance(ciudad, oferta)).toBe(60);
    expect(offerDistance(ciudad, { ...oferta, cityX: null })).toBeNull();
    expect(lotLabel(oferta)).toBe('20 de piedra por 30 de madera');
    expect(ownOfferLabel({ ...oferta, lotsLeft: 1, lotsSold: 2 })).toBe('20 de piedra por 30 de madera · 2 vendidos, quedan 1 de 3');
  });
});

describe('el store y el mercado', () => {
  const state = () => useWorldStore.getState();
  const server = (type: string, seq: number, payload: unknown) =>
    ({ v: 1, type, seq, ts: 1_757_376_000_000, payload }) as unknown as v1.ServerMessage;

  beforeEach(() => {
    state().reset();
    useWorldStore.setState({ playerId: yo });
  });

  it('el snapshot trae las propias; market.listing, el tablón; market.update los actualiza', () => {
    const propia = { ...oferta, id: 3, playerId: yo };
    state().applySnapshot({
      serverTimeMs: 1, tick: 1, chunks: [], terrain: [], units: [], cities: [], territories: [], buildings: [],
      marketOffers: [propia],
    } as v1.WorldSnapshot['payload']);
    expect(state().market.own.map((o) => o.id)).toEqual([3]);
    expect(state().market.board).toBeNull();

    state().applyServerMessage(server('market.listing', 2, { offers: [oferta] }));
    expect(state().market.board!.map((o) => o.id)).toEqual([9]);
    state().applyServerMessage(server('market.update', 3, { offer: { ...propia, status: 'WITHDRAWN' } }));
    expect(state().market.own).toEqual([]);
    state().applyServerMessage(server('market.update', 4, { offer: { ...oferta, lotsLeft: 2, lotsSold: 1 } }));
    expect(state().market.board![0]!.lotsLeft).toBe(2);
    state().reset();
    expect(state().market).toEqual(EMPTY_MARKET);
  });
});
