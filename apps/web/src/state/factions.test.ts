import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import type { TreatyView } from './diplomacy';
import { useWorldStore } from './world';
import {
  canJoinFaction,
  countdown,
  FACTION_CHOICES,
  factionLabel,
  isAffine,
  joinLabel,
  opposed,
  orderPeace,
  territoryFactionLine,
  treatiesBrokenByJoining,
  treatyForbidden,
} from './factions';

const yo = '00000000-0000-4000-8000-000000000001';
const otro = '00000000-0000-4000-8000-000000000002';
const tercero = '00000000-0000-4000-8000-000000000003';

const territorio = (over: Partial<{ ownerType: string; ownerId: string | null; ownerFaction: string | null }> = {}) => ({
  ownerType: 'PLAYER', ownerId: otro, ownerFaction: 'NEUTRAL', ...over,
});

function tratado(over: Partial<TreatyView> = {}): TreatyView {
  return {
    id: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', type: 'ALLIANCE', status: 'ACTIVE',
    proposedByMe: true, breaksAtMs: null, ...over,
  } as TreatyView;
}

describe('facciones en el cliente (SPEC-FACTIONS)', () => {
  it('nombra los bandos, y un código vacío es Neutral', () => {
    expect(factionLabel('ORDER')).toBe('Orden');
    expect(factionLabel('CHAOS')).toBe('Caos');
    expect(factionLabel('NEUTRAL')).toBe('Neutral');
    expect(factionLabel(undefined)).toBe('Neutral');
    expect(FACTION_CHOICES.map((f) => f.code)).toEqual(['ORDER', 'CHAOS']);
  });

  it('sólo un Neutral sin entrada puede elegir bando (RN-FAC-001)', () => {
    expect(canJoinFaction({ faction: 'NEUTRAL', factionJoin: null })).toBe(true);
    expect(canJoinFaction({ faction: 'NEUTRAL', factionJoin: { faction: 'ORDER', atMs: 1 } })).toBe(false);
    expect(canJoinFaction({ faction: 'ORDER', factionJoin: null })).toBe(false);
    expect(canJoinFaction(undefined)).toBe(false);
  });

  it('la cuenta atrás de la entrada, propia y ajena', () => {
    expect(countdown(10_000 + 750_000, 10_000)).toBe('12:30');
    expect(countdown(1, 5)).toBe('0:00');
    expect(joinLabel({ faction: 'ORDER', atMs: 70_000 }, 5_000, true)).toBe('Te unes a la Orden en 1:05');
    expect(joinLabel({ faction: 'CHAOS', atMs: 70_000 }, 5_000, false)).toBe('Se une al Caos en 1:05');
  });

  it('la paz de la Orden y los bandos contrarios (RN-FAC-003, RN-FAC-005)', () => {
    expect(orderPeace('ORDER', 'ORDER')).toBe(true);
    expect(orderPeace('CHAOS', 'CHAOS')).toBe(false);
    expect(orderPeace('ORDER', 'NEUTRAL')).toBe(false);
    expect(opposed('ORDER', 'CHAOS')).toBe(true);
    expect(opposed('CHAOS', 'ORDER')).toBe(true);
    expect(opposed('ORDER', 'NEUTRAL')).toBe(false);
    expect(treatyForbidden('ALLIANCE', 'ORDER', 'CHAOS')).toBe(true);
    expect(treatyForbidden('NON_AGGRESSION', 'CHAOS', 'ORDER')).toBe(true);
    expect(treatyForbidden('TRADE', 'ORDER', 'CHAOS'), 'el comercio, sí').toBe(false);
    expect(treatyForbidden('ALLIANCE', 'ORDER', 'NEUTRAL')).toBe(false);
  });

  it('la tierra afín: la propia siempre, la del bando si no eres Neutral (RN-FAC-007)', () => {
    expect(isAffine(territorio({ ownerId: yo }), yo, 'NEUTRAL')).toBe(true);
    expect(isAffine(territorio({ ownerFaction: 'NEUTRAL' }), yo, 'NEUTRAL'), 'la de otro neutral no').toBe(false);
    expect(isAffine(territorio({ ownerFaction: 'ORDER' }), yo, 'ORDER')).toBe(true);
    expect(isAffine(territorio({ ownerFaction: 'CHAOS' }), yo, 'ORDER')).toBe(false);
    expect(isAffine(territorio({ ownerType: 'NONE', ownerId: null, ownerFaction: null }), yo, 'ORDER')).toBe(false);
    expect(territoryFactionLine(territorio({ ownerFaction: 'ORDER' }), yo, 'ORDER')).toBe(
      'Bando del dueño: Orden. Tierra afín: tus unidades reciben un 20 % menos de daño',
    );
    expect(territoryFactionLine(territorio({ ownerFaction: 'CHAOS' }), yo, 'ORDER')).toBe('Bando del dueño: Caos');
    expect(territoryFactionLine(territorio({ ownerType: 'NONE', ownerId: null, ownerFaction: null }), yo, 'ORDER')).toBeNull();
  });

  it('enumera los pactos y alianzas que se romperán al entrar (RN-FAC-006)', () => {
    const ciudades = [
      { ownerPlayerId: otro, faction: 'CHAOS' },
      { ownerPlayerId: tercero, faction: 'ORDER' },
    ];
    const lista = [
      tratado({ id: 1, otherPlayerId: otro, type: 'ALLIANCE' }),
      tratado({ id: 2, otherPlayerId: tercero, type: 'NON_AGGRESSION' }),
      tratado({ id: 3, otherPlayerId: otro, type: 'TRADE' }),
      tratado({ id: 4, otherPlayerId: otro, type: 'NON_AGGRESSION', breaksAtMs: 99 }),
      tratado({ id: 5, otherPlayerId: otro, type: 'NON_AGGRESSION', status: 'PROPOSED' }),
    ];
    expect(treatiesBrokenByJoining(lista, ciudades, 'ORDER').map((t) => t.id)).toEqual([1]);
    expect(treatiesBrokenByJoining(lista, ciudades, 'CHAOS').map((t) => t.id)).toEqual([2]);
  });
});

