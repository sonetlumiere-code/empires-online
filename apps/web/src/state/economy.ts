/**
 * Decisiones puras de la economía en el cliente (SPEC-ECON-WOOD, SPEC-ECON-STONE).
 *
 * Ninguna decide nada autoritativo: eligen QUÉ INTENCIÓN enviar a partir de
 * lo que el servidor ya contó, y el servidor la acepta o la rechaza. Están
 * aquí, sin React ni Pixi, para poder probarlas.
 *
 * Ver ../../../../docs/architecture/frontend.md §4.4
 */
import type { BuildingView, CityView, UnitView } from './world';

/** El byte de terreno de un bosque (`world.TerrainType`, internal/game/world/tile.go). */
export const TERRAIN_FOREST = 1;
/** El byte de terreno de una colina: da piedra (SPEC-ECON-STONE). */
export const TERRAIN_HILL = 2;

/** Una orden que el clic sobre el mapa traduce en intención. */
export type TileOrder =
  | { readonly kind: 'garrison'; readonly cityId: number }
  | { readonly kind: 'pillage'; readonly cityId: number }
  | { readonly kind: 'demolish'; readonly buildingId: number }
  | { readonly kind: 'gather' }
  | { readonly kind: 'move' }
  | { readonly kind: 'none' };

/**
 * Qué orden es un clic sobre un tile con una unidad propia seleccionada:
 *
 *   1. sobre una casa de una ciudad ajena, con un guerrero → derribarla
 *      (`unit.demolish`, SPEC-COMBAT-SIEGE Q10); en el resto de su zona urbana
 *      (3 × 3) → saquearla (`unit.pillage`, SPEC-COMBAT-PILLAGE);
 *   2. dentro de la zona urbana de cualquier otra ciudad → guarnecerse en ella;
 *   3. sobre un bosque o una colina, con un aldeano → recolectar
 *      (`unit.gather`): el servidor decide por el terreno si es madera o piedra;
 *   4. en cualquier otro caso → moverse (`unit.move`).
 *
 * Una caravana no admite órdenes (SPEC-CARAVANS RN-CARAVAN-003): un clic con una
 * seleccionada no manda nada.
 *
 * Un aldeano que ya recolecta y recibe un clic en otro bosque cambia de
 * bosque (RN-ECON-006); en hierba deja de recolectar y se mueve (RN-ECON-003).
 * Si el bosque está a tiro lo decide el servidor, no el cliente.
 */
export function decideTileOrder(
  tile: { x: number; y: number },
  unit: Pick<UnitView, 'unitType'>,
  terrain: number | undefined,
  cityAt: (tile: { x: number; y: number }) => Pick<CityView, 'id' | 'ownerPlayerId'> | undefined,
  playerId: string | null,
  buildingAt: (tile: { x: number; y: number }) => Pick<BuildingView, 'id' | 'cityId'> | undefined = () => undefined,
): TileOrder {
  if (unit.unitType === 'CARAVAN') return { kind: 'none' };
  const city = cityAt(tile);
  if (city) {
    if (unit.unitType === 'WARRIOR' && city.ownerPlayerId !== playerId) {
      const house = buildingAt(tile);
      if (house && house.cityId === city.id) return { kind: 'demolish', buildingId: house.id };
      return { kind: 'pillage', cityId: city.id };
    }
    return { kind: 'garrison', cityId: city.id };
  }
  if ((terrain === TERRAIN_FOREST || terrain === TERRAIN_HILL) && unit.unitType === 'VILLAGER') return { kind: 'gather' };
  return { kind: 'move' };
}

/** Cuánto dura el aviso de saqueo en el HUD tras el último golpe. */
export const PILLAGE_ALERT_MS = 5_000;

/**
 * Si el aviso de saqueo de una ciudad sigue vigente (SPEC-COMBAT-PILLAGE
 * RN-COMBAT-028): hubo un golpe hace menos de PILLAGE_ALERT_MS. El instante
 * lo pone el servidor, y sólo lo envía al dueño.
 */
export function pillageAlertActive(city: Pick<CityView, 'pillagedAtMs'> | null, serverNowMs: number): boolean {
  if (!city || city.pillagedAtMs === undefined) return false;
  return serverNowMs - city.pillagedAtMs < PILLAGE_ALERT_MS;
}

