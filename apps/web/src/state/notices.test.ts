import { describe, expect, it } from 'vitest';

import {
  applyNotice,
  documentTitle,
  EMPTY_NOTICES,
  hasUnreadNotices,
  markLogOpened,
  MAX_VISIBLE_NOTICES,
  nextNoticeExpiry,
  NOTICE_VISIBLE_MS,
  noticeIsLoss,
  noticeText,
  noticeTile,
  setTabHidden,
  visibleNotices,
  type Notice,
  type NoticeGroup,
  type NoticeState,
} from './notices';

const norte = '00000000-0000-4000-8000-000000000002';
const sur = '00000000-0000-4000-8000-000000000003';
const t0 = 1_800_000_000_000;

function aviso(kind: Notice['kind'], atMs: number, extra: Partial<Notice> = {}): Notice {
  return {
    kind, atMs, otherPlayerId: norte, otherCityId: 7, otherCityName: 'Villa Norte', x: 451, y: 179, ...extra,
  };
}

function con(...avisos: Notice[]): NoticeState {
  return avisos.reduce(applyNotice, EMPTY_NOTICES);
}

/** El grupo más reciente; el test falla si no hay ninguno. */
function primero(s: NoticeState): NoticeGroup {
  const g = s.groups[0];
  if (!g) throw new Error('sin grupos');
  return g;
}

describe('agrupación (SPEC-LIVE-NOTICES Q3, RN-NOTICE-006)', () => {
  it('junta los del mismo tipo, otro jugador y unidad mientras el grupo se ve, con su recuento', () => {
    const s = con(
      aviso('UNIT_LOST', t0, { unitType: 'VILLAGER' }),
      aviso('UNIT_LOST', t0 + 2_000, { unitType: 'VILLAGER', x: 460, y: 180 }),
      aviso('UNIT_LOST', t0 + 4_000, { unitType: 'VILLAGER', x: 470, y: 181 }),
    );
    expect(s.groups).toHaveLength(1);
    expect(s.groups[0]).toMatchObject({ count: 3, firstAtMs: t0, lastAtMs: t0 + 4_000, x: 470, y: 181 });
    expect(noticeText(primero(s))).toBe('Villa Norte mató a 3 de tus aldeanos cerca de (470, 181)');
  });

  it('un saqueo es un solo aviso que crece, con la madera de todos sus golpes', () => {
    let s = EMPTY_NOTICES;
    for (let i = 0; i < 30; i++) s = applyNotice(s, aviso('PILLAGE_DONE', t0 + i * 1_500, { amount: 5 }));
    expect(s.groups).toHaveLength(1);
    expect(s.groups[0]).toMatchObject({ count: 30, amount: 150 });
    expect(noticeText(primero(s))).toBe('Tus guerreros robaron 150 de madera a Villa Norte');
  });

  it('otro tipo, otro jugador u otra unidad abren otro grupo', () => {
    const s = con(
      aviso('UNIT_LOST', t0, { unitType: 'VILLAGER' }),
      aviso('UNIT_LOST', t0 + 100, { unitType: 'WARRIOR' }),
      aviso('UNIT_LOST', t0 + 200, { unitType: 'VILLAGER', otherPlayerId: sur, otherCityName: 'Villa Sur' }),
      aviso('ENEMY_KILLED', t0 + 300, { unitType: 'VILLAGER' }),
    );
    expect(s.groups).toHaveLength(4);
  });

  it('pasado el tiempo en que se ve, el mismo suceso abre un grupo nuevo', () => {
    const s = con(aviso('HOUSE_DESTROYED', t0), aviso('HOUSE_DESTROYED', t0 + NOTICE_VISIBLE_MS));
    expect(s.groups.map((g) => g.count)).toEqual([1, 1]);
  });
});

