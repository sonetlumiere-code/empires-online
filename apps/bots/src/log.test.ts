import { describe, expect, it } from 'vitest';

import { RunLog, parseLog, redact } from './log';
import { formatSummary, summarize } from './summary';

const PASSWORD = 'contraseña-de-bots-123';
const TICKET = 'eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.c2lnbmF0dXJh';

describe('RN-BOT-006: ninguna línea lleva la contraseña, la cookie ni un ticket', () => {
  it('redact tacha los tres, también escapados en JSON', () => {
    const line = JSON.stringify({ a: PASSWORD, b: `eo_session=abc123; Path=/`, c: TICKET, d: 'contraseña-de-bots-123"' });
    const out = redact(line, [PASSWORD]);
    expect(out).not.toContain(PASSWORD);
    expect(out).not.toContain('abc123');
    expect(out).not.toContain(TICKET);
    expect(out).toContain('[secreto]');
    expect(out).toContain('[ticket]');
    expect(out).toContain('eo_session=[cookie]');
  });

  it('RunLog escribe una línea JSON por suceso, ya tachada', () => {
    const lines: string[] = [];
    const log = new RunLog((l) => lines.push(l), [PASSWORD]);
    log.emit(1, 'error', 'bot_x', { message: `falló con ${PASSWORD} y ${TICKET}` });
    expect(lines).toHaveLength(1);
    expect(lines[0]?.endsWith('\n')).toBe(true);
    const parsed = parseLog(lines.join(''));
    expect(parsed[0]).toMatchObject({ t: 1, event: 'error', bot: 'bot_x' });
    expect(lines[0]).not.toContain(PASSWORD);
    expect(lines[0]).not.toContain(TICKET);
  });

  it('parseLog ignora una línea a medias', () => {
    expect(parseLog('{"t":1,"event":"start","bot":"a"}\n{"t":2,"ev')).toHaveLength(1);
  });
});

describe('el resumen sale sólo del JSONL', () => {
  const H = 3_600_000;
  const jsonl = [
    { t: 0, event: 'start', bot: 'bot_constructor_1', profile: 'constructor', civilization: 'ROMAN', era: 'STONE_AGE' },
    { t: 10, event: 'command', bot: 'bot_constructor_1', type: 'city.train' },
    { t: 20, event: 'rejected', bot: 'bot_constructor_1', code: 'INSUFFICIENT_RESOURCES' },
    { t: 30, event: 'trained', bot: 'bot_constructor_1', unitType: 'VILLAGER' },
    { t: 40, event: 'built', bot: 'bot_constructor_1', building: 'HOUSE' },
    { t: 30 * 60_000, event: 'era', bot: 'bot_constructor_1', era: 'BRONZE_AGE' },
    { t: 40 * 60_000, event: 'pillaged', bot: 'bot_constructor_1' },
    { t: 41 * 60_000, event: 'lost', bot: 'bot_constructor_1', unitType: 'WARRIOR' },
    { t: 2 * H, event: 'stop', bot: 'bot_constructor_1', era: 'BRONZE_AGE', gathered: { WOOD: 1_000, STONE: 300 } },
    { t: 0, event: 'start', bot: 'bot_saqueador_1', profile: 'saqueador', civilization: 'NORSE', era: 'STONE_AGE' },
    { t: 5, event: 'raid', bot: 'bot_saqueador_1', target: 'bot_constructor_1polis', size: 3 },
    { t: 6, event: 'pillage_done', bot: 'bot_saqueador_1', amount: 5 },
    { t: 7, event: 'pillage_done', bot: 'bot_saqueador_1', amount: 5 },
    { t: 8, event: 'killed', bot: 'bot_saqueador_1', unitType: 'WARRIOR' },
    { t: H, event: 'stop', bot: 'bot_saqueador_1', era: 'STONE_AGE', gathered: { WOOD: 200, STONE: 0 } },
  ]
    .map((l) => JSON.stringify(l))
    .join('\n');

  it('cuenta eras, recursos por hora, entrenadas, combate y rechazos por código', () => {
    const [c, s] = summarize(parseLog(jsonl));
    expect(c).toMatchObject({
      bot: 'bot_constructor_1',
      profile: 'constructor',
      playedMs: 2 * H,
      eras: { STONE_AGE: 0, BRONZE_AGE: 30 * 60_000 },
      finalEra: 'BRONZE_AGE',
      gatheredPerHour: { WOOD: 500, STONE: 150 },
      trained: { VILLAGER: 1 },
      built: { HOUSE: 1 },
      lost: 1,
      pillagedHits: 1,
      commands: 1,
      rejected: { INSUFFICIENT_RESOURCES: 1 },
    });
    expect(s).toMatchObject({ bot: 'bot_saqueador_1', raids: 1, pillageDone: 10, killed: 1, gatheredPerHour: { WOOD: 200 } });
  });

  it('el texto nombra a cada bot y cada perfil', () => {
    const text = formatSummary(summarize(parseLog(jsonl)));
    expect(text).toContain('bot_constructor_1 (constructor, ROMAN) · jugado 2h00m · era final BRONZE_AGE');
    expect(text).toContain('eras: STONE_AGE 0m00s → BRONZE_AGE 30m00s');
    expect(text).toContain('constructor: 1 bots · a Bronce 1/1 (media 30m00s)');
    expect(text).toContain('saqueador: 1 bots · a Bronce 0/1');
  });
});

