# Registro: lo que pasó mientras no estabas

Que el jugador se entere, al volver, de lo que otros le hicieron y de lo que sus unidades hicieron
solas: muertes, saqueos y cambios en sus tratados. Hoy todo eso queda escrito en `world_events`, pero el cliente
no lo lee nunca, y quien no estaba mirando no se entera.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-EVENT-LOG` |
| Estado | Implemented (2026-11-03). Aceptada el 2026-11-03 con las preguntas de §10 decididas ese día con la recomendación de cada una; en `main` desde la fusión `a0e2096`, con la CI en verde. |
| Milestone | E7 Registro (séptimo slice después de M7) |
| Canon | §12 (persistencia), §16 (errores) |
| Depende de | [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md), [diplomacy.md](diplomacy.md), [presence.md](presence.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-013](../decisions/ADR-013-persistence-pipeline.md) (la cola escribe los sucesos con su efecto), [ADR-022](../decisions/ADR-022-durable-write-retry-and-loop-inbox.md) (reintentos), [ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md) §3 (tope de la protección). [ADR-028](../decisions/ADR-028-event-log-from-world-events.md) (la conexión lee `world_events`; `counterpart_player_id`; agrupación al leer; página por filas; lo visto sólo cubre lo asentado; migración `000010`). |
| Reemplaza a | — |
| Invariantes | Cita los existentes de §8 y asigna `INV-SEC-009` e `INV-PERSIST-006` |

## 1. Objetivo

Que el riesgo de E2 a E6 se vea. Desde E5 y E6 un jugador pierde unidades y madera, y sus tratados cambian, sin
estar delante: en el margen de la desconexión (`OFFLINE_PENDING` no protege, `RN-COMBAT-003`), al vencer el tope de
48 h de la protección (ADR-024 §3) y, sobre todo, **conectado pero sin mirar**, donde no hay protección ninguna.
Los tratados, además, cambian a cualquier hora: ofertas que llegan o caducan, rupturas que se anuncian. Al volver
el jugador encuentra menos aldeanos o menos madera y no sabe por qué. El aviso de saqueo (`pillagedAtMs`) dura
cinco segundos en el HUD y sólo lo recibe el dueño si está conectado en ese momento.

## 2. Scope

**Entra.** Leer de `world_events` los sucesos de §4.1 que afectan al jugador, agruparlos (§4.2), saber cuáles ya
vio (§4.3), dos comandos y un mensaje nuevos (§6), el actor de una ruptura en su evento, la atribución de cada
suceso a los dos jugadores implicados y, en el cliente, la ventana «Mientras no estabas» y un botón «Registro».

**No entra** (`Fuera de MVP` de este slice): avisos en vivo empujados por el servidor (Q5), correo o notificaciones
del sistema, la economía propia (casas terminadas, aldeanos entrenados, bosque agotado; Q1), los ataques que no
mataron ni robaron nada (Q1), ir al lugar con un clic, filtros y búsqueda, y borrar o archivar entradas.

## 3. Actores

Jugador, cliente web, Game Server (la goroutine de la conexión lee el registro y escribe lo visto; el tick loop
**no interviene**, ver §7), la cola de persistencia (ya escribe los sucesos con su efecto) y PostgreSQL.

## 4. Modelo

### 4.1 Qué entra

Cada suceso sale de un evento que la cola ya escribe en la misma transacción que su efecto (ADR-013). El otro
jugador se nombra por su ciudad, como en los tratados (`otherCityName`), y el lugar va en coordenadas.

| Suceso | Evento de origen | Ejemplo |
|---|---|---|
| Unidad tuya muerta | `UnitKilled`, víctima tú | «Villa Norte mató a 2 de tus aldeanos cerca de (451, 179)» |
| Unidad ajena abatida por las tuyas | `UnitKilled`, autor tú | «Tus guerreros mataron a 1 guerrero de Villa Norte» |
| Saqueo sufrido | `CityPillaged`, víctima tú | «Villa Norte te robó 85 de madera» |
| Saqueo hecho | `CityPillaged`, autor tú | «Tu guerrero robó 40 de madera a Villa Norte» |
| Oferta recibida o retirada | `TreatyProposed` y `TreatyWithdrawn` de la otra parte | «Villa Norte te ofrece un pacto de no agresión» |
| Respuesta a tu oferta | `TreatyAccepted`, `TreatyRejected` | «Villa Norte aceptó tu alianza» |
| Oferta caducada | `TreatyExpired` desde `PROPOSED` | «Tu oferta a Villa Norte caducó sin respuesta» |
| Ruptura anunciada por el otro | `TreatyBreakAnnounced` | «Villa Norte rompió el pacto: la paz termina a las 21:15» |
| Tratado terminado | `TreatyBroken`, o `TreatyExpired` desde `ACTIVE` | «La alianza con Villa Norte terminó; tus unidades guarnecidas salieron de su ciudad» |

Lo que hizo el propio jugador en la diplomacia (proponer, romper, retirar) no entra: ya lo sabe. `TreatyExpired` desde
`ACTIVE` sólo lo escriben los tratados administrativos con `expires_at`: el pacto que una alianza sustituye se cierra
sin evento propio, y lo cuenta el `TreatyAccepted` de la alianza.

### 4.2 Agrupación

Un saqueo son decenas de golpes de 5 de madera, y una incursión puede matar a varios aldeanos. Los sucesos del
mismo tipo y con el mismo otro jugador se juntan en una entrada mientras entre uno y el siguiente no pasen más de
`EO_EVENT_LOG_GROUP_GAP_MINUTES` (Q4): la entrada lleva cuántos fueron, la suma de madera y el primer y el último
instante. La agrupación se hace al leer; en la base cada golpe sigue siendo su evento. Un grupo nunca mezcla lo ya
visto con lo nuevo: el cursor (§4.3) lo parte.

### 4.3 Lo ya visto

Cada jugador tiene un cursor: el último evento que dio por visto. Al conectar, el cliente pide la primera página;
si hay entradas posteriores al cursor, abre «Mientras no estabas». Al cerrarla con «Entendido», el cliente manda
`event_log.seen` y el servidor avanza el cursor (Q3).

El cursor no puede ser sin más el `id` más alto mostrado. Los workers de la cola (cuatro por defecto) escriben en
paralelo y los `id` los asigna PostgreSQL al insertar, así que un evento con `id` menor puede quedar guardado
después de otro con `id` mayor: si el cursor ya pasó por encima, ese evento no se mostraría nunca. El servidor sólo
avanza el cursor hasta los eventos **asentados** en el momento de servir la página, los guardados hace más del doble del tope de una escritura de la
cola (hoy 10 s por intento): pasado ese margen, ninguna transacción con un `id` menor puede seguir abierta. Lo más
reciente, que el cursor todavía no cubre, se vuelve a mostrar la próxima vez: es mejor repetir que perder. El
detalle, y por qué basta, en [ADR-028](../decisions/ADR-028-event-log-from-world-events.md) §6.

### 4.4 Reglas

Dominio: `RN-LOG`.

| ID | Regla |
|---|---|
| `RN-LOG-001` | Un jugador sólo recibe entradas de sucesos en los que es parte: la víctima, el autor o uno de los dos firmantes del tratado. |
| `RN-LOG-002` | Cada entrada sale de un evento de `world_events`. El registro no guarda una copia aparte ni inventa nada que la base no tenga: si la escritura de un efecto falla, su suceso tampoco aparece. |
| `RN-LOG-003` | `event_log.fetch { beforeId? }` devuelve la página más reciente, o la de los eventos con `id` menor que `beforeId`, de los últimos `EO_EVENT_LOG_MAX_DAYS` días (Q2), ya agrupada (§4.2), con `EO_EVENT_LOG_PAGE_SIZE` entradas como mucho. La página se corta por eventos, no por grupos (ADR-028 §5): ninguno se salta al paginar, y un grupo en el borde sale en dos entradas, una en cada página. |
| `RN-LOG-004` | `event_log.seen { upToId }` avanza el cursor hasta `upToId` como mucho, y nunca más allá del tope marcable de la última página que esa sesión sirvió (§4.3); sin página servida, no hace nada. El cursor nunca retrocede: un `upToId` menor no hace nada. |
| `RN-LOG-005` | La hora de cada entrada es la del suceso en la simulación, no la de su escritura: un reintento de la cola no la retrasa. |
| `RN-LOG-006` | Leer el registro no pasa por el tick loop ni lo frena. Si PostgreSQL no responde, la página falla con `INTERNAL_ERROR` y la partida sigue. |
| `RN-LOG-007` | El cliente pide la página al conectar y al volver a la pestaña tras un rato fuera; el servidor no empuja nada (Q5). |

## 5. Errores

Sólo códigos existentes del catálogo (canon §16).

| Código | Condición |
|---|---|
| `INVALID_MESSAGE` | `beforeId` o `upToId` que no son enteros positivos (lo rechaza el esquema, como cualquier comando). |
| `INTERNAL_ERROR` | PostgreSQL no responde a tiempo al leer la página o al guardar el cursor (`RN-LOG-006`). |
| `RATE_LIMITED` | El cubo común de mensajes de la sesión, sin límite propio. |

## 6. Eventos y contratos de red

Cliente → servidor, dos comandos nuevos y estrictos, aditivos en v1 como los `treaty.*` de E6:
`event_log.fetch { beforeId? }` y `event_log.seen { upToId }`. No llegan a la simulación: los atiende la conexión,
como `session.ping`, y por eso no entran en `CommandTypes` ni crean trabajos de la cola.

Servidor → cliente: `event_log.page { entries[], seenUpToId, nextBeforeId }`, tipo nuevo (R-VER-3), sólo a la sesión
que lo pidió. `seenUpToId` es el cursor al servirla (las entradas con `id` mayor son nuevas) y `nextBeforeId`, el
`beforeId` de la página siguiente, `null` cuando no hay más. Cada entrada: `{ id, kind, firstAtMs, lastAtMs, count, otherPlayerId, otherCityId, otherCityName, unitType?, amount?,
treatyType?, x?, y?, breaksAtMs? }`. `id` es el del último evento del grupo y sirve para paginar y para el cursor.
El esquema Zod lo fija la implementación.

Eventos de dominio: ninguno nuevo. Dos cambios en los que ya existen (`ADR-028`):

- **Atribución a los dos jugadores.** Hoy cada evento lleva un solo `player_id`: la víctima en `UnitKilled` y
  `CityPillaged`, y quien propuso en los tratados. Con eso, quien recibe una oferta o sufre una ruptura no
  encuentra el evento. Una columna nueva, `counterpart_player_id`, guarda al otro jugador implicado (el autor de la
  muerte o del saqueo, el otro firmante) sin cambiar lo que ya significa `player_id`.
- **Lo que falta en el payload.** `TreatyBreakAnnounced` no dice quién lo anunció, porque `treaties` no lo guarda:
  lo llevará en `byPlayerId`. Los eventos de tratado llevarán `from`, el estado anterior (una oferta que caduca y un
  tratado que caduca son sucesos distintos); `UnitKilled`, el `unitType` de la víctima; y todos los de §4.1, el
  instante de la simulación (`atMs`, `RN-LOG-005`).

Cliente: al conectar, si hay entradas sin ver, la ventana «Mientras no estabas» con esas entradas y un botón
«Entendido»; en el HUD, un botón «Registro» con el número de entradas sin ver, que abre el historial y pagina
hacia atrás.

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | Nada. El registro es historia, no estado del mundo: el loop no lo tiene ni lo consulta |
| Write-through | Los eventos, como hoy, en la transacción de su efecto, ahora con `counterpart_player_id`; el cursor de lo visto, en una escritura propia desde la conexión al recibir `event_log.seen` |
| Dirty-flag | Nada |
| Reconstruible | Las entradas y sus grupos, que se derivan al leer de `world_events` |

Migración `000010`: `world_events.counterpart_player_id` (`uuid`, `REFERENCES players ON DELETE SET NULL`) con un
índice parcial `(counterpart_player_id, occurred_at DESC)`, el par del índice que ya existe por `player_id`; y
`players.event_log_seen_id` (`bigint NOT NULL DEFAULT 0`). Los eventos anteriores a la migración no tienen el otro
jugador, así que quien recibió una oferta antes de ella no la verá en el registro. Las muertes y los saqueos
anteriores sí le llegan a la víctima, con el autor que ya nombraba su payload (ADR-028 §3).

## 8. Invariantes

Cita [INV-SEC-002](../invariants/security.md#inv-sec-002) (ningún comando antes de autenticarse),
[INV-SEC-004](../invariants/security.md#inv-sec-004) (el registro que se lee es siempre el del jugador de la sesión:
ningún campo del comando elige otro), [INV-SEC-005](../invariants/security.md#inv-sec-005) (límites del mensaje) e
[INV-PERSIST-005](../invariants/persistence.md#inv-persist-005) (la migración `000010`). Asigna dos, registrados al
aceptar:

- [INV-SEC-009](../invariants/security.md#inv-sec-009): el registro de un jugador sólo cuenta sucesos en los que es
  parte (`RN-LOG-001`).
- [INV-PERSIST-006](../invariants/persistence.md#inv-persist-006): el cursor nunca pasa por encima de un evento que
  no estaba guardado cuando se sirvió la página que permitió marcarlo, y nunca retrocede (§4.3).

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | Agrupación (mismo tipo y jugador; un hueco mayor que el margen parte el grupo; suma de madera); qué evento entra para quién (cada fila de §4.1 y los propios que no entran); el cursor sólo avanza y sólo hasta lo asentado |
| integration | La atribución a los dos jugadores en cada evento de §4.1; la consulta de la página con el índice; un evento con `id` menor guardado después de que el cursor pasara (dos transacciones en orden inverso) se sigue mostrando; el cursor con fallo inyectado |
| contract / e2e | Los dos comandos y `event_log.page` contra el JSON Schema; un saqueo a un jugador desconectado que, al conectar, ve la entrada, la da por vista y no la vuelve a ver al recargar |
| simulation | Ninguno nuevo: el loop no cambia. Los golden de saqueo, defensa y diplomacia comprueban que sus eventos llevan al otro jugador |
| cliente | La ventana «Mientras no estabas», el contador del botón «Registro» y la paginación; `scripts/smoke.mjs --registro` (tras `--saqueo`, `smoke_victim` ve el saqueo) |

**Precisión (2026-11-03), al implementar.** Los e2e del Game Server corren contra el arnés en memoria, sin
PostgreSQL, así que el recorrido de la fila «contract / e2e» se reparte en tres niveles: el e2e de
`internal/websocket` prueba la conexión con un registro falso (la página es la de la sesión, el tope acota la
marca, un fallo de la base es `INTERNAL_ERROR` y la sesión sigue, que es «el cursor con fallo inyectado»); la
integración prueba la consulta, la atribución y el cursor contra la base; y `scripts/smoke.mjs --registro` hace el
recorrido entero contra el servidor vivo: la víctima ve el saqueo al conectar, lo da por visto, reconecta con un
ticket nuevo y el cursor sigue marcado.

## 10. Decisiones de producto (2026-11-03)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de
producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Qué sucesos entran? | Los de §4.1: lo que otros te hicieron y lo que tus unidades hicieron solas (muertes y saqueos, en los dos sentidos) y los cambios de tus tratados que no hiciste tú. Fuera: la economía propia, que se ve al volver en el HUD, y los ataques que no mataron ni robaron nada (`UnitAttacked`), que con la respuesta automática de E3 llenarían el registro de ruido. |
| Q2 | ¿Hasta cuándo atrás? | 7 días (`EO_EVENT_LOG_MAX_DAYS`) en páginas de 50 entradas ya agrupadas (`EO_EVENT_LOG_PAGE_SIZE`). Cubre a quien juega una vez por semana y a quien pasó del tope de 48 h. Lo más viejo sigue en `world_events` como auditoría, pero no se muestra. |
| Q3 | ¿Cómo se sabe qué ya viste? | Con un cursor en el servidor que avanza al pulsar «Entendido». Recargar no repite la ventana y otro dispositivo tampoco. Las alternativas son peores: «desde la última desconexión» repite o pierde la ventana con cada recarga, según el margen de reconexión, y una marca en el navegador la repite en cada dispositivo. |
| Q4 | ¿Se agrupan los sucesos? | Sí: mismo tipo y mismo otro jugador, con no más de 10 minutos (`EO_EVENT_LOG_GROUP_GAP_MINUTES`) entre uno y el siguiente. «Villa Norte te robó 85 de madera en 17 golpes» se lee; diecisiete líneas de 5 no. |
| Q5 | ¿Avisa en vivo mientras juegas? | No en este slice. Conectado ya ves morir la unidad, el aviso de saqueo y `treaty.update`. Para quien está conectado sin mirar, el cliente vuelve a pedir la página al volver a la pestaña (`RN-LOG-007`), y eso basta. Empujar cada suceso al instante obliga a darle un `id` antes de que la base lo asigne, el mismo problema que `EO-120` tuvo con los movimientos. |
| Q6 | ¿Se dice quién fue? | Sí, por el nombre de su ciudad. No revela nada nuevo: las unidades ya llevan su `playerId` y su ciudad está en el mapa, y saber quién te saquea es lo que da sentido a proponerle un pacto o devolverle el golpe. |

## 11. Documentos relacionados

- [combat-pillage.md](combat-pillage.md) (`pillagedAtMs`, el aviso que este registro completa) y
  [diplomacy.md](diplomacy.md) §6 (los eventos de tratado).
- [presence.md](presence.md) y [combat-raid.md](combat-raid.md) `RN-COMBAT-003` (cuándo se está expuesto).
- [../roadmap/backlog.md](../roadmap/backlog.md): `EO-032` (`world_events` como registro append-only), `EO-120`.
