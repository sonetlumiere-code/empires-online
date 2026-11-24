/**
 * La caída de una ciudad en el cliente (SPEC-CITY-FALL): a qué ciudad ajena se
 * puede ofrecer el asalto con la unidad seleccionada, si se puede y, si no, por qué;
 * y cómo se lee un asalto en curso.
 *
 * Como el resto del estado, no decide nada: el servidor valida la orden y manda el
 * asalto en `CityView.assault`. Esto sólo evita ofrecer un botón que el servidor va a
 * rechazar, con el mismo motivo que daría él.
 * Ver ../../../../docs/specs/city-fall.md
 */
import { formatCountdown, type TreatyView } from './diplomacy';
import { orderPeace } from './factions';
import { isSiegeEngine, unitAttacks } from './units';
import type { BuildingView, CityView, TerritoryView, UnitView } from './world';

/** A cuántos tiles del centro de una ciudad ajena se ofrece «Asaltar». */
export const ASSAULT_OFFER_TILES = 12;

/** La ciudad ajena más cercana a la unidad, a `ASSAULT_OFFER_TILES` o menos; empate: menor id. */
export function assaultTargetNear(cities: Iterable<CityView>, unit: UnitView | undefined, playerId: string | null): CityView | undefined {
  if (!unit) return undefined;
  let best: CityView | undefined;
  let bestD = Infinity;
  for (const c of cities) {
    if (c.ownerPlayerId === playerId) continue;
    const d = Math.max(Math.abs(unit.x - c.centerX), Math.abs(unit.y - c.centerY));
    if (d > ASSAULT_OFFER_TILES) continue;
    if (d < bestD || (d === bestD && best && c.id < best.id)) {
      best = c;
      bestD = d;
    }
  }
  return best;
}

export interface AssaultCheck {
  unit: UnitView | undefined;
  playerId: string | null;
  city: CityView;
  /** La ciudad propia, para la paz de la Orden y la protección propia. */
  ownCity: CityView | undefined;
  buildings: Iterable<BuildingView>;
  treaties: readonly TreatyView[];
  nowMs: number;
}

/** Si la ciudad tiene su muralla completa en pie: está amurallada (RN-SIEGE-002). */
export function walled(city: CityView, buildings: Iterable<BuildingView>): boolean {
  for (const b of buildings) {
    if (b.cityId === city.id && b.type === 'WALL' && b.status === 'COMPLETE') return true;
  }
  return false;
}

function isProtected(c: CityView, nowMs: number): boolean {
  return c.presenceState === 'PROTECTED' && (c.protectionUntilMs === null || c.protectionUntilMs > nowMs);
}

/** «2 d 4 h», «3 h 10 min» o «12 min»: lo que falta de una inmunidad. */
export function formatLongCountdown(ms: number): string {
  const min = Math.max(1, Math.ceil(ms / 60_000));
  const d = Math.floor(min / 1440);
  const h = Math.floor((min % 1440) / 60);
  const m = min % 60;
  if (d > 0) return `${d} d ${h} h`;
  if (h > 0) return `${h} h ${m} min`;
  return `${m} min`;
}

/**
 * Por qué no se puede asaltar la ciudad con la unidad seleccionada, o `null` si se puede
 * (RN-SIEGE-009, RN-SIEGE-010, RN-CITY-029, RN-COMBAT-041). La paz del clan y una zona
 * segura las decide el servidor.
 */
