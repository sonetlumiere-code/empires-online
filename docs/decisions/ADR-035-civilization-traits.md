# ADR-035: Los rasgos de civilización son enteros por mil, aplicados al dar cada orden con la civilización del dueño en la RAM

Propósito: fijar cómo el slice de civilizaciones ([SPEC-CIVILIZATION](../specs/civilizations.md)) convierte los
`traits` sembrados en `000002` en efectos de juego deterministas, dónde vive la civilización de cada jugador mientras
el mundo corre, y cómo llega la elección del registro hasta la fila del jugador.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-05** |
| Ámbito | `internal/domain/player` (el catálogo, `Trait` y las dos fórmulas), `internal/game/simulation` (la civilización de cada jugador, los cinco puntos de consumo, `IntroducePlayer`), `internal/game/loadcheck` (el catálogo y los dueños sin civilización), `internal/httpapi` (la fundación con `civilizationCode`), `internal/persistence/postgres` (la carga, el alta), migración `000015`, `packages/protocol` (`CivilizationCode`, `CityView.civilization`, el catálogo de presentación), `apps/web` (el registro, la migración 3 de identidad), cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-008](ADR-008-grid-coordinate-system.md) (la aritmética entera de la diagonal), [ADR-018](ADR-018-identity-store-and-ticket-issuer.md) (la identidad en `apps/web`), [ADR-020](ADR-020-stale-pending-accounts.md) (la cuenta pendiente y el reintento de la fundación), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (los encargos y sus plazos), [ADR-029](ADR-029-population-over-limit-and-house-destruction.md) (la vida de la casa) |

---

## Contexto

`civilizations.traits` es un `jsonb` de multiplicadores en coma flotante (`{"build.speed": 1.10, …}`) que nadie lee:
`Civilization.Trait(key, def float64)` existe, pero ningún sistema lo llama, y el Game Server funda a todos los
jugadores con `CivilizationID: 1` ([player.md](../specs/player.md) §6.2). La simulación ni siquiera sabe de qué
civilización es cada jugador.

SPEC-CIVILIZATION trae tres cosas nuevas:

- **Una elección en el registro** que tiene que llegar a `players.civilization_id` también cuando la fundación no
  termina y se repite en un login posterior ([ADR-020](ADR-020-stale-pending-accounts.md)).
- **Cinco puntos de consumo** en sistemas que hoy son aritmética entera exacta: la polilínea, el plazo de una obra o
  de un entrenamiento, el trabajo en el tile, la vida de la casa y la carga de botín. Sus golden afirman ticks y
  milisegundos exactos.
- **Una civilización por jugador en el tick**, que tiene que estar en la RAM: la simulación no lee la base.

## Decisión

