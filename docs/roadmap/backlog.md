# Backlog

Backlog priorizado y accionable del vertical slice M0–M7, más la deuda técnica aceptada conscientemente y las preguntas de diseño de juego todavía abiertas.

## Convenciones

**Prioridad.** `P0` bloquea el vertical slice: sin ello no se llega a M7. `P1` es necesario para
cerrar su milestone con la Definition of Done completa. `P2` es deseable y mejora calidad u
operabilidad sin bloquear. `P3` está explícitamente diferido a después del MVP.

**Tamaño.** Estimación orientativa de esfuerzo, no un compromiso de calendario: `S` menos de un día,
`M` dos o tres días, `L` alrededor de una semana, `XL` más de una semana o con incertidumbre
técnica relevante. Estos valores son estimaciones del equipo, no valores del canon.

**Dependencias.** Se listan solo las duras dentro del backlog. Las dependencias entre milestones
están en [milestones.md](milestones.md).

**Criterio de aceptación.** Una línea verificable. Si no se puede comprobar en un test, un comando o
una consulta, el item está mal escrito y hay que reformularlo antes de empezarlo.

## Estado real de los bloques

El grueso de M0 a M5 del lado servidor **ya está implementado y con tests en verde**. Este backlog sigue
siendo el registro de lo comprometido, pero conviene leerlo sabiendo qué queda realmente abierto:

| Bloque | Situación |
|---|---|
| M0 (EO-001 … EO-019) | Completado **salvo `EO-016` (CI)**, que no está empezado. |
| M1 (EO-020 … EO-028) | Completado salvo `EO-028` (volcado del mundo a fichero, `P2`). |
| M2 (EO-030 … EO-039) | Completado. `EO-038` se resolvió con `internal/game/founding`. |
| M3 (EO-040 … EO-057) | Servidor completado. Abiertos: **`EO-041`** (ticket desde Next.js) y **`EO-055`** (cliente PixiJS). |
| M4 (EO-060 … EO-078) | Servidor completado. Abierto: **`EO-075`** (interpolación en el cliente). |
| M5 (EO-080 … EO-089) | Presencia y protección completadas. Abiertos: `EO-084`, `EO-085`, `EO-086` (safe zones y `HIDDEN`). |
| M6 (EO-090 … EO-097) | Sólo `EO-090` (migración). El resto sin empezar. |
| M7 (EO-100 … EO-108) | Sólo `EO-100` (migración). El resto sin empezar. |
| Transversales | `EO-110` y `EO-112` hechos. Abiertos: `EO-111`, `EO-113`, `EO-114`, `EO-115`, `EO-116`. |

Y un recordatorio que atraviesa todo lo anterior: **los tests de integración están escritos pero no
ejecutados** (`DEBT-19`), así que ningún item que sólo verifique la integración puede darse por cerrado.

---

## M0 — Foundation

| ID | Título | Milestone | Prioridad | Tamaño | Dependencias | Criterio de aceptación |
|---|---|---|---|---|---|---|
| EO-001 | Instalar Go y documentar prerrequisitos del entorno | M0 | P0 | S | — | `go version` reporta 1.23 o superior y el procedimiento (`winget install --id GoLang.Go`) queda escrito en la guía de arranque. **Cerrado**: hay Go 1.27.0 instalado; `go.mod` declara `go 1.23` como mínimo. |
| EO-002 | Crear la estructura del monorepo | M0 | P0 | S | — | El árbol de directorios coincide exactamente con el declarado en el canon, incluidos `infra/docker/`, `scripts/` y `docs/`. |
| EO-003 | `pnpm-workspace.yaml` y scripts de tarea en la raíz | M0 | P0 | S | EO-002 | `pnpm install` completa y `pnpm run` lista `db:up`, `db:psql`, `server:run`, `server:test`, `protocol:build`, `lint`, `typecheck`, `test` y `verify`. |
| EO-004 | `docker-compose.yml` con Postgres y Redis | M0 | P0 | M | EO-002 | `pnpm run db:up` deja ambos servicios en estado *healthy* según `docker compose ps`. |
| EO-005 | Inicializar el módulo Go y el árbol `internal/` | M0 | P0 | M | EO-001, EO-002 | `go build ./...` compila con el module path `github.com/empires-online/empires-online/services/game-server`. |
| EO-006 | `internal/config` con todas las variables `EO_` y sus defaults | M0 | P0 | M | EO-005 | Arrancar sin `EO_AUTH_JWT_SECRET` aborta el proceso nombrando la variable —junto al resto de errores, que se reportan todos juntos—, los defaults coinciden con el canon e incluyen `EO_WS_OUTBOUND_QUEUE_SIZE` (256), y se aplican las validaciones cruzadas de tick, chunk, presencia, rate limit y secreto. |
| EO-007 | Logging estructurado JSON con `log/slog` | M0 | P0 | S | EO-005 | Toda línea de log incluye `ts`, `level` y `msg`, y admite `player_id`, `session_id`, `request_id` y `tick`. |
| EO-008 | Registro Prometheus y servidor de métricas | M0 | P0 | M | EO-007 | `GET /metrics` en `EO_METRICS_ADDR` (`:9090`) expone las catorce métricas `eo_*` declaradas, aunque valgan cero. |
| EO-009 | Endpoints `GET /health` y `GET /ready` | M0 | P0 | S | EO-006, EO-008 | `/health` responde 200 sin tocar dependencias; `/ready` responde no-200 si Postgres o Redis están caídos. |
| EO-010 | Abstracciones `Clock` y `RandomSource` con `FakeClock` | M0 | P0 | S | EO-005 | Un test avanza el `FakeClock` y obtiene resultados idénticos en ejecuciones repetidas. |
| EO-011 | Migraciones embebidas con golang-migrate y ledger `schema_migrations` | M0 | P0 | M | EO-004, EO-005 | El arranque del servidor aplica en orden los `.sql` embebidos (`//go:embed`), es idempotente y cada `.down.sql` revierte su `.up.sql`. La URL se reescribe al esquema `pgx5`. |
| EO-012 | Migración inicial: trigger de `updated_at` y tabla `world_state` | M0 | P0 | S | EO-011 | `world_state` existe con `epoch_ms` y el trigger actualiza `updated_at` en cada `UPDATE`. |
| EO-013 | `@empires-online/protocol`: envelopes Zod y códigos de error | M0 | P0 | M | EO-003 | Los esquemas aceptan los envelopes canónicos y rechazan `v` distinto de 1 y `requestId` no UUIDv4. |
| EO-014 | Exportación de JSON Schema en el build del protocolo | M0 | P0 | S | EO-013 | `pnpm run protocol:build` regenera `packages/protocol/schema/v1/*.json` sin diferencias respecto a lo commiteado, y `pnpm run protocol:check` detecta la deriva. |
| EO-015 | `go:embed` de los JSON Schema en el Game Server | M0 | P0 | S | EO-005, EO-014 | El binario embebe los esquemas y los expone a los contract tests sin leer del disco. |
| EO-016 | CI en GitHub Actions con la secuencia completa | M0 | P0 | L | EO-004, EO-005, EO-013 | **Abierto y bloqueante.** Único entregable de M0 sin empezar. El workflow debe ejecutar `format → lint → typecheck → unit → integration → build → docker build` y fallar el PR si cualquier check está en rojo. Es además la vía más rápida para cerrar `DEBT-19`, porque levanta Postgres y Redis como *services*. |
| EO-017 | Lint que prohíbe `time.Now()` y `rand` en el dominio | M0 | P1 | S | EO-005, EO-010 | Un uso directo de `time.Now()` dentro de `internal/domain` hace fallar `pnpm run lint`. |
| EO-018 | Guía de arranque con el entorno real documentado | M0 | P0 | S | EO-002 | La guía nombra Windows 10, Node v22.17.1, pnpm 10.25.0, git 2.38.1, Docker CLI 20.10.22 con Compose v2.15.1, Go 1.27.0 instalado, y la ausencia de `psql`, `redis-cli`, `make` y `gh`. |
| EO-019 | Acceso a Postgres y Redis vía `docker compose exec` documentado | M0 | P1 | S | EO-004 | `pnpm run db:psql` y `pnpm run db:redis` abren una sesión contra el contenedor sin requerir `psql` ni `redis-cli` en el host. |

