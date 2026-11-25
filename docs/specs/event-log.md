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

**No entra** (`Fuera de MVP` de este slice): avisos en vivo empujados por el servidor (Q5; desde E11 los hay, sin
ser entradas: [live-notices.md](live-notices.md)), correo o notificaciones
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
| Casa derribada (desde E8) | `BuildingDestroyed`, víctima tú | «Villa Norte derribó 2 de tus casas» |
| Casa derribada por los tuyos (desde E8) | `BuildingDestroyed`, autor tú | «Tus guerreros derribaron 1 casa de Villa Norte» |
| Muralla derribada (desde E27) | `BuildingDestroyed` con `type = WALL`, víctima tú o autor tú | «Villa Norte derribó tu muralla: tu ciudad está en brecha»; «Tus arietes derribaron la muralla de Villa Norte» |

**Precisión (2026-11-19, [siege-engines.md](siege-engines.md) §6).** Las entradas `HOUSE_LOST` y `HOUSE_DESTROYED`
llevan `buildingType` (opcional), el tipo del edificio que cayó, leído del payload de `BuildingDestroyed`, y forma parte
de la clave del grupo: «2 casas» y «tu muralla» son dos entradas.
| Oferta recibida o retirada | `TreatyProposed` y `TreatyWithdrawn` de la otra parte | «Villa Norte te ofrece un pacto de no agresión» |
| Respuesta a tu oferta | `TreatyAccepted`, `TreatyRejected` | «Villa Norte aceptó tu alianza» |
| Oferta caducada | `TreatyExpired` desde `PROPOSED` | «Tu oferta a Villa Norte caducó sin respuesta» |
| Ruptura anunciada por el otro | `TreatyBreakAnnounced` | «Villa Norte rompió el pacto: la paz termina a las 21:15» |
| Tratado terminado | `TreatyBroken`, o `TreatyExpired` desde `ACTIVE` | «La alianza con Villa Norte terminó; tus unidades guarnecidas salieron de su ciudad» |

**Precisión (2026-11-04, [trade.md](trade.md) Q10, [ADR-032](../decisions/ADR-032-trade-offers-and-two-key-jobs.md) §7).**
Desde E10 entran también los tratos, con el mismo criterio que los tratados: la oferta recibida (`TradeProposed`, «Villa
Norte te ofreció 300 de madera por 100 de piedra»), la propia aceptada (`TradeAccepted`, «Villa Norte aceptó tu trato:
diste 300 de madera y recibiste 100 de piedra») o rechazada (`TradeRejected`), la recibida que el otro retiró
(`TradeWithdrawn`) y la que caducó, propia o recibida (`TradeExpired`). Son los tipos de entrada `TRADE_OFFERED`,
`TRADE_ACCEPTED`, `TRADE_REJECTED`, `TRADE_WITHDRAWN` y `TRADE_OFFER_EXPIRED`, con lo que salía del almacén de quien lee
(`tradeGive`) y lo que entraba (`tradeGet`). Los tratos no se agrupan (§4.2): cada uno es su entrada.

**Precisión (2026-11-13, [caravans.md](caravans.md) §6, [ADR-037](../decisions/ADR-037-caravans-as-units.md) §7).**
Desde E15 entran las caravanas que le pasan a quien lee: la que le llegó (`CaravanArrived`, «Llegó la caravana de
Villa Norte con 100 de piedra»), la suya capturada (`CaravanCaptured`), la que venía a él capturada
(`CaravanIntercepted`) y la suya capturada que se perdió (`CaravanLost`). Son los tipos `CARAVAN_ARRIVED`,
`CARAVAN_CAPTURED`, `CARAVAN_INTERCEPTED` y `CARAVAN_LOST`, con lo que llevaba (`cargo`) y el tile. Como los tratos, no
se agrupan; y la captura que hizo el propio jugador no entra (`RN-LOG-001`).

