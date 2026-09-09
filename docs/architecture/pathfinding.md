# Pathfinding

Diseño del subsistema de búsqueda de rutas del Game Server: interfaz estable, algoritmo A\* determinista sobre grid de 8 direcciones, estructuras de datos, límites, presupuesto temporal y ruta de evolución a Hierarchical A\* sin romper el contrato.

Documentos relacionados: [../specs/movement.md](../specs/movement.md) · [./game-loop.md](./game-loop.md) · [./overview.md](./overview.md) · [../specs/websocket-protocol.md](../specs/websocket-protocol.md)

---

## 1. Objetivo y frontera del subsistema

El subsistema vive en `services/game-server/internal/pathfinding` y responde a una única pregunta: **dada una casilla de origen y una de destino sobre el grid, ¿cuál es la secuencia de tiles transitables que las conecta con menor coste?**

Lo que el subsistema **no** hace, deliberadamente:

- No conoce unidades, jugadores, ciudades ni el protocolo de red. No importa `internal/domain/*`.
- No calcula tiempos. Lo que devuelve es puramente geométrico: un `[]world.Tile`. La conversión a **polilínea temporizada** (`{x, y, tMs}`) es responsabilidad de `internal/domain/movement` (`BuildTimedPath`), porque `baseMsPerTile` es una propiedad del tipo de unidad y el pathfinder es agnóstico a la unidad. Ver [../specs/movement.md](../specs/movement.md).
- No decide si el destino es "razonable" desde el punto de vista de gameplay. Valida transitabilidad y límites; el resto es del dominio.
- No hace I/O. Recibe una vista del mundo (`Grid`) ya en RAM.

Esta frontera es lo que permite reemplazar el algoritmo completo (A\* → Hierarchical A\*) sin tocar el protocolo WebSocket v1 ni el dominio.

```
+-------------------------+
|  websocket / commands   |   unit.move { unitId, target }
+-----------+-------------+
            v
+-------------------------+
|  domain/movement        |   valida ownership, estado, destino
|                         |   construye la polilínea temporizada
+-----------+-------------+
            | Pathfinder.FindPath(ctx, grid, from, to, opts)
            v
+-------------------------+     +----------------------+
|  pathfinding (A*)       |<----|  game/world (Grid)   |
|  - heap binario         |     |  terreno + blocked   |
|  - arrays planos        |     |  overlay             |
+-------------------------+     +----------------------+
```

---

## 2. Interfaz estable

La interfaz real vive en `internal/pathfinding/pathfinding.go`:

```go
package pathfinding

// Grid es la vista MÍNIMA del mundo que necesita el pathfinder.
// *world.World la satisface; el algoritmo no sabe nada de unidades,
// ciudades ni jugadores.
type Grid interface {
	Width() int32
	Height() int32
	InBounds(x, y int32) bool
	IsWalkable(x, y int32) bool        // terreno transitable Y sin construcción encima
	MoveCostUnits(x, y int32) int32    // coste de ENTRAR en el tile, en décimas del coste base
	MinMoveCostUnits() int32           // menor coste de un tile transitable del mundo
}

// Options acota una consulta concreta. 0 = usar el valor por defecto del pathfinder.
type Options struct {
	MaxNodes    int   // EO_PATHFINDING_MAX_NODES
	MaxDistance int32 // EO_PATHFINDING_MAX_DISTANCE, distancia de Chebyshev en tiles
}

// Pathfinder es la interfaz estable. Cualquier implementación futura
// (Hierarchical A*, JPS, flow fields) debe satisfacerla sin cambios.
type Pathfinder interface {
	// FindPath devuelve la ruta como secuencia de tiles contiguos en 8-vecindad,
	// empezando por `from` y terminando en `to`.
	FindPath(ctx context.Context, grid Grid, from, to world.Tile, opts Options) ([]world.Tile, error)
}
```

Notas sobre el contrato de salida, que el dominio da por garantizadas:

- La salida es un `[]world.Tile` desnudo: **no** hay struct `Path`, ni coste acumulado, ni contador de nodos expandidos. El coste real lo recalcula `movement.BuildTimedPath` en la escala de tiempo, que es la única que importa fuera del algoritmo.
- **La ruta incluye el tile de origen** como elemento 0 y el destino como último elemento.
- `from == to` devuelve una ruta de **un solo tile** (`[]world.Tile{from}`) y `error == nil`. No es un caso de error.
- Nunca se devuelve una ruta vacía con `error == nil`: o hay al menos un tile, o hay error.
- El `Grid` se pasa **por consulta**, no se guarda en el `AStar`. Así el mismo pathfinder sirve a mundos distintos en los tests.

**Por qué la interfaz aísla al dominio.** El dominio depende de `Pathfinder` (abstracción), no de `AStar` (implementación). Consecuencias concretas:

| Propiedad | Efecto |
|---|---|
| El dominio recibe tiles, no un estado interno del algoritmo | Cambiar heap, heurística o granularidad no afecta a `domain/movement` |
| La salida no lleva tiempos | La curva de velocidad de unidades evoluciona sin tocar pathfinding |
| Errores centinela envueltos con `%w` | El handler traduce siempre igual con `errors.Is`, sea cual sea la implementación |
| `Options` transporta límites, no los lee del entorno | Tests deterministas: se inyectan límites pequeños sin variables globales |
| Sin `time.Now()` ni `rand` | Misma entrada ⇒ misma salida, siempre |

Errores del paquete: son **valores centinela** (`errors.New`), no un tipo con campo `Code`. Se devuelven envueltos con `fmt.Errorf("%w: …")` para conservar el contexto y se comparan con `errors.Is`:

```go
var (
	ErrPathNotFound      = errors.New("no existe ruta hacia el destino")
	ErrTargetOutOfBounds = errors.New("destino fuera de los límites del mundo")
	ErrTargetNotWalkable = errors.New("el destino no es transitable")
	ErrOriginNotWalkable = errors.New("el origen no es transitable")
	ErrPathTooLong       = errors.New("la ruta excede los límites configurados")
)
```

