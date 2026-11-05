# Conquista de territorio: ganar y perder territorios con guerreros

Propuesta para que el territorio deje de ser estático: un guerrero planta el estandarte de su dueño en un territorio,
aguanta unos minutos y, si nadie lo impide, el territorio cambia de manos y da población a quien lo gana.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-TERRITORY-CONQUEST` |
| Estado | Implemented (2026-11-05). Propuesta en `Draft` y aceptada el 2026-11-04, con la recomendación de cada pregunta de §10; en `main` desde la fusión `a2c3378`, con la CI en verde. |
| Milestone | E12 Conquista (duodécimo slice después de M7) |
| Canon | §4 y §10 (territorios, población), §11 (tratados), §12 (persistencia), §13 (protocolo), §16 (errores) |
| Depende de | [territory.md](territory.md), [city.md](city.md), [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-siege.md](combat-siege.md), [diplomacy.md](diplomacy.md), [event-log.md](event-log.md), [live-notices.md](live-notices.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-016](../decisions/ADR-016-foreign-territory-actions.md) (clasificación de acciones; el territorio no restringe acciones), [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) (encargos en dos fases), [ADR-029](../decisions/ADR-029-population-over-limit-and-house-destruction.md) (población por encima del límite), [ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md) (el avance de era como encargo con su fila), [ADR-032](../decisions/ADR-032-trade-offers-and-two-key-jobs.md) (trabajos de la cola con dos claves), [ADR-033](../decisions/ADR-033-live-notices-ephemeral.md) (avisos en vivo), [ADR-034](../decisions/ADR-034-territory-claims.md) (la reclamación como encargo durable en dos fases y la captura como orden en dos fases que escribe el control y el límite de dos ciudades; migración `000014`). |
| Reemplaza a | En [territory.md](territory.md): «captura por combate, asedio o presencia militar» y el estado `contested` de §2 (fuera de MVP), y `RN-TERR-007` a `RN-TERR-009` en lo que dicen de que la fundación es el único productor y de que no existe la pérdida de control. Responde a la pregunta `Q-10` del [backlog](../roadmap/backlog.md) («¿cómo se reclama y cómo se pierde un territorio?») y a la «disputa de territorio» que la Q2 de [combat-siege.md](combat-siege.md) dejó para su propia spec. |
| Invariantes | Cita [INV-TERR-006](../invariants/territory.md#inv-terr-006) (lo precisa: `contested` deja de ser siempre `false`), [INV-TERR-009](../invariants/territory.md#inv-terr-009), [INV-ECON-004](../invariants/economy.md#inv-econ-004) (lo amplía: la fórmula del límite), [INV-CITY-006](../invariants/city.md#inv-city-006) y [INV-SEC-009](../invariants/security.md#inv-sec-009); asigna [INV-TERR-010](../invariants/territory.md#inv-terr-010) e [INV-TERR-011](../invariants/territory.md#inv-terr-011) (§8) |

## 1. Objetivo

Que el territorio sea un recurso disputado, el pilar 5 de [game-pillars.md](../product/game-pillars.md). Hoy el mundo
está dividido en 64 territorios de 64 × 64 tiles y un territorio sólo cambia de dueño cuando alguien funda su ciudad
en uno libre (`RN-TERR-007`); después no se gana ni se pierde nunca. En la base de desarrollo hay 6 territorios con
dueño y 58 libres, y 4 de las 10 ciudades están dentro de un territorio que posee otro jugador, porque fundar en
territorio ajeno no cambia el dueño (`RN-TERR-008`). Con guerreros, combate, saqueo y asedio en `main`, ya hay con
qué disputarlo. Y para que valga la pena disputarlo, poseerlo tiene que dar algo.

## 2. Scope

**Entra.** Un comando nuevo, `unit.claim`, con el que un guerrero reclama el territorio en el que está; la
reclamación como encargo con su plazo, visible para todos los del área (`contested` y su cuenta atrás); su
interrupción; el cambio de dueño al completarse; el bono de población por territorio conquistado; la defensa
automática de los guardias del dueño; los avisos en vivo y el registro de lo que pasa con un territorio; en el
cliente, el botón «Reclamar territorio», la cuenta atrás y el territorio en disputa en el mapa; el humo `--territorio`.

**No entra** (`Fuera de MVP` de este slice): conquistar o destruir ciudades (la capital es intocable, Q4); restringir
acciones por territorio, como recolectar o construir sólo en el propio ([ADR-016](../decisions/ADR-016-foreign-territory-actions.md)
sigue: todo es `LIBRE`); perder un territorio por abandono o por el paso del tiempo (Q10); liberar un territorio sin
reclamarlo; tope de territorios por jugador (Q9); reclamar con aldeanos; `control_points` como progreso (sigue en 0);
dueños `CLAN` o `FACTION`; los tipos de territorio y `resource_modifiers`; fronteras que no sean la rejilla.

## 3. Actores

Jugador con un guerrero (reclama), dueño del territorio (lo defiende, recibe los avisos), terceros con el territorio
en su área de interés (ven la disputa), Game Server (valida la orden, lleva el plazo y decide la captura,
[INV-SEC-001](../invariants/security.md#inv-sec-001)), tick loop (la fase 2 para la orden, la fase 5 para el plazo).

## 4. Modelo

### 4.1 Qué es una reclamación

Una reclamación es un encargo de un `WARRIOR`: se planta en el tile en que está y, mientras aguante, el territorio
de ese tile está **en disputa**. Al cumplirse el plazo (`EO_TERRITORY_CLAIM_FREE_SECONDS` = 300 s para un territorio
libre, `EO_TERRITORY_CLAIM_SECONDS` = 600 s para uno con dueño, Q3), el territorio pasa al dueño del guerrero. El
guerrero está en un estado nuevo, `CLAIMING`, que todos los que lo ven reciben: no se mueve, no recolecta y no
responde a ataques; sostiene el estandarte, y defenderlo es cosa de su escolta.

### 4.2 Qué da poseer un territorio

Cada territorio **conquistado** suma 5 al límite de población de la ciudad de su dueño (Q1). Conquistado es todo
territorio que el jugador posee y que no contiene el centro de su propia ciudad: la capital, el que ganó al fundar,
no cuenta (Q1). Así nadie gana nada por haber fundado antes, y nadie pierde nada por vivir en territorio ajeno: el
bono sólo se gana reclamando. Queda:

```
population_limit = population_cap(era) + 5 × casas COMPLETE + 5 × territorios conquistados
```

Perder un territorio resta sus 5 al instante. Si la ciudad queda por encima de su límite, conserva sus unidades y no
entrena hasta volver bajo él, como cuando le derriban una casa ([ADR-029](../decisions/ADR-029-population-over-limit-and-house-destruction.md)).
Hoy ningún jugador posee un territorio que no sea su capital: la fórmula nueva no cambia ningún límite al desplegarse.

### 4.3 Reglas

Dominio: `RN-CONQ`.

| ID | Regla |
|---|---|
| `RN-CONQ-001` | **La orden.** `unit.claim { unitId }` sobre un `WARRIOR` propio, vivo y en el mapa (ni guarnecido ni oculto). Sustituye cualquier otra tarea del guerrero (movimiento, recolección, ataque) y lo planta en su posición autoritativa de ese tick; reclama el territorio de ese tile. Sólo los guerreros reclaman (Q2). |
| `RN-CONQ-002` | **Qué se puede reclamar.** El territorio del tile existe, no es ya del jugador, no es la capital de su dueño —no contiene el centro de la ciudad de quien lo posee (Q4)— y no tiene otra reclamación activa (Q7). El tile no está en una Safe Zone ni en la zona urbana (3 × 3) de ninguna ciudad. Si falla algo, `INVALID_TARGET` con el motivo. |
| `RN-CONQ-003` | **Hostilidad** (Q5). Reclamar un territorio con dueño es una acción hostil contra él: la impiden un pacto o una alianza vigentes entre los dos (`INVALID_TARGET`, como el ataque, [combat-raid.md](combat-raid.md) Q5) y la protección offline de cualquiera de los dos (`CITY_PROTECTED`, `RN-COMBAT-018`). El acuerdo comercial no da paz ([trade.md](trade.md) §4.1) y no la impide. Reclamar un territorio libre no es hostil: no hay a quién proteger. |
| `RN-CONQ-004` | **Mientras dura.** El territorio está en disputa: `contested = true` y su reclamación viaja en `TerritoryView` a todas las sesiones con el territorio en su área (§6). Hay como mucho una reclamación activa por territorio ([INV-TERR-010](../invariants/territory.md#inv-terr-010)); un jugador puede tener varias en territorios distintos (Q9). |
| `RN-CONQ-005` | **Qué la rompe** (Q6). En cada tick, en la fase 5, se vuelve a comprobar el mismo predicado que al ordenar, como un enfrentamiento ([INV-COMBAT-002](../invariants/combat.md#inv-combat-002)). Termina sin captura si el guerrero muere, recibe otra orden de su dueño, o la reclamación queda bloqueada: los dos firman la paz, o uno de los dos pasa a `PROTECTED`; en un territorio libre, quien reclama, porque su guerrero sería invulnerable ([ADR-034](../decisions/ADR-034-territory-claims.md) §7). Que le peguen no la rompe: hay que matarlo. El guerrero vuelve a `IDLE` donde está. |
| `RN-CONQ-006` | **Defensa automática** (Q11). Al empezar una reclamación sobre un territorio con dueño, los `WARRIOR` parados de ese dueño a ≤ `EO_COMBAT_GUARD_RADIUS_TILES` del reclamante responden como a un golpe sobre una unidad suya en ese tile (`RN-COMBAT-014` a `RN-COMBAT-017`: correa, vuelta al puesto, simetría de la protección). |
| `RN-CONQ-007` | **La captura.** Cumplido el plazo, el territorio pasa al dueño del guerrero (`owner_type = PLAYER`, `captured_at`), deja de estar en disputa y el guerrero vuelve a `IDLE`. Es una orden en dos fases: la RAM aplica el cambio cuando la base lo ha confirmado, unos milisegundos después ([ADR-034](../decisions/ADR-034-territory-claims.md) §3). La ciudad del nuevo dueño suma 5 a su límite y la del anterior, si lo había, resta 5 (§4.2). Viajan `territory.update` al área del territorio y `city.update { populationLimit }` al chunk de cada ciudad. |
| `RN-CONQ-008` | **La capital es intocable** (Q4). El territorio que contiene el centro de la ciudad de su dueño nunca cambia de manos ([INV-TERR-011](../invariants/territory.md#inv-terr-011)). Como en el MVP las ciudades no se destruyen ni se mueven, lo es para siempre. |
| `RN-CONQ-009` | **Sin abandono** (Q10). Un territorio sólo cambia de manos por una reclamación completada o, como hasta ahora, por la fundación sobre uno libre. No decae, no caduca y no se pierde por no conectarse: estando `PROTECTED`, nadie puede reclamarle nada. |
| `RN-CONQ-010` | **Avisos y registro** (Q8). Una reclamación sobre un territorio con dueño se lo cuenta al dueño al empezar (`TERRITORY_CLAIMED`) y, si se completa, al dueño (`TERRITORY_LOST`) y al conquistador (`TERRITORY_TAKEN`): en vivo, con `event_log.notice`, y en el registro. Reclamar un territorio libre no avisa ni entra en el registro: no hay otro jugador, y es lo que el propio jugador ordenó ([event-log.md](event-log.md) §4.1). |

### 4.4 Estados

| Origen | Destino | Disparador | Fase |
|---|---|---|---|
| sin reclamación | reclamación activa (`contested`) | `unit.claim` válido (`RN-CONQ-001` a `003`) | 2 |
| reclamación activa | sin reclamación, mismo dueño | Muerte, otra orden o bloqueo del guerrero (`RN-CONQ-005`) | 5 |
| reclamación activa | capturando | Plazo cumplido (`RN-CONQ-007`): la transacción en vuelo | 5 |
| capturando | sin reclamación, dueño nuevo | La base confirma la captura | buzón |
| capturando | sin reclamación, mismo dueño | La base la rechaza: una fundación simultánea le dio dueño | buzón |

## 5. Errores

Sólo códigos que ya existen en el catálogo (canon §16).

| Código | Cuándo |
|---|---|
| `UNIT_NOT_FOUND`, `UNIT_NOT_OWNED`, `UNIT_DEAD`, `UNIT_GARRISONED` | Los de cualquier orden a una unidad. |
| `UNIT_NOT_MOVABLE` | La unidad no es un guerrero: el mismo código y mensaje que `unit.attack` con un aldeano. |
| `INVALID_TARGET` | El territorio ya es del jugador; es la capital de su dueño; ya tiene una reclamación activa; el tile está en una Safe Zone o en una zona urbana; hay paz entre los dos; el tile no pertenece a ningún territorio. El mensaje dice cuál. |
| `CITY_PROTECTED` | El dueño del territorio, o quien reclama, está protegido. |
| `INVALID_MESSAGE` | Un `unitId` que no es un entero positivo, o campos de más: lo rechaza el esquema estricto. |

## 6. Eventos y contratos de red

Cliente → servidor, comando nuevo (aditivo en v1), que se clasifica como hostil ([ADR-016](../decisions/ADR-016-foreign-territory-actions.md)
§2.3, [INV-CITY-006](../invariants/city.md#inv-city-006)): la protección se comprueba cuando el territorio tiene
dueño. El catálogo cliente → servidor pasa de 22 a 23 tipos.

```ts
// unit.claim
payload: z.object({ unitId: EntityId }).strict()
```

Servidor → cliente, sin tipos nuevos; todo aditivo (`R-VER-2`):

- `TerritoryView` gana `claim`, opcional: `{ playerId, cityId: EntityId | null, cityName: string | null, startedAtMs,
  endsAtMs }` mientras haya una reclamación activa; `contested` es `true` con ella y `false` sin ella. Viaja en
  `territory.update` y en `world.snapshot.territories[]`.
- `UnitStatus` gana `CLAIMING`, en `entity.update` y en el snapshot.
- `EventLogKind` y el `kind` de `event_log.notice` ganan `TERRITORY_CLAIMED`, `TERRITORY_LOST` y `TERRITORY_TAKEN`;
  la entrada y el aviso ganan `territoryId` y `territoryName`, opcionales, para nombrarlo («Villa Norte reclama tu
  territorio Llanuras del Norte»).

Eventos de dominio en `world_events`, con `counterpart_player_id` y `atMs` ([ADR-028](../decisions/ADR-028-event-log-from-world-events.md)):
`TerritoryClaimStarted` (`player_id` el dueño, `counterpart_player_id` quien reclama; sólo con dueño) y
`TerritoryControlChanged`, que ya existe desde M6 y gana en la captura `counterpart_player_id` (el anterior dueño en
`player_id`, el nuevo en `counterpart_player_id`, como la víctima y el autor de un saqueo). Los dos entran en
`eventlog.EventTypes` y en `Classify`, para el dueño y para el conquistador (`RN-LOG-001`).

Cliente: con un guerrero seleccionado, el HUD dice en qué territorio está y de quién es, y ofrece «Reclamar
territorio», deshabilitado con su motivo cuando no se puede (§5), como «Casa» y «Aldeano». Un territorio en disputa
se dibuja distinto en el overlay y muestra su cuenta atrás; el del jugador, con quién lo reclama.

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | La reclamación activa y su plazo, como el avance de era; el dueño de cada territorio ya lo es |
| Write-through | La reclamación al empezar y al terminar; la captura, con `territory_control`, el límite de las dos ciudades y sus eventos, en una transacción |
| Dirty-flag | Nada nuevo |
| Reconstruible / no persistido | El índice tile → territorio y la huella de chunks, como hasta ahora |

**Por qué hace falta un ADR ([ADR-034](../decisions/ADR-034-territory-claims.md)).** Hasta hoy el control de un territorio sólo lo escribía el alta del jugador,
en la goroutine HTTP y en la misma transacción que la ciudad (`RN-TERR-007`, territory.md §10). Ahora lo escribe la
simulación, y la captura toca a la vez `territory_control`, el `population_limit` de **dos** ciudades y dos eventos:
las dos ciudades tienen que quedar en el orden de la cola de cada una, el mismo problema que el trato de E10. La
propuesta: la reclamación es un **encargo durable en dos fases** con su fila (`territory_claims`, migración `000014`),
como el avance de era ([ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md)), que se recarga al arrancar; y
la captura es una orden en dos fases con las dos ciudades como claves ([ADR-032](../decisions/ADR-032-trade-offers-and-two-key-jobs.md)):
la RAM cambia el dueño después del `COMMIT`, como pide el canon para el dueño. Lo que rompe una reclamación, en
cambio, se decide en la RAM y se escribe después, como una muerte. El ADR deja escritas, entre otras, estas
alternativas descartadas:

1. **Presencia militar automática** (`control_points` que suben por tick con los guerreros de un jugador en el
   territorio): es lo que preveía la columna, pero captura sin que nadie lo ordene, se lee mal («¿por qué ha cambiado
   de color?») y obliga a contar guerreros por territorio en cada tick.
2. **Un edificio estandarte** que construye un aldeano y se derriba con el asedio: más persistente y más caro, pero
   abre construir fuera de la zona urbana, que hoy no existe, y otro tipo de edificio con su vida.
3. **La reclamación sólo en la RAM**: sin migración, pero un reinicio la perdería a medio plazo sin dejar rastro de
   cómo terminó, y el registro tendría un `TerritoryClaimStarted` sin final.

**Migración `000014`:** `territory_claims` y su índice parcial de una activa por territorio. `units.status` no cambia:
como `ATTACKING`, `CLAIMING` no se escribe en la fila de la unidad (el volcado lo escribe como `IDLE`); lo que dura es
la fila de la reclamación, y al arrancar devuelve al guerrero a `CLAIMING`.

## 8. Invariantes

**Cita** [INV-TERR-009](../invariants/territory.md#inv-terr-009) (ningún cambio de control sin `territory.update`),
[INV-CITY-006](../invariants/city.md#inv-city-006) (la acción hostil se frena con la protección: `unit.claim` gana su
test de rechazo) e [INV-SEC-009](../invariants/security.md#inv-sec-009) (los nuevos tipos del registro y de los
avisos sólo llegan a las dos partes).

**Precisa** [INV-TERR-006](../invariants/territory.md#inv-terr-006): `control_points` sigue en 0, pero `contested`
deja de ser siempre `false`; pasa a ser `true` exactamente mientras el territorio tiene una reclamación activa. Y
**amplía** [INV-ECON-004](../invariants/economy.md#inv-econ-004) con el término de los territorios conquistados de §4.2,
que la validación de carga recuenta al arrancar.

**Asigna** dos invariantes nuevos de la familia `INV-TERR`, registrados en [../invariants/territory.md](../invariants/territory.md):

- [INV-TERR-010](../invariants/territory.md#inv-terr-010) — un territorio tiene como mucho una reclamación activa
  (índice parcial único en `territory_claims`).
- [INV-TERR-011](../invariants/territory.md#inv-terr-011) — el territorio que contiene el centro de la ciudad de su
  dueño no cambia de dueño.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | El predicado de la reclamación con cada motivo de §5 (capital, propio, en disputa, Safe Zone, zona urbana, paz, protección de cada lado; libre sin protección); la fórmula del límite con territorios conquistados y la capital que no cuenta; la clasificación de los tres eventos para cada lado |
| simulation | Golden «conquista»: un guerrero reclama un territorio libre y, a los 300 s exactos, es de su dueño, con su `territory.update` y el bono; uno con dueño a los 600 s, con −5 y +5 en las dos ciudades y la ciudad perdedora por encima de su límite; la reclamación se rompe al matar al guerrero, al darle otra orden, al firmar un pacto y al quedar protegido el dueño; los guardias del dueño responden al empezar; una segunda reclamación sobre el mismo territorio se rechaza; la capital no se reclama |
| integration | La captura escribe en una transacción el control, los dos límites y los dos eventos; el índice parcial impide dos reclamaciones activas; la carga recuenta el límite con los territorios |
| recovery | Una reclamación activa sobrevive a un reinicio con su plazo; una que venció durante la caída se completa al arrancar |
| contract / e2e | `unit.claim`, `claim` en `TerritoryView`, `CLAIMING` y los tres tipos nuevos contra el JSON Schema; un dueño con la vista en su ciudad recibe `TERRITORY_CLAIMED` y, al completarse, `TERRITORY_LOST` |
| cliente | El botón con su motivo deshabilitado, la cuenta atrás, el territorio en disputa en el overlay, los textos del registro y de los avisos; `scripts/smoke.mjs --territorio` |

## 10. Decisiones de producto (2026-11-04)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de producto,
no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Qué da poseer un territorio? | **+5 de límite de población por territorio conquistado, sin contar la capital** (§4.2): es la misma moneda que una casa, se gana y se pierde en el acto, y [ADR-029](../decisions/ADR-029-population-over-limit-and-house-destruction.md) ya resuelve qué pasa al perderla. Alternativas: **recolectar sólo en territorio propio o aliado** dejaría sin bosque a las 4 ciudades de 10 que viven en territorio ajeno; **más rendimiento al recolectar** en lo propio es más difícil de ver y de equilibrar; **un tributo** sobre lo que otros recogen allí exige repartir cada depósito entre dos almacenes; **nada todavía** (sólo el color del mapa) dejaría la conquista sin motivo. Que la capital no cuente evita premiar a quien fundó antes. |
| Q2 | ¿Cómo se reclama y quién puede? | **Una orden explícita a un guerrero**, `unit.claim`, que se planta donde está y aguanta. Los aldeanos no reclaman. Alternativas en §7: la presencia automática captura sin que nadie lo ordene; un edificio estandarte es más caro de construir y de implementar. |
| Q3 | ¿Cuánto dura? | **5 minutos uno libre y 10 uno con dueño**, configurables. Lo libre sólo exige paciencia; lo ajeno da tiempo a que el dueño reciba el aviso y mande a alguien, incluso desde la otra punta del mapa. |
| Q4 | ¿Se puede reclamar la capital (el territorio donde está la ciudad de su dueño)? | **No, nunca.** Perderla sería perder el suelo de casa, y que una ciudad caiga es otra spec (Q2 de [combat-siege.md](combat-siege.md)). Quien fundó dentro de un territorio ajeno no puede reclamarlo mientras la ciudad de su dueño esté allí. |
| Q5 | ¿Qué hacen los tratados y la protección? | **Reclamar uno con dueño es hostil**: un pacto o una alianza lo impiden, el acuerdo comercial no; la protección offline de cualquiera de los dos también. Reclamar uno libre no es hostil. Es la misma regla que atacar, saquear y derribar. |
| Q6 | ¿Qué rompe una reclamación? | **Matar al guerrero, que su dueño le dé otra orden, o que deje de poder reclamar** (paz firmada, uno de los dos protegido). Que le peguen no basta: hay que matarlo, y el guerrero no se defiende, porque sostiene el estandarte. |
| Q7 | ¿Puede haber dos reclamaciones sobre el mismo territorio? | **No: una a la vez.** La segunda se rechaza con `INVALID_TARGET`. Dos plazos en paralelo sobre el mismo suelo obligan a decidir quién gana en un empate y no añaden juego: quien quiera ese territorio tiene que matar al que reclama. |
| Q8 | ¿Se avisa y queda en el registro? | **Sí**: el dueño recibe un aviso en vivo al empezar la reclamación (es lo que le permite defenderse) y otro si lo pierde; el conquistador, uno al ganarlo; y los tres quedan en el registro. Es lo que [live-notices.md](live-notices.md) ya hace con lo que pasa fuera de la vista. |
| Q9 | ¿Hay tope de territorios por jugador? | **No en este slice.** Cada territorio hay que conquistarlo y después defenderlo; con pocos jugadores un tope no limita nada. Se mide con jugadores. |
| Q10 | ¿Se pierde un territorio por no conectarse o con el tiempo? | **No.** Sólo si alguien lo reclama, y estando `PROTECTED` nadie puede. La ausencia tiene consecuencias acotadas (pilar 3): se puede perder lo conquistado mientras se está `ONLINE` u `OFFLINE_PENDING`, nunca la capital. |
| Q11 | ¿Responden solos los guerreros del dueño? | **Sí, los que estén parados cerca del reclamante**, como si le hubieran pegado a una unidad suya (`RN-CONQ-006`). Es la defensa de E3 aplicada al suelo; lejos de sus guerreros, el dueño depende del aviso. |

## 11. Documentos relacionados

- [territory.md](territory.md) (la rejilla, el control y lo que esta spec reemplaza) y
  [../invariants/territory.md](../invariants/territory.md) (`INV-TERR`).
- [combat-raid.md](combat-raid.md) (la acción hostil, la protección), [combat-response.md](combat-response.md) (la
  respuesta automática), [combat-siege.md](combat-siege.md) (el límite de población que baja y su Q2).
- [city.md](city.md) (la zona urbana y el límite por era), [economy-wood.md](economy-wood.md) (`RN-ECON-009`, las
  casas en el límite).
- [event-log.md](event-log.md) y [live-notices.md](live-notices.md) (el registro y los avisos).
- [../decisions/ADR-016-foreign-territory-actions.md](../decisions/ADR-016-foreign-territory-actions.md) (la
  clasificación de acciones).
