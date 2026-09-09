# Glosario canónico

Vocabulario único del dominio de Empires Online: cada término con su definición precisa, sus valores canónicos y el documento donde se especifica.

---

## Cómo usar este glosario

- Los términos van **en inglés** porque son los identificadores reales del código, del protocolo y del
  esquema de base de datos. La prosa que los define va en español.
- Si un documento usa un término del dominio con un significado distinto al de aquí, el documento está
  mal, no el glosario.
- Cuando un término tiene una representación exacta en el código (nombre de tabla, columna, tipo de
  mensaje, constante o variable de entorno), esa representación aparece en `monoespaciado`.
- Los términos marcados **Fuera de MVP** están definidos porque el diseño los necesita, pero no se
  implementan en el primer vertical slice.

Orden: alfabético estricto.

---

## A

### A\* (A-star)

Algoritmo de búsqueda de caminos usado por el servidor para resolver un `unit.move`. Opera sobre el grid
de tiles con **vecindad de 8 direcciones** y heurística **octile**. Es la única fuente de caminos del
juego: el cliente nunca calcula ni envía un path.

Restricciones canónicas: costes en enteros escalados —`costScaleOrtho = 1000` y `costScaleDiag = 1414`,
multiplicados por el `costUnits` del terreno— para garantizar admisibilidad y determinismo; desempate
estable por `(f, h, y, x)`; prohibido iterar mapas de Go sin ordenar. Límites
`EO_PATHFINDING_MAX_NODES` (20000) y `EO_PATHFINDING_MAX_DISTANCE` (256 tiles); al excederse se devuelve
`PATH_TOO_LONG`. Si no existe camino, `PATH_NOT_FOUND`. La ruta devuelta **incluye el tile de origen**
como primer elemento, y `from == to` devuelve una ruta de un solo tile sin error.

Se accede siempre a través de la interfaz estable
`type Pathfinder interface { FindPath(ctx context.Context, grid Grid, from, to world.Tile, opts Options) ([]world.Tile, error) }`,
que permite sustituir la implementación por Hierarchical A\* sin tocar el protocolo ni el dominio.

Ver: [../architecture/pathfinding.md](../architecture/pathfinding.md).

### ADR (Architecture Decision Record)

Documento breve e **inmutable** que registra una decisión arquitectónica: contexto, decisión,
alternativas descartadas y consecuencias. No se edita para cambiar de opinión; se crea un ADR nuevo que
lo *supersede*. Es un paso obligatorio del ciclo Spec-Driven Development cuando la decisión es estructural
o cara de revertir.

Ver: [../decisions/README.md](../decisions/README.md).

### Area of Interest (AoI)

Conjunto de chunks a los que una sesión está suscrita en un momento dado y, por extensión, el conjunto de
entidades cuyos deltas recibe. Se calcula como los chunks dentro de un radio de
`EO_INTEREST_RADIUS_CHUNKS` (2 por defecto) alrededor del centro de vista de la sesión. Al conectar, el
centro se sitúa en la ciudad del jugador; después se actualiza con `session.view` (sujeto a rate limit).

Nada fuera del AoI se envía al cliente. El AoI es estado **reconstruible**: no se persiste.

Ver: [../architecture/networking.md](../architecture/networking.md).

### Authoritative Position

Posición verdadera de una unidad según el servidor, expresada siempre en tiles enteros
(*World Coordinates*). Para una unidad en movimiento se define analíticamente como
**el último waypoint de la polilínea cuyo `tMs` es menor o igual a `(T - start_time_ms)`**, donde `T` es
el instante consultado en epoch milliseconds.

Su propiedad clave es que **no requiere replay de ticks**: se reconstruye por cálculo directo desde
`unit_movements` (búsqueda binaria sobre la polilínea), lo que hace que sobreviva a reinicios del proceso.
Antes del instante 0 la posición es el origen; después del último `tMs`, el destino. Es lo contrario de la
*Render Position*, que es una aproximación visual del cliente.

Ver: [../specs/movement.md](../specs/movement.md), [../invariants/movement.md](../invariants/movement.md).

---

## C

### Chunk

Bloque cuadrado de **32 × 32 tiles** (`EO_CHUNK_SIZE=32`). Es la unidad de partición del mundo para
persistencia, difusión de deltas y suscripción de interés. Con el mundo MVP de 512 × 512 hay
16 × 16 = **256 chunks**.

Cálculo canónico:

```
chunkX  = x >> 5
chunkY  = y >> 5
chunkId = chunkY * chunksPerRow + chunkX     // uint32
```

El terreno de cada chunk se persiste en `world_chunks` como un `bytea` de **1024 bytes** (un byte de
`TerrainType` por tile, en orden fila-mayor). Esa copia existe para auditoría y para permitir mapas
editados en el futuro: la fuente primaria del terreno es la **regeneración determinista desde
`EO_WORLD_SEED` en cada arranque**.

