/**
 * Interpolación visual del movimiento.
 *
 * Distinción CENTRAL de todo el cliente:
 *
 *   authoritativePosition — el tile que el SERVIDOR dice que ocupa la unidad.
 *                           Es un entero. Es la verdad.
 *   renderPosition        — dónde se DIBUJA la unidad este frame. Puede ser
 *                           fraccionaria. Es una mentira piadosa para que el
 *                           movimiento no se vea a saltos de 600 ms.
 *
 * Confundirlas es el error que convierte un cliente en un cliente tramposo.
 * `renderPosition` jamás se envía al servidor ni se usa para decidir nada.
 *
 * Ver ../../../../docs/architecture/frontend.md
 */

export interface Waypoint {
  readonly x: number;
  readonly y: number;
  readonly tMs: number;
}

export interface ActiveMovement {
  readonly movementId: number;
  readonly path: readonly Waypoint[];
  readonly startTimeMs: number;
  readonly arrivalTimeMs: number;
  readonly target: { readonly x: number; readonly y: number };
}

export interface RenderPosition {
  readonly x: number;
  readonly y: number;
}

/**
 * Posición AUTORITATIVA en un instante: el último waypoint alcanzado.
 *
 * Es exactamente la misma función que evalúa el servidor. Que el cliente pueda
 * calcularla por su cuenta no lo convierte en autoridad: si alguna vez difiere,
 * gana el servidor.
 */
export function authoritativePosition(movement: ActiveMovement, serverTimeMs: number): RenderPosition {
  const path = movement.path;
  if (path.length === 0) return { x: 0, y: 0 };

  const elapsed = serverTimeMs - movement.startTimeMs;
  const first = path[0]!;
  const last = path[path.length - 1]!;

  if (elapsed <= 0) return { x: first.x, y: first.y };
  if (elapsed >= last.tMs) return { x: last.x, y: last.y };

  const index = waypointIndexAt(path, elapsed);
  const wp = path[index]!;
  return { x: wp.x, y: wp.y };
}

/**
 * Posición INTERPOLADA para dibujar: se avanza suavemente entre el waypoint
 * alcanzado y el siguiente, en proporción al tiempo transcurrido del segmento.
 */
export function renderPosition(movement: ActiveMovement, serverTimeMs: number): RenderPosition {
  const path = movement.path;
  if (path.length === 0) return { x: 0, y: 0 };

  const elapsed = serverTimeMs - movement.startTimeMs;
  const first = path[0]!;
  const last = path[path.length - 1]!;

  if (elapsed <= 0) return { x: first.x, y: first.y };
  if (elapsed >= last.tMs) return { x: last.x, y: last.y };

  const index = waypointIndexAt(path, elapsed);
  const from = path[index]!;
  const to = path[index + 1];
  if (!to) return { x: from.x, y: from.y };

  const segmentMs = to.tMs - from.tMs;
  // Un segmento de duración cero no debe producir una división por cero.
  const alpha = segmentMs > 0 ? (elapsed - from.tMs) / segmentMs : 1;

  return {
    x: from.x + (to.x - from.x) * alpha,
    y: from.y + (to.y - from.y) * alpha,
  };
}

/** Índice del último waypoint con `tMs <= elapsedMs`. Búsqueda binaria. */
export function waypointIndexAt(path: readonly Waypoint[], elapsedMs: number): number {
  if (path.length === 0) return -1;
  if (elapsedMs <= 0) return 0;
  const last = path[path.length - 1]!;
  if (elapsedMs >= last.tMs) return path.length - 1;

  let lo = 0;
  let hi = path.length - 1;
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2);
    if (path[mid]!.tMs <= elapsedMs) lo = mid;
    else hi = mid - 1;
  }
  return lo;
}

/**
 * Reconciliación suave hacia la verdad del servidor.
 *
 * Cuando el servidor corrige la posición de una unidad, teletransportarla sería
 * feo y desconcertante. Se acerca gradualmente... salvo si la divergencia es
 * grande, en cuyo caso arrastrarla lentamente sería peor mentira todavía: ahí sí
 * se salta de golpe.
 *
 * @param rate fracción de la diferencia que se corrige por frame (0..1)
 * @param snapThreshold divergencia en tiles a partir de la cual se salta
 */
export function reconcile(
  current: RenderPosition,
  authoritative: RenderPosition,
  rate: number,
  snapThreshold = 2,
): RenderPosition {
  const dx = authoritative.x - current.x;
  const dy = authoritative.y - current.y;

  if (Math.abs(dx) > snapThreshold || Math.abs(dy) > snapThreshold) {
    return authoritative;
  }
  const k = Math.min(Math.max(rate, 0), 1);
  return { x: current.x + dx * k, y: current.y + dy * k };
}

/** Indica si el movimiento ya terminó en el instante dado. */
export function hasArrived(movement: ActiveMovement, serverTimeMs: number): boolean {
  return serverTimeMs >= movement.arrivalTimeMs;
}

/** Milisegundos que faltan para llegar; 0 si ya llegó. */
export function remainingMs(movement: ActiveMovement, serverTimeMs: number): number {
  return Math.max(0, movement.arrivalTimeMs - serverTimeMs);
}
