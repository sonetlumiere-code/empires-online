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
