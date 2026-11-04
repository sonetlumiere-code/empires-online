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
import { applyTreatyUpdate, type TreatyView } from './diplomacy';
import { applyTradeUpdate, type TradeOfferView } from './trade';
import { applyEventLogPage, EMPTY_EVENT_LOG, markSeenLocally, type EventLogState } from './eventLog';
import { applyNotice, EMPTY_NOTICES, markLogOpened, setTabHidden, type NoticeState } from './notices';
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
  /** Lo que lleva encima una unidad que recolecta (SPEC-ECON-WOOD); 0 si nada. */
  carrying: number;
  /** El recurso de `carrying`, `WOOD` o `STONE` (SPEC-ECON-STONE); `null` si el servidor no lo dijo. */
  carryingResource: string | null;
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
  /** `true` exactamente mientras hay una reclamación activa (SPEC-TERRITORY-CONQUEST §6). */
  contested: boolean;
  /** La reclamación activa: quién reclama y cuándo termina. Ausente si no la hay. */
  claim?: { playerId: string; cityId: number | null; cityName: string | null; startedAtMs: number; endsAtMs: number };
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
  /** Stock de la ciudad, tal cual lo envía el servidor (SPEC-ECON-WOOD, SPEC-ECON-STONE). */
  stocks: { WOOD: number; STONE: number };
  /** Entrenamiento en curso, o `null`. */
  production: { unitType: string; completesAtMs: number } | null;
  /** Avance de era en curso, o `null` (SPEC-ECON-STONE). */
  advance: { era: string; completesAtMs: number } | null;
  /**
   * Instante del último golpe de saqueo, si lo hubo en esta sesión. Sólo lo
   * recibe el dueño (SPEC-COMBAT-PILLAGE RN-COMBAT-028).
   */
  pillagedAtMs?: number;
  /**
   * Instante del último golpe de asedio a una casa de la ciudad, si lo hubo en esta
   * sesión. Sólo lo recibe el dueño (SPEC-COMBAT-SIEGE RN-COMBAT-038).
   */
  besiegedAtMs?: number;
}

/** Edificio tal y como lo ve el cliente (SPEC-ECON-WOOD §4.3). */
export interface BuildingView {
  id: number;
  cityId: number;
  type: string;
  x: number;
  y: number;
  status: string;
  completesAtMs: number;
  /** Vida frente a un asedio y su máximo (SPEC-COMBAT-SIEGE); un servidor sin asedio no la manda. */
  hp?: number;
  maxHp?: number;
}

/**
 * Madera de un tile de bosque que no está lleno (SPEC-ECON-FOREST). Un tile
 * FOREST que no está en el mapa está lleno. Es una capa aparte del terreno, que
 * no cambia nunca.
 */
export interface ForestView {
  x: number;
  y: number;
  remaining: number;
  regrowsAtMs: number;
}

/** Clave de un tile en el mapa de bosque o de colina. */
export const forestKey = (x: number, y: number) => `${x},${y}`;

/**
 * Piedra de un tile de colina que no está lleno (SPEC-ECON-STONE): la misma forma
 * que el bosque, en su propio mapa.
 */