Ver: [../architecture/networking.md](../architecture/networking.md), [../database/schema.md](../database/schema.md).

### City

Asentamiento de un jugador, representado por la tabla `cities`. La ciudad inicial de cada jugador consta
de **1 Town Center**, **1 zona urbana amurallada inicial** —un rectángulo **3 × 3** marcado en el
*blocked overlay* alrededor del centro, sin mutar el terreno— y **3 Villagers**, que nacen a radio 2 del
centro. La ciudad es el ancla del jugador en el mundo: determina el centro de vista inicial al conectar y
es el sujeto de la protección offline mediante su `presence_state`.

El emplazamiento lo elige `internal/game/founding` mediante una búsqueda determinista en espiral desde
una semilla derivada del nombre de usuario, exigiendo un entorno despejado de radio 3 y una separación
mínima de **24 tiles** entre centros de ciudad. `cities` lleva `UNIQUE (center_x, center_y)` y una columna
`version` para concurrencia optimista.

Ver: [../specs/city.md](../specs/city.md), [../invariants/city.md](../invariants/city.md).

### Civilization

Identidad cultural de un jugador (Roman, Byzantine, Persian, Norse…), almacenada en la tabla
`civilizations`. Determina unidades, tecnologías y bonos propios. **Todos los jugadores son humanos**: no
existen razas; la variedad viene de aquí.

Es un eje **ortogonal e independiente** de la *Global Faction*: dos jugadores de la misma civilización
pueden estar en facciones opuestas, y viceversa. Las capacidades exclusivas por civilización son el
mecanismo que genera comercio real entre jugadores.

Ver: [game-pillars.md](game-pillars.md) (Pilar 4), [../specs/player.md](../specs/player.md).

### Command

**Intención validable** enviada por el cliente o generada internamente, expresada en imperativo y aún no
ejecutada. Los comandos del MVP son `MoveUnit` y `CancelMovement`, transportados por los mensajes
`unit.move` y `unit.cancel_move`.

Un comando puede ser rechazado. Se procesa en las fases 1 y 2 del tick (`drain commands`,
`validate & apply commands`) y siempre lleva un *Request Id* para garantizar idempotencia. Distinto de un
*Event*, que es un hecho ya consumado y no rechazable.

Ver: [../specs/websocket-protocol.md](../specs/websocket-protocol.md), [../architecture/game-loop.md](../architecture/game-loop.md).

---

## D

### Delta

Mensaje incremental servidor→cliente que describe **solo lo que ha cambiado** dentro del área de interés
de la sesión: `entity.spawn`, `entity.update`, `entity.despawn`, `city.update`, `territory.update`, y los
mensajes de movimiento `unit.movement.started` / `.completed` / `.cancelled`.

Los deltas derivan de eventos de dominio y se emiten en la fase 7 del tick (`emit deltas`), difundidos por
chunk. **El mundo completo no se retransmite nunca**: tras el `world.snapshot` inicial, todo es delta.

Ver: [../architecture/networking.md](../architecture/networking.md).

### Dirty State

Estado que ha cambiado en RAM y todavía no se ha escrito a PostgreSQL, marcado con un flag para ser
recogido por el *Flush* periódico. Es la estrategia aplicada a datos de alta frecuencia y baja
criticidad: posiciones consolidadas de unidades y HP.

Contrasta con el *Write-through*, que escribe de forma inmediata y transaccional.

Ver: [../database/persistence-strategy.md](../database/persistence-strategy.md).

---

## E

### Era

Escalón de progresión temporal del jugador, definido **como dato en la tabla `eras`, no en código**.
Determina el techo de población de la ciudad.

| Era | Population cap |
|---|---|
| `STONE_AGE` | 20 |
| `BRONZE_AGE` | 50 |
| `IRON_AGE` | 100 |
| `CASTLE_AGE` | 150 |

Ver: [../specs/city.md](../specs/city.md).

### Event

**Hecho consumado**, nombrado en pasado y no rechazable: `UnitMovementStarted`, `UnitMovementCompleted`,
`UnitSpawned`, `CityProtectionEngaged`. Los eventos de dominio son la base tanto de los *Deltas* de red
como de las filas de `world_events`.

La distinción Command / Event / State es explícita y se mantiene en el código: un `Command` es
`MoveUnit`, el `Event` resultante es `UnitMovementStarted`, y el `State` es `Unit.status = MOVING`.

Ver: [../architecture/game-server.md](../architecture/game-server.md).

---

## F

### Flush

Escritura periódica a PostgreSQL del estado marcado como *dirty*. Se ejecuta cada
`EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` ticks (**50**, es decir **5 segundos** a 10 Hz). El flush ocurre en
workers asíncronos: **el tick nunca ejecuta I/O bloqueante contra PostgreSQL**, solo encola trabajo en la
fase 8 (`enqueue persistence`). Los workers reintentan hasta **3 veces con backoff** y, si se agotan,
ejecutan la compensación `OnPermanentFailure`.

