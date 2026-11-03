# Economía: agotamiento del bosque

Slice que convierte el bosque en un recurso finito: cada tile de bosque guarda una cantidad de
madera, la recolección la gasta y un tile agotado deja de dar madera hasta que vuelve a crecer.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-ECON-FOREST` |
| Estado | Implemented (2026-11-03). Aceptada el 2026-11-03 con las preguntas de §10 decididas ese día con la recomendación de cada una; en `main` desde `a35bc1d`, con la CI en verde el mismo día. |
| Milestone | E4 Bosque (cuarto slice después de M7) |
| Canon | §5 (terreno), §12 (persistencia), §16 (errores) |
| Depende de | [economy-wood.md](economy-wood.md), [city.md](city.md), [websocket-protocol.md](websocket-protocol.md), [movement.md](movement.md) |
| Decisiones | [ADR-025](../decisions/ADR-025-forest-wood-layer.md): la madera del bosque es una capa aparte del terreno, que sigue inmutable, escrita por deltas en la transacción de la carga. |
| Reemplaza a | La frase «el bosque no se agota en este slice» de `RN-ECON-001` y la decisión Q3 de [economy-wood.md](economy-wood.md) |
| Invariantes | Cita los existentes de §8 y asigna `INV-ECON-007` e `INV-ECON-008` ([../invariants/economy.md](../invariants/economy.md)) |

## 1. Objetivo

Que el espacio sea un recurso disputado (pilar 5 de [vision.md](../product/vision.md)). Hoy el bosque de al lado
da madera para siempre y nadie tiene motivo para salir de casa. Con este slice, el bosque cercano se acaba: hay que
ir más lejos, y lejos están las incursiones (E2) y hacen falta los guardias (E3).

## 2. Scope

**Entra.** La madera restante de cada tile de bosque; su gasto al cargar; el tile agotado y su regeneración;
qué hace un aldeano cuando su tile se agota; el rechazo de `unit.gather` sobre un tile agotado; la madera
restante en el snapshot y un delta nuevo cuando cambia; su dibujo en el cliente; el criterio de fundación con
bosque agotado.

**No entra** (`Fuera de MVP` de este slice): talar para abrir camino (el terreno no cambia de tipo, §4.1); otros
recursos; plantar o acelerar la regeneración; que el coste de movimiento cambie al talar; capacidad de almacén;
reasignar aldeanos automáticamente a otra tarea.

## 3. Actores

Jugador (ordena recolectar), cliente web (dibuja el bosque según su madera), Game Server (único que decide cuánto
queda, cuándo se agota y cuándo vuelve, [INV-SEC-001](../invariants/security.md#inv-sec-001)), tick loop (fase 5
para la carga y la regeneración), PostgreSQL.

## 4. Modelo

### 4.1 Una capa aparte del terreno

El terreno es inmutable por diseño: se regenera desde la semilla en cada arranque y se compara byte a byte con
`world_chunks` ([INV-WORLD-005](../invariants/world.md#inv-world-005)), y cada sesión recibe el de un chunk una
sola vez porque no cambia. Agotar un bosque **no cambia su terreno**: un tile `FOREST` agotado sigue siendo
`FOREST`, transitable y con su coste de movimiento de siempre (Q4).

Lo que cambia vive en una capa aparte y dispersa, **la madera del bosque**: para cada tile `FOREST` que alguien
ha empezado a talar, cuánta madera le queda y cuándo vuelve a crecer. Un tile sin entrada en la capa está lleno.
Así el mundo recién generado no ocupa nada, y la capa sólo crece con lo que los jugadores tocan.

| Concepto | Valor |
|---|---|
| Capacidad de un tile | `forestWood` = 100 madera (Q1) |
| Madera restante | `0 ≤ remaining ≤ forestWood`; sin entrada = `forestWood` |
| Agotado | `remaining = 0` |
| Regeneración | El tile vuelve a `forestWood` (y sale de la capa) `forestRegrowHours` = 24 h después de su **última** carga (Q2) |

### 4.2 Reglas

Dominio: `RN-ECON`, continuando la numeración de [economy-wood.md](economy-wood.md).

| ID | Regla |
|---|---|
| `RN-ECON-013` | Al vencer `WORKING`, la unidad carga `min(carry, remaining)` del tile donde trabaja y el tile pierde exactamente eso. Si varias unidades cargan del mismo tile en el mismo tick, cargan en orden de `id` de unidad: la última puede llevarse menos, o nada. |
| `RN-ECON-014` | `unit.gather` sobre un tile agotado se rechaza con `INVALID_TARGET` y un mensaje claro; no amplía el catálogo de errores. |
| `RN-ECON-015` | **Tile agotado durante la tarea** (Q3). Si al cargar el tile queda agotado, o ya lo estaba al llegar, la unidad busca el tile `FOREST` con madera más cercano a su tile de trabajo, a distancia ≤ `forestSearchRadius` = 6, alcanzable a pie (desempate por distancia, después `y`, después `x`). Si lleva carga, primero la deposita y el siguiente viaje va al tile nuevo; si no lleva nada, va directa. Si no hay ninguno, deposita lo que lleve y queda `IDLE` en el tile de depósito. |
| `RN-ECON-016` | La regeneración es de la fase 5: un tile cuyo instante de regeneración ha vencido vuelve a estar lleno y sale de la capa. La regeneración se ancla a instantes absolutos, como los temporizadores de [economy-wood.md](economy-wood.md): un reinicio no la adelanta ni la retrasa. |
| `RN-ECON-017` | **Fundación** (Q5). `RN-CITY-023` cuenta sólo tiles `FOREST` con madera y exige al menos `foundingForestTiles` = 10 tiles con madera alcanzables a ≤ `forestRadius` = 8. Una ciudad existente no tiene garantía: su bosque puede agotarse y crece otra vez con el tiempo. |
| `RN-ECON-018` | La madera del bosque es **pública**: cualquier jugador ve cuánta le queda a un tile que está en su área de interés (Q6). |

Lo que no cambia: la ruta, el depósito, la pérdida de lo cargado al cancelar (`RN-ECON-003`) y la regla de la
ausencia (`RN-ECON-005`) son las de [economy-wood.md](economy-wood.md).

**Precisión (2026-11-03).** Lo que la implementación decide dentro de estas reglas:

- La búsqueda del tile siguiente prueba el pathfinding sólo con los ocho candidatos más cercanos
  (`forest.SearchAttempts`): acota el coste del A* cuando el bosque de alrededor está muy talado.
- La regeneración es exacta en la lógica: un tile cuyo instante ya pasó cuenta como lleno aunque el barrido de la
  fase 5, una vez por segundo, no lo haya sacado todavía de la capa. El barrido sólo lo limpia y lo anuncia.
- Un aldeano cuyo tile se agota mientras trabaja y que no encuentra otro vuelve al depósito sin carga y su tarea
  termina allí, con el motivo `FOREST_DEPLETED`.
- La fundación lee de la base los tiles agotados: corre fuera del game loop y no puede leer su RAM. La base va un
  instante por detrás de la RAM, que para elegir un emplazamiento basta.
- El golden «tala» (`testdata/scenarios/tala.json`) fija el recorrido entero: tres aldeanos, un bosque de tres
  tiles con 25, 10 y 0, y 35 maderas en total.

## 5. Errores

| Código | Condición | Estado |
|---|---|---|
| `INVALID_TARGET` | `unit.gather` sobre un tile `FOREST` agotado (`RN-ECON-014`) | Existente |

Los demás rechazos de `unit.gather` no cambian.

## 6. Eventos y contratos de red

Cambios **aditivos** del protocolo v1 ([websocket-protocol.md](websocket-protocol.md) §3, `R-VER-2` y `R-VER-3`):

- `world.snapshot` gana el campo opcional `forests: [{ x, y, remaining, regrowsAtMs }]`, con los tiles del área de
  interés que **no** están llenos. Un cliente antiguo lo ignora.
- Mensaje nuevo servidor → cliente `forest.update { x, y, remaining, regrowsAtMs }`, difundido al chunk del tile
  cada vez que cambia su madera (carga o regeneración). Con `remaining = forestWood` y `regrowsAtMs = null`, el
  tile vuelve a estar lleno.

Eventos de dominio en `world_events`: `ForestDepleted` cuando un tile llega a 0 (con la ciudad de quien dio la
última carga). La regeneración no deja evento: es un hecho del reloj, no de un jugador.

El cliente dibuja un tile agotado como tala (tocones) y uno a medias, menos denso; el detalle es de
[../architecture/frontend.md](../architecture/frontend.md).

## 7. Persistencia

| Pregunta (canon §12) | Respuesta |
|---|---|
| Autoritativo en RAM | La capa de madera: `remaining` y el instante de regeneración por tile tocado |
| Write-through | La carga: la transición `WORKING → TO_DEPOSIT` de la tarea ya es transaccional ([economy-wood.md](economy-wood.md) §7), y el descuento del tile va en **la misma transacción**. Así lo que pierde el bosque es exactamente lo que gana la carga (`INV-ECON-008`) |
| Dirty-flag | Nada |
| Reconstruible / no persistido | La regeneración vencida: al arrancar, un tile cuyo instante ya pasó se carga lleno (y su fila se borra) |

Tabla nueva `forest_wood (x, y, remaining, regrows_at)`, con clave `(x, y)` y `CHECK (remaining BETWEEN 0 AND
100)`. Migración `000008`.

## 8. Invariantes

Cita [INV-WORLD-005](../invariants/world.md#inv-world-005) (el terreno no cambia: este slice lo respeta por
diseño), [INV-ECON-001](../invariants/economy.md#inv-econ-001) e
[INV-ECON-005](../invariants/economy.md#inv-econ-005). Asigna:

- [INV-ECON-007](../invariants/economy.md#inv-econ-007) — La madera de un tile está siempre entre 0 y su capacidad, y sólo existe para tiles `FOREST`.
- [INV-ECON-008](../invariants/economy.md#inv-econ-008) — Una carga nunca crea madera: lo que gana la unidad es exactamente lo que pierde el tile.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| unit | Carga parcial y nula; dos aldeanos sobre el mismo tile en el mismo tick (orden de `id`); búsqueda del tile siguiente en el borde del radio y con desempates; sin tile cercano (`IDLE` en el depósito); `unit.gather` sobre tile agotado; regeneración en su instante exacto; fundación con el umbral de `RN-ECON-017` |
| simulation | Golden «tala»: tres aldeanos agotan un tile, pasan al siguiente y se paran al acabar el bosque cercano, con los ticks exactos de cada carga |
| recovery | Reinicio con tiles a medias y con una regeneración vencida durante la caída |
| integration | Transacción de carga con fallo inyectado (ni tile ni carga a medias); `CHECK` de la tabla |
| contract | `forests` en el snapshot y `forest.update` contra el JSON Schema |
| cliente | Dibujo del tile a medias y agotado; `scripts/smoke.mjs` elige un bosque con madera |

## 10. Decisiones de producto (2026-11-03)

Preguntas que la propuesta dejó abiertas; producto aceptó la recomendación de cada una. Son decisiones de
producto, no de ingeniería: cambiarlas es cambiar la spec.

Datos del mundo de desarrollo (2026-11-03) para decidir: el 19 % de los tiles es bosque, pero varía mucho entre
ciudades. A ≤ 8 tiles del centro, la ciudad 5 tiene 16 tiles de bosque y la ciudad 6, 280; las ciudades 1 y 2 son
anteriores a `RN-CITY-023` y no tienen ninguno. Tres aldeanos juntan unas 90 maderas por minuto.

| # | Pregunta | Decisión |
|---|---|---|
| Q1 | ¿Cuánta madera tiene un tile de bosque? | **100** (diez viajes). Con tres aldeanos, la ciudad 5 agota su bosque cercano en unos 18 minutos de juego y la ciudad 6 no lo nota en una sesión: emplazarse importa, que es el pilar. |
| Q2 | ¿Vuelve a crecer? | Sí, entero, **24 horas** después de su última carga. Sin regeneración, un mundo persistente acaba pelado y los recién llegados nacen en un desierto; con 24 h, dentro de una sesión el bosque talado está talado. Alternativas descartadas: no crecer nunca, o crecer poco a poco (más mensajes y menos claro). |
| Q3 | ¿Qué hace un aldeano cuando su tile se agota? | Busca solo el tile con madera más cercano a ≤ 6 tiles y sigue; si no hay, deposita y se para. Es lo que espera quien viene de Age of Empires, y pararse en seco con 100 madera por tile obligaría a dar una orden cada diez viajes. |
| Q4 | ¿Un tile agotado cambia algo más que la madera? | No: sigue siendo bosque para moverse (mismo coste) y para la vista. Cambiar el terreno rompería su inmutabilidad, que sostiene la verificación del mundo y el envío del terreno una sola vez por sesión. Talar para abrir camino queda fuera. |
| Q5 | ¿Qué garantiza la fundación? | Que la ciudad nueva tenga al menos **10 tiles con madera** (1 000 maderas) alcanzables a ≤ 8. Hoy basta uno, que con agotamiento serían 100 maderas: ni una casa y un aldeano. Las ciudades existentes no tienen garantía y dependen de la regeneración. |
| Q6 | ¿Ve un jugador cuánta madera le queda al bosque de otro? | Sí: es terreno a la vista de todos, y ver un bosque talado cerca de una ciudad es información de juego legítima. |
| Q7 | Números. | Punto de partida: 100 por tile, 24 h, radio de búsqueda 6, umbral de fundación 10 tiles. Se ajustan probando, sin tocar la spec mientras sigan siendo parámetros. |

## 11. Documentos relacionados

- [economy-wood.md](economy-wood.md) (la recolección que esta spec limita), [city.md](city.md) `RN-CITY-023`.
- [../invariants/world.md](../invariants/world.md) (`INV-WORLD-005`), [../architecture/networking.md](../architecture/networking.md)
  (terreno una vez por sesión).
- [combat-raid.md](combat-raid.md) y [combat-response.md](combat-response.md): el riesgo de ir lejos a por madera.
