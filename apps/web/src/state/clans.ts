/**
 * Los clanes en el cliente (SPEC-CLANS): el clan propio y las invitaciones que
 * llegan con `clan.update`, el canal del clan, la vista pública de otro clan y
 * las reglas que el HUD necesita para no ofrecer órdenes que el servidor
 * rechazará.
 *
 * No decide nada: quién entra, la paz del clan, la paz residual y la regla de
 * bandos los aplica el servidor (ADR-040). El texto del canal se pinta como
 * texto, nunca como HTML.
 * Ver ../../../../docs/specs/clans.md
 */
import { v1 } from '@empires-online/protocol';

export type ClanView = v1.ClanView;
export type ClanInvitationView = v1.ClanInvitationView;
export type ClanMessageView = v1.ClanMessageView;
export type ClanInfo = v1.ClanInfo['payload'];
export type ClanChatPage = v1.ClanChatPage['payload'];

/** Longitud del nombre de un clan, en caracteres (SPEC-CLANS §4.1). */
export const CLAN_NAME_MIN = v1.CLAN_NAME_MIN_CHARS;
export const CLAN_NAME_MAX = v1.CLAN_NAME_MAX_CHARS;

/** El canal del clan propio. */
export interface ClanChat {
  readonly clanId: number;
  /** De más antiguo a más reciente. */
  readonly messages: readonly ClanMessageView[];
  /** El `beforeId` de la página anterior; `null` si no hay más. */
  readonly nextBeforeId: number | null;
  /** Lo leído por quien mira, según el servidor y lo que marcó esta pestaña. */
  readonly readUpToId: number;
  readonly loaded: boolean;
}

export interface ClansState {
  /** Si llegó ya el clan.update de la conexión. */
  readonly loaded: boolean;
  readonly clan: ClanView | null;
  readonly invitations: readonly ClanInvitationView[];
  readonly chat: ClanChat | null;
  /** La vista pública que se pidió con «Ver clan». */
  readonly info: ClanInfo | null;
  /** El rechazo del último envío al canal, para su cuadro de texto. */
  readonly sendError: { readonly code: string; readonly message: string } | null;
  /** Si el canal está a la vista: lo que llega no cuenta como no leído. */
  readonly reading: boolean;
}

export const EMPTY_CLANS: ClansState = {
  loaded: false,
  clan: null,
  invitations: [],
  chat: null,
  info: null,
  sendError: null,
  reading: false,
};

/** Aplica un `clan.update`. Otro clan (o ninguno) tira el canal que hubiera. */
export function applyClanUpdate(state: ClansState, p: v1.ClanUpdate['payload']): ClansState {
  const sameClan = state.clan !== null && p.clan !== null && state.clan.id === p.clan.id;
  return {
    ...state,
    loaded: true,
    clan: p.clan,
    invitations: p.invitations,
    chat: sameClan ? state.chat : null,
    sendError: sameClan ? state.sendError : null,
  };
}

/** Aplica una página del canal: la primera reemplaza, una anterior se antepone. */
export function applyChatPage(state: ClansState, page: ClanChatPage, beforeId: number | null): ClansState {
  if (state.clan === null || state.clan.id !== page.clanId) return state;
  const prev = state.chat && state.chat.clanId === page.clanId ? state.chat : null;
  const messages =
    beforeId === null || prev === null
      ? page.messages
      : [...page.messages.filter((m) => !prev.messages.some((p) => p.id === m.id)), ...prev.messages];
  return {
    ...state,
    chat: {
      clanId: page.clanId,
      messages,
      nextBeforeId: page.nextBeforeId,
      readUpToId: Math.max(page.readUpToId, prev?.readUpToId ?? 0),
      loaded: true,
    },
  };
}

/**
 * Aplica un mensaje nuevo del canal. Sin página todavía, se guarda igual: cuenta
 * como no leído hasta abrir el canal. Uno propio, o con el canal a la vista, se
 * da por leído.
 */
export function applyChatNew(state: ClansState, m: ClanMessageView, me: string | null): ClansState {
  if (state.clan === null || state.clan.id !== m.clanId) return state;
  const prev: ClanChat = state.chat ?? { clanId: m.clanId, messages: [], nextBeforeId: null, readUpToId: 0, loaded: false };
  if (prev.messages.some((x) => x.id === m.id)) return state;
  const read = state.reading || m.fromPlayerId === me ? Math.max(prev.readUpToId, m.id) : prev.readUpToId;
  return {
    ...state,
    sendError: m.fromPlayerId === me ? null : state.sendError,
    chat: { ...prev, messages: [...prev.messages, m], readUpToId: read },
  };
}

