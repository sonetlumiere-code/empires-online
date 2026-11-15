import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';
import {
  applyChatNew,
  applyChatPage,
  applyClanUpdate,
  canInvite,
  clanBadge,
  EMPTY_CLANS,
  isLeader,
  nameProblem,
  sameClan,
  setChatReading,
  tagProblem,
  unreadChat,
  withTag,
  type ClanMessageView,
  type ClanView,
} from './clans';
import { useWorldStore } from './world';

const YO = '00000000-0000-4000-8000-000000000001';
const BEA = '00000000-0000-4000-8000-000000000002';
const CE = '00000000-0000-4000-8000-000000000003';

const miembro = (playerId: string, role: 'LEADER' | 'MEMBER'): v1.ClanMemberView => ({
  playerId, cityId: 1, cityName: 'Roma', faction: 'NEUTRAL', role, joinedAtMs: 1,
});
const clan: ClanView = {
  id: 3, name: 'Los Lobos', tag: 'LOB', leaderId: YO,
  members: [miembro(YO, 'LEADER'), miembro(BEA, 'MEMBER')],
  sentInvitations: [{ playerId: CE, cityId: 9, cityName: 'Persépolis', expiresAtMs: 5_000 }],
};
const mensaje = (id: number, from: string, clanId = 3): ClanMessageView => ({
  id, clanId, fromPlayerId: from, fromCityId: 1, fromCityName: 'Roma', body: `m${id}`, sentAtMs: id,
});

describe('clanes en el cliente (SPEC-CLANS §4.8)', () => {
  it('clan.update pone el clan y las invitaciones; otro clan tira el canal', () => {
    let s = applyClanUpdate(EMPTY_CLANS, { clan, invitations: [] });
    expect(s.loaded).toBe(true);
    expect(isLeader(s, YO)).toBe(true);
    expect(isLeader(s, BEA)).toBe(false);
    s = applyChatPage(s, { clanId: 3, messages: [mensaje(1, BEA)], readUpToId: 0, nextBeforeId: null }, null);
    expect(s.chat?.messages).toHaveLength(1);
    s = applyClanUpdate(s, { clan: { ...clan, members: [miembro(YO, 'LEADER')] }, invitations: [] });
    expect(s.chat?.messages, 'el mismo clan conserva el canal').toHaveLength(1);
    s = applyClanUpdate(s, { clan: null, invitations: [] });
    expect(s.chat).toBeNull();
  });

  it('lo no leído del canal: lo de otros por encima de lo leído, salvo con el canal a la vista', () => {
    let s = applyClanUpdate(EMPTY_CLANS, { clan, invitations: [] });
    s = applyChatPage(s, { clanId: 3, messages: [mensaje(1, BEA), mensaje(2, YO)], readUpToId: 1, nextBeforeId: null }, null);
    expect(unreadChat(s, YO)).toBe(0);
    s = applyChatNew(s, mensaje(3, BEA), YO);
    expect(unreadChat(s, YO)).toBe(1);
    s = applyChatNew(s, mensaje(3, BEA), YO);
    expect(s.chat?.messages, 'sin duplicados').toHaveLength(3);
    s = applyChatNew(s, mensaje(4, YO), YO);
    expect(unreadChat(s, YO), 'escribir lo da por leído').toBe(0);
    s = setChatReading(s, true);
    s = applyChatNew(s, mensaje(5, BEA), YO);
    expect(unreadChat(s, YO), 'con el canal a la vista, leído').toBe(0);
    expect(applyChatNew(s, mensaje(6, BEA, 99), YO), 'de otro clan, nada').toBe(s);
  });

  it('una página anterior se antepone; el contador suma invitaciones vivas', () => {
    let s = applyClanUpdate(EMPTY_CLANS, { clan, invitations: [] });
    s = applyChatPage(s, { clanId: 3, messages: [mensaje(5, BEA)], readUpToId: 5, nextBeforeId: 5 }, null);
    s = applyChatPage(s, { clanId: 3, messages: [mensaje(3, BEA), mensaje(4, BEA)], readUpToId: 5, nextBeforeId: null }, 5);
    expect(s.chat?.messages.map((m) => m.id)).toEqual([3, 4, 5]);
    const sin = applyClanUpdate(EMPTY_CLANS, {
      clan: null,
      invitations: [
        { clanId: 4, name: 'Los Osos', tag: 'OSO', expiresAtMs: 2_000 },
        { clanId: 5, name: 'Los Patos', tag: 'PAT', expiresAtMs: 500 },
      ],
    });
    expect(clanBadge(sin, YO, 1_000), 'la caducada no cuenta').toBe(1);
  });

  it('a quién puede invitar el líder, y quién está en paz por su clan', () => {
    const s = applyClanUpdate(EMPTY_CLANS, { clan, invitations: [] });
    expect(canInvite(s, YO, { ownerPlayerId: '00000000-0000-4000-8000-000000000009', clan: null }, 1_000)).toBe(true);
    expect(canInvite(s, YO, { ownerPlayerId: CE, clan: null }, 1_000), 'ya invitado').toBe(false);
    expect(canInvite(s, YO, { ownerPlayerId: CE, clan: null }, 6_000), 'la invitación caducó').toBe(true);
    expect(canInvite(s, YO, { ownerPlayerId: CE, clan: { id: 8 } }, 6_000), 'ya tiene clan').toBe(false);
    expect(canInvite(s, BEA, { ownerPlayerId: CE, clan: null }, 6_000), 'sólo el líder').toBe(false);
    expect(sameClan({ id: 3 }, { id: 3 })).toBe(true);
    expect(sameClan({ id: 3 }, null)).toBe(false);
    expect(withTag('Roma', { tag: 'LOB' })).toBe('[LOB] Roma');
    expect(withTag('Roma', null)).toBe('Roma');
  });

  it('el nombre y la sigla, con las reglas del servidor (RN-CLAN-002)', () => {
    expect(nameProblem('Los Lobos')).toBeNull();
    expect(nameProblem('Ñandú-7')).toBeNull();
    expect(nameProblem('Lo')).not.toBeNull();
    expect(nameProblem(' Lobos')).not.toBeNull();
    expect(nameProblem('Los  Lobos')).not.toBeNull();
    expect(nameProblem('Lobos!')).not.toBeNull();
    expect(tagProblem('LOB')).toBeNull();
    expect(tagProblem('lob')).not.toBeNull();
    expect(tagProblem('LOBOS')).not.toBeNull();
  });
});

