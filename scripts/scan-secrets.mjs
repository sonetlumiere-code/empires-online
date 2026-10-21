#!/usr/bin/env node
/**
 * Escaneo de secretos (INV-SEC-006): ninguna credencial de infraestructura llega
 * al repositorio —en ninguna revisión de su historia— ni al bundle del navegador.
 *
 *   node scripts/scan-secrets.mjs --tree               ficheros versionados hoy
 *   node scripts/scan-secrets.mjs --history            cada línea añadida en la historia
 *   node scripts/scan-secrets.mjs --bundle <dir>       bundle del cliente (.next/static)
 *
 * Sin dependencias, a propósito: sólo Node y git. Lo que no se puede fijar por
 * versión no entra en la cadena que decide si un secreto se ha escapado.
 *
 * Dos clases de hallazgo:
 *
 *   - Patrones de credencial de alta señal (claves privadas, tokens con formato
 *     conocido, JWT firmados, URL con contraseña, asignaciones a variables con
 *     nombre de secreto en ficheros de configuración).
 *   - En el bundle, además, CANARIOS: el valor real de cada variable secreta
 *     presente en el entorno del build. La CI construye el cliente con valores
 *     canario en EO_AUTH_JWT_SECRET, EO_IDENTITY_POSTGRES_URL, ... y aquí se
 *     exige que ninguno aparezca en lo que se sirve al navegador, ni el nombre de
 *     esas variables, ni las marcas de los módulos de servidor.
 *
 * Los valores que parecen credenciales y no lo son —contraseñas del cluster de
 * desarrollo, de los servicios efímeros de la CI, secretos de los tests— están
 * en scripts/secret-scan-allowlist.json, cada uno con su motivo.
 */
import { execFileSync } from 'node:child_process';
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, extname, join, relative, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');

