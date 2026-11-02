/**
 * Tests de las funciones puras de scripts/pg-local.mjs.
 *
 *   node --test "scripts/*.test.mjs"
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { DEFAULT_PORT, parsePort, pgEnv, upsertEnv } from './pg-local.mjs';

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

test('parsePort: sin EO_PG_PORT, el 5433 de siempre', () => {
  assert.equal(parsePort(undefined), DEFAULT_PORT);
  assert.equal(parsePort(''), DEFAULT_PORT);
  assert.equal(DEFAULT_PORT, 5433);
});

test('parsePort: acepta otro puerto cuando el 5433 está ocupado', () => {
  assert.equal(parsePort('5434'), 5434);
});

test('parsePort: rechaza valores que no son un puerto utilizable', () => {
  for (const malo of ['abc', '54.3', '80', '70000', '-1']) {
    assert.throws(() => parsePort(malo), /EO_PG_PORT inválido/);
  }
});

test('pgEnv: en macOS sin LC_ALL fija un locale válido (si no, el postmaster aborta)', () => {
  assert.equal(pgEnv({}, 'darwin').LC_ALL, 'en_US.UTF-8');
  assert.equal(pgEnv({ LC_ALL: 'es_AR.UTF-8' }, 'darwin').LC_ALL, 'es_AR.UTF-8');
  assert.equal(pgEnv({}, 'win32').LC_ALL, undefined);
  assert.equal(pgEnv({}, 'linux').PGCLIENTENCODING, 'UTF8');
});
