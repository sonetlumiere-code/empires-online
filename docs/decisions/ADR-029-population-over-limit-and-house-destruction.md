# ADR-029: Una ciudad puede quedar por encima de su límite de población; la base sólo impide crear por encima, y la destrucción de una casa baja el límite

Propósito: fijar qué pasa con el límite de población cuando el asedio ([SPEC-COMBAT-SIEGE](../specs/combat-siege.md))
destruye una casa, y cómo se escribe esa destrucción sin que el límite y las casas diverjan.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-04** |
| Ámbito | `migrations/000011`, `internal/persistence/postgres` (`economy.go`, `loadcheck.go`), `internal/game/loadcheck`, `internal/game/simulation` (`siege.go`, `economy.go`), `internal/domain/building` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md) (cola por clave), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (toda escritura declara compensación), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (reserva de la plaza al ordenar, compleción optimista), [ADR-026](ADR-026-pillage-two-phase-stock-debit.md) (golpes sobre una ciudad) |

---

## Contexto

[INV-CITY-002](../invariants/city.md#inv-city-002) dice desde M2 que `population` nunca supera `population_limit`, y
el `CHECK cities_population_within_limit` de `000001` lo hace imposible en disco. Desde E1 ese `CHECK` es además la
última barrera de la reserva de plaza al entrenar ([ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md),
`RN-ECON-010`): el encargo sube `population` en su transacción, y si la RAM y la base hubieran divergido, la base lo
rechaza (`TestEntrenarSinPlazaSeRechazaSinDebitar`).

El asedio destruye casas, y cada casa completa suma 5 al límite ([INV-ECON-004](../invariants/economy.md#inv-econ-004)).
Una ciudad con la población al tope que pierde una casa queda con más población que límite, cosa que hoy la base no
admite. Producto decidió (SPEC-COMBAT-SIEGE Q1) que las unidades viven y que la ciudad sólo deja de poder crear
unidades hasta volver bajo el límite.

## Decisión

**1. El exceso es legal; lo prohibido es crear.** INV-CITY-002 pasa a decir: `population >= 0`, y ninguna
transacción que aumente `population` la deja por encima de `population_limit`. Una ciudad puede quedar por encima
cuando el límite baja; conserva sus unidades, sus producciones en curso nacen (la plaza estaba reservada) y ningún
encargo nuevo cabe hasta volver bajo el límite (`POPULATION_LIMIT_REACHED`, sin código nuevo).

**2. La barrera de la base cambia de forma, no desaparece.** `000011` quita el `CHECK cities_population_within_limit`
y crea el trigger `cities_population_not_raised_over_limit`, `BEFORE INSERT OR UPDATE OF population,
population_limit`, que rechaza la fila cuando `NEW.population > NEW.population_limit` y además es un `INSERT` o
`NEW.population > OLD.population`. Rechaza con `ERRCODE = 'check_violation'` y `CONSTRAINT =
'cities_population_within_limit'`, de modo que el código que hoy traduce esa violación a `city.ErrPopulationLimit`
no cambia. Bajar el límite, o bajar la población, siempre pasa.

**3. La reparación de la carga recuenta, no crea.** La validación de carga deja de subir el límite hasta el recuento
(lo que hoy hace por INV-CITY-002): el límite es siempre el de INV-ECON-004, y la población es el recuento de
[INV-CITY-017](../invariants/city.md#inv-city-017) aunque quede por encima. Ese recuento puede subir `population` por
encima del límite sin crear nada, así que su transacción declara `SET LOCAL eo.population_recount = 'on'` y el
trigger la deja pasar. Es la única excepción y sólo vive dentro de esa transacción.

**4. La destrucción es optimista, como la compleción.** El golpe que deja una casa sin vida la quita de la RAM, baja
el límite de la ciudad en su bono si en la RAM estaba completa, difunde `building.destroyed` y `city.update`, y
encola la escritura con la clave `city:<id>` (detrás de cualquier encargo de esa ciudad ya encolado). La transacción:

- marca la casa `DESTROYED` sólo si no lo estaba (idempotente frente a reintentos);
- resta el bono del límite **según el estado que la casa tenía en la base**, no en la RAM: si su compleción aún no se
  había escrito, la base nunca sumó el bono y no hay nada que restar, y esa compleción, al reintentarse, ya no la
  encuentra en obras ([ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) §5);
- escribe `BuildingDestroyed` con `counterpart_player_id` y `atMs` ([ADR-028](ADR-028-event-log-from-world-events.md)).

Con un fallo definitivo, la destrucción **se reencola en el siguiente volcado**, como una compleción descartada: la
RAM no resucita la casa. Si el proceso cae antes del `COMMIT`, la casa sigue en la base y el arranque la carga entera
con su bono: se pierde la destrucción, nunca se duplica ni se resta dos veces.

**5. El tile se libera con la confirmación.** Hasta que la transacción de la destrucción vuelve por el buzón, el tile
sigue ocupado para `city.build`. Así una casa nueva en el mismo sitio siempre se escribe después de la destrucción,
por la misma clave, y no choca con el índice único.

**6. Las casas destruidas conservan su fila.** `DESTROYED` entra en `buildings_status_valid`; las filas destruidas no
se cargan al arrancar, como las unidades `DEAD`, y `buildings_unique_tile` pasa a ser un índice único parcial sobre
las casas no destruidas, para poder reconstruir en el mismo tile.

**7. El daño no se persiste.** La vida de una casa dañada vive en la RAM y se olvida tras
`EO_SIEGE_DAMAGE_RESET_SECONDS` sin golpes o con un reinicio (SPEC-COMBAT-SIEGE Q4). Sólo la destrucción es durable.

## Alternativas descartadas

- **Impedir la destrucción que dejaría la ciudad por encima.** Haría inmunes a las ciudades llenas: cuanto más grande,
  más segura (Q1).
- **Matar unidades hasta volver bajo el límite.** Obliga a elegir víctimas arbitrarias y castiga dos veces el mismo
  golpe (Q1).
- **Quitar el `CHECK` y condicionar sólo el `UPDATE` del encargo** (`WHERE population + $2 <= population_limit`). Es
  más simple, pero deja la base sin barrera ante cualquier escritor futuro que suba la población: la regla viviría en
  una sentencia y no en la tabla.
- **Deshacer la destrucción en la RAM ante un fallo definitivo.** Obligaría al cliente a ver reaparecer una casa que ya
  vio caer, y añadiría un camino de vuelta que la compleción no necesita. Reintentar es lo que ya hace ADR-023 §5.
- **Borrar la fila de la casa destruida.** Perdería la casa de la que habla `BuildingDestroyed`, y las unidades muertas
  ya sientan el precedente de conservar la fila (backlog Q-05).

## Consecuencias

### Positivas

- La reserva de plaza al entrenar conserva su última barrera en la base.
- El límite sigue siendo un valor derivado (INV-ECON-004) sin excepciones: la que tenía para INV-CITY-002 desaparece,
  porque la carga ya no lo sube.
- Reutiliza los patrones existentes: cola por clave de ciudad, compleción optimista con reencolado, filas que no se
  borran.

### Negativas

- Un trigger es menos visible que un `CHECK`: quien lea el esquema tiene que saber que existe. Lo nombra
  [schema.md](../database/schema.md) y lo prueba la integración.
- La excepción de la carga (`eo.population_recount`) es un ajuste de sesión que el trigger tiene que leer; si otro
  escritor la declarara sin ser un recuento, la barrera no lo vería.

### Neutras

- `POPULATION_LIMIT_REACHED` cubre ahora también la ciudad que perdió casas; el cliente lo explica con el HUD de
  población en rojo.
