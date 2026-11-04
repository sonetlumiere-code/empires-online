# ADR-030: El terreno del tile decide su recurso; un coste puede ser de varios recursos, y el avance de era es un encargo de la ciudad con su propia fila

Propósito: fijar cómo el slice de piedra ([SPEC-ECON-STONE](../specs/economy-stone.md)) añade un segundo recurso por
tile sin duplicar la capa del bosque, cómo se paga algo con dos recursos sin romper el débito con su efecto, y dónde
vive el avance de una ciudad a la Edad de Bronce.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-04** |
| Ámbito | `migrations/000012`, `internal/domain` (`deposit`, `economy`, `task`, `advance`), `internal/game/simulation` (`deposit.go`, `gather.go`, `economy.go`, `advance.go`), `internal/persistence/postgres` (`economy.go`), `internal/game/loadcheck`, `packages/protocol`, cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md) (cola por clave), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (toda escritura declara compensación), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (stock por deltas versionados, encargos en dos fases, compleción optimista), [ADR-025](ADR-025-forest-wood-layer.md) (la madera del bosque, capa aparte del terreno), [ADR-029](ADR-029-population-over-limit-and-house-destruction.md) (población por encima del límite) |

---

## Contexto

Hasta E8 la economía tiene un solo recurso, y el código lo da por hecho en tres sitios:

- **La capa del bosque** ([ADR-025](ADR-025-forest-wood-layer.md)) sólo sabe de tiles `FOREST`: el dominio
  `forest`, el mapa de la RAM, la tabla `forest_wood`, su barrido y su mensaje `forest.update`.
- **El stock de la RAM** es un campo `wood` por ciudad con sus depósitos sin volcar, y un coste
  (`economy.Cost`) es un recurso y una cantidad. El encargo lleva un delta versionado sobre una fila de
  `city_stocks` en la transacción de su efecto ([ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) §1).
- **La ciudad tiene una era** (`cities.era`, FK a `eras`) que sólo escribe el alta. El límite sale de ella
  (`RN-CITY-014`), pero nada la cambia.

La spec añade la piedra en los tiles `HILL`, recogida con la misma orden, y un primer gasto que paga en dos recursos:
`city.advance`, que lleva una ciudad de `STONE_AGE` a `BRONZE_AGE` en 90 s, sin entrenar mientras tanto.

## Decisión

### 1. Una capa de recurso por tile, con una instancia por terreno

La capa de ADR-025 deja de ser «del bosque» y pasa a ser **el recurso que da un tile**, descrito por sus parámetros:
el terreno que la da, el recurso, la capacidad, la regeneración y la búsqueda del tile siguiente. Hay dos
instancias, `FOREST → WOOD` y `HILL → STONE`, con los mismos valores (SPEC-ECON-STONE Q7) pero cada una con los
suyos. **El terreno del tile decide la capa**: un tile es bosque o colina, nunca las dos cosas, así que no hay
ambigüedad ni hace falta guardar el recurso por tile.

- En el dominio, el paquete `forest` se generaliza a `deposit`, con un `Layer` por terreno.
- En la RAM, un mapa por capa, con su barrido de regeneración y su mensaje (`forest.update`, `hill.update`).
- En la base, `forest_wood` no cambia y nace **`hill_stone`**, hermana, con la misma forma, su propio
  `CHECK (remaining BETWEEN 0 AND 100)` y el mismo SQL de delta de ADR-025 §2: la carga se descuenta en la
  transacción de la tarea, idempotente por la fase `WORKING` que consume, y la regeneración es perezosa.

### 2. La tarea lleva su recurso

`unit_tasks.kind` gana `GATHER_STONE`. El tipo de la tarea fija la capa de su tile de trabajo y la fila de stock que
recibe el depósito. La búsqueda del tile siguiente (`RN-ECON-022`) sólo mira tiles de la misma capa: un aldeano de
piedra nunca se pasa a un bosque. `unit.gather` decide el tipo por el terreno del objetivo y, antes de aceptar,
comprueba que el tile de trabajo está a una distancia de Chebyshev no mayor que `EO_PATHFINDING_MAX_DISTANCE` del
depósito (`RN-ECON-020`).

### 3. El stock es por recurso, y un coste es una lista

En la RAM, cada ciudad tiene un `economy.Stock` por recurso, cada uno con su versión y sus depósitos sin volcar: es
ADR-023 §1 aplicado fila a fila, porque `city_stocks` ya tenía una fila por `(ciudad, recurso)`.

- **El volcado** escribe un delta por recurso con depósitos pendientes, todos de una ciudad en una transacción.
  Su compensación devuelve cada delta a su recurso.
- **Un coste** pasa a ser una lista de `(recurso, cantidad)`. La casa, el aldeano y el guerrero siguen costando
  sólo madera; el avance es el primero con dos.
- **Reservar** comprueba que alcanzan **todos** los recursos antes de debitar ninguno, y sólo entonces los debita en
  la RAM y se lleva los depósitos sin volcar de cada uno.
