# ADR-043: La unidad de civilización es un tipo exclusivo del catálogo, ligado por `civilizations.unit`, que se paga en varios recursos y lucha por capacidad

Propósito: fijar cómo el slice de unidades de civilización ([SPEC-CIVILIZATION-UNITS](../specs/civilization-units.md))
da a cada civilización una unidad propia sin condicionales por código de civilización, cómo se cobra su entrenamiento
en madera, piedra y el bien propio en una sola transacción, y cómo el combate deja de preguntar por el tipo `WARRIOR`.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-17** |
| Ámbito | `internal/domain/unit` (el catálogo: los cuatro tipos exclusivos, `TrainCosts`, `OwnGoodCost`, `RequiresEra`, `Exclusive`), `internal/domain/player` (`Civilization.Unit` y su validación), `internal/domain/production` (`CanTrain` con civilización y edad), `internal/game/simulation` (la reserva y la devolución del entrenamiento en varios recursos, la respuesta automática por capacidad), `internal/persistence/postgres` (la carga de `civilizations.unit`, el encargo con varios débitos), `internal/protocol` (`units.json`), migración `000024`, `packages/protocol` (`UnitType`, `TrainableUnitType`, el catálogo de presentación de unidades y la unidad de cada civilización), cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (los encargos en dos fases, el débito con su efecto), [ADR-024](ADR-024-combat-simultaneous-resolution.md) (la resolución simultánea), [ADR-030](ADR-030-stone-layer-and-era-advance.md) (el coste en varios recursos), [ADR-035](ADR-035-civilization-traits.md) (los rasgos en el punto de consumo, el catálogo de presentación), [ADR-036](ADR-036-civilization-goods.md) (`civilizations.good`, el requisito de edad del taller), [ADR-039](ADR-039-faction-peace-and-affinity.md) (la tierra afín en cada golpe) |

---

## Contexto

Producto aceptó el 2026-11-17 las diez recomendaciones de la spec: una unidad por civilización (legionario, catafracto,
arquero a caballo, berserker), que se suma al aldeano y al guerrero; desde la Edad de Bronce; por 80 de madera, 40 de
piedra y 10 del bien propio, 35 s y 1 de población; que hace todo lo que hace un guerrero; sin bono propio en tierra
afín, sin compartirse con los aliados y sin tope por ciudad.

El código de hoy tiene tres piezas que no encajan con eso:

- **El catálogo de unidades** (`unit.catalog`, en Go) no sabe de civilizaciones ni de edades, y su coste de
  entrenamiento es **un** recurso (`TrainCost economy.Cost`). La civilización, su bien y sus rasgos viven en la base
  (`civilizations`), y `RN-PLAYER-012` prohíbe preguntar por el código de una civilización.
- **El encargo de entrenamiento** reserva y debita un único recurso (`TrainOrder.Stock`, un `StockDelta`), mientras
  que el de una obra ya reserva varios (`BuildOrder.Stocks`, desde [ADR-036](ADR-036-civilization-goods.md)).
- **El combate** decide casi siempre por la capacidad (`Definition.CanAttack`: el ataque, el saqueo, el asedio y la
  conquista), pero la respuesta automática pregunta `u.Type == unit.TypeWarrior`, y el cliente pregunta por
  `'WARRIOR'` en la proyección de la escena, en la barra de órdenes y en la conquista.

## Decisión

**1. Cuatro tipos exclusivos en el catálogo de Go.** `LEGIONARY`, `CATAPHRACT`, `HORSE_ARCHER` y `BERSERKER` entran en
`unit.catalog` con el perfil de la spec §4.2, en los campos que ya existen (`MaxHP`, `BaseMsPerTile`,
`AttackDamage`, `AttackIntervalMs`, `AttackRange`, `LootCarry`, `PopulationCost`). `Definition` gana tres campos:

- `Exclusive bool`: el tipo es la unidad de una civilización y sólo la entrena ella.
- `RequiresEra city.Era`: la edad mínima de la ciudad para entrenarlo (`BRONZE_AGE` en los cuatro), como
  `building.Definition.RequiresEra`. Vacía en el aldeano y el guerrero.
- `OwnGoodCost int64`: cuánto del **bien de la civilización del dueño** cuesta (10 en los cuatro). El catálogo de
  unidades no nombra ningún bien: el bien lo pone `civilizations.good` al ordenar, y así un tipo exclusivo nunca
  puede pedir un bien ajeno (`RN-CIVUNIT-003`).

