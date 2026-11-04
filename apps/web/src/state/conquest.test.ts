import { describe, expect, it } from 'vitest';

import { anyClaim, claimBlockReason, claimLabel, territoryAt, territoryOwnerLabel, type ClaimCheck } from './conquest';
import type { TreatyView } from './diplomacy';
import type { CityView, TerritoryView, UnitView } from './world';

const yo = '00000000-0000-4000-8000-000000000001';
const otro = '00000000-0000-4000-8000-000000000002';

function territorio(over: Partial<TerritoryView> = {}): TerritoryView {
  return { id: 12, name: 'Llanuras', minX: 64, minY: 0, maxX: 127, maxY: 63, ownerType: 'NONE', ownerId: null, contested: false, ...over };
}

function ciudad(over: Partial<CityView> = {}): CityView {
  return {
    id: 7, ownerPlayerId: otro, name: 'Villa Norte', centerX: 10, centerY: 10, era: 'STONE_AGE', population: 3,
    populationLimit: 20, presenceState: 'ONLINE', protectionUntilMs: null, stocks: { WOOD: 0, STONE: 0 },
    production: null, advance: null, ...over,
  };
}

function guerrero(over: Partial<UnitView> = {}): UnitView {
  return {
    id: 42, playerId: yo, cityId: 1, unitType: 'WARRIOR', x: 80, y: 20, hp: 60, maxHp: 60, status: 'IDLE',
    movement: null, garrisonCityId: null, carrying: 0, carryingResource: null, ...over,
  };
}

function check(over: Partial<ClaimCheck> = {}): ClaimCheck {
  return { unit: guerrero(), playerId: yo, territory: territorio(), cities: [ciudad()], treaties: [], nowMs: 1_000, ...over };
}

const ajeno = { ownerType: 'PLAYER', ownerId: otro };

describe('qué territorio y de quién (SPEC-TERRITORY-CONQUEST)', () => {
  it('resuelve el tile con los límites inclusivos', () => {
    const t = territorio();
    expect(territoryAt([t], 64, 0)).toBe(t);
    expect(territoryAt([t], 127, 63)).toBe(t);
    expect(territoryAt([t], 63, 0)).toBeUndefined();
  });

  it('dice si es libre, tuyo o de otra ciudad', () => {
    expect(territoryOwnerLabel(territorio(), yo, [])).toBe('libre');
    expect(territoryOwnerLabel(territorio({ ownerType: 'PLAYER', ownerId: yo }), yo, [])).toBe('tuyo');
    expect(territoryOwnerLabel(territorio(ajeno), yo, [ciudad()])).toBe('de Villa Norte');
    expect(territoryOwnerLabel(territorio(ajeno), yo, [])).toBe('de otro jugador');
  });
});

describe('el botón «Reclamar territorio» (RN-CONQ-001 a 003)', () => {
  it('se puede con un guerrero propio en un territorio libre o ajeno que no es capital', () => {
    expect(claimBlockReason(check())).toBeNull();
    expect(claimBlockReason(check({ territory: territorio(ajeno) }))).toBeNull();
  });

  it('dice por qué no, como el servidor', () => {
    const paz: TreatyView = {
      id: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', type: 'NON_AGGRESSION',
      status: 'ACTIVE', proposedByMe: false, breaksAtMs: null,
    };
    const casos: [Partial<ClaimCheck>, string][] = [
      [{ unit: guerrero({ unitType: 'VILLAGER' }) }, 'Sólo un guerrero reclama territorios'],
      [{ unit: guerrero({ playerId: otro }) }, 'Selecciona un guerrero tuyo'],
      [{ unit: guerrero({ status: 'CLAIMING' }) }, 'Ya está reclamando este territorio'],
      [{ territory: undefined }, 'Aquí no hay ningún territorio'],
      [{ territory: territorio({ ownerType: 'PLAYER', ownerId: yo }) }, 'Este territorio ya es tuyo'],
      [{ territory: territorio({ ...ajeno, contested: true, claim: { playerId: otro, cityId: 7, cityName: 'Villa Norte', startedAtMs: 0, endsAtMs: 600_000 } }) },
        'Ya hay una reclamación en este territorio'],
      [{ territory: territorio(ajeno), cities: [ciudad({ centerX: 100, centerY: 30 })] }, 'Es la capital de Villa Norte: no se puede reclamar'],
      [{ territory: territorio(ajeno), treaties: [paz] }, 'Un tratado de paz con su dueño lo impide'],
      [{ territory: territorio(ajeno), cities: [ciudad({ presenceState: 'PROTECTED' })] }, 'Su dueño está protegido'],
      [{ unit: guerrero({ x: 11, y: 11 }), territory: territorio({ minX: 0 }) }, 'No se reclama dentro de una ciudad'],
    ];
    for (const [over, motivo] of casos) expect(claimBlockReason(check(over))).toBe(motivo);
  });

  it('el acuerdo comercial no da paz, y una protección vencida no protege', () => {
    const comercio: TreatyView = {
      id: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', type: 'TRADE',
      status: 'ACTIVE', proposedByMe: false, breaksAtMs: null,
    };
    expect(claimBlockReason(check({ territory: territorio(ajeno), treaties: [comercio] }))).toBeNull();
    expect(claimBlockReason(check({
      territory: territorio(ajeno), cities: [ciudad({ presenceState: 'PROTECTED', protectionUntilMs: 500 })],
    }))).toBeNull();
  });
});

describe('la reclamación en curso', () => {
  const claim = { playerId: otro, cityId: 7, cityName: 'Villa Norte', startedAtMs: 0, endsAtMs: 600_000 };
  it('se lee desde quien la mira, con su cuenta atrás', () => {
    expect(claimLabel(territorio(), yo, 0)).toBeNull();
    expect(claimLabel(territorio({ contested: true, claim: { ...claim, playerId: yo } }), yo, 300_000)).toBe('Reclamas Llanuras: falta 5:00');
    expect(claimLabel(territorio({ ownerType: 'PLAYER', ownerId: yo, contested: true, claim }), yo, 599_000))
      .toBe('¡Villa Norte reclama tu territorio Llanuras! Falta 0:01');
    expect(claimLabel(territorio({ contested: true, claim: { ...claim, cityName: null } }), yo, 0)).toBe('otro jugador reclama Llanuras: falta 10:00');
    expect(anyClaim([territorio(), territorio({ claim })])).toBe(true);
    expect(anyClaim([territorio()])).toBe(false);
  });
});
