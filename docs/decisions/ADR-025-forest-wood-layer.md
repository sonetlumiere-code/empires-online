# ADR-025: La madera del bosque es una capa aparte del terreno, escrita por deltas con la carga

Propósito: fijar cómo el slice de agotamiento del bosque ([SPEC-ECON-FOREST](../specs/economy-forest.md)) guarda
la madera que le queda a cada tile sin tocar el terreno, y cómo la escribe para que una carga nunca cree madera.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-03** |
| Ámbito | `internal/game/simulation` (`gather.go`, `forest.go`, fase 5), `internal/game/founding`, `internal/persistence/postgres`, migración `000008`, `packages/protocol` (`forests`, `forest.update`), cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md) (cola de persistencia por clave), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (toda escritura declara compensación), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (el stock por deltas) |

---

## Contexto

La spec hace finito el bosque: cada tile `FOREST` tiene 100 de madera, la carga la gasta y el tile vuelve a crecer
24 horas después de su última carga. Hay dos piezas del sistema que asumen que el terreno no cambia nunca:

- **La verificación del mundo** ([INV-WORLD-005](../invariants/world.md#inv-world-005)): el arranque regenera el
  terreno desde la semilla y lo compara byte a byte con `world_chunks`; si difiere, no arranca.
- **El envío del terreno**: cada sesión recibe el de un chunk una sola vez, porque no cambia
  ([../architecture/networking.md](../architecture/networking.md)).

Y hay una propiedad de la cola de persistencia que condiciona la escritura: los trabajos se ordenan **por unidad**
(ADR-013). Dos aldeanos que cargan del mismo tile escriben desde colas distintas, y sus transacciones pueden llegar
a la base en otro orden que el del tick.

## Decisión

### 1. Una capa dispersa, separada del terreno

El terreno no cambia: un tile agotado sigue siendo `FOREST`. La madera vive en una capa aparte, en la RAM de la
simulación y en la tabla `forest_wood (x, y, remaining, regrows_at_ms)`, **sólo para los tiles que alguien ha
empezado a talar**. Un tile sin fila está lleno. El mundo recién generado no ocupa nada, y la capa crece con lo que
los jugadores tocan, no con el tamaño del mapa.

### 2. La carga escribe un delta en la transacción de la tarea

La transición `WORKING → TO_DEPOSIT` ya es una transacción (la de la tarea, ADR-023). El descuento del tile va en
ella, como **delta**:

```sql
INSERT INTO forest_wood (x, y, remaining, regrows_at_ms) VALUES ($x, $y, 100 - $carga, $regrows)
ON CONFLICT (x, y) DO UPDATE SET
  remaining = CASE WHEN forest_wood.regrows_at_ms <= $instante THEN 100 ELSE forest_wood.remaining END - $carga,
  regrows_at_ms = GREATEST(forest_wood.regrows_at_ms, $regrows)
```

- **Delta y no valor absoluto**: dos cargas del mismo tile desde colas distintas conmutan. Escribir el valor
  absoluto de la RAM dejaría en la base el de la que llegara última, no el de la última carga.
- **La regeneración, perezosa**: la fila de un tile que ya volvió a crecer no se borra en caliente. La siguiente
  carga la ve vencida (comparando con el instante de **esa carga**, no con el reloj de la base) y parte de 100.
- **`GREATEST`** para que una carga anterior que llega tarde no adelante la regeneración.
- **Conservación**: lo que pierde la fila es exactamente lo que gana `unit_tasks.carrying` en la misma
  transacción (`INV-ECON-008`). `CHECK (remaining BETWEEN 0 AND 100)` impide la madera negativa (`INV-ECON-007`).

Si la transacción se descarta, la compensación de la tarea (`TaskPersistFailed`) ya existente termina la tarea y
pierde la carga. La RAM ya había descontado el tile y la base no: el bosque en la RAM queda por debajo del de la
base hasta el siguiente arranque. Se acepta: se pierde madera del mundo, nunca se crea.

### 3. La regeneración en la RAM, por barrido

La fase 5 recorre, una vez por segundo y en orden de `(y, x)`, los tiles cuyo instante de regeneración venció: los
saca de la capa y difunde `forest.update` con el tile lleno. Es el mismo muestreo que la caducidad de los tratados.
Al arrancar, las filas vencidas se ignoran (y se borran como limpieza): el tile está lleno.

### 4. El protocolo, aditivo

`world.snapshot` gana `forests` opcional (los tiles del área que no están llenos) y aparece `forest.update`.
Los dos son aditivos dentro de v1 (`R-VER-2`, `R-VER-3`): un cliente antiguo los ignora y dibuja el bosque lleno.

## Alternativas descartadas

| Alternativa | Por qué no |
|---|---|
| Cambiar el terreno del tile agotado (`FOREST → GRASSLAND`) | Rompe `INV-WORLD-005` y el envío del terreno una sola vez: obligaría a versionar el terreno por chunk, a reenviarlo y a cambiar la verificación de arranque. Además cambiaría el pathfinding en caliente, que hoy asume un grid fijo. |
| Una fila por cada tile `FOREST` del mundo, sembrada al arrancar | 50 216 filas en el mundo canónico, casi todas llenas para siempre. La capa dispersa sólo guarda lo que cambia. |
| Escribir el bosque en el volcado periódico (*dirty-flag*) | La carga ya viaja en una transacción; separar el descuento abre una ventana en la que una caída deja la madera en la mano del aldeano y también en el bosque. |
| Borrar la fila al regenerar, en caliente | Un trabajo más por tile y por día, que además compite en orden con las cargas de otras colas. La regeneración perezosa no necesita escribir nada. |

## Consecuencias

- Nace la migración `000008` con `forest_wood`.
- `RN-CITY-023` deja de mirar sólo el terreno: la fundación lee la capa para contar tiles con madera.
- La familia `INV-ECON` gana `INV-ECON-007` e `INV-ECON-008`.
- El cliente guarda la capa aparte del terreno, que sigue siendo el mismo de siempre.
