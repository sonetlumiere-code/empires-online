/**
 * Contract tests del protocolo v1 (lado TypeScript).
 *
 * Verifican que el contrato hace lo que la especificación promete:
 * rechaza lo inválido, distingue versión no soportada de mensaje inválido,
 * y mantiene sincronizados los esquemas Zod con el JSON Schema exportado.
 * Ver ../../../../docs/testing/contract-tests.md
 */
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import Ajv from 'ajv';
import addFormats from 'ajv-formats';
import { zodToJsonSchema } from 'zod-to-json-schema';

import { ClientMessage, CLIENT_MESSAGE_TYPES } from './client.js';
import { ServerMessage, SERVER_MESSAGE_TYPES } from './server.js';
import { ERROR_CODES } from './errors.js';
import { parseClientMessage, parseServerMessage, TRANSPORT_LIMITS } from './index.js';

const here = dirname(fileURLToPath(import.meta.url));
const schemaDir = resolve(here, '..', '..', 'schema', 'v1');

const uuid = '3f2504e0-4f89-41d3-9a0c-0305e82c3301';

function clientMove(overrides: Record<string, unknown> = {}) {
  return {
    v: 1,
    type: 'unit.move',
    requestId: uuid,
    payload: { unitId: 42, target: { x: 120, y: 88 } },
    ...overrides,
  };
}

