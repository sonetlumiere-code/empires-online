/**
 * El árbol tecnológico en el cliente (SPEC-TECHNOLOGIES §4.7).
 *
 * El catálogo de presentación viene de `@empires-online/protocol`
 * (`technologies.json`, ADR-046 §1): nombres, eras, costes, plazos, requisitos y
 * efectos. Lo que aquí se calcula —qué puede investigar la ciudad, su motivo si
 * no, su coste en palabras y su plazo con el rasgo— es sólo para el panel: el
 * servidor decide (INV-SEC-001) y responde con el motivo de un rechazo.
 */
import { v1 } from '@empires-online/protocol';

import { CIVILIZATIONS, foreignGoods, goodLabel, goodOf, traitPercent } from './civilizations';
import { eraLabel } from './economy';
import { listJoin } from './units';
import type { CityView } from './world';

export const TECHNOLOGIES: readonly v1.TechnologyInfo[] = v1.TECHNOLOGIES;

const ERA_ORDER = ['STONE_AGE', 'BRONZE_AGE', 'IRON_AGE', 'CASTLE_AGE'] as const;

function eraRank(era: string): number {
  return ERA_ORDER.indexOf(era as (typeof ERA_ORDER)[number]);
}

/** La tecnología de un código, o `undefined`. */
export function technologyOf(code: string): v1.TechnologyInfo | undefined {
  return TECHNOLOGIES.find((t) => t.code === code);
}

/** El nombre de una tecnología («Gremios»), o el código si no se conoce. */
export function technologyName(code: string): string {
  return technologyOf(code)?.name ?? code;
}

const EFFECT_LABEL: Readonly<Record<v1.TechnologyEffectKey, string>> = {
  'unit.move_speed': 'velocidad de las unidades',
  'production.speed': 'velocidad de obras y entrenamiento',
  'building.house_hp': 'vida de las casas',
  'combat.loot_carry': 'botín por unidad de combate',
  'economy.gather_speed': 'velocidad de recolección',
  'combat.attack_damage': 'daño en cada golpe',
  'economy.gather_carry': 'carga de cada aldeano',
};

/** Sus efectos en palabras: «+10 % de daño en cada golpe». */
export function effectText(t: v1.TechnologyInfo): string {
  const parts: string[] = [];
  for (const key of v1.TECHNOLOGY_EFFECT_KEYS) {
    const v = t.effects[key];
    if (v === undefined) continue;
    parts.push(`${traitPercent(v)} de ${EFFECT_LABEL[key]}`);
  }
  return listJoin(parts);
}

/** Su coste en palabras: «300 de madera, 200 de piedra y 30 bienes de otras civilizaciones». */
export function costText(t: v1.TechnologyInfo, civilization: string): string {
  const parts: string[] = [];
  if (t.costs.WOOD) parts.push(`${t.costs.WOOD} de madera`);
  if (t.costs.STONE) parts.push(`${t.costs.STONE} de piedra`);
  if (t.foreignGoods > 0) parts.push(`${t.foreignGoods} bienes de otras civilizaciones`);
  const good = goodOf(civilization);
  if (t.ownGood > 0 && good) parts.push(`${t.ownGood} de ${goodLabel(good)}`);
  return listJoin(parts);
}

/**
 * El rasgo `production.speed` efectivo de un jugador: el de su civilización
 * compuesto con el de cada tecnología que tiene, en orden de código, con la misma
 * fórmula que el servidor (SPEC-TECHNOLOGIES §4.4).
 */
export function productionSpeed(civilization: string, owned: readonly string[]): number {
  let r = CIVILIZATIONS.find((c) => c.code === civilization)?.traits['production.speed'] ?? 1000;
  for (const code of [...owned].sort()) {
    const e = technologyOf(code)?.effects['production.speed'];
    if (e !== undefined) r = Math.max(1, Math.floor((r * e + 500) / 1000));
  }
  return r;
}

/** Lo que tardaría en segundos, con el rasgo y las tecnologías que lo aceleran. */
export function researchSeconds(t: v1.TechnologyInfo, civilization: string, owned: readonly string[]): number {
  const r = productionSpeed(civilization, owned);
  const ms = Math.max(1, Math.floor((t.durationMs * 1000 + Math.floor(r / 2)) / r));
  return Math.round(ms / 1000);
}

/**
 * Las que una civilización puede llegar a tener, agrupadas por era: las comunes y
 * su exclusiva (SPEC-TECHNOLOGIES Q3).
 */
export function technologiesFor(civilization: string): { era: string; technologies: v1.TechnologyInfo[] }[] {
  return ERA_ORDER.map((era) => ({
    era,
    technologies: TECHNOLOGIES.filter(
      (t) => t.era === era && (t.civilization === null || t.civilization === civilization),
    ),
  })).filter((g) => g.technologies.length > 0);
}

/**
 * Por qué no se puede investigar ahora, o `null` si se puede intentar (RN-TECH-002 a
 * RN-TECH-004): la era, los requisitos, otra investigación o un avance en curso y el
 * almacén. Una que ya tiene no lleva botón: la marca el panel.
 */
export function researchBlockReason(
  city: Pick<CityView, 'era' | 'civilization' | 'stocks' | 'advance' | 'research' | 'technologies'>,
  t: v1.TechnologyInfo,
): string | null {
  if (t.civilization !== null && t.civilization !== city.civilization) return 'Es de otra civilización.';
  if (city.technologies.includes(t.code)) return 'Ya la tienes.';
  if (eraRank(city.era) < eraRank(t.era)) return `Desde la ${eraLabel(t.era)}.`;
  const falta = t.requires.filter((r) => !city.technologies.includes(r));
  if (falta.length > 0) return `Pide ${listJoin(falta.map(technologyName))}.`;
  if (city.research !== null) return `Ya se investiga ${technologyName(city.research.technology)}.`;
  if (city.advance !== null) return 'La ciudad está avanzando de edad.';
  const faltan: string[] = [];
  if ((t.costs.WOOD ?? 0) > city.stocks.WOOD) faltan.push(`${(t.costs.WOOD ?? 0) - city.stocks.WOOD} de madera`);
  if ((t.costs.STONE ?? 0) > city.stocks.STONE) faltan.push(`${(t.costs.STONE ?? 0) - city.stocks.STONE} de piedra`);
  const good = goodOf(city.civilization);
  if (t.ownGood > 0 && good) {
    const tiene = city.stocks[good] ?? 0;
    if (tiene < t.ownGood) faltan.push(`${t.ownGood - tiene} de ${goodLabel(good)}`);
  }
  const ajenos = t.foreignGoods - foreignGoods(city.stocks, city.civilization);
  if (t.foreignGoods > 0 && ajenos > 0) faltan.push(`${ajenos} bienes de otras civilizaciones`);
  return faltan.length > 0 ? `Faltan ${listJoin(faltan)}.` : null;
}
