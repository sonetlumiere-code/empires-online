import { test } from 'node:test';
import assert from 'node:assert/strict';

import { lintSource, lintTree, stripCommentsAndStrings } from './lint-determinism.mjs';

test('detecta time.Now, time.Since y time.Until', () => {
  const src = 'package x\nfunc f() {\n\ta := time.Now()\n\tb := time.Since(a)\n\t_ = time.Until(a)\n}\n';
  assert.deepEqual(lintSource(src).map((p) => p.line), [3, 4, 5]);
});

test('detecta math/rand y math/rand/v2, con alias o sin él', () => {
  const src = 'package x\nimport (\n\t"math/rand"\n\tr2 "math/rand/v2"\n\t"math/randomness"\n)\n';
  assert.deepEqual(lintSource(src).map((p) => p.line), [3, 4]);
});

test('ignora comentarios y cadenas que mencionan el reloj', () => {
  const src = [
    'package x',
    '// nunca llames a time.Now() aquí',
    '/* ni a time.Since(x)',
    '   en un bloque */',
    'var s = "time.Now()"',
    'var r = `time.Now()`',
    '// import "math/rand"',
  ].join('\n');
  assert.deepEqual(lintSource(src), []);
});

test('el marcador con motivo permite la línea, en ella o en la anterior', () => {
  const src = [
    'package x',
    'func f() {',
    '\tstart := time.Now() //lint:reloj-real mide la duración para la métrica',
    '\t//lint:reloj-real ídem',
    '\t_ = time.Since(start)',
    '}',
  ].join('\n');
  assert.deepEqual(lintSource(src), []);
});

test('un marcador sin motivo no vale', () => {
  const src = 'package x\nfunc f() { _ = time.Now() //lint:reloj-real\n}\n';
  assert.equal(lintSource(src).length, 1);
});

test('quitar comentarios y cadenas conserva los saltos de línea', () => {
  const src = 'a\n/* b\nc */\n"d"\n';
  assert.equal(stripCommentsAndStrings(src).split('\n').length, src.split('\n').length);
});

test('el árbol real del Game Server está limpio', () => {
  assert.deepEqual(lintTree(), []);
});