describe('clanes en el store', () => {
  const state = () => useWorldStore.getState();
  const env = { v: 1 as const, seq: 1, ts: 1 };
  beforeEach(() => {
    state().reset();
    useWorldStore.setState({ playerId: YO });
  });

  it('clan.update, una página pedida, clan.chat.new y el rechazo de un envío', () => {
    state().applyServerMessage({ ...env, type: 'clan.update', payload: { clan, invitations: [] } });
    expect(state().clans.clan?.tag).toBe('LOB');
    // Una página que no se pidió no se aplica.
    state().applyServerMessage({
      ...env, type: 'clan.chat.page', requestId: '00000000-0000-4000-8000-0000000000b1',
      payload: { clanId: 3, messages: [mensaje(1, BEA)], readUpToId: 0, nextBeforeId: null },
    });
    expect(state().clans.chat).toBeNull();
    state().expectClanChatPage('00000000-0000-4000-8000-0000000000b2', null);
    state().applyServerMessage({
      ...env, type: 'clan.chat.page', requestId: '00000000-0000-4000-8000-0000000000b2',
      payload: { clanId: 3, messages: [mensaje(1, BEA)], readUpToId: 0, nextBeforeId: null },
    });
    expect(state().clans.chat?.messages).toHaveLength(1);
    state().applyServerMessage({ ...env, type: 'clan.chat.new', payload: { message: mensaje(2, BEA) } });
    expect(state().clans.chat?.messages).toHaveLength(2);

    state().expectClanChatSend('00000000-0000-4000-8000-0000000000b3');
    state().applyServerMessage({
      ...env, type: 'system.error', requestId: '00000000-0000-4000-8000-0000000000b3',
      payload: { code: 'RATE_LIMITED', message: 'demasiados mensajes seguidos' },
    });
    expect(state().clans.sendError?.code).toBe('RATE_LIMITED');
    expect(state().lastError, 'no va al pie del HUD').toBeNull();
  });
});