## M1 — World

| ID | Título | Milestone | Prioridad | Tamaño | Dependencias | Criterio de aceptación |
|---|---|---|---|---|---|---|
| EO-020 | Enum `TerrainType` y tabla de `walkable` / `costUnits` | M1 | P0 | S | EO-005 | Los seis terrenos tienen sus `costUnits` canónicos en décimas del base (10, 16, 18, —, —, 6), `MOUNTAIN` y `WATER` son no transitables, y `MinTerrainCostUnits` vale 6. |
| EO-021 | Transformaciones de coordenadas tile ↔ chunk | M1 | P0 | S | EO-020 | (511, 511) mapea a chunk (15, 15) con ID 255, y la inversa devuelve el rango correcto. |
| EO-022 | Generador determinista del mundo desde `EO_WORLD_SEED` | M1 | P0 | L | EO-010, EO-020 | Dos generaciones con la misma seed producen los 256 chunks con hash idéntico. |
| EO-023 | Migración `world_chunks` y persistencia como `bytea` de 1024 bytes | M1 | P0 | M | EO-011, EO-022 | `SELECT count(*)` devuelve 256 y toda fila cumple `length(data) = 1024`. |
| EO-024 | Bootstrap del mundo al arrancar desde la semilla | M1 | P0 | M | EO-023 | El mundo se regenera desde `EO_WORLD_SEED` en cada arranque y coincide byte a byte con la copia de `world_chunks`, que existe para auditoría y edición futura, no como fuente primaria. |
| EO-025 | Capa de ocupación (`blocked overlay`) separada del terreno | M1 | P0 | M | EO-020 | Bloquear un tile no altera su `TerrainType` en `world_chunks`. |
| EO-026 | Transitabilidad de 8 direcciones sin corner cutting | M1 | P0 | M | EO-025 | Una diagonal con cualquiera de sus dos ortogonales bloqueado se declara no transitable. |
| EO-027 | Validación de límites del mundo | M1 | P0 | S | EO-021 | Coordenadas negativas o ≥ `EO_WORLD_WIDTH`/`EO_WORLD_HEIGHT` producen un error de dominio mapeable a `TARGET_OUT_OF_BOUNDS`. |
| EO-028 | Volcado del mundo a fichero para inspección manual | M1 | P2 | S | EO-024 | Un script de `scripts/` produce una representación legible de un chunk para depuración. |

## M2 — Player & City

