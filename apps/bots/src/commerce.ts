/**
 * El comercio de los bots (SPEC-BOTS-TRADE §4.1 a §4.4): tratados, tratos y
 * mercado.
 *
 * Puro, como `decide.ts`, que lo llama: recibe lo que el bot ve y su memoria, y
 * añade órdenes a su plan. Lo que se puede proponer, ofrecer, publicar o comprar
 * lo deciden los mismos motivos con que el HUD deshabilita sus botones
 * (`RN-BOT-009`); qué vale cada cosa, la tabla de precios del perfil (Q5).
 */
import type { BuildingView, CityView } from '../../web/src/state/world';
import { proposableTypes, type TreatyView } from '../../web/src/state/diplomacy';
import { canTradeWith, offerBlockReason, pendingWith, type TradeOfferView } from '../../web/src/state/trade';
import { treatyForbidden } from '../../web/src/state/factions';
import {
  MARKET_COST,
  MARKET_MAX_OPEN,
  buyBlockReason,
  buyCost,
  marketBuildBlockReason,
  postBlockReason,
  postCost,
  type MarketOfferView,
} from '../../web/src/state/market';
import { GOODS, foreignGoods, goodOf } from '../../web/src/state/civilizations';
import { firstFreeHouseTile } from '../../web/src/state/economy';
import type { Profile, ProfileName } from './profiles';

// Los tipos de `decide.ts` se importan sólo como tipos: el módulo no depende de él en ejecución.
import type { BotCity, Command, Memory, View } from './decide';

type Stocks = Record<string, number>;

/** Lo que el comercio necesita del plan de un despertar (`Plan` de `decide.ts`). */
export interface Planner {
  readonly full: boolean;
  readonly city: CityView;
  free(key: string): boolean;
  affords(cost: Stocks): boolean;
  add(cmd: Command, cost?: Stocks): void;
}

export interface CommerceOptions {
  /** `--con-humanos`: acepta tratados `TRADE` y tratos de jugadores que no son bots. */
  readonly conHumanos: boolean;
}

// ─────────────────────────────────────────────────────────────
// Quién es quién
// ─────────────────────────────────────────────────────────────

/** Si un nombre de ciudad es de un bot (`bot_*polis`, RN-BOT-008). */
export function isBotName(name: string | null | undefined): boolean {
  return typeof name === 'string' && name.startsWith('bot_');
}

/** El perfil de un bot por el nombre de su ciudad (`bot_<perfil>_<n>polis`), o `null`. */
export function profileOfName(name: string | null | undefined): ProfileName | null {
  if (!isBotName(name)) return null;
  if ((name as string).startsWith('bot_constructor')) return 'constructor';
  if ((name as string).startsWith('bot_saqueador')) return 'saqueador';
  return null;
}

/** Lo que el bot sabe del otro jugador: su entrada del directorio, o el nombre del tratado o la oferta. */
function partnerOf(view: View, playerId: string, name: string | null): { bot: boolean; profile: ProfileName | null; botCity?: BotCity } {
  const botCity = view.botCities.find((b) => b.playerId === playerId);
  const n = botCity?.name ?? name;
  return { bot: isBotName(n), profile: botCity?.profile ?? profileOfName(n), ...(botCity ? { botCity } : {}) };
}

function factionOf(view: View, playerId: string): string | undefined {
  return view.foreignCities.find((c) => c.ownerPlayerId === playerId)?.faction;
}

// ─────────────────────────────────────────────────────────────
// La meta y lo que sobra
// ─────────────────────────────────────────────────────────────

/** La siguiente meta de la ciudad: lo que pide el avance de era siguiente. */
export interface Goal {
  readonly WOOD: number;
  readonly STONE: number;
  /** Bienes de otras civilizaciones, en cualquier mezcla. */
  readonly FOREIGN: number;
}

export function goalOf(era: string): Goal {
  switch (era) {
    case 'STONE_AGE':
      return { WOOD: 400, STONE: 200, FOREIGN: 0 };
    case 'BRONZE_AGE':
      return { WOOD: 800, STONE: 400, FOREIGN: 60 };
    case 'IRON_AGE':
      return { WOOD: 1_600, STONE: 800, FOREIGN: 120 };
    default:
      return { WOOD: 0, STONE: 0, FOREIGN: 0 };
  }
}

