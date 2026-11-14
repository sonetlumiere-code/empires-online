/**
 * Los mensajes entre jugadores en el cliente (SPEC-MESSAGES): la bandeja, la
 * conversación abierta, el aviso de un mensaje que llega y la línea de «Mientras
 * no estabas».
 *
 * Como el resto del estado, no decide nada: quién puede escribir a quién, qué
 * texto se admite, el ritmo y lo leído los decide el servidor (ADR-038). Aquí se
 * guardan las vistas que llegan y se cuenta lo no leído para el HUD. El texto se
 * pinta siempre como texto, nunca como HTML.
 * Ver ../../../../docs/specs/messages.md
 */
import { v1 } from '@empires-online/protocol';

export type MessageView = v1.MessageView;
export type ConversationView = v1.ConversationView;
export type BlockedPlayerView = v1.BlockedPlayerView;
export type MessageInbox = v1.MessageInbox['payload'];
export type MessagePage = v1.MessagePage['payload'];

/** El máximo de caracteres de un mensaje (RN-MSG-003): el contador del cuadro de texto. */
export const MESSAGE_MAX_CHARS = v1.MESSAGE_MAX_CHARS;
/** Cuánto se ve el aviso de un mensaje que llega, como los avisos en vivo. */
export const MESSAGE_NOTICE_MS = 8_000;
/** El largo de la vista previa de la bandeja, el mismo que el del servidor. */
const PREVIEW_CHARS = 80;

/** La conversación abierta en el panel. */
export interface OpenConversation {
  readonly otherPlayerId: string;
  readonly otherCityId: number | null;
  readonly otherCityName: string | null;
  /** De más antiguo a más reciente. */
  readonly messages: readonly MessageView[];
  /** El `beforeId` de la página anterior; `null` si no hay más. */
  readonly nextBeforeId: number | null;
  readonly blockedByMe: boolean;
  /** Si llegó ya su primera página. */
  readonly loaded: boolean;
}

/** El último mensaje recibido con su conversación cerrada: el aviso del HUD (Q7). */
export interface IncomingNotice {
  readonly otherPlayerId: string;
  readonly otherCityName: string | null;
  readonly sentAtMs: number;
}

export interface MessagesState {
  /** Las conversaciones, de la más reciente a la más antigua. */
  readonly conversations: readonly ConversationView[];
  readonly blocked: readonly BlockedPlayerView[];
  /** Si llegó ya alguna bandeja en esta sesión. */
  readonly inboxLoaded: boolean;
  readonly open: OpenConversation | null;
  /** El rechazo del último envío, para el cuadro de texto de su conversación. */
  readonly sendError: { readonly otherPlayerId: string; readonly code: string; readonly message: string } | null;
  readonly incoming: IncomingNotice | null;
  /** Mensajes llegados con la pestaña oculta: el contador del título. */
  readonly hiddenIncoming: number;
  readonly hidden: boolean;
  /**
   * Los no leídos que había al llegar la primera bandeja de la conexión: la línea
   * de «Mientras no estabas». Se borra con «Entendido» o al abrir la bandeja.
   */
  readonly whileAway: { readonly count: number; readonly names: readonly string[] } | null;
}

export const EMPTY_MESSAGES: MessagesState = {
  conversations: [],
  blocked: [],
  inboxLoaded: false,
  open: null,
  sendError: null,
  incoming: null,
  hiddenIncoming: 0,
  hidden: false,
  whileAway: null,
};

/** Cómo se nombra al otro: por su ciudad (RN-MSG-010). */
export function otherName(name: string | null | undefined): string {
  return name ?? 'Un jugador sin ciudad';
}

/** La vista previa de la bandeja: una línea de 80 caracteres como mucho. */
export function preview(body: string): string {
  const flat = body.replace(/\n/g, ' ');
  const chars = Array.from(flat);
  if (chars.length <= PREVIEW_CHARS) return flat;
  return `${chars.slice(0, PREVIEW_CHARS - 1).join('').trimEnd()}…`;
}

/** Cuántos caracteres cuenta el servidor: puntos de código tras recortar (RN-MSG-003). */
export function bodyLength(text: string): number {
  return Array.from(text.trim()).length;
}

/** Por qué no se puede enviar un texto todavía, o `null` si se puede. */
export function sendBlockReason(text: string): string | null {
  const n = bodyLength(text);
  if (n === 0) return 'Escribe algo';
  if (n > MESSAGE_MAX_CHARS) return `Como mucho ${MESSAGE_MAX_CHARS} caracteres`;
  return null;
}

/** El total de no leídos: el contador del botón «Mensajes». */
export function unreadTotal(state: MessagesState): number {
  return state.conversations.reduce((n, c) => n + c.unread, 0);
}

