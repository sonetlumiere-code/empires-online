# ADR-007: Frecuencia del game loop fija a 10 Hz configurable

Propósito: justificar por qué el bucle de simulación autoritativo corre a 10 Hz (`EO_TICK_RATE_HZ=10`, período de 100 ms), qué presupuesto de cómputo impone ese período y qué política se aplica cuando un tick se pasa de tiempo.

- **Estado:** Aceptado
- **Fecha:** 2026-09-09
- **Decisores:** equipo de arquitectura
- **Relacionados:** [ADR-002](ADR-002-authoritative-server.md), [ADR-008](ADR-008-grid-coordinate-system.md), [ADR-011](ADR-011-movement-timed-polyline.md)
- **Ámbito:** `services/game-server/internal/game/loop`

---

## Contexto

El game server es autoritativo: el cliente envía intención y el servidor determina la verdad. Toda esa
verdad se produce dentro de un bucle de simulación con un orden de fases fijo y determinista
(ocho fases, de `drain commands` a `enqueue persistence`), descrito en
[../architecture/game-loop.md](../architecture/game-loop.md). Elegir su frecuencia fija tres cosas a la vez:

1. **La latencia mínima de respuesta a un comando.** Un `unit.move` que llega justo después de la fase 1
   espera hasta el siguiente tick para ser drenado.
2. **El presupuesto de CPU por iteración.** Todo lo que hacen las ocho fases debe caber en un período.
3. **La cadencia máxima de emisión de deltas.** La fase 7 (`emit deltas`) no puede emitir más lotes por
   segundo que ticks por segundo.

La granularidad del dominio ya está fijada por el canon y es gruesa: el servidor razona en **tiles**, no en
píxeles ni en posiciones continuas ([ADR-008](ADR-008-grid-coordinate-system.md)), y la unidad más rápida
del MVP, `VILLAGER`, tarda **600 ms** en cruzar un tile de `GRASSLAND`. El coste temporal de un segmento se
calcula en aritmética entera, con `costUnits` del terreno destino (`CostBase = 10` equivale a
multiplicador 1.0) y √2 en punto fijo, redondeando **cada segmento** al milisegundo más cercano antes de
acumular:

```go
ms := (baseMsPerTile*costUnits + 5) / 10            // redondeo al ms más cercano
if diagonal { ms = (ms*1414214 + 500000) / 1000000 } // √2 en punto fijo, mismo redondeo
```

| Terreno | `costUnits` | Ortogonal | Diagonal | Ticks a 10 Hz (ortogonal) |
|---|---|---|---|---|
| `ROAD` | 6 | 360 ms | 509 ms | 3.6 |
| `GRASSLAND` | 10 | 600 ms | 849 ms | 6.0 |
| `FOREST` | 16 | 960 ms | 1358 ms | 9.6 |
| `HILL` | 18 | 1080 ms | 1527 ms | 10.8 |

El cambio de estado discreto más rápido que el dominio puede producir es, por tanto, **una transición de
tile cada 360 ms**. Cualquier frecuencia de tick se juzga contra ese número, no contra la sensación visual:
la suavidad de la pantalla la produce el cliente interpolando a 60 fps sobre la polilínea temporizada que ya
recibió ([ADR-011](ADR-011-movement-timed-polyline.md)), no el servidor mandando posiciones.

Además, el sistema tiene una restricción dura heredada del canon: **el tick jamás ejecuta I/O bloqueante
contra PostgreSQL**. La persistencia se encola (fase 8) y la ejecutan workers fuera del bucle. Eso deja el
presupuesto del tick para trabajo de CPU y de memoria, no para esperas.

---

## Decisión

**El game loop corre a tick fijo de 10 Hz, con período de 100 ms, configurable mediante
`EO_TICK_RATE_HZ` (valor por defecto `10`).**

Reglas que acompañan a la decisión:

