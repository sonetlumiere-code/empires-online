import { describe, expect, it } from 'vitest';

import type { TreatyView } from './diplomacy';
import {
  applyTradeUpdate,
  canTradeWith,
  describeLines,
  draftLines,
  formatHours,
  offerBlockReason,
  TRADE_RESOURCES,
  pendingWith,
  tradeActions,
  tradeLabel,
  TRADE_MAX_AMOUNT,
  type TradeOfferView,
} from './trade';

// SPEC-TRADE en el cliente: lo puro. El servidor decide si hay tratado, si alcanza
// el almacén y qué se mueve.

const otro = '7c9e6679-7425-40de-944b-e07fc1f90ae7';
const base: TradeOfferView = {
  id: 3, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Vecinópolis',
  give: [{ resource: 'WOOD', amount: 300 }], get: [{ resource: 'STONE', amount: 100 }],
  status: 'PROPOSED', proposedByMe: true, expiresAtMs: 10 * 3_600_000,
};

describe('applyTradeUpdate: la lista de ofertas pendientes', () => {
  it('añade, sustituye y ordena por id; quita lo que se cierra', () => {
    let lista = applyTradeUpdate([], { ...base, id: 5 });
    lista = applyTradeUpdate(lista, base);
    expect(lista.map((o) => o.id)).toEqual([3, 5]);
    for (const fin of ['ACCEPTED', 'REJECTED', 'WITHDRAWN', 'EXPIRED'] as const) {
      expect(applyTradeUpdate(lista, { ...base, status: fin }).map((o) => o.id)).toEqual([5]);
    }
  });
});

describe('tradeActions: qué ofrece el HUD', () => {
  it('la propia se retira; la recibida se acepta o se rechaza; una cerrada, nada', () => {
    expect(tradeActions(base)).toEqual(['withdraw']);
    expect(tradeActions({ ...base, proposedByMe: false })).toEqual(['accept', 'reject']);
    expect(tradeActions({ ...base, status: 'ACCEPTED' })).toEqual([]);
  });
});

describe('tradeLabel: la oferta desde cada lado', () => {
  it('dice lo que sale y lo que entra, y cuánto le queda', () => {
    expect(tradeLabel(base, 0)).toBe('Ofreces a Vecinópolis 300 de madera por 100 de piedra (caduca en 10 h)');
    const recibida = { ...base, proposedByMe: false, give: base.get, get: base.give };
    expect(tradeLabel(recibida, 10 * 3_600_000 - 90_000)).toBe(
      'Vecinópolis te ofrece 300 de madera por 100 de piedra (caduca en 2 min)',
    );
    expect(tradeLabel({ ...base, otherCityName: null }, 0)).toContain('otro jugador');
  });
  it('varias líneas y plazos vencidos', () => {
    expect(describeLines([{ resource: 'WOOD', amount: 1 }, { resource: 'STONE', amount: 2 }])).toBe(
      '1 de madera y 2 de piedra',
    );
    expect(formatHours(-5)).toBe('0 min');
  });
});

describe('canTradeWith y pendingWith: qué botones enseñar', () => {
  const tratado: TreatyView = {
    id: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Vecinópolis',
    type: 'TRADE', status: 'ACTIVE', proposedByMe: false, breaksAtMs: null,
  };
  it('cualquier tratado vigente deja comerciar, también en preaviso; una oferta de tratado no', () => {
    expect(canTradeWith([tratado], otro)).toBe(true);
    expect(canTradeWith([{ ...tratado, type: 'ALLIANCE', breaksAtMs: 5 }], otro)).toBe(true);
    expect(canTradeWith([{ ...tratado, status: 'PROPOSED' }], otro)).toBe(false);
    expect(canTradeWith([tratado], 'otro-jugador')).toBe(false);
  });
  it('una oferta pendiente con el mismo jugador, en cualquier dirección', () => {
    expect(pendingWith([base], otro)?.id).toBe(3);
    expect(pendingWith([{ ...base, proposedByMe: false }], otro)?.id).toBe(3);
    expect(pendingWith([base], 'otro-jugador')).toBeUndefined();
  });
});

