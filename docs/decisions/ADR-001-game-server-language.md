# ADR-001: Lenguaje del game server — Go 1.23

Propósito: fijar Go 1.23 como lenguaje de implementación del proceso autoritativo `services/game-server` y registrar el coste de mantener dos lenguajes en el repositorio.

- **Estado:** Aceptado
- **Fecha:** 2026-09-09
- **Decisores:** equipo de arquitectura
- **Relacionados:** [ADR-002](ADR-002-authoritative-server.md), [ADR-006](ADR-006-websocket-protocol.md), [ADR-007](ADR-007-game-loop-frequency.md), [ADR-009](ADR-009-shared-protocol-package.md), [ADR-012](ADR-012-database-migrations.md)

---

## Contexto

Empires Online es un MMORTS persistente 24/7 con mundo continuo. El proceso que ejecuta la simulación tiene que sostener, simultáneamente, tres cargas de naturaleza distinta:

1. **Un game loop de frecuencia fija.** Tick rate MVP de 10 Hz (`EO_TICK_RATE_HZ=10`), es decir un presupuesto duro de **100 ms por tick** para ejecutar las ocho fases definidas en [../architecture/game-loop.md](../architecture/game-loop.md): drenar comandos, validarlos y aplicarlos, avanzar movimiento, resolver simulación, procesar timers, actualizar conjuntos de interés, emitir deltas y encolar persistencia. Cualquier tick que exceda ese periodo incrementa `eo_game_tick_overruns_total`, y un overrun sostenido degrada la experiencia de todos los jugadores conectados, no de uno.
2. **Miles de conexiones WebSocket concurrentes y de larga vida.** Cada conexión mantiene estado propio (secuencia `seq` monótona, rate limiter de 20 msg/s con burst 40, área de interés suscrita por chunks) y consume mensajes pequeños con alta frecuencia.
3. **Un mundo residente en memoria.** El grid MVP es de 512 × 512 tiles (`EO_WORLD_WIDTH` / `EO_WORLD_HEIGHT`), particionado en chunks de 32 × 32, más las entidades activas. El estado autoritativo de la simulación vive en RAM del proceso; PostgreSQL es la fuente de verdad durable y Redis el estado caliente, según la separación de capas del canon (ver [ADR-003](ADR-003-postgresql-source-of-truth.md) y [ADR-004](ADR-004-redis-hot-state.md)).

A esto se suman restricciones estructurales del proyecto:

- **El servidor es autoritativo sin excepciones** ([ADR-002](ADR-002-authoritative-server.md)). Toda validación de comandos, pathfinding y resolución de estado ocurre en este proceso. No se puede externalizar trabajo al cliente para aliviar CPU.
- **El dominio debe ser determinista.** Nada de `time.Now()` ni de generadores aleatorios globales dentro del dominio: se inyectan interfaces `Clock` y `RandomSource`, y los tests de simulación avanzan un `FakeClock` y asertan estado exacto. El lenguaje debe permitir aislar esas fuentes de no determinismo con facilidad y, sobre todo, el equipo debe poder auditar que no se cuelan.
- **Pathfinding en la ruta caliente.** A\* sobre grid de 8 direcciones con heurística octile en aritmética entera, hasta `EO_PATHFINDING_MAX_NODES=20000` nodos por consulta y `EO_PATHFINDING_MAX_DISTANCE=256` tiles. Es trabajo numérico intensivo, asignación de memoria intensiva y sensible tanto al coste por nodo como a la presión que genera sobre el recolector de basura.
- **Despliegue simple.** El proyecto es greenfield y no dispone todavía de infraestructura; el objetivo es un contenedor pequeño reproducible por `infra/docker/`, con `GET /health` y `GET /ready` y métricas Prometheus en `EO_METRICS_ADDR`.
- **El cliente ya está decidido en TypeScript.** `apps/web` **ya existe en el repositorio**: Next.js 15 (App Router) con React 19 y PixiJS 8 ([ADR-005](ADR-005-pixijs-renderer.md)) cuando llegue su milestone. Sea cual sea la elección del servidor, el frontend será TypeScript. La única forma de tener *un solo* lenguaje sería que el servidor también lo fuese.

