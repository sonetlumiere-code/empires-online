import { describe, expect, it } from 'vitest';

import {
  applyEventLogPage,
  EMPTY_EVENT_LOG,
  entryText,
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
  it('cuenta cada tipo desde el punto de vista del jugador', () => {
    expect(entryText(entrada(1, 'UNIT_LOST', { count: 2, unitType: 'VILLAGER', x: 451, y: 179 })))
      .toBe('Villa Norte mató a 2 de tus aldeanos cerca de (451, 179)');
    expect(entryText(entrada(1, 'UNIT_LOST', { unitType: 'WARRIOR' }))).toBe('Villa Norte mató a uno de tus guerreros');
    expect(entryText(entrada(1, 'ENEMY_KILLED', { unitType: 'WARRIOR' }))).toBe('Tus unidades mataron a un guerrero de Villa Norte');
    expect(entryText(entrada(1, 'PILLAGED', { count: 17, amount: 85 }))).toBe('Villa Norte te robó 85 de madera en 17 golpes');
    expect(entryText(entrada(1, 'PILLAGE_DONE', { amount: 40 }))).toBe('Tus guerreros robaron 40 de madera a Villa Norte');
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