describe('envelope cliente → servidor', () => {
  it('acepta un unit.move bien formado', () => {
    const result = parseClientMessage(clientMove());
    expect(result.ok).toBe(true);
  });

  it('rechaza un tipo desconocido', () => {
    const result = parseClientMessage({ v: 1, type: 'unit.teleport', requestId: uuid, payload: {} });
    expect(result).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('distingue versión no soportada de mensaje inválido', () => {
    const result = parseClientMessage({ ...clientMove(), v: 2 });
    expect(result).toMatchObject({ ok: false, code: 'UNSUPPORTED_VERSION' });
  });

  it('exige requestId con formato UUID', () => {
    const result = parseClientMessage(clientMove({ requestId: 'no-es-un-uuid' }));
    expect(result).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('rechaza coordenadas no enteras: el mundo es un grid de tiles', () => {
    const result = parseClientMessage({
      v: 1,
      type: 'unit.move',
      requestId: uuid,
      payload: { unitId: 42, target: { x: 120.5, y: 88 } },
    });
    expect(result).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('rechaza un payload que intente aportar estado autoritativo desconocido', () => {
    // El servidor es la única autoridad: un payload con campos extra se rechaza
    // en lugar de ignorarse en silencio.
    const result = parseClientMessage({
      v: 1,
      type: 'unit.move',
      requestId: uuid,
      payload: { unitId: 42, target: { x: 1, y: 1 }, hp: 9999 },
    });
    expect(result.ok).toBe(false);
  });

  it('unit.move no admite que el cliente envíe una ruta', () => {
    const result = parseClientMessage({
      v: 1,
      type: 'unit.move',
      requestId: uuid,
      payload: { unitId: 42, target: { x: 1, y: 1 }, path: [{ x: 0, y: 0, tMs: 0 }] },
    });
    expect(result.ok).toBe(false);
  });
});

describe('comandos de guarnición', () => {
  it('acepta unit.garrison con unidad y ciudad', () => {
    const result = parseClientMessage({
      v: 1, type: 'unit.garrison', requestId: uuid, payload: { unitId: 42, cityId: 7 },
    });
    expect(result.ok).toBe(true);
  });

  it('unit.garrison exige la ciudad: no hay ciudad por defecto', () => {
    const result = parseClientMessage({
      v: 1, type: 'unit.garrison', requestId: uuid, payload: { unitId: 42 },
    });
    expect(result).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('unit.ungarrison no admite que el cliente elija el tile de salida', () => {
    const ok = parseClientMessage({
      v: 1, type: 'unit.ungarrison', requestId: uuid, payload: { unitId: 42 },
    });
    expect(ok.ok).toBe(true);
    const conTile = parseClientMessage({
      v: 1, type: 'unit.ungarrison', requestId: uuid, payload: { unitId: 42, x: 3, y: 4 },
    });
    expect(conTile.ok).toBe(false);
  });
});

describe('combate (SPEC-COMBAT-RAID)', () => {
  it('acepta unit.attack con atacante y objetivo', () => {
    const result = parseClientMessage({
      v: 1, type: 'unit.attack', requestId: uuid, payload: { unitId: 42, targetUnitId: 77 },
    });
    expect(result.ok).toBe(true);
  });

  it('unit.attack no admite que el cliente aporte daño, vida ni botín', () => {
    for (const extra of [{ damage: 99 }, { hp: 0 }, { loot: 10 }, { target: { x: 1, y: 1 } }]) {
      const result = parseClientMessage({
        v: 1, type: 'unit.attack', requestId: uuid,
        payload: { unitId: 42, targetUnitId: 77, ...extra },
      });
      expect(result).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });
});

describe('saqueo (SPEC-COMBAT-PILLAGE)', () => {
  it('acepta unit.pillage con guerrero y ciudad', () => {
    const result = parseClientMessage({
      v: 1, type: 'unit.pillage', requestId: uuid, payload: { unitId: 42, cityId: 7 },
    });
    expect(result.ok).toBe(true);
  });

  it('unit.pillage no admite que el cliente aporte cantidad, tile ni suelo', () => {
    for (const extra of [{ amount: 50 }, { target: { x: 1, y: 1 } }, { floor: 0 }, { loot: 20 }]) {
      const result = parseClientMessage({
        v: 1, type: 'unit.pillage', requestId: uuid,
        payload: { unitId: 42, cityId: 7, ...extra },
      });
      expect(result).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('city.update acepta pillagedAtMs y rechaza un instante negativo', () => {
    const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
    expect(parseServerMessage({
      ...env, type: 'city.update', payload: { id: 7, pillagedAtMs: 1_757_376_000_500 },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'city.update', payload: { id: 7, pillagedAtMs: -1 },
    }).ok).toBe(false);
  });
});

describe('asedio (SPEC-COMBAT-SIEGE)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };

  it('acepta unit.demolish con guerrero y casa, y nada más', () => {
    expect(parseClientMessage({
      v: 1, type: 'unit.demolish', requestId: uuid, payload: { unitId: 42, buildingId: 3 },
    }).ok).toBe(true);
    for (const extra of [{ damage: 150 }, { target: { x: 1, y: 1 } }, { cityId: 7 }]) {
      expect(parseClientMessage({
        v: 1, type: 'unit.demolish', requestId: uuid, payload: { unitId: 42, buildingId: 3, ...extra },
      })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
    expect(parseClientMessage({
      v: 1, type: 'unit.demolish', requestId: uuid, payload: { unitId: 42 },
    }).ok).toBe(false);
  });

  it('acepta la vida en building.update y en la vista, y rechaza la negativa', () => {
    expect(parseServerMessage({
      ...env, type: 'building.update', payload: { id: 1, status: 'COMPLETE', hp: 142 },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'building.update', payload: { id: 1, status: 'COMPLETE', hp: -1 },
    }).ok).toBe(false);
    expect(parseServerMessage({
      ...env, type: 'building.spawn',
      payload: {
        building: {
          id: 1, cityId: 7, type: 'HOUSE', x: 9, y: 10, status: 'COMPLETE',
          completesAtMs: 1_757_376_015_000, hp: 150, maxHp: 150,
        },
      },
    }).ok).toBe(true);
  });

  it('acepta building.destroyed y el aviso besiegedAtMs', () => {
    expect(parseServerMessage({
      ...env, type: 'building.destroyed', payload: { id: 1, cityId: 7 },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'building.destroyed', payload: { id: 1 },
    }).ok).toBe(false);
    expect(parseServerMessage({
      ...env, type: 'city.update', payload: { id: 7, besiegedAtMs: 1_757_376_000_500 },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'city.update', payload: { id: 7, besiegedAtMs: -1 },
    }).ok).toBe(false);
  });
});

describe('diplomacia (SPEC-DIPLOMACY)', () => {
  const otro = '7c9e6679-7425-40de-944b-e07fc1f90ae7';

  it('acepta treaty.propose de los tres escalones (SPEC-TRADE Q1), y rechaza otro tipo y el flag suelto', () => {
    for (const type of ['TRADE', 'NON_AGGRESSION', 'ALLIANCE']) {
      expect(parseClientMessage({ v: 1, type: 'treaty.propose', requestId: uuid, payload: { playerId: otro, type } }).ok).toBe(true);
    }
    for (const payload of [
      { playerId: otro, type: 'VASALLAJE' },
      { playerId: otro, type: 'ALLIANCE', allowsGarrison: true },
      { playerId: 'no-es-un-uuid', type: 'ALLIANCE' },
    ]) {
      expect(parseClientMessage({ v: 1, type: 'treaty.propose', requestId: uuid, payload })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('acepta treaty.respond y treaty.cancel, y exige el sí o el no', () => {
    expect(parseClientMessage({ v: 1, type: 'treaty.respond', requestId: uuid, payload: { treatyId: 3, accept: true } }).ok).toBe(true);
    expect(parseClientMessage({ v: 1, type: 'treaty.respond', requestId: uuid, payload: { treatyId: 3 } }).ok).toBe(false);
    expect(parseClientMessage({ v: 1, type: 'treaty.cancel', requestId: uuid, payload: { treatyId: 3 } }).ok).toBe(true);
    expect(parseClientMessage({ v: 1, type: 'treaty.cancel', requestId: uuid, payload: { treatyId: 3, at: 1 } }).ok).toBe(false);
  });

  it('acepta treaty.update y treaties[] en el snapshot', () => {
    const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
    const treaty = {
      id: 3, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Vecinópolis',
      type: 'ALLIANCE', status: 'ACTIVE', proposedByMe: false, breaksAtMs: 1_757_376_900_000,
    };
    expect(parseServerMessage({ ...env, type: 'treaty.update', payload: { treaty } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'treaty.update', payload: { treaty: { ...treaty, status: 'SIGNED' } } }).ok).toBe(false);
    expect(parseServerMessage({
      ...env, type: 'world.snapshot',
      payload: { serverTimeMs: 1, tick: 1, chunks: [], terrain: [], units: [], cities: [], territories: [], buildings: [], treaties: [treaty] },
    }).ok).toBe(true);
  });
});

describe('registro (SPEC-EVENT-LOG)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const entry = {
    id: 41, kind: 'PILLAGED', firstAtMs: 1_757_375_000_000, lastAtMs: 1_757_375_060_000, count: 17,
    otherPlayerId: '00000000-0000-4000-8000-000000000002', otherCityId: 7, otherCityName: 'Villa Norte',
    amount: 85, x: 451, y: 179,
  };

  it('acepta event_log.fetch con y sin beforeId, y nada que elija a otro jugador', () => {
    expect(parseClientMessage({ v: 1, type: 'event_log.fetch', requestId: uuid, payload: {} }).ok).toBe(true);
    expect(parseClientMessage({ v: 1, type: 'event_log.fetch', requestId: uuid, payload: { beforeId: 41 } }).ok).toBe(true);
    for (const payload of [{ beforeId: 0 }, { beforeId: -1 }, { beforeId: 1.5 }, { playerId: uuid }]) {
      expect(parseClientMessage({ v: 1, type: 'event_log.fetch', requestId: uuid, payload })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('acepta event_log.seen con un id positivo y nada más', () => {
    expect(parseClientMessage({ v: 1, type: 'event_log.seen', requestId: uuid, payload: { upToId: 41 } }).ok).toBe(true);
    for (const payload of [{}, { upToId: 0 }, { upToId: 41, playerId: uuid }]) {
      expect(parseClientMessage({ v: 1, type: 'event_log.seen', requestId: uuid, payload })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('acepta event_log.page con entradas de cada tipo y la última página', () => {
    expect(parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [entry], seenUpToId: 0, nextBeforeId: 12 } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [], seenUpToId: 41, nextBeforeId: null } }).ok).toBe(true);
    const otras = [
      { ...entry, kind: 'UNIT_LOST', unitType: 'VILLAGER', amount: undefined },
      { ...entry, kind: 'TREATY_BREAK_ANNOUNCED', treatyType: 'NON_AGGRESSION', breaksAtMs: 1_757_376_900_000, otherCityId: null, otherCityName: null },
      { ...entry, kind: 'TREATY_OFFER_EXPIRED', treatyType: 'ALLIANCE', proposedByMe: true },
      // SPEC-COMBAT-SIEGE: casas derribadas, sufridas y hechas.
      { ...entry, kind: 'HOUSE_LOST', amount: undefined, count: 2 },
      { ...entry, kind: 'HOUSE_DESTROYED', amount: undefined },
    ];
    for (const e of otras) {
      expect(parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [e], seenUpToId: 0, nextBeforeId: null } }).ok).toBe(true);
    }
    for (const e of [{ ...entry, kind: 'UNIT_ATTACKED' }, { ...entry, count: 0 }, { ...entry, unitType: 'CATAPULT' }]) {
      expect(parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [e], seenUpToId: 0, nextBeforeId: null } }).ok).toBe(false);
    }
  });
});

describe('avisos en vivo (SPEC-LIVE-NOTICES)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const notice = {
    kind: 'UNIT_LOST', atMs: 1_757_375_999_900, otherPlayerId: '00000000-0000-4000-8000-000000000002',
    otherCityId: 7, otherCityName: 'Villa Norte', unitType: 'VILLAGER', x: 451, y: 179,
  };
  const parse = (payload: unknown) => parseServerMessage({ ...env, type: 'event_log.notice', payload });

  it('acepta los cuatro tipos que se avisan, con y sin ciudad del otro', () => {
    expect(parse(notice).ok).toBe(true);
    expect(parse({ ...notice, kind: 'ENEMY_KILLED', unitType: 'WARRIOR' }).ok).toBe(true);
    expect(parse({ ...notice, kind: 'PILLAGE_DONE', unitType: undefined, amount: 5 }).ok).toBe(true);
    expect(parse({ ...notice, kind: 'HOUSE_DESTROYED', unitType: undefined, otherCityId: null, otherCityName: null }).ok).toBe(true);
    expect(parse({ ...notice, x: undefined, y: undefined }).ok).toBe(true);
  });

  it('no tiene id ni recuento: no es una entrada del registro (RN-NOTICE-004)', () => {
    const shape = ServerMessage.options.find((o) => o.shape.type.value === 'event_log.notice')!.shape.payload.shape;
    expect(Object.keys(shape)).not.toContain('id');
    expect(Object.keys(shape)).not.toContain('count');
  });

  it('rechaza los tipos que no se avisan y los datos fuera de rango', () => {
    // El saqueo y el asedio sufridos ya tienen su aviso (pillagedAtMs, besiegedAtMs), y
    // los tratados su barra (SPEC-LIVE-NOTICES Q1).
    for (const kind of ['PILLAGED', 'HOUSE_LOST', 'TREATY_OFFERED', 'TRADE_ACCEPTED', 'UNIT_ATTACKED']) {
      expect(parse({ ...notice, kind }).ok).toBe(false);
    }
    for (const bad of [{ ...notice, amount: -1 }, { ...notice, unitType: 'CATAPULT' }, { ...notice, otherPlayerId: 'x' }, { ...notice, atMs: -1 }]) {
      expect(parse(bad).ok).toBe(false);
    }
  });
});

describe('comandos de economía (SPEC-ECON-WOOD)', () => {
  it('acepta unit.gather con unidad y bosque', () => {
    const result = parseClientMessage({
      v: 1, type: 'unit.gather', requestId: uuid, payload: { unitId: 42, target: { x: 20, y: 10 } },
    });
    expect(result.ok).toBe(true);
  });

  it('unit.gather no admite que el cliente aporte la carga ni la ruta', () => {
    for (const extra of [{ carrying: 10 }, { path: [] }, { trips: 3 }]) {
      const result = parseClientMessage({
        v: 1, type: 'unit.gather', requestId: uuid,
        payload: { unitId: 42, target: { x: 20, y: 10 }, ...extra },
      });
      expect(result).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('acepta city.build de una casa y rechaza un coste o un tiempo del cliente', () => {
    const base = { cityId: 7, type: 'HOUSE', x: 9, y: 10 };
    expect(parseClientMessage({ v: 1, type: 'city.build', requestId: uuid, payload: base }).ok).toBe(true);
    for (const extra of [{ cost: 0 }, { completesAtMs: 1 }]) {
      const result = parseClientMessage({
        v: 1, type: 'city.build', requestId: uuid, payload: { ...base, ...extra },
      });
      expect(result.ok).toBe(false);
    }
    const otro = parseClientMessage({
      v: 1, type: 'city.build', requestId: uuid, payload: { ...base, type: 'CASTLE' },
    });
    expect(otro.ok).toBe(false);
  });

  it('acepta city.train de un aldeano y nada más', () => {
    expect(parseClientMessage({
      v: 1, type: 'city.train', requestId: uuid, payload: { cityId: 7, type: 'VILLAGER' },
    }).ok).toBe(true);
    expect(parseClientMessage({
      v: 1, type: 'city.train', requestId: uuid, payload: { cityId: 7, type: 'VILLAGER', count: 5 },
    }).ok).toBe(false);
  });

  it('acepta building.spawn, building.update y city.update con stocks', () => {
    const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
    const spawn = parseServerMessage({
      ...env, type: 'building.spawn',
      payload: {
        building: {
          id: 1, cityId: 7, type: 'HOUSE', x: 9, y: 10,
          status: 'UNDER_CONSTRUCTION', completesAtMs: 1_757_376_015_000,
        },
      },
    });
    expect(spawn.ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'building.update', payload: { id: 1, status: 'COMPLETE' },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'city.update',
      payload: { id: 7, stocks: { WOOD: 40 }, production: null, populationLimit: 25 },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'city.update', payload: { id: 7, stocks: { WOOD: -1 } },
    }).ok).toBe(false);
  });

  it('acepta forest.update a medias y lleno, y rechaza la madera negativa (SPEC-ECON-FOREST)', () => {
    const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
    expect(parseServerMessage({
      ...env, type: 'forest.update', payload: { x: 4, y: 5, remaining: 0, regrowsAtMs: 1_757_462_400_000 },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'forest.update', payload: { x: 4, y: 5, remaining: 100, regrowsAtMs: null },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'forest.update', payload: { x: 4, y: 5, remaining: -1, regrowsAtMs: null },
    }).ok).toBe(false);
  });

  it('world.snapshot admite forests opcional', () => {
    const base = {
      v: 1, seq: 1, ts: 1, type: 'world.snapshot',
      payload: {
        serverTimeMs: 1, tick: 1, chunks: [], terrain: [], units: [], cities: [], territories: [], buildings: [],
      },
    };
    expect(parseServerMessage(base).ok).toBe(true);
    expect(parseServerMessage({
      ...base, payload: { ...base.payload, forests: [{ x: 1, y: 2, remaining: 40, regrowsAtMs: 5 }] },
    }).ok).toBe(true);
  });

  it('entity.update admite WORKING y la carga', () => {
    const result = parseServerMessage({
      v: 1, seq: 1, ts: 1, type: 'entity.update', payload: { id: 3, status: 'WORKING', carrying: 10 },
    });
    expect(result.ok).toBe(true);
  });
});

describe('piedra y avance de era (SPEC-ECON-STONE)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };

  it('acepta city.advance con la ciudad y nada más: la era la decide el servidor', () => {
    expect(parseClientMessage({
      v: 1, type: 'city.advance', requestId: uuid, payload: { cityId: 7 },
    }).ok).toBe(true);
    for (const extra of [{ era: 'BRONZE_AGE' }, { cost: 0 }, { completesAtMs: 1 }]) {
      const result = parseClientMessage({
        v: 1, type: 'city.advance', requestId: uuid, payload: { cityId: 7, ...extra },
      });
      expect(result).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('acepta hill.update a medias y lleno, y rechaza la piedra negativa', () => {
    expect(parseServerMessage({
      ...env, type: 'hill.update', payload: { x: 4, y: 5, remaining: 0, regrowsAtMs: 1_757_462_400_000 },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'hill.update', payload: { x: 4, y: 5, remaining: 100, regrowsAtMs: null },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'hill.update', payload: { x: 4, y: 5, remaining: -1, regrowsAtMs: null },
    }).ok).toBe(false);
  });

  it('el stock admite piedra, opcional, y la rechaza negativa', () => {
    expect(parseServerMessage({
      ...env, type: 'city.update', payload: { id: 7, stocks: { WOOD: 40, STONE: 15 } },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'city.update', payload: { id: 7, stocks: { WOOD: 40 } },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'city.update', payload: { id: 7, stocks: { WOOD: 40, STONE: -1 } },
    }).ok).toBe(false);
  });

  it('city.update admite la era y el avance, que empieza con una vista y termina en null', () => {
    expect(parseServerMessage({
      ...env, type: 'city.update',
      payload: { id: 7, advance: { era: 'BRONZE_AGE', completesAtMs: 1_757_376_090_000 } },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'city.update', payload: { id: 7, era: 'BRONZE_AGE', populationLimit: 50, advance: null },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'city.update', payload: { id: 7, era: 'GOLD_AGE' },
    }).ok).toBe(false);
  });

  it('la carga de una unidad lleva su recurso, del catálogo', () => {
    expect(parseServerMessage({
      ...env, type: 'entity.update', payload: { id: 3, carrying: 10, carryingResource: 'STONE' },
    }).ok).toBe(true);
    expect(parseServerMessage({
      ...env, type: 'entity.update', payload: { id: 3, carrying: 10, carryingResource: 'GOLD' },
    }).ok).toBe(false);
  });

  it('world.snapshot admite hills opcional', () => {
    const base = {
      v: 1, seq: 1, ts: 1, type: 'world.snapshot',
      payload: {
        serverTimeMs: 1, tick: 1, chunks: [], terrain: [], units: [], cities: [], territories: [], buildings: [],
      },
    };
    expect(parseServerMessage({
      ...base, payload: { ...base.payload, hills: [{ x: 1, y: 2, remaining: 40, regrowsAtMs: 5 }] },
    }).ok).toBe(true);
  });
});

describe('envelope servidor → cliente', () => {
  it('acepta unit.movement.started con la polilínea temporizada completa', () => {
    const result = parseServerMessage({
      v: 1,
      type: 'unit.movement.started',
      seq: 7,
      ts: 1_757_376_000_000,
      requestId: uuid,
      payload: {
        unitId: 42,
        movement: {
          movementId: 900,
          path: [
            { x: 10, y: 10, tMs: 0 },
            { x: 11, y: 10, tMs: 600 },
            { x: 12, y: 11, tMs: 1449 },
          ],
          startTimeMs: 1_757_376_000_000,
          arrivalTimeMs: 1_757_376_001_449,
          target: { x: 12, y: 11 },
        },
      },
    });
    expect(result.ok).toBe(true);
  });

  it('exige una polilínea no vacía', () => {
    const result = parseServerMessage({
      v: 1,
      type: 'unit.movement.started',
      seq: 1,
      ts: 1,
      payload: {
        unitId: 1,
        movement: {
          movementId: 1,
          path: [],
          startTimeMs: 1,
          arrivalTimeMs: 1,
          target: { x: 0, y: 0 },
        },
      },
    });
    expect(result.ok).toBe(false);
  });

  it('rechaza un código de error fuera del catálogo estable', () => {
    const result = parseServerMessage({
      v: 1,
      type: 'system.error',
      seq: 1,
      ts: 1,
      payload: { code: 'ALGO_RARO', message: 'x' },
    });
    expect(result.ok).toBe(false);
  });

  it('acepta system.error con cada código del catálogo', () => {
    for (const code of ERROR_CODES) {
      const result = parseServerMessage({
        v: 1,
        type: 'system.error',
        seq: 1,
        ts: 1,
        payload: { code, message: 'mensaje humano' },
      });
      expect(result.ok, `el código ${code} debería ser válido`).toBe(true);
    }
  });
});

describe('cobertura del catálogo de mensajes', () => {
  it('cada tipo declarado cliente → servidor existe en la unión discriminada', () => {
    const options = ClientMessage.options.map((o) => o.shape.type.value);
    expect(new Set(options)).toEqual(new Set(CLIENT_MESSAGE_TYPES));
  });

  it('cada tipo declarado servidor → cliente existe en la unión discriminada', () => {
    const options = ServerMessage.options.map((o) => o.shape.type.value);
    expect(new Set(options)).toEqual(new Set(SERVER_MESSAGE_TYPES));
  });
});

describe('JSON Schema exportado', () => {
  const files = ['client-message.schema.json', 'server-message.schema.json'] as const;

  it.each(files)('%s existe y es JSON Schema válido para AJV', (filename) => {
    const raw = readFileSync(join(schemaDir, filename), 'utf8');
    const schema = JSON.parse(raw);
    const ajv = new Ajv({ strict: false, allErrors: true });
    addFormats(ajv);
    expect(() => ajv.compile(schema)).not.toThrow();
  });

  it('no ha derivado respecto de los esquemas Zod (misma comprobación que la CI)', () => {
    const expected = {
      'client-message.schema.json': zodToJsonSchema(ClientMessage, {
        name: 'ClientMessage',
        $refStrategy: 'root',
        target: 'jsonSchema7',
      }),
      'server-message.schema.json': zodToJsonSchema(ServerMessage, {
        name: 'ServerMessage',
        $refStrategy: 'root',
        target: 'jsonSchema7',
      }),
    };
    for (const [filename, schema] of Object.entries(expected)) {
      const onDisk = JSON.parse(readFileSync(join(schemaDir, filename), 'utf8'));
      expect(onDisk, `${filename} está desactualizado: ejecuta pnpm run protocol:build`).toEqual(schema);
    }
  });

  it('un unit.move válido pasa también la validación por JSON Schema', () => {
    const schema = JSON.parse(readFileSync(join(schemaDir, 'client-message.schema.json'), 'utf8'));
    const ajv = new Ajv({ strict: false, allErrors: true });
    addFormats(ajv);
    const validate = ajv.compile(schema);
    expect(validate(clientMove())).toBe(true);
  });
});

describe('límites de transporte', () => {
  it('coinciden con los valores por defecto de la configuración EO_WS_*', () => {
    expect(TRANSPORT_LIMITS.maxMessageBytes).toBe(16_384);
    expect(TRANSPORT_LIMITS.rateLimitPerSecond).toBe(20);
    expect(TRANSPORT_LIMITS.rateLimitBurst).toBe(40);
  });
});

describe('comercio (SPEC-TRADE)', () => {
  const otro = '7c9e6679-7425-40de-944b-e07fc1f90ae7';
  const propose = (payload: unknown) => parseClientMessage({ v: 1, type: 'trade.propose', requestId: uuid, payload });
  const linea = (resource: string, amount: number) => [{ resource, amount }];

  it('acepta trade.propose con algo que dar y algo que pedir', () => {
    expect(propose({ playerId: otro, give: linea('WOOD', 300), want: linea('STONE', 100) }).ok).toBe(true);
    expect(propose({ playerId: otro, give: linea('STONE', 1), want: linea('WOOD', 1_000_000) }).ok).toBe(true);
  });

  it('rechaza regalos, cantidades fuera de la cota, recursos inventados y campos de más', () => {
    for (const payload of [
      { playerId: otro, give: [], want: linea('STONE', 1) },
      { playerId: otro, give: linea('WOOD', 1), want: linea('STONE', 0) },
      { playerId: otro, give: linea('WOOD', 1_000_001), want: linea('STONE', 1) },
      { playerId: otro, give: linea('WOOD', 1.5), want: linea('STONE', 1) },
      { playerId: otro, give: linea('GOLD', 1), want: linea('STONE', 1) },
      { playerId: otro, give: [{ resource: 'WOOD', amount: 1, price: 2 }], want: linea('STONE', 1) },
      { playerId: otro, give: linea('WOOD', 1), want: [...linea('STONE', 1), ...linea('STONE', 1), ...linea('WOOD', 1)] },
      { playerId: otro, give: linea('WOOD', 1), want: linea('STONE', 1), expiresAtMs: 5 },
      { playerId: 'no-es-un-uuid', give: linea('WOOD', 1), want: linea('STONE', 1) },
    ]) {
      expect(propose(payload)).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('acepta trade.respond y trade.cancel, y exige el sí o el no', () => {
    expect(parseClientMessage({ v: 1, type: 'trade.respond', requestId: uuid, payload: { offerId: 3, accept: false } }).ok).toBe(true);
    expect(parseClientMessage({ v: 1, type: 'trade.respond', requestId: uuid, payload: { offerId: 3 } }).ok).toBe(false);
    expect(parseClientMessage({ v: 1, type: 'trade.cancel', requestId: uuid, payload: { offerId: 3 } }).ok).toBe(true);
  });

  it('trade.update lleva la oferta vista por quien la recibe', () => {
    const offer = {
      id: 3, otherPlayerId: otro, otherCityId: null, otherCityName: null,
      give: linea('WOOD', 300), get: linea('STONE', 100), status: 'PROPOSED', proposedByMe: true, expiresAtMs: 1_757_376_000_000,
    };
    const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
    expect(parseServerMessage({ ...env, type: 'trade.update', payload: { offer } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'trade.update', payload: { offer: { ...offer, status: 'PERDIDA' } } }).ok).toBe(false);
    expect(parseServerMessage({ ...env, type: 'trade.update', payload: { offer: { ...offer, give: [] } } }).ok).toBe(false);
  });
});
