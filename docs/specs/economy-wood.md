# Economía: madera (primer slice)

Propuesta del primer verbo productivo del juego: un aldeano recolecta madera en un bosque, la deposita
en su ciudad, y con ella se construye una casa y se entrena otro aldeano. Un recurso, una orden, un
almacén, un edificio, una producción; nada más.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-ECON-WOOD` |
| Estado | Accepted (2026-10-27; las preguntas de §10 quedaron decididas ese día). Nada implementado todavía. |
| Milestone | E1 Madera (primero después de M7; la implementación lo añade a milestones.md) |
| Canon | §12 (persistencia), §16 (errores) |
| Depende de | [unit.md](unit.md), [city.md](city.md), [movement.md](movement.md), [presence.md](presence.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | [ADR-013](../decisions/ADR-013-persistence-pipeline.md) (cola por entidad y compensaciones), [ADR-022](../decisions/ADR-022-durable-write-retry-and-loop-inbox.md) (toda escritura durable declara compensación). Un ADR nuevo sólo si la tarea persistente de §4.2 se adopta como patrón general. |
| Reemplaza a | — |
| Invariantes | Asigna `INV-ECON-001` a `INV-ECON-006` ([../invariants/economy.md](../invariants/economy.md)) e `INV-CITY-017`, que sustituye al derogado `INV-CITY-009` (§8) |

## 1. Objetivo

Que el jugador tenga algo que hacer durante más de dos minutos y una razón para volver mañana: mandar
aldeanos a por madera, gastarla en una casa que sube el tope de población, y entrenar aldeanos con los
que recolectar más. Es el «expandir» del bucle de la visión, en su versión más fina, y la primera carga
real sobre la fase de temporizadores, la cola de persistencia y el presupuesto de mensajes.

## 2. Scope

**Dentro**

- Un recurso, `WOOD`, almacenado por ciudad en `city_stocks`.
- Una orden de red, `unit.gather { unitId, target }`, que pone a un `VILLAGER` a hacer viajes bosque → ciudad.
- Un estado nuevo de unidad, `WORKING`: parada sobre el tile de recurso hasta que vence su temporizador.
- Un edificio, `HOUSE`, dentro de la zona urbana 3×3, con coste en madera, tiempo de construcción y +5 de
  límite de población al completarse. Usa el hueco que [city.md](city.md) §2 dejó para «modificadores de
  `population_limit` por edificios».
- Una producción, `city.train { cityId, type: VILLAGER }`, con coste en madera y tiempo; una a la vez por ciudad.
- Recuperación tras reinicio de viajes, construcciones y entrenamientos a medias, con el mismo patrón que los
  movimientos: tiempos absolutos persistidos, nada que se «recalcule» al arrancar.

**Fuera (de este slice, no del juego)**

- Otros recursos, agotamiento del bosque, almacén con capacidad, mercado, comercio, tecnologías, eras.
- Aldeanos constructores (la casa se construye sola con temporizador), cola de producción, más de un edificio.
- Edificios fuera de la zona urbana: evitarlo deja intactos el pathfinding y el desplazamiento de unidades.
- Robar o destruir madera: llegará con el slice de riesgo, que es el siguiente.

## 3. Actores

Jugador (ordena), cliente web (muestra el stock y el estado `WORKING`), game server (decide todo: ruta,
tiempos, cantidades, costes), PostgreSQL (stocks, tareas, edificios, producciones), tick loop (fase 3 para
los tramos de movimiento, fase 5 para temporizadores, fase 8 para el volcado de stocks).

## 4. Modelo

### 4.1 Stock

`city_stocks (city_id, resource, amount, version)` con clave `(city_id, resource)`, `resource IN ('WOOD')` y
`amount >= 0` como `CHECK`. Una fila por ciudad y recurso, creada a cero en la fundación. En RAM vive en el
índice de ciudades; se persiste por *dirty-flag* (§9), no por escritura inmediata: cambia con cada depósito y
no es un hecho irreversible.

### 4.2 Tarea de recolección

Una **tarea** es lo que una unidad está haciendo a lo largo de varios tramos. Hoy no existe: una unidad
tiene como mucho un movimiento. La tarea `GATHER_WOOD` encadena tramos y esperas:

```
TO_RESOURCE (movimiento) → WORKING (workMs) → TO_DEPOSIT (movimiento) → depósito → TO_RESOURCE …
```

`unit_tasks (unit_id PK, kind, target_x, target_y, phase, phase_until_ms, carrying, trips_done,
created_at)`, con `phase IN ('TO_RESOURCE','WORKING','TO_DEPOSIT')`. La clave primaria en `unit_id`
materializa «una unidad, una tarea». Los tramos de movimiento son movimientos normales de
[movement.md](movement.md), con su polilínea en `unit_movements`; la tarea sólo decide qué hacer cuando un
tramo termina (fase 3, llegadas) o cuando vence `phase_until_ms` (fase 5).

| Regla | Enunciado |
|---|---|
| `RN-ECON-001` | `unit.gather` sólo acepta un `VILLAGER` propio, vivo, no guarnecido, y un `target` dentro del mundo cuyo terreno sea `FOREST`. El bosque no se agota en este slice. |
| `RN-ECON-002` | El tile de trabajo es el propio tile `FOREST` (es transitable, coste 16). Si no hay ruta desde la unidad, `PATH_NOT_FOUND`; si la hay pero supera `EO_PATHFINDING_MAX_DISTANCE`, `PATH_TOO_LONG`, como un `unit.move`. |
| `RN-ECON-003` | Al llegar, la unidad pasa a `WORKING` durante `workMs` y al vencer carga `carry` unidades. Mientras trabaja no se mueve ni se oculta; una orden de movimiento, guarnición o `unit.cancel_move` cancela la tarea y **pierde lo cargado**. |
| `RN-ECON-004` | El depósito es el primer tile transitable adyacente a la zona urbana de la ciudad de la unidad (`units.city_id`), elegido con el mismo criterio determinista que el emplazamiento de los aldeanos iniciales. Al llegar, `amount += carrying`, `carrying = 0`, `trips_done += 1`. |
| `RN-ECON-005` | **La tarea sólo empieza un viaje nuevo si la ciudad está `ONLINE`.** Con la ciudad en `OFFLINE_PENDING` o `PROTECTED`, el viaje en curso se termina (se deposita lo cargado) y la unidad queda `IDLE` en el tile de depósito. Es la lectura de «no es un idle game» ([vision.md](../product/vision.md) §7): la ausencia no acumula recursos. Ver pregunta abierta Q1. |
| `RN-ECON-006` | Una nueva orden de `unit.gather` sobre una unidad que ya recolecta sustituye la tarea (cancela tramo y carga) en la misma transacción, igual que una orden de movimiento sustituye a la anterior. |

### 4.3 Casa

`buildings (id, city_id, type, x, y, status, completes_at_ms, created_at)`, `type IN ('HOUSE')`,
`status IN ('UNDER_CONSTRUCTION','COMPLETE')`, `UNIQUE (x, y)`.

| Regla | Enunciado |
|---|---|
| `RN-ECON-007` | `city.build { cityId, type: HOUSE, x, y }` exige ciudad propia, tile dentro de la zona urbana 3×3 distinto del centro y sin edificio (`UNIQUE (x, y)`), y `amount >= cost`. Ocho huecos por ciudad: suficiente para el tope de 20 de `STONE_AGE`. |
| `RN-ECON-008` | El coste se **debita en la misma transacción** que inserta el edificio; si la escritura falla, el stock en RAM se restaura con una compensación que vuelve al loop (patrón de ADR-013). Nunca hay madera gastada sin edificio ni edificio sin madera gastada. |
| `RN-ECON-009` | Al vencer `completes_at_ms` (fase 5), `status = COMPLETE` y `population_limit += 5`, escritos juntos en la fila de la ciudad y del edificio. `population_limit = eras.population_cap + 5 × casas completas`, siempre. |

### 4.4 Entrenar un aldeano

`city_productions (city_id PK, unit_type, completes_at_ms, created_at)`: una producción por ciudad.

| Regla | Enunciado |
|---|---|
| `RN-ECON-010` | `city.train { cityId, type: VILLAGER }` exige ciudad propia, sin producción en curso (`CITY_BUSY`), `population + 1 <= population_limit` (`POPULATION_LIMIT_REACHED`) y `amount >= cost` (`INSUFFICIENT_RESOURCES`). La población se **reserva al ordenar**: `population += 1` en la misma transacción que el débito y la inserción. |
| `RN-ECON-011` | Al vencer `completes_at_ms`, nace un `VILLAGER` `IDLE` en el primer tile libre a `spawnRadius` del centro, como los tres iniciales, con `entity.spawn` al chunk; si no hay tile libre, espera al siguiente tick (misma espera que la salida de guarnición, RN-GARR-014). |
| `RN-ECON-012` | Cancelar una producción no está en el slice: una vez ordenada, termina. |

### 4.5 Valores iniciales

Son parámetros de equilibrio, no decisiones de diseño: viven en los catálogos en código (`unit.Definition`,
`building.Definition`) como hoy `BaseMsPerTile`, y pasarán a tablas cuando haya más de un recurso o edificio.

| Parámetro | Valor | Por qué |
|---|---|---|
| `workMs` | 10 000 ms | Un viaje a un bosque a 10 tiles dura ~35 s: tres viajes por minuto por aldeano, visible sin ser frenético. |
| `carry` | 10 madera | Con tres aldeanos, ~90 madera/min: una casa cada 20 s de atención, un aldeano cada 35 s. |
| `HOUSE` | coste 30, 15 s, +5 población | La primera decisión del jugador llega antes del minuto. |
| `VILLAGER` | coste 50, 20 s | Más caro que la casa para que el orden casa → aldeano sea una elección. |

## 5. Errores

| Código | Condición exacta | Estado en el catálogo |
|---|---|---|
| `UNIT_NOT_FOUND`, `UNIT_NOT_OWNED`, `UNIT_DEAD`, `UNIT_GARRISONED` | Como en `unit.move` | Existente |
| `INVALID_TARGET` | `target` no es `FOREST`; o `(x, y)` fuera de la zona urbana, es el centro, o ya tiene edificio | Existente |
| `TARGET_OUT_OF_BOUNDS`, `PATH_NOT_FOUND`, `PATH_TOO_LONG` | Como en `unit.move`, para el tramo al bosque | Existente |
| `CITY_NOT_FOUND` | `cityId` inexistente o ajeno (misma respuesta: no se revela existencia) | Existente |
| `POPULATION_LIMIT_REACHED` | `population + 1 > population_limit` al ordenar `city.train` | Existente |
| `INSUFFICIENT_RESOURCES` | `amount < cost` al ordenar `city.build` o `city.train` | **Nuevo** |
| `CITY_BUSY` | `city.train` con una producción en curso | **Nuevo** |

Dos códigos nuevos en `packages/protocol/src/v1/errors.ts` y en `internal/protocol/codes.go`; el test de
catálogo (`TestCatalogoDeErroresCoincideConElExportado`) exige que coincidan.

## 6. Eventos y contratos de red

Eventos de dominio: *tarea iniciada*, *carga completada*, *madera depositada*, *tarea terminada* (con
motivo: cancelada, ciudad ausente, sustituida), *edificio encargado*, *edificio completado*, *producción
encargada*, *aldeano entrenado*.

| Dirección | Mensaje | Cambio |
|---|---|---|
| C→S | `unit.gather { unitId, target: {x, y} }` | **Nuevo**, estricto, con `requestId` e idempotencia como `unit.move`. |
| C→S | `city.build { cityId, type, x, y }` y `city.train { cityId, type }` | **Nuevos**. |
| S→C | `system.error { requestId, code }` | Rechazo de los tres, como hoy para la guarnición. La aceptación se observa en los mensajes de estado; no hay `*.accepted` nuevos. |
| S→C | `entity.update` | `status: WORKING` (valor nuevo de `UnitStatus`) y `carrying` opcional. |
| S→C | `city.update` | Campo opcional `stocks: { WOOD: n }` y los `population`/`populationLimit` existentes. Un depósito emite un `city.update` al chunk de la ciudad: con `carry = 10` son ~3 por minuto y aldeano, no uno por tick. |
| S→C | `building.spawn { id, cityId, type, x, y, status, completesAtMs }` y `building.update { id, status }` | **Nuevos**; `world.snapshot` incorpora `buildings[]`. |
| S→C | `unit.movement.started/completed` | Sin cambio: los tramos son movimientos normales, y el cliente los interpola igual. |

Los esquemas Zod son la fuente ([ADR-009](../decisions/ADR-009-shared-protocol-package.md)); los payloads de
esta tabla son orientativos hasta que existan allí.

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | Stocks, tareas con su fase y `phase_until_ms`, edificios, producciones, `carrying`. |
| Write-through | Inicio, cambio de fase y fin de una tarea; encargo y finalización de un edificio y de una producción; y **cada débito de madera junto con su efecto** (RN-ECON-008, RN-ECON-010), con `requestId` único en la fila del efecto para la idempotencia (ADR-014). |
| Dirty-flag + flush | `city_stocks.amount` tras un depósito: se pierde como mucho el último flush (5 s) de depósitos, nunca un débito. `units.x/y` como hoy. |
| Reconstruible | La posición durante un tramo (polilínea); el `population_limit` (fórmula de RN-ECON-009, verificada por `loadcheck` al arrancar); la fase vencida durante una caída: al hidratar, una tarea con `phase_until_ms` pasado avanza su máquina de estados igual que un movimiento con llegada vencida, y un edificio o producción con `completes_at_ms` pasado se completa. |

Migración `000006`: `city_stocks`, `unit_tasks`, `buildings`, `city_productions`, el valor `WORKING` en
`units_status_valid`, y el `down` que las retira.

## 8. Invariantes

Esta spec **asigna** la familia `INV-ECON` (`001` a `006`), registrada en
[../invariants/economy.md](../invariants/economy.md) antes de escribir código, e
[INV-CITY-017](../invariants/city.md#inv-city-017), que sustituye a
[INV-CITY-009](../invariants/city.md#inv-city-009): la reserva de población de `RN-ECON-010` hace que
`population` cuente también las producciones en curso, y el enunciado antiguo quedó derogado por
[ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md). **Cita**
[INV-CITY-002](../invariants/city.md#inv-city-002) e [INV-CITY-003](../invariants/city.md#inv-city-003), de
la que `INV-ECON-004` es la fórmula concreta.

| ID | Enunciado | Verificación |
|---|---|---|
| [INV-ECON-001](../invariants/economy.md#inv-econ-001) | `city_stocks.amount >= 0` siempre; un débito nunca deja el stock negativo. | `CHECK` en base + test de integración que intenta gastar más de lo que hay. |
| [INV-ECON-002](../invariants/economy.md#inv-econ-002) | Todo débito de madera ocurre en la misma transacción que el efecto que lo justifica. | Test de integración con fallo inyectado al insertar el edificio: el stock no cambia. |
| [INV-ECON-003](../invariants/economy.md#inv-econ-003) | Una unidad tiene como mucho una tarea, y está `WORKING` si y sólo si su tarea está en fase `WORKING`. | PK de `unit_tasks` + `loadcheck`. |
| [INV-ECON-004](../invariants/economy.md#inv-econ-004) | `population_limit == eras.population_cap + 5 × casas COMPLETE` para toda ciudad. | `loadcheck` al arrancar + test de simulación. |
| [INV-ECON-005](../invariants/economy.md#inv-econ-005) | Una tarea, un edificio o una producción a medias sobrevive a un reinicio y termina en el mismo instante absoluto en que habría terminado. | Tests de recuperación en cada fase. |
| [INV-ECON-006](../invariants/economy.md#inv-econ-006) | Ningún viaje nuevo empieza con la ciudad fuera de `ONLINE` ni con su dueño ausente. | Test de simulación: desconexión a mitad de viaje → un depósito más y `IDLE`. |
| [INV-CITY-017](../invariants/city.md#inv-city-017) | `population` es el recuento de unidades vivas más las producciones en curso. | `loadcheck` al arrancar + tests de integración del encargo y el nacimiento. |

**Precisión (2026-10-27).** `INV-ECON-003` se registra como bicondicional (`WORKING` si y sólo si la tarea está
en `WORKING`), que es lo que la frase original implica para que una unidad `WORKING` no quede parada sin
temporizador. `INV-ECON-006` añade «ni con su dueño ausente»: tras un reinicio una ciudad que estaba `ONLINE`
se queda `ONLINE` aunque nadie se conecte ([presence.md](presence.md) §7.6), y sin esa mitad sus aldeanos
recolectarían indefinidamente, que es justo lo que Q1 descarta.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | Máquina de estados de la tarea (fase a fase, cancelación, sustitución, pérdida de carga); elección del tile de depósito y de nacimiento; validación de `city.build` y `city.train`; fórmula del límite de población. |
| simulation | Golden `madera`: un aldeano, bosque a 10 tiles, tres viajes, ticks exactos de cada depósito; cancelación a mitad de tramo; desconexión del dueño (RN-ECON-005); casa y aldeano completándose en el tick previsto; mismo escenario con `-race`. |
| recovery | Reinicio durante `TO_RESOURCE`, `WORKING` y `TO_DEPOSIT`; durante una construcción; durante un entrenamiento; con el vencimiento ya pasado en la caída. |
| integration | `CHECK` de stock, PK de tareas, `UNIQUE` de edificios; débito y efecto en una transacción con fallo inyectado; idempotencia de `city.build` por `requestId`. |
| contract | Los tres comandos nuevos y los dos mensajes nuevos contra el JSON Schema; los dos códigos nuevos en el catálogo. |
| e2e | `unit.gather` → `unit.movement.started` → `entity.update { WORKING }` → `city.update { stocks }`; `city.build` → `building.spawn` → `building.update { COMPLETE }` + `city.update { populationLimit }`; `city.train` → `entity.spawn`. |
| cliente | Contador de madera y estado `WORKING` en el HUD; `scripts/smoke.mjs` añade un viaje completo. |

## 10. Decisiones de producto (2026-10-27)

Preguntas que la propuesta dejó abiertas y lo que se decidió. Son decisiones de producto, no de ingeniería:
cambiarlas es cambiar la spec.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Recolectan los aldeanos con el dueño desconectado? | **No más allá del viaje en curso** (RN-ECON-005). «Sí, mientras la ciudad esté `OFFLINE_PENDING`» convertiría los 5 min de margen en 15 madera gratis; «sí, siempre» sería un idle game. |
| Q2 | ¿Un aldeano cuesta madera? En AoE cuesta comida. | Sí, en este slice: un único recurso evita un segundo ciclo de recolección antes de haber probado el primero. Cambiará a comida cuando exista. |
| Q3 | ¿Se agota el bosque? | No en este slice. Es la primera cosa que añadir después, porque convierte el espacio en recurso disputado (pilar 5). |
| Q4 | ¿Casas sólo en el 3×3? | Sí: ocho huecos bastan para `STONE_AGE` y no toca pathfinding. Crecer la zona urbana es un slice propio. |
| Q5 | ¿La madera cargada se pierde al cancelar? | Sí: es la regla más simple y hace que interrumpir un viaje cueste algo. |
| Q6 | Números de §4.5. | Son el punto de partida para que cinco personas los prueben; se ajustan con lo observado, sin tocar la spec mientras sigan siendo parámetros de catálogo. |

## 11. Documentos relacionados

- [unit.md](unit.md), [movement.md](movement.md), [city.md](city.md), [presence.md](presence.md).
- [../architecture/game-loop.md](../architecture/game-loop.md) (fases 3, 5 y 8).
- [../decisions/ADR-013-persistence-pipeline.md](../decisions/ADR-013-persistence-pipeline.md), [../decisions/ADR-014-command-idempotency.md](../decisions/ADR-014-command-idempotency.md).
- [../roadmap/roadmap.md](../roadmap/roadmap.md) §5, fase «Economy», de la que este slice es el primer paso.
