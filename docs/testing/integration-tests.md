# Pruebas de integración

Cómo se verifica la capa de persistencia contra PostgreSQL y Redis **reales**: montaje del entorno, base de datos dedicada, limpieza entre tests, el gate `EO_INTEGRATION=1`, y la lista enumerada de casos obligatorios incluido el test de recuperación tras caída del servidor.

> **Estado real: EJECUTADO Y EN VERDE.** Tres ficheros llevan la etiqueta de compilación `integration`
> en `services/game-server`:
>
> | Fichero | Tests | Contra qué |
> |---|---|---|
> | `internal/persistence/postgres/integration_test.go` | 12 | PostgreSQL 16 real |
> | `internal/persistence/postgres/testenv_integration_test.go` | — | montaje y limpieza |
> | `internal/persistence/redis/redis_integration_test.go` | 9 | Redis real |
>
> Los 21 pasan con el detector de carreras activo. **No se ejecutan con Docker**: la máquina de
> desarrollo lo tiene descartado (provoca pantallazos azules por consumo de RAM). PostgreSQL es un
> cluster propio en `.pgdata/` puerto 5433, y Redis vive en WSL. El procedimiento exacto está en
> [local-development.md](../operations/local-development.md) §3-bis. El job `integration` de la CI sí
> usa contenedores, pero la CI todavía no ha llegado a ejecutarse ni una vez.
>
> Donde este documento describa un test que **no** existe todavía, lo dice en su propia fila. Ubicación
> y convenciones en [strategy.md](./strategy.md) §5.

---

## 1. Qué verifica este nivel y qué no

Un test de integración aquí responde a una sola pregunta: **¿el motor real hace cumplir el diseño?**

Los tests unitarios verifican que el dominio calcula bien. No pueden verificar que un índice único parcial impida de verdad dos movimientos `ACTIVE`, que una transacción revierta de verdad, que un TTL de Redis expire de verdad, ni que un `jsonb` devuelva los `tMs` como enteros y no como flotantes. Eso solo lo demuestra el motor.

| Se verifica aquí | No se verifica aquí |
|---|---|
| Atomicidad real de las transacciones de write-through (canon §12) | Corrección de la lógica de dominio → [unit-tests.md](./unit-tests.md) |
| Restricciones: `CHECK`, `UNIQUE`, índices únicos **parciales**, claves foráneas | Serialización del protocolo → [contract-tests.md](./contract-tests.md) |
| Round-trip de tipos: `bigint`, `jsonb`, `timestamptz` y `*_time_ms` | Avance del game loop → [simulation-tests.md](./simulation-tests.md) |
| Expiración real de claves de Redis y semántica de TTL | Rendimiento y capacidad → [load-tests.md](./load-tests.md) |
| Idempotencia durable de `requestId` | |
| **Recuperación tras crash**: el mundo se reconstruye correctamente | |

El nivel `recovery` del canon §19 se implementa como un subconjunto de estos tests: comparte el harness, el gate y los servicios, y se ejecuta dentro del job `integration` de la CI (§8.1 de [strategy.md](./strategy.md)); **no** es un job separado.

---

## 2. Entorno

### 2.1 Servicios

Los mismos de desarrollo, levantados por Docker Compose, descritos en [../operations/local-development.md](../operations/local-development.md):

```powershell
pnpm run db:up          # docker compose up -d postgres redis
```

| Servicio | Imagen | Puerto | Uso en tests |
|---|---|---|---|
| `postgres` | `postgres:16-alpine` | 5432 | Base de datos **dedicada** de test, no la de desarrollo |
| `redis` | `redis:7-alpine` | 6379 | Índice de base de datos **dedicado**, no el de desarrollo |

Ambos servicios llevan healthcheck declarado en `docker-compose.yml`, y así es como los levanta la CI.

**En la máquina de desarrollo no se usa ninguno de los dos**, porque Docker está descartado ahí. El
equivalente verificado es un cluster PostgreSQL propio (`pnpm run pg:start`, puerto **5433**) y un
`redis-server` dentro de WSL, con las URLs correspondientes en `EO_TEST_POSTGRES_URL` y
`EO_TEST_REDIS_URL`. El procedimiento completo está en
[local-development.md](../operations/local-development.md) §3-bis.7.

Quien no tenga ninguna de las dos cosas levantadas no debe ver la suite en rojo: ver el gate en §3.

### 2.2 Aislamiento por base de datos y por índice de Redis

Los valores son los del job `integration` del workflow, que es la única configuración de test que
existe hoy de verdad. En local se replican tal cual.

| Recurso | Desarrollo (`.env.example`, `docker-compose.yml`) | Test (CI y local) |
|---|---|---|
| Base de datos PostgreSQL | `empires` (usuario `empires`, contraseña `empires_dev_password`) | **`empires_test`** (usuario `empires`, contraseña `empires_ci_password`) |
| Índice de Redis | `0` (`redis://localhost:6379/0`) | **`1`** (`redis://localhost:6379/1`) |

**Las variables son propias del nivel, no las de ejecución.** Los tests **no** leen `EO_POSTGRES_URL`
ni `EO_REDIS_URL` (regla R10 de [strategy.md](./strategy.md)): usar nombres distintos hace imposible
apuntar por descuido a la base de desarrollo.

```
EO_INTEGRATION=1
EO_TEST_POSTGRES_URL=postgres://empires:empires_ci_password@localhost:5432/empires_test?sslmode=disable
EO_TEST_REDIS_URL=redis://localhost:6379/1
```

**Salvaguarda obligatoria.** Además de usar variables distintas, el harness inspecciona la URL antes de tocar nada y aborta si detecta la base de datos o el índice de desarrollo. Es una comprobación de tres líneas que evita el accidente más caro y más fácil de cometer: un `TRUNCATE` sobre el mundo de desarrollo.

