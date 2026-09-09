# Game Server (proceso Go)

Arquitectura interna del proceso autoritativo `services/game-server`: paquetes y reglas de dependencia, modelo de concurrencia, propiedad de memoria, ciclo de vida (bootstrap y shutdown) e interfaces de dominio inyectadas.

> Estado: **implementado**. El servicio compila (`go build ./...`), pasa `go vet` y su suite de tests
> unitarios está en verde. Lo que aquí se describe es el código que existe; lo que todavía no existe se
> marca explícitamente como **fuera de MVP** o **TBD**. Los tests de integración contra PostgreSQL y
> Redis reales están escritos y **ambos ejecutados y en verde**: 12 tests contra un cluster PostgreSQL
> local y 9 contra un Redis real en WSL, todos con el detector de carreras activo (§8).
> Documentos relacionados: [game-loop.md](./game-loop.md) · [pathfinding.md](./pathfinding.md) ·
> [specs funcionales](../specs/README.md) · [esquema de base de datos](../database/schema.md) ·
> [invariantes](../invariants/README.md) · [estrategia de tests](../testing/strategy.md) ·
> [configuración](../operations/configuration.md) · [ADRs](../decisions/README.md).

---

## 1. Rol del proceso

`game-server` es un binario Go independiente del frontend Next.js de `apps/web`. La versión **mínima**
de `go.mod` no la elige el proyecto: la imponen las dependencias (`pgx/v5` y `prometheus/client_golang`
declaran `go 1.25.0`) y `go mod tidy` la mantiene. El toolchain de la máquina de desarrollo es Go 1.27.0,
y la CI lee la versión del propio `go.mod` para que no puedan divergir. Es el **único**
componente que puede afirmar verdad sobre el estado del mundo: posiciones, HP, ownership, cooldowns,
ETA y paths. El cliente envía intención; el servidor determina la verdad.

Responsabilidades:

| Responsabilidad | Detalle |
|---|---|
| Simulación autoritativa | Game loop a `EO_TICK_RATE_HZ` (10 Hz, período 100 ms). Ver [game-loop.md](./game-loop.md). |
| Terminación WebSocket | `/ws`, protocolo v1 JSON, autenticación por *game ticket* JWT. |
| Persistencia durable | PostgreSQL como *source of truth*; escritura asíncrona vía cola + workers. |
| Estado caliente | Redis para presencia, idempotencia y consumo de `jti`. |
| Alta de jugador (**provisional**) | `POST /api/auth/register` y `POST /api/auth/login` con bcrypt, servidos por `internal/httpapi`. |
| Observabilidad | `log/slog` en JSON, métricas Prometheus, `GET /health` y `GET /ready`. |

**No** es responsable de: proyección isométrica ni píxeles (cliente), ni renderizado de deltas.

Sobre la autenticación conviene ser exacto, porque es el punto donde el código y la arquitectura
objetivo divergen a propósito: **hoy es el game server quien registra y autentica jugadores**
(`internal/httpapi/auth.go`, con bcrypt y emisión del ticket vía `auth.NewIssuer`). [ADR-010](../decisions/ADR-010-authentication-game-ticket.md)
sitúa esa responsabilidad en Next.js, que emitiría el ticket y dejaría al game server únicamente
verificándolo. La implementación actual es un andamio explícito de MVP, no la arquitectura final.

Módulo Go: `github.com/empires-online/empires-online/services/game-server`.

---

## 2. Estructura de paquetes

Esta es la estructura **real** del servicio, fichero a fichero:

```
services/game-server/
├── go.mod                          # module .../services/game-server; el mínimo lo imponen
│                                   # las dependencias (pgx/v5, client_golang), no el proyecto
├── cmd/
│   ├── server/
│   │   ├── main.go                 # composition root: config, dependencias, señales
│   │   └── hotstate.go             # elige Redis o estado en proceso según EO_REDIS_URL
│   └── migrate/main.go             # migración explícita; viaja en la misma imagen que el servidor
├── migrations/
│   ├── 000001_initial_schema.{up,down}.sql
│   ├── 000002_seed_catalogs.{up,down}.sql
│   └── embed.go                    # package migrations, //go:embed *.sql
└── internal/
    ├── auth/ticket.go              # Verifier, Authenticator, Issuer, Claims
    ├── clock/{clock.go,random.go}  # Clock, SystemClock, FakeClock, RandomSource
    ├── config/{config.go,dotenv.go}  # Config.Load() con validación agregada; carga de .env
    ├── domain/
    │   ├── city/city.go            # City, PresenceState, CanTransition, ShouldEngageProtection
    │   ├── movement/{path.go,movement.go}  # Waypoint, TimedPath, Movement, BuildTimedPath, Validate
    │   ├── player/player.go        # Player, Civilization (Traits), Faction
    │   └── unit/unit.go            # Unit, Status, Type, Definition y catálogo
    ├── game/
    │   ├── founding/site.go        # FindSite: emplazamiento determinista de ciudad
    │   ├── loop/loop.go            # Loop.Run + Loop.Step (8 fases)
    │   ├── simulation/             # state, simulation, commands, recovery,
    │   │                           # snapshot, interest, introduce
    │   └── world/{tile.go,world.go,generator.go}
    ├── httpapi/auth.go             # POST /api/auth/register y /api/auth/login (MVP)
    ├── observability/{logging.go,metrics.go,health.go}
    ├── pathfinding/{pathfinding.go,astar.go}
    ├── persistence/{gamestore.go,queue.go,pgxalias.go}
    ├── persistence/memory/memory.go # estado caliente en proceso: alternativa a Redis
    │                               # SÓLO en desarrollo (ver ADR-004)
    ├── persistence/postgres/       # store, db, migrate, players, cities, units,
    │                               # movements, world, bootstrap, events
    ├── persistence/redis/{redis.go,presence.go,idempotency.go}
    ├── protocol/{protocol.go,codes.go,schema.go}
    │   └── schema/v1/*.json        # espejo generado por `pnpm run protocol:build` (go:embed)
    └── websocket/{hub.go,session.go,server.go}
```

