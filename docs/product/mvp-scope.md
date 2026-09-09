# Scope del MVP — Primer vertical slice

Define exactamente qué entra y qué no entra en la primera entrega, el flujo end-to-end obligatorio y los criterios de aceptación verificables.

---

## 1. Objetivo del vertical slice

El MVP **no** persigue "tener un juego jugable". Persigue demostrar, de extremo a extremo y con evidencia
reproducible, que **el eje persistente funciona**:

> Un jugador entra al mundo, ordena a un aldeano moverse a un tile lejano, cierra el navegador a mitad de
> camino, y al volver encuentra al aldeano exactamente donde el tiempo real dice que debe estar.

Esa frase es el producto entero del primer slice. Todo lo que no sea necesario para demostrarla queda
fuera, aunque sea barato de implementar. La razón es de riesgo, no de esfuerzo: si la simulación
continua, la reconstrucción analítica del movimiento y la recuperación tras reinicio no funcionan, todo lo
que se construya encima habrá que rehacerlo. Los sistemas que sí sabemos construir (economía, tecnologías,
combate) no aportan información nueva sobre ese riesgo.

Un vertical slice atraviesa **todas** las capas —cliente, protocolo, servidor, dominio, persistencia— en
lugar de completar una capa entera. Ver [glossary.md](glossary.md) → *Vertical Slice*.

---

## 2. Tabla IN / OUT

### 2.1 Dentro del MVP

