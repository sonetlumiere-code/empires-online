/**
 * Los avisos en vivo en el cliente (SPEC-LIVE-NOTICES, ADR-033 §7): lo que les
 * pasó a tus unidades fuera de la vista, en el momento en que pasó.
 *
 * Es un estado aparte del registro: un aviso no tiene `id`, no mueve el cursor y
 * no entra en la lista, que sigue saliendo sólo de `event_log.page`
 * (RN-NOTICE-004). Aquí se agrupan, se decide cuáles se ven y se lleva la cuenta
 * del punto en «Registro» y del título de la pestaña. Como el resto del estado,
 * no decide nada del juego: qué se avisa y a quién lo decidió el servidor.
 * Ver ../../../../docs/specs/live-notices.md
 */
import type { v1 } from '@empires-online/protocol';

import { entryText, isLoss, type EventLogEntry } from './eventLog';

export type Notice = v1.EventLogNotice['payload'];
export type NoticeKind = Notice['kind'];

/** Cuánto se ve un grupo desde su último aviso (Q2). */
export const NOTICE_VISIBLE_MS = 8_000;
/** Cuántos grupos se ven a la vez, los más recientes (Q2). */
export const MAX_VISIBLE_NOTICES = 3;
/** Cuántos grupos se guardan: los que salen de la franja ya están en el registro. */
const MAX_KEPT_GROUPS = 10;

/** Avisos del mismo tipo, con el mismo otro jugador y la misma unidad (Q3). */
export interface NoticeGroup {
  readonly key: string;
  readonly kind: NoticeKind;
  readonly otherPlayerId: string;
  readonly otherCityId: number | null;
  readonly otherCityName: string | null;
  readonly unitType?: string;
  /** Cuántos avisos lleva el grupo. */
  readonly count: number;
  /** En el saqueo, la madera de todos sus golpes. */
  readonly amount?: number;
  /** El tile del último aviso, para «Ir». */
  readonly x?: number;
  readonly y?: number;
  /** El primero y el último, en la simulación. */
  readonly firstAtMs: number;
  readonly lastAtMs: number;
}

export interface NoticeState {
  /** Grupos, del más reciente al más viejo. */
  readonly groups: readonly NoticeGroup[];
  /** Avisos llegados desde que se abrió el registro por última vez: el punto en «Registro». */
  readonly sinceLogOpened: number;
  /** Si la pestaña está oculta. */
  readonly hidden: boolean;
  /** Los grupos que recibieron algo con la pestaña oculta: el contador del título (Q7). */
  readonly hiddenKeys: readonly string[];
}

export const EMPTY_NOTICES: NoticeState = { groups: [], sinceLogOpened: 0, hidden: false, hiddenKeys: [] };

/** La clave de agrupación, la del registro sin el lado del cursor (ADR-028 §4). */
export function noticeKey(n: Pick<Notice, 'kind' | 'otherPlayerId' | 'unitType'>): string {
  return `${n.kind}|${n.otherPlayerId}|${n.unitType ?? ''}`;
}

/**
 * Suma un aviso. Se junta con el grupo de su clave si ése todavía se ve —llega
 * antes de que pasen `NOTICE_VISIBLE_MS` desde su último aviso—; si no, abre otro.
 * Los instantes son los de la simulación, así que no hace falta reloj.
 */
