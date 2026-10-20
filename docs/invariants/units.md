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
| `GARRISONED` | **No** | No | No (todavía sí en el snapshot: [INV-UNIT-003](#inv-unit-003)) |
| `HIDDEN` | Sí | Sí | Sólo para su dueño ([INV-SAFE-004](territory.md#inv-safe-004)) |
| `DEAD` | No | **No** | No (no se carga) |

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
| Cobertura | **Cubierto**: `TestRechazosDeMovimiento`, `TestVerticalSliceMovimiento`, `TestFundarApartaLaUnidadQueQuedaDentroDeLaMuralla`, `TestUnaUnidadQueSeDetieneDentroDeLaMurallaSeAparta` y `TestAlArrancarLaMurallaApartaYDetieneComoEnElAlta` (`internal/game/simulation`), más `TestFueraDeLimitesEsIntransitable` (`internal/game/world`); la validación de carga de las posiciones, `TestLasUnidadesSeExcluyenSeReparanOAbortan` (`internal/game/loadcheck`) y `TestUnFatalAbortaLaCargaSinEscribirNada` (integration) |

**Enunciado.** Para toda unidad cuyo estado implica presencia en el mundo (`IDLE`, `MOVING`, `HIDDEN`), su posición `(x, y)` cumple [INV-WORLD-001](world.md#inv-world-001) y el tile correspondiente es transitable: terreno con `walkable = sí` y sin marca en la capa de ocupación.

**Razón.** Una unidad sobre terreno intransitable es un estado del que el sistema no puede salir: el pathfinder no expande tiles bloqueados, luego no puede calcular ningún camino **desde** ese tile. La unidad queda permanentemente inmóvil, con todas sus órdenes rechazadas por `PATH_NOT_FOUND`, y el jugador sin recurso alguno. Es una pérdida de activo silenciosa e irreversible.

Fuera de límites es peor: indexa fuera del array de terreno y calcula un `chunkId` arbitrario, lo que emite la unidad a suscriptores incorrectos.

La excepción de `GARRISONED` y `DEAD` es deliberada y se detalla en [INV-UNIT-003](#inv-unit-003): esas unidades no ocupan tile, y exigirles una posición transitable impondría restricciones sobre un dato que ya no tiene significado espacial.

**Cómo se garantiza.**

- `DOMAIN` — la posición inicial de los tres aldeanos sale de `founding.FindSite`, que sólo acepta un emplazamiento si encuentra tiles transitables a distancia 2 del centro, fuera de la muralla; si no los hay, sigue buscando ([INV-PLAYER-003](player.md#inv-player-003)).
- `DOMAIN` — durante el movimiento la posición no se escribe libremente: se **deriva** de la polilínea (canon §7), cuyos waypoints son todos transitables por [INV-MOVE-004](movement.md#inv-move-004) y [INV-WORLD-003](world.md#inv-world-003). Mientras el movimiento sea válido, la posición derivada lo es por construcción.
- `DOMAIN` — al completar o cancelar un movimiento se hace *snap* a un waypoint de la polilínea, nunca a una posición interpolada ni a un tile calculado aparte ([INV-MOVE-008](movement.md#inv-move-008)).
- `DOMAIN` — **la única forma de que un tile se vuelva intransitable con el servidor en marcha es fundar una ciudad**, y la fundación despeja su huella ([RN-CITY-022](../specs/city.md)): en el mismo tick, los movimientos cuya ruta pendiente atraviesa la muralla se detienen con `PATH_BLOCKED` y las unidades que quedan dentro se apartan al tile libre más cercano. El arranque aplica la misma regla tras reconstruir las murallas (`simulation.ClearFootprints`), porque una caída antes del volcado deja en disco la posición vieja. Hasta `EO-118` no existía: la búsqueda de emplazamiento no mira las unidades, y una ciudad nueva podía emparedar un aldeano ajeno sin remedio.
- `DOMAIN` — la validación de carga (`internal/game/loadcheck`, `EO-119`) comprueba que cada unidad leída de `units` que no está `GARRISONED` cae dentro del mundo y sobre terreno transitable. Mira el terreno y no la capa de ocupación: al cargar, las murallas aún no están puestas, y lo que quede dentro de una lo aparta después `ClearFootprints`. Ninguna ruta del código puede escribir una posición así; sólo una escritura manual, una migración o un cambio del generador.

> **No hay garantía `DB`.** La tabla `units` de `000001_initial_schema.up.sql` declara `x integer NOT NULL` e `y integer NOT NULL` **sin ningún `CHECK`**: ni de límite inferior ni superior. El límite superior es `EO_WORLD_WIDTH`/`EO_WORLD_HEIGHT`, que es configuración y no puede empotrarse como literal en una migración; la transitabilidad tampoco es expresable como `CHECK`, porque el terreno vive en `world_chunks` como `bytea`. Un `INSERT` directo con `x = 99999` **es aceptado por PostgreSQL**. Todo el invariante se garantiza en `DOMAIN` y `TEST`, y la ficha lo declara así en lugar de prometer una constraint inexistente.

**Cómo se verifica.**

- `TestRechazosDeMovimiento` (simulation, `internal/game/simulation`) — los rechazos del pipeline, incluidos `TARGET_OUT_OF_BOUNDS` y `TARGET_NOT_WALKABLE`; un comando rechazado no produce ningún movimiento, así que la unidad no puede acabar fuera del mundo ni sobre un tile bloqueado.
- `TestVerticalSliceMovimiento` (simulation) — la posición derivada en cada tick de un movimiento completo cae siempre sobre waypoints de una polilínea ya validada.
- `TestFundarApartaLaUnidadQueQuedaDentroDeLaMuralla` (simulation) — una unidad dentro del 3×3 de una ciudad recién fundada acaba en el tile libre más cercano, transitable, y puede volver a moverse.
- `TestUnaUnidadQueSeDetieneDentroDeLaMurallaSeAparta` y `TestUnaRutaQueCruzaLaMurallaNuevaSeDetieneConPathBlocked` (simulation) — la unidad en marcha se detiene y, si se detiene dentro, se aparta.
- `TestAlArrancarLaMurallaApartaYDetieneComoEnElAlta` (recovery) — la misma regla al arrancar, con el cierre del movimiento devuelto para la base de datos.
- `TestFueraDeLimitesEsIntransitable` (unit, `internal/game/world`) — los bordes del mundo se comportan como un muro.
- Las cinco piezas de la regla de la fundación se verificaron por mutación: quitar cualquiera hace fallar su test.
- `TestLasUnidadesSeExcluyenSeReparanOAbortan` (unit, `internal/game/loadcheck`) — una unidad persistida sobre `WATER` produce el hallazgo `FAIL_FAST`; `TestLaGuarnicionSeDerivaDeSuTabla`, que una guarnecida en la misma posición no lo produce.
- `TestUnFatalAbortaLaCargaSinEscribirNada` (integration) — esa unidad aborta la carga y nada se escribe, tampoco lo reparable.

**Violación en runtime.** Si al despejar una huella no hay ningún tile transitable en ocho anillos alrededor, la unidad se queda donde está y se emite `invariant_violation` con `inv_id=INV-UNIT-001`, `severity=CRITICO` y la unidad; con el entorno despejado de radio 3 que exige la fundación no puede ocurrir. Apartar una unidad **no** es una reparación de este invariante: es la regla de juego de [RN-CITY-022](../specs/city.md), con su tile elegido de forma determinista. Lo que no se hace es mover la unidad de un jugador por un dato corrupto: al cargar, una unidad fuera del mundo o sobre terreno imposible emite `invariant_violation` con `inv_id=INV-UNIT-001` y política `FAIL_FAST`, y el arranque aborta sin escribir nada en lugar de recolocarla.

---

<a id="inv-unit-002"></a>
## INV-UNIT-002 — Una unidad `DEAD` no acepta órdenes

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M4 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por los casos «unidad muerta» y «unidad ajena y muerta» de `TestRechazosDeMovimiento` (`internal/game/simulation`), `TestEnsureCanMoveDecidePorEstado` (`internal/domain/unit`) y `TestSoloSeEntraDesdeIdle` (`internal/domain/garrison`) |

**Enunciado.** Ningún comando dirigido a una unidad con `status = DEAD` produce efecto alguno, y una unidad `DEAD` nunca vuelve a otro estado. Mientras está en memoria, `unit.move` la rechaza con `UNIT_DEAD`; tras un reinicio no se carga, y el mismo comando responde `UNIT_NOT_FOUND`.

**Razón.** `DEAD` es un estado **terminal**. Si acepta órdenes, se abre la reanimación implícita: una unidad muerta que recibe `unit.move` y pasa a `MOVING` ha resucitado sin ninguna mecánica que lo justifique, con todo el valor material que eso implica. La terminalidad también sostiene la contabilidad: la métrica `eo_active_units` y la `population` de la ciudad se apoyan en que una unidad muerta ya no vuelve a contar.

**Cómo se garantiza.**

- `DOMAIN` — la validación de estado de unidad es el paso 2 del pipeline de `unit.move` (canon §7: ownership → estado → destino → A\* → crear → simular → notificar). Se ejecuta **después** de ownership, para no revelar el estado de unidades ajenas ([INV-PLAYER-001](player.md#inv-player-001)), y **antes** de cualquier trabajo caro como el pathfinding.
- `DOMAIN` — el rechazo lo decide un punto único, `unit.EnsureCanMove() error`, que devuelve `unit.ErrDead` para `DEAD` y `unit.ErrGarrisoned` para `GARRISONED`. No es un `if status == DEAD` disperso por los handlers: la capa de aplicación traduce el error de dominio al código estable con `moveErrorCode`.
- `DB` — `CONSTRAINT units_status_valid CHECK (status IN ('IDLE','MOVING','GARRISONED','HIDDEN','DEAD'))` cierra el dominio del campo, de modo que `DEAD` no puede confundirse con una variante de capitalización que se colara por otra rama.
- `DOMAIN` — que `DEAD` no tenga salida es estructural: `UnitRepo.ListAlive` carga sólo `status <> 'DEAD'`, así que una unidad muerta no vuelve a la memoria tras un reinicio, y las cuatro sentencias que escriben `units` sólo lo hacen sobre unidades que están en memoria. La entrada en guarnición, el otro comando que mira el estado, rechaza `DEAD` en `garrison.CanEnter`, y además exige `IsAlive()`.
- `unit.cancel_move` no mira el estado: sobre una unidad muerta sin movimiento no hace nada, que es justo «ningún efecto».
- Cuando exista el combate, marcar una unidad como `DEAD` deberá cancelar su movimiento activo en la misma transacción ([INV-UNIT-005](#inv-unit-005)). Hoy no hay ninguna ruta que lo haga.
- El código de error `UNIT_DEAD` es estable (canon §16) y distinto de `UNIT_NOT_MOVABLE`, que cubre los casos de estado no móvil sin muerte.

**Cómo se verifica.**

- `TestRechazosDeMovimiento` / casos «unidad muerta» y «unidad ajena y muerta» (simulation) — `UNIT_DEAD` para la propia y `UNIT_NOT_OWNED` para la ajena: la propiedad se comprueba antes que el estado.
- `TestEnsureCanMoveDecidePorEstado` (unit, `internal/domain/unit`) — tabla de los cinco estados del esquema y uno inventado.
- `TestSoloSeEntraDesdeIdle` (unit, `internal/domain/garrison`) — una unidad muerta no se guarnece.
- Test previsto, para cuando exista la muerte: una unidad `MOVING` que muere deja su movimiento en `CANCELLED`, no en `ACTIVE`.

**Violación en runtime.** El rechazo es la regla de negocio, no una violación: `UNIT_DEAD`, sin log de invariante. Una unidad que hubiera salido de `DEAD` no se detectaría hoy, porque no hay ninguna ruta que pueda producirla.

> El combate está **fuera de MVP**. En MVP ninguna mecánica produce `DEAD`; el rechazo se implementa y se testea para que la transición nazca correcta.

---

<a id="inv-unit-003"></a>
## INV-UNIT-003 — Una unidad `GARRISONED` no ocupa tile del mundo

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR |
| Cobertura | **Parcial**: `TestRechazosDeMovimiento` / caso «unidad guarnecida», `TestGuarnecidasYSinVidaNoSeOcultan` y `TestLaMurallaNuevaNoTocaUnidadesGuarnecidas` (`internal/game/simulation`). Falta lo principal: una unidad `GARRISONED` sigue en el índice por chunk y sale en los snapshots (M7, entregable 6) |

**Enunciado.** Una unidad con `status = GARRISONED` no ocupa ningún tile: no se emite en los deltas ni en los snapshots por chunk y no es origen ni destino de pathfinding; su ubicación es la guarnición registrada en `garrisons`, no un tile.

**Razón.** Es un problema de doble contabilidad. Si la unidad guarnecida sigue en el índice de ocupación, ocupa un tile que nadie ve, bloqueando paths de forma inexplicable para los jugadores: una casilla aparentemente vacía por la que no se puede pasar. Y si sigue emitiéndose en deltas de chunk, aparece en pantalla fuera de la guarnición, revelando además la presencia de tropas dentro de una ciudad ajena, que es información con valor táctico directo.

**Cómo se garantiza.**

- Las unidades **no** están en la capa de ocupación del MVP: esa capa sólo la marcan las murallas de las ciudades, y las unidades no se bloquean entre sí. «No figura en la ocupación» se cumple para todas, guarnecidas o no, y no es lo que este invariante protege. Lo que importa es el índice por chunk (`State.unitsByChunk`), que alimenta los snapshots y el reparto de deltas.
- `DOMAIN` — `unit.EnsureCanMove` rechaza `GARRISONED` con `UNIT_GARRISONED`: la unidad no es origen de ningún A\*.
- `DOMAIN` — la fase 5 no oculta una unidad guarnecida (`safezone.NextStatus`), y la regla de la fundación ([RN-CITY-022](../specs/city.md)) no la aparta: ninguna de las dos la trata como si estuviera en el mapa.
- `DOMAIN` — `GarrisonRepo.Enter` cancela el movimiento activo, escribe `GARRISONED` y la fila de `garrisons` en una transacción; `Leave` devuelve la unidad a un tile transitable del anillo exterior (`garrison.ReentryTile`) y borra la fila. Las dos existen y tienen tests de integración, pero **ningún comando de red las invoca todavía**: `unit.garrison` y `unit.ungarrison` son el entregable 6 de M7.
- **Lo que falta.** `State.AddUnit` indexa por chunk a toda unidad cargada, guarnecida o no, y `simulation.VisibleTo` sólo filtra `HIDDEN`. Una unidad `GARRISONED` leída de disco sale por tanto en el snapshot de quien mire su último tile. Hoy ninguna ruta de producción escribe `GARRISONED`, así que no ocurre; deja de ser latente en cuanto existan los comandos, y se corrige con ellos.
- `DOMAIN` — la columna de posición de una unidad `GARRISONED` conserva su último valor como dato histórico. Por eso [INV-UNIT-001](#inv-unit-001) excluye este estado.

**Cómo se verifica.**

- `TestRechazosDeMovimiento` / caso «unidad guarnecida» (simulation) — `unit.move` responde `UNIT_GARRISONED`.
- `TestGuarnecidasYSinVidaNoSeOcultan` y `TestLaMurallaNuevaNoTocaUnidadesGuarnecidas` (simulation) — ni la fase 5 ni la fundación la tratan como presente en el mapa.
- `TestSalirDeLaGuarnicionDevuelveLaUnidadAlMapaYBorraLaFila` (integration, `internal/persistence/postgres`) — la salida la deja `IDLE` en el tile elegido.
- Test previsto (M7, entregable 6): una unidad guarnecida no aparece en el snapshot ni en los deltas de un tercero, y su entrada emite `entity.despawn` al chunk.

**Violación en runtime.** No hay detección: el caso que la ficha describe no es alcanzable hasta que existan los comandos de guarnición.

> Las propiedades específicas de la guarnición viven en [diplomacy.md](diplomacy.md), familia `INV-GARR-*`.

---

<a id="inv-unit-004"></a>
## INV-UNIT-004 — `hp` está en `[0, max_hp]`

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | REJECT + clamp en el cálculo; REPAIR al cargar |
| Cobertura | **Cubierto**: `TestLasRestriccionesDeUnidadesRechazanFilasInvalidas` (integration, `internal/persistence/postgres`) cubre las tres restricciones, `TestIsAliveExigeEstadoYVida` (`internal/domain/unit`) la lectura de la vida, y `TestLasUnidadesSeExcluyenSeReparanOAbortan` (`internal/game/loadcheck`) con `TestLaCargaReparaYAuditaLoQueElEsquemaNoGarantiza` (integration) que al cargar `max_hp` es el del catálogo. El punto único de modificación no existe porque nada modifica `hp` |

**Enunciado.** Para toda unidad, `0 <= hp <= max_hp`, donde `max_hp` es el valor del `unit_type` correspondiente (`VILLAGER` = 40).

**Razón.** `hp` negativo rompe cualquier comparación de vida escrita como `hp > 0` en aritmética con signo y, si se persiste, deja una unidad viva con vida imposible. `hp` por encima de `max_hp` es una unidad invencible obtenida por un bug de curación, con impacto directo en el equilibrio en cuanto exista combate.

La disciplina que este invariante impone es concreta: **el clamp se aplica en el cálculo, no en la escritura**. Un daño de 60 sobre una unidad de 40 debe producir `hp = 0` y muerte, no `hp = -20` seguido de una corrección posterior que puede olvidarse.

**Cómo se garantiza.**

- `DB` — la migración `000001` declara **tres** constraints complementarias sobre `units`: `hp integer NOT NULL CHECK (hp >= 0)`, `max_hp integer NOT NULL CHECK (max_hp > 0)` y `CONSTRAINT units_hp_within_max CHECK (hp <= max_hp)`. Juntas cubren el enunciado; ésos son sus nombres reales.
- `DOMAIN` — cuando exista combate, toda modificación pasará por un punto único que haga el clamp a `[0, max_hp]` como parte del cálculo y devuelva el valor final. Ningún handler asignará `hp` directamente. **Hoy ese punto único no existe porque ninguna ruta modifica `hp`.**
- `DOMAIN` — `max_hp` proviene del catálogo de `internal/domain/unit` (`unit.Lookup`), no de literales dispersos (canon §17): el alta escribe `hp = max_hp = def.MaxHP`. El `40` de `VILLAGER` aparece en un único lugar. Al cargar, la validación de carga (`internal/game/loadcheck`, `EO-119`) compara `max_hp` con el del catálogo y recorta `hp` a `[0, max_hp]`.
- `DOMAIN` — `Unit.IsAlive()` es `Status != DEAD && HP > 0`: la conjunción impide tratar como viva a una unidad con cero vida aunque su `status` se quedara atrás. `garrison.CanEnter` la usa.
- `DOMAIN` — **ninguna escritura posterior al alta toca `hp` ni `max_hp`**: el volcado por lotes escribe posición, chunk y estado, y nada más. No existe un volcado de `hp`.

**Cómo se verifica.**

- `TestLasRestriccionesDeUnidadesRechazanFilasInvalidas` (integration, `internal/persistence/postgres`) — `hp = -1` lo rechaza `units_hp_check`, `hp = max_hp + 1` `units_hp_within_max` y `max_hp = 0` `units_max_hp_check`; los extremos `0` y `max_hp` se aceptan.
- `TestIsAliveExigeEstadoYVida` (unit, `internal/domain/unit`) — sin vida no está viva aunque el estado diga `IDLE`, y `DEAD` no lo está aunque conserve vida.
- `TestLasUnidadesSeExcluyenSeReparanOAbortan` (unit, `internal/game/loadcheck`) y `TestLaCargaReparaYAuditaLoQueElEsquemaNoGarantiza` (integration) — un aldeano con `hp 55/60` queda en `40/40`, en memoria y en la base.
- Test previsto, para cuando exista el combate: tabla de daño y curación sobre el punto único (daño mayor que `hp` deja `hp = 0` y `DEAD`; curación mayor que el déficit deja `max_hp`).

**Violación en runtime.** Un valor fuera de rango no llega a disco: el `CHECK` rechaza la transacción y el worker lo registra como escritura fallida. Lo que el `CHECK` sí admite es un `max_hp` distinto del catálogo: la validación de carga lo detecta con `invariant_violation` (`inv_id=INV-UNIT-004`, `entity=unit:<id>`) y política `REPAIR`: escribe el `max_hp` del catálogo y el `hp` recortado, con un `InvariantRepaired` de auditoría.

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
| Cobertura | **Cubierto** por `TestVerticalSliceMovimiento`, `TestCancelacionExplicita`, `TestNuevaOrdenReemplazaLaAnterior`, `TestRecuperacionReconciliaUnaUnidadMovingSinMovimiento`, `TestRecuperacionNoReanudaElMovimientoDeUnaUnidadGuarnecida` y los tres tests de recuperación de movimiento (`internal/game/simulation`); en disco, `TestIndiceUnicoImpideDosMovimientosActivos` y `TestConLaColaRealElMovimientoLlevaSuIdYCierraConSuPosicion` (integration) |

**Enunciado.** Para toda unidad se cumple la bicondicional: `units.status = MOVING` ⟺ existe exactamente una fila en `unit_movements` con esa unidad y `status = ACTIVE`.

**Razón.** Es una redundancia deliberada entre dos tablas, y toda redundancia puede desincronizarse. Los dos modos de fallo son distintos y ambos graves:

- **`MOVING` sin movimiento `ACTIVE`** — la unidad está «moviéndose» hacia ningún sitio. La posición no se puede derivar (no hay polilínea), el cliente ve una unidad congelada en estado de marcha, y `unit.cancel_move` no tiene nada que cancelar. La unidad queda inutilizable.
- **Movimiento `ACTIVE` con unidad no `MOVING`** — peor: el loop avanza el movimiento y la posición cambia bajo una unidad que se presenta como `IDLE`. Es una unidad que se desplaza sola, y si además el jugador emite una orden nueva, se crea un segundo movimiento que viola [INV-MOVE-001](movement.md#inv-move-001).

**Cómo se garantiza.**

- `DOMAIN` — en memoria, el loop cambia el `status` y el movimiento en el mismo paso: `handleMoveUnit` registra el movimiento y pone `MOVING`; la llegada, la cancelación y el reemplazo lo quitan y ponen `IDLE`. El loop es de un solo hilo, así que no hay instante intermedio observable.
- `DOMAIN` — en disco, el inicio y el cierre de un movimiento son **write-through transaccional** ([ADR-013](../decisions/ADR-013-persistence-pipeline.md) §2.3): `GameStore.PersistMovementStart` cancela el activo anterior, inserta el nuevo y escribe la unidad `MOVING` en su origen en una transacción; `PersistMovementFinish` cierra la fila y deja la unidad `IDLE` en su tile final en otra. El volcado periódico no escribe unidades en marcha ([INV-PERSIST-004](persistence.md#inv-persist-004)), así que no puede dejar en disco un `MOVING` que ningún movimiento respalde.
- `DB` — `unit_movements_one_active_per_unit`: índice **parcial** único sobre `unit_movements (unit_id) WHERE status = 'ACTIVE'`, que impone la unicidad del lado del movimiento ([INV-MOVE-001](movement.md#inv-move-001)) y hace imposible el caso «dos activos» que rompería el «exactamente una fila» del enunciado. Ése es su nombre real en la migración.
- `DOMAIN` — **la fuente de verdad es `unit_movements`**, no `units.status`: es el registro transaccional del hecho. La reparación deriva `status` desde `unit_movements` y nunca al revés.
- `DOMAIN` — `simulation.Hydrate` es la reconciliación de arranque, en las dos direcciones. Un movimiento `ACTIVE` vencido durante la caída deja la unidad `IDLE` en su destino; uno en curso la deja `MOVING`; uno con polilínea inválida se cierra `FAILED` y la unidad queda `IDLE` donde estaba, sin teletransportarla. Una unidad que llega `MOVING` sin movimiento que la respalde queda `IDLE` en su última posición consolidada. Una unidad `GARRISONED` con un movimiento `ACTIVE` —imposible por `GarrisonRepo.Enter`, que lo cancela en su transacción— conserva la guarnición y el movimiento se cierra `FAILED`; hasta `EO-118` se reanudaba, sacando la unidad de la ciudad sin pasar por la salida.

**Cómo se verifica.**

- `TestVerticalSliceMovimiento` (simulation, `internal/game/simulation`) — la unidad pasa a `MOVING` al aceptarse la orden y vuelve a `IDLE` al completarse.
- `TestCancelacionExplicita` (simulation) — `unit.cancel_move` deja el movimiento en `CANCELLED` y la unidad en `IDLE`.
- `TestNuevaOrdenReemplazaLaAnterior` (simulation) — la orden nueva cancela la anterior y la unidad sigue `MOVING` con un único movimiento activo.
- `TestRecuperacionMovimientoEnCurso`, `TestRecuperacionMovimientoVencidoDuranteLaCaida` y `TestRecuperacionConPolilineaInvalida` (recovery) — el arranque deja `status` coherente con `unit_movements` en los tres escenarios.
- `TestRecuperacionReconciliaUnaUnidadMovingSinMovimiento` (recovery) — una unidad `MOVING` sin movimiento queda `IDLE` donde estaba y se vuelca; la que sí lo tiene se reanuda.
- `TestRecuperacionNoReanudaElMovimientoDeUnaUnidadGuarnecida` (recovery) — el movimiento se cierra `FAILED` sin escribir la unidad, que sigue `GARRISONED`. Verificado por mutación.
- `TestIndiceUnicoImpideDosMovimientosActivos` (integration, `internal/persistence/postgres`) — el índice parcial rechaza el segundo `ACTIVE`.
- `TestConLaColaRealElMovimientoLlevaSuIdYCierraConSuPosicion` (integration) — con la cola real, el cierre deja en disco el movimiento `COMPLETED` y la unidad `IDLE` en su tile final, sin esperar a ningún volcado.

**Violación en runtime.** La detección está en la rehidratación, no en el loop: cada reparación de `Hydrate` emite `invariant_violation` con `inv_id=INV-UNIT-005`, `severity=CRITICO`, `policy=REPAIR` y la unidad. Política `REPAIR` derivando desde `unit_movements`, salvo para `DEAD` y `GARRISONED`, que se respetan: `DEAD` no se carga, y en `GARRISONED` manda la guarnición. La reparación no se registra en `world_events`: queda en el log y llega a disco con el volcado o con el cierre del movimiento.

---

<a id="inv-unit-006"></a>
## INV-UNIT-006 — Una unidad pertenece a un único jugador

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, TYPE, TEST |
| Milestone | M2 |
| Política ante violación | FAIL_FAST (constraint) |
| Cobertura | **Cubierto** por `TestLasRestriccionesDeUnidadesRechazanFilasInvalidas` (integration, `internal/persistence/postgres`), `TestEnsureOwnedByComparaElDueno` (`internal/domain/unit`) y `TestNoSePuedeComandarUnaUnidadAjena` (`internal/websocket`) |

**Enunciado.** Toda fila de `units` tiene `player_id` no nulo referenciando una fila existente de `players`, y esa columna es escalar: una unidad nunca tiene cero ni dos propietarios, ni siquiera durante una transferencia de ownership.

**Razón.** Es el fundamento de dato sobre el que se apoya [INV-PLAYER-001](player.md#inv-player-001). La comprobación de ownership compara el `playerId` de la sesión con `units.player_id`; si esa columna puede ser nula, la comparación tiene que decidir qué hacer con el nulo, y en Go una comparación con el valor cero de `uuid` puede resultar verdadera de forma accidental para una sesión cuyo `playerId` no se estableció. Un nulo en ownership es, en la práctica, una unidad que cualquiera puede reclamar.

Además, sin `player_id` la unidad no cuenta en la `population` de ninguna ciudad, lo que la hace invisible al límite de población de [INV-CITY-002](city.md#inv-city-002).

**Cómo se garantiza.**

- `DB` — `units.player_id uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE`, con la clave foránea declarada en línea. La cardinalidad «exactamente uno» es estructural: columna escalar no nula.
- `TYPE` — `unit.Unit` lleva `PlayerID uuid.UUID` como valor, no puntero; «unidad sin dueño» no es representable en memoria.
- `DOMAIN` — en el pipeline de movimiento, la comprobación es `unit.EnsureOwnedBy(playerID) error`, primera etapa tras encontrar la unidad. La guarnición, que no pasa por ese pipeline, compara `u.PlayerID` con quien pide dentro de `garrison.CanEnter` y `CanLeave`: son dos puntos, no uno, y los dos comparan el mismo campo por valor.
- `DOMAIN` — en MVP el ownership es **inmutable tras la creación** ([INV-UNIT-010](#inv-unit-010)): no existe ninguna ruta de transferencia, así que tampoco existe el estado intermedio que habría que proteger. Cuando la haya, será write-through inmediato y transaccional (canon §12).

> **No hay `units.version`.** A diferencia de `cities`, la tabla `units` **no** tiene columna de versión: la migración `000001` no declara concurrencia optimista para unidades. Cualquier redacción que cite `units.version` describe algo que no existe. Añadirla es un cambio de esquema y es **TBD (fuera de MVP)**; hoy la exclusión mutua la da el game loop, que es de un solo hilo sobre el estado del mundo.

**Cómo se verifica.**

- `TestLasRestriccionesDeUnidadesRechazanFilasInvalidas` (integration, `internal/persistence/postgres`) — `player_id NULL` falla con `23502` y un `uuid` inexistente con `units_player_id_fkey`.
- `TestEnsureOwnedByComparaElDueno` (unit, `internal/domain/unit`) — otro jugador y el `uuid` nulo no pasan por el dueño.
- `TestNoSePuedeComandarUnaUnidadAjena` (e2e, `internal/websocket`) — una unidad ajena se rechaza con `UNIT_NOT_OWNED` a través del socket real.

**Violación en runtime.** Imposible en escritura y, por la clave foránea, también en lectura: no hay fila de `units` cuyo `player_id` no resuelva, así que no hay detección que escribir. Nunca se asigna un owner por defecto ni se «adopta» una unidad: cualquiera de esas reparaciones equivale a regalar un activo.

---

<a id="inv-unit-007"></a>
## INV-UNIT-007 — `GARRISONED` implica ciudad y fila de guarnición

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (derivar desde `garrisons`) |
| Cobertura | **Parcial**: `TestGuarnecerConTratadoActivoDejaLaUnidadGarrisonedYPersistida`, `TestSalirDeLaGuarnicionDevuelveLaUnidadAlMapaYBorraLaFila` y `TestTratadosYGuarnicionesSobrevivenAUnReinicio` (integration, `internal/persistence/postgres`) cubren estado y fila juntos; la reconciliación de carga, `TestLaGuarnicionSeDerivaDeSuTabla` (`internal/game/loadcheck`) y `TestLaCargaReparaYAuditaLoQueElEsquemaNoGarantiza` (integration). Falta decidir qué es `units.city_id` de una unidad guarnecida (M7, entregable 6) |

**Enunciado.** `status = GARRISONED` implica `city_id IS NOT NULL` y la existencia de una fila de `garrisons` para esa unidad.

**Razón.** `GARRISONED` es el único estado en que la unidad **no tiene ubicación espacial**: no ocupa tile ([INV-UNIT-003](#inv-unit-003)) y su posición consolidada es un dato histórico que ninguna lógica lee. Si ese estado no viniera acompañado de una guarnición concreta, la unidad quedaría literalmente en ninguna parte: no está en el mundo, no está en una ciudad, y no hay ninguna operación que pueda devolverla al juego, porque salir de la guarnición requiere saber de qué guarnición se sale.

Es la misma clase de fallo que [INV-UNIT-005](#inv-unit-005) —un estado que afirma un hecho que otra tabla debe confirmar— y se resuelve igual: la tabla es la fuente y el `status` se deriva de ella.

**Cómo se garantiza.**

- `DB` — `garrisons.unit_id bigint PRIMARY KEY REFERENCES units (id) ON DELETE CASCADE` y `garrisons.city_id bigint NOT NULL REFERENCES cities (id) ON DELETE CASCADE`: una fila de guarnición siempre nombra una ciudad existente, y una unidad tiene como máximo una ([INV-GARR-001](diplomacy.md#inv-garr-001)).
- `DOMAIN` — `GarrisonRepo.Enter` pone `status = GARRISONED` y escribe la fila de `garrisons` en la **misma transacción**, junto con la cancelación del movimiento activo y el evento `UnitGarrisoned`; `GarrisonRepo.Leave` borra la fila y deja la unidad `IDLE` en su tile de salida en otra. Ningún comando de red las invoca todavía (M7, entregable 6).
- `DOMAIN` — `units.city_id` **no lo toca ninguna de las dos**. Para las unidades del MVP la mitad «`city_id IS NOT NULL`» se cumple igualmente, porque el alta crea los aldeanos con la ciudad de su dueño. Pero la spec de guarnición dice que la entrada fija `units.city_id` a la ciudad anfitriona, y [INV-GARR-003](diplomacy.md#inv-garr-003) exige que coincida con `garrisons.city_id`; mientras que [RN-CITY-012](../specs/city.md) cuenta la población de una ciudad por `units.city_id`. Las dos cosas no pueden ser ciertas a la vez para una unidad guarnecida en una ciudad aliada: o la población viaja con ella, o `city_id` no es la ciudad de la guarnición. Es una decisión de diseño pendiente y se toma con los comandos de guarnición.
- `DOMAIN` — la validación de carga (`internal/game/loadcheck`, `EO-119`) lee todas las filas de `garrisons` (`LoadCheckRepo.Garrisons`) y deriva el estado de ellas, en los dos sentidos: una unidad `GARRISONED` sin fila vuelve a `IDLE` en su última posición, y una unidad viva con fila pasa a `GARRISONED`. Una fila de una unidad que no está viva se borra: no guarnece nada.
- `DB` — `units_city_idx` hace barata la consulta de reconciliación por ciudad.

**Cómo se verifica.**

- `TestGuarnecerConTratadoActivoDejaLaUnidadGarrisonedYPersistida` (integration) — tras entrar, la fila existe con la ciudad anfitriona y la unidad releída de la base está `GARRISONED`.
- `TestSalirDeLaGuarnicionDevuelveLaUnidadAlMapaYBorraLaFila` (integration) — tras salir, la fila ya no existe y la unidad está `IDLE` en el tile de salida.
- `TestTratadosYGuarnicionesSobrevivenAUnReinicio` (integration) — la fila sobrevive a un reinicio.
- `TestLaGuarnicionSeDerivaDeSuTabla` (unit, `internal/game/loadcheck`) — los tres casos, y que la posición histórica de una unidad que pasa a `GARRISONED` no aborta la carga.
- `TestLaCargaReparaYAuditaLoQueElEsquemaNoGarantiza` (integration) — lo mismo en la base: los estados corregidos, la fila de la unidad `DEAD` borrada y un `InvariantRepaired` por caso.

**Violación en runtime.** Detección en la validación de carga. Log `invariant_violation` con `inv_id=INV-UNIT-007` y `entity=unit:<id>`. Política `REPAIR`: prevalece `garrisons`, que es la fuente; se escribe el estado derivado o se borra la fila huérfana, y se registra un `InvariantRepaired`. La mitad «`city_id IS NOT NULL`» no se repara: depende de la decisión pendiente sobre `units.city_id`.

---

<a id="inv-unit-008"></a>
## INV-UNIT-008 — `HIDDEN` implica una Safe Zone activa y reposo

| Campo | Valor |
|---|---|
| Severidad | MEDIO |
| Aplicación | DOMAIN, TEST |
| Milestone | M5 |
| Política ante violación | REPAIR: fuera de zona vuelve a `IDLE`; con movimiento `ACTIVE` pasa a `MOVING` |
| Cobertura | **Cubierto** por `TestReglaDeOcultamiento` (`internal/domain/safezone`) y por los tests de `internal/game/simulation/safezones_test.go`, que lo comprueban sobre todo el mundo tras cada tick |

**Enunciado.** `status = HIDDEN` implica que la unidad pertenece a una Safe Zone activa; el ocultamiento se evalúa sólo en reposo, y una unidad `HIDDEN` que recibe una orden de movimiento pasa a `MOVING` y deja de estar oculta.

**Razón.** El ocultamiento es una propiedad **derivada de la posición**, no un estado que la unidad conserve por su cuenta. Si `HIDDEN` pudiera persistir fuera de una zona, una unidad se volvería invisible en campo abierto, lo que rompe la única garantía que el interest management ofrece al resto de jugadores: lo que está en tu área de interés y no está oculto, lo ves.

La cualificación «sólo en reposo» es lo que hace el invariante barato de sostener. Evaluar el ocultamiento en cada tick de un movimiento obligaría a emitir `entity.despawn` y `entity.spawn` cada vez que la unidad cruzara el borde de la zona, lo que además filtraría la geometría exacta de la zona a cualquiera que observara la secuencia. Evaluándolo sólo al detenerse, el borde no se puede sondear.

**Cómo se garantiza.**

- `DOMAIN` — la transición a `HIDDEN` se evalúa en la fase 5, sólo sobre unidades en reposo (`IDLE`), consultando el índice de safe zones para su tile ([INV-SAFE-003](territory.md#inv-safe-003)).
- `DOMAIN` — aceptar una orden de movimiento pone `status = MOVING` en el mismo paso, de modo que `HIDDEN` y `MOVING` son mutuamente excluyentes por construcción: `units_status_valid` es un enum de un solo valor por fila.
- `DOMAIN` — una unidad `HIDDEN` **sí acepta órdenes** (a diferencia de `GARRISONED` y `DEAD`): el ocultamiento no inmoviliza, sólo oculta.
- `DOMAIN` — la exclusión de los deltas dirigidos a terceros la garantiza [INV-SAFE-004](territory.md#inv-safe-004), no esta ficha.

**Cómo se verifica.**

- `TestReglaDeOcultamiento` (unit) — la tabla completa de `safezone.NextStatus`. `TestUnaUnidadOcultaQueRecibeUnMoveSeRevelaEnElMismoTick` (simulation) — una orden de movimiento sobre una unidad `HIDDEN` la deja `MOVING` en el mismo tick. Todos los tests de `internal/game/simulation/safezones_test.go` comprueban tras cada tick que ninguna unidad `HIDDEN` cae fuera de una zona.

**Violación en runtime.** Detección en la fase 5, sobre las unidades que cambiaron. No hay reconciliación periódica de todo el mundo. Una unidad `HIDDEN` fuera de zona vuelve a `IDLE` y los terceros reciben `entity.spawn`; con movimiento `ACTIVE` pasa a `MOVING` y se registra `invariant_violation` con `inv_id=INV-SAFE-003`, la formulación precisa de esta ficha. Revelar una unidad que no debía estar oculta es preferible a ocultar una que no cumple la condición.

---

<a id="inv-unit-009"></a>
## INV-UNIT-009 — Todo `unit_type` persistido existe en el catálogo

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | EXCLUDE (la unidad no entra en la simulación) |
| Cobertura | **Cubierto**: `TestLookupSoloConoceElCatalogo` (`internal/domain/unit`), el pipeline de movimiento de `internal/game/simulation`, que depende de `unit.Lookup`, y la comprobación de carga, `TestLasUnidadesSeExcluyenSeReparanOAbortan` (`internal/game/loadcheck`) |

**Enunciado.** Para toda fila de `units`, `unit.Lookup(unit_type)` resuelve: el `unit_type` pertenece al catálogo cargado. No hay `CHECK` en la base porque el catálogo vive en código.

**Razón.** `unit.Lookup` devuelve las estadísticas que gobiernan el comportamiento de la unidad: `MaxHP`, `BaseMsPerTile` y `PopulationCost`. Un `unit_type` que no resuelve deja a la unidad **sin velocidad**, y sin velocidad no hay polilínea que construir: `handleMoveUnit` responde `INTERNAL_ERROR` y la unidad queda permanentemente inmóvil. Es la misma pérdida de activo silenciosa que describe [INV-UNIT-001](#inv-unit-001), por otra vía.

La ausencia de `CHECK` es deliberada y merece decirse en voz alta: `units.unit_type` es `text NOT NULL` **sin restricción de dominio**, porque el catálogo es un `map[Type]Definition` en `internal/domain/unit` y una migración no puede conocerlo. La consecuencia honesta es que la base de datos **acepta** un `unit_type` inventado; quien lo detecta es la carga. Cuando el catálogo se mueva a la base de datos —previsto para cuando haya decenas de tipos— este invariante ganará una garantía `DB` por clave foránea.

**Cómo se garantiza.**

- `DOMAIN` — `unit.Lookup(t Type) (Definition, error)` es el **único** acceso al catálogo y devuelve `ErrUnknownType` para un tipo ausente. No hay ningún `switch` sobre `unit_type` disperso por el código.
- `DOMAIN` — la creación de unidades sólo usa tipos del catálogo: el alta escribe `VILLAGER`, que es la única entrada del MVP.
- `DOMAIN` — `handleMoveUnit` llama a `Lookup` antes del A\* y responde `INTERNAL_ERROR` si falla, en lugar de asumir una velocidad por defecto.
- `DOMAIN` — la validación de carga (`internal/game/loadcheck`, `EO-119`) consulta el catálogo para cada unidad leída: `scanUnit` convierte el texto en `unit.Type` sin mirarlo, y una unidad de tipo desconocido ya no llega a la simulación.

**Cómo se verifica.**

- `TestLookupSoloConoceElCatalogo` (unit, `internal/domain/unit`) — `VILLAGER` resuelve con sus cuatro estadísticas; un tipo en minúsculas, vacío, `TOWN_CENTER` o inventado devuelve `ErrUnknownType`, nunca una definición por defecto.
- `TestVerticalSliceMovimiento` y `TestRechazosDeMovimiento` (simulation) — todo el pipeline de movimiento depende de que `Lookup` resuelva.
- `TestLasUnidadesSeExcluyenSeReparanOAbortan` (unit, `internal/game/loadcheck`) — una unidad `KNIGHT` no entra en el resultado y su ciudad la sigue contando en la población.

**Violación en runtime.** Detección en la validación de carga. Log `invariant_violation` con `inv_id=INV-UNIT-009`, `entity=unit:<id>` y el tipo en `detail`. Política `EXCLUDE`: la unidad no se incorpora a la simulación y su fila no se toca, de modo que un despliegue que vuelva a conocer el tipo la recupera intacta; si tenía un movimiento `ACTIVE`, la recuperación lo cierra como huérfano. No aborta el arranque: una fila mala no deja sin servicio al resto del mundo. No se sustituye por `VILLAGER`: asignar estadísticas ajenas a una unidad es cambiar el juego sin decirlo.

---

<a id="inv-unit-010"></a>
## INV-UNIT-010 — Owner y tipo son inmutables tras la creación

| Campo | Valor |
|---|---|
| Severidad | MEDIO |
| Aplicación | DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | REJECT (la escritura no se aplica) |
| Cobertura | **Cubierto** por `TestNingunaEscrituraCambiaElDuenoNiElTipoYTodasMantienenElChunk` (integration, `internal/persistence/postgres`) |

**Enunciado.** `units.player_id` y `units.unit_type` son inmutables tras la creación en MVP: ninguna ruta de escritura los modifica.

**Razón.** Es la propiedad que hace baratos a [INV-UNIT-006](#inv-unit-006) y a [INV-UNIT-009](#inv-unit-009). Mientras el owner no cambie, «una unidad pertenece a un único jugador» no necesita proteger ningún estado intermedio, y la ausencia de `units.version` no es una carencia: no hay dos escritores compitiendo por un campo que nadie escribe. Mientras el tipo no cambie, las estadísticas resueltas al cargar la unidad valen para toda su vida y no hay que revalidarlas en cada uso.

Declararlo como invariante y no dejarlo implícito tiene un propósito concreto: la primera transferencia de ownership que alguien implemente —captura de unidades, herencia de un jugador eliminado, conversión— **rompe este invariante a propósito**, y debe hacerlo con un ADR, una columna de versión y la revisión de las dos fichas de arriba. Un invariante `MEDIO` que se deroga conscientemente es mejor que una suposición tácita que se rompe en silencio.

**Cómo se garantiza.**

- `DOMAIN` — hay exactamente cinco sentencias que escriben `units`: el `INSERT` del alta (`UnitRepo.Create`), que fija los dos campos, y cuatro `UPDATE`: el volcado por lotes (`FlushPositions`, que usan también el inicio y el cierre de un movimiento), `SetStatus`, y el de la salida de guarnición (`GarrisonRepo.Leave`). Las cuatro enumeran sus columnas —posición, chunk, estado— y ninguna nombra `player_id` ni `unit_type`; no hay un `UPDATE` de la fila entera que pudiera reescribir el owner con un valor obsoleto en memoria.
- `DOMAIN` — los campos de `unit.Unit` son públicos, pero fuera de los tests sólo se asignan al construir la unidad en el alta y al leerla de la base (`scanUnit`).

**Cómo se verifica.**

- `TestNingunaEscrituraCambiaElDuenoNiElTipoYTodasMantienenElChunk` (integration, `internal/persistence/postgres`) — toma la huella de `(id, player_id, unit_type)` de todas las unidades, ejecuta las cuatro sentencias de actualización y comprueba que la huella no cambia. Verificado por mutación: un `SetStatus` que reescribiera `unit_type` lo hace fallar.

**Violación en runtime.** No hay detección en runtime: la garantía es que ninguna sentencia puede producirla, y el test la vigila. Un cambio consumado de owner se trataría como incidente, igual que en [INV-UNIT-006](#inv-unit-006): equivale a regalar un activo.

---

<a id="inv-unit-011"></a>
## INV-UNIT-011 — Las columnas de chunk siempre coinciden con la posición

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M2 |
| Política ante violación | REPAIR (recomputar desde `x`, `y`) |
| Cobertura | **Cubierto** por `TestFlushPositionsEscribeElLoteYMantieneElChunk` y `TestNingunaEscrituraCambiaElDuenoNiElTipoYTodasMantienenElChunk` (integration, `internal/persistence/postgres`), y por `TestConversionTileChunk` (`internal/game/world`) |

**Enunciado.** `units.chunk_x` y `units.chunk_y` son siempre el chunk de `(x, y)`: `chunk_x = x / EO_CHUNK_SIZE` y `chunk_y = y / EO_CHUNK_SIZE`, en toda escritura de posición.

**Razón.** `chunk_x` y `chunk_y` son una **desnormalización deliberada**, pensada para el día en que el mundo deje de caber en RAM y la carga pase a ser por región: entonces «todas las unidades vivas de estos chunks» será un recorrido de índice (`units_chunk_idx`) en lugar de un cálculo sobre cada fila.

**Hoy nadie lee esas columnas.** `ListAlive` carga todas las unidades vivas, y el índice por chunk de la simulación (`State.unitsByChunk`), del que salen los snapshots y el reparto de deltas, se deriva de `(x, y)` con `World.ChunkOf` al incorporar cada unidad. Una columna de chunk equivocada no tiene hoy ningún efecto visible; el invariante existe para que no lo tenga el día en que se lean. Entonces la divergencia sería **silenciosa**: la unidad no aparecería en los snapshots de quien mira donde realmente está, y sí —como un fantasma— para quien mira el chunk que la columna dice. Es el modo de fallo que [INV-WORLD-006](world.md#inv-world-006) describe para una partición mal formada, materializado en una columna.

**Cómo se garantiza.**

- `DOMAIN` — toda sentencia que escribe la posición escribe el chunk en la **misma sentencia**: el `INSERT` del alta, el volcado por lotes y la salida de guarnición. No hay ninguna ruta que actualice `x`/`y` sin `chunk_x`/`chunk_y`.
- `DOMAIN` — el cálculo **no** está en un solo sitio: `UnitRepo` divide por el tamaño de chunk con el que se construyó (`cfg.ChunkSize`, el mismo del mundo) y `GarrisonRepo.Leave` recibe el chunk ya calculado de quien lo llama. La división es la misma que la de `World.ChunkOf`, y el barrido de integración comprueba el resultado, no la forma de llegar a él.
- `DOMAIN` — durante un movimiento activo la posición **no se persiste** ([INV-PERSIST-004](persistence.md#inv-persist-004)): la posición y su chunk se escriben al empezar y al terminar, en la transacción del inicio y en la del cierre.
- `DB` — `units_chunk_idx` es un índice parcial sobre `(chunk_x, chunk_y) WHERE status <> 'DEAD'`, coherente con que las unidades muertas no se emiten a nadie.

**Cómo se verifica.**

- `TestFlushPositionsEscribeElLoteYMantieneElChunk` (integration, `internal/persistence/postgres`) — el volcado por lotes escribe posición y chunk de forma coherente.
- `TestNingunaEscrituraCambiaElDuenoNiElTipoYTodasMantienenElChunk` (integration) — tras las cuatro sentencias de actualización, ninguna fila de `units` tiene el chunk descuadrado. Verificado por mutación: un volcado que sumara uno al chunk lo hace fallar.
- `TestConversionTileChunk` (unit, `internal/game/world`) — la conversión de referencia es correcta.

**Violación en runtime.** No hay detección: como nadie lee las columnas, una divergencia no tendría hoy síntoma. Cuando la carga sea por región, la validación de carga deberá recalcular el chunk de cada unidad y prevalecerán `x` e `y`, que son la fuente.

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

Los invariantes de unidad son consecuencia de los de movimiento durante el tránsito: mientras un movimiento válido esté activo, la posición de la unidad es válida **por construcción**. Lo único que puede invalidar un movimiento ya en curso es una muralla nueva, y la fundación lo resuelve en el mismo tick ([RN-CITY-022](../specs/city.md)). Por eso el esfuerzo de verificación se concentra en [movement.md](movement.md).

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-UNIT-001 | `internal/game/simulation` (`footprint.go`), `internal/game/world` | [INV-WORLD-001](world.md#inv-world-001), [INV-MOVE-004](movement.md#inv-move-004) |
| INV-UNIT-002 | `internal/domain/unit` | [INV-MOVE-007](movement.md#inv-move-007) |
| INV-UNIT-003 | `internal/domain/unit`, `internal/domain/garrison`, `internal/game/simulation` | [INV-CITY-006](city.md#inv-city-006) |
| INV-UNIT-004 | `internal/domain/unit` | [INV-PERSIST-004](persistence.md#inv-persist-004) |
| INV-UNIT-005 | `internal/domain/unit`, `internal/domain/movement` | [INV-MOVE-001](movement.md#inv-move-001), [INV-PERSIST-002](persistence.md#inv-persist-002) |
| INV-UNIT-006 | `internal/domain/unit` | [INV-PLAYER-001](player.md#inv-player-001) |
| INV-UNIT-007 | `internal/persistence/postgres` (`GarrisonRepo`) | [INV-GARR-002](diplomacy.md#inv-garr-002), [INV-UNIT-003](#inv-unit-003) |
| INV-UNIT-008 | `internal/domain/safezone`, `internal/game/simulation` | [INV-SAFE-003](territory.md#inv-safe-003) |
| INV-UNIT-009 | `internal/domain/unit` | [../specs/unit.md](../specs/unit.md) |
| INV-UNIT-010 | `internal/domain/unit` | [INV-UNIT-006](#inv-unit-006) |
| INV-UNIT-011 | `internal/persistence/postgres`, `internal/game/world` | [INV-WORLD-006](world.md#inv-world-006) |
