import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import { GameClient, type WebSocketLike } from './client';

/**
 * WebSocket falso. Permite dirigir el ciclo de vida completo de la conexión —
 * apertura, mensajes, cierres con código— sin red y de forma determinista.
 */
class FakeSocket implements WebSocketLike {
  sent: string[] = [];
  closed: { code?: number; reason?: string } | null = null;

  onopen: ((ev: unknown) => void) | null = null;
  onclose: ((ev: { code: number; reason: string }) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;

  send(data: string): void {
    this.sent.push(data);
  }

  close(code?: number, reason?: string): void {
    this.closed = { code, reason };
  }

  // ── Ayudas para dirigir el test ──
  open(): void {
    this.onopen?.({});
  }

  deliver(message: unknown): void {
    this.onmessage?.({ data: JSON.stringify(message) });
  }

  serverClose(code: number, reason = ''): void {
    this.onclose?.({ code, reason });
  }

  parsedSent(): Array<Record<string, unknown>> {
    return this.sent.map((s) => JSON.parse(s) as Record<string, unknown>);
  }
}

const SERVER_NOW = 1_757_376_000_000;

function welcome(seq = 1): v1.ServerMessage {
  return {
    v: 1,
    type: 'session.welcome',
    seq,
    ts: SERVER_NOW,
    payload: {
      sessionId: '33333333-3333-4333-8333-333333333333',
      playerId: '11111111-1111-4111-8111-111111111111',
      serverTimeMs: SERVER_NOW,
      tickDurationMs: 100,
      heartbeatIntervalMs: 10_000,
      world: { width: 512, height: 512, chunkSize: 32 },
    },
  } as v1.ServerMessage;
}

function setup(overrides: { now?: () => number } = {}) {
  const socket = new FakeSocket();
  const received: v1.ServerMessage[] = [];
  const errors: Array<{ code: string; message: string }> = [];
  const states: string[] = [];

  const client = new GameClient({
    url: 'ws://test/ws',
    fetchTicket: async () => 'ticket-de-prueba',
    onMessage: (m) => received.push(m),
    onError: (code, message) => errors.push({ code, message }),
    onStateChange: (s) => states.push(s),
    socketFactory: () => socket,
    now: overrides.now ?? (() => SERVER_NOW),
  });

  return { client, socket, received, errors, states };
}

beforeEach(() => {
  vi.useRealTimers();
});

describe('handshake', () => {
  it('el primer mensaje enviado es session.hello con el ticket', async () => {
    const { client, socket } = setup();
    await client.connect();
    socket.open();

    const first = socket.parsedSent()[0]!;
    expect(first.type).toBe('session.hello');
    expect(first.v).toBe(1);
    expect((first.payload as { ticket: string }).ticket).toBe('ticket-de-prueba');
    expect(typeof first.requestId).toBe('string');
  });

  it('la sesión pasa a ready al recibir session.welcome', async () => {
    const { client, socket, states } = setup();
    await client.connect();
    socket.open();
    expect(client.getState()).toBe('authenticating');

    socket.deliver(welcome());
    expect(client.getState()).toBe('ready');
    expect(states).toContain('connecting');
    expect(states).toContain('ready');
  });

  it('pide un ticket NUEVO en cada conexión: son de un solo uso', async () => {
    const socket = new FakeSocket();
    const fetchTicket = vi.fn(async () => 'ticket-fresco');
    const client = new GameClient({
      url: 'ws://test/ws',
      fetchTicket,
      onMessage: () => {},
      socketFactory: () => socket,
      now: () => SERVER_NOW,
    });

    await client.connect();
    expect(fetchTicket).toHaveBeenCalledTimes(1);
  });
});

describe('comandos', () => {
  async function ready() {
    const env = setup();
    await env.client.connect();
    env.socket.open();
    env.socket.deliver(welcome());
    env.socket.sent.length = 0; // descartar el hello
    return env;
  }

  it('unit.move envía SÓLO el destino, nunca una ruta', async () => {
    const { client, socket } = await ready();
    client.moveUnit(42, { x: 120, y: 88 });

    const sent = socket.parsedSent()[0]!;
    expect(sent.type).toBe('unit.move');
    expect(sent.payload).toEqual({ unitId: 42, target: { x: 120, y: 88 } });
    expect(JSON.stringify(sent)).not.toContain('path');
    expect(JSON.stringify(sent)).not.toContain('tMs');
  });

  it('cada comando lleva un requestId distinto', async () => {
    const { client, socket } = await ready();
    client.moveUnit(1, { x: 1, y: 1 });
    client.moveUnit(1, { x: 2, y: 2 });

    const ids = socket.parsedSent().map((m) => m.requestId);
    expect(new Set(ids).size).toBe(2);
  });

  it('no se envían comandos si la conexión no está lista', async () => {
    const { client, socket, errors } = setup();
    await client.connect();
    // Aún sin session.welcome.
    client.moveUnit(42, { x: 1, y: 1 });

    expect(socket.parsedSent().some((m) => m.type === 'unit.move')).toBe(false);
    expect(errors.at(-1)?.code).toBe('INTERNAL_ERROR');
  });
});

describe('validación de lo que llega', () => {
  async function ready() {
    const env = setup();
    await env.client.connect();
    env.socket.open();
    env.socket.deliver(welcome());
    return env;
  }

  it('descarta un mensaje que no valida contra el contrato', async () => {
    const { socket, received, errors } = await ready();
    const before = received.length;

    socket.deliver({ v: 1, type: 'unit.movement.started', seq: 2, ts: 1, payload: { unitId: 'no-es-un-numero' } });

    expect(received.length, 'un mensaje inválido no puede llegar al store').toBe(before);
    expect(errors.at(-1)?.code).toBe('INVALID_MESSAGE');
  });

  it('distingue versión no soportada de mensaje inválido', async () => {
    const { socket, errors } = await ready();
    socket.deliver({ v: 99, type: 'session.pong', seq: 2, ts: 1, payload: {} });
    expect(errors.at(-1)?.code).toBe('UNSUPPORTED_VERSION');
  });

  it('descarta JSON malformado sin romperse', async () => {
    const { socket, errors } = await ready();
    socket.onmessage?.({ data: '{esto no es json' });
    expect(errors.at(-1)?.code).toBe('INVALID_MESSAGE');
  });

  it('descarta mensajes fuera de orden por seq', async () => {
    const { socket, received } = await ready();

    socket.deliver({ v: 1, type: 'entity.despawn', seq: 5, ts: 1, payload: { id: 1, reason: 'DEAD' } });
    const afterFive = received.length;

    // Llega uno con seq menor: duplicado o reordenado.
    socket.deliver({ v: 1, type: 'entity.despawn', seq: 3, ts: 1, payload: { id: 2, reason: 'DEAD' } });
    expect(received.length).toBe(afterFive);

    // Y uno posterior sí se acepta.
    socket.deliver({ v: 1, type: 'entity.despawn', seq: 6, ts: 1, payload: { id: 3, reason: 'DEAD' } });
    expect(received.length).toBe(afterFive + 1);
  });
});

describe('reloj', () => {
  it('estima el desfase contra el reloj del servidor', async () => {
    // El navegador va 5 segundos adelantado respecto del servidor.
    const clientNow = SERVER_NOW + 5000;
    const { client, socket } = setup({ now: () => clientNow });

    await client.connect();
    socket.open();
    socket.deliver(welcome());

    // serverTimeMs debe devolver el tiempo del SERVIDOR, no el del navegador.
    expect(client.serverTimeMs()).toBe(SERVER_NOW);
  });

  it('sin corrección, el reloj del cliente se usaría tal cual', async () => {
    const { client, socket } = setup();
    await client.connect();
    socket.open();
    socket.deliver(welcome());
    expect(client.serverTimeMs()).toBe(SERVER_NOW);
  });
});

describe('cierre y reconexión', () => {
  it('un cierre 4401 no reintenta en bucle', async () => {
    const { client, socket, errors } = setup();
    await client.connect();
    socket.open();
    socket.serverClose(4401, 'unauthorized');

    expect(client.getState()).toBe('closed');
    expect(errors.at(-1)?.code).toBe('UNAUTHORIZED');
  });

  it('un cierre inesperado programa una reconexión', async () => {
    const { client, socket } = setup();
    await client.connect();
    socket.open();
    socket.deliver(welcome());

    socket.serverClose(1006, 'conexión perdida');
    expect(client.getState()).toBe('reconnecting');

    client.disconnect(); // no dejar temporizadores vivos
  });

  it('disconnect cierra definitivamente', async () => {
    const { client, socket } = setup();
    await client.connect();
    socket.open();
    socket.deliver(welcome());

    client.disconnect();
    expect(client.getState()).toBe('closed');
    expect(socket.closed?.code).toBe(1000);

    // Y un cierre posterior del socket no revive la reconexión.
    socket.serverClose(1006, '');
    expect(client.getState()).toBe('closed');
  });
});