| ID | Título | Milestone | Prioridad | Tamaño | Dependencias | Criterio de aceptación |
|---|---|---|---|---|---|---|
| EO-030 | Migración y seed de `civilizations`, `factions` y `eras` | M2 | P0 | M | EO-011 | Existen exactamente `ORDER`, `CHAOS` y `NEUTRAL`, y las cuatro eras con `population_cap` 20, 50, 100 y 150. |
| EO-031 | Migración de `players`, `cities` y `units` | M2 | P0 | M | EO-030 | `players.id` es `uuid`, el resto de PK son `bigint GENERATED ALWAYS AS IDENTITY` y los enums son `text` + `CHECK`. |
| EO-032 | Migración de `world_events` como registro append-only | M2 | P1 | S | EO-011 | Un evento de dominio insertado es consultable y nunca se actualiza en sitio. |
| EO-033 | Dominio `player` con Civilization y Faction ortogonales | M2 | P0 | M | EO-031 | Cualquier combinación de civilización y facción es válida y ninguna implica la otra. |
| EO-034 | Dominio `city` con `TOWN_CENTER` y zona urbana amurallada | M2 | P0 | M | EO-025, EO-031 | La ciudad marca su ocupación en el `blocked overlay` y `TOWN_CENTER` se modela como building, no como unidad. |
| EO-035 | Dominio `unit` con el tipo `VILLAGER` | M2 | P0 | S | EO-031 | Un `VILLAGER` nace con hp 40, `baseMsPerTile` 600 y `status = 'IDLE'`. |
| EO-036 | Bootstrap atómico de jugador, ciudad y 3 aldeanos | M2 | P0 | L | EO-033, EO-034, EO-035 | Un fallo inyectado antes del commit deja `players`, `cities` y `units` exactamente como estaban. |
| EO-037 | Cálculo de `population_limit` y población actual | M2 | P1 | S | EO-030 | Un jugador en `STONE_AGE` con 3 aldeanos reporta 3 sobre 20. |
| EO-038 | Política de colocación inicial de la ciudad | M2 | P1 | M | EO-024, EO-034 | **Cerrado** por `internal/game/founding`: búsqueda determinista en espiral desde una semilla derivada del nombre de usuario, entorno despejado de radio 3, separación mínima de 24 tiles entre centros, muralla 3×3 en el `blocked overlay` y 3 aldeanos a radio 2. |
| EO-039 | Errores `CITY_NOT_FOUND` y `POPULATION_LIMIT_REACHED` | M2 | P2 | S | EO-036 | Ambos códigos están definidos, se devuelven en su caso y tienen test. |

## M3 — Realtime

| ID | Título | Milestone | Prioridad | Tamaño | Dependencias | Criterio de aceptación |
|---|---|---|---|---|---|---|
| EO-040 | Migración de `sessions` e `idempotency_keys` | M3 | P0 | S | EO-011 | Ambas tablas existen y una clave de idempotencia duplicada es rechazada por restricción. |
| EO-041 | API route de Next.js que emite el game ticket | M3 | P0 | M | EO-003, EO-055 | **Abierto.** Devuelve un JWT HS256 con TTL 60 s y claims `sub`, `jti`, `iat`, `exp` y `aud: "game-server"`. Hoy lo emite el game server desde `POST /api/auth/register` y `POST /api/auth/login`: ver `DEBT-18`. |
| EO-042 | Verificación del ticket y consumo del `jti` en Redis | M3 | P0 | M | EO-006, EO-041 | Un `jti` reutilizado dentro de los 120 s de TTL cierra la conexión con `4401`. |
| EO-043 | Endpoint `/ws` y handshake `session.hello` | M3 | P0 | M | EO-042 | No enviar `session.hello` en 5 segundos produce cierre `4408`. |
| EO-044 | Envelopes del protocolo con `seq` monótono y `ts` del servidor | M3 | P0 | M | EO-013, EO-043 | `seq` es estrictamente creciente en toda la vida de una conexión. |
| EO-045 | `session.ping` / `session.pong` y keepalive | M3 | P1 | S | EO-044 | El servidor envía ping cada 15 s y cierra la conexión tras 45 s sin lectura. |
| EO-046 | Rate limiting y límite de tamaño de mensaje | M3 | P0 | M | EO-044 | Un mensaje de 16385 bytes produce `MESSAGE_TOO_LARGE` y una ráfaga por encima del burst produce `RATE_LIMITED`. |
| EO-047 | Idempotencia por `requestId` en Redis y en `idempotency_keys` | M3 | P0 | M | EO-040, EO-044 | Repetir un `requestId` devuelve la respuesta original y no produce un segundo efecto. |
| EO-048 | Interest management por chunks con radio configurable | M3 | P0 | L | EO-021, EO-044 | El conjunto suscrito es el área de 5×5 chunks alrededor del centro, correctamente recortada en los bordes del mundo. |
| EO-049 | `world.snapshot` al conectar centrado en la ciudad | M3 | P0 | M | EO-036, EO-048 | El snapshot inicial contiene la ciudad del jugador y sus tres aldeanos, y nada fuera del área de interés. |
| EO-050 | `entity.spawn`, `entity.update` y `entity.despawn` | M3 | P0 | M | EO-048 | Un cambio de suscripción produce exactamente los spawns y despawns esperados, sin duplicados ni huecos. |
| EO-051 | Mensaje `city.update` | M3 | P1 | S | EO-050 | Un cambio de estado de la ciudad llega a los suscriptores del chunk correspondiente. |
| EO-052 | `session.view` con rate limit | M3 | P0 | M | EO-048 | Mover la vista actualiza el centro y un abuso de `session.view` produce `RATE_LIMITED`. |
| EO-053 | Códigos de cierre WS `4400`/`4401`/`4403`/`4408`/`4429`/`4500` | M3 | P0 | S | EO-043 | Cada condición produce su código exacto y ninguna produce `4500` por error de programación. |
| EO-054 | Reconexión con snapshot nuevo | M3 | P0 | M | EO-049 | Tras una caída de red, el cliente reconecta y recibe un estado coherente sin intervención manual. |
| EO-055 | Cliente: transporte WS, store y render isométrico en PixiJS | M3 | P0 | L | EO-044 | **Hecho.** `apps/web/` existe con su escena PixiJS. La ciudad y los aldeanos se dibujan con `screenX = (x - y) * 32` y `screenY = (x + y) * 16`. |
| EO-056 | Contract tests contra el JSON Schema exportado | M3 | P0 | M | EO-015, EO-050 | Todo mensaje servidor→cliente emitido en los tests valida contra su esquema. |
| EO-057 | Métricas de conexión y mensajes | M3 | P1 | S | EO-008, EO-044 | `eo_connected_players`, `eo_connected_websockets` y `eo_ws_messages_total` reflejan la actividad real. |

