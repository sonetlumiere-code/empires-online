# ADR-050: Una ciudad cae por un asalto durable, y su caída la arrasa y refunda a su dueño en una sola transacción

Propósito: fijar cómo el slice de la caída de una ciudad ([SPEC-CITY-FALL](../specs/city-fall.md)) hace que una ciudad
deje de existir sin cambiar nunca el dueño de una fila de `cities`, sin dejar en ningún instante a un jugador con cero
ciudades ni con dos, y sin poner una búsqueda de emplazamiento dentro del tick.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-23** |
| Ámbito | `internal/domain/city` (la caída, la inmunidad), `internal/domain/siege` (el asalto), `internal/game/simulation` (`unit.assault`, el plazo en la fase 5, la aplicación de la caída), `internal/game/founding` (la distancia mínima de la refundación), `internal/persistence/postgres` (la transacción de la caída, la migración `000030`, las consultas de «la ciudad del jugador»), `internal/protocol`, `packages/protocol`, `apps/web` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-016](ADR-016-foreign-territory-actions.md) (acción hostil), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (el buzón del loop), [ADR-028](ADR-028-event-log-from-world-events.md) (eventos con el otro jugador), [ADR-029](ADR-029-population-over-limit-and-house-destruction.md) (población por encima del límite), [ADR-034](ADR-034-territory-claims.md) (la reclamación como encargo durable y la captura en dos fases), [ADR-045](ADR-045-clan-wars.md) (el marcador en los eventos), [ADR-048](ADR-048-siege-engines-and-wall.md) (la ciudad en brecha) |

---

## Contexto

Producto aceptó el 2026-11-23 las diez recomendaciones de la spec: una ciudad cae por un **asalto** (una unidad que
derriba sostiene el estandarte 900 s junto a una ciudad en brecha); la ciudad caída **se arrasa** y nadie se la queda;
su dueño **refunda en el acto**, lejos del vencedor, con su era, sus tecnologías y todo lo que es del jugador, y pierde
lo que había en la ciudad; el almacén arde; la ciudad recién fundada o refundada es inmune 72 h; sólo cae desde la Edad
de Bronce; la protección del asaltado no rompe un asalto empezado.

Lo que ya existe y condiciona la forma ([SPEC-CITY-FALL](../specs/city-fall.md) §1.1):

- `cities.owner_player_id` sólo lo escribe el alta (`RN-CITY-020`), y la simulación deriva de «la ciudad del jugador»
  (`State.CityOf`, que indexa una por dueño) la civilización, el bando, la protección del atacante, la vista y el destino
  de una caravana capturada. Un jugador sin ciudad o con dos rompe esas rutas.
- `trade_offers`, `caravans`, `market_offers` y `territory_claims` referencian `cities (id)` sin cascada.
- La reclamación de territorio ([ADR-034](ADR-034-territory-claims.md)) ya resuelve un encargo durable con plazo que se
  rompe matando a quien lo sostiene, sobrevive a un reinicio y termina en una orden en dos fases.
