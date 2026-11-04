# ADR-032: Un trato aceptado es una orden en dos fases que escribe dos almacenes en una transacción, y la cola admite trabajos con dos claves

Propósito: fijar cómo el slice de comercio ([SPEC-TRADE](../specs/trade.md)) decide las ofertas, cómo mueve los
recursos de dos ciudades sin crear ni perder nada, y qué cambia en la cola de persistencia para poder hacerlo.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-04** |
| Ámbito | `internal/persistence` (`queue.go`), `internal/domain/diplomacy` (la escalera), `internal/domain/trade`, `internal/game/simulation` (`trade.go`, fase 5), `internal/persistence/postgres` (`trade.go`), migración `000013`, `packages/protocol` (`trade.*`, `tradeOffers`), `internal/domain/eventlog`, cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md) (la cola ordenada por clave; este ADR la amplía), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (toda escritura declara compensación), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (stock por deltas versionados, órdenes en dos fases), [ADR-026](ADR-026-pillage-two-phase-stock-debit.md) (idempotencia por versión), [ADR-027](ADR-027-hot-treaties.md) (la RAM decide las órdenes de los tratados), [ADR-028](ADR-028-event-log-from-world-events.md) (el registro), [ADR-030](ADR-030-stone-layer-and-era-advance.md) (costes en varios recursos) |

---

## Contexto

El stock de una ciudad se escribe por deltas versionados (`amount = amount + delta, version = v WHERE version < v`,
ADR-023 §1). Ese esquema sólo es seguro si todas las escrituras del stock de una ciudad se ejecutan en el orden en que
la simulación las decidió: la idempotencia por versión da por hecho que, mientras una escritura se reintenta, no se ha
escrito ninguna posterior de la misma ciudad (ADR-026 §3), y el débito de un encargo cuenta con que los depósitos que
la RAM ya había anotado estén en la base, o viajen con él (los «rechazos espurios» de ADR-023). La cola lo garantiza
mandando cada clave a una partición, un canal con un solo worker (`simulation.PartitionOf`, ADR-013 §2.2): todo lo que
escribe el almacén de la ciudad 7 va por `city:7` y sale en orden.

Un trato aceptado escribe **dos** almacenes, y en general las dos ciudades caen en particiones distintas. Si su
transacción fuera por la clave de una sola de ellas, podría adelantar o retrasarse respecto del volcado de la otra:
un volcado posterior de la ciudad B ya escrito haría que el delta del trato en B se diera por aplicado sin estarlo, y
un débito posterior de B podría encontrarse en la base sin la madera que el trato le acreditaba.

El roadmap preveía para Trade «bloqueos en Redis y concurrencia optimista sobre las filas afectadas». Con un solo
escritor que decide en la RAM, como los tratados (ADR-027), las órdenes no compiten entre sí: lo que hay que ordenar son
las escrituras, no las decisiones.

## Decisión

### 1. La RAM decide las ofertas, como los tratados

Al arrancar se cargan las ofertas `PROPOSED` con sus líneas. Cada orden se valida en el tick contra la RAM (el
tratado del par, la oferta, los almacenes) y **reserva el par**: una segunda orden sobre él, o una oferta nueva del
mismo par, se rechaza con `INVALID_TARGET` hasta que vuelve la transacción (`RN-TRADE-007`).

- `trade.propose`, `trade.respond` cuando rechaza, `trade.cancel` y la caducidad no tocan almacenes: van con la clave
  `trade:<a>:<b>` (el par canónico). Proponer inserta la oferta y sus líneas, idempotente por
  `(proposer_id, request_id)`; las demás son actualizaciones condicionadas a `status = 'PROPOSED'`, y un reintento que
  encuentra la fila ya en el estado de destino termina con éxito.
- Proponer **no reserva stock** (SPEC-TRADE Q2): comprueba que la ciudad de quien ofrece tiene lo que da en ese tick.

### 2. Aceptar es una orden en dos fases que mueve los dos almacenes

El tick valida (`RN-TRADE-003`: el tratado vigente y las dos partes cubiertas) y **reserva**, por cada par
`(ciudad, recurso)` que el trato toca:

- resta de la RAM lo que esa ciudad da (`Stock.Debit`, que sube la versión);
- en un recurso que la ciudad sólo recibe, sube la versión sin cambiar la cantidad;
- se lleva los depósitos sin volcar de ese recurso.

