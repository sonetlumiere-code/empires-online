/**
 * Escena isométrica en PixiJS.
 *
 * Vive FUERA del árbol de React a propósito: React gestiona el HUD y los menús,
 * PixiJS gestiona el mundo. Reconciliar cada frame del mundo a través del
 * renderizado de React sería tirar el rendimiento por una comodidad que no
 * necesitamos.
 *
 * La escena recibe del store sólo lo que CAMBIÓ (`apply(SceneUpdate)`): crea,
 * actualiza o retira esos sprites y nada más. Por frame sólo trabaja sobre las
 * unidades activas —en movimiento, recién cambiadas o con un desfase por
 * corregir—; una unidad quieta no cuesta nada hasta que llega su delta.
 *
 * Ver ../../../../docs/architecture/frontend.md
 */
import { Application, Container, Graphics, Text } from 'pixi.js';

import { TILE_H, TILE_W, depthOf, screenToTile, worldToIso, type Camera, type Point, type Tile } from './iso';
import type { CameraController } from './camera';
import { RenderReconciler } from './reconciler';
import type { SceneCity, SceneTerritory, SceneUnit, SceneUpdate, TerrainSource } from './sceneData';

export type { SceneCity, SceneTerritory, SceneUnit, SceneUpdate, TerrainSource } from './sceneData';

/** Colores por tipo de terreno (índice = valor del byte de TerrainType). */
const TERRAIN_COLORS = [
  0x6a9a3b, // 0 GRASSLAND
  0x2f6b33, // 1 FOREST
  0x8a7f5c, // 2 HILL
  0x6b6b6b, // 3 MOUNTAIN
  0x2f6f9f, // 4 WATER
  0xb59b66, // 5 ROAD
] as const;

export interface SceneCallbacks {
  /** El jugador hizo clic sobre un tile del mundo. */
  onTileClick?: (tile: Tile) => void;
  /** El jugador hizo clic sobre una unidad. */
  onUnitClick?: (unitId: number) => void;
  /**
   * Cada frame, tras mover la cámara, con su centro en tiles continuos. Es por
   * donde la página decide si enviar session.view: la escena nunca envía nada.
   */
  onCameraFrame?: (center: Point) => void;
}

interface UnitEntry {
  readonly sprite: Container;
  readonly body: Graphics;
  unit: SceneUnit;
}

interface CityEntry {
  readonly sprite: Container;
  readonly body: Graphics;
  readonly label: Text;
  city: SceneCity;
}

export class IsoScene {
  private app: Application | null = null;
  private readonly root = new Container();
  private readonly terrainLayer = new Container();
  private readonly territoryLayer = new Container();
  private readonly entityLayer = new Container();
  private readonly terrainCache = new Map<string, Graphics>();
  private readonly units = new Map<number, UnitEntry>();
  private readonly cities = new Map<number, CityEntry>();
  // La clave incluye el dueño: cambiar de dueño obliga a redibujar, y comparar
  // la firma es más barato que reconstruir el gráfico.
  private readonly territoryShapes = new Map<number, { signature: string; graphic: Graphics }>();
  /** Unidades que hay que reconciliar este frame: en movimiento, recién cambiadas o con desfase pendiente. */
  private readonly active = new Set<number>();
  /** Algún `zIndex` cambió o entró un sprite desde la última ordenación. */
  private sortDirty = false;
  private terrain: TerrainSource | null = null;
  // Posición dibujada de cada unidad: la interpolación más la corrección
  // gradual de las discontinuidades que traen los deltas (frontend.md §5.3).
  private reconciler = new RenderReconciler();

  // La cámara de este frame: la usan el dibujo y el picking (frontend.md §7.4.1).
  private camera: Camera | null = null;

  constructor(
    private readonly cameraController: CameraController,
    private readonly callbacks: SceneCallbacks,
    /** Instante del SERVIDOR, ya corregido por el desfase de reloj. Se evalúa en cada frame. */
    private readonly serverTimeMs: () => number,
  ) {}

