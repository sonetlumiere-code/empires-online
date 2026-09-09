# Invariantes de Persistencia (INV-PERSIST-xxx)

Propiedades de durabilidad, recuperación tras reinicio, separación de capas de estado y disciplina de migraciones.

Formato y severidades: [README.md](README.md). Capas de estado y estrategia de persistencia: canon §4 y §12.

---

## Contexto: las tres capas de estado

El canon §1.4 fija una jerarquía que estos invariantes protegen:

| Capa | Rol | Qué contiene | Sobrevive a un reinicio |
|---|---|---|---|
| **PostgreSQL** | *Durable source of truth* | Jugadores, ciudades, unidades, movimientos, mundo, sesiones, idempotencia | Sí |
| **Redis** | *Hot / transient state* | Presencia, sesiones, locks, cooldowns, caché, idempotencia de corto plazo | No garantizado |
| **RAM del Game Server** | Simulación activa | Índices de ocupación, conjuntos de interés, movimientos activos en curso | No |

**Redis nunca sustituye a PostgreSQL.** Es la frase literal del canon y el eje de [INV-PERSIST-003](#inv-persist-003).

### Clasificación de cada dato (canon §12)

Todo documento de diseño debe poder responder a las cuatro preguntas; aquí está la respuesta consolidada para el MVP:

| Estrategia | Qué entra | Mecanismo |
|---|---|---|
| **Write-through transaccional, con escritura diferida** | Creación de player/city/unit, inicio y finalización de movimiento, transiciones de presencia, `world_events` | El alta de jugador es una transacción síncrona en `internal/httpapi`; el resto se aplica en RAM y se **encola** en la cola de persistencia, que unos workers escriben fuera del tick |
| **Dirty-flag + flush periódico** | Posiciones consolidadas de unidades, `hp` | Cada `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS = 50` ticks, es decir 5 s a 10 Hz |
| **Reconstruible (no se persiste)** | Posición durante un movimiento activo, conjuntos de interés y suscripciones, presencia derivada | Derivación analítica desde `unit_movements` / recálculo |

El tick **nunca** ejecuta I/O bloqueante contra PostgreSQL (canon §6): la persistencia se encola (fase 8, `enqueue persistence`) y la ejecutan workers fuera del tick, con hasta **3 intentos** y backoff, y una compensación `OnPermanentFailure` si se agotan. La profundidad de esa cola es `eo_persistence_queue_depth`.

> **Ventana de riesgo, dicha en voz alta.** Que la escritura sea diferida tiene un coste que esta documentación no oculta: si el proceso muere entre la aceptación de un comando y su `COMMIT` —típicamente unas decenas de milisegundos—, ese efecto se pierde y la entidad queda en su último estado consolidado. Es un **RPO documentado**, no un descuido, y es la contrapartida de que el tick no bloquee. Lo que el diseño sí garantiza es que nada queda a medias: cada trabajo de la cola es una unidad atómica, y `OnPermanentFailure` compensa en el mundo lo que la base de datos nunca llegó a conocer.

---

<a id="inv-persist-001"></a>
## INV-PERSIST-001 — Lo confirmado sobrevive a la desconexión

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M3 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto** por `TestElMovimientoContinuaConElJugadorDesconectado` (`internal/game/simulation`); `TestMovimientoSobreviveAlCicloDePersistencia` (integration) está **diseñado y no ejecutado** |

**Enunciado.** Una transacción durable ya confirmada (`COMMIT` en PostgreSQL) permanece confirmada con independencia de lo que ocurra con la conexión WebSocket que la originó: cierre limpio, corte abrupto, timeout de lectura o cierre con cualquiera de los códigos `4400`, `4401`, `4403`, `4408`, `4429`, `4500`.

**Razón.** Es la traducción directa del principio de mundo persistente (canon §1.2): *ningún estado durable depende de un WebSocket vivo*. Si un `COMMIT` pudiera deshacerse al caerse el socket, el juego perdería su propiedad fundamental —el mundo evoluciona sin jugadores conectados— y aparecerían pérdidas de progreso arbitrarias en cada corte de red del jugador, que en un MMO ocurren constantemente.

El antipatrón que este invariante prohíbe es concreto y tentador: acoplar el ciclo de vida de la transacción al `context.Context` de la conexión. Con ese diseño, cancelar el contexto al cerrarse el socket aborta transacciones en vuelo y —según cómo esté escrito el `defer`— puede llegar a ejecutar un `ROLLBACK` sobre trabajo ya efectuado.

**Cómo se garantiza.**

- `DOMAIN` — el contexto de una transacción durable **no** desciende del contexto de la conexión. Los trabajos de la cola de persistencia se ejecutan con el contexto del proceso y su propio timeout: el cierre del socket no cancela una escritura en vuelo. Éste es el punto exacto del invariante, y es lo que hace que un jugador que se desconecta a mitad de una orden no la pierda.
- `DOMAIN` — el orden **no** es «`COMMIT` y después notificar»: el efecto se aplica en RAM, se notifica al cliente y la escritura se encola. Lo que el invariante garantiza es lo que dice su enunciado —que un `COMMIT` ya hecho no se deshace por lo que le pase al socket—, no que la notificación llegue después del `COMMIT`. La ventana entre notificación y `COMMIT` es el RPO documentado en el contexto de este documento.
- `DOMAIN` — la simulación **continúa** con el jugador desconectado: un movimiento en curso no se cancela porque el socket se caiga. Es la manifestación directa del principio de mundo persistente.
- `DOMAIN` — los workers de persistencia drenan su cola durante el apagado ordenado antes de terminar el proceso: `Queue.Drain(15s)` espera a que se vacíe o a que expire el plazo, y en ese caso lo registra. El apagado completo tiene su propio plazo de 25 s.
- `DOMAIN` — cada trabajo se reintenta hasta **3 veces** con backoff; si se agotan, `OnPermanentFailure` compensa en el mundo (por ejemplo, deteniendo la unidad cuyo movimiento no se pudo escribir) en lugar de dejar RAM y disco divergentes en silencio.
- `DB` — PostgreSQL con durabilidad por defecto (`synchronous_commit` activo). Desactivarlo requeriría ADR y sería una violación directa de este invariante.

**Cómo se verifica.**

- `TestElMovimientoContinuaConElJugadorDesconectado` (simulation, `internal/game/simulation`) — **existe y pasa**: el movimiento sigue avanzando y completándose aunque el jugador pierda la sesión.
- `TestMovimientoSobreviveAlCicloDePersistencia` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: el movimiento escrito se recupera intacto.
- Test previsto: `Test_INV_PERSIST_001_ConnectionContextDoesNotCancelTransaction` (unit) — se cancela el contexto de conexión durante una escritura en vuelo; la escritura llega a `COMMIT`.
- Test previsto: `Test_INV_PERSIST_001_ShutdownDrainsQueue` (integration) — un apagado ordenado con la cola llena no pierde escrituras.

**Violación en runtime.** Detección: tras reconectar, el cliente reenvía el `requestId` y la búsqueda en `idempotency_keys` no encuentra el registro de un efecto que sí ocurrió (o al revés). Log `invariant_violation` con `inv_id=INV-PERSIST-001`, `request_id` y `player_id`. Política `FAIL_FAST` del worker afectado y escalado: la pérdida de durabilidad es el fallo más caro de diagnosticar a posteriori porque no deja rastro donde se produjo.

---

<a id="inv-persist-002"></a>
## INV-PERSIST-002 — Tras reinicio el estado es consistente con lo confirmado

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M4 |
| Política ante violación | FAIL_FAST en arranque |
| Cobertura | **Cubierto** por `TestRecuperacionCompletaTrasReinicio`, `TestRecuperacionMovimientoEnCurso`, `TestRecuperacionMovimientoVencidoDuranteLaCaida` y `TestRecuperacionConPolilineaInvalida` (`internal/game/simulation`) |

**Enunciado.** Tras un reinicio del Game Server —ordenado o por crash—, el estado reconstruido en memoria es consistente con todo lo confirmado en PostgreSQL antes del corte y no contiene nada que no estuviera confirmado.

**Razón.** Es la propiedad que hace del reinicio un no-evento para los jugadores. Sin ella, cada despliegue produce anomalías: unidades que vuelven a una posición antigua, movimientos que se pierden, ciudades que retroceden de estado de presencia.

El caso más delicado es el de los movimientos en vuelo, y el canon §7 fija exactamente cómo se resuelve:

```
al arrancar (simulation.Hydrate):
  cargar unit_movements WHERE status = 'ACTIVE'
  para cada movimiento m:
      si la polilínea NO valida (movement.Validate):
          cerrar como FAILED; la unidad se queda EXACTAMENTE donde estaba
          (nunca se teletransporta a nadie por un dato dudoso)
      si no, si m.arrival_time_ms <= Clock.NowMs():
          completar: snap al último waypoint, status = COMPLETED,
          units.status = IDLE, emitir UnitMovementCompleted
      si no:
          reanudar desde la polilínea; la posición se sigue derivando con PositionAt
```

El tercer caso —polilínea inválida— es el que más cuidado exige y el que más fácil sería hacer mal. La tentación es «arreglar» el movimiento o llevar la unidad a su destino previsto; ambas cosas mueven la unidad de un jugador basándose en un dato que ya se sabe corrupto. Dejarla donde estaba es la única opción que no inventa nada.

Este diseño es posible precisamente porque la posición es **analíticamente reconstruible** (canon §7): no hace falta replay de ticks ni un log de eventos para saber dónde estaría la unidad. Un servidor caído 20 minutos reconstruye en un instante el estado exacto que habría tenido si nunca hubiera caído.

**Cómo se garantiza.**

- `DOMAIN` — secuencia de arranque estricta, cada paso con su validación: cargar y validar mundo ([INV-WORLD-002](world.md#inv-world-002), [INV-WORLD-005](world.md#inv-world-005)) → validar continuidad del tick ([INV-WORLD-004](world.md#inv-world-004)) → cargar jugadores, ciudades y unidades → cargar movimientos `ACTIVE` y aplicar la regla de arriba → reconciliar `units.status` desde `unit_movements` ([INV-UNIT-005](units.md#inv-unit-005)) → reconstruir índices derivados (ocupación, interés) → abrir el puerto de `EO_HTTP_ADDR`.
- `DOMAIN` — el orden importa: **no se aceptan conexiones hasta que la reconstrucción termina**. `GET /ready` (canon §18) devuelve fallo mientras tanto; `GET /health` responde porque el proceso vive.
- `DOMAIN` — solo se cargan movimientos `ACTIVE`; los terminales no se reabren ([INV-MOVE-007](movement.md#inv-move-007)).
- `DOMAIN` — nada del estado en RAM se considera fuente: los índices se reconstruyen desde PostgreSQL, nunca se restauran desde un volcado propio.
- `DOMAIN` — el `Clock` inyectado permite ejercitar el arranque con tiempos arbitrarios en tests de recovery.

**Cómo se verifica.**

- `TestRecuperacionCompletaTrasReinicio` (recovery, `internal/game/simulation`) — **existe y pasa**: se construye un estado, se toma una huella y la reconstrucción coincide.
- `TestRecuperacionMovimientoEnCurso` (recovery) — **existe y pasa**: movimiento iniciado, reinicio a mitad de trayecto, la unidad continúa hacia el mismo destino con el mismo `arrival_time_ms`. Es el test de recovery de referencia del canon §19 («el movimiento sobrevive»).
- `TestRecuperacionMovimientoVencidoDuranteLaCaida` (recovery) — **existe y pasa**: reinicio después de `arrival_time_ms`: el movimiento se cierra como `COMPLETED`, la unidad aparece en el destino.
- `TestRecuperacionConPolilineaInvalida` (recovery) — **existe y pasa**: una polilínea corrupta cierra el movimiento como `FAILED` y deja la unidad donde estaba.
- `TestBootstrapFallidoNoDejaNadaAMedias` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: una transacción a medias no deja rastro.
- Test previsto: `Test_INV_PERSIST_002_NotReadyUntilReconstructionCompletes` (integration) — `GET /ready` falla durante la reconstrucción y sólo entonces pasa a `200`.

**Violación en runtime.** Detección en las validaciones de cada paso del arranque y en la reconciliación. Log `invariant_violation` con `inv_id=INV-PERSIST-002` y el paso que falló. Política `FAIL_FAST`: el proceso no abre el puerto. Un servidor que arranca con estado inconsistente empieza inmediatamente a persistir consecuencias de ese estado, y a partir de ahí el daño es irreversible.

---

<a id="inv-persist-003"></a>
## INV-PERSIST-003 — Ningún dato durable existe solo en Redis

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M3 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Sin cobertura ejecutada**: `TestPresenciaSeRegistraYExpira`, `TestLatidoRenuevaLaPresencia`, `TestLatidoSobreClaveExpiradaDevuelveFalse` y `TestSoloLaSesionPropietariaRetiraLaPresencia` (`internal/persistence/redis`) están **diseñados y no ejecutados** (Docker) |

**Enunciado.** Todo dato clasificado como durable tiene su representación autoritativa en PostgreSQL. Redis contiene exclusivamente estado *hot/transient*: presencia, sesiones, locks, cooldowns, caché e idempotencia de corto plazo; vaciar Redis por completo no debe producir ninguna pérdida de estado durable.

**Razón.** Redis es un almacén con TTL y sin garantía de durabilidad en el uso que le da este proyecto. Todas sus claves del MVP llevan expiración por diseño:

| Clave | TTL | Naturaleza |
|---|---|---|
| `presence:player:{playerId}` | `EO_PRESENCE_TTL_SECONDS` = 30 s | Presencia observable; **no** es lo que dispara la transición de la ciudad |
| `ticket:jti:{jti}` | 120 s | Anti-replay de tickets; sólo necesita cubrir el TTL de 60 s del ticket |
| `idem:{playerId}:{requestId}` | 300 s | Idempotencia de corto plazo |

> **Precisión sobre la presencia.** La señal que degrada una ciudad a `OFFLINE_PENDING` **no** es la expiración de esta clave: la decide el game loop comparando su marca `disconnectedAt` en RAM contra `DisconnectGrace` (= `EO_PRESENCE_TTL_SECONDS`). La clave de Redis existe para observadores externos y para el futuro multiproceso ([INV-CITY-005](city.md#inv-city-005)). Eso refuerza este invariante en vez de debilitarlo: la decisión de presencia no depende de un almacén sin garantía de durabilidad.

Si un dato durable viviera solo aquí, se perdería por expiración, por evicción bajo presión de memoria o por un reinicio de Redis, y lo haría **en silencio**: no hay error, simplemente el dato deja de estar. La prueba de fuego del invariante es directa: `FLUSHALL` no debe costar nada más que reconexiones y un recálculo de presencia.

El caso límite que merece nombrarse es la idempotencia, y hoy es la **excepción reconocida** a este invariante. `idem:{playerId}:{requestId}` vive en Redis por velocidad, y el diseño prevé que para comandos durables el registro vaya **también** a `idempotency_keys` en PostgreSQL (canon §13), precisamente porque perder la clave de Redis permitiría reejecutar un comando durable. Esa segunda mitad **no está implementada**: la tabla existe y ninguna ruta escribe en ella ([INV-SEC-007](security.md#inv-sec-007)).

La consecuencia honesta es que la garantía de idempotencia **sí** se pierde con un `FLUSHALL`, a diferencia de todo lo demás. No es estado de juego —no se pierde ninguna unidad, ninguna ciudad ni ningún movimiento—, pero sí es una garantía que el invariante promete y que hoy descansa sólo en Redis. Cerrar ese hueco es trabajo pendiente, y está listado como tal en [INV-SEC-007](security.md#inv-sec-007).

**Cómo se garantiza.**

- `DOMAIN` — la clasificación de cada dato es explícita en el diseño (tabla del contexto de este documento) y se revisa en cada PR que añada una clave de Redis: la pregunta obligatoria es «¿qué se pierde si esta clave desaparece?». Si la respuesta incluye algo durable, el diseño es incorrecto.
- `DOMAIN` — los repositorios de Redis viven en `internal/persistence/redis` y no exponen ninguna operación de escritura sin TTL. Una clave sin expiración es una señal de que se está usando Redis como almacén durable.
- `DOMAIN` — la clasificación es lo que se revisa, no la implementación de cada clave: la pregunta obligatoria en revisión de PR es «¿qué se pierde si esta clave desaparece?». Para la idempotencia, la respuesta hoy es «la protección contra un segundo efecto», y por eso está anotada arriba como excepción abierta en lugar de darse por resuelta.
- `DOMAIN` — la presencia derivada es **reconstruible** (canon §12): si Redis se vacía, todos los jugadores aparecen sin clave de presencia, lo que lleva sus ciudades a `OFFLINE_PENDING` por la vía normal ([INV-CITY-005](city.md#inv-city-005)). Los conectados renuevan su clave en el siguiente heartbeat, a los `EO_PRESENCE_HEARTBEAT_SECONDS` = 10 s como mucho. El sistema converge sin intervención.

**Cómo se verifica.**

- `TestPresenciaSeRegistraYExpira` (integration, `internal/persistence/redis`) — **diseñado, aún no ejecutado**: la clave de presencia nace con TTL y expira sola.
- `TestLatidoRenuevaLaPresencia` y `TestLatidoSobreClaveExpiradaDevuelveFalse` (integration, `internal/persistence/redis`) — **diseñados, aún no ejecutados**: el heartbeat renueva el TTL y distingue la clave viva de la expirada.
- `TestSoloLaSesionPropietariaRetiraLaPresencia` (integration, `internal/persistence/redis`) — **diseñado, aún no ejecutado**: una sesión no puede borrar la presencia de otra.
- Test previsto: `Test_INV_PERSIST_003_NoDurableDataOnlyInRedis` (integration) — se construye un estado completo, se ejecuta `FLUSHALL` sobre Redis, y todo el estado durable sigue íntegro en PostgreSQL y en la simulación.
- Test previsto: `Test_INV_PERSIST_003_AllRedisKeysHaveTTL` (integration) — ninguna clave creada durante un escenario funcional tiene `TTL = -1`.

**Violación en runtime.** Detección en el test de TTL de CI y en una aserción del repositorio de Redis que rechaza escrituras sin expiración. Log `invariant_violation` con `inv_id=INV-PERSIST-003` y la clave implicada. Política `FAIL_FAST` de la operación de escritura: la clave sin TTL no se crea.

---

<a id="inv-persist-004"></a>
## INV-PERSIST-004 — El flush no contradice un movimiento activo

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M4 |
| Política ante violación | REPAIR (omitir la escritura) |
| Cobertura | **Cubierto** por `TestVolcadoPeriodicoNoOcurreEnCadaTick` (`internal/game/simulation`); `TestFlushPositionsEscribeElLoteYMantieneElChunk` y `TestFlushVacioNoHaceNada` (integration) están **diseñados y no ejecutados** |

**Enunciado.** El flush periódico de posiciones (`EO_PERSISTENCE_FLUSH_INTERVAL_TICKS = 50`, es decir cada 5 s a 10 Hz) nunca escribe en `units` una posición para una unidad que tenga un movimiento `ACTIVE`.

**Razón.** Es una consecuencia directa de la clasificación del canon §12: la posición durante un movimiento activo es **reconstruible**, no persistida. Escribirla desde el flush crea una segunda fuente para el mismo dato, y las dos fuentes se contradicen inevitablemente porque el flush corre cada 5 s mientras la posición cambia cada pocos cientos de milisegundos.

La contradicción se materializa en el arranque. La secuencia de recuperación carga los movimientos `ACTIVE` y deriva la posición desde la polilínea, pero si `units` contiene una posición escrita por el flush hace 4,9 s, hay dos respuestas a «¿dónde está esta unidad?». Y la posición del flush es siempre la peor de las dos: obsoleta por hasta un intervalo completo.

Hay un caso donde escribir la posición **sí** es correcto y no viola nada: cuando el movimiento termina. Al completar o cancelar, la posición deja de ser derivable y se persiste en la misma transacción de la transición ([INV-MOVE-008](movement.md#inv-move-008)). Ese es un write-through inmediato, no un flush.

**Cómo se garantiza.**

- `DOMAIN` — la marca de *dirty* para posición se pone únicamente al **finalizar** un movimiento o al mutar la posición fuera de un movimiento. Una unidad en tránsito no se marca como dirty por su desplazamiento.
- `DOMAIN` — el flush filtra explícitamente: excluye toda unidad con movimiento `ACTIVE`, consultando el índice en memoria de movimientos activos, que es la misma estructura que usa el loop. No es una consulta separada que pueda desincronizarse.
- `DOMAIN` — el volcado escribe posición y chunk en el **mismo lote y la misma sentencia** ([INV-UNIT-011](units.md#inv-unit-011)), de modo que no puede dejar una unidad con la posición nueva y el chunk viejo.
- `DOMAIN` — `hp` sí se persiste por flush con normalidad: no está relacionado con el movimiento y no tiene una segunda fuente derivable. En MVP, además, `hp` es constante.
- `DOMAIN` — la persistencia se encola en la fase 8 del tick (`enqueue persistence`) y la ejecutan workers; el tick nunca hace I/O bloqueante (canon §6). Eso significa que entre el encolado y la escritura pasa tiempo, y ése es el intervalo que una guarda en SQL contra la existencia de un movimiento activo debería cubrir. **Esa guarda es trabajo pendiente**: hoy la exclusión la da únicamente el filtro en memoria, que es correcto pero no defensivo.
- `DOMAIN` — un lote vacío no genera ninguna sentencia: el volcado no toca la base de datos si no hay nada sucio.

**Cómo se verifica.**

- `TestVolcadoPeriodicoNoOcurreEnCadaTick` (simulation, `internal/game/simulation`) — **existe y pasa**: el volcado respeta su calendario de 50 ticks y no se dispara en cada tick.
- `TestFlushPositionsEscribeElLoteYMantieneElChunk` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: el lote escribe posición y chunk coherentes.
- `TestFlushVacioNoHaceNada` (integration, `internal/persistence/postgres`) — **diseñado, aún no ejecutado**: sin unidades sucias no se emite sentencia.
- Test previsto: `Test_INV_PERSIST_004_FlushSkipsUnitsWithActiveMovement` (integration) — con `FakeClock`, iniciar un movimiento largo, avanzar más de 50 ticks y comprobar que la posición en `units` no ha cambiado durante el trayecto.
- Test previsto: `Test_INV_PERSIST_004_SqlGuardBlocksLateWrite` (integration) — **para cuando exista la guarda**: se encola una escritura de posición y se activa un movimiento antes de que el worker la ejecute.

**Violación en runtime.** Detección en la guarda del `UPDATE` (cero filas afectadas cuando se esperaba una) y en la reconciliación de arranque, si la posición de `units` no coincide con el waypoint derivado. Log `invariant_violation` con `inv_id=INV-PERSIST-004`, `unitId` y ambas posiciones. Política `REPAIR`: la escritura se omite y prevalece la derivación desde `unit_movements`, que es la fuente autoritativa mientras el movimiento esté activo.

---

<a id="inv-persist-005"></a>
## INV-PERSIST-005 — Migraciones ordenadas, idempotentes e inmutables

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M0 |
| Política ante violación | FAIL_FAST en arranque |
| Cobertura | **Sin cobertura ejecutada**: el migrador es `golang-migrate` embebido y sólo se ejercita en integración, aún no ejecutada |

**Enunciado.** Las migraciones de `services/game-server/migrations/` se aplican en orden estrictamente creciente de su prefijo numérico, cada una se aplica **como máximo una vez** (registrada en `schema_migrations`), y una migración ya publicada —fusionada a la rama principal— **nunca se edita**: los cambios se hacen con una migración nueva.

**Razón.** Cada una de las tres propiedades cubre un fallo distinto:

- **Orden** — una migración que añade una columna a una tabla creada por otra posterior falla. El orden numérico es lo que hace el esquema construible desde cero de forma repetible.
- **Idempotencia en la aplicación** — sin registro de lo ya aplicado, cada arranque reejecuta todo el historial. Un `CREATE TABLE` falla y aborta el arranque; peor, un `INSERT` de datos semilla duplica filas de `eras` o `factions`, lo que rompe [INV-CITY-003](city.md#inv-city-003) y [INV-PLAYER-002](player.md#inv-player-002).
- **Inmutabilidad tras publicación** — es la propiedad más fácil de violar y la más difícil de detectar. Editar una migración ya aplicada en producción no tiene ningún efecto sobre esa base de datos (ya está registrada como aplicada) pero **sí** sobre las que se creen desde cero. El resultado son dos esquemas divergentes que se comportan igual en la mayoría de los casos y distinto en el que importa. El síntoma clásico es «funciona en producción y falla en el entorno nuevo», o al revés.

Precisión sobre «idempotente»: el requisito es que la **aplicación del conjunto** sea idempotente —ejecutar el migrador N veces deja el mismo esquema—, no que cada script sea reejecutable por sí solo. El mecanismo es el registro en `schema_migrations`, no llenar los scripts de `IF NOT EXISTS`, que enmascararían divergencias reales.

**Cómo se garantiza.**

- `DB` — nomenclatura fija: `NNNNNN_nombre.up.sql` y `NNNNNN_nombre.down.sql`, con prefijo correlativo, sin huecos y sin duplicados. Las dos migraciones existentes son `000001_initial_schema` y `000002_seed_catalogs`.
- `DB` — los ficheros se **embeben en el binario** con `//go:embed *.sql` (`migrations/embed.go`), de modo que el binario y su esquema viajan juntos: es imposible desplegar un servidor con un juego de migraciones distinto del que se compiló.
- `DB` — el migrador es **golang-migrate** (`internal/persistence/postgres/migrate.go`), con la URL reescrita al esquema `pgx5`. `schema_migrations` registra la **versión aplicada y un indicador `dirty`**; el migrador ordena por prefijo, salta las ya registradas y aplica el resto.
- `DOMAIN` — un esquema marcado como `dirty` —una migración que falló a medias— **aborta el arranque** con un error explícito que pide intervención manual. Continuar automáticamente sería adivinar qué quedó aplicado.
- `DOMAIN` — el `.down.sql` existe para todas las migraciones y se escribe a la vez que el `.up.sql`. Probar `up` → `down` → `up` en CI es trabajo pendiente.
- `DOMAIN` — el migrador lo ejecuta **el propio arranque del Game Server** (`postgres.Migrate` en `cmd/server/main.go`), antes de abrir el puerto. Es una decisión deliberada de operación en un despliegue de un solo proceso: el esquema tiene un único dueño, este servicio, y el frontend nunca migra la base de datos (ADR-012). Si el esquema ya está al día, el migrador registra «esquema ya al día» y sigue.
- Como `psql` no está instalado en el entorno de desarrollo, el acceso a PostgreSQL se hace vía `docker compose exec`, y los tests de integración requieren Docker Desktop iniciado. El task runner es **pnpm scripts**, no Makefile: `make` tampoco está instalado.

> **Sin verificación de checksum.** `golang-migrate` registra la versión y el estado `dirty`, **no** un checksum de cada fichero aplicado. La inmutabilidad de una migración publicada es hoy una **convención de revisión**, no una comprobación automática: editar un `.up.sql` ya aplicado no lo detecta nada, y el síntoma aparecerá en el primer entorno creado desde cero. Un chequeo de checksum sería el mecanismo que la haría verificable, y es **TBD (fuera de MVP)**; hasta entonces, esta ficha no promete lo que el migrador no hace.

**Cómo se verifica.**

- Test previsto: `Test_INV_PERSIST_005_MigrationsOrderedIdempotentImmutable` (integration) — aplica todas las migraciones desde cero, vuelve a ejecutar el migrador y comprueba que no aplica ninguna y que el esquema no cambia.
- Test previsto: `Test_INV_PERSIST_005_FilenamesAreSequential` (unit) — los prefijos son correlativos, sin huecos ni repetidos, y toda migración `.up.sql` tiene su `.down.sql`.
- Test previsto: `Test_INV_PERSIST_005_UpDownUpRoundTrip` (integration) — `up` → `down` → `up` deja el mismo esquema que el `up` inicial.
- Test previsto: `Test_INV_PERSIST_005_DirtySchemaAbortsStartup` (integration) — con `schema_migrations.dirty = true`, el arranque falla y pide intervención manual en lugar de continuar.

**Violación en runtime.** Detección en el migrador (estado `dirty`, orden, huecos) y en la verificación de esquema del arranque (`SchemaVersion`). Log `invariant_violation` con `inv_id=INV-PERSIST-005` y la versión implicada. Política `FAIL_FAST`: ni el migrador ni el servidor continúan. Un esquema en estado desconocido es la peor base posible para todos los demás invariantes de este catálogo, porque las constraints que los sostienen podrían no existir.

---

## Qué es autoritativo, qué se persiste y qué se reconstruye

Resumen consolidado que responde a las cuatro preguntas obligatorias del canon §12 para el MVP:

| Dato | Autoritativo en | Persistencia | Reconstruible |
|---|---|---|---|
| Jugador, civilization, faction | PostgreSQL | Write-through | No |
| Ciudad: owner, era, `population_limit` | PostgreSQL | Write-through | `population_limit` sí, desde `eras` |
| Ciudad: `presence_state` | RAM + PostgreSQL | Escritura encolada en cada transición | No (depende del historial de presencia) |
| Ciudad: `population` | PostgreSQL | Write-through | Sí, recontando `units` ([INV-CITY-009](city.md#inv-city-009)) |
| Presencia del jugador | RAM del loop (`disconnectedAt`, contador de sesiones) + Redis como observable | No durable | Sí, converge al reconectar |
| Unidad: owner, tipo | PostgreSQL | Write-through | No |
| Unidad: posición **sin** movimiento activo | RAM + PostgreSQL | Dirty-flag + flush cada 50 ticks | No |
| Unidad: posición **con** movimiento activo | Derivada de `unit_movements` | **No se persiste** | Sí, `PositionAt` |
| Unidad: `hp` | RAM + PostgreSQL | Dirty-flag + flush | No |
| Movimiento: polilínea, tiempos, estado | PostgreSQL | Write-through al iniciar y al terminar | No |
| Índice de ocupación | RAM | No | Sí, desde `units` + mundo |
| Conjuntos de interés y suscripciones | RAM | No | Sí, desde `session.view` |
| Terreno | PostgreSQL (`world_chunks`) | Generado y persistido una vez | Sí, desde `EO_WORLD_SEED` |
| Unidad: `chunk_x`, `chunk_y` | Derivadas de `(x, y)` | Se escriben con la posición, en la misma sentencia | Sí, con `World.ChunkOf` |
| Idempotencia de comandos | Redis (`idem:{playerId}:{requestId}`) | **Sólo Redis hoy**; `idempotency_keys` existe pero no se escribe | No |

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-PERSIST-001 | `internal/persistence/postgres`, `internal/websocket` | [INV-SEC-007](security.md#inv-sec-007) |
| INV-PERSIST-002 | `cmd/server`, `internal/game/loop` | [INV-MOVE-005](movement.md#inv-move-005), [INV-UNIT-005](units.md#inv-unit-005) |
| INV-PERSIST-003 | `internal/persistence/redis` | [INV-CITY-005](city.md#inv-city-005), [INV-SEC-007](security.md#inv-sec-007) |
| INV-PERSIST-004 | `internal/persistence/postgres`, `internal/domain/movement` | [INV-MOVE-006](movement.md#inv-move-006) |
| INV-PERSIST-005 | `migrations/`, `internal/persistence/postgres` | [../database/schema.md](../database/schema.md), [../decisions/ADR-012-database-migrations.md](../decisions/ADR-012-database-migrations.md) |
