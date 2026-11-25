import { describe, expect, it } from 'vitest';

import {
  applyEventLogPage,
  EMPTY_EVENT_LOG,
  entryText,
  isLoss,
  markSeenLocally,
  markTarget,
  moreUnseenBeyond,
  shouldRefetchOnVisible,
  timeAgo,
  unseenEntries,
  type EventLogEntry,
  type EventLogPage,
} from './eventLog';

const otro = '00000000-0000-4000-8000-000000000002';

function entrada(id: number, kind: EventLogEntry['kind'], extra: Partial<EventLogEntry> = {}): EventLogEntry {
  return {
    id, kind, firstAtMs: 1_000, lastAtMs: 2_000, count: 1,
    otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', ...extra,
  };
}

function pagina(entries: EventLogEntry[], seenUpToId: number, nextBeforeId: number | null): EventLogPage {
  return { entries, seenUpToId, nextBeforeId };
}

describe('páginas del registro (SPEC-EVENT-LOG)', () => {
  it('la primera página sustituye todo: un grupo que creció cambia de id', () => {
    let s = applyEventLogPage(EMPTY_EVENT_LOG, pagina([entrada(41, 'PILLAGED', { count: 3 })], 0, null), null);
    s = applyEventLogPage(s, pagina([entrada(45, 'PILLAGED', { count: 5 })], 0, null), null);
    expect(s.entries.map((e) => e.id)).toEqual([45]);
    expect(s.loaded).toBe(true);
  });

  it('una página anterior se añade al final, sin repetir, y una vieja se descarta', () => {
    let s = applyEventLogPage(EMPTY_EVENT_LOG, pagina([entrada(9, 'PILLAGED'), entrada(8, 'UNIT_LOST')], 0, 8), null);
    s = applyEventLogPage(s, pagina([entrada(8, 'UNIT_LOST'), entrada(5, 'TREATY_OFFERED')], 0, null), 8);
    expect(s.entries.map((e) => e.id)).toEqual([9, 8, 5]);
    expect(s.nextBeforeId).toBeNull();
    const igual = applyEventLogPage(s, pagina([entrada(2, 'UNIT_LOST')], 0, null), 8);
    expect(igual).toBe(s);
  });

  it('nuevo es lo que pasa del cursor del servidor y del de la pestaña', () => {
    let s = applyEventLogPage(EMPTY_EVENT_LOG, pagina([entrada(12, 'PILLAGED'), entrada(10, 'UNIT_LOST'), entrada(4, 'UNIT_LOST')], 5, null), null);
    expect(unseenEntries(s).map((e) => e.id)).toEqual([12, 10]);
    expect(markTarget(s)).toBe(12);
    s = markSeenLocally(s, 12);
    expect(unseenEntries(s)).toEqual([]);
    expect(markTarget(s)).toBeNull();
    expect(markSeenLocally(s, 3)).toBe(s);
    // La siguiente primera página trae el cursor del servidor, que no marcó lo
    // más reciente; en esta pestaña sigue visto.
    s = applyEventLogPage(s, pagina([entrada(14, 'PILLAGED'), entrada(12, 'PILLAGED')], 10, null), null);
    expect(unseenEntries(s).map((e) => e.id)).toEqual([14]);
  });

  it('avisa si lo nuevo sigue en páginas sin cargar', () => {
    const s = applyEventLogPage(EMPTY_EVENT_LOG, pagina([entrada(12, 'PILLAGED'), entrada(10, 'UNIT_LOST')], 0, 10), null);
    expect(moreUnseenBeyond(s)).toBe(true);
    expect(moreUnseenBeyond(markSeenLocally(s, 12))).toBe(false);
  });

  it('vuelve a pedir el registro sólo tras un rato con la pestaña oculta (RN-LOG-007)', () => {
    expect(shouldRefetchOnVisible(null, 100_000)).toBe(false);
    expect(shouldRefetchOnVisible(50_000, 100_000)).toBe(false);
    expect(shouldRefetchOnVisible(40_000, 100_000)).toBe(true);
  });
});

