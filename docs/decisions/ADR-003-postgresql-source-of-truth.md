# ADR-003: PostgreSQL como fuente de verdad durable

Propósito: fijar PostgreSQL como único almacén durable del estado del mundo y explicar por qué sus garantías transaccionales y sus constraints son parte del diseño del dominio, no un detalle de infraestructura.

- **Estado:** Aceptado
- **Fecha:** 2026-09-09
- **Decisores:** equipo de arquitectura
- **Relacionados:** [ADR-001](ADR-001-game-server-language.md), [ADR-002](ADR-002-authoritative-server.md), [ADR-004](ADR-004-redis-hot-state.md), [ADR-011](ADR-011-movement-timed-polyline.md)

---

## Contexto

El canon técnico define tres capas de estado y su jerarquía es explícita:

| Capa | Contenido | Vida |
|---|---|---|
| **PostgreSQL** | *durable source of truth* | Sobrevive a reinicios, despliegues y pérdidas de proceso |
| **Redis** | *hot / transient state*: presencia, sesiones, locks, cooldowns, caché | Puede perderse sin perder el juego ([ADR-004](ADR-004-redis-hot-state.md)) |
| **RAM del Game Server** | simulación activa | Se pierde en cada reinicio; se reconstruye desde PostgreSQL |

Este ADR decide qué tecnología ocupa la primera fila. Las fuerzas en juego:

- **El mundo es persistente 24/7 y evoluciona sin jugadores conectados.** No hay "fin de partida" en el que volcar estado: la base de datos *es* el juego cuando el proceso no está corriendo.
- **Existen invariantes de dominio que no pueden violarse jamás.** El más ilustrativo: *una unidad tiene como máximo un movimiento `ACTIVE`* (`INV-MOVE-001`). Una nueva orden de movimiento debe cancelar la anterior (`CANCELLED`) y crear la nueva **dentro de la misma transacción**. Si ese invariante se rompe, la posición autoritativa de una unidad deja de estar definida, porque se derivaría de dos polilíneas distintas.
- **Recuperación tras crash como requisito de primer nivel.** Al arrancar, el servidor carga los movimientos `ACTIVE`; si `arrival_time_ms <= now` completa el movimiento inmediatamente (snap al tile final), y si no, lo reanuda desde la polilínea. Esto solo funciona si lo escrito antes del crash estaba realmente durable y era internamente coherente.
- **Estructuras de datos heterogéneas.** Junto a filas planas y muy relacionales (`players`, `cities`, `units`, `territories`, `treaties`) conviven dos formas que no son tabulares: la **polilínea temporizada** de `unit_movements` —un array de waypoints `{x, y, tMs}` de longitud variable, hasta cientos de elementos— y los **chunks del mundo** en `world_chunks`, un `bytea` de 1024 bytes por chunk (32 × 32 tiles a un byte de `TerrainType` por tile).
- **Aritmética temporal determinista.** Los instantes de simulación se guardan como `bigint` en epoch milliseconds (`*_time_ms`) además del `timestamptz`, para poder operar sin conversiones ni zonas horarias.
- **El tick no puede bloquearse.** El presupuesto es de 100 ms y la fase 8 del tick solo *encola* persistencia; la escritura real la hacen workers fuera del loop ([../architecture/game-loop.md](../architecture/game-loop.md)). Cualquier almacén elegido debe tolerar ese patrón: escrituras desde un pool de conexiones concurrente, nunca desde el hilo de simulación.
- **Volumen esperado en el MVP.** Mundo de 512 × 512 tiles, 256 chunks, un puñado de miles de entidades. Ni el volumen ni la tasa de escritura son extremos; lo exigente es la **corrección**, no la escala.
- **Restricción operativa real.** `psql` no está instalado en la máquina de desarrollo; el acceso se hace vía `docker compose exec`. Los tests de integración requieren Docker Desktop iniciado y se activan con `EO_INTEGRATION=1`.

## Decisión

**PostgreSQL es la fuente de verdad durable de Empires Online.** Todo estado que deba sobrevivir a un reinicio del proceso vive en PostgreSQL y en ningún otro sitio.

Alcance concreto:

- Conexión mediante `EO_POSTGRES_URL`; driver `pgx` desde el módulo Go.
- Migraciones versionadas en `services/game-server/migrations/` con el formato `NNNN_nombre.up.sql` / `NNNN_nombre.down.sql`, y control en la tabla `schema_migrations`.
- Tablas MVP creadas y usadas: `players`, `civilizations`, `factions`, `eras`, `cities`, `units`, `unit_movements`, `world_state`, `world_chunks`, `sessions`, `idempotency_keys`, `schema_migrations`.
- Tablas creadas en MVP con lógica mínima o diferida: `territories`, `territory_control`, `safe_zones`, `treaties`, `garrisons`, `world_events`.
- **Fuera de MVP** (solo diseño, sin migración): `technologies`, `civilization_technologies`, `trade_routes`, `caravans`, `markets`, `trade_transactions`, `clans`.
- Convenciones obligatorias: claves primarias `bigint GENERATED ALWAYS AS IDENTITY` salvo `players.id` que es `uuid`; `timestamptz` para timestamps más `bigint` en epoch ms para instantes de simulación; `created_at` con `DEFAULT now()` y `updated_at` mantenido por trigger; enums de dominio como `text` + `CHECK` en vez de tipos `ENUM` de PostgreSQL; columna `version integer NOT NULL DEFAULT 0` donde aplique concurrencia optimista.

El esquema canónico —columnas exactas, tipos y constraints— se documenta en [../database/schema.md](../database/schema.md). Los fragmentos SQL de este ADR son **ilustrativos** y solo pretenden mostrar el mecanismo que motiva la decisión.

### Las constraints son diseño de dominio, no decoración

La razón de fondo para elegir una base de datos relacional madura es que permite **materializar invariantes en el almacén**, de forma que sean imposibles de violar aunque el código tenga un bug o dos procesos compitan.

El caso central es `INV-MOVE-001`, con un índice único parcial:

```sql
-- Ilustrativo. Materializa INV-MOVE-001: como máximo un movimiento ACTIVE por unidad.
CREATE UNIQUE INDEX unit_movements_one_active_per_unit
    ON unit_movements (unit_id)
    WHERE status = 'ACTIVE';
```

Ese índice hace tres cosas a la vez, y por eso vale más que cualquier comprobación en Go:

1. **Convierte una condición de carrera en un error de base de datos.** Si dos comandos `unit.move` para la misma unidad llegasen a intentar crear dos movimientos `ACTIVE`, uno de los dos falla con violación de unicidad en lugar de dejar el mundo en un estado indefinido.
2. **Es barato.** Al ser parcial, el índice solo contiene las filas `ACTIVE` —una fracción diminuta de un historial que acumula `COMPLETED`, `CANCELLED` y `FAILED`—, así que ocupa poco y acelera la consulta más frecuente: "¿qué movimientos hay activos?", que es exactamente la del arranque en frío.
3. **Documenta el invariante en el sitio donde importa.** Un desarrollador nuevo que lea el esquema descubre la regla sin leer el dominio.

El mismo razonamiento aplica a los enums de dominio como `text` + `CHECK`:

```sql
-- Ilustrativo.
status text NOT NULL
    CHECK (status IN ('ACTIVE','COMPLETED','CANCELLED','FAILED'))
```

Se prefiere a un tipo `ENUM` nativo porque **añadir un valor a un `CHECK` no requiere el bloqueo ni la coreografía de un `ALTER TYPE`**, y el mundo es 24/7: cada migración que necesite una ventana de bloqueo es una ventana de indisponibilidad.

### `jsonb` para la polilínea

La polilínea temporizada es un array de waypoints `{x, y, tMs}` de longitud variable. Se guarda como `jsonb` en la fila del movimiento en lugar de en una tabla hija:

```sql
-- Ilustrativo.
path jsonb NOT NULL   -- [{"x":10,"y":12,"tMs":0},{"x":11,"y":12,"tMs":600}, ...]
```

Justificación: la polilínea **se lee y se escribe siempre entera y siempre junto a su movimiento**. Nunca se consulta un waypoint suelto, nunca se actualiza uno solo y nunca se une contra otra tabla. Una tabla hija `movement_waypoints` multiplicaría por cien las filas escritas en cada `unit.move` sin ganar ninguna capacidad de consulta que el juego necesite. `jsonb` mantiene la escritura en **una sola fila y una sola transacción**, que es justo lo que hace atómica la secuencia "cancelar el movimiento anterior + crear el nuevo".

El coste asumido: la base de datos no valida la forma interna del JSON. Esa validación vive en Go, y los tests de recuperación la ejercitan. Se puede añadir un `CHECK` sobre `jsonb_typeof(path) = 'array'` como red mínima, pero la garantía real es del código.

### Transacciones: el ejemplo canónico