  /** Inicializa PixiJS sobre un canvas ya montado. */
  async mount(canvas: HTMLCanvasElement, width: number, height: number): Promise<void> {
    const app = new Application();
    await app.init({
      canvas,
      width,
      height,
      antialias: true,
      background: 0x1a1a1f,
      resolution: window.devicePixelRatio || 1,
      autoDensity: true,
    });

    this.app = app;
    this.root.addChild(this.terrainLayer);
    // Entre el terreno y las entidades: el overlay tiñe el suelo sin tapar
    // unidades ni ciudades, que es lo que el jugador necesita poder pulsar.
    this.root.addChild(this.territoryLayer);
    this.root.addChild(this.entityLayer);
    app.stage.addChild(this.root);

    app.stage.eventMode = 'static';
    app.stage.hitArea = app.screen;
    app.stage.on('pointerdown', (event) => {
      // Sólo el botón izquierdo selecciona y ordena: el derecho y el central
      // arrastran la cámara (los gestiona GameCanvas).
      if (event.button !== 0) return;
      if (!this.camera) return;
      const tile = screenToTile(event.global.x, event.global.y, this.camera);

      // El picking de unidades tiene prioridad: seleccionar una unidad es más
      // frecuente que ordenar un movimiento a su tile exacto.
      const hit = this.unitAt(tile);
      if (hit !== null && this.callbacks.onUnitClick) {
        this.callbacks.onUnitClick(hit);
        return;
      }
      this.callbacks.onTileClick?.(tile);
    });

    app.ticker.add(() => this.draw());
  }

  /** Libera todos los recursos de PixiJS. */
  destroy(): void {
    this.app?.destroy(true, { children: true });
    this.app = null;
    this.terrainCache.clear();
    this.units.clear();
    this.cities.clear();
    this.territoryShapes.clear();
    this.active.clear();
    this.terrain = null;
    this.reconciler = new RenderReconciler();
  }

  resize(width: number, height: number): void {
    this.app?.renderer.resize(width, height);
  }

  /**
   * Entrega lo que cambió en el store. Se llama por cambio, no por frame, y
   * sólo toca los sprites de las entidades que vienen en el cambio.
   */
  apply(update: SceneUpdate): void {
    if (update.terrain) this.terrain = update.terrain;

    if (update.complete) {
      this.pruneAbsent(this.units, update.units, (id) => this.removeUnit(id));
      this.pruneAbsent(this.cities, update.cities, (id) => this.removeCity(id));
      this.pruneAbsent(this.territoryShapes, update.territories, (id) => this.removeTerritory(id));
    } else {
      for (const id of update.removedUnits) this.removeUnit(id);
      for (const id of update.removedCities) this.removeCity(id);
      for (const id of update.removedTerritories) this.removeTerritory(id);
    }

    for (const unit of update.units) this.upsertUnit(unit);
    for (const city of update.cities) this.upsertCity(city);
    for (const territory of update.territories) this.upsertTerritory(territory);
  }

  private draw(): void {
    const app = this.app;
    if (!app) return;

    // La cámara avanza con el dt del ticker: el desplazamiento por teclado no
    // depende de los FPS.
    this.cameraController.step(app.ticker.deltaMS);
    const camera = this.cameraController.camera(app.screen.width, app.screen.height);
    this.camera = camera;
    if (!camera) return;
    const center = this.cameraController.centerWorld();
    if (center) this.callbacks.onCameraFrame?.(center);
    this.root.position.set(camera.viewportWidth / 2 - camera.x, camera.viewportHeight / 2 - camera.y);
    this.root.scale.set(camera.zoom);

    // Un instante del servidor por frame, el mismo para todas las unidades; el
    // dt del ticker es lo que mide la continuidad (frontend.md §5.3).
    this.reconciler.frame(this.serverTimeMs(), app.ticker.deltaMS);

    this.drawTerrain(camera);
    this.stepUnits();

    // Orden de dibujado isométrico: lo que está más "al fondo" (menor x+y) se
    // pinta antes. Sin esto, una unidad delante de un edificio se dibujaría
    // detrás de él. Sólo se reordena si algún zIndex cambió.
    if (this.sortDirty) {
      this.entityLayer.children.sort((a, b) => (a.zIndex ?? 0) - (b.zIndex ?? 0));
      this.sortDirty = false;
    }
  }

