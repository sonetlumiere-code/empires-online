/**
 * Qué parte del mundo se ve: el rectángulo de tiles que cubre el viewport.
 *
 * Puro, sin Pixi ni DOM. La escena lo recalcula en cada frame —son cuatro
 * proyecciones— pero sólo actúa cuando el resultado CAMBIA, que es lo que hace
 * que recorrer el terreno y decidir qué entidades se dibujan cueste algo al
 * mover la cámara y nada mientras está quieta.
 *
 * Ver ../../../../docs/architecture/frontend.md §7.4
 */
import { isoToWorld, type Camera } from './iso';

/**
 * Rectángulo cerrado `minX..maxX` × `minY..maxY`. En tiles enteros del mundo
 * (`visibleWorldRect`, `chunkRectOf`) o en píxeles isométricos de la escena
 * (`visibleIsoRect`), según la función que lo produzca.
 */
export interface Rect {
  readonly minX: number;
  readonly minY: number;
  readonly maxX: number;
  readonly maxY: number;
}

/**
 * Los tiles cuyo rombo toca el viewport de una cámara.
 *
 * El viewport es un rectángulo en isométrico y un rombo en el mundo; lo que se
 * devuelve es su caja envolvente en tiles, y el rombo ocupa exactamente la
 * mitad de esa caja. Sirve para decidir qué CHUNKS de terreno hacen falta, que
 * son gruesos y se recorren de uno en uno; para las entidades, que son muchas,
 * `visibleIsoRect` recorta en píxeles de escena sin esa holgura.
 *
 * Un tile `x` ocupa el intervalo de mundo `[x − ½, x + ½]`, porque su rombo se
 * dibuja centrado en `worldToIso(x, y)`: por eso el borde se redondea con medio
 * tile y no con `floor`/`ceil` a secas.
 */
export function visibleWorldRect(camera: Camera): Rect {
  const halfW = camera.viewportWidth / (2 * camera.zoom);
  const halfH = camera.viewportHeight / (2 * camera.zoom);

  const a = isoToWorld(camera.x - halfW, camera.y - halfH);
  const b = isoToWorld(camera.x + halfW, camera.y - halfH);
  const c = isoToWorld(camera.x - halfW, camera.y + halfH);
  const d = isoToWorld(camera.x + halfW, camera.y + halfH);

  const minX = Math.min(a.x, b.x, c.x, d.x);
  const maxX = Math.max(a.x, b.x, c.x, d.x);
  const minY = Math.min(a.y, b.y, c.y, d.y);
  const maxY = Math.max(a.y, b.y, c.y, d.y);

  return {
    minX: Math.ceil(minX - 0.5),
    minY: Math.ceil(minY - 0.5),
    maxX: Math.floor(maxX + 0.5),
    maxY: Math.floor(maxY + 0.5),
  };
}

/**
 * El viewport en píxeles isométricos, con `marginPx` más por cada lado. Es el
 * recorte EXACTO para una posición proyectada con `worldToIso`: una entidad
 * cuyo anclaje cae fuera, con el margen de su sprite, no se ve.
 */
export function visibleIsoRect(camera: Camera, marginPx: number): Rect {
  const halfW = camera.viewportWidth / (2 * camera.zoom);
  const halfH = camera.viewportHeight / (2 * camera.zoom);
  return {
    minX: camera.x - halfW - marginPx,
    minY: camera.y - halfH - marginPx,
    maxX: camera.x + halfW + marginPx,
    maxY: camera.y + halfH + marginPx,
  };
}

/** El mismo rectángulo con `margin` más por cada lado (tiles o píxeles, según el rectángulo). */
export function expandRect(rect: Rect, margin: number): Rect {
  return {
    minX: rect.minX - margin,
    minY: rect.minY - margin,
    maxX: rect.maxX + margin,
    maxY: rect.maxY + margin,
  };
}

export function rectEquals(a: Rect | null, b: Rect): boolean {
  return a !== null && a.minX === b.minX && a.minY === b.minY && a.maxX === b.maxX && a.maxY === b.maxY;
}

/** Si un punto (puede ser fraccionario) cae dentro del rectángulo, bordes incluidos. */
export function rectContains(rect: Rect, x: number, y: number): boolean {
  return x >= rect.minX && x <= rect.maxX && y >= rect.minY && y <= rect.maxY;
}

/**
 * Los chunks que un rectángulo de tiles toca, en coordenadas de chunk. Admite
 * tiles negativos o más allá del mundo: el que pregunte por ese chunk no lo
 * encontrará, y eso basta.
 */
export function chunkRectOf(rect: Rect, chunkSize: number): Rect {
  return {
    minX: Math.floor(rect.minX / chunkSize),
    minY: Math.floor(rect.minY / chunkSize),
    maxX: Math.floor(rect.maxX / chunkSize),
    maxY: Math.floor(rect.maxY / chunkSize),
  };
}
