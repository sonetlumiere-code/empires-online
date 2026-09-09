# Estrategia de índices

Índices de PostgreSQL que **existen** en el esquema del MVP: DDL exacto con su nombre real, consulta que sirve cada uno, coste de escritura y guía de medición con `EXPLAIN ANALYZE`.

Todos los índices de este documento los crea `services/game-server/migrations/000001_initial_schema.up.sql`, en la misma migración que la tabla a la que pertenecen. Los nombres son los de esa migración: son el identificador operativo que aparece en `pg_stat_user_indexes` y en el mensaje de un error `23505`, así que citarlos mal no es cosmético.

El esquema está en [schema.md](./schema.md); las reglas de evolución, en [migrations.md](./migrations.md).

---

## 1. Principio de partida

Antes de listar índices conviene fijar el contexto, porque determina cuáles son realmente calientes:

**El game loop no consulta PostgreSQL.** La simulación vive en RAM. El tick a 10 Hz nunca hace I/O síncrono contra la base; la persistencia es asíncrona vía canal y workers (ver [../architecture/game-loop.md](../architecture/game-loop.md)). Por tanto **ninguna consulta de este documento se ejecuta 10 veces por segundo**. Se ejecutan en:

| Momento | Frecuencia | Sensibilidad a la latencia |
|---|---|---|
| Arranque del proceso (carga del mundo) | Una vez | Alta: determina el tiempo hasta `/ready` |
| Conexión de un jugador (`session.hello` → `world.snapshot`) | Por conexión | **Muy alta**: está en la ruta de la primera impresión |
| Flush de persistencia | Cada 50 ticks (5 s) | Media: si se acumula, `eo_persistence_queue_depth` crece |
| Escritura durable encolada (movimientos, presencia) | Por comando durable | Media: la respuesta al cliente **no** la espera; lo que sube es el retraso y la profundidad de la cola |
| Reconciliación de timers de protección | Al arrancar | Media |
| Barrido de retención | Periódica, fuera de hora punta | Baja |

Consecuencia práctica: se optimiza **el arranque y la conexión**, no un caudal de lecturas sostenido. Y como la mayoría de las escrituras vienen de un flush por lotes, el coste de escritura de los índices se paga concentrado, lo que hace que cada índice de más sea más visible, no menos.

---

## 2. Catálogo de consultas calientes

### 2.1 Cargar las unidades vivas de un jugador — `units_player_idx`

`UnitRepo.ListByPlayer`. Se ejecuta al conectar un jugador. Excluye las unidades muertas: no se cargan en memoria ni se envían al cliente.

```sql
SELECT id, player_id, city_id, unit_type, x, y, hp, max_hp, status
  FROM units
 WHERE player_id = $1 AND status <> 'DEAD'
 ORDER BY id;
```

```sql
CREATE INDEX units_player_idx ON units (player_id) WHERE status <> 'DEAD';
```

| Aspecto | Valor |
|---|---|
| Tipo | B-tree parcial |
| Selectividad | Muy alta: un jugador del MVP tiene 3 unidades sobre el total del mundo |
| Por qué parcial | Las unidades `DEAD` nunca se cargan. Excluirlas reduce el tamaño del índice de forma permanente y creciente: los cadáveres se acumulan, los vivos no |
| Por qué no compuesto con `status` | El predicado ya filtra por `status`; añadirlo como columna del índice sólo engordaría cada entrada sin mejorar la búsqueda |
| Plan esperado | `Index Scan` + acceso al heap. **No** un *index-only scan*: la consulta proyecta nueve columnas y el índice sólo contiene `player_id`. Un `INCLUDE` con las otras ocho lo haría posible, pero engordaría el índice y rompería el escenario HOT que §4.2 defiende |

Requisito del planificador: para usar un índice parcial, el `WHERE` de la consulta debe **implicar** el predicado del índice. `status <> 'DEAD'` es literalmente el predicado, así que basta. Si el código escribiera `status IN ('IDLE','MOVING','GARRISONED','HIDDEN')`, PostgreSQL **no** demostraría la implicación y haría un *seq scan*. Es un detalle que debe respetarse en el SQL del repositorio.

La carga completa del arranque (`UnitRepo.ListAlive`, `WHERE status <> 'DEAD' ORDER BY id`) **no** usa este índice ni ningún otro: lee la tabla entera, que es exactamente lo que quiere, y un *seq scan* es el plan correcto.

### 2.2 Unidades por chunk para el `world.snapshot` — `units_chunk_idx`

El interest management es por chunk, con radio 2 (`EO_INTEREST_RADIUS_CHUNKS=2`), es decir hasta 25 chunks alrededor del centro de vista.

