/**
 * Cliente WebSocket del juego.
 *
 * Su única responsabilidad es el TRANSPORTE: abrir la conexión, hacer el
 * handshake, mantener el latido, reconectar con backoff y entregar los mensajes
 * validados a quien los consuma.
 *
 * Lo que NO hace, deliberadamente:
 *   - No decide nada sobre el mundo.
 *   - No aplica comandos localmente antes de que el servidor los confirme.
 *   - No infiere posiciones autoritativas.
 *
 * Ver ../../../../docs/architecture/frontend.md y ../../../../docs/specs/websocket-protocol.md
 */
import { v1 } from '@empires-online/protocol';

export type ConnectionState =
  | 'idle'
  | 'connecting'
  | 'authenticating'
  | 'ready'
  | 'reconnecting'
  | 'closed';

export interface GameClientOptions {
  /** URL del endpoint WebSocket, p. ej. ws://localhost:8080/ws */
  readonly url: string;
  /** Función que obtiene un game ticket FRESCO. Cada reconexión necesita uno nuevo. */
  readonly fetchTicket: () => Promise<string>;
  readonly onMessage: (message: v1.ServerMessage) => void;
  readonly onStateChange?: (state: ConnectionState) => void;
  readonly onError?: (code: string, message: string) => void;
  /** Inyectable para los tests. */
  readonly now?: () => number;
  readonly socketFactory?: (url: string) => WebSocketLike;
}

/** Lo mínimo que necesitamos de un WebSocket; permite sustituirlo en tests. */
export interface WebSocketLike {
  send(data: string): void;
  close(code?: number, reason?: string): void;
  onopen: ((ev: unknown) => void) | null;
  onclose: ((ev: { code: number; reason: string }) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: string }) => void) | null;
}

const MAX_BACKOFF_MS = 30_000;
const BASE_BACKOFF_MS = 500;

export class GameClient {
  private socket: WebSocketLike | null = null;
  private state: ConnectionState = 'idle';
  private attempt = 0;
  private lastSeq = 0;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;

  /**
   * Desfase estimado entre el reloj del cliente y el del servidor, en ms.
   * `serverNow ≈ Date.now() + clockOffsetMs`.
   *
   * Existe porque toda la interpolación se calcula contra el reloj del SERVIDOR:
   * si el reloj del navegador va cinco segundos adelantado, las unidades
   * aparecerían cinco segundos por delante de donde realmente están.
   */
  private clockOffsetMs = 0;
  private heartbeatIntervalMs = 10_000;

  constructor(private readonly opts: GameClientOptions) {}

  private readonly now = () => (this.opts.now ?? Date.now)();

  /** Instante actual estimado del SERVIDOR. Es el único reloj válido para el juego. */
  serverTimeMs(): number {
    return this.now() + this.clockOffsetMs;
  }

  getState(): ConnectionState {
    return this.state;
  }

  private setState(next: ConnectionState): void {
    if (this.state === next) return;
    this.state = next;
    this.opts.onStateChange?.(next);
  }

  /** Abre la conexión e inicia el ciclo de vida (incluida la reconexión). */
  async connect(): Promise<void> {
    this.stopped = false;
    await this.openSocket();
  }

  /** Cierra definitivamente: no habrá reconexión. */
  disconnect(): void {
    this.stopped = true;
    this.clearTimers();
    this.socket?.close(1000, 'cierre solicitado por el cliente');
    this.socket = null;
    this.setState('closed');
  }

  private async openSocket(): Promise<void> {
    this.setState(this.attempt === 0 ? 'connecting' : 'reconnecting');

    let ticket: string;
    try {
      // Un ticket es de un solo uso y dura 60 s: SIEMPRE se pide uno nuevo,
      // nunca se reutiliza el de la conexión anterior.
      ticket = await this.opts.fetchTicket();
    } catch (err) {
      this.opts.onError?.('UNAUTHORIZED', 'no se pudo obtener un game ticket');
      this.scheduleReconnect();
      return;
    }

    const factory = this.opts.socketFactory ?? ((url: string) => new WebSocket(url) as unknown as WebSocketLike);
    const socket = factory(this.opts.url);
    this.socket = socket;

    socket.onopen = () => {
      this.setState('authenticating');
      this.send('session.hello', { ticket });
    };

    socket.onmessage = (ev) => this.handleRaw(ev.data);

    socket.onclose = ({ code, reason }) => {
      this.clearTimers();
      this.socket = null;
      if (this.stopped) {
        this.setState('closed');
        return;
      }
      // Un cierre por credenciales no se reintenta en bucle: el jugador debe
      // volver a autenticarse.
      if (code === 4401 || code === 4403) {
        this.opts.onError?.('UNAUTHORIZED', reason || 'sesión no autorizada');
        this.setState('closed');
        return;
      }
      this.scheduleReconnect();
    };

    socket.onerror = () => {
      // `onclose` llega siempre después: la reconexión se gestiona allí, en un
      // único sitio.
    };
  }

