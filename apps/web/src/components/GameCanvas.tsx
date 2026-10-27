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

import { IsoScene } from '../render/scene';
import { projectAll, projectChanges } from '../render/projection';
import { CameraController, panDirectionOf } from '../render/camera';
import type { Point, Tile } from '../render/iso';
import { useWorldStore, type WorldState } from '../state/world';

export interface GameCanvasProps {
  /** Instante actual del SERVIDOR (reloj local corregido por el desfase). */
  serverTimeMs: () => number;
  onTileClick: (tile: Tile) => void;
  onUnitClick: (unitId: number) => void;
  /** Centro de la cámara en cada frame, en tiles continuos (frontend.md §7.4.1). */
  onCameraCenter?: (center: Point) => void;
}

/** La ciudad propia en el store, si la hay. */
function ownCityCenter(state: WorldState): Tile | null {
  for (const c of state.cities.values()) {
    if (c.ownerPlayerId === state.playerId) return { x: c.centerX, y: c.centerY };
  }
  return null;
}

export function GameCanvas({ serverTimeMs, onTileClick, onUnitClick, onCameraCenter }: GameCanvasProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const sceneRef = useRef<IsoScene | null>(null);
  const [renderError, setRenderError] = useState<string | null>(null);

  // Los callbacks viven en un ref para que la escena no tenga que recrearse cada
  // vez que el padre se vuelve a renderizar.
  const handlers = useRef({ onTileClick, onUnitClick, onCameraCenter });
  handlers.current = { onTileClick, onUnitClick, onCameraCenter };

  // La cámara vive mientras viva el componente, también a través de una
  // reconexión: así la vista se restaura en lugar de volver a la ciudad.
  const cameraRef = useRef<CameraController>(new CameraController());

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

        const s = new IsoScene(
          cameraRef.current,
          {
            onTileClick: (tile) => handlers.current.onTileClick(tile),
            onUnitClick: (unitId) => handlers.current.onUnitClick(unitId),
            onCameraFrame: (center) => handlers.current.onCameraCenter?.(center),
          },
          // Se pasa la FUNCIÓN, no el valor: la escena necesita el instante de
          // cada frame para interpolar.
          () => timeRef.current(),
        );
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

        // Lo que el store ya tenía cuando la escena estuvo lista; desde aquí
        // sólo llegan cambios.
        const initial = projectAll(useWorldStore.getState());
        if (initial) s.apply(initial);
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

  // El store empuja a la escena SÓLO lo que cambió; la escena decide cuándo
  // dibujar. Los mapas del store son estables: lo que distingue un cambio del
  // mundo de uno que no lo es (un error) es la señal `changes`.
  useEffect(() => {
    const push = (state: WorldState, previous: WorldState | null) => {
      if (previous && state.changes === previous.changes) return;
      const changes = state.changes;

      // La primera vez que se conoce la ciudad propia, la cámara se centra en
      // ella; después ya no la recentra nada salvo el jugador (tecla H). Sólo
      // hay que mirarlo cuando cambian las ciudades o el conjunto entero.
      const camera = cameraRef.current;
      if (state.world && (changes.all || changes.cities.size > 0)) {
        camera.setBounds(state.world);
        const city = ownCityCenter(state);
        if (city) camera.setHome(city);
      }

      const scene = sceneRef.current;
      if (!scene) return;

      const update = previous ? projectChanges(state, changes) : projectAll(state);
      if (update) scene.apply(update);
    };

    push(useWorldStore.getState(), null);
    return useWorldStore.subscribe(push);
  }, []);

  // Teclado y arrastre de la cámara (frontend.md §7.4.1). Viven en el DOM, no en
  // Pixi: el botón izquierdo sigue siendo de la escena (seleccionar y ordenar).
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const camera = cameraRef.current;

    const ignorable = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey || e.altKey) return true;
      const target = e.target as HTMLElement | null;
      const tag = target?.tagName;
      return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target?.isContentEditable === true;
    };

    const onKeyDown = (e: KeyboardEvent) => {
      if (ignorable(e)) return;
      const direction = panDirectionOf(e.key);
      if (direction) {
        camera.setHeld(direction, true);
        e.preventDefault(); // las flechas no deben desplazar la página
        return;
      }
      if (e.key === 'h' || e.key === 'H') camera.goHome();
    };
    const onKeyUp = (e: KeyboardEvent) => {
      const direction = panDirectionOf(e.key);
      if (direction) camera.setHeld(direction, false);
    };
    // Si la ventana pierde el foco con una tecla pulsada, su keyup no llega.
    const onBlur = () => camera.releaseAll();

    let drag: { pointerId: number; x: number; y: number } | null = null;
    const onPointerDown = (e: PointerEvent) => {
      if (e.button !== 1 && e.button !== 2) return;
      drag = { pointerId: e.pointerId, x: e.clientX, y: e.clientY };
      // La captura sólo mejora el arrastre fuera del lienzo; si el puntero ya no
      // está activo el navegador la rechaza, y el arrastre sigue igual.
      try {
        canvas.setPointerCapture(e.pointerId);
      } catch {
        /* sin captura */
      }
      e.preventDefault();
    };
    const onPointerMove = (e: PointerEvent) => {
      if (!drag || e.pointerId !== drag.pointerId) return;
      camera.dragBy(e.clientX - drag.x, e.clientY - drag.y);
      drag = { pointerId: e.pointerId, x: e.clientX, y: e.clientY };
    };
    const onPointerUp = (e: PointerEvent) => {
      if (!drag || e.pointerId !== drag.pointerId) return;
      drag = null;
      if (canvas.hasPointerCapture?.(e.pointerId)) canvas.releasePointerCapture(e.pointerId);
    };
    const onContextMenu = (e: MouseEvent) => e.preventDefault();

    window.addEventListener('keydown', onKeyDown);
    window.addEventListener('keyup', onKeyUp);
    window.addEventListener('blur', onBlur);
    canvas.addEventListener('pointerdown', onPointerDown);
    canvas.addEventListener('pointermove', onPointerMove);
    canvas.addEventListener('pointerup', onPointerUp);
    canvas.addEventListener('pointercancel', onPointerUp);
    canvas.addEventListener('contextmenu', onContextMenu);
    return () => {
      window.removeEventListener('keydown', onKeyDown);
      window.removeEventListener('keyup', onKeyUp);
      window.removeEventListener('blur', onBlur);
      canvas.removeEventListener('pointerdown', onPointerDown);
      canvas.removeEventListener('pointermove', onPointerMove);
      canvas.removeEventListener('pointerup', onPointerUp);
      canvas.removeEventListener('pointercancel', onPointerUp);
      canvas.removeEventListener('contextmenu', onContextMenu);
      camera.releaseAll();
    };
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