/**
 * Aplica una bandeja. La primera de la conexión deja apuntados los no leídos para
 * «Mientras no estabas»; la conversación abierta no cuenta como no leída.
 */
export function applyInbox(state: MessagesState, inbox: MessageInbox): MessagesState {
  const conversations = inbox.conversations.map((c) =>
    state.open && c.otherPlayerId === state.open.otherPlayerId ? { ...c, unread: 0 } : c,
  );
  let whileAway = state.whileAway;
  if (!state.inboxLoaded) {
    const pending = conversations.filter((c) => c.unread > 0);
    const count = pending.reduce((n, c) => n + c.unread, 0);
    whileAway = count > 0 ? { count, names: pending.map((c) => otherName(c.otherCityName)) } : null;
  }
  // Bloquear y desbloquear responden con la bandeja: la conversación abierta lo sabe por ella.
  const open = state.open
    ? { ...state.open, blockedByMe: inbox.blocked.some((b) => b.playerId === state.open!.otherPlayerId) }
    : null;
  return { ...state, conversations, blocked: [...inbox.blocked], inboxLoaded: true, whileAway, open };
}

/** El texto de la línea de «Mientras no estabas»: «Tienes 3 mensajes sin leer de Villa Norte y 2 más». */
export function whileAwayText(w: NonNullable<MessagesState['whileAway']>): string {
  const mensajes = w.count === 1 ? '1 mensaje sin leer' : `${w.count} mensajes sin leer`;
  const [first, ...rest] = w.names;
  const quien = rest.length === 0 ? first : `${first} y ${rest.length} más`;
  return `Tienes ${mensajes} de ${quien}.`;
}

/** Abre una conversación: vacía hasta que llegue su primera página. */
export function openConversation(
  state: MessagesState,
  otherPlayerId: string,
  otherCityId: number | null,
  otherCityName: string | null,
): MessagesState {
  const conv = state.conversations.find((c) => c.otherPlayerId === otherPlayerId);
  const blockedByMe = conv?.blockedByMe ?? state.blocked.some((b) => b.playerId === otherPlayerId);
  return {
    ...state,
    open: {
      otherPlayerId,
      otherCityId: otherCityId ?? conv?.otherCityId ?? null,
      otherCityName: otherCityName ?? conv?.otherCityName ?? null,
      messages: [],
      nextBeforeId: null,
      blockedByMe,
      loaded: false,
    },
    sendError: state.sendError?.otherPlayerId === otherPlayerId ? state.sendError : null,
    incoming: state.incoming?.otherPlayerId === otherPlayerId ? null : state.incoming,
  };
}

export function closeConversation(state: MessagesState): MessagesState {
  return state.open ? { ...state, open: null, sendError: null } : state;
}

/**
 * Aplica una página de una conversación. `before` es el `beforeId` con que se
 * pidió: `null` para la primera, que sustituye todo; un número para una anterior,
 * que se pone delante si sigue donde se quedó. Una página de otra conversación,
 * o vieja, se descarta.
 */
export function applyPage(state: MessagesState, page: MessagePage, before: number | null): MessagesState {
  const open = state.open;
  if (!open || open.otherPlayerId !== page.otherPlayerId) return state;
  if (before === null) {
    // Lo que llegó por message.new mientras se pedía la página, sin repetir.
    const ids = new Set(page.messages.map((m) => m.id));
    const lastId = page.messages.at(-1)?.id ?? 0;
    const newer = open.messages.filter((m) => !ids.has(m.id) && m.id > lastId);
    return {
      ...state,
      open: {
        ...open,
        otherCityId: page.otherCityId ?? open.otherCityId,
        otherCityName: page.otherCityName ?? open.otherCityName,
        messages: [...page.messages, ...newer],
        nextBeforeId: page.nextBeforeId,
        blockedByMe: page.blockedByMe,
        loaded: true,
      },
    };
  }
  if (!open.loaded || before !== open.nextBeforeId) return state;
  const ids = new Set(open.messages.map((m) => m.id));
  return {
    ...state,
    open: {
      ...open,
      messages: [...page.messages.filter((m) => !ids.has(m.id)), ...open.messages],
      nextBeforeId: page.nextBeforeId,
    },
  };
}

/**
 * Aplica un mensaje nuevo, propio o ajeno: sube su conversación al principio de
 * la bandeja, lo añade a la conversación abierta si es la suya y, si es ajeno y
 * su conversación está cerrada, cuenta como no leído y da el aviso (Q7).
 */