/** Bienes ajenos que le faltan para la meta. */
export function foreignNeed(city: CityView): number {
  return Math.max(0, goalOf(city.era).FOREIGN - foreignGoods(city.stocks, city.civilization));
}

/** Bien propio que puede dar sin tocar su reserva. */
export function ownSurplus(city: CityView, profile: Profile): number {
  const good = goodOf(city.civilization);
  if (!good) return 0;
  return Math.max(0, ((city.stocks as unknown as Stocks)[good] ?? 0) - profile.ownGoodReserve);
}

/** Los bienes ajenos, en el orden del catálogo. */
function foreignGoodList(civilization: string): string[] {
  const own = goodOf(civilization);
  return GOODS.filter((g) => g !== own);
}

// ─────────────────────────────────────────────────────────────
// La tabla de precios (Q5, RN-BOT-010)
// ─────────────────────────────────────────────────────────────

type Line = { readonly resource: string; readonly amount: number };

/** El valor de unas líneas según la tabla, y su clase: los bienes no se cambian por madera ni piedra. */
function worth(lines: readonly Line[], profile: Profile): { klass: 'good' | 'raw' | 'mixed'; value: number } {
  let goods = 0;
  let raw = 0;
  let value = 0;
  for (const l of lines) {
    if ((GOODS as readonly string[]).includes(l.resource)) {
      goods++;
      value += l.amount;
    } else {
      raw++;
      value += l.resource === 'STONE' ? l.amount * profile.prices.woodPerStone : l.amount;
    }
  }
  const klass = goods > 0 && raw > 0 ? 'mixed' : goods > 0 ? 'good' : 'raw';
  return { klass, value: klass === 'good' ? value / profile.prices.goodPerGood : value };
}

/**
 * Si el bot acepta un trato (RN-BOT-010): lo que recibe vale, según su tabla, al
 * menos lo que da; los dos lados son de la misma clase (bienes por bienes, madera
 * por piedra: bien por madera o piedra no está en la tabla); y lo que da no le deja
 * por debajo de su meta (madera y piedra, los bienes ajenos que necesita, la
 * reserva de su bien propio).
 */
export function acceptsTrade(offer: Pick<TradeOfferView, 'give' | 'get'>, city: CityView, profile: Profile): boolean {
  const give = worth(offer.give, profile);
  const get = worth(offer.get, profile);
  if (give.klass === 'mixed' || give.klass !== get.klass || get.value < give.value) return false;
  const stocks = city.stocks as unknown as Stocks;
  const goal = goalOf(city.era);
  const own = goodOf(city.civilization);
  let foreign = foreignGoods(city.stocks, city.civilization);
  for (const l of offer.give) {
    const have = stocks[l.resource] ?? 0;
    if (have < l.amount) return false;
    if (l.resource === 'WOOD' || l.resource === 'STONE') {
      if (have - l.amount < goal[l.resource]) return false;
    } else if (l.resource === own) {
      if (have - l.amount < profile.ownGoodReserve) return false;
    } else {
      foreign -= l.amount;
    }
  }
  for (const l of offer.get) if ((GOODS as readonly string[]).includes(l.resource) && l.resource !== own) foreign += l.amount;
  // Puede cambiar bienes ajenos entre sí, pero no quedarse con menos de los que pide su meta.
  return foreign >= Math.min(goal.FOREIGN, foreignGoods(city.stocks, city.civilization));
}

// ─────────────────────────────────────────────────────────────
// Tratados (§4.1)
// ─────────────────────────────────────────────────────────────

/**
 * Responde a las ofertas de tratado recibidas: `TRADE` de bots (y de personas con
 * `--con-humanos`), `NON_AGGRESSION` de otros constructores si es constructor. El
 * saqueador rechaza todo pacto y toda alianza (RN-BOT-011). Lo que no acepta de un
 * bot lo rechaza; lo de una persona sin la opción se deja caducar (RN-BOT-008).
 */
function answerTreaties(view: View, profile: Profile, plan: Planner, opts: CommerceOptions): void {
  for (const t of view.treaties) {
    if (plan.full) return;
    if (t.status !== 'PROPOSED' || t.proposedByMe) continue;
    const key = `treaty:respond:${t.id}`;
    if (!plan.free(key)) continue;
    const other = partnerOf(view, t.otherPlayerId, t.otherCityName);
    let accept: boolean | null = null;
    if (t.type === 'TRADE') {
      accept = other.bot || opts.conHumanos ? true : null;
    } else if (!profile.pact) {
      accept = false;
    } else if (t.type === 'NON_AGGRESSION' && other.profile === 'constructor') {
      accept = !treatyForbidden(t.type, view.city.faction, factionOf(view, t.otherPlayerId));
    } else if (other.bot) {
      accept = false;
    }
    if (accept === null) continue;
    plan.add({ key, type: 'treaty.respond', treatyId: t.id, accept, otherPlayerId: t.otherPlayerId, treatyType: t.type });
  }
}

