# Invariantes de Unidad (INV-UNIT-xxx)

Propiedades de posición, estado, salud y pertenencia de las unidades del mundo.

Formato y severidades: [README.md](README.md). Estados y tipos de unidad: canon §10. Movimiento: [movement.md](movement.md).

---

## Contexto

`units.status` toma exactamente uno de cinco valores (canon §10): `IDLE`, `MOVING`, `GARRISONED`, `HIDDEN`, `DEAD`.

| Estado | Ocupa tile del mundo | Acepta órdenes | Visible en deltas de chunk |
|---|---|---|---|
| `IDLE` | Sí | Sí | Sí |
| `MOVING` | Sí (posición derivada del movimiento) | Sí (una nueva orden cancela la anterior) | Sí |
| `GARRISONED` | **No** | No en MVP | No |
| `HIDDEN` | Sí | Sí | No (reservado: SafeZone) |
| `DEAD` | No | **No** | No |

El único `unit_type` del MVP es `VILLAGER`. Sus estadísticas viven en el **catálogo** `internal/domain/unit`, no repartidas por el código: `{MaxHP: 40, BaseMsPerTile: 600, PopulationCost: 1}`, consultables por `unit.Lookup(Type) (Definition, error)`. `TOWN_CENTER` es un *building*, no una unidad, y no aparece en `units`.

> **Nota sobre nombres de constraint.** Las constraints reales de `units` en `000001_initial_schema.up.sql` son: `units_status_valid` (`CHECK (status IN ('IDLE','MOVING','GARRISONED','HIDDEN','DEAD'))`), `units_hp_within_max` (`CHECK (hp <= max_hp)`), los `CHECK` de columna `hp >= 0` y `max_hp > 0`, y las claves foráneas **en línea** de `player_id` (`ON DELETE CASCADE`) y `city_id` (`ON DELETE SET NULL`). No existen `ck_units_position_bounds`, `ck_units_hp_range` ni `units.version`: este documento cita los mecanismos reales.

El combate está **fuera de MVP** (canon §21). Las unidades no pierden `hp` en MVP; los invariantes sobre `hp` y sobre `DEAD` se especifican y se testean ahora para que el sistema de combate nazca sobre un contrato ya fijado, y así se marca en cada ficha.

---

<a id="inv-unit-001"></a>
## INV-UNIT-001 — La posición referencia un tile válido y transitable

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto** por `TestRechazosDeMovimiento` (`TARGET_NOT_WALKABLE`, `TARGET_OUT_OF_BOUNDS`) y `TestVerticalSliceMovimiento` (`internal/game/simulation`), más `TestFueraDeLimitesEsIntransitable` (`internal/game/world`) |