```sql
-- Ilustrativo. Una nueva orden de movimiento, atómica.
BEGIN;
  UPDATE unit_movements
     SET status = 'CANCELLED', updated_at = now()
   WHERE unit_id = $1 AND status = 'ACTIVE';

  INSERT INTO unit_movements (unit_id, status, path, start_time_ms, arrival_time_ms)
  VALUES ($1, 'ACTIVE', $2::jsonb, $3, $4);

  UPDATE units SET status = 'MOVING', version = version + 1 WHERE id = $1;
COMMIT;
```

Si el proceso muere en cualquier punto, el estado resultante es "antes" o "después", nunca "una unidad con dos movimientos activos" ni "una unidad `MOVING` sin movimiento". Esa propiedad no se puede emular de forma fiable en un almacén sin transacciones multi-documento; se puede *aproximar*, y cada aproximación es una fuente de incidencias en producción.

## Alternativas consideradas

### A. MySQL / MariaDB

**Ventajas reales.** InnoDB es transaccional, ACID y muy probado; el ecosistema de hosting gestionado es enorme y a menudo más barato; la replicación es sencilla de operar y el rendimiento en cargas OLTP simples es excelente. Para el volumen del MVP habría bastado de sobra, y encontrar personas con experiencia operándolo es trivial.

**Por qué se descarta.** Faltan exactamente las herramientas que hacen valiosa la elección:

- **No hay índices parciales.** El `WHERE status = 'ACTIVE'` no se puede expresar; habría que emular el invariante con una columna generada (por ejemplo, `active_unit_id` que valga `unit_id` cuando el estado es `ACTIVE` y `NULL` en otro caso) más un índice único sobre ella. Funciona, pero es un truco que hay que explicar en cada revisión.
- **El soporte JSON es más débil** que `jsonb`: sin el mismo abanico de operadores ni de indexación GIN.
- Los tipos temporales y el manejo de zonas horarias son menos limpios que `timestamptz`.

No es una mala base de datos; simplemente PostgreSQL hace mejor **este** trabajo concreto.

### B. SQLite

**Ventajas reales.** Cero operación: un fichero, sin servidor, sin daemon, sin credenciales. Arrancaría el proyecto hoy mismo —recuérdese que el daemon de Docker está apagado en la máquina de desarrollo—, tiene transacciones ACID reales, es asombrosamente rápido para lecturas y su fiabilidad está fuera de discusión. Para tests unitarios y para el desarrollo local es una tentación legítima.

**Por qué se descarta.** El modelo de escritura: un único escritor a la vez para toda la base de datos. Con workers de persistencia concurrentes escribiendo movimientos, posiciones consolidadas y eventos, la serialización global se convierte en el cuello de botella justo donde no debe estar. Tampoco hay acceso concurrente desde varios procesos ni un camino de crecimiento hacia réplicas u operación gestionada, y el juego es 24/7. Usarlo solo en tests y PostgreSQL en producción se descarta por una razón concreta: probaría un SQL distinto del que se ejecuta de verdad, y los invariantes de esquema —índices parciales incluidos— son precisamente lo que hay que probar. La integración se hace contra PostgreSQL real vía Docker Compose.

### C. MongoDB

**Ventajas reales.** El modelo de documento encaja de forma muy natural con la polilínea y con entidades de forma variable: el movimiento con sus waypoints es un documento único, sin decidir nada sobre tablas hijas ni `jsonb`. El escalado horizontal por sharding está resuelto de fábrica, el esquema flexible acelera la iteración temprana, y desde la versión 4 hay transacciones multi-documento reales.

**Por qué se descarta.**

- **Los invariantes no se pueden materializar con la misma fuerza.** El índice único parcial tiene equivalente (`partialFilterExpression`), pero las constraints de integridad referencial no existen: `units.city_id → cities.id` sería responsabilidad exclusiva del código.
- **El dominio es marcadamente relacional.** Jugadores, ciudades, unidades, territorios, tratados y guarniciones son entidades con relaciones densas y consultas cruzadas. El modelo de documento obliga a elegir entre desnormalizar (y mantener copias coherentes a mano) o hacer joins a mano en la aplicación.
- Las transacciones multi-documento existen pero son más caras y menos idiomáticas que en PostgreSQL, y aquí son el caso normal, no la excepción.
- El escalado horizontal, su mayor ventaja, resuelve un problema que el MVP no tiene.

### D. Event store puro (event sourcing)

