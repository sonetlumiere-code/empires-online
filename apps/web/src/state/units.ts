/**
 * Las unidades en el cliente (SPEC-CIVILIZATION-UNITS §4.6): su nombre, si atacan,
 * cuál es la de cada civilización y por qué no se puede entrenar la propia.
 *
 * El catálogo es el de presentación de `@empires-online/protocol` (`v1.UNITS`), que
 * un test del Game Server ata campo a campo al catálogo de Go (ADR-043 §7). El
 * cliente no decide nada: explica, y el servidor responde INVALID_TARGET o
 * INSUFFICIENT_RESOURCES si no se puede.
 * Ver ../../../../docs/specs/civilization-units.md
 */
import { v1 } from '@empires-online/protocol';
import { civilizationLabel, goodLabel, goodOf } from './civilizations';

/** Singular y plural en minúscula, para las frases del HUD y del registro. */
const NAMES: Readonly<Record<string, readonly [string, string]>> = {
  VILLAGER: ['aldeano', 'aldeanos'],
  WARRIOR: ['guerrero', 'guerreros'],
  CARAVAN: ['caravana', 'caravanas'],
  LEGIONARY: ['legionario', 'legionarios'],
  CATAPHRACT: ['catafracto', 'catafractos'],
  HORSE_ARCHER: ['arquero a caballo', 'arqueros a caballo'],
  BERSERKER: ['berserker', 'berserkers'],
};

/** La identidad de cada unidad exclusiva en una línea (SPEC-CIVILIZATION-UNITS §4.2). */
const IDENTITY: Readonly<Record<string, string>> = {
  LEGIONARY: 'aguanta casi el doble que un guerrero',
  CATAPHRACT: 'el más rápido: alcanza y persigue',
  HORSE_ARCHER: 'golpea a tres casillas',
  BERSERKER: 'pega casi el doble y carga más botín',
};

/** La ficha de presentación de un tipo, o `undefined` si no se conoce. */
export function unitInfoOf(type: string): v1.UnitInfo | undefined {
  return v1.UNITS.find((u) => u.code === type);
}

/** «legionario»; un tipo desconocido, en minúscula. */
export function unitLabel(type: string): string {
  return NAMES[type]?.[0] ?? type.toLowerCase();
}

/** «legionarios», «arqueros a caballo». */
export function unitPlural(type: string): string {
  const known = NAMES[type]?.[1];
  if (known) return known;
  const word = type.toLowerCase();
  return /[aeiou]$/.test(word) ? `${word}s` : `${word}es`;
}

/** «Legionario». */
export function unitName(type: string): string {
  return unitInfoOf(type)?.name ?? type;
}

/**
 * Si el tipo ataca: ataca, responde, saquea, asedia, reclama y captura
 * (RN-CIVUNIT-005). Es la pregunta del cliente en lugar de «¿es un guerrero?».
 */
export function unitAttacks(type: string): boolean {
  return unitInfoOf(type)?.attacks ?? false;
}

/** La unidad exclusiva de una civilización, o `null` si no se conoce. */
export function civilizationUnit(code: string): v1.TrainableUnitType | null {
  return v1.CIVILIZATIONS.find((c) => c.code === code)?.unit ?? null;
}

/** «Unidad propia: legionario, aguanta casi el doble que un guerrero», para el registro. */
export function civilizationUnitLine(code: string): string | null {
  const unit = civilizationUnit(code);
  if (!unit) return null;
  return `Unidad propia: ${unitLabel(unit)}, ${IDENTITY[unit] ?? ''}`.replace(/, $/, '');
}

/** «madera», «piedra», «vino». */
export function resourceLabel(r: string): string {
  return r === 'WOOD' ? 'madera' : r === 'STONE' ? 'piedra' : goodLabel(r);
}

/** «a, b y c». */
export function listJoin(parts: readonly string[]): string {
  return parts.length > 1 ? `${parts.slice(0, -1).join(', ')} y ${parts[parts.length - 1]}` : (parts[0] ?? '');
}

/** «80 de madera, 40 de piedra y 10 de vino». */
export function trainCostText(type: string, civilization: string): string {
  const u = unitInfoOf(type);
  if (!u) return '';
  const parts = u.trainCosts.map((c) => `${c.amount} de ${resourceLabel(c.resource)}`);
  const good = goodOf(civilization);
  if (u.ownGoodCost > 0 && good) parts.push(`${u.ownGoodCost} de ${goodLabel(good)}`);
  return listJoin(parts);
}

/**
 * Cómo se llama una unidad en el panel de selección: «Legionario · Romanos» para
 * una de civilización (§4.6), «guerrero» para las comunes.
 */
export function unitTitle(type: string): string {
  const civ = v1.CIVILIZATIONS.find((c) => c.unit === type);
  return civ ? `${unitName(type)} · ${civilizationLabel(civ.code)}` : unitLabel(type);
}
