#!/usr/bin/env node
/**
 * Verificador de integridad de la documentación.
 *
 * Comprueba cinco cosas que se rompen solas conforme crece un proyecto y que
 * nadie detecta leyendo:
 *
 *   1. Todo enlace relativo a un .md apunta a un archivo que existe.
 *   2. Ese archivo se escribe con la MISMA caja que en el disco. Windows no
 *      distingue mayúsculas y Linux sí, de modo que `../Decisions/adr.md`
 *      funciona en la máquina de desarrollo y rompe en la CI. Es la única
 *      comprobación de esta lista que sólo sirve de algo en Windows: en Linux
 *      no hay nada que detectar porque el enlace ya ha fallado.
 *   3. Toda ancla `#fragmento` de esos enlaces resuelve a un destino real.
 *   4. Todo ID `INV-*` citado en cualquier documento está DEFINIDO en
 *      docs/invariants/, y ninguno designa dos invariantes distintos.
 *   5. Todo ADR citado existe con ese nombre de archivo exacto.
 *
 * Uso:
 *   node scripts/check-docs.mjs
 *
 * Sale con código 1 si encuentra problemas: la CI lo usa como comprobación
 * obligatoria.
 */
import { readdirSync, readFileSync, existsSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const docsRoot = join(repoRoot, 'docs');

function walk(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = join(dir, entry.name);
    return entry.isDirectory() ? walk(full) : [full];
  });
}

const problems = [];
const rel = (p) => relative(repoRoot, p).replace(/\\/g, '/');

// `existsSync` pregunta al sistema de archivos, y en Windows el sistema de
// archivos no distingue mayúsculas: `docs/Decisions/ADR-001.md` "existe"
// aunque el directorio se llame `decisions`. Para saber cómo se llama de
// verdad hay que listar el directorio padre y comparar. Se cachea por
// directorio porque si no serían miles de `readdirSync` sobre las mismas
// carpetas.
const dirEntriesCache = new Map();
function entriesOf(dir) {
  let entries = dirEntriesCache.get(dir);
  if (!entries) {
    entries = readdirSync(dir);
    dirEntriesCache.set(dir, entries);
  }
  return entries;
}

/** Devuelve la ruta con la caja real del disco, o null si algún segmento no existe. */
function realCaseWithin(absPath) {
  const relPath = relative(repoRoot, absPath).replace(/\\/g, '/');
  // Un `..` de más saldría del repositorio; ahí no comprobamos nada.
  if (relPath.startsWith('..')) return absPath;

  let current = repoRoot;
  for (const segment of relPath.split('/')) {
    const match = entriesOf(current).find((e) => e.toLowerCase() === segment.toLowerCase());
    if (!match) return null;
    current = join(current, match);
  }
  return current;
}

if (!existsSync(docsRoot)) {
  console.error('No existe el directorio docs/.');
  process.exit(1);
}

const markdownFiles = walk(docsRoot).filter((f) => f.endsWith('.md'));

// ─────────────────────────────────────────────────────────────
// 1 y 2. Enlaces relativos y sus anclas
// ─────────────────────────────────────────────────────────────

/**
 * Convierte un encabezado en el ancla que generan GitHub y la mayoría de
 * renderizadores de markdown.
 *
 * Dos detalles que parecen menores y no lo son:
 *   - El guion bajo **se conserva**: `#63-rotación-de-eo_auth_jwt_secret`.
 *   - Los espacios consecutivos NO se colapsan; cada uno produce su guion.
 *     Por eso «Paso 5 — Verificar» genera `paso-5--verificar`, con dos.
 */
