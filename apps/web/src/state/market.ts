/**
 * Decisiones puras del mercado en el cliente (SPEC-MARKETS).
 *
 * Como las del comercio, no deciden nada autoritativo: describen lo que el
 * servidor contó y qué órdenes tiene sentido ofrecer, con su motivo cuando no.
 * Si hay mercado, si alcanza el almacén, la comisión y la ruta los decide el
 * servidor (INV-SEC-001); esto sólo evita mandar lo que se rechazaría seguro.
 * Ver ../../../../docs/specs/markets.md
 */
import { v1 } from '@empires-online/protocol';

import { resourceName } from './trade';
import { firstFreeHouseTile } from './economy';
import type { BuildingView, CityStocks, CityView } from './world';

export type MarketOfferView = v1.MarketOfferView;
type ResourceType = v1.ResourceType;

/** Máximo de lotes de una oferta y de ofertas abiertas por jugador (Q10). */
export const MARKET_MAX_LOTS = v1.MARKET_MAX_LOTS;
export const MARKET_MAX_OPEN = 3;
/** Cota técnica del total de un lado (ADR-032 §7). */
export const MARKET_MAX_TOTAL = v1.TRADE_MAX_AMOUNT;
/** El alcance de las caravanas (EO_PATHFINDING_MAX_DISTANCE, en Chebyshev). */
export const MARKET_REACH_TILES = 256;

/** Lo que cuesta el mercado (SPEC-MARKETS Q3). */
export const MARKET_COST = { WOOD: 150, STONE: 100 } as const;

/** Lo que el cliente sabe del mercado: sus ofertas abiertas y el último tablón pedido. */
export interface MarketState {
  /** Las ofertas abiertas del jugador, por id. */
  own: readonly MarketOfferView[];
  /** El último tablón recibido, en el orden del servidor; `null` si no se ha pedido. */
  board: readonly MarketOfferView[] | null;
}

export const EMPTY_MARKET: MarketState = { own: [], board: null };

/**
 * La comisión de un lote que da `amount` (RN-MARKET-006): la décima parte,
 * redondeada hacia arriba. Un lote de 1 paga 1.
 */
export function commission(amount: number): number {
  return amount > 0 ? Math.ceil(amount / 10) : 0;
}

/** Lo que sale del almacén al publicar: la custodia de todos los lotes y la comisión. */
export function postCost(giveAmount: number, lots: number): { custody: number; commission: number; total: number } {
  const custody = giveAmount * lots;
  const c = commission(giveAmount) * lots;
  return { custody, commission: c, total: custody + c };
}

/** Lo que paga y recibe quien compra `lots` lotes de una oferta. */
export function buyCost(o: Pick<MarketOfferView, 'give' | 'want'>, lots: number): { pay: number; commission: number; total: number; receive: number } {
  const pay = o.want.amount * lots;
  const c = commission(o.want.amount) * lots;
  return { pay, commission: c, total: pay + c, receive: o.give.amount * lots };
}

/**
 * Aplica un market.update. Si es una oferta propia, la añade o la sustituye
 * mientras está abierta y la quita al cerrarse; si es ajena (el comprador la
 * recibe tras comprar), actualiza su fila del tablón, o la quita si se agotó.
 */
export function applyMarketUpdate(state: MarketState, o: MarketOfferView, playerId: string | null): MarketState {
  if (o.playerId === playerId) {
    const rest = state.own.filter((x) => x.id !== o.id);
    if (o.status === 'OPEN') rest.push({ ...o });
    return { ...state, own: rest.sort((a, b) => a.id - b.id) };
  }
  if (state.board === null) return state;
  const board = o.status === 'OPEN'
    ? state.board.map((x) => (x.id === o.id ? { ...o } : x))
    : state.board.filter((x) => x.id !== o.id);
  return { ...state, board };
}

/** El tablón recibido sustituye al anterior. */
export function applyMarketListing(state: MarketState, offers: readonly MarketOfferView[]): MarketState {
  return { ...state, board: offers.map((o) => ({ ...o })) };
}

/** La distancia de Chebyshev entre la ciudad propia y la de la oferta, o `null` si no se conoce. */
export function offerDistance(own: Pick<CityView, 'centerX' | 'centerY'>, o: Pick<MarketOfferView, 'cityX' | 'cityY'>): number | null {
  if (o.cityX === null || o.cityY === null) return null;
  return Math.max(Math.abs(own.centerX - o.cityX), Math.abs(own.centerY - o.cityY));
}

/** Por qué la ciudad no tiene un mercado con el que operar, o `null` si lo tiene. */
export function marketMissingReason(cityBuildings: readonly Pick<BuildingView, 'type' | 'status'>[]): string | null {
  const m = cityBuildings.find((b) => b.type === 'MARKET');
  if (!m) return 'Sin mercado';
  if (m.status !== 'COMPLETE') return 'El mercado está en obras';
  return null;
}

/**
 * Por qué no se puede encargar el mercado, o `null` si se puede intentar
 * (RN-MARKET-001): pide Bronce, uno por ciudad, un hueco y 150 de madera y 100
 * de piedra.
 */
