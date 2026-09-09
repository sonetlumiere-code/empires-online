/**
 * Estado del mundo en el cliente.
 *
 * Es una RÉPLICA DE SOLO LECTURA de lo que dice el servidor, construida a partir
 * de `world.snapshot` y mantenida con deltas incrementales. Ninguna acción del
 * jugador la modifica directamente: el jugador envía una intención, el servidor
 * decide, y el cambio llega como un delta.
 *
 * Ver ../../../../docs/architecture/networking.md
 */
import { create } from 'zustand';
import type { v1 } from '@empires-online/protocol';
import type { ActiveMovement } from './interpolation';

export interface UnitView {
  id: number;
  playerId: string;
  cityId: number | null;
  unitType: string;
  /** Posición autoritativa consolidada que envió el servidor. */
  x: number;
  y: number;
  hp: number;
  maxHp: number;
  status: string;
  movement: ActiveMovement | null;
}

export interface CityView {
  id: number;
  ownerPlayerId: string;
  name: string;
  centerX: number;
  centerY: number;
  era: string;
  population: number;
  populationLimit: number;
  presenceState: string;
  protectionUntilMs: number | null;
}

export interface ChunkTerrain {
  cx: number;
  cy: number;
  size: number;
  /** Bytes de terreno ya decodificados del base64. */
  tiles: Uint8Array;
}

export interface WorldInfo {
  width: number;
  height: number;
  chunkSize: number;
}

interface WorldState {
  playerId: string | null;
  sessionId: string | null;
  world: WorldInfo | null;
  tick: number;

  units: Map<number, UnitView>;
  cities: Map<number, CityView>;
  terrain: Map<string, ChunkTerrain>;

  selectedUnitId: number | null;
  lastError: { code: string; message: string } | null;

  applyWelcome: (payload: v1.SessionWelcome['payload']) => void;
  applySnapshot: (payload: v1.WorldSnapshot['payload']) => void;
  applyServerMessage: (message: v1.ServerMessage) => void;
  selectUnit: (unitId: number | null) => void;
  reset: () => void;
}

const chunkKey = (cx: number, cy: number) => `${cx}:${cy}`;

/** Decodifica el terreno base64 de un chunk. */
function decodeTerrain(base64: string): Uint8Array {
  const binary = typeof atob === 'function' ? atob(base64) : Buffer.from(base64, 'base64').toString('binary');
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
  return out;
}