La profundidad de esa cola se observa con la métrica `eo_persistence_queue_depth`.

Ver: [../database/persistence-strategy.md](../database/persistence-strategy.md).

---

## G

### Game Loop

Bucle principal del Game Server: ejecuta un *Tick* cada 100 ms (`EO_TICK_RATE_HZ=10`) de forma continua e
independiente de que haya jugadores conectados. Su orden de fases es fijo y determinista:

1. `drain commands` (cola no bloqueante)
2. `validate & apply commands`
3. `advance movement`
4. `resolve simulation` (reservado: combate — **Fuera de MVP**)
5. `process timers/scheduled events` (presence, protección, territorio)
6. `update world state / interest sets`
7. `emit deltas` (broadcast por chunk)
8. `enqueue persistence`

Ver: [../architecture/game-loop.md](../architecture/game-loop.md).

### Game Ticket

Credencial de un solo uso y vida corta que autoriza la apertura de una sesión de juego. Es un **JWT
HS256** firmado con `EO_AUTH_JWT_SECRET`, con **TTL de 60 s** y claims
`{ sub: playerId, jti, iat, exp, aud: "game-server" }`.

**Quién lo emite hoy:** el propio Game Server, desde `POST /api/auth/register` y `POST /api/auth/login`
(`internal/httpapi`, contraseñas con bcrypt). Es explícitamente provisional: la arquitectura objetivo de
[../decisions/ADR-010-authentication-game-ticket.md](../decisions/ADR-010-authentication-game-ticket.md)
traslada esos endpoints a Next.js cuando exista `apps/web`.

El cliente lo envía en `session.hello { ticket }` como primer mensaje del WebSocket. El servidor verifica
firma, `exp` y `aud`, y **consume el `jti` en Redis** (`ticket:jti:{jti}`, TTL 120 s) para impedir replay.
Un ticket ya consumido es rechazado.

Ver: [../specs/websocket-protocol.md](../specs/websocket-protocol.md).

### Garrison

Estacionamiento de unidades de un jugador dentro de una ciudad ajena, representado por la tabla
`garrisons`. **Requiere un `Treaty` en estado `ACTIVE` con el flag `allows_garrison`**; en caso contrario
el servidor rechaza con `TREATY_REQUIRED`. Una unidad guarnecida tiene `units.status = GARRISONED` y no
puede moverse (`UNIT_GARRISONED`).

En el MVP la tabla se crea con **lógica mínima o diferida**.

Ver: [../specs/garrison.md](../specs/garrison.md).

### Global Faction

Alineación en el conflicto global del mundo: `ORDER`, `CHAOS` o `NEUTRAL`, almacenada en la tabla
`factions`. Define el marco de conflicto de alto nivel entre jugadores.

Es un eje **ortogonal e independiente** de la *Civilization*. No confundir ambos conceptos: la
civilización dice *qué puedes producir*, la facción dice *con quién estás alineado*.

Ver: [vision.md](vision.md), [../specs/player.md](../specs/player.md).

---

## H

### Heartbeat

Latido periódico que mantiene viva la clave de presencia del jugador en Redis. Se emite cada
`EO_PRESENCE_HEARTBEAT_SECONDS` (**10 s**) y refresca `presence:player:{playerId}`, cuyo TTL es
`EO_PRESENCE_TTL_SECONDS` (**30 s**). La relación 10 s / 30 s tolera la pérdida de dos latidos
consecutivos antes de considerar ausente al jugador. La validación cruzada del arranque exige
`EO_PRESENCE_HEARTBEAT_SECONDS < EO_PRESENCE_TTL_SECONDS`, estrictamente.

No confundir con el ping del protocolo WebSocket (`session.ping` / `session.pong`, ping cada 15 s y
timeout de lectura de 45 s), que vigila la conexión, no la presencia. Esos cuatro plazos del transporte
—`WSHandshakeTimeout = 5 s`, `WSPingInterval = 15 s`, `WSReadTimeout = 45 s`, `WSWriteTimeout = 10 s`—
son **constantes de código**, no variables de entorno.

Ver: [../specs/presence.md](../specs/presence.md).

---

## I

### Idempotency

Garantía de que ejecutar el mismo comando dos veces produce el mismo efecto que ejecutarlo una vez. Se
implementa registrando el *Request Id* en Redis (`idem:{playerId}:{requestId}`, **TTL 300 s**) y, para
comandos durables, también en la tabla `idempotency_keys`.

Regla canónica: **un `requestId` repetido devuelve la respuesta original, no re-ejecuta el comando**. Es
lo que hace segura la reconexión y el reintento del cliente. La reserva se hace con `SETNX` antes de
ejecutar; si Redis no responde, el comando **se ejecuta igualmente**: se prefiere dejar jugar a bloquear
al jugador, y así está decidido a conciencia.

