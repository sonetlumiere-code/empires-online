import { describe, expect, it } from 'vitest';

import type { MarketOfferView } from '../../web/src/state/market';
import type { TradeOfferView } from '../../web/src/state/trade';
import type { TreatyView } from '../../web/src/state/diplomacy';
import { acceptsTrade, bestPurchase, foreignNeed, goalOf, isBotName, profileOfName } from './commerce';
import { EMPTY_MEMORY, decide, onRejected, type Command, type View } from './decide';
import { HUMAN, ME, OTHER_BOT, botCity, building, city, stocks, view } from './fixtures.test-helpers';
import { PROFILES } from './profiles';
import { createRng } from './random';

// Tests del comercio de los bots (SPEC-BOTS-TRADE §4.1 a §4.4, RN-BOT-008 a
// RN-BOT-011). Las ciudades de los fixtures: la propia, romana (vino), en Bronce;
// la del otro bot, `bot_constructor_2polis`, persa (especias).

const NOW = 1_757_376_000_000;
const constructor = PROFILES.constructor;
const saqueador = PROFILES.saqueador;
const rng = () => createRng(42);
const OTHER_SAQ = '44444444-4444-4444-8444-444444444444';

const persa = botCity({ civilization: 'PERSIAN', profile: 'constructor' });
const saq = botCity({ playerId: OTHER_SAQ, cityId: 30, name: 'bot_saqueador_1polis', civilization: 'NORSE', profile: 'saqueador' });

function treaty(over: Partial<TreatyView> = {}): TreatyView {
  return {
    id: 7,
    otherPlayerId: OTHER_BOT,
    otherCityId: 20,
    otherCityName: 'bot_constructor_2polis',
    type: 'TRADE',
    status: 'ACTIVE',
    proposedByMe: false,
    breaksAtMs: null,
    ...over,
  };
}

function offer(over: Partial<TradeOfferView> = {}): TradeOfferView {
  return {
    id: 70,
    otherPlayerId: OTHER_BOT,
    otherCityId: 20,
    otherCityName: 'bot_constructor_2polis',
    give: [{ resource: 'WINE', amount: 10 }],
    get: [{ resource: 'SPICES', amount: 10 }],
    status: 'PROPOSED',
    proposedByMe: false,
    expiresAtMs: NOW + 3_600_000,
    ...over,
  };
}

function lot(over: Partial<MarketOfferView> = {}): MarketOfferView {
  return {
    id: 90,
    playerId: OTHER_BOT,
    cityId: 20,
    cityName: 'bot_constructor_2polis',
    civilization: 'PERSIAN',
    cityX: 140,
    cityY: 100,
    give: { resource: 'SPICES', amount: 10 },
    want: { resource: 'WINE', amount: 10 },
    lotsTotal: 5,
    lotsLeft: 5,
    lotsSold: 0,
    status: 'OPEN',
    expiresAtMs: NOW + 3_600_000,
    ...over,
  };
}

/** Una ciudad en Bronce sin otra cosa que hacer: taller y muralla hechos, investigando y elaborando. */
function busy(over: Partial<ReturnType<typeof city>> = {}): ReturnType<typeof city> {
  return city({
    era: 'BRONZE_AGE',
    population: 12,
    populationLimit: 20,
    research: { technology: 'STONE_TOOLS', completesAtMs: NOW + 60_000 },
    craft: { good: 'WINE', amount: 10, completesAtMs: NOW + 60_000 },
    ...over,
  });
}
const done = [building({ id: 61, type: 'WORKSHOP', x: 99, y: 99 }), building({ id: 62, type: 'WALL', x: 100, y: 100 })];

/** En Bronce, con vino propio de sobra, sin bienes ajenos y nada más que hacer. */
function bronze(over: Partial<View> = {}, wine = 50): View {
  return view({
    city: busy({ stocks: stocks(0, 0, { WINE: wine }) }),
    buildings: done,
    botCities: [persa],
    ...over,
  });
}

