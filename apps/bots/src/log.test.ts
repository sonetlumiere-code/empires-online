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
    expect(s?.raidLog).toEqual([
      { target: 'bot_constructor_1polis', gathered: 3, rallyWaitMs: 40_000, lost: 1, killed: 1, wood: 5 },
      { target: 'bot_constructor_2polis', gathered: null, rallyWaitMs: null, lost: 1, killed: 0, wood: 0 },
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
