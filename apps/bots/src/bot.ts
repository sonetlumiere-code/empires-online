/**
 * Un bot (SPEC-BOTS §4.2): su sesión web, su conexión «casa» con la vista en la
 * ciudad, la de «campaña» que sigue a una partida de saqueo, su espejo del mundo
 * en cada una, el bucle que despierta cada `thinkMs` y el registro.
 *
 * Es la parte impura: hora, red y temporizadores. Lo que decide está en
 * `decide.ts`, que es pura.
 */
import type { v1 } from '@empires-online/protocol';
import { GameClient, type WebSocketLike } from '../../web/src/net/client';
import { createWorldStore, terrainAt, type BuildingView, type CityView, type UnitView } from '../../web/src/state/world';
import { TERRAIN_HILL } from '../../web/src/state/economy';
import { WebSession, type Account } from './auth';
import {
  EMPTY_MEMORY,
  chebyshev,
  decide,
  fieldFocus,
  forestTilesAround,
  onRaidLoss,
  onRejected,
  type BotCity,
  type Command,
  type Memory,
  type Tile,
  type View,
} from './decide';
import type { RunLog } from './log';
import type { Profile } from './profiles';
import { createRng, type Rng } from './random';

/** Las ciudades de los bots del proceso, para elegir blanco aunque no estén a la vista. */
export class Directory {
  private readonly cities = new Map<string, BotCity>();
  set(c: BotCity): void {
    this.cities.set(c.playerId, c);
  }
  list(): BotCity[] {
    return [...this.cities.values()].sort((a, b) => a.playerId.localeCompare(b.playerId));
  }
}

export interface BotDeps {
  readonly name: string;
  readonly profile: Profile;
  readonly civilization: string;
  readonly web: string;
  readonly server: string;
  readonly password: string;
  readonly thinkMs: number;
  readonly seed: number;
  readonly contraHumanos: boolean;
  readonly directory: Directory;
  readonly log: RunLog;
  /** Para la consola: nunca recibe secretos. */
  readonly say: (text: string) => void;
  readonly socketFactory?: (url: string) => WebSocketLike;
  readonly session?: WebSession;
}

/** Una conexión del bot con su propio espejo. */
class Link {
  readonly store: ReturnType<typeof createWorldStore>;
  readonly client: GameClient;
  private waiters: ((m: v1.ServerMessage) => boolean)[] = [];

  constructor(
    url: string,
    fetchTicket: () => Promise<string>,
    onMessage: (link: Link, m: v1.ServerMessage) => void,
    onError: (code: string, message: string) => void,
    socketFactory?: (url: string) => WebSocketLike,
  ) {
    this.store = createWorldStore();
    this.client = new GameClient({
      url,
      fetchTicket,
      onMessage: (m) => {
        this.store.getState().applyServerMessage(m);
        this.waiters = this.waiters.filter((w) => !w(m));
        onMessage(this, m);
      },
      onError,
      ...(socketFactory ? { socketFactory } : {}),
    });
  }

  /** Espera al primer mensaje que cumpla `pred`, o falla tras `timeoutMs`. */
  waitFor(pred: (m: v1.ServerMessage) => boolean, timeoutMs: number): Promise<v1.ServerMessage> {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.waiters = this.waiters.filter((w) => w !== check);
        reject(new Error('tiempo de espera agotado'));
      }, timeoutMs);
      const check = (m: v1.ServerMessage) => {
        if (!pred(m)) return false;
        clearTimeout(timer);
        resolve(m);
        return true;
      };
      this.waiters.push(check);
    });
  }

  get ready(): boolean {
    return this.client.getState() === 'ready';
  }
}

type Stocks = Record<string, number>;

export class Bot {
  readonly name: string;
  private readonly session: WebSession;
  private readonly rng: Rng;
  private account: Account | null = null;
  private home: Link | null = null;
  private field: Link | null = null;
  private fieldView: Tile | null = null;
  private memory: Memory = EMPTY_MEMORY;
  private hills: Tile[] = [];
  private forestTiles: Tile[] = [];
  private scouting = false;
  private stopped = false;
  private thinkTimer: ReturnType<typeof setTimeout> | null = null;
  private stockTimer: ReturnType<typeof setInterval> | null = null;
  private readonly pending = new Map<string, { command: Command; sentAt: number }>();
  private readonly orderedAt = new Map<string, number>();
  private prevCity: CityView | null = null;
  /** Estado conocido de cada edificio de la ciudad, para apuntar la obra que termina una vez. */
  private readonly buildingStatus = new Map<number, string>();
  private startedAt = 0;
  private gathered: Stocks = { WOOD: 0, STONE: 0 };