export type HillView = ForestView;

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
  readonly buildings: ReadonlySet<number>;
  /** Llegaron chunks de terreno nuevos. */
  readonly terrain: boolean;
  /** Cambió la madera de algún tile de bosque. */
  readonly forests: boolean;
  /** Cambió la piedra de algún tile de colina. */
  readonly hills: boolean;
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
  buildings: ReadonlyMap<number, BuildingView>;
  terrain: ReadonlyMap<string, ChunkTerrain>;
  /** Tiles de bosque que no están llenos, por `forestKey`. */
  forests: ReadonlyMap<string, ForestView>;
  /** Tiles de colina que no están llenos, por `forestKey`. */
  hills: ReadonlyMap<string, HillView>;
  /**
   * Tratados abiertos del jugador (SPEC-DIPLOMACY), por id. A diferencia de los
   * mapas de arriba se sustituye en cada cambio: sólo lo lee el HUD.
   */
  treaties: readonly TreatyView[];
  /** Ofertas de comercio pendientes del jugador (SPEC-TRADE), por id. Sólo las lee el HUD. */
  tradeOffers: readonly TradeOfferView[];
  /** El registro (SPEC-EVENT-LOG): lo que el servidor contó que pasó. Sólo lo lee el HUD. */
  eventLog: EventLogState;
  /**
   * Los avisos en vivo (SPEC-LIVE-NOTICES), aparte del registro: no tienen `id` ni
   * entran en su lista. Sólo los lee el HUD.
   */
  notices: NoticeState;
  changes: WorldChanges;

  selectedUnitId: number | null;
  lastError: { code: string; message: string } | null;

  applyWelcome: (payload: v1.SessionWelcome['payload']) => void;
  applySnapshot: (payload: v1.WorldSnapshot['payload']) => void;
  applyServerMessage: (message: v1.ServerMessage) => void;
  selectUnit: (unitId: number | null) => void;
  /**
   * Anota que se pidió una página del registro con ese requestId: `null` para la
   * primera, o el `beforeId` de una anterior. Sólo se aplican las páginas pedidas.
   */
  expectEventLogPage: (requestId: string, beforeId: number | null) => void;
  /** Lo da por visto en esta pestaña hasta `upToId`. */
  markEventLogSeen: (upToId: number) => void;
  /** Se abrió el registro: «Registro» pierde el punto de avisos (SPEC-LIVE-NOTICES). */
  markNoticesOpened: () => void;
  /** La pestaña se ocultó o volvió: el contador del título (SPEC-LIVE-NOTICES Q7). */
  setTabHidden: (hidden: boolean) => void;
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
    carrying: u.carrying ?? 0,
    carryingResource: u.carryingResource ?? null,
  };
}

