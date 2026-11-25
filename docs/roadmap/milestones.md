# Milestones

Detalle ejecutable de M0 a M7 y de los milestones posteriores (E1 a E7): alcance exacto, entregables numerados, tests obligatorios, documentación afectada, invariantes cubiertos y criterios de aceptación verificables.

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
    `NNNNNN_nombre.up.sql` / `NNNNNN_nombre.down.sql` y `embed.go` (`//go:embed *.sql`), aplicadas con
    **golang-migrate** embebido reescribiendo la URL al esquema `pgx5`. Fuera de producción las aplica **el
    arranque del servidor** (`EO_MIGRATE_ON_START`, verdadero salvo con `EO_ENV=production`); en producción,
    el binario `cmd/migrate`, antes de desplegar. En los dos casos de forma idempotente, y abortando ante un
    esquema `dirty`. Las migraciones existentes son `000001_initial_schema` (esquema completo, incluido el
    trigger `set_updated_at()` y `world_state` con `epoch_ms`), `000002_seed_catalogs`,
    `000003_territory_control_version`, `000004_drop_idempotency_keys` y `000005_players_without_credentials`
    ([ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md)).
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

**Estado: CERRADO (2026-10-21).** El lado servidor —`internal/auth`, `internal/websocket` (`hub`, `session`,
`server`), idempotencia y presencia en Redis, snapshot e interest management en `internal/game/simulation`—
tiene tests de unidad, contrato, simulación y *e2e* en verde, y el cliente de `apps/web/` existe (entregable
13). Lo que lo tenía abierto se cerró así: el entregable 2 con [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md), que saca la identidad del Game Server y
hace que Next.js emita el ticket; la aceptación 10 con la validación contra el JSON Schema de todo mensaje que el
servidor real emite en los e2e; y el e2e pendiente de `session.view`.

**Objetivo.** Conectar cliente y servidor por WebSocket con autenticación por ticket, sesión,
snapshot inicial, deltas incrementales, interest management por chunks y reconexión.

**Alcance.** Handshake, sesión, envelopes, rate limiting, idempotencia, suscripción por chunk y los
mensajes de sincronización de entidades y ciudad. **No entra**: `unit.move` y la familia de
movimiento, que son M4; ni `territory.update`, que se emite en M6.

### Entregables

1. **HECHO** — Migración de `sessions` e `idempotency_keys`, con `sessions_player_idx` e
   `idempotency_keys_expiry_idx`. `idempotency_keys` nunca tuvo escritor y la eliminó `000004`
   ([ADR-014](../decisions/ADR-014-command-idempotency.md)).
2. **HECHO** ([ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md)) — Emisión del game ticket desde `apps/web`: JWT HS256 firmado con
   `EO_AUTH_JWT_SECRET`, TTL 60 s, claims `{ sub: playerId, jti, iat, exp, aud: "game-server" }`, emitido con
   `node:crypto` (`src/server/tokens.ts`) en el alta, el login y `POST /api/auth/ticket`. La identidad vive en
   un almacén propio de `apps/web` (base `empires_identity`, con su rol): cuentas con hash `scrypt` y sesiones
   web con una cookie opaca `HttpOnly`. El alta funda al jugador en el mundo llamando a `POST /internal/players`
   del Game Server con un token de audiencia `game-server-internal`; la cuenta sólo se activa cuando el jugador
   existe (`INV-PLAYER-009`). El Game Server ya no ve contraseñas: `internal/httpapi` perdió el alta, el login,
   bcrypt y su limitador, y la migración `000005` borró `players.password_hash`.

   Lo que lo tenía bloqueado era una contradicción entre reglas propias: [migrations.md](../database/migrations.md)
   prohibía a `apps/web` tener credenciales de PostgreSQL, y una contraseña sólo se verifica donde vive su hash.
   ADR-018 la resuelve precisando la regla a su motivo —`apps/web` nunca toca el **esquema del juego**— y dándole
   un almacén propio. Un fixture compartido ata el formato del ticket entre los dos lenguajes.
3. **HECHO** — `internal/auth`: `Verifier` (de una audiencia: la del ticket o la de la fundación), `Authenticator`
   y `Claims`; `Issuer` sólo lo usan ya los tests. Verificación de firma, `exp` y `aud`, y consumo del `jti` en Redis con `SETNX` sobre `ticket:jti:{jti}` y TTL 120 s para
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
13. **HECHO — Cliente en `apps/web`** (`EO-055`): capa de transporte que pide el ticket con la cookie de
    sesión, abre la conexión, envía `session.hello`, mantiene el ping, aplica snapshot y deltas a un store local, y renderiza en
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
- **HECHO** *e2e*: `session.view` que desplaza el centro dos chunks: el servidor responde con un
  `world.snapshot` del área nueva, con el `requestId` del comando, y el terreno sólo de los chunks que la
  sesión no tenía (`TestSessionViewRecentraConUnSnapshotDelAreaNueva`, en un mundo de 256 × 256 donde el área
  de 5 × 5 ya no lo cubre todo). Este punto preveía `entity.spawn` y `entity.despawn` por los chunks que entran
  y salen; la spec del protocolo (§7.3) fijó un snapshot, y el criterio se reescribe para decir lo que la spec
  manda.
- **HECHO** *e2e* (2026-10-26): una **unidad** que cruza de chunk aparece en el área de otra sesión con
  `entity.spawn` y su polilínea, seguida de sus `entity.update`; desaparece del área que deja con
  `entity.despawn OUT_OF_INTEREST` y nada más; y quien observa los dos chunks sólo recibe `entity.update`
  (`TestUnaUnidadQueCruzaDeChunkApareceYDesapareceEnElAreaDeOtraSesion`, tres sesiones colocadas con
  `session.view` en un mundo de 256 × 256; `Hub.BroadcastChunkTransition`, networking.md §4.3).
- **HECHO** *contract*: los esquemas embebidos y el catálogo de 22 códigos de error de Go coinciden con
  los exportados por `@empires-online/protocol`, y un delta parcial serializa sólo los campos que cambian
  (`TestEntityUpdateEsUnDeltaDeVerdad`). Y **todo mensaje que el servidor real emite en los e2e** se valida
  contra `server-message.schema.json` (`internal/websocket/schema_e2e_test.go`, con
  `santhosh-tekuri/jsonschema/v6` fijado en `go.sum` y sólo en tests): la validación vive en la única función
  que lee del socket, y `TestMain` exige que cada uno de los 14 tipos servidor→cliente se haya validado al
  menos una vez. Tres no los emitía ningún e2e —`city.update`, `territory.update` y
  `unit.movement.cancelled`— y `contract_e2e_test.go` los provoca. Verificado por mutación: un campo renombrado,
  un campo que el contrato no declara o un tipo que ningún test emite hacen fallar el paquete.
- **HECHO** identidad (`apps/web` y Game Server): orquestación del alta y del login con un Game Server falso,
  bordes HTTP y cookie, almacén contra PostgreSQL real (incluido que el rol de identidad no abre la base del
  juego), contrato del ticket entre lenguajes, fundación contra PostgreSQL (idempotencia, carrera, audiencias,
  logs) y logs del handshake. Detalle en [../specs/player.md](../specs/player.md) §13; cada guarda nueva se
  verificó por mutación.
- **HECHO** *e2e*: un cliente WS se autentica, recibe `world.snapshot` con su ciudad y sus tres aldeanos,
  y responde a los pings (`TestVerticalSliceEndToEnd`, `TestPingPongDevuelveElRelojDelServidor`), contra
  el servidor real sobre `httptest`, sin Docker.

### Documentación actualizada al cerrar

[../specs/websocket-protocol.md](../specs/websocket-protocol.md),
[../decisions/ADR-010-authentication-game-ticket.md](../decisions/ADR-010-authentication-game-ticket.md),
[ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md), [../specs/player.md](../specs/player.md),
[../architecture/networking.md](../architecture/networking.md),
[../architecture/system-context.md](../architecture/system-context.md),
[../database/schema.md](../database/schema.md), [../database/migrations.md](../database/migrations.md),
[../operations/configuration.md](../operations/configuration.md),
[../operations/local-development.md](../operations/local-development.md).

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
10. **Cumplido.** Todo mensaje que el servidor real emite en los tests e2e se valida contra el JSON Schema
    servidor→cliente, y el paquete falla si alguno de los 14 tipos del contrato no se ha emitido y validado
    (`schema_e2e_test.go`, `contract_e2e_test.go`).
11. **Cumplido** (nuevo, del entregable 2). El ticket lo emite Next.js y el Game Server no ve contraseñas:
    `smoke` recorre el alta en la web, la fundación, el handshake y una reconexión con un ticket pedido con la
    cookie; verificado el 2026-10-21 contra `empires_test` y `empires_identity_test`, también en el navegador.

---

## M4 — Movement

**Estado: CERRADO (2026-10-20).** El lado servidor es la parte mejor cubierta por tests del proyecto:
`internal/pathfinding`, `internal/domain/movement`, `internal/game/loop` y el *vertical slice* de
`internal/game/simulation` —con reemplazo de orden, cancelación, los siete rechazos, los tests de
recuperación, reproducibilidad y volcado por lotes— están **en verde**. El cliente dibuja el movimiento de
forma continua y corrige gradualmente las discontinuidades que traen los deltas (`EO-075`, entregable 15),
con test de cada guarda y verificado en el navegador. El entregable 12 se cerró por decisión: el movimiento
no se registra en `world_events` (`EO-072`, [../specs/movement.md](../specs/movement.md) §11).

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
12. **HECHO, con una parte cerrada por decisión** — **Separación Command / Event / State**: `MoveUnit` y
    `CancelMovement` son comandos, `Unit.status = MOVING` es estado, y los hechos de movimiento se comunican
    como deltas de red (`unit.movement.started`, `unit.movement.completed`, `unit.movement.cancelled`).
    **No se escriben en `world_events`**, y es una decisión de la spec
    ([../specs/movement.md](../specs/movement.md) §11), no un hueco: el historial durable es la propia tabla
    `unit_movements`, los deltas no se derivan de `world_events` porque el MVP no tiene bus de eventos, y
    duplicar la escritura más frecuente del juego sin un lector no compensa. Añadirlo después es aditivo
    (`EO-072`, ahora `P3`).
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
15. **HECHO — Cliente** (`EO-075`): la interpolación visual entre waypoints (`renderPosition`, tests en
    `apps/web/src/state/interpolation.test.ts`) y la corrección gradual hacia la verdad del servidor cuando
    un delta hace saltar el objetivo: `RenderReconciler` (`apps/web/src/render/reconciler.ts`), al que la
    escena PixiJS pide la posición de cada unidad en cada frame. Corrige como un desfase que decae
    (τ = 60 ms), salta por encima de 2 tiles y no corrige más de 300 ms; el movimiento sin discontinuidades
    se dibuja exactamente en la interpolación. Diseño en
    [../architecture/frontend.md](../architecture/frontend.md) §5.3. El cliente nunca decide la posición
    final ni el ETA.
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
  `TestMovementStartedLlevaLaPolilineaCompleta`). Desde el cierre de M3, los cinco se validan contra el esquema cada vez que el servidor real los emite en un e2e (`schema_e2e_test.go`).
- **HECHO** *unit* del cliente (`apps/web/src/render/reconciler.test.ts`, quince tests): sin
  discontinuidades el dibujo es la interpolación exacta en todo el trayecto; sustitución, cancelación y
  llegada anticipada se corrigen sin salto, sin oscilar y hasta cero; salto por encima de 2 tiles y
  deslizamiento justo en el umbral; `EPS`; la cota de 300 ms, también con un objetivo que salta en cada
  frame; la misma convergencia a 30, 60 y 144 fps; corrección del reloj y vuelta de una pestaña oculta;
  reaparición sin deslizarse; picking sobre la posición dibujada. Once mutaciones de sus guardas hacen
  fallar la batería ([../specs/movement.md](../specs/movement.md) §13, «Cliente»).

### Documentación actualizada al cerrar

[../specs/movement.md](../specs/movement.md),
[../architecture/frontend.md](../architecture/frontend.md) (§5 y §7.3, y su cabecera de estado),
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
10. **Cumplido.** En el cliente la unidad se dibuja de forma continua entre waypoints (`renderPosition`) y
    las correcciones que traen los deltas se reparten en unos frames (`RenderReconciler`, quince tests).
    Verificado en el navegador el 2026-10-20 contra un servidor real sobre `empires_test`: con el reloj de
    Pixi conducido a ~56 fps se ordenó un trayecto y se sustituyó dos veces a mitad de camino. En la
    primera sustitución el servidor reinicia la ruta desde el último tile alcanzado, `(493,73)`, con el
    dibujo ya en `492.29`: sin corrección habría sido un retroceso de 0,71 tiles en un frame; con ella, 0,36
    en un frame largo de 54 ms y después 0,05, 0,03, 0,02… hasta empalmar con la ruta nueva. Fuera de las
    sustituciones, el mayor paso por frame fue el de la propia marcha en los frames más largos (0,10 tiles
    en 62 ms), sin errores en la consola.

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
8. **HECHO** — Emisión de `city.update` con `presence_state` y `protection_until` en cada transición.
   **Cerrado por decisión** en lo demás ([../specs/presence.md](../specs/presence.md) §11, 2026-10-21): la
   presencia no se registra en `world_events` en el MVP. El texto anterior marcaba como hecho el «evento de
   dominio `CityProtectionEngaged` registrado en `world_events`», que nunca existió; la reconciliación del
   2026-10-21 lo encontró y la spec decide no escribirlo hasta Combat, con sus motivos.
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
  `TestSoloElAtaqueEsHostilYNingunaSeRestringePorTerritorio`). **Precisión (2026-11-23):** el segundo se llama hoy
  `TestSoloAtaqueSaqueoAsedioYReclamarSonHostilesYNingunaSeRestringePorTerritorio`, porque el saqueo, el asedio y la
  reclamación se sumaron a las hostiles.

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
   `TestLaCaducidadSeDetectaYTransicionaAExpired`). **Precisión (2026-11-23):** el primero se retiró en E6
   ([ADR-027](../decisions/ADR-027-hot-treaties.md)), cuando el barrido muestreado `treaty.expire` se sustituyó por
   vencimientos optimistas en la fase 5; la caducidad la cubren hoy `TestUnaOfertaCaducaALas24Horas` y
   `TestUnPreavisoVencidoDuranteLaCaidaSeAplicaAlArrancar`, y el segundo sigue en la integración.
7. Tras reiniciar, treaties y garrisons conservan su estado exacto.

---

## E1 — Madera

**Estado: HECHO (2026-11-02)** — fusionado en `main` con la CI en verde y probado en vivo: humo con recolección,
casa, aldeano, reconexión y reinicio a mitad de viaje, y desconexión recolectando. La prueba en vivo encontró que
una ciudad podía nacer sin bosque al alcance, corregido con `RN-CITY-023`. Primer milestone después del vertical slice, y el primero de la fase «Economy» de
[roadmap.md](roadmap.md) §5. Su contrato es [SPEC-ECON-WOOD](../specs/economy-wood.md), aceptada el
2026-10-27 con sus seis decisiones de producto.

**Objetivo.** Que el jugador tenga algo que hacer durante más de dos minutos y una razón para volver: mandar
aldeanos a por madera, gastarla en una casa que sube el tope de población y entrenar aldeanos con los que
recolectar más.

**Alcance.** Un recurso (`WOOD`) almacenado por ciudad, la tarea de recolección `GATHER_WOOD` con el estado de
unidad `WORKING`, la casa (`HOUSE`) en la zona urbana 3×3, el entrenamiento de un `VILLAGER` a la vez por
ciudad, y la recuperación de todo ello tras un reinicio. **No entra** (spec §2): otros recursos, agotamiento del
bosque, capacidad de almacén, aldeanos constructores, colas de producción, edificios fuera del 3×3, robo de
madera.

### Entregables

1. **HECHO** — Registro de la familia `INV-ECON` ([../invariants/economy.md](../invariants/economy.md)),
   derogación de `INV-CITY-009` y su sustituto `INV-CITY-017`, y
   [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md), antes que el código.
2. **HECHO** — Migración `000006`: `city_stocks`, `unit_tasks`, `buildings`, `city_productions` y el valor
   `WORKING` en `units_status_valid`, con su `down` reversible.
3. **HECHO** — Dominio puro: `internal/domain/economy` (stock y costes), `internal/domain/building` (casa,
   emplazamiento, fórmula del límite), `internal/domain/production` (entrenamiento) e `internal/domain/task`
   (máquina de estados de `GATHER_WOOD`), con los valores de la spec §4.5 en los catálogos de código.
4. **HECHO** — `unit.gather` y la tarea en la simulación: tramos como movimientos normales, la fase 3
   decide al llegar y la fase 5 al vencer `phase_until_ms`; el depósito marca el stock sucio; ningún viaje nuevo
   con el dueño ausente (`RN-ECON-005`).
5. **HECHO** — `city.build` y `city.train` en dos fases ([ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md)):
   el tick valida y reserva, la transacción debita y escribe el efecto, el resultado vuelve por el buzón.
6. **HECHO** — Finalización de casas (`population_limit + 5`) y nacimiento del aldeano en la fase 5.
7. **HECHO** — `postgres.EconomyRepo` con una compensación declarada para cada escritura (ADR-022) y el
   volcado de depósitos en la fase 8.
8. **HECHO** — Arranque: la validación de carga repara `INV-ECON-003`, `INV-ECON-004` e `INV-CITY-017`;
   `Hydrate` reanuda tareas, casas y producciones con sus instantes absolutos.
9. **HECHO** — Protocolo v1: `unit.gather`, `city.build`, `city.train`, `building.spawn`, `building.update`,
   `buildings[]` en `world.snapshot`, `stocks` en `city.update` y `CityView`, `WORKING`, `carrying`, y los
   códigos `INSUFFICIENT_RESOURCES` y `CITY_BUSY`.
10. **HECHO** — Manejadores WebSocket de los tres comandos, clasificados según
    [ADR-016](../decisions/ADR-016-foreign-territory-actions.md).
11. **HECHO** — Cliente: contador de madera, botones «Casa» y «Aldeano», orden de recolección sobre un
    bosque, edificios y unidad `WORKING` en la escena ([../architecture/frontend.md](../architecture/frontend.md)
    §4.4), probado en el navegador contra un servidor vivo el 2026-11-02.
12. **HECHO** — Escenario golden `madera` (`TestLosEscenariosReproducenSuGolden`) y un viaje de recolección
    completo en `scripts/smoke.mjs`, en verde contra un servidor vivo.

### Tests

Los de la spec §9: *unit* de cada regla `RN-ECON-*` y de la máquina de estados; *simulation* con el golden
`madera` y la desconexión a mitad de viaje; *recovery* en cada fase de la tarea, en una construcción y en un
entrenamiento; *integration* de los `CHECK`, la PK y los `UNIQUE`, del débito con fallo inyectado y de la
idempotencia por `requestId`; *contract* de los mensajes y códigos nuevos; *e2e* de los tres flujos; y del
cliente, el store, los selectores y la decisión entre recolectar y mover.

### Documentación actualizada al cerrar

[../specs/economy-wood.md](../specs/economy-wood.md), [../specs/unit.md](../specs/unit.md) (`WORKING`),
[../specs/city.md](../specs/city.md) (`population_limit` con casas),
[../specs/websocket-protocol.md](../specs/websocket-protocol.md),
[../database/schema.md](../database/schema.md), [../architecture/networking.md](../architecture/networking.md),
[../architecture/frontend.md](../architecture/frontend.md), [../invariants/economy.md](../invariants/economy.md).

### Invariantes cubiertos

`INV-ECON-001` a `INV-ECON-006` e `INV-CITY-017`. `EJE-SEC`: el cliente envía el bosque, el tipo de edificio y
el tile, nunca cantidades, costes ni tiempos. `EJE-PERSIST` para stocks, tareas, edificios y producciones.

### Aceptación

1. `unit.gather` sobre un bosque produce `unit.movement.started`, después `entity.update` con `WORKING` y, tras
   el viaje de vuelta, `city.update` con `stocks.WOOD` aumentado en 10.
2. `unit.gather` sobre un tile que no es `FOREST` devuelve `system.error` con `INVALID_TARGET`.
3. `city.build` con madera suficiente produce `building.spawn` y, 15 s después, `building.update` con
   `COMPLETE` y `city.update` con `populationLimit` cinco mayor.
4. `city.build` o `city.train` sin madera devuelve `INSUFFICIENT_RESOURCES` y el stock no cambia, en la RAM ni
   en la base.
5. Un segundo `city.train` con una producción en curso devuelve `CITY_BUSY`.
6. `city.train` termina con `entity.spawn` de un `VILLAGER` `IDLE` junto a la ciudad.
7. Con el dueño desconectado a mitad de viaje, la unidad deposita una vez más y queda `IDLE`.
8. Tras un reinicio a mitad de cada fase, lo que estaba en curso termina en su instante absoluto.
9. El golden `madera` se reproduce y la CI está en verde.

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
| M7 → E1 | La economía se apoya en la presencia (M5), el movimiento (M4) y la cola de persistencia con sus compensaciones; E1 empieza con el vertical slice cerrado. |

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

---

## E2 — Incursión

**Estado: HECHO (2026-11-02)**, fusionado en `main` (`bc87cce`) con la CI en verde el 2026-11-03. Contrato:
[SPEC-COMBAT-RAID](../specs/combat-raid.md), aceptada el 2026-11-02 con sus ocho decisiones de producto, y
[ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md).

**Objetivo.** Que salir de casa cueste algo: una unidad militar entrenada con madera puede matar a una unidad
enemiga y llevarse lo que cargaba, y la protección, las Safe Zones, el ocultamiento y los tratados por fin
protegen de algo.

**Alcance.** El tope de la protección offline y el reanclaje al arrancar (prerrequisito, DEBT-02); `WARRIOR`;
`unit.attack` con persecución; resolución simultánea en la fase 4; HP, `DEAD` y botín. **No entra** (spec §2):
atacar edificios o ciudades, asedio, respuesta automática, daño a distancia, curación.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-024 y la familia `INV-COMBAT`, antes que el código.
2. **HECHO** — Tope de la protección (`EO_CITY_PROTECTION_MAX_HOURS`) y `Simulation.AfterBoot`.
3. **HECHO** — Protocolo: `unit.attack`, `WARRIOR`, `ATTACKING`.
4. **HECHO** — Simulación: orden, persecución, fase 4, muerte y botín; `unit.attack` es la primera acción hostil.
5. **HECHO** — Persistencia: migración `000007`, transacción de muerte, `hp` y botín en el volcado.
6. **HECHO** — Cliente: botón «Guerrero», orden de ataque con clic sobre una unidad enemiga, vida y
   `ATTACKING` en la escena; incursión en `scripts/smoke.mjs --incursion`.
7. **HECHO** — Prueba en vivo (2026-11-02): humo con incursión en verde, ataque y entrenamiento desde el
   navegador, reanclaje al arrancar observado en el servidor.

## E3 — Defensa

**Estado: HECHO (2026-11-03)**, fusionado en `main` (`1e99a96`) con la CI en verde el mismo día. Contrato:
[SPEC-COMBAT-RESPONSE](../specs/combat-response.md), aceptada el 2026-11-03 con sus ocho decisiones de producto.

**Objetivo.** Que defender no exija estar delante de la pantalla: un guerrero parado responde a los golpes sobre
él o sobre sus unidades cercanas, persigue con correa y vuelve a su puesto.

**Alcance.** Respuesta automática disparada por golpes (radio de guardia), correa y vuelta al puesto, protección
simétrica del atacante y el evento `UnitAttacked` que E2 prometió. **No entra** (spec §2): agresión proactiva,
huida de aldeanos, posturas por unidad, salir de la guarnición a defender.

### Entregables

1. **HECHO** — Aceptación: spec y retirada de `RN-COMBAT-011`.
2. **HECHO** — Simulación: alertas en la fase 4, enfrentamiento automático con puesto y correa, vuelta al
   puesto, `RN-COMBAT-018` en el predicado de `INV-COMBAT-002`. Tests en
   `internal/game/simulation/combat_response_test.go`, con el golden «escolta» y la recuperación.
3. **HECHO** — Persistencia: `UnitAttacked` en `world_events`, con `auto` en las respuestas
   (`TestElPrimerGolpeDejaUnitAttacked`, integration). Sin migración.
4. **HECHO** — Configuración: `EO_COMBAT_GUARD_RADIUS_TILES` y `EO_COMBAT_LEASH_TILES`.
5. **HECHO** — Humo: `scripts/smoke.mjs --incursion` lleva el aldeano de la víctima escoltado por un guardia
   que responde solo. La víctima repone guardia y aldeanos si le faltan y busca los que quedaron varados en
   incursiones anteriores, para que el humo se pueda repetir. De paso se corrigió la cola de mensajes del
   humo: un mensaje entregado a una espera también se quedaba encolado, y una espera posterior del mismo tipo
   lo recibía otra vez.
6. **HECHO** — Prueba en vivo (2026-11-03): humo con incursión en verde; el guardia de la víctima respondió
   sin órdenes y, al golpear al incursor, los guerreros parados del incursor respondieron a su vez y lo
   mataron. `UnitAttacked` con `auto: true` escrito en `world_events`.

