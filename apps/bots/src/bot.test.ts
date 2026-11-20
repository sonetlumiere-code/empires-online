import { describe, expect, it } from 'vitest';

import type { WebSocketLike } from '../../web/src/net/client';
import { WebSession } from './auth';
import { Bot, Directory } from './bot';
import { RunLog, parseLog } from './log';
import { PROFILES } from './profiles';

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

  constructor() {
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
          civilization: 'ROMAN',
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

  deliver(type: string, payload: unknown): void {
    this.seq += 1;
    this.onmessage?.({ data: JSON.stringify({ v: 1, type, seq: this.seq, ts: NOW, payload }) });
  }
}

function fakeWeb() {
  return async (url: string) => ({
    status: 200,
    headers: { get: (n: string) => (n === 'set-cookie' ? 'eo_session=galleta-secreta; Path=/' : null) },
    json: async () => (url.endsWith('/ticket') ? { ticket: TICKET } : { playerId: PLAYER, ticket: TICKET }),
  });
}

function newBot(lines: string[], sockets: FakeServer[], said: string[] = []): Bot {
  return new Bot({
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
}

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