| # | Elemento | Alcance concreto | Justificación |
|---|---|---|---|
| IN-01 | **Mundo 512 × 512 determinista** | Grid de tiles int32, **regenerado desde `EO_WORLD_SEED` (20260909) en cada arranque**; `world_chunks` (bytea de 1024 B por chunk) guarda una copia para auditoría y edición futura, no como fuente primaria. | Sin mundo fijo y reproducible no hay nada que persistir ni forma de comparar dos ejecuciones. |
| IN-02 | **Chunks de 32 × 32** | 256 chunks; `chunkX = x >> 5`, `chunkY = y >> 5`, `chunkId = chunkY*chunksPerRow + chunkX`. `EO_WORLD_WIDTH` y `EO_WORLD_HEIGHT` deben ser múltiplos exactos de `EO_CHUNK_SIZE`. | Unidad de suscripción de red e índice de persistencia; introducirla después obligaría a rehacer el protocolo. |
| IN-03 | **Terreno con coste y bloqueo** | `GRASSLAND` (10), `FOREST` (16), `HILL` (18), `ROAD` (6) transitables y `MOUNTAIN`/`WATER` bloqueados, con el coste en décimas del base (`world.CostBase = 10`); capa de ocupación (*blocked overlay*) separada, que no muta el terreno. | El coste del terreno es lo que hace del tiempo un recurso y del A\* algo no trivial. |
| IN-04 | **Player** | Tabla `players` (PK `uuid`, `password_hash` con bcrypt, CHECK `username ~ '^[A-Za-z0-9_-]{3,24}$'`), con `Civilization` y `Global Faction` como ejes ortogonales. | La estructura de identidad debe existir desde el principio; migrarla más tarde tocaría a todos los jugadores. |
| IN-05 | **City inicial** | 1 `TOWN_CENTER` + 1 zona urbana amurallada inicial (rectángulo 3 × 3 bloqueado alrededor del centro) + 3 `VILLAGER` nacidos a radio 2. Emplazamiento determinista en espiral, entorno despejado de radio 3 y separación mínima de 24 tiles entre centros. `population_limit` = `era.population_cap`. | Ancla del jugador en el mundo y sujeto de la protección offline. |
| IN-06 | **Eras como datos** | Tabla `eras` con `STONE_AGE` (20), `BRONZE_AGE` (50), `IRON_AGE` (100), `CASTLE_AGE` (150). | Se crean como datos, sin lógica de avance de era: fijan el `population_limit`. |
| IN-07 | **Unit `VILLAGER`** | hp 40, `baseMsPerTile` 600; estados `IDLE`, `MOVING`, `GARRISONED`, `HIDDEN`, `DEAD`. | Un solo tipo de unidad basta para ejercitar todo el pipeline de movimiento. |
| IN-08 | **Autenticación por game ticket** | JWT HS256, TTL 60 s, `aud: "game-server"`, `jti` consumido en Redis (`ticket:jti:{jti}`, TTL 120 s). Hoy lo emiten `POST /api/auth/register` y `POST /api/auth/login` del propio game server (`internal/httpapi`); trasladarlos a Next.js es el objetivo de ADR-010. | Sin identidad verificada no hay ownership, y sin ownership no hay autoridad del servidor. |
| IN-09 | **Protocolo WebSocket v1** | C→S: `session.hello`, `session.ping`, `session.view`, `unit.move`, `unit.cancel_move`. S→C: `session.welcome`, `session.pong`, `system.error`, `world.snapshot`, `entity.spawn`, `entity.update`, `entity.despawn`, `city.update`, `territory.update`, `unit.move.accepted`, `unit.move.rejected`, `unit.movement.started`, `unit.movement.completed`, `unit.movement.cancelled`. | Es la superficie mínima que cubre el flujo completo; cada mensaje extra es superficie de ataque y de mantenimiento. |
| IN-10 | **Idempotencia** | `requestId` UUIDv4 obligatorio; Redis `idem:{playerId}:{requestId}` TTL 300 s + tabla `idempotency_keys` para comandos durables. | La reconexión implica reintentos; sin idempotencia el slice produce estado duplicado justo en el escenario que quiere demostrar. |
| IN-11 | **Interest management por chunk** | Radio `EO_INTEREST_RADIUS_CHUNKS` = 2; centro inicial en la ciudad; actualizable con `session.view` (rate-limited). | Es lo que hace que `world.snapshot` sea acotado y que el cliente no reciba el mundo entero. |
| IN-12 | **Game loop 10 Hz** | Ocho fases en orden fijo; sin I/O bloqueante de PostgreSQL dentro del tick; métricas `eo_game_tick_duration_seconds` y `eo_game_tick_overruns_total`. | Es el pilar 1 hecho código. |
| IN-13 | **A\* con heurística octile** | 8 direcciones, sin *corner cutting*, escalas enteras `costScaleOrtho = 1000` / `costScaleDiag = 1414`, heurística ponderada por `MinTerrainCostUnits = 6` (`ROAD`), desempate `(f, h, y, x)`, límites `EO_PATHFINDING_MAX_NODES` (20000) y `EO_PATHFINDING_MAX_DISTANCE` (256). La ruta devuelta incluye el tile de origen. | El pathfinding autoritativo es la única forma de que el cliente no aporte caminos. |
| IN-14 | **Movimiento como polilínea temporizada** | `unit_movements.path` `jsonb` con waypoints `{x, y, tMs}`, más `start_time_ms` y `arrival_time_ms` `bigint`; estados `ACTIVE`, `COMPLETED`, `CANCELLED`, `FAILED`; máximo un `ACTIVE` por unidad, garantizado por el índice único parcial `unit_movements_one_active_per_unit`. | Es el mecanismo que hace la posición reconstruible sin replay: el corazón del slice. |
| IN-15 | **Recuperación tras crash** | `simulation.Hydrate` carga los movimientos `ACTIVE`: si el movimiento venció durante la caída, la unidad aparece en el destino y el movimiento se cierra `COMPLETED`; si sigue en curso, se reanuda desde la polilínea; si la polilínea es inválida, queda `FAILED` y la unidad se queda donde estaba —nunca se teletransporta a nadie por un dato dudoso—. | Es la prueba de que ningún estado durable depende de un WebSocket ni de un proceso vivo. |
| IN-16 | **Presencia y protección offline** | Redis `presence:player:{playerId}` TTL 30 s, heartbeat 10 s; máquina `ONLINE → OFFLINE_PENDING → PROTECTED` con cooldown 300 s, evaluada por el game loop en RAM contra `DisconnectGrace` (= `EO_PRESENCE_TTL_SECONDS`). | Pilar 3; además es lo que da sentido de juego a la desconexión del flujo end-to-end. |
| IN-17 | **Persistencia por capas** | Transacción propia para lo crítico, **encolada** hacia workers (hasta 3 intentos con backoff y compensación `OnPermanentFailure`) porque el tick no hace I/O de PostgreSQL; dirty-flag + flush cada `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` (50 ticks = 5 s) para posiciones consolidadas y HP. | Define y valida el contrato "qué es durable, qué es eventual, qué es reconstruible". |
| IN-18 | **Cliente isométrico mínimo** — *pendiente* | Next.js 15 + React 19 + PixiJS 8: render del terreno del AoI, selección de unidad, orden de movimiento por clic, interpolación sub-tile puramente visual. **`apps/web/` ya existe y el slice se ha ejecutado de extremo a extremo** en el navegador. | Sin cliente no hay vertical slice: el flujo debe ser observable por un humano, no solo por un test. |
| IN-19 | **Observabilidad base** | Logging JSON con `log/slog` (`ts`, `level`, `msg`, `player_id`, `session_id`, `request_id`, `tick`); métricas `eo_*` en `EO_METRICS_ADDR/metrics`; `GET /health` y `GET /ready`. | Un mundo persistente sin observabilidad no es depurable, y sus bugs son permanentes. |
| IN-20 | **Tablas creadas con lógica mínima o diferida** | `territories`, `territory_control`, `safe_zones`, `treaties`, `garrisons`, `world_events`. | Se crean ahora porque su ausencia condicionaría el diseño del esquema; su lógica se difiere. |

