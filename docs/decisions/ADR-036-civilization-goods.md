# ADR-036: El bien de cada civilización es un recurso de catálogo, el taller lo elabora con un encargo con su fila, y la Edad de Hierro cobra bienes ajenos en el orden del catálogo

Propósito: fijar cómo el slice de bienes ([SPEC-CIVILIZATION-GOODS](../specs/civilization-goods.md)) añade cuatro
recursos a la vez sin condicionales por civilización, cómo se persiste la elaboración del taller y cómo se cobra un
coste que no es una lista fija de recursos.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-06** |
| Ámbito | `internal/domain/economy` (los recursos), `internal/domain/player` (el bien de cada civilización), `internal/domain/building` (el taller y los costes en varios recursos), `internal/domain/craft` (la elaboración, paquete nuevo), `internal/domain/advance` (el paso de Bronce a Hierro y su resolución), `internal/game/simulation` (la orden, el plazo, la entrega, el avance), `internal/persistence/postgres` (`city_crafts`, el cobro), `internal/game/loadcheck`, migración `000016`, `packages/protocol`, cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (deltas versionados y encargos en dos fases), [ADR-029](ADR-029-population-over-limit-and-house-destruction.md) (derribar un edificio), [ADR-030](ADR-030-stone-layer-and-era-advance.md) (el avance de era como encargo con su fila), [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md) (el trato), [ADR-035](ADR-035-civilization-traits.md) (los rasgos y la civilización en la ciudad) |

---

## Contexto

Hasta E13 hay dos recursos, madera y piedra, que cualquier ciudad recoge del terreno; un edificio, la casa, que cuesta
madera; una producción por ciudad, el entrenamiento; y un avance de era, de Piedra a Bronce, que cobra una lista fija
de recursos con deltas versionados en una transacción ([ADR-030](ADR-030-stone-layer-and-era-advance.md) §4). La
civilización de cada jugador viaja en su ciudad y sus rasgos se aplican al dar cada orden
([ADR-035](ADR-035-civilization-traits.md)).

SPEC-CIVILIZATION-GOODS trae cuatro cosas nuevas:

- **Cuatro recursos a la vez**, uno por civilización, que sólo produce el taller de esa civilización.
- **Un edificio que cuesta dos recursos** y del que sólo puede haber uno por ciudad.
- **Una segunda producción por ciudad**, independiente del entrenamiento y del avance.
- **Un coste abierto**: «60 bienes ajenos en cualquier mezcla», que depende de la civilización de quien paga y de lo
  que tiene en el almacén.

## Decisión

**1. Los bienes son recursos como la madera y la piedra.** `WINE`, `SILK`, `SPICES` y `FURS` entran en
`economy.Resources()`, en `Stocks` de Zod y de Go, y en los `CHECK` de `city_stocks` y de `trade_offer_lines`; cada
ciudad tiene su fila a cero de cada uno (la migración las crea para las que existen y `CreateCityStocks` para las
nuevas). Así el almacén, el volcado, el comercio, la vista del almacén y la carga los tratan sin código nuevo. El
saqueo sigue llevándose sólo madera (Q9).

**2. Qué bien es de quién es un dato del catálogo.** `civilizations.good` (texto, `NOT NULL`, único, con el mismo
`CHECK` de recursos) dice el bien de cada civilización, y `player.Civilization.Good` lo lleva en la RAM. El taller
pregunta por el bien de la civilización del dueño de su ciudad, nunca por el código de la civilización
(`RN-PLAYER-012`). La validación de carga exige que cada civilización tenga un bien de los recursos conocidos y que
ninguno se repita (`RN-GOODS-001`). El catálogo de presentación de `packages/protocol` gana `good`, y el test que lo
ata a la tabla lo compara también.

**3. El taller es un tipo de edificio más, con costes en varios recursos.** `building.Definition.Cost` pasa a
`Costs` (la casa, con una sola línea) y gana `RequiresEra` y `UniquePerCity`. El taller (`WORKSHOP`): 100 de madera y
50 de piedra, 45 s con `production.speed`, sin población, 200 de vida, `BRONZE_AGE`, único. Se construye, se completa
y se derriba por los mismos caminos que la casa ([ADR-029](ADR-029-population-over-limit-and-house-destruction.md)):
la orden lo reserva en el tick y su transacción debita las dos líneas con deltas versionados. El único lo garantiza
en la base un índice único parcial `(city_id) WHERE type = 'WORKSHOP' AND status <> 'DESTROYED'`, y en la RAM la
reserva del tick.