  constructor(private readonly deps: BotDeps) {
    this.name = deps.name;
    this.session = deps.session ?? new WebSession(deps.web, deps.name, deps.password, deps.civilization);
    this.rng = createRng(deps.seed);
  }

  /** La hora del servidor estimada, en ms enteros (el desfase lleva medio RTT). */
  private now(): number {
    return Math.round(this.home?.client.serverTimeMs() ?? Date.now());
  }

  private emit(event: Parameters<RunLog['emit']>[1], fields: Record<string, unknown> = {}): void {
    this.deps.log.emit(this.now(), event, this.name, fields);
  }

  /** Entra (o se da de alta), conecta, explora las colinas y empieza a pensar. */
  async start(): Promise<void> {
    this.account = await this.session.open();
    this.deps.say(`${this.name}: ${this.account.created ? 'alta' : 'entrada'} (${this.deps.profile.name}, ${this.deps.civilization})`);

    this.home = this.link();
    const snapshot = this.home.waitFor((m) => m.type === 'world.snapshot', 30_000);
    await this.home.client.connect();
    await snapshot;

    const city = this.ownCity();
    if (!city) throw new Error(`${this.name}: el snapshot no trae su ciudad`);
    this.deps.directory.set({
      playerId: this.account.playerId,
      cityId: city.id,
      name: city.name,
      centerX: city.centerX,
      centerY: city.centerY,
    });
    this.startedAt = this.now();
    this.prevCity = city;
    this.emit('start', {
      profile: this.deps.profile.name,
      civilization: city.civilization,
      cityId: city.id,
      center: { x: city.centerX, y: city.centerY },
      era: city.era,
      stocks: city.stocks,
      population: city.population,
      populationLimit: city.populationLimit,
    });

    await this.scoutHills(city);
    this.stockTimer = setInterval(() => this.logStock(), 60_000);
    this.schedule(this.rng.int(this.deps.thinkMs));
  }

  /** Cierra las conexiones y escribe `stop` (RN-BOT-007). */
  stop(): void {
    if (this.stopped) return;
    this.stopped = true;
    if (this.thinkTimer) clearTimeout(this.thinkTimer);
    if (this.stockTimer) clearInterval(this.stockTimer);
    const city = this.ownCity();
    if (city) {
      this.emit('stop', {
        era: city.era,
        stocks: city.stocks,
        gathered: this.gathered,
        population: city.population,
        populationLimit: city.populationLimit,
        units: this.ownUnits().length,
        playedMs: this.now() - this.startedAt,
      });
    }
    this.home?.client.disconnect();
    this.field?.client.disconnect();
  }

  // ─────────────────────────────────────────────────────────
  // Conexiones
  // ─────────────────────────────────────────────────────────

  private link(): Link {
    return new Link(
      this.deps.server,
      () => this.ticket(),
      (link, m) => this.onMessage(link, m),
      (code, message) => this.onClientError(code, message),
      this.deps.socketFactory,
    );
  }

  private async ticket(): Promise<string> {
    try {
      return await this.session.ticket();
    } catch (err) {
      // La cookie caducó: se vuelve a entrar con la contraseña una vez (SPEC-BOTS §5).
      if ((err as { code?: string }).code === 'UNAUTHORIZED') {
        await this.session.relogin();
        return this.session.ticket();
      }
      throw err;
    }
  }

  private onClientError(code: string, message: string): void {
    // Los rechazos de una orden llegan como system.error con requestId y se tratan en onMessage.
    if (code === 'INVALID_MESSAGE' || code === 'UNAUTHORIZED' || code === 'INTERNAL_ERROR' || code === 'RATE_LIMITED') {
      this.emit('error', { code, message: message.slice(0, 200) });
    }
  }

  private ownCity(): CityView | null {
    const playerId = this.account?.playerId;
    if (!playerId || !this.home) return null;
    for (const c of this.home.store.getState().cities.values()) if (c.ownerPlayerId === playerId) return c;
    return null;
  }

  private ownUnits(): UnitView[] {
    const playerId = this.account?.playerId;
    const byId = new Map<number, UnitView>();
    for (const link of [this.home, this.field]) {
      if (!link) continue;
      for (const u of link.store.getState().units.values()) if (u.playerId === playerId) byId.set(u.id, u);
    }
    return [...byId.values()].sort((a, b) => a.id - b.id);
  }

