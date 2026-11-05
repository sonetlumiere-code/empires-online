# Civilizaciones: elegir una al nacer y jugar con sus rasgos

La civilización deja de ser una columna muerta: el jugador la elige al registrarse, no cambia
nunca y sus rasgos cambian de verdad cómo juega (lo rápido que anda, construye y entrena, lo que aguantan sus casas,
lo que se lleva al saquear y lo que tarda en recolectar).

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CIVILIZATION` |
| Estado | Accepted (2026-11-05). Propuesta en `Draft` ese mismo día; producto aceptó la recomendación de cada pregunta de §10. |
| Milestone | E13 Civilizaciones (decimotercer slice después de M7) |
| Canon | §0 (civilización y facción como ejes), §1.5 (determinismo), §11 (catálogos), §12 (persistencia), §13 (protocolo), §16 (errores) |
| Depende de | [player.md](player.md), [movement.md](movement.md), [economy-wood.md](economy-wood.md), [economy-forest.md](economy-forest.md), [economy-stone.md](economy-stone.md), [combat-raid.md](combat-raid.md), [combat-pillage.md](combat-pillage.md), [combat-siege.md](combat-siege.md), [trade.md](trade.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md) (la identidad vive en `apps/web`), [ADR-020](../decisions/ADR-020-stale-pending-accounts.md) (la cuenta pendiente y el reintento de la fundación), [ADR-029](../decisions/ADR-029-population-over-limit-and-house-destruction.md) (la vida de la casa). [ADR-035](../decisions/ADR-035-civilization-traits.md) (los rasgos como enteros por mil aplicados en el punto de consumo con la civilización del dueño en la RAM; migración `000015` y la 3 del almacén de identidad). |
| Reemplaza a | En [player.md](player.md): «`civilizationCode` / `factionCode` son parámetros de arranque» (§4) en lo que toca a la civilización, «Elección de civilización por parte del jugador en el alta» y «Efecto de gameplay de los traits» de su no-scope (§2), y el «Estado real en MVP: los traits están sembrados pero ningún sistema los lee» de §6.2 con su tabla de puntos de consumo. En [movement.md](movement.md), «Modificadores de velocidad por civilización: Fuera de MVP». Responde a la pregunta `Q-02` del [backlog](../roadmap/backlog.md) («¿la civilización se elige al crear el jugador y es inmutable?»). |
| Invariantes | Cita [INV-PLAYER-002](../invariants/player.md#inv-player-002) (lo precisa: la civilización la elige el jugador), [INV-PLAYER-003](../invariants/player.md#inv-player-003), [INV-MOVE-009](../invariants/movement.md#inv-move-009), [INV-ECON-005](../invariants/economy.md#inv-econ-005), [INV-ECON-008](../invariants/economy.md#inv-econ-008) e [INV-SEC-001](../invariants/security.md#inv-sec-001); asigna [INV-PLAYER-011](../invariants/player.md#inv-player-011) e [INV-PLAYER-012](../invariants/player.md#inv-player-012) (§8) |

## 1. Objetivo

Que exista el pilar 4 de [game-pillars.md](../product/game-pillars.md), el único que hoy no tiene ninguna mecánica.
El catálogo `civilizations` está sembrado desde la migración `000002` con cuatro civilizaciones y sus `traits`, y
cada jugador tiene su `civilization_id`; pero el Game Server funda a todos con el parámetro de arranque
`CivilizationID: 1` (todos son `ROMAN`) y ningún sistema lee un rasgo ([player.md](player.md) §6.2). Este slice hace
de la civilización una **elección del jugador con consecuencias**, y deja montada la capa de rasgos —determinista,
en enteros— sobre la que se apoyarán las capacidades exclusivas de cada civilización, las tecnologías y las
facciones.

## 2. Scope

**Entra.** Elegir la civilización en el registro (el formulario, `POST /api/auth/register` y la fundación interna
`POST /internal/players`), y guardarla en la cuenta pendiente para que el reintento de la fundación funde con la
misma; los rasgos como enteros por mil (migración `000015`); la simulación conoce la civilización de cada jugador
(se carga con el mundo y llega con `IntroducePlayer`); cinco rasgos aplicados en los sistemas que ya existen
(§4.2): velocidad de las unidades, duración de las obras y del entrenamiento, vida de las casas, botín y tiempo de
recolección; la civilización en la vista de ciudad, propia y ajena; en el cliente, la elección en el registro con
lo que da y lo que cuesta cada una, la civilización en el HUD y en el panel de una ciudad ajena; la semilla de
desarrollo puede asignar civilización a sus cuentas; comprobaciones en el humo.

**No entra** (`Fuera de MVP` de este slice): capacidades exclusivas por civilización —una unidad, un edificio o una
tecnología propios— (Q6); cambiar de civilización después del alta (Q2); elegir la facción (sigue `NEUTRAL`, Q10);
rasgos sobre el combate (daño, vida o alcance de las unidades), sobre el avance de era y sobre el comercio (Q5); el
pipeline de modificadores con `ADD`/`MUL`/`SET_MIN`/`SET_MAX`, `scope` y `source` que [player.md](player.md) §6.2 ya
deja fuera; modificadores por tecnología, territorio o era; mandar los rasgos por el protocolo (§6); civilizaciones
nuevas; reequilibrar con datos de jugadores (los valores son los de `000002`, Q4 y Q8).

## 3. Actores

Jugador que se registra (elige), cliente web (el formulario, el HUD), `apps/web` (la API de registro y el almacén de
identidad, [ADR-018](../decisions/ADR-018-identity-store-and-ticket-issuer.md)), Game Server (valida el código en la
fundación y aplica los rasgos en la simulación, [INV-SEC-001](../invariants/security.md#inv-sec-001)), PostgreSQL
(el catálogo y la civilización del jugador), los demás jugadores (ven la civilización de una ciudad ajena).

## 4. Modelo

### 4.1 Elegir la civilización

El formulario de registro pide, además del nombre y la contraseña, una de las cuatro civilizaciones del catálogo,
sin ninguna preseleccionada (Q1). Cada opción muestra lo que da y lo que cuesta en porcentajes (Q9). El código viaja
en `POST /api/auth/register` (`civilization`), se guarda en la cuenta `PENDING` del almacén de identidad y llega al
Game Server en `POST /internal/players` (`civilizationCode`), que lo resuelve contra el catálogo y lo escribe en
`players.civilization_id` en la misma transacción del alta ([INV-PLAYER-003](../invariants/player.md#inv-player-003)).
Si la fundación no termina y el jugador vuelve a entrar, el reintento funda con la civilización guardada en la
cuenta ([ADR-020](../decisions/ADR-020-stale-pending-accounts.md)): nunca con un valor por defecto.

La civilización **no cambia nunca** después del alta (Q2). Los jugadores que ya existen siguen siendo `ROMAN` (Q3).
La facción no se elige: sigue `NEUTRAL` hasta la spec de facciones (Q10).

### 4.2 Los rasgos

Un rasgo es un **entero por mil**: `1000` es «sin efecto», `1100` es +10 % y `950` es −5 %. Las claves cambian de
nombre para decir dónde se aplican, y `economy.trade_yield` no se aplica (Q5). Con los valores sembrados en `000002`
(Q4), y la sustitución del rasgo persa de Q5:

| Clave nueva (antes) | Qué cambia | Base | `ROMAN` | `BYZANTINE` | `PERSIAN` | `NORSE` |
|---|---|---|---|---|---|---|
| `unit.move_speed` | Lo que tarda cada paso de toda unidad del jugador | 600 ms/tile | 1000 → 600 | 950 → 632 | 1050 → 571 | 1100 → 545 |
| `production.speed` (`build.speed`) | Lo que tarda una obra y un entrenamiento (Q4) | casa 15 000 ms; aldeano 20 000; guerrero 25 000 | 1100 → 13 636; 18 182; 22 727 | 1000 → sin cambio | 950 → 15 789; 21 053; 26 316 | 900 → 16 667; 22 222; 27 778 |
| `building.house_hp` (`defense.wall_hp`) | La vida de una casa frente al asedio | 150 | — | 1200 → 180 | — | — |
| `combat.loot_carry` (`combat.raid_bonus`) | Lo que carga un guerrero al saquear o al recoger botín | 20 | — | — | — | 1150 → 23 |
| `economy.gather_speed` (`economy.gather_rate`) | Lo que tarda el trabajo en el tile de cada viaje de recolección | 10 000 ms | 1000 → sin cambio | — | 1100 → 9 091 (en lugar de `economy.trade_yield`, Q5) | — |

Ninguna civilización tiene más de tres rasgos ni toca la población, el coste en recursos, el daño ni la vida de las
unidades. Una clave ausente vale `1000`.

### 4.3 Cómo se aplica

- **Aritmética entera** ([INV-MOVE-009](../invariants/movement.md#inv-move-009), canon §1.5). Un rasgo de velocidad
  divide la duración: `(base × 1000 + r/2) / r`. Un rasgo de cantidad la multiplica: `(base × r + 500) / 1000`.
  Las dos redondean al entero más cercano y nunca bajan de 1, como `movement.StepDurationMs`.
- **Al dar la orden.** El rasgo se aplica al construir la polilínea, al encargar la obra o el entrenamiento y al
  empezar cada trabajo en el tile; el plazo que resulta queda fijado en su fila o en la RAM, como hoy. Lo que está en
  curso termina en el mismo instante absoluto tras un reinicio ([INV-ECON-005](../invariants/economy.md#inv-econ-005))
  y no cambia si una migración de balance cambia el catálogo; la siguiente orden ya usa el valor nuevo.
- **La vida de la casa** sale de la civilización del dueño de su ciudad cada vez que se lee: una casa bizantina
  aguanta 180 y llega así a `BuildingView.maxHp`. El daño acumulado no se reescala.
- **El botín** se mide al tomar cada golpe, con la civilización del dueño del guerrero. Un rasgo de cantidad nunca
  crea recurso: el botín sale del almacén atacado o del muerto, igual que hoy
  ([INV-ECON-008](../invariants/economy.md#inv-econ-008)).
- **Un único punto de consulta.** Toda lectura pasa por el dominio (`Civilization.Trait` con su clave, que pasa a
  devolver el entero), nunca por el código de la civilización (`RN-PLAYER-012`).

### 4.4 Reglas

| ID | Regla |
|---|---|
| `RN-CIV-001` | La civilización se elige en el alta entre las del catálogo. Es obligatoria y no tiene valor por defecto; un código desconocido se rechaza (§5). |
| `RN-CIV-002` | La civilización de un jugador no cambia después del alta. |
| `RN-CIV-003` | Todo rasgo es un entero positivo por mil; `1000` es «sin efecto» y una clave ausente vale `1000`. La carga rechaza un catálogo con un rasgo que no lo sea. |
| `RN-CIV-004` | Un rasgo de velocidad divide la duración y uno de cantidad multiplica el valor, en aritmética entera con redondeo al más cercano y un mínimo de 1 (§4.3). |
| `RN-CIV-005` | El rasgo se aplica al dar la orden y su resultado queda fijado en el plazo; lo que está en curso no cambia. |
| `RN-CIV-006` | La vida máxima de una casa es la de su tipo por el rasgo de la civilización del dueño de su ciudad. |
| `RN-CIV-007` | La carga de botín de un guerrero es la de su tipo por el rasgo de la civilización de su dueño. |
| `RN-CIV-008` | Ningún rasgo multiplica lo que un jugador recibe de otro. Lo que pasa entre dos jugadores se entrega tal cual. |
| `RN-CIV-009` | La civilización de una ciudad es pública: viaja en su vista para todo el que la ve. |

### 4.5 Qué ve el jugador

- **Registro**: cuatro opciones con su nombre y lo que da y cuesta («+10 % velocidad de movimiento, −10 % velocidad
  de producción»). No se puede enviar sin elegir una, y el texto dice que la elección es para siempre.
- **HUD**: la civilización propia junto a la era.
- **Ciudad ajena**: su civilización en el panel, junto a la era y el almacén que ya se ven
  (Q9 de [combat-pillage.md](combat-pillage.md)).
- Las cuentas atrás de obra, entrenamiento y movimiento ya salen de los plazos del servidor, así que muestran el rasgo
  aplicado sin que el cliente lo calcule. Igual con la vida de la casa (`maxHp`).

## 5. Errores

| Condición | Respuesta |
|---|---|
| `POST /api/auth/register` sin `civilization` o con un código que no está en el catálogo | `400 INVALID_MESSAGE`; no se crea la cuenta. El formulario no deja llegar aquí. |
| `POST /internal/players` con un `civilizationCode` desconocido (una web desplegada contra otro catálogo) | `400` definitivo: la web borra la cuenta pendiente y el nombre queda libre ([ADR-020](../decisions/ADR-020-stale-pending-accounts.md)). |
| El catálogo tiene un rasgo que no es un entero positivo | El Game Server no arranca (validación de carga). |
| Órdenes del WebSocket | Ningún error nuevo: un rasgo cambia plazos y cantidades, nunca rechaza una orden. |

## 6. Eventos y contratos de red

**Eventos.** Ninguno nuevo. `PlayerBootstrapped` lleva en su payload el código de la civilización elegida. Nada de
este slice entra en el registro ni en los avisos.

**WebSocket (protocolo v1).** `CityView` gana `civilization` (un `CivilizationCode`: `ROMAN`, `BYZANTINE`, `PERSIAN`,
`NORSE`), en Zod y en Go; `city.update` no lo lleva, porque no cambia. No hay mensajes nuevos: siguen 23 y 23.

**HTTP.** `POST /api/auth/register` gana `civilization`; `POST /internal/players`, `civilizationCode`. La lectura
`GET /internal/players/{id}` no cambia.

**Los rasgos no viajan** ([player.md](player.md) §6.2: los valores efectivos son del servidor). Lo que el cliente
enseña en el registro sale de un catálogo de presentación en `packages/protocol` (código, nombre y rasgos), que un
test de integración compara con la tabla `civilizations` para que no se desvíen en silencio (§7).

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | La civilización de cada jugador y el catálogo de rasgos, inmutables durante el proceso |
| Write-through | `players.civilization_id`, en la transacción del alta como hoy |
| Dirty-flag | Nada nuevo |
| Reconstruible / no persistido | Los valores efectivos (ms por tile, vida máxima, carga): se recalculan de la base y del rasgo; sólo los plazos ya fijados se guardan, como hoy |

**Por qué hace falta un ADR ([ADR-035](../decisions/ADR-035-civilization-traits.md)).** Es el primer sistema de modificadores y fija dónde vive cada número. La
propuesta: rasgos **enteros por mil** en `civilizations.traits`, aplicados **en el punto de consumo** con la
civilización del dueño, que la simulación tiene en la RAM. El ADR deja escritas, entre otras, estas alternativas
descartadas:

1. **Mantener los `float64` de hoy** y multiplicar al aplicar: un `1.1 × 15 000` en coma flotante puede diferir en el
   último bit entre plataformas y, tras redondear, en un milisegundo, que rompe los golden y la reproducibilidad
   ([INV-MOVE-009](../invariants/movement.md#inv-move-009) explica el mismo caso con la diagonal).
2. **El pipeline completo** (`ADD`/`MUL`/`SET_MIN`/`SET_MAX` con orden y origen): hace falta cuando se combinen
   civilización, tecnología y territorio; con un solo origen es complejidad sin uso.
3. **Congelar los valores efectivos en las filas** (`ms_per_tile` en `units`, `max_hp` en `buildings`): duplica un
   dato derivado y una migración de balance dejaría de aplicarse a lo existente.
4. **Mandar los rasgos en el protocolo**: el cliente no los necesita para jugar, porque los plazos y la vida ya
   llegan resueltos; sólo para el texto del registro, que se sirve antes de tener sesión.

**Migración `000015`:** reescribe `civilizations.traits` con las claves nuevas en enteros por mil (con la sustitución
de Q5) y añade un trigger que rechaza un `UPDATE` de `players.civilization_id` (`RN-CIV-002`). `players` no cambia de
columnas. **Identidad (`apps/web`):** su migración 3 añade `accounts.civilization`, con `ROMAN` para las cuentas que
ya existen.

## 8. Invariantes

**Cita** [INV-PLAYER-003](../invariants/player.md#inv-player-003) (la civilización se escribe en la transacción del
alta), [INV-MOVE-009](../invariants/movement.md#inv-move-009) (la polilínea con el rasgo sigue siendo función pura de
sus datos), [INV-ECON-005](../invariants/economy.md#inv-econ-005) (lo que está a medias termina en el mismo instante),
[INV-ECON-008](../invariants/economy.md#inv-econ-008) (ningún rasgo crea recurso) e
[INV-SEC-001](../invariants/security.md#inv-sec-001) (el cliente manda un código, no un rasgo).

**Precisa** [INV-PLAYER-002](../invariants/player.md#inv-player-002): la civilización deja de ser un parámetro de
arranque y la elige el jugador; su «no se asigna nunca una civilización por defecto» pasa a ser cierto también en el
reintento de la fundación.

**Asigna** dos invariantes nuevos de la familia `INV-PLAYER`, registrados en [../invariants/player.md](../invariants/player.md):

- [INV-PLAYER-011](../invariants/player.md#inv-player-011) — la civilización de un jugador no cambia después del
  alta (trigger de `000015`).
- [INV-PLAYER-012](../invariants/player.md#inv-player-012) — todo rasgo de civilización es un entero positivo por mil
  y se aplica sólo con aritmética entera (validación de carga y tests de la fórmula).

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | Las dos fórmulas de §4.3 con los valores de la tabla de §4.2, el redondeo y el mínimo de 1; `Trait` con clave ausente y presente; la validación del catálogo (cero, negativo, decimal, texto); el rasgo en la polilínea, la obra, el entrenamiento, el trabajo de recolección, la vida de la casa y la carga de botín |
| simulation | Golden por rasgo con los ticks exactos: un aldeano nórdico llega antes que uno bizantino por la misma ruta; una casa romana se completa a los 13 636 ms; una casa bizantina aguanta 180; un guerrero nórdico saquea 23; un aldeano persa trabaja 9 091 ms. Los golden que ya existen se fijan con una civilización neutra de test (todo a `1000`) para que no cambien |
| integration | El alta con cada civilización escribe la suya; el trigger rechaza cambiarla; un código desconocido se rechaza sin crear nada; la carga lee los rasgos enteros y rechaza uno inválido; el catálogo de presentación de `packages/protocol` coincide con la tabla |
| recovery | Un movimiento, una obra y un entrenamiento con rasgo en curso terminan tras un reinicio en el mismo instante absoluto |
| contract / e2e | `CityView.civilization` contra el JSON Schema; el registro y la fundación con y sin código (web: `authService` y `edges`; Go: la fundación interna); el reintento de una cuenta pendiente funda con la civilización guardada |
| cliente | El formulario no se envía sin elegir; los textos de cada opción; la civilización en el HUD y en el panel de una ciudad ajena; el humo comprueba la civilización en la vista y el plazo de una casa romana |

## 10. Decisiones de producto (2026-11-05)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de producto,
no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Cuándo y cómo se elige la civilización? | **En el formulario de registro, obligatoria y sin ninguna preseleccionada.** Elegirla después (en la primera partida) dejaría un jugador sin civilización mientras tanto, que [INV-PLAYER-002](../invariants/player.md#inv-player-002) no admite; preseleccionar una haría que la mayoría la aceptara sin leer, y la elección es para siempre. |
| Q2 | ¿Se puede cambiar después? | **No, nunca en este slice** (responde `Q-02`). Es lo que da peso a la elección y lo que hará que las capacidades exclusivas creen dependencia ([vision.md](../product/vision.md) §5). Un cambio con coste y espera, si algún día hace falta, es otra spec. |
| Q3 | ¿Qué pasa con los jugadores que ya existen? | **Siguen siendo `ROMAN`**, sin elegir. Hoy sólo existen las cuentas de desarrollo y del humo; la semilla de desarrollo podrá asignar civilizaciones a sus cuentas para probar. Si cuando haya jugadores reales se lanza con jugadores ya creados, se decide entonces. |
| Q4 | ¿A qué se aplican los rasgos sembrados? | **A cinco sistemas que ya existen** (§4.2): velocidad de todas las unidades; duración de las obras **y del entrenamiento** (`build.speed` → `production.speed`: sólo con la casa, el bono romano de 1,4 s sería invisible); vida de las casas (`defense.wall_hp`: las murallas no tienen vida, la casa es lo único que se asedia); carga de botín (`combat.raid_bonus`: saqueo y botín de combate, no el daño, para no tocar el balance de E3); tiempo de recolección (`economy.gather_rate`). El avance de era no se toca. |
| Q5 | ¿Qué hacemos con el rasgo persa `economy.trade_yield` (+15 % en el comercio)? | **No aplicarlo y darle al persa `economy.gather_speed` 1100** (recolecta un 10 % más rápido). Si el persa recibiera un 15 % más de cada trato, el recurso saldría de la nada: dos cuentas que se pasan madera con «doy 1000, pido 1» (posible a propósito, Q3 de [trade.md](trade.md)) la multiplicarían sin fin. Alternativa: dejar al persa sólo con +5 % de velocidad y −5 % de producción, que es la civilización más floja. |
| Q6 | ¿Sólo rasgos, o también una capacidad exclusiva por civilización en este slice? | **Sólo rasgos.** Una capacidad exclusiva (una unidad, un edificio o un recurso que sólo una civilización produce) es lo que de verdad crea comercio según el pilar 4, pero cada una es una unidad o un edificio nuevo con su balance, su protocolo y su dibujo: cuatro a la vez son un slice entero. La capa de rasgos de este slice es la que esas capacidades necesitan; van en el siguiente. |
| Q7 | ¿Ven los demás la civilización de un jugador? | **Sí, en el panel de su ciudad**, como la era y el almacén. Es identidad, no un secreto, y saber que el vecino es nórdico (saquea más) es información para decidir con quién pactar. Ocultarla sería niebla de guerra, otra spec. |
| Q8 | ¿Se mantienen los valores sembrados, con sus penalizaciones? | **Sí, como punto de partida** (sin datos de jugadores, como el resto del balance): el bizantino anda un 5 % más lento, el persa produce un 5 % más lento y el nórdico un 10 %; el romano no paga nada por su +10 % de producción y es la opción «sin sorpresas». Los números se ajustan con una migración cuando haya datos. |
| Q9 | ¿Se enseñan los números en el registro? | **Sí, en porcentajes** («+10 % velocidad de movimiento»). Es una elección que no se puede deshacer: tiene que poder compararse. |
| Q10 | ¿Se elige también la facción en el alta? | **No: sigue `NEUTRAL`** hasta la spec de facciones (`Q-03`), que decidirá si se puede cambiar y qué hace. Elegirla ahora sin ningún efecto sería una elección vacía que después habría que respetar. |

## 11. Documentos relacionados

- [player.md](player.md) (los dos ejes, el catálogo y `Civilization.Trait`) y
  [../invariants/player.md](../invariants/player.md) (`INV-PLAYER`).
- [../product/game-pillars.md](../product/game-pillars.md) (pilar 4) y [../product/vision.md](../product/vision.md) §5.
- [movement.md](movement.md) (la polilínea temporizada), [economy-wood.md](economy-wood.md) (obras y entrenamiento),
  [economy-forest.md](economy-forest.md) y [economy-stone.md](economy-stone.md) (el trabajo en el tile),
  [combat-pillage.md](combat-pillage.md) (el botín), [combat-siege.md](combat-siege.md) (la vida de la casa),
  [trade.md](trade.md) (por qué el comercio no lleva rasgo).
- [../roadmap/backlog.md](../roadmap/backlog.md) (`Q-02`, `Q-03`).