### 2.2 Fuera del MVP

| # | Elemento | Justificación de la exclusión |
|---|---|---|
| OUT-01 | **Combate completo** | La fase 4 del tick (`resolve simulation`) está reservada pero vacía. El combate multiplica los invariantes a verificar sin aportar nada al riesgo que el slice quiere despejar. |
| OUT-02 | **Economía completa** | Recolección, almacenes, costes y producción exigen un modelo de recursos que aún no está especificado; añadirlo ahora acoplaría el movimiento a un sistema inmaduro. |
| OUT-03 | **Árbol tecnológico completo** | Las tablas `technologies` y `civilization_technologies` son solo diseño, sin migración. La progresión tecnológica no aporta señal sobre la persistencia. |
| OUT-04 | **Cientos de unidades** | Un único `unit_type` (`VILLAGER`) ejercita el pipeline entero. Más tipos son variación de datos, no de arquitectura, y encarecen cada test. |
| OUT-05 | **Crafting** | Depende de la economía (OUT-02). Sin recursos no hay nada que fabricar. |
| OUT-06 | **Quests** | Contenido dirigido; irrelevante para validar el mundo persistente y costoso en herramientas de autoría. |
| OUT-07 | **NPCs complejos** | Requieren IA y simulación adicional dentro del tick, justo el punto donde el presupuesto de 100 ms es más sensible. |
| OUT-08 | **Marketplace** | `markets`, `trade_routes`, `caravans` y `trade_transactions` son solo diseño. El comercio necesita antes economía y tecnologías exclusivas. |
| OUT-09 | **Chat avanzado** | Canales, moderación e historial son un subsistema propio con sus propios problemas de persistencia y abuso; no valida ninguna hipótesis del slice. |
| OUT-10 | **Ranking** | Requiere métricas de juego estables que todavía no existen; un ranking sobre reglas provisionales genera expectativas difíciles de retirar. |
| OUT-11 | **IA avanzada** | Mismo motivo que OUT-07, con mayor coste de CPU dentro del tick determinista. |
| OUT-12 | **Naval** | El terreno `WATER` está bloqueado en el MVP. El movimiento naval introduce un segundo dominio de pathfinding y transiciones tierra-agua. |
| OUT-13 | **Clanes** | La tabla `clans` es solo diseño. La organización social de alto nivel presupone diplomacia funcionando, que aquí está diferida. |
| OUT-14 | **Sieges y guerra a gran escala** | Consecuencia directa de OUT-01: sin combate no hay asedio. |
| OUT-15 | **Avance de era como mecánica** | Las eras existen como datos y fijan el `population_limit`, pero no hay acción de avanzar de era: eso requiere economía (OUT-02). |
| OUT-16 | **Tests de carga (k6)** | Diferidos por decisión explícita: no bloquean el MVP. El objetivo del slice es corrección, no capacidad. |
| OUT-17 | **Escalado multiproceso / sharding** | El MVP es un único proceso de Game Server. Los límites y las vías de crecimiento se documentan, no se implementan. Ver [../architecture/scalability.md](../architecture/scalability.md). |

