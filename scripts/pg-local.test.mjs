/**
 * Tests de las funciones puras de scripts/pg-local.mjs.
 *
 *   node --test "scripts/*.test.mjs"
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { upsertEnv } from './pg-local.mjs';

test('upsertEnv sustituye una clave vacía en su sitio, sin duplicarla', () => {
  // Así queda un .env copiado de .env.example: la clave existe y está vacía.
  // Los cargadores se quedan con la primera aparición, así que añadir otra
  // línea detrás no surtiría efecto.
  const antes = 'EO_ENV=development\nEO_IDENTITY_POSTGRES_URL=\nEO_TEST_IDENTITY_POSTGRES_URL=\nOTRA=1\n';
  const despues = upsertEnv(antes, {
    EO_IDENTITY_POSTGRES_URL: 'postgres://a',
    EO_TEST_IDENTITY_POSTGRES_URL: 'postgres://b',
  });
  assert.equal(despues, 'EO_ENV=development\nEO_IDENTITY_POSTGRES_URL=postgres://a\nEO_TEST_IDENTITY_POSTGRES_URL=postgres://b\nOTRA=1\n');
  assert.equal(despues.match(/EO_IDENTITY_POSTGRES_URL=/g).length, 1);
});

test('upsertEnv añade al final lo que no existía y respeta lo demás', () => {
  const despues = upsertEnv('EO_ENV=development\r\n# comentario\n', { EO_IDENTITY_POSTGRES_URL: 'postgres://a' });
  const lineas = despues.split('\n');
  assert.equal(lineas[0], 'EO_ENV=development');
  assert.equal(lineas[1], '# comentario');
  assert.ok(lineas.includes('EO_IDENTITY_POSTGRES_URL=postgres://a'));
});

test('upsertEnv sobre un .env que no existe lo crea', () => {
  assert.match(upsertEnv('', { A: '1' }), /^\n?# Almacén[^\n]*\nA=1\n$/);
});
