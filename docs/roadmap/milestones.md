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
| **HECHO** | Implementado en el repositorio y cubierto por tests que se ejecutan en la CI y están en verde. |
| **PARCIAL** | Implementado sólo en parte, o implementado sin un test que lo cubra entero. |
| **PENDIENTE** | No existe código todavía. |

Tres hechos condicionan las marcas y conviene tenerlos presentes al leer:

1. **La CI se ejecuta y está en verde** sobre `main`
   ([../operations/local-development.md](../operations/local-development.md) §1): documentación y lint de
   determinismo, TypeScript, Game Server con `-race`, integración contra PostgreSQL y Redis reales, e
   imagen del contenedor. Un milestone puede cerrarse; que esté cerrado depende de sus criterios.
2. **Los tests de integración se ejecutan**, en la CI y en local. En la máquina de desarrollo Docker está
   descartado —provoca pantallazos azules por consumo de RAM—, y el camino real es un cluster propio de
   PostgreSQL en el puerto 5433 y Redis en WSL (§3-bis de la misma guía). Los criterios escritos en
   términos de `docker compose` se verifican por ese camino, y así se dice en cada uno.
3. **Algunas marcas de esta página las fijó la reconciliación de `EO-118`**, ficha por ficha contra la
   suite y, donde un criterio pedía un comportamiento observable, arrancando el servidor real. Lo que se
   verificó así lo dice expresamente.

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

**Estado: CERRADO.** Monorepo, módulo Go, configuración, observabilidad, migraciones, protocolo
compartido, esquemas embebidos y `apps/web/`, todo en verde. Los dos bloqueantes que quedaban han
caído: `apps/web/` existe (entregable 1) y **la CI se ejecuta y pasa entera** (entregable 13) desde
que el repositorio es público, con los cinco jobs verdes — documentación, TypeScript, Game Server con
`-race`, integración contra PostgreSQL y Redis reales, y la construcción de la imagen del contenedor.

Sus dos primeras ejecuciones reales encontraron dos fallos que ninguna verificación local podía ver, y
ese es exactamente el motivo por el que M0 no se daba por cerrado antes: la versión de pnpm declarada
en dos sitios, y un typecheck que en un checkout limpio no encuentra los tipos de
`@empires-online/protocol` porque `dist/` no existe todavía. Ambos arreglados.

**Objetivo.** Dejar el repositorio en un estado en el que cualquier ingeniero pueda clonar, arrancar
la infraestructura local, ejecutar el servidor, correr los tests y recibir verificación automática
en cada pull request.

**Alcance.** Estructura del monorepo, workspace de pnpm, infraestructura Docker, esqueleto del
módulo Go con configuración, logging, métricas y endpoints de salud, mecanismo de migraciones con
las migraciones iniciales, paquete de protocolo con Zod y exportación de JSON Schema, y CI en GitHub
Actions. **No entra**: lógica de dominio, mundo, jugadores, WebSocket ni mensajes de juego.

### Entregables

1. **HECHO — Estructura del monorepo**: existen `services/game-server/` (con `cmd/server/`, `cmd/migrate/`,
   `cmd/devseed/`, `internal/`, `migrations/`), `packages/protocol/`, `apps/web/`, `docs/`,
   `docker-compose.yml`, `pnpm-workspace.yaml` y `.github/workflows/ci.yml`.
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
   Sirve a otros entornos. En la máquina de desarrollo **Docker está descartado** —provoca pantallazos
   azules por consumo de RAM— y el camino real es un cluster propio de PostgreSQL en el puerto 5433 y
   Redis en WSL ([../operations/local-development.md](../operations/local-development.md) §3-bis). La CI
   no usa este fichero: declara PostgreSQL y Redis como *services* del job.
4. **HECHO — Instalación de Go**: `winget install --id GoLang.Go`, verificado con `go version`.
   Instalado **Go 1.27.0**; el `go.mod` declara `go 1.25.11` como versión **mínima**. ([ADR-001](../decisions/ADR-001-game-server-language.md) eligió Go 1.23; las dependencias subieron el mínimo después.)
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
    **golang-migrate** embebido reescribiendo la URL al esquema `pgx5`. Fuera de producción las aplica **el
    arranque del servidor** (`EO_MIGRATE_ON_START`, verdadero salvo con `EO_ENV=production`); en producción,
    el binario `cmd/migrate`, antes de desplegar. En los dos casos de forma idempotente, y abortando ante un
    esquema `dirty`. Las migraciones existentes son `000001_initial_schema` (esquema completo, incluido el
    trigger `set_updated_at()` y `world_state` con `epoch_ms`), `000002_seed_catalogs`,
    `000003_territory_control_version` y `000004_drop_idempotency_keys`.
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
13. **HECHO — CI en `.github/workflows/ci.yml`**, con cinco jobs verdes: integridad documental y tests
    de los scripts, TypeScript (typecheck, tests del protocolo, deriva del JSON Schema y `next build`),
    Game Server (`gofmt`, `go mod tidy` sin cambios, `go vet` con y sin la etiqueta `integration`,
    build, tests con `-race` y cobertura), integración con Postgres y Redis como *services* y
    `EO_INTEGRATION=1`, y la construcción de la imagen del contenedor. Cualquier check obligatorio en
    rojo invalida el PR.

    El **lint de determinismo** (`EO-017`) corre en el job `docs`: `node scripts/lint-determinism.mjs` falla si el núcleo
    determinista lee el reloj real o el azar global. **ESLint sigue sin existir**: `pnpm run lint`
    ejecuta además el script `lint` de cada paquete que lo declare, y ninguno lo declara todavía.
14. **HECHO — `docs/`** inicializado con la estructura de grupos y la guía de arranque que describe el
    entorno real: Windows 10, Node v22.17.1, pnpm 10.25.0, git 2.38.1, Docker CLI 20.10.22 con
    Compose v2.15.1, Go 1.27.0 instalado, y ausencia de `psql`, `redis-cli`, `make` y `gh`.

### Tests

- **HECHO** *unit* (Go): carga de configuración con valores por defecto, con obligatorias ausentes y con
  todas las validaciones cruzadas; formato del logger; registro de métricas sin duplicados; `FakeClock`
  avanza determinísticamente.
- **HECHO** *unit* (Vitest): 18 tests. Los esquemas Zod aceptan envelopes válidos y rechazan `v` distinto
  de 1, `type` desconocido, coordenadas no enteras y **campos extra** en mensajes cliente→servidor.
- **PARCIAL** *integration*: las migraciones embebidas se aplican sobre una base limpia, el migrador es
  idempotente al repetirse y un esquema `dirty` lo aborta (`TestElMigradorEsIdempotente`,
  `TestUnEsquemaDirtyAbortaElMigrador`); los nombres son correlativos y cada `.up.sql` tiene su `.down.sql`
  (`TestLasMigracionesSonCorrelativasYCompletas`). **No hay test** de que cada `.down.sql` revierta su
  `.up.sql`.
- **PARCIAL** `GET /ready`: devuelve 200 con PostgreSQL y Redis arriba, 503 al parar Redis y 200 al
  volver. **Verificado a mano** contra el servidor real el 2026-10-20; no hay test automático.
- **HECHO** *contract*: el JSON Schema exportado por el build coincide con el commiteado —la CI lo
  comprueba en el job de TypeScript y falla ante la deriva— y el catálogo de códigos de error de Go
  coincide con el de TypeScript (`TestCatalogoDeErroresCoincideConElExportado`).

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
2. **Cumplido.** `go version` reporta una versión igual o superior a la que exige `go.mod` (1.25.11): hay
   1.27.0 instalado.
