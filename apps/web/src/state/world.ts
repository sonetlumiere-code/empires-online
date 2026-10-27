/**
 * Estado del mundo en el cliente.
 *
 * Es una RÉPLICA DE SOLO LECTURA de lo que dice el servidor, construida a partir
 * de `world.snapshot` y mantenida con deltas incrementales. Ninguna acción del
 * jugador la modifica directamente: el jugador envía una intención, el servidor
 * decide, y el cambio llega como un delta.
 *
 * Un delta cuesta lo que cambia, no lo que existe: los mapas de entidades son
 * mutables y de referencia estable, cada entidad es un objeto inmutable que se
 * reemplaza al cambiar, y cada `set` que toca el mundo publica en `changes` qué
 * ids tocó. Los consumidores actualizan sólo eso (ver `WorldChanges`).
 *
 * Ver ../../../../docs/architecture/networking.md y frontend.md §4
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
  /** Ciudad que la aloja si está GARRISONED; `null` en el mapa. `cityId` es la de origen. */
  garrisonCityId: number | null;
}

/**
 * Territorio tal y como lo ve el cliente.
 *
 * El cliente NO calcula ownership: `ownerType` y `ownerId` llegan tal cual del
 * servidor y no se derivan de nada local. La geometría viaja en cada mensaje, así
 * que tampoco hay que mantener un catálogo aparte que invalidar.
 */
export interface TerritoryView {
  id: number;
  name: string;
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
  ownerType: string;
  ownerId: string | null;
  contested: boolean;
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

/**
 * Qué tocó el último cambio del mundo.
 *
 * Los mapas de entidades NO se copian por mensaje (con 2 000 unidades, copiar
 * `units` costaba más de 100 µs por `entity.update`): la referencia del mapa
 * es siempre la misma y no sirve para detectar cambios. Lo que se compara es
 * este objeto, que se reemplaza en cada `set` que toca el mundo y enumera los
 * ids afectados, de modo que la escena actualiza sólo esos sprites y el HUD
 * decide con un selector por valor. Cada entidad sí es un objeto inmutable que
 * se reemplaza entero al cambiar: `units.get(id)` de antes y de después se
 * pueden comparar por identidad.
 */
export interface WorldChanges {
  /** Crece con cada cambio del mundo; dos cambios nunca comparten número. */
  readonly revision: number;
  /** Cambió el conjunto entero (snapshot, welcome, reset): los ids no se enumeran. */
  readonly all: boolean;
  /** Unidades añadidas, modificadas o retiradas, y las que entran o salen de la selección. */
  readonly units: ReadonlySet<number>;
  readonly cities: ReadonlySet<number>;
  readonly territories: ReadonlySet<number>;
  /** Llegaron chunks de terreno nuevos. */
  readonly terrain: boolean;
}

export interface WorldState {
  playerId: string | null;
  sessionId: string | null;
  world: WorldInfo | null;
  tick: number;

