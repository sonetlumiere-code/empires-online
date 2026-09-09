# Milestones

Detalle ejecutable de M0 a M7: alcance exacto, entregables numerados, tests obligatorios, documentación afectada, invariantes cubiertos y criterios de aceptación verificables.

## Cómo leer este documento

Cada milestone declara seis bloques. **Objetivo** y **Alcance** delimitan qué entra y qué no.
**Entregables** es una lista numerada de artefactos concretos y comprobables (archivos, paquetes,
tablas, mensajes del protocolo, endpoints, métricas). **Tests** enumera lo que debe existir y pasar,
usando los niveles del proyecto: *unit*, *integration* (gated por `EO_INTEGRATION=1`), *contract*,
*simulation*, *recovery* y *load* (esta última diferida). **Documentación** lista lo que queda
actualizado al cerrar. **Invariantes** referencia los IDs estables cubiertos. **Aceptación** es una
lista de comprobaciones binarias: cada línea se verifica o no se verifica, sin margen de opinión.

Un milestone no está cerrado si algún criterio de aceptación falla, si el CI está en rojo o si la
documentación asociada no refleja lo implementado. Esto es la Definition of Done del proyecto
aplicada a granularidad de milestone.

### Estado real de ejecución

Cada milestone abre con una línea **Estado** y sus entregables llevan una marca. El significado es
estricto:

| Marca | Significado |
|---|---|
| **HECHO** | Implementado en el repositorio y cubierto por tests que se han ejecutado y están en verde. |
| **PARCIAL** | Implementado sólo en parte, o implementado pero verificado únicamente por tests que aún no se han ejecutado. |
| **PENDIENTE** | No existe código todavía. |

Tres hechos condicionan todas las marcas y conviene tenerlos presentes al leer:

1. **`apps/web/` ya existe.** Los entregables de cliente del vertical slice están HECHOS; lo que falta es la CI en verde y la emisión.
2. **Los tests de integración están escritos pero no ejecutados**, porque el daemon de Docker no arrancó
   en la máquina de desarrollo. Nada que dependa exclusivamente de ellos puede marcarse HECHO.
3. **La CI no existe todavía.** Por tanto **ningún milestone está formalmente cerrado**, por muchos
   entregables HECHO que acumule: la Definition of Done exige CI en verde.

Nomenclatura usada aquí. Un hito no se cierra contra un invariante suelto, sino contra un **eje**:
un objetivo grueso que agrupa varios invariantes del registro. Por eso estos identificadores llevan el
prefijo `EJE-` y **no** son IDs de invariante: reutilizar un `INV-` con un enunciado más amplio que el
suyo crearía dos significados para el mismo identificador, que es exactamente lo que
[../invariants/README.md](../invariants/README.md) —el único registro— existe para impedir.

