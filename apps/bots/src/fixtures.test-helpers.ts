/**
 * Espejos fijos para los tests del decisor. No es un test: lo importan los
 * `*.test.ts` del paquete.
 */
import type { BuildingView, CityView, ForestView, UnitView } from '../../web/src/state/world';
import type { BotCity, Tile, View } from './decide';

export const ME = '11111111-1111-4111-8111-111111111111';
export const OTHER_BOT = '22222222-2222-4222-8222-222222222222';
export const HUMAN = '33333333-3333-4333-8333-333333333333';

export function city(over: Partial<CityView> = {}): CityView {
  return {
    id: 10,
    ownerPlayerId: ME,
    name: 'bot_constructor_1polis',
    centerX: 100,
    centerY: 100,
    era: 'STONE_AGE',
    civilization: 'ROMAN',
    faction: 'NEUTRAL',
    factionJoin: null,
    clan: null,
    population: 3,
    populationLimit: 10,
    presenceState: 'ONLINE',
    protectionUntilMs: null,
    stocks: { WOOD: 0, STONE: 0, WINE: 0, SILK: 0, SPICES: 0, FURS: 0 },
    production: null,
    advance: null,
    craft: null,
    research: null,
    technologies: [],
    ...over,
  };
}

export function unit(over: Partial<UnitView> & { id: number }): UnitView {
  return {
    playerId: ME,
    cityId: 10,
    unitType: 'VILLAGER',
    x: 102,
    y: 100,
    hp: 40,
    maxHp: 40,
    status: 'IDLE',
    movement: null,
    garrisonCityId: null,
    carrying: 0,
    carryingResource: null,
    ...over,
  };
}

export function building(over: Partial<BuildingView> & { id: number }): BuildingView {
  return { cityId: 10, type: 'HOUSE', x: 99, y: 99, status: 'COMPLETE', completesAtMs: 0, ...over };
}

/** Todo hierba (0) salvo los bosques y colinas que se pasen. */
export function terrain(forests: readonly Tile[] = [], hills: readonly Tile[] = []): View['terrainAt'] {
  const f = new Set(forests.map((t) => `${t.x},${t.y}`));
  const h = new Set(hills.map((t) => `${t.x},${t.y}`));
  return (x, y) => (f.has(`${x},${y}`) ? 1 : h.has(`${x},${y}`) ? 2 : 0);
}

export const FORESTS: readonly Tile[] = [
  { x: 104, y: 100 },
  { x: 105, y: 100 },
  { x: 106, y: 100 },
];
export const HILLS: readonly Tile[] = [{ x: 260, y: 100 }];

export function botCity(over: Partial<BotCity> = {}): BotCity {
  return { playerId: OTHER_BOT, cityId: 20, name: 'bot_constructor_2polis', centerX: 140, centerY: 100, ...over };
}

export function view(over: Partial<View> = {}): View {
  return {
    playerId: ME,
    city: city(),
    buildings: [],
    units: [],
    foreignCities: [],
    foreignBuildings: [],
    terrainAt: terrain(FORESTS, HILLS),
    forests: new Map<string, ForestView>(),
    forestTiles: FORESTS,
    hills: [],
    botCities: [],
    ...over,
  };
}

export function stocks(wood: number, stone = 0, extra: Partial<CityView['stocks']> = {}): CityView['stocks'] {
  return { WOOD: wood, STONE: stone, WINE: 0, SILK: 0, SPICES: 0, FURS: 0, ...extra };
}