/**
 * Propone, como mucho uno por despertar, `TRADE` a cada bot sin relación y, si es
 * constructor, `NON_AGGRESSION` a los constructores con los que ya comercia. Lo
 * proponible lo dicen los mismos motivos que el panel de diplomacia.
 */
function proposeTreaty(view: View, profile: Profile, memory: Memory, plan: Planner, nowMs: number): Memory {
  if (plan.full) return memory;
  for (const b of view.botCities) {
    if (b.playerId === view.playerId || !isBotName(b.name)) continue;
    if ((memory.treatyCooldowns[b.playerId] ?? 0) > nowMs) continue;
    const types = proposableTypes(view.treaties, b.playerId);
    const other = partnerOf(view, b.playerId, b.name);
    let type: 'TRADE' | 'NON_AGGRESSION' | null = null;
    if (types.includes('TRADE')) type = 'TRADE';
    else if (profile.pact && other.profile === 'constructor' && types.includes('NON_AGGRESSION')) type = 'NON_AGGRESSION';
    if (type === null || treatyForbidden(type, view.city.faction, factionOf(view, b.playerId))) continue;
    const key = `treaty:propose:${b.playerId}:${type}`;
    if (!plan.free(key)) continue;
    plan.add({ key, type: 'treaty.propose', playerId: b.playerId, treatyType: type });
    return { ...memory, treatyCooldowns: { ...memory.treatyCooldowns, [b.playerId]: nowMs + profile.treatyRetryMs } };
  }
  return memory;
}

// ─────────────────────────────────────────────────────────────
// Tratos (§4.2)
// ─────────────────────────────────────────────────────────────

function answerTrades(view: View, profile: Profile, memory: Memory, plan: Planner, opts: CommerceOptions): void {
  for (const o of view.tradeOffers) {
    if (plan.full) return;
    if (o.status !== 'PROPOSED' || o.proposedByMe) continue;
    const key = `trade:respond:${o.id}`;
    if (!plan.free(key)) continue;
    const other = partnerOf(view, o.otherPlayerId, o.otherCityName);
    if (!other.bot && !opts.conHumanos) continue;
    // Sin ruta, aceptar volvería a fallar: la oferta se deja caducar (RN-BOT-013).
    if (memory.unreachablePlayers.includes(o.otherPlayerId)) continue;
    const accept = acceptsTrade(o, plan.city, profile);
    const cost: Stocks = {};
    if (accept) for (const l of o.give) cost[l.resource] = (cost[l.resource] ?? 0) + l.amount;
    plan.add(
      {
        key,
        type: 'trade.respond',
        offerId: o.id,
        accept,
        otherPlayerId: o.otherPlayerId,
        give: o.give,
        receive: o.get,
      },
      cost,
    );
  }
}

/**
 * Propone, como mucho uno por despertar, un trato a un bot con tratado activo: su
 * bien propio por el del otro, si le faltan bienes ajenos y le sobra el suyo; si
 * no, madera por piedra, si le falta piedra y le sobra madera (Q5).
 */
