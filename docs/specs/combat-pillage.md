# Combate: saqueo de la ciudad

El primer ataque a una ciudad: un guerrero junto a la muralla enemiga roba madera del almacén golpe a
golpe y tiene que llevársela a casa para que cuente. La ciudad no tiene vida ni cae: lo que se arriesga es el
almacén.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-COMBAT-PILLAGE` |
| Estado | Implemented (2026-11-03). Aceptada el 2026-11-03 con las preguntas de §10 decididas ese día con la recomendación de cada una; en `main` desde la fusión `82f3db8`, con la CI en verde el mismo día. |
| Milestone | E5 Saqueo (quinto slice después de M7) |
| Canon | §9 (protección), §12 (persistencia), §16 (errores), §21 (fuera de MVP: asedio) |
| Depende de | [combat-raid.md](combat-raid.md), [combat-response.md](combat-response.md), [economy-wood.md](economy-wood.md), [city.md](city.md), [presence.md](presence.md), [safe-zones.md](safe-zones.md), [garrison.md](garrison.md) |
| Decisiones | [ADR-016](../decisions/ADR-016-foreign-territory-actions.md) (acción hostil), [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) (deltas del stock), [ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md) (fase 4). [ADR-026](../decisions/ADR-026-pillage-two-phase-stock-debit.md) (el golpe de saqueo, un débito en dos fases). |
| Reemplaza a | — |
| Invariantes | Amplía dos existentes y asigna `INV-COMBAT-004` e `INV-COMBAT-005` ([../invariants/combat.md](../invariants/combat.md)) |

## 1. Objetivo

Que tener cosas cueste algo. Desde E2 el riesgo se queda en las unidades que salen de casa: la ciudad es
intocable y un almacén lleno no corre peligro. Con este slice otro jugador puede vaciar parte del almacén
mientras su dueño está fuera, y los guerreros de la ciudad tienen algo que defender. Es el riesgo de
[vision.md](../product/vision.md) aplicado a lo acumulado, no sólo a lo que viaja.

## 2. Scope

**Entra.** La orden `unit.pillage` de un `WARRIOR` sobre una ciudad enemiga; la aproximación hasta la muralla;
golpes de saqueo en la fase 4 que pasan madera del almacén de la víctima a la carga del guerrero; el suelo del
almacén; la respuesta de los guerreros de la ciudad (extensión de E3); el aviso al saqueado conectado; el
depósito en casa con la regla del botín de E2.

**No entra** (`Fuera de MVP` de este slice): vida de edificios, destruir casas o la muralla, que la ciudad caiga
o cambie de dueño, máquinas de asedio, captura de territorio (`contested` sigue sin productor), saqueo de otros
recursos (sólo existe `WOOD`), ocultar el almacén ajeno (ver Q9) y notificaciones fuera del juego.

## 3. Actores

Jugador saqueador y saqueado, cliente web, Game Server (único autoritativo: alcance, cantidades, suelo y carga
los decide él, [INV-SEC-001](../invariants/security.md#inv-sec-001)), tick loop (fase 4), PostgreSQL.

## 4. Modelo

### 4.1 La orden y el alcance

`unit.pillage { unitId, cityId }` sustituye cualquier orden en curso del guerrero, como `unit.attack`. La ciudad
no es una unidad: el objetivo es su **muralla**, el 3 × 3 de [city.md](city.md) (`RN-CITY-009`), que es `BLOCKED`
entera. Un guerrero está **en alcance** sobre cualquier tile transitable del anillo que rodea el 3 × 3 (distancia
de Chebyshev 2 al centro). El guerrero pasa a `MOVING` hacia el tile transitable del anillo más cercano a él (distancia de
Chebyshev; empate: menor `y`, después menor `x`) al que haya ruta y, al llegar, a `ATTACKING`, el mismo estado de
E2.

### 4.2 Valores de partida

Parámetros de catálogo y de configuración, como los de [combat-raid.md](combat-raid.md) §4.1 (Q2 y Q3):

| Parámetro | Valor propuesto | Hoy |
|---|---|---|
| Intervalo entre golpes de saqueo | 1 500 ms (el de ataque del `WARRIOR`) | — |
| Madera por golpe (`EO_PILLAGE_WOOD_PER_HIT`) | 5 | — |
| Carga máxima del `WARRIOR` (`LootCarry`) | 20, también para el botín de E2 | 10 |
| Suelo del almacén (`EO_PILLAGE_STOCK_FLOOR`) | 50, el coste de un aldeano | — |

Con estos números un guerrero llena su carga en 4 golpes (~6 s) y vuelve a casa con 20 de madera: el doble de un
viaje de recolección, a cambio de cruzar el mapa dos veces con un objetivo encima.

### 4.3 Reglas

Dominio: `RN-COMBAT`, a continuación de las de [combat-response.md](combat-response.md).

| ID | Regla |
|---|---|
| `RN-COMBAT-019` | Sólo saquea un `WARRIOR` propio, vivo y no guarnecido. El objetivo es una ciudad **de otro jugador**. |
| `RN-COMBAT-020` | `unit.pillage` es **hostil** (ADR-016) y pasa por el mismo predicado que `unit.attack` ([INV-COMBAT-002](../invariants/combat.md#inv-combat-002)): la ciudad `PROTECTED` responde `CITY_PROTECTED`; un tratado `NON_AGGRESSION` o `ALLIANCE`, o el guerrero sobre una Safe Zone, `INVALID_TARGET`. `OFFLINE_PENDING` no protege, como en `RN-COMBAT-003`. La muralla nunca está en una Safe Zone (`INV-SAFE-006`). |
| `RN-COMBAT-021` | **El golpe de saqueo** vence en la fase 4 como uno de E2 (`now >= next_hit_at_ms`, guerrero en alcance) y roba `min(madera por golpe, hueco de la carga, stock − suelo)`. Si eso es cero, no hay golpe y el saqueo termina. |
| `RN-COMBAT-022` | **Orden y determinismo.** Los golpes de saqueo se calculan con los demás golpes de la fase 4 (`RN-COMBAT-007`), pero se aplican después de los golpes a unidades y en orden de `id` del guerrero, cada uno contra el stock que dejó el anterior: varios saqueadores sobre una ciudad se reparten lo que queda por encima del suelo sin pasar de él. Sin azar. |
| `RN-COMBAT-023` | **El suelo.** Ningún saqueo deja el almacén de la víctima por debajo de `EO_PILLAGE_STOCK_FLOOR` (Q3). Los encargos de la víctima sí pueden bajar de él: el suelo frena al ladrón, no al dueño. |
| `RN-COMBAT-024` | **Saqueador presente.** Un golpe de saqueo exige al dueño del guerrero presente: ciudad `ONLINE` y sesión viva o dentro del margen de reconexión, como un viaje de recolección ([INV-ECON-006](../invariants/economy.md#inv-econ-006)) (Q4). Si se ausenta, el saqueo termina. |
| `RN-COMBAT-025` | El saqueo termina, y el guerrero queda `IDLE` donde está, cuando su carga se llena, el almacén llega al suelo, el guerrero sale del anillo, su dueño se ausenta o pasa a `PROTECTED` (`RN-COMBAT-018`), la ciudad pasa a `PROTECTED`, nace un tratado entre ambos o el guerrero muere. No vuelve solo a casa (Q6). |
| `RN-COMBAT-026` | **Depósito.** La madera saqueada es carga del guerrero y sigue `RN-COMBAT-010`: se deposita al llegar al tile de depósito de su ciudad (`RN-ECON-004`), y si muere pasa a quien lo mate. Matar a un saqueador cargado es la forma de recuperar lo robado. |
| `RN-COMBAT-027` | **Defensa.** Un golpe de saqueo cuenta, para `RN-COMBAT-014`, como un golpe sobre una unidad del saqueado situada en el centro de la ciudad: responden sus guerreros disponibles a ≤ `EO_COMBAT_GUARD_RADIUS_TILES` del centro, con la correa y la vuelta de E3 (Q5). Los guarnecidos no responden (`RN-COMBAT-012`). |
| `RN-COMBAT-028` | **Aviso.** Cada golpe de saqueo apunta en la ciudad el instante del último saqueo; el cliente del dueño conectado muestra el aviso mientras sea reciente (Q7). |

## 5. Errores

Sólo códigos existentes del catálogo (canon §16).

| Código | Condición |
|---|---|
| `UNIT_NOT_FOUND` / `UNIT_NOT_OWNED` | El guerrero no existe o no es del jugador |
| `UNIT_NOT_MOVABLE` | El tipo no ataca (`VILLAGER`) |
| `UNIT_DEAD` / `UNIT_GARRISONED` | El guerrero está muerto o guarnecido |
| `CITY_NOT_FOUND` | La ciudad no existe |
| `INVALID_TARGET` | Ciudad propia, tratado que lo impide o guerrero sobre una Safe Zone |
| `CITY_PROTECTED` | La ciudad objetivo está `PROTECTED` |
| `PATH_NOT_FOUND`, `PATH_TOO_LONG` | Ningún tile del anillo es alcanzable, como en `unit.move` |

Un almacén ya en el suelo **no** es un error: la orden se acepta, el guerrero llega y el primer golpe no roba
nada (`RN-COMBAT-021`). Rechazarla al ordenar diría desde lejos cuánto tiene la víctima (ver Q9).

## 6. Eventos y contratos de red

Cliente → servidor: `unit.pillage { unitId, cityId }`, comando nuevo y estricto, aditivo en v1 como los de E1 y E2.
Servidor → cliente, sin mensajes nuevos: `unit.movement.*` para la aproximación, `entity.update { status:
ATTACKING, carrying }` del guerrero y `city.update { stocks }` de la víctima. `city.update` gana el campo opcional
`pillagedAtMs` (R-VER-2), que sólo recibe el dueño (Q7). Evento de dominio en `world_events`: `CityPillaged`
por golpe, con el guerrero, la ciudad y la cantidad.

Cliente: con un guerrero seleccionado, un clic sobre el 3 × 3 de una ciudad **ajena** es la orden de saquearla
(hoy es la de guarnecerse, que acaba en `TREATY_REQUIRED` porque no hay tratados en caliente, backlog Q-04); en
la propia sigue siendo guarnecerse. El HUD del saqueado muestra «¡Están saqueando tu ciudad!» y el del
saqueador, la carga que lleva.

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | El saqueo en curso (guerrero → ciudad, `next_hit_at_ms`), los stocks, la carga y `pillagedAtMs` |
| Write-through | Cada golpe de saqueo: el débito del stock de la víctima con su `CityPillaged`, en una transacción en dos fases (ADR-026) |
| Dirty-flag | La carga del guerrero, en el volcado de unidades, **sólo lo ya confirmado** (ver abajo) |
| Reconstruible / no persistido | El saqueo en curso y `ATTACKING`, como en E2: un reinicio lo termina y el guerrero queda `IDLE`. `pillagedAtMs` se pierde al reiniciar |

**Por qué hace falta un ADR.** Hasta E5 el stock tenía dos escritores ([ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md)
§1): el volcado, que sólo suma depósitos, y el encargo, que resta en la transacción de su efecto. El saqueo es
un tercero que resta, y su efecto (la carga del guerrero) vive en otra fila con otra clave de la cola. Si el
volcado de unidades escribiera la carga robada antes que el débito de la víctima, una caída entre ambos
**duplicaría madera**, justo lo que prohíbe [INV-COMBAT-003](../invariants/combat.md#inv-combat-003).

**Decisión ([ADR-026](../decisions/ADR-026-pillage-two-phase-stock-debit.md)).** El golpe de saqueo es una orden en dos fases, como un encargo (ADR-023 §2): el tick
reserva (resta del stock en RAM y suma a la carga como **pendiente**), la transacción con la clave de la ciudad
víctima escribe el delta versionado del stock («depósitos pendientes − robado») y su `CityPillaged`, y el resultado
vuelve al loop por el buzón. Con éxito, lo pendiente pasa a carga confirmada; con un fallo definitivo, vuelve al
almacén de la víctima. El volcado de unidades escribe sólo la carga confirmada. Una caída puede así perder lo
robado (la víctima ya pagó y el guerrero aún no lo tiene en disco), nunca crearlo. El ADR fija también la
idempotencia por versión y los casos de borde: depositar o morir con carga pendiente.

Sin migración: `world_events.event_type` no tiene `CHECK`, `units.loot` ya existe (`000007`) y `ATTACKING` sólo
vive en la RAM. La siguiente libre sigue siendo `000009`.

## 8. Invariantes

Cita [INV-ECON-001](../invariants/economy.md#inv-econ-001) (stock nunca negativo),
[INV-ECON-002](../invariants/economy.md#inv-econ-002) (todo débito en la transacción de su efecto: el saqueo
lo cumple con las dos fases de §7), [INV-ECON-006](../invariants/economy.md#inv-econ-006) y
[INV-CITY-006](../invariants/city.md#inv-city-006). **Amplía** [INV-COMBAT-002](../invariants/combat.md#inv-combat-002)
a los golpes de saqueo y [INV-COMBAT-003](../invariants/combat.md#inv-combat-003) a «un combate o un saqueo nunca
crea madera». **Asigna** [INV-COMBAT-004](../invariants/combat.md#inv-combat-004) (ningún saqueo deja el almacén
por debajo del suelo, `RN-COMBAT-023`) e [INV-COMBAT-005](../invariants/combat.md#inv-combat-005) (ningún golpe de
saqueo cae con el dueño del saqueador ausente, `RN-COMBAT-024`).

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | Validación de `unit.pillage` (cada fila de §5); alcance en el anillo y elección del tile; cantidad robada con cada límite (golpe, hueco, suelo); fin por cada causa de `RN-COMBAT-025` |
| simulation | Golden «saqueo»: guerrero que cruza el mapa, cuatro golpes en sus ticks exactos, vuelta y depósito; dos saqueadores sobre una ciudad cerca del suelo (orden de `RN-COMBAT-022`); guardia que responde a un saqueo y recupera la carga al matar al saqueador |
| recovery | Reinicio con un saqueo en curso; caída entre la transacción del golpe y el volcado de la carga (se pierde, no se duplica) |
| integration | Transacción del golpe con fallo inyectado (ni débito sin `CityPillaged` ni al revés); el delta versionado del saqueo entre un volcado y un encargo de la víctima |
| contract / e2e | `unit.pillage` y `pillagedAtMs` contra el JSON Schema; recorrido completo por WebSocket |
| cliente | Clic con guerrero sobre una ciudad ajena, aviso de saqueo en el HUD; `scripts/smoke.mjs` añade `--saqueo` contra `smoke_victim` |

## 10. Decisiones de producto (2026-11-03)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de
producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Qué se ataca de una ciudad: el **almacén** (saqueo) o las **casas** (destrucción)? | **Saqueo**, esta spec. Da al atacante algo que ganar y al defensor algo que recuperar, sin vida de edificios ni migración. Destruir casas se puede hacer después como «E6 Asedio», pero pide una decisión más dura: una casa destruida baja `population_limit`, y con la población al tope la ciudad quedaría por encima de su límite, que hoy prohíben [INV-CITY-002](../invariants/city.md#inv-city-002) y el `CHECK cities_population_within_limit`. Habría que permitir el exceso (y bloquear sólo el entrenamiento) o matar unidades, y las dos cosas cambian la economía de E1. |
| Q2 | ¿Cuánto se roba por golpe y cuánto carga el guerrero? | 5 por golpe y carga de 20 (§4.2). Con la carga de hoy, 10, saquear rinde lo mismo que recolectar y nadie cruzaría el mapa para hacerlo. Subirla también dobla el botín máximo de E2, que me parece bien: hace más valioso matar a un saqueador. |
| Q3 | ¿Hay un suelo por debajo del cual no se roba? | Sí, 50 de madera, el coste de un aldeano. Una ciudad saqueada siempre puede volver a producir; sin suelo, un jugador grande podría dejar a uno pequeño a cero en cada desconexión. |
| Q4 | ¿Puede saquear un guerrero cuyo dueño está desconectado? | No, como la recolección (`INV-ECON-006`): saquear es una acción del jugador presente. Si no, bastaría dejar diez guerreros pegados a una muralla y apagar el ordenador. |
| Q5 | ¿Defienden la ciudad sus guerreros parados? | Sí: un golpe de saqueo cuenta como un golpe a una unidad en el centro de la ciudad (`RN-COMBAT-027`), con el radio de 4 de E3. Es el uso natural de dejar guerreros en casa. Ojo con la observación de balance abierta de E3 (varios guerreros parados juntos son muy fuertes): aquí juega a favor del defensor, que me parece correcto. |
| Q6 | Con la carga llena, ¿vuelve solo a casa? | No: queda `IDLE` junto a la muralla. El viaje de vuelta es la ventana de riesgo del saqueador y debe decidirlo él; volver solo convertiría el saqueo en una granja automática. |
| Q7 | ¿Se avisa al saqueado? | Sí, si está conectado: aviso en el HUD a partir de `pillagedAtMs`. Sin correo ni notificación fuera del juego, que no existen. Un desconectado lo verá en su almacén al volver; un registro de «qué pasó mientras no estabas» sería otra spec. |
| Q8 | ¿Se puede saquear una ciudad `OFFLINE_PENDING` (recién desconectada, aún sin proteger)? | Sí, igual que sus unidades (`RN-COMBAT-003`): es la ventana de riesgo de la desconexión, y quitarla dejaría el saqueo sólo contra jugadores conectados que lo ven venir. |
| Q9 | Hoy `city.update` difunde `stocks` a todo el chunk: cualquiera ve el almacén ajeno. ¿Se oculta? | No en este slice: saber cuánto tiene la víctima es el «explorar antes de atacar» de la referencia. Ocultarlo es niebla de guerra, que merece su propia spec. |

## 11. Documentos relacionados

- [combat-raid.md](combat-raid.md) (botín, fase 4), [combat-response.md](combat-response.md) (respuesta y correa),
  [economy-wood.md](economy-wood.md) §7 y [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) (stock por deltas).
- [city.md](city.md) (muralla 3 × 3, `RN-CITY-009`), [presence.md](presence.md) (`OFFLINE_PENDING` y `PROTECTED`).
- [../roadmap/roadmap.md](../roadmap/roadmap.md) §5, fase «Sieges», de la que este slice es el primer paso.
