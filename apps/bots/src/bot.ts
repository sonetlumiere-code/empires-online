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
import {
  createWorldStore,
  terrainAt,
  type BuildingView,
  type CityView,
  type ForestView,
  type TerritoryView,
  type UnitView,
} from '../../web/src/state/world';
import { TERRAIN_HILL } from '../../web/src/state/economy';
import { commission } from '../../web/src/state/market';
import { walled } from '../../web/src/state/cityFall';
import { maxOwnCities, ownCitiesOf } from '../../web/src/state/multiCity';
import { WebSession, type Account } from './auth';
import {
  EMPTY_MEMORY,
  STRAY_HOME_RADIUS,
  addStrays,
  chebyshev,
  decide,
  endFounding,
  fieldFocus,
  forestTilesAround,
  isMilitary,
  isRam,
  missingPopulation,
  onFounderLost,
  onOwnCityFell,
  onRaidLoss,
  onRejected,
  onTargetFell,
  sweepCenters,
  type Raid,
  type BotCity,
  type Command,
  type Founding,
  type Memory,
  type StrayResult,
  type Tile,
  type View,
} from './decide';
import type { RunLog } from './log';
import { colonyProfile, type Profile } from './profiles';
import { createRng, type Rng } from './random';

/** Las ciudades de los bots del proceso, para elegir blanco aunque no estén a la vista. */
export class Directory {
  private readonly cities = new Map<string, BotCity>();
  private readonly sites = new Map<string, Tile>();
  set(c: BotCity): void {
    this.cities.set(c.playerId, c);
  }
  list(): BotCity[] {
    return [...this.cities.values()].sort((a, b) => a.playerId.localeCompare(b.playerId));
  }
  /** El sitio que va a fundar el bot `playerId`, o `null` si no funda (SPEC-BOTS-FOUND §9.1). */
  claimSite(playerId: string, site: Tile | null): void {
    if (site) this.sites.set(playerId, site);
    else this.sites.delete(playerId);
  }
  /** Los sitios que van a fundar los demás bots, en orden de playerId. */
  sitesBesides(playerId: string): Tile[] {
    return [...this.sites].filter(([id]) => id !== playerId).sort(([a], [b]) => a.localeCompare(b)).map(([, t]) => t);
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

/** Una ciudad propia que no es la capital y lo que el bot sabe de ella (SPEC-BOTS-ALL-CITIES §4.1). */
interface Colony {
  readonly cityId: number;
  /** La conexión que sólo mira su centro (RN-BOT-024). */
  readonly link: Link;
  linkView: Tile | null;
  memory: Memory;
  forestTiles: Tile[];
  /** Tal como se vio por última vez: `city.fallen` la quita del espejo antes de apuntarla. */
  last: CityView | null;
  /** De dónde salió (para el registro, §4.7). */
  readonly origin: 'founded' | 'taken' | 'start';
}

type Stocks = Record<string, number>;

/** El área de interés del servidor (`EO_INTEREST_RADIUS_CHUNKS`), para los cuadros del barrido. */
const SWEEP_RADIUS_CHUNKS = 2;

/** Una línea de recurso con el tipo del protocolo. */
/**
 * La ciudad con la que juega el bot: su capital, la propia de id menor
 * (SPEC-MULTI-CITY RN-CITY-033, Q10). Los bots no fundan, pero un jugador con dos
 * ciudades a la vista no debe cambiar de ciudad según el orden del store.
 */
export function capitalOf(cities: Iterable<CityView>, playerId: string): CityView | null {
  return ownCitiesOf(cities, playerId)[0] ?? null;
}

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
  /** Las órdenes en vuelo; `colony`, la ciudad que no es la capital que la dio, o `null`. */
  private readonly pending = new Map<string, { command: Command; sentAt: number; colony: number | null }>();
  /**
   * Los centros de las ciudades propias en pie que el bot ha visto, por id: la
   * capital es la de id menor aunque el espejo de la casa no la traiga
   * (SPEC-BOTS-FOUND §4.5, como `ownCityHomes` del cliente).
   */
  private readonly homes = new Map<number, Tile>();
  /**
   * Los territorios y los centros de ciudades ajenas que ha visto alguna conexión, como
   * se vieron por última vez: el espejo los olvida al mover la vista y el terreno no, así
   * que un sitio lejano parecía libre (la prueba en vivo de SPEC-BOTS-FOUND-SITE).
   */
  private readonly seenTerritories = new Map<number, TerritoryView>();
  private readonly seenCities = new Map<number, Tile>();
  /**
   * Cada ciudad propia que no es la capital, por id: su conexión, su memoria y lo que
   * juega (SPEC-BOTS-FOUND §4.4, §4.5; SPEC-BOTS-ALL-CITIES §4.1, §4.3).
   */
  private readonly colonies = new Map<number, Colony>();
  /** Las ciudades que fueron colonia alguna vez: la que pasa a capital no se refunda. */
  private readonly everColony = new Set<number>();
  /** Las ciudades que tomó el bot (`takenCityId`), para el origen de su colonia. */
  private readonly takenIds = new Set<number>();
  /** Las ciudades propias que cayeron: un espejo atrasado no las vuelve a traer. */
  private readonly fallenIds = new Set<number>();
  /** Unidad propia vista en la exploración o el barrido → de qué ciudad es y su tipo (SPEC-BOTS-ALL-CITIES §4.6). */
  private readonly seenUnits = new Map<number, { cityId: number | null; unitType: string }>();
  private sweeping = false;
  /** La casa pidió volver a la capital y aún no llegó un snapshot que la traiga. */
  private recentering = false;
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
    await this.sweep(city);
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
    for (const c of this.colonies.values()) c.link.client.disconnect();
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
    const capitalId = this.capitalId();
    if (capitalId === null) return capitalOf(this.home.store.getState().cities.values(), playerId);
    return this.cityById(capitalId);
  }

