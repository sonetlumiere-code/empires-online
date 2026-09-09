# Game Loop autoritativo

Especificación del bucle de simulación del Game Server: las 8 fases del tick en orden fijo, el presupuesto de tiempo, la política de overrun y catch-up, las reglas de determinismo y la integración con `FakeClock` para *simulation tests*.

> Estado: **implementado** en `services/game-server/internal/game/loop/loop.go` (`Loop.Run` + `Loop.Step`)
> y `internal/game/simulation/`. Este documento describe el código que existe, no una intención.
> Documentos relacionados: [game-server.md](./game-server.md) (paquetes, concurrencia, ciclo de vida) ·
> [specs funcionales](../specs/README.md) · [esquema de base de datos](../database/schema.md) ·
> [invariantes](../invariants/README.md) · [estrategia de tests](../testing/strategy.md).

---

## 1. Contrato del loop

El loop es la **única** goroutine autorizada a mutar el estado del mundo (ver
[game-server.md §4](./game-server.md#4-propiedad-de-memoria)). Su contrato:

1. Ejecuta exactamente las **8 fases del canon, siempre en el mismo orden**, sin saltarse ninguna.
2. **Nunca** ejecuta I/O bloqueante contra PostgreSQL. Sólo encola trabajos de persistencia en
   `simulation.Persister` (implementado por `persistence.Queue`), cuyo `Submit` nunca bloquea.
3. **Nunca** bloquea leyendo o escribiendo canales: drena y encola con `select`/`default`.
4. No llama a `time.Now()` ni a `rand` dentro de la simulación: usa `Clock` y `RandomSource` inyectados.
   La única excepción es la medición de duración del propio tick (`time.Since` en `Step` y `observe`),
   que es observabilidad, no simulación.
5. Dado el mismo instante de tick, el mismo estado inicial y la misma secuencia de comandos drenados,
   produce **exactamente** el mismo estado final y la misma secuencia de eventos.
6. Preserva la invariante de movimiento único activo por unidad (`INV-MOVE-001`): una nueva orden
   cancela la anterior **en RAM antes** de registrar la nueva, y la transacción encolada
   (`UPDATE ... 'CANCELLED'` + `INSERT ... 'ACTIVE'`) las agrupa en un solo commit.
7. **Aísla los pánicos por comando**: `applyCommand` envuelve cada comando en un `recover`. Un bug en
   una regla se registra, se cuenta en `eo_commands_total{result="failed"}` y el tick continúa; no
   tumba la simulación del mundo entero.

---

## 2. Tiempo: tickNumber, epoch_ms y tickTime

| Concepto | Definición | Origen |
|---|---|---|
| `EO_TICK_RATE_HZ` | 10 Hz por defecto; debe **dividir exactamente a 1000** (lo valida el arranque) | Configuración |
| `tickDurationMs` | `1000 / EO_TICK_RATE_HZ` = **100 ms** | Derivado (`Config.TickDuration`) |
| `epoch_ms` | Instante (epoch milliseconds) en que nació el mundo | `world_state.epoch_ms` → `loop.Config.EpochMs` |
| `tickNumber` | `uint64` monótono; se **reanuda** desde `world_state.current_tick` (`loop.Config.StartTick`) y avanza en exactamente 1 por tick ejecutado | `Loop.tick` |
| `tickTime` | El instante `nowMs` con el que se ejecuta el tick: `Run` lo lee **una sola vez** del `Clock` inyectado (`l.clk.NowMs()`) y se lo pasa a `Step(nowMs)` | `Clock` |

`tickTime` es **la noción de "ahora" que recorre el tick**. Todos los instantes durables
(`start_time_ms`, `arrival_time_ms`, `protection_until`, `tMs` de los waypoints) se comparan contra
el reloj **inyectado**, jamás contra `time.Now()` dentro de la simulación. Las fases 3 y 5 reciben
`nowMs` como parámetro (`AdvanceMovements(nowMs)`, `ProcessTimers(time.UnixMilli(nowMs).UTC())`), de
modo que todas las entidades que procesan ven exactamente el mismo instante.

Precisión sobre la fase 2, para no idealizar el código: los manejadores de comandos
(`handleMoveUnit`, `handleCancelMovement`, `handleRequestSnapshot`) **releen el instante del mismo
`Clock` inyectado** con `s.deps.Clock.NowMs()` al entrar en cada comando, en lugar de recibir el
`nowMs` del tick. No rompe el determinismo de los *simulation tests* —el `FakeClock` no avanza
mientras corre `Step`, así que ambos valores coinciden al milisegundo—, pero en producción pueden
diferir en los pocos milisegundos que dure el tick. Unificar ambos instantes pasando `nowMs` a
`Simulation.Apply` es una mejora **pendiente**, no una propiedad que hoy se pueda dar por cierta.

En los tests, `Step` se invoca directamente con un instante arbitrario y un `FakeClock`, lo que hace
el avance del mundo exactamente reproducible.

Precisión importante: **`tickTime` NO se calcula como `epoch_ms + tickNumber * tickDurationMs`**. El
contador de ticks y el instante de simulación son dos cosas distintas y a propósito:

- El instante viene del reloj, de modo que el mundo persistente evoluciona en **tiempo real** aunque
  el proceso se salte ticks o haya estado caído. Derivarlo del contador haría que cada tick perdido
  atrasara permanentemente el reloj del mundo.
- El contador `tickNumber` es una etiqueta monótona para logs, métricas, snapshots (`world.snapshot.tick`)
  y continuidad tras un reinicio; no es un ancla temporal.
- Tras una caída, el loop **no reproduce** los ticks perdidos: reanuda el contador desde
  `world_state.current_tick` y el estado se reconstruye analíticamente desde `unit_movements`
  (la posición es el último waypoint con `tMs <= T - start_time_ms`). Recuperar mil ticks de bucle
  sería inútil y peligroso; recalcular una polilínea es exacto y O(log n).

---

## 3. Las 8 fases del tick

Orden fijo, sin excepciones. Ninguna fase puede adelantar trabajo de otra.

```
┌───────────────────────────── TICK N (100 ms) ─────────────────────────────┐
│ 1 drain commands      cola → buffer local, no bloqueante                  │
│ 2 validate & apply    ownership, estado, destino, A*, mutación            │
│ 3 advance movement    polilínea temporizada → posiciones autoritativas    │
│ 4 resolve simulation  RESERVADO (combate) — no-op en MVP                  │
│ 5 timers / scheduled  presencia, protección de ciudad, territorio         │
│ 6 world state / interest sets   chunk membership (en el punto del hecho)  │
│ 7 emit deltas         hechos → mensajes por chunk → hub                   │
│ 8 enqueue persistence lote del dirty-set cada 50 ticks (jamás I/O aquí)   │
└───────────────────────────────────────────────────────────────────────────┘
```

| # | Fase | Entrada | Salida | Puede | No puede |
|---|---|---|---|---|---|
| 1 | `drain commands` | Canal `commands` (capacidad 8192) | Comandos del tick | Consumir con `select`/`default` hasta un tope de `MaxCommandsPerTick` (**1024**); lo que no entra espera al tick siguiente y se registra un `warn` | Bloquear; superar el tope; validar o mutar |
| 2 | `validate & apply commands` | Comandos del paso 1 | Mutaciones + mensajes + rechazos | Validar ownership/estado/destino, ejecutar A*, crear/cancelar movimientos, responder `unit.move.rejected` con los códigos del catálogo, encolar el trabajo durable del movimiento | Tocar Postgres o Redis de forma bloqueante; usar cualquier reloj que no sea el `Clock` inyectado |
| 3 | `advance movement` | Movimientos `ACTIVE`, `tickTime` | Posiciones nuevas, `unit.movement.completed` | Recorrer movimientos, avanzar por la polilínea, completar los vencidos, actualizar el índice espacial, marcar la unidad como *dirty* | Recalcular paths; crear movimientos nuevos; cancelar por decisión propia |
| 4 | `resolve simulation` | — | — | **Fuera de MVP**: reservado a combate. Hoy no existe ninguna llamada en `Step` | Cualquier cosa, mientras esté fuera de MVP |
| 5 | `process timers / scheduled events` | `tickTime`, contadores de sesión y marcas de desconexión en RAM | Transiciones de presencia + `city.update` | Evaluar `ONLINE → OFFLINE_PENDING` (margen `DisconnectGrace` = `EO_PRESENCE_TTL_SECONDS`) y `OFFLINE_PENDING → PROTECTED` (`EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS`) | Consultar Redis dentro del tick: la decisión es **enteramente aritmética sobre RAM** |
| 6 | `update world state / interest sets` | Posiciones nuevas; el conjunto suscrito por sesión | Membresía de chunk actualizada | Recalcular el chunk de cada entidad (`chunkX = x / EO_CHUNK_SIZE`) y mantener el índice `chunk → entidades` | Enviar mensajes; mutar entidades |
| 7 | `emit deltas` | Hechos de las fases 2, 3 y 5 | Broadcast por chunk / por jugador hacia el `Hub` | Construir los payloads v1 y entregarlos al `Hub`, que los deposita en la cola de salida de cada sesión | Escribir en sockets (lo hace el `writePump` de cada conexión); asignar `seq`/`ts` (los estampa `Session.Send`); bloquear si la cola está llena |
| 8 | `enqueue persistence` | *Dirty-set* de unidades | Un trabajo de volcado por lote | Cada `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` (**50**) drenar el dirty-set y encolar **un único** `units.flush` | Ejecutar SQL; esperar confirmación; perder un trabajo durable en silencio |

**Precisión sobre las fases 6, 7 y 8, porque es donde el código se aparta de la lectura ingenua del
diagrama.** No existen tres pasadas separadas al final del tick:

- Las fases 6 y 7 **no son bloques de código propios**: la membresía de chunk se actualiza y el delta
  correspondiente se emite en el mismo punto en que ocurre el hecho, dentro de la fase que lo produce
  (`state.MoveUnitTo` reindexa, `Broadcaster.BroadcastChunk` emite). El orden que ve el cliente es,
  por tanto, el orden de las fases: respuestas a comandos (fase 2) → deltas de movimiento (fase 3) →
  transiciones de presencia (fase 5). El recálculo del **conjunto suscrito** de una sesión no ocurre
  en el tick: lo hace `Hub.Subscribe` cuando llega un `session.view`, y devuelve los chunks que
  entran y los que salen.
- La fase 8 encola **sólo el volcado por lotes** del dirty-set. Las escrituras durables de un hecho
  concreto (inicio, cancelación y finalización de movimiento; transición de presencia) se encolan en
  la fase que produce el hecho — 2, 3 y 5 respectivamente —, no al final del tick. Lo que la fase 8
  garantiza es lo importante: **el tick encola, nunca escribe**.

### 3.1 Detalle de la fase 2

Orden de validación para `unit.move` (cortocircuito en el primer fallo). La propiedad se comprueba
antes que cualquier otra cosa: así un jugador no puede deducir el estado de unidades ajenas a partir
de qué error recibe.

| Comprobación | Error si falla |
|---|---|
| La unidad existe | `UNIT_NOT_FOUND` |
| El jugador de la sesión es su dueño | `UNIT_NOT_OWNED` |
| `status` distinto de `DEAD` | `UNIT_DEAD` |
| `status` distinto de `GARRISONED` | `UNIT_GARRISONED` |
| El resto de estados que no admiten movimiento | `UNIT_NOT_MOVABLE` |
| Destino dentro de `[0, EO_WORLD_WIDTH) × [0, EO_WORLD_HEIGHT)` | `TARGET_OUT_OF_BOUNDS` |
| Tile destino `walkable` y no bloqueado por el *overlay* | `TARGET_NOT_WALKABLE` |
| El tipo de unidad está en el catálogo (`unit.Lookup`) | `INTERNAL_ERROR` |
| Distancia Chebyshev ≤ `EO_PATHFINDING_MAX_DISTANCE` (256), comprobada dentro del A\* **antes** de expandir nodos | `PATH_TOO_LONG` |
| El A\* no agota el presupuesto `EO_PATHFINDING_MAX_NODES` (20000) | `PATH_TOO_LONG` |
| El A\* alcanza el destino (el conjunto abierto no se vacía antes) | `PATH_NOT_FOUND` |
| La polilínea se construye sobre el mundo actual (`BuildTimedPath`) | `INTERNAL_ERROR` |

Detalle que conviene no confundir: **agotar el presupuesto de nodos NO produce `PATH_NOT_FOUND`**.
`astar.go` devuelve `ErrPathTooLong` tanto al superar la distancia Chebyshev como al superar
`MaxNodes`, y `pathErrorCode` lo traduce a `PATH_TOO_LONG`. `PATH_NOT_FOUND` queda reservado al caso
en que la búsqueda termina sin candidatos: el destino es inalcanzable de verdad.

Dos casos que la tabla no captura y conviene declarar:

- **Origen igual al destino.** Se comprueba justo **después** de `TARGET_NOT_WALKABLE` y **antes** de
  `unit.Lookup` y del A\*. Se acepta sin crear movimiento: se cancela el movimiento en curso —con
  razón `CANCELLED_BY_PLAYER`, no `REPLACED`—, se
  responde `unit.move.accepted` con `movementId = 0` y **no** se emite `unit.movement.started`. Evita
  polilíneas degeneradas de un solo punto y hace el comando idempotente en su efecto observable.
- **`INVALID_TARGET`** existe en el catálogo de códigos pero el flujo de `unit.move` no lo emite: un
  payload sin destino decodificable se rechaza antes, en la capa `websocket`, con `INVALID_MESSAGE`.

El MVP **rechaza** destinos intransitables en lugar de buscar un tile cercano. El origen del camino es
la posición **autoritativa en este instante** —derivada de la polilínea si la unidad ya se movía—, no
la posición consolidada en `units`. Si todas las comprobaciones pasan: se cancela en RAM el movimiento
`ACTIVE` previo de esa unidad, se construye la polilínea temporizada con aritmética entera exacta

```go
ms := (baseMsPerTile*costUnits + 5) / 10               // world.CostBase = 10
if diagonal { ms = (ms*1414214 + 500000) / 1000000 }   // √2 en punto fijo
if ms < 1 { ms = 1 }
```

—se redondea **cada segmento** al milisegundo más cercano y sólo después se acumula, porque mil pasos
truncados regalarían casi un segundo de ventaja—, se pasa `status` a `MOVING`, se encola el trabajo
durable `movement.start` y se emiten `unit.move.accepted` (al solicitante) y `unit.movement.started`
(al chunk de origen).

**El rechazo produce únicamente `unit.move.rejected { unitId, code, message }`**, sin un `system.error`
adicional: los fallos de dominio de este comando son su respuesta tipada, y `system.error` queda
reservado a los fallos de transporte, versión, sesión y saturación.

El A* corre **dentro del tick**: es CPU pura y acotada, no I/O. Su coste se mide con
`eo_pathfinding_duration_seconds` y `eo_pathfinding_requests_total`. Si el presupuesto del tick se
demostrase insuficiente bajo carga, la mitigación (delegar el A* a un pool de workers y aplicar el
resultado en un tick posterior) es **TBD (fuera de MVP)**.

### 3.2 Dos órdenes de la misma unidad en el mismo tick

Con un rate limit de 20 msg/s (burst 40) caben holgadamente dos `unit.move` de la misma unidad dentro
de un tick de 100 ms. El caso está resuelto en RAM y acotado —no ignorado— en disco:

- **En RAM no hay ambigüedad.** La fase 1 drena los dos comandos y la fase 2 los aplica en secuencia
  dentro de la misma goroutine. El segundo cancela el primero (`cancelActiveMovement` con razón
  `REPLACED`) antes de registrarse. En ningún instante existen dos movimientos activos de la unidad.
- **En disco el orden no está garantizado.** `persistence.Queue` corre con **4 workers** y no
  particiona por `unit_id`, así que las dos transacciones encoladas pueden ejecutarse concurrentemente.
  La red de seguridad es el índice único parcial `unit_movements_one_active_per_unit`: la segunda
  inserción falla con `23505`, el trabajo se reintenta hasta 3 veces con backoff (`RetryDelay` de
  200 ms multiplicado por el número de intento) y, si se agota, se registra como error y se invoca
  `OnPermanentFailure` si el trabajo declaró uno. Hoy sólo `movement.start` lo declara, y **su
  callback se limita a registrar**: detener realmente la unidad en RAM es trabajo pendiente.
- **Mitigación pendiente**, para que el orden durable coincida siempre con el orden de RAM: un único
  worker, o particionado de la cola por `unit_id`. Hoy no está implementado y así debe leerse este
  documento.

---

## 4. Presupuesto de tiempo por tick

Período = 100 ms. Objetivo de diseño: **p99 del tick ≤ 50 % del período**, dejando la mitad como
margen para GC, picos de conexión y crecimiento del mundo. Reparto orientativo, verificable con el
histograma `eo_game_tick_duration_seconds`:

| Fase | Presupuesto orientativo | Comentario |
|---|---|---|
| 1 drain | < 1 ms | Copia de punteros desde el canal |
| 2 validate & apply | ~20 ms | Dominado por A*; es la fase con varianza real |
| 3 advance movement | ~10 ms | Lineal en movimientos `ACTIVE`, búsqueda binaria por polilínea |
| 4 simulation | 0 ms | No-op en MVP |
| 5 timers | ~5 ms | Lineal en ciudades con timer pendiente |
| 6 interest sets | ~5 ms | Lineal en sesiones conectadas |
| 7 emit deltas | ~8 ms | Serialización JSON agrupada por chunk |
| 8 enqueue persistence | ~1 ms | Encolado no bloqueante |
| **Total objetivo** | **≤ 50 ms** | El resto del período es margen deliberado |

Estos números son objetivos de diseño para dimensionar, no umbrales del canon. La única condición
normativa es: **si la duración del tick supera el período, se incrementa `eo_game_tick_overruns_total`**.

---

## 5. Overrun y catch-up

### Política

1. El loop calcula el instante de despertar del siguiente tick con **tiempo absoluto**: fija un
   `start = Clock.Now()` al arrancar y programa el vencimiento número *n* en `start + n*periodo`,
   nunca con `sleep(periodo - duracion)` acumulativo. Así el error de temporización no se acumula.
2. Si un tick se pasa de presupuesto: se incrementa `eo_game_tick_overruns_total`, se registra
   `level=warn` con `tick` y duración, y **se continúa**. No se aborta el tick a medias: un tick parcial
   dejaría el mundo en estado inconsistente.
3. Si al terminar el tick ya venció el instante de uno o varios ticks siguientes, esos **vencimientos
   se descartan** y el calendario se reancla al presente, con un `warn` que registra cuántos se
   perdieron. No hay *spiral of death*: el loop nunca intenta "recuperar" trabajo atrasado ejecutando
   ticks a ritmo acelerado. Nótese la distinción: lo que se salta son **vencimientos programados**; el
   contador `tickNumber` sigue avanzando en exactamente 1 por tick **ejecutado**, y el instante de
   simulación lo aporta el reloj, no el contador.
4. Saltar vencimientos es seguro por diseño: el movimiento es una polilínea temporizada evaluada
   contra el instante del reloj, así que una unidad que debía recorrer 4 tiles durante ese hueco
   simplemente aparece 4 tiles más adelante en el primer tick ejecutado. Ningún estado depende de
   haber ejecutado un tick concreto.
5. El número de ticks saltados se registra en el log del tick. Una métrica dedicada para ticks saltados
   es **TBD (fuera de MVP)**: el canon sólo define `eo_game_tick_overruns_total`.

```
tiempo real ──────────────────────────────────────────────────────────►
ticks planificados   |100|100|100|100|100|100|  (ms)
tick 41 dura 260 ms  |█████████████████████|
                     ^ overrun: +1 a eo_game_tick_overruns_total, warn con tick=41
vencimientos 42 y 43 ── DESCARTADOS, warn "ticks perdidos descartados" missed=2 ──
siguiente ejecución  tickNumber = 42 (el contador avanza de uno en uno);
                     tickTime = Clock.NowMs(), es decir el presente real.
                     Las unidades en movimiento aparecen donde toca: la posición
                     se deriva de la polilínea y del reloj, no del número de ticks
                     corridos.
```

---

## 6. Determinismo

Sin determinismo no hay *simulation tests* reproducibles ni depuración fiable de incidentes.

| Regla | Cómo se cumple | Cómo se verifica |
|---|---|---|
| Nada de `time.Now()` en la simulación | `Clock` inyectado; `Run` (driver de infraestructura) lo lee una vez por tick y se lo pasa a `Step(nowMs)`; los manejadores de la fase 2 releen ese **mismo** `Clock` (§2). La única `time.Now()` del loop mide la duración del propio tick, que es observabilidad y no entra en el estado | Lint/test de arquitectura: prohibido `time.Now` bajo `internal/domain` e `internal/game/simulation` |
| Nada de `rand` global | `RandomSource` inyectada (`internal/clock/random.go`) y sembrada con `EO_WORLD_SEED` | Igual que arriba, para `math/rand` |
| **Prohibido iterar mapas de Go sin ordenar** | Todo recorrido de entidades que produzca salida observable usa un slice de IDs en orden ascendente, o `slices.Sort` explícito antes de iterar | Revisión + test que ejecuta dos veces el mismo escenario y compara el estado y la traza de eventos |
| Orden estable de entidades | Unidades por `id` ascendente; ciudades por `id`; chunks recorridos por `(cy, cx)` | Simulation test de doble ejecución (`reproducibilidad`) |
| Orden estable de comandos | FIFO del canal dentro de un tick; en tests, la secuencia de inyección es el orden | Los simulation tests inyectan comandos explícitamente, sin concurrencia |
| Desempate del A* | `(f, h, y, x)`, con costes enteros escalados (`costScaleOrtho = 1000`, `costScaleDiag = 1414`) y heurística octile **ponderada por `MinTerrainCostUnits` = 6** (ROAD, el terreno más barato): `h = 6*1000*rectos + 6*1414*diagonales`. Usar el coste de la hierba haría la heurística inadmisible en un mundo con caminos más baratos | Tests de `internal/pathfinding`: determinismo, preferencia de ROAD sobre FOREST, corner cutting |
| Aritmética en enteros | Instantes en `int64` epoch ms; costes de terreno en `costUnits` enteros; redondeo **al milisegundo más cercano por segmento**, y sólo después acumulación | Test del ejemplo numérico canónico en `internal/domain/movement` |
| Un solo escritor | El estado mutable pertenece al loop; `simulation.Simulation` no tiene mutexes a propósito: el aislamiento es por diseño, no por candados | `go test -race` |

Nota sobre el alcance del determinismo: es **determinismo dado el mismo instante de tick y la misma
secuencia de comandos drenados**. En producción, el reparto de comandos entre ticks depende de la
llegada por red y no se pretende reproducible; en los *simulation tests* la secuencia es fija, el
instante lo fija un `FakeClock` y el resultado es exacto.

---

## 7. Integración con FakeClock (simulation tests)

El loop separa **driver** de **simulación**:

- `Run(ctx)` es el driver de producción: calcula el próximo instante absoluto, espera con un
  `time.Timer`, lee el reloj y llama a `Step(nowMs)`. Es el único punto que programa esperas.
- `Step(nowMs int64)` ejecuta **exactamente un tick** con las 8 fases sobre el instante que recibe.
  Está separado de `Run` a propósito: es la unidad de ejecución de los tests.

```go
// Simulation test: "avanza 10 s y asserta el estado exacto".
func TestVillagerReachesTarget(t *testing.T) {
    clk := clock.NewFake(1_700_000_000_000) // instante fijo, sin dependencia del reloj real
    commands := make(chan simulation.Command, 16)
    sim := simulation.New(state, simulation.Deps{Clock: clk, Pathfinder: pf, /* ... */})
    lp := loop.New(sim, clk, commands, loop.Config{
        TickDuration:       100 * time.Millisecond,
        FlushIntervalTicks: 50,
        MaxCommandsPerTick: 1024,
    }, nil, nil, log)

    commands <- simulation.MoveUnit{PlayerID: p, UnitID: 1, Target: world.Tile{X: 10, Y: 10}, RequestID: reqID}

    for i := 0; i < 100; i++ { // 100 ticks × 100 ms = 10 s de simulación, en microsegundos de CPU
        clk.Advance(100 * time.Millisecond)
        lp.Step(clk.NowMs())
    }

    u, _ := sim.State().Unit(1)
    require.Equal(t, world.Tile{X: 10, Y: 10}, u.Tile())
    require.Equal(t, unit.StatusIdle, u.Status)
}
```

Reglas de los simulation tests:

- **Nunca** llaman a `Run` ni a `time.Sleep`. El tiempo lo mueve `FakeClock.Advance` y el tick se
  dispara con `Step`.
- El instante inicial se fija en el test; los instantes esperados se calculan, no se aproximan con
  tolerancias.
- El `Persister` se sustituye por un doble que **registra** los trabajos encolados (o por
  `simulation.NoopPersister`): así se assertan también los efectos durables sin Postgres. Lo mismo
  vale para el `Broadcaster` con `simulation.NoopBroadcaster` o un doble que capture los mensajes.
- El `Pathfinder` puede sustituirse por uno que devuelva un path fijo cuando el test es sobre el
  movimiento y no sobre la búsqueda.
- Doble ejecución del mismo escenario ⇒ mismo estado final y misma traza de mensajes.

Ver la clasificación completa de niveles (unit, integration, contract, simulation, recovery, load) en
[la estrategia de tests](../testing/strategy.md).

---

## 8. Pseudocódigo del loop

Estructura real de `internal/game/loop/loop.go`, reducida a lo esencial:

```go
package loop

// Run es el driver de producción: calendario en TIEMPO ABSOLUTO y descarte de vencimientos perdidos.
func (l *Loop) Run(ctx context.Context) error {
    period := l.cfg.TickDuration
    start := l.clk.Now()
    timer := time.NewTimer(period)
    defer timer.Stop()

    var scheduled int64 = 1
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-timer.C:
        }

        l.Step(l.clk.NowMs())          // el instante del tick lo aporta el reloj, no el contador

        elapsed := l.clk.Now().Sub(start)
        scheduled++
        next := time.Duration(scheduled) * period
        if next <= elapsed {
            // Vamos por detrás: se DESCARTAN los vencimientos perdidos y se reancla el calendario.
            if missed := int64(elapsed/period) - scheduled + 1; missed > 0 {
                scheduled += missed
                l.log.Warn("ticks perdidos descartados", "missed", missed, "tick", l.tick)
            }
            next = time.Duration(scheduled) * period
            if next <= elapsed {
                next = elapsed + period
            }
        }
        timer.Reset(next - elapsed)
    }
}

// Step ejecuta UN tick completo en el instante indicado. Es lo que ejercitan los simulation tests.
func (l *Loop) Step(nowMs int64) {
    started := time.Now()               // sólo para medir: no entra en el estado

    l.tick++
    l.sim.State().SetTick(l.tick)

    l.drainCommands()                   // fases 1-2
    l.sim.AdvanceMovements(nowMs)       // fase 3
    // fase 4: reservada a combate — fuera de MVP, sin llamada
    l.sim.ProcessTimers(time.UnixMilli(nowMs).UTC())   // fase 5

    // Fases 6-7: la membresía de chunk y los deltas ya se aplicaron y emitieron dentro de las
    // fases anteriores, según ocurrían los hechos.

    // Fase 8: encolar persistencia. Nunca I/O síncrono aquí.
    if l.cfg.FlushIntervalTicks > 0 && l.tick%uint64(l.cfg.FlushIntervalTicks) == 0 {
        l.sim.FlushDirty()
    }

    l.observe(started, nowMs)           // métricas, latido de /ready
}

// drainCommands consume la cola sin bloquearse jamás, con tope por tick.
func (l *Loop) drainCommands() {
    for i := 0; i < l.cfg.MaxCommandsPerTick; i++ {   // 1024 por defecto
        select {
        case cmd, ok := <-l.commands:
            if !ok {
                return
            }
            l.applyCommand(cmd)
        default:
            return                                    // la cola se vació: no se espera a nadie
        }
    }
    l.log.Warn("límite de comandos por tick alcanzado; el resto espera al siguiente",
        "limit", l.cfg.MaxCommandsPerTick, "tick", l.tick)
}

// applyCommand aísla el pánico de un comando concreto.
func (l *Loop) applyCommand(cmd simulation.Command) {
    defer func() {
        if p := recover(); p != nil {
            l.log.Error("pánico al aplicar un comando", "panic", p, "tick", l.tick)
            l.metrics.CommandsTotal.WithLabelValues("unknown", observability.ResultFailed).Inc()
        }
    }()
    l.sim.Apply(cmd)
    l.metrics.CommandsTotal.WithLabelValues(commandLabel(cmd), observability.ResultAccepted).Inc()
}
```

Dos detalles que este código fija y que no conviene idealizar:

1. **El drenaje está acotado por `MaxCommandsPerTick`, no por `len(ch)` al entrar.** Una avalancha no
   puede convertir un tick en un bloqueo; lo que no entra espera al tick siguiente, con `warn`.
2. **La mutación en RAM precede al encolado durable, no al revés.** `handleMoveUnit` cancela el
   movimiento previo, registra el nuevo, pasa la unidad a `MOVING` y **después** llama a
   `Persister.Submit`. Si la cola de persistencia está saturada, `Submit` descarta el trabajo, lo
   registra como error e invoca `OnPermanentFailure` **si el trabajo declaró uno** (`movement.start`
   sí; `movement.cancel`, `movement.complete`, `city.presence` y `units.flush` no); en ningún caso se
   revierte la mutación en RAM. Es la contrapartida honesta de no hacer I/O en el tick, y está
   descrita como ventana de riesgo en [estrategia de persistencia](./persistence.md).

---

## 9. Secuencia completa de un `unit.move`

```mermaid
sequenceDiagram
    autonumber
    participant C as Cliente (PixiJS)
    participant R as WS reader (goroutine)
    participant Q as commands chan
    participant L as Game loop (1 goroutine)
    participant P as Persistence worker
    participant H as Hub / WS writer

    C->>R: unit.move { v:1, requestId, payload:{unitId, target} }
    R->>R: ReadLimit 16384 B, rate limit 20/s (burst 40), parseo del envelope v1
    R->>R: idempotencia: SETNX idem:{playerId}:{requestId} en Redis (TTL 300 s)
    alt cola del loop llena
        R->>R: comando descartado y registrado como error
    else encolado
        R->>Q: Command{MoveUnit}
    end

    Note over L: TICK N — tickTime = Clock.NowMs()

    L->>Q: FASE 1 drain (no bloqueante, tope MaxCommandsPerTick)
    L->>L: FASE 2 ownership -> status -> destino -> A* octile
    alt validación falla
        L->>H: unit.move.rejected { unitId, code, message }  (sin system.error adicional)
        H-->>C: seq++, ts, payload
    else validación OK
        L->>L: cancela en RAM el movimiento ACTIVE previo (razón REPLACED)
        L->>L: construye la polilínea temporizada [{x,y,tMs}...], primer waypoint tMs=0
        L->>L: unit.status = MOVING, unidad marcada como dirty
        L->>P: ENCOLA el job durable "movement.start" (cancel + insert, una transacción)
        L->>H: unit.move.accepted (al solicitante) + unit.movement.started (al chunk de origen)
        H-->>C: deltas del chunk (seq++, ts)
        P->>P: FUERA DEL TICK: UPDATE unit_movements SET status='CANCELLED' ... ; INSERT ... 'ACTIVE'
    end

    Note over L: TICKS N+1 .. N+k — la unidad avanza

    L->>L: FASE 3 PositionAt(tickTime): último waypoint con tMs <= tickTime - start_time_ms
    L->>L: FASE 6 reindexado de chunk al cambiar de tile
    L->>H: FASE 7 entity.update al chunk de la posición nueva
    H-->>C: deltas (el cliente interpola sub-tile, sólo visual)
    L->>P: FASE 8 cada 50 ticks: encola "units.flush" con el dirty-set

    Note over L: TICK N+k — arrival_time_ms alcanzado

    L->>L: FASE 3 snap al tile final, unit.status = IDLE, movimiento retirado de RAM
    L->>P: ENCOLA "movement.complete"
    L->>H: unit.movement.completed
    H-->>C: seq++, ts, payload
```

Puntos que este diagrama fija:

- El cliente **nunca** envía posiciones intermedias; envía un destino y recibe hechos.
- El `seq` y el `ts` los estampa `Session.Send` en cada conexión, no el loop.
- **La confirmación al cliente precede al `COMMIT`.** El tick encola y sigue simulando; el worker
  escribe después. La ventana de riesgo —si el proceso muere entre la aceptación y el commit, ese
  movimiento se pierde y la unidad queda en su última posición consolidada— es un RPO documentado en
  [estrategia de persistencia](./persistence.md), no un descuido.
- La posición durante el movimiento se **deriva** de `unit_movements`; el volcado por lotes cada 50
  ticks puede escribir una posición intermedia, pero en la recuperación prevalece la derivada.
- Si el proceso muere entre el tick N y el N+k, al reiniciar `simulation.Hydrate` reanuda o completa
  el movimiento desde la polilínea, y descarta como `FAILED` cualquier polilínea que ya no valide
  contra el mundo actual (ver [game-server.md](./game-server.md)).

---

## 10. Errores que puede producir el loop

La lógica de control usa `code`, jamás el texto humano. **El mensaje portador depende del tipo de
fallo**, y esta distinción es normativa:

| Fallo | Mensaje portador |
|---|---|
| Dominio de `unit.move` | `unit.move.rejected { unitId, code, message }` — y **sólo** ese |
| Dominio de `unit.cancel_move` | `system.error { code, message }` con el `requestId` correlacionado |
| Transporte, versión, sesión, saturación | `system.error { code, message, details? }` |

| Fase | Códigos posibles |
|---|---|
| 1 | Ninguno: la saturación de la cola la detecta el productor (`internal/websocket`), que descarta el comando y lo registra |
| 2 | `UNIT_NOT_FOUND`, `UNIT_NOT_OWNED`, `UNIT_NOT_MOVABLE`, `UNIT_DEAD`, `UNIT_GARRISONED`, `TARGET_OUT_OF_BOUNDS`, `TARGET_NOT_WALKABLE`, `PATH_NOT_FOUND`, `PATH_TOO_LONG`, `INTERNAL_ERROR` |
| 3–8 | Ninguno hacia el cliente: un fallo aquí se registra con `tick`, se cuenta y, si el latido del loop se detiene, `/ready` deja de responder `200` |

`INVALID_TARGET`, `CITY_NOT_FOUND`, `CITY_PROTECTED`, `TREATY_REQUIRED`, `POPULATION_LIMIT_REACHED`,
`NOT_IMPLEMENTED`, `UNAUTHORIZED`, `FORBIDDEN` y `MESSAGE_TOO_LARGE` están en el catálogo de 22 códigos
—que un contract test verifica contra `packages/protocol`— pero **ningún camino del MVP los emite
todavía**: pertenecen a comandos que aún no existen, o se resuelven cerrando el socket con su código
de cierre (`4401` para credenciales, `1009` del propio `ReadLimit` para un frame sobredimensionado)
sin llegar a mandar un `system.error`. `INVALID_MESSAGE`, `UNSUPPORTED_VERSION` y `RATE_LIMITED` sí se
emiten, pero en la capa `websocket` y antes de que exista un comando: nunca llegan al loop.

---

## 11. Observabilidad del loop

| Señal | Nombre | Uso |
|---|---|---|
| Métrica | `eo_game_tick_duration_seconds` (histogram) | Presupuesto de la sección 4; alerta sobre p99 |
| Métrica | `eo_game_tick_overruns_total` | Ticks que superaron el período |
| Métrica | `eo_commands_total{type,result}` | `type` = `unit.move`, `unit.cancel_move`, `player.connected`, `player.disconnected`, `unknown`; `result` = `accepted` o `failed` (pánico recuperado) |
| Métrica | `eo_pathfinding_requests_total{result}`, `eo_pathfinding_duration_seconds` | Coste del A* dentro de la fase 2, agregado al final del tick |
| Métrica | `eo_active_units`, `eo_active_movements`, `eo_persistence_queue_depth` | Tamaño del mundo activo y salud del carril de escritura |
| Log | `slog` JSON con `tick` | Todo log emitido desde el loop incluye `tick`; los originados por un comando incluyen además `player_id`, `session_id`, `request_id` |
| Readiness | `GET /ready` | Incluye "loop vivo": el loop late en cada tick (`Health.BeatLoop`) y el latido no puede tener más de **5 s** de antigüedad. `GET /health` (liveness) no depende de nada |

Regla de logging dentro del tick: **nada de logs por entidad en el camino caliente**. Se registra por
evento de dominio y por excepción; un log por unidad y por tick a 10 Hz saturaría la salida y añadiría
latencia al presupuesto.

---

## 12. Fuera de MVP

- Fase 4 (`resolve simulation`) con combate real: reservada y hoy sin ninguna llamada en `Step`.
- Paralelización de fases o del A* dentro del tick.
- Métrica dedicada de ticks saltados por catch-up (hoy sólo log + `eo_game_tick_overruns_total`).
- **Orden durable por unidad**: un único worker de persistencia, o particionado de la cola por
  `unit_id`, para que el orden de commit coincida siempre con el orden de aplicación en RAM. Hoy la
  garantía la aporta el índice único parcial `unit_movements_one_active_per_unit` más los reintentos.
- Tick rate variable o adaptativo en caliente: `EO_TICK_RATE_HZ` se lee en el bootstrap y no cambia
  durante la vida del proceso.
- Reproducción de ticks históricos (*replay* determinista de una partida completa) como herramienta de
  auditoría.
