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
import { CityView, CivilizationCode, GoodType, TrainableUnitType, UnitType } from './common.js';
import { CIVILIZATIONS, CIVILIZATION_TRAIT_KEYS } from './civilizations.js';
import { UNITS, unitInfo } from './units.js';
import { TECHNOLOGIES, TECHNOLOGY_EFFECT_KEYS } from './technologies.js';

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
      v: 1, type: 'city.build', requestId: uuid, payload: { ...base, type: 'TOWER' },
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

  it('unit.move.accepted admite movementId 0, el del destino que ya es la posición actual (§7.4)', () => {
    const accepted = (movementId: number) =>
      parseServerMessage({ v: 1, type: 'unit.move.accepted', seq: 1, ts: 1_757_376_000_000, requestId: uuid, payload: { unitId: 42, movementId } });
    expect(accepted(0).ok).toBe(true);
    expect(accepted(77301).ok).toBe(true);
    expect(accepted(-1).ok).toBe(false);
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
    // SPEC-TRADE-FROM-CITY §6: la ciudad de la que sale, opcional.
    expect(propose({ playerId: otro, give: linea('WOOD', 3), want: linea('STONE', 1), cityId: 7 }).ok).toBe(true);
    expect(propose({ playerId: otro, give: linea('WOOD', 3), want: linea('STONE', 1), cityId: 'x' }).ok).toBe(false);
  });

  it('rechaza regalos, cantidades fuera de la cota, recursos inventados y campos de más', () => {
    for (const payload of [
      { playerId: otro, give: [], want: linea('STONE', 1) },
      { playerId: otro, give: linea('WOOD', 1), want: linea('STONE', 0) },
      { playerId: otro, give: linea('WOOD', 1_000_001), want: linea('STONE', 1) },
      { playerId: otro, give: linea('WOOD', 1.5), want: linea('STONE', 1) },
      { playerId: otro, give: linea('GOLD', 1), want: linea('STONE', 1) },
      { playerId: otro, give: [{ resource: 'WOOD', amount: 1, price: 2 }], want: linea('STONE', 1) },
      // Más líneas que recursos (seis desde E14).
      { playerId: otro, give: linea('WOOD', 1), want: ['WOOD', 'STONE', 'WINE', 'SILK', 'SPICES', 'FURS', 'WOOD'].flatMap((r) => linea(r, 1)) },
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

describe('conquista (SPEC-TERRITORY-CONQUEST)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const otro = '00000000-0000-4000-8000-000000000002';
  const territory = {
    id: 12, name: 'Llanuras del Norte', minX: 128, minY: 64, maxX: 191, maxY: 127,
    ownerType: 'PLAYER', ownerId: otro, ownerFaction: 'NEUTRAL', ownerClan: null, contested: false,
  };
  const claim = { playerId: uuid, cityId: 7, cityName: 'Villa Norte', startedAtMs: 1_757_376_000_000, endsAtMs: 1_757_376_600_000 };

  it('acepta unit.claim con sólo el guerrero, y nada más', () => {
    expect(parseClientMessage({ v: 1, type: 'unit.claim', requestId: uuid, payload: { unitId: 42 } }).ok).toBe(true);
    for (const payload of [{}, { unitId: 0 }, { unitId: 42, territoryId: 12 }, { unitId: 42, x: 1, y: 2 }]) {
      expect(parseClientMessage({ v: 1, type: 'unit.claim', requestId: uuid, payload })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('TerritoryView lleva la reclamación activa, opcional, y CLAIMING es un estado de unidad', () => {
    expect(parseServerMessage({ ...env, type: 'territory.update', payload: { territory } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'territory.update', payload: { territory: { ...territory, contested: true, claim } } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'territory.update', payload: { territory: { ...territory, contested: true, claim: { ...claim, cityId: null, cityName: null } } } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'territory.update', payload: { territory: { ...territory, claim: { ...claim, playerId: 'x' } } } }).ok).toBe(false);
    expect(parseServerMessage({ ...env, type: 'entity.update', payload: { id: 42, status: 'CLAIMING' } }).ok).toBe(true);
  });

  it('los tres tipos de territorio son entradas del registro y avisos, con el territorio', () => {
    for (const kind of ['TERRITORY_CLAIMED', 'TERRITORY_LOST', 'TERRITORY_TAKEN']) {
      const entry = {
        id: 41, kind, firstAtMs: 1, lastAtMs: 2, count: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte',
        territoryId: 12, territoryName: 'Llanuras del Norte',
      };
      expect(parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [entry], seenUpToId: 0, nextBeforeId: null } }).ok).toBe(true);
      const notice = { kind, atMs: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', territoryId: 12, territoryName: 'Llanuras del Norte', x: 150, y: 90 };
      expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: notice }).ok).toBe(true);
      expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: { ...notice, territoryId: 0 } }).ok).toBe(false);
    }
  });
});

