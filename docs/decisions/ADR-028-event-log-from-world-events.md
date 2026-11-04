# ADR-028: El registro se lee de `world_events` desde la conexión; cada suceso nombra a los dos jugadores y lo visto sólo cubre lo asentado

Propósito: fijar cómo el slice del registro ([SPEC-EVENT-LOG](../specs/event-log.md)) lee los sucesos de un jugador
sin tocar el tick loop, qué añade a los eventos y al esquema, y cómo marca lo visto sin esconder un evento escrito
tarde.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-03** |
| Ámbito | `internal/domain/eventlog` (agrupación), `internal/persistence/postgres` (`events.go`, `eventlog.go` y los escritores de los eventos de §4.1 de la spec), `internal/websocket` (los dos comandos), migración `000010`, `packages/protocol` (`event_log.*`), cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md) (la cola escribe el evento con su efecto), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (reintentos), [ADR-026](ADR-026-pillage-two-phase-stock-debit.md) (`CityPillaged`), [ADR-027](ADR-027-hot-treaties.md) (los eventos de tratado) |

---

## Contexto

`world_events` es, desde M2, el registro append-only de los eventos de dominio: cada muerte, cada golpe de saqueo y
cada transición de un tratado escribe su fila en la misma transacción que su efecto. Nadie lo lee fuera de los tests.
Para enseñarle a un jugador lo que le pasó hay tres huecos:

- Cada fila lleva un solo `player_id`: la víctima en `UnitKilled` y `CityPillaged`, y quien propuso en los tratados.
  El autor de una muerte no encuentra la suya, y el otro firmante de un tratado tampoco.
- Faltan datos para contarlo: `UnitKilled` no dice qué unidad murió, `TreatyBreakAnnounced` no dice quién rompió, un
  `TreatyExpired` no dice si caducó una oferta o un tratado, y `occurred_at` es la hora de la escritura, que un
  reintento retrasa.
- Los `id` los asigna PostgreSQL al insertar, desde cuatro workers en paralelo: un evento puede guardarse con un
  `id` menor que otro ya visible.

## Decisión

**1. La conexión lee el registro de PostgreSQL; el loop no interviene.** `event_log.fetch` y `event_log.seen` los
atiende la goroutine de lectura de la sesión, como `session.ping`: consulta la base con un plazo de 3 s y responde.
El canon prohíbe al borde leer la RAM de la simulación (un solo escritor); el registro no es RAM, es historia
append-only que ningún otro proceso modifica. Ni los comandos ni sus escrituras pasan por `CommandTypes` o
`JobNames`. Si la base no responde a tiempo, `INTERNAL_ERROR` y la partida sigue.

**2. Cada suceso nombra a los dos jugadores.** Columna nueva `world_events.counterpart_player_id`, el otro jugador
implicado, sin cambiar lo que significa `player_id`:

| Evento | `player_id` (como hoy) | `counterpart_player_id` |
|---|---|---|
| `UnitKilled` | la víctima | el dueño del asesino |
| `CityPillaged` | la víctima | el saqueador |
| `UnitAttacked` | el atacado | el atacante |
| Los siete de tratado | quien propuso (`player_a` en los administrativos, que no tienen) | el otro firmante |

Un registro es `player_id = P OR counterpart_player_id = P`, con un índice por cada columna.

**3. Lo que falta, en el payload** (aditivo: `jsonb`, sin migración): `atMs`, el instante de la simulación, en todos
los de la tabla; `unitType` de la víctima en `UnitKilled`; `from`, el estado anterior, en los de tratado; y `byPlayerId`
en `TreatyBreakAnnounced`. La hora que se muestra es `atMs`, o `occurred_at` en las filas anteriores a este ADR.

**4. La agrupación se hace al leer, en `internal/domain/eventlog`, sin estado.** Cada fila se traduce, para quien
lee, en un tipo de entrada (víctima o autor de una muerte, de un saqueo; oferta recibida, respondida o caducada;
ruptura anunciada por el otro; tratado terminado) o en nada (lo que hizo él mismo en la diplomacia). Las filas se
recorren de la más nueva a la más vieja, y cada una se suma al grupo abierto de su clave —tipo, otro jugador, tipo de
unidad y si está por encima o por debajo del cursor— si entre las dos no hay más de
`EO_EVENT_LOG_GROUP_GAP_MINUTES`; si no, abre otro.

