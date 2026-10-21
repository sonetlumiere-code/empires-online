/**
 * Corrección gradual de la posición dibujada hacia la verdad del servidor.
 *
 * Mientras nada cambie, una unidad se dibuja EXACTAMENTE en `renderPosition`:
 * la interpolación de su polilínea en el reloj del servidor. Lo que se suaviza
 * son las discontinuidades —una orden que sustituye a otra, una cancelación, una
 * llegada anunciada antes de tiempo, un `entity.update` con posición, un
 * snapshot tras reconectar, una corrección del reloj— y nada más.
 *
 * El mecanismo es un DESFASE por unidad que decae hacia cero, no una
 * persecución del objetivo: perseguirlo dejaría el dibujo retrasado durante
 * todo el trayecto y la cota dura lo haría saltar cada 300 ms.
 *
 * Sin Pixi ni DOM: la escena le pide posiciones y él no dibuja nada.
 *
 * Ver ../../../../docs/architecture/frontend.md §5.3
 */
import { reconcile, renderPosition, type ActiveMovement, type RenderPosition } from '../state/interpolation';

/** Constante de tiempo del decaimiento exponencial del desfase. */
export const RECONCILE_TAU_MS = 60;
/** Cota dura: un desfase que lleva este tiempo corrigiéndose se anula de golpe. */
export const RECONCILE_MAX_MS = 300;
/** Una discontinuidad mayor, en algún eje, se salta en lugar de deslizarse. */
export const RECONCILE_SNAP_TILES = 2;
/** Un desfase menor, en los dos ejes, se anula: es menos de un píxel. */
export const RECONCILE_EPS_TILES = 0.01;

/** Lo que el servidor dice de una unidad y basta para saber dónde dibujarla. */
export interface UnitInputs {
  readonly x: number;
  readonly y: number;
  readonly movement: ActiveMovement | null;
}

/** Posición sin corregir: la interpolación si se mueve, su tile si no. */
export function targetPosition(inputs: UnitInputs, serverTimeMs: number): RenderPosition {
  if (!inputs.movement) return { x: inputs.x, y: inputs.y };
  return renderPosition(inputs.movement, serverTimeMs);
}

interface UnitRecord {
  inputs: UnitInputs;
  ox: number;
  oy: number;
  /** Tiempo que lleva corrigiéndose el desfase actual; 0 si no hay desfase. */
  correctingMs: number;
  drawnX: number;
  drawnY: number;
}

export class RenderReconciler {
  private readonly records = new Map<number, UnitRecord>();
  private nowMs = 0;
  private dtMs = 0;
  /** Instante en que continuaría la línea temporal del frame anterior. */
  private continuityMs = 0;
  private started = false;

  /**
   * Abre un frame. `serverTimeMs` es el reloj del servidor ya corregido;
   * `dtMs`, lo que avanzó el ticker desde el frame anterior.
   *
   * La continuidad se mide con `dtMs` y no con la diferencia de relojes a
   * propósito: así una corrección del reloj, o la vuelta de una pestaña oculta
   * (el ticker acota cada frame), cuentan como discontinuidad y se suavizan.
   */
  frame(serverTimeMs: number, dtMs: number): void {
    const dt = Math.max(0, dtMs);
    this.continuityMs = this.started ? this.nowMs + dt : serverTimeMs;
    this.nowMs = serverTimeMs;
    this.dtMs = dt;
    this.started = true;
  }

  /** Posición en la que se dibuja la unidad este frame. Llamar una vez por unidad y frame. */
  position(unitId: number, inputs: UnitInputs): RenderPosition {
    const target = targetPosition(inputs, this.nowMs);
    const rec = this.records.get(unitId);

    // Sin registro no hay continuidad de la que partir: la unidad aparece donde
    // dice el servidor (spawn, primer snapshot, reaparición).
    if (!rec) {
      this.records.set(unitId, {
        inputs,
        ox: 0,
        oy: 0,
        correctingMs: 0,
        drawnX: target.x,
        drawnY: target.y,
      });
      return target;
    }

    // Dónde estaría si nada hubiera cambiado: lo de antes, un frame después.
    const before = targetPosition(rec.inputs, this.continuityMs);
    let offset = { x: before.x + rec.ox - target.x, y: before.y + rec.oy - target.y };
    rec.inputs = inputs;

    if (Math.abs(offset.x) <= RECONCILE_EPS_TILES && Math.abs(offset.y) <= RECONCILE_EPS_TILES) {
      offset = { x: 0, y: 0 };
    } else if (rec.correctingMs + this.dtMs > RECONCILE_MAX_MS) {
      offset = { x: 0, y: 0 };
    } else {
      const k = 1 - Math.exp(-this.dtMs / RECONCILE_TAU_MS);
      // reconcile salta al objetivo (desfase cero) si la discontinuidad pasa de
      // RECONCILE_SNAP_TILES en algún eje.
      offset = reconcile(offset, { x: 0, y: 0 }, k, RECONCILE_SNAP_TILES);
    }

    const settled = offset.x === 0 && offset.y === 0;
    rec.correctingMs = settled ? 0 : rec.correctingMs + this.dtMs;
    rec.ox = offset.x;
    rec.oy = offset.y;
    rec.drawnX = target.x + offset.x;
    rec.drawnY = target.y + offset.y;
    return { x: rec.drawnX, y: rec.drawnY };
  }

  /** Última posición dibujada, para el picking. */
  drawn(unitId: number): RenderPosition | undefined {
    const rec = this.records.get(unitId);
    return rec ? { x: rec.drawnX, y: rec.drawnY } : undefined;
  }

  /** Olvida las unidades que ya no se dibujan: si vuelven, reaparecen sin deslizarse. */
  retain(unitIds: ReadonlySet<number>): void {
    for (const id of this.records.keys()) {
      if (!unitIds.has(id)) this.records.delete(id);
    }
  }

  /** Unidades con registro. Sólo para tests y diagnóstico. */
  get size(): number {
    return this.records.size;
  }
}
