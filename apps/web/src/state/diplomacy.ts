/**
 * Decisiones puras de la diplomacia en el cliente (SPEC-DIPLOMACY).
 *
 * Como las de la economía, no deciden nada autoritativo: describen lo que el
 * servidor ya contó y qué órdenes tiene sentido ofrecer. El servidor acepta o
 * rechaza cada una. Ver ../../../../docs/architecture/frontend.md §4.5
 */
import type { v1 } from '@empires-online/protocol';

export type TreatyView = v1.TreatyView;

/** Estados en los que un tratado sigue en la lista del jugador. */
const OPEN = new Set(['PROPOSED', 'ACTIVE']);

/**
 * Aplica un treaty.update a la lista: lo añade o lo sustituye si sigue
 * abierto, y lo quita si terminó. Devuelve una lista nueva, ordenada por id.
 */
export function applyTreatyUpdate(list: readonly TreatyView[], t: TreatyView): TreatyView[] {
  const rest = list.filter((x) => x.id !== t.id);
  if (OPEN.has(t.status)) rest.push({ ...t });
  return rest.sort((a, b) => a.id - b.id);
}

/** Una orden que el HUD ofrece sobre un tratado. */
export type TreatyAction = 'accept' | 'reject' | 'withdraw' | 'break';

/**
 * Qué se puede hacer con un tratado (RN-DIPLO-002, RN-DIPLO-003): una oferta
 * recibida se acepta o se rechaza; la propia, se retira; uno vigente se rompe,
 * salvo que ya esté en preaviso.
 */
export function treatyActions(t: TreatyView): TreatyAction[] {
  if (t.status === 'PROPOSED') return t.proposedByMe ? ['withdraw'] : ['accept', 'reject'];
  if (t.status === 'ACTIVE') return t.breaksAtMs === null ? ['break'] : [];
  return [];
}

const TYPE_LABEL: Record<string, string> = {
  NON_AGGRESSION: 'pacto de no agresión',
  ALLIANCE: 'alianza',
  TRADE: 'acuerdo comercial',
};

/** El texto de un tratado para el HUD. */
export function treatyLabel(t: TreatyView, serverNowMs: number): string {
  const who = t.otherCityName ?? 'otro jugador';
  const tipo = TYPE_LABEL[t.type] ?? t.type;
  if (t.status === 'PROPOSED') {
    return t.proposedByMe ? `Ofreces ${tipo} a ${who}` : `${who} te ofrece ${tipo}`;
  }
  if (t.breaksAtMs !== null) {
    return `${capitalize(tipo)} con ${who}: se rompe en ${formatCountdown(t.breaksAtMs - serverNowMs)}`;
  }
  return `${capitalize(tipo)} con ${who}`;
}

/** mm:ss de una cuenta atrás; nunca negativa. */
export function formatCountdown(ms: number): string {
  const s = Math.max(0, Math.ceil(ms / 1000));
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, '0')}`;
}

/** Si algún tratado está en preaviso: el HUD necesita refrescar la cuenta atrás. */
export function anyCountdown(list: readonly TreatyView[]): boolean {
  return list.some((t) => t.breaksAtMs !== null);
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** La escalera de tratados (SPEC-TRADE §4.1): cada escalón incluye lo del anterior. */
export const TREATY_LADDER = ['TRADE', 'NON_AGGRESSION', 'ALLIANCE'] as const;
export type ProposableType = (typeof TREATY_LADDER)[number];

/**
 * Qué tratados tiene sentido proponer a ese jugador: sin nada, los tres; con uno
 * vigente, sólo los de un escalón superior, y ninguno si está en preaviso o si ya
 * hay una oferta pendiente (RN-DIPLO-001, INV-DIPLO-002). El servidor decide.
 */
export function proposableTypes(list: readonly TreatyView[], otherPlayerId: string): ProposableType[] {
  const open = list.filter((t) => t.otherPlayerId === otherPlayerId);
  if (open.some((t) => t.status === 'PROPOSED')) return [];
  const active = open.find((t) => t.status === 'ACTIVE');
  if (!active) return [...TREATY_LADDER];
  if (active.breaksAtMs !== null) return [];
  const rank = TREATY_LADDER.indexOf(active.type as ProposableType);
  return TREATY_LADDER.slice(rank + 1);
}