Tres ausencias que conviene declarar, porque diseños anteriores las daban por hechas:

- **No hay `internal/events`.** Los hechos no se modelan como structs de evento intermedios: la
  simulación emite directamente mensajes de protocolo por el `Broadcaster`, y lo que se persiste como
  historial va a la tabla `world_events` desde `persistence/postgres/events.go`.
- **No hay `internal/presence` ni `internal/domain/territory` / `internal/domain/diplomacy`.** La
  máquina de presencia vive en `internal/domain/city` (`PresenceState`, `CanTransition`,
  `ShouldEngageProtection`) y la conduce el game loop; territorio y diplomacia existen hoy solo como
  tablas (`territories`, `territory_control`, `safe_zones`, `treaties`, `garrisons`), sin paquete de
  dominio. Ver §9.
- **No hay directorio `testdata/`.** Los tests construyen sus mundos en el propio fichero de test
  (mapas ASCII en `pathfinding`, mundos sintéticos en `simulation`), sin ficheros externos.

Y una presencia que no estaba prevista: **`internal/protocol`** es un paquete de primer nivel, no una
parte de `websocket`. Contiene los tipos de envelope, el catálogo cerrado de códigos de error y los
JSON Schema embebidos que espejan `packages/protocol`, de modo que un contract test en Go puede
comprobar que las dos mitades del protocolo no han divergido.

### 2.1 Responsabilidad y dependencias por paquete

`stdlib` está permitido en todos. "Prohibido" significa **import ilegal**; hoy la regla se sostiene por
revisión de código, no por un test de arquitectura automático — añadirlo está pendiente.

| Paquete | Responsabilidad exacta | Puede importar | Prohibido importar |
|---|---|---|---|
| `cmd/server` | Composition root: lee config, construye todas las dependencias concretas, las inyecta y arranca/para el proceso. Cero lógica de negocio. | todo `internal/*` | — |
| `internal/config` | Parseo y validación de las variables `EO_*` a una struct inmutable. Aplica valores por defecto y **reporta todos los errores juntos**. Falla rápido si falta un secreto. | — | todo `internal/*` |
| `internal/clock` | `Clock` (`Now`, `NowMs`), `SystemClock`, `FakeClock` y `RandomSource` (`Int63n`, `Float64`) con PRNG PCG sembrado. Es la única fuente de tiempo y azar autorizada. | — | todo `internal/*` |
| `internal/observability` | `*slog.Logger` JSON con campos estándar, registro de métricas Prometheus y `Health` con los handlers `/health` y `/ready`. | — | `domain/*`, `game/*`, `persistence/*`, `websocket` |
| `internal/protocol` | Envelope v1 (entrante `{v,type,requestId,payload}` / saliente `{v,type,seq,ts,requestId?,payload}`), tipos de payload, catálogo cerrado de 22 códigos de error, códigos de cierre 44xx/4500 y los JSON Schema embebidos. **No importa nada de `internal/*`**: es el paquete hoja del que dependen los demás. | — | todo `internal/*` |
| `internal/game/world` | Grid `EO_WORLD_WIDTH`×`EO_WORLD_HEIGHT`, `TerrainType`, *blocked overlay* bajo `sync.RWMutex`, chunks, geometría, `Tile`, generador determinista desde la semilla. No conoce entidades de dominio. | — | `domain/*`, `loop`, `simulation`, `persistence/*`, `websocket`, `pathfinding` |
| `internal/game/loop` | El tick: `Run` (calendario absoluto, descarte de ticks perdidos) y `Step` (las 8 fases). Drena la cola de comandos, aísla pánicos por comando y vuelca métricas. Recibe sus parámetros en un `loop.Config` propio, **no** importa `internal/config`. | `simulation`, `clock`, `observability` | `config`, `persistence/*`, `websocket` |
| `internal/game/simulation` | **El corazón del MVP**, no un reservado: estado del mundo (`state.go`), aplicación de comandos (`commands.go`, `simulation.go`), hidratación y recuperación (`recovery.go`), snapshots (`snapshot.go`), interest management (`interest.go`) y alta de jugador en caliente (`introduce.go`). La resolución de combate (fase 4) sí sigue fuera de MVP. | `world`, `domain/*`, `pathfinding`, `protocol`, `clock`, `observability` | `persistence/postgres`, `persistence/redis`, `websocket` |
| `internal/game/founding` | `FindSite`: emplazamiento determinista de una ciudad nueva por búsqueda en espiral desde una semilla derivada del nombre de usuario. | `world` | `domain/*`, `persistence/*`, `websocket` |
| `internal/domain/player` | Identidad del jugador, `Civilization` con sus `Traits`, `Faction`. | — | `game/*`, `persistence/*`, `websocket`, `pathfinding` |
| `internal/domain/city` | Ciudad, `PresenceState` y su autómata (`CanTransition`), `ShouldEngageProtection`, límite de población. | — | ídem |
| `internal/domain/unit` | Entidad unidad, `Status` (`IDLE`, `MOVING`, `GARRISONED`, `HIDDEN`, `DEAD`), `Type`, `Definition` y el catálogo con `VILLAGER` (`baseMsPerTile` 600). | — | ídem |
| `internal/domain/movement` | Polilínea temporizada: `Waypoint`, `TimedPath`, `BuildTimedPath`, `StepDurationMs`, `PositionAt`, `IndexAt`, `Validate`, y el ciclo `ACTIVE`/`COMPLETED`/`CANCELLED`/`FAILED`. | `world` | ídem |
| `internal/pathfinding` | A\* octile con costes `u×1000` / `u×1414`, heurística ponderada por `MinTerrainCostUnits`, desempate `(f, h, y, x)` y límites `EO_PATHFINDING_MAX_NODES` / `EO_PATHFINDING_MAX_DISTANCE`. Ver [pathfinding.md](./pathfinding.md). | `world` | `domain/*`, `game/loop`, `simulation`, `persistence/*`, `websocket`, `observability` |
| `internal/auth` | `Verifier` del game ticket HS256 (`exp`, `aud`), `Claims`, `Authenticator` (verifica y consume el `jti` contra Redis) e `Issuer` para el alta provisional. | `clock` | `domain/*`, `game/*`, `persistence/*` |
| `internal/httpapi` | `POST /api/auth/register` y `POST /api/auth/login`: bcrypt, alta transaccional del jugador y emisión del ticket. **Provisional**, ver §1. | `auth`, `founding`, `persistence/postgres`, `simulation`, `world` | `websocket` |
| `internal/websocket` | `Hub` (registro de sesiones e índices por jugador y por chunk), `Session` (writer único, `seq`, cola de salida, memoria de terreno enviado) y `Server` (upgrade, handshake, rate limit, traducción mensaje→comando). Declara sus propios puertos `PresenceTracker` y `Deduper`, que Redis satisface sin ser importado. | `auth`, `protocol`, `simulation`, `world`, `observability` | `persistence/*`, `pathfinding` |
| `internal/persistence` | Contratos y mecanismos transversales: `GameStore` (fachada de repositorios), `Queue` (cola de escrituras durables con workers y reintentos) y `pgxalias`. | `simulation`, `persistence/postgres`, `observability` | `websocket`, `loop` |
| `internal/persistence/postgres` | Pool `pgx`, migraciones embebidas con golang-migrate, repositorios (`players`, `cities`, `units`, `movements`, `world`, `events`) y el `Bootstrapper` del alta atómica. | `domain/*`, `world`, `migrations`, `observability` | `loop`, `simulation`, `websocket`, `pathfinding` |
| `internal/persistence/redis` | Presencia, idempotencia (`Claim` con `SETNX`) y consumo de `jti` de tickets. | `observability` | `loop`, `simulation`, `websocket` |

