# ADR-022: Ninguna escritura durable ni comando del sistema se pierde en silencio: el volcado reencola lo que la cola descarta y el loop tiene buzón propio

Propósito: cerrar el hallazgo de la auditoría del 2026-10-26 sobre la cola de persistencia —cuatro de sus
siete trabajos se perdían sin rastro, y las compensaciones y las bajas de sesión competían con los comandos de
los jugadores por el sitio en el canal del loop— con un patrón único de compensación, un canal con prioridad y
dos contadores cuyo valor sano es cero.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-10-27** |
| Ámbito | `internal/game/simulation`, `internal/game/loop`, `internal/persistence`, `internal/persistence/postgres` (`CityRepo.SetPresence`), `internal/websocket`, `internal/observability`, `cmd/server` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-013](ADR-013-persistence-pipeline.md) (precisa su §2.4: el buzón y qué pasa cuando está lleno), [ADR-017](ADR-017-garrison-commands.md) (sus compensaciones usan el mismo buzón), [ADR-021](ADR-021-game-server-perimeter.md) (el patrón de series a cero desde el arranque) |

---

## Contexto

El tick nunca hace I/O contra PostgreSQL: toda escritura durable se aplica en RAM y se encola como un
`simulation.Job` que ejecutan cuatro workers, uno por partición de clave, con tres intentos y una espera de
200 ms × intento ([ADR-013](ADR-013-persistence-pipeline.md)). `Queue.Submit` nunca bloquea: si la partición
está llena, descarta el trabajo en el acto, en la goroutine del loop. Agotados los intentos, el worker lo
descarta también. En ambos casos la cola llama a `OnPermanentFailure` **si el trabajo lo declara**, y ese
callback corre fuera del loop, así que para compensar devuelve un comando por `Deps.Inbox`.

La auditoría independiente del 2026-10-26 encontró lo siguiente:

- **Sólo `movement.start`, `garrison.enter` y `garrison.leave` declaraban compensación.** `movement.cancel`,
  `movement.complete`, `city.presence` y `units.flush` se perdían con un `error` en el log y nada más. Las
  consecuencias no son simétricas: un `movement.complete` perdido deja la fila `ACTIVE` y la unidad `MOVING`
  en la base hasta el reinicio, donde `Hydrate` la completa; un `movement.cancel` perdido hace lo mismo, pero
  al rehidratar la polilínea manda sobre la fila de la unidad y **la cancelación se deshace**: la unidad
  reanuda o aparece en el destino que el jugador había cancelado; un `city.presence` perdido deja la ciudad
  con una presencia obsoleta que ningún reinicio repara —una fila `ONLINE` con el dueño ausente se queda
  `ONLINE` indefinidamente ([../specs/presence.md](../specs/presence.md) §7.6), es decir, atacable o
  inatacable al revés de lo que el mundo ve—; y un `units.flush` perdido deja posiciones y estados (`HIDDEN`,
  por ejemplo) sin escribir hasta que la unidad vuelva a cambiar, porque `FlushDirty` sacaba las unidades del
  conjunto sucio **antes** de saber si se habían escrito. Con la economía futura, la misma cola transportará
  stocks: un lote perdido sería oro que desaparece sin rastro.
- **`Deps.Inbox` era el canal de comandos de los jugadores.** `reinject` hacía un envío no bloqueante a
  `commands` (8192) y, si estaba lleno, registraba y perdía la compensación. El canal lo llenan los jugadores a
  20 mensajes por segundo y conexión; una avalancha de órdenes podía por tanto dejar RAM y base de datos sin
  reconciliar hasta el siguiente arranque, justo cuando el servidor va más cargado.
- **`PlayerConnected` y `PlayerDisconnected` se descartaban igual.** El servidor WebSocket los enviaba al
  mismo canal con el mismo envío no bloqueante. La simulación llevaba las sesiones con un **contador** por
  jugador; una baja perdida lo dejaba en 1 para siempre y la ciudad no salía nunca de `ONLINE`, ni siquiera
  cuando el dueño volvía y se iba. El latido de presencia de Redis (TTL 30 s) no lo cubre: el paso a
  `OFFLINE_PENDING` lo decide el loop en RAM sin consultar Redis (`RN-PRESENCE-004`), a propósito.
- **No había métrica.** Los descartes sólo existían como líneas de log; una alerta exige una serie.

Restricciones que cualquier solución tiene que respetar: el tick no hace I/O ni bloquea en canales (canon §6,
[../architecture/game-loop.md](../architecture/game-loop.md) §1); `OnPermanentFailure` puede correr en la
goroutine del worker **o en la del loop** (descarte al encolar), así que no puede bloquear en ninguna de las
dos ni mutar el mundo; el alta de una sesión debe aplicarse **antes** que su snapshot inicial
(`INV-CITY-014`), que viaja por el canal de comandos; y la cardinalidad de toda etiqueta de métrica está
acotada ([ADR-021](ADR-021-game-server-perimeter.md), `EO-121`).

