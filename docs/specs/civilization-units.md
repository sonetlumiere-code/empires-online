# Unidades de civilización: una unidad propia por civilización, sobre el combate que ya existe

Propuesta para que cada civilización tenga una unidad que sólo ella entrena: un tipo más del catálogo de unidades, con
su perfil de combate, que se entrena como el guerrero a partir de una edad y paga parte de su coste con el bien de su
civilización.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CIVILIZATION-UNITS` |
| Estado | Implemented (2026-11-17). Propuesta en `Draft` y aceptada el mismo día, con la recomendación de cada pregunta de §10; en `main` desde la fusión `6fde18f`, con la CI en verde. |
| Milestone | E22 Unidades de civilización (vigesimosegundo slice después de M7) |
| Canon | §0 (civilización como eje), §1.5 (determinismo), §11 (catálogos), §12 (persistencia), §13 (protocolo), §16 (errores) |
| Depende de | [unit.md](unit.md), [economy-wood.md](economy-wood.md), [economy-stone.md](economy-stone.md), [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md), [combat-siege.md](combat-siege.md), [territory-conquest.md](territory-conquest.md), [caravans.md](caravans.md), [civilizations.md](civilizations.md), [civilization-goods.md](civilization-goods.md), [factions.md](factions.md), [clan-territories.md](clan-territories.md), [clan-alliances.md](clan-alliances.md), [garrison.md](garrison.md), [event-log.md](event-log.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) (los encargos en dos fases y el débito con su efecto), [ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md) (la resolución simultánea), [ADR-035](../decisions/ADR-035-civilization-traits.md) (los rasgos en enteros por mil, aplicados al dar la orden), [ADR-036](../decisions/ADR-036-civilization-goods.md) (el bien de cada civilización, dato del catálogo `civilizations`) y [ADR-039](../decisions/ADR-039-faction-peace-and-affinity.md) (la tierra afín, calculada en cada golpe). [ADR-043](../decisions/ADR-043-civilization-units.md) (§7): la unidad de civilización como tipo del catálogo ligado por `civilizations.unit`, con requisito de edad y coste en varios recursos, y el combate que pregunta por capacidades y no por el tipo `WARRIOR`. |
| Reemplaza a | En [civilizations.md](civilizations.md) §2, «capacidades exclusivas por civilización —una unidad…» del no-scope; en [civilization-goods.md](civilization-goods.md) §2, «unidades… exclusivos además del bien (Q1)». Precisa `RN-ECON-010` (el entrenamiento con requisito de edad y de civilización, y coste en varios recursos), Q8 de [civilization-goods.md](civilization-goods.md) (el bien propio gana un segundo uso, Q5), `RN-COMBAT-001` y `RN-COMBAT-012` (atacar y responder lo hace toda unidad que ataca, no sólo `WARRIOR`) y, con ellas, las reglas de saqueo, asedio, conquista y captura que nombran al guerrero (§4.5). |
| Invariantes | Cita [INV-SEC-001](../invariants/security.md#inv-sec-001), [INV-ECON-002](../invariants/economy.md#inv-econ-002), [INV-ECON-005](../invariants/economy.md#inv-econ-005), [INV-CITY-017](../invariants/city.md#inv-city-017), [INV-COMBAT-001](../invariants/combat.md#inv-combat-001), [INV-COMBAT-002](../invariants/combat.md#inv-combat-002), [INV-PLAYER-002](../invariants/player.md#inv-player-002), [INV-PLAYER-012](../invariants/player.md#inv-player-012) e [INV-MOVE-009](../invariants/movement.md#inv-move-009); asigna [INV-UNIT-012](../invariants/units.md#inv-unit-012) e [INV-UNIT-013](../invariants/units.md#inv-unit-013) (§8) |

## 1. Objetivo

Que el pilar 4 de [game-pillars.md](../product/game-pillars.md) («las civilizaciones tienen unidades y tecnologías
exclusivas») tenga su parte de unidades. Desde E13 la civilización da rasgos y desde E14 un bien que sólo ella
elabora, pero en el campo de batalla todas juegan con el mismo guerrero: un ejército nórdico y uno bizantino se
distinguen por cuánto tardan en llegar, no por lo que hacen al llegar. Este slice da a cada civilización **una unidad
propia** con un perfil de combate que la identifica, sin inventar mecánicas: es un tipo más del catálogo de
[unit.md](unit.md), se entrena con `city.train` como el guerrero y lucha con las reglas de E2 a E12. Y ata esa unidad
a la economía de E14: parte de su coste es el bien de su civilización, que deja de servir sólo para comerciar.

## 2. Scope

**Entra.** Una unidad exclusiva por civilización (Q1), que se **suma** al aldeano y al guerrero comunes (Q2), con el
perfil de §4.2 (Q3); su ligadura a la civilización como dato del catálogo (`civilizations.unit`), no como condicional
por código (`RN-PLAYER-012`); el entrenamiento con `city.train`, a partir de la Edad de Bronce (Q4), con un coste en
madera, piedra y el bien propio (Q5); que pueda hacer todo lo que hace un guerrero —atacar, responder sola, saquear,
asediar, plantar el estandarte, capturar caravanas, cargar botín, entrar en guarnición— (Q6); los rasgos de E13
sobre ella como sobre cualquier unidad; la migración `000024`; en el protocolo, los cuatro tipos nuevos en `UnitType`
y en `TrainableUnitType`, sin mensajes nuevos; en el cliente, su silueta, su nombre, el botón de entrenamiento con su
coste y su motivo, y su nombre en el registro y en los avisos (Q7); un `scripts/smoke.mjs --unidad-civilizacion`.

**No entra** (`Fuera de MVP` de este slice): más de una unidad por civilización (Q1); sustituir al guerrero (Q2);
mecánicas nuevas de combate —bonos contra un tipo, cargas, área, curación, formaciones, contraataques a distancia—
(Q3); un bono propio en tierra afín (Q8); entrenar la unidad de otra civilización, por alianza o como mercenario
(Q9); un tope por ciudad (Q10); edificios o tecnologías exclusivos; la Edad de los Castillos; mejorar o reconvertir
unidades ya entrenadas; reequilibrar con datos de jugadores. **Precisión (2026-11-17, [castle-age.md](castle-age.md), [ADR-044](../decisions/ADR-044-castle-shooting.md)):** la Edad de los Castillos llega con E23, y no cambia las unidades de civilización (Q3 de esa spec).

## 3. Actores

Jugador (entrena su unidad y la manda), los demás jugadores (la ven, la combaten, comercian el bien que la paga),
cliente web (botón, silueta, textos), Game Server (valida el entrenamiento en el tick y aplica el combate,
[INV-SEC-001](../invariants/security.md#inv-sec-001)), tick loop (fase 2 para la orden, fase 4 para el combate,
fase 5 para el nacimiento), PostgreSQL (el catálogo `civilizations`, la producción y su débito, las unidades).

## 4. Modelo

### 4.1 Una unidad por civilización

Cada civilización tiene **una unidad propia** (Q1), un tipo del catálogo de unidades que sólo entrenan las ciudades
de esa civilización:

| Civilización | Unidad | Código | Bien que la paga |
|---|---|---|---|
| `ROMAN` | Legionario | `LEGIONARY` | Vino (`WINE`) |
| `BYZANTINE` | Catafracto | `CATAPHRACT` | Seda (`SILK`) |
| `PERSIAN` | Arquero a caballo | `HORSE_ARCHER` | Especias (`SPICES`) |
| `NORSE` | Berserker | `BERSERKER` | Pieles (`FURS`) |

Qué unidad es de qué civilización es un dato de `civilizations` (columna `unit`), como su bien
([ADR-036](../decisions/ADR-036-civilization-goods.md)): el entrenamiento pregunta «¿es este tipo la unidad de la
civilización del dueño?», nunca por el código de la civilización (`RN-PLAYER-012`). Las estadísticas viven en el
catálogo de unidades de Go con las de las demás (`unit.Lookup`), marcadas como **exclusivas**. Añadir una
civilización es añadir su fila, su bien y su unidad.

La unidad se **suma** al aldeano y al guerrero, que siguen siendo de todos (Q2).

### 4.2 Sus perfiles

Los cuatro perfiles salen de los campos que el catálogo ya tiene (`MaxHP`, `BaseMsPerTile`, `AttackDamage`,
`AttackIntervalMs`, `AttackRange`, `LootCarry`, `PopulationCost`); ninguno necesita una mecánica nueva (Q3). Cada uno
gana el duelo contra un guerrero sin llegar a valer dos, y es mejor que él en una cosa, que es su identidad:

| | Guerrero (común) | Legionario | Catafracto | Arquero a caballo | Berserker |
|---|---|---|---|---|---|
| Identidad | — | Aguanta: conquista y defiende | Rapidez: alcanza y persigue | Distancia: golpea antes | Golpe: saquea y derriba |
| Vida | 60 | **110** | 80 | 60 | 60 |
| Daño por golpe | 8 | 8 | 10 | 8 | **14** |
| Intervalo | 1 500 ms | 1 500 ms | 1 500 ms | 1 500 ms | 1 500 ms |
| Alcance (Chebyshev) | 1 | 1 | 1 | **3** | 1 |
| Paso (tile de coste 1) | 600 ms | 600 ms | **450 ms** | 540 ms | 600 ms |
| Carga de botín | 20 | 20 | 20 | 20 | **30** |
| Población | 1 | 1 | 1 | 1 | 1 |
| Golpes para matar a un guerrero | 8 | 8 | 6 | 8 | **5** |
| Golpes de guerrero para matarlo | 8 | **14** | 10 | 8 | 8 |
| Golpes para derribar una casa (150) | 19 | 19 | 15 | 19 | **11** |

Los rasgos de E13 se aplican igual que a cualquier unidad, en el punto de consumo de
[ADR-035](../decisions/ADR-035-civilization-traits.md): el catafracto bizantino anda a 474 ms por tile
(`unit.move_speed` 950), el berserker nórdico carga 35 (`combat.loot_carry` 1150) y el legionario romano se entrena
un 10 % antes (`production.speed` 1100). Son números de partida, sin datos de jugadores, y se ajustan en el catálogo.

**El alcance 3 del arquero** usa el campo `AttackRange` que el combate ya lee al golpear, al perseguir, al responder
y al asediar (`chebyshevTiles(…) <= def.AttackRange`), pero ninguna unidad de `main` tiene hoy un alcance distinto
de 1: es lo único del perfil que se estrena (§9 lo cubre con un golden). El saqueo sigue pidiendo estar junto a la
muralla (`RN-COMBAT-019`), así que el arquero saquea pegado a ella como los demás; el asedio, en cambio, usa el
alcance, y derriba una casa desde tres tiles.

### 4.3 Entrenarla

`city.train { cityId, type }` admite los cuatro tipos nuevos, con las reglas de `RN-ECON-010` y tres más:

- **La civilización.** El tipo tiene que ser la unidad de la civilización del dueño de la ciudad. Pedir la de otra
  es `INVALID_TARGET`.
- **La edad.** La ciudad tiene que estar en la **Edad de Bronce** o después (Q4), como el taller. Antes,
  `INVALID_TARGET`.
- **El coste**, en tres recursos (Q5): **80 de madera, 40 de piedra y 10 del bien propio**, que se debitan juntos
  al confirmarse, en la misma transacción que la producción y la reserva de población
  ([INV-ECON-002](../invariants/economy.md#inv-econ-002), [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md)).
  Si falta cualquiera, `INSUFFICIENT_RESOURCES`. El bien es siempre el **propio**: los ajenos no pagan la unidad.
- **El plazo**: **35 s**, con el rasgo `production.speed` (31 818 ms un romano, 38 889 un nórdico).

Lo demás no cambia: una producción por ciudad (`CITY_BUSY`), la plaza de población reservada al ordenar
([INV-CITY-017](../invariants/city.md#inv-city-017)), no se entrena mientras la ciudad avanza de edad
(`RN-ECON-026`) ni por encima de su límite tras un asedio, y lo que está a medias nace tras un reinicio en el mismo
instante absoluto ([INV-ECON-005](../invariants/economy.md#inv-econ-005)). No hay tope por ciudad aparte de la
población (Q10).

Si la ciudad pierde la Edad de Bronce —hoy no ocurre— o el bien se acaba, las unidades ya entrenadas siguen: el
requisito y el coste se miran al ordenar, no después.

### 4.4 Lo que puede hacer

La unidad de civilización es, para el combate, **una unidad que ataca** (`Definition.CanAttack`), como el guerrero
(Q6). Todo lo que hoy hace el guerrero lo hace ella, con su perfil:

- atacar con `unit.attack` y perseguir ([combat-raid.md](combat-raid.md));
- responder sola a un golpe sobre ella o sobre las unidades cercanas de su dueño, con su correa y su vuelta al puesto
  ([combat-response.md](combat-response.md)): hoy esa respuesta pregunta `u.Type == WARRIOR` y pasa a preguntar por la
  capacidad, como ya hacen el ataque, el saqueo, el asedio y la conquista;
- saquear el almacén y llevarse el botín a casa ([combat-pillage.md](combat-pillage.md));
- derribar casas y talleres ([combat-siege.md](combat-siege.md));
- plantar el estandarte con `unit.claim`, para su dueño o para su clan ([territory-conquest.md](territory-conquest.md),
  [clan-territories.md](clan-territories.md));
- matar una caravana y capturarla ([caravans.md](caravans.md));
- entrar en guarnición en una ciudad propia o, con un tratado que lo permita, ajena ([garrison.md](garrison.md)).

No recolecta, como el guerrero. Se ataca, se mata y deja botín como cualquier unidad, y su muerte entra en el registro
y en los avisos con su tipo.

**Tierra afín** (Q8). Recibe el daño por `EO_FACTION_AFFINE_DAMAGE_PERMILLE` en tierra afín a su dueño, como toda
unidad (`RN-FAC-008`), sin un bono propio: el legionario aguanta 19 golpes de guerrero en tierra afín en vez de 14.

**Alianzas** (Q9). La unidad es de su dueño y de nadie más: ni un aliado por tratado (`ALLIANCE`), ni un compañero de
clan, ni un clan aliado la entrena ni la manda. Lo que la alianza cambia es lo de siempre: la paz (`AtPeace`) impide
que se ataquen entre sí, una respuesta automática no se dispara contra un aliado, y un tratado con `allows_garrison`
la deja entrar en la ciudad del aliado como cualquier unidad. Lo que una alianza sí puede hacer es venderle a otro su
bien (E10), y ése es el camino por el que la unidad crea comercio.

### 4.5 Reglas

| ID | Regla |
|---|---|
| `RN-CIVUNIT-001` | Cada civilización tiene exactamente una unidad exclusiva, dato de `civilizations.unit`; dos civilizaciones no comparten unidad y la unidad exclusiva de una no es ni el aldeano ni el guerrero. |
| `RN-CIVUNIT-002` | Una ciudad sólo entrena la unidad exclusiva de la civilización de su dueño, y sólo desde la Edad de Bronce. Pedir otra, o pedirla antes, es `INVALID_TARGET`. |
| `RN-CIVUNIT-003` | El entrenamiento de una unidad exclusiva cobra madera, piedra y el bien propio juntos, al confirmarse y con su efecto; nunca un bien ajeno. |
| `RN-CIVUNIT-004` | El requisito de edad, la civilización y el coste se comprueban al ordenar; una unidad ya entrenada no se pierde si dejan de cumplirse. |
| `RN-CIVUNIT-005` | Toda regla de combate, respuesta, saqueo, asedio, conquista y captura que dice `WARRIOR` o «guerrero» vale para toda unidad que ataca (`CanAttack`): `RN-COMBAT-001`, `RN-COMBAT-010`, `RN-COMBAT-012` a `RN-COMBAT-016`, `RN-COMBAT-019` a `RN-COMBAT-027`, `RN-COMBAT-029` a `RN-COMBAT-037`, `RN-CONQ-001` y `RN-CONQ-005` a `RN-CONQ-007`, `RN-CLANTERR-001` y `RN-CLANTERR-005`, `RN-CARAVAN-006` y `RN-CIV-007`. Ningún sistema del servidor pregunta por el tipo `WARRIOR` para decidir quién ataca. |
| `RN-CIVUNIT-006` | Los rasgos de civilización se aplican a la unidad exclusiva en los mismos puntos de consumo que a cualquier otra (ADR-035); su perfil no tiene un modificador propio. |
| `RN-CIVUNIT-007` | El tipo de una unidad es público: viaja en `UnitView.unitType` para todo el que la ve, como hoy. |

### 4.6 Qué ve el jugador

- **Entrenar.** En el panel de su ciudad, junto a «Aldeano» y «Guerrero», un tercer botón con el nombre de su unidad
  y su coste («Legionario — 80 madera, 40 piedra, 10 vino»), deshabilitado con su motivo: «Desde la Edad de Bronce»,
  «Falta vino», o los de siempre (ya entrena, sin población, avanzando de edad). Sólo aparece la unidad propia.
- **En el mapa** (Q7). Cada unidad exclusiva tiene su **silueta**, dibujada como las demás con la geometría de
  PixiJS y sin imágenes nuevas: el cuerpo del guerrero con un escudo grande (legionario), sobre un caballo con lanza
  (catafracto), sobre un caballo con arco (arquero) y con dos hachas (berserker). El color de propio y ajeno, la
  barra de vida, la carga y el estado de ataque se dibujan como en el guerrero. Las unidades ajenas se ven igual:
  saber que el vecino tiene berserkers es información para decidir.
- **Selección.** Al seleccionar una, el panel dice su nombre y su civilización («Legionario · Romano»), y ofrece las
  mismas órdenes que al guerrero: atacar, saquear, derribar, plantar estandarte, guarnecer.
- **Registro y avisos.** Las muertes nombran el tipo de la unidad que murió («Murió tu catafracto»), como hoy con el
  aldeano y el guerrero.
- **Registro (alta).** La descripción de cada civilización en el formulario de registro añade su unidad, con su
  identidad en una línea («Unidad propia: legionario, aguanta el doble que un guerrero»): la elección es para
  siempre y la unidad es parte de lo que se elige.

## 5. Errores

Sin códigos nuevos (canon §16):

| Orden | Condición | Código |
|---|---|---|
| `city.train` | Un tipo que no está en `TrainableUnitType` | `INVALID_MESSAGE` (esquema) |
| | La ciudad no es propia | `CITY_NOT_FOUND` |
| | Una unidad exclusiva de otra civilización | `INVALID_TARGET` |
| | La unidad propia antes de la Edad de Bronce | `INVALID_TARGET` |
| | Ya entrena o avanza de edad | `CITY_BUSY` |
| | Sin plaza de población | `POPULATION_LIMIT_REACHED` |
| | Falta madera, piedra o el bien propio | `INSUFFICIENT_RESOURCES` |
| Órdenes de combate, saqueo, asedio, conquista y guarnición | Las de siempre, con la unidad exclusiva en el lugar del guerrero | Sin cambio |

El orden de las comprobaciones es el de `production.CanTrain` (ciudad, tipo, civilización, edad, ocupada, población,
recursos), y cada rechazo tiene su test.

## 6. Eventos y contratos de red

**Eventos.** Ninguno nuevo. `UnitTrained` y `UnitKilled` llevan ya el tipo de la unidad; con este slice puede ser uno de
los cuatro nuevos. No hay tipos nuevos en `world_events` ni en `eventlog.EventTypes`; las muertes del registro y de
los avisos ya llevan `unitType`.

**WebSocket (protocolo v1).** `UnitType` gana `LEGIONARY`, `CATAPHRACT`, `HORSE_ARCHER` y `BERSERKER`, y
`TrainableUnitType` los cuatro también. Es un valor nuevo en un enum estricto (AGENTS.md §4.2): rompe a un cliente
viejo, así que cliente y servidor se estrenan juntos. No hay mensajes ni campos nuevos: siguen los 44 mensajes
cliente→servidor y los 31 servidor→cliente. `CityView` no cambia: el cliente sabe cuál es la unidad propia por la
civilización de su ciudad y el catálogo de presentación.

**Catálogo de presentación.** Como el de civilizaciones de E13, `packages/protocol` exporta un catálogo de
presentación de las unidades entrenables (código, nombre, coste, plazo base, vida, daño, alcance, paso, población) y
la unidad de cada civilización, para el botón, el panel y el registro. Las estadísticas efectivas no viajan: la vida
llega en `UnitView.maxHp` y los plazos en la producción y en la polilínea. Un test compara ese catálogo con el de Go
y con `civilizations.unit` para que no se desvíen en silencio (§9).

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | La producción en curso y las unidades, como hoy; el catálogo de unidades y la unidad de cada civilización, inmutables durante el proceso |
| Write-through | El encargo con su débito en tres recursos y la reserva de población, en una transacción; el nacimiento de la unidad, como hoy |
| Dirty-flag | Nada nuevo |
| Reconstruible / no persistido | Las estadísticas efectivas (paso con el rasgo, carga de botín, vida máxima del tipo); qué civilización entrena qué unidad, que sale del catálogo |

**Por qué hace falta un ADR ([ADR-043](../decisions/ADR-043-civilization-units.md)).** Hay tres decisiones estructurales que conviene dejar escritas con sus
alternativas:

1. **La unidad es un tipo del catálogo ligado por `civilizations.unit`.** Descartadas: una columna `civilization` en
   el catálogo de unidades (el catálogo de unidades vive en Go y la civilización en la base, y la pregunta
   «¿cuál es la unidad de X?» quedaría en dos sitios); un **único** tipo `CIVILIZATION_UNIT` cuyas estadísticas
   cambian con la civilización del dueño (rompe que el tipo diga lo que es una unidad, y el cliente y el registro
   tendrían que cruzar dos datos para saber qué ven); un rasgo por civilización sobre el guerrero (es otra versión de
   E13, no una unidad).
2. **El coste de entrenar pasa a ser una lista de recursos** (`TrainCosts`, el `economy.Costs` que ya usan las obras
   desde E14) con un requisito de edad (`RequiresEra`, como los edificios). Descartado: un segundo débito aparte para
   el bien, que partiría la transacción del encargo en dos.
3. **El combate pregunta por capacidades** (`CanAttack`), no por el tipo `WARRIOR`. En el servidor queda un único
   sitio que pregunta por el tipo (la respuesta automática) y en el cliente unos pocos (la proyección, la barra de
   órdenes, la conquista); todos pasan a la capacidad del catálogo de presentación. Descartado: enumerar los cinco
   tipos que atacan en cada sitio, que repetiría el problema con la próxima unidad.

**Migración `000024`:**

- `civilizations.unit text`, rellenada con los cuatro códigos de §4.1, `NOT NULL`, `UNIQUE` y con un `CHECK` de los
  cuatro valores (como `civilizations.good` en `000016`).
- `city_productions_unit_type_valid` admite los cuatro tipos nuevos.
- Un trigger `BEFORE INSERT` en `city_productions` que rechaza una unidad exclusiva que no sea la de la civilización
  del dueño de la ciudad (la defensa en la base de `RN-CIVUNIT-002`).
- `units.unit_type` no tiene `CHECK` de tipo desde `000001` (lo recuerda `000017`) y no lo gana.

La carga del Game Server comprueba que la unidad de cada civilización es un tipo del catálogo de Go, exclusivo y
entrenable, y que ningún tipo exclusivo queda sin civilización; si no, no arranca. Ninguna tabla nueva, así que el
`TRUNCATE` de los tests de integración no cambia.

## 8. Invariantes

**Cita** [INV-SEC-001](../invariants/security.md#inv-sec-001) (el cliente pide un tipo; el servidor decide si su
civilización y su edad lo permiten), [INV-ECON-002](../invariants/economy.md#inv-econ-002) (el débito en tres
recursos va con su efecto), [INV-ECON-005](../invariants/economy.md#inv-econ-005) (el entrenamiento a medias nace en
el mismo instante tras un reinicio), [INV-CITY-017](../invariants/city.md#inv-city-017) (la unidad reserva su plaza),
[INV-COMBAT-001](../invariants/combat.md#inv-combat-001) e [INV-COMBAT-002](../invariants/combat.md#inv-combat-002)
(la resolución y el predicado de hostilidad, sin cambio con otro atacante),
[INV-PLAYER-002](../invariants/player.md#inv-player-002) e [INV-PLAYER-012](../invariants/player.md#inv-player-012)
(la civilización y sus rasgos, que se aplican a la nueva unidad) e
[INV-MOVE-009](../invariants/movement.md#inv-move-009) (la polilínea con otro paso base sigue siendo función pura de
sus datos).

**Asigna** dos invariantes nuevos de la familia `INV-UNIT`, registrados en [../invariants/units.md](../invariants/units.md):

- [INV-UNIT-012](../invariants/units.md#inv-unit-012) — una ciudad sólo produce la unidad exclusiva de la civilización de su dueño, y sólo desde la Edad de Bronce (dominio
  en `production.CanTrain`, trigger de `000024` en la base).
- [INV-UNIT-013](../invariants/units.md#inv-unit-013) — cada civilización tiene exactamente una unidad exclusiva, distinta de las demás, entrenable y presente en el
  catálogo de unidades (`NOT NULL` y `UNIQUE` de `civilizations.unit`, validación de carga).

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | `production.CanTrain` con cada fila de §5 en su orden; el coste en tres recursos con cada uno faltando; el catálogo: cada exclusiva `CanAttack`, entrenable y con su perfil de §4.2; la validación de carga (civilización sin unidad, unidad desconocida, no exclusiva, dos civilizaciones con la misma, exclusiva huérfana) |
| simulation | Golden con los ticks exactos: un legionario contra un guerrero (14 golpes recibidos, gana) y un berserker contra un guerrero (5 golpes); el **arquero golpea desde tres tiles** al ordenar, al perseguir y al responder, y asedia una casa desde tres tiles pero saquea junto a la muralla; el catafracto llega antes que un guerrero por la misma ruta (450 ms por tile, 474 bizantino); el legionario en tierra afín aguanta 19 golpes; una unidad exclusiva **responde sola** a un golpe sobre un aldeano cercano; saquea, derriba, planta el estandarte y captura una caravana; el entrenamiento con `production.speed` (31 818 ms romano, 38 889 nórdico); el catálogo de comandos y de trabajos sin cambios de recuento |
| integration | `000024` arriba y abajo; el trigger rechaza la unidad de otra civilización y admite la propia (y se comprueba por mutación con `DISABLE TRIGGER`); el encargo debita madera, piedra y el bien en una transacción y no deja nada si falla; `civilizations.unit` coincide con el catálogo de presentación |
| recovery | Un legionario a medias nace tras un reinicio en el mismo instante absoluto; una unidad exclusiva viva se carga con su tipo, su vida y su botín |
| contract / e2e | `UnitType` y `TrainableUnitType` con los cuatro valores contra el JSON Schema (Go y Zod); `city.train` con la unidad propia, con la ajena (`INVALID_TARGET`) y en la Edad de Piedra (`INVALID_TARGET`) por WebSocket; un `unit.attack` de una unidad exclusiva |
| cliente | El botón de la unidad propia con su coste y cada motivo; que no aparece la ajena; la proyección y la barra de órdenes usan la capacidad y no el tipo; los textos de registro y avisos con los cuatro nombres; el texto del registro de alta |
| humo | `scripts/smoke.mjs --unidad-civilizacion`, con su función exportada y su test en `scripts/smoke.test.mjs`: una cuenta en Bronce con bien propio (semilla `EO_DEVSEED_STOCK_FOR`) entrena su unidad, la ve nacer con su tipo y su vida, y ve rechazada la de otra civilización |

**Precisiones (2026-11-17, al implementarlo).**

- **Los golden de §9.** El duelo del legionario se comprueba como lo ve el juego: el guerrero que lo ataca lo despierta,
  el legionario responde, mata al guerrero en ocho golpes y termina con 46 de vida tras recibir ocho
  (`TestElLegionarioAguantaYGanaElDuelo`); los 14 golpes que aguantaría son la cuenta de su vida, no un golden. Con
  el berserker, el primer golpe cae en el tick de la orden, porque ya está en contacto: 46, 32, 18, 4 y 0. El
  arquero no busca un tile a tres de su objetivo: persigue hacia él, como el guerrero, y se para en cuanto queda a
  tres (`chase` y `startAttacking` ya preguntaban por `AttackRange`); asedia desde un tile a tres de la casa y saquea
  desde el anillo de la muralla.
- **La producción cargada al arrancar** resuelve su coste con el bien de la civilización de su ciudad, para que la
  devolución de un rechazo sea exacta; una confirmada no se devuelve nunca.
- **El protocolo.** `CivilizationInfo.unit` es un `TrainableUnitType`, no un `UnitType`: así el cliente lo pasa a
  `city.train` sin convertirlo.
- **El cliente** (§4.6). El botón dice «Legionario (80 de madera, 40 de piedra y 10 de vino)»; el panel de selección,
  «Legionario · Romanos» (el nombre del catálogo de civilizaciones es plural); el registro, «Unidad propia:
  legionario, aguanta casi el doble que un guerrero». Reclamar con una unidad que no ataca dice «Sólo un guerrero o tu
  unidad propia reclaman territorios».
- **La migración de vuelta** borra las producciones de tipos exclusivos y deja las unidades ya nacidas, que un
  servidor anterior excluye al cargar (`INV-UNIT-009`).

## 10. Decisiones de producto (2026-11-17)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una (en negrita), y las alternativas
quedan escritas. Son decisiones de producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Cuántas unidades exclusivas por civilización? | **Una.** Basta para que cada civilización tenga una cara en el campo de batalla, y cuatro perfiles ya son cuatro balances sin datos de jugadores. Una segunda unidad por civilización (por ejemplo una de defensa y otra de ataque) duplica el balance y el dibujo y conviene decidirla con datos de cómo se usa la primera. Alternativas: **dos** (una de Bronce y otra de Hierro), que da algo nuevo que desbloquear al llegar a Hierro, pero ocho perfiles a la vez; **ninguna por ahora**, y que la identidad militar siga saliendo de los rasgos. |
| Q2 | ¿Sustituye a una común o se suma? | **Se suma: el aldeano y el guerrero siguen siendo de todos.** El guerrero es lo que se entrena en la Edad de Piedra, antes de poder pagar la unidad propia, y lo que el humo y las pasadas de combate dan por hecho. Sustituirlo haría que una civilización perdiera la opción barata y uniforme, y que cuatro flujos (conquista, saqueo, asedio, respuesta) dependieran de la civilización. Alternativa: **sustituye al guerrero desde la Edad de Bronce** (el guerrero deja de entrenarse y la unidad propia es la única de combate), más «de civilización» pero obliga a pagar el bien en cada soldado y deja sin ejército barato al que no elabora. |
| Q3 | ¿Qué perfil tiene cada una? | **Los cuatro de §4.2, cada uno mejor que el guerrero en una cosa y con una identidad que encaja con sus rasgos:** legionario romano, que aguanta (110 de vida); catafracto bizantino, rápido (450 ms por tile, que compensa el −5 % de su civilización); arquero a caballo persa, que golpea a tres tiles; berserker nórdico, que pega fuerte (14) y carga más botín (30), en la línea de su rasgo de saqueo. Todos con los campos que el catálogo ya tiene: ninguna mecánica nueva. El riesgo está en el arquero, el único con alcance mayor que 1 (§4.2). Alternativas: **sin unidad a distancia** (un «inmortal» persa con más vida y algo más rápido, en vez del arquero), más seguro porque no estrena nada; **perfiles con una mecánica propia** (bono contra un tipo, daño en área, curación), que es lo que más diferencia pero cada mecánica es una regla de combate nueva con sus invariantes, y queda para otro slice. |
| Q4 | ¿En qué edad se desbloquea? | **En la Edad de Bronce**, la misma que el taller. Es cuando la ciudad empieza a elaborar su bien, que paga la unidad (Q5), y llega pronto: la civilización se nota en la partida, no sólo al final. Alternativas: **la Edad de Hierro**, como premio a haber comerciado (Hierro pide 60 bienes ajenos), pero con pocos jugadores muchos no la verían nunca; **la Edad de Piedra**, que sin el bien la dejaría pagar sólo con madera y piedra y la desligaría de E14. |
| Q5 | ¿Cuánto cuesta y usa el bien de su civilización? | **80 de madera, 40 de piedra y 10 del bien propio, 35 s y 1 de población.** El bien propio le da a lo que elabora el taller un segundo uso: cada unidad es una decisión entre vender el bien (para que otros lleguen a Hierro y te paguen) o convertirlo en ejército, que es justo la tensión que el pilar 4 quiere. 10 bienes son dos elaboraciones (60 s y 60 de madera y 30 de piedra), así que la unidad cuesta en total algo más del doble que un guerrero, y le gana el duelo sin llegar a valer dos (§4.2). Precisa la Q8 de [civilization-goods.md](civilization-goods.md) («los bienes propios no sirven a su productor más que para comerciar»). Alternativas: **un bien ajeno** (por ejemplo seda para el legionario), que obliga a comerciar para tener ejército propio y fuerza más la interdependencia, pero deja sin su unidad a quien no encuentra socio; **sólo madera y piedra**, que no toca la economía de los bienes y hace la unidad accesible a todos, pero la desliga del pilar 4; **2 de población**, que frena el número sin tocar el precio. |
| Q6 | ¿Puede hacer todo lo que hace un guerrero? | **Sí: atacar, responder sola, saquear, asediar, plantar el estandarte, capturar caravanas, cargar botín y entrar en guarnición, con su perfil.** Es la forma de no inventar mecánicas: la unidad es un guerrero con otros números, y el servidor ya decide casi todo por la capacidad de atacar (§4.4). Que el berserker no pudiera saquear o el legionario no pudiera conquistar haría que la identidad dijera una cosa y las reglas otra. Alternativa: **restringir cada una a su papel** (sólo el legionario conquista, sólo el berserker saquea), más temático pero hace del guerrero común imprescindible y cambia las reglas de E5, E8 y E12. |
| Q7 | ¿Cómo se ve en el cliente? | **Una silueta propia por unidad, dibujada con la geometría que ya usa la escena (escudo, caballo con lanza, caballo con arco, dos hachas), con el mismo color de propio y ajeno, la misma barra de vida y su nombre y civilización en el panel de selección; los demás la ven igual.** Sin imágenes ni sprites nuevos, que la escena no usa para ninguna unidad. Que se vea desde lejos qué tipo de ejército tiene el vecino es información para decidir, como su civilización (Q7 de E13). Alternativas: **la silueta del guerrero con un distintivo de color** por civilización, más barata pero que se confunde con el color de propio y ajeno; **sprites ilustrados**, que piden un sistema de recursos gráficos que el cliente aún no tiene. |
| Q8 | ¿Cómo encaja con la tierra afín? | **Igual que cualquier unidad: recibe el 80 % del daño en tierra afín a su dueño (`RN-FAC-008`), sin bono propio.** Así la tierra afín sigue siendo una regla de bando y la unidad una regla de civilización, y las dos se suman sin multiplicarse por sorpresa (el legionario aguanta 19 golpes de guerrero en tierra afín). Alternativas: **un bono de unidad en tierra afín** (por ejemplo, el legionario recibe aún menos daño en su tierra), que mezcla los dos ejes que la visión quiere ortogonales; **que la tierra afín no proteja a las unidades exclusivas**, para que no sean demasiado fuertes defendiendo, que complica una regla hoy uniforme. |
| Q9 | ¿Cómo encaja con las alianzas? | **No se comparte: sólo su dueño la entrena y la manda.** Un aliado (tratado `ALLIANCE`, compañero de clan o clan aliado) no puede entrenar ni recibir la unidad de otra civilización; la alianza da lo de siempre: paz, que no se ataquen ni se respondan, y guarnición con un tratado que la permita. El comercio del bien (E10) es lo que une una unidad a la civilización de al lado. Alternativas: **mercenarios**: una ciudad puede entrenar la unidad de la civilización de un aliado pagando el bien de ese aliado, que crea un mercado del bien muy fuerte pero borra lo exclusivo de cada civilización; **un bono de alianza** (por ejemplo, la unidad recibe menos daño en tierra de un aliado), que suma una fuente de afinidad que E21 decidió no dar (Q2 de E21). |
| Q10 | ¿Hay un tope de unidades exclusivas por ciudad? | **No: la limitan la población, el coste y el bien.** Un tope fijo («como mucho 5 legionarios») es una regla más que explicar y que comprobar en cada nacimiento y en cada captura de guarnición, y el bien ya es un freno que depende de cuánto elabora y cuánto vende cada jugador. Alternativa: **un tope por ciudad** (por ejemplo, 10), por si el bien resulta barato y un ejército sólo de exclusivas se impone; se puede añadir después con una migración si los datos lo piden. |

## 11. Documentos relacionados

- [unit.md](unit.md) (el catálogo y los estados), [../invariants/units.md](../invariants/units.md) (`INV-UNIT`).
- [economy-wood.md](economy-wood.md) (`city.train` y `RN-ECON-010`), [economy-stone.md](economy-stone.md) (la Edad de
  Bronce y `RN-ECON-026`).
- [civilizations.md](civilizations.md) y [ADR-035](../decisions/ADR-035-civilization-traits.md) (los rasgos),
  [civilization-goods.md](civilization-goods.md) y [ADR-036](../decisions/ADR-036-civilization-goods.md) (el bien y
  el taller).
- [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md),
  [combat-siege.md](combat-siege.md), [territory-conquest.md](territory-conquest.md) y [caravans.md](caravans.md)
  (lo que hace una unidad que ataca).
- [factions.md](factions.md) (la tierra afín), [clan-territories.md](clan-territories.md) y
  [clan-alliances.md](clan-alliances.md) (la paz entre clanes), [garrison.md](garrison.md) (la guarnición).
- [../product/game-pillars.md](../product/game-pillars.md) (pilar 4).
