import { describe, expect, it } from 'vitest';

import { CAMERA_PAN_PX_PER_S, CameraController, panDirectionOf } from './camera';
import { worldToIso } from './iso';

const WORLD = { width: 512, height: 512 };

function colocada(x = 100, y = 100): CameraController {
  const cam = new CameraController();
  cam.setBounds(WORLD);
  cam.centerOn({ x, y });
  return cam;
}

/** Centro de la cámara en píxeles isométricos, para razonar en pantalla. */
function isoCenter(cam: CameraController) {
  const c = cam.centerWorld()!;
  return worldToIso(c.x, c.y);
}

describe('CameraController', () => {
  it('sin posición no hay cámara que dibujar', () => {
    const cam = new CameraController();
    expect(cam.isPlaced()).toBe(false);
    expect(cam.camera(800, 600)).toBeNull();
  });

  it('centrada en un tile, la cámara apunta a su proyección', () => {
    const cam = colocada(100, 40);
    const iso = worldToIso(100, 40);
    expect(cam.camera(800, 600)).toEqual({ x: iso.x, y: iso.y, zoom: 1, viewportWidth: 800, viewportHeight: 600 });
  });

  it('las flechas desplazan en PANTALLA: derecha es +x isométrico y arriba es -y', () => {
    const cam = colocada();
    const antes = isoCenter(cam);
    cam.setHeld('right', true);
    cam.step(1000);
    const tras = isoCenter(cam);
    expect(tras.x - antes.x).toBeCloseTo(CAMERA_PAN_PX_PER_S, 6);
    expect(tras.y).toBeCloseTo(antes.y, 6);

    cam.setHeld('right', false);
    cam.setHeld('up', true);
    cam.step(500);
    const arriba = isoCenter(cam);
    expect(arriba.x).toBeCloseTo(tras.x, 6);
    expect(arriba.y - tras.y).toBeCloseTo(-CAMERA_PAN_PX_PER_S / 2, 6);
  });

  it('el desplazamiento no depende de la tasa de frames', () => {
    const a = colocada();
    const b = colocada();
    a.setHeld('down', true);
    b.setHeld('down', true);
    a.step(1000);
    for (let i = 0; i < 60; i++) b.step(1000 / 60);
    expect(b.centerWorld()!.x).toBeCloseTo(a.centerWorld()!.x, 9);
    expect(b.centerWorld()!.y).toBeCloseTo(a.centerWorld()!.y, 9);
  });

  it('sin teclas pulsadas no se mueve, y soltar todo detiene el desplazamiento', () => {
    const cam = colocada();
    cam.step(1000);
    expect(cam.centerWorld()).toEqual({ x: 100, y: 100 });
    cam.setHeld('left', true);
    cam.releaseAll();
    cam.step(1000);
    expect(cam.centerWorld()).toEqual({ x: 100, y: 100 });
  });

  it('el arrastre mueve la cámara en sentido contrario: el mapa sigue al cursor', () => {
    const cam = colocada();
    const antes = isoCenter(cam);
    cam.dragBy(64, 32); // el puntero baja a la derecha
    const tras = isoCenter(cam);
    expect(tras.x - antes.x).toBeCloseTo(-64, 9);
    expect(tras.y - antes.y).toBeCloseTo(-32, 9);
  });

  it('el centro se recorta a los límites del mundo', () => {
    const cam = colocada(2, 2);
    cam.setHeld('up', true);
    cam.step(60_000);
    const c = cam.centerWorld()!;
    expect(c.x).toBeGreaterThanOrEqual(0);
    expect(c.y).toBeGreaterThanOrEqual(0);
    cam.centerOn({ x: 9999, y: -5 });
    expect(cam.centerWorld()).toEqual({ x: 511, y: 0 });
  });

  it('la ciudad propia coloca la cámara sólo la primera vez', () => {
    const cam = new CameraController();
    cam.setBounds(WORLD);
    cam.setHome({ x: 50, y: 60 });
    expect(cam.centerWorld()).toEqual({ x: 50, y: 60 });
    cam.dragBy(640, 0);
    const lejos = cam.centerWorld();
    cam.setHome({ x: 50, y: 60 }); // otro snapshot que la trae: no recentra
    expect(cam.centerWorld()).toEqual(lejos);
  });

  it('H vuelve a la ciudad aunque ya no esté en el área de interés', () => {
    const cam = new CameraController();
    cam.setBounds(WORLD);
    cam.goHome(); // sin ciudad conocida, nada
    expect(cam.isPlaced()).toBe(false);
    cam.setHome({ x: 50, y: 60 });
    cam.dragBy(-3000, 1200); // muy lejos; los snapshots de allí no traen la ciudad
    cam.goHome();
    expect(cam.centerWorld()).toEqual({ x: 50, y: 60 });
  });

  it('las teclas de desplazamiento son flechas y WASD; las demás no desplazan', () => {
    expect(panDirectionOf('ArrowLeft')).toBe('left');
    expect(panDirectionOf('w')).toBe('up');
    expect(panDirectionOf('D')).toBe('right');
    expect(panDirectionOf('h')).toBeUndefined();
    expect(panDirectionOf('Enter')).toBeUndefined();
  });
});