**Enunciado.** Para toda unidad cuyo estado implica presencia en el mundo (`IDLE`, `MOVING`, `HIDDEN`), su posición `(x, y)` cumple [INV-WORLD-001](world.md#inv-world-001) y el tile correspondiente es transitable: terreno con `walkable = sí` y sin marca en la capa de ocupación.

**Razón.** Una unidad sobre terreno intransitable es un estado del que el sistema no puede salir: el pathfinder no expande tiles bloqueados, luego no puede calcular ningún camino **desde** ese tile. La unidad queda permanentemente inmóvil, con todas sus órdenes rechazadas por `PATH_NOT_FOUND`, y el jugador sin recurso alguno. Es una pérdida de activo silenciosa e irreversible.

Fuera de límites es peor: indexa fuera del array de terreno y calcula un `chunkId` arbitrario, lo que emite la unidad a suscriptores incorrectos.

La excepción de `GARRISONED` y `DEAD` es deliberada y se detalla en [INV-UNIT-003](#inv-unit-003): esas unidades no ocupan tile, y exigirles una posición transitable impondría restricciones sobre un dato que ya no tiene significado espacial.

**Cómo se garantiza.**

- `DOMAIN` — la posición inicial de los tres aldeanos se elige entre tiles transitables adyacentes a la ciudad ([INV-PLAYER-003](player.md#inv-player-003)); si no hay ninguno, la creación falla con rollback.
- `DOMAIN` — durante el movimiento la posición no se escribe libremente: se **deriva** de la polilínea (canon §7), cuyos waypoints son todos transitables por [INV-MOVE-004](movement.md#inv-move-004) y [INV-WORLD-003](world.md#inv-world-003). Mientras el movimiento sea válido, la posición derivada lo es por construcción.
- `DOMAIN` — al completar o cancelar un movimiento se hace *snap* a un waypoint de la polilínea, nunca a una posición interpolada ni a un tile calculado aparte ([INV-MOVE-008](movement.md#inv-move-008)).
- `DOMAIN` — validación de arranque: al cargar unidades se verifica que toda unidad presente en el mundo está sobre tile transitable.

> **No hay garantía `DB`.** La tabla `units` de `000001_initial_schema.up.sql` declara `x integer NOT NULL` e `y integer NOT NULL` **sin ningún `CHECK`**: ni de límite inferior ni superior. El límite superior es `EO_WORLD_WIDTH`/`EO_WORLD_HEIGHT`, que es configuración y no puede empotrarse como literal en una migración; la transitabilidad tampoco es expresable como `CHECK`, porque el terreno vive en `world_chunks` como `bytea`. Un `INSERT` directo con `x = 99999` **es aceptado por PostgreSQL**. Todo el invariante se garantiza en `DOMAIN` y `TEST`, y la ficha lo declara así en lugar de prometer una constraint inexistente.

**Cómo se verifica.**

- `TestRechazosDeMovimiento` (simulation, `internal/game/simulation`) — **existe y pasa**: los seis rechazos del pipeline, incluidos `TARGET_OUT_OF_BOUNDS` y `TARGET_NOT_WALKABLE`; un comando rechazado no produce ningún movimiento, así que la unidad no puede acabar fuera del mundo ni sobre un tile bloqueado.
- `TestVerticalSliceMovimiento` (simulation, `internal/game/simulation`) — **existe y pasa**: la posición derivada en cada tick de un movimiento completo cae siempre sobre waypoints de una polilínea ya validada.
- `TestFueraDeLimitesEsIntransitable` (unit, `internal/game/world`) — **existe y pasa**: los bordes del mundo se comportan como un muro.
- Test previsto: `Test_INV_UNIT_001_StartupDetectsUnitOnBlockedTile` (integration) — una unidad persistida sobre `WATER` provoca fallo de arranque.
- Test previsto: `Test_INV_UNIT_001_OutOfBoundsRejectedInDomain` (unit) — el dominio rechaza una posición fuera de rango; **no** se espera que lo haga la base de datos, que no tiene esa constraint.

**Violación en runtime.** Detección en el constructor de unidad, en la validación de arranque y en la aserción posterior al snap de finalización de movimiento. Log `invariant_violation` con `inv_id=INV-UNIT-001`, `unitId`, posición y terreno encontrado. Política `FAIL_FAST` en arranque. En el loop, la unidad se excluye de la simulación y se escala; **no** se la teletransporta al tile transitable más cercano, porque mover la unidad de un jugador sin orden suya es un efecto de gameplay que no puede decidir un mecanismo de reparación.

---

<a id="inv-unit-002"></a>
## INV-UNIT-002 — Una unidad `DEAD` no acepta órdenes

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M4 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por el caso «unidad muerta» de `TestRechazosDeMovimiento` (`internal/game/simulation`) |

**Enunciado.** Ningún comando dirigido a una unidad con `status = DEAD` produce efecto alguno: todos se rechazan con `UNIT_DEAD`, y una unidad `DEAD` nunca vuelve a otro estado.

**Razón.** `DEAD` es un estado **terminal**. Si acepta órdenes, se abre la reanimación implícita: una unidad muerta que recibe `unit.move` y pasa a `MOVING` ha resucitado sin ninguna mecánica que lo justifique, con todo el valor material que eso implica. La terminalidad también sostiene la contabilidad: la métrica `eo_active_units` y la `population` de la ciudad se apoyan en que una unidad muerta ya no vuelve a contar.

**Cómo se garantiza.**

- `DOMAIN` — la validación de estado de unidad es el paso 2 del pipeline de `unit.move` (canon §7: ownership → estado → destino → A\* → crear → simular → notificar). Se ejecuta **después** de ownership, para no revelar el estado de unidades ajenas ([INV-PLAYER-001](player.md#inv-player-001)), y **antes** de cualquier trabajo caro como el pathfinding.
- `DOMAIN` — el rechazo lo decide un punto único, `unit.EnsureCanMove() error`, que devuelve `unit.ErrDead` para `DEAD` y `unit.ErrGarrisoned` para `GARRISONED`. No es un `if status == DEAD` disperso por los handlers: la capa de aplicación traduce el error de dominio al código estable con `moveErrorCode`.
- `DB` — `CONSTRAINT units_status_valid CHECK (status IN ('IDLE','MOVING','GARRISONED','HIDDEN','DEAD'))` cierra el dominio del campo, de modo que `DEAD` no puede confundirse con una variante de capitalización que se colara por otra rama.
- `DOMAIN` — al marcar una unidad como `DEAD` se cancela su movimiento activo si lo hubiera, en la misma transacción, dejando el movimiento en `CANCELLED` ([INV-UNIT-005](#inv-unit-005)).
- El código de error `UNIT_DEAD` es estable (canon §16) y distinto de `UNIT_NOT_MOVABLE`, que cubre los casos de estado no móvil sin muerte.

**Cómo se verifica.**

- `TestRechazosDeMovimiento` / caso «unidad muerta» (simulation, `internal/game/simulation`) — **existe y pasa**: un `unit.move` sobre una unidad `DEAD` responde `UNIT_DEAD` y no emite ningún `unit.movement.started`.
- `TestEstadosDeMovimiento` (unit, `internal/domain/movement`) — **existe y pasa**: los estados terminales del movimiento asociado no se reabren.
- Test previsto: `Test_INV_UNIT_002_DeadIsTerminal` (unit) — ninguna arista sale de `DEAD`; verificado exhaustivamente sobre los cinco estados.
- Test previsto: `Test_INV_UNIT_002_DeathCancelsActiveMovement` (integration) — una unidad `MOVING` que muere deja su movimiento en `CANCELLED`, no en `ACTIVE`.

**Violación en runtime.** Detección en `unit.EnsureCanMove` y en la validación de comando. Log `invariant_violation` con `inv_id=INV-UNIT-002`, `unitId` y el comando intentado. Política `REJECT` con `UNIT_DEAD`. Si se detecta una unidad que **salió** de `DEAD`, es corrupción: la unidad se excluye de la simulación y se escala; no se la vuelve a matar automáticamente porque ya pudo haber producido efectos.

> El combate está **fuera de MVP**. En MVP ninguna mecánica produce `DEAD`; el invariante se implementa y se testea para que la transición nazca correcta.

---

<a id="inv-unit-003"></a>
## INV-UNIT-003 — Una unidad `GARRISONED` no ocupa tile del mundo

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR |
| Cobertura | **Parcial**: `TestRechazosDeMovimiento` / caso «unidad guarnecida» (`internal/game/simulation`) cubre el rechazo de órdenes; el efecto sobre la ocupación y los deltas **no tiene test todavía**, porque la lógica de guarnición está diferida |

**Enunciado.** Una unidad con `status = GARRISONED` no figura en el índice de ocupación del mundo, no se emite en los deltas por chunk y no es candidata a origen ni destino de pathfinding; su ubicación es la guarnición registrada en `garrisons`, no un tile.

**Razón.** Es un problema de doble contabilidad. Si la unidad guarnecida sigue en el índice de ocupación, ocupa un tile que nadie ve, bloqueando paths de forma inexplicable para los jugadores: una casilla aparentemente vacía por la que no se puede pasar. Y si sigue emitiéndose en deltas de chunk, aparece en pantalla fuera de la guarnición, revelando además la presencia de tropas dentro de una ciudad ajena, que es información con valor táctico directo.

**Cómo se garantiza.**

- `DOMAIN` — la transición a `GARRISONED` retira la unidad del índice de ocupación y emite `entity.despawn` a los suscriptores del chunk, en la misma unidad de trabajo. La transición inversa la reinserta y emite `entity.spawn`.
- `DOMAIN` — el índice de ocupación se construye a partir de una única consulta que filtra por estados presentes en el mundo (`IDLE`, `MOVING`, `HIDDEN`), de modo que `GARRISONED` y `DEAD` quedan excluidos por construcción y no por un filtro repetido en cada llamada.
- `DOMAIN` — el garrison de unidades ajenas exige un `treaties` en estado `ACTIVE` con `allows_garrison` (canon §10); sin él se rechaza con `TREATY_REQUIRED`. Esa es la regla de negocio; este invariante solo cubre el efecto sobre la ocupación.
- `DOMAIN` — la columna de posición de una unidad `GARRISONED` conserva su último valor como dato histórico, pero **ninguna** lógica espacial la lee. Por eso [INV-UNIT-001](#inv-unit-001) excluye explícitamente este estado.

**Cómo se verifica.**

- `TestRechazosDeMovimiento` / caso «unidad guarnecida» (simulation, `internal/game/simulation`) — **existe y pasa**: una unidad `GARRISONED` rechaza `unit.move` con `UNIT_GARRISONED`.
- Test previsto: `Test_INV_UNIT_003_GarrisonedUnitNotInWorldOccupancy` (unit) — tras guarnecer, el índice de ocupación no contiene la unidad y un path que atraviesa su antiguo tile tiene éxito.
- Test previsto: `Test_INV_UNIT_003_GarrisonEmitsDespawn` (integration) — un observador suscrito al chunk recibe exactamente un `entity.despawn` con `reason = GARRISONED` y ningún `entity.update` posterior sobre esa unidad.
- Test previsto: `Test_INV_UNIT_003_UngarrisonRestoresOccupancy` (integration) — al salir de la guarnición, la unidad reaparece con `entity.spawn` sobre un tile transitable.

**Violación en runtime.** Detección en la reconciliación periódica que compara el índice de ocupación con los estados de `units`. Log `invariant_violation` con `inv_id=INV-UNIT-003` y `unitId`. Política `REPAIR`: se retira la unidad del índice y se emite `entity.despawn`. La reparación es segura porque el estado autoritativo es `units.status` y la ocupación es un índice derivado y reconstruible.

> `garrisons` es una tabla del MVP con **lógica mínima o diferida** (canon §11): existe en `000001_initial_schema.up.sql` con `unit_id bigint PRIMARY KEY` y su índice `garrisons_city_idx`, pero ninguna ruta de código escribe todavía en ella. El invariante se especifica ahora; su implementación completa llega con M7 (Diplomacy foundation). Las propiedades específicas de la guarnición viven en [diplomacy.md](diplomacy.md), familia `INV-GARR-*`.

---

<a id="inv-unit-004"></a>
## INV-UNIT-004 — `hp` está en `[0, max_hp]`

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | REJECT + clamp en el cálculo |
| Cobertura | **Sin cobertura ejecutada**: el combate está fuera de MVP y no hay ninguna ruta que modifique `hp`; el rechazo por `CHECK` sólo se ejercita en integración, aún no ejecutada |

**Enunciado.** Para toda unidad, `0 <= hp <= max_hp`, donde `max_hp` es el valor del `unit_type` correspondiente (`VILLAGER` = 40).

**Razón.** `hp` negativo rompe cualquier comparación de vida escrita como `hp > 0` en aritmética con signo y, si se persiste, deja una unidad viva con vida imposible. `hp` por encima de `max_hp` es una unidad invencible obtenida por un bug de curación, con impacto directo en el equilibrio en cuanto exista combate.

La disciplina que este invariante impone es concreta: **el clamp se aplica en el cálculo, no en la escritura**. Un daño de 60 sobre una unidad de 40 debe producir `hp = 0` y muerte, no `hp = -20` seguido de una corrección posterior que puede olvidarse.

**Cómo se garantiza.**

- `DB` — la migración `000001` declara **tres** constraints complementarias sobre `units`: `hp integer NOT NULL CHECK (hp >= 0)`, `max_hp integer NOT NULL CHECK (max_hp > 0)` y `CONSTRAINT units_hp_within_max CHECK (hp <= max_hp)`. Juntas cubren el enunciado; ésos son sus nombres reales.
- `DOMAIN` — cuando exista combate, toda modificación pasará por un punto único que haga el clamp a `[0, max_hp]` como parte del cálculo y devuelva el valor final. Ningún handler asignará `hp` directamente. **Hoy ese punto único no existe porque ninguna ruta modifica `hp`.**
- `DOMAIN` — `max_hp` proviene del catálogo de `internal/domain/unit` (`unit.Lookup`), no de literales dispersos (canon §17). El `40` de `VILLAGER` aparece en un único lugar.
- `DOMAIN` — `Unit.IsAlive()` es `Status != DEAD && HP > 0`: la conjunción impide tratar como viva a una unidad con cero vida aunque su `status` se quedara atrás.

**Cómo se verifica.**

- Test previsto: `Test_INV_UNIT_004_HpWithinRange` (unit) — tabla: daño mayor que `hp` deja `hp = 0` y `status = DEAD`; curación mayor que el déficit deja `hp = max_hp`; daño cero no cambia nada.
- Test previsto: `Test_INV_UNIT_004_DatabaseRejectsOutOfRange` (integration) — `UPDATE units SET hp = -1` falla por el `CHECK` de columna y `hp = max_hp + 1` falla por `units_hp_within_max`.
- Test previsto: `Test_INV_UNIT_004_ZeroHpImpliesDead` (unit) — no existe estado alcanzable con `hp = 0` y `status != DEAD`.
- Test previsto: `Test_INV_UNIT_004_FlushNeverWritesOutOfRange` (integration) — el flush periódico de `hp` (canon §12) nunca envía un valor fuera de rango.

**Violación en runtime.** Detección en el futuro punto único de modificación de `hp` y en el error de constraint. Log `invariant_violation` con `inv_id=INV-UNIT-004`, `unitId`, `hp` calculado y `max_hp`. Política: el clamp corrige el valor y la operación continúa, pero la violación se registra igualmente porque indica un cálculo mal escrito aguas arriba. Un valor fuera de rango que **llega a la base de datos** es `FAIL_FAST` del worker de persistencia: significa que se saltó el punto único de modificación.

> El combate está **fuera de MVP**: en MVP `hp` es constante e igual a `max_hp` para todo `VILLAGER`. El invariante fija el contrato por adelantado.

---

<a id="inv-unit-005"></a>
## INV-UNIT-005 — `MOVING` si y solo si existe un movimiento `ACTIVE`

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M4 |
| Política ante violación | REPAIR (derivando desde `unit_movements`) |
| Cobertura | **Cubierto** por `TestVerticalSliceMovimiento`, `TestCancelacionExplicita`, `TestNuevaOrdenReemplazaLaAnterior` y los tres tests de recuperación (`internal/game/simulation`); la constraint la cubre `TestIndiceUnicoImpideDosMovimientosActivos` (integration, **diseñado y no ejecutado**) |

**Enunciado.** Para toda unidad se cumple la bicondicional: `units.status = MOVING` ⟺ existe exactamente una fila en `unit_movements` con esa unidad y `status = ACTIVE`.

**Razón.** Es una redundancia deliberada entre dos tablas, y toda redundancia puede desincronizarse. Los dos modos de fallo son distintos y ambos graves:

- **`MOVING` sin movimiento `ACTIVE`** — la unidad está «moviéndose» hacia ningún sitio. La posición no se puede derivar (no hay polilínea), el cliente ve una unidad congelada en estado de marcha, y `unit.cancel_move` no tiene nada que cancelar. La unidad queda inutilizable.
- **Movimiento `ACTIVE` con unidad no `MOVING`** — peor: el loop avanza el movimiento y la posición cambia bajo una unidad que se presenta como `IDLE`. Es una unidad que se desplaza sola, y si además el jugador emite una orden nueva, se crea un segundo movimiento que viola [INV-MOVE-001](movement.md#inv-move-001).

**Cómo se garantiza.**

- `DOMAIN` — el inicio y la finalización de movimiento son **write-through inmediato y transaccional** (canon §12): la fila de `unit_movements` y el `status` de la unidad se escriben en la **misma** transacción. No existe ninguna ruta que actualice una sin la otra.
- `DB` — `unit_movements_one_active_per_unit`: índice **parcial** único sobre `unit_movements (unit_id) WHERE status = 'ACTIVE'`, que impone la unicidad del lado del movimiento ([INV-MOVE-001](movement.md#inv-move-001)) y hace imposible el caso «dos activos» que rompería el «exactamente una fila» del enunciado. Ése es su nombre real en la migración.
- `DOMAIN` — **la fuente de verdad es `unit_movements`**, no `units.status`. Esta jerarquía es la decisión clave del invariante: `unit_movements` es el registro transaccional del hecho, mientras que `units.status` se toca también en el flush periódico y por tanto tiene más superficie de error. Toda reparación deriva `status` desde `unit_movements` y nunca al revés.
- `DOMAIN` — la recuperación tras crash (`simulation.Hydrate`) carga los movimientos `ACTIVE` y, tras completar los ya vencidos (`arrival_time_ms <= now`), fija el `status` de cada unidad según el resultado. Esa pasada de arranque es también la reconciliación de este invariante. Una polilínea inválida cierra el movimiento como `FAILED` y deja la unidad donde estaba: nunca se teletransporta a nadie por un dato dudoso.

**Cómo se verifica.**

- `TestVerticalSliceMovimiento` (simulation, `internal/game/simulation`) — **existe y pasa**: la unidad pasa a `MOVING` al aceptarse la orden y vuelve a `IDLE` al completarse.
- `TestCancelacionExplicita` (simulation) — **existe y pasa**: `unit.cancel_move` deja el movimiento en `CANCELLED` y la unidad en `IDLE`.
- `TestNuevaOrdenReemplazaLaAnterior` (simulation) — **existe y pasa**: la orden nueva cancela la anterior y la unidad sigue `MOVING` con un único movimiento activo.
- `TestRecuperacionMovimientoEnCurso`, `TestRecuperacionMovimientoVencidoDuranteLaCaida` y `TestRecuperacionConPolilineaInvalida` (recovery, `internal/game/simulation`) — **existen y pasan**: el arranque deja `status` coherente con `unit_movements` en los tres escenarios.
- `TestIndiceUnicoImpideDosMovimientosActivos` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: el índice parcial rechaza el segundo `ACTIVE`.
- Test previsto: `Test_INV_UNIT_005_MovingIffActiveMovement` (integration) — barrido sobre todas las unidades: no existe ninguna con `status='MOVING'` sin activo, ni ninguna con activo y `status != 'MOVING'`.

**Violación en runtime.** Detección en la reconciliación de arranque y en una aserción del loop antes de avanzar movimiento. Log `invariant_violation` con `inv_id=INV-UNIT-005`, `unitId`, `status` observado y estado del movimiento. Política `REPAIR` derivando desde `unit_movements`: si hay activo, `status = MOVING`; si no lo hay, `status = IDLE` (salvo que el estado actual sea `DEAD` o `GARRISONED`, que son terminales o no espaciales y se respetan). La reparación se registra en `world_events`.

---

<a id="inv-unit-006"></a>
## INV-UNIT-006 — Una unidad pertenece a un único jugador

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, TYPE, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST (constraint) |
| Cobertura | **Cubierto** en el borde por `TestNoSePuedeComandarUnaUnidadAjena` (`internal/websocket`); la mitad `DB` sólo se ejercita en integración, aún no ejecutada |

**Enunciado.** Toda fila de `units` tiene `player_id` no nulo referenciando una fila existente de `players`, y esa columna es escalar: una unidad nunca tiene cero ni dos propietarios, ni siquiera durante una transferencia de ownership.

**Razón.** Es el fundamento de dato sobre el que se apoya [INV-PLAYER-001](player.md#inv-player-001). La comprobación de ownership compara el `playerId` de la sesión con `units.player_id`; si esa columna puede ser nula, la comparación tiene que decidir qué hacer con el nulo, y en Go una comparación con el valor cero de `uuid` puede resultar verdadera de forma accidental para una sesión cuyo `playerId` no se estableció. Un nulo en ownership es, en la práctica, una unidad que cualquiera puede reclamar.

Además, sin `player_id` la unidad no cuenta en la `population` de ninguna ciudad, lo que la hace invisible al límite de población de [INV-CITY-002](city.md#inv-city-002).

**Cómo se garantiza.**

- `DB` — `units.player_id uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE`, con la clave foránea declarada en línea. La cardinalidad «exactamente uno» es estructural: columna escalar no nula.
- `TYPE` — `unit.Unit` lleva `PlayerID uuid.UUID` como valor, no puntero; «unidad sin dueño» no es representable en memoria.
- `DOMAIN` — el único punto de comprobación es `unit.EnsureOwnedBy(playerID) error`, primera etapa del pipeline de comandos. No hay comparaciones de ownership escritas a mano en los handlers.
- `DOMAIN` — en MVP el ownership es **inmutable tras la creación** ([INV-UNIT-010](#inv-unit-010)): no existe ninguna ruta de transferencia, así que tampoco existe el estado intermedio que habría que proteger. Cuando la haya, será write-through inmediato y transaccional (canon §12).

> **No hay `units.version`.** A diferencia de `cities`, la tabla `units` **no** tiene columna de versión: la migración `000001` no declara concurrencia optimista para unidades. Cualquier redacción que cite `units.version` describe algo que no existe. Añadirla es un cambio de esquema y es **TBD (fuera de MVP)**; hoy la exclusión mutua la da el game loop, que es de un solo hilo sobre el estado del mundo.

**Cómo se verifica.**

- `TestNoSePuedeComandarUnaUnidadAjena` (e2e, `internal/websocket`) — **existe y pasa**: el ownership es escalar y comparable; una unidad ajena se rechaza con `UNIT_NOT_OWNED`.
- Test previsto: `Test_INV_UNIT_006_SingleOwner` (integration) — `INSERT` con `player_id NULL` falla por `NOT NULL`; `INSERT` con un `uuid` inexistente falla por clave foránea.

**Violación en runtime.** Imposible en escritura. En lectura, un `player_id` irresoluble produce log `invariant_violation` con `inv_id=INV-UNIT-006` y `unitId`. Política `FAIL_FAST` de la carga de esa unidad: no se incorpora a la simulación. Nunca se asigna un owner por defecto ni se «adopta» la unidad: cualquiera de esas reparaciones equivale a regalar un activo.

---

<a id="inv-unit-007"></a>
## INV-UNIT-007 — `GARRISONED` implica ciudad y fila de guarnición

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (derivar desde `garrisons`) |
| Cobertura | **Sin cobertura**: la lógica de guarnición está diferida y `garrisons` no la escribe ninguna ruta de código todavía |

**Enunciado.** `status = GARRISONED` implica `city_id IS NOT NULL` y la existencia de una fila de `garrisons` para esa unidad.

**Razón.** `GARRISONED` es el único estado en que la unidad **no tiene ubicación espacial**: no ocupa tile ([INV-UNIT-003](#inv-unit-003)) y su posición consolidada es un dato histórico que ninguna lógica lee. Si ese estado no viniera acompañado de una guarnición concreta, la unidad quedaría literalmente en ninguna parte: no está en el mundo, no está en una ciudad, y no hay ninguna operación que pueda devolverla al juego, porque salir de la guarnición requiere saber de qué guarnición se sale.

Es la misma clase de fallo que [INV-UNIT-005](#inv-unit-005) —un estado que afirma un hecho que otra tabla debe confirmar— y se resuelve igual: la tabla es la fuente y el `status` se deriva de ella.

**Cómo se garantiza.**

- `DB` — `garrisons.unit_id bigint PRIMARY KEY REFERENCES units (id) ON DELETE CASCADE` y `garrisons.city_id bigint NOT NULL REFERENCES cities (id) ON DELETE CASCADE`: una fila de guarnición siempre nombra una ciudad existente, y una unidad tiene como máximo una ([INV-GARR-001](diplomacy.md#inv-garr-001)).
- `DOMAIN` — la entrada en guarnición escribirá la fila de `garrisons`, fijará `units.city_id` y pondrá `status = GARRISONED` en la **misma transacción**; la salida deshará las tres cosas juntas.
- `DOMAIN` — la reconciliación de arranque deriva el `status` desde `garrisons`, nunca al revés: la tabla es el registro transaccional del hecho.
- `DB` — `units_city_idx` hace barata la consulta de reconciliación por ciudad.

**Cómo se verifica.**

- Test previsto: `Test_INV_UNIT_007_GarrisonedHasCityAndGarrisonRow` (integration) — barrido: ninguna unidad `GARRISONED` tiene `city_id IS NULL` ni carece de fila en `garrisons`.

**Violación en runtime.** Detección en la reconciliación de arranque. Log `invariant_violation` con `inv_id=INV-UNIT-007` y `unitId`. Política `REPAIR` derivando desde `garrisons`: si hay fila, se fijan `city_id` y `status`; si no la hay, la unidad vuelve a `IDLE` sobre su última posición consolidada, que sigue siendo un tile válido. Se registra un `world_events` de auditoría.

---

<a id="inv-unit-008"></a>
## INV-UNIT-008 — `HIDDEN` implica una Safe Zone activa y reposo

| Campo | Valor |
|---|---|
| Severidad | MEDIO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (volver a `IDLE`) |
| Cobertura | **Sin cobertura**: las Safe Zones existen como tabla pero su índice y sus efectos están diferidos |

**Enunciado.** `status = HIDDEN` implica que la unidad pertenece a una Safe Zone activa; el ocultamiento se evalúa sólo en reposo, y una unidad `HIDDEN` que recibe una orden de movimiento pasa a `MOVING` y deja de estar oculta.

**Razón.** El ocultamiento es una propiedad **derivada de la posición**, no un estado que la unidad conserve por su cuenta. Si `HIDDEN` pudiera persistir fuera de una zona, una unidad se volvería invisible en campo abierto, lo que rompe la única garantía que el interest management ofrece al resto de jugadores: lo que está en tu área de interés y no está oculto, lo ves.

La cualificación «sólo en reposo» es lo que hace el invariante barato de sostener. Evaluar el ocultamiento en cada tick de un movimiento obligaría a emitir `entity.despawn` y `entity.spawn` cada vez que la unidad cruzara el borde de la zona, lo que además filtraría la geometría exacta de la zona a cualquiera que observara la secuencia. Evaluándolo sólo al detenerse, el borde no se puede sondear.

**Cómo se garantiza.**

- `DOMAIN` — la transición a `HIDDEN` se evaluará al llegar la unidad a reposo (`IDLE`), consultando el índice de safe zones para su tile ([INV-SAFE-003](territory.md#inv-safe-003)).
- `DOMAIN` — aceptar una orden de movimiento pone `status = MOVING` en el mismo paso, de modo que `HIDDEN` y `MOVING` son mutuamente excluyentes por construcción: `units_status_valid` es un enum de un solo valor por fila.
- `DOMAIN` — una unidad `HIDDEN` **sí acepta órdenes** (a diferencia de `GARRISONED` y `DEAD`): el ocultamiento no inmoviliza, sólo oculta.
- `DOMAIN` — la exclusión de los deltas dirigidos a terceros la garantiza [INV-SAFE-004](territory.md#inv-safe-004), no esta ficha.

**Cómo se verifica.**

- Test previsto: `Test_INV_UNIT_008_HiddenImpliesActiveSafeZone` (unit) — ninguna unidad `HIDDEN` cae fuera de una safe zone, y una orden de movimiento sobre una unidad `HIDDEN` la deja `MOVING`.

**Violación en runtime.** Detección en la reconciliación periódica que compara `status` con el índice de safe zones. Log `invariant_violation` con `inv_id=INV-UNIT-008`, `unitId` y su tile. Política `REPAIR`: la unidad vuelve a `IDLE` y se emite `entity.spawn` a los suscriptores del chunk. Revelar una unidad que no debía estar oculta es preferible a ocultar una que no cumple la condición.

---

<a id="inv-unit-009"></a>
## INV-UNIT-009 — Todo `unit_type` persistido existe en el catálogo

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST en la carga de esa unidad |
| Cobertura | **Cubierto indirectamente** por `TestRechazosDeMovimiento` y `TestVerticalSliceMovimiento` (`internal/game/simulation`), que dependen de `unit.Lookup`; **no existe todavía** un test dedicado al barrido de `units` |

**Enunciado.** Para toda fila de `units`, `unit.Lookup(unit_type)` resuelve: el `unit_type` pertenece al catálogo cargado. No hay `CHECK` en la base porque el catálogo vive en código.

**Razón.** `unit.Lookup` devuelve las estadísticas que gobiernan el comportamiento de la unidad: `MaxHP`, `BaseMsPerTile` y `PopulationCost`. Un `unit_type` que no resuelve deja a la unidad **sin velocidad**, y sin velocidad no hay polilínea que construir: `handleMoveUnit` responde `INTERNAL_ERROR` y la unidad queda permanentemente inmóvil. Es la misma pérdida de activo silenciosa que describe [INV-UNIT-001](#inv-unit-001), por otra vía.

La ausencia de `CHECK` es deliberada y merece decirse en voz alta: `units.unit_type` es `text NOT NULL` **sin restricción de dominio**, porque el catálogo es un `map[Type]Definition` en `internal/domain/unit` y una migración no puede conocerlo. La consecuencia honesta es que la base de datos **acepta** un `unit_type` inventado; quien lo detecta es la carga. Cuando el catálogo se mueva a la base de datos —previsto para cuando haya decenas de tipos— este invariante ganará una garantía `DB` por clave foránea.

**Cómo se garantiza.**

- `DOMAIN` — `unit.Lookup(t Type) (Definition, error)` es el **único** acceso al catálogo y devuelve `ErrUnknownType` para un tipo ausente. No hay ningún `switch` sobre `unit_type` disperso por el código.
- `DOMAIN` — la creación de unidades sólo usa tipos del catálogo: el alta escribe `VILLAGER`, que es la única entrada del MVP.
- `DOMAIN` — la carga de arranque resuelve el tipo de cada unidad antes de incorporarla a la simulación; una unidad irresoluble no entra.
- `DOMAIN` — `handleMoveUnit` llama a `Lookup` antes del A\* y responde `INTERNAL_ERROR` si falla, en lugar de asumir una velocidad por defecto.

**Cómo se verifica.**

- `TestVerticalSliceMovimiento` y `TestRechazosDeMovimiento` (simulation, `internal/game/simulation`) — **existen y pasan**: todo el pipeline de movimiento depende de que `Lookup` resuelva.
- Test previsto: `Test_INV_UNIT_009_UnitTypeExistsInCatalog` (integration) — barrido sobre `units`: todo `unit_type` distinto resuelve en el catálogo.

**Violación en runtime.** Detección en `unit.Lookup` durante la carga de arranque y en `handleMoveUnit`. Log `invariant_violation` con `inv_id=INV-UNIT-009`, `unitId` y el `unit_type` crudo. Política `FAIL_FAST` de la carga de esa unidad: no se incorpora a la simulación. No se sustituye por `VILLAGER`: asignar estadísticas ajenas a una unidad es cambiar el juego sin decirlo.

---

<a id="inv-unit-010"></a>
## INV-UNIT-010 — Owner y tipo son inmutables tras la creación

| Campo | Valor |
|---|---|
| Severidad | MEDIO |
| Aplicación | DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | REJECT (la escritura no se aplica) |
| Cobertura | **Sin cobertura**: ninguna ruta de escritura toca `player_id` ni `unit_type` hoy; **no existe todavía** el test que lo fije |

**Enunciado.** `units.player_id` y `units.unit_type` son inmutables tras la creación en MVP: ninguna ruta de escritura los modifica.

**Razón.** Es la propiedad que hace baratos a [INV-UNIT-006](#inv-unit-006) y a [INV-UNIT-009](#inv-unit-009). Mientras el owner no cambie, «una unidad pertenece a un único jugador» no necesita proteger ningún estado intermedio, y la ausencia de `units.version` no es una carencia: no hay dos escritores compitiendo por un campo que nadie escribe. Mientras el tipo no cambie, las estadísticas resueltas al cargar la unidad valen para toda su vida y no hay que revalidarlas en cada uso.

Declararlo como invariante y no dejarlo implícito tiene un propósito concreto: la primera transferencia de ownership que alguien implemente —captura de unidades, herencia de un jugador eliminado, conversión— **rompe este invariante a propósito**, y debe hacerlo con un ADR, una columna de versión y la revisión de las dos fichas de arriba. Un invariante `MEDIO` que se deroga conscientemente es mejor que una suposición tácita que se rompe en silencio.

**Cómo se garantiza.**

- `DOMAIN` — las únicas escrituras sobre `units` en el MVP son: el alta (que fija ambos campos), el flush de posición, y las transiciones de `status`. Ninguna toca `player_id` ni `unit_type`.
- `DOMAIN` — `unit.Unit` no expone ningún setter de owner ni de tipo; los campos se rellenan en la construcción y en la hidratación desde la base de datos.
- `DOMAIN` — el `UPDATE` del flush enumera explícitamente sus columnas (posición, chunk, estado); no es un `UPDATE` de la fila entera que pudiera reescribir el owner con un valor obsoleto en memoria.

**Cómo se verifica.**

- Test previsto: `Test_INV_UNIT_010_OwnerAndTypeAreImmutable` (integration) — se toma la huella de `(id, player_id, unit_type)` de todas las unidades, se ejecuta un escenario funcional completo y la huella no cambia.

**Violación en runtime.** Detección en una aserción posterior al flush que compara la huella de owner y tipo. Log `invariant_violation` con `inv_id=INV-UNIT-010`, `unitId`, valores previo y nuevo. Política `REJECT`: la escritura no se aplica. Un cambio consumado de owner se trata como incidente, igual que en [INV-UNIT-006](#inv-unit-006): equivale a regalar un activo.

---

<a id="inv-unit-011"></a>
## INV-UNIT-011 — Las columnas de chunk siempre coinciden con la posición

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | REPAIR (recomputar desde `x`, `y`) |
| Cobertura | **Cubierto** en el volcado por `TestFlushPositionsEscribeElLoteYMantieneElChunk` (`internal/persistence/postgres`, integration **diseñado y no ejecutado**) y por `TestConversionTileChunk` (`internal/game/world`) |

**Enunciado.** `units.chunk_x` y `units.chunk_y` son siempre el chunk de `(x, y)`: `chunk_x = x / EO_CHUNK_SIZE` y `chunk_y = y / EO_CHUNK_SIZE`, en toda escritura de posición.

**Razón.** `chunk_x` y `chunk_y` son una **desnormalización deliberada**: existen para que la consulta caliente del snapshot —«todas las unidades vivas de estos chunks»— sea un recorrido de índice (`units_chunk_idx`) en lugar de un cálculo aritmético sobre cada fila. Esa es una decisión correcta y es la clave del interest management del MVP.

Como toda desnormalización, puede divergir, y aquí la divergencia es **silenciosa y unidireccional**: la unidad desaparece. Si `chunk_x` apunta a un chunk equivocado, la unidad no aparece en los snapshots de quien mira donde realmente está, y sí aparece —como un fantasma— para quien mira el chunk que la columna dice. No hay error, no hay log: sólo una entidad que existe en el servidor y nadie ve donde debería. Es exactamente el modo de fallo que [INV-WORLD-006](world.md#inv-world-006) describe para una partición mal formada, materializado en una columna.

**Cómo se garantiza.**

- `DOMAIN` — el chunk se recomputa con `World.ChunkOf(x, y)` en la **misma sentencia** que escribe la posición. No hay ninguna ruta que actualice `x`/`y` sin actualizar `chunk_x`/`chunk_y`: el flush por lotes escribe las cuatro columnas juntas.
- `DOMAIN` — `World.ChunkOf` es la única implementación de la conversión ([INV-WORLD-006](world.md#inv-world-006)); ningún repositorio calcula `x / 32` a mano.
- `DOMAIN` — durante un movimiento activo la posición **no se persiste** ([INV-PERSIST-004](persistence.md#inv-persist-004)), así que no hay escrituras intermedias que pudieran quedar a medias: la posición y su chunk se escriben una sola vez, al terminar.
- `DB` — `units_chunk_idx` es un índice parcial sobre `(chunk_x, chunk_y) WHERE status <> 'DEAD'`, coherente con que las unidades muertas no se emiten a nadie.

**Cómo se verifica.**

- `TestFlushPositionsEscribeElLoteYMantieneElChunk` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: el volcado por lotes escribe posición y chunk de forma coherente.
- `TestConversionTileChunk` (unit, `internal/game/world`) — **existe y pasa**: la conversión de referencia es correcta.
- Test previsto: `Test_INV_UNIT_011_ChunkColumnsMatchPosition` (integration) — barrido sobre `units`: para toda fila, `chunk_x = x / chunk_size` y `chunk_y = y / chunk_size`.

**Violación en runtime.** Detección en la reconciliación de arranque, que recalcula el chunk de cada unidad cargada. Log `invariant_violation` con `inv_id=INV-UNIT-011`, `unitId`, chunk persistido y chunk calculado. Política `REPAIR`: prevalecen `x` e `y`, que son la fuente, y se reescriben las columnas de chunk. La reparación es segura porque el valor correcto es una función pura de un dato autoritativo.

---

## Relación con los invariantes de movimiento

```
INV-UNIT-005 (status ⟺ movimiento ACTIVE)
    │
    ├── depende de ── INV-MOVE-001 (a lo sumo un ACTIVE)
    │
INV-UNIT-001 (posición transitable)
    │
    ├── depende de ── INV-MOVE-004 (waypoints transitables y contiguos)
    ├── depende de ── INV-MOVE-008 (cancelación hace snap a tile)
    └── depende de ── INV-WORLD-003 (path nunca cruza bloqueado)
```

Los invariantes de unidad son consecuencia de los de movimiento durante el tránsito: mientras un movimiento válido esté activo, la posición de la unidad es válida **por construcción**. Por eso el esfuerzo de verificación se concentra en [movement.md](movement.md).

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-UNIT-001 | `internal/domain/unit`, `internal/game/world` | [INV-WORLD-001](world.md#inv-world-001), [INV-MOVE-004](movement.md#inv-move-004) |
| INV-UNIT-002 | `internal/domain/unit` | [INV-MOVE-007](movement.md#inv-move-007) |
| INV-UNIT-003 | `internal/domain/unit`, `internal/domain/diplomacy` | [INV-CITY-006](city.md#inv-city-006) |
| INV-UNIT-004 | `internal/domain/unit` | [INV-PERSIST-004](persistence.md#inv-persist-004) |
| INV-UNIT-005 | `internal/domain/unit`, `internal/domain/movement` | [INV-MOVE-001](movement.md#inv-move-001), [INV-PERSIST-002](persistence.md#inv-persist-002) |
| INV-UNIT-006 | `internal/domain/unit` | [INV-PLAYER-001](player.md#inv-player-001) |
| INV-UNIT-007 | `internal/domain/unit` | [INV-GARR-002](diplomacy.md#inv-garr-002), [INV-UNIT-003](#inv-unit-003) |
| INV-UNIT-008 | `internal/domain/unit` | [INV-SAFE-003](territory.md#inv-safe-003) |
| INV-UNIT-009 | `internal/domain/unit` | [../specs/unit.md](../specs/unit.md) |
| INV-UNIT-010 | `internal/domain/unit` | [INV-UNIT-006](#inv-unit-006) |
| INV-UNIT-011 | `internal/persistence/postgres`, `internal/game/world` | [INV-WORLD-006](world.md#inv-world-006) |
