# Pruebas unitarias

Catálogo de los tests de dominio puro del Game Server: qué se verifica sin ninguna infraestructura, con qué fixtures, y la lista enumerada de casos obligatorios por subsistema con sus valores numéricos exactos.

> **Estado real.** Buena parte de este nivel **ya existe y está en verde**: seis ficheros `_test.go`
> que se ejecutan con `pnpm run server:test`. Cada sección distingue con una etiqueta los casos
> **✔ implementados** (con su nombre de test real) de los **○ previstos**.
> Ubicación, nombres y convenciones vienen de [strategy.md](./strategy.md) §5.

---

## 1. Qué es un test unitario en este proyecto

Un test unitario aquí cumple **las cinco condiciones a la vez**:

1. No abre sockets, no toca PostgreSQL, no toca Redis. (Sí puede fijar variables de entorno con `t.Setenv`: es lo que hace `config_test.go`, y sigue siendo hermético porque `t.Setenv` las restaura al terminar.)
2. No consulta el reloj del sistema: recibe un `clock.Clock` inyectado, o el instante como argumento.
3. No usa aleatoriedad no inyectada: recibe un `RandomSource` explícito o una semilla declarada.
4. Termina en milisegundos y no depende del orden de ejecución respecto de otros tests.
5. Afirma un valor **exacto**, no un rango, salvo que la spec defina un rango.

Todo lo que no cumpla las cinco es otro nivel: [integration](./integration-tests.md), [contract](./contract-tests.md) o [simulation](./simulation-tests.md).

**Superficie cubierta por este nivel.**

| Paquete | Fichero real | Qué se prueba aquí |
|---|---|---|
| `internal/game/world` | `world_test.go` ✔ | Coordenadas, límites, conversión tile ↔ chunk, disposición fila-mayor del blob de chunk, costes de terreno, capa de ocupación, determinismo del generador. |
| `internal/pathfinding` | `astar_test.go` ✔ | A\* octile: corrección, límites de nodos y distancia, determinismo, regla anti corner-cutting, cancelación por contexto. |
| `internal/domain/movement` | `path_test.go` ✔ | Construcción de la polilínea temporizada, `ArrivalTimeMs`, función de posición autoritativa, `Validate`, ciclo de vida del movimiento. |
| `internal/domain/city` | `city_test.go` ✔ | Autómata de presencia, `ShouldEngageProtection` y sus bordes, límite de población. |
| `internal/auth` | `ticket_test.go` ✔ | Verificación del game ticket: caducidad, firma, `alg=none`, audiencia, claims, replay, fail-closed. |
| `internal/config` | `config_test.go` ✔ | Parseo de las `EO_*`, valores por defecto del canon §17 y validaciones cruzadas. |
| `internal/domain/unit` | ○ pendiente | Máquina de estados de unidad. Hoy las transiciones se ejercitan indirectamente desde [simulation-tests.md](./simulation-tests.md); un `unit_test.go` propio está previsto (§7.1). |

Lo que **no** se prueba aquí: SQL, serialización JSON del protocolo, avance real del loop, y por supuesto nada de red. En particular, **la validación del comando `unit.move` no vive en este nivel** (§8): se ejerce contra la simulación completa, porque el orden de validación es una propiedad del comando, no de una función pura.

---

## 2. `FakeClock` y `RandomSource` deterministas

**No viven en un paquete de test**: viven en `services/game-server/internal/clock`, que es **código de producción**. Esa es la decisión de diseño que hace posible todo lo demás: el `Clock` inyectado no es andamiaje para tests, es la única fuente de tiempo autorizada dentro del dominio (canon §1.5). El código real es éste:

```go
// internal/clock/clock.go
package clock

// Clock es la única fuente de tiempo autorizada dentro del dominio.
type Clock interface {
	// Now devuelve el instante actual.
	Now() time.Time
	// NowMs devuelve el instante actual en epoch milliseconds. Es la unidad
	// canónica de la simulación: toda aritmética temporal del juego usa enteros.
	NowMs() int64
}

// SystemClock es la implementación de producción: delega en el reloj del sistema.
type SystemClock struct{}

// FakeClock es un reloj controlado manualmente para tests deterministas.
// Es seguro para uso concurrente.
type FakeClock struct {
	mu sync.RWMutex
	ms int64
}

func NewFakeClock(startMs int64) *FakeClock
func (c *FakeClock) Now() time.Time
func (c *FakeClock) NowMs() int64
func (c *FakeClock) Advance(d time.Duration)   // adelanta una duración
func (c *FakeClock) AdvanceMs(ms int64)        // adelanta milisegundos
func (c *FakeClock) SetMs(ms int64)            // fija un instante absoluto
```

`FakeClock` lleva un `sync.RWMutex` porque el loop y la goroutine del test lo consultan desde hilos distintos y la suite corre con `-race`. `SetMs` existe para los tests de recuperación, que simulan "el servidor arranca en T".

`RandomSource` vive en `internal/clock/random.go` por el mismo motivo y con la misma disciplina.

Ejemplo de uso real, de `ticket_test.go` — el reloj hace en microsegundos lo que con el reloj del sistema costaría un minuto:

```go
func TestTicketCaducado(t *testing.T) {
	clk := clock.NewFakeClock(testEpoch)
	issuer := auth.NewIssuer(testSecret, 60*time.Second, clk)
	verifier := auth.NewVerifier(testSecret, clk)

	ticket, err := issuer.Issue(uuid.New())
	require.NoError(t, err)

	// Justo antes de caducar sigue valiendo.
	clk.Advance(59 * time.Second)
	_, err = verifier.Verify(ticket)
	require.NoError(t, err)

	// Pasado el minuto, no.
	clk.Advance(2 * time.Second)
	_, err = verifier.Verify(ticket)
	require.ErrorIs(t, err, auth.ErrExpiredTicket)
}
```

Y en el dominio puro ni siquiera hace falta un reloj: la aritmética de movimiento recibe el instante como **argumento**, que es aún más fuerte que inyectar un `Clock`.

```go
// path_test.go — la posición es una función pura de (polilínea, tiempo)
const start int64 = 1_757_376_000_000
m := movement.New(42, path, world.Tile{X: 2, Y: 1}, start)

require.Equal(t, start+1449, m.ArrivalTimeMs, "la llegada es el inicio más la duración total")
require.Equal(t, world.Tile{X: 1, Y: 0}, m.PositionAt(start+600))
```

**Regla asociada (R3 de [strategy.md](./strategy.md)).** Ni `time.Now()` ni `math/rand` aparecen bajo `internal/domain/**`, `internal/game/**` ni `internal/pathfinding`. La única manera de obtener el instante actual dentro del dominio es el `Clock` recibido por constructor, y `clock.SystemClock` sólo se construye en `cmd/server`.

---

## 3. Coordenadas y conversión tile ↔ chunk

Referencia: canon §4. Mundo MVP `512 × 512` (`EO_WORLD_WIDTH`, `EO_WORLD_HEIGHT`), chunk `32 × 32` (`EO_CHUNK_SIZE`), `chunksPerRow = 512/32 = 16`, total 256 chunks.

API real bajo prueba (`internal/game/world`), toda derivada de `chunkSize` y de las dimensiones del mundo, nunca hardcodeada:

```go
w.InBounds(x, y int32) bool
w.ChunkOf(x, y int32) (cx, cy int32)
w.ChunkIndex(cx, cy int32) int32        // cy * ChunksPerRow() + cx
w.ChunksPerRow() int32
w.ChunksPerColumn() int32
w.ChunkTerrain(cx, cy int32) ([]byte, error)   // copia de chunkSize*chunkSize bytes
w.ChunksInRadius(center world.Tile, radius int32) []world.ChunkCoord
w.TerrainAt(x, y int32) world.TerrainType
w.IsWalkable(x, y int32) bool
w.IsBlocked(x, y int32) bool
w.SetBlocked(minX, minY, maxX, maxY int32, blocked bool)
```

Con `EO_CHUNK_SIZE = 32` eso equivale a `chunkX = x >> 5`, `localX = x & 31` y `offset = localY*32 + localX` sobre un blob de 1024 bytes, pero el código no asume 32: `world.New` **rechaza** un chunk que no divida exactamente al mundo.

### 3.1 Casos ✔ implementados (`world_test.go`)

| # | Test real | Entrada | Salida esperada exacta |
|---|---|---|---|
| C1 | `TestNewRechazaDimensionesIncoherentes` | mundo 30×30 con chunk 32; terreno de longitud incorrecta; byte de terreno `99` | Error en los tres. El tercero con `"terreno inválido"` |
| C2 | `TestLimitesDelMundo` (`// INV-WORLD-001`) | `(0,0)`, `(63,63)`, `(-1,0)`, `(0,-1)`, `(64,0)`, `(0,64)` en un mundo 64×64 | Dentro los dos primeros, fuera los cuatro restantes |
| C3 | `TestFueraDeLimitesEsIntransitable` | `(-1,0)`, `(64,64)`, `(-1,-1)` | `TerrainAt = WATER`, `IsWalkable = false`, `IsBlocked = true`. **Los bordes del mundo se comportan como un muro, no provocan pánico** |
| C4 | `TestConversionTileChunk` (`// INV-WORLD-006`) | `(0,0)`, `(31,31)`, `(32,0)`, `(0,32)`, `(127,127)`, `(64,96)` en 128×128 | `chunk(0,0)`, `(0,0)`, `(1,0)`, `(0,1)`, `(3,3)`, `(2,3)`; `ChunksPerRow = 4`; `ChunkIndex(0,0)=0`, `(1,1)=5`, `(3,3)=15` |
| C5 | `TestCostesDeTerreno` | los seis terrenos | `CostUnits`: GRASSLAND 10, FOREST 16, HILL 18, ROAD 6; MOUNTAIN y WATER 0 y no transitables. `world.MinTerrainCostUnits == 6` |
| C6 | `TestCapaDeOcupacionNoMutaElTerreno` | `SetBlocked(9,9,11,11,true)` sobre un mundo de FOREST | `(10,10)` deja de ser transitable pero `TerrainAt(10,10)` sigue siendo `FOREST`; `(12,12)` intacto; desbloquear lo devuelve a su estado original |
| C7 | `TestChunkTerrainDevuelveUnaCopia` | `ChunkTerrain(1,1)` en 64×64 con chunk 32 | Exactamente `32*32 = 1024` bytes; mutar la copia **no** toca el mundo; `ChunkTerrain(99,99)` ⇒ `world.ErrOutOfBounds` |
| C8 | `TestChunkTerrainRespetaLaDisposicionFilaMayor` | marca `ROAD` en el tile global `(33,34)` | Aparece en `chunk[(34-32)*32 + (33-32)]`. Fija la disposición fila-mayor por escrito |
| C9 | `TestChunksEnRadioSeRecortanYVanOrdenados` | radio 1 desde `(64,64)`; radio 1 desde `(0,0)`; radio 2 dos veces | 9 chunks de `(1,1)` a `(3,3)`; **4** en la esquina (se recorta al mundo); orden idéntico entre llamadas |
| C10 | `TestGeneracionEsDeterminista` (`// INV-WORLD-005`) | `world.Generate(128,128,20260909)` dos veces, y una con `20260910` | Idénticos los dos primeros, distinto el tercero |
| C11 | `TestGeneracionProduceTerrenoValidoYVariado` | `world.Generate(256,256,20260909)` | `256*256` bytes, todos terrenos válidos; hay hierba y hay camino; la proporción transitable supera 0,5 |
| C12 | `TestAdyacenciaYDiagonal` | `(10,10)` frente a `(11,10)`, `(11,11)`, `(12,10)`, sí mismo | Adyacente / adyacente y diagonal / no adyacente / **no adyacente a sí mismo** |

### 3.2 Casos ○ previstos

| # | Test | Entrada | Salida esperada exacta |
|---|---|---|---|
| C13 | `TestConversionTileChunkEnElMundoDelMvp` | `(100,100)` y `(511,511)` en el mundo MVP 512×512 (`ChunksPerRow = 16`) | `chunk(3,3)`, `ChunkIndex = 51`, local `(4,4)`, offset `132`; y `chunk(15,15)`, `ChunkIndex = 255`, local `(31,31)`, offset `1023` |
| C14 | `TestParticionCompletaDelMundo` | los 262 144 tiles del mundo MVP | Cada tile produce exactamente un `ChunkIndex`; la unión de los 256 conjuntos es una partición sin solapes. Caro en apariencia y barato de verdad: son microsegundos de aritmética entera, y verifica la partición completa en vez de una muestra |
| C15 | `TestChunkIndexDependeDeLaConfiguracion` | mundo 256×256 con chunk 32 ⇒ `ChunksPerRow = 8` | `(32,32)` ⇒ `ChunkIndex = 1*8+1 = 9`. Verifica que `ChunksPerRow` se deriva del mundo y no está hardcodeado |

### 3.3 Proyección isométrica: se prueba en el cliente, y el servidor prueba que NO la tiene

El canon §4 es explícito: el servidor **nunca** maneja píxeles. La verificación es doble, y **ninguna de las dos mitades existe todavía**:

**○ En `apps/web` (Vitest).** `apps/` está vacío: el frontend Next.js no existe aún, así que estos casos se activan con el milestone que lo cree. Fórmula: `screenX = (x - y) * (TILE_W/2)`, `screenY = (x + y) * (TILE_H/2)`, con `TILE_W = 64` y `TILE_H = 32`.

| # | Entrada `(x,y)` | `(screenX, screenY)` esperado |
|---|---|---|
| C16 | `(0,0)` | `(0, 0)` |
| C17 | `(1,0)` | `(32, 16)` |
| C18 | `(0,1)` | `(-32, 16)` |
| C19 | `(1,1)` | `(0, 32)` |
| C20 | `(100,100)` | `(0, 3200)` |
| C21 | Ida y vuelta `screenToTile(tileToScreen(t)) == t` para 1000 tiles | Identidad exacta |

**○ En el servidor:** `TestSinConstantesDePixeles` recorre el árbol `internal/` y falla si aparecen los identificadores `TILE_W`, `TILE_H`, `screenX` o `screenY`. Es un test de arquitectura, barato, y evita que la proyección se filtre al lado autoritativo. Hoy la propiedad se cumple —no hay ni una constante de píxeles en el módulo Go— pero no está verificada por ningún test.

---

## 4. Pathfinding A\*

Referencia: canon §8 y [../architecture/pathfinding.md](../architecture/pathfinding.md). Parámetros **reales** de la implementación:

- Vecindad de 8 direcciones, **prohibido el corner cutting**: una diagonal sólo existe si los dos ortogonales adyacentes son transitables.
- Costes internos escalados en enteros: `costScaleOrtho = 1000`, `costScaleDiag = 1414`. El coste de un paso es `terrainCostUnits * escala`, imputado **al tile destino del paso**.
- **Heurística octile ponderada por `world.MinTerrainCostUnits`, que vale 6 (`ROAD`)**, no 10: `h = 6*1000*rectos + 6*1414*diagonales`. Ponderar con el coste de la hierba haría la heurística **inadmisible** en un mundo con caminos más baratos, y A\* dejaría de garantizar la ruta óptima.
- Desempate determinista del heap por `(f, h, y, x)`.
- Constructor: `pathfinding.NewAStar(maxNodes int, maxDistance int32)`; consulta: `FindPath(ctx context.Context, grid Grid, from, to world.Tile, opts Options) ([]world.Tile, error)`, donde las `Options` a cero significan "usa los del constructor". El coste mínimo del mundo lo aporta el propio grid, con `MinMoveCostUnits()`.
- Errores exportados: `ErrPathNotFound`, `ErrTargetOutOfBounds`, `ErrTargetNotWalkable`, `ErrOriginNotWalkable`, `ErrPathTooLong`.
- **La ruta devuelta incluye el tile de origen** como primer elemento, y `from == to` devuelve una ruta de un solo tile **sin error**.
- Orden real de las comprobaciones previas dentro de `FindPath`, que es el que fijan P3, P4, P10 y P13: destino en límites → origen en límites → **origen transitable** → destino transitable → distancia Chebyshev → `from == to`. Un origen no transitable con destino también no transitable devuelve `ErrOriginNotWalkable`, no `ErrTargetNotWalkable`.

Todos los tests usan mapas ASCII construidos con el helper local `buildWorld` (leyenda en [strategy.md](./strategy.md) §6.2).

### 4.1 Casos ✔ implementados (`astar_test.go`)

Los tests comparan la **ruta** y el **error**, nunca un coste interno: `FindPath` devuelve `([]world.Tile, error)` y el coste escalado es un detalle del algoritmo, no del contrato.

| # | Test real | Escenario | Resultado esperado exacto |
|---|---|---|---|
| **P1** | `TestRutaTrivialEnLineaRecta` | 6×3 GRASSLAND, `(0,1) → (5,1)` | `path[0] == (0,1)` —**la ruta empieza siempre en el origen**—, `path[last] == (5,1)`, `len(path) == 6` |
| **P2** | `TestOrigenIgualADestino` | `(1,1) → (1,1)` | `len(path) == 1`, `path[0] == (1,1)`, **sin error**. Es el pathfinder quien devuelve el camino trivial; el rechazo de "destino = posición actual" ocurre antes, en la validación del comando (§8) |
| **P3** | `TestDestinoIntransitableSeRechaza` | Destino sobre `MOUNTAIN` | `pathfinding.ErrTargetNotWalkable`. No se inicia la búsqueda |
| **P4** | `TestDestinoFueraDeLimites` | Destino `(99,0)` en un mundo 3×3 | `pathfinding.ErrTargetOutOfBounds` |
| **P5** | `TestSinRutaPosible` | Muro de `MOUNTAIN` que parte el mundo en dos | `pathfinding.ErrPathNotFound` |
| **P6** | `TestRodeaElObstaculo` | `MOUNTAIN` en `(2,1)`, `(0,1) → (4,1)` | Ningún waypoint es `(2,1)`; **todos** los waypoints son transitables; **todos** los pares consecutivos son adyacentes en 8-vecindad |
| **P7** | `TestPrefiereElCaminoAlBosque` | Fila 0 de `ROAD` sobre dos filas de `FOREST`, `(0,1) → (4,1)` | La ruta **sube al camino**: con `FOREST` a 16 y `ROAD` a 6, el rodeo por camino es más barato que la línea recta por bosque |
| **P8** | `TestProhibidoAtajarEsquinas` | Ver mapa en §4.3 | Toda diagonal de la ruta cumple `IsWalkable(cur.X, prev.Y) && IsWalkable(prev.X, cur.Y)`. La diagonal `(0,0)→(1,1)` entre dos montañas **no existe** |
| **P9** | `TestLimiteDeNodosSeAplica` | Mundo abierto 40×40, `Options{MaxNodes: 3}` | `pathfinding.ErrPathTooLong`: la búsqueda **aborta**, no completa y luego comprueba |
| **P10** | `TestLimiteDeDistanciaSeAplica` | Mundo 32×8, `(0,0) → (31,0)`, `Options{MaxDistance: 10}` | `pathfinding.ErrPathTooLong`. La distancia se mide en **tiles con la métrica Chebyshev** (`max(abs(dx), abs(dy))`) y se compara **antes** de iniciar la búsqueda |
| **P11** | `TestRutaEsDeterminista` | La misma consulta 21 veces sobre el mismo mundo con obstáculos | Ruta idéntica tile a tile en las 21 ejecuciones |
| **P12** | `TestConsultasSucesivasNoSeContaminan` | Consulta A, consulta B distinta, consulta A otra vez | `A == A'` y `A != B`: el espacio de trabajo reutilizado entre consultas no queda contaminado por la anterior |
| **P13** | `TestOrigenBloqueadoSeDetecta` | Origen sobre `MOUNTAIN` | `pathfinding.ErrOriginNotWalkable`, distinto de `ErrTargetNotWalkable` |
| **P14** | `TestConstruccionesBloqueanLaRuta` | `SetBlocked` sobre la columna central | `pathfinding.ErrPathNotFound`: **la capa de ocupación corta el paso igual que la montaña**, sin haber tocado el terreno |
| **P15** | `TestCancelacionPorContexto` | Mundo 200×200, `ctx` cancelado antes de llamar | `context.Canceled`, sin panic y sin estado residual en el espacio de trabajo reutilizable |

**Sobre P10 (importante).** El límite `EO_PATHFINDING_MAX_DISTANCE = 256` cuenta **tiles**, no coste. La heurística octile del código está escalada (`h = minCost*1000*rectos + minCost*1414*diagonales`, con `minCost = MinTerrainCostUnits = 6`), de modo que un solo paso ortogonal ya vale `6 × 1000 = 6000`. Compararla contra `256` rechazaría **cualquier** destino que no fuese el tile de origen. La métrica correcta, y la que usa el código, es Chebyshev sobre tiles:

```go
// internal/pathfinding/astar.go
if chebyshev(from, to) > maxDistance {
    return nil, fmt.Errorf("%w: distancia %d > %d", ErrPathTooLong, chebyshev(from, to), maxDistance)
}
```

**P11 y P12 son la defensa contra la regla R4** (canon §8: prohibido iterar mapas de Go sin ordenar). Un `map` recorrido en el bucle de vecinos pasa P1–P10 sin problemas y falla P11 de forma intermitente, que es exactamente el bug que hay que impedir que entre.

### 4.2 Casos ○ previstos

| # | Test | Escenario | Resultado esperado exacto |
|---|---|---|---|
| **P16** | `TestDestinoSobreAgua` | Destino sobre `WATER` | `ErrTargetNotWalkable`, igual que sobre `MOUNTAIN`. Hoy sólo se cubre la variante `MOUNTAIN` |
| **P17** | `TestEsquinaBloqueadaPorAmbosLadosEsInalcanzable` | `(0,0) → (1,1)` con `(1,0)` y `(0,1)` = `MOUNTAIN` | `ErrPathNotFound` |
| **P18** | `TestDesempateEsEstablePorFHYX` | Mundo abierto donde existen ≥ 2 rutas de coste idéntico entre `(0,0)` y `(3,3)` | La ruta devuelta es exactamente la que dicta el desempate `(f, h, y, x)`, enumerada tile a tile en el test |
| **P19** | `TestRutaNuncaAtraviesaTileNoTransitable` | 200 consultas generadas sobre mapas con obstáculos | Ningún waypoint cae en un tile no transitable, ninguna diagonal ataja una esquina, y ningún par consecutivo repite tile. Generaliza P6 y P8 |