/** El rechazo de un envío al canal. */
export function applyChatSendError(state: ClansState, code: string, message: string): ClansState {
  return { ...state, sendError: { code, message } };
}

/** El canal a la vista (o no): al abrirlo, todo lo que hay queda leído. */
export function setChatReading(state: ClansState, reading: boolean): ClansState {
  if (!reading || state.chat === null) return { ...state, reading };
  const last = state.chat.messages.at(-1)?.id ?? 0;
  return { ...state, reading, chat: { ...state.chat, readUpToId: Math.max(state.chat.readUpToId, last) } };
}

/** El mayor id del canal recibido: lo que se puede marcar como leído. */
export function lastChatId(state: ClansState): number {
  return state.chat?.messages.at(-1)?.id ?? 0;
}

/** Los mensajes del canal sin leer que escribieron otros. */
export function unreadChat(state: ClansState, me: string | null): number {
  if (state.chat === null) return 0;
  const upTo = state.chat.readUpToId;
  return state.chat.messages.filter((m) => m.id > upTo && m.fromPlayerId !== me).length;
}

/** El contador del botón «Clan»: invitaciones vivas y mensajes sin leer. */
export function clanBadge(state: ClansState, me: string | null, nowMs: number): number {
  return liveInvitations(state, nowMs).length + unreadChat(state, me);
}

/** Las invitaciones que no han caducado. */
export function liveInvitations(state: ClansState, nowMs: number): ClanInvitationView[] {
  return state.invitations.filter((i) => i.expiresAtMs > nowMs);
}

/** Aplica la vista pública de un clan. */
export function applyClanInfo(state: ClansState, info: ClanInfo): ClansState {
  return { ...state, info };
}

/** Si el jugador es el líder de su clan. */
export function isLeader(state: ClansState, me: string | null): boolean {
  return state.clan !== null && me !== null && state.clan.leaderId === me;
}

export type ClanRole = v1.ClanRole;

/**
 * Lo que tiene que llevar el líder sin jugar para que el liderazgo pase a quien
 * sigue jugando (SPEC-CLAN-GOVERNANCE Q6). Copia del valor por defecto del
 * servidor (EO_CLAN_LEADER_INACTIVE_SECONDS), sólo para el texto del panel.
 */
export const LEADER_INACTIVE_DAYS = 7;

/** El rango del jugador en su clan, o `null` si no tiene (SPEC-CLAN-GOVERNANCE §4.1). */
export function myRole(state: ClansState, me: string | null): ClanRole | null {
  if (state.clan === null || me === null) return null;
  return state.clan.members.find((m) => m.playerId === me)?.role ?? null;
}

/** Si el rango invita y retira invitaciones: el líder y los oficiales (RN-CLANGOV-002). */
export function managesInvitations(role: ClanRole | null): boolean {
  return role === 'LEADER' || role === 'OFFICER';
}

/**
 * Si un rango puede expulsar a otro (RN-CLANGOV-002): el líder a cualquiera
 * menos a sí mismo; un oficial, sólo a un miembro raso. Al líder no lo expulsa
 * nadie. El servidor lo vuelve a comprobar.
 */
export function canExpel(by: ClanRole | null, target: ClanRole): boolean {
  if (by === 'LEADER') return target !== 'LEADER';
  if (by === 'OFFICER') return target === 'MEMBER';
  return false;
}

/** El texto de la confirmación de «Ceder el liderazgo» (SPEC-CLAN-GOVERNANCE §4.6). */
export function transferWarning(cityName: string | null, tag: string): string {
  return `${cityName ?? 'Ese jugador'} pasará a liderar [${tag}] y tú quedarás como oficial`;
}

/** «[LOB] Roma»: el nombre de una ciudad con la sigla de su clan, si lo tiene. */
export function withTag(name: string, clan: { tag: string } | null | undefined): string {
  return clan ? `[${clan.tag}] ${name}` : name;
}

/** «Líder», «Oficial» o «Miembro». */
export function roleLabel(role: string): string {
  if (role === 'LEADER') return 'Líder';
  if (role === 'OFFICER') return 'Oficial';
  return 'Miembro';
}

/**
 * Si el líder o un oficial puede invitar al dueño de una ciudad: es otro
 * jugador, no tiene clan y no está ya invitado (el tope y los bandos los dice el
 * servidor).
 */
export function canInvite(
  state: ClansState,
  me: string | null,
  target: { ownerPlayerId: string; clan: { id: number } | null },
  nowMs: number,
): boolean {
  if (!managesInvitations(myRole(state, me)) || state.clan === null) return false;
  if (target.ownerPlayerId === me || target.clan !== null) return false;
  return !state.clan.sentInvitations.some((i) => i.playerId === target.ownerPlayerId && i.expiresAtMs > nowMs);
}

