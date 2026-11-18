import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import { advanceBlockReason } from './economy';
import {
  costText,
  effectText,
  productionSpeed,
  researchBlockReason,
  researchSeconds,
  technologiesFor,
  technologyName,
  technologyOf,
} from './technologies';
import { useWorldStore, type CityStocks } from './world';

// SPEC-TECHNOLOGIES en el cliente (§4.7): el panel explica qué se puede
// investigar, cuánto cuesta y tarda, y por qué no; el servidor decide.

const vacio: CityStocks = { WOOD: 0, STONE: 0, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 };
const tec = (code: string) => technologyOf(code)!;
type Ciudad = Parameters<typeof researchBlockReason>[0];
const ciudad = (over: Partial<Ciudad> = {}): Ciudad => ({
  era: 'IRON_AGE', civilization: 'ROMAN', advance: null, research: null, technologies: [],
  stocks: { ...vacio, WOOD: 1000, STONE: 1000, WINE: 100, SILK: 40 },
  ...over,
});

describe('el catálogo en el panel', () => {
  it('nombres, efectos y costes en palabras', () => {
    expect(technologyName('GUILDS')).toBe('Gremios');
    expect(technologyName('NO_EXISTE')).toBe('NO_EXISTE');
    expect(effectText(tec('BRONZE_WEAPONS'))).toBe('+10 % de daño en cada golpe');
    expect(effectText(tec('BASKETRY'))).toBe('+20 % de carga de cada aldeano');
    expect(costText(tec('STONE_TOOLS'), 'ROMAN')).toBe('150 de madera y 50 de piedra');
    expect(costText(tec('WHEEL'), 'ROMAN')).toBe('300 de madera, 200 de piedra y 30 bienes de otras civilizaciones');
    expect(costText(tec('ROMAN_ROADS'), 'ROMAN')).toBe('200 de madera, 200 de piedra y 40 de vino');
  });

  it('el plazo con production.speed compuesto, como el servidor (§4.4)', () => {
    expect(productionSpeed('BYZANTINE', [])).toBe(1000);
    expect(productionSpeed('ROMAN', [])).toBe(1100);
    expect(productionSpeed('ROMAN', ['GUILDS'])).toBe(1210);
    expect(researchSeconds(tec('STONE_TOOLS'), 'BYZANTINE', [])).toBe(60);
    expect(researchSeconds(tec('GUILDS'), 'ROMAN', []), '163 636 ms').toBe(164);
    expect(researchSeconds(tec('ARCHITECTURE'), 'ROMAN', ['GUILDS']), '300 000 / 1,21').toBe(248);
    expect(researchSeconds(tec('STONE_TOOLS'), 'NORSE', []), '60 000 / 0,9').toBe(67);
  });

  it('cada civilización ve las ocho comunes y la suya, por era', () => {
    const romanas = technologiesFor('ROMAN');
    expect(romanas.map((g) => g.era)).toEqual(['STONE_AGE', 'BRONZE_AGE', 'IRON_AGE', 'CASTLE_AGE']);
    const codigos = romanas.flatMap((g) => g.technologies.map((t) => t.code));
    expect(codigos).toHaveLength(9);
    expect(codigos).toContain('ROMAN_ROADS');
    expect(codigos).not.toContain('RAIDING');
    expect(technologiesFor('NORSE').flatMap((g) => g.technologies.map((t) => t.code))).toContain('RAIDING');
  });
});

