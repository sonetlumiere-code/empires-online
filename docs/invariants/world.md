# Invariantes del Mundo (INV-WORLD-xxx)

Propiedades estructurales del grid, los chunks, la generación determinista del mapa y la línea temporal del simulador.

Formato y severidades: [README.md](README.md). Modelo de coordenadas y terreno: canon §4 y §5.

---

## Contexto

El mundo MVP es un grid de tiles cuadrados de `EO_WORLD_WIDTH` × `EO_WORLD_HEIGHT` = **512 × 512**, particionado en chunks de `EO_CHUNK_SIZE` = **32 × 32** tiles, lo que da 16 × 16 = **256 chunks**. El origen `(0,0)` está arriba-izquierda, `x` crece hacia el este, `y` hacia el sur, ambos `int32`.

```
        x=0        x=31 x=32       x=63           x=511
   y=0  +-------------+-------------+ ... +-------------+
        |  chunk 0    |  chunk 1    |     |  chunk 15   |
        |  (0,0)      |  (1,0)      |     |  (15,0)     |
   y=31 +-------------+-------------+ ... +-------------+
   y=32 |  chunk 16   |  chunk 17   |     |  chunk 31   |
        |  (0,1)      |  (1,1)      |     |  (15,1)     |
        +-------------+-------------+ ... +-------------+
        :             :             :     :             :
  y=511 +-------------+-------------+ ... +-------------+
                                               chunk 255

  chunkX = x / EO_CHUNK_SIZE ; chunkY = y / EO_CHUNK_SIZE      (World.ChunkOf)
  chunkId = chunkY*chunksPerRow + chunkX   (uint32, World.ChunkIndex)
  chunksPerRow = EO_WORLD_WIDTH / EO_CHUNK_SIZE = 16
```

El servidor **nunca** maneja píxeles: la proyección isométrica (`TILE_W = 64`, `TILE_H = 32`) es responsabilidad exclusiva del cliente. Ningún invariante de este archivo habla de pantalla.

Dos conceptos distintos que estos invariantes tratan por separado:

| Concepto | Fuente | Muta en runtime |
|---|---|---|
| **Terreno** (`TerrainType`) | Generación determinista desde `EO_WORLD_SEED`, persistido en `world_chunks` | No en MVP |
| **Capa de ocupación** (`blocked overlay`) | Edificios y zonas urbanas | Sí (creación de ciudad) |

«Transitable» = terreno con `walkable = sí` **y** ausencia de la capa de ocupación. `MOUNTAIN` y `WATER` son intransitables por terreno; el `TOWN_CENTER` y la zona urbana amurallada inicial bloquean por ocupación sin mutar el terreno base.

---

<a id="inv-world-001"></a>
## INV-WORLD-001 — Toda coordenada válida está dentro del mundo

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | TYPE, BOUNDARY, DOMAIN, TEST |
| Milestone | M1 |
| Política ante violación | FAIL_FAST en el dominio · REJECT en el borde |
| Cobertura | **Cubierto** por `TestLimitesDelMundo` y `TestFueraDeLimitesEsIntransitable` (`internal/game/world`), `TestDestinoFueraDeLimites` (`internal/pathfinding`) y `TestRechazosDeMovimiento` (`internal/game/simulation`) |

**Enunciado.** Toda coordenada `(x, y)` que el sistema acepte, almacene, derive o emita cumple `0 <= x < EO_WORLD_WIDTH` y `0 <= y < EO_WORLD_HEIGHT`, con `x, y` de tipo `int32`.

**Razón.** Una coordenada fuera de rango indexaría fuera del array de tiles: un índice que envuelve lee terreno de otra fila y produce paths a través de montañas y posiciones imposibles. Además, `chunkId` calculado sobre una coordenada negativa produce un id negativo convertido a `uint32`, que se traduce en broadcasts a suscriptores arbitrarios: es una fuga de información del mundo.