## E4 — Bosque

**Estado: HECHO (2026-11-03)**, fusionado en `main` (`a35bc1d`) con la CI en verde el mismo día. Contrato:
[SPEC-ECON-FOREST](../specs/economy-forest.md), aceptada el 2026-11-03 con sus siete decisiones de producto, y
[ADR-025](../decisions/ADR-025-forest-wood-layer.md).

**Objetivo.** Que el espacio sea un recurso disputado: cada tile de bosque tiene 100 de madera, la recolección la
gasta, el aldeano busca el tile siguiente y el bosque talado vuelve a crecer 24 horas después.

**Alcance.** La capa de madera aparte del terreno, su carga exacta y su regeneración; la búsqueda del tile
siguiente; `forests` y `forest.update` en el protocolo; el dibujo en el cliente; la fundación con al menos diez
tiles con madera. **No entra** (spec §2): talar para abrir camino, otros recursos, plantar.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-025, `INV-ECON-007` e `INV-ECON-008`.
2. **HECHO** — Simulación: la capa en la RAM (`forest.go`), la carga parcial, la búsqueda del tile siguiente y la
   regeneración; tests en `forest_test.go` y el golden «tala».
3. **HECHO** — Persistencia: migración `000008` y el descuento por delta en la transacción de la carga, idempotente
   frente a reintentos (`forest_integration_test.go`).
4. **HECHO** — Protocolo y cliente: `forests`, `forest.update` y el bosque talado en su propia capa de la escena.
5. **HECHO** — Fundación: diez tiles con madera alcanzables a ≤ 8; el alta lee de la base los tiles agotados.
6. **HECHO** — Humo y prueba en vivo (2026-11-03): `scripts/smoke.mjs --tala` en verde. Tres aldeanos agotaron el
   tile (279, 7), se escribió `ForestDepleted` y uno pasó solo a (278, 6). En el navegador, el tile agotado se ve
   como tala con tocones y el de al lado, a medias.

## E5 — Saqueo

**Estado: HECHO (2026-11-03)**, fusionado en `main` (`82f3db8`) con la CI en verde el mismo día y probado en vivo. Contrato:
[SPEC-COMBAT-PILLAGE](../specs/combat-pillage.md), aceptada el 2026-11-03 con sus nueve decisiones de producto, y
[ADR-026](../decisions/ADR-026-pillage-two-phase-stock-debit.md).

**Objetivo.** Que tener cosas cueste algo: un guerrero junto a la muralla enemiga roba madera del almacén golpe a
golpe, hasta un suelo, y la tiene que llevar a casa; los guerreros de la ciudad la defienden.

**Alcance.** La orden `unit.pillage`, el alcance en el anillo de la muralla, los golpes de saqueo en la fase 4 con
su débito en dos fases, el suelo, la presencia del saqueador, la defensa con la regla de E3, el aviso
`pillagedAtMs` al dueño y el clic de saqueo en el cliente. **No entra** (spec §2): vida de edificios, destruir
casas, que la ciudad caiga, máquinas de asedio, niebla de guerra.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-026, `INV-COMBAT-004` e `INV-COMBAT-005`.
2. **HECHO** — Protocolo: `unit.pillage` y `pillagedAtMs`, con el JSON Schema y los contract tests
   (`TestElComandoDeSaqueoEsElDelEsquema`, `TestElAvisoDeSaqueoEstaEnElEsquemaDeCityUpdate`).
3. **HECHO** — Simulación (`pillage.go`): la orden, la aproximación al anillo, los golpes con el suelo y la
   presencia, la defensa y las dos fases; tests en `pillage_test.go` con el golden «saqueo». Tres mutaciones (sin
   suelo, sin presencia, sin defensa) las detecta algún test.
4. **HECHO** — Persistencia: la transacción del golpe, idempotente por versión, con los depósitos que viajan y fallo
   inyectado (`pillage_integration_test.go`); e2e por WebSocket (`pillage_e2e_test.go`).
5. **HECHO** — Cliente: clic de saqueo con un guerrero sobre una ciudad ajena (`decideTileOrder`) y el aviso en el
   HUD (`pillageAlertActive`).
6. **HECHO** — Humo y prueba en vivo (2026-11-03): `scripts/smoke.mjs --saqueo` en verde (22 comprobaciones). El
   aldeano de `smoke_victim`, varado junto a `smoke_runner` desde E3, volvió a casa y juntó madera; el guerrero 15
   cruzó el mapa hasta (449, 181), junto a la muralla de (451, 179), y dio cuatro golpes de 5 cada 1,5 s (cuatro
   `CityPillaged` en la base) hasta quedar `IDLE` con 20 de carga. En el navegador, con `smoke_victim` conectado,
   el HUD mostró «¡Están saqueando tu ciudad!» desde el primer golpe hasta cinco segundos después del último.

## E6 — Diplomacia

**Estado: HECHO (2026-11-03)**, fusionado en `main` (`800a269`) y con la CI en verde desde `7943e87`, que corrigió un
test intermitente: el par de un tratado instalado sin ordenar no daba paz (ver el entregable 9). Contrato:
[SPEC-DIPLOMACY](../specs/diplomacy.md), aceptada el 2026-11-03 con sus nueve decisiones de producto, y
[ADR-027](../decisions/ADR-027-hot-treaties.md).

**Objetivo.** Que el riesgo de E2 a E5 tenga contrapeso: los jugadores proponen, aceptan, rechazan, retiran y rompen
pactos de no agresión y alianzas mientras juegan, con un preaviso que impide la traición por sorpresa.

**Alcance.** `treaty.propose`, `treaty.respond` y `treaty.cancel`; la RAM como fuente de las órdenes y la paz; los
vencimientos optimistas en la fase 5 (ofertas, preavisos, `expires_at`); la expulsión de las guarniciones al terminar
una alianza; `treaty.update` y `treaties[]` en el snapshot; la migración `000009`; el panel del cliente. **No entra**
(spec §2): `TRADE`, plazos al firmar, chat, alianzas visibles para terceros.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-027, `INV-DIPLO-001`, `INV-DIPLO-002` e `INV-GARR-008` vigente.
2. **HECHO** — Migración `000009` y dominio `diplomacy` con la máquina de estados ampliada y las reglas de cada orden.
3. **HECHO** — Protocolo: los tres comandos, `treaty.update` y `treaties[]`, con el JSON Schema y los contract tests
   (`diplomacy_contract_test.go`).
4. **HECHO** — Simulación (`treaties.go`): las órdenes en dos fases, los vencimientos, la paz en caliente y la expulsión;
   golden «alianza» y el resto en `treaties_test.go`. Dos mutaciones (responde quien propuso, sin expulsión) las
   detecta algún test.
5. **HECHO** — Persistencia: cada transición, idempotente, índices de `INV-DIPLO-002` y fallo inyectado
   (`treaty_orders_integration_test.go`); e2e con dos jugadores (`diplomacy_e2e_test.go`).
6. **HECHO** — Cliente: barra de propuesta, ofertas y tratados con la cuenta atrás del preaviso (`diplomacy.ts`).
7. **HECHO** — Humo y prueba en vivo (2026-11-03): `scripts/smoke.mjs --diplomacia` en verde (20 comprobaciones):
   pacto propuesto y aceptado, saqueo rechazado por la paz, ruptura con preaviso. En el navegador, con
   `smoke_victim`, el HUD mostró la cuenta atrás y la conservó tras reiniciar el servidor. El preaviso venció con el
   servidor parado: al arrancar, el primer barrido dejó el tratado `BROKEN` con `broken_at` en su instante exacto
   (19:02:15) y escribió `TreatyBroken`, y el HUD dejó de mostrarlo.
8. **HECHO** — Fallo encontrado al implementarlo: los catálogos de métricas no tenían los trabajos y comandos de E2
   y E5; corregido, con un `TestMain` que lo impide.
9. **HECHO** — Fallo encontrado por la CI de `800a269`: `SetTreaties` guardaba el par tal como llegaba y `AtPeace` lo
   busca ordenado. En producción no pasa (la base lo guarda ordenado), pero dos tests de E2 y E5 fallaban unas 6 veces
   de cada 100. Corregido en `0f96c3b` con `TestUnTratadoConElParSinOrdenarDaPazEnLasDosDirecciones`.

## E7 — Registro

**Estado: HECHO (2026-11-03)**, fusionado en `main` (`a0e2096`) con la CI en verde. Contrato:
[SPEC-EVENT-LOG](../specs/event-log.md), aceptada el 2026-11-03 con sus seis decisiones de producto, y
[ADR-028](../decisions/ADR-028-event-log-from-world-events.md).

**Objetivo.** Que el riesgo de E2 a E6 se vea: al volver, el jugador sabe qué unidades perdió, cuánta madera le
robaron, qué hicieron solas las suyas y qué cambió en sus tratados, también si estaba conectado sin mirar.

**Alcance.** `event_log.fetch`, `event_log.seen` y `event_log.page`, atendidos por la conexión sin pasar por el
loop; `counterpart_player_id` y los datos que faltaban en los eventos; la agrupación al leer; el cursor de lo visto
con su tope marcable; la migración `000010`; la ventana «Mientras no estabas» y el botón «Registro». **No entra**
(spec §2): avisos en vivo, correo, la economía propia, los ataques sin consecuencia.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-028, `INV-SEC-009` e `INV-PERSIST-006`.
2. **HECHO** — Migración `000010` y los escritores de los eventos con el otro jugador (`counterpart_player_id`) y
   `atMs`, `unitType`, `from` y `byPlayerId` en el payload; la simulación pasa el instante, el tipo y el autor
   (`combat_test.go`, `pillage_test.go`, `treaties_test.go`).
3. **HECHO** — Dominio `eventlog`: qué evento entra para quién, la agrupación y el corte de la página
   (`eventlog_test.go`).
4. **HECHO** — Persistencia: la lectura de la página, el tope marcable y el cursor (`eventlog_integration_test.go`, 6
   tests). Con el margen de asentado a cero, fallan los dos tests de `INV-PERSIST-006`. Las muertes y los saqueos
   anteriores a la migración se leen con el autor de su payload.
5. **HECHO** — Protocolo y conexión: los dos comandos y `event_log.page` con el JSON Schema, los contract tests
   (`eventlog_contract_test.go`) y un e2e con un registro falso (`eventlog_e2e_test.go`). Son mensajes de sesión en
   el catálogo de ADR-016.
6. **HECHO** — Cliente: la ventana «Mientras no estabas», el botón «Registro» con su contador y la paginación
   (`eventLog.ts`).
7. **HECHO** — Humo y prueba en vivo (2026-11-03). El servidor aplicó la migración 9 → 10 al arrancar. Con
   `scripts/smoke.mjs --saqueo --diplomacia` en verde (26 comprobaciones), en el navegador `smoke_victim` vio la
   ventana con la ruptura del pacto, la oferta, el saqueo de 5 de madera y los de E2 y E5 (anteriores a la
   migración), todos de «smoke_runnerpolis»; tras «Entendido» y una recarga, la ventana no volvió y «Registro» quedó
   sin contador. `--registro` en verde (19 comprobaciones): la víctima ve `TREATY_BREAK_ANNOUNCED`,
   `TREATY_OFFERED`, `PILLAGED` y `UNIT_LOST`; `smoke_runner`, `TREATY_ACCEPTED` y `PILLAGE_DONE`; y al
   reconectar el cursor sigue en el mismo id.
8. **HECHO** — Fallo encontrado en el navegador: la mayúscula inicial de cada entrada cambiaba el nombre de la ciudad
   («Smoke_runnerpolis»). Corregido: sólo se pone a «Otro jugador», con
   `el nombre de la ciudad no se toca, tampoco al principio` (`eventLog.test.ts`).

## E8 — Asedio

**Estado: HECHO (2026-11-04)**, fusionado en `main` (`3b1cd48`) con la CI en verde el mismo día. Contrato:
[SPEC-COMBAT-SIEGE](../specs/combat-siege.md), aceptada el 2026-11-04 con sus diez decisiones de producto, y
[ADR-029](../decisions/ADR-029-population-over-limit-and-house-destruction.md).

**Objetivo.** Que la ciudad misma corra riesgo, no sólo lo que guarda: un guerrero junto a la muralla enemiga
derriba una casa golpe a golpe, la ciudad pierde las plazas que daba y, si queda por encima de su límite, conserva
sus unidades pero no entrena hasta volver bajo él.

**Alcance.** La orden `unit.demolish`, el alcance junto a la casa, los golpes de asedio en la fase 4 con su vida y
su olvido, la destrucción optimista con su escritura, `INV-CITY-002` reescrito con su trigger, la carga que ya no
sube el límite, la defensa con la regla de E5, el aviso `besiegedAtMs`, `BuildingDestroyed` en el registro, la
migración `000011` y el cliente. **No entra** (spec §2): la muralla y el Centro Urbano, que la ciudad caiga,
máquinas de asedio, reparar casas, niebla de guerra.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-029, `INV-CITY-002` reescrito, la excepción de `INV-ECON-004` retirada e
   `INV-COMBAT-002` e `INV-COMBAT-005` ampliados.
2. **HECHO** — Protocolo: `unit.demolish`, `building.destroyed`, `hp` y `maxHp` de los edificios y `besiegedAtMs`,
   con el JSON Schema y los contract tests (`siege_contract_test.go`); `HOUSE_LOST` y `HOUSE_DESTROYED` en el registro.
3. **HECHO** — Migración `000011` y persistencia: el trigger, el único parcial por tile, la destrucción idempotente, la
   carga sin casas destruidas y el recuento exento; integración con fallo inyectado (`siege_integration_test.go`, 8
   tests). Fallo encontrado al probarlo: el trigger, que corre antes que los `CHECK`, le quitaba el error a
   `cities_population_limit_check` con un límite negativo; corregido en la migración antes de aplicarse fuera de los
   tests.
4. **HECHO** — Simulación (`siege.go`): la orden, el alcance, los golpes, el olvido del daño, la destrucción con su
   reencolado, la ciudad por encima del límite, la defensa y el aviso; golden «asedio» y recuperación, 15 tests en
   `siege_test.go` y un e2e por WebSocket (`siege_e2e_test.go`). Seis mutaciones (sin presencia, sin revalidar, sin
   defensa, sin olvido, bono siempre restado, tile libre al instante) las detecta su test.
5. **HECHO** — Registro: `BuildingDestroyed` en `eventlog.EventTypes` y `Classify`, para la víctima y para el autor.
6. **HECHO** — Cliente: clic sobre una casa ajena, barra de vida, HUD de población por encima del límite, botones de
   entrenar con su motivo, aviso y entradas del registro, con sus tests.
7. **HECHO** — Humo y prueba en vivo (2026-11-04). El servidor aplicó la migración 10 → 11 al arrancar, y la validación de
   carga no encontró nada. `scripts/smoke.mjs --saqueo --asedio --registro`: la primera pasada, con el saqueo ya en
   verde, encontró un fallo del propio humo (el `city.build` de la víctima llevaba `tile` en vez de `x` e `y`;
   corregido en `27855fd`); la segunda, `--asedio --registro`, en verde (29 comprobaciones). `smoke_victim` construyó
   una casa en (450, 178), el guerrero 15 la golpeó (150 → 142, con el aviso `besiegedAtMs`), la casa cayó
   (`building.destroyed`) y el límite bajó de 25 a 20; el registro de la víctima tiene `HOUSE_LOST` y el de
   `smoke_runner`, `HOUSE_DESTROYED`. En el navegador, con `smoke_victim` conectado, el HUD mostró «¡Están
   derribando tus casas!», la población en 1/25 y la barra de vida sobre la casa; tras la caída, 1/20, y al recargar
   el registro decía «smoke_runnerpolis derribó una de tus casas». En la base, la casa 3 quedó `DESTROYED` con su
   `BuildingDestroyed`, que nombra a la víctima y al autor.
8. **HECHO** — Fallos encontrados: el trigger le quitaba el error al `CHECK` del límite negativo (entregable 3), el
   humo enviaba mal el `city.build` (entregable 7) y el `title` del botón «Registro» no nombraba las casas
   derribadas (`6f6496b`).

## E9 — Piedra

**Estado: HECHO (2026-11-04)**, fusionado en `main` (`d12db5e`) con la CI en verde el mismo día. Contrato:
[SPEC-ECON-STONE](../specs/economy-stone.md), aceptada el 2026-11-04 con sus diez decisiones de producto, y
[ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md).

**Objetivo.** Que la geografía pese en la economía y que haya algo por lo que comerciar: la piedra sólo está en las
colinas, que el mapa concentra en siete manchas, y su primer uso es llevar una ciudad a la Edad de Bronce.

**Alcance.** `STONE` en `city_stocks`; `unit.gather` sobre una colina (`GATHER_STONE`); la capa de piedra por tile
con su agotamiento y su regeneración; el depósito al alcance antes de aceptar un viaje; `city.advance` en dos fases,
pagando madera y piedra, que no deja entrenar mientras dura y sube el límite al terminar; `INV-CITY-018`; la
migración `000012`; el cliente. **No entra** (spec §2): comercio, otras eras, saquear piedra, piedra en la
fundación, niebla de guerra.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-030, `INV-CITY-018` y `INV-ECON-002`, `005`, `007` y `008` ampliados; la
   tensión «4 vs. 5 (recursos)» en los pilares.
2. **HECHO** — Dominio: `forest` pasa a `deposit`, con una capa por terreno; `STONE` y `Costs` en `economy`;
   `GATHER_STONE` en `task`; el paquete `advance` con el único paso del catálogo.
3. **HECHO** — Migración `000012` y persistencia: `hill_stone`, `city_era_advances`, el encargo que debita todos los
   recursos o ninguno, el fin idempotente que nunca baja la era, `Train` que no entrena durante un avance; integración en
   `stone_integration_test.go`. La tabla de migraciones de `migrations.md`, que llegaba a `000006`, llega ya a `000012`.
4. **HECHO** — Simulación: la capa compartida, la recolección de piedra, `RN-ECON-020`, el stock por recurso,
   `city.advance` y su fin; `stone_test.go`, `advance_test.go` y el golden «cantera» (los de «madera» y «tala» sólo
   ganan `STONE: 0` y `carryingResource`). Ocho mutaciones las detecta su test.
5. **HECHO** — Protocolo y WebSocket: `city.advance`, `hill.update` y los campos nuevos, con contract tests
   (`stone_contract_test.go`) y e2e (`stone_e2e_test.go`).
6. **HECHO** — Cliente: contador de piedra, era, botón «Edad de Bronce» con su motivo y su cuenta atrás, colina como
   destino de recolección y capa de colina picada (`stone.test.ts`).
7. **HECHO** — Humo y prueba en vivo (2026-11-04). El migrador llevó la base de desarrollo de 11 a 12, la carga no
   encontró nada y `EO_DEVSEED_STOCK_FOR=smoke_runner,smoke_victim pnpm run dev:seed` dejó 200 de piedra en las dos
   ciudades. En el navegador, con `smoke_victim`: el HUD mostraba «Edad de Piedra», Madera 400 y Piedra 200 y el botón
   «Edad de Bronce» con su coste; al pulsarlo, los dos recursos a 0, «Aldeano» y «Guerrero» desactivados con «La ciudad
   está avanzando de era» y la cuenta atrás; a los 90 s, «Edad de Bronce», población 1/50 y los botones otra vez
   activos. `scripts/smoke.mjs --piedra --avance` con `smoke_runner`: con el tope por defecto, la orden de piedra se
   rechaza con `PATH_TOO_LONG` (entregable 8); con `EO_PATHFINDING_MAX_NODES=50000`, 22 comprobaciones en verde: la
   colina (169, 182) a 163 tiles, una ida de 165, la carga con `hill.update` (quedan 90) y `carryingResource: STONE`, el
   depósito (piedra 200 → 210), el avance con `CITY_BUSY` al entrenar y la Edad de Bronce con límite 50. En la base, las
   dos ciudades en `BRONZE_AGE` con límite 50, sus `EraAdvanceOrdered` y `EraAdvanced`, ningún avance colgado y la colina
   en 90. No se comprobó en el navegador el dibujo de la colina picada, que queda a cientos de tiles de las ciudades
   de prueba; lo cubren los tests del store y de la proyección.
8. **HECHO** — Fallos encontrados: el humo tomaba como aldeano de la piedra el que ya recolectaba madera y confundía sus
   tramos (corregido en el propio humo); y `RN-ECON-020` miraba sólo la distancia: con el tope de nodos del A\*
   (20 000) una vuelta de 165 tiles no tiene ruta aunque quepa en los 256 tiles, y un aldeano acercado a mano habría
   cargado y perdido la carga. La orden calcula ahora la ruta de vuelta (`TestLaVueltaAlDepositoTieneQueTenerRuta`,
   precisión en la spec §4.2). Medido sobre el mapa canónico, con el tope actual sólo el 80 % de la tierra llega a su
   colina (el 98 % con 50 000 nodos).
9. **HECHO** — Producto decidió subir el tope del A\* a 50 000 nodos ([ADR-031](../decisions/ADR-031-pathfinding-node-cap-50k.md)):
   el valor por defecto de `EO_PATHFINDING_MAX_NODES`, el respaldo de `NewAStar`, `.env.example` y los docs que lo
   citan. Con el servidor en su configuración por defecto, `scripts/smoke.mjs --piedra` pasa con `smoke_runner`
   (19 comprobaciones): la colina a 163 tiles, la carga y el depósito (piedra 20 → 30), donde antes se rechazaba.

## E10 — Comercio

**Estado: HECHO (2026-11-04)**, fusionado en `main` (`84e6f67`) con la CI en verde el mismo día. Contrato: [SPEC-TRADE](../specs/trade.md),
aceptada el 2026-11-04 con sus diez decisiones de producto, y
[ADR-032](../decisions/ADR-032-trade-offers-and-two-key-jobs.md).

**Objetivo.** Que la geografía de E9 se pueda negociar: un trato «doy esto, pido aquello» entre dos ciudades con un
tratado vigente, que mueve los dos almacenes en el acto.

**Alcance.** El tratado `TRADE` como primer escalón de la diplomacia; `trade.propose`, `trade.respond` y
`trade.cancel`; la caducidad de las ofertas y su cierre al terminar el tratado; el trato aceptado en una transacción
que escribe los dos almacenes; el trabajo de la cola con dos claves; `trade.update` y `tradeOffers[]`; los tratos en el
registro; `INV-TRADE-001` a `004`; la migración `000013`; el cliente y `scripts/smoke.mjs --comercio`. **No entra**
(spec §2): caravanas, mercados, saquear piedra, que terceros vean los tratos, avisos en vivo.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-032, la familia `INV-TRADE`, y las precisiones de `INV-DIPLO-002` (la escalera)
   e `INV-ECON-002` (el trato).
2. **HECHO** — Cola: `Job.Key2`; el trabajo de dos claves entra en las dos particiones o en ninguna y se ejecuta cuando
   llegan los dos workers (`TestUnTrabajoDeDosClavesEsperaALasDosParticiones`, `TestDosTratosCruzadosNoSeBloquean`,
   `TestUnTrabajoDeDosClavesQueNoCabeNoEntraEnNinguna`).
3. **HECHO** — Dominio: la escalera en `diplomacy` (`Rank`, `Supersedes`, `AllowsTradeAt`; `TRADE` proponible) y el
   paquete `trade` con la oferta, sus líneas, quién puede cerrarla y las patas que conservan cada recurso.
4. **HECHO** — Migración `000013` y persistencia: `trade_offers`, `trade_offer_lines`, `postgres.TradeRepo` (la oferta
   con su evento, idempotente; el trato con los deltas de las dos ciudades en una transacción) e integración en
   `trade_integration_test.go` (seis tests).
5. **HECHO** — Simulación: `trade.go` con las órdenes en dos fases, la reserva de los dos almacenes (también la versión
   del recurso que sólo se recibe y los depósitos sin volcar), el crédito al confirmarse, la caducidad en el barrido de
   los tratados y la carga al arrancar; quince tests en `trade_test.go`, con el golden «comercio» y los de
   recuperación. Nueve mutaciones, todas detectadas por su test.
6. **HECHO** — Protocolo y WebSocket: los tres comandos (22 cliente→servidor), `trade.update` (22 servidor→cliente),
   `tradeOffers[]`, `ResourceAmount` con su cota y `TRADE` en `treaty.propose`; contract tests
   (`trade_contract_test.go`) y e2e (`trade_e2e_test.go`), con `trade.update` validado contra el esquema.
7. **HECHO** — Registro: `TradeProposed`, `TradeAccepted`, `TradeRejected`, `TradeWithdrawn` y `TradeExpired` en
   `eventlog.EventTypes`; cinco tipos de entrada con `tradeGive` y `tradeGet` vistos desde quien lee, sin agrupar.
8. **HECHO** — Cliente: `src/state/trade.ts` (lista, órdenes, texto, motivo del botón), la escalera de botones
   (`proposableTypes`), el formulario «Doy … y pido …» y la lista de ofertas en la barra de diplomacia, y los textos del
   registro; tests en `trade.test.ts`, `diplomacy.test.ts` y `eventLog.test.ts`.