describe('el store y la facción', () => {
  const state = () => useWorldStore.getState();
  const server = (type: string, seq: number, payload: unknown) =>
    ({ v: 1, type, seq, ts: 1_757_376_000_000, payload }) as v1.ServerMessage;

  beforeEach(() => {
    state().reset();
    state().applySnapshot({
      serverTimeMs: 1_757_376_000_000, tick: 1, chunks: [{ cx: 0, cy: 0 }], terrain: [], units: [], territories: [],
      buildings: [],
      cities: [{
        id: 10, ownerPlayerId: yo, name: 'Roma', centerX: 5, centerY: 5, era: 'STONE_AGE', civilization: 'ROMAN',
        faction: 'NEUTRAL', population: 3, populationLimit: 20, presenceState: 'ONLINE', protectionUntilMs: null,
      }],
    } as v1.WorldSnapshot['payload']);
  });

  it('sin factionJoin en el snapshot no hay entrada; city.update la apunta y la hace contar', () => {
    expect(state().cities.get(10)!.factionJoin).toBeNull();
    state().applyServerMessage(server('city.update', 2, { id: 10, factionJoin: { faction: 'ORDER', atMs: 99 } }));
    expect(state().cities.get(10)!.factionJoin).toEqual({ faction: 'ORDER', atMs: 99 });
    expect(state().cities.get(10)!.faction).toBe('NEUTRAL');
    state().applyServerMessage(server('city.update', 3, { id: 10, population: 4 }));
    expect(state().cities.get(10)!.factionJoin, 'sin la clave, se conserva').not.toBeNull();
    state().applyServerMessage(server('city.update', 4, { id: 10, faction: 'ORDER', factionJoin: null }));
    expect(state().cities.get(10)!.faction).toBe('ORDER');
    expect(state().cities.get(10)!.factionJoin).toBeNull();
  });
});