- El alta busca el emplazamiento fuera del tick (`internal/httpapi`, `founding.FindSite`, de 4 a 61 ms con el alcance
  de E30) y lo vuelve a comprobar dentro de la transacción bajo el cerrojo de fundación (`founding.FarEnough`,
  [INV-CITY-008](../invariants/city.md#inv-city-008)); la RAM conoce al jugador cuando la transacción confirma
  (`IntroducePlayer`).

## Decisión

### 1. Arrasar, no traspasar

Una ciudad caída **no cambia de dueño**: `owner_player_id` sigue inmutable y `RN-CITY-020` no cambia. La migración
`000030` añade `cities.fallen_at timestamptz NULL`; la caída lo escribe y la fila se queda, con su historia apuntándola.
Una ciudad con `fallen_at` no se carga en la RAM, no ocupa su 3 × 3 en el overlay y no cuenta para ninguna consulta de
«la ciudad del jugador» (`GetByOwner`, el `DISTINCT ON` de mensajes, registro y clanes, que filtran `fallen_at IS NULL`).
`cities_unique_center` pasa a índice único parcial sobre las ciudades en pie, para que otra pueda fundar donde estuvo.

### 2. El asalto es un encargo durable, como una reclamación

La tabla `city_assaults` (`id`, `city_id`, `unit_id`, `player_id`, `started_at`, `ends_at`, `status` `ACTIVE`, `FELL` o
`BROKEN`), con un único parcial de un `ACTIVE` por ciudad, sigue el patrón de `territory_claims`: el asalto nace en dos
fases por la cola (la RAM lo da por empezado al ordenar y la fila lo confirma), se rompe con una escritura en la clave
de la ciudad asaltada, y al arrancar se carga con su plazo; un plazo vencido con el servidor parado se aplica en el
primer tick. La unidad está `CLAIMING`, el valor que ya existe para quien sostiene un estandarte.

En cada tick, en la fase 5, el asalto vuelve a pasar el predicado de agresión de la ciudad (`cityAttackBlocked`, el de
saqueo y derribo) con **una asimetría**: la protección offline del asaltado sólo cuenta al ordenar; la del asaltante y
su presencia cuentan en cada tick. La ciudad que vuelve a estar amurallada (`State.Walled`) también lo rompe, por el
mismo camino que `RN-SIEGE-006`.

### 3. La caída y la refundación, en una transacción en dos fases

Al vencer el plazo, la simulación marca la ciudad como **cayendo** (no admite órdenes que la cambien, ni del dueño ni
contra ella) y encola un trabajo `CityFall` con las claves de la ciudad caída y de la ciudad del vencedor. El trabajo,
fuera del tick:

1. Toma el cerrojo de fundación y comprueba que el asalto sigue `ACTIVE` y la ciudad sin `fallen_at` (un `BROKEN`
   encolado antes en la misma clave ya se escribió).
2. Busca el sitio de la ciudad nueva con `founding.FindSite`, sobre las ciudades en pie salvo la caída, con las reglas
   del alta y una condición más: a ≥ `EO_CITY_REFOUND_MIN_DISTANCE` de la ciudad caída y de la del vencedor. Si ningún
   sitio la cumple, se repite sin ella, como `RN-CITY-024` (el jugador nunca se queda sin ciudad). Lo vuelve a
   comprobar con `FarEnough` bajo el cerrojo.
3. En **una** transacción: escribe `fallen_at`, los edificios a `DESTROYED`, el asalto a `FELL` y el territorio capital
   sin dueño; crea la ciudad nueva (mismo nombre, la era de la caída, almacén a cero) y sus tres aldeanos; reasigna a
   ella las unidades del jugador y resuelve tratos, ofertas, custodia, caravanas y guarniciones según la spec §4.3; y
   escribe `CityFell` con `counterpart_player_id`, `atMs` y, si hay guerra entre los clanes, `war` con sus puntos
   ([ADR-045](ADR-045-clan-wars.md) §2).
4. Devuelve el resultado al buzón del loop ([ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md)).

La RAM, al recibirlo, quita la ciudad caída (`cities`, `cityByOwner`, overlay, índice por chunk) y añade la nueva por
el camino de `IntroducePlayer` (incluido el apartado de unidades de `RN-CITY-022`) en el mismo comando: entre dos ticks
el jugador pasa de una ciudad a otra, nunca por cero ni por dos. Si la transacción falla de forma definitiva o la
comprobación del punto 1 la rechaza, la ciudad deja de estar cayendo y sigue en pie; una caída antes del `COMMIT`
pierde la caída, nunca la deja a medias.

### 4. Una ciudad en pie por jugador, en la base

`cities_one_standing_per_owner`, índice único parcial sobre `(owner_player_id) WHERE fallen_at IS NULL`, convierte «un
jugador, una ciudad» de nota del dominio ([city.md](../specs/city.md) §9) en garantía de la base para las ciudades en
pie. La transacción de la caída tiene que escribir `fallen_at` antes del `INSERT` de la nueva, y un error de orden
sería un fallo de la transacción, no un jugador con dos ciudades.

### 5. El vencido recibe su mundo nuevo sin reconectar

Tras aplicar la caída, la simulación manda `city.fallen` al área de la ciudad caída y, a cada sesión del vencido, un
`world.snapshot` centrado en su ciudad nueva, por el mismo camino que el snapshot del handshake. El cliente sustituye su
mundo como al conectar.

## Alternativas descartadas

1. **Traspasar la ciudad** (`UPDATE cities SET owner_player_id`): rompe `RN-CITY-020`, deja al vencedor con dos ciudades
   y al vencido con cero, y obliga a revisar cada `CityOf` que deriva civilización y bando de la ciudad.
2. **Borrar la fila de la ciudad**: cuatro tablas la referencian sin cascada, y la historia (tratos, caravanas,
   reclamaciones, eventos) perdería su ciudad.
3. **Refundar en un trabajo aparte, después de la caída**: entre los dos el jugador no tiene ciudad, y una caída del
   servidor en medio lo deja así.
4. **Buscar el sitio en el tick**: de 4 a 61 ms por búsqueda dentro de un tick de 100 ms.
5. **Una vida para el Centro Urbano**: un edificio nuevo en el centro, que ya ocupa la muralla, y un daño que se olvida
   con un reinicio; un plazo durable se recupera igual que una reclamación.
6. **Cerrar las sesiones del vencido** para que reconecten con el snapshot nuevo: el cliente lo trataría como un fallo de
   red y perdería su estado de interfaz.

## Consecuencias

- `cities` tiene filas que ya no son ciudades del mundo: toda consulta nueva que busque la ciudad de un jugador filtra
  `fallen_at IS NULL`, y el test de integración de las consultas lo comprueba.
- La familia `INV-CITY` suma dos invariantes ([INV-CITY-020](../invariants/city.md#inv-city-020), una ciudad en pie por
  jugador; [INV-CITY-021](../invariants/city.md#inv-city-021), un asalto activo por ciudad) y `INV-COMBAT` uno
  ([INV-COMBAT-010](../invariants/combat.md#inv-combat-010), sólo cae por un asalto válido hasta el final).
- La capital deja de ser intocable para siempre: lo es mientras su ciudad está en pie
  ([INV-TERR-011](../invariants/territory.md#inv-terr-011)).
- La caída es la escritura más ancha del proyecto (ciudades, edificios, unidades, tratos, ofertas, caravanas, guarniciones,
  territorio y eventos en una transacción); es rara y de alta consecuencia, como las demás escrituras de `cities`.
- El multiciudad, cuando llegue, partirá de una base que ya distingue ciudades en pie y caídas, y tendrá que relajar
  `cities_one_standing_per_owner` con su propio ADR.
