/**
 * Decisiones puras de la economía en el cliente (SPEC-ECON-WOOD).
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

/** Una orden que el clic sobre el mapa traduce en intención. */
export type TileOrder =
  | { readonly kind: 'garrison'; readonly cityId: number }
  | { readonly kind: 'pillage'; readonly cityId: number }
  | { readonly kind: 'gather' }
  | { readonly kind: 'move' };

/**
 * Qué orden es un clic sobre un tile con una unidad propia seleccionada:
 *
 *   1. dentro de la zona urbana (3 × 3) de una ciudad ajena, con un guerrero →
 *      saquearla (`unit.pillage`, SPEC-COMBAT-PILLAGE);
 *   2. dentro de la zona urbana de cualquier otra ciudad → guarnecerse en ella;
 *   3. sobre un bosque, con un aldeano → recolectar (`unit.gather`);
 *   4. en cualquier otro caso → moverse (`unit.move`).
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
): TileOrder {
  const city = cityAt(tile);
  if (city) {
    if (unit.unitType === 'WARRIOR' && city.ownerPlayerId !== playerId) return { kind: 'pillage', cityId: city.id };
    return { kind: 'garrison', cityId: city.id };
  }
  if (terrain === TERRAIN_FOREST && unit.unitType === 'VILLAGER') return { kind: 'gather' };
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
      return 'No hay madera suficiente. Manda aldeanos a un bosque y espera a que la traigan.';
    case 'CITY_BUSY':
      return 'La ciudad ya está entrenando una unidad: espera a que termine.';
    case 'CITY_PROTECTED':
      return 'Ese jugador está protegido mientras está desconectado: no se le puede atacar.';
    case 'UNIT_NOT_FOUND':
      return 'Esa unidad ya no está: ha muerto, se ha escondido o se ha ido.';
    case 'POPULATION_LIMIT_REACHED':
      return 'No caben más habitantes: construye una casa para subir el límite de población.';
    case 'INVALID_TARGET':
      return 'Ahí no se puede: no se ataca en una zona segura, a un aliado ni a una unidad propia; para recolectar, elige un bosque.';
    default:
      return `${code}: ${message}`;
  }
}
