# ADR-034: La reclamación de un territorio es un encargo durable en dos fases, y la captura una orden en dos fases que escribe el control y el límite de dos ciudades

Propósito: fijar cómo el slice de conquista ([SPEC-TERRITORY-CONQUEST](../specs/territory-conquest.md)) pasa la
escritura del control de un territorio del alta del jugador a la simulación, cómo sobrevive una reclamación a un
reinicio y cómo la captura cambia a la vez el dueño y el límite de población de dos ciudades sin desordenar sus
escrituras.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-04** |
| Ámbito | `internal/domain/territory` (el predicado de la reclamación, el bono), `internal/domain/building` (la fórmula del límite), `internal/game/simulation` (la orden, el barrido de la fase 5, la carga), `internal/persistence/postgres` (`territory_claims`, la captura), `internal/game/loadcheck` (el recuento del límite), migración `000014`, `packages/protocol`, cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md) (la cola ordenada por clave), [ADR-016](ADR-016-foreign-territory-actions.md) (la clasificación de acciones), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (toda escritura declara compensación), [ADR-024](ADR-024-combat-simultaneous-resolution.md) (lo que se decide en la RAM y se escribe después), [ADR-029](ADR-029-population-over-limit-and-house-destruction.md) (el límite que baja), [ADR-030](ADR-030-stone-layer-and-era-advance.md) (el encargo con su fila), [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md) (el trabajo con dos claves), [ADR-033](ADR-033-live-notices-ephemeral.md) (los avisos) |

---

## Contexto

Hasta E11 el control de un territorio sólo lo escribía el alta del jugador: `TerritoryRepo.ClaimForPlayer`, en la
goroutine HTTP y en la misma transacción que la ciudad, con concurrencia optimista sobre `territory_control.version`
y la condición `owner_type = 'NONE'`; la RAM se entera después del `COMMIT` ([territory.md](../specs/territory.md)
§10). El canon §12 clasifica el cambio de dueño como escritura inmediata.

La conquista trae tres cosas nuevas:

- **Un encargo que dura minutos** (5 o 10, Q3) y que tiene que sobrevivir a un reinicio, como el avance de era.
- **Un cambio de dueño que decide el tick**, no el alta.
- **Un efecto en dos ciudades**: el límite de población del conquistador sube y el del anterior dueño baja
  (SPEC-TERRITORY-CONQUEST §4.2). Cada una de esas escrituras tiene que quedar en el orden de la cola de su ciudad,
  porque las casas, su destrucción y el avance de era también escriben `cities.population_limit` por deltas.

## Decisión

**1. La reclamación tiene su fila, `territory_claims`, y empieza en dos fases.** El tick valida la orden
(`RN-CONQ-001` a `003`) y la reserva en la RAM: el territorio y el guerrero quedan apartados, así que ni otra
reclamación ni otra del mismo guerrero pasan la validación. La cola escribe en una transacción la fila `ACTIVE`,
`territory_control.contested = true` y, si el territorio tiene dueño, `TerritoryClaimStarted`. La confirmación
vuelve por el buzón y sólo entonces la reclamación existe para los demás: el guerrero pasa a `CLAIMING`, viaja
`territory.update` con su `claim`, el dueño recibe `TERRITORY_CLAIMED` y sus guardias responden (`RN-CONQ-006`). El
plazo cuenta desde el tick de la orden. Si la transacción falla, la reserva se deshace y el jugador recibe el
código; el índice parcial único de §7 convierte una segunda reclamación activa en un rechazo de dominio
(`INVALID_TARGET`). Es el patrón del avance de era ([ADR-030](ADR-030-stone-layer-and-era-advance.md)): nada se
anuncia antes de existir.

**2. Lo que rompe una reclamación se decide en la RAM y se escribe después.** En la fase 5, en orden de territorio,
cada reclamación activa se vuelve a comprobar (`RN-CONQ-005`): guerrero vivo, en el tile de la reclamación y en
`CLAIMING` (otra orden lo saca de ese estado), territorio todavía reclamable y nada que la bloquee (paz, protección).
Si falla, la RAM la termina en el acto —el guerrero a `IDLE`, `territory.update` sin `claim`— y la cola escribe
`FAILED` y `contested = false`. La escritura es idempotente (`WHERE status = 'ACTIVE'`) y, si falla del todo, se
reencola en el volcado, como la muerte ([ADR-024](ADR-024-combat-simultaneous-resolution.md)). Si se perdiera por
una caída, la carga de §5 la vuelve a romper en el primer tick.

**3. La captura es una orden en dos fases con las dos ciudades como claves.** Cumplido el plazo y con el predicado
en pie, el tick marca la reclamación como **capturando** —sigue activa y nadie la puede romper ni reclamar ese
territorio— y encola un trabajo con la ciudad del conquistador como clave y la del anterior dueño, si lo había,
como segunda clave ([ADR-032](ADR-032-trade-offers-and-two-key-jobs.md)). La transacción:

| Paso | Qué hace |
|---|---|
| 1 | Cierra la reclamación: `UPDATE territory_claims SET status = 'COMPLETED' WHERE id = $1 AND status = 'ACTIVE'`; sin fila, ya estaba escrita y no hace nada más (un reintento) |
| 2 | Comprueba `INV-TERR-011`: ninguna ciudad del dueño actual tiene su centro dentro del territorio |
| 3 | Cambia el dueño con la versión que conocía la RAM y el dueño esperado: `UPDATE territory_control SET owner_type = 'PLAYER', owner_id, captured_at, contested = false, version = version + 1 WHERE territory_id AND version AND owner_id IS NOT DISTINCT FROM $anterior` |
| 4 | Suma el bono al límite de la ciudad del conquistador y lo resta del de la ciudad del anterior dueño, como deltas (`population_limit = population_limit ± 5`), según la fórmula de §6 |
| 5 | Escribe `TerritoryControlChanged` |

