/**
 * `pnpm run bots`: lanza N bots contra una web y un Game Server (SPEC-BOTS §4.4).
 *
 *   pnpm run bots -- --web http://localhost:3001 --server ws://localhost:8080/ws \
 *     --constructores 3 --saqueadores 1 --duracion 2h --semilla 42 [--contra-humanos] \
 *     [--sin-comercio] [--con-humanos] [--sufijo a1]
 *
 * La contraseña de las cuentas es `EO_BOT_PASSWORD`, del entorno o del `.env` de la
 * raíz (de él sólo se leen las variables `EO_BOT_*`, y nunca se imprimen).
 */
import { createWriteStream, existsSync, mkdirSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { v1 } from '@empires-online/protocol';
import { AuthThrottle } from './auth';
import { Bot, Directory } from './bot';
import { RunLog, parseLog } from './log';
import { OptionsError, botPassword, linkWarning, parseOptions } from './options';
import { PROFILES, type ProfileName } from './profiles';
import { seedFor } from './random';
import { formatSummary, summarize } from './summary';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');

/** Las `EO_BOT_*` del `.env` que el entorno no trae ya. */
export function botEnvFromDotenv(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const raw of text.split('\n')) {
    const m = /^\s*(EO_BOT_[A-Z0-9_]+)\s*=\s*(.*?)\s*$/.exec(raw);
    if (!m) continue;
    let value = m[2] ?? '';
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    out[m[1] as string] = value;
  }
  return out;
}

/**
 * Los bots de una ejecución: nombres numerados por perfil y una civilización por
 * turno. Con `sufijo`, cuentas nuevas (`bot_constructor_1_<sufijo>`) para medir
 * desde cero sin tocar las de siempre (SPEC-BOTS-TRADE §4.8, Q9).
 */
export function rosterFor(
  constructores: number,
  saqueadores: number,
  sufijo: string | null = null,
): { name: string; profile: ProfileName; civilization: string }[] {
  const civs = v1.CIVILIZATIONS.map((c) => c.code);
  const tail = sufijo ? `_${sufijo}` : '';
  const out: { name: string; profile: ProfileName; civilization: string }[] = [];
  for (let i = 1; i <= constructores; i++) out.push({ name: `bot_constructor_${i}${tail}`, profile: 'constructor', civilization: '' });
  for (let i = 1; i <= saqueadores; i++) out.push({ name: `bot_saqueador_${i}${tail}`, profile: 'saqueador', civilization: '' });
  return out.map((b, i) => ({ ...b, civilization: civs[i % civs.length] as string }));
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function main(): Promise<void> {
  const dotenv = resolve(repoRoot, '.env');
  const env: Record<string, string | undefined> = {
    ...(existsSync(dotenv) ? botEnvFromDotenv(readFileSync(dotenv, 'utf8')) : {}),
    ...process.env,
  };

  let opts;
  let password: string;
  try {
    opts = parseOptions(process.argv.slice(2), env, Date.now(), repoRoot);
    password = botPassword(env);
  } catch (err) {
    if (err instanceof OptionsError) {
      console.error(`bots: ${err.message}`);
      process.exit(2);
    }
    throw err;
  }

  mkdirSync(opts.runsDir, { recursive: true });
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  const file = resolve(opts.runsDir, `${stamp}-${opts.semilla}.jsonl`);
  const out = createWriteStream(file, { flags: 'a' });
  const log = new RunLog((line) => out.write(line), [password]);
  const say = (text: string) => console.log(text);

  say(
    `Bots contra ${opts.web} y ${opts.server}: ${opts.constructores} constructores y ${opts.saqueadores} saqueadores, ` +
      `semilla ${opts.semilla}${opts.duracionMs ? `, ${Math.round(opts.duracionMs / 60_000)} min` : ', hasta Ctrl-C'}` +
      `${opts.contraHumanos ? ', también contra humanos' : ''}` +
      `${opts.comercio ? (opts.conHumanos ? ', comerciando también con humanos' : '') : ', sin comercio'}.`,
  );
  say(`Registro: ${file}`);
  const warning = linkWarning(opts.constructores + opts.saqueadores);
  if (warning) say(warning);

  const directory = new Directory();
  const bots: Bot[] = [];
  let stopping = false;

  const stop = async (why: string) => {
    if (stopping) return;
    stopping = true;
    say(`\nParando (${why})…`);
    for (const b of bots) b.stop();
    await new Promise<void>((r) => out.end(r));
    const rows = summarize(parseLog(readFileSync(file, 'utf8')));
    say(formatSummary(rows));
    say(`\nRegistro completo: ${file}`);
    process.exit(0);
  };
  process.on('SIGINT', () => void stop('Ctrl-C'));
  process.on('SIGTERM', () => void stop('SIGTERM'));
  if (opts.duracionMs) setTimeout(() => void stop('fin de la duración'), opts.duracionMs).unref();

  const throttle = new AuthThrottle();
  for (const r of rosterFor(opts.constructores, opts.saqueadores, opts.sufijo)) {
    if (stopping) break;
    const wait = throttle.take();
    if (wait > 0) {
      say(`(esperando ${Math.ceil(wait / 1000)} s por el límite de altas y entradas de la web)`);
      await sleep(wait);
    }
    const bot = new Bot({
      name: r.name,
      profile: PROFILES[r.profile],
      civilization: r.civilization,
      web: opts.web,
      server: opts.server,
      password,
      thinkMs: opts.thinkMs,
      seed: seedFor(opts.semilla, r.name),
      contraHumanos: opts.contraHumanos,
      comercio: opts.comercio,
      conHumanos: opts.conHumanos,
      directory,
      log,
      say,
    });
    bots.push(bot);
    try {
      await bot.start();
    } catch (err) {
      say(`${r.name}: no arrancó: ${(err as Error).message}`);
      log.emit(Date.now(), 'error', r.name, { code: 'START_FAILED', message: String((err as Error).message).slice(0, 200) });
    }
  }
  if (!stopping) say('Todos los bots en marcha.');
}

const isEntry = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (isEntry) {
  main().catch((err) => {
    console.error(`bots: ${(err as Error).message}`);
    process.exit(1);
  });
}