Ver: [../specs/websocket-protocol.md](../specs/websocket-protocol.md), [../invariants/security.md](../invariants/security.md).

### Interest Management

Mecanismo que decide qué información recibe cada sesión. La suscripción se hace **por chunk**, con radio
por defecto de `EO_INTEREST_RADIUS_CHUNKS` (2) alrededor del centro de vista; el centro se establece en la
ciudad del jugador al conectar y se actualiza con `session.view`.

Su propósito es doble: limitar el ancho de banda y **limitar la información** (un cliente no puede
observar lo que no debería ver). La suscripción la gestiona el `Hub`: `Subscribe` devuelve a la vez los
chunks que entran y los que salen. Los conjuntos de interés se recalculan en la fase 6 del tick y son
estado reconstruible.

El terreno de un chunk se envía **una sola vez por sesión** (`NeedsTerrain` / `MarkTerrainSent`), porque
el terreno es inmutable.

Ver: [../architecture/networking.md](../architecture/networking.md).

### Invariant

Propiedad del sistema que debe ser cierta en **todo** momento observable, con un **ID estable** y un test
que la verifica. Familias canónicas: `INV-WORLD-*`, `INV-PLAYER-*`, `INV-CITY-*`, `INV-UNIT-*`,
`INV-MOVE-*`, `INV-PERSIST-*`, `INV-SEC-*`, más `INV-TERR-*`, `INV-SAFE-*` e `INV-GARR-*` para
territorio, zonas seguras y diplomacia. El **único registro** es `docs/invariants/`: un ID estable
designa un solo invariante y jamás se reutiliza con otro significado.

Una violación de invariante es un defecto de severidad máxima, no una discusión de diseño. "Sin
violaciones de invariantes" es un punto explícito de la Definition of Done.

Ver: [../invariants/README.md](../invariants/README.md).

### Isometric Projection

Transformación de *World Coordinates* a *Screen Coordinates*, ejecutada **exclusivamente en el cliente**:

```
screenX = (x - y) * (TILE_W / 2)
screenY = (x + y) * (TILE_H / 2)
TILE_W = 64 ; TILE_H = 32
```

**El servidor nunca maneja píxeles.** Ninguna constante de proyección puede aparecer en el dominio del
Game Server.

Ver: [../architecture/frontend.md](../architecture/frontend.md).

---

## M

### Movement

Traslado de una unidad desde un tile origen hasta un tile destino, materializado como una fila de
`unit_movements` con su *Timed Polyline* (columna `path`, de tipo **`jsonb`**, con
`[{"x":int,"y":int,"tMs":int}, ...]`), su `start_time_ms` y su `arrival_time_ms`, ambos `bigint` en epoch
milliseconds.

Estados: `ACTIVE`, `COMPLETED`, `CANCELLED`, `FAILED`.

Invariante central: **una unidad tiene como máximo un movimiento `ACTIVE`**. Una nueva orden cancela la
anterior (`CANCELLED`) dentro de la misma transacción. La regla la materializa en base un índice único
parcial:

```sql
CREATE UNIQUE INDEX unit_movements_one_active_per_unit
    ON unit_movements (unit_id) WHERE status = 'ACTIVE';
```

Coste temporal por segmento, en **aritmética entera exacta** (nada de coma flotante):

```go
ms := (baseMsPerTile*costUnits + 5) / 10           // redondeo al ms más cercano
if diagonal { ms = (ms*1414214 + 500000) / 1000000 }  // √2 en punto fijo, mismo redondeo
if ms < 1 { ms = 1 }
```

Regla canónica: **cada segmento se redondea al milisegundo más cercano y sólo después se acumula.** No se
trunca, porque mil pasos truncados regalarían casi un segundo de ventaja. `baseMsPerTile` es propiedad
del tipo de unidad (`VILLAGER` = 600 ms) y `costUnits` es el coste del terreno de destino en décimas
(`world.CostBase = 10`).

Ver: [../specs/movement.md](../specs/movement.md).

---

## O

### Octile Heuristic

Heurística admisible de A\* para grids de 8 direcciones con coste diagonal distinto del ortogonal. Se
calcula en enteros escalados y **ponderada por `MinTerrainCostUnits`, que vale 6 (`ROAD`)**:

```
h = minCost*1000*pasosRectos + minCost*1414*pasosDiagonales     // minCost = 6
```

Ponderarla con el coste de la hierba (10) la haría **inadmisible** en un mundo donde existen caminos más
baratos, y A* dejaría de garantizar la ruta óptima. El uso de enteros elimina la aritmética de coma
flotante del corazón del algoritmo y garantiza admisibilidad y determinismo.

Ver: [../architecture/pathfinding.md](../architecture/pathfinding.md).