  // ─────────────────────────────────────────────────────────
  // Exploración: las colinas están lejos de las ciudades
  // ─────────────────────────────────────────────────────────

  /**
   * Busca colinas moviendo la vista en anillos alrededor de la ciudad, como el
   * humo (`--piedra`): el terreno es inmutable y se queda en el espejo. Después
   * vuelve la vista a casa.
   */
  private async scoutHills(city: CityView): Promise<void> {
    const home = this.home;
    if (!home) return;
    this.scouting = true;
    const c = { x: city.centerX, y: city.centerY };
    const world = home.store.getState().world;
    const max = (world?.width ?? 512) - 1;
    try {
      this.hills = this.knownHills(c);
      for (const r of [96, 160, 224]) {
        if (this.hills.length > 0 && chebyshev(c, this.hills[0] as Tile) <= r - 64) break;
        for (const [dx, dy] of [[1, 0], [1, 1], [0, 1], [-1, 1], [-1, 0], [-1, -1], [0, -1], [1, -1]] as const) {
          if (this.stopped) return;
          const at = { x: Math.min(max, Math.max(0, c.x + dx * r)), y: Math.min(max, Math.max(0, c.y + dy * r)) };
          const snap = home.waitFor((m) => m.type === 'world.snapshot', 15_000);
          home.client.setView(at);
          await snap.catch(() => undefined);
        }
        this.hills = this.knownHills(c);
      }
    } finally {
      const back = home.waitFor((m) => m.type === 'world.snapshot', 15_000);
      home.client.setView(c);
      await back.catch(() => undefined);
      this.scouting = false;
    }
    const first = this.hills[0];
    this.deps.say(
      `${this.name}: ${this.hills.length > 0 && first ? `colina más cercana a ${chebyshev(c, first)} tiles` : 'sin colinas a la vista'}`,
    );
  }

  /** Las colinas del terreno conocido, de la más cercana a la más lejana (hasta 24). */
  private knownHills(c: Tile): Tile[] {
    const home = this.home;
    if (!home) return [];
    const out: (Tile & { d: number })[] = [];
    for (const chunk of home.store.getState().terrain.values()) {
      for (let i = 0; i < chunk.tiles.length; i++) {
        if (chunk.tiles[i] !== TERRAIN_HILL) continue;
        const x = chunk.cx * chunk.size + (i % chunk.size);
        const y = chunk.cy * chunk.size + Math.floor(i / chunk.size);
        out.push({ x, y, d: chebyshev(c, { x, y }) });
      }
    }
    out.sort((a, b) => a.d - b.d || a.y - b.y || a.x - b.x);
    return out.slice(0, 24).map(({ x, y }) => ({ x, y }));
  }

  // ─────────────────────────────────────────────────────────
  // El bucle
  // ─────────────────────────────────────────────────────────

  private schedule(delayMs: number): void {
    if (this.stopped) return;
    this.thinkTimer = setTimeout(() => {
      try {
        this.think();
      } catch (err) {
        this.emit('error', { code: 'BOT_ERROR', message: String((err as Error).message ?? err).slice(0, 200) });
      }
      this.schedule(this.deps.thinkMs);
    }, delayMs);
  }

  /** Lo que el bot ve ahora, para `decide`. */
  buildView(): View | null {
    const home = this.home;
    const playerId = this.account?.playerId;
    const city = this.ownCity();
    if (!home || !playerId || !city) return null;
    const hs = home.store.getState();
    const fs = this.field?.store.getState() ?? null;
    const chunkSize = hs.world?.chunkSize ?? 32;

    const lookup = (x: number, y: number) =>
      terrainAt(hs.terrain, chunkSize, x, y) ?? (fs ? terrainAt(fs.terrain, chunkSize, x, y) : undefined);
    if (this.forestTiles.length === 0) {
      this.forestTiles = forestTilesAround({ x: city.centerX, y: city.centerY }, 40, lookup).slice(0, 48);
    }

    const cities = new Map<number, CityView>();
    const buildings = new Map<number, BuildingView>();
    for (const s of fs ? [fs, hs] : [hs]) {
      for (const c of s.cities.values()) cities.set(c.id, c);
      for (const b of s.buildings.values()) buildings.set(b.id, b);
    }
    const byId = <T extends { id: number }>(a: T, b: T) => a.id - b.id;
    return {
      playerId,
      city,
      buildings: [...buildings.values()].filter((b) => b.cityId === city.id).sort(byId),
      units: this.ownUnits(),
      foreignCities: [...cities.values()].filter((c) => c.ownerPlayerId !== playerId).sort(byId),
      foreignBuildings: [...buildings.values()].filter((b) => b.cityId !== city.id).sort(byId),
      terrainAt: lookup,
      forests: hs.forests,
      forestTiles: this.forestTiles,
      hills: this.hills,
      botCities: this.deps.directory.list(),
    };
  }

