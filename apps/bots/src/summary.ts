/**
 * El resumen de una ejecución (SPEC-BOTS §4.5), calculado sólo a partir del JSONL:
 * se puede rehacer de un fichero viejo sin volver a jugar.
 */
import type { LogLine } from './log';

export interface BotSummary {
  readonly bot: string;
  readonly profile: string;
  readonly civilization: string;
  readonly playedMs: number;
  /** Era → ms desde el `start` del bot hasta llegar a ella. */
  readonly eras: Readonly<Record<string, number>>;
  readonly finalEra: string;
  /** Lo que entró en el almacén (recolección y botín), por hora jugada. */
  readonly gatheredPerHour: Readonly<Record<string, number>>;
  readonly trained: Readonly<Record<string, number>>;
  readonly built: Readonly<Record<string, number>>;
  readonly researched: number;
  readonly crafted: number;
  readonly lost: number;
  readonly killed: number;
  readonly pillageDone: number;
  readonly demolished: number;
  readonly pillagedHits: number;
  readonly besiegedHits: number;
  readonly housesLost: number;
  readonly raids: number;
  readonly commands: number;
  readonly rejected: Readonly<Record<string, number>>;
  // ── Comercio y partidas (SPEC-BOTS-TRADE §4.7) ──
  readonly treaties: number;
  readonly trades: number;
  /** Bienes de otras civilizaciones recibidos por trato y por el mercado. */
  readonly goodsByTrade: number;
  readonly goodsByMarket: number;
  /** Comisión pagada en el mercado, al publicar y al comprar. */
  readonly commission: number;
  readonly caravansLost: number;
  /** El balance de cada partida, en orden. */
  readonly raidLog: readonly RaidBalance[];
  // ── El asalto (SPEC-BOTS-ASSAULT §4.7) ──
  /** Estandartes plantados y ciudades tumbadas por este bot. */
  readonly assaultsPlanted: number;
  readonly citiesTaken: number;
  /** Asaltos propios rotos, por causa, y partidas de asalto que volvieron sin la caída, por causa. */
  readonly assaultBreaks: Readonly<Record<string, number>>;
  readonly assaultAbandons: Readonly<Record<string, number>>;
  /** Como defensor: asaltos sufridos y rechazados (terminaron sin caer). */
  readonly assaultsSuffered: number;
  readonly assaultsRepelled: number;
  readonly wallsBreached: number;
  /** Cada caída de su ciudad, con lo que tardó en recuperarse. */
  readonly falls: readonly FallRecovery[];
  // ── La fundación (SPEC-BOTS-FOUND §4.7) ──
  readonly foundsStarted: number;
  /** De ellas, las que salieron del anillo de una colonia (SPEC-BOTS-FOUND-SITE §4.4). */
  readonly foundsFromColony: number;
  /** Fundaciones que terminaron mal, por causa. */
  readonly foundFailures: Readonly<Record<string, number>>;
  /** Cada colonia (fundada, tomada o que ya estaba al arrancar), con lo que tardó en crecer. */
  readonly colonies: readonly ColonyGrowth[];
  // ── Todas las ciudades (SPEC-BOTS-ALL-CITIES §4.7) ──
  /** Ciudades propias en pie en la última línea `cities` y el máximo. */
  readonly citiesFinal: number;
  readonly citiesMax: number;
  /** Minutos (líneas `cities`) con alguna ciudad en pie sin jugar: el número que tiene que ser cero. */
  readonly unplayedMinutes: number;
  /** El barrido del arranque (`null`: no hizo falta). */
  readonly sweep: { readonly missing: number; readonly views: number; readonly found: number } | null;
  /** Varados vistos (y de ellos, los que trajo el barrido), de vuelta y perdidos por causa. */
  readonly straysSeen: number;
  readonly straysSwept: number;
  readonly straysHome: number;
  readonly straysLost: Readonly<Record<string, number>>;
}

