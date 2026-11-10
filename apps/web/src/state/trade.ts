/**
 * Decisiones puras del comercio en el cliente (SPEC-TRADE).
 *
 * Como las de la diplomacia, no deciden nada autoritativo: describen lo que el
 * servidor contó y qué órdenes tiene sentido ofrecer. Si hay tratado, si alcanza
 * el almacén y qué se mueve lo decide el servidor (INV-SEC-001).
 * Ver ../../../../docs/specs/trade.md
 */
import { v1 } from '@empires-online/protocol';

import type { TreatyView } from './diplomacy';

export type TradeOfferView = v1.TradeOfferView;
export type ResourceAmount = v1.ResourceAmount;
export type ResourceType = v1.ResourceType;

/** La cota técnica de una línea, la del protocolo (ADR-032 §7). */
export const TRADE_MAX_AMOUNT = v1.TRADE_MAX_AMOUNT;

/**
 * Aplica un trade.update a la lista: lo añade o lo sustituye si sigue pendiente,
 * y lo quita si se cerró. Devuelve una lista nueva, ordenada por id.
 */
export function applyTradeUpdate(list: readonly TradeOfferView[], o: TradeOfferView): TradeOfferView[] {
  const rest = list.filter((x) => x.id !== o.id);
  if (o.status === 'PROPOSED') rest.push({ ...o, give: [...o.give], get: [...o.get] });
  return rest.sort((a, b) => a.id - b.id);
}

/** Una orden que el HUD ofrece sobre una oferta. */
export type TradeAction = 'accept' | 'reject' | 'withdraw';

/** Qué se puede hacer con una oferta (RN-TRADE-002, RN-TRADE-005). */
export function tradeActions(o: TradeOfferView): TradeAction[] {
  if (o.status !== 'PROPOSED') return [];
  return o.proposedByMe ? ['withdraw'] : ['accept', 'reject'];
}

const RESOURCE_NAME: Record<string, string> = {
  WOOD: 'madera',
  STONE: 'piedra',
  WINE: 'vino',
  SILK: 'seda',
  SPICES: 'especias',
  FURS: 'pieles',
};

/** El nombre de un recurso en el formulario y en las ofertas. */
export function resourceName(r: string): string {
  return RESOURCE_NAME[r] ?? r.toLowerCase();
}

/** Los recursos que se pueden ofrecer y pedir, en el orden del protocolo. */
export const TRADE_RESOURCES: readonly ResourceType[] = v1.ResourceType.options;

/** «300 de madera», «300 de madera y 20 de piedra». */
export function describeLines(lines: readonly ResourceAmount[]): string {
  const parts = lines.map((l) => `${l.amount} de ${RESOURCE_NAME[l.resource] ?? l.resource}`);
  if (parts.length <= 1) return parts.join('');
  return `${parts.slice(0, -1).join(', ')} y ${parts[parts.length - 1]}`;
}

/** El texto de una oferta para el HUD, siempre desde el punto de vista del jugador. */
export function tradeLabel(o: TradeOfferView, serverNowMs: number): string {
  const who = o.otherCityName ?? 'otro jugador';
  const left = formatHours(o.expiresAtMs - serverNowMs);
  return o.proposedByMe
    ? `Ofreces a ${who} ${describeLines(o.give)} por ${describeLines(o.get)} (caduca en ${left})`
    : `${who} te ofrece ${describeLines(o.get)} por ${describeLines(o.give)} (caduca en ${left})`;
}

/** «23 h», «45 min»: lo que queda hasta la caducidad; nunca negativo. */
export function formatHours(ms: number): string {
  const min = Math.max(0, Math.ceil(ms / 60_000));
  if (min < 60) return `${min} min`;
  return `${Math.floor(min / 60)} h`;
}

/**
 * Si un tratado vigente deja comerciar con ese jugador (SPEC-TRADE §4.1): los tres
 * escalones lo hacen, también durante el preaviso. Lo decide el servidor; esto sólo
 * elige qué botones enseñar.
 */
export function canTradeWith(treaties: readonly TreatyView[], otherPlayerId: string): boolean {
  return treaties.some((t) => t.otherPlayerId === otherPlayerId && t.status === 'ACTIVE');
}

/** Si ya hay una oferta pendiente con ese jugador, en cualquier dirección (Q5). */
export function pendingWith(offers: readonly TradeOfferView[], otherPlayerId: string): TradeOfferView | undefined {
  return offers.find((o) => o.otherPlayerId === otherPlayerId && o.status === 'PROPOSED');
}

/**
 * Lo que el formulario arma: doy `give` de un recurso, pido `want` de otro. Desde
 * E14 se elige también el que se pide: con los bienes hay seis (RN-GOODS-008).
 */
export interface TradeDraft {
  giveResource: ResourceType;
  giveAmount: number;
  wantResource: ResourceType;
  wantAmount: number;
}

/** Las dos listas que viajan en trade.propose. */
export function draftLines(d: TradeDraft): { give: ResourceAmount[]; want: ResourceAmount[] } {
  return {
    give: [{ resource: d.giveResource, amount: d.giveAmount }],
    want: [{ resource: d.wantResource, amount: d.wantAmount }],
  };
}

/**
 * Por qué no se puede ofrecer el trato, o `null` si se puede: lo que el servidor
 * rechazaría seguro, para no mandarlo. Cantidades enteras de 1 a la cota (Q3), y lo
 * que se da tiene que estar en el almacén (RN-TRADE-001).
 */
export function offerBlockReason(d: TradeDraft, stocks: Readonly<Partial<Record<string, number>>> | undefined): string | null {
  for (const n of [d.giveAmount, d.wantAmount]) {
    if (!Number.isInteger(n) || n < 1) return 'Las cantidades tienen que ser enteros mayores que 0';
    if (n > TRADE_MAX_AMOUNT) return `Como mucho ${TRADE_MAX_AMOUNT} por línea`;
  }
  // SPEC-TRADE Q4: cada recurso en un solo lado.
  if (d.giveResource === d.wantResource) return 'No se pide lo mismo que se da';
  const have = stocks?.[d.giveResource] ?? 0;
  if (have < d.giveAmount) return `No tienes ${d.giveAmount} de ${resourceName(d.giveResource)}`;
  return null;
}