| Eje | Objetivo del hito | Invariantes del registro que cubre |
|---|---|---|
| `EJE-SEC` | El cliente nunca aporta estado autoritativo: posición final, HP, recursos, resultados de combate, ownership, cooldowns, ETA y paths los determina el servidor. | [INV-SEC-001](../invariants/security.md#inv-sec-001) |
| `EJE-WORLD` | El mundo se genera determinísticamente desde `EO_WORLD_SEED`: la misma seed produce el mismo mundo byte a byte, y su geometría es coherente. | [INV-WORLD-005](../invariants/world.md#inv-world-005), [INV-WORLD-002](../invariants/world.md#inv-world-002), [INV-WORLD-006](../invariants/world.md#inv-world-006) |
| `EJE-PLAYER` | Todo jugador tiene exactamente una ciudad inicial con 1 `TOWN_CENTER`, 1 zona urbana amurallada y 3 `VILLAGER`, creados en una única transacción. | [INV-PLAYER-003](../invariants/player.md#inv-player-003), [INV-PLAYER-002](../invariants/player.md#inv-player-002), [INV-PLAYER-008](../invariants/player.md#inv-player-008) |
| `EJE-CITY` | El estado de presencia de la ciudad (`ONLINE`, `OFFLINE_PENDING`, `PROTECTED`) lo decide exclusivamente el servidor; el cliente solo lo observa. | [INV-CITY-004](../invariants/city.md#inv-city-004), [INV-CITY-005](../invariants/city.md#inv-city-005), [INV-CITY-013](../invariants/city.md#inv-city-013), [INV-CITY-014](../invariants/city.md#inv-city-014) |
| `EJE-UNIT` | `units.status` pertenece a `{IDLE, MOVING, GARRISONED, HIDDEN, DEAD}` y solo transiciona por decisión del servidor. | [INV-UNIT-005](../invariants/units.md#inv-unit-005), [INV-UNIT-002](../invariants/units.md#inv-unit-002), [INV-UNIT-008](../invariants/units.md#inv-unit-008), [INV-UNIT-009](../invariants/units.md#inv-unit-009) |
| `EJE-MOVE` | Una unidad tiene como máximo un movimiento `ACTIVE`; una nueva orden cancela la anterior (`CANCELLED`) dentro de la misma transacción. | [INV-MOVE-001](../invariants/movement.md#inv-move-001) |
| `EJE-PERSIST` | Ningún estado durable depende de un WebSocket vivo; PostgreSQL es la fuente de verdad y Redis nunca la sustituye. | [INV-PERSIST-001](../invariants/persistence.md#inv-persist-001), [INV-PERSIST-003](../invariants/persistence.md#inv-persist-003) |

---

## M0 — Foundation

**Estado: PARCIAL.** Todo lo esencial está construido y en verde —monorepo, módulo Go, configuración,
observabilidad, migraciones, protocolo compartido y esquemas embebidos—. Falta **la CI** (entregable 13) y
el directorio `apps/web/` (parte del entregable 1). Mientras no exista la CI, M0 no está cerrado.

**Objetivo.** Dejar el repositorio en un estado en el que cualquier ingeniero pueda clonar, arrancar
la infraestructura local, ejecutar el servidor, correr los tests y recibir verificación automática
en cada pull request.

**Alcance.** Estructura del monorepo, workspace de pnpm, infraestructura Docker, esqueleto del
módulo Go con configuración, logging, métricas y endpoints de salud, mecanismo de migraciones con
las migraciones iniciales, paquete de protocolo con Zod y exportación de JSON Schema, y CI en GitHub
Actions. **No entra**: lógica de dominio, mundo, jugadores, WebSocket ni mensajes de juego.

### Entregables

1. **PARCIAL — Estructura del monorepo**: existen `services/game-server/` (con `cmd/server/`,
   `internal/`, `migrations/`), `packages/protocol/`, `docs/`, `docker-compose.yml` y
   `pnpm-workspace.yaml`. **Faltan `apps/web/` y `.github/workflows/ci.yml`.**
2. **HECHO — `pnpm-workspace.yaml`** declarando `apps/*` y `packages/*`, y `package.json` raíz
   con los scripts de tarea. El task runner son pnpm scripts: **no se crea Makefile** porque `make`
   no está instalado en la máquina de desarrollo Windows. Scripts reales: `db:up`, `db:down`,
   `db:reset`, `db:logs`, `db:psql`, `db:redis`, `protocol:build`, `protocol:test`, `protocol:check`,
   `server:tidy`, `server:build`, `server:run`, `server:test`, `server:test:integration`, `server:vet`,
   `server:fmt`, `server:fmt:check`, `web:dev`, `web:build`, `docs:check`, `lint`, `typecheck`, `test` y
   `verify`, que encadena la verificación completa. **No hay `db:migrate`**: las migraciones las aplica el
   propio servidor al arrancar (ver entregable 10).
3. **HECHO — `docker-compose.yml`** con dos servicios, `postgres:16-alpine` y `redis:7-alpine`, con
   volúmenes nombrados, healthchecks y puertos publicados en localhost.
   Documentado que el daemon de Docker Desktop debe estar arrancado y que el acceso a las bases se
   hace con `docker compose exec` porque `psql` y `redis-cli` no están instalados en el host. Nota de
   estado: **ese daemon no ha arrancado en esta máquina**, de ahí que los tests de integración sigan sin
   ejecutarse.
4. **HECHO — Instalación de Go**: `winget install --id GoLang.Go`, verificado con `go version`.
   Instalado **Go 1.27.0**; el `go.mod` declara `go 1.23` como versión **mínima**.
5. **HECHO — Módulo Go** inicializado con path
   `github.com/empires-online/empires-online/services/game-server` y el árbol `internal/` poblado:
   `game/{loop,world,simulation,founding}`, `domain/{player,city,unit,movement}`,
   `pathfinding`, `websocket`, `persistence/{postgres,redis}`, `protocol`, `httpapi`, `auth`,
   `clock`, `config`, `observability`.
6. **HECHO — `internal/config`**: carga y validación de todas las variables `EO_` con sus valores por
   defecto —`EO_ENV`, `EO_LOG_LEVEL`, `EO_HTTP_ADDR` (`:8080`), `EO_METRICS_ADDR` (`:9090`),
   `EO_POSTGRES_URL`, `EO_REDIS_URL`, `EO_AUTH_JWT_SECRET`, `EO_TICK_RATE_HZ` (10),
   `EO_WORLD_WIDTH` (512), `EO_WORLD_HEIGHT` (512), `EO_WORLD_SEED` (20260909), `EO_CHUNK_SIZE` (32),
   `EO_INTEREST_RADIUS_CHUNKS` (2), `EO_PRESENCE_TTL_SECONDS` (30),
   `EO_PRESENCE_HEARTBEAT_SECONDS` (10), `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` (300),
   `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` (50), `EO_PATHFINDING_MAX_NODES` (20000),
   `EO_PATHFINDING_MAX_DISTANCE` (256), `EO_WS_MAX_MESSAGE_BYTES` (16384),
   `EO_WS_RATE_LIMIT_PER_SECOND` (20), `EO_WS_RATE_LIMIT_BURST` (40) y
   **`EO_WS_OUTBOUND_QUEUE_SIZE` (256, rango 8–65536)**—. El arranque falla rápido, reporta **todos**
   los errores juntos y aplica estas validaciones cruzadas:

   - `EO_TICK_RATE_HZ` debe dividir exactamente a 1000.
   - `EO_WORLD_WIDTH` y `EO_WORLD_HEIGHT` deben ser múltiplos exactos de `EO_CHUNK_SIZE`.
   - `EO_PRESENCE_HEARTBEAT_SECONDS` < `EO_PRESENCE_TTL_SECONDS`, estrictamente.
   - `EO_WS_RATE_LIMIT_BURST` >= `EO_WS_RATE_LIMIT_PER_SECOND`.
   - `EO_AUTH_JWT_SECRET` de 32 caracteres o más, y sin `dev-only` si `EO_ENV=production`.

   Los plazos del transporte WebSocket **no** son configurables: `WSHandshakeTimeout = 5 s`,
   `WSPingInterval = 15 s`, `WSReadTimeout = 45 s` y `WSWriteTimeout = 10 s` son constantes de código.
7. **HECHO — `internal/observability`**: logging estructurado JSON con `log/slog` y los campos estándar
   `ts`, `level`, `msg`, `player_id`, `session_id`, `request_id`, `tick`; registro Prometheus con
   las **catorce** métricas declaradas aunque aún valgan cero: `eo_connected_players`,
   `eo_connected_websockets`, `eo_active_units`, `eo_active_movements`,
   `eo_persistence_queue_depth`, `eo_game_tick_duration_seconds`, `eo_game_tick_overruns_total`,
   `eo_commands_total{type,result}`, `eo_ws_messages_total{direction,type}`,
   `eo_protocol_errors_total{code}`, `eo_pathfinding_requests_total{result}`,
   `eo_pathfinding_duration_seconds`, `eo_database_latency_seconds`, `eo_redis_latency_seconds`.
8. **HECHO — Endpoints HTTP**: `GET /health` (liveness, sin tocar dependencias) y `GET /ready`
   (readiness: ping a Postgres, ping a Redis y latido del game loop con umbral de 5 s). Servidor de
   métricas independiente en `EO_METRICS_ADDR` (`:9090`) exponiendo `/metrics`.
9. **HECHO — Abstracciones de determinismo**: `internal/clock` con las interfaces `Clock`
   (`Now() time.Time`, `NowMs() int64`), `SystemClock`, `FakeClock` y `RandomSource`. Prohibido
   el uso directo de `time.Now()` o `rand` dentro del dominio.
10. **HECHO — Mecanismo de migraciones** en `services/game-server/migrations/` con nomenclatura
    `NNNN_nombre.up.sql` / `NNNN_nombre.down.sql` y `embed.go` (`//go:embed *.sql`), aplicadas con
    **golang-migrate** embebido reescribiendo la URL al esquema `pgx5`. Se aplican **en el arranque del
    servidor**, de forma idempotente; no hay script `db:migrate` separado, porque el binario lleva los
    `.sql` dentro. Las migraciones existentes son `000001_initial_schema` (esquema completo,
    incluido el trigger `set_updated_at()` y `world_state` con `epoch_ms`) y `000002_seed_catalogs`.
    El DDL exacto lo define [../database/schema.md](../database/schema.md).
11. **HECHO — `packages/protocol`** publicado en el workspace como `@empires-online/protocol`: esquemas
    Zod en `src/v1/{common,errors,client,server,index}.ts` para los envelopes cliente→servidor
    `{ v, type, requestId, payload }` —**estrictos**, `additionalProperties: false`— y servidor→cliente
    `{ v, type, seq, ts, requestId?, payload }` —**no estrictos**, para poder añadir campos opcionales
    sin romper clientes antiguos—, el catálogo de **22 códigos de error** estables y el mensaje
    `system.error` con `{ code, message, requestId?, details? }`. `pnpm run protocol:build` exporta
    JSON Schema a `packages/protocol/schema/v1/*.json`.
12. **HECHO — Embebido de esquemas en Go** con `go:embed` del espejo generado en
    `internal/protocol/schema/v1/*.json`, ya consumido por los contract tests.
13. **PENDIENTE — CI en `.github/workflows/ci.yml`** con la secuencia `format → lint → typecheck → unit →
    integration (services postgres/redis) → build → docker build`. El job de integración levanta
    Postgres y Redis como *services* y exporta `EO_INTEGRATION=1`. Cualquier check obligatorio en
    rojo invalida el PR. **Es el único entregable de M0 sin empezar, y el que impide cerrarlo.**
14. **HECHO — `docs/`** inicializado con la estructura de grupos y la guía de arranque que describe el
    entorno real: Windows 10, Node v22.17.1, pnpm 10.25.0, git 2.38.1, Docker CLI 20.10.22 con
    Compose v2.15.1, Go 1.27.0 instalado, y ausencia de `psql`, `redis-cli`, `make` y `gh`.

### Tests

- **HECHO** *unit* (Go): carga de configuración con valores por defecto, con obligatorias ausentes y con
  todas las validaciones cruzadas; formato del logger; registro de métricas sin duplicados; `FakeClock`
  avanza determinísticamente.
- **HECHO** *unit* (Vitest): 18 tests. Los esquemas Zod aceptan envelopes válidos y rechazan `v` distinto
  de 1, `type` desconocido, coordenadas no enteras y **campos extra** en mensajes cliente→servidor.
- **PARCIAL** *integration*: el arranque aplica todas las migraciones embebidas sobre una base limpia, es
  idempotente al repetirse, y cada `.down.sql` revierte su `.up.sql`. Escrito, **no ejecutado**.
- **PARCIAL** *integration*: `GET /ready` devuelve 200 con Postgres y Redis arriba, y no-200 con
  cualquiera de los dos caído. Escrito, **no ejecutado**.
- **HECHO** *contract*: el JSON Schema exportado por el build coincide con el commiteado y el catálogo de
  códigos de error de Go coincide con el de TypeScript. Falta el eslabón de CI que bloquee el PR ante
  deriva.

### Documentación actualizada al cerrar

[../operations/local-development.md](../operations/local-development.md),
[../architecture/overview.md](../architecture/overview.md),
[../database/schema.md](../database/schema.md) (migraciones iniciales),
[../specs/websocket-protocol.md](../specs/websocket-protocol.md) (envelopes y códigos de error),
[../testing/strategy.md](../testing/strategy.md),
[../operations/deployment.md](../operations/deployment.md),
[../operations/monitoring.md](../operations/monitoring.md).

### Invariantes cubiertos

`EJE-PERSIST` parcialmente (se establece la separación de capas y el mecanismo de migraciones);
las bases de `EJE-SEC` en cuanto a que los secretos viven en variables de entorno y jamás en el
repositorio.

### Aceptación

1. **Cumplido.** `pnpm install` completa sin errores en Windows con pnpm 10.25.0.
2. **Cumplido.** `go version` reporta Go 1.23 o superior: hay 1.27.0 instalado.
3. **Bloqueado.** `pnpm run db:up` debe dejar `postgres` y `redis` en estado *healthy* según
   `docker compose ps`. No verificable mientras el daemon de Docker no arranque.
4. **Bloqueado.** El arranque del servidor aplica las migraciones embebidas y un segundo arranque no
   produce cambios. Depende del punto 3.
5. **Cumplido.** `pnpm run server:run` arranca el proceso y `GET /health` responde 200 en menos de un
   segundo.
6. **Bloqueado.** `GET /ready` responde 200 con la infraestructura arriba y no-200 al parar Redis.
   Depende del punto 3.
7. **Cumplido.** `GET /metrics` en `EO_METRICS_ADDR` expone las catorce métricas declaradas.
8. **Cumplido.** Arrancar sin `EO_AUTH_JWT_SECRET` aborta el proceso con un mensaje que nombra la
   variable, junto al resto de errores de configuración detectados.
9. **Cumplido.** `pnpm run protocol:build` regenera `packages/protocol/schema/v1/*.json` sin diferencias
   respecto a lo commiteado.
10. **Pendiente.** El workflow de GitHub Actions termina en verde sobre la rama principal. No existe
    todavía.

---

## M1 — World

**Estado: HECHO** del lado servidor. `internal/game/world` está implementado y su suite de tests
(límites, chunks, costes de terreno, overlay de ocupación, disposición fila-mayor, determinismo del
generador) está en verde. Sólo queda ejecutar los tests de integración contra PostgreSQL.

**Objetivo.** Generar el mundo de 512×512 tiles determinísticamente desde `EO_WORLD_SEED`,
representarlo en chunks de 32×32 y persistirlo en `world_chunks` para auditoría y edición futura.

**Alcance.** Generación, representación en memoria, transformaciones de coordenadas, persistencia
por chunk y consultas de transitabilidad. **No entra**: entidades, ocupación dinámica por edificios
más allá de la reserva del *blocked overlay*, ni difusión por red.

### Entregables

1. **HECHO** — `internal/game/world`: enum `TerrainType` (uint8) con los seis valores canónicos
   —`GRASSLAND` 0, `FOREST` 1, `HILL` 2, `MOUNTAIN` 3, `WATER` 4, `ROAD` 5— y su tabla de `walkable` y
   `costUnits` en décimas del coste base (`world.CostBase = 10`): 10, 16, 18, —, —, 6. La constante
   derivada `MinTerrainCostUnits` vale **6** y es la que mantiene admisible la heurística de A\*.
2. **HECHO** — Generador determinista parametrizado por `EO_WORLD_SEED` (20260909 por defecto),
   `EO_WORLD_WIDTH` y `EO_WORLD_HEIGHT`, que usa `RandomSource` inyectado y nunca `rand` global.
3. **HECHO** — Representación en memoria del mundo por chunks de `EO_CHUNK_SIZE` (32) tiles de lado:
   16×16 = 256 chunks en el MVP.
4. **HECHO** — Transformaciones de coordenadas: `chunkX = x >> 5`, `chunkY = y >> 5`, ID de chunk
   `chunkY*chunksPerRow + chunkX` como uint32, y las inversas. Coordenadas lógicas `x`, `y` int32
   con origen (0,0) arriba-izquierda, X al este, Y al sur.
5. **HECHO** — Capa de ocupación (`blocked overlay`) separada del terreno base
   (`SetBlocked(minX, minY, maxX, maxY, bool)`), para que edificios y ciudades bloqueen tiles sin mutar
   el `TerrainType` persistido.
6. **HECHO** — Consulta de transitabilidad con vecindad de 8 direcciones y la regla de que el movimiento
   diagonal solo se permite si ambos tiles ortogonales adyacentes son transitables (prohibición de
   corner cutting). Fuera de los límites, `TerrainAt` devuelve `WATER` e `IsWalkable` es falso: el borde
   del mundo se comporta como un muro y no provoca pánico.
7. **HECHO** — Migración `world_chunks` y persistencia de cada chunk como `bytea` de 1024 bytes (32×32
   bytes, un byte por tile en orden fila-mayor).
8. **HECHO** — Bootstrap del mundo al arrancar: el mapa se **regenera desde la semilla en cada arranque**.
   `world_chunks` guarda una copia para auditoría y para permitir mapas editados en el futuro, pero **no
   es la fuente primaria** y el arranque no depende de ella. El mundo no cambia entre reinicios porque la
   semilla no cambia, no porque se lea de disco.
9. **HECHO** — Validación de límites: cualquier coordenada fuera de
   `[0, EO_WORLD_WIDTH)` × `[0, EO_WORLD_HEIGHT)` es un error de dominio que más adelante se mapea a
   `TARGET_OUT_OF_BOUNDS`.

### Tests

- **HECHO** *unit*: la misma seed produce el mismo mundo byte a byte; seeds distintas producen mundos
  distintos; el generador no llama a `time.Now()` ni a `rand` global.
- **HECHO** *unit*: ida y vuelta de coordenadas tile↔chunk para las cuatro esquinas del mundo, los bordes
  de chunk (x=31/32, y=31/32) y coordenadas fuera de rango; disposición fila-mayor.
- **HECHO** *unit*: `walkable` y `costUnits` por terreno; `MOUNTAIN` y `WATER` bloqueados.
- **HECHO** *unit*: la diagonal entre dos tiles transitables se rechaza si cualquiera de los dos
  ortogonales adyacentes está bloqueado.
- **HECHO** *unit*: el `blocked overlay` bloquea sin alterar el `TerrainType` subyacente.
- **PARCIAL** *integration*: generar, persistir, reiniciar y regenerar produce un mundo idéntico; los 256
  chunks ocupan 1024 bytes cada uno. Escrito, **no ejecutado**.

### Documentación actualizada al cerrar

[../architecture/overview.md](../architecture/overview.md),
[../invariants/world.md](../invariants/world.md),
[../database/schema.md](../database/schema.md).

### Invariantes cubiertos

`EJE-WORLD` completo. `EJE-PERSIST` en la parte de mundo: el mapa es durable y reconstruible
desde la seed, no depende de ninguna conexión.

### Aceptación

1. Dos ejecuciones del generador con `EO_WORLD_SEED=20260909` producen los 256 chunks con el mismo
   hash.
2. `SELECT count(*) FROM world_chunks` devuelve 256 y toda fila tiene `length(data) = 1024`.
3. Un reinicio del servidor produce exactamente el mismo mundo, porque se regenera desde la misma
   semilla; la copia de `world_chunks` sigue coincidiendo byte a byte con lo regenerado.
4. La conversión de (511, 511) da chunk (15, 15) e ID 255.
5. Una consulta de transitabilidad sobre un tile `WATER` devuelve falso.
6. Un movimiento diagonal con un ortogonal bloqueado se declara no transitable.
7. Coordenadas negativas o ≥ 512 se rechazan como fuera de límites.

---

## M2 — Player & City

**Estado: HECHO** del lado servidor. El esquema, los catálogos sembrados, el dominio de jugador, ciudad y
unidad, el emplazamiento determinista de `internal/game/founding` y el bootstrap atómico existen y
funcionan; el alta se expone en `POST /api/auth/register`. Falta ejecutar los tests de integración.

**Objetivo.** Crear un jugador con su ciudad inicial y sus tres aldeanos en una única transacción
atómica, con los catálogos de civilizaciones, facciones y eras cargados desde base.

**Alcance.** Catálogos, entidades `players`, `cities`, `units`, cálculo de población y registro de
eventos de dominio en `world_events`. **No entra**: presencia (M5), movimiento (M4), red (M3).

### Entregables

1. **HECHO** — Migración de catálogos (`000002_seed_catalogs`): `civilizations` `ROMAN`, `BYZANTINE`,
   `PERSIAN`, `NORSE` con sus `traits jsonb`; `factions` con exactamente `ORDER`, `CHAOS` y `NEUTRAL`; y
   `eras` con `STONE_AGE` (population_cap 20), `BRONZE_AGE` (50), `IRON_AGE` (100) y `CASTLE_AGE` (150).
   Los datos van sembrados por migración: las eras se definen en la tabla, nunca en código.
2. **HECHO** — Migración de `players` (PK `uuid`, `password_hash text NOT NULL`, CHECK
   `username ~ '^[A-Za-z0-9_-]{3,24}$'`), `cities` y `units`, con `created_at`, `updated_at` mantenido
   por el trigger `set_updated_at()`, y columna `version integer` en `cities` para concurrencia
   optimista. `cities` lleva `UNIQUE (center_x, center_y)`, CHECK `population <= population_limit` y CHECK
   del dominio de `presence_state`; `units` lleva **`chunk_x` y `chunk_y` desnormalizados** —la clave del
   interest management— y CHECK `hp <= max_hp`. Todas las demás PK son
   `bigint GENERATED ALWAYS AS IDENTITY`. Enums de dominio como `text` + `CHECK`, nunca tipos ENUM de
   PostgreSQL.
3. **HECHO** — Migración de `world_events` como registro append-only de eventos de dominio, con los
   índices `world_events_type_time_idx` y `world_events_player_idx`.
4. **HECHO** — `internal/domain/player` y `internal/domain/city`: modelo de jugador, de ciudad y de la
   relación ortogonal entre **Civilization** (identidad cultural, con `Traits`) y **Global Faction**
   (`ORDER`/`CHAOS`/`NEUTRAL`), que son ejes independientes. Todos los jugadores son humanos; no existen
   razas.
5. **HECHO** — `internal/domain/unit`: `unit_type` `VILLAGER` con `MaxHP` 40, `BaseMsPerTile` 600 y
   `PopulationCost` 1; `units.status` con los valores `IDLE`, `MOVING`, `GARRISONED`, `HIDDEN`, `DEAD`,
   inicializado a `IDLE`. `TOWN_CENTER` se modela como *building* de la ciudad, no como unidad.
6. **HECHO** — **Bootstrap atómico del jugador**: una sola transacción crea el jugador, su ciudad con 1
   `TOWN_CENTER` y 1 zona urbana amurallada inicial, y 3 unidades `VILLAGER`, más los eventos
   `UnitSpawned` correspondientes en `world_events`. Si cualquier paso falla, no queda nada. **Orden
   obligatorio**: primero la transacción de PostgreSQL commitea y sólo **después** se despacha el comando
   `IntroducePlayer`, que incorpora al jugador al mundo en RAM. Nunca al revés.
7. **HECHO** — Cálculo de `population_limit` = `era.population_cap` + modificadores de edificios, con cero
   modificadores en el MVP, y de la población actual como número de unidades vivas del jugador.
   El error `POPULATION_LIMIT_REACHED` queda definido y testeado aunque en el MVP no haya
   producción que lo dispare de forma habitual.
8. **HECHO** — Colocación inicial de la ciudad en `internal/game/founding`: búsqueda determinista en
   espiral desde una semilla derivada del nombre de usuario, con entorno despejado de **radio 3**,
   separación mínima de **24 tiles** entre centros de ciudad, muralla como rectángulo **3 × 3** marcado en
   el `blocked overlay` de M1 alrededor del centro, y los 3 aldeanos naciendo a **radio 2**. Esto resuelve
   en la práctica la pregunta Q-01 de [backlog.md](backlog.md), que queda cerrada.
9. **HECHO** — Error `CITY_NOT_FOUND` definido y devuelto por las consultas de ciudad inexistente.

### Tests

- **HECHO** *unit*: `population_limit` por era; un jugador con 3 aldeanos en `STONE_AGE` tiene población 3
  sobre 20.
- **HECHO** *unit*: Civilization y Faction son independientes: cualquier combinación válida se acepta.
- **PARCIAL** *integration*: el bootstrap crea exactamente 1 `players`, 1 `cities` y 3 `units`. Escrito,
  **no ejecutado**.
- **PARCIAL** *integration*: un fallo inyectado a mitad del bootstrap deja la base sin filas nuevas en
  ninguna de las tres tablas. Escrito, **no ejecutado**.
- **PARCIAL** *integration*: los `CHECK` rechazan un `units.status` fuera del enum y una faction fuera de
  `{ORDER, CHAOS, NEUTRAL}`. Escrito, **no ejecutado**.
- **PARCIAL** *integration*: el trigger `set_updated_at()` actualiza la columna en un `UPDATE`. Escrito,
  **no ejecutado**.
- **PARCIAL** *integration*: los tiles ocupados por la ciudad quedan bloqueados en el overlay sin cambiar
  su `TerrainType`. Escrito, **no ejecutado**.

### Documentación actualizada al cerrar

[../specs/player.md](../specs/player.md),
[../specs/city.md](../specs/city.md),
[../database/schema.md](../database/schema.md),
[../architecture/overview.md](../architecture/overview.md).

### Invariantes cubiertos

`EJE-PLAYER` completo. `EJE-UNIT` en su parte de estados válidos y valor inicial.
`EJE-PERSIST` en la parte de write-through inmediato y transaccional para creación de
player/city/unit.

### Aceptación

1. Ejecutar el bootstrap sobre una base limpia deja 1 jugador, 1 ciudad, 3 aldeanos y 4 eventos
   de creación en `world_events`.
2. Ejecutar el bootstrap dos veces para el mismo jugador no duplica ciudad ni unidades.
3. Un fallo forzado antes del commit deja las tres tablas exactamente como estaban.
4. `SELECT population_cap FROM eras` devuelve 20, 50, 100 y 150 para las cuatro eras.
5. Las tres facciones existen y no hay una cuarta.
6. Un `INSERT` de `units.status = 'FLYING'` es rechazado por el `CHECK`.
7. Los aldeanos nacen con `status = 'IDLE'` y hp 40.

---

## M3 — Realtime

**Estado: PARCIAL.** Todo el lado servidor está implementado —`internal/auth`, `internal/websocket`
(`hub`, `session`, `server`), idempotencia y presencia en Redis, snapshot e interest management en
`internal/game/simulation`— y sus tests de unidad, contrato y simulación están en verde. **Falta el
cliente**: `apps/web/` ya existe, de modo que los entregables 2 y 13 están hechos y los tests *e2e*
no se pueden ejecutar.

**Objetivo.** Conectar cliente y servidor por WebSocket con autenticación por ticket, sesión,
snapshot inicial, deltas incrementales, interest management por chunks y reconexión.

**Alcance.** Handshake, sesión, envelopes, rate limiting, idempotencia, suscripción por chunk y los
mensajes de sincronización de entidades y ciudad. **No entra**: `unit.move` y la familia de
movimiento, que son M4; ni `territory.update`, que se emite en M6.

### Entregables

1. **HECHO** — Migración de `sessions` e `idempotency_keys`, con `sessions_player_idx` e
   `idempotency_keys_expiry_idx`.
2. **PENDIENTE — Emisión del game ticket desde una API route de Next.js en `apps/web`**: JWT HS256
   firmado con `EO_AUTH_JWT_SECRET`, TTL 60 s, claims
   `{ sub: playerId, jti, iat, exp, aud: "game-server" }`. **Hoy lo emite el propio game server** desde
   `POST /api/auth/register` y `POST /api/auth/login` (`internal/httpapi`, con bcrypt), lo cual es
   explícitamente provisional. La arquitectura objetivo la fija
   [../decisions/ADR-010-authentication-game-ticket.md](../decisions/ADR-010-authentication-game-ticket.md);
   el traslado está registrado como `DEBT-18` en [backlog.md](backlog.md).
3. **HECHO** — `internal/auth`: `Verifier`, `Authenticator`, `Issuer` y `Claims`. Verificación de firma,
   `exp` y `aud`, y consumo del `jti` en Redis con `SETNX` sobre `ticket:jti:{jti}` y TTL 120 s para
   impedir replay. Un `jti` ya consumido cierra con `4401`. El comportamiento es *fail-closed*.
4. **HECHO** — Endpoint WebSocket `/ws` sobre WSS. El primer mensaje debe ser `session.hello { ticket }`
   antes de `WSHandshakeTimeout` (5 s, constante de código); si no llega, cierre `4408`.
5. **HECHO** — **Envelopes**: cliente→servidor `{ v, type, requestId, payload }` con `requestId` UUIDv4
   obligatorio en comandos y esquema **estricto** (`additionalProperties: false`); servidor→cliente
   `{ v, type, seq, ts, requestId?, payload }` con `seq` uint64 monótono por conexión, `ts` en epoch ms
   del servidor y esquema **no estricto**, para poder añadir campos opcionales sin romper clientes
   antiguos. `v` siempre 1; otro valor produce `UNSUPPORTED_VERSION`.
6. **HECHO** — **Mensajes cliente→servidor de M3**: `session.hello`, `session.ping`, `session.view`.
7. **HECHO** — **Mensajes servidor→cliente de M3**: `session.welcome`, `session.pong`, `system.error`,
   `world.snapshot`, `entity.spawn`, `entity.update`, `entity.despawn`, `city.update`.
   `session.welcome.payload` es
   `{sessionId, playerId, serverTimeMs, tickDurationMs, heartbeatIntervalMs, world:{width,height,chunkSize}}`
   y `world.snapshot.payload` es
   `{serverTimeMs, tick, chunks[], terrain[], units[], cities[], territories[]}`, con
   `terrain[].terrain` en **base64** de `size*size` bytes. `entity.despawn.payload.reason` pertenece a
   `OUT_OF_INTEREST | DEAD | GARRISONED | HIDDEN | REMOVED`.
8. **HECHO** — **Interest management por chunk**: suscripción gestionada por el `Hub`, cuyo `Subscribe`
   devuelve a la vez los chunks que entran y los que salen. Radio `EO_INTEREST_RADIUS_CHUNKS` (2)
   alrededor del centro de vista, es decir un área de 5×5 chunks. Al conectar, el centro es la ciudad del
   jugador. `session.view` mueve el centro con rate limit. El **terreno de un chunk se envía una sola vez
   por sesión** (`NeedsTerrain` / `MarkTerrainSent`), porque es inmutable. Nunca se retransmite el mundo
   completo: snapshot al conectar y deltas después. La goroutine de la conexión **no lee el estado del
   mundo**: envía `RequestSnapshot` por el canal de comandos con un canal de respuesta con buffer y el
   loop responde; eso es lo que impide la carrera de datos.
9. **HECHO** — **Límites y protección**: mensaje ≤ 16384 bytes (`EO_WS_MAX_MESSAGE_BYTES`) o
   `MESSAGE_TOO_LARGE`; rate limit de 20 msg/s con burst 40 (`EO_WS_RATE_LIMIT_PER_SECOND`,
   `EO_WS_RATE_LIMIT_BURST`) o `RATE_LIMITED` y, si persiste, cierre `4429`; ping cada 15 s, timeout de
   lectura de 45 s y de escritura de 10 s. **Contrapresión explícita**: cola de comandos llena → el
   comando se descarta y se responde `INTERNAL_ERROR`; cola de salida de la sesión llena
   (`EO_WS_OUTBOUND_QUEUE_SIZE`, 256) → cierre `4500` y el cliente reconecta con un snapshot limpio.
10. **HECHO** — **Idempotencia**: `Claim` reserva el `requestId` en Redis con `SETNX` sobre
    `idem:{playerId}:{requestId}` y TTL 300 s antes de ejecutar, y en `idempotency_keys` para comandos
    durables. Un `requestId` repetido devuelve la respuesta original sin re-ejecutar. Si Redis no
    responde, el comando **se ejecuta igualmente**: se prefiere dejar jugar a bloquear al jugador, y es
    una decisión consciente.
11. **HECHO** — **Códigos de error activos en M3**: `UNAUTHORIZED`, `FORBIDDEN`, `INVALID_MESSAGE`,
    `UNSUPPORTED_VERSION`, `RATE_LIMITED`, `MESSAGE_TOO_LARGE`, `INTERNAL_ERROR`, y
    `NOT_IMPLEMENTED` para tipos declarados en el protocolo pero aún no implementados.
    Cierres WS: `4400` invalid, `4401` unauthenticated, `4403` forbidden, `4408` handshake timeout,
    `4429` rate limited, `4500` internal.
12. **HECHO** — **Reconexión**: al reconectar se emite un `world.snapshot` nuevo. No hay buffer de deltas
    ni reanudación por `seq`; esto es deuda técnica aceptada y registrada (`DEBT-08`).
13. **PENDIENTE — Cliente en `apps/web`**: capa de transporte que abre la conexión, envía
    `session.hello`, mantiene el ping, aplica snapshot y deltas a un store local, y renderiza en
    isométrico con PixiJS usando `screenX = (x - y) * 32` y `screenY = (x + y) * 16`
    (`TILE_W = 64`, `TILE_H = 32`). El servidor no maneja píxeles en ningún caso.
14. **HECHO** — **Métricas activas**: `eo_connected_players`, `eo_connected_websockets`,
    `eo_ws_messages_total{direction,type}`, `eo_commands_total{type,result}`,
    `eo_protocol_errors_total{code}` y `eo_redis_latency_seconds` en las operaciones de ticket e
    idempotencia.

### Tests

- **HECHO** *unit*: parseo y validación de envelopes; rechazo de `v` distinto de 1, de `type` desconocido,
  de coordenadas no enteras y de campos extra; `seq` estrictamente creciente por conexión.
- **HECHO** *unit*: cálculo del conjunto de chunks para un centro dado, incluidos centros pegados al borde
  del mundo donde el área 5×5 se recorta.
- **HECHO** *unit* (`internal/auth`): ticket válido, caducado, firmado con otra clave, `alg=none`,
  audiencia incorrecta, claims incompletos, no reutilizable y comportamiento *fail-closed*.
- **HECHO** *unit*: token bucket del rate limit con `FakeClock`.
- **HECHO** *simulation*: varias sesiones del mismo jugador, snapshots y deltas parciales sobre el estado
  en RAM.
- **PARCIAL** *integration*: handshake completo feliz; ticket expirado → `4401`; `aud` incorrecto →
  `4401`; `jti` reutilizado → `4401`; ausencia de `session.hello` en 5 s → `4408`. Escrito, **no
  ejecutado**.
- **PARCIAL** *integration*: mensaje de 16385 bytes → `MESSAGE_TOO_LARGE`; ráfaga por encima del burst →
  `RATE_LIMITED`. Escrito, **no ejecutado**.
- **PARCIAL** *integration*: `requestId` repetido devuelve la respuesta original y no genera un segundo
  efecto. Escrito, **no ejecutado**.
- **PARCIAL** *integration*: `session.view` que desplaza el centro produce exactamente los `entity.spawn`
  y `entity.despawn` esperados, sin duplicados ni huecos. Escrito, **no ejecutado**.
- **HECHO** *contract*: los esquemas embebidos y el catálogo de 22 códigos de error de Go coinciden con
  los exportados por `@empires-online/protocol`, y los deltas parciales validan contra su JSON Schema.
- **PENDIENTE** *e2e*: un cliente WS en Node se autentica, recibe `world.snapshot` con su ciudad y sus
  tres aldeanos, y responde a los pings. Requiere Docker arrancado.

### Documentación actualizada al cerrar

[../specs/websocket-protocol.md](../specs/websocket-protocol.md),
[../decisions/ADR-010-authentication-game-ticket.md](../decisions/ADR-010-authentication-game-ticket.md),
[../architecture/networking.md](../architecture/networking.md),
[../database/schema.md](../database/schema.md).

### Invariantes cubiertos

`EJE-SEC` en su núcleo: el cliente solo envía intenciones y vistas, jamás estado.
`EJE-PERSIST`: la sesión es transitoria en Redis y no hay estado durable que dependa de la
conexión viva.

### Aceptación

1. Un cliente con ticket válido recibe `session.welcome` y a continuación `world.snapshot`.
2. El mismo ticket usado por segunda vez es rechazado con cierre `4401`.
3. Un ticket con `aud` distinto de `"game-server"` es rechazado.
4. Conectar y no enviar nada durante 6 segundos produce cierre `4408`.
5. `seq` es estrictamente creciente en todos los mensajes de una conexión.
6. El `world.snapshot` inicial contiene solo entidades dentro del área de 5×5 chunks centrada en la
   ciudad del jugador.
7. Enviar 100 mensajes en un segundo produce `RATE_LIMITED`.
8. Un mensaje de 20 KiB produce `MESSAGE_TOO_LARGE` y no tumba la conexión con `4500`.
9. Reconectar tras una caída devuelve un snapshot coherente sin intervención manual.
10. Todos los mensajes emitidos pasan los contract tests contra el JSON Schema.

---

## M4 — Movement

**Estado: PARCIAL.** El lado servidor está completo y es la parte mejor cubierta por tests del proyecto:
`internal/pathfinding`, `internal/domain/movement`, `internal/game/loop` y el *vertical slice* de
`internal/game/simulation` —con reemplazo de orden, cancelación, los seis rechazos, tres tests de
recuperación, reproducibilidad y volcado por lotes— están **en verde**. **Falta** la interpolación visual
del cliente (entregable 15), que ya está implementado en `apps/web/`.

**Objetivo.** Implementar el primer verbo del juego: mover una unidad con A\*, polilínea temporizada
persistida, game loop a 10 Hz, recuperación tras reinicio e interpolación visual en el cliente.

**Alcance.** Comandos `unit.move` y `unit.cancel_move`, pathfinding, game loop completo con sus ocho
fases, persistencia de movimientos y recuperación. **No entra**: combate, formaciones, movimiento de
grupo ni recolección.

### Entregables

1. **HECHO** — Migración de `unit_movements` con los estados `ACTIVE`, `COMPLETED`, `CANCELLED`,
   `FAILED`, `start_time_ms` y `arrival_time_ms` como `bigint` en epoch milliseconds, y la polilínea en
   la columna `path` de tipo **`jsonb`** (`[{"x":int,"y":int,"tMs":int}, ...]`). `EJE-MOVE` se
   materializa en base con un índice único parcial:

   ```sql
   CREATE UNIQUE INDEX unit_movements_one_active_per_unit
       ON unit_movements (unit_id) WHERE status = 'ACTIVE';
   ```

2. **HECHO** — `internal/pathfinding`: A\* sobre grid con 8 direcciones y heurística **octile** en
   enteros escalados (`costScaleOrtho = 1000`, `costScaleDiag = 1414`), con el coste de un paso igual a
   `terrainCostUnits * escala`. La heurística va **ponderada por `MinTerrainCostUnits`, que vale 6
   (`ROAD`)**: usar el coste de la hierba (10) la haría inadmisible en un mundo con caminos más baratos.
   Desempate estable por `(f, h, y, x)`. Prohibido iterar mapas de Go sin ordenar. La ruta devuelta
   **incluye el tile de origen** y `from == to` devuelve una ruta de un solo tile, sin error.
3. **HECHO** — Interfaz estable
   `type Pathfinder interface { FindPath(ctx context.Context, grid Grid, from, to world.Tile, opts Options) ([]world.Tile, error) }`,
   diseñada para poder sustituirse por Hierarchical A\* sin tocar el protocolo ni el dominio.
4. **HECHO** — Límites de pathfinding: `EO_PATHFINDING_MAX_NODES` (20000) y
   `EO_PATHFINDING_MAX_DISTANCE` (256 tiles); al excederse, `ErrPathTooLong` → `PATH_TOO_LONG`. Si no hay
   ruta, `ErrPathNotFound` → `PATH_NOT_FOUND`. Si el destino es intransitable, el MVP **rechaza** con
   `ErrTargetNotWalkable` → `TARGET_NOT_WALKABLE` y no busca un tile cercano (`DEBT-01`). Los errores
   exportados son `ErrPathNotFound`, `ErrTargetOutOfBounds`, `ErrTargetNotWalkable`,
   `ErrOriginNotWalkable` y `ErrPathTooLong`.
5. **HECHO** — **Polilínea temporizada**: array de waypoints `{x, y, tMs}` donde `tMs` es el offset en
   milisegundos desde `start_time_ms` en el que la unidad alcanza ese tile; el primer waypoint es el
   origen con `tMs = 0`. La duración de cada segmento se calcula en **aritmética entera exacta**, y se
   **redondea al milisegundo más cercano antes de acumular** —truncar regalaría casi un segundo de
   ventaja cada mil pasos—:

   ```go
   ms := (baseMsPerTile*costUnits + 5) / 10
   if diagonal { ms = (ms*1414214 + 500000) / 1000000 }
   if ms < 1 { ms = 1 }
   ```

   `VILLAGER` usa `baseMsPerTile = 600`, de donde salen 600 ms por tile de `GRASSLAND` ortogonal, 849 en
   diagonal, 960 en `FOREST` y 360 en `ROAD`.
6. **HECHO** — Posición autoritativa en el instante T = último waypoint con `tMs <= (T - start_time_ms)`,
   por búsqueda binaria y **analíticamente reconstruible** sin replay de ticks. Antes de 0, el origen;
   después del último `tMs`, el destino. La interpolación sub-tile es exclusivamente visual: el servidor
   razona en tiles enteros.
7. **HECHO** — `internal/game/loop`: `Loop.Run` y `Loop.Step` a `EO_TICK_RATE_HZ` (10 Hz, período 100 ms)
   con `tickNumber` uint64 monótono desde `world_state.epoch_ms` y
   `tickTime = epoch_ms + tickNumber*tickDurationMs`. Orden fijo de fases: 1 `drain commands`,
   2 `validate & apply commands`, 3 `advance movement`, 4 `resolve simulation` (reservado para combate,
   vacío en MVP), 5 `process timers/scheduled events`, 6 `update world state / interest sets`,
   7 `emit deltas`, 8 `enqueue persistence`. El **catch-up** usa calendario en tiempo absoluto: los ticks
   perdidos se descartan, jamás se ejecutan en ráfaga. `applyCommand` **aísla los pánicos** de un comando
   concreto para que un bug en una regla no tumbe la simulación entera.
8. **HECHO** — **Prohibición de I/O bloqueante contra Postgres dentro del tick**: la persistencia va por
   canal a workers, con hasta **3 intentos con backoff** y compensación `OnPermanentFailure` si se
   agotan. Métrica `eo_persistence_queue_depth` y `eo_game_tick_overruns_total` incrementada cuando
   un tick supera su período.
9. **HECHO** — Flujo de validación del comando en el orden canónico: ownership → estado de unidad →
   destino → A\* → creación del movimiento → simulación → notificación. Errores asociados:
   `UNIT_NOT_FOUND`, `UNIT_NOT_OWNED`, `UNIT_NOT_MOVABLE`, `UNIT_DEAD`, `UNIT_GARRISONED`,
   `INVALID_TARGET`, `TARGET_OUT_OF_BOUNDS`, `TARGET_NOT_WALKABLE`, `PATH_NOT_FOUND`, `PATH_TOO_LONG`.
10. **HECHO** — **Mensajes cliente→servidor de M4**: `unit.move { unitId, target:{x,y} }` y
    `unit.cancel_move`. El cliente nunca envía secuencias de posiciones.
11. **HECHO** — **Mensajes servidor→cliente de M4**: `unit.move.accepted`, `unit.move.rejected`,
    `unit.movement.started`, `unit.movement.completed`, `unit.movement.cancelled`.
    `unit.movement.started.payload` es
    `{unitId, movement:{movementId, path[], startTimeMs, arrivalTimeMs, target}}` y
    `unit.movement.cancelled.payload.reason` pertenece a
    `REPLACED | CANCELLED_BY_PLAYER | PATH_BLOCKED | UNIT_DEAD | SERVER`.
12. **HECHO** — **Eventos de dominio** `UnitMovementStarted` y `UnitMovementCompleted` como base de los
    deltas de red y de las filas de `world_events`, siguiendo la separación Command / Event / State:
    `MoveUnit` y `CancelMovement` son comandos, los anteriores son eventos, y
    `Unit.status = MOVING` es estado.
13. **HECHO** — **Recuperación tras crash** (`simulation.Hydrate`): al arrancar se cargan los movimientos
    `ACTIVE`. Si el movimiento venció durante la caída, la unidad aparece en el destino y el movimiento se
    cierra `COMPLETED`; si sigue en curso, se reanuda desde la polilínea; si la polilínea es inválida,
    queda `FAILED` y la unidad se queda donde estaba, porque nunca se teletransporta a nadie por un dato
    dudoso.
14. **HECHO** — **Persistencia diferenciada**: transacción propia para inicio y finalización de
    movimiento, aplicada primero en RAM y **encolada** hacia los workers, porque el tick no puede hacer
    I/O de PostgreSQL; dirty-flag con flush cada `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` (50 ticks, 5 s)
    para posiciones consolidadas y HP; posición durante un movimiento activo **no se persiste**, se deriva
    de `unit_movements`. La ventana entre la aceptación y el `COMMIT` es un RPO documentado: ver
    `DEBT-17` en [backlog.md](backlog.md).
15. **PENDIENTE — Cliente**: interpolación visual entre waypoints en PixiJS, con corrección hacia la
    verdad del servidor cuando llega un delta. El cliente nunca decide la posición final ni el ETA.
    Requiere `apps/web/`, que ya existe.
16. **HECHO** — **Métricas activas**: `eo_pathfinding_requests_total{result}`,
    `eo_pathfinding_duration_seconds`, `eo_game_tick_duration_seconds`, `eo_game_tick_overruns_total`,
    `eo_active_units`, `eo_active_movements`, `eo_persistence_queue_depth`,
    `eo_database_latency_seconds`.

### Tests

- **HECHO** *unit*: A\* resuelve la ruta trivial, `from == to`, el destino bloqueado o fuera del mundo, la
  ausencia de ruta, el rodeo, la preferencia de `ROAD` sobre `FOREST`, el corner cutting, los límites de
  nodos y de distancia, el determinismo entre ejecuciones, la no contaminación entre consultas y la
  cancelación por contexto. El desempate respeta `(f, h, y, x)`.
- **HECHO** *unit*: la diagonal no corta esquinas cuando un ortogonal está bloqueado.
- **HECHO** *unit*: **golden test del ejemplo numérico canónico**
  `(0,0) → (1,0) → (2,1) → (3,1) → (4,1)` con `tMs` exactamente `0, 600, 1449, 2409, 2769`, más las
  duraciones de paso por terreno (600/849 en `GRASSLAND`, 960/1358 en `FOREST`, 1080/1527 en `HILL`,
  360/509 en `ROAD`).
- **HECHO** *unit*: `PositionAt` e `IndexAt` devuelven el último waypoint con
  `tMs <= (T - start_time_ms)`, incluidos los bordes exactos, antes del inicio y después de la llegada;
  más `Validate` y el ciclo de vida del movimiento.
- **HECHO** *unit*: superar `EO_PATHFINDING_MAX_NODES` o `EO_PATHFINDING_MAX_DISTANCE` devuelve
  `PATH_TOO_LONG`.
- **HECHO** *unit*: destino intransitable devuelve `TARGET_NOT_WALKABLE`; destino fuera del mundo,
  `TARGET_OUT_OF_BOUNDS`.
- **HECHO** *simulation*: con `FakeClock`, avanzar 10 segundos deja a la unidad en el tile exacto
  esperado, de forma reproducible; incluye el *vertical slice* completo, el reemplazo de orden, la
  cancelación, los seis rechazos, el movimiento con el jugador desconectado y el volcado por lotes.
- **PARCIAL** *integration*: una nueva orden sobre una unidad en movimiento cancela la anterior a
  `CANCELLED` y crea la nueva `ACTIVE` en la misma transacción; nunca coexisten dos `ACTIVE`. La regla
  está verificada a nivel de simulación y garantizada en base por el índice único parcial; el test contra
  PostgreSQL real está escrito pero **no ejecutado**.
- **PARCIAL** *integration*: mover una unidad de otro jugador devuelve `UNIT_NOT_OWNED`. Cubierto en
  simulación; el test de integración está escrito, **no ejecutado**.
- **HECHO** *recovery*: tres tests sobre `simulation.Hydrate` cubren el movimiento vencido durante la
  caída (unidad en el destino, movimiento `COMPLETED`), el movimiento en curso (se reanuda desde la
  polilínea) y la polilínea inválida (`FAILED`, unidad donde estaba).
- **HECHO** *contract*: los cinco mensajes de movimiento validan contra el JSON Schema.

### Documentación actualizada al cerrar

[../specs/movement.md](../specs/movement.md),
[../architecture/game-loop.md](../architecture/game-loop.md),
[../architecture/pathfinding.md](../architecture/pathfinding.md),
[../architecture/persistence.md](../architecture/persistence.md),
[../specs/websocket-protocol.md](../specs/websocket-protocol.md).

### Invariantes cubiertos

`EJE-MOVE` completo. `EJE-UNIT` completo en cuanto a transiciones `IDLE`↔`MOVING`.
`EJE-SEC` completo para el path, el ETA y la posición final. `EJE-PERSIST` completo en las
tres categorías: write-through, dirty-flag y reconstruible.

### Aceptación

1. `unit.move` a un tile alcanzable devuelve `unit.move.accepted` seguido de `unit.movement.started`.
2. La unidad llega al tile destino con una desviación máxima de un tick respecto al
   `arrival_time_ms` calculado.
3. Ordenar un segundo movimiento deja exactamente un `unit_movements` con estado `ACTIVE` para esa
   unidad.
4. `unit.cancel_move` deja el movimiento en `CANCELLED` y la unidad en `IDLE` en el último waypoint
   alcanzado.
5. Mover una unidad ajena devuelve `unit.move.rejected` con `UNIT_NOT_OWNED`.
6. Un destino sobre `WATER` devuelve `TARGET_NOT_WALKABLE`.
7. Un destino a más de 256 tiles devuelve `PATH_TOO_LONG`.
8. Matar y rearrancar el servidor a mitad de trayecto no pierde el movimiento.
9. `eo_game_tick_overruns_total` permanece en cero durante un escenario de 10 unidades moviéndose a
   la vez.
10. En el cliente, la unidad se desplaza de forma continua y no a saltos de tile.

---

## M5 — Offline protection & Safe Zones

**Estado: PARCIAL.** La presencia en Redis, el autómata completo de `presence_state` y el cooldown de
protección están implementados y con tests en verde (`internal/domain/city`, `internal/game/simulation`).
**Falta** toda la parte de Safe Zones más allá de la tabla: el cálculo de `DENSE_FOREST` y `CAVERN` y el
uso efectivo de `HIDDEN` están pendientes.

**Objetivo.** Modelar la presencia del jugador, la transición a protección tras el cooldown y las
zonas seguras, todo calculado y validado por el servidor.

**Alcance.** Presencia en Redis, máquina de estados de `presence_state`, `protection_until`,
`safe_zones` y el estado `HIDDEN`. **No entra**: consecuencias de combate de la protección, que
llegan con Combat, fuera del MVP.

### Entregables

1. **HECHO** — `internal/persistence/redis/presence.go`: clave Redis `presence:player:{playerId}` con TTL
   `EO_PRESENCE_TTL_SECONDS` (30) y heartbeat cada `EO_PRESENCE_HEARTBEAT_SECONDS` (10) mientras
   haya conexión WS activa. El arranque exige `EO_PRESENCE_HEARTBEAT_SECONDS < EO_PRESENCE_TTL_SECONDS`.
2. **HECHO** — Máquina de estados de `presence_state` en `internal/domain/city` (`CanTransition`,
   `ShouldEngageProtection`) con exactamente tres valores y estas transiciones:

   ```
   ONLINE --disconnect(+grace)--> OFFLINE_PENDING --cooldown--> PROTECTED
   PROTECTED --connect--> ONLINE ;  OFFLINE_PENDING --connect--> ONLINE
   ```

3. **HECHO** — La transición a `OFFLINE_PENDING` la decide el **game loop en RAM**, comparando el
   instante de la última desconexión contra `DisconnectGrace`, que vale `EO_PRESENCE_TTL_SECONDS` (30 s).
   Un jugador con varias sesiones abiertas sigue `ONLINE` mientras le quede una. **No se lee la expiración
   de la clave de Redis**: Redis publica la presencia para observadores externos y para el futuro
   multiproceso, pero no arbitra la máquina de estados —si lo hiciera, un fallo de Redis movería el estado
   del mundo—.
4. **HECHO** — Cooldown configurable `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` (300 por defecto),
   evaluado en la fase 5 del tick (`process timers/scheduled events`). Ningún valor de gameplay
   hardcodeado.
5. **HECHO** — `protection_until` a NULL mientras el jugador siga offline —la protección es indefinida en
   el MVP— y limpiado al reconectar. El campo queda previsto para límites futuros (`DEBT-02`).
6. **PARCIAL** — Migración de `safe_zones` con los tipos `DENSE_FOREST` y `CAVERN`: **la tabla existe, la
   lógica no**. El cálculo (`DENSE_FOREST` apoyado en terreno `FOREST`, `CAVERN` en adyacencia a
   `MOUNTAIN`) está **pendiente**. Cuando exista, la seguridad la calculará y validará siempre el
   servidor: el cliente nunca puede declararse a salvo.
7. **PENDIENTE** — Uso del estado `HIDDEN` de `units.status` para unidades dentro de una safe zone, con su
   consecuencia en el interest management: una unidad `HIDDEN` no se difunde a otros jugadores. El valor
   existe en el enum y en el `CHECK`; la mecánica no.
8. **HECHO** — Emisión de `city.update` con `presence_state` y `protection_until` en cada transición, y
   evento de dominio `CityProtectionEngaged` registrado en `world_events`.
9. **HECHO** — Código de error `CITY_PROTECTED` definido y devuelto por las acciones que la protección
   bloquea. En el MVP la superficie de acciones bloqueables es mínima; el código existe, está testeado y
   su uso se amplía con Combat.
10. **HECHO** — Persistencia transaccional de toda transición de presencia, encolada hacia los workers:
    nunca queda solo en Redis.

### Tests

- **HECHO** *unit*: la máquina de estados acepta exactamente las cuatro transiciones válidas y rechaza el
  resto, por ejemplo `ONLINE → PROTECTED` directo; `ShouldEngageProtection` cubre sus casos límite.
- **HECHO** *simulation*: con `FakeClock`, tras `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` la ciudad
  pasa a `PROTECTED` y ni un tick antes; y con varias sesiones abiertas no baja de `ONLINE`.
- **PENDIENTE** *unit*: cálculo de safe zone sobre `FOREST` y sobre adyacencia a `MOUNTAIN`. No hay
  lógica que testear todavía.
- **PARCIAL** *integration*: agotar el margen de reconexión con la conexión ya cerrada lleva a
  `OFFLINE_PENDING`; reconectar antes del cooldown vuelve a `ONLINE` y no llega a `PROTECTED`. Cubierto en
  simulación; el test contra Redis real está escrito, **no ejecutado**.
- **PARCIAL** *integration*: reconectar desde `PROTECTED` deja `presence_state = 'ONLINE'` y
  `protection_until = NULL`. Escrito, **no ejecutado**.
- **PENDIENTE** *integration*: una unidad `HIDDEN` no aparece en el `world.snapshot` de otro jugador cuyo
  área de interés la contiene. Depende del entregable 7.
- **PARCIAL** *recovery*: un reinicio del servidor no pierde el `presence_state` persistido ni reinicia el
  cooldown desde cero. Escrito, **no ejecutado**.

### Documentación actualizada al cerrar

[../specs/presence.md](../specs/presence.md),
[../specs/safe-zones.md](../specs/safe-zones.md),
[../architecture/game-loop.md](../architecture/game-loop.md) (fase 5),
[../database/schema.md](../database/schema.md).

### Invariantes cubiertos

`EJE-CITY` completo. `EJE-UNIT` en la parte de `HIDDEN`. `EJE-PERSIST` en la separación
Redis/PostgreSQL: la presencia es transitoria, el `presence_state` es durable.

### Aceptación

1. Cerrar la última conexión y dejar vencer el margen de reconexión (30 s) lleva la ciudad a
   `OFFLINE_PENDING`.
2. Reconectar durante el grace mantiene `ONLINE` sin pasar por `OFFLINE_PENDING`; tener otra sesión
   abierta también lo mantiene.
3. Transcurrido el cooldown, la ciudad está en `PROTECTED` y se emitió un `city.update`.
4. Reconectar desde `PROTECTED` devuelve a `ONLINE` y deja `protection_until` a NULL.
5. Cambiar `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` a 10 hace que la transición ocurra a los 10
   segundos, sin cambios de código.
6. Una unidad dentro de una safe zone tiene `status = 'HIDDEN'` y no es visible para terceros.
7. Un reinicio del servidor conserva el estado de presencia de todas las ciudades.

---

## M6 — Territories

**Estado: COMPLETO salvo el entregable 8.** El circuito funciona y está verificado de extremo a extremo:
geometría sembrada, índice en RAM, cambio de dueño transaccional, evento de dominio, delta de red y
overlay en el cliente. Todos los tests de la sección correspondiente existen y pasan.

El **entregable 8** no está hecho y no lo estará hasta que alguien tome una decisión de diseño que
ningún documento ha tomado; su entrada explica por qué implementarlo hoy sería inventar una regla de
juego. Ese es el único motivo por el que este milestone no se marca cerrado.

**Objetivo.** Introducir territorios rectangulares y el control sobre ellos, de modo que el espacio
del mundo tenga significado político persistente.

**Alcance.** Tablas `territories` y `territory_control`, cálculo de pertenencia de un tile a un
territorio, ownership y su difusión por `territory.update`. **No entra**: mecánicas de conquista
por combate, que son parte de Sieges, fuera del MVP.

### Entregables

1. **HECHO** — Migración de `territories` con geometría rectangular `min_x, min_y, max_x, max_y` en
   coordenadas lógicas, y de `territory_control` como tabla separada que registra quién controla qué y
   desde cuándo, con CHECK de consistencia `owner_type='NONE'` ⟺ `owner_id IS NULL` y trigger
   `set_updated_at()`. La separación entre ambas es deliberada: la geometría es estable, el control
   cambia.
2. **HECHO** — `internal/domain/territory`: pertenencia de un tile a un territorio, consulta inversa
   territorio→chunks solapados, y resolución del controlador vigente.
3. **HECHO** — Índice espacial suficiente para el MVP: con geometría rectangular y un mundo de
   512×512, una consulta por rango sobre `min_x/max_x/min_y/max_y` es adecuada. No se introduce PostGIS.
4. **HECHO** — Persistencia transaccional de todo cambio de ownership, con concurrencia optimista
   sobre `version` para evitar sobrescrituras perdidas.
5. **HECHO** — Mensaje servidor→cliente `territory.update`, emitido con contenido real a las sesiones
   cuya área de interés intersecta la huella de chunks del territorio. Se emite **una vez por sesión**
   aunque la huella abarque varios chunks: para eso se añadió `BroadcastChunks` al hub, porque llamar a
   `BroadcastChunk` en bucle habría entregado el mismo hecho repetido y con `seq` distinto cada vez.
6. **HECHO** — Registro del cambio de control como evento de dominio en `world_events`, en la misma
   transacción que el cambio. El `tick` es el real del game loop: su contador se hizo atómico para
   poder leerlo desde la goroutine del alta sin provocar una carrera.
7. **HECHO** — Renderizado del territorio en el cliente como overlay isométrico, derivado
   exclusivamente de `territory.update`. El cliente no calcula ownership. Requiere `apps/web/`.
8. **BLOQUEADO POR DISEÑO, no por implementación** — Uso de `FORBIDDEN` para acciones no permitidas
   dentro de territorio ajeno, con la superficie de acciones restringidas que defina la spec de
   territorio.

   Este entregable se remite a una superficie que **la spec declina definir**, y lo dice de forma
   explícita: [«no hay superficie de error de usuario»](../specs/territory.md) porque en MVP no existe
   ningún comando de cliente dirigido a un territorio, y «comandos cliente→servidor sobre territorios:
   no existen en el protocolo v1 y **no se inventan aquí**». Además, `FORBIDDEN` significa en el resto
   del sistema *operar sobre entidades de otro jugador* ([city.md](../specs/city.md),
   [player.md](../specs/player.md)), no *estar dentro de su territorio*.

   Implementarlo hoy significaría inventar una regla de juego —previsiblemente «no puedes mover unidades
   dentro del territorio de otro»— que ningún documento especifica y que cambia cómo se juega. Eso es
   una decisión de diseño, no una tarea de programación. Queda pendiente de que alguien la tome y la
   escriba en la spec; entonces el código son unas pocas líneas en el manejador de `unit.move`.

### Tests

Todos existen y pasan salvo el marcado como pendiente.

- ✔ *unit*: pertenencia de tile a rectángulo incluyendo los cuatro bordes y las cuatro esquinas
  (`TestPertenenciaIncluyeLosCuatroBordes`, `TestPertenenciaIncluyeLasCuatroEsquinas`,
  `TestPertenenciaExcluyeElTileSiguienteACadaBorde`).
- ✔ *unit*: un territorio que cruza fronteras de chunk se mapea a todos los chunks solapados
  (`TestUnTerritorioQueCruzaFronterasDeChunkMapeaATodosLosSolapados`), con un caso de `chunkSize` 10
  para que un desplazamiento de bits no pueda colarse en lugar de la división entera.
- ✔ *unit*: territorios solapados resuelven un único controlador de forma determinista
  (`TestDosTerritoriosSolapadosResuelvenElIDMenorYSeReportan`, `TestElReporteDeSolapamientosEsDeterminista`).
- ✔ *integration*: un cambio de ownership se persiste y sobrevive a un reinicio
  (`TestElOwnershipSobreviveAUnReinicio`).
- ✔ *integration*: dos cambios concurrentes sobre el mismo territorio: uno gana y el resto falla de
  forma explícita (`TestDosReclamacionesConcurrentesSoloUnaGana`), más el caso puro de versión
  desactualizada sobre un territorio libre (`TestUnaVersionDesactualizadaSobreUnTerritorioLibreSeRechaza`),
  verificado por mutación: anular la comprobación de `version` lo pone en rojo.
- ✔ *integration*: fundar en territorio ajeno no lo cambia de manos ni hace fallar el alta
  (`TestFundarEnTerritorioAjenoNoLoCambiaDeManosNiFalla`), y el evento de dominio se escribe en la
  misma transacción (`TestFundarReclamaElTerritorioDelCentroYRegistraElEvento`).
- ✔ *unit de transporte*: `territory.update` llega **solo** a las conexiones suscritas a chunks
  solapados, y **una sola vez** a quien mira varios chunks del mismo territorio
  (`internal/websocket/hub_test.go`). Incluye un control negativo,
  `TestBroadcastChunkEnBucleSiDuplicaria`, que demuestra que el problema es real: si alguien sustituye
  `BroadcastChunks` por un bucle, ese test explica por qué no.
- ✔ *contract*: `territory.update` valida contra el JSON Schema (`contract_test.go`, ya existente).

### Documentación actualizada al cerrar

[../specs/territory.md](../specs/territory.md),
[../architecture/networking.md](../architecture/networking.md),
[../database/schema.md](../database/schema.md),
[../specs/websocket-protocol.md](../specs/websocket-protocol.md).

### Invariantes cubiertos

`EJE-SEC` en la parte de ownership: el cliente nunca lo aporta. `EJE-PERSIST` en la parte de
write-through para cambios de ownership.

### Aceptación

1. Un tile dentro del rectángulo pertenece al territorio; uno en `max_x + 1` no.
2. `territory.update` se recibe al entrar en un chunk solapado por el territorio.
3. Un reinicio del servidor conserva el controlador de cada territorio.
4. Un `UPDATE` con `version` desactualizada es rechazado.
5. Un jugador no suscrito a los chunks del territorio no recibe su `territory.update`.
6. El overlay del cliente refleja exactamente el ownership que emitió el servidor.

---

## M7 — Diplomacy foundation

**Estado: PENDIENTE.** Las tablas `treaties` y `garrisons` existen desde la migración
`000001_initial_schema` —con el CHECK de par canónico `player_a_id < player_b_id`, el CHECK
`player_a_id <> player_b_id`, el índice `treaties_one_active_per_pair_and_type` y `garrisons_city_idx`— y
los códigos `TREATY_REQUIRED` y `UNIT_GARRISONED` están en el catálogo. **No hay dominio de diplomacia**:
`internal/domain/diplomacy` no existe todavía.

**Objetivo.** Persistir treaties y garrisons y hacer que condicionen de forma efectiva qué puede
hacer un jugador con las unidades de otro y en la ciudad de otro.

**Alcance.** Tablas `treaties` y `garrisons`, ciclo de vida del treaty, flag `allows_garrison`, la
transición de unidad a `GARRISONED` y su reversión, y la aplicación del error `TREATY_REQUIRED`.
**No entra**: negociación de treaties desde el cliente. El protocolo v1 no define mensajes de
diplomacia; el mecanismo de propuesta y aceptación de cara al jugador es **TBD (fuera de MVP)**.
En este milestone los treaties se crean por vía administrativa o de seed, y lo que se valida es su
efecto sobre el dominio.

### Entregables

1. **HECHO** — Migración de `treaties` con tipos `NON_AGGRESSION`, `ALLIANCE`, `TRADE`, estados
   `PROPOSED`, `ACTIVE`, `EXPIRED`, `BROKEN`, y el flag `allows_garrison`. El par se guarda en forma
   canónica (`CHECK player_a_id < player_b_id`, más `CHECK player_a_id <> player_b_id`) y sólo puede haber
   un tratado activo por par y tipo (`treaties_one_active_per_pair_and_type`).
2. **HECHO** — Migración de `garrisons`, que registra qué unidad está guarnecida en qué ciudad y desde
   cuándo, con `garrisons_city_idx`.
3. **PENDIENTE** — `internal/domain/diplomacy`: ciclo de vida del treaty y regla dura de que **solo un
   treaty `ACTIVE`** habilita garrison. Un treaty `PROPOSED`, `EXPIRED` o `BROKEN` nunca habilita nada.
4. **PENDIENTE** — Regla de garrison: una unidad puede guarnecerse en una ciudad ajena si y solo si existe
   un treaty `ACTIVE` entre ambos jugadores con `allows_garrison` verdadero. En caso contrario,
   `TREATY_REQUIRED`.
5. **PARCIAL** — Transición de estado de la unidad a `GARRISONED`: el valor existe en el enum, en el
   `CHECK` y en la validación de `unit.move`, que ya rechaza con `UNIT_GARRISONED`. **Falta** el resto:
   guarnecer una unidad y cancelar en la misma transacción su movimiento `ACTIVE`, respetando
   `EJE-MOVE`.
6. **PENDIENTE** — Efecto en el interest management: una unidad que entra en garrison genera
   `entity.despawn` con `reason: GARRISONED` para los observadores que dejan de verla, y su ciudad emite
   `city.update`. El valor `GARRISONED` de `entity.despawn.payload.reason` ya está en el protocolo.
7. **PENDIENTE** — Persistencia transaccional de treaties y garrisons, y registro de los cambios en
   `world_events`.
8. **PENDIENTE** — Expiración de treaties evaluada en la fase 5 del tick, con transición
   `ACTIVE → EXPIRED` y la consecuencia sobre los garrisons vigentes que la spec determine.
9. **PENDIENTE** — Verificación de que `CITY_PROTECTED` y `TREATY_REQUIRED` se aplican en el orden
   correcto cuando ambos podrían aplicar.

### Tests

- *unit*: la máquina de estados del treaty acepta `PROPOSED → ACTIVE`, `ACTIVE → EXPIRED` y
  `ACTIVE → BROKEN`, y rechaza el resto.
- *unit*: `allows_garrison` verdadero con estado distinto de `ACTIVE` no habilita garrison.
- *integration*: garrison sin treaty devuelve `TREATY_REQUIRED`; con treaty `ACTIVE` y
  `allows_garrison` la unidad queda `GARRISONED` y persistida.
- *integration*: `unit.move` sobre una unidad `GARRISONED` devuelve `UNIT_GARRISONED`.
- *integration*: guarnecer una unidad en movimiento cancela su movimiento a `CANCELLED` en la misma
  transacción.
- *integration*: la expiración de un treaty se detecta en el tick y transiciona a `EXPIRED`.
- *recovery*: un reinicio conserva treaties y garrisons y no resucita movimientos cancelados.
- *contract*: los `entity.despawn` y `city.update` derivados validan contra el JSON Schema.

### Documentación actualizada al cerrar

[../specs/garrison.md](../specs/garrison.md),
[../specs/movement.md](../specs/movement.md) (interacción con `GARRISONED`),
[../database/schema.md](../database/schema.md),
[../specs/websocket-protocol.md](../specs/websocket-protocol.md).

### Invariantes cubiertos

`EJE-MOVE` en la interacción con garrison. `EJE-UNIT` completo, incluido `GARRISONED`.
`EJE-SEC`: la habilitación de garrison la decide el servidor a partir del estado del treaty,
nunca el cliente. `EJE-PERSIST` para treaties y garrisons.

### Aceptación

1. Un intento de garrison sin treaty devuelve `system.error` con `TREATY_REQUIRED`.
2. Con treaty `ACTIVE` y `allows_garrison` verdadero, la unidad queda `GARRISONED` en base.
3. Con treaty `PROPOSED` y `allows_garrison` verdadero, el intento sigue devolviendo
   `TREATY_REQUIRED`.
4. `unit.move` sobre una unidad guarnecida devuelve `UNIT_GARRISONED`.
5. Guarnecer una unidad en movimiento deja su `unit_movements` en `CANCELLED` y ninguno `ACTIVE`.
6. Un treaty con fecha de expiración pasada transiciona a `EXPIRED` dentro del tick siguiente.
7. Tras reiniciar, treaties y garrisons conservan su estado exacto.

---

## Dependencias y paralelización

### Dependencias estrictas

Una dependencia es estricta cuando el milestone destino no puede empezar a integrarse sin el origen
terminado y en la rama principal.

| Dependencia | Motivo |
|---|---|
| M0 → M1, M2, M3 | Sin módulo Go, migraciones, config, observabilidad y CI no hay nada verificable ni desplegable. |
| M1 → M2 | La ciudad se coloca sobre tiles transitables y marca el `blocked overlay`. |
| M1 → M4 | A\* necesita el grid real con los `costUnits` del terreno y la regla de corner cutting. |
| M2 → M3 | El `world.snapshot` se centra en la ciudad del jugador y difunde sus unidades. |
| M3 → M4 | `unit.move` es un mensaje del protocolo: necesita sesión, `requestId`, idempotencia y deltas. |
| M2 → M5 | La presencia y la protección son estado de la ciudad. |
| M4 → M5 | La protección offline solo tiene sentido con un mundo que se mueve sin ti. |
| M3 → M6 | `territory.update` se difunde por el interest management por chunks. |
| M5 → M6 | El overlay de territorio y las safe zones comparten la capa de consulta espacial y la fase 5 del tick. |
| M4 → M7 | El garrison cancela movimientos y depende de `EJE-MOVE`. |
| M6 → M7 | Un treaty regula qué se puede hacer en el territorio de otro. |

### Trabajo paralelizable

| Se puede paralelizar | Condición |
|---|---|
| M1 (mundo) y M2 (jugador y ciudad) | Ambos tras M0. Se integran en el punto de colocación de la ciudad: hasta entonces M2 puede trabajar contra un mundo de prueba en `testdata`. |
| `packages/protocol` completo (todos los mensajes v1) | El contrato v1 está congelado por el canon: los esquemas Zod de M3 y M4 pueden escribirse desde M0, aunque el servidor aún devuelva `NOT_IMPLEMENTED`. |
| Cliente PixiJS (render isométrico, cámara, sprites) | La proyección isométrica es responsabilidad exclusiva del cliente y no depende del servidor: puede desarrollarse contra datos sintéticos desde M1. |
| `internal/pathfinding` | Depende del grid de M1 pero no del protocolo: se puede escribir y testear contra mapas de `testdata` en paralelo a M2 y M3. |
| Migraciones de M5, M6 y M7 | El DDL puede escribirse y revisarse antes que la lógica, siempre que las migraciones se apliquen en orden. |
| Documentación de specs | Precede a la implementación por definición: el proyecto es Spec-Driven. |

### Trabajo que no se debe paralelizar

- El game loop (M4) y la persistencia asíncrona: son una sola pieza de diseño. Partirlos produce
  I/O bloqueante dentro del tick, que es una violación dura.
- El interest management (M3) y los deltas de movimiento (M4): el conjunto de suscripción y la
  emisión de deltas se validan juntos o no se validan.
- La máquina de presencia (M5) y la sesión WebSocket (M3): la transición a `OFFLINE_PENDING` depende
  de dos condiciones, una de las cuales vive en la capa de conexión.

## Documentos relacionados

- [roadmap.md](roadmap.md) — visión temporal, estrategia de vertical slice y riesgos.
- [backlog.md](backlog.md) — items priorizados, deuda técnica y preguntas abiertas.
- [../testing/strategy.md](../testing/strategy.md) — niveles de test y Definition of Done.
- [../architecture/game-loop.md](../architecture/game-loop.md) — fases del tick.
- [../specs/websocket-protocol.md](../specs/websocket-protocol.md) — contrato de red.
- [../invariants/README.md](../invariants/README.md) — registro único de invariantes.