3. **Cumplido por otro camino.** El criterio original —`pnpm run db:up` deja `postgres` y `redis` en
   estado *healthy*— presupone Docker, que en esta máquina está descartado. La infraestructura local es
   el cluster propio de PostgreSQL (`node scripts/pg-local.mjs start`, puerto 5433) y Redis en WSL; la CI
   levanta los dos como *services* y ejecuta contra ellos la suite de integración entera.
4. **Cumplido.** El arranque del servidor aplica las migraciones embebidas y un segundo arranque no produce
   cambios (`TestElMigradorEsIdempotente`). Comprobado además en vivo el 2026-10-20: un arranque contra la
   base de desarrollo aplicó `000004` («migraciones aplicadas, from 3, to 4») y los siguientes no
   aplicaron nada.
5. **Cumplido.** `pnpm run server:run` arranca el proceso y `GET /health` responde 200 en menos de un
   segundo.
6. **Cumplido, verificado a mano.** `GET /ready` respondió 200 con la infraestructura arriba, 503 con
   `hot_state` en error al parar Redis, y 200 al volver a arrancarlo (2026-10-20). No hay test automático.
7. **Cumplido con una precisión.** `GET /metrics` en `EO_METRICS_ADDR` expone desde el arranque las diez
   métricas sin etiquetas o con histograma; las cuatro con etiquetas (`eo_commands_total`,
   `eo_ws_messages_total`, `eo_protocol_errors_total`, `eo_pathfinding_requests_total`) aparecen con su
   primera observación, que es como el cliente de Prometheus trata un vector sin series. Las catorce están
   registradas.
8. **Cumplido.** Arrancar sin `EO_AUTH_JWT_SECRET` aborta el proceso con un mensaje que nombra la
   variable, junto al resto de errores de configuración detectados.
9. **Cumplido.** `pnpm run protocol:build` regenera `packages/protocol/schema/v1/*.json` sin diferencias
   respecto a lo commiteado, y la CI lo comprueba en cada push.
10. **Cumplido.** El workflow de GitHub Actions termina en verde sobre la rama principal.

---

## M1 — World

**Estado: CERRADO.** `internal/game/world` está implementado y su suite (límites, chunks, costes de
terreno, overlay de ocupación, disposición fila-mayor, determinismo y golden del generador) está en verde, y
la integración contra PostgreSQL también. La reconciliación de `EO-118` añadió lo que faltaba para que el
criterio 3 se cumpla en el arranque y no sólo en un test: el servidor compara el terreno regenerado con
`world_chunks` y aborta si difiere.

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
   `EO_WORLD_WIDTH` y `EO_WORLD_HEIGHT`: ruido de valor por hash entero de la semilla, sin `RandomSource`,
   sin `rand` y sin punto flotante. El lint de determinismo prohíbe el reloj real y el azar global en el paquete.
3. **HECHO** — Representación en memoria del mundo por chunks de `EO_CHUNK_SIZE` (32) tiles de lado:
   16×16 = 256 chunks en el MVP.
4. **HECHO** — Transformaciones de coordenadas: `chunkX = x / EO_CHUNK_SIZE`, `chunkY = y / EO_CHUNK_SIZE`
   (división entera, no desplazamiento: el tamaño de chunk no tiene por qué ser potencia de dos), ID de
   chunk `chunkY*chunksPerRow + chunkX` como uint32, y las inversas. Coordenadas lógicas `x`, `y` int32
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
   es la fuente primaria**: el mundo no cambia entre reinicios porque la semilla no cambia. En cada
   arranque posterior al primero, `WorldRepo.VerifyTerrain` compara esa copia byte a byte con lo
   regenerado y aborta si difieren: es la forma de detectar un generador que cambió sin migración de mundo.
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
- **HECHO** *integration*: generar, persistir, reiniciar y regenerar produce un mundo idéntico
  (`TestMundoPersistidoCoincideByteAByteConLaSemilla`); un byte alterado, un chunk de 1023 bytes o un chunk
  que falta hacen fallar la comprobación de arranque (`TestElArranqueRechazaUnTerrenoPersistidoQueNoEsElDeLaSemilla`).
- **HECHO** *unit*: la huella SHA-256 del mundo canónico no cambia (`TestElMundoCanonicoConservaSuHuella`).

### Documentación actualizada al cerrar

[../architecture/overview.md](../architecture/overview.md),
[../invariants/world.md](../invariants/world.md),
[../database/schema.md](../database/schema.md).

### Invariantes cubiertos

`EJE-WORLD` completo. `EJE-PERSIST` en la parte de mundo: el mapa es durable y reconstruible
desde la seed, no depende de ninguna conexión.

### Aceptación

1. **Cumplido.** Dos ejecuciones del generador con `EO_WORLD_SEED=20260909` producen los 256 chunks con el
   mismo hash (`TestGeneracionEsDeterminista`, `TestElMundoCanonicoConservaSuHuella`).
2. **Cumplido.** `SELECT count(*) FROM world_chunks` devuelve 256 y toda fila tiene
   `length(terrain) = 1024`. Lo comprueba `VerifyTerrain` en cada arranque.
3. **Cumplido.** Un reinicio del servidor produce exactamente el mismo mundo, porque se regenera desde la
   misma semilla, y el arranque comprueba que la copia de `world_chunks` coincide byte a byte con lo
   regenerado. Verificado en vivo contra la base de desarrollo («mundo existente cargado y verificado»).
4. **Cumplido.** La conversión de (511, 511) da chunk (15, 15) e ID 255 (`TestConversionTileChunk`).
5. **Cumplido.** Una consulta de transitabilidad sobre un tile `WATER` devuelve falso
   (`TestCostesDeTerreno`, `TestFueraDeLimitesEsIntransitable`).
6. **Cumplido.** Un movimiento diagonal con un ortogonal bloqueado se declara no transitable: el A\* no lo
   genera (`TestProhibidoAtajarEsquinas`) y `Validate` lo rechaza (`TestValidateComprobaciones`).
7. **Cumplido.** Coordenadas negativas o ≥ 512 se rechazan como fuera de límites (`TestLimitesDelMundo`).

---

## M2 — Player & City

**Estado: CERRADO.** El esquema, los catálogos sembrados, el dominio de jugador, ciudad y unidad, el
emplazamiento determinista de `internal/game/founding` y el bootstrap atómico existen y tienen tests
unitarios y de integración en verde; el alta se expone en `POST /api/auth/register`. El criterio 1 se
reescribió para describir el evento que el alta escribe de verdad.

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
   `TOWN_CENTER` y 1 zona urbana amurallada inicial, y 3 unidades `VILLAGER`, más **un** evento agregado
   `PlayerBootstrapped` en `world_events` con la ciudad, el centro y el número de aldeanos (y, si el centro
   cae en un territorio libre, el cambio de control de M6). Si cualquier paso falla, no queda nada. **Orden
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
9. **HECHO** — Error `CITY_NOT_FOUND` definido en el catálogo de códigos. Lo emite `unit.garrison` con una ciudad
   inexistente (M7, entregable 6; `TestRechazosDeGuarnicion`).

### Tests

- **HECHO** *unit*: `population_limit` por era; un jugador con 3 aldeanos en `STONE_AGE` tiene población 3
  sobre 20.
