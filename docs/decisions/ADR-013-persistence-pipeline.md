# ADR-013: La simulación asigna la identidad de los movimientos y la cola de persistencia ordena por entidad

Propósito: fijar quién asigna el id de un movimiento, en qué orden se ejecutan las escrituras diferidas de
una misma entidad, qué se persiste de una unidad mientras se mueve y cómo se compensa una escritura que
falla de forma definitiva.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-10-20** |
| Ámbito | `internal/game/simulation`, `internal/persistence`, `internal/persistence/postgres`, `cmd/server` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-003](ADR-003-postgresql-source-of-truth.md), [ADR-007](ADR-007-game-loop-frequency.md), [ADR-011](ADR-011-movement-timed-polyline.md) |

---

## 1. Contexto

El tick nunca hace I/O contra PostgreSQL ([ADR-007](ADR-007-game-loop-frequency.md), canon §6). Las
escrituras durables que provoca la simulación se **encolan** y las ejecutan workers fuera del tick
(`internal/persistence.Queue`). Esa separación es correcta, pero la reconciliación de `EO-118` encontró
cuatro defectos que nacen del mismo sitio: la cola trataba cada trabajo como independiente.

1. **Identidad del movimiento.** `unit_movements.id` es `GENERATED ALWAYS AS IDENTITY`, así que lo asigna
   PostgreSQL al ejecutar el `INSERT` en el worker. Pero el loop lo necesita **al aceptar la orden**, para
   `unit.move.accepted` y `unit.movement.started`, y después para cerrar el movimiento. Con la cola real el
   loop leía `movementId: 0`, y `m.ID` se escribía y se leía desde goroutines distintas sin sincronizar: una
   carrera de datos que ningún test veía porque todos usan persistidores síncronos.
2. **Orden por entidad.** Cuatro workers sacan trabajos del mismo canal. El cierre de un movimiento podía
   ejecutarse antes que su inicio si éste se reintentaba, y un volcado viejo reintentado podía pisar uno más
   reciente.
