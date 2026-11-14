import { describe, expect, it } from 'vitest';

import {
  applyInbox,
  applyNewMessage,
  applyPage,
  applySendError,
  bodyLength,
  closeConversation,
  dismissWhileAway,
  EMPTY_MESSAGES,
  markConversationRead,
  MESSAGE_MAX_CHARS,
  MESSAGE_NOTICE_MS,
  openConversation,
  preview,
  readTarget,
  sendBlockReason,
  setMessagesTabHidden,
  unreadTotal,
  visibleIncoming,
  whileAwayText,
  type ConversationView,
  type MessagePage,
  type MessageView,
} from './messages';

const yo = '11111111-1111-4111-8111-111111111111';
const bea = '22222222-2222-4222-8222-222222222222';
const ce = '33333333-3333-4333-8333-333333333333';

function deBea(id: number, body = `mensaje ${id}`, sentAtMs = id * 1000): MessageView {
  return { id, fromPlayerId: bea, toPlayerId: yo, otherPlayerId: bea, otherCityId: 2, otherCityName: 'Bizancio', body, sentAtMs };
}
function aBea(id: number, body = `mío ${id}`, sentAtMs = id * 1000): MessageView {
  return { id, fromPlayerId: yo, toPlayerId: bea, otherPlayerId: bea, otherCityId: 2, otherCityName: 'Bizancio', body, sentAtMs };
}
function conversacion(otherPlayerId: string, unread: number, otherCityName: string, lastAtMs = 1): ConversationView {
  return { otherPlayerId, otherCityId: 2, otherCityName, lastBody: 'hola', lastFromMe: false, lastAtMs, unread, blockedByMe: false };
}
function pagina(messages: MessageView[], nextBeforeId: number | null = null): MessagePage {
  return { otherPlayerId: bea, otherCityId: 2, otherCityName: 'Bizancio', messages, readUpToId: 0, nextBeforeId, blockedByMe: false };
}