El entorno real de desarrollo condiciona la evaluación: Windows 10 Pro, Node v22.17.1, pnpm 10.25.0, git 2.38.1, Docker CLI 20.10.22 con Compose v2.15.1. **Go está instalado** (toolchain 1.27.0, instalado con `winget install --id GoLang.Go`), de modo que `go build ./...`, `go vet ./...` y `go test ./...` se ejecutan en la máquina. Lo que **no** está disponible es el daemon de Docker Desktop (instalado pero sin arrancar), ni `psql`, `redis-cli`, `make` o `gh`.

## Decisión

**El game server (`services/game-server`) se implementa en Go 1.23.**

Alcance concreto:

- Módulo Go con path `github.com/empires-online/empires-online/services/game-server`, con la estructura `cmd/server/` + `internal/{auth,clock,config,domain,game,httpapi,observability,pathfinding,persistence,protocol,websocket}`. `go.mod` declara `go 1.23` como versión **mínima** del lenguaje; el toolchain instalado en la máquina de desarrollo es más reciente (1.27.0) y eso es compatible por diseño.
- Toda la lógica autoritativa —validación de comandos, game loop, pathfinding, dominio, interest management, persistencia— vive en este módulo. No hay lógica de juego en `apps/web`.
- Stack de bibliotecas: `jackc/pgx/v5` para PostgreSQL, `redis/go-redis/v9`, `gorilla/websocket`, `prometheus/client_golang`, `golang-migrate/migrate/v4` para las migraciones embebidas ([ADR-012](ADR-012-database-migrations.md)) y `golang-jwt/jwt/v5` para el game ticket ([ADR-010](ADR-010-authentication-game-ticket.md)). Logging estructurado JSON con `log/slog` de la biblioteca estándar. Tests con `testing` + `testify/require`.
- La validación de mensajes en runtime se escribe a mano en Go por rendimiento; la conformidad con el esquema se verifica en *contract tests* contra el JSON Schema exportado por `packages/protocol` ([ADR-009](ADR-009-shared-protocol-package.md)).
- El task runner del repositorio son **pnpm scripts** (`pnpm run server:test`, `pnpm run db:up`), no un Makefile, porque `make` no está disponible en la máquina de desarrollo. Esa elección no tiene ADR propio: se documenta en [../operations/local-development.md](../operations/local-development.md).

Fuera del alcance de este ADR: la elección del lenguaje del cliente (ya fijada como TypeScript) y el mecanismo de compartición del protocolo entre ambos, que se decide en [ADR-009](ADR-009-shared-protocol-package.md).

### Por qué Go satisface las restricciones

| Restricción | Cómo la cubre Go |
|---|---|
| Decenas de miles de conexiones WS | Goroutines: una o dos por conexión (lector/escritor) a coste de unos pocos KiB de pila inicial, planificadas por el runtime sobre los hilos del SO. No hay que construir una máquina de estados sobre un event loop para no bloquear a los demás. |
| Presupuesto de 100 ms por tick | GC concurrente con marcado tricolor y objetivos de pausa en el rango sub-milisegundo. Las pausas son pequeñas frente al periodo del tick, y el *stop-the-world* real es corto y acotado. |
| Mundo residente en RAM | Structs planos, arrays y slices sin cabeceras de objeto pesadas; control explícito del layout (`[]uint8` de 1024 bytes por chunk, igual que su representación persistida en `world_chunks`). Densidad de memoria muy superior a la de un runtime con objetos dinámicos. |
| Pathfinding intensivo | Compilación nativa, aritmética entera (costes escalados con `costScaleOrtho = 1000` y `costScaleDiag = 1414`, heurística octile ponderada por `MinTerrainCostUnits = 6`), estructuras reutilizables entre consultas para reducir presión de asignación. |
| Determinismo | Tipado estático y ausencia de `this` dinámico facilitan inyectar `Clock` y `RandomSource` (`internal/clock`). El punto delicado —el orden de iteración de mapas es deliberadamente aleatorio en Go— es un riesgo conocido y explícito: el canon prohíbe iterar mapas sin ordenar, y es verificable con revisión y lint. No hay ADR propio de determinismo: la regla vive en el canon §1.5 y su verificación en [../testing/simulation-tests.md](../testing/simulation-tests.md). |
| Despliegue | Binario estático sin dependencias de runtime; imagen de contenedor mínima; arranque en milisegundos, lo que abarata reinicios y el flujo de recuperación de movimientos `ACTIVE` al arrancar. |
| Observabilidad | `log/slog` en la biblioteca estándar para los campos `ts`, `level`, `msg`, `player_id`, `session_id`, `request_id`, `tick`; cliente Prometheus maduro para `eo_game_tick_duration_seconds`, `eo_connected_players` y el resto de métricas del canon. `pprof` incorporado para diagnosticar overruns. |
| Ecosistema | `pgx` es el driver PostgreSQL de referencia y expone tipos nativos, `COPY` y pooling; clientes Redis y WebSocket maduros. Nada de esto obliga a un framework. |

