# El asalto en el mapa

Spec para que **la escena dibuje el asalto** de una ciudad: el estandarte sobre la unidad que lo sostiene y la
cuenta atrás sobre la ciudad, para todo el que la tenga a la vista, en vez de que un tercero sólo vea la ciudad
desaparecer.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-ASSAULT-MAP` |
| Estado | Accepted (2026-11-24). Propuesta en `Draft` y aceptada el mismo día con la recomendación de cada pregunta de §10. |
| Milestone | E33 El asalto en el mapa (trigésimo tercer slice después de M7) |
| Canon | §1.3 (el servidor es la única verdad), §13 (protocolo), §19 (niveles de test) |
| Depende de | [city-fall.md](city-fall.md), [territory-conquest.md](territory-conquest.md), [siege-engines.md](siege-engines.md), [bots-assault.md](bots-assault.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | Sin ADR nuevo: no cambia el servidor, ni el protocolo, ni la base; es presentación de un estado que el cliente ya recibe. |
| Reemplaza a | En [city-fall.md](city-fall.md) §9.1, «el cliente no pinta el asalto en la escena», y en [bots-assault.md](bots-assault.md) §9.1, «pintar el asalto en la escena es otro slice». |
| Invariantes | No asigna invariantes: el cliente no decide nada (§8). Las reglas nuevas son `RN-SIEGE-012` a `RN-SIEGE-014` (§4.6). |

## 1. Objetivo

Que un asalto se vea en el mapa como lo que es: un acontecimiento público, con un plazo, que se puede romper. El
servidor ya lo hace público (`CityView.assault` llega a todo el que ve la ciudad); falta dibujarlo.

### 1.1 Qué se ve hoy (lectura del código)

Rutas desde `apps/web/`:

- **La escena** (`src/render/scene.ts`) dibuja cada ciudad como un bloque con su nombre encima (`upsertCity`,
  `paintCityBody`), atenuada si está protegida. No mira `CityView.assault`: una ciudad bajo asalto se dibuja igual que
  cualquier otra.
- **`SceneCity`** (`src/render/sceneData.ts`) lleva id, centro, nombre, presencia y si es propia; `SceneUnit`, entre
  otras cosas, si trabaja o ataca, pero no si sostiene un estandarte (`CLAIMING`).
- **El HUD** (`app/page.tsx`) enseña la cuenta atrás (`assaultLabel`, `src/state/cityFall.ts`) en la barra de la ciudad
  propia y, a quien tiene seleccionada una unidad suya a 12 tiles o menos de la ciudad (`assaultTargetNear`), junto al
  botón «Asaltar». Un tercero sin una unidad cerca no ve nada hasta que la ciudad desaparece con `city.fallen`.
- **La reclamación de territorio** (E12) se ve en el overlay: el territorio en disputa se pinta en naranja
  (`territoryStyle`, `contested`), pero tampoco su portaestandarte lleva marca.
- **Las pasadas en vivo de E32** ([bots-assault.md](bots-assault.md) §9.1) asaltaron y tumbaron ciudades de bots; desde
  una cuenta humana, lo único visible en el mapa habría sido la desaparición.

## 2. Scope

**Entra:**

- El **estandarte** sobre la unidad que lo sostiene (`CLAIMING`), para todo el que la ve.
- La **ciudad bajo asalto**: su contorno marcado y, bajo el nombre, la **cuenta atrás** hasta `endsAtMs`, con la hora
  del servidor, para todo el que la ve.
- El **tono** de las dos marcas según quién mira: la ciudad propia asaltada, el asalto propio, el de otros.
- Lo que pasa **al llegar a cero** antes de que llegue la caída, y **al caer** la ciudad a la vista.
- Los campos que la proyección del store pasa a la escena (`SceneCity`, `SceneUnit`) y sus tests.

**No entra:**

- **Nada en el servidor, el protocolo ni la base.** Si algo de lo que se quiere dibujar no llega hoy al cliente, se
  queda fuera (§10).
- **Ruinas** donde cayó una ciudad: la ciudad caída no viaja en el snapshot (Q5).
- Cambiar **el HUD** o el ofrecimiento de «Asaltar» a 12 tiles (Q6).
- **Sonido**, partículas o animaciones más allá de la cuenta atrás y el desvanecido de Q5.
- **Los bots**: no cambian; sirven para la prueba en vivo (§9).

## 3. Actores

- **Quien mira**: cualquier sesión cuya área de interés cubre la ciudad o la unidad; puede ser el defensor, el
  asaltante o un tercero.
- **El store del cliente** (`src/state/world.ts`): aplica `world.snapshot`, `city.update`, `entity.*` y `city.fallen`
  como hoy.
- **La proyección** (`src/render/projection.ts`): traduce el store a `SceneUpdate`.
- **La escena** (`src/render/scene.ts`, PixiJS 8): dibuja y, por frame, refresca las cuentas atrás.
- **El Game Server**: sin cambios; es la única fuente del asalto, de su plazo y de la caída.

## 4. Modelo

### 4.1 El estandarte

Toda unidad en `CLAIMING` se dibuja con un **estandarte**: un mástil sobre su silueta con un banderín del tono de §4.3.
`CLAIMING` es el estado de quien sostiene un estandarte, sea el de un asalto ([city-fall.md](city-fall.md)) o el de una
reclamación de territorio ([territory-conquest.md](territory-conquest.md)): los dos se ven igual (Q2). El estandarte
desaparece cuando la unidad deja `CLAIMING` (otra orden, la rotura, la caída) o deja de verse.

### 4.2 La ciudad bajo asalto

Una ciudad con `assault` se dibuja **bajo asalto**:

- Su bloque lleva un **contorno** grueso del tono de §4.3.
- Bajo el nombre, una línea con la **cuenta atrás**: «Cae en 14:32», con `formatCountdown(endsAtMs − ahora)` y la hora
  del servidor (`serverTimeMs`), como el HUD. Se redibuja sólo cuando cambia el segundo que muestra.
- **Al llegar a cero** sin que haya llegado `city.fallen` ni un `city.update` con `assault: null` (la latencia, o una
  rotura en el último tick), la línea dice «Cayendo…» y se queda así: el cliente no adelanta la caída (Q8).
- **Sin `assault`** (`city.update { assault: null }`: el asalto se rompió), el contorno y la línea se quitan.
- **Al caer** (`city.fallen`), la ciudad se desvanece en 1 s y se retira (Q5). Sin animación si la escena no la tiene
  en pantalla.

La cuenta atrás se ve **en todo el que tiene la ciudad a la vista**, implicado o no (Q3): el asalto es público.

### 4.3 El tono

Un solo criterio para el banderín y para la ciudad, según quién mira:

| Quién mira | Tono | Por qué |
|---|---|---|
| El dueño de la ciudad asaltada | Rojo (el de `--danger`) | Es una pérdida inminente, como en su barra («¡Están asaltando tu ciudad!»). |
| El que asalta (`assault.playerId` es él) | Dorado (el de lo propio) | Es su asalto. |
| Un tercero | Naranja (el del territorio en disputa) | Es una disputa ajena, como la reclamación de territorio. |

Para el estandarte de una **reclamación de territorio**, el tono sale de la unidad: dorado si es propia, naranja si no;
y rojo si el territorio reclamado es del que mira (Q4). El territorio es el que contiene el tile de la unidad y lleva
una reclamación de su dueño (`TerritoryView.claim.playerId`): quien reclama se planta dentro, y `claim` no trae la
unidad. Si no se ve ese territorio, el tono es el de la unidad.

### 4.4 Lo que pasa a la escena

- **`SceneCity`** añade `assault: { endsAtMs, tone } | null`.
- **`SceneUnit`** añade `standard: tone | null` (`null` si no está en `CLAIMING`).
- La proyección los calcula con funciones puras en `src/state/cityFall.ts` (`assaultTone`, `standardTone`), con test.
- Un `city.update` que trae o quita `assault`, o un `entity.update` que cambia el estado de una unidad, ya marcan la
  entidad como cambiada: la escena recibe sólo eso, como hoy ([frontend.md](../architecture/frontend.md) §7.3).

### 4.5 El frame

- La escena guarda las ciudades con asalto en un conjunto y, por frame, sólo para ellas compara el segundo mostrado
  con el actual; si cambió, reescribe su texto. Ninguna otra ciudad cuesta nada por frame.
- El culling y el orden de dibujado son los de la ciudad y la unidad: las marcas van dentro de su sprite.

### 4.6 Reglas

| ID | Regla |
|---|---|
| `RN-SIEGE-012` | Una ciudad con `CityView.assault` se dibuja bajo asalto para todo el que la ve: contorno y cuenta atrás hasta `endsAtMs` con la hora del servidor; a cero, «Cayendo…» hasta que el servidor diga la caída o la rotura. El cliente no adelanta ni la una ni la otra. |
| `RN-SIEGE-013` | Una unidad en `CLAIMING` se dibuja con un estandarte, sea de un asalto o de una reclamación de territorio. |
| `RN-SIEGE-014` | El tono de las dos marcas depende de quién mira (§4.3): rojo para el que pierde, dorado para lo propio, naranja para un tercero. |

## 5. Errores

Ninguno nuevo: no hay mensajes ni comandos nuevos. El cliente no rechaza nada.

## 6. Eventos y contratos de red

Sin cambios. La escena lee lo que ya llega ([websocket-protocol.md](websocket-protocol.md) §8):
`CityView.assault` (`playerId`, `unitId`, `startedAtMs`, `endsAtMs`) en `world.snapshot` y `city.update`,
`UnitView.status` (`CLAIMING`) en `world.snapshot`, `entity.spawn` y `entity.update`, y `city.fallen`.

## 7. Persistencia

Nada: es presentación. Lo autoritativo es el asalto del servidor (`city_assaults`, [city-fall.md](city-fall.md) §7); lo
que dibuja el cliente se reconstruye de cada snapshot.

## 8. Invariantes

No asigna invariantes: el cliente no decide nada. Sigue valiendo que el asalto, su plazo y la caída los decide el
servidor ([city-fall.md](city-fall.md) §8); esta spec sólo los dibuja.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (Vitest, `apps/web/src/state`) | `assaultTone` y `standardTone` para el defensor, el asaltante y un tercero, y para la reclamación de territorio; el texto de la cuenta atrás y «Cayendo…» a cero. |
| Unit (Vitest, `apps/web/src/render`) | La proyección: `SceneCity.assault` y `SceneUnit.standard` desde el snapshot y desde un `city.update` o `entity.update` que los trae o los quita, sólo con las entidades tocadas. |
| Navegador | Con el panel visible: un asalto entre bots (E32) visto desde una cuenta humana que no es parte (tercero), el estandarte, la cuenta atrás, «Cayendo…» y la ciudad que se desvanece; y, con el humo `--caida`, la ciudad propia asaltada en rojo. Servidor con `EO_CITY_ASSAULT_SECONDS=120` y `EO_CITY_FALL_IMMUNITY_HOURS=0`. |

PixiJS no carga en Node: lo que se pinta sólo se comprueba en el navegador; lo que se calcula, en Vitest.

### 9.1 Precisiones de la implementación (2026-11-24)

- **La cuenta atrás** va justo encima del bloque, donde estaba el nombre, y el nombre sube una línea mientras dura el
  asalto: entre el nombre y el bloque no cabe otra. Sigue leyéndose «bajo el nombre».
- **Los tonos** son el rojo de `--danger` (`0xff6b6b`), un dorado más vivo que el bloque de la ciudad propia
  (`0xffd23f`, para que el contorno se distinga sobre ella) y el naranja del territorio en disputa (`0xe0a030`).
- **El tono del estandarte de una reclamación** sólo es rojo para un territorio del jugador (`ownerType: PLAYER`); el
  de su clan se ve naranja, como el de cualquier otro.
- **Lo que vuelve a proyectarse**: un `city.update` de una ciudad bajo asalto vuelve a proyectar a su portaestandarte,
  y cualquier `territory.update`, a toda unidad en `CLAIMING`: los mensajes del asalto y del estado de la unidad
  pueden llegar en cualquier orden.
- **La caída**: el store marca la ciudad en `WorldChanges.fallenCities` al aplicar `city.fallen`, y la proyección la
  pasa en `SceneUpdate.fallenCities`. Sólo esas se desvanecen: una ciudad que sale de la vista con un snapshot, aunque
  esté bajo asalto, se retira en el acto. El sprite que se desvanece sale del mapa de ciudades, así que el snapshot
  que recibe el vencido justo después no lo toca.
- **Tests**: `el asalto en el mapa` en `src/state/cityFall.test.ts` (los tonos, el estandarte de una reclamación y la
  cuenta atrás con «Cayendo…») y en `src/render/projection.test.ts` (el snapshot, el `city.update` que trae y quita el
  asalto, la ciudad caída frente a la que sale de la vista y el `territory.update`).

## 10. Decisiones de producto (2026-11-24)

Producto aceptó el 2026-11-24 la recomendación de cada pregunta. Cada fila deja la decisión (en negrita) y las
alternativas que se descartaron. Son decisiones de producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Qué se dibuja? | **El estandarte sobre el portaestandarte y, en la ciudad, el contorno y la cuenta atrás bajo el nombre.** Se lee de un vistazo y cuesta poco por frame. Descartadas: **Sólo el estandarte** (no dice cuánto falta); **un arco de progreso alrededor de la ciudad** (más bonito, pero la isometría lo deforma y no dice los minutos). |
| Q2 | ¿El estandarte también en la reclamación de territorio? | **Sí**: es el mismo estado (`CLAIMING`) y la misma idea; separarlos pediría distinguirlos sin datos nuevos. Su cuenta atrás sigue sólo en el HUD. Descartadas: **Sólo en el asalto** (habría que decidir el estandarte por la ciudad cercana, adivinando). |
| Q3 | ¿Quién ve la cuenta atrás en el mapa? | **Todo el que tiene la ciudad a la vista**: el servidor ya la hace pública, y que un tercero pueda acudir o aprovecharlo es juego. Descartadas: **Sólo el defensor y el asaltante** (el tercero vería un estandarte sin plazo). |
| Q4 | ¿Un tono por quién mira? | **Tres tonos (§4.3)**: rojo para el que pierde, dorado para lo propio, naranja para un tercero, los colores que el juego ya usa para eso. Descartadas: **Un solo color para todos** (el defensor no distingue de un vistazo su ciudad de la de otro). |
| Q5 | ¿Qué se ve al caer la ciudad? | **Se desvanece en 1 s** al llegar `city.fallen`, sólo si está en pantalla: dice que cayó, no que se fue la conexión. Descartadas: **Desaparece en el acto** (como hoy); **ruinas en el sitio** (la ciudad caída no viaja en el snapshot: pide servidor, otro slice). |
| Q6 | ¿Cambia el HUD del tercero (la condición de los 12 tiles)? | **No**: con el mapa basta para enterarse, y el botón «Asaltar» sigue ofreciéndose sólo con una unidad cerca. Descartadas: **Enseñar en el HUD todo asalto a la vista** (repite lo que ya dice el mapa). |
| Q7 | ¿Cambian los bots o el humo? | **No**: la prueba en vivo usa los bots de E32 (el saqueador asalta) y el humo `--caida`, tal como están. Descartadas: **Una opción de humo nueva** (no hay nada nuevo en el protocolo que comprobar). |
| Q8 | ¿Qué dice la cuenta atrás a cero sin la caída? | **«Cayendo…»** hasta el mensaje del servidor: el plazo venció, pero la caída la confirma el servidor (una rotura en el último tick la evita). Descartadas: **Esconder la línea** (parece que el asalto se rompió); **quitar la ciudad** (adelanta lo que decide el servidor). |

## 11. Documentos relacionados

- [city-fall.md](city-fall.md): el asalto, su plazo y la caída.
- [territory-conquest.md](territory-conquest.md): la reclamación, el otro `CLAIMING`.
- [bots-assault.md](bots-assault.md): los bots que asaltan, para la prueba en vivo.
- [../architecture/frontend.md](../architecture/frontend.md): la escena (§7) y el store (§4).
