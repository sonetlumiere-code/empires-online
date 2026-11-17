import { describe, expect, it } from 'vitest';
import { v1 } from '@empires-online/protocol';

import {
  civilizationUnit,
  civilizationUnitLine,
  trainCostText,
  unitAttacks,
  unitLabel,
  unitPlural,
  unitTitle,
} from './units';
import { civilizationUnitBlockReason, decideTileOrder, decideUnitClick } from './economy';
import type { CityView } from './world';

// SPEC-CIVILIZATION-UNITS §4.6 en el cliente: nombres, capacidades, la unidad de
// cada civilización y por qué no se puede entrenar la propia.

const PLAYER = '11111111-1111-4111-8111-111111111111';
const OTHER = '22222222-2222-4222-8222-222222222222';

describe('unidades de civilización', () => {
  it('cada civilización tiene la suya, con su línea para el registro', () => {
    expect(civilizationUnit('ROMAN')).toBe('LEGIONARY');
    expect(civilizationUnit('BYZANTINE')).toBe('CATAPHRACT');
    expect(civilizationUnit('PERSIAN')).toBe('HORSE_ARCHER');
    expect(civilizationUnit('NORSE')).toBe('BERSERKER');
    expect(civilizationUnit('ATLANTE')).toBeNull();
    expect(civilizationUnitLine('ROMAN')).toBe('Unidad propia: legionario, aguanta casi el doble que un guerrero');
    expect(civilizationUnitLine('PERSIAN')).toBe('Unidad propia: arquero a caballo, golpea a tres casillas');
  });

  it('los nombres, en singular, en plural y en el panel de selección', () => {
    expect(unitLabel('HORSE_ARCHER')).toBe('arquero a caballo');
    expect(unitPlural('HORSE_ARCHER')).toBe('arqueros a caballo');
    expect(unitPlural('BERSERKER')).toBe('berserkers');
    expect(unitPlural('WARRIOR')).toBe('guerreros');
    expect(unitTitle('LEGIONARY')).toBe('Legionario · Romanos');
    expect(unitTitle('WARRIOR')).toBe('guerrero');
  });

  it('atacan las que atacan: el guerrero y las cuatro de civilización (RN-CIVUNIT-005)', () => {
    expect(v1.UNITS.filter((u) => unitAttacks(u.code)).map((u) => u.code)).toEqual([
      'WARRIOR', 'LEGIONARY', 'CATAPHRACT', 'HORSE_ARCHER', 'BERSERKER',
    ]);
    expect(unitAttacks('KNIGHT')).toBe(false);
  });

  it('una unidad de civilización saquea, derriba y ataca como el guerrero', () => {
    const ajena = () => ({ id: 20, ownerPlayerId: OTHER });
    const casa = () => ({ id: 9, cityId: 20 });
    for (const unitType of ['LEGIONARY', 'BERSERKER']) {
      expect(decideTileOrder({ x: 6, y: 4 }, { unitType }, 0, ajena, PLAYER)).toEqual({ kind: 'pillage', cityId: 20 });
      expect(decideTileOrder({ x: 6, y: 4 }, { unitType }, 0, ajena, PLAYER, casa)).toEqual({ kind: 'demolish', buildingId: 9 });
      expect(decideUnitClick({ playerId: OTHER }, { playerId: PLAYER, unitType }, PLAYER)).toEqual({ kind: 'attack' });
    }
    expect(decideUnitClick({ playerId: OTHER }, { playerId: PLAYER, unitType: 'VILLAGER' }, PLAYER)).toEqual({ kind: 'none' });
  });

  it('el coste con el bien propio', () => {
    expect(trainCostText('LEGIONARY', 'ROMAN')).toBe('80 de madera, 40 de piedra y 10 de vino');
    expect(trainCostText('BERSERKER', 'NORSE')).toBe('80 de madera, 40 de piedra y 10 de pieles');
    expect(trainCostText('WARRIOR', 'ROMAN')).toBe('60 de madera');
  });
});

describe('civilizationUnitBlockReason', () => {
  const base: Pick<CityView, 'era' | 'civilization' | 'stocks' | 'population' | 'populationLimit' | 'production' | 'advance'> = {
    era: 'BRONZE_AGE',
    civilization: 'ROMAN',
    stocks: { WOOD: 80, STONE: 40, WINE: 10, SILK: 0, SPICES: 0, FURS: 0 },
    population: 3,
    populationLimit: 50,
    production: null,
    advance: null,
  };

  it('pide la Edad de Bronce antes que nada', () => {
    expect(civilizationUnitBlockReason({ ...base, era: 'STONE_AGE', production: { unitType: 'WARRIOR', completesAtMs: 1 } }))
      .toBe('El legionario pide la Edad de Bronce.');
  });

  it('después, lo de siempre: entrenando, avanzando o sin población', () => {
    expect(civilizationUnitBlockReason({ ...base, production: { unitType: 'WARRIOR', completesAtMs: 1 } }))
      .toBe('Ya está entrenando una unidad.');
    expect(civilizationUnitBlockReason({ ...base, population: 50 })).toBe('Población al tope: construye una casa para subir el límite.');
  });

  it('cada recurso que falta, también el bien propio; los ajenos no cuentan', () => {
    expect(civilizationUnitBlockReason({ ...base, stocks: { ...base.stocks, WINE: 4, FURS: 99 } })).toBe('Faltan 6 de vino.');
    expect(civilizationUnitBlockReason({ ...base, stocks: { ...base.stocks, WOOD: 0, STONE: 30, WINE: 0 } }))
      .toBe('Faltan 80 de madera, 10 de piedra y 10 de vino.');
    expect(civilizationUnitBlockReason(base)).toBeNull();
  });
});
