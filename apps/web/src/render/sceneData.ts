/**
 * Lo que la escena recibe del store, sin Pixi.
 *
 * Separado de `scene.ts` para que la proyección (`projection.ts`) y sus tests
 * no carguen PixiJS, que no existe en Node.
 *
 * Ver ../../../../docs/architecture/frontend.md §7.3
 */
import type { ActiveMovement } from '../state/interpolation';
import type { ChunkTerrain } from '../state/world';

export interface SceneUnit {
  readonly id: number;
  readonly x: number;
  readonly y: number;
  readonly movement: ActiveMovement | null;
  readonly isOwn: boolean;
  readonly isSelected: boolean;
}

export interface SceneCity {
  readonly id: number;
  readonly centerX: number;
  readonly centerY: number;
  readonly name: string;
  readonly presenceState: string;
  readonly isOwn: boolean;
}

export interface SceneTerritory {
  readonly id: number;
  readonly minX: number;
  readonly minY: number;
  readonly maxX: number;
  readonly maxY: number;
  /** Tal cual lo envió el servidor. El cliente NO deduce ownership. */
  readonly ownerType: string;
  readonly isOwn: boolean;
}

/** De dónde saca la escena el terreno. El mapa de chunks del store es estable, así que basta con consultarlo. */
export interface TerrainSource {
  readonly chunkSize: number;
  /** El chunk `(cx, cy)` si ya llegó su terreno. */
  chunkAt(cx: number, cy: number): ChunkTerrain | undefined;
}

/**
 * Un cambio para la escena: SÓLO lo que cambió desde el anterior.
 *
 * Con `complete`, las tres listas son el conjunto entero y la escena retira
 * lo que no esté en ellas (snapshot, reconexión, primera vez); si no, lleva
 * las entidades tocadas y los ids de las que dejan de dibujarse.
 */
export interface SceneUpdate {
  readonly complete: boolean;
  readonly units: readonly SceneUnit[];
  readonly removedUnits: readonly number[];
  readonly cities: readonly SceneCity[];
  readonly removedCities: readonly number[];
  readonly territories: readonly SceneTerritory[];
  readonly removedTerritories: readonly number[];
  /** Fuente de terreno si hay chunks nuevos o es un cambio completo; `null` si el terreno no cambió. */
  readonly terrain: TerrainSource | null;
}