/** Cuánto dura el aviso de asedio en el HUD tras el último golpe a una casa. */
export const SIEGE_ALERT_MS = 5_000;

/**
 * Si el aviso de asedio de una ciudad sigue vigente (SPEC-COMBAT-SIEGE
 * RN-COMBAT-038): hubo un golpe a una de sus casas hace menos de SIEGE_ALERT_MS.
 */
export function siegeAlertActive(city: Pick<CityView, 'besiegedAtMs'> | null, serverNowMs: number): boolean {
  if (!city || city.besiegedAtMs === undefined) return false;
  return serverNowMs - city.besiegedAtMs < SIEGE_ALERT_MS;
}

/** El edificio en pie de un tile, si lo hay. */
export function buildingAt(
  buildings: ReadonlyMap<number, BuildingView>,
  tile: { x: number; y: number },
): BuildingView | undefined {
  for (const b of buildings.values()) if (b.x === tile.x && b.y === tile.y) return b;
  return undefined;
}

/** Si la ciudad tiene más población que límite: perdió casas (SPEC-COMBAT-SIEGE RN-COMBAT-034). */
export function overPopulationLimit(city: Pick<CityView, 'population' | 'populationLimit'>): boolean {
  return city.population > city.populationLimit;
}

/**
 * Por qué no se puede entrenar ahora, o `null` si se puede intentar. El cliente
 * sólo lo explica: quien decide es el servidor (`CITY_BUSY`,
 * `POPULATION_LIMIT_REACHED`).
 */
export function trainBlockReason(
  city: Pick<CityView, 'population' | 'populationLimit' | 'production' | 'advance'>,
): string | null {
  if (city.production !== null) return 'Ya está entrenando una unidad.';
  // RN-ECON-026: mientras avanza de era, la ciudad no entrena.
  if (city.advance !== null) return 'La ciudad está avanzando de era: espera a que termine.';
  if (overPopulationLimit(city)) {
    return 'La ciudad tiene más población que límite: perdió casas. Reconstrúyelas para volver a entrenar.';
  }
  if (city.population >= city.populationLimit) return 'Población al tope: construye una casa para subir el límite.';
  return null;
}

/**
 * Lo que cuesta llegar a la Edad de Bronce (SPEC-ECON-STONE §4.4). Es sólo para
 * explicarlo en el HUD: el coste lo cobra y lo decide el servidor, que responde
 * INSUFFICIENT_RESOURCES si no alcanza.
 */
export const BRONZE_AGE_COST = { WOOD: 400, STONE: 200 } as const;

/**
 * Lo que cuesta llegar a la Edad de Hierro (SPEC-CIVILIZATION-GOODS §4.4): madera,
 * piedra y bienes de otras civilizaciones, en cualquier mezcla. Como el de Bronce,
 * es sólo para explicarlo: lo cobra y lo decide el servidor.
 */
export const IRON_AGE_COST = { WOOD: 800, STONE: 400, FOREIGN_GOODS: 60 } as const;

/** Lo que cuesta el taller (SPEC-CIVILIZATION-GOODS Q6). */
export const WORKSHOP_COST = { WOOD: 100, STONE: 50 } as const;

/** Lo que cuesta y da una elaboración (SPEC-CIVILIZATION-GOODS Q7). */
export const CRAFT_COST = { WOOD: 30, STONE: 15 } as const;
export const CRAFT_AMOUNT = 5;

/** «Faltan 20 de madera y 5 de piedra», o `null` si alcanza. */
function missing(stocks: { WOOD: number; STONE: number }, cost: { WOOD: number; STONE: number }): string | null {
  const faltan: string[] = [];
  if (stocks.WOOD < cost.WOOD) faltan.push(`${cost.WOOD - stocks.WOOD} de madera`);
  if (stocks.STONE < cost.STONE) faltan.push(`${cost.STONE - stocks.STONE} de piedra`);
  return faltan.length > 0 ? `Faltan ${faltan.join(' y ')}.` : null;
}

/**
 * Por qué no se puede encargar el taller, o `null` si se puede intentar
 * (RN-GOODS-003): pide Bronce, uno por ciudad, un hueco y 100 de madera y 50 de
 * piedra. Lo decide el servidor (INVALID_TARGET, INSUFFICIENT_RESOURCES).
 */