function proposeTrade(view: View, profile: Profile, memory: Memory, plan: Planner, nowMs: number): Memory {
  if (plan.full) return memory;
  const city = plan.city;
  const stocks = city.stocks as unknown as Stocks;
  const own = goodOf(city.civilization);
  const goal = goalOf(city.era);
  const need = foreignNeed(city);
  const surplus = ownSurplus(city, profile);
  for (const b of view.botCities) {
    if (b.playerId === view.playerId || !isBotName(b.name)) continue;
    if ((memory.tradeCooldowns[b.playerId] ?? 0) > nowMs || memory.unreachablePlayers.includes(b.playerId)) continue;
    if (!canTradeWith(view.treaties, b.playerId) || pendingWith(view.tradeOffers, b.playerId)) continue;
    let draft: { giveResource: string; giveAmount: number; wantResource: string; wantAmount: number } | null = null;
    const theirs = b.civilization ? goodOf(b.civilization) : null;
    if (need > 0 && own && theirs && theirs !== own && surplus > 0) {
      const amount = Math.min(profile.tradeLot, surplus);
      draft = { giveResource: own, giveAmount: amount, wantResource: theirs, wantAmount: amount * profile.prices.goodPerGood };
    } else if (
      (stocks.STONE ?? 0) < goal.STONE &&
      (stocks.WOOD ?? 0) - profile.stoneLot * profile.prices.woodPerStone >= goal.WOOD
    ) {
      draft = {
        giveResource: 'WOOD',
        giveAmount: profile.stoneLot * profile.prices.woodPerStone,
        wantResource: 'STONE',
        wantAmount: profile.stoneLot,
      };
    }
    if (!draft) continue;
    if (offerBlockReason(draft as Parameters<typeof offerBlockReason>[0], city.stocks) !== null) continue;
    const key = `trade:propose:${b.playerId}`;
    if (!plan.free(key)) continue;
    plan.add({
      key,
      type: 'trade.propose',
      playerId: b.playerId,
      give: { resource: draft.giveResource, amount: draft.giveAmount },
      want: { resource: draft.wantResource, amount: draft.wantAmount },
    });
    return { ...memory, tradeCooldowns: { ...memory.tradeCooldowns, [b.playerId]: nowMs + profile.tradeRetryMs } };
  }
  return memory;
}

// ─────────────────────────────────────────────────────────────
// Mercado (§4.3)
// ─────────────────────────────────────────────────────────────

function standing(view: View): BuildingView[] {
  return view.buildings.filter((b) => b.status !== 'DESTROYED');
}

/** Si tiene el mercado completo. */
function hasMarket(view: View): boolean {
  return view.buildings.some((b) => b.type === 'MARKET' && b.status === 'COMPLETE');
}

/** Construye el mercado desde Bronce, con lo que cuesta de sobra respecto a su meta. */
function buildMarket(view: View, plan: Planner): void {
  if (plan.full) return;
  const city = plan.city;
  const built = standing(view);
  if (marketBuildBlockReason(city, built) !== null) return;
  const goal = goalOf(city.era);
  const stocks = city.stocks as unknown as Stocks;
  if ((stocks.WOOD ?? 0) - MARKET_COST.WOOD < goal.WOOD || (stocks.STONE ?? 0) - MARKET_COST.STONE < goal.STONE) return;
  const tile = firstFreeHouseTile(city, built);
  if (!tile) return;
  plan.add({ key: 'build:MARKET', type: 'city.build', cityId: city.id, building: 'MARKET', tile }, { ...MARKET_COST });
}

/**
 * La compra más barata del tablón que paga con su bien y recibe uno ajeno, al
 * precio de la tabla. Las ofertas sin ruta (`unreachable`) no cuentan: si no,
 * la mejor sin ruta taparía para siempre a las demás (RN-BOT-013).
 */
export function bestPurchase(
  board: readonly MarketOfferView[],
  view: Pick<View, 'buildings'>,
  city: CityView,
  profile: Profile,
  unreachable: readonly number[] = [],
): { offer: MarketOfferView; lots: number } | null {
  const own = goodOf(city.civilization);
  const need = foreignNeed(city);
  const surplus = ownSurplus(city, profile);
  if (!own || need <= 0 || surplus <= 0) return null;
  const built = standing(view as View);
  let best: { offer: MarketOfferView; lots: number; ratio: number } | null = null;
  for (const o of board) {
    if (o.want.resource !== own || o.give.resource === own || !(GOODS as readonly string[]).includes(o.give.resource)) continue;
    if (unreachable.includes(o.id)) continue;
    // El precio de la tabla: lo que paga por lote (sin comisión) no pasa de lo que recibe por su precio.
    if (o.want.amount > o.give.amount * profile.prices.goodPerGood) continue;
    const perLot = buyCost(o, 1).total;
    const lots = Math.min(o.lotsLeft, Math.ceil(need / o.give.amount), Math.floor(surplus / perLot));
    if (lots < 1 || buyBlockReason(o, lots, city, built) !== null) continue;
    const ratio = o.want.amount / o.give.amount;
    if (!best || ratio < best.ratio || (ratio === best.ratio && o.id < best.offer.id)) best = { offer: o, lots, ratio };
  }
  return best && { offer: best.offer, lots: best.lots };
}