La implementación elige explícitamente **no entrar en pánico**: `World.TerrainAt` devuelve `WATER` fuera de límites e `World.IsWalkable` devuelve `false`, de modo que los bordes del mundo se comportan como un muro. Eso convierte el fallo en una negativa observable en lugar de en la caída del goroutine del tick, pero no exime del rechazo en el borde: una coordenada fuera de rango sigue siendo un dato que nunca debió entrar.

**Cómo se garantiza.**

- `BOUNDARY` — el manejador de `unit.move` valida el destino con `World.TileInBounds` **antes** de cualquier trabajo caro; fuera de rango se rechaza con `TARGET_OUT_OF_BOUNDS` (canon §16). Lo mismo aplica al centro de vista de `session.view`.
- `TYPE` — el dominio no pasea `int32` sueltos: usa el tipo `world.Tile` (`{X, Y int32}`) como unidad de coordenada, de modo que ninguna firma puede confundir el orden de los ejes ni mezclar tiles con píxeles. El tipo **no** valida por construcción: la validación de rango es responsabilidad de `World`, que es quien conoce las dimensiones.
- `DOMAIN` — todo acceso al terreno pasa por `World.TerrainAt(x, y)` / `World.IsWalkable(x, y)`, que comprueban `InBounds` antes de indexar y devuelven `WATER` / `false` fuera de rango. No existe ninguna ruta que indexe el array de terreno sin ese chequeo.
- `TYPE` — el protocolo (`packages/protocol/src/v1/`) declara las coordenadas como enteros con `Zod`, lo que descarta flotantes y `NaN` antes de llegar a Go.

> **No hay garantía `DB`.** La migración `000001_initial_schema.up.sql` **no** declara ningún `CHECK` de rango sobre `units.x`, `units.y`, `cities.center_x` ni `cities.center_y`. El límite superior es `EO_WORLD_WIDTH`/`EO_WORLD_HEIGHT`, que es configuración y no puede empotrarse como literal en una migración sin congelarla; el inferior tampoco está declarado. Un `INSERT` directo con `x = 99999` **es aceptado por PostgreSQL**: el rango se garantiza en `BOUNDARY`, `DOMAIN` y `TEST`, y así se declara aquí en lugar de prometer una constraint inexistente.

**Cómo se verifica.**

- `TestLimitesDelMundo` (unit, `internal/game/world`) — **existe y pasa**: `InBounds` rechaza `(-1,0)`, `(0,-1)`, `(width,0)` y `(0,height)`.
- `TestFueraDeLimitesEsIntransitable` (unit, `internal/game/world`) — **existe y pasa**: fuera de límites `TerrainAt` devuelve `WATER` e `IsWalkable` devuelve `false`, sin pánico.
- `TestDestinoFueraDeLimites` (unit, `internal/pathfinding`) — **existe y pasa**: el pathfinder devuelve `ErrTargetOutOfBounds`.
- `TestRechazosDeMovimiento` (simulation, `internal/game/simulation`) — **existe y pasa**: un `unit.move` fuera de límites se responde con `TARGET_OUT_OF_BOUNDS`.

**Violación en runtime.** Detección en `World.InBounds` y en el handler de comandos. Log `invariant_violation` con `inv_id=INV-WORLD-001` y la coordenada ofensiva. En el borde: `REJECT` con `TARGET_OUT_OF_BOUNDS` (regla de negocio) o `INVALID_MESSAGE` si el tipo es incorrecto. Si una coordenada fuera de rango aparece **persistida** —lo que la base de datos no impide—, la política es `FAIL_FAST` de la carga de esa entidad: no se incorpora a la simulación.

---

<a id="inv-world-002"></a>
## INV-WORLD-002 — Todo tile referenciado existe

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M1 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Parcial**: `TestNewRechazaDimensionesIncoherentes` y `TestChunkTerrainRespetaLaDisposicionFilaMayor` (`internal/game/world`) cubren la materialización en RAM; `TestMundoPersistidoCoincideByteAByteConLaSemilla` (integration) está **diseñado pero no ejecutado** (el daemon de Docker no arrancó) |

