# Estrategia de persistencia

Propósito: definir operativamente qué se escribe en PostgreSQL, cuándo, en qué lotes y con qué garantías de pérdida, incluyendo el diseño del dirty-set, la cola asíncrona, el comportamiento bajo degradación y el procedimiento de recuperación tras un crash.

---

## 1. Alcance

Este documento es la contraparte operativa de [../architecture/persistence.md](../architecture/persistence.md) (capas, contratos, repositorios, transacciones). Aquí se resuelven las cuatro preguntas obligatorias del canon §12 y se especifica el mecanismo concreto de escritura.

El modelo real, en una frase: **RAM primero, cola después**. Toda mutación se aplica en memoria dentro del tick; la escritura durable se **encola** como un `Job` y la ejecuta un worker fuera del tick, con hasta 3 intentos y compensación si se agotan. El tick nunca hace I/O de PostgreSQL — ni siquiera para las escrituras críticas. La ventana de riesgo que eso abre está declarada en §6 y no se disimula.

---

## 2. Las cuatro preguntas, por entidad

El canon exige que todo documento relevante responda: *¿qué es autoritativo en RAM? ¿qué se persiste inmediatamente? ¿qué se persiste eventualmente? ¿qué es reconstruible?* Respuesta formal, entidad por entidad, para el MVP.

Leyenda de la columna **Clase**:

- `TX` — **transacción síncrona** en el camino del comando, fuera del game loop. Sólo el alta de jugador.
- `DQ` — **escritura durable encolada**: se aplica en RAM y el `Job` se encola; el worker hace el `COMMIT` unos milisegundos después.
- `DF` — *dirty-set + flush* periódico cada `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` = 50 ticks (5 s a 10 Hz).
- `RC` — *reconstruible*: no es autoritativo en disco; se deriva al arrancar.
- `TR` — *transitorio*: vive en Redis con TTL; su pérdida es aceptable por diseño.

| Entidad / dato | Autoritativo en RAM | Escritura durable | Persistencia eventual (DF) | Reconstruible (RC) | Clase |
|---|---|---|---|---|---|
| Alta de `players` + `cities` + 3 `units` + `world_events` | sí, tras el alta | sí, **una sola transacción síncrona** antes de introducir al jugador en el mundo | — | — | TX |
| `cities.presence_state`, `protection_until` | sí (máquina de estados, fase 5 del tick, en RAM) | sí, `UPDATE` encolado por transición | — | — | DQ |
| `cities.population` | sí | sí, recalculado con `count(*)` en la transacción de alta | — | sí, desde `units` | TX + RC |
| `unit_movements` (crear / cancelar / completar) | sí (copia caliente) | sí, transacción encolada | — | — | DQ |
| `units.status` | sí | — | sí, en el lote de flush | sí, desde la existencia de un movimiento `ACTIVE` | DF |
| `units.x`, `units.y`, `chunk_x`, `chunk_y` | sí | — | sí, en el lote de flush | parcialmente | DF |
| `units.hp` | sí | — | **no**: la sentencia del flush escribe `x`, `y`, `status`, `chunk_x` y `chunk_y`, y `hp` no está entre ellas | — | — (sin combate en MVP, no cambia) |
| `world_state.epoch_ms` | `tickNumber` en RAM | sí, una vez, en la creación del mundo | — | `tickNumber` = `(now − epoch_ms) / tickDurationMs` | TX + RC |
| `world_state.current_tick` | sí | sí, **un solo `SaveTick` en el apagado ordenado** (fuera del tick, después de drenar la cola) | — | parcialmente: tras una caída abrupta el loop reanuda desde el último valor guardado | TX |
| `world_chunks` (terreno) | sí (grid regenerado desde la semilla) | sí, un `COPY` en la primera generación | — | sí, determinista desde `EO_WORLD_SEED` | TX + RC |
| Blocked overlay (murallas, ocupación) | sí | — | — | sí, desde `cities` al arrancar | RC |
| Presencia del jugador | **sí, la RAM decide**; Redis publica | — | — | — (`presence:player:{playerId}`, TTL 30 s) | TR |
| `idem:{playerId}:{requestId}` | no | — | — | — (TTL 300 s) | TR |
| `ticket:jti:{jti}` | no | — | — | — (TTL 120 s) | TR |
| Interest sets / suscripciones por chunk | sí | — | — | sí, desde `session.hello` y `session.view` | RC |
| `world_events` | no | sí, junto al hecho que lo origina | — | — | TX |
| `sessions`, `idempotency_keys` | RAM / Redis | **todavía no se escriben** (tablas creadas sin escritor) | — | — | — |
| `territories`, `territory_control`, `safe_zones`, `treaties`, `garrisons` | — | cuando su lógica se active | — | — | — |

