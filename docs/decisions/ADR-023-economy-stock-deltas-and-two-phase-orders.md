# ADR-023: El stock se persiste por deltas versionados y los encargos de la economía son órdenes en dos fases

Propósito: fijar cómo el primer slice de economía ([SPEC-ECON-WOOD](../specs/economy-wood.md)) escribe la
madera, las casas, los entrenamientos y los nacimientos a través de la cola de persistencia sin romper
`RN-ECON-008` —nunca madera gastada sin edificio ni edificio sin madera gastada—, sin rechazos espurios y sin
que un reintento cobre dos veces.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-10-27** |
| Ámbito | `internal/game/simulation` (`economy.go`, `gather.go`), `internal/persistence/postgres` (`EconomyRepo`), migración `000006`, `internal/game/loadcheck` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md) (cola ordenada por clave, el loop asigna ids que sólo él crea), [ADR-014](ADR-014-command-idempotency.md) (`requestId` con `UNIQUE` en la fila del efecto), [ADR-017](ADR-017-garrison-commands.md) (la orden en dos fases), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (toda escritura declara compensación) |

---

## Contexto

La spec fija el qué y deja el cómo:

- El stock de cada ciudad cambia con cada depósito (unos tres por minuto y aldeano) y se persiste por
  *dirty-flag*: entre dos volcados la RAM va por delante de la base hasta 5 s de depósitos (§7).
- Cada débito se confirma en la misma transacción que su efecto, y si la escritura falla la RAM se restaura
  con una compensación que vuelve al loop (`RN-ECON-008`, `RN-ECON-010`).
- La población se **reserva** al ordenar un entrenamiento: `population += 1` en la transacción del encargo
  (`RN-ECON-010`), para que el `CHECK (population <= population_limit)` de `cities` siga siendo la última
  barrera.

El tick no hace I/O. Los trabajos de la cola se ejecutan en el orden en que se encolaron por clave, con
reintentos, y su `Run` debe ser idempotente: un `COMMIT` cuya confirmación se pierde se reintenta y ya estaba
escrito. Si un trabajo agota sus intentos o no cabe en la cola, su compensación vuelve al loop por el buzón.

Dos hechos del código acotan las opciones. Los ids de `units` salen de su secuencia de identidad, que comparte
con la fundación de jugadores, y ésta escribe desde la goroutine HTTP, fuera del loop: el loop no puede
asignarlos como asigna los de `unit_movements` ([ADR-013](ADR-013-persistence-pipeline.md) §2.1), porque
colisionaría. Y la presencia de una ciudad que estaba `ONLINE` cuando cayó el proceso se queda `ONLINE` tras el
arranque aunque nadie se conecte ([presence.md](../specs/presence.md) §7.6).

## Decisión

**1. El stock se escribe por deltas versionados.** Cada stock en RAM lleva `amount`, una `version` que sube con
cada cambio y los depósitos pendientes de volcar. Toda escritura es
`amount = amount + delta, version = v WHERE version < v`: aplicar dos veces la misma versión no hace nada, y
una escritura vieja no pisa una nueva. Hay exactamente dos escritores:

- el volcado (`stocks.flush`, clave `city:<id>`, en la fase 8 con el de unidades), que escribe **sólo
  depósitos**, un delta positivo;
- el encargo (`city.build`, `city.train`), que escribe `depósitos pendientes − coste` **en la transacción de
  su efecto**. Los depósitos que aún no se habían volcado viajan con él, de modo que la base nunca ve un
  débito sin la madera que lo cubre en la RAM.

**2. `city.build` y `city.train` son órdenes en dos fases**, como la entrada en guarnición
([ADR-017](ADR-017-garrison-commands.md)). El tick valida contra la RAM y **reserva**: resta el coste, aparta el
tile o la plaza de producción y, al entrenar, suma uno a la población. La transacción de la cola comprueba la
idempotencia por `(city_id, request_id)`, aplica el delta, inserta el efecto y su `world_events`, y devuelve
el resultado al loop por el buzón. Con éxito, el loop confirma y difunde (`building.spawn`, `city.update`);
con un rechazo de dominio o un fallo definitivo, libera la reserva y devuelve los depósitos que viajaban con
ella. Nada se anuncia antes de existir en la base, así que no hay nada que retirar a la vista de terceros.

**3. El nacimiento de un aldeano también es en dos fases.** La fase 5 decide que una producción vencida nace y
dónde; la transacción inserta la unidad (la base asigna el id) y apunta ese id en
`city_productions.unit_id`, que es lo que hace idempotente el reintento; el loop recibe el id, incorpora la
unidad y emite `entity.spawn`. Es el mismo orden que el alta de un jugador (`INV-PLAYER-008`): el mundo en RAM
sólo conoce unidades que la base ya confirmó. La fila de la producción se conserva con su `unit_id` hasta el
siguiente encargo de la ciudad, que la sustituye.

