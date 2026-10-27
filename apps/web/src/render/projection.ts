/**
 * Proyección del store a lo que la escena dibuja.
 *
 * Pura y sin Pixi: a partir del estado y de la señal `changes` decide qué
 * entidades llevar a la escena —sólo las tocadas— y cuáles retirar. Es lo que
 * hace que un delta sobre una unidad no reproyecte las demás.
 *
 * `isOwn` se decide comparando el dueño que envió el servidor con el jugador
 * de esta sesión. Es lo único que el cliente calcula sobre propiedad, y no es
 * ownership: es a quién pintar de azul o de verde (ADR-002).
 *
 * Ver ../../../../docs/architecture/frontend.md §7.3
 */
import { chunkAt, type CityView, type TerritoryView, type UnitView, type WorldChanges, type WorldState } from '../state/world';
import type { SceneCity, SceneTerritory, SceneUnit, SceneUpdate, TerrainSource } from './sceneData';

/** Lo que la proyección lee del store. */
export type ProjectableState = Pick<
  WorldState,
  'playerId' | 'world' | 'units' | 'cities' | 'territories' | 'terrain' | 'selectedUnitId'
>;

function toSceneUnit(u: UnitView, ownId: string | null, selectedUnitId: number | null): SceneUnit {
  return {
    id: u.id,
    x: u.x,
    y: u.y,
    movement: u.movement,
    isOwn: u.playerId === ownId,
    isSelected: selectedUnitId === u.id,
  };
}

function toSceneCity(c: CityView, ownId: string | null): SceneCity {
  return {
    id: c.id,
    centerX: c.centerX,
    centerY: c.centerY,
    name: c.name,
    presenceState: c.presenceState,
    isOwn: c.ownerPlayerId === ownId,
  };
}

function toSceneTerritory(t: TerritoryView, ownId: string | null): SceneTerritory {
  return {
    id: t.id,
    minX: t.minX,
    minY: t.minY,
    maxX: t.maxX,
    maxY: t.maxY,
    ownerType: t.ownerType,
    isOwn: t.ownerType === 'PLAYER' && t.ownerId === ownId,
  };
}

/** Una unidad guarnecida está dentro de su ciudad, no en el mapa: no se dibuja. */
const drawable = (u: UnitView): boolean => u.garrisonCityId === null;

/** La fuente de terreno de la escena, o `null` antes del `session.welcome`. */
export function terrainSourceOf(state: ProjectableState): TerrainSource | null {
  if (!state.world) return null;
  const { terrain } = state;
  return { chunkSize: state.world.chunkSize, chunkAt: (cx, cy) => chunkAt(terrain, cx, cy) };
}

/** El mundo entero, para la primera vez y para cuando `changes.all` lo pide. */
export function projectAll(state: ProjectableState): SceneUpdate | null {
  if (!state.world) return null;
  const ownId = state.playerId;

  const units: SceneUnit[] = [];
  for (const u of state.units.values()) {
    if (drawable(u)) units.push(toSceneUnit(u, ownId, state.selectedUnitId));
  }
  const cities: SceneCity[] = [];
  for (const c of state.cities.values()) cities.push(toSceneCity(c, ownId));
  const territories: SceneTerritory[] = [];
  for (const t of state.territories.values()) territories.push(toSceneTerritory(t, ownId));

  return {
    complete: true,
    units,
    removedUnits: [],
    cities,
    removedCities: [],
    territories,
    removedTerritories: [],
    terrain: terrainSourceOf(state),
  };
}

/**
 * Sólo lo que `changes` dice que cambió. Una unidad señalada que ya no está
 * en el store (despawn) o que dejó de dibujarse (guarnecida) se retira.
 */
export function projectChanges(state: ProjectableState, changes: WorldChanges): SceneUpdate | null {
  if (changes.all) return projectAll(state);
  if (!state.world) return null;
  const ownId = state.playerId;

  const units: SceneUnit[] = [];
  const removedUnits: number[] = [];
  for (const id of changes.units) {
    const u = state.units.get(id);
    if (u && drawable(u)) units.push(toSceneUnit(u, ownId, state.selectedUnitId));
    else removedUnits.push(id);
  }

  const cities: SceneCity[] = [];
  const removedCities: number[] = [];
  for (const id of changes.cities) {
    const c = state.cities.get(id);
    if (c) cities.push(toSceneCity(c, ownId));
    else removedCities.push(id);
  }

  const territories: SceneTerritory[] = [];
  const removedTerritories: number[] = [];
  for (const id of changes.territories) {
    const t = state.territories.get(id);
    if (t) territories.push(toSceneTerritory(t, ownId));
    else removedTerritories.push(id);
  }

  return {
    complete: false,
    units,
    removedUnits,
    cities,
    removedCities,
    territories,
    removedTerritories,
    terrain: changes.terrain ? terrainSourceOf(state) : null,
  };
}