**Consecuencia clave:** la posición de una unidad en movimiento **no se escribe por tick** —eso es lo que el diseño prohíbe—, pero sí converge en el lote de flush cada 50 ticks, porque avanzar de waypoint marca la unidad como sucia. No es una contradicción con la reconstruibilidad: mientras exista un `unit_movements` `ACTIVE`, la posición autoritativa es la derivada de la polilínea (`PositionAt`), y la recuperación la usa **en lugar** de `units.x/y`. La columna es un denormalizado de convergencia, no una segunda fuente de verdad, y por eso una posición de flush de hace 4,9 s nunca gana a la derivación.

Esta es también la lectura correcta de `INV-PERSIST-004` («el flush no contradice un movimiento activo»): lo que el invariante protege es que **nunca prevalezca** la posición volcada sobre la derivada, no que el `UPDATE` no llegue a ocurrir.

---

## 3. Dirty-set y flush periódico

### 3.1 Por qué jamás un `UPDATE` por unidad por tick

Aritmética del caso: 10 Hz × *N* unidades activas.

| Unidades activas | `UPDATE`s/s con escritura por tick | Sentencias/s con dirty-set + flush por lotes |
|---|---|---|
| 100 | 1 000 | ≤ 0,2 (1 sentencia cada 5 s) |
| 1 000 | 10 000 | ≤ 0,2 |
| 5 000 | 50 000 | ≤ 1 (troceado en varias sentencias) |

Razones concretas, no de estilo:

1. **Bloqueo del loop.** Cada `UPDATE` es un round-trip de red + fsync amortizado. A 100 ms de presupuesto por tick, cualquier escritura síncrona dispara `eo_game_tick_overruns_total`. El canon lo prohíbe explícitamente (§6).
2. **MVCC.** PostgreSQL no actualiza in-place: cada `UPDATE` crea una tupla nueva y una muerta. 50 000 UPDATE/s son 50 000 tuplas muertas/s que autovacuum no puede seguir; el resultado es bloat de tabla e índice y latencia creciente.
3. **WAL.** El volumen de WAL crece linealmente con las escrituras, saturando disco y replicación futura.
4. **Redundancia.** El 99 % de esas escrituras serían información derivable de `unit_movements`, ya persistido. Se estaría pagando I/O por un dato reconstruible.

Nótese que el flush **sí** vuelca la posición de las unidades en movimiento, una vez cada 50 ticks (§2). La regla que se respeta no es «nunca se escribe la posición de una unidad que se mueve», sino «nunca se escribe **por tick**»: 1 escritura por lote cada 5 s en lugar de 10 por segundo y por unidad, y siempre subordinada a la derivación desde la polilínea.

### 3.2 Estructura del dirty-set

Vive en RAM, dentro de `simulation.State`, en la goroutine del game loop (single-writer, sin mutex). Es un conjunto de identificadores, **no** de copias del estado: la copia de los valores se toma en el instante del flush, de modo que múltiples marcas entre flushes se colapsan naturalmente en una única escritura.

```go
// internal/game/simulation/state.go
unitDirty map[int64]struct{}   // ids de unidades con cambios pendientes

func (s *State) MarkDirty(id int64) { s.unitDirty[id] = struct{}{} }

// DrainDirty devuelve las unidades sucias ORDENADAS POR ID y vacía el conjunto.
// Sólo lo llama el game loop.
func (s *State) DrainDirty() []*unit.Unit
```

Puntos de marcado (todos dentro del tick, coste O(1)):