### 2.2 Regla de dependencias (una sola dirección)

```
              cmd/server  (composition root: conoce a todos)
                   │  construye e inyecta
   ┌───────────────┼───────────────┬──────────────────────┐
   │               │               │                      │
websocket   httpapi│              loop          persistence ─► postgres
   │               │               │                      └─► redis
   └───────────────┴──────────► simulation ──► pathfinding
                                   │  │
                                domain/*  └──► world
                                   │
        protocol      clock      config      observability   (paquetes hoja)
```

Cuatro reglas que no se negocian:

1. **El dominio no conoce infraestructura.** `internal/domain/*` no importa `persistence`, `websocket`
   ni `pathfinding`; los puertos los declara quien los consume (`simulation.Deps`,
   `websocket.PresenceTracker`, `websocket.Deduper`).
2. **`game/world` no importa `domain`.** Contiene tipos de valor geométricos (`Tile`, `TerrainType`,
   `ChunkCoord`) que el dominio sí usa. Así se evita el ciclo `world ↔ domain`.
3. **`simulation` no importa implementaciones concretas de I/O.** Habla con Postgres, Redis y las
   conexiones exclusivamente a través de las interfaces de `simulation.Deps` (`Broadcaster`,
   `Persister`, `Repos`), cableadas en `cmd/server`.
4. **`loop` no toca el mundo directamente.** Solo llama a métodos de `simulation`; toda mutación del
   estado vive en un único paquete.

---

## 3. Modelo de concurrencia

### 3.1 Mapa de goroutines

```
                          ┌──────────────────────────────────────┐
   WS conn #1  ─ readPump─┤  commands chan simulation.Command    │
              ─ writePump─┤  (buffer 8192)                       │
   WS conn #2  ─ readPump─┼─────────────────────────────────────►│  GAME LOOP
              ─ writePump─┤                                      │  (1 goroutine)
      ...                 │                                      │  owner del mundo
                          │                                      │
        Hub  ◄────────────┴── llamada DIRECTA, en la goroutine ──┤
     (fan-out por chunk)      del loop: BroadcastChunk /         │
              │               SendToPlayer                       │
              ▼                                                  │
      session.out (chan, EO_WS_OUTBOUND_QUEUE_SIZE) ──► writePump│
                                                                 │
                       Queue.Submit(Job) ──────────────────────► │ (no bloqueante)
                                    │
                          ┌─────────┴──────────┐
                          │ persistence workers│ ──► PostgreSQL
                          │  (4, sin sharding) │
                          └────────────────────┘
   metrics HTTP server (goroutine propia, EO_METRICS_ADDR :9090)
   HTTP/WS server      (goroutine propia, EO_HTTP_ADDR    :8080)
```

| Goroutine | Cardinalidad | Posee | Bloquea en |
|---|---|---|---|
| Game loop | **1** | Todo el estado mutable del mundo (`simulation.State`) | Su `time.Timer` de tick y el drenado no bloqueante del canal de comandos |
| WS `readPump` | 1 por conexión | Buffer de lectura, *token bucket* de esa conexión | `conn.ReadMessage()` con deadline `WSReadTimeout` (45 s) |
| WS `writePump` | 1 por conexión | El lado de escritura del socket, contador `seq` | El canal `out` y el ticker de ping (`WSPingInterval`, 15 s) |
| Persistence worker | **4**, fijas, sin sharding | Su conexión del pool `pgx` | El canal `jobs` y la red hacia Postgres |
| HTTP/WS server | 1 (+ las que genera `net/http`) | Listener | `ListenAndServe()` |
| Metrics server | 1 | Listener `EO_METRICS_ADDR` | `ListenAndServe()` |

