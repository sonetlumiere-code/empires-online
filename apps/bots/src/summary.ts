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
}

export interface FallRecovery {
  readonly by: string | null;
  readonly era: string;
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
        case 'fell':
          falls.push({
            t: l.t,
            by: typeof l.by === 'string' ? l.by : null,
            era: String(l.era),
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
    });
  }
  return out;
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
    for (const p of r.raidLog) {
      const rally = p.gathered === null ? 'sin reunirse' : `reunidos ${p.gathered} en ${formatDuration(p.rallyWaitMs ?? 0)}`;
      if (p.kind === 'assault') {
        const end = p.outcome === 'fell' ? `cae en ${formatDuration(p.msToFall ?? 0)}` : `vuelve (${p.outcome ?? '—'})`;
        out.push(`  asalto contra ${p.target}: ${rally} · perdidos ${p.lost} (arietes ${p.ramsLost}) · muertos ${p.killed} · ${end}`);
      } else {
        out.push(`  partida contra ${p.target}: ${rally} · perdidos ${p.lost} · muertos ${p.killed} · madera ${p.wood}`);
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
      out.push(
        `  cayó ante ${f.by ?? '—'} en ${f.era}: muralla ${ms(f.wallMs)} · población de antes ${ms(f.populationMs)} · era siguiente ${ms(f.nextEraMs)}`,
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
