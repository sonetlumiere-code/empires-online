import { describe, expect, it } from 'vitest';

import { decideTileOrder, ramBlockReason, WALL_COST, wallBlockReason } from './economy';
import { entryText, type EventLogEntry } from './eventLog';
import { applyNotice, EMPTY_NOTICES, noticeKey, noticeText } from './notices';
import { isSiegeEngine, unitAttacks, unitLabel, unitPlural } from './units';

// SPEC-SIEGE-ENGINES en el cliente (§4.5): el clic con el ariete y con el
// guerrero contra una ciudad amurallada, los motivos de «Muralla» y «Ariete», y
// la muralla en el registro y en los avisos.

const PLAYER = '11111111-1111-4111-8111-111111111111';
const OTRO = '22222222-2222-4222-8222-222222222222';
const ajena = () => ({ id: 20, ownerPlayerId: OTRO, centerX: 5, centerY: 5 });
const propia = () => ({ id: 10, ownerPlayerId: PLAYER, centerX: 5, centerY: 5 });
const ariete = { unitType: 'RAM' };
const guerrero = { unitType: 'WARRIOR' };

type Edificio = { id: number; cityId: number; type: 'HOUSE' | 'WALL'; status: 'COMPLETE' | 'UNDER_CONSTRUCTION' };
function edificios(muralla: Edificio['status'] | null) {
  return (t: { x: number; y: number }): Edificio | undefined => {
    if (t.x === 5 && t.y === 5 && muralla) return { id: 60, cityId: 20, type: 'WALL', status: muralla };
    if (t.x === 6 && t.y === 4) return { id: 77, cityId: 20, type: 'HOUSE', status: 'COMPLETE' };
    return undefined;
  };
}

describe('el ariete en el cliente', () => {
  it('es una máquina de asedio que no ataca, con su nombre', () => {
    expect(isSiegeEngine('RAM')).toBe(true);
    expect(unitAttacks('RAM')).toBe(false);
    expect(isSiegeEngine('WARRIOR')).toBe(false);
    expect(unitLabel('RAM')).toBe('ariete');
    expect(unitPlural('RAM')).toBe('arietes');
  });

  it('contra una ciudad amurallada, cualquier clic en su 3 × 3 derriba la muralla', () => {
    for (const tile of [{ x: 6, y: 4 }, { x: 5, y: 5 }, { x: 4, y: 6 }]) {
      expect(decideTileOrder(tile, ariete, 0, ajena, PLAYER, edificios('COMPLETE'))).toEqual({ kind: 'demolish', buildingId: 60 });
    }
  });

  it('en brecha, derriba el edificio del tile; sin edificio, dice por qué', () => {
    expect(decideTileOrder({ x: 6, y: 4 }, ariete, 0, ajena, PLAYER, edificios(null))).toEqual({ kind: 'demolish', buildingId: 77 });
    expect(decideTileOrder({ x: 6, y: 4 }, ariete, 0, ajena, PLAYER, edificios('UNDER_CONSTRUCTION')))
      .toEqual({ kind: 'demolish', buildingId: 77 });
    expect(decideTileOrder({ x: 4, y: 6 }, ariete, 0, ajena, PLAYER, edificios(null))).toMatchObject({ kind: 'blocked' });
  });

  it('no entra en guarnición ni saquea: sobre la ciudad propia no manda nada', () => {
    expect(decideTileOrder({ x: 5, y: 5 }, ariete, 0, propia, PLAYER, () => undefined)).toEqual({ kind: 'none' });
  });
});