### 4.3 Mapa del caso P8 (prohibición de corner cutting)

Es el mapa literal del test:

```
       x=0   1   2                Paso (0,0) -> (1,1):
 y=0    S   .   #                   requiere IsWalkable(1,0) AND IsWalkable(0,1)
 y=1    .   #   .                   (1,1) es MOUNTAIN => esa diagonal no existe
 y=2    .   .   G
```

`S = (0,0)`, `G = (2,2)`, con `MOUNTAIN` en `(2,0)` y `(1,1)`. Sin la regla anti corner-cutting una unidad podría "colarse" entre dos muros que se tocan en diagonal, atravesando una pared que visualmente está cerrada.

### 4.4 Mapa del caso P7 (preferencia por ROAD)

```
       x=0   1   2   3   4
 y=0    =   =   =   =   =        ROAD, costUnits 6
 y=1    f   f   f   f   f        FOREST, costUnits 16   (S = (0,1), G = (4,1))
 y=2    f   f   f   f   f
```

La línea recta por `FOREST` cuesta 4 pasos × 16 = 64 unidades escaladas ×1000; subir al camino, recorrerlo y bajar cuesta dos diagonales (6×1414 al entrar en `ROAD` y 16×1414 al volver al bosque) más pasos de camino a 6×1000. El test **no** asserta un coste: asserta que la ruta **pasa por al menos un tile `ROAD`**, que es la propiedad observable y la que no se rompe si mañana se afina el escalado. La imputación del coste al tile destino del paso no se verifica aquí, sino en el nivel de la polilínea (§5.3, W1: el segmento hacia `(3,1)` cuesta lo que cuesta `FOREST`).

---

## 5. Construcción de la polilínea temporizada

Referencia: canon §7 y [../specs/movement.md](../specs/movement.md). La aritmética es **entera y en punto fijo**, no en coma flotante. Ésta es la función real:

```go
// internal/domain/movement/path.go
const sqrt2Num, sqrt2Den = 1414214, 1000000   // √2 en punto fijo
// world.CostBase = 10

func StepDurationMs(baseMsPerTile int64, costUnits int32, diagonal bool) int64 {
    ms := (baseMsPerTile*int64(costUnits) + 5) / 10          // redondeo al ms más cercano
    if diagonal { ms = (ms*1414214 + 500000) / 1000000 }     // redondeo al ms más cercano
    if ms < 1 { ms = 1 }
    return ms
}
```

```
tMs[0]        = 0                        // el primer waypoint es el origen
tMs[i]        = tMs[i-1] + StepDurationMs(...)   // acumulado sobre segmentos YA redondeados
arrivalTimeMs = startTimeMs + tMs[last]
```

`baseMsPerTile` es propiedad del tipo de unidad: **`VILLAGER` = 600 ms** (canon §10). `costUnits` es el del **tile destino** del paso (§3.1, C5).

Dos decisiones que los tests fijan por escrito:

1. **Se redondea cada segmento al milisegundo más cercano y sólo después se acumula.** Truncar en vez de redondear regalaría casi un segundo de ventaja cada mil pasos.
2. **Ningún paso dura 0 ms.** El `if ms < 1 { ms = 1 }` garantiza que los `tMs` son estrictamente crecientes: un `tMs` repetido rompería la monotonía de la polilínea y con ella la búsqueda binaria de `PositionAt`.

### 5.1 Tabla de referencia de coste por segmento (VILLAGER, 600 ms/tile)

| Terreno destino | `costUnits` | ortogonal (ms) | diagonal (ms) |
|---|---|---|---|
| `GRASSLAND` | 10 | **600** | **849** |
| `FOREST` | 16 | **960** | **1358** |
| `HILL` | 18 | **1080** | **1527** |
| `ROAD` | 6 | **360** | **509** |
| `MOUNTAIN`, `WATER` | — | no transitable | no transitable |

### 5.2 Casos ✔ implementados (`path_test.go`)

| # | Test real | Entrada | Resultado esperado exacto |
|---|---|---|---|
| **W1** | `TestEjemploNumericoCanonico` | El ejemplo canónico del proyecto, §5.3 | `path = [{0,0,0}, {1,0,600}, {2,1,1449}, {3,1,2409}, {4,1,2769}]`; `DurationMs() == 2769`; `Origin() == (0,0)`; `Destination() == (4,1)` |
| **W2** | `TestDuracionDeUnPaso` | Siete de las ocho combinaciones de la tabla de §5.1, table-driven: hierba ortogonal y diagonal, bosque ortogonal y diagonal, colina ortogonal, camino ortogonal y diagonal | `600 / 849 / 960 / 1358 / 1080 / 360 / 509`. Falta la diagonal de `HILL` (1527), cubierta por la tabla de §5.1 pero no por un caso propio |
| **W3** | `TestNingunPasoEsInstantaneo` | `StepDurationMs(1, 1, false)` | **1**, no 0. Con velocidad y coste mínimos el paso sigue durando un milisegundo |
| **W4** | `TestBuildTimedPathRechazaEntradasInvalidas` | ruta vacía; velocidad 0; salto no contiguo `(0,0)→(4,1)` | `movement.ErrEmptyPath`; error; `movement.ErrPathNotContiguous` |
| **W5** | `TestValidateComprobaciones` | polilínea correcta; vacía; origen con `tMs != 0`; tiempos no crecientes; waypoints no contiguos; tile bloqueado **después** de calcular la ruta | `nil`; `ErrEmptyPath`; `ErrInvalidOrigin`; `ErrPathNotMonotonic`; `ErrPathNotContiguous`; `ErrPathNotWalkable` |
| **W6** | `TestMovementCicloDeVida` | `movement.New(42, path, target, start)` | `Status == ACTIVE`; `ArrivalTimeMs == start + 1449`; `HasArrived` falso en `start+1448` y cierto en `start+1449` y mucho después; `RemainingMs(start) == 1449` y **nunca negativo**; `Destination() == (2,1)` |
| **W7** | `TestEstadosDeMovimiento` | `ACTIVE`, `COMPLETED`, `CANCELLED`, `FAILED`, `"PAUSED"` | Los cuatro primeros válidos, el quinto no. El dominio de `Status` es cerrado |

W5 merece énfasis. Su último subcaso —bloquear un tile de una polilínea **ya calculada** y comprobar que `Validate` lo detecta— es el que sostiene la recuperación tras crash: si el mapa cambia bajo un movimiento persistido, se falla el movimiento en vez de teletransportar a la unidad.

### 5.3 Verificación numérica de W1, paso a paso

Éste es **el ejemplo numérico canónico del proyecto** y aparece igual en toda la documentación. El mundo es 5×2 con `FOREST` en `(3,1)` y `ROAD` en `(4,1)`, todo lo demás `GRASSLAND`:

```
(0,0) -> (1,0)  GRASSLAND ortogonal   600 * 10/10        =  600   acc    0 -> 600
(1,0) -> (2,1)  GRASSLAND diagonal    600 * 10/10 * √2   =  849   acc  600 -> 1449
(2,1) -> (3,1)  FOREST    ortogonal   600 * 16/10        =  960   acc 1449 -> 2409
(3,1) -> (4,1)  ROAD      ortogonal   600 *  6/10        =  360   acc 2409 -> 2769

path = [ {x:0,y:0,tMs:0}, {x:1,y:0,tMs:600}, {x:2,y:1,tMs:1449},
         {x:3,y:1,tMs:2409}, {x:4,y:1,tMs:2769} ]
arrival_time_ms = start_time_ms + 2769
```

