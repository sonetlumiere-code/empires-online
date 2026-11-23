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
import { commission } from '../../web/src/state/market';
import { walled } from '../../web/src/state/cityFall';
import { WebSession, type Account } from './auth';
import {
  EMPTY_MEMORY,
  chebyshev,
  decide,
  fieldFocus,
  forestTilesAround,
  onOwnCityFell,
  onRaidLoss,
  onRejected,
  onTargetFell,
  type Raid,
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
  /** `--sin-comercio` lo apaga (SPEC-BOTS-TRADE §4.8). */
  readonly comercio?: boolean;
  /** `--con-humanos` (RN-BOT-008). */
  readonly conHumanos?: boolean;
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

/** Una línea de recurso con el tipo del protocolo. */
function line(l: { resource: string; amount: number }): v1.ResourceAmount {
  return { resource: l.resource as v1.ResourceType, amount: l.amount };
}

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
  /** El tipo de cada edificio de la ciudad: un `building.destroyed` sólo trae el id. */
  private readonly buildingType = new Map<number, string>();
  /** La muralla propia cayó en esta ejecución: la próxima completa es una reconstrucción. */
  private wallBreached = false;
  /** El `city.fallen` de la ciudad propia, hasta que llega el snapshot de la refundada. */
  private pendingFall: { cityId: number; byPlayerId: string } | null = null;
  private startedAt = 0;
  /** Último estado apuntado de cada tratado, trato y caravana (para no repetirlo). */
  private readonly seenStatus = new Map<string, string>();
  /** Lotes vendidos de cada oferta propia del mercado. */
  private readonly lotsSold = new Map<number, number>();
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

    this.home = this.link();
    const snapshot = this.home.waitFor((m) => m.type === 'world.snapshot', 30_000);
    await this.home.client.connect();
    await snapshot;

    const city = this.ownCity();
    if (!city) throw new Error(`${this.name}: el snapshot no trae su ciudad`);
    // La civilización, de la ciudad: la del reparto sólo vale para un alta nueva.
    this.deps.say(`${this.name}: ${this.account.created ? 'alta' : 'entrada'} (${this.deps.profile.name}, ${city.civilization})`);
    this.publish(city);
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

  /**
   * La entrada del directorio de su ciudad, al día: dónde está y, para el blanco de
   * un asalto, su era, su inmunidad y su muralla (SPEC-BOTS-ASSAULT §4.2). Tras una
   * caída, la de la refundada (RN-BOT-017).
   */
  private publish(city: CityView): void {
    const playerId = this.account?.playerId;
    if (!playerId) return;
    const buildings = this.home ? [...this.home.store.getState().buildings.values()] : [];
    this.deps.directory.set({
      playerId,
      cityId: city.id,
      name: city.name,
      centerX: city.centerX,
      centerY: city.centerY,
      civilization: city.civilization,
      profile: this.deps.profile.name,
      era: city.era,
      ...(city.fallImmuneUntilMs !== undefined && city.fallImmuneUntilMs !== null
        ? { fallImmuneUntilMs: city.fallImmuneUntilMs }
        : {}),
      walled: walled(city, buildings),
    });
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
      treaties: hs.treaties,
      tradeOffers: hs.tradeOffers,
      market: hs.market,
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
      comercio: this.deps.comercio !== false,
      conHumanos: this.deps.conHumanos === true,
    });
    this.memory = memory;
    for (const cmd of commands) this.send(cmd, nowMs);

    // Una partida que termina y otra que sale en el mismo despertar: decide cierra la
    // vieja y abre la nueva sin pasar por null (lo encontró la prueba en vivo de E32).
    const replaced =
      hadRaid !== null &&
      memory.raid !== null &&
      ((hadRaid.departed && !memory.raid.departed) || hadRaid.kind !== memory.raid.kind);
    if (replaced) {
      this.emit('raid_end', { target: hadRaid.targetName, lost: hadRaid.lost, size: hadRaid.size });
    } else {
      this.logAssault(hadRaid, memory.raid);
    }
    if ((!hadRaid || replaced) && memory.raid) {
      this.emit('raid', {
        target: memory.raid.targetName,
        cityId: memory.raid.targetCityId,
        size: memory.raid.size,
        kind: memory.raid.kind,
        rams: memory.raid.rams.length,
      });
      this.deps.say(`${this.name}: sale una partida de ${memory.raid.size} contra ${memory.raid.targetName}`);
    }
    if (hadRaid && !memory.raid) this.emit('raid_end', { target: hadRaid.targetName, lost: hadRaid.lost, size: hadRaid.size });
    // La partida reunida (SPEC-BOTS-TRADE §4.7): pasa de la marcha a la orden de saquear.
    if (hadRaid?.phase === 'march' && memory.raid?.phase === 'strike' && memory.raid.targetCityId === hadRaid.targetCityId) {
      this.emit('rally', {
        target: memory.raid.targetName,
        warriors: memory.raid.gathered,
        alive: memory.raid.warriors.length,
        waitMs: memory.raid.rallyWaitMs,
      });
    }
    this.followRaid();
    for (const [id, p] of this.pending) if (nowMs - p.sentAt > 60_000) this.pending.delete(id);
  }

  /**
   * Lo que el registro apunta de una partida de asalto al pasar de un despertar al
   * siguiente (SPEC-BOTS-ASSAULT §4.7): reunida, brecha abierta, estandarte
   * plantado, asalto roto y vuelta, con su causa.
   */
  private logAssault(before: Raid | null, after: Raid | null): void {
    const r = after ?? before;
    if (!r || r.kind !== 'assault') return;
    const target = r.targetName;
    if (after && before && after.targetCityId === before.targetCityId) {
      if (before.phase === 'march' && after.phase === 'strike') {
        this.emit('assault', { status: 'rallied', target, warriors: after.gathered, rams: after.rams.length, waitMs: after.rallyWaitMs });
      }
      // Sólo si la partida la vio amurallada: una ciudad sin muralla no se abre.
      if (before.phase === 'strike' && before.stage === 'breach' && after.stage !== 'breach' && after.stage !== null && after.sawWall) {
        this.emit('assault', { status: 'breached', target, rams: after.rams.length });
      }
      if (after.planted > before.planted) this.emit('assault', { status: 'planted', target, holder: after.holder });
      if (after.breaks > before.breaks) this.emit('assault', { status: 'broken', target, cause: after.lastBreak });
      if (after.abandon !== null && before.abandon === null) {
        this.emit(
          'assault',
          after.abandon === 'fell' ? { status: 'fell', target } : { status: 'abandoned', target, cause: after.abandon },
        );
      }
    }
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
      case 'unit.assault':
        requestId = c.assaultCity(cmd.unitId, cmd.cityId);
        break;
      case 'city.build':
        requestId =
          cmd.building === 'HOUSE'
            ? c.buildHouse(cmd.cityId, cmd.tile)
            : cmd.building === 'WORKSHOP'
              ? c.buildWorkshop(cmd.cityId, cmd.tile)
              : cmd.building === 'MARKET'
                ? c.buildMarket(cmd.cityId, cmd.tile)
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
      case 'treaty.propose':
        requestId = c.proposeTreaty(cmd.playerId, cmd.treatyType);
        break;
      case 'treaty.respond':
        requestId = c.respondTreaty(cmd.treatyId, cmd.accept);
        break;
      case 'trade.propose':
        requestId = c.proposeTrade(cmd.playerId, [line(cmd.give)], [line(cmd.want)]);
        break;
      case 'trade.respond':
        requestId = c.respondTrade(cmd.offerId, cmd.accept);
        break;
      case 'market.post':
        requestId = c.postMarketOffer(line(cmd.give), line(cmd.want), cmd.lots);
        break;
      case 'market.buy':
        requestId = c.buyMarketOffer(cmd.offerId, cmd.lots);
        break;
      case 'market.cancel':
        requestId = c.cancelMarketOffer(cmd.offerId);
        break;
      case 'market.list':
        requestId = c.listMarket({ receive: cmd.receive as v1.ResourceType });
        break;
    }
    this.pending.set(requestId, { command: cmd, sentAt: nowMs });
    this.orderedAt.set(cmd.key, nowMs);
    this.emit('command', { type: cmd.type, key: cmd.key, requestId });
    this.logCommerceSent(cmd);
  }

  /** Lo que el registro apunta al mandar una orden de comercio (SPEC-BOTS-TRADE §4.7). */
  private logCommerceSent(cmd: Command): void {
    const name = (playerId: string) => this.deps.directory.list().find((b) => b.playerId === playerId)?.name ?? playerId;
    switch (cmd.type) {
      case 'treaty.propose':
        this.emit('treaty', { with: name(cmd.playerId), type: cmd.treatyType, status: 'proposed' });
        break;
      case 'trade.propose':
        this.emit('trade', { with: name(cmd.playerId), give: [cmd.give], receive: [cmd.want], status: 'proposed' });
        break;
      default:
        break;
    }
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
        const before = this.memory.unreachablePlayers;
        this.memory = onRejected(this.memory, p.command, code, nowMs, this.deps.profile);
        this.emit('rejected', { type: p.command.type, key: p.command.key, code });
        // Una pareja sin ruta se apunta una vez: desde ahora queda apartada (RN-BOT-013).
        for (const playerId of this.memory.unreachablePlayers) {
          if (before.includes(playerId)) continue;
          const name = this.deps.directory.list().find((b) => b.playerId === playerId)?.name ?? playerId;
          this.emit('trade', { with: name, status: 'unreachable', code });
        }
      }
      return;
    }
    if (m.requestId) {
      const p = this.pending.get(m.requestId);
      this.pending.delete(m.requestId);
      if (p) this.onAccepted(p.command, m);
    }
    if (link === this.home) this.onCommerce(m);

    // Un guerrero de la partida que muere, lo vea la casa o la campaña.
    if (
      (m.type === 'entity.update' && m.payload.status === 'DEAD') ||
      (m.type === 'entity.despawn' && m.payload.reason === 'DEAD')
    ) {
      this.memory = onRaidLoss(this.memory, m.payload.id);
    }
    // Una ciudad que cae (SPEC-BOTS-ASSAULT §4.4, §4.6): la que asaltaba la partida, o la propia.
    if (m.type === 'city.fallen') {
      const before = this.memory.raid;
      this.memory = onTargetFell(this.memory, m.payload.cityId, m.payload.ownerPlayerId, nowMs, this.deps.profile);
      this.logAssault(before, this.memory.raid);
      if (m.payload.ownerPlayerId === this.account?.playerId && this.prevCity?.id === m.payload.cityId) {
        this.pendingFall = { cityId: m.payload.cityId, byPlayerId: m.payload.byPlayerId };
      }
    }
    if (link !== this.home) return;

    switch (m.type) {
      case 'world.snapshot': {
        // Un snapshot reemplaza la vista: su almacén es la nueva referencia, no recolección.
        const city = this.ownCity();
        const before = this.prevCity;
        // Primero la ciudad nueva: lo que hace onRefounded (buscar colinas) trae más snapshots.
        this.prevCity = city ?? before;
        if (city && before && city.id !== before.id) this.onRefounded(before, city);
        this.recordBuildings();
        break;
      }
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
        this.buildingType.set(id, b.type);
        if (b.status === 'COMPLETE' && before !== 'COMPLETE') {
          const since = this.orderedAt.get(`build:${b.type}`);
          this.emit('built', { building: b.type, ...(since ? { ms: nowMs - since } : {}) });
          if (b.type === 'WALL' && this.wallBreached) {
            this.wallBreached = false;
            this.emit('wall', { status: 'rebuilt' });
          }
          if (b.type === 'WALL') this.publish(city);
        }
        break;
      }
      case 'building.destroyed': {
        const city = this.ownCity();
        if (city && m.payload.cityId === city.id) {
          this.emit('house_lost', { buildingId: m.payload.id });
          // La muralla propia cae: la ciudad queda en brecha (SPEC-BOTS-ASSAULT §4.7).
          if (this.buildingType.get(m.payload.id) === 'WALL') {
            this.wallBreached = true;
            this.emit('wall', { status: 'breached' });
            this.publish(city);
          }
        }
        break;
      }
      case 'event_log.notice':
        this.onNotice(m.payload);
        break;
      default:
        break;
    }
  }

  /** Una orden de mercado que el servidor aceptó: el registro la apunta con su comisión. */
  private onAccepted(cmd: Command, m: v1.ServerMessage): void {
    if (m.type !== 'market.update') return;
    if (cmd.type === 'market.post') {
      this.emit('market', {
        status: 'posted',
        give: cmd.give,
        receive: cmd.want,
        lots: cmd.lots,
        commission: commission(cmd.give.amount) * cmd.lots,
      });
    } else if (cmd.type === 'market.buy') {
      this.emit('market', {
        status: 'bought',
        give: cmd.give,
        receive: cmd.receive,
        lots: cmd.lots,
        commission: commission(cmd.give.amount) * cmd.lots,
      });
    }
  }

  /**
   * Tratados, tratos, ofertas del mercado y caravanas, tal como llegan
   * (SPEC-BOTS-TRADE §4.7). Cada cambio de estado se apunta una vez.
   */
  private onCommerce(m: v1.ServerMessage): void {
    const playerId = this.account?.playerId;
    switch (m.type) {
      case 'treaty.update': {
        const t = m.payload.treaty;
        const before = this.seenStatus.get(`treaty:${t.id}`);
        this.seenStatus.set(`treaty:${t.id}`, t.status);
        const status =
          t.status === 'ACTIVE' && before !== 'ACTIVE'
            ? 'accepted'
            : t.status === 'REJECTED'
              ? 'rejected'
              : t.status === 'EXPIRED'
                ? // Un tratado vigente sólo pasa a EXPIRED al sustituirlo el del escalón superior.
                  before === 'ACTIVE'
                  ? 'replaced'
                  : 'expired'
                : null;
        if (status && before !== t.status) {
          this.emit('treaty', { with: t.otherCityName ?? t.otherPlayerId, type: t.type, status, proposedByMe: t.proposedByMe });
        }
        break;
      }
      case 'trade.update': {
        const o = m.payload.offer;
        const status = o.status === 'ACCEPTED' ? 'accepted' : o.status === 'REJECTED' ? 'rejected' : o.status === 'EXPIRED' ? 'expired' : null;
        const key = `trade:${o.id}`;
        if (status && this.seenStatus.get(key) !== o.status) {
          this.seenStatus.set(key, o.status);
          this.emit('trade', {
            with: o.otherCityName ?? o.otherPlayerId,
            give: o.give,
            receive: o.get,
            status,
            proposedByMe: o.proposedByMe,
          });
        }
        break;
      }
      case 'market.update': {
        const o = m.payload.offer;
        if (o.playerId !== playerId) break;
        const sold = o.lotsSold - (this.lotsSold.get(o.id) ?? 0);
        this.lotsSold.set(o.id, o.lotsSold);
        if (sold > 0) {
          this.emit('market', { status: 'sold', give: o.give, receive: o.want, lots: sold, commission: 0 });
        }
        if (o.status === 'EXPIRED' || o.status === 'WITHDRAWN') {
          this.emit('market', {
            status: o.status === 'EXPIRED' ? 'expired' : 'cancelled',
            give: o.give,
            receive: o.want,
            lots: o.lotsLeft,
            commission: 0,
          });
        }
        break;
      }
      case 'caravan.update': {
        const c = m.payload.caravan;
        const city = this.ownCity();
        const key = `caravan:${c.id}`;
        let status: string | null = null;
        if (c.status === 'DELIVERED' && city && c.toCityId === city.id) status = 'arrived';
        else if (c.status === 'LOST') status = 'lost';
        else if (c.ownerPlayerId !== c.senderPlayerId && c.senderPlayerId === playerId) status = 'captured';
        if (status && this.seenStatus.get(key) !== status) {
          this.seenStatus.set(key, status);
          this.emit('caravan', { status, cargo: c.cargo, from: c.fromCityName ?? c.fromCityId, to: c.toCityName ?? c.toCityId });
        }
        break;
      }
      default:
        break;
    }
  }

  /** Lo que ya está en pie al conectar o reconectar no es una obra nueva. */
  private recordBuildings(): void {
    const city = this.ownCity();
    if (!city || !this.home) return;
    for (const b of this.home.store.getState().buildings.values()) {
      if (b.cityId !== city.id) continue;
      this.buildingStatus.set(b.id, b.status);
      this.buildingType.set(b.id, b.type);
    }
  }

  /**
   * La ciudad propia cayó y llega el snapshot de la refundada (ADR-050 §5): el bot
   * lo apunta, pone al día su entrada del directorio, olvida lo de la ciudad y
   * sigue jugando desde la nueva (RN-BOT-017, SPEC-BOTS-ASSAULT §4.6).
   */
  private onRefounded(fallen: CityView, city: CityView): void {
    const by = this.pendingFall?.byPlayerId ?? null;
    const byName = by ? (this.deps.directory.list().find((b) => b.playerId === by)?.name ?? by) : null;
    this.pendingFall = null;
    this.emit('fell', {
      cityId: fallen.id,
      newCityId: city.id,
      center: { x: city.centerX, y: city.centerY },
      by: byName,
      era: city.era,
      population: fallen.population,
    });
    this.deps.say(`${this.name}: su ciudad cayó${byName ? ` ante ${byName}` : ''}; refundada en (${city.centerX}, ${city.centerY})`);
    this.memory = onOwnCityFell(this.memory);
    this.buildingStatus.clear();
    this.buildingType.clear();
    this.wallBreached = false;
    this.forestTiles = [];
    this.fieldView = null;
    this.publish(city);
    // Las colinas de la casa vieja no sirven: se vuelven a buscar alrededor de la nueva.
    void this.scoutHills(city);
  }

  private onCity(nowMs: number): void {
    const city = this.ownCity();
    const prev = this.prevCity;
    if (!city) return;
    this.prevCity = city;
    if (!prev) return;
    if (city.era !== prev.era || city.fallImmuneUntilMs !== prev.fallImmuneUntilMs) this.publish(city);
    // El asalto sobre la ciudad propia termina sin caer (SPEC-BOTS-ASSAULT §4.7).
    if (city.id === prev.id && prev.assault && !city.assault) this.emit('assault', { status: 'repelled' });
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
      case 'CITY_ASSAULTED':
        // Lo ve el defensor (SPEC-BOTS-ASSAULT §4.7); la partida que asalta lo apunta en su lado.
        this.emit('assault', { status: 'suffered', by: other });
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