export interface ColonyGrowth {
  readonly cityId: number | null;
  /** De dónde salió: `founded`, `taken` o `start` (ya estaba al arrancar). */
  readonly origin: string;
  /** La ciudad que fue al fundarla (2, 3 o 4); `null` si no se fundó en esta ejecución. */
  readonly ordinal: number | null;
  /**
   * Ms desde el `start` del bot hasta fundarla (o verla por primera vez), y desde que la
   * capital llegó a la era que la permite (Bronce para la segunda, Hierro para la
   * tercera, Castillos para la cuarta; desde el arranque si ya estaba).
   */
  readonly foundedMs: number;
  readonly fromEraMs: number;
  /** Ms desde la fundación (o la primera línea `colony`) hasta sus aldeanos objetivo (`null`: no llegó). */
  readonly grownMs: number | null;
  /** Su población en la última línea `colony`, y si cayó. */
  readonly finalPopulation: number | null;
  /** Madera neta del almacén por hora, entre la primera y la última línea `colony`. */
  readonly woodPerHour: number | null;
  readonly fell: boolean;
}

/** La era de la capital que permite fundar la ciudad número `ordinal` (RN-CITY-040). */
const ERA_FOR_ORDINAL: Readonly<Record<number, string>> = { 2: 'BRONZE_AGE', 3: 'IRON_AGE', 4: 'CASTLE_AGE' };

export interface FallRecovery {
  readonly by: string | null;
  readonly era: string;
  /** Quedaba otra en pie (SPEC-BOTS-FOUND §4.6) y si la caída era la capital. */
  readonly kept: boolean;
  readonly capital: boolean;
  /** Ms desde la caída hasta volver a tener muralla, la población de antes y la era siguiente (`null`: no llegó). */
  readonly wallMs: number | null;
  readonly populationMs: number | null;
  readonly nextEraMs: number | null;
}

export interface RaidBalance {
  readonly target: string;
  /** Guerreros reunidos al dar la orden de saquear (`null` si no llegó a reunirse). */
  readonly gathered: number | null;
  readonly rallyWaitMs: number | null;
  readonly lost: number;
  readonly killed: number;
  readonly wood: number;
  /** De asalto o de saqueo; en la de asalto, los arietes perdidos y cómo acabó. */
  readonly kind: string;
  readonly ramsLost: number;
  /** `fell` o la causa de la vuelta; `null` si no se supo. */
  readonly outcome: string | null;
  /** Ms desde la salida hasta la caída. */
  readonly msToFall: number | null;
}

type Line = { resource?: unknown; amount?: unknown };

/** Lo que suman unas líneas de bienes (todo lo que no es madera ni piedra). */
function goodsIn(lines: unknown, lots = 1): number {
  const list = Array.isArray(lines) ? (lines as Line[]) : lines && typeof lines === 'object' ? [lines as Line] : [];
  let n = 0;
  for (const l of list) {
    if (l.resource === 'WOOD' || l.resource === 'STONE' || typeof l.amount !== 'number') continue;
    n += l.amount * lots;
  }
  return n;
}

function inc(r: Record<string, number>, k: string, n = 1): void {
  r[k] = (r[k] ?? 0) + n;
}

