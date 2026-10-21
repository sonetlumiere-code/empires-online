import { describe, expect, it } from 'vitest';

import { DEV_PLAYERS, uuidV5 } from './seed-identity.mjs';

/**
 * Los mismos valores que fija `TestLosIdsDeDesarrolloSonDeterministas` en
 * internal/devseed: la cuenta de cada jugador de desarrollo tiene que llevar el
 * id con el que `cmd/devseed` lo fundó en el mundo (ADR-018).
 */
describe('seed-identity', () => {
  it('calcula el mismo UUID v5 que Go', () => {
    expect(Object.fromEntries(DEV_PLAYERS.map((n) => [n, uuidV5(n)]))).toEqual({
      dev_norte: '5802b8b1-cdb1-5e7a-83f0-f32e243688bd',
      dev_sur: '8211d668-939b-5d33-8614-983ac4814ee0',
      dev_este: '704568fc-a197-5c7f-82de-b071e2abf42c',
      dev_oeste: '112988a8-a53f-5bd9-8586-d760dca48c6b',
    });
  });

  it('coincide con el vector de la RFC para el espacio DNS', () => {
    expect(uuidV5('www.example.com', '6ba7b810-9dad-11d1-80b4-00c04fd430c8')).toBe(
      '2ed6657d-e927-568b-95e1-2665a8aea6a2',
    );
  });
});
