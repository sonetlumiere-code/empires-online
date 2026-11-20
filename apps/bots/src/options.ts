/**
 * Las opciones de `pnpm run bots` (SPEC-BOTS §4.4).
 *
 * Funciones puras: leen `argv` y el entorno y devuelven las opciones o un error
 * legible. Nada de aquí abre conexiones ni lee ficheros.
 */

export interface Options {
  /** Origen de la web (alta, login y tickets). */
  readonly web: string;
  /** URL del WebSocket del Game Server. */
  readonly server: string;
  readonly constructores: number;
  readonly saqueadores: number;
  /** `null`: hasta Ctrl-C. */
  readonly duracionMs: number | null;
  readonly semilla: number;
  readonly contraHumanos: boolean;
  /** Cada cuánto despierta un bot (`EO_BOT_THINK_MS`). */
  readonly thinkMs: number;
  /** Directorio de los JSONL (`bots/runs` en la raíz del repositorio). */
  readonly runsDir: string;
}

/** Tope de bots por proceso (SPEC-BOTS Q9). */
export const MAX_BOTS = 20;
export const DEFAULT_THINK_MS = 2_000;

export class OptionsError extends Error {
  override readonly name = 'OptionsError';
}

/** «2h», «30m», «90s», «1500ms» o un número de segundos → milisegundos. */
export function parseDuration(text: string): number {
  const m = /^(\d+)(ms|s|m|h)?$/.exec(text.trim());
  if (!m) throw new OptionsError(`duración no válida: «${text}» (usa 90s, 30m o 2h)`);
  const n = Number(m[1]);
  const unit = m[2] ?? 's';
  const ms = unit === 'ms' ? n : unit === 's' ? n * 1_000 : unit === 'm' ? n * 60_000 : n * 3_600_000;
  if (ms <= 0) throw new OptionsError(`la duración tiene que ser positiva: «${text}»`);
  return ms;
}

function parseCount(name: string, text: string | undefined): number {
  const n = Number(text);
  if (text === undefined || !Number.isInteger(n) || n < 0) {
    throw new OptionsError(`--${name} pide un entero no negativo`);
  }
  return n;
}

/** Lee las opciones de la línea de órdenes y del entorno. */
export function parseOptions(
  argv: readonly string[],
  env: Readonly<Record<string, string | undefined>>,
  nowMs: number,
  repoRoot: string,
): Options {
  let web = env.EO_BOT_WEB ?? 'http://localhost:3001';
  let server = env.EO_BOT_SERVER ?? env.NEXT_PUBLIC_GAME_SERVER_WS_URL ?? 'ws://localhost:8080/ws';
  let constructores = 3;
  let saqueadores = 1;
  let duracionMs: number | null = null;
  let semilla = nowMs % 2_147_483_647;
  let contraHumanos = false;

  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    const value = () => {
      const v = argv[i + 1];
      if (v === undefined || v.startsWith('--')) throw new OptionsError(`${arg} pide un valor`);
      i++;
      return v;
    };
    switch (arg) {
      case '--':
        break;
      case '--web':
        web = value();
        break;
      case '--server':
        server = value();
        break;
      case '--constructores':
        constructores = parseCount('constructores', value());
        break;
      case '--saqueadores':
        saqueadores = parseCount('saqueadores', value());
        break;
      case '--duracion':
        duracionMs = parseDuration(value());
        break;
      case '--semilla': {
        const v = value();
        const n = Number(v);
        if (!Number.isInteger(n) || n < 0) throw new OptionsError('--semilla pide un entero no negativo');
        semilla = n;
        break;
      }
      case '--contra-humanos':
        contraHumanos = true;
        break;
      default:
        throw new OptionsError(`opción desconocida: ${arg}`);
    }
  }

  const total = constructores + saqueadores;
  if (total === 0) throw new OptionsError('no hay ningún bot que lanzar');
  if (total > MAX_BOTS) throw new OptionsError(`como mucho ${MAX_BOTS} bots por proceso (pediste ${total})`);

  const thinkText = env.EO_BOT_THINK_MS;
  const thinkMs = thinkText === undefined || thinkText === '' ? DEFAULT_THINK_MS : Number(thinkText);
  if (!Number.isInteger(thinkMs) || thinkMs < 500 || thinkMs > 60_000) {
    throw new OptionsError('EO_BOT_THINK_MS tiene que ser un entero entre 500 y 60000');
  }

  return {
    web: web.replace(/\/+$/, ''),
    server,
    constructores,
    saqueadores,
    duracionMs,
    semilla,
    contraHumanos,
    thinkMs,
    runsDir: `${repoRoot}/bots/runs`,
  };
}

/** La contraseña de las cuentas de bots. Sin ella no se arranca (SPEC-BOTS §4.4). */
export function botPassword(env: Readonly<Record<string, string | undefined>>): string {
  const p = env.EO_BOT_PASSWORD;
  if (!p) {
    throw new OptionsError(
      'falta EO_BOT_PASSWORD: ponla en el .env (no hay contraseña por defecto). Las cuentas bot_* se dan de alta con ella.',
    );
  }
  if (p.length < 12) throw new OptionsError('EO_BOT_PASSWORD tiene que tener al menos 12 caracteres');
  return p;
}