1. **Calendario en tiempo absoluto, no acumulativo.** El planificador guarda el instante de arranque del
   bucle y calcula el vencimiento del tick número *n* como `start + n * tickDuration`; nunca suma el período
   al reloj del tick anterior. Así el bucle no acumula deriva, aunque un tick concreto tarde de más.
   `world_state.epoch_ms` es el origen temporal de la simulación —lo que permite reanudar `tickNumber`
   (`uint64` monótono) desde `world_state.current_tick` tras un reinicio—, y el instante que reciben las
   fases es el reloj inyectado (`clock.NowMs()`) al empezar el tick.
2. **Ticks perdidos DESCARTADOS, jamás espiral de recuperación.** Si al terminar un tick el reloj ya pasó
   el vencimiento del siguiente, el bucle **no** ejecuta los ticks atrasados en ráfaga: los descarta, se
   reancla al calendario absoluto (`scheduled += missed`) y registra un aviso `ticks perdidos descartados`
   con cuántos se saltaron. La métrica `eo_game_tick_overruns_total` es cosa aparte: cuenta los ticks cuya
   **ejecución** superó el período, y se incrementa al observar la duración del tick.
3. **Todo trabajo programado se expresa como «vence en o antes de `tickTime`», nunca como «exactamente en
   el tick N».** Esta regla es la que hace que saltar ticks sea seguro: un temporizador de presencia o de
   protección que debía dispararse en un tick saltado se dispara en el siguiente, con retraso acotado por el
   salto, no se pierde.
4. **La posición de las unidades no depende de que el tick se ejecute.** Es una función pura de
   `(polilínea, tiempo)`. Un tick saltado no pierde movimiento; como mucho retrasa la *notificación* de una
   llegada.
5. **La frecuencia es configuración, no constante de código.** Ningún módulo hardcodea 100 ms;
   todo pasa por `internal/config` ([../operations/configuration.md](../operations/configuration.md)).

### Planificador (`Loop.Run`, forma real)

```go
// period = tickDuration = 1000 ms / EO_TICK_RATE_HZ  (100 ms con el valor por defecto)
start := clock.Now()
scheduled := int64(1)
timer := time.NewTimer(period)

for {
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-timer.C:
    }

    Step(clock.NowMs()) // fases 1..8, orden fijo, sin I/O bloqueante contra PostgreSQL

    elapsed := clock.Now().Sub(start)
    scheduled++
    next := time.Duration(scheduled) * period
    if next <= elapsed {
        // Vamos por detrás: los ticks perdidos se DESCARTAN y el calendario se
        // reancla al presente. Nunca se ejecutan en ráfaga.
        if missed := int64(elapsed/period) - scheduled + 1; missed > 0 {
            scheduled += missed
            log.Warn("ticks perdidos descartados", "missed", missed)
        }
        next = time.Duration(scheduled) * period
        if next <= elapsed {
            next = elapsed + period
        }
    }
    timer.Reset(next - elapsed)
}
```

`Step(nowMs)` está separado de `Run` a propósito: los tests de simulación lo invocan directamente con un
`FakeClock`, avanzan N ticks y assertan el estado exacto sin depender de temporizadores reales ni de la
velocidad de la máquina. Ese es el mecanismo por el que se cumple el principio de determinismo del canon:
el bucle no aporta nada al resultado más allá de decidir *cuándo* se llama a `Step`.

### Presupuesto por fase

Presupuesto objetivo de p99 por fase, sobre los 100 ms disponibles. Es un objetivo de diseño, no una
medición: el proyecto es greenfield y estos números se validan con `eo_game_tick_duration_seconds`.