export function workshopBlockReason(
  city: Pick<CityView, 'era' | 'stocks' | 'centerX' | 'centerY' | 'id'>,
  cityBuildings: readonly Pick<BuildingView, 'type' | 'x' | 'y'>[],
): string | null {
  if (city.era === 'STONE_AGE') return 'El taller pide la Edad de Bronce.';
  if (cityBuildings.some((b) => b.type === 'WORKSHOP')) return 'La ciudad ya tiene un taller.';
  if (firstFreeHouseTile(city, cityBuildings) === null) return 'No queda hueco en la zona urbana.';
  return missing(city.stocks, WORKSHOP_COST);
}

/**
 * Por qué no se puede elaborar, o `null` si se puede intentar (RN-GOODS-004): un
 * taller completo, ninguna elaboración en curso y 30 de madera y 15 de piedra.
 */
export function craftBlockReason(
  city: Pick<CityView, 'stocks' | 'craft'>,
  cityBuildings: readonly Pick<BuildingView, 'type' | 'status'>[],
): string | null {
  const taller = cityBuildings.find((b) => b.type === 'WORKSHOP');
  if (!taller) return 'Construye antes un taller.';
  if (taller.status !== 'COMPLETE') return 'El taller está en obras.';
  if (city.craft !== null) return 'El taller ya está elaborando.';
  return missing(city.stocks, CRAFT_COST);
}

/** El nombre de una era en el HUD. */
export function eraLabel(era: string): string {
  switch (era) {
    case 'STONE_AGE':
      return 'Edad de Piedra';
    case 'BRONZE_AGE':
      return 'Edad de Bronce';
    case 'IRON_AGE':
      return 'Edad de Hierro';
    case 'CASTLE_AGE':
      return 'Edad de los Castillos';
    default:
      return era;
  }
}

/** La era a la que avanza una ciudad, o `null` si no hay más (RN-ECON-024, Q11). */
export function nextEra(era: string): string | null {
  if (era === 'STONE_AGE') return 'BRONZE_AGE';
  if (era === 'BRONZE_AGE') return 'IRON_AGE';
  return null;
}

/**
 * Por qué no se puede avanzar de era ahora, o `null` si se puede intentar
 * (RN-ECON-024): de Piedra a Bronce y de Bronce a Hierro, la ciudad no puede estar
 * entrenando ni avanzando, y hace falta todo el coste; para Hierro, también 60
 * bienes de otras civilizaciones (RN-GOODS-006). `foreign` es cuántos tiene.
 */
export function advanceBlockReason(
  city: Pick<CityView, 'era' | 'production' | 'advance' | 'stocks'>,
  foreign = 0,
): string | null {
  const next = nextEra(city.era);
  if (next === null) return 'No hay más eras por ahora.';
  if (city.advance !== null) return 'La ciudad ya está avanzando de era.';
  if (city.production !== null) return 'La ciudad está entrenando una unidad: espera a que termine.';
  if (next === 'BRONZE_AGE') {
    const falta = missing(city.stocks, BRONZE_AGE_COST);
    return falta && `${falta} La piedra está en las colinas.`;
  }
  const falta = missing(city.stocks, IRON_AGE_COST);
  const bienes = IRON_AGE_COST.FOREIGN_GOODS - foreign;
  if (bienes > 0) {
    const otros = `${bienes} bienes de otras civilizaciones: cómpralos con un trato.`;
    return falta ? `${falta.slice(0, -1)} y ${otros}` : `Faltan ${otros}`;
  }
  return falta;
}

/** Lo que es un clic sobre una unidad. */
export type UnitClick =
  | { readonly kind: 'select' }
  | { readonly kind: 'attack' }
  | { readonly kind: 'none' };

/**
 * Qué es un clic sobre una unidad (SPEC-COMBAT-RAID):
 *
 *   1. una propia → seleccionarla;
 *   2. una ajena, con un guerrero propio seleccionado → atacarla (`unit.attack`);
 *   3. una ajena sin guerrero seleccionado → nada: no se seleccionan ajenas.
 *
 * Si el ataque se puede (protección, Safe Zone, tratado) lo decide el servidor.
 */
