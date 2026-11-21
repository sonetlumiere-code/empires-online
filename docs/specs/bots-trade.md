# Bots que comercian y la defensa del saqueo

Slice para que los bots de E28 **firmen tratados, cierren tratos y usen el mercado**, de modo que una partida sin
sembrar pueda llegar a Hierro y a los Castillos y se mida cuánto tarda, y para **corregir el equilibrio de la defensa
automática de E3 frente al saqueo** con los datos de esas partidas.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-BOTS-TRADE` |
| Estado | Accepted (2026-11-21). Propuesta en `Draft` el 2026-11-20 y aceptada el 2026-11-21 con la recomendación de cada pregunta de §10. |
| Milestone | E29 Bots que comercian (vigesimonoveno slice después de M7) |
| Canon | §1.3 (el servidor es la única verdad), §9 (presencia y protección), §19 (niveles de test) |
| Depende de | [bots.md](bots.md), [diplomacy.md](diplomacy.md), [trade.md](trade.md), [caravans.md](caravans.md), [markets.md](markets.md), [civilization-goods.md](civilization-goods.md), [castle-age.md](castle-age.md), [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md), [combat-siege.md](combat-siege.md) |
| Decisiones | [ADR-049](../decisions/ADR-049-bots-as-protocol-clients.md) sigue valiendo para los bots. Sin ADR nuevo: el ajuste de la defensa (Q7) cambia cuándo responde un guerrero, no la estructura de la fase 4 ([ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md)) ni el débito del saqueo ([ADR-026](../decisions/ADR-026-pillage-two-phase-stock-debit.md)); va como reglas nuevas de [combat-response.md](combat-response.md) y precisiones de sus vecinas (§4.6). |
| Reemplaza a | — |
| Invariantes | No asigna invariantes de dominio. Las reglas nuevas son `RN-COMBAT-039` y `RN-COMBAT-040` ([combat-response.md](combat-response.md) §4.3) y `RN-BOT-008` a `RN-BOT-012` (§4.9). Cita [INV-COMBAT-002](../invariants/combat.md#inv-combat-002) y la familia `INV-TRADE` (§8) |

## 1. Objetivo

La prueba en vivo de E28 dejó dos hallazgos que impiden saber si el juego se juega entero:

1. **Sin comercio no se pasa de Bronce.** El avance a Hierro pide 60 bienes ajenos y el de Castillos 120
   ([civilization-goods.md](civilization-goods.md), [castle-age.md](castle-age.md)), y un bien ajeno sólo llega por un
   trato, una compra en el mercado o una caravana capturada. Los bots de E28 no comercian, así que sólo llegaron a
   Hierro con los almacenes sembrados por `dev:seed`. No sabemos cuánto tarda de verdad el camino a Hierro y a
   Castillos, ni si el mercado, con su 10 % de comisión, compensa frente al trato directo.
2. **El saqueo no compensa.** Una partida de 3 guerreros perdió entera contra 2 guardias parados, y en la pasada larga
   el saqueador perdió 15 guerreros por 10 muertos del otro lado. Es la observación de balance que E3 dejó abierta
   ([combat-pillage.md](combat-pillage.md) Q5, [combat-siege.md](combat-siege.md) Q7).

Esta spec resuelve los dos con lo que ya hay: el primero sin tocar el servidor, enseñando a los bots el
comercio que el protocolo ya da; el segundo con un cambio pequeño de una regla del servidor, medido antes y después con
las mismas partidas.

### 1.1 Por qué pierde el saqueador (lectura del código)

No es un problema de números (vida 60, daño 8 cada 1,5 s para los dos lados), sino de quién pelea:

- **El saqueador no devuelve los golpes.** Mientras saquea tiene un enfrentamiento abierto contra la ciudad, y la
  respuesta automática sólo despierta a guerreros **disponibles** (`RN-COMBAT-013`: parados o volviendo a su puesto).
  Recibir golpes tampoco termina el saqueo (`RN-COMBAT-025`). Sus compañeros, que también saquean, tampoco responden.
- **Los guardias rematan en cadena.** El primer golpe de saqueo despierta a los dos guardias contra el mismo
  saqueador (`RN-COMBAT-027`); al morir, vuelven a su puesto, quedan disponibles y el golpe de saqueo siguiente los
  lanza contra el siguiente. Dos guardias quitan 16 por descarga: matan a un saqueador en unos 4,5 s sin recibir daño.
- **La partida del bot llega escalonada.** El decisor manda como mucho dos órdenes por despertar (`RN-BOT-003`) y cada
  guerrero calcula su ruta: los tres `unit.pillage` salen en dos despertares y llegan al anillo en momentos
  distintos. El que vuelve cargado camina con una orden del jugador y tampoco responde.

Lo mismo vale para el asedio de casas (`RN-COMBAT-036`, `RN-COMBAT-037`): el asediador tampoco está disponible.

## 2. Scope

**Entra.**

- **En los bots (`apps/bots`), sin cambios en el servidor ni en el protocolo:**
  - Tratados: proponer, aceptar y rechazar `treaty.*` entre bots (Q2).
  - Tratos: proponer y aceptar `trade.*` entre bots con tratado, con una tabla de precios en el fichero de perfiles (Q5).
  - Mercado: construir el `MARKET` en Bronce, publicar, comprar y retirar con `market.*`, y consultar el tablón con
    `market.list` (Q4).
  - La partida de saqueo se reúne antes de saquear (§4.5).
  - Registro y resumen: los sucesos nuevos y el tiempo hasta Hierro y Castillos sin sembrar (§4.7).
- **En el servidor:** el ajuste de la defensa de Q7: un guerrero que saquea o asedia responde a los golpes como uno
  disponible. Con su precisión en [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md) y
  [combat-siege.md](combat-siege.md), y sus tests de simulación.
- La prueba en vivo: una pasada corta con plazos acortados, las medidas antes y después del ajuste con la misma semilla
  y una ejecución larga sin sembrar (§4.8).

**No entra.**

- Ningún mensaje, campo, tabla ni migración nueva. Ningún cambio en el cliente web.
- Alianzas, guarnición en ciudad aliada, rupturas de tratado, clanes, facciones, mensajes entre jugadores y conquista
  de territorios en los bots.
- Captura de caravanas por el saqueador (Q6).
- Bots en producción y una marca de bot en el juego (siguen las decisiones Q1 y Q2 de [bots.md](bots.md)).
- Cambiar los costes de las eras, la comisión del mercado o los números del combate. Si las medidas lo piden, irán en
  otra spec con estos datos.

## 3. Actores

| Actor | Papel |
|---|---|
| Persona que lo lanza | Lanza `pnpm run bots` con las opciones nuevas, lee el resumen y decide el equilibrio. |
| Proceso de bots | Cada bot suma a su decisor las reglas de comercio; la partida del saqueador se reúne antes de atacar. |
| Game Server | Valida tratados, tratos y mercado como para cualquier jugador. Con Q7, aplica la regla nueva de la respuesta automática. |
| Navegador | Mira a los bots comerciar: caravanas en camino y el panel del mercado de una cuenta humana. |

## 4. Modelo

### 4.1 Tratados entre bots

Cada bot, al terminar de explorar, mira la lista de ciudades de bots del directorio del proceso (la de E28) y:

1. Propone `treaty.propose { type: TRADE }` a cada bot con el que no tenga relación, uno por despertar como mucho.
2. Acepta las ofertas `TRADE` de otros bots y, si es constructor, las de `NON_AGGRESSION` de otros constructores (Q2).
3. Un constructor propone `NON_AGGRESSION` a los constructores con los que ya tiene `TRADE` (sube un escalón de la
   escalera de [diplomacy.md](diplomacy.md)).
4. **El saqueador nunca propone ni acepta `NON_AGGRESSION` ni `ALLIANCE`**: le cerraría blancos (`RN-COMBAT-020`).
   Rechaza las que le lleguen.
5. Una oferta de alguien que no es bot se deja sin responder (caduca a las 24 h), salvo con `--con-humanos` (Q3).

Lo que se puede proponer se decide con `proposableTypes` y `treatyForbidden` de `apps/web/src/state`, los mismos que
deshabilitan los botones del panel de diplomacia (`RN-BOT-002`). Tras un rechazo, el servidor impide reproponer en
10 min (`EO_TREATY_RETRY_MINUTES`); el bot usa ese plazo como enfriamiento.

### 4.2 Tratos entre bots

Con un tratado `ACTIVE`, un bot que necesita bienes ajenos para su siguiente avance y tiene bien propio de sobra
propone a un bot con tratado un trato de **su bien por el bien del otro** al precio de la tabla (Q5). Reglas:

- Una oferta pendiente por pareja (lo impone el servidor) y como mucho una oferta nueva por despertar.
- **Qué pedir:** el bien ajeno que más le falte para la siguiente meta, en lotes de `tradeLot` (por defecto 10).
- **Qué ofrecer:** su bien propio, que sólo le sirve para comerciar y para la unidad de civilización
  ([civilization-goods.md](civilization-goods.md) Q8). Con madera o piedra de sobra respecto a su meta, también puede
  ofrecerlas por la que le falta (Q5).
- **Aceptar:** un bot acepta un trato si recibe al menos lo que su tabla de precios dice que vale lo que da y si lo
  que da no le deja por debajo de su meta. Si no, lo rechaza.
- Lo deshabilitado se decide con `canTradeWith`, `pendingWith` y `offerBlockReason` de `apps/web/src/state/trade.ts`.
  La ruta entre depósitos no la comprueba el cliente: un `PATH_NOT_FOUND` o `PATH_TOO_LONG` aparta a esa pareja
  durante el enfriamiento de blanco (10 min), como la piedra sin ruta de E28.

Los bienes llegan en caravana (RN-CARAVAN-001). El bot no espera a la caravana para seguir decidiendo: cuenta lo que
ya tiene en el almacén, no lo que viene.

### 4.3 Mercado

Desde Bronce, cuando tenga la madera y la piedra de `MARKET_COST` sin bajar de su meta, el constructor construye el
mercado (`marketBuildBlockReason`). El saqueador no lo construye (Q4). Con el mercado completo:

- **Publicar:** si le sobra bien propio y no tiene tratos pendientes que lo usen, publica lotes de su bien a cambio del
  bien ajeno que más le falte, al precio de la tabla, sin pasar de `MARKET_MAX_OPEN` ofertas abiertas
  (`postBlockReason`).
- **Comprar:** cada `marketScanMs` (por defecto 60 s) pide `market.list { receive: <bien que le falta> }` y compra la
  oferta más barata que pueda pagar dentro del alcance (`buyBlockReason`), si su precio no pasa del de la tabla más la
  comisión.
- **Retirar:** una oferta suya abierta más de `marketStaleMs` (por defecto 30 min) se retira y se vuelve a publicar.

Así el mercado cubre a los bots sin tratado entre sí (Q2 deja al saqueador sin pacto) y a los que no tienen ruta
directa, y se mide si la comisión pesa frente al trato.

### 4.4 Precios

La tabla vive en el fichero de perfiles, como los demás números del decisor ([bots.md](bots.md) Q8):

| Cambio | Precio de partida |
|---|---|
| Bien propio ↔ bien ajeno | 1 por 1 |
| Madera ↔ piedra | 2 de madera por 1 de piedra |
| Bien ↔ madera o piedra | No se ofrece en este slice |

### 4.5 La partida se reúne antes de saquear

Hoy la partida del saqueador sale en cuanto hay 3 guerreros parados y cada uno recibe su `unit.pillage` cuando le toca.
Con esta spec la partida:

1. Elige el blanco como en E28 y un **punto de reunión** a `rallyDistance` tiles (por defecto 16) del blanco, en el
   lado de su casa y transitable.
2. Manda a todos sus guerreros al punto con `unit.move`.
3. Cuando todos están parados a ≤ 2 tiles del punto (o han pasado `rallyTimeoutMs`, por defecto 3 min, y queda al
   menos `raidMinParty`), ordena `unit.pillage` a todos en despertares seguidos, sin intercalar otras órdenes.

Así la medida del equilibrio no depende de que la partida llegue en fila por cómo decide el bot. Con o sin el ajuste
de Q7, la comparación es la misma partida reunida.

### 4.6 El ajuste de la defensa (servidor)

Decidido en Q7 y Q8. Dos reglas nuevas en [combat-response.md](combat-response.md) §4.3, `RN-COMBAT-039` y
`RN-COMBAT-040`, con sus precisiones en `RN-COMBAT-013` y en las reglas del saqueo y del asedio que dicen cuándo
terminan y quién defiende ([combat-pillage.md](combat-pillage.md) `RN-COMBAT-025` y `RN-COMBAT-027`,
[combat-siege.md](combat-siege.md) `RN-COMBAT-036` y `RN-COMBAT-037`):

- **Un guerrero que saquea o asedia también está disponible para responder.** A efectos de la respuesta automática
  (`RN-COMBAT-013`), un guerrero con un saqueo (`unit.pillage`, [combat-pillage.md](combat-pillage.md)) o un asedio (`unit.demolish`, [combat-siege.md](combat-siege.md)) en
  curso cuenta como disponible. Se dispara igual que hoy: un golpe sobre él o sobre una unidad de su dueño a ≤
  `EO_COMBAT_GUARD_RADIUS_TILES` (`RN-COMBAT-014`), así que el golpe a un saqueador despierta también a sus
  compañeros que saquean al lado. Una orden de movimiento, de ataque o de recolección sigue sin sustituirse.
- **Al responder, deja de saquear y no vuelve solo.** La respuesta termina el saqueo o el asedio, el **puesto** es el
  tile donde saqueaba y, al acabar el enfrentamiento, vuelve a él y queda `IDLE`, como cualquier respuesta. No reanuda
  el saqueo: lo vuelve a ordenar el jugador (coherente con [combat-pillage.md](combat-pillage.md) Q6, que no quiere
  una granja automática).

Lo que concreta la aceptación, dentro de esas reglas:

- **Desde la orden.** Cuenta el saqueo o el asedio abierto, también mientras el guerrero camina hacia el anillo o
  hacia la casa: el enfrentamiento de saqueo o de asedio nace con la orden. Su puesto es entonces el tile en que
  estaba al responder.
- **Sólo quien ataca.** El ariete asedia pero no hiere a unidades (`CanAttack` falso, [INV-COMBAT-009](../invariants/combat.md#inv-combat-009)):
  no responde, como ya dice `RN-COMBAT-012`, y sigue derribando.
- **Lo demás no cambia.** El saqueador que vuelve a casa cargado camina con una orden de movimiento y sigue sin
  responder; la madera que lleva sigue siendo su carga (`RN-COMBAT-026`); el daño de una casa asediada sigue su plazo
  de `RN-COMBAT-032`.

La decisión Q3 de E3 («una orden del jugador en curso nunca se sustituye por una respuesta») cambia sólo para el saqueo
y el asedio. Lo que se espera, por cálculo con los números de hoy y a comprobar con un golden de simulación:

| Encuentro (todos a la vez, sin muralla ni castillo) | Hoy | Con el ajuste |
|---|---|---|
| 3 saqueadores contra 2 guardias | Ganan los guardias sin bajas | Ganan los saqueadores con una baja |
| 2 saqueadores contra 2 guardias | Ganan los guardias sin bajas | Ganan los guardias, que golpean primero (el contacto es suyo), con un guardia herido |
| 1 saqueador contra 1 guardia | Gana el guardia sin daño | Gana el guardia por un golpe |

Es decir: con el ajuste, la defensa gana a igualdad de fuerzas por golpear primero y por la tierra afín y el castillo
cuando los hay; el atacante gana si trae más. Hoy el defensor gana siempre que tenga dos guardias.

**Precisión (2026-11-21).** Los goldens de `raider_response_test.go` (los saqueadores ya en el anillo norte y los
guardias parados detrás, todos de 60) dan los ticks exactos y corrigen dos casillas de la tabla:

| Encuentro | Hoy (medido con la regla desactivada) | Con el ajuste |
|---|---|---|
| 3 contra 2 | Ganan los guardias con una baja: un saqueador que llena la carga (4 golpes) queda `IDLE` y entonces sí responde | Ganan los saqueadores con una baja: caen 201 a los 3 200 ms, 100 a los 6 100 y 202 a los 9 200 |
| 2 contra 2 | Ganan los guardias sin bajas (uno a 28) | Ganan los guardias **con una baja**: caen 100 a los 4 600 ms, 201 a los 6 200 y 101 a los 13 800; el otro guardia queda a 20 |
| 1 contra 1 | Gana el guardia, a 28: el saqueador sólo responde al llenar la carga | Gana el guardia por un golpe (a 4), a los 10 600 ms |

La conclusión no cambia: hoy el saqueador sólo se defiende cuando deja de saquear, y con el ajuste la defensa gana a
igualdad de fuerzas y pierde contra más.

La medida en vivo (§4.8) repite la misma partida reunida con la misma semilla antes y después del ajuste.

### 4.7 Lo que se mide

Sucesos nuevos en el JSONL de [bots.md](bots.md) §4.5:

| Suceso | Campos |
|---|---|
| `treaty` | bot, con quién, tipo, `proposed` / `accepted` / `rejected` / `expired` |
| `trade` | bot, con quién, da, recibe, `proposed` / `accepted` / `rejected` |
| `market` | bot, `posted` / `bought` / `sold` / `expired` / `cancelled`, da, recibe, lotes, comisión |
| `caravan` | bot, `arrived` / `captured` / `lost`, carga, de dónde |
| `rally` | bot, partida, guerreros reunidos, ms de espera |

El resumen suma por bot: tiempo hasta Hierro y Castillos **sin siembra** (si la ejecución empezó sin `dev:seed`),
bienes ajenos obtenidos por trato y por mercado, comisión pagada, caravanas perdidas, y para el saqueador el balance
de cada partida (guerreros perdidos y muertos, madera robada).

### 4.8 Opciones nuevas y prueba en vivo

| Opción | Por defecto | Qué hace |
|---|---|---|
| `--sin-comercio` | apagado | Desactiva tratados, tratos y mercado: la ejecución de E28, para comparar. |
| `--con-humanos` | apagado | Los bots aceptan tratados `TRADE` y tratos de jugadores que no son bots, al precio de la tabla. No hace hostil a nadie (eso sigue siendo `--contra-humanos`). |

La prueba en vivo tiene cuatro partes:

1. **Pasada corta** con los plazos acortados por entorno: al menos un tratado aceptado, un trato cerrado con sus
   caravanas llegadas, un mercado construido y una compra.
2. **Defensa antes y después:** una partida reunida de 3 saqueadores contra un constructor con 2 guardias, con la misma
   semilla, en la rama sin el cambio del servidor y con él.
3. **Ejecución larga sin sembrar** (Q9), en segundo plano, con cuentas nuevas y los plazos reales: tiempo hasta Hierro
   y Castillos. Se anota en milestones como medida, con la semilla y la duración.
4. **Navegador** con el panel visible: una caravana entre bots en el mapa y el tablón del mercado de una cuenta humana
   con ofertas de bots.

### 4.9 Reglas

Continúan las de [bots.md](bots.md) §4.7.

| ID | Regla |
|---|---|
| `RN-BOT-008` | Sin `--con-humanos`, un bot sólo propone o acepta tratados y tratos con jugadores cuyo nombre empieza por `bot_`. Las compras del mercado pueden ser a cualquiera, porque el mercado es anónimo para quien compra ([markets.md](markets.md)). |
| `RN-BOT-009` | Un bot nunca propone un trato, publica, compra ni propone un tratado que el HUD tendría deshabilitado con el mismo espejo (extensión de `RN-BOT-002` a los paneles de diplomacia, comercio y mercado). |
| `RN-BOT-010` | Un bot acepta un trato sólo si lo que recibe vale, según su tabla de precios, al menos lo que da, y si lo que da no le deja por debajo de su siguiente meta. |
| `RN-BOT-011` | Un saqueador nunca propone ni acepta `NON_AGGRESSION` ni `ALLIANCE`. |
| `RN-BOT-012` | Una partida de saqueo no ordena `unit.pillage` hasta estar reunida (o vencido `rallyTimeoutMs` con al menos `raidMinParty` guerreros), y entonces lo ordena a todos sin intercalar otras órdenes. |

## 5. Errores

No hay códigos nuevos. Lo que el bot hace con los que ya existen, además de [bots.md](bots.md) §5:

| Código | Condición | Qué hace el bot |
|---|---|---|
| `TREATY_REQUIRED` | Trato sin tratado activo (RN-TRADE-001) | Enfriamiento de la pareja; vuelve a proponer el tratado si no hay relación. |
| `INSUFFICIENT_RESOURCES` | Al aceptar un trato o comprar, alguien ya no tiene lo suyo | `rejected` y enfriamiento de esa oferta. |
| `PATH_NOT_FOUND`, `PATH_TOO_LONG` | Sin ruta entre depósitos (RN-CARAVAN-002) | Aparta a la pareja o la oferta durante el enfriamiento de blanco. |
| `INVALID_TARGET` | Tratado ya existente, oferta ya cerrada, reproponer antes de 10 min | `rejected` y enfriamiento. |

El ajuste de la defensa no tiene errores: la respuesta automática no es un comando.

## 6. Eventos y contratos de red

Ningún mensaje ni valor de enum nuevo. Los bots envían `treaty.propose`, `treaty.respond`, `trade.propose`,
`trade.respond`, `trade.cancel`, `market.post`, `market.buy`, `market.cancel`, `market.list` y `city.build` con
`MARKET` ([websocket-protocol.md](websocket-protocol.md) §7), y leen `treaty.update`, `trade.update`, `caravan.update`,
`market.update` y `market.listing` (§8), que el espejo de `apps/web/src/state/world.ts` ya guarda.

El ajuste de la defensa no cambia ningún evento: la respuesta escribe `UnitAttacked` con `auto: true` como hoy, y el
fin del saqueo se ve con `entity.update` como cualquier fin de saqueo.

## 7. Persistencia

| Pregunta | Respuesta |
|---|---|
| ¿Qué es autoritativo en RAM del game server? | Lo de siempre. El ajuste sólo cambia cuándo se abre un enfrentamiento automático, que ya vive en RAM. |
| ¿Qué se persiste inmediatamente? | Lo de siempre: tratados, tratos, ofertas del mercado y caravanas, por sus specs. |
| ¿Qué se persiste eventualmente? | Lo de siempre. |
| ¿Qué es reconstruible y no se persiste? | La respuesta y su puesto, como en [combat-response.md](combat-response.md) §7: un reinicio la termina y el guerrero queda `IDLE`. En los bots, la tabla de precios y los enfriamientos no se guardan. |

## 8. Invariantes

No asigna invariantes. Cita [INV-COMBAT-002](../invariants/combat.md#inv-combat-002): la respuesta de un saqueador pasa
por el mismo predicado que cualquier golpe (protección, Safe Zone, ocultamiento, tratado). Y cita la familia `INV-TRADE`
de [../invariants/trade.md](../invariants/trade.md), que los bots no pueden saltarse por jugar por el protocolo.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (Vitest, `apps/bots`) | Tratados: propone `TRADE` a los bots sin relación, sube a `NON_AGGRESSION` entre constructores, el saqueador rechaza el pacto (`RN-BOT-011`), ignora a humanos sin `--con-humanos` (`RN-BOT-008`). Tratos: qué pide y qué ofrece según la meta, aceptación por la tabla (`RN-BOT-010`), nada deshabilitado (`RN-BOT-009`). Mercado: construir, publicar, comprar la más barata, retirar la vieja. Reunión (`RN-BOT-012`). |
| Unit (Vitest) | El registro y el resumen con los sucesos nuevos; `--sin-comercio` y `--con-humanos` en las opciones. |
| Unit (Go, `internal/game/simulation`) | `available()` con un saqueo y con un asedio en curso, también mientras camina hacia la ciudad (`RN-COMBAT-039`); la respuesta termina el saqueo o el asedio y fija el puesto en el tile en que estaba (`RN-COMBAT-040`); un ariete que asedia no responde; un movimiento ordenado, también el del saqueador que vuelve cargado, sigue sin sustituirse. |
| Simulation (golden) | Los tres encuentros de §4.6, con los ticks exactos y el resultado de la tabla; el compañero que saquea al lado responde; la vuelta al puesto sin reanudar el saqueo. Lo mismo para un asedio. Los goldens de E3 y E5 que cambien se actualizan en el mismo commit, diciendo por qué. |
| Recovery | Reinicio con un saqueador respondiendo: queda `IDLE`, sin saqueo. |
| Integración / e2e | Ninguno nuevo: ni mensajes ni base cambian. |

## 10. Decisiones de producto (2026-11-21)

Producto aceptó el 2026-11-21 la recomendación de cada pregunta. Cada fila deja la decisión (en negrita) y las
alternativas que se descartaron. Son decisiones de producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Qué perfiles comercian? | **Los dos.** El saqueador también necesita bienes ajenos para pasar de Bronce y para su unidad de civilización; sin comercio, la comparación entre perfiles sigue torcida. Descartada: **sólo el constructor**, más simple, pero el saqueador se queda en Bronce y la medida del saqueo se hace con unidades de una era menos. |
| Q2 | ¿Qué tratados firman? | **`TRADE` entre todos los bots y `NON_AGGRESSION` entre constructores; el saqueador nunca firma pacto.** El tratado comercial no da paz, así que el saqueador puede comerciar con su víctima y saquearla (la escalera de [diplomacy.md](diplomacy.md) lo permite). Descartadas: **sólo `TRADE`**, que deja la escalera sin usar; **toda la escalera con alianzas y rupturas**, que mete la guarnición aliada y el preaviso y hace la medida difícil de leer. |
| Q3 | ¿Comercian con personas? | **Sólo con `--con-humanos`, y nunca con hostilidad nueva.** Sin la opción, una oferta de una persona se deja caducar. Con ella, quien prueba desde el navegador puede firmar un tratado con un bot y cambiarle bienes. Descartadas: **siempre**, que deja a los bots como socios fijos en el mundo de desarrollo; **nunca**, que impide probar el comercio con un bot desde el HUD. |
| Q4 | ¿Tratos directos, mercado o los dos? | **Los dos: el trato con quien tiene tratado y ruta, y el mercado del constructor para el resto.** Así se mide si la comisión del 10 % pesa. El saqueador no construye mercado y, como comprar también pide mercado propio ([markets.md](markets.md) `RN-MARKET-004`), sólo comercia por trato. Descartadas: **sólo tratos**, un decisor más pequeño, sin medir el mercado; **sólo mercado**, que deja los tratados sin uso. |
| Q5 | ¿A qué precio? | **Bien por bien 1 a 1, y madera por piedra 2 a 1, en la tabla del fichero de perfiles (§4.4).** Los bienes valen lo mismo para el avance (cualquier mezcla, [civilization-goods.md](civilization-goods.md)), y la piedra es la que falta en las partidas de E28. Descartadas: **sólo bienes**, sin tocar madera y piedra; **un precio que se mueve con la oferta del tablón**, más interesante pero fuera del objetivo de medir. |
| Q6 | ¿El saqueador captura caravanas? | **No en este slice.** Primero se mide el camino a Hierro sin interferencias; capturar caravanas mezcla dos efectos en la misma medida. Descartada: **sí**, si pasa una a ≤ `STRIKE_RADIUS` de su partida, que mide el riesgo de las caravanas pero castiga a los constructores que el slice quiere ver avanzar. |
| Q7 | ¿Cómo se corrige la defensa? | **Un guerrero que saquea o asedia responde a los golpes como uno disponible (§4.6).** Ataca la causa (el saqueador no se defiende) sin tocar números, y deja que la defensa gane a igualdad de fuerzas. Cambia la decisión Q3 de E3 sólo para el saqueo y el asedio. Descartadas: **sólo reunir la partida en los bots (§4.5) sin tocar el servidor**, que no basta: un saqueador sigue sin devolver golpes y 3 contra 2 pierde igual; **bajar el radio de guardia del saqueo** (de 4 a 2, una variable `EO_` nueva), que debilita a todo defensor, también al que juega en casa y está a la vista; **que el golpe de saqueo no despierte a los guardias** (revertir `RN-COMBAT-027`), que deja la ciudad sin defensa automática. |
| Q8 | Tras responder, ¿el saqueador vuelve a saquear solo? | **No: vuelve al tile donde saqueaba y queda `IDLE`; el jugador vuelve a ordenar.** Coherente con [combat-pillage.md](combat-pillage.md) Q6 (nada de granja automática), y el bot lo hace en el despertar siguiente. Descartada: **reanudar el saqueo solo** al volver al puesto, más cómodo para una persona pero que convierte la defensa en una pausa. |
| Q9 | ¿Cómo se mide el camino a Hierro? | **Una ejecución larga de 4 bots nuevos sin sembrar, a velocidad real, de 3 h en segundo plano, y su resumen en milestones.** Con los costes de hoy (800 de madera, 400 de piedra y 60 bienes para Hierro) es lo mínimo para ver si alguien llega. Descartadas: **con plazos acortados por entorno**, más rápida pero no comparable con una partida real ([bots.md](bots.md) Q5); **6 h o más**, que mide Castillos pero ocupa la máquina medio día. |
| Q10 | ¿Este slice puede cambiar números del equilibrio si las medidas lo piden? | **No: se anotan como hallazgos y van a otra spec.** Así esta spec cambia una regla y se mide su efecto, sin mezclarlo con otros cambios. Descartada: **ajustar en el mismo slice** los costes de las eras o la comisión si la ejecución larga sale muy desviada, que acorta el ciclo pero deja sin saber qué cambio produjo qué. |

## 11. Documentos relacionados

- [bots.md](bots.md) — los bots de E28, su decisor, el registro y las reglas `RN-BOT-001` a `RN-BOT-007`.
- [diplomacy.md](diplomacy.md), [trade.md](trade.md), [caravans.md](caravans.md), [markets.md](markets.md) — lo que
  los bots aprenden a usar.
- [combat-response.md](combat-response.md), [combat-pillage.md](combat-pillage.md), [combat-siege.md](combat-siege.md)
  — las reglas que cambia el ajuste de la defensa.
- [civilization-goods.md](civilization-goods.md), [castle-age.md](castle-age.md) — los bienes ajenos que piden Hierro y
  Castillos.