Cubre en cuatro segmentos las tres cosas que pueden salir mal: el redondeo de la diagonal (849, no 848 ni 849,5), la imputación del coste al tile destino (el segmento a `(3,1)` cuesta lo que cuesta `FOREST`, no lo que costaba el tile de origen) y la acumulación sobre valores ya redondeados.

### 5.4 Casos ○ previstos

| # | Test | Entrada | Resultado esperado exacto |
|---|---|---|---|
| **W8** | `TestRedondeoPorSegmentoNoAlFinal` | 3 diagonales `GRASSLAND` seguidas | `[0, 849, 1698, 2547]`. Acumular en real y redondear una sola vez al final daría `2546`; el test distingue dos implementaciones que parecen equivalentes y no lo son |
| **W9** | `TestPolilineaDeUnSoloWaypoint` | `[(5,5)]` | `[0]`, `DurationMs() == 0`, `arrivalTimeMs == startTimeMs` |
| **W10** | `TestTiemposEstrictamenteCrecientes` | 200 polilíneas generadas sobre mapas variados | `tMs[i] > tMs[i-1]` para todo `i > 0`, y `arrivalTimeMs == startTimeMs + tMs[len-1]` exactamente. Generaliza W3 y W6 |

---

## 6. Función de posición autoritativa

Referencia: canon §7. **Posición en el instante `T` = último waypoint con `tMs <= (T - start_time_ms)`.** Se resuelve con búsqueda binaria y es una función **pura y total** de `(path, startTimeMs, T)`: no requiere replay de ticks y por tanto es verificable sin loop. Hay dos entradas al mismo cálculo: `path.PositionAt(elapsedMs)` sobre la polilínea, y `movement.PositionAt(absoluteMs)` sobre el movimiento completo.

### 6.1 Casos ✔ implementados (`TestPositionAt`, table-driven)

Sobre la polilínea canónica de §5.3, con `elapsed` medido desde el inicio del movimiento:

```
tMs:    0      600     1449    2409    2769
tile: (0,0)   (1,0)   (2,1)   (3,1)   (4,1)
```

| # | `elapsed` | Posición esperada | Por qué |
|---|---|---|---|
| **A1** | `-1000` | `(0,0)` | Antes de empezar, en el origen. Ningún `tMs <= -1000`; el dominio **no** extrapola hacia atrás |
| **A2** | `0` | `(0,0)` | En el instante cero, en el origen |
| **A3** | `1` | `(0,0)` | Aún no ha alcanzado el siguiente tile |
| **A4** | `599` | `(0,0)` | Un milisegundo antes de llegar, sigue en el origen |
| **A5** | `600` | `(1,0)` | **En el instante exacto del waypoint, ya está en él**: el límite es inclusivo (`tMs <= elapsed`) |
| **A6** | `1448` | `(1,0)` | Entre waypoints se ocupa el último alcanzado |
| **A7** | `1449` | `(2,1)` | Waypoint diagonal alcanzado |
| **A8** | `2408` / `2409` | `(2,1)` / `(3,1)` | El par que fija el borde del tercer segmento |
| **A9** | `2769` | `(4,1)` | Llegada exacta; coincide con `arrivalTimeMs - startTimeMs` |
| **A10** | `999_999` | `(4,1)` | Después de llegar se permanece en el destino. Nunca se extrapola más allá; la función es total y no devuelve error |

A3, A4 y A6 son la frontera entre servidor y cliente: si un test esperase `(0.5, 0)` en `elapsed = 300`, el diseño se habría roto. **La interpolación sub-tile es exclusivamente visual y vive en el cliente** (canon §7).

`TestIndexAt` verifica lo mismo un nivel más abajo, sobre el índice del waypoint: `IndexAt(-5) == 0`, `IndexAt(0) == 0`, `IndexAt(599) == 0`, `IndexAt(600) == 1`, `IndexAt(1449) == 2`, `IndexAt(50_000) == 2` sobre una polilínea de tres waypoints. Es el que fija que la búsqueda binaria satura por ambos extremos en vez de salirse del array.

### 6.2 Casos ○ previstos

| # | Test | Escenario | Resultado esperado |
|---|---|---|---|
| **A11** | `TestPositionAtEnPolilineaDeUnSoloWaypoint` | Cualquier `elapsed` sobre `[(5,5)]` | Siempre el único waypoint |
| **A12** | `TestIndiceMonotonoEnBarrido` | Barrido de `-100` a `2900` de 1 en 1 ms | El índice devuelto es monótono no decreciente; nunca retrocede. Unas 3 000 evaluaciones, microsegundos |
| **A13** | `TestPosicionDerivadaNuncaAdelantaAlReloj` | 50 instantes de la ventana del movimiento | La posición derivada nunca corresponde a un `tMs > elapsed`. Es la propiedad que impide "adelantar" a la unidad |
| **A14** | `TestBusquedaBinariaCoincideConBarridoLineal` | 200 polilíneas generadas, evaluadas en `tMs-1`, `tMs` y `tMs+1` de cada segmento | El resultado de la búsqueda binaria coincide con el de un barrido lineal ingenuo. Protege la optimización |

### 6.3 Cancelación

La regla —**cancelar deja la unidad en el último tile alcanzado, jamás entre dos**— se deriva directamente de `PositionAt`, y hoy se verifica **de extremo a extremo en el nivel `simulation`**, no aquí: `TestCancelacionExplicita` cancela a los 1800 ms y comprueba `stoppedAt == (15,10)`, y `TestNuevaOrdenReemplazaLaAnterior` comprueba que la orden nueva parte de `(14,10)`, el tile realmente alcanzado. Ver [simulation-tests.md](./simulation-tests.md) §4.1 y §4.2.

Lo que sí falta en este nivel es la variante puramente funcional, ○ prevista:

| # | Test | Escenario | Resultado esperado |
|---|---|---|---|
| **A15** | `TestCancelarSeAjustaAlUltimoTileAlcanzado` | Cancelar en `elapsed = 1000` sobre la polilínea de §5.3 | `(1,0)`, el último waypoint alcanzado: ni el siguiente ni una posición intermedia |
| **A16** | `TestCancelarSobreWaypointExactoSeQuedaAhi` | Cancelar en `elapsed = 1449` | `(2,1)` |
| **A17** | `TestCancelarSinProgresoSeQuedaEnElOrigen` | Cancelar en `elapsed = 10` | `(0,0)` |
| **A18** | `TestCancelarTrasLaLlegadaNoHaceNada` | Cancelar en `elapsed = 5000` | `(4,1)`; el movimiento ya es terminal y no vuelve a `ACTIVE` |

---

## 7. Transiciones de estado

### 7.1 Estado de unidad — ○ pendiente

Referencia: [../specs/unit.md](../specs/unit.md) §7 y canon §10. Estados reales del dominio (`internal/domain/unit`): `IDLE`, `MOVING`, `GARRISONED`, `HIDDEN`, `DEAD`.

**No existe todavía `internal/domain/unit/unit_test.go`.** Hoy las transiciones se ejercitan indirectamente desde el nivel `simulation` (`TestVerticalSliceMovimiento` recorre `IDLE → MOVING → IDLE`; `TestRechazosDeMovimiento` cubre los rechazos desde `DEAD` y `GARRISONED`). Eso cubre los caminos que el MVP recorre, pero no la matriz completa, que es donde aparece la transición que nadie pensó en prohibir.