const of = (cs: readonly Command[], type: Command['type']) => cs.filter((c) => c.type === type);

describe('quién es un bot', () => {
  it('por el nombre de su ciudad, y su perfil por el prefijo', () => {
    expect(isBotName('bot_constructor_2polis')).toBe(true);
    expect(isBotName('Roma')).toBe(false);
    expect(isBotName(null)).toBe(false);
    expect(profileOfName('bot_constructor_2polis')).toBe('constructor');
    expect(profileOfName('bot_saqueador_1polis')).toBe('saqueador');
    expect(profileOfName('Roma')).toBeNull();
  });

  it('la meta es el avance siguiente, con sus bienes ajenos', () => {
    expect(goalOf('BRONZE_AGE')).toEqual({ WOOD: 800, STONE: 400, FOREIGN: 60 });
    expect(foreignNeed(city({ era: 'BRONZE_AGE', stocks: stocks(0, 0, { WINE: 99, SPICES: 25 }) }))).toBe(35);
  });
});

describe('tratados (§4.1)', () => {
  it('propone TRADE a un bot sin relación, uno por despertar, y no repite antes del enfriamiento', () => {
    const v = bronze({ botCities: [persa, saq] });
    const { commands, memory } = decide(v, constructor, EMPTY_MEMORY, NOW, rng());
    expect(of(commands, 'treaty.propose')).toEqual([
      { key: `treaty:propose:${OTHER_BOT}:TRADE`, type: 'treaty.propose', playerId: OTHER_BOT, treatyType: 'TRADE' },
    ]);
    expect(memory.treatyCooldowns[OTHER_BOT]).toBe(NOW + constructor.treatyRetryMs);
    // El despertar siguiente, al otro bot; nunca a sí mismo.
    const next = decide(v, constructor, memory, NOW + 2_000, rng());
    expect(of(next.commands, 'treaty.propose')).toEqual([
      expect.objectContaining({ playerId: OTHER_SAQ, treatyType: 'TRADE' }),
    ]);
  });

  it('un constructor sube a NON_AGGRESSION con otro constructor con el que comercia; con el saqueador, no', () => {
    const v = bronze({
      botCities: [persa, saq],
      treaties: [treaty(), treaty({ id: 8, otherPlayerId: OTHER_SAQ, otherCityName: 'bot_saqueador_1polis' })],
    });
    const { commands } = decide(v, constructor, EMPTY_MEMORY, NOW, rng());
    expect(of(commands, 'treaty.propose')).toEqual([
      expect.objectContaining({ playerId: OTHER_BOT, treatyType: 'NON_AGGRESSION' }),
    ]);
    const other = decide(v, constructor, { ...EMPTY_MEMORY, treatyCooldowns: { [OTHER_BOT]: NOW + 1 } }, NOW, rng());
    expect(of(other.commands, 'treaty.propose')).toEqual([]);
  });

  it('RN-BOT-011: el saqueador nunca propone pacto y rechaza el que le llega', () => {
    const v = bronze({
      city: busy({ name: 'bot_saqueador_1polis', population: 6, stocks: stocks(0) }),
      treaties: [treaty({ status: 'ACTIVE' }), treaty({ id: 9, type: 'NON_AGGRESSION', status: 'PROPOSED' })],
    });
    const { commands } = decide(v, saqueador, EMPTY_MEMORY, NOW, rng());
    expect(of(commands, 'treaty.propose')).toEqual([]);
    expect(of(commands, 'treaty.respond')).toEqual([
      expect.objectContaining({ treatyId: 9, accept: false, treatyType: 'NON_AGGRESSION' }),
    ]);
  });

  it('acepta TRADE de un bot y NON_AGGRESSION de un constructor; rechaza la alianza de un bot', () => {
    const v = bronze({
      treaties: [
        treaty({ id: 1, status: 'PROPOSED' }),
        treaty({ id: 2, status: 'PROPOSED', type: 'ALLIANCE', otherPlayerId: OTHER_SAQ, otherCityName: 'bot_saqueador_1polis' }),
      ],
      botCities: [persa, saq],
    });
    const { commands } = decide(v, constructor, EMPTY_MEMORY, NOW, rng());
    expect(of(commands, 'treaty.respond')).toEqual([
      expect.objectContaining({ treatyId: 1, accept: true }),
      expect.objectContaining({ treatyId: 2, accept: false }),
    ]);
    const pact = bronze({ treaties: [treaty({ id: 3, status: 'PROPOSED', type: 'NON_AGGRESSION' })] });
    expect(of(decide(pact, constructor, EMPTY_MEMORY, NOW, rng()).commands, 'treaty.respond')).toEqual([
      expect.objectContaining({ treatyId: 3, accept: true }),
    ]);
  });

  it('RN-BOT-008: la oferta de una persona se deja caducar; con --con-humanos acepta su TRADE', () => {
    const human = treaty({ id: 4, status: 'PROPOSED', otherPlayerId: HUMAN, otherCityName: 'Roma' });
    const v = bronze({ treaties: [human], botCities: [] });
    expect(of(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands, 'treaty.respond')).toEqual([]);
    const yes = decide(v, constructor, EMPTY_MEMORY, NOW, rng(), { contraHumanos: false, conHumanos: true });
    expect(of(yes.commands, 'treaty.respond')).toEqual([expect.objectContaining({ treatyId: 4, accept: true })]);
    // Su pacto, ni con la opción.
    const pact = bronze({ treaties: [{ ...human, type: 'NON_AGGRESSION' }], botCities: [] });
    const none = decide(pact, constructor, EMPTY_MEMORY, NOW, rng(), { contraHumanos: false, conHumanos: true });
    expect(of(none.commands, 'treaty.respond')).toEqual([]);
  });

  it('RN-BOT-009: no propone lo que el panel deshabilita (oferta pendiente, preaviso, facciones enfrentadas)', () => {
    const pending = bronze({ treaties: [treaty({ status: 'PROPOSED', proposedByMe: true })] });
    expect(of(decide(pending, constructor, EMPTY_MEMORY, NOW, rng()).commands, 'treaty.propose')).toEqual([]);
    const breaking = bronze({ treaties: [treaty({ breaksAtMs: NOW + 60_000 })] });
    expect(of(decide(breaking, constructor, EMPTY_MEMORY, NOW, rng()).commands, 'treaty.propose')).toEqual([]);
    const enemies = bronze({
      city: busy({ faction: 'ORDER', stocks: stocks(0) }),
      treaties: [treaty()],
      foreignCities: [city({ id: 20, ownerPlayerId: OTHER_BOT, name: 'bot_constructor_2polis', faction: 'CHAOS' })],
    });
    expect(of(decide(enemies, constructor, EMPTY_MEMORY, NOW, rng()).commands, 'treaty.propose')).toEqual([]);
  });

  it('un rechazo enfría al jugador', () => {
    const cmd: Command = { key: 'treaty:propose:x:TRADE', type: 'treaty.propose', playerId: OTHER_BOT, treatyType: 'TRADE' };
    expect(onRejected(EMPTY_MEMORY, cmd, 'INVALID_TARGET', NOW, constructor).treatyCooldowns[OTHER_BOT]).toBe(
      NOW + constructor.treatyRetryMs,
    );
  });
});

