import { describe, expect, it } from 'vitest';

import {
  authoritativePosition,
  hasArrived,
  reconcile,
  remainingMs,
  renderPosition,
  waypointIndexAt,
  type ActiveMovement,
} from './interpolation';

const START = 1_757_376_000_000;

/**
 * La misma polilínea que produce el servidor para un VILLAGER (600 ms/tile):
 * hierba ortogonal, hierba diagonal, bosque ortogonal, camino ortogonal.
 * Debe coincidir con el ejemplo canónico de docs/specs/movement.md.
 */
const movement: ActiveMovement = {
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

describe('authoritativePosition', () => {
  it('reproduce exactamente lo que calcula el servidor', () => {
    const cases: Array<[number, { x: number; y: number }]> = [
      [-1000, { x: 0, y: 0 }],
      [0, { x: 0, y: 0 }],
      [599, { x: 0, y: 0 }],
      [600, { x: 1, y: 0 }],
      [1448, { x: 1, y: 0 }],
      [1449, { x: 2, y: 1 }],
      [2408, { x: 2, y: 1 }],
      [2409, { x: 3, y: 1 }],
      [2769, { x: 4, y: 1 }],
      [999_999, { x: 4, y: 1 }],
    ];
    for (const [elapsed, want] of cases) {
      expect(authoritativePosition(movement, START + elapsed), `elapsed=${elapsed}`).toEqual(want);
    }
  });

  it('siempre devuelve coordenadas enteras: el servidor razona en tiles', () => {
    for (let elapsed = 0; elapsed <= 2769; elapsed += 37) {
      const pos = authoritativePosition(movement, START + elapsed);
      expect(Number.isInteger(pos.x)).toBe(true);
      expect(Number.isInteger(pos.y)).toBe(true);
    }
  });
});

describe('renderPosition', () => {
  it('coincide con la autoritativa en los instantes exactos de cada waypoint', () => {
    for (const wp of movement.path) {
      expect(renderPosition(movement, START + wp.tMs)).toEqual({ x: wp.x, y: wp.y });
    }
  });

  it('interpola linealmente dentro del segmento', () => {
    // Mitad exacta del primer segmento (0..600).
    expect(renderPosition(movement, START + 300)).toEqual({ x: 0.5, y: 0 });
    // Un cuarto del segundo segmento (600..1449, dura 849).
    const pos = renderPosition(movement, START + 600 + Math.round(849 / 4));
    expect(pos.x).toBeCloseTo(1.25, 2);
    expect(pos.y).toBeCloseTo(0.25, 2);
  });

  it('avanza de forma monótona: nunca retrocede', () => {
    let prev = -Infinity;
    for (let elapsed = 0; elapsed <= 2769; elapsed += 13) {
      const pos = renderPosition(movement, START + elapsed);
      const progress = pos.x + pos.y;
      expect(progress).toBeGreaterThanOrEqual(prev - 1e-9);
      prev = progress;
    }
  });

  it('se queda clavada en el destino después de llegar', () => {
    expect(renderPosition(movement, START + 2769)).toEqual({ x: 4, y: 1 });
    expect(renderPosition(movement, START + 60_000)).toEqual({ x: 4, y: 1 });
  });

  it('antes de empezar está en el origen', () => {
    expect(renderPosition(movement, START - 5000)).toEqual({ x: 0, y: 0 });
  });

  it('la posición dibujada nunca se aleja más de un tile de la autoritativa', () => {
    // Es la garantía de que la interpolación es una ayuda visual, no una
    // predicción que pueda divergir de la verdad.
    for (let elapsed = 0; elapsed <= 2769; elapsed += 7) {
      const auth = authoritativePosition(movement, START + elapsed);
      const render = renderPosition(movement, START + elapsed);
      expect(Math.abs(render.x - auth.x)).toBeLessThanOrEqual(1);
      expect(Math.abs(render.y - auth.y)).toBeLessThanOrEqual(1);
    }
  });

  it('no divide por cero si un segmento tuviera duración nula', () => {
    const degenerate: ActiveMovement = {
      ...movement,
      path: [
        { x: 0, y: 0, tMs: 0 },
        { x: 1, y: 0, tMs: 0 },
        { x: 2, y: 0, tMs: 600 },
      ],
      arrivalTimeMs: START + 600,
    };
    const pos = renderPosition(degenerate, START + 1);
    expect(Number.isFinite(pos.x)).toBe(true);
    expect(Number.isFinite(pos.y)).toBe(true);
  });
});

describe('waypointIndexAt', () => {
  it('encuentra el último waypoint alcanzado', () => {
    expect(waypointIndexAt(movement.path, -1)).toBe(0);
    expect(waypointIndexAt(movement.path, 0)).toBe(0);
    expect(waypointIndexAt(movement.path, 599)).toBe(0);
    expect(waypointIndexAt(movement.path, 600)).toBe(1);
    expect(waypointIndexAt(movement.path, 1449)).toBe(2);
    expect(waypointIndexAt(movement.path, 2769)).toBe(4);
    expect(waypointIndexAt(movement.path, 99_999)).toBe(4);
  });

  it('devuelve -1 para una polilínea vacía', () => {
    expect(waypointIndexAt([], 100)).toBe(-1);
  });
});

describe('reconcile', () => {
  it('se acerca gradualmente a la verdad del servidor', () => {
    const step = reconcile({ x: 0, y: 0 }, { x: 1, y: 1 }, 0.25);
    expect(step).toEqual({ x: 0.25, y: 0.25 });
  });

  it('salta de golpe si la divergencia es grande', () => {
    // Arrastrar lentamente una unidad que está a diez tiles de distancia sería
    // una mentira peor que el salto.
    const step = reconcile({ x: 0, y: 0 }, { x: 10, y: 10 }, 0.25, 2);
    expect(step).toEqual({ x: 10, y: 10 });
  });

  it('converge en unas pocas iteraciones', () => {
    let pos = { x: 0, y: 0 };
    const truth = { x: 1.5, y: 0.5 };
    for (let i = 0; i < 40; i++) pos = reconcile(pos, truth, 0.3);
    expect(pos.x).toBeCloseTo(truth.x, 4);
    expect(pos.y).toBeCloseTo(truth.y, 4);
  });

  it('acota el ritmo fuera del rango [0,1]', () => {
    expect(reconcile({ x: 0, y: 0 }, { x: 1, y: 0 }, 5)).toEqual({ x: 1, y: 0 });
    expect(reconcile({ x: 0, y: 0 }, { x: 1, y: 0 }, -3)).toEqual({ x: 0, y: 0 });
  });
});

describe('hasArrived y remainingMs', () => {
  it('detecta la llegada en el instante exacto', () => {
    expect(hasArrived(movement, START + 2768)).toBe(false);
    expect(hasArrived(movement, START + 2769)).toBe(true);
  });

  it('el tiempo restante nunca es negativo', () => {
    expect(remainingMs(movement, START)).toBe(2769);
    expect(remainingMs(movement, START + 2769)).toBe(0);
    expect(remainingMs(movement, START + 100_000)).toBe(0);
  });
});
