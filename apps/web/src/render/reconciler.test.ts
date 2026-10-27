import { describe, expect, it } from 'vitest';

import {
  RECONCILE_EPS_TILES,
  RECONCILE_MAX_MS,
  RECONCILE_SNAP_TILES,
  RenderReconciler,
  targetPosition,
  type UnitInputs,
} from './reconciler';
import { renderPosition, type ActiveMovement } from '../state/interpolation';

const START = 1_757_376_000_000;
const FRAME = 1000 / 60;

/** El ejemplo canónico de docs/specs/movement.md: (0,0) → (4,1) en 2769 ms. */
const canonical: ActiveMovement = {
  movementId: 900,
  path: [
    { x: 0, y: 0, tMs: 0 },
    { x: 1, y: 0, tMs: 600 },
    { x: 2, y: 1, tMs: 1449 },
    { x: 3, y: 1, tMs: 2409 },
    { x: 4, y: 1, tMs: 2769 },
  ],
  startTimeMs: START,
  arrivalTimeMs: START + 2769,
  target: { x: 4, y: 1 },
};

const moving: UnitInputs = { x: 0, y: 0, movement: canonical };

/** Una orden nueva desde (1,0) hacia el sur, como la que manda el servidor al sustituir. */
function replacement(atMs: number): ActiveMovement {
  return {
    movementId: 901,
    path: [
      { x: 1, y: 0, tMs: 0 },
      { x: 1, y: 1, tMs: 600 },
      { x: 1, y: 2, tMs: 1200 },
    ],
    startTimeMs: atMs,
    arrivalTimeMs: atMs + 1200,
    target: { x: 1, y: 2 },
  };
}

/** Avanza `ms` en frames de `frameMs` y devuelve la última posición dibujada. */
function run(
  r: RenderReconciler,
  inputs: UnitInputs,
  fromMs: number,
  ms: number,
  frameMs = FRAME,
  onFrame?: (t: number, p: { x: number; y: number }) => void,
): { t: number; p: { x: number; y: number } } {
  let t = fromMs;
  let p = { x: Number.NaN, y: Number.NaN };
  // Se cuentan frames, no milisegundos: con instantes del orden de 1e12, sumar
  // fracciones de frame acumula error de redondeo y añadiría un frame de más.
  const frames = Math.round(ms / frameMs);
  for (let i = 1; i <= frames; i++) {
    t = fromMs + i * frameMs;
    r.frame(t, frameMs);
    p = r.position(1, inputs);
    onFrame?.(t, p);
  }
  return { t, p };
}

const dist = (a: { x: number; y: number }, b: { x: number; y: number }) =>
  Math.max(Math.abs(a.x - b.x), Math.abs(a.y - b.y));