## M4 — Movement

| ID | Título | Milestone | Prioridad | Tamaño | Dependencias | Criterio de aceptación |
|---|---|---|---|---|---|---|
| EO-060 | Migración de `unit_movements` | M4 | P0 | M | EO-011 | La tabla soporta los estados `ACTIVE`, `COMPLETED`, `CANCELLED` y `FAILED`, y guarda `start_time_ms` y `arrival_time_ms`. |
| EO-061 | A\* octile determinista con desempate estable | M4 | P0 | XL | EO-026 | Cien ejecuciones sobre el mismo mapa devuelven exactamente el mismo path, con desempate `(f, h, y, x)`, escalas `costScaleOrtho = 1000` / `costScaleDiag = 1414` y heurística ponderada por `MinTerrainCostUnits` (6). |
| EO-062 | Interfaz `Pathfinder` estable | M4 | P0 | S | EO-061 | `FindPath(ctx, grid, from, to, opts) ([]world.Tile, error)`: sustituir la implementación no obliga a tocar el protocolo ni el dominio. La ruta devuelta incluye el tile de origen y `from == to` da una ruta de un tile. |
| EO-063 | Límites de pathfinding y errores asociados | M4 | P0 | S | EO-061 | Superar 20000 nodos o 256 tiles devuelve `PATH_TOO_LONG`; sin ruta devuelve `PATH_NOT_FOUND`. |
| EO-064 | Construcción de la polilínea temporizada | M4 | P0 | L | EO-035, EO-061 | El ejemplo canónico `(0,0) → (1,0) → (2,1) → (3,1) → (4,1)` produce `tMs` exactamente 0, 600, 1449, 2409, 2769, con redondeo al ms más cercano por segmento antes de acumular. |
| EO-065 | Posición autoritativa analítica en un instante T | M4 | P0 | M | EO-064 | La posición se obtiene sin replay de ticks, como el último waypoint con `tMs <= (T - start_time_ms)`. |
| EO-066 | Game loop a 10 Hz con las ocho fases en orden fijo | M4 | P0 | XL | EO-010, EO-024 | Las fases se ejecutan siempre en el orden canónico y `eo_game_tick_overruns_total` se incrementa si un tick excede su período. |
| EO-067 | Cola de persistencia asíncrona con workers | M4 | P0 | L | EO-066 | Ninguna operación de Postgres ocurre dentro del tick y `eo_persistence_queue_depth` está instrumentada. |
| EO-068 | Comando `unit.move` con validación en el orden canónico | M4 | P0 | L | EO-047, EO-062 | La validación sigue ownership → estado → destino → A\*, y cada fallo devuelve su código específico. |
| EO-069 | Comando `unit.cancel_move` | M4 | P0 | M | EO-068 | El movimiento queda `CANCELLED` y la unidad `IDLE` en el último waypoint alcanzado. |
| EO-070 | Mensajes `unit.move.accepted` y `unit.move.rejected` | M4 | P0 | S | EO-068 | Todo `unit.move` produce exactamente uno de los dos, con el `requestId` original. |
| EO-071 | Mensajes `unit.movement.started`, `completed` y `cancelled` | M4 | P0 | M | EO-050, EO-068 | Los tres se difunden a los suscriptores de los chunks afectados. |
| EO-072 | Eventos de dominio persistidos en `world_events` | M4 | P1 | M | EO-032, EO-071 | `UnitMovementStarted` y `UnitMovementCompleted` quedan registrados y son la base de los deltas. |
| EO-073 | Recuperación de movimientos `ACTIVE` al arrancar | M4 | P0 | L | EO-060, EO-064 | Si `arrival_time_ms <= now` la unidad hace snap al tile final y el movimiento se cierra; si no, se reanuda. |
| EO-074 | Dirty-flag y flush cada `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` | M4 | P0 | M | EO-067 | Las posiciones consolidadas se escriben cada 50 ticks y nunca en cada tick. |
| EO-075 | Cliente: interpolación visual entre waypoints | M4 | P0 | M | EO-055, EO-071 | **Abierto.** Depende de `EO-055`. La unidad se desplaza de forma continua y se corrige hacia la verdad del servidor al recibir un delta. |
| EO-076 | Métricas de pathfinding y de tick | M4 | P1 | S | EO-008, EO-066 | `eo_pathfinding_requests_total`, `eo_pathfinding_duration_seconds` y `eo_game_tick_duration_seconds` publican datos reales. |
| EO-077 | Tests de simulación con `FakeClock` | M4 | P0 | M | EO-066 | Avanzar 10 segundos deja la unidad en el tile exacto esperado, de forma reproducible. |
| EO-078 | Garantía de un único movimiento `ACTIVE` por unidad | M4 | P0 | M | EO-060, EO-068 | Una nueva orden cancela la anterior en la misma transacción y el índice único parcial `unit_movements_one_active_per_unit` (sobre `unit_id WHERE status = 'ACTIVE'`) lo impide en base. |

## M5 — Offline protection & Safe Zones