## Alternativas consideradas

### A. Node.js con TypeScript

**Ventajas reales, y son importantes.** Sería **un único lenguaje en todo el repositorio**: el mismo modelo de dominio, los mismos tipos y, sobre todo, el paquete de protocolo compartido sin fricción — los esquemas Zod de `packages/protocol` se consumirían directamente como validador en runtime del servidor, eliminando por completo la necesidad de exportar JSON Schema, embeberlo y escribir validación manual en Go. Un desarrollador cambiaría entre cliente y servidor sin cambiar de herramientas, de linter ni de gestor de paquetes. La cadena de build ya existe (pnpm 10.25.0, Node v22.17.1) y el equipo la domina. Para un proyecto greenfield con recursos limitados, esta reducción de superficie es un argumento de peso, no un detalle.

**Por qué se descarta.**

- **Un solo hilo para la simulación.** El game loop, la validación de comandos y el pathfinding competirían por el mismo hilo que serializa y emite los deltas de todas las conexiones. Una consulta A\* que roce los 20 000 nodos bloquea el event loop entero, y con él los `session.ping`, el rate limiting y la emisión de deltas. Se puede mitigar con `worker_threads`, pero eso reintroduce el problema que se quería evitar —comunicación entre hilos con serialización o memoria compartida y un modelo de concurrencia explícito— sin la ergonomía de las goroutines.
- **Pausas de GC menos predecibles bajo carga.** El recolector generacional de V8 es excelente para cargas de petición-respuesta, pero un heap grande y de larga vida (el mundo entero residente) provoca ciclos *major* cuyo coste crece con el heap vivo. Frente a un presupuesto de 100 ms compartido con todo lo demás, esa varianza es el riesgo que más directamente se traduce en `eo_game_tick_overruns_total`.
- **Peor densidad de memoria.** Cada entidad y cada tile como objeto JavaScript arrastra cabecera, forma oculta y punteros. Representar el grid y las entidades activas con estructuras planas exige recurrir a `TypedArray` y a un estilo "structs sobre buffers" que anula gran parte de la comodidad que motivaba la elección.
- **Concurrencia de E/S mezclada con simulación.** El modelo `async/await` es cómodo para I/O, pero invita a introducir `await` dentro del tick, exactamente lo que el canon prohíbe: el tick no ejecuta I/O bloqueante contra PostgreSQL.

El descarte no es por rendimiento absoluto —Node es rápido— sino por **previsibilidad** bajo un presupuesto de tick fijo y por el aislamiento entre simulación y red.

### B. Rust

**Ventajas reales.** El mejor rendimiento del conjunto y el único candidato **sin recolector de basura**: latencias de tick sin varianza atribuible al GC. El sistema de tipos y el *borrow checker* eliminan por construcción clases enteras de errores de concurrencia, algo especialmente valioso en un servidor con estado mutable compartido entre el loop y los manejadores de conexión. La densidad de memoria es la mejor posible y `tokio` sostiene sin dificultad decenas de miles de conexiones. Los enums algebraicos modelan los estados del dominio (`ACTIVE`, `COMPLETED`, `CANCELLED`, `FAILED`; `IDLE`, `MOVING`, `GARRISONED`, `HIDDEN`, `DEAD`) con exhaustividad comprobada por el compilador, mejor que cualquier alternativa de esta lista.

**Por qué se descarta.** El coste de desarrollo. Un mundo mutable y compartido en Rust obliga a decidir por adelantado la estrategia de ownership (arenas, índices en lugar de referencias, `Arc<Mutex<…>>` o paso de mensajes), y equivocarse cuesta refactors caros en un proyecto que todavía está descubriendo su propio dominio. La velocidad de iteración en la fase de vertical slice importa más que el último 30 % de rendimiento, y los tiempos de compilación penalizan el ciclo editar-probar. A esto se suma el coste de contratación: encontrar y sustituir desarrolladores Rust con experiencia en sistemas de tiempo real es medible y caro. **Rust es la mejor opción técnica pura y aun así se descarta**; es la alternativa que se reevaluaría si un componente concreto (por ejemplo el pathfinding) se convirtiera en cuello de botella medido.