describe('texto de cada entrada', () => {
  it('CITY_FOUNDED dice dónde se fundó la segunda ciudad (SPEC-MULTI-CITY)', () => {
    expect(entryText(entrada(1, 'CITY_FOUNDED', { otherPlayerId: null, otherCityId: null, otherCityName: null, x: 40, y: 40 })))
      .toBe('Fundaste una ciudad nueva cerca de (40, 40)');
  });

  it('cuenta cada tipo desde el punto de vista del jugador', () => {
    expect(entryText(entrada(1, 'UNIT_LOST', { count: 2, unitType: 'VILLAGER', x: 451, y: 179 })))
      .toBe('Villa Norte mató a 2 de tus aldeanos cerca de (451, 179)');
    expect(entryText(entrada(1, 'UNIT_LOST', { unitType: 'WARRIOR' }))).toBe('Villa Norte mató a uno de tus guerreros');
    // SPEC-CIVILIZATION-UNITS §4.6: las muertes nombran la unidad, con su plural.
    expect(entryText(entrada(1, 'UNIT_LOST', { unitType: 'HORSE_ARCHER', count: 2 })))
      .toBe('Villa Norte mató a 2 de tus arqueros a caballo');
    expect(entryText(entrada(1, 'UNIT_LOST', { unitType: 'CATAPHRACT' }))).toBe('Villa Norte mató a uno de tus catafractos');
    expect(entryText(entrada(1, 'ENEMY_KILLED', { unitType: 'WARRIOR' }))).toBe('Tus unidades mataron a un guerrero de Villa Norte');
    // SPEC-CASTLE-AGE §4.7: lo que mata un castillo, a los dos lados.
    expect(entryText(entrada(1, 'UNIT_LOST_TO_CASTLE', { unitType: 'BERSERKER', x: 22, y: 41 })))
      .toBe('El castillo de Villa Norte mató a uno de tus berserkers cerca de (22, 41)');
    expect(entryText(entrada(1, 'CASTLE_KILL', { unitType: 'WARRIOR', count: 3 })))
      .toBe('Tu castillo mató a 3 guerreros de Villa Norte');
    expect(entryText(entrada(1, 'PILLAGED', { count: 17, amount: 85 }))).toBe('Villa Norte te robó 85 de madera en 17 golpes');
    expect(entryText(entrada(1, 'PILLAGE_DONE', { amount: 40 }))).toBe('Tus guerreros robaron 40 de madera a Villa Norte');
    expect(entryText(entrada(1, 'HOUSE_LOST', { count: 2 }))).toBe('Villa Norte derribó 2 de tus casas');
    expect(entryText(entrada(1, 'HOUSE_LOST'))).toBe('Villa Norte derribó una de tus casas');
    expect(entryText(entrada(1, 'HOUSE_DESTROYED'))).toBe('Tus guerreros derribaron una casa de Villa Norte');
    expect(entryText(entrada(1, 'HOUSE_DESTROYED', { count: 3 }))).toBe('Tus guerreros derribaron 3 casas de Villa Norte');
    expect(isLoss(entrada(1, 'HOUSE_LOST')), 'perder una casa se resalta').toBe(true);
    expect(isLoss(entrada(1, 'HOUSE_DESTROYED'))).toBe(false);
    expect(entryText(entrada(1, 'TREATY_OFFERED', { treatyType: 'NON_AGGRESSION' }))).toBe('Villa Norte te ofreció un pacto de no agresión');
    expect(entryText(entrada(1, 'TREATY_OFFERED', { treatyType: 'ALLIANCE' }))).toBe('Villa Norte te ofreció una alianza');
    expect(entryText(entrada(1, 'TREATY_ACCEPTED', { treatyType: 'ALLIANCE' }))).toBe('Villa Norte aceptó tu oferta de alianza');
    expect(entryText(entrada(1, 'TREATY_REJECTED', { treatyType: 'ALLIANCE' }))).toBe('Villa Norte rechazó tu oferta de alianza');
    expect(entryText(entrada(1, 'TREATY_WITHDRAWN', { treatyType: 'ALLIANCE' }))).toBe('Villa Norte retiró su oferta de alianza');
    expect(entryText(entrada(1, 'TREATY_OFFER_EXPIRED', { treatyType: 'ALLIANCE', proposedByMe: true })))
      .toBe('Tu oferta de alianza a Villa Norte caducó sin respuesta');
    expect(entryText(entrada(1, 'TREATY_OFFER_EXPIRED', { treatyType: 'ALLIANCE', proposedByMe: false })))
      .toBe('La oferta de alianza de Villa Norte caducó');
    expect(entryText(entrada(1, 'TREATY_BREAK_ANNOUNCED', { treatyType: 'NON_AGGRESSION' })))
      .toBe('Villa Norte rompió el pacto de no agresión: la paz termina');
    expect(entryText(entrada(1, 'TREATY_ENDED', { treatyType: 'ALLIANCE' })))
      .toBe('La alianza con Villa Norte terminó; tus unidades guarnecidas salieron de su ciudad');
    expect(entryText(entrada(1, 'TREATY_ENDED', { treatyType: 'NON_AGGRESSION' }))).toBe('El pacto de no agresión con Villa Norte terminó');
  });

  it('las caravanas, con lo que llevaban; perderlas se resalta (SPEC-CARAVANS §6)', () => {
    const cargo = [{ resource: 'STONE' as const, amount: 100 }];
    expect(entryText(entrada(1, 'CARAVAN_ARRIVED', { cargo }))).toBe('Llegó la caravana de Villa Norte con 100 de piedra');
    expect(entryText(entrada(1, 'CARAVAN_CAPTURED', { cargo, x: 30, y: 31 })))
      .toBe('Villa Norte capturó tu caravana con 100 de piedra cerca de (30, 31)');
    expect(entryText(entrada(1, 'CARAVAN_INTERCEPTED', { cargo })))
      .toBe('Villa Norte capturó la caravana que te traía 100 de piedra');
    expect(entryText(entrada(1, 'CARAVAN_LOST', { cargo })))
      .toBe('Villa Norte capturó tu caravana con 100 de piedra, y se perdió: no había camino hasta su ciudad');
    expect(isLoss(entrada(1, 'CARAVAN_ARRIVED', { cargo }))).toBe(false);
    for (const kind of ['CARAVAN_CAPTURED', 'CARAVAN_INTERCEPTED', 'CARAVAN_LOST'] as const) {
      expect(isLoss(entrada(1, kind, { cargo })), kind).toBe(true);
    }
  });

  it('el nombre de la ciudad no se toca, tampoco al principio', () => {
    expect(entryText(entrada(1, 'PILLAGED', { amount: 5, otherCityName: 'smoke_runnerpolis' }))).toBe('smoke_runnerpolis te robó 5 de madera');
  });

  it('sin ciudad, el otro es «otro jugador»', () => {
    expect(entryText(entrada(1, 'PILLAGED', { amount: 5, otherCityId: null, otherCityName: null }))).toBe('Otro jugador te robó 5 de madera');
  });

  it('dice cuánto hace', () => {
    const ahora = 10 * 86_400_000;
    expect(timeAgo(ahora - 10_000, ahora)).toBe('hace un momento');
    expect(timeAgo(ahora - 5 * 60_000, ahora)).toBe('hace 5 min');
    expect(timeAgo(ahora - 3 * 3_600_000, ahora)).toBe('hace 3 h');
    expect(timeAgo(ahora - 86_400_000, ahora)).toBe('hace 1 día');
    expect(timeAgo(ahora - 2 * 86_400_000, ahora)).toBe('hace 2 días');
  });
});