| ID | Título | Milestone | Prioridad | Tamaño | Dependencias | Criterio de aceptación |
|---|---|---|---|---|---|---|
| EO-080 | Presencia en Redis con TTL y heartbeat | M5 | P0 | M | EO-043 | `presence:player:{playerId}` existe con TTL 30 s y se refresca cada 10 s mientras haya WS activo. |
| EO-081 | Máquina de estados de `presence_state` | M5 | P0 | M | EO-034, EO-080 | Solo se aceptan las cuatro transiciones canónicas; `ONLINE → PROTECTED` directo es rechazado. La transición a `OFFLINE_PENDING` la decide el game loop en RAM contra `DisconnectGrace` (= `EO_PRESENCE_TTL_SECONDS`), no la expiración de la clave de Redis. |
| EO-082 | Cooldown de protección evaluado en la fase 5 del tick | M5 | P0 | M | EO-066, EO-081 | Con el cooldown a 10 segundos la transición ocurre a los 10 segundos, sin cambios de código. |
| EO-083 | `protection_until` a NULL y limpieza al reconectar | M5 | P1 | S | EO-082 | Reconectar desde `PROTECTED` deja `presence_state = 'ONLINE'` y `protection_until = NULL`. |
| EO-084 | Migración de `safe_zones` | M5 | P1 | S | EO-011 | La tabla admite `DENSE_FOREST` y `CAVERN` y rechaza cualquier otro tipo. |
| EO-085 | Cálculo de safe zones por el servidor | M5 | P1 | M | EO-020, EO-084 | `DENSE_FOREST` se apoya en `FOREST` y `CAVERN` en adyacencia a `MOUNTAIN`, siempre validado en servidor. |
| EO-086 | Estado `HIDDEN` y su efecto en el interest management | M5 | P1 | M | EO-048, EO-085 | Una unidad `HIDDEN` no aparece en el snapshot de otro jugador cuya área de interés la contiene. |
| EO-087 | `city.update` de presencia y evento `CityProtectionEngaged` | M5 | P1 | S | EO-051, EO-081 | Cada transición emite `city.update` y registra el evento en `world_events`. |
| EO-088 | Error `CITY_PROTECTED` | M5 | P2 | S | EO-082 | El código está definido, se devuelve en las acciones bloqueadas y tiene test. |
| EO-089 | Persistencia transaccional de las transiciones de presencia | M5 | P0 | S | EO-081 | Un reinicio conserva el `presence_state` de todas las ciudades y no reinicia el cooldown. |

## M6 — Territories

| ID | Título | Milestone | Prioridad | Tamaño | Dependencias | Criterio de aceptación |
|---|---|---|---|---|---|---|
| EO-090 | Migración de `territories` y `territory_control` | M6 | P0 | M | EO-011 | La geometría rectangular vive en `territories` y el control en `territory_control`, en tablas separadas. |
| EO-091 | Dominio de territorio: pertenencia y controlador vigente | M6 | P0 | M | EO-090 | Un tile en `max_x` pertenece al territorio y uno en `max_x + 1` no. |
| EO-092 | Mapeo de territorio a chunks solapados | M6 | P0 | S | EO-021, EO-091 | Un territorio que cruza fronteras de chunk se mapea a todos los chunks que toca. |
| EO-093 | Ownership con concurrencia optimista sobre `version` | M6 | P0 | M | EO-091 | Dos cambios concurrentes: uno gana y el otro falla explícitamente, sin pérdida silenciosa. |
| EO-094 | Mensaje `territory.update` difundido por chunk | M6 | P0 | M | EO-050, EO-092 | Solo las conexiones suscritas a chunks solapados lo reciben. |
| EO-095 | Overlay de territorio en el cliente | M6 | P1 | M | EO-055, EO-094 | El overlay refleja exactamente el ownership emitido por el servidor y no lo calcula localmente. |
| EO-096 | `FORBIDDEN` en acciones dentro de territorio ajeno | M6 | P2 | S | EO-091 | La acción restringida devuelve `FORBIDDEN` y no `INTERNAL_ERROR`. |
| EO-097 | Persistencia y recuperación del ownership | M6 | P0 | S | EO-093 | Un reinicio conserva el controlador de cada territorio. |

## M7 — Diplomacy foundation

| ID | Título | Milestone | Prioridad | Tamaño | Dependencias | Criterio de aceptación |
|---|---|---|---|---|---|---|
| EO-100 | Migración de `treaties` y `garrisons` | M7 | P0 | M | EO-011 | `treaties` admite `NON_AGGRESSION`, `ALLIANCE` y `TRADE` con estados `PROPOSED`, `ACTIVE`, `EXPIRED` y `BROKEN`, y el flag `allows_garrison`. |
| EO-101 | Ciclo de vida del treaty | M7 | P0 | M | EO-100 | Solo se aceptan `PROPOSED → ACTIVE`, `ACTIVE → EXPIRED` y `ACTIVE → BROKEN`. |
| EO-102 | Regla de habilitación de garrison | M7 | P0 | M | EO-101 | Solo un treaty `ACTIVE` con `allows_garrison` verdadero habilita el garrison. |
| EO-103 | Error `TREATY_REQUIRED` | M7 | P0 | S | EO-102 | Un garrison sin treaty válido devuelve `system.error` con ese código exacto. |
| EO-104 | Transición a `GARRISONED` y cancelación del movimiento activo | M7 | P0 | M | EO-069, EO-102 | Guarnecer una unidad en movimiento deja su `unit_movements` en `CANCELLED` en la misma transacción. |
| EO-105 | `UNIT_GARRISONED` en `unit.move` | M7 | P0 | S | EO-068, EO-104 | Mover una unidad guarnecida devuelve `unit.move.rejected` con `UNIT_GARRISONED`. |
| EO-106 | Expiración de treaties en la fase 5 del tick | M7 | P1 | M | EO-066, EO-101 | Un treaty con expiración pasada transiciona a `EXPIRED` en el tick siguiente. |
| EO-107 | Efecto del garrison en el interest management | M7 | P1 | S | EO-050, EO-104 | La unidad guarnecida produce `entity.despawn` para quienes dejan de verla. |
| EO-108 | Recuperación de treaties y garrisons tras reinicio | M7 | P0 | S | EO-104 | Un reinicio conserva el estado exacto y no resucita movimientos cancelados. |

