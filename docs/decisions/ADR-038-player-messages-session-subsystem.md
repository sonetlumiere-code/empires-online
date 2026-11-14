# ADR-038: Los mensajes entre jugadores son un subsistema de sesión, se guardan antes de entregarse y se serializan por pareja

Propósito: fijar cómo el slice de mensajes ([SPEC-MESSAGES](../specs/messages.md)) guarda y entrega texto entre dos
jugadores sin tocar la simulación, cómo se garantiza que lo leído no esconde un mensaje y que un bloqueo no deja pasar
ninguno, y qué cambiará el día que haya más de un proceso.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-14** |
| Ámbito | `internal/websocket` (los cinco comandos, los límites de ritmo y la entrega), `internal/domain/message` (paquete nuevo: validación del texto, la vista y los límites), `internal/persistence/postgres` (`player_messages`, `message_reads`, `player_blocks`), `internal/game/simulation/actions.go` (los comandos como mensajes de sesión), `internal/observability` (las métricas), migración `000018`, `packages/protocol`, cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-004](ADR-004-redis-hot-state.md) (lo que vive en Redis), [ADR-014](ADR-014-command-idempotency.md) (la deduplicación por `requestId`), [ADR-018](ADR-018-identity-store-and-ticket-issuer.md) (la identidad fuera del Game Server), [ADR-021](ADR-021-game-server-perimeter.md) (el perímetro y el cubo común), [ADR-028](ADR-028-event-log-from-world-events.md) (el registro que atiende la conexión y el cursor asentado), [ADR-033](ADR-033-live-notices-ephemeral.md) (los avisos en vivo) |

---

## Contexto

Hasta E15 los jugadores no tienen ningún canal para hablar. Todo lo que cruza de un jugador a otro es estado del
mundo: tratados, tratos, caravanas, golpes. Ese estado lo decide el tick loop, de un solo escritor, y se persiste por
la cola ordenada por clave ([ADR-013](ADR-013-persistence-pipeline.md)); lo que los jugadores leen de la historia
(el registro de E7) lo atiende la **conexión**, fuera del loop, leyendo `world_events`
([ADR-028](ADR-028-event-log-from-world-events.md) §1), y el loop no se entera.

Piezas que ya existen y que el slice usa:

- `Hub.SendToPlayer` entrega un mensaje a todas las sesiones abiertas de un jugador en este proceso.
- Cada sesión atiende sus mensajes uno detrás de otro; una consulta del registro bloquea esa sesión como mucho
  `eventLogTimeout` (3 s) y no a las demás.
- Los comandos del mundo se deduplican por `requestId` en Redis ([ADR-014](ADR-014-command-idempotency.md)):
  reservar, ejecutar, guardar la respuesta.