**Precisión (2026-11-15, [clans.md](clans.md) Q10, [ADR-040](../decisions/ADR-040-clans-membership-and-peace.md)).**
Desde E18 entra la expulsión de un clan (`ClanMemberExpelled`, con el líder como `counterpart_player_id`): al
expulsado, `CLAN_EXPELLED` («Villa Norte te expulsó de su clan»), y al líder, `CLAN_MEMBER_EXPELLED` («Expulsaste a
Villa Norte de tu clan»). Fundar, entrar y salir por voluntad propia no entran (`ClanFounded`, `ClanJoined` y
`ClanLeft` no son tipos del registro).

**Precisión (2026-11-16, [clan-governance.md](clan-governance.md) Q8).** Desde E20 entra el cambio de líder de un clan
(`ClanLeaderChanged`, con el nuevo líder como `player_id` y el anterior como `counterpart_player_id`). Por traspaso: al
nuevo, `CLAN_LEADERSHIP_RECEIVED` («Villa Norte te cedió el liderazgo de [LOB]»), y al anterior,
`CLAN_LEADERSHIP_GIVEN` («Cediste el liderazgo de [LOB] a Villa Norte»). Porque el líder llevaba días sin jugar: al
nuevo, `CLAN_LEADERSHIP_INHERITED`, y al ausente, `CLAN_LEADERSHIP_LOST`, que lo encuentra en «Mientras no estabas».
Las cuatro entradas llevan `clanTag`. Nombrar o quitar un oficial no entra (`ClanRankChanged` no es tipo del registro).

**Precisión (2026-11-16, [clan-alliances.md](clan-alliances.md) Q9, [ADR-042](../decisions/ADR-042-clan-alliances.md) §6).**
Desde E21 entran las alianzas entre clanes, con la sigla del otro clan en `clanTag`. Al líder del clan que recibe una
oferta, `CLAN_ALLIANCE_OFFERED` («Villa Norte propone una alianza entre [OSO] y tu clan»); al que propuso, la
aceptación (`CLAN_ALLIANCE_ACCEPTED`) y el rechazo (`CLAN_ALLIANCE_REJECTED`). El preaviso de ruptura
(`CLAN_ALLIANCE_BREAK_ANNOUNCED`, con `breaksAtMs`) es una fila por miembro abierto de los dos clanes, de un solo lado
(`only`), con quien lo provocó como `counterpart_player_id`: al líder que rompió, o a quien con su entrada en un bando o
en el clan juntó a la Orden y al Caos, no se le cuenta. Retirar, la ruptura vencida y la caducidad no entran. Cada
suceso de una alianza es su propia entrada. El catálogo pasa de 31 a 35 tipos.

**Precisión (2026-11-17, [castle-age.md](castle-age.md) §6, [ADR-044](../decisions/ADR-044-castle-shooting.md) §6).**
Desde E23, una muerte que causa un castillo (`UnitKilled` con `killerBuildingType: "CASTLE"` en el payload y el dueño
del castillo como `counterpart_player_id`) da sus propios tipos: a la víctima, `UNIT_LOST_TO_CASTLE` («El castillo de
Villa Norte mató a uno de tus guerreros»), y al dueño del castillo, `CASTLE_KILL` («Tu castillo mató a 3 guerreros de
Villa Norte»), con `unitType` y el tile como las muertes de siempre. No se agrupan con las que causó un ejército del
mismo jugador. `EventTypes` no cambia; el catálogo pasa de 35 a 37 tipos.