| # | Fase | Objetivo p99 | Nota |
|---|---|---|---|
| 1 | `drain commands` | 2 ms | Cola no bloqueante, lote acotado por el rate limit de WS. |
| 2 | `validate & apply commands` | 15 ms | Incluye A\*; es la fase con más varianza. |
| 3 | `advance movement` | 5 ms | Evaluación analítica de polilíneas, sin replay. |
| 4 | `resolve simulation` | 0 ms | Reservado para combate. **Fuera de MVP.** |
| 5 | `process timers/scheduled events` | 3 ms | Presencia, protección, territorio. |
| 6 | `update world state / interest sets` | 5 ms | Recalcula suscripciones por chunk. |
| 7 | `emit deltas` | 15 ms | Serialización JSON y difusión por chunk. |
| 8 | `enqueue persistence` | 2 ms | Solo encolado; el I/O ocurre en workers. |
| | **Total objetivo** | **≈47 ms** | **≈53 % de holgura** sobre el período. |

Tres precisiones sobre cómo se materializan esas fases en `Loop.Step`, para que nadie busque ocho bloques
de código donde no los hay:

- Las fases 6 y 7 no son una pasada aparte: los conjuntos de interés y los deltas se emiten **dentro** de
  las fases anteriores, según ocurren los hechos.
- El drenaje de la fase 1 está acotado por `MaxCommandsPerTick` (1024 por defecto) y nunca bloquea: lo que
  no entra en un tick espera al siguiente, y se registra al alcanzar el límite.
- La fase 8 no vuelca en todos los ticks: `FlushDirty` se ejecuta cada `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS`
  (50 por defecto, es decir cada 5 s a 10 Hz). Además, cada comando se aplica con aislamiento de pánico: un
  bug en una regla concreta se registra y se cuenta, pero no tumba la simulación del mundo entero.

Diseñar para consumir menos de la mitad del presupuesto no es conservadurismo gratuito: la fase 2 ejecuta
A\* dentro del tick y una sola búsqueda que agote `EO_PATHFINDING_MAX_NODES=20000` puede comerse una
fracción grande del período. La exposición se acota con el rate limit por conexión
(`EO_WS_RATE_LIMIT_PER_SECOND=20`, burst 40), pero **no** está acotada por número de conexiones: una
ráfaga simultánea de órdenes de movimiento de muchos jugadores es el escenario que primero hará crecer
`eo_game_tick_overruns_total`. Mover el pathfinding a un pool de workers fuera del tick es la primera
palanca prevista, y requerirá su propio ADR. Detalles del algoritmo en
[../architecture/pathfinding.md](../architecture/pathfinding.md).

---

## Alternativas consideradas

### Tick a 1 Hz

**A favor (real).** Presupuesto de 1000 ms por tick: A\*, serialización y hasta un descuido de rendimiento
caben sin esfuerzo. Un orden de magnitud menos de trabajo de planificación, de despertares del scheduler
del sistema operativo y de lotes de deltas. Para la parte del mundo que evoluciona en minutos —protección
offline con `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS=300`, presencia con TTL de 30 s— 1 Hz sobra.

**En contra.** La latencia de comando llega a **1000 ms en el peor caso y 500 ms de media**, solo por
esperar el drenaje, antes de sumar RTT de red. Un click de movimiento en un RTS que tarda medio segundo en
acusar recibo se percibe como un juego roto, no como un juego lento. Peor: 1000 ms es **mayor** que la
transición de tile más rápida del dominio (360 ms en `ROAD`), de modo que entre dos ticks una unidad puede
haber cruzado casi tres tiles; los temporizadores, los conjuntos de interés y las entradas y salidas de
chunk se evaluarían con una granularidad más gruesa que la del propio dominio. Rechazado.

### Tick a 20–30 Hz

**A favor (real).** La latencia de drenaje baja a 50 ms (20 Hz) o 33 ms (30 Hz) en el peor caso.
Es el rango habitual en shooters y en RTS con control de unidad fino, y deja margen si algún día el
combate exige resolución temporal más fina.