```go
func mustBeTestDatabase(t *testing.T, pgURL, redisURL string) {
	t.Helper()
	require.Contains(t, pgURL, "empires_test",
		"EO_TEST_POSTGRES_URL debe apuntar a la base de datos de test; abortando para no destruir datos de desarrollo")
	require.NotContains(t, pgURL, "/empires?",
		"EO_TEST_POSTGRES_URL apunta a la base de datos de DESARROLLO")
	require.True(t, strings.HasSuffix(redisURL, "/1"),
		"EO_TEST_REDIS_URL debe usar el índice 1 en tests")
}
```

### 2.3 Creación de la base de datos y migraciones

La base de datos de test se crea una vez y se migra al arrancar la suite, no en cada test.

**El script `db:migrate` existe** (`go run ./cmd/migrate`) y sirve para migrar a mano, pero **el harness de
integración no lo usa**. Las migraciones son **SQL embebido con `go:embed`** y las aplica
**golang-migrate desde el propio proceso** al arrancar (`internal/persistence/postgres/migrate.go`,
que además reescribe la URL al esquema `pgx5`). El harness de integración llama a ese mismo migrador,
no a un `schema.sql` paralelo: un esquema de test generado por otro camino deja de representar la
realidad en cuanto alguien añade una migración, y entonces los tests dejan de proteger nada.
Convención `NNNN_nombre.up.sql` / `.down.sql` y control por `schema_migrations`: ver
[../database/migrations.md](../database/migrations.md).

Ciclo completo en local (PowerShell), replicando lo que hace el job `integration`:

```powershell
# 1. Servicios arriba (requiere el daemon de Docker Desktop ARRANCADO)
pnpm run db:up

# 2. Base de datos de test (idempotente; ignorar "already exists")
docker compose exec -T postgres psql -U empires -d postgres -c "CREATE DATABASE empires_test OWNER empires;"

# 3. Suite (el propio harness aplica las migraciones embebidas sobre esa base)
$env:EO_INTEGRATION = "1"
$env:EO_TEST_POSTGRES_URL = "postgres://empires:empires_dev_password@localhost:5432/empires_test?sslmode=disable"
$env:EO_TEST_REDIS_URL = "redis://localhost:6379/1"
pnpm run server:test:integration
```

La contraseña del paso 3 es la de `docker-compose.yml` (`empires_dev_password`); en CI es
`empires_ci_password`, porque allí el servicio `postgres` se declara en el workflow.

**Datos de catálogo.** Las tablas `civilizations`, `factions` y `eras` son catálogos: se pueblan por migración de datos y **no** se truncan entre tests. Los tests dependen de que `STONE_AGE` exista con `population_cap = 20` (canon §10).

---

## 3. El doble gate: etiqueta `integration` **y** `EO_INTEGRATION=1`

Regla R8 de [strategy.md](./strategy.md): los tests de integración están *gated* por **dos**
mecanismos a la vez, no por uno. La razón es concreta y local: en la máquina de desarrollo el daemon
de Docker no está activo (de hecho no arrancó), y una suite que falla por eso entrena a la gente a
ignorar el rojo.

**Gate 1 — etiqueta de compilación.** Los ficheros del nivel se llaman `*_integration_test.go` y
empiezan con la etiqueta. Es lo que hace que `pnpm run server:test` (`go test ./...`) los excluya de
la compilación, y lo que hace que el job `integration` de la CI los incluya con
`go test -tags=integration ./...`.

```go
//go:build integration

package postgres_test
```

**Gate 2 — variable de entorno.** Dentro del fichero, cada test se salta con `t.Skip` —nunca
`t.Fatal`— si la variable no está. Cubre el caso de quien compila con la etiqueta pero no tiene los
servicios levantados.

```go
// requireIntegration marca el test como de integración. Si EO_INTEGRATION no
// vale "1", el test se SALTA (t.Skip), nunca falla.
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("EO_INTEGRATION") != "1" {
		t.Skip("integration: requiere EO_INTEGRATION=1 y Docker arrancado (pnpm run db:up)")
	}
}
```

Uso en cada test del nivel:

```go
// INV-MOVE-001: una unidad tiene como máximo un movimiento ACTIVE.
func TestDosMovimientosActivosViolanElIndiceUnico(t *testing.T) {
	requireIntegration(t)
	db := newTestDB(t) // conexión + limpieza registrada con t.Cleanup
	// …
}
```

**Los helpers viven en el paquete de test, no en un `internal/testsupport`.** No existe tal paquete y
no debe crearse todavía: los dobles y constructores del árbol se declaran en el `_test.go` del
paquete que los necesita (`recorder`, `syncPersister`, `fakeRepos`, `newHarness`, `buildWorld`), y
extraer una abstracción sin tres usuarios reales sólo añade indirección. Ver
[strategy.md](./strategy.md) §5.1.

**El coste de la etiqueta, y cómo se compensa.** Una etiqueta de compilación excluye los ficheros del
`go build` y del `go vet` por defecto, con lo que un test de integración que ha dejado de compilar
—porque cambió una firma— podría pasar desapercibido. Se compensa en el job `integration` de cada
PR, que compila **y** ejecuta con `-tags=integration`: la deriva de compilación se detecta ahí, no en
la nocturna.

**En CI el gate siempre está activo.** Los servicios están garantizados, así que un *skip* en CI sería
una regresión silenciosa. Cuando existan los primeros ficheros, el job debe verificar además que el
número de tests ejecutados es mayor que cero; **hoy el job pasa con cero tests**, y eso es
precisamente lo que este documento no puede presentar como verde.

---

## 4. Limpieza entre tests

Dos mecanismos, con criterio explícito de cuándo usar cada uno. Elegir mal produce tests que pasan sin verificar nada.

### 4.1 Mecanismo A — transacción revertida

El test abre una transacción, la inyecta como `UnitOfWork` en el repositorio bajo prueba, y hace `ROLLBACK` en el `t.Cleanup`. No queda rastro y es rapidísimo.