describe('entryText: los tratos (SPEC-TRADE §6)', () => {
  const madera = [{ resource: 'WOOD' as const, amount: 300 }];
  const piedra = [{ resource: 'STONE' as const, amount: 100 }];
  it('cada uno, con lo que salía del almacén propio y lo que entraba', () => {
    expect(entryText(entrada(1, 'TRADE_OFFERED', { tradeGive: piedra, tradeGet: madera }))).toBe(
      'Villa Norte te ofreció 300 de madera por 100 de piedra',
    );
    expect(entryText(entrada(1, 'TRADE_ACCEPTED', { tradeGive: madera, tradeGet: piedra }))).toBe(
      'Villa Norte aceptó tu trato: diste 300 de madera y recibiste 100 de piedra',
    );
    expect(entryText(entrada(1, 'TRADE_REJECTED', { tradeGive: madera, tradeGet: piedra }))).toBe(
      'Villa Norte rechazó tu oferta de 300 de madera por 100 de piedra',
    );
    expect(entryText(entrada(1, 'TRADE_WITHDRAWN', { tradeGive: piedra, tradeGet: madera }))).toBe(
      'Villa Norte retiró su oferta de 300 de madera por 100 de piedra',
    );
    expect(entryText(entrada(1, 'TRADE_OFFER_EXPIRED', { proposedByMe: true }))).toBe('Tu oferta de comercio a Villa Norte caducó');
    expect(entryText(entrada(1, 'TRADE_OFFER_EXPIRED', { proposedByMe: false }))).toBe('La oferta de comercio de Villa Norte caducó');
  });
  it('romper un acuerdo comercial termina el comercio, no la paz', () => {
    expect(entryText(entrada(1, 'TREATY_BREAK_ANNOUNCED', { treatyType: 'TRADE' }))).toBe(
      'Villa Norte rompió el acuerdo comercial: el comercio termina',
    );
    expect(entryText(entrada(1, 'TREATY_BREAK_ANNOUNCED', { treatyType: 'ALLIANCE' }))).toBe(
      'Villa Norte rompió la alianza: la paz termina',
    );
  });
});

