/**
 * El multiciudad en el cliente (SPEC-MULTI-CITY): las ciudades propias con la
 * capital primero, adónde lleva «H», y si el aldeano seleccionado puede fundar
 * y, si no, por qué.
 *
 * Como el resto del estado, no decide nada: el servidor valida la orden, mira el
 * sitio con la capa de madera y manda un snapshot nuevo al confirmarla. Esto sólo
 * evita ofrecer un botón que el servidor va a rechazar, con el mismo motivo que
 * daría él.
 * Ver ../../../../docs/specs/multi-city.md
 */
import type { CityView, OwnCityHome, UnitView } from './world';

/** El techo de ciudades en pie por jugador (SPEC-MORE-CITIES RN-CITY-040). */
export const MAX_OWN_CITIES = 4;

/** El tope de ciudades en pie de un jugador cuya capital está en esa era (RN-CITY-040). */
export function maxOwnCities(era: string): number {
  return { BRONZE_AGE: 2, IRON_AGE: 3, CASTLE_AGE: 4 }[era] ?? 1;
}

/** Lo que cuesta fundar, de la ciudad del aldeano (Q3, `EO_CITY_FOUND_*` por defecto). */
export const FOUND_COST = { WOOD: 800, STONE: 400 } as const;

const ERA_ORDER = ['STONE_AGE', 'BRONZE_AGE', 'IRON_AGE', 'CASTLE_AGE'];

/** Las ciudades propias por id: la primera es la capital (RN-CITY-033). */
export function ownCitiesOf(cities: Iterable<CityView>, playerId: string | null): CityView[] {
  const out: CityView[] = [];
  if (playerId === null) return out;
  for (const c of cities) if (c.ownerPlayerId === playerId) out.push(c);
  return out.sort((a, b) => a.id - b.id);
}

/**
 * Adónde lleva «H» (Q9): la primera vez, a la capital; después, a la siguiente
 * ciudad propia de aquella a la que se fue, y de la última otra vez a la capital.
 */
export function nextHome<T extends { id: number }>(own: readonly T[], currentId: number | null): T | undefined {
  if (own.length === 0) return undefined;
  const i = currentId === null ? -1 : own.findIndex((c) => c.id === currentId);
  return own[(i + 1) % own.length];
}

/** Las ciudades propias recordadas (`ownCityHomes`), por id: adónde lleva «H». */
export function homesOf(homes: ReadonlyMap<number, { x: number; y: number }>): { id: number; x: number; y: number }[] {
  return [...homes].map(([id, t]) => ({ id, x: t.x, y: t.y })).sort((a, b) => a.id - b.id);
}

/**
 * La capital y cuántas ciudades propias hay en pie, con las de fuera del área
 * (`ownCityHomes`, SPEC-MULTI-CITY Q9) y las cargadas: la capital es la de id menor
 * (RN-CITY-033), y su era, la de su vista si está cargada.
 */
export function capitalOf(
  own: readonly CityView[],
  homes: ReadonlyMap<number, OwnCityHome> = new Map(),
): { id: number; era: string; count: number } | null {
  const ids = new Set<number>([...homes.keys(), ...own.map((c) => c.id)]);
  if (ids.size === 0) return null;
  const id = Math.min(...ids);
  const era = own.find((c) => c.id === id)?.era ?? homes.get(id)?.era ?? 'STONE_AGE';
  return { id, era, count: ids.size };
}

/** La ciudad que muestra el panel: la última a la que llevó «H», o la capital. */
export function shownCity(own: readonly CityView[], homeCityId: number | null): CityView | null {
  return own.find((c) => c.id === homeCityId) ?? own[0] ?? null;
}

/**
 * Por qué el aldeano seleccionado no puede fundar, o `null` si el HUD puede
 * ofrecerlo (RN-CITY-030, RN-CITY-031). El sitio lo decide el servidor.
 */
export function foundCityBlockReason(
  unit: UnitView | undefined,
  own: readonly CityView[],
  homes: ReadonlyMap<number, OwnCityHome> = new Map(),
): string | null {
  if (!unit) return 'Selecciona un aldeano';
  if (unit.unitType !== 'VILLAGER') return 'Sólo un aldeano funda una ciudad';
  if (unit.garrisonCityId !== null) return 'El aldeano está guarnecido';
  if (unit.status !== 'IDLE') return 'El aldeano tiene que estar parado';
  const from = own.find((c) => c.id === unit.cityId);
  if (!from) return 'El aldeano no es de una ciudad tuya a la vista';
  if (ERA_ORDER.indexOf(from.era) < ERA_ORDER.indexOf('BRONZE_AGE')) return `${from.name} tiene que estar en la Edad de Bronce`;
  // El tope es el de la era de la capital, la de id menor, y cuentan también las que
  // no están a la vista (SPEC-MORE-CITIES RN-CITY-040, SPEC-MULTI-CITY Q9).
  const capital = capitalOf(own, homes);
  const count = capital?.count ?? own.length;
  const tope = maxOwnCities(capital?.era ?? 'STONE_AGE');
  if (count >= tope) return `Ya tienes ${count} ${count === 1 ? 'ciudad' : 'ciudades'}: el tope de tu capital es ${tope}`;
  if (from.stocks.WOOD < FOUND_COST.WOOD || from.stocks.STONE < FOUND_COST.STONE) {
    return `Fundar cuesta ${FOUND_COST.WOOD} de madera y ${FOUND_COST.STONE} de piedra de ${from.name}`;
  }
  return null;
}

/**
 * De qué ciudad sale una orden de comercio o de mercado (SPEC-TRADE-FROM-CITY Q7):
 * la del panel si no es la capital; `undefined` (la capital) si lo es o no hay. La
 * capital es la de id menor de todas, también de las que no están a la vista: con la
 * capital fuera, el panel muestra otra, y es de ésa de la que sale.
 */
export function commerceCityId(
  shown: CityView | null,
  own: readonly CityView[],
  homes: ReadonlyMap<number, OwnCityHome> = new Map(),
): number | undefined {
  if (!shown || !own.some((c) => c.id === shown.id)) return undefined;
  return shown.id === capitalOf(own, homes)?.id ? undefined : shown.id;
}

