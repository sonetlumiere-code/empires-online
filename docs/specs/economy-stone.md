# Economía: la piedra de las colinas y la Edad de Bronce

Propuesta del segundo recurso: piedra que sólo hay en las colinas, recogida con la misma orden que la madera, y
su primer uso, avanzar la ciudad a la Edad de Bronce. Quien tiene colina cerca la consigue en minutos; quien no,
manda expediciones largas o, cuando exista, comercia.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-ECON-STONE` |
| Estado | Implemented (2026-11-04). Aceptada el 2026-11-04 con las preguntas de §10 decididas ese día con la recomendación de cada una; en `main` desde la fusión `d12db5e`, con la CI en verde el mismo día. |
| Milestone | E9 Piedra (noveno slice después de M7) |
| Canon | §5 (terreno), §10 (eras y límite de población), §12 (persistencia), §16 (errores) |
| Depende de | [economy-wood.md](economy-wood.md), [economy-forest.md](economy-forest.md), [city.md](city.md), [combat-siege.md](combat-siege.md), [combat-pillage.md](combat-pillage.md), [movement.md](movement.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) (stock por deltas versionados, encargos en dos fases), [ADR-025](../decisions/ADR-025-forest-wood-layer.md) (el recurso de un tile en una capa aparte del terreno), [ADR-029](../decisions/ADR-029-population-over-limit-and-house-destruction.md) (población por encima del límite). [ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md): la capa de recurso por tile con una instancia por terreno, los costes en varios recursos y el avance de era como encargo de la ciudad con su propia fila. |
| Reemplaza a | «Otros recursos» y «eras» del no-scope de [economy-wood.md](economy-wood.md) §2; el `NOT_IMPLEMENTED` del avance de era de [city.md](city.md) §8, sólo para el paso de `STONE_AGE` a `BRONZE_AGE`. Responde en parte a Q-11 y Q-12 de [backlog.md](../roadmap/backlog.md). |
| Invariantes | Amplía [INV-ECON-002](../invariants/economy.md#inv-econ-002), [INV-ECON-005](../invariants/economy.md#inv-econ-005), [INV-ECON-007](../invariants/economy.md#inv-econ-007) e [INV-ECON-008](../invariants/economy.md#inv-econ-008); asigna [INV-CITY-018](../invariants/city.md#inv-city-018) (§8) |

## 1. Objetivo

Que la geografía pese en la economía y que haya algo por lo que comerciar. Hoy la madera está garantizada junto a
cada ciudad nueva (`RN-CITY-023`) y todas pueden hacer lo mismo con el mismo esfuerzo. La piedra no está en
todas partes: sólo en las colinas, que el mapa concentra en unas pocas manchas. Es el pilar 5 de
[game-pillars.md](../product/game-pillars.md) («la posición geográfica es una ventaja permanente o un problema
permanente») aplicado a la economía, y prepara el terreno para el comercio de E10 y para la tecnología (Q-12).

## 2. Scope

**Entra.** El recurso `STONE` en `city_stocks`; recoger piedra en un tile `HILL` con la misma orden `unit.gather`
(el terreno del tile decide el recurso); la piedra restante de cada tile de colina, su agotamiento y su
regeneración con las reglas del bosque; la comprobación de que el depósito queda al alcance antes de aceptar el
viaje; el avance de una ciudad de `STONE_AGE` a `BRONZE_AGE` pagando madera y piedra; el límite de población que
sube con la era; costes en más de un recurso; en el cliente, el contador de piedra, la colina dibujada según su
piedra, la era de la ciudad y el botón de avanzar.

**No entra** (`Fuera de MVP` de este slice): comercio (E10) y caravanas; las eras `IRON_AGE` y `CASTLE_AGE`; nada
de lo que una era desbloquee aparte del límite (tecnologías, unidades, edificios); saquear piedra; garantizar
piedra en la fundación; comida, oro u otros recursos; almacén con capacidad; edificios de piedra; los rasgos de
civilización (`economy.gather_rate` y demás siguen sin aplicarse); cambiar el terreno; ocultar el almacén o la era
ajenos (niebla de guerra); avisos en vivo de lo que les pasa a las expediciones.

## 3. Actores

Jugador (ordena recoger y avanzar), cliente web (muestra la piedra, la colina y la era), Game Server (único que
decide cuánta piedra queda, qué se carga, qué se paga y cuándo cambia la era,
[INV-SEC-001](../invariants/security.md#inv-sec-001)), tick loop (fase 3 para las llegadas, fase 5 para la carga,
la regeneración y el fin del avance, fase 8 para el volcado), PostgreSQL.

## 4. Modelo

### 4.1 Dónde está la piedra

En los tiles `HILL`, que ya existen, se ven y son transitables (coste 18). No hace falta una capa de yacimientos
sembrada aparte: la colina es lo bastante frecuente para tener piedra y lo bastante rara para que importe dónde
está (Q1). El terreno no cambia ([INV-WORLD-005](../invariants/world.md#inv-world-005)): una colina agotada sigue
siendo `HILL`, como un bosque talado sigue siendo `FOREST`.

Datos del mundo canónico (`EO_WORLD_SEED=20260909`, medidos el 2026-11-04 sobre `world_chunks`):

| Medida | Valor |
|---|---|
| Tiles `HILL` | 6 167 de 262 144 (2,35 %); la montaña son 24 (Q-17) y el bosque, 50 216 (19,2 %) |
| Manchas de colina (4-conexas) | 7, de 2 937, 1 477, 593, 568, 388, 187 y 17 tiles |
| Hierba con colina a ≤ 8 / 32 / 64 / 128 / 256 tiles | 3,5 % / 17,5 % / 43,0 % / 75,3 % / 98,9 % |
| Distancia de la hierba a la colina más cercana | mediana 74, p90 187, máximo 283 |
| Ciudades de desarrollo | a 30, 54, 62, 163, 194 (smoke_victim) y 216 tiles; smoke_runner, a 163 |

Casi toda la tierra tiene colina dentro del alcance del pathfinding (256 tiles), así que **nadie queda sin acceso a la
piedra**: lo que cambia de una ciudad a otra es cuánto tarda y cuánto se expone. Es ventaja comparativa, no
dependencia absoluta (Q2).

**Precisión (2026-11-04): el tope de nodos del A\* recorta ese alcance.** Medido sobre 388 puntos de hierba del mapa
canónico con colina a ≤ 256 tiles, buscando la ruta a la colina más cercana:

| Colina a… | Puntos | Con 20 000 nodos (hasta ADR-031) | Con 50 000 (desde ADR-031) | Con 100 000 | p95 de la búsqueda con 50 000 |
|---|---|---|---|---|---|
| 0–95 tiles | 249 | 249 | 249 | 249 | ≤ 5,3 ms |
| 96–127 | 42 | 39 | 42 | 42 | 7,0 ms |
| 128–159 | 43 | 18 | 43 | 43 | 10,8 ms |
| 160–191 | 25 | 4 | 25 | 25 | 12,8 ms |
| 192–255 | 29 | 0 | 22 | 29 | 21,3 ms |
| **Total** | **388** | **310 (80 %)** | **381 (98 %)** | **388** | |

Con 20 000 nodos, una de cada cinco ciudades (las que tienen la colina a más de unos 130 tiles; entre ellas
smoke_runner y smoke_victim) no podía recoger piedra: la orden se rechaza al darla (`RN-ECON-020`) y sólo le quedaba el
comercio de E10. Producto decidió el mismo día subir el tope a **50 000**
([ADR-031](../decisions/ADR-031-pathfinding-node-cap-50k.md)): llega el 98 % de la tierra, a cambio de que la búsqueda
más cara pase de ~8 a ~20 ms y de que una orden de recoger lejana, con su ida y su vuelta, pueda llevarse unos 40 ms
de un tick. Si el tick se resiente, lo siguiente es el A\* jerárquico de la deuda DEBT-04, no subir más el tope.

### 4.2 Recoger piedra

Dominio: `RN-ECON`, continuando la numeración de [economy-forest.md](economy-forest.md).

| ID | Regla |
|---|---|
| `RN-ECON-019` | `unit.gather` acepta como `target` un tile `HILL` con piedra, además de un `FOREST` con madera. **El terreno del tile decide el recurso**: `FOREST` → `WOOD` (tarea `GATHER_WOOD`), `HILL` → `STONE` (tarea `GATHER_STONE`). La ruta, `WORKING`, el depósito, la regla de la ausencia y la sustitución de la tarea son las de `RN-ECON-001` a `RN-ECON-006`. Cambiar de recurso es dar otra orden, que pierde la carga como cualquier sustitución. |
| `RN-ECON-020` | **El depósito tiene que quedar al alcance.** La orden se rechaza con `PATH_TOO_LONG` si el tile de trabajo está a una distancia de Chebyshev mayor que `EO_PATHFINDING_MAX_DISTANCE` del tile de depósito de la ciudad (`RN-ECON-004`). Sin esta regla, un aldeano llevado lejos con `unit.move` cargaría y no podría volver: la tarea terminaría sin ruta y perdería la carga. Vale también para la madera: un aldeano llevado lejos ya podía caer en esa trampa con un bosque lejano, aunque con uno a ≤ 8 de la ciudad (`RN-CITY-023`) nadie tenía motivo. Si la ruta de vuelta falla por otra causa (el tope de nodos, una muralla nueva), la tarea termina como hoy. |
| `RN-ECON-021` | **La piedra de la colina** sigue las reglas de la madera del bosque con sus propios parámetros (§4.3): cada tile `HILL` tiene `hillStone`; la carga se lleva `min(carry, remaining)` en orden de `id` de unidad (`RN-ECON-013`); un tile agotado rechaza `unit.gather` con `INVALID_TARGET` (`RN-ECON-014`); y vuelve a estar lleno `hillRegrowHours` después de su última carga, anclado a instantes absolutos (`RN-ECON-016`) (Q7). |
| `RN-ECON-022` | **Tile agotado durante la tarea.** Como `RN-ECON-015`, buscando el tile `HILL` con piedra más cercano a ≤ `forestSearchRadius` = 6 del tile de trabajo. Nunca cambia de recurso: un aldeano de piedra no se pasa a un bosque. |
| `RN-ECON-023` | El depósito suma la carga al stock `STONE` de la ciudad, por el mismo volcado de deltas que la madera ([ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md)). La piedra de la colina es **pública**, como la madera del bosque (`RN-ECON-018`). |

**Precisión (2026-11-04).** Lo que la implementación decide dentro de estas reglas:

- **La piedra no es botín.** Hasta E9 la carga de un aldeano que muere pasaba a su asesino como botín de madera
  (`RN-COMBAT-010`). Con piedra, eso la convertiría en madera ([INV-COMBAT-003](../invariants/combat.md#inv-combat-003)):
  la piedra que lleva un aldeano muerto se pierde con él, y el botín sigue siendo sólo madera, en la línea de Q8
  (`TestLaPiedraDeUnAldeanoMuertoNoEsBotin`).
- **`RN-ECON-020` comprueba la ruta de vuelta, no sólo la distancia.** La primera versión miraba sólo la distancia de
  Chebyshev, y la prueba en vivo encontró que con el tope de nodos del A\* (`EO_PATHFINDING_MAX_NODES`, 20 000) una
  vuelta de 165 tiles no tiene ruta aunque quepa en los 256: un aldeano acercado a mano habría cargado y perdido la
  carga. La orden calcula ahora la ruta del tile de trabajo al depósito con los mismos topes y, si no la hay, responde
  `PATH_TOO_LONG` (o `PATH_NOT_FOUND`) al darla (`TestLaVueltaAlDepositoTieneQueTenerRuta`).
- **Una sola capa por terreno.** El dominio `forest` pasó a `deposit`, con una capa para el bosque y otra para la
  colina con los mismos números ([ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md) §1); el golden «tala»
  no cambió.
- **La carga viaja con su recurso** sólo cuando existe: `carryingResource` acompaña a `carrying` en la vista de la
  unidad y en `entity.update`, y falta cuando no hay carga.

### 4.3 Valores de partida

Parámetros de catálogo, como los de [economy-forest.md](economy-forest.md) §10 Q7: se ajustan sin tocar la spec.

| Parámetro | Valor propuesto | Por qué |
|---|---|---|
| `hillStone` | 100 por tile | El del bosque (Q7): la escasez viene de dónde está la colina, no de cuánto da un tile |
| `hillRegrowHours` | 24 h tras la última carga | El del bosque |
| `workMs`, `carry` | 10 000 ms, 10 | Los del aldeano para la madera |

Orden de magnitud con un aldeano a unos 0,7 s por tile (600 ms en hierba recta, 849 en diagonal, 1 080 al entrar
en una colina):

| Ciudad con la colina a… | Viaje de ida y vuelta | Piedra por minuto y aldeano | 200 de piedra con cinco aldeanos |
|---|---|---|---|
| 8 tiles (3,5 % de la hierba) | ≈ 21 s | ≈ 28 | ≈ 1,5 min |
| 74 tiles (la mediana) | ≈ 115 s | ≈ 5 | ≈ 8 min |
| 194 tiles (smoke_victim) | ≈ 280 s | ≈ 2 | ≈ 20 min, con las expediciones expuestas a incursiones |

### 4.4 Avanzar a la Edad de Bronce

Una ciudad avanza de era pagando y esperando, como un entrenamiento. El límite de población ya sale de la era
(`RN-CITY-014`: `population_limit = eras.population_cap + 5 × casas COMPLETE`), así que pasar de `STONE_AGE` (20)
a `BRONZE_AGE` (50) sube el límite en 30, hasta 90 con las ocho casas.

| ID | Regla |
|---|---|
| `RN-ECON-024` | `city.advance { cityId }` exige ciudad propia (`CITY_NOT_FOUND` si no), era `STONE_AGE`, ningún entrenamiento ni avance en curso (`CITY_BUSY`) y stock suficiente en **cada** recurso del coste (`INSUFFICIENT_RESOURCES`). Desde `BRONZE_AGE` responde `NOT_IMPLEMENTED`: el paso siguiente llega con su propio slice (Q6). |
| `RN-ECON-025` | El encargo es **en dos fases**, como `city.train` ([ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) §2): el tick valida y reserva el coste; la transacción debita **todos** los recursos y registra el avance, o no debita ninguno; un rechazo devuelve a la RAM lo reservado. Idempotente por `requestId` ([ADR-014](../decisions/ADR-014-command-idempotency.md)). |
| `RN-ECON-026` | **Mientras avanza, la ciudad no entrena**: `city.train` responde `CITY_BUSY`, igual que `city.advance` con un entrenamiento en curso. Construir casas sí se puede. El avance no se cancela (como `RN-ECON-012`) y ningún ataque lo interrumpe (Q5). |
| `RN-ECON-027` | Al vencer (fase 5), `era = BRONZE_AGE` y `population_limit` se recalcula con la nueva `population_cap`, en una transacción con el evento `EraAdvanced`. Optimista en la RAM con compensación si la escritura se descarta, como la casa que se completa (`RN-ECON-009`). |
| `RN-ECON-028` | **La era nunca retrocede.** Ni la destrucción de casas, ni la ausencia, ni la protección la tocan. Una ciudad por encima de su límite por un asedio ([ADR-029](../decisions/ADR-029-population-over-limit-and-house-destruction.md)) puede avanzar: el avance sólo sube el límite. |

| Parámetro | Valor propuesto | Por qué |
|---|---|---|
| Coste de `BRONZE_AGE` | 400 de madera y 200 de piedra | La madera, unos 4–5 min con tres aldeanos junto al bosque; la piedra, de minutos a una sesión según la colina (§4.3). Ocho aldeanos de madera: es una meta, no un trámite (Q5) |
| Duración | 90 s | Larga para notarse en la ciudad parada, corta para que no se pierda en una sesión |

### 4.5 Costes en más de un recurso

Hoy un coste es un solo recurso (`economy.Cost`). El avance es el primero que paga en dos. Se valida y se reserva
cada recurso en la RAM, y la transacción del efecto lleva un delta por fila de `city_stocks` con su versión; la
compensación devuelve todos. La casa, el aldeano y el guerrero siguen costando sólo madera (Q3).

## 5. Errores

Sólo códigos existentes del catálogo (canon §16).

| Código | Condición |
|---|---|
| `INVALID_TARGET` | `unit.gather` sobre un tile que no es `FOREST` ni `HILL`, o sobre un `HILL` agotado (`RN-ECON-021`) |
| `PATH_TOO_LONG` | El tile de trabajo está a más de `EO_PATHFINDING_MAX_DISTANCE` del depósito (`RN-ECON-020`), o la ruta hasta él es demasiado larga, como en `unit.move` |
| `PATH_NOT_FOUND`, `TARGET_OUT_OF_BOUNDS` | Como en `unit.move`, para el tramo a la colina |
| `CITY_NOT_FOUND` | `city.advance` sobre una ciudad inexistente o ajena (misma respuesta: no revela existencia) |
| `CITY_BUSY` | `city.advance` con un entrenamiento o un avance en curso; `city.train` durante un avance (`RN-ECON-026`) |
| `INSUFFICIENT_RESOURCES` | Falta madera o piedra para el avance (`RN-ECON-024`) |
| `NOT_IMPLEMENTED` | `city.advance` desde `BRONZE_AGE` |

## 6. Eventos y contratos de red

Cliente → servidor: `city.advance { cityId }`, comando nuevo y estricto, con `requestId` e idempotencia como
`city.train`, y clasificado en `internal/game/simulation/actions.go` como acción sobre la ciudad propia (no
hostil). `unit.gather` no cambia de forma: su `target` puede ser una colina.

Servidor → cliente, todo aditivo en v1 ([websocket-protocol.md](websocket-protocol.md), `R-VER-2` y `R-VER-3`):

- `Stocks` gana `STONE` opcional, y `ResourceType` el valor `STONE` (hoy ningún mensaje lo usa, así que no cambia
  ningún contrato).
- `UnitView` y `entity.update` ganan `carryingResource` opcional, que acompaña a `carrying` para que el cliente
  dibuje y nombre la carga.
- `world.snapshot` gana `hills: [{ x, y, remaining, regrowsAtMs }]` opcional, con los tiles de colina del área que
  no están llenos, y aparece `hill.update { x, y, remaining, regrowsAtMs }`, difundido al chunk del tile: los
  gemelos de `forests` y `forest.update`.
- `CityView` gana `advance: { era, completesAtMs } | null` opcional, y `city.update` los opcionales `era` y
  `advance` (`null` al terminar).

Con ellos, el protocolo v1 pasa a 19 mensajes cliente→servidor y 21 servidor→cliente.

Eventos de dominio en `world_events`: `HillDepleted` cuando un tile llega a 0, como `ForestDepleted`;
`EraAdvanceOrdered` con el encargo, como `TrainingOrdered`; y `EraAdvanced` al terminar el avance, con la ciudad y
las dos eras. Ninguno implica a otro jugador y ninguno entra
en el registro de E7: el registro es lo que te hicieron otros y lo que tus unidades hicieron solas
([event-log.md](event-log.md)).

Cliente: con un aldeano seleccionado, un clic sobre una colina es recoger piedra, como un clic sobre un bosque es
recoger madera. La colina a medias y agotada se dibuja como el bosque talado. El HUD suma la piedra junto a la
madera y muestra la era de la ciudad; el botón «Edad de Bronce» dice su coste y, desactivado, por qué
(`trainBlockReason`). Durante el avance, una barra de progreso y los botones de entrenar desactivados con «La
ciudad está avanzando de era».

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | La capa de piedra (`remaining` e instante de regeneración por tile tocado), el stock `STONE`, el avance en curso |
| Write-through | La carga de piedra: su descuento va en la transacción de la tarea, como el del bosque ([ADR-025](../decisions/ADR-025-forest-wood-layer.md) §2); el encargo del avance (débito de los dos recursos y fila del avance, idempotente por `requestId`); su fin (`era`, `population_limit`, `cities.version + 1` y `EraAdvanced`) |
| Dirty-flag | Los depósitos de piedra, en el volcado de deltas de [ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md) |
| Reconstruible / no persistido | La regeneración vencida (un tile cuyo instante pasó se carga lleno y su fila se borra); `population_limit`, de la fórmula con la era; un avance vencido durante una caída termina en el primer tick tras el arranque |

**Por qué hace falta un ADR.** Tres decisiones con alternativas que merecen memoria, que fija
[ADR-030](../decisions/ADR-030-stone-layer-and-era-advance.md):

1. **Costes en varios recursos.** El débito de ADR-023 es un delta versionado sobre una fila `(ciudad, recurso)`.
   Un encargo que paga en dos lleva dos deltas en la misma transacción, y la compensación tiene que devolver los
   dos. Es la ampliación de [INV-ECON-002](../invariants/economy.md#inv-econ-002): un coste se debita entero o no
   se debita.
2. **Dónde vive el avance.** La propuesta es una tabla aparte, `city_era_advances (city_id PK, era,
   completes_at_ms, request_id, created_at)`, y no una fila más de `city_productions`. Una producción reserva
   población y la cuenta [INV-CITY-017](../invariants/city.md#inv-city-017) (`population` es unidades vivas más
   producciones en curso); el avance no reserva nada y no debe entrar en ese recuento. «Una cosa a la vez» la
   decide la RAM, que es el único escritor.
3. **La capa de piedra.** La propuesta es una tabla hermana, `hill_stone`, con la forma de `forest_wood` y su
   propio `CHECK (remaining BETWEEN 0 AND 100)`, y el código de la capa parametrizado por recurso. Generalizar
   `forest_wood` a una tabla por `(tile, recurso)` obligaría a migrar datos de una tabla en uso a cambio de nada:
   un tile es bosque o colina, nunca las dos cosas.

**Migración `000012`.** `STONE` en `city_stocks_resource_valid`, con su fila a cero para las ciudades que ya
existen (como hizo `000006` con la madera); `GATHER_STONE` en `unit_tasks_kind_valid`; las tablas `hill_stone` y
`city_era_advances`. `world_events.event_type` no tiene `CHECK`, así que los eventos nuevos no necesitan migración.
La siguiente libre pasará a ser `000013`.

## 8. Invariantes

**Amplía**, con el mismo significado extendido al recurso nuevo:

- [INV-ECON-002](../invariants/economy.md#inv-econ-002) — todo débito ocurre en la misma transacción que su
  efecto: también el del avance, y un coste en varios recursos se debita entero o no se debita.
- [INV-ECON-005](../invariants/economy.md#inv-econ-005) — un avance a medias sobrevive a un reinicio y termina en
  el mismo instante absoluto.
- [INV-ECON-007](../invariants/economy.md#inv-econ-007) — el recurso de un tile está entre 0 y su capacidad y sólo
  existe para el terreno que lo da: madera en `FOREST`, piedra en `HILL`.
- [INV-ECON-008](../invariants/economy.md#inv-econ-008) — una carga nunca crea recurso: lo que gana la unidad es
  exactamente lo que pierde el tile, sea madera o piedra.

**Cita** [INV-ECON-001](../invariants/economy.md#inv-econ-001) (el stock nunca es negativo, ya por recurso),
[INV-ECON-004](../invariants/economy.md#inv-econ-004) e [INV-CITY-003](../invariants/city.md#inv-city-003) (el
límite sale de la era vigente: con `BRONZE_AGE`, de su `population_cap`),
[INV-CITY-017](../invariants/city.md#inv-city-017) (el avance no cuenta como población) e
[INV-WORLD-005](../invariants/world.md#inv-world-005) (el terreno no cambia).

**Asigna** [INV-CITY-018](../invariants/city.md#inv-city-018): *la era de una ciudad nunca retrocede*. Hasta hoy era
cierto por vacuidad, porque sólo el alta escribía la era; con este slice tiene un escritor y un test que lo vigile.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | El recurso que decide el terreno; `RN-ECON-020` en el borde exacto de la distancia; carga parcial y nula de piedra; búsqueda del tile siguiente sin pasarse al bosque; validación de `city.advance` (cada fila de §5); coste en dos recursos que no alcanza en uno solo; límite con `BRONZE_AGE` y casas |
| simulation | Golden «cantera»: un aldeano a una colina, tres cargas de piedra con sus ticks exactos y el depósito en el stock `STONE`; avance que termina en su tick, sube el límite en 30 y rechaza un `city.train` mientras dura; un avance pedido con un entrenamiento en curso; una ciudad por encima de su límite que avanza |
| recovery | Reinicio con un avance a medias (termina en su instante) y con un avance vencido durante la caída; tiles de colina a medias y una regeneración vencida |
| integration | Encargo del avance con fallo inyectado (ni piedra ni madera gastadas sin avance, ni avance sin las dos); idempotencia por `requestId`; `CHECK` de `hill_stone` y de los valores nuevos; la carga de piedra con la tarea en una transacción |
| contract / e2e | `city.advance`, `hill.update`, `hills`, `STONE`, `carryingResource`, `era` y `advance` contra el JSON Schema; recorridos completos por WebSocket. `hill.update` llega en el mismo commit que su e2e (el `TestMain` de `schema_e2e_test.go` lo exige) |
| cliente | Contador de piedra, era y botón de avanzar con su motivo; clic sobre una colina; dibujo de la colina a medias y agotada. `scripts/smoke.mjs` añade `--piedra`, que llega hasta la primera carga: las ciudades del humo están a 163 y 194 tiles de la colina y un viaje entero dura minutos. El avance en vivo necesita stock: lo siembra `pnpm run dev:seed` en la base de desarrollo, nunca en una migración |

**Precisión (2026-11-04).** `--piedra` espera también el depósito, y el avance en vivo tiene su propia opción,
`--avance`, que es irreversible por ciudad. La semilla del almacén es `EO_DEVSEED_STOCK_FOR=usuario pnpm run dev:seed`
([../operations/local-development.md](../operations/local-development.md) §3-bis.8).

## 10. Decisiones de producto (2026-11-04)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de
producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Dónde está la piedra? | **En la colina** (§4.1). Las alternativas son peores. Una **capa de yacimientos sembrada desde la semilla** permite ajustar la densidad, pero añade un generador que verificar en cada arranque como el terreno, y un dibujo nuevo, para algo que la colina ya da. **Piedra garantizada junto a cada ciudad** haría a todas iguales y mataría el comercio antes de nacer. |
| Q2 | ¿Es la geografía una fuente legítima de dependencia? El pilar 4 rechaza la «escasez artificial de recursos aleatoria» y quiere que la dependencia venga de las civilizaciones, que se eligen; el emplazamiento no se elige (sale del nombre de usuario). | **Sí, como complemento.** La colina no es aleatoria ni artificial: sale de la semilla, se ve desde el primer día y no cambia, que es lo que pide el pilar 5. Y no crea dependencia absoluta: el 98,9 % de la tierra tiene colina dentro del alcance del pathfinding, así que todos pueden conseguir piedra, unos en minutos y otros en una sesión. La dependencia por capacidades seguirá siendo la de las civilizaciones. Al aceptar, se anota en la tabla de tensiones de [game-pillars.md](../product/game-pillars.md) como «4 vs. 5» (hecho el 2026-11-04). |
| Q3 | ¿Para qué sirve la piedra? | **Para avanzar a la Edad de Bronce** (§4.4): demanda universal y permanente, y el puente a la tecnología. Las alternativas: **fortificar casas** contra el asedio es pequeña y liga con E8, pero su demanda sólo existe bajo amenaza; **una unidad nueva** tocaría el balance de E3, que sigue abierto; **añadirla al coste de la casa, el aldeano o el guerrero** bloquearía el bucle básico a quien vive lejos de una colina. |
| Q4 | ¿Qué da la Edad de Bronce? | **Sólo el límite**: 50 en vez de 20, hasta 90 con las ocho casas. Lo demás que pueda desbloquear una era es la fase de tecnología y merece su spec. |
| Q5 | ¿Cuánto cuesta y qué bloquea? | **400 de madera y 200 de piedra, 90 s**, y la ciudad **no entrena mientras avanza**, como el Centro Urbano en la referencia: avanzar es una apuesta, no un trámite. Construir casas sí se puede. No se cancela y ningún ataque lo interrumpe: el asedio ya tiene su castigo y no hace falta otro. Los números son parámetros. |
| Q6 | ¿Más eras en este slice? | **No: sólo de Piedra a Bronce.** `IRON_AGE` y `CASTLE_AGE` siguen en el catálogo sin coste, y `city.advance` desde Bronce responde `NOT_IMPLEMENTED`. Darles coste sin nada nuevo que desbloquear sería repetir el mismo paso con números más grandes. **Precisión (2026-11-06, [civilization-goods.md](civilization-goods.md) Q5):** desde E14 hay avance de Bronce a Hierro, con 800 de madera, 400 de piedra y 60 bienes de otras civilizaciones; `CASTLE_AGE` sigue sin coste. |
| Q7 | ¿Cuánta piedra da un tile y vuelve? | **Como el bosque: 100 y 24 h** tras la última carga. La escasez viene de dónde está la colina y no de cuánto da: un número distinto sería una regla más que aprender sin añadir nada. Agotarse para siempre vaciaría las siete manchas en un mundo persistente. |
| Q8 | ¿Se saquea la piedra? | **No en este slice.** El saqueo sigue llevándose madera; su golden y su suelo no cambian. Cuando la piedra tenga precio en el comercio (E10), saquearla será una decisión con datos. **Precisión (2026-11-13, [caravans.md](caravans.md) Q6):** el saqueo sigue sin llevarse piedra, pero la captura de una caravana se lleva su carga entera, piedra incluida. |
| Q9 | ¿Garantiza la fundación algo de piedra? | **No**, ni evita las colinas. Garantizarla sería la segunda alternativa de Q1. Una ciudad nueva sabe desde el primer minuto dónde le queda la colina más cercana. |
| Q10 | ¿Ven los demás mi piedra y mi era? | **Sí**, como la madera de tu almacén (Q9 de E5) y la madera del bosque: el stock viaja al chunk de la ciudad y la era ya está en el snapshot. Ocultarlo es niebla de guerra, que es otra spec. |

## 11. Documentos relacionados

- [economy-wood.md](economy-wood.md) (la tarea, el depósito y los encargos que este slice reutiliza) y
  [economy-forest.md](economy-forest.md) (la capa de recurso por tile y su regeneración).
- [city.md](city.md) §6.3 (el límite por era, `RN-CITY-014` a `RN-CITY-016`) y
  [combat-siege.md](combat-siege.md) (la ciudad por encima de su límite).
- [diplomacy.md](diplomacy.md) (los tratados `TRADE`, que esperan al comercio) y
  [../roadmap/roadmap.md](../roadmap/roadmap.md) §5, fases «Economy», «Technology» y «Trade».
- [../product/game-pillars.md](../product/game-pillars.md), pilares 4 y 5 (Q2).