```go
func NewTestTx(t *testing.T) pgx.Tx {
	t.Helper()
	pool := sharedPool(t)
	tx, err := pool.Begin(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}
```

**Válido para:** verificar que una consulta escribe y lee lo esperado, que un `CHECK` rechaza un valor, que un índice único dispara `23505`, que el mapeo de tipos es correcto.

**No válido para** —y esto es lo que hay que tener claro—:

| Situación | Por qué la transacción revertida no sirve |
|---|---|
| Concurrencia entre dos conexiones | Los cambios de la transacción no son visibles fuera de ella; la segunda conexión no ve nada contra lo que competir |
| Comportamiento de `COMMIT` | Los triggers de `updated_at` y la visibilidad post-commit no se ejercitan |
| Test de recuperación | El reinicio del servidor abre conexiones nuevas: lo que no está confirmado no existe |
| Verificar que un rollback **de la aplicación** deja la base limpia | Se estaría verificando el rollback del test, no el del código |

### 4.2 Mecanismo B — truncado

Para todo lo anterior. Se confirman las transacciones de verdad y, al terminar, se vacían las tablas de datos preservando los catálogos.

```sql
TRUNCATE TABLE
    unit_movements, units, garrisons, territory_control, territories,
    safe_zones, treaties, world_events, cities, sessions,
    idempotency_keys, players, world_chunks, world_state
RESTART IDENTITY CASCADE;
```

- **`RESTART IDENTITY`** reinicia las secuencias de `bigint GENERATED ALWAYS AS IDENTITY`: los ids vuelven a empezar en 1 en cada test, lo que permite aserciones legibles y estables.
- **`CASCADE`** cubre las dependencias declaradas; el orden de la lista se mantiene igualmente de hijo a padre para que el fallo sea comprensible si alguien añade una tabla y olvida actualizarla.
- **No se truncan** `civilizations`, `factions`, `eras` ni `schema_migrations`.
- Se ejecuta en el `t.Cleanup`, **no** al principio del test: así, cuando un test falla, el estado que lo hizo fallar sigue en la base para inspeccionarlo con `pnpm run db:psql` (que apunta a la base de desarrollo: para la de test se usa el comando de §6). El siguiente test lo limpia. Un flag `EO_TEST_KEEP_DATA=1` que desactive el truncado del último test para depurar está ○ previsto, no implementado.

### 4.3 Redis

`FLUSHDB` sobre el índice 1 en el `t.Cleanup`. Nunca `FLUSHALL`: destruiría también el índice 0 de desarrollo.

```go
func NewTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	c := connectToTestIndex(t)  // índice 1
	t.Cleanup(func() { _ = c.FlushDB(context.Background()).Err() })
	return c
}
```

### 4.4 Paralelismo

La suite de integración se ejecuta **serializada a nivel de paquete** (`go test -p 1`) y ningún test del mecanismo B llama a `t.Parallel()`. Truncar tablas compartidas mientras otro test escribe produce fallos irreproducibles, exactamente la clase de *flaky* que la regla R7 prohíbe tolerar. El coste es aceptable: el objetivo de duración de toda la suite es inferior a 90 s.

---

## 5. Casos obligatorios

### 5.1 Creación atómica de jugador, ciudad y unidades

Canon §10 y §12: la creación de player/city/unit es **write-through inmediato y transaccional**. La ciudad inicial es 1 `TOWN_CENTER`, 1 zona urbana amurallada inicial y **3 `VILLAGER`**.

| # | Test | Escenario | Aserciones |
|---|---|---|---|
| **I1** | `Test_INV_PLAYER_003_NewPlayerHasOneCityAndThreeVillagers` | Alta completa de jugador | Exactamente 1 fila en `players` (con `id` `uuid`), 1 en `cities`, **3** en `units` con `unit_type = 'VILLAGER'`, `status = 'IDLE'`, `hp = 40`; `cities.population = 3`; `cities.population_limit = 20` derivado de `STONE_AGE`; `cities.presence_state = 'ONLINE'` |
| **I2** | `TestPlayerCreation_PartialFailureLeavesNothing` | Se inyecta un fallo al insertar el **tercer** `VILLAGER` | `players`, `cities` y `units` quedan **con cero filas** para ese jugador. No hay ciudad huérfana ni jugador sin unidades |
| **I3** | `TestPlayerCreation_DuplicateUsernameRollsBackEverything` | Segundo alta con el mismo `username` | Violación de `UNIQUE` en `players.username`; la ciudad y las unidades de ese segundo intento no existen |
| **I4** | `TestPlayerCreation_DuplicateCityPositionRollsBackEverything` | Dos ciudades sobre el mismo tile | Violación de la restricción real `cities_unique_center` (`UNIQUE (center_x, center_y)`); nada persistido del segundo intento |
| **I5** | `Test_INV_CITY_007_CityCenterOnValidTile` | Ciudad sobre `MOUNTAIN` o fuera de límites | Rechazo en el dominio (`internal/game/founding`); ninguna fila creada. La base no lo comprueba: el terreno no vive en PostgreSQL como restricción |
| **I6** | `TestPlayerCreation_IsSingleTransaction` | Instrumentación del pool | Todas las escrituras del alta ocurren en **una** transacción. Tres transacciones separadas pasarían I1 y fallarían I2 |

**Orden real del alta.** Primero la transacción en PostgreSQL (jugador + ciudad + 3 aldeanos, atómica)
y **sólo después** el comando `IntroducePlayer` que incorpora al jugador al mundo en RAM. Un test que
observara el mundo en memoria antes del `COMMIT` estaría verificando un orden que el código no tiene.
Nótese además que hoy el alta la sirve el propio game server en `POST /api/auth/register`
(`internal/httpapi`, con bcrypt): es explícitamente provisional, y ADR-010 la traslada a Next.js.