**1. Los rasgos son enteros por mil.** `1000` es «sin efecto». La migración `000015` reescribe `civilizations.traits`
con las claves de la spec (`unit.move_speed`, `production.speed`, `building.house_hp`, `combat.loot_carry`,
`economy.gather_speed`) y los valores sembrados multiplicados por mil, con la sustitución del rasgo persa
(`economy.trade_yield` desaparece y entra `economy.gather_speed: 1100`, Q5). `Civilization.Traits` pasa a
`map[string]int32` y `Trait(key)` devuelve el entero, o `1000` si la clave falta. El catálogo se valida al cargar:
un valor que no sea un entero positivo impide arrancar ([INV-PLAYER-012](../invariants/player.md#inv-player-012)).

**2. Dos fórmulas, en el dominio.** `player.ScaleDuration(base, r) = (base × 1000 + r/2) / r` para los rasgos de
velocidad y `player.ScaleAmount(base, r) = (base × r + 500) / 1000` para los de cantidad, en `int64`, con mínimo 1.
Son las únicas operaciones admitidas sobre un rasgo; ningún sistema multiplica por su cuenta. Redondean al más
cercano, como `movement.StepDurationMs`, para que una ruta larga no acumule sesgo.

**3. Se aplican en el punto de consumo, al dar la orden.** La polilínea se construye con
`ScaleDuration(def.BaseMsPerTile, move_speed)`; la obra y el entrenamiento fijan su `completesAt` con
`ScaleDuration(BuildMs o TrainMs, production.speed)`; cada trabajo en el tile, con
`ScaleDuration(GatherWorkMs, gather_speed)`; la carga de botín se mide al tomar cada golpe con
`ScaleAmount(LootCarry, loot_carry)`; la vida máxima de una casa se calcula cada vez que se lee con
`ScaleAmount(MaxHP, house_hp)` de la civilización del dueño de su ciudad. Ningún valor efectivo se escribe en una
fila: lo que dura son los plazos, como hoy, y por eso lo que está en curso no cambia si el catálogo cambia
([INV-ECON-005](../invariants/economy.md#inv-econ-005)).

**4. La simulación tiene la civilización de cada jugador.** La carga lee `players.civilization_id` de los dueños de
las ciudades junto con el catálogo, y `State` guarda el mapa jugador → civilización; `IntroducePlayer` trae la del
jugador nuevo. Un dueño de ciudad sin civilización conocida es un error de la validación de carga
([INV-PLAYER-002](../invariants/player.md#inv-player-002): no hay valor por defecto). Los tests de simulación que ya
existen crean el estado con una civilización neutra de test (todo a `1000`) para que sus golden no cambien; los
nuevos fijan la suya.

**Precisión (2026-11-05, al implementarlo).** El mapa jugador → civilización es la ciudad: `city.City` lleva el código
de la civilización de su dueño (`Civilization`), que `CityRepo` lee de `players` en cada carga y el alta resuelve en su
transacción, y `State` instala el catálogo con `SetCivilizations`. Así `CityView.civilization` y los rasgos salen del
mismo dato, y en el MVP cada jugador tiene una ciudad. Sin catálogo instalado, o con un código que no está en él, los
rasgos son neutros: es lo que hacen los tests de simulación que no lo fijan, y por eso sus golden no cambian. En
producción no ocurre, porque la validación de carga aborta el arranque si una ciudad no lleva una civilización del
catálogo o un rasgo es inválido (`INV-PLAYER-002`, `INV-PLAYER-012`).

**5. La elección viaja del registro a la fundación, y se guarda en la cuenta.** `POST /api/auth/register` exige
`civilization`, que la web valida contra `CivilizationCode` (Zod) antes de crear la cuenta; la migración 3 del
almacén de identidad añade `accounts.civilization` (`ROMAN` para las cuentas que ya existen), que se escribe con la
cuenta `PENDING`. `POST /internal/players` lleva `civilizationCode`, que el Game Server resuelve contra el catálogo
cargado y escribe en la transacción del alta; un código desconocido es un rechazo definitivo (`400`). El login de
una cuenta `PENDING` funda con la civilización de la cuenta. El parámetro de arranque `CivilizationID` desaparece de
`httpapi.Options`; `FactionID` sigue (Q10).

**6. La civilización no cambia.** `000015` añade un trigger `BEFORE UPDATE OF civilization_id ON players` que rechaza
cualquier cambio ([INV-PLAYER-011](../invariants/player.md#inv-player-011)). Una migración de balance cambia los
rasgos del catálogo, nunca la civilización de un jugador.

**7. El protocolo lleva el código, no los rasgos.** `CityView` gana `civilization: CivilizationCode`, en Zod y en Go;
no hay mensajes nuevos. El texto del registro sale de un catálogo de presentación en `packages/protocol` (código,
nombre y rasgos), que `protocol:build` exporta junto a los JSON Schema y el Game Server embebe con ellos; un test de
integración comprueba que coincide con la tabla `civilizations`, de modo que una migración de balance que no lo
actualice rompe la CI en lugar de mentir en el formulario.

**8. Esquema, migración `000015`:**

```sql
UPDATE civilizations SET traits = '{"unit.move_speed": 1000, "production.speed": 1100}' WHERE code = 'ROMAN';
UPDATE civilizations SET traits = '{"unit.move_speed": 950, "production.speed": 1000, "building.house_hp": 1200}' WHERE code = 'BYZANTINE';
UPDATE civilizations SET traits = '{"unit.move_speed": 1050, "production.speed": 950, "economy.gather_speed": 1100}' WHERE code = 'PERSIAN';
UPDATE civilizations SET traits = '{"unit.move_speed": 1100, "production.speed": 900, "combat.loot_carry": 1150}' WHERE code = 'NORSE';

CREATE FUNCTION players_civilization_immutable() RETURNS trigger …;   -- RAISE si NEW.civilization_id <> OLD.civilization_id
CREATE TRIGGER players_civilization_immutable BEFORE UPDATE OF civilization_id ON players …;
```

El `economy.gather_rate: 1.00` romano desaparece, porque `1000` es lo mismo que no tenerlo. La migración de vuelta
restaura los `traits` de `000002` y quita el trigger.

## Alternativas descartadas

1. **Mantener los `float64`** y multiplicar al aplicar. `1.1 × 15 000` en coma flotante puede diferir en el último
   bit entre plataformas, compiladores u optimizaciones y, tras redondear, en un milisegundo: rompe los golden y la
   reproducibilidad ([INV-MOVE-009](../invariants/movement.md#inv-move-009) cuenta el mismo caso con la diagonal,
   [ADR-008](ADR-008-grid-coordinate-system.md)). Convertir a entero al cargar dejaría en la base un valor que no
   es el que se aplica.
2. **El pipeline completo** de modificadores (`ADD`/`MUL`/`SET_MIN`/`SET_MAX`, con orden, `scope` y origen). Hará
   falta cuando se combinen civilización, tecnología y territorio; con un solo origen y un solo operador es
   complejidad sin uso. Las dos fórmulas del §2 son el primer operador de ese pipeline, no un camino distinto.
3. **Congelar los valores efectivos en las filas** (`ms_per_tile` en `units`, `max_hp` en `buildings`). Duplica un
   dato derivado de dos fuentes, y una migración de balance dejaría de aplicarse a lo que ya existe.
4. **Mandar los rasgos en el protocolo.** El cliente no los necesita para jugar: los plazos, la vida y las cargas
   llegan resueltos. Sólo los necesita el formulario de registro, que se sirve antes de tener sesión WebSocket.
5. **Guardar la civilización sólo en el mundo** y no en la cuenta. El reintento de una fundación que no terminó no
   sabría con cuál fundar, y tendría que elegir una por defecto, que es lo que [INV-PLAYER-002](../invariants/player.md#inv-player-002)
   prohíbe.
6. **Aplicar `economy.trade_yield`** a lo que un jugador recibe de un trato. Crearía recurso en cada transferencia:
   dos cuentas que se pasan madera con «doy 1000, pido 1» la multiplicarían sin fin (SPEC-CIVILIZATION Q5).

## Consecuencias

### Positivas

- La capa de rasgos que necesitarán las capacidades exclusivas, las tecnologías y las facciones queda montada y
  probada con un solo origen.
- La aritmética sigue siendo entera y exacta: los golden siguen afirmando milisegundos.
- Equilibrar es una migración del catálogo, que se aplica a la siguiente orden de todos los jugadores.

### Negativas

- Cinco puntos de consumo tienen que pedir la civilización del dueño; olvidar uno es un bono que no se aplica en
  silencio. Lo cubren los golden por rasgo de la spec §9.
- El catálogo de presentación duplica los rasgos de la tabla; el test de integración lo ata a ella.
- Una migración más en el almacén de identidad, y el registro deja de ser compatible con un cliente que no mande
  `civilization`.

### Neutras

- La facción sigue fijada por el parámetro de arranque (`NEUTRAL`) hasta su spec.
- Las unidades no guardan ninguna velocidad: una migración de balance cambia la siguiente ruta, como ya decía
  [player.md](../specs/player.md) §6.2.