function useMarket(view: View, profile: Profile, memory: Memory, plan: Planner, nowMs: number): Memory {
  // Las ofertas propias: cuándo se vio cada una por primera vez, para retirar las viejas.
  const seen: Record<number, number> = {};
  for (const o of view.market.own) seen[o.id] = memory.marketSeen[o.id] ?? nowMs;
  let mem: Memory = { ...memory, marketSeen: seen };
  if (!hasMarket(view)) return mem;
  const city = plan.city;
  const own = goodOf(city.civilization);
  const built = standing(view);

  // Retirar: una oferta abierta demasiado tiempo vuelve al almacén y se publica de nuevo.
  for (const o of view.market.own) {
    if (plan.full) return mem;
    if (o.status !== 'OPEN' || nowMs - (seen[o.id] ?? nowMs) <= profile.marketStaleMs) continue;
    const key = `market:cancel:${o.id}`;
    if (plan.free(key)) plan.add({ key, type: 'market.cancel', offerId: o.id });
  }

  // Comprar, con el último tablón pedido.
  if (!plan.full && view.market.board) {
    const p = bestPurchase(view.market.board, view, city, profile, mem.unreachableOffers);
    if (p && own) {
      const key = `market:buy:${p.offer.id}`;
      if (plan.free(key)) {
        plan.add(
          { key, type: 'market.buy', offerId: p.offer.id, lots: p.lots, give: p.offer.want, receive: p.offer.give },
          { [own]: buyCost(p.offer, p.lots).total },
        );
      }
    }
  }

  const need = foreignNeed(plan.city);
  const surplus = ownSurplus(plan.city, profile);
  const goods = foreignGoodList(city.civilization);

  // Consultar el tablón, un bien ajeno cada vez.
  if (!plan.full && need > 0 && nowMs >= mem.marketScanAtMs && goods.length > 0) {
    const receive = goods[mem.marketScanIndex % goods.length] as string;
    plan.add({ key: `market:list:${nowMs}`, type: 'market.list', receive });
    mem = { ...mem, marketScanAtMs: nowMs + profile.marketScanMs, marketScanIndex: mem.marketScanIndex + 1 };
  }

  // Publicar lo que sobra, si no hay un trato propio pendiente que lo use.
  const tradePending = view.tradeOffers.some((o) => o.status === 'PROPOSED' && o.proposedByMe);
  if (!plan.full && own && need > 0 && !tradePending && view.market.own.length < MARKET_MAX_OPEN) {
    const wanted = new Set<string>(view.market.own.map((o) => o.want.resource));
    const want = goods.find((g) => !wanted.has(g));
    const lot = profile.tradeLot;
    const lots = Math.min(profile.marketLots, Math.floor(surplus / postCost(lot, 1).total), Math.ceil(need / lot));
    if (want && lots >= 1) {
      const draft = { giveResource: own, giveAmount: lot, wantResource: want, wantAmount: lot * profile.prices.goodPerGood, lots };
      if (postBlockReason(draft as Parameters<typeof postBlockReason>[0], plan.city.stocks, built, view.market.own.length) === null) {
        const key = `market:post:${want}`;
        if (plan.free(key)) {
          plan.add(
            {
              key,
              type: 'market.post',
              give: { resource: own, amount: lot },
              want: { resource: want, amount: draft.wantAmount },
              lots,
            },
            { [own]: postCost(lot, lots).total },
          );
        }
      }
    }
  }
  return mem;
}

// ─────────────────────────────────────────────────────────────
// La regla
// ─────────────────────────────────────────────────────────────

/** La regla de comercio de un despertar: tratados, tratos y, si es constructor, mercado. */
export function commerceRule(
  view: View,
  profile: Profile,
  memory: Memory,
  plan: Planner,
  nowMs: number,
  opts: CommerceOptions,
): Memory {
  if (!profile.trades) return memory;
  answerTreaties(view, profile, plan, opts);
  answerTrades(view, profile, memory, plan, opts);
  let mem = proposeTreaty(view, profile, memory, plan, nowMs);
  mem = proposeTrade(view, profile, mem, plan, nowMs);
  if (profile.market) {
    buildMarket(view, plan);
    mem = useMarket(view, profile, mem, plan, nowMs);
  }
  return mem;
}

/** Para los tests: los tratados que el espejo trae, con su tipo. */
export type { TreatyView };