  /**
   * Réplica de sólo lectura. Los mapas pertenecen al store y son los mismos
   * objetos durante toda la vida de la pestaña: un snapshot o un `reset` los
   * vacían y rellenan en sitio, nunca los sustituyen.
   */
  units: ReadonlyMap<number, UnitView>;
  cities: ReadonlyMap<number, CityView>;
  territories: ReadonlyMap<number, TerritoryView>;
  terrain: ReadonlyMap<string, ChunkTerrain>;
  changes: WorldChanges;

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

function unitFromView(u: v1.UnitView): UnitView {
  return {
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
    garrisonCityId: u.garrisonCityId ?? null,
  };
}

/** Un conjunto vacío compartido: la mayoría de los deltas no toca dos familias a la vez. */
const NO_IDS: ReadonlySet<number> = new Set<number>();

const INITIAL_CHANGES: WorldChanges = {
  revision: 0,
  all: true,
  units: NO_IDS,
  cities: NO_IDS,
  territories: NO_IDS,
  terrain: false,
};

export const useWorldStore = create<WorldState>((set, get) => {
  // Los mapas viven en esta clausura y se mutan en sitio: son los que el estado
  // expone como `ReadonlyMap`. Fuera del store nadie debe sustituirlos.
  const units = new Map<number, UnitView>();
  const cities = new Map<number, CityView>();
  const territories = new Map<number, TerritoryView>();
  const terrain = new Map<string, ChunkTerrain>();

  let revision = 0;
  const changed = (partial: Partial<Omit<WorldChanges, 'revision'>>): WorldChanges => ({
    revision: ++revision,
    all: false,
    units: NO_IDS,
    cities: NO_IDS,
    territories: NO_IDS,
    terrain: false,
    ...partial,
  });
  const unitChanged = (id: number): WorldChanges => changed({ units: new Set([id]) });

  return {
    playerId: null,
    sessionId: null,
    world: null,
    tick: 0,
    units,
    cities,
    territories,
    terrain,
    changes: INITIAL_CHANGES,
    selectedUnitId: null,
    lastError: null,

    // Cambiar de jugador cambia qué es «propio» en todas las entidades: se
    // señala el conjunto entero, que al conectar está vacío y al reconectar se
    // reemplaza enseguida por el snapshot.
    applyWelcome: (payload) =>
      set({
        playerId: payload.playerId,
        sessionId: payload.sessionId,
        world: payload.world,
        changes: changed({ all: true }),
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
      units.clear();
      for (const u of payload.units) units.set(u.id, unitFromView(u));

      territories.clear();
      for (const t of payload.territories) territories.set(t.id, { ...t });

      cities.clear();
      for (const c of payload.cities) cities.set(c.id, { ...c });

      let newTerrain = false;
      for (const t of payload.terrain) {
        terrain.set(chunkKey(t.cx, t.cy), {
          cx: t.cx,
          cy: t.cy,
          size: t.size,
          tiles: decodeTerrain(t.terrain),
        });
        newTerrain = true;
      }

      const selected = get().selectedUnitId;
      set({
        tick: payload.tick,
        // Si la unidad seleccionada ya no está en el área de interés, se deselecciona.
        selectedUnitId: selected !== null && units.has(selected) ? selected : null,
        changes: changed({ all: true, terrain: newTerrain }),
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
          units.set(u.id, unitFromView(u));
          set({ changes: unitChanged(u.id) });
          break;
        }

        case 'entity.update': {
          const p = message.payload;
          const existing = units.get(p.id);
          // Un delta sobre una entidad desconocida se ignora: llegará su spawn
          // o el próximo snapshot. Inventarla a partir de un delta parcial
          // produciría una unidad con campos fantasma.
          if (!existing) break;

          // Los campos ausentes no se tocan; `null` sí es un valor (cityId,
          // movement y garrisonCityId), por eso se compara con `undefined`.
          units.set(p.id, {
            ...existing,
            x: p.x !== undefined ? p.x : existing.x,
            y: p.y !== undefined ? p.y : existing.y,
            hp: p.hp !== undefined ? p.hp : existing.hp,
            status: p.status !== undefined ? p.status : existing.status,
            cityId: p.cityId !== undefined ? p.cityId : existing.cityId,
            movement: p.movement !== undefined ? p.movement : existing.movement,
            garrisonCityId: p.garrisonCityId !== undefined ? p.garrisonCityId : existing.garrisonCityId,
          });
          set({ changes: unitChanged(p.id) });
          break;
        }

        case 'entity.despawn': {
          const { id } = message.payload;
          if (!units.delete(id)) break;
          const selected = get().selectedUnitId;
          set({
            selectedUnitId: selected === id ? null : selected,
            changes: unitChanged(id),
          });
          break;
        }

        case 'territory.update': {
          // A diferencia de `entity.update`, esto NO es un delta parcial: el
          // mensaje trae el `TerritoryView` completo, geometría incluida
          // (RN-TERR-014). Por eso un territorio desconocido se añade en lugar de
          // ignorarse — no hay campos que quedaran sin rellenar.
          const t = message.payload.territory;
          territories.set(t.id, { ...t });
          set({ changes: changed({ territories: new Set([t.id]) }) });
          break;
        }

        case 'city.update': {
          const p = message.payload;
          const existing = cities.get(p.id);
          if (!existing) break;
          cities.set(p.id, {
            ...existing,
            name: p.name !== undefined ? p.name : existing.name,
            population: p.population !== undefined ? p.population : existing.population,
            populationLimit: p.populationLimit !== undefined ? p.populationLimit : existing.populationLimit,
            presenceState: p.presenceState !== undefined ? p.presenceState : existing.presenceState,
            protectionUntilMs: p.protectionUntilMs !== undefined ? p.protectionUntilMs : existing.protectionUntilMs,
          });
          set({ changes: changed({ cities: new Set([p.id]) }) });
          break;
        }

        case 'unit.movement.started': {
          const { unitId, movement } = message.payload;
          const existing = units.get(unitId);
          if (!existing) break;
          units.set(unitId, { ...existing, movement, status: 'MOVING' });
          set({ changes: unitChanged(unitId) });
          break;
        }

        case 'unit.movement.completed': {
          const { unitId, finalPosition } = message.payload;
          const existing = units.get(unitId);
          if (!existing) break;
          units.set(unitId, {
            ...existing,
            movement: null,
            status: 'IDLE',
            x: finalPosition.x,
            y: finalPosition.y,
          });
          set({ changes: unitChanged(unitId) });
          break;
        }

        case 'unit.movement.cancelled': {
          const { unitId, stoppedAt } = message.payload;
          const existing = units.get(unitId);
          if (!existing) break;
          units.set(unitId, {
            ...existing,
            movement: null,
            status: 'IDLE',
            x: stoppedAt.x,
            y: stoppedAt.y,
          });
          set({ changes: unitChanged(unitId) });
          break;
        }

        case 'unit.move.rejected':
          set({ lastError: { code: message.payload.code, message: message.payload.message } });
          break;

        case 'system.error':
          set({ lastError: { code: message.payload.code, message: message.payload.message } });
          break;

        default:
          // session.pong y unit.move.accepted no alteran el estado observable
          // del mundo.
          break;
      }
    },

    // La selección es estado de interfaz, pero la escena la dibuja: la unidad
    // que la pierde y la que la gana se señalan como cambiadas.
    selectUnit: (unitId) => {
      const previous = get().selectedUnitId;
      if (previous === unitId) return;
      const ids = new Set<number>();
      if (previous !== null) ids.add(previous);
      if (unitId !== null) ids.add(unitId);
      set({ selectedUnitId: unitId, changes: changed({ units: ids }) });
    },

    reset: () => {
      units.clear();
      cities.clear();
      territories.clear();
      terrain.clear();
      set({
        playerId: null,
        sessionId: null,
        world: null,
        tick: 0,
        selectedUnitId: null,
        lastError: null,
        changes: changed({ all: true }),
      });
    },
  };
});

/** Terreno de un tile concreto, o `undefined` si su chunk no está cargado. */
export function terrainAt(
  terrain: ReadonlyMap<string, ChunkTerrain>,
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

/** El chunk `(cx, cy)` si ya llegó su terreno. */
export function chunkAt(
  terrain: ReadonlyMap<string, ChunkTerrain>,
  cx: number,
  cy: number,
): ChunkTerrain | undefined {
  return terrain.get(chunkKey(cx, cy));
}

export { chunkKey, decodeTerrain };