La lista de exclusiones OUT-01 a OUT-13 corresponde literalmente al anti-scope-creep del canon técnico.
**No se documenta ninguno de estos sistemas como si existiera**; donde aparecen, van marcados
`Fuera de MVP`.

---

## 3. Flujo end-to-end obligatorio

Este es el recorrido que el MVP debe entregar completo. Es la definición operativa de "terminado".

```
login -> world -> city -> 3 villagers -> select -> move -> A* -> game loop
      -> websocket sync -> disconnect -> el movimiento continúa -> reconnect
      -> estado correcto restaurado
```

### 3.1 Secuencia detallada

```mermaid
sequenceDiagram
    participant U as Jugador
    participant W as apps/web (Next.js + PixiJS) — pendiente
    participant G as game-server (Go)
    participant R as Redis
    participant P as PostgreSQL

    U->>W: login
    W->>G: POST /api/auth/login (hoy en el game server; ADR-010 lo mueve a Next.js)
    G-->>W: game ticket (JWT HS256, TTL 60 s)
    W->>G: WSS connect + session.hello { ticket }
    G->>R: consume ticket:jti:{jti} (TTL 120 s)
    G->>P: crea sessions; city.presence_state = ONLINE
    G->>R: SET presence:player:{playerId} (TTL 30 s)
    G-->>W: session.welcome
    G-->>W: world.snapshot (AoI = 2 chunks alrededor de la ciudad)
    Note over W: render isométrico del terreno + entidades
    U->>W: selecciona villager y hace clic en el destino
    W->>G: unit.move { unitId, target:{x,y} } + requestId
    G->>G: valida ownership, estado, destino; ejecuta A*; aplica en RAM
    G->>P: encola INSERT unit_movements (path jsonb, start_time_ms, arrival_time_ms)
    G-->>W: unit.move.accepted + unit.movement.started
    loop cada tick (100 ms)
        G->>G: advance movement; emit deltas por chunk
        G-->>W: entity.update
    end
    U->>W: cierra el navegador
    W--xG: WebSocket cerrado
    Note over G: vence DisconnectGrace (30 s) sin ninguna sesión abierta
    G->>P: city.presence_state = OFFLINE_PENDING
    Note over G: el movimiento SIGUE avanzando: el mundo no se detiene
    G->>P: al llegar: unit_movements.status = COMPLETED, units.position actualizada
    Note over G: tras 300 s -> city.presence_state = PROTECTED
    U->>W: vuelve a entrar (nuevo ticket)
    W->>G: session.hello { ticket }
    G->>P: presence_state = ONLINE
    G-->>W: session.welcome + world.snapshot
    Note over W: el villager aparece donde el tiempo real dice
```

### 3.2 Los cuatro momentos que el slice debe probar

| Momento | Qué demuestra | Pilar |
|---|---|---|
| `move` → `A*` → `unit.movement.started` | El servidor es la única fuente del camino y del ETA. | 2 |
| `disconnect` → el movimiento continúa | La simulación no depende de la conexión. | 1 |
| `disconnect` → `OFFLINE_PENDING` → `PROTECTED` | La ausencia tiene consecuencias acotadas y observables. | 3 |
| `reconnect` → estado correcto | La posición es reconstruible analíticamente, no por replay. | 1, 7 |