export function applyNotice(state: NoticeState, n: Notice): NoticeState {
  const key = noticeKey(n);
  const open = state.groups.find((g) => g.key === key && n.atMs < g.lastAtMs + NOTICE_VISIBLE_MS);
  const group: NoticeGroup = open
    ? {
        ...open,
        count: open.count + 1,
        amount: n.amount !== undefined ? (open.amount ?? 0) + n.amount : open.amount,
        x: n.x ?? open.x,
        y: n.y ?? open.y,
        otherCityId: n.otherCityId,
        otherCityName: n.otherCityName,
        lastAtMs: Math.max(open.lastAtMs, n.atMs),
      }
    : {
        key,
        kind: n.kind,
        otherPlayerId: n.otherPlayerId,
        otherCityId: n.otherCityId,
        otherCityName: n.otherCityName,
        unitType: n.unitType,
        count: 1,
        amount: n.amount,
        x: n.x,
        y: n.y,
        firstAtMs: n.atMs,
        lastAtMs: n.atMs,
      };
  const groups = [group, ...state.groups.filter((g) => g !== open)]
    .sort((a, b) => b.lastAtMs - a.lastAtMs)
    .slice(0, MAX_KEPT_GROUPS);
  const hiddenKeys = state.hidden && !state.hiddenKeys.includes(key) ? [...state.hiddenKeys, key] : state.hiddenKeys;
  return { ...state, groups, sinceLogOpened: state.sinceLogOpened + 1, hiddenKeys };
}

/** Los grupos que se ven ahora, los más recientes primero, tres como mucho (Q2). */
export function visibleNotices(state: NoticeState, nowMs: number): NoticeGroup[] {
  return state.groups.filter((g) => nowMs < g.lastAtMs + NOTICE_VISIBLE_MS).slice(0, MAX_VISIBLE_NOTICES);
}

/** Cuándo deja de verse el siguiente grupo visible, para volver a pintar; `null` si ninguno se ve. */
export function nextNoticeExpiry(state: NoticeState, nowMs: number): number | null {
  const ends = visibleNotices(state, nowMs).map((g) => g.lastAtMs + NOTICE_VISIBLE_MS);
  return ends.length > 0 ? Math.min(...ends) : null;
}

/** Abrir el registro borra el punto: la página que se pide trae lo avisado. */
export function markLogOpened(state: NoticeState): NoticeState {
  return state.sinceLogOpened === 0 ? state : { ...state, sinceLogOpened: 0 };
}

/** Si «Registro» lleva el punto de novedades. */
export function hasUnreadNotices(state: NoticeState): boolean {
  return state.sinceLogOpened > 0;
}

/** La pestaña se oculta o vuelve; al volver, el contador se borra (Q7). */
export function setTabHidden(state: NoticeState, hidden: boolean): NoticeState {
  if (hidden) return state.hidden ? state : { ...state, hidden: true };
  return state.hidden || state.hiddenKeys.length > 0 ? { ...state, hidden: false, hiddenKeys: [] } : state;
}

/** El título de la pestaña: con avisos llegados mientras estaba oculta, «(2) Empires Online». */
export function documentTitle(base: string, state: NoticeState): string {
  return state.hiddenKeys.length > 0 ? `(${state.hiddenKeys.length}) ${base}` : base;
}

/** El grupo como la entrada del registro que contaría lo mismo: el aviso se lee igual. */
function asEntry(g: NoticeGroup): EventLogEntry {
  return {
    id: 0,
    kind: g.kind,
    firstAtMs: g.firstAtMs,
    lastAtMs: g.lastAtMs,
    count: g.count,
    otherPlayerId: g.otherPlayerId,
    otherCityId: g.otherCityId,
    otherCityName: g.otherCityName,
    unitType: g.unitType as EventLogEntry['unitType'],
    amount: g.amount,
    x: g.x,
    y: g.y,
  };
}

/** El texto del aviso: el mismo `entryText` del registro. */
export function noticeText(g: NoticeGroup): string {
  return entryText(asEntry(g));
}

/** Si el aviso cuenta una pérdida: se resalta como en el registro. */
export function noticeIsLoss(g: NoticeGroup): boolean {
  return isLoss(asEntry(g));
}

/** El tile de «Ir», o `null` si el aviso no lo trae (Q4). */
export function noticeTile(g: NoticeGroup): { x: number; y: number } | null {
  return g.x !== undefined && g.y !== undefined ? { x: g.x, y: g.y } : null;
}
