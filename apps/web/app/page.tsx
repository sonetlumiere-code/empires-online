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

import { terrainAt, useWorldStore, type CityView } from '../src/state/world';
import { createHudSelector } from '../src/state/selectors';
import {
  advanceBlockReason,
  craftBlockReason,
  CRAFT_AMOUNT,
  CRAFT_COST,
  IRON_AGE_COST,
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
  unitTypeLabel,
} from '../src/state/economy';
import { ViewSync } from '../src/state/viewSync';
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
    selectedUnit && selectedUnit.unitType === 'WARRIOR' && selectedUnit.playerId === s.playerId
      ? territoryAt(s.territories.values(), selectedUnit.x, selectedUnit.y)
      : undefined,
  );
  const claimEndsAtMs = claimTerritory?.claim?.endsAtMs;
  useEffect(() => {
    if (claimEndsAtMs === undefined) return;
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [claimEndsAtMs]);
  useEffect(() => {
    if (!countdown) return;
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [countdown]);
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
      else if (click.kind === 'attack' && selectedUnitId !== null) clientRef.current?.attackUnit(selectedUnitId, unitId);
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
  // Los edificios de la ciudad propia, para los motivos del taller y de la elaboración.
  const ownBuildings = ownCity ? buildingsOf(useWorldStore.getState().buildings, ownCity.id) : [];
  const ownGood = ownCity ? goodOf(ownCity.civilization) : null;

  // Por qué no se puede reclamar con el guerrero seleccionado (SPEC-TERRITORY-CONQUEST §5).
  const claimReason = claimTerritory
    ? claimBlockReason({
        unit: selectedUnit,
        playerId,
        territory: claimTerritory,
        cities: useWorldStore.getState().cities.values(),
        treaties,
        nowMs: serverTimeMs(),
      })
    : null;

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
              — {eraLabel(ownCity.era)}
            </span>
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
                ¡Están derribando tus casas!
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
                  : `Avanza a la Edad de Hierro: ${IRON_AGE_COST.WOOD} de madera, ${IRON_AGE_COST.STONE} de piedra y ${IRON_AGE_COST.FOREIGN_GOODS} bienes de otras civilizaciones (se cobran en el orden vino, seda, especias, pieles; el tuyo nunca), 180 s sin entrenar. El límite sube de 50 a 100`;
              return (
                <button
                  className="secondary"
                  onClick={() => clientRef.current?.advanceEra(ownCity.id)}
                  disabled={reason !== null}
                  title={reason ?? what}
                >
                  {eraLabel(next)}
                  {next === 'IRON_AGE' && ` (${foreign}/${IRON_AGE_COST.FOREIGN_GOODS} bienes)`}
                </button>
              );
            })()}
            {ownCity.advance && (
              <span style={{ color: 'var(--muted)' }}>
                Avanzando a la {eraLabel(ownCity.advance.era)}:{' '}
                {secondsUntil(ownCity.advance.completesAtMs, serverTimeMs())} s
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
              {claimLabel(claimTerritory, playerId, serverTimeMs()) ??
                `${claimTerritory.name}: ${territoryOwnerLabel(claimTerritory, playerId, useWorldStore.getState().cities.values())}`}
            </span>
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
          </>
        )}
        <span style={{ marginLeft: 'auto', color: 'var(--muted)' }}>
          {selectedUnitId !== null
            ? `${selectedUnit ? unitTypeLabel(selectedUnit.unitType) : 'Unidad'} ${selectedUnitId}${describeUnit(selectedUnit)} — ${helpFor(selectedUnit)}`
            : `${ownUnitCount} unidades — clic en una para seleccionarla · flechas, WASD o arrastre con el botón derecho para mover la cámara · H vuelve a la ciudad`}
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
                {diplomacyTarget.name} ({civilizationLabel(diplomacyTarget.civilization)}, {eraLabel(diplomacyTarget.era)}):
              </span>
              {proposableTypes(treaties, diplomacyTarget.ownerPlayerId).map((tipo) => (
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
          {onWrite && (
            <button
              className="secondary"
              style={{ fontSize: '0.72rem', padding: '0.1rem 0.4rem' }}
              onClick={() => onWrite(e.otherPlayerId, e.otherCityId, e.otherCityName)}
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
  if (u?.unitType === 'WARRIOR') {
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