**Ningún repositorio emite hoy esta consulta**: el snapshot se sirve desde RAM (ver la nota del final de esta sección) y las dos únicas sentencias que tocan `chunk_x`/`chunk_y` son el `INSERT` de `UnitRepo.Create` y el `UPDATE` del flush. El índice se crea con la tabla porque es el acceso previsto para el arranque en frío y para reconstrucciones puntuales, y porque añadirlo después sobre `units` ya poblada exigiría `CREATE INDEX CONCURRENTLY` (§4 de [migrations.md](./migrations.md)). La consulta que serviría es:

```sql
SELECT id, player_id, unit_type, status, x, y, chunk_x, chunk_y, hp, max_hp
  FROM units
 WHERE chunk_x BETWEEN $1 AND $2
   AND chunk_y BETWEEN $3 AND $4
   AND status <> 'DEAD';
```

```sql
CREATE INDEX units_chunk_idx ON units (chunk_x, chunk_y) WHERE status <> 'DEAD';
```

| Aspecto | Valor |
|---|---|
| Tipo | B-tree compuesto parcial |
| Orden de columnas | `(chunk_x, chunk_y)`. El B-tree ordena lexicográficamente: con un rango en la primera columna, la segunda sólo filtra dentro de cada valor de `chunk_x`. Con 16 chunks por fila en el mundo MVP, el rango de `chunk_x` cubre como mucho 5 valores, así que el escaneo recorre 5 subárboles pequeños. Aceptable y muy predecible |
| Alternativa evaluada | Un `chunk_id` entero único con `IN (...)` de 25 valores. Sería más compacto, pero `chunk_id` depende de `chunksPerRow` = `EO_WORLD_WIDTH / EO_CHUNK_SIZE` e indexar por él ataría el índice a la configuración del mundo. Se descartó, y por eso `world_chunks` tampoco tiene esa columna |

**Nota importante sobre la autoridad**: en régimen normal el snapshot se sirve **desde RAM** —la goroutine de la conexión ni siquiera lee el estado: pide un `RequestSnapshot` por el canal de comandos y el loop responde—, no desde esta consulta. Este índice existe para el arranque en frío y para reconstrucciones puntuales. Es caliente en frecuencia baja y en criticidad alta.

### 2.3 Movimientos `ACTIVE` al arrancar — `unit_movements_active_idx`

`MovementRepo.ListActive`. Consulta de recuperación tras un reinicio: recorre todos los movimientos vivos del mundo y `simulation.Hydrate` los clasifica en "ya llegaron mientras estábamos caídos" y "hay que reanudarlos".

```sql
SELECT id, unit_id, path, target_x, target_y, start_time_ms, arrival_time_ms, status
  FROM unit_movements
 WHERE status = 'ACTIVE'
 ORDER BY id;
```

```sql
CREATE INDEX unit_movements_active_idx
    ON unit_movements (arrival_time_ms, id)
    WHERE status = 'ACTIVE';
```

| Aspecto | Valor |
|---|---|
| Tipo | B-tree compuesto parcial, ordenado por tiempo de llegada y desempatado por `id` |
| Por qué parcial por `status` | `unit_movements` es la tabla que más crece: cada orden deja una fila permanente. Las `ACTIVE` son, como mucho, una por unidad viva. Un índice total contendría el historial completo; el parcial contiene sólo el conjunto de trabajo — órdenes de magnitud menos entradas, y prácticamente siempre en caché |
| Por qué `arrival_time_ms` primero | Es la columna por la que se filtra por rango: `WHERE status = 'ACTIVE' AND arrival_time_ms <= $now` separa en un descenso de árbol los movimientos ya vencidos de los que siguen en curso, que es la partición exacta que hace la recuperación |
| Por qué `id` como segunda columna | Desempate determinista y estable |
| Salvedad honesta | La consulta de `ListActive` que hay hoy **ordena por `id`**, no por `arrival_time_ms`, así que se apoya en el predicado parcial del índice pero **no** en su orden, y el plan puede incluir un nodo `Sort`. Es aceptable: se ejecuta una vez por arranque, sobre un conjunto pequeño |

Este índice **no** sustituye a `unit_movements_one_active_per_unit` (§2.8): uno sirve consultas, el otro impone `INV-MOVE-001`.

`MovementRepo.GetActiveByUnit` (`WHERE unit_id = $1 AND status = 'ACTIVE'`) y el `UPDATE ... WHERE unit_id = $1 AND status = 'ACTIVE'` que cancela el movimiento anterior sí usan el índice único parcial de §2.8: para ellos es, además de una restricción, el índice de acceso.