describe('territorios de clan (SPEC-CLAN-TERRITORIES)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const otro = '00000000-0000-4000-8000-000000000002';
  const territory = {
    id: 12, name: 'Llanuras del Norte', minX: 128, minY: 64, maxX: 191, maxY: 127,
    ownerType: 'CLAN', ownerId: '3', ownerFaction: null, ownerClan: { id: 3, name: 'Los Lobos', tag: 'LOB' }, contested: false,
  };
  const upd = (t: unknown) => parseServerMessage({ ...env, type: 'territory.update', payload: { territory: t } });

  it('unit.claim acepta forClan, booleano y opcional', () => {
    const cmd = (payload: unknown) => parseClientMessage({ v: 1, type: 'unit.claim', requestId: uuid, payload });
    expect(cmd({ unitId: 42, forClan: true }).ok).toBe(true);
    expect(cmd({ unitId: 42, forClan: false }).ok).toBe(true);
    for (const payload of [{ unitId: 42, forClan: 'yes' }, { unitId: 42, forClan: true, clanId: 3 }]) {
      expect(cmd(payload)).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('TerritoryView exige ownerClan: el clan dueño o null', () => {
    expect(upd(territory).ok).toBe(true);
    const { ownerClan: _, ...sin } = territory;
    expect(upd(sin).ok, 'viaja siempre').toBe(false);
    expect(upd({ ...territory, ownerClan: { id: 3, name: 'Los Lobos', tag: 'lob' } }).ok).toBe(false);
    expect(upd({ ...territory, ownerType: 'NONE', ownerId: null, ownerClan: null }).ok).toBe(true);
  });

  it('la reclamación dice para qué clan, si es para uno', () => {
    const claim = { playerId: otro, cityId: 7, cityName: 'Villa Norte', startedAtMs: 1, endsAtMs: 2, clan: { id: 4, tag: 'OSO' } };
    expect(upd({ ...territory, contested: true, claim }).ok).toBe(true);
    expect(upd({ ...territory, contested: true, claim: { ...claim, clan: { id: 4 } } }).ok).toBe(false);
  });

  it('las vistas del clan llevan sus territorios', () => {
    const miembro = { playerId: uuid, cityId: 7, cityName: 'Roma', faction: 'ORDER', role: 'LEADER', joinedAtMs: 1 };
    const clan = { id: 3, name: 'Los Lobos', tag: 'LOB', leaderId: uuid, members: [miembro], sentInvitations: [], territories: [{ id: 12, name: 'Llanuras del Norte', x: 159, y: 95 }], alliances: [], wars: [], warRecord: { won: 0, lost: 0, drawn: 0 } };
    expect(parseServerMessage({ ...env, type: 'clan.update', payload: { clan, invitations: [] } }).ok).toBe(true);
    const { territories: _, ...sin } = clan;
    expect(parseServerMessage({ ...env, type: 'clan.update', payload: { clan: sin, invitations: [] } }).ok).toBe(false);
    const info = { clanId: 3, name: 'Los Lobos', tag: 'LOB', members: [miembro], territories: [{ id: 12, name: 'Llanuras del Norte', x: 159, y: 95 }], alliances: [], wars: [], warRecord: { won: 0, lost: 0, drawn: 0 } };
    expect(parseServerMessage({ ...env, type: 'clan.info', payload: info }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'clan.info', payload: { ...info, territories: [{ id: 12, name: 'x' }] } }).ok, 'sin su tile central').toBe(false);
  });

  it('las alianzas entre clanes: tres comandos y las alianzas en las vistas (SPEC-CLAN-ALLIANCES §6)', () => {
    const cmd = (type: string, payload: unknown) => parseClientMessage({ v: 1, type, requestId: uuid, payload });
    expect(cmd('clan.alliance.propose', { clanId: 4 }).ok).toBe(true);
    expect(cmd('clan.alliance.propose', { clanId: 0 }).ok, 'un id válido').toBe(false);
    expect(cmd('clan.alliance.propose', { clanId: 4, leader: uuid }).ok, 'estricto').toBe(false);
    expect(cmd('clan.alliance.respond', { allianceId: 9, accept: true }).ok).toBe(true);
    expect(cmd('clan.alliance.respond', { allianceId: 9 }).ok, 'sin accept').toBe(false);
    expect(cmd('clan.alliance.cancel', { allianceId: 9 }).ok).toBe(true);
    expect(cmd('clan.alliance.cancel', {}).ok).toBe(false);

    const miembro = { playerId: uuid, cityId: 7, cityName: 'Roma', faction: 'ORDER', role: 'LEADER', joinedAtMs: 1 };
    const alianza = { id: 9, clanId: 4, name: 'Los Osos', tag: 'OSO', status: 'ACTIVE', proposedByUs: true, breaksAtMs: null };
    const clan = { id: 3, name: 'Los Lobos', tag: 'LOB', leaderId: uuid, members: [miembro], sentInvitations: [], territories: [], alliances: [alianza], wars: [], warRecord: { won: 0, lost: 0, drawn: 0 } };
    const upd = (c: unknown) => parseServerMessage({ ...env, type: 'clan.update', payload: { clan: c, invitations: [] } });
    expect(upd(clan).ok).toBe(true);
    expect(upd({ ...clan, alliances: [{ ...alianza, status: 'PROPOSED', breaksAtMs: 5_000 }] }).ok).toBe(true);
    expect(upd({ ...clan, alliances: [{ ...alianza, status: 'BROKEN' }] }).ok, 'sólo PROPOSED o ACTIVE').toBe(false);
    const { alliances: _, ...sin } = clan;
    expect(upd(sin).ok, 'alliances es obligatoria').toBe(false);
    const info = { clanId: 3, name: 'Los Lobos', tag: 'LOB', members: [miembro], territories: [], alliances: [{ clanId: 4, name: 'Los Osos', tag: 'OSO', breaksAtMs: null }], wars: [], warRecord: { won: 0, lost: 0, drawn: 0 } };
    expect(parseServerMessage({ ...env, type: 'clan.info', payload: info }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'clan.info', payload: { ...info, alliances: [{ clanId: 4 }] } }).ok).toBe(false);
  });

  it('las guerras entre clanes: los tres comandos, wars y warRecord obligatorios, y sus entradas (SPEC-CLAN-WARS §6)', () => {
    const cmd = (type: string, payload: unknown) => parseClientMessage({ v: 1, type, requestId: uuid, payload });
    expect(cmd('clan.war.declare', { clanId: 4 }).ok).toBe(true);
    expect(cmd('clan.war.declare', { clanId: 0 }).ok, 'un id válido').toBe(false);
    expect(cmd('clan.war.declare', { clanId: 4, starts: 1 }).ok, 'estricto').toBe(false);
    expect(cmd('clan.war.peace', { warId: 9, offer: true }).ok).toBe(true);
    expect(cmd('clan.war.peace', { warId: 9 }).ok, 'sin offer').toBe(false);
    expect(cmd('clan.war.surrender', { warId: 9 }).ok).toBe(true);
    expect(cmd('clan.war.surrender', {}).ok).toBe(false);

    const miembro = { playerId: uuid, cityId: 7, cityName: 'Roma', faction: 'ORDER', role: 'LEADER', joinedAtMs: 1 };
    const guerra = {
      id: 9, clanId: 4, name: 'Los Osos', tag: 'OSO', declaredByUs: true, status: 'DECLARED', startsAtMs: 900,
      endsAtMs: null, ourScore: 0, theirScore: 0, peaceOfferedByUs: false, peaceOfferedByThem: false,
    };
    const clan = { id: 3, name: 'Los Lobos', tag: 'LOB', leaderId: uuid, members: [miembro], sentInvitations: [], territories: [], alliances: [], wars: [guerra], warRecord: { won: 1, lost: 0, drawn: 2 } };
    const upd = (c: unknown) => parseServerMessage({ ...env, type: 'clan.update', payload: { clan: c, invitations: [] } });
    expect(upd(clan).ok).toBe(true);
    expect(upd({ ...clan, wars: [{ ...guerra, status: 'ACTIVE', endsAtMs: 5_000, ourScore: 13 }] }).ok).toBe(true);
    expect(upd({ ...clan, wars: [{ ...guerra, status: 'ENDED' }] }).ok, 'sólo DECLARED o ACTIVE').toBe(false);
    expect(upd({ ...clan, wars: [{ ...guerra, ourScore: -1 }] }).ok, 'el marcador no es negativo').toBe(false);
    const { wars: _w, ...sinGuerras } = clan;
    expect(upd(sinGuerras).ok, 'wars es obligatoria').toBe(false);
    const { warRecord: _r, ...sinHistorial } = clan;
    expect(upd(sinHistorial).ok, 'warRecord es obligatorio').toBe(false);
    const publica = { clanId: 4, name: 'Los Osos', tag: 'OSO', declaredByUs: false, status: 'ACTIVE', startsAtMs: 900, endsAtMs: 5_000, ourScore: 2, theirScore: 7 };
    const info = { clanId: 3, name: 'Los Lobos', tag: 'LOB', members: [miembro], territories: [], alliances: [], wars: [publica], warRecord: { won: 0, lost: 0, drawn: 0 } };
    expect(parseServerMessage({ ...env, type: 'clan.info', payload: info }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'clan.info', payload: { ...info, wars: [{ clanId: 4 }] } }).ok).toBe(false);

    const base = { firstAtMs: 1, lastAtMs: 1, count: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', clanTag: 'OSO' };
    const page = (e: unknown) => parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [e], seenUpToId: 0, nextBeforeId: null } });
    expect(page({ id: 41, kind: 'CLAN_WAR_DECLARED', startsAtMs: 900, ...base }).ok).toBe(true);
    expect(page({ id: 42, kind: 'CLAN_WAR_PEACE_OFFERED', ...base }).ok).toBe(true);
    expect(page({ id: 43, kind: 'CLAN_WAR_ENDED', warResult: 'WON', warReason: 'TIMEOUT', ourScore: 31, theirScore: 18, ...base }).ok).toBe(true);
    expect(page({ id: 43, kind: 'CLAN_WAR_ENDED', warResult: 'MAYBE', ...base }).ok, 'un resultado del catálogo').toBe(false);
    expect(page({ id: 44, kind: 'TREATY_BREAK_ANNOUNCED', treatyType: 'NON_AGGRESSION', breaksAtMs: 900, breakReason: 'WAR', ...base }).ok).toBe(true);
    expect(page({ id: 44, kind: 'TREATY_BREAK_ANNOUNCED', breakReason: 'CANCEL', ...base }).ok, 'sólo WAR').toBe(false);
    const notice = { kind: 'CLAN_WAR_ENDED', atMs: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', clanTag: 'OSO', warResult: 'DRAWN', warReason: 'PEACE', ourScore: 0, theirScore: 0 };
    expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: notice }).ok).toBe(true);
  });

  it('el registro y los avisos de la conquista llevan la sigla del clan, opcional', () => {
    const entry = {
      id: 41, kind: 'TERRITORY_LOST', firstAtMs: 1, lastAtMs: 2, count: 1, otherPlayerId: otro, otherCityId: 7,
      otherCityName: 'Villa Norte', territoryId: 12, territoryName: 'Llanuras del Norte', clanTag: 'LOB',
    };
    expect(parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [entry], seenUpToId: 0, nextBeforeId: null } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [{ ...entry, clanTag: 'lob' }], seenUpToId: 0, nextBeforeId: null } }).ok).toBe(false);
    const notice = { kind: 'TERRITORY_CLAIMED', atMs: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', territoryId: 12, territoryName: 'Llanuras del Norte', clanTag: 'LOB' };
    expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: notice }).ok).toBe(true);
  });
});