  private handleRaw(raw: string): void {
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch {
      this.opts.onError?.('INVALID_MESSAGE', 'el servidor envió JSON malformado');
      return;
    }

    const result = v1.parseServerMessage(parsed);
    if (!result.ok) {
      // Un mensaje que no valida contra el contrato NO se procesa a medias.
      this.opts.onError?.(result.code, result.issues.join('; '));
      return;
    }

    const message = result.message;

    // Ordenación: `seq` es monótono por conexión. Un mensaje con seq menor o
    // igual al último visto llegó fuera de orden o duplicado y se descarta.
    if (message.seq <= this.lastSeq && message.type !== 'session.welcome') {
      return;
    }
    this.lastSeq = message.seq;

    if (message.type === 'session.welcome') {
      // El snapshot posterior reinicia la vista: al reconectar se empieza de cero.
      this.lastSeq = message.seq;
      this.attempt = 0;
      this.clockOffsetMs = message.payload.serverTimeMs - this.now();
      this.heartbeatIntervalMs = message.payload.heartbeatIntervalMs;
      this.startHeartbeat();
      this.setState('ready');
    }

    if (message.type === 'session.pong') {
      // Estimación del desfase corrigiendo la mitad del viaje de ida y vuelta.
      const rtt = this.now() - message.payload.clientTimeMs;
      this.clockOffsetMs = message.payload.serverTimeMs + rtt / 2 - this.now();
    }

    if (message.type === 'system.error') {
      this.opts.onError?.(message.payload.code, message.payload.message);
    }

    this.opts.onMessage(message);
  }

  private startHeartbeat(): void {
    this.clearHeartbeat();
    this.heartbeatTimer = setInterval(() => {
      this.send('session.ping', { clientTimeMs: this.now() });
    }, this.heartbeatIntervalMs);
  }

  private scheduleReconnect(): void {
    if (this.stopped || this.reconnectTimer) return;
    this.setState('reconnecting');

    // Backoff exponencial CON jitter: sin el jitter, mil clientes desconectados
    // por el mismo incidente reconectarían todos en el mismo milisegundo y
    // tumbarían el servidor justo cuando acaba de recuperarse.
    const exponential = Math.min(BASE_BACKOFF_MS * 2 ** this.attempt, MAX_BACKOFF_MS);
    const jitter = Math.random() * exponential * 0.3;
    const delay = exponential + jitter;
    this.attempt += 1;

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      void this.openSocket();
    }, delay);
  }

  private clearHeartbeat(): void {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  private clearTimers(): void {
    this.clearHeartbeat();
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  // ─────────────────────────────────────────────────────────
  // Comandos: INTENCIONES, nunca estado
  // ─────────────────────────────────────────────────────────

  /** Ordena mover una unidad. El servidor decide la ruta, el tiempo y si se puede. */
  moveUnit(unitId: number, target: { x: number; y: number }): string {
    return this.send('unit.move', { unitId, target });
  }

  /** Cancela el movimiento activo de una unidad. */
  cancelMove(unitId: number): string {
    return this.send('unit.cancel_move', { unitId });
  }

  /** Recentra el área de interés (la cámara). */
  setView(center: { x: number; y: number }): string {
    return this.send('session.view', { center });
  }

  private send(type: string, payload: unknown): string {
    const requestId = crypto.randomUUID();
    const message = { v: 1, type, requestId, payload };

    if (!this.socket || (this.state !== 'ready' && type !== 'session.hello')) {
      // No se encolan comandos a ciegas: si la conexión no está lista, el
      // jugador debe volver a darlos cuando lo esté. Reproducir órdenes viejas
      // tras una reconexión produciría movimientos que nadie pidió.
      this.opts.onError?.('INTERNAL_ERROR', 'no hay conexión con el servidor');
      return requestId;
    }

    this.socket.send(JSON.stringify(message));
    return requestId;
  }
}