export const useWorldStore = create<WorldState>((set, get) => ({
  playerId: null,
  sessionId: null,
  world: null,
  tick: 0,
  units: new Map(),
  cities: new Map(),
  terrain: new Map(),
  selectedUnitId: null,
  lastError: null,

  applyWelcome: (payload) =>
    set({
      playerId: payload.playerId,
      sessionId: payload.sessionId,
      world: payload.world,
    }),

  /**
   * El snapshot REEMPLAZA el estado observable, no lo mezcla.
   *
   * Es la razón por la que reconectar es fiable: no hay que reproducir deltas
   * perdidos ni razonar sobre qué se quedó a medias. Se tira lo que había y se
   * parte de la foto que acaba de enviar el servidor.
   *
   * El terreno es la excepción: es inmutable, así que los chunks ya conocidos se
   * conservan y sólo se añaden los nuevos.
   */
  applySnapshot: (payload) => {
    const units = new Map<number, UnitView>();
    for (const u of payload.units) {
      units.set(u.id, {
        id: u.id,
        playerId: u.playerId,
        cityId: u.cityId,
        unitType: u.unitType,
        x: u.x,
        y: u.y,
        hp: u.hp,
        maxHp: u.maxHp,
        status: u.status,
        movement: u.movement ?? null,
      });
    }

    const cities = new Map<number, CityView>();
    for (const c of payload.cities) cities.set(c.id, { ...c });

    const terrain = new Map(get().terrain);
    for (const t of payload.terrain) {
      terrain.set(chunkKey(t.cx, t.cy), {
        cx: t.cx,
        cy: t.cy,
        size: t.size,
        tiles: decodeTerrain(t.terrain),
      });
    }

    const selected = get().selectedUnitId;
    set({
      units,
      cities,
      terrain,
      tick: payload.tick,
      // Si la unidad seleccionada ya no está en el área de interés, se deselecciona.
      selectedUnitId: selected !== null && units.has(selected) ? selected : null,
    });
  },

  applyServerMessage: (message) => {
    switch (message.type) {
      case 'session.welcome':
        get().applyWelcome(message.payload);
        break;

      case 'world.snapshot':
        get().applySnapshot(message.payload);
        break;

      case 'entity.spawn': {
        const u = message.payload.unit;
        set((s) => {
          const units = new Map(s.units);
          units.set(u.id, {
            id: u.id,
            playerId: u.playerId,
            cityId: u.cityId,
            unitType: u.unitType,
            x: u.x,
            y: u.y,
            hp: u.hp,
            maxHp: u.maxHp,
            status: u.status,
            movement: u.movement ?? null,
          });
          return { units };
        });
        break;
      }

      case 'entity.update': {
        const p = message.payload;
        set((s) => {
          const existing = s.units.get(p.id);
          // Un delta sobre una entidad desconocida se ignora: llegará su spawn
          // o el próximo snapshot. Inventarla a partir de un delta parcial
          // produciría una unidad con campos fantasma.
          if (!existing) return s;

          const units = new Map(s.units);
          units.set(p.id, {
            ...existing,
            ...(p.x !== undefined ? { x: p.x } : {}),
            ...(p.y !== undefined ? { y: p.y } : {}),
            ...(p.hp !== undefined ? { hp: p.hp } : {}),
            ...(p.status !== undefined ? { status: p.status } : {}),
            ...(p.cityId !== undefined ? { cityId: p.cityId } : {}),
            ...(p.movement !== undefined ? { movement: p.movement } : {}),
          });
          return { units };
        });
        break;
      }

      case 'entity.despawn': {
        const { id } = message.payload;
        set((s) => {
          if (!s.units.has(id)) return s;
          const units = new Map(s.units);
          units.delete(id);
          return {
            units,
            selectedUnitId: s.selectedUnitId === id ? null : s.selectedUnitId,
          };
        });
        break;
      }

      case 'city.update': {
        const p = message.payload;
        set((s) => {
          const existing = s.cities.get(p.id);
          if (!existing) return s;
          const cities = new Map(s.cities);
          cities.set(p.id, {
            ...existing,
            ...(p.name !== undefined ? { name: p.name } : {}),
            ...(p.population !== undefined ? { population: p.population } : {}),
            ...(p.populationLimit !== undefined ? { populationLimit: p.populationLimit } : {}),
            ...(p.presenceState !== undefined ? { presenceState: p.presenceState } : {}),
            ...(p.protectionUntilMs !== undefined ? { protectionUntilMs: p.protectionUntilMs } : {}),
          });
          return { cities };
        });
        break;
      }

      case 'unit.movement.started': {
        const { unitId, movement } = message.payload;
        set((s) => {
          const existing = s.units.get(unitId);
          if (!existing) return s;
          const units = new Map(s.units);
          units.set(unitId, { ...existing, movement, status: 'MOVING' });
          return { units };
        });
        break;
      }

      case 'unit.movement.completed': {
        const { unitId, finalPosition } = message.payload;
        set((s) => {
          const existing = s.units.get(unitId);
          if (!existing) return s;
          const units = new Map(s.units);
          units.set(unitId, {
            ...existing,
            movement: null,
            status: 'IDLE',
            x: finalPosition.x,
            y: finalPosition.y,
          });
          return { units };
        });
        break;
      }

      case 'unit.movement.cancelled': {
        const { unitId, stoppedAt } = message.payload;
        set((s) => {
          const existing = s.units.get(unitId);
          if (!existing) return s;
          const units = new Map(s.units);
          units.set(unitId, {
            ...existing,
            movement: null,
            status: 'IDLE',
            x: stoppedAt.x,
            y: stoppedAt.y,
          });
          return { units };
        });
        break;
      }

      case 'unit.move.rejected':
        set({ lastError: { code: message.payload.code, message: message.payload.message } });
        break;

      case 'system.error':
        set({ lastError: { code: message.payload.code, message: message.payload.message } });
        break;

      default:
        // session.pong, unit.move.accepted y territory.update no alteran el
        // estado observable del mundo.
        break;
    }
  },

  selectUnit: (unitId) => set({ selectedUnitId: unitId }),

  reset: () =>
    set({
      playerId: null,
      sessionId: null,
      world: null,
      tick: 0,
      units: new Map(),
      cities: new Map(),
      terrain: new Map(),
      selectedUnitId: null,
      lastError: null,
    }),
}));

/** Terreno de un tile concreto, o `undefined` si su chunk no está cargado. */
export function terrainAt(
  terrain: Map<string, ChunkTerrain>,
  chunkSize: number,
  x: number,
  y: number,
): number | undefined {
  const cx = Math.floor(x / chunkSize);
  const cy = Math.floor(y / chunkSize);
  const chunk = terrain.get(chunkKey(cx, cy));
  if (!chunk) return undefined;
  const localX = x - cx * chunkSize;
  const localY = y - cy * chunkSize;
  return chunk.tiles[localY * chunk.size + localX];
}

export { chunkKey, decodeTerrain };