/** Patrones de credencial. `secret` es el grupo que contiene el valor. */
export const RULES = [
  { id: 'private-key', re: /-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----/ },
  { id: 'aws-access-key', re: /\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/ },
  { id: 'github-token', re: /\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,})\b/ },
  { id: 'slack-token', re: /\bxox[abposr]-[A-Za-z0-9-]{10,}/ },
  { id: 'stripe-live-key', re: /\b[sr]k_live_[A-Za-z0-9]{20,}\b/ },
  { id: 'google-api-key', re: /\bAIza[0-9A-Za-z_-]{35}\b/ },
  { id: 'llm-api-key', re: /\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{32,}/ },
  { id: 'npm-token', re: /\bnpm_[A-Za-z0-9]{36}\b/ },
  { id: 'signed-jwt', re: /\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{16,}/ },
  {
    id: 'url-with-password',
    re: /\b(?:postgres(?:ql)?|redis|rediss|mysql|mongodb(?:\+srv)?|amqps?):\/\/[^:\s/@'"`]*:([^@\s'"`]+)@/, // usuario opcional: redis://:clave@host
    secret: 1,
  },
  {
    // Sólo en ficheros de configuración y documentación: en el código, un nombre
    // así va seguido de una expresión, no de un valor.
    id: 'secret-assignment',
    re: /\b([A-Z0-9_]*(?:SECRET|PASSWORD|PASSWD|TOKEN|API_KEY|PRIVATE_KEY)[A-Z0-9_]*)["']?\s*[=:]\s*["']?([^\s"'`#,;)]+)/,
    secret: 2,
    configOnly: true,
  },
];

/** Extensiones de código fuente: `secret-assignment` no se aplica en ellas. */
const CODE_EXTENSIONS = new Set(['.go', '.ts', '.tsx', '.js', '.mjs', '.cjs', '.jsx']);

/** Valores que nunca son un secreto: vacíos, referencias o marcadores. */
export function isPlaceholder(value) {
  if (!value) return true;
  return (
    value.length < 8 ||
    !/^[A-Za-z0-9]/.test(value) || // un fragmento de expresión o de comando, no un valor
    /^\$\{?\{?/.test(value) || // ${VAR}, $VAR, ${{ secrets.X }}
    /^<[^>]*>?$/.test(value) || // <password>
    /^\[?REDACTED\]?$/i.test(value) ||
    /^(?:changeme|change-me|example|placeholder|x{3,}|\*{3,}|\.{3,}|…)$/i.test(value) ||
    /^(?:true|false|null|undefined|required|optional)$/i.test(value) ||
    /^[A-Z0-9_]+$/.test(value) // otro nombre de variable, no un valor
  );
}

export function loadAllowlist(file = join(repoRoot, 'scripts', 'secret-scan-allowlist.json')) {
  const doc = JSON.parse(readFileSync(file, 'utf8'));
  for (const e of doc.entries) {
    if (!e.reason || (!e.value && !e.path)) throw new Error(`entrada de la lista sin motivo o sin valor/ruta: ${JSON.stringify(e)}`);
  }
  return doc.entries;
}

function allowed(allowlist, { value, path, rule }) {
  return allowlist.some(
    (e) =>
      (!e.value || e.value === value) &&
      (!e.path || path === e.path || path.startsWith(e.path.endsWith('/') ? e.path : `${e.path}/`)) &&
      (!e.rules || e.rules.includes(rule)),
  );
}

/** Hallazgos de una línea. `path` decide si es configuración o código. */
export function scanLine(line, path, allowlist = []) {
  const findings = [];
  const isCode = CODE_EXTENSIONS.has(extname(path).toLowerCase());
  for (const rule of RULES) {
    if (rule.configOnly && isCode) continue;
    const re = new RegExp(rule.re.source, 'g');
    for (const m of line.matchAll(re)) {
      const value = rule.secret ? m[rule.secret] : m[0];
      if (rule.secret && isPlaceholder(value)) continue;
      if (allowed(allowlist, { value, path, rule: rule.id })) continue;
      findings.push({ rule: rule.id, path, value });
    }
  }
  return findings;
}

/** Nunca se imprime un valor entero: el informe de un escaneo no puede filtrar lo que encuentra. */
export function mask(value) {
  if (value.length <= 8) return '*'.repeat(value.length);
  return `${value.slice(0, 4)}…${value.slice(-2)} (${value.length} caracteres)`;
}

function git(args) {
  return execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8', maxBuffer: 512 * 1024 * 1024 });
}

const BINARY = /\.(?:png|jpe?g|gif|webp|ico|woff2?|ttf|otf|pdf|zip|gz|tgz|wasm|lock)$/i;
const SKIP_PATHS = ['pnpm-lock.yaml', 'services/game-server/go.sum'];

export function scanTree(allowlist) {
  const findings = [];
  for (const path of git(['ls-files', '-z']).split('\0').filter(Boolean)) {
    if (BINARY.test(path) || SKIP_PATHS.includes(path)) continue;
    const full = join(repoRoot, path);
    if (!existsSync(full) || statSync(full).size > 2 * 1024 * 1024) continue;
    readFileSync(full, 'utf8')
      .split(/\r?\n/)
      .forEach((line, i) => {
        for (const f of scanLine(line, path, allowlist)) findings.push({ ...f, where: `${path}:${i + 1}` });
      });
  }
  return findings;
}

export function scanHistory(allowlist) {
  const findings = [];
  const log = git(['log', '--all', '-p', '--no-color', '--no-ext-diff', '--format=commit %H', '--unified=0']);
  let commit = '';
  let path = '';
  for (const line of log.split('\n')) {
    if (line.startsWith('commit ')) commit = line.slice(7, 19);
    else if (line.startsWith('+++ b/')) path = line.slice(6);
    else if (line.startsWith('+') && !line.startsWith('+++')) {
      if (BINARY.test(path) || SKIP_PATHS.includes(path)) continue;
      for (const f of scanLine(line.slice(1), path, allowlist)) findings.push({ ...f, where: `${commit} ${path}` });
    }
  }
  return findings;
}

/** Variables cuyo VALOR jamás puede aparecer en el bundle. */
export const SECRET_ENV = ['EO_AUTH_JWT_SECRET', 'EO_IDENTITY_POSTGRES_URL', 'EO_POSTGRES_URL', 'EO_REDIS_URL'];
/** Marcas de los módulos de servidor de apps/web: si aparecen, el bundle arrastra código de servidor. */
export const SERVER_MARKERS = [...SECRET_ENV, 'game-server-internal', 'scrypt$', 'web_sessions'];

export function scanBundle(dir, env = process.env, allowlist = []) {
  const findings = [];
  const canaries = SECRET_ENV.map((k) => [k, env[k]]).filter(([, v]) => v && v.length >= 8);
  const walk = (d) => {
    for (const name of readdirSync(d)) {
      const full = join(d, name);
      if (statSync(full).isDirectory()) walk(full);
      else if (!BINARY.test(name) || name.endsWith('.map')) {
        const rel = relative(repoRoot, full).replace(/\\/g, '/');
        const text = readFileSync(full, 'utf8');
        for (const [k, v] of canaries) if (text.includes(v)) findings.push({ rule: 'canary', path: rel, value: v, where: `${rel} (${k})` });
        for (const marker of SERVER_MARKERS) {
          if (text.includes(marker)) findings.push({ rule: 'server-marker', path: rel, value: marker, where: `${rel} («${marker}»)` });
        }
        text.split(/\n/).forEach((line) => {
          for (const f of scanLine(line, rel, allowlist)) findings.push({ ...f, where: rel });
        });
      }
    }
  };
  if (!existsSync(dir)) throw new Error(`no existe ${dir}: construye antes el cliente (pnpm run web:build)`);
  walk(dir);
  return { findings, canaries: canaries.map(([k]) => k) };
}

function report(title, findings) {
  if (findings.length === 0) {
    console.log(`  ✓ ${title}: sin hallazgos`);
    return 0;
  }
  console.error(`  ✗ ${title}: ${findings.length} hallazgo(s)`);
  for (const f of findings.slice(0, 50)) {
    const shown = f.rule === 'server-marker' ? f.value : mask(f.value);
    console.error(`      [${f.rule}] ${f.where}  ${shown}`);
  }
  if (findings.length > 50) console.error(`      … y ${findings.length - 50} más`);
  return 1;
}

function main(argv) {
  const allowlist = loadAllowlist();
  let failed = 0;
  console.log('\nEscaneo de secretos (INV-SEC-006)\n');
  if (argv.includes('--tree')) failed |= report('árbol de trabajo versionado', scanTree(allowlist));
  if (argv.includes('--history')) failed |= report('historia completa (líneas añadidas)', scanHistory(allowlist));
  const b = argv.indexOf('--bundle');
  if (b >= 0) {
    const dir = resolve(argv[b + 1] ?? '');
    const { findings, canaries } = scanBundle(dir, process.env, allowlist);
    if (argv.includes('--require-canaries') && canaries.length < SECRET_ENV.length) {
      console.error(`  ✗ faltan canarios en el entorno: hay ${canaries.join(', ') || 'ninguno'}; se esperan ${SECRET_ENV.join(', ')}`);
      failed = 1;
    }
    failed |= report(`bundle del cliente (${relative(repoRoot, dir)}; canarios: ${canaries.join(', ') || 'ninguno'})`, findings);
  }
  if (!argv.some((a) => ['--tree', '--history', '--bundle'].includes(a))) {
    console.log('Uso: node scripts/scan-secrets.mjs [--tree] [--history] [--bundle <dir> [--require-canaries]]');
    return 2;
  }
  console.log('');
  return failed;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.exit(main(process.argv.slice(2)));
}