describe('entryText: la conquista (SPEC-TERRITORY-CONQUEST §6)', () => {
  it('nombra el territorio, y la reclamación y la pérdida se resaltan', () => {
    const t = { territoryId: 12, territoryName: 'Llanuras' };
    expect(entryText(entrada(1, 'TERRITORY_CLAIMED', t))).toBe('Villa Norte reclamó tu territorio Llanuras');
    expect(entryText(entrada(2, 'TERRITORY_LOST', t))).toBe('Villa Norte te quitó el territorio Llanuras');
    expect(entryText(entrada(3, 'TERRITORY_TAKEN', t))).toBe('Tus guerreros conquistaron Llanuras a Villa Norte');
    expect(isLoss(entrada(1, 'TERRITORY_CLAIMED', t))).toBe(true);
    expect(isLoss(entrada(2, 'TERRITORY_LOST', t))).toBe(true);
    expect(isLoss(entrada(3, 'TERRITORY_TAKEN', t))).toBe(false);
  });
});

describe('entryText: los territorios de clan (SPEC-CLAN-TERRITORIES §6)', () => {
  it('dice «de tu clan» al reclamarlo o perderlo, y «para tu clan» al ganarlo', () => {
    const t = { territoryId: 12, territoryName: 'Llanuras', clanTag: 'LOB' };
    expect(entryText(entrada(1, 'TERRITORY_CLAIMED', t))).toBe('Villa Norte reclamó Llanuras, de tu clan [LOB]');
    expect(entryText(entrada(2, 'TERRITORY_LOST', t))).toBe('Villa Norte le quitó a tu clan [LOB] Llanuras');
    expect(entryText(entrada(3, 'TERRITORY_TAKEN', t))).toBe('Tus guerreros ganaron Llanuras para tu clan [LOB], a Villa Norte');
    expect(isLoss(entrada(2, 'TERRITORY_LOST', t))).toBe(true);
  });
});

