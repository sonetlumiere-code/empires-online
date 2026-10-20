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

El tick **nunca** ejecuta I/O bloqueante contra PostgreSQL (canon §6): la persistencia se encola (fase 8, `enqueue persistence`) y la ejecutan cuatro workers fuera del tick, uno por partición, con hasta **3 intentos** y backoff. La profundidad de la cola es `eo_persistence_queue_depth`.

> **Cómo se ordena y se compensa** ([ADR-013](../decisions/ADR-013-persistence-pipeline.md)). Cada trabajo lleva la clave de su entidad y va al worker de esa clave, así que las escrituras de una misma unidad o ciudad se ejecutan en el orden en que se encolaron. El id de un movimiento lo asigna el loop al aceptar la orden. Si un trabajo agota sus intentos, su `OnPermanentFailure` —que corre en el worker y no toca el mundo— devuelve un comando al loop por `Deps.Inbox`. Hasta `EO-120` nada de esto era así: el id llegaba a los clientes como `0`, el cierre de un movimiento podía adelantar a su inicio y ninguna compensación hacía nada.

> **Ventana de riesgo, dicha en voz alta.** Que la escritura sea diferida tiene un coste que esta documentación no oculta: si el proceso muere entre la aceptación de un comando y su `COMMIT` —típicamente unas decenas de milisegundos—, ese efecto se pierde y la entidad queda en su último estado consolidado. Para las posiciones, la ventana es el intervalo de volcado: hasta 5 s. Es un **RPO documentado**, no un descuido, y es la contrapartida de que el tick no bloquee. Lo que el diseño sí garantiza es que cada trabajo de la cola es una unidad atómica. En un apagado **ordenado** la ventana se cierra: el loop vuelca lo sucio al parar y la cola se vacía antes de salir.

---

<a id="inv-persist-001"></a>
## INV-PERSIST-001 — Lo confirmado sobrevive a la desconexión

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M3 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto** por `TestElMovimientoContinuaConElJugadorDesconectado` y `TestAlPararElLoopVuelcaLoQueQuedabaSucio` (`internal/game/simulation`), `TestCancelarElProcesoNoCancelaUnaEscrituraEnVuelo` y `TestCloseEsperaAlTrabajoEnVueloYALosPendientes` (`internal/persistence`), y `TestMovimientoSobreviveAlCicloDePersistencia` (integration) |

**Enunciado.** Una transacción durable ya confirmada (`COMMIT` en PostgreSQL) permanece confirmada con independencia de lo que ocurra con la conexión WebSocket que la originó: cierre limpio, corte abrupto, timeout de lectura o cierre con cualquiera de los códigos `4400`, `4401`, `4403`, `4408`, `4429`, `4500`.

**Razón.** Es la traducción directa del principio de mundo persistente (canon §1.2): *ningún estado durable depende de un WebSocket vivo*. Si un `COMMIT` pudiera deshacerse al caerse el socket, el juego perdería su propiedad fundamental —el mundo evoluciona sin jugadores conectados— y aparecerían pérdidas de progreso arbitrarias en cada corte de red del jugador, que en un MMO ocurren constantemente.

El antipatrón que este invariante prohíbe es concreto y tentador: acoplar el ciclo de vida de la transacción al `context.Context` de la conexión. Con ese diseño, cancelar el contexto al cerrarse el socket aborta transacciones en vuelo y —según cómo esté escrito el `defer`— puede llegar a ejecutar un `ROLLBACK` sobre trabajo ya efectuado.

**Cómo se garantiza.**

