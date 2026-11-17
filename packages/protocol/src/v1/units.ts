/**
 * Catálogo de presentación de las unidades (SPEC-CIVILIZATION-UNITS §6, ADR-043 §7).
 *
 * Es lo que el cliente necesita para enseñar una unidad sin preguntarle al
 * servidor: su nombre, si ataca, lo que cuesta entrenarla y su perfil. NO es la
 * fuente de las estadísticas: ésa es el catálogo de Go (`unit.catalog`), y el
 * servidor no mira este fichero. Se exporta a `units.json` junto a los JSON
 * Schema y un test del Game Server lo compara con el catálogo de Go campo a
 * campo, para que un cambio de balance que no lo actualice rompa la CI en lugar
 * de mentir en el botón.
 *
 * Las estadísticas efectivas no salen de aquí: la vida llega en `UnitView.maxHp`
 * y los plazos en la producción y en la polilínea, con los rasgos ya aplicados.
 */
import type { EraCode, ResourceType, UnitType } from './common.js';

export interface UnitCost {
  readonly resource: ResourceType;
  readonly amount: number;
}

export interface UnitInfo {
  readonly code: UnitType;
  readonly name: string;
  /** Ataca, responde, saquea, asedia, reclama y captura (`CanAttack`, RN-CIVUNIT-005). */
  readonly attacks: boolean;
  /** Sólo la entrena la civilización que la tiene como unidad (`CivilizationInfo.unit`). */
  readonly exclusive: boolean;
  /** Coste en recursos fijos; vacío si no se entrena. */
  readonly trainCosts: readonly UnitCost[];
  /** Cuánto del bien de la civilización del dueño cuesta además (RN-CIVUNIT-003). */
  readonly ownGoodCost: number;
  /** Plazo base de entrenamiento, antes del rasgo `production.speed`; 0 si no se entrena. */
  readonly trainMs: number;
  readonly requiresEra: EraCode | null;
  readonly maxHp: number;
  /** Lo que tarda un paso de terreno base, antes del rasgo `unit.move_speed`. */
  readonly msPerTile: number;
  readonly attackDamage: number;
  readonly attackIntervalMs: number;
  /** En tiles Chebyshev; 0 si no ataca. */
  readonly attackRange: number;
  readonly lootCarry: number;
  readonly populationCost: number;
}

const EXCLUSIVE = {
  attacks: true,
  exclusive: true,
  trainCosts: [
    { resource: 'WOOD', amount: 80 },
    { resource: 'STONE', amount: 40 },
  ],
  ownGoodCost: 10,
  trainMs: 35_000,
  requiresEra: 'BRONZE_AGE',
  attackIntervalMs: 1_500,
  populationCost: 1,
} as const;

/** En el orden de `unit.Types()` del Game Server. */
export const UNITS: readonly UnitInfo[] = [
  {
    code: 'VILLAGER',
    name: 'Aldeano',
    attacks: false,
    exclusive: false,
    trainCosts: [{ resource: 'WOOD', amount: 50 }],
    ownGoodCost: 0,
    trainMs: 20_000,
    requiresEra: null,
    maxHp: 40,
    msPerTile: 600,
    attackDamage: 0,
    attackIntervalMs: 0,
    attackRange: 0,
    lootCarry: 0,
    populationCost: 1,
  },
  {
    code: 'WARRIOR',
    name: 'Guerrero',
    attacks: true,
    exclusive: false,
    trainCosts: [{ resource: 'WOOD', amount: 60 }],
    ownGoodCost: 0,
    trainMs: 25_000,
    requiresEra: null,
    maxHp: 60,
    msPerTile: 600,
    attackDamage: 8,
    attackIntervalMs: 1_500,
    attackRange: 1,
    lootCarry: 20,
    populationCost: 1,
  },
  {
    code: 'CARAVAN',
    name: 'Caravana',
    attacks: false,
    exclusive: false,
    trainCosts: [],
    ownGoodCost: 0,
    trainMs: 0,
    requiresEra: null,
    maxHp: 80,
    msPerTile: 1_200,
    attackDamage: 0,
    attackIntervalMs: 0,
    attackRange: 0,
    lootCarry: 0,
    populationCost: 0,
  },
  { ...EXCLUSIVE, code: 'LEGIONARY', name: 'Legionario', maxHp: 110, msPerTile: 600, attackDamage: 8, attackRange: 1, lootCarry: 20 },
  { ...EXCLUSIVE, code: 'CATAPHRACT', name: 'Catafracto', maxHp: 80, msPerTile: 450, attackDamage: 10, attackRange: 1, lootCarry: 20 },
  {
    ...EXCLUSIVE,
    code: 'HORSE_ARCHER',
    name: 'Arquero a caballo',
    maxHp: 60,
    msPerTile: 540,
    attackDamage: 8,
    attackRange: 3,
    lootCarry: 20,
  },
  { ...EXCLUSIVE, code: 'BERSERKER', name: 'Berserker', maxHp: 60, msPerTile: 600, attackDamage: 14, attackRange: 1, lootCarry: 30 },
];

/** La ficha de presentación de un tipo. Todo `UnitType` tiene la suya (lo comprueba un test). */
export function unitInfo(type: UnitType): UnitInfo {
  const info = UNITS.find((u) => u.code === type);
  if (!info) throw new Error(`unidad sin ficha de presentación: ${type}`);
  return info;
}