### 2.4 Resolver la ciudad por owner — `cities_owner_idx`

`CityRepo.GetByOwner`. Se ejecuta al conectar (el `world.snapshot` inicial se centra en la ciudad del jugador).

```sql
SELECT id, owner_player_id, name, center_x, center_y, era, population,
       population_limit, presence_state, last_online_at, last_offline_at,
       protection_until, version
  FROM cities
 WHERE owner_player_id = $1
 ORDER BY id
 LIMIT 1;
```

```sql
CREATE INDEX cities_owner_idx ON cities (owner_player_id);
```

| Aspecto | Valor |
|---|---|
| Tipo | B-tree simple |
| Por qué no `UNIQUE` | En MVP un jugador tiene exactamente una ciudad, pero eso lo valida el dominio —la migración `000001` **no** crea ningún índice único sobre `owner_player_id`—, no una verdad permanente del producto: el multi-ciudad está previsto. Un `UNIQUE` habría que retirarlo, y retirar una restricción sobre una tabla viva es una migración de contracción evitable ([migrations.md](./migrations.md#3-cambios-compatibles-hacia-adelante-expand--migrate--contract)). El `ORDER BY id LIMIT 1` de la consulta es la consecuencia visible de esa decisión |
| Por qué no parcial | `cities` es pequeña y no tiene filas "muertas" que excluir |
| Cardinalidad | Con una ciudad por jugador, este índice es tan selectivo como una PK |

### 2.5 Ciudades en `OFFLINE_PENDING` cuyo cooldown venció — `cities_offline_pending_idx`

`CityRepo.ListPendingProtection`.

```sql
SELECT id
  FROM cities
 WHERE presence_state = 'OFFLINE_PENDING'
   AND last_offline_at IS NOT NULL
   AND last_offline_at <= $1
 ORDER BY id;
```

```sql
CREATE INDEX cities_offline_pending_idx
    ON cities (last_offline_at)
    WHERE presence_state = 'OFFLINE_PENDING';
```

| Aspecto | Valor |
|---|---|
| Tipo | B-tree parcial sobre el instante en que la ciudad quedó pendiente |
| Reparto columna / predicado | La columna que se compara **por rango** (`last_offline_at`) va en el índice; la que se compara por igualdad y tiene tres valores posibles (`presence_state`) va en el predicado parcial. Es el reparto correcto y no el contrario |
| Coste | El conjunto candidato son las ciudades actualmente en `OFFLINE_PENDING` — un estado transitorio de 300 s por defecto — y la consulta es un descenso de árbol que termina en el primer valor mayor que el corte |

**Quién decide la transición.** Esta consulta **no** es un decisor. `ONLINE → OFFLINE_PENDING → PROTECTED` lo decide exclusivamente la fase 5 del tick, en RAM, comparando contra `DisconnectGrace` (= `EO_PRESENCE_TTL_SECONDS`) y contra el cooldown; la base recibe después el `UPDATE` encolado. `ListPendingProtection` es una consulta de **arranque y reconciliación**: sirve para que un proceso que acaba de levantarse sepa qué ciudades quedaron a medio camino, igual que §2.2 sirve el arranque en frío del snapshot. De hecho hoy **no la llama nadie en régimen**: `simulation.ProcessTimers` recorre las ciudades en RAM con `city.ShouldEngageProtection` y no consulta la base. El método del repositorio existe para la reconciliación de arranque y para diagnóstico. Si además la evaluara un worker en régimen, habría dos decisores sobre `cities.presence_state` y el worker podría proteger la ciudad de un jugador que acaba de reconectar, porque no ve el espejo de presencia en RAM.

El cooldown **no está materializado** en una columna `protection_eligible_at_ms`: el corte se calcula en la aplicación (`now - EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS`) y se pasa como parámetro. Así el valor de configuración vive en un solo sitio y no queda congelado dentro de una columna ni de un índice sobre expresión.

### 2.6 Retención de claves de idempotencia — `idempotency_keys_expiry_idx`

```sql
DELETE FROM idempotency_keys WHERE expires_at < now();
```

```sql
CREATE INDEX idempotency_keys_expiry_idx ON idempotency_keys (expires_at);
```

Sirve al barrido de retención: sin él, la limpieza escanearía la tabla entera cada vez. Ese barrido **todavía no está implementado**, coherentemente con que la tabla aún no tenga escritor. La búsqueda por clave (`WHERE player_id = $1 AND request_id = $2`) no necesita índice propio: **la clave primaria compuesta `(player_id, request_id)` ya lo es**, y además impide físicamente insertar dos veces la misma clave — dos ejecuciones concurrentes del mismo comando colisionan con `23505` en vez de duplicar el efecto. El prefijo `player_id` agrupa las claves del mismo jugador en páginas contiguas, lo que ayuda a la localidad de caché en una sesión activa.

Recuérdese (§14 de [schema.md](./schema.md)) que **el MVP todavía no escribe esta tabla**: la deduplicación vigente es `idem:{playerId}:{requestId}` en Redis con `SETNX` y TTL 300 s. El índice existe para cuando el respaldo durable entre en servicio.

### 2.7 Leer el terreno — PK `(chunk_x, chunk_y)`

```sql
SELECT chunk_x, chunk_y, terrain FROM world_chunks ORDER BY chunk_y, chunk_x;
```

**Sin índice adicional.** `WorldRepo.LoadTerrain` lee la tabla entera de una vez, ordenada, y la PK compuesta es el único índice existente. En MVP la tabla tiene **256 filas de 1024 bytes** de terreno: 256 KiB. Cabe entera en `shared_buffers` y se lee, como mucho, una vez por arranque —y sólo para verificar contra el mapa regenerado desde la semilla—. A partir de ahí el pathfinding y las comprobaciones de transitabilidad no vuelven a tocar PostgreSQL.

### 2.8 Índices únicos que imponen invariantes

Dos índices del esquema no existen por rendimiento sino por corrección. Se listan aquí porque tienen coste de escritura como cualquier otro:

```sql
-- Garantía física de INV-MOVE-001.
CREATE UNIQUE INDEX unit_movements_one_active_per_unit
    ON unit_movements (unit_id)
    WHERE status = 'ACTIVE';

-- Como máximo un tratado vigente de cada tipo entre dos jugadores.
CREATE UNIQUE INDEX treaties_one_active_per_pair_and_type
    ON treaties (player_a_id, player_b_id, treaty_type)
    WHERE status = 'ACTIVE';
```

El primero es la garantía física de `INV-MOVE-001` (una unidad tiene como máximo un movimiento `ACTIVE`); su justificación completa está en [schema.md](./schema.md#101-unit_movements_one_active_per_unit-es-la-garantía-física-de-inv-move-001). El segundo aplica el mismo patrón a los tratados, y es directo porque `treaties_canonical_pair` obliga a `player_a_id < player_b_id`: no hace falta normalizar el par con `LEAST`/`GREATEST` dentro del índice. Ambos son parciales, y esa parcialidad es la que los mantiene diminutos: sólo contienen el estado vivo.

La guarnición no necesita índice equivalente: `garrisons` tiene `unit_id` como **clave primaria**, así que "una unidad está guarnecida en una sola ciudad" ya lo impone la PK.

### 2.9 Los índices restantes

Existen, sirven consultas reales o previstas, y conviene no olvidarlos al medir:

| Índice | DDL | Para qué |
|---|---|---|
| `units_city_idx` | `ON units (city_id) WHERE city_id IS NOT NULL` | Recálculo de población: `count(*)` de las unidades vivas de una ciudad (`CityRepo.UpdatePopulation`). Es parcial por `city_id IS NOT NULL`, no por `status`, porque la consulta filtra el estado dentro del `count` |
| `sessions_player_idx` | `ON sessions (player_id, connected_at DESC)` | Historial de conexiones de un jugador, de la más reciente hacia atrás. Tabla aún sin escritor (§13 de schema.md) |
| `garrisons_city_idx` | `ON garrisons (city_id)` | Qué unidades hay guarnecidas en una ciudad. Lógica diferida |
| `world_events_type_time_idx` | `ON world_events (event_type, occurred_at DESC)` | Auditoría: los últimos eventos de un tipo |
| `world_events_player_idx` | `ON world_events (player_id, occurred_at DESC) WHERE player_id IS NOT NULL` | Auditoría: los últimos eventos de un jugador. Parcial porque los eventos sin actor no se consultan por esta vía |

---

## 3. Resumen de índices del MVP

Inventario completo, con los nombres reales de la migración `000001`:

| Índice | Tabla | Tipo | Consulta que sirve |
|---|---|---|---|
| PK | todas | B-tree único | Acceso por identidad |
| `units_player_idx` | `units` | B-tree parcial | §2.1 unidades vivas del jugador |
| `units_chunk_idx` | `units` | B-tree compuesto parcial | §2.2 snapshot por chunk |
| `units_city_idx` | `units` | B-tree parcial | §2.9 recálculo de población |
| `unit_movements_active_idx` | `unit_movements` | B-tree compuesto parcial | §2.3 recuperación tras crash |
| `unit_movements_one_active_per_unit` | `unit_movements` | B-tree único parcial | `INV-MOVE-001`; además, acceso al movimiento activo de una unidad |
| `cities_owner_idx` | `cities` | B-tree | §2.4 ciudad por owner |
| `cities_offline_pending_idx` | `cities` | B-tree parcial | §2.5 cooldown de protección |
| `cities_unique_center` | `cities` | B-tree único (constraint `UNIQUE`) | Un tile, una ciudad |
| PK `(player_id, request_id)` | `idempotency_keys` | B-tree único compuesto | §2.6 deduplicación durable |
| `idempotency_keys_expiry_idx` | `idempotency_keys` | B-tree | §2.6 retención |
| PK `(chunk_x, chunk_y)` | `world_chunks` | B-tree único compuesto | §2.7 lectura del terreno |
| `sessions_player_idx` | `sessions` | B-tree compuesto | §2.9 historial de conexiones |
| `treaties_one_active_per_pair_and_type` | `treaties` | B-tree único parcial | Invariante de tratado |
| `garrisons_city_idx` | `garrisons` | B-tree | §2.9 guarnición por ciudad |
| `world_events_type_time_idx` | `world_events` | B-tree compuesto | §2.9 auditoría por tipo |
| `world_events_player_idx` | `world_events` | B-tree compuesto parcial | §2.9 auditoría por jugador |
| `players_username_key`, `civilizations_code_key`, `factions_code_key`, `eras_code_key`, `eras_ordinal_key` | catálogos y `players` | B-tree único | Unicidad de identificadores naturales (índices implícitos de los `UNIQUE`) |

Todos son B-tree. No hay GIN, GiST, BRIN ni hash en el MVP. Los `jsonb` (`unit_movements.path`, `world_events.payload`, `idempotency_keys.response`, `civilizations.traits`, `territories.resource_modifiers`) **no están indexados**: nunca se consultan por su contenido, sólo se leen enteros por la clave de su fila. Indexar un `jsonb` con GIN sin una consulta que lo aproveche es pagar el coste de escritura más caro del catálogo a cambio de nada.

---

## 4. Coste de escritura y por qué no se indexa todo

### 4.1 Qué cuesta exactamente un índice

Cada índice adicional impone, por cada fila insertada o actualizada:

1. **Una entrada de índice más que escribir**, con su posible división de página.
2. **Más WAL**. Las divisiones de página de índice generan registros de WAL completos; con `full_page_writes` activo, el primer cambio de una página tras un checkpoint escribe la página entera. Un índice de más multiplica esto por cada escritura.
3. **Más trabajo para autovacuum**, que debe limpiar las entradas muertas de cada índice, no sólo de la tabla.
4. **Memoria de caché ocupada** en `shared_buffers` que deja de estar disponible para las páginas realmente calientes.

### 4.2 El coste específico que importa aquí: bloquear las actualizaciones HOT

Este es el argumento decisivo en `units`. PostgreSQL puede aplicar una **actualización HOT** (*Heap-Only Tuple*) cuando se cumplen dos condiciones: la nueva versión de la fila cabe en la misma página, y **ninguna columna indexada cambia**. Una actualización HOT no toca ningún índice: es dramáticamente más barata y no genera basura de índice para autovacuum.

`units` recibe una actualización por unidad sucia cada 50 ticks (5 s) desde el flush de persistencia. Las columnas que cambian en ese `UPDATE ... FROM unnest(...)` son exactamente `x`, `y`, `status`, `chunk_x`, `chunk_y` — más `updated_at`, que pone el trigger.

De ellas, sólo `chunk_x` y `chunk_y` están indexadas (y `status` aparece en el predicado de dos índices parciales, lo que también obliga a tocarlos cuando una unidad pasa a `DEAD`). Y eso tiene una consecuencia muy concreta y muy favorable: **una unidad que se mueve dentro del mismo chunk cambia `x` e `y` pero no `chunk_x` ni `chunk_y`**, así que su actualización sigue siendo candidata a HOT. Con chunks de 32 × 32 tiles, la inmensa mayoría de los pasos de movimiento son intra-chunk. Sólo el paso que cruza una frontera de chunk fuerza actualización de índice.

Ese es exactamente el motivo por el que **`x` e `y` no están indexadas**. Un índice sobre `(x, y)` destruiría el escenario HOT: cada consolidación de posición actualizaría un índice, cada lote de flush generaría entradas muertas, y autovacuum tendría que perseguirlas. Además no serviría para nada: no existe ninguna consulta del MVP que busque unidades por tile exacto — el interest management es por chunk y la colisión por tile se resuelve en RAM.

Otra consecuencia del mismo razonamiento: `units` **no** tiene columna `version`. Si la tuviera, cambiaría en cada flush y no aportaría nada, porque el lote de flush es el único escritor de esas filas.

`units` **no** lleva hoy un `fillfactor` reducido: la tabla se crea con el 100 % por defecto. Bajarlo es la palanca disponible si la medición muestra que las actualizaciones dejan de ser HOT por falta de sitio en la página:

```sql
-- Sólo cuando la medición lo justifique; no está aplicado.
ALTER TABLE units SET (fillfactor = 85);
```

Dejar un 15 % de cada página libre daría sitio para que las nuevas versiones de fila quepan en la misma página: espacio en disco cambiado por escrituras más baratas. Se decidirá con `pg_stat_user_tables.n_tup_hot_upd` frente a `n_tup_upd` sobre un mundo con volumen, no antes.

### 4.3 La regla

> Un índice se añade cuando existe una consulta concreta, escrita, que lo usa, y se ha comprobado con `EXPLAIN ANALYZE` que sin él el plan es inaceptable.

No se añaden índices "por si acaso", ni "porque la columna aparece en un `WHERE` alguna vez", ni "porque es una FK". PostgreSQL **no** crea índices automáticamente para las claves foráneas, y no todas los necesitan: sólo si se consulta por esa columna o si se borran filas de la tabla referenciada con frecuencia (el chequeo de `ON DELETE CASCADE`/`RESTRICT` escanea la tabla hija). En el MVP, borrar un jugador es una operación de soporte excepcional; los índices por `player_id` que existen se justifican por consultas de lectura, no por las FKs.

Índices explícitamente **descartados** y por qué:

| Índice descartado | Motivo |
|---|---|
| `units (x, y)` | Ninguna consulta busca por tile; rompería HOT en la tabla más actualizada |
| `units (status)` | Baja cardinalidad (5 valores) y muy sesgada. Un *seq scan* es más barato que el índice; `status` ya vive en predicados parciales, que es donde sirve |
| `unit_movements (unit_id)` total | El histórico crecería sin límite. El índice único parcial ya cubre las consultas por unidad viva; el histórico se consulta por rango de tiempo en investigaciones puntuales, donde un escaneo es aceptable |
| `unit_movements (status)` | Mismo argumento: cuatro valores y muy sesgado hacia los terminales. El predicado parcial de los dos índices existentes es la forma correcta de usar esa columna |
| `world_events` GIN sobre `payload` | No hay consulta por contenido del JSON en MVP. Se reevaluará cuando exista |
| `civilizations` GIN sobre `traits` | El catálogo tiene cuatro filas y se carga entero al arrancar |
| Cualquier índice en `world_chunks` más allá de la PK | 256 filas, leídas de una vez |

---

## 5. Indexado espacial: `(chunk_x, chunk_y)` desnormalizado

### 5.1 La decisión

El "índice espacial" del MVP es un B-tree compuesto sobre dos columnas enteras desnormalizadas en `units`, calculadas por la aplicación como `x / chunkSize` e `y / chunkSize`. **No se usa PostGIS ni ningún índice GiST/SP-GiST.** `cities` no lleva chunk desnormalizado: una ciudad se localiza por su dueño, no por rango espacial.

### 5.2 Por qué se prefiere a una extensión geoespacial

**El interés es por chunk, no por radio.** El interest management del protocolo suscribe al jugador a chunks completos, con radio de 2 chunks. La consulta natural no es "unidades a menos de N tiles de este punto" sino "unidades en este conjunto de 25 celdas de rejilla". Eso es una comparación de enteros, no una consulta geométrica. Un índice GiST resolvería con solapamiento de bounding boxes un problema que aquí se resuelve con igualdad exacta — más maquinaria, más lento y menos predecible.

**El mundo es una rejilla discreta y minúscula.** 512 × 512 tiles, 256 chunks. El espacio de valores de `chunk_x` y `chunk_y` es `[0, 16)`. Un B-tree sobre ese dominio es esencialmente una tabla de dispersión con caché perfecta. PostGIS está diseñado para geometrías arbitrarias en espacios continuos con millones de objetos; nada de eso describe este problema.

**Coste operativo cero.** PostGIS es una extensión que hay que instalar en la imagen, versionar junto al servidor, y considerar en cada actualización mayor de PostgreSQL y en cada restauración de backup. El MVP no requiere **ninguna** extensión (ver [migrations.md](./migrations.md#6-migraciones-del-mvp)), lo que significa que la imagen oficial sirve tal cual y que un `pg_dump` restaura en cualquier instancia estándar. En un entorno de desarrollo Windows donde ni siquiera `psql` está instalado y todo el acceso pasa por `docker compose exec`, cada dependencia añadida se paga varias veces.

**Determinismo.** `chunkX = x / chunkSize` es una división entera de enteros no negativos, idéntica en Go, en TypeScript y en SQL. Nótese que **no** se escribe como `x >> 5`: el desplazamiento sólo equivale a la división si `chunk_size` es exactamente 32, y `EO_CHUNK_SIZE` es configuración (rango 1–256) sin ninguna obligación de ser potencia de dos. Los operadores geométricos con aritmética de punto flotante tampoco ofrecen la garantía de determinismo, que es un principio no negociable del proyecto.

**Postgres no es el camino caliente.** La posición autoritativa vive en RAM; el interest management se calcula sobre estructuras en memoria. Las consultas por chunk contra la base son de arranque en frío y reconstrucción. Introducir una extensión completa para acelerar una consulta que se ejecuta al arrancar sería optimizar en el sitio equivocado.

### 5.3 Coherencia de la desnormalización

`chunk_x`/`chunk_y` son datos derivados de `x`/`y`, y todo dato derivado puede desincronizarse. Tres decisiones lo contienen:

1. **Un único punto de escritura.** `UnitRepo` recibe el `chunkSize` al construirse y calcula el chunk tanto en `Create` como en `FlushPositions`. Ninguna sentencia SQL suelta escribe `x` sin escribir el chunk: las dos únicas que escriben posición lo hacen en la misma sentencia.
2. **No es una columna generada** (`GENERATED ALWAYS AS (x / 32) STORED`). Sería automáticamente coherente, pero congelaría `EO_CHUNK_SIZE` dentro del esquema, en contra de la regla de configuración única del canon. La coherencia se compra con disciplina de código, no con un valor de gameplay hardcodeado en el DDL.
3. **Test de integración de coherencia.** Una prueba verifica, tras una sesión de simulación, que `chunk_x = x / chunk_size` para toda fila, leyendo `chunk_size` de `world_state`:

```sql
SELECT count(*)
  FROM units u, world_state w
 WHERE u.chunk_x <> u.x / w.chunk_size
    OR u.chunk_y <> u.y / w.chunk_size;
-- debe devolver 0
```

---

## 6. Medición con `EXPLAIN ANALYZE`

### 6.1 Procedimiento

`psql` no está instalado en la máquina de desarrollo: toda sesión SQL pasa por Docker, con el daemon arrancado.

```powershell
pnpm run db:up     # docker compose up -d postgres redis
pnpm run db:psql   # docker compose exec postgres psql -U empires -d empires
```

Para cada consulta del §2, sobre una base con volumen representativo:

```sql
EXPLAIN (ANALYZE, BUFFERS, VERBOSE, SETTINGS)
SELECT id, player_id, city_id, unit_type, x, y, hp, max_hp, status
  FROM units
 WHERE player_id = '00000000-0000-0000-0000-000000000001'
   AND status <> 'DEAD'
 ORDER BY id;
```

`ANALYZE` **ejecuta** la consulta. Para medir un `UPDATE` o `DELETE` sin efectos, se envuelve en una transacción que se revierte:

```sql
BEGIN;
EXPLAIN (ANALYZE, BUFFERS) DELETE FROM idempotency_keys WHERE expires_at < now();
ROLLBACK;
```

### 6.2 Qué mirar en la salida

| Señal | Interpretación |
|---|---|
| `Seq Scan on units` en una consulta del §2 | El índice no se está usando. Causas típicas: el `WHERE` no implica el predicado del índice parcial, hay una conversión de tipo implícita, o la tabla es tan pequeña que el planificador prefiere el escaneo (normal en desarrollo con pocas filas — hay que medir con volumen) |
| `rows=N` estimado vs `actual rows=M` con desviación grande | Estadísticas obsoletas. `ANALYZE units;` y volver a medir. Si persiste, considerar `ALTER TABLE ... ALTER COLUMN ... SET STATISTICS` |
| `Buffers: shared read=` alto frente a `shared hit=` | Se está leyendo de disco. Puede ser caché fría o un índice demasiado grande para `shared_buffers` |
| `Index Scan` donde se esperaba `Index Only Scan` | Faltan columnas en el índice o el mapa de visibilidad está desactualizado (`VACUUM`). Ninguna consulta del §2 espera hoy un *index-only scan*: todos los índices son de una o dos columnas y sin `INCLUDE` |
| Nodo `Sort` en §2.3 | Esperado mientras `ListActive` ordene por `id` y el índice lo haga por `arrival_time_ms`. Sólo es un problema si el conjunto `ACTIVE` deja de ser pequeño |
| `Rows Removed by Filter` alto | El índice devuelve muchas filas que luego se descartan: el predicado del índice parcial o el orden de columnas del compuesto están mal elegidos |

### 6.3 Umbrales de alerta de latencia

La métrica `eo_database_latency_seconds` (histogram, ver [../operations/monitoring.md](../operations/monitoring.md)) es la fuente de alertas. La observa el worker de la cola de persistencia alrededor de **cada intento** de cada trabajo durable; el alta de jugador, que no pasa por la cola, no queda cubierta por ella. Objetivos por clase de consulta:

| Clase de consulta | p50 objetivo | p95 objetivo | Alerta (warning) | Alerta (crítica) |
|---|---|---|---|---|
| Punto por PK o índice único (§2.4, §2.6) | < 1 ms | < 5 ms | p95 > 10 ms durante 5 min | p95 > 50 ms durante 2 min |
| Rango pequeño (§2.1, §2.2, §2.5) | < 3 ms | < 20 ms | p95 > 40 ms durante 5 min | p95 > 150 ms durante 2 min |
| Transacción durable de movimiento (§2.8) | < 5 ms | < 25 ms | p95 > 50 ms durante 5 min | p95 > 200 ms durante 2 min |
| Lote de flush de persistencia | < 50 ms | < 200 ms | p95 > 400 ms durante 10 min | p95 > 1 s, o `eo_persistence_queue_depth` creciendo de forma sostenida |
| Bootstrap completo (§2.3 + carga de mapa) | — | — | > 5 s | > 15 s (retrasa `/ready`) |

Estos son objetivos de diseño para el MVP con un mundo de 512 × 512 y un puñado de jugadores, **no cifras medidas**: los tests de integración contra PostgreSQL y Redis reales están escritos pero todavía no se han ejecutado, porque el daemon de Docker no arrancó en la máquina de desarrollo. Se revisan con datos reales en cuanto haya un entorno con carga.

**Correlación obligatoria.** Una alerta de `eo_database_latency_seconds` se interpreta junto a `eo_persistence_queue_depth` y `eo_game_tick_duration_seconds`. Si la latencia de base sube pero el tick no se degrada, el problema está contenido en el camino asíncrono (que es exactamente lo que el diseño pretende). Si sube el tick a la vez, hay que buscar I/O síncrona colada en el loop: eso es un bug de arquitectura, no un problema de índices.

### 6.4 Instrumentación permanente

| Herramienta | Uso |
|---|---|
| `pg_stat_statements` | Extensión de diagnóstico, activada **sólo** en entornos de desarrollo y staging. Identifica las consultas por tiempo total acumulado, que es lo que importa, no por tiempo por ejecución |
| `log_min_duration_statement` | `200ms` en desarrollo, para que toda consulta lenta quede en el log de PostgreSQL con su texto |
| `auto_explain` | En desarrollo, con `auto_explain.log_min_duration = '200ms'` y `auto_explain.log_analyze = on`, para capturar el plan real de la consulta lenta sin tener que reproducirla |
| `pg_stat_user_indexes` | Detecta índices **no usados**: `idx_scan = 0` tras un periodo representativo indica un índice que sólo cuesta escrituras. Se retira con `DROP INDEX CONCURRENTLY` |

```sql
SELECT relname, indexrelname, idx_scan, pg_size_pretty(pg_relation_size(indexrelid))
  FROM pg_stat_user_indexes
 ORDER BY idx_scan ASC, pg_relation_size(indexrelid) DESC;
```

Esa consulta se ejecuta como parte de la revisión periódica del esquema. Un índice grande con `idx_scan = 0` es una regresión de rendimiento silenciosa, y retirarlo es tan legítimo como añadirlo.

---

## Documentos relacionados

- [schema.md](./schema.md) — definición de tablas y columnas indexadas
- [migrations.md](./migrations.md) — `CREATE INDEX CONCURRENTLY` y por qué no cabe en una transacción
- [persistence-strategy.md](./persistence-strategy.md) — dirty-set, cola de persistencia y lotes de flush
- [../architecture/game-loop.md](../architecture/game-loop.md) — por qué el tick no consulta PostgreSQL
- [../architecture/persistence.md](../architecture/persistence.md) — capas de estado y contratos de repositorio
- [../operations/monitoring.md](../operations/monitoring.md) — métricas Prometheus y alertas