### 3.3 Ejemplo numérico canónico

Este es **el único** ejemplo canónico del proyecto: es el que ejercita el golden test de
`internal/domain/movement` y el que debe citar cualquier otro documento. Un `VILLAGER`
(`baseMsPerTile = 600`) recorre `(0,0) → (1,0) → (2,1) → (3,1) → (4,1)`.

La duración de cada segmento se calcula en **aritmética entera exacta**, con `costUnits` en décimas del
coste base (`world.CostBase = 10`):

```go
ms := (baseMsPerTile*costUnits + 5) / 10           // redondeo al ms más cercano
if diagonal { ms = (ms*1414214 + 500000) / 1000000 }  // √2 en punto fijo, mismo redondeo
if ms < 1 { ms = 1 }
```

Regla canónica: **se redondea cada segmento al milisegundo más cercano y sólo después se acumula.** No se
trunca: mil pasos truncados regalarían casi un segundo de ventaja.

| Segmento | Tipo | `costUnits` | Segmento (ms) | `tMs` acumulado |
|---|---|---|---|---|
| — (origen) | — | — | — | `0` |
| `(0,0) → (1,0)` | ortogonal, `GRASSLAND` | 10 | `600` | `600` |
| `(1,0) → (2,1)` | diagonal, `GRASSLAND` | 10 | `849` | `1449` |
| `(2,1) → (3,1)` | ortogonal, `FOREST` | 16 | `960` | `2409` |
| `(3,1) → (4,1)` | ortogonal, `ROAD` | 6 | `360` | `2769` |

```json
{
  "path": [
    { "x": 0, "y": 0, "tMs": 0 },
    { "x": 1, "y": 0, "tMs": 600 },
    { "x": 2, "y": 1, "tMs": 1449 },
    { "x": 3, "y": 1, "tMs": 2409 },
    { "x": 4, "y": 1, "tMs": 2769 }
  ]
}
```

`arrival_time_ms = start_time_ms + 2769`. La *Authoritative Position* en cualquier instante `T` es el
último waypoint con `tMs <= (T - start_time_ms)`, resuelto por búsqueda binaria; en
`T = start_time_ms + 1000` la unidad está en `(1,0)`, sin necesidad de haber simulado un solo tick. Antes
del instante 0 la posición es el origen; después del último `tMs`, el destino.

El factor diagonal solo es aplicable si ambos tiles ortogonales adyacentes son transitables: el
*corner cutting* está prohibido.

---

## 4. Criterios de aceptación

Numerados, verificables y con el nivel de test que los cubre. El MVP está terminado cuando **todos** pasan
en CI. `[unit]`, `[integration]`, `[contract]`, `[simulation]`, `[recovery]` y `[manual]` indican el nivel
que los verifica.

**Estado real a fecha de hoy.** Los criterios cubiertos por `[unit]`, `[contract]`, `[simulation]` y
`[recovery]` están implementados y su suite está **en verde** (`go build ./...`, `go vet` y `go test` del
game server; 18 tests de Vitest en `packages/protocol`). Los criterios `[integration]` están **escritos
pero no ejecutados**: requieren PostgreSQL y Redis reales vía Docker Compose y el daemon de Docker no
arrancó en la máquina de desarrollo. Los criterios `[manual]` (AC-32 a AC-34) están **pendientes** porque
`apps/web/` ya existe y el flujo se ejecutó de extremo a extremo. Lo que sigue sin ejecutarse es la CI.

### 4.1 Mundo y determinismo

- **AC-01** `[integration]` Con `EO_WORLD_SEED=20260909`, dos generaciones independientes del mundo
  producen `world_chunks` **byte a byte idénticos** para los 256 chunks.
- **AC-02** `[unit]` Para todo tile válido, `chunkX = x >> 5`, `chunkY = y >> 5` y
  `chunkId = chunkY*chunksPerRow + chunkX` coinciden con la partición de 16 × 16 chunks.