- **HECHO** *unit*: Civilization y Faction son independientes: cualquier combinación válida se acepta.
- **HECHO** *integration*: el bootstrap crea exactamente 1 `players`, 1 `cities` y 3 `units`
  (`TestBootstrapCreaMundoCompletoDelJugador`), con un único evento `PlayerBootstrapped`, y repetirlo con el
  mismo nombre no duplica nada (`TestElAltaRegistraUnEventoYNoSeDuplica`).
- **HECHO** *integration*: un fallo inyectado a mitad del bootstrap deja la base sin filas nuevas en
  ninguna de las tres tablas (`TestBootstrapFallidoNoDejaNadaAMedias`).
- **HECHO** *integration*: los `CHECK` rechazan un `units.status` fuera del dominio
  (`TestLasRestriccionesDeUnidadesRechazanFilasInvalidas`) y una faction fuera del catálogo
  (`TestLasRestriccionesDeJugadoresRechazanFilasInvalidas`, `TestLosCatalogosDeCivilizacionYFaccionSonLosDelCanon`).
- **HECHO** *integration*: el trigger `set_updated_at()` sella la columna en cada `UPDATE`
  (`TestElTriggerSellaUpdatedAtEnCadaUpdate`).
- **HECHO** *unit*: los tiles de la ciudad quedan bloqueados en el overlay sin cambiar su `TerrainType`
  (`TestCapaDeOcupacionNoMutaElTerreno`), y el arranque los rebloquea desde `cities`
  (`TestUnaZonaCargadaSobreUnaCiudadExcluyeSuMuralla`, integration).

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

1. **Cumplido.** Ejecutar el bootstrap sobre una base limpia deja 1 jugador, 1 ciudad, 3 aldeanos y un
   evento `PlayerBootstrapped` en `world_events` (`TestBootstrapCreaMundoCompletoDelJugador`,
   `TestElAltaRegistraUnEventoYNoSeDuplica`). El criterio original pedía «4 eventos de creación»; el alta
   escribe uno agregado, que es lo que se decidió al implementarla, y el criterio se corrigió en `EO-118`.
2. **Cumplido.** Ejecutar el bootstrap dos veces para el mismo jugador no duplica ciudad ni unidades
   (`TestElAltaRegistraUnEventoYNoSeDuplica`).
3. **Cumplido.** Un fallo forzado antes del commit deja las tres tablas exactamente como estaban
   (`TestBootstrapFallidoNoDejaNadaAMedias`).
4. **Cumplido.** `SELECT population_cap FROM eras` devuelve 20, 50, 100 y 150 para las cuatro eras
   (`TestLasErasDelCatalogoSonLasDelCanon`), y el alta toma el límite de su era (`TestElAltaTomaElLimiteDePoblacionDeSuEra`).
5. **Cumplido.** Las tres facciones existen y no hay una cuarta (`TestLosCatalogosDeCivilizacionYFaccionSonLosDelCanon`).
6. **Cumplido.** Un `INSERT` de `units.status = 'FLYING'` es rechazado por el `CHECK`; el test usa
   `WALKING` (`TestLasRestriccionesDeUnidadesRechazanFilasInvalidas`).
7. **Cumplido.** Los aldeanos nacen con `status = 'IDLE'` y hp 40 (`TestBootstrapCreaMundoCompletoDelJugador`).

---

## M3 — Realtime

**Estado: PARCIAL.** El lado servidor está implementado —`internal/auth`, `internal/websocket`
(`hub`, `session`, `server`), idempotencia y presencia en Redis, snapshot e interest management en
`internal/game/simulation`— con tests de unidad, contrato, simulación y *e2e* en verde, y el cliente de
`apps/web/` existe (entregable 13). Lo que impide cerrarlo es el entregable 2, bloqueado por una decisión
(`DEBT-18`), y dos criterios de aceptación que sólo se cumplen en parte (6 y 10).

**Objetivo.** Conectar cliente y servidor por WebSocket con autenticación por ticket, sesión,
snapshot inicial, deltas incrementales, interest management por chunks y reconexión.

**Alcance.** Handshake, sesión, envelopes, rate limiting, idempotencia, suscripción por chunk y los
mensajes de sincronización de entidades y ciudad. **No entra**: `unit.move` y la familia de
movimiento, que son M4; ni `territory.update`, que se emite en M6.

### Entregables

1. **HECHO** — Migración de `sessions` e `idempotency_keys`, con `sessions_player_idx` e
   `idempotency_keys_expiry_idx`. `idempotency_keys` nunca tuvo escritor y la eliminó `000004`
   ([ADR-014](../decisions/ADR-014-command-idempotency.md)).
2. **BLOQUEADO por una decisión que el proyecto aplazó** — Emisión del game ticket desde una API route
   de Next.js en `apps/web`: JWT HS256 firmado con `EO_AUTH_JWT_SECRET`, TTL 60 s, claims
   `{ sub: playerId, jti, iat, exp, aud: "game-server" }`. **Hoy lo emite el propio game server** desde
   `POST /api/auth/register` y `POST /api/auth/login` (`internal/httpapi`, con bcrypt). La arquitectura
   objetivo la fija
   [../decisions/ADR-010-authentication-game-ticket.md](../decisions/ADR-010-authentication-game-ticket.md).

   Al intentar cerrarlo apareció una contradicción entre reglas propias.
   [migrations.md](../database/migrations.md) establece **como regla de arquitectura** que `apps/web`
   no tendrá credenciales de PostgreSQL ni importará ningún cliente SQL, y
   [frontend.md](../architecture/frontend.md) que Next.js sólo toca `EO_AUTH_JWT_SECRET`. Si Next.js no
   puede leer la base, **no puede verificar una contraseña**: bcrypt tiene que vivir donde vive
   `password_hash`. Y el flujo de ADR-010 arranca con «sesión web ya autenticada», cuyo mecanismo
   [system-context.md](../architecture/system-context.md) marca **TBD (fuera de MVP)**.

   Es decir: no es trabajo pendiente, es una decisión pendiente. Detalle completo y las dos salidas
   posibles en `DEBT-18` de [backlog.md](backlog.md). Lo que **sí** se ha hecho es quitarle el filo:
   ambos endpoints tienen ya límite de tasa por dirección de origen, porque bcrypt sin autenticación
   previa y sin límite era un amplificador de denegación de servicio contra el proceso del game loop.
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
9. **HECHO** — **Límites y protección**: un frame de más de 16384 bytes (`EO_WS_MAX_MESSAGE_BYTES`) lo
   corta el transporte con el cierre estándar `1009` sin leerlo (`MESSAGE_TOO_LARGE` queda reservado);
   rate limit de 20 msg/s con burst 40 (`EO_WS_RATE_LIMIT_PER_SECOND`, `EO_WS_RATE_LIMIT_BURST`): agotado,
   `RATE_LIMITED` y cierre `4429` en el acto; ping cada 15 s, timeout de
   lectura de 45 s y de escritura de 10 s. **Contrapresión explícita**: cola de comandos llena → el
   comando no se ejecuta, se responde `INTERNAL_ERROR` con su `requestId` y se libera su reserva de
   idempotencia para que el reintento sí entre; cola de salida de la sesión llena
   (`EO_WS_OUTBOUND_QUEUE_SIZE`, 256) → cierre `4500` y el cliente reconecta con un snapshot limpio.
