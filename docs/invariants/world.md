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
| Cobertura | **Cubierto**: `TestLimitesDelMundo`, `TestFueraDeLimitesEsIntransitable` y `TestChunksEnRadioSeRecortanYVanOrdenados` (`internal/game/world`), `TestDestinoFueraDeLimites` (`internal/pathfinding`) y `TestRechazosDeMovimiento` (`internal/game/simulation`) cubren lo que entra por la red; `TestUnaEntidadFueraDelMundoAbortaElArranque` (`internal/game/loadcheck`), las coordenadas persistidas |

**Enunciado.** Toda coordenada `(x, y)` que el sistema acepte, almacene, derive o emita cumple `0 <= x < EO_WORLD_WIDTH` y `0 <= y < EO_WORLD_HEIGHT`, con `x, y` de tipo `int32`.

**Razón.** Una coordenada fuera de rango indexaría fuera del array de tiles: un índice que envuelve lee terreno de otra fila y produce paths a través de montañas y posiciones imposibles. Además, `chunkId` calculado sobre una coordenada negativa produce un id negativo convertido a `uint32`, que se traduce en broadcasts a suscriptores arbitrarios: es una fuga de información del mundo.

La implementación elige explícitamente **no entrar en pánico**: `World.TerrainAt` devuelve `WATER` fuera de límites e `World.IsWalkable` devuelve `false`, de modo que los bordes del mundo se comportan como un muro. Eso convierte el fallo en una negativa observable en lugar de en la caída del goroutine del tick, pero no exime del rechazo en el borde: una coordenada fuera de rango sigue siendo un dato que nunca debió entrar.

**Cómo se garantiza.**

