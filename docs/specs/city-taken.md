# La ciudad tomada

Propuesta para que el asalto que tumba una ciudad pueda **quedársela**: si el vencedor tiene hueco para otra ciudad,
la caída no arrasa la ciudad sino que la **toma**, y el vencedor gana una segunda ciudad en el mismo sitio con los
edificios que seguían en pie. Es la otra vía de la Q1 de [multi-city.md](multi-city.md), la que E34 dejó fuera.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CITY-TAKEN` |
| Estado | Implemented (2026-11-25). Producto delegó en la recomendación del agente (sesión desatendida del 2026-11-25): las nueve preguntas de §10, con su recomendación; en `main` desde la fusión `7171d45`, con la CI en verde. |
| Milestone | E36 La ciudad tomada (trigésimo sexto slice después de M7) |
| Canon | §9 (protección), §10 (eras y población), §12 (persistencia), §13 (protocolo), §21 (fuera de MVP: conquista de ciudades) |
| Depende de | [city-fall.md](city-fall.md), [multi-city.md](multi-city.md), [city.md](city.md), [territory-conquest.md](territory-conquest.md), [castle-age.md](castle-age.md), [siege-engines.md](siege-engines.md), [event-log.md](event-log.md), [live-notices.md](live-notices.md), [bots-assault.md](bots-assault.md), [bots-found.md](bots-found.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-052](../decisions/ADR-052-city-taken.md): la ciudad tomada es una **fila nueva** del vencedor en el centro de la caída, que hereda los edificios completos; la caída sigue siendo la de [ADR-050](../decisions/ADR-050-city-fall.md) (la fila vieja con `fallen_at`) y el dueño de cada fila sigue inmutable (`RN-CITY-020`). **Pide migración `000032`** (§7). |
| Reemplaza a | En [city-fall.md](city-fall.md) §4.3, «los edificios se pierden» cuando el vencedor tiene hueco; en [multi-city.md](multi-city.md) §2, el «quedarse una ciudad ajena al asaltarla» de lo que no entra. |
| Invariantes | Registra [INV-CITY-024](../invariants/city.md#inv-city-024) (§8), sin cobertura hasta la implementación. Reglas nuevas: `RN-CITY-036` a `RN-CITY-039` (§4.6). Se apoya en [INV-CITY-022](../invariants/city.md#inv-city-022) (como mucho dos en pie) y en [INV-CITY-023](../invariants/city.md#inv-city-023) (la caída que no refunda). |

## 1. Objetivo

Desde E31 una ciudad cae y desde E34 un jugador puede tener dos. Pero tumbar una ciudad sólo destruye: el vencedor no
gana nada que se quede, y la segunda ciudad sólo se consigue fundándola. La Q1 de [multi-city.md](multi-city.md) dejó
«conquistarla» para otro slice porque cambia el dueño de una ciudad, que [city.md](city.md) quiere inmutable
(`RN-CITY-020`). Este slice la abre sin romper esa regla: la ciudad tomada es una ciudad **nueva** del vencedor, en el
mismo sitio, que se queda con lo que seguía en pie.

### 1.1 Qué pasa hoy al caer una ciudad (lectura del código)

Rutas desde `services/game-server/`:

- **La caída** es el trabajo `city.fall` (`internal/game/simulation/assault.go`) con `CityFallRepo.FallCity`
  (`internal/persistence/postgres/cityfall.go`) en una transacción: `fallen_at`, los edificios a `DESTROYED`, los
  encargos borrados, el almacén a cero, el asalto `FELL`, el territorio capital libre (`freeCapital`) y, si el vencido
  no tiene otra en pie, la refundada; si la tiene, sus unidades pasan a ella (`intoKept`). `applyFall` hace lo mismo en
  la RAM y avisa con `city.fallen` y `CITY_LOST`/`CITY_TAKEN`.
- **El dueño** sólo se escribe en el `INSERT` de una ciudad; `cities_unique_center` y `cities_one_standing_per_owner`
  son índices parciales sobre las ciudades en pie (migraciones `000030` y `000031`): una fila nueva en el centro de una
  caída cabe.
- **El límite de población** es el de la era más las casas completas y, en la capital, el bono de territorio
  (`INV-ECON-004`); el castillo dispara por la ciudad dueña del edificio (`castle-age.md`).
- **El cliente** borra con `city.fallen` la ciudad y sus edificios (`apps/web/src/state/world.ts`); una ciudad que
  entra en la vista llega como `city.update` y un edificio como `building.spawn`.

## 2. Scope

**Entra.**

- La toma (§4.1): al caer, si el vencedor tiene hueco (menos de dos ciudades en pie), la ciudad no se arrasa: el
  vencedor gana una ciudad nueva en su centro.
- Con qué nace la tomada (§4.2): nombre y era de la caída, sus edificios completos, tres aldeanos nuevos, almacén a
  cero, inmunidad desde la toma.
- El vencido, como hoy (§4.3): refunda si era la última; si no, sigue con la otra.
- Registro y avisos (§4.4); el protocolo (`city.fallen` con `takenCityId`, §6); el cliente (la tomada aparece con sus
  edificios); los bots (el saqueador que toma juega la tomada como la segunda de E35); el humo `--toma`.
- La migración `000032` y [ADR-052](../decisions/ADR-052-city-taken.md).

**No entra.**

- Elegir entre tomar y arrasar (Q1): se toma si hay hueco.
- Más de dos ciudades (E37), trasladar la capital, vasallos, tributos o rescates.
- Quedarse el almacén o los encargos de la caída (Q3).
- Recuperar una ciudad propia perdida por otra vía que el asalto.

## 3. Actores

| Actor | Papel |
|---|---|
| Vencedor | Tumba la ciudad y, si tiene hueco, se la queda. |
| Vencido | Pierde la ciudad como en E31 y E34. |
| Terceros con la ciudad a la vista | Ven caer la vieja y aparecer la tomada con sus edificios. |
| Game Server | Decide en la transacción de la caída si toma o arrasa ([INV-SEC-001](../invariants/security.md#inv-sec-001)). |

## 4. Modelo

### 4.1 Tomar o arrasar (Q1)

En la transacción de la caída, bajo el cerrojo de fundación, se cuenta las ciudades en pie del **vencedor** (el
dueño de la unidad que sostenía el estandarte): con menos de dos (`city.MaxStandingPerOwner`), la ciudad se **toma**;
con dos, se arrasa como hoy. No hay orden nueva: el asaltante sabe al plantar si tiene hueco. La paz, la protección, la
inmunidad y la era mínima del asalto no cambian ([city-fall.md](city-fall.md) §4.4).

### 4.2 La ciudad tomada (Q2 a Q5, Q8)

| Qué | Qué pasa |
|---|---|
| La fila | **Nueva**, del vencedor, en el mismo centro, en el hueco libre del vencedor; la caída se queda con `fallen_at` como en E31 (`RN-CITY-020` no cambia). `taken_from_city_id` apunta a la caída. |
| Nombre y era | Los de la caída. **Precisión (2026-11-26, [city-names.md](city-names.md)):** si el vencedor ya tiene en pie otra con ese nombre, el primero libre de su serie (`RN-CITY-045`). |
| Edificios completos (casas, taller, mercado, castillo) | **Pasan a la tomada** (cambian de ciudad, con su id y su vida). |
| Edificios en obra y la muralla | En obra: se pierden (`DESTROYED`), como en E31. La muralla está caída (la ciudad estaba en brecha); si la reconstruía, estaba en obra. |
| Almacén, encargos | Arden como en E31 (Q3): la tomada nace con el almacén a cero y sin encargos. |
| Unidades | Las del vencido, como en E31 y E34. La tomada nace con **tres aldeanos nuevos** en su anillo (`RN-PLAYER-007`); el portaestandarte sigue siendo de su ciudad (Q4). |
| Población y límite | Tres; el límite, el de su era más las casas que hereda (`INV-ECON-004`). No es la capital: sin bono de territorio. |
| Inmunidad | La suya, desde la toma (`RN-CITY-029`): nadie la recupera el primer día (Q5). |
| Presencia | La del vencedor. |
| Territorio | El capital del vencido queda libre (como hoy) y la tomada lo gana si está libre (`RN-TERR-007`): es su capital territorial. |
| Castillo | Dispara por su dueño nuevo. |
| Tratos, ofertas, caravanas del vencido | Como en E31 y E34: siguen hacia la que le queda o la refundada. |

### 4.3 El vencido (Q6)

Nada cambia para él: si era la última, refunda (`RN-CITY-026` a `RN-CITY-028`); si tenía otra, sigue con ella
(`RN-CITY-035`). Recibe `city.fallen` y su snapshot como hoy.

### 4.4 Registro y avisos (Q7)

- `CityFell` en `world_events` gana `takenCityId` cuando se toma. El aviso `CITY_TAKEN` del vencedor dice «Tomaste
  <nombre>: ahora es tuya» y el `CITY_LOST` del vencido, «<vencedor> tomó <nombre>» (precisión de §9.1: los textos no
  cambian en este slice).
- El marcador de una guerra de clanes suma la caída como hoy.

### 4.5 Los bots (Q9)

El saqueador de E32 que tumba una ciudad con hueco se la queda y la juega con el perfil de colonia de E35 (aldeanos,
casas, madera), por su conexión colonia. La partida vuelve a casa como hoy. Nada nuevo en su decisor salvo eso.

### 4.6 Reglas

| ID | Regla |
|---|---|
| `RN-CITY-036` | Una ciudad que cae se toma si el vencedor tiene menos de dos ciudades en pie en la transacción de la caída; si no, se arrasa (§4.1). |
| `RN-CITY-037` | La tomada es una ciudad nueva del vencedor en el centro de la caída, con su nombre y su era, sus edificios completos, tres aldeanos nuevos y el almacén a cero (§4.2). **Precisión (2026-11-26, [city-names.md](city-names.md)):** si el vencedor ya tiene en pie otra con ese nombre, el primero libre de su serie (`RN-CITY-045`). |
| `RN-CITY-038` | La inmunidad de caída de la tomada corre desde la toma. |
| `RN-CITY-039` | Para el vencido, tomar es caer: refunda o sigue con la otra como en E31 y E34. |

## 5. Errores

Sin códigos nuevos: la toma no tiene orden propia; los rechazos del asalto son los de [city-fall.md](city-fall.md) §5.

## 6. Eventos y contratos de red

- `city.fallen` gana `takenCityId` (entero, opcional): la ciudad nueva cuando se toma. Un cliente que no lo conoce
  sigue funcionando: la tomada le llega como cualquier ciudad que entra en la vista.
- Tras `city.fallen`, la tomada llega como `city.update` del alta a quien tenga su chunk a la vista, y sus edificios
  heredados como `building.spawn` (con su `cityId` nuevo); sus aldeanos, con `entity.spawn`.
- El vencedor recibe además un `world.snapshot` centrado en la tomada, como el fundador de E34.
- `CityFell` y los avisos, §4.4.

## 7. Persistencia

- **Migración `000032`:** `cities.taken_from_city_id bigint NULL REFERENCES cities(id)` con `CHECK (taken_from_city_id
  <> id)` y un índice único parcial (`taken_from_city_id IS NOT NULL`): una caída se toma como mucho una vez. El `down`
  la quita.
- **La caída que toma**, en la misma transacción de ADR-050: lo de hoy (fallen_at, encargos, almacén, asalto `FELL`,
  territorio libre, el vencido) salvo los edificios completos, que pasan a la fila nueva; la fila nueva, sus tres
  aldeanos, su almacén, su límite y el territorio si está libre; y `CityFell` con `takenCityId`.
- **Recuperación:** la tomada se carga como cualquier ciudad; la validación de carga recuenta su límite con sus casas.

## 8. Invariantes

- **[INV-CITY-024](../invariants/city.md#inv-city-024):** una ciudad caída no conserva edificios en pie: todos están `DESTROYED` o pasaron, en la misma
  transacción, a la ciudad que la tomó (`taken_from_city_id`), y una caída se toma como mucho una vez.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (`internal/domain`) | Tomar o arrasar según las ciudades del vencedor; el límite de la tomada con sus casas. |
| Simulación | La caída con hueco toma (la tomada en la RAM con sus edificios, aldeanos, límite e inmunidad; el castillo de su dueño nuevo) y sin hueco arrasa; el vencido refunda o sigue. |
| Integración | La migración `000032` (up/down); la transacción que toma (fila nueva, edificios movidos, los de obra destruidos, almacén a cero, territorio); sin hueco, la de siempre; la toma única. |
| Contract | `takenCityId` en Zod y en Go; el e2e por WebSocket de una toma. |
| Recuperación | Un jugador con una tomada se carga con su capital y su tomada. |
| Cliente (Vitest) | `city.fallen` con `takenCityId` y los `building.spawn` que siguen; el texto de los avisos. |
| Bots (Vitest) | El saqueador que toma juega la tomada con el perfil de colonia. |
| Humo | `--toma`: el asaltante con una ciudad tumba otra y se la queda con sus casas. |

### 9.1 Precisiones de la implementación (2026-11-25)

- **Precisión (2026-11-25): los textos de los avisos no cambian** (los cambia E39, [city-taken-notices.md](city-taken-notices.md)). §4.4 pedía «Tomaste <nombre>: ahora es tuya» y
  «<vencedor> tomó <nombre>». El aviso no lleva hoy si se tomó, y añadirlo es un campo más en `EventLogNotice` y en la
  entrada del registro para un texto. Se queda para otro slice: `takenCityId` va en `CityFell` (y en `city.fallen`), y
  el vencedor ve su ciudad nueva en el snapshot.
- **Precisión (2026-11-25): quién cuenta el hueco.** El tick pide los tres aldeanos de la tomada
  (`founding.SpawnsAround`) sólo si el vencedor tiene menos de dos ciudades en la RAM; la transacción lo vuelve a
  contar bajo el cerrojo de fundación y, sin hueco, arrasa aunque se los pidieran.
- **Precisión (2026-11-25): el orden de los mensajes.** La tomada entra (`city.update` del alta y `building.spawn` de
  sus edificios) antes que `city.fallen`: el store del cliente ya tiene los edificios con su ciudad nueva cuando
  `city.fallen` quita los de la caída, sin cambios en `apps/web`.
- **Precisión (2026-11-25): el cliente y los bots, sin código nuevo.** El store aplica la toma con lo que ya sabía
  (`cityFall.test.ts` lo prueba), y el saqueador que toma juega la tomada como la segunda ciudad de E35 (`bot.test.ts`).

## 10. Decisiones de producto (2026-11-25)

Producto delegó en la recomendación del agente (sesión desatendida del 2026-11-25): cada fila deja la decisión (la
recomendación, en negrita) y las alternativas descartadas. Son decisiones de producto, no de ingeniería: cambiarlas es
cambiar la spec, y la persona usuaria puede revisarlas al volver.

| # | Pregunta | Decisión | Descartadas |
|---|---|---|---|
| Q1 | ¿Cuándo se toma y cuándo se arrasa? | **Se toma si el vencedor tiene hueco (menos de dos en pie); si no, se arrasa.** Sin orden nueva: lo decide la transacción. | **Elegir al plantar el estandarte** (otro campo en `unit.assault` y otra rama en el HUD); **arrasar siempre salvo orden expresa** (la toma casi no se vería). |
| Q2 | ¿Qué es la tomada? | **Una ciudad nueva del vencedor en el mismo centro, que hereda los edificios completos**; la caída sigue en su fila con `fallen_at`. El dueño de cada fila no cambia. | **Cambiar el dueño de la fila** (rompe `RN-CITY-020` y la historia de ADR-050); **copiar los edificios con ids nuevos** (pierde su vida y su historia). |
| Q3 | ¿Almacén y encargos? | **Arden, como en E31**: el botín es el saqueo de antes. | **El almacén para el vencedor** (un botín que dobla al saqueo); **la mitad** (otra regla que medir). |
| Q4 | ¿Con qué unidades nace? | **Tres aldeanos nuevos, como una fundación**; el portaestandarte sigue en su ciudad. | **El portaestandarte pasa a la tomada** (deja coja a la capital y la tomada sin aldeanos); **sin unidades** (una ciudad que no recolecta). |
| Q5 | ¿Inmunidad? | **La suya, desde la toma.** | **Sin inmunidad** (el vencido la recupera en el acto: ping-pong); **la que le quedaba a la caída** (ya vencida, por eso cayó). |
| Q6 | ¿Y el vencido? | **Como hoy:** refunda si era la última, sigue con la otra si no. | **Refunda siempre** (con dos ciudades la caída dejaría de costar). |
| Q7 | ¿Qué se avisa? | **Los avisos de hoy con otro texto** («Tomaste…», «… tomó …») y `takenCityId` en `CityFell`. | **Un evento nuevo `CityTaken`** (otro tipo en el registro para lo mismo). |
| Q8 | ¿La muralla y lo que está en obra? | **Lo que está en obra se pierde**; la muralla, caída, sigue caída. | **Heredar las obras** (pagadas por el vencido: regalo raro). |
| Q9 | ¿Y los bots? | **El saqueador se queda la ciudad si tiene hueco y la juega como la segunda de E35**, sin más reglas. | **Que el bot arrase siempre** (no mide la toma). |