describe('el resumen del comercio y de las partidas (SPEC-BOTS-TRADE §4.7)', () => {
  const H = 3_600_000;
  const C = 'bot_constructor_1';
  const S = 'bot_saqueador_1';
  const jsonl = [
    { t: 0, event: 'start', bot: C, profile: 'constructor', civilization: 'ROMAN', era: 'BRONZE_AGE' },
    { t: 1, event: 'treaty', bot: C, with: 'bot_constructor_2polis', type: 'TRADE', status: 'proposed' },
    { t: 2, event: 'treaty', bot: C, with: 'bot_constructor_2polis', type: 'TRADE', status: 'accepted' },
    { t: 3, event: 'trade', bot: C, with: 'x', give: [{ resource: 'WINE', amount: 10 }], receive: [{ resource: 'SPICES', amount: 10 }], status: 'accepted' },
    { t: 4, event: 'trade', bot: C, with: 'x', give: [{ resource: 'WOOD', amount: 40 }], receive: [{ resource: 'STONE', amount: 20 }], status: 'accepted' },
    { t: 5, event: 'trade', bot: C, with: 'x', give: [{ resource: 'WINE', amount: 10 }], receive: [{ resource: 'SILK', amount: 10 }], status: 'rejected' },
    { t: 6, event: 'market', bot: C, status: 'posted', give: { resource: 'WINE', amount: 10 }, receive: { resource: 'SILK', amount: 10 }, lots: 3, commission: 3 },
    { t: 7, event: 'market', bot: C, status: 'bought', give: { resource: 'WINE', amount: 8 }, receive: { resource: 'FURS', amount: 10 }, lots: 2, commission: 2 },
    { t: 8, event: 'caravan', bot: C, status: 'lost', cargo: [{ resource: 'FURS', amount: 10 }] },
    { t: 9, event: 'caravan', bot: C, status: 'arrived', cargo: [{ resource: 'FURS', amount: 10 }] },
    { t: 90 * 60_000, event: 'era', bot: C, era: 'IRON_AGE' },
    { t: 2 * H, event: 'stop', bot: C, era: 'IRON_AGE', gathered: {} },
    { t: 0, event: 'start', bot: S, profile: 'saqueador', civilization: 'NORSE', era: 'STONE_AGE' },
    { t: 10, event: 'raid', bot: S, target: 'bot_constructor_1polis', size: 3 },
    { t: 20, event: 'rally', bot: S, target: 'bot_constructor_1polis', warriors: 3, alive: 3, waitMs: 40_000 },
    { t: 30, event: 'pillage_done', bot: S, amount: 5 },
    { t: 31, event: 'killed', bot: S, unitType: 'WARRIOR' },
    { t: 32, event: 'lost', bot: S, unitType: 'WARRIOR' },
    { t: 40, event: 'raid_end', bot: S, target: 'bot_constructor_1polis', lost: 1, size: 3 },
    { t: 50, event: 'raid', bot: S, target: 'bot_constructor_2polis', size: 3 },
    { t: 60, event: 'lost', bot: S, unitType: 'WARRIOR' },
    { t: H, event: 'stop', bot: S, era: 'STONE_AGE', gathered: {} },
  ]
    .map((l) => JSON.stringify(l))
    .join('\n');

  it('suma tratados, tratos, bienes ajenos por cada vía, comisión, caravanas perdidas y el balance de cada partida', () => {
    const [c, s] = summarize(parseLog(jsonl));
    expect(c).toMatchObject({
      treaties: 1,
      trades: 2,
      goodsByTrade: 10,
      goodsByMarket: 20,
      commission: 5,
      caravansLost: 1,
      eras: { BRONZE_AGE: 0, IRON_AGE: 90 * 60_000 },
    });
    // Una partida de saqueo, sin lo del asalto (SPEC-BOTS-ASSAULT §4.7).
    const PILLAGE = { kind: 'pillage', ramsLost: 0, outcome: null, msToFall: null };
    expect(s?.raidLog).toEqual([
      { target: 'bot_constructor_1polis', gathered: 3, rallyWaitMs: 40_000, lost: 1, killed: 1, wood: 5, ...PILLAGE },
      { target: 'bot_constructor_2polis', gathered: null, rallyWaitMs: null, lost: 1, killed: 0, wood: 0, ...PILLAGE },
    ]);
    const text = formatSummary([c, s].filter((r) => r !== undefined));
    expect(text).toContain('comercio: tratados 1 · tratos 2 · bienes ajenos por trato 10 y por mercado 20 · comisión 5 · caravanas perdidas 1');
    expect(text).toContain('partida contra bot_constructor_1polis: reunidos 3 en 0m40s · perdidos 1 · muertos 1 · madera 5');
    expect(text).toContain('partida contra bot_constructor_2polis: sin reunirse · perdidos 1 · muertos 0 · madera 0');
    expect(text).toContain('a Hierro 1/1 (media 1h30m)');
    // Arrancó en Bronce: eso no es llegar a Bronce (la prueba en vivo del 2026-11-21 lo contaba).
    expect(text).toContain('constructor: 1 bots · a Bronce 0/1 (media —)');
  });
});