export function decideUnitClick(
  clicked: Pick<UnitView, 'playerId'>,
  selected: Pick<UnitView, 'playerId' | 'unitType'> | undefined,
  ownPlayerId: string | null,
): UnitClick {
  if (clicked.playerId === ownPlayerId) return { kind: 'select' };
  if (selected && selected.playerId === ownPlayerId && selected.unitType === 'WARRIOR') return { kind: 'attack' };
  return { kind: 'none' };
}

/** El nombre de un tipo de unidad en el HUD. */
export function unitTypeLabel(type: string): string {
  switch (type) {
    case 'VILLAGER':
      return 'aldeano';
    case 'WARRIOR':
      return 'guerrero';
    case 'CARAVAN':
      return 'caravana';
    default:
      return type.toLowerCase();
  }
}

/**
 * El tile donde el botón «Casa» pide construir: el primer hueco libre de la
 * zona urbana 3 × 3 (los ocho tiles que no son el centro), en orden (y, x). Un
 * edificio de la propia ciudad ocupa su tile; `null` si no quedan huecos.
 *
 * Es una comodidad, no una regla: si entre tanto alguien ocupa el tile, el
 * servidor responde INVALID_TARGET (RN-ECON-007).
 */
export function firstFreeHouseTile(
  city: Pick<CityView, 'id' | 'centerX' | 'centerY'>,
  buildings: Iterable<Pick<BuildingView, 'x' | 'y'>>,
): { x: number; y: number } | null {
  const occupied = new Set<string>();
  for (const b of buildings) occupied.add(`${b.x},${b.y}`);
  for (let dy = -1; dy <= 1; dy++) {
    for (let dx = -1; dx <= 1; dx++) {
      if (dx === 0 && dy === 0) continue;
      const x = city.centerX + dx;
      const y = city.centerY + dy;
      if (!occupied.has(`${x},${y}`)) return { x, y };
    }
  }
  return null;
}

/** Los edificios de una ciudad, en el orden del store. */
export function buildingsOf(buildings: ReadonlyMap<number, BuildingView>, cityId: number): BuildingView[] {
  const out: BuildingView[] = [];
  for (const b of buildings.values()) if (b.cityId === cityId) out.push(b);
  return out;
}

/** Segundos que faltan (redondeados hacia arriba) hasta un instante del servidor; nunca negativos. */
export function secondsUntil(atMs: number, serverNowMs: number): number {
  return Math.max(0, Math.ceil((atMs - serverNowMs) / 1000));
}

/**
 * El mensaje que ve una persona por un código del servidor. La lógica nunca
 * mira el texto: sólo el código (websocket-protocol.md §9). Los de la economía
 * dicen qué hacer; el resto conserva el código para diagnosticar.
 */
export function readableError(code: string, message: string): string {
  switch (code) {
    case 'INSUFFICIENT_RESOURCES':
      return 'No hay recursos suficientes. Manda aldeanos a un bosque a por madera o a una colina a por piedra.';
    case 'CITY_BUSY':
      return 'La ciudad ya está entrenando una unidad o avanzando de era: espera a que termine.';
    case 'NOT_IMPLEMENTED':
      return 'Eso todavía no existe en el juego.';
    case 'PATH_TOO_LONG':
      return 'Demasiado lejos: el camino, o la vuelta a la ciudad con la carga, supera lo que una unidad puede recorrer de una vez. Un trato necesita que las caravanas puedan ir de una ciudad a la otra.';
    case 'PATH_NOT_FOUND':
      return 'No hay camino hasta ahí. Un trato necesita un camino por tierra entre las dos ciudades para sus caravanas.';
    case 'UNIT_NOT_MOVABLE':
      return 'Esa unidad no puede recibir esa orden ahora. Las caravanas no obedecen órdenes: van solas a su destino.';
    case 'CITY_PROTECTED':
      return 'Ese jugador está protegido mientras está desconectado: no se le puede atacar.';
    case 'UNIT_NOT_FOUND':
      return 'Esa unidad ya no está: ha muerto, se ha escondido o se ha ido.';
    case 'POPULATION_LIMIT_REACHED':
      return 'No caben más habitantes: construye una casa para subir el límite de población.';
    case 'INVALID_TARGET':
      return 'Ahí no se puede: no se ataca en una zona segura, a un aliado ni a una unidad propia; para recolectar, elige un bosque o una colina que no esté agotada.';
    default:
      return `${code}: ${message}`;
  }
}
