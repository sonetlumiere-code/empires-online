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
import { login, register, ticketProvider, AuthError } from '../src/net/auth';
import type { v1 } from '@empires-online/protocol';

import { useWorldStore } from '../src/state/world';
import { ViewSync } from '../src/state/viewSync';
import type { Point, Tile } from '../src/render/iso';

const WS_URL = process.env.NEXT_PUBLIC_GAME_SERVER_WS_URL ?? 'ws://localhost:8080/ws';

/** Lo que ve el jugador cuando su sesión web ya no sirve y vuelve al formulario. */
const SESSION_ENDED_MESSAGE = 'Tu sesión ha caducado o ya no es válida. Vuelve a entrar.';

export default function Page() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [authError, setAuthError] = useState<string | null>(null);
  const [connection, setConnection] = useState<ConnectionState>('idle');

  const clientRef = useRef<GameClient | null>(null);
  // Cuándo decirle al servidor dónde mira la cámara (frontend.md §7.4.1).
  const viewSyncRef = useRef(new ViewSync());

  const applyServerMessage = useWorldStore((s) => s.applyServerMessage);
  const selectUnit = useWorldStore((s) => s.selectUnit);
  const selectedUnitId = useWorldStore((s) => s.selectedUnitId);
  const playerId = useWorldStore((s) => s.playerId);
  const units = useWorldStore((s) => s.units);
  const cities = useWorldStore((s) => s.cities);
  // Los mapas son de referencia estable: lo que cambia por delta es `changes`.
  // Provisional hasta que el HUD se suscriba a un selector por valor.
  useWorldStore((s) => s.changes);
  const lastError = useWorldStore((s) => s.lastError);

  useEffect(() => () => clientRef.current?.disconnect(), []);

  const startSession = useCallback(
    async (mode: 'login' | 'register') => {
      setBusy(true);
      setAuthError(null);
      try {
        const creds = { username, password };
        // El alta y el inicio de sesión devuelven un ticket y dejan una cookie de
        // sesión; cada reconexión pide un ticket nuevo con ella, porque los
        // tickets son de un solo uso. La contraseña no se guarda.
        const { ticket } = await (mode === 'register' ? register(creds) : login(creds));
        setPassword('');

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
      } catch (err) {
        setAuthError(err instanceof AuthError ? `${err.code}: ${err.message}` : String(err));
      } finally {
        setBusy(false);
      }
    },
    [username, password, applyServerMessage],
  );

  const serverTimeMs = useCallback(() => clientRef.current?.serverTimeMs() ?? Date.now(), []);

  // Cada frame: si la cámara cruzó de chunk, el servidor tiene que saberlo.
  const handleCameraCenter = useCallback((center: Point) => {
    const tile = viewSyncRef.current.update(center, performance.now());
    if (tile) clientRef.current?.setView(tile);
  }, []);

  const handleTileClick = useCallback(
    (tile: Tile) => {
      const client = clientRef.current;
      if (!client || selectedUnitId === null) return;
      // Un clic sobre la zona urbana de una ciudad (su 3 × 3) es la orden de
      // entrar en ella, como un clic sobre un edificio en la referencia. El
      // cliente sólo elige el objetivo; si se puede lo decide el servidor.
      const ciudad = [...useWorldStore.getState().cities.values()].find(
        (c) => Math.abs(tile.x - c.centerX) <= 1 && Math.abs(tile.y - c.centerY) <= 1,
      );
      if (ciudad) {
        client.garrisonUnit(selectedUnitId, ciudad.id);
        selectUnit(null);
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
      const unit = useWorldStore.getState().units.get(unitId);
      // Sólo se seleccionan unidades propias: el servidor rechazaría cualquier
      // orden sobre una ajena, así que ni se ofrece.
      if (unit && unit.playerId === playerId) selectUnit(unitId);
    },
    [playerId, selectUnit],
  );

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

          <div style={{ display: 'flex', gap: '0.6rem' }}>
            <button onClick={() => void startSession('login')} disabled={busy}>
              Entrar
            </button>
            <button className="secondary" onClick={() => void startSession('register')} disabled={busy}>
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

  const ownUnits = [...units.values()].filter((u) => u.playerId === playerId);
  const garrisoned = ownUnits.filter((u) => u.garrisonCityId !== null);
  const ownCity = [...cities.values()].find((c) => c.ownerPlayerId === playerId);

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
              {ownCity.name} — {ownCity.era}
            </span>
            <span style={{ color: 'var(--muted)' }}>
              Población {ownCity.population}/{ownCity.populationLimit}
            </span>
            <span style={{ color: 'var(--muted)' }}>Presencia: {ownCity.presenceState}</span>
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
        <span style={{ marginLeft: 'auto', color: 'var(--muted)' }}>
          {selectedUnitId !== null
            ? `Unidad ${selectedUnitId} seleccionada — clic en el mapa para moverla, en una ciudad para guarnecerla`
            : `${ownUnits.length} unidades — clic en una para seleccionarla · flechas, WASD o arrastre con el botón derecho para mover la cámara · H vuelve a la ciudad`}
        </span>
      </header>

      <div style={{ flex: 1, minHeight: 0 }}>
        <GameCanvas
          serverTimeMs={serverTimeMs}
          onTileClick={handleTileClick}
          onUnitClick={handleUnitClick}
          onCameraCenter={handleCameraCenter}
        />
      </div>

      {lastError && (
        <footer style={{ ...styles.hud, borderTop: '1px solid var(--border)', borderBottom: 'none' }}>
          <span style={{ color: 'var(--danger)' }}>
            {lastError.code}: {lastError.message}
          </span>
        </footer>
      )}
    </main>
  );
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
  hud: {
    display: 'flex',
    alignItems: 'center',
    gap: '1.25rem',
    padding: '0.6rem 1rem',
    background: 'var(--panel)',
    borderBottom: '1px solid var(--border)',
    fontSize: '0.82rem',
  },
} as const;
