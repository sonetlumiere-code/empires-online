/**
 * Catálogo de presentación de las civilizaciones (SPEC-CIVILIZATION §6, ADR-035 §7).
 *
 * Es lo que el formulario de registro enseña antes de que haya sesión: el nombre
 * y los rasgos de cada civilización. NO es la fuente de los rasgos: ésa es la
 * tabla `civilizations` (migración 000015), y el servidor los aplica sin mirar
 * este fichero. Se exporta a `civilizations.json` junto a los JSON Schema y un
 * test de integración del Game Server comprueba que coincide con la tabla, para
 * que una migración de balance que no lo actualice rompa la CI en lugar de
 * mentir en el formulario.
 */
import { CivilizationCode, GoodType, TrainableUnitType } from './common.js';

/** Rasgos que aplica el servidor. Enteros por mil: `1000` es «sin efecto» (RN-CIV-003). */
export const CIVILIZATION_TRAIT_KEYS = [
  'unit.move_speed',
  'production.speed',
  'building.house_hp',
  'combat.loot_carry',
  'economy.gather_speed',
] as const;
export type CivilizationTraitKey = (typeof CIVILIZATION_TRAIT_KEYS)[number];

export interface CivilizationInfo {
  readonly code: CivilizationCode;
  readonly name: string;
  /** El bien que sólo elabora su taller (SPEC-CIVILIZATION-GOODS §4.1). */
  readonly good: GoodType;
  /** Su unidad exclusiva, la única que sólo ella entrena (SPEC-CIVILIZATION-UNITS §4.1). */
  readonly unit: TrainableUnitType;
  /** Sólo las claves que la civilización declara; una ausente vale 1000. */
  readonly traits: Readonly<Partial<Record<CivilizationTraitKey, number>>>;
}

/** En el orden de la tabla (por `id`). */
export const CIVILIZATIONS: readonly CivilizationInfo[] = [
  { code: 'ROMAN', name: 'Romanos', good: 'WINE', unit: 'LEGIONARY', traits: { 'unit.move_speed': 1000, 'production.speed': 1100 } },
  {
    code: 'BYZANTINE',
    name: 'Bizantinos',
    good: 'SILK',
    unit: 'CATAPHRACT',
    traits: { 'unit.move_speed': 950, 'production.speed': 1000, 'building.house_hp': 1200 },
  },
  {
    code: 'PERSIAN',
    name: 'Persas',
    good: 'SPICES',
    unit: 'HORSE_ARCHER',
    traits: { 'unit.move_speed': 1050, 'production.speed': 950, 'economy.gather_speed': 1100 },
  },
  {
    code: 'NORSE',
    name: 'Nórdicos',
    good: 'FURS',
    unit: 'BERSERKER',
    traits: { 'unit.move_speed': 1100, 'production.speed': 900, 'combat.loot_carry': 1150 },
  },
];
