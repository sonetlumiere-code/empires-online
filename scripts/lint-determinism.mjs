#!/usr/bin/env node
/**
 * Lint de determinismo del Game Server (EO-017).
 *
 * El canon prohíbe que el núcleo determinista lea el reloj real o el azar
 * global: todo instante sale del `clock.Clock` inyectado y todo azar del
 * `clock.RandomSource`. Es lo que hace reproducibles los tests de simulación y
 * la regeneración del mundo desde la semilla. Este script lo comprueba en los
 * paquetes que forman ese núcleo:
 *
 *   - una llamada a `time.Now`, `time.Since` o `time.Until`;
 *   - una importación de `math/rand` o `math/rand/v2`.
 *
 * Medir cuánto TARDA algo —la duración de un tick o de una búsqueda de ruta,
 * para las métricas— es legítimo y no afecta al resultado. Se permite marcando
 * la línea, o la anterior, con un comentario que da el motivo:
 *
 *     start := time.Now() //lint:reloj-real mide la duración del A* para la métrica
 *
 * Un marcador sin motivo no vale. Los comentarios y los literales de cadena no
 * cuentan: mencionar `time.Now()` en un comentario no es usarlo.
 *
 * Uso:
 *   node scripts/lint-determinism.mjs
 *
 * Sale con código 1 si encuentra algo. Lo ejecutan `pnpm run lint` y la CI.
 */
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const serverRoot = join(repoRoot, 'services', 'game-server');

/**
 * Paquetes del núcleo determinista. `internal/clock` queda fuera porque es
 * justamente donde vive el reloj real; los bordes —red, persistencia, alta—
 * quedan fuera porque operan en tiempo real por naturaleza.
 */
export const DETERMINISTIC_ROOTS = [
  'internal/domain',
  'internal/game/world',
  'internal/game/simulation',
  'internal/game/loop',
  'internal/game/founding',
  'internal/pathfinding',
];

const MARKER = /\/\/\s*lint:reloj-real\s+\S/;
const FORBIDDEN_CALL = /\btime\.(Now|Since|Until)\s*\(/;
const FORBIDDEN_IMPORT = /^\s*(?:[A-Za-z_][A-Za-z0-9_]*\s+)?"math\/rand(?:\/v2)?"/;

/**
 * Sustituye comentarios y literales de cadena y de runa por espacios,
 * conservando los saltos de línea para que los números de línea sigan valiendo.
 */
export function stripCommentsAndStrings(src) {
  let out = '';
  let i = 0;
  const blank = (ch) => (ch === '\n' ? '\n' : ' ');
  while (i < src.length) {
    const c = src[i];
    const n = src[i + 1];
    if (c === '/' && n === '/') {
      while (i < src.length && src[i] !== '\n') { out += ' '; i++; }
    } else if (c === '/' && n === '*') {
      out += '  '; i += 2;
      while (i < src.length && !(src[i] === '*' && src[i + 1] === '/')) { out += blank(src[i]); i++; }
      out += '  '; i += 2;
    } else if (c === '`') {
      out += ' '; i++;
      while (i < src.length && src[i] !== '`') { out += blank(src[i]); i++; }
      out += ' '; i++;
    } else if (c === '"' || c === "'") {
      const q = c;
      out += ' '; i++;
      while (i < src.length && src[i] !== q && src[i] !== '\n') {
        if (src[i] === '\\') { out += ' '; i++; }
        out += ' '; i++;
      }
      out += ' '; i++;
    } else {
      out += c; i++;
    }
  }
  return out;
}

/** Devuelve los problemas de un fichero Go: [{ line, message }]. */
export function lintSource(src) {
  const raw = src.split('\n');
  const code = stripCommentsAndStrings(src).split('\n');
  const problems = [];
  const marked = (i) => MARKER.test(raw[i]) || (i > 0 && MARKER.test(raw[i - 1]));

  for (let i = 0; i < raw.length; i++) {
    const m = code[i].match(FORBIDDEN_CALL);
    if (m && !marked(i)) {
      problems.push({ line: i + 1, message: `time.${m[1]}() en el núcleo determinista: usa el clock.Clock inyectado, o marca la línea con //lint:reloj-real <motivo> si sólo mide una duración` });
    }
    // Las rutas de importación son cadenas: se buscan en el texto original,
    // descartando las líneas que son comentario.
    if (FORBIDDEN_IMPORT.test(raw[i]) && !/^\s*\/\//.test(raw[i])) {
      problems.push({ line: i + 1, message: 'importa math/rand: usa el clock.RandomSource inyectado' });
    }
  }
  return problems;
}

function goFiles(dir) {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) return goFiles(full);
    return name.endsWith('.go') && !name.endsWith('_test.go') ? [full] : [];
  });
}

export function lintTree(root = serverRoot) {
  const found = [];
  for (const sub of DETERMINISTIC_ROOTS) {
    for (const file of goFiles(join(root, sub))) {
      for (const p of lintSource(readFileSync(file, 'utf8'))) {
        found.push({ file: relative(repoRoot, file).replace(/\\/g, '/'), ...p });
      }
    }
  }
  return found;
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const found = lintTree();
  if (found.length === 0) {
    console.log(`Determinismo: sin reloj real ni azar global en ${DETERMINISTIC_ROOTS.length} raíces del núcleo.`);
  } else {
    for (const p of found) console.error(`${p.file}:${p.line}: ${p.message}`);
    console.error(`\n${found.length} problema(s) de determinismo.`);
    process.exit(1);
  }
}