- `BOUNDARY` — el manejador de `unit.move` valida el destino con `World.TileInBounds` **antes** de cualquier trabajo caro; fuera de rango se rechaza con `TARGET_OUT_OF_BOUNDS` (canon §16). El centro de `session.view` **no** se rechaza: `World.ChunksInRadius` recorta el área de interés al mundo, de modo que un centro fuera de rango produce un snapshot de los chunks válidos más cercanos y nunca un chunk inexistente.
- `TYPE` — el dominio no pasea `int32` sueltos: usa el tipo `world.Tile` (`{X, Y int32}`) como unidad de coordenada, de modo que ninguna firma puede confundir el orden de los ejes ni mezclar tiles con píxeles. El tipo **no** valida por construcción: la validación de rango es responsabilidad de `World`, que es quien conoce las dimensiones.
- `DOMAIN` — todo acceso al terreno pasa por `World.TerrainAt(x, y)` / `World.IsWalkable(x, y)`, que comprueban `InBounds` antes de indexar y devuelven `WATER` / `false` fuera de rango. No existe ninguna ruta que indexe el array de terreno sin ese chequeo.
- `DOMAIN` — al arrancar, la validación de carga (`internal/game/loadcheck`, `EO-119`) comprueba con `World.InBounds` las coordenadas persistidas que la base no limita: la posición de toda unidad viva no guarnecida y el centro de toda ciudad ([INV-UNIT-001](units.md#inv-unit-001), [INV-CITY-007](city.md#inv-city-007)).
- `TYPE` — el protocolo (`packages/protocol/src/v1/`) declara las coordenadas como enteros con `Zod`, y el servidor las decodifica en `int32` con `protocol.DecodeStrict`: un flotante, un `NaN` o un valor fuera de `int32` no decodifica y se responde `INVALID_MESSAGE`.

> **No hay garantía `DB`.** La migración `000001_initial_schema.up.sql` **no** declara ningún `CHECK` de rango sobre `units.x`, `units.y`, `cities.center_x` ni `cities.center_y`. El límite superior es `EO_WORLD_WIDTH`/`EO_WORLD_HEIGHT`, que es configuración y no puede empotrarse como literal en una migración sin congelarla; el inferior tampoco está declarado. Un `INSERT` directo con `x = 99999` **es aceptado por PostgreSQL**: el rango se garantiza en `BOUNDARY`, `DOMAIN` y `TEST`, y así se declara aquí en lugar de prometer una constraint inexistente.

**Cómo se verifica.**

- `TestLimitesDelMundo` (unit, `internal/game/world`) — `InBounds` rechaza `(-1,0)`, `(0,-1)`, `(width,0)` y `(0,height)`.
- `TestFueraDeLimitesEsIntransitable` (unit, `internal/game/world`) — fuera de límites `TerrainAt` devuelve `WATER` e `IsWalkable` devuelve `false`, sin pánico.
- `TestDestinoFueraDeLimites` (unit, `internal/pathfinding`) — el pathfinder devuelve `ErrTargetOutOfBounds`.
- `TestRechazosDeMovimiento` (simulation, `internal/game/simulation`) — un `unit.move` fuera de límites se responde con `TARGET_OUT_OF_BOUNDS`.
- `TestChunksEnRadioSeRecortanYVanOrdenados` (unit, `internal/game/world`) — el área de interés se recorta al mundo.
- `TestUnaEntidadFueraDelMundoAbortaElArranque` (unit, `internal/game/loadcheck`) — una unidad en `x = -1` y un centro en `y = height` producen dos hallazgos `FAIL_FAST`.

**Violación en runtime.** En el borde, una coordenada fuera de rango es la regla de negocio, no una violación: `REJECT` con `TARGET_OUT_OF_BOUNDS`, o `INVALID_MESSAGE` si el tipo es incorrecto, sin log de invariante. Una coordenada fuera de rango **persistida** —lo que la base de datos no impide— la detecta la validación de carga: log `invariant_violation` con el `inv_id` de la entidad (`INV-UNIT-001` o `INV-CITY-007`) y política `FAIL_FAST`; el arranque aborta sin escribir nada.

---

<a id="inv-world-002"></a>
## INV-WORLD-002 — Todo tile referenciado existe

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M1 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto** por `TestNewRechazaDimensionesIncoherentes` y `TestChunkTerrainRespetaLaDisposicionFilaMayor` (`internal/game/world`) y por `TestMundoPersistidoCoincideByteAByteConLaSemilla` y `TestElArranqueRechazaUnTerrenoPersistidoQueNoEsElDeLaSemilla` (integration, `internal/persistence/postgres`) |

**Enunciado.** Todo tile referenciado por cualquier entidad (posición de unidad, centro de ciudad, waypoint de movimiento, rectángulo de territorio, safe zone) tiene una entrada de terreno cargada en memoria y un chunk persistido en `world_chunks`.

**Razón.** `INV-WORLD-001` garantiza el rango; este garantiza la **materialización**. Un mundo cargado parcialmente —por un chunk faltante en `world_chunks`, una migración a medias o un fallo de lectura silenciado— produce un array de terreno con huecos. Leer un hueco devuelve el valor cero de `TerrainType`, que es `GRASSLAND` (código 0, transitable): el fallo se disfraza de pradera y el pathfinder generará rutas por terreno inexistente.

**Cómo se garantiza.**

- `DOMAIN` — el mundo se **regenera desde `EO_WORLD_SEED` en cada arranque**: el array de terreno se aloja de una vez con tamaño `EO_WORLD_WIDTH * EO_WORLD_HEIGHT` y se rellena por completo, de modo que no puede haber huecos por construcción. No existe carga perezosa de chunks en MVP.
- `DOMAIN` — `world_chunks` guarda una copia del terreno para auditoría y para permitir mapas editados en el futuro, pero **no es la fuente primaria**. En cada arranque posterior al primero, `WorldRepo.VerifyTerrain` la lee entera y comprueba que hay exactamente `chunksPerRow * chunksPerColumn` = 256 filas, que cada `bytea` mide exactamente **1024 bytes** (32 × 32 tiles a 1 byte por tile, canon §5) y que coincide byte a byte con el terreno regenerado ([INV-WORLD-005](#inv-world-005)); una desviación aborta el arranque antes de abrir el puerto de `EO_HTTP_ADDR`. Hasta `EO-118` esa comprobación sólo existía en un test: el arranque no leía `world_chunks`.
- `DOMAIN` — `world.New` rechaza dimensiones incoherentes (mundo no múltiplo del chunk, `chunkSize` no positivo, longitud del terreno distinta de `width*height`), de modo que un mundo mal dimensionado no llega a existir.
- `DOMAIN` — el servidor HTTP, que sirve `GET /ready` (canon §18) y el WebSocket, sólo arranca cuando el mundo está cargado y verificado.

**Cómo se verifica.**

- `TestNewRechazaDimensionesIncoherentes` (unit, `internal/game/world`) — un mundo cuyo terreno no mide `width*height`, o cuyas dimensiones no son múltiplos del chunk, no se construye.
- `TestChunkTerrainRespetaLaDisposicionFilaMayor` (unit, `internal/game/world`) — el terreno de un chunk son exactamente `chunkSize*chunkSize` bytes en orden fila-mayor.
- `TestMundoPersistidoCoincideByteAByteConLaSemilla` (integration, `internal/persistence/postgres`) — lo que se guarda la primera vez coincide byte a byte con el mundo regenerado desde la semilla.
- `TestElArranqueRechazaUnTerrenoPersistidoQueNoEsElDeLaSemilla` (integration) — un byte alterado, un chunk de 1023 bytes y un chunk que falta hacen fallar `VerifyTerrain`, y el error nombra el chunk. Verificado por mutación. Verificado además en vivo contra la base de desarrollo: «mundo existente cargado y verificado».

**Violación en runtime.** Detección en `VerifyTerrain`, al arrancar. Log `invariant_violation` (con `inv_id=INV-WORLD-005`, que es la comprobación que la contiene) y el chunk afectado. Política `FAIL_FAST`: el proceso no arranca, porque las decisiones que tome sobre terreno fantasma se persistirían como hechos. En RAM no hay huecos que detectar: el terreno se aloja entero de una vez.

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
- `DOMAIN` — `movement.BuildTimedPath` rechaza un tile intransitable al construir la polilínea, y `movement.Validate` recorre la polilínea completa al **rehidratarla** desde la base de datos y reverifica cada waypoint contra `IsWalkable`, y cada diagonal contra sus dos ortogonales. Protege contra un bug del pathfinder, que es el componente con más aristas, y contra una polilínea corrupta en disco.
- `DOMAIN` — la capa de ocupación sólo cambia con el servidor en marcha cuando se funda una ciudad, y en ese tick `TimedPath.ValidateFrom` revalida la ruta pendiente de todo movimiento activo: la que la muralla nueva corta se detiene con `PATH_BLOCKED` ([RN-CITY-022](../specs/city.md)). Así ningún waypoint **por recorrer** queda sobre un tile bloqueado; los ya recorridos no importan.
- El pathfinder no depende del tipo concreto de mundo (`Pathfinder` es una interfaz estable, canon §8), así que la comprobación de salida es el único punto que no se puede sustituir al cambiar a Hierarchical A\*.

**Cómo se verifica.**

- `TestRodeaElObstaculo` (unit, `internal/pathfinding`) — con un muro intransitable y una única abertura, el path la rodea y ningún waypoint es intransitable.
- `TestConstruccionesBloqueanLaRuta` (unit, `internal/pathfinding`) — un rectángulo marcado en la capa de ocupación (no en el terreno) desvía la ruta.
- `TestProhibidoAtajarEsquinas` (unit, `internal/pathfinding`) — no se atraviesa la esquina entre dos obstáculos ortogonales.
- `TestSinRutaPosible` (unit, `internal/pathfinding`) — sin camino, `ErrPathNotFound`; no se devuelve una ruta parcial.
- `TestValidateComprobaciones` (unit, `internal/domain/movement`) — `Validate` rechaza una polilínea con un waypoint intransitable o una esquina atajada.
- `TestUnaRutaQueCruzaLaMurallaNuevaSeDetieneConPathBlocked` (simulation, `internal/game/simulation`) — la ruta pendiente que una muralla nueva corta se detiene.

**Violación en runtime.** Al crear, un tile intransitable en la ruta hace fallar `BuildTimedPath`: se registra un `error`, el movimiento **no se crea** y el comando se responde con `unit.move.rejected` + `INTERNAL_ERROR`. Al rehidratar, `Validate` cierra el movimiento `FAILED` con un `warn` y la unidad queda donde estaba. No se intenta reparar el path: un pathfinder que devuelve rutas inválidas es un bug que debe verse, no absorberse.


---

<a id="inv-world-004"></a>
## INV-WORLD-004 — `tickNumber` es estrictamente monótono creciente

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | TYPE, DOMAIN, TEST |
| Milestone | M1 |
| Política ante violación | REPAIR (arrancar desde el último tick guardado) |
| Cobertura | **Cubierto** por `TestElTickSeReanclaAlRelojAlArrancar` (`internal/game/loop`) y `TestElTickEsEstrictamenteCreciente` (`internal/game/simulation`) |

**Enunciado.** Para dos observaciones sucesivas del loop, `tickNumber(n+1) > tickNumber(n)`, con `tickNumber` de tipo `uint64`; el valor nunca decrece ni se repite, tampoco a través de un reinicio del proceso, sea un apagado ordenado o una caída.

**Razón.** `tickNumber` es la etiqueta lógica de la simulación: sella el campo `tick` de los logs estructurados (canon §18), el de `world.snapshot` y la columna `tick` de `world_events`, que es durable. Si retrocede, dos eventos distintos comparten sello y el orden causal se pierde: un evento posterior a una caída puede quedar ordenado antes que uno anterior, y la reconstrucción de estado desde `world_events` deja de ser posible.

El riesgo real no es el contador en memoria, que es un `++`. Es el **reinicio**. Hasta `EO-118` el loop retomaba el contador de `world_state.current_tick`, que sólo se escribe en el apagado ordenado: tras una caída volvía al valor del último apagado ordenado, que podía ser de horas antes, y repetía números ya emitidos.

**Cómo se garantiza.**

- `DOMAIN` — el loop incrementa el contador en un único punto, al inicio del tick, antes de la fase 1 (`drain commands`). Ninguna otra parte del sistema escribe `tickNumber`.
- `DOMAIN` — el calendario del loop es de **tiempo absoluto**: los ticks perdidos se descartan, no se cuentan y jamás se ejecutan en ráfaga para «ponerse al día». De ahí sale la cota que sostiene todo lo demás: el loop nunca ejecuta más de un tick por período, así que en todo momento `tick <= (ahora − epoch_ms) / tickDurationMs`.
- `DOMAIN` — **al arrancar, el tick se reancla al reloj** ([ADR-015](../decisions/ADR-015-tick-continuity.md)): `loop.ResumeTick` devuelve `max(world_state.current_tick, (ahora − epoch_ms) / tickDurationMs)`. Por la cota anterior, el valor derivado del reloj supera todo tick emitido antes, se apagara el proceso como se apagara. `world_state.current_tick`, que se sigue guardando en el apagado ordenado, queda como suelo frente a un reloj que va hacia atrás.
- `TYPE` — el contador es `uint64` y sólo se expone por lectura; el tipo descarta valores negativos por construcción.
- `DOMAIN` — el `Clock` inyectado (canon §1.5) es la única fuente de tiempo; `ResumeTick` es una función pura que recibe el instante como argumento.

**Cómo se verifica.**

- `TestElTickSeReanclaAlRelojAlArrancar` (unit, `internal/game/loop`) — tras una caída con el último guardado muy atrasado arranca en el valor del reloj; con el reloj por detrás de lo guardado arranca en lo guardado y lo señala; un mundo recién creado arranca en cero; y la cota se sostiene: el arranque siguiente queda por encima de todo lo ejecutado.
- `TestElTickEsEstrictamenteCreciente` (simulation, `internal/game/simulation`) — el contador avanza en exactamente uno por tick ejecutado, con el reloj quieto o dando saltos de una hora.
- `TestVolcadoPeriodicoNoOcurreEnCadaTick` (simulation) — el calendario por número de tick se respeta.
- `TestTickDebeDividirAMil` (unit, `internal/config`) — `EO_TICK_RATE_HZ` divide exactamente a 1000, de modo que `tickDurationMs` es entero.
- Verificado en vivo contra la base de desarrollo, con `world_state.current_tick = 22`: el primer arranque empezó en el tick 35 619 858; tras matar el proceso sin apagado ordenado, el segundo empezó en 35 620 115. Con el código anterior los dos habrían empezado en 22.

**Violación en runtime.** Si el reloj va por detrás del último tick guardado —un ajuste NTP hacia atrás, una máquina virtual restaurada de un snapshot—, el arranque no aborta: empieza desde el valor guardado, que preserva la monotonía frente a todo lo que se guardó, y emite `invariant_violation` con `inv_id=INV-WORLD-004`. El riesgo residual es un doble fallo: que el reloj retroceda más allá del último guardado **y** que el proceso hubiera caído después de él; entonces podrían repetirse los ticks de ese intervalo. Abortar convertiría un ajuste de reloj en una caída de servicio sin proteger nada: el tick es una etiqueta, y el tiempo de juego lo da el reloj.

---

<a id="inv-world-005"></a>
## INV-WORLD-005 — Un mismo seed genera un mapa idéntico byte a byte

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M1 |
| Política ante violación | FAIL_FAST en arranque |
| Cobertura | **Cubierto** por `TestGeneracionEsDeterminista`, `TestElMundoCanonicoConservaSuHuella` y `TestGeneracionProduceTerrenoValidoYVariado` (`internal/game/world`) y por `TestMundoPersistidoCoincideByteAByteConLaSemilla` y `TestElArranqueRechazaUnTerrenoPersistidoQueNoEsElDeLaSemilla` (integration) |

**Enunciado.** Dos ejecuciones del generador de mundo con el mismo `EO_WORLD_SEED` (por defecto `20260909`) y las mismas dimensiones producen exactamente los mismos 256 chunks de 1024 bytes, byte a byte, en cualquier máquina y con cualquier versión de Go que compile el proyecto.

**Razón.** Sin esta propiedad no se puede: reproducir un bug de pathfinding a partir de un reporte, escribir un test de simulación con posiciones esperadas exactas, ni regenerar un entorno de desarrollo equivalente al de otro ingeniero. Es la manifestación concreta del principio de determinismo (canon §1.5) en la capa de mundo.

Las fuentes habituales de no-determinismo que este invariante prohíbe: iteración de `map` de Go sin orden (canon §8 lo prohíbe explícitamente), `rand` global sin semilla inyectada, concurrencia con orden de escritura no determinista, y aritmética de punto flotante cuyo redondeo varía. El generador no usa azar alguno: `world.Generate` construye ruido de valor a partir de un hash entero de la semilla y de las coordenadas, en aritmética entera.

**Cómo se garantiza.**

- `DOMAIN` — `world.Generate(width, height, seed)` es una función pura de sus argumentos: ruido de valor por hash entero de la semilla, sin `RandomSource`, sin `math/rand` y sin punto flotante. El lint de determinismo (`scripts/lint-determinism.mjs`) prohíbe el reloj real y el azar global en `internal/game/world`.
- `DOMAIN` — el recorrido de generación es un doble bucle `for y { for x { … } }` en orden creciente, nunca una iteración de mapa ni un `errgroup` con escrituras concurrentes al mismo buffer.
- `DOMAIN` — el resultado se serializa a `world_chunks` como `bytea` de 1024 bytes con el terreno como `uint8` según los códigos del canon §5 (`GRASSLAND=0` … `ROAD=5`), sin padding ni cabeceras variables.
- `DOMAIN` — el mundo se regenera desde la semilla en cada arranque y `world_chunks` conserva la copia escrita la primera vez. `WorldRepo.VerifyTerrain` compara **contra esa copia**, chunk a chunk: si el terreno regenerado no coincide con el persistido, el generador cambió sin migración de mundo y el arranque aborta. Hasta `EO-118` esa comparación sólo existía en un test, y un generador cambiado habría arrancado en silencio sobre un mapa distinto.

> **Precisión sobre el mecanismo.** `world_state` (migración `000001`) tiene exactamente las columnas `id`, `seed`, `width`, `height`, `chunk_size`, `epoch_ms`, `current_tick`, `created_at` y `updated_at`: **no existe ninguna columna de hash del mundo**. Cualquier redacción que cite un `world_hash` almacenado describe un mecanismo que no está implementado. La detección de deriva se apoya en la comparación byte a byte contra `world_chunks` y en el golden test de más abajo; añadir una columna de hash sería un cambio de esquema y exigiría ADR y migración: **TBD (fuera de MVP)**.

**Cómo se verifica.**

- `TestGeneracionEsDeterminista` (unit, `internal/game/world`) — dos generaciones con el mismo seed coinciden byte a byte, y un seed distinto produce un mapa distinto (control negativo incluido).
- `TestGeneracionProduceTerrenoValidoYVariado` (unit, `internal/game/world`) — todo byte generado corresponde a un `TerrainType` conocido.
- `TestMundoPersistidoCoincideByteAByteConLaSemilla` (integration, `internal/persistence/postgres`) — la copia de `world_chunks` coincide con el mundo regenerado.
- `TestElArranqueRechazaUnTerrenoPersistidoQueNoEsElDeLaSemilla` (integration) — la comprobación de arranque rechaza un byte alterado, un chunk corto y un chunk que falta.
- `TestElMundoCanonicoConservaSuHuella` (unit, `internal/game/world`) — SHA-256 del terreno de `EO_WORLD_SEED = 20260909` sobre 512 × 512 contra una constante. Falla intencionadamente cuando alguien cambia el generador: la respuesta correcta es un ADR y una migración de mundo, no actualizar la constante.

**Violación en runtime.** Detección en el arranque, en `VerifyTerrain`. Log `invariant_violation` con `inv_id=INV-WORLD-005`, la semilla y el chunk divergente. Política `FAIL_FAST`: el proceso no arranca, porque arrancar sobre un mundo distinto al que las entidades persistidas asumen colocaría ciudades sobre agua.

---

<a id="inv-world-006"></a>
## INV-WORLD-006 — Cada tile pertenece a exactamente un chunk

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M1 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto** por `TestConversionTileChunk` y `TestChunksEnRadioSeRecortanYVanOrdenados` (`internal/game/world`) y `TestMundoDebeSerMultiploDelChunk` (`internal/config`) |

**Enunciado.** La composición `World.ChunkOf(x, y) = (x / EO_CHUNK_SIZE, y / EO_CHUNK_SIZE)` seguida de `World.ChunkIndex(cx, cy) = cy * chunksPerRow + cx` es total sobre el dominio válido de coordenadas y define una **partición**: cada tile válido pertenece a un chunk y solo a uno, y la unión de los chunks cubre el mundo entero sin solapes ni huecos.

**Razón.** El interest management del canon §13 suscribe por chunk con radio `EO_INTEREST_RADIUS_CHUNKS = 2`. Si un tile perteneciera a dos chunks, las entidades sobre él se emitirían dos veces al mismo cliente (`entity.spawn` duplicado, contadores de entidades inflados). Si no perteneciera a ninguno, esas entidades serían **invisibles**: existirían en el servidor y nunca aparecerían en pantalla, un fallo mucho peor porque no genera error, solo ausencia.

**Cómo se garantiza.**

- `DOMAIN` — la partición es exacta **solo si** el mundo es múltiplo del chunk. `config.Load` valida que `EO_WORLD_WIDTH % EO_CHUNK_SIZE == 0` y `EO_WORLD_HEIGHT % EO_CHUNK_SIZE == 0`, y `world.New` lo revalida; si no se cumple, el arranque aborta. Un mundo de 500 de ancho dejaría una columna de tiles en un chunk truncado. La implementación usa **división entera** (`x / chunkSize`), no un desplazamiento de bits, de modo que no exige que `EO_CHUNK_SIZE` sea potencia de dos.
- `DOMAIN` — la referencia es `World.ChunkOf` / `World.ChunkIndex`, exportada desde `internal/game/world`, y es la que usa la simulación para su índice por chunk, el reparto de deltas y el snapshot. **No es la única**: `UnitRepo` divide por el tamaño de chunk con el que se construye para las columnas desnormalizadas de `units` ([INV-UNIT-011](units.md#inv-unit-011)) y la huella de un territorio calcula sus chunks con su propia división entera (`TestLaHuellaUsaDivisionEnteraYNoDesplazamientoDeBits`). Las tres usan la misma aritmética y el mismo `EO_CHUNK_SIZE`, y sus tests comprueban el resultado; que no diverjan es responsabilidad de la revisión.
- `DOMAIN` — la operación es aritmética pura sobre una coordenada ya validada por [INV-WORLD-001](#inv-world-001), por lo que no puede producir un id fuera de `[0, chunksPerRow * chunksPerColumn)`.

**Cómo se verifica.**

- `TestConversionTileChunk` (unit, `internal/game/world`) — cada tile cae en un único chunk, y `(31,31)` y `(32,32)` caen en chunks distintos.
- `TestChunksEnRadioSeRecortanYVanOrdenados` (unit, `internal/game/world`) — el conjunto de chunks del radio de interés se recorta al mundo y sale en orden estable, sin repeticiones.
- `TestMundoDebeSerMultiploDelChunk` (unit, `internal/config`) — `EO_WORLD_WIDTH = 500` con `EO_CHUNK_SIZE = 32` aborta la configuración.
- `TestBroadcastChunksEntregaUnaSolaVezAQuienEstaSuscritoAVarios` (unit, `internal/websocket`) — un mensaje dirigido a varios chunks llega una sola vez a quien está suscrito a más de uno.
- Test previsto: una unidad en `(31,31)` y otra en `(32,32)` aparecen exactamente una vez cada una en el snapshot de un observador que cubre ambos chunks.

**Violación en runtime.** Detección en la validación de configuración de arranque: un mundo que no es múltiplo del chunk no arranca (`FAIL_FAST`). Con la configuración válida, la partición es aritmética y no hay una aserción en el emisor de deltas que la vigile.

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