### Offline Pending

Estado intermedio de `cities.presence_state`. Se alcanza cuando el jugador no tiene **ninguna** sesión
WebSocket abierta y, además, ha transcurrido el margen de reconexión (`DisconnectGrace`, igual a
`EO_PRESENCE_TTL_SECONDS`, 30 s). Ese margen evita que un corte de red breve altere el estado del mundo.

La transición **la decide el game loop en RAM**, comparando el instante de la última desconexión contra
`DisconnectGrace` en la fase 5 del tick. No se lee la expiración de la clave de Redis: Redis mantiene la
presencia para observadores externos y para el futuro multiproceso, pero no arbitra la máquina de estados.

Desde `OFFLINE_PENDING`, transcurrido `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` (300), la ciudad pasa
a `PROTECTED`. Si el jugador reconecta antes, vuelve a `ONLINE`. Su función de diseño es impedir la
protección instantánea por desconexión deliberada.

Ver: [../specs/presence.md](../specs/presence.md).

### Online

Estado de `cities.presence_state` que indica que el jugador está presente: tiene al menos una conexión
WebSocket activa. Un jugador con varias sesiones abiertas sigue `ONLINE` mientras le quede una. Es el
único estado en el que la ciudad está plenamente expuesta a las reglas del mundo.

Ver: [../specs/presence.md](../specs/presence.md).

---

## P

### Path

Secuencia de tiles devuelta por el *Pathfinder* como resultado de A\*, desde el tile origen hasta el tile
destino, ambos incluidos. Es **estado autoritativo**: el cliente nunca lo calcula ni lo propone.

Un `Path` se convierte en *Timed Polyline* al asignar a cada tile su `tMs` acumulado. Sin ese paso, un
path es solo geometría.

Ver: [../architecture/pathfinding.md](../architecture/pathfinding.md).

### Player

Cuenta de jugador, fila de la tabla `players`. Es la única tabla cuya clave primaria es **`uuid`** (el
resto usa `bigint GENERATED ALWAYS AS IDENTITY`). Todo jugador es humano y está caracterizado por su
*Civilization* y su *Global Faction*, ejes independientes.

Ver: [../specs/player.md](../specs/player.md), [../invariants/player.md](../invariants/player.md).

### Population

Número de unidades vivas que un jugador sostiene, contabilizadas contra el *Population Limit* de su
ciudad. En el MVP la ciudad inicial arranca con 3 `VILLAGER`.

Ver: [../specs/city.md](../specs/city.md).

### Population Limit

Techo de población de una ciudad, calculado como:

```
population_limit = era.population_cap + modificadores de edificios
```

En el MVP **no hay modificadores de edificios**, por lo que equivale al `population_cap` de la era actual.
Superarlo produce `POPULATION_LIMIT_REACHED`.

Ver: [../specs/city.md](../specs/city.md).

### Presence

Hecho de que un jugador esté efectivamente conectado, representado en Redis por la clave
`presence:player:{playerId}` con TTL `EO_PRESENCE_TTL_SECONDS` (30 s) refrescada por *Heartbeat* cada
`EO_PRESENCE_HEARTBEAT_SECONDS` (10 s).

La presencia es **estado caliente y derivado**: vive en Redis, no en PostgreSQL, y se reconstruye. Lo que
sí es durable es su consecuencia: el `presence_state` de la ciudad. El servidor es el único que decide el
estado; el cliente solo lo observa. La clave de Redis publica la presencia hacia fuera; **no** es la que
dispara la transición a `OFFLINE_PENDING`, que la evalúa el game loop en RAM.

Ver: [../specs/presence.md](../specs/presence.md).

### Protected

Estado de `cities.presence_state` en el que la ciudad está protegida por ausencia del jugador. Se alcanza
desde `OFFLINE_PENDING` tras el *Protection Cooldown*, y se abandona al reconectar (`→ ONLINE`).

Las acciones hostiles contra una ciudad en este estado se rechazan con `CITY_PROTECTED`. La columna
`protection_until` permanece **NULL** mientras el jugador siga offline —la protección es indefinida en el
MVP— y se limpia al reconectar; el campo existe para poder acotarla en el futuro sin migrar el esquema.

Ver: [../specs/presence.md](../specs/presence.md).

### Protection Cooldown

Tiempo que una ciudad permanece en `OFFLINE_PENDING` antes de pasar a `PROTECTED`. Configurable mediante
`EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS`, **300 segundos** por defecto. Ningún valor de gameplay se
hardcodea: pasa por `internal/config`.

Ver: [../specs/presence.md](../specs/presence.md), [../operations/configuration.md](../operations/configuration.md).

---

## R

### Render Position

Posición **visual** de una entidad en el cliente, típicamente entre dos tiles, obtenida por interpolación
sub-tile a partir de la *Timed Polyline* recibida. Existe únicamente para que el movimiento se vea fluido
a 60 fps sobre una simulación de 10 Hz.

