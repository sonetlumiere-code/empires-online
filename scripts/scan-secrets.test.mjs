/**
 * Tests de scripts/scan-secrets.mjs (INV-SEC-006).
 *
 * Las muestras de credencial se construyen concatenando trozos: así este fichero
 * no contiene ninguna literal, y el escaneo del árbol no lo marca a él.
 *
 *   node --test "scripts/*.test.mjs"
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { RULES, SECRET_ENV, isPlaceholder, loadAllowlist, mask, scanBundle, scanLine } from './scan-secrets.mjs';

const j = (...p) => p.join('');
const SAMPLES = {
  'private-key': j('-----BEGIN ', 'RSA PRIVATE', ' KEY-----'),
  'aws-access-key': j('AKIA', 'IOSFODNN7EXAMPLE'),
  'github-token': j('ghp_', 'a'.repeat(36)),
  'slack-token': j('xoxb', '-123456789012-abcdef'),
  'stripe-live-key': j('sk_', 'live_', 'a1B2c3D4e5F6g7H8i9J0k1L2'),
  'google-api-key': j('AIza', 'Sy', 'A'.repeat(33)),
  'llm-api-key': j('sk-', 'ant-', 'x'.repeat(40)),
  'npm-token': j('npm_', 'b'.repeat(36)),
  'signed-jwt': j('eyJ', 'hbGciOiJIUzI1NiJ9', '.', 'eyJ', 'zdWIiOiJ4In0abcdef', '.', 'c2lnbmF0dXJlLWZpcm1hZGE'),
  'url-with-password': j('postgres://app:', 'S3cr3t-Real-Pw', '@db.example.com:5432/x'),
  'secret-assignment': j('EO_AUTH_JWT_', 'SECRET=', 'q8Zr2Lx0Vt7Kp4Nw'),
};

test('cada regla detecta su muestra, y sólo hay reglas con muestra', () => {
  for (const rule of RULES) {
    const sample = SAMPLES[rule.id];
    assert.ok(sample, `la regla ${rule.id} no tiene muestra en el test`);
    const found = scanLine(sample, 'config/.env', []);
    assert.ok(found.some((f) => f.rule === rule.id), `${rule.id} no detectó su muestra`);
  }
  assert.equal(Object.keys(SAMPLES).length, RULES.length);
});

test('una URL con contraseña se detecta en código, en configuración y en documentación', () => {
  // Redis suele llevar la contraseña sin usuario.
  assert.equal(scanLine(j('redis://:', 'R3dis-Real-Pw-01', '@cache.example.com:6379/0'), 'deploy/x.yml')[0]?.value, 'R3dis-Real-Pw-01');
  for (const path of ['internal/x.go', 'apps/web/x.ts', 'docs/x.md', '.github/workflows/ci.yml']) {
    assert.equal(scanLine(SAMPLES['url-with-password'], path)[0]?.value, 'S3cr3t-Real-Pw', path);
  }
});

test('la asignación a una variable de secreto sólo cuenta fuera del código', () => {
  const linea = SAMPLES['secret-assignment'];
  assert.equal(scanLine(linea, '.env.example').length, 1);
  assert.equal(scanLine(linea, 'docs/operations/configuration.md').length, 1);
  assert.equal(scanLine(linea, 'deploy/compose.yml').length, 1);
  // En Go o TypeScript, un nombre así va seguido de una expresión, no de un valor.
  assert.equal(scanLine(j('EO_AUTH_JWT_', 'SECRET: NewSecret(v.str("EO_AUTH_JWT_SECRET", ""))'), 'internal/config/config.go').length, 0);
});

test('los marcadores y las referencias no son secretos', () => {
  for (const v of ['', 'corto', '${EO_AUTH_JWT_SECRET}', '$EO_X', '${{ secrets.TOKEN }}', '<contraseña>', 'REDACTED', '[REDACTED]', 'changeme', 'OTRA_VARIABLE', '.*|EO_X=.*', '$(openssl rand -base64 48)']) {
    assert.ok(isPlaceholder(v), `«${v}» debería ser un marcador`);
  }
  assert.ok(!isPlaceholder('q8Zr2Lx0Vt7Kp4Nw'));
  assert.equal(scanLine(j('EO_AUTH_JWT_', 'SECRET='), '.env.example').length, 0);
  assert.equal(scanLine(j('EO_AUTH_JWT_', 'SECRET=${EO_AUTH_JWT_SECRET}'), 'deploy/compose.yml').length, 0);
});

test('la lista de permitidos admite por valor, por ruta y por regla, y nada más', () => {
  const lista = [
    { value: 'S3cr3t-Real-Pw', reason: 'prueba' },
    { path: 'fixtures', rules: ['signed-jwt'], reason: 'prueba' },
  ];
  assert.equal(scanLine(SAMPLES['url-with-password'], 'docs/x.md', lista).length, 0);
  assert.equal(scanLine(SAMPLES['signed-jwt'], 'fixtures/a.json', lista).length, 0);
  assert.equal(scanLine(SAMPLES['signed-jwt'], 'otra/a.json', lista).length, 1, 'otra ruta');
  assert.equal(scanLine(SAMPLES['github-token'], 'fixtures/a.json', lista).length, 1, 'otra regla');
  assert.equal(scanLine(SAMPLES['signed-jwt'], 'fixturesX/a.json', lista).length, 1, 'un prefijo no es la carpeta');
});

test('la lista real se carga, y una entrada sin motivo se rechaza', () => {
  assert.ok(loadAllowlist().length > 0);
  const dir = mkdtempSync(join(tmpdir(), 'eo-scan-'));
  try {
    writeFileSync(join(dir, 'a.json'), JSON.stringify({ entries: [{ value: 'x' }] }));
    assert.throws(() => loadAllowlist(join(dir, 'a.json')), /sin motivo/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('el informe nunca muestra un valor entero', () => {
  assert.equal(mask('corto'), '*****');
  const m = mask('q8Zr2Lx0Vt7Kp4Nw');
  assert.ok(!m.includes('q8Zr2Lx0Vt7Kp4Nw'));
  assert.match(m, /16 caracteres/);
});

test('el bundle no puede contener el valor de ninguna variable secreta, ni marcas de servidor', () => {
  const dir = mkdtempSync(join(tmpdir(), 'eo-bundle-'));
  const env = Object.fromEntries(SECRET_ENV.map((k, i) => [k, `canario-${i}-${'z'.repeat(12)}`]));
  try {
    mkdirSync(join(dir, 'chunks'));
    writeFileSync(join(dir, 'chunks', 'limpio.js'), 'console.log("hola")');
    let r = scanBundle(dir, env);
    assert.deepEqual(r.findings, []);
    assert.deepEqual(r.canaries, SECRET_ENV);

    writeFileSync(join(dir, 'chunks', 'fuga.js'), `const s = "${env.EO_AUTH_JWT_SECRET}";`);
    r = scanBundle(dir, env);
    assert.ok(r.findings.some((f) => f.rule === 'canary' && f.where.includes('EO_AUTH_JWT_SECRET')));

    rmSync(join(dir, 'chunks', 'fuga.js'));
    writeFileSync(join(dir, 'chunks', 'servidor.js'), 'const aud = "game-server-internal";');
    r = scanBundle(dir, env);
    assert.ok(r.findings.some((f) => f.rule === 'server-marker'));

    rmSync(join(dir, 'chunks', 'servidor.js'));
    writeFileSync(join(dir, 'chunks', 'map.js.map'), j('{"x":"', SAMPLES['aws-access-key'], '"}'));
    r = scanBundle(dir, env);
    assert.ok(r.findings.some((f) => f.rule === 'aws-access-key'), 'también en los .map');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('sin el valor en el entorno no hay canario que buscar, y se dice', () => {
  const dir = mkdtempSync(join(tmpdir(), 'eo-bundle-'));
  try {
    assert.deepEqual(scanBundle(dir, {}).canaries, []);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