**Enunciado.** Todo tile referenciado por cualquier entidad (posición de unidad, centro de ciudad, waypoint de movimiento, rectángulo de territorio, safe zone) tiene una entrada de terreno cargada en memoria y un chunk persistido en `world_chunks`.

**Razón.** `INV-WORLD-001` garantiza el rango; este garantiza la **materialización**. Un mundo cargado parcialmente —por un chunk faltante en `world_chunks`, una migración a medias o un fallo de lectura silenciado— produce un array de terreno con huecos. Leer un hueco devuelve el valor cero de `TerrainType`, que es `GRASSLAND` (código 0, transitable): el fallo se disfraza de pradera y el pathfinder generará rutas por terreno inexistente.

**Cómo se garantiza.**

- `DOMAIN` — el mundo se **regenera desde `EO_WORLD_SEED` en cada arranque**: el array de terreno se aloja de una vez con tamaño `EO_WORLD_WIDTH * EO_WORLD_HEIGHT` y se rellena por completo, de modo que no puede haber huecos por construcción. No existe carga perezosa de chunks en MVP.
- `DOMAIN` — `world_chunks` guarda una copia del terreno para auditoría y para permitir mapas editados en el futuro, pero **no es la fuente primaria**. Su validación de arranque comprueba que existen exactamente `chunksPerRow * chunksPerColumn` = 256 filas y que cada `bytea` mide exactamente **1024 bytes** (32 × 32 tiles a 1 byte por tile, canon §5); una desviación aborta el arranque antes de abrir el puerto de `EO_HTTP_ADDR`.
- `DOMAIN` — `world.New` rechaza dimensiones incoherentes (mundo no múltiplo del chunk, `chunkSize` no positivo, longitud del terreno distinta de `width*height`), de modo que un mundo mal dimensionado no llega a existir.
- `DOMAIN` — `GET /ready` (canon §18) no responde `200` hasta que el mundo está completamente cargado y validado.

**Cómo se verifica.**

- `TestNewRechazaDimensionesIncoherentes` (unit, `internal/game/world`) — **existe y pasa**: un mundo cuyo terreno no mide `width*height`, o cuyas dimensiones no son múltiplos del chunk, no se construye.
- `TestChunkTerrainRespetaLaDisposicionFilaMayor` (unit, `internal/game/world`) — **existe y pasa**: el terreno de un chunk son exactamente `chunkSize*chunkSize` bytes en orden fila-mayor.
- `TestMundoPersistidoCoincideByteAByteConLaSemilla` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: las 256 filas de `world_chunks` coinciden byte a byte con el mundo regenerado desde la semilla.
- Test previsto: `Test_INV_WORLD_002_LoaderRejectsShortChunk` (integration) — un chunk de 1023 bytes debe abortar el arranque.

**Violación en runtime.** Detección en el cargador y en `World.TerrainAt` mediante un chequeo de longitud del slice. Log `invariant_violation` con `inv_id=INV-WORLD-002` y el `chunkId` afectado. Política `FAIL_FAST`: el proceso no debe servir un mundo incompleto, porque las decisiones que tome sobre terreno fantasma se persistirán como hechos.

---

<a id="inv-world-003"></a>
## INV-WORLD-003 — Un tile bloqueado nunca es waypoint de un path

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M4 |
| Política ante violación | FAIL_FAST (el movimiento no se crea) |
| Cobertura | **Cubierto** por `TestConstruccionesBloqueanLaRuta`, `TestRodeaElObstaculo`, `TestProhibidoAtajarEsquinas` y `TestSinRutaPosible` (`internal/pathfinding`) y `TestValidateComprobaciones` (`internal/domain/movement`) |

**Enunciado.** Ningún waypoint de una polilínea de `unit_movements` referencia un tile intransitable, entendiendo por intransitable un terreno con `walkable = no` (`MOUNTAIN`, `WATER`) o un tile marcado en la capa de ocupación.

**Razón.** Es la propiedad que sostiene la credibilidad física del mundo. Si un path atraviesa agua o una muralla, el jugador ve unidades caminando sobre lo imposible y, sobre todo, el modelo de bloqueo deja de ser una restricción: las murallas y el terreno dejan de significar algo, y con ellos toda la mecánica de territorio y protección que se apoya en la geometría.

