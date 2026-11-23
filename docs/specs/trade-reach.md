# Al alcance del comercio

Slice para que **ninguna ciudad nueva nazca fuera del alcance de las caravanas** de todas las demás, y para que los
bots dejen de insistir con socios a los que no llega ninguna ruta. Sale de la ejecución larga de E29
([milestones.md](../roadmap/milestones.md) §E29), que dejó a una ciudad sin un solo trato en tres horas.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-TRADE-REACH` |
| Estado | Accepted (2026-11-22). Propuesta en `Draft` el 2026-11-22 y aceptada el mismo día con la recomendación de cada pregunta de §10. |
| Milestone | E30 Al alcance del comercio (trigésimo slice después de M7) |
| Canon | §4 (vecindad de 8 direcciones), §5 (terreno), §19 (niveles de test) |
| Depende de | [city.md](city.md) (la fundación), [caravans.md](caravans.md), [trade.md](trade.md), [markets.md](markets.md), [civilization-goods.md](civilization-goods.md), [movement.md](movement.md) (los topes del pathfinding), [bots-trade.md](bots-trade.md) |
| Decisiones | **Sin ADR.** La fundación ya es una búsqueda determinista con reglas de [city.md](city.md) §6.1 y esto le añade dos (`RN-CITY-024`, `RN-CITY-025`); [ADR-031](../decisions/ADR-031-pathfinding-node-cap-50k.md) (el tope de nodos) no cambia (Q4), y el alcance de la fundación usa los mismos topes que una caravana. |
| Reemplaza a | — |
| Invariantes | Registra [INV-CITY-019](../invariants/city.md#inv-city-019) (§8), sin cobertura hasta la implementación. Reglas nuevas: `RN-CITY-024` y `RN-CITY-025` ([city.md](city.md) §6.1) y `RN-BOT-013` ([bots-trade.md](bots-trade.md) §4.9) |

## 1. Objetivo

Desde E14 ningún jugador pasa de Bronce sin comerciar: el avance a Hierro pide 60 bienes de otras civilizaciones y el
de Castillos 120 ([civilization-goods.md](civilization-goods.md) §4.4, `RN-GOODS-006`; [castle-age.md](castle-age.md)
`RN-CASTLE-001`), y un bien ajeno sólo llega en una caravana, por un trato, una compra en el mercado o una caravana
capturada. Una caravana necesita una ruta entre los dos depósitos dentro de los topes del pathfinding
(`RN-CARAVAN-002`, `RN-MARKET-004`).

La ejecución larga de E29 enseñó que eso puede dejar a una ciudad sin salida: `bot_constructor_1_e29` no cerró ni un
trato en tres horas, construyó un mercado sin poder comprar nada y se quedó en Bronce, mientras los otros dos
constructores llegaban a Hierro comerciando entre sí. La decisión Q3 de [civilization-goods.md](civilization-goods.md)
descartó un recurso por civilización en el terreno porque «ataría la capacidad a dónde fundaste, que no se elige»;
la geografía de las rutas lo hace igual por otro camino.

### 1.1 Por qué no llegaba (lectura del código y del registro)

- **El tope real es el de nodos, no el de distancia.** El A* rechaza con `PATH_TOO_LONG` si la distancia de Chebyshev
  pasa de `EO_PATHFINDING_MAX_DISTANCE` (256) **o** si expande más de `EO_PATHFINDING_MAX_NODES` (50 000, ADR-031)
  (`internal/pathfinding/astar.go`). Con agua o montañas de por medio, el segundo llega antes: `bot_constructor_1_e29`
  (436,231) y `bot_constructor_3_e29` (213,427) están a 223 tiles de Chebyshev, por debajo de 256, y sus 35 propuestas
  en las dos direcciones volvieron con `PATH_TOO_LONG`. El alcance de una caravana depende del terreno y nadie lo ve.
- **La fundación no mira a los vecinos.** `founding.FindSite` arranca en un punto derivado del nombre de usuario,
  recorre el mundo en espiral y exige 24 tiles de distancia mínima a cualquier ciudad (`RN-CITY-004`), pero **ninguna
  distancia máxima** ni ninguna ruta hacia otra ciudad. En un mundo de 512×512 con pocas ciudades, una cuenta nueva
  puede nacer donde no llega ninguna caravana.
- **En el mundo de desarrollo no hay hoy ninguna ciudad aislada** en línea recta: las 24 ciudades tienen entre 6 y 20
  a menos de 256 tiles. `bot_constructor_1_e29` tenía cerca las de los bots de E28 (a 24-60 tiles), pero los bots
  sólo comercian con los del mismo proceso ([bots-trade.md](bots-trade.md) §4.1). El problema es real para el mundo
  vacío de un despliegue nuevo y para las cuentas que nacen en sus bordes, no un fallo de los bots.
- **Los bots insisten.** Tras un `PATH_TOO_LONG`, la pareja se aparta 10 min y se vuelve a intentar: 54 rechazos en
  tres horas para la misma ciudad, sin ninguna posibilidad de éxito.

## 2. Scope

**Entra.**

- **Fundación (Game Server):** una ciudad nueva, si ya hay otras en el mundo, nace a una distancia máxima de alguna
  existente y con una ruta de caravana hasta su depósito (Q1, Q2; `RN-CITY-024`, `RN-CITY-025`). La primera ciudad del mundo, y el caso en que no
  quede ningún sitio así, siguen la búsqueda de hoy (Q3).
- **Bots:** una pareja sin ruta (`PATH_TOO_LONG` o `PATH_NOT_FOUND` al proponer o aceptar un trato) se aparta para el
  resto de la ejecución, no 10 min (Q6, `RN-BOT-013`).
- Su precisión en [city.md](city.md) §6.1, [caravans.md](caravans.md) y [bots-trade.md](bots-trade.md); sus tests; y la
  prueba en vivo (§4.5).

**No entra.**

- Mover ciudades que ya existen: la fundación es para siempre (Q3).
- Cambiar los topes del pathfinding o la forma de las rutas de las caravanas (Q4).
- Una fuente de bienes ajenos que no sea el comercio: comerciante neutral, conversión en el taller o mercado sin
  caravanas (Q5). La decisión Q1 de [civilization-goods.md](civilization-goods.md) sigue: el comercio es necesario.
- Decir en el HUD si hay ruta antes de proponer un trato: el cliente no tiene el pathfinding (INV-SEC-001), y el
  rechazo ya trae el código.
- Ningún mensaje, campo, tabla ni migración nueva.

## 3. Actores

| Actor | Papel |
|---|---|
| Persona que se da de alta | Elige nombre y civilización, como hoy; no elige dónde nace. Con esta spec nace al alcance de alguien. |
| Game Server (fundación) | `FindSite` añade la condición de alcance (`RN-CITY-024`, `RN-CITY-025`) a las de hoy (`RN-CITY-001` a `RN-CITY-006`, `RN-CITY-023`). |
| Proceso de bots | Aparta para toda la ejecución a la pareja sin ruta. |

## 4. Modelo

### 4.1 La condición de alcance

Un candidato de la espiral de `FindSite` que ya cumple las reglas de hoy (dentro del mundo, transitable, despejado,
a ≥ 24 de cualquier ciudad, con bosque alcanzable y con sitio para los aldeanos) **vale además** si hay al menos una
ciudad existente:

1. a una distancia de Chebyshev ≤ `EO_FOUNDING_MAX_NEIGHBOR_TILES` (recomendado: 96, Q2) entre centros, y
2. con una ruta entre el depósito del candidato y el depósito de esa ciudad **con los mismos topes que una caravana**
   (`EO_PATHFINDING_MAX_DISTANCE` y `EO_PATHFINDING_MAX_NODES`): la misma búsqueda que hace `RN-CARAVAN-002` al
   proponer un trato.

Las ciudades se prueban de la más cercana a la más lejana (empate: menor id), y basta una. El A* sólo se lanza para un
candidato que ya pasó todo lo demás y tiene una ciudad a ≤ 96, así que el coste extra de la fundación es una o pocas
búsquedas por alta, fuera del tick (la fundación corre en el listener interno, `internal/httpapi`).

### 4.2 Cuando no hay sitio al alcance

- **Mundo vacío:** sin ninguna ciudad, la condición no se aplica y la primera nace como hoy.
- **Sin sitio al alcance de nadie** (todo lo cercano a una ciudad está ocupado o es agua): la espiral recorre el mundo
  entero con la condición; si no encuentra nada, repite la búsqueda de hoy, sin ella (Q3). Un alta nunca falla por
  esta regla: `NO_SITE_AVAILABLE` sigue siendo sólo para un mundo sin ningún sitio válido (`RN-CITY-006`).
- El determinismo de `RN-CITY-005` se mantiene: la misma búsqueda con el mismo nombre, mundo y ciudades da el mismo
  sitio.

### 4.3 Bots: la pareja sin ruta

Un `PATH_TOO_LONG` o `PATH_NOT_FOUND` al proponer o aceptar un trato (`trade.propose`, `trade.respond` con
`accept: true`) aparta a esa pareja **hasta el final de la ejecución**, para proponerle y para aceptar lo suyo: las
ciudades no se mueven y el terreno es inmutable, así que la ruta no va a aparecer. Lo mismo para una compra del
mercado sin ruta (`market.buy`), que aparta esa oferta (`RN-BOT-013`). El registro lo apunta una vez por pareja
(`trade` con `status: "unreachable"`, [bots-trade.md](bots-trade.md) §4.7).

### 4.4 Reglas

Numeradas en la aceptación, continuando las familias; viven en [city.md](city.md) §6.1 y en
[bots-trade.md](bots-trade.md) §4.9, y esta tabla las repite:

| ID | Regla |
|---|---|
| `RN-CITY-024` (alcance) | Si hay al menos una ciudad en el mundo, una ciudad nueva nace a ≤ `EO_FOUNDING_MAX_NEIGHBOR_TILES` de Chebyshev de alguna existente y con una ruta entre los dos depósitos dentro de los topes del pathfinding. Si ningún sitio del mundo lo cumple, nace donde la búsqueda de hoy diga. |
| `RN-CITY-025` (orden) | La condición de alcance se comprueba la última, y las ciudades vecinas de la más cercana a la más lejana (empate: menor id); basta una. |
| `RN-BOT-013` (pareja sin ruta) | Un bot no vuelve a proponer ni a aceptar un trato, durante la misma ejecución, con un jugador con el que una propuesta o una aceptación volvió con `PATH_TOO_LONG` o `PATH_NOT_FOUND`, ni a comprar una oferta del mercado que volvió así. |

### 4.5 Prueba en vivo

1. **Altas nuevas en el mundo de desarrollo:** cuatro cuentas nuevas con `pnpm run bots -- --sufijo …` y el servidor
   con la regla; cada ciudad nueva tiene una vecina a ≤ 96 y, entre las cuatro, al menos un trato cerrado en 30 min.
2. **El caso del borde:** un mundo de tests sin sitio al alcance (una ciudad en una isla) funda donde la búsqueda de
   hoy, sin `NO_SITE_AVAILABLE`.
3. **Los bots:** la ejecución con las cuentas `_e29` de nuevo (las de la ejecución larga): `bot_constructor_1_e29`
   apunta una vez cada pareja inalcanzable y deja de proponerles.

## 5. Errores

No hay códigos nuevos. `NO_SITE_AVAILABLE` (409 del listener interno, `RN-CITY-006`) no cambia de significado: la regla
de alcance nunca lo provoca (§4.2).

## 6. Eventos y contratos de red

Ningún mensaje, campo ni valor de enum nuevo. En los bots, el suceso `trade` del JSONL suma el estado `unreachable`.

## 7. Persistencia

| Pregunta | Respuesta |
|---|---|
| ¿Qué es autoritativo en RAM del game server? | Nada nuevo: la fundación lee el mundo y las ciudades que ya tiene. |
| ¿Qué se persiste inmediatamente? | Lo de siempre al fundar (jugador, ciudad, aldeanos). |
| ¿Qué se persiste eventualmente? | Nada nuevo. |
| ¿Qué es reconstruible y no se persiste? | En los bots, las parejas sin ruta: se olvidan al acabar la ejecución. |

Ninguna migración. Una variable de entorno nueva, `EO_FOUNDING_MAX_NEIGHBOR_TILES` (Q2), en `internal/config`,
`.env.example` y [configuration.md](../operations/configuration.md).

## 8. Invariantes

Registra [INV-CITY-019](../invariants/city.md#inv-city-019), sin cobertura hasta la implementación: **toda ciudad
fundada cuando ya había otra en el mundo tiene, al fundarse, una vecina a ≤ `EO_FOUNDING_MAX_NEIGHBOR_TILES` con ruta
entre sus depósitos, salvo que ningún sitio del mundo la tuviera.** Se comprueba en el test de la fundación (unidad sobre mundos
generados) y en la integración del alta. Cita [INV-CITY-001](../invariants/city.md#inv-city-001) y las demás de la
fundación, que no cambian.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (Go, `internal/game/founding`) | Un mundo con una ciudad: el sitio nuevo queda a ≤ 96 y con ruta; con un lago entre la ciudad y el primer candidato de la espiral, el candidato sin ruta se salta; el orden de las vecinas y el empate por id; el mundo vacío funda como hoy; una isla sin sitio al alcance funda como hoy sin `ErrNoSite`; el determinismo (`RN-CITY-005`) con la regla. |
| Integración (`internal/persistence/postgres/fundacion_integration_test.go`, `-tags=integration`) | El alta por el listener interno con dos jugadores: el segundo nace al alcance del primero. **Precisión (2026-11-22):** los tests de integración de la fundación viven ahí, no en `internal/httpapi`, porque montan el listener sobre la base de tests. |
| Unit (Vitest, `apps/bots`) | La pareja sin ruta se aparta hasta el final; el registro `unreachable` una vez por pareja; una oferta del mercado sin ruta se aparta. |
| Mutación | La condición de alcance desactivada hace caer el test del sitio con vecina ([INV-CITY-019](../invariants/city.md#inv-city-019)). |

## 10. Decisiones de producto (2026-11-22)

Producto aceptó el 2026-11-22 la recomendación de cada pregunta. Cada fila deja la decisión (en negrita) y las
alternativas que se descartaron. Son decisiones de producto, no de ingeniería: cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Cómo se evita que una ciudad quede sin comercio? | **Que una ciudad nueva nazca al alcance de otra (§4.1, `RN-CITY-024`).** Ataca la causa en el único momento en que se elige dónde está una ciudad, no cuesta nada en el tick y deja intacto que el comercio sea necesario. Descartadas: **caravanas por tramos** (rutas de cualquier longitud en varios A*), que acaba con el límite pero pone varias búsquedas de 50 000 nodos en el tick por cada trato; **subir los topes sólo para las caravanas**, que alarga el alcance sin garantizarlo y cuesta CPU del tick; **una fuente de bienes sin comercio** (Q5), que rompe la decisión Q1 de [civilization-goods.md](civilization-goods.md). |
| Q2 | ¿A qué distancia máxima de una vecina? | **96 tiles de Chebyshev, configurable (`EO_FOUNDING_MAX_NEIGHBOR_TILES`).** Cuatro veces el mínimo de 24, deja hueco para muchas ciudades alrededor de cada una y queda lejos del tope de nodos con el terreno de hoy (con 50 000 nodos, la mediana de la hierba a su colina más cercana es de 74 tiles y llega el 98 %, ADR-031). Descartadas: **64**, mundos más apretados y más choques tempranos; **128**, más espacio pero más rutas que el tope de nodos corta. |
| Q3 | ¿Y si ningún sitio está al alcance de nadie? | **La búsqueda de hoy, sin la condición: el alta nunca falla por esta regla (§4.2).** Rechazar el alta (`NO_SITE_AVAILABLE`) dejaría fuera a alguien por cómo está el mapa. Las ciudades que ya existen no se mueven. Descartadas: **rechazar el alta**, que garantiza el alcance a costa de cerrar la puerta; **reubicar** ciudades aisladas, que rompe «la fundación es para siempre». |
| Q4 | ¿Se tocan los topes del pathfinding o la forma de las rutas? | **No en este slice.** El tope de nodos es la defensa del tick (ADR-031), y con Q1 toda ciudad nueva tiene una vecina con ruta. Si el mundo crece y aparecen ciudades con vecinas pero sin socios dispuestos, se mide antes de cambiar nada (Q10 de [bots-trade.md](bots-trade.md): una regla por spec). Descartada: **un tope de nodos propio para las rutas de caravana**, más alto, calculado al proponer y aceptar, que pide su ADR. |
| Q5 | ¿Una fuente de bienes ajenos para quien no tenga socios? | **No.** El comercio es necesario por decisión (civilization-goods Q1), y el comerciante neutral ya se descartó en markets.md Q7. Con Q1 el caso deja de darse en las altas nuevas. Descartadas: **convertir en el taller** el bien propio en uno ajeno a 3 por 1, una salida cara que nunca compensa frente a un trato; **un mercado neutral** en el mapa, ya descartado. |
| Q6 | En los bots, ¿cuánto se aparta una pareja sin ruta? | **Hasta el final de la ejecución (`RN-BOT-013`)**, porque ni las ciudades ni el terreno se mueven; el registro lo apunta una vez. Descartada: **el enfriamiento de 10 min de hoy**, que sólo gasta órdenes (54 rechazos en la ejecución larga). |
| Q7 | ¿Los bots comercian también con bots de otras ejecuciones a la vista? | **No en este slice:** proponer a un bot que no corre deja la oferta sin respuesta 24 h y bloquea la pareja. Basta con lanzar juntos los bots que tienen que comerciar. Descartada: **sí**, con las ciudades `bot_*` del espejo, que daría socios a `bot_constructor_1_e29` pero llena el mundo de ofertas que caducan. |

## 11. Documentos relacionados

- [city.md](city.md) §6.1 — la fundación y sus reglas `RN-CITY-001` a `RN-CITY-006` y `RN-CITY-023`.
- [caravans.md](caravans.md), [trade.md](trade.md), [markets.md](markets.md) — la ruta que pide cada caravana.
- [civilization-goods.md](civilization-goods.md), [castle-age.md](castle-age.md) — los bienes ajenos que piden Hierro y
  Castillos.
- [bots-trade.md](bots-trade.md) — los bots que comercian y la ejecución larga que encontró el caso.
- [ADR-031](../decisions/ADR-031-pathfinding-node-cap-50k.md) — el tope de nodos del pathfinding.
