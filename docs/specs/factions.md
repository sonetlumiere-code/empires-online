# Facciones: elegir bando y que el bando cuente

Propuesta para que la Global Faction (`ORDER`, `CHAOS`, `NEUTRAL`) deje de ser una columna muerta: el jugador puede
unirse a un bando, el bando decide a quién se puede agredir y con quién se puede firmar la paz, y la tierra del propio
bando defiende a sus unidades.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-FACTIONS` |
| Estado | Accepted (2026-11-15). Propuesta en `Draft` y aceptada ese mismo día, con la recomendación de cada pregunta de §10. |
| Milestone | E17 Facciones (decimoséptimo slice después de M7) |
| Canon | §0 (civilización y facción como ejes), §1.5 (determinismo), §11 (catálogos), §12 (persistencia), §13 (protocolo), §16 (errores) |
| Depende de | [player.md](player.md), [civilizations.md](civilizations.md), [diplomacy.md](diplomacy.md), [trade.md](trade.md), [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md), [combat-siege.md](combat-siege.md), [territory.md](territory.md), [territory-conquest.md](territory-conquest.md), [caravans.md](caravans.md), [event-log.md](event-log.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-016](../decisions/ADR-016-foreign-territory-actions.md) (qué es una acción hostil), [ADR-027](../decisions/ADR-027-hot-treaties.md) (los tratados en caliente y su preaviso), [ADR-034](../decisions/ADR-034-territory-claims.md) (la conquista de territorio), [ADR-035](../decisions/ADR-035-civilization-traits.md) (los rasgos como enteros por mil aplicados en el punto de consumo). [ADR-039](../decisions/ADR-039-faction-peace-and-affinity.md) (la facción como segunda fuente de paz, la entrada en dos fases con su preaviso y su vencimiento en una transacción, y la tierra afín calculada en cada golpe): ver §7. |
| Reemplaza a | En [player.md](player.md): `RN-PLAYER-013` («la facción no restringe ninguna acción del MVP»), «Mecánica de facciones (bonos, guerra ORDER/CHAOS, reputación): `Fuera de MVP`» de su no-scope (§2) en lo que toca a las reglas de agresión y los bonos, y que la facción sea un parámetro de arranque (§4) en lo que toca a poder cambiarla en partida. En [civilizations.md](civilizations.md), la Q10 («la facción sigue `NEUTRAL` hasta la spec de facciones»). Precisa `RN-DIPLO-001` de [diplomacy.md](diplomacy.md) (un tipo más de oferta prohibida) y `RN-COMBAT-006` de [combat-raid.md](combat-raid.md) (la paz entre dos jugadores ya no sale sólo de un tratado). Responde a la pregunta `Q-03` del [backlog](../roadmap/backlog.md) («¿la Global Faction se puede cambiar, con qué coste y con qué consecuencias sobre treaties y territorios?»). |
| Invariantes | Cita [INV-PLAYER-002](../invariants/player.md#inv-player-002) (lo precisa: la facción puede cambiar una vez), [INV-DIPLO-002](../invariants/diplomacy.md#inv-diplo-002), [INV-COMBAT-002](../invariants/combat.md#inv-combat-002), [INV-MOVE-009](../invariants/movement.md#inv-move-009), [INV-SEC-001](../invariants/security.md#inv-sec-001), [INV-SEC-004](../invariants/security.md#inv-sec-004), [INV-SEC-007](../invariants/security.md#inv-sec-007) e [INV-PERSIST-005](../invariants/persistence.md#inv-persist-005); asigna [INV-PLAYER-013](../invariants/player.md#inv-player-013), [INV-COMBAT-006](../invariants/combat.md#inv-combat-006) e [INV-DIPLO-003](../invariants/diplomacy.md#inv-diplo-003) (§8) |

## 1. Objetivo

Que exista el segundo eje de la visión. [vision.md](../product/vision.md) §5 separa la civilización (*cómo juegas,
qué produces*) de la Global Faction (*con quién estás alineado*), y desde E13 el primer eje tiene mecánica; el segundo
no tiene ninguna: el catálogo `factions` está sembrado desde `000002`, cada jugador tiene su `faction_id`, y todos
nacen `NEUTRAL` por un parámetro de arranque (`httpapi.Options.FactionID`). Hoy dos jugadores sólo son «del mismo
lado» si firman un tratado, uno a uno. Este slice da al mundo un **mapa político**: bloques que se reconocen sin
negociar, que no pueden firmar la paz con el bloque contrario, y cuya tierra protege a los suyos. El conflicto que ya
existe (incursión, saqueo, asedio, conquista, captura de caravanas) gana un marco: a quién conviene atacar y dónde.

## 2. Scope

**Entra.** Unirse a `ORDER` o a `CHAOS` desde `NEUTRAL`, en partida y una sola vez, con un preaviso antes de que
cuente (Q1, Q2, Q3); la **paz de la Orden**: dos jugadores de `ORDER` no pueden agredirse (Q4); entre `ORDER` y
`CHAOS` no hay pacto de no agresión ni alianza, y los que existan al unirse empiezan su preaviso de ruptura (Q5); el
**territorio afín** y su efecto: las unidades reciben menos daño en tierra de su bando (Q6, Q7); la facción, y la
entrada pendiente, en la vista de ciudad, propia y ajena (Q8); la migración `000019`; en el cliente, el bando en el
HUD y en el panel de una ciudad ajena, «Elegir bando» con lo que da y lo que cuesta cada uno, la cuenta atrás de la
entrada, el bando del dueño de un territorio y los rechazos con su motivo; el humo con cuentas propias.

**No entra** (`Fuera de MVP` de este slice): elegir la facción en el registro (Q1); volver a `NEUTRAL` o cambiar de
`ORDER` a `CHAOS` y al revés (Q2); objetivos globales del bando, puntuación, ranking o recompensas por bando (Q10);
territorios cuyo dueño es una facción o un clan (los dueños `FACTION` y `CLAN` que [territory-conquest.md](territory-conquest.md)
§2 deja fuera); reputación o «criminalidad» por agredir (Q4); canales de mensajes por bando ([messages.md](messages.md)
§2); que el bando cambie la velocidad, la producción, la recolección o el botín; el efecto sobre las casas (Q6);
facciones nuevas; reequilibrar con datos de jugadores.

## 3. Actores

Jugador (elige bando, agrede o no, firma o no), los demás jugadores (ven el bando de una ciudad ajena y sufren sus
reglas), cliente web, Game Server (valida la orden en el tick, aplica la paz y la afinidad en la simulación, vence el
preaviso en la fase 5, [INV-SEC-001](../invariants/security.md#inv-sec-001)), PostgreSQL (la facción y la entrada
pendiente del jugador, los tratados que rompe la entrada). Redis no participa. El almacén de identidad no participa
(Q1).

## 4. Modelo

### 4.1 Los tres bandos

| Bando | A quién puede agredir | Con quién puede firmar `NON_AGGRESSION` o `ALLIANCE` | Tierra afín |
|---|---|---|---|
| `NEUTRAL` | A cualquiera | Con cualquiera | Sólo la suya (Q7) |
| `ORDER` | A cualquiera **menos a otro de `ORDER`** (la paz de la Orden) | Con `ORDER` y `NEUTRAL`; **nunca con `CHAOS`** | La de cualquier jugador de `ORDER` |
| `CHAOS` | A cualquiera, también a otro de `CHAOS` | Con `CHAOS` y `NEUTRAL`; **nunca con `ORDER`** | La de cualquier jugador de `CHAOS` |

`TRADE` se firma entre cualesquiera dos bandos (Q5): los bienes ajenos que pide Hierro ([civilization-goods.md](civilization-goods.md))
no deben depender del bando. Ningún bando tiene rasgos propios ni toca la civilización: los dos ejes siguen
ortogonales (`RN-PLAYER-005`) y cualquier combinación de civilización y bando es válida.

«Agredir» es exactamente lo que ya es hostil en `internal/game/simulation/actions.go`: `unit.attack` (también contra
una caravana, que es como se captura), `unit.pillage`, `unit.demolish` y `unit.claim` sobre un territorio con dueño.
La respuesta automática de E3 ([combat-response.md](combat-response.md)) no es una orden, pero tampoco puede empezar
entre dos jugadores en paz, porque nadie puede haber golpeado primero.

### 4.2 Unirse a un bando

`faction.join { faction }` con `faction` en `ORDER` o `CHAOS`. Es un comando de la simulación (pasa por el tick: la
facción cambia lo que la simulación permite), **no hostil**. En orden:

1. El esquema (`INVALID_MESSAGE` si el código no es `ORDER` ni `CHAOS`).
2. El jugador es `NEUTRAL` y no tiene una entrada pendiente (`INVALID_TARGET`, con el motivo).
3. Se apunta la **entrada pendiente** `{ faction, atMs }`, con `atMs` = ahora + `EO_FACTION_JOIN_SECONDS` (900 s,
   Q3). Va en dos fases, como el encargo de un avance de era: el tick la reserva —todavía no la ve nadie—, el trabajo
   `faction.join` de la cola la escribe, y al volver por el buzón se confirma y su ciudad se difunde con `factionJoin`
   (un rechazo de la base la borra y responde el código). La entrada no se puede retirar (Q2).

Durante el preaviso **nada cambia**: el jugador sigue siendo `NEUTRAL` a todos los efectos, y todos ven la entrada
pendiente en su ciudad (Q8). Al vencer, en la fase 5 —o en el tick siguiente si alguno de sus pares con un tratado que
va a romper tiene una orden de tratado en vuelo ([ADR-039](../decisions/ADR-039-faction-peace-and-affinity.md) §4)—,
en el mismo tick:

- la facción del jugador pasa a ser la elegida en la RAM, y un único trabajo de la cola (`faction.joined`) escribe en
  una transacción la facción, el borrado de la entrada y las rupturas del punto siguiente;
- todo `NON_AGGRESSION` o `ALLIANCE` vigente con un jugador del bando contrario que no esté ya en preaviso empieza su
  preaviso de ruptura (`RN-DIPLO-004`) como si lo hubiera roto quien se une, con su evento `TreatyBreakAnnounced` y
  su entrada en el registro del otro (Q5);
- toda oferta pendiente de `NON_AGGRESSION` o `ALLIANCE` con el bando contrario se rechazará al responderla
  (`RN-FAC-005`); no se retira sola;
- si se une a `ORDER`, los enfrentamientos, saqueos, derribos y estandartes entre él y otros jugadores de `ORDER`
  terminan como cuando nace un tratado (`RN-COMBAT-008`, `RN-COMBAT-025` y la conquista);
- su ciudad se difunde con `faction` y `factionJoin: null`, y cada territorio suyo con su `ownerFaction` nuevo.

Un reinicio no pierde el preaviso: la entrada pendiente se carga con el mundo y, si venció mientras el servidor estaba
parado, se aplica en el primer tick.

### 4.3 La paz de la Orden

La simulación tiene hoy un único punto que dice si dos jugadores están en paz (`State.AtPeace`, consultado por el
combate, el saqueo, el asedio y la conquista). Ese punto pasa a responder «sí» también cuando **los dos jugadores son
de `ORDER`**, con la facción efectiva, nunca con la pendiente. Así las cuatro acciones hostiles, la captura de
caravanas y la respuesta automática quedan cubiertas por una sola regla, y el rechazo dice el motivo («los dos sois de
la Orden») en lugar del de un tratado.

La paz de la Orden no permite guarnecer: eso sigue siendo cosa de la alianza (`allows_garrison`). Tampoco impide
proponer un tratado entre dos de `ORDER`, que sigue sirviendo para el comercio y la guarnición.

### 4.4 El territorio afín

Un territorio es **afín** a un jugador cuando su dueño es él mismo, o cuando el jugador es de `ORDER` o de `CHAOS` y
el dueño es de su mismo bando (Q7). Un territorio libre no es afín a nadie. La afinidad se calcula **en el momento del
golpe** con la facción efectiva del dueño y el control del territorio en la RAM: si el territorio cambia de manos o el
dueño se une a un bando, el siguiente golpe ya lo nota.

**Efecto (Q6).** Una unidad que recibe un golpe mientras está en un tile de territorio afín a su dueño recibe el daño
por `EO_FACTION_AFFINE_DAMAGE_PERMILLE` (800, un 20 % menos), con la aritmética de los rasgos de E13: `(daño × r + 500)
/ 1000`, nunca menos de 1 ([INV-MOVE-009](../invariants/movement.md#inv-move-009), `RN-CIV-004`). Con los números de
hoy, un golpe de guerrero quita 6 en vez de 8 y un guerrero aguanta 10 golpes en vez de 8; una caravana, 14 en vez de
10. Se aplica a toda unidad (también a los aldeanos y a las caravanas) y a ningún edificio: el asedio de una casa no
cambia. Cuando los dos son del mismo bando y luchan en tierra de ese bando, los dos reciben el bono.

### 4.5 Reglas

Dominio: `RN-FAC`.

| ID | Regla |
|---|---|
| `RN-FAC-001` | Todo jugador nace `NEUTRAL`. La facción la cambia sólo el propio jugador, con `faction.join`, y sólo de `NEUTRAL` a `ORDER` o a `CHAOS`, una vez (Q1, Q2). |
| `RN-FAC-002` | Una entrada en un bando cuenta al vencer su preaviso de `EO_FACTION_JOIN_SECONDS`; hasta entonces el jugador es `NEUTRAL` a todos los efectos. La entrada no se retira y sobrevive a un reinicio (Q3). |
| `RN-FAC-003` | **La paz de la Orden.** Entre dos jugadores de `ORDER` ninguna acción hostil se acepta ni sigue en curso: `INVALID_TARGET` al darla, y fin del enfrentamiento, saqueo, derribo o estandarte en el tick en que los dos pasan a ser de `ORDER` (Q4). |
| `RN-FAC-004` | `CHAOS` y `NEUTRAL` no tienen ninguna restricción de agresión por su bando; los tratados, la protección y las Safe Zones siguen como hoy. |
| `RN-FAC-005` | Entre un jugador de `ORDER` y uno de `CHAOS` no se propone ni se acepta un `NON_AGGRESSION` ni una `ALLIANCE` (`INVALID_TARGET`); `TRADE`, sí (Q5). |
| `RN-FAC-006` | Al vencer la entrada en un bando, todo `NON_AGGRESSION` o `ALLIANCE` vigente con el bando contrario que no esté ya en preaviso empieza el suyo, como si lo hubiera roto quien se une (Q5). |
| `RN-FAC-007` | **Territorio afín.** Es afín a un jugador el territorio que es suyo y, si es de `ORDER` o de `CHAOS`, el de cualquier jugador de su bando. Se evalúa en cada golpe con la facción efectiva y el control de la RAM (Q7). |
| `RN-FAC-008` | Una unidad golpeada en un tile de territorio afín a su dueño recibe el daño por `EO_FACTION_AFFINE_DAMAGE_PERMILLE`, en enteros, redondeado al más cercano y nunca menos de 1. Los edificios no (Q6). |
| `RN-FAC-009` | La facción de un jugador y su entrada pendiente son públicas: viajan en la vista de su ciudad para todo el que la ve (Q8). |
| `RN-FAC-010` | La facción y la civilización son independientes: ninguna regla de este slice lee una para decidir sobre la otra (`RN-PLAYER-005`). |

### 4.6 Qué ve el jugador

- **HUD**: el bando propio junto a la civilización y la era. Si es `NEUTRAL` sin entrada pendiente, un botón
  «Elegir bando». Con la entrada pendiente, «Te unes a la Orden en 12:30».
- **Elegir bando**: una ventana con los dos bandos y lo que da y cuesta cada uno, en frases del §4.1 («No puedes
  atacar a otros de la Orden ni ellos a ti», «No puedes firmar la paz con el Caos», «En tierra de la Orden tus
  unidades reciben un 20 % menos de daño»), los pactos y alianzas que se romperán, y que la decisión es para siempre.
  Confirmar envía `faction.join`.
- **Ciudad ajena**: su bando (y su entrada pendiente, con la cuenta atrás) en el panel, junto a la civilización.
  La barra de diplomacia no ofrece «Proponer pacto» ni «Proponer alianza» si los bandos lo impiden, y no ofrece
  «Atacar» contra una unidad de `ORDER` si el jugador es de `ORDER`; el servidor rechaza igual si llega.
- **Territorio**: el panel de un territorio con dueño dice el bando del dueño y, si es afín, «Tierra afín: tus unidades
  reciben un 20 % menos de daño».
- Un rechazo del servidor sale con su motivo («Los dos sois de la Orden», «La Orden y el Caos no firman la paz»).
- El cliente copia del servidor el 20 % para el texto, como ya copia los costes de Bronce y Hierro
  (`src/state/economy.ts`); el daño real lo dicen los `hp` que llegan.

## 5. Errores

Sólo códigos que ya existen en el catálogo (canon §16).

| Código | Condición |
|---|---|
| `INVALID_MESSAGE` | `faction.join` con un código que no es `ORDER` ni `CHAOS` (también `NEUTRAL`). |
| `INVALID_TARGET` | `faction.join` de un jugador que ya no es `NEUTRAL` o que tiene una entrada pendiente; una acción hostil entre dos de `ORDER` (`RN-FAC-003`); proponer o aceptar un `NON_AGGRESSION` o una `ALLIANCE` entre `ORDER` y `CHAOS` (`RN-FAC-005`). El mensaje dice cuál. |
| `CITY_PROTECTED` | Ninguno nuevo: `faction.join` no es hostil y no lo frena la protección propia ni la ajena. |

## 6. Eventos y contratos de red

**Cliente → servidor.** Un comando nuevo y estricto (R-VER-3), `faction.join { faction }`, de la simulación: entra en
`CommandTypes` y en la clasificación de `actions.go` como **no hostil**; crea dos trabajos nuevos de la cola, `faction.join` (la orden) y `faction.joined` (su vencimiento), con la clave
del jugador, que entran en `simulation.JobNames`. El catálogo pasa de 29 a 30. El enum de la facción en Zod
(`FactionCode`, con los tres valores, para la vista) no abre `NEUTRAL` como valor del comando: el comando lleva su
propio enum de dos valores.

**Servidor → cliente.** Ningún tipo nuevo. `CityView` gana `faction` (un `FactionCode`) y `factionJoin` (`{ faction,
atMs }` o `null`); llegan por la vista y los deltas de ciudad que ya existen, con su e2e. `TerritoryView` gana
`ownerFaction` (el `FactionCode` del dueño, o `null` si no tiene un jugador por dueño), para que el cliente diga el bando
del dueño y si la tierra es afín; un territorio se vuelve a difundir con `territory.update` cuando su dueño entra en
un bando.

**Eventos de dominio** en `world_events`: `FactionJoinOrdered` (al dar la orden) y `FactionJoined` (al vencer), sin
`counterpart_player_id` porque no implican a otro jugador. No van al registro ni a los avisos (Q9): son decisiones
propias, como `EraAdvanceOrdered`. Los tratados que la entrada rompe producen el `TreatyBreakAnnounced` de siempre,
con su `counterpart_player_id` y su `atMs` ([ADR-028](../decisions/ADR-028-event-log-from-world-events.md)), y por
tanto llegan al registro del otro firmante y a su aviso en vivo si ya lo tiene hoy.

**Métricas.** `eo_faction_joins_total{faction}` (series a cero para `ORDER` y `CHAOS`), a
[../operations/monitoring.md](../operations/monitoring.md) §2.

**Configuración.** `EO_FACTION_JOIN_SECONDS` (900) y `EO_FACTION_AFFINE_DAMAGE_PERMILLE` (800).

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | La facción efectiva de cada jugador y su entrada pendiente (se cargan con el mundo y llegan con `IntroducePlayer`, como la civilización); la paz de la Orden y la afinidad, que se derivan en cada consulta |
| Write-through | La orden de entrada y su vencimiento, cada uno en su trabajo de la cola de persistencia; el vencimiento escribe la facción, borra la entrada y anuncia la ruptura de los tratados incompatibles en una sola transacción |
| Dirty-flag | Nada |
| Reconstruible / no persistido | Qué territorio es afín a quién, y quién está en paz por su bando |

**Por qué hace falta un ADR ([ADR-039](../decisions/ADR-039-faction-peace-and-affinity.md)).** Tres decisiones estructurales, más la transacción única del vencimiento (ADR-039 §4):

1. **La facción es una segunda fuente de paz.** `AtPeace` deja de ser «hay un tratado» y pasa a ser «hay un tratado o
   los dos son de la Orden». Alternativa descartada: crear un pacto implícito por cada pareja de jugadores de `ORDER`,
   que crece con el cuadrado de los jugadores, se rompería con la semántica de preaviso de los tratados y llenaría la
   diplomacia de filas que nadie firmó.
2. **La afinidad se calcula en el golpe, no se guarda.** Depende del control del territorio y de la facción del
   dueño, que ya están en la RAM; guardarla obligaría a recalcularla en cada conquista y en cada entrada en un bando.
   Alternativa descartada: un bono de velocidad en tierra afín, que obligaría a que la polilínea tuviera duraciones
   distintas por tile y cambiaría el modelo de movimiento ([movement.md](movement.md)).
3. **Unirse es unilateral y por eso lleva preaviso.** Una entrada inmediata en `ORDER` daría paz en el acto frente a
   un atacante de `ORDER` sin que él aceptara nada, lo que ningún tratado permite. El preaviso tiene la misma forma
   que el de ruptura de [diplomacy.md](diplomacy.md) y sobrevive a un reinicio igual.

**Migración `000019`.** `players` gana `faction_join_id integer NULL REFERENCES factions (id)` y
`faction_join_at timestamptz NULL`, con el `CHECK` `players_faction_join_pair` (las dos son nulas o ninguna lo es), y
el trigger `players_faction_transition`, que sólo admite cambiar `faction_id` desde `NEUTRAL` a `ORDER` o `CHAOS` y
una entrada pendiente sobre un `NEUTRAL` hacia un bando ([INV-PERSIST-005](../invariants/persistence.md#inv-persist-005)).
No crea tablas: el `TRUNCATE` de `testenv_integration_test.go` no cambia. La siguiente migración libre pasará a ser
`000020`.

## 8. Invariantes

**Cita** [INV-PLAYER-002](../invariants/player.md#inv-player-002) (lo precisa: la facción la elige el jugador en
partida, una vez), [INV-DIPLO-002](../invariants/diplomacy.md#inv-diplo-002) (la ruptura automática no crea un segundo
tratado), [INV-COMBAT-002](../invariants/combat.md#inv-combat-002) (los demás motivos que impiden un golpe siguen
igual), [INV-MOVE-009](../invariants/movement.md#inv-move-009) (el bono en enteros),
[INV-SEC-001](../invariants/security.md#inv-sec-001) y [INV-SEC-004](../invariants/security.md#inv-sec-004) (la
facción la cambia el jugador de la sesión y la decide el servidor), [INV-SEC-007](../invariants/security.md#inv-sec-007)
(un `requestId` repetido no crea otra entrada) e [INV-PERSIST-005](../invariants/persistence.md#inv-persist-005) (la
migración `000019`).

**Asigna** tres invariantes nuevos:

- [INV-PLAYER-013](../invariants/player.md#inv-player-013): la facción de un jugador sólo cambia de `NEUTRAL` a `ORDER` o a `CHAOS`, y como mucho una vez (el
  trigger de `000019` y un test que intenta las demás transiciones).
- [INV-COMBAT-006](../invariants/combat.md#inv-combat-006): ningún golpe, robo, derribo ni avance de estandarte se aplica entre dos jugadores que son de `ORDER` en
  ese tick.
- [INV-DIPLO-003](../invariants/diplomacy.md#inv-diplo-003): nunca pasa a `ACTIVE` un `NON_AGGRESSION` ni una `ALLIANCE` entre un jugador de `ORDER` y uno de
  `CHAOS`, y todo el que esté vigente entre ellos está en preaviso.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | La validación de `faction.join` (cada fila de §5); `AtPeace` con tratado, con la Orden, con la Orden pendiente (no cuenta) y con el Caos; la afinidad de un territorio para cada combinación de bando del dueño y de la unidad, propio, ajeno y libre; el daño reducido con el redondeo y el mínimo de 1; la tabla de §4.1 entera, celda a celda |
| simulation | El preaviso que vence en la fase 5 y su tick exacto; dos de `ORDER` que se enfrentaban, saqueaban, derribaban o plantaban estandarte y paran en el tick en que el segundo entra; la alianza `ORDER`–`NEUTRAL` que pasa a preaviso cuando el `NEUTRAL` entra en `CHAOS`; un «golden» en Go con los ticks exactos de un combate en tierra afín y fuera de ella; el catálogo de comandos (30) y de trabajos |
| integration | La migración: el `CHECK` de la entrada pendiente y el trigger que rechaza `ORDER`→`CHAOS`, `CHAOS`→`NEUTRAL` y `ORDER`→`NEUTRAL`; el vencimiento que escribe la facción, borra la entrada y anuncia la ruptura en una transacción; un fallo inyectado que no deja nada a medias |
| recovery | Una entrada pendiente sobrevive a un reinicio y vence en su instante; una que venció con el servidor parado se aplica en el primer tick |
| contract / e2e | `faction.join` contra el JSON Schema (y que `NEUTRAL` no pasa); `CityView` con `faction` y `factionJoin`; de punta a punta por WebSocket: la orden, la cuenta atrás en la vista ajena, el vencimiento y un `unit.attack` entre dos de `ORDER` rechazado con `INVALID_TARGET` |
| cliente | El bando en el HUD y en el panel ajeno; «Elegir bando» con su confirmación; la cuenta atrás; los botones que la barra no ofrece; el texto del territorio afín; `scripts/smoke.mjs --faccion` con **cuentas de humo propias** (nunca `smoke_runner` ni `smoke_victim`, cuyas pasadas de saqueo, asedio y diplomacia necesitan que sigan `NEUTRAL`): una cuenta entra en `ORDER` y otra en `CHAOS` con `EO_FACTION_JOIN_SECONDS` corto, y una propuesta de pacto entre ellas se rechaza; en una pasada siguiente, con las cuentas ya en su bando, sólo comprueba el rechazo |

## 10. Decisiones de producto (2026-11-15)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de producto,
no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Cuándo se elige el bando? | **En partida, desde `NEUTRAL`.** Todos nacen `NEUTRAL` y eligen cuando conocen a sus vecinos: un bando sin saber quién está en cada uno es una apuesta a ciegas, al revés que la civilización, que dice cómo juegas tú. Sirve igual a las cuentas que ya existen (todas `NEUTRAL`) y no toca el registro ni el almacén de identidad (sin migración de identidad 4). Alternativa: **en el registro, como la civilización**, con un formulario más y una migración del almacén de identidad, y las cuentas viejas eligiendo igualmente en partida. |
| Q2 | ¿Se puede cambiar de bando? | **No: `NEUTRAL` es la casilla de salida y `ORDER` o `CHAOS` son para siempre**, y la entrada no se retira durante el preaviso. Si se pudiera cambiar, entrar en la Orden cuando eres débil y pasarte al Caos cuando eres fuerte vaciaría de sentido las dos cosas. Alternativas: **cambiar con un plazo de espera** (p. ej. una vez cada 7 días, con ruptura de los tratados incompatibles), que deja abierta la traición como juego político; **volver a `NEUTRAL`**, que convierte el bando en un interruptor. |
| Q3 | ¿Cuenta en el acto o con preaviso? | **Con 15 minutos de preaviso** (`EO_FACTION_JOIN_SECONDS` = 900), los mismos que la ruptura de un tratado. Unirse es unilateral: sin preaviso, el que va a perder un combate contra uno de la Orden entra en la Orden y lo para en seco. Durante el preaviso todos ven la entrada pendiente (Q8). |
| Q4 | ¿Qué prohíbe el bando? | **Sólo la paz de la Orden: dos de `ORDER` no se agreden.** El Caos y los neutrales no tienen restricción de bando. La Orden es un bloque que se protege a sí mismo y no puede saquear a los suyos; el Caos puede atacar a cualquiera, también a otro del Caos. Alternativas: **la Orden sólo agrede al Caos**, como las armadas de los MMO clásicos, que deja a la Orden sin poder responder a un neutral que la saquea más que con la respuesta automática; **paz también dentro del Caos**, que hace del Caos un espejo de la Orden sin identidad propia; **reputación**, agredir a los tuyos te vuelve «criminal», que es un sistema entero aparte. |
| Q5 | ¿Pueden la Orden y el Caos firmar algo? | **Comercio sí; pacto y alianza no.** Y al entrar en un bando, los pactos y alianzas con el contrario empiezan su preaviso de ruptura solos: la ventana de «Elegir bando» los enumera antes de confirmar. Prohibir el comercio entre bandos dejaría a muchos sin los bienes que pide Hierro. Alternativas: **no dejar entrar con tratados incompatibles** (hay que romperlos antes a mano), que no resuelve que el otro firmante entre en el bando contrario a la vez; **sin restricción de tratados**, que reduce el bando a un adorno. |
| Q6 | ¿Qué da la tierra afín? | **Menos daño recibido: un 20 % menos** (`EO_FACTION_AFFINE_DAMAGE_PERMILLE` = 800) a toda unidad golpeada en tierra afín, también aldeanos y caravanas; no a las casas. Defender es más fácil que invadir, y el mapa político se nota donde se lucha. Con el redondeo, un guerrero aguanta 10 golpes en vez de 8. Alternativas: **más daño infligido** (premia atacar desde tierra amiga, no defenderla); **recolección más rápida** (económico, no cambia dónde conviene pelear); **más velocidad** (descartado: cambiaría el modelo de movimiento, ADR-039). Es un número de partida, sin datos. |
| Q7 | ¿Qué es tierra afín para un neutral? | **Sólo la suya** (su capital y lo que conquistó). Así `NEUTRAL` no queda por debajo de los demás: defiende lo suyo igual que cualquiera, y lo que gana entrando en un bando es la tierra de todo el bloque. Alternativa: **ninguna**, que hace del neutral una casilla que todos querrán abandonar y castiga al que aún no ha decidido. |
| Q8 | ¿Es público el bando? ¿Y la entrada pendiente? | **Los dos, en la vista de la ciudad.** Las reglas de agresión tienen que ser predecibles: si no sabes de qué bando es el otro, no sabes si puedes atacarle ni firmar con él. La entrada pendiente se ve para que nadie se encuentre con una paz o una ruptura por sorpresa, igual que el preaviso de ruptura lo ven los dos firmantes. |
| Q9 | ¿Se avisa cuando alguien entra en un bando? | **No, salvo a quien pierde un tratado.** Entrar en un bando es una decisión propia (como avanzar de era) y no entra en el registro de nadie; la ve quien mira su ciudad. El firmante de un pacto o alianza que se rompe por ello sí lo recibe, con el `TreatyBreakAnnounced` de siempre en su registro. Alternativa: **avisar a todos los que tienen esa ciudad a la vista**, que llena los avisos de política ajena. |
| Q10 | ¿Entran objetivos globales del bando (puntuación, territorios del bando, recompensas)? | **No en este slice.** El roadmap habla de «objetivos globales», pero sin jugadores no hay con qué equilibrarlos, y el bando ya cambia el juego con las reglas de agresión y la tierra afín. Queda para un slice posterior, cuando haya datos de cuántos jugadores elige cada bando. |

## 11. Documentos relacionados

- [player.md](player.md) (la facción como atributo, los dos ejes) y [civilizations.md](civilizations.md) (la otra
  elección, los enteros por mil).
- [diplomacy.md](diplomacy.md) y [trade.md](trade.md) (qué se puede firmar y el preaviso de ruptura).
- [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md),
  [combat-siege.md](combat-siege.md), [territory-conquest.md](territory-conquest.md) y [caravans.md](caravans.md)
  (las acciones hostiles que la paz de la Orden prohíbe y los golpes que la tierra afín reduce).
- [../product/vision.md](../product/vision.md) §5 y [../roadmap/roadmap.md](../roadmap/roadmap.md) (la fase
  Factions) y [../roadmap/backlog.md](../roadmap/backlog.md) (`Q-03`).