| Momento | Marca |
|---|---|
| Se acepta un `unit.move`: la unidad pasa a `MOVING` | `MarkDirty(unitID)` |
| Fase 3, `AdvanceMovements`: la unidad alcanza un waypoint nuevo | `MarkDirty(unitID)` |
| Movimiento completado: la unidad vuelve a `IDLE` | `MarkDirty(unitID)` |
| Movimiento cancelado: la unidad se detiene en el último tile alcanzado | `MarkDirty(unitID)` |
| Recuperación (`Hydrate`): unidad reubicada o con movimiento cerrado | `MarkDirty(unitID)` |

`DrainDirty()` se invoca en la fase 8 del tick cuando `tick % EO_PERSISTENCE_FLUSH_INTERVAL_TICKS == 0`. Los ids se **ordenan** antes de construir el lote: iterar un mapa de Go sin ordenar es no determinista, y aquí además el orden estable reduce el riesgo de deadlock entre lotes.

### 3.3 Agrupación del lote

```
tick % 50 == 0
     │
     ▼
DrainDirty() -> []*unit.Unit ordenado por id     # O(n), sin I/O
     │
     ▼
copia por valor de cada unidad                   # el loop sigue mutando los originales
     │
     ▼
Submit(Job{Name:"units.flush"}) --> cola de persistencia (NO bloqueante)
     │
     ▼  (worker, fuera del tick)
UPDATE units ... FROM unnest(...)
```

La **copia por valor** no es un detalle de estilo: el worker escribe desde otra goroutine mientras el loop sigue mutando los objetos del mundo, así que pasarle los punteros sería una carrera de datos.

El troceado del lote en varias sentencias no está implementado: hoy se envía uno solo, y la cota dura la impone PostgreSQL con sus **65 535 parámetros** por sentencia — irrelevante aquí, porque el flush pasa **seis arrays**, no seis parámetros por fila (§3.4). El umbral a partir del cual convendría trocear es **TBD (fuera de MVP)**.

### 3.4 La sentencia de flush

Un único `UPDATE ... FROM unnest(...)` por lote:

```sql
UPDATE units u
   SET x = v.x, y = v.y, status = v.status, chunk_x = v.chunk_x, chunk_y = v.chunk_y
  FROM (
      SELECT * FROM unnest($1::bigint[], $2::int[], $3::int[], $4::text[], $5::int[], $6::int[])
          AS t(id, x, y, status, chunk_x, chunk_y)
  ) AS v
 WHERE u.id = v.id;
```

Propiedades:

- **Una sola sentencia, un solo round-trip, un solo plan.** El planificador expande los arrays como una relación y une contra la clave primaria de `units`.
- **Seis arrays, no seis parámetros por fila.** Es la diferencia entre un `VALUES` que crece con el lote —y que choca con el límite de parámetros— y una sentencia de tamaño constante que acepta miles de filas. Los casts explícitos (`$1::bigint[]`, …) son obligatorios: sin ellos PostgreSQL infiere `unknown` y falla.
- **`chunk_x` y `chunk_y` viajan en el mismo `UPDATE`.** `UnitRepo` los recalcula como `x / chunkSize` e `y / chunkSize` al construir los arrays del lote, con el `chunkSize` que recibió al crearse. Ninguna sentencia escribe `x` sin escribir el chunk, que es lo que mantiene coherente la desnormalización del interest management.
- **Sin `version` ni concurrencia optimista**, porque no hace falta: el lote de flush es el **único** escritor de estas columnas. La transacción del movimiento no toca `units`, y por eso `units` no lleva columna `version`. Un `WHERE u.version = v.version` protegería contra un conflicto que ningún camino del código puede producir.
- El lote entero es un `Job` de la cola: si falla, se reintenta hasta **3 veces** con backoff creciente y, si se agotan, se descarta con un log a `level=error`. Descartar es seguro: los datos DF tienen RPO no nulo por definición (§6) y las unidades se volverán a marcar en cuanto vuelvan a cambiar.

### 3.5 Lo que **no** pasa por el dirty-set

Nunca: alta o baja de entidades, ownership, transiciones de presencia, movimientos, `world_events`. Todo eso es escritura durable propia — el alta como transacción síncrona, el resto como `Job` encolado con su propia transacción — y no espera al siguiente múltiplo de 50 ticks.

---

## 4. Cola de persistencia

### 4.1 Topología