- `DOMAIN` — el contexto de una transacción durable **no** desciende del contexto de la conexión. Los trabajos de la cola de persistencia se ejecutan con el contexto del proceso y su propio timeout: el cierre del socket no cancela una escritura en vuelo. Éste es el punto exacto del invariante, y es lo que hace que un jugador que se desconecta a mitad de una orden no la pierda.
- `DOMAIN` — el orden **no** es «`COMMIT` y después notificar»: el efecto se aplica en RAM, se notifica al cliente y la escritura se encola. Lo que el invariante garantiza es lo que dice su enunciado —que un `COMMIT` ya hecho no se deshace por lo que le pase al socket—, no que la notificación llegue después del `COMMIT`. La ventana entre notificación y `COMMIT` es el RPO documentado en el contexto de este documento.
- `DOMAIN` — la simulación **continúa** con el jugador desconectado: un movimiento en curso no se cancela porque el socket se caiga. Es la manifestación directa del principio de mundo persistente.
- `DOMAIN` — el apagado ordenado sigue este orden en `cmd/server`: deja de aceptar tráfico, cierra las sesiones, **espera a que el game loop pare** —que al parar encola un volcado final de lo sucio—, `Queue.Drain(15s)` espera a que el canal se vacíe y `Queue.Close` espera además a los trabajos que un worker ya estaba ejecutando. Antes de `EO-118` no se esperaba al loop: el volcado final no existía, se perdían hasta 5 s de posiciones en cada apagado, y un `Submit` tardío sobre la cola cerrada habría sido un `panic`.
- `DOMAIN` — cada trabajo se reintenta hasta **3 veces** con backoff; si se agotan, se llama a `OnPermanentFailure`. Hoy ese callback **no compensa nada en el mundo**: el del inicio de movimiento sólo registra el error, y la unidad sigue moviéndose en RAM sin fila en `unit_movements`. Compensar exige devolver un comando al loop, porque el callback corre en la goroutine del worker (`EO-120`).
- `DB` — PostgreSQL con durabilidad por defecto (`synchronous_commit` activo). Desactivarlo requeriría ADR y sería una violación directa de este invariante.

**Cómo se verifica.**

- `TestElMovimientoContinuaConElJugadorDesconectado` (simulation) — el movimiento sigue avanzando y completándose aunque el jugador pierda la sesión.
- `TestCancelarElProcesoNoCancelaUnaEscrituraEnVuelo` (unit, `internal/persistence`) — cancelar el contexto del proceso con una escritura en marcha no cancela el contexto de esa escritura.
- `TestCloseEsperaAlTrabajoEnVueloYALosPendientes` (unit) — `Close` no vuelve hasta terminar el trabajo en curso y los pendientes; `Drain` solo no basta.
- `TestUnFalloTransitorioSeReintenta`, `TestAgotadosLosIntentosSeCompensaUnaVez` y `TestConLaColaLlenaSubmitNoBloqueaYCompensa` (unit) — la política de reintentos y de saturación.
- `TestAlPararElLoopVuelcaLoQueQuedabaSucio` (simulation) — al parar, el loop vuelca la unidad que llegó entre dos volcados. Verificado por mutación.
- `TestMovimientoSobreviveAlCicloDePersistencia` (integration) — el movimiento escrito se recupera intacto.

**Violación en runtime.** No hay detección en runtime: una escritura confirmada que desaparece no deja rastro donde se perdió. Lo que sí se registra es cada escritura descartada, por saturación de la cola o por agotar los reintentos, con el nombre del trabajo y el error. Ésa es la señal de que algo durable no llegó a disco.

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