- **AC-03** `[unit]` Un destino con `x < 0`, `y < 0`, `x >= 512` o `y >= 512` produce
  `TARGET_OUT_OF_BOUNDS`.
- **AC-04** `[unit]` Un destino sobre `MOUNTAIN` o `WATER` produce `TARGET_NOT_WALKABLE`; el servidor
  **no** busca un tile cercano alternativo.

### 4.2 Identidad, sesión y protocolo

- **AC-05** `[integration]` Un jugador nuevo recibe una `city` con exactamente **1 `TOWN_CENTER`**, la zona
  urbana amurallada inicial y **3 unidades `VILLAGER`** con `hp = 40` y `status = IDLE`.
- **AC-06** `[integration]` Un `session.hello` con un `jti` ya consumido en `ticket:jti:{jti}` es
  rechazado; la conexión se cierra con código `4401`.
- **AC-07** `[integration]` Una conexión que no envía `session.hello` en 5 s se cierra con código `4408`.
- **AC-08** `[contract]` Todo mensaje emitido por el servidor valida contra el JSON Schema exportado por
  `@empires-online/protocol` para su tipo, incluido el envelope `{ v, type, seq, ts, requestId?, payload }`
  con `v = 1`.
- **AC-09** `[integration]` El campo `seq` es estrictamente monótono creciente dentro de una conexión.
- **AC-10** `[integration]` Un mensaje mayor que `EO_WS_MAX_MESSAGE_BYTES` (16384) produce
  `MESSAGE_TOO_LARGE`; superar 20 msg/s con burst 40 produce `RATE_LIMITED` y, si persiste, cierre `4429`.
- **AC-11** `[integration]` Reenviar un `unit.move` con el mismo `requestId` devuelve **la respuesta
  original** y **no** crea un segundo movimiento; se comprueba en Redis (`idem:{playerId}:{requestId}`) y
  en `idempotency_keys`.

### 4.3 Interés y sincronización

- **AC-12** `[integration]` Al conectar, `world.snapshot` contiene únicamente entidades de los chunks
  dentro de `EO_INTEREST_RADIUS_CHUNKS = 2` alrededor de la ciudad del jugador, y ninguna de fuera.
- **AC-13** `[integration]` Tras el snapshot inicial, el servidor no vuelve a enviar el mundo completo:
  toda actualización llega como `entity.spawn` / `entity.update` / `entity.despawn`.
- **AC-14** `[integration]` Un `session.view` que desplaza el centro provoca `entity.spawn` de las
  entidades que entran en el AoI y `entity.despawn` de las que salen.

### 4.4 Movimiento y pathfinding

- **AC-15** `[unit]` Dado el escenario de la sección 3.3, la polilínea generada es exactamente la mostrada,
  con `tMs` `0`, `600`, `1449`, `2409`, `2769`.
- **AC-16** `[unit]` La `Authoritative Position` calculada para un `T` arbitrario coincide con el último
  waypoint cuyo `tMs <= (T - start_time_ms)`, sin ejecutar ticks.
- **AC-17** `[unit]` A\* nunca produce una diagonal que atraviese una esquina bloqueada (*corner cutting*).
- **AC-18** `[unit]` Dos ejecuciones de A\* sobre la misma entrada devuelven **el mismo path**, incluido el
  desempate por `(f, h, y, x)`.
- **AC-19** `[unit]` Una petición que excede `EO_PATHFINDING_MAX_NODES` (20000) o
  `EO_PATHFINDING_MAX_DISTANCE` (256) devuelve `PATH_TOO_LONG`; un destino inalcanzable devuelve
  `PATH_NOT_FOUND`.
- **AC-20** `[integration]` Un `unit.move` sobre una unidad de otro jugador devuelve `UNIT_NOT_OWNED`;
  sobre una unidad inexistente, `UNIT_NOT_FOUND`; sobre una unidad `GARRISONED`, `UNIT_GARRISONED`.