Nunca es autoritativa, nunca se envía al servidor y nunca debe usarse para tomar decisiones de juego. La
verdad es la *Authoritative Position*, siempre en tiles enteros.

Ver: [../architecture/frontend.md](../architecture/frontend.md).

### Request Id

UUIDv4 generado por el cliente e **obligatorio en todo comando**, transportado en el envelope
cliente→servidor `{ v, type, requestId, payload }`. El servidor lo devuelve en las respuestas
correlacionadas y lo usa como clave de *Idempotency*.

También es un campo estándar del logging estructurado (`request_id`), lo que permite trazar un comando de
extremo a extremo.

Ver: [../specs/websocket-protocol.md](../specs/websocket-protocol.md).

---

## S

### Safe Zone

Región del mundo donde aplican reglas de seguridad especiales, representada por la tabla `safe_zones`.
Tipos canónicos: `DENSE_FOREST` (sobre terreno `FOREST`) y `CAVERN` (adyacente a `MOUNTAIN`).

**La seguridad la calcula y valida SIEMPRE el servidor.** El cliente no puede declararse a salvo. En el
MVP la tabla se crea con lógica mínima o diferida.

Ver: [../specs/safe-zones.md](../specs/safe-zones.md).

### Screen Coordinates

Coordenadas en píxeles dentro del canvas del cliente, resultado de aplicar la *Isometric Projection* a las
*World Coordinates*. Existen solo en el cliente. Ningún mensaje del protocolo transporta screen
coordinates y ningún paquete del dominio del servidor las conoce.

Ver: [../architecture/frontend.md](../architecture/frontend.md).

### Session

Vínculo autenticado entre un jugador y el Game Server durante una conexión WebSocket, registrado en la
tabla `sessions`. Se crea tras validar el *Game Ticket* en `session.hello` y responder `session.welcome`.

Cada sesión mantiene su propio contador `seq` (uint64 monótono por conexión) para los mensajes
servidor→cliente, su área de interés, el conjunto de chunks cuyo terreno ya recibió, su presupuesto de
rate limit (20 msg/s, burst 40) y su cola de salida (`EO_WS_OUTBOUND_QUEUE_SIZE`, 256 por defecto). Si esa
cola se llena, la conexión se cierra con `4500` y el cliente reconecta con un snapshot limpio. El
`session_id` es campo estándar del logging.

Ver: [../specs/websocket-protocol.md](../specs/websocket-protocol.md).

### Snapshot

Mensaje `world.snapshot` enviado **al conectar**, que contiene el estado completo de las entidades dentro
del área de interés inicial de la sesión. Es el único envío "grueso" del protocolo: a partir de él todo es
*Delta*. Su payload es
`{serverTimeMs, tick, chunks[], terrain[], units[], cities[], territories[]}`, donde `terrain[].terrain`
va en **base64** de `size*size` bytes.

La goroutine de la conexión **no lee el estado del mundo**: envía el comando `RequestSnapshot` por el
canal de comandos, con un canal de respuesta con buffer, y el game loop responde. Eso es lo que impide una
carrera de datos contra la simulación.

Regla canónica: **nunca se retransmite el mundo completo**. Un snapshot es del AoI, no del mundo.

Ver: [../architecture/networking.md](../architecture/networking.md).

### State

Situación actual de una entidad del dominio, distinta de la intención (*Command*) y del hecho (*Event*).
Ejemplo canónico: `Unit.status = MOVING`.

Estados canónicos del MVP:

| Entidad | Columna | Valores |
|---|---|---|
| Unit | `units.status` | `IDLE`, `MOVING`, `GARRISONED`, `HIDDEN`, `DEAD` |
| Movement | `unit_movements.status` | `ACTIVE`, `COMPLETED`, `CANCELLED`, `FAILED` |
| City | `cities.presence_state` | `ONLINE`, `OFFLINE_PENDING`, `PROTECTED` |
| Treaty | `treaties.status` | `PROPOSED`, `ACTIVE`, `EXPIRED`, `BROKEN` |

Ver: [../specs/README.md](../specs/README.md).

---

## T

### Terrain Cost

Coste temporal de **entrar** en un tile, asociado al `TerrainType` de destino de un segmento y expresado
en **décimas del coste base** (`costUnits`, con `world.CostBase = 10`, de modo que 10 equivale al
multiplicador 1.0). Se expresa como entero a propósito: la aritmética debe ser exacta y reproducible en
cualquier plataforma. Enum `TerrainType` (uint8, persistido como byte):

