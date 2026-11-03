# ADR-026: El golpe de saqueo es un débito del stock en dos fases, y la carga robada no se vuelca hasta confirmarlo

Propósito: fijar cómo el slice de saqueo ([SPEC-COMBAT-PILLAGE](../specs/combat-pillage.md)) resta madera del
almacén de una ciudad para dársela a un guerrero sin que una caída del servidor pueda duplicarla.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-03** |
| Ámbito | `internal/game/simulation` (`combat.go`, `pillage.go`, fase 4), `internal/persistence/postgres` (`combat.go`), `packages/protocol` (`unit.pillage`, `pillagedAtMs`), cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md) (cola por clave), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (toda escritura declara compensación), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (stock por deltas, encargos en dos fases), [ADR-024](ADR-024-combat-simultaneous-resolution.md) (fase 4, botín en el volcado) |

---

## Contexto

[ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) §1 deja dos escritores del stock: el volcado,
que sólo suma depósitos, y el encargo, que resta en la transacción de su efecto. El saqueo es un **tercer
escritor que resta**, y su efecto —la madera en la carga del guerrero— vive en otra fila (`units.loot`) que se
escribe con otra clave de la cola (la de la unidad) y por otra vía (el volcado de unidades, ADR-024 §2).

Si cada golpe restara sólo en la RAM y dejara el stock al volcado, como un depósito al revés, el volcado de
unidades (que va antes que el de stocks) podría escribir la carga robada y caer el servidor antes del débito:
la víctima conservaría su madera y el guerrero tendría una copia. Eso viola
[INV-COMBAT-003](../invariants/combat.md#inv-combat-003), un combate (o un saqueo) nunca crea madera.

## Decisión

**1. Cada golpe de saqueo es una orden en dos fases**, como un encargo (ADR-023 §2). El tick, en la fase 4,
**reserva**: resta lo robado del stock de la víctima en la RAM y lo suma al guerrero como **carga pendiente**.
La transacción, encolada con la clave de la ciudad víctima (`city:<id>`), aplica el delta versionado
«depósitos sin volcar − robado» y escribe `world_events` `CityPillaged`. El resultado vuelve al loop por el buzón
(`pillage.resolved`):

- con éxito, la carga pendiente pasa a `Loot`, la carga confirmada;
- con un fallo definitivo, lo robado y los depósitos que viajaban con él vuelven al almacén de la víctima, y la
  carga pendiente desaparece.

**2. El volcado de unidades escribe sólo la carga confirmada.** La carga pendiente vive aparte en la RAM
(`combat.pendingLoot`), no en `unit.Loot`. Así, en disco, el débito de la víctima siempre llega antes que la
carga del guerrero: una caída entre ambos **pierde** lo robado, nunca lo duplica. Lo que ve el cliente
(`carrying`) es la suma de las dos.

**3. Idempotencia por versión.** La transacción no tiene fila propia con `requestId`: es idempotente porque el
delta va condicionado a la versión (`WHERE version < v`). Si al reintentar la base ya tiene esa versión o una
posterior, el golpe ya se escribió y el reintento termina con éxito sin escribir el evento otra vez. Es seguro
porque todas las escrituras del stock de una ciudad —volcado, encargos y saqueos— van por la misma clave de la
cola, que las ejecuta en orden: mientras un golpe se reintenta no puede haberse escrito ninguna posterior de
esa ciudad.

**4. Casos de borde.**

- **Depositar con carga pendiente:** el depósito lleva sólo la carga confirmada. Si la confirmación llega con
  el guerrero parado en su tile de depósito, se deposita en ese momento.
- **Morir con carga pendiente:** la pendiente no se hereda; el asesino gana sólo la confirmada (RN-COMBAT-010).
  Si la transacción después se confirma, esa madera se pierde; si falla, vuelve a la víctima.
- **Varios saqueadores en el mismo tick:** se aplican en orden de `id` del guerrero contra el stock que dejó el
  anterior (RN-COMBAT-022); cada uno es su propia transacción, en ese orden, por la misma clave.

**5. El aviso al saqueado no se persiste.** `pillagedAtMs` es un instante en la RAM de la ciudad que viaja en un
`city.update` dirigido sólo al dueño (`SendToPlayer`). Un reinicio lo pierde, y está bien: es un aviso, no estado
del mundo.

## Alternativas descartadas

- **Débito en el volcado de stocks, como un depósito negativo.** Más simple, pero el orden del volcado
  (unidades antes que stocks) es el que protege el depósito del botín, y aquí haría lo contrario: duplicaría.
  Invertir el orden rompería el depósito.
- **Débito y carga en la misma transacción.** Escribiría `units.loot` con la clave de la ciudad, desordenada
  respecto del volcado de la unidad (la objeción de ADR-024 §2 al botín en la transacción de la muerte): un
  volcado viejo de la unidad podría pisar la carga.
- **Escribir sólo al terminar el saqueo, con el total.** Menos transacciones, pero el guerrero llevaría durante
  segundos una carga que no está en ninguna parte: una caída a mitad la perdería sin rastro, y una muerte a
  mitad tendría que repartir algo que la base no conoce.

## Consecuencias

### Positivas

- `INV-COMBAT-003` se mantiene sin excepciones: lo robado puede perderse en una caída, nunca crearse.
- Reutiliza el patrón de ADR-023 (reserva, transacción por clave de ciudad, buzón) y su compensación: no hay
  un mecanismo nuevo de recuperación.
- Sin migración: `city_stocks`, `units.loot` y `world_events` ya existen.

### Negativas

- Una transacción por golpe: con un saqueador, una cada 1,5 s. Es del orden de un depósito por aldeano y no
  preocupa a esta escala; si preocupara, los golpes de un mismo tick y ciudad se pueden agrupar.
- Durante unos milisegundos el cliente ve una carga (`carrying`) que el guerrero no podría depositar todavía.

### Neutras

- `CityPillaged` queda en `world_events` por golpe, con el guerrero, la ciudad y la cantidad: es la auditoría
  del saqueo y la base de un futuro «qué pasó mientras no estabas».
