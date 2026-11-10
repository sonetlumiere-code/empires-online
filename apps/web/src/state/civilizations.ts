/**
 * Las civilizaciones en el cliente (SPEC-CIVILIZATION): su nombre y lo que da y
 * cuesta cada una, para el formulario de registro, el HUD y el panel de una
 * ciudad ajena.
 *
 * No aplica ningún rasgo: los plazos, la vida de las casas y las cargas llegan
 * ya resueltos del servidor (ADR-035 §7). El catálogo es el de presentación de
 * `@empires-online/protocol`, que un test de integración del Game Server ata a
 * la tabla `civilizations`.
 * Ver ../../../../docs/specs/civilizations.md
 */
import { v1 } from '@empires-online/protocol';

export type CivilizationCode = v1.CivilizationCode;

/** Las cuatro, en el orden del catálogo. */
export const CIVILIZATIONS: readonly v1.CivilizationInfo[] = v1.CIVILIZATIONS;

/** Qué cambia cada rasgo, dicho para el jugador (SPEC-CIVILIZATION §4.2). */
const TRAIT_LABEL: Readonly<Record<v1.CivilizationTraitKey, string>> = {
  'unit.move_speed': 'velocidad de las unidades',
  'production.speed': 'velocidad de obras y entrenamiento',
  'building.house_hp': 'vida de las casas',
  'combat.loot_carry': 'botín por guerrero',
  'economy.gather_speed': 'velocidad de recolección',
};

/** El nombre de una civilización («Nórdicos»), o el código si no se conoce. */
export function civilizationLabel(code: string): string {
  return CIVILIZATIONS.find((c) => c.code === code)?.name ?? code;
}

/** Un rasgo por mil como porcentaje con signo: 1100 → «+10 %», 950 → «−5 %». */
export function traitPercent(perMille: number): string {
  const delta = (perMille - 1000) / 10;
  const n = Number.isInteger(delta) ? String(Math.abs(delta)) : Math.abs(delta).toFixed(1).replace('.', ',');
  return `${delta < 0 ? '−' : '+'}${n} %`;
}

export interface TraitLine {
  readonly text: string;
  /** Si es una ventaja (sube) o un coste (baja). */
  readonly good: boolean;
}

/**
 * Lo que da y lo que cuesta una civilización, ventajas primero y en el orden del
 * catálogo de claves (Q9). Los rasgos sin efecto (1000) no se enseñan.
 */
export function traitLines(info: v1.CivilizationInfo): TraitLine[] {
  const lines: TraitLine[] = [];
  for (const key of v1.CIVILIZATION_TRAIT_KEYS) {
    const v = info.traits[key];
    if (v === undefined || v === 1000) continue;
    lines.push({ text: `${traitPercent(v)} ${TRAIT_LABEL[key]}`, good: v > 1000 });
  }
  return [...lines.filter((l) => l.good), ...lines.filter((l) => !l.good)];
}

const GOOD_NAME: Readonly<Record<string, string>> = { WINE: 'vino', SILK: 'seda', SPICES: 'especias', FURS: 'pieles' };

/** Los bienes, en el orden del catálogo (SPEC-CIVILIZATION-GOODS §4.1). */
export const GOODS: readonly v1.GoodType[] = CIVILIZATIONS.map((c) => c.good);

/** El nombre de un bien («vino»), o el código si no se conoce. */
export function goodLabel(good: string): string {
  return GOOD_NAME[good] ?? good.toLowerCase();
}

/** El bien que elabora el taller de una civilización, o `null`. */
export function goodOf(code: string): v1.GoodType | null {
  return CIVILIZATIONS.find((c) => c.code === code)?.good ?? null;
}

/**
 * Los bienes que no son el de la civilización: los que cuentan para la Edad de
 * Hierro (RN-GOODS-006). El propio nunca cuenta.
 */
export function foreignGoods(stocks: Readonly<Record<string, number>>, code: string): number {
  const own = goodOf(code);
  return GOODS.filter((g) => g !== own).reduce((n, g) => n + (stocks[g] ?? 0), 0);
}

/** Una línea con todo, para un `title` o un texto corto. */
export function traitSummary(code: string): string {
  const info = CIVILIZATIONS.find((c) => c.code === code);
  if (!info) return '';
  return traitLines(info)
    .map((l) => l.text)
    .join(', ');
}