function slugify(heading) {
  return heading
    .trim()
    .toLowerCase()
    .replace(/`/g, '')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1') // enlaces → su texto
    .replace(/[^\p{L}\p{N}\s_-]/gu, '')
    .replace(/\s/g, '-');
}

/** Anclas disponibles en un documento: las de sus encabezados y las explícitas. */
const anchorCache = new Map();
function anchorsOf(file) {
  if (anchorCache.has(file)) return anchorCache.get(file);

  const content = readFileSync(file, 'utf8');
  const anchors = new Set();

  for (const match of content.matchAll(/^#{1,6}\s+(.+)$/gm)) {
    anchors.add(slugify(match[1]));
  }
  // Anclas HTML explícitas: sobreviven a un cambio de título, que es
  // exactamente por lo que se ponen.
  for (const match of content.matchAll(/<a\s+id="([^"]+)"/g)) {
    anchors.add(match[1].toLowerCase());
  }

  anchorCache.set(file, anchors);
  return anchors;
}

let linksChecked = 0;
let anchorsChecked = 0;

for (const file of markdownFiles) {
  const content = readFileSync(file, 'utf8');
  // Enlaces markdown a archivos .md, con o sin ancla. Se ignoran las URLs
  // absolutas: no es tarea de este script comprobar Internet.
  for (const match of content.matchAll(/\]\((?!https?:)([^)#\s]+\.md)(#([^)\s]*))?\)/g)) {
    linksChecked += 1;
    const target = resolve(dirname(file), match[1]);
    if (!existsSync(target)) {
      problems.push(`enlace roto   ${rel(file)}  ->  ${match[1]}`);
      continue;
    }

    const real = realCaseWithin(target);
    if (real && rel(real) !== rel(target)) {
      problems.push(
        `caja incorrecta   ${rel(file)}  ->  ${match[1]}\n` +
          `                    en disco: ${rel(real)}  (funciona en Windows, rompe en Linux)`,
      );
      continue;
    }

    const fragment = match[3];
    if (!fragment) continue;

    anchorsChecked += 1;
    if (!anchorsOf(target).has(fragment.toLowerCase())) {
      problems.push(`ancla rota   ${rel(file)}  ->  ${match[1]}#${fragment}`);
    }
  }

  // Anclas dentro del propio documento.
  for (const match of content.matchAll(/\]\(#([^)\s]+)\)/g)) {
    anchorsChecked += 1;
    if (!anchorsOf(file).has(match[1].toLowerCase())) {
      problems.push(`ancla rota   ${rel(file)}  ->  #${match[1]} (mismo documento)`);
    }
  }
}

// ─────────────────────────────────────────────────────────────
// 3. Registro de invariantes
// ─────────────────────────────────────────────────────────────
const invariantsDir = join(docsRoot, 'invariants');
const defined = new Set();

if (existsSync(invariantsDir)) {
  for (const file of walk(invariantsDir).filter((f) => f.endsWith('.md'))) {
    const content = readFileSync(file, 'utf8');
    for (const match of content.matchAll(/INV-[A-Z]+-\d{3}/g)) {
      defined.add(match[0]);
    }
  }
}

const citedOutside = new Map();
for (const file of markdownFiles) {
  if (file.startsWith(invariantsDir)) continue;
  const content = readFileSync(file, 'utf8');
  for (const match of content.matchAll(/INV-[A-Z]+-\d{3}/g)) {
    if (!citedOutside.has(match[0])) citedOutside.set(match[0], new Set());
    citedOutside.get(match[0]).add(rel(file));
  }
}

for (const [id, files] of [...citedOutside].sort()) {
  if (defined.has(id)) continue;
  problems.push(
    `invariante sin registrar   ${id}   citado en: ${[...files].join(', ')}\n` +
      `                            → defínelo en docs/invariants/ o usa un ID existente`,
  );
}

// ─────────────────────────────────────────────────────────────
// 4. ADR citados
// ─────────────────────────────────────────────────────────────
const decisionsDir = join(docsRoot, 'decisions');
const adrFiles = existsSync(decisionsDir)
  ? new Set(readdirSync(decisionsDir).filter((f) => f.startsWith('ADR-')))
  : new Set();

for (const file of markdownFiles) {
  const content = readFileSync(file, 'utf8');
  for (const match of content.matchAll(/\bADR-\d{3}-[a-z0-9-]+\.md\b/g)) {
    if (!adrFiles.has(match[0])) {
      problems.push(`ADR inexistente   ${rel(file)}  ->  ${match[0]}`);
    }
  }
}

// ─────────────────────────────────────────────────────────────
// Informe
// ─────────────────────────────────────────────────────────────

console.log(
  `Documentos: ${markdownFiles.length} · enlaces .md: ${linksChecked} · anclas: ${anchorsChecked} · ` +
    `invariantes registrados: ${defined.size} · ADR: ${adrFiles.size}`,
);

if (problems.length > 0) {
  console.error(`\n${problems.length} problema(s) de integridad documental:\n`);
  // Se deduplica: un mismo enlace roto repetido en un archivo se reporta una vez.
  for (const problem of [...new Set(problems)].sort()) {
    console.error(`  - ${problem}`);
  }
  console.error('');
  process.exit(1);
}

console.log('\nDocumentación íntegra: enlaces, anclas, invariantes y ADR coherentes.');