Cada par da un delta versionado, «depósitos − lo que da + lo que recibe». La transacción bloquea la oferta, comprueba
que sigue `PROPOSED` (o que ya está `ACCEPTED` con este `requestId`, un reintento: termina con éxito), aplica todos los
deltas —si alguno no se aplica (`ErrStaleStock`) o dejaría un stock negativo, revierte entera—, pasa la oferta a
`ACCEPTED` con `accepted_request_id` y `closed_at`, y escribe `TradeAccepted`. El resultado vuelve por el buzón:

- con éxito, la RAM **acredita** a cada ciudad lo que recibe y difunde `trade.update` a los dos y `city.update` con los
  almacenes al chunk de cada ciudad. Lo recibido no se puede gastar antes: nada se anuncia antes de existir;
- con un rechazo o un fallo definitivo, devuelve lo debitado y los depósitos que viajaban, y la oferta sigue
  `PROPOSED`.

Lo que sale de una ciudad y lo que entra en la otra salen de las mismas líneas de la oferta, en el mismo trabajo: un
trato no crea ni destruye recursos (`INV-TRADE-001`).

### 3. La cola admite trabajos con dos claves

`simulation.Job` gana un segundo campo de clave (`Key2`). Si las dos claves caen en la misma partición, el trabajo es
uno normal. Si no:

- `Submit` lo pone en **las dos** particiones o en ninguna: comprueba antes que caben en las dos, bajo un cerrojo que
  hace atómico el par de envíos. Si no cabe, se descarta entero y se llama a su compensación, como cualquier trabajo
  que no cabe (ADR-022).
- Cada worker, al llegar a su mitad, avisa y espera a que llegue el de la otra partición. Cuando han llegado los dos,
  el de la primera clave ejecuta el trabajo —con sus reintentos y su compensación de siempre— y el de la segunda espera
  a que termine. Después los dos siguen con su canal.

**No puede haber bloqueo cruzado.** Todos los `Submit` salen del loop de la simulación, y el cerrojo impide además que
dos productores intercalen sus envíos: cada par de particiones ve los trabajos de dos claves en el mismo orden
relativo. Si un worker espera en el trabajo T a otra partición, todo lo que esa partición tiene delante de T se encoló
antes que T y no espera a nada posterior a T, así que terminará y llegará a T. Un test cruza dos tratos sobre las
mismas dos ciudades en sentidos opuestos y comprueba que los dos terminan y que el orden con los volcados de cada
ciudad se respeta.

### 4. La caducidad es optimista, como la de los tratados

La fase 5, en el barrido de un segundo de los tratados y justo después de él, cierra en la RAM las ofertas cuyo
`expires_at` pasó y las de un par que ya no tiene un tratado vigente (`RN-TRADE-006`): las pasa a `EXPIRED`, las
difunde y encola la escritura (`trade.end`, clave del par), condicionada a `PROPOSED`. Si se pierde, su compensación
la apunta y el barrido siguiente la reencola. Una oferta con una orden en vuelo se mira en el barrido siguiente.

### 5. La escalera de tratados

`diplomacy.Type` gana un rango (`TRADE` 1, `NON_AGGRESSION` 2, `ALLIANCE` 3) y `TRADE` pasa a ser proponible. Con un
tratado vigente sin preaviso, sólo se puede proponer un tipo de rango mayor, y aceptarlo cierra el vigente en la
misma transacción: es la sustitución de un pacto por una alianza de ADR-027 §2, generalizada. `GivesPeaceAt` no
cambia (sólo el pacto y la alianza dan paz); `AllowsTradeAt(now)` dice si un tratado deja comerciar: `ACTIVE`, de
cualquier escalón, sin su fin alcanzado.

### 6. Esquema, migración `000013`

