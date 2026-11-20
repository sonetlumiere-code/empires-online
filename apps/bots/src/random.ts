/**
 * El aleatorio con semilla de los bots (SPEC-BOTS RN-BOT-005).
 *
 * El decisor nunca llama a `Math.random`: recibe un `Rng` que el bucle crea a
 * partir de la semilla de la ejecución y del nombre del bot. Dos ejecuciones con
 * la misma semilla toman las mismas decisiones ante el mismo estado.
 */

export interface Rng {
  /** Un número en [0, 1). */
  next(): number;
  /** Un entero en [0, n). */
  int(n: number): number;
}

/** mulberry32: pequeño, rápido y suficiente para desempates y desfases. */
export function createRng(seed: number): Rng {
  let a = seed >>> 0;
  const next = () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4_294_967_296;
  };
  return { next, int: (n) => Math.floor(next() * n) };
}

/** Una semilla derivada para cada bot: la de la ejecución mezclada con su nombre (FNV-1a). */
export function seedFor(runSeed: number, name: string): number {
  let h = (2_166_136_261 ^ runSeed) >>> 0;
  for (let i = 0; i < name.length; i++) {
    h ^= name.charCodeAt(i);
    h = Math.imul(h, 16_777_619) >>> 0;
  }
  return h;
}
