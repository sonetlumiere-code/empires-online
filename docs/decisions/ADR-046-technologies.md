# ADR-046: Las tecnologías son un catálogo en base cuyos efectos son rasgos por mil, compuestos con la civilización en un único punto

Propósito: fijar cómo el slice del árbol tecnológico ([SPEC-TECHNOLOGIES](../specs/technologies.md)) guarda el
catálogo y las tecnologías de cada jugador, cómo se investiga sin inventar un encargo nuevo y cómo los efectos llegan a
la simulación sin que ningún sistema pregunte por un código de tecnología.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-18** |
| Ámbito | `internal/domain/technology` (el catálogo, su validación, la composición y la orden), `internal/domain/player` (las dos claves nuevas), `internal/game/simulation` (las tecnologías de cada jugador, `State.trait` compuesto, `city.research`, el vencimiento y los dos puntos de consumo nuevos), `internal/persistence/postgres` (el encargo, el fin, la carga y el catálogo), `internal/game/loadcheck`, `internal/protocol`, `packages/protocol` (`city.research`, `research` y `technologies`, `technologies.json`), `apps/web` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (ninguna escritura se pierde en silencio), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (encargos en dos fases y débito con su efecto), [ADR-030](ADR-030-stone-layer-and-era-advance.md) (el avance de era como encargo con su fila y fin optimista), [ADR-035](ADR-035-civilization-traits.md) (los rasgos por mil en el punto de consumo), [ADR-036](ADR-036-civilization-goods.md) (los bienes ajenos en cualquier mezcla) |

---

## Contexto

Producto aceptó el 2026-11-18 las quince recomendaciones de la spec: doce tecnologías (ocho comunes, dos por era, y
una exclusiva por civilización desde Hierro), del jugador, privadas y permanentes; se investigan en la ciudad, una a
la vez y nunca durante un avance de era, pagando madera, piedra y, en Hierro y Castillos, bienes ajenos (la exclusiva,
su bien propio); sus efectos son las cinco claves de los rasgos de civilización y dos nuevas, el daño de un golpe y la
carga de un aldeano, y no abren unidades, edificios ni eras.

Lo que ya existe:

- **Los rasgos de civilización** son enteros por mil en `civilizations.traits`, validados al cargar, y la simulación
  los pide en un único sitio, `State.trait(jugador, clave)`, que usan los cinco puntos de consumo con
  `player.ScaleDuration` o `player.ScaleAmount` al dar la orden ([ADR-035](ADR-035-civilization-traits.md)).
- **El avance de era** es un encargo en dos fases con su fila (`city_era_advances`), un trabajo con la clave de la
  ciudad, un fin optimista con su reencolado y la carga de la fila al arrancar ([ADR-030](ADR-030-stone-layer-and-era-advance.md)).
- **Los catálogos de unidades y edificios** viven en Go; el de civilizaciones vive en la base con un catálogo de
  presentación (`civilizations.json`) que un test de integración compara con la tabla.

El roadmap pide que los efectos de las tecnologías sean «datos en base, nunca condicionales en el código del dominio».

## Decisión

### 1. El catálogo vive en la base, y el dominio lo valida al cargar

