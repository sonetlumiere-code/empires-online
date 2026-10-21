/**
 * Carga el `.env` de la raíz del repositorio en `process.env`.
 *
 * Mismas tres reglas que `config.LoadDotEnv` del Game Server:
 *   1. NUNCA pisa una variable ya definida: el entorno real manda.
 *   2. Si no hay archivo, no pasa nada: en producción no lo hay.
 *   3. Busca hacia arriba desde el directorio actual, porque Next.js arranca en
 *      `apps/web` y el `.env` del proyecto vive en la raíz.
 *
 * JavaScript plano a propósito: lo usan también los scripts de Node, que no
 * compilan TypeScript.
 */
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';

/** @param {string} line */
export function parseDotEnvLine(line) {
  const trimmed = line.trim();
  if (!trimmed || trimmed.startsWith('#')) return null;
  const eq = trimmed.indexOf('=');
  if (eq <= 0) return null;
  const key = trimmed.slice(0, eq).trim();
  let value = trimmed.slice(eq + 1).trim();
  if (value.length >= 2 && (value[0] === '"' || value[0] === "'") && value.at(-1) === value[0]) {
    value = value.slice(1, -1);
  }
  return [key, value];
}

/**
 * @param {string} [from] directorio desde el que buscar
 * @param {Record<string, string | undefined>} [env] destino; por defecto process.env
 * @returns {string | null} la ruta cargada, o null si no encontró ninguna
 */
export function loadRootDotEnv(from = process.cwd(), env = process.env) {
  let dir = from;
  for (;;) {
    const candidate = join(dir, '.env');
    if (existsSync(candidate)) {
      for (const line of readFileSync(candidate, 'utf8').split(/\r?\n/)) {
        const kv = parseDotEnvLine(line);
        if (kv && env[kv[0]] === undefined) env[kv[0]] = kv[1];
      }
      return candidate;
    }
    const parent = dirname(dir);
    if (parent === dir) return null;
    dir = parent;
  }
}