**El `Hub` no es una goroutine.** Es una estructura con `sync.RWMutex` que mantiene tres índices
(`sessions`, `byPlayer`, `byChunk`) y cuyos métodos `BroadcastChunk` y `SendToPlayer` se invocan
**desde la goroutine del loop**, de forma síncrona. No hay canal de broadcast intermedio. El fan-out es
barato porque termina en `Session.Send`, que solo encola en el canal `out` de cada sesión y **nunca
bloquea**: un cliente lento no puede frenar el tick, se cierra su sesión (§3.3).

Regla de escritura del socket: **un único writer por conexión**. `gorilla/websocket` no admite
escrituras concurrentes; el `seq` monótono por conexión (`atomic.Uint64` estampado en `Send`) y la
escritura efectiva la hace exclusivamente `writePump`.

### 3.2 Canal de comandos y backpressure

El canal `commands` tiene **buffer finito de 8192**, fijado por la constante `commandQueueSize` de
`cmd/server/main.go`. No es una variable `EO_*`: es un parámetro de proceso, no de gameplay, y
exponerlo queda **TBD (fuera de MVP)**. Está dimensionado para absorber varios ticks de tráfico
admisible (`EO_WS_RATE_LIMIT_PER_SECOND` × conexiones × período de tick). El comportamiento:

- **El productor nunca bloquea.** `Server.dispatch` envía con `select`/`default`.
- **El loop nunca bloquea.** `drainCommands` drena con `select`/`default` y como mucho
  `MaxCommandsPerTick` (1024) comandos por tick; el resto espera al siguiente.
- **Cola llena ⇒ descarte con registro**, nunca espera. `dispatch` devuelve `false` y escribe un
  `level=error` en el log.

```go
// internal/websocket/server.go — productor, en la goroutine readPump de la conexión.
func (s *Server) dispatch(cmd simulation.Command) bool {
	select {
	case s.commands <- cmd:
		return true
	default:
		s.log.Error("cola de comandos saturada; comando descartado")
		return false
	}
}
```

Asimetría real y documentada: la ruta de `session.view` **sí** convierte ese `false` en un
`system.error` con código `INTERNAL_ERROR` (`"el servidor está saturado"`), mientras que `unit.move` y
`unit.cancel_move` ignoran hoy el valor de retorno, de modo que el comando se descarta y el cliente no
recibe rechazo. Es una carencia conocida, no un diseño: unificar la respuesta a `INTERNAL_ERROR` en las
tres rutas está pendiente.

Hay dos niveles de control de flujo, en este orden:

1. **Por conexión**: *token bucket* de `EO_WS_RATE_LIMIT_PER_SECOND` (20) con burst
   `EO_WS_RATE_LIMIT_BURST` (40). Al agotarse: `system.error` con `RATE_LIMITED` **y cierre inmediato
   con 4429** — no hay tolerancia progresiva.
2. **Global**: la saturación del canal de comandos, descrita arriba.

Un jugador educado nunca alcanza el nivel 2; alcanzarlo indica sobrecarga del servidor.

### 3.3 Salida: fan-out y consumidores lentos

El loop no escribe en sockets. La simulación emite los hechos según ocurren llamando a
`Hub.BroadcastChunk` (a todas las sesiones suscritas a ese chunk) o `Hub.SendToPlayer` (a todas las
sesiones de un jugador), y ambas terminan en `Session.Send`, que encola en el canal `out` de la sesión.

Si el canal `out` está lleno, el jugador no consume al ritmo del servidor: se **cierra la conexión con
`4500`** (`CloseInternalError`, "cola de salida saturada") en lugar de degradar el tick o mezclar
deltas inconsistentes. Su capacidad es `EO_WS_OUTBOUND_QUEUE_SIZE` (256 por defecto, rango 8–65536).
Al reconectar, el cliente recibe un `world.snapshot` completo de su área de interés, por lo que la
pérdida es recuperable por diseño.

Detalle de ancho de banda que vive en la sesión: el terreno de un chunk se envía **una sola vez por
sesión**. `Session.NeedsTerrain` / `MarkTerrainSent` recuerdan qué chunks ya lo recibieron, porque el
terreno es inmutable y reenviarlo en cada movimiento de cámara sería malgastar la conexión.

### 3.4 Cola de persistencia

Hay **una sola cola** (`persistence.Queue`), no dos carriles, con `DefaultQueueConfig()`: capacidad
4096, **4 workers**, hasta **3 intentos** por trabajo y 200 ms de espera entre reintentos. Los workers
se arrancan con `queue.Start(ctx, 4)`.

| Vía | Contenido | Semántica |
|---|---|---|
| `Queue.Submit(Job)` | Alta de jugador/ciudad/unidades, inicio, fin y cancelación de movimiento, transiciones de presencia, `world_events` | Escritura durable diferida, cada trabajo en su propia transacción |
| `simulation.FlushDirty()` | Posiciones consolidadas de unidades marcadas *dirty* | Lote cada `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` (50 ticks = 5 s), encolado también como `Job` |

"Escritura durable **diferida**" significa *fuera del tick, en su propia transacción*: el tick jamás
ejecuta I/O de Postgres.

- **No hay sharding por entidad.** Los cuatro workers consumen el mismo canal, así que entre trabajos
  de la misma unidad no hay garantía de orden más allá del orden de encolado y de que cada trabajo es
  atómico. En el MVP los trabajos de una unidad son escasos y autocontenidos; si eso deja de ser
  cierto, el sharding por `hash(entityID) % N` es la evolución natural.
