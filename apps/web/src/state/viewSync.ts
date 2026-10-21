/**
 * Cuándo enviar `session.view` (EO-122).
 *
 * El servidor centra el área de interés en la ciudad del jugador al conectar, y
 * sólo la mueve si el cliente le dice dónde mira. Esta clase decide cuándo
 * decírselo: cuando el chunk del centro de la cámara deja de ser el que el
 * servidor conoce, como mucho una vez por segundo, y nunca fuera de una sesión
 * con su snapshot inicial ya servido.
 *
 * Pura: recibe el centro y el reloj monótono local, devuelve el tile que hay que
 * enviar o `null`. Quien envía es la página.
 *
 * Ver ../../../../docs/architecture/frontend.md §7.4.1
 */
/** Coordenada de mundo continua (el centro de la cámara). */
export interface Point {
  readonly x: number;
  readonly y: number;
}

/** Tile entero: lo único que viaja al servidor. */
export interface Tile {
  readonly x: number;
  readonly y: number;
}

/** Separación mínima entre dos `session.view`. */
export const VIEW_MIN_INTERVAL_MS = 1000;

export interface WorldGeometry {
  readonly width: number;
  readonly height: number;
  readonly chunkSize: number;
}

type Phase = 'inactive' | 'awaitingSnapshot' | 'active';

export class ViewSync {
  private phase: Phase = 'inactive';
  private world: WorldGeometry | null = null;
  /** Chunk del último centro que conoce el servidor, como `cx:cy`. */
  private knownChunk: string | null = null;
  private lastSentAt = Number.NEGATIVE_INFINITY;

  /** Empieza una sesión: hasta su snapshot inicial no se envía nada. */
  onWelcome(world: WorldGeometry): void {
    this.world = world;
    this.phase = 'awaitingSnapshot';
    this.knownChunk = null;
    this.lastSentAt = Number.NEGATIVE_INFINITY;
  }

  /**
   * Llegó el snapshot inicial de la sesión, que el servidor centró en
   * `serverCenter` (la ciudad propia, o el centro del mundo si no la hay).
   */
  onInitialSnapshot(serverCenter: Tile): void {
    if (this.phase !== 'awaitingSnapshot' || !this.world) return;
    this.knownChunk = chunkKeyOf(this.toTile(serverCenter), this.world.chunkSize);
    this.phase = 'active';
  }

  /** Se cerró la conexión: nada hasta la siguiente sesión. */
  suspend(): void {
    this.phase = 'inactive';
  }

  /** Si la sesión está esperando su snapshot inicial. */
  isAwaitingSnapshot(): boolean {
    return this.phase === 'awaitingSnapshot';
  }

  /**
   * Se llama en cada frame con el centro de la cámara. Devuelve el tile que hay
   * que enviar en `session.view`, o `null` si no toca.
   */
  update(center: Point | null, nowMs: number): Tile | null {
    if (this.phase !== 'active' || !this.world || !center) return null;
    const tile = this.toTile(center);
    const chunk = chunkKeyOf(tile, this.world.chunkSize);
    if (chunk === this.knownChunk) return null;
    if (nowMs - this.lastSentAt < VIEW_MIN_INTERVAL_MS) return null;
    this.knownChunk = chunk;
    this.lastSentAt = nowMs;
    return tile;
  }

  /** El centro, redondeado hacia abajo a tile entero y recortado al mundo. */
  private toTile(p: Point): Tile {
    const w = this.world!;
    return {
      x: Math.min(Math.max(Math.floor(p.x), 0), w.width - 1),
      y: Math.min(Math.max(Math.floor(p.y), 0), w.height - 1),
    };
  }
}

function chunkKeyOf(tile: Tile, chunkSize: number): string {
  return `${Math.floor(tile.x / chunkSize)}:${Math.floor(tile.y / chunkSize)}`;
}