I2 es el test que da valor a todo el bloque. Implementación concreta del fallo inyectado: el repositorio recibe un `UnitOfWork` decorado que devuelve error en la n-ésima escritura.

```go
func TestPlayerCreation_PartialFailureLeavesNothing(t *testing.T) {
	requireIntegration(t)
	db := newTestDB(t)          // mecanismo B: commits reales
	uow := failingUnitOfWork(db, failOnWriteN(5)) // 5ª escritura = 3er villager

	_, err := playersvc.CreatePlayer(ctx, uow, newPlayerRequest())
	require.Error(t, err)

	require.Equal(t, 0, db.CountRows(t, "players"))
	require.Equal(t, 0, db.CountRows(t, "cities"))
	require.Equal(t, 0, db.CountRows(t, "units"))
}
```

### 5.2 Persistir e hidratar un movimiento

Canon §7 y §12. El movimiento se aplica primero en RAM y su transacción se **encola** en la cola de
persistencia (workers, hasta 3 intentos con backoff y compensación `OnPermanentFailure` si se agotan):
el tick nunca hace I/O de PostgreSQL. Eso deja una ventana de riesgo honesta y documentable —si el
proceso muere entre la aceptación y el `COMMIT`, típicamente pocas decenas de ms, ese movimiento se
pierde y la unidad queda en su última posición consolidada— que es un RPO declarado, no un descuido.

**Columnas reales de `unit_movements`** (migración `000001_initial_schema`), porque casi todos los
tests de este bloque las nombran: `id`, `unit_id`, `path jsonb`, `target_x`, `target_y`,
`start_time_ms bigint`, `arrival_time_ms bigint`, `status`, `finished_at timestamptz`, `created_at`,
`updated_at`. **No existen** `from_x`/`from_y`, `to_x`/`to_y`, `started_at`, `ended_at`,
`cancel_reason`, `request_id` ni `version`: el origen del trayecto es el primer waypoint de `path`, y
la razón de cancelación viaja en el mensaje `unit.movement.cancelled` pero **no se persiste**.

| # | Test | Aserciones |
|---|---|---|
| **I7** | `TestMovementRepository_PersistAndHydrate` | Se persiste un movimiento `(100,100)→(103,102)` sobre `GRASSLAND`: dos diagonales y un paso ortogonal ⇒ `tMs = [0, 849, 1698, 2298]`. Al hidratarlo: mismos `unit_id`, `target_x`/`target_y`, `start_time_ms`, `arrival_time_ms = start_time_ms + 2298`, `status = 'ACTIVE'`, y la polilínea **idéntica elemento a elemento**, con el primer waypoint en el origen y `tMs = 0` |
| **I8** | `TestMovementRepository_JsonbPreservesIntegerTMs` | Los `tMs` vuelven como enteros, no como `float64`. Un `tMs` de `849` que regrese como `849.0` y se redondee mal desplazaría la posición reconstruida un tile |
| **I9** | `TestMovementRepository_BigintIdsSurviveRoundTrip` | Un `unit_id` cercano al máximo de `bigint` (`9223372036854775807`) se persiste y recupera sin pérdida de precisión |
| **I10** | `TestMovementRepository_TimestamptzAndTimeMsAgree` | `created_at` (`timestamptz`, con `DEFAULT now()`) y `start_time_ms` (`bigint`, epoch ms de la simulación) representan instantes coherentes. La aritmética de simulación usa **siempre** el `bigint`; el `timestamptz` es sólo para auditoría humana |
| **I11** | `TestMovementRepository_CheckConstraintsRejectMalformedRows` | Inserción directa por SQL que viola `unit_movements_time_ordered` (`arrival_time_ms < start_time_ms`), `unit_movements_path_is_array` (`path` que no es un array, o array vacío) y `unit_movements_status_valid` (`status = 'PAUSED'`) ⇒ rechazo del motor. Son los tres nombres reales de las restricciones |
| **I12** | `TestFlushNoContradiceLaPolilinea` (`INV-PERSIST-004`) | Con un movimiento `ACTIVE` en curso, el flush periódico (`EO_PERSISTENCE_FLUSH_INTERVAL_TICKS = 50`) no deja una `units.x/y` incompatible con la polilínea. La contraparte en RAM ya está verificada: `TestVolcadoPeriodicoNoOcurreEnCadaTick` ([simulation-tests.md](./simulation-tests.md) §4.7) comprueba que en 60 ticks hay **exactamente un** `units.flush` |
| **I40** | `TestUnitsMantieneElChunkDesnormalizado` | Toda escritura de `units.x/y` recalcula `units.chunk_x`/`chunk_y` en la misma sentencia. Es la clave del interest management (`units_chunk_idx`, parcial sobre `status <> 'DEAD'`) y una desincronización haría desaparecer unidades del snapshot |

### 5.3 El índice único parcial impide dos movimientos `ACTIVE`

Canon §7: *una unidad tiene como máximo un movimiento `ACTIVE`*. La garantía física es el índice único
**parcial** que crea la migración `000001_initial_schema`, y su nombre real —el que aparecerá en el
error `23505` y en `pg_stat_user_indexes`— es `unit_movements_one_active_per_unit`:

```sql
CREATE UNIQUE INDEX unit_movements_one_active_per_unit
    ON unit_movements (unit_id)
    WHERE status = 'ACTIVE';
```

El índice de rehidratación al arrancar es `unit_movements_active_idx`, sobre `(arrival_time_ms, id)`
y también parcial sobre `status = 'ACTIVE'`: da un orden estable de carga.

