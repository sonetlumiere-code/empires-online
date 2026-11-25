import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';

import {
  anyAssault,
  assaultBlockReason,
  assaultCountdownText,
  assaultLabel,
  assaultTargetNear,
  assaultTone,
  formatLongCountdown,
  standardTone,
  walled,
  type AssaultCheck,
} from './cityFall';
import type { TreatyView } from './diplomacy';
import { entryText, isLoss, type EventLogEntry } from './eventLog';
import { applyNotice, EMPTY_NOTICES, noticeText } from './notices';
import { useWorldStore, type BuildingView, type CityView, type TerritoryView, type UnitView } from './world';

// SPEC-CITY-FALL en el cliente (§4.6): a qué ciudad se ofrece «Asaltar» y por qué
// no, la cuenta atrás, la ciudad que cae y sus entradas en el registro y los avisos.

const yo = '00000000-0000-4000-8000-000000000001';
const otro = '00000000-0000-4000-8000-000000000002';

function ciudad(over: Partial<CityView> = {}): CityView {
  return {
    id: 7, ownerPlayerId: otro, name: 'Villa Norte', centerX: 20, centerY: 40, era: 'BRONZE_AGE', civilization: 'ROMAN', faction: 'NEUTRAL',
    factionJoin: null, clan: null, population: 3, populationLimit: 20, presenceState: 'ONLINE', protectionUntilMs: null,
    stocks: { WOOD: 0, STONE: 0, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 }, craft: null, production: null, advance: null, research: null,
    technologies: [], ...over,
  };
}

function guerrero(over: Partial<UnitView> = {}): UnitView {
  return {
    id: 42, playerId: yo, cityId: 1, unitType: 'WARRIOR', x: 22, y: 40, hp: 60, maxHp: 60, status: 'IDLE',
    movement: null, garrisonCityId: null, carrying: 0, carryingResource: null, ...over,
  };
}

function check(over: Partial<AssaultCheck> = {}): AssaultCheck {
  return { unit: guerrero(), playerId: yo, city: ciudad(), ownCity: ciudad({ id: 1, ownerPlayerId: yo, name: 'Mía' }), buildings: [], treaties: [], nowMs: 1_000, ...over };
}

const muralla: BuildingView = { id: 60, cityId: 7, type: 'WALL', x: 20, y: 40, status: 'COMPLETE', completesAtMs: 0 };

describe('a qué ciudad se ofrece el asalto', () => {
  it('la ajena más cercana a 12 tiles o menos; nunca la propia', () => {
    const lejos = ciudad({ id: 8, centerX: 60, centerY: 40 });
    const propia = ciudad({ id: 1, ownerPlayerId: yo, centerX: 22, centerY: 41 });
    expect(assaultTargetNear([lejos, propia, ciudad()], guerrero(), yo)?.id).toBe(7);
    expect(assaultTargetNear([lejos], guerrero(), yo)).toBeUndefined();
    expect(assaultTargetNear([ciudad()], undefined, yo)).toBeUndefined();
  });
});

describe('por qué no se puede asaltar (RN-SIEGE-009, RN-SIEGE-010, RN-CITY-029)', () => {
  it('con un guerrero propio, junto a una ciudad en brecha de Bronce, se puede', () => {
    expect(assaultBlockReason(check())).toBeNull();
  });

  it('cada motivo, con el texto del servidor cuando lo hay', () => {
    expect(assaultBlockReason(check({ unit: guerrero({ unitType: 'VILLAGER' }) }))).toMatch(/Sólo un guerrero/);
    expect(assaultBlockReason(check({ unit: guerrero({ unitType: 'RAM' }) }))).toMatch(/Sólo un guerrero/);
    expect(assaultBlockReason(check({ buildings: [muralla] }))).toMatch(/muralla está en pie/);
    expect(assaultBlockReason(check({ buildings: [{ ...muralla, status: 'UNDER_CONSTRUCTION' }] }))).toBeNull();
    expect(assaultBlockReason(check({ city: ciudad({ era: 'STONE_AGE' }) }))).toMatch(/Edad de Piedra/);
    expect(assaultBlockReason(check({ city: ciudad({ fallImmuneUntilMs: 1_000 + 52 * 3_600_000 }) }))).toBe(
      'Ciudad recién fundada: se podrá asaltar en 2 d 4 h',
    );
    expect(assaultBlockReason(check({ city: ciudad({ assault: { playerId: otro, unitId: 1, startedAtMs: 0, endsAtMs: 9 } }) }))).toMatch(/Ya hay un asalto/);
    expect(assaultBlockReason(check({ city: ciudad({ presenceState: 'PROTECTED' }) }))).toMatch(/protegida/);
    expect(assaultBlockReason(check({ city: ciudad({ faction: 'ORDER' }), ownCity: ciudad({ id: 1, ownerPlayerId: yo, faction: 'ORDER' }) }))).toMatch(/Orden/);
    const pacto = { otherPlayerId: otro, status: 'ACTIVE', type: 'NON_AGGRESSION' } as TreatyView;
    expect(assaultBlockReason(check({ treaties: [pacto] }))).toMatch(/tratado de paz/);
  });

  it('la muralla cuenta sólo completa y de esa ciudad', () => {
    expect(walled(ciudad(), [muralla])).toBe(true);
    expect(walled(ciudad({ id: 8 }), [muralla])).toBe(false);
    expect(formatLongCountdown(3_600_000 * 3 + 600_000)).toBe('3 h 10 min');
    expect(formatLongCountdown(30_000)).toBe('1 min');
  });
});