export function assaultBlockReason(c: AssaultCheck): string | null {
  const { unit, playerId, city } = c;
  if (!unit || unit.playerId !== playerId) return 'Selecciona una unidad tuya';
  if (!unitAttacks(unit.unitType) || isSiegeEngine(unit.unitType)) return 'Sólo un guerrero o tu unidad propia asaltan ciudades';
  if (unit.status === 'GARRISONED' || unit.status === 'DEAD') return 'La unidad tiene que estar en el mapa';
  if (city.ownerPlayerId === playerId) return 'Es tu ciudad';
  if (walled(city, c.buildings)) return 'La muralla está en pie: primero hay que abrirla con arietes';
  if (city.era === 'STONE_AGE') return 'Una ciudad de la Edad de Piedra no puede caer';
  if (city.fallImmuneUntilMs !== undefined && city.fallImmuneUntilMs > c.nowMs) {
    return `Ciudad recién fundada: se podrá asaltar en ${formatLongCountdown(city.fallImmuneUntilMs - c.nowMs)}`;
  }
  if (city.assault) return 'Ya hay un asalto sobre esa ciudad';
  if (isProtected(city, c.nowMs)) return 'Esa ciudad está protegida';
  if (c.ownCity && isProtected(c.ownCity, c.nowMs)) return 'Estás protegido: no puedes asaltar ciudades';
  if (c.ownCity && orderPeace(c.ownCity.faction, city.faction)) return 'La paz de la Orden lo impide';
  const peace = c.treaties.some(
    (tr) => tr.otherPlayerId === city.ownerPlayerId && tr.status === 'ACTIVE' && (tr.type === 'NON_AGGRESSION' || tr.type === 'ALLIANCE'),
  );
  if (peace) return 'Un tratado de paz con su dueño lo impide';
  return null;
}

/** El asalto en curso de una ciudad, para el HUD; `null` si no lo hay. */
export function assaultLabel(city: CityView, playerId: string | null, nowMs: number): string | null {
  const a = city.assault;
  if (!a) return null;
  const left = formatCountdown(a.endsAtMs - nowMs);
  if (city.ownerPlayerId === playerId) return `¡Están asaltando tu ciudad! Cae en ${left}`;
  if (a.playerId === playerId) return `Asaltas ${city.name}: cae en ${left}`;
  return `${city.name} está bajo asalto: cae en ${left}`;
}

/** Si alguna ciudad cargada está bajo asalto: el HUD necesita refrescar su cuenta atrás. */
export function anyAssault(cities: Iterable<CityView>): boolean {
  for (const c of cities) if (c.assault) return true;
  return false;
}

// ─────────────────────────────────────────────────────────────
// El asalto en el mapa (SPEC-ASSAULT-MAP)
// ─────────────────────────────────────────────────────────────

/**
 * El tono de una marca del asalto según quién mira (RN-SIEGE-014): `loss` para el que
 * pierde (rojo), `own` para lo propio (dorado) y `other` para un tercero (naranja).
 */
export type MarkTone = 'loss' | 'own' | 'other';

/** El tono de una ciudad bajo asalto para quien mira; `null` si no lo está (RN-SIEGE-012). */
export function assaultTone(city: CityView, playerId: string | null): MarkTone | null {
  const a = city.assault;
  if (!a) return null;
  if (city.ownerPlayerId === playerId) return 'loss';
  if (a.playerId === playerId) return 'own';
  return 'other';
}

function inside(t: TerritoryView, x: number, y: number): boolean {
  return x >= t.minX && x <= t.maxX && y >= t.minY && y <= t.maxY;
}

/**
 * El tono del estandarte de una unidad en `CLAIMING`, o `null` si no lo sostiene
 * (RN-SIEGE-013). El del asalto es el de su ciudad; el de una reclamación de
 * territorio, el de la unidad, salvo que reclame un territorio de quien mira: está
 * dentro y la reclamación es de su dueño, porque `claim` no trae la unidad.
 */
export function standardTone(
  unit: UnitView,
  playerId: string | null,
  cities: Iterable<CityView>,
  territories: Iterable<TerritoryView>,
): MarkTone | null {
  if (unit.status !== 'CLAIMING') return null;
  for (const c of cities) {
    if (c.assault?.unitId === unit.id) return assaultTone(c, playerId);
  }
  if (unit.playerId === playerId) return 'own';
  for (const t of territories) {
    if (t.ownerType !== 'PLAYER' || t.ownerId !== playerId || t.claim?.playerId !== unit.playerId) continue;
    if (inside(t, unit.x, unit.y)) return 'loss';
  }
  return 'other';
}

/** La línea bajo el nombre de una ciudad bajo asalto: «Cae en 14:32» o, a cero, «Cayendo…» (Q8). */
export function assaultCountdownText(endsAtMs: number, nowMs: number): string {
  return endsAtMs - nowMs <= 0 ? 'Cayendo…' : `Cae en ${formatCountdown(endsAtMs - nowMs)}`;
}