La tabla `technologies` guarda cada tecnología: código único, nombre, era (`eras.code`), civilización (nula para las
comunes), costes fijos en `jsonb` (`{"WOOD": 150, "STONE": 50}`), bienes ajenos, bien propio, plazo en milisegundos,
requisitos (códigos) en `jsonb` y efectos (`{"clave": por mil}`) en `jsonb`. La migración `000027` la siembra con las
doce de la spec; un cambio de balance es una migración de catálogo. `internal/domain/technology` la carga en un
`Catalog` inmutable y la valida: toda clave de efecto es una del catálogo de rasgos (`player.TraitKeys`) con un valor
positivo; todo requisito existe, no es exclusivo de otra civilización y no pide una era posterior; no hay ciclos; una
exclusiva pide su bien propio y ningún bien ajeno, y una común, al revés. Un catálogo inválido impide arrancar
([INV-TECH-002](../invariants/technology.md#inv-tech-002)), como un rasgo inválido.

El catálogo de presentación (`technologies.json`, exportado desde `packages/protocol`) copia nombres, efectos, costes,
plazos y requisitos para el cliente, y un test de integración lo compara con la tabla.

### 2. Los efectos son rasgos por mil, y `State.trait` los compone con la civilización

Las dos claves nuevas se añaden al catálogo de rasgos de `internal/domain/player`: `combat.attack_damage` y
`economy.gather_carry`, las dos de cantidad. Ninguna civilización las declara hoy, así que valen 1000 para todas.

`State.trait(jugador, clave)` deja de leer sólo la civilización: devuelve el rasgo de la civilización compuesto con el
efecto de cada tecnología del jugador que lleva esa clave, en orden de código, con
`technology.Compose(r, e) = (r × e + 500) / 1000` (mínimo 1) en cada paso. El resultado se guarda por jugador en la RAM
al cargar y al terminar cada investigación, no se recalcula en cada consulta. Los cinco puntos de consumo no cambian,
y los dos nuevos usan el mismo camino:

- **El daño de un golpe** de una unidad que ataca, contra una unidad o contra un edificio, es
  `ScaleAmount(def.AttackDamage, trait(dueño, combat.attack_damage))`, medido al calcular el golpe. El disparo del
  castillo no lo lee.
- **La carga de un aldeano** es `ScaleAmount(def.GatherCarry, trait(dueño, economy.gather_carry))`, medida al
  terminar el trabajo en el tile, cuando toma su carga del recurso del tile.

Ningún sistema pregunta por un código de tecnología: sólo por una clave.

### 3. La investigación es un encargo con su fila, como el avance de era

`city.research { cityId, technology }` sigue el camino de ADR-030: el tick valida con `technology.CanResearch` (ciudad
propia, tecnología del catálogo y al alcance de su civilización, no la tiene, sus requisitos sí, la era de la ciudad,
ninguna investigación ni avance en curso, el almacén cubre el coste) y reserva el coste con los depósitos sin volcar;
el trabajo `city.research` (clave de la ciudad) lo debita y escribe la fila de `city_research` en una transacción; la
respuesta confirma o devuelve la reserva. El plazo se fija al ordenar con `production.speed` compuesto.

El fin es optimista: en la fase 5, en orden de ciudad, una investigación confirmada que venció añade la tecnología al
jugador en la RAM, recompone sus rasgos, manda `city.update { research: null, technologies }` y encola el trabajo
`city.research_complete`, que inserta la fila de `player_technologies`, borra la de `city_research` y escribe
`TechnologyResearched` en `world_events`, idempotente y con su reencolado en el volcado si se descarta.

Investigar no bloquea entrenar, construir ni elaborar; un avance de era y una investigación se excluyen en los dos
sentidos (`CITY_BUSY`).

Para el humo, `EO_TECHNOLOGY_RESEARCH_SECONDS` (0 por defecto) sustituye el plazo del catálogo en desarrollo.

### 4. La migración `000027`

- `technologies`, sembrada; `city_research` (clave primaria `city_id`, tecnología, `completes_at_ms`, `request_id`);
  `player_technologies` (`(player_id, technology_id)` única, `researched_at`).
- El trigger `city_research_guard` rechaza con `23514` una investigación cuya ciudad avanza de era, cuyo dueño ya
  tiene la tecnología o no tiene sus requisitos, cuya era es posterior a la de la ciudad o que es exclusiva de otra
  civilización; `city_era_advances_research_guard` rechaza un avance en una ciudad que investiga; y
  `player_technologies_civ_guard` rechaza una exclusiva de otra civilización
  ([INV-TECH-001](../invariants/technology.md#inv-tech-001)).

La carga lee el catálogo, las tecnologías de los dueños de ciudad y las investigaciones en curso; la validación de carga
descarta una investigación que no cumpla [INV-TECH-001](../invariants/technology.md#inv-tech-001) y lo registra.

### 5. La vista

`CityView` y `city.update` ganan `research` (`{ technology, completesAtMs }` o `null`) y `technologies` (los códigos
del jugador), opcionales y sólo en la vista del dueño, como `stocks` y `production`. No hay valores nuevos en ningún
enum estricto: `technology` es un código de catálogo que valida el servidor.

## Alternativas descartadas

- **El catálogo en Go, como el de unidades.** Más simple de leer y de testear, pero contradice el roadmap y obliga a
  recompilar para cambiar un número de balance que la spec deja en una migración (Q15).
- **Una tabla puente `civilization_technologies`**, como dibujaba el esquema de diseño. Para una exclusiva por
  civilización basta una columna nula; la tabla puente sólo serviría para tecnologías de varias civilizaciones, que
  ningún slice pide.
- **Sumar los efectos en vez de multiplicarlos.** `1100 + 1100 − 1000` es más fácil de explicar, pero deja de componer
  igual con el rasgo de la civilización, que ya es un factor.
- **Recalcular el rasgo compuesto en cada consulta.** Cada paso de una polilínea, cada golpe y cada viaje lo pedirían;
  guardarlo por jugador cuesta un mapa pequeño que cambia doce veces como mucho en la vida de un jugador.
- **Aplicar los efectos también a lo que está en curso** (una ruta, una obra). Rompe la regla de ADR-035: lo que tiene
  plazo lo mantiene.

## Consecuencias

- La simulación gana un estado por jugador (sus tecnologías y su rasgo compuesto) y un encargo más por ciudad; el
  camino caliente sigue leyendo un mapa por consulta.
- Un cambio de balance de una tecnología es una migración de catálogo que también toca `technologies.json`, o la CI
  falla.
- Que una tecnología abra unidades, edificios o eras, o que se comparta con el clan, no cabe en esta forma sin otra
  decisión; el catálogo en base y la composición en `State.trait` se mantendrían.