describe('la cuenta atrás de un asalto', () => {
  const asalto = { playerId: yo, unitId: 42, startedAtMs: 0, endsAtMs: 900_000 };
  it('se lee desde cada lado', () => {
    expect(assaultLabel(ciudad(), yo, 0)).toBeNull();
    expect(assaultLabel(ciudad({ assault: asalto }), yo, 300_000)).toBe('Asaltas Villa Norte: cae en 10:00');
    expect(assaultLabel(ciudad({ ownerPlayerId: yo, assault: { ...asalto, playerId: otro } }), yo, 0)).toBe('¡Están asaltando tu ciudad! Cae en 15:00');
    expect(assaultLabel(ciudad({ assault: { ...asalto, playerId: otro } }), 'tercero', 0)).toMatch(/bajo asalto/);
    expect(anyAssault([ciudad(), ciudad({ assault: asalto })])).toBe(true);
  });
});

function server<T extends v1.ServerMessage['type']>(type: T, payload: unknown): v1.ServerMessage {
  return { v: 1, type, seq: 1, ts: 1_757_376_000_000, payload } as v1.ServerMessage;
}

describe('el mundo del cliente', () => {
  beforeEach(() => {
    useWorldStore.getState().reset();
    useWorldStore.setState({ playerId: yo, world: { width: 64, height: 64, chunkSize: 32 } });
  });

  it('city.update abre y cierra el asalto; city.fallen quita la ciudad y sus edificios', () => {
    const v = { ...ciudad(), stocks: undefined } as unknown as v1.CityView;
    useWorldStore.getState().applySnapshot({
      serverTimeMs: 0, tick: 1, chunks: [{ cx: 0, cy: 1 }], terrain: [], units: [], territories: [], cities: [v],
      buildings: [muralla, { ...muralla, id: 61, cityId: 9 }],
    } as unknown as v1.WorldSnapshot['payload']);
    const s = useWorldStore.getState();
    const asalto = { playerId: yo, unitId: 42, startedAtMs: 0, endsAtMs: 900_000 };
    s.applyServerMessage(server('city.update', { id: 7, assault: asalto }));
    expect(useWorldStore.getState().cities.get(7)?.assault).toEqual(asalto);
    s.applyServerMessage(server('city.update', { id: 7, population: 4 }));
    expect(useWorldStore.getState().cities.get(7)?.assault).toEqual(asalto);
    s.applyServerMessage(server('city.update', { id: 7, assault: null }));
    expect(useWorldStore.getState().cities.get(7)?.assault).toBeUndefined();

    s.applyServerMessage(server('city.fallen', { cityId: 7, ownerPlayerId: otro, byPlayerId: yo, atMs: 1 }));
    expect(useWorldStore.getState().cities.has(7)).toBe(false);
    expect(useWorldStore.getState().buildings.has(60)).toBe(false);
    expect(useWorldStore.getState().buildings.has(61)).toBe(true);
  });

  it('SPEC-CITY-TAKEN §6: los edificios heredados llegan con la tomada y city.fallen no se los lleva', () => {
    const v = { ...ciudad(), stocks: undefined } as unknown as v1.CityView;
    useWorldStore.getState().applySnapshot({
      serverTimeMs: 0, tick: 1, chunks: [{ cx: 0, cy: 1 }], terrain: [], units: [], territories: [], cities: [v],
      buildings: [muralla, { ...muralla, id: 62, type: 'HOUSE', x: v.centerX + 3 }],
    } as unknown as v1.WorldSnapshot['payload']);
    const s = useWorldStore.getState();
    // El servidor manda la tomada y sus edificios con su ciudad nueva, y después city.fallen.
    s.applyServerMessage(server('building.spawn', { building: { ...muralla, id: 62, type: 'HOUSE', x: v.centerX + 3, cityId: 21 } }));
    s.applyServerMessage(server('city.fallen', { cityId: 7, ownerPlayerId: otro, byPlayerId: yo, atMs: 1, takenCityId: 21 }));
    expect(useWorldStore.getState().cities.has(7)).toBe(false);
    expect(useWorldStore.getState().buildings.has(60)).toBe(false);
    expect(useWorldStore.getState().buildings.get(62)?.cityId).toBe(21);
  });
});

