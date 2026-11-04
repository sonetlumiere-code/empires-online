import { describe, expect, it } from 'vitest';

import {
  anyCountdown,
  applyTreatyUpdate,
  formatCountdown,
  proposableTypes,
  treatyActions,
  treatyLabel,
  type TreatyView,
} from './diplomacy';

// SPEC-DIPLOMACY en el cliente: lo puro (frontend.md §4.5).

const base: TreatyView = {
  id: 3, otherPlayerId: '7c9e6679-7425-40de-944b-e07fc1f90ae7', otherCityId: 7, otherCityName: 'Vecinópolis',
  type: 'ALLIANCE', status: 'PROPOSED', proposedByMe: false, breaksAtMs: null,
};

describe('applyTreatyUpdate: la lista de tratados abiertos', () => {
  it('añade, sustituye y ordena por id; quita lo que termina', () => {
    let lista = applyTreatyUpdate([], { ...base, id: 5 });
    lista = applyTreatyUpdate(lista, base);
    expect(lista.map((t) => t.id)).toEqual([3, 5]);
    lista = applyTreatyUpdate(lista, { ...base, status: 'ACTIVE' });
    expect(lista.find((t) => t.id === 3)?.status).toBe('ACTIVE');
    for (const fin of ['REJECTED', 'WITHDRAWN', 'EXPIRED', 'BROKEN'] as const) {
      expect(applyTreatyUpdate(lista, { ...base, status: fin }).map((t) => t.id)).toEqual([5]);
    }
  });
});

describe('treatyActions: qué ofrece el HUD', () => {
  it('oferta recibida: aceptar o rechazar; propia: retirar; vigente: romper una vez', () => {
    expect(treatyActions(base)).toEqual(['accept', 'reject']);
    expect(treatyActions({ ...base, proposedByMe: true })).toEqual(['withdraw']);
    expect(treatyActions({ ...base, status: 'ACTIVE' })).toEqual(['break']);
    expect(treatyActions({ ...base, status: 'ACTIVE', breaksAtMs: 10 })).toEqual([]);
  });
});

describe('treatyLabel y la cuenta atrás del preaviso', () => {
  it('dice quién ofrece qué, y cuánto falta para la ruptura', () => {
    expect(treatyLabel(base, 0)).toBe('Vecinópolis te ofrece alianza');
    expect(treatyLabel({ ...base, proposedByMe: true, type: 'NON_AGGRESSION' }, 0)).toBe('Ofreces pacto de no agresión a Vecinópolis');
    expect(treatyLabel({ ...base, status: 'ACTIVE' }, 0)).toBe('Alianza con Vecinópolis');
    expect(treatyLabel({ ...base, status: 'ACTIVE', breaksAtMs: 90_500 }, 0)).toBe('Alianza con Vecinópolis: se rompe en 1:31');
    expect(formatCountdown(-5)).toBe('0:00');
    expect(anyCountdown([base, { ...base, breaksAtMs: 1 }])).toBe(true);
    expect(anyCountdown([base])).toBe(false);
  });
});

describe('proposableTypes: la escalera de tratados (SPEC-TRADE §4.1)', () => {
  const otro = base.otherPlayerId;
  it('sin tratado, los tres; con uno vigente, sólo los de arriba', () => {
    expect(proposableTypes([], otro)).toEqual(['TRADE', 'NON_AGGRESSION', 'ALLIANCE']);
    expect(proposableTypes([{ ...base, type: 'TRADE', status: 'ACTIVE' }], otro)).toEqual(['NON_AGGRESSION', 'ALLIANCE']);
    expect(proposableTypes([{ ...base, type: 'NON_AGGRESSION', status: 'ACTIVE' }], otro)).toEqual(['ALLIANCE']);
    expect(proposableTypes([{ ...base, status: 'ACTIVE' }], otro)).toEqual([]);
  });
  it('nada con una oferta pendiente o en preaviso; otro jugador no cuenta', () => {
    expect(proposableTypes([base], otro)).toEqual([]);
    expect(proposableTypes([{ ...base, type: 'TRADE', status: 'ACTIVE', breaksAtMs: 5 }], otro)).toEqual([]);
    expect(proposableTypes([{ ...base, status: 'ACTIVE' }], 'otro-jugador')).toHaveLength(3);
  });
});