describe('civilizaciones (SPEC-CIVILIZATION)', () => {
  const ciudad = {
    id: 7,
    ownerPlayerId: uuid,
    name: 'Roma',
    centerX: 10,
    centerY: 12,
    era: 'STONE_AGE',
    civilization: 'NORSE',
    faction: 'NEUTRAL',
    clan: null,
    population: 3,
    populationLimit: 20,
    presenceState: 'ONLINE',
    protectionUntilMs: null,
  };

  it('CityView exige la civilización y sólo admite las del catálogo', () => {
    expect(CityView.safeParse(ciudad).success).toBe(true);
    const { civilization: _, ...sin } = ciudad;
    expect(CityView.safeParse(sin).success).toBe(false);
    expect(CityView.safeParse({ ...ciudad, civilization: 'AZTEC' }).success).toBe(false);
  });

  it('el catálogo de presentación tiene una entrada por código, en el orden del enum', () => {
    expect(CIVILIZATIONS.map((c) => c.code)).toEqual(CivilizationCode.options);
  });

  it('todo rasgo del catálogo es un entero positivo por mil de una clave conocida (RN-CIV-003)', () => {
    for (const c of CIVILIZATIONS) {
      for (const [clave, valor] of Object.entries(c.traits)) {
        expect(CIVILIZATION_TRAIT_KEYS, `${c.code}: ${clave}`).toContain(clave);
        expect(Number.isInteger(valor) && (valor as number) > 0, `${c.code}: ${clave} = ${valor}`).toBe(true);
      }
    }
  });

  it('civilizations.json exportado coincide con el catálogo', () => {
    const exportado = JSON.parse(readFileSync(join(schemaDir, 'civilizations.json'), 'utf8'));
    expect(exportado).toEqual({ version: 1, civilizations: CIVILIZATIONS });
  });
});

describe('bienes de civilización (SPEC-CIVILIZATION-GOODS)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const otro = '7c9e6679-7425-40de-944b-e07fc1f90ae7';

  it('city.craft sólo nombra la ciudad', () => {
    const craft = (payload: unknown) => parseClientMessage({ v: 1, type: 'city.craft', requestId: uuid, payload });
    expect(craft({ cityId: 7 }).ok).toBe(true);
    for (const payload of [{}, { cityId: 0 }, { cityId: 7, good: 'WINE' }, { cityId: 7, amount: 50 }]) {
      expect(craft(payload)).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('city.update y la ciudad llevan la elaboración, que empieza con una vista y termina en null', () => {
    const vista = { good: 'WINE', amount: 5, completesAtMs: 1_757_376_030_000 };
    expect(parseServerMessage({ ...env, type: 'city.update', payload: { id: 7, craft: vista } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'city.update', payload: { id: 7, craft: null } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'city.update', payload: { id: 7, craft: { ...vista, good: 'WOOD' } } }).ok).toBe(false);
    expect(parseServerMessage({ ...env, type: 'city.update', payload: { id: 7, craft: { ...vista, amount: 0 } } }).ok).toBe(false);
  });

  it('el almacén lleva los cuatro bienes y el taller es un edificio', () => {
    const stocks = { WOOD: 40, STONE: 15, WINE: 5, SILK: 0, SPICES: 0, FURS: 20 };
    expect(parseServerMessage({ ...env, type: 'city.update', payload: { id: 7, stocks } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'city.update', payload: { id: 7, stocks: { ...stocks, FURS: -1 } } }).ok).toBe(false);
    const taller = { id: 9, cityId: 7, type: 'WORKSHOP', x: 11, y: 10, status: 'UNDER_CONSTRUCTION', completesAtMs: 1 };
    expect(parseServerMessage({ ...env, type: 'building.spawn', payload: { building: taller } }).ok).toBe(true);
    expect(
      parseClientMessage({ v: 1, type: 'city.build', requestId: uuid, payload: { cityId: 7, type: 'WORKSHOP', x: 11, y: 10 } }).ok,
    ).toBe(true);
  });

  it('un trato puede dar o pedir bienes', () => {
    const propose = (payload: unknown) => parseClientMessage({ v: 1, type: 'trade.propose', requestId: uuid, payload });
    expect(propose({ playerId: otro, give: [{ resource: 'WINE', amount: 30 }], want: [{ resource: 'FURS', amount: 30 }] }).ok).toBe(true);
  });

  it('cada civilización tiene un bien distinto, y entre todas los cuatro', () => {
    const bienes = CIVILIZATIONS.map((c) => c.good);
    expect([...bienes].sort()).toEqual([...GoodType.options].sort());
  });
});


describe('caravanas (SPEC-CARAVANS)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const otro = '7c9e6679-7425-40de-944b-e07fc1f90ae7';
  const caravan = {
    id: 5, unitId: 77, ownerPlayerId: uuid, senderPlayerId: uuid,
    fromCityId: 1, fromCityName: 'Roma', toCityId: 2, toCityName: 'Bizancio', originalToCityId: 2,
    cargo: [{ resource: 'WINE', amount: 30 }], status: 'IN_TRANSIT', arrivesAtMs: 1_757_376_200_000,
  };

  it('CARAVAN es un tipo de unidad que viaja en los mensajes de entidad', () => {
    const unidad = {
      id: 77, playerId: uuid, cityId: 1, unitType: 'CARAVAN', x: 10, y: 12, hp: 80, maxHp: 80,
      status: 'MOVING', movement: null, garrisonCityId: null,
    };
    expect(parseServerMessage({ ...env, type: 'entity.spawn', payload: { unit: unidad } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'entity.despawn', payload: { id: 77, reason: 'REMOVED' } }).ok).toBe(true);
  });

  it('city.train no admite CARAVAN: sólo la crea un trato (RN-CARAVAN-001)', () => {
    const train = (type: string) => parseClientMessage({ v: 1, type: 'city.train', requestId: uuid, payload: { cityId: 7, type } });
    expect(train('WARRIOR').ok).toBe(true);
    expect(train('CARAVAN')).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('caravan.update lleva la caravana con su carga, y exige carga y estado del catálogo', () => {
    expect(parseServerMessage({ ...env, type: 'caravan.update', payload: { caravan } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'caravan.update', payload: { caravan: { ...caravan, status: 'DELIVERED', arrivesAtMs: null } } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'caravan.update', payload: { caravan: { ...caravan, cargo: [] } } }).ok).toBe(false);
    expect(parseServerMessage({ ...env, type: 'caravan.update', payload: { caravan: { ...caravan, status: 'ROBADA' } } }).ok).toBe(false);
    const { cargo: _, ...sinCarga } = caravan;
    expect(parseServerMessage({ ...env, type: 'caravan.update', payload: { caravan: sinCarga } }).ok).toBe(false);
  });

  it('el snapshot lleva las caravanas del jugador, opcionales', () => {
    const snapshot = { serverTimeMs: 1, tick: 1, chunks: [], terrain: [], units: [], cities: [], territories: [], buildings: [] };
    expect(parseServerMessage({ ...env, type: 'world.snapshot', payload: snapshot }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'world.snapshot', payload: { ...snapshot, caravans: [caravan] } }).ok).toBe(true);
  });

  it('las caravanas son entradas del registro con su carga, y las capturas también avisos', () => {
    for (const kind of ['CARAVAN_ARRIVED', 'CARAVAN_CAPTURED', 'CARAVAN_INTERCEPTED', 'CARAVAN_LOST']) {
      const entry = {
        id: 41, kind, firstAtMs: 1, lastAtMs: 2, count: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte',
        cargo: [{ resource: 'STONE', amount: 100 }], x: 150, y: 90,
      };
      expect(parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [entry], seenUpToId: 0, nextBeforeId: null } }).ok).toBe(true);
    }
    for (const kind of ['CARAVAN_CAPTURED', 'CARAVAN_INTERCEPTED', 'CARAVAN_LOST']) {
      const notice = { kind, atMs: 1, otherPlayerId: otro, otherCityId: null, otherCityName: null, cargo: [{ resource: 'WOOD', amount: 3 }] };
      expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: notice }).ok).toBe(true);
    }
    const llegada = { kind: 'CARAVAN_ARRIVED', atMs: 1, otherPlayerId: otro, otherCityId: null, otherCityName: null };
    expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: llegada }).ok, 'Q11: la llegada no se avisa').toBe(false);
  });
});