```
   game loop (goroutine única)                4 workers de persistencia
   ───────────────────────────                ──────────────────────────────────────
   fases 2, 3, 5 y 8: Submit(Job)
        │  envío NO bloqueante
        ▼
   ┌───────────────────────────┐   consume    ┌──────────────────────────────┐
   │  chan simulation.Job       │ ───────────► │ job.Run(ctx)                 │
   │  (capacidad 4096)          │              │ 3 intentos, backoff creciente│
   └───────────────────────────┘              └──────────────────────────────┘
        │                                                    │ se agotan
        └── métrica: eo_persistence_queue_depth               ▼
            (gauge, len(chan))                    job.OnPermanentFailure(err)
```

Parámetros reales, fijados como constantes en `persistence.DefaultQueueConfig()` — **no** hay variables `EO_` para ellos, y añadirlas exigiría pasar antes por el canon §17:

| Parámetro | Valor |
|---|---|
| Capacidad del canal | 4096 |
| Workers | 4 |
| Intentos por trabajo | 3 |
| Espera entre intentos | 200 ms × número de intento (200 ms, 400 ms) |
| Timeout por intento | 10 s |

Dos decisiones del worker que conviene conocer:

- **El contexto de la escritura no hereda la cancelación del proceso.** Si el servidor se está apagando, una escritura pendiente termina en lugar de abortarse a medias. El apagado ordenado drena la cola antes de cerrar.
- **La cola transporta también las escrituras críticas.** `movement.start`, `movement.cancel`, `movement.complete` y las transiciones de presencia son `Job`s como el `units.flush`. No hay un camino síncrono paralelo: la única transacción síncrona del sistema es el alta de jugador, que ocurre en el manejador HTTP, no en el tick.

### 4.2 Backpressure: el loop nunca se bloquea

```go
// persistence.Queue.Submit
select {
case q.jobs <- job:
    q.metrics.PersistenceQueue.Set(float64(len(q.jobs)))
default:
    // Cola llena: se descarta y se grita. NO se bloquea el tick.
    q.log.Error("cola de persistencia saturada: trabajo descartado",
        "job", job.Name, "capacity", cap(q.jobs))
    if job.OnPermanentFailure != nil {
        job.OnPermanentFailure(context.DeadlineExceeded)
    }
}
```

Bloquear aquí sería bloquear el tick, así que no se bloquea nunca. El precio es explícito: **un trabajo descartado es una escritura perdida**, y por eso el descarte no es silencioso — se registra a `level=error` y se ejecuta la compensación del trabajo, si la tiene. Para un `units.flush` el descarte es inocuo (no lleva compensación: el estado sigue en RAM y se volverá a marcar). Para un `movement.start` sí hay `OnPermanentFailure`, pero conviene decir exactamente qué hace hoy: **registra el fallo a `level=error` y nada más**. Detener la unidad —que es lo que debería ocurrir para que el mundo no conserve un movimiento que la base nunca conoció— exige devolver un comando al loop por el canal de comandos, porque el worker corre en otra goroutine y no puede mutar el mundo sin provocar una carrera de datos. Cerrar ese hueco es trabajo **pendiente**, y mientras no se cierre esa ruta deja una inconsistencia posible entre RAM y PostgreSQL.

Que esto aparezca en los logs significa que la base de datos no sigue el ritmo del mundo. Nunca es normal.

### 4.3 Modos de degradación

`eo_persistence_queue_depth` es la señal de control. Umbrales concretos y conmutación automática de modo: **TBD (fuera de MVP)** — hoy el comportamiento es el de `NORMAL` más el descarte de §4.2, sin cambio de régimen. La tabla describe hacia dónde debe evolucionar:

| Modo | Disparador | Comportamiento previsto |
|---|---|---|
| `NORMAL` | profundidad baja y estable | Flush cada 50 ticks. Todo normal. **Es el único modo implementado.** |
| `DEGRADED` | profundidad sostenidamente alta | Alargar el intervalo efectivo de flush (saltarse ciclos), agrupando más cambios por lote: menos transacciones y menos WAL. Log `level=warn` y marca en `/ready`. |
| `SATURATED` | cola llena o escrituras fallando | Suspender el flush DF y reservar la capacidad restante para los trabajos críticos. Alerta. El mundo sigue simulando; el RPO de los datos DF crece hasta que la situación se resuelva. |