- **AC-21** `[integration]` Emitir un `unit.move` para una unidad que ya tiene un movimiento `ACTIVE` deja
  el anterior en `CANCELLED` y el nuevo en `ACTIVE`, **en la misma transacción**, y en ningún instante
  existen dos movimientos `ACTIVE` para la misma unidad.
- **AC-22** `[integration]` `unit.cancel_move` deja el movimiento en `CANCELLED`, la unidad en el último
  waypoint alcanzado y `units.status = IDLE`, y emite `unit.movement.cancelled`.

### 4.5 Game loop y determinismo temporal

- **AC-23** `[simulation]` Con `FakeClock`, avanzar 10 s desde un estado conocido produce un estado final
  **exacto** y reproducible entre ejecuciones.
- **AC-24** `[unit]` Las ocho fases del tick se ejecutan siempre en el orden canónico; ninguna fase realiza
  I/O bloqueante contra PostgreSQL.
- **AC-25** `[integration]` Con 3 unidades en movimiento simultáneo, el percentil observado de
  `eo_game_tick_duration_seconds` se mantiene por debajo del período de 100 ms y
  `eo_game_tick_overruns_total` no se incrementa durante la ejecución de la prueba.

### 4.6 Persistencia, presencia y recuperación

- **AC-26** `[integration]` El inicio y la finalización de un movimiento se escriben en `unit_movements`
  como una **transacción propia**, encolada por el tick hacia los workers de persistencia y no acumulada
  hasta el siguiente flush. El tick no ejecuta ninguna consulta a PostgreSQL. La ventana entre la
  aceptación en RAM y el `COMMIT` es un RPO documentado: ver `DEBT-17` en
  [../roadmap/backlog.md](../roadmap/backlog.md).
- **AC-27** `[integration]` Las posiciones consolidadas se escriben mediante flush a más tardar cada
  `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` (50 ticks = 5 s).
- **AC-28** `[recovery]` Con un movimiento `ACTIVE` en curso, matar y reiniciar el proceso deja la unidad
  en la posición que corresponde al tiempo transcurrido; si `arrival_time_ms <= now`, el movimiento queda
  `COMPLETED` con snap al tile final.
- **AC-29** `[simulation]` Al cerrar la conexión, la ciudad **no** pasa a `OFFLINE_PENDING` mientras no
  haya vencido el margen de reconexión (`DisconnectGrace` = `EO_PRESENCE_TTL_SECONDS`, 30 s), que el game
  loop evalúa en RAM; un jugador con otra sesión abierta sigue `ONLINE`. La clave
  `presence:player:{playerId}` publica la presencia hacia fuera, pero no arbitra la transición.
- **AC-30** `[simulation]` Desde `OFFLINE_PENDING`, tras `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS`
  (300) la ciudad pasa a `PROTECTED`; reconectar desde `OFFLINE_PENDING` o desde `PROTECTED` la devuelve a
  `ONLINE` y limpia `protection_until`.
- **AC-31** `[integration]` Ningún estado durable se pierde al cerrar todas las conexiones WebSocket: el
  mundo sigue avanzando y los cambios siguen persistiéndose.

### 4.7 Cliente y flujo completo

- **AC-32** `[manual]` El cliente dibuja el terreno del AoI con la proyección isométrica canónica
  (`TILE_W = 64`, `TILE_H = 32`) y ninguna coordenada de pantalla viaja en el protocolo.
- **AC-33** `[manual]` Seleccionar un villager y hacer clic en un tile válido produce un único `unit.move`
  y un movimiento visualmente continuo por interpolación sub-tile.
- **AC-34** `[manual]` El recorrido completo de la sección 3.1 se ejecuta de principio a fin —incluyendo
  cerrar el navegador durante el movimiento y volver a entrar— y el estado mostrado al reconectar coincide
  con el estado autoritativo en base de datos.

### 4.8 Calidad de entrega

