# Mensajes: los jugadores pueden hablar entre ellos

Propuesta para que dos jugadores puedan escribirse: mensajes directos, guardados, que llegan en vivo si el otro está
conectado y le esperan si no lo está.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-MESSAGES` |
| Estado | Accepted (2026-11-14). Propuesta en `Draft` y aceptada ese mismo día, con la recomendación de cada pregunta de §10. |
| Milestone | E16 Mensajes (decimosexto slice después de M7) |
| Canon | §12 (persistencia), §13 (protocolo), §14 (seguridad), §16 (errores) |
| Depende de | [player.md](player.md), [city.md](city.md), [diplomacy.md](diplomacy.md), [trade.md](trade.md), [event-log.md](event-log.md), [live-notices.md](live-notices.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md) (la identidad fuera del Game Server), [ADR-021](../decisions/ADR-021-game-server-perimeter.md) (el perímetro del Game Server), [ADR-028](../decisions/ADR-028-event-log-from-world-events.md) (los mensajes de sesión que atiende la conexión), [ADR-033](../decisions/ADR-033-live-notices-ephemeral.md) (los avisos en vivo). [ADR-038](../decisions/ADR-038-player-messages-session-subsystem.md) (los mensajes como subsistema de sesión fuera de la simulación, guardados antes de entregarse y serializados por pareja): ver §7. |
| Reemplaza a | Responde en parte a la pregunta `Q-16` del [backlog](../roadmap/backlog.md) («¿qué comunicación entre jugadores existirá y con qué moderación?»): los mensajes directos, sí; canales, clanes y moderación con moderadores, no. Precisa `OUT-09` de [mvp-scope.md](../product/mvp-scope.md): el chat avanzado sigue fuera. En [websocket-protocol.md](websocket-protocol.md) §4.2, que no hay ningún sub-límite por tipo de mensaje (Q6). |
| Invariantes | Cita [INV-SEC-002](../invariants/security.md#inv-sec-002), [INV-SEC-004](../invariants/security.md#inv-sec-004), [INV-SEC-005](../invariants/security.md#inv-sec-005), [INV-SEC-007](../invariants/security.md#inv-sec-007), [INV-PERSIST-003](../invariants/persistence.md#inv-persist-003) e [INV-PERSIST-005](../invariants/persistence.md#inv-persist-005); asigna [INV-SEC-010](../invariants/security.md#inv-sec-010), [INV-SEC-011](../invariants/security.md#inv-sec-011) e [INV-PERSIST-007](../invariants/persistence.md#inv-persist-007) (§8) |

## 1. Objetivo

Que la diplomacia y el comercio se puedan negociar. Hoy un jugador puede proponer un pacto, ofrecer un trato o
capturar una caravana, pero no decir «te propongo paz si dejas de saquearme», «¿me das 60 de vino por 200 de
piedra?» ni «tu caravana pasa por mi tierra». El backlog lo dejó escrito en `Q-16`: la diplomacia sin comunicación
es poco jugable. El pilar 4 de [game-pillars.md](../product/game-pillars.md) dice que la comunidad no aparece por
poner un chat, sino cuando los jugadores se necesitan; desde E14 se necesitan (Hierro pide bienes ajenos), y ahora
tienen que poder pedírselo.

## 2. Scope

**Entra.** Mensajes de texto entre **dos** jugadores, guardados en PostgreSQL; una conversación por pareja; la entrega
en vivo a todas las sesiones del destinatario y del remitente; los no leídos que esperan a quien no estaba; la
bandeja con las conversaciones y su historial paginado; lo leído, por conversación; **bloquear** y desbloquear a un
jugador; límites de ritmo propios contra el spam; en el cliente, el botón «Mensajes» con su contador, la bandeja, la
conversación, «Escribir» allí donde aparece otra ciudad (la barra de diplomacia, los tratados, los tratos, el
registro, los avisos, «Caravanas en camino») y una línea en «Mientras no estabas»; `scripts/smoke.mjs --mensajes`.

**No entra** (`Fuera de MVP` de este slice): canales de grupo, de región, de facción o globales; clanes (`OUT-13`);
denunciar un mensaje, moderadores, filtros de palabras y sanciones (Q5); editar o borrar un mensaje enviado (Q3);
adjuntos, enlaces activos, formato o emojis propios; un directorio o una búsqueda de jugadores por nombre (Q1);
adjuntar a un mensaje una oferta de trato o de tratado (las ofertas siguen siendo las de E6 y E10); avisos por correo
o notificaciones del sistema operativo; «visto» para el remitente (Q8); el borrado definitivo de los mensajes viejos,
que va con la operación 24/7 (Q4); repartir la entrega entre varios procesos del Game Server (`OUT-17`).

## 3. Actores

El jugador que escribe, el que recibe (conectado o no), cliente web, Game Server (la conexión de cada sesión valida,
guarda y entrega; **el tick loop no participa**), PostgreSQL. Redis no participa.

## 4. Modelo

### 4.1 El mensaje y la conversación

Un mensaje es `{ id, remitente, destinatario, texto, enviado }`. El `id` lo asigna la base al guardarlo
(`bigint GENERATED ALWAYS AS IDENTITY`), así que existe antes de que nadie lo vea: no aparece el problema que obligó a
los avisos de E11 a ir sin `id` ([ADR-033](../decisions/ADR-033-live-notices-ephemeral.md)).

La **conversación** es el par de jugadores, sin orden: todo lo que A escribió a B y B a A, por `id`. No es una fila
propia; se deriva de los mensajes. A cada jugador se le nombra por **su ciudad** (`otherCityId`, `otherCityName`),
como en el registro (Q6 de E7) y en la diplomacia: el nombre de usuario es la credencial de entrada y no sale nunca del
almacén de identidad (Q2).

El texto es texto plano: entre 1 y `EO_MESSAGE_MAX_CHARS` caracteres (500, Q3) después de recortar los espacios de
los extremos, con saltos de línea y sin otros caracteres de control. El cliente lo pinta como texto, nunca como HTML,
y no convierte los enlaces en enlaces.

### 4.2 Enviar

`message.send { toPlayerId, body }` lo atiende la **conexión** de quien escribe, como `event_log.fetch`
([ADR-028](../decisions/ADR-028-event-log-from-world-events.md) §1): no entra en la cola de comandos ni en el tick, no
crea trabajos de la cola de persistencia y no toca el estado del mundo. En orden:

1. El esquema (texto vacío, demasiado largo o con caracteres de control: `INVALID_MESSAGE`).
2. El destinatario existe y no es el propio jugador (`INVALID_TARGET`).
3. Ninguno de los dos tiene bloqueado al otro (`INVALID_TARGET`, con el motivo; Q5).
4. Los límites de ritmo del remitente (`RATE_LIMITED`, sin cerrar la conexión; Q6).
5. Se guarda en una transacción propia (write-through). Si PostgreSQL falla o no responde a tiempo, `INTERNAL_ERROR`
   y el mensaje no existe.
6. Tras el `COMMIT`, `message.new` a todas las sesiones del destinatario y del remitente (la que lo envió lo recibe
   con su `requestId`, que es la confirmación). Si el destinatario no está conectado, no se le manda nada: lo verá al
   abrir la bandeja.

Un `requestId` repetido no guarda un segundo mensaje: la base tiene la pareja `(remitente, request_id)` única, y el
reintento recibe el `message.new` del mensaje que ya existía ([INV-SEC-007](../invariants/security.md#inv-sec-007)).

La entrega es «como mucho una vez en vivo, siempre en la bandeja»: si la sesión del destinatario se cae justo después
del `COMMIT`, el mensaje no se pierde, porque la bandeja se lee de la base al conectar.

### 4.3 Leer

- `message.list {}` → `message.inbox`: las conversaciones del jugador, de la más reciente a la más antigua, cada una
  con el otro jugador (su ciudad), el último mensaje recortado, su hora y cuántos no leídos tiene; y la lista de
  jugadores que tiene bloqueados.
- `message.fetch { otherPlayerId, beforeId? }` → `message.page`: la página más reciente de esa conversación, o la de
  los mensajes con `id` menor que `beforeId`, de los últimos `EO_MESSAGE_MAX_DAYS` días (Q4), con
  `EO_MESSAGE_PAGE_SIZE` mensajes (50) y el `beforeId` de la página siguiente.
- `message.read { otherPlayerId, upToId }` marca leída la conversación hasta `upToId`, con el mismo tope que el
  cursor del registro (`RN-LOG-004`): nunca más allá del último mensaje que esa sesión sirvió de esa conversación,
  por página o por `message.new`. No responde nada. A diferencia del registro, no hace falta un margen de asentado:
  los envíos de una pareja se serializan y sus `id` se confirman en orden
  ([ADR-038](../decisions/ADR-038-player-messages-session-subsystem.md) §3).

El cliente pide la bandeja al conectar y al volver a la pestaña tras un rato fuera, como la página del registro
(`RN-LOG-007`). Ninguna lectura frena el tick: si la base no responde, `INTERNAL_ERROR` y la partida sigue.

### 4.4 Bloquear

`message.block { playerId, blocked }` bloquea o desbloquea a un jugador y responde con la bandeja (`message.inbox`).
Mientras A tiene bloqueado a B, ni B puede escribir a A ni A a B (`INVALID_TARGET`, con el motivo: «no acepta tus
mensajes» o «lo tienes bloqueado»). La conversación anterior sigue en la bandeja de los dos, marcada como bloqueada en
la de quien bloqueó. Bloquear no toca la diplomacia, el comercio ni el combate: es sólo para los mensajes.

### 4.5 Reglas

Dominio: `RN-MSG`.

| ID | Regla |
|---|---|
| `RN-MSG-001` | El remitente de un mensaje es siempre el jugador de la sesión que lo envía; ningún campo del comando lo elige. |
| `RN-MSG-002` | Un mensaje va de un jugador a otro jugador existente y distinto; no hace falta ningún tratado, ni estar conectado, ni haberse visto en el mapa (Q1). |
| `RN-MSG-003` | El texto es texto plano de 1 a `EO_MESSAGE_MAX_CHARS` caracteres tras recortar los extremos, sin caracteres de control salvo el salto de línea; se guarda tal como llega, sin filtrar palabras. |
| `RN-MSG-004` | Mientras uno de los dos tiene bloqueado al otro, no se guarda ni se entrega ningún mensaje entre ellos, en ningún sentido (Q5). |
| `RN-MSG-005` | **Ritmo.** Cada jugador, sumando todas sus sesiones, envía como mucho `EO_MESSAGE_RATE_PER_MINUTE` mensajes por minuto y abre como mucho `EO_MESSAGE_NEW_RECIPIENTS_PER_HOUR` conversaciones nuevas por hora (una conversación es nueva si los dos no se habían escrito nunca). Por encima, `RATE_LIMITED` con el `requestId` y la conexión sigue abierta (Q6). Los contadores viven en la RAM del proceso y empiezan de cero al reiniciar. |
| `RN-MSG-006` | **Entrega.** Un mensaje existe si y sólo si su transacción confirmó; sólo entonces sale `message.new`, a todas las sesiones de los dos jugadores. Un `requestId` repetido no crea un segundo mensaje. |
| `RN-MSG-007` | **Privacidad.** Una sesión sólo lee conversaciones en las que su jugador es parte; el texto no va a los logs, a las métricas ni a `world_events`. |
| `RN-MSG-008` | **Lo leído** avanza por conversación, sólo hacia delante y nunca más allá de lo servido a esa sesión (§4.3). El remitente no sabe si su mensaje se leyó (Q8). |
| `RN-MSG-009` | Los mensajes no pasan por el tick loop ni lo frenan, y la simulación nunca los lee: la partida es la misma con mensajes o sin ellos. |
| `RN-MSG-010` | Al otro jugador se le nombra por su ciudad; el nombre de usuario no sale en ningún mensaje del protocolo (Q2). |

### 4.6 Qué ve el jugador

- En el HUD, un botón «Mensajes» con el número de no leídos. Abre la bandeja; un clic en una conversación abre su
  historial (los más recientes abajo, «Ver anteriores» arriba), con un cuadro de texto, el contador de caracteres y
  «Enviar». Abrir la conversación la marca leída.
- «Escribir» allí donde aparece otra ciudad: en la barra que sale al hacer clic en una ciudad ajena (junto a
  «Proponer…» y «Ofrecer trato»), en los tratados y los tratos pendientes, en las entradas del registro, en los avisos
  en vivo y en «Caravanas en camino».
- Un mensaje que llega con la conversación cerrada suma al contador, muestra un aviso breve («Villa Norte te ha
  escrito») y, con la pestaña en segundo plano, cambia el título como los avisos de E11 (Q7).
- En «Mientras no estabas», una línea «Tienes N mensajes sin leer de Villa Norte y 2 más» que abre la bandeja. Los
  mensajes no entran en el registro (Q7).
- En la conversación, «Bloquear» (y «Desbloquear» en la bandeja); un rechazo del servidor sale en el cuadro de texto
  con su motivo, sin perder lo escrito.

## 5. Errores

Sólo códigos que ya existen en el catálogo (canon §16).

| Código | Condición |
|---|---|
| `INVALID_MESSAGE` | Texto vacío tras recortar, más largo que el máximo o con caracteres de control; un `toPlayerId`, `otherPlayerId` o `playerId` que no es un UUID; `beforeId` o `upToId` que no son enteros positivos. |
| `INVALID_TARGET` | Destinatario inexistente o el propio jugador; escribir a quien te bloqueó o a quien tienes bloqueado (el mensaje dice cuál, `RN-MSG-004`); bloquearse a uno mismo o a un jugador inexistente. |
| `RATE_LIMITED` | Los límites propios de `RN-MSG-005`, como `system.error` con el `requestId` y **sin cerrar la conexión**. El cubo común de la sesión (§4.2 del protocolo) sigue igual: ése sí cierra con `4429`. |
| `INTERNAL_ERROR` | PostgreSQL no responde a tiempo al guardar o al leer (`RN-MSG-009`). |

## 6. Eventos y contratos de red

Cliente → servidor, cinco comandos nuevos y estrictos (R-VER-3), todos **de sesión**: se suman a los mensajes de sesión
de `internal/game/simulation/actions.go`, no a `CommandTypes`, y no crean trabajos de la cola. El catálogo pasa de
24 a 29.

- `message.send { toPlayerId, body }`
- `message.list {}`
- `message.fetch { otherPlayerId, beforeId? }`
- `message.read { otherPlayerId, upToId }`
- `message.block { playerId, blocked }`

Servidor → cliente, tres tipos nuevos (R-VER-3), cada uno con su e2e en el mismo commit (el `TestMain` de
`schema_e2e_test.go`). El catálogo pasa de 24 a 27.

- `message.new { message }`, a todas las sesiones de los dos jugadores; la del remitente que lo envió, con su
  `requestId`. La vista: `{ id, fromPlayerId, toPlayerId, otherPlayerId, otherCityId, otherCityName, body, sentAtMs }`,
  donde «otro» es siempre el jugador que no es el de la sesión.
- `message.inbox { conversations[], blocked[] }`, sólo a la sesión que lo pidió. Cada conversación:
  `{ otherPlayerId, otherCityId, otherCityName, lastBody, lastFromMe, lastAtMs, unread, blockedByMe }`, con `lastBody`
  recortado a 80 caracteres. Cada bloqueado: `{ playerId, cityId, cityName }`.
- `message.page { otherPlayerId, messages[], readUpToId, nextBeforeId }`, sólo a la sesión que lo pidió.

El esquema Zod lo fija la implementación. Eventos de dominio: **ninguno**. Un mensaje no es un suceso del mundo:
no va a `world_events` (que es la auditoría del mundo y lo leen el registro y los avisos), ni al registro, ni a
`event_log.notice`. Bloquear tampoco.

Métricas: `eo_messages_sent_total` y `eo_messages_rejected_total{reason}` (con `reason` en un catálogo cerrado:
`invalid`, `target`, `blocked`, `rate`, `internal`). Sin el texto ni los jugadores en las etiquetas.

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | Nada del mundo. Sólo los contadores de ritmo de `RN-MSG-005`, que no son estado de juego y se pierden al reiniciar |
| Write-through | El mensaje, en su transacción desde la conexión, antes de entregarlo; lo leído y los bloqueos, cada uno en su escritura desde la conexión |
| Dirty-flag | Nada |
| Reconstruible / no persistido | La bandeja, los no leídos y las páginas, que se derivan al leer |

**Por qué hace falta un ADR ([ADR-038](../decisions/ADR-038-player-messages-session-subsystem.md)).** Tres decisiones estructurales, más el candado por pareja que sostiene lo leído y el bloqueo (ADR-038 §3):

1. **Los mensajes son un subsistema de sesión, fuera de la simulación.** Los atiende la conexión, como el registro de
   E7, y el loop no los ve (`RN-MSG-009`). Pasarlos por la cola de comandos y el tick no aporta nada (no tocan el
   mundo ni el determinismo) y gastaría presupuesto del tick en texto.
2. **Se escriben antes de entregarse.** El mensaje se entrega tras el `COMMIT`, con su `id` de la base, al revés que
   las escrituras optimistas de la simulación: un mensaje que el destinatario vio y luego no existe sería peor que uno
   que tarda unos milisegundos.
3. **La entrega en vivo es del proceso.** `Hub.SendToPlayer` alcanza las sesiones de este proceso, que hoy son todas
   (`OUT-17`). Con varios procesos haría falta repartirla (por ejemplo, con publicación en Redis); el ADR lo deja
   escrito como el punto que cambiará, sin hacerlo ahora.

Alternativas descartadas que el ADR deja escritas, entre otras: **un servicio de chat aparte** (otro proceso, otro despliegue y
otra autenticación para mensajes de dos personas); **atenderlos en Next.js**, que guarda la identidad pero no tiene el
WebSocket y no podría empujar nada; **Redis como almacén** de la bandeja, que contradice
[INV-PERSIST-003](../invariants/persistence.md#inv-persist-003) (ningún dato durable sólo en Redis); **mensajes en
`world_events`**, que mezclaría texto privado con la auditoría del mundo.

**Migración `000018`.** `player_messages` (`id` identidad, `sender_player_id` y `recipient_player_id` con FK a
`players`, la pareja en orden canónico en `player_low` y `player_high` para leer una conversación por un índice
`(player_low, player_high, id DESC)`, `body text` con un `CHECK` de longitud, `request_id uuid` con
`UNIQUE (sender_player_id, request_id)`, `sent_at`, y un `CHECK` de que remitente y destinatario son distintos);
`message_reads (player_id, other_player_id, read_up_to_id)`, con PK de los dos jugadores; y `player_blocks (player_id,
blocked_player_id, created_at)`, con PK de los dos y el mismo `CHECK`. Las tres tablas se suman al `TRUNCATE` de
`testenv_integration_test.go`. Ningún trabajo nuevo en `simulation.JobNames`. La siguiente migración libre pasará a
ser `000019`.

## 8. Invariantes

**Cita** [INV-SEC-002](../invariants/security.md#inv-sec-002) (ningún mensaje antes de autenticarse),
[INV-SEC-004](../invariants/security.md#inv-sec-004) (el remitente es el jugador de la sesión, `RN-MSG-001`),
[INV-SEC-005](../invariants/security.md#inv-sec-005) (los límites del mensaje entrante),
[INV-SEC-007](../invariants/security.md#inv-sec-007) (un `requestId` repetido no guarda otro mensaje) e
[INV-PERSIST-005](../invariants/persistence.md#inv-persist-005) (la migración `000018`).

**Asigna** tres invariantes nuevos, registrados en [../invariants/security.md](../invariants/security.md) y
[../invariants/persistence.md](../invariants/persistence.md):

- [INV-SEC-010](../invariants/security.md#inv-sec-010) — una sesión sólo recibe mensajes de conversaciones en las que
  su jugador es remitente o destinatario, y todo mensaje lleva como remitente al jugador de la sesión que lo envió.
- [INV-SEC-011](../invariants/security.md#inv-sec-011) — ningún mensaje entre dos jugadores se confirma mientras uno
  de los dos tiene bloqueado al otro.
- [INV-PERSIST-007](../invariants/persistence.md#inv-persist-007) — lo leído de una conversación nunca retrocede ni
  pasa por encima de un mensaje que no se le había servido a esa sesión (el mismo caso que
  [INV-PERSIST-006](../invariants/persistence.md#inv-persist-006): dos envíos que confirman en orden inverso al de sus
  `id`).

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | La validación del texto (recorte, longitud en caracteres y no en bytes, saltos de línea, caracteres de control); cada fila de §5; los límites de ritmo (por minuto, destinatarios nuevos, suma de sesiones); el tope de lo leído; la vista con «el otro» desde cada lado |
| integration | Guardar y leer una conversación por el índice; la unicidad de `(remitente, request_id)`; el `CHECK` de longitud y de remitente distinto; bloquear y desbloquear; dos envíos que confirman en orden inverso y lo leído que no se salta el primero; la bandeja con no leídos de varias conversaciones; un fallo inyectado de la base que no deja mensaje |
| contract / e2e | Los cinco comandos y los tres tipos contra el JSON Schema; un envío de punta a punta por WebSocket con el destinatario conectado en dos sesiones; el destinatario desconectado que, al conectar, ve el no leído, lo lee y no lo vuelve a ver como no leído; el bloqueo en los dos sentidos; `RATE_LIMITED` sin cierre; un `requestId` repetido |
| simulation | Ninguno nuevo: el loop no cambia. Un test comprueba que los cinco comandos son de sesión y no llegan a la simulación |
| cliente | La bandeja, la conversación con paginación, el contador, «Escribir» desde la barra de diplomacia, el aviso y la línea de «Mientras no estabas», el texto pintado como texto (un `<b>` llega como `<b>`); `scripts/smoke.mjs --mensajes` (smoke_runner escribe a smoke_victim conectado y lo recibe en vivo; smoke_victim desconectado lo ve al entrar; smoke_victim bloquea a smoke_runner y el envío se rechaza; desbloquea) |

**Precisiones (2026-11-14), al implementar.** Ninguna cambia una decisión de §10; las que difieren del texto de
[ADR-038](../decisions/ADR-038-player-messages-session-subsystem.md) se anotan aquí, porque un ADR aceptado no se edita.

- **El límite del texto en el esquema.** ADR-038 §8 dice que el esquema acota el texto «en bytes». Zod cuenta unidades
  UTF-16, no bytes: el esquema admite de 1 a `2 × MESSAGE_MAX_CHARS` (1 000) unidades, para que quepan 500 caracteres
  de dos unidades, y el dominio (`message.NormalizeBody`) cuenta puntos de código. Un test de contrato ata los dos
  números. Además de los caracteres de control, el dominio rechaza los de dirección del texto (U+202A–U+202E,
  U+2066–U+2069 y similares: un texto que se lee distinto de lo que dice) y convierte `\r\n` en `\n`.
- **Un reintento no choca con el ritmo.** Si el remitente agotó su límite por minuto, `message.send` mira antes si ese
  `requestId` ya tiene mensaje (`MessageRepo.Existing`) y, si lo tiene, responde con él: la idempotencia de
  [INV-SEC-007](../invariants/security.md#inv-sec-007) gana al límite, que sólo frena lo nuevo.
- **Lo que llevan las respuestas.** `message.page` lleva además la ciudad del otro y `blockedByMe` (si quien mira lo
  tiene bloqueado; que el otro le bloqueó a él sólo lo sabe al escribirle, Q5). La bandeja trae como mucho las 200
  conversaciones más recientes, y una conversación sin ningún mensaje de los últimos 30 días no sale en ella.
- **«Mientras no estabas».** La ventana se abre también cuando sólo hay mensajes sin leer: la línea sale de la
  primera bandeja de la conexión y se borra con «Entendido», con «Leer» o al abrir la bandeja.
- **Los e2e.** Como en el registro, los e2e del Game Server corren con un almacén en memoria: prueban la conexión
  (quién recibe qué, el tope de lo leído, los límites, los errores y que el texto no sale en los logs). El candado
  por pareja, la idempotencia y lo leído contra PostgreSQL son de la integración, y el recorrido entero contra el
  servidor vivo es `scripts/smoke.mjs --mensajes`.

## 10. Decisiones de producto (2026-11-14)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de producto,
no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿A quién se puede escribir? | **A cualquier jugador cuya ciudad conozcas, sin tratado.** Se escribe desde donde aparece su ciudad: el mapa, los tratados, los tratos, el registro, los avisos y las caravanas. Hablar es justo lo que hace falta *antes* de un pacto, y el que te saquea es a quien más le quieres escribir. Alternativas: **sólo con un tratado** (no podrías negociar el primero); **cualquiera por nombre**, con un buscador o un directorio de jugadores, que facilita el spam masivo y no hace falta mientras el mundo se descubre caminando. |
| Q2 | ¿Cómo se nombra al otro jugador? | **Por el nombre de su ciudad**, como en el registro y la diplomacia. El nombre de usuario es la mitad de la credencial de entrada: mostrarlo regalaría a cualquiera la mitad de un login. Si un día los jugadores tienen un nombre público aparte, se usará ése. |
| Q3 | ¿Qué forma tiene un mensaje? | **Texto plano de hasta 500 caracteres, entre dos jugadores, sin editar ni borrar.** 500 caben en una propuesta con condiciones y no en un muro. No editar ni borrar mantiene la prueba si alguien acosa y ahorra estados. Sin grupos ni canales: son el chat avanzado de `OUT-09` y piden moderación de verdad. |
| Q4 | ¿Cuánto se conservan? | **Se muestran los últimos 30 días** (`EO_MESSAGE_MAX_DAYS`), paginados de 50 en 50. La base los guarda más tiempo; el borrado definitivo de los viejos va con la operación 24/7, que sigue aparcada. Alternativa: **7 días, como el registro**, que se queda corto para una negociación que dura semanas. |
| Q5 | ¿Qué moderación hay? | **Sólo bloquear, y quien escribe a quien le bloqueó lo sabe** («no acepta tus mensajes»). Sin moderadores no hay a quién mandar una denuncia, y un filtro de palabras se esquiva y falla con lo que no debe. Que el bloqueado lo sepa es honesto y no deja mensajes guardados que nadie leerá. Alternativa: **bloqueo silencioso** (el bloqueado cree que envía): no le da señal para insistir con otra cuenta, pero engaña y guarda texto que no se entrega. |
| Q6 | ¿Qué límites contra el spam? | **10 mensajes por minuto y 10 conversaciones nuevas por hora por jugador.** Una conversación entre dos personas no llega a 10 por minuto; mandar el mismo texto a 50 jugadores sí choca con el segundo límite. El rechazo no corta la conexión, al revés que el cubo común del protocolo: cortar la partida por escribir rápido sería un castigo desproporcionado. Son números de partida, sin datos. |
| Q7 | ¿Cómo te enteras de un mensaje? | **En vivo:** el contador del botón «Mensajes», un aviso breve en el HUD y el título de la pestaña, como los avisos de E11. **Al volver:** una línea en «Mientras no estabas» con cuántos y de quién, que abre la bandeja. Los mensajes **no entran en el registro**, que cuenta sucesos del mundo (`RN-LOG-002`); mezclarlos llenaría el registro de conversación. |
| Q8 | ¿Ve el remitente si se leyó? | **No.** Un «visto» presiona a contestar y dice cuándo está conectado alguien, que hoy no se ve. Lo leído sólo sirve al lector para su contador. |
| Q9 | ¿Afecta bloquear a algo más que a los mensajes? | **No.** Bloquear no rompe tratados ni cancela tratos ni impide atacar ni ser atacado: separa el canal de la partida. Si bloquear impidiera, por ejemplo, recibir ofertas, sería una protección contra la diplomacia ajena que nadie ha pedido. |

## 11. Documentos relacionados

- [event-log.md](event-log.md) (los mensajes de sesión, el cursor de lo visto y «Mientras no estabas») y
  [live-notices.md](live-notices.md) (el aviso en el HUD y el título de la pestaña).
- [diplomacy.md](diplomacy.md) y [trade.md](trade.md) (lo que se negocia y la barra desde la que se escribe).
- [websocket-protocol.md](websocket-protocol.md) (el catálogo de mensajes y el cubo común de §4.2).
- [../invariants/security.md](../invariants/security.md) y [../invariants/persistence.md](../invariants/persistence.md).
- [../roadmap/backlog.md](../roadmap/backlog.md) (`Q-16`) y [../product/mvp-scope.md](../product/mvp-scope.md)
  (`OUT-09`).
