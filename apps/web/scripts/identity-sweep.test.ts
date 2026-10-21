import { describe, expect, it } from 'vitest';

import { DEFAULT_LIMIT, parseLimit } from './identity-sweep.mjs';

describe('identity:sweep', () => {
  it('sin argumento resuelve hasta el límite por defecto', () => {
    expect(parseLimit(undefined)).toBe(DEFAULT_LIMIT);
    expect(parseLimit('50')).toBe(50);
  });

  it('rechaza un límite que no es un entero positivo', () => {
    for (const raw of ['0', '-1', '2.5', 'muchas', '100001']) expect(() => parseLimit(raw)).toThrow(/límite inválido/);
  });
});