describe('el formulario del trato', () => {
  it('se da un recurso y se pide otro, también un bien (RN-GOODS-008)', () => {
    expect(draftLines({ giveResource: 'STONE', giveAmount: 20, wantResource: 'WOOD', wantAmount: 80 })).toEqual({
      give: [{ resource: 'STONE', amount: 20 }],
      want: [{ resource: 'WOOD', amount: 80 }],
    });
    expect(draftLines({ giveResource: 'WINE', giveAmount: 30, wantResource: 'FURS', wantAmount: 20 })).toEqual({
      give: [{ resource: 'WINE', amount: 30 }],
      want: [{ resource: 'FURS', amount: 20 }],
    });
    expect(TRADE_RESOURCES).toEqual(['WOOD', 'STONE', 'WINE', 'SILK', 'SPICES', 'FURS']);
    expect(describeLines([{ resource: 'SPICES', amount: 5 }])).toBe('5 de especias');
  });
  it('se bloquea con su motivo lo que el servidor rechazaría seguro', () => {
    const stocks = { WOOD: 300, STONE: 5 };
    expect(offerBlockReason({ giveResource: 'WOOD', giveAmount: 300, wantResource: 'STONE', wantAmount: 100 }, stocks)).toBeNull();
    expect(offerBlockReason({ giveResource: 'WOOD', giveAmount: 301, wantResource: 'STONE', wantAmount: 100 }, stocks)).toBe(
      'No tienes 301 de madera',
    );
    expect(offerBlockReason({ giveResource: 'STONE', giveAmount: 6, wantResource: 'WOOD', wantAmount: 1 }, stocks)).toBe('No tienes 6 de piedra');
    expect(offerBlockReason({ giveResource: 'WOOD', giveAmount: 0, wantResource: 'STONE', wantAmount: 1 }, stocks)).toMatch(/mayores que 0/);
    expect(offerBlockReason({ giveResource: 'WOOD', giveAmount: 10, wantResource: 'STONE', wantAmount: 1.5 }, stocks)).toMatch(/enteros/);
    expect(
      offerBlockReason({ giveResource: 'WOOD', giveAmount: 1, wantResource: 'STONE', wantAmount: TRADE_MAX_AMOUNT + 1 }, stocks),
    ).toMatch(/Como mucho/);
    expect(offerBlockReason({ giveResource: 'WOOD', giveAmount: 1, wantResource: 'WOOD', wantAmount: 1 }, stocks)).toBe(
      'No se pide lo mismo que se da',
    );
    expect(offerBlockReason({ giveResource: 'WINE', giveAmount: 31, wantResource: 'FURS', wantAmount: 1 }, { WINE: 30 })).toBe(
      'No tienes 31 de vino',
    );
    expect(offerBlockReason({ giveResource: 'STONE', giveAmount: 1, wantResource: 'WOOD', wantAmount: 1 }, { WOOD: 0 })).toBe(
      'No tienes 1 de piedra',
    );
  });
});

describe('el store guarda las ofertas que cuenta el servidor', () => {
  it('el snapshot las reemplaza y trade.update las añade o las quita', async () => {
    const { useWorldStore } = await import('./world');
    const state = () => useWorldStore.getState();
    const PLAYER = '11111111-1111-4111-8111-111111111111';
    state().reset();
    state().applyWelcome({
      playerId: PLAYER, sessionId: '22222222-2222-4222-8222-222222222222',
      world: { width: 64, height: 64, chunkSize: 32 }, heartbeatIntervalMs: 10_000,
    } as never);
    const snap = { serverTimeMs: 1, tick: 1, chunks: [{ cx: 0, cy: 0 }], terrain: [], units: [], cities: [],
      territories: [], buildings: [] };
    state().applySnapshot({ ...snap, tradeOffers: [{ ...base, id: 9 }] } as never);
    expect(state().tradeOffers.map((o) => o.id)).toEqual([9]);

    state().applyServerMessage({ v: 1, type: 'trade.update', seq: 2, ts: 1, payload: { offer: base } } as never);
    expect(state().tradeOffers.map((o) => o.id)).toEqual([3, 9]);
    state().applyServerMessage({ v: 1, type: 'trade.update', seq: 3, ts: 1,
      payload: { offer: { ...base, status: 'ACCEPTED' } } } as never);
    expect(state().tradeOffers.map((o) => o.id)).toEqual([9]);

    state().applySnapshot(snap as never);
    expect(state().tradeOffers).toEqual([]);
  });
});