La traducción a códigos estables del protocolo la hace `simulation.pathErrorCode`, **fuera** del paquete de pathfinding:

| Error centinela | Código de protocolo |
|---|---|
| `ErrTargetOutOfBounds` | `TARGET_OUT_OF_BOUNDS` |
| `ErrTargetNotWalkable` | `TARGET_NOT_WALKABLE` |
| `ErrPathTooLong` | `PATH_TOO_LONG` |
| `ErrPathNotFound` | `PATH_NOT_FOUND` |
| `ErrOriginNotWalkable`, `context.Canceled`, cualquier otro | `INTERNAL_ERROR` (rama `default`) |

`ErrOriginNotWalkable` no tiene código propio a propósito: significa que una unidad está sobre un tile intransitable, es decir, **estado corrupto del servidor**, no un error del jugador.

---

## 3. Modelo de coste entero

El terreno expone su coste en **décimas del coste base** (`world.CostBase = 10`, es decir, `costUnits = 10` equivale a multiplicador 1.00). El pathfinder no usa multiplicadores: escala esas décimas a enteros grandes con dos constantes de `pathfinding.go`:

```go
const (
	costScaleOrtho int32 = 1000
	// 1414/1000 ≈ √2. Truncar hacia abajo mantiene la heurística admisible.
	costScaleDiag int32 = 1414
)

step := units * costScaleOrtho          // paso ortogonal
if diagonal { step = units * costScaleDiag } // paso diagonal
```

Nunca se multiplica en float dentro del bucle de búsqueda, y no hay tabla precalculada: `grid.MoveCostUnits(x, y)` devuelve las décimas y la multiplicación por la escala es una instrucción entera.

| TerrainType | valor byte | walkable | `costUnits` | paso ortogonal (`u × 1000`) | paso diagonal (`u × 1414`) |
|---|---|---|---|---|---|
| GRASSLAND | 0 | sí | 10 | 10 000 | 14 140 |
| FOREST | 1 | sí | 16 | 16 000 | 22 624 |
| HILL | 2 | sí | 18 | 18 000 | 25 452 |
| MOUNTAIN | 3 | no | 0 | — | — |
| WATER | 4 | no | 0 | — | — |
| ROAD | 5 | sí | 6 | 6 000 | 8 484 |

`MoveCostUnits` devuelve **0** para un tile intransitable (terreno o blocked overlay), y el bucle de expansión trata `units <= 0` como arista inexistente: es una segunda barrera además de `IsWalkable`.

El coste de un paso se imputa **al tile de destino** del paso, coherentemente con la fórmula de tiempo de [../specs/movement.md](../specs/movement.md) (`costUnits` del tile al que se entra). A diferencia del diseño anterior, la diagonal **no se redondea por terreno**: se escala siempre por 1414, así que el factor diagonal es idéntico en los cuatro terrenos transitables.

**Por qué enteros garantizan determinismo.** Con `float64` la suma de costes depende del orden de las operaciones y del uso de FMA por parte del compilador; dos ejecuciones de la misma búsqueda pueden producir `g` que difieren en el último bit y, por tanto, un desempate distinto y un `Path` distinto. Con `int32`:

1. La suma es asociativa y exacta: `g` de un nodo es idéntico en cualquier arquitectura y con cualquier versión del compilador Go.
2. Las comparaciones `<`, `==` son totales y sin sorpresas (no hay `NaN`, no hay `-0.0`).
3. Los tests de simulación pueden afirmar el `Path` **exacto**, no una tolerancia.
4. El resultado es reproducible entre el servidor y cualquier herramienta offline de auditoría.

**Rango.** Coste máximo por paso = 25 452 (HILL diagonal). Con `EO_PATHFINDING_MAX_NODES=20000`, el `g` acumulado máximo concebible es `20000 × 25 452 = 509 040 000`, todavía muy por debajo del máximo de `int32` (2 147 483 647). No hay riesgo de desbordamiento en la configuración por defecto; si algún día `EO_PATHFINDING_MAX_NODES` subiera a más de ~84 000 con ruta íntegramente de colinas en diagonal, habría que pasar a `int64`. Está anotado como límite conocido, no como suposición.

**Separación explícita entre coste de búsqueda y coste de tiempo.** Son dos escalas distintas y no deben confundirse:

- **Búsqueda** (este documento): enteros, ortogonal `u × 1000`, diagonal `u × 1414`. Sirve para ordenar candidatos.
- **Tiempo** ([../specs/movement.md](../specs/movement.md)): `StepDurationMs(baseMsPerTile, costUnits, diagonal)`, con redondeo al milisegundo más cercano por segmento y factor diagonal `1414214/1000000`.

La diferencia (`1414/1000 = 1.414` frente a `1414214/1000000 = 1.414214`) es intencional: el A\* necesita enteros pequeños para desempatar; la polilínea necesita milisegundos fieles a la geometría. Ambas son deterministas por separado. Como el factor diagonal se aplica **al coste ya escalado por terreno** (y no a una tabla redondeada por terreno), la razón «ms reales / unidad de coste de búsqueda» apenas se dispersa:

| paso | coste de búsqueda | ms (VILLAGER, `baseMsPerTile = 600`) | ms por unidad de búsqueda |
|---|---|---|---|
| cualquier terreno, ortogonal | `u × 1000` | `60 × u` | 0,060000 |
| GRASSLAND diagonal | 14 140 | 849 | 0,060042 |
| FOREST diagonal | 22 624 | 1358 | 0,060025 |
| HILL diagonal | 25 452 | 1527 | 0,059996 |
| ROAD diagonal | 8 484 | 509 | 0,059995 |

El peor cociente entre dos filas es `0,060042 / 0,059995 ≈ 1,0008`: el A\* puede, en casos límite, elegir una ruta cuyo tiempo real sea **hasta un 0,08 % peor** que otra de igual coste de búsqueda. Se acepta en MVP.

---

## 4. Heurística octile ponderada por el coste mínimo

La heurística **no** usa la escala desnuda (1000 / 1414): la pondera por `MinTerrainCostUnits`, el menor `costUnits` de un terreno transitable del mundo, que el `Grid` expone con `MinMoveCostUnits()`.

