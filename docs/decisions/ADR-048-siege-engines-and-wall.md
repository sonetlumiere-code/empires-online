# ADR-048: La muralla es un edificio en el tile del centro, y un solo predicado de asedio decide quién daña qué

Propósito: fijar cómo el slice de arietes y murallas ([SPEC-SIEGE-ENGINES](../specs/siege-engines.md)) mete en el mundo
una muralla con vida sin inventar un segundo ciclo de edificio, cómo se decide en un solo sitio qué unidad puede dañar
qué edificio según la ciudad esté amurallada o en brecha, y cómo entra el ariete en el catálogo de unidades sin que
las rutas que eligen objetivos de unidades necesiten un caso especial.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-19** |
| Ámbito | `internal/domain/building` (`WALL`), `internal/domain/unit` (`RAM`, el golpe contra edificios), `internal/domain/siege` (el predicado), `internal/game/simulation` (`unit.demolish`, los golpes de asedio, el fin de un asedio que la regla ya no admite), `internal/persistence/postgres` (la migración `000029`), `internal/protocol`, `packages/protocol`, `apps/web` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-016](ADR-016-foreign-territory-actions.md) (acción hostil), [ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md) (encargos en dos fases), [ADR-024](ADR-024-combat-simultaneous-resolution.md) (la resolución simultánea de la fase 4), [ADR-029](ADR-029-population-over-limit-and-house-destruction.md) (la escritura de la destrucción), [ADR-043](ADR-043-civilization-units.md) (el catálogo de unidades por capacidad), [ADR-044](ADR-044-castle-shooting.md) (el castillo dispara a quien derriba) |

---

## Contexto

Producto aceptó el 2026-11-19 las once recomendaciones de la spec: sólo el ariete, desde la Edad de Hierro y entrenado
en la ciudad; la muralla, un edificio desde la Edad de Bronce, uno por ciudad, en el centro, que las ciudades no traen
de serie; con la muralla completa en pie sólo el ariete daña la ciudad, y sólo a la muralla; el saqueo no cambia; el
daño de la muralla se olvida como el de una casa; derribada, la ciudad queda en brecha hasta que se vuelve a levantar;
el ariete no hace nada contra unidades; la ciudad no cae.

Lo que ya existe:

- **Los edificios** (casa, taller, castillo, mercado) comparten un ciclo: encargo en dos fases con su cobro
  ([ADR-023](ADR-023-economy-stock-deltas-and-two-phase-orders.md)), obra con `production.speed`, vida frente a un
  asedio con el daño en la RAM (`RN-COMBAT-032`), destrucción escrita con su evento
  ([ADR-029](ADR-029-population-over-limit-and-house-destruction.md)) y los mensajes `building.*`. Cada uno ocupa uno de
  los ocho huecos del 3 × 3; el centro, el Centro Urbano, no lo ocupa ninguno.
- **El derribo** (`unit.demolish`, E8) lo valida la orden con `def.CanAttack()` y lo aplica la fase 4 con
  `stepSiege`, que vuelve a mirar en cada golpe si la unidad sigue en alcance, si su dueño está presente y si la
  agresión sigue permitida (`RN-COMBAT-030`, `RN-COMBAT-036`).
- **El catálogo de unidades** decide por capacidad, no por tipo ([ADR-043](ADR-043-civilization-units.md)):
  `CanAttack()` es la puerta de `unit.attack`, de la respuesta automática y del objetivo del castillo.

## Decisión

### 1. La muralla es una fila de `buildings` en el tile del centro

`WALL` es un tipo más del catálogo de edificios: Edad de Bronce, 200 de piedra y 100 de madera, 120 s con
`production.speed`, 1 500 de vida, sin población. Su fila va en el tile del centro de la ciudad, el único del 3 × 3 sin
hueco, así que no compite con ningún otro edificio ni cambia la capa de ocupación (el 3 × 3 ya es `BLOCKED`,
`RN-CITY-009`). Hereda sin código nuevo el encargo, la obra, la vida, el olvido del daño, la destrucción y los
mensajes. La migración `000029` suma `WALL` a `buildings_type_valid`, un único parcial
`buildings_one_wall_per_city` sobre `(city_id) WHERE type = 'WALL' AND status <> 'DESTROYED'`, y un trigger
`buildings_wall_at_center` que rechaza una muralla fuera del centro de su ciudad (cruza `buildings` con `cities`).
`city.build { type: "WALL", x, y }` sólo admite el centro.

