# Caravanas: el comercio viaja por el mapa y se puede interceptar

Propuesta para que un trato deje de entregarse en el acto: cada parte manda su mercancía en una caravana que recorre el
mapa hasta la otra ciudad, y quien la mate por el camino se la queda.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CARAVANS` |
| Estado | Accepted (2026-11-13). Propuesta en `Draft` y aceptada ese mismo día, con la recomendación de cada pregunta de §10. |
| Milestone | E15 Caravanas (decimoquinto slice después de M7) |
| Canon | §9 (unidades), §11 (tratados), §12 (persistencia), §13 (protocolo), §16 (errores) |
| Depende de | [trade.md](trade.md), [movement.md](movement.md), [unit.md](unit.md), [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md), [civilization-goods.md](civilization-goods.md), [civilizations.md](civilizations.md), [safe-zones.md](safe-zones.md), [event-log.md](event-log.md), [live-notices.md](live-notices.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-013](../decisions/ADR-013-persistence-pipeline.md) (cola ordenada por clave), [ADR-016](../decisions/ADR-016-foreign-territory-actions.md) (acciones hostiles), [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) (deltas de stock y órdenes en dos fases), [ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md) (el combate en la fase 4), [ADR-031](../decisions/ADR-031-pathfinding-node-cap-50k.md) (el tope de nodos del A\*), [ADR-032](../decisions/ADR-032-trade-offers-and-two-key-jobs.md) (el trato y el trabajo de dos claves), [ADR-036](../decisions/ADR-036-civilization-goods.md) (la entrega optimista de la elaboración). [ADR-037](../decisions/ADR-037-caravans-as-units.md) (la caravana como tipo de unidad con su carga en una tabla aparte, el trato que carga en lugar de acreditar, la entrega optimista y la captura como cambio de dueño; migración `000017`). |
| Reemplaza a | En [trade.md](trade.md), `RN-TRADE-004` («entrega en el acto») y la Q7 («entrega instantánea, a cualquier distancia»). En [combat-raid.md](combat-raid.md), `RN-COMBAT-003` sólo para las caravanas (Q7). En [trade.md](trade.md) Q8, [economy-stone.md](economy-stone.md) §4 y [civilization-goods.md](civilization-goods.md) §4.5, que la piedra y los bienes nunca cambian de manos por la fuerza: la captura de una caravana es la excepción (Q6). Abre la fase Caravans de [roadmap.md](../roadmap/roadmap.md), sin rutas permanentes. |
| Invariantes | Cita [INV-ECON-001](../invariants/economy.md#inv-econ-001), [INV-ECON-002](../invariants/economy.md#inv-econ-002), [INV-SEC-001](../invariants/security.md#inv-sec-001), [INV-UNIT-005](../invariants/units.md#inv-unit-005), [INV-COMBAT-001](../invariants/combat.md#inv-combat-001) e [INV-SAFE-004](../invariants/territory.md#inv-safe-004); precisa [INV-TRADE-001](../invariants/trade.md#inv-trade-001), [INV-COMBAT-002](../invariants/combat.md#inv-combat-002) e [INV-CITY-017](../invariants/city.md#inv-city-017); asigna [INV-TRADE-005](../invariants/trade.md#inv-trade-005), [INV-TRADE-006](../invariants/trade.md#inv-trade-006) e [INV-TRADE-007](../invariants/trade.md#inv-trade-007) (§8) |

## 1. Objetivo

Devolver al comercio la distancia y el riesgo que E10 aplazó. Hoy un trato entre dos esquinas del mapa llega en el
mismo segundo ([trade.md](trade.md) Q7), y desde E14 es el camino obligado para llegar a la Edad de Hierro: 60 bienes
que sólo producen otras civilizaciones. Con este slice la mercancía sale de una ciudad, recorre el mapa a la vista de
todos y llega a la otra al cabo de unos minutos, si llega. Es la mezcla que busca el juego: la interdependencia del
pilar 4 de [game-pillars.md](../product/game-pillars.md) con el riesgo de Argentum Online, y el pilar 5 recupera lo
que el comercio instantáneo le quitaba (la «tensión 4 vs. 5»): vivir lejos de tu socio vuelve a costar algo.

## 2. Scope

**Entra.** Un trato aceptado crea **dos caravanas**, una por sentido, que salen del depósito de cada ciudad con lo que
da esa parte y caminan hasta el depósito de la otra; el tipo de unidad `CARAVAN` (sin población, sin órdenes del
jugador, con vida y velocidad propias); la ruta calculada una vez al aceptar, y la comprobación de que hay ruta al
proponer y al aceptar; la entrega al llegar; atacar una caravana con el `unit.attack` de siempre y **capturarla** con
el golpe mortal, que la desvía a la ciudad del captor con la carga entera; la recaptura; la respuesta automática de los
guerreros del dueño (E3) como escolta; `caravan.update` y `caravans[]` en el snapshot para los implicados; las
capturas y las llegadas en el registro, y la captura como aviso en vivo; en el cliente, el dibujo de la caravana, su
ruta para los implicados y la lista «Caravanas en camino»; `scripts/smoke.mjs --caravana`, y `--comercio` y `--bienes`
esperando la llegada.

**No entra** (`Fuera de MVP` de este slice): rutas comerciales permanentes y caravanas que van y vuelven solas
(`trade_routes`); mandar una caravana sin trato (regalos, envíos a un aliado o entre ciudades propias); mercados
públicos; una orden de escolta o de seguir a otra unidad; mover, detener o desviar una caravana a mano (Q8); caravanas
por agua; varias caravanas por sentido o una capacidad de carga (Q3); que los guerreros ataquen solos a una caravana
que pasa; niebla de guerra; el A\* jerárquico (`DEBT-04`: aquí se busca una ruta por trato, no de forma continua).

## 3. Actores

Los dos jugadores del trato, el jugador que intercepta, cliente web, Game Server (único que decide la ruta, la llegada,
los golpes y la captura, [INV-SEC-001](../invariants/security.md#inv-sec-001)), tick loop (fases 1-2 para las órdenes,
3 para el avance por la polilínea, 4 para los golpes y la captura, 5 para la llegada), pathfinding, cola de
persistencia, PostgreSQL.

## 4. Modelo

### 4.1 La caravana

Una unidad nueva del catálogo, `CARAVAN`, que nunca se entrena: sólo la crea un trato aceptado.

| Parámetro | Valor propuesto (Q4) | Comparación |
|---|---|---|
| Vida | 80 | Guerrero 60, aldeano 40: un guerrero solo la captura en 10 golpes (15 s) |
| `BaseMsPerTile` | 1 200 | El doble que un guerrero (600): un guerrero la alcanza siempre |
| Rasgo de velocidad | `unit.move_speed` de la civilización de su dueño ([civilizations.md](civilizations.md) §4.2) | Con los nórdicos (1 100), unos 1 090 ms por tile |
| Coste de población | 0 | No cuenta en `population` ni en el límite |
| Ataque, recolección, guarnición | No | No golpea, no responde, no huye, no se guarnece |
| Carga | Lo que da su parte del trato, entero: cualquier mezcla de los seis recursos | Sin tope (Q3) |

Con estos números, una caravana de smoke_victim (451,179) a smoke_runner (291,19), unos 160 tiles en diagonal, tarda
entre 3 y 5 minutos según el rodeo; una a 24 tiles, como la de dev_norte, medio minuto.

### 4.2 Del trato a las caravanas

Aceptar sigue siendo una orden en dos fases con el trabajo de dos claves de
[ADR-032](../decisions/ADR-032-trade-offers-and-two-key-jobs.md), con dos cambios:

- **Ruta.** En el tick de la aceptación se busca una ruta del depósito de la ciudad que propuso
  (`RN-ECON-004`) al depósito de la otra, con los límites de siempre (`EO_PATHFINDING_MAX_DISTANCE`, 256 tiles de
  Chebyshev, y 50 000 nodos). La caravana de vuelta usa la misma ruta al revés: el A\* no corta esquinas, así que la
  transitabilidad es simétrica y basta una búsqueda por trato. Sin ruta, la aceptación se rechaza
  (`PATH_NOT_FOUND`, `PATH_TOO_LONG`) y la oferta sigue pendiente. Proponer hace la misma búsqueda, para no dejar
  ofertas que nunca se podrán aceptar.
- **Carga en lugar de crédito.** La transacción del trato debita los dos almacenes como hoy, pero en vez de acreditar
  a la otra ciudad **crea las dos caravanas**: su fila de `units` en el depósito de origen, su movimiento `ACTIVE`
  ([INV-UNIT-005](../invariants/units.md#inv-unit-005)), su fila de `caravans` y sus líneas de carga. Todo o nada:
  o se aceptó el trato y existen las dos caravanas cargadas, o no se movió nada.

Las caravanas nacen en el tick en que vuelve la confirmación de la base, como lo que entrega hoy un trato: nada se
anuncia antes de existir.

### 4.3 En camino y llegada

Una caravana es una unidad `MOVING` más: avanza por su polilínea en la fase 3, se ve en el chunk por donde pasa con
`entity.spawn`/`entity.update` y su posición tras una caída se deriva de la polilínea, como la de cualquier unidad
([movement.md](movement.md)). Nadie le da órdenes: un `unit.move`, `unit.gather` o cualquier otra orden sobre ella
responde `UNIT_NOT_MOVABLE`.

Cuando llega al depósito de su ciudad de destino, en ese mismo tick: la RAM acredita la carga en el almacén del
destino, la caravana desaparece (`entity.despawn { REMOVED }`) y se encola la entrega. La entrega es **optimista**,
como la de la elaboración de E14: su transacción pasa la caravana a `DELIVERED` (condicionado a `IN_TRANSIT`), aplica
el delta versionado de cada recurso en el almacén del destino y borra su fila de `units`. Un reintento sin la
caravana en `IN_TRANSIT` no acredita nada. Si la entrega se descarta del todo, el crédito ya está en la RAM y la
entrega se reintenta en el siguiente volcado con una versión nueva de cada stock, como la de la elaboración de E14.
Una caravana parada fuera del depósito de su destino (tras un reinicio con un movimiento que no se pudo recuperar)
vuelve a buscar ruta, como mucho una vez por minuto.

Que el tratado termine con caravanas en camino no las detiene: ya están pagadas, y llegan (Q10).

### 4.4 Interceptar y capturar

Una caravana se ataca con `unit.attack`, como cualquier unidad: es **hostil** y pasa por el predicado de
[INV-COMBAT-002](../invariants/combat.md#inv-combat-002), con una excepción (Q7): **la protección offline de su dueño
no la cubre**. Un tratado de paz (`NON_AGGRESSION` o `ALLIANCE`) con su dueño, una Safe Zone bajo el atacante o bajo
la caravana, y la protección del propio atacante (`RN-COMBAT-018`) siguen impidiendo el golpe. El comercio sin paz
(`TRADE`) no impide nada: tu socio puede atacar tus caravanas.

Los golpes caen en la fase 4 como los de E2 y, al ser una unidad del dueño, cada golpe alerta a sus guerreros
disponibles a ≤ `EO_COMBAT_GUARD_RADIUS_TILES` (`RN-COMBAT-014`): **la escolta es tener guerreros cerca**, parados en
la ruta o caminando junto a ella.

El golpe que deja la caravana en 0 de vida **la captura** (Q5) en lugar de matarla:

- Pasa a ser del dueño del guerrero que dio el golpe mortal (el mismo desempate que el botín de `RN-COMBAT-010`), de su
  ciudad, con la vida entera y la carga intacta.
- Se busca una ruta desde donde está hasta el depósito de la ciudad del captor y sigue por ella, con el rasgo de
  velocidad de la civilización del captor. Si no hay ruta, la caravana se destruye y la carga se pierde
  (`CaravanLost`).
- Terminan los enfrentamientos de los guerreros del captor contra ella; los demás siguen si el nuevo dueño es atacable
  por ellos.
- Se puede recapturar con la misma regla, cuantas veces sea: siempre va a la ciudad de su dueño de ese momento. El
  destinatario original puede recuperarla y llevársela a su ciudad, que es adonde iba.

Una caravana nunca queda `DEAD`: llega, se captura o se pierde.

### 4.5 Reglas

Dominio: `RN-CARAVAN`.

| ID | Regla |
|---|---|
| `RN-CARAVAN-001` | Sólo un trato aceptado crea caravanas: exactamente dos, una por sentido, cada una con lo que da su parte, en el depósito de su ciudad y en la misma transacción que los débitos (§4.2). Ningún otro camino crea una caravana. |
| `RN-CARAVAN-002` | Proponer y aceptar un trato exigen una ruta entre los depósitos de las dos ciudades dentro de los límites del pathfinding; si no la hay, `PATH_NOT_FOUND` o `PATH_TOO_LONG`, y una aceptación rechazada deja la oferta pendiente. La ruta de la aceptación es la de las dos caravanas, una al revés de la otra. |
| `RN-CARAVAN-003` | Una caravana no admite órdenes de nadie (`UNIT_NOT_MOVABLE`), no cuenta en la población de su ciudad, no ataca, no responde a los golpes, no se guarnece y no recolecta. |
| `RN-CARAVAN-004` | **Llegada.** Al alcanzar el depósito de su ciudad de destino, la carga entra en ese almacén y la caravana desaparece, en el mismo tick; la entrega durable acredita como mucho una vez (§4.3). |
| `RN-CARAVAN-005` | **Atacar una caravana** es `unit.attack` y sigue `RN-COMBAT-001` a `RN-COMBAT-008`, salvo `RN-COMBAT-003`: la protección offline de su dueño no la cubre (Q7). |
| `RN-CARAVAN-006` | **Captura.** El golpe que la deja en 0 la pasa al dueño del guerrero que lo dio, con la vida entera, la carga intacta y una ruta nueva a la ciudad del captor; sin ruta, se pierde con su carga. Nunca pasa a `DEAD`. |
| `RN-CARAVAN-007` | **Determinismo.** Las llegadas se procesan en la fase 5 en orden de `id` de la caravana; las capturas, al aplicar los golpes de la fase 4 en el orden de `RN-COMBAT-007`. Una caravana que llega y recibe el golpe mortal en el mismo tick se captura (la fase 4 va antes). Sin azar. |
| `RN-CARAVAN-008` | El fin del tratado entre las dos partes no afecta a las caravanas en camino (Q10). |
| `RN-CARAVAN-009` | La carga de una caravana sólo la ven su dueño, el destinatario original y quien la capture; los demás ven la caravana, su dueño y su vida (Q9). |

### 4.6 Qué ve el jugador

- En el mapa, la caravana como un carro, del color de su dueño, como cualquier unidad.
- En el HUD, «Caravanas en camino»: las que salen de tu ciudad, las que vienen a ella y las que capturaste, con el otro
  jugador, la carga y la llegada estimada; «Ir» centra la cámara en la caravana.
- Con un guerrero seleccionado, un clic en una caravana ajena la ataca, como en cualquier unidad enemiga; la ayuda del
  guerrero dice que así se captura. Con la caravana propia seleccionada, un clic no manda nada.
- Al aceptar o ver aceptado un trato, la mercancía aparece en «Caravanas en camino» con su llegada, no en el almacén.
- Un aviso en vivo cuando te capturan una caravana, tuya o que venía a ti (Q11).

**Precisión (2026-11-13, implementación).** Tres cosas de esta lista no se hacen como decía la propuesta: el cliente no
puede saber si hay camino entre las dos ciudades, así que el formulario no se deshabilita por eso; el servidor
rechaza el trato con `PATH_NOT_FOUND` o `PATH_TOO_LONG` y el pie lo explica. La ruta de una caravana se dibuja como la
de cualquier unidad, sin un trazado aparte para los implicados. Y no hay un panel propio de la caravana con
«Capturar»: el ataque es el clic de siempre, y la ayuda lo dice.

## 5. Errores

Sólo códigos que ya existen en el catálogo (canon §16).

| Código | Condición |
|---|---|
| `PATH_NOT_FOUND` | `trade.propose` o `trade.respond { accept: true }` sin ruta transitable entre los depósitos de las dos ciudades (`RN-CARAVAN-002`). |
| `PATH_TOO_LONG` | Lo mismo con las dos ciudades a más de `EO_PATHFINDING_MAX_DISTANCE`, o con la búsqueda agotando el tope de nodos. |
| `UNIT_NOT_MOVABLE` | Cualquier orden de unidad sobre una caravana, propia o ajena (`RN-CARAVAN-003`). |
| `INVALID_TARGET` | `unit.attack` sobre una caravana propia, de alguien con quien tienes un pacto o una alianza, o con el atacante o la caravana en una Safe Zone; como hoy para cualquier unidad. |
| `CITY_PROTECTED` | Sólo cuando quien ataca está protegido (`RN-COMBAT-018`); **no** por la protección del dueño de la caravana (Q7). |

## 6. Eventos y contratos de red

Cliente → servidor: **ningún mensaje nuevo**. Atacar es `unit.attack`; los tratos, los de E10. El catálogo sigue en 24.

Servidor → cliente:

- `UnitType` suma `CARAVAN`. Las caravanas viajan en `entity.spawn`, `entity.update` y `entity.despawn` como cualquier
  unidad, **sin la carga**: esos mensajes van a todo el chunk.
- `caravan.update { caravan }`, tipo nuevo (R-VER-3), a las sesiones de los implicados (el dueño de ahora, quien la
  mandó, el destinatario original, el dueño de su destino y, en una captura, quien la pierde) cuando una caravana
  sale, cambia de dueño, llega o se pierde; y `caravans[]`, campo opcional de `world.snapshot` (R-VER-2), con las
  caravanas `IN_TRANSIT` que el jugador ve con su carga. La vista: `{ id, unitId, ownerPlayerId, senderPlayerId,
  fromCityId, fromCityName, toCityId, toCityName, originalToCityId, cargo, status, arrivesAtMs }`. El catálogo pasa
  de 23 a 24 tipos, y su e2e llega en el mismo commit.
- `EventLogKind` suma `CARAVAN_ARRIVED`, `CARAVAN_CAPTURED`, `CARAVAN_INTERCEPTED` y `CARAVAN_LOST`, y
  `EventLogNoticeKind` los tres últimos; las entradas y los avisos llevan `cargo`.

Eventos de dominio en `world_events`, con `counterpart_player_id` y `atMs` cuando implican a dos jugadores
([ADR-028](../decisions/ADR-028-event-log-from-world-events.md)):

- `TradeAccepted` (el de E10) pasa a llevar los dos `caravanIds`.
- `CaravanArrived`: jugador el destinatario, contraparte el remitente, con la carga.
- `CaravanCaptured`: jugador el dueño que la pierde, contraparte el captor, con la carga y la ciudad adonde va ahora.
  Si el destinatario original es un tercero (ni el que la pierde ni el captor), una segunda fila
  `CaravanIntercepted` con él como jugador y el captor como contraparte, para que le llegue al registro.
- `CaravanLost`: la caravana capturada sin ruta a la ciudad del captor, con lo que se perdió.

Registro ([event-log.md](event-log.md)), sin lo que hizo el propio jugador (`RN-LOG-001`): la caravana que te llegó,
la tuya capturada, la que venía a ti capturada y la que se perdió. La captura que hiciste tú no entra, como la muerte
que causaste con una orden. Como la entrega y la captura son optimistas, su entrada aparece en el registro cuando su
transacción se confirma, unos milisegundos después del cambio en el almacén y del aviso: la misma ventana que el
aviso de saqueo ([ADR-033](../decisions/ADR-033-live-notices-ephemeral.md) §6). Los tipos de entrada nuevos se suman a `eventlog.Kinds()`, al enum del protocolo y a
`Classify`; los eventos, a `eventlog.EventTypes()`; y los tipos de `CaravanCaptured`, `CaravanIntercepted` y
`CaravanLost`, a `eventlog.NoticeKinds` (Q11: las capturas que te afectan, también la que se pierde).

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | Cada caravana `IN_TRANSIT`: su unidad, su movimiento, su dueño, su destino y su carga |
| Write-through | La salida, en la transacción del trato; la captura (dueño, ciudad, vida, movimiento nuevo y eventos) y la pérdida, cada una en su transacción; la entrega, en una transacción con el delta del almacén del destino |
| Dirty-flag | Nada nuevo: el crédito de la llegada viaja con la entrega, que se reintenta en el volcado si se descarta |
| Reconstruible / no persistido | La posición en camino, de la polilínea; al arrancar se cargan las caravanas `IN_TRANSIT` con su movimiento, y las que llegaron durante la caída se entregan en el primer tick |

**Por qué hace falta un ADR ([ADR-037](../decisions/ADR-037-caravans-as-units.md)).** Cuatro decisiones estructurales:

1. **La caravana es un tipo de unidad**, no una entidad aparte. Hereda el movimiento, la recuperación, el interés por
   chunks, los golpes y el dibujo. Su carga va en una tabla aparte, `caravan_cargo`, porque `units.loot` es un
   entero de madera y una columna por recurso habría que tocarla con cada recurso nuevo.
2. **El trato carga en lugar de acreditar.** [INV-TRADE-001](../invariants/trade.md#inv-trade-001) pasa de «lo que
   sale de una ciudad entra en la otra en la misma transacción» a «lo que sale entra en las caravanas en la misma
   transacción, y lo que lleva una caravana entra entero en un almacén al llegar, o se pierde».
3. **La entrega es optimista** (la RAM acredita en el tick y la transacción confirma después), como la elaboración de
   [ADR-036](../decisions/ADR-036-civilization-goods.md). La alternativa en dos fases dejaría la caravana parada en el
   depósito hasta que vuelva la base, y la ventana de una doble entrega es la misma que ya está documentada.
4. **La captura es un cambio de dueño** de una unidad viva, que hoy no existe. La caravana deja de contar para nadie
   en la población (`RN-CARAVAN-003`), así que cambiar `units.player_id` y `units.city_id` no mueve el límite de
   ninguna ciudad.

Alternativas descartadas que el ADR deja escritas, entre otras: **una entidad aparte** (`caravans` con su propia posición, su
propio mensaje de mapa y su propio interés) duplica el movimiento y la recuperación; **acreditar al aceptar y dibujar
la caravana como adorno** no tiene riesgo, que es lo que el slice quiere; **matarla y repartir la carga como botín** pide
botín de seis recursos en el guerrero (Q5); **una ruta continua** (que se recalcula al moverse) es lo que reabre
`DEBT-04`, y aquí no hace falta.

**Migración `000017`.** `caravans` (`id`, `unit_id` con FK a `units` y `ON DELETE SET NULL`, `offer_id` con FK a
`trade_offers`, `sender_player_id`, `from_city_id`, `to_city_id` —el destino vigente—, `original_to_city_id`,
`status` con `CHECK` en `IN_TRANSIT`, `DELIVERED` y `LOST`, `departed_at` y `closed_at`), con un índice único parcial
de `unit_id` para las `IN_TRANSIT`; y `caravan_cargo (caravan_id, resource, amount)`, con `amount > 0`, el mismo
`CHECK` de recurso que `city_stocks` y la PK `(caravan_id, resource)`. `city_productions` no admite `CARAVAN` (no se
entrena). Las dos tablas se suman al `TRUNCATE` de los tests de integración; un recurso nuevo tocará también el `CHECK`
de `caravan_cargo`. Los trabajos nuevos de la cola (`caravan.deliver`, la entrega, y `caravan.capture`, la captura o la pérdida) se suman a
`simulation.JobNames`. La siguiente migración libre pasará a ser `000018`.

## 8. Invariantes

**Cita** [INV-ECON-001](../invariants/economy.md#inv-econ-001) (la entrega no deja nada en negativo),
[INV-ECON-002](../invariants/economy.md#inv-econ-002) (el débito del trato, en la misma transacción que la carga),
[INV-SEC-001](../invariants/security.md#inv-sec-001) (el cliente no decide la ruta, la llegada ni la captura),
[INV-UNIT-005](../invariants/units.md#inv-unit-005) (una caravana en camino tiene su movimiento `ACTIVE`),
[INV-COMBAT-001](../invariants/combat.md#inv-combat-001) (una caravana nunca se guarda con 0 de vida: la captura la
llena) e [INV-SAFE-004](../invariants/territory.md#inv-safe-004).

**Precisa**:

- [INV-TRADE-001](../invariants/trade.md#inv-trade-001): un trato nunca crea recursos; lo que sale de un almacén entra
  en una caravana en la misma transacción, y sólo la pérdida de una caravana capturada sin ruta los destruye.
- [INV-COMBAT-002](../invariants/combat.md#inv-combat-002): la protección offline cubre las unidades de su dueño salvo
  las caravanas.
- [INV-CITY-017](../invariants/city.md#inv-city-017): `population` cuenta las unidades vivas que no son caravanas.

**Asigna** tres invariantes nuevos de la familia `INV-TRADE`, registrados en [../invariants/trade.md](../invariants/trade.md):

- [INV-TRADE-005](../invariants/trade.md#inv-trade-005) — la carga de una caravana no cambia desde que sale hasta que
  se entrega o se pierde, ni siquiera al capturarla.
- [INV-TRADE-006](../invariants/trade.md#inv-trade-006) — una caravana se entrega como mucho una vez, y su carga entra
  en un solo almacén.
- [INV-TRADE-007](../invariants/trade.md#inv-trade-007) — toda caravana nace de un trato aceptado, y cada trato
  aceptado tiene exactamente dos.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | El catálogo de `CARAVAN` y su rasgo de velocidad; la ruta inversa como polilínea válida; cada fila de §5; la población sin caravanas; la captura (nuevo dueño, vida, carga, ruta) y la pérdida sin ruta; el predicado hostil con el dueño protegido; la vista con y sin carga según quién mira; `Classify` de los eventos nuevos para cada jugador |
| simulation | Golden `caravanas`: trato, salida, llegada de las dos caravanas y los dos almacenes en el tick exacto; captura por un tercero y llegada a su ciudad; recaptura por el destinatario; escolta que responde (E3); llegada y golpe mortal en el mismo tick (`RN-CARAVAN-007`); el tratado roto con caravanas en camino; caravana capturada sin ruta |
| recovery | Reinicio con caravanas en camino: siguen por su polilínea y llegan en su instante absoluto; reinicio con una que llegó durante la caída; reinicio con una entrega encolada y sin confirmar (como mucho un crédito) |
| integration | La transacción del trato con fallo inyectado (o los dos débitos y las dos caravanas, o nada); la entrega idempotente; la captura y su `units.player_id`; el `CHECK` de recurso y la PK de `caravan_cargo` |
| contract / e2e | `caravan.update`, `caravans[]`, `CARAVAN` y `CARAVAN_CAPTURED` contra el JSON Schema; un trato de punta a punta por WebSocket hasta la llegada, y una captura, en el mismo commit que `caravan.update` |
| cliente | La lista «Caravanas en camino», el dibujo y la ruta, «Capturar», el motivo sin ruta en el formulario; `scripts/smoke.mjs --caravana` (un trato de smoke_runner con smoke_victim por el `TRADE` vigente, la llegada medida contra la distancia y la civilización, y la captura de la caravana de smoke_victim por uno de los guerreros de smoke_runner que están junto a su ciudad) |

## 10. Decisiones de producto (2026-11-13)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de producto,
no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Viaja en caravana todo trato? | **Sí, todos.** Dos reglas (instantáneo cerca y caravana lejos, o caravana opcional) se explican peor, y la opcional nadie la elegiría: con la distancia como única diferencia, el riesgo desaparece. Entre vecinos la caravana tarda poco y casi no corre peligro, que es la ventaja de tener el socio cerca. |
| Q2 | ¿Qué es una caravana y quién la paga? | **Una unidad `CARAVAN` que nace sola del trato, gratis y sin población.** Alternativas: **entrenarla** (o construir antes un mercado) añade un coste y un edificio sin decisión nueva: el trato ya cuesta lo que das; **sacarla de la población** castigaría el comercio de las ciudades llenas y haría que un trato aceptado mientras no estás fallara por un límite que no controlas. |
| Q3 | ¿Cuántas caravanas por trato y cuánto lleva cada una? | **Una por sentido, sin tope de carga.** Un trato de 1 000 de piedra es una sola caravana muy apetecible: quien comercia mucho de golpe arriesga mucho de golpe, y eso lo decide él. Alternativa: **una capacidad** (p. ej. 100 por caravana) reparte el riesgo en varias, pero multiplica las unidades en el mapa y en el tick por cada trato grande. |
| Q4 | ¿Cuánto vive y a qué velocidad va? | **80 de vida y 1 200 ms por tile, el doble que un guerrero, con el rasgo de velocidad de su dueño.** Un guerrero solo la alcanza siempre y la captura en 15 s; dos, en 8. Entre 3 y 5 minutos de smoke_victim a smoke_runner, medio minuto entre vecinos. Son números de partida, sin datos. |
| Q5 | ¿Qué pasa cuando la matan? | **Se captura**: pasa al dueño del guerrero con la carga entera y va hacia su ciudad, y se puede recapturar por el camino. Es la intercepción de verdad: persecución, rescate y riesgo para el captor, que tiene que llevársela a casa. Alternativas: **se pierde la carga** (destruir sin ganar nada sólo sirve para hacer daño); **botín para el guerrero** hasta su carga (20 de madera, y el resto se pierde: casi nada, y pediría botín de seis recursos). |
| Q6 | ¿La captura se lleva también la piedra y los bienes? | **Sí, todo.** La piedra (Q8 de E10) y los bienes (E14) no se saquean del almacén, y eso no cambia: la caravana es la excepción deliberada, el riesgo del camino. Si la captura sólo diera madera, interceptar el comercio que importa (bienes para Hierro) no valdría nada. |
| Q7 | ¿Protege la protección offline a la caravana? | **No.** Si la protegiera, toda caravana de alguien desconectado sería intocable, y como un trato se acepta mientras el otro no está, buena parte de ellas lo serían. La consecuencia sigue acotada (pilar 3): sólo arriesgas lo que ofreciste, nunca la ciudad. Los pactos, las alianzas y las Safe Zones sí la protegen. |
| Q8 | ¿El dueño puede dirigir su caravana? | **No: ruta fija, la más corta, calculada al aceptar.** Desviarla a mano pediría órdenes, una ruta nueva en el tick por cada cambio y un cliente para dirigirla. La protección es la escolta: los guerreros del dueño que estén cerca responden solos a los golpes (E3). Una orden de escolta o de «seguir» queda fuera. |
| Q9 | ¿Qué ven los demás? | **La caravana, su dueño y su vida, pero no la carga.** Los tratos son privados (Q9 de E10); ver la carga de todas las caravanas los haría públicos. Quien intercepta apuesta: toda caravana lleva algo. Alternativa: **carga visible**, más legible para el que caza y peor para la privacidad. |
| Q10 | ¿Qué pasa con las caravanas si termina el tratado? | **Siguen y llegan.** Ya están pagadas; cortarlas sería perder mercancía por una ruptura que el otro decide. El preaviso de 15 minutos no cambia. |
| Q11 | ¿Qué entra en el registro y qué se avisa en vivo? | **Registro:** la caravana que te llegó, la tuya capturada, la que venía a ti capturada y la que se perdió. **Aviso en vivo:** sólo las capturas que te afectan, como las muertes de E11. La llegada no se avisa: el almacén ya cambia a la vista. |
| Q12 | ¿Se pueden mandar caravanas sin trato (regalos, a un aliado o entre ciudades propias)? | **No en este slice.** Hoy cada jugador tiene una ciudad, y un envío sin contrapartida es el regalo que la Q3 de E10 dejó a propósito difícil (las multicuentas). Las rutas permanentes y los envíos van con la fase Caravans completa. |

## 11. Documentos relacionados

- [trade.md](trade.md) (el trato y su transacción) y [../invariants/trade.md](../invariants/trade.md).
- [movement.md](movement.md) (la polilínea y su recuperación) y [unit.md](unit.md) (el catálogo de unidades).
- [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md) y [combat-pillage.md](combat-pillage.md)
  (golpes, escolta y botín).
- [civilization-goods.md](civilization-goods.md) (los bienes que hacen falta para Hierro).
- [../architecture/pathfinding.md](../architecture/pathfinding.md) y [../roadmap/backlog.md](../roadmap/backlog.md)
  (`DEBT-04`).
- [../roadmap/roadmap.md](../roadmap/roadmap.md) (la fase Caravans) y
  [../product/game-pillars.md](../product/game-pillars.md) (pilares 3, 4 y 5).
