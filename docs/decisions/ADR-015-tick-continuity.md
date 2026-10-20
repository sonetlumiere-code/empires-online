# ADR-015: El tick se reancla al reloj en cada arranque

Propósito: fijar de dónde arranca el contador de ticks tras un reinicio, de modo que ningún número de tick se
repita nunca, tampoco después de una caída, y qué se hace si el reloj de la máquina va hacia atrás.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-10-20** |
| Ámbito | `internal/game/loop`, `cmd/server` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-007](ADR-007-game-loop-frequency.md), [ADR-011](ADR-011-movement-timed-polyline.md) |

---

## 1. Contexto

`tickNumber` es la etiqueta lógica del game loop: sella el campo `tick` de los logs estructurados, el de
`world.snapshot` y, sobre todo, la columna `tick` de `world_events`, que es durable. El loop lo incrementa en
uno por tick ejecutado; los ticks que descarta cuando va por detrás no se cuentan
([../architecture/game-loop.md](../architecture/game-loop.md) §2).

Al arrancar, el contador se retoma de `world_state.current_tick`, que **sólo se escribe en el apagado
ordenado**. Tras una caída, ese valor es el del último apagado ordenado, que puede ser de hace horas: el
contador retrocede y vuelve a emitir números que ya se usaron. Dos eventos de `world_events` separados por la
caída pueden compartir tick, o quedar ordenados al revés, y el orden causal que el tick debía dar se pierde
([INV-WORLD-004](../invariants/world.md#inv-world-004)). La ficha de ese invariante describía además una
comprobación de arranque contra el reloj que no existía.

Dos hechos hacen posible una solución sin escrituras nuevas:

- `world_state.epoch_ms` existe desde la creación del mundo y no cambia.
- El loop nunca ejecuta más de un tick por período: el calendario es de tiempo absoluto y los ticks perdidos
  se descartan, no se recuperan en ráfaga. Por tanto, en todo momento, `tick <= (ahora − epoch_ms) / período`,
  siempre que el reloj no retroceda.

---

## 2. Decisión

1. **Al arrancar, el tick se reancla al reloj:**

   ```
   startTick = max(world_state.current_tick, (ahora − epoch_ms) / tickDurationMs)
   ```

   El primer tick que ejecuta el proceso es `startTick + 1`. Por la cota de §1, el valor derivado del reloj
   es mayor o igual que cualquier tick emitido antes, se apagara el proceso como se apagara; y como arrancar
   lleva más de un período, el primer tick nuevo es estrictamente mayor que el último emitido. No hace falta
   haber guardado nada.

2. **Durante la ejecución no cambia nada**: el contador avanza en uno por tick ejecutado. Derivarlo del reloj
   en cada tick haría que dos ticks del mismo período, por jitter del temporizador, compartieran número, y
   que el volcado periódico —que se programa por múltiplos del tick— se saltara.

3. **`world_state.current_tick` se sigue guardando en el apagado ordenado**, y deja de ser el origen del
   contador: es sólo un suelo frente a un reloj que va hacia atrás.

4. **Si el reloj va por detrás del último tick guardado** —un ajuste NTP hacia atrás, una máquina virtual
   restaurada de un snapshot—, el arranque **no aborta**: arranca desde el valor guardado, que preserva la
   monotonía frente a todo lo que se guardó, y emite `invariant_violation` con `inv_id=INV-WORLD-004`. El
   tick no gobierna la simulación —el tiempo de juego lo da el reloj, y la posición se deriva de él—: es una
   etiqueta, y convertir un ajuste de reloj en una caída del servidor no protege nada.

---

## 3. Alternativas consideradas

- **Guardar el tick periódicamente**, con cada volcado. Estrecha la ventana a 5 s pero no la cierra: los ticks
  entre el último guardado y la caída se repiten igual. Y añade una escritura que el reloj hace innecesaria.
- **Retomar desde `max(tick)` de `world_events`.** Protege sólo lo que llegó a disco como evento, cuesta una
  consulta por arranque y no dice nada de los logs. El reloj da una cota mejor sin leer nada.
- **Derivar el tick del reloj siempre**, también durante la ejecución. Descartada por §2.2.
- **Abortar el arranque si el reloj va por detrás**, como describía la ficha. Descartada por §2.4.

---

## 4. Consecuencias

- Tras un reinicio el tick **salta**: el salto es el tiempo que el proceso estuvo parado, más los ticks que
  descartó por ir por detrás. Un mundo creado hace diez días arranca hacia el tick 8,6 millones aunque el
  servidor sólo haya ejecutado unos miles: el tick de cada arranque es, aproximadamente, «ticks desde
  `epoch_ms`». Nadie usa el tick como duración, sólo como orden, así que el salto no rompe nada.
- Ningún número de tick se repite entre arranques, salvo que el reloj retroceda más allá del último tick
  guardado **y** el proceso hubiera caído después de ese guardado. Es un doble fallo, y queda registrado.
- `world_state.current_tick` deja de ser fuente: si se perdiera, el arranque seguiría siendo monótono.
- Los *simulation tests* no cambian: construyen el loop con un `StartTick` explícito y lo conducen con
  `FakeClock`.

---

## 5. Verificación

- `TestElTickSeReanclaAlRelojAlArrancar` (unit, `internal/game/loop`) — tras una caída, con el último
  guardado muy por detrás del reloj, el tick arranca en el valor del reloj; con el reloj por detrás de lo
  guardado, arranca en lo guardado y lo señala; un mundo recién creado arranca en cero.
- `TestElTickEsEstrictamenteCreciente` (simulation, `internal/game/simulation`) — el contador avanza en uno
  por tick ejecutado, con el reloj quieto o dando saltos.

---

## 6. Referencias

- [../invariants/world.md](../invariants/world.md) — `INV-WORLD-004`.
- [../architecture/game-loop.md](../architecture/game-loop.md) §2 — `tickNumber`, `epoch_ms` y `tickTime`.
- [ADR-007](ADR-007-game-loop-frequency.md) — frecuencia del loop y política ante un tick que se pasa de
  tiempo.