## Transversales

| ID | Título | Milestone | Prioridad | Tamaño | Dependencias | Criterio de aceptación |
|---|---|---|---|---|---|---|
| EO-110 | Registro único de invariantes con IDs estables | Transversal | P0 | M | EO-018 | `docs/invariants/` es el **único** registro: cada `INV-*` tiene enunciado formal, milestone que lo cubre y test que lo verifica, y ningún ID se reutiliza con otro significado. |
| EO-111 | Runbook de operación 24/7 | Transversal | P1 | M | EO-009 | **Abierto.** Describe arranque, parada, migración con estado vivo, lectura de métricas y diagnóstico de overruns. |
| EO-112 | ADRs de las decisiones estructurales | Transversal | P1 | S | EO-018 | Existen los doce ADR de `docs/decisions/`, de `ADR-001-game-server-language.md` a `ADR-012-database-migrations.md`. Todo enlace debe usar esos nombres exactos. |
| EO-113 | Cliente WS de pruebas end-to-end | Transversal | P1 | M | EO-044 | **Abierto.** Un cliente en Node ejecuta el recorrido completo de conexión, snapshot y movimiento en CI. Bloqueado por `EO-016` y por `DEBT-19`. |
| EO-114 | Seeds de datos de desarrollo | Transversal | P2 | S | EO-036 | Un script deja un mundo con varios jugadores listos para pruebas manuales reproducibles. |
| EO-115 | Escenario de simulación reproducible para regresión | Transversal | P2 | M | EO-077 | Un escenario fijo con `FakeClock` produce el mismo estado final en cada ejecución y se compara contra un *golden file*. |
| EO-116 | Tests de carga con k6 | Transversal | P3 | L | EO-113 | Diferido: no forma parte del MVP y no bloquea ningún milestone. |

---

## Deuda técnica aceptada

Cada entrada es una decisión tomada a conciencia, no un descuido. La columna **Condición de
revisión** define el disparador objetivo que obliga a reabrir la decisión: mientras no se cumpla, la
deuda se mantiene sin discusión; en cuanto se cumpla, se convierte en un item de backlog con
prioridad.

