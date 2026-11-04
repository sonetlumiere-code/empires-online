/**
 * La conquista en el cliente (SPEC-TERRITORY-CONQUEST): en qué territorio está el
 * guerrero seleccionado, de quién es, si se puede reclamar y, si no, por qué; y
 * cómo se lee una reclamación en curso.
 *
 * Como el resto del estado, no decide nada: el servidor valida la orden y manda la
 * reclamación en `TerritoryView.claim`. Esto sólo evita ofrecer un botón que el
 * servidor va a rechazar, con el mismo motivo que daría él.
 * Ver ../../../../docs/specs/territory-conquest.md
 */
import { formatCountdown, type TreatyView } from './diplomacy';
import type { CityView, TerritoryView, UnitView } from './world';

/** El territorio que contiene el tile, si hay alguno cargado (RN-TERR-001: límites inclusivos). */
export function territoryAt(territories: Iterable<TerritoryView>, x: number, y: number): TerritoryView | undefined {
  for (const t of territories) {
    if (x >= t.minX && x <= t.maxX && y >= t.minY && y <= t.maxY) return t;
  }
  return undefined;
}

/** La ciudad de un jugador entre las cargadas. */
function cityOf(cities: Iterable<CityView>, playerId: string): CityView | undefined {
  for (const c of cities) if (c.ownerPlayerId === playerId) return c;
  return undefined;
}

function contains(t: TerritoryView, x: number, y: number): boolean {
  return x >= t.minX && x <= t.maxX && y >= t.minY && y <= t.maxY;
}

/** De quién es, para el HUD: «libre», «tuyo» o la ciudad de su dueño. */
export function territoryOwnerLabel(t: TerritoryView, playerId: string | null, cities: Iterable<CityView>): string {
  if (t.ownerType !== 'PLAYER' || t.ownerId === null) return 'libre';
  if (t.ownerId === playerId) return 'tuyo';
  return `de ${cityOf(cities, t.ownerId)?.name ?? 'otro jugador'}`;
}

export interface ClaimCheck {
  unit: UnitView | undefined;
  playerId: string | null;
  territory: TerritoryView | undefined;
  cities: Iterable<CityView>;
  treaties: readonly TreatyView[];
  nowMs: number;
}

/**
 * Por qué no se puede reclamar con el guerrero seleccionado, o `null` si se puede
 * (RN-CONQ-001 a 003, Q4, Q5, Q7). Lo que el cliente no sabe —una zona segura, la
 * protección de una ciudad que no tiene cargada— lo dice el servidor.
 */
export function claimBlockReason(c: ClaimCheck): string | null {
  const { unit, playerId, territory: t } = c;
  if (!unit || unit.playerId !== playerId) return 'Selecciona un guerrero tuyo';
  if (unit.unitType !== 'WARRIOR') return 'Sólo un guerrero reclama territorios';
  if (unit.status === 'GARRISONED' || unit.status === 'DEAD') return 'El guerrero tiene que estar en el mapa';
  if (unit.status === 'CLAIMING') return 'Ya está reclamando este territorio';
  if (!t) return 'Aquí no hay ningún territorio';
  if (t.claim) return 'Ya hay una reclamación en este territorio';
  if (t.ownerType === 'PLAYER' && t.ownerId !== null) {
    const owner = t.ownerId;
    if (owner === playerId) return 'Este territorio ya es tuyo';
    const ownerCity = cityOf(c.cities, owner);
    if (ownerCity && contains(t, ownerCity.centerX, ownerCity.centerY)) {
      return `Es la capital de ${ownerCity.name}: no se puede reclamar`;
    }
    const peace = c.treaties.some(
      (tr) => tr.otherPlayerId === owner && tr.status === 'ACTIVE' && (tr.type === 'NON_AGGRESSION' || tr.type === 'ALLIANCE'),
    );
    if (peace) return 'Un tratado de paz con su dueño lo impide';
    if (ownerCity && isProtected(ownerCity, c.nowMs)) return 'Su dueño está protegido';
  }
  for (const city of c.cities) {
    if (Math.abs(unit.x - city.centerX) <= 1 && Math.abs(unit.y - city.centerY) <= 1) {
      return 'No se reclama dentro de una ciudad';
    }
  }
  return null;
}

function isProtected(c: CityView, nowMs: number): boolean {
  return c.presenceState === 'PROTECTED' && (c.protectionUntilMs === null || c.protectionUntilMs > nowMs);
}

/** La reclamación en curso de un territorio, para el HUD; `null` si no la hay. */
export function claimLabel(t: TerritoryView, playerId: string | null, nowMs: number): string | null {
  const claim = t.claim;
  if (!claim) return null;
  const left = formatCountdown(claim.endsAtMs - nowMs);
  if (claim.playerId === playerId) return `Reclamas ${t.name}: falta ${left}`;
  const who = claim.cityName ?? 'otro jugador';
  if (t.ownerId === playerId) return `¡${who} reclama tu territorio ${t.name}! Falta ${left}`;
  return `${who} reclama ${t.name}: falta ${left}`;
}

/** Si algún territorio cargado está en disputa: el HUD necesita refrescar su cuenta atrás. */
export function anyClaim(territories: Iterable<TerritoryView>): boolean {
  for (const t of territories) if (t.claim) return true;
  return false;
}