**Precisión (2026-11-17, [clan-wars.md](clan-wars.md) Q11, [ADR-045](../decisions/ADR-045-clan-wars.md) §6).**
Desde E24 entran las guerras entre clanes, con la sigla del otro clan en `clanTag`. La declaración
(`CLAN_WAR_DECLARED`, con `startsAtMs`: «Villa Norte declaró la guerra entre tu clan y [OSO]: empieza a las 15:30») y el
final (`CLAN_WAR_ENDED`, con `warResult`, `warReason`, `ourScore` y `theirScore`: «La guerra con [OSO] terminó:
victoria por 31 – 18 (plazo)») son una fila por miembro abierto de los dos clanes, de un solo lado (`only`), salvo para
quien dio la orden; el final por plazo no lo dio nadie y llega a todos, y el de una disolución, sólo al otro clan. La
oferta de paz (`CLAN_WAR_PEACE_OFFERED`) es una fila por líder y oficial del clan que la recibe. El comienzo y la
retirada o el rechazo de una oferta no entran. Cada suceso de una guerra es su propia entrada. Un
`TREATY_BREAK_ANNOUNCED` que abrió una guerra lleva `breakReason: "WAR"` («La guerra entre vuestros clanes rompe el
pacto de no agresión con Villa Norte: la paz termina a las 15:30»). `EventTypes` gana `ClanWarDeclared`,
`ClanWarPeaceOffered` y `ClanWarEnded`; el catálogo pasa de 37 a 40 tipos.

**Precisión (2026-11-18, [markets.md](markets.md) Q11, [ADR-047](../decisions/ADR-047-markets.md)).**
Desde E26 entra el mercado, sólo para el vendedor y sin aviso en vivo. La venta (`MARKET_SOLD`, de `MarketPurchase`, con
el comprador como `counterpart_player_id`) lleva `lots`, lo que salió de la custodia en `tradeGive` y lo que viene en la
caravana del comprador en `tradeGet` («Villa Norte compró 2 lotes de tu oferta en el mercado: le mandas 40 de piedra y te
manda 60 de madera»); al comprador no se le cuenta su compra. La caducidad (`MARKET_EXPIRED`, de `MarketOfferExpired`) es
la **única entrada sin otro jugador**: `otherPlayerId`, `otherCityId` y `otherCityName` van a `null`, y lleva los lotes
devueltos en `lots` y lo que volvió al almacén en `tradeGet` («Tu oferta del mercado caducó: 1 lote sin vender, vuelven
20 de piedra a tu almacén»). La publicación y la retirada no entran: las ordenó el propio jugador. Cada suceso es su
propia entrada. `EventTypes` gana `MarketPurchase` y `MarketOfferExpired`; el catálogo pasa de 40 a 42 tipos.

**Precisión (2026-11-24, [multi-city.md](multi-city.md) Q11, [ADR-051](../decisions/ADR-051-multi-city-capital.md)).**
Desde E34 entra la fundación de una segunda ciudad: `CityFounded` se cuenta sólo al fundador, sin otro jugador
(`otherPlayerId` nulo, como `MARKET_EXPIRED`), como `CITY_FOUNDED` con el centro de la ciudad nueva en `x`/`y` («Fundaste
una ciudad nueva cerca de (40, 40)»). Cada fundación es su propia entrada y no se avisa en vivo. `EventTypes` gana
`CityFounded`; el catálogo pasa de 45 a 46 tipos. Con dos ciudades, el otro jugador se sigue nombrando por la de id
menor, su capital.

**Precisión (2026-11-23, [city-fall.md](city-fall.md) Q9, [ADR-050](../decisions/ADR-050-city-fall.md)).**
Desde E31 entra la caída de una ciudad. El comienzo de un asalto (`CITY_ASSAULTED`, de `CityAssaultStarted`) se cuenta
sólo al asaltado, con `fallsAtMs` y el centro de su ciudad en `x`/`y` («¡Villa Norte asalta tu ciudad! Cae a las 18:15
si nadie mata al que sostiene el estandarte o levantas la muralla»); a quien asalta no se le cuenta lo que ordenó. La
caída (`CityFell`) se cuenta a los dos: `CITY_LOST` al vencido, con el centro de su ciudad nueva en `x`/`y` («Tu ciudad
cayó ante Villa Norte: la refundaste en (300, 400)»), y `CITY_TAKEN` al vencedor, con el de la arrasada. Un asalto roto
no se escribe. Cada suceso es su propia entrada. `EventTypes` gana `CityAssaultStarted` y `CityFell`; el catálogo pasa
de 42 a 45 tipos. El otro jugador se nombra por su ciudad en pie: la consulta de las ciudades del registro ignora las
caídas. **Precisión (2026-11-25):** desde E36 ([city-taken.md](city-taken.md)), el payload de `CityFell` lleva `takenCityId`
cuando el vencedor se quedó la ciudad; los textos de `CITY_LOST` y `CITY_TAKEN` no cambian.