export function summarize(lines: readonly LogLine[]): BotSummary[] {
  const byBot = new Map<string, LogLine[]>();
  for (const l of lines) {
    const list = byBot.get(l.bot) ?? [];
    list.push(l);
    byBot.set(l.bot, list);
  }
  const out: BotSummary[] = [];
  for (const [bot, ls] of [...byBot.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    const start = ls.find((l) => l.event === 'start');
    if (!start) continue;
    const stop = [...ls].reverse().find((l) => l.event === 'stop');
    const lastT = stop?.t ?? ls[ls.length - 1]?.t ?? start.t;
    const playedMs = Math.max(0, lastT - start.t);
    const eras: Record<string, number> = { [String(start.era)]: 0 };
    const trained: Record<string, number> = {};
    const built: Record<string, number> = {};
    const rejected: Record<string, number> = {};
    let gathered: Record<string, number> = {};
    let finalEra = String(start.era);
    let researched = 0;
    let crafted = 0;
    let lost = 0;
    let killed = 0;
    let pillageDone = 0;
    let demolished = 0;
    let pillagedHits = 0;
    let besiegedHits = 0;
    let housesLost = 0;
    let raids = 0;
    let commands = 0;
    let treaties = 0;
    let trades = 0;
    let goodsByTrade = 0;
    let goodsByMarket = 0;
    let commission = 0;
    let caravansLost = 0;
    const raidLog: RaidBalance[] = [];
    let current: {
      target: string;
      gathered: number | null;
      rallyWaitMs: number | null;
      lost: number;
      killed: number;
      wood: number;
      kind: string;
      ramsLost: number;
      outcome: string | null;
      msToFall: number | null;
      startT: number;
    } | null = null;
    let assaultsPlanted = 0;
    let citiesTaken = 0;
    const assaultBreaks: Record<string, number> = {};
    const assaultAbandons: Record<string, number> = {};
    let assaultsSuffered = 0;
    let assaultsRepelled = 0;
    let wallsBreached = 0;
    const falls: ({ -readonly [K in keyof FallRecovery]: FallRecovery[K] } & { t: number; population: number })[] = [];
    let foundsStarted = 0;
    let foundsFromColony = 0;
    const foundFailures: Record<string, number> = {};
    const colonies: ({ -readonly [K in keyof ColonyGrowth]: ColonyGrowth[K] } & {
      t: number;
      firstWood: { t: number; wood: number } | null;
      lastWood: { t: number; wood: number } | null;
    })[] = [];
    let citiesFinal = 1;
    let citiesMax = 1;
    let unplayedMinutes = 0;
    let sweep: BotSummary['sweep'] = null;
    let straysSeen = 0;
    let straysSwept = 0;
    let straysHome = 0;
    const straysLost: Record<string, number> = {};
    const colonyOf = (id: unknown) => colonies.find((c) => c.cityId === id && !c.fell);
    const close = (c: NonNullable<typeof current>): RaidBalance => {
      const { startT: _t, ...rest } = c;
      return rest;
    };
    for (const l of ls) {
      switch (l.event) {
        case 'era':
          eras[String(l.era)] = l.t - start.t;
          finalEra = String(l.era);
          for (const f of falls) if (f.nextEraMs === null) f.nextEraMs = l.t - f.t;
          break;
        case 'stock':
        case 'stop':
          if (l.gathered && typeof l.gathered === 'object') gathered = { ...(l.gathered as Record<string, number>) };
          for (const f of falls) {
            if (f.populationMs === null && typeof l.population === 'number' && l.population >= f.population) {
              f.populationMs = l.t - f.t;
            }
          }
          if (l.event === 'stop' && typeof l.era === 'string') finalEra = l.era;
          break;
        case 'trained':
          inc(trained, String(l.unitType));
          break;
        case 'built':
          inc(built, String(l.building));
          if (l.building === 'WALL') for (const f of falls) if (f.wallMs === null) f.wallMs = l.t - f.t;
          break;
        case 'researched':
          researched++;
          break;
        case 'crafted':
          crafted++;
          break;
        case 'lost':
          lost++;
          if (current) {
            current.lost++;
            if (l.unitType === 'RAM') current.ramsLost++;
          }
          break;
        case 'killed':
          killed++;
          if (current) current.killed++;
          break;
        case 'pillage_done':
          pillageDone += typeof l.amount === 'number' ? l.amount : 0;
          if (current) current.wood += typeof l.amount === 'number' ? l.amount : 0;
          break;
        case 'demolished':
          demolished++;
          break;
        case 'pillaged':
          pillagedHits++;
          break;
        case 'besieged':
          besiegedHits++;
          break;
        case 'house_lost':
          housesLost++;
          break;
        case 'raid':
          raids++;
          if (current) raidLog.push(close(current));
          current = {
            target: String(l.target),
            gathered: null,
            rallyWaitMs: null,
            lost: 0,
            killed: 0,
            wood: 0,
            kind: typeof l.kind === 'string' ? l.kind : 'pillage',
            ramsLost: 0,
            outcome: null,
            msToFall: null,
            startT: l.t,
          };
          break;
        case 'rally':
          if (current) {
            current.gathered = typeof l.warriors === 'number' ? l.warriors : null;
            current.rallyWaitMs = typeof l.waitMs === 'number' ? l.waitMs : null;
          }
          break;
        case 'raid_end':
          if (current) raidLog.push(close(current));
          current = null;
          break;
        case 'assault':
          switch (l.status) {
            case 'planted':
              assaultsPlanted++;
              break;
            case 'broken':
              inc(assaultBreaks, String(l.cause));
              break;
            case 'fell':
              citiesTaken++;
              if (current) {
                current.outcome = 'fell';
                current.msToFall = l.t - current.startT;
              }
              break;
            case 'abandoned':
              inc(assaultAbandons, String(l.cause));
              if (current) current.outcome = String(l.cause);
              break;
            case 'suffered':
              assaultsSuffered++;
              break;
            case 'repelled':
              assaultsRepelled++;
              break;
            default:
              break;
          }
          break;
        case 'wall':
          if (l.status === 'breached') wallsBreached++;
          break;
        case 'found':
          if (l.status === 'started') foundsStarted++;
          if (l.status === 'started' && l.fromCapital === false) foundsFromColony++;
          if (l.status === 'no_site') inc(foundFailures, 'no_site');
          if (l.status === 'failed') inc(foundFailures, String(l.cause));
          if (l.status === 'founded') {
            const ordinal = typeof l.ordinal === 'number' ? l.ordinal : 2;
            const era = eras[ERA_FOR_ORDINAL[ordinal] ?? 'BRONZE_AGE'] ?? 0;
            // La primera línea `colony` puede llegar antes que su `founded` (la prueba en vivo
            // de E42 dio la 70 dos veces): es la misma colonia.
            const early = colonyOf(l.cityId);
            if (early) {
              Object.assign(early, { origin: 'founded', ordinal, foundedMs: l.t - start.t, fromEraMs: l.t - start.t - era });
              break;
            }
            colonies.push({
              t: l.t,
              cityId: typeof l.cityId === 'number' ? l.cityId : null,
              origin: 'founded',
              ordinal,
              foundedMs: l.t - start.t,
              fromEraMs: l.t - start.t - era,
              grownMs: null,
              finalPopulation: null,
              woodPerHour: null,
              fell: false,
              firstWood: null,
              lastWood: null,
            });
          }
          break;
        case 'colony': {
          let c = colonyOf(l.cityId);
          // Una tomada o la que ya estaba al arrancar: empieza en su primera línea.
          if (!c && typeof l.cityId === 'number') {
            c = {
              t: l.t,
              cityId: l.cityId,
              origin: typeof l.origin === 'string' ? l.origin : 'start',
              ordinal: null,
              foundedMs: l.t - start.t,
              fromEraMs: l.t - start.t,
              grownMs: null,
              finalPopulation: null,
              woodPerHour: null,
              fell: false,
              firstWood: null,
              lastWood: null,
            };
            colonies.push(c);
          }
          if (!c) break;
          if (typeof l.population === 'number') c.finalPopulation = l.population;
          // Los aldeanos (desde E41) o, en un registro viejo, la población.
          const grown = typeof l.villagers === 'number' ? l.villagers : l.population;
          const target = typeof l.target === 'number' ? l.target : 8;
          if (c.grownMs === null && typeof grown === 'number' && grown >= target) c.grownMs = l.t - c.t;
          if (typeof l.wood === 'number') {
            c.firstWood ??= { t: l.t, wood: l.wood };
            c.lastWood = { t: l.t, wood: l.wood };
          }
          break;
        }
        case 'cities':
          if (typeof l.standing === 'number') {
            citiesFinal = l.standing;
            citiesMax = Math.max(citiesMax, l.standing);
            if (typeof l.played === 'number' && l.played < l.standing) unplayedMinutes++;
          }
          break;
        case 'sweep':
          sweep = {
            missing: typeof l.missing === 'number' ? l.missing : 0,
            views: typeof l.views === 'number' ? l.views : 0,
            found: typeof l.found === 'number' ? l.found : 0,
          };
          break;
        case 'stray':
          if (l.status === 'seen') {
            const n = typeof l.units === 'number' ? l.units : 1;
            straysSeen += n;
            if (l.sweep === true) straysSwept += n;
          }
          if (l.status === 'home') straysHome++;
          if (l.status === 'lost') inc(straysLost, String(l.cause));
          break;
        case 'fell':
          if (l.kept === true && l.capital === false) {
            const c = colonyOf(l.cityId);
            if (c) c.fell = true;
          }
          falls.push({
            t: l.t,
            by: typeof l.by === 'string' ? l.by : null,
            era: String(l.era),
            kept: l.kept === true,
            capital: l.capital !== false,
            population: typeof l.population === 'number' ? l.population : 0,
            wallMs: null,
            populationMs: null,
            nextEraMs: null,
          });
          break;
        case 'treaty':
          if (l.status === 'accepted') treaties++;
          break;
        case 'trade':
          if (l.status === 'accepted') {
            trades++;
            goodsByTrade += goodsIn(l.receive);
          }
          break;
        case 'market':
          if (l.status === 'bought') goodsByMarket += goodsIn(l.receive, typeof l.lots === 'number' ? l.lots : 1);
          if (l.status === 'posted' || l.status === 'bought') commission += typeof l.commission === 'number' ? l.commission : 0;
          break;
        case 'caravan':
          if (l.status === 'lost' || l.status === 'captured') caravansLost++;
          break;
        case 'command':
          commands++;
          break;
        case 'rejected':
          inc(rejected, String(l.code));
          break;
        default:
          break;
      }
    }
    if (current) raidLog.push(close(current));
    const hours = playedMs / 3_600_000;
    const gatheredPerHour: Record<string, number> = {};
    for (const [r, n] of Object.entries(gathered)) gatheredPerHour[r] = hours > 0 ? Math.round(n / hours) : 0;
    out.push({
      bot,
      profile: String(start.profile),
      civilization: String(start.civilization),
      playedMs,
      eras,
      finalEra,
      gatheredPerHour,
      trained,
      built,
      researched,
      crafted,
      lost,
      killed,
      pillageDone,
      demolished,
      pillagedHits,
      besiegedHits,
      housesLost,
      raids,
      commands,
      rejected,
      treaties,
      trades,
      goodsByTrade,
      goodsByMarket,
      commission,
      caravansLost,
      raidLog,
      assaultsPlanted,
      citiesTaken,
      assaultBreaks,
      assaultAbandons,
      assaultsSuffered,
      assaultsRepelled,
      wallsBreached,
      falls: falls.map(({ t: _t, population: _p, ...f }) => f),
      foundsStarted,
      foundsFromColony,
      foundFailures,
      colonies: colonies.map(({ t: _t, firstWood, lastWood, ...c }) => {
        const h = firstWood && lastWood ? (lastWood.t - firstWood.t) / 3_600_000 : 0;
        return { ...c, woodPerHour: firstWood && lastWood && h > 0 ? Math.round((lastWood.wood - firstWood.wood) / h) : null };
      }),
      citiesFinal,
      citiesMax,
      unplayedMinutes,
      sweep,
      straysSeen,
      straysSwept,
      straysHome,
      straysLost,
    });
  }
  return out;
}

/**
 * Las partidas seguidas de la misma clase contra el mismo blanco y con la misma vuelta,
 * juntas: el saqueador replanta el asalto que abandona, y el resumen daba una línea
 * igual por cada vez (la prueba en vivo de E41). Una caída nunca se agrupa.
 */
function groupRaids(raids: readonly RaidBalance[]): RaidBalance[][] {
  const groups: RaidBalance[][] = [];
  for (const p of raids) {
    const last = groups[groups.length - 1];
    const prev = last?.[0];
    if (last && prev && prev.kind === p.kind && prev.target === p.target && prev.outcome === p.outcome && p.outcome !== 'fell') {
      last.push(p);
    } else {
      groups.push([p]);
    }
  }
  return groups;
}

/** Cuántos se reunieron y cuánto tardaron; con varias partidas, la media de las que se reunieron. */
function rallyText(group: readonly RaidBalance[]): string {
  const rallied = group.filter((p) => p.gathered !== null);
  if (rallied.length === 0) return 'sin reunirse';
  const counts = rallied.map((p) => p.gathered as number);
  const min = Math.min(...counts);
  const max = Math.max(...counts);
  const wait = rallied.reduce((n, p) => n + (p.rallyWaitMs ?? 0), 0) / rallied.length;
  const unrallied = group.length - rallied.length;
  return (
    `reunidos ${min === max ? min : `${min}-${max}`} en ${formatDuration(wait)}${rallied.length > 1 ? ' de media' : ''}` +
    (unrallied > 0 ? ` (${unrallied} sin reunirse)` : '')
  );
}

export function formatDuration(ms: number): string {
  const s = Math.round(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const r = s % 60;
  return h > 0 ? `${h}h${String(m).padStart(2, '0')}m` : `${m}m${String(r).padStart(2, '0')}s`;
}

const list = (r: Readonly<Record<string, number>>) =>
  Object.keys(r).length === 0
    ? '—'
    : Object.entries(r)
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([k, v]) => `${k} ${v}`)
        .join(', ');

/** El texto que se imprime al terminar: un bloque por bot y una línea por perfil. */
export function formatSummary(rows: readonly BotSummary[]): string {
  const out: string[] = ['', 'Resumen de la ejecución', ''];
  for (const r of rows) {
    const eras = Object.entries(r.eras)
      .sort(([, a], [, b]) => a - b)
      .map(([era, ms]) => `${era} ${formatDuration(ms)}`)
      .join(' → ');
    out.push(`${r.bot} (${r.profile}, ${r.civilization}) · jugado ${formatDuration(r.playedMs)} · era final ${r.finalEra}`);
    out.push(`  eras: ${eras}`);
    out.push(`  recursos por hora: ${list(r.gatheredPerHour)}`);
    out.push(`  entrenadas: ${list(r.trained)} · construidos: ${list(r.built)} · tecnologías ${r.researched} · elaboraciones ${r.crafted}`);
    out.push(
      `  bajas propias ${r.lost} · enemigos muertos ${r.killed} · partidas ${r.raids} · saqueado ${r.pillageDone} de madera · derribos ${r.demolished}`,
    );
    out.push(`  sufrido: ${r.pillagedHits} golpes de saqueo, ${r.besiegedHits} de asedio, ${r.housesLost} edificios perdidos`);
    out.push(
      `  comercio: tratados ${r.treaties} · tratos ${r.trades} · bienes ajenos por trato ${r.goodsByTrade} y por mercado ${r.goodsByMarket} · ` +
        `comisión ${r.commission} · caravanas perdidas ${r.caravansLost}`,
    );
    for (const g of groupRaids(r.raidLog)) {
      const p = g[0] as RaidBalance;
      const times = g.length > 1 ? ` ×${g.length}` : '';
      const sum = (f: (x: RaidBalance) => number) => g.reduce((n, x) => n + f(x), 0);
      if (p.kind === 'assault') {
        const end = p.outcome === 'fell' ? `cae en ${formatDuration(p.msToFall ?? 0)}` : `vuelve (${p.outcome ?? '—'})`;
        out.push(
          `  asalto contra ${p.target}${times}: ${rallyText(g)} · perdidos ${sum((x) => x.lost)} (arietes ${sum((x) => x.ramsLost)}) · ` +
            `muertos ${sum((x) => x.killed)} · ${end}`,
        );
      } else {
        out.push(
          `  partida contra ${p.target}${times}: ${rallyText(g)} · perdidos ${sum((x) => x.lost)} · muertos ${sum((x) => x.killed)} · ` +
            `madera ${sum((x) => x.wood)}`,
        );
      }
    }
    if (r.assaultsPlanted + r.citiesTaken + r.assaultsSuffered + r.wallsBreached + r.falls.length > 0) {
      out.push(
        `  asalto: estandartes ${r.assaultsPlanted} · ciudades tumbadas ${r.citiesTaken} · rotos: ${list(r.assaultBreaks)} · ` +
          `vueltas: ${list(r.assaultAbandons)} · sufridos ${r.assaultsSuffered} (rechazados ${r.assaultsRepelled}) · murallas perdidas ${r.wallsBreached}`,
      );
    }
    const ms = (v: number | null) => (v === null ? 'no' : formatDuration(v));
    for (const f of r.falls) {
      const which = f.kept ? (f.capital ? ' (la capital, con otra en pie)' : ' (una colonia)') : '';
      out.push(
        `  cayó${which} ante ${f.by ?? '—'} en ${f.era}: muralla ${ms(f.wallMs)} · población de antes ${ms(f.populationMs)} · era siguiente ${ms(f.nextEraMs)}`,
      );
    }
    if (r.foundsStarted > 0 || r.foundFailures.no_site) {
      const ring = r.foundsFromColony > 0 ? ` (${r.foundsFromColony} junto a una colonia)` : '';
      out.push(`  fundaciones: empezadas ${r.foundsStarted}${ring} · logradas ${r.colonies.filter((c) => c.origin === 'founded').length} · fallidas: ${list(r.foundFailures)}`);
    }
    for (const c of r.colonies) {
      const how =
        c.origin === 'founded'
          ? `la ${c.ordinal ?? '—'}.ª, fundada a los ${formatDuration(c.foundedMs)} (${formatDuration(c.fromEraMs)} desde la era que la permite)`
          : `${c.origin === 'taken' ? 'tomada' : 'ya estaba'}, vista a los ${formatDuration(c.foundedMs)}`;
      out.push(
        `  colonia ${c.cityId ?? '—'}: ${how} · crecida ${ms(c.grownMs)} · población final ${c.finalPopulation ?? '—'} · ` +
          `madera ${c.woodPerHour ?? '—'} por hora${c.fell ? ' · cayó' : ''}`,
      );
    }
    if (r.citiesMax > 1 || r.unplayedMinutes > 0) {
      out.push(`  ciudades: al final ${r.citiesFinal} · máximo ${r.citiesMax} · minutos con alguna sin jugar ${r.unplayedMinutes}`);
    }
    if (r.sweep || r.straysSeen > 0) {
      const sw = r.sweep ? `barrido de ${r.sweep.views} vistas (faltaban ${r.sweep.missing}, encontradas ${r.sweep.found}) · ` : '';
      out.push(
        `  varados: ${sw}vistos ${r.straysSeen} (por el barrido ${r.straysSwept}) · de vuelta ${r.straysHome} · perdidos: ${list(r.straysLost)}`,
      );
    }
    out.push(`  comandos ${r.commands} · rechazados: ${list(r.rejected)}`);
  }
  const profiles = [...new Set(rows.map((r) => r.profile))].sort();
  if (profiles.length > 0) out.push('', 'Por perfil');
  for (const p of profiles) {
    const rs = rows.filter((r) => r.profile === p);
    // La era en que arrancó un bot está en `eras` con 0: no es una llegada.
    const bronze = rs.map((r) => r.eras.BRONZE_AGE).filter((v): v is number => v !== undefined && v > 0);
    const avgBronze = bronze.length > 0 ? formatDuration(bronze.reduce((a, b) => a + b, 0) / bronze.length) : '—';
    const sum = (f: (r: BotSummary) => number) => rs.reduce((a, r) => a + f(r), 0);
    // El tiempo hasta cada era, desde el arranque del bot: sin siembra, es el camino entero.
    const reach = (era: string) => {
      const ms = rs.map((r) => r.eras[era]).filter((v): v is number => v !== undefined && v > 0);
      const avg = ms.length > 0 ? formatDuration(ms.reduce((a, b) => a + b, 0) / ms.length) : '—';
      return `${ms.length}/${rs.length} (media ${avg})`;
    };
    out.push(
      `  ${p}: ${rs.length} bots · a Bronce ${bronze.length}/${rs.length} (media ${avgBronze}) · a Hierro ${reach('IRON_AGE')} · ` +
        `a Castillos ${reach('CASTLE_AGE')} · bajas ${sum((r) => r.lost)} · ` +
        `saqueado ${sum((r) => r.pillageDone)} · derribos ${sum((r) => r.demolished)} · ` +
        `bienes ajenos ${sum((r) => r.goodsByTrade + r.goodsByMarket)} · comisión ${sum((r) => r.commission)} · ` +
        `rechazos ${sum((r) => Object.values(r.rejected).reduce((a, b) => a + b, 0))}`,
    );
    const founded = rs.flatMap((r) => r.colonies);
    if (rs.some((r) => r.foundsStarted > 0 || r.foundFailures.no_site)) {
      const own = founded.filter((c) => c.origin === 'founded');
      const avgFound = own.length > 0 ? formatDuration(own.reduce((a, c) => a + c.foundedMs, 0) / own.length) : '—';
      out.push(`  ${p}: fundaciones ${own.length} (media hasta fundar ${avgFound}) · colonias crecidas ${founded.filter((c) => c.grownMs !== null).length}`);
    }
    if (rs.some((r) => r.citiesMax > 1)) {
      const wood = founded.map((c) => c.woodPerHour).filter((v): v is number => v !== null);
      const avgWood = wood.length > 0 ? Math.round(wood.reduce((a, b) => a + b, 0) / wood.length) : '—';
      out.push(
        `  ${p}: ciudades al final ${(sum((r) => r.citiesFinal) / rs.length).toFixed(1)} de media · madera por colonia ${avgWood} por hora · ` +
          `minutos sin jugar ${sum((r) => r.unplayedMinutes)}`,
      );
    }
  }
  // Las caídas por hora de ejecución, la medida de SPEC-BOTS-ASSAULT Q9.
  const falls = rows.reduce((a, r) => a + r.falls.length, 0);
  const taken = rows.reduce((a, r) => a + r.citiesTaken, 0);
  if (falls + taken > 0) {
    const hours = Math.max(...rows.map((r) => r.playedMs)) / 3_600_000;
    const perHour = hours > 0 ? (Math.max(falls, taken) / hours).toFixed(2) : '—';
    out.push('', `Caídas: ${Math.max(falls, taken)} en ${formatDuration(hours * 3_600_000)} (${perHour} por hora)`);
  }
  return out.join('\n');
}