  /** La ciudad `id` tal como la trae alguna de las conexiones, la casa primero. */
  private cityById(id: number): CityView | null {
    for (const link of this.links()) {
      const c = link.store.getState().cities.get(id);
      if (c) return c;
    }
    return null;
  }

  /** La capital: la ciudad propia en pie de id menor que el bot conoce (RN-CITY-033). */
  private capitalId(): number | null {
    let min: number | null = null;
    for (const id of this.homes.keys()) if (min === null || id < min) min = id;
    return min;
  }

  /** Las ciudades propias en pie que no son la capital, en orden de id (SPEC-BOTS-ALL-CITIES §4.1). */
  private colonyIds(): number[] {
    const capital = this.capitalId();
    return [...this.homes.keys()].filter((id) => id !== capital).sort((a, b) => a - b);
  }

  /** Todas las conexiones abiertas: la casa, las colonias y la campaña. */
  private links(): Link[] {
    const out: Link[] = [];
    if (this.home) out.push(this.home);
    for (const c of [...this.colonies.values()].sort((a, b) => a.cityId - b.cityId)) out.push(c.link);
    if (this.field) out.push(this.field);
    return out;
  }

  /**
   * Apunta las ciudades propias que conoce el espejo de una conexión: las de su vista y
   * las de `ownCities` del snapshot, también fuera de la vista (SPEC-BOTS-ALL-CITIES
   * §4.1). Abre o cierra las colonias según lo que queda.
   */
  private refreshHomes(link: Link): void {
    const playerId = this.account?.playerId;
    if (!playerId) return;
    const st = link.store.getState();
    for (const [id, h] of st.ownCityHomes) if (!this.fallenIds.has(id)) this.homes.set(id, { x: h.x, y: h.y });
    for (const c of ownCitiesOf(st.cities.values(), playerId)) {
      if (!this.fallenIds.has(c.id)) this.homes.set(c.id, { x: c.centerX, y: c.centerY });
    }
    this.syncColonies();
  }