El test previsto es **table-driven sobre la matriz completa 5×5**: 25 celdas, cada una con su veredicto explícito.

| # | Test | Verifica |
|---|---|---|
| **U1** | `TestTransicionesPermitidas` | `IDLE→MOVING`, `HIDDEN→MOVING`, `MOVING→IDLE`, `IDLE→GARRISONED`, `GARRISONED→IDLE`, `IDLE→HIDDEN`, `HIDDEN→IDLE` se aceptan |
| **U2** | `TestTransicionesProhibidas` | `MOVING→GARRISONED` ⇒ `UNIT_NOT_MOVABLE`, `GARRISONED→MOVING` ⇒ `UNIT_GARRISONED`, `MOVING→HIDDEN`, `GARRISONED→HIDDEN`, `HIDDEN→GARRISONED`, `DEAD→*` ⇒ `UNIT_DEAD` |
| **U3** | `TestUnidadMuertaRechazaOrdenes` | Toda acción sobre una unidad `DEAD` devuelve `UNIT_DEAD` y **no muta nada**. Hoy cubierto parcialmente por `TestRechazosDeMovimiento` en `simulation` |
| **U4** | `TestSegundaOrdenPasaPorIdle` | Una segunda orden sobre una unidad `MOVING` no salta de `MOVING` a `MOVING`: cancela el movimiento anterior y vuelve a entrar en `MOVING`. Hoy cubierto de extremo a extremo por `TestNuevaOrdenReemplazaLaAnterior` |
| **U5** | `TestLaMatrizDeTransicionesEsExhaustiva` | Las 25 celdas tienen veredicto declarado. Añadir un estado nuevo sin actualizar la tabla rompe este test |
| **U6** | `TestMovingEquivaleAMovimientoActivo` | `status == MOVING` ⟺ el agregado tiene un movimiento `ACTIVE`. La contraparte en base de datos la verifica [integration](./integration-tests.md) |

**Nota de scope.** `DEAD` **no tiene productor en MVP**: el combate está fuera de scope (canon §21). Los tests que llevan una unidad a `DEAD` la construyen directamente en ese estado, como hace hoy `TestRechazosDeMovimiento`; no existe un comando que mate unidades.

### 7.2 Estado de presencia de ciudad — ✔ implementado (`city_test.go`)

Referencia: canon §9 y [../specs/presence.md](../specs/presence.md). Estados: `ONLINE`, `OFFLINE_PENDING`, `PROTECTED`.

```
ONLINE --disconnect(+grace)--> OFFLINE_PENDING --cooldown--> PROTECTED
PROTECTED --connect--> ONLINE ;  OFFLINE_PENDING --connect--> ONLINE
```

La API real es de **funciones puras**, sin reloj inyectado: reciben el instante como argumento.

```go
city.CanTransition(from, to PresenceState) bool
city.NextStateOnDisconnect(current PresenceState) PresenceState
city.ShouldEngageProtection(state PresenceState, offlineAt *time.Time, cooldown time.Duration, now time.Time) bool
(*city.City).HasPopulationRoom(n int32) bool
(*city.City).IsProtected() bool
```

| # | Test real | Escenario | Resultado esperado |
|---|---|---|---|
| **S1** | `TestAutomataDePresencia` (`// INV-CITY-004`) | Las 4 transiciones permitidas y 4 prohibidas | Permitidas: `ONLINE→OFFLINE_PENDING`, `OFFLINE_PENDING→PROTECTED`, `OFFLINE_PENDING→ONLINE`, `PROTECTED→ONLINE`. Prohibidas: `ONLINE→PROTECTED` (sin cumplir el cooldown), `PROTECTED→OFFLINE_PENDING` (la protección no retrocede) y cualquier estado desconocido, **en origen o en destino** |
| **S2** | `TestTransicionAlMismoEstadoEsIdempotente` | `s → s` para los tres estados | Permitida. Reaplicar el estado actual no es un error: la reconexión puede llegar dos veces y no debe registrarse como violación |
| **S3** | `TestNextStateOnDisconnect` | Desconectar desde cada estado | `ONLINE → OFFLINE_PENDING`; `OFFLINE_PENDING → OFFLINE_PENDING`; `PROTECTED → PROTECTED`. Desconectarse otra vez estando ya offline no cambia nada |
| **S4** | `TestShouldEngageProtection` | Seis subcasos con cooldown de 300 s | No aplica si la ciudad está `ONLINE`; no aplica sin marca de desconexión (`offlineAt == nil`); **falso a 299 s**; **cierto a exactamente 300 s** (el límite es inclusivo); cierto 24 h después; y con cooldown 0 protege de inmediato |
| **S5** | `TestEstadosDePresenciaValidos` | `ONLINE`, `OFFLINE_PENDING`, `PROTECTED`, `"INVISIBLE"` | Los tres primeros válidos, el cuarto no. **El dominio de `presence_state` es cerrado y se rechaza antes de llegar al `CHECK` de la base de datos** |
| **S6** | `TestIsProtected` | Una ciudad en cada estado | Sólo `PROTECTED` está protegida |

S4 es la razón de existir del tiempo como valor: con reloj real, comprobar los bordes de 299 s y 300 s costaría diez minutos y sería *flaky*. Aquí es aritmética de `time.Time` y cuesta microsegundos.

**Nota sobre el ID de S1.** El comentario que hoy encabeza `TestAutomataDePresencia` en el árbol dice `// INV-CITY-004`, pero por el enunciado del registro le corresponde **`INV-CITY-005`** («Solo transiciones permitidas de `presence_state`»); `INV-CITY-004` es «El dominio de `presence_state` es cerrado», que es lo que verifica S5. Ambos invariantes están cubiertos —cada uno por su test—, sólo está mal la etiqueta del comentario. Corregirla es un cambio de una línea en `city_test.go`, y hasta que se haga este documento describe lo que el árbol dice, no lo que debería decir.

### 7.3 Casos ○ previstos de presencia

| # | Test | Escenario | Resultado esperado |
|---|---|---|---|
| **S7** | `TestProteccionIndefinidaMientrasSigaOffline` | Entrar en `PROTECTED` | `ProtectionUntil == nil`: en MVP la protección no caduca sola mientras el jugador siga offline (canon §9). Hoy sólo se comprueba el reverso —que reconectar lo limpia— desde `simulation` |
| **S8** | `TestCiudadProtegidaRechazaAccionesProhibidas` | Acción prohibida sobre ciudad `PROTECTED` | `CITY_PROTECTED`, sin mutación |

El ciclo completo en el tiempo (desconexión → margen de 30 s → `OFFLINE_PENDING` → cooldown de 300 s → `PROTECTED` → reconexión) **sí está verificado hoy**, pero en el nivel `simulation`, porque es el game loop quien decide la transición: ver `TestCicloDePresenciaYProteccion` en [simulation-tests.md](./simulation-tests.md) §4.4.

---

## 8. Validación de comandos y códigos de error

Referencia: canon §16, [../specs/unit.md](../specs/unit.md) §8 y [../specs/websocket-protocol.md](../specs/websocket-protocol.md) §7.4. El orden de validación es **estricto** y forma parte del contrato: un test que espera `UNIT_DEAD` para una unidad ajena y muerta está mal escrito, porque `UNIT_NOT_OWNED` se evalúa antes.

