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
    for (const l of ls) {
      switch (l.event) {
        case 'era':
          eras[String(l.era)] = l.t - start.t;
          finalEra = String(l.era);
          break;
        case 'stock':
        case 'stop':
          if (l.gathered && typeof l.gathered === 'object') gathered = { ...(l.gathered as Record<string, number>) };
          if (l.event === 'stop' && typeof l.era === 'string') finalEra = l.era;
          break;
        case 'trained':
          inc(trained, String(l.unitType));
          break;
        case 'built':
          inc(built, String(l.building));
          break;
        case 'researched':
          researched++;
          break;
        case 'crafted':
          crafted++;
          break;
        case 'lost':
          lost++;
          break;
        case 'killed':
          killed++;
          break;
        case 'pillage_done':
          pillageDone += typeof l.amount === 'number' ? l.amount : 0;
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
    out.push(`  comandos ${r.commands} · rechazados: ${list(r.rejected)}`);
  }
  const profiles = [...new Set(rows.map((r) => r.profile))].sort();
  if (profiles.length > 0) out.push('', 'Por perfil');
  for (const p of profiles) {
    const rs = rows.filter((r) => r.profile === p);
    const bronze = rs.map((r) => r.eras.BRONZE_AGE).filter((v): v is number => v !== undefined);
    const avgBronze = bronze.length > 0 ? formatDuration(bronze.reduce((a, b) => a + b, 0) / bronze.length) : '—';
    const sum = (f: (r: BotSummary) => number) => rs.reduce((a, r) => a + f(r), 0);
    out.push(
      `  ${p}: ${rs.length} bots · a Bronce ${bronze.length}/${rs.length} (media ${avgBronze}) · bajas ${sum((r) => r.lost)} · ` +
        `saqueado ${sum((r) => r.pillageDone)} · derribos ${sum((r) => r.demolished)} · rechazos ${sum((r) => Object.values(r.rejected).reduce((a, b) => a + b, 0))}`,
    );
  }
  return out.join('\n');
}