```go
// minCostUnits = grid.MinMoveCostUnits() = world.MinTerrainCostUnits = 6 (ROAD)
func heuristic(from, to world.Tile, minCostUnits int32) int32 {
	dx := abs32(to.X - from.X)
	dy := abs32(to.Y - from.Y)
	diag := dy
	if dx < dy {
		diag = dx
	}
	straight := dx + dy - 2*diag
	return minCostUnits*costScaleOrtho*straight + minCostUnits*costScaleDiag*diag
}
```

Es decir, `h = 6·1000·rectos + 6·1414·diagonales = 6000·rectos + 8484·diagonales`. `straight` y `diag` son la descomposición octile exacta del desplazamiento en un grid de 8 direcciones sin obstáculos.

**Por qué se pondera por el mínimo y no por el coste de la hierba.** Una heurística es admisible si nunca sobreestima el coste real restante. El paso más barato posible del mundo es exactamente `MinTerrainCostUnits × costScaleOrtho` en ortogonal y `MinTerrainCostUnits × costScaleDiag` en diagonal:

- `MinTerrainCostUnits` vale **6** (ROAD), calculado en `world.computeMinTerrainCost()` recorriendo el catálogo de terrenos transitables; no es una constante escrita a mano, así que añadir un terreno más barato lo actualiza solo.
- Con ese peso, `h` es igual al coste real de un tramo íntegramente de ROAD y **estrictamente menor** en cualquier otro terreno: nunca sobreestima ⇒ **admisible**.
- Además es **consistente** (monótona): para toda arista `n → m`, `h(n) ≤ coste(n,m) + h(m)`, porque el coste de esa arista es al menos el mínimo con el que `h` la contabiliza. Con una heurística consistente y lista cerrada, A\* cierra cada nodo con su `g` óptimo y **no necesita reabrir nodos cerrados**: la ruta devuelta es de coste mínimo.

Haber usado el coste de la hierba (10) habría hecho la heurística **inadmisible** sobre ROAD (sobreestimación con factor hasta `10/6`), y entonces —al no reabrirse nodos cerrados— A\* podría cerrar un nodo con un `g` subóptimo y devolver una ruta peor que la óptima justo en los corredores de camino, que es donde el jugador espera que el pathfinding acierte. Por eso el peso es el mínimo y no la hierba.

Consecuencias prácticas y decisión de MVP:

1. La ponderación se lee del `Grid` en cada consulta, no se cachea en el `AStar`: dos mundos con catálogos de terreno distintos siguen siendo correctos con el mismo pathfinder.
2. Si `MinMoveCostUnits()` devolviera `0` o negativo, el algoritmo lo eleva a `1`. Es una defensa contra un catálogo mal formado, no un caso esperado.
3. El precio de la admisibilidad es expandir más nodos que con una heurística agresiva: al ser el mundo mayoritariamente hierba o peor, la cota inferior de 6 es floja y la frontera crece. Es un intercambio deliberado — corrección antes que velocidad — acotado por `EO_PATHFINDING_MAX_NODES`.
4. **Añadir un terreno transitable más barato que ROAD no rompe nada**, porque `MinTerrainCostUnits` se recalcula desde la tabla. Lo que sí exigiría revisión es introducir costes por unidad (§13), donde el mínimo dejaría de ser global.

---

## 5. Vecindad de 8 direcciones y regla anti corner-cutting

El orden de exploración de vecinos es **fijo y declarado en un array a nivel de paquete**. Nunca se itera un `map` de Go (el orden de iteración de un map es aleatorio por diseño). Los **cuatro ortogonales van primero y los cuatro diagonales después**:

```go
var neighbors = [8]struct{ dx, dy int32 }{
	{0, -1}, {1, 0}, {0, 1}, {-1, 0}, // N, E, S, W  — ortogonales primero
	{1, -1}, {1, 1}, {-1, 1}, {-1, -1}, // NE, SE, SW, NW — diagonales después
}
```

No hay campo `Diagonal`: la condición se deriva en el bucle con `diagonal := n.dx != 0 && n.dy != 0`.

Que los ortogonales precedan a los diagonales **no** es cosmético. Al conservarse el primer padre ante `g` empatado (§7), este orden hace que, en terreno uniforme, las rutas prefieran el paso recto frente al diagonal equivalente; y fija el desempate de forma reproducible sin depender de la geometría del mapa.

**Regla anti corner-cutting:** el movimiento diagonal solo se permite si **ambos** tiles ortogonales adyacentes que forman la esquina son transitables.

```
        x-1     x      x+1
      +------+------+------+
 y-1  |      |  B   |  D   |     Paso A -> D (diagonal NE)
      |      |(x,y-1)(x+1,y-1)   requiere:
      +------+------+------+       IsWalkable(B) == true
 y    |      |  A   |  C   |       IsWalkable(C) == true
      |      | (x,y)|(x+1,y)      Si B o C están bloqueados,
      +------+------+------+      la arista A->D NO existe.
```

Casos que la regla elimina:

```
   MOUNTAIN en B                 MOUNTAIN en B y C
  +------+------+               +------+------+
  |  ##  |  ..  |               |  ##  |  ..  |
  +------+------+               +------+------+
  |  ..  |  ##  |  <- C libre   |  ..  |  ##  |
  +------+------+               +------+------+
  A->D prohibido                A->D prohibido
  (se atravesaría la            (paso imposible por
   arista de la montaña)         un vértice puro)
```

Implementación (dentro del bucle de expansión, antes de cualquier cálculo de coste):

```go
diagonal := n.dx != 0 && n.dy != 0
if diagonal {
	if !grid.IsWalkable(cur.x+n.dx, cur.y) || !grid.IsWalkable(cur.x, cur.y+n.dy) {
		continue // corner-cutting prohibido
	}
}
```

Esta comprobación se aplica también al **validar la polilínea** en el dominio (`movement.Validate`, ver [../specs/movement.md](../specs/movement.md)): el pathfinder no es la única barrera. Está cubierta por `TestProhibidoAtajarEsquinas`.

