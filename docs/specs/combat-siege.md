# Combate: asedio de las casas

El segundo ataque a una ciudad: un guerrero junto a la muralla enemiga derriba una casa golpe a golpe.
La ciudad pierde las plazas de población que daba la casa y, si se queda por encima de su límite, conserva sus
unidades pero no puede entrenar más. La muralla y el Centro Urbano no se tocan: la ciudad no cae.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-COMBAT-SIEGE` |
| Estado | Implemented (2026-11-04). Aceptada el 2026-11-04 con las preguntas de §10 decididas ese día con la recomendación de cada una; en `main` desde la fusión `3b1cd48`, con la CI en verde el mismo día. |
| Milestone | E8 Asedio (octavo slice después de M7) |
| Canon | §9 (protección), §10 (eras y límite de población), §12 (persistencia), §16 (errores), §21 (fuera de MVP: asedio) |
| Depende de | [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md), [economy-wood.md](economy-wood.md), [city.md](city.md), [presence.md](presence.md), [event-log.md](event-log.md) |
| Decisiones | [ADR-016](../decisions/ADR-016-foreign-territory-actions.md) (acción hostil), [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) (reserva de la plaza al ordenar), [ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md) (fase 4), [ADR-026](../decisions/ADR-026-pillage-two-phase-stock-debit.md) (golpes sobre una ciudad), [ADR-028](../decisions/ADR-028-event-log-from-world-events.md) (eventos con el otro jugador). [ADR-029](../decisions/ADR-029-population-over-limit-and-house-destruction.md) (la población por encima del límite y la escritura de la destrucción). |
| Reemplaza a | — |
| Invariantes | Reescribe [INV-CITY-002](../invariants/city.md#inv-city-002), retira la excepción de [INV-ECON-004](../invariants/economy.md#inv-econ-004) y amplía dos de combate (§8) |

## 1. Objetivo

Que la ciudad misma corra riesgo, no sólo lo que guarda. E2 pone en juego las unidades que salen y E5 el almacén;
las casas, que son la inversión de largo plazo de una ciudad, siguen siendo intocables. Con este slice un rival
puede quitarle a una ciudad capacidad de crecer, y la defensa con guerreros parados protege algo más que madera.
Es el primer paso de la fase «Sieges» de [roadmap.md](../roadmap/roadmap.md) §5, sin máquinas de asedio ni
captura.

## 2. Scope

**Entra.** La orden `unit.demolish` de un `WARRIOR` sobre una casa de una ciudad enemiga, completa o en obra; la
aproximación hasta un tile junto a la casa; golpes de asedio en la fase 4 contra la vida de la casa; la
destrucción, que baja `population_limit`; una ciudad por encima de su límite que conserva sus unidades y no
entrena (el cambio de [INV-CITY-002](../invariants/city.md#inv-city-002), ADR-029); la defensa con la regla de
E5; el aviso al dueño conectado; la entrada en el registro de E7; el clic sobre una casa ajena en el cliente.

**No entra** (`Fuera de MVP` de este slice): atacar la muralla o el Centro Urbano, que la ciudad caiga o cambie de
dueño, máquinas de asedio, reparar casas con aldeanos, otros edificios (sólo existe la casa), captura de
territorio (`contested` sigue sin productor), ocultar las casas ajenas (niebla de guerra), tocar el balance de la
respuesta de E3 y notificaciones fuera del juego.

## 3. Actores

Jugador atacante y atacado, cliente web, Game Server (único autoritativo: alcance, vida, destrucción y límite los
decide él, [INV-SEC-001](../invariants/security.md#inv-sec-001)), tick loop (fase 4), PostgreSQL.

## 4. Modelo

### 4.1 La orden y el alcance

`unit.demolish { unitId, buildingId }` sustituye cualquier orden en curso del guerrero, como `unit.attack` y
`unit.pillage`. Las casas ocupan los ocho tiles del 3 × 3 de la ciudad que no son su centro
([economy-wood.md](economy-wood.md) §4.3, `RN-ECON-007`), y todo ese 3 × 3 es muralla `BLOCKED`
([city.md](city.md), `RN-CITY-009`). Un guerrero está **en alcance** de una casa sobre cualquier tile transitable a
distancia de Chebyshev 1 de ella, su `AttackRange`; esos tiles están siempre en el anillo de la muralla (distancia 2
al centro): tres para una casa de lado y cinco para una de esquina. El guerrero pasa a `MOVING` hacia el más
cercano a él al que haya ruta (empate: menor `y`, después menor `x`, como en [combat-pillage.md](combat-pillage.md) §4.1) y, al llegar, a
`ATTACKING`.

### 4.2 Valores de partida

Parámetros de catálogo y de configuración, como los de [combat-pillage.md](combat-pillage.md) §4.2:

| Parámetro | Valor propuesto | Hoy |
|---|---|---|
| Vida de una casa (`MaxHP` del catálogo de edificios) | 150 | — (las casas no tienen vida) |
| Daño y ritmo del golpe de asedio | 8 cada 1 500 ms, los del `WARRIOR` | — |
| Olvido del daño (`EO_SIEGE_DAMAGE_RESET_SECONDS`, nombre provisional) | 300 s sin golpes | — |

Con estos números un guerrero solo derriba una casa en 19 golpes (≈ 27 s pegado a la muralla) y cinco en 4 (≈ 5
s). A la víctima cada casa le costó 30 de madera y 15 s; perder las ocho le quita 40 plazas, pero nunca baja del
`population_cap` de su era (20 en `STONE_AGE`), que no depende de ningún edificio.

### 4.3 Reglas

Dominio: `RN-COMBAT`, a continuación de las de [combat-pillage.md](combat-pillage.md).

| ID | Regla |
|---|---|
| `RN-COMBAT-029` | Sólo derriba un `WARRIOR` propio, vivo y no guarnecido. El objetivo es una casa, completa o en obra, de una ciudad **de otro jugador**. La muralla y el Centro Urbano no son objetivos (Q2). |
| `RN-COMBAT-030` | `unit.demolish` es **hostil** (ADR-016) y pasa por el mismo predicado que `unit.pillage` (`RN-COMBAT-020`, [INV-COMBAT-002](../invariants/combat.md#inv-combat-002)): ciudad `PROTECTED`, `CITY_PROTECTED`; tratado `NON_AGGRESSION` o `ALLIANCE`, o guerrero sobre una Safe Zone, `INVALID_TARGET`. `OFFLINE_PENDING` no protege (Q6). |
| `RN-COMBAT-031` | **El golpe de asedio** vence en la fase 4 como uno de E2 (`now >= next_hit_at_ms`, guerrero en alcance) y resta su daño a la vida de la casa. Se calcula con los demás golpes (`RN-COMBAT-007`) y se aplica después de los golpes a unidades y de los de saqueo, en orden de `id` del guerrero, cada uno sobre la vida que dejó el anterior. Sin azar. |
| `RN-COMBAT-032` | **La vida de la casa** empieza en su `MaxHP` (Q3). El daño vive en la RAM: una casa que pasa `EO_SIEGE_DAMAGE_RESET_SECONDS` sin recibir golpes vuelve a estar entera, y un reinicio también la deja entera (Q4). No hay reparación. |
| `RN-COMBAT-033` | **Destrucción.** El golpe que deja la vida en 0 destruye la casa: pasa a `DESTROYED`; si estaba completa, `population_limit` baja en su bono (5, `RN-ECON-009`); si estaba en obra, la madera de su encargo se pierde (Q8). El atacante no recibe nada (Q5). El tile vuelve a admitir una casa cuando la destrucción está confirmada (§7). |
| `RN-COMBAT-034` | **Por encima del límite.** Una ciudad con `population > population_limit` conserva sus unidades, y sus producciones en curso nacen al vencer porque su plaza ya estaba reservada (`RN-ECON-010`). Ningún encargo nuevo cabe mientras `population + coste > population_limit`: `POPULATION_LIMIT_REACHED`, como hoy (Q1, ADR-029). Vuelve a entrenar cuando mueren unidades o reconstruye casas. |
| `RN-COMBAT-035` | **Atacante presente.** Un golpe de asedio exige al dueño del guerrero presente, como un golpe de saqueo (`RN-COMBAT-024`) (Q6). |
| `RN-COMBAT-036` | El asedio termina, y el guerrero queda `IDLE` donde está, cuando la casa se destruye, el guerrero sale del alcance, su dueño se ausenta o pasa a `PROTECTED` (`RN-COMBAT-018`), la ciudad pasa a `PROTECTED`, nace un tratado entre ambos o el guerrero muere. No pasa solo a la casa siguiente (Q10). |
| `RN-COMBAT-037` | **Defensa.** Un golpe de asedio cuenta, para `RN-COMBAT-014`, como un golpe sobre una unidad del dueño situada en el centro de la ciudad, igual que un golpe de saqueo (`RN-COMBAT-027`): responden sus guerreros disponibles a ≤ `EO_COMBAT_GUARD_RADIUS_TILES` del centro (Q7). |
| `RN-COMBAT-038` | **Aviso.** Cada golpe de asedio apunta en la ciudad el instante del último golpe a sus casas; el cliente del dueño conectado muestra el aviso mientras sea reciente, como el de saqueo (Q9). |

## 5. Errores

Sólo códigos existentes del catálogo (canon §16).

| Código | Condición |
|---|---|
| `UNIT_NOT_FOUND` / `UNIT_NOT_OWNED` | El guerrero no existe o no es del jugador |
| `UNIT_NOT_MOVABLE` | El tipo no ataca (`VILLAGER`) |
| `UNIT_DEAD` / `UNIT_GARRISONED` | El guerrero está muerto o guarnecido |
| `INVALID_TARGET` | La casa no existe, ya está destruida o es propia; un tratado lo impide; el guerrero está sobre una Safe Zone |
| `CITY_PROTECTED` | La ciudad de la casa está `PROTECTED` |
| `PATH_NOT_FOUND`, `PATH_TOO_LONG` | Ningún tile junto a la casa es alcanzable, como en `unit.move` |
| `POPULATION_LIMIT_REACHED` | Sin cambio: `city.train` de una ciudad que, tras perder casas, no tiene plaza (`RN-COMBAT-034`) |

Una casa que no existe responde `INVALID_TARGET` y no un código propio: el catálogo no tiene uno de edificio, y
`CITY_NOT_FOUND` diría algo de la ciudad que la orden no nombra.

## 6. Eventos y contratos de red

Cliente → servidor: `unit.demolish { unitId, buildingId }`, comando nuevo y estricto, aditivo en v1 como
`unit.pillage`, y clasificado como hostil en `internal/game/simulation/actions.go`. Servidor → cliente:

- `unit.movement.*` para la aproximación y `entity.update { status: ATTACKING }` del guerrero, sin cambios.
- `building.update` gana el campo opcional `hp`, y `BuildingView` los opcionales `hp` y `maxHp` para el snapshot
  (`R-VER-2`): el cliente dibuja la vida de una casa dañada.
- `building.destroyed { id, cityId }`, mensaje nuevo (`R-VER-3`), al chunk de la casa. La destrucción no viaja como
  un estado más de `BuildingStatus`: la casa deja de existir para el cliente, como una unidad muerta sale con
  `entity.despawn`.
- `city.update { populationLimit }` de la víctima, sin cambios, y el campo opcional nuevo `besiegedAtMs`, que sólo
  recibe el dueño, como `pillagedAtMs` (Q9).

Con ellos, el protocolo v1 pasa a 18 mensajes cliente→servidor y 20 servidor→cliente.

Evento de dominio en `world_events`: `BuildingDestroyed` al destruirse, con el guerrero, la casa, su tile, si
estaba completa, `counterpart_player_id` (el atacante) y `atMs` y `attackerPlayerId` en el payload, como
`CityPillaged` ([ADR-028](../decisions/ADR-028-event-log-from-world-events.md)). Entra en `eventlog.EventTypes` y en
`Classify`, con dos tipos de entrada, como los saqueos: `HOUSE_LOST` para la víctima («Villa Norte derribó 2 de tus
casas») y `HOUSE_DESTROYED` para el autor («Tus guerreros derribaron 1 casa de Villa Norte»), agrupadas por otro
jugador. Los golpes que no destruyen no se escriben, como `UnitAttacked` no sale en el registro (SPEC-EVENT-LOG Q1).

**Precisión (2026-11-04).** La propuesta decía que `DESTROYED` no podía entrar en `BuildingStatus` porque un valor
nuevo de enum rompe v1 (`R-VER-4`); el proyecto ya había añadido valores así como aditivos (`ATTACKING`, `WARRIOR`,
`DEAD` en E2), así que el motivo real del mensaje propio es el de arriba. También decía que el payload llevaría
`unitType` y `byPlayerId`: lleva `attackerPlayerId`, la clave que ya usa `CityPillaged`, y el registro no necesita el
tipo de unidad del atacante. Y sólo nombraba la entrada de la víctima: la del autor la exige `RN-LOG-001`, que da
las muertes y los saqueos a los dos.

Cliente: con un guerrero seleccionado, un clic sobre una casa de una ciudad **ajena** es la orden de derribarla; un
clic sobre el resto de su 3 × 3 (el centro o un tile sin casa) sigue siendo saquear (Q10). La casa dañada lleva una
barra de vida y desaparece al destruirse. El HUD de población del atacado se pone en rojo cuando supera el límite
(«23 / 20»), y sus botones de entrenar dicen por qué no responden. El aviso dice «¡Están derribando tus casas!».

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | La vida de las casas dañadas y el instante de su último golpe, el asedio en curso (guerrero → casa, `next_hit_at_ms`) y `besiegedAtMs` |
| Write-through | La destrucción: la casa a `DESTROYED`, `population_limit` menos su bono, `cities.version + 1` y `BuildingDestroyed`, en una transacción con la clave de la ciudad víctima |
| Dirty-flag | Nada nuevo |
| Reconstruible / no persistido | El daño (un reinicio deja las casas enteras, `RN-COMBAT-032`), el asedio en curso y `ATTACKING`, como en E2 y E5, y `besiegedAtMs` |

**Por qué hace falta un ADR ([ADR-029](../decisions/ADR-029-population-over-limit-and-house-destruction.md)).** Dos
cosas cambian contratos que hoy sostienen invariantes:

1. **La población por encima del límite.** Hoy el `CHECK cities_population_within_limit` (`000001`) hace imposible
   que una fila tenga `population > population_limit`, y es la última barrera de la reserva de plaza al entrenar
   (`postgres/economy.go`, `TestEntrenarSinPlazaSeRechazaSinDebitar`). Una casa destruida baja el límite sin tocar
   la población, así que el `CHECK` tiene que irse. La propuesta es que la barrera cambie de forma y no desaparezca:
   un trigger rechaza toda escritura que **suba** `population` por encima de `population_limit`, y deja pasar las
   que bajan el límite. [INV-CITY-002](../invariants/city.md#inv-city-002) pasa de «la población nunca supera el
   límite» a «nada se crea por encima del límite». La validación de carga deja de subir el límite hasta el
   recuento, porque ese exceso ya es legal, y la excepción de [INV-ECON-004](../invariants/economy.md#inv-econ-004)
   desaparece. Su recuento sí puede subir la población por encima del límite sin crear nada, y es la única escritura
   que el trigger deja pasar (ADR-029 §3).
2. **Un escritor que baja el límite.** Hasta hoy `population_limit` sólo lo escriben el alta, la casa que se
   completa y la validación de carga. La destrucción lo baja en la RAM en el mismo tick y lo confirma por la cola
   con la clave de la ciudad, detrás de cualquier encargo de esa ciudad ya encolado. El bono se resta según el estado
   de la casa en la base, de modo que una compleción que aún no se había escrito no se resta dos veces. Si la
   transacción falla de forma definitiva, se reencola en el siguiente volcado, como una compleción descartada
   ([ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) §5): la casa no reaparece. Una caída
   antes del `COMMIT` pierde la destrucción, nunca la duplica. Mientras la destrucción no esté confirmada, el tile
   sigue ocupado para `city.build`, de modo que una casa nueva en el mismo sitio se escribe siempre después.

**Migración `000011`.** `DESTROYED` en `buildings_status_valid`; `buildings_unique_tile` pasa a ser un índice único
parcial sobre las casas no destruidas, para poder reconstruir en el mismo tile; el `CHECK` de población se cambia
por el trigger del punto 1. Las casas destruidas conservan su fila y no se cargan al arrancar, como las unidades
`DEAD` (backlog Q-05). `world_events.event_type` no tiene `CHECK`, así que `BuildingDestroyed` no necesita
migración. La siguiente libre pasará a ser `000012`.

## 8. Invariantes

**Reescribe** [INV-CITY-002](../invariants/city.md#inv-city-002) con ADR-029: `0 <= population` siempre, y ninguna
transacción que aumente `population` la deja por encima de `population_limit`. **Retira** la excepción de
[INV-ECON-004](../invariants/economy.md#inv-econ-004): el límite vuelve a ser siempre `population_cap + 5 × casas
COMPLETE`, que una casa `DESTROYED` no cuenta. **Amplía** [INV-COMBAT-002](../invariants/combat.md#inv-combat-002) a
los golpes de asedio e [INV-COMBAT-005](../invariants/combat.md#inv-combat-005) a «ningún golpe de saqueo o de asedio
cae con el dueño del atacante ausente». Cita [INV-COMBAT-003](../invariants/combat.md#inv-combat-003) (derribar no
crea madera: el atacante no recibe nada), [INV-CITY-017](../invariants/city.md#inv-city-017) (la población sigue
siendo unidades vivas más producciones en curso) e [INV-SAFE-006](../invariants/territory.md#inv-safe-006). No
propone invariantes nuevos.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | Validación de `unit.demolish` (cada fila de §5); alcance junto a una casa de lado y de esquina y elección del tile; vida, olvido del daño y destrucción; límite con casas `DESTROYED`; fin por cada causa de `RN-COMBAT-036` |
| simulation | Golden «asedio»: guerrero que cruza el mapa, 19 golpes en sus ticks exactos, casa destruida y límite que baja; dos atacantes sobre una casa (orden de `RN-COMBAT-031`); casa en obra destruida; ciudad por encima del límite que no entrena y cuya producción en curso nace; guardia que responde a un golpe de asedio |
| recovery | Reinicio con una casa dañada (vuelve entera); caída entre la destrucción en la RAM y su `COMMIT` (la casa sigue en pie, el límite no baja) |
| integration | Transacción de la destrucción con fallo inyectado (ni casa destruida sin evento ni límite rebajado sin casa); el trigger rechaza subir la población por encima del límite y deja bajar el límite; reconstruir en el tile de una casa destruida |
| contract / e2e | `unit.demolish`, `building.destroyed`, `hp`, `maxHp` y `besiegedAtMs` contra el JSON Schema; recorrido completo por WebSocket; `BuildingDestroyed` en el registro de la víctima |
| cliente | Clic sobre una casa ajena y sobre el resto del 3 × 3; barra de vida; HUD de población por encima del límite; `scripts/smoke.mjs` añade `--asedio` (la víctima construye una casa si no tiene; en una misma pasada va antes de `--diplomacia`, cuyo pacto lo impediría) |

## 10. Decisiones de producto (2026-11-04)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de
producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | Una casa destruida puede dejar la ciudad con más población que límite, que hoy prohíben [INV-CITY-002](../invariants/city.md#inv-city-002) y el `CHECK` de la base. ¿Qué se hace? | **Permitir el exceso y bloquear sólo los encargos nuevos** (ADR-029, §7): las unidades viven y la ciudad no entrena hasta volver bajo el límite. Las alternativas son peores. **Impedir la destrucción** si dejaría la ciudad por encima haría inmunes justo a las ciudades llenas: cuanto más grande, más segura. **Matar unidades** obliga a elegir víctimas arbitrarias (¿el aldeano que trabaja, el guerrero que defiende?) y castiga dos veces el mismo golpe. Es además lo que hace la referencia: perder casas frena, no mata. |
| Q2 | ¿Qué se puede derribar? | **Sólo casas**, completas o en obra. Ni la muralla ni el Centro Urbano: que la ciudad caiga o cambie de dueño es una disputa de territorio y merece su spec. Así el daño tiene un fondo: el `population_cap` de la era no depende de ningún edificio. |
| Q3 | ¿Cuánta vida tiene una casa? | **150** (§4.2): un guerrero solo necesita ≈ 27 s pegado a la muralla, expuesto a la defensa, y cinco ≈ 5 s. Derribar tiene que costar más que saquear (4 golpes), porque el daño a la víctima dura más. Es un parámetro de catálogo: se ajusta sin cambiar la spec. |
| Q4 | ¿El daño que no destruye se queda? | **No: se olvida** a los 5 minutos sin golpes, y también con un reinicio (`RN-COMBAT-032`). Guardarlo pediría una columna, su volcado y una tarea de reparación que no existe, y sin ella una casa a 1 de vida se quedaría así para siempre. Que se olvide obliga al atacante a terminar lo que empieza. |
| Q5 | ¿Gana algo el atacante al derribar? | **Nada material.** Devolverle madera la crearía de la nada ([INV-COMBAT-003](../invariants/combat.md#inv-combat-003)): la madera de la casa ya se gastó. La ganancia es estratégica: el rival pierde 30 de madera y 15 s por casa y deja de poder entrenar. Junto con el saqueo, golpea el almacén y la capacidad a la vez. |
| Q6 | ¿Quién está protegido y quién tiene que estar presente? | **Lo mismo que en el saqueo**: la ciudad `PROTECTED` y los tratados lo impiden, `OFFLINE_PENDING` no protege, y el dueño del atacante tiene que estar presente. Si no, bastaría dejar diez guerreros pegados a una casa con el ordenador apagado. |
| Q7 | ¿Defienden las casas los guerreros parados de la ciudad? | **Sí**, con la regla del saqueo (`RN-COMBAT-037`): un golpe de asedio cuenta como un golpe a una unidad en el centro de la ciudad. Sigue abierta la observación de balance de E3 (varios guerreros parados juntos son muy fuertes); aquí juega a favor del defensor, como en E5. |
| Q8 | ¿Qué pasa con lo que estaba en marcha? | **La casa en obra se puede derribar y su madera se pierde**: construir bajo asedio es un riesgo. **Lo que se estaba entrenando nace**, porque su plaza se reservó al ordenar (`RN-ECON-010`); quitarla sería matar una unidad antes de nacer. |
| Q9 | ¿Se avisa al atacado y queda en el registro? | **Sí a las dos.** El dueño conectado ve «¡Están derribando tus casas!» con el mismo mecanismo que el saqueo (`besiegedAtMs`), y el registro de E7 le dice quién derribó cuántas. Los golpes que no destruyen no van al registro. |
| Q10 | ¿Cómo se ordena desde el cliente? ¿Sigue solo con la casa siguiente? | **Clic sobre la casa ajena para derribarla**; el resto del 3 × 3 sigue siendo saquear. Hoy un clic en cualquier tile del 3 × 3, casas incluidas, es saquear: este cambio afecta a E5 en los tiles con casa. **No sigue solo**: cada casa es una orden. Una granja automática de casas sería el mismo problema que la vuelta automática del saqueo (Q6 de E5), y darle al defensor el tiempo entre una orden y la siguiente es justo. |

## 11. Documentos relacionados

- [combat-pillage.md](combat-pillage.md) (el otro ataque a una ciudad, su predicado y su defensa),
  [combat-raid.md](combat-raid.md) (fase 4, `ATTACKING`), [combat-response.md](combat-response.md) (respuesta y correa).
- [economy-wood.md](economy-wood.md) §4.3 (la casa, su sitio y su bono) y [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md)
  (la reserva de la plaza al ordenar).
- [city.md](city.md) (muralla 3 × 3, `RN-CITY-009`; límite por era), [presence.md](presence.md) (`OFFLINE_PENDING` y `PROTECTED`).
- [event-log.md](event-log.md) (cómo entra `BuildingDestroyed` en el registro).
- [../roadmap/roadmap.md](../roadmap/roadmap.md) §5, fase «Sieges», de la que este slice es el segundo paso tras el saqueo.