function cityFromView(c: v1.CityView): CityView {
  return {
    ...c,
    stocks: { WOOD: c.stocks?.WOOD ?? 0, STONE: c.stocks?.STONE ?? 0 },
    production: c.production ?? null,
    advance: c.advance ?? null,
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
  buildings: NO_IDS,
  terrain: false,
  forests: true,
  hills: true,
};

export const useWorldStore = create<WorldState>((set, get) => {
  // Los mapas viven en esta clausura y se mutan en sitio: son los que el estado
  // expone como `ReadonlyMap`. Fuera del store nadie debe sustituirlos.
  const units = new Map<number, UnitView>();
  const cities = new Map<number, CityView>();
  const territories = new Map<number, TerritoryView>();
  const buildings = new Map<number, BuildingView>();
  // Páginas del registro pedidas y aún sin respuesta: requestId → beforeId (null
  // para la primera). No es estado observable.
  const eventLogRequests = new Map<string, number | null>();
  const terrain = new Map<string, ChunkTerrain>();
  const forests = new Map<string, ForestView>();
  const hills = new Map<string, HillView>();

  let revision = 0;
  const changed = (partial: Partial<Omit<WorldChanges, 'revision'>>): WorldChanges => ({
    revision: ++revision,
    all: false,
    units: NO_IDS,
    cities: NO_IDS,
    territories: NO_IDS,
    buildings: NO_IDS,
    terrain: false,
    forests: false,
    hills: false,
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
    buildings,
    terrain,
    forests,
    hills,
    treaties: [],
    tradeOffers: [],
    eventLog: EMPTY_EVENT_LOG,
    notices: EMPTY_NOTICES,
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
      for (const c of payload.cities) cities.set(c.id, cityFromView(c));

      buildings.clear();
      for (const b of payload.buildings ?? []) buildings.set(b.id, { ...b });

      // El snapshot trae todo el bosque a medias de su área: lo que no viene está lleno.
      forests.clear();
      for (const f of payload.forests ?? []) {
        if (f.regrowsAtMs !== null) forests.set(forestKey(f.x, f.y), { ...f, regrowsAtMs: f.regrowsAtMs });
      }
      // Igual la colina (SPEC-ECON-STONE).
      hills.clear();
      for (const h of payload.hills ?? []) {
        if (h.regrowsAtMs !== null) hills.set(forestKey(h.x, h.y), { ...h, regrowsAtMs: h.regrowsAtMs });
      }

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
        changes: changed({ all: true, terrain: newTerrain, forests: true, hills: true }),
        // Como el resto del snapshot, la lista de tratados se reemplaza (ADR-027 §5).
        treaties: [...(payload.treaties ?? [])].sort((a, b) => a.id - b.id),
        // Y la de ofertas de comercio, igual (SPEC-TRADE §6).
        tradeOffers: [...(payload.tradeOffers ?? [])].sort((a, b) => a.id - b.id),
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
            carrying: p.carrying !== undefined ? p.carrying : existing.carrying,
            carryingResource: p.carryingResource !== undefined ? p.carryingResource : existing.carryingResource,
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
            // Un servidor anterior a la piedra no la manda: se conserva la que había.
            stocks:
              p.stocks !== undefined
                ? { WOOD: p.stocks.WOOD, STONE: p.stocks.STONE ?? existing.stocks.STONE }
                : existing.stocks,
            production: p.production !== undefined ? p.production : existing.production,
            era: p.era !== undefined ? p.era : existing.era,
            advance: p.advance !== undefined ? p.advance : existing.advance,
            pillagedAtMs: p.pillagedAtMs !== undefined ? p.pillagedAtMs : existing.pillagedAtMs,
            besiegedAtMs: p.besiegedAtMs !== undefined ? p.besiegedAtMs : existing.besiegedAtMs,
          });
          set({ changes: changed({ cities: new Set([p.id]) }) });
          break;
        }

        case 'building.spawn': {
          // Como territory.update, trae la vista completa: un edificio que no
          // se conocía se añade, uno conocido se reemplaza.
          const b = message.payload.building;
          buildings.set(b.id, { ...b });
          set({ changes: changed({ buildings: new Set([b.id]) }) });
          break;
        }

        case 'building.update': {
          const p = message.payload;
          const existing = buildings.get(p.id);
          if (!existing) break;
          buildings.set(p.id, { ...existing, status: p.status, hp: p.hp !== undefined ? p.hp : existing.hp });
          set({ changes: changed({ buildings: new Set([p.id]) }) });
          break;
        }

        case 'building.destroyed': {
          // Una casa derribada por un asedio deja de existir, como una unidad
          // muerta (SPEC-COMBAT-SIEGE §6): la escena la retira por su id.
          const p = message.payload;
          if (!buildings.delete(p.id)) break;
          set({ changes: changed({ buildings: new Set([p.id]) }) });
          break;
        }

        case 'treaty.update':
          set({ treaties: applyTreatyUpdate(get().treaties, message.payload.treaty) });
          break;

        case 'trade.update':
          set({ tradeOffers: applyTradeUpdate(get().tradeOffers, message.payload.offer) });
          break;

        case 'forest.update': {
          // Sin instante de regeneración, el tile vuelve a estar lleno y sale de la capa.
          const f = message.payload;
          if (f.regrowsAtMs === null) forests.delete(forestKey(f.x, f.y));
          else forests.set(forestKey(f.x, f.y), { ...f, regrowsAtMs: f.regrowsAtMs });
          set({ changes: changed({ forests: true }) });
          break;
        }

        case 'hill.update': {
          // Como el bosque: sin instante de regeneración, la colina vuelve a estar llena.
          const h = message.payload;
          if (h.regrowsAtMs === null) hills.delete(forestKey(h.x, h.y));
          else hills.set(forestKey(h.x, h.y), { ...h, regrowsAtMs: h.regrowsAtMs });
          set({ changes: changed({ hills: true }) });
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

        case 'event_log.page': {
          const id = message.requestId;
          const before = id !== undefined ? eventLogRequests.get(id) : undefined;
          if (id === undefined || before === undefined) break;
          eventLogRequests.delete(id);
          set({ eventLog: applyEventLogPage(get().eventLog, message.payload, before) });
          break;
        }

        case 'event_log.notice':
          // No es una entrada: no toca la lista ni el cursor (RN-NOTICE-004).
          set({ notices: applyNotice(get().notices, message.payload) });
          break;

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

    expectEventLogPage: (requestId, beforeId) => {
      eventLogRequests.set(requestId, beforeId);
    },

    markEventLogSeen: (upToId) => {
      set({ eventLog: markSeenLocally(get().eventLog, upToId) });
    },

    markNoticesOpened: () => {
      set({ notices: markLogOpened(get().notices) });
    },

    setTabHidden: (hidden) => {
      set({ notices: setTabHidden(get().notices, hidden) });
    },

    reset: () => {
      units.clear();
      cities.clear();
      territories.clear();
      buildings.clear();
      terrain.clear();
      forests.clear();
      hills.clear();
      set({
        playerId: null,
        sessionId: null,
        world: null,
        tick: 0,
        selectedUnitId: null,
        lastError: null,
        treaties: [],
        tradeOffers: [],
        eventLog: EMPTY_EVENT_LOG,
        notices: EMPTY_NOTICES,
        changes: changed({ all: true }),
      });
      eventLogRequests.clear();
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