describe('tratos (§4.2)', () => {
  it('con tratado, le faltan bienes ajenos y le sobra el suyo: su bien por el del otro, 1 a 1', () => {
    const v = bronze({ treaties: [treaty()] });
    const { commands, memory } = decide(v, constructor, EMPTY_MEMORY, NOW, rng());
    expect(of(commands, 'trade.propose')).toEqual([
      {
        key: `trade:propose:${OTHER_BOT}`,
        type: 'trade.propose',
        playerId: OTHER_BOT,
        give: { resource: 'WINE', amount: 10 },
        want: { resource: 'SPICES', amount: 10 },
      },
    ]);
    expect(memory.tradeCooldowns[OTHER_BOT]).toBe(NOW + constructor.tradeRetryMs);
  });

  it('sin tratado, con una oferta pendiente o en enfriamiento, no propone', () => {
    expect(of(decide(bronze(), constructor, EMPTY_MEMORY, NOW, rng()).commands, 'trade.propose')).toEqual([]);
    const pending = bronze({ treaties: [treaty()], tradeOffers: [offer({ proposedByMe: true })] });
    expect(of(decide(pending, constructor, EMPTY_MEMORY, NOW, rng()).commands, 'trade.propose')).toEqual([]);
    const cool = { ...EMPTY_MEMORY, tradeCooldowns: { [OTHER_BOT]: NOW + 1 } };
    expect(of(decide(bronze({ treaties: [treaty()] }), constructor, cool, NOW, rng()).commands, 'trade.propose')).toEqual([]);
  });

  it('con piedra por debajo de la meta y madera de sobra: madera por piedra, 2 a 1', () => {
    const v = bronze({
      city: busy({ stocks: stocks(900, 100) }),
      treaties: [treaty()],
    });
    expect(of(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands, 'trade.propose')).toEqual([
      expect.objectContaining({ give: { resource: 'WOOD', amount: 40 }, want: { resource: 'STONE', amount: 20 } }),
    ]);
  });

  it('RN-BOT-010: acepta si recibe lo que vale lo que da y no baja de su meta', () => {
    const c = city({ era: 'BRONZE_AGE', stocks: stocks(900, 500, { WINE: 40, FURS: 5 }) });
    // Las líneas, desde el punto de vista de quien recibe: lo que daría y lo que recibiría.
    expect(acceptsTrade(offer(), c, constructor)).toBe(true);
    expect(acceptsTrade(offer({ get: [{ resource: 'SPICES', amount: 9 }] }), c, constructor)).toBe(false);
    expect(acceptsTrade(offer({ give: [{ resource: 'WINE', amount: 50 }], get: [{ resource: 'SPICES', amount: 50 }] }), c, constructor)).toBe(false);
    // Madera por piedra a 2 a 1, con piedra de sobra respecto a su meta.
    expect(acceptsTrade(offer({ give: [{ resource: 'STONE', amount: 20 }], get: [{ resource: 'WOOD', amount: 40 }] }), c, constructor)).toBe(true);
    expect(acceptsTrade(offer({ give: [{ resource: 'STONE', amount: 20 }], get: [{ resource: 'WOOD', amount: 39 }] }), c, constructor)).toBe(false);
    const short = city({ era: 'BRONZE_AGE', stocks: stocks(900, 410) });
    expect(acceptsTrade(offer({ give: [{ resource: 'STONE', amount: 20 }], get: [{ resource: 'WOOD', amount: 40 }] }), short, constructor)).toBe(false);
    // Un bien por madera no está en la tabla.
    expect(acceptsTrade(offer({ get: [{ resource: 'WOOD', amount: 100 }] }), c, constructor)).toBe(false);
    // No da los bienes ajenos que su meta pide.
    expect(acceptsTrade(offer({ give: [{ resource: 'FURS', amount: 5 }], get: [{ resource: 'WINE', amount: 5 }] }), c, constructor)).toBe(false);
    // El saqueador guarda su reserva para la unidad de civilización.
    const s = city({ era: 'BRONZE_AGE', civilization: 'ROMAN', stocks: stocks(0, 0, { WINE: 35 }) });
    expect(acceptsTrade(offer(), s, saqueador)).toBe(false);
  });

  it('responde a las ofertas de bots; las de personas, sólo con --con-humanos', () => {
    const v = bronze({ tradeOffers: [offer(), offer({ id: 71, otherPlayerId: HUMAN, otherCityName: 'Roma' })] });
    const { commands } = decide(v, constructor, EMPTY_MEMORY, NOW, rng());
    expect(of(commands, 'trade.respond')).toEqual([
      {
        key: 'trade:respond:70',
        type: 'trade.respond',
        offerId: 70,
        accept: true,
        otherPlayerId: OTHER_BOT,
        give: [{ resource: 'WINE', amount: 10 }],
        receive: [{ resource: 'SPICES', amount: 10 }],
      },
    ]);
    const all = decide(v, constructor, EMPTY_MEMORY, NOW, rng(), { contraHumanos: false, conHumanos: true });
    expect(of(all.commands, 'trade.respond').map((c) => (c as { offerId: number }).offerId)).toEqual([70, 71]);
  });

  it('sin ruta entre depósitos, la pareja se aparta diez minutos', () => {
    const cmd: Command = {
      key: 'trade:propose:x',
      type: 'trade.propose',
      playerId: OTHER_BOT,
      give: { resource: 'WINE', amount: 10 },
      want: { resource: 'SPICES', amount: 10 },
    };
    expect(onRejected(EMPTY_MEMORY, cmd, 'PATH_TOO_LONG', NOW, constructor).tradeCooldowns[OTHER_BOT]).toBe(
      NOW + constructor.targetCooldownMs,
    );
  });

  it('--sin-comercio: ni tratados ni tratos', () => {
    const v = bronze({ treaties: [treaty(), treaty({ id: 2, status: 'PROPOSED' })], botCities: [persa, saq] });
    const { commands } = decide(v, constructor, EMPTY_MEMORY, NOW, rng(), { contraHumanos: false, comercio: false });
    expect(commands.filter((c) => c.type.startsWith('treaty.') || c.type.startsWith('trade.'))).toEqual([]);
  });
});