Este invariante es la contraparte estructural de la regla de negocio `TARGET_NOT_WALKABLE`: la regla rechaza destinos intransitables (canon §8, el MVP **no** busca un tile cercano); el invariante prohíbe que el interior del camino los atraviese aunque el destino sea válido.

**Cómo se garantiza.**

- `DOMAIN` — el expansor de vecinos del A\* consulta `World.IsWalkable(Tile)`, que combina terreno y capa de ocupación. Un tile no transitable jamás entra en la open list, por lo que no puede aparecer en el path reconstruido.
- `DOMAIN` — validación de salida obligatoria: `movement.Validate(p TimedPath, grid CostGrid) error` recorre la polilínea completa y reverifica cada waypoint contra `IsWalkable` antes de persistir el movimiento, y **también al rehidratarla desde la base de datos**. Esta doble verificación es deliberada: protege contra un bug del pathfinder, que es el componente con más aristas, y contra una polilínea corrupta en disco.
- El pathfinder no depende del tipo concreto de mundo (`Pathfinder` es una interfaz estable, canon §8), así que la comprobación de salida es el único punto que no se puede sustituir al cambiar a Hierarchical A\*.

**Cómo se verifica.**

- `TestRodeaElObstaculo` (unit, `internal/pathfinding`) — **existe y pasa**: con un muro intransitable y una única abertura, el path la rodea y ningún waypoint es intransitable.
- `TestConstruccionesBloqueanLaRuta` (unit, `internal/pathfinding`) — **existe y pasa**: un rectángulo marcado en la capa de ocupación (no en el terreno) desvía la ruta.
- `TestProhibidoAtajarEsquinas` (unit, `internal/pathfinding`) — **existe y pasa**: no se atraviesa la esquina entre dos obstáculos ortogonales.
- `TestSinRutaPosible` (unit, `internal/pathfinding`) — **existe y pasa**: sin camino, `ErrPathNotFound`; no se devuelve una ruta parcial.
- `TestValidateComprobaciones` (unit, `internal/domain/movement`) — **existe y pasa**: `Validate` rechaza una polilínea con un waypoint intransitable.

**Violación en runtime.** Detección en `movement.Validate`. Log `invariant_violation` con `inv_id=INV-WORLD-003`, el `unitId` y el waypoint ofensivo. Política: el movimiento **no se crea**, la unidad permanece `IDLE` en su posición actual y el comando se responde con `unit.move.rejected` + `INTERNAL_ERROR`. No se intenta reparar el path: un pathfinder que devuelve rutas inválidas es un bug que debe verse, no absorberse.

> **Fuera de MVP.** La revalidación de paths ya activos cuando la capa de ocupación cambia bajo ellos (por ejemplo, una construcción nueva sobre el camino de una unidad en tránsito). En MVP la ocupación solo se altera en la creación de la ciudad inicial, que ocurre sobre terreno donde no hay movimientos activos. La política definitiva es **TBD (fuera de MVP)**.

---

<a id="inv-world-004"></a>
## INV-WORLD-004 — `tickNumber` es estrictamente monótono creciente

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | TYPE, DOMAIN, TEST |
| Milestone | M1 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Parcial**: `TestTickDebeDividirAMil` (`internal/config`) y `TestVolcadoPeriodicoNoOcurreEnCadaTick` (`internal/game/simulation`) ejercitan el calendario del loop; **no existe todavía** un test dedicado a la monotonía del contador |

**Enunciado.** Para dos observaciones sucesivas del loop, `tickNumber(n+1) > tickNumber(n)`, con `tickNumber` de tipo `uint64` contado desde `world_state.epoch_ms`; el valor nunca decrece ni se repite, tampoco a través de un reinicio del proceso.

