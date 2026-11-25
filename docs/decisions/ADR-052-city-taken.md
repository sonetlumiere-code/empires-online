# ADR-052: La ciudad tomada es una fila nueva del vencedor que hereda los edificios completos de la caída

Propósito: fijar cómo el asalto que tumba una ciudad se la queda ([SPEC-CITY-TAKEN](../specs/city-taken.md)) sin
cambiar el dueño de ninguna fila de `cities` y sin abrir un camino de escritura nuevo fuera de la transacción de la
caída.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-25** |
| Ámbito | `internal/persistence/postgres` (`CityFallRepo.FallCity`), `internal/game/simulation` (`applyFall`), `internal/domain/cityfall`, migración `000032` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-050](ADR-050-city-fall.md) (la caída en una transacción, la fila caída con `fallen_at`), [ADR-051](ADR-051-multi-city-capital.md) (el hueco y la capital), [ADR-029](ADR-029-population-over-limit-and-house-destruction.md) (el límite con las casas) |

---

## Contexto

Producto delegó el 2026-11-25 las nueve preguntas de la spec en la recomendación del agente (sesión desatendida): una
ciudad que cae se **toma** si el vencedor tiene hueco (menos de dos en pie); la tomada nace con el nombre y la era de
la caída, sus edificios completos, tres aldeanos nuevos y el almacén a cero; el vencido cae como siempre.

Lo que condiciona la forma: `RN-CITY-020` quiere el dueño de una ciudad inmutable y `cities.owner_player_id` sólo se
escribe en el `INSERT`; la caída de ADR-050 deja la fila con `fallen_at` y la historia (tratos, caravanas, ofertas,
reclamaciones) apunta a ella; los índices de centro y de hueco son parciales sobre las ciudades en pie.

## Decisión

### 1. Una fila nueva, no un cambio de dueño

La tomada es un `INSERT` en `cities` del vencedor, en el centro de la caída y en el hueco libre del vencedor, con
`taken_from_city_id` apuntando a la caída. La caída se arrasa como hoy (`fallen_at`). Ninguna fila cambia de dueño;
la historia sigue apuntando a la ciudad caída.

### 2. Los edificios completos cambian de ciudad

En la misma transacción, `UPDATE buildings SET city_id = <tomada> WHERE city_id = <caída> AND status = 'COMPLETE'`
antes de destruir el resto. Conservan su id, su tipo, su tile y su estado. El límite de población de la tomada es el
de su era más el `PopulationBonus` de sus casas, como lo recuenta la carga (`INV-ECON-004`).

### 3. Tomar o arrasar, dentro de la transacción

La transacción cuenta las ciudades en pie del vencedor bajo el cerrojo de fundación: con hueco, toma; sin él, arrasa.
El índice por dueño y hueco de ADR-051 es la última barrera. La RAM aplica el resultado en el mismo comando que la
caída (`applyFall`), con la tomada entrando como un alta.

### 4. Una caída se toma una vez

`taken_from_city_id` tiene un índice único parcial: una caída da como mucho una tomada ([INV-CITY-024](../invariants/city.md#inv-city-024)).

## Consecuencias

- **Positivas:** `RN-CITY-020` sigue literal; ninguna consulta de historia cambia; la tomada se carga, se recuenta y
  se valida como cualquier ciudad.
- **Negativas:** los clientes ven caer una ciudad y aparecer otra con otro id en el mismo sitio, y sus edificios
  cambian de `cityId`: el protocolo lo anuncia con `takenCityId` en `city.fallen` y los vuelve a mandar con
  `building.spawn`.
- **Riesgo:** un edificio que la RAM tiene en obra y la base completo (o al revés) en el instante de la caída; la
  transacción manda, y la RAM aplica lo que la base devuelve.

## Alternativas consideradas

- **`UPDATE cities SET owner_player_id`**: rompe `RN-CITY-020`, deja la historia apuntando a una ciudad que ya no es
  del jugador con el que se hizo y pide revisar cada consulta por dueño.
- **Copiar los edificios con ids nuevos**: pierde la vida dañada y el id que el cliente ya conoce, sin ganar nada.