**4. La elaboración es un encargo en dos fases con su fila, `city_crafts`.** Una por ciudad (`city_id` es la clave),
con `good`, `amount`, `completes_at_ms` y `request_id`, en paquete de dominio propio (`internal/domain/craft`). La
orden `city.craft` valida en el tick (taller completo, sin elaboración en curso, stock), reserva y encola la
transacción que debita madera y piedra e inserta la fila; la confirmación vuelve por el buzón y sólo entonces la
elaboración existe para los demás (`craft` en `CityView` y `city.update`). Al vencer, una segunda transacción
**borra la fila y acredita el bien** con su delta: un reintento que ya no encuentra la fila no acredita nada, así que
la entrega es idempotente sin columna de estado. Qué bien sale se fija al ordenar, con el `good` de la civilización
del dueño, y se guarda en la fila: la carga no lo recalcula. Es independiente de `city_productions` y de
`city_era_advances`: ninguna de las tres bloquea a las otras salvo lo que ya decidió E9 (avanzar bloquea entrenar).
Derribar el taller borra su fila de `city_crafts` en la misma transacción del derribo, sin devolver nada
(`RN-GOODS-007`). La carga recupera las elaboraciones; una vencida durante la caída se entrega al arrancar
([INV-ECON-005](../invariants/economy.md#inv-econ-005)). Los trabajos nuevos de la cola (`craft.order`,
`craft.complete`) y los comandos internos entran en `simulation.JobNames` y `CommandTypes`.

**5. El paso de Bronce a Hierro tiene un coste abierto que el dominio resuelve.** `advance.Step` gana
`ForeignGoods int64`. Al validar la orden, `advance.Resolve(step, good, stock)` convierte el paso en una lista
concreta de costes: la madera y la piedra fijas y, para los 60 ajenos, cada bien que no es `good` en el orden del
catálogo de civilizaciones (por `id`), tomando de cada uno lo que haya hasta completar. Si no llega, es
`INSUFFICIENT_RESOURCES`. La lista resuelta es la que se reserva en el tick y la que la transacción cobra con deltas
versionados, igual que el primer avance: `city_era_advances` no guarda costes y no cambia. El propio nunca entra en
la resolución ([INV-ECON-011](../invariants/economy.md#inv-econ-011)). Desde Hierro no hay paso (`NOT_IMPLEMENTED`).

**6. Los eventos.** `CraftOrdered` y `CraftCompleted` en `world_events`, del jugador y su ciudad, sin
`counterpart_player_id`: no entran en el registro ni en los avisos. El taller usa los eventos de los edificios y el
avance los de E9.

**7. Esquema, migración `000016`:**

```sql
ALTER TABLE city_stocks DROP CONSTRAINT city_stocks_resource_valid;
ALTER TABLE city_stocks ADD CONSTRAINT city_stocks_resource_valid
    CHECK (resource IN ('WOOD', 'STONE', 'WINE', 'SILK', 'SPICES', 'FURS'));
INSERT INTO city_stocks (city_id, resource, amount) SELECT id, r, 0 FROM cities, unnest(...) r ON CONFLICT DO NOTHING;
-- igual para trade_offer_lines_resource_valid

ALTER TABLE civilizations ADD COLUMN good text;           -- se rellena para las cuatro y pasa a NOT NULL UNIQUE con su CHECK
ALTER TABLE buildings DROP CONSTRAINT buildings_type_valid;
ALTER TABLE buildings ADD CONSTRAINT buildings_type_valid CHECK (type IN ('HOUSE', 'WORKSHOP'));
CREATE UNIQUE INDEX buildings_one_workshop_per_city ON buildings (city_id)
    WHERE type = 'WORKSHOP' AND status <> 'DESTROYED';

CREATE TABLE city_crafts (
    city_id         bigint PRIMARY KEY REFERENCES cities (id) ON DELETE CASCADE,
    good            text   NOT NULL,      -- con el CHECK de recursos de bienes
    amount          integer NOT NULL CHECK (amount > 0),
    completes_at_ms bigint NOT NULL,
    request_id      text   NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
```

La migración de vuelta borra los talleres y las elaboraciones, quita las filas de bienes de `city_stocks` (los bienes
se pierden) y restaura los `CHECK`; las ciudades que llegaron a Hierro se quedan en Hierro, porque `IRON_AGE` existe
desde `000002`. Las ofertas pendientes que lleven bienes se borran con sus líneas.

## Alternativas descartadas

1. **Una unidad exclusiva por civilización** (SPEC-CIVILIZATION-GOODS Q1): crea diferencias militares, pero nadie
   necesita la unidad de otro, así que no crea comercio.
2. **Un recurso de terreno por civilización**, recogido por aldeanos: obligaría a cambiar el mundo canónico byte a
   byte y ataría la capacidad a la geografía, que no se elige (Q3).
3. **Un único recurso «bienes» con su origen** en otra columna: el almacén y las líneas de un trato tendrían que
   llevar el origen, y el coste «ajeno» dependería de una columna que ningún otro recurso tiene. Cuatro recursos de
   catálogo cuestan sólo filas.
4. **La producción pasiva del taller**: escribiría el stock en cada intervalo sin que nadie lo ordenara y produciría
   mientras el dueño está ausente, que [INV-ECON-006](../invariants/economy.md#inv-econ-006) no admite para la
   recolección (Q3).
5. **Reutilizar `city_productions`** con un tipo `CRAFT`: la producción es «una por ciudad» y el avance la bloquea;
   la elaboración tiene que convivir con las dos (`RN-GOODS-005`).
6. **Que el jugador elija qué bienes paga** en el avance: un payload con una lista de recursos y cantidades que el
   servidor tendría que validar, para un caso que el orden del catálogo resuelve sin ambigüedad. Quien quiera
   conservar un bien para comerciar puede comerciarlo antes de avanzar.
7. **Un coste fijo de 20 de cada uno de los otros tres** (Q4): bloquea a todos si falta una civilización en el mundo.

## Consecuencias

### Positivas

- La interdependencia del pilar 4 tiene mecánica: sin comerciar no se pasa de Bronce.
- Los bienes reutilizan el almacén, el comercio, el volcado y la vista sin código nuevo; añadir una civilización es
  añadir su fila, su bien y su recurso.

### Negativas

- Cuatro claves más en `Stocks`, que viajan con cada `city.update` de stock y en el snapshot.
- Un tercer encargo por ciudad, con dos trabajos de la cola y su recuperación.
- El orden del catálogo decide qué bienes se cobran: un jugador puede no esperarlo, y el texto del avance tiene que
  decirlo.

### Neutras

- `city_era_advances` y su avance en dos fases no cambian: la resolución de los bienes es dominio puro.
- La Edad de los Castillos sigue sin coste.