**Razón.** `tickNumber` es el reloj lógico de toda la simulación: ordena los eventos de dominio, sella los deltas de red y es el campo `tick` de todo log estructurado (canon §18). Si retrocede, dos eventos distintos comparten sello temporal y el orden causal se pierde: un `UnitMovementCompleted` puede quedar ordenado antes de su `UnitMovementStarted`, y la reconstrucción de estado desde `world_events` deja de ser posible.

El riesgo real no es el contador en memoria, que es un `++`. Es el **reinicio**: `tickTime = epoch_ms + tickNumber * tickDurationMs` implica que al arrancar el tick se recalcula desde el reloj de pared. Un ajuste NTP hacia atrás, una máquina virtual restaurada de un snapshot o un `epoch_ms` mal leído producen un `tickNumber` inicial inferior al último emitido antes del corte.

**Cómo se garantiza.**

- `DOMAIN` — el loop incrementa el contador en un único punto, al inicio del tick, antes de la fase 1 (`drain commands`). Ninguna otra parte del sistema escribe `tickNumber`.
- `DOMAIN` — el calendario del loop es de **tiempo absoluto**: los ticks perdidos se descartan y jamás se ejecutan en ráfaga para «ponerse al día». Un catch-up en ráfaga sería la vía más directa a repetir sellos temporales.
- `DOMAIN` — en arranque se calcula `startTick = (Clock.NowMs() - world_state.epoch_ms) / tickDurationMs` y se compara con el último tick persistido en `world_state`. Si `startTick <= lastPersistedTick`, el arranque **aborta**: es un skew de reloj y continuar corrompería el orden.
- `TYPE` — el contador es `uint64` y solo se expone por lectura; el tipo descarta valores negativos por construcción.
- `DOMAIN` — el `Clock` inyectado (canon §1.5) es la única fuente de tiempo; el dominio no llama a `time.Now()`, lo que hace el comportamiento reproducible con `FakeClock`.

**Cómo se verifica.**

- `TestTickDebeDividirAMil` (unit, `internal/config`) — **existe y pasa**: `EO_TICK_RATE_HZ` debe dividir exactamente a 1000, de modo que `tickDurationMs` sea entero y `tickTime` no acumule deriva.
- `TestVolcadoPeriodicoNoOcurreEnCadaTick` (simulation, `internal/game/simulation`) — **existe y pasa**: el calendario por número de tick se respeta y no se ejecuta trabajo de más.
- Test previsto: `Test_INV_WORLD_004_TickNumberStrictlyIncreases` (simulation) — con `FakeClock`, avanzar 10 s a 10 Hz y comprobar que la secuencia observada es exactamente `n, n+1, …, n+100` sin huecos ni repeticiones.
- Test previsto: `Test_INV_WORLD_004_StartupRejectsClockGoingBackwards` (unit) — `FakeClock` posicionado antes del último tick persistido; el arranque devuelve error.
- Test previsto: `Test_INV_WORLD_004_OverrunDoesNotSkipCounter` (simulation) — un tick que tarda más que su período incrementa el contador una sola vez y eleva `eo_game_tick_overruns_total`.

**Violación en runtime.** Detección en el arranque (comparación con `world_state`) y en una aserción defensiva del loop (`next != current+1`). Log `invariant_violation` con `inv_id=INV-WORLD-004`, `tick` actual y esperado. Política `FAIL_FAST`: mejor un servidor caído y visible que una línea temporal corrupta que se persiste en silencio.

---

<a id="inv-world-005"></a>
## INV-WORLD-005 — Un mismo seed genera un mapa idéntico byte a byte

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M1 |
| Política ante violación | FAIL_FAST en arranque |
| Cobertura | **Cubierto** por `TestGeneracionEsDeterminista` y `TestGeneracionProduceTerrenoValidoYVariado` (`internal/game/world`); `TestMundoPersistidoCoincideByteAByteConLaSemilla` (integration) está **diseñado pero no ejecutado** |

**Enunciado.** Dos ejecuciones del generador de mundo con el mismo `EO_WORLD_SEED` (por defecto `20260909`) y las mismas dimensiones producen exactamente los mismos 256 chunks de 1024 bytes, byte a byte, en cualquier máquina y con cualquier versión de Go que compile el proyecto.