describe('el registro y los avisos (SPEC-CITY-FALL Q9)', () => {
  const base = { id: 1, firstAtMs: 1, lastAtMs: 2, count: 1, otherPlayerId: otro, otherCityId: 9, otherCityName: 'Puerto Sur' };
  it('cada tipo con su texto, y el asalto y la caída como pérdidas', () => {
    const asaltada = { ...base, kind: 'CITY_ASSAULTED', fallsAtMs: Date.UTC(2026, 10, 23, 18, 15) } as EventLogEntry;
    expect(entryText(asaltada)).toMatch(/^¡Puerto Sur asalta tu ciudad! Cae a las/);
    expect(isLoss(asaltada)).toBe(true);
    const perdida = { ...base, kind: 'CITY_LOST', x: 300, y: 400 } as EventLogEntry;
    expect(entryText(perdida)).toBe('Tu ciudad cayó ante Puerto Sur: la refundaste en (300, 400). Conservas tu era y tus tecnologías');
    expect(isLoss(perdida)).toBe(true);
    const tomada = { ...base, kind: 'CITY_TAKEN', x: 20, y: 40 } as EventLogEntry;
    expect(entryText(tomada)).toBe('Tus unidades tomaron y arrasaron la ciudad de Puerto Sur cerca de (20, 40)');
    expect(isLoss(tomada)).toBe(false);
  });

  it('el aviso del asalto lleva cuándo cae', () => {
    const st = applyNotice(EMPTY_NOTICES, {
      kind: 'CITY_ASSAULTED', atMs: 1, otherPlayerId: otro, otherCityId: 9, otherCityName: 'Puerto Sur', fallsAtMs: Date.UTC(2026, 10, 23, 18, 15),
    });
    const g = st.groups[0]!;
    expect(g.fallsAtMs).toBe(Date.UTC(2026, 10, 23, 18, 15));
    expect(noticeText(g)).toMatch(/Cae a las/);
  });
});

// SPEC-ASSAULT-MAP: el asalto en el mapa (RN-SIEGE-012 a RN-SIEGE-014).
describe('el asalto en el mapa', () => {
  const tercero = '00000000-0000-4000-8000-000000000003';
  const asalto = { playerId: otro, unitId: 42, startedAtMs: 0, endsAtMs: 120_000 };

  it('el tono de la ciudad: rojo para su dueño, dorado para quien asalta, naranja para un tercero', () => {
    const mia = ciudad({ ownerPlayerId: yo, assault: asalto });
    expect(assaultTone(mia, yo)).toBe('loss');
    const suya = ciudad({ ownerPlayerId: tercero, assault: { ...asalto, playerId: yo } });
    expect(assaultTone(suya, yo)).toBe('own');
    expect(assaultTone(ciudad({ ownerPlayerId: tercero, assault: asalto }), yo)).toBe('other');
    expect(assaultTone(ciudad(), yo)).toBeNull();
  });

  it('el estandarte del asalto lleva el tono de su ciudad; sin CLAIMING no hay estandarte', () => {
    const portador = guerrero({ id: 42, playerId: otro, status: 'CLAIMING' });
    const mia = ciudad({ ownerPlayerId: yo, assault: asalto });
    expect(standardTone(portador, yo, [mia], [])).toBe('loss');
    expect(standardTone(portador, tercero, [mia], [])).toBe('other');
    expect(standardTone({ ...portador, status: 'IDLE' }, yo, [mia], [])).toBeNull();
  });

  it('el de una reclamación: dorado si es propio, rojo si reclama un territorio de quien mira, naranja si no', () => {
    const reclama = guerrero({ id: 50, playerId: otro, status: 'CLAIMING', x: 5, y: 5 });
    const territorio = (over: Partial<TerritoryView>): TerritoryView => ({
      id: 30, name: 'Llanura', minX: 0, minY: 0, maxX: 15, maxY: 15, ownerType: 'PLAYER', ownerId: yo, ownerFaction: null,
      ownerClan: null, contested: true, claim: { playerId: otro, cityId: 7, cityName: 'Villa Norte', startedAtMs: 0, endsAtMs: 1 }, ...over,
    });
    expect(standardTone({ ...reclama, playerId: yo }, yo, [], [])).toBe('own');
    expect(standardTone(reclama, yo, [], [territorio({})])).toBe('loss');
    // Fuera del territorio, o con la reclamación de otro, no es la suya.
    expect(standardTone({ ...reclama, x: 30 }, yo, [], [territorio({})])).toBe('other');
    expect(standardTone(reclama, yo, [], [territorio({ claim: { playerId: tercero, cityId: null, cityName: null, startedAtMs: 0, endsAtMs: 1 } })])).toBe(
      'other',
    );
    expect(standardTone(reclama, yo, [], [territorio({ ownerId: tercero })])).toBe('other');
  });

  it('la cuenta atrás, y «Cayendo…» a cero sin que el servidor diga la caída', () => {
    expect(assaultCountdownText(120_000, 0)).toBe('Cae en 2:00');
    expect(assaultCountdownText(120_000, 119_500)).toBe('Cae en 0:01');
    expect(assaultCountdownText(120_000, 120_000)).toBe('Cayendo…');
    expect(assaultCountdownText(120_000, 125_000)).toBe('Cayendo…');
  });
});
