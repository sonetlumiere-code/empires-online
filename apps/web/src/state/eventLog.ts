/**
 * El registro en el cliente (SPEC-EVENT-LOG): lo que el servidor contó que pasó.
 *
 * Como el resto del estado, no decide nada: guarda las páginas que llegan, dice
 * qué entradas son nuevas y cómo se leen. Qué entra, cómo se agrupa y hasta dónde
 * se marca lo visto lo decide el servidor (ADR-028).
 * Ver ../../../../docs/specs/event-log.md
 */
import type { v1 } from '@empires-online/protocol';

import { unitTypeLabel } from './economy';
import { describeLines } from './trade';

export type EventLogEntry = v1.EventLogEntry;
export type EventLogPage = v1.EventLogPage['payload'];

export interface EventLogState {
  /** Entradas cargadas, de la más reciente a la más vieja. */
  readonly entries: readonly EventLogEntry[];
  /** El cursor de lo visto según el servidor, al servir la primera página. */
  readonly seenUpToId: number;
  /**
   * Hasta dónde dio el jugador por visto en esta pestaña. Puede ir por delante del
   * servidor, que no marca lo de los últimos segundos (ADR-028 §6): aquí no se le
   * vuelve a enseñar, y en la próxima sesión sí. Mejor repetir que perder.
   */
  readonly localSeenUpToId: number;
  /** El `beforeId` de la página siguiente; `null` si no hay más. */
  readonly nextBeforeId: number | null;
  /** Si ya llegó alguna página en esta sesión. */
  readonly loaded: boolean;
}

export const EMPTY_EVENT_LOG: EventLogState = {
  entries: [],
  seenUpToId: 0,
  localSeenUpToId: 0,
  nextBeforeId: null,
  loaded: false,
};

/**
 * Aplica una página. `before` es el `beforeId` con que se pidió: `null` para la
 * primera, que sustituye todo (un grupo que creció cambia de id); un número para
 * una página anterior, que se añade al final si sigue donde se quedó la lista.
 * Una página vieja que ya no encaja se descarta.
 */
export function applyEventLogPage(state: EventLogState, page: EventLogPage, before: number | null): EventLogState {
  if (before === null) {
    return {
      entries: [...page.entries],
      seenUpToId: page.seenUpToId,
      localSeenUpToId: state.localSeenUpToId,
      nextBeforeId: page.nextBeforeId,
      loaded: true,
    };
  }
  if (!state.loaded || before !== state.nextBeforeId) return state;
  const ids = new Set(state.entries.map((e) => e.id));
  return {
    ...state,
    entries: [...state.entries, ...page.entries.filter((e) => !ids.has(e.id))],
    nextBeforeId: page.nextBeforeId,
  };
}

/** El cursor efectivo: el del servidor o el de esta pestaña, el mayor. */
function seenCursor(state: EventLogState): number {
  return Math.max(state.seenUpToId, state.localSeenUpToId);
}

/** Si una entrada es nueva para el jugador. */
export function isUnseen(state: EventLogState, entry: EventLogEntry): boolean {
  return entry.id > seenCursor(state);
}

/** Las entradas nuevas, en el orden de la lista. */
export function unseenEntries(state: EventLogState): EventLogEntry[] {
  return state.entries.filter((e) => isUnseen(state, e));
}

/**
 * Si quedan entradas nuevas más allá de lo cargado: la última cargada es nueva y
 * hay más páginas. La ventana avisa de que el resto está en el Registro.
 */
export function moreUnseenBeyond(state: EventLogState): boolean {
  const last = state.entries[state.entries.length - 1];
  return state.nextBeforeId !== null && last !== undefined && isUnseen(state, last);
}

/** Hasta dónde marcar al dar por visto lo cargado; `null` si no hay nada nuevo. */
export function markTarget(state: EventLogState): number | null {
  const unseen = unseenEntries(state);
  if (unseen.length === 0) return null;
  return Math.max(...unseen.map((e) => e.id));
}

/** Lo da por visto en esta pestaña hasta `upToId`; nunca retrocede. */
export function markSeenLocally(state: EventLogState, upToId: number): EventLogState {
  if (upToId <= state.localSeenUpToId) return state;
  return { ...state, localSeenUpToId: upToId };
}