- **AC-35** `[integration]` `GET /health` responde sin consultar dependencias; `GET /ready` responde
  correctamente solo si PostgreSQL, Redis y el game loop están vivos.
- **AC-36** `[integration]` Las catorce métricas `eo_*` declaradas están expuestas en
  `EO_METRICS_ADDR/metrics` (`:9090`) y los logs incluyen `player_id`, `session_id`, `request_id` y `tick`
  cuando aplican.
- **AC-37** Cada uno de los **22 códigos de error** del catálogo alcanzable en el MVP tiene al menos un
  test que lo provoca y comprueba el campo `code` de `system.error` (nunca el texto humano). Un contract
  test en Go verifica además que el catálogo coincide con el exportado por `@empires-online/protocol`.
- **AC-38** El pipeline de CI ejecuta `format → lint → typecheck → unit → integration → build → docker build`
  y está en verde. **Pendiente**: la CI todavía no existe en el repositorio.

---

## 5. Correspondencia con los milestones

El vertical slice se construye recorriendo M0 a M4; M5 aporta la parte de protección offline que el flujo
end-to-end exige. M6 y M7 quedan como fundamentos preparados, no como funcionalidad entregada.

La columna **Estado real** refleja lo que hay hoy en el repositorio, no lo planificado. El detalle
entregable por entregable está en [../roadmap/milestones.md](../roadmap/milestones.md).

| Milestone | Contenido | Estado en el MVP | Estado real |
|---|---|---|---|
| **M0** Foundation | Monorepo, configuración, Docker Compose, migraciones, protocolo | Dentro | **Hecho** salvo la CI, que falta |
| **M1** World | Grid, tiles, chunks, generación determinista | Dentro | **Hecho**, con tests en verde |
| **M2** Player & City | `players`, `cities`, `units` iniciales | Dentro | **Hecho** del lado servidor (alta vía `POST /api/auth/register`) |
| **M3** Realtime | WebSocket, auth, snapshot, deltas, reconexión | Dentro | **Parcial**: servidor hecho; falta el cliente |
| **M4** Movement | A\*, loop, persistencia del movimiento, interpolación | Dentro | **Parcial**: servidor hecho; falta la interpolación del cliente |
| **M5** Offline protection & Safe Zones | Presencia y protección **sí**; Safe Zones con lógica mínima | Parcial | **Parcial**: presencia y protección hechas; `safe_zones` sólo tabla |
| **M6** Territories | Tablas creadas, lógica diferida | Fundamento | Tablas creadas; lógica diferida |
| **M7** Diplomacy foundation | `treaties` y `garrisons` creados, lógica diferida | Fundamento | Tablas creadas; lógica diferida |

Ver [../roadmap/milestones.md](../roadmap/milestones.md) para el criterio de salida de cada uno.

---

## 6. Cómo se decide si algo entra

Ante cualquier propuesta de ampliar el slice, se aplican estas tres preguntas en orden. Basta un "no" para
excluirla:

1. **¿Es necesaria para ejecutar el flujo de la sección 3 de principio a fin?** Si no, va al backlog.
2. **¿Su ausencia obligaría a rehacer el esquema, el protocolo o el modelo de identidad más adelante?** Si
   sí, entra aunque sea con lógica mínima (es el caso de `territories`, `treaties`, `garrisons`,
   `safe_zones` y `world_events`).
3. **¿Reduce el riesgo técnico principal —persistencia continua y reconstrucción del movimiento— o solo
   añade contenido?** Si solo añade contenido, va al backlog.

Ver [../roadmap/backlog.md](../roadmap/backlog.md).

---

## Documentos relacionados

- [vision.md](vision.md) — por qué este es el riesgo que hay que despejar primero.
- [game-pillars.md](game-pillars.md) — los pilares que el slice demuestra.
- [../specs/movement.md](../specs/movement.md) — la spec del sistema central del MVP.
- [../testing/strategy.md](../testing/strategy.md) — cómo se verifican estos criterios.
- [../roadmap/roadmap.md](../roadmap/roadmap.md) — qué viene después.