10. **HECHO, con una corrección de `EO-118`** — **Idempotencia**: `Claim` reserva el `requestId` en Redis
    con `SETNX` sobre `idem:{playerId}:{requestId}` y TTL 300 s antes de ejecutar; un `requestId` repetido
    no se re-ejecuta, y si el original ya tuvo respuesta directa el duplicado la recibe otra vez (I-5 de
    [../specs/websocket-protocol.md](../specs/websocket-protocol.md)). Si Redis no responde, el comando
    **se ejecuta igualmente**: se prefiere dejar jugar a bloquear al jugador, y es una decisión consciente.
    Este entregable decía además que el registro iba también a `idempotency_keys`; eso no se implementó y
    lo descartó [ADR-014](../decisions/ADR-014-command-idempotency.md).
11. **HECHO** — **Códigos de error de M3**. Los que el servidor emite como `system.error` son
    `INVALID_MESSAGE`, `UNSUPPORTED_VERSION`, `RATE_LIMITED` e `INTERNAL_ERROR`. `UNAUTHORIZED` y `FORBIDDEN`
    no viajan como mensaje: el handshake fallido se comunica sólo con el cierre `4401`, y `FORBIDDEN`
    está reservado. `MESSAGE_TOO_LARGE` también está reservado (el exceso lo corta el transporte con `1009`),
    y `NOT_IMPLEMENTED` no lo emite ninguna ruta: un tipo desconocido es `INVALID_MESSAGE`.
    Cierres WS: `4400` invalid, `4401` unauthenticated, `4403` forbidden, `4408` handshake timeout,
    `4429` rate limited, `4500` internal.
12. **HECHO** — **Reconexión**: al reconectar se emite un `world.snapshot` nuevo. No hay buffer de deltas
    ni reanudación por `seq`; esto es deuda técnica aceptada y registrada (`DEBT-08`).
13. **HECHO — Cliente en `apps/web`** (`EO-055`): capa de transporte que abre la conexión, envía
    `session.hello`, mantiene el ping, aplica snapshot y deltas a un store local, y renderiza en
    isométrico con PixiJS usando `screenX = (x - y) * 32` y `screenY = (x + y) * 16`
    (`TILE_W = 64`, `TILE_H = 32`). El servidor no maneja píxeles en ningún caso.
14. **HECHO** — **Métricas activas**: `eo_connected_players`, `eo_connected_websockets`,
    `eo_ws_messages_total{direction,type}`, `eo_commands_total{type,result}`,
    `eo_protocol_errors_total{code}` y `eo_redis_latency_seconds` en las operaciones de ticket e
    idempotencia.

### Tests

- **HECHO** *contract*: el decodificador Go rechaza `v` distinto de 1, `type` desconocido y propiedades fuera
  del contrato a cualquier profundidad (`TestElDecodificadorEsTanEstrictoComoElEsquema`,
  `TestVersionNoSoportada`, `TestTipoDeMensajeDesconocidoSeRechaza`); en TypeScript, además, las
  coordenadas no enteras (Vitest de `packages/protocol`).
- **HECHO** *e2e*: `seq` crece de uno en uno en todos los mensajes de una conexión
  (`TestElSeqCreceDeUnoEnUnoEnUnaConexion`).
- **HECHO** *unit*: cálculo del conjunto de chunks para un centro dado, incluidos centros pegados al borde
  del mundo donde el área 5×5 se recorta (`TestChunksEnRadioSeRecortanYVanOrdenados`).
- **HECHO** *unit* (`internal/auth`): ticket válido, caducado, firmado con otra clave, `alg=none`,
  audiencia incorrecta, claims incompletos, no reutilizable y comportamiento *fail-closed*.
- **HECHO** *e2e*: el token bucket deja pasar el burst y, agotado, responde `RATE_LIMITED` y cierra con
  `4429` (`TestUnaRafagaPorEncimaDelBurstCierraCon4429`). No tiene test unitario propio: el del
  `httpapi` es otro limitador.
- **HECHO** *simulation*: varias sesiones del mismo jugador, snapshots y deltas parciales sobre el estado
  en RAM.
- **HECHO** *e2e*: handshake completo feliz (`TestVerticalSliceEndToEnd`); ticket inválido → `4401`
  (`TestTicketInvalidoCierraLaConexion`); `jti` reutilizado → `4401`
  (`TestTicketNoSePuedeReutilizarEnOtraConexion`); ningún mensaje dentro del plazo → `4408`
  (`TestSinHandshakeEnPlazoSeCierraCon4408`). El ticket expirado y el `aud` incorrecto los rechaza la
  verificación (`TestTicketCaducado`, `TestAudienciaIncorrecta`) y siguen el mismo camino de cierre.
- **HECHO** *e2e*: un frame por encima del límite → cierre `1009`, antes y después del handshake
  (`TestUnMensajeDemasiadoGrandeCierraCon1009`).
- **HECHO** *e2e*: un `requestId` repetido no genera un segundo efecto y, si el original ya tuvo respuesta,
  la recibe (`TestComandoDuplicadoSeIgnora`, `TestUnDuplicadoTrasReconectarRecibeLaRespuestaOriginal`); con
  la cola llena, `INTERNAL_ERROR` y reintento posible (`TestConLaColaLlenaElComandoRespondeYSePuedeReintentar`).
- **PENDIENTE** *e2e*: `session.view` que desplaza el centro. El servidor responde con un `world.snapshot`
  nuevo del área (`TestSnapshotDelAreaDeInteres` cubre el snapshot en la simulación); no emite
  `entity.spawn` ni `entity.despawn` por los chunks que entran o salen, como preveía este punto, y no hay
  test de extremo a extremo.
- **HECHO** *contract*: los esquemas embebidos y el catálogo de 22 códigos de error de Go coinciden con
  los exportados por `@empires-online/protocol`, y un delta parcial serializa sólo los campos que cambian
  (`TestEntityUpdateEsUnDeltaDeVerdad`). **Ningún test Go valida los mensajes emitidos contra el JSON Schema**
  servidor→cliente: lo hace el cliente, con Zod, al recibirlos.
- **HECHO** *e2e*: un cliente WS se autentica, recibe `world.snapshot` con su ciudad y sus tres aldeanos,
  y responde a los pings (`TestVerticalSliceEndToEnd`, `TestPingPongDevuelveElRelojDelServidor`), contra
  el servidor real sobre `httptest`, sin Docker.

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

1. **Cumplido.** Un cliente con ticket válido recibe `session.welcome` y a continuación `world.snapshot`
   (`TestVerticalSliceEndToEnd`).
2. **Cumplido.** El mismo ticket usado por segunda vez es rechazado con cierre `4401`
   (`TestTicketNoSePuedeReutilizarEnOtraConexion`).
3. **Cumplido.** Un ticket con `aud` distinto de `"game-server"` es rechazado (`TestAudienciaIncorrecta`).
4. **Cumplido.** Conectar y no enviar nada durante el plazo de handshake produce cierre `4408`
   (`TestSinHandshakeEnPlazoSeCierraCon4408`, con el plazo acortado; en producción son 5 s).
5. **Cumplido.** `seq` es estrictamente creciente en todos los mensajes de una conexión
   (`TestElSeqCreceDeUnoEnUnoEnUnaConexion`).
