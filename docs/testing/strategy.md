# Estrategia de pruebas

Cómo se verifica Empires Online: los seis niveles de test del canon, qué garantiza cada uno, las reglas duras que ningún cambio puede saltarse, las convenciones de nombres y fixtures, los objetivos de cobertura y el pipeline de CI que decide si un pull request es válido.

> **Estado real.** Todos los niveles del MVP **se ejecutan y pasan hoy** (§2.2): `unit`, `contract`,
> `simulation`, `e2e de transporte`, `integration` —30 tests contra PostgreSQL y Redis reales— y
> `recovery`, en sus dos mitades. El detector de carreras corre sobre toda la suite. `load` sigue fuera
> del MVP.
>
> La infraestructura de `integration` **no es Docker**: está descartado en la máquina de desarrollo. Es
> un cluster PostgreSQL propio y un Redis en WSL; el procedimiento está en
> [../operations/local-development.md](../operations/local-development.md) §3-bis.
> Este documento distingue en todo momento lo que pasa hoy de lo que está previsto.

---

## 1. Por qué este proyecto necesita una estrategia explícita

Un MMORTS persistente falla de maneras que un CRUD no conoce. Los tres modos de fallo caros son:

1. **Estado divergente.** El servidor cree que una unidad está en `(103,102)` y la base de datos dice `(100,100)`. Nadie se entera hasta que un jugador se reconecta y ve teletransporte.
2. **No determinismo.** Un test pasa en el portátil y falla en CI porque alguien iteró un `map` de Go, o porque `time.Now()` se coló en el dominio. El bug real solo aparece bajo carga, meses después.
3. **Deriva de contrato.** El servidor añade un campo, el cliente no lo espera, y el fallo se manifiesta como una pantalla en blanco en producción sin ningún error en los logs del servidor.

La estrategia entera está diseñada contra esos tres modos: los niveles **unit** y **simulation** atacan el no determinismo, **integration** y **recovery** atacan el estado divergente, y **contract** ataca la deriva de contrato. **load** existe para que el modo de fallo número cuatro —degradación bajo carga— tenga un lugar reservado antes de que sea urgente.

La regla de fondo es la del canon §1.6, *Spec-Driven Development*: el test no se escribe después del código para "cubrirlo". El test se deriva de la spec y de los invariantes, que se escriben antes. Un test que solo describe lo que el código hace no verifica nada; solo congela el bug.

---

## 2. La pirámide adaptada

La pirámide clásica (muchos unit, algunos integration, pocos e2e) no describe bien un servidor de simulación. Aquí el eje no es "tamaño del sistema bajo test" sino **qué clase de verdad garantiza cada nivel**. Son seis, exactamente los del canon §19.

```
                            ┌───────────────┐
                            │     load      │  Diferido (no MVP)
                            │   k6 + gen Go │  ¿aguanta?
                          ┌─┴───────────────┴─┐
                          │     recovery      │  ¿sobrevive a un crash?
                        ┌─┴───────────────────┴─┐
                        │      simulation       │  ¿el loop produce el
                        │   FakeClock + N ticks │   estado EXACTO?
                      ┌─┴───────────────────────┴─┐
                      │        contract           │  ¿cliente y servidor
                      │   Zod → JSON Schema → Go  │   hablan lo mismo?
                    ┌─┴───────────────────────────┴─┐
                    │         integration           │  ¿la persistencia real
                    │   Postgres + Redis reales     │   respeta el diseño?
                  ┌─┴───────────────────────────────┴─┐
                  │              unit                 │  ¿la lógica pura es
                  │  dominio, FakeClock, sin I/O      │   correcta y determinista?
                  └───────────────────────────────────┘
```

| Nivel | Qué garantiza exactamente | Qué NO garantiza | Infra | Duración objetivo |
|---|---|---|---|---|
| **unit** | Que el dominio puro calcula lo correcto: coordenadas, A\*, polilínea temporizada, posición autoritativa, transiciones de presencia, aritmética de configuración. Determinismo bit a bit con `Clock` y `RandomSource` inyectados. | Que eso se persista bien, se serialice bien o sobreviva a un reinicio. | Ninguna | < 10 s toda la suite |
| **integration** | Que el esquema real de PostgreSQL y las estructuras reales de Redis hacen cumplir el diseño: transaccionalidad, índices únicos parciales, TTL de presencia, idempotencia durable. | Que el protocolo de red sea correcto, ni que el loop avance bien. | Docker Compose (`postgres`, `redis`) | < 90 s |
| **contract** | Que todo mensaje que sale y entra por el WebSocket valida contra el JSON Schema exportado desde Zod, y que los mensajes inválidos se rechazan **sin efectos secundarios**. | Que la semántica del juego sea correcta: un mensaje puede ser válido y mentir. | Ninguna (schema embebido) | < 15 s |
| **simulation** | Que el game loop, avanzando N ticks con `FakeClock`, produce un estado **exactamente** igual al esperado, y que la misma semilla más la misma secuencia de comandos produce el mismo estado final byte a byte. | Que ese estado se persista, ni que se emita por red. | Ninguna | < 30 s |
| **recovery** | Que un movimiento `ACTIVE` sobrevive a la caída y reinicio del proceso, incluyendo el caso en que la llegada venció durante la caída. | Que el rendimiento sea aceptable. | Docker Compose (`postgres`, `redis`) | < 60 s |
| **load** | Que el sistema sostiene N conexiones, X comandos/s y una latencia p95/p99 acotada, sin fugas de memoria ni crecimiento indefinido de la cola de persistencia. | Nada funcional: la carga no descubre errores de lógica. | Entorno dedicado | Minutos a 24 h (soak) |