**Razón.** Sin esta propiedad no se puede: reproducir un bug de pathfinding a partir de un reporte, escribir un test de simulación con posiciones esperadas exactas, ni regenerar un entorno de desarrollo equivalente al de otro ingeniero. Es la manifestación concreta del principio de determinismo (canon §1.5) en la capa de mundo.

Las fuentes habituales de no-determinismo que este invariante prohíbe: iteración de `map` de Go sin orden (canon §8 lo prohíbe explícitamente), `rand` global sin semilla inyectada, concurrencia con orden de escritura no determinista, y aritmética de punto flotante cuyo redondeo varía. El generador usa la abstracción `RandomSource` inyectada, sembrada con `EO_WORLD_SEED`.

**Cómo se garantiza.**

- `DOMAIN` — el generador recibe `RandomSource` por inyección; no existe acceso a `math/rand` global desde `internal/game/world`.
- `DOMAIN` — el recorrido de generación es un doble bucle `for y { for x { … } }` en orden creciente, nunca una iteración de mapa ni un `errgroup` con escrituras concurrentes al mismo buffer.
- `DOMAIN` — el resultado se serializa a `world_chunks` como `bytea` de 1024 bytes con el terreno como `uint8` según los códigos del canon §5 (`GRASSLAND=0` … `ROAD=5`), sin padding ni cabeceras variables.
- `DOMAIN` — el mundo se regenera desde la semilla en cada arranque y `world_chunks` conserva la copia escrita la primera vez. La comparación de arranque es **contra esa copia**, chunk a chunk: si el terreno regenerado no coincide con el persistido, el generador cambió sin migración de mundo y el arranque aborta.

> **Precisión sobre el mecanismo.** `world_state` (migración `000001`) tiene exactamente las columnas `id`, `seed`, `width`, `height`, `chunk_size`, `epoch_ms`, `current_tick`, `created_at` y `updated_at`: **no existe ninguna columna de hash del mundo**. Cualquier redacción que cite un `world_hash` almacenado describe un mecanismo que no está implementado. La detección de deriva se apoya en la comparación byte a byte contra `world_chunks` y en el golden test previsto más abajo; añadir una columna de hash sería un cambio de esquema y exigiría ADR y migración: **TBD (fuera de MVP)**.

**Cómo se verifica.**

- `TestGeneracionEsDeterminista` (unit, `internal/game/world`) — **existe y pasa**: dos generaciones con el mismo seed coinciden byte a byte, y un seed distinto produce un mapa distinto (control negativo incluido).
- `TestGeneracionProduceTerrenoValidoYVariado` (unit, `internal/game/world`) — **existe y pasa**: todo byte generado corresponde a un `TerrainType` conocido.
- `TestMundoPersistidoCoincideByteAByteConLaSemilla` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: la copia de `world_chunks` coincide con el mundo regenerado.
- Test previsto: `Test_INV_WORLD_005_GoldenHashMatches` (unit) — SHA-256 de la concatenación de los 256 chunks contra un golden file en `services/game-server/testdata/`. Falla intencionadamente cuando alguien cambia el generador: la respuesta correcta es un ADR y un golden nuevo, no editar el fixture sin pensar.

**Violación en runtime.** Detección en arranque por comparación del terreno regenerado contra `world_chunks`. Log `invariant_violation` con `inv_id=INV-WORLD-005`, seed y el `chunkId` divergente. Política `FAIL_FAST`: arrancar sobre un mundo distinto al que las entidades persistidas asumen colocaría ciudades sobre agua.

---

<a id="inv-world-006"></a>
## INV-WORLD-006 — Cada tile pertenece a exactamente un chunk

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | TYPE, DOMAIN, TEST |
| Milestone | M1 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto** por `TestConversionTileChunk` y `TestChunksEnRadioSeRecortanYVanOrdenados` (`internal/game/world`) y `TestMundoDebeSerMultiploDelChunk` (`internal/config`) |