**Precisión (2026-11-16, [clan-territories.md](clan-territories.md) §6, [ADR-041](../decisions/ADR-041-clan-territories.md) §5).**
Desde E19, con un clan dueño, `TerritoryClaimStarted` y `TerritoryControlChanged` se escriben una fila por miembro
abierto del clan, con `only: "player"` en el payload: cada fila le cuenta el suceso sólo a su miembro, con la sigla del
clan (`clanTag`), y nada a quien reclama; quien lo gana recibe su `TERRITORY_TAKEN` de una fila aparte con
`only: "counterpart"` (su `player_id` es el miembro más antiguo del clan que lo pierde). Lo ganado para un clan lleva la
sigla de ese clan. La liberación de los territorios al disolverse un clan escribe un `TerritoryControlChanged` sin
`counterpart_player_id`, que no entra en el registro de nadie.

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
| `RN-LOG-007` | El cliente pide la página al conectar y al volver a la pestaña tras un rato fuera; el servidor no empuja nada (Q5). **Precisión (2026-11-04, [live-notices.md](live-notices.md) `RN-NOTICE-004`, [ADR-033](../decisions/ADR-033-live-notices-ephemeral.md)):** el servidor no empuja entradas del registro. Desde E11 empuja avisos (`event_log.notice`), que no son entradas: no llevan `id`, no mueven el cursor y no entran en la lista. El cliente pide además la primera página al abrir el registro. |

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
| Q5 | ¿Avisa en vivo mientras juegas? | No en este slice. Conectado ya ves morir la unidad, el aviso de saqueo y `treaty.update`. Para quien está conectado sin mirar, el cliente vuelve a pedir la página al volver a la pestaña (`RN-LOG-007`), y eso basta. Empujar cada suceso al instante obliga a darle un `id` antes de que la base lo asigne, el mismo problema que `EO-120` tuvo con los movimientos. **Precisión (2026-11-04, [live-notices.md](live-notices.md) Q1, [ADR-033](../decisions/ADR-033-live-notices-ephemeral.md)):** desde E11 sí avisa, pero no con entradas. Quien mira otra parte del mapa no ve morir su unidad lejos, y la respuesta automática de E3 mata sin que nadie lo ordene: el servidor manda en el tick del suceso un aviso efímero sin `id` (`event_log.notice`) de cuatro tipos —`UNIT_LOST`, `ENEMY_KILLED`, `PILLAGE_DONE` y `HOUSE_DESTROYED`—, clasificado con la misma función que el registro. La lista sigue saliendo sólo de la página, así que el problema del `id` no aparece. |
| Q6 | ¿Se dice quién fue? | Sí, por el nombre de su ciudad. No revela nada nuevo: las unidades ya llevan su `playerId` y su ciudad está en el mapa, y saber quién te saquea es lo que da sentido a proponerle un pacto o devolverle el golpe. |

## 11. Documentos relacionados

- [combat-pillage.md](combat-pillage.md) (`pillagedAtMs`, el aviso que este registro completa) y
  [diplomacy.md](diplomacy.md) §6 (los eventos de tratado).
- [presence.md](presence.md) y [combat-raid.md](combat-raid.md) `RN-COMBAT-003` (cuándo se está expuesto).
- [../roadmap/backlog.md](../roadmap/backlog.md): `EO-032` (`world_events` como registro append-only), `EO-120`.