### 2. Un solo predicado decide quién daña qué

Una ciudad está **amurallada** si y sólo si tiene una muralla `COMPLETE` con vida (`State.Walled(cityID)`), y en
brecha en cualquier otro caso. `siege.CanDamage(attacker unit.Definition, target building.Type, walled bool)` es la
única regla:

| | Amurallada | En brecha |
|---|---|---|
| Unidad con golpe de combate (guerrero, unidades de civilización) | nada | cualquier edificio |
| Unidad de asedio (ariete) | sólo `WALL` | cualquier edificio |

La llaman `handleDemolishBuilding` (al dar la orden, `INVALID_TARGET` con el motivo) y `stepSiege` (en cada golpe,
antes de aplicarlo). Así, cuando una muralla se completa a mitad de un derribo, el siguiente golpe del guerrero no pasa
el predicado y su asedio termina en ese tick con la unidad `IDLE` (`RN-SIEGE-006`), por el mismo camino que una
ciudad que pasa a `PROTECTED` (`RN-COMBAT-036`). Que la muralla se complete en la fase 5 y los golpes vayan en la 4
deja un orden fijo: el tick en que se completa aún admite los golpes de la fase 4 y el siguiente ya no.

### 3. El ariete es una unidad con un golpe sólo contra edificios

El catálogo de unidades gana `SiegeDamage` y `SiegeIntervalMs`, el golpe contra edificios, separados de
`AttackDamage` y `AttackIntervalMs`. El guerrero y las unidades de civilización tienen el de asedio igual al de
combate (8 cada 1 500 ms, sin cambio de comportamiento); el ariete, 40 cada 3 000 ms y el de combate a cero. Dos
capacidades:

- `CanAttack()` (sin cambio): golpe de combate. Es falso para el ariete, así que `unit.attack`, `unit.pillage`,
  `unit.claim`, la respuesta automática y el objetivo de un enfrentamiento lo excluyen sin un caso especial.
- `CanDemolish()` (nueva): golpe de asedio. Es la puerta de `unit.demolish`, en lugar de `CanAttack()`.

`RAM` entra en `units_type_valid` y en `city_productions_unit_type_valid`. El ariete no tiene `LootCarry` ni entra en
guarnición, y la orden de recolectar lo rechaza como a cualquier unidad que no sea aldeano.

### 4. Sin mensajes nuevos

`WALL` y `RAM` entran en los enums estrictos `BuildingType` y `UnitType`, y se estrenan a la vez en el cliente y en el
servidor. La muralla viaja con `building.*` y la vida de `BuildingView`; el ariete, con `entity.*`. El registro reutiliza
`HOUSE_LOST` y `HOUSE_DESTROYED` con un `buildingType` opcional (`R-VER-2`) que dice qué cayó.

## Alternativas descartadas

1. **La muralla como columnas de `cities`** (`wall_hp`, `wall_status`): duplica el ciclo de un edificio (encargo, obra,
   destrucción, mensajes) en otra tabla y en otro mensaje.
2. **La muralla como tiles `BLOCKED` con vida por tile**: cada tile del anillo con su vida abre brechas parciales que el
   3 × 3 no tiene, y pide cambiar la capa de ocupación en caliente.
3. **La regla de quién daña qué repartida** entre la orden y la fase 4: un olvido en uno de los dos sitios deja golpear
   a un guerrero contra una ciudad que se amuralló a mitad del asedio.
4. **El ariete como unidad con `AttackDamage` y un caso especial** en cada ruta de combate: cada sitio que elige
   objetivos de unidades tendría que saber que el ariete no los elige.
5. **El ariete como orden de la ciudad** (un «asalto» sin unidad): quita al defensor el blanco que defender y al castillo
   a quién disparar.

## Consecuencias

- El catálogo de edificios suma un tipo sin hueco; el cliente lo dibuja como un anillo alrededor del 3 × 3, no en un
  tile.
- La familia `INV-COMBAT` suma dos invariantes: la muralla protege (`INV-COMBAT-008`) y el ariete no hiere ni roba
  (`INV-COMBAT-009`).
- El castillo dispara al ariete sin cambios: el ariete está en `ATTACKING` contra un edificio de su dueño
  ([ADR-044](ADR-044-castle-shooting.md) §3).
- Una ciudad con la muralla derribada vuelve a ser vulnerable a cualquier guerrero hasta que su dueño paga y espera la obra:
  es la consecuencia buscada (Q6), no un estado intermedio.
