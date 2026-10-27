import { describe, expect, it } from 'vitest';

import { DEFAULT_LIMIT, internalBaseUrl, parseLimit } from './identity-sweep.mjs';

describe('identity:sweep', () => {
  it('consulta al listener interno del Game Server (8081), no al público, y respeta la variable', () => {
    expect(internalBaseUrl({})).toBe('http://localhost:8081');
    expect(internalBaseUrl({ EO_GAME_SERVER_INTERNAL_URL: 'http://mundo:9001/' })).toBe('http://mundo:9001');
  });

  it('sin argumento resuelve hasta el límite por defecto', () => {
    expect(parseLimit(undefined)).toBe(DEFAULT_LIMIT);
    expect(parseLimit('50')).toBe(50);
  });

  it('rechaza un límite que no es un entero positivo', () => {
    for (const raw of ['0', '-1', '2.5', 'muchas', '100001']) expect(() => parseLimit(raw)).toThrow(/límite inválido/);
  });
});