describe('el botón «Investigar» dice por qué no responde (§5)', () => {
  it('cada motivo, en el orden de la spec', () => {
    expect(researchBlockReason(ciudad(), tec('RAIDING'))).toBe('Es de otra civilización.');
    expect(researchBlockReason(ciudad({ technologies: ['WHEEL'] }), tec('WHEEL'))).toBe('Ya la tienes.');
    expect(researchBlockReason(ciudad({ era: 'STONE_AGE' }), tec('BRONZE_WEAPONS'))).toBe('Desde la Edad de Bronce.');
    expect(researchBlockReason(ciudad({ era: 'CASTLE_AGE' }), tec('IRON_WEAPONS'))).toBe('Pide Armas de bronce.');
    expect(researchBlockReason(ciudad({ research: { technology: 'GUILDS', completesAtMs: 1 } }), tec('WHEEL'))).toBe(
      'Ya se investiga Gremios.',
    );
    expect(researchBlockReason(ciudad({ advance: { era: 'CASTLE_AGE', completesAtMs: 1 } }), tec('WHEEL'))).toBe(
      'La ciudad está avanzando de edad.',
    );
    expect(researchBlockReason(ciudad({ stocks: { ...vacio, WOOD: 250, STONE: 200 } }), tec('WHEEL'))).toBe(
      'Faltan 50 de madera y 30 bienes de otras civilizaciones.',
    );
    expect(researchBlockReason(ciudad({ stocks: { ...vacio, WOOD: 500, STONE: 500, WINE: 39 } }), tec('ROMAN_ROADS'))).toBe(
      'Faltan 1 de vino.',
    );
    expect(researchBlockReason(ciudad({ stocks: { ...vacio, WOOD: 500, STONE: 500, WINE: 500 } }), tec('WHEEL'))).toBe(
      'Faltan 30 bienes de otras civilizaciones.',
    );
    expect(researchBlockReason(ciudad(), tec('WHEEL'))).toBeNull();
    expect(researchBlockReason(ciudad({ era: 'CASTLE_AGE', technologies: ['BRONZE_WEAPONS'] }), tec('IRON_WEAPONS'))).toBe(
      'Faltan 20 bienes de otras civilizaciones.',
    );
    expect(
      researchBlockReason(
        ciudad({ era: 'CASTLE_AGE', technologies: ['BRONZE_WEAPONS'], stocks: { ...vacio, WOOD: 400, STONE: 300, SILK: 40, SPICES: 20 } }),
        tec('IRON_WEAPONS'),
      ),
    ).toBeNull();
  });

  it('una investigación en curso impide avanzar de era (RN-TECH-003)', () => {
    const c = { era: 'IRON_AGE', production: null, advance: null, stocks: { ...vacio, WOOD: 5000, STONE: 5000 } };
    expect(advanceBlockReason({ ...c, research: { technology: 'WHEEL', completesAtMs: 1 } }, 500)).toBe(
      'La ciudad está investigando: espera a que termine.',
    );
    expect(advanceBlockReason({ ...c, research: null }, 500)).toBeNull();
  });
});

describe('el store y la investigación', () => {
  const state = () => useWorldStore.getState();
  const server = (type: string, seq: number, payload: unknown) =>
    ({ v: 1, type, seq, ts: 1_757_376_000_000, payload }) as unknown as v1.ServerMessage;
  const vista = (over: Record<string, unknown> = {}) => ({
    id: 10, ownerPlayerId: '11111111-1111-4111-8111-111111111111', name: 'Roma', centerX: 5, centerY: 5,
    era: 'IRON_AGE', civilization: 'ROMAN', faction: 'NEUTRAL', factionJoin: null, clan: null, population: 3, populationLimit: 100,
    presenceState: 'ONLINE', protectionUntilMs: null, stocks: { WOOD: 40, STONE: 15 }, production: null, advance: null,
    ...over,
  });

  beforeEach(() => state().reset());

  it('una ciudad ajena llega sin investigación ni tecnologías', () => {
    state().applySnapshot({
      serverTimeMs: 1, tick: 1, chunks: [{ cx: 0, cy: 0 }], terrain: [], units: [], territories: [], buildings: [],
      cities: [vista()],
    } as v1.WorldSnapshot['payload']);
    expect(state().cities.get(10)!.research).toBeNull();
    expect(state().cities.get(10)!.technologies).toEqual([]);
  });

  it('la propia llega con las suyas; city.update empieza la investigación y la termina con la tecnología', () => {
    state().applySnapshot({
      serverTimeMs: 1, tick: 1, chunks: [{ cx: 0, cy: 0 }], terrain: [], units: [], territories: [], buildings: [],
      cities: [vista({ research: null, technologies: ['STONE_TOOLS'] })],
    } as v1.WorldSnapshot['payload']);
    expect(state().cities.get(10)!.technologies).toEqual(['STONE_TOOLS']);
    state().applyServerMessage(server('city.update', 2, { id: 10, research: { technology: 'WHEEL', completesAtMs: 99 } }));
    expect(state().cities.get(10)!.research).toEqual({ technology: 'WHEEL', completesAtMs: 99 });
    state().applyServerMessage(server('city.update', 3, { id: 10, stocks: { WOOD: 1, STONE: 1 } }));
    expect(state().cities.get(10)!.research, 'sin research en el mensaje, se conserva').not.toBeNull();
    state().applyServerMessage(server('city.update', 4, { id: 10, research: null, technologies: ['STONE_TOOLS', 'WHEEL'] }));
    expect(state().cities.get(10)!.research).toBeNull();
    expect(state().cities.get(10)!.technologies).toEqual(['STONE_TOOLS', 'WHEEL']);
  });
});
