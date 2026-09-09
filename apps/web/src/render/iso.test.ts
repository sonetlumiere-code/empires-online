import { describe, expect, it } from 'vitest';

import {
  TILE_H,
  TILE_W,
  cameraAt,
  depthOf,
  isoToTile,
  isoToWorld,
  screenToTile,
  worldToIso,
  worldToScreen,
} from './iso';

// Estas transformaciones son la frontera entre el estado autoritativo (tiles) y
// los píxeles. Un error aquí no se ve como un bug de render: se ve como si el
// jugador hiciera clic en un sitio y la unidad fuera a otro.

describe('worldToIso', () => {
  it('el origen del mundo cae en el origen isométrico', () => {
    expect(worldToIso(0, 0)).toEqual({ x: 0, y: 0 });
  });

  it('aplica las fórmulas canónicas', () => {
    // screenX = (x - y) * TILE_W/2 ; screenY = (x + y) * TILE_H/2
    expect(worldToIso(1, 0)).toEqual({ x: TILE_W / 2, y: TILE_H / 2 });
    expect(worldToIso(0, 1)).toEqual({ x: -TILE_W / 2, y: TILE_H / 2 });
    expect(worldToIso(1, 1)).toEqual({ x: 0, y: TILE_H });
    expect(worldToIso(10, 4)).toEqual({ x: 6 * (TILE_W / 2), y: 14 * (TILE_H / 2) });
  });

  it('avanzar en x mueve a la derecha y avanzar en y mueve a la izquierda', () => {
    expect(worldToIso(5, 0).x).toBeGreaterThan(worldToIso(4, 0).x);
    expect(worldToIso(0, 5).x).toBeLessThan(worldToIso(0, 4).x);
    // Ambos ejes bajan en pantalla: es lo que produce la vista isométrica.
    expect(worldToIso(5, 0).y).toBeGreaterThan(0);
    expect(worldToIso(0, 5).y).toBeGreaterThan(0);
  });
});

describe('isoToWorld', () => {
  it('es la inversa exacta de worldToIso', () => {
    for (const [x, y] of [
      [0, 0],
      [1, 0],
      [0, 1],
      [7, 3],
      [123, 456],
      [511, 511],
    ] as const) {
      const iso = worldToIso(x, y);
      const back = isoToWorld(iso.x, iso.y);
      expect(back.x).toBeCloseTo(x, 10);
      expect(back.y).toBeCloseTo(y, 10);
    }
  });

  it('funciona con coordenadas fraccionarias (interpolación)', () => {
    const iso = worldToIso(3.5, 7.25);
    const back = isoToWorld(iso.x, iso.y);
    expect(back.x).toBeCloseTo(3.5, 10);
    expect(back.y).toBeCloseTo(7.25, 10);
  });
});

describe('isoToTile', () => {
  it('el centro de un tile devuelve ese tile', () => {
    for (const [x, y] of [
      [0, 0],
      [12, 5],
      [200, 199],
    ] as const) {
      const iso = worldToIso(x, y);
      expect(isoToTile(iso.x, iso.y)).toEqual({ x, y });
    }
  });

  it('redondea al tile más cercano', () => {
    const iso = worldToIso(10.4, 20.4);
    expect(isoToTile(iso.x, iso.y)).toEqual({ x: 10, y: 20 });

    const iso2 = worldToIso(10.6, 20.6);
    expect(isoToTile(iso2.x, iso2.y)).toEqual({ x: 11, y: 21 });
  });
});

describe('depthOf', () => {
  it('ordena por profundidad isométrica: menor x+y está más al fondo', () => {
    expect(depthOf(0, 0)).toBeLessThan(depthOf(1, 0));
    expect(depthOf(3, 4)).toBe(depthOf(4, 3));
    expect(depthOf(10, 10)).toBeGreaterThan(depthOf(5, 5));
  });
});

describe('cámara y picking', () => {
  const camera = cameraAt({ x: 100, y: 100 }, 800, 600, 1);

  it('el tile del centro de la cámara se dibuja en el centro del viewport', () => {
    const screen = worldToScreen(100, 100, camera);
    expect(screen.x).toBeCloseTo(400, 6);
    expect(screen.y).toBeCloseTo(300, 6);
  });

  it('screenToTile invierte worldToScreen', () => {
    for (const [x, y] of [
      [100, 100],
      [103, 97],
      [95, 108],
    ] as const) {
      const screen = worldToScreen(x, y, camera);
      expect(screenToTile(screen.x, screen.y, camera)).toEqual({ x, y });
    }
  });

  it('el picking sigue siendo exacto con zoom', () => {
    const zoomed = cameraAt({ x: 50, y: 50 }, 1024, 768, 1.75);
    const screen = worldToScreen(53, 47, zoomed);
    expect(screenToTile(screen.x, screen.y, zoomed)).toEqual({ x: 53, y: 47 });
  });
});
