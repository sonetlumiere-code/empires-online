import { describe, expect, it } from 'vitest';

import { CIVILIZATIONS, civilizationLabel, traitLines, traitPercent, traitSummary } from './civilizations';

describe('civilizaciones en el cliente (SPEC-CIVILIZATION)', () => {
  it('las cuatro del catálogo, en su orden', () => {
    expect(CIVILIZATIONS.map((c) => c.code)).toEqual(['ROMAN', 'BYZANTINE', 'PERSIAN', 'NORSE']);
  });

  it('el nombre sale del catálogo; un código desconocido se enseña tal cual', () => {
    expect(civilizationLabel('NORSE')).toBe('Nórdicos');
    expect(civilizationLabel('AZTEC')).toBe('AZTEC');
  });

  it('un rasgo por mil se lee como porcentaje con signo (Q9)', () => {
    expect(traitPercent(1100)).toBe('+10 %');
    expect(traitPercent(950)).toBe('−5 %');
    expect(traitPercent(1150)).toBe('+15 %');
    expect(traitPercent(1005)).toBe('+0,5 %');
  });

  it('lo que da cada una va antes que lo que cuesta, y lo neutro no se enseña', () => {
    const textos = (code: string) => traitLines(CIVILIZATIONS.find((c) => c.code === code)!).map((l) => l.text);
    expect(textos('ROMAN')).toEqual(['+10 % velocidad de obras y entrenamiento']);
    expect(textos('BYZANTINE')).toEqual(['+20 % vida de las casas', '−5 % velocidad de las unidades']);
    expect(textos('PERSIAN')).toEqual([
      '+5 % velocidad de las unidades',
      '+10 % velocidad de recolección',
      '−5 % velocidad de obras y entrenamiento',
    ]);
    expect(textos('NORSE')).toEqual([
      '+10 % velocidad de las unidades',
      '+15 % botín por guerrero',
      '−10 % velocidad de obras y entrenamiento',
    ]);
  });

  it('el resumen junta las líneas', () => {
    expect(traitSummary('BYZANTINE')).toBe('+20 % vida de las casas, −5 % velocidad de las unidades');
    expect(traitSummary('AZTEC')).toBe('');
  });
});