---

## 6. Estructuras de datos

### 6.1 Índice plano en lugar de mapas

Todo estado por tile se guarda en **arrays planos indexados por `idx = y*width + x`**, nunca en `map[Tile]X`.

| Motivo | Detalle |
|---|---|
| Determinismo | Un `map` de Go tiene orden de iteración aleatorio por diseño; un array no se itera, se indexa |
| Latencia | Acceso O(1) sin hashing, sin colisiones, sin rehash a mitad de búsqueda |
| Localidad de caché | Vecinos E/W son contiguos en memoria; N/S están a `width` enteros de distancia, predecible por el prefetcher |
| Sin asignaciones en régimen | Los arrays se asignan una vez y se reutilizan; un `map` que crece asigna en pleno tick |
| Reconstrucción barata | Del `idx` se recuperan las coordenadas con `x = idx % width`, `y = idx / width` |

Con el mundo MVP de `512 × 512`, `width*height = 262 144` entradas.

```go
type workspace struct {
	generation uint32
	stamp      []uint32 // 262144 × 4 B = 1 MiB (generación en que se escribió gScore)
	gScore     []int32  // 1 MiB
	cameFrom   []int32  // 1 MiB (índice del padre; -1 = raíz)
	closed     []uint32 // 1 MiB (generación en que se cerró el nodo)
	heap       nodeHeap // capacidad amortizada; se recorta con heap[:0], no se reasigna
}
```

Total **4 MiB** exactos de estado por workspace en el mundo por defecto, más el heap. Es memoria fija: no depende del número de unidades ni de jugadores.

`closed` es un array de `uint32` sellado por generación, **no un bitset**: cuesta 1 MiB en lugar de 32 KiB, y a cambio se limpia con el mismo mecanismo de generación que `stamp`, sin recorrer nada por consulta. Es un intercambio consciente de memoria por simplicidad y por no tener dos esquemas de reset distintos.

### 6.2 Reset por generación

Limpiar 262 144 entradas en cada consulta costaría más que la propia búsqueda. En su lugar se usa un sello de generación, en `workspace.reset(size)`:

```go
w.generation++
// gScore[idx] es válido SOLO si stamp[idx] == w.generation; si no, se trata como +∞
// el nodo idx está cerrado SOLO si closed[idx] == w.generation
```

Los arrays solo se reasignan si `cap(w.stamp) < size` (mundo mayor que el de la consulta anterior); en ese caso se reasignan los cuatro y `generation` vuelve a 0 antes del incremento, porque la memoria nueva ya viene a cero.

Al **desbordar** `generation` (`uint32`, tras ~4,29 × 10⁹ consultas) el incremento la deja en 0, que es precisamente el valor «nunca escrito». El código lo detecta con `if w.generation == 0`, recorre `stamp` y `closed` poniéndolos a cero y reinicia `generation = 1`. Es la única consulta que paga un barrido completo, una vez cada 4 mil millones.

`heap` se recorta con `w.heap = w.heap[:0]`, conservando la capacidad ya asignada.

### 6.3 Heap binario con desempate estable

```go
type node struct {
	idx  int32
	f    int32 // gScore + h
	h    int32 // heurística en el nodo
	x, y int32 // coordenadas, cacheadas para no dividir por width en el bucle
}

// Orden TOTAL: (f, h, y, x).
func less(a, b node) bool {
	if a.f != b.f {
		return a.f < b.f
	}
	if a.h != b.h {
		return a.h < b.h // ante igual coste total, preferir lo más cercano al objetivo
	}
	if a.y != b.y {
		return a.y < b.y
	}
	return a.x < b.x
}
```

Decisiones:

- **Heap propio, no `container/heap`.** `container/heap` opera sobre `heap.Interface` con `any` en `Push`/`Pop`, lo que produce boxing y asignaciones por operación. Un heap concreto sobre `[]node` (20 bytes por nodo, sin punteros) evita el escaneo del GC y las asignaciones.
- **`x` e `y` viajan dentro del nodo.** Cuestan 8 bytes por entrada y ahorran una división y un módulo por expansión, que es la operación más cara del bucle interno. La reconstrucción de la ruta sí divide por `width`, pero eso ocurre una vez por consulta, no por nodo.
- **Sin `decrease-key`.** Al relajar un nodo ya en el heap se **inserta un duplicado** con el nuevo `f` (lazy deletion). Al extraer, si el nodo ya está cerrado se descarta (`if ws.isClosed(cur.idx) { continue }`). Cuesta memoria de heap acotada por el número de relajaciones (≤ 8 × nodos expandidos) y ahorra mantener un índice posición→nodo.
- **Desempate explícito por `(y, x)`, no por `idx`.** Aunque `idx = y*width + x` induzca el mismo orden, comparar los campos declarados hace que el criterio siga siendo el mismo si un día el índice deja de ser fila-mayor. El desempate por `h` antes que por posición empuja la búsqueda hacia el destino y reduce la meseta de expansiones en terreno uniforme; `(y, x)` es lo que convierte el orden en **total**: nunca hay dos nodos comparables como iguales, así que el orden de extracción está completamente determinado.

### 6.4 Buffers reutilizables

```go
type AStar struct {
	defaultMaxNodes    int
	defaultMaxDistance int32
	pool               sync.Pool // *workspace
}

func (a *AStar) FindPath(ctx context.Context, grid Grid, from, to world.Tile, opts Options) ([]world.Tile, error) {
	// ... validaciones baratas primero, antes de tocar el pool ...
	ws := a.pool.Get().(*workspace)
	defer a.pool.Put(ws)
	ws.reset(int(width) * int(height))
	...
}
```