**Enunciado.** La composición `World.ChunkOf(x, y) = (x / EO_CHUNK_SIZE, y / EO_CHUNK_SIZE)` seguida de `World.ChunkIndex(cx, cy) = cy * chunksPerRow + cx` es total sobre el dominio válido de coordenadas y define una **partición**: cada tile válido pertenece a un chunk y solo a uno, y la unión de los chunks cubre el mundo entero sin solapes ni huecos.

**Razón.** El interest management del canon §13 suscribe por chunk con radio `EO_INTEREST_RADIUS_CHUNKS = 2`. Si un tile perteneciera a dos chunks, las entidades sobre él se emitirían dos veces al mismo cliente (`entity.spawn` duplicado, contadores de entidades inflados). Si no perteneciera a ninguno, esas entidades serían **invisibles**: existirían en el servidor y nunca aparecerían en pantalla, un fallo mucho peor porque no genera error, solo ausencia.

**Cómo se garantiza.**

- `DOMAIN` — la partición es exacta **solo si** el mundo es múltiplo del chunk. `config.Load` valida que `EO_WORLD_WIDTH % EO_CHUNK_SIZE == 0` y `EO_WORLD_HEIGHT % EO_CHUNK_SIZE == 0`, y `world.New` lo revalida; si no se cumple, el arranque aborta. Un mundo de 500 de ancho dejaría una columna de tiles en un chunk truncado. La implementación usa **división entera** (`x / chunkSize`), no un desplazamiento de bits, de modo que no exige que `EO_CHUNK_SIZE` sea potencia de dos.
- `TYPE` — existe una única implementación, `World.ChunkOf` / `World.ChunkIndex`, exportada desde `internal/game/world`. Está prohibido recalcular `x/32` a mano en otro paquete; la revisión de PR lo trata como defecto.
- `DOMAIN` — la operación es aritmética pura sobre una coordenada ya validada por [INV-WORLD-001](#inv-world-001), por lo que no puede producir un id fuera de `[0, chunksPerRow * chunksPerColumn)`.

**Cómo se verifica.**

- `TestConversionTileChunk` (unit, `internal/game/world`) — **existe y pasa**: cada tile cae en un único chunk, y `(31,31)` y `(32,32)` caen en chunks distintos.
- `TestChunksEnRadioSeRecortanYVanOrdenados` (unit, `internal/game/world`) — **existe y pasa**: el conjunto de chunks del radio de interés se recorta al mundo y sale en orden estable, sin repeticiones.
- `TestMundoDebeSerMultiploDelChunk` (unit, `internal/config`) — **existe y pasa**: `EO_WORLD_WIDTH = 500` con `EO_CHUNK_SIZE = 32` aborta la configuración.
- Test previsto: `Test_INV_WORLD_006_ChunkBoundaryEntitiesEmittedOnce` (integration) — una unidad en `(31,31)` y otra en `(32,32)` producen exactamente un `entity.spawn` cada una para un observador que cubre ambos chunks.

**Violación en runtime.** Detección en la validación de configuración de arranque y en una aserción del emisor de deltas que comprueba `chunkId < chunkCount`. Log `invariant_violation` con `inv_id=INV-WORLD-006`. Política `FAIL_FAST` en arranque; en el emisor, se omite el broadcast del chunk inválido y se escala, para no derribar el loop por un fallo de visibilidad.

---

## Trazabilidad

| Invariante | Componente propietario | Documentos que lo citan |
|---|---|---|
| INV-WORLD-001 | `internal/game/world` | [movement.md](movement.md), [units.md](units.md), [city.md](city.md) |
| INV-WORLD-002 | `internal/game/world` | [persistence.md](persistence.md) |
| INV-WORLD-003 | `internal/pathfinding` | [movement.md](movement.md) |
| INV-WORLD-004 | `internal/game/loop` | [movement.md](movement.md), [persistence.md](persistence.md) |
| INV-WORLD-005 | `internal/game/world` | [persistence.md](persistence.md) |
| INV-WORLD-006 | `internal/game/world` | [../architecture/game-loop.md](../architecture/game-loop.md) |
