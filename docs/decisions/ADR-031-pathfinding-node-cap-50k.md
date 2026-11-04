# ADR-031: El tope de nodos del A\* pasa de 20 000 a 50 000

Propósito: fijar el nuevo valor por defecto de `EO_PATHFINDING_MAX_NODES` y lo que cuesta, ahora que la piedra
([SPEC-ECON-STONE](../specs/economy-stone.md)) hace habituales las rutas largas.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-04** |
| Ámbito | `internal/config` (valor por defecto), `.env.example`, canon §8 y §17 |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-007](ADR-007-game-loop-frequency.md) (presupuesto del tick; este ADR lo sustituye en parte), [ADR-030](ADR-030-stone-layer-and-era-advance.md) (`RN-ECON-020`, la ruta de vuelta), [ADR-011](ADR-011-movement-timed-polyline.md) (polilínea) |

---

## Contexto

Desde M4 el A\* expande como mucho 20 000 nodos por búsqueda (`EO_PATHFINDING_MAX_NODES`, canon §8) y acepta como
mucho 256 tiles de distancia de Chebyshev (`EO_PATHFINDING_MAX_DISTANCE`). Hasta E8 casi todas las rutas eran cortas:
el bosque está a ≤ 8 tiles de cada ciudad nueva y los ataques se dan entre vecinos.

La piedra sólo está en las colinas, y la mediana de distancia de la hierba a su colina más cercana es de 74 tiles.
Medido el 2026-11-04 sobre 388 puntos de hierba del mapa canónico con colina a ≤ 256 tiles
([../specs/economy-stone.md](../specs/economy-stone.md) §4.1):

| Tope | Puntos que llegan a su colina | Búsqueda más cara medida (p95, la franja de 192–255 tiles) |
|---|---|---|
| 20 000 | 310 de 388 (80 %): casi nadie a más de unos 130 tiles | — |
| 50 000 | 381 de 388 (98 %) | ~21 ms |
| 100 000 | 388 de 388 | — |

Con 20 000, una de cada cinco ciudades no puede recoger piedra: la orden se rechaza al darla (`RN-ECON-020`). Eso
contradice la premisa con la que producto aceptó la piedra (SPEC-ECON-STONE Q2: todos pueden conseguirla, unos en
minutos y otros en una sesión).

## Decisión

**El valor por defecto de `EO_PATHFINDING_MAX_NODES` pasa a 50 000**, con el mismo rango (100–10 000 000). Lo decidió
producto el 2026-11-04 con los datos de arriba. `EO_PATHFINDING_MAX_DISTANCE` sigue en 256.

Nada más cambia: el A\* sigue dentro del tick, plano, y el tope sigue siendo la defensa contra destinos patológicos.

## Alternativas descartadas

| Alternativa | Por qué no |
|---|---|
| Dejar 20 000 y que las ciudades lejanas consigan la piedra comerciando (E10) | Rompe la premisa de SPEC-ECON-STONE Q2 y deja a una de cada cinco ciudades sin poder avanzar de era hasta que exista el comercio. |
| 100 000 | Cubre el 2 % que falta a cambio de duplicar otra vez el peor caso; esas ciudades están a más de 192 tiles y la piedra les costaría una sesión entera de todas formas. |
| Un tope distinto sólo para la recolección | Dos presupuestos para el mismo algoritmo dentro del mismo tick; el peor caso del tick es el mismo y es más difícil de razonar. |
| A\* jerárquico (DEBT-04) | Es la solución de fondo, pero es un slice propio con su spec. Este ADR no la sustituye: la aplaza con un número. |
| Sacar el pathfinding del tick a un pool de workers (la palanca de ADR-007) | Igual: requiere su propio ADR y cambia el modelo de un solo escritor. |

## Consecuencias

### Positivas

- El 98 % de la tierra llega a su colina más cercana; la premisa de la piedra vuelve a ser cierta.
- Ninguna ruta que antes funcionaba deja de hacerlo: el tope sólo sube.

### Negativas

- **El peor caso de una búsqueda pasa de ~8 a ~20 ms.** Es el presupuesto agregado de pathfinding por tick entero
  ([../architecture/pathfinding.md](../architecture/pathfinding.md) §8.3, 20 ms de 100): dos búsquedas largas en el
  mismo tick ya lo superan. ADR-007 razonaba con 20 000 nodos; este ADR lo sustituye en ese punto.
- **La orden de recoger cuesta dos búsquedas**, la de ida y la de vuelta (`RN-ECON-020`, ADR-030 §2): una orden a una
  colina lejana puede llevarse unos 40 ms de un tick.
- **La amplificación crece 2,5 veces.** Un cliente puede pedir 20 órdenes por segundo (`EO_WS_RATE_LIMIT_PER_SECOND`)
  hacia destinos lejanos y gastar CPU del tick con cada una ([INV-SEC-004](../invariants/security.md#inv-sec-004) acota
  el orden, no el coste). El rate limit por conexión sigue siendo la única cota.
- **Memoria del heap del A\*:** el peor caso pasa de ~3 MiB a ~7,6 MiB por búsqueda, una a la vez.
- **El coste acumulado se acerca a su límite.** `g` es un `int32`: con 50 000 nodos y una ruta entera de colinas en
  diagonal llega a ~1 270 millones, por debajo de 2 147 millones; por encima de ~84 000 nodos habría que pasar a
  `int64` ([../architecture/pathfinding.md](../architecture/pathfinding.md)).

### Neutras

- `eo_pathfinding_duration_seconds` y `eo_game_tick_overruns_total` dicen si el presupuesto se supera de forma
  sostenida. Esa es la condición de revisión de DEBT-04, y desde E9 se da con más probabilidad: si aparece, lo siguiente
  es el A\* jerárquico o el pathfinding fuera del tick, no subir más el tope.
- Cualquier despliegue puede volver a 20 000 con la variable de entorno; las ciudades lejanas volverían a quedarse sin
  piedra.
