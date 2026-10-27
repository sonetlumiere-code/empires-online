/**
 * Selectores del HUD: lo que React necesita del mundo, por valor.
 *
 * Los mapas del store no se copian por delta, así que suscribirse a `s.units`
 * no re-renderizaría nunca; y suscribirse a `changes` re-renderizaría con el
 * delta de cualquier unidad. El selector memorizado devuelve el MISMO objeto
 * mientras lo que el HUD muestra no cambie, y Zustand (`Object.is`) no vuelve
 * a renderizar. Recorre las unidades en cada cambio del store, sin reservar
 * memoria salvo cuando el resultado cambia: son comparaciones, no renders.
 *
 * Ver ../../../../docs/architecture/frontend.md §7.6
 */
import type { CityView, UnitView, WorldState } from './world';

export interface HudView {
  /** Unidades propias en el área de interés, guarnecidas incluidas. */
  readonly ownUnitCount: number;
  /** Unidades propias guarnecidas, en el orden del store. */
  readonly garrisoned: readonly UnitView[];
  /** La ciudad propia si está en el área; `null` si no. */
  readonly ownCity: CityView | null;
}

export type HudState = Pick<WorldState, 'playerId' | 'units' | 'cities'>;

/** Calcula la vista del HUD. Reserva un objeto; por eso se memoriza con `createHudSelector`. */
export function computeHud(state: HudState): HudView {
  const ownId = state.playerId;
  let ownUnitCount = 0;
  const garrisoned: UnitView[] = [];
  for (const u of state.units.values()) {
    if (u.playerId !== ownId) continue;
    ownUnitCount++;
    if (u.garrisonCityId !== null) garrisoned.push(u);
  }

  let ownCity: CityView | null = null;
  for (const c of state.cities.values()) {
    if (c.ownerPlayerId === ownId) {
      ownCity = c;
      break;
    }
  }

  return { ownUnitCount, garrisoned, ownCity };
}

/**
 * Igualdad por valor. Las unidades y la ciudad son objetos inmutables que el
 * store reemplaza enteros al cambiar, así que compararlos por identidad es
 * compararlos por valor.
 */
export function hudEquals(a: HudView, b: HudView): boolean {
  if (a.ownUnitCount !== b.ownUnitCount || a.ownCity !== b.ownCity) return false;
  if (a.garrisoned.length !== b.garrisoned.length) return false;
  for (let i = 0; i < a.garrisoned.length; i++) {
    if (a.garrisoned[i] !== b.garrisoned[i]) return false;
  }
  return true;
}

/** Un selector que devuelve la misma referencia mientras la vista no cambie por valor. */
export function createHudSelector(): (state: HudState) => HudView {
  let last: HudView | null = null;
  return (state) => {
    const next = computeHud(state);
    if (last && hudEquals(last, next)) return last;
    last = next;
    return next;
  };
}