9. **HECHO** — Humo y prueba en vivo (2026-11-04). El migrador llevó la base de desarrollo de 12 a 13 y
   `EO_DEVSEED_STOCK_FOR=smoke_victim pnpm run dev:seed` dejó 400 de madera y 200 de piedra a `smoke_victim`.
   `scripts/smoke.mjs --comercio` con `smoke_runner`, 22 comprobaciones en verde: el acuerdo comercial propuesto y
   aceptado (TRADE no da paz), la oferta de más madera de la que hay rechazada con `INSUFFICIENT_RESOURCES`, 50 de madera
   por 10 de piedra visto desde cada lado, la aceptación con el almacén de `smoke_victim` 400 → 450 y 200 → 190, y el
   registro de `smoke_runner` con `TRADE_ACCEPTED` y sus cantidades. En la base, la oferta `ACCEPTED` con sus líneas y
   `TradeProposed` y `TradeAccepted`. (La piedra de `smoke_runner` subió 20 y no 10: al arrancar, el servidor cerró con
   `OWNER_ABSENT` una tarea de piedra de una pasada de E9 y la unidad descargó 10; la madera, igual.) En el navegador, con
   `smoke_runner`: la barra de diplomacia mostraba el acuerdo vigente, la oferta de tratado de `dev_norte` y la de
   comercio «smoke_victimpolis te ofrece 20 de piedra por 30 de madera (caduca en 24 h)»; al aceptarla, Madera
   1400 → 1370 y Piedra 50 → 70 en el HUD y la oferta fuera de la barra; «Mientras no estabas» contaba los tratos con sus
   cantidades. **No se comprobó en el navegador** el formulario «Ofrecer trato» sobre una ciudad ajena: con el panel
   oculto no hay frames y el clic se convierte en tile con la cámara del último frame, así que no se puede llegar a otra
   ciudad; lo cubren los tests de `trade.ts` y el typecheck. `scripts/smoke.mjs --diplomacia` sobre el acuerdo comercial
   vigente (21 comprobaciones): el pacto se propone encima, sustituye al TRADE (`EXPIRED`) en la misma transacción,
   impide el saqueo y se rompe con su preaviso.
10. **HECHO** — Fallo encontrado: `scripts/smoke.mjs --diplomacia` daba por pacto cualquier tratado vigente y, con el
    acuerdo comercial que deja `--comercio`, habría comprobado que un TRADE impide el saqueo, que no lo impide. Elige
    ahora sólo un pacto o una alianza (`pactoVigente`, con su test en `scripts/smoke.test.mjs`, que falla con la lógica
    anterior) y, con un TRADE vigente, propone el pacto como subida de escalón y comprueba la sustitución.

## E11 — Avisos

**Estado: HECHO (2026-11-04)**, fusionado en `main` (`d2f7d88`) con la CI en verde el mismo día. Contrato:
[SPEC-LIVE-NOTICES](../specs/live-notices.md), aceptada el 2026-11-04 con sus ocho decisiones de producto, y
[ADR-033](../decisions/ADR-033-live-notices-ephemeral.md).

**Objetivo.** Que el riesgo lejos de casa se note cuando ocurre: si matan a tu aldeano en una colina a 160 tiles, te
enteras en el acto, aunque estés mirando tu ciudad.

**Alcance.** `event_log.notice`, un aviso efímero sin `id` que la simulación manda en la fase 4 al jugador del suceso:
tus unidades muertas (`UNIT_LOST`) y lo que las tuyas matan (`ENEMY_KILLED`), saquean (`PILLAGE_DONE`) o derriban
(`HOUSE_DESTROYED`); la clasificación con la misma función que el registro (`eventlog.Notices`); la precisión de
`INV-SEC-009`; en el cliente, la franja de avisos del HUD agrupada, «Ir», el punto en «Registro» y el contador en el
título de la pestaña. **No entra** (spec §2): notificaciones del sistema, sonido, guardar o reenviar avisos, avisos a
terceros, los sucesos de E9 y lo que ya llega por otra vía (saqueo y asedio sufridos, tratados, tratos).

### Entregables

1. **HECHO** — Aceptación: spec, ADR-033 y las precisiones de `INV-SEC-009` (los avisos) y de la Q5 y `RN-LOG-007` de
   [event-log.md](../specs/event-log.md).
2. **HECHO** — Protocolo: `event_log.notice` en Zod (`EventLogNoticeKind`, subconjunto de `EventLogKind`) y en Go
   (`EventLogNoticePayload`), los dos JSON Schema y sus contract tests (23 tipos servidor → cliente):
   `TestElAvisoEsElDelEsquema` y el bloque «avisos en vivo» de `protocol.test.ts`.
3. **HECHO** — Dominio: `eventlog.Notices` y `eventlog.NoticeKinds`, que pasan el suceso por `Classify` para sus dos
   jugadores (`TestCadaSucesoAvisaASusDosLadosSegunElRegistro`, `TestSinOtroJugadorNoHayAviso`,
   `TestSoloSeAvisanLosCuatroTiposDeCombate`; **precisión (2026-11-23):** hoy
   `TestSoloSeAvisanLosTiposDeCombateYDeTerritorio`, desde que E12 sumó los avisos de territorio).
4. **HECHO** — Simulación: `notices.go` y el aviso en `kill`, `applyPillage` y `destroyHouse`, con la ciudad del otro
   jugador y el tile; cinco tests en `notices_test.go`: los dos lados de una muerte en el tick del despawn, un aviso
   por golpe de saqueo y ninguno al saqueado, la casa derribada, una muerte y una casa reencoladas que no vuelven a
   avisar, y un golpe que no se escribe y ya se avisó. Tres mutaciones (el aviso en `submitDeath`, en
   `submitDestruction` y sin el filtro de tipos), todas detectadas.
5. **HECHO** — WebSocket: `TestUnAvisoLlegaConLaVistaEnOtraParteDelMapa` (`notices_e2e_test.go`), en un mundo de 8 × 8
   chunks: el dueño, con la vista en su ciudad, recibe `UNIT_LOST` sin recibir el despawn; el asesino, `ENEMY_KILLED`;
   un testigo, el despawn y ningún aviso. Mandar el aviso al chunk lo pone en rojo.
6. **HECHO** — Cliente: `src/state/notices.ts` (agrupación, los 8 s, los tres visibles, el punto, el contador del
   título) con `notices.test.ts`, `notices` en el store sin tocar `eventLog` (`world.test.ts`), la franja
   `NoticeStrip` con «Ir» (prop `focus` de `GameCanvas`) y el punto en «Registro».
7. **HECHO** — Humo y prueba en vivo (2026-11-04). Sin migración nueva: la base de desarrollo sigue en la 13, y al
   arrancar el pacto 5 entre `smoke_runner` y `smoke_victim` pasó a `BROKEN`. `scripts/smoke.mjs --saqueo --asedio
   --registro` con `smoke_runner`, 40 comprobaciones en verde: una segunda sesión de `smoke_runner` con la vista en su
   ciudad (291,19), a ~160 tiles y fuera de su área de interés, recibió `PILLAGE_DONE` («5 de madera a
   smoke_victimpolis en (449, 181)») y `HOUSE_DESTROYED` («en (450, 178)»), y `smoke_victim` ninguno de los dos; el
   registro de cada lado los cuenta. Una segunda pasada con `--asedio`, 26 comprobaciones. En el navegador, con
   `smoke_runner` en su ciudad durante esa pasada, un observador del DOM vio llegar el aviso «Tus guerreros derribaron
   una casa de smoke_victimpolis» con «Ir», el punto en «Registro» y el título «(1) Empires Online» (el panel estaba
   oculto), y la franja vaciarse sola 8,1 s después; abrir el registro borró el punto y trajo las dos entradas
   avisadas. **No se comprobó en el navegador** que «Ir» mueva la cámara ni que el título se borre al volver a la
   pestaña: con el panel oculto no hay frames ni cambio de visibilidad que provocar. Lo cubren `notices.test.ts`,
   `world.test.ts` y el typecheck.

## E12 — Conquista

**Estado: HECHO (2026-11-04)**, fusionado en `main` (`a2c3378`) con la CI en verde el 2026-11-05. Contrato:
[SPEC-TERRITORY-CONQUEST](../specs/territory-conquest.md), aceptada el 2026-11-04 con sus once decisiones de
producto, y [ADR-034](../decisions/ADR-034-territory-claims.md).

**Objetivo.** Que el territorio sea un recurso disputado (pilar 5): ganarlo y perderlo con guerreros, y que poseerlo
dé algo.

**Alcance.** `unit.claim`: un guerrero reclama el territorio donde está y, si aguanta 5 minutos (libre) o 10 (con
dueño), el territorio pasa a su dueño; +5 de límite de población por territorio conquistado, sin contar la capital,
que es intocable; reclamar lo ajeno es hostil (paz y protección lo impiden); la reclamación se ve (`contested`, su
`claim` y `CLAIMING`), se rompe al matar al guerrero o darle otra orden, avisa al dueño y queda en el registro; los
guardias del dueño responden solos; `territory_claims` en la migración `000014`; `INV-TERR-010` e `INV-TERR-011`; el
cliente y `scripts/smoke.mjs --territorio`. **No entra** (spec §2): conquistar ciudades, restringir acciones por
territorio, perder territorio por abandono, tope de territorios, `control_points`.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-034, `INV-TERR-010` e `INV-TERR-011`, las precisiones de `INV-TERR-006`
   (`contested`), `INV-ECON-004` (el bono) y de `RN-TERR-009` y la Q-10 del backlog.
2. **HECHO** — Protocolo: `unit.claim` (23 tipos cliente → servidor), `claim` en `TerritoryView`, `CLAIMING`, los tres
   tipos del registro y de los avisos y `territoryId`/`territoryName`; contract tests en Go
   (`TestTerritoryUpdateSerializaExactamenteLaVistaDelContrato`, `TestCatalogoDeComandosEsCerrado`) y el bloque
   «conquista» de `protocol.test.ts`.
3. **HECHO** — Dominio: `territory.CheckClaimable`, `CountConquered`, `CaptureBonus` y la reclamación en
   `internal/domain/territory/claim.go`; `building.PopulationLimit` con el término de los territorios; `CLAIMING` en
   `unit`.
4. **HECHO** — Migración `000014` y persistencia: `postgres.ConquestRepo` (el comienzo idempotente, el fin, la captura de
   ADR-034 §3 y la carga) y el adaptador; la validación de carga recuenta el límite y repara `contested`; tres tests de
   integración en `conquest_integration_test.go`.
5. **HECHO** — Simulación: `conquest.go` con la orden en dos fases, el barrido de la fase 5, la captura con dos claves,
   la defensa automática, la cancelación y la recarga; once tests en `conquest_test.go`, con el golden «conquista».
   Tres mutaciones (la capital en el dominio y en la transacción, y otra orden que no rompe la reclamación), todas
   detectadas.
6. **HECHO** — Registro y avisos: `TerritoryClaimStarted` y `TerritoryControlChanged` en `eventlog`, con
   `TERRITORY_CLAIMED`, `TERRITORY_LOST` y `TERRITORY_TAKEN` para cada lado y sin agrupar; el e2e
   `TestUnaConquistaAvisaAlDuenoConLaVistaEnSuCiudad`.
7. **HECHO** — Cliente: `src/state/conquest.ts`, el botón «Reclamar territorio» con su motivo, la cuenta atrás, el
   territorio en disputa en ámbar, los textos del registro y de los avisos; `conquest.test.ts`, `eventLog.test.ts` y
   `notices.test.ts`.
8. **HECHO** — Humo y prueba en vivo (2026-11-04). Al arrancar, el servidor llevó la base de desarrollo de la migración 13
   a la 14 sin hallazgos en la validación de carga; se arrancó con `EO_TERRITORY_CLAIM_FREE_SECONDS=30` y
   `EO_TERRITORY_CLAIM_SECONDS=60`. `scripts/smoke.mjs --territorio` con `smoke_runner`, 26 comprobaciones: su guerrero 15
   reclama la Región 7-3, libre, que viaja en disputa con su plazo; a los 30 s es suya y su límite pasa de 55 a 60.
   `smoke_victim` se la reconquista con su guerrero 45: una segunda sesión de `smoke_runner` con la vista en su ciudad
   recibe `TERRITORY_CLAIMED` y, a los 60 s, `TERRITORY_LOST`; `smoke_victim`, `TERRITORY_TAKEN`; el límite vuelve a 55.
   Una pasada anterior con `--registro` conquistó la Región 6-2 (50 → 55) y pasó entera. En la base: cinco
   reclamaciones `COMPLETED`, ningún territorio en disputa, `smoke_runner` con un conquistado (límite 55) y `smoke_victim`
   con dos (60). En el navegador, con `smoke_runner`, llegaron «smoke_victimpolis reclamó tu territorio Región 6-3» con
   «Ir», el punto en «Registro» y el título «(1) Empires Online», y 60 s después «smoke_victimpolis te quitó el
   territorio Región 6-3», con la población de la cabecera de 60 a 55; «Mientras no estabas» contaba cada suceso de
   territorio por separado. **No se comprobó en el navegador** el botón «Reclamar territorio» ni el territorio en
   disputa en ámbar: con el panel oculto no se puede seleccionar una unidad haciendo clic en el mapa ni ver la escena;
   lo cubren `conquest.test.ts`, el typecheck y el humo.
9. **HECHO** — Fallo encontrado en el humo: en la reconquista, `smoke_victim` tomó como suya la reclamación anterior de
   `smoke_runner` sobre el mismo territorio, que seguía en su cola, y esperó la pérdida con un plazo vencido. Sólo vale
   ya la reclamación de quien reclama (`esReclamacionDe`, con su test en `scripts/smoke.test.mjs`, que falla con la
   lógica anterior); y si la víctima no tiene guerrero, lo entrena.

## E13 — Civilizaciones

**Estado: HECHO (2026-11-05)**, fusionado en `main` (`e3e9db1`) con la CI en verde el mismo día. Contrato:
[SPEC-CIVILIZATION](../specs/civilizations.md), aceptada el 2026-11-05 con sus diez decisiones de producto, y
[ADR-035](../decisions/ADR-035-civilization-traits.md).

**Objetivo.** Que el pilar 4 tenga su primera mecánica: la civilización se elige al nacer y cambia cómo se juega.

**Alcance.** La civilización se elige en el registro, obligatoria y sin preselección, se guarda en la cuenta pendiente
y no cambia nunca; los jugadores que ya existían siguen `ROMAN`. Los rasgos pasan a enteros por mil (migración
`000015`) y se aplican en cinco sistemas: velocidad de las unidades, duración de obras y entrenamiento, vida de las
casas, carga de botín y tiempo de recolección; el rasgo persa de comercio se sustituye por recolección. La
civilización viaja en `CityView` y se ve en el HUD y en el panel de una ciudad ajena; `INV-PLAYER-011` e
`INV-PLAYER-012`. **No entra** (spec §2): capacidades exclusivas, cambiar de civilización, elegir la facción, rasgos
sobre el combate, el avance de era o el comercio, el pipeline completo de modificadores.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-035, `INV-PLAYER-011` e `INV-PLAYER-012`, la precisión de `INV-PLAYER-002`, las de
   [player.md](../specs/player.md) y [movement.md](../specs/movement.md), y la Q-02 del backlog.
2. **HECHO** — Protocolo: `CivilizationCode`, `CityView.civilization` (obligatoria; no va en `city.update`) y el
   catálogo de presentación `CIVILIZATIONS`, exportado a `civilizations.json` y embebido en Go; contract tests en los
   dos lados (`TestLaCiudadLlevaUnaCivilizacionDelCatalogo`).
3. **HECHO** — Dominio: `Civilization.Traits` en enteros, `Trait`, `Validate`, `ScaleDuration` y `ScaleAmount`
   (`TestLasFormulasDeLosRasgosDanLosValoresDeLaSpec`).
4. **HECHO** — Migración `000015` y persistencia: los rasgos por mil, el trigger `players_civilization_immutable`, la
   ciudad con el código de su civilización, la fundación con `civilizationCode` (sin el parámetro de arranque) y la
   carga que rechaza un catálogo inválido; cinco tests de integración nuevos.
5. **HECHO** — Simulación: los cinco puntos de consumo (`civilization.go`), la vida de la casa leída con la
   civilización del dueño, la validación de carga; los golden por rasgo, verificados por mutación uno a uno, y un
   reinicio que no recalcula los plazos fijados.
6. **HECHO** — Web: el registro con `civilization` (validada en `AuthService`), la migración 3 de identidad
   (`accounts.civilization`), el reintento de la fundación con la de la cuenta (verificado por mutación), un
   `INVALID_MESSAGE` de la fundación como rechazo definitivo y la semilla de identidad.
7. **HECHO** — Cliente: `src/state/civilizations.ts`, la elección en el formulario con sus porcentajes y sin ninguna
   preseleccionada, la civilización en el HUD y en la barra de una ciudad ajena (frontend.md §4.10).
8. **HECHO** — Semilla, humo y prueba en vivo (2026-11-05): `cmd/devseed` reparte las cuatro civilizaciones en una
   base nueva. Contra la base de desarrollo, el servidor aplicó `000015` al arrancar y la validación de carga aceptó
   las diez ciudades (todas `ROMAN`). `node scripts/smoke.mjs --web http://localhost:3001 --civilizacion` pasó sus 22
   comprobaciones: la ciudad de smoke_runner lleva `ROMAN` y su polilínea va a 600 ms por tile; un alta sin
   civilización y otra con `AZTEC` se rechazan con 400; `smoke_norse` se dio de alta `NORSE` y su aldeano anda a 545 ms
   por tile; y una casa romana se anunció con su plazo de 13 636 ms. En el navegador integrado, el formulario enseña
   las cuatro con sus porcentajes y «Fundar imperio» no se puede pulsar hasta elegir; el HUD dice «smoke_runnerpolis —
   Romanos — Edad de Bronce», y la barra de dev_nortepolis, «dev_nortepolis (Romanos, Edad de Piedra)», con el
   formulario «Ofrecer trato» de E10 debajo.

## E14 — Bienes

**Estado: HECHO (2026-11-10)**, fusionado en `main` (`da11ac2`) con la CI en verde el mismo día. Contrato:
[SPEC-CIVILIZATION-GOODS](../specs/civilization-goods.md), aceptada el 2026-11-06 con sus doce decisiones de producto,
y [ADR-036](../decisions/ADR-036-civilization-goods.md).

**Objetivo.** Que ningún jugador sea autosuficiente (pilar 4): cada civilización produce algo que las demás necesitan.

**Alcance.** Un bien por civilización (vino, seda, especias, pieles) como recurso del almacén y del comercio; el taller,
que pide Bronce y es único por ciudad; `city.craft`, que elabora 5 del bien propio por 30 de madera y 15 de piedra en
30 s, sin bloquear el entrenamiento; el avance de Bronce a Hierro con 800 de madera, 400 de piedra y 60 bienes ajenos;
los rasgos de producción de E13 sobre el taller y la elaboración; `INV-ECON-009` a `011`; migración `000016`; el
cliente y `scripts/smoke.mjs --bienes`. **No entra** (spec §2): unidades o tecnologías exclusivas, la Edad de los
Castillos, otros usos de los bienes, saquearlos, la producción pasiva.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-036, `INV-ECON-009` a `011`, las precisiones de [economy-stone.md](../specs/economy-stone.md)
   Q6, [trade.md](../specs/trade.md) Q4, [economy-wood.md](../specs/economy-wood.md) §4.5 y
   [civilizations.md](../specs/civilizations.md) Q6, y la `Q-12` del backlog.
2. **HECHO** — Protocolo: `city.craft` (24 tipos cliente → servidor), `craft` en `CityView` y `city.update`,
   `WORKSHOP`, los cuatro recursos en `Stocks` y en las líneas de un trato, `good` en el catálogo de presentación;
   contract tests en los dos lados.
3. **HECHO** — Dominio: los recursos y `economy.Goods()`, `Civilization.Good` y `ValidateCatalog`, el taller con costes
   en varios recursos, `RequiresEra` y `UniquePerCity`, `internal/domain/craft`, `advance.Resolve` y el paso a Hierro.
4. **HECHO** — Migración `000016` y persistencia: los `CHECK`, `civilizations.good`, el único parcial del taller,
   `city_crafts`, el encargo y la entrega idempotentes, el derribo que borra la elaboración; seis tests de
   integración nuevos.
5. **HECHO** — Simulación: la orden en dos fases, la entrega optimista con su reintento a versión nueva, el taller
   único también contra los encargos en vuelo, el derribo, el avance con los bienes resueltos; los golden «bienes»,
   verificados por mutación, y un e2e de elaborar por la red. Fallo encontrado: el rasgo `building.house_hp` se
   aplicaba a cualquier edificio y un taller bizantino salía con 240 de vida; ahora sólo a las casas.
6. **HECHO** — Cliente: «Taller», «Elaborar» con su cuenta atrás, los bienes en el HUD, el formulario de trato con los
   seis recursos y el avance a Hierro con sus bienes (frontend.md §4.11).
7. **HECHO** — Humo y prueba en vivo (2026-11-10): el servidor aplicó `000016` al arrancar y la validación de carga
   aceptó las once ciudades. `node scripts/smoke.mjs --web http://localhost:3001 --comercio --bienes` pasó sus 31
   comprobaciones: smoke_runner (romana, en Bronce) encargó el taller con su plazo de 40 909 ms, elaboró 5 de vino con
   el de 27 273, el avance a Hierro sin bienes ajenos se rechazó, y con el acuerdo comercial que firmó `--comercio`
   cambió 5 de vino por 1 de madera a smoke_victim. En el navegador integrado, el HUD enseña «Vino», «Taller»
   deshabilitado porque ya hay uno, «Elaborar (5 de vino)» y «Edad de Hierro (0/60 bienes)» con lo que falta; pulsar
   «Elaborar» puso la cuenta atrás y, al vencer, el vino subió a 5.


## E15 — Caravanas

**Estado: HECHO (2026-11-13)**, fusionado en `main` (`38faac9`) con la CI en verde el mismo día. Contrato:
[SPEC-CARAVANS](../specs/caravans.md), aceptada el 2026-11-13 con sus doce decisiones de producto, y
[ADR-037](../decisions/ADR-037-caravans-as-units.md).

**Objetivo.** Que el comercio recorra el mapa y se pueda interceptar: la interdependencia del pilar 4 con la distancia
del pilar 5 y el riesgo del camino.

**Alcance.** Cada trato aceptado sale en dos caravanas, una por sentido, desde el depósito de cada ciudad; el tipo de
unidad `CARAVAN` (80 de vida, 1 200 ms por tile, sin población ni órdenes); la ruta buscada al proponer y al aceptar,
con `PATH_NOT_FOUND` y `PATH_TOO_LONG` si no la hay; la entrega optimista al llegar; la captura con el golpe mortal,
que la lleva con su carga entera a la ciudad del captor, y la recaptura; la protección del dueño no la cubre;
`caravan.update`, `caravans[]` y los cuatro tipos de caravana del registro, tres de ellos avisados; `INV-TRADE-005` a `007`;
migración `000017`; el cliente y `scripts/smoke.mjs --caravana`. **No entra** (spec §2): rutas permanentes, envíos sin
trato, mercados, órdenes de escolta o de dirigir la caravana, varias caravanas por sentido, caravanas por agua, el A\*
jerárquico.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-037, `INV-TRADE-005` a `007`, las precisiones de `INV-TRADE-001`,
   `INV-COMBAT-002` e `INV-CITY-017`, de [trade.md](../specs/trade.md) (`RN-TRADE-004`, Q7 y Q8),
   [combat-raid.md](../specs/combat-raid.md) (`RN-COMBAT-003`), [civilization-goods.md](../specs/civilization-goods.md)
   (`RN-GOODS-008` y Q9) y [economy-stone.md](../specs/economy-stone.md) (Q8), y la nota de `DEBT-04` en el backlog.
2. **HECHO** — Protocolo: `CARAVAN` en `UnitType` (no en `city.train`: `TrainableUnitType`), `caravan.update` (24
   tipos servidor → cliente) con su e2e (`TestUnaCaravanaDePuntaAPunta`), `caravans[]` en `world.snapshot`, los tipos
   `CARAVAN_ARRIVED`, `CARAVAN_CAPTURED`, `CARAVAN_INTERCEPTED` y `CARAVAN_LOST` en el registro (los tres últimos en
   `event_log.notice`) y `cargo` en los dos; contract tests en los dos lados.
3. **HECHO** — Dominio: `CARAVAN` en el catálogo de unidades (`Autonomous`, `EnsureOrderable`), `internal/domain/caravan`
   (las dos mitades de un trato, la carga, la ruta inversa, quién ve la carga), las caravanas en `eventlog`, la
   población sin caravanas en la validación de carga y la excepción de la protección en el predicado hostil.
4. **HECHO** — Migración `000017` y persistencia: `caravans` y `caravan_cargo`; `TradeRepo.Accept` debita y crea las
   dos caravanas con sus unidades y movimientos; `CaravanRepo` con la entrega idempotente, la captura y la pérdida, la
   carga al arrancar y la comprobación del arranque (`Violations`); seis tests de integración nuevos y los del trato
   reescritos.