- **Orden real: se muta en RAM y DESPUÉS se encola.** `applyMove` aplica el movimiento al estado y
  luego llama a `Persister.Submit`. La ventana de riesgo es honesta y hay que documentarla como tal:
  si el proceso muere entre la aceptación y el COMMIT (típicamente pocas decenas de ms), ese
  movimiento se pierde y la unidad queda en su última posición consolidada. Es un RPO conocido, no un
  descuido.
- **Cola llena**: el trabajo se **descarta**, se registra `level=error` con el nombre del trabajo y la
  capacidad, y se invoca su `OnPermanentFailure` como compensación. Es una condición crítica, no un
  caso normal; el síntoma se ve en `eo_persistence_queue_depth`.
- **Fallo del worker**: hasta 3 intentos con espera entre ellos; agotados, se registra `level=error` y
  se ejecuta `OnPermanentFailure` (por ejemplo, detener la unidad cuyo movimiento la base de datos
  nunca conoció).
- **El contexto de la escritura no hereda la cancelación del proceso** (`context.WithoutCancel` más un
  timeout de 10 s): si el servidor se está apagando, una escritura ya empezada debe terminar, no
  abortarse a medias.
- **Idempotencia**: la primera línea de defensa es Redis (`Claim` con `SETNX` sobre el `requestId`,
  TTL 300 s) antes de ejecutar el comando. Si Redis no responde, **el comando se ejecuta igualmente**:
  se prefiere jugar a bloquear al jugador, y así está decidido a propósito.

---

## 4. Propiedad de memoria

### 4.1 Reglas

1. **Single writer.** El estado mutable del mundo —registros de entidades, movimientos activos,
   `tick`— pertenece a la goroutine del loop, dentro de `simulation.State`. Ninguna otra goroutine
   posee un puntero a esas estructuras.
2. **Lo que sale del loop sale por valor.** Lo que se emite son payloads de protocolo ya construidos
   (`protocol.UnitMovementStartedPayload`, etc.), no punteros a entidades. Nunca se publica un
   `*unit.Unit` hacia el hub o hacia un worker.
3. **Lo que entra al loop entra por valor.** Un `simulation.Command` es un struct autocontenido con
   IDs y datos primitivos, más el `RequestID` y el `SessionID` para responder.
4. **Nadie lee el mundo desde fuera del loop, ni siquiera para un snapshot.** La goroutine de la
   conexión **no** lee el estado: envía un comando `RequestSnapshot` con un canal de respuesta con
   buffer 1 y espera (hasta 2 s) a que el loop se lo devuelva. Esto es exactamente lo que impide una
   carrera de datos contra la simulación, y es la razón de que `snapshot.go` viva en `simulation` y no
   en `websocket`.
5. **Prohibido el estado global mutable**: nada de `var World *world.State`, `sync.Map` de entidades,
   ni `init()` que construya dependencias. La configuración es inmutable tras el bootstrap.
6. **Excepciones acotadas y justificadas**:
   - contadores de métricas (propiedad de `observability`, thread-safe por contrato de Prometheus);
   - los índices de sesiones del hub (`sync.RWMutex` sobre mapas que **no** son estado de simulación);
   - `Session.seq` y `Session.closed` (`atomic`), y `Session.terrainSent` (`sync.Mutex`);
   - el **blocked overlay** de `world.World`, protegido con `sync.RWMutex` porque el alta de un jugador
     lo modifica desde la goroutine HTTP (`SetBlocked` al fundar la ciudad) antes de que el comando
     `IntroducePlayer` llegue al loop;
   - el pool `pgx` (thread-safe por contrato).

### 4.2 Por qué

- **Determinismo.** El resultado de un tick debe depender sólo del tick y de la secuencia de comandos
  drenados. Con múltiples escritores el orden de aplicación depende del planificador de Go y el mismo
  escenario deja de reproducirse; los *simulation tests* con `FakeClock` perderían sentido. Hay un test
  de reproducibilidad en `internal/game/simulation` que lo comprueba.
- **Ausencia de deadlocks.** Sin locks sobre el estado de simulación no hay jerarquía de adquisición
  que respetar ni inversión de orden que auditar.
- **Coste.** A 10 Hz y 512×512 tiles el trabajo por tick cabe holgadamente en una goroutine; pagar
  contención de mutex por paralelismo que no se necesita sería regresivo.
- **Razonamiento local.** Toda mutación del mundo está en un solo paquete y en un solo hilo de
  ejecución: revisar invariantes es leer `internal/game/simulation`, no auditar N goroutines.

Complemento del single writer: **aislamiento de pánicos**. `Loop.applyCommand` recupera el pánico de un
comando concreto, de modo que un bug en una regla no tumba la simulación del mundo entero.

Verificación: `go test -race ./...`, más *simulation tests* que ejecutan el mismo escenario dos veces y
comparan el estado final.

---

## 5. Ciclo de vida del proceso

### 5.1 Bootstrap (orden estricto)

Cada paso falla rápido: si un paso no puede completarse, el proceso registra el error y sale con código
distinto de cero. Nunca se arranca a medias.