describe('qué se ve (Q2)', () => {
  it('cada grupo se ve 8 s desde su último aviso', () => {
    const s = con(aviso('ENEMY_KILLED', t0, { unitType: 'WARRIOR' }));
    expect(visibleNotices(s, t0 + NOTICE_VISIBLE_MS - 1)).toHaveLength(1);
    expect(visibleNotices(s, t0 + NOTICE_VISIBLE_MS)).toHaveLength(0);
    // Un aviso del mismo grupo lo renueva.
    const renovado = applyNotice(s, aviso('ENEMY_KILLED', t0 + 7_000, { unitType: 'WARRIOR' }));
    expect(visibleNotices(renovado, t0 + 10_000)).toHaveLength(1);
    expect(nextNoticeExpiry(renovado, t0 + 10_000)).toBe(t0 + 7_000 + NOTICE_VISIBLE_MS);
    expect(nextNoticeExpiry(renovado, t0 + 20_000)).toBeNull();
  });

  it('tres a la vez como mucho, los más recientes primero', () => {
    const s = con(
      aviso('UNIT_LOST', t0, { unitType: 'VILLAGER' }),
      aviso('ENEMY_KILLED', t0 + 100, { unitType: 'VILLAGER' }),
      aviso('PILLAGE_DONE', t0 + 200, { amount: 5 }),
      aviso('HOUSE_DESTROYED', t0 + 300),
    );
    const vistos = visibleNotices(s, t0 + 1_000);
    expect(vistos).toHaveLength(MAX_VISIBLE_NOTICES);
    expect(vistos.map((g) => g.kind)).toEqual(['HOUSE_DESTROYED', 'PILLAGE_DONE', 'ENEMY_KILLED']);
  });

  it('el texto es el del registro, también sin ciudad del otro, y las pérdidas se resaltan', () => {
    const perdida = primero(con(aviso('UNIT_LOST', t0, { unitType: 'WARRIOR', otherCityId: null, otherCityName: null })));
    expect(noticeText(perdida)).toBe('Otro jugador mató a uno de tus guerreros cerca de (451, 179)');
    expect(noticeIsLoss(perdida)).toBe(true);
    const casa = primero(con(aviso('HOUSE_DESTROYED', t0)));
    expect(noticeText(casa)).toBe('Tus guerreros derribaron una casa de Villa Norte');
    expect(noticeIsLoss(casa)).toBe(false);
  });
});

describe('«Ir» (Q4)', () => {
  it('lleva al tile del último aviso, y no aparece si el aviso no lo trae', () => {
    const g = primero(con(aviso('PILLAGE_DONE', t0, { amount: 5 }), aviso('PILLAGE_DONE', t0 + 1_500, { amount: 5, x: 452, y: 180 })));
    expect(noticeTile(g)).toEqual({ x: 452, y: 180 });
    const sinTile = primero(con(aviso('HOUSE_DESTROYED', t0, { x: undefined, y: undefined })));
    expect(noticeTile(sinTile)).toBeNull();
  });
});

describe('el punto en «Registro» (RN-NOTICE-004)', () => {
  it('está mientras hay avisos desde que se abrió el registro, y abrirlo lo borra', () => {
    expect(hasUnreadNotices(EMPTY_NOTICES)).toBe(false);
    const s = con(aviso('UNIT_LOST', t0, { unitType: 'VILLAGER' }));
    expect(hasUnreadNotices(s)).toBe(true);
    const abierto = markLogOpened(s);
    expect(hasUnreadNotices(abierto)).toBe(false);
    expect(abierto.groups).toEqual(s.groups);
    expect(markLogOpened(abierto)).toBe(abierto);
  });
});

describe('el título de la pestaña (Q7)', () => {
  it('con la pestaña oculta cuenta los grupos que reciben algo, y al volver se borra', () => {
    let s = setTabHidden(con(aviso('ENEMY_KILLED', t0, { unitType: 'VILLAGER' })), true);
    expect(documentTitle('Empires Online', s)).toBe('Empires Online');
    for (let i = 0; i < 30; i++) s = applyNotice(s, aviso('PILLAGE_DONE', t0 + i * 1_500, { amount: 5 }));
    expect(documentTitle('Empires Online', s)).toBe('(1) Empires Online');
    s = applyNotice(s, aviso('ENEMY_KILLED', t0 + 50_000, { unitType: 'VILLAGER' }));
    expect(documentTitle('Empires Online', s)).toBe('(2) Empires Online');
    s = setTabHidden(s, false);
    expect(documentTitle('Empires Online', s)).toBe('Empires Online');
    expect(s.groups.length).toBeGreaterThan(0);
  });

  it('con la pestaña a la vista no cuenta nada', () => {
    const s = con(aviso('UNIT_LOST', t0, { unitType: 'VILLAGER' }));
    expect(documentTitle('Empires Online', s)).toBe('Empires Online');
  });
});

describe('avisos de territorio (SPEC-TERRITORY-CONQUEST Q8)', () => {
  it('se agrupan por territorio: dos territorios distintos son dos avisos, con su nombre', () => {
    const s = con(
      aviso('TERRITORY_LOST', t0, { territoryId: 12, territoryName: 'Llanuras' }),
      aviso('TERRITORY_LOST', t0 + 100, { territoryId: 13, territoryName: 'Colinas' }),
    );
    expect(s.groups).toHaveLength(2);
    expect(noticeText(primero(s))).toBe('Villa Norte te quitó el territorio Colinas');
    expect(noticeIsLoss(primero(s))).toBe(true);
  });
});