### C. C# con .NET

**Ventajas reales.** Runtime muy maduro, JIT excelente, GC en modo servidor configurable y una historia de juegos consolidada: la industria tiene décadas de servidores de juego en C#, herramientas de perfilado de primera y personas con experiencia directa en el dominio. `async`/`await` con un *thread pool* real evita el problema del hilo único de Node, `Span<T>` y `struct` permiten un control razonable del layout de memoria, y el tipado es fuerte y expresivo. Es, objetivamente, un candidato sólido y por rendimiento bruto está más cerca de Go que de Node.

**Por qué se descarta.** Encaje con el resto del stack. El repositorio ya es un monorepo pnpm; añadir la cadena de herramientas de .NET (SDK, MSBuild, NuGet) supone un tercer ecosistema de build sobre el que ya se tiene (Node/pnpm) y el que se vaya a añadir. El binario estático de Go y su imagen de contenedor mínima simplifican el despliegue frente al modelo de publicación de .NET. Las pausas del GC son configurables pero requieren afinado deliberado, mientras que el GC de Go es de baja latencia por defecto. La decisión aquí es de **afinidad operativa**, no de capacidad del lenguaje.

### D. Elixir / BEAM

**Ventajas reales.** Probablemente el mejor runtime existente para **conexiones masivas de larga vida**: procesos ligerísimos, planificador preemptivo justo (ningún proceso puede acaparar el scheduler), aislamiento total de fallos y árboles de supervisión que reinician una conexión rota sin tocar al resto. Phoenix Channels resolvería el fan-out por chunk casi de fábrica, y la tolerancia a fallos y el hot code upgrade son argumentos legítimos para un servicio 24/7 persistente.

**Por qué se descarta.** El perfil de carga no es solo de conexiones: dentro de cada tick hay **simulación numérica intensiva** —A\* con miles de nodos, recálculo de posiciones sobre polilíneas temporizadas, evaluación de conjuntos de interés—. La BEAM está optimizada para concurrencia y paso de mensajes, no para bucles numéricos apretados sobre estructuras mutables; sus datos son inmutables, lo que fuerza copias o el uso de ETS y complica mantener un mundo mutable de 512 × 512 tiles con coste de acceso predecible. Empujar el trabajo pesado a NIFs reintroduce C o Rust y con ello el riesgo de bloquear un scheduler. Además, el equipo no tiene experiencia previa y el ecosistema de bibliotecas para el resto del stack es más estrecho.

### E. Resumen comparativo

| Criterio | Go 1.23 | Node/TS | Rust | C# .NET | Elixir |
|---|---|---|---|---|---|
| Concurrencia de conexiones | Muy buena | Buena (1 hilo) | Muy buena | Buena | Excelente |
| Previsibilidad de latencia en tick | Buena | Media | Excelente | Buena (con afinado) | Media |
| Densidad de memoria del mundo | Buena | Baja | Excelente | Buena | Media |
| Rendimiento en A\* | Bueno | Medio | Excelente | Bueno | Bajo |
| Velocidad de desarrollo | Buena | Excelente | Baja | Buena | Media |
| Un solo lenguaje en el repo | No | **Sí** | No | No | No |
| Simplicidad de despliegue | Excelente | Buena | Excelente | Media | Media |
| Coste de contratación | Bajo | Muy bajo | Alto | Bajo | Alto |

## Consecuencias

### Positivas

- El aislamiento entre el goroutine del game loop y los goroutines de conexión es natural: una conexión lenta o un cliente malicioso no roban tiempo al tick, y el drenaje de comandos se hace desde una cola no bloqueante en la fase 1 del tick.
- El presupuesto de 100 ms queda holgado para el MVP: las pausas del GC se miden en microsegundos-milisegundos, muy por debajo del periodo, y `eo_game_tick_duration_seconds` puede vigilarse con margen real.
- El binario estático hace triviales el `docker build` del pipeline de CI y los reinicios; el arranque rápido abarata la recuperación de movimientos `ACTIVE` descrita en [ADR-011](ADR-011-movement-timed-polyline.md).
- El tipado estático más `go vet` y el linter dan una red de seguridad barata sobre un dominio con muchos estados enumerados.
- La biblioteca estándar cubre HTTP, JSON, logging estructurado (`log/slog`), tests y perfilado sin dependencias adicionales, lo que reduce la superficie de supply chain del proceso más crítico.
- `go:embed` permite incrustar los JSON Schema del protocolo en el binario sin ficheros sueltos en el contenedor.