```
 0. config        config.Load() lee y valida EO_*. Aplica defaults y reporta TODOS los errores
                  juntos. Falla si falta EO_POSTGRES_URL, EO_REDIS_URL o EO_AUTH_JWT_SECRET.
                  Los secretos jamás se registran en logs.
    logger        slog JSON con nivel EO_LOG_LEVEL, instalado también como slog.Default().
    metrics       Registro Prometheus propio (no el global).
    signals       signal.NotifyContext con os.Interrupt y SIGTERM: el rootCtx del proceso.
 1. migrations    postgres.Migrate aplica migrations/*.up.sql embebidas con golang-migrate.
                  Ocurre ANTES de abrir el pool de la aplicación, con su propia conexión.
 2. postgres      postgres.New: pool pgx contra EO_POSTGRES_URL.
    redis         redisstore.New contra EO_REDIS_URL.
    repos         playerRepo, cityRepo, unitRepo, movementRepo, worldRepo, bootstrapper.
    clock         clock.NewSystemClock().
 3. world         loadWorld: world_state se lee o se inicializa (LoadOrInit). El terreno se
                  GENERA SIEMPRE desde la semilla con world.Generate; world_chunks es una
                  copia de auditoría, no la fuente primaria. Si el mundo es nuevo, se
                  persisten sus chunks.
 4. hidratación   Se cargan unidades vivas, ciudades y movimientos ACTIVE, y simulation.Hydrate
                  reconstruye el estado:
                    movimiento vencido durante la caída -> la unidad aparece en el destino y el
                                                           movimiento se cierra como COMPLETED;
                    movimiento en curso                 -> se reanuda desde la polilínea;
                    polilínea inválida                  -> FAILED y la unidad se queda donde
                                                           estaba (nunca se teletransporta a
                                                           nadie por un dato dudoso).
                  Después se rebloquean las murallas 3x3 de las ciudades existentes: la capa de
                  ocupación no se persiste, se deriva. Y se cierran en base de datos los
                  movimientos recuperados como COMPLETED.
 5. simulación    persistence.NewQueue + queue.Start(rootCtx, 4); observability.NewHealth con
                  umbral de 5 s y los checkers de postgres y redis; ws.NewHub; el canal de
                  comandos (8192); simulation.New(...) y loop.New(...).
 6. transporte    auth.NewAuthenticator (verifier + ticket store de Redis), presence store,
                  idempotency store, ws.NewServer, httpapi.NewAuthAPI (que lee el catálogo de
                  eras y toma la primera como era inicial) y el mux HTTP:
                  /health, /ready, /ws, /api/auth/register, /api/auth/login.
 7. arranque      Tres goroutines: servidor de juego en EO_HTTP_ADDR, servidor de métricas en
                  EO_METRICS_ADDR y gameLoop.Run(rootCtx). El primer tick continúa el
                  CurrentTick cargado de world_state y el tiempo se ancla a EpochMs.
```

Cada paso falla rápido: si un paso no puede completarse, `run()` devuelve el error, `main` lo escribe
en stderr y el proceso sale con código 1. Nunca se arranca a medias.

`GET /health` es *liveness* sin dependencias (el proceso responde). `GET /ready` es *readiness*: exige
Postgres alcanzable, Redis alcanzable y **loop vivo**, definido como "el loop latió hace menos de
**5 s**" (`observability.NewHealth(5*time.Second, ...)`, alimentado por `health.BeatLoop` al final de
cada tick).

Dos detalles de orden que importan y que el código fija a propósito:

- **Las migraciones van antes que el pool de la aplicación.** Se aplican con una conexión propia y la
  URL reescrita al esquema `pgx5` que espera golang-migrate.
- **El alta de un jugador es primero PostgreSQL y después RAM**: `httpapi` ejecuta la transacción
  atómica (jugador + ciudad + 3 aldeanos) y solo entonces encola el comando `IntroducePlayer` que lo
  incorpora al mundo en memoria. Al revés existiría un jugador en el mundo que la base de datos no
  conoce.

### 5.2 Shutdown ordenado

Disparado por `SIGINT`/`SIGTERM` (y por `docker compose down`). El objetivo es **no perder escrituras
durables** y dejar el mundo en un estado reanudable.

```
 1. señal               rootCtx se cancela. Esa misma cancelación detiene gameLoop.Run.
 2. contexto de apagado context.WithTimeout de 25 s (constante shutdownTimeout de main.go),
                        DESACOPLADO de rootCtx para que el apagado no se cancele a sí mismo.
 3. drenaje de entrada  gameHTTP.Shutdown y metricsHTTP.Shutdown: se dejan de aceptar peticiones
                        y conexiones nuevas.
 4. cierre de sesiones  hub.CloseAll(1001, "servidor en mantenimiento"). Se usa el código
                        estándar 1001 ("going away"); los 44xx quedan reservados a errores.
 5. flush de persistencia queue.Drain(15 s): se espera a que la cola se vacíe. Si no se vacía a
                        tiempo se registra level=error con la profundidad pendiente.
 6. tick final          worldRepo.SaveTick(gameLoop.Tick()) persiste el tick alcanzado en
                        world_state. Si falla se registra level=warn: es una molestia, no una
                        pérdida de datos.
```

Presupuesto: 25 s de techo total y 15 s para vaciar la cola de persistencia, ambos constantes de
`cmd/server/main.go`, no variables `EO_*`. El techo absoluto de reloj de pared lo impone además el
orquestador (`stop_grace_period`) y es un parámetro de despliegue: **TBD (fuera de MVP)**. Si una fase
agota su límite se registra y se continúa con la siguiente; el proceso **nunca** se queda colgado
esperando.

Nótese lo que **no** hay, para no prometerlo: no existe un "drenaje de comandos" con ticks extra ni un
tick final que fuerce el flush de los *dirty flags*. El loop simplemente para cuando su contexto se
cancela. Esto es aceptable porque la propiedad de recuperación no depende del apagado ordenado: un
movimiento `ACTIVE` en Postgres con su polilínea completa siempre puede reanudarse tras el reinicio
(§5.1 paso 4), incluso si el apagado fue abrupto (`SIGKILL`). El shutdown ordenado es una optimización
de limpieza, no un requisito de corrección. Lo que sí se pierde en un apagado abrupto son las
posiciones consolidadas desde el último flush y las escrituras encoladas sin COMMIT, que es el RPO
documentado en §3.4. Ver [../testing/integration-tests.md](../testing/integration-tests.md).