**5. La página es de filas, no de grupos.** Se recorren las filas de `id < beforeId` de los últimos
`EO_EVENT_LOG_MAX_DAYS` días hasta que la siguiente abriría la entrada número `EO_EVENT_LOG_PAGE_SIZE + 1`. Esa fila
no entra, y la página devuelve `nextBeforeId`, el menor `id` incluido; `null` cuando no queda nada. Todas las filas
por encima de `nextBeforeId` están en esta página o en una anterior, así que paginar no salta ninguna. A cambio, un
grupo que cae en el borde sale partido en dos entradas, una en cada página.

**6. Lo visto sólo cubre lo asentado.** El cursor es `players.event_log_seen_id`. Al servir una página, la sesión
guarda su **tope marcable**: el mayor `id` de las filas de ese jugador con `occurred_at <= now() − S`, donde `S` es el
doble del plazo de un intento de la cola (10 s) más 5 s de holgura, 25 s. `event_log.seen { upToId }` deja el cursor
en `GREATEST(cursor, LEAST(upToId, tope))`: nunca retrocede, y nunca pasa del tope de la última página que esa sesión
sirvió. Sin página servida, no hace nada.

Por qué basta: sea `C` una fila con `occurred_at <= F − S`, donde `F` es el instante en que se sirvió la página, y
sea `X` cualquier fila con `id` menor. `X` tomó su `id` antes que `C`, así que su transacción empezó antes de
`C.occurred_at + D`, donde `D` = 10 s es lo más que dura un intento: el contexto lo cancela. Por tanto `X` terminó,
confirmada o deshecha, antes de `F − S + 2D < F`. Si `X` se confirmó, estaba en la base al servir la página; si se
deshizo, el reintento la escribe con un `id` nuevo, mayor que el cursor. El cursor no puede pasar por encima de
algo que el jugador no tuvo delante (`INV-PERSIST-006`). Lo de los últimos 25 s se muestra, pero queda sin marcar
y vuelve a salir la próxima vez: mejor repetir que perder.

**7. Esquema, migración `000010`:**

| Cambio | Para qué |
|---|---|
| `world_events.counterpart_player_id uuid NULL` (FK a `players`, `ON DELETE SET NULL`) | §2 |
| Índice parcial `(counterpart_player_id, occurred_at DESC) WHERE counterpart_player_id IS NOT NULL` | El par del que ya existe por `player_id` |
| `players.event_log_seen_id bigint NOT NULL DEFAULT 0` | §6 |

## Alternativas descartadas

- **Que la simulación lea y empuje el registro.** El loop no debe esperar a PostgreSQL, y empujar cada suceso en vivo
  obliga a darle un `id` antes de que la base lo asigne, el problema que `EO-120` tuvo con los movimientos.
- **Una tabla propia de avisos por jugador.** Duplicaría cada evento en otra escritura de la misma transacción, con
  otra tabla que retener y que vaciar en los tests, para contar lo mismo que `world_events` ya guarda.
- **Escribir los eventos de tratado dos veces, una por firmante.** Rompe el registro de auditoría: un suceso, una fila.
- **Cursor por `id` sin tope, o por `occurred_at`.** Los dos esconden la fila que se confirma tarde con un `id` o una
  hora menores (§6).
- **Agrupar al escribir.** Exige leer y reescribir el grupo abierto en cada golpe, en la transacción del saqueo:
  una escritura más en el camino caliente para algo que sólo se mira al volver.

## Consecuencias

### Positivas

- El loop, la cola y sus catálogos no cambian: sólo los escritores de los eventos añaden una columna y datos al
  payload.
- Las filas anteriores siguen sirviendo con lo que tienen: su `player_id` y `occurred_at`.

### Negativas

- Una página lee varias filas por entrada (un saqueo son decenas). El recorrido va en bloques de 500 filas hasta
  completar la página, con un tope de 5.000 filas: un registro patológico corta la página antes y la siguiente sigue
  desde ahí.
- Los eventos de tratado anteriores a la migración no llegan al otro firmante.

### Neutras

- Lo de los últimos 25 s puede salir dos veces en «Mientras no estabas».
