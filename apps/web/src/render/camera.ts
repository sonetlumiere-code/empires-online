/**
 * Cámara desplazable (EO-122).
 *
 * Guarda el CENTRO en coordenadas de mundo continuas, no en píxeles: así el
 * recorte a los límites del mundo y el chunk del centro son aritmética de
 * tiles, y la cámara de cada frame se deriva con `worldToIso`.
 *
 * Sin Pixi ni DOM: recibe teclas y arrastres ya traducidos y no dibuja nada.
 *
 * Ver ../../../../docs/architecture/frontend.md §7.4.1
 */
import { isoToWorld, worldToIso, type Camera, type Point, type Tile } from './iso';

/** Velocidad del desplazamiento con teclado, en píxeles de pantalla por segundo a zoom 1. */
export const CAMERA_PAN_PX_PER_S = 900;

/** Direcciones de desplazamiento en PANTALLA: arriba es arriba en la pantalla, no -y del mundo. */
export type PanDirection = 'up' | 'down' | 'left' | 'right';

const KEY_DIRECTIONS: Readonly<Record<string, PanDirection>> = {
  ArrowUp: 'up',
  ArrowDown: 'down',
  ArrowLeft: 'left',
  ArrowRight: 'right',
  w: 'up',
  W: 'up',
  s: 'down',
  S: 'down',
  a: 'left',
  A: 'left',
  d: 'right',
  D: 'right',
};

/** La dirección de desplazamiento que corresponde a una tecla, o `undefined` si no desplaza. */
export function panDirectionOf(key: string): PanDirection | undefined {
  return KEY_DIRECTIONS[key];
}

export interface WorldBounds {
  readonly width: number;
  readonly height: number;
}

export class CameraController {
  private center: Point | null = null;
  private home: Tile | null = null;
  private bounds: WorldBounds | null = null;
  private readonly held = new Set<PanDirection>();

  constructor(private readonly zoom = 1) {}

  /** Si la cámara ya tiene posición. Antes de la primera vez no hay nada que dibujar. */
  isPlaced(): boolean {
    return this.center !== null;
  }

  /** Fija las dimensiones del mundo (del `session.welcome`) y recorta el centro actual. */
  setBounds(bounds: WorldBounds): void {
    this.bounds = bounds;
    if (this.center) this.center = this.clamp(this.center);
  }

  /** Centra la cámara en un tile del mundo. */
  centerOn(tile: Tile): void {
    this.center = this.clamp({ x: tile.x, y: tile.y });
  }

  /**
   * La ciudad propia. Se recuerda aunque después salga del área de interés —un
   * snapshot de otra zona ya no la trae—, y la primera vez coloca la cámara.
   */
  setHome(tile: Tile): void {
    this.home = { x: tile.x, y: tile.y };
    if (!this.center) this.centerOn(this.home);
  }

  /** Vuelve a la ciudad propia (tecla H). Sin ciudad conocida no hace nada. */
  goHome(): void {
    if (this.home) this.centerOn(this.home);
  }

  /** Centro actual en tiles continuos, o `null` si todavía no tiene posición. */
  centerWorld(): Point | null {
    return this.center;
  }

  /** Marca una dirección como pulsada o suelta. */
  setHeld(direction: PanDirection, down: boolean): void {
    if (down) this.held.add(direction);
    else this.held.delete(direction);
  }

  /** Suelta todas las direcciones: al perder el foco la ventana no llega el `keyup`. */
  releaseAll(): void {
    this.held.clear();
  }

  /**
   * Avanza el desplazamiento por teclado `dtMs` milisegundos. La velocidad es
   * en píxeles de pantalla, así que la cámara se mueve igual de deprisa en
   * pantalla en cualquier dirección y a cualquier tasa de frames.
   */
  step(dtMs: number): void {
    if (!this.center || this.held.size === 0 || dtMs <= 0) return;
    let sx = 0;
    let sy = 0;
    if (this.held.has('left')) sx -= 1;
    if (this.held.has('right')) sx += 1;
    if (this.held.has('up')) sy -= 1;
    if (this.held.has('down')) sy += 1;
    if (sx === 0 && sy === 0) return;
    const px = (CAMERA_PAN_PX_PER_S * dtMs) / 1000;
    this.moveByScreen(sx * px, sy * px);
  }

  /**
   * Arrastre: el mapa sigue al cursor, así que la cámara se mueve en sentido
   * contrario a lo que se desplazó el puntero en pantalla.
   */
  dragBy(screenDx: number, screenDy: number): void {
    this.moveByScreen(-screenDx, -screenDy);
  }

  /** La cámara de este frame, en coordenadas isométricas, para un viewport dado. */
  camera(viewportWidth: number, viewportHeight: number): Camera | null {
    if (!this.center) return null;
    const iso = worldToIso(this.center.x, this.center.y);
    return { x: iso.x, y: iso.y, zoom: this.zoom, viewportWidth, viewportHeight };
  }

  /** Desplaza el centro por un vector de PANTALLA (píxeles), convertido a mundo. */
  private moveByScreen(screenDx: number, screenDy: number): void {
    if (!this.center) return;
    // La proyección es lineal: un vector isométrico se lleva al mundo con la
    // misma inversa que un punto.
    const delta = isoToWorld(screenDx / this.zoom, screenDy / this.zoom);
    this.center = this.clamp({ x: this.center.x + delta.x, y: this.center.y + delta.y });
  }

  private clamp(p: Point): Point {
    if (!this.bounds) return p;
    return {
      x: Math.min(Math.max(p.x, 0), this.bounds.width - 1),
      y: Math.min(Math.max(p.y, 0), this.bounds.height - 1),
    };
  }
}
