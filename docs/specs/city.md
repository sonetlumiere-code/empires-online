# City

Especificación de la entidad `City`: emplazamiento válido, zona urbana amurallada inicial como overlay de ocupación, población y límite por era, y ownership único.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CITY` |
| Estado | Draft |
| Milestone | M2 Player & City |
| Canon | §4, §5, §9, §10, §11, §12, §13, §16, §17 |
| Depende de | [ADR-008](../decisions/ADR-008-grid-coordinate-system.md), [player.md](player.md), [presence.md](presence.md) |
| Reemplaza a | — |
| Invariantes | Reutiliza `INV-CITY-001..007` del registro ([../invariants/city.md](../invariants/city.md)) y **añade** `INV-CITY-008..010`. Los de presencia y protección son `INV-CITY-011..016`, en [presence.md](presence.md) |

## 1. Objetivo

Definir la ciudad como ancla territorial y demográfica de un jugador: dónde puede existir, cómo ocupa el
terreno sin mutarlo, cuánta población soporta y cómo el servidor difunde sus cambios. La presencia y la
protección de la ciudad se especifican aparte en [presence.md](presence.md); aquí solo se declaran sus columnas
y su relación con el resto del modelo.

## 2. Scope (y no-scope)

**Dentro del MVP**

- Tabla `cities` completa con todos los campos canónicos.
- Reglas de emplazamiento: tile transitable, dentro de los límites del mundo, distancia mínima a otras
  ciudades, zona urbana completa sobre tiles válidos.
- Zona urbana amurallada inicial representada como **overlay de ocupación** derivado, sin mutar el terreno base
  ni `world_chunks`.
- Cálculo de `population` y derivación de `population_limit` desde `eras.population_cap`.
- Error `POPULATION_LIMIT_REACHED` y su punto de aplicación.
- Ownership único e inmutable en MVP.
- Mensaje `city.update` y su difusión por chunk.

**Fuera de MVP (no se implementa; se documenta el punto de extensión)**

- Tabla general de edificios: el canon §11 fija las tablas del MVP y no incluye `buildings`. En MVP el
  `TOWN_CENTER` no es una fila, es el tile central de la ciudad. `Fuera de MVP`.
- Modificadores de `population_limit` por edificios: la fórmula los contempla, el MVP los evalúa a cero
  (canon §10: "sin modificadores en MVP").
- Avance de era, fundación de ciudades adicionales, renombrado, conquista, destrucción y transferencia de
  ownership: `TBD (fuera de MVP)`.
- Almacenes, recursos, producción y entrenamiento de unidades: no existe mensaje `unit.train` en el canon §13.
  `Fuera de MVP`.
- Asedio y combate sobre ciudades: `Fuera de MVP` (ver [presence.md](presence.md) §6 para la regla de
  protección y su punto de aplicación futuro).

## 3. Actores

| Actor | Rol |
|---|---|
| Jugador propietario | Observa su ciudad; en MVP no la modifica directamente con ningún comando v1. |
| Otros jugadores | Observan la ciudad cuando su área de interés incluye el chunk de la ciudad. |
| Game server | Único autoritativo: elige emplazamiento, calcula población, difunde `city.update`. |
| Game loop | Único escritor de `presence_state`: la degradación por ausencia se evalúa en la fase 5 (*process timers*) y la vuelta a `ONLINE` llega como comando y se aplica en la fase 1-2. Ver [presence.md](presence.md). |
| PostgreSQL | Fuente de verdad durable de `cities`. |
| Overlay de ocupación (RAM) | Estructura derivada que marca los tiles bloqueados por ciudades. |

## 4. Inputs

### 4.1 Del alta del jugador

La única vía de creación de ciudades en MVP es el bootstrap de [player.md](player.md) §10.1. No existe un
comando `city.found` en el protocolo v1 (canon §13).

| Input | Tipo | Origen |
|---|---|---|
| `ownerPlayerId` | `uuid` | Transacción de bootstrap (`internal/persistence/postgres/bootstrap.go`). |
| `name` | `text` | Hoy se deriva del `username`: `username + "polis"` (`internal/httpapi/auth.go`). Que el jugador proponga el nombre es `Fuera de MVP`. Validación en `RN-CITY-011`. |
| Semilla de emplazamiento | `hashSeed(username) uint64` | Elección determinista del sitio: dos altas simultáneas no compiten por el mismo tile. **No** deriva de `EO_WORLD_SEED`, que gobierna el terreno, no el emplazamiento. |

### 4.2 Del mundo

| Input | Origen |
|---|---|
| `TerrainType` de cada tile candidato | `world_chunks` (bytea de 1024 bytes por chunk) / caché en RAM. |
| Overlay de ocupación vigente | RAM, derivado de las ciudades existentes. |
| Centros de las ciudades existentes | `cities.center_x`, `cities.center_y`. |

### 4.3 Configuración consumida

| Variable | Valor | Uso |
|---|---|---|
| `EO_WORLD_WIDTH` | 512 | Límite superior exclusivo de `center_x`. |
| `EO_WORLD_HEIGHT` | 512 | Límite superior exclusivo de `center_y`. |
| `EO_WORLD_SEED` | 20260909 | Determinismo del **terreno** generado, sobre el que se valida el emplazamiento. |
| `EO_CHUNK_SIZE` | 32 | Cálculo del chunk de la ciudad para la difusión por interés. |
| `EO_INTEREST_RADIUS_CHUNKS` | 2 | Alcance de la difusión de `city.update`. |
| `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` | 300 | Solo consumido por [presence.md](presence.md). |

La geometría del asentamiento **no** es configuración por entorno: son constantes del paquete
`internal/game/founding` (`site.go`), documentadas aquí como tales y no como variables `EO_`.

| Constante | Valor | Uso |
|---|---|---|
| `townCenterRadius` | 1 | Semilado de la zona urbana: un rectángulo **3×3** alrededor del centro. |
| `clearRadius` | 3 | Semilado del entorno que debe estar despejado y transitable para aceptar un candidato. |
| `spawnRadius` | 2 | Distancia a la que nacen los aldeanos iniciales, justo fuera del 3×3. |
| `minCityDistance` | 24 | Separación mínima (Chebyshev) entre centros de ciudad. |

## 5. Outputs

| Output | Destino | Cuándo |
|---|---|---|
| Fila en `cities` | PostgreSQL | Bootstrap, dentro de la transacción del jugador. |
| Marcas en el overlay de ocupación | RAM | Al incorporar la ciudad (`IntroducePlayer`) y al cargar el mundo al arrancar. |
| `city.update` | Suscriptores del chunk de la ciudad | Cambio de `name`, `population`, `populationLimit`, `presenceState` o `protectionUntilMs`. |
| `CityView` dentro de `world.snapshot` | Sesión que entra en el área de interés | Snapshot inicial y cada `session.view`. `entity.spawn` transporta **unidades**, no ciudades. |
| Fila `PlayerBootstrapped` en `world_events` | PostgreSQL | Dentro de la transacción del bootstrap. |
| Log `msg="jugador incorporado al mundo"` con `city_id` y `units` | stdout JSON | Incorporación al mundo en RAM. |

## 6. Reglas de negocio

Dominio de reglas: `RN-CITY`.

### 6.1 Emplazamiento

| ID | Regla |
|---|---|
| `RN-CITY-001` | El tile central `(center_x, center_y)` debe estar **dentro de los límites del mundo**: `0 <= center_x < EO_WORLD_WIDTH` y `0 <= center_y < EO_WORLD_HEIGHT`. Fuera de rango es un fallo del generador, no del jugador. |
| `RN-CITY-002` | El tile central debe ser **transitable**: `TerrainType` ∈ {`GRASSLAND`, `FOREST`, `HILL`, `ROAD`}. `MOUNTAIN` y `WATER` están bloqueados (canon §5). |
| `RN-CITY-003` | **Todo** el entorno de semilado `clearRadius = 3` alrededor del centro (un cuadrado 7×7, más amplio que la propia zona urbana 3×3) debe pasar `World.IsWalkable`: dentro de límites, terreno transitable y **sin marca en el overlay de ocupación**. Como `IsWalkable` ya combina terreno y ocupación, la zona urbana de otra ciudad invalida el candidato sin ninguna comprobación adicional. |
| `RN-CITY-004` | La distancia **Chebyshev** entre el centro de la nueva ciudad y el centro de cualquier ciudad existente debe ser `>= minCityDistance = 24` tiles. Se usa Chebyshev porque la vecindad del mundo es de 8 direcciones (canon §4). Es una constante del paquete `internal/game/founding`, no una variable `EO_`: parametrizarla por entorno haría que dos despliegues generaran mundos incompatibles. |
| `RN-CITY-005` | La búsqueda de emplazamiento es **determinista**: dados el mismo `username`, el mismo mundo y el mismo conjunto de ciudades previas, produce siempre el mismo resultado. Se implementa como un punto de partida derivado de `hashSeed(username)` seguido de una **espiral cuadrada** de anillos con orden de tiles fijo (`ringTiles`), muestreada con paso `ring/8 + 1` para no generar millones de candidatos en los anillos grandes; prohibido iterar mapas de Go sin ordenar. |
| `RN-CITY-006` | Si la espiral recorre el mundo entero sin encontrar un sitio válido, `FindSite` devuelve `ErrNoSite` y el alta falla. Hoy el alta la sirve `POST /api/auth/register` (`internal/httpapi`), que responde `409 Conflict` con el código HTTP `NO_SITE_AVAILABLE`. Ese código **no** pertenece al catálogo de 22 códigos del protocolo WebSocket (canon §16) y no se inventa ninguno: es un error de la API HTTP de alta. |

### 6.2 Zona urbana amurallada

| ID | Regla |
|---|---|
| `RN-CITY-007` | La zona urbana inicial es un **rectángulo centrado** en `(center_x, center_y)` de semilado `townCenterRadius = 1`, es decir un **3×3** completo: `[center_x-1, center_x+1] × [center_y-1, center_y+1]`. La geometría es rectangular por coherencia con `territories` (canon §10: geometría rectangular en MVP). Zonas urbanas mayores o crecientes son `Fuera de MVP`. |
| `RN-CITY-008` | La zona urbana se representa **exclusivamente en la capa de ocupación** (`blocked overlay`, canon §5), mediante `World.SetBlocked(minX, minY, maxX, maxY, true)`. **Nunca** se modifica el `TerrainType` de un tile ni el contenido de `world_chunks`: el terreno es el mapa generado desde `EO_WORLD_SEED` y debe seguir siendo reproducible bit a bit desde la semilla. |
| `RN-CITY-009` | El 3×3 se marca **entero** como `BLOCKED`: en MVP no hay distinción entre muralla e interior, ni puertas, porque el rectángulo no tiene interior. Los 3 aldeanos iniciales nacen **fuera** del bloque, a `spawnRadius = 2` del centro, en un orden de offsets fijo (E, O, S, N, y luego las cuatro diagonales) del que se toman los primeros transitables. Murallas con perímetro, interior y puertas son `TBD (fuera de MVP)`. |
| `RN-CITY-010` | El overlay **no se persiste**: es una estructura reconstruible en RAM, derivada de `cities` al arrancar el servidor. Ver §10. |
| `RN-CITY-011` | No se exige unicidad global de nombres de ciudad en MVP; la identidad es `cities.id`. Hoy el nombre lo genera el servidor (`username + "polis"`) y `cities.name` no lleva `CHECK` de longitud en la migración 000001: una cota de longitud y una normalización `NFKC` quedan `TBD (fuera de MVP)`, para cuando el jugador pueda proponer el nombre. |

En MVP el `TOWN_CENTER` (canon §10) **no es una fila de ninguna tabla**: la migración 000001 no crea ninguna
tabla `buildings`. Se representa por el tile `(center_x, center_y)` de la ciudad más su marca en el overlay.
Cuando exista una tabla de edificios (`Fuera de MVP`), el `TOWN_CENTER` pasará a ser una fila y esta regla se
sustituirá por otra spec.

```
Zona urbana (3x3, townCenterRadius = 1) y aldeanos a spawnRadius = 2

        x-3 x-2 x-1  cx  x+1 x+2 x+3
  y-3 [ o ][ o ][ o ][ o ][ o ][ o ][ o ]     # = tile BLOCKED en el overlay (3x3 completo)
  y-2 [ o ][ o ][ o ][ o ][ o ][ o ][ o ]     T = centro de la ciudad (TOWN_CENTER), BLOCKED
  y-1 [ o ][ o ][ # ][ # ][ # ][ o ][ o ]     v = VILLAGER inicial (3, status IDLE)
   cy [ o ][ v ][ # ][ T ][ # ][ v ][ o ]     o = entorno exigido despejado (clearRadius = 3)
  y+1 [ o ][ o ][ # ][ # ][ # ][ o ][ o ]
  y+2 [ o ][ o ][ o ][ v ][ o ][ o ][ o ]
  y+3 [ o ][ o ][ o ][ o ][ o ][ o ][ o ]

Los 3 primeros aldeanos toman los 3 primeros offsets transitables del orden fijo
(+2,0), (-2,0), (0,+2), (0,-2) y luego las cuatro diagonales a distancia 2.

El TerrainType de estos tiles NO cambia: sigue siendo el que generó EO_WORLD_SEED.
Lo único que cambia es la capa de ocupación, que vive en RAM y se reconstruye al arrancar.
```

Consecuencia sobre pathfinding: A\* consulta `World.IsWalkable`, que es *terreno transitable* **y** *no ocupado*.
Un tile `GRASSLAND` marcado en el overlay es intransitable a efectos de path, pero conserva su `costUnits = 10`
y su byte en `world_chunks`. Ver [movement.md](movement.md) y
[ADR-008](../decisions/ADR-008-grid-coordinate-system.md).

### 6.3 Población

| ID | Regla |
|---|---|
| `RN-CITY-012` | `population` es el número de unidades vivas asociadas a la ciudad. **No se lleva a mano**: `CityRepo.UpdatePopulation` la recalcula con `SET population = COALESCE((SELECT count(*) FROM units u WHERE u.city_id = c.id AND u.status <> 'DEAD'), 0)` dentro de la misma transacción que crea o destruye unidades. Los estados `IDLE`, `MOVING`, `GARRISONED` y `HIDDEN` cuentan; `DEAD` no. |
| `RN-CITY-013` | Cada unidad consume 1 punto de población en MVP, porque el único `unit_type` del MVP es `VILLAGER` (canon §10). Una tabla de coste poblacional por tipo de unidad es `Fuera de MVP`. |
| `RN-CITY-014` | `population_limit = era.population_cap + Σ(modificadores de edificios)`. En MVP el sumatorio es **cero** (canon §10), luego `population_limit == eras.population_cap` de la era de la ciudad. |
| `RN-CITY-015` | `eras.population_cap` se lee de la tabla `eras`, **nunca** de una constante en código: `STONE_AGE`=20, `BRONZE_AGE`=50, `IRON_AGE`=100, `CASTLE_AGE`=150 (canon §10). El código no contiene esos literales; la tabla se siembra por migración. |
| `RN-CITY-016` | `population_limit` se materializa como columna de `cities` (lectura barata para `city.update`) y se recalcula en cada transacción que pueda alterarlo: creación de la ciudad y cambio de era (`Fuera de MVP`). Es un caché derivado, y [`INV-CITY-003`](../invariants/city.md#inv-city-003) lo verifica. |
| `RN-CITY-017` | Toda operación que incremente la población valida **antes de escribir** que `population + coste <= population_limit` (guard de dominio `City.HasPopulationRoom(n)`). Si no cabe, la operación se rechaza entera con `POPULATION_LIMIT_REACHED` y no se escribe nada. La validación ocurre dentro de la misma transacción que la escritura, para que no haya carrera entre dos comandos concurrentes; `cities.version` da la concurrencia optimista y el `CHECK cities_population_within_limit` es la última línea de defensa. |
| `RN-CITY-018` | En MVP la única operación que incrementa la población es el bootstrap (3 aldeanos contra un cap de 20), de modo que `POPULATION_LIMIT_REACHED` **no es alcanzable en juego normal**. La regla y su código existen igualmente y se testean con un fixture que baja el límite artificialmente. El entrenamiento de unidades es `Fuera de MVP`. |

### 6.4 Ownership

| ID | Regla |
|---|---|
| `RN-CITY-019` | Una ciudad tiene **exactamente un** propietario: `owner_player_id uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE`. No existe ciudad sin dueño, ni copropiedad, ni ownership compartido por clan (`Fuera de MVP`). |
| `RN-CITY-020` | En MVP `owner_player_id` es **inmutable** tras la creación: no hay conquista, cesión ni abandono. Cuando existan, el cambio de ownership será write-through inmediato y transaccional (canon §12) y emitirá su propio evento. |
| `RN-CITY-021` | El ownership lo determina siempre el servidor. Ningún mensaje del cliente puede afirmar, sugerir ni modificar la propiedad de una ciudad (canon §1.1). |

## 7. Estados y transiciones

La ciudad tiene un único eje de estado en MVP: `presence_state` ∈ {`ONLINE`, `OFFLINE_PENDING`, `PROTECTED`}.

```
ONLINE --disconnect(+grace)--> OFFLINE_PENDING --cooldown--> PROTECTED
PROTECTED --connect--> ONLINE ;  OFFLINE_PENDING --connect--> ONLINE
```

El autómata vive en `city.CanTransition(from, to)` (`internal/domain/city/city.go`), que además acepta
`from == to` como idempotente: reaplicar el estado vigente no es un error, y ningún otro par lo es.

Las transiciones, sus disparadores exactos, el grace de reconexión y el cooldown están especificados en
[presence.md](presence.md). Esta spec solo fija que:

- `presence_state` es una columna **durable** de `cities` en PostgreSQL, no un valor derivado de Redis;
- su valor inicial en el bootstrap es `ONLINE`, porque el bootstrap ocurre en el contexto de un jugador que
  acaba de autenticarse;
- ninguna otra regla de esta spec cambia `presence_state`.

Ciclo de vida de la fila (independiente del anterior): `creada` → (`Fuera de MVP`: destruida / conquistada).
En MVP una ciudad, una vez creada, existe para siempre.

## 8. Errores

| Código | Condición exacta | Notas |
|---|---|---|
| `CITY_NOT_FOUND` | Se referencia un `cities.id` inexistente o no visible para el solicitante. | Se usa también para no filtrar existencia de ciudades fuera del área de interés. |
| `POPULATION_LIMIT_REACHED` | Una operación incrementaría `population` por encima de `population_limit`. | Ver `RN-CITY-017`. No alcanzable en juego normal en MVP (`RN-CITY-018`). |
| `CITY_PROTECTED` | Acción hostil contra una ciudad con `presence_state = 'PROTECTED'`. | Punto de aplicación en [presence.md](presence.md) §6. El combate está `Fuera de MVP`. |
| `FORBIDDEN` | Operación sobre una ciudad que no pertenece al jugador de la sesión. | |
| `INVALID_MESSAGE` | Nombre de ciudad fuera de rango o mal formado, coordenadas ausentes. | `details` indica el campo. |
| `TARGET_OUT_OF_BOUNDS` | Coordenada de ciudad fuera de `[0, EO_WORLD_WIDTH)` × `[0, EO_WORLD_HEIGHT)`. | Aplicable a la validación del emplazamiento. |
| `TARGET_NOT_WALKABLE` | Tile de emplazamiento sobre `MOUNTAIN` o `WATER`. | |
| `INTERNAL_ERROR` | Fallo de transacción o violación de invariante detectada en runtime. | |
| `NOT_IMPLEMENTED` | Fundación adicional, renombrado, avance de era, cesión. | Todo `Fuera de MVP`. |

Los nueve códigos anteriores pertenecen al catálogo cerrado de 22 del protocolo WebSocket
(`internal/protocol/codes.go`, verificado contra `@empires-online/protocol` por un contract test). El alta de
jugador, en cambio, la sirve hoy `POST /api/auth/register` sobre HTTP y usa códigos **propios de esa API**, que
no están en el catálogo y no deben confundirse con él: `NO_SITE_AVAILABLE` (409, `RN-CITY-006`),
`USERNAME_TAKEN` (409), `INVALID_USERNAME` (400), `WEAK_PASSWORD` (400) e `INTERNAL_ERROR` (500).

## 9. Invariantes (con ID)

El **único registro de numeración** es [docs/invariants/](../invariants/README.md): un ID estable designa un
solo invariante y no se reutiliza con otro significado. `INV-CITY-001..007` ya están asignados allí; esta spec
los **cita**, no los redefine. Los invariantes nuevos que introduce esta spec continúan la familia a partir de
`INV-CITY-008` y están registrados en [../invariants/city.md](../invariants/city.md). Los de presencia y
protección son `INV-CITY-011..016` y viven en [presence.md](presence.md).

**Invariantes del registro que esta spec hace cumplir** (significado fijado en `docs/invariants/city.md`):

| ID | Enunciado del registro | Dónde lo defiende esta spec |
|---|---|---|
| [`INV-CITY-001`](../invariants/city.md#inv-city-001) | Una ciudad tiene exactamente un owner. | `RN-CITY-019..021`; `owner_player_id NOT NULL` + FK a `players (id)`. |
| [`INV-CITY-002`](../invariants/city.md#inv-city-002) | `0 <= population <= population_limit`. | `RN-CITY-017`; `CHECK cities_population_within_limit` y `CHECK (population >= 0)`. |
| [`INV-CITY-003`](../invariants/city.md#inv-city-003) | `population_limit` deriva de la era vigente. | `RN-CITY-014..016`; en MVP `population_limit == eras.population_cap`. |
| [`INV-CITY-004`](../invariants/city.md#inv-city-004) | El dominio de `presence_state` es cerrado. | `CHECK cities_presence_state_valid` + el tipo `city.PresenceState` con `Valid()`. |
| [`INV-CITY-005`](../invariants/city.md#inv-city-005) | Solo transiciones permitidas de `presence_state`. | §7; el autómata es `city.CanTransition`. Detalle en [presence.md](presence.md). |
| [`INV-CITY-006`](../invariants/city.md#inv-city-006) | Una ciudad protegida rechaza las acciones prohibidas. | Error `CITY_PROTECTED` de §8; punto de aplicación en [presence.md](presence.md) §6.2. |
| [`INV-CITY-007`](../invariants/city.md#inv-city-007) | El centro de la ciudad está sobre un tile válido. | `RN-CITY-001..003`; `founding.FindSite` solo acepta candidatos que pasan `World.IsWalkable`. |

**Invariantes nuevos que introduce esta spec** (`INV-CITY-008..010`):

| ID | Invariante | Verificación |
|---|---|---|
| `INV-CITY-008` | Las zonas urbanas de dos ciudades distintas **nunca** comparten un tile en el overlay de ocupación. | `UNIQUE (center_x, center_y)` (`cities_unique_center`) más `minCityDistance = 24` en `founding.FindSite`, muy por encima del tamaño 3×3 de la zona; test que crea N ciudades y verifica que ningún tile del overlay se marca dos veces. |
| `INV-CITY-009` | `population` coincide siempre con el número de unidades no `DEAD` de la ciudad tras cada transacción confirmada. | `CityRepo.UpdatePopulation` recalcula desde `units` en la misma transacción; test de integración que compara la columna con el conteo real. |
| `INV-CITY-010` | El `TerrainType` de un tile **nunca** cambia por la existencia de una ciudad: `world_chunks` regenerado desde `EO_WORLD_SEED` es idéntico byte a byte antes y después de fundar ciudades. | Fundar solo llama a `World.SetBlocked`, que escribe el overlay y no el array de terreno; test de recovery con hash del chunk antes y después. |

Nota sobre «un jugador, una ciudad»: es una regla del MVP que se sostiene en el dominio
(`State.cityByOwner` indexa una ciudad por owner y `CityRepo.GetByOwner` hace `ORDER BY id LIMIT 1`), **no** un
índice único: la migración 000001 crea `cities_owner_idx`, que no es único, precisamente porque el multi-ciudad
es una evolución prevista. No es un invariante propio: es el alcance MVP de
[`INV-CITY-001`](../invariants/city.md#inv-city-001) y complementa
[`INV-PLAYER-003`](../invariants/player.md#inv-player-003).

## 10. Persistencia

**1. Autoritativo en RAM del game server**

- **Overlay de ocupación** (`blocked overlay`): bitset por chunk que marca los tiles bloqueados por murallas y
  centros urbanos. Es la estructura que consulta el pathfinding.
- Índice de ciudades por chunk, para resolver rápidamente a quién difundir `city.update`.
- `population` y `population_limit` en caliente durante el tick.

**2. Escritura durable transaccional** (canon §12)

- Creación de la ciudad y recálculo de `population`: **síncronos y transaccionales**, dentro de la transacción
  de bootstrap (`Bootstrapper.Create`), que corre fuera del tick.
- Transiciones de `presence_state`, `last_online_at`, `last_offline_at`, `protection_until` y `version`: las
  decide el game loop y **se encolan** en la cola de persistencia (`Persister.Submit`), porque el tick nunca
  hace I/O de PostgreSQL. La difusión de `city.update` no espera al `COMMIT`. Ver [presence.md](presence.md)
  §10 para la ventana de riesgo y su recuperación.
- Cambios de ownership: `Fuera de MVP`; cuando existan serán transaccionales.

**3. Dirty-flag + flush periódico (`EO_PERSISTENCE_FLUSH_INTERVAL_TICKS = 50`, 5 s)**

- Nada de `cities` usa esta vía. Las escrituras de ciudad son de baja frecuencia y de alta consecuencia; se
  hacen transaccionales.

**4. Reconstruible (no se persiste)**

- El **overlay de ocupación** completo: se reconstruye al arrancar recorriendo `cities` y aplicando
  `SetBlocked(center_x-1, center_y-1, center_x+1, center_y+1, true)` por cada una (`cmd/server/main.go`, justo
  después de `simulation.Hydrate`). No hay tabla que lo almacene y no se persiste ningún tile bloqueado.
- El índice ciudad→chunk: `World.ChunkOf(x, y)` = `(x / EO_CHUNK_SIZE, y / EO_CHUNK_SIZE)` (canon §4).
- El conjunto de suscriptores de una ciudad: deriva del interés de cada sesión.

### 10.1 Tabla `cities`

El DDL **autoritativo** es la migración `000001_initial_schema.up.sql`, reflejada en
[../database/schema.md](../database/schema.md). Se reproduce aquí **verbatim**, sin variantes, porque el resto
de la spec cita estos nombres de columna:

```sql
CREATE TABLE cities (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_player_id   uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    name              text        NOT NULL,
    center_x          integer     NOT NULL,
    center_y          integer     NOT NULL,
    era               text        NOT NULL REFERENCES eras (code),
    population        integer     NOT NULL DEFAULT 0 CHECK (population >= 0),
    population_limit  integer     NOT NULL CHECK (population_limit >= 0),
    presence_state    text        NOT NULL DEFAULT 'ONLINE',
    last_online_at    timestamptz,
    last_offline_at   timestamptz,
    protection_until  timestamptz,
    -- Concurrencia optimista.
    version           integer     NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    -- INV-CITY-004: el autómata de presencia sólo admite estos tres valores.
    CONSTRAINT cities_presence_state_valid
        CHECK (presence_state IN ('ONLINE', 'OFFLINE_PENDING', 'PROTECTED')),
    -- INV-CITY-002: la población nunca supera el límite.
    CONSTRAINT cities_population_within_limit
        CHECK (population <= population_limit),
    -- Dos ciudades no pueden compartir el mismo centro (INV-CITY-008).
    CONSTRAINT cities_unique_center UNIQUE (center_x, center_y)
);

CREATE TRIGGER cities_set_updated_at
    BEFORE UPDATE ON cities
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX cities_owner_idx ON cities (owner_player_id);
-- Consulta caliente del tick de timers: ciudades cuyo cooldown puede haber vencido.
CREATE INDEX cities_offline_pending_idx
    ON cities (last_offline_at)
    WHERE presence_state = 'OFFLINE_PENDING';
```

Notas:

- **No existen** columnas `*_time_ms` en `cities`. Las tres marcas de presencia son `timestamptz`
  (`last_online_at`, `last_offline_at`, `protection_until`) y la aritmética del cooldown se hace en Go sobre
  `time.Time` con el `Clock` inyectado (`city.ShouldEngageProtection`). Ver [presence.md](presence.md) §10.
- `cities_owner_idx` **no es único**: la regla MVP «un jugador, una ciudad» se sostiene en el dominio, no en la
  base de datos, porque el multi-ciudad es una evolución prevista que no debe exigir una migración destructiva.
- Los límites superiores de `center_x` y `center_y` no van en un `CHECK` con literal 512: el tamaño del mundo
  es configuración (`EO_WORLD_WIDTH` / `EO_WORLD_HEIGHT`) y hardcodearlo en el esquema lo duplicaría. La
  validación completa de `INV-CITY-007` (límites **y** transitabilidad) es responsabilidad del dominio
  (`RN-CITY-001..003`).
- `era` es una FK a `eras (code)` en lugar de un `CHECK` con la lista de eras, porque el canon §10 exige que
  las eras y sus `population_cap` vivan en la tabla y no en código. No lleva `DEFAULT`: el alta la fija
  explícitamente.
- `version` da concurrencia optimista (canon §11) para el patrón de `RN-CITY-017`; `CityRepo.SetPresence` y
  `CityRepo.UpdatePopulation` lo incrementan en cada escritura.
- `updated_at` lo mantiene el trigger común `set_updated_at()` (canon §11).

## 11. Eventos

Los eventos se nombran en pasado (canon §15). Hoy el log `world_events` tiene **una sola** fila escrita por el
alta; el resto de los hechos de ciudad viaja como delta de red (`city.update`) sin dejar traza en
`world_events`.

| Evento | Payload | Emitido cuando | Estado |
|---|---|---|---|
| `PlayerBootstrapped` | `{ cityId, villagers, center: { x, y } }`, con `player_id` y `tick` en columnas propias de `world_events` | Dentro de la transacción de bootstrap, antes del `COMMIT`. | **Implementado** (`internal/persistence/postgres/bootstrap.go`). Es el único `world_events` que escribe el MVP. |
| Cambio de población | `city.update` con `population` y `populationLimit` | Al incorporar al jugador al mundo (`IntroducePlayer`). | Implementado como delta de red; **no** se registra en `world_events`. |
| Cambio de presencia / protección | `city.update` con `presenceState` | Transición aplicada por el game loop. Ver [presence.md](presence.md) §11. | Implementado como delta de red; **no** se registra en `world_events`. |

Registrar `CityCreated`, `CityPopulationChanged` y `CityProtectionEngaged` como filas propias de `world_events`,
y un bus de eventos de dominio con consumidores, es `TBD (fuera de MVP)`: la tabla y su repositorio
(`EventRepo.Append`) ya existen y son el punto de extensión.

## 12. Contratos de red

Fuente de verdad: esquemas Zod en `packages/protocol/src/v1/`, espejados en Go por
`internal/protocol/schema/v1/*.json`. `city.update` es un **delta parcial**: solo `id` es obligatorio y el
resto de los campos son opcionales, de modo que un cambio de presencia no reenvía la ciudad entera.

`city.update` (servidor→cliente), tal como lo define `CityUpdate` en `packages/protocol/src/v1/server.ts`:

```json
{
  "v": 1,
  "type": "city.update",
  "seq": 84,
  "ts": 1767830412100,
  "payload": {
    "id": 1042,
    "name": "alicepolis",
    "population": 3,
    "populationLimit": 20,
    "presenceState": "ONLINE",
    "protectionUntilMs": null
  }
}
```

El estado **completo** de una ciudad no viaja en `city.update` sino en `CityView`, dentro del array `cities[]`
de `world.snapshot`: `{ id, ownerPlayerId, name, centerX, centerY, era, population, populationLimit,
presenceState, protectionUntilMs }`.

Reglas de difusión:

- `city.update` se envía a **todas** las sesiones suscritas al chunk que contiene `(center_x, center_y)`
  (`Broadcaster.BroadcastChunk`), no solo al propietario: `presence_state` es información pública, porque es la
  que permite a un jugador saber que una ciudad está protegida antes de intentar una acción hostil.
- Se emite **solo ante cambio** de alguno de sus campos, nunca en cada tick. El estado inicial de la ciudad
  llega en `world.snapshot` al entrar en el área de interés.
- `id` es un entero JSON (`EntityId = z.number().int().min(1)`), no una cadena.
- `protectionUntilMs` es `null` mientras el jugador siga offline: en MVP la protección es indefinida y el campo
  queda previsto para límites futuros (canon §9). Al reconectar, `CityRepo.SetPresence` escribe
  `protection_until = NULL`.
- Los esquemas servidor→cliente **no** son estrictos: pueden ganar campos opcionales sin romper clientes
  antiguos. Los de cliente→servidor sí lo son (`additionalProperties: false`).
- El cliente **nunca** envía `city.update` ni ningún comando que altere la ciudad: los cinco mensajes
  cliente→servidor de la v1 son `session.hello`, `session.ping`, `session.view`, `unit.move` y
  `unit.cancel_move`.

## 13. Tests esperados

| ID | Nivel | Descripción | Cubre |
|---|---|---|---|
| `T-CITY-U-001` | unit | Validación de emplazamiento: rechaza `MOUNTAIN`, `WATER`, fuera de límites y tiles ya ocupados. | `RN-CITY-001..003` |
| `T-CITY-U-002` | unit | Distancia Chebyshev a ciudades existentes por debajo del mínimo → candidato rechazado. | `RN-CITY-004` |
| `T-CITY-U-003` | unit | La búsqueda en espiral con la misma semilla y el mismo mundo devuelve siempre el mismo tile (10 ejecuciones). | `RN-CITY-005` |
| `T-CITY-U-004` | unit | Mundo enteramente `WATER`: `FindSite` devuelve `ErrNoSite` en lugar de colocar la ciudad. | `RN-CITY-006` |
| `T-CITY-U-005` | unit | La fundación marca el 3×3 completo en el overlay, coloca los 3 aldeanos a distancia 2 y no toca el `TerrainType`. | `RN-CITY-007..009` |
| `T-CITY-U-006` | unit | `population_limit` se calcula desde la fila de `eras` inyectada; no hay literal 20 en el paquete de dominio. | `RN-CITY-014`, `RN-CITY-015` |
| `T-CITY-U-007` | unit | Con `population_limit` forzado a 3, un cuarto aldeano produce `POPULATION_LIMIT_REACHED` y no escribe nada. | `RN-CITY-017`, `RN-CITY-018` |
| `T-CITY-I-001` | integration | Bootstrap: fila de `cities` con `population = 3`, `population_limit = 20`, `presence_state = 'ONLINE'`. | `INV-CITY-002`, `INV-CITY-003`, `INV-CITY-009` |
| `T-CITY-I-002` | integration | `INSERT` sin `owner_player_id` o con uuid inexistente falla. | `INV-CITY-001` |
| `T-CITY-I-003` | integration | Una segunda ciudad con el mismo `(center_x, center_y)` falla por `cities_unique_center`. | `INV-CITY-008` |
| `T-CITY-I-004` | integration | `presence_state` con un valor no canónico falla por `cities_presence_state_valid`. | `INV-CITY-004` |
| `T-CITY-I-005` | integration | 50 altas consecutivas: ningún par de zonas urbanas comparte tile y todos los centros distan `>= 24`. | `INV-CITY-008`, `RN-CITY-004` |
| `T-CITY-S-001` | simulation | Con el mismo mundo y los mismos nombres de usuario, 200 altas producen exactamente el mismo conjunto de centros en dos ejecuciones. | `RN-CITY-005` |
| `T-CITY-R-001` | recovery | Reinicio del servidor: el overlay de ocupación reconstruido desde `cities` es idéntico al previo (hash por chunk). | `RN-CITY-010`, §10 |
| `T-CITY-R-002` | recovery | Hash de `world_chunks` idéntico antes y después de fundar ciudades. | `INV-CITY-010` |
| `T-CITY-C-001` | contract | `city.update` valida contra el JSON Schema exportado por `@empires-online/protocol`. | §12 |
| `T-CITY-C-002` | contract | `city.update` se emite solo ante cambio real de campos, no periódicamente. | §12 |

Los tests de integración y recovery requieren Docker Desktop iniciado y `EO_INTEGRATION=1` (canon §3, §19).
A día de hoy están **diseñados pero no ejecutados**: el daemon de Docker no arranca en la máquina de
desarrollo, así que no se afirma que pasen. Los de nivel unit y simulation del paquete
`internal/domain/city` sí están escritos y en verde (autómata de presencia completo,
`ShouldEngageProtection` con sus bordes y límite de población).