  /**
   * Mueve las unidades activas a su posición de este frame. Una unidad quieta
   * y ya en su sitio deja de ser activa hasta su próximo delta: no se le pide
   * nada al reconciliador ni se toca su sprite.
   */
  private stepUnits(): void {
    for (const id of this.active) {
      const entry = this.units.get(id);
      if (!entry) {
        this.active.delete(id);
        continue;
      }
      const unit = entry.unit;
      // La posición dibujada es la INTERPOLADA, no la autoritativa: por eso el
      // movimiento se ve continuo aunque el servidor sólo avance un tile cada
      // 600 ms. Cuando un delta la hace saltar, el reconciliador reparte el
      // salto en unos frames en lugar de dibujarlo de golpe.
      const pos = this.reconciler.position(id, unit);
      const iso = worldToIso(pos.x, pos.y);
      entry.sprite.position.set(iso.x, iso.y);
      this.setDepth(entry.sprite, depthOf(pos.x, pos.y) + 0.5); // por delante del terreno y de la ciudad
      // Quieta y sin desfase que corregir: hasta el próximo delta no hay nada que hacer.
      if (unit.movement === null && pos.x === unit.x && pos.y === unit.y) this.active.delete(id);
    }
  }

  private setDepth(sprite: Container, z: number): void {
    if (sprite.zIndex === z) return;
    sprite.zIndex = z;
    this.sortDirty = true;
  }

  /** La unidad que se DIBUJA en ese tile, si la hay: la posición que el jugador ve, no la autoritativa. */
  private unitAt(tile: Tile): number | null {
    for (const [id, entry] of this.units) {
      const pos = this.reconciler.drawn(id) ?? { x: entry.unit.x, y: entry.unit.y };
      if (Math.round(pos.x) === tile.x && Math.round(pos.y) === tile.y) return id;
    }
    return null;
  }

  private pruneAbsent<T>(map: Map<number, T>, present: readonly { readonly id: number }[], remove: (id: number) => void): void {
    const seen = new Set<number>();
    for (const p of present) seen.add(p.id);
    for (const id of map.keys()) {
      if (!seen.has(id)) remove(id);
    }
  }

  private upsertUnit(unit: SceneUnit): void {
    let entry = this.units.get(unit.id);
    if (!entry) {
      const sprite = new Container();
      const body = new Graphics();
      paintUnitBody(body, unit.isOwn);
      sprite.addChild(body);
      // Aparece donde dice el servidor; el primer frame la lleva a la posición
      // interpolada por el reconciliador.
      const iso = worldToIso(unit.x, unit.y);
      sprite.position.set(iso.x, iso.y);
      entry = { sprite, body, unit };
      this.units.set(unit.id, entry);
      this.entityLayer.addChild(sprite);
      this.sortDirty = true;
    } else if (entry.unit.isOwn !== unit.isOwn) {
      paintUnitBody(entry.body, unit.isOwn);
    }
    entry.unit = unit;
    entry.sprite.alpha = unit.isSelected ? 1 : 0.9;
    entry.sprite.scale.set(unit.isSelected ? 1.25 : 1);
    this.active.add(unit.id);
  }