### 4.4 Si PostgreSQL está lento

Secuencia esperada y correcta:

1. Los workers tardan más ⇒ `eo_database_latency_seconds` sube (la observa el propio worker alrededor de cada intento).
2. La cola crece ⇒ `eo_persistence_queue_depth` sube.
3. El game loop **no se entera**: sigue ticando a 10 Hz; `eo_game_tick_duration_seconds` no se degrada y `eo_game_tick_overruns_total` no crece. Tampoco sufren latencia los comandos: al cliente se le confirma desde RAM.
4. Si la lentitud persiste hasta llenar el canal, empiezan los descartes de §4.2 con su log de error y sus compensaciones.

Regla de diagnóstico: **si `eo_game_tick_overruns_total` crece a la vez que `eo_persistence_queue_depth`, hay I/O dentro del tick — es un bug, no un problema de capacidad.**

---

## 5. Recuperación tras crash

Precondición: PostgreSQL contiene las escrituras durables cuyo `COMMIT` alcanzó a ejecutarse antes del crash —no necesariamente todas las confirmadas al cliente: ver la ventana de §6.1— y los datos DF hasta el último flush exitoso. Redis puede estar vacío (se asume el peor caso).

### 5.1 Procedimiento paso a paso

1. **Arranque y configuración.** Cargar `internal/config` (prefijo `EO_`). Fallar rápido si falta `EO_POSTGRES_URL`, `EO_REDIS_URL` o `EO_AUTH_JWT_SECRET`; los errores se reportan todos juntos, no de uno en uno.
2. **Migrar y conectar.** `postgres.Migrate` es lo primero que hace `run()`, antes incluso de abrir el pool: aplica las migraciones embebidas y aborta el arranque si `schema_migrations` está marcada como `dirty`. Sólo después se abren el pool de Postgres y el cliente Redis.
3. **`/health` responde OK; `/ready` responde *not ready*.** Los puertos HTTP y `/ws` no se abren hasta que la hidratación ha terminado: la secuencia de arranque es estrictamente secuencial y el `ListenAndServe` es posterior.
4. **Cargar `world_state`** con `WorldRepo.LoadOrInit`, que **falla el arranque** si `seed`, `width`, `height` o `chunk_size` de la fila no coinciden con la configuración actual. De ahí salen `epoch_ms` y `current_tick`; el loop arranca con `StartTick = current_tick`, de modo que el contador de ticks continúa donde lo dejó el último apagado ordenado. El tiempo del mundo, en cambio, no depende de ese contador: cada fase del tick recibe `Clock.NowMs()`.
5. **Regenerar el terreno desde la semilla.** `world.Generate(width, height, seed)` reconstruye el grid 512 × 512 en RAM en cada arranque. **`world_chunks` no se lee**: es la copia de auditoría, no la fuente primaria, y sólo se escribe (con un `COPY`) la primera vez que el mundo se crea. `WorldRepo.LoadTerrain` existe para comparar byte a byte la copia con lo regenerado (`INV-WORLD-005`) y para permitir en el futuro mapas editados a mano, pero no está en el camino de arranque.
6. **Cargar `cities`** (`CityRepo.ListAll`). Reconstruir ownership, `presence_state` y `protection_until`, y volver a bloquear **el blocked overlay**: por cada ciudad, el rectángulo 3 × 3 alrededor de su centro. La ocupación no se persiste como capa propia porque es reconstruible.
7. **Cargar `units`** con `status <> 'DEAD'` (`UnitRepo.ListAlive`). Posiciones y HP entran en RAM tal como estaban en el último flush; se corregirán en el paso 8 para las unidades con movimiento.
8. **Cargar `unit_movements` con `status = 'ACTIVE'`** y resolver cada uno contra `now = clock.NowMs()`:

   ```
   para cada movimiento ACTIVE m (simulation.Hydrate):
       si su unidad ya no existe:
           m.status       := FAILED          # movimiento huérfano, no hay nada que colocar
       si no, si movement.Validate(m.Path, world) falla:
           unit.status    := IDLE            # la unidad se queda DONDE ESTABA
           m.status       := FAILED
       si no, si m.arrival_time_ms <= now:
           # el movimiento terminó mientras el servidor estaba caído
           unit.x, unit.y := último waypoint de la polilínea   # snap al tile final
           unit.status    := IDLE
           m.status       := COMPLETED
       si no:
           # el movimiento sigue en curso: se reanuda, no se reinicia
           unit.x, unit.y := PositionAt(now)  # último waypoint con tMs <= now - start_time_ms
           unit.status    := MOVING
           reinsertar m en el conjunto de movimientos activos del loop
   ```

   Detalles que importan:

   - El caso `arrival_time_ms <= now` es el escenario explícito del canon §7 y es **la razón** por la que la polilínea se persiste temporizada: el resultado no depende de cuánto duró la caída, sólo de la aritmética. Un servidor caído 10 s y otro caído 3 h llegan al mismo estado final.
   - **El instante de mundo en que el movimiento terminó es `arrival_time_ms`, que ya está en la fila y no se toca.** El cierre escribe `status` y `finished_at = now()`, que sólo dice cuándo lo anotó el proceso. No existe ninguna columna `completed_at`: cualquier documento que la cite está describiendo algo que no está en el esquema.
   - Las ramas `COMPLETED` y `FAILED` marcan la unidad como sucia, así que su posición consolidada llega a la base por el flush ordinario. Los cierres los aplica `GameStore.FinishRecoveredMovements` antes de arrancar el loop, **con un `UPDATE` por movimiento**, no en un único lote: agruparlos es una mejora pendiente y barata, pero hoy no está hecha.
   - `MovementRepo.Finish` es idempotente a propósito (`WHERE id = $1 AND status = 'ACTIVE'`, y 0 filas afectadas no es error): el cierre puede llegar por el tick y por la recuperación.
   - Si un movimiento en curso tiene waypoints sobre tiles hoy bloqueados por el overlay reconstruido, `movement.Validate` lo rechaza y el movimiento se cierra como `FAILED` con la unidad en su última posición consolidada. Nunca se teletransporta a nadie por un dato dudoso.
   - Un movimiento con `start_time_ms > now` (reloj hacia atrás) no rompe nada: `PositionAt` devuelve el origen para todo instante anterior a `tMs = 0`, así que la unidad se reanuda en su punto de partida.

