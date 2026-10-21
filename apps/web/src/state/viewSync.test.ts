import { describe, expect, it } from 'vitest';

import { VIEW_MIN_INTERVAL_MS, ViewSync } from './viewSync';

const WORLD = { width: 512, height: 512, chunkSize: 32 };
const CITY = { x: 100, y: 100 }; // chunk (3,3)

/** Una sesión con su snapshot inicial ya servido, centrado en la ciudad. */
function activa(): ViewSync {
  const sync = new ViewSync();
  sync.onWelcome(WORLD);
  sync.onInitialSnapshot(CITY);
  return sync;
}

describe('ViewSync', () => {
  it('no envía nada fuera de una sesión', () => {
    const sync = new ViewSync();
    expect(sync.update({ x: 300, y: 300 }, 0)).toBeNull();
  });

  it('no envía nada entre el welcome y el snapshot inicial', () => {
    const sync = new ViewSync();
    sync.onWelcome(WORLD);
    expect(sync.isAwaitingSnapshot()).toBe(true);
    expect(sync.update({ x: 300, y: 300 }, 0)).toBeNull();
  });

  it('no envía nada mientras la cámara siga en el chunk que el servidor conoce', () => {
    const sync = activa();
    expect(sync.update({ x: 96.2, y: 127.9 }, 0)).toBeNull(); // sigue en (3,3)
    expect(sync.update({ x: 120, y: 100 }, 5000)).toBeNull();
  });

  it('al cruzar de chunk envía el centro como tile entero', () => {
    const sync = activa();
    expect(sync.update({ x: 128.7, y: 100.4 }, 0)).toEqual({ x: 128, y: 100 });
  });

  it('como mucho uno por segundo, y al cumplirse envía el centro de ese momento', () => {
    const sync = activa();
    expect(sync.update({ x: 130, y: 100 }, 1000)).toEqual({ x: 130, y: 100 }); // chunk (4,3)
    // Otro chunk antes del segundo: se retiene.
    expect(sync.update({ x: 170, y: 100 }, 1000 + VIEW_MIN_INTERVAL_MS - 1)).toBeNull();
    // Cumplido el segundo, sale el centro más reciente, no el del cruce.
    expect(sync.update({ x: 200, y: 101 }, 1000 + VIEW_MIN_INTERVAL_MS)).toEqual({ x: 200, y: 101 });
  });

  it('si antes del segundo vuelve al chunk ya conocido, no envía nada', () => {
    const sync = activa();
    expect(sync.update({ x: 130, y: 100 }, 1000)).not.toBeNull(); // conoce (4,3)
    expect(sync.update({ x: 170, y: 100 }, 1500)).toBeNull(); // (5,3), retenido
    expect(sync.update({ x: 131, y: 100 }, 2500)).toBeNull(); // de vuelta en (4,3)
  });

  it('recorta el centro al mundo', () => {
    const sync = activa();
    expect(sync.update({ x: 900, y: -40 }, 0)).toEqual({ x: 511, y: 0 });
  });

  it('una reconexión restaura la vista: tras el snapshot inicial envía el centro de la cámara', () => {
    const sync = activa();
    expect(sync.update({ x: 400, y: 400 }, 10_000)).toEqual({ x: 400, y: 400 });

    sync.suspend();
    expect(sync.update({ x: 401, y: 400 }, 10_050)).toBeNull();

    // Sesión nueva: el servidor vuelve a centrar en la ciudad.
    sync.onWelcome(WORLD);
    expect(sync.update({ x: 400, y: 400 }, 10_100)).toBeNull();
    sync.onInitialSnapshot(CITY);
    // Sin esperar el segundo de la sesión anterior (el último envío fue hace
    // 200 ms): la cadencia empieza con la sesión, y la vista se restaura ya.
    expect(sync.update({ x: 400, y: 400 }, 10_200)).toEqual({ x: 400, y: 400 });
  });

  it('un snapshot que no es el inicial no cambia lo que el servidor conoce', () => {
    const sync = activa();
    sync.onInitialSnapshot({ x: 400, y: 400 }); // ya estaba activa: se ignora
    expect(sync.update({ x: 100, y: 100 }, 0)).toBeNull(); // sigue conociendo (3,3)
  });
});