| # | Test | Escenario | Aserciones |
|---|---|---|---|
| **I13** | `Test_INV_MOVE_001_AtMostOneActiveMovement` | Insertar dos filas `ACTIVE` para la misma `unit_id` | La segunda falla con `23505` (`unique_violation`) sobre `unit_movements_one_active_per_unit`. **No** se comprueba solo el mensaje: se comprueba el `SQLSTATE`. La contraparte en RAM es `TestNuevaOrdenReemplazaLaAnterior` ([simulation-tests.md](./simulation-tests.md) §4.1) |
| **I14** | `TestMovementRepository_ManyTerminalMovementsAreAllowed` | 50 filas `COMPLETED` y `CANCELLED` para la misma unidad | Todas se insertan. El índice es parcial precisamente para preservar el historial |
| **I15** | `TestMovementRepository_ReplaceOrderCancelsAndCreatesInOneTransaction` | Nueva orden sobre unidad en movimiento | En **una** transacción: `UPDATE … SET status='CANCELLED'` sobre el anterior e `INSERT` del nuevo. Al terminar hay exactamente 1 `ACTIVE` y 1 `CANCELLED` |
| **I16** | `TestMovementRepository_ConcurrentDoubleStartLosesOne` | Dos conexiones reales intentan iniciar movimiento para la misma unidad simultáneamente | Una gana; la otra recibe `23505` o un conflicto de serialización. Nunca quedan dos `ACTIVE`. **Requiere mecanismo B**: con transacción revertida este test no verifica nada |
| **I17** | `TestMovementRepository_ConflictIsRetriedThenRejected` | Se fuerza el conflicto de I16 en el camino de comando | El comando reintenta; si vuelve a fallar, se rechaza con `INTERNAL_ERROR` (canon §16). **Jamás** se resuelve borrando el índice |
| **I18** | `Test_INV_UNIT_005_MovingIffActiveMovement` (parte de base de datos) | Barrido sobre el estado persistido | No existe ninguna `units` con `status='MOVING'` sin `unit_movements` `ACTIVE`, ni viceversa |

### 5.4 Presencia con expiración real de la clave de Redis

Canon §9: `presence:player:{playerId}` con TTL `EO_PRESENCE_TTL_SECONDS` (30 s), heartbeat cada `EO_PRESENCE_HEARTBEAT_SECONDS` (10 s).

**Quién decide la transición, exactamente.** La transición a `OFFLINE_PENDING` la decide **el game
loop en RAM**, comparando el tiempo transcurrido desde la desconexión contra `DisconnectGrace`
(= `EO_PRESENCE_TTL_SECONDS`, 30 s). **No** se lee la expiración de la clave de Redis para decidirla.
Redis mantiene la presencia para observadores externos y para el futuro multiproceso, y un jugador
con varias sesiones abiertas sigue `ONLINE` mientras le quede una
(`TestVariasSesionesDelMismoJugador`, [simulation-tests.md](./simulation-tests.md) §4.4).

Este nivel verifica, por tanto, dos cosas distintas y complementarias: que **Redis expira de verdad**
la clave con el TTL configurado, y que la transición que decidió el loop **se persiste** en
`cities.presence_state`. No verifica la regla de decisión, que ya está cubierta en RAM.

**El problema del tiempo.** El TTL lo aplica el reloj de Redis, no el `Clock` inyectado: `FakeClock` no puede acelerarlo. Solución adoptada:

1. El TTL se **inyecta por configuración**, nunca se hardcodea. El test configura `EO_PRESENCE_TTL_SECONDS` con un valor pequeño (1 s) o escribe la clave con `PEXPIRE` de 200 ms.
2. La espera se hace con **deadline y condición**, no con `time.Sleep` fijo. Es la única excepción documentada a la regla R5 de [strategy.md](./strategy.md), y va comentada en el código.
3. La lógica de transición sigue siendo pura y se prueba a fondo en [unit-tests.md](./unit-tests.md) §7.2 con `FakeClock`. Aquí solo se verifica que **Redis expira de verdad** y que el servidor se entera.

| # | Test | Escenario | Aserciones |
|---|---|---|---|
| **I19** | `TestPresence_KeyExpiresAfterTtl` | `SET presence:player:{id}` con TTL corto; esperar con deadline | `EXISTS` pasa de 1 a 0; `TTL` decrece monótonamente y nunca es negativo mientras la clave vive |
| **I20** | `TestPresence_HeartbeatRefreshesTtl` | Heartbeat antes de la expiración | El TTL vuelve al valor completo; la clave no expira mientras haya heartbeats |
| **I21** | `TestPresence_OnlineWhileWithinGrace` | Cerrar el WS pero sin agotar `DisconnectGrace` | `cities.presence_state` sigue `'ONLINE'`: es el margen de reconexión (canon §9) |
| **I22** | `TestPresence_TransitionsToOfflinePendingIsPersisted` | Agotado el margen sin ninguna sesión abierta | `cities.presence_state = 'OFFLINE_PENDING'` y `cities.last_offline_at` no nulo, **persistidos en PostgreSQL**, no sólo en RAM. Es lo que hace útil el índice parcial `cities_offline_pending_idx` sobre `last_offline_at` |
| **I23** | `TestPresence_OfflinePendingToProtectedIsPersisted` | Vencido el cooldown (con `FakeClock` para los 300 s del temporizador de dominio) | `cities.presence_state = 'PROTECTED'` y `cities.protection_until IS NULL` en la fila real: en MVP la protección no caduca sola mientras el jugador siga offline |
| **I24** | `TestPresence_ReconnectClearsProtection` | Reconexión estando `PROTECTED` | `cities.presence_state = 'ONLINE'`, `cities.protection_until IS NULL`, clave de presencia recreada con TTL completo |
| **I25** | `Test_INV_PERSIST_003_NoDurableDataOnlyInRedis` | `FLUSHDB` del índice de test con el mundo poblado | Tras vaciar Redis, ningún dato durable se ha perdido: jugadores, ciudades, unidades y movimientos siguen en PostgreSQL. Solo se degrada la presencia, que se reconstruye |
| **I26** | `TestPresence_CityStateIsServerOnly` | Intento de escribir `presence_state` desde un payload de cliente | Imposible por construcción: no existe camino de escritura desde el protocolo (canon §9) |