## Decisión

**Todo trabajo durable declara compensación salvo que un hecho periódico lo vuelva a producir; la compensación
no reescribe nada por su cuenta, sino que devuelve lo descartado al volcado periódico; el loop tiene un buzón
propio, con prioridad, para los comandos que el servidor se devuelve a sí mismo; y cada descarte se cuenta.**

1. **Dos contadores, a cero desde el arranque.** `eo_persistence_jobs_dropped_total{job, reason}` cuenta cada
   trabajo que la cola descarta, **antes** de ejecutar su compensación, con `reason` en `{queue_full,
   attempts_exhausted}` y `job` acotado al catálogo cerrado `simulation.JobNames` más `unknown`.
   `eo_commands_dropped_total{type}` cuenta cada comando que no cupo ni en el canal de comandos ni en el buzón,
   con `type` el tipo que define el código (`simulation.CommandType`, catálogo `simulation.CommandTypes`). Las
   series existen a cero desde `NewMetrics`, y la alerta propuesta es «cualquier valor > 0»
   ([../operations/monitoring.md](../operations/monitoring.md) §5.8). El catálogo de nombres y tipos vive en
   `simulation`, que es su dueño; `observability` lo importa para inicializar las series. El dominio no depende
   de la infraestructura; la infraestructura sí conoce el catálogo del dominio.
2. **Compensación por reencolado en el volcado.** `FlushDirty` (cada `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` =
   50 ticks, y en el apagado ordenado) vuelve a encolar, antes del lote de unidades, lo que la cola descartó
   desde el volcado anterior:
   - `city.presence` → `CityPresencePersistFailed{CityID}` marca la ciudad en `presenceDirty`; el volcado
     encola **la presencia actual** de la ciudad —estado y marca vigentes en RAM—, no la del trabajo perdido,
     de modo que si hubo otra transición entre medias la base converge al estado vigente de una sola vez. Para
     que esa escritura deje la fila completa, `CityRepo.SetPresence(PROTECTED)` escribe también
     `last_offline_at`, con el mismo instante que ya escribió `OFFLINE_PENDING`: en el camino normal no cambia
     nada, y si aquella escritura se perdió, la repara. Una transición nueva sustituye a la pendiente.
   - `movement.cancel` y `movement.complete` → `MovementFinishPersistFailed{MovementID, Status, UnitID}`
     apunta el cierre en `pendingFinishes` y devuelve la unidad al conjunto sucio. El volcado reintenta el
     cierre **sólo sobre la fila** de `unit_movements` (idempotente, y si un inicio posterior ya la canceló no
     cambia nada) y escribe la unidad con su posición **actual**, si sigue parada, por el camino del dirty-set.
     Nunca con la posición del cierre perdido, que puede ser antigua.
   - `units.flush` → `UnitsFlushPersistFailed{UnitIDs}` devuelve las unidades del lote al conjunto sucio sin
     apuntarlas al ocultamiento; salen en el siguiente volcado con su estado de entonces.
   - `movement.start` conserva la compensación de ADR-013 §2.4 (detener la unidad), y `garrison.enter` y
     `garrison.leave` las de ADR-017. `treaty.expire` **no** declara compensación: el barrido se repite cada
     segundo y el siguiente recoge lo que éste no pudo.
   La regla para toda escritura durable futura —los stocks de la economía los primeros— es la misma: o declara
   compensación que devuelva el dato al volcado, o se justifica por escrito que un hecho periódico lo vuelve a
   producir.
3. **El buzón del loop.** `loop.Inbox` es un canal propio, de 8192 (`cmd/server`, `inboxSize`), por el que el
   servidor se devuelve comandos a sí mismo: `Deps.Inbox` es `Inbox.Offer`, y por él viajan las
   compensaciones y respuestas de la cola (`MovementPersistFailed`, `GarrisonEntryResolved`,
   `GarrisonExitFailed` y los tres comandos nuevos) y la baja de una sesión cuando el canal de comandos está
   lleno. `drainCommands` lo drena **antes** que el canal de comandos en cada tick, con el mismo tope
   `MaxCommandsPerTick` para los dos. `Offer` nunca bloquea; si el buzón está lleno, cuenta y registra. Su
   capacidad acota lo que puede fallar entre dos ticks: la cola de persistencia entera (4096) más los
   descartes al encolar de un tick; llenarlo exige un fallo que no es de capacidad.
