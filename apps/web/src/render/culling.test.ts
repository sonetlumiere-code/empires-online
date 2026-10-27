import { describe, expect, it } from 'vitest';

import { chunkRectOf, expandRect, rectContains, rectEquals, visibleIsoRect, visibleWorldRect } from './culling';
import { cameraAt, worldToIso, type Camera } from './iso';

/**
 * Un viewport de 640 × 320 a zoom 1 mide 10 tiles de medio ancho en cada eje
 * del mundo: la esquina (−320, −160) en isométrico es (−10, 0) en tiles y la
 * (+320, −160) es (0, −10).
 */
const centrada = (zoom = 1): Camera => cameraAt({ x: 100, y: 100 }, 640, 320, zoom);

describe('visibleWorldRect', () => {
  it('cubre la caja envolvente del viewport en tiles', () => {
    expect(visibleWorldRect(centrada())).toEqual({ minX: 90, minY: 90, maxX: 110, maxY: 110 });
  });

  it('con el doble de zoom se ve la mitad de tiles', () => {
    expect(visibleWorldRect(centrada(2))).toEqual({ minX: 95, minY: 95, maxX: 105, maxY: 105 });
  });

  it('incluye el tile cuyo rombo asoma por el borde aunque su centro quede fuera', () => {
    // Desplazar la cámara 4 px a la derecha mete un octavo de tile más por la
    // derecha: el tile 110 sigue tocando el borde y el 111 todavía no.
    const cam = { ...centrada(), x: centrada().x + 4 };
    expect(visibleWorldRect(cam).maxX).toBe(110);
    // Con 48 px (tres cuartos de tile en X isométrico) el 111 ya asoma.
    const lejos = { ...centrada(), x: centrada().x + 48 };
    expect(visibleWorldRect(lejos).maxX).toBe(111);
  });

  it('un desplazamiento menor que un tile no cambia el rectángulo: la escena no recalcula nada', () => {
    const base = visibleWorldRect(centrada());
    const movida = { ...centrada(), x: centrada().x + 10, y: centrada().y + 3 };
    expect(rectEquals(base, visibleWorldRect(movida))).toBe(true);
  });

  it('acepta un viewport asimétrico y una cámara en coordenadas fraccionarias', () => {
    const iso = worldToIso(10.4, 20.7);
    const cam: Camera = { x: iso.x, y: iso.y, zoom: 1, viewportWidth: 1920, viewportHeight: 1080 };
    const rect = visibleWorldRect(cam);
    // La caja envolvente del viewport girado mide, en cada eje del mundo,
    // 1920/64 + 1080/32 = 63,75 tiles: el rombo visible es la mitad de la caja.
    // El redondeo al tile puede dar un tile más en un eje que en otro.
    for (const span of [rect.maxX - rect.minX, rect.maxY - rect.minY]) {
      expect(span).toBeGreaterThanOrEqual(63);
      expect(span).toBeLessThanOrEqual(65);
    }
    expect(rectContains(rect, 10.4, 20.7)).toBe(true);
  });
});

describe('visibleIsoRect', () => {
  it('es el viewport alrededor de la cámara más el margen, dividido por el zoom', () => {
    const cam = centrada(2);
    expect(visibleIsoRect(cam, 10)).toEqual({
      minX: cam.x - 160 - 10,
      minY: cam.y - 80 - 10,
      maxX: cam.x + 160 + 10,
      maxY: cam.y + 80 + 10,
    });
  });

  it('una unidad proyectada justo fuera del margen no se dibuja; dentro, sí', () => {
    const cam = centrada();
    const rect = visibleIsoRect(cam, 64);
    // La proyección del centro de la cámara está dentro, y un punto a 321 px
    // (medio viewport más un píxel) queda fuera cuando el margen no lo cubre.
    expect(rectContains(rect, cam.x, cam.y)).toBe(true);
    expect(rectContains(rect, cam.x + 320 + 64, cam.y)).toBe(true);
    expect(rectContains(rect, cam.x + 320 + 65, cam.y)).toBe(false);
  });
});

describe('expandRect y rectContains', () => {
  it('el margen crece por los cuatro lados y la pertenencia es cerrada', () => {
    const rect = expandRect({ minX: 0, minY: 0, maxX: 2, maxY: 2 }, 2);
    expect(rect).toEqual({ minX: -2, minY: -2, maxX: 4, maxY: 4 });
    expect(rectContains(rect, -2, 4)).toBe(true);
    expect(rectContains(rect, 4.01, 0)).toBe(false);
    expect(rectContains(rect, 0, -2.5)).toBe(false);
  });

  it('rectEquals distingue cualquier lado y tolera no tener rectángulo anterior', () => {
    const a = { minX: 0, minY: 0, maxX: 2, maxY: 2 };
    expect(rectEquals(null, a)).toBe(false);
    expect(rectEquals(a, { ...a })).toBe(true);
    expect(rectEquals(a, { ...a, maxY: 3 })).toBe(false);
  });
});

describe('chunkRectOf', () => {
  it('traduce tiles a chunks con la división entera del servidor', () => {
    expect(chunkRectOf({ minX: 0, minY: 31, maxX: 32, maxY: 95 }, 32)).toEqual({
      minX: 0,
      minY: 0,
      maxX: 1,
      maxY: 2,
    });
  });

  it('los tiles negativos van al chunk −1, que nadie tendrá cargado', () => {
    expect(chunkRectOf({ minX: -3, minY: -40, maxX: 5, maxY: 5 }, 32)).toEqual({
      minX: -1,
      minY: -2,
      maxX: 0,
      maxY: 0,
    });
  });
});