describe('facciones (SPEC-FACTIONS)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const cmd = (payload: unknown) => parseClientMessage({ v: 1, type: 'faction.join', requestId: uuid, payload });
  const ciudad = {
    id: 7, ownerPlayerId: uuid, name: 'Roma', centerX: 10, centerY: 12, era: 'STONE_AGE', civilization: 'ROMAN',
    faction: 'NEUTRAL', clan: null, population: 3, populationLimit: 20, presenceState: 'ONLINE', protectionUntilMs: null,
  };
  const territory = {
    id: 12, name: 'Llanuras del Norte', minX: 128, minY: 64, maxX: 191, maxY: 127,
    ownerType: 'PLAYER', ownerId: uuid, ownerFaction: 'NEUTRAL', ownerClan: null, contested: false,
  };

  it('faction.join sólo nombra ORDER o CHAOS: NEUTRAL no es un valor del comando (RN-FAC-001)', () => {
    expect(cmd({ faction: 'ORDER' }).ok).toBe(true);
    expect(cmd({ faction: 'CHAOS' }).ok).toBe(true);
    for (const payload of [{ faction: 'NEUTRAL' }, { faction: 'order' }, {}, { faction: 'ORDER', atMs: 1 }, { faction: 'ORDER', playerId: uuid }]) {
      expect(cmd(payload)).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('CityView exige la facción y lleva la entrada pendiente, pública (RN-FAC-009)', () => {
    expect(CityView.safeParse(ciudad).success).toBe(true);
    const { faction: _, ...sin } = ciudad;
    expect(CityView.safeParse(sin).success).toBe(false);
    expect(CityView.safeParse({ ...ciudad, faction: 'GUILD' }).success).toBe(false);
    expect(CityView.safeParse({ ...ciudad, factionJoin: null }).success).toBe(true);
    expect(CityView.safeParse({ ...ciudad, factionJoin: { faction: 'CHAOS', atMs: 1_757_376_900_000 } }).success).toBe(true);
    expect(CityView.safeParse({ ...ciudad, factionJoin: { faction: 'NEUTRAL', atMs: 1 } }).success).toBe(false);
  });

  it('city.update lleva la facción nueva y la entrada que se apunta o cuenta', () => {
    const upd = (payload: unknown) => parseServerMessage({ ...env, type: 'city.update', payload });
    expect(upd({ id: 7, factionJoin: { faction: 'ORDER', atMs: 1_757_376_900_000 } }).ok).toBe(true);
    expect(upd({ id: 7, faction: 'ORDER', factionJoin: null }).ok).toBe(true);
    expect(upd({ id: 7, faction: 'PIRATES' }).ok).toBe(false);
  });

  it('TerritoryView lleva el bando del dueño, o null sin jugador por dueño', () => {
    const upd = (t: unknown) => parseServerMessage({ ...env, type: 'territory.update', payload: { territory: t } });
    expect(upd(territory).ok).toBe(true);
    const { ownerFaction: _, ...sin } = territory;
    expect(upd(sin).ok, 'viaja siempre, null si no hay dueño').toBe(false);
    expect(upd({ ...territory, ownerFaction: 'CHAOS' }).ok).toBe(true);
    expect(upd({ ...territory, ownerType: 'NONE', ownerId: null, ownerFaction: null }).ok).toBe(true);
    expect(upd({ ...territory, ownerFaction: 'GUILD' }).ok).toBe(false);
  });
});

describe('mensajes (SPEC-MESSAGES)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const otro = '7c9e6679-7425-40de-944b-e07fc1f90ae7';
  const cmd = (type: string, payload: unknown) => parseClientMessage({ v: 1, type, requestId: uuid, payload });
  const message = {
    id: 9, fromPlayerId: uuid, toPlayerId: otro, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte',
    body: '¿60 de vino por 200 de piedra?', sentAtMs: 1_757_376_000_000,
  };

  it('message.send lleva el destinatario y el texto, y nada que elija al remitente (RN-MSG-001)', () => {
    expect(cmd('message.send', { toPlayerId: otro, body: 'hola' }).ok).toBe(true);
    expect(cmd('message.send', { toPlayerId: otro, body: '𝔸'.repeat(500) }).ok, '500 caracteres fuera del plano básico caben').toBe(true);
    for (const payload of [
      { toPlayerId: otro, body: '' },
      { toPlayerId: otro, body: 'x'.repeat(1001) },
      { toPlayerId: 'no-es-uuid', body: 'hola' },
      { body: 'hola' },
      { toPlayerId: otro, body: 'hola', fromPlayerId: uuid },
    ]) {
      expect(cmd('message.send', payload)).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('message.list no lleva nada, y message.fetch, message.read y message.block nombran sólo al otro', () => {
    expect(cmd('message.list', {}).ok).toBe(true);
    expect(cmd('message.list', { playerId: otro })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('message.fetch', { otherPlayerId: otro }).ok).toBe(true);
    expect(cmd('message.fetch', { otherPlayerId: otro, beforeId: 9 }).ok).toBe(true);
    expect(cmd('message.fetch', { otherPlayerId: otro, beforeId: 0 })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('message.fetch', {})).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('message.read', { otherPlayerId: otro, upToId: 9 }).ok).toBe(true);
    expect(cmd('message.read', { otherPlayerId: otro, upToId: 0 })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('message.block', { playerId: otro, blocked: true }).ok).toBe(true);
    expect(cmd('message.block', { playerId: otro, blocked: false }).ok).toBe(true);
    expect(cmd('message.block', { playerId: otro })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('message.new lleva el mensaje visto desde la sesión', () => {
    expect(parseServerMessage({ ...env, type: 'message.new', requestId: uuid, payload: { message } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'message.new', payload: { message: { ...message, otherCityId: null, otherCityName: null } } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'message.new', payload: { message: { ...message, body: '' } } }).ok).toBe(false);
    const { otherPlayerId: _, ...sinOtro } = message;
    expect(parseServerMessage({ ...env, type: 'message.new', payload: { message: sinOtro } }).ok).toBe(false);
  });

  it('message.inbox lleva las conversaciones y los bloqueados', () => {
    const conversation = {
      otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', lastBody: 'hola', lastFromMe: false,
      lastAtMs: 1, unread: 2, blockedByMe: false,
    };
    const blocked = { playerId: otro, cityId: null, cityName: null };
    expect(parseServerMessage({ ...env, type: 'message.inbox', payload: { conversations: [conversation], blocked: [blocked] } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'message.inbox', payload: { conversations: [], blocked: [] } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'message.inbox', payload: { conversations: [{ ...conversation, unread: -1 }], blocked: [] } }).ok).toBe(false);
  });

  it('message.page lleva los mensajes, lo leído y la página siguiente', () => {
    const page = {
      otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', messages: [message], readUpToId: 0,
      nextBeforeId: null, blockedByMe: false,
    };
    expect(parseServerMessage({ ...env, type: 'message.page', payload: page }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'message.page', payload: { ...page, messages: [], nextBeforeId: 3 } }).ok).toBe(true);
    const { blockedByMe: _, ...sinBloqueo } = page;
    expect(parseServerMessage({ ...env, type: 'message.page', payload: sinBloqueo }).ok).toBe(false);
  });
});

describe('clanes (SPEC-CLANS)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const otro = '7c9e6679-7425-40de-944b-e07fc1f90ae7';
  const cmd = (type: string, payload: unknown) => parseClientMessage({ v: 1, type, requestId: uuid, payload });
  const ciudad = {
    id: 7, ownerPlayerId: uuid, name: 'Roma', centerX: 10, centerY: 12, era: 'STONE_AGE', civilization: 'ROMAN',
    faction: 'NEUTRAL', clan: null, population: 3, populationLimit: 20, presenceState: 'ONLINE', protectionUntilMs: null,
  };
  const miembro = {
    playerId: uuid, cityId: 7, cityName: 'Roma', faction: 'ORDER', role: 'LEADER', joinedAtMs: 1_757_376_000_000,
  };
  const clan = { id: 3, name: 'Los Lobos', tag: 'LOB', leaderId: uuid, members: [miembro], sentInvitations: [], territories: [], alliances: [], wars: [], warRecord: { won: 0, lost: 0, drawn: 0 } };

  it('clan.create lleva el nombre y la sigla, y la sigla es de 2 a 4 letras mayúsculas o dígitos', () => {
    expect(cmd('clan.create', { name: 'Los Lobos', tag: 'LOB' }).ok).toBe(true);
    expect(cmd('clan.create', { name: 'Ñandú-7', tag: 'N7' }).ok).toBe(true);
    for (const payload of [
      { name: 'Lo', tag: 'LOB' },
      { name: 'x'.repeat(25), tag: 'LOB' },
      { name: 'Los Lobos', tag: 'lob' },
      { name: 'Los Lobos', tag: 'L' },
      { name: 'Los Lobos', tag: 'LOBOS' },
      { name: 'Los Lobos', tag: 'LO-B' },
      { name: 'Los Lobos' },
      { name: 'Los Lobos', tag: 'LOB', leaderId: uuid },
    ]) {
      expect(cmd('clan.create', payload)).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('los demás comandos nombran sólo lo que les toca: nunca el clan propio ni el remitente', () => {
    expect(cmd('clan.invite', { playerId: otro, invited: true }).ok).toBe(true);
    expect(cmd('clan.invite', { playerId: otro, invited: false }).ok).toBe(true);
    expect(cmd('clan.invite', { playerId: otro, invited: true, clanId: 3 })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('clan.respond', { clanId: 3, accept: true }).ok).toBe(true);
    expect(cmd('clan.respond', { clanId: 0, accept: true })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('clan.leave', {}).ok).toBe(true);
    expect(cmd('clan.leave', { clanId: 3 })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('clan.expel', { playerId: otro }).ok).toBe(true);
    expect(cmd('clan.expel', { playerId: 'no-es-uuid' })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('clan.lookup', { clanId: 3 }).ok).toBe(true);
    expect(cmd('clan.chat.send', { body: 'al norte' }).ok).toBe(true);
    expect(cmd('clan.chat.send', { body: '' })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('clan.chat.send', { body: 'hola', clanId: 3 })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('clan.chat.fetch', {}).ok).toBe(true);
    expect(cmd('clan.chat.fetch', { beforeId: 9 }).ok).toBe(true);
    expect(cmd('clan.chat.fetch', { beforeId: 0 })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('clan.chat.read', { upToId: 9 }).ok).toBe(true);
    expect(cmd('clan.chat.read', {})).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('clan.promote y clan.transfer nombran sólo al miembro (SPEC-CLAN-GOVERNANCE §6)', () => {
    expect(cmd('clan.promote', { playerId: otro, officer: true }).ok).toBe(true);
    expect(cmd('clan.promote', { playerId: otro, officer: false }).ok).toBe(true);
    expect(cmd('clan.promote', { playerId: otro })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('clan.promote', { playerId: otro, officer: 'sí' })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('clan.promote', { playerId: otro, officer: true, clanId: 3 })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('clan.transfer', { playerId: otro }).ok).toBe(true);
    expect(cmd('clan.transfer', { playerId: 'no-es-uuid' })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(cmd('clan.transfer', {})).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('CityView exige el clan de su dueño, o null (RN-CLAN-008)', () => {
    expect(CityView.safeParse(ciudad).success).toBe(true);
    const { clan: _, ...sin } = ciudad;
    expect(CityView.safeParse(sin).success, 'viaja siempre').toBe(false);
    expect(CityView.safeParse({ ...ciudad, clan: { id: 3, tag: 'LOB' } }).success).toBe(true);
    expect(CityView.safeParse({ ...ciudad, clan: { id: 3, tag: 'lob' } }).success).toBe(false);
    const upd = (payload: unknown) => parseServerMessage({ ...env, type: 'city.update', payload });
    expect(upd({ id: 7, clan: { id: 3, tag: 'LOB' } }).ok).toBe(true);
    expect(upd({ id: 7, clan: null }).ok).toBe(true);
  });

  it('clan.update lleva el clan propio o null, y las invitaciones recibidas', () => {
    const upd = (payload: unknown) => parseServerMessage({ ...env, type: 'clan.update', payload });
    const invitacion = { clanId: 4, name: 'Los Osos', tag: 'OSO', expiresAtMs: 1_757_462_400_000 };
    expect(upd({ clan, invitations: [] }).ok).toBe(true);
    expect(upd({ clan: null, invitations: [invitacion] }).ok).toBe(true);
    expect(upd({ clan: { ...clan, members: [] }, invitations: [] }).ok, 'un clan tiene al menos un miembro').toBe(false);
    expect(upd({ clan: { ...clan, members: [{ ...miembro, role: 'OFFICER' }] }, invitations: [] }).ok, 'SPEC-CLAN-GOVERNANCE').toBe(true);
    expect(upd({ clan: { ...clan, members: [{ ...miembro, role: 'CAPTAIN' }] }, invitations: [] }).ok).toBe(false);
    const enviada = { playerId: otro, cityId: null, cityName: null, expiresAtMs: 1 };
    expect(upd({ clan: { ...clan, sentInvitations: [enviada] }, invitations: [] }).ok).toBe(true);
    expect(upd({ clan }).ok).toBe(false);
  });

  it('clan.info es la vista pública, y el canal lleva sus mensajes y su página', () => {
    expect(parseServerMessage({ ...env, type: 'clan.info', payload: { clanId: 3, name: 'Los Lobos', tag: 'LOB', members: [miembro], territories: [], alliances: [], wars: [], warRecord: { won: 0, lost: 0, drawn: 0 } } }).ok).toBe(true);
    const mensaje = {
      id: 5, clanId: 3, fromPlayerId: uuid, fromCityId: 7, fromCityName: 'Roma', body: 'al norte', sentAtMs: 1,
    };
    expect(parseServerMessage({ ...env, type: 'clan.chat.new', requestId: uuid, payload: { message: mensaje } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'clan.chat.new', payload: { message: { ...mensaje, body: '' } } }).ok).toBe(false);
    const page = { clanId: 3, messages: [mensaje], readUpToId: 0, nextBeforeId: null };
    expect(parseServerMessage({ ...env, type: 'clan.chat.page', payload: page }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'clan.chat.page', payload: { ...page, nextBeforeId: 0 } }).ok).toBe(false);
  });

  it('la expulsión es un tipo del registro, y se avisa sólo al expulsado', () => {
    const notice = { kind: 'CLAN_EXPELLED', atMs: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte' };
    expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: notice }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: { ...notice, kind: 'CLAN_MEMBER_EXPELLED' } }).ok).toBe(false);
  });

  it('el liderazgo recibido se avisa; el cedido o perdido sólo va al registro (SPEC-CLAN-GOVERNANCE Q8)', () => {
    const notice = { kind: 'CLAN_LEADERSHIP_RECEIVED', atMs: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', clanTag: 'LOB' };
    const aviso = (kind: string) => parseServerMessage({ ...env, type: 'event_log.notice', payload: { ...notice, kind } }).ok;
    expect(aviso('CLAN_LEADERSHIP_RECEIVED')).toBe(true);
    expect(aviso('CLAN_LEADERSHIP_INHERITED')).toBe(true);
    expect(aviso('CLAN_LEADERSHIP_GIVEN')).toBe(false);
    expect(aviso('CLAN_LEADERSHIP_LOST')).toBe(false);
  });
});

describe('unidades de civilización (SPEC-CIVILIZATION-UNITS)', () => {
  const exclusivas = ['LEGIONARY', 'CATAPHRACT', 'HORSE_ARCHER', 'BERSERKER'] as const;

  it('las cuatro son tipos de unidad y se entrenan con city.train', () => {
    const train = (type: string) =>
      parseClientMessage({ v: 1, type: 'city.train', requestId: uuid, payload: { cityId: 7, type } });
    for (const tipo of exclusivas) {
      expect(UnitType.options).toContain(tipo);
      expect(TrainableUnitType.options).toContain(tipo);
      expect(train(tipo).ok).toBe(true);
    }
    expect(train('KNIGHT')).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('todo tipo de unidad tiene su ficha de presentación, en el orden del enum', () => {
    expect(UNITS.map((u) => u.code)).toEqual(UnitType.options);
    for (const tipo of UnitType.options) expect(unitInfo(tipo).name.length).toBeGreaterThan(0);
    for (const u of UNITS) {
      expect(u.trainMs > 0).toBe(TrainableUnitType.options.includes(u.code as TrainableUnitType));
    }
  });

  it('cada civilización tiene una unidad exclusiva distinta, que ataca y pide Bronce y el bien propio', () => {
    const unidades = CIVILIZATIONS.map((c) => c.unit);
    expect(new Set(unidades).size).toBe(CIVILIZATIONS.length);
    expect([...unidades].sort()).toEqual([...exclusivas].sort());
    for (const tipo of unidades) {
      const u = unitInfo(tipo);
      expect(u).toMatchObject({ exclusive: true, attacks: true, requiresEra: 'BRONZE_AGE', ownGoodCost: 10 });
    }
    expect(UNITS.filter((u) => u.exclusive).map((u) => u.code).sort()).toEqual([...exclusivas].sort());
  });
});

describe('Edad de los Castillos (SPEC-CASTLE-AGE)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const otroJugador = '7c9e6679-7425-40de-944b-e07fc1f90ae7';
  const castillo = {
    id: 60, cityId: 2, type: 'CASTLE', x: 21, y: 41, status: 'COMPLETE', completesAtMs: 1, hp: 1000, maxHp: 1000,
  };

  it('el castillo es un tipo de edificio que se encarga con city.build', () => {
    const build = (type: string) =>
      parseClientMessage({ v: 1, type: 'city.build', requestId: uuid, payload: { cityId: 7, type, x: 9, y: 9 } });
    expect(build('CASTLE').ok).toBe(true);
    expect(build('TOWER')).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
  });

  it('la vista del edificio admite el objetivo del castillo, opcional y anulable', () => {
    const spawn = (building: unknown) => parseServerMessage({ ...env, type: 'building.spawn', payload: { building } }).ok;
    expect(spawn(castillo)).toBe(true);
    expect(spawn({ ...castillo, targetUnitId: 100 })).toBe(true);
    expect(spawn({ ...castillo, targetUnitId: null })).toBe(true);
    expect(spawn({ ...castillo, targetUnitId: 0 })).toBe(false);
  });

  it('building.update lleva el objetivo nuevo o null cuando deja de apuntar', () => {
    const update = (payload: unknown) => parseServerMessage({ ...env, type: 'building.update', payload }).ok;
    expect(update({ id: 60, status: 'COMPLETE', targetUnitId: 100 })).toBe(true);
    expect(update({ id: 60, status: 'COMPLETE', targetUnitId: null })).toBe(true);
    expect(update({ id: 60, status: 'COMPLETE', targetUnitId: 'cien' })).toBe(false);
  });

  it('las muertes del castillo son tipos del registro y se avisan a los dos lados', () => {
    const notice = { atMs: 1, otherPlayerId: otroJugador, otherCityId: 2, otherCityName: 'Vecina', unitType: 'WARRIOR', x: 22, y: 41 };
    for (const kind of ['UNIT_LOST_TO_CASTLE', 'CASTLE_KILL']) {
      expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: { ...notice, kind } }).ok).toBe(true);
    }
  });
});

describe('árbol tecnológico (SPEC-TECHNOLOGIES)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };

  it('acepta city.research con la ciudad y la tecnología; el resto lo decide el servidor', () => {
    const research = (payload: unknown) => parseClientMessage({ v: 1, type: 'city.research', requestId: uuid, payload });
    expect(research({ cityId: 7, technology: 'WHEEL' }).ok).toBe(true);
    expect(research({ cityId: 7, technology: 'UNA_NUEVA_DE_OTRA_MIGRACION' }).ok).toBe(true);
    expect(research({ cityId: 7 })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(research({ cityId: 7, technology: 'wheel' })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(research({ cityId: 7, technology: 'WHEEL', durationMs: 1 })).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    expect(CLIENT_MESSAGE_TYPES).toContain('city.research');
  });

  it('city.update lleva la investigación (o null) y las tecnologías, opcionales', () => {
    const update = (extra: Record<string, unknown>) =>
      parseServerMessage({ ...env, type: 'city.update', payload: { id: 1, ...extra } }).ok;
    expect(update({ research: { technology: 'WHEEL', completesAtMs: 9 } })).toBe(true);
    expect(update({ research: null, technologies: ['WHEEL'] })).toBe(true);
    expect(update({})).toBe(true);
    expect(update({ research: { technology: 'WHEEL' } })).toBe(false);
    expect(update({ technologies: 'WHEEL' })).toBe(false);
  });

  it('la vista de la ciudad admite los dos campos y una ajena vive sin ellos', () => {
    const ciudad = {
      id: 1, ownerPlayerId: uuid, name: 'Roma', centerX: 10, centerY: 10, era: 'IRON_AGE', civilization: 'ROMAN',
      faction: 'NEUTRAL', clan: null, population: 3, populationLimit: 100, presenceState: 'ONLINE', protectionUntilMs: null,
    };
    expect(CityView.safeParse(ciudad).success).toBe(true);
    expect(CityView.safeParse({ ...ciudad, research: null, technologies: [] }).success).toBe(true);
    expect(CityView.safeParse({ ...ciudad, research: { technology: 'GUILDS', completesAtMs: 5 }, technologies: ['WHEEL'] }).success).toBe(true);
  });

  it('el catálogo de presentación: doce, una exclusiva por civilización, efectos con claves del servidor', () => {
    expect(TECHNOLOGIES).toHaveLength(12);
    const exclusivas = TECHNOLOGIES.filter((t) => t.civilization !== null);
    expect(new Set(exclusivas.map((t) => t.civilization))).toEqual(new Set(CIVILIZATIONS.map((c) => c.code)));
    for (const t of TECHNOLOGIES) {
      for (const [k, v] of Object.entries(t.effects)) {
        expect(TECHNOLOGY_EFFECT_KEYS as readonly string[]).toContain(k);
        expect(v).toBeGreaterThan(0);
      }
      for (const r of t.requires) {
        expect(TECHNOLOGIES.map((x) => x.code)).toContain(r);
      }
    }
  });
});

describe('mercados (SPEC-MARKETS)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const otro = '7c9e6679-7425-40de-944b-e07fc1f90ae7';
  const msg = (type: string, payload: unknown) => parseClientMessage({ v: 1, type, requestId: uuid, payload });
  const lote = (resource: string, amount: number) => ({ resource, amount });
  const oferta = {
    id: 9, playerId: otro, cityId: 7, cityName: 'Villa Norte', civilization: 'PERSIAN', cityX: 120, cityY: 88,
    give: lote('STONE', 20), want: lote('WOOD', 30), lotsTotal: 3, lotsLeft: 3, lotsSold: 0, status: 'OPEN',
    expiresAtMs: 1_757_376_000_000,
  };

  it('acepta market.post con un recurso por lado y de 1 a 100 lotes', () => {
    expect(msg('market.post', { give: lote('STONE', 20), want: lote('WOOD', 30), lots: 3 }).ok).toBe(true);
    // SPEC-TRADE-FROM-CITY §6: la ciudad de la que sale, opcional.
    expect(msg('market.post', { give: lote('STONE', 20), want: lote('WOOD', 30), lots: 3, cityId: 7 }).ok).toBe(true);
    expect(msg('market.post', { give: lote('STONE', 20), want: lote('WOOD', 30), lots: 3, cityId: 0 }).ok).toBe(false);
    expect(msg('market.buy', { offerId: 9, lots: 1, cityId: 7 }).ok).toBe(true);
    expect(msg('market.post', { give: lote('SILK', 1), want: lote('WINE', 1_000_000), lots: 100 }).ok).toBe(true);
    for (const payload of [
      { give: lote('STONE', 20), want: lote('WOOD', 30), lots: 0 },
      { give: lote('STONE', 20), want: lote('WOOD', 30), lots: 101 },
      { give: lote('STONE', 20), want: lote('WOOD', 30) },
      { give: lote('STONE', 0), want: lote('WOOD', 30), lots: 1 },
      { give: lote('GOLD', 1), want: lote('WOOD', 30), lots: 1 },
      { give: [lote('STONE', 1)], want: lote('WOOD', 30), lots: 1 },
      { give: { ...lote('STONE', 1), price: 3 }, want: lote('WOOD', 30), lots: 1 },
      { give: lote('STONE', 1), want: lote('WOOD', 30), lots: 1, commission: 0 },
    ]) {
      expect(msg('market.post', payload)).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
  });

  it('acepta market.buy, market.cancel y market.list con su filtro opcional', () => {
    expect(msg('market.buy', { offerId: 9, lots: 2 }).ok).toBe(true);
    expect(msg('market.buy', { offerId: 9 }).ok).toBe(false);
    expect(msg('market.buy', { offerId: 9, lots: 0 }).ok).toBe(false);
    expect(msg('market.cancel', { offerId: 9 }).ok).toBe(true);
    expect(msg('market.list', {}).ok).toBe(true);
    expect(msg('market.list', { receive: 'SILK', pay: 'WOOD' }).ok).toBe(true);
    expect(msg('market.list', { receive: 'GOLD' }).ok).toBe(false);
    for (const tipo of ['market.post', 'market.buy', 'market.cancel', 'market.list']) {
      expect(CLIENT_MESSAGE_TYPES).toContain(tipo);
    }
  });

  it('market.update y market.listing llevan la oferta con su ciudad, sus lotes y su estado', () => {
    expect(parseServerMessage({ ...env, type: 'market.update', payload: { offer: oferta } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'market.update', payload: { offer: { ...oferta, status: 'SOLD' } } }).ok).toBe(false);
    expect(parseServerMessage({ ...env, type: 'market.update', payload: { offer: { ...oferta, give: [lote('STONE', 20)] } } }).ok).toBe(false);
    expect(parseServerMessage({ ...env, type: 'market.listing', payload: { offers: [oferta] } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'market.listing', payload: { offers: Array(101).fill(oferta) } }).ok).toBe(false);
    const snapshot = { serverTimeMs: 1, tick: 1, chunks: [], terrain: [], units: [], cities: [], territories: [], buildings: [] };
    expect(parseServerMessage({ ...env, type: 'world.snapshot', payload: { ...snapshot, marketOffers: [oferta] } }).ok).toBe(true);
  });

  it('MARKET es un tipo de edificio que se construye con city.build', () => {
    expect(msg('city.build', { cityId: 7, type: 'MARKET', x: 10, y: 11 }).ok).toBe(true);
  });

  it('la venta y la caducidad son entradas del registro; la caducidad, sin otro jugador; ninguna se avisa', () => {
    const page = (entry: Record<string, unknown>) =>
      parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [entry], seenUpToId: 0, nextBeforeId: null } }).ok;
    const base = { id: 41, firstAtMs: 1, lastAtMs: 1, count: 1, otherCityId: null, otherCityName: null };
    expect(page({ ...base, kind: 'MARKET_SOLD', otherPlayerId: otro, lots: 2, tradeGive: [lote('STONE', 40)], tradeGet: [lote('WOOD', 60)] })).toBe(true);
    expect(page({ ...base, kind: 'MARKET_EXPIRED', otherPlayerId: null, lots: 1, tradeGet: [lote('STONE', 20)] })).toBe(true);
    expect(page({ ...base, kind: 'MARKET_SOLD', otherPlayerId: otro, lots: 0 })).toBe(false);
    const notice = { kind: 'MARKET_SOLD', atMs: 1, otherPlayerId: otro, otherCityId: null, otherCityName: null };
    expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: notice }).ok, 'Q11: no se avisa').toBe(false);
  });
});

describe('arietes y murallas (SPEC-SIEGE-ENGINES)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const otro = '00000000-0000-4000-8000-000000000002';

  it('WALL se construye con city.build y viaja como cualquier edificio', () => {
    expect(parseClientMessage({ v: 1, type: 'city.build', requestId: uuid, payload: { cityId: 7, type: 'WALL', x: 10, y: 10 } }).ok).toBe(true);
    const building = { id: 60, cityId: 7, type: 'WALL', x: 10, y: 10, status: 'COMPLETE', completesAtMs: 1, hp: 1_500, maxHp: 1_500 };
    expect(parseServerMessage({ ...env, type: 'building.spawn', payload: { building } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'building.spawn', payload: { building: { ...building, type: 'GATE' } } }).ok).toBe(false);
  });

  it('RAM es un tipo de unidad que se entrena', () => {
    expect(UnitType.options).toContain('RAM');
    expect(TrainableUnitType.options).toContain('RAM');
    expect(parseClientMessage({ v: 1, type: 'city.train', requestId: uuid, payload: { cityId: 7, type: 'RAM' } }).ok).toBe(true);
    const ram = unitInfo('RAM');
    expect(ram.attacks).toBe(false);
    expect(ram.siegeDamage).toBe(40);
    expect(ram.siegeIntervalMs).toBe(3_000);
    expect(ram.requiresEra).toBe('IRON_AGE');
    for (const u of UNITS) {
      if (u.code !== 'RAM') expect(u.siegeDamage, u.code).toBe(0);
    }
  });

  it('los derribos del registro y su aviso dicen qué edificio cayó', () => {
    const entry = { id: 41, kind: 'HOUSE_LOST', firstAtMs: 1, lastAtMs: 2, count: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', buildingType: 'WALL', x: 10, y: 10 };
    const page = (e: unknown) => parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [e], seenUpToId: 0, nextBeforeId: null } });
    expect(page(entry).ok).toBe(true);
    expect(page({ ...entry, buildingType: 'GATE' }).ok).toBe(false);
    const notice = { kind: 'HOUSE_DESTROYED', atMs: 1, otherPlayerId: otro, otherCityId: 7, otherCityName: 'Villa Norte', buildingType: 'WALL', x: 10, y: 10 };
    expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: notice }).ok).toBe(true);
  });
});

