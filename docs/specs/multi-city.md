# Multiciudad

Propuesta para que un jugador pueda tener **una segunda ciudad**: un aldeano la funda donde está, pagando de su ciudad,
y desde entonces el jugador tiene una **capital** y otra ciudad, cada una con su era, sus edificios, su almacén y su
población. Es el alcance más pequeño que se puede jugar; lo grande (conquistar ciudades, más de dos, el cliente con un
selector completo) queda para slices siguientes.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-MULTI-CITY` |
| Estado | Implemented (2026-11-25). Producto delegó en la recomendación del agente (sesión desatendida del 2026-11-24): las once preguntas de §10, con su recomendación; en `main` desde la fusión `0e5ea92`, con la CI en verde. |
| Milestone | E34 Multiciudad (trigésimo cuarto slice después de M7) |
| Canon | §9 (protección), §10 (eras y población), §12 (persistencia), §13 (protocolo), §16 (errores), §21 (fuera de MVP) |
| Depende de | [city.md](city.md), [city-fall.md](city-fall.md), [player.md](player.md), [presence.md](presence.md), [territory-conquest.md](territory-conquest.md), [trade-reach.md](trade-reach.md), [event-log.md](event-log.md), [bots.md](bots.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-051](../decisions/ADR-051-multi-city-capital.md): varias ciudades por jugador con una **capital** (la de id menor en pie) que responde por el jugador donde hoy se pregunta por «su ciudad», y el tope de ciudades en pie como índice único parcial de la base sobre un **hueco** (`slot`). Se apoya en ADR-050 (la caída en una transacción y «una ciudad en pie» como garantía de la base, que este slice afloja a «una o dos»). **Pide migración `000031`** (§7). |
| Reemplaza a | En [city-fall.md](city-fall.md), «nadie tiene dos» y la refundación de toda ciudad caída: con dos ciudades, la que cae se arrasa sin refundar (§4.5). En [city.md](city.md) §9, la nota de «un jugador, una ciudad». |
| Invariantes | Precisa [INV-CITY-020](../invariants/city.md#inv-city-020) (de «exactamente una» a «al menos una»). Registra [INV-CITY-022](../invariants/city.md#inv-city-022) e [INV-CITY-023](../invariants/city.md#inv-city-023) (§8), sin cobertura hasta la implementación. Reglas nuevas: `RN-CITY-030` a `RN-CITY-035` (§4.7). |

## 1. Objetivo

Desde E31 una ciudad cae y su dueño refunda; desde E32 los bots asaltan. Pero un jugador sigue teniendo una sola
ciudad, así que crecer es sólo subir de era y la caída lo devuelve todo a cero. La Q3 de [city-fall.md](city-fall.md)
dejó el multiciudad para su propio slice. Este lo abre por la vía más corta: **fundar** una segunda ciudad con un
aldeano, con un tope de dos, sin tocar quién responde por el jugador en el resto del juego (la capital).

### 1.1 Qué supone hoy una ciudad por jugador (lectura del código)

Rutas desde `services/game-server/` salvo que se diga otra cosa:

- **La base.** `cities_one_standing_per_owner`, índice único parcial `(owner_player_id) WHERE fallen_at IS NULL`
  (migración `000030`, `INV-CITY-020`), impide dos ciudades en pie del mismo dueño. `cities_unique_center` también es
  parcial desde `000030`; `cities_owner_idx` no es único. `CityRepo.GetByOwner` (`internal/persistence/postgres/cities.go`)
  lee una sola fila, y los nombres de «la ciudad del otro» en mensajes, registro y clanes toman la de id menor
  (`DISTINCT ON (owner_player_id) … ORDER BY id` en `messages.go`, `eventlog.go`, `clans.go`).
- **La fundación** de la primera ciudad es el alta, un endpoint HTTP (`POST /internal/players`,
  `internal/httpapi/founding.go`), con `founding.FindSite` (`internal/game/founding/site.go`: distancia mínima de 24
  entre centros, bosque al alcance `RN-CITY-023`, el alcance de vecinos de E30). No hay fundación desde el juego. La
  **refundación** de E31 es el trabajo `city.fall` (`internal/game/simulation/assault.go`), con
  `CityFallRepo.FallCity` en una transacción (`internal/persistence/postgres/cityfall.go`) y `applyFall` en la RAM,
  que llama a `handleIntroducePlayer` (`introduce.go`) como un alta.
- **Población y límite** son de la ciudad (`cities.population`, `population_limit`), pero el bono de territorios
  conquistados se cuenta por jugador (`territory.CountConquered`, `internal/domain/territory/claim.go`) excluyendo el
  territorio de «la» ciudad, y la captura da su bono a una ciudad (`conquest.go`, con `CityOf`). Con dos ciudades,
  las dos cobrarían el bono entero.
- **Presencia y protección** están en columnas de la ciudad (`presence_state`, `last_offline_at`, `protection_until`)
  pero las mueve el jugador: `setPresence` (`simulation.go`) cambia «su» ciudad, y la protección del atacante se lee
  de `CityOf` en combate, castillo, saqueo, asedio, conquista, territorio de clan y asalto. La inmunidad de caída es
  por ciudad (`City.FoundedAtMs`, `EO_CITY_FALL_IMMUNITY_HOURS`).
- **Territorio y capital.** `territory_control` es del jugador; la capital es implícita: un territorio que contiene el
  centro de una ciudad suya. `territory.IsCapital` ya recibe una lista de centros; `claimBlocked` (`conquest.go`) le
  pasa sólo el de `CityOf`. La comprobación en la base (`internal/persistence/postgres/conquest.go`, `INV-TERR-011`)
  ya mira todas las ciudades en pie.
- **Lo del jugador copiado en la ciudad.** Civilización, bando, ingreso al bando y clan viven en el `city.City` de la
  RAM (`internal/domain/city/city.go`) y se leen de «la» ciudad: `civilizationOf`, `FactionOf`, `stanceOf`,
  `setCityClan`. Era e investigación son de la ciudad (`cities.era`, `city_research`); las tecnologías, del jugador.
- **La RAM.** `State.cityByOwner` (`state.go`) guarda una ciudad por jugador: `AddCity` la sobrescribe y `CityOf` la
  devuelve. Tiene 41 llamadas en la simulación; varias sólo preguntan si el jugador existe.
- **El protocolo.** Los comandos de ciudad llevan `cityId` (`city.build`, `city.train`, `city.advance`,
  `city.research`, `city.craft`); el comercio, el mercado, la reclamación, el bando y el centro de la vista se deducen
  de «la» ciudad. No hay `ownCityId`: el cliente toma la primera ciudad suya del snapshot.
- **El cliente** (`apps/web`): `computeHud` devuelve `ownCity`, la primera ciudad propia (`src/state/selectors.ts`),
  y todo el HUD de la ciudad cuelga de ella (`app/page.tsx`); «H» lleva la cámara a `ownCityCenter`
  (`src/components/GameCanvas.tsx`).
- **Los bots** (`apps/bots`): `ownCity()` toma la primera ciudad propia (`src/bot.ts`), y `View.city` es una sola;
  detectan la refundación por el cambio de id.
- **El humo** (`scripts/smoke.mjs`) busca «la» ciudad propia con `find` en siete sitios; `--caida` espera la
  refundada.

**Conclusión de diseño:** casi todo lo que pregunta por «la ciudad del jugador» pregunta en realidad por el jugador
(su civilización, su bando, su protección, el destino de su comercio). Si `CityOf` pasa a devolver la **capital**, esas
41 llamadas siguen valiendo; sólo cambian las que de verdad son de cada ciudad: la presencia (todas a la vez), el bono
de territorio (sólo la capital), los centros de la capital territorial (todas) y la caída.

## 2. Scope

**Entra:**

- Fundar una segunda ciudad con un aldeano propio, parado, donde está (§4.1), pagando de la ciudad del aldeano.
- El tope de **dos** ciudades en pie por jugador, garantizado por la base (§7).
- La capital: la ciudad en pie de id menor; `State.CityOf` la devuelve y `State.CitiesOf` da todas (§4.3).
- Lo que es de cada ciudad y lo que es del jugador (§4.4): la presencia para todas a la vez, el bono de territorio sólo
  para la capital.
- La caída con dos ciudades (§4.5): se arrasa sin refundar, y las unidades pasan a la que queda.
- El comando `unit.found_city`, su confirmación y el evento `CityFounded` en el registro (§6).
- El HUD: el botón «Fundar ciudad» y «H» que alterna entre las ciudades propias (§4.6).
- El humo `--multiciudad`.

**No entra:**

- Quedarse una ciudad ajena al asaltarla, vasallos, tributos (otro slice).
- Más de dos ciudades, o un tope que dependa de la era (otro slice: el hueco de §7 ya lo admite).
- Elegir de qué ciudad sale una caravana, un trato o una oferta del mercado: salen de la capital (Q7).
- Trasladar la capital, renombrar ciudades, abandonar una ciudad.
- Que los bots funden (Q10).
- Un selector de ciudades en el HUD más allá de «H» (Q9).

## 3. Actores

- **Jugador** (cliente web): selecciona un aldeano y pulsa «Fundar ciudad».
- **Game Server, simulación**: valida la orden y encola el trabajo de fundación; aplica el resultado en la RAM.
- **Cola de trabajos** (`internal/game/simulation`): el trabajo `city.found`, de dos claves (la ciudad que paga y el
  jugador).
- **PostgreSQL**: la transacción de fundación, el hueco único y el evento.
- **Bots**: no fundan; siguen jugando con su capital.

## 4. Modelo

### 4.1 Fundar (Q1, Q3, Q4)

Un jugador funda con `unit.found_city { unitId }`:

1. **La unidad:** un `VILLAGER` propio, vivo, `IDLE`, fuera de guarnición, de una ciudad en pie.
2. **El jugador:** tiene una sola ciudad en pie (el tope es dos, Q2), y ninguna fundación en vuelo.
3. **La ciudad que paga** (la del aldeano): en la Edad de Bronce o posterior, con 800 de madera y 400 de piedra en el
   almacén (`EO_CITY_FOUND_WOOD`, `EO_CITY_FOUND_STONE`, Q3), y sin una caída en vuelo.
4. **El sitio:** el tile del aldeano es el centro. Pasa las reglas de fundación de siempre (`RN-CITY-001` a
   `RN-CITY-006`, `RN-CITY-023`: dentro del mapa, la zona urbana transitable y libre, a 24 o más de cualquier otro
   centro en pie, bosque al alcance) y no está en territorio de otro jugador ni de un clan ajeno, ni en una Safe Zone.
   El alcance de vecinos de E30 (`RN-CITY-024`) **no** se pide: el sitio lo elige el jugador, y comerciar con
   cualquiera sale de la capital (Q4).

Si todo pasa, la simulación reserva lo pagado (como un encargo) y encola `city.found`. La transacción (§7) vuelve a
comprobar el tope y el sitio bajo el lock de fundación, cobra, crea la ciudad y escribe el evento. Si falla, la
reserva vuelve y la orden se rechaza.

### 4.2 Con qué nace (Q5)

- **Nombre:** el de la capital seguido de « II» (`bot_saqueador_1polis II`).
- **Era:** la de la ciudad que la funda.
- **Almacén** a cero; **edificios**, ninguno.
- **Unidades:** el aldeano fundador pasa a la ciudad nueva (deja la población de la suya) y nacen dos aldeanos más en
  el anillo, como en un alta (`RN-PLAYER-007`): tres en total.
- **Presencia:** la del jugador (la de su capital). **Inmunidad de caída:** la suya, desde su fundación
  (`RN-CITY-029`).
- **Territorio:** si el centro cae en un territorio libre, el jugador lo gana como en un alta (`RN-TERR-007`) y es
  capital territorial (`RN-CONQ-008`).

### 4.3 La capital (Q6)

La **capital** de un jugador es su ciudad en pie de **id menor**: la primera que fundó, o la que queda si cae la otra.
No se elige ni se traslada. `State.CityOf(player)` devuelve la capital, y lo que hoy se pregunta por «la ciudad del
jugador» se responde con ella: civilización, bando, clan, la protección del atacante, el centro de la vista por
defecto, de dónde salen y adónde llegan el comercio, el mercado y las caravanas capturadas, y el nombre que ven los
demás (la de id menor, como ya hacen los mensajes y el registro). `State.CitiesOf(player)` da las dos, por id.

### 4.4 Qué es de cada ciudad y qué del jugador (Q7, Q8)

| Qué | De quién | Con dos ciudades |
|---|---|---|
| Era, edificios, almacén, población y límite, encargos (entrenar, avanzar, elaborar, investigar), muralla | de la ciudad | Cada una la suya. |
| Inmunidad de caída | de la ciudad | Desde la fundación de cada una. |
| Tecnologías, civilización, bando, clan, tratados, mensajes, registro | del jugador | Como hoy. |
| Presencia y protección offline | del jugador | **Las dos ciudades cambian a la vez** (`ONLINE`, `OFFLINE_PENDING`, `PROTECTED`). |
| Bono de territorios conquistados | del jugador | **Sólo la capital** lo cobra; ninguna capital territorial cuenta como conquistada. |
| Comercio, mercado, reclamación de territorio, caravanas | del jugador | Salen de la capital y llegan a ella. |

### 4.5 La caída con dos ciudades (Q6)

- Si cae una ciudad y su dueño tiene **otra en pie**, se arrasa como en E31 (`fallen_at`, edificios destruidos, su
  territorio capital libre) **sin refundar**: sus unidades vivas de fuera y las propias guarnecidas pasan a la que
  queda; las caravanas hacia ella siguen hacia la que queda; sus tratos pendientes caducan y sus ofertas se retiran
  con la custodia a la que queda. Si era la capital, la otra pasa a serlo (es ya la de id menor) y el bono de
  territorio pasa a ella.
- Si cae la **última**, se refunda como en E31 (`RN-CITY-026` a `RN-CITY-028`), en el hueco de la caída.
- En ningún tick un jugador tiene cero ciudades en pie ni tres.

### 4.6 Qué ve el jugador (Q9)

- Con un aldeano propio seleccionado, el HUD muestra **«Fundar ciudad»**, deshabilitado con su motivo (lo dice el
  cliente con las mismas reglas que el servidor donde las conoce: ya tiene dos, la ciudad no ha llegado a Bronce,
  faltan recursos, el aldeano no está parado; el sitio lo decide el servidor).
- **«H»** lleva la cámara a la capital y, pulsada otra vez, a la otra ciudad; el panel de la ciudad muestra aquella a
  la que «H» llevó por última vez (la capital al entrar).
- El registro dice «Fundaste <nombre>».

### 4.7 Reglas

| ID | Regla |
|---|---|
| `RN-CITY-030` | Un aldeano propio, `IDLE` y fuera de guarnición funda una ciudad en su tile con `unit.found_city` si su ciudad está en Bronce o posterior y tiene `EO_CITY_FOUND_WOOD` de madera y `EO_CITY_FOUND_STONE` de piedra, y el sitio pasa las reglas de fundación (`RN-CITY-001` a `RN-CITY-006`, `RN-CITY-023`) fuera de territorio ajeno y de Safe Zones (§4.1). |
| `RN-CITY-031` | Un jugador tiene como mucho dos ciudades en pie. |
| `RN-CITY-032` | La ciudad fundada nace con el nombre de la capital y « II», en la era de la que la funda, con el almacén a cero, el aldeano fundador y dos aldeanos nuevos (§4.2). |
| `RN-CITY-033` | La capital es la ciudad en pie de id menor; responde por el jugador donde la regla no nombra una ciudad (§4.3). |
| `RN-CITY-034` | La presencia y la protección offline son del jugador: todas sus ciudades cambian a la vez; el bono de territorios conquistados sólo lo cobra la capital (§4.4). |
| `RN-CITY-035` | Una ciudad que cae con otra en pie del mismo dueño se arrasa sin refundar y lo suyo pasa a la que queda; la última refunda (§4.5). |

## 5. Errores

Sin códigos nuevos (canon §16):

| Código | Cuándo |
|---|---|
| `UNIT_NOT_FOUND`, `UNIT_NOT_OWNED`, `UNIT_DEAD`, `UNIT_GARRISONED` | La unidad no existe, no es suya, está muerta o guarnecida. |
| `UNIT_NOT_MOVABLE` | No está `IDLE`. |
| `INVALID_TARGET` | No es un aldeano; el jugador ya tiene dos ciudades o una fundación en vuelo; la ciudad no ha llegado a Bronce o tiene una caída en vuelo; el sitio no pasa las reglas de §4.1. |
| `INSUFFICIENT_RESOURCES` | El almacén de la ciudad del aldeano no llega. |
| `CITY_BUSY` | La ciudad del aldeano tiene en vuelo otro trabajo que la cambia. |

## 6. Eventos y contratos de red

- **Cliente → servidor:** `unit.found_city { unitId }`.
- **Servidor → cliente:** sin mensaje nuevo (precisión de §9.1): al confirmar, un `world.snapshot` a las sesiones del
  fundador; si no, `system.error` con el código de §5. La ciudad nueva llega como cualquier ciudad que entra en la vista
  (`city.update` del alta), y sus dos aldeanos con `entity.spawn`.
- **Evento de dominio:** `CityFounded` en `world_events` (`player_id` el fundador, sin `counterpart_player_id`,
  `atMs`, la ciudad nueva y la que pagó). Va al registro del jugador (`CITY_FOUNDED`); no se avisa en vivo: lo hizo él.

Los esquemas viven en Zod (`packages/protocol/src/v1`); [websocket-protocol.md](websocket-protocol.md) §7 y §8 los
recogen al implementar.

## 7. Persistencia

- **Migración `000031`:** `cities.slot smallint NOT NULL DEFAULT 1 CHECK (slot IN (1, 2))`, y el índice único parcial
  `cities_one_standing_per_owner` pasa a `(owner_player_id, slot) WHERE fallen_at IS NULL`. Las filas de hoy quedan en
  el hueco 1. El `down` vuelve al índice de una por dueño (falla si alguien tiene dos en pie: se arrasa una antes).
- **La fundación** es una transacción: el lock de fundación, el tope y el sitio otra vez, el cobro del almacén, la
  ciudad en el hueco libre, el aldeano fundador a la ciudad nueva (y la población de las dos), los dos aldeanos
  nuevos, el territorio si es libre, y `CityFounded`.
- **La caída** (`CityFallRepo`) elige: con otra en pie, arrasa y reasigna a la que queda; si no, refunda en el hueco
  de la caída.
- **Qué es reconstruible:** todo sale de `cities` al arrancar; la capital no se guarda, se deduce (id menor en pie).
- **La recuperación** carga las dos ciudades de cada jugador y la reserva de una fundación en vuelo vuelve como la de
  cualquier trabajo.

## 8. Invariantes

- **`INV-CITY-020`** (precisión): de «exactamente una ciudad en pie» a «**al menos una**»; el tope y la caída, en los
  dos nuevos.
- **[INV-CITY-022](../invariants/city.md#inv-city-022):** un jugador tiene como mucho dos ciudades en pie (base: el
  índice sobre el hueco; dominio: la validación y la transacción).
- **[INV-CITY-023](../invariants/city.md#inv-city-023):** una ciudad cae sin refundar sólo si su dueño tiene otra en pie
  en esa misma transacción; la última siempre refunda.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (`internal/domain`) | La validación del sitio en un tile dado; el nombre de la segunda ciudad. |
| Simulación | `unit.found_city` aceptado y cada rechazo de §5; la reserva y su vuelta; la capital tras fundar y tras caer; la presencia de las dos a la vez; el bono sólo en la capital; la caída con dos (sin refundar, las unidades a la que queda) y con una (refunda). |
| Integración | La migración `000031` (up/down); la transacción de fundación; el hueco único (dos en pie sí, tres no); la caída con dos y con una. |
| Contract | `unit.found_city` y su confirmación en Zod y en Go; el e2e por WebSocket. |
| Recuperación | Un jugador con dos ciudades se carga con su capital; una fundación en vuelo al caer el proceso. |
| Cliente (Vitest) | El motivo de «Fundar ciudad»; «H» que alterna; el HUD de la ciudad elegida. |
| Bots (Vitest) | Con dos ciudades propias en la vista, el bot juega con la capital. |
| Humo | `--multiciudad`: fundar, ver la ciudad nueva, el segundo intento rechazado. |

### 9.1 Precisiones de la implementación (2026-11-24)

- **Precisión (2026-11-24): sin mensaje de confirmación.** §6 nombraba `unit.found_city.accepted`; no hace falta. Al
  confirmarse la fundación, las sesiones del fundador reciben un `world.snapshot` nuevo (centrado en la nueva), por el
  mismo camino que el vencido de una caída ([ADR-050](../decisions/ADR-050-city-fall.md) §5); los rechazos llegan como
  `system.error` con el `requestId`. El protocolo gana un solo tipo cliente→servidor y ninguno servidor→cliente.
- **Precisión (2026-11-24): el snapshot, centrado en la ciudad nueva.** Lo encontró la prueba en vivo: en el mundo de
  desarrollo, denso, el sitio libre más cercano quedó a 80 tiles de la capital, y el snapshot de `Resnapshot`, centrado
  en ella, no traía la ciudad nueva. El de la fundación va centrado en la nueva (`ResnapshotAt`). Y como el store
  olvida las ciudades que salen del área de interés, el cliente recuerda los centros de las propias que ha visto
  (`ownCityHomes`, hasta su caída): «H» lleva a cualquiera de ellas aunque no esté a la vista, y el panel muestra la
  elegida en cuanto llega su snapshot.
- **Precisión (2026-11-24): el sitio, en un territorio libre.** §4.1 decía «no en territorio de otro jugador ni de un
  clan ajeno»; tampoco en uno propio: el centro de la ciudad hace de su territorio una capital territorial, que no
  cuenta como conquistada, y fundar en uno conquistado le quitaría a la capital su bono a mitad de la transacción. El
  territorio libre lo gana la fundación (`RN-TERR-007`).
- **Precisión (2026-11-24): quién comprueba qué.** El tick comprueba la unidad, el tope, la era, el almacén y el sitio
  con `founding.CheckSite` sin la capa de madera, el territorio y la Safe Zone, y reserva lo pagado; la cola vuelve a
  comprobar el sitio con el bosque agotado (`FoundSite`), y la transacción, bajo el cerrojo de fundación, el tope, el
  aldeano, la distancia y el territorio. El aldeano puede moverse mientras la orden va en vuelo: el centro es el tile
  donde estaba al ordenar.
- **Precisión (2026-11-24): la caída con dos ciudades** no busca sitio de refundación: `CityFallRepo.FallCity` mira
  antes si queda otra en pie y, si al confirmar ya no lo está (cayó en el mismo instante), refunda como en E31. Si la
  que cae es la capital, la que queda suma el bono de territorio (los conquistados que no contienen ninguna ciudad en
  pie del jugador).
- **Precisión (2026-11-24): la validación de carga** registra `invariant_violation` con `inv_id=INV-CITY-022` y no
  arranca si un jugador tiene más de dos ciudades en pie, y sólo da el bono de territorio a la capital.

## 10. Decisiones de producto (2026-11-24)

Producto delegó en la recomendación del agente (sesión desatendida del 2026-11-24): cada fila deja la decisión (la recomendación, en negrita) y las alternativas descartadas. Son decisiones de
producto, no de ingeniería: cambiarlas es cambiar la spec, y la persona usuaria puede revisarlas al volver.

| # | Pregunta | Decisión | Descartadas |
|---|---|---|---|
| Q1 | ¿Cómo se consigue la segunda ciudad? | **Fundándola con un aldeano donde está** (`unit.found_city`). Es una vía sola, entera en manos del jugador, y reutiliza la fundación, la transacción y la RAM del alta y la refundación. | **Conquistándola** (quedarse la ciudad asaltada en vez de arrasarla: cambia el dueño inmutable de `RN-CITY-020` y lo de §4.3 de city-fall, otro slice); **las dos vías** (dobla el slice). |
| Q2 | ¿Cuántas como máximo? | **Dos, fijo**, garantizado por la base con un hueco (`slot` 1 o 2). Es lo más pequeño que ya es multiciudad; subirlo es ampliar el `CHECK`. | **Una por era** (1 a 4: más juego, pero pide reglas de qué pasa al bajar de era, que no existe); **configurable** (la base no puede leer `EO_*`). |
| Q3 | ¿De qué depende y cuánto cuesta? | **De la ciudad que funda: desde la Edad de Bronce, 800 de madera y 400 de piedra** (`EO_CITY_FOUND_WOOD`, `EO_CITY_FOUND_STONE`), lo de pasar de Bronce a Hierro. Bronce es la era desde la que una ciudad puede caer: la segunda llega cuando ya hay algo que perder. | **Desde Piedra** (una cuenta nueva tendría dos ciudades el primer día); **desde Hierro** (llega tarde para quien juega poco); **un coste que crezca** (sólo hay una segunda). |
| Q4 | ¿Dónde? | **En el tile del aldeano**, con las reglas de fundación de siempre (24 tiles de cualquier centro, bosque, zona libre), fuera de territorio ajeno y de Safe Zones, **sin** el alcance de vecinos de E30. El sitio lo elige quien camina hasta él. | **Donde diga `FindSite`** (cómodo, pero el jugador no elige); **cerca de la capital, con un radio** (limita sin razón de juego). |
| Q5 | ¿Con qué nace? | **El nombre de la capital con « II», la era de la que la funda, almacén a cero, el aldeano fundador y dos más.** Como una refundación: pagar 800/400 y empezar en Piedra sería castigar dos veces. | **En Piedra** (nace inútil para quien ya está en Hierro); **con un almacén inicial** (teletransporta recursos). |
| Q6 | ¿Qué es la capital y qué pasa si cae? | **La de id menor en pie, sin elección. Si cae una y queda otra, se arrasa sin refundar y lo suyo pasa a la que queda, que es la capital; si cae la última, se refunda como en E31.** Así perder una de dos duele sin dejar a nadie sin ciudad. | **Capital elegida o trasladable** (otro comando y otra regla); **refundar siempre** (con dos ciudades, la caída dejaría de costar). |
| Q7 | ¿Qué es por ciudad y qué por jugador? | **Lo de §4.4:** era, edificios, almacén, población y encargos por ciudad; tecnologías, civilización, bando, clan, tratados y presencia por jugador; el bono de territorio sólo para la capital; comercio, mercado y caravanas desde la capital. Mantiene las 41 preguntas por «la ciudad del jugador» con una sola respuesta. | **Bono de territorio para cada ciudad** (dos ciudades cobrarían lo mismo dos veces); **elegir la ciudad del comercio** (toca trato, mercado y caravanas: otro slice). |
| Q8 | ¿Protección y presencia? | **Del jugador: las dos ciudades pasan juntas a `OFFLINE_PENDING` y `PROTECTED`; la inmunidad de caída, por ciudad desde su fundación.** El jugador está o no está; la segunda ciudad nueva no debe caer el primer día. | **Protección por ciudad** (no hay un «estar en una ciudad»); **la segunda sin inmunidad** (se perdería antes de construir nada). |
| Q9 | ¿Qué cambia en el HUD? | **«Fundar ciudad» con un aldeano seleccionado, y «H» que alterna entre las ciudades; el panel muestra la última a la que se fue.** Lo mínimo para jugar con dos sin un selector nuevo. | **Un selector de ciudades** (más claro, más cliente: cuando haya más de dos); **el panel de la ciudad que se clica** (pide selección de ciudades en la escena, que no existe). |
| Q10 | ¿Y los bots? | **No fundan; juegan con su capital** y no se rompen si ven dos ciudades suyas. Que funden es medir una estrategia nueva: su propio slice, como el asalto (E32). | **El constructor funda al llegar a Hierro** (medido pronto, pero el decisor crece y la prueba en vivo se alarga). |
| Q11 | ¿Qué se registra y avisa? | **`CityFounded` en el registro del jugador; sin aviso en vivo** (lo hizo él). | **Avisar a los vecinos** (no hay contraparte; otro slice si hace falta). |

## 11. Documentos relacionados

- [ADR-051](../decisions/ADR-051-multi-city-capital.md): la capital, el hueco y la caída que elige.

- [city-fall.md](city-fall.md): la caída y la refundación que este slice afina.
- [city.md](city.md): las reglas de fundación (§6) y la población.
- [territory-conquest.md](territory-conquest.md): la capital territorial y el bono.
- [presence.md](presence.md): la protección offline.
- [../architecture/frontend.md](../architecture/frontend.md): el HUD y la cámara.
