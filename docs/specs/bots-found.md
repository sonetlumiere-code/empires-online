# Bots que fundan

Propuesta para que el constructor de los bots **funde su segunda ciudad** con un aldeano en cuanto pueda pagarla y la
**juegue** (aldeanos, casas, recolección) con las reglas que ya tiene, de modo que una ejecución mida cuándo funda,
cuánto tarda la segunda en crecer y qué pasa cuando cae una de las dos.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-BOTS-FOUND` |
| Estado | Implemented (2026-11-25). Producto delegó en la recomendación del agente (sesión desatendida del 2026-11-25): las nueve preguntas de §10, con su recomendación; en `main` desde la fusión `c915715`, con la CI en verde. |
| Milestone | E35 Bots que fundan (trigésimo quinto slice después de M7) |
| Canon | §1.3 (el servidor es la única verdad), §10 (eras y población), §19 (niveles de test) |
| Depende de | [bots.md](bots.md), [bots-trade.md](bots-trade.md), [bots-assault.md](bots-assault.md), [multi-city.md](multi-city.md), [city-fall.md](city-fall.md) |
| Decisiones | [ADR-049](../decisions/ADR-049-bots-as-protocol-clients.md) sigue valiendo. Sin ADR nuevo: no cambia el servidor, ni el protocolo, ni la base. |
| Reemplaza a | La Q10 de [multi-city.md](multi-city.md) («los bots no fundan») y, en [bots.md](bots.md), `RN-BOT-003` pasa a contar los comandos por ciudad (§4.8). |
| Invariantes | No asigna invariantes. Las reglas nuevas son `RN-BOT-018` a `RN-BOT-021` (§4.8). Cita [INV-CITY-022](../invariants/city.md#inv-city-022) e [INV-SEC-001](../invariants/security.md#inv-sec-001) (§8). |

## 1. Objetivo

E34 dejó que un jugador tenga dos ciudades, pero ninguna medida dice si fundar compensa: el humo `--multiciudad` funda
una vez y para. Con los números de hoy (fundar desde Bronce por 800 de madera y 400 de piedra, la misma cuenta que
pasar a Hierro; la nueva nace con tres aldeanos y el almacén a cero) no sabemos:

1. **Cuándo llega un jugador a fundar**: cuánto tarda desde Bronce en juntar lo que cuesta, y cuánto le retrasa el
   avance a Hierro.
2. **Cuánto tarda la segunda en crecer**: desde tres aldeanos y nada en el almacén, cuánto hasta tener sus aldeanos y
   sus casas.
3. **Qué pasa si cae una de las dos**: la que queda sigue, el bot no se rompe, y vuelve a fundar.

La Q10 de [multi-city.md](multi-city.md) dejó a los bots fuera porque fundar es una estrategia nueva y medirla es su
propio slice. Esta spec enseña al constructor lo que el protocolo ya da (`unit.found_city`) y mide, sin cambiar ninguna
regla del servidor.

### 1.1 Qué falta hoy en los bots (lectura del código)

Rutas desde `apps/bots/src/`:

- **Nadie funda.** `decide.ts` no tiene una regla de fundación; `GameClient.foundCity` existe en
  `apps/web/src/net/client.ts` y el motivo del HUD, `foundCityBlockReason`, en `apps/web/src/state/multiCity.ts`.
- **El bot juega una ciudad.** `View.city` es una sola, la capital (`capitalOf`, E34), y `View.units` lleva todas las
  unidades propias a la vista: con dos ciudades, la capital daría órdenes a los aldeanos de la otra.
- **La vista sigue a la capital.** La conexión «casa» mira la capital; la de «campaña», la partida. El snapshot de la
  fundación llega centrado en la ciudad nueva (`ResnapshotAt`, [multi-city.md](multi-city.md) §9.1) a **todas** las
  sesiones del jugador, y el espejo olvida las ciudades que salen del área de interés (radio de 2 chunks): tras fundar,
  la casa podría quedarse sin la capital.
- **La caída de una de dos** no refunda: el servidor manda `city.fallen` y un `world.snapshot` centrado en la que
  queda (`Resnapshot`). El bot de E32 trata cualquier cambio de id de su capital como una refundación.
- **El sitio.** El servidor comprueba el tile del aldeano (`founding.CheckSite`: el 7 × 7 transitable, a 24 o más de
  todo centro en pie, 10 tiles de bosque con madera a 8 o menos) y el territorio libre fuera de una Safe Zone. El humo
  ya elige un sitio así con lo que ve (`sitioParaFundar` en `scripts/smoke.mjs`).

## 2. Scope

**Entra.**

- **En los bots (`apps/bots`), sin cambios en el servidor, el protocolo ni el cliente web:**
  - El constructor elige un sitio (§4.2), lleva un aldeano de la capital hasta él y funda (§4.1, §4.3), con lo que
    cuesta apartado mientras camina (Q2).
  - La segunda ciudad juega con su propia memoria y el perfil de colonia: aldeanos, casas y madera (§4.4, Q4).
  - Una conexión más, «colonia», con la vista en la segunda ciudad; la casa vuelve a la capital (§4.5, Q5).
  - La caída de una de dos: la que queda sigue, y el bot vuelve a fundar (§4.6, Q8).
  - Registro y resumen: la fundación, el crecimiento de la segunda y la caída de una de dos (§4.7).
- La prueba en vivo: una pasada de 30 a 45 min con cuentas nuevas (Q9).

**No entra.**

- Ningún mensaje, campo, tabla, migración ni variable `EO_` nueva. Ningún cambio en el servidor ni en el cliente web.
- Que el saqueador funde (Q1).
- Que la segunda ciudad avance de era, levante muralla o taller, comercie, investigue o entrene militares (Q4).
- Que otros bots elijan la segunda ciudad como blanco (Q6): el directorio sigue con la capital.
- Más de dos ciudades, conquistarlas o elegir de qué ciudad sale el comercio: son E36 a E38.

## 3. Actores

| Actor | Papel |
|---|---|
| Persona que lo lanza | Lanza `pnpm run bots`, lee el resumen y decide si los números de la fundación cambian. |
| Constructor bot | Funda la segunda ciudad, la juega y, si cae una, sigue con la otra. |
| Saqueador bot | No funda; sigue saqueando y asaltando capitales como en E32. |
| Game Server | Valida la fundación y decide el sitio como para cualquier jugador ([INV-SEC-001](../invariants/security.md#inv-sec-001)). |

## 4. Modelo

### 4.1 Cuándo funda el constructor

El constructor empieza una fundación cuando se cumplen todas (Q1, Q2):

1. Tiene **una** ciudad en pie (el tope es dos, `RN-CITY-031`) y ninguna fundación en curso.
2. Su capital está en **Bronce** o después y su almacén tiene lo que cuesta fundar (`FOUND_COST`, 800 de madera y 400
   de piedra), con los mismos motivos del HUD (`foundCityBlockReason`, `RN-BOT-002`).
3. Tiene un aldeano de la capital vivo, fuera de guarnición y sin carga que perder (parado o recolectando sin carga;
   si no, el que menos lleva), y hay un sitio (§4.2) que no esté apartado.

La regla va antes que el avance de era, el taller, la muralla y la investigación: en Bronce, el constructor funda antes
de pasar a Hierro (Q2). Para juntar la piedra de las dos cosas, su piedra objetivo en Bronce sube de 400 a 800 (§4.8).

### 4.2 El sitio (Q3)

El sitio más cercano a la capital, a entre `foundMinDistance` (28) y `foundMaxDistance` (96, precisión de §9.1) tiles de Chebyshev, que
pase lo que el espejo conoce de las reglas del servidor, como `sitioParaFundar` del humo:

- el 7 × 7 alrededor del centro, conocido y transitable (ni agua ni montaña);
- a 24 o más de todo centro de ciudad que conoce: las de la vista, las del directorio y las propias;
- al menos 10 tiles de bosque a 8 o menos del centro, fuera del 3 × 3, que el espejo no dé por agotados;
- en un territorio libre (`ownerType` `NONE`) y sin reclamación, o fuera de todo territorio conocido.

A igual distancia, el de `y` menor y luego el de `x` menor. Lo que el espejo no ve (el bosque agotado fuera de la
vista, una ciudad fuera del área de interés, una Safe Zone, la ruta) lo dice el servidor: un rechazo aparta el sitio
`foundSiteCooldownMs` (30 min) y la fundación vuelve a empezar con otro (`RN-BOT-021`).

### 4.3 La fundación

1. **Elegir.** El aldeano de §4.1 y el sitio de §4.2. Desde ese despertar, lo que cuesta fundar queda apartado: el
   plan de cada despertar lo resta del almacén antes que ninguna otra regla, y la recolección no reasigna al aldeano
   (`RN-BOT-019`).
2. **Caminar.** `unit.move` al sitio, por tramos de `MARCH_STEP` si queda lejos, desplazados en espiral si el servidor
   rechaza el tramo, como la marcha de una partida. El aldeano que queda parado sin avanzar cuenta como un tramo
   rechazado.
3. **Fundar.** Parado en el sitio (o en un tile a su lado que también lo pase, si la espiral lo dejó ahí),
   `unit.found_city` con el aldeano.
4. **Terminar.** La fundación termina bien cuando el bot tiene dos ciudades propias; termina mal si el servidor la
   rechaza, si el aldeano muere, si suma 9 tramos rechazados o si vence `foundWalkTimeoutMs` (10 min) desde que
   empezó: el sitio se aparta y lo apartado vuelve al plan.

### 4.4 La segunda ciudad (Q4)

La juega el mismo decisor, con **su propia memoria** y el **perfil de colonia**: el del constructor sin partidas,
asalto, avance, taller, elaboración, muralla, investigación, comercio, mercado, guardias ni fundación, con
`colonyVillagers` (8) aldeanos en cualquier era y sin piedra objetivo (todo a la madera). Es decir: aldeanos hasta 8,
una casa cuando la población llega al tope y los parados a la madera. Su vista lleva sólo lo suyo: su ciudad, sus
edificios, sus unidades (las de `cityId` suyo), su bosque.

Cada ciudad tiene su tope de dos comandos por despertar (`RN-BOT-003` con la precisión de §4.8): la capital juega como
hasta ahora y la segunda no le quita órdenes. La capital, a su vez, ya no ve las unidades de la segunda.

### 4.5 Lo que ve el bot (Q5)

- Una conexión «colonia» con la vista en la segunda ciudad, que se abre cuando el bot la ve por primera vez (el
  snapshot de la fundación) y se cierra si cae.
- Tras un snapshot en la casa que no trae la capital (el de la fundación), la casa vuelve a mirar la capital.
- Todas las órdenes salen por la casa, como hoy las de la partida; la conexión colonia sólo mira.
- El bot recuerda los centros de sus ciudades (como `ownCityHomes` del cliente) para saber cuál es la capital aunque el
  espejo de la casa aún no la traiga.

### 4.6 Si cae una de las dos (Q8)

| Cae | Lo que llega | Lo que hace el bot |
|---|---|---|
| La segunda | `city.fallen` de su ciudad y un snapshot centrado en la capital | Apunta `fell` con `kept`, cierra la conexión colonia y olvida la memoria de la segunda. Sigue con la capital y vuelve a fundar cuando §4.1 se cumpla otra vez. |
| La capital | `city.fallen` y un snapshot centrado en la segunda, que pasa a capital (`RN-CITY-033`) | Apunta `fell` con `kept`, cierra la conexión colonia, la casa mira la nueva capital, pone al día el directorio (`RN-BOT-017`) y sigue con el perfil entero desde ella, olvidando lo de la ciudad caída. Las unidades de la caída pasan a la que queda (`RN-CITY-035`). |
| La única | Lo de E32 | Lo de E32: sigue desde la refundada (`RN-BOT-017`). |

### 4.7 Lo que se mide

Sucesos nuevos o ampliados en el JSONL de [bots.md](bots.md) §4.5:

| Suceso | Campos |
|---|---|
| `found` | `status`: `started` (aldeano, sitio, distancia a la capital), `founded` (ciudad nueva, su centro, ms desde `started`), `failed` (causa: `rejected` con el código, `unit_died`, `stuck`, `timeout`) |
| `colony` | Cada minuto, con la segunda en pie: su id, población, límite, madera y casas completas |
| `fell` | Lo de E32 y `kept` (`true` si quedaba otra en pie) y `capital` (`true` si la caída era la capital) |

El resumen suma por constructor: fundaciones empezadas, logradas y fallidas por causa; cuándo fundó la primera (ms
desde el arranque y desde que llegó a Bronce); de cada segunda ciudad, el tiempo hasta 8 aldeanos de población y hasta
su primera casa, y su población al final; y las caídas con `kept`. Por perfil, las fundaciones y la media del tiempo
hasta fundar.

### 4.8 Reglas y perfil

Continúan las de [bots-assault.md](bots-assault.md) §4.8.

| ID | Regla |
|---|---|
| `RN-BOT-018` | Un bot con `founds` y una sola ciudad en pie funda la segunda con un aldeano de su capital en cuanto `foundCityBlockReason` no la deshabilita salvo por el estado del aldeano, en el sitio de §4.2; el saqueador no funda. |
| `RN-BOT-019` | Mientras dura una fundación, lo que cuesta fundar no lo gasta ninguna otra regla y su aldeano no recibe otra orden que las de la fundación. |
| `RN-BOT-020` | La segunda ciudad juega con su propia memoria y el perfil de colonia; la capital no da órdenes a sus unidades. |
| `RN-BOT-021` | Un sitio que el servidor rechaza, o al que el aldeano no llega, no se vuelve a elegir hasta `foundSiteCooldownMs`. |

**Precisión de `RN-BOT-003`:** un bot envía como mucho dos comandos **por ciudad** y despertar.

Campos nuevos del perfil (datos, [bots.md](bots.md) Q8): `founds` (sí en el constructor, no en el saqueador),
`foundMinDistance` (28), `foundMaxDistance` (96, §9.1), `foundWalkTimeoutMs` (10 min), `foundSiteCooldownMs` (30 min),
`colonyVillagers` (8), y la piedra objetivo del constructor en Bronce de 400 a 800.

## 5. Errores

No hay códigos nuevos. Lo que el bot hace con los que ya existen ([multi-city.md](multi-city.md) §5):

| Código | Condición | Qué hace el bot |
|---|---|---|
| `INVALID_TARGET` | Al fundar: el sitio no pasa, ya tiene dos, la capital no está en Bronce o está cayendo | Aparta el sitio y termina la fundación (`failed`, `rejected`). |
| `INSUFFICIENT_RESOURCES` | El almacén no llega (un saqueo mientras caminaba) | Igual. |
| `UNIT_NOT_MOVABLE`, `UNIT_DEAD`, `UNIT_NOT_FOUND` | El aldeano no está parado o murió | Igual; un aldeano que no está parado vuelve a intentarlo en el despertar siguiente sin apartar el sitio. |
| `CITY_BUSY` | Otro encargo de la capital en vuelo | Enfría la orden (`RN-BOT-002`) y la repite después. |
| `TARGET_NOT_WALKABLE`, `PATH_NOT_FOUND`, `PATH_TOO_LONG` | Un tramo de la caminata | Desplaza el tramo en espiral; a los 9, `failed`, `stuck`. |

## 6. Eventos y contratos de red

Ningún mensaje ni valor de enum nuevo. Los bots envían `unit.found_city` y `unit.move`
([websocket-protocol.md](websocket-protocol.md) §7), y leen el `world.snapshot` de la fundación, `city.fallen` y el
`world.snapshot` que sigue a la caída de una de dos, que el espejo de `apps/web/src/state` ya guarda.

## 7. Persistencia

| Pregunta | Respuesta |
|---|---|
| ¿Qué es autoritativo en RAM del game server? | Lo de siempre: la fundación en vuelo y las dos ciudades, por [multi-city.md](multi-city.md). |
| ¿Qué se persiste inmediatamente? | Lo de siempre: la fundación (`city.found`) en su transacción. |
| ¿Qué se persiste eventualmente? | Lo de siempre. |
| ¿Qué es reconstruible y no se persiste? | En los bots, la fundación en curso y la memoria de la segunda: no sobreviven a un reinicio del proceso. Al arrancar con dos ciudades, el bot las juega las dos; un aldeano que caminaba queda parado donde esté y la recolección lo vuelve a usar. |

## 8. Invariantes

No asigna invariantes. Cita [INV-CITY-022](../invariants/city.md#inv-city-022) (como mucho dos ciudades en pie: el bot
no funda con dos) e [INV-SEC-001](../invariants/security.md#inv-sec-001) (el bot juega por el protocolo y el servidor
decide el sitio).

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (Vitest, `apps/bots`) | El sitio: el más cercano en el anillo, descarta el terreno que no se pisa o no se conoce, los centros a menos de 24, el poco bosque y el territorio ajeno o reclamado, y los apartados. La fundación: no empieza con dos ciudades, antes de Bronce, sin lo que cuesta ni siendo saqueador; aparta lo que cuesta del plan (el avance de era no lo gasta); camina por tramos y funda parado en el sitio; termina con dos ciudades, con el rechazo, con la muerte del aldeano, a los 9 tramos y por el plazo. La segunda: juega con el perfil de colonia (aldeanos, casas, madera, nada más), su propio tope de dos comandos, y la capital no da órdenes a sus unidades. |
| Unit (Vitest) | El bot: abre la conexión colonia al ver la segunda y devuelve la casa a la capital; la caída de la segunda y la de la capital con otra en pie (§4.6). El registro y el resumen con los sucesos nuevos; la misma semilla da los mismos comandos (`RN-BOT-005`). |
| Go, simulación / integración / e2e | Ninguno nuevo: las reglas del servidor no cambian. |

### 9.1 Precisiones de la implementación (2026-11-25)

- **Precisión (2026-11-25): el anillo llega a 96 tiles.** §4.2 decía «entre 28 y 56». Lo encontró la prueba en vivo:
  en el mundo denso de desarrollo, dos de los tres constructores no tenían ningún sitio a 56 o menos (el humo de E34
  ya fue a 81) y pasaron a Hierro sin fundar. `foundMaxDistance` es 96; el sitio sigue siendo el más cercano, y lo que
  queda fuera de la vista de la casa lo conoce el espejo por la exploración de las colinas. Cada búsqueda sin sitio se
  apunta como `found` con `status: "no_site"` y el resumen la cuenta entre las fallidas.
- **Precisión (2026-11-25): la casa de la segunda.** En Bronce el límite de la era es 50 y la segunda llega a sus 8
  aldeanos sin casa: «primera casa» sale «no» en el resumen hasta que el perfil de colonia pida más aldeanos que el
  límite. No es un fallo: la regla de la casa de siempre sólo construye con la población al tope.
- **Precisión (2026-11-25): al arrancar con dos.** Un bot que arranca con dos ciudades juega la segunda si la ve la
  casa o la exploración; si no la ve, la regla de fundación lo intentará y el servidor la rechazará por el tope
  (`INVALID_TARGET`), y el sitio se aparta. En la prueba en vivo, las tres segundas estaban a la vista.
- **Precisión (2026-11-25): la vuelta de la casa.** Si el snapshot que sigue a la vuelta tampoco trae la capital, la
  casa no vuelve a pedirla hasta el siguiente snapshot que la traiga (`recentering`): un bucle de `session.view` no
  tiene salida si la capital acaba de caer.

## 10. Decisiones de producto (2026-11-25)

Producto delegó en la recomendación del agente (sesión desatendida del 2026-11-25): cada fila deja la decisión (la
recomendación, en negrita) y las alternativas descartadas. Son decisiones de producto, no de ingeniería: cambiarlas es
cambiar la spec, y la persona usuaria puede revisarlas al volver.

| # | Pregunta | Decisión | Descartadas |
|---|---|---|---|
| Q1 | ¿Qué perfil funda? | **Sólo el constructor.** El saqueador gasta la piedra en arietes y su ciudad es la base de sus partidas; que funde es otra estrategia que medir. | **Los dos** (el saqueador con dos ciudades cambia lo medido en E32); **una opción de la línea de órdenes** (otra rama sin medida que la pida). |
| Q2 | ¿Cuándo funda? | **En cuanto la capital está en Bronce y tiene los 800/400, antes de pasar a Hierro**, con la piedra objetivo de Bronce a 800 y lo que cuesta apartado mientras camina. Es el umbral del servidor y mide pronto. | **Desde Hierro** (retrasa la medida y la prueba en vivo); **cuando pueda pagar fundar y el avance a la vez** (1 600/800: en Bronce tarda horas). |
| Q3 | ¿Dónde? | **El sitio libre más cercano a la capital entre 28 y 56 tiles**, con las reglas que el espejo conoce (como el humo); lo demás lo dice el servidor y el sitio rechazado se aparta. | **Lejos de las ciudades de otros bots** (más seguro, pero lejos de su bosque y de su vista); **junto a las colinas** (la segunda no recoge piedra, Q4). |
| Q4 | ¿Cómo juega la segunda? | **Aldeanos (hasta 8), casas y madera, con su propia memoria y su tope de comandos**; nada de era, muralla, taller, comercio ni militares. Lo mínimo que crece y se mide. | **El perfil entero** (dos comercios, dos murallas: dobla el decisor y la prueba); **sólo aldeanos** (sin casas, se queda en el límite de población). |
| Q5 | ¿Cómo ve la segunda? | **Una conexión más, con la vista en ella**, y la casa de vuelta en la capital. | **Una conexión que alterna la vista** (lo que no mira, lo olvida el espejo); **sólo sitios a la vista de la capital** (en un mundo denso no hay). |
| Q6 | ¿La segunda es blanco de otros bots? | **No: el directorio sigue con la capital.** El saqueador asalta capitales como en E32; si cae una capital con otra en pie, ya se mide la caída de una de dos. | **Las dos en el directorio** (el saqueador iría a la nueva, sin muralla: mide otra cosa). |
| Q7 | ¿Qué se mide? | **La fundación (empezada, lograda, fallida y por qué, y cuándo), el crecimiento de la segunda (hasta 8 de población y su primera casa) y las caídas con `kept`.** | **Sólo la fundación** (no dice si la segunda sirve); **la economía de las dos sumada** (mezcla lo de la capital). |
| Q8 | ¿Si pierde una, vuelve a fundar? | **Sí**, en cuanto vuelva a cumplir §4.1: la segunda vuelve a tener sentido y se mide cuánto tarda. | **Una sola fundación por ejecución** (no mide la recuperación). |
| Q9 | ¿Cómo es la prueba en vivo? | **Una pasada de 30 a 45 min con 3 constructores y 1 saqueador nuevos (`--sufijo e35`, `--semilla 35`), sembrados en Piedra con `EO_DEVSEED_STOCK_FOR`, con el asalto a 120 s y la inmunidad a 0** para que pueda caer una de dos. | **A velocidad real** (en 45 min no cae ninguna); **sin saqueador** (no mide la caída). |

## 11. Documentos relacionados

- [multi-city.md](multi-city.md): la fundación, la capital y la caída de una de dos.
- [bots.md](bots.md), [bots-trade.md](bots-trade.md), [bots-assault.md](bots-assault.md): el decisor, el perfil y el
  registro que esta spec amplía.
- [../architecture/frontend.md](../architecture/frontend.md): los bots y lo que importan de `src/net` y `src/state`.