/**
 * Si dos ciudades son del mismo clan: el clic de ataque no sale (RN-CLAN-004).
 * La paz residual de una salida reciente no se ve aquí: la dice el servidor.
 */
export function sameClan(a: { id: number } | null | undefined, b: { id: number } | null | undefined): boolean {
  return a != null && b != null && a.id === b.id;
}

/** El motivo por el que el nombre de un clan no vale, o `null` (RN-CLAN-002). */
export function nameProblem(name: string): string | null {
  const n = Array.from(name).length;
  if (n < CLAN_NAME_MIN || n > CLAN_NAME_MAX) return `El nombre tiene de ${CLAN_NAME_MIN} a ${CLAN_NAME_MAX} caracteres`;
  if (name !== name.trim() || name.includes('  ')) return 'Sin espacios en los extremos ni dobles';
  if (!/^[\p{L}\p{N} -]+$/u.test(name)) return 'Sólo letras, dígitos, espacios y guiones';
  return null;
}

/** El motivo por el que una sigla no vale, o `null`. */
export function tagProblem(tag: string): string | null {
  return /^[A-Z0-9]{2,4}$/.test(tag) ? null : 'La sigla tiene de 2 a 4 letras mayúsculas o dígitos';
}

export type ClanAllianceView = v1.ClanAllianceView;

/**
 * El tope de alianzas activas de un clan (SPEC-CLAN-ALLIANCES Q4). Copia del valor
 * por defecto del servidor (EO_CLAN_MAX_ALLIES), sólo para el texto del panel y
 * para no ofrecer un botón que el servidor rechazaría.
 */
export const MAX_CLAN_ALLIES = 2;

/** El preaviso de ruptura de una alianza, en minutos (el de los tratados, Q5). */
export const ALLIANCE_BREAK_MINUTES = 15;

/** Las alianzas activas del propio clan, también las que están en preaviso. */
export function activeAlliances(state: ClansState): ClanAllianceView[] {
  return state.clan?.alliances.filter((a) => a.status === 'ACTIVE') ?? [];
}

/** Las ofertas del propio clan: las recibidas y las enviadas (sólo las ve quien gobierna). */
export function allianceOffers(state: ClansState): { received: ClanAllianceView[]; sent: ClanAllianceView[] } {
  const offers = state.clan?.alliances.filter((a) => a.status === 'PROPOSED') ?? [];
  return { received: offers.filter((a) => !a.proposedByUs), sent: offers.filter((a) => a.proposedByUs) };
}

/** Si el clan de una ciudad es un aliado del propio (la barra dice «Aliado de [TAG]»). */
export function isAlly(state: ClansState, clan: { id: number } | null | undefined): boolean {
  return clan != null && activeAlliances(state).some((a) => a.clanId === clan.id);
}

/**
 * Por qué el jugador no puede proponer una alianza al clan de la vista pública, o
 * `null` si puede (RN-CLANALLY-002, Q4). Los bandos y el plazo tras un rechazo los
 * dice el servidor.
 */
export function allianceProblem(state: ClansState, me: string | null, otherClanId: number): string | null {
  if (state.clan === null || myRole(state, me) !== 'LEADER') return 'Sólo el líder firma las alianzas del clan';
  if (otherClanId === state.clan.id) return 'Es tu propio clan';
  const actual = state.clan.alliances.find((a) => a.clanId === otherClanId);
  if (actual?.status === 'ACTIVE') return 'Vuestros clanes ya están aliados';
  if (actual?.status === 'PROPOSED') return 'Ya hay una oferta pendiente con ese clan';
  if (activeAlliances(state).length >= MAX_CLAN_ALLIES) return `Tu clan ya tiene ${MAX_CLAN_ALLIES} alianzas`;
  return null;
}

/** «La alianza con [OSO] termina en 12 min», o `null` sin preaviso. */
export function allianceCountdown(a: ClanAllianceView, nowMs: number): string | null {
  if (a.breaksAtMs === null) return null;
  const min = Math.max(0, Math.ceil((a.breaksAtMs - nowMs) / 60_000));
  return `La alianza con [${a.tag}] termina en ${min} min`;
}

/** El texto de la confirmación de «Romper la alianza» (SPEC-CLAN-ALLIANCES §4.6). */
export function breakAllianceWarning(tag: string): string {
  return `Dentro de ${ALLIANCE_BREAK_MINUTES} minutos, los miembros de [${tag}] podrán atacar a los tuyos`;
}