- El `AStar` **no guarda el `Grid`**: solo los límites por defecto (`NewAStar(maxNodes, maxDistance)`, con respaldo a 20000 / 256 si se le pasan valores ≤ 0) y el pool.
- En MVP el pathfinding se ejecuta **exclusivamente en la goroutine del game loop**, por lo que en la práctica hay un único `workspace` vivo y el `sync.Pool` no llega a contender.
- El `sync.Pool` se introduce desde el principio porque es la pieza que hace segura la evolución al pool de workers (§9.2): cada goroutine obtiene su propio buffer sin sincronización adicional.
- **La propiedad honesta es «sin asignaciones amortizado», no «cero asignaciones».** `sync.Pool` vacía su contenido en cada ciclo de GC, así que la primera consulta posterior a un GC reasigna los 4 MiB del `workspace` dentro de la fase 2 del tick. En régimen estacionario entre GCs no se asigna nada fuera de la ruta devuelta; el peor caso por ciclo de GC es una reasignación de 4 MiB. Si esa reasignación llegara a aparecer en el presupuesto de §8.3, la corrección es dar al `AStar` un `*workspace` propio mientras el pathfinding siga siendo de una sola goroutine, y reservar el pool para §9.2.
- El `[]world.Tile` devuelto **sí** se asigna por consulta: sale del subsistema y su vida útil la controla el dominio (se convierte en polilínea y se persiste). Se construye con `append` hacia atrás desde el destino y se invierte in situ.

---

## 7. Algoritmo

```go
func (a *AStar) FindPath(ctx context.Context, grid Grid, from, to world.Tile, opts Options) ([]world.Tile, error) {
	// 0. Límites: 0 en Options significa "usa el valor por defecto del AStar"
	//    maxNodes    <- opts.MaxNodes    o a.defaultMaxNodes
	//    maxDistance <- opts.MaxDistance o a.defaultMaxDistance

	// 1. Validaciones baratas, en ESTE orden exacto
	//    !grid.InBounds(to)            -> ErrTargetOutOfBounds
	//    !grid.InBounds(from)          -> ErrTargetOutOfBounds
	//    !grid.IsWalkable(from)        -> ErrOriginNotWalkable
	//    !grid.IsWalkable(to)          -> ErrTargetNotWalkable
	//    chebyshev(from,to) > maxDistance -> ErrPathTooLong
	//    from == to                    -> []world.Tile{from}, nil

	// 2. Preparación
	//    ws := pool.Get(); defer pool.Put(ws); ws.reset(width*height)
	//    minCost := grid.MinMoveCostUnits()  (elevado a 1 si es <= 0)
	//    setG(start,0); setFrom(start,-1); push(start con f = h = heuristic(from,to,minCost))

	// 3. Bucle A*
	//    - cada 512 iteraciones (expanded%512 == 0): comprobar ctx.Done() -> ctx.Err()
	//    - pop del heap con orden (f, h, y, x)
	//    - si isClosed(cur): descartar (entrada obsoleta del heap)
	//    - close(cur)
	//    - si cur.idx == goalIdx: reconstruct() e invertir -> ruta CON el origen
	//    - expanded++; si expanded > maxNodes: ErrPathTooLong
	//    - para cada n en neighbors (4 ortogonales y luego 4 diagonales):
	//        * fuera de límites            -> continue
	//        * isClosed(nIdx)              -> continue
	//        * !grid.IsWalkable(nx,ny)     -> continue
	//        * diagonal && corner-cutting  -> continue
	//        * units := grid.MoveCostUnits(nx,ny); si units <= 0 -> continue
	//        * step := units * costScaleOrtho (o costScaleDiag si diagonal)
	//        * tentative := curG + step
	//        * si ya hay g en esta generación y tentative >= g -> continue  (mejora ESTRICTA)
	//        * setG, setFrom, push(node{f: tentative + h, h: h, x: nx, y: ny})

	// 4. Heap vacío sin alcanzar el destino
	return nil, fmt.Errorf("%w: de %s a %s", ErrPathNotFound, from, to)
}
```

Detalles que no son adorno:

**El objetivo se comprueba al extraer, no al relajar.** Devolver la ruta en cuanto un vecino coincide con el destino sería devolver la primera ruta encontrada, no la más barata. Al comprobarlo tras el `pop`, el destino sale del heap con su `g` mínimo.

**Mejora estrictamente menor (`tentative >= known` ⇒ `continue`).** Ante dos padres con idéntico `g`, se conserva el primero encontrado según el orden fijo de `neighbors`. Junto con el orden total del heap, esto hace que la ruta reconstruida sea única y reproducible, no solo óptima en coste. Lo verifica `TestRutaEsDeterminista` (20 repeticiones de la misma consulta).

**Los nodos cerrados se filtran dos veces**: al extraer del heap y al expandir vecinos. La segunda comprobación es una poda barata que evita calcular coste y heurística de un nodo que se descartaría igualmente. Es correcta precisamente porque la heurística es consistente (§4): un nodo cerrado ya tiene su `g` óptimo y jamás necesita reabrirse.

**Cancelación.** `ctx` se comprueba **cada 512 iteraciones del bucle** (`expanded%512 == 0`) con un `select` no bloqueante, para no pagar el coste en cada iteración. Un `ctx` cancelado devuelve `ctx.Err()` tal cual (`context.Canceled` o `context.DeadlineExceeded`), que `pathErrorCode` traduce a `INTERNAL_ERROR` por la rama `default`. Cubierto por `TestCancelacionPorContexto`. En MVP el único cancelador previsto es el apagado del servidor: la llamada de `simulation` pasa hoy un `context.Background()`.

**Reconstrucción.** Se recorre `cameFrom` hacia atrás desde el destino hasta el origen y se invierte el resultado in situ, de modo que la ruta devuelta **empieza siempre en el origen** (`path[0] == from`) y termina en el destino. Lo asertan `TestRutaTrivialEnLineaRecta` y `TestOrigenIgualADestino`.

---

## 8. Límites, errores y presupuesto

### 8.1 Límites

