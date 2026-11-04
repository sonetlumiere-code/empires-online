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
import {
  chunkAt,
  type BuildingView,
  type CityView,
  type TerritoryView,
  type UnitView,
  type WorldChanges,
  type WorldState,
} from '../state/world';
import type {
  SceneBuilding,
  SceneCity,
  SceneForest,
  SceneTerritory,
  SceneUnit,
  SceneUpdate,
  TerrainSource,
} from './sceneData';

/** Lo que la proyección lee del store. */
export type ProjectableState = Pick<
  WorldState,
  | 'playerId'
  | 'world'
  | 'units'
  | 'cities'
  | 'territories'
  | 'buildings'
  | 'terrain'
  | 'forests'
  | 'hills'
  | 'selectedUnitId'
>;

function toSceneUnit(u: UnitView, ownId: string | null, selectedUnitId: number | null): SceneUnit {
  return {
    id: u.id,
    x: u.x,
    y: u.y,
    movement: u.movement,
    isOwn: u.playerId === ownId,
    isSelected: selectedUnitId === u.id,
    working: u.status === 'WORKING',
    carrying: u.carrying,
    warrior: u.unitType === 'WARRIOR',
    attacking: u.status === 'ATTACKING',
    hpRatio: u.maxHp > 0 ? Math.max(0, Math.min(1, u.hp / u.maxHp)) : 1,
  };
}

/** Un edificio es propio si lo es su ciudad; una ciudad fuera del área no se sabe, y se pinta ajena. */
function toSceneBuilding(b: BuildingView, state: ProjectableState): SceneBuilding {
  const city = state.cities.get(b.cityId);
  return {
    id: b.id,
    x: b.x,
    y: b.y,
    type: b.type,
    status: b.status,
    isOwn: city !== undefined && city.ownerPlayerId === state.playerId,
    hpRatio: b.hp !== undefined && b.maxHp !== undefined && b.maxHp > 0 ? Math.max(0, Math.min(1, b.hp / b.maxHp)) : 1,
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

/** La capa de bosque entera: los tiles que no están llenos (SPEC-ECON-FOREST). */
function sceneForests(state: ProjectableState): SceneForest[] {
  const out: SceneForest[] = [];
  for (const f of state.forests.values()) out.push({ x: f.x, y: f.y, remaining: f.remaining });
  return out;
}

/** La capa de colina entera: los tiles que no están llenos (SPEC-ECON-STONE). */
function sceneHills(state: ProjectableState): SceneForest[] {
  const out: SceneForest[] = [];
  for (const h of state.hills.values()) out.push({ x: h.x, y: h.y, remaining: h.remaining });
  return out;
}

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
  const buildings: SceneBuilding[] = [];
  for (const b of state.buildings.values()) buildings.push(toSceneBuilding(b, state));

  return {
    complete: true,
    units,
    removedUnits: [],
    cities,
    removedCities: [],
    territories,
    removedTerritories: [],
    buildings,
    removedBuildings: [],
    terrain: terrainSourceOf(state),
    forests: sceneForests(state),
    hills: sceneHills(state),
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

  const buildings: SceneBuilding[] = [];
  const removedBuildings: number[] = [];
  for (const id of changes.buildings) {
    const b = state.buildings.get(id);
    if (b) buildings.push(toSceneBuilding(b, state));
    else removedBuildings.push(id);
  }

  return {
    complete: false,
    units,
    removedUnits,
    cities,
    removedCities,
    territories,
    removedTerritories,
    buildings,
    removedBuildings,
    terrain: changes.terrain ? terrainSourceOf(state) : null,
    forests: changes.forests ? sceneForests(state) : null,
    hills: changes.hills ? sceneHills(state) : null,
  };
}