- `DOMAIN` — secuencia de arranque de `cmd/server`: migrar si `EO_MIGRATE_ON_START` → cargar el mundo, regenerando el terreno desde la semilla y comparando los parámetros persistidos ([INV-WORLD-005](world.md#inv-world-005)) → cargar ciudades, unidades vivas y movimientos `ACTIVE` → `Hydrate` aplica la regla de arriba y **reconcilia** toda unidad que llegue `MOVING` sin un movimiento que la respalde ([INV-UNIT-005](units.md#inv-unit-005)) → cerrar en la base los movimientos que terminaron durante la caída → reconstruir las murallas, los territorios y las safe zones → abrir el puerto de `EO_HTTP_ADDR`. El tick se retoma del último guardado, que sólo se escribe en el apagado ordenado; su continuidad tras una caída es asunto de [INV-WORLD-004](world.md#inv-world-004).
- `DOMAIN` — el orden importa: **no se aceptan conexiones hasta que la reconstrucción termina**, porque el servidor HTTP —que sirve también el WebSocket— sólo se arranca después. `GET /ready` comprueba PostgreSQL, Redis y que el game loop siga latiendo; `GET /health` responde mientras el proceso viva.
- `DOMAIN` — solo se cargan movimientos `ACTIVE`; los terminales no se reabren ([INV-MOVE-007](movement.md#inv-move-007)).
- `DOMAIN` — nada del estado en RAM se considera fuente: los índices se reconstruyen desde PostgreSQL, nunca se restauran desde un volcado propio.
- `DOMAIN` — el `Clock` inyectado permite ejercitar el arranque con tiempos arbitrarios en tests de recovery.

**Cómo se verifica.**

- `TestRecuperacionCompletaTrasReinicio` (integration, `internal/persistence/postgres`) — un movimiento escrito en PostgreSQL se reconstruye a mitad de trayecto en el tile correcto, y vencido durante la caída aparece en su destino y se cierra en la base.
- `TestRecuperacionReconciliaUnaUnidadMovingSinMovimiento` (recovery, `internal/game/simulation`) — una unidad `MOVING` sin movimiento que la respalde queda `IDLE` donde estaba y se vuelca; una que sí lo tiene se reanuda.
- `TestRecuperacionMovimientoEnCurso` (recovery) — **existe y pasa**: movimiento iniciado, reinicio a mitad de trayecto, la unidad continúa hacia el mismo destino con el mismo `arrival_time_ms`. Es el test de recovery de referencia del canon §19 («el movimiento sobrevive»).
- `TestRecuperacionMovimientoVencidoDuranteLaCaida` (recovery) — **existe y pasa**: reinicio después de `arrival_time_ms`: el movimiento se cierra como `COMPLETED`, la unidad aparece en el destino.
- `TestRecuperacionConPolilineaInvalida` (recovery) — **existe y pasa**: una polilínea corrupta cierra el movimiento como `FAILED` y deja la unidad donde estaba.
- `TestBootstrapFallidoNoDejaNadaAMedias` (integration) — una transacción a medias no deja rastro.
- `TestRehidratarDosVecesNoEscribePresencia` y `TestUnReinicioNoReiniciaElCooldownDeProteccion` (integration) — la rehidratación no altera la presencia ni reinicia el cooldown.

**Violación en runtime.** Un error al cargar el mundo, los territorios o las safe zones inválidas aborta el arranque antes de abrir el puerto (`FAIL_FAST`). La reconciliación de `MOVING` sin movimiento emite `invariant_violation` con `inv_id=INV-UNIT-005` y repara. Una caída deja además las posiciones del último intervalo de volcado sin escribir, que es el RPO documentado en el contexto. Un servidor que arranca con estado inconsistente empieza inmediatamente a persistir consecuencias de ese estado, y a partir de ahí el daño es irreversible.

---

<a id="inv-persist-003"></a>
## INV-PERSIST-003 — Ningún dato durable existe solo en Redis

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M3 |
| Política ante violación | FAIL_FAST |
| Cobertura | **Cubierto** por `TestNingunaClaveDelServidorQuedaSinCaducidad`, `TestUnAlmacenSinTTLNoSeConstruye`, `TestPresenciaSeRegistraYExpira` y `TestLatidoRenuevaLaPresencia` (integration, `internal/persistence/redis`). La deduplicación de comandos no es estado durable ([ADR-014](../decisions/ADR-014-command-idempotency.md)) |

**Enunciado.** Todo dato clasificado como durable tiene su representación autoritativa en PostgreSQL. Redis contiene exclusivamente estado *hot/transient*: presencia, sesiones, locks, cooldowns, caché e idempotencia de corto plazo; vaciar Redis por completo no debe producir ninguna pérdida de estado durable.

**Razón.** Redis es un almacén con TTL y sin garantía de durabilidad en el uso que le da este proyecto. Todas sus claves del MVP llevan expiración por diseño:

| Clave | TTL | Naturaleza |
|---|---|---|
| `presence:player:{playerId}` | `EO_PRESENCE_TTL_SECONDS` = 30 s | Presencia observable; **no** es lo que dispara la transición de la ciudad |
| `ticket:jti:{jti}` | 120 s | Anti-replay de tickets; sólo necesita cubrir el TTL de 60 s del ticket |
| `idem:{playerId}:{requestId}` | 300 s | Idempotencia de corto plazo |

> **Precisión sobre la presencia.** La señal que degrada una ciudad a `OFFLINE_PENDING` **no** es la expiración de esta clave: la decide el game loop comparando su marca `disconnectedAt` en RAM contra `DisconnectGrace` (= `EO_PRESENCE_TTL_SECONDS`). La clave de Redis existe para observadores externos y para el futuro multiproceso ([INV-CITY-005](city.md#inv-city-005)). Eso refuerza este invariante en vez de debilitarlo: la decisión de presencia no depende de un almacén sin garantía de durabilidad.

Si un dato durable viviera solo aquí, se perdería por expiración, por evicción bajo presión de memoria o por un reinicio de Redis, y lo haría **en silencio**: no hay error, simplemente el dato deja de estar. La prueba de fuego del invariante es directa: `FLUSHALL` no debe costar nada más que reconexiones y un recálculo de presencia.

El caso límite que merece nombrarse es la idempotencia. `idem:{playerId}:{requestId}` vive sólo en Redis, y un `FLUSHALL` borra la deduplicación de los últimos 300 s. [ADR-014](../decisions/ADR-014-command-idempotency.md) decidió que **no es estado durable**: es una protección de corto plazo contra reintentos, y con los comandos actuales —mover y cancelar— una reejecución deja el mundo igual. El diseño anterior preveía un respaldo en la tabla `idempotency_keys`, que nunca se escribió y se eliminó en `000004`. Un comando futuro cuyo efecto durable no se pueda repetir sin daño tendrá que ser idempotente por construcción: su `requestId` irá en la fila de su efecto, con `UNIQUE`, y entonces sí será estado durable en PostgreSQL ([INV-SEC-007](security.md#inv-sec-007)).

**Cómo se garantiza.**

- `DOMAIN` — la clasificación de cada dato es explícita en el diseño (tabla del contexto de este documento) y se revisa en cada PR que añada una clave de Redis: la pregunta obligatoria es «¿qué se pierde si esta clave desaparece?». Si la respuesta incluye algo durable, el diseño es incorrecto.
- `DOMAIN` — los repositorios de Redis viven en `internal/persistence/redis` y todas sus escrituras llevan TTL: `SET` y `SETNX` con expiración y `EXPIRE` en el latido. Sus constructores **abortan** si reciben un TTL no positivo, porque en go-redis una expiración de 0 significa «para siempre». `Client.Raw()` expone el cliente sin restricciones, y sólo lo usan los tests.
- `DOMAIN` — la clasificación es lo que se revisa, no la implementación de cada clave: la pregunta obligatoria en revisión de PR es «¿qué se pierde si esta clave desaparece?». Para la idempotencia, la respuesta hoy es «la protección contra un segundo efecto», y por eso está anotada arriba como excepción abierta en lugar de darse por resuelta.
- `DOMAIN` — la presencia derivada es **reconstruible** (canon §12): si Redis se vacía, todos los jugadores aparecen sin clave de presencia, lo que lleva sus ciudades a `OFFLINE_PENDING` por la vía normal ([INV-CITY-005](city.md#inv-city-005)). Los conectados renuevan su clave en el siguiente heartbeat, a los `EO_PRESENCE_HEARTBEAT_SECONDS` = 10 s como mucho. El sistema converge sin intervención.

**Cómo se verifica.**

- `TestNingunaClaveDelServidorQuedaSinCaducidad` (integration) — tras ejercitar todas las escrituras de los tres almacenes, ninguna clave de la base tiene TTL `-1`.
- `TestUnAlmacenSinTTLNoSeConstruye` (integration) — un TTL de 0 o negativo aborta la construcción.
- `TestPresenciaSeRegistraYExpira`, `TestLatidoRenuevaLaPresencia`, `TestLatidoSobreClaveExpiradaDevuelveFalse` y `TestSoloLaSesionPropietariaRetiraLaPresencia` (integration) — la presencia nace con TTL, el latido la renueva y una sesión no borra la de otra.
- Que vaciar Redis no pierde estado de juego es estructural: la rehidratación del arranque sólo lee PostgreSQL, y los tests de recuperación (`arrancar` en la suite de integración) arrancan sin Redis.

**Violación en runtime.** Un almacén sin TTL no llega a construirse: el arranque aborta (`FAIL_FAST`). Perder la deduplicación con un `FLUSHALL` no viola el invariante: no es estado durable.

---

<a id="inv-persist-004"></a>
## INV-PERSIST-004 — El flush no contradice un movimiento activo

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M4 |
| Política ante violación | REPAIR (omitir la escritura) |
| Cobertura | **Cubierto** por `TestUnaUnidadEnMarchaNoSeVuelca` y `TestElCierreDelMovimientoLlevaLaPosicionFinal` (`internal/game/simulation`) y `TestConLaColaRealElMovimientoLlevaSuIdYCierraConSuPosicion` (integration) |

> **Decisión.** `Q-18` se resolvió a favor de esta ficha con [ADR-013](../decisions/ADR-013-persistence-pipeline.md): la posición en tránsito no se persiste, y [SPEC-MOVEMENT](../specs/movement.md) se corrigió en consecuencia. Hasta entonces la spec prescribía volcarla y el código lo hacía.

**Enunciado.** El flush periódico de posiciones (`EO_PERSISTENCE_FLUSH_INTERVAL_TICKS = 50`, es decir cada 5 s a 10 Hz) nunca escribe en `units` una posición para una unidad que tenga un movimiento `ACTIVE`.

**Razón.** Es una consecuencia directa de la clasificación del canon §12: la posición durante un movimiento activo es **reconstruible**, no persistida. Escribirla desde el flush crea una segunda fuente para el mismo dato, y las dos fuentes se contradicen inevitablemente porque el flush corre cada 5 s mientras la posición cambia cada pocos cientos de milisegundos.

La contradicción se materializa en el arranque. La secuencia de recuperación carga los movimientos `ACTIVE` y deriva la posición desde la polilínea, pero si `units` contiene una posición escrita por el flush hace 4,9 s, hay dos respuestas a «¿dónde está esta unidad?». Y la posición del flush es siempre la peor de las dos: obsoleta por hasta un intervalo completo.

Hay un caso donde escribir la posición **sí** es correcto y no viola nada: cuando el movimiento termina. Al completar o cancelar, la posición deja de ser derivable y se persiste en la misma transacción de la transición ([INV-MOVE-008](movement.md#inv-move-008)). Ese es un write-through inmediato, no un flush.

**Cómo se garantiza.**

- `DOMAIN` — `AdvanceMovements` mueve la unidad en RAM sin marcarla como sucia. La marca se pone al **finalizar** un movimiento o al cambiar la unidad fuera de un movimiento, por ejemplo al ocultarse.
- `DOMAIN` — `FlushDirty` filtra explícitamente: excluye toda unidad con movimiento `ACTIVE`, consultando el índice en memoria de movimientos activos, que es la misma estructura que usa el loop. No es una consulta separada que pueda desincronizarse.
- `DOMAIN` — el inicio y el cierre del movimiento escriben la unidad en su propia transacción: `MOVING` en el origen y `IDLE` en el tile final. Así la posición durable de una unidad parada nunca depende del volcado.
- `DOMAIN` — el volcado escribe posición y chunk en el **mismo lote y la misma sentencia** ([INV-UNIT-011](units.md#inv-unit-011)), de modo que no puede dejar una unidad con la posición nueva y el chunk viejo.
- `DOMAIN` — `hp` sí se persiste por flush con normalidad: no está relacionado con el movimiento y no tiene una segunda fuente derivable. En MVP, además, `hp` es constante.
- `DOMAIN` — entre el encolado y la escritura pasa tiempo, pero la cola ejecuta en orden las escrituras de una misma unidad (ADR-013 §2.2): un volcado encolado antes de un inicio de movimiento se escribe antes que él, y nunca lo pisa. No hay guarda en SQL contra la existencia de un movimiento activo; el orden por clave la hace innecesaria.
- `DOMAIN` — un lote vacío no genera ninguna sentencia: el volcado no toca la base de datos si no hay nada sucio.

**Cómo se verifica.**

- `TestUnaUnidadEnMarchaNoSeVuelca` (simulation) — dos volcados con una unidad en marcha no la escriben, y su inicio la deja `MOVING` en el origen.
- `TestElCierreDelMovimientoLlevaLaPosicionFinal` (simulation) — la llegada y la cancelación llevan el tile final al cierre durable.
- `TestConLaColaRealElMovimientoLlevaSuIdYCierraConSuPosicion` (integration) — con la cola real, la posición final está en `units` nada más cerrarse el movimiento, sin ningún volcado, y un reinicio deja la unidad donde llegó. Verificado por mutación.
- `TestElVolcadoSeParteEnUnTrabajoPorParticion` (simulation) y `TestLaColaOrdenaPorClaveYNoBloqueaOtrasClaves` (`internal/persistence`) — el orden por entidad.
- `TestVolcadoPeriodicoNoOcurreEnCadaTick` (simulation), `TestFlushPositionsEscribeElLoteYMantieneElChunk` y `TestFlushVacioNoHaceNada` (integration) — el calendario y el lote.

**Violación en runtime.** No hay detección en runtime: la exclusión es estructural, en `FlushDirty`. Si una posición en tránsito llegara a disco, no haría daño en el arranque, porque la recuperación deriva la posición de la polilínea mientras el movimiento esté `ACTIVE` y descarta la de `units`.

---

<a id="inv-persist-005"></a>
## INV-PERSIST-005 — Migraciones ordenadas, idempotentes e inmutables

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M0 |
| Política ante violación | FAIL_FAST en arranque |
| Cobertura | **Cubierto** por `TestLasMigracionesSonCorrelativasYCompletas` (`migrations`), `TestElMigradorEsIdempotente` y `TestUnEsquemaDirtyAbortaElMigrador` (integration, `internal/persistence/postgres`). La inmutabilidad tras publicar sigue siendo una convención de revisión |

**Enunciado.** Las migraciones de `services/game-server/migrations/` se aplican en orden estrictamente creciente de su prefijo numérico, cada una se aplica **como máximo una vez** (registrada en `schema_migrations`), y una migración ya publicada —fusionada a la rama principal— **nunca se edita**: los cambios se hacen con una migración nueva.

**Razón.** Cada una de las tres propiedades cubre un fallo distinto:

- **Orden** — una migración que añade una columna a una tabla creada por otra posterior falla. El orden numérico es lo que hace el esquema construible desde cero de forma repetible.
- **Idempotencia en la aplicación** — sin registro de lo ya aplicado, cada arranque reejecuta todo el historial. Un `CREATE TABLE` falla y aborta el arranque; peor, un `INSERT` de datos semilla duplica filas de `eras` o `factions`, lo que rompe [INV-CITY-003](city.md#inv-city-003) y [INV-PLAYER-002](player.md#inv-player-002).
- **Inmutabilidad tras publicación** — es la propiedad más fácil de violar y la más difícil de detectar. Editar una migración ya aplicada en producción no tiene ningún efecto sobre esa base de datos (ya está registrada como aplicada) pero **sí** sobre las que se creen desde cero. El resultado son dos esquemas divergentes que se comportan igual en la mayoría de los casos y distinto en el que importa. El síntoma clásico es «funciona en producción y falla en el entorno nuevo», o al revés.

Precisión sobre «idempotente»: el requisito es que la **aplicación del conjunto** sea idempotente —ejecutar el migrador N veces deja el mismo esquema—, no que cada script sea reejecutable por sí solo. El mecanismo es el registro en `schema_migrations`, no llenar los scripts de `IF NOT EXISTS`, que enmascararían divergencias reales.

**Cómo se garantiza.**

- `DB` — nomenclatura fija: `NNNNNN_nombre.up.sql` y `NNNNNN_nombre.down.sql`, con prefijo correlativo, sin huecos y sin duplicados. Hoy hay tres: `000001_initial_schema`, `000002_seed_catalogs` y `000003_territory_control_version`.
- `DB` — los ficheros se **embeben en el binario** con `//go:embed *.sql` (`migrations/embed.go`), de modo que el binario y su esquema viajan juntos: es imposible desplegar un servidor con un juego de migraciones distinto del que se compiló.
- `DB` — el migrador es **golang-migrate** (`internal/persistence/postgres/migrate.go`), con la URL reescrita al esquema `pgx5`. `schema_migrations` registra la **versión aplicada y un indicador `dirty`**; el migrador ordena por prefijo, salta las ya registradas y aplica el resto.
- `DOMAIN` — un esquema marcado como `dirty` —una migración que falló a medias— **aborta el arranque** con un error explícito que pide intervención manual. Continuar automáticamente sería adivinar qué quedó aplicado.
- `DOMAIN` — el `.down.sql` existe para todas las migraciones y se escribe a la vez que el `.up.sql`. Probar `up` → `down` → `up` en CI es trabajo pendiente.
- `DOMAIN` — el arranque del Game Server ejecuta el migrador (`postgres.Migrate` en `cmd/server/main.go`) antes de abrir el puerto **sólo si** `EO_MIGRATE_ON_START` es verdadero, que es el valor por defecto fuera de producción. En producción se desactiva por defecto y las migraciones las aplica `cmd/migrate` como paso previo del despliegue: con varias instancias arrancando a la vez, todas intentarían migrar el mismo esquema. El esquema tiene un único dueño, este servicio, y el frontend nunca migra la base de datos (ADR-012). Si el esquema ya está al día, el migrador registra «esquema ya al día» y sigue.
- Para desarrollo y tests no hace falta Docker: el clúster propio de [../operations/local-development.md](../operations/local-development.md) §3-bis da PostgreSQL en el puerto 5433. El task runner es **pnpm scripts**, no Makefile.

> **Sin verificación de checksum.** `golang-migrate` registra la versión y el estado `dirty`, **no** un checksum de cada fichero aplicado. La inmutabilidad de una migración publicada es hoy una **convención de revisión**, no una comprobación automática: editar un `.up.sql` ya aplicado no lo detecta nada, y el síntoma aparecerá en el primer entorno creado desde cero. Un chequeo de checksum sería el mecanismo que la haría verificable, y es **TBD (fuera de MVP)**; hasta entonces, esta ficha no promete lo que el migrador no hace.

**Cómo se verifica.**

- `TestLasMigracionesSonCorrelativasYCompletas` (unit, `migrations`) — sobre el FS embebido, que es lo que viaja en el binario: formato del nombre, prefijos correlativos desde `000001` sin huecos ni repetidos, y un `.down.sql` para cada `.up.sql`.
- `TestElMigradorEsIdempotente` (integration) — volver a ejecutar el migrador no cambia la versión, que es la de la última migración embebida, ni la huella de columnas del esquema.
- `TestUnEsquemaDirtyAbortaElMigrador` (integration) — con `schema_migrations.dirty = true`, el migrador falla pidiendo intervención manual.
- Pendiente: el ida y vuelta `up` → `down` → `up`.

**Violación en runtime.** Un esquema `dirty` hace fallar `postgres.Migrate` con un error que pide intervención manual, y el arranque no continúa (`FAIL_FAST`). Una migración publicada que se edita no se detecta: no hay checksum. Un esquema en estado desconocido es la peor base posible para todos los demás invariantes de este catálogo, porque las constraints que los sostienen podrían no existir.

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
| Unidad: posición **sin** movimiento activo | RAM + PostgreSQL | Write-through al cerrar el movimiento que la dejó ahí; dirty-flag + flush cada 50 ticks para el resto | No |
| Unidad: posición **con** movimiento activo | Derivada de `unit_movements` | **No se persiste** | Sí, `PositionAt` |
| Unidad: `hp` | RAM + PostgreSQL | Dirty-flag + flush | No |
| Movimiento: polilínea, tiempos, estado | PostgreSQL | Write-through al iniciar y al terminar | No |
| Índice de ocupación | RAM | No | Sí, desde `units` + mundo |
| Conjuntos de interés y suscripciones | RAM | No | Sí, desde `session.view` |
| Terreno | PostgreSQL (`world_chunks`) | Generado y persistido una vez | Sí, desde `EO_WORLD_SEED` |
| Unidad: `chunk_x`, `chunk_y` | Derivadas de `(x, y)` | Se escriben con la posición, en la misma sentencia | Sí, con `World.ChunkOf` |
| Idempotencia de comandos | Redis (`idem:{playerId}:{requestId}`) | Sólo Redis, 300 s ([ADR-014](../decisions/ADR-014-command-idempotency.md)) | No |
| Unidad: `status` | RAM + PostgreSQL | Write-through en el inicio y el cierre de cada movimiento; dirty-flag + flush para el resto (por ejemplo `HIDDEN`) | Sí al arrancar: `MOVING` sin movimiento se reconcilia a `IDLE` |

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-PERSIST-001 | `internal/persistence/postgres`, `internal/websocket` | [INV-SEC-007](security.md#inv-sec-007) |
| INV-PERSIST-002 | `cmd/server`, `internal/game/loop` | [INV-MOVE-005](movement.md#inv-move-005), [INV-UNIT-005](units.md#inv-unit-005) |
| INV-PERSIST-003 | `internal/persistence/redis` | [INV-CITY-005](city.md#inv-city-005), [INV-SEC-007](security.md#inv-sec-007) |
| INV-PERSIST-004 | `internal/persistence/postgres`, `internal/domain/movement` | [INV-MOVE-006](movement.md#inv-move-006) |
| INV-PERSIST-005 | `migrations/`, `internal/persistence/postgres` | [../database/schema.md](../database/schema.md), [../decisions/ADR-012-database-migrations.md](../decisions/ADR-012-database-migrations.md) |