| code | valor | walkable | `costUnits` | equivalente | notas |
|---|---|---|---|---|---|
| `GRASSLAND` | 0 | sí | 10 | ×1.00 | base |
| `FOREST` | 1 | sí | 16 | ×1.60 | soporta SafeZone `DENSE_FOREST` |
| `HILL` | 2 | sí | 18 | ×1.80 | |
| `MOUNTAIN` | 3 | no | — | — | bloqueado; soporta SafeZone `CAVERN` adyacente |
| `WATER` | 4 | no | — | — | bloqueado en MVP (naval **Fuera de MVP**) |
| `ROAD` | 5 | sí | 6 | ×0.60 | menor coste transitable: `MinTerrainCostUnits = 6` |

Duraciones de paso resultantes para un `VILLAGER` (`baseMsPerTile = 600`):

| terreno | ortogonal | diagonal |
|---|---|---|
| `GRASSLAND` | 600 ms | 849 ms |
| `FOREST` | 960 ms | 1358 ms |
| `HILL` | 1080 ms | 1527 ms |
| `ROAD` | 360 ms | 509 ms |

Fuera de los límites del mundo, `TerrainAt` devuelve `WATER` y la transitabilidad es falsa: los bordes se
comportan como un muro y no provocan pánico.

El bloqueo dinámico por edificios y ciudades se representa aparte, en una capa de ocupación
(*blocked overlay*, `SetBlocked(minX, minY, maxX, maxY, bool)`), **sin mutar el terreno base**: fundar una
ciudad no cambia el `TerrainType` de debajo.

Ver: [../architecture/pathfinding.md](../architecture/pathfinding.md).

### Territory

Región del mundo reclamable, tabla `territories`. En el MVP su geometría es **rectangular**
(`min_x, min_y, max_x, max_y`). La geometría es estable en el tiempo; quién la domina no lo es, y por eso
se modela aparte (ver *Territory Control*).

Ver: [../specs/territory.md](../specs/territory.md).

### Territory Control

Relación de dominio sobre un territorio, tabla **separada** `territory_control`. La separación respecto a
`territories` es deliberada: permite que el control cambie de manos, se dispute o quede vacante sin tocar
la definición geométrica.

En el MVP se crea con lógica mínima o diferida. El delta de red asociado es `territory.update`.

Ver: [../specs/territory.md](../specs/territory.md).

### Tick

Iteración del *Game Loop*. Duración canónica **100 ms** (10 Hz). Ejecuta las ocho fases en orden fijo y
**nunca realiza I/O bloqueante contra PostgreSQL**.

Si un tick tarda más que su período, se incrementa la métrica `eo_game_tick_overruns_total`; su duración
se observa con el histograma `eo_game_tick_duration_seconds`.

Ver: [../architecture/game-loop.md](../architecture/game-loop.md).

### Tick Number

Contador `uint64` **monótono** de ticks transcurridos desde el origen temporal del mundo. Relación
canónica con el tiempo absoluto:

```
tickTime = world_state.epoch_ms + tickNumber * tickDurationMs
```

Es el reloj lógico del mundo y aparece como campo `tick` en el logging estructurado. No debe confundirse
con el timestamp `ts` de los mensajes servidor→cliente, que es epoch ms del servidor.

Ver: [../architecture/game-loop.md](../architecture/game-loop.md).

### Tile

Celda cuadrada del grid del mundo, unidad atómica de posición. **El servidor razona exclusivamente en
tiles enteros**; los píxeles son asunto del cliente.

Cada tile tiene un `TerrainType` y, adicionalmente, puede estar bloqueado por la capa de ocupación.
Vecindad de **8 direcciones**, con movimiento diagonal permitido **solo si ambos tiles ortogonales
adyacentes son transitables** (prohibido el *corner cutting*).

Ver: [../architecture/overview.md](../architecture/overview.md).

### Timed Polyline

Representación canónica de un movimiento: array de waypoints `{x, y, tMs}` donde `tMs` es el offset en
**milisegundos desde `start_time_ms`** en el que la unidad **alcanza** ese tile. El primer waypoint es el
origen con `tMs = 0`.

Es la pieza central del modelo de movimiento porque hace la posición **analíticamente reconstruible**: no
hace falta replay de ticks, ni que el proceso haya estado vivo, para saber dónde está una unidad en un
instante dado.

Ver: [../specs/movement.md](../specs/movement.md),
[../decisions/ADR-011-movement-timed-polyline.md](../decisions/ADR-011-movement-timed-polyline.md).

### Town Center

Edificio central de la ciudad (`TOWN_CENTER`). **Es un building, no una unidad**: no aparece en `units` ni
cuenta como población. Toda ciudad inicial tiene exactamente uno.

Ver: [../specs/city.md](../specs/city.md).

### Treaty

Acuerdo persistido entre jugadores, tabla `treaties`. Tipos: `NON_AGGRESSION`, `ALLIANCE`, `TRADE`.
Estados: `PROPOSED`, `ACTIVE`, `EXPIRED`, `BROKEN`. Incluye el flag `allows_garrison`.