describe('mensajes (SPEC-MESSAGES)', () => {
  it('el texto se cuenta en caracteres tras recortar, como en el servidor (RN-MSG-003)', () => {
    expect(bodyLength('  hola \n')).toBe(4);
    expect(bodyLength('𝔸'.repeat(3))).toBe(3);
    expect(sendBlockReason('   ')).toBe('Escribe algo');
    expect(sendBlockReason('ñ'.repeat(MESSAGE_MAX_CHARS))).toBeNull();
    expect(sendBlockReason('a'.repeat(MESSAGE_MAX_CHARS + 1))).toMatch(/500/);
  });

  it('la vista previa es una línea de 80 caracteres como mucho', () => {
    expect(preview('hola\nmundo')).toBe('hola mundo');
    const largo = preview('ñ'.repeat(200));
    expect(Array.from(largo)).toHaveLength(80);
    expect(largo.endsWith('…')).toBe(true);
  });

  it('la primera bandeja deja la línea de «Mientras no estabas»; las siguientes, no', () => {
    let s = applyInbox(EMPTY_MESSAGES, {
      conversations: [conversacion(bea, 2, 'Bizancio', 5), conversacion(ce, 1, 'Persépolis', 3), conversacion('x', 0, 'Nada')],
      blocked: [],
    });
    expect(unreadTotal(s)).toBe(3);
    expect(s.whileAway).toEqual({ count: 3, names: ['Bizancio', 'Persépolis'] });
    expect(whileAwayText(s.whileAway!)).toBe('Tienes 3 mensajes sin leer de Bizancio y 1 más.');
    s = dismissWhileAway(s);
    s = applyInbox(s, { conversations: [conversacion(bea, 4, 'Bizancio')], blocked: [] });
    expect(s.whileAway).toBeNull();
    expect(whileAwayText({ count: 1, names: ['Bizancio'] })).toBe('Tienes 1 mensaje sin leer de Bizancio.');
    expect(applyInbox(EMPTY_MESSAGES, { conversations: [conversacion(bea, 0, 'B')], blocked: [] }).whileAway).toBeNull();
  });

  it('un mensaje ajeno con la conversación cerrada cuenta como no leído y avisa (Q7)', () => {
    let s = applyNewMessage(EMPTY_MESSAGES, deBea(1), yo);
    expect(unreadTotal(s)).toBe(1);
    expect(s.conversations[0]).toMatchObject({ otherPlayerId: bea, lastFromMe: false, unread: 1, otherCityName: 'Bizancio' });
    expect(visibleIncoming(s, 1000)).toEqual({ otherPlayerId: bea, otherCityName: 'Bizancio', sentAtMs: 1000 });
    expect(visibleIncoming(s, 1000 + MESSAGE_NOTICE_MS)).toBeNull();

    s = applyNewMessage(s, aBea(2), yo);
    expect(s.conversations[0]).toMatchObject({ lastFromMe: true, unread: 1, lastBody: 'mío 2' });
    s = applyNewMessage(s, { ...deBea(3), otherPlayerId: ce, fromPlayerId: ce, otherCityName: 'Persépolis' }, yo);
    expect(s.conversations.map((c) => c.otherPlayerId)).toEqual([ce, bea]);
    expect(unreadTotal(s)).toBe(2);
  });

  it('con la conversación abierta, el mensaje entra en ella sin contar ni avisar', () => {
    let s = openConversation(EMPTY_MESSAGES, bea, 2, 'Bizancio');
    s = applyPage(s, pagina([deBea(1), aBea(2)]), null);
    s = applyNewMessage(s, deBea(3), yo);
    expect(s.open!.messages.map((m) => m.id)).toEqual([1, 2, 3]);
    expect(unreadTotal(s)).toBe(0);
    expect(s.incoming).toBeNull();
    s = applyNewMessage(s, deBea(3), yo);
    expect(s.open!.messages).toHaveLength(3);
    expect(readTarget(s)).toEqual({ otherPlayerId: bea, upToId: 3 });
  });

  it('una página sustituye la conversación, conserva lo llegado mientras tanto y pagina hacia atrás', () => {
    let s = openConversation(EMPTY_MESSAGES, bea, null, null);
    expect(readTarget(s)).toBeNull();
    s = applyNewMessage(s, deBea(9), yo);
    s = applyPage(s, pagina([deBea(5), aBea(6)], 5), null);
    expect(s.open!.messages.map((m) => m.id)).toEqual([5, 6, 9]);
    expect(s.open!.otherCityName).toBe('Bizancio');
    s = applyPage(s, pagina([deBea(3), deBea(4)], null), 5);
    expect(s.open!.messages.map((m) => m.id)).toEqual([3, 4, 5, 6, 9]);
    expect(s.open!.nextBeforeId).toBeNull();
    const vieja = applyPage(s, pagina([deBea(1)], null), 3);
    expect(vieja).toBe(s);
    const deOtra = applyPage(s, { ...pagina([deBea(1)]), otherPlayerId: ce }, null);
    expect(deOtra).toBe(s);
  });

  it('marcar leída pone a cero su conversación, y la bandeja no cuenta la abierta', () => {
    let s = applyInbox(EMPTY_MESSAGES, { conversations: [conversacion(bea, 2, 'Bizancio')], blocked: [] });
    s = markConversationRead(s, bea);
    expect(unreadTotal(s)).toBe(0);
    s = openConversation(s, bea, 2, 'Bizancio');
    s = applyInbox(s, { conversations: [conversacion(bea, 2, 'Bizancio')], blocked: [] });
    expect(unreadTotal(s)).toBe(0);
    s = closeConversation(s);
    expect(s.open).toBeNull();
  });

  it('un rechazo queda en su conversación hasta que un envío propio sale', () => {
    let s = openConversation(EMPTY_MESSAGES, bea, 2, 'Bizancio');
    s = applySendError(s, bea, 'INVALID_TARGET', 'ese jugador no acepta tus mensajes');
    expect(s.sendError).toEqual({ otherPlayerId: bea, code: 'INVALID_TARGET', message: 'ese jugador no acepta tus mensajes' });
    s = applyNewMessage(s, deBea(4), yo);
    expect(s.sendError).not.toBeNull();
    s = applyNewMessage(s, aBea(5), yo);
    expect(s.sendError).toBeNull();
  });

  it('abrir una conversación sabe si está bloqueada y quita su aviso', () => {
    let s = applyInbox(EMPTY_MESSAGES, { conversations: [], blocked: [{ playerId: bea, cityId: 2, cityName: 'Bizancio' }] });
    s = applyNewMessage(s, { ...deBea(1), otherPlayerId: ce, fromPlayerId: ce }, yo);
    s = openConversation(s, bea, 2, 'Bizancio');
    expect(s.open!.blockedByMe).toBe(true);
    expect(s.incoming?.otherPlayerId).toBe(ce);
    s = openConversation(s, ce, null, 'Persépolis');
    expect(s.incoming).toBeNull();
  });

  it('con la pestaña oculta, los mensajes ajenos cuentan para el título; al volver, se borra', () => {
    let s = setMessagesTabHidden(EMPTY_MESSAGES, true);
    s = applyNewMessage(s, deBea(1), yo);
    s = applyNewMessage(s, aBea(2), yo);
    s = applyNewMessage(s, deBea(3), yo);
    expect(s.hiddenIncoming).toBe(2);
    s = setMessagesTabHidden(s, false);
    expect(s.hiddenIncoming).toBe(0);
  });
});

describe('bloqueo en la conversación abierta', () => {
  it('la bandeja que responde a bloquear o desbloquear actualiza la conversación abierta', () => {
    let s = openConversation(EMPTY_MESSAGES, bea, 2, 'Bizancio');
    s = applyInbox(s, { conversations: [], blocked: [{ playerId: bea, cityId: 2, cityName: 'Bizancio' }] });
    expect(s.open!.blockedByMe).toBe(true);
    s = applyInbox(s, { conversations: [], blocked: [] });
    expect(s.open!.blockedByMe).toBe(false);
  });
});