**Ventajas reales.** Es conceptualmente el ajuste más elegante con el modelo del proyecto, que ya separa **Command** / **Event** / **State** y ya define eventos de dominio en pasado (`UnitMovementStarted`, `UnitMovementCompleted`, `UnitSpawned`, `CityProtectionEngaged`) que además son la base de los deltas de red y de `world_events`. Da auditoría perfecta, capacidad de reconstruir el mundo en cualquier instante pasado, depuración excepcional de incidencias y un encaje natural con la emisión de deltas.

**Por qué se descarta como *almacén primario*.**

- **Reconstruir por replay es incompatible con el arranque en frío deseado.** El servidor debe recuperar los movimientos `ACTIVE` y ponerse a simular en segundos; reproducir el log completo de un mundo persistente que lleva meses vivo no lo permite. Se resolvería con snapshots, es decir, reintroduciendo estado materializado — que es exactamente lo que la opción quería evitar.
- **Consultar el estado actual se vuelve indirecto.** Toda lectura pasa por proyecciones que hay que construir, mantener y reconstruir cuando cambian. Es un multiplicador de complejidad grande para un equipo pequeño y un dominio todavía inestable.
- **La evolución de esquema de eventos es una disciplina en sí misma**: un evento escrito hace seis meses debe seguir siendo interpretable para siempre.
- **La consistencia inmediata es más difícil.** El invariante "un solo movimiento `ACTIVE`" se comprueba de forma natural contra estado, no contra un log de hechos.

Se adopta la parte buena sin el coste: `world_events` guarda los eventos de dominio relevantes **junto** al estado materializado. Auditoría e historia sin depender del replay para operar. El event sourcing completo queda **fuera de MVP**.

### E. Solo Redis con persistencia (RDB/AOF)

**Ventajas reales.** Elimina una dependencia entera del sistema: un solo almacén, un solo cliente, una sola cosa que operar. Las latencias son de otro orden de magnitud, con AOF en `appendfsync everysec` la durabilidad es razonable para muchos usos, y las estructuras nativas (hashes, sorted sets) modelan bien presencia y cooldowns. El acoplamiento entre "estado caliente" y "estado durable" desaparecería.

**Por qué se descarta.** Es la alternativa más peligrosa porque parece suficiente hasta el día que no lo es:

- **Durabilidad configurable no es durabilidad transaccional.** RDB pierde por diseño la ventana entre snapshots; AOF con `everysec` puede perder hasta un segundo de escrituras ante una caída del proceso. Perder un segundo de estado en un mundo persistente con riesgo real significa perder movimientos confirmados, ownership o transiciones de protección ya notificadas al jugador.
- **No hay transacciones multi-clave con rollback.** `MULTI/EXEC` agrupa comandos pero no revierte si uno falla lógicamente; los scripts Lua son atómicos pero convierten la lógica de dominio en código incrustado en el almacén.
- **No hay constraints ni integridad referencial.** Todos los invariantes volverían al código, sin red de seguridad.
- **Las consultas ad hoc son inviables.** Diagnosticar un incidente ("¿qué unidades quedaron `MOVING` sin movimiento activo?") requeriría escanear el keyspace.

El canon lo zanja: **Redis nunca sustituye a PostgreSQL**. Su papel se define en [ADR-004](ADR-004-redis-hot-state.md).

### F. Comparativa

| Criterio | PostgreSQL | MySQL | SQLite | MongoDB | Event store | Solo Redis |
|---|---|---|---|---|---|---|
| Transacciones ACID multi-fila | Sí | Sí | Sí | Sí (con coste) | Parcial | No |
| Índices parciales | **Sí** | No (emulable) | Sí | Sí | n/a | No |
| Estructuras semiestructuradas | `jsonb` | JSON limitado | JSON | Nativo | Nativo | Nativo |
| Integridad referencial | Sí | Sí | Sí | No | No | No |
| Escritura concurrente multi-proceso | Sí | Sí | **No** | Sí | Sí | Sí |
| Arranque en frío rápido | Sí | Sí | Sí | Sí | **No** (sin snapshots) | Sí |
| Durabilidad garantizada | Sí | Sí | Sí | Sí | Sí | **Configurable** |
| Madurez operativa / gestionado | Excelente | Excelente | n/a | Buena | Escasa | Buena |

## Consecuencias

### Positivas