I25 es el test que hace cumplir el principio del canon §1.4: **Redis nunca sustituye a PostgreSQL**.

### 5.5 Idempotencia de un `requestId` repetido

Canon §13: `requestId` se registra en Redis (`idem:{playerId}:{requestId}`, TTL 300 s) y, para comandos durables, también en `idempotency_keys` (`UNIQUE (player_id, request_id)`). Un `requestId` repetido **devuelve la respuesta original, no re-ejecuta**.

| # | Test | Escenario | Aserciones |
|---|---|---|---|
| **I27** | `Test_INV_SEC_007_RepeatedRequestIdIsIdempotent` | Mismo `unit.move` con el mismo `requestId`, dos veces | Exactamente **1** fila en `unit_movements`; ambas respuestas tienen el mismo `payload` (el `seq` difiere, porque es por conexión) |
| **I28** | `TestIdempotency_RedisKeyIsWrittenWithCorrectTtl` | Comando aceptado | Existe `idem:{playerId}:{requestId}` con TTL ≈ 300 s |
| **I29** | `TestIdempotency_DurableTableSurvivesRedisFlush` | Ejecutar, `FLUSHDB`, repetir el comando | Sigue siendo idempotente: la fila de `idempotency_keys` es la segunda línea de defensa. **1** sola fila en `unit_movements` |
| **I30** | `TestIdempotency_ConcurrentDuplicateExecutesOnce` | Dos frames idénticos procesados en el mismo tick | Una sola ejecución; dos respuestas con idéntico `payload` |
| **I31** | `TestIdempotency_DifferentRequestIdExecutesAgain` | Mismo comando, `requestId` distinto | **2** movimientos: el primero queda `CANCELLED` (el mensaje lleva `reason: "REPLACED"`, que no se persiste), el segundo `ACTIVE` |
| **I32** | `TestIdempotency_ScopedByPlayer` | Mismo `requestId` de dos jugadores distintos | Ambos se ejecutan: la clave es `(player_id, request_id)`, no `request_id` solo |
| **I33** | `TestIdempotency_RejectedCommandIsNotCached` | Comando rechazado con `TARGET_NOT_WALKABLE`, reintentado con el mismo `requestId` tras desbloquear el destino | Decisión de MVP: los rechazos **no** consumen la clave de idempotencia; el reintento se evalúa de nuevo. Se documenta explícitamente para que el comportamiento sea deliberado |
| **I34** | `TestIdempotency_ExpiredKeyAllowsReexecution` | Clave vencida (TTL corto inyectado) y fila de `idempotency_keys` purgada | El comando se ejecuta de nuevo. Es correcto: pasados 300 s el cliente ya no está reintentando |

### 5.6 Recuperación tras caída del servidor

Canon §7: *al arrancar se cargan los movimientos `ACTIVE`; si `arrival_time_ms <= now` el movimiento se completa inmediatamente (snap al tile final) y se finaliza; si no, se reanuda desde la polilínea.*

Este es el test más importante del nivel, porque verifica la promesa central del producto: **el mundo persiste sin jugadores conectados** (canon §1.2).

**La mitad en RAM ya está verificada, la durable no.** La función que decide qué hacer con cada
movimiento al arrancar es `simulation.Hydrate`, y sus tres casos —reanudar, completar por llegada
vencida, y fallar ante una polilínea inválida— **están en verde hoy** en
`simulation_test.go` ([simulation-tests.md](./simulation-tests.md) §4.5, casos H1, H2 y H3). Lo que
falta, y es lo que describe esta sección, es la mitad durable: que el estado del que parte `Hydrate`
sea exactamente el que quedó confirmado en PostgreSQL tras un `SIGKILL`.

#### 5.6.1 Cómo se simula la caída

No se mata un contenedor: se destruye el **proceso lógico** del servidor sin cierre ordenado, dejando intacto lo que ya estaba confirmado en PostgreSQL.

```go
// serverHarness (declarado en el propio paquete de test) envuelve el arranque completo del Game Server:
// pools de conexión, world, loop, workers de persistencia.
//
//	Crash() aborta el contexto raíz y cierra los pools SIN ejecutar
//	el shutdown ordenado: no hay flush final, no hay drenaje de la cola
//	de persistencia, no se cierran sesiones. Es exactamente lo que ocurre
//	con un SIGKILL o un corte de corriente.
//
//	Restart(clock) construye un servidor NUEVO contra la MISMA base de
//	datos y el mismo Redis, con el Clock posicionado donde el test quiera.
func (h *serverHarness) Crash()
func (h *serverHarness) Restart(clk clock.Clock) *serverHarness
```

Lo esencial es que `Crash()` **no** es un `Close()` educado: si el harness hiciera flush al caer, el test verificaría el camino feliz y no la recuperación.

#### 5.6.2 Escenario base

```
t = T0            El jugador ordena unit.move (100,100) -> (103,102)
                  Se confirma en PostgreSQL:
                    unit_movements: status=ACTIVE
                                    path=[{100,100,0},{101,101,849},{102,102,1698},{103,102,2298}]
                                    start_time_ms=T0, arrival_time_ms=T0+2298
                    units:          status=MOVING, x=100, y=100
                                    (x,y NO se actualizan durante el movimiento: es reconstruible)

t = T0 + 1000     CRASH. Sin flush, sin cierre ordenado.

t = ?             RESTART con el Clock posicionado en el instante que el caso requiera.
```

#### 5.6.3 Casos obligatorios