Con el `COMMIT`, el buzón devuelve el control nuevo y la RAM lo aplica: dueño, versión, `contested = false`, los
dos límites y el guerrero a `IDLE`; viajan `territory.update`, `city.update { populationLimit }` al chunk de cada
ciudad y los avisos `TERRITORY_LOST` y `TERRITORY_TAKEN`. Si el paso 2 o el 3 no se cumplen —sólo una fundación
simultánea sobre ese territorio libre puede cambiar la versión— es un rechazo de dominio: no se escribe nada, la
reclamación termina como en §2 y la RAM recibe el control de la fundación por su camino de siempre. Si la base no
responde, la reclamación sigue activa y la captura se vuelve a encolar en el volcado. Es write-through, como pide
el canon para el dueño: la RAM cambia después del `COMMIT`. Si el guerrero muere mientras la transacción está en
vuelo, la captura vale: el plazo se cumplió.

**4. `contested` es «hay una reclamación activa».** Se escribe con la fila en §1, §2 y §3, y la RAM lo deriva de sus
reclamaciones; `control_points` sigue dormida ([INV-TERR-006](../invariants/territory.md#inv-terr-006), precisado).

**5. La carga recupera las reclamaciones.** `territory_claims` en `ACTIVE` se carga con el resto del mundo. En el
primer tick, cada una pasa por el barrido de §2: si su guerrero sigue vivo y en su tile, vuelve a `CLAIMING` —en la
base estaba `IDLE`, porque el volcado escribe `CLAIMING` como `IDLE`, igual que `ATTACKING`— y, si venció durante la
caída, se captura en ese tick. Si no, termina. La validación de carga repara un `contested` que no coincida con las
reclamaciones activas.

**6. El límite tiene un término más, y un único sitio donde se calcula.** `building.PopulationLimit` pasa a ser
`population_cap(era) + 5 × casas COMPLETE + 5 × territorios conquistados`, donde conquistado es todo territorio del
jugador que no contiene el centro de su ciudad. La validación de carga recibe la geometría de los territorios y lo
recuenta ([INV-ECON-004](../invariants/economy.md#inv-econ-004), ampliado). La captura calcula sus deltas con la
misma regla: en la práctica +5 al conquistador y −5 al anterior dueño, porque la capital no se puede reclamar.

**7. Esquema, migración `000014`:**

| Cambio | Para qué |
|---|---|
| `territory_claims (id, territory_id → territories, unit_id → units, player_id → players, city_id → cities, owner_player_id → players NULL, x, y, started_at_ms, completes_at_ms, status, end_reason, request_id, created_at, ended_at)` | §1, §5 |
| `CHECK (status IN ('ACTIVE', 'COMPLETED', 'FAILED'))` y `CHECK (completes_at_ms > started_at_ms)` | Estados cerrados |
| Índice parcial único `(territory_id) WHERE status = 'ACTIVE'` | `INV-TERR-010` |
| Índice parcial único `(unit_id) WHERE status = 'ACTIVE'` | Un guerrero, una reclamación |

`units.status` no cambia (§5). Los nombres nuevos de la cola y de los comandos internos entran en `JobNames` y
`CommandTypes`; `unit.claim` se clasifica como hostil ([ADR-016](ADR-016-foreign-territory-actions.md) §2.3): la
protección se comprueba cuando el territorio tiene dueño, y también la de quien reclama, que en un territorio libre
tampoco puede seguir si queda protegido —su guerrero sería invulnerable—.

## Alternativas descartadas

1. **Presencia militar automática**: `control_points` que suben por tick con los guerreros de un jugador en el
   territorio. Captura sin que nadie lo ordene, se lee mal y obliga a contar guerreros por territorio en cada tick.
2. **Un edificio estandarte** que construye un aldeano y se derriba con el asedio. Abre construir fuera de la zona
   urbana, que no existe, con otro tipo de edificio y su vida.
3. **La reclamación sólo en la RAM.** Sin migración, pero un reinicio la perdería a medio plazo sin dejar rastro de
   cómo terminó, y el registro tendría un `TerritoryClaimStarted` sin final.
4. **La captura optimista**, con la RAM primero y la escritura después, como la destrucción de una casa. Una
   fundación simultánea sobre el mismo territorio libre podría ganar en la base después de que la RAM lo hubiera dado
   al conquistador, y los dos quedarían con dueños distintos hasta el siguiente reinicio. El canon pide escritura
   inmediata para el dueño, y la orden en dos fases lo cumple sin coste visible.
5. **La captura con el territorio como única clave.** Las escrituras de `population_limit` de las dos ciudades
   podrían adelantar o retrasar a las de sus casas, el mismo problema que resolvió [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md).

## Consecuencias

### Positivas

- Se reutilizan piezas que ya están probadas: el encargo con su fila, el trabajo con dos claves, la respuesta
  automática, los avisos y el registro.
- La RAM y la base no pueden discrepar sobre el dueño de un territorio.

### Negativas

- Una tabla, tres trabajos de la cola y cuatro comandos internos más.
- Entre el vencimiento y la captura pasan unos milisegundos, y con la base caída, hasta el siguiente volcado.
- `building.PopulationLimit` y la validación de carga necesitan la geometría de los territorios y el centro de cada
  ciudad.

### Neutras

- `control_points` sigue sin usarse; si algún día hace falta un progreso parcial, la columna sigue ahí.
- El alta del jugador sigue escribiendo el control de su capital como hasta ahora: la fundación no cambia.