`TrainCost economy.Cost` pasa a `TrainCosts economy.Costs`, el tipo que ya usan las obras (80 de madera y 40 de
piedra en los exclusivos; el aldeano y el guerrero siguen con su madera). `Definition.CostsWith(good)` devuelve la
lista completa, con el bien añadido si `OwnGoodCost > 0`. `CanTrain()` sigue siendo «tiene plazo y algún coste».

**2. La ligadura es `civilizations.unit`.** Como el bien ([ADR-036](ADR-036-civilization-goods.md)), qué unidad es de
qué civilización es un dato del catálogo de la base, que la carga lee en `player.Civilization.Unit`.
`player.ValidateCatalog` comprueba además que cada civilización tiene una unidad, que es un tipo exclusivo y
entrenable del catálogo de Go, que dos civilizaciones no la comparten y que ningún tipo exclusivo queda sin
civilización. Un catálogo que no lo cumple impide arrancar (`INV-UNIT-013`).

**3. `production.CanTrain` con civilización y edad.** Recibe la unidad y el bien de la civilización del dueño y
comprueba, en el orden de la spec §5: ciudad propia (`CITY_NOT_FOUND`), tipo entrenable (`INVALID_TARGET`), si es
exclusivo, que sea la unidad de la civilización (`ErrNotYourCivilization` → `INVALID_TARGET`), la edad
(`ErrEraRequired` → `INVALID_TARGET`), ocupada (`CITY_BUSY`), población (`POPULATION_LIMIT_REACHED`) y recursos
(`INSUFFICIENT_RESOURCES`). Devuelve la definición y la lista de costes resuelta.