**Diferido.** El nivel `load` está **fuera del MVP** (canon §19). Se diseña ahora ([load-tests.md](./load-tests.md)) para que las métricas y los contadores necesarios existan desde el principio, pero no bloquea ningún merge del vertical slice.

### 2.1 Cómo se encadenan

Ningún nivel sustituye a otro. La cadena de confianza es acumulativa y cada eslabón tiene un punto de fallo propio:

```mermaid
flowchart LR
  U[unit<br/>lógica pura] --> I[integration<br/>persistencia real]
  I --> C[contract<br/>protocolo]
  C --> S[simulation<br/>loop determinista]
  S --> R[recovery<br/>crash y reinicio]
  R -.diferido.-> L[load<br/>capacidad]
  U -.invariantes.-> INV[(docs/invariants)]
  I -.invariantes.-> INV
  S -.invariantes.-> INV
```

El orden es el del canon §1.6 (`Unit → Integration → Contract`) con `simulation` y `recovery` enganchados después, tal como fija el [índice de documentación](../README.md#4-metodología-spec-driven-development).

### 2.2 Estado real de cada nivel

Lo que hay hoy en el árbol, con nombre de archivo. Nada de esta tabla es aspiracional.

| Nivel | Archivos reales | Estado |
|---|---|---|
| **unit** | `internal/game/world/world_test.go`, `internal/pathfinding/astar_test.go`, `internal/domain/movement/path_test.go`, `internal/domain/city/city_test.go`, `internal/domain/territory/{territory,seed}_test.go` (30), `internal/auth/ticket_test.go`, `internal/config/config_test.go`, `internal/config/dotenv_test.go`, `internal/persistence/memory/memory_test.go` | **En verde** (`pnpm run server:test`) |
| **contract** | `internal/protocol/contract_test.go` (Go) y `packages/protocol/src/v1/protocol.test.ts` (18 tests Vitest) | **En verde** (`pnpm run server:test` + `pnpm run protocol:test`) |
| **simulation** | `internal/game/simulation/simulation_test.go` (17 tests: el vertical slice, presencia/protección y los casos de recuperación en RAM sobre `simulation.Hydrate`) | **En verde** |
| **e2e de transporte** | `internal/websocket/e2e_test.go` (11 tests): el vertical slice sobre un WebSocket real, con dobles en memoria de PostgreSQL y del estado caliente. `internal/websocket/hub_test.go` (6) cubre el reparto por chunks y su deduplicación por sesión | **En verde**. Es el nivel que encontró que `r.Context()` mataba la sesión tras el primer mensaje |
| **integration** | `internal/persistence/postgres/integration_test.go` (12), `territories_integration_test.go` (9) y `internal/persistence/redis/redis_integration_test.go` (9), más el harness `testenv_integration_test.go`. Etiqueta `integration` + gate `EO_INTEGRATION=1` (§4, R8) | **En verde, 30 tests**, ejecutados con `-race` |
| **recovery** | RAM en `simulation_test.go`; durable en `TestRecuperacionCompletaTrasReinicio` y, para el ownership, `TestElOwnershipSobreviveAUnReinicio` (nivel `integration`) | **En verde**, ambas partes |
| **frontend** | `apps/web/src/{net/client,render/iso,state/interpolation,state/world}.test.ts` | **En verde**, 58 tests Vitest |
| **humo de despliegue** | [`scripts/smoke.mjs`](../../scripts/smoke.mjs) | **Ejecutado.** No es un nivel de la pirámide: ver abajo |
| **load** | Ninguno | **Fuera de MVP** |

**El humo no es un test, y la distinción importa.** Los niveles de arriba verifican el *código*: corren en
CI, sobre dobles o sobre servicios efímeros, y responden a «¿está bien escrito esto?». `scripts/smoke.mjs`
verifica un *despliegue*: se ejecuta contra un servidor ya arrancado y responde a «¿este binario, contra
esta base, con esta configuración, hace lo que promete?». Un cambio de configuración no rompe ningún test
y sí rompe el humo — que es exactamente para lo que está.

Recorre el vertical slice completo por la red, incluida la parte que ninguna sonda HTTP puede comprobar:
cierra la conexión a mitad de movimiento, espera sin cliente conectado más allá de la hora de llegada y
reconecta para verificar que la unidad llegó igualmente. Su sitio en el procedimiento es el Paso 5 de
[deployment.md](../operations/deployment.md), y también sirve para validar una máquina de desarrollo
recién montada.

```bash
node scripts/smoke.mjs --url http://localhost:8080
```

---

## 3. Herramientas

| Lenguaje | Framework | Aserciones | Notas |
|---|---|---|---|
| Go (versión del módulo en `services/game-server/go.mod`; toolchain instalado en la máquina de desarrollo: **1.27.0**) | `testing` estándar | `github.com/stretchr/testify/require` | `require` (aborta), **no** `assert` (continúa). Un test que sigue tras una aserción fallida produce cascadas de errores ilegibles. |
| TypeScript | **Vitest 2** | `expect` de Vitest | Configurado en `packages/protocol/vitest.config.ts`, con `include: ['src/**/*.test.ts']`. |
| E2E cliente↔servidor | Cliente WS en Go | — | Canon §19. **Aún no existe**: se implementará en Go, dentro del mismo módulo, para reutilizar los dobles de prueba. |
| Carga (diferido) | **k6** + generador propio en Go | — | Ver [load-tests.md](./load-tests.md). |

**Dependencias de test permitidas en Go.** `testify/require`, `github.com/golang-jwt/jwt/v5` y `github.com/google/uuid` (ya dependencias de producción) y la librería estándar. Cualquier otra dependencia de test (mocking frameworks, generadores de aserciones, contenedores efímeros gestionados desde el test) requiere ADR. Razón: los dobles se escriben a mano en el propio `_test.go` del paquete —`recorder`, `syncPersister`, `fakeRepos`, `memoryConsumer`—, son pocos y son parte del diseño; un framework de mocks invita a testear interacciones en vez de comportamiento.

**Ejecución.** Todo pasa por pnpm scripts (canon §2). **No hay Makefile**: `make` no está instalado en la máquina de desarrollo Windows. Estos son los scripts que **existen hoy** en el `package.json` de la raíz:

```powershell
pnpm run server:test              # go test ./...  → unit + contract (Go) + simulation
pnpm run protocol:test            # vitest run en packages/protocol
pnpm run server:test:integration  # go test -tags=integration ./...  (requiere Docker y EO_INTEGRATION=1)
pnpm run server:vet               # go vet ./...
pnpm run protocol:build           # Zod → JSON Schema, y espejo en internal/protocol/schema/v1
pnpm run protocol:check           # falla si el JSON Schema versionado ha derivado de Zod
pnpm run docs:check               # node scripts/check-docs.mjs: integridad de docs/ (enlaces, ADR, INV-*)
pnpm run server:fmt:check         # gofmt -l . (falla si la lista no está vacía)
pnpm run verify                   # protocol:build + docs:check + typecheck + test
                                  #   + server:fmt:check + server:vet + server:test
```

**No existen** `test:unit`, `test:contract`, `test:simulation` ni `test:recovery` como scripts propios: los tres primeros niveles viven dentro de `server:test` (más `protocol:test` para el lado TypeScript) y el cuarto todavía no tiene código. Si en el futuro se separan, se añaden al `package.json` y se actualiza esta lista.

El detalle de cada script está en [../operations/local-development.md](../operations/local-development.md).

---

## 4. Reglas duras

No son recomendaciones. Un pull request que las viole se rechaza en revisión aunque CI esté en verde.

| # | Regla | Cómo se detecta la violación |
|---|---|---|
| **R1** | Ninguna feature importante se acepta sin tests. "Importante" = cualquier cambio que toque `internal/domain/**`, `internal/game/**`, `internal/pathfinding`, el protocolo o el esquema de base de datos. | Revisión humana + el checklist de la Definition of Done (§9). |
| **R2** | Todo invariante importante tiene test cuando es viable. Cada `INV-*` del [catálogo](../invariants/README.md) queda asociado a su test. Si un invariante no es verificable con test automatizado, el documento debe decir por qué y qué control lo sustituye. | La asociación real en el árbol es un **comentario inmediatamente encima del test** con el ID y su enunciado — por ejemplo `// INV-WORLD-001: toda coordenada válida está dentro de [0,width) x [0,height).` sobre `TestLimitesDelMundo`. Un `grep -rn "INV-" --include=*_test.go` enumera la cobertura; un `INV-*` sin test y sin justificación explícita es un hallazgo de revisión. |
| **R3** | **Nada de `time.Now()` ni de aleatoriedad no inyectada en el dominio** (canon §1.5). El dominio recibe `clock.Clock` y `clock.RandomSource` por constructor o por argumento. | `go vet` + revisión, más una búsqueda de `time.Now(` y `math/rand` bajo `internal/domain/**`, `internal/game/**` e `internal/pathfinding`. La única implementación autorizada de `Clock` en producción es `clock.SystemClock`, y sólo se construye en `cmd/server`. |
| **R4** | Prohibido iterar un `map` de Go cuando el orden afecta al resultado (canon §8). Si hay que recorrer un mapa, se ordenan las claves primero. | Revisión + tests de reproducibilidad (`TestSimulacionEsReproducible`, `TestRutaEsDeterminista`, `TestChunksEnRadioSeRecortanYVanOrdenados`) que ejecutan lo mismo dos o más veces y comparan el resultado. |
| **R5** | Ningún test usa `time.Sleep` para esperar a que "algo pase". Se usa `clock.FakeClock`, un canal, o una espera con condición y deadline explícito. | Revisión + búsqueda de `time.Sleep` en `*_test.go`: **hoy no aparece ninguna**. Las únicas excepciones legítimas serán las del nivel `integration` (expiración real de una clave de Redis) y deberán llevar comentario justificándolo. |
| **R6** | Los tests no comparten estado mutable global. Cada test construye su mundo (`newTestWorld`, `buildWorld`, `newHarness`). Los tests de integración limpiarán su rastro (§4 de [integration-tests.md](./integration-tests.md)). | `go test -count=2` y ejecución en orden aleatorio (`-shuffle=on`) en el pipeline nocturno. |
| **R7** | Un test que falla intermitentemente se **arregla o se borra**, nunca se marca como *skip* indefinido ni se reintenta automáticamente. Un `flaky` tolerado destruye la señal de toda la suite. | Pipeline nocturno con `-count=10`; cualquier test no determinista aparece ahí. |
| **R8** | Los tests de integración están *gated* por **dos** mecanismos a la vez: la etiqueta de compilación `integration` y la variable `EO_INTEGRATION=1`, con `t.Skip` —no `t.Fatal`— si la variable no está. Quien no tenga Docker arrancado debe poder ejecutar `pnpm run server:test` sin ruido. | Ejecutar `pnpm run server:test` sin la variable: debe terminar en verde. Es lo que hace `go test -race -count=1 ./...` en el job `game-server` de la CI. |
| **R9** | Los códigos de error del canon §16 se comparan por `code`, **jamás** por el texto humano del mensaje. | Revisión: un test que asserta `err.Error() == "unidad no encontrada"` se rechaza. El patrón correcto es `require.ErrorIs(t, err, pathfinding.ErrTargetNotWalkable)` o `require.Equal(t, protocol.CodeUnitNotOwned, payload.Code)`. |
| **R10** | Ningún test escribe en la base de datos de desarrollo. Los tests de integración usan una base de datos y un índice de Redis dedicados, y su URL llega por variables **propias**: `EO_TEST_POSTGRES_URL` y `EO_TEST_REDIS_URL` (§2 de [integration-tests.md](./integration-tests.md)). | Los tests no leen `EO_POSTGRES_URL` ni `EO_REDIS_URL`: usar variables distintas hace imposible apuntar por descuido a la base de desarrollo. El harness aborta además si detecta el nombre de la base de desarrollo. |
| **R11** | Los valores de gameplay no se hardcodean en los tests si el canon los define como configurables (canon §17). El test lee la configuración o inyecta un valor explícito y comentado. | Revisión. Excepción: los valores *esperados* de una aserción son literales a propósito (ver §7). |

### 4.1 Sobre R3: por qué es la regla más importante

`time.Now()` dentro del dominio destruye tres cosas a la vez, no una:

- **Los tests dejan de ser deterministas.** Un test de movimiento que dependa del reloj real da resultados distintos si la máquina está cargada.
- **Los tests dejan de ser rápidos.** Verificar un cooldown de 300 s (`EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS`) requeriría esperar 300 s reales. Con `FakeClock` cuesta microsegundos.
- **La recuperación tras crash deja de ser auditable.** La posición autoritativa se deriva de `start_time_ms` y de la polilínea (canon §7). Si el dominio consultara el reloj del sistema en puntos arbitrarios, la reconstrucción analítica dejaría de ser reproducible y el nivel `recovery` no podría afirmar nada.

Por eso la prohibición se aplica con lint, no con buena voluntad.

---

## 5. Convenciones de nombres y organización de archivos

### 5.1 Ubicación

Árbol **real** (✔ = existe hoy; ○ = previsto):

```
services/game-server/
├── internal/
│   ├── clock/
│   │   ├── clock.go                      ✔ Clock, SystemClock, FakeClock
│   │   └── random.go                     ✔ RandomSource determinista
│   ├── domain/movement/
│   │   ├── path.go, movement.go
│   │   └── path_test.go                  ✔ unit: polilínea, PositionAt, Validate, ciclo de vida
│   ├── domain/city/
│   │   └── city_test.go                  ✔ unit: autómata de presencia, protección, población
│   ├── pathfinding/
│   │   ├── pathfinding.go, astar.go
│   │   └── astar_test.go                 ✔ unit: A*, límites, determinismo, corner cutting
│   ├── game/world/
│   │   └── world_test.go                 ✔ unit: límites, chunks, costes, overlay, generador
│   ├── auth/
│   │   └── ticket_test.go                ✔ unit: verificación del game ticket
│   ├── config/
│   │   └── config_test.go                ✔ unit: defaults y validaciones cruzadas
│   ├── protocol/
│   │   ├── protocol.go, codes.go, schema.go
│   │   ├── schema/v1/*.json              ✔ espejo generado (go:embed), NO se edita a mano
│   │   └── contract_test.go              ✔ contract (lado Go)
│   ├── game/simulation/
│   │   └── simulation_test.go            ✔ simulation + recuperación en RAM
│   ├── game/world/world_test.go          ✔ unit: límites, chunks, terreno, generador determinista
│   ├── pathfinding/astar_test.go         ✔ unit: A*, corner cutting, límites, determinismo
│   ├── domain/movement/path_test.go      ✔ unit: polilínea temporizada y posición autoritativa
│   ├── domain/city/city_test.go          ✔ unit: autómata de presencia y protección
│   ├── websocket/e2e_test.go             ✔ e2e: vertical slice completo sobre transporte real
│   ├── persistence/memory/memory_test.go ✔ unit: TTL y atomicidad con reloj falso
│   └── persistence/{postgres,redis}/
│       └── *_integration_test.go         postgres ✔ ejecutado (12); redis ○ sin Redis en la máquina
└── testdata/                             ○ golden files y escenarios (aún no existe)
```

```
packages/protocol/
├── src/v1/{common,errors,client,server,index}.ts
├── src/v1/protocol.test.ts               ✔ 18 tests (Vitest)
├── src/scripts/export-schema.ts          ✔ genera schema/v1 y el espejo del módulo Go
└── schema/v1/{client-message.schema.json,server-message.schema.json,error-codes.json}
```

**No existe un paquete `internal/testsupport`.** Los dobles de prueba viven en el `_test.go` del paquete que los necesita (`recorder`, `syncPersister`, `fakeRepos` en `simulation_test.go`; `memoryConsumer`, `failingConsumer` en `ticket_test.go`) y los constructores de mundo son funciones de test locales (`newTestWorld`, `buildWorld`, `newHarness`). Lo único compartido y compilable es `internal/clock`, que es código de producción: `FakeClock` vive ahí porque el `Clock` inyectado forma parte del diseño, no del andamiaje de test. Si algún día un doble se necesita desde tres paquetes distintos, se extrae a `internal/testsupport` y se actualiza este árbol; hoy sería una abstracción sin usuarios.

### 5.2 Nombres de test en Go

La convención **efectiva del árbol** es una frase descriptiva que nombra la regla verificada, no el método bajo prueba:

| Clase | Patrón | Ejemplo real |
|---|---|---|
| Comportamiento | `Test<FraseQueDescribeLaRegla>` | `TestDestinoIntransitableSeRechaza`, `TestProhibidoAtajarEsquinas`, `TestTicketNoSePuedeReutilizar` |
| Invariante | El mismo patrón, con el ID declarado en un **comentario justo encima** | `// INV-MOVE-001: una unidad tiene como máximo un movimiento activo.` sobre `TestNuevaOrdenReemplazaLaAnterior` |
| Benchmark | `Benchmark<Sujeto>_<Escenario>` | Ninguno todavía |

El nombre del test se lee como una frase que describe la regla. Si no se puede nombrar así, probablemente el test verifica varias cosas y hay que partirlo.

**Por qué el ID de invariante va en un comentario y no en el nombre.** Un mismo test suele cubrir un invariante y algo más (`TestAutomataDePresencia` cubre las transiciones permitidas de `presence_state` y además el rechazo de estados desconocidos), y un mismo invariante se verifica desde dos niveles (`INV-MOVE-001` en `simulation_test.go` en RAM y, cuando exista, en `integration` contra el índice único parcial). Meter el ID en el nombre obligaría a partir tests que están bien como están, o a nombres que mienten. El comentario permite `grep` igual de bien y no distorsiona el diseño del test.

**Subtests.** Table-driven con `t.Run(tc.name, ...)` siempre que haya más de dos casos de la misma forma. El `name` del caso es una descripción corta, nunca `case1`, `case2`:

```go
cases := []struct {
    name     string
    cost     int32
    diagonal bool
    want     int64
}{
    {"hierba ortogonal", 10, false, 600},
    {"hierba diagonal", 10, true, 849},
    {"bosque ortogonal", 16, false, 960},
}
```

**Paquete de test.** **Externo siempre** (`package movement_test`, `package world_test`, `package simulation_test`): obliga a probar a través de la API pública y detecta acoplamientos accidentales. Hoy no hay ni una sola excepción en el árbol; introducir una exige un comentario que explique por qué el helper no exportado merece test propio.

### 5.3 Nombres de test en TypeScript

`packages/protocol/src/v1/protocol.test.ts` agrupa por contrato, con `describe` en español y expectativas que nombran la regla:

```ts
describe('envelope cliente → servidor', () => {
  it('distingue versión no soportada de mensaje inválido', () => { /* … */ })
  it('rechaza coordenadas no enteras: el mundo es un grid de tiles', () => { /* … */ })
})
```

Los ficheros de test viven junto al código que verifican (`src/v1/*.test.ts`), que es lo que recoge `include: ['src/**/*.test.ts']` en `vitest.config.ts`. **No hay directorio `__tests__`.**

---

## 6. Datos de prueba y fixtures deterministas

### 6.1 Principios

1. **Cada test construye su mundo.** No hay una "base de datos de test poblada" compartida entre tests. La poblada compartida es la fuente número uno de tests acoplados.
2. **Los fixtures son valores, no ficheros.** Hoy **todos** los mundos de test se construyen en el propio `_test.go`: `newTestWorld(t, 64, 64, 32, world.Grassland)` rellena un `[]byte` uniforme, `buildWorld(t, []string{...})` parsea un mapa ASCII de la propia tabla del test. Un fichero en `testdata/` sólo se justifica cuando el valor es grande y estructurado (§6.4).
3. **Nada aleatorio sin semilla explícita.** El generador real se siembra con `EO_WORLD_SEED` (20260909) y el test asserta que el resultado es idéntico entre ejecuciones (`TestGeneracionEsDeterminista`) y distinto con otra semilla.
4. **El tiempo siempre es un valor.** Los tests usan un epoch fijo, nunca el reloj del sistema.

### 6.2 Mapas ASCII

El fixture de mundo canónico del proyecto es un mapa ASCII, porque un test de pathfinding se lee y se corrige a ojo. La leyenda **real** de `astar_test.go` usa el `costUnits` entero de cada terreno (canon §5), no un multiplicador en coma flotante:

```
. = GRASSLAND (byte 0, walkable, costUnits 10)
f = FOREST    (byte 1, walkable, costUnits 16)
h = HILL      (byte 2, walkable, costUnits 18)
# = MOUNTAIN  (byte 3, NO walkable)
~ = WATER     (byte 4, NO walkable)
= = ROAD      (byte 5, walkable, costUnits  6)
```

```go
w := buildWorld(t, []string{
    ".....",
    "..#..",
    ".....",
})
```

`buildWorld` construye un `*world.World` con `chunkSize = 1` (para que cualquier dimensión sea válida) y `seed = 1`, y todas las filas deben medir lo mismo. El origen del mapa es `(0,0)` arriba-izquierda, igual que el mundo real (canon §4).

Para bloquear un tile **sin** cambiar su terreno se usa la capa de ocupación, no un carácter del mapa:

```go
w.SetBlocked(2, 0, 2, 2, true)   // una ciudad ocupa la columna central
```

Es exactamente lo que hace `TestConstruccionesBloqueanLaRuta`, y verifica algo que un carácter de mapa no podría: que el overlay corta el paso **igual que la montaña** sin mutar el terreno de debajo.

### 6.3 Constantes de fixture

No hay un paquete de constantes compartido: cada paquete de test declara las suyas al principio del fichero, marcadas como lo que son.

```go
// simulation_test.go y ticket_test.go
// epoch es un instante fijo. Todos los tests parten de aquí: nada depende del
// reloj real de la máquina.
const epoch int64 = 1_757_376_000_000

// path_test.go — villagerMsPerTile es la velocidad base del aldeano del MVP.
const villagerMsPerTile int64 = 600
```

El epoch de fixture vigente en el árbol es **`1_757_376_000_000`** y la semilla de mundo, la canónica `EO_WORLD_SEED = 20260909` (canon §17).

Los valores de gameplay que son del canon (`baseMsPerTile` de `VILLAGER` = 600 ms, TTL de presencia = 30 s, cooldown = 300 s) no se "inventan" en el test: se pasan explícitamente al constructor bajo prueba —`ProtectionCooldown: 300 * time.Second`, `DisconnectGrace: 30 * time.Second` en `simulation.Deps`— para que el test declare qué configuración está ejercitando. `config_test.go` es el único que comprueba que esos valores son también los **valores por defecto** de `internal/config`.

### 6.4 Golden files

**Estado: previstos, aún no existen.** El único artefacto generado que hoy se versiona y se compara es el JSON Schema del protocolo (`packages/protocol/schema/v1/` y su espejo `services/game-server/internal/protocol/schema/v1/`), y su deriva la detecta la CI (§8). Cuando se añadan golden files de mensajes en `services/game-server/testdata/protocol/v1/`, valen estas reglas:

- Un golden file se regenera con un flag explícito (`go test ./... -update-golden`), nunca automáticamente.
- El diff del golden file entra en la revisión del pull request como cualquier otro cambio. Un golden que cambia sin que nadie explique por qué es un hallazgo de revisión.
- Los golden del protocolo son además los ejemplos del documento [../specs/websocket-protocol.md](../specs/websocket-protocol.md): si un ejemplo del documento deja de validar, el test falla.

---

## 7. Cobertura: objetivos y por qué no es el objetivo

### 7.1 Objetivos por paquete

La cobertura útil es la del código donde un error es caro y silencioso. En los adaptadores, la cobertura alta mide sobre todo la paciencia de quien escribió mocks.

| Paquete / área | Objetivo de líneas | Racional |
|---|---|---|
| `internal/domain/{movement,city}` | **≥ 90 %** | Lógica pura, barata de testear, cara de equivocar. Aquí viven los invariantes. |
| `internal/pathfinding` | **≥ 90 %** | Algoritmo con muchas ramas y un desempate determinista que hay que fijar por test. |
| `internal/game/{loop,world,simulation}` | **≥ 85 %** | El orden de fases y la aritmética de tick son el corazón del determinismo. |
| `internal/auth` | **≥ 80 %** | Superficie de seguridad; cada camino de rechazo debe estar cubierto. |
| `internal/websocket` | **≥ 70 %** líneas, **100 % de los caminos de error** | El *happy path* lo cubrirán contract y e2e; lo que importa aquí es que cada código del canon §16 alcanzable tenga su test. |
| `internal/config` | **≥ 70 %** | Parseo, valores por defecto y validaciones cruzadas. |
| `internal/protocol` | **≥ 70 %** | Envelopes, deltas parciales y catálogo de códigos; el resto son constantes. |
| `internal/persistence/{postgres,redis}` | **≥ 50 %**, medida con la etiqueta `integration` | Casi todo su valor lo verifican los tests de integración contra el motor real; la cobertura sin Docker es engañosamente baja y no debe perseguirse con mocks. **Hoy la cobertura de estos paquetes es 0 %**, y así seguirá hasta que el nivel `integration` se ejecute. |
| `internal/observability` | Sin objetivo numérico | Cableado y emisión de métricas. Se verifica que emiten, no cuánto código se ejecuta. |
| `internal/httpapi`, `cmd/server` | Sin objetivo numérico | Composición de dependencias y alta provisional de jugador. Se verifica arrancando: `/health` y `/ready` (canon §18). |
| `packages/protocol` (Vitest) | **≥ 95 %** | Son esquemas: cada rama es un caso de validación real. |
| `apps/web` | **≥ 40 %** | **Ya existe**, con 58 tests de Vitest sobre transformaciones isométricas, interpolación, store del mundo y cliente WebSocket: el objetivo aplica desde el milestone que cree el frontend Next.js. Se testeará la lógica de estado y el cliente de protocolo; el render de PixiJS no se testea unitariamente. |

Estos porcentajes son **umbrales de alerta**, no gates de CI que rompan el build por medio punto. Una bajada brusca en `internal/domain/**` es una señal para mirar el diff, no para añadir tests decorativos. La CI publica hoy la cobertura agregada (`go tool cover -func=coverage.out | tail -1`) como dato informativo, sin umbral que rompa el build.

### 7.2 Por qué la cobertura no es el objetivo

La cobertura mide qué líneas se **ejecutaron**, no qué propiedades se **verificaron**. Un test que llama a `FindPath` y solo comprueba `require.NoError(t, err)` cubre el 100 % de A\* y no detecta que la ruta atraviesa una montaña.

Lo que sí es objetivo, y se revisa en cada pull request:

1. **Cada invariante del catálogo tiene su test nombrado** (regla R2).
2. **Cada código de error alcanzable del canon §16 tiene un test que lo produce**, y ese test verifica además que **no hubo mutación de estado** (Definition of Done, canon §19).
3. **Cada regla de negocio de la spec tiene al menos un caso positivo y uno negativo.**
4. **Cada fórmula numérica se verifica con valores exactos**, no con rangos ni tolerancias (ver §8 de [unit-tests.md](./unit-tests.md)).

Si esas cuatro se cumplen, la cobertura sale alta sola. Si se persigue la cobertura directamente, salen tests que ejecutan código sin afirmar nada.

---

## 8. Pipeline de CI

GitHub Actions, fichero `.github/workflows/ci.yml`, que **existe** y se dispara en `push` a `main` y en cada `pull_request` contra `main`. Son **cinco jobs**, no una cadena lineal de diez etapas: `docs`, `protocol`, `game-server` e `integration` corren en paralelo, y `docker` es el único que depende de otro (`needs: [game-server]`).

```
docs ────────┐
protocol ────┤
             ├─────────────────────►  (PR válido)
game-server ─┼──► docker
integration ─┘
```

El workflow lleva además `concurrency` con `cancel-in-progress: true`: un push nuevo sobre la misma rama cancela la ejecución anterior.

Versiones fijadas en el workflow: `GO_VERSION: '1.23'`, `NODE_VERSION: '22'`, `PNPM_VERSION: '10'`.

### 8.1 Pasos de cada job

| Job | Paso | Comando | Servicios | Falla si… |
|---|---|---|---|---|
| **docs** | Integridad documental | `node scripts/check-docs.mjs` | — | Hay enlaces relativos rotos, ADR citados que no existen o `INV-*` citados y no registrados. Es el paso más barato del pipeline y el que impide que esta carpeta vuelva a divergir del árbol. |
| **protocol** | Typecheck | `pnpm -r run typecheck` | — | El TypeScript no compila. |
| | Tests del protocolo | `pnpm run protocol:test` | — | Falla cualquiera de los 18 tests de Vitest. |
| | **Deriva de schema** | `pnpm run protocol:build` + `pnpm run protocol:check` + `git diff --exit-code -- packages/protocol/schema services/game-server/internal/protocol/schema` | — | El JSON Schema versionado no coincide con el generado desde Zod, **en cualquiera de las dos copias** (§6 de [contract-tests.md](./contract-tests.md)). |
| **game-server** | Formato | `gofmt -l .` (falla si la lista no está vacía) | — | Hay ficheros sin formatear. |
| | Dependencias limpias | `go mod tidy` + `git diff --exit-code -- go.mod go.sum` | — | `go.mod`/`go.sum` no están al día. |
| | Vet | `go vet ./...` | — | `go vet` señala algo. |
| | Build | `go build ./...` | — | El módulo no compila. |
| | **Tests** | `go test -race -count=1 ./...` | — | Falla cualquier test de `unit`, `contract` (lado Go) o `simulation`, o salta el detector de carreras. |
| | Cobertura | `go test -count=1 -coverprofile=coverage.out ./...` + `go tool cover -func` | — | Informativo: publica el total, no impone umbral. |
| **integration** | Tests de integración | `go test -race -count=1 -tags=integration ./...` con `EO_INTEGRATION=1`, `EO_TEST_POSTGRES_URL=postgres://empires:empires_ci_password@localhost:5432/empires_test?sslmode=disable` y `EO_TEST_REDIS_URL=redis://localhost:6379/1` | `postgres:16-alpine` (usuario `empires`, base `empires_test`) y `redis:7-alpine` (índice **1**), ambos con healthcheck | Falla cualquier test de integración. La etiqueta `integration` cubre hoy tres ficheros: `internal/persistence/postgres/integration_test.go` y su `testenv_integration_test.go` (12 tests, verificados en verde contra PostgreSQL real) e `internal/persistence/redis/redis_integration_test.go`, que **no se ha ejecutado nunca** —ver la nota bajo esta tabla—. |
| **docker** | Imagen | `docker/build-push-action` sobre `infra/docker/game-server.Dockerfile` | — | La imagen del Game Server no se construye. |

**Todas son obligatorias.** Un pull request con cualquier check en rojo no es válido, independientemente de la urgencia (canon §19).

> **Qué de esta tabla se ha ejecutado de verdad, y qué no.** El documento describe una CI que todavía no ha llegado a correr —GitHub Actions rechaza los jobs por facturación de la cuenta, antes de arrancarlos—, así que conviene no leerlo como si todo estuviera probado.
>
> 1. **El detector de carreras: verificado.** `-race` requiere cgo y, por tanto, un compilador de C, que la máquina de desarrollo (Windows) no tiene: allí `go test -race` aborta con `-race requires cgo`. Se ejecuta desde **WSL** (Ubuntu 22.04, Go 1.27.0, gcc 11.4.0) siguiendo el procedimiento de [local-development.md](../operations/local-development.md). Los 10 paquetes con tests pasan. Importa porque el aislamiento del bucle de juego —un solo escritor, sin mutexes, **por diseño**— no está garantizado por ningún candado: `-race` es lo único que distingue «aislado» de «todavía no ha fallado».
> 2. **Los tests de integración de Redis: verificados.** 9 tests contra un Redis real en WSL, con `-race`, ejecutados y en verde (no saltados: `t.Skip` sólo actúa sin `EO_TEST_REDIS_URL`). Nota de versión: en WSL es Redis **6.0.16** y la CI usa **7-alpine**. Los comandos que ejercen estos tests (`SET NX`, `EVAL`, TTL) son muy anteriores a ambas, pero la diferencia existe y no está cubierta.
> 3. **La construcción de la imagen del contenedor: sin verificar, y sin vía local.** No es que Docker no esté instalado: es que **no se va a usar en esta máquina**, porque produce pantallazos azules por consumo de RAM. No hay sustituto —construir una imagen exige un daemon—, así que este job sólo puede comprobarse en la CI. Es hoy la única afirmación de este documento sin ninguna evidencia detrás.

**Lo que la CI todavía no hace, y hay que añadir cuando exista lo que verifica:** no hay paso de `lint`/ESLint (`pnpm run lint` requiere que cada paquete declare su script), no hay paso de `web:build` pese a que `apps/web` ya existe, y no existe todavía el pipeline nocturno de §8.2. La comprobación de enlaces rotos **sí** existe ya, en el job `docs`. Documentar lo pendiente como pendiente es preferible a describir una CI que no está.

En CI el gate `EO_INTEGRATION=1` **estará siempre activo** en su job, porque los servicios están garantizados. En local es opcional: quien no tenga el daemon de Docker arrancado ejecuta `pnpm run server:test` sin ruido, porque la etiqueta `integration` excluye esos ficheros de la compilación (regla R8).

### 8.2 Diferencias entre PR y ejecución nocturna

**Estado: la ejecución nocturna está diseñada, no configurada.** No existe todavía un workflow programado en `.github/workflows/`. Esta tabla es el contrato de lo que debe contener cuando se cree.

| Comprobación | En cada PR | Nocturna |
|---|---|---|
| `unit`, `contract`, `simulation` con `-race` | Sí (job `game-server`) | Sí |
| `integration`, `recovery` con `-race` | No (coste) | **Sí** |
| Detección de *flakies*: `-count=10 -shuffle=on` | No | **Sí** |
| Informe de cobertura completo por paquete y comparación con los umbrales de §7.1 | Resumen | **Sí**, con histórico |
| Cruce `INV-*` ↔ tests existentes (regla R2) | Sí | Sí |
| Migraciones: `up` completo → `down` completo → `up` completo sobre base vacía | No | **Sí** (`INV-PERSIST-005`) |
| Fuzzing del parser de mensajes WebSocket (`go test -fuzz`) | No | **Sí**, presupuesto acotado |
| Auditoría de dependencias | No | **Sí** |
| **load** (k6, soak de 24 h) | No | **No en MVP**: se activa en su milestone. Ver [load-tests.md](./load-tests.md). |

La lógica del reparto: en el PR va todo lo que da señal en minutos y bloquea un error real. En la nocturna va todo lo que cuesta tiempo o cuya señal es estadística (flakies, deriva de cobertura, fugas).

### 8.3 Qué hacer cuando la nocturna se pone roja

Una nocturna roja no bloquea merges, pero sí abre trabajo con prioridad. Concretamente: un *flaky* detectado por `-count=10` se arregla o se borra en el mismo ciclo (regla R7); una migración que no revierte limpiamente es un bloqueante de despliegue y se trata como incidente, no como deuda.

---

## 9. Definition of Done

Copiada del canon §19 y desarrollada con el criterio de verificación de cada punto. Una unidad de trabajo está terminada solo si cumple **todos**. No existe "casi terminado".

| # | Punto | Cómo se verifica |
|---|---|---|
| 1 | **Spec escrita y actualizada** | Existe o se ha modificado el documento en [../specs/](../specs/) con la plantilla completa del canon §22: Objetivo · Scope y no-scope · Actores · Inputs · Outputs · Reglas de negocio · Estados y transiciones · Errores · Invariantes · Persistencia · Eventos · Contratos de red · Tests esperados. |
| 2 | **Invariantes declarados con ID estable** | Los `INV-*` afectados están en [../invariants/](../invariants/) con su test asociado nombrado. Si el cambio crea un invariante nuevo, tiene ID nuevo, no reutilizado. |
| 3 | **ADR si aplica** | Toda decisión estructural o difícil de revertir tiene su ADR en [../decisions/](../decisions/), con el nombre de fichero completo (`ADR-011-movement-timed-polyline.md`, no `ADR-011.md`). Un ADR no se edita: se supersede. |
| 4 | **Implementación completa** | Sin `TODO` que oculte comportamiento faltante. Lo no implementado devuelve `NOT_IMPLEMENTED` (canon §16) y está marcado como `Fuera de MVP` en la documentación. |
| 5 | **Tests unit** | Dominio puro, `clock.FakeClock` y `RandomSource` inyectados. Ver [unit-tests.md](./unit-tests.md). |
| 6 | **Tests integration** | Contra PostgreSQL y Redis reales, con etiqueta `integration` y `EO_INTEGRATION=1`. Ver [integration-tests.md](./integration-tests.md). |
| 7 | **Tests contract** | Todo mensaje nuevo o modificado valida contra el JSON Schema exportado, en los dos lados (Go y Vitest). Ver [contract-tests.md](./contract-tests.md). |
| 8 | **Errores testeados** | Cada código de error alcanzable tiene un test que lo produce **y** que comprueba la ausencia de efectos secundarios. |
| 9 | **Documentación actualizada** | Spec, glosario, invariantes y, si cambió el protocolo, [../specs/websocket-protocol.md](../specs/websocket-protocol.md). |
| 10 | **Logs y métricas emitidos** | Campos estándar del canon §18 (`ts`, `level`, `msg`, `player_id`, `session_id`, `request_id`, `tick`) y las métricas `eo_*` que corresponda. |
| 11 | **CI en verde** | Los cinco jobs de §8.1 (`docs`, `protocol`, `game-server`, `integration`, `docker`). |
| 12 | **Sin violaciones de invariantes** | Ningún test de invariante en rojo ni desactivado. Una violación de invariante es severidad máxima, no una discusión de diseño. |

Puntos 5, 6 y 7 admiten una excepción documentada y solo una: cuando el cambio no tiene superficie en ese nivel (por ejemplo, un cambio puramente de pathfinding no tiene contrato de red). La excepción se escribe en la descripción del pull request, no se deja implícita.

---

## 10. Documentos relacionados

- [unit-tests.md](./unit-tests.md) — casos concretos de dominio puro, `FakeClock` y `RandomSource`.
- [integration-tests.md](./integration-tests.md) — PostgreSQL y Redis reales, y el test de recuperación.
- [contract-tests.md](./contract-tests.md) — protocolo v1, JSON Schema y deriva de contrato.
- [simulation-tests.md](./simulation-tests.md) — loop determinista y reproducibilidad byte a byte.
- [load-tests.md](./load-tests.md) — carga, diferida al milestone correspondiente.
- [../invariants/README.md](../invariants/README.md) — catálogo de `INV-*` y su test asociado.
- [../operations/local-development.md](../operations/local-development.md) — scripts pnpm y arranque de Docker en Windows.
- [../architecture/game-loop.md](../architecture/game-loop.md) — orden fijo de fases del tick.
- [../architecture/game-server.md](../architecture/game-server.md) — interfaces `Clock`, `RandomSource`, `Pathfinder` y puertos de persistencia.
