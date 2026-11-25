'use client';

/**
 * Pantalla principal: autenticación y, tras ella, el mundo.
 *
 * Todo lo que hace el jugador aquí se traduce en INTENCIONES enviadas al Game
 * Server. Ninguna acción modifica el estado del mundo localmente: se envía el
 * comando y se espera al delta. Si el servidor lo rechaza, no pasa nada — que es
 * exactamente lo que debe pasar.
 */
import { useCallback, useEffect, useRef, useState } from 'react';

import { GameCanvas } from '../src/components/GameCanvas';
import { GameClient, type ConnectionState } from '../src/net/client';
import { login, logout, register, resumeSession, ticketProvider, AuthError } from '../src/net/auth';
import type { v1 } from '@empires-online/protocol';

import { terrainAt, useWorldStore, type BuildingView, type CityView } from '../src/state/world';
import { createHudSelector } from '../src/state/selectors';
import {
  advanceBlockReason,
  craftBlockReason,
  CRAFT_AMOUNT,
  CRAFT_COST,
  IRON_AGE_COST,
  CASTLE_AGE_COST,
  CASTLE_COST,
  castleBlockReason,
  ramBlockReason,
  WALL_COST,
  wallBlockReason,
  nextEra,
  workshopBlockReason,
  WORKSHOP_COST,
  BRONZE_AGE_COST,
  buildingAt,
  buildingsOf,
  decideTileOrder,
  decideUnitClick,
  eraLabel,
  firstFreeHouseTile,
  overPopulationLimit,
  pillageAlertActive,
  PILLAGE_ALERT_MS,
  readableError,
  secondsUntil,
  siegeAlertActive,
  SIEGE_ALERT_MS,
  trainBlockReason,
  civilizationUnitBlockReason,
  unitTypeLabel,
} from '../src/state/economy';
import {
  civilizationUnit,
  civilizationUnitLine,
  trainCostText,
  unitAttacks,
  unitInfoOf,
  unitName,
  unitTitle,
} from '../src/state/units';
import { ViewSync } from '../src/state/viewSync';
import {
  buyBlockReason,
  buyCost,
  lotLabel,
  MARKET_COST,
  MARKET_MAX_LOTS,
  marketBuildBlockReason,
  marketMissingReason,
  offerDistance,
  ownOfferLabel,
  postBlockReason,
  postCost,
  type MarketDraft,
  type MarketOfferView,
  type MarketState,
} from '../src/state/market';
import {
  costText,
  effectText,
  researchBlockReason,
  researchSeconds,
  technologiesFor,
  technologyName,
} from '../src/state/technologies';
import {
  CIVILIZATIONS,
  civilizationLabel,
  foreignGoods,
  GOODS,
  goodLabel,
  goodOf,
  traitLines,
  traitSummary,
} from '../src/state/civilizations';
import {
  anyCountdown,
  proposableTypes,
  treatyActions,
  treatyLabel,
  type ProposableType,
  type TreatyAction,
} from '../src/state/diplomacy';
import {
  canTradeWith,
  draftLines,
  formatHours,
  offerBlockReason,
  resourceName,
  TRADE_RESOURCES,
  pendingWith,
  tradeActions,
  tradeLabel,
  type ResourceType,
  type TradeAction,
  type TradeDraft,
} from '../src/state/trade';
import {
  entryText,
  isLoss,
  isUnseen,
  markTarget,
  moreUnseenBeyond,
  shouldRefetchOnVisible,
  timeAgo,
  unseenEntries,
  type EventLogEntry,
} from '../src/state/eventLog';
import {
  documentTitle,
  hasUnreadNotices,
  nextNoticeExpiry,
  noticeIsLoss,
  noticeText,
  noticeTile,
  visibleNotices,
  type NoticeGroup,
} from '../src/state/notices';
import { claimBlockReason, claimLabel, territoryAt, territoryOwnerLabel } from '../src/state/conquest';
import { assaultBlockReason, assaultLabel, assaultTargetNear } from '../src/state/cityFall';
import { FOUND_COST, foundCityBlockReason } from '../src/state/multiCity';
import {
  canJoinFaction,
  FACTION_CHOICES,
  factionLabel,
  joinLabel,
  orderPeace,
  territoryFactionLine,
  treatiesBrokenByJoining,
  treatyForbidden,
  type JoinableFaction,
} from '../src/state/factions';
import { caravanLabel } from '../src/state/caravans';
import {
  MESSAGE_MAX_CHARS,
  MESSAGE_NOTICE_MS,
  bodyLength,
  messageTime,
  otherName,
  readTarget,
  sendBlockReason,
  sendErrorText,
  unreadTotal,
  visibleIncoming,
  whileAwayText,
  type MessagesState,
} from '../src/state/messages';
import {
  activeAlliances,
  allianceCountdown,
  allianceOffers,
  allianceProblem,
  breakAllianceWarning,
  isAlly,
  isAtWarWith,
  openWars,
  warScore,
  warCountdown,
  warRecordText,
  warProblem,
  declareWarWarning,
  surrenderWarning,
  MAX_CLAN_ALLIES,
  canExpel,
  canInvite,
  clanBadge,
  lastChatId,
  LEADER_INACTIVE_DAYS,
  liveInvitations,
  managesInvitations,
  myRole,
  nameProblem,
  roleLabel,
  sameClan,
  tagProblem,
  transferWarning,
  withTag,
  type ClansState,
} from '../src/state/clans';
import type { Point, Tile } from '../src/render/iso';

const WS_URL = process.env.NEXT_PUBLIC_GAME_SERVER_WS_URL ?? 'ws://localhost:8080/ws';

// Lo que el HUD muestra del mundo, por valor: misma vista, misma referencia, y
// React no se vuelve a renderizar con el delta de una unidad que no le incumbe.
const selectHud = createHudSelector();

/** Lo que ve el jugador cuando su sesión web ya no sirve y vuelve al formulario. */
const SESSION_ENDED_MESSAGE = 'Tu sesión ha caducado o ya no es válida. Vuelve a entrar.';

