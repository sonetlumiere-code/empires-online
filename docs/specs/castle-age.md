# Edad de los Castillos: el último avance de era y el castillo que defiende la ciudad

Propuesta para que `CASTLE_AGE`, que está en el catálogo de eras desde `000002` pero no se alcanza, tenga su avance
desde la Edad de Hierro, con un coste que vuelve a pedir comercio, y desbloquee algo más que población: el
**castillo**, un edificio que dispara a quien ataca la ciudad.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CASTLE-AGE` |
| Estado | Draft (2026-11-17). Pendiente de las decisiones de producto de §10; prohibido implementar contra ella hasta su aceptación. |
| Milestone | E23 Edad de los Castillos (vigesimotercer slice después de M7) |
| Canon | §0 (civilización como eje), §1.5 (determinismo), §10 (eras y población), §11 (catálogos), §12 (persistencia), §13 (protocolo), §16 (errores) |
| Depende de | [economy-stone.md](economy-stone.md), [civilization-goods.md](civilization-goods.md), [civilization-units.md](civilization-units.md), [economy-wood.md](economy-wood.md), [city.md](city.md), [trade.md](trade.md), [caravans.md](caravans.md), [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md), [combat-siege.md](combat-siege.md), [civilizations.md](civilizations.md), [factions.md](factions.md), [clans.md](clans.md), [clan-territories.md](clan-territories.md), [clan-alliances.md](clan-alliances.md), [presence.md](presence.md), [safe-zones.md](safe-zones.md), [event-log.md](event-log.md), [live-notices.md](live-notices.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) (encargos en dos fases y débito con su efecto), [ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md) (resolución simultánea en la fase 4), [ADR-029](../decisions/ADR-029-population-over-limit-and-house-destruction.md) (el asedio de un edificio), [ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md) (el avance de era como encargo con su fila), [ADR-035](../decisions/ADR-035-civilization-traits.md) (los rasgos en el punto de consumo), [ADR-036](../decisions/ADR-036-civilization-goods.md) (los bienes ajenos en cualquier mezcla), [ADR-039](../decisions/ADR-039-faction-peace-and-affinity.md) (la tierra afín) y [ADR-043](../decisions/ADR-043-civilization-units.md) (el combate que pregunta por capacidades). Propone el **ADR-044** (§7): un edificio que golpea sin ser una unidad, dentro de la resolución simultánea, y a quién se atribuyen sus muertes. |
| Reemplaza a | La Q11 de [civilization-goods.md](civilization-goods.md) y la Q6 de [economy-stone.md](economy-stone.md) («`CASTLE_AGE` sigue sin coste»); en [civilization-units.md](civilization-units.md) §2, «la Edad de los Castillos» del no-scope; y, en parte, la `Q-12` del [backlog](../roadmap/backlog.md) (cómo se llega a `CASTLE_AGE`). Precisaría `RN-ECON-024` (`NOT_IMPLEMENTED` pasa a responderse desde `CASTLE_AGE`), `RN-GOODS-006` (el avance a Castillos también cobra bienes ajenos), `RN-COMBAT-029` (el castillo es un objetivo de asedio más) y `RN-COMBAT-014` (un disparo del castillo no despierta la respuesta automática, §4.4). |
| Invariantes | Cita [INV-SEC-001](../invariants/security.md#inv-sec-001), [INV-ECON-002](../invariants/economy.md#inv-econ-002), [INV-ECON-004](../invariants/economy.md#inv-econ-004), [INV-ECON-005](../invariants/economy.md#inv-econ-005), [INV-ECON-010](../invariants/economy.md#inv-econ-010), [INV-ECON-011](../invariants/economy.md#inv-econ-011), [INV-CITY-017](../invariants/city.md#inv-city-017), [INV-CITY-018](../invariants/city.md#inv-city-018), [INV-COMBAT-001](../invariants/combat.md#inv-combat-001), [INV-COMBAT-002](../invariants/combat.md#inv-combat-002), [INV-COMBAT-006](../invariants/combat.md#inv-combat-006) e [INV-PLAYER-012](../invariants/player.md#inv-player-012); propone uno nuevo de la familia `INV-ECON` y uno de la familia `INV-COMBAT` (§8) |

## 1. Objetivo

Cerrar la escalera de eras del canon y darle a su último peldaño algo que se note en el mapa. Hoy una ciudad llega a
la Edad de Hierro pagando bienes de otras civilizaciones ([civilization-goods.md](civilization-goods.md)) y ahí se
queda: `city.advance` responde `NOT_IMPLEMENTED`, aunque `CASTLE_AGE` (límite 150) existe en la tabla `eras`. La
visión dice que cada era «amplía el techo de población y abre unidades y tecnologías»
([vision.md](../product/vision.md)), y hasta ahora sólo Bronce ha abierto algo (el taller y la unidad propia).

Este slice hace dos cosas: un **avance a la Edad de los Castillos** con el mismo encargo que los dos anteriores
([ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md)), que vuelve a pedir bienes ajenos para que el
comercio siga siendo necesario al final de la progresión; y el **castillo**, un edificio de la zona urbana que
defiende la ciudad golpeando a las unidades que la atacan. Hoy una ciudad sólo se defiende con las unidades que su
dueño tiene cerca; el castillo es la primera defensa que no depende de tener un ejército en casa, y hace que asediar
o saquear una ciudad avanzada pida algo más que un guerrero suelto.

## 2. Scope

**Entra.** El avance de `IRON_AGE` a `CASTLE_AGE` con `city.advance`, con su coste en madera, piedra y bienes ajenos
(Q1) y su plazo (Q2); el límite de población de 150 que ya da la era; el castillo (Q3, Q4): su construcción con
`city.build`, su vida, su asedio y su disparo (Q5); su encaje con la tierra afín (Q6), con la paz de los tratados, la
Orden, los clanes y las alianzas de clanes, y con la protección offline (Q7); la migración `000025`; en el protocolo,
`CASTLE` en `BuildingType` y el objetivo del disparo en la vista del edificio, sin mensajes nuevos; en el cliente, el
avance a Castillos con su motivo, el botón «Castillo», su dibujo, su disparo y los textos del registro y los avisos
(Q8); un `scripts/smoke.mjs --castillo` (Q9).

**No entra** (`Fuera de MVP` de este slice): eras posteriores a `CASTLE_AGE` (Q10); mejorar o reconvertir las
unidades de civilización, una segunda unidad o una unidad de asedio (Q3); tecnologías o investigación; que el castillo
dé población, guarnición con disparos extra, o defienda a otros jugadores (Q5, Q7); murallas con vida o torres fuera
de la zona urbana; ampliar la zona urbana; ayuda del clan o de los aliados para pagar el avance (Q7); que el castillo
haga afín su entorno (Q6); reequilibrar con datos de jugadores.

## 3. Actores

Jugador (avanza, construye el castillo), los demás jugadores (le venden los bienes, lo atacan y reciben sus disparos),
cliente web (botones, dibujo, disparo, textos), Game Server (cobra el avance, valida la obra y decide a quién dispara
el castillo, [INV-SEC-001](../invariants/security.md#inv-sec-001)), tick loop (fase 2 para las órdenes, fase 4 para
los disparos y los golpes de asedio, fase 5 para el fin del avance y de la obra), PostgreSQL (el avance, el castillo,
los sucesos).

## 4. Modelo

### 4.1 El avance a la Edad de los Castillos

`city.advance` desde la Edad de Hierro pasa a la **Edad de los Castillos**, con el mismo encargo que los dos avances
anteriores ([ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md) §4: fila en `city_era_advances`, débito con
su efecto, fin optimista en la fase 5) y estos números (Q1, Q2):

- **1 600 de madera, 800 de piedra y 120 bienes ajenos**, en cualquier mezcla de los bienes de otras civilizaciones,
  resueltos como en Hierro: en el orden del catálogo de civilizaciones, saltándose el propio, hasta sumar 120
  (`RN-GOODS-006`, [ADR-036](../decisions/ADR-036-civilization-goods.md) §5). El propio nunca cuenta
  ([INV-ECON-011](../invariants/economy.md#inv-econ-011)).
- Dura **360 s**. Los rasgos de civilización no lo tocan, como a los avances anteriores (Q4 de
  [civilizations.md](civilizations.md)).
- Mientras dura, la ciudad no entrena (`RN-ECON-026`); el taller sigue elaborando y se puede construir (`RN-GOODS-005`).
- Al llegar, el límite de población sube de 100 a 150, el `population_cap` de `CASTLE_AGE` que existe desde `000002`
  ([INV-ECON-004](../invariants/economy.md#inv-econ-004)); la era no retrocede nunca
  ([INV-CITY-018](../invariants/city.md#inv-city-018)).

La escalera queda así: cada avance cuesta el doble que el anterior en madera y piedra y el doble en plazo, y desde
Hierro pide bienes de otros:

| Paso | Madera | Piedra | Bienes ajenos | Plazo | Límite |
|---|---|---|---|---|---|
| Piedra → Bronce | 400 | 200 | — | 90 s | 20 → 50 |
| Bronce → Hierro | 800 | 400 | 60 | 180 s | 50 → 100 |
| **Hierro → Castillos** | **1 600** | **800** | **120** | **360 s** | **100 → 150** |

120 bienes son 24 elaboraciones de quien los produce (doce minutos de taller, 720 de madera y 360 de piedra), así que
el avance pide un socio comercial estable o varios, no un trato suelto. Con cualquier mezcla basta con una sola
civilización ajena: un mundo donde falten dos civilizaciones no bloquea a nadie (Q1).

Desde la Edad de los Castillos, `city.advance` responde `NOT_IMPLEMENTED`: no hay más eras en el catálogo (Q10).

### 4.2 El castillo

El castillo es un edificio de la zona urbana, como la casa y el taller ([economy-wood.md](economy-wood.md) §4.5,
[civilization-goods.md](civilization-goods.md) §4.2), con estas reglas (Q4):

- Se construye con `city.build { type: "CASTLE" }` en un tile libre de la zona urbana: ocupa uno de los ocho huecos,
  así que una ciudad con taller y castillo tiene sitio para seis casas (límite máximo 150 + 30 = 180).
- Pide la **Edad de los Castillos**.
- **Uno por ciudad** en pie (en obra o completo); si se derriba, se puede volver a construir.
- Cuesta **500 de madera y 800 de piedra** y tarda **180 s**, con el rasgo `production.speed` de E13 (163 636 ms un
  romano, 189 474 un persa, 200 000 un nórdico).
- No da población ([INV-ECON-004](../invariants/economy.md#inv-econ-004) no cambia).
- Tiene **1 000 de vida**. El rasgo bizantino `building.house_hp` es de la casa y no se le aplica.
- Se asedia como una casa o un taller ([combat-siege.md](combat-siege.md)): el daño se olvida tras
  `EO_SIEGE_DAMAGE_RESET_SECONDS` sin golpes y con un reinicio, no hay reparación, y derribarlo no devuelve nada.

### 4.3 El disparo

Un castillo **completo** golpea a las unidades que atacan a su dueño (Q5). En cada tick, en la fase 4 y con el estado
al inicio de la fase, como los golpes de las unidades (`RN-COMBAT-007`, [ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md)):

- **A quién.** Es **objetivo** una unidad viva, en el mapa, a distancia de Chebyshev **≤ 5** del tile del castillo, que
  en ese momento está **atacando** a una unidad del dueño del castillo, **saqueando** su almacén o **derribando** uno de
  sus edificios (también el propio castillo). Ver o pasar cerca no basta: un guerrero ajeno que cruza junto a la
  muralla sin atacar no recibe nada, como en la respuesta automática (`RN-COMBAT-014`).
- **Nunca** a una unidad cuyo dueño está en paz con el dueño del castillo (tratado `NON_AGGRESSION` o `ALLIANCE`, la
  Orden, el mismo clan o su paz residual, una alianza de clanes: el predicado `AtPeace`), ni a una unidad oculta, en
  una Safe Zone o de un jugador `PROTECTED` ([INV-COMBAT-002](../invariants/combat.md#inv-combat-002),
  [INV-COMBAT-006](../invariants/combat.md#inv-combat-006)). En la práctica, una unidad que cumple todo eso no podría
  estar atacando, pero el castillo lo comprueba igual.
- **Cuánto.** **10 de daño cada 2 000 ms**, a un solo objetivo por disparo: el más cercano al castillo (Chebyshev) y,
  a igual distancia, el de menor `id`. Sin azar.
- **Cuándo.** El primer disparo cae en el primer tick en que hay objetivo y el castillo está listo; después, cada
  2 000 ms mientras siga habiendo objetivo. Sin objetivo, el castillo queda listo para disparar en cuanto lo haya.
- **Sin dueño presente.** El castillo dispara aunque su dueño no esté conectado: es una defensa, no un ataque, y
  `INV-COMBAT-005` (el atacante presente) es del saqueo y del asedio. Si el dueño pasa a `PROTECTED`, nadie puede
  atacarle y el castillo se queda sin objetivos.
- **Muerte.** Un disparo que deja la vida en 0 mata a la unidad con las reglas de siempre (`RN-COMBAT-009`,
  [INV-COMBAT-001](../invariants/combat.md#inv-combat-001)), pero su botín se pierde: el castillo no carga ni deposita
  nada.

Con esos números, un guerrero solo (60 de vida) cae en seis disparos, 10 s después del primero, cuando lleva unos siete
golpes de asedio sobre una casa de 150 (`RN-COMBAT-031`): un guerrero suelto no derriba nada en una ciudad con
castillo. Seis guerreros juntos derriban el castillo (1 000 de vida) en unos 40 s, perdiendo tres por el camino. El
legionario cae con el undécimo disparo; el arquero a caballo, que asedia desde tres tiles, sigue al alcance del
castillo, porque ningún tile de la zona urbana está a más de dos de otro.

### 4.4 Lo que el castillo no cambia

- **La respuesta automática.** Un disparo del castillo no despierta a las unidades del jugador golpeado: no hay unidad
  a la que responder (`RN-COMBAT-014` sigue hablando de golpes de unidades). Quien quiera callar el castillo lo asedia
  con `unit.demolish`.
- **Los golpes de las unidades.** Ningún perfil cambia: ni el guerrero ni las unidades de civilización de E22 ganan
  nada en la Edad de los Castillos (Q3).
- **La tierra afín** (Q6). Un disparo del castillo es un golpe más para `RN-FAC-008`: una unidad golpeada en tierra
  afín a su dueño recibe 8 en vez de 10. El castillo no hace afín su entorno ni recibe menos daño en la propia tierra
  (los edificios no se benefician de la afinidad, Q6 de [factions.md](factions.md)).
- **La conquista.** El castillo no protege territorios: la capital ya no se puede reclamar (`RN-CONQ-002`) y el resto
  de territorios queda, en general, fuera de su alcance.

### 4.5 Clanes y alianzas

El avance se paga **solo**, con el almacén de la ciudad (Q7): ni el clan ni los aliados contribuyen. Lo que sí hacen es
lo de siempre: los bienes ajenos llegan por tratos con un tratado `TRADE` ([trade.md](trade.md)) y viajan en caravanas
([caravans.md](caravans.md)), y los compañeros de clan y los clanes aliados los firman entre sí como cualquiera
(`RN-CLAN-010`).

El castillo defiende **lo de su dueño** y nada más: no dispara a quien ataca a un compañero de clan o a un aliado,
aunque esté a su alcance, y nunca dispara a quien está en paz con su dueño. Eso mantiene la regla de la respuesta
automática (cada uno defiende lo suyo) y evita que un castillo arrastre a su dueño a la guerra de otro.

### 4.6 Reglas

| ID | Regla |
|---|---|
| `RN-CASTLE-001` | `city.advance` desde `IRON_AGE` pasa a `CASTLE_AGE` cobrando madera, piedra y bienes ajenos en cualquier mezcla, nunca el propio, con las reglas de `RN-ECON-024` a `RN-ECON-028`; desde `CASTLE_AGE` responde `NOT_IMPLEMENTED`. |
| `RN-CASTLE-002` | El castillo pide la Edad de los Castillos, un tile libre de la zona urbana y que la ciudad no tenga otro en pie; si no, `INVALID_TARGET`. |
| `RN-CASTLE-003` | El castillo cobra su coste al confirmarse el encargo, con su efecto, y aplica `production.speed` a su obra; no da población. |
| `RN-CASTLE-004` | Un castillo completo golpea, en la fase 4 y con la resolución simultánea, a una sola unidad por disparo: la más cercana de las que, a su alcance, atacan a una unidad de su dueño, saquean su almacén o derriban uno de sus edificios; a igual distancia, la de menor `id`. |
| `RN-CASTLE-005` | El castillo nunca golpea a una unidad de un jugador en paz con su dueño, oculta, en una Safe Zone o de un jugador `PROTECTED`. |
| `RN-CASTLE-006` | Un disparo del castillo no despierta la respuesta automática, no carga botín y aplica la reducción de la tierra afín como cualquier golpe. |
| `RN-CASTLE-007` | El castillo se asedia como cualquier edificio de la ciudad; derribado, deja de disparar en el tick en que cae y se puede volver a construir. |

### 4.7 Qué ve el jugador

- **Avance.** El botón de avanzar en una ciudad de Hierro dice «Avanzar a la Edad de los Castillos» y, deshabilitado,
  qué falta, también cuántos bienes ajenos («Faltan 40 bienes de otras civilizaciones»). En Castillos, «No hay más
  eras».
- **Castillo.** Un botón «Castillo» como «Taller», deshabilitado con su motivo (falta la Edad de los Castillos, ya
  hay uno, sin hueco, sin recursos), y su cuenta atrás mientras se construye.
- **En el mapa** (Q8). El castillo se dibuja en su tile con la geometría de PixiJS, como la casa y el taller, sin
  imágenes nuevas: un cuerpo de piedra con almenas y dos torres, del color de propio o ajeno, con la barra de vida
  cuando está dañado. Cuando dispara, una línea breve del castillo a su objetivo. Todos lo ven igual: saber que el
  vecino tiene castillo es información para decidir.
- **Registro y avisos.** Las muertes que causa el castillo entran en el registro y en los avisos de los dos lados:
  «Tu castillo mató a un guerrero de Fulano» y «El castillo de Fulano mató a tu berserker». Su derribo entra como el
  de cualquier edificio.

## 5. Errores

Sin códigos nuevos (canon §16):

| Orden | Condición | Código |
|---|---|---|
| `city.advance` desde Hierro | La ciudad no es propia | `CITY_NOT_FOUND` |
| | Ya entrena o avanza | `CITY_BUSY` |
| | Falta madera, piedra o bienes ajenos | `INSUFFICIENT_RESOURCES` |
| `city.advance` desde Castillos | No hay más eras | `NOT_IMPLEMENTED` |
| `city.build` `CASTLE` | La ciudad no es propia | `CITY_NOT_FOUND` |
| | Antes de la Edad de los Castillos, o ya hay un castillo en pie | `INVALID_TARGET` |
| | El tile no es libre o no es de la zona urbana | `INVALID_TARGET` |
| | Falta madera o piedra | `INSUFFICIENT_RESOURCES` |
| `unit.demolish` sobre un castillo | Las de siempre (`RN-COMBAT-030`) | Sin cambio |

El disparo no responde a ninguna orden, así que no tiene errores; lo que no puede golpear, no es objetivo.

## 6. Eventos y contratos de red

**Eventos.** El avance reutiliza `EraAdvanceOrdered` y `EraAdvanced`, y el castillo los eventos de los edificios
(encargo, fin de obra, derribo). Las muertes que causa el castillo son un `UnitKilled` con el castillo como autor y su
dueño como contraparte (`counterpart_player_id` y `atMs`, [ADR-028](../decisions/ADR-028-event-log-from-world-events.md)),
clasificadas en el registro y en los avisos de los dos lados; el ADR-044 decide si van como `UnitKilled` con un autor
edificio o como un tipo propio, y con él `eventlog.EventTypes`, `Classify`, `eventlog.NoticeKinds` y los textos del
cliente. Cada disparo que no mata no es un suceso, como los golpes de las unidades.

**WebSocket (protocolo v1).** `BuildingType` gana `CASTLE`: es un valor nuevo en un enum estricto (AGENTS.md §4.2),
rompe a un cliente viejo y se estrena junto en cliente y servidor. `BuildingView` gana un campo opcional,
`targetUnitId` (`EntityId | null`), con la unidad a la que apunta el castillo, que viaja en `building.update` cuando
cambia; es aditivo (`R-VER-2`). `city.advance` no cambia (ya admite la ciudad de Hierro) y `advance.era` ya admite
`CASTLE_AGE` (`EraCode` lo tiene desde M2). No hay mensajes nuevos: siguen los 44 cliente→servidor y los 31
servidor→cliente.

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | El avance en curso y el castillo, como los demás edificios; el próximo disparo de cada castillo y su objetivo; el daño del castillo |
| Write-through | El encargo del avance con su débito de madera, piedra y bienes, y su fin, como en Hierro; el encargo del castillo con su débito, su fin de obra y su derribo; las muertes que causa, con su suceso |
| Dirty-flag | La vida de las unidades golpeadas, como cualquier golpe de E2 |
| Reconstruible / no persistido | El instante del próximo disparo (tras un reinicio, el castillo está listo); su objetivo, que sale del estado de ataque de las unidades; el daño del castillo, que un reinicio olvida como el de una casa (`RN-COMBAT-032`) |

**Por qué hace falta un ADR (ADR-044).** El castillo es lo primero que golpea sin ser una unidad, y eso toca tres
decisiones estructurales que conviene dejar escritas con sus alternativas:

1. **El castillo es un atacante de la resolución simultánea, no una unidad.** Sus disparos se calculan con el estado
   al inicio de la fase 4, junto a los golpes de las unidades, y se aplican con ellos en un orden fijo (los de las
   unidades por `id` de atacante, después los de los castillos por `id` de edificio, cada uno sobre la vida que dejó
   el anterior). Descartado: modelarlo como una unidad inmóvil del catálogo (una fila en `units` con su tipo), que
   reutilizaría el combate pero mezclaría lo que es un edificio (hueco de la zona urbana, asedio, población) con lo que
   es una unidad (estado, movimiento, guarnición, población consumida).
2. **El objetivo sale del estado de ataque**, no de una lista de hostiles: el castillo mira a quién apunta cada unidad
   (`ATTACKING`, saqueo, asedio) y no necesita su propio predicado de hostilidad más allá de `AtPeace` y
   [INV-COMBAT-002](../invariants/combat.md#inv-combat-002). Descartado: disparar a toda unidad armada sin paz a su
   alcance, que convierte cada castillo en un peaje para quien pasa (Q5).
3. **A quién se atribuyen sus muertes**: el suceso lleva el edificio como autor y su dueño como contraparte, para el
   registro y los avisos.

**Migración `000025`:**

- `CASTLE` en `buildings_type_valid`.
- Un único parcial de un castillo en pie por ciudad, como el del taller de `000016`
  ([INV-ECON-010](../invariants/economy.md#inv-econ-010)).
- El avance a `CASTLE_AGE` no necesita esquema: `city_era_advances` admite cualquier era del catálogo, y `eras` ya
  tiene `CASTLE_AGE` con su `population_cap` de 150.

Ninguna tabla nueva, así que el `TRUNCATE` de los tests de integración no cambia. El `down` borra los castillos (en
pie o derribados) y los avances a `CASTLE_AGE` en curso, que un servidor anterior no reconocería como paso del
catálogo, y deja en `CASTLE_AGE` las ciudades que ya llegaron: un servidor anterior las carga sin avance posible.

## 8. Invariantes

**Cita** [INV-SEC-001](../invariants/security.md#inv-sec-001) (el cliente pide avanzar o construir; el servidor decide
y elige el objetivo del castillo), [INV-ECON-002](../invariants/economy.md#inv-econ-002) (el avance y el castillo
debitan con su efecto), [INV-ECON-004](../invariants/economy.md#inv-econ-004) (el límite es el de la era más las casas;
el castillo no suma), [INV-ECON-005](../invariants/economy.md#inv-econ-005) (el avance y la obra a medias terminan en
su instante tras un reinicio), [INV-ECON-010](../invariants/economy.md#inv-econ-010) (el taller, que el castillo imita),
[INV-ECON-011](../invariants/economy.md#inv-econ-011) (que en la aceptación se precisaría para cubrir también el
avance a Castillos), [INV-CITY-017](../invariants/city.md#inv-city-017) (el avance no cuenta como población),
[INV-CITY-018](../invariants/city.md#inv-city-018) (de Hierro a Castillos, nunca hacia atrás),
[INV-COMBAT-001](../invariants/combat.md#inv-combat-001), [INV-COMBAT-002](../invariants/combat.md#inv-combat-002) e
[INV-COMBAT-006](../invariants/combat.md#inv-combat-006) (los disparos del castillo son golpes y respetan lo mismo) e
[INV-PLAYER-012](../invariants/player.md#inv-player-012) (el rasgo de producción sobre la obra del castillo).

**Propone** dos invariantes nuevos, que se registrarían sin cobertura en la aceptación:

- Familia `INV-ECON`: una ciudad tiene como mucho un castillo en pie, y sólo una ciudad en la Edad de los Castillos lo
  tiene (dominio en el predicado de la obra, único parcial de `000025` en la base).
- Familia `INV-COMBAT`: un castillo sólo golpea a una unidad viva, a su alcance, que estaba atacando, saqueando o
  derribando algo de su dueño, y nunca a la de un jugador en paz con él (dominio en la elección del objetivo,
  comprobado en la simulación).

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | El paso Hierro → Castillos del catálogo de avances (coste, 120 ajenos, 360 s) y `NOT_IMPLEMENTED` desde Castillos; la resolución de 120 bienes ajenos con mezclas que llegan y que no, sin el propio; el predicado del castillo con cada fila de §5; la elección del objetivo: el más cercano, el desempate por `id`, quien no ataca no es objetivo, quien ataca a otro jugador tampoco, y cada exclusión de `RN-CASTLE-005` |
| simulation | Golden «castillo», con los ticks exactos: un guerrero que asedia una casa recibe el primer disparo en el tick en que empieza y muere con el sexto, a los 10 s; dos atacantes a distinta distancia (cae primero el cercano); un saqueador junto a la muralla; un arquero a caballo que asedia desde tres tiles; un guerrero ajeno que pasa sin atacar no recibe nada; uno en paz no es objetivo; el disparo en tierra afín del golpeado resta 8; el disparo no despierta la respuesta automática; seis guerreros derriban el castillo, que mata a tres, y deja de disparar en ese tick; el avance a Castillos cobra 120 ajenos y no el propio, no entrena mientras dura y a los 360 s sube el límite a 150; la obra del castillo con `production.speed` (163 636 ms romano, 200 000 nórdico); el catálogo de comandos y de trabajos |
| integration | `000025` arriba y abajo; el único parcial rechaza un segundo castillo en pie y admite uno tras derribar el anterior (y se comprueba por mutación quitando el índice); el encargo del castillo debita en una transacción y no deja nada si falla; el cobro del avance a Castillos con varios bienes; la muerte por el castillo escribe su suceso con la contraparte |
| recovery | Un avance a Castillos a medias termina en su instante tras un reinicio; una obra de castillo a medias, también; un castillo completo se carga entero y listo para disparar |
| contract / e2e | `CASTLE` en `BuildingType` y `targetUnitId` en `BuildingView` contra el JSON Schema (Go y Zod); `city.advance` desde Hierro y desde Castillos por WebSocket; `city.build` `CASTLE` antes de tiempo (`INVALID_TARGET`) y en Castillos; un castillo que dispara a un atacante y su `building.update` con el objetivo |
| cliente | El motivo del avance a Castillos (también los bienes que faltan) y «No hay más eras»; el botón del castillo con cada motivo; el dibujo del castillo y la línea del disparo con y sin objetivo; los textos del registro y de los avisos de los dos lados |
| humo | `scripts/smoke.mjs --castillo`, con su función exportada y su test en `scripts/smoke.test.mjs` (Q9) |

## 10. Decisiones de producto (pendientes)

Preguntas abiertas. Cada una lleva la recomendación (en negrita) y sus alternativas. Son decisiones de producto, no de
ingeniería: la spec no se acepta hasta tenerlas.

| # | Pregunta | Recomendación |
|---|---|---|
| Q1 | ¿Qué cuesta el avance en bienes: propios, ajenos, cuántos? | **120 bienes ajenos en cualquier mezcla, sin el propio**: el doble que Hierro y con la misma regla. Mantiene la lógica de E14 (para progresar hay que comerciar) sin crear un bloqueo nuevo: con un solo socio de otra civilización se llega, y el código ya sabe resolverlo (`ForeignGoods`). 120 bienes son 24 elaboraciones del vendedor: hace falta un socio estable o varios. Alternativas: **ajenos de al menos dos civilizaciones distintas** (por ejemplo, 60 de cada una de dos), que obliga a tratar con más gente pero bloquea a todos en un mundo con sólo dos civilizaciones; **también bienes propios** (por ejemplo, 120 ajenos y 40 propios), que da al bien propio un tercer uso pero rompe la Q8 de E14 («el bien propio no sirve para el propio avance») y compite con la unidad de E22; **los mismos 60 que Hierro**, que abarata el último paso. |
| Q2 | ¿Cuánta madera y piedra, y cuánto tarda? | **1 600 de madera, 800 de piedra y 360 s, sin entrenar mientras dura.** Es la escalera de los dos avances anteriores (cada paso, el doble de recursos y de plazo), fácil de explicar y de equilibrar después en el catálogo. Seis minutos sin entrenar son el riesgo de avanzar: una ciudad que avanza no repone su ejército. Alternativas: **240 s**, para que el último paso no sea el más largo de esperar; **sin bloquear el entrenamiento** en este avance, que rompe una regla común a los tres (`RN-ECON-026`). |
| Q3 | ¿Qué desbloquea además del límite de población? | **Un edificio, el castillo, que defiende la ciudad (§4.2, §4.3), y nada más.** Es lo que el nombre de la era promete y lo que hoy le falta al juego: una defensa que no dependa de tener un ejército en casa, que hace que asediar o saquear una ciudad avanzada pida una fuerza de verdad. Un solo desbloqueo mantiene el slice en una mecánica nueva. Alternativas: **una mejora de élite de las unidades de civilización** (más vida o daño para las entrenadas en Castillos), barata en reglas pero que pide cuatro perfiles más o un estado nuevo por unidad, y más sentido tras ver cómo se juegan las de E22; **una segunda unidad** (por ejemplo, un ariete común que sólo golpea edificios), que añade una capacidad nueva al combate; **sólo población**, como Hierro, que repite el paso con números más grandes (la Q6 de E9 ya lo descartó). |
| Q4 | ¿Cómo es el castillo? | **Pide Castillos, uno por ciudad, ocupa un hueco de la zona urbana, 500 de madera y 800 de piedra, 180 s con `production.speed`, sin población, 1 000 de vida y se asedia como una casa.** La piedra pesa más que la madera para que la geografía (las colinas) vuelva a contar; uno por ciudad evita amontonar defensas; ocupar un hueco obliga a elegir entre castillo y una casa más (cinco plazas). El rasgo bizantino de vida de las casas no se le aplica. Alternativas: **sin ocupar hueco** (en el centro de la ciudad), que no cuesta población pero cambia `RN-ECON-007`; **más vida y sin asedio posible**, que hace inexpugnable a la ciudad y deja el saqueo sin respuesta; **que el rasgo bizantino también le dé vida**. |
| Q5 | ¿Qué hace el castillo; cambia algo del combate? | **Dispara 10 de daño cada 2 s, a un objetivo, hasta 5 tiles, sólo a las unidades que están atacando a una unidad de su dueño, saqueando su almacén o derribando uno de sus edificios; nunca a quien está en paz con su dueño, protegido, oculto o en una Safe Zone. No despierta la respuesta automática y no carga botín. Ninguna otra regla de combate cambia.** Es una defensa que reacciona a un ataque, como la respuesta automática de E3, y por eso no castiga a quien pasa por el camino, ni a las caravanas, ni a los aldeanos. Un guerrero suelto muere antes de derribar una casa; seis derriban el castillo en unos 40 s, perdiendo tres. Es la única mecánica nueva del slice (un edificio que golpea, ADR-044). Alternativas: **dispara a toda unidad armada sin paz a su alcance**, como una torre clásica, que hace del entorno de cada ciudad un peaje y provoca conflictos que nadie inició; **un castillo pasivo** (las unidades propias reciben menos daño a su alrededor, con el mecanismo de la tierra afín), que no estrena nada pero se nota poco; **más disparos con unidades en guarnición**, que liga castillo y guarnición pero pide decidir qué cuenta una unidad aliada guarnecida. |
| Q6 | ¿Cambia algo de la tierra afín? | **No.** El disparo del castillo es un golpe más para `RN-FAC-008` (una unidad en tierra afín a su dueño recibe 8 en vez de 10), el castillo no hace afín su entorno y, como edificio, no recibe menos daño en la propia tierra. La afinidad sigue siendo una regla de bando y el castillo una de era. Alternativas: **el castillo hace afín su entorno** (por ejemplo, 5 tiles) para su dueño, que suma una tercera fuente de afinidad después del bando y del clan; **los edificios también reciben menos daño en tierra afín**, que cambia la Q6 de E17. |
| Q7 | ¿Cómo encaja con los clanes y las alianzas? | **El avance se paga solo, y el castillo defiende sólo lo de su dueño.** Los bienes llegan por los tratos de siempre, que compañeros de clan y clanes aliados firman entre sí (`RN-CLAN-010`); y el castillo no dispara a quien ataca a un compañero o a un aliado, ni a nadie en paz con su dueño. Así cada uno defiende lo suyo, como en la respuesta automática, y un castillo no arrastra a su dueño a la guerra de otro. Alternativas: **contribuciones del clan al avance** (los miembros aportan madera, piedra o bienes), que crea una transferencia sin trato ni caravana y salta E10 y E15; **el castillo defiende también a los compañeros de clan y a los clanes aliados** a su alcance, más cooperativo pero que mete al dueño en peleas ajenas y pide decidir qué pasa con la paz residual. |
| Q8 | ¿Cómo se ve en el cliente? | **El castillo dibujado con la geometría de PixiJS en su tile (cuerpo de piedra, almenas y dos torres), del color de propio o ajeno, con su barra de vida cuando está dañado y una línea breve hacia su objetivo cuando dispara; el botón «Castillo» y el avance con sus motivos; las muertes que causa en el registro y en los avisos de los dos lados.** Sin imágenes nuevas, como las siluetas de E22. La línea del disparo es lo que explica por qué muere una unidad junto a una ciudad, y pide el campo `targetUnitId` en la vista del edificio. Alternativas: **sin línea de disparo** (sólo la vida que baja y el registro), sin campo nuevo en el protocolo pero más difícil de entender en vivo; **sprites ilustrados**, que piden un sistema de recursos gráficos que el cliente aún no tiene. |
| Q9 | ¿Hace falta un humo nuevo? | **Sí, `--castillo`**: la ciudad del humo avanza lo que le falte hasta Castillos (el avance es irreversible, como `--avance`, y una pasada que ya está en Castillos se lo salta), construye el castillo y comprueba que dispara a un guerrero de `smoke_victim` que saquea la ciudad. La semilla (`EO_DEVSEED_STOCK_FOR`) se amplía para dejar en el almacén lo de los avances que falten, también los bienes ajenos, para no depender de una sesión de comercio. La primera pasada tarda unos doce minutos (Hierro, Castillos y la obra); las siguientes, sólo la prueba del disparo. Alternativas: **sin humo nuevo**, con los e2e por WebSocket y la prueba en el navegador, más barato pero sin ver el disparo en el servidor de verdad; **plazos acortados por una variable `EO_`** sólo para el humo, que acelera la pasada pero añade una configuración que no existe para los otros avances. |
| Q10 | ¿Más eras después de Castillos? | **No: `CASTLE_AGE` es la última del catálogo**, y `city.advance` desde ella responde `NOT_IMPLEMENTED` («No hay más eras» en el cliente). Una era más pide antes decidir qué abriría, y eso es otra spec. |

## 11. Documentos relacionados

- [economy-stone.md](economy-stone.md) (el primer avance y `RN-ECON-024` a `RN-ECON-028`),
  [civilization-goods.md](civilization-goods.md) (el avance a Hierro, los bienes ajenos y el taller) y
  [civilization-units.md](civilization-units.md) (lo que abrió la Edad de Bronce).
- [city.md](city.md) (la zona urbana y el límite por era), [economy-wood.md](economy-wood.md) (los edificios).
- [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md) y
  [combat-siege.md](combat-siege.md) (los golpes, la respuesta, el saqueo y el asedio).
- [factions.md](factions.md) (la tierra afín y la Orden), [clans.md](clans.md),
  [clan-alliances.md](clan-alliances.md) y [diplomacy.md](diplomacy.md) (la paz).
- [trade.md](trade.md) y [caravans.md](caravans.md) (cómo llegan los bienes ajenos).
- [event-log.md](event-log.md) y [live-notices.md](live-notices.md) (dónde se ven sus muertes).
- [../product/vision.md](../product/vision.md) (las eras) y [../roadmap/backlog.md](../roadmap/backlog.md) (`Q-12`).
