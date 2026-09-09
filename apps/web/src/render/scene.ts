/**
 * Escena isométrica en PixiJS.
 *
 * Vive FUERA del árbol de React a propósito: React gestiona el HUD y los menús,
 * PixiJS gestiona el mundo. Reconciliar cada frame del mundo a través del
 * renderizado de React sería tirar el rendimiento por una comodidad que no
 * necesitamos.
 *
 * Ver ../../../../docs/architecture/frontend.md
 */
import { Application, Container, Graphics, Text } from 'pixi.js';

import { TILE_H, TILE_W, depthOf, screenToTile, worldToIso, type Camera, type Tile } from './iso';
import { renderPosition, type ActiveMovement } from '../state/interpolation';

/** Colores por tipo de terreno (índice = valor del byte de TerrainType). */
const TERRAIN_COLORS = [
  0x6a9a3b, // 0 GRASSLAND
  0x2f6b33, // 1 FOREST
  0x8a7f5c, // 2 HILL
  0x6b6b6b, // 3 MOUNTAIN
  0x2f6f9f, // 4 WATER
  0xb59b66, // 5 ROAD
] as const;

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

export interface SceneData {
  readonly units: readonly SceneUnit[];
  readonly cities: readonly SceneCity[];
  /** Devuelve el terreno de un tile, o undefined si su chunk no está cargado. */
  readonly terrainAt: (x: number, y: number) => number | undefined;
  /** Instante del SERVIDOR, ya corregido por el desfase de reloj. */
  readonly serverTimeMs: () => number;
  readonly camera: Camera;
}

export interface SceneCallbacks {
  /** El jugador hizo clic sobre un tile del mundo. */
  onTileClick?: (tile: Tile) => void;
  /** El jugador hizo clic sobre una unidad. */
  onUnitClick?: (unitId: number) => void;
}

export class IsoScene {
  private app: Application | null = null;
  private readonly root = new Container();
  private readonly terrainLayer = new Container();
  private readonly entityLayer = new Container();
  private readonly terrainCache = new Map<string, Graphics>();
  private readonly unitSprites = new Map<number, Container>();
  private readonly citySprites = new Map<number, Container>();
  private data: SceneData | null = null;

  constructor(private readonly callbacks: SceneCallbacks = {}) {}

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
    this.root.addChild(this.entityLayer);
    app.stage.addChild(this.root);