/**
 * Cuánto tiene que haber estado la pestaña oculta para volver a pedir el
 * registro al volver (RN-LOG-007): quien juega mirando ya ve lo que pasa.
 */
export const REFETCH_AFTER_HIDDEN_MS = 60_000;

export function shouldRefetchOnVisible(hiddenSinceMs: number | null, nowMs: number): boolean {
  return hiddenSinceMs !== null && nowMs - hiddenSinceMs >= REFETCH_AFTER_HIDDEN_MS;
}

const TREATY_WITH_ARTICLE: Record<string, string> = {
  NON_AGGRESSION: 'el pacto de no agresión',
  ALLIANCE: 'la alianza',
  TRADE: 'el acuerdo comercial',
};

const TREATY_NAME: Record<string, string> = {
  NON_AGGRESSION: 'pacto de no agresión',
  ALLIANCE: 'alianza',
  TRADE: 'acuerdo comercial',
};

function plural(word: string): string {
  return /[aeiou]$/.test(word) ? `${word}s` : `${word}es`;
}

/** «a uno de tus aldeanos» / «a 3 de tus guerreros». */
function ofYours(count: number, unitType: string | undefined): string {
  const units = plural(unitTypeLabel(unitType ?? 'unidad'));
  return count === 1 ? `a uno de tus ${units}` : `a ${count} de tus ${units}`;
}

/** «a un guerrero» / «a 3 guerreros». */
function some(count: number, unitType: string | undefined): string {
  const unit = unitTypeLabel(unitType ?? 'unidad');
  return count === 1 ? `a un ${unit}` : `a ${count} ${plural(unit)}`;
}

