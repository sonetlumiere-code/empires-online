/**
 * Contract tests del protocolo v1 (lado TypeScript).
 *
 * Verifican que el contrato hace lo que la especificación promete:
 * rechaza lo inválido, distingue versión no soportada de mensaje inválido,
 * y mantiene sincronizados los esquemas Zod con el JSON Schema exportado.
 * Ver ../../../../docs/testing/contract-tests.md
 */
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import Ajv from 'ajv';
import addFormats from 'ajv-formats';
import { zodToJsonSchema } from 'zod-to-json-schema';

import { ClientMessage, CLIENT_MESSAGE_TYPES } from './client.js';
import { ServerMessage, SERVER_MESSAGE_TYPES } from './server.js';
import { ERROR_CODES } from './errors.js';
import { parseClientMessage, parseServerMessage, TRANSPORT_LIMITS } from './index.js';

const here = dirname(fileURLToPath(import.meta.url));
const schemaDir = resolve(here, '..', '..', 'schema', 'v1');

const uuid = '3f2504e0-4f89-41d3-9a0c-0305e82c3301';

function clientMove(overrides: Record<string, unknown> = {}) {
  return {
    v: 1,
    type: 'unit.move',
    requestId: uuid,
    payload: { unitId: 42, target: { x: 120, y: 88 } },
    ...overrides,
  };
}

describe('envelope cliente → servidor', () => {
  it('acepta un unit.move bien formado', () => {
    const result = parseClientMessage(clientMove());
    expect(result.ok).toBe(true);
  });

  it('rechaza un tipo desconocido', () => {
    const result = parseClientMessage({ v: 1, type: 'unit.teleport', requestId: uuid, payload: {} });
    expect(result).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('distingue versión no soportada de mensaje inválido', () => {
    const result = parseClientMessage({ ...clientMove(), v: 2 });
    expect(result).toMatchObject({ ok: false, code: 'UNSUPPORTED_VERSION' });
  });

  it('exige requestId con formato UUID', () => {
    const result = parseClientMessage(clientMove({ requestId: 'no-es-un-uuid' }));
    expect(result).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('rechaza coordenadas no enteras: el mundo es un grid de tiles', () => {
    const result = parseClientMessage({
      v: 1,
      type: 'unit.move',
      requestId: uuid,
      payload: { unitId: 42, target: { x: 120.5, y: 88 } },
    });
    expect(result).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('rechaza un payload que intente aportar estado autoritativo desconocido', () => {
    // El servidor es la única autoridad: un payload con campos extra se rechaza
    // en lugar de ignorarse en silencio.
    const result = parseClientMessage({
      v: 1,
      type: 'unit.move',
      requestId: uuid,
      payload: { unitId: 42, target: { x: 1, y: 1 }, hp: 9999 },
    });
    expect(result.ok).toBe(false);
  });

  it('unit.move no admite que el cliente envíe una ruta', () => {
    const result = parseClientMessage({
      v: 1,
      type: 'unit.move',
      requestId: uuid,
      payload: { unitId: 42, target: { x: 1, y: 1 }, path: [{ x: 0, y: 0, tMs: 0 }] },
    });
    expect(result.ok).toBe(false);
  });
});

describe('envelope servidor → cliente', () => {
  it('acepta unit.movement.started con la polilínea temporizada completa', () => {
    const result = parseServerMessage({
      v: 1,
      type: 'unit.movement.started',
      seq: 7,
      ts: 1_757_376_000_000,
      requestId: uuid,
      payload: {
        unitId: 42,
        movement: {
          movementId: 900,
          path: [
            { x: 10, y: 10, tMs: 0 },
            { x: 11, y: 10, tMs: 600 },
            { x: 12, y: 11, tMs: 1449 },
          ],
          startTimeMs: 1_757_376_000_000,
          arrivalTimeMs: 1_757_376_001_449,
          target: { x: 12, y: 11 },
        },
      },
    });
    expect(result.ok).toBe(true);
  });

  it('exige una polilínea no vacía', () => {
    const result = parseServerMessage({
      v: 1,
      type: 'unit.movement.started',
      seq: 1,
      ts: 1,
      payload: {
        unitId: 1,
        movement: {
          movementId: 1,
          path: [],
          startTimeMs: 1,
          arrivalTimeMs: 1,
          target: { x: 0, y: 0 },
        },
      },
    });
    expect(result.ok).toBe(false);
  });

  it('rechaza un código de error fuera del catálogo estable', () => {
    const result = parseServerMessage({
      v: 1,
      type: 'system.error',
      seq: 1,
      ts: 1,
      payload: { code: 'ALGO_RARO', message: 'x' },
    });
    expect(result.ok).toBe(false);
  });

  it('acepta system.error con cada código del catálogo', () => {
    for (const code of ERROR_CODES) {
      const result = parseServerMessage({
        v: 1,
        type: 'system.error',
        seq: 1,
        ts: 1,
        payload: { code, message: 'mensaje humano' },
      });
      expect(result.ok, `el código ${code} debería ser válido`).toBe(true);
    }
  });
});

describe('cobertura del catálogo de mensajes', () => {
  it('cada tipo declarado cliente → servidor existe en la unión discriminada', () => {
    const options = ClientMessage.options.map((o) => o.shape.type.value);
    expect(new Set(options)).toEqual(new Set(CLIENT_MESSAGE_TYPES));
  });

  it('cada tipo declarado servidor → cliente existe en la unión discriminada', () => {
    const options = ServerMessage.options.map((o) => o.shape.type.value);
    expect(new Set(options)).toEqual(new Set(SERVER_MESSAGE_TYPES));
  });
});

describe('JSON Schema exportado', () => {
  const files = ['client-message.schema.json', 'server-message.schema.json'] as const;

  it.each(files)('%s existe y es JSON Schema válido para AJV', (filename) => {
    const raw = readFileSync(join(schemaDir, filename), 'utf8');
    const schema = JSON.parse(raw);
    const ajv = new Ajv({ strict: false, allErrors: true });
    addFormats(ajv);
    expect(() => ajv.compile(schema)).not.toThrow();
  });

  it('no ha derivado respecto de los esquemas Zod (misma comprobación que la CI)', () => {
    const expected = {
      'client-message.schema.json': zodToJsonSchema(ClientMessage, {
        name: 'ClientMessage',
        $refStrategy: 'root',
        target: 'jsonSchema7',
      }),
      'server-message.schema.json': zodToJsonSchema(ServerMessage, {
        name: 'ServerMessage',
        $refStrategy: 'root',
        target: 'jsonSchema7',
      }),
    };
    for (const [filename, schema] of Object.entries(expected)) {
      const onDisk = JSON.parse(readFileSync(join(schemaDir, filename), 'utf8'));
      expect(onDisk, `${filename} está desactualizado: ejecuta pnpm run protocol:build`).toEqual(schema);
    }
  });

  it('un unit.move válido pasa también la validación por JSON Schema', () => {
    const schema = JSON.parse(readFileSync(join(schemaDir, 'client-message.schema.json'), 'utf8'));
    const ajv = new Ajv({ strict: false, allErrors: true });
    addFormats(ajv);
    const validate = ajv.compile(schema);
    expect(validate(clientMove())).toBe(true);
  });
});

describe('límites de transporte', () => {
  it('coinciden con los valores por defecto de la configuración EO_WS_*', () => {
    expect(TRANSPORT_LIMITS.maxMessageBytes).toBe(16_384);
    expect(TRANSPORT_LIMITS.rateLimitPerSecond).toBe(20);
    expect(TRANSPORT_LIMITS.rateLimitBurst).toBe(40);
  });
});