describe('el liderazgo de un clan (SPEC-CLAN-GOVERNANCE Q8)', () => {
  it('a quien lo recibe y a quien lo cede o lo pierde, con la sigla; perderlo se resalta', () => {
    expect(entryText(entrada(1, 'CLAN_LEADERSHIP_RECEIVED', { clanTag: 'LOB' }))).toBe('Villa Norte te cedió el liderazgo de [LOB]');
    expect(entryText(entrada(1, 'CLAN_LEADERSHIP_GIVEN', { clanTag: 'LOB' }))).toBe('Cediste el liderazgo de [LOB] a Villa Norte');
    expect(entryText(entrada(1, 'CLAN_LEADERSHIP_INHERITED', { clanTag: 'LOB' })))
      .toBe('Ahora lideras [LOB]: Villa Norte llevaba 7 días sin jugar');
    expect(entryText(entrada(1, 'CLAN_LEADERSHIP_LOST', { clanTag: 'LOB' })))
      .toBe('Perdiste el liderazgo de [LOB] por inactividad; ahora lo tiene Villa Norte');
    expect(entryText(entrada(1, 'CLAN_LEADERSHIP_RECEIVED')), 'sin sigla').toBe('Villa Norte te cedió el liderazgo de tu clan');
    expect(isLoss(entrada(1, 'CLAN_LEADERSHIP_LOST'))).toBe(true);
    expect(isLoss(entrada(1, 'CLAN_LEADERSHIP_GIVEN'))).toBe(false);
    // SPEC-CLAN-ALLIANCES Q9: con la sigla del otro clan.
    expect(entryText(entrada(1, 'CLAN_ALLIANCE_OFFERED', { clanTag: 'OSO' }))).toBe('Villa Norte propone una alianza entre [OSO] y tu clan');
    expect(entryText(entrada(1, 'CLAN_ALLIANCE_ACCEPTED', { clanTag: 'OSO' }))).toBe('Villa Norte aceptó la alianza entre [OSO] y tu clan');
    expect(entryText(entrada(1, 'CLAN_ALLIANCE_REJECTED', { clanTag: 'OSO' }))).toBe('Villa Norte rechazó la alianza entre [OSO] y tu clan');
    expect(entryText(entrada(1, 'CLAN_ALLIANCE_BREAK_ANNOUNCED', { clanTag: 'OSO' })))
      .toBe('La alianza de tu clan con [OSO] termina en 15 minutos: después, sus miembros podrán atacar a los tuyos');
    expect(entryText(entrada(1, 'CLAN_ALLIANCE_BREAK_ANNOUNCED', { clanTag: 'OSO', breaksAtMs: Date.UTC(2026, 10, 16, 15, 30) })))
      .toMatch(/^La alianza de tu clan con \[OSO\] termina a las \d{2}:\d{2}: después/);
    expect(isLoss(entrada(1, 'CLAN_ALLIANCE_BREAK_ANNOUNCED'))).toBe(true);
    expect(isLoss(entrada(1, 'CLAN_ALLIANCE_OFFERED'))).toBe(false);
  });

  it('las guerras entre clanes, con la sigla del otro clan y el marcador (SPEC-CLAN-WARS Q11)', () => {
    expect(entryText(entrada(1, 'CLAN_WAR_DECLARED', { clanTag: 'OSO' })))
      .toBe('Villa Norte declaró la guerra entre tu clan y [OSO]: empieza en 15 minutos');
    expect(entryText(entrada(1, 'CLAN_WAR_DECLARED', { clanTag: 'OSO', startsAtMs: Date.UTC(2026, 10, 17, 15, 30) })))
      .toMatch(/^Villa Norte declaró la guerra entre tu clan y \[OSO\]: empieza a las \d{2}:\d{2}$/);
    expect(entryText(entrada(1, 'CLAN_WAR_PEACE_OFFERED', { clanTag: 'OSO' }))).toBe('Villa Norte ofrece la paz entre [OSO] y tu clan');
    expect(entryText(entrada(1, 'CLAN_WAR_ENDED', { clanTag: 'OSO', warResult: 'WON', warReason: 'TIMEOUT', ourScore: 31, theirScore: 18 })))
      .toBe('La guerra con [OSO] terminó: victoria por 31 – 18 (plazo)');
    expect(entryText(entrada(1, 'CLAN_WAR_ENDED', { clanTag: 'OSO', warResult: 'DRAWN', warReason: 'PEACE', ourScore: 0, theirScore: 0 })))
      .toBe('La guerra con [OSO] terminó: empate por 0 – 0 (paz pactada)');
    expect(entryText(entrada(1, 'CLAN_WAR_ENDED', { clanTag: 'OSO', warResult: 'LOST', warReason: 'SURRENDER', ourScore: 2, theirScore: 9 })))
      .toBe('La guerra con [OSO] terminó: derrota por 2 – 9 (rendición)');
    expect(entryText(entrada(1, 'TREATY_BREAK_ANNOUNCED', { treatyType: 'NON_AGGRESSION', breakReason: 'WAR' })))
      .toBe('La guerra entre vuestros clanes rompe el pacto de no agresión con Villa Norte: la paz termina');
    expect(isLoss(entrada(1, 'CLAN_WAR_DECLARED'))).toBe(true);
    expect(isLoss(entrada(1, 'CLAN_WAR_ENDED', { warResult: 'LOST' }))).toBe(true);
    expect(isLoss(entrada(1, 'CLAN_WAR_ENDED', { warResult: 'WON' }))).toBe(false);
    expect(isLoss(entrada(1, 'CLAN_WAR_PEACE_OFFERED'))).toBe(false);
  });
});