9. **Sesiones.** No hay nada que cerrar: `sessions` **todavía no tiene escritor** (§13 de [schema.md](./schema.md)), así que tampoco hay filas huérfanas. La sesión viva vivía sólo en RAM y en Redis. No se intenta "reanudar" sesiones: el cliente reconecta con un ticket nuevo (TTL 60 s) y recibe `world.snapshot`.
10. **Presencia.** No se reconstruye: las claves `presence:player:{playerId}` (TTL 30 s) habrán expirado o expirarán solas. La máquina de presencia se reactiva con el loop y llevará a `OFFLINE_PENDING` a las ciudades cuyos jugadores no reconecten dentro de la gracia — comportamiento correcto: efectivamente no están conectados.
11. **Interest sets vacíos.** Se recalculan a medida que los clientes hagan `session.hello` y `session.view`.
12. **Arrancar el game loop** junto con los servidores HTTP. `/ready` pasa a OK cuando responden PostgreSQL y Redis **y** el latido del game loop es reciente (umbral 5 s), no antes: hasta ese momento un orquestador no debe enviar tráfico. Los puertos no se abren antes porque la hidratación de los pasos 4–8 es previa y secuencial; aceptar conexiones a mitad produciría un `world.snapshot` de un mundo a medio cargar.

### 5.2 Diagrama

```
  start ──► config ──► migrate (¿dirty? ──► exit(1)) ──► pool PG + Redis
                                                              │
                                                              ▼
   world_state ──► terreno regenerado desde seed ──► cities (+overlay) ──► units ──► unit_movements ACTIVE
                                                                        │
                        ┌───────────────────────────────────────────────┼───────────────────────┐
                        ▼                                               ▼                       ▼
           polilínea inválida / sin unidad              arrival_time_ms <= now      arrival_time_ms > now
           FAILED, la unidad no se mueve                snap final + COMPLETED      reanudar desde polilínea
                        └───────────────────────────────┬───────────────────────────────────────┘
                                                        ▼
                       FinishRecoveredMovements ──► arrancar loop + HTTP ──► /ready OK cuando late el loop
```

