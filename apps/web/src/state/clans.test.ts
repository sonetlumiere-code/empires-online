import { beforeEach, describe, expect, it } from 'vitest';
import type { v1 } from '@empires-online/protocol';
import {
  activeAlliances,
  allianceCountdown,
  allianceOffers,
  allianceProblem,
  breakAllianceWarning,
  declareWarWarning,
  isAlly,
  isAtWarWith,
  MAX_CLAN_WARS,
  openWars,
  shortDuration,
  surrenderWarning,
  warCountdown,
  warProblem,
  warRecordText,
  warScore,
  MAX_CLAN_ALLIES,
  applyChatNew,
  applyChatPage,
  applyClanUpdate,
  canExpel,
  canInvite,
  clanBadge,
  EMPTY_CLANS,
  isLeader,
  managesInvitations,
  myRole,
  nameProblem,
  roleLabel,
  sameClan,
  setChatReading,
  tagProblem,
  transferWarning,
  unreadChat,
  withTag,
  type ClanMessageView,
  type ClanView,
} from './clans';
import { useWorldStore } from './world';

const YO = '00000000-0000-4000-8000-000000000001';
const BEA = '00000000-0000-4000-8000-000000000002';
const CE = '00000000-0000-4000-8000-000000000003';

const miembro = (playerId: string, role: v1.ClanRole): v1.ClanMemberView => ({
  playerId, cityId: 1, cityName: 'Roma', faction: 'NEUTRAL', role, joinedAtMs: 1,
});
const clan: ClanView = {
  id: 3, name: 'Los Lobos', tag: 'LOB', leaderId: YO,
  members: [miembro(YO, 'LEADER'), miembro(BEA, 'MEMBER')],
  sentInvitations: [{ playerId: CE, cityId: 9, cityName: 'Persépolis', expiresAtMs: 5_000 }],
  territories: [],
  alliances: [],
  wars: [],
  warRecord: { won: 0, lost: 0, drawn: 0 },
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
    expect(canInvite(s, BEA, { ownerPlayerId: CE, clan: null }, 6_000), 'un raso no invita').toBe(false);
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

describe('el gobierno del clan (SPEC-CLAN-GOVERNANCE §4.2, §4.6)', () => {
  const conOficial: ClanView = { ...clan, members: [miembro(YO, 'LEADER'), miembro(BEA, 'OFFICER'), miembro(CE, 'MEMBER')] };

  it('el rango de quien mira y los botones que le tocan', () => {
    const s = applyClanUpdate(EMPTY_CLANS, { clan: conOficial, invitations: [] });
    expect(myRole(s, YO)).toBe('LEADER');
    expect(myRole(s, BEA)).toBe('OFFICER');
    expect(myRole(s, CE)).toBe('MEMBER');
    expect(myRole(EMPTY_CLANS, YO)).toBeNull();
    expect(managesInvitations('OFFICER')).toBe(true);
    expect(managesInvitations('MEMBER')).toBe(false);
    expect(canInvite(s, BEA, { ownerPlayerId: '00000000-0000-4000-8000-000000000009', clan: null }, 6_000), 'un oficial invita').toBe(true);
    expect(roleLabel('OFFICER')).toBe('Oficial');
    expect(roleLabel('LEADER')).toBe('Líder');
    expect(roleLabel('MEMBER')).toBe('Miembro');
  });

  it('quién expulsa a quién: el líder a cualquiera, un oficial sólo a un raso', () => {
    expect(canExpel('LEADER', 'OFFICER')).toBe(true);
    expect(canExpel('LEADER', 'MEMBER')).toBe(true);
    expect(canExpel('LEADER', 'LEADER')).toBe(false);
    expect(canExpel('OFFICER', 'MEMBER')).toBe(true);
    expect(canExpel('OFFICER', 'OFFICER')).toBe(false);
    expect(canExpel('OFFICER', 'LEADER')).toBe(false);
    expect(canExpel('MEMBER', 'MEMBER')).toBe(false);
    expect(canExpel(null, 'MEMBER')).toBe(false);
  });

  it('la confirmación de ceder el liderazgo', () => {
    expect(transferWarning('Bizancio', 'LOB')).toBe('Bizancio pasará a liderar [LOB] y tú quedarás como oficial');
    expect(transferWarning(null, 'LOB')).toBe('Ese jugador pasará a liderar [LOB] y tú quedarás como oficial');
  });
});

describe('las alianzas entre clanes (SPEC-CLAN-ALLIANCES §4.6)', () => {
  const alianza = (id: number, clanId: number, tag: string, status: 'ACTIVE' | 'PROPOSED', proposedByUs = true,
    breaksAtMs: number | null = null): v1.ClanAllianceView => ({ id, clanId, name: `Los ${tag}`, tag, status, proposedByUs, breaksAtMs });
  const con = (alliances: v1.ClanAllianceView[]) => applyClanUpdate(EMPTY_CLANS, { clan: { ...clan, alliances }, invitations: [] });

  it('las activas, las ofertas recibidas y enviadas, y si una ciudad es de un aliado', () => {
    const s = con([alianza(1, 4, 'OSO', 'ACTIVE'), alianza(2, 5, 'CIE', 'PROPOSED', false), alianza(3, 6, 'ALC', 'PROPOSED')]);
    expect(activeAlliances(s).map((a) => a.tag)).toEqual(['OSO']);
    expect(allianceOffers(s).received.map((a) => a.tag)).toEqual(['CIE']);
    expect(allianceOffers(s).sent.map((a) => a.tag)).toEqual(['ALC']);
    expect(isAlly(s, { id: 4 })).toBe(true);
    expect(isAlly(s, { id: 5 }), 'una oferta no es una alianza').toBe(false);
    expect(isAlly(s, null)).toBe(false);
  });

  it('quién puede proponer, y a quién', () => {
    const s = con([alianza(1, 4, 'OSO', 'ACTIVE'), alianza(2, 5, 'CIE', 'PROPOSED')]);
    expect(allianceProblem(s, YO, 7)).toBeNull();
    expect(allianceProblem(s, BEA, 7)).toBe('Sólo el líder firma las alianzas del clan');
    expect(allianceProblem(s, YO, 3)).toBe('Es tu propio clan');
    expect(allianceProblem(s, YO, 4)).toBe('Vuestros clanes ya están aliados');
    expect(allianceProblem(s, YO, 5)).toBe('Ya hay una oferta pendiente con ese clan');
    const lleno = con([alianza(1, 4, 'OSO', 'ACTIVE'), alianza(2, 5, 'CIE', 'ACTIVE')]);
    expect(MAX_CLAN_ALLIES).toBe(2);
    expect(allianceProblem(lleno, YO, 7)).toBe('Tu clan ya tiene 2 alianzas');
    expect(allianceProblem(EMPTY_CLANS, YO, 7)).toBe('Sólo el líder firma las alianzas del clan');
  });

  it('la cuenta atrás del preaviso y la confirmación de romper', () => {
    expect(allianceCountdown(alianza(1, 4, 'OSO', 'ACTIVE'), 0)).toBeNull();
    expect(allianceCountdown(alianza(1, 4, 'OSO', 'ACTIVE', true, 12 * 60_000), 30_000)).toBe('La alianza con [OSO] termina en 12 min');
    expect(allianceCountdown(alianza(1, 4, 'OSO', 'ACTIVE', true, 1_000), 5_000)).toBe('La alianza con [OSO] termina en 0 min');
    expect(breakAllianceWarning('OSO')).toBe('Dentro de 15 minutos, los miembros de [OSO] podrán atacar a los tuyos');
  });
});

describe('las guerras entre clanes (SPEC-CLAN-WARS §4.7)', () => {
  const guerra = (id: number, clanId: number, tag: string, extra: Partial<v1.ClanWarView> = {}): v1.ClanWarView => ({
    id, clanId, name: `Los ${tag}`, tag, declaredByUs: true, status: 'ACTIVE', startsAtMs: 0, endsAtMs: 3_600_000,
    ourScore: 12, theirScore: 7, peaceOfferedByUs: false, peaceOfferedByThem: false, ...extra,
  });
  const con = (wars: v1.ClanWarView[], alliances: v1.ClanAllianceView[] = []) =>
    applyClanUpdate(EMPTY_CLANS, { clan: { ...clan, wars, alliances }, invitations: [] });

  it('las abiertas, si una ciudad es de un clan enemigo, el marcador y el historial', () => {
    const s = con([guerra(1, 4, 'OSO')]);
    expect(openWars(s).map((w) => w.tag)).toEqual(['OSO']);
    expect(isAtWarWith(s, { id: 4 })).toBe(true);
    expect(isAtWarWith(s, { id: 5 })).toBe(false);
    expect(isAtWarWith(s, null)).toBe(false);
    expect(warScore('LOB', guerra(1, 4, 'OSO'))).toBe('[LOB] 12 – 7 [OSO]');
    expect(warRecordText({ won: 3, lost: 1, drawn: 2 })).toBe('3 victorias, 1 derrota, 2 empates');
    expect(warRecordText({ won: 1, lost: 0, drawn: 0 })).toBe('1 victoria, 0 derrotas, 0 empates');
    expect(warRecordText({ won: 0, lost: 0, drawn: 0 })).toBe('Sin guerras terminadas');
  });

  it('la cuenta atrás de la preparación y del plazo', () => {
    expect(warCountdown(guerra(1, 4, 'OSO', { status: 'DECLARED', startsAtMs: 9 * 60_000, endsAtMs: null }), 0)).toBe('Empieza en 9 min');
    expect(warCountdown(guerra(1, 4, 'OSO', { endsAtMs: (2 * 24 + 4) * 3_600_000 }), 0)).toBe('Termina en 2 d 4 h');
    expect(shortDuration(3 * 3_600_000 + 10 * 60_000)).toBe('3 h 10 min');
    expect(shortDuration(-5)).toBe('0 min');
  });

  it('quién puede declarar, y a quién', () => {
    const aliado: v1.ClanAllianceView = { id: 9, clanId: 6, name: 'Los ALC', tag: 'ALC', status: 'ACTIVE', proposedByUs: true, breaksAtMs: null };
    const s = con([guerra(1, 4, 'OSO', { declaredByUs: false })], [aliado]);
    expect(warProblem(s, YO, 7)).toBeNull();
    expect(warProblem(s, BEA, 7)).toBe('Sólo el líder decide las guerras del clan');
    expect(warProblem(s, YO, 3)).toBe('Es tu propio clan');
    expect(warProblem(s, YO, 4)).toBe('Ya hay una guerra abierta con ese clan');
    expect(warProblem(s, YO, 6)).toBe('Vuestros clanes están aliados: rompe antes la alianza');
    expect(MAX_CLAN_WARS).toBe(1);
    expect(warProblem(con([guerra(1, 4, 'OSO')]), YO, 7)).toBe('Tu clan ya tiene una guerra declarada abierta');
    expect(warProblem(EMPTY_CLANS, YO, 7)).toBe('Sólo el líder decide las guerras del clan');
  });

  it('las confirmaciones de declarar y de rendirse', () => {
    expect(declareWarWarning('OSO')).toBe('Guerra a [OSO]: los pactos entre vuestros miembros terminarán en 15 minutos');
    expect(surrenderWarning('OSO')).toBe('[OSO] ganará la guerra y no podréis declararle otra en 24 h');
  });
});