| Límite | Variable | Valor por defecto | Comprobación | Error centinela |
|---|---|---|---|---|
| Nodos expandidos | `EO_PATHFINDING_MAX_NODES` | 20000 (rango 100 – 10 000 000) | durante el bucle, tras cada expansión | `ErrPathTooLong` |
| Distancia origen-destino | `EO_PATHFINDING_MAX_DISTANCE` | 256 tiles (rango 1 – 65536) | Chebyshev, **antes** de iniciar la búsqueda | `ErrPathTooLong` |
| Destino intransitable | — | — | antes de la búsqueda | `ErrTargetNotWalkable` |
| Origen intransitable | — | — | antes de la búsqueda | `ErrOriginNotWalkable` |
| Origen/destino fuera del mundo | `EO_WORLD_WIDTH` / `EO_WORLD_HEIGHT` | 512 / 512 | antes de la búsqueda | `ErrTargetOutOfBounds` |
| Grafo agotado sin llegar | — | — | heap vacío | `ErrPathNotFound` |

Notas de diseño:

- La distancia se mide con **Chebyshev** (`max(|dx|,|dy|)`), coherente con la vecindad de 8 direcciones: es exactamente el número mínimo de pasos en ausencia de obstáculos. Es una cota inferior barata que evita lanzar búsquedas condenadas.
- `MAX_NODES` y `MAX_DISTANCE` comparten el mismo error centinela (`ErrPathTooLong`) y por tanto el mismo código de protocolo (`PATH_TOO_LONG`). Se distinguen por el **mensaje envuelto** (`"distancia %d > %d"` frente a `"%d nodos expandidos supera el límite de %d"`), nunca mediante códigos nuevos.
- El **origen fuera de límites también devuelve `ErrTargetOutOfBounds`**, no un error propio: es la misma clase de fallo desde el punto de vista del contrato, y `ErrOriginNotWalkable` queda reservado para el caso realmente distinto (unidad sobre tile intransitable ⇒ estado corrupto).
- **Destino intransitable ⇒ rechazo, no tile cercano.** El MVP no busca el tile libre más próximo: sería una heurística de gameplay disfrazada de pathfinding, con reglas propias (¿qué distancia máxima?, ¿qué desempate?) que ningún requisito exige todavía. Decisión documentada y revisable.
- Una ruta vacía nunca se devuelve junto a `error == nil`. O hay ruta con al menos un tile, o hay error.

### 8.2 Complejidad

Sea `N` el número de nodos expandidos, acotado por `min(width*height, MaxNodes)`.

| Recurso | Coste |
|---|---|
| Tiempo | `O(N · 8 · log N)` — 8 relajaciones por expansión, cada `push` es `O(log N)` |
| Memoria de trabajo | `O(width · height)` fija: 4 MiB en el mundo 512 × 512, reutilizada entre consultas |
| Memoria del heap | `O(8N)` nodos de 20 B en el peor caso con duplicados perezosos: ≤ 3,05 MiB (3,2 MB) con `N = 20000` |
| Salida | `O(L)` con `L` = longitud de la ruta |

### 8.3 Presupuesto temporal objetivo

Con `EO_TICK_RATE_HZ=10` el período de tick es **100 ms**. Los siguientes son **objetivos de diseño**, no variables de configuración ni mediciones: el algoritmo está implementado y sus tests unitarios están en verde, pero **no se ha ejecutado ningún benchmark** (§12 lo anota como deuda consciente).

| Escenario | Nodos esperados | Objetivo |
|---|---|---|
| Ruta corta en terreno abierto (≤ 20 tiles) | ~300 | p50 ≤ 60 µs |
| Ruta media con obstáculos (60 tiles) | ~2 500 | p99 ≤ 1,5 ms |
| Peor caso admitido (`MaxNodes` agotado) | 20 000 | ≤ 8 ms |
| **Presupuesto agregado de pathfinding por tick** | — | **≤ 20 ms (20 % del período)** |

Si `eo_pathfinding_duration_seconds` o `eo_game_tick_overruns_total` muestran que el presupuesto agregado se supera de forma sostenida, se activa la evolución descrita en §9.2. La regla operativa es: **el pathfinding nunca puede ser la causa de un overrun de tick**.

---

## 9. Ejecución respecto del game loop

### 9.1 MVP: síncrono, dentro de la fase 2

El pathfinding se ejecuta **dentro de la fase 2 del tick** (`validate & apply commands`, ver [./game-loop.md](./game-loop.md)), en la misma goroutine del loop y sin I/O.

```
tick N (100 ms)
 1. drain commands            <- se sacan de la cola los unit.move pendientes
 2. validate & apply commands <- AQUÍ se ejecuta A*; los tiles resultantes se
                                 convierten en polilínea (BuildTimedPath) y se
                                 crea el movimiento
 3. advance movement          <- solo lee polilíneas ya existentes
 4. resolve simulation        (fuera de MVP)
 5. process timers
 6-7. estado del mundo y deltas  <- unit.movement.started se emite dentro de la
                                    fase que lo provoca, no en un paso aparte
 8. enqueue persistence       <- INSERT unit_movements (asíncrono, vía cola)
```

Por qué síncrono en MVP:

- **Coherencia trivial.** El `Grid` que lee A\* es el estado del mundo en el tick N, sin mutaciones concurrentes. No hacen falta locks ni snapshots.
- **Latencia mínima.** El jugador recibe `unit.move.accepted` y `unit.movement.started` en el mismo tick en que emitió la orden: ≤ 100 ms de latencia de servidor.
- **Determinismo del loop.** Un tick es una función pura del estado previo y de la cola de comandos; los tests de nivel *simulation* ("avanza 10 s, asserta estado exacto") siguen siendo válidos sin sincronización.
- El coste está **acotado por construcción**: `EO_PATHFINDING_MAX_NODES` pone techo duro a cuánto puede tardar una consulta, la cola de comandos está acotada por el rate limit de `EO_WS_RATE_LIMIT_PER_SECOND` (20 msg/s por conexión, ráfaga 40) y el número de comandos aplicados por tick por `loop.Config.MaxCommandsPerTick` (1024, constante de código, no variable de entorno).

### 9.2 Evolución prevista: pool de workers y aplicación diferida

**Esto es diseño previsto, no implementado en MVP.** Se documenta ahora para que la interfaz y las estructuras ya estén preparadas.

Disparador: el presupuesto agregado de §8.3 se supera de forma sostenida, o el número de órdenes de movimiento por tick crece por encima de lo que el loop puede absorber.