---

## 6. Interfaces clave

Todas se declaran del lado del **consumidor** y se implementan en infraestructura. Ninguna se resuelve
por variable global: se pasan por constructor desde `cmd/server`.

```go
// internal/clock — abstracciones de entorno. Nada de time.Now() ni math/rand en el dominio.
type Clock interface {
	Now() time.Time
	NowMs() int64 // epoch milliseconds; es la unidad aritmética de la simulación
}

type RandomSource interface {
	Int63n(n int64) int64
	Float64() float64
}

// internal/pathfinding — puerto de pathfinding. El Grid viaja POR CONSULTA.
type Pathfinder interface {
	FindPath(ctx context.Context, grid Grid, from, to world.Tile, opts Options) ([]world.Tile, error)
}

// internal/game/simulation — todo lo que la simulación necesita del exterior.
type Broadcaster interface {
	BroadcastChunk(cx, cy int32, msgType string, payload any)
	SendToPlayer(playerID uuid.UUID, msgType, requestID string, payload any)
}

type Persister interface {
	Submit(job Job) // NUNCA bloquea
	Depth() int
}

type Repositories interface {
	PersistMovementStart(ctx context.Context, m *movement.Movement) error
	PersistMovementFinish(ctx context.Context, movementID int64, status movement.Status) error
	PersistUnitPositions(ctx context.Context, units []*unit.Unit) error
	PersistCityPresence(ctx context.Context, cityID int64, state city.PresenceState, at time.Time) error
}
```

| Interfaz | Implementación de producción | Implementación de test | Por qué existe |
|---|---|---|---|
| `Clock` | `clock.SystemClock` | `clock.FakeClock` (avance manual, seguro concurrentemente) | Sin ella los *simulation tests* no son deterministas ni instantáneos. Ver [game-loop.md](./game-loop.md). |
| `RandomSource` | `clock.NewSeededRandom` (PCG sembrado) | La misma, con semilla fija | Misma semilla ⇒ misma secuencia. El generador del mapa no la usa: `world.Generate` es determinista por sí mismo a partir de `EO_WORLD_SEED`. |
| `Pathfinder` | `pathfinding.AStar` | `AStar` real, no un mock: es barato y determinista | Permite sustituir por Hierarchical A\* sin tocar protocolo ni dominio. |
| `Broadcaster` | `websocket.Hub` | `simulation.NoopBroadcaster` | Deja probar la simulación sin sockets. |
| `Persister` | `persistence.Queue` | `simulation.NoopPersister` | Es la frontera que hace cumplible "el tick nunca hace I/O". |
| `Repositories` | `persistence.GameStore` sobre `persistence/postgres` | Fakes en memoria | Aíslan la simulación de SQL y permiten tests sin Docker. |

Sobre `Job`, que es la unidad de trabajo durable:

```go
type Job struct {
	Name               string                     // identifica el trabajo en logs
	Run                func(ctx context.Context) error // DEBE ser idempotente: puede reintentarse
	OnPermanentFailure func(err error)            // compensa lo que no se pudo escribir en disco
}
```

`OnPermanentFailure` es la pieza que cierra el círculo del RPO de §3.4: si una escritura falla de forma
definitiva, el mundo en RAM no puede quedarse con un hecho que la base de datos nunca conoció.

La exclusividad del movimiento activo por unidad **no** se sostiene con una transacción cuidadosa desde
Go, sino en el esquema, con un índice único parcial: `unit_movements_one_active_per_unit` sobre
`(unit_id) WHERE status = 'ACTIVE'`. En RAM se preserva cancelando el movimiento anterior **antes** de
registrar el nuevo, de modo que en ningún instante existen dos activos de la misma unidad.

Reglas de inyección:

- **Constructor injection, sin framework de DI y sin `init()`.** El único lugar que conoce tipos
  concretos es `cmd/server`.
- Cada componente recibe una struct explícita (`simulation.Deps`, `loop.Config`, `ws.Config`,
  `httpapi.Options`); añadir una dependencia obliga a tocar el composition root, lo que hace visible el
  acoplamiento en la revisión de código.
- `context.Context` se propaga siempre como primer parámetro en operaciones de I/O; **nunca** se usa
  para transportar dependencias.
- La simulación recibe un `Persister` (`Submit`, no bloqueante), no repositorios directos: así es
  imposible que un handler del tick llame a Postgres por accidente. Hoy esa regla la garantiza la
  revisión de código; un test de arquitectura que la verifique está pendiente.

---

## 7. Configuración que gobierna este proceso

Todo pasa por `internal/config`; ningún valor de gameplay se hardcodea en más de un lugar.