**En contra.** El beneficio real es de **50 ms en el peor caso** frente a 10 Hz; comparado con un RTT de
internet típico de 30–80 ms y con el frame time del cliente (16,7 ms a 60 fps), esa mejora está por debajo
del ruido. A cambio se pagan dos costes concretos: el presupuesto por tick cae a 50 ms o 33 ms —lo que deja
a una sola búsqueda A\* de coste máximo con capacidad para provocar overruns por sí sola— y el número
máximo de lotes de deltas por segundo se duplica o triplica, con su sobrecarga de envelope
(`{v, type, seq, ts, requestId?, payload}`) pagada en cada lote aunque el contenido sea mínimo.
Y sobre todo: **no compra suavidad visual**, porque la suavidad la produce la interpolación del cliente
sobre la polilínea, no la frecuencia de muestreo del servidor. Rechazado por coste sin beneficio percibible.

### Tick variable (delta time) o bucle dirigido por eventos

**A favor (real).** Es el modelo más eficiente en CPU: no se hace nada cuando no hay nada que hacer, y en un
mundo persistente 24/7 con actividad muy desigual eso es tentador. Un bucle dirigido por eventos elimina
además la latencia de drenaje: cada comando se procesa al llegar.

**En contra.** Rompe el principio de determinismo del canon. Con `dt` variable el resultado de la
simulación depende de la carga de la máquina, y dos ejecuciones del mismo escenario dejan de ser
comparables: los tests de simulación («avanza 10 s con `FakeClock` y asserta el estado exacto») dejan de
tener sentido, y un bug de producción deja de ser reproducible. Con orden de eventos en vez de orden de
fases, la reproducibilidad depende del orden de llegada por red, que no controlamos. Además el canon fija un
orden de fases determinista dentro del tick, y ese orden es precisamente lo que un bucle por eventos
disuelve. Rechazado.

### Tick fijo con recuperación de ticks atrasados (*catch-up*)

**A favor (real).** Conserva el número de ticks ejecutados: cada tick lógico se ejecuta exactamente una vez,
lo cual es la opción correcta en simulaciones donde el estado se integra paso a paso y saltar un paso
cambia el resultado (física, integración numérica).

**En contra.** Si la causa del retraso es carga sostenida, ejecutar los ticks perdidos en ráfaga añade más
carga y provoca la clásica *espiral de la muerte*: cada ronda de recuperación se retrasa más que la
anterior. En Empires Online el argumento a favor ni siquiera aplica: el estado que avanza en el tiempo
—la posición durante un movimiento— es **analíticamente reconstruible**, no integrado paso a paso, así que
saltar ticks no cambia ningún resultado. Rechazado; se adopta el descarte de los ticks perdidos (regla 2 de «Decisión»).

---

## Consecuencias

### Positivas

- **Latencia percibida acotada y honesta.** El coste añadido por el bucle es de 0 a 100 ms, 50 ms de media.
  Sumado a un RTT típico, el jugador ve confirmación de su orden en el rango de 80–180 ms, que para un RTS
  de mundo persistente es holgadamente aceptable.
- **Determinismo verificable.** El resultado de un tick depende solo del estado, de los comandos y del
  instante que se le pasa a `Step(nowMs)`. Con `FakeClock`, un test de simulación avanza N ticks y asserta
  el estado exacto, sin depender del reloj de pared ni de la velocidad de la máquina.
- **Presupuesto amplio.** 100 ms por tick con un objetivo de 47 ms deja espacio para que la fase 2 tenga
  varianza sin que el jugador lo note.
- **Volumen de deltas desacoplado del tick.** Como el movimiento viaja como polilínea, una unidad en marcha
  emite `unit.movement.started` y `unit.movement.completed`, no diez `entity.update` por segundo. El tick
  acota el número **máximo** de lotes por segundo (10); el volumen real lo determina la tasa de eventos
  discretos, y un área sin actividad no emite nada.
- **Frecuencia ajustable sin refactor.** Si la telemetría demuestra que hace falta más resolución,
  `EO_TICK_RATE_HZ` se sube. Ningún invariante del dominio depende del valor 10.

### Negativas

- **100 ms de latencia añadida en el peor caso, siempre.** No hay atajo: un comando que llega 1 ms después
  de la fase 1 espera 99 ms. No existe procesamiento fuera de banda para comandos urgentes, y añadirlo
  rompería el orden determinista.
