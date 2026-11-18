# Árbol tecnológico: la investigación que mejora una ciudad sin dejarla hacerlo todo

Propuesta para que una ciudad pueda **investigar tecnologías**: mejoras permanentes del jugador que se pagan en
recursos y tiempo, que piden una era y, a veces, otra tecnología antes, y cuyos efectos son datos de catálogo que se
aplican con las mismas fórmulas que los rasgos de civilización. Cada civilización tiene una tecnología exclusiva, así
que nadie completa el árbol entero.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-TECHNOLOGIES` |
| Estado | Accepted (2026-11-18). Propuesta en `Draft` el 2026-11-17 y aceptada el 2026-11-18 con la recomendación de cada pregunta de §10. |
| Milestone | E25 Árbol tecnológico (vigesimoquinto slice después de M7) |
| Canon | §0 (civilización como eje), §1.5 (determinismo), §10 (eras), §11 (catálogos), §12 (persistencia), §13 (protocolo), §16 (errores) |
| Depende de | [economy-wood.md](economy-wood.md), [economy-stone.md](economy-stone.md), [civilization-goods.md](civilization-goods.md), [civilizations.md](civilizations.md), [civilization-units.md](civilization-units.md), [castle-age.md](castle-age.md), [combat-raid.md](combat-raid.md), [combat-siege.md](combat-siege.md), [combat-pillage.md](combat-pillage.md), [city.md](city.md), [player.md](player.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) (encargos en dos fases y débito con su efecto), [ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md) (el avance de era como encargo con su fila), [ADR-035](../decisions/ADR-035-civilization-traits.md) (los rasgos por mil en el punto de consumo), [ADR-036](../decisions/ADR-036-civilization-goods.md) (los bienes ajenos en cualquier mezcla) y [ADR-043](../decisions/ADR-043-civilization-units.md) (el combate que pregunta por capacidades). [ADR-046](../decisions/ADR-046-technologies.md) (§7): el catálogo en base con sus efectos como rasgos por mil, la composición con el rasgo de la civilización en un único punto y la investigación como encargo con su fila. |
| Reemplaza a | En [mvp-scope.md](../product/mvp-scope.md), `OUT-03` («Árbol tecnológico completo»), y la fase Technology de [roadmap.md](../roadmap/roadmap.md); cierra la `Q-12` del [backlog](../roadmap/backlog.md) (la investigación no hace avanzar de era, Q12). Precisa `RN-ECON-026` (un avance de era y una investigación no conviven, Q8) y, en [civilizations.md](civilizations.md), `RN-CIV-004` (un rasgo y las tecnologías se componen, §4.4). |
| Invariantes | Cita [INV-SEC-001](../invariants/security.md#inv-sec-001), [INV-SEC-007](../invariants/security.md#inv-sec-007), [INV-ECON-001](../invariants/economy.md#inv-econ-001), [INV-ECON-002](../invariants/economy.md#inv-econ-002), [INV-ECON-005](../invariants/economy.md#inv-econ-005), [INV-ECON-011](../invariants/economy.md#inv-econ-011), [INV-CITY-018](../invariants/city.md#inv-city-018), [INV-PLAYER-011](../invariants/player.md#inv-player-011), [INV-PLAYER-012](../invariants/player.md#inv-player-012), [INV-MOVE-009](../invariants/movement.md#inv-move-009) e [INV-PERSIST-005](../invariants/persistence.md#inv-persist-005); asigna [INV-TECH-001](../invariants/technology.md#inv-tech-001) e [INV-TECH-002](../invariants/technology.md#inv-tech-002) (§8) |

## 1. Objetivo

Hoy una ciudad progresa de una sola manera: avanza de era. Cada avance sube el límite de población y abre algo (el
taller y la unidad propia en Bronce, el castillo en Castillos), pero entre dos avances no hay nada que decidir con lo
que se acumula, salvo entrenar más o construir casas. La visión dice que cada era «abre unidades y tecnologías»
([vision.md](../product/vision.md)), el pilar 4 pide que las civilizaciones tengan «tecnologías exclusivas»
([game-pillars.md](../product/game-pillars.md)) y la fase Technology del roadmap exige que sus efectos sean «datos en
base, nunca condicionales en el código del dominio».

Este slice añade la **investigación**: una orden de la ciudad que, pagada y cumplido su plazo, deja al jugador una
mejora permanente (recolectar más deprisa, golpear más fuerte, casas más duras…). Las tecnologías comunes las puede
investigar cualquiera cuando llega a su era; cada civilización tiene además **una exclusiva**, que pide su propio bien
y que las demás no pueden investigar. Así cumple el anti-pilar del pilar 4 («cada jugador puede desbloquear todo el
árbol»): el árbol de cada jugador es distinto.

## 2. Scope

**Entra.** El catálogo de tecnologías (Q3, Q4), en base y con sus efectos como datos; la orden `city.research` con su
coste (Q5), su plazo (Q6), su era y sus requisitos (Q7); una investigación en curso por ciudad, que convive con
entrenar, construir y elaborar, pero no con un avance de era (Q8); las tecnologías de cada jugador, permanentes
(Q10); sus efectos en los puntos de consumo de los rasgos de E13 más dos nuevos, el daño de un golpe y la carga de un
aldeano (Q4); la composición con los rasgos de civilización (§4.4); la migración `000027`; en el protocolo, la orden
nueva, `research` y `technologies` en la vista propia de la ciudad y el catálogo de presentación; en el cliente, el
panel «Tecnologías» de la ciudad con su coste, su motivo y su cuenta atrás; `scripts/smoke.mjs --tecnologia`.

**No entra.** Que una tecnología abra unidades, edificios o eras (Q12: el avance de era sigue como está); cancelar
una investigación (Q9); más de una investigación a la vez en una ciudad; perder una tecnología (Q10); compartirlas con
el clan o con un aliado, o comprarlas en un trato (Q11); tecnologías que cambien el castillo, las caravanas, el
comercio o la conquista; ramas excluyentes («elige una de dos», alternativa de Q3); un registro o un aviso por cada
investigación terminada (Q13); que otros jugadores vean las tecnologías ajenas (Q14); reequilibrar con datos de
jugadores.

## 3. Actores

Jugador dueño de la ciudad (ordena la investigación y ve su progreso), cliente web (el panel, los motivos, la cuenta
atrás), Game Server (valida la orden, reserva el coste, fija el plazo, aplica los efectos en cada punto de consumo;
[INV-SEC-001](../invariants/security.md#inv-sec-001)), PostgreSQL (el catálogo, la investigación en curso y las
tecnologías de cada jugador), los demás jugadores (no ven nada nuevo, salvo el efecto en el mapa: unidades más
rápidas, golpes más fuertes).

## 4. Modelo

### 4.1 El catálogo

Una tecnología es una fila de catálogo con su código, su nombre, la era que pide, su coste, su plazo, sus requisitos
(otras tecnologías) y sus efectos. Doce tecnologías: ocho comunes, dos por era, y una exclusiva por civilización
(Q3). Con la propuesta de valores de Q4 a Q7:

| Código | Nombre | Era | Requisito | Efecto | Coste | Plazo |
|---|---|---|---|---|---|---|
| `STONE_TOOLS` | Herramientas de piedra | Piedra | — | `economy.gather_speed` 1100 (el trabajo en el tile, −9 %) | 150 madera, 50 piedra | 60 s |
| `BASKETRY` | Cestería | Piedra | — | `economy.gather_carry` 1200 (un aldeano carga 12 en vez de 10) | 120 madera, 30 piedra | 60 s |
| `BRONZE_WEAPONS` | Armas de bronce | Bronce | — | `combat.attack_damage` 1100 (el golpe de toda unidad que ataca) | 250 madera, 150 piedra | 120 s |
| `MASONRY` | Albañilería | Bronce | — | `building.house_hp` 1200 (la vida de una casa) | 200 madera, 200 piedra | 120 s |
| `WHEEL` | La rueda | Hierro | — | `unit.move_speed` 1050 (toda unidad del jugador) | 300 madera, 200 piedra, 30 bienes ajenos | 180 s |
| `GUILDS` | Gremios | Hierro | — | `production.speed` 1100 (obras y entrenamientos) | 300 madera, 300 piedra, 30 bienes ajenos | 180 s |
| `IRON_WEAPONS` | Armas de hierro | Castillos | `BRONZE_WEAPONS` | `combat.attack_damage` 1100 | 400 madera, 300 piedra, 60 bienes ajenos | 300 s |
| `ARCHITECTURE` | Arquitectura | Castillos | `GUILDS` | `production.speed` 1100 | 400 madera, 400 piedra, 60 bienes ajenos | 300 s |
| `ROMAN_ROADS` | Calzadas | Hierro | — (sólo `ROMAN`) | `unit.move_speed` 1100 | 200 madera, 200 piedra, 40 vino | 180 s |
| `THEODOSIAN_WALLS` | Muros de Teodosio | Hierro | — (sólo `BYZANTINE`) | `building.house_hp` 1200 | 200 madera, 200 piedra, 40 seda | 180 s |
| `CARAVANSERAI` | Caravasares | Hierro | — (sólo `PERSIAN`) | `economy.gather_carry` 1200 | 200 madera, 200 piedra, 40 especias | 180 s |
| `RAIDING` | Incursiones | Hierro | — (sólo `NORSE`) | `combat.loot_carry` 1200 | 200 madera, 200 piedra, 40 pieles | 180 s |

Un jugador puede llegar a nueve: las ocho comunes y la suya. Los bienes ajenos se cobran en cualquier mezcla de los
de otras civilizaciones, como el avance a Hierro ([ADR-036](../decisions/ADR-036-civilization-goods.md)); la
exclusiva cobra el bien propio, la única orden que lo pide además de la unidad exclusiva
([civilization-units.md](civilization-units.md)).

### 4.2 Investigar

`city.research { cityId, technology }` la da el dueño de la ciudad. Se acepta si la ciudad está en la era de la
tecnología o en una posterior, el jugador tiene sus requisitos y no la tiene ya, la tecnología es común o exclusiva de
su civilización, la ciudad no tiene otra investigación en curso ni un avance de era (Q8) y el almacén cubre el coste.
Como todo encargo ([ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md)), el tick reserva el
coste y la transacción lo debita y escribe la investigación con su plazo, todo o nada
([INV-ECON-002](../invariants/economy.md#inv-econ-002)); el plazo se fija al ordenar con el rasgo `production.speed`
de la civilización y con las tecnologías que ya tiene (Q6). Al vencer, en el tick de su plazo, la tecnología pasa a
ser del jugador y la ciudad recibe `city.update`. Lo que está a medias termina, tras un reinicio, en el mismo instante
absoluto ([INV-ECON-005](../invariants/economy.md#inv-econ-005)).

Mientras dura, la ciudad entrena, construye y elabora como siempre; lo que no puede es avanzar de era, ni investigar
otra cosa (`CITY_BUSY`). Una investigación no se cancela (Q9).

### 4.3 Los efectos

Un efecto es un par **clave, entero por mil**, con las mismas claves y el mismo significado que los rasgos de
civilización ([civilizations.md](civilizations.md) §4.2): `1100` en una clave de velocidad divide la duración entre
1,1; en una de cantidad la multiplica por 1,1. Las claves de E13 siguen en los mismos puntos de consumo (la polilínea,
el plazo de una obra o un entrenamiento, el trabajo en el tile, la vida de la casa, la carga de botín) y se añaden dos
(Q4):

- `combat.attack_damage` (cantidad): el daño de cada golpe de una unidad que ataca, medido al calcular el golpe con
  las tecnologías de su dueño. Vale también para el golpe de asedio a un edificio, que es el mismo golpe; no toca el
  disparo del castillo (Q4).
- `economy.gather_carry` (cantidad): lo que carga un aldeano en cada viaje de recolección, medido al terminar el
  trabajo en el tile, cuando toma su carga.

Como con los rasgos, el efecto se aplica **al dar la orden** y lo que está en curso no cambia: la ruta de una unidad
que ya andaba sigue igual, la obra encargada antes de `GUILDS` termina a su hora, y la siguiente orden ya va más
rápida. La vida de la casa se calcula cada vez que se lee, con las tecnologías del dueño de su ciudad, y el daño
acumulado no se reescala.

### 4.4 Cómo se componen

Para una clave, el valor efectivo es el rasgo de la civilización compuesto con el efecto de cada tecnología que el
jugador tiene y que lo lleva, en orden de código de tecnología, con la fórmula de cantidad de E13 en cada paso
(`(r × e + 500) / 1000`, mínimo 1). El resultado es un entero por mil y se aplica con las dos fórmulas de siempre
([INV-PLAYER-012](../invariants/player.md#inv-player-012)). Un romano con `ROMAN_ROADS` y `WHEEL` anda con
`1000 → 1100 → 1155` (primero `ROMAN_ROADS`, luego `WHEEL`): 600 ms por tile pasan a 519. Un nórdico (`unit.move_speed` 1100) con `WHEEL` anda con 1155: 600
pasan a 519; sin la rueda, a 545.

No hay un techo en el código: lo pone el catálogo. Con el de §4.1, las tecnologías juntas llevan una clave como mucho a
1210 en velocidad (`production.speed` con `GUILDS` y `ARCHITECTURE`) y a 1440 en cantidad (`building.house_hp` con
`MASONRY` y la exclusiva bizantina, o `economy.gather_carry` con `BASKETRY` y la persa), sin contar el rasgo de la
civilización (Q4). Un catálogo cuyo efecto no sea un
entero positivo, o que use una clave sin punto de consumo, impide arrancar, como un rasgo.

### 4.5 Clanes, alianzas y tratos

Las tecnologías son del jugador y de nadie más: ni el clan, ni un aliado, ni un trato las comparte o las vende (Q11).
Lo que la alianza y el comercio sí cambian es lo de siempre: el bien ajeno que piden las tecnologías de Hierro y de
Castillos se consigue comerciando, y el bien propio que pide la exclusiva se elabora en el taller.

### 4.6 Reglas

| ID | Regla |
|---|---|
| `RN-TECH-001` | Una tecnología es una fila del catálogo con su era, su coste, su plazo, sus requisitos y sus efectos; sus efectos son datos, nunca condiciones en el código del dominio. |
| `RN-TECH-002` | Sólo el dueño de la ciudad investiga, y sólo una tecnología de su era o anterior, común o exclusiva de su civilización, que no tenga ya y cuyos requisitos tenga. Si no, `INVALID_TARGET`. |
| `RN-TECH-003` | Una ciudad tiene como mucho una investigación en curso, y nunca a la vez que un avance de era. Si no, `CITY_BUSY`. |
| `RN-TECH-004` | La investigación cobra su coste al confirmarse y con su efecto, en una transacción; sin almacén suficiente, `INSUFFICIENT_RESOURCES`. Una exclusiva cobra el bien propio; las de Hierro y Castillos, bienes ajenos en cualquier mezcla. |
| `RN-TECH-005` | El plazo se fija al ordenar con `production.speed` compuesto (§4.4) y no cambia después. Al vencer, la tecnología es del jugador para siempre. |
| `RN-TECH-006` | Un efecto se aplica en el punto de consumo de su clave al dar la orden, compuesto con el rasgo de la civilización y los demás efectos en orden de código (§4.4); lo que está en curso no cambia. |
| `RN-TECH-007` | Las tecnologías de un jugador no se comparten, no se venden y no se pierden. |
| `RN-TECH-008` | Las tecnologías y la investigación de una ciudad sólo las ve su dueño. |

### 4.7 Qué ve el jugador

- **El panel «Tecnologías»** de su ciudad: las doce que puede llegar a tener (las comunes y la suya), agrupadas por
  era, cada una con su nombre, su efecto en palabras («+10 % de daño en cada golpe»), su coste y su plazo. Las que ya
  tiene, marcadas; la que se investiga, con su cuenta atrás; las demás, con «Investigar» o deshabilitadas con su
  motivo («Desde la Edad de Bronce», «Pide Armas de bronce», «Falta vino», «Ya se investiga Gremios», «La ciudad está
  avanzando de edad»).
- **En el HUD**, la investigación en curso junto al avance de era, con su cuenta atrás.
- **Los efectos**, en los números que ya se ven: el plazo de una obra, la vida de una casa, la carga de un aldeano.

## 5. Errores

| Situación | Código | Motivo (`message`) |
|---|---|---|
| La ciudad no existe o no es suya | `INVALID_TARGET` | sin motivo, como hoy |
| La tecnología no existe, es exclusiva de otra civilización o ya la tiene | `INVALID_TARGET` | «Esa tecnología no está a tu alcance.» / «Ya tienes …» |
| La ciudad no está en su era | `INVALID_TARGET` | «… pide la Edad de …» |
| Le falta un requisito | `INVALID_TARGET` | «… pide …» |
| Otra investigación en curso o un avance de era | `CITY_BUSY` | «Ya se investiga …» / «La ciudad está avanzando de edad» |
| El almacén no cubre el coste | `INSUFFICIENT_RESOURCES` | como en los demás encargos |
| `requestId` repetido | — | la misma respuesta, sin segundo cobro ([INV-SEC-007](../invariants/security.md#inv-sec-007)) |

Ningún código nuevo.

## 6. Eventos y contratos de red

- **Cliente → servidor:** `city.research { cityId, technology }` (un comando nuevo, el 48.º tipo de v1).
- **Servidor → cliente:** en la vista **propia** de la ciudad (`CityView` del dueño y su `city.update`), dos campos
  opcionales: `research` (`{ technology, completesAtMs }` o `null`) y `technologies` (la lista de códigos que tiene el
  jugador). No viajan en la vista ajena (`RN-TECH-008`).
- **Catálogo de presentación:** los nombres, efectos, costes y plazos llegan al cliente como un catálogo estático
  (`technologies.json` en `packages/protocol`), como `civilizations.json`; el servidor sólo manda códigos. Un test de
  integración del Game Server compara ese catálogo con la tabla, para que una migración de balance que no lo
  actualice rompa la CI.
- **Eventos:** `TechnologyResearched` en `world_events` al vencer, con el jugador, la ciudad y el código, fuera del
  registro y de los avisos (Q13).

Todo aditivo (`R-VER-2`, `R-VER-3`): ningún enum estricto cambia; `technology` es un código de catálogo validado por
el servidor.

## 7. Persistencia

Migración `000027`:

- `technologies`: el catálogo (código único, nombre, era → `eras`, civilización → `civilizations` o nula para las
  comunes, coste en `jsonb`, plazo, requisitos en `jsonb`, efectos en `jsonb`), sembrado con las doce de §4.1. Toma el
  lugar de las dos tablas de diseño de [schema.md](../database/schema.md) (`technologies` y
  `civilization_technologies`): la exclusividad es una columna, no una tabla puente (§7 del ADR).
- `city_research`: la investigación en curso, una fila por ciudad como mucho (clave primaria `city_id`), con la
  tecnología, `completes_at_ms` y el `request_id` de la orden.
- `player_technologies`: `(player_id, technology_id)` única, con `researched_at`.
- `CHECK` y triggers: la exclusiva sólo para un jugador de su civilización, una investigación que no conviva con un
  avance de era en la misma ciudad.

La carga lee el catálogo, las tecnologías de cada jugador y las investigaciones en curso; el valor compuesto de cada
clave se calcula en la RAM al cargar y al terminar cada investigación, nunca con joins por tick.

**[ADR-046](../decisions/ADR-046-technologies.md).** El catálogo de tecnologías en base, con sus efectos como rasgos
por mil; la composición en orden de código con la fórmula de cantidad; los dos puntos de consumo nuevos; la
investigación como encargo con su fila, que reutiliza el de ADR-030.

## 8. Invariantes

**Cita:**

- [INV-SEC-001](../invariants/security.md#inv-sec-001) e [INV-SEC-007](../invariants/security.md#inv-sec-007): la
  investigación la decide el servidor y un `requestId` repetido no cobra dos veces.
- [INV-ECON-001](../invariants/economy.md#inv-econ-001) e [INV-ECON-002](../invariants/economy.md#inv-econ-002): el
  coste nunca deja el almacén en negativo y se debita con su efecto.
- [INV-ECON-005](../invariants/economy.md#inv-econ-005): la investigación a medias termina tras un reinicio en el
  mismo instante.
- [INV-ECON-011](../invariants/economy.md#inv-econ-011): sigue valiendo para el avance de era; la exclusiva es la
  excepción buscada para la investigación, y se dice así.
- [INV-CITY-018](../invariants/city.md#inv-city-018) e [INV-PLAYER-011](../invariants/player.md#inv-player-011): la
  era y la civilización, de las que dependen qué se puede investigar, no retroceden ni cambian.
- [INV-PLAYER-012](../invariants/player.md#inv-player-012) e [INV-MOVE-009](../invariants/movement.md#inv-move-009):
  los efectos son enteros por mil con aritmética entera y la posición sigue siendo función pura de la polilínea.
- [INV-PERSIST-005](../invariants/persistence.md#inv-persist-005): la migración `000027`.

**Asigna** dos invariantes de una familia nueva, `INV-TECH`, en [technology.md](../invariants/technology.md):

- [INV-TECH-001](../invariants/technology.md#inv-tech-001) — **Una investigación por ciudad, nunca con un avance.** Una ciudad tiene como mucho una investigación en curso, nunca
  a la vez que un avance de era, y sólo de una tecnología de su era, con sus requisitos y de su civilización si es
  exclusiva. Lo garantizan la simulación al ordenar y la base (la clave primaria, los `CHECK` y un trigger).
- [INV-TECH-002](../invariants/technology.md#inv-tech-002) — **Los efectos son datos y se componen en un único punto.** Todo valor afectado por una tecnología sale del rasgo de
  la civilización compuesto con los efectos de las tecnologías del jugador en orden de código, con aritmética entera,
  en el punto de consumo de su clave; ningún sistema pregunta por un código de tecnología.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | La composición de §4.4 (orden, redondeo, mínimo 1) con los ejemplos de la tabla; la validación del catálogo (efecto no positivo, clave desconocida, requisito inexistente o circular); cada regla de `RN-TECH-002` y `RN-TECH-003` |
| simulation | Golden «investigación» con los ticks exactos: ordenar, el cobro al confirmarse, el plazo con `production.speed`, la tecnología al vencer y su `city.update`; la siguiente obra ya va más rápida y la que estaba en curso no. Cada efecto en su punto de consumo (los siete), y uno compuesto con el rasgo. Cada rechazo de §5. La investigación y el avance que no conviven en los dos órdenes. El catálogo de comandos (48) y de trabajos |
| integration | La migración: las tres tablas, sus `CHECK` y el trigger. El encargo con su débito y su fila, idempotente por `requestId`; el vencimiento que escribe la tecnología y borra la fila en una transacción; una exclusiva de otra civilización rechazada por la base |
| recovery | Una investigación a medias termina en su instante tras un reinicio; las tecnologías vuelven y los valores compuestos coinciden con los de antes de parar |
| contract / e2e | `city.research` contra el JSON Schema; `research` y `technologies` sólo en la vista propia. Por WebSocket: investigar, recibir la cuenta atrás y la tecnología al vencer; otro jugador no ve ninguno de los dos campos |
| cliente | El panel con cada motivo, la cuenta atrás y la marca de las que tiene; el catálogo de presentación igual al de la base; `scripts/smoke.mjs --tecnologia`: `smoke_runner` investiga una tecnología de su era con un plazo corto (por variable de entorno) y comprueba que la siguiente orden lleva su efecto |

**Precisiones de implementación (2026-11-18).**

- **Lo que sólo ve el dueño viaja por su canal.** `research` y `technologies` van en la vista de la ciudad sólo en el
  snapshot de su dueño, y sus cambios por `SendToPlayer`; el stock que la investigación debita va al chunk como siempre.
  [ADR-046](../decisions/ADR-046-technologies.md) §5 dice «como `stocks` y `production`», pero esos dos viajan a todo el
  chunk: la comparación es inexacta, no el mecanismo.
- **Una ciudad ajena es `CITY_NOT_FOUND`**, como en el avance de era y el entrenamiento (§5 decía `INVALID_TARGET`
  sin motivo).
- **Dos eventos, no uno.** El encargo escribe `ResearchOrdered` y el fin `TechnologyResearched`, los dos de un solo
  jugador y sin `counterpart`, así que ninguno va al registro ni a los avisos (Q13).
- **El código de una tecnología** viaja validado por patrón (`^[A-Z][A-Z0-9_]{0,47}$`), no como enum.
- **El rasgo compuesto se calcula al consultarlo** y se guarda por jugador hasta que cambian sus tecnologías; un
  jugador sin tecnologías usa directamente el de su civilización (ADR-046 §2).
- **La carga** descarta una investigación que no cumple INV-TECH-001 (la ciudad no existe o avanza, la tecnología no
  está en el catálogo, es de otra civilización o de otra era, ya la tiene o le falta un requisito) y la borra de la
  base con su `invariant_violation`.
- **El humo** (`--tecnologia`) elige Albañilería si puede pagarla, porque su efecto se ve en la vida de una casa; con
  otra tecnología sólo comprueba el encargo, el plazo y el fin, y deja el efecto a los tests de simulación.

## 10. Decisiones de producto (2026-11-18)

Producto aceptó el 2026-11-18 la recomendación de cada pregunta. Cada fila deja la decisión (en negrita) y las
alternativas que se descartaron.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿De quién es una tecnología? | **Del jugador.** Cada jugador tiene una ciudad, así que en la práctica es lo mismo que de la ciudad, pero si algún día hay varias ciudades por jugador una mejora de «golpear más fuerte» se entiende del jugador. Alternativa: **de la ciudad**, que obligaría a investigar otra vez en cada ciudad nueva. |
| Q2 | ¿Dónde se investiga? | **En la ciudad, sin edificio nuevo.** Es una orden más del panel de la ciudad, como el avance de era. Alternativa: **un edificio propio** (una academia que hay que construir y que se puede derribar), que da al asedio un blanco más y retrasa la primera investigación, pero suma otro edificio, otra silueta y otra regla de asedio a un slice que ya es grande. |
| Q3 | ¿Cuántas tecnologías y cómo se reparten? | **Doce: ocho comunes, dos por era, y una exclusiva por civilización desde la Edad de Hierro (§4.1).** Cada jugador llega a nueve y ninguno a todas, que es lo que pide el pilar 4. Alternativas: **ramas excluyentes** (en cada era, elegir una de dos para siempre), que especializa más y fuerza a depender de otros, pero castiga una mala elección sin vuelta atrás; **un árbol grande** (25-30), que da más que decidir pero se equilibra a ciegas sin jugadores. |
| Q4 | ¿Qué pueden cambiar sus efectos? | **Las cinco claves de los rasgos de civilización y dos nuevas: el daño de cada golpe de una unidad que ataca (también contra un edificio) y la carga de un aldeano.** Son números que el jugador ya ve y puntos de consumo que ya existen o casi; con una mejora máxima de +21 % en velocidad y +44 % en cantidad sobre la base (§4.4). Alternativas: **sólo las cinco de E13**, sin tocar el combate; **también la vida de las unidades, el alcance o el castillo**, que piden reescalar la vida acumulada o tocar el disparo de E23. |
| Q5 | ¿Qué cuesta investigar? | **Madera y piedra en Piedra y Bronce; además bienes ajenos (30 en Hierro, 60 en Castillos); la exclusiva, 40 de su bien propio (§4.1).** Repite la escalera de los avances de era: lo avanzado pide comercio. Alternativas: **sólo madera y piedra**, más fácil, pero no crea comercio; **también oro o un recurso nuevo**, fuera de este slice. |
| Q6 | ¿Cuánto tarda? | **60 s en Piedra, 120 en Bronce, 180 en Hierro y 300 en Castillos, acelerado por `production.speed` (el rasgo y las tecnologías que lo llevan).** Es del orden de los avances de era (90, 180, 360 s) sin superarlos. Alternativa: **sin rasgo**, el mismo plazo para todos, más simple pero rompe la regla de que «la producción» la acelera el rasgo. |
| Q7 | ¿Qué pide cada tecnología antes? | **La era de su fila y, para las dos de Castillos, la común de Bronce o Hierro de su misma línea (`IRON_WEAPONS` pide `BRONZE_WEAPONS`, `ARCHITECTURE` pide `GUILDS`).** Un árbol poco profundo que se lee de un vistazo. Alternativas: **sólo la era**; **cadenas largas** (cada una pide la anterior de su era). |
| Q8 | ¿Qué puede hacer la ciudad mientras investiga? | **Todo menos avanzar de era o investigar otra cosa: entrena, construye y elabora.** Investigar no compite con el ejército ni con la economía, sólo con el avance, que es la otra progresión de la ciudad. Alternativas: **nada más** (bloquea también entrenar, como el avance), más duro y muy lento; **sin bloqueo** con el avance, que deja las dos progresiones en paralelo. |
| Q9 | ¿Se puede cancelar una investigación? | **No.** Como el avance de era y la elaboración: lo pagado no vuelve y la orden se piensa antes. Alternativa: **cancelar con la mitad del coste devuelta**, que pide una orden más y su regla de reembolso. |
| Q10 | ¿Se puede perder una tecnología? | **No, nunca.** Una ciudad no se destruye ni cambia de dueño, y la era no retrocede. Alternativa: **perderla con una derrota** (una guerra perdida, un saqueo grande), que añade castigo sin datos. |
| Q11 | ¿Se comparten con el clan o con un aliado, o se venden? | **No: cada jugador investiga las suyas.** La interdependencia viene de los bienes que piden, no de prestarse la tecnología. Alternativas: **un descuento** para los miembros de un clan donde alguien ya la tiene; **venderla en un trato**, que convierte el conocimiento en mercancía pero rompe la exclusividad de las de civilización. |
| Q12 | ¿La investigación hace avanzar de era o abre unidades y edificios? | **No: el avance de era sigue como está (recursos y tiempo, Q-12 del backlog), y las tecnologías sólo mejoran números.** Cerraría la `Q-12` del backlog con «la investigación no hace avanzar de era». Alternativas: **una tecnología por era que sea requisito del avance**, que cambia tres slices ya cerrados; **tecnologías que abran unidades o edificios**, un slice aparte. |
| Q13 | ¿Una investigación terminada va al registro o se avisa? | **No: como el avance de era, sólo `city.update` y el panel.** Es algo que el jugador encargó y cuyo plazo conoce. Alternativa: **una entrada en el registro y un aviso**, útil si vuelve tras horas fuera («Mientras no estabas: terminaste Gremios»). |
| Q14 | ¿Ven los demás las tecnologías de un jugador? | **No: son privadas.** Lo que se ve son sus efectos en el mapa. Alternativas: **públicas en la vista de la ciudad**, que da información para atacar o comerciar; **visibles para el clan**. |
| Q15 | ¿Los valores (costes, plazos, efectos) son los de §4.1? | **Sí, como punto de partida, ajustables por migración de catálogo.** Alternativa: proponer otros antes de aceptar. |

## 11. Documentos relacionados

- [civilizations.md](civilizations.md) (los rasgos por mil y sus fórmulas), [civilization-goods.md](civilization-goods.md)
  (los bienes ajenos y el propio), [civilization-units.md](civilization-units.md) (la unidad exclusiva y su coste en
  bien propio), [castle-age.md](castle-age.md) y [economy-stone.md](economy-stone.md) (los avances de era).
- [../roadmap/roadmap.md](../roadmap/roadmap.md) (fase Technology), [../roadmap/backlog.md](../roadmap/backlog.md)
  (`Q-12`), [../product/game-pillars.md](../product/game-pillars.md) (pilar 4) y
  [../database/schema.md](../database/schema.md) (las tablas de diseño).
