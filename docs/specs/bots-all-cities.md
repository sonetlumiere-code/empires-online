# Los bots juegan todas sus ciudades

Propuesta para que un bot **juegue cada ciudad propia en pie**, no sólo la capital y una segunda: que el constructor
funde hasta el tope de su era, que la ciudad tomada por el saqueador no se quede parada, y que los aldeanos varados
lejos de casa vuelvan aunque queden fuera de la exploración.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-BOTS-ALL-CITIES` |
| Estado | Implemented (2026-11-27). La persona usuaria aceptó las ocho recomendaciones de §10; en `main` desde la fusión `1f9ecd6`, con la CI en verde. |
| Milestone | E41 Los bots juegan todas sus ciudades (cuadragésimo primer slice después de M7) |
| Canon | §1.3 (el servidor es la única verdad), §10 (eras y población), §19 (niveles de test) |
| Depende de | [bots.md](bots.md), [bots-found.md](bots-found.md), [bots-assault.md](bots-assault.md), [more-cities.md](more-cities.md), [city-taken.md](city-taken.md), [multi-city.md](multi-city.md) |
| Decisiones | [ADR-049](../decisions/ADR-049-bots-as-protocol-clients.md) sigue valiendo. Sin ADR nuevo: no cambia el servidor, ni el protocolo, ni la base. |
| Precisa | La Q5 de [more-cities.md](more-cities.md) («los bots no pasan de dos»); en [bots-found.md](bots-found.md), `RN-BOT-018` (fundar «la segunda») y `RN-BOT-020` (la capital no da órdenes a las unidades «de la segunda»), y la precisión de §9.1 «al arrancar con dos». |
| Invariantes | No asigna invariantes. Reglas nuevas: `RN-BOT-022` a `RN-BOT-025` (§4.8). Cita [INV-CITY-022](../invariants/city.md#inv-city-022) e [INV-SEC-001](../invariants/security.md#inv-sec-001) (§8). |

## 1. Objetivo

E37 hizo que el tope de ciudades en pie dependa de la era de la capital (1 a 4), pero los bots se quedaron en dos
(Q5 de [more-cities.md](more-cities.md)). La ejecución larga de E32 (2026-11-25, [milestones.md](../roadmap/milestones.md)
§E32) midió lo que eso cuesta:

- **La tercera ciudad no la juega nadie.** El saqueador tomó la 55 (de `_1_e29`, en Bronce) y la 57 (de `_3_e29`, en
  Castillos). Con la capital en Castillos el tope era 4, así que el servidor le dio las dos. El bot juega la 55 como
  colonia (de 3 a 8 aldeanos en 2 min, 12 886 de madera al final); la 57 se quedó 3 h con sus 3 aldeanos parados,
  porque el bot sólo abre una conexión de colonia.
- **Quedan aldeanos varados fuera del alcance de la exploración.** Los 12 de `_3_e29` siguen en (120, 445), donde
  estaba su capital antes de caer; la refundada está en (68, 104), a unos 340 tiles, y la exploración no llega
  ([milestones.md](../roadmap/milestones.md) §E35, el repaso de la fundación). Cuentan en su población.
- **La segunda ciudad no levanta casas en Bronce** ([bots-found.md](bots-found.md) §9.1): el resumen dice «primera
  casa: no» de todas, y esa medida no dice nada.

Esta spec deja que el bot juegue todas sus ciudades con lo que el protocolo ya da, y mide:

1. **Cuánto rinde una ciudad más**: si la tercera y la cuarta crecen como la segunda, y cuánta madera suman.
2. **Cuándo llega un constructor a la tercera y a la cuarta**, y cuánto le retrasa el avance a Castillos.
3. **Si vuelven los varados**, cuántos y cuánto tardan.

### 1.1 Qué hace hoy el código (lectura del código)

Rutas desde `apps/bots/src/`:

- **Una sola colonia.** `Bot.secondCityId()` (`bot.ts`) devuelve la ciudad propia de id menor que no es la capital, y
  todo lo de la colonia es singular: `colony`, `colonyCityId`, `colonyMemory`, `colonyForestTiles`, `colonyLast`. Una
  tercera ciudad no tiene conexión, memoria ni decisor.
- **La capital ve las unidades de la tercera.** `buildView` quita de `View.units` sólo las de `secondCityId()`: las de
  una tercera que entren en la vista de la casa recibirían órdenes de la capital (`RN-BOT-020` sólo cubre la segunda).
  En la ejecución larga no pasó porque la 57 quedaba fuera de la vista.
- **La fundación para en dos.** `foundRule` (`decide.ts`) compara con `MAX_OWN_CITIES = 2`, aunque el motivo del HUD
  que importa (`foundCityBlockReason`, `apps/web/src/state/multiCity.ts`) ya sabe el tope de la era (E37).
- **Las ciudades propias, sólo las vistas.** `Bot.homes` se llena con las ciudades propias que traen los espejos. Desde
  el repaso del 2026-11-26, el `world.snapshot` trae `ownCities` (id, centro y era de todas las propias en pie, hasta
  4) y el estado del cliente lo guarda en `ownCityHomes`; el bot no lo lee. Por eso un bot que arranca con una ciudad
  fuera de la vista no la juega ([bots-found.md](bots-found.md) §9.1, «al arrancar con dos»).
- **Los varados.** `noteStrays` sólo mira durante la exploración del arranque (`scoutHills`): anillos de 96, 160 y 224
  tiles alrededor de la capital con una vista de 5 × 5 chunks (160 tiles), que además paran antes si ya hay colinas
  cerca (`r - 64`). Los varados van a la memoria de la capital y `strayRule` los trae a su centro en 20 min como mucho
  (`STRAY_WATCH_MS`). Un aldeano de la colonia nunca vuelve: la vista de la capital no lleva sus unidades, así que se
  le olvida al vencer el plazo.
- **El perfil de colonia** (`colonyProfile`, `profiles.ts`) pide `colonyVillagers` (8) aldeanos en cualquier era. El
  límite de población de una ciudad sin casas es el de su era (20, 50, 100 y 150, `RN-CITY-014`): con 8 aldeanos
  ninguna colonia llega a su tope, ni una tomada en Piedra, y nunca hace falta una casa.
- **Las conexiones.** Hoy, como mucho tres por bot: casa, campaña (partida, fundación o varado) y colonia. El servidor
  acepta 32 sockets por IP (`EO_WS_MAX_CONNECTIONS_PER_IP`, [configuration.md](../operations/configuration.md)) y un
  proceso admite 20 bots (`MAX_BOTS`, `options.ts`): con más de diez bots ya se puede pasar del tope.
- **El tope de comandos** es de dos por ciudad y despertar ([bots-found.md](bots-found.md) §4.8, precisión de
  `RN-BOT-003`); todas las órdenes salen por la casa, cuyo límite es de 20 mensajes por segundo
  (`EO_WS_RATE_LIMIT_PER_SECOND`).

## 2. Scope

**Entra.**

- **En los bots (`apps/bots`), sin cambios en el servidor, el protocolo ni el cliente web:**
  - Cada ciudad propia en pie juega: la capital con el perfil entero, las demás con el perfil de colonia y su propia
    memoria (§4.1, Q1).
  - El constructor funda hasta el tope de la era de su capital, una fundación a la vez (§4.2, Q2).
  - Una conexión de colonia por ciudad que no es la capital (§4.3, Q3).
  - El tope de comandos con varias colonias (§4.4, Q4).
  - El barrido del mapa cuando faltan unidades y la vuelta de cada varado a su ciudad (§4.6, Q5).
  - Lo que juega una colonia y la medida de sus casas (§4.5, Q6).
  - Registro y resumen (§4.7, Q7).
- La prueba en vivo (Q8).

**No entra.**

- Ningún mensaje, campo, tabla, migración ni variable `EO_` nueva. Ningún cambio en el servidor ni en el cliente web.
- Que el saqueador funde ([bots-found.md](bots-found.md) Q1 sigue valiendo).
- Que una colonia avance de era, levante muralla o taller, comercie (E38 lo permite; los bots no), investigue o entrene
  militares.
- Que otros bots elijan una colonia como blanco: el directorio sigue con la capital ([bots-found.md](bots-found.md)
  Q6).
- Guardar el estado de un bot entre ejecuciones: lo que deja una ejecución lo encuentra la siguiente (§4.6, §7).

## 3. Actores

| Actor | Papel |
|---|---|
| Persona que lo lanza | Lanza `pnpm run bots`, lee el resumen y decide si los números de las ciudades de más cambian. |
| Constructor bot | Funda hasta el tope de su era y juega todas sus ciudades. |
| Saqueador bot | No funda; juega como colonias las ciudades que toma (E36). |
| Game Server | Valida cada orden y el tope de ciudades como para cualquier jugador ([INV-SEC-001](../invariants/security.md#inv-sec-001)). |

## 4. Modelo

### 4.1 Las ciudades del bot (Q1)

El bot conoce sus ciudades por `ownCityHomes` (el `ownCities` del último snapshot) y por las que traen sus espejos. La
**capital** es la de id menor (`RN-CITY-033`) y juega el perfil entero, como hoy. **Cada una de las demás** juega con
el perfil de colonia, su propia memoria y su propia vista ([bots-found.md](bots-found.md) §4.4), en orden de id: da
igual que se haya fundado, tomado o que ya estuviera al arrancar. Como mucho son cuatro, el tope del servidor.

Una ciudad sólo da órdenes a sus unidades (las de `cityId` suyo): la vista de la capital lleva sólo las de la capital,
y la de cada colonia, sólo las suyas.

Una ciudad entra al aparecer en `ownCities` (o en un espejo) y sale con su `city.fallen`; la caída de la capital con
otras en pie pasa la capital a la siguiente por id ([bots-found.md](bots-found.md) §4.6, ahora con cualquier número de
ciudades).

### 4.2 Fundar hasta el tope (Q2)

El constructor funda **la siguiente ciudad** con las mismas condiciones de [bots-found.md](bots-found.md) §4.1, salvo
la primera: en lugar de «una ciudad en pie», **menos ciudades en pie que el tope de la era de su capital** (2 en
Bronce, 3 en Hierro, 4 en Castillos; `maxOwnCities` del cliente). Sigue habiendo **una fundación a la vez**, siempre
desde la capital, con un aldeano de la capital, lo que cuesta apartado y la regla antes del avance de era.

- El sitio es el de [bots-found.md](bots-found.md) §4.2: entre 28 y 96 tiles de la capital (**precisión (2026-11-27,
  [bots-found-site.md](bots-found-site.md), E42):** de cualquier ciudad propia en pie) y a 24 o más de todo centro
  conocido, incluidos los de todas las ciudades propias.
- La piedra objetivo del constructor en Hierro y Castillos sigue en 800: llega para fundar (400) y deja margen.
- En Bronce no cambia nada: funda la segunda antes de pasar a Hierro, como hoy. En Hierro, con dos ciudades, la
  tercera va antes que el avance a Castillos; en Castillos, la cuarta.

### 4.3 Las conexiones (Q3)

- **Una conexión «colonia» por cada ciudad que no es la capital**, con la vista en su centro. Se abre al conocer la
  ciudad (`ownCities`), así que se abre también para una ciudad fuera de la vista de la casa, y se cierra cuando cae
  o pasa a capital.
- Las conexiones de colonia sólo miran; todas las órdenes salen por la casa, como hoy.
- Por bot, como mucho cinco: casa, campaña y tres colonias. Con los cuatro bots de las ejecuciones habituales son 20,
  por debajo de los 32 por IP del servidor.
- Al arrancar, el proceso avisa una vez si `5 × bots` pasa de 32 y nombra la variable del servidor que hay que
  subir. Una conexión que el servidor rechaza por el tope (`429`) reconecta con la espera de siempre; mientras tanto
  esa ciudad no juega, y el resumen lo cuenta (§4.7).

### 4.4 El tope de comandos (Q4)

Sigue siendo **dos por ciudad y despertar** (`RN-BOT-003` con la precisión de E35): la capital decide primero y
después cada colonia por id, y ninguna le quita órdenes a otra. Con cuatro ciudades son ocho comandos cada
`EO_BOT_THINK_MS` (2 s): 4 mensajes por segundo por la casa, frente al límite de 20.

### 4.5 Lo que juega una colonia (Q6)

Cada colonia juega el perfil de colonia de E35: aldeanos hasta `colonyVillagers` (8), una casa cuando la población
llega al tope y los parados a la madera. Lo que cambia es la medida: como ninguna colonia llega a su tope con 8 aldeanos, el
resumen deja de dar «primera casa» y da el tiempo hasta `colonyVillagers` aldeanos y la madera
por hora de cada colonia.

### 4.6 Los varados lejanos (Q5)

1. **Qué falta.** Tras la exploración del arranque, cada ciudad propia compara su población con sus unidades a la
   vista de alguna conexión (y la que está entrenando). Si faltan unidades, están fuera de la vista.
2. **El barrido.** Si a alguna ciudad le faltan, la casa sigue moviendo la vista por el resto del mapa, en cuadros de
   160 tiles (16 en el mapa de 512 × 512 de desarrollo), del más cercano a la capital al más lejano, y para cuando ya
   no falta ninguna o ha mirado el mapa entero. Como la exploración, el decisor no corre mientras barre.
3. **La vuelta.** Cada aldeano parado a más de 32 tiles de **su** ciudad se apunta en la memoria de esa ciudad (no en
   la de la capital) y vuelve a ella por tramos de 48 en espiral, como hoy. El plazo crece con la distancia: 20 min
   por cada 160 tiles, y al menos 20 min. La campaña mira al primero de la capital; la conexión de cada colonia, al
   primero de la suya cuando no queda nada que mirar en casa.
4. **Los militares** que el barrido encuentre parados lejos, fuera de toda partida, vuelven igual a su ciudad (el
   hallazgo de E32: doce militares y dos arietes fuera de la vista).

### 4.7 Lo que se mide (Q7)

Sucesos nuevos o ampliados en el JSONL de [bots.md](bots.md) §4.5:

| Suceso | Campos |
|---|---|
| `found` | Lo de E35, y `ordinal` (2, 3 o 4: la ciudad que funda) y `era` de la capital al empezar |
| `colony` | Lo de E35 por cada colonia en pie, cada minuto, y `origin` (`founded`, `taken` o `start`: ya estaba al arrancar) |
| `cities` | Cada minuto: cuántas ciudades propias en pie hay, cuántas juegan (con conexión y vista) y el tope de la era de la capital |
| `sweep` | Al terminar el barrido: cuántas unidades faltaban, cuántos cuadros miró y cuántas encontró |
| `stray` | Lo de E35 (`seen`), y `home` (llegó a 32 tiles de su ciudad, con los ms desde `seen`) y `lost` (con la causa: `timeout`, `stuck`, `died`); con `cityId` y `sweep` (`true` si lo trajo el barrido) |

El resumen suma por bot: ciudades al final y máximo en pie; **minutos con alguna ciudad en pie sin jugar** (el número
que esta spec quiere a cero); por colonia, de dónde vino, el tiempo hasta `colonyVillagers` aldeanos y la madera por
hora; las fundaciones por ordinal, con el tiempo desde que la capital llegó a la era que la permite; los varados
vistos, de vuelta y perdidos, y cuántos trajo el barrido. Por perfil, las ciudades medias y la madera de las colonias.

### 4.8 Reglas

Continúan las de [bots-found.md](bots-found.md) §4.8.

| ID | Regla |
|---|---|
| `RN-BOT-022` | Un bot juega cada ciudad propia en pie que conoce: la capital con su perfil, cada una de las demás con su propia memoria y el perfil de colonia. Ninguna ciudad da órdenes a las unidades de otra. |
| `RN-BOT-023` | Un bot con `founds` funda mientras tenga menos ciudades en pie que el tope de la era de su capital, una fundación a la vez y siempre desde la capital. |
| `RN-BOT-024` | Un bot abre una conexión que sólo mira por cada ciudad propia que no es la capital, y la cierra cuando la ciudad cae o pasa a capital. |
| `RN-BOT-025` | Si a una ciudad propia le faltan unidades tras la exploración, el bot barre el mapa hasta encontrarlas o haberlo mirado entero, y cada unidad parada lejos de su ciudad vuelve a ella. |

**Precisión de `RN-BOT-018`:** «funda la segunda» pasa a «funda la siguiente», con `RN-BOT-023`. **Precisión de
`RN-BOT-020`:** la regla vale para todas las colonias (`RN-BOT-022`). `RN-BOT-003` sigue con la precisión de E35: dos
comandos por ciudad y despertar.

Sin campos de perfil nuevos salvo los del plazo de la vuelta (`strayWatchMs`, 20 min, y `strayWatchTiles`, 160), que
pasan de constantes del decisor al perfil.

## 5. Errores

No hay códigos nuevos. Lo que el bot hace con los que ya existen:

| Código | Condición | Qué hace el bot |
|---|---|---|
| `INVALID_TARGET` | Al fundar: el tope de la era ya está lleno (el bot contaba una ciudad de menos) o el sitio no pasa | Lo de E35: aparta el sitio y termina la fundación (`failed`, `rejected`); el siguiente `ownCities` corrige la cuenta. |
| `UNIT_NOT_MOVABLE`, `UNIT_DEAD`, `UNIT_NOT_FOUND` | Un varado que ya no está parado o murió | Se le olvida (`stray` con `lost`). |
| `TARGET_NOT_WALKABLE`, `PATH_NOT_FOUND`, `PATH_TOO_LONG` | Un tramo de la vuelta | Desplaza el tramo en espiral; a los 9, `lost` con `stuck`. |
| `429` en el upgrade (no es un código del protocolo) | Una conexión de colonia por encima de `EO_WS_MAX_CONNECTIONS_PER_IP` | Reconecta con la espera del cliente; la ciudad no juega mientras tanto (§4.3). |

## 6. Eventos y contratos de red

Ningún mensaje ni valor de enum nuevo. Los bots leen `ownCities` del `world.snapshot` (que el espejo de
`apps/web/src/state` ya guarda en `ownCityHomes`) y envían lo de siempre ([websocket-protocol.md](websocket-protocol.md)
§7).

## 7. Persistencia

| Pregunta | Respuesta |
|---|---|
| ¿Qué es autoritativo en RAM del game server? | Lo de siempre: las ciudades, sus unidades y el tope, por [more-cities.md](more-cities.md). |
| ¿Qué se persiste inmediatamente? | Lo de siempre. |
| ¿Qué se persiste eventualmente? | Lo de siempre. |
| ¿Qué es reconstruible y no se persiste? | En los bots, la memoria de cada ciudad y los varados: no sobreviven a un reinicio del proceso. Al arrancar, el bot juega todas las ciudades de `ownCities` y el barrido encuentra lo que dejó fuera de la vista una ejecución anterior. |

## 8. Invariantes

No asigna invariantes. Cita [INV-CITY-022](../invariants/city.md#inv-city-022) (el tope de ciudades en pie por la era
de la capital: el bot no funda por encima) e [INV-SEC-001](../invariants/security.md#inv-sec-001) (el bot juega por el
protocolo y el servidor valida cada orden).

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (Vitest, `apps/bots`) | La fundación: funda la tercera en Hierro y la cuarta en Castillos, no con el tope lleno ni con otra fundación en curso, siempre desde la capital. La vuelta: el varado va a su propia ciudad, el plazo crece con la distancia, `home` y `lost` por cada causa; el militar fuera de partida vuelve y el de la partida no. Qué falta: población menos unidades vistas y en producción. |
| Unit (Vitest, `bot.test.ts`) | Con tres y cuatro ciudades: una conexión por colonia, abierta también para la que está fuera de la vista (`ownCities`), cerrada al caer; cada colonia con su memoria, su vista y sus dos comandos; la capital no ve las unidades de ninguna colonia; las claves de enfriamiento no se mezclan entre colonias. La caída de la capital con dos o tres más en pie. El barrido: empieza sólo si faltan unidades, va del cuadro más cercano al más lejano y para al no faltar ninguna. El aviso de las conexiones al arrancar. |
| Unit (Vitest, `log.test.ts`) | Los sucesos nuevos y ampliados, y el resumen: minutos sin jugar, colonias por origen, fundaciones por ordinal, varados. La misma semilla da los mismos comandos (`RN-BOT-005`). |
| Go, simulación / integración / e2e | Ninguno nuevo: las reglas del servidor no cambian. |
| Unit (Vitest, `options.test.ts`) | **Precisión (2026-11-27):** el aviso de las conexiones (`linkWarning`): nada hasta seis bots, y con siete, las 35 conexiones y la variable del servidor. |

Los tests están en `apps/bots/src/all-cities.test.ts` (el decisor), `bot.test.ts` (las colonias, las caídas y el
barrido, con un servidor falso que sólo manda las unidades del área de interés), `log.test.ts` y `options.test.ts`.

### 9.1 Precisiones de la implementación (2026-11-27)

- **Precisión (2026-11-27): quién mira a los varados de una colonia.** §4.6 decía que la conexión de cada colonia
  mira a su primer varado «cuando no queda nada que mirar en casa». Mover esa vista deja la colonia sin ver sus
  unidades ni su almacén mientras tanto, así que la conexión de colonia mira siempre su centro y es la de **campaña**
  la que va al primer varado de una colonia cuando la capital no tiene partida, fundación ni varado propio.
- **Precisión (2026-11-27): `lost` con `busy`.** Además de `timeout`, `stuck` y `died`, un varado se olvida con la
  causa `busy` si se le ve con otra orden (trabajando, en guarnición) o si una partida lo toma: ya no es un varado.
- **Precisión (2026-11-27): qué falta.** La cuenta de §4.6 resta lo que ocupa cada unidad en la población (el ariete,
  dos) y lo que está entrenando. Las unidades que se ven en la exploración o el barrido quedan contadas aunque salgan
  luego de la vista (`Bot.seenUnits`). Una unidad cuya ciudad no se conoce cuenta para la capital.
- **Precisión (2026-11-27): los militares varados del saqueador.** La vuelta los trae a 32 tiles de casa, y la llamada
  a casa de la partida (`raid:recall`) no los ordena mientras son varados, para que no reciban dos órdenes.
- **Precisión (2026-11-27): «juega».** En `cities`, una colonia juega si su conexión está lista y su espejo trae su
  ciudad; la capital cuenta siempre. `stray` con `seen` lleva también la distancia a su ciudad (`distance`), y
  `sweep`, lo que sigue faltando (`left`).
- **Precisión (2026-11-27): la prueba en vivo sin sembrar** (Q8). En la base de desarrollo, `_2_e29` y `_3_e29` tienen
  ya la capital en Castillos con dos ciudades (tope 4) y almacén de sobra, y el saqueador tiene sus tres (24, 55 y
  57): no hace falta sembrar ningún constructor para que funde la tercera. `_3_e29` tiene 16 aldeanos y 2 guerreros a
  más de 300 tiles de su capital; el saqueador, 12 militares fuera de casa, y `_1_e29`, 2 guerreros.
- **Precisión (2026-11-27): el barrido no olvida la capital** (lo encontró la pasada 1 de la prueba en vivo). Al
  mover la vista de la casa, la capital sale del área de interés y el espejo la olvida; la cuenta de lo que falta la
  saltaba y el barrido del saqueador paró a las 2 vistas con 4 de población sin encontrar (el de `_3_e29` no lo notó:
  su colonia, al lado, la ve). El barrido guarda las ciudades como se veían antes de moverse y cuenta con ellas. Lo
  reproduce el test del barrido con un servidor falso que también olvida las ciudades fuera del área.
- **Precisión (2026-11-27): los territorios de todas las conexiones** (misma pasada). Fundar la tercera junto a la
  segunda falló una vez tras otra con `INVALID_TARGET` («sólo se funda en un territorio libre»): el sitio, a 24 tiles
  de la colonia 60 de `_2_e29`, caía en su territorio (Región 1-7), que la vista de la capital no trae, y el decisor
  sólo leía los territorios de la casa. Cada rechazo apartaba un tile y la siguiente fundación iba al de al lado. Ahora
  la vista lleva los territorios de todas las conexiones (la de campaña sigue al fundador; la de la colonia ve el
  suyo). Es un fallo de E35 que no se veía con dos ciudades lejos una de otra.
- **Precisión (2026-11-27): la piedra de la siguiente ciudad, apartada antes de empezar** (rama
  `claude/repaso-bots-piedra`, lo que dejó la prueba en vivo). Con la capital en Castillos, `_3_e29` se quedó con 106
  de piedra: rehízo la muralla tras cada asalto abandonado (200 cada vez, tres en 45 min) y la investigación se llevaba
  el resto, así que nunca tenía los 400 cuando la fundación buscaba sitio. Lo apartado de `RN-BOT-019` sólo valía con
  la fundación en curso, y para empezarla hace falta tenerlo. Ahora, mientras falte una ciudad hasta el tope (desde
  Bronce, como la fundación), el plan aparta la piedra de fundar (400, o lo que haya) antes que ninguna otra regla: es
  lo que decidió la Q2 (la piedra no se la llevan la investigación ni la muralla). Si la última búsqueda no encontró
  sitio, no se aparta nada hasta que otra lo encuentre (`foundNoSite`): sin sitio no tiene sentido tener la piedra
  parada. La piedra objetivo sigue en 800 y cuenta, como durante la fundación, lo que queda tras apartarla; la muralla
  se rehace sólo con 300 de piedra por encima de los 400. Lo cubre `all-cities.test.ts`.
- **Precisión (2026-11-27): el anillo, a otra spec.** En la pasada 2 ni `_2_e29` ni `_3_e29` encontraron sitio en el
  anillo de 28 a 96 de su capital. En `_2_e29`, con la capital a 15 tiles del borde del mapa, ningún tile del anillo
  cae en un territorio libre. Buscarlo también alrededor de las colonias cambia dónde funda (la Q3 de
  [bots-found.md](bots-found.md)): es una decisión de producto, en [bots-found-site.md](bots-found-site.md) (E42, aceptada el mismo día con sus tres
  recomendaciones).

## 10. Decisiones de producto (2026-11-27)

La persona usuaria aceptó las ocho recomendaciones (2026-11-27): cada fila deja la decisión, en negrita, y las
alternativas descartadas. Cambiarlas es cambiar la spec.

| # | Pregunta | Decisión | Descartadas |
|---|---|---|---|
| Q1 | ¿Cuántas ciudades juega un bot? | **Todas las propias en pie**, hasta las cuatro del servidor: la capital con su perfil y las demás como colonias. Es lo que juega una persona, y una ciudad parada sólo estropea la medida. | **Hasta un tope del bot** (p. ej. tres: menos conexiones, pero la cuarta se queda como la 57); **sólo la capital y la segunda, y las demás apuntadas** (lo de hoy, con la medida de las paradas). |
| Q2 | ¿Funda el constructor más de una segunda ciudad? | **Sí, hasta el tope de la era de su capital, una a la vez y antes del avance de era.** Mide cuándo llega a la tercera y a la cuarta y cuánto le retrasa Castillos. | **Sólo la segunda, como hoy** (las demás sólo por toma: el constructor no toma, así que nunca pasa de dos); **hasta el tope, pero después del avance** (no retrasa Castillos; en Hierro la tercera llega tarde o nunca, porque la piedra se la llevan la investigación y la muralla, [milestones.md](../roadmap/milestones.md) §E35). |
| Q3 | ¿Cuántas conexiones abre? | **Una por ciudad que no es la capital, más la casa y la campaña: hasta cinco por bot**, y un aviso al arrancar si el proceso puede pasar de los 32 por IP. Con cuatro bots son 20. | **Una sola de colonia que alterna la vista entre ellas** (lo que no mira, el espejo lo olvida: unidades, edificios y bosque; E35 Q5 ya la descartó); **ninguna: las colonias se juegan desde la casa** (la casa sólo mira un sitio). |
| Q4 | ¿Cómo se reparte el tope de comandos? | **Dos por ciudad y despertar, como hoy**: la capital primero y las colonias por id. Ocho cada 2 s con cuatro ciudades, lejos del límite del servidor. | **Dos por bot, repartidos entre todas** (la capital deja sin órdenes a las colonias, o al revés); **dos para la capital y uno por colonia** (las colonias crecen más despacio y se mide otra cosa). |
| Q5 | ¿Qué hace con los varados lejanos? | **Barre el mapa cuando a una ciudad le faltan unidades tras la exploración, y cada varado (aldeano o militar fuera de partida) vuelve a su ciudad con un plazo que crece con la distancia.** Recupera los 12 de `_3_e29` y los militares de E32 sin guardar nada entre ejecuciones. | **Fundar con ellos donde están, si hay hueco** (aprovecha el viaje, pero cuesta 800/400 y el sitio rara vez pasa); **barrer siempre, falten o no** (16 vistas más en cada arranque); **dejarlos** (cuentan en la población y el bot entrena menos). |
| Q6 | ¿Qué juegan las colonias y qué pasa con sus casas? | **El perfil de colonia de E35 para todas (8 aldeanos, casas al tope, madera), y la casa sale de la medida**: con 8 aldeanos no hace falta en ninguna era; se mide el tiempo hasta 8 y la madera por hora. | **Más aldeanos en las colonias según la era** (12 y 16, como la capital: más madera, y sigue sin hacer falta casa); **colonias que también recogen piedra o avanzan** (otra estrategia que medir, otro slice). |
| Q7 | ¿Qué se mide? | **Las ciudades en pie y las que juegan cada minuto (los minutos sin jugar, a cero), cada colonia por su origen, las fundaciones por ordinal con el tiempo desde la era, el barrido y los varados vistos, de vuelta y perdidos.** | **Sólo que no quede ninguna sin jugar** (no dice si la ciudad de más rinde); **la economía de todas sumada** (mezcla la capital con las colonias). |
| Q8 | ¿Cómo es la prueba en vivo? | **Una pasada de 45 min a velocidad real con las cuentas `_e29`** (el saqueador ya tiene tres ciudades y `_3_e29` sus 12 varados) **y un constructor sembrado a Hierro con `EO_DEVSEED_STOCK_FOR`** para que funde la tercera. | **La ejecución larga de 3 h otra vez** (mide la cuarta y las caídas de varias, pero cuesta una tarde); **cuentas nuevas** (sin varados ni ciudades tomadas: no mide lo de §1). |

## 11. Documentos relacionados

- [bots-found.md](bots-found.md): la fundación, la colonia y la vuelta de los varados que esta spec amplía.
- [more-cities.md](more-cities.md): el tope de ciudades por era y su Q5.
- [city-taken.md](city-taken.md): la ciudad tomada que el saqueador ahora juega.
- [bots.md](bots.md), [bots-assault.md](bots-assault.md): el decisor, el perfil, las conexiones y el registro.
- [../architecture/frontend.md](../architecture/frontend.md): los bots y lo que importan de `src/net` y `src/state`.