Diseño objetivo:

```
fase 2 (tick N)      : se valida el comando (ownership, estado, destino).
                       Si es válido, se encola una PathRequest y la unidad
                       queda en estado de espera; se emite unit.move.accepted.
pool de workers      : G goroutines, cada una con su propio *workspace del
                       sync.Pool. Consumen PathRequest, producen PathResult.
fase 2 (tick N+k)    : se drenan los PathResult listos y se crean los
                       movimientos. Se emite unit.movement.started.
```

Reglas que preservan las garantías:

1. Los workers leen una **vista inmutable del `Grid`** correspondiente a un tick concreto (`gridVersion`). El resultado se sella con esa versión.
2. Al aplicar en el tick `N+k`, si `gridVersion` ya no es la actual y algún tile de la ruta dejó de ser transitable, el resultado se **descarta y se recalcula**; nunca se aplica una ruta obsoleta.
3. Los `PathResult` se drenan en **orden determinista** por `(tickEnqueued, playerId, unitId)`, no en orden de finalización de los workers, para que el loop siga siendo reproducible.
4. `start_time_ms` sigue siendo el `tickTime` del tick que **crea** el movimiento (`N+k`), no el que aceptó el comando. El cliente ya recibió `unit.move.accepted`; `unit.movement.started` llega después con la polilínea.
5. Cota dura: si un `PathRequest` no se resuelve en `k` ticks, se cancela y se responde `unit.move.rejected` con `INTERNAL_ERROR`.

El contrato `Pathfinder` **no cambia**: solo cambia quién lo llama y cuándo se aplica el resultado.

---

## 10. Ruta de migración a Hierarchical A\*

**Fuera de MVP.** Se documenta la ruta para justificar que la interfaz actual no es un callejón sin salida.

Motivación: A\* plano sobre `512 × 512` es cómodo, pero el coste crece con el área explorada. Si el mundo crece o las rutas largas se vuelven frecuentes, HPA\* reduce el trabajo en uno o dos órdenes de magnitud a cambio de un pre-cómputo.

### 10.1 Pre-cómputo por chunk

El grid ya está dividido en chunks de `32 × 32` (ver [ADR-008](../decisions/ADR-008-grid-coordinate-system.md): `chunkX = x / chunkSize`, `chunkId = chunkY*chunksPerRow + chunkX`), es decir `16 × 16 = 256` chunks en MVP. HPA\* reutiliza esa partición tal cual:

1. **Entradas/salidas (portales).** Para cada frontera entre dos chunks adyacentes se detectan los tramos contiguos de tiles transitables a ambos lados. Cada tramo aporta un nodo abstracto (su tile central); un tramo largo puede aportar dos (extremos) para no perder rutas.
2. **Aristas intra-chunk.** Para cada par de portales del mismo chunk se ejecuta un A\* **confinado al chunk** (≤ 1024 tiles) y se guarda el coste. Es el grafo abstracto.
3. **Aristas inter-chunk.** Coste fijo del paso entre los dos portales enfrentados de la frontera.

```
+--------+--------+        Grafo abstracto:
|  c0,0  o  c1,0  |          o = portal (nodo)
|    \   |   /    |          - = arista intra-chunk (coste precalculado)
|     o--+--o     |          | = arista inter-chunk (coste de un paso)
+-----o--+--o-----+
|  c0,1  o  c1,1  |
+--------+--------+
```

Coste de construcción: 256 chunks × (portales² × A\* de ≤1024 tiles). Se hace una vez al arrancar y se **invalida por chunk** cuando cambia el blocked overlay de ese chunk (una ciudad nueva, una muralla). La invalidación toca el chunk y sus 4 vecinos ortogonales, no el mundo entero.

### 10.2 Caché de rutas inter-chunk

Las consultas se resuelven en tres pasos: insertar `from` y `to` como nodos temporales en sus chunks, buscar en el grafo abstracto (cientos de nodos, no cientos de miles) y **refinar** cada tramo abstracto a tiles concretos.

La caché guarda tramos ya refinados con clave `(portalA, portalB, gridVersion(chunk))`, evicción LRU. Una ruta larga suele compartir sus tramos centrales con otras rutas; solo los extremos (origen y destino reales) se refinan siempre.

### 10.3 Por qué el contrato de salida no cambia

| Elemento | A\* plano | HPA\* | ¿Cambia? |
|---|---|---|---|
| `Pathfinder.FindPath` | firma estable | idéntica | no |
| `[]world.Tile` devuelto | tiles contiguos 8-adyacentes, origen incluido | tiles contiguos 8-adyacentes tras refinar, origen incluido | no |
| Regla anti corner-cutting | aplicada en expansión | aplicada en el refinado intra-chunk | no |
| Errores | mismos centinelas del paquete | mismos centinelas | no |
| Protocolo WS | `unit.movement.started` con polilínea | idéntico | **no** |
| `domain/movement` | consume `[]world.Tile` | consume `[]world.Tile` | **no** |

La clave es que HPA\* **siempre refina hasta tiles antes de devolver**. El dominio nunca ve nodos abstractos, portales ni chunks. El único efecto observable para el jugador es que las rutas pueden dejar de ser estrictamente óptimas (HPA\* es subóptimo acotado), lo cual se documenta al activarlo y se cubre con un test comparativo contra A\* plano sobre mapas de referencia en `testdata/`.

Riesgo aceptado y anotado: activar HPA\* **cambia rutas** y por tanto rompe los tests de simulación que afirman polilíneas exactas. Esos tests deben parametrizarse por implementación desde el principio.

---

## 11. Observabilidad

| Señal | Tipo | Etiquetas | Uso |
|---|---|---|---|
| `eo_pathfinding_requests_total` | counter (`CounterVec`) | `result` | tasa de consultas; hoy **todas** se contabilizan con la etiqueta `found`, también las fallidas |
| `eo_pathfinding_duration_seconds` | histogram | — | p50/p99 frente al presupuesto de §8.3 |
| `eo_game_tick_duration_seconds` | histogram | — | detectar si el pathfinding empuja el tick |
| `eo_game_tick_overruns_total` | counter | — | disparador de la evolución §9.2 |