/** HH:MM en la hora local del jugador. */
export function clockTime(ms: number): string {
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

/** Cuánto hace: «hace un momento», «hace 5 min», «hace 3 h», «hace 2 días». */
export function timeAgo(atMs: number, nowMs: number): string {
  const s = Math.max(0, Math.floor((nowMs - atMs) / 1000));
  if (s < 60) return 'hace un momento';
  const m = Math.floor(s / 60);
  if (m < 60) return `hace ${m} min`;
  const h = Math.floor(m / 60);
  if (h < 24) return `hace ${h} h`;
  const d = Math.floor(h / 24);
  return d === 1 ? 'hace 1 día' : `hace ${d} días`;
}

/**
 * El texto de una entrada, siempre desde el punto de vista del jugador. El nombre
 * de la ciudad va tal cual, también al principio: es un nombre, no una palabra
 * que se pueda poner en mayúscula.
 */
export function entryText(e: EventLogEntry): string {
  const text = rawText(e);
  return e.otherCityName === null && text.startsWith(UNKNOWN) ? capitalize(text) : text;
}

/** Cómo se nombra al otro cuando no tiene ciudad. */
const UNKNOWN = 'otro jugador';

function rawText(e: EventLogEntry): string {
  const who = e.otherCityName ?? UNKNOWN;
  const tipo = TREATY_NAME[e.treatyType ?? ''] ?? 'tratado';
  const elTipo = TREATY_WITH_ARTICLE[e.treatyType ?? ''] ?? 'el tratado';
  const where = e.x !== undefined && e.y !== undefined ? ` cerca de (${e.x}, ${e.y})` : '';
  const wood = e.amount ?? 0;
  switch (e.kind) {
    case 'UNIT_LOST':
      return `${who} mató ${ofYours(e.count, e.unitType)}${where}`;
    case 'ENEMY_KILLED':
      return `Tus unidades mataron ${some(e.count, e.unitType)} de ${who}`;
    case 'PILLAGED':
      return `${who} te robó ${wood} de madera${e.count > 1 ? ` en ${e.count} golpes` : ''}`;
    case 'PILLAGE_DONE':
      return `Tus guerreros robaron ${wood} de madera a ${who}`;
    case 'HOUSE_LOST':
      return `${who} derribó ${e.count === 1 ? 'una de tus casas' : `${e.count} de tus casas`}`;
    case 'HOUSE_DESTROYED':
      return `Tus guerreros derribaron ${e.count === 1 ? 'una casa' : `${e.count} casas`} de ${who}`;
    case 'TREATY_OFFERED':
      return `${who} te ofreció ${tipo === 'alianza' ? 'una alianza' : `un ${tipo}`}`;
    case 'TREATY_WITHDRAWN':
      return `${who} retiró su oferta de ${tipo}`;
    case 'TREATY_ACCEPTED':
      return `${who} aceptó tu oferta de ${tipo}`;
    case 'TREATY_REJECTED':
      return `${who} rechazó tu oferta de ${tipo}`;
    case 'TREATY_OFFER_EXPIRED':
      return e.proposedByMe
        ? `Tu oferta de ${tipo} a ${who} caducó sin respuesta`
        : `La oferta de ${tipo} de ${who} caducó`;
    case 'TREATY_BREAK_ANNOUNCED':
      // TRADE no da paz (SPEC-TRADE §4.1): lo que termina es el comercio.
      return `${who} rompió ${elTipo}: ${e.treatyType === 'TRADE' ? 'el comercio' : 'la paz'} termina${
        e.breaksAtMs !== undefined ? ` a las ${clockTime(e.breaksAtMs)}` : ''
      }`;
    case 'TREATY_ENDED':
      return `${capitalize(elTipo)} con ${who} terminó${
        e.treatyType === 'ALLIANCE' ? '; tus unidades guarnecidas salieron de su ciudad' : ''
      }`;
    // Los tratos (SPEC-TRADE §6): cada uno, su entrada, con lo que salía del
    // almacén propio (tradeGive) y lo que entraba (tradeGet).
    case 'TRADE_OFFERED':
      return `${who} te ofreció ${describeLines(e.tradeGet ?? [])} por ${describeLines(e.tradeGive ?? [])}`;
    case 'TRADE_ACCEPTED':
      return `${who} aceptó tu trato: diste ${describeLines(e.tradeGive ?? [])} y recibiste ${describeLines(e.tradeGet ?? [])}`;
    case 'TRADE_REJECTED':
      return `${who} rechazó tu oferta de ${describeLines(e.tradeGive ?? [])} por ${describeLines(e.tradeGet ?? [])}`;
    case 'TRADE_WITHDRAWN':
      return `${who} retiró su oferta de ${describeLines(e.tradeGet ?? [])} por ${describeLines(e.tradeGive ?? [])}`;
    case 'TRADE_OFFER_EXPIRED':
      return e.proposedByMe
        ? `Tu oferta de comercio a ${who} caducó`
        : `La oferta de comercio de ${who} caducó`;
    // La conquista (SPEC-TERRITORY-CONQUEST §6), con el territorio por su nombre.
    case 'TERRITORY_CLAIMED':
      return `${who} reclamó tu territorio ${e.territoryName ?? ''}`.trimEnd();
    case 'TERRITORY_LOST':
      return `${who} te quitó el territorio ${e.territoryName ?? ''}`.trimEnd();
    case 'TERRITORY_TAKEN':
      return `Tus guerreros conquistaron ${e.territoryName ?? 'un territorio'} a ${who}`;
    // Las caravanas (SPEC-CARAVANS §6), cada una con lo que llevaba.
    case 'CARAVAN_ARRIVED':
      return `Llegó la caravana de ${who} con ${describeLines(e.cargo ?? [])}`;
    case 'CARAVAN_CAPTURED':
      return `${who} capturó ${e.count === 1 ? 'tu caravana' : `${e.count} de tus caravanas`} con ${describeLines(e.cargo ?? [])}${where}`;
    case 'CARAVAN_INTERCEPTED':
      return `${who} capturó la caravana que te traía ${describeLines(e.cargo ?? [])}${where}`;
    case 'CARAVAN_LOST':
      return `${who} capturó tu caravana con ${describeLines(e.cargo ?? [])}${where}, y se perdió: no había camino hasta su ciudad`;
    default:
      return `${who}: ${String(e.kind)}`;
  }
}

/** Si la entrada cuenta una pérdida: el HUD la resalta. */
export function isLoss(e: EventLogEntry): boolean {
  return (
    e.kind === 'UNIT_LOST' ||
    e.kind === 'PILLAGED' ||
    e.kind === 'HOUSE_LOST' ||
    e.kind === 'TREATY_BREAK_ANNOUNCED' ||
    e.kind === 'TERRITORY_CLAIMED' ||
    e.kind === 'TERRITORY_LOST' ||
    e.kind === 'CARAVAN_CAPTURED' ||
    e.kind === 'CARAVAN_INTERCEPTED' ||
    e.kind === 'CARAVAN_LOST'
  );
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}