  private think(): void {
    if (this.stopped || this.scouting || !this.home?.ready) return;
    const view = this.buildView();
    if (!view) return;
    const nowMs = this.now();
    const hadRaid = this.memory.raid;
    const { commands, memory } = decide(view, this.deps.profile, this.memory, nowMs, this.rng, {
      contraHumanos: this.deps.contraHumanos,
    });
    this.memory = memory;
    for (const cmd of commands) this.send(cmd, nowMs);

    if (!hadRaid && memory.raid) {
      this.emit('raid', { target: memory.raid.targetName, cityId: memory.raid.targetCityId, size: memory.raid.size });
      this.deps.say(`${this.name}: sale una partida de ${memory.raid.size} contra ${memory.raid.targetName}`);
    }
    if (hadRaid && !memory.raid) this.emit('raid_end', { target: hadRaid.targetName, lost: hadRaid.lost, size: hadRaid.size });
    this.followRaid();
    for (const [id, p] of this.pending) if (nowMs - p.sentAt > 60_000) this.pending.delete(id);
  }

  /** La conexión de campaña mira donde está la partida; se abre con la primera. */
  private followRaid(): void {
    const focus = fieldFocus(this.memory);
    if (!focus) return;
    if (!this.field) {
      this.field = this.link();
      void this.field.client.connect();
      this.fieldView = null;
      return;
    }
    if (!this.field.ready) return;
    if (this.fieldView && this.fieldView.x === focus.x && this.fieldView.y === focus.y) return;
    this.field.client.setView(focus);
    this.fieldView = focus;
  }

  private send(cmd: Command, nowMs: number): void {
    const c = this.home?.client;
    if (!c) return;
    let requestId: string;
    switch (cmd.type) {
      case 'unit.gather':
        requestId = c.gatherWood(cmd.unitId, cmd.target);
        break;
      case 'unit.move':
        requestId = c.moveUnit(cmd.unitId, cmd.target);
        break;
      case 'unit.pillage':
        requestId = c.pillageCity(cmd.unitId, cmd.cityId);
        break;
      case 'unit.demolish':
        requestId = c.demolishBuilding(cmd.unitId, cmd.buildingId);
        break;
      case 'city.build':
        requestId =
          cmd.building === 'HOUSE'
            ? c.buildHouse(cmd.cityId, cmd.tile)
            : cmd.building === 'WORKSHOP'
              ? c.buildWorkshop(cmd.cityId, cmd.tile)
              : c.buildWall(cmd.cityId, cmd.tile);
        break;
      case 'city.train':
        requestId = c.trainUnit(cmd.cityId, cmd.unitType as v1.TrainableUnitType);
        break;
      case 'city.advance':
        requestId = c.advanceEra(cmd.cityId);
        break;
      case 'city.craft':
        requestId = c.craft(cmd.cityId);
        break;
      case 'city.research':
        requestId = c.researchTechnology(cmd.cityId, cmd.technology);
        break;
    }
    this.pending.set(requestId, { command: cmd, sentAt: nowMs });
    this.orderedAt.set(cmd.key, nowMs);
    this.emit('command', { type: cmd.type, key: cmd.key, requestId });
  }

  // ─────────────────────────────────────────────────────────
  // Lo que llega
  // ─────────────────────────────────────────────────────────