| ID | Deuda | Decisión asumida | Coste y riesgo | Condición de revisión |
|---|---|---|---|---|
| DEBT-01 | Destino no transitable rechazado en lugar de buscar un tile cercano | El MVP devuelve `TARGET_NOT_WALKABLE` y no hace *nearest walkable tile*. | Fricción de usabilidad: clicar sobre agua, montaña o un edificio no hace nada. | Cuando la telemetría de `unit.move` muestre una proporción sostenida y elevada de rechazos por este código, o cuando el cliente introduzca selección de destino sobre entidades en lugar de sobre tiles. |
| DEBT-02 | Protección offline sin límite superior | `protection_until` queda a NULL: la protección es indefinida mientras el jugador siga offline. El campo ya existe para límites futuros. | Un jugador puede permanecer inmune indefinidamente, lo que rompe el equilibrio en cuanto exista combate. | Antes de cerrar la fase Combat. Es prerrequisito duro: no se puede abrir combate con protección indefinida. |
| DEBT-03 | Sin compresión en el WebSocket | JSON UTF-8 plano, sin `permessage-deflate` ni agregación de deltas. | Consumo de ancho de banda y presión sobre el límite de 16384 bytes por mensaje en snapshots densos. | Cuando un `world.snapshot` típico se acerque al límite de tamaño de mensaje, o cuando el tráfico por conexión deje de ser despreciable en las métricas de `eo_ws_messages_total`. |
| DEBT-04 | A\* plano en lugar de jerárquico | Búsqueda sobre el grid completo con límites de 20000 nodos y 256 tiles. La interfaz `Pathfinder` ya está diseñada para permitir la sustitución. | Coste por consulta creciente con la distancia y con la densidad de obstáculos. | Cuando `eo_pathfinding_duration_seconds` degrade el presupuesto del tick, cuando las peticiones se acerquen habitualmente al límite de nodos, o al abordar Caravans, donde el pathfinding pasa a ser continuo. |
| DEBT-05 | Sin sharding: un único proceso de Game Server | El mundo de 512×512 y el volumen objetivo del MVP caben en un proceso. | Techo duro de escalado y punto único de fallo. | Cuando `eo_game_tick_overruns_total` crezca de forma sostenida con carga normal, o al planificar Large-scale warfare. |
| DEBT-06 | Sin tests de carga en el MVP | k6 queda diferido; el MVP se valida con unit, integration, contract, simulation y recovery. | Los límites reales de concurrencia son desconocidos hasta que se midan. | Antes de cualquier exposición pública con jugadores reales, y en todo caso antes de abordar Large-scale warfare. |
| DEBT-07 | Validación del protocolo duplicada en Zod y en Go | Zod es la fuente de verdad; el servidor valida a mano por rendimiento. | Riesgo permanente de deriva entre las dos implementaciones. | Se mantiene mientras los contract tests contra el JSON Schema exportado sigan siendo obligatorios y verdes. Si el CI deja de bloquear por deriva, la deuda deja de ser aceptable de inmediato. |
| DEBT-08 | Sin reanudación por `seq` al reconectar | La reconexión emite un `world.snapshot` completo del área de interés; no hay buffer de deltas. | Pico de tráfico en reconexiones masivas y trabajo redundante tras cortes breves. | Cuando la tasa de reconexión o el coste del snapshot sean visibles en métricas, o al introducir movilidad de cliente con redes inestables. |
| DEBT-09 | Territorios rectangulares | Geometría `min_x, min_y, max_x, max_y`, sin polígonos ni PostGIS. | Expresividad limitada: no se pueden modelar fronteras irregulares. | Cuando el diseño de juego exija fronteras no rectangulares, previsiblemente con Sieges o con la conquista territorial real. |
| DEBT-10 | Formato de red JSON en lugar de binario | JSON UTF-8 legible, fácil de depurar y de validar contra JSON Schema. | Overhead de tamaño y de parseo frente a un formato binario. | Junto con DEBT-03, cuando el ancho de banda o el coste de serialización aparezcan en el presupuesto del tick. |
| DEBT-11 | Sin modificadores de población por edificios | `population_limit` = `era.population_cap`, con cero modificadores en el MVP. | El modelo está previsto pero no ejercitado; el primer modificador real puede destapar supuestos. | Al abordar Economy, donde los edificios pasan a tener efecto. |
| DEBT-12 | Mundo completo residente en RAM | 256 chunks de 1024 bytes son 256 KiB de terreno: cargarlo entero es trivialmente barato. | El supuesto deja de ser válido si crece `EO_WORLD_WIDTH`/`EO_WORLD_HEIGHT` en órdenes de magnitud. | Cuando el mundo configurado supere lo que es razonable mantener en memoria de un solo proceso, o al introducir sharding (DEBT-05). |
| DEBT-13 | Radio de interés único y global | `EO_INTEREST_RADIUS_CHUNKS` es un valor de servidor, igual para todos los jugadores y situaciones. | No se puede reducir el radio bajo carga ni ampliarlo para vistas alejadas. | Cuando el cliente ofrezca niveles de zoom significativos, o cuando haga falta degradar el radio dinámicamente bajo presión. |
| DEBT-14 | Sin negociación de treaties en el protocolo v1 | La lista de mensajes v1 no incluye diplomacia; en M7 los treaties se crean por vía administrativa o de seed. El mecanismo de cara al jugador es **TBD (fuera de MVP)**. | La funcionalidad no es utilizable por un jugador real hasta que exista ese mecanismo. | Antes de exponer la diplomacia a jugadores. Requiere una versión del protocolo con mensajes nuevos y, por tanto, decisión de versionado. |
| DEBT-15 | `WATER` bloqueado y sin naval | El agua es un obstáculo puro en el MVP; lo naval está fuera de scope. | El mundo generado puede contener regiones inaccesibles separadas por agua. | Al diseñar la fase naval, que hoy no está planificada, o si la generación produce aislamiento suficiente para perjudicar el juego. |
| DEBT-16 | Sin política diferenciada para consumidores lentos | Todas las conexiones reciben deltas al mismo ritmo; no hay descarte selectivo ni prioridad por tipo de mensaje. La única defensa es la cola de salida por conexión (`EO_WS_OUTBOUND_QUEUE_SIZE`, 256): cuando se llena, la sesión se cierra con `4500` y el cliente reconecta con un snapshot limpio. | Una conexión lenta se desconecta en lugar de degradarse; en una reconexión masiva eso multiplica el coste de snapshots. | Cuando aparezcan desconexiones por backlog en producción, o al abordar Large-scale warfare. |
| DEBT-17 | Escritura durable del movimiento diferida: ventana de riesgo aceptada | El movimiento se aplica primero en RAM y su transacción se **encola** hacia los workers de persistencia (hasta 3 intentos con backoff, y compensación `OnPermanentFailure` si se agotan). El tick nunca hace I/O de PostgreSQL, porque un tick bloqueado por la base de datos sería un mundo detenido. | **RPO documentado**: si el proceso muere entre la aceptación del `unit.move` y el `COMMIT` —típicamente pocas decenas de milisegundos— ese movimiento se pierde y la unidad queda en su última posición consolidada. El jugador ve una orden aceptada que al reconectar no ocurrió. No es un descuido: es el precio elegido a cambio de un tick que nunca se detiene. | Cuando `eo_persistence_queue_depth` deje de vaciarse en el margen de un flush bajo carga normal, cuando aparezcan fallos permanentes de la cola en producción, o antes de cualquier exposición pública en la que perder una orden aceptada sea inaceptable. En ese momento la decisión a tomar es acuse de recibo diferido —confirmar al cliente sólo tras el `COMMIT`— frente a un WAL propio del servidor. |
| DEBT-18 | El alta de jugador vive en el game server, no en Next.js | `POST /api/auth/register` y `POST /api/auth/login` los sirve hoy `internal/httpapi` del game server, con bcrypt, y es él quien emite el game ticket. La arquitectura objetivo de [../decisions/ADR-010-authentication-game-ticket.md](../decisions/ADR-010-authentication-game-ticket.md) sitúa esos endpoints en una API route de Next.js. Se aceptó porque `apps/web/` no existía y sin alta no había nada que probar de extremo a extremo; ahora existe, pero los endpoints siguen en el Game Server. | El proceso autoritativo de simulación carga además con manejo de contraseñas y con el coste de bcrypt, que es deliberadamente lento: es superficie de ataque y latencia en el proceso que menos debería tener ninguna de las dos. Además desvía al game server de su única responsabilidad. | En cuanto exista `apps/web/` con su capa de sesión. Es prerrequisito duro de cerrar M3: no se declara M3 terminado con el alta viviendo en el game server. |
| ~~DEBT-19~~ | ~~Tests de integración escritos pero no ejecutados~~ | **CERRADA.** La suite corre y está en verde por dos vías independientes: en la máquina de desarrollo contra un cluster PostgreSQL propio y un Redis en WSL —sin Docker, que está descartado ahí—, y en la CI contra `postgres:16-alpine` y `redis:7-alpine`, con el detector de carreras activo. Son 44 tests. | — | Cerrada. Al ejecutarse por primera vez destapó un fallo real en `SetPresence` que ningún test unitario podía ver: una rama con dos marcadores recibía tres argumentos. Ese fue exactamente el retorno de la deuda. |