4. **El alta y la baja de una sesión.** `PlayerConnected` sigue en el canal de comandos, porque debe aplicarse
   antes que el `RequestSnapshot` que la misma conexión envía a continuación por ese canal (`INV-CITY-014`), y
   compartir cola es lo que conserva el orden; la conexión espera a que quepa, como mucho
   `ws.Config.DispatchTimeout` (2 s por defecto; con el loop vivo, el canal entero se drena en menos de uno),
   y si no cabe la sesión se cierra con `4500` **sin baja**: nunca llegó a contar. `PlayerDisconnected` va al
   canal de comandos y, si está lleno, al buzón: no puede perderse. Para que una baja que adelante a su alta
   por el buzón, o una baja duplicada, no desajusten nada, la simulación lleva las sesiones **por id** en un
   conjunto, no por recuento: una baja de una sesión desconocida se recuerda y anula su alta cuando llega; una
   duplicada no resta ni reinicia el margen de reconexión (`RN-PRESENCE-023`).
5. **Lo que todavía se pierde, y por qué se acepta.** (a) Lo que espere en `presenceDirty`, `pendingFinishes`
   o el conjunto sucio cuando el proceso cae: es RAM. El RPO deja de ser «la profundidad de la cola» para ser
   «lo que lleva fallando desde el primer descarte»; `eo_persistence_jobs_dropped_total` lo dice y la alerta
   lo convierte en incidente antes de que un crash lo convierta en pérdida. (b) Un comando que no cabe ni en el
   canal ni en el buzón: se cuenta en `eo_commands_dropped_total`; con 8192 plazas drenadas con prioridad en
   cada tick, exige un loop parado. (c) Un `treaty.expire` descartado: lo recoge el barrido siguiente. (d) La
   fila `ONLINE` de una ciudad cuyo proceso cayó antes de degradarla sigue sin repararse al arrancar
   ([../specs/presence.md](../specs/presence.md) §7.6): no es un descarte de la cola, es un arranque sin
   instante de desconexión, y queda como estaba. (e) `IntroducePlayer`, que `internal/httpapi` encola con una
   espera de 2 s y sólo un log si falla, no cambia aquí: no es una escritura ni un comando del loop hacia sí
   mismo, y su pérdida —un jugador fundado en la base que el mundo no conoce hasta el reinicio— merece su
   propia decisión.

## Alternativas consideradas

### Reintentar dentro de la cola hasta que salga bien

El worker reintentaría indefinidamente, con espera creciente, sin devolver nada al loop.

**Ventajas reales.** Ningún cambio en la simulación ni comandos nuevos; el orden por clave se conserva solo.

**Descarte.** Un trabajo que no sale bloquea su partición —y con ella a todas las entidades de esa partición—
mientras dure el fallo, que con PostgreSQL caído es indefinido; el trabajo retiene su carga original, que para
la presencia y los cierres puede quedar obsoleta mientras el mundo sigue cambiando; y los descartes al encolar
(`queue_full`) no pasan por el worker, así que no se reintentarían.

### Que la compensación vuelva a encolar en el acto

`OnPermanentFailure` devolvería un comando cuyo manejador hiciera `Submit` de nuevo, sin esperar al volcado.

**Ventajas reales.** Latencia de recuperación mínima: un tick en lugar de hasta 5 s.

**Descarte.** Con la partición llena, el reencolado se descarta en el acto, vuelve por el buzón, se reencola al
tick siguiente y así diez veces por segundo y por entidad: un bucle que llena el log y la métrica de ruido
mientras la base no responde. El volcado da una cadencia de 5 s sin añadir ningún temporizador, y es el
mismo mecanismo que ya gobierna el dirty-set.

### Esperar con plazo en `reinject` y en `dispatch`

Bloquear hasta un segundo a que haya sitio en el canal de comandos.

**Ventajas reales.** Sin canal nuevo ni cambios en el loop.

**Descarte.** `OnPermanentFailure` corre en la goroutine del loop cuando `Submit` descarta al encolar:
bloquear ahí es bloquear el tick. En el worker, bloquea su partición. Y no resuelve la causa: si el canal está
lleno es porque los jugadores lo llenan, y una espera sólo aplaza la competencia. Para el alta de una sesión sí
se acepta una espera acotada, porque la goroutine de la conexión no tiene nada mejor que hacer y el orden con
el snapshot obliga a usar ese canal.

### Llevar también `PlayerDisconnected` por el canal de comandos con espera acotada

**Ventajas reales.** Conserva el orden FIFO con el alta de la misma sesión, y no necesita llevar las sesiones
por id.