describe('mercado (§4.3)', () => {
  const market = building({ id: 60, type: 'MARKET', x: 101, y: 99 });
  const withMarket = (over: Partial<View> = {}, wine = 50) => bronze({ buildings: [...done, market], botCities: [], ...over }, wine);

  it('el constructor lo construye en Bronce con lo que cuesta de sobra respecto a su meta; el saqueador, nunca', () => {
    const rich = bronze({ city: busy({ stocks: stocks(950, 500) }), botCities: [] });
    expect(of(decide(rich, constructor, EMPTY_MEMORY, NOW, rng()).commands, 'city.build')).toEqual([
      expect.objectContaining({ key: 'build:MARKET', building: 'MARKET' }),
    ]);
    const tight = bronze({ city: busy({ stocks: stocks(900, 450) }), botCities: [] });
    expect(of(decide(tight, constructor, EMPTY_MEMORY, NOW, rng()).commands, 'city.build')).toEqual([]);
    const saqRich = { ...rich, city: { ...rich.city, name: 'bot_saqueador_1polis', population: 6 } };
    expect(of(decide(saqRich, saqueador, EMPTY_MEMORY, NOW, rng()).commands, 'city.build').filter((c) => (c as { building: string }).building === 'MARKET')).toEqual([]);
  });

  it('consulta el tablón cada minuto, un bien ajeno cada vez', () => {
    const { commands, memory } = decide(withMarket(), constructor, EMPTY_MEMORY, NOW, rng());
    expect(of(commands, 'market.list')).toEqual([{ key: `market:list:${NOW}`, type: 'market.list', receive: 'SILK' }]);
    expect(memory).toMatchObject({ marketScanAtMs: NOW + constructor.marketScanMs, marketScanIndex: 1 });
    expect(of(decide(withMarket(), constructor, memory, NOW + 1_000, rng()).commands, 'market.list')).toEqual([]);
  });

  it('compra la oferta más barata que paga con su bien, al precio de la tabla', () => {
    const board = [
      lot({ id: 91, want: { resource: 'WINE', amount: 12 } }), // más cara que la tabla
      lot({ id: 92, give: { resource: 'SPICES', amount: 10 }, want: { resource: 'WINE', amount: 10 } }),
      lot({ id: 93, give: { resource: 'FURS', amount: 10 }, want: { resource: 'WINE', amount: 8 } }),
      lot({ id: 94, give: { resource: 'FURS', amount: 10 }, want: { resource: 'WOOD', amount: 5 } }), // no paga con su bien
      lot({ id: 95, playerId: ME, give: { resource: 'SILK', amount: 10 }, want: { resource: 'WINE', amount: 1 } }), // suya
    ];
    const v = withMarket({ market: { own: [], board } });
    const p = bestPurchase(board, v, v.city, constructor);
    expect(p?.offer.id).toBe(93);
    // 50 de vino: 8 + 1 de comisión por lote → 5 lotes, y le faltan 60 bienes → 5.
    expect(p?.lots).toBe(5);
    const { commands } = decide(v, constructor, { ...EMPTY_MEMORY, marketScanAtMs: NOW + 1 }, NOW, rng());
    expect(of(commands, 'market.buy')).toEqual([
      expect.objectContaining({ key: 'market:buy:93', offerId: 93, lots: 5 }),
    ]);
  });

  it('publica lo que le sobra a cambio de un bien ajeno, sin pasar de tres ofertas', () => {
    const { commands } = decide(withMarket({}, 40), constructor, { ...EMPTY_MEMORY, marketScanAtMs: NOW + 1 }, NOW, rng());
    // 40 de vino: lote de 10 más 1 de comisión → 3 lotes.
    expect(of(commands, 'market.post')).toEqual([
      {
        key: 'market:post:SILK',
        type: 'market.post',
        give: { resource: 'WINE', amount: 10 },
        want: { resource: 'SILK', amount: 10 },
        lots: 3,
      },
    ]);
    const three = [1, 2, 3].map((id) => lot({ id, playerId: ME, give: { resource: 'WINE', amount: 10 }, want: { resource: 'SILK', amount: 10 } }));
    const full = withMarket({ market: { own: three, board: null } });
    expect(of(decide(full, constructor, { ...EMPTY_MEMORY, marketScanAtMs: NOW + 1 }, NOW, rng()).commands, 'market.post')).toEqual([]);
  });

  it('retira la oferta propia que lleva abierta más de media hora', () => {
    const own = lot({ id: 99, playerId: ME, give: { resource: 'WINE', amount: 10 }, want: { resource: 'SILK', amount: 10 } });
    const v = withMarket({ market: { own: [own], board: null } }, 0);
    const first = decide(v, constructor, EMPTY_MEMORY, NOW, rng());
    expect(first.memory.marketSeen[99]).toBe(NOW);
    expect(of(first.commands, 'market.cancel')).toEqual([]);
    const later = decide(v, constructor, first.memory, NOW + constructor.marketStaleMs + 1, rng());
    expect(of(later.commands, 'market.cancel')).toEqual([{ key: 'market:cancel:99', type: 'market.cancel', offerId: 99 }]);
  });

  it('el constructor deja libre el hueco del mercado al hacer casas', () => {
    // Siete edificios en pie (el taller entre ellos) y la población al tope: el octavo hueco es del mercado.
    const seven = [
      building({ id: 1, type: 'WORKSHOP', x: 99, y: 99 }),
      ...[2, 3, 4, 5, 6, 7].map((id) => building({ id, x: 99 + (id % 3), y: 100 + Math.floor(id / 3) })),
    ];
    const v = view({
      city: city({ era: 'BRONZE_AGE', population: 12, populationLimit: 12, stocks: stocks(500) }),
      buildings: seven,
    });
    expect(of(decide(v, constructor, EMPTY_MEMORY, NOW, rng()).commands, 'city.build')).toEqual([]);
  });
});