**Dónde vive esto realmente.** La validación de `unit.move` no es una función pura del dominio: necesita el estado del mundo, el pathfinder y el emisor de mensajes. Por eso se verifica **contra la simulación completa**, en `internal/game/simulation/simulation_test.go`, y no en este nivel. Los casos ✔ implementados están catalogados en [simulation-tests.md](./simulation-tests.md) §4.3; aquí se listan sólo para que el mapa de cobertura sea completo.

### 8.1 `unit.move`: estado de las validaciones

| # | Estado | Preparación | `code` esperado | Dónde |
|---|---|---|---|---|
| **E1** | ✔ | `unitId` inexistente (`9999`) | `UNIT_NOT_FOUND` | `TestRechazosDeMovimiento/unidad inexistente` |
| **E2** | ✔ | Unidad existente de otro `playerId` | `UNIT_NOT_OWNED` | `TestRechazosDeMovimiento/unidad ajena` |
| **E3** | ✔ | Unidad propia en `DEAD` | `UNIT_DEAD` | `TestRechazosDeMovimiento/unidad muerta` |
| **E4** | ✔ | Unidad propia en `GARRISONED` | `UNIT_GARRISONED` | `TestRechazosDeMovimiento/unidad guarnecida` |
| **E5** | ○ | Estado que no admite la acción (`HIDDEN`) | `UNIT_NOT_MOVABLE` | pendiente |
| **E6** | ✔ | `target = (500,500)` en un mundo 64×64 | `TARGET_OUT_OF_BOUNDS` | `TestRechazosDeMovimiento/destino fuera del mundo` |
| **E7** | ✔ (parcial) | `target` = posición actual de la unidad | Hoy **se acepta** sin crear polilínea: `unit.move.accepted` y **ningún** `unit.movement.started`, «no se crea una polilínea degenerada de un solo punto» | `TestMoverseAlSitioDondeYaEstas` |
| **E8** | ✔ | `target` sobre un tile del *blocked overlay* | `TARGET_NOT_WALKABLE` | `TestRechazosDeMovimiento/destino intransitable` |
| **E9** | ✔ | Destino transitable pero amurallado por completo | `PATH_NOT_FOUND` | `TestSinRutaPosibleSeRechaza` |
| **E10** | ○ | Distancia Chebyshev > `EO_PATHFINDING_MAX_DISTANCE`; y variante que agota `EO_PATHFINDING_MAX_NODES` | `PATH_TOO_LONG` | pendiente en `simulation`; el pathfinder por su cuenta sí lo cubre (§4.1, P9 y P10) |

**Sobre E7.** El comportamiento implementado es aceptar y no generar movimiento, no rechazar con `INVALID_TARGET`. Es una decisión deliberada —una orden redundante no es un error del jugador— y así está fijada por test. Si la spec exige `INVALID_TARGET`, es el código el que debe cambiar y este documento con él, pero hoy manda lo implementado.

### 8.2 Orden de precedencia — ○ pendiente

| # | Test | Escenario | Esperado |
|---|---|---|---|
| **E11** | `TestElOrdenDeValidacionEsEstricto` | Unidad **ajena** y **muerta**, con destino **fuera de límites** | `UNIT_NOT_OWNED`: gana la comprobación de propiedad, no la de estado ni la de destino |
| **E12** | `TestMuertaGanaAGuarnecida` | Unidad propia `DEAD` marcada además como guarnecida | `UNIT_DEAD` |
| **E13** | `TestFueraDeLimitesGanaANoTransitable` | `target` fuera del mundo | `TARGET_OUT_OF_BOUNDS`, nunca `TARGET_NOT_WALKABLE` |

### 8.3 Ausencia de efectos secundarios

Requisito explícito de la Definition of Done (canon §19): *errores testeados*. No basta con el `code`.

| # | Estado | Verifica |
|---|---|---|
| **E14** | ✔ (parcial) | `TestRechazosDeMovimiento` comprueba, **para cada uno de sus seis subcasos**, que no se emitió ningún `unit.movement.started`: «un comando rechazado no puede producir ningún movimiento». Falta extender la aserción a `status`, `x` e `y` de la unidad |
| **E15** | ○ | `TestLaPropiedadSeCompruebaAntesDeBuscarRuta`: el path **no se calcula** cuando falla la propiedad, verificado con un `Pathfinder` doble que cuenta llamadas |
| **E16** | ○ | `TestElRechazoLlevaElRequestId`: el rechazo lleva el `requestId` original para que el cliente correlacione |
| **E17** | ✔ | `TestErrorDeProtocoloLlevaCodigoEstable` (en `internal/protocol/contract_test.go`): el error expone `Code` y `Details`, y la lógica de control nunca usa el texto (regla R9 de [strategy.md](./strategy.md)) |
| **E18** | ✔ | `TestCatalogoDeErroresCoincideConElExportado` (ídem): los **22** códigos del canon §16 de Go coinciden exactamente con los exportados por `packages/protocol`. Ver [contract-tests.md](./contract-tests.md) §5.1 |

### 8.4 `unit.cancel_move`

| # | Estado | Escenario | Esperado |
|---|---|---|---|
| **E19** | ✔ | Cancelar un movimiento en curso | `unit.movement.cancelled` con `reason: "CANCELLED_BY_PLAYER"` y `stoppedAt` en el último tile alcanzado; la unidad queda `IDLE` y **no avanza más**. `TestCancelacionExplicita` |
| **E20** | ○ | Unidad `IDLE`, sin movimiento activo | **No es error**: se responde con la última información conocida ([../specs/unit.md](../specs/unit.md) §7) |
| **E21** | ○ | Unidad ajena | `UNIT_NOT_OWNED` |
| **E22** | ○ | Unidad `DEAD` / `GARRISONED` | `UNIT_DEAD` / `UNIT_GARRISONED` |

El enum de `reason` implementado es **`REPLACED | CANCELLED_BY_PLAYER | PATH_BLOCKED | UNIT_DEAD | SERVER`** (`movement.CancelReason`). Un test que espere `SUPERSEDED` o `PLAYER_REQUEST` está escrito contra un contrato que no existe.

---

## 9. Población y límite por era

Referencia: canon §10. `population_limit = era.population_cap + modificadores de edificios`, **sin modificadores en MVP**. Las eras y sus topes viven en la tabla `eras`, no en código: el dominio recibe el catálogo cargado.

| Era | `ordinal` | `population_cap` |
|---|---|---|
| `STONE_AGE` | 1 | 20 |
| `BRONZE_AGE` | 2 | 50 |
| `IRON_AGE` | 3 | 100 |
| `CASTLE_AGE` | 4 | 150 |

Los topes se siembran en la migración `000002_seed_catalogs`, no en código.