describe('RenderReconciler', () => {
  it('la primera vez que ve una unidad la dibuja donde dice el servidor', () => {
    const r = new RenderReconciler();
    r.frame(START + 900, FRAME);
    expect(r.position(1, moving)).toEqual(renderPosition(canonical, START + 900));
    expect(r.position(2, { x: 7, y: 9, movement: null })).toEqual({ x: 7, y: 9 });
  });

  it('sin discontinuidades sigue la interpolación exactamente, sin retraso ni saltos', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, moving);
    // El trayecto entero, frame a frame: el dibujo ES la interpolación. Si se
    // persiguiera el objetivo, iría una décima de tile por detrás y la cota
    // dura lo haría saltar cada 300 ms.
    run(r, moving, START, 3000, FRAME, (t, p) => {
      const expected = renderPosition(canonical, t);
      expect(p.x).toBeCloseTo(expected.x, 9);
      expect(p.y).toBeCloseTo(expected.y, 9);
    });
  });

  it('una cancelación a mitad de segmento vuelve al tile alcanzado sin saltar', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, moving);
    const { t, p: drawnBefore } = run(r, moving, START, 1000);
    expect(drawnBefore.x).toBeGreaterThan(1.4); // camino de (2,1)

    // unit.movement.cancelled: stoppedAt es el último waypoint alcanzado.
    const stopped: UnitInputs = { x: 1, y: 0, movement: null };
    r.frame(t + FRAME, FRAME);
    const first = r.position(1, stopped);
    // Continuidad: el primer frame se queda cerca de donde estaba, no en (1,0).
    expect(dist(first, drawnBefore)).toBeLessThan(dist(drawnBefore, { x: 1, y: 0 }) * 0.3);
    expect(dist(first, { x: 1, y: 0 })).toBeGreaterThan(0.2);

    let prev = dist(first, { x: 1, y: 0 });
    const { p: last } = run(r, stopped, t + FRAME, RECONCILE_MAX_MS, FRAME, (_t, p) => {
      const d = dist(p, { x: 1, y: 0 });
      expect(d).toBeLessThanOrEqual(prev); // converge sin oscilar
      // Nunca se pasa de largo: se acerca desde el lado por el que venía.
      expect(p.x).toBeGreaterThanOrEqual(1);
      prev = d;
    });
    expect(last).toEqual({ x: 1, y: 0 });
  });

  it('una orden que sustituye a otra empalma con la ruta nueva', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, moving);
    const { t, p: drawnBefore } = run(r, moving, START, 1000);

    const replaced: UnitInputs = { x: 1, y: 0, movement: replacement(t) };
    r.frame(t + FRAME, FRAME);
    const first = r.position(1, replaced);
    expect(dist(first, drawnBefore)).toBeLessThan(0.15);

    // Pasada la corrección, el dibujo es la interpolación de la ruta nueva.
    const { t: t2, p } = run(r, replaced, t + FRAME, 400);
    const expected = renderPosition(replaced.movement!, t2);
    expect(p.x).toBeCloseTo(expected.x, 9);
    expect(p.y).toBeCloseTo(expected.y, 9);
  });

  it('una llegada anunciada antes de que el reloj local la alcance se completa suavemente', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, moving);
    // El reloj local va 80 ms por detrás: el servidor ya dio la llegada.
    const { t, p: drawnBefore } = run(r, moving, START, 2769 - 80);
    expect(dist(drawnBefore, { x: 4, y: 1 })).toBeGreaterThan(0.1);

    const arrived: UnitInputs = { x: 4, y: 1, movement: null };
    r.frame(t + FRAME, FRAME);
    const first = r.position(1, arrived);
    expect(dist(first, { x: 4, y: 1 })).toBeGreaterThan(0.05);
    const { p } = run(r, arrived, t + FRAME, RECONCILE_MAX_MS);
    expect(p).toEqual({ x: 4, y: 1 });
  });

  it('una discontinuidad de más de RECONCILE_SNAP_TILES se salta en el mismo frame', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, { x: 10, y: 10, movement: null });
    r.frame(START + FRAME, FRAME);
    // Más de 2 tiles en un solo eje basta: la distancia es la de Chebyshev.
    expect(r.position(1, { x: 10, y: 10 + RECONCILE_SNAP_TILES + 0.5, movement: null })).toEqual({
      x: 10,
      y: 12.5,
    });
  });

  it('una discontinuidad de exactamente RECONCILE_SNAP_TILES todavía se desliza', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, { x: 10, y: 10, movement: null });
    r.frame(START + FRAME, FRAME);
    const p = r.position(1, { x: 10 + RECONCILE_SNAP_TILES, y: 10 - RECONCILE_SNAP_TILES, movement: null });
    expect(p.x).toBeLessThan(10 + RECONCILE_SNAP_TILES);
    expect(p.y).toBeGreaterThan(10 - RECONCILE_SNAP_TILES);
  });

  it('un desfase por debajo de RECONCILE_EPS_TILES se anula sin suavizar', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, { x: 10, y: 10, movement: null });
    r.frame(START + FRAME, FRAME);
    const tiny = RECONCILE_EPS_TILES / 2;
    expect(r.position(1, { x: 10 + tiny, y: 10 - tiny, movement: null })).toEqual({
      x: 10 + tiny,
      y: 10 - tiny,
    });
  });

  it('la cota dura anula el desfase al pasar RECONCILE_MAX_MS, nunca antes', () => {
    const r = new RenderReconciler();
    r.frame(START, 10);
    r.position(1, { x: 10, y: 10, movement: null });
    const settled: UnitInputs = { x: 11.9, y: 10, movement: null };
    // Frames de 10 ms: al cabo de 300 ms al desfase de 1.9 tiles le quedan
    // 1.9·e^-5 ≈ 0.013, más que EPS. Sólo la cota lo anula.
    let t = START;
    for (let elapsed = 10; elapsed <= RECONCILE_MAX_MS; elapsed += 10) {
      t += 10;
      r.frame(t, 10);
      const p = r.position(1, settled);
      expect(p.x).toBeLessThan(11.9);
    }
    r.frame(t + 10, 10);
    expect(r.position(1, settled)).toEqual({ x: 11.9, y: 10 });
  });

  it('un objetivo que salta en cada frame tampoco se corrige más allá de la cota', () => {
    const r = new RenderReconciler();
    let t = START;
    r.frame(t, FRAME);
    r.position(1, { x: 10, y: 10, movement: null });
    let snappedAt: number | null = null;
    for (let i = 1; i <= 40 && snappedAt === null; i++) {
      t += FRAME;
      r.frame(t, FRAME);
      // Un entity.update por frame, alternando: el desfase nunca baja de EPS.
      const x = i % 2 === 0 ? 10 : 11;
      const p = r.position(1, { x, y: 10, movement: null });
      if (p.x === x) snappedAt = i * FRAME;
    }
    expect(snappedAt).not.toBeNull();
    expect(snappedAt!).toBeGreaterThan(RECONCILE_MAX_MS - FRAME);
    expect(snappedAt!).toBeLessThanOrEqual(RECONCILE_MAX_MS + FRAME);
  });

  it('converge igual a 30, 60 y 144 fps', () => {
    const after = (fps: number) => {
      const r = new RenderReconciler();
      const frameMs = 1000 / fps;
      r.frame(START, frameMs);
      r.position(1, { x: 10, y: 10, movement: null });
      // La discontinuidad aparece en el primer frame y se corrige durante 100 ms.
      const { p } = run(r, { x: 11.5, y: 10, movement: null }, START, 100, frameMs);
      return p.x;
    };
    const at30 = after(30);
    const at60 = after(60);
    const at144 = after(144);
    expect(Math.abs(at30 - at60)).toBeLessThan(0.03);
    expect(Math.abs(at60 - at144)).toBeLessThan(0.03);
  });

  it('una corrección del reloj no hace saltar a una unidad en marcha', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, moving);
    const { t, p: before } = run(r, moving, START, 700);
    // session.pong: el desfase de reloj se recalcula y el instante del servidor
    // avanza 150 ms de golpe, aunque el frame sólo dure 16 ms.
    const jumped = t + FRAME + 150;
    r.frame(jumped, FRAME);
    const p = r.position(1, moving);
    expect(dist(p, before)).toBeLessThan(0.1);
    // Y después recupera la interpolación del reloj corregido.
    const { t: t2, p: later } = run(r, moving, jumped, 400);
    const expected = renderPosition(canonical, t2);
    expect(later.x).toBeCloseTo(expected.x, 9);
    expect(later.y).toBeCloseTo(expected.y, 9);
  });

  it('al volver de una pestaña oculta salta si la unidad avanzó más de 2 tiles', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, moving);
    // El ticker acota el frame a 100 ms; el reloj del servidor avanzó 2.5 s.
    r.frame(START + 2500, 100);
    expect(r.position(1, moving)).toEqual(targetPosition(moving, START + 2500));
  });

  it('una unidad que deja de dibujarse reaparece sin deslizarse', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, { x: 10, y: 10, movement: null });
    r.position(2, { x: 20, y: 20, movement: null });
    // La unidad 1 se guarnece o sale del área de interés.
    r.retain(new Set([2]));
    expect(r.size).toBe(1);
    expect(r.drawn(1)).toBeUndefined();
    r.frame(START + FRAME, FRAME);
    expect(r.position(1, { x: 11, y: 10, movement: null })).toEqual({ x: 11, y: 10 });
  });

  it('forget olvida una sola unidad y deja las demás con su registro', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, { x: 10, y: 10, movement: null });
    r.position(2, { x: 20, y: 20, movement: null });
    r.forget(1);
    expect(r.size).toBe(1);
    expect(r.drawn(1)).toBeUndefined();
    expect(r.drawn(2)).toEqual({ x: 20, y: 20 });
    r.frame(START + FRAME, FRAME);
    // Sin registro reaparece donde dice el servidor, sin deslizarse.
    expect(r.position(1, { x: 11, y: 10, movement: null })).toEqual({ x: 11, y: 10 });
    // Olvidar lo que no existe no es un error.
    r.forget(99);
    expect(r.size).toBe(2);
  });

  it('una unidad quieta a la que no se le pide posición durante frames se corrige igual al llegar su delta', () => {
    // La escena sólo pide posición a las unidades activas; una quieta y ya en
    // su sitio deja de pedirla. Cuando su delta la mueve un tile, el desfase
    // debe ser el mismo que si se hubiera pedido en cada frame.
    const continua = new RenderReconciler();
    const saltada = new RenderReconciler();
    const quieta: UnitInputs = { x: 10, y: 10, movement: null };
    continua.frame(START, FRAME);
    saltada.frame(START, FRAME);
    continua.position(1, quieta);
    saltada.position(1, quieta);
    for (let i = 1; i <= 30; i++) {
      continua.frame(START + i * FRAME, FRAME);
      saltada.frame(START + i * FRAME, FRAME);
      continua.position(1, quieta);
      // saltada: no se le pide nada.
    }
    const movida: UnitInputs = { x: 11, y: 10, movement: null };
    continua.frame(START + 31 * FRAME, FRAME);
    saltada.frame(START + 31 * FRAME, FRAME);
    const a = continua.position(1, movida);
    const b = saltada.position(1, movida);
    expect(b).toEqual(a);
    expect(a.x).toBeGreaterThan(10);
    expect(a.x).toBeLessThan(11);
  });

  it('el picking ve la posición dibujada, no la autoritativa', () => {
    const r = new RenderReconciler();
    r.frame(START, FRAME);
    r.position(1, { x: 10, y: 10, movement: null });
    r.frame(START + FRAME, FRAME);
    const p = r.position(1, { x: 11, y: 10, movement: null });
    expect(r.drawn(1)).toEqual(p);
    expect(p.x).not.toBe(11);
  });
});