6. **Cumplido.** El `world.snapshot` inicial contiene sólo entidades dentro del área de 5×5 chunks centrada
   en la ciudad del jugador (`TestSnapshotDelAreaDeInteres`). La excepción que había —una unidad
   `GARRISONED` seguía en el índice por chunk y salía en el snapshot de cualquiera
   ([INV-UNIT-003](../invariants/units.md#inv-unit-003))— se corrigió con M7, entregable 6: sólo la recibe
   su dueño (`TestUnaUnidadGuarnecidaSoloLaVeSuDuenoEnElSnapshot`).
7. **Cumplido.** Enviar más mensajes que el burst en un segundo produce `RATE_LIMITED` y el cierre `4429`
   (`TestUnaRafagaPorEncimaDelBurstCierraCon4429`).
8. **Cumplido tras corregir el criterio.** Decía que un mensaje de 20 KiB produce `MESSAGE_TOO_LARGE` sin
   cerrar con `4500`. Lo que se decidió —y fija la spec del protocolo, §6.1— es que el transporte lo corta
   con el cierre estándar `1009` sin leerlo: ni `MESSAGE_TOO_LARGE` ni `4500`
   (`TestUnMensajeDemasiadoGrandeCierraCon1009`).
9. **Cumplido.** Reconectar tras una caída devuelve un snapshot coherente sin intervención manual
   (`TestUnDuplicadoTrasReconectarRecibeLaRespuestaOriginal` reconecta y recibe el snapshot).
10. **Parcial.** Los mensajes que tienen contract test coinciden con el contrato, pero ningún test valida
    todos los mensajes emitidos contra el JSON Schema servidor→cliente.

---

## M4 — Movement

**Estado: PARCIAL.** El lado servidor está completo y es la parte mejor cubierta por tests del proyecto:
`internal/pathfinding`, `internal/domain/movement`, `internal/game/loop` y el *vertical slice* de
`internal/game/simulation` —con reemplazo de orden, cancelación, los siete rechazos, los tests de
recuperación, reproducibilidad y volcado por lotes— están **en verde**. El cliente dibuja el movimiento
de forma continua; lo que impide cerrar es la otra mitad del entregable 15, la corrección gradual hacia
la verdad del servidor (`EO-075`), que existe en `apps/web` y no está conectada.

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
   con `tickNumber` uint64 estrictamente creciente —al arrancar se reancla al reloj, `max(current_tick,
   (ahora − epoch_ms) / período)`, para no repetir números tras una caída
   ([ADR-015](../decisions/ADR-015-tick-continuity.md))— y el instante de cada tick leído una vez del
   `Clock` (no `epoch_ms + tickNumber × período`). Orden fijo de fases: 1 `drain commands`,
   2 `validate & apply commands`, 3 `advance movement`, 4 `resolve simulation` (reservado para combate,
   vacío en MVP), 5 `process timers/scheduled events`, 6 `update world state / interest sets`,
   7 `emit deltas`, 8 `enqueue persistence`. El **catch-up** usa calendario en tiempo absoluto: los ticks
   perdidos se descartan, jamás se ejecutan en ráfaga. `applyCommand` **aísla los pánicos** de un comando
   concreto para que un bug en una regla no tumbe la simulación entera.
8. **HECHO** — **Prohibición de I/O bloqueante contra Postgres dentro del tick**: la persistencia va por
   canal a workers ordenados por entidad, con hasta **3 intentos con backoff**; si se agotan,
   `OnPermanentFailure` devuelve un comando al loop, que compensa en el mundo —un inicio de movimiento que
   no llegó a disco detiene la unidad— ([ADR-013](../decisions/ADR-013-persistence-pipeline.md)). Métrica `eo_persistence_queue_depth` y `eo_game_tick_overruns_total` incrementada cuando
   un tick supera su período.
9. **HECHO** — Flujo de validación del comando en el orden canónico: ownership → estado de unidad →
   destino → A\* → creación del movimiento → simulación → notificación. Errores asociados:
   `UNIT_NOT_FOUND`, `UNIT_NOT_OWNED`, `UNIT_NOT_MOVABLE`, `UNIT_DEAD`, `UNIT_GARRISONED`,
   `TARGET_OUT_OF_BOUNDS`, `TARGET_NOT_WALKABLE`, `PATH_NOT_FOUND`, `PATH_TOO_LONG` e `INTERNAL_ERROR`.
   `INVALID_TARGET` está en el catálogo y ninguna ruta lo emite: el destino lo validan los dos `TARGET_*`.
10. **HECHO** — **Mensajes cliente→servidor de M4**: `unit.move { unitId, target:{x,y} }` y
    `unit.cancel_move`. El cliente nunca envía secuencias de posiciones.
11. **HECHO** — **Mensajes servidor→cliente de M4**: `unit.move.accepted`, `unit.move.rejected`,
    `unit.movement.started`, `unit.movement.completed`, `unit.movement.cancelled`.
    `unit.movement.started.payload` es
    `{unitId, movement:{movementId, path[], startTimeMs, arrivalTimeMs, target}}` y
    `unit.movement.cancelled.payload.reason` pertenece a
    `REPLACED | CANCELLED_BY_PLAYER | PATH_BLOCKED | UNIT_DEAD | SERVER`.
12. **PARCIAL** — **Separación Command / Event / State**: `MoveUnit` y `CancelMovement` son comandos,
    `Unit.status = MOVING` es estado, y los hechos de movimiento se comunican como deltas de red
    (`unit.movement.started`, `unit.movement.completed`, `unit.movement.cancelled`). **No se escriben en
    `world_events`**: el movimiento no deja rastro en el registro de eventos; su historial es la propia
    tabla `unit_movements`.
13. **HECHO** — **Recuperación tras crash** (`simulation.Hydrate`): al arrancar se cargan los movimientos
    `ACTIVE`. Si el movimiento venció durante la caída, la unidad aparece en el destino y el movimiento se
    cierra `COMPLETED`; si sigue en curso, se reanuda desde la polilínea; si la polilínea es inválida,
    queda `FAILED` y la unidad se queda donde estaba, porque nunca se teletransporta a nadie por un dato
    dudoso.
14. **HECHO** — **Persistencia diferenciada**: transacción propia para inicio y finalización de
    movimiento, que escribe también la unidad —`MOVING` en su origen, `IDLE` en su tile final— y se
    aplica primero en RAM y se **encola** hacia los workers, porque el tick no puede hacer I/O de
    PostgreSQL ([ADR-013](../decisions/ADR-013-persistence-pipeline.md)); dirty-flag con flush cada
    `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` (50 ticks, 5 s) para las posiciones consolidadas de unidades en
    reposo (`hp` no se vuelca: nada lo modifica en el MVP); posición durante un movimiento activo **no se persiste**, se deriva
    de `unit_movements`. La ventana entre la aceptación y el `COMMIT` es un RPO documentado: ver
    `DEBT-17` en [backlog.md](backlog.md).
15. **PARCIAL — Cliente** (`EO-075`): la interpolación visual entre waypoints existe y se usa
    (`renderPosition` en la escena PixiJS, con sus tests en `apps/web/src/state/interpolation.test.ts`). La
    corrección gradual hacia la verdad del servidor cuando llega un delta (`reconcile`) está escrita y
    probada, pero **la escena no la llama**. El cliente nunca decide la posición final ni el ETA.
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
  cancelación, los siete rechazos —sin que ninguno llegue a invocar el A\*—, el movimiento con el jugador
  desconectado, el volcado por lotes y la orden hacia el tile propio en tránsito.
- **HECHO** *simulation*: diez unidades moviéndose a la vez, con sus diez A\* en el mismo tick, no producen
  ningún overrun (`TestDiezUnidadesMoviendoseNoPasanDelPresupuestoDelTick`). Es un presupuesto de tiempo de
  pared: se salta con `-race`, que multiplica el coste de CPU, y se mide en las ejecuciones sin él (el paso
  «Cobertura» de la CI y la suite local).
- **HECHO** *unit*: la posición derivada nunca adelanta al reloj, como propiedad sobre polilíneas generadas
  con semilla fija (`TestLaPosicionNuncaAdelantaAlReloj`).
- **HECHO** *integration*: una nueva orden sobre una unidad en movimiento cancela la anterior a
  `CANCELLED` y crea la nueva `ACTIVE` en la misma transacción, y el índice único parcial rechaza un
  segundo `ACTIVE` insertado a mano (`TestIndiceUnicoImpideDosMovimientosActivos`).
- **HECHO** *e2e*: mover una unidad de otro jugador devuelve `UNIT_NOT_OWNED`
  (`TestNoSePuedeComandarUnaUnidadAjena`).
- **HECHO** *recovery*: los tests sobre `simulation.Hydrate` cubren el movimiento vencido durante la
  caída (unidad en el destino, movimiento `COMPLETED`), el movimiento en curso (se reanuda desde la
  polilínea), la polilínea inválida (`FAILED`, unidad donde estaba), la llegada de la fila que no cuadra
  con su polilínea y la unidad guarnecida con un movimiento; y, contra PostgreSQL,
  `TestRecuperacionCompletaTrasReinicio`.
- **HECHO** *contract*: los cinco mensajes de movimiento existen en el JSON Schema embebido y
  `unit.movement.started` lleva la polilínea completa (`TestTiposDeMensajePresentesEnElEsquema`,
  `TestMovementStartedLlevaLaPolilineaCompleta`). No se validan contra el esquema.

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

1. **Cumplido.** `unit.move` a un tile alcanzable devuelve `unit.move.accepted` seguido de
   `unit.movement.started` (`TestVerticalSliceMovimiento`, `TestVerticalSliceEndToEnd`).
2. **Cumplido.** La unidad llega al tile destino en el tick de su `arrival_time_ms` exacto
   (`TestVerticalSliceMovimiento`: llegada calculada a los 3000 ms y completada en ese tick).
3. **Cumplido.** Ordenar un segundo movimiento deja exactamente un movimiento `ACTIVE` para esa unidad, en
   RAM (`TestNuevaOrdenReemplazaLaAnterior`) y en disco (`TestIndiceUnicoImpideDosMovimientosActivos`).
4. **Cumplido.** `unit.cancel_move` deja el movimiento en `CANCELLED` y la unidad en `IDLE` en el último
   waypoint alcanzado (`TestCancelacionExplicita`), y el cierre lleva esa posición a disco
   (`TestElCierreDelMovimientoLlevaLaPosicionFinal`).
5. **Cumplido.** Mover una unidad ajena devuelve `unit.move.rejected` con `UNIT_NOT_OWNED`
   (`TestRechazosDeMovimiento`, `TestNoSePuedeComandarUnaUnidadAjena`).
6. **Cumplido.** Un destino intransitable por terreno devuelve `TARGET_NOT_WALKABLE`
   (`TestDestinoIntransitableSeRechaza` con montaña; `WATER` es igual de intransitable,
   `TestCostesDeTerreno`).
7. **Cumplido.** Un destino más allá de `EO_PATHFINDING_MAX_DISTANCE` devuelve `PATH_TOO_LONG`
   (`TestLimiteDeDistanciaSeAplica`).
8. **Cumplido.** Matar y rearrancar el servidor a mitad de trayecto no pierde el movimiento
   (`TestRecuperacionMovimientoEnCurso`, `TestRecuperacionCompletaTrasReinicio`).
9. **Cumplido.** `eo_game_tick_overruns_total` permanece en cero durante un escenario de 10 unidades
   moviéndose a la vez (`TestDiezUnidadesMoviendoseNoPasanDelPresupuestoDelTick`, medido sin `-race`: con el
   detector de carreras el primer tick ya pasa de 100 ms, y eso rompió la CI de `86878d5`).
10. **Parcial.** En el cliente la unidad se dibuja de forma continua entre waypoints (`renderPosition`); la
    corrección suave al llegar un delta no está conectada (`EO-075`). No se ha verificado a la vista en
    esta reconciliación.

---

## M5 — Offline protection & Safe Zones

**Estado: CERRADO.** La CI pasó entera sobre el commit que lo cierra (`5f692d5`). Presencia y
protección offline están implementadas desde el principio. Las Safe Zones se implementaron después:
índice tile → zona, ocultamiento en la fase 5 y filtrado por destinatario, verificados nivel por
nivel según [../specs/safe-zones.md](../specs/safe-zones.md) §13. Todos los criterios de aceptación se
cumplen.

Una precisión que no bloquea el cierre: **el mundo canónico no tiene ninguna safe zone**. Su siembra
es una decisión de diseño de mundo que la spec declara `TBD (fuera de MVP)`. El mecanismo funciona en
cuanto `safe_zones` tenga filas, y los tests lo demuestran con zonas sintéticas.

**Objetivo.** Modelar la presencia del jugador, la transición a protección tras el cooldown y las
zonas seguras, todo calculado y validado por el servidor.

**Alcance.** Presencia en Redis, máquina de estados de `presence_state`, `protection_until`,
`safe_zones` y el estado `HIDDEN`. **No entra**: consecuencias de combate de la protección, que
llegan con Combat, fuera del MVP; ni la geometría de las zonas del mundo canónico.

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
6. **HECHO** — Cálculo de safe zones en `internal/domain/safezone`: predicados `DENSE_FOREST` (tile
   `FOREST`) y `CAVERN` (tile transitable con un vecino `MOUNTAIN` en las 8 direcciones), índice denso
   tile → zona de 1 MiB en un mundo de 512 × 512, construcción determinista por `id` ascendente y
   resolución de solapes a favor del `id` menor. `postgres.SafeZoneRepo` lee la tabla al arrancar, y
   `cmd/server` construye el índice después de marcar las murallas, que quedan fuera de toda zona. La
   seguridad la calcula y la valida siempre el servidor: ningún mensaje del cliente menciona zonas.
7. **HECHO** — Uso del estado `HIDDEN`: la fase 5 oculta a las unidades `IDLE` en reposo dentro de una
   zona y revela a las que dejan de estarlo; `unit.move` sobre una unidad oculta la revela en el mismo
   tick. Interest management: el propietario recibe `entity.update` con el estado; los terceros,
   `entity.despawn { reason: "HIDDEN" }` o `entity.spawn` por `Hub.BroadcastChunkExcept`; y
   `world.snapshot` filtra las unidades ocultas ajenas por destinatario. Cero mensajes nuevos en el
   protocolo. El estado se persiste por dirty-flag; el tick no hace I/O.
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
  pasa a `PROTECTED` y ni un tick antes, y reconectar dentro del margen no degrada la ciudad
  (`TestCicloDePresenciaYProteccion`, `TestReconexionDentroDelMargenNoDegradaLaCiudad`,
  `TestVariasSesionesDelMismoJugador`).
- **HECHO** *integration* (Redis real): la clave de presencia se registra con TTL, el latido la renueva y
  sólo la sesión propietaria la retira (`TestPresenciaSeRegistraYExpira`, `TestLatidoRenuevaLaPresencia`,
  `TestSoloLaSesionPropietariaRetiraLaPresencia`). La transición a `OFFLINE_PENDING` no se prueba
  contra Redis porque, por diseño, Redis no la decide: la cubre el nivel *simulation*.
- **HECHO** *integration* (PostgreSQL real): `OFFLINE_PENDING`, `PROTECTED` y la vuelta a `ONLINE` se
  persisten, y volver a `ONLINE` deja `protection_until = NULL` (`TestTransicionesDePresenciaSePersisten`).
- **HECHO** *unit* (safe zones): los dos ejemplos resueltos de la spec reproducidos literalmente, las 8
  direcciones de `CAVERN`, bordes inclusivos, solapes, recorte por terreno y por murallas, zonas
  inválidas y determinismo del índice. Catorce tests en `internal/domain/safezone`.
- **HECHO** *simulation* (safe zones): diez tests en `internal/game/simulation/safezones_test.go`, que
  además comprueban `INV-SAFE-003` sobre todo el mundo tras cada tick. Verificados por mutación: cuatro
  fallos introducidos a propósito —despawn a todo el chunk, snapshot sin filtro, mover sin revelar,
  ocultar en tránsito— ponen alguno en rojo.
- **HECHO** *e2e de transporte*: una unidad `HIDDEN` no aparece en el `world.snapshot` ni en los deltas de
  otro jugador cuya área de interés la contiene, y su dueño sí la ve
  (`TestUnaUnidadOcultaEsInvisibleParaTercerosYVisibleParaSuDueno`). Vive en `internal/websocket` y no en
  integración: a qué conexión llega cada mensaje no depende de PostgreSQL.
- **HECHO** *integration* (safe zones): los `CHECK` de `safe_zones`, el orden de carga, la persistencia
  de `HIDDEN` a través de flush y reinicio, y el recorte por una ciudad creada por el alta real. Cuatro
  tests en `safezones_integration_test.go`, que aloja también el de recuperación de la presencia.
- **HECHO** *contract*: `entity.despawn` admite `reason: "HIDDEN"` y `entity.update` y `world.snapshot`
  admiten `status: "HIDDEN"`, leídos del JSON Schema exportado.
- **HECHO** *recovery*: un reinicio no pierde el `presence_state` persistido ni reinicia el cooldown.
  `TestUnReinicioNoReiniciaElCooldownDeProteccion` rearranca la simulación desde PostgreSQL 200 s después
  de la desconexión: la ciudad sigue en `OFFLINE_PENDING` a los 299 s y pasa a `PROTECTED` a los 300 s,
  contados desde la desconexión y no desde el arranque. El equivalente para `HIDDEN` es
  `TestHiddenSePersisteSobreviveAlReinicioYSeRevierteSiLaZonaDesaparece`.

### Documentación actualizada al cerrar

[../specs/presence.md](../specs/presence.md),
[../specs/safe-zones.md](../specs/safe-zones.md) (estado `Implemented`),
[../invariants/territory.md](../invariants/territory.md) (fichas `INV-SAFE-*`),
[../invariants/units.md](../invariants/units.md) (`INV-UNIT-008`),
[../architecture/game-loop.md](../architecture/game-loop.md) (fase 5),
[../architecture/networking.md](../architecture/networking.md) (filtro por destinatario),
[../database/schema.md](../database/schema.md).

### Invariantes cubiertos

`EJE-CITY` completo. `EJE-UNIT` en la parte de `HIDDEN`. `EJE-PERSIST` en la separación
Redis/PostgreSQL: la presencia es transitoria, el `presence_state` es durable. Del registro:
`INV-SAFE-001` a `INV-SAFE-007` e `INV-UNIT-008`.

### Aceptación

1. **Cumplido.** Cerrar la última conexión y dejar vencer el margen de reconexión (30 s) lleva la ciudad
   a `OFFLINE_PENDING`.
2. **Cumplido.** Reconectar durante el grace mantiene `ONLINE` sin pasar por `OFFLINE_PENDING`; tener
   otra sesión abierta también lo mantiene.
3. **Cumplido.** Transcurrido el cooldown, la ciudad está en `PROTECTED` y se emitió un `city.update`.
4. **Cumplido.** Reconectar desde `PROTECTED` devuelve a `ONLINE` y deja `protection_until` a NULL.
5. **Cumplido por construcción.** Cambiar `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` a 10 hace que la
   transición ocurra a los 10 segundos, sin cambios de código: `cmd/server` pasa el valor a
   `simulation.Deps.ProtectionCooldown`. Ningún test lo ejecuta con 10; los tests usan 300.
6. **Cumplido con zonas sintéticas.** Una unidad dentro de una safe zone tiene `status = 'HIDDEN'` y no
   es visible para terceros. En el mundo canónico no hay zonas que pisar hasta que se decida su siembra.
7. **Cumplido.** Un reinicio del servidor conserva el estado de presencia de todas las ciudades y no
   reinicia el cooldown (`TestUnReinicioNoReiniciaElCooldownDeProteccion`).

---

## M6 — Territories

**Estado: CERRADO.** El circuito funciona y está verificado de extremo a extremo: geometría sembrada,
índice en RAM, cambio de dueño transaccional, evento de dominio, delta de red y overlay en el cliente.
Todos los tests de la sección existen y pasan. El entregable 8, que esperaba una decisión de juego, la
tiene desde [ADR-016](../decisions/ADR-016-foreign-territory-actions.md).

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
8. **HECHO, por decisión** — Uso de `FORBIDDEN` para acciones no permitidas dentro de territorio ajeno.

   La decisión está en [ADR-016](../decisions/ADR-016-foreign-territory-actions.md): **ninguna acción del
   MVP se restringe por territorio**, y `FORBIDDEN` conserva su significado de *operar sobre entidades de
   otro jugador*, no de *estar dentro de su territorio*. La razón de juego es concreta: guarnecer en una
   ciudad aliada exige estar junto a ella, es decir, dentro del territorio del aliado, y un jugador
   desconectado no puede convertirse en un muro permanente del mapa. Lo que sí existe es la clasificación
   de cada acción de mundo —hostil o no, y dónde se permite— con un test que exige clasificar todo comando
   nuevo (`simulation.ActionPolicyOf`).

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
- ✔ *simulation*: un cambio de control se aplica en la RAM y se emite una sola vez a la huella
  completa, y sin cambio no se emite nada (`internal/game/simulation/territory_test.go`).
- ✔ *integration*: las restricciones del esquema rechazan cada fila inválida con su nombre exacto
  (`TestLasRestriccionesDeTerritoriosRechazanFilasInvalidas`).
- ✔ *contract*: `territory.update` serializa exactamente la vista del esquema exportado, con
  `ownerId: null` explícito, y los tipos de dueño coinciden con su enum
  (`internal/protocol/territory_contract_test.go`). Hasta `EO-118` esta línea afirmaba que lo validaba
  `contract_test.go`, que sólo comprueba que el nombre del tipo aparece en el esquema.
- ✔ *unit*: todo mensaje cliente→servidor está clasificado como acción de mundo o de sesión, y ninguna
  acción del MVP es hostil ni se restringe por territorio (`TestTodaAccionDelCatalogoEstaClasificada`,
  `TestNingunaAccionDelMVPEsHostilNiSeRestringePorTerritorio`).

### Documentación actualizada al cerrar

[../specs/territory.md](../specs/territory.md),
[../architecture/networking.md](../architecture/networking.md),
[../database/schema.md](../database/schema.md),
[../specs/websocket-protocol.md](../specs/websocket-protocol.md). La decisión del entregable 8:
[ADR-016](../decisions/ADR-016-foreign-territory-actions.md).

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

**Estado: CERRADO.** Existen `internal/domain/diplomacy` (ciclo de vida del tratado) e
`internal/domain/garrison` (condiciones de entrada y salida), su ejecución transaccional en
`postgres.TreatyRepo` y `postgres.GarrisonRepo`, el barrido de caducidad enganchado a la fase 5 del tick
y, desde [ADR-017](../decisions/ADR-017-garrison-commands.md), la puerta de entrada: los comandos
`unit.garrison` y `unit.ungarrison`. Los tratados siguen creándose por vía administrativa o de seed, que es
lo que el alcance de este milestone fija; lo que se valida es su efecto sobre el dominio, ahora también por
la red.

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
3. **HECHO** — `internal/domain/diplomacy`: ciclo de vida del treaty y regla dura de que **solo un
   treaty `ACTIVE`** habilita garrison. Un treaty `PROPOSED`, `EXPIRED` o `BROKEN` nunca habilita nada.
4. **HECHO** — Regla de garrison: una unidad puede guarnecerse en una ciudad ajena si y solo si existe
   un treaty `ACTIVE` entre ambos jugadores con `allows_garrison` verdadero. En caso contrario,
   `TREATY_REQUIRED`.
5. **HECHO** — Transición de estado de la unidad a `GARRISONED`, con la cancelación de su movimiento
   `ACTIVE` en la **misma transacción** (`postgres.GarrisonRepo.Enter`), respetando `EJE-MOVE`. Una
   unidad `GARRISONED` con un movimiento vivo sería un estado que ninguna regla sabe interpretar: el
   bucle la seguiría moviendo mientras el resto del sistema la cree dentro de una ciudad. `unit.move`
   sobre una guarnecida ya rechazaba con `UNIT_GARRISONED`.
6. **HECHO** — Efecto en el interest management: una unidad que entra en garrison genera
   `entity.despawn` con `reason: GARRISONED` para los observadores que dejan de verla, y `entity.spawn`
   al salir; su dueño recibe `entity.update` con la ciudad anfitriona en `garrisonCityId`, y el snapshot
   de un tercero no la trae. Llegó con los comandos `unit.garrison` y `unit.ungarrison`
   ([ADR-017](../decisions/ADR-017-garrison-commands.md)), la entrada en dos fases —el tick valida, la
   transacción decide el tratado— y la salida decidida por el tick con compensación.

   La frase original pedía además que la ciudad emitiera `city.update`. Se retiró: guarnecer no cambia
   nada de lo que transporta `city.update` —la unidad sigue contando en la población de su ciudad de
   origen ([INV-GARR-009](../invariants/diplomacy.md#inv-garr-009))—, y un `city.update` sin cambio
   contradiría `T-CITY-C-002`.
7. **HECHO** — Persistencia transaccional de treaties y garrisons, y registro de los cambios en
   `world_events`.
8. **HECHO en su parte implementable** — Expiración de treaties evaluada en la fase 5 del tick, con
   transición `ACTIVE → EXPIRED`.

   El tick **no** consulta la base: encola el barrido en la cola de persistencia y lo hace **muestreado
   a una vez por segundo**, no en cada tick. A 10 Hz serían diez consultas por segundo para descartar
   casi siempre cero filas; un tratado que caduca un segundo tarde no cambia nada del juego, ese
   tráfico sí. Cada tratado caduca en su propia transacción: uno que falle porque otra vía ya lo
   terminó no debe impedir que caduquen los demás.

   **La consecuencia sobre los garrisons vigentes NO se implementa**, y no es un olvido: la spec de
   guarnición elige *expulsión diferida con periodo de gracia* (§6.4) y a continuación explica por qué
   está fuera de MVP —`garrisons` no tiene dónde persistir el vencimiento de la gracia, y la duración
   exigiría una variable `EO_` que el canon no define y que la spec se niega a inventar—. Habilitarla
   pide una migración aditiva y una decisión de configuración.
9. **HECHO por construcción** — `CITY_PROTECTED` y `TREATY_REQUIRED` no compiten: RN-GARR-009 establece
   que la protección offline del anfitrión **no** bloquea la guarnición, porque la protección es contra
   agresión y no contra logística. `CanEnter` no consulta el estado de presencia de la ciudad en ningún
   punto, y un test lo fija (`TestLaProteccionOfflineDelAnfitrionNoBloqueaLaEntrada`). No hay orden que
   verificar porque no hay concurrencia entre ambos códigos.

### Tests

55 tests nuevos: 19 en `internal/domain/diplomacy`, 22 en `internal/domain/garrison`, 14 de integración
contra PostgreSQL real y 2 de simulación sobre el muestreo del barrido.

- ✔ *unit*: la máquina de estados acepta `PROPOSED → ACTIVE`, `ACTIVE → EXPIRED` y `ACTIVE → BROKEN`, y
  rechaza el resto. El test **enumera los dieciséis pares posibles** y rechaza los trece restantes, en
  lugar de escribir a mano una lista negra que dejaría fuera justo los casos que a nadie se le ocurren.
- ✔ *unit*: `allows_garrison` verdadero con estado distinto de `ACTIVE` no habilita garrison, para los
  cuatro estados y los tres tipos de tratado.
- ✔ *integration*: garrison sin treaty devuelve el rechazo por tratado y **no escribe nada** —la
  transacción revierte entera—; con treaty `ACTIVE` y `allows_garrison` la unidad queda `GARRISONED`,
  releída de la base y no del objeto en memoria.
- ✔ *integration*: guarnecer una unidad en movimiento lo deja en `CANCELLED` y sin ninguno `ACTIVE`, en
  la misma transacción.
- ✔ *integration*: un tratado vencido se detecta y transiciona a `EXPIRED`, conservando `expires_at`
  como prueba de cuándo *debía* caducar y no de cuándo se detectó.
- ✔ *integration*: el índice único parcial impide un segundo tratado activo del mismo tipo entre el
  mismo par, y admite otro de tipo distinto.
- ✔ *recovery*: un reinicio conserva treaties y garrisons y no resucita movimientos cancelados.
- ✔ *simulation*: el barrido de caducidad se **muestrea** y no ocurre en cada tick.
- ✔ *unit*: `unit.move` sobre una unidad `GARRISONED` devuelve `UNIT_GARRISONED` (ya existía).
- ✔ *contract*: los comandos de guarnición, `garrisonCityId` y el motivo `GARRISONED` coinciden con el
  esquema exportado (`TestLosComandosDeGuarnicionSonLosDelEsquema`, `TestUnitViewLlevaLaCiudadAnfitrionaSiempre`,
  `TestEntityUpdateDistingueEntradaSalidaYSinCambio`, `TestEntityDespawnAdmiteElMotivoGarrisoned`).
- ✔ *simulation* y *e2e*: entrada en dos fases, rechazos, salida al anillo, salida pendiente,
  compensación y visibilidad por destinatario (`internal/game/simulation/garrison_test.go`,
  `TestLaGuarnicionPorLaRedSoloLaVeSuDueno`); idempotencia de la entrada y la salida contra PostgreSQL
  (`TestUnaEntradaRepetidaEsIdempotenteYNoDuplicaElEvento`, `TestUnaSalidaRepetidaEsIdempotenteYNoDuplicaElEvento`).

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
6. Un treaty con fecha de expiración pasada transiciona a `EXPIRED` dentro del segundo siguiente. Decía
   «dentro del tick siguiente»; el barrido se muestrea a una vez por segundo por la razón del entregable 8,
   y el criterio se ajusta a esa decisión (`TestLaCaducidadDeTratadosSeMuestreaYNoOcurreEnCadaTick`,
   `TestLaCaducidadSeDetectaYTransicionaAExpired`).
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
