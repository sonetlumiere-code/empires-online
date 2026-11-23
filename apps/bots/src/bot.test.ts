import { describe, expect, it } from 'vitest';

import type { WebSocketLike } from '../../web/src/net/client';
import { WebSession } from './auth';
import { Bot, Directory } from './bot';
import { EMPTY_MEMORY, decide, type Command, type Memory } from './decide';
import { botCity, city as fixtureCity, stocks as fixtureStocks, unit, view } from './fixtures.test-helpers';
import { RunLog, parseLog } from './log';
import { PROFILES, type Profile } from './profiles';
import { createRng } from './random';

const PLAYER = '11111111-1111-4111-8111-111111111111';
const NOW = 1_757_376_000_000;
const PASSWORD = 'contraseña-de-bots-123';
const TICKET = 'eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJwMSJ9.ZmlybWE';

/** Un Game Server falso en proceso: responde al hello con welcome y snapshot, y a cada vista con un snapshot. */
class FakeServer implements WebSocketLike {
  sent: Record<string, unknown>[] = [];
  closed: { code?: number } | null = null;
  private seq = 0;
  onopen: ((ev: unknown) => void) | null = null;
  onclose: ((ev: { code: number; reason: string }) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;

  constructor(readonly civilization = 'ROMAN') {
    setTimeout(() => this.onopen?.({}), 0);
  }

  send(data: string): void {
    const m = JSON.parse(data) as { type: string };
    this.sent.push(m);
    if (m.type === 'session.hello') {
      this.deliver('session.welcome', {
        sessionId: '33333333-3333-4333-8333-333333333333',
        playerId: PLAYER,
        serverTimeMs: NOW,
        tickDurationMs: 100,
        heartbeatIntervalMs: 10_000,
        world: { width: 512, height: 512, chunkSize: 32 },
      });
      this.snapshot();
    }
    if (m.type === 'session.view') this.snapshot();
  }

  close(code?: number): void {
    this.closed = { code };
  }

  wood = 0;

  /** Empuja un mensaje del servidor, como si llegara por la red. */
  push(type: string, payload: unknown): void {
    this.deliver(type, payload);
  }

  snapshot(): void {
    this.deliver('world.snapshot', {
      serverTimeMs: NOW,
      tick: 1,
      chunks: [],
      terrain: [],
      units: [],
      cities: [
        {
          id: 10,
          ownerPlayerId: PLAYER,
          name: 'bot_constructor_1polis',
          centerX: 100,
          centerY: 100,
          era: 'STONE_AGE',
          civilization: this.civilization,
          faction: 'NEUTRAL',
          factionJoin: null,
          clan: null,
          population: 3,
          populationLimit: 10,
          presenceState: 'ONLINE',
          protectionUntilMs: null,
          stocks: { WOOD: this.wood, STONE: 0, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 },
        },
      ],
      territories: [],
      buildings: [],
    });
  }

  deliver(type: string, payload: unknown, requestId?: string): void {
    this.seq += 1;
    this.onmessage?.({ data: JSON.stringify({ v: 1, type, seq: this.seq, ts: NOW, ...(requestId ? { requestId } : {}), payload }) });
  }
}

function fakeWeb() {
  return async (url: string) => ({
    status: 200,
    headers: { get: (n: string) => (n === 'set-cookie' ? 'eo_session=galleta-secreta; Path=/' : null) },
    json: async () => (url.endsWith('/ticket') ? { ticket: TICKET } : { playerId: PLAYER, ticket: TICKET }),
  });
}

function newBot(
  lines: string[],
  sockets: FakeServer[],
  said: string[] = [],
  serverCivilization = 'ROMAN',
  profile: Profile = PROFILES.constructor,
): Bot {
  return new Bot({
    name: 'bot_constructor_1',
    profile,
    civilization: 'ROMAN',
    web: 'http://w',
    server: 'ws://s/ws',
    password: PASSWORD,
    thinkMs: 60_000,
    seed: 1,
    contraHumanos: false,
    directory: new Directory(),
    log: new RunLog((l) => lines.push(l), [PASSWORD]),
    say: (t) => said.push(t),
    socketFactory: () => {
      const s = new FakeServer(serverCivilization);
      sockets.push(s);
      return s;
    },
    session: new WebSession('http://w', 'bot_constructor_1', PASSWORD, 'ROMAN', fakeWeb()),
  });
}

describe('la entrada', () => {
  it('dice la civilización de la ciudad, no la que el reparto daría a una cuenta nueva', async () => {
    // Lo encontró la prueba en vivo del 2026-11-20: bot_saqueador_1, romano desde
    // su alta, salía como «NORSE» porque el reparto por turno de esta ejecución
    // le tocaba esa civilización.
    const said: string[] = [];
    const bot = newBot([], [], said, 'NORSE');
    await bot.start();
    bot.stop();
    expect(said[0]).toBe('bot_constructor_1: entrada (constructor, NORSE)');
  });
});

describe('lo recolectado', () => {
  it('cuenta lo que entra por city.update, no el salto que trae un snapshot', async () => {
    const lines: string[] = [];
    const sockets: FakeServer[] = [];
    const bot = newBot(lines, sockets);
    await bot.start();
    const server = sockets[0] as FakeServer;
    const stocks = (wood: number) => ({ WOOD: wood, STONE: 0, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 });
    server.push('city.update', { id: 10, stocks: stocks(15) });
    // Un snapshot con 400 de madera (sembrada con el servidor parado): no es recolección.
    server.wood = 400;
    server.snapshot();
    server.push('city.update', { id: 10, stocks: stocks(410) });
    server.push('city.update', { id: 10, stocks: stocks(360) });
    bot.stop();
    const stop = parseLog(lines.join('')).find((e) => e.event === 'stop');
    expect(stop?.gathered).toEqual({ WOOD: 25, STONE: 0 });
  });
});

describe('lo construido', () => {
  it('un edificio propio que se completa se apunta una vez, aunque el delta no traiga ciudad ni tipo', async () => {
    const lines: string[] = [];
    const sockets: FakeServer[] = [];
    const bot = newBot(lines, sockets);
    await bot.start();
    const server = sockets[0] as FakeServer;
    server.push('building.spawn', {
      building: { id: 5, cityId: 10, type: 'WORKSHOP', x: 99, y: 99, status: 'UNDER_CONSTRUCTION', completesAtMs: NOW + 60_000 },
    });
    server.push('building.update', { id: 5, status: 'COMPLETE' });
    // Un golpe de asedio después: sigue completo, no es otra obra.
    server.push('building.update', { id: 5, status: 'COMPLETE', hp: 300 });
    // Un edificio de otra ciudad que se completa no es nuestro.
    server.push('building.spawn', {
      building: { id: 6, cityId: 99, type: 'HOUSE', x: 10, y: 10, status: 'UNDER_CONSTRUCTION', completesAtMs: NOW + 1 },
    });
    server.push('building.update', { id: 6, status: 'COMPLETE' });
    bot.stop();
    const built = parseLog(lines.join('')).filter((e) => e.event === 'built');
    expect(built).toHaveLength(1);
    expect(built[0]).toMatchObject({ building: 'WORKSHOP' });
  });
});

describe('el registro del comercio (SPEC-BOTS-TRADE §4.7)', () => {
  it('apunta cada cambio de tratado, trato, oferta del mercado y caravana una sola vez', async () => {
    const lines: string[] = [];
    const sockets: FakeServer[] = [];
    const bot = newBot(lines, sockets);
    await bot.start();
    const server = sockets[0] as FakeServer;
    const other = '22222222-2222-4222-8222-222222222222';
    const treaty = (status: string) => ({
      treaty: { id: 7, otherPlayerId: other, otherCityId: 20, otherCityName: 'bot_constructor_2polis', type: 'TRADE', status,
        proposedByMe: true, breaksAtMs: null },
    });
    server.push('treaty.update', treaty('PROPOSED'));
    server.push('treaty.update', treaty('ACTIVE'));
    server.push('treaty.update', treaty('ACTIVE'));
    // El pacto lo sustituye: el acuerdo comercial pasa a EXPIRED (SPEC-TRADE §4.1).
    server.push('treaty.update', treaty('EXPIRED'));
    const trade = (status: string) => ({
      offer: { id: 70, otherPlayerId: other, otherCityId: 20, otherCityName: 'bot_constructor_2polis',
        give: [{ resource: 'WINE', amount: 10 }], get: [{ resource: 'SPICES', amount: 10 }], status, proposedByMe: true,
        expiresAtMs: NOW + 1 },
    });
    server.push('trade.update', trade('ACCEPTED'));
    server.push('trade.update', trade('ACCEPTED'));
    const own = (lotsSold: number, status = 'OPEN') => ({
      offer: { id: 90, playerId: PLAYER, cityId: 10, cityName: 'bot_constructor_1polis', civilization: 'ROMAN', cityX: 100,
        cityY: 100, give: { resource: 'WINE', amount: 10 }, want: { resource: 'SILK', amount: 10 }, lotsTotal: 3,
        lotsLeft: 3 - lotsSold, lotsSold, status, expiresAtMs: NOW + 1 },
    });
    server.push('market.update', own(0));
    server.push('market.update', own(2));
    server.push('market.update', own(2, 'EXPIRED'));
    const caravan = (status: string) => ({
      caravan: { id: 5, unitId: 50, ownerPlayerId: other, senderPlayerId: other, fromCityId: 20,
        fromCityName: 'bot_constructor_2polis', toCityId: 10, toCityName: 'bot_constructor_1polis', originalToCityId: 10,
        cargo: [{ resource: 'SPICES', amount: 10 }], status, arrivesAtMs: null },
    });
    server.push('caravan.update', caravan('IN_TRANSIT'));
    server.push('caravan.update', caravan('DELIVERED'));
    server.push('caravan.update', caravan('DELIVERED'));
    bot.stop();
    const events = parseLog(lines.join('')).filter((e) => ['treaty', 'trade', 'market', 'caravan'].includes(e.event));
    expect(events.map((e) => [e.event, e.status])).toEqual([
      ['treaty', 'accepted'],
      ['treaty', 'replaced'],
      ['trade', 'accepted'],
      ['market', 'sold'],
      ['market', 'expired'],
      ['caravan', 'arrived'],
    ]);
    expect(events[3]).toMatchObject({ lots: 2 });
    expect(events[5]).toMatchObject({ cargo: [{ resource: 'SPICES', amount: 10 }], from: 'bot_constructor_2polis' });
  });
});

/** Lo privado del bot que estos tests tocan: su memoria, el despertar y el envío de una orden. */
type Inner = { memory: Memory; think(): void; send(cmd: Command, nowMs: number): void };

describe('el registro del mercado (SPEC-BOTS-TRADE §4.7)', () => {
  it('apunta posted y bought al llegar el market.update de la orden aceptada, con su comisión', async () => {
    const lines: string[] = [];
    const sockets: FakeServer[] = [];
    const bot = newBot(lines, sockets);
    await bot.start();
    const server = sockets[0] as FakeServer;
    const inner = bot as unknown as Inner;
    const lastRequestId = () => (server.sent[server.sent.length - 1] as { requestId: string }).requestId;
    const offer = (id: number, playerId: string, lotsLeft: number) => ({
      offer: { id, playerId, cityId: 10, cityName: 'bot_constructor_1polis', civilization: 'ROMAN', cityX: 100, cityY: 100,
        give: { resource: 'WINE', amount: 10 }, want: { resource: 'SILK', amount: 10 }, lotsTotal: 3, lotsLeft, lotsSold: 3 - lotsLeft,
        status: 'OPEN', expiresAtMs: NOW + 3_600_000 },
    });

    inner.send(
      { key: 'market:post:SILK', type: 'market.post', give: { resource: 'WINE', amount: 10 }, want: { resource: 'SILK', amount: 10 }, lots: 3 },
      NOW,
    );
    const postId = lastRequestId();
    // Nada se apunta hasta que el servidor confirma con el requestId de la orden.
    expect(parseLog(lines.join('')).filter((e) => e.event === 'market')).toEqual([]);
    server.deliver('market.update', offer(90, PLAYER, 3), postId);

    const seller = '22222222-2222-4222-8222-222222222222';
    inner.send(
      { key: 'market:buy:91', type: 'market.buy', offerId: 91, give: { resource: 'SILK', amount: 8 }, receive: { resource: 'FURS', amount: 10 }, lots: 2 },
      NOW,
    );
    server.deliver('market.update', offer(91, seller, 1), lastRequestId());
    // Repetir el mensaje con el mismo requestId no apunta otra compra: la orden ya no está pendiente.
    server.deliver('market.update', offer(91, seller, 1), lastRequestId());
    bot.stop();

    const market = parseLog(lines.join('')).filter((e) => e.event === 'market');
    expect(market).toHaveLength(2);
    // La comisión es el 10 % de lo que se da por lote, redondeado hacia arriba, por lote.
    expect(market[0]).toMatchObject({ status: 'posted', give: { resource: 'WINE', amount: 10 }, lots: 3, commission: 3 });
    expect(market[1]).toMatchObject({ status: 'bought', give: { resource: 'SILK', amount: 8 }, receive: { resource: 'FURS', amount: 10 },
      lots: 2, commission: 2 });
  });
});

describe('el registro de la reunión (SPEC-BOTS-TRADE §4.7)', () => {
  it('apunta rally una vez, al pasar la partida de la marcha a la orden de saquear', async () => {
    const lines: string[] = [];
    const sockets: FakeServer[] = [];
    const bot = newBot(lines, sockets, [], 'ROMAN', PROFILES.saqueador);
    await bot.start();
    const inner = bot as unknown as Inner;
    const saqueador = PROFILES.saqueador;
    const solo = { contraHumanos: false, comercio: false } as const;
    const base = { city: fixtureCity({ name: 'bot_saqueador_1polis', population: 4, populationLimit: 5, stocks: fixtureStocks(0) }) };

    // La partida sale y todos tienen la orden del punto de reunión (124,100), como en decide.test.ts,
    // medio minuto antes de la hora del bot (la del servidor falso, NOW).
    const out0 = NOW - 30_000;
    const home = view({ ...base, units: [1, 2, 3].map((id) => unit({ id, unitType: 'WARRIOR', hp: 60, maxHp: 60 })), botCities: [botCity()] });
    const out = decide(home, saqueador, EMPTY_MEMORY, out0, createRng(42), solo);
    const walking = view({
      ...base,
      units: [unit({ id: 1, unitType: 'WARRIOR', status: 'MOVING' }), unit({ id: 2, unitType: 'WARRIOR', status: 'MOVING' }),
        unit({ id: 3, unitType: 'WARRIOR' })],
      botCities: [botCity()],
    });
    const march = decide(walking, saqueador, out.memory, out0 + 2_000, createRng(42), solo).memory;
    expect(march.raid).toMatchObject({ phase: 'march', ordered: [1, 2, 3], rallyAtMs: out0 + 2_000 });

    // Un despertar todavía en marcha (uno camina) no apunta nada.
    inner.memory = march;
    const moving = view({
      ...base,
      units: [unit({ id: 1, unitType: 'WARRIOR', x: 124, y: 100 }), unit({ id: 2, unitType: 'WARRIOR', x: 124, y: 101 }),
        unit({ id: 3, unitType: 'WARRIOR', x: 115, y: 100, status: 'MOVING' })],
    });
    bot.buildView = () => moving;
    inner.think();
    expect(parseLog(lines.join('')).filter((e) => e.event === 'rally')).toEqual([]);

    // Reunidos los tres: pasa a saquear y lo apunta.
    const gathered = view({ ...base, units: [1, 2, 3].map((id) => unit({ id, unitType: 'WARRIOR', x: 124, y: 99 + id })) });
    bot.buildView = () => gathered;
    inner.think();
    expect(inner.memory.raid?.phase).toBe('strike');
    // El despertar siguiente, ya en saqueo, no lo repite.
    inner.think();
    bot.stop();

    const rally = parseLog(lines.join('')).filter((e) => e.event === 'rally');
    expect(rally).toHaveLength(1);
    expect(rally[0]).toMatchObject({ bot: 'bot_constructor_1', target: 'bot_constructor_2polis', warriors: 3, alive: 3 });
    // La espera corre desde que el último tuvo la orden del punto de reunión: 28 s y lo que tarde el test.
    expect(rally[0]?.waitMs).toBeGreaterThanOrEqual(28_000);
    expect(rally[0]?.waitMs).toBeLessThan(38_000);
    const commands = parseLog(lines.join('')).filter((e) => e.event === 'command').map((e) => e.key);
    expect(commands.filter((k) => String(k).startsWith('raid:pillage:'))).toEqual(['raid:pillage:1', 'raid:pillage:2', 'raid:pillage:3']);
  });
});

describe('RN-BOT-007: la parada limpia', () => {
  it('al parar cierra las conexiones y escribe stop; nada secreto en el registro', async () => {
    const lines: string[] = [];
    const sockets: FakeServer[] = [];
    const said: string[] = [];
    const bot = new Bot({
      name: 'bot_constructor_1',
      profile: PROFILES.constructor,
      civilization: 'ROMAN',
      web: 'http://w',
      server: 'ws://s/ws',
      password: PASSWORD,
      thinkMs: 60_000,
      seed: 1,
      contraHumanos: false,
      directory: new Directory(),
      log: new RunLog((l) => lines.push(l), [PASSWORD]),
      say: (t) => said.push(t),
      socketFactory: () => {
        const s = new FakeServer();
        sockets.push(s);
        return s;
      },
      session: new WebSession('http://w', 'bot_constructor_1', PASSWORD, 'ROMAN', fakeWeb()),
    });

    await bot.start();
    // La exploración movió la vista y la devolvió a casa.
    const views = sockets[0]?.sent.filter((m) => m.type === 'session.view') ?? [];
    expect(views.length).toBe(25);
    expect(views[views.length - 1]).toMatchObject({ payload: { center: { x: 100, y: 100 } } });

    bot.stop();
    expect(sockets[0]?.closed).toEqual({ code: 1000 });

    const events = parseLog(lines.join(''));
    expect(events.map((e) => e.event)).toEqual(['start', 'stop']);
    expect(events[1]).toMatchObject({ bot: 'bot_constructor_1', era: 'STONE_AGE', population: 3 });
    const all = lines.join('') + said.join('\n');
    expect(all).not.toContain(PASSWORD);
    expect(all).not.toContain(TICKET);
    expect(all).not.toContain('galleta-secreta');

    // Parar dos veces no escribe otro stop.
    bot.stop();
    expect(parseLog(lines.join('')).filter((e) => e.event === 'stop')).toHaveLength(1);
  });
});