- **Un tick saltado retrasa notificaciones.** Un `unit.movement.completed` puede emitirse hasta el ancho
  del salto más tarde de lo debido. El estado durable es correcto —la posición se deriva del tiempo— pero
  el cliente ve la confirmación tarde. En saltos grandes esto es visible.
- **La política de salto hace que el número de ticks ejecutados no sea función determinista del tiempo real
  de producción.** El determinismo que garantizamos es «dada la misma secuencia de ticks y los mismos
  comandos, el mismo resultado», no «el mismo número de ticks en dos ejecuciones bajo carga distinta».
  Por eso la regla 3 de «Decisión» no es opcional: cualquier lógica escrita como «en el tick N exactamente» es un
  bug latente que solo se manifiesta bajo carga.
- **A\* dentro del tick es la fuente de riesgo principal.** El peor caso de una sola búsqueda no está
  acotado por debajo del período; solo está acotado por `EO_PATHFINDING_MAX_NODES`. Bajo ráfaga de órdenes
  simultáneas, `eo_game_tick_overruns_total` crecerá antes que ninguna otra métrica.
- **Subir la frecuencia es barato de configurar y caro de sostener.** Cambiar `EO_TICK_RATE_HZ` a 20 divide
  el presupuesto por dos sin cambiar el coste del peor caso de A\*. La variable existe, pero moverla sin
  medir es una forma rápida de fabricar overruns.

### Neutras

- El bucle es el **único** lugar donde se muta el estado del mundo: todo lo demás —conexiones WebSocket,
  HTTP, workers de persistencia— habla con él por canales. Eso concentra la contención en un punto y hace
  que cualquier necesidad futura de paralelizar la simulación pase por partir el mundo, no por añadir
  cerrojos.
- El número de ticks ejecutados y el número de ticks programados no coinciden bajo carga; el log de
  `ticks perdidos descartados` es el registro de esa diferencia.
- `EO_TICK_RATE_HZ` debe dividir exactamente a 1000 —el arranque lo valida y falla si no—, de modo que el
  período siempre es un número entero de milisegundos.

## Verificación

| Qué se verifica | Cómo |
|---|---|
| Duración de tick | Histograma `eo_game_tick_duration_seconds`, alerta sobre p99 > 50 ms. |
| Overruns | Contador `eo_game_tick_overruns_total`; su derivada distinta de cero es señal de investigación. |
| Determinismo | Tests de simulación con `FakeClock`: avanzar N ticks y assertar estado exacto. |
| Salto sin pérdida | Test que fuerza un retraso artificial y comprueba que los temporizadores vencidos se procesan en el tick siguiente y que la posición derivada es la correcta. |

Detalle de niveles y gates en [../testing/simulation-tests.md](../testing/simulation-tests.md) y
[../testing/strategy.md](../testing/strategy.md).

---

## Referencias

- [../architecture/game-loop.md](../architecture/game-loop.md) — orden de fases y ciclo de vida del bucle.
- [../architecture/pathfinding.md](../architecture/pathfinding.md) — coste y límites de A\*.
- [ADR-011](ADR-011-movement-timed-polyline.md) — por qué la posición no necesita ejecutarse tick a tick.
- [../operations/configuration.md](../operations/configuration.md) — `EO_TICK_RATE_HZ` y demás variables.
- [../operations/monitoring.md](../operations/monitoring.md) — métricas `eo_*` y umbrales.
- [README.md](README.md) — índice de ADR.

---

## Estado

**Aceptado** el 2026-09-09.

Se sustituiría por un ADR nuevo si `eo_game_tick_duration_seconds` demostrara que el presupuesto de 100 ms
es insuficiente con el mundo MVP, o si apareciera jugabilidad que exija resolución temporal más fina que
una transición de tile cada 360 ms. Mover el pathfinding fuera del tick —la primera palanca prevista contra
los overruns— no sustituye a este ADR: requiere el suyo propio.