| # | Test | Instante del reinicio | Estado esperado tras arrancar |
|---|---|---|---|
| **R1** | `Test_INV_PERSIST_002_RestartStateMatchesCommitted` | `T0 + 1000` | Movimiento sigue `ACTIVE`. Posición reconstruida = **`(101,101)`** (último waypoint con `tMs <= 1000`). `units.status = 'MOVING'`. La polilínea en memoria es idéntica a la persistida |
| **R2** | `TestRecovery_ResumedMovementCompletesAtOriginalArrival` | `T0 + 1000`, y luego se avanza el loop | El movimiento termina en `T0 + 2298` **exactamente**, no en `reinicio + 2298`. La caída no regala ni roba tiempo al jugador |
| **R3** | `TestRecovery_ArrivalExpiredDuringDowntimeCompletesImmediately` | `T0 + 5000` (la llegada venció durante la caída) | Al arrancar y **sin avanzar un solo tick**: `unit_movements.status = 'COMPLETED'`, `finished_at` fijado (el nombre real de la columna; no existe `ended_at`), `units.x = 103`, `units.y = 102`, `units.status = 'IDLE'`, `units.chunk_x`/`chunk_y` recalculados. Es la versión durable de H2 |
| **R4** | `TestRecovery_ArrivalExactlyAtRestartCompletes` | `T0 + 2298`, exactamente `arrival_time_ms` | Se completa: la condición del canon es `arrival_time_ms <= now`, inclusiva |
| **R5** | `TestRecovery_OneMillisecondBeforeArrivalResumes` | `T0 + 2297` | **No** se completa: sigue `ACTIVE` en `(102,102)` y termina 1 ms después |
| **R6** | `TestRecovery_MultipleActiveMovementsAllRecovered` | 100 unidades con movimientos `ACTIVE`, mitad vencidos y mitad en curso | Los 50 vencidos quedan `COMPLETED` con su tile final; los 50 en curso siguen `ACTIVE` con su posición reconstruida correcta |
| **R7** | `TestRecovery_IsIdempotentAcrossDoubleRestart` | Reiniciar dos veces seguidas | El segundo arranque no vuelve a completar lo ya completado ni emite eventos duplicados: un movimiento terminal nunca vuelve a `ACTIVE` (`INV-MOVE-007`), y aquí se comprueba contra la fila real |
| **R8** | `TestRecovery_CancelledAndCompletedMovementsAreNotReloaded` | Historial con filas `CANCELLED`, `COMPLETED` y `FAILED` | Solo se cargan las `ACTIVE`, por el índice parcial `unit_movements_active_idx`. El arranque no depende del tamaño del historial |
| **R9** | `TestRecovery_UnitPositionInDbMayLagAndIsReconciled` | `units.x/y` quedaron en `(100,100)` por diseño | El arranque **no** confía en `units.x/y` para una unidad con movimiento `ACTIVE`: reconstruye la posición evaluando la polilínea de `unit_movements.path` en el instante del arranque. No existe ninguna columna `position_time_ms`; el instante lo aporta el reloj, no la fila |
| **R14** | `TestRecovery_InvalidPolylineFailsWithoutTeleporting` | El mapa cambió durante la caída y un tile de la polilínea persistida ya no es transitable | El movimiento se cierra como `FAILED` y `units.x/y` **no cambian**: ante un dato dudoso no se teletransporta a nadie. Versión durable de H3 |
| **R10** | `Test_INV_PERSIST_001_CommittedSurvivesDisconnect` | Caída inmediatamente después del `COMMIT` y antes de emitir el delta al cliente | El movimiento existe tras el reinicio. La confirmación al cliente puede perderse; el estado durable no |
| **R11** | `TestRecovery_EpochMsIsNeverRewritten` | Reinicio con `world_state` poblado | `world_state.epoch_ms` no cambia: modificarlo reinterpretaría todos los `*_time_ms` persistidos |
| **R12** | `TestRecovery_SnapshotAfterRestartMatchesPolyline` | Reconexión de un cliente tras R1 | El `world.snapshot` trae la misma polilínea que había antes de la caída (nivel `contract` verifica su forma; aquí se verifica el contenido) |
| **R13** | `TestRecovery_RedisEmptyAfterCrashDoesNotLoseWorld` | `FLUSHDB` antes del reinicio (Redis se perdió con el host) | El mundo se reconstruye completo desde PostgreSQL; solo la presencia parte de cero, y las ciudades se reevalúan hacia `OFFLINE_PENDING` según las reglas del canon §9 |

#### 5.6.4 Esqueleto del caso R3

```go
func TestRecovery_ArrivalExpiredDuringDowntimeCompletesImmediately(t *testing.T) {
	requireIntegration(t)
	db := newTestDB(t)
	redis := newTestRedis(t)

	t0 := testEpochMs
	clk := clock.NewFakeClock(t0)
	srv := startServer(t, db, redis, clk)

	unitID := srv.SeedPlayerWithVillagerAt(t, world.Tile{X: 100, Y: 100})
	require.NoError(t, srv.SubmitMove(t, unitID, world.Tile{X: 103, Y: 102}))
	srv.AdvanceTicks(1) // fase 2: el comando se valida y persiste

	// Estado confirmado antes de la caída.
	mv := db.QueryActiveMovement(t, unitID)
	require.Equal(t, "ACTIVE", mv.Status)
	require.Equal(t, t0, mv.StartTimeMs)
	require.Equal(t, t0+2298, mv.ArrivalTimeMs)
	require.Equal(t, []int64{0, 849, 1698, 2298}, mv.PathTMs())

	// Caída sin cierre ordenado, con el movimiento a mitad de camino.
	clk.AdvanceMs(1000)
	srv.Crash()

	// El servidor vuelve DESPUÉS de la hora de llegada.
	restartClock := clock.NewFakeClock(t0 + 5000)
	srv2 := restartServer(t, db, redis, restartClock)

	// Sin avanzar un solo tick: el arranque ya debe haber cerrado el movimiento.
	mv2 := db.QueryLastMovement(t, unitID)
	require.Equal(t, "COMPLETED", mv2.Status)
	require.NotNil(t, mv2.FinishedAt)

	u := db.QueryUnit(t, unitID)
	require.Equal(t, int32(103), u.X)
	require.Equal(t, int32(102), u.Y)
	require.Equal(t, "IDLE", u.Status)
	require.Equal(t, int32(3), u.ChunkX) // 103 >> 5 == 3
	require.Equal(t, int32(3), u.ChunkY) // 102 >> 5 == 3

	require.Equal(t, 0, db.CountRows(t, "unit_movements WHERE status = 'ACTIVE'"))
	_ = srv2
}
```