**Descarte.** La espera retiene la plaza de conexión del limitador ([ADR-021](ADR-021-game-server-perimeter.md))
durante una saturación, que es cuando más falta hace; y si el plazo vence, el único sitio al que ir es el
buzón, con el mismo problema de orden. Llevar las sesiones por id lo resuelve de raíz y además hace cierto
`RN-PRESENCE-023` ante bajas duplicadas, que el contador sólo cumplía a medias.

### Que la expiración de la clave de Redis degrade la ciudad

**Ventajas reales.** Ya existe y no depende del canal.

**Descarte.** `RN-PRESENCE-004` decide en RAM sin consultar Redis a propósito: Redis puede estar vacío o caído
y el mundo debe seguir ([ADR-004](ADR-004-redis-hot-state.md)). Invertirlo sería un ADR distinto y peor.

## Consecuencias

### Positivas

- Ningún trabajo de la cola se pierde en silencio: todos salvo `treaty.expire` vuelven al volcado, y los siete
  más `unknown` se cuentan por motivo. La alerta es binaria y las series existen desde el arranque.
- Una compensación nunca compite con los comandos de los jugadores por el canal, y una baja de sesión no
  puede dejar una ciudad `ONLINE` para siempre.
- Una cancelación de movimiento perdida ya no se deshace al rehidratar: la fila acaba `CANCELLED`.
- La escritura de recuperación de la presencia deja la fila completa, incluido `last_offline_at` en
  `PROTECTED`.
- El patrón para la economía está decidido antes de escribir su primera escritura.

### Negativas

- La recuperación tarda hasta un volcado (5 s) más que el fallo; una transición de presencia puede estar hasta
  ~5,6 s en RAM sin reflejo en la base, frente a los ~0,6 s de los tres intentos. Es tiempo de mundo en
  marcha, no de pérdida.
- `presenceDirty` y `pendingFinishes` son RAM: un crash con descartes pendientes los pierde, y el RPO de esos
  datos deja de estar acotado por la cola. La métrica y la alerta son la mitigación, no la cura.
- El buzón se drena antes que los comandos con el mismo tope: una avalancha de compensaciones puede dejar sin
  presupuesto a los comandos de los jugadores durante un tick. Está acotado y sólo ocurre en un incidente.
- Un cliente que conecta con el canal lleno espera hasta 2 s y recibe `4500` en lugar de un snapshot: lo que
  antes era una sesión a medias es ahora una reconexión.
- `earlyDisconnects` retiene el id de una baja duplicada mientras viva el proceso (16 bytes por duplicado). El
  servidor no produce duplicados; lo producirían sólo tests que no emparejan ids, y se corrigieron.
- `loop.New` y `ws.NewServer` cambian de firma, y el test que construye un loop debe darle un buzón o `nil`.

### Neutras

- `simulation` gana tres comandos (`MovementFinishPersistFailed`, `CityPresencePersistFailed`,
  `UnitsFlushPersistFailed`), las constantes `Job*` con `JobNames` y `CommandType`/`CommandTypes`; `State`
  gana `presenceDirty` y `pendingFinishes` con `PresenceDirtyCount` y `PendingFinishCount`; `Simulation` gana
  `SessionCount`.
- `loop` gana `Inbox`; `observability` importa `simulation` para el catálogo; `ws.Config` gana
  `DispatchTimeout`, sin variable de entorno (2 s no es un parámetro de gameplay ni de despliegue).
- Tests nuevos: `TestLasSeriesDeDescartesExistenACeroDesdeElArranque` y
  `TestUnNombreDeTrabajoFueraDelCatalogoCuentaComoUnknown` (`observability`);
  `TestUnDescartePorColaLlenaSeCuentaPorTrabajoYMotivo` y `TestAgotarLosIntentosSeCuentaUnaVezPorTrabajo`
  (`persistence`); `TestUnaPresenciaQueNoSePersisteSeReencolaConElEstadoActual`,
  `TestUnVolcadoQueNoSePersisteDevuelveLasUnidadesAlConjuntoSucio`,
  `TestUnCierreQueNoSePersisteSeReintentaEnElSiguienteVolcado` y
  `TestUnaBajaAdelantadaODuplicadaNoDesajustaLasSesiones` (`simulation`);
  `TestElBuzonLlenoNoBloqueaYCuentaElComandoQueNoCupo` y `TestElBuzonSeDrenaAntesQueElCanalDeComandos`
  (`loop`); `TestUnComandoDescartadoPorColaLlenaSeCuentaPorTipo`,
  `TestConElCanalLlenoLaBajaDeLaSesionVaAlBuzonDelLoop` y `TestSinSitioParaElAltaLaSesionSeCierraSinBaja`
  (`websocket`).

## Estado

Aceptado.
