import { describe, expect, it } from 'vitest';

import { anyClaim, claimBlockReason, claimLabel, territoryAt, territoryOwnerLabel, type ClaimCheck } from './conquest';
import type { TreatyView } from './diplomacy';
import type { CityView, TerritoryView, UnitView } from './world';

const yo = '00000000-0000-4000-8000-000000000001';
const otro = '00000000-0000-4000-8000-000000000002';

function territorio(over: Partial<TerritoryView> = {}): TerritoryView {
  return { id: 12, name: 'Llanuras', minX: 64, minY: 0, maxX: 127, maxY: 63, ownerType: 'NONE', ownerId: null, ownerFaction: null, ownerClan: null, contested: false, ...over };
}

function ciudad(over: Partial<CityView> = {}): CityView {
  return {
    id: 7, ownerPlayerId: otro, name: 'Villa Norte', centerX: 10, centerY: 10, era: 'STONE_AGE', civilization: 'ROMAN', faction: 'NEUTRAL', factionJoin: null, clan: null, population: 3,
    populationLimit: 20, presenceState: 'ONLINE', protectionUntilMs: null, stocks: { WOOD: 0, STONE: 0, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 }, craft: null,
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

describe('los territorios de clan (SPEC-CLAN-TERRITORIES)', () => {
  const deClan = (id: number) => ({ ownerType: 'CLAN', ownerId: String(id), ownerClan: { id, name: 'Los Lobos', tag: 'LOB' } });

  it('dice de qué clan es, y si es del propio', () => {
    expect(territoryOwnerLabel(territorio(deClan(3)), yo, [], 3)).toBe('de tu clan [LOB]');
    expect(territoryOwnerLabel(territorio(deClan(3)), yo, [], 4)).toBe('del clan Los Lobos [LOB]');
    expect(territoryOwnerLabel(territorio(deClan(3)), yo, [])).toBe('del clan Los Lobos [LOB]');
  });

  it('«Reclamar para el clan» pide un clan, y el del propio clan no se reclama (§5)', () => {
    expect(claimBlockReason(check({ forClan: true, myClanId: null }))).toBe('No estás en ningún clan');
    expect(claimBlockReason(check({ forClan: true, myClanId: 3 }))).toBeNull();
    expect(claimBlockReason(check({ territory: territorio(deClan(3)), myClanId: 3 }))).toBe('Este territorio ya es de tu clan');
    expect(claimBlockReason(check({ territory: territorio(deClan(3)), myClanId: 3, forClan: true }))).toBe('Este territorio ya es de tu clan');
    expect(claimBlockReason(check({ territory: territorio(deClan(3)), myClanId: 4 })), 'el de otro clan: lo decide el servidor').toBeNull();
    expect(claimBlockReason(check({ territory: territorio({ ownerType: 'PLAYER', ownerId: yo }), myClanId: 3, forClan: true }))).toBe(
      'Este territorio ya es tuyo',
    );
  });

  it('la reclamación dice para qué clan y avisa si es la tierra del propio', () => {
    const claim = { playerId: otro, cityId: 7, cityName: 'Villa Norte', startedAtMs: 0, endsAtMs: 60_000 };
    expect(claimLabel(territorio({ claim: { ...claim, playerId: yo, clan: { id: 3, tag: 'LOB' } } }), yo, 0, 3)).toBe(
      'Reclamas Llanuras para [LOB]: falta 1:00',
    );
    expect(claimLabel(territorio({ ...deClan(3), claim }), yo, 0, 3)).toBe('¡Villa Norte reclama Llanuras, de tu clan! Falta 1:00');
    expect(claimLabel(territorio({ ...deClan(3), claim }), yo, 0, 4)).toBe('Villa Norte reclama Llanuras: falta 1:00');
  });
});