5. **HECHO** — Simulación: la ruta en la propuesta y la aceptación (una búsqueda, la vuelta al revés), las caravanas al
   volver la confirmación, la llegada en la fase 5, la captura en la fase 4 con la escolta de E3, la pérdida sin ruta,
   el reintento de una entrega descartada y la recuperación; el golden «caravanas» y once tests más. Fallo encontrado:
   el aviso de una captura salía sin su carga (`sendNotices` no la copiaba).
6. **HECHO** — Cliente: el carro en la escena, «Caravanas en camino» con su llegada e «Ir», ninguna orden con una
   caravana seleccionada, los textos de las cuatro entradas y de los avisos, y los mensajes de `PATH_NOT_FOUND` y
   `UNIT_NOT_MOVABLE` (frontend.md §4.12).
7. **HECHO** — Humo y prueba en vivo (2026-11-13): el servidor aplicó `000017` al arrancar. `node scripts/smoke.mjs
   --web http://localhost:3001 --comercio --bienes` pasó: el trato de 50 de madera por 10 de piedra sólo debitó, y las
   dos caravanas llegaron unos 4 min 33 s después, a 55 ms de lo anunciado, con `CARAVAN_ARRIVED` en el registro de
   smoke_victim; el vino de `--bienes` llegó igual. `--caravana` pasó sus 23 comprobaciones: cuatro guerreros de
   smoke_runner capturaron la caravana de piedra de smoke_victim junto a su ciudad, la víctima recibió
   `CARAVAN_CAPTURED` con su carga y la caravana llegó a smoke_runnerpolis. En el navegador integrado, smoke_victim vio
   «Caravanas en camino» con las dos caravanas desde su lado y el carro en el mapa con su ruta, y «Mientras no estabas»
   contó la captura en rojo y las llegadas. Dos fallos del humo, no del juego: leía el registro antes de que la entrega
   optimista se confirmara (ahora espera la entrada hasta 5 s, y la spec documenta esa ventana) y mandaba
   `targetId` en lugar de `targetUnitId`.

## E16 — Mensajes

**Estado: HECHO (2026-11-14)**, fusionado en `main` (`04eed8a`) con la CI en verde el mismo día. Contrato:
[SPEC-MESSAGES](../specs/messages.md), aceptada el 2026-11-14 con sus nueve decisiones de producto, y
[ADR-038](../decisions/ADR-038-player-messages-session-subsystem.md).

**Objetivo.** Que los jugadores puedan negociar la diplomacia y el comercio hablando: la comunicación que pedía
`Q-16` para que la interdependencia del pilar 4 sea jugable.

**Alcance.** Mensajes de texto plano de hasta 500 caracteres entre dos jugadores, sin tratado previo, con el otro
nombrado por su ciudad; guardados antes de entregarse y entregados en vivo a todas las sesiones de los dos, o en la
bandeja al volver; la bandeja, el historial de 30 días paginado y lo leído por conversación; bloquear en los dos
sentidos; 10 mensajes por minuto y 10 conversaciones nuevas por hora, con `RATE_LIMITED` sin cierre; cinco comandos
de sesión y `message.new`, `message.inbox` y `message.page`, fuera del tick; `INV-SEC-010`, `INV-SEC-011` e
`INV-PERSIST-007`; migración `000018`; el cliente y `scripts/smoke.mjs --mensajes`. **No entra** (spec §2): canales,
grupos, clanes, denuncias, moderadores y filtros, editar o borrar, adjuntos y formato, un directorio de jugadores, el
«visto», el borrado de los mensajes viejos y la entrega entre varios procesos.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-038, `INV-SEC-010`, `INV-SEC-011` e `INV-PERSIST-007`, la precisión de
   [websocket-protocol.md](../specs/websocket-protocol.md) §4.2, la de `OUT-09` en
   [mvp-scope.md](../product/mvp-scope.md) y `Q-16` en parte respondida en el backlog.
2. **HECHO** — Protocolo: los cinco comandos (29 cliente → servidor) y los tres tipos (27 servidor → cliente), cada
   uno con su e2e; contract tests en los dos lados.
3. **HECHO** — Dominio: `internal/domain/message` (validación del texto, la pareja canónica, la vista previa y los
   límites de ritmo).
4. **HECHO** — Migración `000018` y persistencia: `player_messages`, `message_reads` y `player_blocks`, el envío
   con el candado por pareja (verificado por mutación), la unicidad del `requestId`, la bandeja y lo leído.
5. **HECHO** — Conexión: los comandos como mensajes de sesión, la entrega tras el `COMMIT`, el tope de lo leído
   por conversación, el reintento que no choca con el ritmo y las métricas.
6. **HECHO** — Cliente: el botón «Mensajes» con su contador, la bandeja, la conversación, «Escribir» desde la
   barra de diplomacia y las demás listas, el aviso, la línea de «Mientras no estabas» y el bloqueo.
7. **HECHO** — Humo y prueba en vivo (2026-11-14): `scripts/smoke.mjs --mensajes` pasó entero contra el servidor con
   la migración `000018` recién aplicada (23 comprobaciones: la entrega en vivo con smoke_runner nombrado por su
   ciudad, el no leído que espera con smoke_victim desconectada y desaparece al leer, el bloqueo en los dos sentidos y
   la vuelta al desbloquear). En el navegador integrado, como smoke_victim: «Mientras no estabas» con la línea de
   mensajes y «Leer», la bandeja y la conversación, el contador a cero al leer, una respuesta con `<b>` pintada como
   texto, «Bloquear» y «Desbloquear», «Escribir» desde el tratado y desde el registro, y el aviso «… te ha escrito» con
   «Mensajes (2)». Los logs del servidor no llevan el texto. Un fallo del cliente, corregido: «Cerrar» dejaba la
   conversación abierta en el store y lo que llegaba después no contaba como no leído ni avisaba. Sin comprobar en el
   navegador: «Escribir» desde la barra de una ciudad ajena tras un clic en el mapa (es el mismo `writeTo` que el del
   tratado) y el contador del título con la pestaña oculta (cubierto por los tests de `notices` y `messages`).

## E17 — Facciones

**Estado: HECHO (2026-11-15)**, fusionado en `main` (`21b3dbe`) con la CI en verde el mismo día. Contrato:
[SPEC-FACTIONS](../specs/factions.md), aceptada el 2026-11-15 con sus diez decisiones de producto, y
[ADR-039](../decisions/ADR-039-faction-peace-and-affinity.md).

**Objetivo.** Que exista el segundo eje de la visión: el bando decide a quién se puede agredir y con quién firmar, y
la tierra del propio bando protege a los suyos. Responde a `Q-03`.

**Alcance.** Todos nacen `NEUTRAL` y pueden unirse una sola vez, en partida, a `ORDER` o a `CHAOS` con
`faction.join`, con un preaviso de 15 minutos visible para todos; la paz de la Orden (dos de `ORDER` no se agreden);
entre `ORDER` y `CHAOS`, comercio sí y pacto o alianza no, y los vigentes empiezan su preaviso al entrar; la tierra
afín (la propia y, en un bando, la de todo el bando) reduce un 20 % el daño que reciben las unidades; la facción y la
entrada pendiente en `CityView`, y el bando del dueño en `TerritoryView`; `INV-PLAYER-013`, `INV-COMBAT-006` e
`INV-DIPLO-003`; migración `000019`; el cliente y `scripts/smoke.mjs --faccion`. **No entra** (spec §2): elegir el
bando en el registro, cambiar de bando o volver a `NEUTRAL`, objetivos globales y recompensas por bando, territorios
de facción, reputación, canales por bando y bonos que no sean el daño en tierra afín.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-039, `INV-PLAYER-013`, `INV-COMBAT-006` e `INV-DIPLO-003`, la precisión de
   `INV-PLAYER-002`, las de [player.md](../specs/player.md) (`RN-PLAYER-013` y su no-scope),
   [civilizations.md](../specs/civilizations.md) (Q10), [diplomacy.md](../specs/diplomacy.md) (`RN-DIPLO-001`) y
   [combat-raid.md](../specs/combat-raid.md) (`RN-COMBAT-006`), y `Q-03` respondida en el backlog.
2. **HECHO** — Protocolo: `faction.join` (30 cliente → servidor), `faction` y `factionJoin` en `CityView` y
   `ownerFaction` en `TerritoryView`; contract tests en los dos lados.
3. **HECHO** — Dominio y simulación: la facción en la ciudad, `AtPeace` con la Orden, los tratados entre bandos,
   la entrada en dos fases y su vencimiento, el daño en tierra afín.
4. **HECHO** — Migración `000019` y persistencia: las columnas de la entrada, el trigger de transición, la carga,
   la orden y el vencimiento con las rupturas en una transacción.
5. **HECHO** — Cliente: el bando en el HUD y en el panel ajeno, «Elegir bando», la cuenta atrás, la barra sin las
   órdenes que el bando impide, el texto del territorio afín y los rechazos con su motivo.
6. **HECHO** — Humo y prueba en vivo (2026-11-15): el servidor aplicó la migración `000019` al arrancar y la validación
   de carga aceptó el mundo de desarrollo (todos `NEUTRAL`). `scripts/smoke.mjs --faccion`, con el servidor arrancado
   con `EO_FACTION_JOIN_SECONDS=20`, pasó entero (28 comprobaciones: smoke_orden y smoke_caos, cuentas nuevas, piden
   entrar en la Orden y en el Caos, la entrada vuelve con su `requestId` y cuenta a los 20 s, una segunda se rechaza, un
   pacto y una alianza entre ellas se rechazan con «la Orden y el Caos no firman la paz» y un trato de comercio se
   propone y se retira); una segunda pasada, con las cuentas ya en su bando, pasó también (26). La base guarda las dos
   facciones sin entrada pendiente, `world_events` tiene dos `FactionJoinOrdered` y dos `FactionJoined`, y
   `eo_faction_joins_total` vale 1 en cada bando. smoke_runner y smoke_victim siguen `NEUTRAL`. En el navegador
   integrado: como smoke_orden, la cabecera dice «… — Edad de Piedra — Orden» sin «Elegir bando»; como smoke_victim,
   «… — Neutral» con «Elegir bando», y la ventana muestra los dos bandos con lo que da y cuesta cada uno y «Unirme a la
   Orden para siempre»; «Cancelar» la cierra sin mandar nada. Sin comprobar en el navegador: la cuenta atrás de una
   entrada pendiente, la barra de una ciudad ajena con su bando (las ciudades de los dos bandos quedaron a 124 tiles),
   el clic de ataque entre dos de la Orden y la línea de tierra afín del territorio; los cubren los tests de
   `factions.test.ts`, el e2e y la simulación.

## E18 — Clanes

**Estado: HECHO (2026-11-15)**, fusionado en `main` (`b228fdf`) con la CI en verde el mismo día. Contrato:
[SPEC-CLANS](../specs/clans.md), aceptada el 2026-11-15 con sus doce decisiones de producto, y
[ADR-040](../decisions/ADR-040-clans-membership-and-peace.md).

**Objetivo.** Que exista un nivel social entre el jugador solo y el bando: un grupo pequeño y elegido que se protege,
se coordina y tiene un nombre que los demás reconocen en el mapa. Abre la fase Clans del roadmap y responde en parte a
`Q-16` (los canales de clan).

**Alcance.** Fundar un clan con nombre y sigla únicos; invitar, aceptar, rechazar, retirar, salir y expulsar; un líder
y hasta 20 miembros, con el liderazgo que pasa al más antiguo y la disolución cuando sale el último; la paz del clan
(dos miembros no se agreden) con 15 minutos de paz residual al salir o ser expulsado; ningún clan con la Orden y el
Caos a la vez; la sigla en `CityView` y la lista pública de miembros; el canal del clan con las reglas de E16, leído
desde la entrada; la expulsión en el registro y en los avisos; `INV-CLAN-001`, `INV-CLAN-002`, `INV-CLAN-003` e
`INV-SEC-012`; migración `000020`; el cliente y `scripts/smoke.mjs --clan`. **No entra** (spec §2): territorios y
tierra afín de clan, guarnición o comercio sin tratado, oficiales y traspaso explícito, solicitudes y clanes abiertos,
renombrar, bando propio del clan, guerras y alianzas entre clanes, ranking y directorio de clanes.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-040, la familia `INV-CLAN` (`INV-CLAN-001` a `003`) e `INV-SEC-012`, las
   precisiones de [combat-raid.md](../specs/combat-raid.md) (`RN-COMBAT-006`), [factions.md](../specs/factions.md)
   (`RN-FAC-005`), [messages.md](../specs/messages.md) (§2), [mvp-scope.md](../product/mvp-scope.md) (`OUT-13`) y
   [schema.md](../database/schema.md) (§17.3), y `Q-16` en el backlog.
2. **HECHO** — Protocolo: nueve comandos (39 cliente → servidor) y cuatro tipos (31 servidor → cliente), `clan` en
   `CityView`; contract tests en los dos lados y un e2e por tipo nuevo.
3. **HECHO** — Dominio y simulación: el clan, `clanState`, los cinco comandos de la simulación en dos fases o
   optimistas, `AtPeace` con el clan y la paz residual, la regla de bandos también en `faction.join`, la expulsión en
   el registro y los avisos.
4. **HECHO** — Migración `000020` y persistencia: las cinco tablas, los índices, los dos triggers, la carga, los
   trabajos de pertenencia y el canal con su candado por clan.
5. **HECHO** — Cliente: el botón y el panel del clan, el canal, «Invitar al clan» y «Ver clan» en la barra de una
   ciudad ajena, la sigla en el mapa, el clic de ataque entre compañeros y los rechazos con su motivo.
6. **HECHO** — Humo y prueba en vivo: `scripts/smoke.mjs --clan` con cuentas de humo propias.

**Verificación (2026-11-15).** Todo en serie y en verde: los scripts de documentación, determinismo y secretos, sus
tests, `go build`, los dos `go vet`, `gofmt`, la suite de Go, las dos integraciones, el `typecheck`, los tests del
protocolo y del cliente, la deriva del esquema, y `web:build` con los canarios y el escaneo del bundle. Cuatro guards
verificados por mutación: la paz del clan en `AtPeace` (`INV-CLAN-003`), el trigger de bandos (`INV-CLAN-002`), y el
candado de la salida y el filtro de `first_message_id` del canal (`INV-SEC-012`). La mutación del candado destapó que
su test colgaba al fallar con la transacción abierta: ahora la suelta en un `t.Cleanup`.

**Prueba en vivo (2026-11-15).** El servidor aplicó `000020` sobre la base de desarrollo (de la migración 19 a la 20).
`scripts/smoke.mjs --web http://localhost:3001 --clan` pasó dos veces: la primera (31 comprobaciones) fundó el clan
«Humo» [HUM] con `smoke_clan_a`, con la sigla en su ciudad; `smoke_clan_b` recibió la invitación en vivo, entró, y el
mensaje del canal le llegó en vivo y en su página; `smoke_orden` entró en el clan, la invitación a `smoke_caos` se
rechazó («un clan no junta a la Orden y al Caos») y `smoke_caos` vio los tres miembros por la vista pública; la
expulsión de `smoke_clan_b` le llegó como aviso. La segunda (29) reconoció el clan de la pasada anterior y repitió
invitar, entrar, la regla de bandos, la vista pública y la expulsión. En el navegador, como `smoke_clan_a`:
«Mientras no estabas» con «Expulsaste a smoke_clan_bpolis de tu clan» (`CLAN_MEMBER_EXPELLED`), el botón «Clan
[HUM]», la etiqueta «[HUM] smoke_clan_apolis» en el mapa, el panel con los miembros (bando y rango), «Expulsar» y el
canal, y un envío desde el cuadro de texto que llega al canal con `<b>` pintado como texto. Sin comprobar en el
navegador: «Ver clan» e «Invitar al clan» en la barra de una ciudad ajena, el clic de ataque entre compañeros y el
contador del botón con mensajes sin leer (los cubren `clans.test.ts`, la proyección y el e2e). En la base de
desarrollo quedan el clan [HUM] con `smoke_clan_a` (líder) y `smoke_orden`, tres mensajes en su canal y
`smoke_clan_b` fuera, expulsado.

## E19 — Territorios de clan

**Estado: HECHO (2026-11-16)**, fusionado en `main` (`f21911a`) con la CI en verde el mismo día. Contrato:
[SPEC-CLAN-TERRITORIES](../specs/clan-territories.md), aceptada el 2026-11-16 con sus once decisiones de producto, y
[ADR-041](../decisions/ADR-041-clan-territories.md).

**Objetivo.** Que un clan tenga algo que defender juntos sobre el mapa: un miembro conquista un territorio para el
clan, el clan entero lo aprovecha como tierra afín y lo defiende. Activa el dueño `CLAN` que el esquema preveía desde
M6 y responde a la Q11 de [SPEC-CLANS](../specs/clans.md).

**Alcance.** `unit.claim` con `forClan`, con los plazos y la disputa de E12; el territorio del clan como tierra afín
para sus miembros abiertos, sin población; la hostilidad contra todos los miembros (la paz con cualquiera la impide, la
protección sólo si lo están todos); la defensa automática de los guerreros de cualquier miembro; los avisos y el
registro de cada miembro; el territorio que se queda en el clan al salir y queda libre al disolverse; la sigla del clan
dueño en el mapa y la lista de territorios en la vista del clan; `INV-TERR-012` e `INV-CLAN-004`; migración `000021`;
el cliente y `scripts/smoke.mjs --territorio-clan`. **No entra** (spec §2): ceder territorios entre un jugador y su
clan, soltarlos, un tope por clan, la tierra afín de compañero o de bando por el clan, bonos de población o de
recursos, almacén o edificios del clan, rangos, guerras y alianzas entre clanes, dueños `FACTION`.

### Entregables

1. **HECHO** — Aceptación: spec, ADR-041, `INV-TERR-012` e `INV-CLAN-004`, y las precisiones de
   [territory.md](../specs/territory.md) (§2, §6 y los estados), [territory-conquest.md](../specs/territory-conquest.md)
   (§2 y `RN-CONQ-002`, `003`, `005`, `006`, `009` y `010`), [factions.md](../specs/factions.md) (`RN-FAC-007`),
   [clans.md](../specs/clans.md) (Q11), [mvp-scope.md](../product/mvp-scope.md) (`OUT-13`) y
   [schema.md](../database/schema.md) (§17.3).
2. **HECHO** — Protocolo: `forClan` en `unit.claim`, `ownerClan` en `TerritoryView`, `clan` en
   `TerritoryClaimView`, `territories[]` (con el tile central) en las vistas del clan y `clanTag` en los avisos y el
   registro (sigue en 39 cliente → servidor y 31 servidor → cliente); contract tests en los dos lados y el e2e
   `TestUnTerritorioDeClanPorLaRed`.
3. **HECHO** — Dominio y simulación: el dueño de clan en el control y en `clanState`, la reclamación para el clan
   y su ruptura al dejar el clan, la hostilidad contra todos los miembros, la defensa de cualquier miembro, la captura
   con la clave del clan, la última salida con territorios en dos fases, la afinidad con el clan y los avisos a cada
   miembro.
4. **HECHO** — Migración `000021` y persistencia: `territory_claims.clan_id`, los dos triggers, la carga y su
   reparación, la captura y la disolución con sus eventos por miembro.
5. **HECHO** — Cliente: «Reclamar para el clan», la línea del territorio con su clan dueño, el mapa en azul (clan
   propio) o violeta (otro clan), la línea de tierra afín, «Territorios del clan» con «Ir» y los textos de los avisos y
   del registro.
6. **HECHO** — Humo y prueba en vivo: `scripts/smoke.mjs --territorio-clan` con cuentas de humo propias.

**Verificación (2026-11-16).** Todo en serie y en verde: los scripts de documentación, determinismo y secretos, sus
tests, `go build`, los dos `go vet`, `gofmt`, la suite de Go, las dos integraciones, el `typecheck`, los tests del
protocolo y del cliente, la deriva del esquema, y `web:build` con los canarios y el escaneo del bundle. Cinco guards
verificados por mutación: la ruptura de la reclamación al dejar el clan y la estancia abierta en la captura
(`INV-CLAN-004`), los dos triggers de `000021` (`INV-TERR-012`), la protección de todos los miembros
(`RN-CLANTERR-004`) y las filas del registro de un solo lado (ADR-041 §5). Al aplicar los triggers, un test de la
validación de carga que escribía a mano un dueño `CLAN` inexistente pasó a usar un clan real.

**Prueba en vivo (2026-11-16).** El servidor aplicó `000021` sobre la base de desarrollo (de la migración 20 a la 21),
con los plazos cortos de la conquista y, antes, 60 de madera para entrenar en las ciudades de `smoke_clan_a` y
`smoke_norse` (`EO_DEVSEED_STOCK_FOR`). `scripts/smoke.mjs --web http://localhost:3001` pasó tres veces, una por paso de
la disputa de la Región 6-4: con `--territorio-clan --clan` (37 comprobaciones), `smoke_clan_a` entrenó un guerrero,
reclamó para [HUM] (`claim.clan`) y a los 30 s el territorio fue del clan, con su `ownerClan` y en la lista del clan y
en su vista pública; con `--territorio-clan` (28), reclamar para un clan sin clan se rechazó («no estás en ningún
clan»), `smoke_norse` se lo quitó en 60 s y `smoke_clan_a`, con la vista en su ciudad, recibió `TERRITORY_CLAIMED` y
`TERRITORY_LOST` con [HUM], y `smoke_norse` un solo `TERRITORY_TAKEN`; y otra vez con `--territorio-clan` (26), [HUM]
lo recuperó, `smoke_norse` recibió su `TERRITORY_LOST` sin sigla y `smoke_clan_a` su `TERRITORY_TAKEN` con [HUM]. En
la base, una fila de evento por miembro abierto (`smoke_clan_a` y `smoke_orden`) con `only: player`, una sola para
quien lo ganó y las tres reclamaciones `CAPTURED` (dos con `clan_id`). Sin `invariant_violation` en el log. En el
navegador, como `smoke_clan_a`: «Mientras no estabas» con los tres textos nuevos («… reclamó Región 6-4, de tu clan
[HUM]», «… le quitó a tu clan [HUM] Región 6-4», «Tus guerreros ganaron Región 6-4 para tu clan [HUM], a …») y el
panel del clan con «Territorios del clan: Región 6-4» e «Ir», que lleva la cámara. Sin comprobar en el navegador (el
panel estaba oculto y sin `requestAnimationFrame`): el color azul o violeta del territorio en el mapa y «Reclamar para
el clan [HUM]» con un guerrero seleccionado (los cubren la proyección y `conquest.test.ts`). En la base de desarrollo
la Región 6-4 queda de [HUM]; la pasada siguiente de `--territorio-clan` es la de `smoke_norse`.

## E20 — Gobierno del clan

**Estado: HECHO (2026-11-16)**, fusionado en `main` (`983035e`) con la CI en verde el mismo día. Contrato:
[SPEC-CLAN-GOVERNANCE](../specs/clan-governance.md), aceptada el 2026-11-16 con sus diez decisiones de producto, sin
ADR nuevo.

**Objetivo.** Que un clan no dependa de una sola persona: oficiales que comparten el trabajo del líder, un traspaso
voluntario del liderazgo y una sucesión automática cuando el líder deja de jugar, para que un clan con territorios
(E19) no se quede congelado. Cierra la observación de E18 sobre el líder inactivo.

**Alcance.** Tres rangos (líder, oficial y miembro); los oficiales invitan, retiran invitaciones y expulsan a rasos;
`clan.promote` y `clan.transfer` (de 39 a 41 comandos); la herencia del oficial más antiguo al salir el líder; la
sucesión al oficial o miembro activo más antiguo cuando el líder lleva `EO_CLAN_LEADER_INACTIVE_SECONDS` (7 días) sin
conectarse, y el ausente queda como oficial; `ClanRole` con `OFFICER`; `ClanLeaderChanged` en el registro y en los
avisos (28 tipos); `INV-CLAN-005`; migración `000022` (`clan_members.rank` y su trigger); el cliente y
`scripts/smoke.mjs --clan` ampliado. **No entra** (spec §2): permisos configurables, votaciones, disolver con una
orden, expulsar inactivos, moderar el canal, restringir por rango la reclamación para el clan, alianzas y guerras entre
clanes.

### Entregables

1. **HECHO** — Aceptación: spec, `INV-CLAN-005`, y las precisiones de `INV-CLAN-001` y de
   [clans.md](../specs/clans.md) (Q4 y `RN-CLAN-007`).
2. **HECHO** — Protocolo: `clan.promote` y `clan.transfer`, `OFFICER` en `ClanRole` y el aviso
   `ClanLeaderChanged`; contract tests en los dos lados y su e2e.
3. **HECHO** — Dominio y simulación: los rangos en `clanRecord`, los permisos por rango, el traspaso optimista
   con su deshacer, la herencia del oficial y el barrido de la ausencia del líder.
4. **HECHO** — Migración `000022` y persistencia: `rank`, el trigger, la carga y la comprobación del rango en cada
   transacción con el candado del clan.