| Variable | Default (rango) | Efecto en el proceso |
|---|---|---|
| `EO_ENV`, `EO_LOG_LEVEL` | `development` / `info` | Nivel de log y estrictez de validación del secreto. |
| `EO_HTTP_ADDR` | `:8080` | Listener de `/health`, `/ready`, `/ws`, `/api/auth/*`. |
| `EO_METRICS_ADDR` | `:9090` | Listener de `/metrics`. |
| `EO_POSTGRES_URL`, `EO_REDIS_URL` | — | Obligatorias. Migraciones y pools del bootstrap. |
| `EO_AUTH_JWT_SECRET` | — | Verificación HS256 del game ticket. Obligatoria, ≥ 32 caracteres; nunca en el repositorio. |
| `EO_TICK_RATE_HZ` | 10 (1–1000) | Período del loop (100 ms). Debe dividir exactamente a 1000. |
| `EO_WORLD_WIDTH` / `_HEIGHT` | 512 / 512 (16–65536) | Dimensiones del grid. Múltiplos exactos de `EO_CHUNK_SIZE`. |
| `EO_WORLD_SEED` | 20260909 | Semilla del generador determinista del mapa. |
| `EO_CHUNK_SIZE` | 32 (1–256) | Lado del chunk. |
| `EO_INTEREST_RADIUS_CHUNKS` | 2 (0–64) | Radio de suscripción por chunk del interest management. |
| `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` | 50 (1–100000) | Cadencia del volcado por lotes de posiciones (5 s a 10 Hz). |
| `EO_PATHFINDING_MAX_NODES` | 20000 (100–10 000 000) | Cota de CPU del A\* dentro del tick. |
| `EO_PATHFINDING_MAX_DISTANCE` | 256 (1–65536) | Distancia de Chebyshev máxima admitida por consulta. |
| `EO_WS_MAX_MESSAGE_BYTES` | 16384 (256–4 194 304) | Tamaño máximo de un mensaje entrante. |
| `EO_WS_RATE_LIMIT_PER_SECOND` / `_BURST` | 20 / 40 | *Token bucket* por conexión. El burst no puede ser menor que la tasa. |
| `EO_WS_OUTBOUND_QUEUE_SIZE` | 256 (8–65536) | Capacidad de la cola de salida por conexión (§3.3). |
| `EO_PRESENCE_TTL_SECONDS` / `_HEARTBEAT_SECONDS` | 30 / 10 | Margen de reconexión y latido. El heartbeat debe ser **estrictamente menor** que el TTL. |
| `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` | 300 (0–86400) | Transición `OFFLINE_PENDING → PROTECTED`. |

Constantes de tiempo del WebSocket que **no** son configurables por entorno, fijadas en
`internal/config`: `WSHandshakeTimeout = 5s`, `WSPingInterval = 15s`, `WSReadTimeout = 45s`,
`WSWriteTimeout = 10s`.

`config.Load()` reporta **todos** los errores de validación juntos, no de uno en uno: arrancar tres
veces seguidas para descubrir tres variables mal puestas es una pérdida de tiempo evitable.

---

## 8. Entorno de desarrollo

- Windows 10, **Node v22.17.1**, **pnpm 10.25.0**, **git 2.38.1**. Task runner: **pnpm scripts**
  (`pnpm run pg:start`, `pnpm run server:test`). **No hay Makefile**: `make` no está instalado.
- **Go 1.27.0 instalado** en `C:\Program Files\Go` (vía `winget install --id GoLang.Go`). `go.mod`
  declara **`go 1.25.11`**, y no por elección: lo imponen las dependencias —`pgx/v5` y
  `prometheus/client_golang` declaran `go 1.25.0`— de modo que bajar la directiva a mano no funciona,
  `go mod tidy` la restaura. Por eso la CI lee la versión con `go-version-file` en lugar de fijarla:
  duplicar ese número en dos sitios garantiza que un día diverjan, y ya ocurrió una vez.
- **Docker no se usa** en esta máquina: produce pantallazos azules por consumo de RAM (y el daemon
  tampoco llegó a arrancar). La infraestructura local es un cluster PostgreSQL propio en `.pgdata/`
  (puerto 5433) y Redis dentro de WSL.
- **WSL: Ubuntu 22.04.1 (WSL 1)** con `gcc 11.4.0` y Go 1.27.0 en `$HOME/golang`. Es donde se ejecutan
  las dos comprobaciones que Windows no puede hacer: el detector de carreras —`-race` necesita cgo— y
  los tests de integración contra un Redis real. Desde WSL 1 `localhost:5433` alcanza el PostgreSQL de
  Windows, así que la suite completa corre desde allí.
- **Los tests de integración están ejecutados y en verde**: 12 contra PostgreSQL
  (`internal/persistence/postgres/integration_test.go`) y 9 contra Redis
  (`internal/persistence/redis/redis_integration_test.go`), todos con `-race`.
- `psql`, `initdb` y `pg_ctl` están en `C:\Program Files\PostgreSQL\16\bin`, fuera del `PATH`;
  `pnpm run pg:psql` los localiza y abre una sesión contra el cluster local. Ojo con la distinción:
  los scripts **`db:*`** pasan todos por `docker compose exec` y aquí no sirven; los **`pg:*`** operan
  el cluster propio. `redis-cli` vive en WSL. `gh` 2.100.0 está instalado pero **sin sesión iniciada**.
  Ver [../operations/local-development.md](../operations/local-development.md).

---

## 9. Fuera de MVP

Se documenta para que nadie lo implemente por inercia ni lo asuma existente:

- **Combate** (fase 4 del tick): el paquete `simulation` existe y está lleno, pero la resolución de
  combate no está implementada; la fase 4 no hace nada.
- **Territorio y diplomacia como lógica de dominio.** Las tablas `territories`, `territory_control`,
  `safe_zones`, `treaties` y `garrisons` existen en la migración 000001, pero no hay paquete
  `domain/territory` ni `domain/diplomacy` ni reglas que las consuman.
- **Emisión del ticket desde Next.js** (ADR-010): hoy la sirve `internal/httpapi`. Ver §1.
- Sharding del mundo entre varios procesos: el MVP es **un único proceso** dueño del mundo entero.
- Paralelización interna del tick (A\* en pool de workers, fases concurrentes).
- Sharding de los workers de persistencia por entidad.
- Capacidad del canal de comandos y presupuesto de shutdown como variables `EO_*`.
- Test de arquitectura automático que verifique el grafo de imports de §2.1.
- Naval, marketplace, clanes y tecnologías.
