'use client';

/**
 * Puente entre React y PixiJS.
 *
 * React posee el ciclo de vida del componente; PixiJS posee la escena. Este
 * componente los conecta y nada más: no toma decisiones de juego.
 *
 * Cuidado con el montaje: `Application.init()` es asíncrono y React en Strict
 * Mode ejecuta el efecto DOS veces (montar, limpiar, montar). Si la segunda
 * `Application` se crea antes de que la primera termine de destruirse, PixiJS
 * reutiliza shaders de un contexto WebGL ya muerto y la escena queda en negro.
 * Por eso el montaje y la destrucción se serializan explícitamente.
 */
import { useEffect, useRef, useState } from 'react';

import {
  IsoScene,
  type SceneCity,
  type SceneData,
  type SceneTerritory,
  type SceneUnit,
} from '../render/scene';
import { cameraAt, type Tile } from '../render/iso';
import { terrainAt, useWorldStore } from '../state/world';

export interface GameCanvasProps {
  /** Instante actual del SERVIDOR (reloj local corregido por el desfase). */
  serverTimeMs: () => number;
  onTileClick: (tile: Tile) => void;
  onUnitClick: (unitId: number) => void;
}

/** Proyecta el estado del store a lo que la escena necesita dibujar. */
function toSceneData(
  state: ReturnType<typeof useWorldStore.getState>,
  canvas: HTMLCanvasElement,
  serverTimeMs: () => number,
): SceneData | null {
  if (!state.world) return null;

  const ownId = state.playerId;

  const units: SceneUnit[] = [];
  for (const u of state.units.values()) {
    units.push({
      id: u.id,
      x: u.x,
      y: u.y,
      movement: u.movement,
      isOwn: u.playerId === ownId,
      isSelected: state.selectedUnitId === u.id,
    });
  }

  const cities: SceneCity[] = [];
  let focus = { x: Math.floor(state.world.width / 2), y: Math.floor(state.world.height / 2) };
  for (const c of state.cities.values()) {
    const isOwn = c.ownerPlayerId === ownId;
    if (isOwn) focus = { x: c.centerX, y: c.centerY };
    cities.push({
      id: c.id,
      centerX: c.centerX,
      centerY: c.centerY,
      name: c.name,
      presenceState: c.presenceState,
      isOwn,
    });
  }

  // `isOwn` se decide comparando el `ownerId` que envió el servidor con el
  // jugador de esta sesión. Es lo único que el cliente calcula sobre territorio,
  // y no es ownership: es a quién pintar de verde.
  const territories: SceneTerritory[] = [];
  for (const t of state.territories.values()) {
    territories.push({
      id: t.id,
      minX: t.minX,
      minY: t.minY,
      maxX: t.maxX,
      maxY: t.maxY,
      ownerType: t.ownerType,
      isOwn: t.ownerType === 'PLAYER' && t.ownerId === ownId,
    });
  }

  const chunkSize = state.world.chunkSize;
  const terrainSnapshot = state.terrain;

  return {
    units,
    cities,
    territories,
    terrainAt: (x, y) => terrainAt(terrainSnapshot, chunkSize, x, y),
    // Se pasa la FUNCIÓN, no el valor: la escena necesita el instante de cada
    // frame para interpolar. Congelarlo aquí dejaría las unidades quietas entre
    // actualizaciones del store.
    serverTimeMs,
    camera: cameraAt(focus, canvas.clientWidth, canvas.clientHeight, 1),
  };
}

export function GameCanvas({ serverTimeMs, onTileClick, onUnitClick }: GameCanvasProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const sceneRef = useRef<IsoScene | null>(null);
  const [renderError, setRenderError] = useState<string | null>(null);

  // Los callbacks viven en un ref para que la escena no tenga que recrearse cada
  // vez que el padre se vuelve a renderizar.
  const handlers = useRef({ onTileClick, onUnitClick });
  handlers.current = { onTileClick, onUnitClick };

  const timeRef = useRef(serverTimeMs);
  timeRef.current = serverTimeMs;

  // Cola de destrucción: el siguiente montaje espera a que termine el anterior.
  const teardownRef = useRef<Promise<void>>(Promise.resolve());

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    let disposed = false;
    let scene: IsoScene | null = null;

    const started = teardownRef.current
      .then(async () => {
        if (disposed) return;

        const s = new IsoScene({
          onTileClick: (tile) => handlers.current.onTileClick(tile),
          onUnitClick: (unitId) => handlers.current.onUnitClick(unitId),
        });
        await s.mount(canvas, canvas.clientWidth, canvas.clientHeight);

        // Pudo desmontarse mientras se inicializaba: entonces se destruye sin
        // llegar a usarse.
        if (disposed) {
          s.destroy();
          return;
        }

        scene = s;
        sceneRef.current = s;
        setRenderError(null);

        const data = toSceneData(useWorldStore.getState(), canvas, () => timeRef.current());
        if (data) s.update(data);
      })
      .catch((err: unknown) => {
        // Un fallo de WebGL no puede quedarse en un lienzo negro sin explicación.
        const message = err instanceof Error ? err.message : String(err);
        console.error('no se pudo inicializar el renderizador', err);
        setRenderError(message);
      });

    const onResize = () => sceneRef.current?.resize(canvas.clientWidth, canvas.clientHeight);
    window.addEventListener('resize', onResize);

    return () => {
      disposed = true;
      sceneRef.current = null;
      window.removeEventListener('resize', onResize);
      // La destrucción se encadena al montaje: nunca se destruye algo a medio
      // inicializar, ni se inicializa lo siguiente antes de terminar esto.
      teardownRef.current = started.then(() => {
        scene?.destroy();
        scene = null;
      });
    };
  }, []);

  // El store empuja el estado a la escena; la escena decide cuándo dibujar.
  useEffect(() => {
    const push = (state: ReturnType<typeof useWorldStore.getState>) => {
      const scene = sceneRef.current;
      const canvas = canvasRef.current;
      if (!scene || !canvas) return;

      const data = toSceneData(state, canvas, () => timeRef.current());
      if (data) scene.update(data);
    };

    push(useWorldStore.getState());
    return useWorldStore.subscribe(push);
  }, []);

  return (
    <div style={{ position: 'relative', width: '100%', height: '100%' }}>
      <canvas
        ref={canvasRef}
        style={{ width: '100%', height: '100%', display: 'block', cursor: 'crosshair' }}
      />
      {renderError && (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: '2rem',
            textAlign: 'center',
            color: 'var(--danger)',
            background: 'rgba(16,16,20,0.9)',
          }}
        >
          <div>
            <p style={{ fontWeight: 600, marginBottom: '0.5rem' }}>
              No se pudo iniciar el renderizador del mundo
            </p>
            <p style={{ color: 'var(--muted)', fontSize: '0.85rem' }}>
              El mundo necesita WebGL. La sesión sigue conectada y el estado del juego es
              correcto; sólo falta el dibujado.
            </p>
            <p style={{ color: 'var(--muted)', fontSize: '0.75rem', marginTop: '0.75rem' }}>
              {renderError}
            </p>
          </div>
        </div>
      )}
    </div>
  );
}