### 5.7 Sesiones, tickets y migraciones

| # | Test | Aserciones |
|---|---|---|
| **I35** | `Test_INV_SEC_003_TicketRedeemableOnce` | El `jti` se consume en Redis (`ticket:jti:{jti}`); el segundo canje devuelve `UNAUTHORIZED`. La mitad en memoria ya está en verde: `TestTicketNoSePuedeReutilizar` y `TestFalloDelRegistroDeTicketsFallaCerrado` en `ticket_test.go`. **La tabla `sessions` no tiene columna `jti`** (sus columnas reales son `id`, `player_id`, `connected_at`, `disconnected_at`, `remote_addr`, `client_version`), así que la defensa durable contra el replay no existe hoy: si Redis se vacía, un ticket aún vigente podría recanjearse. Añadir esa columna con su `UNIQUE` es una decisión pendiente, no un hecho |
| **I36** | `TestSessions_ExpiredTicketIsRejected` | Ticket con `exp` vencido (TTL 60 s, canon §14) ⇒ `UNAUTHORIZED` y cierre `4401`. La verificación pura ya está cubierta por `TestTicketCaducado` |
| **I37** | `Test_INV_PERSIST_005_MigrationsOrderedIdempotentImmutable` | Sobre base vacía: las dos migraciones `.up.sql` en orden (`000001_initial_schema`, `000002_seed_catalogs`), luego las `.down.sql` en orden inverso, luego las `.up.sql` otra vez. El esquema final es idéntico y `schema_migrations` es coherente. Se aplican con **golang-migrate embebido**. **Etapa nocturna** (§8.2 de [strategy.md](./strategy.md)) |
| **I38** | `TestWorldChunks_BlobIsExactly1024Bytes` | Cada fila de `world_chunks` almacena exactamente 1024 bytes en `bytea` (32×32 tiles, 1 byte por `TerrainType`, orden fila-mayor). **`world_chunks` no es la fuente primaria**: el mapa se regenera desde la semilla en cada arranque y esta tabla guarda una copia para auditoría y para permitir mapas editados en el futuro |
| **I39** | `TestSeedCatalogsSeInsertaronCompletos` | Tras `000002_seed_catalogs`: civilizaciones `ROMAN`/`BYZANTINE`/`PERSIAN`/`NORSE` con su `traits jsonb`; facciones `ORDER`/`CHAOS`/`NEUTRAL`; eras `STONE_AGE(20)`/`BRONZE_AGE(50)`/`IRON_AGE(100)`/`CASTLE_AGE(150)`. Es de lo que dependen I1 y I5 |

---

## 6. Ejecución local

**No existen los scripts `test:integration`, `test:recovery` ni `test:unit`.** El único script del
nivel es `server:test:integration`, que ejecuta `go test -tags=integration ./...`; la recuperación es
parte de él, no un script aparte.

```powershell
# 1. Docker Desktop arrancado (el daemon no arranca solo; aquí no llegó a arrancar)
docker info

# 2. Servicios
pnpm run db:up

# 3. Suite completa de integración, recuperación incluida
$env:EO_INTEGRATION = "1"
$env:EO_TEST_POSTGRES_URL = "postgres://empires:empires_dev_password@localhost:5432/empires_test?sslmode=disable"
$env:EO_TEST_REDIS_URL = "redis://localhost:6379/1"
pnpm run server:test:integration
```

En Git Bash:

```bash
EO_INTEGRATION=1 \
EO_TEST_POSTGRES_URL='postgres://empires:empires_dev_password@localhost:5432/empires_test?sslmode=disable' \
EO_TEST_REDIS_URL='redis://localhost:6379/1' \
pnpm run server:test:integration
```

**Sin Docker arrancado**, `pnpm run server:test` termina en verde: la etiqueta `integration` excluye
estos ficheros de la compilación, así que ni siquiera aparecen como *skip*. Eso es correcto y
deliberado: ver §3. Y es exactamente la situación actual de esta máquina.

**Diagnóstico.** Cuando un test de integración falla, el estado queda en la base (§4.2). Inspección directa sin `psql` instalado:

```powershell
docker compose exec postgres psql -U empires -d empires_test -c "SELECT id, unit_id, status, start_time_ms, arrival_time_ms FROM unit_movements ORDER BY id;"
docker compose exec redis redis-cli -n 1 --scan --pattern "presence:player:*"
```

---

## 7. Documentos relacionados

- [strategy.md](./strategy.md) — reglas duras, gate, orden de etapas de CI.
- [unit-tests.md](./unit-tests.md) — la lógica que aquí se persiste, verificada sin infraestructura.
- [simulation-tests.md](./simulation-tests.md) — avance del loop con `FakeClock`.
- [contract-tests.md](./contract-tests.md) — forma de los mensajes emitidos tras la recuperación.
- [../database/schema.md](../database/schema.md) — tablas, `CHECK`s e índice único parcial.
- [../database/migrations.md](../database/migrations.md) — convención `NNNN_nombre.up.sql` / `.down.sql`.
- [../database/persistence-strategy.md](../database/persistence-strategy.md) — write-through, dirty-flag y estado reconstruible.
- [../operations/local-development.md](../operations/local-development.md) — Docker Compose en Windows y acceso sin `psql` ni `redis-cli`.
- [../operations/disaster-recovery.md](../operations/disaster-recovery.md) — el mismo escenario de recuperación, desde la perspectiva de operación.
