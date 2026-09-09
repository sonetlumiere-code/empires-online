/**
 * Transformaciones de coordenadas del cliente.
 *
 * Regla no negociable: las coordenadas de MUNDO son la única verdad; las de
 * pantalla son un detalle de presentación que se recalcula cada frame y JAMÁS se
 * envía al servidor ni se guarda como estado.
 *
 *   world (tiles)  →  isométrico  →  pantalla (píxeles)
 *
 * Ver ../../../../docs/architecture/frontend.md
 */

/** Ancho de un tile en la proyección isométrica, en píxeles. */
export const TILE_W = 64;
/** Alto de un tile en la proyección isométrica, en píxeles. */
export const TILE_H = 32;

export interface Tile {
  readonly x: number;
  readonly y: number;
}

export interface Point {
  readonly x: number;
  readonly y: number;
}

/**
 * Proyecta una coordenada de mundo (puede ser fraccionaria, para interpolación)
 * al plano isométrico.
 *
 *   screenX = (x - y) * TILE_W/2
 *   screenY = (x + y) * TILE_H/2
 */
export function worldToIso(x: number, y: number): Point {
  return {
    x: (x - y) * (TILE_W / 2),
    y: (x + y) * (TILE_H / 2),
  };
}

/**
 * Inversa exacta de `worldToIso`. Es lo que permite el picking: traducir dónde
 * hizo clic el jugador a qué tile del mundo quiere señalar.
 *
 * Despejando el sistema:
 *   x = isoY/TILE_H + isoX/TILE_W
 *   y = isoY/TILE_H - isoX/TILE_W
 */
export function isoToWorld(isoX: number, isoY: number): Point {
  return {
    x: isoY / TILE_H + isoX / TILE_W,
    y: isoY / TILE_H - isoX / TILE_W,
  };
}

/** Devuelve el tile que contiene un punto isométrico, redondeando al entero. */
export function isoToTile(isoX: number, isoY: number): Tile {
  const world = isoToWorld(isoX, isoY);
  return { x: Math.floor(world.x + 0.5), y: Math.floor(world.y + 0.5) };
}

/**
 * Orden de dibujado en una escena isométrica.
 *
 * Lo que está "más al fondo" tiene menor `x + y`. Ordenar por esa suma hace que
 * una unidad delante de un edificio se dibuje encima de él, que es lo que el ojo
 * espera. Sin esto, la escena se ve rota aunque las posiciones sean correctas.
 */
export function depthOf(x: number, y: number): number {
  return x + y;
}

/** Convierte una coordenada de mundo a píxeles de pantalla aplicando la cámara. */
export function worldToScreen(x: number, y: number, camera: Camera): Point {
  const iso = worldToIso(x, y);
  return {
    x: (iso.x - camera.x) * camera.zoom + camera.viewportWidth / 2,
    y: (iso.y - camera.y) * camera.zoom + camera.viewportHeight / 2,
  };
}

/** Convierte un punto de pantalla al tile de mundo correspondiente. */
export function screenToTile(screenX: number, screenY: number, camera: Camera): Tile {
  const isoX = (screenX - camera.viewportWidth / 2) / camera.zoom + camera.x;
  const isoY = (screenY - camera.viewportHeight / 2) / camera.zoom + camera.y;
  return isoToTile(isoX, isoY);
}

export interface Camera {
  /** Centro de la cámara en coordenadas isométricas. */
  readonly x: number;
  readonly y: number;
  readonly zoom: number;
  readonly viewportWidth: number;
  readonly viewportHeight: number;
}

/** Crea una cámara centrada en un tile del mundo. */
export function cameraAt(tile: Tile, viewportWidth: number, viewportHeight: number, zoom = 1): Camera {
  const iso = worldToIso(tile.x, tile.y);
  return { x: iso.x, y: iso.y, zoom, viewportWidth, viewportHeight };
}