### 5.3 Cobertura de tests

El nivel de test `recovery` del canon §19 cubre exactamente esto: rehidratar con `simulation.Hydrate` un mundo con un movimiento `ACTIVE` y assertar el estado resultante. Los **tres tests de recuperación** que existen hoy en `internal/game/simulation` están en verde y cubren: (a) crash con `arrival_time_ms` futuro ⇒ el movimiento se reanuda desde la polilínea y llega al mismo tile en el mismo instante de mundo; (b) crash con `arrival_time_ms` pasado ⇒ snap al tile final y cierre como `COMPLETED`; (c) polilínea inválida ⇒ `FAILED` y la unidad no se mueve. El caso "crash entre flushes con el proceso real reiniciado" pertenece a la suite de integración, que está escrita pero **todavía no se ha ejecutado** porque el daemon de Docker no arrancó en la máquina de desarrollo. Ver [../testing/strategy.md](../testing/strategy.md).

---

## 6. Ventana de pérdida aceptable (RPO)

RPO = cantidad máxima de trabajo confirmado que puede perderse ante un crash del proceso.

| Clase de dato | RPO objetivo | Justificación |
|---|---|---|
| Alta de jugador + ciudad + 3 aldeanos | **0** | Es la identidad del jugador. Perderla es perder la cuenta. Única transacción síncrona del sistema, y el jugador no entra en el mundo hasta que ha confirmado. |
| Ownership de ciudad (`cities.owner_player_id`) | **0** | Se escribe en esa misma transacción de alta y después no cambia en el MVP. Dato con el mayor coste de inconsistencia percibida: "me robaron la ciudad" es irreparable socialmente. |
| Transiciones de presencia y protección | **≈ ventana de cola** (unas decenas de ms) | El objetivo de diseño es 0 —determinan si una ciudad es atacable—, pero el `UPDATE` va encolado: si el proceso muere antes del `COMMIT`, la transición se pierde y al rearrancar la ciudad vuelve a evaluarse desde su `last_offline_at`, que es la reconciliación que contiene el daño. |
| Inicio / cancelación / finalización de movimiento | **≈ ventana de cola** (unas decenas de ms) | Es la ventana honesta del modelo, no un descuido: ver el recuadro que sigue a esta tabla. |
| `idempotency_keys` de comandos durables | **n/a en MVP** | La tabla existe pero **no tiene escritor**: la deduplicación vigente es `idem:{playerId}:{requestId}` en Redis, con el RPO de la fila correspondiente más abajo. |
| Posición de unidad en movimiento | **0 efectivo** | No se persiste por tick, pero es reconstruible de la polilínea ya persistida. La derivación es exacta, no aproximada. |
| Posición consolidada de unidad en reposo (`units.x/y`) | **≤ 5 s** (50 ticks) + latencia de cola | Es un denormalizado de convergencia. En el peor caso se recupera del último `unit_movements`, por lo que la pérdida real observable tiende a cero. |
| `units.hp` | **≤ 5 s** + latencia de cola | Sin combate en MVP el HP prácticamente no cambia; 5 s es holgadamente suficiente. Se revisará al entrar combate. |
| `units.status` sin movimiento asociado | **≤ 5 s** | Mismo razonamiento; además es derivable de la existencia de un movimiento `ACTIVE`. |
| Presencia (`presence:player:{playerId}`) | **≤ 30 s** (TTL) | Estado transitorio por diseño. Su pérdida sólo adelanta una transición a `OFFLINE_PENDING` que el heartbeat corrige o confirma. |
| Idempotencia rápida (`idem:...`) | **≤ 300 s** (TTL) | Pérdida aceptable, y hoy sin respaldo durable: si Redis no responde, `Claim` **deja ejecutar el comando igualmente** — se prefiere dejar jugar a bloquear al jugador, y así está decidido. |
| `ticket:jti:{jti}` | **≤ 120 s** (TTL) | Su pérdida sólo puede permitir el replay de un ticket cuyo `exp` (60 s) probablemente ya venció; el `exp` sigue verificándose siempre. |
| `world_chunks` | **0**, y además regenerable | Inmutable tras la generación y determinista desde `EO_WORLD_SEED`. |
| Interest sets, `seq`, blocked overlay | **n/a** | Reconstruibles; no tienen semántica durable. |

