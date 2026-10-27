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
  | { readonly kind: 'gather' }
  | { readonly kind: 'move' };

/**
 * Qué orden es un clic sobre un tile con una unidad propia seleccionada:
 *
 *   1. dentro de la zona urbana (3 × 3) de una ciudad → guarnecerse en ella;
 *   2. sobre un bosque, con un aldeano → recolectar (`unit.gather`);
 *   3. en cualquier otro caso → moverse (`unit.move`).
 *
 * Un aldeano que ya recolecta y recibe un clic en otro bosque cambia de
 * bosque (RN-ECON-006); en hierba deja de recolectar y se mueve (RN-ECON-003).
 * Si el bosque está a tiro lo decide el servidor, no el cliente.
 */
export function decideTileOrder(
  tile: { x: number; y: number },
  unit: Pick<UnitView, 'unitType'>,
  terrain: number | undefined,
  cityAt: (tile: { x: number; y: number }) => Pick<CityView, 'id'> | undefined,
): TileOrder {
  const city = cityAt(tile);
  if (city) return { kind: 'garrison', cityId: city.id };
  if (terrain === TERRAIN_FOREST && unit.unitType === 'VILLAGER') return { kind: 'gather' };
  return { kind: 'move' };
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
      return 'La ciudad ya está entrenando un aldeano: espera a que termine.';
    case 'POPULATION_LIMIT_REACHED':
      return 'No caben más habitantes: construye una casa para subir el límite de población.';
    case 'INVALID_TARGET':
      return 'Ahí no se puede: elige un bosque para recolectar o un hueco libre junto al centro de la ciudad.';
    default:
      return `${code}: ${message}`;
  }
}
