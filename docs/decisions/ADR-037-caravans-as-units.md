# ADR-037: La caravana es una unidad con su carga en una tabla aparte, el trato carga en lugar de acreditar, la entrega es optimista y la captura cambia el dueño

Propósito: fijar cómo el slice de caravanas ([SPEC-CARAVANS](../specs/caravans.md)) hace que un trato viaje por el
mapa sin duplicar el movimiento ni la recuperación, cómo se persiste lo que va en camino y cómo una unidad viva cambia
de dueño sin descuadrar la población ni la cola.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-13** |
| Ámbito | `internal/domain/unit` (el tipo `CARAVAN`), `internal/domain/caravan` (paquete nuevo: la carga, la llegada y la captura), `internal/domain/trade` (el trato que carga), `internal/game/simulation` (la aceptación con ruta, la llegada, la captura, la recuperación), `internal/persistence/postgres` (`caravans`, `caravan_cargo`, el trato y la entrega), `internal/game/loadcheck`, `internal/eventlog`, migración `000017`, `packages/protocol`, cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md) (la cola ordenada por clave), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (deltas versionados y órdenes en dos fases), [ADR-024](ADR-024-combat-simultaneous-resolution.md) (los golpes de la fase 4), [ADR-028](ADR-028-event-log-from-world-events.md) (el registro sale de `world_events`), [ADR-031](ADR-031-pathfinding-node-cap-50k.md) (el tope de nodos), [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md) (el trato y el trabajo de dos claves), [ADR-033](ADR-033-live-notices-ephemeral.md) (los avisos en vivo), [ADR-036](ADR-036-civilization-goods.md) (la entrega optimista de la elaboración) |

---

## Contexto

Hasta E14 un trato aceptado es una orden en dos fases: el tick reserva en los dos almacenes y un trabajo con las dos
claves `city:<id>` aplica los deltas versionados de las dos ciudades y pasa la oferta a `ACCEPTED` en una
transacción ([ADR-032](ADR-032-trade-offers-and-two-key-jobs.md)). Lo que recibe cada ciudad está en su almacén en
cuanto vuelve la confirmación, a cualquier distancia.

Las unidades nacen con el `id` que les da la base (`UnitRepo`, `EconomyRepo.BirthUnit`) y entran en la RAM cuando
vuelve la confirmación. Se mueven por una polilínea temporizada cuyo movimiento `ACTIVE` está en `unit_movements`, y
tras una caída su posición se deriva de ella ([movement.md](../specs/movement.md)). Los golpes se resuelven en la
fase 4 ([ADR-024](ADR-024-combat-simultaneous-resolution.md)) y la muerte es `UnitRepo.Kill`. Las escrituras de una
unidad van por la clave `unit:<id>` y las de un almacén por `city:<id>` ([ADR-013](ADR-013-persistence-pipeline.md)
§2.2). El botín de un guerrero es `units.loot`, un entero de madera.

SPEC-CARAVANS trae cuatro cosas nuevas:

- **Mercancía en camino**: lo que sale de un almacén no entra en el otro hasta minutos después, y entretanto se ve y
  se puede atacar.
- **Una carga de varios recursos** que viaja en una unidad.
- **Una entrega que ocurre sola**, cuando la caravana llega, sin ninguna orden.
- **Una unidad viva que cambia de dueño** al capturarla.

## Decisión