### 6.1 La ventana de riesgo del movimiento, dicha sin adornos

El servidor acepta un `unit.move`, lo aplica en RAM, responde `unit.move.accepted` y difunde `unit.movement.started` **antes** de que exista fila alguna en `unit_movements`: la transacción se encola y un worker la confirma unos milisegundos después. Entre la confirmación al cliente y el `COMMIT` hay una ventana —típicamente unas pocas decenas de milisegundos, más si la cola está cargada— en la que un `SIGKILL`, un OOM o un corte de corriente hacen que ese movimiento **se pierda**: al rearrancar, `unit_movements` no lo contiene, la unidad queda en su última posición consolidada y el cliente ve, tras reconectar, una orden que el servidor confirmó y luego olvidó.

Es un RPO **documentado**, no un descuido, y es el precio explícito de la regla "el tick nunca hace I/O de PostgreSQL": la alternativa —confirmar sólo después del `COMMIT`— pondría la latencia de disco dentro del camino del comando y, si se hiciera dentro del tick, dentro del presupuesto de 100 ms. Lo que sí es exigible es no mentir sobre ella:

- La ventana no crece indefinidamente en régimen normal; crece exactamente con `eo_persistence_queue_depth`, que por eso es la señal de control de §4.
- Un trabajo descartado por cola llena (§4.2) amplía la misma ventana a una pérdida segura, y por eso se registra a `level=error`.
- Ningún otro dato del sistema tiene una ventana peor, porque el alta de jugador —lo único cuya pérdida sería irrecuperable— sí es síncrona y ocurre fuera del loop.

Resumen en una frase: **sólo el alta de jugador tiene RPO 0 estricto; el resto de escrituras durables tienen el RPO de la cola, y los datos derivables o transitorios el de su flush o su TTL.**

En modo `SATURATED` (§4.3) el RPO de los datos DF crece sin límite hasta que la cola se recupera, y el de las escrituras encoladas crece con ella.

---

## 7. Métricas y señales

| Métrica (canon §18) | Uso en persistencia |
|---|---|
| `eo_persistence_queue_depth` | Señal principal de backpressure y disparador de `DEGRADED`/`SATURATED`. |
| `eo_database_latency_seconds` | Latencia de las transacciones write-through y de los lotes de flush. |
| `eo_redis_latency_seconds` | Presencia, idempotencia rápida y consumo de `jti`. |
| `eo_game_tick_duration_seconds` / `eo_game_tick_overruns_total` | Deben permanecer planas ante lentitud de Postgres. Si no, hay I/O en el tick. |
| `eo_active_units` | Denominador para dimensionar lotes y cola. |

Logs estructurados (`log/slog`, JSON) con los campos estándar `ts`, `level`, `msg`, `player_id`, `session_id`, `request_id`, `tick`. Cada intento fallido de un trabajo durable se registra a `level=warn` con `job`, `attempt` y `max_attempts`; el agotamiento de los tres intentos y el descarte por cola llena, a `level=error` con el nombre del trabajo. No hay ningún registro de "discrepancia de `version`" en el camino del flush, sencillamente porque el flush no compara versiones (§3.4).

---

## 8. Documentos relacionados

- [../architecture/persistence.md](../architecture/persistence.md) — capas, contratos, repositorios, transacciones, claves de Redis.
- [../database/schema.md](../database/schema.md) — DDL, columnas, índices y migraciones.
- [../architecture/game-loop.md](../architecture/game-loop.md) — fases del tick; fase 8 `enqueue persistence`.
- [../specs/movement.md](../specs/movement.md) — polilínea temporizada y reconstrucción analítica de la posición.
- [../invariants/](../invariants/) — registro de invariantes (`INV-PERSIST-*`, `INV-MOVE-*`).
- [../testing/strategy.md](../testing/strategy.md) — niveles `integration`, `simulation` y `recovery`.
- [../operations/monitoring.md](../operations/monitoring.md) — métricas Prometheus y alertas de cola saturada.
- [../operations/disaster-recovery.md](../operations/disaster-recovery.md) — arranque tras crash y esquema `dirty`.