---

## Preguntas abiertas de diseño de juego

Ninguna de estas preguntas está resuelta en el canon técnico y ninguna es deducible de él. Cada una
debe decidirse **antes** del hito indicado, y su respuesta puede requerir un ADR o una spec nueva.
Hasta entonces, la documentación las trata como `TBD (fuera de MVP)` y no se implementa una respuesta
implícita en el código.

| ID | Pregunta | Por qué importa | Debe decidirse antes de |
|---|---|---|---|
| ~~Q-01~~ **RESUELTA** | ¿Cómo se elige la posición de la ciudad inicial y qué distancia mínima se garantiza entre ciudades? | Determina la experiencia de los primeros minutos y la densidad del mundo; con 512×512 tiles el espacio es finito. | **Respondida en `internal/game/founding`**: búsqueda determinista en espiral desde una semilla derivada del nombre de usuario, entorno despejado de radio 3 y separación mínima de **24 tiles** entre centros de ciudad, con `UNIQUE (center_x, center_y)` en base. Documentado en [../specs/city.md](../specs/city.md). |
| Q-02 | ¿La Civilization se elige al crear el jugador y es inmutable? | Condiciona toda la fase Civilizations y el diseño de los bonos culturales. | La fase Civilizations. |
| Q-03 | ¿La Global Faction se puede cambiar, con qué coste y con qué consecuencias sobre treaties y territorios? | ORDER/CHAOS/NEUTRAL es un eje ortogonal a la civilización; su mutabilidad afecta a la diplomacia. | La fase Factions. |
| Q-04 | ¿Cómo propone y acepta un jugador un treaty? | El protocolo v1 no define mensajes de diplomacia; sin esta decisión M7 es solo base técnica. | Exponer diplomacia a jugadores (ver DEBT-14). |
| Q-05 | ¿Qué ocurre con una unidad en estado `DEAD`: se conserva la fila, se archiva o se elimina? | Afecta al tamaño de `units`, a la auditoría vía `world_events` y a la reconstrucción histórica. | La fase Combat. |
| Q-06 | ¿`HIDDEN` es exclusivo de las safe zones o habrá sigilo como mecánica general? | Determina si el interest management necesita reglas de visibilidad por observador más allá del chunk. | La fase Combat. |
| Q-07 | ¿La protección offline tendrá un tope temporal y de qué valor? | Es el disparador de DEBT-02 y condiciona todo el equilibrio del riesgo. | La fase Combat. |
| Q-08 | ¿Las unidades de un jugador `PROTECTED` que están fuera de su ciudad también quedan protegidas? | Define si la protección es de ciudad o de jugador, con consecuencias directas sobre el modelo de datos. | La fase Combat. |
| Q-09 | ¿Pueden solaparse territorios y, si se solapan, cómo se resuelve el controlador vigente? | Sin regla determinista el ownership deja de ser reproducible y rompe el principio de determinismo. | Cerrar M6. |
| Q-10 | ¿Cómo se reclama y cómo se pierde un territorio mientras no exista combate? | M6 entrega la estructura pero no la mecánica; sin ella el territorio es estático. | Cerrar M6. |
| Q-11 | ¿Qué recursos existen, cómo se almacenan y qué límites tienen? | Es el cimiento de Economy, Technology y Trade a la vez. | La fase Economy. |
| Q-12 | ¿El avance de era se consigue por investigación, por acumulación de recursos, por tiempo o por una combinación? | Las cuatro eras y sus `population_cap` ya están en la tabla `eras`, pero no la condición de avance. | La fase Technology. |
| Q-13 | ¿Existe un único mundo persistente o habrá varios mundos o regiones independientes? | Es la decisión que determina si el sharding (DEBT-05) es una optimización o un requisito de producto. | La fase Large-scale warfare. |
| Q-14 | ¿El mundo es eterno o habrá temporadas con reinicio? | Condiciona la estrategia de migraciones, de archivado y de operación 24/7. | Cualquier despliegue con jugadores reales. |
| Q-15 | ¿Cuál es el objetivo de jugadores concurrentes y de unidades activas simultáneas? | Sin una cifra objetivo no se pueden dimensionar el tick, el interest management ni los tests de carga. | Diseñar los tests de carga (EO-116). |
| Q-16 | ¿Qué comunicación entre jugadores existirá y con qué moderación? | El chat avanzado está fuera del MVP, pero la diplomacia sin comunicación es poco jugable. | La fase Clans. |

---

## Documentos relacionados

- [roadmap.md](roadmap.md) — visión temporal, estrategia de vertical slice y riesgos del proyecto.
- [milestones.md](milestones.md) — alcance, entregables, tests y criterios de aceptación por milestone.
- [../decisions/README.md](../decisions/README.md) — registro de decisiones de arquitectura (ADR).
- [../testing/strategy.md](../testing/strategy.md) — niveles de test y Definition of Done.
