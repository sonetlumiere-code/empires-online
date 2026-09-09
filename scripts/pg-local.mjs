#!/usr/bin/env node
/**
 * Cluster de PostgreSQL local, propiedad del usuario. Alternativa a Docker.
 *
 * Crea y gobierna una instancia de PostgreSQL DENTRO del repositorio, en un
 * puerto propio. No necesita permisos de administrador, no instala ningún
 * servicio y no toca ninguna otra instancia que ya tengas en la máquina.
 *
 * Existe porque Docker Desktop en Windows exige elevación para arrancar su
 * servicio, y porque una instancia de PostgreSQL preinstalada suele venir con
 * una contraseña de superusuario que nadie recuerda. `initdb` resuelve las dos
 * cosas: quien crea el cluster es su superusuario.
 *
 *   node scripts/pg-local.mjs init     crea el cluster y las bases
 *   node scripts/pg-local.mjs start    arranca
 *   node scripts/pg-local.mjs stop     detiene
 *   node scripts/pg-local.mjs status   estado
 *   node scripts/pg-local.mjs destroy  BORRA el cluster entero
 *
 * Los datos viven en .pgdata/ y los logs en .pglogs/, ambos ignorados por git.
 * Se pueden borrar y recrear cuando se quiera: no son fuente de nada.
 */
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, rmSync, appendFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const dataDir = join(repoRoot, '.pgdata');
const logDir = join(repoRoot, '.pglogs');
const logFile = join(logDir, 'postgres.log');

const PORT = 5433;
const ROLE = 'empires';
const PASSWORD = 'empires_dev_password';
const DATABASES = ['empires', 'empires_test'];

/** Localiza los binarios de PostgreSQL. */
function findBinDir() {
  if (process.env.EO_PG_BIN) return process.env.EO_PG_BIN;

  if (process.platform === 'win32') {
    const base = 'C:\\Program Files\\PostgreSQL';
    if (existsSync(base)) {
      // La versión más alta disponible.
      const versions = readdirSync(base)
        .filter((v) => /^\d+$/.test(v))
        .sort((a, b) => Number(b) - Number(a));
      for (const v of versions) {
        const bin = join(base, v, 'bin');
        if (existsSync(join(bin, 'initdb.exe'))) return bin;
      }
    }
    fail(
      'No encuentro los binarios de PostgreSQL.\n' +
        'Instálalo con: winget install --id PostgreSQL.PostgreSQL.16\n' +
        'O indica su ruta con la variable EO_PG_BIN.',
    );
  }
  // En Linux y macOS se asume que están en el PATH.
  return '';
}

const binDir = findBinDir();
const exe = (name) => (binDir ? join(binDir, name) : name);

function fail(message) {
  console.error(`\n${message}\n`);
  process.exit(1);
}

function run(command, args, opts = {}) {
  const result = spawnSync(exe(command), args, {
    stdio: opts.quiet ? 'pipe' : 'inherit',
    encoding: 'utf8',
    env: { ...process.env, PGPASSWORD: PASSWORD, PGCLIENTENCODING: 'UTF8' },
  });
  if (result.error) fail(`no se pudo ejecutar ${command}: ${result.error.message}`);
  return result;
}

function isRunning() {
  const result = run('pg_ctl', ['-D', dataDir, 'status'], { quiet: true });
  return result.status === 0;
}

// ─────────────────────────────────────────────────────────────

