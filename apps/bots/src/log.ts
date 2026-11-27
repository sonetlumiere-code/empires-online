/**
 * El registro de una ejecución (SPEC-BOTS §4.5): una línea JSON por suceso.
 *
 * Las líneas se construyen sólo con datos del juego; aun así, cada una pasa por
 * `redact` antes de escribirse, que tacha la contraseña, cualquier cookie de
 * sesión y cualquier token con forma de JWT (RN-BOT-006).
 */

export type EventName =
  | 'start'
  | 'command'
  | 'rejected'
  | 'era'
  | 'stock'
  | 'trained'
  | 'built'
  | 'researched'
  | 'crafted'
  | 'lost'
  | 'killed'
  | 'pillage_done'
  | 'demolished'
  | 'pillaged'
  | 'besieged'
  | 'house_lost'
  | 'raid'
  | 'raid_end'
  | 'treaty'
  | 'trade'
  | 'market'
  | 'caravan'
  | 'rally'
  | 'assault'
  | 'fell'
  | 'wall'
  | 'found'
  | 'colony'
  | 'stray'
  | 'cities'
  | 'sweep'
  | 'error'
  | 'stop';

export interface LogLine {
  /** Hora del servidor (ms desde la época). */
  readonly t: number;
  readonly event: EventName;
  readonly bot: string;
  readonly [field: string]: unknown;
}

const JWT = /eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+/g;
const COOKIE = /eo_session=[^;"\s]*/g;

/** Tacha de una línea ya serializada lo que nunca puede salir (RN-BOT-006). */
export function redact(line: string, secrets: readonly string[]): string {
  let out = line.replace(JWT, '[ticket]').replace(COOKIE, 'eo_session=[cookie]');
  for (const s of secrets) {
    if (s.length > 0) out = out.split(s).join('[secreto]');
    const escaped = JSON.stringify(s).slice(1, -1);
    if (escaped !== s && escaped.length > 0) out = out.split(escaped).join('[secreto]');
  }
  return out;
}

export class RunLog {
  constructor(
    private readonly write: (line: string) => void,
    private readonly secrets: readonly string[],
  ) {}

  emit(t: number, event: EventName, bot: string, fields: Record<string, unknown> = {}): void {
    const line: LogLine = { t, event, bot, ...fields };
    this.write(`${redact(JSON.stringify(line), this.secrets)}\n`);
  }
}

/** Lee un JSONL ya escrito; las líneas que no son JSON se ignoran. */
export function parseLog(text: string): LogLine[] {
  const out: LogLine[] = [];
  for (const raw of text.split('\n')) {
    if (!raw.trim()) continue;
    try {
      const v = JSON.parse(raw) as LogLine;
      if (typeof v.event === 'string' && typeof v.bot === 'string') out.push(v);
    } catch {
      // Una línea a medias (el proceso se mató) no invalida el resto.
    }
  }
  return out;
}