5. **HECHO** — Cliente: el rango en la lista, los botones según el rango, «Ceder el liderazgo» con su
   confirmación, «Invitar al clan» para un oficial y los textos del aviso y del registro.
6. **HECHO** — Humo y prueba en vivo: `scripts/smoke.mjs --clan` con el oficial y el traspaso de ida y vuelta.

**Verificación (2026-11-16).** Todo en serie y en verde:
- los scripts de documentación, determinismo y secretos, y sus tests;
- `go build`, los dos `go vet`, `gofmt` y la suite de Go;
- las dos integraciones;
- el `typecheck`, los tests del protocolo y del cliente, y la deriva del esquema;
- `web:build` con los canarios y el escaneo del bundle.

Verificado por mutación:
- el rango en las transacciones de invitar, expulsar, nombrar y ceder (`INV-CLAN-005`);
- el permiso del oficial;
- el plazo exacto de la ausencia;
- deshacer una expulsión y un traspaso que la base no admite;
- los dos triggers de `000022`.

La integración destapó que la sigla de una entrada del registro sólo se copiaba en las de un territorio; se corrigió
(precisión de la spec).

**Prueba en vivo (2026-11-16).**
- **Migración.** El servidor aplicó `000022` sobre la base de desarrollo (de la migración 21 a la 22).
- **Humo.** `scripts/smoke.mjs --web http://localhost:3001 --clan` pasó dos veces seguidas, con 33 comprobaciones
  cada vez:
  - `smoke_clan_a` nombra oficial a `smoke_orden`, y ella invita a `smoke_clan_b`, que entra;
  - `smoke_clan_a` le cede el liderazgo, con el aviso `CLAN_LEADERSHIP_RECEIVED` y [HUM], y `smoke_orden` se lo
    devuelve;
  - `smoke_orden`, de oficial, expulsa a `smoke_clan_b`, que recibe el aviso de su expulsión;
  - `smoke_orden` vuelve a miembro raso.
- **Base.** El clan [HUM] queda como estaba: `smoke_clan_a` de líder y `smoke_orden` de miembro, los dos con
  `rank = 'MEMBER'`. La primera pasada escribió dos `ClanLeaderChanged` (`TRANSFER`) y dos `ClanRankChanged`.
- **Log.** Sin `invariant_violation`.
- **Navegador**, como `smoke_clan_a`:
  - «Mientras no estabas» con «smoke_ordenpolis te cedió el liderazgo de [HUM]» y «Cediste el liderazgo de [HUM] a
    smoke_ordenpolis»;
  - el panel del clan con «Líder» y «Miembro», «Expulsar», «Nombrar oficial» y «Ceder el liderazgo», y la línea de la
    sucesión por ausencia;
  - «Nombrar oficial» la pasa a «Oficial» con «Quitar oficial», y «Quitar oficial» la devuelve;
  - «Ceder el liderazgo» pide la confirmación («smoke_ordenpolis pasará a liderar [HUM] y tú quedarás como oficial»),
    que se canceló.
- **Sin probar en vivo.** La sucesión por ausencia pediría tener al líder desconectado durante el plazo; la cubren el
  golden y la recuperación.

## E21 — Alianzas entre clanes

**Estado: HECHO (2026-11-16)**, fusionado en `main` (`57690c9`) con la CI en verde el mismo día. Contrato:
[SPEC-CLAN-ALLIANCES](../specs/clan-alliances.md), aceptada el 2026-11-16 con sus once decisiones de producto, y
[ADR-042](../decisions/ADR-042-clan-alliances.md).

**Objetivo.** Que dos clanes puedan ir juntos sin fundirse en uno: una alianza que firman sus líderes y que pone en paz
a todos sus miembros, con el preaviso de un tratado para que no sirva para traicionar. Es la diplomacia de los grupos,
sobre la misma `AtPeace` que los tratados, la Orden y el clan.

**Alcance.** `clan.alliance.propose`, `clan.alliance.respond` y `clan.alliance.cancel` (de 41 a 44 comandos), sólo
para el líder; el ciclo de vida y los plazos de un tratado; como mucho `EO_CLAN_MAX_ALLIES` (2) alianzas activas por
clan; la paz entre los miembros de los dos clanes y la paz residual con los aliados al salir; la regla de bandos al
firmar y el preaviso cuando la mezcla aparece después; la expiración al disolverse un clan; `alliances` en
`clan.update` y en `clan.info`; los eventos `ClanAlliance*` en el registro y tres avisos nuevos (32 tipos);
`INV-CLAN-006` e `INV-CLAN-007`; migración `000023` (`clan_alliances` y sus triggers); el cliente y
`scripts/smoke.mjs --alianza-clan`. **No entra** (spec §2): guerras declaradas, guarnición, comercio, almacén, canal
o tierra afín compartidos, alianzas de más de dos clanes, un mapa político y que los oficiales firmen.

### Entregables

1. **HECHO** — Aceptación: spec, [ADR-042](../decisions/ADR-042-clan-alliances.md), `INV-CLAN-006` e
   `INV-CLAN-007`, y las precisiones de `RN-CLAN-005` y `RN-CLAN-008` ([clans.md](../specs/clans.md)) y de
   `RN-FAC-005` ([factions.md](../specs/factions.md)).
2. **HECHO** — Protocolo: los tres comandos, `alliances` en `ClanView` y en `clan.info`, y los avisos nuevos;
   contract tests en los dos lados y su e2e.
3. **HECHO** — Dominio y simulación: la alianza en `clanState`, las órdenes en dos fases, `AllyPeace` en
   `AtPeace` con la paz residual, los vencimientos, la mezcla de bandos al entrar en un bando o en un clan y la
   expiración al disolver.
4. **HECHO** — Migración `000023` y persistencia: `clan_alliances`, los triggers, la carga y cada orden con los dos
   candados.
5. **HECHO** — Cliente: la sección «Alianzas» del panel, «Proponer alianza» en la vista pública, «Aliado de [TAG]»
   en la barra de una ciudad ajena y los textos del registro y de los avisos.
6. **HECHO** — Humo y prueba en vivo: `scripts/smoke.mjs --alianza-clan`.

### Prueba en vivo (2026-11-16)

Contra la base de desarrollo, que el servidor llevó de la migración 22 a la 23 al arrancar:

- `node scripts/smoke.mjs --web http://localhost:3001 --clan --alianza-clan`: `--clan` entero, y la alianza hasta el
  ataque, que se rechazó con «sois del mismo clan»: la paz residual de la expulsión de smoke_clan_b, que `--clan` acaba
  de hacer, va antes que la de la alianza. El humo lo distingue desde entonces.
- Con el servidor rearrancado con `EO_CLAN_PEACE_GRACE_SECONDS=5`, la alianza que había quedado activa volvió con la
  carga, y `--alianza-clan` pasó entero (29 comprobaciones): smoke_clan_b sale de su [ECO], que se disuelve y expira la
  alianza; funda otro; la oferta y la aceptación con sus avisos y la sigla; la vista pública; el ataque rechazado con
  «vuestros clanes están aliados»; la ruptura con su aviso; la disolución que la expira; y la entrada del registro.
- En el navegador, como smoke_clan_a: «Mientras no estabas» con «smoke_clan_bpolis propone una alianza entre [ECO] y tu
  clan» y las aceptaciones; la sección «Alianzas» del panel con la oferta recibida, «Aceptar» que la deja en «[HUM]
  tiene 1 de 2 · Aliados de Eco [ECO]», «Romper» con su confirmación («Dentro de 15 minutos, los miembros de [ECO]
  podrán atacar a los tuyos») y la cuenta atrás «La alianza con [ECO] termina en 15 min»; y, al disolverse [ECO], el
  panel volvió en vivo a «0 de 2».
- **Sin comprobar en el navegador:** «Proponer alianza» desde «Ver clan» (pide hacer clic en la ciudad de otro clan en el
  mapa), «Aliado de [TAG]» en la barra de una ciudad ajena y el clic de ataque contra ella (los cubren
  `clans.test.ts` y el humo). En la base de desarrollo [HUM] queda sin alianzas, smoke_clan_b sin clan y smoke_orden de
  miembro raso.

## E22 — Unidades de civilización

**Estado: HECHO (2026-11-17)**, fusionado en `main` (`6fde18f`) con la CI en verde el mismo día. Contrato:
[SPEC-CIVILIZATION-UNITS](../specs/civilization-units.md), aceptada el 2026-11-17 con sus diez decisiones de producto,
y [ADR-043](../decisions/ADR-043-civilization-units.md).

**Objetivo.** Que cada civilización tenga una cara en el campo de batalla: una unidad propia que sólo ella entrena,
pagada en parte con su bien, que lucha con las reglas de combate que ya existen.

**Alcance.** Legionario (`ROMAN`), catafracto (`BYZANTINE`), arquero a caballo (`PERSIAN`) y berserker (`NORSE`) en el
catálogo de unidades, con el perfil de la spec §4.2 y ligados por `civilizations.unit`; `city.train` con ellas desde la
Edad de Bronce, por 80 de madera, 40 de piedra y 10 del bien propio, 35 s y 1 de población; el combate, la respuesta
automática, el saqueo, el asedio, la conquista y la captura por capacidad y no por el tipo `WARRIOR`; `UnitType` y
`TrainableUnitType` con los cuatro tipos y el catálogo de presentación `units.json`; `INV-UNIT-012` e `INV-UNIT-013`;
migración `000024`; el cliente (botón, siluetas, textos) y `scripts/smoke.mjs --unidad-civilizacion`. **No entra**
(spec §2): más de una unidad por civilización, sustituir al guerrero, mecánicas nuevas de combate, un bono propio en
tierra afín, compartir la unidad con los aliados y un tope por ciudad.

### Entregables

1. **HECHO** — Aceptación: spec, [ADR-043](../decisions/ADR-043-civilization-units.md), `INV-UNIT-012` e
   `INV-UNIT-013`, y las precisiones de `RN-ECON-010` ([economy-wood.md](../specs/economy-wood.md)), `RN-COMBAT-001`
   ([combat-raid.md](../specs/combat-raid.md)), `RN-COMBAT-012` ([combat-response.md](../specs/combat-response.md)),
   Q1 y Q8 de [civilization-goods.md](../specs/civilization-goods.md), Q6 de
   [civilizations.md](../specs/civilizations.md) e `INV-COMBAT-003` ([combat.md](../invariants/combat.md)).
2. **HECHO** — Dominio: los cuatro tipos exclusivos, `TrainCosts`, `OwnGoodCost`, `RequiresEra`, `Exclusive`,
   `Civilization.Unit` con su validación (`INV-UNIT-013`, también en la carga) y `production.CanTrain` con
   civilización y edad (`TestLaUnidadExclusivaPideSuCivilizacionSuEraYSuBien`,
   `TestLasUnidadesDeCivilizacionTienenSuPerfil`, `TestElCatalogoTieneUnaUnidadExclusivaPorCivilizacion`).
3. **HECHO** — Migración `000024` y persistencia: `civilizations.unit`, el trigger de `city_productions`, la carga y el
   encargo con varios débitos (`TestCadaCivilizacionTieneSuUnidadEnLaBase`,
   `TestElEncargoDeUnaExclusivaDebitaLosTresRecursosJuntos`,
   `TestLaBaseRechazaLaUnidadDeOtraCivilizacionOAntesDeBronce`; el trigger, verificado por mutación).
4. **HECHO** — Simulación: la reserva y la devolución en varios recursos, la respuesta automática por `CanAttack` y los
   golden de cada perfil (`civilization_units_test.go`: legionario, berserker, arquero a tres tiles, catafracto,
   tierra afín, asedio y saqueo, una unidad de cada tipo que ataca, saquea y derriba, el estandarte de un legionario y
   la caravana que captura un berserker) y la recuperación
   (`TestReinicioDuranteElEntrenamientoDeUnLegionario`).
5. **HECHO** — Protocolo: los cuatro tipos en Zod y en Go, `units.json`, `CivilizationInfo.unit`; contract tests
   (`unit_catalog_contract_test.go`), e2e (`TestEntrenarUnLegionarioPorLaRed`, `TestUnArqueroAtacaPorLaRedDesdeTresTiles`) y Zod.
6. **HECHO** — Cliente: el botón de la unidad propia, las siluetas, las capacidades y los textos (`units.test.ts` y los
   de economía, conquista, registro y proyección).
7. **HECHO** — Humo y prueba en vivo: `scripts/smoke.mjs --unidad-civilizacion` (sus funciones, con test en
   `scripts/smoke.test.mjs`).

### Prueba en vivo (2026-11-17)

Contra la base de desarrollo, que el servidor llevó de la migración 23 a la 24 al arrancar, con `pnpm run
verify:full` en verde antes:

- `node scripts/smoke.mjs --web http://localhost:3001 --unidad-civilizacion` pasó entero (21 comprobaciones):
  smoke_runner (`ROMAN`, en Bronce) pide `CATAPHRACT` y se rechaza con «la unidad es de otra civilización»; sin vino,
  su taller elabora 10; el legionario se encarga con 80 de madera, 40 de piedra y 10 de vino y el plazo romano
  (31 818 ms), y nace con su tipo y 110 de vida.
- En el navegador, como smoke_clan_a (`ROMAN`, en la Edad de Piedra): tras «Aldeano» y «Guerrero», el botón
  «Legionario (80 de madera, 40 de piedra y 10 de vino)», deshabilitado con «El legionario pide la Edad de Bronce.».
- Más tarde, con el panel visible y la página recargada (si carga con el panel oculto, el lienzo se queda en 300 × 150),
  como smoke_clan_a con la cámara sobre smoke_runnerpolis: el legionario de smoke_runner se dibuja como un rombo ajeno
  con el escudo claro delante, y se distingue del guerrero de al lado.
- **Sin comprobar en el navegador:** las siluetas del catafracto, el arquero a caballo y el berserker (no hay ninguno en
  la base de desarrollo), «Legionario · Romanos» en el panel de selección (sólo el dueño selecciona su unidad) y la
  línea de la unidad propia en el registro de alta (pide cerrar la sesión del navegador). Los textos los cubren
  `units.test.ts` y la proyección; esas tres siluetas, sólo el typecheck.
