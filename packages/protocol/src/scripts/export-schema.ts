/**
 * Exporta los esquemas Zod del protocolo a JSON Schema.
 *
 * Escribe en DOS destinos:
 *   1. packages/protocol/schema/v1/          — artefacto publicado del paquete
 *   2. services/game-server/internal/protocol/schema/v1/ — consumido por `go:embed`
 *
 * `go:embed` no puede salir del módulo Go, de ahí el espejo. Ambos destinos se
 * versionan en git para que un cambio de contrato sea visible en el diff.
 *
 * Uso:
 *   tsx src/scripts/export-schema.ts           # regenera
 *   tsx src/scripts/export-schema.ts --check   # falla si hay deriva (usado en CI)
 */
import { mkdirSync, readFileSync, existsSync, writeFileSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { zodToJsonSchema } from 'zod-to-json-schema';

import { ClientMessage } from '../v1/client.js';
import { ServerMessage } from '../v1/server.js';
import { ERROR_CODES } from '../v1/errors.js';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const packageRoot = resolve(scriptDir, '..', '..');
const repoRoot = resolve(packageRoot, '..', '..');

const TARGET_DIRS = [
  join(packageRoot, 'schema', 'v1'),
  join(repoRoot, 'services', 'game-server', 'internal', 'protocol', 'schema', 'v1'),
];

interface Artifact {
  readonly filename: string;
  readonly content: string;
}

function toSchema(name: string, schema: Parameters<typeof zodToJsonSchema>[0]): string {
  const json = zodToJsonSchema(schema, {
    name,
    $refStrategy: 'root',
    target: 'jsonSchema7',
  });
  // Salida estable y con salto de línea final: cualquier cambio real produce un diff
  // legible, y ninguno espurio por formato.
  return `${JSON.stringify(json, null, 2)}\n`;
}

function buildArtifacts(): Artifact[] {
  return [
    { filename: 'client-message.schema.json', content: toSchema('ClientMessage', ClientMessage) },
    { filename: 'server-message.schema.json', content: toSchema('ServerMessage', ServerMessage) },
    {
      filename: 'error-codes.json',
      content: `${JSON.stringify({ version: 1, codes: ERROR_CODES }, null, 2)}\n`,
    },
  ];
}

function main(): void {
  const checkOnly = process.argv.includes('--check');
  const artifacts = buildArtifacts();
  const drifted: string[] = [];
  let written = 0;

  for (const dir of TARGET_DIRS) {
    if (!checkOnly) mkdirSync(dir, { recursive: true });
    for (const artifact of artifacts) {
      const target = join(dir, artifact.filename);
      const current = existsSync(target) ? readFileSync(target, 'utf8') : null;

      if (current === artifact.content) continue;

      if (checkOnly) {
        drifted.push(relative(repoRoot, target) + (current === null ? ' (falta)' : ' (desactualizado)'));
        continue;
      }
      writeFileSync(target, artifact.content, 'utf8');
      written += 1;
      console.log(`  escrito  ${relative(repoRoot, target)}`);
    }
  }

  if (checkOnly) {
    if (drifted.length > 0) {
      console.error('\nDERIVA DE ESQUEMA DETECTADA. Los siguientes artefactos no coinciden con los esquemas Zod:\n');
      for (const d of drifted) console.error(`  - ${d}`);
      console.error('\nEjecuta `pnpm run protocol:build` y versiona el resultado.\n');
      process.exit(1);
    }
    console.log('Esquemas al día: sin deriva entre Zod y JSON Schema.');
    return;
  }

  console.log(
    written === 0
      ? 'Esquemas ya actualizados: nada que escribir.'
      : `\n${written} archivo(s) de esquema actualizados.`,
  );
}

main();