Regla canónica: **solo un tratado `ACTIVE` habilita garrison**. En el MVP la tabla se crea con lógica
mínima o diferida.

Ver: [../specs/garrison.md](../specs/garrison.md).

---

## U

### Unit

Entidad móvil propiedad de un jugador, fila de la tabla `units`. Estados (`units.status`): `IDLE`,
`MOVING`, `GARRISONED`, `HIDDEN`, `DEAD`.

El único `unit_type` del MVP es `VILLAGER`. Cada tipo define su `baseMsPerTile`, usado en el cálculo de
coste temporal del movimiento.

Ver: [../specs/unit.md](../specs/unit.md), [../invariants/units.md](../invariants/units.md).

### Villager

Único `unit_type` del MVP: aldeano. Valores canónicos: **hp 40**, **speed 600 ms/tile**
(`baseMsPerTile = 600`). Cada ciudad inicial arranca con **3 villagers**.

Ver: [../specs/unit.md](../specs/unit.md).

### Vertical Slice

Rebanada funcional que atraviesa **todas** las capas del sistema —cliente, protocolo, servidor, dominio,
persistencia— en lugar de completar una capa entera. Es la unidad de entrega del MVP: su propósito es
demostrar que el eje persistente funciona de extremo a extremo, no acumular funcionalidad.

Ver: [mvp-scope.md](mvp-scope.md).

---

## W

### Waypoint

Elemento de una *Timed Polyline*: `{x, y, tMs}`. Representa un tile del path junto al offset temporal, en
milisegundos desde `start_time_ms`, en el que la unidad lo alcanza. El primero es siempre el tile de
origen con `tMs = 0`; el último corresponde a `arrival_time_ms`.

Ver: [../specs/movement.md](../specs/movement.md).

### World

El único mundo compartido y persistente del juego: grid 2D de tiles cuadrados de **512 × 512** en el MVP
(`EO_WORLD_WIDTH`, `EO_WORLD_HEIGHT`, que deben ser múltiplos exactos de `EO_CHUNK_SIZE`). Se
**regenera determinísticamente desde `EO_WORLD_SEED` en cada arranque**; `world_chunks` guarda una copia
del terreno para auditoría y para permitir mapas editados en el futuro, pero no es la fuente primaria. Su
estado global vive en `world_state`, que incluye `epoch_ms`.

No hay instancias, no hay salas y no hay reinicio.

Ver: [vision.md](vision.md), [../architecture/overview.md](../architecture/overview.md).

### World Coordinates

Coordenadas lógicas de un tile: `x`, `y` de tipo **int32**, con origen `(0,0)` arriba-izquierda, **X hacia
el este** e **Y hacia el sur**. Son las únicas coordenadas que existen en el protocolo, en la base de
datos y en el dominio del servidor.

Su rango válido en el MVP es `0 ≤ x < 512`, `0 ≤ y < 512`; fuera de él, `TARGET_OUT_OF_BOUNDS`.

Ver: [../architecture/overview.md](../architecture/overview.md), [../invariants/world.md](../invariants/world.md).

### Write-through

Estrategia de persistencia **transaccional y no consolidable**: el cambio se escribe en PostgreSQL como
una transacción propia, en lugar de acumularse hasta el siguiente flush. Se aplica a creación de player,
city y unit; inicio y finalización de movimiento; cambios de ownership; transiciones de presencia;
treaties; garrisons; y `world_events`.

Matiz obligatorio: **no todas esas escrituras son síncronas**. El alta de jugador
(`POST /api/auth/register`) sí commitea antes de nada —primero la transacción de PostgreSQL, y sólo
después el comando `IntroducePlayer` que incorpora al jugador al mundo en RAM—. En cambio el movimiento
se aplica primero en RAM y su transacción se **encola**, porque el tick no puede hacer I/O de PostgreSQL.
Ver *Dirty State*, *Flush* y la deuda `DEBT-17` de [../roadmap/backlog.md](../roadmap/backlog.md).

Es la contraparte del *Dirty State* + *Flush*, reservado para datos de alta frecuencia y baja criticidad.

Ver: [../database/persistence-strategy.md](../database/persistence-strategy.md).

---

## Términos definidos fuera de este glosario

Los códigos de error (`UNAUTHORIZED`, `TARGET_NOT_WALKABLE`, `PATH_TOO_LONG`…), los tipos de mensaje del
protocolo v1 y las variables de entorno `EO_*` no se repiten aquí uno a uno. Su catálogo completo y
autoritativo está en:

- [../specs/websocket-protocol.md](../specs/websocket-protocol.md) — mensajes, envelopes, códigos de error y de cierre.
- [../operations/configuration.md](../operations/configuration.md) — variables `EO_*` y valores por defecto.
- [../database/schema.md](../database/schema.md) — tablas, columnas y `CHECK`s.