- El cursor del registro no pasa de un tope «asentado», calculado con un margen de tiempo, porque los workers de la
  cola escriben en paralelo y un `id` menor puede confirmarse después que uno mayor
  ([INV-PERSIST-006](../invariants/persistence.md#inv-persist-006)).

SPEC-MESSAGES trae texto privado, escrito por personas, que tiene que llegar en vivo si el otro está y esperarle si
no, con un «leído» por conversación, un bloqueo y límites contra el spam.

## Decisión

**1. Los mensajes son un subsistema de sesión.** Los cinco comandos (`message.send`, `message.list`, `message.fetch`,
`message.read` y `message.block`) se suman a `sessionMessages` de `internal/game/simulation/actions.go`; no entran en
`CommandTypes`, ni en la cola de comandos, ni en `simulation.JobNames`. Los atiende el `Server` de
`internal/websocket` con un repositorio que se conecta como el del registro (`SetMessages`), y cada consulta tiene su
plazo, como `eventLogTimeout`. La simulación nunca lee un mensaje (`RN-MSG-009`): no hay nada que reproducir ni que
recuperar tras una caída, y el determinismo no cambia.

**2. Tres tablas, con la pareja en orden canónico** (migración `000018`):

- `player_messages`: `id` identidad, `sender_player_id`, `recipient_player_id`, `player_low` y `player_high` (la
  pareja ordenada, con un `CHECK` de que `player_low < player_high` y de que son el remitente y el destinatario),
  `body`, `request_id`, `sent_at`. Índice `(player_low, player_high, id DESC)` para leer una conversación;
  `UNIQUE (sender_player_id, request_id)`; `CHECK` de longitud de `body` en caracteres.
- `message_reads (player_id, other_player_id, read_up_to_id)`, PK `(player_id, other_player_id)`.
- `player_blocks (player_id, blocked_player_id, created_at)`, PK de los dos y `CHECK` de que son distintos.

Las FK van a `players` con `ON DELETE CASCADE`: un jugador que se borra se lleva sus conversaciones, como se lleva
hoy sus unidades. La conversación no es una fila: es el par `(player_low, player_high)`, como el par ordenado de los
tratados.

**3. Se guarda antes de entregarse, y los envíos de una pareja se serializan.** `message.send` abre una transacción
que, en este orden:

1. Toma un candado de transacción por la pareja (`pg_advisory_xact_lock` sobre un hash de `player_low` y
   `player_high`). Dos parejas que comparten hash se esperan entre sí sin más consecuencia.
2. Comprueba que ninguno de los dos tiene bloqueado al otro (`player_blocks`).
3. Mira si la pareja se había escrito antes (para el límite de conversaciones nuevas del punto 5).
4. Inserta el mensaje, y confirma.

Sólo después del `COMMIT` sale `message.new`, con el `id` de la base, a las sesiones de los dos.
`message.block` toma el mismo candado antes de escribir en `player_blocks`. De ahí salen dos garantías:

- **Dentro de una conversación los `id` se confirman en orden.** El `id` se asigna al insertar, con el candado ya
  tomado, y el candado no se suelta hasta el `COMMIT`. Todo mensaje de esa pareja con un `id` menor que uno ya servido
  está confirmado. El tope de `message.read` es el mayor `id` que esa sesión recibió de esa conversación (por página
  o por `message.new`), sin margen de asentado; la escritura es `GREATEST`, así que nunca retrocede
  ([INV-PERSIST-007](../invariants/persistence.md#inv-persist-007)).
- **Un bloqueo y un envío no se cruzan.** O el bloqueo confirma antes y el envío lo ve, o el envío confirma antes y
  el mensaje es anterior al bloqueo ([INV-SEC-011](../invariants/security.md#inv-sec-011)).

El orden canónico en la pareja también evita el abrazo mortal: los dos sentidos de una conversación toman el mismo
candado.

**4. La idempotencia la da la base, no Redis.** `message.send` no pasa por el `Deduper` de
[ADR-014](ADR-014-command-idempotency.md). El efecto es una fila, y `UNIQUE (sender_player_id, request_id)` lo
hace único en la misma transacción que lo crea. Un reintento con el mismo `requestId` encuentra la fila (`ON CONFLICT
DO NOTHING` y una lectura) y recibe el `message.new` del mensaje que ya existía. Con Redis caído, el envío sigue
siendo idempotente ([INV-SEC-007](../invariants/security.md#inv-sec-007)). `message.read` y `message.block` son
idempotentes por sí mismos (`GREATEST` y una PK); `message.list` y `message.fetch` sólo leen.

**5. Los límites de ritmo viven en la RAM del `Server`, por jugador.** Un mapa por `playerID` con dos ventanas
deslizantes: los envíos del último minuto (`EO_MESSAGE_RATE_PER_MINUTE`, 10) y las conversaciones nuevas de la última
hora (`EO_MESSAGE_NEW_RECIPIENTS_PER_HOUR`, 10), sumando todas las sesiones del jugador. El primero se mira antes de
abrir la transacción. El segundo se mira dentro, cuando el paso 3 dice que la pareja es nueva: si no quedan
conversaciones nuevas, la transacción se deshace sin insertar. Un envío rechazado o fallido no gasta nada. Superar un
límite responde `system.error { RATE_LIMITED }` **con el `requestId` y sin cerrar la conexión**: el cubo común de la
sesión ([ADR-021](ADR-021-game-server-perimeter.md)) sigue cerrando con `4429`, y el cliente distingue los dos casos
por el cierre. Los contadores se pierden al reiniciar: perderlos sólo deja escribir otros diez mensajes, y guardarlos
en Redis o en la base costaría una escritura por envío.

**6. La entrega en vivo es del proceso.** Tras el `COMMIT`, `Hub.SendToPlayer` al destinatario y al remitente. La
sesión que envió recibe su copia con el `requestId`, y es su confirmación. Si el destinatario no tiene sesión en el
proceso, no se manda nada: la bandeja se lee de la base al conectar y al volver a la pestaña. La entrega en vivo es
«como mucho una vez» y la bandeja, «siempre». Con un solo proceso de Game Server (`OUT-17`) es la entrega entera.
Con varios, este es el punto que cambia: un mensaje guardado en un proceso tendría que llegar a las sesiones de los
otros, por ejemplo publicándolo en Redis tras el `COMMIT`. El almacén no cambiaría.

**7. Privacidad por construcción.**

- Las consultas toman siempre el jugador de la sesión y la pareja que forma con `otherPlayerId`, así que ninguna
  devuelve mensajes de una pareja ajena ([INV-SEC-010](../invariants/security.md#inv-sec-010)). El remitente de un
  envío es siempre el jugador de la sesión ([INV-SEC-004](../invariants/security.md#inv-sec-004)).
- El texto no se escribe en los logs (sólo `id`, longitud y resultado), ni en las métricas, ni en `world_events`.
- Al otro jugador se le nombra por su ciudad: la vista resuelve `otherCityId` y `otherCityName` al leer, y ninguna
  consulta toca `players.username`.

**8. El texto se valida en los dos extremos y se pinta como texto.** El esquema Zod acota la longitud en bytes
(lo que cabe en `EO_WS_MAX_MESSAGE_BYTES`) y el dominio, en caracteres: recorta los extremos, exige de 1 a
`EO_MESSAGE_MAX_CHARS` y rechaza los caracteres de control salvo `\n`. El `CHECK` de la base repite la longitud. El
cliente lo pinta como nodo de texto de React, nunca con HTML, y no enlaza las URL.

## Alternativas descartadas

- **Pasar los mensajes por la cola de comandos y el tick.** No tocan el mundo; pasar por el loop gastaría presupuesto
  del tick en texto y metería en la simulación algo que no necesita determinismo ni recuperación.
- **Entrega optimista** (avisar en el acto y guardar después, como la llegada de una caravana). Un mensaje que el
  destinatario ya leyó y luego no existe es peor que uno que tarda unos milisegundos. Además, el `id` tendría que
  existir antes que la fila: el problema que obligó a los avisos de [ADR-033](ADR-033-live-notices-ephemeral.md) a ir
  sin `id`.
- **El cursor asentado del registro** (un tope con margen de tiempo, como
  [INV-PERSIST-006](../invariants/persistence.md#inv-persist-006)). Funcionaría, pero dejaría el último mensaje sin
  poder marcarse como leído durante el margen, y el contador seguiría encendido con la conversación abierta. El
  candado por pareja cuesta una espera entre dos envíos simultáneos de la misma conversación, que no es un caso
  frecuente.
- **«Leído» por mensaje** (una columna `read_at`). Una escritura por mensaje en lugar de una por conversación, y el
  mismo problema de orden en el contador.
- **Un servicio de chat aparte.** Otro proceso, otro despliegue y otra autenticación para mensajes de dos personas.
- **Atenderlos en Next.js**, que guarda la identidad. No tiene el WebSocket, así que no podría empujar nada en vivo.
- **Redis como almacén de la bandeja.** Lo prohíbe [INV-PERSIST-003](../invariants/persistence.md#inv-persist-003):
  ningún dato durable vive sólo en Redis.
- **Los mensajes en `world_events`.** Mezclaría texto privado con la auditoría del mundo, que leen el registro y los
  avisos.
- **El `Deduper` de Redis para `message.send`.** Sobra con la unicidad en la base, que no depende de Redis y es
  atómica con el efecto.
- **Cerrar la conexión al superar el límite de mensajes**, como el cubo común. Cortaría la partida por escribir
  deprisa.

## Consecuencias

### Positivas

- El loop no cambia: ningún golden, ninguna fase ni ningún trabajo de la cola se tocan.
- Un mensaje visto existe siempre, y lo leído no esconde ninguno, sin márgenes de tiempo.
- Bloquear es exacto: ningún mensaje se cuela por una carrera.
- Idempotente aunque Redis no esté.

### Negativas

- Cada envío es una transacción con candado desde la conexión. Si la base va lenta, esa sesión espera (hasta su
  plazo) y las demás no. Es el mismo coste que ya tiene el registro.
- Los límites de ritmo empiezan de cero tras un reinicio.
- La entrega en vivo es de un solo proceso; con varios habrá que repartirla (punto 6).
- Los mensajes viejos no se borran solos: la base crece hasta que la operación 24/7 añada el borrado.

### Neutras

- `RATE_LIMITED` se usa ya en dos sentidos: con cierre (el cubo común) y sin cierre ni pérdida de la sesión (los
  límites de los mensajes). El código es el mismo y el cierre los distingue; queda escrito como precisión en
  [websocket-protocol.md](../specs/websocket-protocol.md) §4.2.
- El catálogo del protocolo pasa a 29 mensajes cliente→servidor y 27 servidor→cliente.