    app.stage.eventMode = 'static';
    app.stage.hitArea = app.screen;
    app.stage.on('pointerdown', (event) => {
      if (!this.data) return;
      const tile = screenToTile(event.global.x, event.global.y, this.data.camera);

      // El picking de unidades tiene prioridad: seleccionar una unidad es más
      // frecuente que ordenar un movimiento a su tile exacto.
      const hit = this.data.units.find((u) => {
        const pos = this.positionOf(u);
        return Math.round(pos.x) === tile.x && Math.round(pos.y) === tile.y;
      });

      if (hit && this.callbacks.onUnitClick) {
        this.callbacks.onUnitClick(hit.id);
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
    this.unitSprites.clear();
    this.citySprites.clear();
  }

  resize(width: number, height: number): void {
    this.app?.renderer.resize(width, height);
  }

  /** Entrega el estado más reciente. Se llama cuando cambia el store, no cada frame. */
  update(data: SceneData): void {
    this.data = data;
  }

  /** Posición en la que se DIBUJA una unidad este frame (puede ser fraccionaria). */
  private positionOf(unit: SceneUnit): { x: number; y: number } {
    if (!unit.movement || !this.data) return { x: unit.x, y: unit.y };
    return renderPosition(unit.movement, this.data.serverTimeMs());
  }

  private draw(): void {
    const data = this.data;
    if (!data || !this.app) return;

    const { camera } = data;
    this.root.position.set(camera.viewportWidth / 2 - camera.x, camera.viewportHeight / 2 - camera.y);
    this.root.scale.set(camera.zoom);

    this.drawTerrain(data);
    this.drawCities(data);
    this.drawUnits(data);

    // Orden de dibujado isométrico: lo que está más "al fondo" (menor x+y) se
    // pinta antes. Sin esto, una unidad delante de un edificio se dibujaría
    // detrás de él.
    this.entityLayer.children.sort((a, b) => (a.zIndex ?? 0) - (b.zIndex ?? 0));
  }

  private drawTerrain(data: SceneData): void {
    // Sólo se dibujan los tiles visibles: culling por área de interés.
    const halfW = data.camera.viewportWidth / (2 * data.camera.zoom);
    const halfH = data.camera.viewportHeight / (2 * data.camera.zoom);
    const margin = 4;

    const centerIso = { x: data.camera.x, y: data.camera.y };
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
        const terrain = data.terrainAt(x, y);
        if (terrain === undefined) continue;

        const key = `${x}:${y}`;
        visible.add(key);

        let tile = this.terrainCache.get(key);
        if (!tile) {
          tile = this.createTerrainTile(x, y, terrain);
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

  private createTerrainTile(x: number, y: number, terrain: number): Graphics {
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

  private drawCities(data: SceneData): void {
    const seen = new Set<number>();

    for (const city of data.cities) {
      seen.add(city.id);
      let sprite = this.citySprites.get(city.id);

      if (!sprite) {
        sprite = new Container();
        const body = new Graphics();
        body
          .rect(-TILE_W / 2, -TILE_H * 1.5, TILE_W, TILE_H * 1.5)
          .fill({ color: city.isOwn ? 0xc9a227 : 0x8a5a2b })
          .stroke({ color: 0x000000, width: 1, alpha: 0.4 });
        sprite.addChild(body);

        const label = new Text({
          text: city.name,
          style: { fontSize: 11, fill: 0xffffff, fontFamily: 'monospace' },
        });
        label.anchor.set(0.5, 1);
        label.position.set(0, -TILE_H * 1.6);
        sprite.addChild(label);

        this.citySprites.set(city.id, sprite);
        this.entityLayer.addChild(sprite);
      }

      const iso = worldToIso(city.centerX, city.centerY);
      sprite.position.set(iso.x, iso.y);
      sprite.zIndex = depthOf(city.centerX, city.centerY);
      // Una ciudad protegida se distingue de un vistazo.
      sprite.alpha = city.presenceState === 'PROTECTED' ? 0.65 : 1;
    }

    this.prune(this.citySprites, seen);
  }

  private drawUnits(data: SceneData): void {
    const seen = new Set<number>();

    for (const unit of data.units) {
      seen.add(unit.id);
      let sprite = this.unitSprites.get(unit.id);

      if (!sprite) {
        sprite = new Container();
        const body = new Graphics();
        body
          .circle(0, -TILE_H / 3, 7)
          .fill({ color: unit.isOwn ? 0x4ea3ff : 0xff6b6b })
          .stroke({ color: 0x0b1a2a, width: 1.5 });
        sprite.addChild(body);
        this.unitSprites.set(unit.id, sprite);
        this.entityLayer.addChild(sprite);
      }

      // La posición dibujada es la INTERPOLADA, no la autoritativa: por eso el
      // movimiento se ve continuo aunque el servidor sólo avance un tile cada
      // 600 ms.
      const pos = this.positionOf(unit);
      const iso = worldToIso(pos.x, pos.y);
      sprite.position.set(iso.x, iso.y);
      sprite.zIndex = depthOf(pos.x, pos.y) + 0.5; // por delante del terreno y de la ciudad
      sprite.alpha = unit.isSelected ? 1 : 0.9;
      sprite.scale.set(unit.isSelected ? 1.25 : 1);
    }

    this.prune(this.unitSprites, seen);
  }

  private prune(map: Map<number, Container>, seen: Set<number>): void {
    for (const [id, sprite] of map) {
      if (seen.has(id)) continue;
      this.entityLayer.removeChild(sprite);
      sprite.destroy({ children: true });
      map.delete(id);
    }
  }
}