### Negativas

- **Dos lenguajes en el repositorio.** Es el coste principal y se asume de forma explícita. Obliga a: mantener dos cadenas de build y dos linters en el CI, duplicar los tipos del protocolo en TypeScript y en Go, y **resolver la estrategia de protocolo compartido de [ADR-009](ADR-009-shared-protocol-package.md)** (Zod como fuente de verdad → exportación a JSON Schema → `go:embed` → contract tests). Sin esa disciplina, cliente y servidor derivarán y el fallo aparecerá en producción, no en compilación.
- **Validación de mensajes escrita a mano en Go.** Por rendimiento no se valida contra el JSON Schema en runtime, así que la validación manual puede divergir del esquema. Se mitiga con contract tests obligatorios, pero es trabajo recurrente en cada cambio de protocolo.
- **El toolchain de Go es una dependencia añadida del entorno de desarrollo.** Ya está resuelta (`winget install --id GoLang.Go`, toolchain 1.27.0), pero es una segunda cadena de herramientas que instalar, versionar y mantener junto a Node/pnpm. Sigue pendiente arrancar el daemon de Docker Desktop —instalado pero apagado—, y hasta entonces los tests de integración contra PostgreSQL y Redis **no se ejecutan**, solo los unitarios y los de simulación. Lo mismo aplica a las herramientas ausentes (`psql`, `redis-cli`, `make`, `gh`): el acceso a PostgreSQL y Redis se hace vía `docker compose exec`.
- **El orden de iteración de mapas en Go es aleatorio por diseño.** Es una trampa directa contra el requisito de determinismo. Cualquier `range` sobre un `map` que influya en el resultado del tick o del pathfinding es un bug latente que solo se manifiesta como divergencia intermitente. Exige revisión activa y ordenación explícita.
- **Expresividad del sistema de tipos.** Go no tiene *sum types*; los estados del dominio se modelan como constantes de tipo `string` o `uint8` con validación explícita, sin exhaustividad comprobada por el compilador. Un estado nuevo no rompe la compilación de los `switch` existentes.
- **Manejo de errores verboso.** El estilo `if err != nil` multiplica las líneas y tienta a ignorar errores en rutas frías; se compensa envolviendo con contexto y mapeando a los códigos estables del canon.
- Curva de aprendizaje real para quien venga solo de TypeScript: punteros, valor frente a referencia, `context.Context` y el modelo de memoria requieren tiempo antes de escribir código concurrente correcto.

### Neutras

- El repositorio contiene un módulo Go conviviendo con workspaces pnpm; `pnpm-workspace.yaml` solo declara `apps/*` y `packages/*`, así que `services/game-server` queda fuera del workspace y sus tareas se invocan desde pnpm scripts.
- El CI ejecuta dos conjuntos de herramientas (formato/lint/typecheck de TypeScript y formato/vet/test de Go) dentro del mismo pipeline `format → lint → typecheck → unit → integration → build → docker build`.
- La numeración de versiones del runtime pasa a ser una dependencia explícita: la versión mínima `go 1.23` se fija en el `go.mod`, y la imagen base del contenedor y el workflow de CI fijan el toolchain concreto; cualquier actualización se coordina en los tres sitios.
- El perfilado del servidor se hará con `pprof`, herramienta distinta de la que usa el equipo de frontend.

## Estado

**Aceptado** el 2026-09-09.

Se reevaluaría si se cumpliera alguna de estas condiciones, cada una verificable con métricas ya definidas en [../operations/monitoring.md](../operations/monitoring.md):

1. `eo_game_tick_overruns_total` crece de forma sostenida con el mundo MVP y el perfilado atribuye la causa principal a pausas del GC y no al trabajo de simulación. Respuesta esperada: reducir asignaciones antes de cambiar de lenguaje; solo si eso se agota, considerar Rust para el componente afectado.
2. `eo_pathfinding_duration_seconds` consume una fracción inaceptable del presupuesto de tick incluso tras adoptar Hierarchical A\* detrás de la interfaz `Pathfinder`. Respuesta esperada: extraer únicamente el pathfinding, no reescribir el servidor.

Ninguna de las dos justifica por sí sola cambiar el lenguaje del proceso completo. Este ADR se sustituiría solo si se decidiera reescribir `services/game-server` en otro lenguaje.