- **Repaso (2026-11-17 y 18):** comprobados la línea de la unidad propia en el formulario de alta, «Legionario ·
  Romanos» en el panel de selección y la silueta del berserker; las del catafracto y el arquero a caballo siguen sin
  comprobar en el navegador ([§E24](#e24--guerras-entre-clanes), «Repaso de los tests»).

## E23 — Edad de los Castillos

**Estado: HECHO (2026-11-17)**, fusionado en `main` (`0d818fe`) con la CI en verde el mismo día. Contrato:
[SPEC-CASTLE-AGE](../specs/castle-age.md), aceptada el 2026-11-17 con sus diez decisiones de producto, y
[ADR-044](../decisions/ADR-044-castle-shooting.md).

**Objetivo.** Cerrar la escalera de eras del canon con un avance que vuelve a pedir comercio, y darle a la última era
algo que se note en el mapa: el castillo, la primera defensa de una ciudad que no depende de tener un ejército en casa.

**Alcance.** `city.advance` de Hierro a Castillos por 1 600 de madera, 800 de piedra y 120 bienes ajenos en cualquier
mezcla, 360 s, que sube el límite a 150; el castillo (`CASTLE`): uno por ciudad, en un hueco de la zona urbana, 500 de
madera y 800 de piedra, 180 s con `production.speed`, 1 000 de vida, asediable; su disparo de 10 cada 2 s a 5 tiles
contra quien ataca a su dueño, nunca en paz, sin despertar la respuesta automática; sus muertes en el registro y los
avisos (`UNIT_LOST_TO_CASTLE`, `CASTLE_KILL`); `targetUnitId` en `BuildingView`; `INV-ECON-012` e `INV-COMBAT-007`;
migración `000025`; el cliente (avance, botón, dibujo, disparo, textos) y `scripts/smoke.mjs --castillo`. **No entra**
(spec §2): eras posteriores, mejoras o nuevas unidades, tecnologías, que el castillo dé población o defienda a otros,
ayuda del clan para el avance y que el castillo haga afín su entorno.

### Entregables

1. **HECHO** — Aceptación: spec, [ADR-044](../decisions/ADR-044-castle-shooting.md), `INV-ECON-012` e
   `INV-COMBAT-007`, la precisión de `INV-ECON-011` ([economy.md](../invariants/economy.md)) y las de `RN-ECON-024` y
   Q6 ([economy-stone.md](../specs/economy-stone.md)), `RN-GOODS-006` y Q11
   ([civilization-goods.md](../specs/civilization-goods.md)), el no-scope de
   [civilization-units.md](../specs/civilization-units.md), `RN-COMBAT-029` ([combat-siege.md](../specs/combat-siege.md)),
   `RN-COMBAT-014` ([combat-response.md](../specs/combat-response.md)) y `Q-12` del [backlog](backlog.md).
2. **HECHO** — Dominio: el paso Hierro → Castillos de `advance`, `CASTLE` en el catálogo de edificios con su
   perfil de disparo (`CanShoot`), y los tipos `UNIT_LOST_TO_CASTLE` y `CASTLE_KILL` en `eventlog`.
3. **HECHO** — Migración `000025` y persistencia: `CASTLE`, el único parcial y la muerte con autor edificio
   (`killerBuildingId`, `killerBuildingType`). `000025` probada abajo y arriba en la base de tests.
4. **HECHO** — Simulación: `castleState`, el objetivo y los disparos en la fase 4, el golden «castillo», seis
   guerreros contra el castillo, las exclusiones y el reinicio; `INV-ECON-012` e `INV-COMBAT-007` verificados por
   mutación.
5. **HECHO** — Protocolo y registro: `CASTLE`, `targetUnitId`, los dos tipos de entrada; contract tests (Go y Zod)
   y dos e2e por WebSocket.
6. **HECHO** — Cliente: el avance a Castillos, el botón y el dibujo del castillo, la línea del disparo y los textos,
   con tests de lo puro.
7. **HECHO** — Humo y prueba en vivo: `scripts/smoke.mjs --castillo`.

### Prueba en vivo (2026-11-17)

Contra la base de desarrollo, que el servidor llevó de la migración 24 a la 25, con el almacén sembrado
(`EO_DEVSEED_STOCK_FOR=smoke_runner,smoke_victim pnpm run dev:seed`):

- **Primera pasada** de `--castillo`: smoke_runner, en Bronce, avanzó a Hierro (límite 110) y a Castillos (límite
  160) con sus plazos; un avance más se rechazó con `NOT_IMPLEMENTED`; el castillo se encargó en (292, 18) con el plazo
  romano (163 636 ms) y 1 000 de vida, y se terminó. El castillo apuntó al guerrero de smoke_victim y le disparó (60 →
  50), pero lo remató un guerrero de smoke_runner que respondió al asedio de su ciudad (`RN-COMBAT-037`), así que el
  aviso del castillo no llegó y la pasada falló. El castillo hizo lo que dice la spec; el humo pasó a apartar antes a
  los guardias propios (`guardiasCerca`, con test).
- **Segunda pasada**, verde (31 comprobaciones): siete guardias de smoke_runner se apartan, un guerrero nuevo de
  smoke_victim asedia el castillo, recibe el primer disparo (60 → 50), el castillo apunta a él (`building.update` con
  `targetUnitId`) y lo mata; smoke_victim recibe `UNIT_LOST_TO_CASTLE` y smoke_runner `CASTLE_KILL`.
- **En el navegador**, con la sesión de smoke_runner y el panel visible: la ciudad en la Edad de los Castillos, sin
  botón de avanzar, con «Castillo» deshabilitado y su motivo («La ciudad ya tiene un castillo.»); el castillo dibujado
  junto al centro (muro con almenas, dos torres y puerta), con su barra de vida mientras lo asediaban; la línea del
  castillo al sitiador mientras le disparaba; y el aviso y la entrada del registro «Tu castillo mató a un guerrero de
  smoke_victimpolis».
- **Sin comprobar en el navegador:** el botón «Edad de los Castillos (n/120 bienes)» de una ciudad en Hierro y
  «Castillo» habilitado con su coste (smoke_runner ya estaba en Castillos y con castillo), el texto «El castillo de … mató
  a uno de tus …» del lado de la víctima (lo comprobó el humo como aviso y lo cubre `eventLog.test.ts`), y el castillo
  en obra (sólo el contorno). En la base de desarrollo smoke_runner queda en Castillos con su castillo, y sus guardias
  junto a (277, 19).
- **Repaso (2026-11-17):** comprobados el botón «Edad de los Castillos (120/120 bienes)» de una ciudad en Hierro,
  «Castillo» habilitado con su coste, el texto del lado de la víctima y el castillo en obra
  ([§E24](#e24--guerras-entre-clanes), «Repaso de los tests»).

## E24 — Guerras entre clanes

**Estado: HECHO (2026-11-17)**, fusionado en `main` (`66ad94b`) con la CI en verde el mismo día. Contrato:
[SPEC-CLAN-WARS](../specs/clan-wars.md), aceptada el 2026-11-17 con sus once decisiones de producto, y
[ADR-045](../decisions/ADR-045-clan-wars.md).

**Objetivo.** Que el conflicto entre dos clanes tenga principio, compromiso y final: una guerra que declara un líder,
que rompe los pactos entre los miembros de los dos clanes para que nadie haga una paz por separado, con un marcador
público y un resultado que queda en la historia de los dos. Cierra la fase Clans del roadmap.

**Alcance.** `clan.war.declare`, `clan.war.peace` y `clan.war.surrender` (de 44 a 47 comandos), sólo para el líder;
15 minutos de preparación, 72 horas de guerra como mucho (`EO_CLAN_WAR_HOURS`) y 24 de tregua sin paz
(`EO_CLAN_WAR_COOLDOWN_HOURS`); una guerra declarada abierta por clan (`EO_CLAN_MAX_WARS`); los pactos y alianzas entre
los miembros de los dos clanes en preaviso y ninguno nuevo; el marcador (1 por baja, 3 por edificio destruido, 10 por
territorio ganado) en el payload de los eventos que puntúan; el final por paz, rendición, plazo o disolución, con el
historial; `wars` y `warRecord` en `clan.update` y en `clan.info`; los eventos `ClanWar*`, tres tipos nuevos en el
registro y en los avisos; `INV-CLAN-008`, `INV-CLAN-009` e `INV-DIPLO-004`; migración `000026` (`clan_wars`, sus
triggers y el índice del marcador); el cliente y `scripts/smoke.mjs --guerra-clan`. **No entra** (spec §2): bonos de
guerra, tributo o premio material, levantar la paz de la Orden o la protección offline, llamada a las armas de los
aliados, coaliciones, guerras contra jugadores sin clan, objetivos de guerra, puntos por saqueo o caravanas, rankings.

### Entregables

1. **HECHO** — Aceptación: spec, [ADR-045](../decisions/ADR-045-clan-wars.md), `INV-CLAN-008`, `INV-CLAN-009` e
   `INV-DIPLO-004`, y las precisiones de `RN-CLAN-008` ([clans.md](../specs/clans.md)), `RN-CLANALLY-001`
   ([clan-alliances.md](../specs/clan-alliances.md)) y `RN-DIPLO-001` ([diplomacy.md](../specs/diplomacy.md)).
2. **HECHO** — Dominio: la guerra, su máquina de estados, los puntos y el resultado (`internal/domain/clan`).
3. **HECHO** — Migración `000026` y persistencia: `clan_wars`, los triggers, el índice del marcador, cada orden
   con los dos candados, el final en la disolución, la carga y la vista pública.
4. **HECHO** — Simulación: las guerras en `clanState`, las órdenes en dos fases, `enforceWarTreaties`, el marcador
   en los eventos, el comienzo y el plazo, el final por disolución y el golden «guerra de clanes».
5. **HECHO** — Protocolo y registro: los tres comandos, `wars` y `warRecord`, los tres tipos nuevos; contract
   tests (Go y Zod) y su e2e.
6. **HECHO** — Cliente: la sección «Guerras» del panel, «Declarar la guerra» en la vista pública, «En guerra con
   [TAG]» en la barra de una ciudad ajena y los textos del registro y de los avisos.
7. **HECHO** — Humo y prueba en vivo: `scripts/smoke.mjs --guerra-clan`.

### Prueba en vivo (2026-11-17)

Contra la base de desarrollo, que el servidor llevó de la migración 25 a la 26, arrancado con
`EO_TREATY_BREAK_NOTICE_MINUTES=1` (la preparación en un minuto) y `EO_CLAN_WAR_SCORE_PUSH_SECONDS=2`:

- **Humo** `--guerra-clan`, verde a la primera (31 comprobaciones): smoke_clan_b funda [RIV], smoke_clan_a le declara
  la guerra con su aviso y la vista pública de [HUM] la lleva; la guerra empieza al minuto; un aldeano de smoke_clan_b
  muere ante el guerrero de smoke_clan_a y el marcador queda en 1 – 0; smoke_clan_b ofrece la paz, smoke_clan_a la
  acepta, los dos ven el empate (aviso y registro) y [RIV] se disuelve.
- **En el navegador**, con la sesión de smoke_runner y el panel visible: fundar [PRU] desde el panel, con la sección
  «Guerras» («Sin guerras terminadas»); la vista pública de [HUM] con su historial y «Declarar la guerra», con su
  confirmación; tras declarar, «[PRU] 0 – 0 [HUM] — Empieza en 1 min», y al minuto «Termina en 3 d»; «En guerra con
  [PRU]» en la barra de la ciudad de [HUM] y «En guerra con: [HUM] 0 – 0 [PRU]» en su vista pública; «Ofrecer la paz»,
  «Has ofrecido la paz» y «Retirar la oferta»; «Rendirse» con su confirmación, y después «1 derrota» en [PRU] y
  «1 victoria» en [HUM]. Al final smoke_runner salió de [PRU], que se disolvió.
- **Un fallo, corregido.** Volver a declarar durante la tregua se rechazó, pero el pie decía el texto general de
  `INVALID_TARGET` y no el motivo; `readableError` muestra ahora el motivo del servidor (test en `economy.test.ts`,
  visto fallar antes del arreglo; precisión en la spec).
- **Sin comprobar en el navegador:** el texto corregido del rechazo por tregua (lo cubre el test; no volví a llevar la
  cámara a [HUM] tras la recarga), los avisos y las entradas del registro de la guerra del lado del jugador (los
  comprobó el humo y los cubre `eventLog.test.ts`) y el marcador que sube en el panel. Siguen pendientes las
  comprobaciones de §E22 y §E23.

### Repaso de los tests (2026-11-17, rama `claude/tests-guerras-clan`)

- **Simulación.** `TestUnaConquistaEnGuerraPuntuaAlConfirmarse`: una conquista entre los dos lados suma 10 en el tick
  en que vuelve la confirmación y no en el que la base la escribe, sobre el territorio de un miembro y sobre el del
  clan enemigo, y nada si la base la rechaza. `TestUnaCaravanaQueMataUnCastilloNoPuntua`: la caravana muere sin
  puntos y un aldeano muerto por el mismo castillo suma 1. En el juego un castillo no llega a disparar a una caravana
  (sólo apunta a quien ataca a su dueño y la caravana no ataca), así que el golpe mortal se aplica con `KillByCastle`,
  un gancho que sólo existe en los tests; precisión en la spec.
- **Integración.** `CaptureTerritory` escribe `war` en una sola fila de su evento, también con un clan dueño (una fila
  por miembro y `war` sólo en la de quien lo gana), y `DestroyBuilding` en su payload; `WarScores` suma esos puntos.
- **INV-CLAN-009**, siete mutaciones nuevas, cada una vista fallar (ficha).
- **En el navegador**, con el servidor arrancado con `EO_TREATY_BREAK_NOTICE_MINUTES=1` y el panel oculto (sólo se
  leyó el texto de la página; sin `requestAnimationFrame` el mapa no se dibuja):
  - el formulario de alta, al salir, con la línea «Unidad propia: …» de cada civilización (§E22);
  - smoke_norse, sembrado con `EO_DEVSEED_STOCK_FOR=smoke_norse`, avanzó de Piedra a Castillos desde el HUD: en
    Hierro, el botón «Edad de los Castillos (120/120 bienes)» y «Castillo» deshabilitado con «El castillo pide la
    Edad de los Castillos.»; en Castillos, «Faltan 160 de madera y 80 de piedra.» y, sembrado otra vez, «Castillo»
    habilitado con «Encarga el castillo: 500 de madera y 800 de piedra, unos 180 s…» (§E23). Por el camino construyó
    su taller, elaboró 10 pieles y entrenó un berserker;
  - tras `smoke.mjs --castillo` (verde, 24 comprobaciones), smoke_victim vio en «Mientras no estabas» «El castillo de
    smoke_runnerpolis mató a uno de tus guerreros cerca de (293, 19)» (§E23).
- **Con el panel visible** (2026-11-18): smoke_norse encargó su castillo y se dibujó en obra, sólo el contorno gris
  con sus dos torres, junto al centro de smoke_norsepolis, frente al castillo completo y relleno de smoke_runnerpolis
  (§E23); su berserker se dibuja como un rombo propio con las dos hachas cruzadas y, seleccionado, el panel dice
  «Berserker · Nórdicos» (§E22); el legionario de smoke_runner, seleccionado, «Legionario · Romanos» (§E22). Para
  seleccionarlo hubo que apartar a los seis guerreros que compartían su tile (el clic elige la primera unidad del
  tile): las guardias de smoke_runner quedan repartidas en los tiles de al lado de (277, 19).
- **La tregua** (2026-11-18): smoke_runner fundó [PRU] desde el panel, abrió «Ver clan» de [HUM] desde su ciudad y le
  declaró la guerra («[PRU] 0 – 0 [HUM] — Empieza en 1 min»). Al volver a declarar tras rendirse, el pie dijo «No se
  puede: vuestros clanes terminaron una guerra hace poco: espera a que acabe la tregua.». Después smoke_runner salió
  de [PRU], que se disolvió.
- **Un fallo, corregido.** Rendirse **durante la preparación** devolvió `INTERNAL_ERROR: no se pudo aplicar la orden de
  guerra`: la base rechazaba el final por el `CHECK clan_wars_ends_at` (una guerra que no empezó no tenía plazo). Lo
  mismo pasaba con la paz y con la disolución de un clan en preparación. `War.End` da ahora a esa guerra su final como
  plazo y la disolución escribe `ends_at = coalesce(ends_at, ended_at)`, sin migración;
  `TestUnaGuerraTerminaEnSuPreparacion` (integration) lo reproduce, visto fallar sin el arreglo y con cada mitad
  quitada; precisión en la spec. La rendición de la prueba se hizo ya con la guerra activa, tras reiniciar el servidor
  con el arreglo.
- **Sin comprobar en el navegador:** las siluetas del catafracto y del arquero a caballo (no hay jugadores bizantinos
  ni persas en la base de desarrollo). La confirmación de «Declarar la guerra» dice «terminarán en 15 minutos» aunque
  el servidor corra con `EO_TREATY_BREAK_NOTICE_MINUTES=1`: el texto del cliente es el valor por defecto.

## E25 — Árbol tecnológico

**Estado: HECHO (2026-11-18)**, fusionado en `main` (`ab7bcb0`) con la CI en verde el mismo día. Contrato:
[SPEC-TECHNOLOGIES](../specs/technologies.md), aceptada el 2026-11-18 con sus quince decisiones de producto, y
[ADR-046](../decisions/ADR-046-technologies.md).

**Objetivo.** Que lo que una ciudad acumula entre dos avances de era tenga en qué gastarse: mejoras permanentes del
jugador que se investigan con recursos y tiempo, con una exclusiva por civilización para que nadie complete el árbol.
Abre la fase Technology del roadmap.

**Alcance.** `city.research` (de 47 a 48 comandos); doce tecnologías en la tabla `technologies` (ocho comunes, dos por
era, y una exclusiva por civilización desde Hierro); una investigación por ciudad, nunca durante un avance de era, que
no impide entrenar, construir ni elaborar; el coste en madera, piedra y, en Hierro y Castillos, bienes ajenos (la
exclusiva, su bien propio); las tecnologías del jugador, privadas y permanentes; sus efectos compuestos con el rasgo de
la civilización en las cinco claves de E13 y dos nuevas (`combat.attack_damage`, `economy.gather_carry`); `research` y
`technologies` en la vista propia de la ciudad; `technologies.json`; `INV-TECH-001` e `INV-TECH-002`; migración
`000027`; `EO_TECHNOLOGY_RESEARCH_SECONDS`; el panel «Tecnologías» y `scripts/smoke.mjs --tecnologia`. **No entra**
(spec §2): tecnologías que abran unidades, edificios o eras, cancelar una investigación, más de una a la vez, perderlas,
compartirlas o venderlas, ramas excluyentes, un registro o un aviso por investigación, verlas en una ciudad ajena.

### Entregables

1. **HECHO** — Aceptación: spec, [ADR-046](../decisions/ADR-046-technologies.md), la familia `INV-TECH` con
   `INV-TECH-001` e `INV-TECH-002`, las precisiones de `RN-ECON-026` ([economy-stone.md](../specs/economy-stone.md)) y
   `RN-CIV-004` ([civilizations.md](../specs/civilizations.md)), y la `Q-12` del [backlog](backlog.md) cerrada.
2. **HECHO** — Dominio: el catálogo, su validación, la composición y la orden (`internal/domain/technology`) y las
   dos claves nuevas de rasgo.
3. **HECHO** — Migración `000027` y persistencia: las tres tablas sembradas, los tres triggers, el encargo, el fin,
   la carga y la validación de carga.
4. **HECHO** — Simulación: las tecnologías por jugador, `State.trait` compuesto, `city.research` en dos fases, el
   fin optimista, los dos puntos de consumo nuevos y el golden «investigación».
5. **HECHO** — Protocolo: `city.research`, `research` y `technologies`, `technologies.json`; contract tests (Go y
   Zod) y su e2e.
6. **HECHO** — Cliente: el panel «Tecnologías» con sus motivos y su cuenta atrás.
7. **HECHO** — Humo y prueba en vivo: `scripts/smoke.mjs --tecnologia`.

### Prueba en vivo (2026-11-18)

Contra la base de desarrollo, que el servidor llevó de la migración 26 a la 27 y cuyo catálogo validó al arrancar,
con `EO_TECHNOLOGY_RESEARCH_SECONDS=20` y el almacén de smoke_runner sembrado (`EO_DEVSEED_STOCK_FOR=smoke_runner`):

- **Humo** `--tecnologia`, verde a la primera (23 comprobaciones): la vista propia de smoke_runner llevaba sus
  tecnologías (ninguna); `POLVORA` se rechazó con «esa tecnología no está a tu alcance»; Albañilería se encargó con su
  plazo de 20 s, una segunda a la vez fue `CITY_BUSY`, llegó como tecnología al vencer y una casa de smoke_runner pasó
  de 150 a 180 de vida.
- **En el navegador**, con la sesión de smoke_runner: «Tecnologías (1)» en el HUD; el panel con las nueve de un romano
  por era (las ocho comunes y Calzadas · propia), cada una con su efecto, su coste y su plazo con el rasgo romano
  («unos 55 s»), Albañilería con «✓ La tienes» y los motivos («Faltan 30 bienes de otras civilizaciones.», «Faltan 40
  de vino.», «Pide Armas de bronce.», «Pide Gremios.»). «Investigar» en Herramientas de piedra: «Investigando: 20 s» en
  el panel y «Investigando Herramientas de piedra: 20 s» en el HUD, las demás con «Ya se investiga Herramientas de
  piedra.»; al vencer, «✓ La tienes» y «Tecnologías (2)».
- **Sin comprobar en el navegador:** que una ciudad ajena no enseñe nada (no hay interfaz para mirarla; lo cubren el
  e2e y el snapshot de la simulación), el motivo del avance de era mientras se investiga (smoke_runner ya está en la
  última era; lo cubre `technologies.test.ts`) y los efectos en el juego salvo la vida de la casa (los cubren los tests
  de simulación).

## E26 — Mercados

**Estado: HECHO (2026-11-19)**, en `main` desde la fusión `afbf0ee`, con la CI en verde. Contrato: [SPEC-MARKETS](../specs/markets.md),
aceptada el 2026-11-18 con sus trece decisiones de producto, y [ADR-047](../decisions/ADR-047-markets.md).

**Objetivo.** Que se pueda comprar y vender a cualquiera sin un trato entre dos: un tablón público de ofertas en lotes,
con lo ofrecido en custodia y una comisión, que cualquiera con mercado compra sin tratado. Cierra lo que quedaba de
`OUT-08` y abre la parte de mercados de la fase Trade del roadmap.

**Alcance.** El edificio `MARKET` (Bronce, uno por ciudad, derribable); `market.post`, `market.buy`, `market.cancel` y
la consulta `market.list` (de 48 a 52 mensajes); ofertas de un recurso por otro, de 1 a 100 lotes, 3 abiertas por
jugador durante `EO_MARKET_OFFER_HOURS`; la custodia al publicar; la comisión de `⌈cantidad / 10⌉` por lote y por
parte; dos caravanas por compra; `market.listing`, `market.update` y `marketOffers[]`; `MARKET_SOLD` y
`MARKET_EXPIRED` en el registro; `INV-TRADE-008` a `INV-TRADE-010` y las precisiones de `INV-TRADE-001` e
`INV-TRADE-007`; migración `000028`; el panel «Mercado» y `scripts/smoke.mjs --mercado`. **No entra** (spec §2): un
mercado del sistema, una moneda, el cruce automático, órdenes de compra, varios recursos por lado, precios de
referencia, ofertas dirigidas, el tablón que se refresca solo y avisos en vivo de las ventas.

### Entregables

1. **HECHO** — Aceptación: spec, [ADR-047](../decisions/ADR-047-markets.md), `INV-TRADE-008` a `INV-TRADE-010`, las
   precisiones de `INV-TRADE-001`, `INV-TRADE-007` y `RN-CARAVAN-001` ([caravans.md](../specs/caravans.md)).
2. **HECHO** — Protocolo: los cuatro comandos, `market.listing`, `market.update`, `marketOffers[]`, `MARKET` y los
   dos tipos de entrada del registro, con `otherPlayerId` anulable y `lots`; contract tests (Go y Zod).
3. **HECHO** — Dominio, migración `000028` y persistencia: la oferta, la comisión, el edificio, la publicación, la
   compra con sus caravanas, el cierre, la carga y la lectura del tablón.
4. **HECHO** — Simulación: las tres órdenes, la reserva de lotes, el cierre optimista y la caducidad; el golden
   «mercado».
5. **HECHO** — WebSocket: `market.list` desde la conexión y el e2e de dos jugadores.
6. **HECHO** — Cliente: «Construir mercado», su silueta, el botón «Mercado» y el panel con sus tres pestañas.
7. **HECHO** — Humo y prueba en vivo: `scripts/smoke.mjs --mercado`.

### Prueba en vivo (2026-11-18)

Contra la base de desarrollo, que el servidor llevó de la migración 27 a la 28 al arrancar, con la web en el 3001:

- **Humo** `node scripts/smoke.mjs --web http://localhost:3001 --mercado`: 30 comprobaciones. La primera pasada
  construyó los dos mercados (54 545 ms cada uno, con el rasgo romano) y encontró un fallo del propio humo, que leía el
  almacén de smoke_victim de antes de pagar su mercado (corregido). La segunda retiró la oferta que la primera dejó
  abierta, publicó (madera 578 → 556: 20 en custodia y 2 de comisión), smoke_victim leyó el tablón, el vendedor no pudo
  comprarse su oferta (`INVALID_TARGET`), smoke_victim compró un lote (piedra 1897 → 1894), la caravana de madera llegó
  96 ms después de lo anunciado, la retirada devolvió 10 de madera y el registro del vendedor contó la venta
  (`MARKET_SOLD`). El servidor no registró ningún error ni `invariant_violation`.
- **Navegador** (smoke_runner, con el panel oculto): «Mientras no estabas» cuenta las dos ventas del humo («smoke_victimpolis
  compró 1 lote de tu oferta en el mercado: le mandas 10 de madera y te manda 2 de piedra»); el panel «Mercado» abre en
  «Comprar» con el tablón vacío; «Vender» con el ejemplo de la spec dice «Salen de tu almacén 60 de piedra en custodia y 6
  de comisión» y «Publicar» quita 66 de piedra (454 → 388); «Mis ofertas (1)» la lista («20 de piedra por 30 de madera ·
  0 vendidos, quedan 3 de 3, caduca en 24 h») y «Retirar» devuelve 60 (388 → 448).
- **Sin comprobar en el navegador:** la silueta del mercado en el mapa (con el panel oculto no se dibuja la escena;
  vista el 2026-11-20 en el repaso de E28, donde el mercado propio queda casi tapado por el centro urbano),
  comprar desde la pestaña «Comprar» (no había ofertas ajenas abiertas; lo cubren el humo, el e2e y `market.test.ts`) y
  «Construir mercado» (smoke_runner ya tenía el suyo; lo cubren el humo y `market.test.ts`).

## E27 — Arietes y murallas

**Estado: HECHO (2026-11-19)**, en `main` desde la fusión `9922439`, con la CI en verde. Contrato: [SPEC-SIEGE-ENGINES](../specs/siege-engines.md),
aceptada el 2026-11-19 con sus once decisiones de producto, y [ADR-048](../decisions/ADR-048-siege-engines-and-wall.md).

**Objetivo.** Que una ciudad pueda proteger sus edificios con una muralla de piedra, y que el atacante tenga con qué
abrirla: el ariete. Es el segundo paso de la fase Sieges del roadmap, después del derribo de E8, sin que la ciudad caiga.

**Alcance.** El edificio `WALL` (Bronce, uno por ciudad, en el tile del centro, 1 500 de vida); la unidad `RAM` (Hierro,
250 de vida, 40 cada 3 000 ms sólo contra edificios); la regla de quién daña qué: con la muralla completa en pie sólo el
ariete daña la ciudad, y sólo a la muralla; derribada, la ciudad queda en brecha; el ariete en `unit.demolish` y
rechazado en las demás órdenes; `WALL` y `RAM` en los enums estrictos y `buildingType` en el registro, sin mensajes
nuevos; `INV-COMBAT-008` e `INV-COMBAT-009`; migración `000029`; «Muralla» y «Ariete» en el cliente con sus siluetas y
`scripts/smoke.mjs --asedio`. **No entra** (spec §2): que la ciudad caiga, otras máquinas, murallas por niveles o con
puertas, que la muralla frene el saqueo, reparar un daño que no derribó y tecnologías o rasgos para el asedio.

### Entregables

1. **HECHO** — Aceptación: spec, [ADR-048](../decisions/ADR-048-siege-engines-and-wall.md), `INV-COMBAT-008` e
   `INV-COMBAT-009`, las precisiones de `RN-COMBAT-029` ([combat-siege.md](../specs/combat-siege.md)) y de
   `RN-CITY-009` ([city.md](../specs/city.md)).
2. **HECHO** — Protocolo: `WALL` y `RAM` en `BuildingType` y `UnitType`, `buildingType` en `EventLogEntry` y en el aviso;
   contract tests (Go y Zod).
3. **HECHO** — Dominio, migración `000029` y persistencia: la muralla y el ariete en sus catálogos, `CanDemolish`,
   `siege.CanDamage`, el único parcial y el trigger del centro.
4. **HECHO** — Simulación: la muralla como edificio del centro, el predicado en la orden y en cada golpe, el golpe
   del ariete y el fin del asedio que la regla deja de admitir; el golden «asedio».
5. **HECHO** — WebSocket: el e2e del ariete que derriba la muralla.
6. **HECHO** — Cliente: «Muralla» y «Ariete», sus siluetas, la vida de la muralla y los motivos del clic.
7. **HECHO** — Humo y prueba en vivo: `scripts/smoke.mjs --ariete` y `--ariete-completo`.

**Prueba en vivo (2026-11-19).** Con `verify:full` en verde, la base de desarrollo pasó de la migración 28 a la 29 al
arrancar el servidor, y `node scripts/smoke.mjs --web http://localhost:3001 --ariete-completo` superó sus 30
comprobaciones: smoke_victim (Bronce, romana) levantó su muralla en el centro en 109 091 ms (120 s con su
`production.speed`); smoke_runner (Castillos) entrenó un ariete y lo llevó a (451, 184), junto a la víctima; el ariete
contra la casa y el guerrero de `--asedio` se rechazaron con `INVALID_TARGET` y su motivo; el primer golpe dejó la
muralla en 1 460/1 500 con el aviso `besiegedAtMs` a la víctima; la muralla cayó con `building.destroyed`, y la sesión de
smoke_runner con la vista en casa recibió `HOUSE_DESTROYED` con `buildingType: WALL`. En el navegador integrado, el
HUD de smoke_runner enseña «Muralla» y «Ariete» con sus títulos, y «Mientras no estabas», «Tus arietes derribaron la
muralla de smoke_victimpolis». **Sin comprobar en el navegador:** las siluetas de la muralla y del ariete (el panel
estaba oculto y sin `requestAnimationFrame` el mapa no se dibuja), con las del mercado, el catafracto y el arquero a
caballo. Muralla, ariete y mercado se vieron el 2026-11-20 en el repaso de E28; el catafracto y el arquero a caballo
siguen sin ver.

## E28 — Bots

**Estado: HECHO (2026-11-20)**, en `main` desde la fusión `ade9b58`, con la CI en verde. Contrato: [SPEC-BOTS](../specs/bots.md), aceptada el
2026-11-19 con sus nueve decisiones de producto, y [ADR-049](../decisions/ADR-049-bots-as-protocol-clients.md).

**Objetivo.** Empezar a medir la jugabilidad y ver el mundo vivo: jugadores sin interfaz que se dan de alta y juegan
por el WebSocket v1 como un cliente, para poblar el mundo de desarrollo (y staging cuando exista) y dejar un registro
con el que medir el equilibrio. Prepara el pase visual (unidades que se mueven, construyen y pelean) y el despliegue a
staging (tráfico).

**Alcance.** El paquete `apps/bots` (TypeScript, `tsx` y Vitest, sin dependencias nuevas) que reutiliza el `GameClient`
y los reductores del cliente web; dos perfiles, constructor y saqueador, con un decisor puro con semilla; `pnpm run bots`
con `--constructores`, `--saqueadores`, `--duracion`, `--semilla` y `--contra-humanos`; el alta escalonada por la web
con `EO_BOT_PASSWORD`; el JSONL por ejecución y su resumen. **No entra** (spec §2): ningún cambio en el servidor, la base
o el protocolo; diplomacia, comercio, mercados, clanes, facciones, mensajes, conquista y arietes en los bots; bots en
producción y una marca de bot en el juego; pruebas de carga; acelerar el tiempo del mundo.

### Entregables

1. **HECHO** — Aceptación: spec y [ADR-049](../decisions/ADR-049-bots-as-protocol-clients.md); sin invariantes de
   dominio nuevos ni precisiones de reglas vecinas (el servidor no cambia).
2. **HECHO** — Paquete `apps/bots`: conexión (`GameClient` con el `WebSocket` de Node) y un espejo por conexión con
   `createWorldStore()`, que sale del mismo `createWorldState` que `useWorldStore` (test en `world.test.ts`); el alta,
   la entrada y los tickets con la cookie en memoria (`auth.ts`); `@empires-online/protocol` exporta también
   `default` para que `tsx` lo cargue desde los `.ts` de `apps/web`.
3. **HECHO** — El decisor puro y los perfiles (`decide.ts`, `profiles.ts`), con el saqueo por tramos, el depósito
   del botín y el derribo al llegar al suelo; `decide.test.ts` cubre cada regla y `RN-BOT-002` a `RN-BOT-005`.
4. **HECHO** — La ejecución (`main.ts`, `bot.ts`): opciones, altas escalonadas, exploración de colinas, la conexión
   de campaña, parada limpia (`RN-BOT-007`, `bot.test.ts` con un servidor falso), el JSONL y el resumen (`RN-BOT-006`,
   `log.test.ts`); `EO_BOT_*` en `.env.example` y en configuration.md §3.10; `pnpm run bots`; el paso `bots-test` de
   `verify:full` y, con el visto bueno de producto, el paso «Tests de los bots» de la CI.
5. **HECHO** — Docs: `docs/architecture/frontend.md` §4.24, `docs/operations/local-development.md` §3-bis.10 y §8,
   `docs/README.md`, AGENTS.md y las precisiones de la spec (§4.8, §9).
6. **HECHO** — Prueba en vivo (abajo); la parte del navegador, en el repaso del 2026-11-20, salvo una obra en curso.

### Prueba en vivo (2026-11-19)

Contra el servidor y la web de desarrollo (migración 29), en la rama `claude/slice-bots`. `pnpm run verify:full` en
verde con el paso nuevo `bots-test`.

- **Pasada corta** (`--duracion 4m --semilla 1`): tres altas nuevas (`bot_constructor_1` a `_3`) y una entrada
  (`bot_saqueador_1`); cada bot exploró y encontró su colina más cercana (63 a 192 tiles), recolectó y entrenó. A
  mitad se paró el servidor para sembrar las cuentas con `dev:seed` y se rearrancó: los cuatro reconectaron solos. Al
  vencer la duración, la parada cerró las conexiones y escribió el resumen. Sacó dos fallos, arreglados con su test:
  el saqueador gastó 15 órdenes en colinas sin ruta (`PATH_TOO_LONG`) con los aldeanos parados, y el resumen contaba
  como recolectado el salto de almacén del snapshot sembrado (2 269 de madera por hora en cuatro minutos).
- **Pasada larga** (`--duracion 25m --semilla 7`), con los almacenes sembrados por `dev:seed` (que sube a los costes de
  la era y más, con 180 bienes ajenos): los cuatro llegaron a Bronce a los 3 min 29 s–3 min 46 s y a Hierro a los
  8 min 23 s–8 min 59 s; `bot_constructor_3` y el saqueador, a Castillos a los 17 min 7 s y 16 min. Los constructores
  levantaron taller y muralla, elaboraron su bien (12 a 20 veces) e investigaron (4 a 7 tecnologías). El saqueador
  salió tres veces contra `bot_constructor_2`: 590 de madera robada en 118 golpes de 5 (la víctima vio 64 avisos de saqueo, que agrupan golpes), 15 guerreros perdidos frente a 10
  muertos del otro lado; la defensa automática de E3 con dos guardias deshizo la primera partida entera. **Ningún
  comando rechazado** en 262. Sacó un tercer fallo, también con su test: `building.update` no trae la ciudad ni el
  tipo, y el registro no apuntaba ninguna obra terminada (por eso esa pasada dice «construidos: —»).
- **Para producto, como medida de partida** (no son resultados del slice, §4.5 de la spec): con la economía sembrada,
  una partida de saqueo de 3 guerreros pierde contra 2 guardias parados, la observación de E3 que sigue abierta; y un
  constructor sin comercio no pasa de Bronce si no le siembran bienes ajenos (Hierro pide 60).
- **Sin comprobar en el navegador:** ver a los bots en el mapa. El panel del navegador integrado estuvo oculto (sin
  `requestAnimationFrame` el mapa no se dibuja) y la cámara del HUD no tiene forma de saltar a coordenadas ajenas: la
  sesión de smoke_runner estaba lejos de las ciudades de los bots.

### Repaso (2026-11-20, rama `claude/repaso-bots`)

Docs que no conocían `apps/bots` (README raíz, overview, system-context, estrategia de tests, pruebas de carga), dos
tests del saqueo y dos arreglos de los bots que sacó la prueba en vivo de este día, con su test visto fallar antes del
cambio (precisiones en [SPEC-BOTS](../specs/bots.md) §4.8). `pnpm run verify:full` en verde sobre `08f2365`, antes de
los dos arreglos.

- **Pasada de 40 min** (`--semilla 11`, plazos reales, sin sembrar): ni un saqueo. Los seis guerreros vivos de
  `bot_saqueador_1` seguían parados a unos 50 tiles de casa desde la pasada larga del 2026-11-19, cortada por su
  duración; la partida sólo se forma con guerreros en casa y el máximo ya estaba cubierto, así que no entrenó ni
  salió. **Arreglo:** sin partida, un militar parado fuera de casa vuelve; si el servidor rechaza la vuelta
  (`TARGET_NOT_WALKABLE` en la pasada siguiente, por un tramo que caía en un tile que no se pisa), los tramos se
  desplazan en espiral. De paso, la consola anunciaba la civilización del reparto por turno (`NORSE`) en vez de la de
  la cuenta (`ROMAN`).
- **Pasada de 45 min** (`--semilla 13`, con los dos arreglos): cuatro partidas contra `bot_constructor_2`, 1 175 de
  madera robada en 235 golpes, 28 guerreros perdidos frente a 19 muertos del otro lado, que el constructor repuso
  entrenando. **Ningún comando rechazado** en 161. Confirma, con la economía sin sembrar, la observación de E3: la
  defensa automática gana aunque pierda guardias, porque el saqueador no devuelve los golpes (análisis en la propuesta
  `SPEC-BOTS-TRADE`, en `Draft` en la rama `claude/slice-bots-comercio`).
- **Constructores parados:** `bot_constructor_1` (Hierro) y `bot_constructor_3` (Castillos) no mandaron ni una orden
  en 45 min: con las tecnologías hechas, el bien elaborado hasta su tope y sin bienes ajenos para el avance siguiente,
  sólo recolectan. Es el hallazgo del comercio de la prueba en vivo de E28, ahora sin siembra de por medio.
- **En el navegador** (sesión de smoke_runner, con la cámara llevada a cada ciudad arrastrando con el botón derecho;
  el panel no llegó a verse como visible y sólo dibujaba un fotograma por captura, así que lo visto son instantáneas,
  no animaciones): los aldeanos de `bot_saqueador_1` en sus viajes de recolección, en otra posición en cada captura;
  un saqueo de 3 guerreros contra `bot_constructor_2` (pasada `--semilla 15`), con los rombos rojos de borde amarillo
  (atacando) pegados a la muralla y, en la captura siguiente, alejándose con la marca de carga; la **muralla** de
  `bot_constructor_2` (el contorno gris con postes) y su taller; el **ariete** de smoke_runner junto a
  smoke_victimpolis (el tejadillo sobre ruedas con el tronco) y el **mercado** ajeno de smoke_victim (el toldo a rayas
  moradas). El **mercado propio** de smoke_runner (rayas verdes) se ve a medias: queda en el tile contiguo al centro
  urbano y el bloque del centro lo tapa casi entero, como a la casa y al taller; lo anoto para el pase visual.
- **Sin comprobar en el navegador:** una obra en curso (los bots de este mundo ya lo tienen todo construido y uno
  nuevo, `bot_constructor_4`, nórdico, dado de alta en esta pasada en (494,259), no levanta casa hasta llenar sus 20
  de población) y las siluetas del **catafracto** y del **arquero a caballo**, que no saca ningún bot: los
  constructores bizantino y persa sólo entrenan guerreros de guardia y el saqueador no elabora el bien que piden; hará
  falta una cuenta bizantina y otra persa en Bronce con taller.
- **El navegador integrado, para la próxima:** si la página se carga con el panel oculto, el lienzo se queda en
  300×150 aunque luego se muestre; hay que recargarla. Con el panel oculto, el desplazamiento con teclado apenas
  avanza (va por fotogramas); el arrastre con el botón derecho no depende de ellos.

## E29 — Bots que comercian

**Estado: HECHO (2026-11-22)**, en `main` desde la fusión `e781dee`, con la CI en verde. Contrato:
[SPEC-BOTS-TRADE](../specs/bots-trade.md), propuesta el 2026-11-20 y aceptada el 2026-11-21 con sus diez decisiones de
producto. Sin ADR nuevo: el ajuste de la defensa cambia una regla de la respuesta automática, no la fase 4.

**Objetivo.** Los dos hallazgos de E28: que una partida sin sembrar pueda pasar de Bronce, enseñando a los bots el
comercio que el protocolo ya da, y medir cuánto tarda en llegar a Hierro; y que el saqueo deje de perder siempre contra
dos guardias, porque el saqueador no devolvía los golpes.

**Alcance.** En `apps/bots`: tratados `TRADE` entre todos los bots y `NON_AGGRESSION` entre constructores (el saqueador
nunca firma pacto), tratos bien por bien con la tabla de precios del fichero de perfiles, el mercado del constructor
(construir, publicar, comprar, retirar), la partida de saqueo que se reúne antes de atacar, `--sin-comercio` y
`--con-humanos`, los sucesos nuevos del JSONL y del resumen (`RN-BOT-008` a `RN-BOT-012`). En el servidor: un guerrero
que saquea o asedia responde a los golpes y, al terminar, vuelve al tile en que estaba sin reanudar (`RN-COMBAT-039`,
`RN-COMBAT-040`). **No entra** (spec §2): mensajes, campos, tablas o migraciones nuevos; cambios en el cliente web;
alianzas, clanes, facciones y conquista en los bots; captura de caravanas por el saqueador; tocar los números del
equilibrio (los hallazgos van a otra spec, Q10).

### Entregables

1. **HECHO** — Aceptación: spec en `Accepted` con §10 como decisiones; `RN-COMBAT-039` y `RN-COMBAT-040` en
   [combat-response.md](../specs/combat-response.md), con precisiones de `RN-COMBAT-013` y de su Q3, de
   `RN-COMBAT-025`, `RN-COMBAT-027` y Q5 en [combat-pillage.md](../specs/combat-pillage.md) y de `RN-COMBAT-036`,
   `RN-COMBAT-037` y Q7 en [combat-siege.md](../specs/combat-siege.md); `RN-BOT-008` a `RN-BOT-012` con precisiones de
   §2, `RN-BOT-002` y `RN-BOT-004` en [bots.md](../specs/bots.md). Sin invariantes de dominio nuevos.
2. **HECHO** — Servidor: la disponibilidad del saqueador y del asediador y el fin de su saqueo o asedio al
   responder, con sus tests de simulación, el golden de los tres encuentros de spec §4.6 y la recuperación; los
   goldens de E3 y E5 que cambien, con el motivo.
3. **HECHO** — Bots: tratados, tratos y precios (`RN-BOT-008` a `RN-BOT-011`), con sus tests.
4. **HECHO** — Bots: el mercado del constructor y la partida que se reúne (`RN-BOT-012`), con sus tests.
5. **HECHO** — Registro, resumen y opciones (`--sin-comercio`, `--con-humanos`); docs (`docs/architecture/frontend.md`,
   `docs/operations/local-development.md`).
6. **HECHO** — Prueba en vivo (spec §4.8): pasada corta con plazos acortados; la misma partida reunida de 3
   saqueadores contra 2 guardias antes y después del cambio del servidor; la ejecución larga de 3 h sin sembrar con
   4 bots nuevos (Q9), anotada aquí como medida; y, con el panel visible, una caravana entre bots y el tablón del
   mercado con ofertas de bots.

### Prueba en vivo (2026-11-21)

Servidor, web y bots en esta Mac, con las cuentas de desarrollo de siempre (`bot_constructor_1..4`,
`bot_saqueador_1`): las tres primeras ciudades y el saqueador ya en Hierro o Castillos, con su bien elaborado y sin
bienes ajenos; `bot_constructor_4`, nórdico, en Piedra. Registros en `bots/runs/` (sin versionar).

- **Pasada corta** (20 min, `--semilla 21`, plazos reales y sin sembrar): todo lo que pide §4.8 en los primeros
  cuatro minutos. TRADE entre todos y pacto entre los cuatro constructores (7 tratados aceptados por constructor),
  entre 22 y 34 tratos cerrados por constructor, ninguna caravana perdida, tres mercados construidos, ofertas
  publicadas y la primera compra entre bots (`bot_constructor_2` compra vino a `bot_constructor_1`). **Medida:**
  `bot_constructor_1` pasó de Hierro a Castillos en **14 min** con 124 bienes ajenos por trato y 30 por mercado
  (8 de comisión). El saqueador levantó su taller y elaboró vino (Q1); sus tratos fallaron al principio porque no
  tenía vino por encima de su reserva.
- **Encontrado y arreglado en la pasada** (con su test, visto caer sin el arreglo): la marcha daba la orden de un
  tramo a un guerrero y esperaba a que se parara para dar la del siguiente (40 s entre órdenes); el registro
  apuntaba como caducado el acuerdo comercial que sustituye el pacto (el servidor lo pasa a `EXPIRED`), ahora
  «replaced»; el resumen contaba como llegada la era en que arrancaba cada bot.
- **La partida reunida:** tres partidas contra `bot_constructor_4`, cada una reunida entera a 16 tiles en 40 s.
- **Defensa antes y después** (15 min cada una, `--semilla 21`, la misma partida reunida de 4 contra el constructor
  nórdico con sus guardias; el «antes», con el servidor de `main` compilado aparte): **antes**, el saqueador perdió 8
  y mató 4 (605 de madera robada); **después**, perdió 8 y mató 15 (400 de madera). Con los mismos números, el
  saqueador que devuelve los golpes cambia el balance de 1 a 2 en contra a casi 2 a 1 a favor, como predicen los
  goldens de §4.6. El mundo de desarrollo es persistente, así que las dos pasadas no parten del mismo estado
  (`bot_constructor_4` iba reponiendo guardias): es una medida, no una repetición exacta.
- **En el navegador** (sesión de smoke_runner, panel oculto, una captura por fotograma): el tablón del mercado con
  las ofertas de `bot_constructor_1` y `bot_constructor_2` («10 de seda por 10 de vino», con «Fuera de alcance» y
  «Falta vino» donde tocaba), y, con la cámara entre `bot_constructor_2polis` y `bot_constructor_3polis`, las dos
  ciudades con muralla y mercado y caravanas en camino (el carro de SPEC-CARAVANS).
- **Ejecución larga (Q9, 2026-11-22):** 3 h a velocidad real, sin sembrar, con 4 cuentas nuevas
  (`--sufijo e29`, `--semilla 29`: `bot_constructor_1_e29` romano en 436,231, `_2_e29` bizantino en 221,496,
  `_3_e29` persa en 213,427 y `bot_saqueador_1_e29` nórdico en 61,40). **Medida:** los tres constructores llegan a
  Bronce (media 31 min, de 26 a 40); a Hierro llegan dos, `_3_e29` en **1 h 51 min** y `_2_e29` en **2 h 36 min**, con
  250 bienes ajenos cada uno, todos por trato (25 tratos cada uno, ninguna caravana perdida); nadie llega a Castillos.
  El saqueador llega a Bronce en 20 min y hace una sola partida (3 reunidos en 12 s, 1 baja, 2 muertos, 43 de madera).
- **Hallazgos para otra spec (Q10):** `bot_constructor_1_e29` queda a más de 256 tiles de los otros dos (el alcance de
  una caravana): sus tratos se rechazan con `PATH_TOO_LONG` (54 en 3 h, uno por pareja cada 10 min), construye el
  mercado pero no compra nada y se queda en Bronce. Con el mapa de hoy, una ciudad aislada no puede pasar de Bronce.
  **Precisión (2026-11-22):** no era sólo la distancia. `bot_constructor_3_e29` está a 223 tiles, por debajo de 256,
  y sus rutas también volvían con `PATH_TOO_LONG`: el corte es el tope de 50 000 nodos del A* (ADR-031) con el
  terreno de por medio. Y la ciudad tenía vecinas al alcance (las de los bots de E28, a 24-60 tiles), pero los bots
  sólo comercian con los de su ejecución. En el mundo de desarrollo todas las ciudades tienen entre 6 y 20 a menos de
  256 tiles; el riesgo es una cuenta que nazca lejos de todas, porque la fundación no mira a los vecinos. Lo recoge la
  propuesta [SPEC-TRADE-REACH](../specs/trade-reach.md).
  Los constructores 2 y 3 no levantan mercado (piden 150 de madera y 100 de piedra por encima de la meta de Hierro)
  y no lo necesitan: se cambian los bienes entre ellos.
- **Encontrado y arreglado en la ejecución larga** (con su test, visto caer sin el arreglo): la partida del
  saqueador, a 375 tiles de casa, pedía la vuelta en línea recta; el servidor la rechazaba (`PATH_TOO_LONG`) y la
  partida no terminaba nunca (652 rechazos en dos horas, y por eso una sola partida). Ahora la vuelta de una partida
  va, tras el primer rechazo, por tramos de 48 en espiral, como la de un guerrero varado. No se pudo comprobar en
  vivo: la partida no sobrevive a un reinicio del proceso, y al arrancar los guerreros lejanos ya vuelven por la
  regla de los varados.


## E30 — Al alcance del comercio

**Estado: HECHO (2026-11-23)**, en `main` desde la fusión `ab34622`, con la CI en verde. Contrato: [SPEC-TRADE-REACH](../specs/trade-reach.md), propuesta y aceptada el 2026-11-22 con
sus siete decisiones de producto. Sin ADR: la fundación ya es una búsqueda determinista con reglas de
[city.md](../specs/city.md) §6.1, y el tope de nodos de [ADR-031](../decisions/ADR-031-pathfinding-node-cap-50k.md) no
cambia (Q4).

**Objetivo.** El hallazgo de la ejecución larga de E29: una ciudad que nace donde no le llega ninguna caravana no puede
pasar de Bronce, porque los bienes ajenos sólo llegan comerciando. Que ninguna ciudad nueva nazca así, y que los bots
dejen de insistir con socios a los que no llega ninguna ruta.

**Alcance.** En el servidor: la fundación exige, si ya hay ciudades, una vecina a ≤ `EO_FOUNDING_MAX_NEIGHBOR_TILES`
(96) con ruta de caravana entre los depósitos, y si ningún sitio la tiene funda como antes (`RN-CITY-024`,
`RN-CITY-025`, [INV-CITY-019](../invariants/city.md#inv-city-019)). En `apps/bots`: la pareja o la oferta sin ruta se
aparta hasta el final de la ejecución, con el registro `unreachable` (`RN-BOT-013`). **No entra** (spec §2): mover
ciudades que ya existen, tocar los topes del pathfinding o la forma de las rutas, una fuente de bienes ajenos sin
comercio, avisar en el HUD de si hay ruta, y mensajes, campos, tablas o migraciones nuevos.

### Entregables

1. **HECHO** — Aceptación: spec en `Accepted` con §10 como decisiones, sin ADR; `RN-CITY-024` y `RN-CITY-025` en
   [city.md](../specs/city.md) §6.1, con precisiones de `RN-CITY-004`, `RN-CITY-005` y `RN-CITY-006`; `RN-BOT-013`
   en [bots-trade.md](../specs/bots-trade.md) §4.9, con su precisión en [bots.md](../specs/bots.md) §4.7 y el estado
   `unreachable` en §4.7 de bots-trade; la precisión de `RN-CARAVAN-002` en [caravans.md](../specs/caravans.md) (el
   tope de nodos, ADR-031); [INV-CITY-019](../invariants/city.md#inv-city-019) registrado sin cobertura.
2. **HECHO** — Fundación: la condición de alcance en `founding.FindSite` (`founding.Reach`, con un filtro de
   componentes conexas antes del A\*) y su llamada desde `internal/httpapi` y `cmd/devseed`, con
   `EO_FOUNDING_MAX_NEIGHBOR_TILES` en `internal/config`, `.env.example` y
   [configuration.md](../operations/configuration.md).
3. **HECHO** — Tests de la fundación: siete en `internal/game/founding/site_test.go` (spec §9.1) y
   `TestElSegundoJugadorNaceAlAlcanceDelPrimero` en `internal/persistence/postgres/fundacion_integration_test.go`;
   cuatro mutaciones de INV-CITY-019, que pasa a ✓.
4. **HECHO** — Bots: la pareja y la oferta sin ruta apartadas para toda la ejecución y el registro `unreachable`
   una vez por pareja (`RN-BOT-013`), con sus tests; la oferta sin ruta ya no tapa a las demás en la compra.
5. **HECHO** — Docs de arquitectura que describen `FindSite`: [overview.md](../architecture/overview.md),
   [game-server.md](../architecture/game-server.md) y [scalability.md](../architecture/scalability.md) §4.
6. **HECHO** — `pnpm run verify:full` entero, en verde (2026-11-22).
7. **HECHO** — Prueba en vivo (spec §4.5): cuatro altas nuevas con `--sufijo` y al menos un trato entre ellas en 30
   min; el caso del borde sin `NO_SITE_AVAILABLE`; las cuentas `_e29` apuntando una vez cada pareja inalcanzable.

### Prueba en vivo (2026-11-23)

Servidor y web de desarrollo en esta Mac, sin Redis (estado caliente en proceso), plazos reales y sin sembrar; dos
ejecuciones de bots a la vez. Registros en `bots/runs/` (sin versionar). No tiene parte visual: nada que comprobar en
el navegador.

- **Altas nuevas** (`--sufijo e30`, `--semilla 30`, 30 min): `bot_constructor_1_e30` romano en 161,209,
  `_2_e30` bizantino en 267,389, `_3_e30` persa en 104,85 y `bot_saqueador_1_e30` nórdico en 172,416, con su vecina
  más cercana a 26, 39, 45 y 36 tiles. A los 10 min `_3_e30` y `_1_e30` cierran el primer trato (40 de madera por 20
  de piedra), y el saqueador cierra cuatro con `_2_e30`. Tres parejas sin ruta entre ellas (`_3_e30` con el saqueador
  y con `_2_e30`), apuntadas una vez cada una. **Medida:** dos constructores y el saqueador llegan a Bronce (13, 25 y
  22 min); `_2_e30`, saqueado por el saqueador (750 de madera), se queda en Piedra.
- **La regla en el mundo de desarrollo no cambió ningún sitio.** Reconstruidas las cuatro altas sobre el mismo mundo y
  las mismas ciudades, con y sin la regla dan el mismo centro: el mundo ya es denso y toda ciudad tiene vecina a
  menos de 96. `_1_e30` nació a 77 tiles de su punto de partida por la espiral de siempre (agua y bosque alrededor),
  no por la regla. Que la regla actúa lo prueban el test de integración (sin ella, el segundo jugador nace a 98) y
  los de `internal/game/founding`, sobre mundos vacíos, con lago y con el tope de nodos.
- **El caso del borde** (una isla sin sitio al alcance) lo cubre `TestUnaIslaSinSitioAlAlcanceFundaComoAntes`, como
  pide la spec («un mundo de tests»): el mundo de desarrollo no tiene una isla así.
- **Las cuentas `_e29`** (`--sufijo e29`, `--semilla 29`, 20 min): diez parejas sin ruta, cada una apuntada una vez
  como `unreachable` con `PATH_TOO_LONG` en el primer minuto, y exactamente un rechazo de `trade.propose` por pareja
  (en la ejecución larga de E29, 54 en 3 h sólo para `_1_e29`). `_2_e29` y `_3_e29` siguen comerciando entre sí y
  `_3_e29` llega a Castillos en 13 min 21 s. `_1_e29` sigue sin socios en su ejecución y en Bronce: la regla no mueve
  ciudades que ya existen (Q3), y comerciar con otros bots a la vista quedó fuera (Q7).

## E31 — La caída de una ciudad

**Estado: HECHO (2026-11-23)**, en `main` desde la fusión `9b31ae2`, con la CI en verde. Contrato: [SPEC-CITY-FALL](../specs/city-fall.md),
propuesta y aceptada el 2026-11-23 con sus diez decisiones de producto, y [ADR-050](../decisions/ADR-050-city-fall.md).

**Objetivo.** Que la guerra tenga final: una ciudad en brecha puede caer. Es el último paso de la fase Sieges del
roadmap, después del derribo de E8 y de la muralla y el ariete de E27.

**Alcance.** `unit.assault`: una unidad que derriba planta el estandarte en el anillo de una ciudad en brecha, de Bronce
o posterior y fuera de su inmunidad de 72 h, y si aguanta 900 s la ciudad cae (`RN-SIEGE-009` a `RN-SIEGE-011`,
`RN-COMBAT-041`). La caída la arrasa (`fallen_at`) y su dueño refunda en la misma transacción, a ≥ 64 tiles de la
caída y del vencedor, con su era y sus tecnologías y sin edificios, almacén ni encargos; sus unidades pasan a la ciudad
nueva y el territorio capital queda libre (`RN-CITY-026` a `RN-CITY-029`). `CITY_ASSAULTED`, `CITY_LOST` y `CITY_TAKEN`
en el registro y los avisos; 30 puntos por ciudad en una guerra de clanes. `city.fallen`, `CityView.assault` y
`fallImmuneUntilMs`; migración `000030`; tres variables `EO_`; [INV-CITY-020](../invariants/city.md#inv-city-020),
[INV-CITY-021](../invariants/city.md#inv-city-021) e [INV-COMBAT-010](../invariants/combat.md#inv-combat-010);
`scripts/smoke.mjs --caida`. **No entra** (spec §2): quedarse la ciudad como segunda ciudad, caer sin asalto, botín de
la caída, elegir dónde refundar y los bots que asaltan.

### Entregables

1. **HECHO** — Aceptación: spec, [ADR-050](../decisions/ADR-050-city-fall.md), `INV-CITY-020`, `INV-CITY-021` e
   `INV-COMBAT-010`, las precisiones de `INV-CITY-001`, `INV-PLAYER-003` e `INV-TERR-011`, de `RN-CITY-020` y la nota de
   «un jugador, una ciudad» ([city.md](../specs/city.md)), de `RN-CONQ-008` ([territory-conquest.md](../specs/territory-conquest.md)),
   del no-scope de [combat-siege.md](../specs/combat-siege.md) y [siege-engines.md](../specs/siege-engines.md) y de
   `RN-CLANWAR-007` ([clan-wars.md](../specs/clan-wars.md)).
2. **HECHO** — Protocolo: `unit.assault`, `city.fallen`, `CityView.assault` y `fallImmuneUntilMs`, los tres tipos
   del registro y de los avisos; contract tests (Go y Zod).
3. **HECHO** — Dominio, migración `000030` y persistencia: `fallen_at`, los índices parciales, `city_assaults`, la
   transacción de la caída y la refundación, y las consultas de «la ciudad del jugador» sin las caídas.
4. **HECHO** — Simulación: la orden, el plazo en la fase 5 con su predicado por tick, la caída en dos fases y su
   aplicación en la RAM, el snapshot nuevo del vencido y el marcador; el golden «caída».
5. **HECHO** — WebSocket: el e2e del asalto y la caída.
6. **HECHO** — Cliente: «Asaltar» con sus motivos, la cuenta atrás, la ciudad que desaparece, el salto del vencido y
   los textos del registro.
7. **HECHO** — Humo y prueba en vivo: `scripts/smoke.mjs --caida`.

### Prueba en vivo (2026-11-23)

Contra la base de desarrollo, que el servidor llevó de la migración 29 a la 30, con `EO_CITY_ASSAULT_SECONDS=20` y
`EO_CITY_FALL_IMMUNITY_HOURS=0`, y la web en el 3001:

- `node scripts/smoke.mjs --web http://localhost:3001 --caida`, en tres pasadas. La primera dio de alta a `smoke_caida`
  y se paró pidiendo recursos para el avance (`EO_DEVSEED_STOCK_FOR=smoke_caida pnpm run dev:seed`, con el servidor
  parado). La segunda: avance a Bronce (90 s), el guerrero de `smoke_runner` va a la ciudad, la asalta, el asaltado
  recibe `CITY_ASSAULTED` con la hora de la caída, a los 20 s cae (`city.fallen` a los dos, `CITY_TAKEN` y `CITY_LOST`)
  y `smoke_caida` recibe sin reconectar el snapshot de su ciudad refundada, con el mismo nombre y su Bronce, a 300
  tiles. 28 comprobaciones.
- **Fallo encontrado y arreglado.** En el navegador, `smoke_caida` veía el mundo vacío: la ciudad refundada entraba en
  la RAM sin civilización y el cliente descartaba el snapshot entero ([city-fall.md](../specs/city-fall.md) §9.1).
  Reproducido con un test de simulación y el e2e, arreglado, y repetida la pasada con el servidor nuevo y el navegador
  conectado como `smoke_caida`: al caer, el cliente pasó solo a la ciudad refundada (población 9/50: seis supervivientes
  y tres aldeanos nuevos), con el aviso «Tu ciudad cayó ante smoke_runnerpolis: la refundaste en (123, 180)» y su «Ir», y
  el registro con el asalto y la caída. 25 comprobaciones.
- **Sin comprobar en el navegador:** el botón «Asaltar» con sus motivos y la cuenta atrás del asalto en el HUD (el
  asalto del humo dura 20 s y lo da el humo, no un clic); lo cubren `src/state/cityFall.test.ts` y el e2e.
  **Comprobado en el repaso (2026-11-23)**, abajo.

### Repaso en el navegador (2026-11-23)

Servidor con `EO_CITY_ASSAULT_SECONDS=60` y `EO_CITY_FALL_IMMUNITY_HOURS=0`; la víctima, `smoke_caida` (Bronce, sin
muralla).

- **La víctima.** Con el navegador conectado como `smoke_caida`, `node scripts/smoke.mjs --web http://localhost:3001
  --caida` (25 comprobaciones): al plantarse el asalto el HUD mostró en rojo «¡Están asaltando tu ciudad! Cae en 1:00» y
  la cuenta bajó segundo a segundo; a la hora exacta de la caída la etiqueta desapareció y el HUD pasó, sin reconectar,
  a la ciudad refundada (población 9/50 → 12/50).
- **El atacante.** Con el navegador como `smoke_runner` y `smoke_caida` conectada por un script (para que su ciudad no
  pasara a protegida): con el guerrero seleccionado a 68 tiles de la ciudad no se ofrece «Asaltar»; al ordenarle ir
  hacia ella, el botón «Asaltar smoke_caidapolis» apareció **activo** al entrar en los 12 tiles, con su título. Un clic
  plantó el asalto: el guerrero pasó a `CLAIMING` en el anillo, el HUD mostró «Asaltas smoke_caidapolis: cae en 1:00» y
  el botón quedó desactivado con el motivo «Ya hay un asalto sobre esa ciudad». A los 60 s la ciudad cayó y la cuenta
  atrás y el botón desaparecieron; el aviso `CITY_TAKEN` llegó y el registro abre con «Tus unidades tomaron y arrasaron
  la ciudad de smoke_caidapolis cerca de (161, 116)».
- **Sin comprobar en el navegador:** los demás motivos del botón (muralla en pie, Edad de Piedra, inmunidad, ciudad
  protegida, propia protegida, paz de la Orden, tratado): siguen cubiertos sólo por `src/state/cityFall.test.ts`.
- **Observaciones, sin arreglo en este repaso:**
  - El título del botón dice «si aguanta 15 minutos»: es el plazo por defecto, fijo en el cliente; no refleja
    `EO_CITY_ASSAULT_SECONDS` si el servidor lo cambia.
  - A 1024 px de ancho, la etiqueta de la cuenta atrás del asaltado se parte en cinco líneas (~62 px de ancho).
  - El vencido que tiene la ciudad en su vista recibe `city.fallen` dos veces (la difusión al chunk y el envío directo
    al dueño, `simulation/assault.go`); el cliente lo ignora la segunda vez (`cities.delete` devuelve `false`).
  - La refundación reutiliza solares arrasados: `smoke_caida` alterna entre (123, 180) y (161, 116), a 64 tiles
    justos el uno del otro, el mínimo de la Q6.
  - Con la cámara lejos de la ciudad propia, el HUD no muestra la fila de la ciudad (ni «Registro») hasta volver con
    H; es así desde antes de E31.

## E32 — Bots que asaltan

**Estado: HECHO (2026-11-24)**, en `main` desde la fusión `0465eba`, con la CI en verde; queda la ejecución larga del
entregable 5 como medida. Contrato:
[SPEC-BOTS-ASSAULT](../specs/bots-assault.md), propuesta y aceptada el 2026-11-23 con sus diez decisiones de producto.
Sin ADR nuevo: no cambia el servidor, ni el protocolo, ni la base.

**Objetivo.** Medir la caída de E31 con bots: si un atacante con arietes llega a tumbar una ciudad defendida, si al
defensor le basta con reconstruir la muralla, y cuánto tarda el vencido en recuperarse.

**Alcance.** En `apps/bots`: el saqueador, desde Hierro, entrena arietes y forma una partida de asalto de 4 guerreros y
2 arietes que se reúne, abre la muralla, planta el estandarte y escolta; replanta si muere el portaestandarte y derriba
la muralla en obra; vuelve por las causas de spec §4.4 (`RN-BOT-014` a `RN-BOT-016`). El bot cuya ciudad cae actualiza
el directorio y sigue jugando desde la refundada (`RN-BOT-017`). Sucesos `assault`, `fell` y `wall` en el JSONL y su
resumen (caídas por hora, coste de cada caída, recuperación del vencido). **No entra** (spec §2): cambios en el
servidor, el protocolo o el cliente web; que el constructor asalte o se defienda de otra forma; tocar los números del
asalto, la inmunidad, el ariete o la muralla (los hallazgos van a otra spec, Q10).

### Entregables

1. **HECHO** — Aceptación: spec en `Accepted` con §10 como decisiones; `RN-BOT-014` a `RN-BOT-017`, con precisiones de
   §2, `RN-BOT-002` y `RN-BOT-004` en [bots.md](../specs/bots.md) y de §2 y Q10 en
   [city-fall.md](../specs/city-fall.md). Sin invariantes de dominio nuevos.
2. **HECHO** — Bots: los arietes en `militaryRule`, el blanco asaltable y la partida de asalto con su reunión, la
   brecha, el estandarte y lo de spec §4.4 (`RN-BOT-014` a `RN-BOT-016`), con sus tests (`apps/bots/src/assault.test.ts`).
3. **HECHO** — Bots: el vencido que sigue jugando y actualiza el directorio (`RN-BOT-017`), con sus tests
   (`bot.test.ts`), y el fallo de la búsqueda de colinas tras la caída (spec §9.1).
4. **HECHO** — Registro y resumen con los sucesos nuevos; el perfil con sus campos nuevos; docs
   (`docs/architecture/frontend.md`, `docs/operations/local-development.md`).
5. **EN PARTE** — Prueba en vivo (spec §4.7, Q5). **HECHA** la pasada corta con `EO_CITY_ASSAULT_SECONDS=120` y
   `EO_CITY_FALL_IMMUNITY_HOURS=0` (cinco pasadas, abajo), con los dos fallos de la marcha y de la vuelta que
   destapó (spec §9.1). **PENDIENTE** la ejecución larga de 3 h a velocidad real con las cuentas `_e29` (fuera de su
   inmunidad desde el 2026-11-25) y el saqueador sembrado a Hierro, anotada aquí como medida.

### Prueba en vivo (2026-11-23 y 2026-11-24)

Servidor y web de desarrollo en esta Mac, con `EO_CITY_ASSAULT_SECONDS=120` y `EO_CITY_FALL_IMMUNITY_HOURS=0`, y
`pnpm run bots -- --web http://localhost:3001 --constructores 3 --saqueadores 1 --sufijo e30`. Las cuentas `_e30`,
con el saqueador sembrado a Hierro (`EO_DEVSEED_STOCK_FOR`). Registros en `bots/runs/` (sin versionar), por la semilla
del nombre.

- **Pasada 1** (`--semilla 32`, 44 min 39 s): el saqueador pasa a Hierro a los 3 min 7 s y a Castillos a los 12 min
  18 s, y entrena 2 arietes y 4 berserkers. Planta 5 estandartes; dos asaltos se rompen al morir el portaestandarte
  (`holder_died`), y `_2_e30` cae dos veces (ciudades 26 y 34, refundada en la 35): **2,69 caídas por hora**. El
  vencido sigue jugando desde la refundada. Destapó los cuatro primeros fallos de spec §9.1 (los arietes que volvían
  desde el punto de reunión, `raid_end` y `raid` en el mismo despertar, `breached` sin muralla, el enfriamiento de la
  víctima de la Q8).
- **Pasada 2** (`--semilla 33`, 2 min 17 s): un asalto plantado al final de la pasada 1 siguió en el servidor y tumbó
  a los 0,8 min la ciudad de `_1_e30` (25, refundada en la 36), la que la partida nueva iba a saquear: la partida de
  saqueo cuyo blanco cae no volvía (spec §9.1).
- **Pasada 3** (`--semilla 34`, 21 min 2 s): sin asalto. La partida de saqueo hacia `_2_e30` llegó a su punto de
  reunión desplazado y, desde el minuto 3,8 hasta el final, repitió el mismo `raid:march` a sus tres militares: 753
  órdenes, que el servidor aceptaba con `movementId: 0` y el cliente viejo rechazaba con `INVALID_MESSAGE` (spec
  §9.1, la marcha y el esquema). `_2_e30` y `_3_e30` levantan su muralla.
- **Pasada 4** (`--semilla 35`, 35 min 39 s, cortada): con el arreglo de la marcha, la partida de 5 llega al mismo
  punto tras los mismos 26 rechazos, se reúne en 26 s y saquea 139 de madera. Al volver, el primer tramo acabó fuera
  de la vista de las dos conexiones y la partida no terminó en los 29 min que quedaban (spec §9.1, la vuelta).
- **Pasada 5** (`--semilla 35`, 37 min 28 s, cortada con la partida siguiente aún en marcha): dos partidas de saqueo
  contra `_2_e30` (3 y 6 militares; vuelven todos salvo uno), con la vuelta por tramos fuera de la vista ya
  resuelta. Entrena los 2 arietes en 45 s cada uno y a los 14,7 min sale **la partida de asalto** (4 militares y 2
  arietes) contra `_2_e30`, amurallada: reunida en 1 min 18 s (minuto 24,5), la muralla abierta por los
  arietes 69 s después (`wall breached` del defensor y `breached` del saqueador, que la había visto en pie), el
  estandarte plantado en el acto (`suffered` del defensor) y **la caída a los 120 s** (minuto 27,7), 12 min 56 s
  después de salir. `_2_e30` refunda en la 37, en 134,213, con 22 de población y en Bronce, y sigue recolectando y
  comerciando. La partida vuelve entera (minuto 36,8), sin perder ningún ariete, y la siguiente sale contra `_1_e30`, a
  321 tiles, y no contra la refundada de `_2_e30`, a 203: el enfriamiento de la víctima de la Q8. **1 caída en 37 min
  (1,60 por hora).**
- **Hallazgos para otra spec (Q10):** el punto de reunión hacia `_2_e30` no se pisa y la marcha gasta 26 rechazos
  `TARGET_NOT_WALKABLE` cada vez (78 en la pasada 5): `rallyPoint` da por bueno el terreno que no conoce. Los
  militares que una ejecución deja fuera de la vista no los recupera la siguiente (spec §9.1): quedan doce militares y
  dos arietes así, y por eso la pasada 5 entrenó un tercer ariete. **Precisión (2026-11-24), repaso de la vista**
  (rama `claude/repaso-bots-vista`): el punto de reunión que la vista descubre que no se pisa se vuelve a elegir con
  el terreno conocido y sus rechazos empiezan de cero, y `militaryRule` cuenta también los militares y arietes de la
  partida en curso que no ve (spec §9.1). Los de una ejecución anterior siguen sin recuperarse: el protocolo no manda
  las unidades propias fuera del área de interés.
- **Pasada 6** (`--semilla 36`, 19 min 58 s, con el repaso de la vista): el saqueador no tiene **ningún rechazo** (78
  `TARGET_NOT_WALKABLE` en la pasada 5) y sólo entrena aldeanos: ni militares ni arietes de más. Una partida de saqueo
  de 3 contra `_2_e30` (reunida en 28 s, 105 de madera) y la de asalto contra `_2_e30`, reunida en 1 min 22 s, que la
  tumba a los 8 min 11 s de salir; la siguiente, contra `_1_e30`, no llega a reunirse antes del final. **1 caída en
  20 min (3,01 por hora).**
- **Sin comprobar en el navegador:** el asalto entre bots visto desde una cuenta humana (spec §4.7, parte 3): el panel
  del navegador integrado estuvo oculto en las cinco pasadas y, sin él, el mapa no se dibuja.

## E33 — El asalto en el mapa

**Estado: HECHO (2026-11-24)**, en `main` desde la fusión `1bebacd`, con la CI en verde. Contrato:
[SPEC-ASSAULT-MAP](../specs/assault-map.md), propuesta y aceptada el 2026-11-24 con sus ocho decisiones de producto.
Sin ADR nuevo: no cambia el servidor, ni el protocolo, ni la base.

**Objetivo.** Que el asalto de una ciudad se vea en el mapa: hoy un tercero sin una unidad cerca sólo ve la ciudad
desaparecer.

**Alcance.** En `apps/web`: el estandarte sobre toda unidad en `CLAIMING` (asalto y reclamación de territorio), la
ciudad bajo asalto con su contorno y la cuenta atrás bajo el nombre para todo el que la ve, el tono según quién mira y
el desvanecido al caer (`RN-SIEGE-012` a `RN-SIEGE-014`). **No entra** (spec §2): el servidor, el protocolo, la base, las
ruinas, el HUD, los bots y el humo.

### Entregables

1. **HECHO** — Aceptación: spec en `Accepted` con §10 como decisiones; `RN-SIEGE-012` a `RN-SIEGE-014`; precisiones
   en [city-fall.md](../specs/city-fall.md) y [bots-assault.md](../specs/bots-assault.md). Sin invariantes nuevos.
2. **HECHO** — Estado: `assaultTone`, `standardTone` y `assaultCountdownText` en `src/state/cityFall.ts`, la
   proyección de `SceneCity.assault` y `SceneUnit.standard`, y `fallenCities` en `WorldChanges` y `SceneUpdate`, con
   sus tests.
3. **HECHO** — Escena: el estandarte, el contorno, la cuenta atrás con «Cayendo…» y el desvanecido de 1 s.
4. **HECHO** — Docs (`docs/architecture/frontend.md` §4 y §7.3, spec §9.1) y prueba en vivo en el navegador.

### Prueba en vivo (2026-11-24)

Servidor y web de desarrollo con `EO_CITY_ASSAULT_SECONDS=120` y `EO_CITY_FALL_IMMUNITY_HOURS=0`; el navegador
integrado, con el panel visible, en la cuenta `smoke_caida`, y `node scripts/smoke.mjs --web http://localhost:3001
--caida` (25 comprobaciones superadas).

- **El defensor** ve, en cuanto el guerrero de `smoke_runner` planta el estandarte, el banderín rojo sobre él, el
  contorno rojo de su ciudad y «Cae en 1:59» en rojo, con el nombre una línea más arriba. La cuenta atrás baja segundo a
  segundo (1:51, 1:01, 0:16).
- **Al caer** (`city.fallen 33`), la ciudad se desvanece sin contorno ni cuenta atrás, el guerrero deja el estandarte y,
  con el snapshot de la refundada (38, en 161,116), la cámara se queda donde estaba, con el aviso «Tu ciudad cayó…».
- **Sin comprobar en el navegador:** «Cayendo…» (la caída llegó dentro del mismo segundo en que la cuenta llegó a
  cero), el tono naranja de un tercero, el dorado del asaltante y el estandarte de una reclamación de territorio. Los
  cubren los tests de `cityFall.test.ts` y `projection.test.ts`.

## E34 — Multiciudad

**Estado: HECHO (2026-11-25)**, en `main` desde la fusión `0e5ea92`, con la CI en verde. Contrato:
[SPEC-MULTI-CITY](../specs/multi-city.md), propuesta y aceptada el 2026-11-24 con sus once decisiones (producto delegó en
la recomendación del agente, sesión desatendida). [ADR-051](../decisions/ADR-051-multi-city-capital.md); migración
`000031`.

**Objetivo.** Que un jugador pueda tener una segunda ciudad: hoy crecer es sólo subir de era, y la caída lo devuelve
todo a cero.

**Alcance.** Fundar con un aldeano (`unit.found_city`) desde Bronce por 800/400, con un tope de dos en pie garantizado
por la base; la capital (la de id menor) responde por el jugador; presencia de todas a la vez; el bono de territorio
sólo en la capital; la caída de una de dos sin refundar; «Fundar ciudad» y «H» que alterna en el HUD; el humo
`--multiciudad` (`RN-CITY-030` a `RN-CITY-035`). **No entra** (spec §2): conquistar ciudades, más de dos, elegir de
qué ciudad sale el comercio, trasladar la capital, que los bots funden.

### Entregables

1. **HECHO** — Aceptación: spec en `Accepted` con §10 como decisiones; ADR-051; `INV-CITY-022` e `INV-CITY-023`
   registrados sin cobertura; precisiones de `INV-CITY-020`, [city.md](../specs/city.md) §9 y
   [city-fall.md](../specs/city-fall.md) §4.2.
2. **HECHO** — Servidor: la migración `000031` (`cities.slot`, el índice por dueño y hueco), la capital en `State`
   (`CityOf`, `CitiesOf`), la presencia de todas a la vez, el bono sólo en la capital (también en la validación de
   carga), `unit.found_city` con su reserva y el trabajo `city.found` (`founding.CheckSite`, `CityFallRepo.FoundCity`),
   y la caída con dos sin refundar, con sus tests (`multi_city_test.go`, `multi_city_integration_test.go`).
3. **HECHO** — Protocolo: `unit.found_city` en Zod y en Go, `CITY_FOUNDED` en el registro, con su e2e
   (`multi_city_e2e_test.go`). Sin mensaje de confirmación: un snapshot nuevo (spec §9.1).
4. **HECHO** — Cliente y bots: «Fundar ciudad» con su motivo, «H» que alterna y el panel de la ciudad elegida
   (`src/state/multiCity.ts`), y la capital en los bots (`capitalOf`).
5. **HECHO** — Humo `--multiciudad` (con `sitioParaFundar` y su test), docs (protocolo, registro, base, cliente,
   configuración) y prueba en vivo.

### Prueba en vivo (2026-11-24)

Servidor y web de desarrollo en esta Mac; el servidor aplicó la migración 30 → 31 al arrancar sobre el mundo de
desarrollo (29 ciudades). `node scripts/smoke.mjs --web http://localhost:3001 --multiciudad`, con las cuentas
sembradas con `EO_DEVSEED_STOCK_FOR`.

- **`smoke_multi`** avanza a Bronce y funda su segunda ciudad, `smoke_multipolis II` (41), en (256, 383).
- **`smoke_multi2`** (`EO_SMOKE_MULTI_USER`) avanza a Bronce, busca sitio moviendo la vista, lleva un aldeano a
  (108, 375), a 81 tiles de su capital, y funda `smoke_multi2polis II` (43) en Bronce; la tercera se rechaza con
  `INVALID_TARGET` («ya tienes dos ciudades»). En la pasada siguiente de `smoke_multi`, ya con dos, la sonda se rechaza
  por el tope.
- **Encontrado y arreglado en vivo** (spec §9.1): en el mundo denso el sitio libre más cercano quedó a 80 tiles de la
  capital, y el snapshot de la fundación, centrado en la capital, no traía la ciudad nueva: ahora va centrado en ella
  (`ResnapshotAt`), y el cliente recuerda los centros de sus ciudades (`ownCityHomes`) para que «H» llegue a la que no
  está a la vista. El humo, además, busca el sitio alrededor de la capital y camina por tramos que aceptan
  `movementId: 0`.
- **Sin comprobar en el navegador:** «Fundar ciudad», «H» que alterna y el panel de la ciudad elegida: el panel del
  navegador integrado estaba oculto y, sin él, el mapa no se dibuja. Los cubren los tests de `multiCity.test.ts` y
  `selectors.test.ts`.

## E35 — Bots que fundan

**Estado: EN CURSO (2026-11-25)**, en la rama `claude/slice-bots-fundan`. Contrato:
[SPEC-BOTS-FOUND](../specs/bots-found.md), propuesta y aceptada el 2026-11-25 con sus nueve decisiones (producto delegó en
la recomendación del agente, sesión desatendida). Sin ADR nuevo: no cambia el servidor, ni el protocolo, ni la base.

**Objetivo.** Medir la fundación de E34 con bots: cuándo funda un jugador, cuánto tarda la segunda ciudad en crecer y
qué pasa si cae una de las dos.

**Alcance.** En `apps/bots`: el constructor elige un sitio, lleva un aldeano y funda en cuanto su capital está en
Bronce con 800/400, con lo que cuesta apartado; la segunda ciudad juega con su memoria y el perfil de colonia
(aldeanos, casas, madera) y una conexión propia; la caída de una de dos (`RN-BOT-018` a `RN-BOT-021`). Sucesos
`found` y `colony` y `fell` con `kept`, y su resumen. **No entra** (spec §2): el servidor, el protocolo, el cliente
web, que el saqueador funde, que la segunda avance o comercie, que sea blanco de otros bots.

### Entregables

1. **HECHO** — Aceptación: spec en `Accepted` con §10 como decisiones; `RN-BOT-018` a `RN-BOT-021`, con precisiones de
   `RN-BOT-003` en [bots.md](../specs/bots.md) y de la Q10 en [multi-city.md](../specs/multi-city.md). Sin invariantes.
2. **HECHO** — Bots: el sitio, la fundación y la segunda ciudad con su perfil de colonia, con sus tests
   (`apps/bots/src/found.test.ts`).
3. **HECHO** — Bots: la conexión colonia, la casa de vuelta en la capital y la caída de una de dos, con sus tests
   (`bot.test.ts`).
4. **HECHO** — Registro y resumen (`found`, `colony`, `fell` con `kept`; `log.test.ts`); docs
   (`docs/architecture/frontend.md`, `docs/operations/local-development.md`, spec §9.1).
5. **HECHO** — Prueba en vivo (spec Q9), abajo.

### Prueba en vivo (2026-11-25)

Servidor y web de desarrollo en esta Mac, con `EO_CITY_ASSAULT_SECONDS=120` y `EO_CITY_FALL_IMMUNITY_HOURS=0`, y
`pnpm run bots -- --web http://localhost:3001 --constructores 3 --saqueadores 1 --sufijo e35`. Cuentas nuevas `_e35`,
dadas de alta con una pasada de 1 min y sembradas en Piedra con `EO_DEVSEED_STOCK_FOR` (3 300 de madera y 2 200 de
piedra). Registros en `bots/runs/` (sin versionar).

- **Pasada 1** (`--semilla 35`, 12 min, cortada para el arreglo): los tres constructores llegan a Bronce a los
  3 min 40 s–3 min 52 s. `_3_e35` sale a fundar en el mismo despertar a (112, 192), a 47 tiles, y **funda la ciudad 48
  a los 62 s**; la segunda llega a 8 de población 2 min 7 s después. `_1_e35` y `_2_e35` **no encuentran sitio** a 56
  tiles o menos y pasan a Hierro sin fundar (spec §9.1: el anillo pasa a 96).
- **Pasada 2** (`--semilla 36`, 35 min, con el anillo de 96; las cuentas siguen desde la pasada 1, ya en Hierro):
  `_2_e35` funda la 49 en (3, 316), a 60 tiles, en 60 s (8 de población en 2 min 59 s). `_1_e35` no funda: gastó la
  piedra en Hierro y no vuelve a juntar 400 (la medida de Q2). `_3_e35` arranca con dos ciudades y juega las dos.
  El saqueador, en Castillos a los 6 min 52 s, tumba con arietes **tres capitales con la otra ciudad en pie**: la de
  `_2_e35` (minuto 4,1 de su partida), la de `_3_e35` (12 min 37 s) y, después, la 49 de `_2_e35`. En cada una el
  vencido sigue desde la otra, que pasa a capital sin refundar (`fell` con `kept`), y pone al día el directorio. Tras
  perder la primera capital, `_2_e35` **vuelve a fundar** (Q8): la 50 en (41, 353), a 38 tiles, en 14 s, a los 32 min.
  **3 caídas en 35 min (5,14 por hora)**; ningún rechazo de `unit.found_city`.
- **Hallazgos para otra spec:** la segunda ciudad no levanta casas en Bronce (límite 50, spec §9.1); un constructor en
  Hierro rara vez vuelve a tener 400 de piedra para fundar (la investigación y la muralla se la llevan); con la
  inmunidad a 0, el saqueador encadena caídas de capitales recién heredadas.
- **Sin comprobar en el navegador:** ver una segunda ciudad de un bot en el mapa; el panel del navegador integrado no se
  usó en esta sesión desatendida.