- **Los invariantes críticos son inviolables**, porque están en el esquema y no solo en el código. `INV-MOVE-001` sobrevive a un bug de dominio, a un despliegue a medias y a dos procesos compitiendo.
- **La recuperación tras crash es simple y verificable.** Cargar los movimientos `ACTIVE` al arrancar es una consulta indexada; los tests de nivel *recovery* comprueban que un movimiento sobrevive a un reinicio.
- **`jsonb` evita una tabla hija** y mantiene la escritura de un movimiento en una sola fila y una sola transacción, sin sacrificar la posibilidad de inspeccionar la polilínea con SQL cuando haga falta depurar.
- **Diagnóstico real.** Cualquier incidente se puede investigar con SQL sobre el estado exacto, e incluso cruzando con `world_events`.
- **Madurez operativa y opciones gestionadas.** Backups, PITR, réplicas de lectura y proveedores gestionados están disponibles cuando el proyecto los necesite, sin cambiar de tecnología.
- **Evolución sin bloqueos**: `text` + `CHECK` permite añadir estados de dominio (por ejemplo, nuevos `unit_type` o nuevos estados de tratado) con migraciones baratas en un servicio 24/7.
- La concurrencia optimista con `version integer` permite detectar escrituras conflictivas sin mantener locks abiertos durante la simulación.

### Negativas

- **El escalado de escritura es vertical al principio.** Un único primario acepta todas las escrituras; crecer significa máquina más grande, y solo después particionado o separación por dominios. No hay sharding automático. Para el MVP sobra, pero es un techo conocido y hay que vigilarlo con `eo_database_latency_seconds` y `eo_persistence_queue_depth`.
- **Exige disciplina permanente para no bloquear el tick.** Es el riesgo operativo número uno de esta decisión. Basta una consulta síncrona dentro del game loop para convertir un pico de latencia de la base de datos en overruns de tick para todos los jugadores. Reglas derivadas, de cumplimiento obligatorio:
  - El tick **solo encola** trabajo de persistencia (fase 8). Nunca espera un resultado de PostgreSQL.
  - Las escrituras las ejecutan workers con su propio pool de conexiones, fuera del goroutine del loop.
  - Toda consulta lleva `context.Context` con timeout; una base de datos lenta degrada la persistencia, nunca la simulación.
  - La profundidad de la cola es una métrica de primera clase (`eo_persistence_queue_depth`): si crece de forma sostenida, el problema es de escritura, y hay que verlo antes de que se convierta en pérdida de datos.
- **Presión de escritura en `unit_movements`.** Cada orden de movimiento escribe una fila con su polilínea; el historial crece de forma monótona y necesitará una política de retención o archivado. Esa política es **TBD (fuera de MVP)**.
- **Los `jsonb` grandes tienen coste.** Una polilínea de cientos de waypoints puede ir a almacenamiento TOAST y encarecer lecturas masivas. El límite práctico lo acota `EO_PATHFINDING_MAX_DISTANCE=256`, pero conviene no olvidarlo.
- **Una dependencia más en el arranque.** `GET /ready` exige PostgreSQL disponible; sin base de datos no hay servicio, y esto es correcto y deliberado: sin fuente de verdad no hay juego que servir.
- **Coste en el entorno de desarrollo.** Sin `psql` instalado, toda inspección pasa por `docker compose exec`, y los tests de integración requieren Docker Desktop iniciado (`EO_INTEGRATION=1`). Es fricción real en el día a día en Windows.

### Neutras

- El repositorio incorpora un directorio de migraciones (`services/game-server/migrations/`) con convención `NNNN_nombre.up.sql` / `.down.sql` y tabla de control `schema_migrations`; cada cambio de dominio pasa por ahí.
- El CI arranca servicios PostgreSQL y Redis para la etapa de integración, lo que alarga el pipeline y lo hace dependiente de contenedores.
- El pool de conexiones de `pgx` es un recurso que se dimensiona y se vigila: demasiado pequeño encola, demasiado grande satura el servidor de base de datos.
- Se mantiene la separación entre el instante `timestamptz` (para humanos y auditoría) y el `bigint` en epoch ms (para aritmética determinista de la simulación). Son dos representaciones del mismo hecho y hay que mantenerlas coherentes al escribir.

## Estado

**Aceptado** el 2026-09-09.

Se reevaluaría si el primario de escritura se convirtiese en cuello de botella medido (latencias sostenidas en `eo_database_latency_seconds` junto a crecimiento continuo de `eo_persistence_queue_depth`). La respuesta esperada **no** sería cambiar de motor, sino, en este orden: reducir la frecuencia de flush (`EO_PERSISTENCE_FLUSH_INTERVAL_TICKS`), archivar el historial de `unit_movements`, escalar verticalmente y, solo entonces, considerar particionado o separación por dominios en un ADR nuevo.