  /** Una colonia por cada ciudad propia que no es la capital; la que cae o pasa a capital se cierra (RN-BOT-024). */
  private syncColonies(): void {
    if (this.stopped) return;
    const ids = new Set(this.colonyIds());
    for (const id of [...this.colonies.keys()]) if (!ids.has(id)) this.closeColony(id);
    for (const id of ids) {
      if (this.colonies.has(id)) continue;
      const link = this.link();
      const origin = this.takenIds.has(id) ? 'taken' : this.startedAt === 0 ? 'start' : 'founded';
      this.colonies.set(id, { cityId: id, link, linkView: null, memory: EMPTY_MEMORY, forestTiles: [], last: null, origin });
      this.everColony.add(id);
      void link.client.connect();
    }
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
    for (const link of this.links()) {
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
      this.noteStrays();
      this.hills = this.knownHills(c);
      for (const r of [96, 160, 224]) {
        if (this.hills.length > 0 && chebyshev(c, this.hills[0] as Tile) <= r - 64) break;
        for (const [dx, dy] of [[1, 0], [1, 1], [0, 1], [-1, 1], [-1, 0], [-1, -1], [0, -1], [1, -1]] as const) {
          if (this.stopped) return;
          const at = { x: Math.min(max, Math.max(0, c.x + dx * r)), y: Math.min(max, Math.max(0, c.y + dy * r)) };
          const snap = home.waitFor((m) => m.type === 'world.snapshot', 15_000);
          home.client.setView(at);
          await snap.catch(() => undefined);
          this.noteStrays();
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

  /**
   * Lo que trae la vista de la exploración o del barrido (SPEC-BOTS-FOUND §9.1,
   * SPEC-BOTS-ALL-CITIES §4.6): cada unidad propia queda vista para la cuenta de lo
   * que falta, y las paradas lejos de su ciudad (aldeanos, y militares fuera de la
   * partida) se apuntan en la memoria de esa ciudad para que vuelvan.
   */
  private noteStrays(): void {
    const playerId = this.account?.playerId;
    const home = this.home;
    const capitalId = this.capitalId();
    if (!playerId || !home || capitalId === null) return;
    const far = new Map<number, UnitView[]>();
    for (const u of home.store.getState().units.values()) {
      if (u.playerId !== playerId || u.status === 'DEAD') continue;
      this.seenUnits.set(u.id, { cityId: u.cityId, unitType: u.unitType });
      const cityId = u.cityId ?? capitalId;
      const at = this.homes.get(cityId);
      if (!at || u.status !== 'IDLE' || u.garrisonCityId !== null) continue;
      if (u.unitType !== 'VILLAGER' && !isMilitary(u) && !isRam(u)) continue;
      if (chebyshev(u, at) <= STRAY_HOME_RADIUS) continue;
      far.set(cityId, [...(far.get(cityId) ?? []), u]);
    }
    for (const [cityId, units] of [...far].sort(([a], [b]) => a - b)) {
      const at = this.homes.get(cityId) ?? null;
      const colony = this.colonies.get(cityId);
      const memory = colony ? colony.memory : this.memory;
      const before = (memory.strays ?? []).length;
      const next = addStrays(memory, units, this.now(), at, this.deps.profile);
      if (colony) colony.memory = next;
      else this.memory = next;
      const added = (next.strays ?? []).length - before;
      if (added > 0) {
        this.emit('stray', {
          status: 'seen',
          units: added,
          cityId,
          near: { x: units[0]?.x, y: units[0]?.y },
          distance: at && units[0] ? chebyshev(units[0], at) : null,
          sweep: this.sweeping,
        });
      }
    }
  }

  /**
   * Lo que falta de la población de cada ciudad propia, con lo visto y lo que está a la
   * vista ahora (§4.6). `known` da la ciudad que el espejo ya no trae: al mover la vista
   * de la casa, la capital sale del área de interés (lo encontró la prueba en vivo).
   */
  private missingByCity(known: ReadonlyMap<number, CityView> = new Map()): Map<number, number> {
    const seen = new Map(this.seenUnits);
    for (const u of this.ownUnits()) seen.set(u.id, { cityId: u.cityId, unitType: u.unitType });
    const capitalId = this.capitalId();
    const out = new Map<number, number>();
    for (const id of this.homes.keys()) {
      const city = this.cityById(id) ?? known.get(id);
      if (!city) continue;
      const units = [...seen.values()].filter((u) => (u.cityId ?? capitalId) === id);
      const missing = missingPopulation(city, units);
      if (missing > 0) out.set(id, missing);
    }
    return out;
  }

  private missingTotal(known?: ReadonlyMap<number, CityView>): number {
    let n = 0;
    for (const v of this.missingByCity(known).values()) n += v;
    return n;
  }

  /** Espera a que la conexión de cada colonia traiga su ciudad, como mucho `timeoutMs`. */
  private async awaitColonyViews(timeoutMs: number): Promise<void> {
    await Promise.all(
      [...this.colonies.values()].map((c) =>
        c.link.store.getState().cities.has(c.cityId)
          ? Promise.resolve()
          : c.link
              .waitFor((m) => m.type === 'world.snapshot' && c.link.store.getState().cities.has(c.cityId), timeoutMs)
              .then(() => undefined)
              .catch(() => undefined),
      ),
    );
  }

  /**
   * El barrido (SPEC-BOTS-ALL-CITIES §4.6, RN-BOT-025): si tras la exploración a
   * alguna ciudad le faltan unidades, la casa mueve la vista por el mapa entero, del
   * cuadro más cercano a la capital al más lejano, hasta que no falte ninguna. Lo que
   * encuentra parado lejos vuelve a su ciudad.
   */
  private async sweep(city: CityView): Promise<void> {
    const home = this.home;
    const world = home?.store.getState().world;
    if (!home || !world || this.stopped) return;
    await this.awaitColonyViews(20_000);
    // Las ciudades tal como se ven antes de mover la vista: luego el espejo las olvida.
    const known = new Map<number, CityView>();
    for (const id of this.homes.keys()) {
      const c = this.cityById(id);
      if (c) known.set(id, c);
    }
    const missing = this.missingTotal(known);
    if (missing <= 0) return;
    const c = { x: city.centerX, y: city.centerY };
    this.scouting = true;
    this.sweeping = true;
    let views = 0;
    try {
      for (const at of sweepCenters({ width: world.width, height: world.height, chunkSize: world.chunkSize }, SWEEP_RADIUS_CHUNKS, c)) {
        if (this.stopped || this.missingTotal(known) <= 0) break;
        const snap = home.waitFor((m) => m.type === 'world.snapshot', 15_000);
        home.client.setView(at);
        await snap.catch(() => undefined);
        views++;
        this.noteStrays();
      }
    } finally {
      const back = home.waitFor((m) => m.type === 'world.snapshot', 15_000);
      home.client.setView(c);
      await back.catch(() => undefined);
      this.scouting = false;
      this.sweeping = false;
    }
    const left = Math.max(0, this.missingTotal(known));
    this.emit('sweep', { missing, views, found: missing - left, left });
    this.deps.say(`${this.name}: barrido de ${views} vistas; faltaban ${missing} de población, faltan ${left}`);
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

    // El terreno de todas las conexiones, la casa primero: el sitio de una fundación puede
    // ir junto a una colonia, que sólo ve su conexión (SPEC-BOTS-FOUND-SITE §4.1).
    const stores = this.links().map((l) => l.store.getState());
    const lookup = (x: number, y: number) => {
      for (const st of stores) {
        const t = terrainAt(st.terrain, chunkSize, x, y);
        if (t !== undefined) return t;
      }
      return undefined;
    };
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
    // La capital no da órdenes a las unidades de ninguna colonia (RN-BOT-022).
    const colonyIds = new Set(this.colonyIds());
    return {
      playerId,
      city,
      buildings: [...buildings.values()].filter((b) => b.cityId === city.id).sort(byId),
      units: this.ownUnits().filter((u) => u.cityId === null || !colonyIds.has(u.cityId)),
      foreignCities: [...cities.values()].filter((c) => c.ownerPlayerId !== playerId).sort(byId),
      foreignBuildings: [...buildings.values()].filter((b) => b.cityId !== city.id).sort(byId),
      terrainAt: lookup,
      forests: this.knownForests(stores),
      forestTiles: this.forestTiles,
      hills: this.hills,
      botCities: this.deps.directory.list(),
      claimedSites: this.deps.directory.sitesBesides(playerId),
      treaties: hs.treaties,
      tradeOffers: hs.tradeOffers,
      market: hs.market,
      ...this.ownCitiesFields(city.id),
      territories: this.knownTerritories(),
      rememberedCenters: this.rememberedCenters(playerId),
    };
  }

  /**
   * Los territorios que traen todas las conexiones, la casa primero: el sitio de una
   * fundación lejos de la capital lo ve la campaña o una colonia, no la casa (lo
   * encontró la prueba en vivo de SPEC-BOTS-ALL-CITIES: «sólo se funda en un territorio
   * libre» en el territorio de la propia colonia).
   */
  private knownTerritories(): TerritoryView[] {
    const now = new Map<number, TerritoryView>();
    for (const link of this.links()) {
      for (const t of link.store.getState().territories.values()) if (!now.has(t.id)) now.set(t.id, t);
    }
    // Lo que se ve ahora manda; lo demás, como se vio por última vez (SPEC-BOTS-FOUND-SITE §9.1).
    for (const [id, t] of now) this.seenTerritories.set(id, t);
    return [...this.seenTerritories.values()].sort((a, b) => a.id - b.id);
  }

  /** Los centros de las ciudades ajenas vistas que ya no trae ninguna conexión (SPEC-BOTS-FOUND-SITE §9.1). */
  private rememberedCenters(playerId: string): Tile[] {
    const visible = new Set<number>();
    for (const link of this.links()) {
      for (const c of link.store.getState().cities.values()) {
        if (c.ownerPlayerId === playerId) continue;
        visible.add(c.id);
        this.seenCities.set(c.id, { x: c.centerX, y: c.centerY });
      }
    }
    return [...this.seenCities].filter(([id]) => !visible.has(id)).sort(([a], [b]) => a - b).map(([, t]) => t);
  }

  /**
   * Los bosques que traen las conexiones, la casa primero (sólo hay más de un almacén con
   * colonias): el sitio junto a una colonia mira si su bosque está agotado.
   */
  private knownForests(stores: readonly ReturnType<Link['store']['getState']>[]): ReadonlyMap<string, ForestView> {
    const [first, ...rest] = stores;
    if (!first) return new Map();
    if (rest.length === 0) return first.forests;
    const out = new Map(first.forests);
    for (const st of rest) for (const [k, f] of st.forests) if (!out.has(k)) out.set(k, f);
    return out;
  }

  /** Cuántas ciudades propias conoce y los centros de las demás, con su id (SPEC-BOTS-FOUND §4.5). */
  private ownCitiesFields(cityId: number): Pick<View, 'ownCityCount' | 'otherOwnCenters'> {
    const others = [...this.homes]
      .filter(([id]) => id !== cityId)
      .sort(([a], [b]) => a - b)
      .map(([id, t]) => ({ id, x: t.x, y: t.y }));
    return { ownCityCount: Math.max(1, this.homes.size), otherOwnCenters: others };
  }

  /**
   * Lo que ve una colonia (SPEC-BOTS-FOUND §4.4, SPEC-BOTS-ALL-CITIES §4.1): su ciudad,
   * sus edificios, sus unidades y su bosque; sin colinas ni comercio (el perfil de
   * colonia no los usa). Sin `id`, la primera.
   */
  buildColonyView(id: number | undefined = this.colonyIds()[0]): View | null {
    const playerId = this.account?.playerId;
    const colony = id === undefined ? undefined : this.colonies.get(id);
    if (!playerId || id === undefined || !colony || !this.home) return null;
    const city = this.cityById(id);
    if (!city) return null;
    colony.last = city;
    const stores = [this.home, colony.link].map((l) => l.store.getState());
    const chunkSize = stores[0]?.world?.chunkSize ?? 32;
    const lookup = (x: number, y: number) => {
      for (const st of stores) {
        const t = terrainAt(st.terrain, chunkSize, x, y);
        if (t !== undefined) return t;
      }
      return undefined;
    };
    if (colony.forestTiles.length === 0) {
      colony.forestTiles = forestTilesAround({ x: city.centerX, y: city.centerY }, 40, lookup).slice(0, 48);
    }
    const cities = new Map<number, CityView>();
    const buildings = new Map<number, BuildingView>();
    const forests = new Map<string, ForestView>();
    for (const st of stores) {
      for (const c of st.cities.values()) cities.set(c.id, c);
      for (const b of st.buildings.values()) buildings.set(b.id, b);
      for (const [k, f] of st.forests) forests.set(k, f);
    }
    const byId = <T extends { id: number }>(a: T, b: T) => a.id - b.id;
    const hs = this.home.store.getState();
    return {
      playerId,
      city,
      buildings: [...buildings.values()].filter((b) => b.cityId === id).sort(byId),
      units: this.ownUnits().filter((u) => u.cityId === id),
      foreignCities: [...cities.values()].filter((c) => c.ownerPlayerId !== playerId).sort(byId),
      foreignBuildings: [...buildings.values()].filter((b) => b.cityId !== id).sort(byId),
      terrainAt: lookup,
      forests,
      forestTiles: colony.forestTiles,
      hills: [],
      botCities: this.deps.directory.list(),
      claimedSites: this.deps.directory.sitesBesides(playerId),
      treaties: hs.treaties,
      tradeOffers: hs.tradeOffers,
      market: hs.market,
      ...this.ownCitiesFields(id),
    };
  }

  private think(): void {
    if (this.stopped || this.scouting || !this.home?.ready) return;
    const view = this.buildView();
    if (!view) return;
    const nowMs = this.now();
    const hadRaid = this.memory.raid;
    const hadFounding = this.memory.founding;
    const hadSearchAt = this.memory.foundSearchAtMs;
    const { commands, memory } = decide(view, this.deps.profile, this.memory, nowMs, this.rng, {
      contraHumanos: this.deps.contraHumanos,
      comercio: this.deps.comercio !== false,
      conHumanos: this.deps.conHumanos === true,
    });
    this.memory = memory;
    this.logStrays(null);
    for (const cmd of commands) this.send(cmd, nowMs);
    this.deps.directory.claimSite(view.playerId, memory.founding?.site ?? null);
    this.logFounding(hadFounding, memory);
    // Sin sitio a la vista: se apunta cada vez que la búsqueda se aplaza (SPEC-BOTS-FOUND §9.1).
    if (memory.foundSearchAtMs > hadSearchAt) this.emit('found', { status: 'no_site' });
    this.thinkColonies(nowMs);

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

  /**
   * La conexión de campaña mira donde está la partida; se abre con la primera. Sin nada
   * fuera de casa en la capital, mira el primer varado de una colonia
   * (SPEC-BOTS-ALL-CITIES §4.6).
   */
  private followRaid(): void {
    const focus =
      fieldFocus(this.memory) ??
      [...this.colonies.values()].sort((a, b) => a.cityId - b.cityId).map((c) => c.memory.strays?.[0]?.at).find((t) => t !== undefined) ??
      null;
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

  /**
   * Cada colonia juega con su memoria, el perfil de colonia y su propio tope de
   * comandos, en orden de id (SPEC-BOTS-ALL-CITIES §4.1, §4.4; RN-BOT-022), y su
   * conexión mira hacia ella (§4.3).
   */
  private thinkColonies(nowMs: number): void {
    this.syncColonies();
    for (const id of this.colonyIds()) {
      const colony = this.colonies.get(id);
      if (!colony) continue;
      this.followColony(colony);
      const view = this.buildColonyView(id);
      if (!view) continue;
      const { commands, memory } = decide(view, colonyProfile(this.deps.profile), colony.memory, nowMs, this.rng, {
        contraHumanos: false,
        comercio: false,
      });
      colony.memory = memory;
      this.logStrays(colony);
      for (const cmd of commands) this.send(cmd, nowMs, id);
    }
  }

  /** La conexión de una colonia mira su centro en cuanto está lista. */
  private followColony(colony: Colony): void {
    const at = this.homes.get(colony.cityId);
    if (!at || !colony.link.ready) return;
    if (colony.linkView && colony.linkView.x === at.x && colony.linkView.y === at.y) return;
    // Antes de pedirla: el snapshot de la respuesta vuelve a pasar por aquí.
    colony.linkView = at;
    colony.link.client.setView(at);
  }

  private closeColony(id: number): void {
    const colony = this.colonies.get(id);
    if (!colony) return;
    colony.link.client.disconnect();
    this.colonies.delete(id);
  }

  /**
   * Apunta cómo terminó la vuelta de los varados de la capital (`null`) o de una
   * colonia, y lo vacía (SPEC-BOTS-ALL-CITIES §4.7).
   */
  private logStrays(colony: Colony | null): void {
    const memory = colony ? colony.memory : this.memory;
    const results: readonly StrayResult[] = memory.strayResults ?? [];
    if (results.length === 0) return;
    const cityId = colony ? colony.cityId : this.capitalId();
    for (const r of results) {
      this.emit('stray', { status: r.status, unitId: r.unitId, cityId, ms: r.ms, ...(r.cause ? { cause: r.cause } : {}) });
    }
    const cleared = { ...memory, strayResults: [] };
    if (colony) colony.memory = cleared;
    else this.memory = cleared;
  }

  /** Lo que el registro apunta de una fundación al empezar y al terminar (SPEC-BOTS-FOUND §4.7). */
  private logFounding(before: Founding | null, after: Memory): void {
    const f = after.founding;
    if (f && (before === null || before.startedAtMs !== f.startedAtMs)) {
      // La ciudad que funda (2, 3 o 4) y la era de la capital (SPEC-BOTS-ALL-CITIES §4.7).
      const ordinal = (f.cities ?? 1) + 1;
      // `from`: la ciudad de cuyo anillo salió el sitio (SPEC-BOTS-FOUND-SITE §4.4).
      this.emit('found', {
        status: 'started',
        unitId: f.unitId,
        site: f.site,
        distance: f.distance,
        ordinal,
        era: this.ownCity()?.era ?? null,
        from: f.from ?? null,
        fromCapital: f.from === null || f.from === undefined || f.from === this.ownCity()?.id,
      });
      this.deps.say(`${this.name}: un aldeano sale a fundar en (${f.site.x}, ${f.site.y}), a ${f.distance} tiles`);
    }
    if (before && (f === null || f.startedAtMs !== before.startedAtMs) && after.foundResult) {
      const r = after.foundResult;
      if (r.status === 'founded') {
        // La fundada es la de id mayor: la fila nueva.
        const id = this.colonyIds().at(-1) ?? null;
        const at = id === null ? null : this.homes.get(id);
        const ordinal = (before.cities ?? 1) + 1;
        this.emit('found', { status: 'founded', cityId: id, center: at ?? r.site, ms: r.ms, ordinal });
        this.deps.say(`${this.name}: funda su ciudad número ${ordinal} en (${(at ?? r.site).x}, ${(at ?? r.site).y})`);
      } else {
        this.emit('found', { status: 'failed', cause: r.cause, ...(r.code ? { code: r.code } : {}), site: r.site, ms: r.ms });
      }
    }
  }

  private send(cmd: Command, nowMs: number, colony: number | null = null): void {
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
      case 'unit.found_city':
        requestId = c.foundCity(cmd.unitId);
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
    this.pending.set(requestId, { command: cmd, sentAt: nowMs, colony });
    // Las claves de cada colonia llevan su prefijo: «build:HOUSE» es de cada ciudad.
    const key = colony === null ? cmd.key : `colony:${colony}:${cmd.key}`;
    this.orderedAt.set(key, nowMs);
    this.emit('command', { type: cmd.type, key, requestId });
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
        if (p.colony !== null) {
          const colony = this.colonies.get(p.colony);
          this.emit('rejected', { type: p.command.type, key: `colony:${p.colony}:${p.command.key}`, code });
          if (colony) {
            colony.memory = onRejected(colony.memory, p.command, code, nowMs, colonyProfile(this.deps.profile));
            this.logStrays(colony);
          }
          return;
        }
        const before = this.memory.unreachablePlayers;
        const hadFounding = this.memory.founding;
        this.memory = onRejected(this.memory, p.command, code, nowMs, this.deps.profile);
        this.emit('rejected', { type: p.command.type, key: p.command.key, code });
        this.logStrays(null);
        this.logFounding(hadFounding, this.memory);
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
      const hadFounding = this.memory.founding;
      this.memory = onFounderLost(this.memory, m.payload.id, nowMs, this.deps.profile);
      this.logFounding(hadFounding, this.memory);
    }
    // Una ciudad que cae (SPEC-BOTS-ASSAULT §4.4, §4.6): la que asaltaba la partida, o la propia.
    if (m.type === 'city.fallen') {
      // La caída ya no cuenta como vecina de un sitio.
      this.seenCities.delete(m.payload.cityId);
      const before = this.memory.raid;
      this.memory = onTargetFell(this.memory, m.payload.cityId, m.payload.ownerPlayerId, nowMs, this.deps.profile);
      this.logAssault(before, this.memory.raid);
      // La ciudad que toma el bot (E36): su colonia sale como «tomada» (SPEC-BOTS-ALL-CITIES §4.7).
      if (m.payload.byPlayerId === this.account?.playerId && m.payload.takenCityId !== undefined) {
        this.takenIds.add(m.payload.takenCityId);
      }
      if (m.payload.ownerPlayerId === this.account?.playerId) {
        if (this.prevCity?.id === m.payload.cityId) {
          this.pendingFall = { cityId: m.payload.cityId, byPlayerId: m.payload.byPlayerId };
        }
        // Cae una colonia y la capital sigue (SPEC-BOTS-FOUND §4.6, ahora con cualquiera).
        if (this.colonies.has(m.payload.cityId) && m.payload.cityId !== this.capitalId()) {
          this.onColonyFell(m.payload.cityId, m.payload.byPlayerId);
        }
        this.homes.delete(m.payload.cityId);
        this.fallenIds.add(m.payload.cityId);
        this.syncColonies();
      }
    }
    if ((m.type === 'world.snapshot' || m.type === 'city.update') && link !== this.home) this.refreshHomes(link);
    // La conexión de una colonia mira su centro en cuanto llega su primer snapshot.
    if (m.type === 'world.snapshot') {
      for (const c of this.colonies.values()) if (c.link === link) this.followColony(c);
    }
    if (link !== this.home) return;

    switch (m.type) {
      case 'world.snapshot': {
        const known = this.homes.size;
        this.refreshHomes(link);
        // Una ciudad propia nueva (la fundación): la campaña vuelve a mirar su partida.
        if (this.homes.size > known && known > 0) this.fieldView = null;
        // El snapshot de la fundación llega centrado en la nueva (ResnapshotAt): la casa
        // vuelve a la capital (SPEC-BOTS-FOUND §4.5).
        const capitalId = this.capitalId();
        const capitalAt = capitalId === null ? undefined : this.homes.get(capitalId);
        // Una vez por vuelta: si el snapshot siguiente tampoco la trae, no se insiste.
        if (capitalId !== null && link.store.getState().cities.has(capitalId)) this.recentering = false;
        else if (!this.scouting && capitalAt && !this.recentering) {
          this.recentering = true;
          link.client.setView(capitalAt);
        }
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
        this.refreshHomes(link);
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
    // Con otra en pie, la que queda pasa a capital sin refundar (SPEC-BOTS-FOUND §4.6).
    const kept = this.everColony.has(city.id);
    this.emit('fell', {
      cityId: fallen.id,
      newCityId: city.id,
      center: { x: city.centerX, y: city.centerY },
      by: byName,
      era: city.era,
      population: fallen.population,
      kept,
      capital: true,
    });
    this.deps.say(
      kept
        ? `${this.name}: su capital cayó${byName ? ` ante ${byName}` : ''}; sigue desde ${city.name}`
        : `${this.name}: su ciudad cayó${byName ? ` ante ${byName}` : ''}; refundada en (${city.centerX}, ${city.centerY})`,
    );
    if (kept) this.closeColony(city.id);
    const hadFounding = this.memory.founding;
    this.memory = endFounding(this.memory, 'failed', 'city_fell', this.now(), this.deps.profile);
    this.logFounding(hadFounding, this.memory);
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

  /**
   * Cae la segunda ciudad y la capital sigue (SPEC-BOTS-FOUND §4.6): se apunta, se
   * cierra la conexión colonia y se olvida su memoria; la fundación vuelve cuando
   * se pueda (Q8).
   */
  private onColonyFell(cityId: number, byPlayerId: string): void {
    const fallen = this.cityById(cityId) ?? this.colonies.get(cityId)?.last ?? null;
    const byName = this.deps.directory.list().find((b) => b.playerId === byPlayerId)?.name ?? byPlayerId;
    this.emit('fell', {
      cityId,
      newCityId: this.capitalId(),
      by: byName,
      era: fallen?.era ?? null,
      population: fallen?.population ?? 0,
      kept: true,
      capital: false,
    });
    this.deps.say(`${this.name}: su ciudad ${fallen?.name ?? cityId} cayó ante ${byName}; sigue con la capital`);
    this.closeColony(cityId);
    // Una fundación en curso ahora termina bien con una ciudad menos (RN-BOT-023).
    const f = this.memory.founding;
    if (f) this.memory = { ...this.memory, founding: { ...f, cities: Math.max(1, (f.cities ?? 1) - 1) } };
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
    const cap = maxOwnCities(city.era);
    let played = 1;
    for (const id of this.colonyIds()) {
      const colony = this.colonies.get(id);
      const c = this.cityById(id) ?? colony?.last ?? null;
      // Juega si su conexión está lista y su vista trae la ciudad (SPEC-BOTS-ALL-CITIES §4.7).
      if (colony?.link.ready && colony.link.store.getState().cities.has(id)) played++;
      if (!colony || !c) continue;
      const houses = [this.home, colony.link]
        .flatMap((l) => (l ? [...l.store.getState().buildings.values()] : []))
        .filter((b, i, all) => b.cityId === c.id && b.type === 'HOUSE' && b.status === 'COMPLETE' && all.findIndex((x) => x.id === b.id) === i).length;
      const villagers = this.ownUnits().filter((u) => u.cityId === c.id && u.unitType === 'VILLAGER').length;
      this.emit('colony', {
        cityId: c.id,
        origin: colony.origin,
        era: c.era,
        population: c.population,
        populationLimit: c.populationLimit,
        villagers,
        wood: c.stocks.WOOD,
        houses,
        target: this.deps.profile.colonyVillagers,
      });
    }
    this.emit('cities', { standing: Math.max(1, this.homes.size), played, cap });
  }
}