export type ClanWarView = v1.ClanWarView;
export type ClanPublicWarView = v1.ClanPublicWarView;
export type ClanWarRecordView = v1.ClanWarRecordView;

/**
 * El tope de guerras abiertas que declara un clan (SPEC-CLAN-WARS Q7). Copia del valor
 * por defecto del servidor (EO_CLAN_MAX_WARS), sólo para no ofrecer un botón que el
 * servidor rechazaría; la tregua la dice el servidor.
 */
export const MAX_CLAN_WARS = 1;

/** La preparación de una guerra, en minutos: el preaviso de los tratados (Q6). */
export const WAR_PREPARATION_MINUTES = 15;

/** La tregua tras una guerra, en horas (Q6). */
export const WAR_COOLDOWN_HOURS = 24;

/** Las guerras abiertas del propio clan. */
export function openWars(state: ClansState): ClanWarView[] {
  return state.clan?.wars ?? [];
}

/** Si el clan de una ciudad está en guerra con el propio (la barra dice «En guerra con [TAG]»). */
export function isAtWarWith(state: ClansState, clan: { id: number } | null | undefined): boolean {
  return clan != null && openWars(state).some((w) => w.clanId === clan.id);
}

/** «[LOB] 12 – 7 [OSO]»: el marcador desde el clan de quien lo dice. */
export function warScore(ourTag: string, w: { tag: string; ourScore: number; theirScore: number }): string {
  return `[${ourTag}] ${w.ourScore} – ${w.theirScore} [${w.tag}]`;
}

/** Una duración en días, horas o minutos: «2 d 4 h», «3 h 10 min», «9 min». */
export function shortDuration(ms: number): string {
  const min = Math.max(0, Math.ceil(ms / 60_000));
  const d = Math.floor(min / 1440);
  const h = Math.floor((min % 1440) / 60);
  const m = min % 60;
  if (d > 0) return h > 0 ? `${d} d ${h} h` : `${d} d`;
  if (h > 0) return m > 0 ? `${h} h ${m} min` : `${h} h`;
  return `${m} min`;
}

/** «Empieza en 9 min» en la preparación, «Termina en 2 d 4 h» en guerra (SPEC-CLAN-WARS §4.7). */
export function warCountdown(w: { status: string; startsAtMs: number; endsAtMs: number | null }, nowMs: number): string {
  if (w.status === 'DECLARED') return `Empieza en ${shortDuration(w.startsAtMs - nowMs)}`;
  return w.endsAtMs !== null ? `Termina en ${shortDuration(w.endsAtMs - nowMs)}` : 'En guerra';
}

/** «3 victorias, 1 derrota, 2 empates», o «Sin guerras terminadas». */
export function warRecordText(r: ClanWarRecordView): string {
  if (r.won + r.lost + r.drawn === 0) return 'Sin guerras terminadas';
  const plural = (n: number, uno: string, varios: string) => `${n} ${n === 1 ? uno : varios}`;
  return [plural(r.won, 'victoria', 'victorias'), plural(r.lost, 'derrota', 'derrotas'), plural(r.drawn, 'empate', 'empates')].join(', ');
}

/**
 * Por qué el jugador no puede declarar la guerra al clan de la vista pública, o
 * `null` si puede (RN-CLANWAR-001 a RN-CLANWAR-003). La tregua la dice el servidor.
 */
export function warProblem(state: ClansState, me: string | null, otherClanId: number): string | null {
  if (state.clan === null || myRole(state, me) !== 'LEADER') return 'Sólo el líder decide las guerras del clan';
  if (otherClanId === state.clan.id) return 'Es tu propio clan';
  if (openWars(state).some((w) => w.clanId === otherClanId)) return 'Ya hay una guerra abierta con ese clan';
  if (activeAlliances(state).some((a) => a.clanId === otherClanId)) return 'Vuestros clanes están aliados: rompe antes la alianza';
  if (openWars(state).filter((w) => w.declaredByUs).length >= MAX_CLAN_WARS) return 'Tu clan ya tiene una guerra declarada abierta';
  return null;
}

/** El texto de la confirmación de «Declarar la guerra» (SPEC-CLAN-WARS §4.7). */
export function declareWarWarning(tag: string): string {
  return `Guerra a [${tag}]: los pactos entre vuestros miembros terminarán en ${WAR_PREPARATION_MINUTES} minutos`;
}

/** El texto de la confirmación de «Rendirse». */
export function surrenderWarning(tag: string): string {
  return `[${tag}] ganará la guerra y no podréis declararle otra en ${WAR_COOLDOWN_HOURS} h`;
}