  private removeUnit(id: number): void {
    const entry = this.units.get(id);
    if (!entry) return;
    this.entityLayer.removeChild(entry.sprite);
    entry.sprite.destroy({ children: true });
    this.units.delete(id);
    this.active.delete(id);
    // Si vuelve, reaparece donde diga el servidor, sin deslizarse desde donde estaba.
    this.reconciler.forget(id);
  }

  private upsertCity(city: SceneCity): void {
    let entry = this.cities.get(city.id);
    if (!entry) {
      const sprite = new Container();
      const body = new Graphics();
      paintCityBody(body, city.isOwn);
      sprite.addChild(body);

      const label = new Text({
        text: city.name,
        style: { fontSize: 11, fill: 0xffffff, fontFamily: 'monospace' },
      });
      label.anchor.set(0.5, 1);
      label.position.set(0, -TILE_H * 1.6);
      sprite.addChild(label);

      entry = { sprite, body, label, city };
      this.cities.set(city.id, entry);
      this.entityLayer.addChild(sprite);
      this.sortDirty = true;
    }
    entry.city = city;

    const iso = worldToIso(city.centerX, city.centerY);
    entry.sprite.position.set(iso.x, iso.y);
    this.setDepth(entry.sprite, depthOf(city.centerX, city.centerY));
    // Una ciudad protegida se distingue de un vistazo.
    entry.sprite.alpha = city.presenceState === 'PROTECTED' ? 0.65 : 1;
  }

  private removeCity(id: number): void {
    const entry = this.cities.get(id);
    if (!entry) return;
    this.entityLayer.removeChild(entry.sprite);
    entry.sprite.destroy({ children: true });
    this.cities.delete(id);
  }

  /**
   * Overlay de territorios: el contorno del rectángulo, proyectado.
   *
   * Un rectángulo del mundo se ve como un ROMBO en isométrico, no como un
   * rectángulo. Sus cuatro vértices son los extremos de las cuatro esquinas del
   * área, no los centros de esos tiles: por eso cada uno lleva el desplazamiento
   * de medio tile que lo lleva al borde de su diamante.
   *
   * El color sale ÚNICAMENTE de `ownerType` y de si el dueño es el jugador de
   * esta sesión, ambos enviados por el servidor. El cliente no deduce ownership
   * de ninguna otra cosa (ADR-002).
   */
  private upsertTerritory(t: SceneTerritory): void {
    const signature = `${t.ownerType}:${t.isOwn}:${t.minX},${t.minY},${t.maxX},${t.maxY}`;
    const cached = this.territoryShapes.get(t.id);
    if (cached && cached.signature === signature) return;
    if (cached) this.removeTerritory(t.id);

    const north = worldToIso(t.minX, t.minY);
    const east = worldToIso(t.maxX, t.minY);
    const south = worldToIso(t.maxX, t.maxY);
    const west = worldToIso(t.minX, t.maxY);

    const { color, alpha } = territoryStyle(t);
    const g = new Graphics();
    g.moveTo(north.x, north.y - TILE_H / 2)
      .lineTo(east.x + TILE_W / 2, east.y)
      .lineTo(south.x, south.y + TILE_H / 2)
      .lineTo(west.x - TILE_W / 2, west.y)
      .closePath()
      .fill({ color, alpha })
      .stroke({ color, width: 2, alpha: Math.min(1, alpha * 6) });

    this.territoryLayer.addChild(g);
    this.territoryShapes.set(t.id, { signature, graphic: g });
  }

  private removeTerritory(id: number): void {
    const entry = this.territoryShapes.get(id);
    if (!entry) return;
    this.territoryLayer.removeChild(entry.graphic);
    entry.graphic.destroy();
    this.territoryShapes.delete(id);
  }