  private onMessage(link: Link, m: v1.ServerMessage): void {
    if (this.stopped) return;
    const nowMs = this.now();

    // Un rechazo de una orden propia (RN-BOT-002).
    if ((m.type === 'system.error' || m.type === 'unit.move.rejected') && m.requestId) {
      const p = this.pending.get(m.requestId);
      if (p) {
        this.pending.delete(m.requestId);
        const code = m.payload.code;
        this.memory = onRejected(this.memory, p.command, code, nowMs, this.deps.profile);
        this.emit('rejected', { type: p.command.type, key: p.command.key, code });
      }
      return;
    }
    if (m.requestId) this.pending.delete(m.requestId);

    // Un guerrero de la partida que muere, lo vea la casa o la campaña.
    if (
      (m.type === 'entity.update' && m.payload.status === 'DEAD') ||
      (m.type === 'entity.despawn' && m.payload.reason === 'DEAD')
    ) {
      this.memory = onRaidLoss(this.memory, m.payload.id);
    }
    if (link !== this.home) return;

    switch (m.type) {
      case 'world.snapshot':
        // Un snapshot reemplaza la vista: su almacén es la nueva referencia, no recolección.
        this.prevCity = this.ownCity() ?? this.prevCity;
        this.recordBuildings();
        break;
      case 'city.update':
        this.onCity(nowMs);
        break;
      case 'building.update':
      case 'building.spawn': {
        // El delta sólo trae id y estado: la ciudad y el tipo se leen del espejo, ya aplicado.
        const id = m.type === 'building.spawn' ? m.payload.building.id : m.payload.id;
        const b = this.home?.store.getState().buildings.get(id);
        const city = this.ownCity();
        if (!b || !city || b.cityId !== city.id) break;
        const before = this.buildingStatus.get(id);
        this.buildingStatus.set(id, b.status);
        if (b.status === 'COMPLETE' && before !== 'COMPLETE') {
          const since = this.orderedAt.get(`build:${b.type}`);
          this.emit('built', { building: b.type, ...(since ? { ms: nowMs - since } : {}) });
        }
        break;
      }
      case 'building.destroyed': {
        const city = this.ownCity();
        if (city && m.payload.cityId === city.id) this.emit('house_lost', { buildingId: m.payload.id });
        break;
      }
      case 'event_log.notice':
        this.onNotice(m.payload);
        break;
      default:
        break;
    }
  }

  /** Lo que ya está en pie al conectar o reconectar no es una obra nueva. */
  private recordBuildings(): void {
    const city = this.ownCity();
    if (!city || !this.home) return;
    for (const b of this.home.store.getState().buildings.values()) {
      if (b.cityId === city.id) this.buildingStatus.set(b.id, b.status);
    }
  }

  private onCity(nowMs: number): void {
    const city = this.ownCity();
    const prev = this.prevCity;
    if (!city) return;
    this.prevCity = city;
    if (!prev) return;
    if (city.era !== prev.era) {
      this.emit('era', { era: city.era, ms: nowMs - this.startedAt });
      this.deps.say(`${this.name}: llega a ${city.era}`);
    }
    if (prev.production && !city.production) {
      const since = this.orderedAt.get(`train:${prev.production.unitType}`);
      this.emit('trained', { unitType: prev.production.unitType, ...(since ? { ms: nowMs - since } : {}) });
    }
    if (prev.research && !city.research) this.emit('researched', { technology: prev.research.technology });
    if (prev.craft && !city.craft) this.emit('crafted', { good: prev.craft.good, amount: prev.craft.amount });
    for (const r of ['WOOD', 'STONE'] as const) {
      const delta = city.stocks[r] - prev.stocks[r];
      if (delta > 0) this.gathered[r] = (this.gathered[r] ?? 0) + delta;
    }
    if (city.pillagedAtMs !== undefined && city.pillagedAtMs !== prev.pillagedAtMs) this.emit('pillaged', {});
    if (city.besiegedAtMs !== undefined && city.besiegedAtMs !== prev.besiegedAtMs) this.emit('besieged', {});
  }

  private onNotice(n: v1.EventLogNotice['payload']): void {
    const other = n.otherCityName ?? n.otherPlayerId;
    switch (n.kind) {
      case 'UNIT_LOST':
        this.emit('lost', { unitType: n.unitType ?? null, by: other });
        break;
      case 'ENEMY_KILLED':
        this.emit('killed', { unitType: n.unitType ?? null, of: other });
        break;
      case 'PILLAGE_DONE':
        this.emit('pillage_done', { amount: n.amount ?? 0, of: other });
        break;
      case 'HOUSE_DESTROYED':
        this.emit('demolished', { buildingType: n.buildingType ?? 'HOUSE', of: other });
        break;
      default:
        break;
    }
  }

  private logStock(): void {
    const city = this.ownCity();
    if (!city) return;
    this.emit('stock', {
      era: city.era,
      stocks: city.stocks,
      gathered: this.gathered,
      population: city.population,
      populationLimit: city.populationLimit,
      raid: this.memory.raid ? this.memory.raid.targetName : null,
    });
  }
}