**4. El encargo reserva y debita varios recursos.** `handleTrainUnit` reserva con el mismo `reserve` de las obras (el
coste sale ya de la RAM y los depósitos sin volcar de cada recurso viajan con el encargo, [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) §1);
`TrainOrder.Stock` pasa a `TrainOrder.Stocks []StockDelta` y `EconomyRepo.Train` aplica cada delta en la misma
transacción que la reserva de población y la fila de la producción: si uno no se aplica, no se aplica ninguno
([INV-ECON-002](../invariants/economy.md#inv-econ-002)). La devolución de un rechazo usa `refund` con la lista. La
producción cargada al arrancar guarda la lista que resuelve su tipo, igual que hoy guarda el coste único.

**5. La base repite la regla.** La migración `000024` añade `civilizations.unit` (`NOT NULL`, `UNIQUE`, `CHECK` de los
cuatro códigos), amplía `city_productions_unit_type_valid` y crea un trigger `BEFORE INSERT ON city_productions` que,
si el tipo es la unidad de alguna civilización, exige que sea la de la civilización del dueño de la ciudad y que la
era de la ciudad tenga un `ordinal` igual o mayor que el de `BRONZE_AGE`; si no, `RAISE … USING ERRCODE =
'check_violation'`, que `Train` traduce a `production.ErrNotTrainable` (`INVALID_TARGET`). Es la última barrera de
`INV-UNIT-012`: la RAM decide antes.

**6. El combate pregunta por la capacidad.** La respuesta automática pasa de `u.Type == unit.TypeWarrior` a
`def.CanAttack()`, como el resto del combate. Después de este slice ningún sistema del servidor decide quién ataca por
el tipo (`RN-CIVUNIT-005`); un test recorre el catálogo y comprueba que todo tipo que ataca puede atacar, responder,
saquear, asediar y reclamar. El cliente hace lo mismo con el catálogo de presentación (§7): la proyección pasa de los
booleanos `warrior`/`caravan` al tipo, y la barra de órdenes y la conquista preguntan `attacks`.

**7. El protocolo lleva tipos, no estadísticas.** `UnitType` y `TrainableUnitType` ganan los cuatro valores (un enum
estricto: cliente y servidor se estrenan juntos). No hay mensajes ni campos nuevos. Como el de civilizaciones de
[ADR-035](ADR-035-civilization-traits.md) §7, `packages/protocol` exporta un catálogo de presentación de las unidades
entrenables (`units.json`: nombre, si ataca, costes, bien propio, plazo base, edad, vida, daño, intervalo, alcance,
paso, carga y población) y `CivilizationInfo` gana `unit`. Un test de Go compara `units.json` con `unit.catalog`
campo a campo y el test de integración del catálogo de civilizaciones compara `unit` con la tabla: un cambio de
balance que no actualice el texto rompe la CI.

**8. Los rasgos, sin cambios.** La unidad exclusiva pasa por los mismos puntos de consumo de
[ADR-035](ADR-035-civilization-traits.md) (`msPerTile`, `productionMs`, `lootCarry`) con la civilización de su dueño;
la tierra afín la calcula el golpe como para cualquier unidad ([ADR-039](ADR-039-faction-peace-and-affinity.md)).
Ningún modificador nuevo.

**Esquema, migración `000024`:**

```sql
ALTER TABLE civilizations ADD COLUMN unit text;
UPDATE civilizations SET unit = 'LEGIONARY'    WHERE code = 'ROMAN';
UPDATE civilizations SET unit = 'CATAPHRACT'   WHERE code = 'BYZANTINE';
UPDATE civilizations SET unit = 'HORSE_ARCHER' WHERE code = 'PERSIAN';
UPDATE civilizations SET unit = 'BERSERKER'    WHERE code = 'NORSE';
ALTER TABLE civilizations ALTER COLUMN unit SET NOT NULL;
ALTER TABLE civilizations ADD CONSTRAINT civilizations_unit_unique UNIQUE (unit);
ALTER TABLE civilizations ADD CONSTRAINT civilizations_unit_valid
    CHECK (unit IN ('LEGIONARY', 'CATAPHRACT', 'HORSE_ARCHER', 'BERSERKER'));

ALTER TABLE city_productions DROP CONSTRAINT city_productions_unit_type_valid;
ALTER TABLE city_productions ADD CONSTRAINT city_productions_unit_type_valid
    CHECK (unit_type IN ('VILLAGER', 'WARRIOR', 'LEGIONARY', 'CATAPHRACT', 'HORSE_ARCHER', 'BERSERKER'));

CREATE FUNCTION city_productions_civilization_unit() RETURNS trigger …;  -- civilización del dueño y BRONZE_AGE
CREATE TRIGGER city_productions_civilization_unit BEFORE INSERT ON city_productions …;
```

La migración de vuelta quita el trigger, borra las producciones de tipos exclusivos, restaura el `CHECK` de
`000007` y quita la columna. `units.unit_type` sigue sin `CHECK` de tipo.

## Alternativas descartadas

1. **Una columna `civilization` en el catálogo de unidades.** El catálogo de unidades vive en Go y la civilización en
   la base: la pregunta «¿cuál es la unidad de X?» quedaría repartida entre los dos, y el catálogo de Go tendría que
   nombrar códigos de civilización, que es lo que `RN-PLAYER-012` evita.
2. **Un único tipo `CIVILIZATION_UNIT`** cuyas estadísticas cambian con la civilización del dueño. El tipo dejaría de
   decir qué es una unidad: el cliente, el registro y los avisos tendrían que cruzar el tipo con el dueño para saber
   qué ven, y `units.max_hp` dependería de un dato de otra tabla.
3. **El bien con nombre en el catálogo de unidades** (`TrainCosts` con `WINE` en el legionario). Duplica la ligadura
   civilización → bien que ya tiene `civilizations.good`, y una migración que cambiara el bien de una civilización
   dejaría su unidad pidiendo el viejo.
4. **Un segundo débito aparte para el bien.** Partiría la transacción del encargo en dos y abriría el caso de un
   encargo con la madera cobrada y sin el bien; la lista de `StockDelta` de las obras ya resuelve lo mismo.
5. **Enumerar los tipos que atacan** en la respuesta automática y en el cliente. Repetiría el problema con la próxima
   unidad; la capacidad del catálogo ya es la pregunta que usa el resto del combate.
6. **Mandar las estadísticas en `UnitView`.** La vida ya llega (`maxHp`) y los plazos en la producción y la polilínea;
   el resto sólo hace falta para el texto, que sale del catálogo de presentación sin crecer cada mensaje de unidad.

## Consecuencias

### Positivas

- Añadir una civilización es añadir su fila, su bien, su unidad en el catálogo de Go y su presentación; ningún sistema
  pregunta por su código.
- El entrenamiento y las obras comparten la misma reserva, la misma devolución y el mismo débito en varios recursos.
- El combate queda entero por capacidad: una unidad nueva que ataca hereda el ataque, la respuesta, el saqueo, el
  asedio, la conquista y la captura sin tocar ninguno.

### Negativas

- El catálogo de presentación de unidades duplica el de Go; lo ata un test.
- El trigger lee `cities`, `players` y `civilizations` en cada encargo de entrenamiento: una lectura por clave
  primaria más en una transacción que ya toca la ciudad.
- `UnitType` crece en un enum estricto: un cliente viejo no entiende una unidad exclusiva ajena y tiene que
  actualizarse con el servidor.

### Neutras

- El aldeano y el guerrero siguen como hoy: `TrainCosts` de un elemento, sin edad ni bien.
- El alcance 3 del arquero es el primero mayor que 1 en `main`, pero no cambia el código del combate: lo cubren los
  golden de la spec §9.