export function applyNewMessage(state: MessagesState, message: MessageView, myPlayerId: string | null): MessagesState {
  const mine = message.fromPlayerId === myPlayerId;
  const other = message.otherPlayerId;
  const isOpen = state.open?.otherPlayerId === other;
  const previous = state.conversations.find((c) => c.otherPlayerId === other);
  if (previous && previous.lastAtMs > message.sentAtMs) {
    // Uno viejo repetido: sólo la conversación abierta lo necesita.
    return isOpen ? { ...state, open: withMessage(state.open!, message) } : state;
  }
  const conversation: ConversationView = {
    otherPlayerId: other,
    otherCityId: message.otherCityId,
    otherCityName: message.otherCityName,
    lastBody: preview(message.body),
    lastFromMe: mine,
    lastAtMs: message.sentAtMs,
    unread: isOpen ? 0 : (previous?.unread ?? 0) + (mine ? 0 : 1),
    blockedByMe: previous?.blockedByMe ?? false,
  };
  const alreadyCounted = isOpen && state.open!.messages.some((m) => m.id === message.id);
  return {
    ...state,
    conversations: [conversation, ...state.conversations.filter((c) => c.otherPlayerId !== other)],
    open: isOpen ? withMessage(state.open!, message) : state.open,
    sendError: mine && state.sendError?.otherPlayerId === other ? null : state.sendError,
    incoming:
      mine || isOpen ? state.incoming : { otherPlayerId: other, otherCityName: message.otherCityName, sentAtMs: message.sentAtMs },
    hiddenIncoming: state.hidden && !mine && !alreadyCounted ? state.hiddenIncoming + 1 : state.hiddenIncoming,
  };
}

function withMessage(open: OpenConversation, message: MessageView): OpenConversation {
  if (open.messages.some((m) => m.id === message.id)) return open;
  const messages = [...open.messages, message].sort((a, b) => a.id - b.id);
  return { ...open, messages };
}

/**
 * Lo que hay que dar por leído de la conversación abierta: su último mensaje
 * recibido del otro, si lo hay. El servidor acota a lo que sirvió a esta sesión.
 */
export function readTarget(state: MessagesState): { otherPlayerId: string; upToId: number } | null {
  const open = state.open;
  if (!open || !open.loaded) return null;
  const last = open.messages.at(-1);
  if (!last) return null;
  return { otherPlayerId: open.otherPlayerId, upToId: last.id };
}

/** Pone a cero los no leídos de una conversación en la bandeja local. */
export function markConversationRead(state: MessagesState, otherPlayerId: string): MessagesState {
  const conv = state.conversations.find((c) => c.otherPlayerId === otherPlayerId);
  if (!conv || conv.unread === 0) return state;
  return {
    ...state,
    conversations: state.conversations.map((c) => (c.otherPlayerId === otherPlayerId ? { ...c, unread: 0 } : c)),
  };
}

/** Un envío rechazado: su motivo va al cuadro de texto de su conversación. */
export function applySendError(state: MessagesState, otherPlayerId: string, code: string, message: string): MessagesState {
  return { ...state, sendError: { otherPlayerId, code, message } };
}

/** «Entendido» en «Mientras no estabas», o abrir la bandeja: la línea no vuelve. */
export function dismissWhileAway(state: MessagesState): MessagesState {
  return state.whileAway ? { ...state, whileAway: null } : state;
}

/** La pestaña se oculta o vuelve; al volver, el contador del título se borra. */
export function setMessagesTabHidden(state: MessagesState, hidden: boolean): MessagesState {
  if (hidden) return state.hidden ? state : { ...state, hidden: true };
  return state.hidden || state.hiddenIncoming > 0 ? { ...state, hidden: false, hiddenIncoming: 0 } : state;
}

/** El aviso del HUD, si todavía se ve. */
export function visibleIncoming(state: MessagesState, nowMs: number): IncomingNotice | null {
  const n = state.incoming;
  return n && nowMs < n.sentAtMs + MESSAGE_NOTICE_MS ? n : null;
}

/** El texto de un rechazo de envío, para una persona. */
export function sendErrorText(code: string, message: string): string {
  switch (code) {
    case 'RATE_LIMITED':
      return `Vas muy deprisa: ${message}`;
    case 'INVALID_TARGET':
    case 'INVALID_MESSAGE':
      return message;
    default:
      return 'No se pudo enviar. Inténtalo de nuevo.';
  }
}

/** La hora de un mensaje, corta: «14:05» hoy, «12/11 14:05» otro día. */
export function messageTime(sentAtMs: number, nowMs: number): string {
  const d = new Date(sentAtMs);
  const hm = d.toLocaleTimeString('es', { hour: '2-digit', minute: '2-digit' });
  const today = new Date(nowMs).toDateString() === d.toDateString();
  return today ? hm : `${d.toLocaleDateString('es', { day: '2-digit', month: '2-digit' })} ${hm}`;
}