3. **Posición en tránsito.** [SPEC-MOVEMENT](../specs/movement.md) marcaba la unidad como sucia en cada paso
   del trayecto, de modo que el volcado periódico escribía posiciones intermedias y `status = MOVING`.
   [INV-PERSIST-004](../invariants/persistence.md#inv-persist-004) decía lo contrario (`Q-18`). Y el cierre
   del movimiento no escribía la posición final: ésta llegaba con el siguiente volcado, hasta 5 s después.
   Una caída en esa ventana dejaba en disco el movimiento cerrado y la unidad a mitad de camino.
4. **Compensación.** `OnPermanentFailure` corre en la goroutine del worker, que no puede tocar el estado del
   mundo sin una carrera. Por eso ningún callback compensaba nada: el del inicio de movimiento sólo
   registraba el error y la unidad seguía moviéndose sin fila en `unit_movements`.

Restricciones: el despliegue del MVP es **un solo proceso** de Game Server, y es el único que inserta en
`unit_movements`. Una unidad en movimiento tiene su posición **derivable** de la polilínea en cualquier
instante ([ADR-011](ADR-011-movement-timed-polyline.md)). En un RTS hay en cada momento muchas más unidades
moviéndose que arrancando o terminando un movimiento.

---

## 2. Decisión

**La simulación asigna el id de cada movimiento; la cola ejecuta en orden los trabajos de una misma
entidad; la posición de una unidad sólo se escribe cuando no se está moviendo; y un fallo definitivo vuelve
al loop como comando.**

1. **Ids de movimiento.** `State` lleva un contador monótono. Al arrancar se inicializa con
   `max(unit_movements.id)`; cada movimiento nuevo toma el siguiente **en el loop**, antes de emitir nada. El
   `INSERT` usa `OVERRIDING SYSTEM VALUE` para escribir ese id. La secuencia de identidad de la columna deja
   de avanzar y no se usa: el Game Server es el único que inserta movimientos. Con varios procesos, este
   esquema se sustituiría por reservas de bloques (ver §3.2).
2. **Cola particionada por clave.** Cada `simulation.Job` declara una `Key` (por ejemplo `unit:42`,
   `city:7`). La cola tiene un canal por worker y envía cada trabajo al worker `hash(Key) mod N`: los de una
   misma clave se ejecutan en el orden en que se encolaron, con sus reintentos incluidos, y los de claves
   distintas en paralelo. El volcado periódico se parte en un lote por worker.
3. **Escritura de la unidad en las transiciones de movimiento.** El inicio de un movimiento escribe, en la
   misma transacción que cancela el anterior e inserta el nuevo, `units.status = MOVING` y la posición de
   origen. El cierre (`COMPLETED`, `CANCELLED` o `FAILED`) escribe, en la misma transacción que cierra la
   fila, la posición final, su chunk y `units.status = IDLE`. El volcado periódico **excluye** toda unidad
   con un movimiento activo, y el avance de un movimiento ya no marca la unidad como sucia. Así se cumple
   `INV-PERSIST-004` y se cierra `Q-18`.
4. **Compensación por comando.** `simulation.Deps` recibe un `Inbox` por el que los workers devuelven
   comandos al loop sin bloquear. Si el inicio de un movimiento falla de forma definitiva, el worker envía
   `MovementPersistFailed{UnitID, MovementID}`. El loop lo aplica en su fase de comandos: si ese movimiento
   sigue activo, se cancela con `reason = SERVER` y la unidad se detiene donde esté. Si el buzón está lleno,
   se registra y el estado se resuelve en el siguiente arranque.

Fuera de alcance: el orden **entre** claves distintas, que no hace falta; y la durabilidad de lo que todavía
está en la cola cuando el proceso cae, que sigue siendo el RPO documentado.

---

## 3. Alternativas consideradas

### 3.1 Que PostgreSQL siga asignando el id, y avisar al loop cuando llegue

El worker devolvería el id por el `Inbox` y el loop lo incorporaría al movimiento.

**Ventajas reales.** La columna conserva su identidad sin trucos, y el esquema no cambia de semántica.

**Descarte.** `unit.move.accepted` y `unit.movement.started` se emiten en el mismo tick en que se acepta la
orden, antes de que exista ningún `COMMIT`. Habría que retrasarlos hasta el commit, lo que añade latencia
perceptible a cada orden, o emitirlos sin id y mandar otro mensaje después, lo que amplía el protocolo con
un estado intermedio que el cliente tendría que reconciliar.

### 3.2 Reservar bloques de ids a PostgreSQL

El loop pediría por adelantado bloques de 1 000 ids (`nextval` de la secuencia) y los repartiría.

**Ventajas reales.** Funciona con varios procesos sin coordinación adicional, y es el esquema al que habrá
que ir cuando el mundo se reparta entre instancias.

**Descarte, por ahora.** La reserva es I/O, así que tiene que hacerse fuera del tick y con margen, y añade
un estado de «bloque agotándose» que hoy no aporta nada a un despliegue de un solo proceso. Queda como la
evolución prevista del punto 1.

### 3.3 Un solo worker

Con un worker, el orden global sale gratis.

**Ventajas reales.** Es la solución más simple posible y elimina toda la clase de errores de orden.

**Descarte.** Serializa todas las escrituras del mundo detrás de la latencia de PostgreSQL. Un reintento con
backoff de una entidad bloquearía a todas las demás. La partición da el mismo orden **donde importa** sin
ese cuello de botella.

### 3.4 Seguir volcando la posición en tránsito

Lo que prescribía SPEC-MOVEMENT.

**Ventajas reales.** Tras una caída, la unidad aparece cerca de donde iba aunque su movimiento se hubiera
perdido.

**Descarte.** Escribe cada 5 s una fila por unidad en movimiento, que en un RTS es la mayoría del tráfico de
volcado, para guardar un dato que la polilínea ya deriva exactamente. Y deja `status = MOVING` en disco sin
nada que lo respalde cuando el cierre llega antes que el volcado siguiente.

---

## 4. Consecuencias

### Positivas

- `unit.move.accepted` y `unit.movement.started` llevan el id real del movimiento desde el primer instante,
  y desaparece la carrera de datos sobre `m.ID`.
- El inicio y el cierre de un movimiento no pueden reordenarse, ni dos volcados de la misma unidad.
- Tras una caída, la posición de una unidad parada es exactamente la de su último cierre de movimiento: no
  hay ventana de 5 s para las llegadas.
- El volcado periódico deja de escribir unidades en movimiento.
- Un inicio de movimiento que no se pudo persistir se compensa en el mundo, en el hilo del loop.

### Negativas

- La columna `unit_movements.id` mantiene `GENERATED ALWAYS`, pero su secuencia queda desfasada. Cualquier
  `INSERT` manual sin `OVERRIDING SYSTEM VALUE` chocaría con ids ya usados. Se documenta en el esquema.
- Un segundo proceso que insertara movimientos rompería la unicidad de ids sin coordinación. El MVP es de un
  solo proceso; pasar a varios exige la alternativa 3.2.
- Una partición caliente —muchos trabajos de una misma clave— no reparte carga. Con claves por entidad, es
  improbable.
- `units.status` deja de escribirse por volcado para las transiciones de movimiento, y pasa a depender de
  que los trabajos de inicio y cierre lleguen a ejecutarse. Si el proceso cae con ellos en la cola, la
  rehidratación los reconcilia.

### Neutras

- `simulation.Job` gana el campo `Key`; `simulation.Deps` gana `Inbox`.
- `Repositories.PersistMovementStart` y `PersistMovementFinish` reciben también la posición y el estado de
  la unidad.

---

## 5. Verificación

- La cola real (no un persistidor síncrono) entrega `unit.move.accepted` con el id que acaba en
  `unit_movements`, y pasa con `-race`.
- Los trabajos de una misma clave se ejecutan en orden aunque el primero se reintente; los de claves
  distintas no se esperan.
- Tras completar o cancelar un movimiento, `units` tiene la posición final y `IDLE` sin que haya habido
  ningún volcado.
- El volcado periódico no escribe ninguna unidad con movimiento activo.
- Un inicio de movimiento que falla en todos sus intentos acaba con la unidad detenida y un
  `unit.movement.cancelled` con `reason = SERVER`.

## 6. Referencias

- [../invariants/persistence.md](../invariants/persistence.md), `INV-PERSIST-001`, `INV-PERSIST-004`.
- [../specs/movement.md](../specs/movement.md).
- [../roadmap/backlog.md](../roadmap/backlog.md), `EO-120` y `Q-18`.
