/**
 * Tests de las funciones puras de scripts/smoke.mjs.
 *
 * El resto del script son llamadas de red y no se testea aquí: su verificación
 * es ejecutarlo contra un servidor de verdad. Lo que sí necesita test es la
 * decodificación del terreno, porque un error ahí no se manifiesta como un
 * error: se manifiesta como un destino intransitable, un `unit.move.rejected`
 * y un diagnóstico que apunta al servidor cuando el problema estaba en el
 * cliente de prueba.
 *
 * Runner integrado de Node (>= 22), sin dependencias:
 *   node --test scripts/
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { elegirDestino, indexarTerreno } from './smoke.mjs';

// Terrenos según internal/game/world/tile.go
const GRASSLAND = 0;
const MOUNTAIN = 3;
const WATER = 4;
const ROAD = 5;

/** Construye un chunk como lo serializa el servidor: filas contiguas, base64. */
function chunkDesdeFilas(cx, cy, filas) {
  const size = filas.length;
  const bytes = Buffer.alloc(size * size);
  filas.forEach((fila, y) => {
    assert.equal(fila.length, size, 'el mapa de prueba debe ser cuadrado');
    fila.forEach((tipo, x) => {
      bytes[y * size + x] = tipo;
    });
  });
  return { cx, cy, size, terrain: bytes.toString('base64') };
}

test('indexarTerreno respeta el orden por filas del servidor', () => {
  // Asimétrico a propósito: si se confundieran filas y columnas, un mapa
  // simétrico pasaría el test igualmente y no probaría nada.
  const chunk = chunkDesdeFilas(0, 0, [
    [GRASSLAND, WATER],
    [ROAD, MOUNTAIN],
  ]);

  const mapa = indexarTerreno([chunk]);

  assert.equal(mapa.get('0,0'), GRASSLAND);
  assert.equal(mapa.get('1,0'), WATER, 'x avanza dentro de la fila');
  assert.equal(mapa.get('0,1'), ROAD, 'y avanza entre filas');
  assert.equal(mapa.get('1,1'), MOUNTAIN);
});

test('indexarTerreno desplaza cada chunk a sus coordenadas de mundo', () => {
  const chunk = chunkDesdeFilas(2, 3, [
    [GRASSLAND, ROAD],
    [WATER, MOUNTAIN],
  ]);

  const mapa = indexarTerreno([chunk]);

  // El chunk (2,3) de tamaño 2 empieza en el tile (4,6).
  assert.equal(mapa.get('4,6'), GRASSLAND);
  assert.equal(mapa.get('5,6'), ROAD);
  assert.equal(mapa.get('4,7'), WATER);
  assert.equal(mapa.get('5,7'), MOUNTAIN);
  assert.equal(mapa.size, 4);
});

test('indexarTerreno combina varios chunks sin pisarse', () => {
  const mapa = indexarTerreno([
    chunkDesdeFilas(0, 0, [[GRASSLAND, GRASSLAND], [GRASSLAND, GRASSLAND]]),
    chunkDesdeFilas(1, 0, [[WATER, WATER], [WATER, WATER]]),
  ]);

  assert.equal(mapa.size, 8);
  assert.equal(mapa.get('1,0'), GRASSLAND);
  assert.equal(mapa.get('2,0'), WATER, 'el segundo chunk empieza en x=2');
});

test('elegirDestino nunca devuelve un tile intransitable', () => {
  const mapa = new Map();
  for (let y = 0; y < 30; y++) {
    for (let x = 0; x < 30; x++) {
      // Todo agua salvo una única isla transitable a distancia 8.
      mapa.set(`${x},${y}`, x === 8 && y === 0 ? GRASSLAND : WATER);
    }
  }

  const destino = elegirDestino(mapa, { x: 0, y: 0 });

  assert.deepEqual({ x: destino.x, y: destino.y }, { x: 8, y: 0 });
});

test('elegirDestino descarta lo que queda fuera del rango de distancia', () => {
  const mapa = new Map();
  mapa.set('0,0', GRASSLAND);
  mapa.set('3,0', GRASSLAND); // demasiado cerca (< 6)
  mapa.set('40,0', GRASSLAND); // demasiado lejos (> 12)

  assert.equal(elegirDestino(mapa, { x: 0, y: 0 }), null);
});

test('elegirDestino es determinista ante empates', () => {
  const mapa = new Map();
  // Tres candidatos a la MISMA distancia de Chebyshev (10).
  for (const [x, y] of [[10, 4], [10, 2], [4, 10]]) mapa.set(`${x},${y}`, GRASSLAND);

  const primero = elegirDestino(mapa, { x: 0, y: 0 });
  const segundo = elegirDestino(new Map([...mapa.entries()].reverse()), { x: 0, y: 0 });

  // Desempate por y y luego x: (10,2) gana a (10,4) y a (4,10).
  assert.deepEqual({ x: primero.x, y: primero.y }, { x: 10, y: 2 });
  assert.deepEqual(
    { x: segundo.x, y: segundo.y },
    { x: primero.x, y: primero.y },
    'el orden de inserción del Map no debe cambiar el resultado',
  );
});

test('elegirDestino prefiere el candidato más lejano dentro del rango', () => {
  const mapa = new Map();
  mapa.set('6,0', GRASSLAND);
  mapa.set('12,0', GRASSLAND);

  const destino = elegirDestino(mapa, { x: 0, y: 0 });

  // Cuanto más largo el trayecto, más margen tiene el paso de la desconexión.
  assert.equal(destino.x, 12);
});