Cómo se alimentan hoy, con exactitud:

- El pathfinder **no toca métricas**. La simulación acumula `LastPathfindingCalls` y `LastPathfindingTime` por tick, y `Loop.observe` los vuelca al final del tick. Mantener el algoritmo libre de dependencias de observabilidad es lo que permite ejecutarlo en tests sin registro de Prometheus.
- Por eso `eo_pathfinding_duration_seconds` observa **el tiempo agregado de todas las consultas del tick**, no una muestra por consulta. Es el número que importa frente al presupuesto agregado de §8.3; el p99 por consulta individual aún no se instrumenta — TBD (fuera de MVP).
- La etiqueta `result` admite los valores del catálogo de `internal/observability` (`found`, `not_found`, …), pero el loop **emite hoy `found` para todas las consultas**: `simulation` incrementa `LastPathfindingCalls` justo después de `FindPath`, **antes** de mirar el error, y `Loop.observe` vuelca ese contador con `observability.ResultFound` fijo. Es decir, `eo_pathfinding_requests_total{result="found"}` es en realidad "consultas totales" y hoy no discrimina fallos. Corregirlo exige separar el contador por resultado en `simulation`; está anotado como deuda, no como cobertura existente. Mientras tanto, la discriminación de fallos se obtiene de `eo_protocol_errors_total{code}` con `code` en `PATH_NOT_FOUND` / `PATH_TOO_LONG` / `TARGET_NOT_WALKABLE` / `TARGET_OUT_OF_BOUNDS`.

Log estructurado (`log/slog`) **solo en fallo**; nunca por consulta exitosa, para no inundar el log en régimen.

---

## 12. Tests

Nivel **unit**, en `services/game-server/internal/pathfinding/astar_test.go`. Sin Docker, sin ficheros: los mapas se escriben en el propio test como **arte ASCII** (`.` GRASSLAND, `f` FOREST, `h` HILL, `#` MOUNTAIN, `~` WATER, `=` ROAD) y `buildWorld` los convierte en un `*world.World`. Todos deterministas y con `require` de `testify`.

Estos son los tests que **existen y están en verde**:

| Test | Caso | Resultado verificado |
|---|---|---|
| `TestRutaTrivialEnLineaRecta` | línea recta en terreno abierto | `path[0] == from`, último elemento `== to`, longitud 6 para 6 tiles |
| `TestOrigenIgualADestino` | `from == to` | ruta de **un solo tile**, sin error |
| `TestDestinoIntransitableSeRechaza` | destino sobre MOUNTAIN | `ErrTargetNotWalkable` |
| `TestOrigenBloqueadoSeDetecta` | origen sobre MOUNTAIN | `ErrOriginNotWalkable` |
| `TestDestinoFueraDeLimites` | `to.X` fuera del mundo | `ErrTargetOutOfBounds` |
| `TestSinRutaPosible` | muro de MOUNTAIN que parte el mundo | `ErrPathNotFound` |
| `TestRodeaElObstaculo` | montaña aislada en medio | la ruta la bordea, todos los tiles transitables y adyacentes en 8-vecindad |
| `TestPrefiereElCaminoAlBosque` | corredor ROAD sobre campo de FOREST | la ruta sube al ROAD: 6 décimas frente a 16 |
| `TestProhibidoAtajarEsquinas` | dos montañas que se tocan en diagonal | ninguna diagonal del resultado atraviesa una esquina cerrada |
| `TestLimiteDeDistanciaSeAplica` | Chebyshev 31 con `MaxDistance = 10` | `ErrPathTooLong` |
| `TestLimiteDeNodosSeAplica` | mundo 40 × 40 con `MaxNodes = 3` | `ErrPathTooLong` |
| `TestRutaEsDeterminista` | misma consulta 21 veces sobre un mapa con obstáculos | ruta idéntica en las 21 ejecuciones |
| `TestConsultasSucesivasNoSeContaminan` | A, luego B, luego A otra vez con el mismo `AStar` | la repetición de A coincide con la primera A y difiere de B (valida el reset por generación) |
| `TestConstruccionesBloqueanLaRuta` | `SetBlocked` sobre la columna central | `ErrPathNotFound`: el blocked overlay corta igual que la montaña |
| `TestCancelacionPorContexto` | mundo 200 × 200 con `ctx` ya cancelado | `context.Canceled` |

Pendientes, anotados como deuda consciente y no como cobertura existente:

- Comparación contra un Dijkstra exhaustivo para asertar optimalidad exacta del coste (hoy la optimalidad se argumenta desde la consistencia de la heurística en §4, no se comprueba con un oráculo independiente).
- Desbordamiento de `generation` (`workspace.reset` con `generation` forzada a `math.MaxUint32`): la rama existe en el código pero ningún test la ejercita.
- Benchmarks (`go test -bench`) con `-benchmem` como guardarraíl frente al presupuesto de §8.3.

Nivel **simulation**: el pathfinding entra en `internal/game/simulation` como dependencia **real** (`pathfinding.NewAStar(20000, 256)`), nunca como mock; ver [../specs/movement.md](../specs/movement.md).

---

## 13. Fuera de MVP

- Hierarchical A\* (§10) y su caché de rutas inter-chunk.
- Pool de workers con aplicación diferida (§9.2).
- Jump Point Search y flow fields para movimiento de grupos.
- Búsqueda del tile transitable más cercano cuando el destino está bloqueado: el MVP rechaza.
- Coste por unidad (unidades que ignoran el coste de FOREST, caballería penalizada en HILL): el `Grid` ya está parametrizado por tile, pero `Options` no lleva perfil de movilidad. Requeriría además revisar la ponderación de la heurística, que hoy usa un mínimo **global** del mundo.
- Evitación dinámica entre unidades: en MVP las unidades **no se bloquean entre sí**; el blocked overlay solo contiene edificios y murallas.
- Pathfinding naval y multicapa (WATER es intransitable en MVP).