**4. La población cuenta lo reservado.** `cities.population` es el número de unidades vivas de la ciudad
**más** sus producciones en curso. [INV-CITY-009](../invariants/city.md#inv-city-009) deja de ser cierto tal
como está escrito y se deroga; lo sustituye [INV-CITY-017](../invariants/city.md#inv-city-017).

**5. Completar una casa es optimista con compensación**, como el cierre de un movimiento: el tick la da por
completa y difunde, y encola `building.complete`, condicionado a `status = 'UNDER_CONSTRUCTION'` para que el
reintento no sume dos veces. Si se pierde, la compensación apunta la casa y el siguiente volcado la reencola
([ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md)).

**6. Cada transición de una tarea escribe, en una transacción, la fila de `unit_tasks`, el tramo de movimiento
que cierra o abre y `units.status`.** Si se pierde, la compensación termina la tarea en el loop por el camino
de una cancelación, que escribe un estado nuevo y completo. Cancelar el tramo de una unidad por cualquier otra
vía (orden de movimiento, `unit.cancel_move`, ruta bloqueada) borra su tarea en la transacción que cierra el
movimiento.

**7. Un viaje nuevo exige al dueño presente**: la ciudad `ONLINE` y el dueño con sesión viva o dentro del
margen de reconexión ([INV-ECON-006](../invariants/economy.md#inv-econ-006)). Cierra para la economía el hueco
de §7.6 de la presencia sin tocar su autómata.

**Fuera de esta decisión.** La tarea persistente (`unit_tasks`) **no** se adopta como patrón general para
cualquier acción de varios tramos: modela `GATHER_WOOD` y nada más. Generalizarla con una sola acción que la
use sería diseñar a ciegas; se decidirá con la segunda.

## Alternativas consideradas

**Escribir el stock como valor absoluto (`amount = <RAM>`).** Es lo más simple, y es idempotente sin versión.
Pero el volcado que sale después de un encargo y antes de que su transacción se resuelva lleva un valor que ya
descuenta un coste no confirmado: si el encargo falla, la base queda con madera gastada sin edificio hasta el
volcado siguiente, y una caída en ese intervalo la deja así para siempre. Suspender el volcado mientras haya
encargos pendientes lo arregla para uno, no para dos encargos seguidos de la misma ciudad.

**Débito relativo en la transacción y volcado absoluto.** Respeta `RN-ECON-008` al pie de la letra, pero la
base va por detrás de la RAM: con 40 de madera recién depositada y 0 volcada, una casa de 30 viola el `CHECK`
de la base aunque el jugador la pueda pagar. Sería un `INSUFFICIENT_RESOURCES` espurio en el camino más común.

**Encargos optimistas con compensación, como el inicio de un movimiento.** La casa aparecería en el mismo tick.
Exige que el loop asigne el id del edificio, y una compensación tendría que retirar a la vista de todos un
edificio ya anunciado. La latencia que ahorra es una ida y vuelta a la base sobre una construcción de 15 s.

**Reservar ids de `units` por bloques para nacer en el mismo tick.** El aldeano aparecería en el instante exacto
de `completes_at_ms`. Exige coordinar la secuencia con la fundación (un `setval` por bloque y su relleno con
I/O), y un bloque perdido en una caída deja huecos. Un nacimiento confirmado un tick más tarde no cambia nada
del juego; la coordinación de secuencias sí añade una forma nueva de fallar.

**No reservar población y contar las producciones al validar.** Evita derogar un invariante. Pero contradice
`RN-ECON-010`, y la base pierde su última barrera: el `CHECK` de `cities` ya no vería la plaza reservada.

## Consecuencias

### Positivas

- `INV-ECON-001` e `INV-ECON-002` se cumplen en la base tras cada `COMMIT`, también con fallos inyectados y con
  reintentos; un débito nunca se aplica dos veces.
- Ningún rechazo espurio por depósitos sin volcar.
- Nada se anuncia a terceros antes de existir: no hay `building.despawn` de compensación.
- El arranque no recalcula nada: los tiempos absolutos persistidos y la versión del stock bastan.

### Negativas

- `building.spawn` y el `entity.spawn` de un aldeano llegan una ida y vuelta a la base después del tick que
  los decide.
- La versión del stock es contabilidad nueva que el código tiene que mantener en cada cambio.
- `INV-CITY-009` se deroga; quien lea `cities.population` tiene que saber que incluye lo reservado.
- `city_productions` conserva la última producción terminada de cada ciudad hasta el siguiente encargo.
- Un volcado de depósitos que agota sus intentos devuelve su delta a la ciudad **con una versión nueva**, para que
  el reintento supere a cualquier encargo escrito entre medias y no se pierda. El precio es el caso patológico de
  un `COMMIT` confirmado cuya confirmación se perdió y cuyos tres reintentos fallaron después: ese delta se
  sumaría dos veces. Exige una caída de la base justo después de confirmar; la alternativa, conservar la versión,
  perdería los depósitos en un caso mucho más común (un encargo escrito antes del reintento).

### Neutras

- Seis trabajos nuevos en el catálogo cerrado (`task.write`, `stocks.flush`, `city.build`, `city.train`,
  `building.complete`, `production.complete`) y sus series de `eo_persistence_jobs_dropped_total`.
- Comandos internos nuevos para las respuestas de la cola, en el catálogo de `simulation.CommandTypes`.

## Estado

Aceptado el 2026-10-27, con la implementación de SPEC-ECON-WOOD.