  private drawTerrain(camera: Camera): void {
    const source = this.terrain;
    if (!source) return;

    // Sólo se dibujan los tiles visibles: culling por área de interés.
    const halfW = camera.viewportWidth / (2 * camera.zoom);
    const halfH = camera.viewportHeight / (2 * camera.zoom);
    const margin = 4;

    const centerIso = { x: camera.x, y: camera.y };
    const centerWorld = {
      x: centerIso.y / TILE_H + centerIso.x / TILE_W,
      y: centerIso.y / TILE_H - centerIso.x / TILE_W,
    };
    const radius = Math.ceil(Math.max(halfW / (TILE_W / 2), halfH / (TILE_H / 2))) + margin;

    const minX = Math.floor(centerWorld.x - radius);
    const maxX = Math.ceil(centerWorld.x + radius);
    const minY = Math.floor(centerWorld.y - radius);
    const maxY = Math.ceil(centerWorld.y + radius);

    const visible = new Set<string>();

    for (let y = minY; y <= maxY; y++) {
      for (let x = minX; x <= maxX; x++) {
        const terrain = terrainOf(source, x, y);
        if (terrain === undefined) continue;

        const key = `${x}:${y}`;
        visible.add(key);

        let tile = this.terrainCache.get(key);
        if (!tile) {
          tile = createTerrainTile(x, y, terrain);
          this.terrainCache.set(key, tile);
          this.terrainLayer.addChild(tile);
        }
      }
    }

    // Se retiran los tiles que salieron de la vista para que la caché no crezca
    // sin límite mientras el jugador recorre el mundo.
    for (const [key, graphic] of this.terrainCache) {
      if (visible.has(key)) continue;
      this.terrainLayer.removeChild(graphic);
      graphic.destroy();
      this.terrainCache.delete(key);
    }
  }
}

/** Terreno de un tile, o `undefined` si su chunk no ha llegado. */
function terrainOf(source: TerrainSource, x: number, y: number): number | undefined {
  const size = source.chunkSize;
  const cx = Math.floor(x / size);
  const cy = Math.floor(y / size);
  const chunk = source.chunkAt(cx, cy);
  if (!chunk) return undefined;
  return chunk.tiles[(y - cy * size) * chunk.size + (x - cx * size)];
}

function createTerrainTile(x: number, y: number, terrain: number): Graphics {
  const iso = worldToIso(x, y);
  const color = TERRAIN_COLORS[terrain] ?? 0xff00ff;

  const g = new Graphics();
  g.moveTo(0, -TILE_H / 2)
    .lineTo(TILE_W / 2, 0)
    .lineTo(0, TILE_H / 2)
    .lineTo(-TILE_W / 2, 0)
    .closePath()
    .fill({ color })
    .stroke({ color: 0x000000, width: 0.5, alpha: 0.15 });

  g.position.set(iso.x, iso.y);
  return g;
}

function paintUnitBody(body: Graphics, isOwn: boolean): void {
  body
    .clear()
    .circle(0, -TILE_H / 3, 7)
    .fill({ color: isOwn ? 0x4ea3ff : 0xff6b6b })
    .stroke({ color: 0x0b1a2a, width: 1.5 });
}

function paintCityBody(body: Graphics, isOwn: boolean): void {
  body
    .clear()
    .rect(-TILE_W / 2, -TILE_H * 1.5, TILE_W, TILE_H * 1.5)
    .fill({ color: isOwn ? 0xc9a227 : 0x8a5a2b })
    .stroke({ color: 0x000000, width: 1, alpha: 0.4 });
}

/**
 * Color y opacidad del overlay según el dueño.
 *
 * Sin dueño se pinta muy tenue: la frontera tiene que leerse sin competir con el
 * terreno, porque en el MVP la mayoría del mapa está sin reclamar y un overlay
 * saturado convertiría el mundo en una rejilla ilegible.
 */
function territoryStyle(t: SceneTerritory): { color: number; alpha: number } {
  if (t.ownerType === 'NONE') return { color: 0x8a8a8a, alpha: 0.05 };
  if (t.isOwn) return { color: 0x4ec27a, alpha: 0.13 };
  return { color: 0xc2544e, alpha: 0.13 };
}