describe('el guerrero contra una ciudad amurallada', () => {
  it('no derriba la casa y dice por qué; el centro sigue siendo saquear', () => {
    expect(decideTileOrder({ x: 6, y: 4 }, guerrero, 0, ajena, PLAYER, edificios('COMPLETE'))).toEqual({
      kind: 'blocked',
      reason: 'La muralla está en pie: sólo un ariete la derriba.',
    });
    expect(decideTileOrder({ x: 5, y: 5 }, guerrero, 0, ajena, PLAYER, edificios('COMPLETE'))).toEqual({ kind: 'pillage', cityId: 20 });
  });

  it('con la muralla en obra o derribada, derriba como en E8', () => {
    expect(decideTileOrder({ x: 6, y: 4 }, guerrero, 0, ajena, PLAYER, edificios('UNDER_CONSTRUCTION')))
      .toEqual({ kind: 'demolish', buildingId: 77 });
    expect(decideTileOrder({ x: 6, y: 4 }, guerrero, 0, ajena, PLAYER, edificios(null))).toEqual({ kind: 'demolish', buildingId: 77 });
  });
});

describe('los motivos de «Muralla» y «Ariete»', () => {
  const stocks = { WOOD: 1_000, STONE: 1_000 };

  it('la muralla pide Bronce, una por ciudad y su coste', () => {
    expect(wallBlockReason({ era: 'STONE_AGE', stocks } as never, [])).toBe('La muralla pide la Edad de Bronce.');
    expect(wallBlockReason({ era: 'BRONZE_AGE', stocks } as never, [{ type: 'WALL' }])).toBe('La ciudad ya tiene una muralla.');
    expect(wallBlockReason({ era: 'BRONZE_AGE', stocks: { WOOD: 100, STONE: 150 } } as never, [])).toBe('Faltan 50 de piedra.');
    expect(wallBlockReason({ era: 'IRON_AGE', stocks } as never, [{ type: 'HOUSE' }])).toBeNull();
    expect(WALL_COST).toEqual({ WOOD: 100, STONE: 200 });
  });

  it('el ariete pide Hierro, dos plazas y su coste', () => {
    const ciudad = { era: 'IRON_AGE', stocks, population: 3, populationLimit: 20, production: null, advance: null };
    expect(ramBlockReason({ ...ciudad, era: 'BRONZE_AGE' } as never)).toBe('El ariete pide la Edad de Hierro.');
    expect(ramBlockReason({ ...ciudad, population: 19 } as never)).toMatch(/dos plazas/);
    expect(ramBlockReason({ ...ciudad, stocks: { WOOD: 150, STONE: 70 } } as never)).toBe('Faltan 5 de piedra.');
    expect(ramBlockReason(ciudad as never)).toBeNull();
  });
});

describe('la muralla en el registro y en los avisos (Q10)', () => {
  const entrada = (kind: EventLogEntry['kind'], extra: Partial<EventLogEntry> = {}): EventLogEntry => ({
    id: 1, kind, firstAtMs: 1_000, lastAtMs: 2_000, count: 1,
    otherPlayerId: OTRO, otherCityId: 7, otherCityName: 'Villa Norte', ...extra,
  });

  it('dice «tu muralla» y no «tus casas»', () => {
    expect(entryText(entrada('HOUSE_LOST', { buildingType: 'WALL' }))).toBe('Villa Norte derribó tu muralla: tu ciudad está en brecha');
    expect(entryText(entrada('HOUSE_DESTROYED', { buildingType: 'WALL' }))).toBe('Tus arietes derribaron la muralla de Villa Norte');
    expect(entryText(entrada('HOUSE_LOST', { buildingType: 'HOUSE' }))).toBe('Villa Norte derribó una de tus casas');
  });

  it('el aviso de la muralla no se junta con el de las casas', () => {
    const base = { kind: 'HOUSE_DESTROYED' as const, atMs: 1_000, otherPlayerId: OTRO, otherCityId: 7, otherCityName: 'Villa Norte' };
    expect(noticeKey({ ...base, buildingType: 'WALL' })).not.toBe(noticeKey(base));
    const s = applyNotice(applyNotice(EMPTY_NOTICES, base), { ...base, atMs: 1_500, buildingType: 'WALL' });
    expect(s.groups).toHaveLength(2);
    expect(noticeText(s.groups[0]!)).toBe('Tus arietes derribaron la muralla de Villa Norte');
  });
});