function init() {
  if (existsSync(dataDir)) {
    console.log(`El cluster ya existe en ${dataDir}. Usa "start" o "destroy".`);
    return;
  }

  // initdb no acepta la contraseña por argumento —quedaría en el historial del
  // shell— así que se pasa por un archivo temporal que se borra acto seguido.
  const pwFile = join(repoRoot, '.pgpass.tmp');
  try {
    appendFileSync(pwFile, PASSWORD);
    console.log('Creando el cluster...');
    const result = run('initdb', [
      '-D', dataDir,
      '-U', ROLE,
      `--pwfile=${pwFile}`,
      '--auth-host=scram-sha-256',
      '--auth-local=scram-sha-256',
      '--encoding=UTF8',
      '--locale=C',
    ]);
    if (result.status !== 0) fail('initdb falló');
  } finally {
    rmSync(pwFile, { force: true });
  }

  // Puerto propio para no chocar con otra instancia, y escucha restringida a
  // loopback: este cluster es de desarrollo y no sale de la máquina.
  appendFileSync(
    join(dataDir, 'postgresql.conf'),
    `\n# --- Empires Online: cluster de desarrollo ---\nport = ${PORT}\nlisten_addresses = 'localhost'\n`,
  );

  start();

  for (const db of DATABASES) {
    const result = run('createdb', ['-h', 'localhost', '-p', String(PORT), '-U', ROLE, db], { quiet: true });
    console.log(result.status === 0 ? `  base "${db}" creada` : `  base "${db}" ya existía`);
  }

  console.log(`\nListo. Añade esto a tu .env:\n`);
  console.log(`  EO_POSTGRES_URL=postgres://${ROLE}:${PASSWORD}@localhost:${PORT}/empires?sslmode=disable\n`);
  console.log(`Aplica el esquema con: pnpm run db:migrate\n`);
}

function start() {
  if (!existsSync(dataDir)) fail('No hay cluster. Ejecuta primero: node scripts/pg-local.mjs init');
  if (isRunning()) {
    console.log(`Ya estaba en marcha en el puerto ${PORT}.`);
    return;
  }
  mkdirSync(logDir, { recursive: true });
  const result = run('pg_ctl', ['-D', dataDir, '-l', logFile, '-w', 'start']);
  if (result.status !== 0) fail(`no arrancó; revisa ${logFile}`);
  console.log(`PostgreSQL en marcha en localhost:${PORT}`);
}

function stop() {
  if (!existsSync(dataDir)) fail('No hay cluster que detener.');
  if (!isRunning()) {
    console.log('Ya estaba detenido.');
    return;
  }
  // `fast` cierra las conexiones abiertas y hace checkpoint: es el apagado
  // correcto, no el brusco.
  run('pg_ctl', ['-D', dataDir, '-m', 'fast', '-w', 'stop']);
  console.log('PostgreSQL detenido.');
}

function status() {
  if (!existsSync(dataDir)) {
    console.log('No hay cluster local. Créalo con: node scripts/pg-local.mjs init');
    return;
  }
  console.log(isRunning() ? `En marcha en localhost:${PORT}` : 'Detenido.');
}

function destroy() {
  if (!existsSync(dataDir)) {
    console.log('No hay nada que borrar.');
    return;
  }
  if (isRunning()) stop();
  rmSync(dataDir, { recursive: true, force: true });
  rmSync(logDir, { recursive: true, force: true });
  console.log('Cluster borrado. Recréalo con "init".');
}

// Abre una sesión interactiva contra este cluster. Existe porque los scripts
// `db:*` del package.json pasan todos por `docker compose exec`, y en una
// máquina sin Docker no había ninguna forma corta de llegar a la base.
function psql() {
  if (!existsSync(dataDir)) {
    fail('No hay cluster local. Créalo con: node scripts/pg-local.mjs init');
  }
  if (!isRunning()) {
    fail('El cluster está detenido. Arráncalo con: node scripts/pg-local.mjs start');
  }

  // Lo que venga después del comando se pasa tal cual a psql, lo que permite
  //   pnpm run pg:psql -- -d empires_test -c "select count(*) from units"
  //
  // El `--` separador lo consume pnpm en Unix pero lo reenvía literal en
  // Windows; si llegara hasta psql, éste trataría todo lo siguiente como
  // argumentos posicionales y los ignoraría con una advertencia confusa.
  const extra = process.argv.slice(3);
  if (extra[0] === '--') extra.shift();
  const traeBase = extra.some((a) => a === '-d' || a === '--dbname' || a.startsWith('--dbname='));

  const result = run('psql', [
    '-h', 'localhost',
    '-p', String(PORT),
    '-U', ROLE,
    ...(traeBase ? [] : ['-d', DATABASES[0]]),
    ...extra,
  ]);
  process.exit(result.status ?? 0);
}

const commands = { init, start, stop, status, destroy, psql };
const command = process.argv[2];

if (!command || !(command in commands)) {
  console.log('Uso: node scripts/pg-local.mjs <init|start|stop|status|destroy|psql>');
  process.exit(command ? 1 : 0);
}
commands[command]();