export default function Page() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  // La civilización sólo la pide el alta; no hay ninguna elegida de antemano (Q1).
  const [civilization, setCivilization] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [authError, setAuthError] = useState<string | null>(null);
  const [connection, setConnection] = useState<ConnectionState>('idle');
  // Al cargar se intenta retomar la sesión web antes de enseñar el formulario.
  const [resuming, setResuming] = useState(true);
  const [leaving, setLeaving] = useState(false);
  const [logoutError, setLogoutError] = useState<string | null>(null);

  const clientRef = useRef<GameClient | null>(null);
  // Strict Mode monta dos veces: la sesión se retoma una sola.
  const resumeStartedRef = useRef(false);
  // Cuándo decirle al servidor dónde mira la cámara (frontend.md §7.4.1).
  const viewSyncRef = useRef(new ViewSync());

  const applyServerMessage = useWorldStore((s) => s.applyServerMessage);
  const selectUnit = useWorldStore((s) => s.selectUnit);
  const selectedUnitId = useWorldStore((s) => s.selectedUnitId);
  const playerId = useWorldStore((s) => s.playerId);
  const hud = useWorldStore(selectHud);
  const lastError = useWorldStore((s) => s.lastError);
  // La unidad seleccionada es un objeto inmutable que el store reemplaza al
  // cambiar: suscribirse a ella re-renderiza sólo con sus propios deltas.
  const selectedUnit = useWorldStore((s) => (s.selectedUnitId !== null ? s.units.get(s.selectedUnitId) : undefined));

  // Un segundero mientras la ciudad entrena, para la cuenta atrás del HUD.
  const [, setTick] = useState(0);

  // Diplomacia (SPEC-DIPLOMACY): los tratados abiertos y la ciudad ajena sobre
  // la que se hizo clic sin unidad seleccionada, para proponerle un tratado.
  const treaties = useWorldStore((s) => s.treaties);
  const [diplomacyTarget, setDiplomacyTarget] = useState<CityView | null>(null);
  // Facciones (SPEC-FACTIONS): la ventana «Elegir bando» y el bando que se mira en ella.
  const [factionPickerOpen, setFactionPickerOpen] = useState(false);
  // Clanes (SPEC-CLANS): el panel, el borrador del canal y el de fundar.
  const clans = useWorldStore((s) => s.clans);
  const [clanOpen, setClanOpen] = useState(false);
  // El panel «Tecnologías» (SPEC-TECHNOLOGIES §4.7).
  const [techOpen, setTechOpen] = useState(false);
  // El panel «Mercado» (SPEC-MARKETS §4.8): sus ofertas y el último tablón.
  const market = useWorldStore((s) => s.market);
  const [marketOpen, setMarketOpen] = useState(false);
  const [clanDraft, setClanDraft] = useState('');
  const [clanForm, setClanForm] = useState({ name: '', tag: '' });
  // Comercio (SPEC-TRADE): las ofertas pendientes y el trato que se está armando.
  const tradeOffers = useWorldStore((s) => s.tradeOffers);
  const [draft, setDraft] = useState<TradeDraft>({ giveResource: 'WOOD', giveAmount: 100, wantResource: 'STONE', wantAmount: 50 });
  const countdown = anyCountdown(treaties);
  // Caravanas (SPEC-CARAVANS): las que el jugador ve con su carga, con su llegada.
  const caravans = useWorldStore((s) => s.caravans);
  const travelling = caravans.length > 0;
  useEffect(() => {
    if (!travelling) return;
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [travelling]);
  // Conquista (SPEC-TERRITORY-CONQUEST): el territorio del guerrero seleccionado.
  // Cada TerritoryView se sustituye al cambiar, así que la suscripción a él sólo
  // re-renderiza con sus propios cambios.
  const claimTerritory = useWorldStore((s) =>
    selectedUnit && unitAttacks(selectedUnit.unitType) && selectedUnit.playerId === s.playerId
      ? territoryAt(s.territories.values(), selectedUnit.x, selectedUnit.y)
      : undefined,
  );
  const claimEndsAtMs = claimTerritory?.claim?.endsAtMs;
  useEffect(() => {
    if (claimEndsAtMs === undefined) return;
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [claimEndsAtMs]);
  // La caída de una ciudad (SPEC-CITY-FALL): la ciudad ajena junto a la unidad
  // seleccionada, a la que se ofrece «Asaltar», y la cuenta atrás de un asalto sobre
  // ella o sobre la propia.
  const assaultCity = useWorldStore((s) =>
    selectedUnit && selectedUnit.playerId === s.playerId ? assaultTargetNear(s.cities.values(), selectedUnit, s.playerId) : undefined,
  );
  const assaultEndsAtMs = assaultCity?.assault?.endsAtMs ?? hud.ownCity?.assault?.endsAtMs;
  useEffect(() => {
    if (assaultEndsAtMs === undefined) return;
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [assaultEndsAtMs]);
  useEffect(() => {
    if (!countdown) return;
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [countdown]);
  // La cuenta atrás de una entrada en un bando, propia o de la ciudad ajena que se mira.
  const joinAtMs = hud.ownCity?.factionJoin?.atMs ?? diplomacyTarget?.factionJoin?.atMs;
  useEffect(() => {
    if (joinAtMs === undefined) return;
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [joinAtMs]);
  const training = hud.ownCity?.production ?? null;
  // El avance de era también lleva cuenta atrás (SPEC-ECON-STONE).
  const advancing = hud.ownCity?.advance ?? null;
  useEffect(() => {
    if (!training && !advancing) return;
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [training, advancing]);

  // El aviso de saqueo se apaga solo: un render más cuando caduca.
  const pillagedAtMs = hud.ownCity?.pillagedAtMs;
  useEffect(() => {
    if (pillagedAtMs === undefined) return;
    const left = pillagedAtMs + PILLAGE_ALERT_MS - (clientRef.current?.serverTimeMs() ?? Date.now());
    if (left <= 0) return;
    const id = setTimeout(() => setTick((n) => n + 1), left + 50);
    return () => clearTimeout(id);
  }, [pillagedAtMs]);

  // El aviso de asedio, igual (SPEC-COMBAT-SIEGE RN-COMBAT-038).
  const besiegedAtMs = hud.ownCity?.besiegedAtMs;
  useEffect(() => {
    if (besiegedAtMs === undefined) return;
    const left = besiegedAtMs + SIEGE_ALERT_MS - (clientRef.current?.serverTimeMs() ?? Date.now());
    if (left <= 0) return;
    const id = setTimeout(() => setTick((n) => n + 1), left + 50);
    return () => clearTimeout(id);
  }, [besiegedAtMs]);

  useEffect(() => () => clientRef.current?.disconnect(), []);

  // Registro (SPEC-EVENT-LOG): se pide al conectar y al volver a la pestaña tras
  // un rato fuera (RN-LOG-007). El servidor no empuja entradas: los avisos en vivo
  // van aparte (SPEC-LIVE-NOTICES) y no tocan la lista.
  const eventLog = useWorldStore((s) => s.eventLog);
  const [logOpen, setLogOpen] = useState(false);
  const requestEventLog = useCallback((beforeId: number | null) => {
    const client = clientRef.current;
    if (!client || client.getState() !== 'ready') return;
    const requestId = client.fetchEventLog(beforeId ?? undefined);
    useWorldStore.getState().expectEventLogPage(requestId, beforeId);
  }, []);
  useEffect(() => {
    if (connection === 'ready') requestEventLog(null);
  }, [connection, requestEventLog]);
  useEffect(() => {
    let hiddenSince: number | null = null;
    const onVisibility = () => {
      // El contador del título de los avisos (SPEC-LIVE-NOTICES Q7).
      useWorldStore.getState().setTabHidden(document.visibilityState === 'hidden');
      if (document.visibilityState === 'hidden') {
        hiddenSince = Date.now();
        return;
      }
      if (shouldRefetchOnVisible(hiddenSince, Date.now())) {
        requestEventLog(null);
        // La bandeja también (SPEC-MESSAGES §4.3).
        const client = clientRef.current;
        if (client && client.getState() === 'ready') client.listMessages();
      }
      hiddenSince = null;
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, [requestEventLog]);
  // Dar por visto lo cargado: al servidor, que marca hasta donde puede (ADR-028
  // §6), y a la pestaña, que no lo vuelve a enseñar.
  const markLogSeen = useCallback(() => {
    const target = markTarget(useWorldStore.getState().eventLog);
    if (target === null) return;
    clientRef.current?.markEventLogSeen(target);
    useWorldStore.getState().markEventLogSeen(target);
  }, []);
  const toggleLog = useCallback(() => {
    if (logOpen) markLogSeen();
    else {
      // Abrir el registro pide la primera página, que trae lo avisado
      // (RN-NOTICE-004), y borra el punto de avisos.
      requestEventLog(null);
      useWorldStore.getState().markNoticesOpened();
    }
    setLogOpen(!logOpen);
  }, [logOpen, markLogSeen, requestEventLog]);

  // Avisos en vivo (SPEC-LIVE-NOTICES): la franja se apaga sola, con un render más
  // cuando caduca el siguiente grupo; el título cuenta lo llegado con la pestaña
  // oculta; «Ir» lleva la cámara al tile, nunca sola (Q4).
  const notices = useWorldStore((s) => s.notices);
  const [focus, setFocus] = useState<{ x: number; y: number; nonce: number } | null>(null);
  const goTo = useCallback((tile: { x: number; y: number }) => {
    setFocus((f) => ({ ...tile, nonce: (f?.nonce ?? 0) + 1 }));
  }, []);

  useEffect(() => {
    const now = clientRef.current?.serverTimeMs() ?? Date.now();
    const expiry = nextNoticeExpiry(notices, now);
    if (expiry === null) return;
    const id = setTimeout(() => setTick((n) => n + 1), expiry - now + 50);
    return () => clearTimeout(id);
  }, [notices]);
  // Mensajes (SPEC-MESSAGES): la bandeja se pide al conectar y al volver a la
  // pestaña, como el registro; la conversación abierta se da por leída al verla.
  const messages = useWorldStore((s) => s.messages);
  const [messagesOpen, setMessagesOpen] = useState(false);
  const [messageDraft, setMessageDraft] = useState('');
  const requestInbox = useCallback(() => {
    const client = clientRef.current;
    if (!client || client.getState() !== 'ready') return;
    client.listMessages();
  }, []);
  useEffect(() => {
    if (connection === 'ready') requestInbox();
  }, [connection, requestInbox]);
  const requestConversation = useCallback((otherPlayerId: string, beforeId: number | null) => {
    const client = clientRef.current;
    if (!client || client.getState() !== 'ready') return;
    const requestId = client.fetchMessages(otherPlayerId, beforeId ?? undefined);
    useWorldStore.getState().expectMessagePage(requestId, otherPlayerId, beforeId);
  }, []);
  // «Escribir»: abre el panel en la conversación con ese jugador.
  const writeTo = useCallback(
    (otherPlayerId: string, otherCityId: number | null, otherCityName: string | null) => {
      if (otherPlayerId === useWorldStore.getState().playerId) return;
      useWorldStore.getState().openConversation(otherPlayerId, otherCityId, otherCityName);
      useWorldStore.getState().dismissMessagesWhileAway();
      setMessageDraft('');
      setMessagesOpen(true);
      requestConversation(otherPlayerId, null);
    },
    [requestConversation],
  );
  const toggleMessages = useCallback(() => {
    if (!messagesOpen) {
      requestInbox();
      useWorldStore.getState().dismissMessagesWhileAway();
    } else {
      // Con el panel cerrado no hay conversación a la vista: lo que llegue cuenta
      // como no leído y avisa (SPEC-MESSAGES Q7).
      useWorldStore.getState().closeConversation();
    }
    setMessagesOpen(!messagesOpen);
  }, [messagesOpen, requestInbox]);
  const openConv = messagesOpen ? messages.open : null;
  const lastOpenId = openConv?.loaded ? (openConv.messages.at(-1)?.id ?? 0) : 0;
  useEffect(() => {
    if (lastOpenId === 0 || document.visibilityState === 'hidden') return;
    const target = readTarget(useWorldStore.getState().messages);
    if (!target) return;
    clientRef.current?.markMessagesRead(target.otherPlayerId, target.upToId);
    useWorldStore.getState().markConversationRead(target.otherPlayerId);
  }, [lastOpenId]);
  const sendDraft = () => {
    const client = clientRef.current;
    const open = useWorldStore.getState().messages.open;
    if (!client || !open || sendBlockReason(messageDraft) !== null) return;
    const requestId = client.sendMessage(open.otherPlayerId, messageDraft);
    useWorldStore.getState().expectMessageSend(requestId, open.otherPlayerId);
    setMessageDraft('');
  };
  const incomingMessage = visibleIncoming(messages, clientRef.current?.serverTimeMs() ?? Date.now());
  useEffect(() => {
    const n = messages.incoming;
    if (!n) return;
    const left = n.sentAtMs + MESSAGE_NOTICE_MS - (clientRef.current?.serverTimeMs() ?? Date.now());
    if (left <= 0) return;
    const id = setTimeout(() => setTick((t) => t + 1), left + 50);
    return () => clearTimeout(id);
  }, [messages.incoming]);

  // Clanes (SPEC-CLANS §4.8): al abrir el panel con clan, la página más reciente
  // del canal; con el panel abierto, lo que llega se da por leído.
  const requestClanChat = useCallback((beforeId: number | null) => {
    const client = clientRef.current;
    if (!client || client.getState() !== 'ready') return;
    const requestId = client.fetchClanChat(beforeId ?? undefined);
    useWorldStore.getState().expectClanChatPage(requestId, beforeId);
  }, []);
  const clanId = clans.clan?.id ?? null;
  useEffect(() => {
    // Con clan, el canal se pide al conectar: el contador del botón cuenta lo no leído.
    if (connection === 'ready' && clanId !== null) requestClanChat(null);
  }, [connection, clanId, requestClanChat]);
  const toggleClan = useCallback(() => {
    const next = !clanOpen;
    if (next && useWorldStore.getState().clans.clan) requestClanChat(null);
    useWorldStore.getState().setClanChatReading(next);
    setClanOpen(next);
  }, [clanOpen, requestClanChat]);
  const lastClanId = clanOpen ? lastChatId(clans) : 0;
  useEffect(() => {
    if (lastClanId === 0 || document.visibilityState === 'hidden') return;
    clientRef.current?.readClanChat(lastClanId);
    useWorldStore.getState().setClanChatReading(true);
  }, [lastClanId]);
  const sendClanDraft = () => {
    const client = clientRef.current;
    if (!client || sendBlockReason(clanDraft) !== null) return;
    useWorldStore.getState().expectClanChatSend(client.sendClanChat(clanDraft));
    setClanDraft('');
  };

  useEffect(() => {
    document.title = documentTitle(BASE_TITLE, notices, messages.hiddenIncoming);
  }, [notices, messages.hiddenIncoming]);

  // Abre el WebSocket con el primer ticket; las reconexiones piden el suyo con
  // la cookie de sesión, porque los tickets son de un solo uso.
  const connectWith = useCallback(
    async (ticket: string) => {
      const client = new GameClient({
        url: WS_URL,
        fetchTicket: ticketProvider(ticket),
        onMessage: (message) => {
          applyServerMessage(message);
          trackSession(viewSyncRef.current, message);
        },
        onStateChange: (state) => {
          // Fuera de una sesión lista no se envía session.view.
          if (state !== 'ready') viewSyncRef.current.suspend();
          setConnection(state);
        },
        // Un UNAUTHORIZED del transporte —ticket rechazado porque la sesión web
        // caducó o se cerró, o cierre 4401/4403— deja al cliente en `closed` y
        // a la página en el formulario: el mensaje es para una persona, no un
        // código (frontend.md §3.6). El resto se muestra tal cual.
        onError: (code, message) =>
          setAuthError(code === 'UNAUTHORIZED' ? SESSION_ENDED_MESSAGE : `${code}: ${message}`),
      });
      clientRef.current = client;
      await client.connect();
    },
    [applyServerMessage],
  );

  // Una recarga no pide entrar otra vez: si la cookie de sesión sigue viva, se
  // conecta directamente (frontend.md §3.2). Sin sesión, el formulario.
  useEffect(() => {
    if (resumeStartedRef.current) return;
    resumeStartedRef.current = true;
    void (async () => {
      try {
        const ticket = await resumeSession();
        if (ticket) await connectWith(ticket);
      } catch (err) {
        setAuthError(
          err instanceof AuthError
            ? `No se pudo recuperar la sesión (${err.code}): entra de nuevo.`
            : `No se pudo recuperar la sesión: ${String(err)}`,
        );
      } finally {
        setResuming(false);
      }
    })();
  }, [connectWith]);

  const startSession = useCallback(
    async (mode: 'login' | 'register') => {
      setBusy(true);
      setAuthError(null);
      try {
        const creds = { username, password };
        // El alta y el inicio de sesión devuelven un ticket y dejan una cookie de
        // sesión. La contraseña no se guarda.
        const { ticket } = await (mode === 'register'
          ? register({ ...creds, civilization: civilization ?? '' })
          : login(creds));
        setPassword('');
        await connectWith(ticket);
      } catch (err) {
        setAuthError(err instanceof AuthError ? `${err.code}: ${err.message}` : String(err));
      } finally {
        setBusy(false);
      }
    },
    [username, password, civilization, connectWith],
  );

  // «Salir»: primero se borra la sesión web en el servidor y sólo después se
  // cierra el WebSocket. Si el servidor no lo confirma, se sigue conectado: con
  // la cookie viva, una recarga volvería a entrar. Cerrar el WebSocket es una
  // desconexión normal (SPEC-PRESENCE): la ciudad sigue el camino de siempre
  // hacia OFFLINE_PENDING y PROTECTED.
  const endSession = useCallback(async () => {
    setLeaving(true);
    setLogoutError(null);
    try {
      await logout();
    } catch (err) {
      setLogoutError(
        `No se pudo cerrar la sesión${err instanceof AuthError ? ` (${err.code})` : ''}: inténtalo de nuevo.`,
      );
      return;
    } finally {
      setLeaving(false);
    }
    const client = clientRef.current;
    clientRef.current = null;
    client?.disconnect();
    useWorldStore.getState().reset();
    setAuthError(null);
  }, []);

  const serverTimeMs = useCallback(() => clientRef.current?.serverTimeMs() ?? Date.now(), []);

  // Cada frame: si la cámara cruzó de chunk, el servidor tiene que saberlo.
  const handleCameraCenter = useCallback((center: Point) => {
    const tile = viewSyncRef.current.update(center, performance.now());
    if (tile) clientRef.current?.setView(tile);
  }, []);

  const handleTileClick = useCallback(
    (tile: Tile) => {
      const client = clientRef.current;
      if (!client) return;
      if (selectedUnitId === null) {
        // Sin unidad: un clic en una ciudad ajena abre la propuesta de tratado.
        const c = cityAt(tile);
        const own = useWorldStore.getState().playerId;
        setDiplomacyTarget(c && c.ownerPlayerId !== own ? c : null);
        return;
      }
      // Un clic sobre la zona urbana de una ciudad (su 3 × 3) es la orden de
      // entrar en ella, como un clic sobre un edificio en la referencia. El
      // cliente sólo elige el objetivo; si se puede lo decide el servidor.
      // Un clic sobre un bosque con un aldeano es la orden de recolectar en él
      // (SPEC-ECON-WOOD, frontend.md §4.4). El cliente sólo elige la orden; si
      // se puede lo decide el servidor.
      const state = useWorldStore.getState();
      const unit = state.units.get(selectedUnitId);
      if (!unit) return;
      const terrain = state.world ? terrainAt(state.terrain, state.world.chunkSize, tile.x, tile.y) : undefined;
      const order = decideTileOrder(tile, unit, terrain, cityAt, state.playerId, (t) => buildingAt(state.buildings, t));
      // Una caravana no admite órdenes (RN-CARAVAN-003): no se manda nada.
      if (order.kind === 'none') return;
      if (order.kind === 'blocked') {
        // SPEC-SIEGE-ENGINES §4.5: el motivo, sin mandar la orden.
        useWorldStore.setState({ lastError: { code: 'INVALID_TARGET', message: order.reason } });
        return;
      }
      if (order.kind === 'demolish') {
        // Un guerrero sobre una casa ajena: derribarla (SPEC-COMBAT-SIEGE Q10).
        client.demolishBuilding(selectedUnitId, order.buildingId);
        return;
      }
      if (order.kind === 'pillage') {
        // Un guerrero sobre una ciudad ajena: saquearla (SPEC-COMBAT-PILLAGE).
        client.pillageCity(selectedUnitId, order.cityId);
        return;
      }
      if (order.kind === 'garrison') {
        client.garrisonUnit(selectedUnitId, order.cityId);
        selectUnit(null);
        return;
      }
      if (order.kind === 'gather') {
        client.gatherWood(selectedUnitId, tile);
        return;
      }
      // Sólo se envía la INTENCIÓN. La ruta, el tiempo y la validez los decide
      // el servidor; el cliente ni siquiera intenta adivinarlos.
      client.moveUnit(selectedUnitId, tile);
    },
    [selectedUnitId, selectUnit],
  );

  const handleUnitClick = useCallback(
    (unitId: number) => {
      const state = useWorldStore.getState();
      const unit = state.units.get(unitId);
      if (!unit) return;
      // Sólo se seleccionan unidades propias; un clic sobre una ajena con un
      // guerrero seleccionado es la orden de atacarla (SPEC-COMBAT-RAID). Si se
      // puede lo decide el servidor.
      const selected = selectedUnitId !== null ? state.units.get(selectedUnitId) : undefined;
      const click = decideUnitClick(unit, selected, playerId);
      if (click.kind === 'select') selectUnit(unitId);
      else if (click.kind === 'attack' && selectedUnitId !== null) {
        // RN-FAC-003: entre dos de la Orden no se ofrece el ataque; el servidor
        // lo rechazaría igual.
        const cities = [...state.cities.values()];
        const mine = cities.find((c) => c.ownerPlayerId === playerId)?.faction;
        const theirs = cities.find((c) => c.ownerPlayerId === unit.playerId)?.faction;
        if (orderPeace(mine, theirs)) {
          useWorldStore.setState({ lastError: { code: 'INVALID_TARGET', message: 'los dos sois de la Orden' } });
          return;
        }
        // RN-CLAN-004: entre compañeros de clan, tampoco.
        const myClan = cities.find((c) => c.ownerPlayerId === playerId)?.clan;
        const theirClan = cities.find((c) => c.ownerPlayerId === unit.playerId)?.clan;
        if (sameClan(myClan, theirClan)) {
          useWorldStore.setState({ lastError: { code: 'INVALID_TARGET', message: 'sois del mismo clan' } });
          return;
        }
        // RN-CLANALLY-003: entre clanes aliados, tampoco (la paz residual la dice el servidor).
        if (isAlly(useWorldStore.getState().clans, theirClan)) {
          useWorldStore.setState({ lastError: { code: 'INVALID_TARGET', message: 'vuestros clanes están aliados' } });
          return;
        }
        clientRef.current?.attackUnit(selectedUnitId, unitId);
      }
    },
    [playerId, selectUnit, selectedUnitId],
  );

  if (resuming) {
    return (
      <main style={styles.center}>
        <div style={styles.panel}>
          <h1 style={{ margin: '0 0 0.25rem', fontSize: '1.4rem' }}>Empires Online</h1>
          <p style={{ margin: 0, color: 'var(--muted)', fontSize: '0.85rem' }}>Recuperando tu sesión…</p>
        </div>
      </main>
    );
  }

  if (connection === 'idle' || connection === 'closed') {
    return (
      <main style={styles.center}>
        <div style={styles.panel}>
          <h1 style={{ margin: '0 0 0.25rem', fontSize: '1.4rem' }}>Empires Online</h1>
          <p style={{ margin: '0 0 1.25rem', color: 'var(--muted)', fontSize: '0.85rem' }}>
            El mundo sigue existiendo aunque cierres esta pestaña.
          </p>

          <div style={{ marginBottom: '0.75rem' }}>
            <label htmlFor="username">Nombre de usuario</label>
            <input
              id="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
              placeholder="3-24 caracteres"
            />
          </div>

          <div style={{ marginBottom: '1.1rem' }}>
            <label htmlFor="password">Contraseña</label>
            <input
              id="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              placeholder="mínimo 8 caracteres"
            />
          </div>

          <fieldset style={styles.civilizations} aria-label="Civilización">
            <legend style={{ fontSize: '0.8rem', padding: 0, marginBottom: '0.4rem' }}>
              Para fundar un imperio, elige tu civilización. <strong>Es para siempre.</strong>
            </legend>
            {CIVILIZATIONS.map((c) => (
              <label key={c.code} style={styles.civilization} title={traitSummary(c.code)}>
                <input
                  type="radio"
                  name="civilization"
                  value={c.code}
                  checked={civilization === c.code}
                  onChange={() => setCivilization(c.code)}
                  style={{ width: 'auto', margin: '0.15rem 0 0', padding: 0 }}
                />
                <span>
                  <strong>{c.name}</strong>
                  {traitLines(c).map((l) => (
                    <span key={l.text} style={{ display: 'block', color: l.good ? 'var(--ok)' : 'var(--danger)' }}>
                      {l.text}
                    </span>
                  ))}
                  {civilizationUnitLine(c.code) && (
                    <span style={{ display: 'block', color: 'var(--muted)' }}>{civilizationUnitLine(c.code)}</span>
                  )}
                </span>
              </label>
            ))}
          </fieldset>

          <div style={{ display: 'flex', gap: '0.6rem' }}>
            <button onClick={() => void startSession('login')} disabled={busy}>
              Entrar
            </button>
            <button
              className="secondary"
              onClick={() => void startSession('register')}
              disabled={busy || civilization === null}
              title={civilization === null ? 'Elige antes tu civilización: no se puede cambiar después' : undefined}
            >
              Fundar imperio
            </button>
          </div>

          {authError && (
            <p style={{ color: 'var(--danger)', fontSize: '0.8rem', marginTop: '1rem' }}>{authError}</p>
          )}
        </div>
      </main>
    );
  }

  const { ownUnitCount, garrisoned, ownCity } = hud;

  // Órdenes de tratado (SPEC-DIPLOMACY). Romper es irreversible y avisa de su
  // preaviso: se confirma antes.
  const runTreatyAction = (action: TreatyAction, treatyId: number) => {
    const client = clientRef.current;
    if (!client) return;
    if (action === 'accept' || action === 'reject') {
      client.respondTreaty(treatyId, action === 'accept');
      return;
    }
    if (action === 'break' && !window.confirm('¿Romper el tratado? Seguirá vigente 15 minutos y después terminará.')) return;
    client.cancelTreaty(treatyId);
  };

  // Órdenes de comercio (SPEC-TRADE). Aceptar mueve los dos almacenes en el acto.
  const runTradeAction = (action: TradeAction, offerId: number) => {
    const client = clientRef.current;
    if (!client) return;
    if (action === 'withdraw') {
      client.cancelTrade(offerId);
      return;
    }
    client.respondTrade(offerId, action === 'accept');
  };
  const offerTrade = (target: CityView) => {
    const { give, want } = draftLines(draft);
    clientRef.current?.proposeTrade(target.ownerPlayerId, give, want);
    setDiplomacyTarget(null);
  };

  // «Casa»: el primer hueco libre del 3 × 3, sin pedir clic (frontend.md §4.4).
  // Un clic en la zona urbana ya es la orden de guarnecerse.
  const buildHouse = () => {
    const state = useWorldStore.getState();
    if (!ownCity) return;
    const tile = firstFreeHouseTile(ownCity, buildingsOf(state.buildings, ownCity.id));
    if (tile) clientRef.current?.buildHouse(ownCity.id, tile);
  };

  // «Taller»: como «Casa», en el primer hueco libre (SPEC-CIVILIZATION-GOODS §4.6).
  const buildWorkshop = () => {
    const state = useWorldStore.getState();
    if (!ownCity) return;
    const tile = firstFreeHouseTile(ownCity, buildingsOf(state.buildings, ownCity.id));
    if (tile) clientRef.current?.buildWorkshop(ownCity.id, tile);
  };
  // «Mercado»: como «Taller», en el primer hueco libre (SPEC-MARKETS §4.8).
  const buildMarket = () => {
    const state = useWorldStore.getState();
    if (!ownCity) return;
    const tile = firstFreeHouseTile(ownCity, buildingsOf(state.buildings, ownCity.id));
    if (tile) clientRef.current?.buildMarket(ownCity.id, tile);
  };
  // «Castillo»: como «Taller», en el primer hueco libre (SPEC-CASTLE-AGE §4.7).
  const buildCastle = () => {
    const state = useWorldStore.getState();
    if (!ownCity) return;
    const tile = firstFreeHouseTile(ownCity, buildingsOf(state.buildings, ownCity.id));
    if (tile) clientRef.current?.buildCastle(ownCity.id, tile);
  };
  // «Muralla»: en el centro de la ciudad, que siempre está libre (SPEC-SIEGE-ENGINES §4.5).
  const buildWall = () => {
    if (!ownCity) return;
    clientRef.current?.buildWall(ownCity.id, { x: ownCity.centerX, y: ownCity.centerY });
  };
  // Los edificios de la ciudad propia, para los motivos del taller y de la elaboración.
  const ownBuildings = ownCity ? buildingsOf(useWorldStore.getState().buildings, ownCity.id) : [];
  const ownGood = ownCity ? goodOf(ownCity.civilization) : null;

  // Por qué no se puede reclamar con el guerrero seleccionado (SPEC-TERRITORY-CONQUEST §5),
  // para sí o para el clan propio (SPEC-CLAN-TERRITORIES §5).
  const myClanId = clans.clan?.id ?? null;
  const claimCheck = claimTerritory
    ? {
        unit: selectedUnit,
        playerId,
        territory: claimTerritory,
        cities: useWorldStore.getState().cities.values(),
        treaties,
        nowMs: serverTimeMs(),
        myClanId,
      }
    : null;
  const claimReason = claimCheck ? claimBlockReason(claimCheck) : null;
  // Por qué no se puede asaltar la ciudad ajena de al lado (SPEC-CITY-FALL §5).
  const assaultReason = assaultCity
    ? assaultBlockReason({
        unit: selectedUnit,
        playerId,
        city: assaultCity,
        ownCity: ownCity ?? undefined,
        buildings: useWorldStore.getState().buildings.values(),
        treaties,
        nowMs: serverTimeMs(),
      })
    : null;
  // Por qué el aldeano seleccionado no puede fundar (SPEC-MULTI-CITY §5).
  const foundReason = foundCityBlockReason(selectedUnit, hud.ownCities);
  const clanClaimReason = claimCheck && clans.clan ? claimBlockReason({ ...claimCheck, cities: useWorldStore.getState().cities.values(), forClan: true }) : null;

  return (
    <main style={{ height: '100vh', display: 'flex', flexDirection: 'column' }}>
      <header style={styles.hud}>
        <span style={{ fontWeight: 600 }}>Empires Online</span>
        <span style={{ color: connection === 'ready' ? 'var(--ok)' : 'var(--danger)' }}>
          {connection === 'ready' ? '● conectado' : `● ${connection}`}
        </span>
        {ownCity && (
          <>
            <span style={{ color: 'var(--muted)' }}>
              {ownCity.name} — <span title={traitSummary(ownCity.civilization)}>{civilizationLabel(ownCity.civilization)}</span>{' '}
              — {eraLabel(ownCity.era)} — <span title="Tu bando (SPEC-FACTIONS)">{factionLabel(ownCity.faction)}</span>
            </span>
            {ownCity.factionJoin && (
              <span style={{ color: 'var(--muted)' }} title="Hasta entonces sigues siendo Neutral a todos los efectos">
                {joinLabel(ownCity.factionJoin, serverTimeMs(), true)}
              </span>
            )}
            {canJoinFaction(ownCity) && (
              <button
                className="secondary"
                onClick={() => setFactionPickerOpen(true)}
                title="Únete a la Orden o al Caos. Es para siempre"
              >
                Elegir bando
              </button>
            )}
            <span
              style={{ color: overPopulationLimit(ownCity) ? 'var(--danger)' : 'var(--muted)' }}
              title={overPopulationLimit(ownCity) ? 'Perdiste casas: no puedes entrenar hasta volver bajo el límite' : undefined}
            >
              Población {ownCity.population}/{ownCity.populationLimit}
            </span>
            <span style={{ color: 'var(--muted)' }}>Presencia: {ownCity.presenceState}</span>
            <button
              className="secondary"
              onClick={toggleLog}
              aria-expanded={logOpen}
              title="Lo que te pasó en los últimos días: muertes, saqueos, casas derribadas y cambios en tus tratados"
            >
              Registro{unseenEntries(eventLog).length > 0 ? ` (${unseenEntries(eventLog).length})` : ''}
              {hasUnreadNotices(notices) && (
                <span style={{ color: 'var(--danger)', marginLeft: '0.3rem' }} aria-label="avisos nuevos" title="Avisos nuevos">
                  ●
                </span>
              )}
            </button>
            <button
              className="secondary"
              onClick={toggleMessages}
              aria-expanded={messagesOpen}
              title="Tus conversaciones con otros jugadores"
            >
              Mensajes{unreadTotal(messages) > 0 ? ` (${unreadTotal(messages)})` : ''}
            </button>
            <button
              className="secondary"
              onClick={toggleClan}
              aria-expanded={clanOpen}
              title="Tu clan: sus miembros, su canal y tus invitaciones"
            >
              {clans.clan ? `Clan [${clans.clan.tag}]` : 'Clan'}
              {clanBadge(clans, playerId, serverTimeMs()) > 0 ? ` (${clanBadge(clans, playerId, serverTimeMs())})` : ''}
            </button>
            <button
              className="secondary"
              onClick={() => setTechOpen(!techOpen)}
              aria-expanded={techOpen}
              title="Mejoras para siempre que se investigan en la ciudad, una a la vez"
            >
              Tecnologías{ownCity.technologies.length > 0 ? ` (${ownCity.technologies.length})` : ''}
            </button>
            {ownCity.era !== 'STONE_AGE' && (
              <button
                className="secondary"
                onClick={() => {
                  // El tablón se pide al abrir el panel (SPEC-MARKETS §4.8).
                  if (!marketOpen) clientRef.current?.listMarket();
                  setMarketOpen(!marketOpen);
                }}
                aria-expanded={marketOpen}
                title="Compra y vende a cualquiera sin un tratado: ofertas públicas en lotes, con comisión"
              >
                Mercado{market.own.length > 0 ? ` (${market.own.length})` : ''}
              </button>
            )}
            <span title="Madera de la ciudad">Madera {ownCity.stocks.WOOD}</span>
            <span title="Piedra de la ciudad: se recoge en las colinas">Piedra {ownCity.stocks.STONE}</span>
            {/* Los bienes: el propio siempre, los ajenos si hay (SPEC-CIVILIZATION-GOODS §4.6). */}
            {GOODS.filter((g) => g === ownGood || ownCity.stocks[g] > 0)
              .sort((a, b) => (a === ownGood ? -1 : b === ownGood ? 1 : 0))
              .map((g) => (
                <span
                  key={g}
                  title={
                    g === ownGood
                      ? 'Tu bien: sólo lo elabora tu taller y sólo sirve para comerciar'
                      : 'Bien de otra civilización: cuenta para la Edad de Hierro'
                  }
                >
                  {goodLabel(g).charAt(0).toUpperCase() + goodLabel(g).slice(1)} {ownCity.stocks[g]}
                </span>
              ))}
            {pillageAlertActive(ownCity, serverTimeMs()) && (
              <span style={{ color: 'var(--danger)', fontWeight: 600 }} role="alert">
                ¡Están saqueando tu ciudad!
              </span>
            )}
            {siegeAlertActive(ownCity, serverTimeMs()) && (
              <span style={{ color: 'var(--danger)', fontWeight: 600 }} role="alert">
                ¡Están asediando tu ciudad!
              </span>
            )}
            {ownCity.assault && (
              <span style={{ color: 'var(--danger)', fontWeight: 600 }} role="alert">
                {assaultLabel(ownCity, playerId, serverTimeMs())}
              </span>
            )}
            <button className="secondary" onClick={buildHouse} title="Encarga una casa en el primer hueco libre junto al centro: +5 de población al terminar">
              Casa
            </button>
            <button
              className="secondary"
              onClick={buildWorkshop}
              disabled={workshopBlockReason(ownCity, ownBuildings) !== null}
              title={
                workshopBlockReason(ownCity, ownBuildings) ??
                `Encarga el taller: ${WORKSHOP_COST.WOOD} de madera y ${WORKSHOP_COST.STONE} de piedra. Elabora ${ownGood ? goodLabel(ownGood) : 'tu bien'}, que sólo produce tu civilización`
              }
            >
              Taller
            </button>
            {ownCity.era !== 'STONE_AGE' && !ownBuildings.some((b) => b.type === 'MARKET') && (
              <button
                className="secondary"
                onClick={buildMarket}
                disabled={marketBuildBlockReason(ownCity, ownBuildings) !== null}
                title={
                  marketBuildBlockReason(ownCity, ownBuildings) ??
                  `Encarga el mercado: ${MARKET_COST.WOOD} de madera y ${MARKET_COST.STONE} de piedra, unos 60 s. Deja publicar y comprar en el tablón público`
                }
              >
                Construir mercado
              </button>
            )}
            {(ownCity.era === 'IRON_AGE' || ownCity.era === 'CASTLE_AGE') && (
              <button
                className="secondary"
                onClick={buildCastle}
                disabled={castleBlockReason(ownCity, ownBuildings) !== null}
                title={
                  castleBlockReason(ownCity, ownBuildings) ??
                  `Encarga el castillo: ${CASTLE_COST.WOOD} de madera y ${CASTLE_COST.STONE} de piedra, unos 180 s. Golpea a quien ataque tu ciudad o tus unidades a 5 casillas; no da población`
                }
              >
                Castillo
              </button>
            )}
            {ownCity.era !== 'STONE_AGE' && (
              <button
                className="secondary"
                onClick={buildWall}
                disabled={wallBlockReason(ownCity, ownBuildings) !== null}
                title={
                  wallBlockReason(ownCity, ownBuildings) ??
                  `Levanta la muralla: ${WALL_COST.STONE} de piedra y ${WALL_COST.WOOD} de madera, unos 120 s. Completa, sólo un ariete daña tu ciudad, y sólo a la muralla; el saqueo no cambia`
                }
              >
                Muralla
              </button>
            )}
            {ownBuildings.some((b) => b.type === 'WORKSHOP') &&
              (ownCity.craft ? (
                <span style={{ color: 'var(--muted)' }}>
                  Elaborando {ownCity.craft.amount} de {goodLabel(ownCity.craft.good)}:{' '}
                  {secondsUntil(ownCity.craft.completesAtMs, serverTimeMs())} s
                </span>
              ) : (
                <button
                  className="secondary"
                  onClick={() => clientRef.current?.craft(ownCity.id)}
                  disabled={craftBlockReason(ownCity, ownBuildings) !== null}
                  title={
                    craftBlockReason(ownCity, ownBuildings) ??
                    `Elabora ${CRAFT_AMOUNT} de ${ownGood ? goodLabel(ownGood) : 'tu bien'}: ${CRAFT_COST.WOOD} de madera y ${CRAFT_COST.STONE} de piedra, unos 30 s. No impide entrenar`
                  }
                >
                  Elaborar ({CRAFT_AMOUNT} de {ownGood ? goodLabel(ownGood) : 'tu bien'})
                </button>
              ))}
            <button
              className="secondary"
              onClick={() => clientRef.current?.trainVillager(ownCity.id)}
              disabled={trainBlockReason(ownCity) !== null}
              title={trainBlockReason(ownCity) ?? 'Entrena un aldeano en la ciudad'}
            >
              Aldeano
            </button>
            <button
              className="secondary"
              onClick={() => clientRef.current?.trainUnit(ownCity.id, 'WARRIOR')}
              disabled={trainBlockReason(ownCity) !== null}
              title={
                trainBlockReason(ownCity) ??
                'Entrena un guerrero en la ciudad: 60 de madera. Selecciónalo y haz clic en una unidad enemiga para atacarla, en una casa enemiga para derribarla o en el resto de una ciudad enemiga para saquearla'
              }
            >
              Guerrero
            </button>
            {(() => {
              // SPEC-CIVILIZATION-UNITS §4.6: la unidad propia, y sólo ella.
              const propia = civilizationUnit(ownCity.civilization);
              if (!propia) return null;
              const reason = civilizationUnitBlockReason(ownCity);
              return (
                <button
                  className="secondary"
                  onClick={() => clientRef.current?.trainUnit(ownCity.id, propia)}
                  disabled={reason !== null}
                  title={
                    reason ??
                    `Entrena un ${unitTypeLabel(propia)}, la unidad propia de tu civilización: ${trainCostText(propia, ownCity.civilization)}, unos ${Math.round((unitInfoOf(propia)?.trainMs ?? 0) / 1000)} s. Hace todo lo que hace un guerrero`
                  }
                >
                  {unitName(propia)} ({trainCostText(propia, ownCity.civilization)})
                </button>
              );
            })()}
            {(ownCity.era === 'IRON_AGE' || ownCity.era === 'CASTLE_AGE') && (
              <button
                className="secondary"
                onClick={() => clientRef.current?.trainUnit(ownCity.id, 'RAM')}
                disabled={ramBlockReason(ownCity) !== null}
                title={
                  ramBlockReason(ownCity) ??
                  `Entrena un ariete: ${trainCostText('RAM', ownCity.civilization)}, unos 40 s, dos plazas. Lento y frágil: sólo golpea edificios y es lo único que daña una muralla en pie`
                }
              >
                Ariete
              </button>
            )}
            {ownCity.production && (
              <span style={{ color: 'var(--muted)' }}>
                Entrenando {unitTypeLabel(ownCity.production.unitType)}:{' '}
                {secondsUntil(ownCity.production.completesAtMs, serverTimeMs())} s
              </span>
            )}
            {nextEra(ownCity.era) !== null && !ownCity.advance && (() => {
              const foreign = foreignGoods(ownCity.stocks, ownCity.civilization);
              const reason = advanceBlockReason(ownCity, foreign);
              const next = nextEra(ownCity.era)!;
              const what =
                next === 'BRONZE_AGE'
                  ? `Avanza a la Edad de Bronce: ${BRONZE_AGE_COST.WOOD} de madera y ${BRONZE_AGE_COST.STONE} de piedra, 90 s sin entrenar. El límite de población sube de 20 a 50`
                  : next === 'IRON_AGE'
                    ? `Avanza a la Edad de Hierro: ${IRON_AGE_COST.WOOD} de madera, ${IRON_AGE_COST.STONE} de piedra y ${IRON_AGE_COST.FOREIGN_GOODS} bienes de otras civilizaciones (se cobran en el orden vino, seda, especias, pieles; el tuyo nunca), 180 s sin entrenar. El límite sube de 50 a 100`
                    : `Avanza a la Edad de los Castillos: ${CASTLE_AGE_COST.WOOD} de madera, ${CASTLE_AGE_COST.STONE} de piedra y ${CASTLE_AGE_COST.FOREIGN_GOODS} bienes de otras civilizaciones (el tuyo nunca), 360 s sin entrenar. El límite sube de 100 a 150 y podrás construir un castillo`;
              const goodsNeeded = next === 'CASTLE_AGE' ? CASTLE_AGE_COST.FOREIGN_GOODS : IRON_AGE_COST.FOREIGN_GOODS;
              return (
                <button
                  className="secondary"
                  onClick={() => clientRef.current?.advanceEra(ownCity.id)}
                  disabled={reason !== null}
                  title={reason ?? what}
                >
                  {eraLabel(next)}
                  {next !== 'BRONZE_AGE' && ` (${foreign}/${goodsNeeded} bienes)`}
                </button>
              );
            })()}
            {ownCity.advance && (
              <span style={{ color: 'var(--muted)' }}>
                Avanzando a la {eraLabel(ownCity.advance.era)}:{' '}
                {secondsUntil(ownCity.advance.completesAtMs, serverTimeMs())} s
              </span>
            )}
            {ownCity.research && (
              <span style={{ color: 'var(--muted)' }}>
                Investigando {technologyName(ownCity.research.technology)}:{' '}
                {secondsUntil(ownCity.research.completesAtMs, serverTimeMs())} s
              </span>
            )}
          </>
        )}
        {garrisoned.map((u) => (
          <button
            key={u.id}
            className="secondary"
            onClick={() => clientRef.current?.ungarrisonUnit(u.id)}
            title={`Guarnecida en la ciudad ${u.garrisonCityId}`}
          >
            Sacar unidad {u.id}
          </button>
        ))}
        {claimTerritory && selectedUnit && (
          <>
            <span style={{ color: claimTerritory.claim ? 'var(--danger)' : 'var(--muted)' }}>
              {claimLabel(claimTerritory, playerId, serverTimeMs(), myClanId) ??
                `${claimTerritory.name}: ${territoryOwnerLabel(claimTerritory, playerId, useWorldStore.getState().cities.values(), myClanId)}`}
            </span>
            {territoryFactionLine(claimTerritory, playerId, ownCity?.faction, myClanId) && (
              <span style={{ color: 'var(--muted)' }}>{territoryFactionLine(claimTerritory, playerId, ownCity?.faction, myClanId)}</span>
            )}
            <button
              className="secondary"
              onClick={() => clientRef.current?.claimTerritory(selectedUnit.id)}
              disabled={claimReason !== null}
              title={
                claimReason ??
                'El guerrero planta su estandarte aquí: si aguanta 5 minutos (10 si el territorio tiene dueño), el territorio es tuyo y tu ciudad gana 5 de población'
              }
            >
              Reclamar territorio
            </button>
            {clans.clan && (
              <button
                className="secondary"
                onClick={() => clientRef.current?.claimTerritory(selectedUnit.id, true)}
                disabled={clanClaimReason !== null}
                title={
                  clanClaimReason ??
                  'El guerrero planta el estandarte de tu clan: si aguanta, el territorio es del clan y tierra afín para todos sus miembros (sin población para nadie)'
                }
              >
                Reclamar para el clan [{clans.clan.tag}]
              </button>
            )}
          </>
        )}
        {assaultCity && selectedUnit && (
          <>
            {assaultCity.assault && (
              <span style={{ color: 'var(--danger)' }}>{assaultLabel(assaultCity, playerId, serverTimeMs())}</span>
            )}
            <button
              className="secondary"
              onClick={() => clientRef.current?.assaultCity(selectedUnit.id, assaultCity.id)}
              disabled={assaultReason !== null}
              title={
                assaultReason ??
                `La unidad va al anillo de ${assaultCity.name} y planta su estandarte: si aguanta 15 minutos, la ciudad cae y se arrasa`
              }
            >
              Asaltar {assaultCity.name}
            </button>
          </>
        )}
        {selectedUnit && selectedUnit.playerId === playerId && selectedUnit.unitType === 'VILLAGER' && (
          <button
            className="secondary"
            onClick={() => clientRef.current?.foundCity(selectedUnit.id)}
            disabled={foundReason !== null}
            title={
              foundReason ??
              `El aldeano funda una ciudad nueva aquí: ${FOUND_COST.WOOD} de madera y ${FOUND_COST.STONE} de piedra de su ciudad (SPEC-MULTI-CITY)`
            }
          >
            Fundar ciudad
          </button>
        )}
        <span style={{ marginLeft: 'auto', color: 'var(--muted)' }}>
          {selectedUnitId !== null
            ? `${selectedUnit ? unitTitle(selectedUnit.unitType) : 'Unidad'} ${selectedUnitId}${describeUnit(selectedUnit)} — ${helpFor(selectedUnit)}`
            : `${ownUnitCount} unidades — clic en una para seleccionarla · flechas, WASD o arrastre con el botón derecho para mover la cámara · H va a tus ciudades`}
        </span>
        <button
          className="secondary"
          onClick={() => void endSession()}
          disabled={leaving}
          title="Cierra la sesión en este navegador. Tu ciudad sigue en el mundo y, como en cualquier desconexión, pasa a estar protegida"
        >
          Salir
        </button>
      </header>

      {(diplomacyTarget || treaties.length > 0 || tradeOffers.length > 0) && (
        <div style={{ ...styles.hud, flexWrap: 'wrap', gap: '0.75rem' }} aria-label="Diplomacia">
          {diplomacyTarget && (
            <>
              <span title={traitSummary(diplomacyTarget.civilization)}>
                {withTag(diplomacyTarget.name, diplomacyTarget.clan)} ({civilizationLabel(diplomacyTarget.civilization)}, {eraLabel(diplomacyTarget.era)},{' '}
                {factionLabel(diplomacyTarget.faction)}
                {diplomacyTarget.factionJoin ? ` — ${joinLabel(diplomacyTarget.factionJoin, serverTimeMs(), false)}` : ''}
                {isAlly(clans, diplomacyTarget.clan) ? ` — Aliado de [${clans.clan?.tag}]` : ''}
                {isAtWarWith(clans, diplomacyTarget.clan) ? ` — En guerra con [${clans.clan?.tag}]` : ''}):
              </span>
              {proposableTypes(treaties, diplomacyTarget.ownerPlayerId)
                // RN-FAC-005: la Orden y el Caos no firman la paz; el comercio, sí.
                .filter((tipo) => !treatyForbidden(tipo, ownCity?.faction, diplomacyTarget.faction))
                .map((tipo) => (
                <button
                  key={tipo}
                  className="secondary"
                  onClick={() => { clientRef.current?.proposeTreaty(diplomacyTarget.ownerPlayerId, tipo); setDiplomacyTarget(null); }}
                >
                  {PROPOSE_LABEL[tipo]}
                </button>
              ))}
              {canTradeWith(treaties, diplomacyTarget.ownerPlayerId) &&
                (pendingWith(tradeOffers, diplomacyTarget.ownerPlayerId) ? (
                  <span style={{ color: 'var(--muted)' }}>Ya hay una oferta de comercio pendiente</span>
                ) : (
                  <span style={{ display: 'inline-flex', gap: '0.4rem', alignItems: 'center' }} aria-label="Ofrecer un trato">
                    <span>Doy</span>
                    <input
                      type="number" min={1} step={1} value={draft.giveAmount} style={{ width: '5rem' }} aria-label="Cantidad que doy"
                      onChange={(e) => setDraft({ ...draft, giveAmount: Number(e.target.value) })}
                    />
                    <select
                      value={draft.giveResource} aria-label="Recurso que doy"
                      onChange={(e) => setDraft({ ...draft, giveResource: e.target.value as ResourceType })}
                    >
                      {TRADE_RESOURCES.map((r) => (
                        <option key={r} value={r}>de {resourceName(r)}</option>
                      ))}
                    </select>
                    <span>y pido</span>
                    <input
                      type="number" min={1} step={1} value={draft.wantAmount} style={{ width: '5rem' }} aria-label="Cantidad que pido"
                      onChange={(e) => setDraft({ ...draft, wantAmount: Number(e.target.value) })}
                    />
                    <select
                      value={draft.wantResource} aria-label="Recurso que pido"
                      onChange={(e) => setDraft({ ...draft, wantResource: e.target.value as ResourceType })}
                    >
                      {TRADE_RESOURCES.map((r) => (
                        <option key={r} value={r}>de {resourceName(r)}</option>
                      ))}
                    </select>
                    {(() => {
                      const reason = offerBlockReason(draft, ownCity?.stocks);
                      return (
                        <button
                          className="secondary"
                          disabled={reason !== null}
                          title={reason ?? 'El otro jugador lo acepta o lo rechaza; si lo acepta, cada uno manda en una caravana lo que da, y llega cuando llega su caravana'}
                          onClick={() => offerTrade(diplomacyTarget)}
                        >
                          Ofrecer trato
                        </button>
                      );
                    })()}
                  </span>
                ))}
              <button
                className="secondary"
                title="Escribe un mensaje al dueño de esta ciudad"
                onClick={() => { writeTo(diplomacyTarget.ownerPlayerId, diplomacyTarget.id, diplomacyTarget.name); setDiplomacyTarget(null); }}
              >
                Escribir
              </button>
              {diplomacyTarget.clan && (
                <button
                  className="secondary"
                  title="Los miembros de su clan"
                  onClick={() => { clientRef.current?.lookupClan(diplomacyTarget.clan!.id); setClanOpen(true); }}
                >
                  Ver clan
                </button>
              )}
              {canInvite(clans, playerId, diplomacyTarget, serverTimeMs()) && (
                <button
                  className="secondary"
                  title="Le llega una invitación a tu clan; si la acepta, estaréis en paz"
                  onClick={() => clientRef.current?.inviteToClan(diplomacyTarget.ownerPlayerId, true)}
                >
                  Invitar al clan
                </button>
              )}
              <button className="secondary" onClick={() => setDiplomacyTarget(null)} title="Cerrar">×</button>
            </>
          )}
          {treaties.map((tr) => (
            <span key={tr.id} style={{ display: 'inline-flex', gap: '0.4rem', alignItems: 'center' }}>
              <span>{treatyLabel(tr, serverTimeMs())}</span>
              {treatyActions(tr).map((a) => (
                <button key={a} className="secondary" onClick={() => runTreatyAction(a, tr.id)}>
                  {ACTION_LABEL[a]}
                </button>
              ))}
              <button className="secondary" onClick={() => writeTo(tr.otherPlayerId, tr.otherCityId, tr.otherCityName)}>
                Escribir
              </button>
            </span>
          ))}
          {tradeOffers.map((o) => (
            <span key={`trade-${o.id}`} style={{ display: 'inline-flex', gap: '0.4rem', alignItems: 'center' }}>
              <span>{tradeLabel(o, serverTimeMs())}</span>
              {tradeActions(o).map((a) => (
                <button key={a} className="secondary" onClick={() => runTradeAction(a, o.id)}>
                  {ACTION_LABEL[a]}
                </button>
              ))}
              <button className="secondary" onClick={() => writeTo(o.otherPlayerId, o.otherCityId, o.otherCityName)}>
                Escribir
              </button>
            </span>
          ))}
        </div>
      )}

      {caravans.length > 0 && (
        <div style={{ ...styles.hud, flexWrap: 'wrap', gap: '0.75rem' }} aria-label="Caravanas en camino">
          <strong>Caravanas en camino:</strong>
          {caravans.map((c) => (
            <span key={`caravan-${c.unitId}`} style={{ display: 'inline-flex', gap: '0.4rem', alignItems: 'center' }}>
              <span>{caravanLabel(c, playerId, serverTimeMs())}</span>
              <button
                className="secondary"
                title="Lleva la cámara a la caravana, si está a la vista, o a la ciudad de la que salió"
                onClick={() => {
                  const state = useWorldStore.getState();
                  const u = state.units.get(c.unitId);
                  const from = state.cities.get(c.fromCityId);
                  if (u) goTo({ x: u.x, y: u.y });
                  else if (from) goTo({ x: from.centerX, y: from.centerY });
                }}
              >
                Ir
              </button>
              {(() => {
                const other = caravanCounterpart(c, playerId, useWorldStore.getState().cities);
                return other ? (
                  <button className="secondary" onClick={() => writeTo(other.playerId, other.cityId, other.cityName)}>
                    Escribir
                  </button>
                ) : null;
              })()}
            </span>
          ))}
        </div>
      )}

      {messagesOpen && (
        <MessagesPanel
          messages={messages}
          playerId={playerId}
          nowMs={serverTimeMs()}
          draft={messageDraft}
          onDraft={setMessageDraft}
          onSend={sendDraft}
          onOpen={(c) => writeTo(c.otherPlayerId, c.otherCityId, c.otherCityName)}
          onBack={() => { useWorldStore.getState().closeConversation(); requestInbox(); }}
          onOlder={(before) => openConv && requestConversation(openConv.otherPlayerId, before)}
          onBlock={(other, blocked) => clientRef.current?.blockPlayer(other, blocked)}
          onClose={toggleMessages}
        />
      )}

      {clanOpen && (
        <ClanPanel
          clans={clans}
          playerId={playerId}
          nowMs={serverTimeMs()}
          form={clanForm}
          onForm={setClanForm}
          onCreate={() => clientRef.current?.createClan(clanForm.name, clanForm.tag)}
          onRespond={(id, accept) => clientRef.current?.respondClan(id, accept)}
          onWithdraw={(p) => clientRef.current?.inviteToClan(p, false)}
          onExpel={(p) => clientRef.current?.expelFromClan(p)}
          onPromote={(p, officer) => clientRef.current?.promoteInClan(p, officer)}
          onTransfer={(p) => clientRef.current?.transferClan(p)}
          onLeave={() => clientRef.current?.leaveClan()}
          draft={clanDraft}
          onDraft={setClanDraft}
          onSend={sendClanDraft}
          onOlder={(before) => requestClanChat(before)}
          onCloseInfo={() => useWorldStore.getState().closeClanInfo()}
          onProposeAlliance={(id) => clientRef.current?.proposeClanAlliance(id)}
          onRespondAlliance={(id, accept) => clientRef.current?.respondClanAlliance(id, accept)}
          onCancelAlliance={(id) => clientRef.current?.cancelClanAlliance(id)}
          onDeclareWar={(id) => clientRef.current?.declareClanWar(id)}
          onWarPeace={(id, offer) => clientRef.current?.clanWarPeace(id, offer)}
          onSurrender={(id) => clientRef.current?.surrenderClanWar(id)}
          onGoTerritory={goTo}
          onClose={toggleClan}
        />
      )}

      {techOpen && ownCity && (
        <TechnologyPanel
          city={ownCity}
          nowMs={serverTimeMs()}
          onResearch={(code) => clientRef.current?.researchTechnology(ownCity.id, code)}
          onClose={() => setTechOpen(false)}
        />
      )}

      {marketOpen && ownCity && (
        <MarketPanel
          city={ownCity}
          cityBuildings={ownBuildings}
          market={market}
          nowMs={serverTimeMs()}
          onRefresh={() => clientRef.current?.listMarket()}
          onPost={(d) =>
            clientRef.current?.postMarketOffer(
              { resource: d.giveResource, amount: d.giveAmount },
              { resource: d.wantResource, amount: d.wantAmount },
              d.lots,
            )
          }
          onBuy={(id, lots) => clientRef.current?.buyMarketOffer(id, lots)}
          onCancel={(id) => clientRef.current?.cancelMarketOffer(id)}
          onClose={() => setMarketOpen(false)}
        />
      )}

      {logOpen && (
        <section style={styles.log} aria-label="Registro">
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', marginBottom: '0.5rem' }}>
            <strong>Registro</strong>
            <span style={{ color: 'var(--muted)' }}>Los últimos 7 días</span>
            <button className="secondary" style={{ marginLeft: 'auto' }} onClick={toggleLog}>
              Cerrar
            </button>
          </div>
          {eventLog.entries.length === 0 ? (
            <p style={{ margin: 0, color: 'var(--muted)' }}>
              {eventLog.loaded ? 'No te ha pasado nada en estos días.' : 'Cargando…'}
            </p>
          ) : (
            <EventLogList entries={eventLog.entries} isNew={(e) => isUnseen(eventLog, e)} nowMs={serverTimeMs()} onWrite={writeTo} />
          )}
          {eventLog.nextBeforeId !== null && (
            <button className="secondary" style={{ marginTop: '0.5rem' }} onClick={() => requestEventLog(eventLog.nextBeforeId)}>
              Ver más
            </button>
          )}
        </section>
      )}

      {!logOpen && (unseenEntries(eventLog).length > 0 || messages.whileAway !== null) && (
        <div style={styles.overlay} role="dialog" aria-modal="true" aria-labelledby="while-away-title">
          <div style={{ ...styles.panel, width: 'min(480px, 92vw)', maxHeight: '80vh', overflowY: 'auto' }}>
            <h2 id="while-away-title" style={{ margin: '0 0 0.75rem', fontSize: '1.1rem' }}>
              Mientras no estabas
            </h2>
            {messages.whileAway && (
              <p style={{ margin: '0 0 0.75rem', display: 'flex', gap: '0.6rem', alignItems: 'center' }}>
                <span>{whileAwayText(messages.whileAway)}</span>
                <button
                  className="secondary"
                  onClick={() => { markLogSeen(); useWorldStore.getState().dismissMessagesWhileAway(); requestInbox(); setMessagesOpen(true); }}
                >
                  Leer
                </button>
              </p>
            )}
            <EventLogList entries={unseenEntries(eventLog)} isNew={() => false} nowMs={serverTimeMs()} />
            {moreUnseenBeyond(eventLog) && (
              <p style={{ color: 'var(--muted)', fontSize: '0.8rem' }}>Hay más en el Registro.</p>
            )}
            <button
              style={{ marginTop: '0.75rem' }}
              onClick={() => { markLogSeen(); useWorldStore.getState().dismissMessagesWhileAway(); }}
              autoFocus
            >
              Entendido
            </button>
          </div>
        </div>
      )}

      {factionPickerOpen && ownCity && canJoinFaction(ownCity) && (
        <FactionPicker
          broken={(target) =>
            treatiesBrokenByJoining(treaties, useWorldStore.getState().cities.values(), target).map(
              (t) => t.otherCityName ?? 'otra ciudad',
            )
          }
          onJoin={(target) => { clientRef.current?.joinFaction(target); setFactionPickerOpen(false); }}
          onClose={() => setFactionPickerOpen(false)}
        />
      )}

      {logoutError && (
        <div style={{ ...styles.hud, color: 'var(--danger)' }}>{logoutError}</div>
      )}

      <div style={{ flex: 1, minHeight: 0, position: 'relative' }}>
        <GameCanvas
          serverTimeMs={serverTimeMs}
          onTileClick={handleTileClick}
          onUnitClick={handleUnitClick}
          onCameraCenter={handleCameraCenter}
          focus={focus}
        />
        <NoticeStrip
          groups={visibleNotices(notices, serverTimeMs())}
          onGo={goTo}
          onWrite={writeTo}
          incoming={incomingMessage}
          onRead={() => incomingMessage && writeTo(incomingMessage.otherPlayerId, null, incomingMessage.otherCityName)}
        />
      </div>

      {lastError && (
        <footer style={{ ...styles.hud, borderTop: '1px solid var(--border)', borderBottom: 'none' }}>
          <span style={{ color: 'var(--danger)' }}>{readableError(lastError.code, lastError.message)}</span>
        </footer>
      )}
    </main>
  );
}

/** El título de la pestaña sin avisos (el de app/layout.tsx). */
const BASE_TITLE = 'Empires Online';

/**
 * La franja de avisos en vivo sobre el mapa (SPEC-LIVE-NOTICES Q2): tres como
 * mucho, con el texto del registro y «Ir» cuando traen tile.
 */
function NoticeStrip({
  groups,
  onGo,
  onWrite,
  incoming,
  onRead,
}: {
  groups: readonly NoticeGroup[];
  onGo: (tile: { x: number; y: number }) => void;
  onWrite: (otherPlayerId: string, otherCityId: number | null, otherCityName: string | null) => void;
  incoming: MessagesState['incoming'];
  onRead: () => void;
}) {
  return (
    <ul style={styles.notices} role="status" aria-live="polite" aria-label="Avisos">
      {incoming && (
        <li key={`message-${incoming.otherPlayerId}-${incoming.sentAtMs}`} style={styles.notice}>
          <span>{otherName(incoming.otherCityName)} te ha escrito</span>
          <button className="secondary" style={{ marginLeft: 'auto' }} onClick={onRead}>
            Leer
          </button>
        </li>
      )}
      {groups.map((g) => {
        const tile = noticeTile(g);
        return (
          <li key={`${g.key}-${g.firstAtMs}`} style={styles.notice}>
            <span style={{ color: noticeIsLoss(g) ? 'var(--danger)' : undefined }}>{noticeText(g)}</span>
            {tile && (
              <button
                className="secondary"
                style={{ marginLeft: 'auto' }}
                onClick={() => onGo(tile)}
                title={`Llevar la cámara a (${tile.x}, ${tile.y})`}
              >
                Ir
              </button>
            )}
            <button
              className="secondary"
              style={tile ? undefined : { marginLeft: 'auto' }}
              onClick={() => onWrite(g.otherPlayerId, g.otherCityId, g.otherCityName)}
              title="Escribe un mensaje a este jugador"
            >
              Escribir
            </button>
          </li>
        );
      })}
    </ul>
  );
}

/** Las entradas del registro, con cuánto hace; las pérdidas, resaltadas. */
/**
 * El panel «Tecnologías» (SPEC-TECHNOLOGIES §4.7): las que la ciudad puede llegar a
 * tener, por era, con su efecto, su coste y su plazo; las que tiene, marcadas; la
 * que se investiga, con su cuenta atrás; las demás, con «Investigar» o con su
 * motivo.
 */
function TechnologyPanel({
  city,
  nowMs,
  onResearch,
  onClose,
}: {
  city: CityView;
  nowMs: number;
  onResearch: (code: string) => void;
  onClose: () => void;
}) {
  return (
    <section style={styles.log} aria-label="Tecnologías">
      <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', marginBottom: '0.5rem' }}>
        <strong>Tecnologías</strong>
        <span style={{ color: 'var(--muted)' }}>Para siempre; una a la vez y nunca mientras la ciudad avanza de edad</span>
        <button className="secondary" style={{ marginLeft: 'auto' }} onClick={onClose}>
          Cerrar
        </button>
      </div>
      {technologiesFor(city.civilization).map((grupo) => (
        <div key={grupo.era} style={{ marginBottom: '0.5rem' }}>
          <div style={{ color: 'var(--muted)', margin: '0.25rem 0' }}>{eraLabel(grupo.era)}</div>
          <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.3rem' }}>
            {grupo.technologies.map((t) => {
              const tiene = city.technologies.includes(t.code);
              const enCurso = city.research?.technology === t.code;
              const motivo = tiene || enCurso ? null : researchBlockReason(city, t);
              return (
                <li key={t.code} style={{ display: 'flex', alignItems: 'center', gap: '0.6rem', flexWrap: 'wrap' }}>
                  <span style={{ minWidth: '11rem' }}>
                    <strong>{t.name}</strong>
                    {t.civilization !== null && <span style={{ color: 'var(--muted)' }}> · propia</span>}
                  </span>
                  <span>{effectText(t)}</span>
                  <span style={{ color: 'var(--muted)' }}>
                    {costText(t, city.civilization)}, unos {researchSeconds(t, city.civilization, city.technologies)} s
                  </span>
                  {tiene && <span style={{ color: 'var(--ok, #5bd15b)' }}>✓ La tienes</span>}
                  {enCurso && city.research && (
                    <span style={{ color: 'var(--muted)' }}>Investigando: {secondsUntil(city.research.completesAtMs, nowMs)} s</span>
                  )}
                  {!tiene && !enCurso && (
                    <>
                      <button
                        className="secondary"
                        disabled={motivo !== null}
                        title={motivo ?? `Investiga ${t.name}: ${costText(t, city.civilization)}`}
                        onClick={() => onResearch(t.code)}
                      >
                        Investigar
                      </button>
                      {motivo && <span style={{ color: 'var(--muted)' }}>{motivo}</span>}
                    </>
                  )}
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </section>
  );
}

/**
 * El panel «Mercado» (SPEC-MARKETS §4.8): el tablón con las ofertas de los demás,
 * el formulario de venta y las ofertas propias. Todo lo decide el servidor; los
 * motivos sólo evitan mandar lo que se rechazaría seguro.
 */
function MarketPanel({
  city,
  cityBuildings,
  market,
  nowMs,
  onRefresh,
  onPost,
  onBuy,
  onCancel,
  onClose,
}: {
  city: CityView;
  cityBuildings: readonly BuildingView[];
  market: MarketState;
  nowMs: number;
  onRefresh: () => void;
  onPost: (d: MarketDraft) => void;
  onBuy: (offerId: number, lots: number) => void;
  onCancel: (offerId: number) => void;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<'buy' | 'sell' | 'own'>('buy');
  const [lotsFor, setLotsFor] = useState<Record<number, number>>({});
  const [draft, setDraft] = useState<MarketDraft>({ giveResource: 'STONE', giveAmount: 20, wantResource: 'WOOD', wantAmount: 30, lots: 3 });
  const sinMercado = marketMissingReason(cityBuildings);
  const venta = postCost(draft.giveAmount, draft.lots);
  const motivoVenta = postBlockReason(draft, city.stocks, cityBuildings, market.own.length);
  const numero = (v: string) => (v === '' ? 0 : Math.trunc(Number(v)));
  return (
    <section style={styles.log} aria-label="Mercado">
      <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '0.5rem', flexWrap: 'wrap' }}>
        <strong>Mercado</strong>
        {(['buy', 'sell', 'own'] as const).map((t) => (
          <button key={t} className={tab === t ? undefined : 'secondary'} onClick={() => setTab(t)} aria-pressed={tab === t}>
            {t === 'buy' ? 'Comprar' : t === 'sell' ? 'Vender' : `Mis ofertas (${market.own.length})`}
          </button>
        ))}
        {sinMercado && <span style={{ color: 'var(--muted)' }}>{sinMercado}: no puedes publicar ni comprar</span>}
        <button className="secondary" style={{ marginLeft: 'auto' }} onClick={onClose}>
          Cerrar
        </button>
      </div>

      {tab === 'buy' && (
        <div>
          <div style={{ display: 'flex', gap: '0.6rem', alignItems: 'center', marginBottom: '0.4rem' }}>
            <span style={{ color: 'var(--muted)' }}>Las ofertas abiertas de otros jugadores. Cada lote paga además una comisión de la décima parte.</span>
            <button className="secondary" style={{ marginLeft: 'auto' }} onClick={onRefresh}>
              Actualizar
            </button>
          </div>
          {market.board === null ? (
            <p style={{ color: 'var(--muted)' }}>Cargando el tablón…</p>
          ) : market.board.length === 0 ? (
            <p style={{ color: 'var(--muted)' }}>No hay ofertas en el mercado.</p>
          ) : (
            <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.35rem' }}>
              {market.board.map((o: MarketOfferView) => {
                const lots = Math.min(lotsFor[o.id] ?? 1, Math.max(o.lotsLeft, 1));
                const coste = buyCost(o, lots);
                const motivo = buyBlockReason(o, lots, city, cityBuildings);
                const d = offerDistance(city, o);
                return (
                  <li key={o.id} style={{ display: 'flex', gap: '0.6rem', alignItems: 'center', flexWrap: 'wrap' }}>
                    <span style={{ minWidth: '9rem' }}>
                      <strong>{o.cityName ?? 'otro jugador'}</strong>
                      <span style={{ color: 'var(--muted)' }}>
                        {o.civilization ? ` · ${o.civilization.toLowerCase()}` : ''}
                        {d !== null ? ` · ${d} casillas` : ''}
                      </span>
                    </span>
                    <span>{lotLabel(o)}</span>
                    <span style={{ color: 'var(--muted)' }}>
                      quedan {o.lotsLeft} · caduca en {formatHours(o.expiresAtMs - nowMs)}
                    </span>
                    <input
                      type="number"
                      min={1}
                      max={o.lotsLeft}
                      value={lots}
                      aria-label="Lotes"
                      style={{ width: '4rem' }}
                      onChange={(e) => setLotsFor({ ...lotsFor, [o.id]: numero(e.target.value) })}
                    />
                    <button
                      className="secondary"
                      disabled={motivo !== null}
                      title={motivo ?? `Pagas ${coste.pay} de ${resourceName(o.want.resource)} más ${coste.commission} de comisión; recibes ${coste.receive} de ${resourceName(o.give.resource)} en una caravana`}
                      onClick={() => onBuy(o.id, lots)}
                    >
                      Comprar ({coste.total} de {resourceName(o.want.resource)})
                    </button>
                    {motivo && <span style={{ color: 'var(--muted)' }}>{motivo}</span>}
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      )}

      {tab === 'sell' && (
        <div style={{ display: 'grid', gap: '0.4rem' }}>
          <div style={{ display: 'flex', gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
            <span>Cada lote da</span>
            <input type="number" min={1} value={draft.giveAmount} aria-label="Cantidad que da cada lote" style={{ width: '5rem' }}
              onChange={(e) => setDraft({ ...draft, giveAmount: numero(e.target.value) })} />
            <select value={draft.giveResource} aria-label="Recurso que das"
              onChange={(e) => setDraft({ ...draft, giveResource: e.target.value as MarketDraft['giveResource'] })}>
              {TRADE_RESOURCES.map((r) => <option key={r} value={r}>{resourceName(r)}</option>)}
            </select>
            <span>y pide</span>
            <input type="number" min={1} value={draft.wantAmount} aria-label="Cantidad que pide cada lote" style={{ width: '5rem' }}
              onChange={(e) => setDraft({ ...draft, wantAmount: numero(e.target.value) })} />
            <select value={draft.wantResource} aria-label="Recurso que pides"
              onChange={(e) => setDraft({ ...draft, wantResource: e.target.value as MarketDraft['wantResource'] })}>
              {TRADE_RESOURCES.map((r) => <option key={r} value={r}>{resourceName(r)}</option>)}
            </select>
            <span>·</span>
            <input type="number" min={1} max={MARKET_MAX_LOTS} value={draft.lots} aria-label="Lotes" style={{ width: '4rem' }}
              onChange={(e) => setDraft({ ...draft, lots: numero(e.target.value) })} />
            <span>lotes</span>
          </div>
          <span style={{ color: 'var(--muted)' }}>
            Salen de tu almacén {venta.custody} de {resourceName(draft.giveResource)} en custodia y {venta.commission} de comisión, que no vuelve aunque la retires o caduque (24 h).
          </span>
          <div style={{ display: 'flex', gap: '0.6rem', alignItems: 'center' }}>
            <button disabled={motivoVenta !== null} title={motivoVenta ?? 'Publica la oferta en el tablón'} onClick={() => onPost(draft)}>
              Publicar
            </button>
            {motivoVenta && <span style={{ color: 'var(--muted)' }}>{motivoVenta}</span>}
          </div>
        </div>
      )}

      {tab === 'own' && (
        market.own.length === 0 ? (
          <p style={{ color: 'var(--muted)' }}>No tienes ofertas abiertas.</p>
        ) : (
          <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.35rem' }}>
            {market.own.map((o) => (
              <li key={o.id} style={{ display: 'flex', gap: '0.6rem', alignItems: 'center', flexWrap: 'wrap' }}>
                <span>{ownOfferLabel(o)}</span>
                <span style={{ color: 'var(--muted)' }}>caduca en {formatHours(o.expiresAtMs - nowMs)}</span>
                <button className="secondary" title="Vuelve a tu almacén lo que queda en custodia; la comisión, no" onClick={() => onCancel(o.id)}>
                  Retirar
                </button>
              </li>
            ))}
          </ul>
        )
      )}
    </section>
  );
}

function EventLogList({
  entries,
  isNew,
  nowMs,
  onWrite,
}: {
  entries: readonly EventLogEntry[];
  isNew: (e: EventLogEntry) => boolean;
  nowMs: number;
  onWrite?: (otherPlayerId: string, otherCityId: number | null, otherCityName: string | null) => void;
}) {
  return (
    <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.4rem', fontSize: '0.85rem' }}>
      {entries.map((e) => (
        <li key={e.id} style={{ display: 'flex', gap: '0.6rem', alignItems: 'baseline' }}>
          <span style={{ color: isLoss(e) ? 'var(--danger)' : undefined, fontWeight: isNew(e) ? 600 : undefined }}>
            {entryText(e)}
          </span>
          <span style={{ color: 'var(--muted)', fontSize: '0.75rem', whiteSpace: 'nowrap' }}>{timeAgo(e.lastAtMs, nowMs)}</span>
          {/* MARKET_EXPIRED no tiene otro jugador a quien escribir (SPEC-MARKETS §6). */}
          {onWrite && e.otherPlayerId !== null && (
            <button
              className="secondary"
              style={{ fontSize: '0.72rem', padding: '0.1rem 0.4rem' }}
              onClick={() => onWrite(e.otherPlayerId!, e.otherCityId, e.otherCityName)}
            >
              Escribir
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}

/**
 * El otro jugador de una caravana, para «Escribir»: quien la mandó, si no eres tú;
 * si es tuya, el dueño de la ciudad adonde va, si está en el mapa.
 */
function caravanCounterpart(
  c: { senderPlayerId: string; ownerPlayerId: string; fromCityId: number; fromCityName: string | null; toCityId: number; toCityName: string | null },
  playerId: string | null,
  cities: ReadonlyMap<number, CityView>,
): { playerId: string; cityId: number | null; cityName: string | null } | null {
  if (c.senderPlayerId !== playerId) return { playerId: c.senderPlayerId, cityId: c.fromCityId, cityName: c.fromCityName };
  if (c.ownerPlayerId !== playerId) return { playerId: c.ownerPlayerId, cityId: null, cityName: null };
  const to = cities.get(c.toCityId);
  return to && to.ownerPlayerId !== playerId ? { playerId: to.ownerPlayerId, cityId: to.id, cityName: to.name } : null;
}

/**
 * La ventana «Elegir bando» (SPEC-FACTIONS §4.6): los dos bandos con lo que da y
 * cuesta cada uno, los pactos y alianzas que se romperán, y una confirmación
 * que dice que la decisión es para siempre.
 */
function FactionPicker({
  broken,
  onJoin,
  onClose,
}: {
  broken: (target: JoinableFaction) => string[];
  onJoin: (target: JoinableFaction) => void;
  onClose: () => void;
}) {
  const [chosen, setChosen] = useState<JoinableFaction | null>(null);
  const rotos = chosen ? broken(chosen) : [];
  return (
    <div style={styles.overlay} role="dialog" aria-modal="true" aria-labelledby="faction-title">
      <div style={{ ...styles.panel, width: 'min(560px, 92vw)', maxHeight: '80vh', overflowY: 'auto' }}>
        <h2 id="faction-title" style={{ margin: '0 0 0.5rem', fontSize: '1.1rem' }}>
          Elegir bando
        </h2>
        <p style={{ margin: '0 0 0.75rem', color: 'var(--muted)', fontSize: '0.85rem' }}>
          Eres Neutral. Puedes unirte a la Orden o al Caos una sola vez, y es para siempre. La entrada cuenta dentro
          de 15 minutos; hasta entonces sigues siendo Neutral, y todos ven que vas a entrar.
        </p>
        <div style={{ display: 'flex', gap: '0.75rem', flexWrap: 'wrap' }}>
          {FACTION_CHOICES.map((f) => (
            <label
              key={f.code}
              style={{
                flex: '1 1 220px',
                border: `1px solid ${chosen === f.code ? 'var(--ok)' : 'var(--border)'}`,
                borderRadius: 6,
                padding: '0.6rem',
                cursor: 'pointer',
              }}
            >
              <input
                type="radio"
                name="faction"
                value={f.code}
                checked={chosen === f.code}
                onChange={() => setChosen(f.code)}
                style={{ marginRight: '0.4rem' }}
              />
              <strong>{f.name}</strong>
              <ul style={{ margin: '0.4rem 0 0', paddingLeft: '1.1rem', fontSize: '0.85rem' }}>
                {f.lines.map((l) => (
                  <li key={l.text} style={{ color: l.good ? 'var(--ok)' : 'var(--danger)' }}>
                    {l.text}
                  </li>
                ))}
              </ul>
            </label>
          ))}
        </div>
        {chosen && rotos.length > 0 && (
          <p style={{ margin: '0.75rem 0 0', color: 'var(--danger)', fontSize: '0.85rem' }}>
            Al entrar se romperán tus pactos y alianzas con {rotos.join(', ')}: empezará su preaviso de ruptura.
          </p>
        )}
        <div style={{ display: 'flex', gap: '0.5rem', marginTop: '0.75rem' }}>
          <button disabled={chosen === null} onClick={() => chosen && onJoin(chosen)}>
            {chosen ? `Unirme ${chosen === 'ORDER' ? 'a la Orden' : 'al Caos'} para siempre` : 'Elige un bando'}
          </button>
          <button className="secondary" onClick={onClose} autoFocus>
            Cancelar
          </button>
        </div>
      </div>
    </div>
  );
}

/**
 * El panel de mensajes (SPEC-MESSAGES §4.6): la bandeja o, con una conversación
 * abierta, su historial y el cuadro de texto. El texto se pinta como texto.
 */
function MessagesPanel({
  messages,
  playerId,
  nowMs,
  draft,
  onDraft,
  onSend,
  onOpen,
  onBack,
  onOlder,
  onBlock,
  onClose,
}: {
  messages: MessagesState;
  playerId: string | null;
  nowMs: number;
  draft: string;
  onDraft: (text: string) => void;
  onSend: () => void;
  onOpen: (c: { otherPlayerId: string; otherCityId: number | null; otherCityName: string | null }) => void;
  onBack: () => void;
  onOlder: (beforeId: number) => void;
  onBlock: (otherPlayerId: string, blocked: boolean) => void;
  onClose: () => void;
}) {
  const open = messages.open;
  const reason = sendBlockReason(draft);
  return (
    <section style={styles.log} aria-label="Mensajes">
      <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', marginBottom: '0.5rem' }}>
        {open ? (
          <>
            <button className="secondary" onClick={onBack}>← Bandeja</button>
            <strong>{otherName(open.otherCityName)}</strong>
            <button
              className="secondary"
              onClick={() => onBlock(open.otherPlayerId, !open.blockedByMe)}
              title={open.blockedByMe ? 'Volver a aceptar sus mensajes' : 'No recibirás sus mensajes ni podrás escribirle; no cambia tratados ni tratos'}
            >
              {open.blockedByMe ? 'Desbloquear' : 'Bloquear'}
            </button>
          </>
        ) : (
          <>
            <strong>Mensajes</strong>
            <span style={{ color: 'var(--muted)' }}>Los últimos 30 días</span>
          </>
        )}
        <button className="secondary" style={{ marginLeft: 'auto' }} onClick={onClose}>
          Cerrar
        </button>
      </div>

      {open ? (
        <>
          {open.nextBeforeId !== null && (
            <button className="secondary" style={{ marginBottom: '0.5rem' }} onClick={() => onOlder(open.nextBeforeId!)}>
              Ver anteriores
            </button>
          )}
          {!open.loaded ? (
            <p style={{ margin: 0, color: 'var(--muted)' }}>Cargando…</p>
          ) : open.messages.length === 0 ? (
            <p style={{ margin: 0, color: 'var(--muted)' }}>Todavía no os habéis escrito.</p>
          ) : (
            <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.35rem' }} aria-label="Conversación">
              {open.messages.map((m) => {
                const mine = m.fromPlayerId === playerId;
                return (
                  <li key={m.id} style={{ justifySelf: mine ? 'end' : 'start', maxWidth: '80%' }}>
                    <div
                      style={{
                        whiteSpace: 'pre-wrap',
                        overflowWrap: 'anywhere',
                        padding: '0.35rem 0.6rem',
                        borderRadius: '8px',
                        border: '1px solid var(--border)',
                        background: mine ? 'var(--border)' : 'transparent',
                      }}
                    >
                      {m.body}
                    </div>
                    <div style={{ color: 'var(--muted)', fontSize: '0.7rem', textAlign: mine ? 'right' : 'left' }}>
                      {messageTime(m.sentAtMs, nowMs)}
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
          {open.blockedByMe ? (
            <p style={{ color: 'var(--muted)', marginBottom: 0 }}>Lo tienes bloqueado: desbloquéalo para escribirle.</p>
          ) : (
            <form
              style={{ display: 'flex', gap: '0.5rem', alignItems: 'flex-end', marginTop: '0.6rem' }}
              onSubmit={(e) => { e.preventDefault(); onSend(); }}
            >
              <textarea
                value={draft}
                onChange={(e) => onDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); onSend(); }
                }}
                rows={2}
                style={{ flex: 1, resize: 'vertical' }}
                aria-label="Mensaje"
                placeholder="Escribe un mensaje (Intro envía, Mayús+Intro salta de línea)"
              />
              <span style={{ color: bodyLength(draft) > MESSAGE_MAX_CHARS ? 'var(--danger)' : 'var(--muted)', fontSize: '0.75rem' }}>
                {bodyLength(draft)}/{MESSAGE_MAX_CHARS}
              </span>
              <button type="submit" disabled={reason !== null} title={reason ?? 'Enviar'}>
                Enviar
              </button>
            </form>
          )}
          {messages.sendError?.otherPlayerId === open.otherPlayerId && (
            <p style={{ color: 'var(--danger)', margin: '0.4rem 0 0' }} role="alert">
              {sendErrorText(messages.sendError.code, messages.sendError.message)}
            </p>
          )}
        </>
      ) : !messages.inboxLoaded ? (
        <p style={{ margin: 0, color: 'var(--muted)' }}>Cargando…</p>
      ) : messages.conversations.length === 0 ? (
        <p style={{ margin: 0, color: 'var(--muted)' }}>
          No tienes conversaciones. Para escribir a alguien, haz clic en su ciudad y pulsa «Escribir».
        </p>
      ) : (
        <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.3rem' }} aria-label="Bandeja">
          {messages.conversations.map((c) => (
            <li key={c.otherPlayerId}>
              <button
                className="secondary"
                style={{ width: '100%', display: 'flex', gap: '0.6rem', alignItems: 'baseline', textAlign: 'left' }}
                onClick={() => onOpen(c)}
              >
                <strong style={{ whiteSpace: 'nowrap' }}>{otherName(c.otherCityName)}</strong>
                <span style={{ color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 }}>
                  {c.lastFromMe ? 'Tú: ' : ''}
                  {c.lastBody}
                </span>
                {c.blockedByMe && <span style={{ color: 'var(--muted)' }}>(bloqueado)</span>}
                {c.unread > 0 && <span style={{ color: 'var(--danger)', fontWeight: 600 }}>{c.unread}</span>}
                <span style={{ color: 'var(--muted)', fontSize: '0.72rem', whiteSpace: 'nowrap' }}>{messageTime(c.lastAtMs, nowMs)}</span>
              </button>
            </li>
          ))}
        </ul>
      )}

      {!open && messages.blocked.length > 0 && (
        <div style={{ marginTop: '0.75rem' }}>
          <span style={{ color: 'var(--muted)' }}>Bloqueados: </span>
          {messages.blocked.map((b) => (
            <span key={b.playerId} style={{ display: 'inline-flex', gap: '0.3rem', alignItems: 'center', marginRight: '0.75rem' }}>
              {otherName(b.cityName)}
              <button className="secondary" onClick={() => onBlock(b.playerId, false)}>
                Desbloquear
              </button>
            </span>
          ))}
        </div>
      )}
    </section>
  );
}

/** El botón que propone cada escalón de la escalera de tratados (SPEC-TRADE §4.1). */
const PROPOSE_LABEL: Record<ProposableType, string> = {
  TRADE: 'Proponer comercio',
  NON_AGGRESSION: 'Proponer pacto',
  ALLIANCE: 'Proponer alianza',
};

/** El texto de cada orden de tratado o de comercio en el HUD. */
const ACTION_LABEL: Record<TreatyAction | TradeAction, string> = {
  accept: 'Aceptar',
  reject: 'Rechazar',
  withdraw: 'Retirar',
  break: 'Romper',
};

/** Lo que el HUD dice de la unidad seleccionada: su vida, si trabaja o ataca, y cuánto lleva. */
function describeUnit(
  u: { status: string; carrying: number; carryingResource: string | null; hp: number; maxHp: number } | undefined,
): string {
  if (!u) return '';
  const vida = u.hp < u.maxHp ? `, vida ${u.hp}/${u.maxHp}` : '';
  if (u.status === 'WORKING') return ` (recolectando${vida})`;
  if (u.status === 'ATTACKING') return ` (atacando${vida})`;
  if (u.status === 'CLAIMING') return ` (reclamando el territorio${vida})`;
  if (u.carrying > 0) return ` (lleva ${u.carrying} de ${u.carryingResource === 'STONE' ? 'piedra' : 'madera'}${vida})`;
  return vida ? ` (${vida.slice(2)})` : '';
}

/** Qué se puede hacer con la unidad seleccionada. */
function helpFor(u: { unitType: string } | undefined): string {
  // RN-CIVUNIT-005: la unidad propia de una civilización hace lo mismo que el guerrero.
  if (u && unitAttacks(u.unitType)) {
    return 'clic en una unidad enemiga para atacarla (a una caravana, para capturarla), en una ciudad enemiga para saquearla, en el mapa para moverla; con botín, llévala junto a tu ciudad; «Reclamar territorio» planta su estandarte donde está';
  }
  if (u?.unitType === 'CARAVAN') {
    return 'las caravanas no admiten órdenes: van solas a su destino; tus guerreros cercanos la defienden si la atacan';
  }
  return 'clic en el mapa para moverla, en un bosque o una colina para recolectar, en una ciudad para guarnecerla';
}

/** La ciudad cuya zona urbana (su 3 × 3) contiene el tile, si la hay. Se consulta al hacer clic, no por render. */
function cityAt(tile: Tile): CityView | undefined {
  for (const c of useWorldStore.getState().cities.values()) {
    if (Math.abs(tile.x - c.centerX) <= 1 && Math.abs(tile.y - c.centerY) <= 1) return c;
  }
  return undefined;
}

/**
 * Lleva a ViewSync el principio de cada sesión: el welcome la abre y el primer
 * snapshot le dice dónde centró el servidor la vista (la ciudad propia, o el
 * centro del mundo si el jugador no la tiene).
 */
function trackSession(sync: ViewSync, message: v1.ServerMessage): void {
  if (message.type === 'session.welcome') {
    sync.onWelcome(message.payload.world);
    return;
  }
  if (message.type === 'world.snapshot' && sync.isAwaitingSnapshot()) {
    const { playerId, world } = useWorldStore.getState();
    const own = message.payload.cities.find((c) => c.ownerPlayerId === playerId);
    const center = own
      ? { x: own.centerX, y: own.centerY }
      : { x: Math.floor((world?.width ?? 0) / 2), y: Math.floor((world?.height ?? 0) / 2) };
    sync.onInitialSnapshot(center);
  }
}

/**
 * El panel del clan (SPEC-CLANS §4.8, SPEC-CLAN-GOVERNANCE §4.6). Sin clan:
 * fundar uno y las invitaciones recibidas. Con clan: los miembros con su rango,
 * el canal y, según el rango de quien mira, las invitaciones enviadas,
 * «Expulsar», «Nombrar oficial» y «Ceder el liderazgo», y sus alianzas
 * (SPEC-CLAN-ALLIANCES §4.6): las activas con su preaviso y, para el líder y los
 * oficiales, las ofertas. Encima, la vista pública pedida con «Ver clan», con
 * sus aliados y «Proponer alianza». El texto del canal se pinta como texto.
 */
function ClanPanel({
  clans,
  playerId,
  nowMs,
  form,
  onForm,
  onCreate,
  onRespond,
  onWithdraw,
  onExpel,
  onPromote,
  onTransfer,
  onLeave,
  draft,
  onDraft,
  onSend,
  onOlder,
  onCloseInfo,
  onProposeAlliance,
  onRespondAlliance,
  onCancelAlliance,
  onDeclareWar,
  onWarPeace,
  onSurrender,
  onGoTerritory,
  onClose,
}: {
  clans: ClansState;
  playerId: string | null;
  nowMs: number;
  form: { name: string; tag: string };
  onForm: (f: { name: string; tag: string }) => void;
  onCreate: () => void;
  onRespond: (clanId: number, accept: boolean) => void;
  onWithdraw: (playerId: string) => void;
  onExpel: (playerId: string) => void;
  onPromote: (playerId: string, officer: boolean) => void;
  onTransfer: (playerId: string) => void;
  onLeave: () => void;
  draft: string;
  onDraft: (text: string) => void;
  onSend: () => void;
  onOlder: (beforeId: number) => void;
  onCloseInfo: () => void;
  /** Las órdenes de las alianzas entre clanes (SPEC-CLAN-ALLIANCES §4.3). */
  onProposeAlliance: (clanId: number) => void;
  onRespondAlliance: (allianceId: number, accept: boolean) => void;
  onCancelAlliance: (allianceId: number) => void;
  /** Las órdenes de las guerras entre clanes (SPEC-CLAN-WARS §4.3). */
  onDeclareWar: (clanId: number) => void;
  onWarPeace: (warId: number, offer: boolean) => void;
  onSurrender: (warId: number) => void;
  /** Lleva la cámara al tile central de un territorio del clan (SPEC-CLAN-TERRITORIES §4.8). */
  onGoTerritory: (tile: { x: number; y: number }) => void;
  onClose: () => void;
}) {
  const clan = clans.clan;
  const role = myRole(clans, playerId);
  const leader = role === 'LEADER';
  const manager = managesInvitations(role);
  const invitations = liveInvitations(clans, nowMs);
  const formProblem = nameProblem(form.name) ?? tagProblem(form.tag);
  const reason = sendBlockReason(draft);
  const [confirmLeave, setConfirmLeave] = useState(false);
  const [confirmTransfer, setConfirmTransfer] = useState<string | null>(null);
  const [confirmBreak, setConfirmBreak] = useState<number | null>(null);
  const allies = activeAlliances(clans);
  const offers = allianceOffers(clans);
  const proposeProblem = clans.info ? allianceProblem(clans, playerId, clans.info.clanId) : null;
  const wars = openWars(clans);
  const declareProblem = clans.info ? warProblem(clans, playerId, clans.info.clanId) : null;
  const [confirmDeclare, setConfirmDeclare] = useState(false);
  const [confirmSurrender, setConfirmSurrender] = useState<number | null>(null);
  return (
    <section style={styles.log} aria-label="Clan">
      <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', marginBottom: '0.5rem' }}>
        <strong>{clan ? `${clan.name} [${clan.tag}]` : 'Clan'}</strong>
        <button className="secondary" style={{ marginLeft: 'auto' }} onClick={onClose}>
          Cerrar
        </button>
      </div>

      {clans.info && (
        <div style={{ border: '1px solid var(--border)', borderRadius: '6px', padding: '0.5rem', marginBottom: '0.6rem' }}>
          <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
            <strong>{clans.info.name} [{clans.info.tag}]</strong>
            <button className="secondary" style={{ marginLeft: 'auto' }} onClick={onCloseInfo}>×</button>
          </div>
          <ul style={{ margin: '0.3rem 0 0', paddingLeft: '1.1rem' }} aria-label="Miembros de otro clan">
            {clans.info.members.map((m) => (
              <li key={m.playerId}>
                {m.cityName ?? 'Un jugador sin ciudad'} — {factionLabel(m.faction)}
                {m.role === 'MEMBER' ? '' : ` — ${roleLabel(m.role)}`}
              </li>
            ))}
          </ul>
          {clans.info.territories.length > 0 && (
            <p style={{ margin: '0.3rem 0 0', color: 'var(--muted)' }}>
              Territorios: {clans.info.territories.map((t) => t.name).join(', ')}
            </p>
          )}
          {clans.info.alliances.length > 0 && (
            <p style={{ margin: '0.3rem 0 0', color: 'var(--muted)' }}>
              Aliados: {clans.info.alliances.map((a) => `[${a.tag}]${a.breaksAtMs !== null ? ' (en preaviso)' : ''}`).join(', ')}
            </p>
          )}
          {clan !== null && clans.info.clanId !== clan.id && (leader || proposeProblem === null) && (
            <button
              style={{ marginTop: '0.4rem' }}
              disabled={proposeProblem !== null}
              title={proposeProblem ?? 'Si su líder la acepta, los miembros de los dos clanes estaréis en paz'}
              onClick={() => onProposeAlliance(clans.info!.clanId)}
            >
              Proponer alianza
            </button>
          )}
          {clans.info.wars.length > 0 && (
            <p style={{ margin: '0.3rem 0 0', color: 'var(--muted)' }}>
              En guerra con:{' '}
              {clans.info.wars
                .map((w) => `${warScore(clans.info!.tag, w)}${w.status === 'DECLARED' ? ' (en preparación)' : ''}`)
                .join(', ')}
            </p>
          )}
          <p style={{ margin: '0.3rem 0 0', color: 'var(--muted)' }}>Guerras: {warRecordText(clans.info.warRecord)}</p>
          {clan !== null && clans.info.clanId !== clan.id && (leader || declareProblem === null) && (
            confirmDeclare ? (
              <p style={{ margin: '0.4rem 0 0' }}>
                {declareWarWarning(clans.info.tag)}{' '}
                <button onClick={() => { setConfirmDeclare(false); onDeclareWar(clans.info!.clanId); }}>Confirmar</button>{' '}
                <button className="secondary" onClick={() => setConfirmDeclare(false)}>Cancelar</button>
              </p>
            ) : (
              <button
                style={{ marginTop: '0.4rem', marginLeft: '0.4rem' }}
                className="secondary"
                disabled={declareProblem !== null}
                title={declareProblem ?? 'Una guerra con marcador: los pactos entre vuestros miembros terminan'}
                onClick={() => setConfirmDeclare(true)}
              >
                Declarar la guerra
              </button>
            )
          )}
        </div>
      )}

      {!clans.loaded ? (
        <p style={{ margin: 0, color: 'var(--muted)' }}>Cargando…</p>
      ) : clan === null ? (
        <>
          {invitations.length > 0 && (
            <ul style={{ listStyle: 'none', margin: '0 0 0.6rem', padding: 0, display: 'grid', gap: '0.35rem' }} aria-label="Invitaciones">
              {invitations.map((i) => (
                <li key={i.clanId} style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
                  <span>Te invitan a {i.name} [{i.tag}]</span>
                  <button onClick={() => onRespond(i.clanId, true)} title="Entras en el clan y estás en paz con sus miembros">
                    Aceptar
                  </button>
                  <button className="secondary" onClick={() => onRespond(i.clanId, false)}>Rechazar</button>
                </li>
              ))}
            </ul>
          )}
          <form
            style={{ display: 'flex', gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}
            onSubmit={(e) => { e.preventDefault(); if (formProblem === null) onCreate(); }}
            aria-label="Fundar un clan"
          >
            <span>Fundar un clan:</span>
            <input
              value={form.name}
              onChange={(e) => onForm({ ...form, name: e.target.value })}
              placeholder="Nombre"
              aria-label="Nombre del clan"
              maxLength={24}
            />
            <input
              value={form.tag}
              onChange={(e) => onForm({ ...form, tag: e.target.value.toUpperCase() })}
              placeholder="Sigla"
              aria-label="Sigla del clan"
              maxLength={4}
              style={{ width: '5rem' }}
            />
            <button type="submit" disabled={formProblem !== null} title={formProblem ?? 'Serás su líder. El nombre y la sigla no cambian'}>
              Fundar
            </button>
          </form>
        </>
      ) : (
        <>
          <ul style={{ margin: '0 0 0.5rem', paddingLeft: '1.1rem' }} aria-label="Miembros">
            {clan.members.map((m) => (
              <li key={m.playerId}>
                {m.cityName ?? 'Un jugador sin ciudad'} — {factionLabel(m.faction)} — {roleLabel(m.role)}
                {m.playerId !== playerId && canExpel(role, m.role) && (
                  <button className="secondary" style={{ marginLeft: '0.5rem' }} onClick={() => onExpel(m.playerId)}
                    title="Sale del clan; seguís en paz unos minutos">
                    Expulsar
                  </button>
                )}
                {leader && m.playerId !== playerId && (
                  <>
                    <button className="secondary" style={{ marginLeft: '0.5rem' }}
                      onClick={() => onPromote(m.playerId, m.role !== 'OFFICER')}
                      title={m.role === 'OFFICER' ? 'Vuelve a ser miembro' : 'Podrá invitar y expulsar a los miembros'}>
                      {m.role === 'OFFICER' ? 'Quitar oficial' : 'Nombrar oficial'}
                    </button>
                    {confirmTransfer === m.playerId ? (
                      <span style={{ marginLeft: '0.5rem' }}>
                        {transferWarning(m.cityName, clan.tag)}{' '}
                        <button onClick={() => { setConfirmTransfer(null); onTransfer(m.playerId); }}>Confirmar</button>{' '}
                        <button className="secondary" onClick={() => setConfirmTransfer(null)}>Cancelar</button>
                      </span>
                    ) : (
                      <button className="secondary" style={{ marginLeft: '0.5rem' }} onClick={() => setConfirmTransfer(m.playerId)}>
                        Ceder el liderazgo
                      </button>
                    )}
                  </>
                )}
              </li>
            ))}
          </ul>
          <p style={{ margin: '0 0 0.5rem', color: 'var(--muted)', fontSize: '0.75rem' }}>
            Si el líder pasa {LEADER_INACTIVE_DAYS} días sin jugar, el liderazgo pasa al oficial más antiguo que siga
            jugando.
          </p>
          <div style={{ margin: '0 0 0.5rem', display: 'flex', gap: '0.5rem', flexWrap: 'wrap', alignItems: 'center' }}
            aria-label="Territorios del clan">
            <span style={{ color: 'var(--muted)' }}>Territorios del clan:</span>
            {clan.territories.length === 0 ? (
              <span style={{ color: 'var(--muted)' }}>ninguno todavía; «Reclamar para el clan» con un guerrero</span>
            ) : (
              clan.territories.map((t) => (
                <span key={t.id} style={{ display: 'inline-flex', gap: '0.3rem', alignItems: 'center' }}>
                  {t.name}
                  <button className="secondary" onClick={() => onGoTerritory({ x: t.x, y: t.y })} title="Lleva la cámara al territorio">Ir</button>
                </span>
              ))
            )}
          </div>
          <div style={{ margin: '0 0 0.5rem', display: 'grid', gap: '0.3rem' }} aria-label="Alianzas">
            <span style={{ color: 'var(--muted)' }}>
              Alianzas: [{clan.tag}] tiene {allies.length} de {MAX_CLAN_ALLIES}
              {allies.length === 0 ? '; «Proponer alianza» desde «Ver clan» de otro clan' : ''}
            </span>
            {allies.map((a) => (
              <span key={a.id} style={{ display: 'inline-flex', gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
                Aliados de {a.name} [{a.tag}]
                {allianceCountdown(a, nowMs) && <span style={{ color: 'var(--danger, #c0392b)' }}>— {allianceCountdown(a, nowMs)}</span>}
                {leader && a.breaksAtMs === null && (confirmBreak === a.id ? (
                  <span>
                    {breakAllianceWarning(a.tag)}{' '}
                    <button onClick={() => { setConfirmBreak(null); onCancelAlliance(a.id); }}>Confirmar</button>{' '}
                    <button className="secondary" onClick={() => setConfirmBreak(null)}>Cancelar</button>
                  </span>
                ) : (
                  <button className="secondary" onClick={() => setConfirmBreak(a.id)}>Romper</button>
                ))}
              </span>
            ))}
            {manager && offers.received.map((a) => (
              <span key={a.id} style={{ display: 'inline-flex', gap: '0.4rem', alignItems: 'center' }}>
                {a.name} [{a.tag}] propone una alianza
                {leader && (
                  <>
                    <button onClick={() => onRespondAlliance(a.id, true)} title="Los miembros de los dos clanes estaréis en paz">Aceptar</button>
                    <button className="secondary" onClick={() => onRespondAlliance(a.id, false)}>Rechazar</button>
                  </>
                )}
              </span>
            ))}
            {manager && offers.sent.map((a) => (
              <span key={a.id} style={{ display: 'inline-flex', gap: '0.4rem', alignItems: 'center' }}>
                Ofreciste una alianza a {a.name} [{a.tag}]
                {leader && <button className="secondary" onClick={() => onCancelAlliance(a.id)}>Retirar</button>}
              </span>
            ))}
          </div>
          <div style={{ margin: '0 0 0.5rem', display: 'grid', gap: '0.3rem' }} aria-label="Guerras">
            <span style={{ color: 'var(--muted)' }}>
              Guerras: {warRecordText(clan.warRecord)}
              {wars.length === 0 ? '; «Declarar la guerra» desde «Ver clan» de otro clan' : ''}
            </span>
            {wars.map((w) => (
              <span key={w.id} style={{ display: 'inline-flex', gap: '0.4rem', alignItems: 'center', flexWrap: 'wrap' }}>
                {w.declaredByUs ? 'Declarasteis la guerra a' : 'Os declaró la guerra'} {w.name} [{w.tag}]: {warScore(clan.tag, w)}
                <span style={{ color: 'var(--danger, #c0392b)' }}>— {warCountdown(w, nowMs)}</span>
                {manager && w.peaceOfferedByThem && <span>— [{w.tag}] ofrece la paz</span>}
                {manager && w.peaceOfferedByUs && <span>— Has ofrecido la paz</span>}
                {leader && (
                  <>
                    {w.peaceOfferedByThem ? (
                      <>
                        <button onClick={() => onWarPeace(w.id, true)} title="La guerra termina en empate">Aceptar la paz</button>
                        <button className="secondary" onClick={() => onWarPeace(w.id, false)}>Rechazar</button>
                      </>
                    ) : w.peaceOfferedByUs ? (
                      <button className="secondary" onClick={() => onWarPeace(w.id, false)}>Retirar la oferta</button>
                    ) : (
                      <button className="secondary" onClick={() => onWarPeace(w.id, true)} title="Si su líder la acepta, la guerra termina en empate">
                        Ofrecer la paz
                      </button>
                    )}
                    {confirmSurrender === w.id ? (
                      <span>
                        {surrenderWarning(w.tag)}{' '}
                        <button onClick={() => { setConfirmSurrender(null); onSurrender(w.id); }}>Confirmar</button>{' '}
                        <button className="secondary" onClick={() => setConfirmSurrender(null)}>Cancelar</button>
                      </span>
                    ) : (
                      <button className="secondary" onClick={() => setConfirmSurrender(w.id)}>Rendirse</button>
                    )}
                  </>
                )}
              </span>
            ))}
          </div>
          {manager && clan.sentInvitations.filter((i) => i.expiresAtMs > nowMs).length > 0 && (
            <p style={{ margin: '0 0 0.5rem', display: 'flex', gap: '0.5rem', flexWrap: 'wrap', alignItems: 'center' }}>
              <span style={{ color: 'var(--muted)' }}>Invitados:</span>
              {clan.sentInvitations.filter((i) => i.expiresAtMs > nowMs).map((i) => (
                <span key={i.playerId} style={{ display: 'inline-flex', gap: '0.3rem', alignItems: 'center' }}>
                  {i.cityName ?? 'un jugador'}
                  <button className="secondary" onClick={() => onWithdraw(i.playerId)}>Retirar</button>
                </span>
              ))}
            </p>
          )}

          {clans.chat?.nextBeforeId != null && (
            <button className="secondary" style={{ marginBottom: '0.5rem' }} onClick={() => onOlder(clans.chat!.nextBeforeId!)}>
              Ver anteriores
            </button>
          )}
          {!clans.chat?.loaded ? (
            <p style={{ margin: 0, color: 'var(--muted)' }}>Cargando el canal…</p>
          ) : clans.chat.messages.length === 0 ? (
            <p style={{ margin: 0, color: 'var(--muted)' }}>El canal está vacío: lo que escribas lo leen todos los miembros.</p>
          ) : (
            <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.35rem' }} aria-label="Canal del clan">
              {clans.chat.messages.map((m) => {
                const mine = m.fromPlayerId === playerId;
                return (
                  <li key={m.id} style={{ justifySelf: mine ? 'end' : 'start', maxWidth: '80%' }}>
                    <div style={{ color: 'var(--muted)', fontSize: '0.7rem', textAlign: mine ? 'right' : 'left' }}>
                      {mine ? 'Tú' : (m.fromCityName ?? 'Un jugador sin ciudad')} · {messageTime(m.sentAtMs, nowMs)}
                    </div>
                    <div
                      style={{
                        whiteSpace: 'pre-wrap',
                        overflowWrap: 'anywhere',
                        padding: '0.35rem 0.6rem',
                        borderRadius: '8px',
                        border: '1px solid var(--border)',
                        background: mine ? 'var(--border)' : 'transparent',
                      }}
                    >
                      {m.body}
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
          <form
            style={{ display: 'flex', gap: '0.5rem', alignItems: 'flex-end', marginTop: '0.6rem' }}
            onSubmit={(e) => { e.preventDefault(); onSend(); }}
          >
            <textarea
              value={draft}
              onChange={(e) => onDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); onSend(); }
              }}
              rows={2}
              style={{ flex: 1, resize: 'vertical' }}
              aria-label="Mensaje al clan"
              placeholder="Escribe al clan (Intro envía, Mayús+Intro salta de línea)"
            />
            <span style={{ color: bodyLength(draft) > MESSAGE_MAX_CHARS ? 'var(--danger)' : 'var(--muted)', fontSize: '0.75rem' }}>
              {bodyLength(draft)}/{MESSAGE_MAX_CHARS}
            </span>
            <button type="submit" disabled={reason !== null} title={reason ?? 'Enviar al clan'}>
              Enviar
            </button>
          </form>
          {clans.sendError && (
            <p style={{ color: 'var(--danger)', margin: '0.4rem 0 0' }} role="alert">
              {sendErrorText(clans.sendError.code, clans.sendError.message)}
            </p>
          )}
          <p style={{ margin: '0.6rem 0 0', display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
            {confirmLeave ? (
              <>
                <span>Dejas el canal y la sigla ya; la paz con tus compañeros dura unos minutos más.</span>
                <button onClick={() => { setConfirmLeave(false); onLeave(); }}>Salir del clan</button>
                <button className="secondary" onClick={() => setConfirmLeave(false)}>Cancelar</button>
              </>
            ) : (
              <button className="secondary" onClick={() => setConfirmLeave(true)}>Salir del clan</button>
            )}
          </p>
        </>
      )}
    </section>
  );
}

const styles = {
  civilizations: {
    border: '1px solid var(--border)',
    borderRadius: '6px',
    padding: '0.6rem 0.75rem',
    margin: '0 0 1.1rem',
    display: 'grid',
    gridTemplateColumns: '1fr 1fr',
    gap: '0.5rem',
  },
  civilization: {
    display: 'flex',
    gap: '0.4rem',
    alignItems: 'flex-start',
    fontSize: '0.75rem',
    cursor: 'pointer',
  },
  center: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    height: '100vh',
  },
  panel: {
    background: 'var(--panel)',
    border: '1px solid var(--border)',
    borderRadius: '10px',
    padding: '1.75rem',
    width: 'min(360px, 92vw)',
  },
  overlay: {
    position: 'fixed',
    inset: 0,
    background: 'rgba(0, 0, 0, 0.55)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    zIndex: 10,
  },
  log: {
    padding: '0.75rem 1rem',
    background: 'var(--panel)',
    borderBottom: '1px solid var(--border)',
    maxHeight: '40vh',
    overflowY: 'auto',
    fontSize: '0.82rem',
  },
  hud: {
    display: 'flex',
    alignItems: 'center',
    gap: '1.25rem',
    padding: '0.6rem 1rem',
    background: 'var(--panel)',
    borderBottom: '1px solid var(--border)',
    fontSize: '0.82rem',
  },
  // Por encima del lienzo y por debajo de «Mientras no estabas» (zIndex 10).
  notices: {
    position: 'absolute',
    top: '0.75rem',
    right: '0.75rem',
    zIndex: 5,
    listStyle: 'none',
    margin: 0,
    padding: 0,
    display: 'grid',
    gap: '0.4rem',
    width: 'min(360px, calc(100% - 1.5rem))',
    pointerEvents: 'none',
  },
  notice: {
    display: 'flex',
    alignItems: 'center',
    gap: '0.6rem',
    padding: '0.5rem 0.75rem',
    background: 'var(--panel)',
    border: '1px solid var(--border)',
    borderRadius: '8px',
    fontSize: '0.82rem',
    pointerEvents: 'auto',
  },
} as const;