**1. La caravana es un tipo de unidad.** `CARAVAN` entra en el catálogo de `internal/domain/unit` con 80 de vida,
`BaseMsPerTile` 1 200, coste de población 0, sin ataque ni botín, y no se entrena (`city_productions` no la admite).
Al ser una unidad hereda sin código nuevo el avance por la polilínea, la recuperación tras una caída, el interés por
chunks (`entity.spawn`, `entity.update`, `entity.despawn`), los golpes, la respuesta automática de los guerreros de
su dueño ([combat-response.md](../specs/combat-response.md)) y el dibujo. El dominio la marca como no ordenable: toda
orden de unidad sobre ella es `UNIT_NOT_MOVABLE` (`RN-CARAVAN-003`). La población la excluye por tipo, en la RAM y en
el recuento de la validación de carga ([INV-CITY-017](../invariants/city.md#inv-city-017)).

**2. La carga va en `caravan_cargo`, una fila por recurso.** `units.loot` es un entero de madera y una columna por
recurso habría que tocarla con cada recurso nuevo; las líneas de un trato ya son una lista `(recurso, cantidad)`, y la
carga de cada caravana es exactamente una de sus dos mitades. La fila de `caravans` guarda además el trato del que
nace, el remitente, la ciudad de origen, el destino vigente y el original, y el estado (`IN_TRANSIT`, `DELIVERED`,
`LOST`). La RAM tiene la caravana como una unidad más y su carga y su destino en un mapa aparte de la simulación.

**3. El trato carga en lugar de acreditar.** La aceptación sigue siendo la orden en dos fases de ADR-032 con su
trabajo de dos claves, con dos cambios:

- **La ruta se busca en el tick de la aceptación**, del depósito de la ciudad que propuso al de la otra
  (`RN-ECON-004`), con los límites de siempre. La de vuelta es la misma lista de tiles al revés: el A\* no corta
  esquinas, así que lo transitable en un sentido lo es en el otro, y cada caravana construye su polilínea con su
  `BaseMsPerTile` y el rasgo de velocidad de su dueño. Una búsqueda por trato, no dos. Sin ruta, la aceptación se
  rechaza con `PATH_NOT_FOUND` o `PATH_TOO_LONG` antes de reservar. `trade.propose` hace la misma búsqueda.
- **`TradeRepo.Accept` debita las dos ciudades y crea las dos caravanas** en la misma transacción: inserta las dos
  filas de `units` en los depósitos de origen, sus dos movimientos `ACTIVE` con la polilínea y el instante de salida
  fijados en el tick, las dos filas de `caravans` y sus líneas de carga, pasa la oferta a `ACCEPTED` y escribe
  `TradeAccepted` con los dos `caravanId`. No acredita nada. Devuelve los dos `id`, y las caravanas entran en la RAM al
  volver la confirmación por el buzón, ya avanzadas lo que diga su polilínea. Si la transacción se rechaza o falla del
  todo, la RAM devuelve lo reservado como hoy y no existe ninguna caravana.

[INV-TRADE-001](../invariants/trade.md#inv-trade-001) se precisa: lo que sale de un almacén entra en una caravana en
la misma transacción.

**4. La llegada es optimista, como la elaboración de ADR-036.** En la fase 5, en orden de `id`, cada caravana cuya
polilínea terminó en el depósito de su destino vigente se entrega: la RAM suma la carga al almacén del destino (con
`city.update`), quita la unidad (`entity.despawn { REMOVED }`) y encola `caravan.deliver` con **dos claves**,
`unit:<id>` y `city:<destino>`. La primera la ordena detrás de cualquier escritura pendiente de la caravana (su
captura, por ejemplo); la segunda, detrás de los volcados y los encargos de ese almacén, como cualquier delta
versionado. La transacción pasa la caravana de `IN_TRANSIT` a `DELIVERED` (condicionado), aplica el delta de cada
recurso, borra la fila de `units` (`caravans.unit_id` queda a `NULL` por la FK) y escribe `CaravanArrived`. Un
reintento que ya no encuentra la caravana en `IN_TRANSIT` no acredita nada. Si se descarta del todo, el crédito ya
está en la RAM y viaja en el siguiente volcado de ese almacén con versión nueva; la ventana en la que un fallo total
tras el `COMMIT` acreditaría dos veces al reiniciar es la misma que la de la elaboración, y queda documentada igual.

La entrega en dos fases (esperar al `COMMIT` para acreditar) se descarta: dejaría la caravana visible y atacable en el
depósito mientras vuelve la base, sin ganar nada sobre la ventana documentada.

**5. La captura cambia el dueño de una unidad viva.** El golpe que deja una caravana en 0 de vida no llama a `kill`:
en la RAM, la caravana pasa al dueño del guerrero que dio el golpe mortal (el desempate de `RN-COMBAT-010`) y a su
ciudad, recupera la vida entera, cancela su movimiento y busca una ruta nueva hasta el depósito de la ciudad del
captor, con el rasgo de velocidad del captor; terminan los enfrentamientos de los guerreros del captor contra ella.
Se encola `caravan.capture` con la clave `unit:<id>`: en una transacción cambia `units.player_id`, `units.city_id` y
`units.hp`, cancela el movimiento anterior e inserta el nuevo, cambia `caravans.to_city_id` y escribe
`CaravanCaptured` (y `CaravanIntercepted` si el destinatario original es un tercero). Como la caravana no cuenta en la
población de nadie, cambiar `city_id` no mueve el contador de ninguna ciudad. Si no hay ruta, la caravana se pierde:
`caravan.lose` borra la unidad, pasa la caravana a `LOST` y escribe `CaravanLost`; la carga desaparece, y es el único
camino por el que un trato destruye recursos.

La captura es optimista, como la muerte: la RAM la aplica en el tick y la transacción la confirma después. Si falla
del todo, se reintenta con el estado de la RAM, que es la fuente de verdad de la unidad.

**6. La protección offline no cubre a las caravanas.** El predicado hostil de
[INV-COMBAT-002](../invariants/combat.md#inv-combat-002) no mira la protección del dueño del objetivo cuando el
objetivo es una caravana. El resto del predicado (Safe Zone, ocultamiento, tratado de paz, protección del atacante)
no cambia. Es una rama por tipo de objetivo en la misma función, no un predicado aparte, para que la revalidación de
cada golpe siga siendo una sola.

**7. Quién se entera.** Las caravanas viajan en los mensajes de entidad, sin la carga, a todo el chunk.
`caravan.update { caravan }` va por `SendToPlayer` a los implicados (el dueño, el destinatario original y el captor)
en cada salida, captura, llegada o pérdida, y `world.snapshot` lleva `caravans[]` con las `IN_TRANSIT` que salen de la
ciudad del jugador, van a ella o son suyas. Las capturas salen además como `event_log.notice` con
`CARAVAN_CAPTURED` para quien la pierde y para el destinatario original
([ADR-033](ADR-033-live-notices-ephemeral.md)). En `world_events`, `CaravanArrived`, `CaravanCaptured`,
`CaravanIntercepted` y `CaravanLost` llevan `counterpart_player_id` y `atMs` cuando implican a dos jugadores; los
cuatro entran en `eventlog.EventTypes()` y en `Classify` ([ADR-028](ADR-028-event-log-from-world-events.md)).

**8. Esquema, migración `000017`:**

```sql
CREATE TABLE caravans (
    id                  bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    unit_id             bigint      REFERENCES units (id) ON DELETE SET NULL,
    offer_id            bigint      NOT NULL REFERENCES trade_offers (id),
    sender_player_id    uuid        NOT NULL REFERENCES players (id),
    from_city_id        bigint      NOT NULL REFERENCES cities (id),
    to_city_id          bigint      NOT NULL REFERENCES cities (id),   -- el destino vigente
    original_to_city_id bigint      NOT NULL REFERENCES cities (id),
    status              text        NOT NULL DEFAULT 'IN_TRANSIT',
    departed_at         timestamptz NOT NULL,
    closed_at           timestamptz,
    CONSTRAINT caravans_status_valid CHECK (status IN ('IN_TRANSIT', 'DELIVERED', 'LOST')),
    CONSTRAINT caravans_unit_while_in_transit CHECK ((status = 'IN_TRANSIT') = (unit_id IS NOT NULL))
);
CREATE UNIQUE INDEX caravans_one_per_unit ON caravans (unit_id) WHERE status = 'IN_TRANSIT';
CREATE UNIQUE INDEX caravans_two_per_offer ON caravans (offer_id, from_city_id);

CREATE TABLE caravan_cargo (
    caravan_id bigint  NOT NULL REFERENCES caravans (id) ON DELETE CASCADE,
    resource   text    NOT NULL,   -- con el CHECK de recursos de city_stocks
    amount     integer NOT NULL,
    PRIMARY KEY (caravan_id, resource),
    CONSTRAINT caravan_cargo_amount_positive CHECK (amount > 0)
);
```

El tipo de unidad no tiene `CHECK` en `units` y no lo gana aquí; `city_productions_unit_type_valid` sigue con
`VILLAGER` y `WARRIOR`. La carga de la simulación lee las caravanas `IN_TRANSIT` con su carga y su unidad; la
validación de carga rechaza una caravana `IN_TRANSIT` sin unidad viva, con una carga distinta de las líneas de su
lado del trato, con una oferta que no está `ACCEPTED` o con un movimiento que no termina en el depósito de su destino. Los trabajos nuevos (`caravan.deliver`, `caravan.capture`, `caravan.lose`)
entran en `simulation.JobNames`; las dos tablas, en el `TRUNCATE` de los tests de integración. La migración de vuelta
devuelve la carga de cada caravana `IN_TRANSIT` al almacén de su destino vigente (nada se pierde al bajar), borra sus
unidades y las dos tablas.

## Alternativas descartadas

1. **Una entidad aparte**, con su posición, su movimiento, su mensaje de mapa y su interés por chunks: duplica la
   polilínea, la recuperación y el dibujo que una unidad ya tiene, y el combate tendría que aprender a golpear algo
   que no es una unidad.
2. **Acreditar al aceptar y dibujar la caravana como adorno**: sin riesgo no hay slice (Q1 y Q5 de la spec).
3. **Matar la caravana y repartir la carga como botín** (Q5): el botín es un entero de madera; llevar seis recursos en
   el guerrero tocaría `units` y el depósito, y por la carga de 20 casi todo se perdería.
4. **La carga en columnas de `units`**, una por recurso: cada recurso nuevo tocaría la tabla más caliente del esquema.
5. **Dos búsquedas por trato**, una por sentido: el doble de coste en el tick para una ruta que ya es simétrica.
6. **Una ruta que se recalcula mientras camina** (pathfinding continuo): es lo que reabre `DEBT-04`, y una ruta fija
   (Q8) no lo necesita.
7. **Entregar en dos fases**, acreditando sólo tras el `COMMIT` (§4).
8. **Un predicado hostil aparte para las caravanas** (§6): dos predicados acabarían divergiendo, y la revalidación en
   cada golpe es la garantía de INV-COMBAT-002.

## Consecuencias

### Positivas

- El comercio recupera la distancia y el riesgo, y la escolta ya existe: los guerreros que responden solos.
- No hay mensajes cliente→servidor nuevos: atacar una caravana es `unit.attack`.
- Lo que va en camino está en la base y se recupera tras una caída como cualquier movimiento.

### Negativas

- Una búsqueda A\* en el tick por cada propuesta y cada aceptación, y otra por cada captura: con el tope de 50 000
  nodos, hasta unos 20-40 ms cada una en el peor caso. Son órdenes ocasionales; si `eo_game_tick_overruns_total`
  crece, la salida es la misma que para `DEBT-04`.
- Un trato grande es una sola caravana: si se pierde sin ruta, se pierde entero.
- El smoke del comercio y el de los bienes esperan la llegada: minutos en vez de segundos.

### Neutras

- `trade_offers` y sus líneas no cambian; `TradeAccepted` gana dos campos.
- Las caravanas no ocupan población, así que la conquista, el asedio y el límite de las ciudades no las ven.
