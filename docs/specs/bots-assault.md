# Bots que asaltan

Propuesta para que el saqueador de los bots **entrene arietes, abra la muralla y asalte** ciudades de otros bots, y
para que un bot cuya ciudad cae **siga jugando desde la refundada**, de modo que una ejecución mida cuántas ciudades
caen, cuánto cuesta tumbar una y cuánto tarda el vencido en recuperarse.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-BOTS-ASSAULT` |
| Estado | Accepted (2026-11-23). Propuesta en `Draft` y aceptada el mismo día con la recomendación de cada pregunta de §10. |
| Milestone | E32 Bots que asaltan (trigésimo segundo slice después de M7) |
| Canon | §1.3 (el servidor es la única verdad), §9 (presencia y protección), §19 (niveles de test) |
| Depende de | [bots.md](bots.md), [bots-trade.md](bots-trade.md), [trade-reach.md](trade-reach.md), [city-fall.md](city-fall.md), [siege-engines.md](siege-engines.md), [combat-siege.md](combat-siege.md), [combat-response.md](combat-response.md), [castle-age.md](castle-age.md) |
| Decisiones | [ADR-049](../decisions/ADR-049-bots-as-protocol-clients.md) sigue valiendo. Sin ADR nuevo: no cambia el servidor, ni el protocolo, ni la base. |
| Reemplaza a | La Q10 de [city-fall.md](city-fall.md) («los bots no asaltan en este slice») y, en [bots.md](bots.md) Q3, el «arietes» de lo que los bots no juegan. |
| Invariantes | No asigna invariantes. Las reglas nuevas son `RN-BOT-014` a `RN-BOT-017` (§4.8). Cita [INV-COMBAT-009](../invariants/combat.md#inv-combat-009), [INV-CITY-020](../invariants/city.md#inv-city-020) e [INV-SEC-001](../invariants/security.md#inv-sec-001) (§8) |

## 1. Objetivo

E31 hizo que una ciudad pudiera caer, pero ninguna medida dice si caen demasiadas o ninguna: el humo `--caida` tumba
una ciudad de Bronce sin muralla con un guerrero y el servidor a 20 s de asalto, y la prueba en vivo lo repitió a mano.
Con los números de hoy (asalto de 900 s, inmunidad de 72 h, muralla de 1 500 de vida que se reconstruye en 120 s,
ariete desde Hierro) no sabemos:

1. **Si un atacante con arietes llega a tumbar una ciudad defendida** por sus guardias, su muralla y, en Castillos, su
   castillo; cuánto le cuesta (arietes y guerreros perdidos, tiempo desde que sale de casa).
2. **Si el defensor gana sólo con reconstruir la muralla.** Una muralla completa rompe el asalto (`RN-SIEGE-011`), y se
   levanta en 120 s, mucho menos que los 900 s del plazo. Un constructor con 300 de piedra quizá no cae nunca.
3. **Cuánto le cuesta al vencido volver**: cuánto tarda la ciudad refundada en recuperar su población, su muralla y su
   almacén, y si vuelve a avanzar.

La Q10 de [city-fall.md](city-fall.md) dejó a los bots fuera del asalto porque el saqueador no entrena arietes y los
constructores levantan la muralla. Esta spec enseña a los bots lo que el protocolo ya da (`city.train RAM`,
`unit.demolish` sobre la muralla, `unit.assault`) y mide, sin cambiar ninguna regla del servidor.

### 1.1 Qué falta hoy en los bots (lectura del código)

Rutas desde `apps/bots/src/`:

- **El saqueador no asalta ni entrena arietes.** Su partida saquea y, cuando el almacén de la víctima baja del suelo,
  derriba **casas** (`raidRule` en `decide.ts`); `militaryRule` sólo entrena guerreros y unidades de civilización.
  Contra una ciudad amurallada no puede ni saquear casas: con la muralla en pie sólo el ariete golpea, y sólo a la
  muralla (`RN-SIEGE-004`).
- **El saqueador apenas llega a Hierro.** En la ejecución larga de E29 llegó a Bronce a los 20 min y no pasó de ahí en
  3 h; el ariete pide Hierro (`RN-SIEGE-003`). Comercia sólo por trato (no construye mercado, [bots-trade.md](bots-trade.md)
  Q4).
- **Los constructores levantan la muralla en cuanto pueden** (`bronzeRule`: 200 de piedra y 100 de madera, con 100 de
  piedra de reserva) y la vuelven a levantar si cae, porque la regla sólo mira si hay una en pie.
- **Un bot cuya ciudad cae se queda con el directorio viejo.** `Bot.start` apunta su ciudad en el directorio del
  proceso (`bot.ts`, `directory.set`) una sola vez, al arrancar; tras la caída, los demás bots siguen viendo el
  `cityId` y el centro de la ciudad arrasada. El espejo del cliente sí cambia de ciudad: el `world.snapshot` que manda
  el servidor tras `city.fallen` (ADR-050 §5) lo trae.
- **El registro no sabe de asaltos ni de caídas**: el JSONL de [bots.md](bots.md) §4.5 no tiene sucesos para ellos.

## 2. Scope

**Entra.**

- **En los bots (`apps/bots`), sin cambios en el servidor, el protocolo ni el cliente web:**
  - El saqueador, desde Hierro, entrena arietes y forma una **partida de asalto** (Q1 a Q3, §4.1 a §4.4).
  - La partida se reúne (`RN-BOT-012`), abre la muralla con los arietes si la hay, planta el estandarte con un guerrero
    y escolta hasta la caída; si el asalto se rompe, lo vuelve a plantar mientras pueda (§4.4).
  - El bot vencido sigue jugando desde la ciudad refundada y actualiza su entrada del directorio (§4.6, Q7).
  - Lo que hace el defensor no cambia (Q4): sus guardias responden solos y su regla de la muralla la reconstruye.
  - Registro y resumen: asaltos, caídas, sus causas y la recuperación del vencido (§4.7).
- La prueba en vivo: una pasada corta con plazos acortados y cuentas sembradas, y una ejecución larga a velocidad real
  (§4.7, Q5).

**No entra.**

- Ningún mensaje, campo, tabla, migración ni variable `EO_` nueva. Ningún cambio en el servidor ni en el cliente web.
- Que el constructor asalte o entrene arietes (Q1).
- Una defensa activa nueva del constructor ante un asalto (salir a matar al portaestandarte, entrenar guerreros al
  recibir `CITY_ASSAULTED`): Q4.
- Castillos y unidades de civilización nuevas en el decisor más allá de lo que ya hace.
- Cambiar el plazo del asalto, la inmunidad, el coste del ariete o de la muralla. Si las medidas lo piden, van a otra
  spec con estos datos (Q10).
- Asaltar ciudades de personas sin `--contra-humanos` (`RN-BOT-004`).

## 3. Actores

| Actor | Papel |
|---|---|
| Persona que lo lanza | Lanza `pnpm run bots`, lee el resumen y decide si los números del asalto cambian. |
| Saqueador bot | Desde Hierro entrena arietes, forma la partida de asalto, abre la muralla, asalta y escolta. |
| Constructor bot | Se defiende como hoy (guardias, muralla) y, si cae, sigue jugando desde la ciudad refundada. |
| Game Server | Valida cada orden y decide la caída como para cualquier jugador ([INV-SEC-001](../invariants/security.md#inv-sec-001)). |
| Navegador | Mira un asalto entre bots desde una cuenta humana: la cuenta atrás y la caída. |

## 4. Modelo

### 4.1 Cuándo asalta el saqueador

El saqueador sigue saqueando como en E28 y E29 hasta que se cumplen las tres condiciones de la partida de asalto:

1. Su ciudad está en **Hierro** o después (el ariete, `RN-SIEGE-003`).
2. Tiene en casa, parados y sin carga, `assaultWarriors` militares que derriban (por defecto 4: guerreros o unidades
   de civilización, que también asaltan, `RN-SIEGE-009`) y `assaultRams` arietes (por defecto 2) (Q3). En esta spec,
   «guerrero» es cualquiera de esos militares que no es un ariete.
3. Hay un **blanco asaltable** (§4.2).

Si no, la partida que sale es de saqueo, como hoy. Las dos no van a la vez: una partida por bot (Q2).

Para tener los arietes, `militaryRule` entrena `RAM` en Hierro hasta `assaultRams`, después de los militares que le
falten (hoy prefiere la unidad de civilización al guerrero) y sin pasar del límite de población; con un ariete muerto, entrena el siguiente. El ariete cuesta 150 de madera y 75
de piedra y ocupa 2 de población ([siege-engines.md](siege-engines.md) §4.2): el perfil sube su piedra objetivo en
Hierro para pagarlos (§4.8).

### 4.2 El blanco

Un blanco asaltable es una ciudad de otro bot (o de cualquiera con `--contra-humanos`) que **no** tendría deshabilitado
«Asaltar» en el HUD, salvo por la muralla: el bot usa `assaultBlockReason` de `apps/web/src/state/cityFall.ts` y
descarta las ciudades cuyo motivo no sea el de la muralla («La muralla está en pie: primero hay que abrirla con
arietes») (`RN-BOT-002`). Es decir: Bronce o posterior, fuera de
su inmunidad (`fallImmuneUntilMs`), sin otro asalto, no protegida, sin paz con ella. Lo que el espejo no ve (la paz de
clan, una Safe Zone) lo dice el servidor, y un rechazo enfría el blanco como hoy.

Entre los asaltables, el más cercano (como `chooseTarget`), con preferencia por los que están **en brecha** a igual
distancia. Un blanco amurallado pide al menos un ariete vivo en la partida.

### 4.3 La partida de asalto

1. **Reunión.** Como la de saqueo (`RN-BOT-012`): todos al punto de reunión a `rallyDistance` del blanco. El ariete
   camina a 1 800 ms por tile, el triple que un guerrero: el plazo de reunión de la partida de asalto es
   `assaultRallyTimeoutMs` (por defecto 10 min) y cuenta a los arietes entre los que hay que esperar.
2. **La brecha.** Si la ciudad está amurallada, los arietes reciben `unit.demolish` sobre la muralla y los guerreros
   avanzan al anillo con `unit.move` para escoltarlos: parados, responden solos a quien golpee a un compañero a ≤
   `EO_COMBAT_GUARD_RADIUS_TILES` (`RN-COMBAT-013`, `RN-COMBAT-014`). Dos arietes abren una muralla entera en unos 57 s.
3. **El estandarte.** Con la ciudad en brecha, el guerrero vivo más cercano al anillo recibe `unit.assault`. Los demás
   guerreros se quedan parados en el anillo, de escolta; los arietes, también parados, a la espera de §4.4.
4. **Hasta la caída.** Nadie recibe otra orden mientras el asalto siga en pie, salvo lo de §4.4.

### 4.4 Lo que rompe el asalto, y lo que hace la partida

| Lo que pasa | Lo que ve el bot | Lo que hace |
|---|---|---|
| Muere el portaestandarte | La ciudad sin `assault`, su guerrero `DEAD` | Otro guerrero vivo planta el estandarte (`unit.assault`), si quedan. |
| El defensor empieza a reconstruir la muralla | Un `WALL` de la ciudad en obra | Los arietes (y, si no hay, los guerreros de escolta) la derriban con `unit.demolish`: una ciudad en brecha admite cualquier golpe sobre cualquier edificio (`RN-SIEGE-004`), también uno en obra (`RN-COMBAT-029`). |
| La muralla se completa | La ciudad amurallada, el asalto roto (`RN-SIEGE-011`) | Vuelve a §4.3 paso 2 con los arietes que queden. Sin arietes, la partida vuelve a casa. |
| Llega la paz o la protección del asaltante | `INVALID_TARGET` o `CITY_PROTECTED` al replantar | La partida vuelve; el blanco se enfría como hoy. |
| Quedan menos de `assaultMinWarriors` guerreros (por defecto 2) | — | La partida vuelve. |
| Vence `assaultTimeoutMs` (por defecto 45 min) desde la salida | — | La partida vuelve. |
| La ciudad cae | `city.fallen` | La partida vuelve a casa; el blanco no vuelve a ser asaltable hasta que venza la inmunidad de la refundada (Q8). |

Que el asalto siga en pie lo dice el espejo (`CityView.assault`). El bot no adivina: si la ciudad no lleva asalto y su
portaestandarte vive y está `IDLE`, se rompió.

### 4.5 El defensor

Q4 decide que no cambia nada en el constructor: sus guardias responden solos a los golpes y al asalto (los guardias del
dueño responden como a un golpe en su centro, `RN-SIEGE-011`), y su `bronzeRule` vuelve a encargar la muralla en
cuanto la ciudad queda sin una en pie y le alcanza la piedra. Lo que se mide es precisamente si eso basta.

### 4.6 El bot que cae

Cuando la ciudad de un bot cae, el servidor le manda `city.fallen` y el `world.snapshot` de la refundada sin
reconectar (ADR-050 §5). El bot:

1. Apunta `fell` en su registro (§4.7) con la ciudad caída, la nueva, el vencedor y la era.
2. **Actualiza su entrada del directorio** del proceso con el `cityId` y el centro nuevos, para que los demás bots
   dejen de ver la arrasada y sus tratados sigan apuntando a él (los tratados son del jugador, [city-fall.md](city-fall.md)
   §4.3).
3. **Olvida lo de la ciudad**: obras, encargos y reservas de hueco del plan; conserva lo del jugador (enfriamientos de
   blanco, tratados, parejas sin ruta de `RN-BOT-013`). Una partida suya en marcha sigue: sus unidades pasan a la ciudad
   nueva (`RN-CITY-028`) y vuelven a la casa nueva al acabar.
4. Sigue jugando con su perfil desde la era en que cayó (Q7).

### 4.7 Lo que se mide

Sucesos nuevos en el JSONL de [bots.md](bots.md) §4.5:

| Suceso | Campos |
|---|---|
| `assault` | bot, blanco, `rallied` / `breached` / `planted` / `broken` / `fell` / `abandoned`; en `broken`, la causa (`holder_died`, `wall_completed`, `peace`, `protected`); en `abandoned`, la causa (`timeout`, `no_rams`, `too_few`) |
| `fell` | bot (el vencido), ciudad caída, ciudad nueva y su centro, vencedor, era |
| `wall` | bot (el defensor), `breached` / `rebuilt`, por quién |

El resumen suma: por saqueador, asaltos empezados, ciudades tumbadas, arietes y guerreros perdidos, y el tiempo de la
salida a la caída; por constructor, asaltos sufridos y cuántos rompió y cómo, caídas, y tras cada caída el tiempo hasta
volver a tener muralla, la población de antes y la era siguiente. Y en total: **caídas por hora de ejecución**.

La prueba en vivo tiene tres partes (Q5):

1. **Pasada corta** con `EO_CITY_ASSAULT_SECONDS=120` y `EO_CITY_FALL_IMMUNITY_HOURS=0`, cuentas sembradas a Hierro
   (`EO_DEVSEED_STOCK_FOR`): al menos un asalto con la muralla abierta por arietes, uno roto por la muralla
   reconstruida o por la muerte del portaestandarte, y una caída con el vencido jugando desde la refundada.
2. **Ejecución larga** a velocidad real (asalto de 900 s, inmunidad de 72 h), en segundo plano, con cuentas cuya
   inmunidad ya venció y el saqueador sembrado a Hierro; sus medidas en milestones con la semilla y la duración.
3. **Navegador**: una cuenta humana con un asalto entre bots a la vista (la cuenta atrás en el HUD y la ciudad que
   desaparece).

### 4.8 Reglas y perfil

Continúan las de [bots-trade.md](bots-trade.md) §4.9.

| ID | Regla |
|---|---|
| `RN-BOT-014` | Un bot sólo asalta una ciudad cuyo `assaultBlockReason` es `null` (o, para abrir la brecha, el motivo de la muralla), y sólo derriba la muralla con arietes mientras está completa (extensión de `RN-BOT-002`). |
| `RN-BOT-015` | Un saqueador lleva como mucho una partida, de saqueo o de asalto; la de asalto no sale sin `assaultWarriors` guerreros y, contra una ciudad amurallada, al menos un ariete. |
| `RN-BOT-016` | Mientras la partida no vuelve, un asalto roto por la muerte del portaestandarte se vuelve a plantar con otro guerrero, y una muralla en obra de la ciudad asaltada se derriba antes de que se complete. |
| `RN-BOT-017` | Un bot cuya ciudad cae actualiza su entrada del directorio y sigue jugando desde la refundada con su perfil. |

Campos nuevos del perfil (datos, [bots.md](bots.md) Q8): `assaults` (sí en el saqueador, no en el constructor),
`assaultWarriors` (4), `assaultRams` (2), `assaultMinWarriors` (2), `assaultRallyTimeoutMs` (10 min),
`assaultTimeoutMs` (45 min), y la piedra objetivo del saqueador en Hierro de 200 a 350 (dos arietes y un margen).

## 5. Errores

No hay códigos nuevos. Lo que el bot hace con los que ya existen, además de [bots.md](bots.md) §5 y
[bots-trade.md](bots-trade.md) §5:

| Código | Condición | Qué hace el bot |
|---|---|---|
| `INVALID_TARGET` | Al asaltar: amurallada, inmune, con otro asalto, en caída o en paz ([city-fall.md](city-fall.md) §5); al derribar: la muralla con un guerrero | Enfría el blanco y la partida vuelve. |
| `CITY_PROTECTED` | El asaltado o el asaltante protegidos al ordenar | Igual. |
| `PATH_NOT_FOUND`, `PATH_TOO_LONG` | Ningún tile del anillo alcanzable | Igual que un blanco sin ruta en E28. |
| `UNIT_NOT_MOVABLE` | Un ariete que recibe una orden que no es mover o derribar | Es un fallo del decisor: el test lo impide (§9). |
| `CITY_NOT_FOUND` | La ciudad ya cayó | La partida vuelve. |

## 6. Eventos y contratos de red

Ningún mensaje ni valor de enum nuevo. Los bots envían `city.train` con `RAM`, `unit.demolish` sobre `WALL` y
`unit.assault` ([websocket-protocol.md](websocket-protocol.md) §7), y leen `CityView.assault`, `fallImmuneUntilMs`,
`city.fallen`, el `world.snapshot` que sigue a una caída y los avisos `CITY_ASSAULTED`, `CITY_LOST` y `CITY_TAKEN`
(§8), que el espejo de `apps/web/src/state` ya guarda.

## 7. Persistencia

| Pregunta | Respuesta |
|---|---|
| ¿Qué es autoritativo en RAM del game server? | Lo de siempre: el asalto, la muralla y su daño, por sus specs. |
| ¿Qué se persiste inmediatamente? | Lo de siempre: el asalto (`city_assaults`) y la caída, por [city-fall.md](city-fall.md). |
| ¿Qué se persiste eventualmente? | Lo de siempre. |
| ¿Qué es reconstruible y no se persiste? | En los bots, la partida de asalto y su fase: no sobreviven a un reinicio del proceso, como la de saqueo; al arrancar, los militares lejanos vuelven por la regla de los varados. El asalto plantado sí sobrevive en el servidor y cae aunque el bot no esté. |

## 8. Invariantes

No asigna invariantes. Cita [INV-COMBAT-009](../invariants/combat.md#inv-combat-009) (el ariete no hiere unidades: el
bot no le da órdenes de ataque), [INV-CITY-020](../invariants/city.md#inv-city-020) (una ciudad en pie por jugador: el
bot vencido tiene siempre una) e [INV-SEC-001](../invariants/security.md#inv-sec-001) (el bot juega por el protocolo y
el servidor decide).

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (Vitest, `apps/bots`) | El blanco: descarta los inmunes, protegidos, de Piedra, en paz o con otro asalto, y prefiere el que está en brecha. La partida: no sale sin los guerreros o sin arietes contra una amurallada; se reúne esperando a los arietes; derriba la muralla sólo con arietes; planta el estandarte con el más cercano; replanta al morir el portaestandarte; derriba la muralla en obra; vuelve por cada causa de §4.4. Ningún ariete recibe una orden distinta de mover o derribar. `militaryRule` entrena arietes sólo en Hierro y hasta el tope. |
| Unit (Vitest) | El bot vencido: actualiza el directorio, olvida lo de la ciudad y conserva lo del jugador. El registro y el resumen con los sucesos nuevos; la misma semilla da los mismos comandos (`RN-BOT-005`). |
| Go, simulación | Ninguno nuevo: las reglas del servidor no cambian. Si la implementación necesita confirmar que una muralla **en obra** se derriba con la ciudad en brecha y no rompe el asalto hasta completarse, se añade ese caso a los tests de E27 y E31. |
| Integración / e2e | Ninguno nuevo. |

### 9.1 Precisiones de la implementación (2026-11-23)

Lo que la implementación concreta dentro de las reglas y las decisiones:

- **La partida de asalto es la de saqueo con `kind: 'assault'`.** Comparte la reunión por tramos, el punto de reunión,
  la vuelta a casa (también por tramos en espiral) y la conexión de campaña; los arietes van en `warriors` y en `rams`.
  Al volver, la orden de mover sustituye la tarea de los que derriban o sostienen el estandarte.
- **El blanco.** Cada bot publica en el directorio del proceso, al arrancar y cuando cambian, la era, el fin de la
  inmunidad y si tiene la muralla completa de su ciudad. Una ciudad que no está a la vista se juzga con eso: sin era
  conocida no se elige, y con la muralla desconocida se da por amurallada. Una a la vista, con `assaultBlockReason`.
- **Los arietes** se entrenan cuando los militares ya están completos (`warriorsMax`, con la unidad de civilización
  antes que el guerrero), con `ramBlockReason`. `villagerCount` resta dos plazas por ariete: el ariete no es militar
  (no ataca) y antes contaba como aldeano.
- **La reunión** de una partida de asalto da por suficiente tener reunidos `assaultMinWarriors` militares al vencer
  su plazo.
- **La escolta** va a 3 tiles del centro, en el lado del punto de reunión (`rallyPoint`), y sólo se mueven los
  militares parados a más de 4. **El portaestandarte** es el militar parado más cercano al centro (empate: menor id);
  mientras camina o sostiene (`MOVING`, `CLAIMING`) sin que haya llegado el asalto en `city.update`, no se planta otro.
- **La causa de una rotura** es la que se ve: el portaestandarte muerto (`holder_died`), la ciudad amurallada
  (`wall_completed`), la ciudad propia protegida (`protected`) o, si no, `peace`, que recoge también cualquier otra
  causa que sólo sabe el servidor. Con `peace` o `protected`, otro jugador que ya asalta la ciudad, o un rechazo de
  `unit.assault` o del derribo (`INVALID_TARGET`, `CITY_NOT_FOUND`, `CITY_PROTECTED`, sin ruta), la partida vuelve con
  la causa `blocked` y el blanco se enfría.
- **El defensor** apunta también `assault` con `suffered` (el aviso `CITY_ASSAULTED`) y `repelled` (el asalto de su
  ciudad termina sin caer). `wall` no dice quién: el `building.destroyed` no lo trae.
- **El vencido** apunta `fell` al llegar el snapshot de la refundada, con la ciudad caída, la nueva, el vencedor, la
  era y la población de antes, y vuelve a buscar colinas alrededor de la nueva. **Fallo encontrado y arreglado** (con
  su test en `bot.test.ts`, visto caer sin el arreglo): la búsqueda de colinas pide snapshots, y el bot, que aún no
  había apuntado su ciudad nueva, creía en cada uno que había vuelto a caer, en bucle.
- **El resumen** cuenta las caídas como el mayor de las sufridas y las causadas por los bots de la ejecución, por
  hora de la ejecución más larga.
- **Encontrado en la pasada corta en vivo y arreglado** (cada uno con su test, visto caer sin el arreglo):
  - Los arietes volvían a casa desde el punto de reunión: la vuelta de la partida da por llena la carga de quien ya
    lleva su máximo de botín, y el ariete carga 0. Ahora sólo vuelve «lleno» quien puede cargar algo.
  - Una partida que termina y otra que sale en el mismo despertar no apuntaban `raid_end` ni `raid` (venía de E28), y
    la partida de asalto no salía en el resumen.
  - `breached` se apuntaba también contra una ciudad sin muralla: ahora sólo si la partida la vio amurallada
    (`sawWall`).
  - Faltaba el enfriamiento de la Q8: con la inmunidad a 0, la refundada tiene otro id y el saqueador volvió a por
    ella a los 7 min. Ahora el dueño de la ciudad tumbada no se vuelve a elegir durante el enfriamiento de blanco
    (`victimCooldowns`).
  - Una partida de **saqueo** cuyo blanco cae no volvía: `onTargetFell` sólo miraba la de asalto, y un `unit.pillage`
    rechazado con `CITY_NOT_FOUND` no enfriaba el blanco. Lo destapó un asalto plantado al final de una ejecución, que
    siguió en el servidor y tumbó a los 0,8 min de la siguiente la ciudad que la partida nueva iba a saquear. Ahora
    vuelve, como con un blanco protegido.
  - **Fuera de los bots:** el `unit.move.accepted` con `movementId: 0` (el destino ya era la posición actual,
    [websocket-protocol.md](websocket-protocol.md) §7.4) no pasaba el esquema Zod, que pedía un id de 1 o más, y el
    cliente lo rechazaba con `INVALID_MESSAGE`. Lo destapó el tramo de marcha que se manda a quien ya está en el punto de
    reunión. Se corrige el esquema, que no decía lo mismo que la spec; el servidor no cambia.
  - **Precisión (2026-11-24): la marcha de una partida se repetía sin fin** (venía de E28/E29). En la tercera pasada
    el punto de reunión no se pisaba y el espiral de los rechazos lo desplazó 9 tiles (26 intentos). El último tramo
    sólo se reconocía a 4 tiles o menos del punto de reunión; con la partida ya parada en el desplazado, el tramo
    siguiente salía igual, y el saqueador mandó el mismo `raid:march` a sus tres militares en cada despertar durante
    17 min (753 órdenes). El servidor las aceptaba con `movementId: 0`, y el error de esquema de arriba sólo añadía
    ruido: las posiciones del bot estaban al día, y con el esquema arreglado el bucle seguía. Ahora el último tramo es
    también el punto de reunión desplazado con los intentos de la partida, por lejos que quede (`isRallyLeg`); con él
    corre el plazo de reunión y, reunidos, sale la orden de saquear.
  - **Precisión (2026-11-24): una vuelta que sale de la vista no terminaba.** En la cuarta pasada, la partida volvía
    por tramos desde un blanco a 267 tiles; el primer tramo acabó en 106,247, fuera de la vista de casa y de la del
    blanco (radio de 2 chunks), y el servidor dejó de mandar a los cinco (`entity.despawn OUT_OF_INTEREST`). Sin
    verlos, el bot no les daba el tramo siguiente, y la partida, que termina cuando todos están en casa, siguió abierta
    los 29 min que quedaban. Ahora la partida apunta dónde vio por última vez a cada uno, o adónde iba si caminaba
    (`whereabouts`), y fuera de la marcha la conexión de campaña va a mirar allí si falta alguno (`lookAt`); mientras
    los ve a todos, no se mueve.
  - **Lo que no se arregla en el bot:** los militares que una ejecución anterior dejó fuera de la vista. El protocolo
    no tiene un mensaje con las unidades propias fuera del área de interés, así que el proceso nuevo no sabe que
    existen: cuentan en la población, no se recuperan y el saqueador entrena otros. En este mundo quedan así doce
    militares y los dos arietes de las pasadas anteriores.
- **El tercero** (§4.7, parte 3) sólo ve la cuenta atrás de un asalto ajeno en el HUD si tiene seleccionada una
  unidad suya a 12 tiles o menos (`assaultTargetNear`); si no, ve la ciudad desaparecer. Pintar el asalto en la escena
  es otro slice.

## 10. Decisiones de producto (2026-11-23)

Producto aceptó el 2026-11-23 la recomendación de cada pregunta. Cada fila deja la decisión (en negrita) y las
alternativas que se descartaron. Son decisiones de producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Quién asalta? | **El saqueador, desde Hierro; el constructor no.** Es el perfil hostil, ya forma partidas y nunca firma pactos (`RN-BOT-011`); el constructor es la víctima que se mide. Descartadas: **un perfil nuevo, el asaltante**, que no saquea y sólo asalta (medidas más limpias, pero una cuenta más por ejecución y un tercer camino en el decisor); **también el constructor**, contra quien no tiene pacto (más caídas, pero mezcla quién defiende y quién ataca). |
| Q2 | ¿Saqueo y asalto a la vez? | **No: una partida por bot; desde Hierro, si hay un blanco asaltable y la partida completa, sale la de asalto; si no, la de saqueo.** El saqueo sigue pagando arietes y guerreros. Descartadas: **sólo asalto desde Hierro** (deja al saqueador sin ingresos mientras junta la partida); **dos partidas a la vez** (más presión, y el doble de estado en el decisor). |
| Q3 | ¿Cómo es la partida? | **4 guerreros y 2 arietes; el asalto vuelve con menos de 2 guerreros.** Dos arietes abren la muralla en ≈ 57 s y ocupan 4 de población; cuatro guerreros dan un portaestandarte y tres de escolta contra los 2 guardias del constructor. Descartadas: **3 y 1** (más barata, un ariete tarda 114 s y no aguanta un castillo); **6 y 3** (casi segura, pero el saqueador tardaría horas en juntarla). |
| Q4 | ¿El defensor hace algo nuevo? | **No: guardias y muralla como hoy.** Lo que se mide es si la defensa de hoy basta; si el constructor no cae nunca o cae siempre, la medida lo dice sin ruido. Descartadas: **defensa activa** (los guardias salen a matar al portaestandarte, el constructor entrena guerreros al recibir `CITY_ASSAULTED`), que se parece más a una persona pero cambia dos cosas a la vez; **no reconstruir la muralla durante un asalto**, que regalaría caídas. |
| Q5 | ¿Cómo se mide? | **Una pasada corta (asalto de 120 s, sin inmunidad, cuentas sembradas a Hierro) para ver el ciclo entero, y una ejecución larga de 3 h a velocidad real con cuentas cuya inmunidad ya venció.** La corta prueba las reglas; la larga da las caídas por hora con los números de verdad ([bots.md](bots.md) Q5). Las cuentas `_e29` y `_e30` salen de su inmunidad el 2026-11-25 y el 2026-11-26 (72 h desde su fundación). Como el saqueador `_e29` sigue en Bronce, para la larga **sólo el saqueador se siembra a Hierro** (`EO_DEVSEED_STOCK_FOR`): la medida es la del asalto, no la del camino a Hierro, que ya midió E29. Descartadas: **sólo la corta** (rápida, pero sin medida de equilibrio); **la larga con cuentas nuevas** (72 h de inmunidad: nadie caería en 3 h). |
| Q6 | ¿Contra personas? | **Sólo con `--contra-humanos`, como el saqueo (`RN-BOT-004`).** En el mundo de desarrollo hay cuentas de humo que una ejecución no debe arrasar sin quererlo. Descartada: **nunca**, ni con la opción (impide probar a defenderse de un bot desde el navegador). |
| Q7 | ¿Qué hace el bot que cae? | **Sigue jugando desde la refundada con su perfil, y actualiza el directorio.** Es lo que da la medida de la recuperación y no deja una entrada vieja a la que los demás disparen. Descartadas: **se para y apunta `fell`** (más simple, sin medida de la recuperación); **cambia de perfil** (el constructor caído se vuelve saqueador), una venganza que no mide nada de lo que importa. |
| Q8 | ¿Vuelve a por la misma víctima? | **Sí, cuando vuelva a ser asaltable; la inmunidad de 72 h ya lo impide antes.** Con la inmunidad a 0 de la pasada corta, el enfriamiento de blanco (10 min) evita el bucle. Descartadas: **nunca dos veces a la misma** durante la ejecución (más reparto, pero oculta si una víctima cae en bucle tras la inmunidad); **una por jugador y día**, una regla más en el decisor. |
| Q9 | ¿Qué se apunta? | **Los tres sucesos de §4.7 y, en el resumen, caídas por hora, coste de cada caída (arietes y guerreros perdidos, tiempo) y la recuperación del vencido (tiempo hasta muralla, población y era).** Es lo que responde a las tres preguntas de §1. Descartada: **sólo caídas**, más corto pero sin saber por qué no caen. |
| Q10 | ¿Este slice puede cambiar números del asalto si las medidas lo piden? | **No: se anotan como hallazgos y van a otra spec**, como decidió la Q10 de [bots-trade.md](bots-trade.md). Así esta spec sólo enseña a los bots y se ve qué pasa con los números de hoy. Descartada: **ajustar en el mismo slice** el plazo del asalto o el de la muralla si sale muy desviado, que acorta el ciclo pero mezcla causas. |

## 11. Documentos relacionados

- [bots.md](bots.md), [bots-trade.md](bots-trade.md), [trade-reach.md](trade-reach.md) — los bots, su decisor, el
  comercio y las reglas `RN-BOT` vigentes.
- [city-fall.md](city-fall.md) — el asalto, la caída y la refundación que los bots aprenden a usar.
- [siege-engines.md](siege-engines.md), [combat-siege.md](combat-siege.md) — el ariete, la muralla y el derribo.
- [combat-response.md](combat-response.md) — la defensa automática que escolta al portaestandarte y defiende la ciudad.