export function marketBuildBlockReason(
  city: Pick<CityView, 'era' | 'stocks' | 'centerX' | 'centerY' | 'id'>,
  cityBuildings: readonly Pick<BuildingView, 'type' | 'x' | 'y'>[],
): string | null {
  if (city.era === 'STONE_AGE') return 'El mercado pide la Edad de Bronce.';
  if (cityBuildings.some((b) => b.type === 'MARKET')) return 'La ciudad ya tiene un mercado.';
  if (firstFreeHouseTile(city, cityBuildings) === null) return 'No queda hueco en la zona urbana.';
  const faltan: string[] = [];
  if (city.stocks.WOOD < MARKET_COST.WOOD) faltan.push(`${MARKET_COST.WOOD - city.stocks.WOOD} de madera`);
  if (city.stocks.STONE < MARKET_COST.STONE) faltan.push(`${MARKET_COST.STONE - city.stocks.STONE} de piedra`);
  return faltan.length > 0 ? `Faltan ${faltan.join(' y ')}.` : null;
}

/** Lo que arma el formulario de venta: qué da y pide cada lote y cuántos lotes. */
export interface MarketDraft {
  giveResource: ResourceType;
  giveAmount: number;
  wantResource: ResourceType;
  wantAmount: number;
  lots: number;
}

/**
 * Por qué no se puede publicar, o `null` si se puede intentar (RN-MARKET-002): un
 * mercado completo, menos de 3 abiertas, un recurso por lado y distinto, enteros
 * en rango y el almacén con la custodia y la comisión.
 */
export function postBlockReason(
  d: MarketDraft,
  stocks: CityStocks,
  cityBuildings: readonly Pick<BuildingView, 'type' | 'status'>[],
  openOffers: number,
): string | null {
  const sinMercado = marketMissingReason(cityBuildings);
  if (sinMercado !== null) return sinMercado;
  if (openOffers >= MARKET_MAX_OPEN) return `Tienes ${MARKET_MAX_OPEN} ofertas abiertas`;
  if (d.giveResource === d.wantResource) return 'Das y pides lo mismo';
  for (const n of [d.giveAmount, d.wantAmount]) {
    if (!Number.isInteger(n) || n < 1) return 'Cada lote da y pide al menos 1';
  }
  if (!Number.isInteger(d.lots) || d.lots < 1 || d.lots > MARKET_MAX_LOTS) return `De 1 a ${MARKET_MAX_LOTS} lotes`;
  if (d.giveAmount * d.lots > MARKET_MAX_TOTAL || d.wantAmount * d.lots > MARKET_MAX_TOTAL) {
    return `Como mucho ${MARKET_MAX_TOTAL} por lado`;
  }
  const need = postCost(d.giveAmount, d.lots).total;
  const have = stocks[d.giveResource] ?? 0;
  if (have < need) return `Falta ${resourceName(d.giveResource)}`;
  return null;
}

/**
 * Por qué no se pueden comprar `lots` lotes de una oferta, o `null` si se puede
 * intentar (RN-MARKET-004): un mercado completo, una oferta ajena con lotes, la
 * ciudad a su alcance y el almacén con lo que se paga y la comisión.
 */
export function buyBlockReason(
  o: MarketOfferView,
  lots: number,
  city: Pick<CityView, 'centerX' | 'centerY' | 'stocks' | 'ownerPlayerId'>,
  cityBuildings: readonly Pick<BuildingView, 'type' | 'status'>[],
): string | null {
  const sinMercado = marketMissingReason(cityBuildings);
  if (sinMercado !== null) return sinMercado;
  if (o.playerId === city.ownerPlayerId) return 'Es tu oferta';
  if (o.status !== 'OPEN') return 'Ya no está en el mercado';
  if (!Number.isInteger(lots) || lots < 1) return 'Elige cuántos lotes';
  if (lots > o.lotsLeft) return o.lotsLeft === 1 ? 'Queda 1 lote' : `Quedan ${o.lotsLeft} lotes`;
  const d = offerDistance(city, o);
  if (d !== null && d > MARKET_REACH_TILES) return 'Fuera de alcance';
  const need = buyCost(o, lots).total;
  if ((city.stocks[o.want.resource] ?? 0) < need) return `Falta ${resourceName(o.want.resource)}`;
  return null;
}

/** «20 de piedra por 30 de madera». */
export function lotLabel(o: Pick<MarketOfferView, 'give' | 'want'>): string {
  return `${o.give.amount} de ${resourceName(o.give.resource)} por ${o.want.amount} de ${resourceName(o.want.resource)}`;
}

/** La fila de una oferta propia: lotes vendidos y los que quedan. */
export function ownOfferLabel(o: MarketOfferView): string {
  const vendidos = o.lotsSold === 1 ? '1 vendido' : `${o.lotsSold} vendidos`;
  return `${lotLabel(o)} · ${vendidos}, quedan ${o.lotsLeft} de ${o.lotsTotal}`;
}