describe('la caída de una ciudad (SPEC-CITY-FALL)', () => {
  const env = { v: 1, seq: 1, ts: 1_757_376_000_000 };
  const otro = '00000000-0000-4000-8000-000000000002';
  const assault = { playerId: otro, unitId: 42, startedAtMs: 1_757_376_000_000, endsAtMs: 1_757_376_900_000 };
  const city = {
    id: 7, ownerPlayerId: uuid, name: 'Villa Norte', centerX: 150, centerY: 90, era: 'BRONZE_AGE', civilization: 'ROMAN',
    faction: 'NEUTRAL', clan: null, population: 3, populationLimit: 20, presenceState: 'ONLINE', protectionUntilMs: null,
  };

  it('acepta unit.assault con la unidad y la ciudad, y nada más', () => {
    const cmd = (payload: unknown) => parseClientMessage({ v: 1, type: 'unit.assault', requestId: uuid, payload });
    expect(cmd({ unitId: 42, cityId: 7 }).ok).toBe(true);
    for (const payload of [{ unitId: 42 }, { cityId: 7 }, { unitId: 0, cityId: 7 }, { unitId: 42, cityId: 7, x: 1 }]) {
      expect(cmd(payload)).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
    expect(CLIENT_MESSAGE_TYPES).toContain('unit.assault');
  });

  it('acepta unit.found_city con el aldeano, y nada más (SPEC-MULTI-CITY)', () => {
    const cmd = (payload: unknown) => parseClientMessage({ v: 1, type: 'unit.found_city', requestId: uuid, payload });
    expect(cmd({ unitId: 42 }).ok).toBe(true);
    for (const payload of [{}, { unitId: 0 }, { unitId: 42, x: 1 }, { unitId: 42, cityId: 7 }]) {
      expect(cmd(payload)).toMatchObject({ ok: false, code: 'INVALID_MESSAGE' });
    }
    expect(CLIENT_MESSAGE_TYPES).toContain('unit.found_city');
  });

  it('el snapshot trae las ciudades propias con su centro y su era, opcionales (SPEC-MULTI-CITY Q9)', () => {
    const snapshot = { serverTimeMs: 1, tick: 1, chunks: [], terrain: [], units: [], cities: [], territories: [], buildings: [] };
    const snap = (extra: object) => parseServerMessage({ ...env, type: 'world.snapshot', payload: { ...snapshot, ...extra } });
    const propia = { id: 7, centerX: 150, centerY: 90, era: 'BRONZE_AGE' };
    expect(snap({}).ok).toBe(true);
    expect(snap({ ownCities: [propia, { ...propia, id: 9, era: 'CASTLE_AGE' }] }).ok).toBe(true);
    expect(snap({ ownCities: [{ id: 7, centerX: 150, centerY: 90 }] }).ok).toBe(false);
    expect(snap({ ownCities: [{ ...propia, era: 'BRASS_AGE' }] }).ok).toBe(false);
    expect(snap({ ownCities: Array.from({ length: 5 }, (_, i) => ({ ...propia, id: i + 1 })) }).ok).toBe(false);
  });

  it('CityView lleva el asalto y la inmunidad, opcionales; city.update abre y cierra el asalto', () => {
    expect(CityView.safeParse(city).success).toBe(true);
    expect(CityView.safeParse({ ...city, assault, fallImmuneUntilMs: 1_757_635_200_000 }).success).toBe(true);
    expect(CityView.safeParse({ ...city, assault: { ...assault, unitId: 0 } }).success).toBe(false);
    expect(parseServerMessage({ ...env, type: 'city.update', payload: { id: 7, assault } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'city.update', payload: { id: 7, assault: null } }).ok).toBe(true);
  });

  it('city.fallen dice qué ciudad cayó, de quién y ante quién', () => {
    const payload = { cityId: 7, ownerPlayerId: uuid, byPlayerId: otro, atMs: 1_757_376_900_000 };
    expect(parseServerMessage({ ...env, type: 'city.fallen', payload }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'city.fallen', payload: { ...payload, byPlayerId: 'x' } }).ok).toBe(false);
    expect(SERVER_MESSAGE_TYPES).toContain('city.fallen');
    // SPEC-CITY-TAKEN §6: la ciudad que la tomó, opcional.
    expect(parseServerMessage({ ...env, type: 'city.fallen', payload: { ...payload, takenCityId: 9 } }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'city.fallen', payload: { ...payload, takenCityId: 'x' } }).ok).toBe(false);
  });

  it('CITY_FOUNDED es una entrada del registro sin otro jugador, y no un aviso (SPEC-MULTI-CITY)', () => {
    const entry = { id: 42, kind: 'CITY_FOUNDED', firstAtMs: 1, lastAtMs: 1, count: 1, otherPlayerId: null, otherCityId: null, otherCityName: null, x: 40, y: 40 };
    expect(parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [entry], seenUpToId: 0, nextBeforeId: null } }).ok).toBe(true);
    const notice = { kind: 'CITY_FOUNDED', atMs: 1, x: 40, y: 40 };
    expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: notice }).ok).toBe(false);
  });

  it('los tres tipos de la caída son entradas del registro y avisos', () => {
    for (const kind of ['CITY_ASSAULTED', 'CITY_LOST', 'CITY_TAKEN']) {
      const entry = {
        id: 41, kind, firstAtMs: 1, lastAtMs: 2, count: 1, otherPlayerId: otro, otherCityId: 9, otherCityName: 'Puerto Sur',
        x: 150, y: 90, fallsAtMs: 1_757_376_900_000,
      };
      expect(parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [entry], seenUpToId: 0, nextBeforeId: null } }).ok).toBe(true);
      const notice = { kind, atMs: 1, otherPlayerId: otro, otherCityId: 9, otherCityName: 'Puerto Sur', x: 150, y: 90, fallsAtMs: 2 };
      expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: notice }).ok).toBe(true);
    }
    // SPEC-CITY-TAKEN-NOTICES §6: taken, sólo true y opcional.
    const tomada = { kind: 'CITY_TAKEN', atMs: 1, otherPlayerId: otro, otherCityId: 9, otherCityName: 'Puerto Sur', taken: true };
    expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: tomada }).ok).toBe(true);
    expect(parseServerMessage({ ...env, type: 'event_log.notice', payload: { ...tomada, taken: false } }).ok).toBe(false);
    const entrada = { id: 42, kind: 'CITY_LOST', firstAtMs: 1, lastAtMs: 2, count: 1, otherPlayerId: otro, otherCityId: 9, otherCityName: 'Puerto Sur', taken: true };
    expect(parseServerMessage({ ...env, type: 'event_log.page', payload: { entries: [entrada], seenUpToId: 0, nextBeforeId: null } }).ok).toBe(true);
  });
});
