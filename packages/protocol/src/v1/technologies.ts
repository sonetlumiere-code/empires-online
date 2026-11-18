/**
 * Catálogo de presentación de las tecnologías (SPEC-TECHNOLOGIES §6, ADR-046 §1).
 *
 * Es lo que el panel «Tecnologías» enseña: el nombre, la era, el coste, el plazo
 * base, los requisitos y los efectos de cada una. NO es la fuente: ésa es la tabla
 * `technologies` (migración 000027), y el servidor decide sin mirar este fichero.
 * Se exporta a `technologies.json` junto a los JSON Schema y un test de
 * integración del Game Server comprueba que coincide con la tabla, para que una
 * migración de balance que no lo actualice rompa la CI en lugar de mentir en el
 * panel.
 */
import { CivilizationCode, EraCode } from './common.js';
import { CIVILIZATION_TRAIT_KEYS } from './civilizations.js';

/**
 * Claves de los efectos: las de los rasgos de civilización más las dos que sólo
 * llevan las tecnologías (SPEC-TECHNOLOGIES §4.3).
 */
export const TECHNOLOGY_EFFECT_KEYS = [
  ...CIVILIZATION_TRAIT_KEYS,
  'combat.attack_damage',
  'economy.gather_carry',
] as const;
export type TechnologyEffectKey = (typeof TECHNOLOGY_EFFECT_KEYS)[number];

/** Los recursos que cobra como coste fijo: madera y piedra. */
export type TechnologyFixedResource = 'WOOD' | 'STONE';

export interface TechnologyInfo {
  readonly code: string;
  readonly name: string;
  readonly era: EraCode;
  /** La civilización de una exclusiva; `null` en una común (SPEC-TECHNOLOGIES Q3). */
  readonly civilization: CivilizationCode | null;
  readonly costs: Readonly<Partial<Record<TechnologyFixedResource, number>>>;
  /** Bienes de otras civilizaciones, en cualquier mezcla; 0 si ninguno. */
  readonly foreignGoods: number;
  /** Bien de la civilización propia; sólo lo pide una exclusiva. */
  readonly ownGood: number;
  /** Plazo base, antes del rasgo `production.speed`. */
  readonly durationMs: number;
  readonly requires: readonly string[];
  /** Enteros por mil: `1100` es +10 %. */
  readonly effects: Readonly<Partial<Record<TechnologyEffectKey, number>>>;
}

/** En el orden de la tabla (por `id`). */
export const TECHNOLOGIES: readonly TechnologyInfo[] = [
  {
    code: 'STONE_TOOLS', name: 'Herramientas de piedra', era: 'STONE_AGE', civilization: null,
    costs: { WOOD: 150, STONE: 50 }, foreignGoods: 0, ownGood: 0, durationMs: 60_000, requires: [],
    effects: { 'economy.gather_speed': 1100 },
  },
  {
    code: 'BASKETRY', name: 'Cestería', era: 'STONE_AGE', civilization: null,
    costs: { WOOD: 120, STONE: 30 }, foreignGoods: 0, ownGood: 0, durationMs: 60_000, requires: [],
    effects: { 'economy.gather_carry': 1200 },
  },
  {
    code: 'BRONZE_WEAPONS', name: 'Armas de bronce', era: 'BRONZE_AGE', civilization: null,
    costs: { WOOD: 250, STONE: 150 }, foreignGoods: 0, ownGood: 0, durationMs: 120_000, requires: [],
    effects: { 'combat.attack_damage': 1100 },
  },
  {
    code: 'MASONRY', name: 'Albañilería', era: 'BRONZE_AGE', civilization: null,
    costs: { WOOD: 200, STONE: 200 }, foreignGoods: 0, ownGood: 0, durationMs: 120_000, requires: [],
    effects: { 'building.house_hp': 1200 },
  },
  {
    code: 'WHEEL', name: 'La rueda', era: 'IRON_AGE', civilization: null,
    costs: { WOOD: 300, STONE: 200 }, foreignGoods: 30, ownGood: 0, durationMs: 180_000, requires: [],
    effects: { 'unit.move_speed': 1050 },
  },
  {
    code: 'GUILDS', name: 'Gremios', era: 'IRON_AGE', civilization: null,
    costs: { WOOD: 300, STONE: 300 }, foreignGoods: 30, ownGood: 0, durationMs: 180_000, requires: [],
    effects: { 'production.speed': 1100 },
  },
  {
    code: 'IRON_WEAPONS', name: 'Armas de hierro', era: 'CASTLE_AGE', civilization: null,
    costs: { WOOD: 400, STONE: 300 }, foreignGoods: 60, ownGood: 0, durationMs: 300_000, requires: ['BRONZE_WEAPONS'],
    effects: { 'combat.attack_damage': 1100 },
  },
  {
    code: 'ARCHITECTURE', name: 'Arquitectura', era: 'CASTLE_AGE', civilization: null,
    costs: { WOOD: 400, STONE: 400 }, foreignGoods: 60, ownGood: 0, durationMs: 300_000, requires: ['GUILDS'],
    effects: { 'production.speed': 1100 },
  },
  {
    code: 'ROMAN_ROADS', name: 'Calzadas', era: 'IRON_AGE', civilization: 'ROMAN',
    costs: { WOOD: 200, STONE: 200 }, foreignGoods: 0, ownGood: 40, durationMs: 180_000, requires: [],
    effects: { 'unit.move_speed': 1100 },
  },
  {
    code: 'THEODOSIAN_WALLS', name: 'Muros de Teodosio', era: 'IRON_AGE', civilization: 'BYZANTINE',
    costs: { WOOD: 200, STONE: 200 }, foreignGoods: 0, ownGood: 40, durationMs: 180_000, requires: [],
    effects: { 'building.house_hp': 1200 },
  },
  {
    code: 'CARAVANSERAI', name: 'Caravasares', era: 'IRON_AGE', civilization: 'PERSIAN',
    costs: { WOOD: 200, STONE: 200 }, foreignGoods: 0, ownGood: 40, durationMs: 180_000, requires: [],
    effects: { 'economy.gather_carry': 1200 },
  },
  {
    code: 'RAIDING', name: 'Incursiones', era: 'IRON_AGE', civilization: 'NORSE',
    costs: { WOOD: 200, STONE: 200 }, foreignGoods: 0, ownGood: 40, durationMs: 180_000, requires: [],
    effects: { 'combat.loot_carry': 1200 },
  },
];