| Cambio | Para qué |
|---|---|
| `trade_offers (id, proposer_id, target_id, proposer_city_id, target_city_id, status, request_id, accepted_request_id, created_at, expires_at, closed_at)` | La oferta y su ciclo de vida. `status` con `CHECK` de `PROPOSED`, `ACCEPTED`, `REJECTED`, `WITHDRAWN` y `EXPIRED`; `proposer_id <> target_id`. |
| Único parcial `(proposer_id, request_id)` | Idempotencia de `trade.propose`. |
| Único parcial `(least(proposer_id, target_id), greatest(proposer_id, target_id)) WHERE status = 'PROPOSED'` | `INV-TRADE-003`: una oferta pendiente por par, en cualquier dirección. |
| `trade_offer_lines (offer_id, side, resource, amount)`, PK `(offer_id, resource)` | Lo que da y lo que pide. `side` en `GIVE` o `WANT`, `amount > 0`, el `CHECK` de recurso de `city_stocks`; la PK impide un recurso en los dos lados. |

`treaties` no cambia: admite `TRADE` desde `000001`. `world_events.event_type` no tiene `CHECK`.

### 7. Red y registro

- Las ofertas `PROPOSED` del jugador viajan en `world.snapshot` (`tradeOffers[]`), que construye la simulación, y cada
  cambio en `trade.update` a los dos jugadores y a nadie más, como los tratados (ADR-027 §5).
- Las cantidades del protocolo van de 1 a 1 000 000 por línea. Es una cota técnica, no una regla de juego: nadie tiene
  hoy un almacén que se le acerque, y la columna es `bigint`.
- Los cinco eventos (`TradeProposed`, `TradeAccepted`, `TradeRejected`, `TradeWithdrawn`, `TradeExpired`) llevan
  `player_id` de quien propuso y `counterpart_player_id` de quien recibió la oferta, con `atMs`, `byPlayerId`, el
  `offerId` y las líneas en el payload (ADR-028 §3). En el registro **no se agrupan**: cada trato es su propia entrada,
  porque lo que importa de él son sus cantidades.

## Alternativas descartadas

- **Dos transacciones, una por ciudad, con un estado intermedio durable** (una saga). Cada mitad iría por su clave y
  la cola no cambiaría. Pero una caída entre las dos deja a una ciudad pagada y a la otra sin pagar: el arranque
  tendría que encontrar los tratos a medias, terminarlos y rehacer en la RAM reservas que ya no existen. Es un
  mecanismo de recuperación nuevo para algo que una transacción hace sola.
- **Reservar el stock al proponer.** Las aceptaciones no fallarían nunca, pero el stock quedaría apartado durante
  horas, y el saqueo, los encargos y el suelo del saqueo tendrían que saber qué parte del almacén está comprometida
  (SPEC-TRADE Q2).
- **Una clave del par para el trato.** Ordenaría los tratos de dos jugadores entre sí, pero no con los volcados ni los
  encargos de cada ciudad, que es lo que hace falta.
- **Una partición única para todo lo que toca dos ciudades.** Mismo problema: serializa los tratos, no su orden con
  el resto de escrituras de cada almacén.
- **Bloqueos en Redis y concurrencia optimista** (lo que preveía el roadmap). Resuelven decisiones concurrentes, y aquí
  no las hay: decide un solo escritor. Seguiría haciendo falta ordenar las escrituras.

## Consecuencias

### Positivas

- `INV-ECON-001`, `INV-ECON-002` e `INV-TRADE-001` se cumplen en la base tras cada `COMMIT`: un trato se mueve entero
  o no se mueve, también con fallos inyectados y reintentos.
- No hay nada que recuperar al arrancar: un trato está escrito o no lo está, y la oferta lo dice.
- Reutiliza la reserva por recurso de ADR-030 §3 y la versión del stock de ADR-023 tal como están.

### Negativas

- Mientras un trabajo de dos claves espera, el worker de la partición que llegó antes no escribe nada más. Con tratos
  de vez en cuando no se nota; si un día fueran muchos, la espera se acotaría por particiones.
- `eo_persistence_queue_depth` cuenta dos veces un trabajo de dos claves (está en dos canales).
- El caso patológico de ADR-023 sigue ahí: un `COMMIT` confirmado cuya confirmación se pierde y cuyos tres reintentos
  fallan devuelve la reserva a la RAM aunque la base ya movió el trato. La RAM queda por delante de la base hasta el
  siguiente arranque, que la recarga de la base.

### Neutras

- Tres trabajos nuevos en el catálogo cerrado (`trade.order`, `trade.accept`, `trade.end`) y sus comandos de vuelta en
  `simulation.CommandTypes`.
- Un recurso nuevo toca también el `CHECK` de `trade_offer_lines`.