| # | Estado | Test | Escenario | Esperado |
|---|---|---|---|---|
| **N1** | ✔ | `TestLimiteDePoblacion` (`// INV-CITY-002`) | `City{Population: 3, PopulationLimit: 20}` y `City{Population: 20, PopulationLimit: 20}` | `HasPopulationRoom(1)` y `(17)` ciertos, `(18)` falso; en la ciudad llena `(1)` falso y `(0)` **cierto**. El borde exacto queda fijado por escrito |
| **N2** | ○ | `TestLimiteDePoblacionDerivaDeLaEra` | Las cuatro eras | `population_limit` = 20 / 50 / 100 / 150; ningún literal `20` en el dominio: procede del catálogo cargado desde `eras` |
| **N3** | ○ | `TestCiudadInicialTieneTresAldeanos` | Ciudad recién creada | `population == 3` (canon §10: 1 `TOWN_CENTER` + 3 `VILLAGER`). El `TOWN_CENTER` es un *building*, **no** cuenta como población. Verificado hoy de forma indirecta por el harness de `simulation`, que crea exactamente 3 aldeanos con `Population: 3` |
| **N4** | ○ | `TestEnElLimiteSeRechazaLaUnidadNueva` | `population == 20` en `STONE_AGE` | `POPULATION_LIMIT_REACHED`, sin crear la unidad |
| **N5** | ○ | `TestPoblacionNuncaSuperaElLimite` | Barrido intentando crear 40 unidades en `STONE_AGE` desde `population = 3` | Se crean exactamente 17; las 23 restantes fallan con `POPULATION_LIMIT_REACHED`; `population` final = 20 |
| **N6** | ○ | `TestAvanzarDeEraSubeElLimiteSinTocarLaPoblacion` | `STONE_AGE (20/20)` → `BRONZE_AGE` | `population_limit` pasa a 50, `population` sigue 20; no se crean ni destruyen unidades |
| **N7** | ○ | `TestSinModificadoresDeEdificioEnMvp` | Cualquier configuración de edificios | El modificador agregado es 0. Marca explícita: los modificadores de edificios son **Fuera de MVP** |
| **N8** | ○ | `TestLaPoblacionBajaAlRetirarUnaUnidad` | Retirar una unidad | `population` baja en 1 y nunca por debajo de 0 |

---

## 10. Configuración

✔ Implementado en `config_test.go`. El helper `setValidEnv(t)` deja el entorno en el mínimo válido con `t.Setenv` —las tres obligatorias— y cada test cambia sólo lo que quiere ejercitar.

| # | Test real | Verifica |
|---|---|---|
| **G1** | `TestValoresPorDefectoDelCanon` | Con sólo las tres obligatorias puestas, los valores por defecto son exactamente los del canon §17: `EO_ENV=development`, `EO_HTTP_ADDR=":8080"`, `EO_METRICS_ADDR=":9090"`, `EO_TICK_RATE_HZ=10`, `EO_WORLD_WIDTH=512`, `EO_WORLD_HEIGHT=512`, `EO_CHUNK_SIZE=32`, `EO_WORLD_SEED=20260909`, `EO_INTEREST_RADIUS_CHUNKS=2`, `EO_PRESENCE_TTL_SECONDS=30`, `EO_PRESENCE_HEARTBEAT_SECONDS=10`, `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS=300`, `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS=50`, `EO_PATHFINDING_MAX_NODES=20000`, `EO_PATHFINDING_MAX_DISTANCE=256`, `EO_WS_MAX_MESSAGE_BYTES=16384`, `EO_WS_RATE_LIMIT_PER_SECOND=20`, `EO_WS_RATE_LIMIT_BURST=40`. Comprueba además los derivados: `TickDuration() == 100ms`, `TickDurationMs() == 100`, `IsProduction() == false` |
| **G2** | `TestObligatoriasAusentes` | Sin `EO_POSTGRES_URL`, `EO_REDIS_URL` ni `EO_AUTH_JWT_SECRET`, el error menciona **las tres a la vez**. Arreglar la configuración de una en una es una pérdida de tiempo en un despliegue |
| **G3** | `TestSecretoDemasiadoCorto` | Secreto de 5 caracteres ⇒ error `"al menos 32 caracteres"` |
| **G4** | `TestSecretoDeDesarrolloEnProduccion` | `EO_ENV=production` con un secreto que contiene `dev-only` ⇒ error `"valor de desarrollo en un entorno de producción"`. Es exactamente el valor que trae `.env.example` |
| **G5** | `TestTickDebeDividirAMil` / `TestTicksValidos` | `EO_TICK_RATE_HZ=3` ⇒ error `"debe dividir exactamente a 1000"`. Aceptados: 1, 2, 4, 5, 10, 20, 25, 50, 100. Un tick que no dura un número entero de milisegundos rompería la aritmética entera de la simulación |
| **G6** | `TestMundoDebeSerMultiploDelChunk` | `EO_WORLD_WIDTH=500` con `EO_CHUNK_SIZE=32` ⇒ error `"múltiplos exactos"`. Un chunk a medias no existe |
| **G7** | `TestLatidoDebeSerMenorQueElTTL` | `EO_PRESENCE_HEARTBEAT_SECONDS == EO_PRESENCE_TTL_SECONDS` ⇒ error `"estrictamente menor"`. Con el latido igual o más lento que el TTL, la presencia expiraría entre latidos y todos los jugadores parpadearían entre conectado y desconectado |
| **G8** | `TestBurstNoPuedeSerMenorQueLaTasa` | `EO_WS_RATE_LIMIT_BURST=5` con `EO_WS_RATE_LIMIT_PER_SECOND=20` ⇒ error `"no puede ser menor"` |
| **G9** | `TestValorFueraDeRango` / `TestValorNoNumerico` | `EO_TICK_RATE_HZ=0` ⇒ `"fuera del rango permitido"`; `EO_WORLD_SEED=no-es-un-numero` ⇒ `"no es un entero válido"`. **Error de arranque, nunca un valor silenciosamente corregido** |
| **G10** | `TestSobrescrituraPorEntorno` | Con `EO_ENV=production` y valores propios: `IsProduction()` cierto, `TickRateHz == 20`, `TickDuration() == 50ms`, cooldown 600 s, `WorldWidth == 256` |

○ Pendiente: `TestLaConfiguracionNuncaRegistraSecretos`, que verificaría que la representación textual de `Config` enmascara `EO_AUTH_JWT_SECRET` y las credenciales embebidas en `EO_POSTGRES_URL` y `EO_REDIS_URL` (`INV-SEC-006`).

Este bloque es el que hace cumplir la regla del canon §17: *ningún valor de gameplay se hardcodea en más de un lugar; todo pasa por `internal/config`*.

---

## 11. Ejecución

```powershell
pnpm run server:test      # go test ./... en services/game-server
pnpm run protocol:test    # vitest run en packages/protocol
```

Para reproducir lo que hace la CI, con detector de carreras y sin caché:

```powershell
cd services/game-server; go test -race -count=1 ./...
```

Y para un solo paquete mientras se trabaja en él:

```powershell
cd services/game-server; go test -run TestEjemploNumericoCanonico ./internal/domain/movement
```

No requiere Docker, no requiere red y debe terminar en segundos ([strategy.md](./strategy.md) §2). Si alguna vez deja de cumplirse, el test que lo rompe está en el nivel equivocado.

---

## 12. Documentos relacionados

- [strategy.md](./strategy.md) — reglas duras, convenciones y CI.
- [simulation-tests.md](./simulation-tests.md) — el mismo dominio, pero avanzando ticks reales del loop; ahí viven la validación de comandos y el ciclo de presencia.
- [contract-tests.md](./contract-tests.md) — catálogo de códigos de error y forma de los mensajes.
- [integration-tests.md](./integration-tests.md) — persistencia real de lo que aquí se calcula.
- [../architecture/pathfinding.md](../architecture/pathfinding.md) — costes enteros escalados, heurística octile y desempate.
- [../specs/movement.md](../specs/movement.md) — polilínea temporizada y reglas del comando.
- [../specs/unit.md](../specs/unit.md) — máquina de estados y orden de validación.
- [../specs/presence.md](../specs/presence.md) — máquina `ONLINE → OFFLINE_PENDING → PROTECTED`.
- [../decisions/ADR-011-movement-timed-polyline.md](../decisions/ADR-011-movement-timed-polyline.md) — por qué el movimiento es una polilínea temporizada y no un replay de ticks.
- [../invariants/README.md](../invariants/README.md) — catálogo `INV-*` con su test asociado.