describe('el resumen del asalto (SPEC-BOTS-ASSAULT §4.7, Q9)', () => {
  const H = 3_600_000;
  const M = 60_000;
  const S = 'bot_saqueador_1';
  const C = 'bot_constructor_1';
  const jsonl = [
    { t: 0, event: 'start', bot: S, profile: 'saqueador', civilization: 'NORSE', era: 'IRON_AGE' },
    { t: 10 * M, event: 'raid', bot: S, target: 'bot_constructor_1polis', size: 6, kind: 'assault', rams: 2 },
    { t: 20 * M, event: 'assault', bot: S, status: 'rallied', target: 'bot_constructor_1polis', warriors: 6, rams: 2, waitMs: 60_000 },
    { t: 22 * M, event: 'lost', bot: S, unitType: 'RAM' },
    { t: 23 * M, event: 'assault', bot: S, status: 'breached', target: 'bot_constructor_1polis', rams: 1 },
    { t: 24 * M, event: 'assault', bot: S, status: 'planted', target: 'bot_constructor_1polis', holder: 1 },
    { t: 26 * M, event: 'lost', bot: S, unitType: 'WARRIOR' },
    { t: 26 * M, event: 'assault', bot: S, status: 'broken', target: 'bot_constructor_1polis', cause: 'holder_died' },
    { t: 27 * M, event: 'assault', bot: S, status: 'planted', target: 'bot_constructor_1polis', holder: 2 },
    { t: 42 * M, event: 'assault', bot: S, status: 'fell', target: 'bot_constructor_1polis' },
    { t: 50 * M, event: 'raid_end', bot: S, target: 'bot_constructor_1polis', lost: 2, size: 6 },
    { t: 55 * M, event: 'raid', bot: S, target: 'bot_constructor_2polis', size: 6, kind: 'assault', rams: 2 },
    { t: 70 * M, event: 'assault', bot: S, status: 'abandoned', target: 'bot_constructor_2polis', cause: 'no_rams' },
    { t: 75 * M, event: 'raid_end', bot: S, target: 'bot_constructor_2polis', lost: 0, size: 6 },
    { t: 2 * H, event: 'stop', bot: S, era: 'IRON_AGE', gathered: {} },
    { t: 0, event: 'start', bot: C, profile: 'constructor', civilization: 'ROMAN', era: 'BRONZE_AGE' },
    { t: 5 * M, event: 'stock', bot: C, era: 'BRONZE_AGE', population: 14, gathered: {} },
    { t: 21 * M, event: 'wall', bot: C, status: 'breached' },
    { t: 24 * M, event: 'assault', bot: C, status: 'suffered', by: 'bot_saqueador_1polis' },
    { t: 42 * M, event: 'fell', bot: C, cityId: 10, newCityId: 11, by: 'bot_saqueador_1polis', era: 'BRONZE_AGE', population: 14 },
    { t: 50 * M, event: 'stock', bot: C, era: 'BRONZE_AGE', population: 9, gathered: {} },
    { t: 62 * M, event: 'built', bot: C, building: 'WALL' },
    { t: 72 * M, event: 'stock', bot: C, era: 'BRONZE_AGE', population: 14, gathered: {} },
    { t: 2 * H, event: 'stop', bot: C, era: 'BRONZE_AGE', gathered: {} },
  ]
    .map((l) => JSON.stringify(l))
    .join('\n');

  it('suma estandartes, ciudades tumbadas, causas y la recuperación del vencido', () => {
    const [c, s] = summarize(parseLog(jsonl));
    expect(s).toMatchObject({
      assaultsPlanted: 2,
      citiesTaken: 1,
      assaultBreaks: { holder_died: 1 },
      assaultAbandons: { no_rams: 1 },
    });
    expect(s?.raidLog).toEqual([
      { target: 'bot_constructor_1polis', gathered: null, rallyWaitMs: null, lost: 2, killed: 0, wood: 0, kind: 'assault', ramsLost: 1, outcome: 'fell', msToFall: 32 * M },
      { target: 'bot_constructor_2polis', gathered: null, rallyWaitMs: null, lost: 0, killed: 0, wood: 0, kind: 'assault', ramsLost: 0, outcome: 'no_rams', msToFall: null },
    ]);
    expect(c).toMatchObject({ assaultsSuffered: 1, wallsBreached: 1 });
    expect(c?.falls).toEqual([{ by: 'bot_saqueador_1polis', era: 'BRONZE_AGE', wallMs: 20 * M, populationMs: 30 * M, nextEraMs: null }]);

    const text = formatSummary([c, s].filter((r) => r !== undefined));
    expect(text).toContain('asalto contra bot_constructor_1polis: sin reunirse · perdidos 2 (arietes 1) · muertos 0 · cae en 32m00s');
    expect(text).toContain('asalto contra bot_constructor_2polis: sin reunirse · perdidos 0 (arietes 0) · muertos 0 · vuelve (no_rams)');
    expect(text).toContain('cayó ante bot_saqueador_1polis en BRONZE_AGE: muralla 20m00s · población de antes 30m00s · era siguiente no');
    expect(text).toContain('Caídas: 1 en 2h00m (0.50 por hora)');
  });
});