- **La transacción** aplica un delta versionado por recurso. Si alguno no se aplica (la base tiene esa versión o una
  posterior) o dejaría un stock negativo, la transacción entera revierte: un coste se debita entero o no se debita
  ([INV-ECON-002](../invariants/economy.md#inv-econ-002)). La compensación devuelve todos los recursos y los
  depósitos que viajaban con ellos.

### 4. El avance vive en `city_era_advances`

`city_era_advances (city_id PK, from_era, to_era, completes_at_ms, request_id, created_at)`, con `from_era` y
`to_era` como FK a `eras (code)`. Una fila es un avance confirmado y en curso. **No** es una fila más de
`city_productions`: una producción reserva una plaza de población y la cuenta
[INV-CITY-017](../invariants/city.md#inv-city-017), y el avance no reserva nada.

El encargo es en dos fases, como `city.train`. La RAM valida y reserva; la transacción, con la clave de la ciudad:

- es idempotente por `request_id`: un reintento encuentra su fila y no hace nada más;
- lee la ciudad `FOR UPDATE` y exige `era = from_era` (si no, es un rechazo de dominio y no se debita nada);
- exige que no haya otro avance ni una producción en curso (`CITY_BUSY`);
- debita todos los recursos (§3), inserta la fila y escribe `EraAdvanceOrdered`.

`EconomyRepo.Train` gana la comprobación simétrica: con un avance en curso, `CITY_BUSY`. La exclusión la decide la
RAM, que es el único escritor; la base la repite como última barrera.

### 5. El fin es optimista, como la compleción de una casa

Al vencer `completes_at_ms` (fase 5), la RAM cambia la era de la ciudad y sube su límite en `cap(to) − cap(from)`,
difunde `city.update { era, populationLimit, advance: null }` y encola la escritura con la clave de la ciudad. Los
`population_cap` los lee la simulación del catálogo `eras` al arrancar, como la validación de carga: el número no
está en el código (`RN-CITY-015`).

La transacción hace `UPDATE cities SET era = to, population_limit = population_limit + cap(to) − cap(from),
version = version + 1 WHERE id = $1 AND era = from`, con los `cap` leídos de `eras`; borra la fila del avance y
escribe `EraAdvanced`. Sólo si la ciudad seguía en `from`: un reintento no suma dos veces. Usar un delta y no la
fórmula entera deja que una compleción de casa todavía en la cola sume su bono sin pisarse con el avance. Un fallo
definitivo se reencola en el siguiente volcado, como una compleción descartada (ADR-023 §5). Un avance que vence con
el servidor caído termina en el primer tick tras el arranque
([INV-ECON-005](../invariants/economy.md#inv-econ-005)).

### 6. La era nunca retrocede

Ningún escritor baja `cities.era`: el avance la sube y sólo si estaba en `from`. Lo registra
[INV-CITY-018](../invariants/city.md#inv-city-018). La destrucción de casas baja el límite
([ADR-029](ADR-029-population-over-limit-and-house-destruction.md)), nunca la era.

### 7. Migración `000012`

`STONE` en `city_stocks_resource_valid`, con su fila a cero para las ciudades que ya existen; `GATHER_STONE` en
`unit_tasks_kind_valid`; las tablas `hill_stone` y `city_era_advances`. Su `down` retira todo eso y borra las filas
de `STONE` y las tareas `GATHER_STONE`.

## Alternativas descartadas

| Alternativa | Por qué no |
|---|---|
| Una capa de yacimientos sembrada desde la semilla | Spec Q1: un generador más que verificar en cada arranque, como el terreno, y un dibujo nuevo, para algo que la colina ya da. |
| Generalizar `forest_wood` a una tabla por `(tile, recurso)` | Migra los datos de una tabla en uso a cambio de nada: un tile nunca tiene dos recursos. Además, una capacidad por recurso pediría un `CHECK` con `CASE`. |
| Duplicar la capa del bosque para la colina | Dos copias de la carga, la regeneración, la búsqueda y la vista que divergirían con el primer arreglo. |
| El avance como fila de `city_productions` | `unit_type NOT NULL`, la plaza de población que el avance no reserva e [INV-CITY-017](../invariants/city.md#inv-city-017), que cuenta las producciones en curso. |
| El avance como columnas de `cities` (`advancing_to`, `advance_completes_at_ms`, `advance_request_id`) | Mezcla un encargo idempotente con la fila más escrita de la base y con su `version`; una tabla aparte sigue el patrón de los demás encargos. |
| Pagar en dos recursos con dos escrituras | Una caída entre las dos dejaría la mitad del coste pagada y nada encargado: es lo que [INV-ECON-002](../invariants/economy.md#inv-econ-002) prohíbe. |
| Escribir la era y la fórmula entera del límite al terminar | Si una compleción de casa de la misma ciudad está todavía en la cola, la fórmula y el delta se pisan; con deltas, cada escritor suma lo suyo. |

## Consecuencias

### Positivas

- La piedra reutiliza entero el camino de la madera: tarea, depósito, volcado, encargos y regeneración. Un tercer
  recurso de terreno sería una instancia más de la capa.
- Ningún coste puede quedar a medias, aunque pague en varios recursos.
- El límite sigue siendo derivado ([INV-ECON-004](../invariants/economy.md#inv-econ-004)): la validación de carga ya
  lo calcula con la era de cada ciudad.

### Negativas

- El dominio `forest` cambia de nombre y de forma, y con él sus tests: es un cambio amplio para una capa que ya
  funciona. Lo protege el golden «tala», que no cambia.
- Dos tablas de encargo (`city_productions` y `city_era_advances`) comparten la regla «una cosa a la vez», que la
  base sólo comprueba dentro de cada transacción.

### Neutras

- `city.update` y `CityView` ganan `era` y `advance`; el protocolo, `hill.update` y `city.advance`. Todo aditivo en v1.
