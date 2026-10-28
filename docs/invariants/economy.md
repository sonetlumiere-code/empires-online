# Invariantes de Economía (INV-ECON-xxx)

Propiedades del primer slice de economía: el stock de madera de cada ciudad, la tarea de recolección, la casa y
el entrenamiento de aldeanos.

Formato y severidades: [README.md](README.md). Ciudades: [city.md](city.md). Unidades: [units.md](units.md).
Spec: [../specs/economy-wood.md](../specs/economy-wood.md). Decisiones:
[ADR-013](../decisions/ADR-013-persistence-pipeline.md), [ADR-022](../decisions/ADR-022-durable-write-retry-and-loop-inbox.md),
[ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md).

---

## Contexto: la economía, de extremo a extremo

| Pieza | Dónde | Qué hace |
|---|---|---|
| Reglas | `internal/domain/economy`, `internal/domain/building`, `internal/domain/production`, `internal/domain/task` | Stock y costes; casa y su emplazamiento; entrenamiento; máquina de estados de la tarea `GATHER_WOOD`. No escriben ni conocen el protocolo. |
| Simulación | `internal/game/simulation/economy.go`, `gather.go` | La tarea enganchada a la fase 3 (llegadas) y a la fase 5 (fin del trabajo, edificios y producciones vencidos); `city.build` y `city.train` en dos fases. |
| Persistencia | `postgres.EconomyRepo` | Cada transición de tarea con su tramo de movimiento; el volcado de depósitos; cada débito con su efecto, en una transacción e idempotente por `requestId`. |
| Arranque | `internal/game/loadcheck`, `simulation.Hydrate` | Repara lo que el esquema no garantiza (`INV-ECON-003`, `INV-ECON-004`) y reanuda lo que estaba a medias. |

### Lo que la base de datos garantiza y lo que no

| Constraint | Tabla | Qué impone |
|---|---|---|
| `city_stocks_amount_non_negative` | `city_stocks` | `CHECK (amount >= 0)` |
| `city_stocks_resource_valid` | `city_stocks` | `resource IN ('WOOD')` |
| `city_stocks_pkey` | `city_stocks` | `PRIMARY KEY (city_id, resource)`: una fila por ciudad y recurso |
| `unit_tasks_pkey` | `unit_tasks` | `PRIMARY KEY (unit_id)`: una tarea por unidad |
| `unit_tasks_phase_valid`, `unit_tasks_working_has_deadline` | `unit_tasks` | Fase cerrada, y `phase_until_ms` existe si y sólo si la fase es `WORKING` |
| `buildings_unique_tile` | `buildings` | `UNIQUE (x, y)`: un edificio por tile |
| `buildings_unique_request` | `buildings` | `UNIQUE (city_id, request_id)`: un encargo no produce dos edificios |
| `city_productions_pkey` | `city_productions` | `PRIMARY KEY (city_id)`: una producción por ciudad |

> **Lo que el esquema NO garantiza.** Que una unidad `WORKING` tenga su tarea en fase `WORKING` cruza dos
> tablas; que `population_limit` sea la fórmula de las casas cruza `cities`, `eras` y `buildings`. Las dos
> cosas las escribe la misma transacción que las cambia, y las comprueba la validación de carga al arrancar
> (`INV-ECON-003`, `INV-ECON-004`). Ninguna ficha de esta familia declara `DB` para ellas.

---

<a id="inv-econ-001"></a>
## INV-ECON-001 — El stock de una ciudad nunca es negativo

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E1 |
| Política ante violación | REJECT (constraint y guarda de dominio) |
| Cobertura | **Cubierto** por `Test_INV_ECON_001_UnDebitoMayorQueElStockNoSeEscribe` (integration, `internal/persistence/postgres`), `TestUnDebitoNuncaDejaElStockNegativo` (unit, `internal/domain/economy`) y `TestRechazosDeConstruccion` (simulation) |

**Enunciado.** Para toda fila de `city_stocks`, `amount >= 0`; y para toda ciudad en la RAM del Game Server, el
stock de cada recurso es `>= 0` tras cada comando y cada fase del tick.

**Razón.** Un stock negativo es madera gastada que nunca existió: un edificio o un aldeano gratis. Es la forma
más directa de hacer trampa en una economía, y por eso es la primera propiedad de la familia.

**Cómo se garantiza.**

- `DB` — `city_stocks_amount_non_negative`: un `UPDATE` que dejara el stock por debajo de cero aborta su
  transacción, y con ella el efecto que pagaba (ver [INV-ECON-002](#inv-econ-002)).
- `DOMAIN` — `economy.Stock.Debit` rechaza con `ErrInsufficientResources` un coste mayor que lo disponible y no
  muta nada; la simulación valida contra la RAM antes de reservar (`RN-ECON-007`, `RN-ECON-010`), y responde
  `INSUFFICIENT_RESOURCES`.
- `DOMAIN` — el volcado periódico sólo **suma** depósitos ([ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md)):
  ninguna escritura fuera de un encargo resta madera.

**Cómo se verifica.**

- `Test_INV_ECON_001_UnDebitoMayorQueElStockNoSeEscribe` (integration) — un `UPDATE` a negativo viola el
  `CHECK`, y un encargo que pide más de lo que hay se rechaza con `ErrInsufficientResources` sin escribir nada.
- `TestUnDebitoNuncaDejaElStockNegativo` y `TestCantidadesInvalidasNoMutan` (unit, `internal/domain/economy`) —
  el débito exacto deja cero, el excesivo no muta ni la cantidad ni la versión.
- `TestRechazosDeConstruccion`, `TestRechazosDeEntrenamiento` (simulation) — `INSUFFICIENT_RESOURCES` sin tocar
  el stock.

**Violación en runtime.** Un débito rechazado por el `CHECK` vuelve al loop como `INSUFFICIENT_RESOURCES` y la
compensación restaura la RAM. Si la RAM llegara a negativo —no hay ruta que lo haga—, el log
`invariant_violation` con `inv_id=INV-ECON-001` y la ciudad en `entity`.

---

<a id="inv-econ-002"></a>
## INV-ECON-002 — Todo débito ocurre en la misma transacción que su efecto

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E1 |
| Política ante violación | REJECT (la transacción revierte entera; la RAM se compensa) |
| Cobertura | **Cubierto** por `Test_INV_ECON_002_UnFalloAlInsertarElEdificioNoGastaMadera` y `TestUnEncargoDeCasaEsIdempotentePorRequestID` (integration), y `TestUnEncargoRechazadoDevuelveLaMadera` (simulation) |

**Enunciado.** Toda reducción de `city_stocks.amount` se confirma en la misma transacción que inserta el
edificio (`buildings`) o la producción (`city_productions`) que la justifica; y toda fila de `buildings` o de
`city_productions` se confirmó en una transacción que descontó su coste.

**Razón.** «Nunca hay madera gastada sin edificio ni edificio sin madera gastada» (`RN-ECON-008`). Si el débito
y el efecto viajaran en escrituras separadas, una caída entre las dos regalaría el edificio o se comería la
madera, y ninguna de las dos cosas se puede reconstruir después: el jugador no tiene cómo demostrar qué pagó.

**Cómo se garantiza.**

- `DB` — `EconomyRepo.Build` y `EconomyRepo.Train` aplican el delta del stock y la inserción en un solo
  `InTx`; cualquier error revierte las dos.
- `DOMAIN` — el stock se escribe por **deltas versionados** ([ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md)):
  el volcado periódico escribe sólo depósitos (deltas positivos), y el único camino que resta es el de un
  encargo, que lleva su coste y los depósitos pendientes de volcar en la transacción del efecto.
- `DOMAIN` — si la transacción falla o se rechaza, la respuesta vuelve al loop por su buzón y restaura la RAM:
  la madera reservada y los depósitos que viajaban con ella (`RN-ECON-008`).
- `DB` — `buildings_unique_request` y la fila de `city_productions` hacen el encargo idempotente: un reintento
  tras perder la confirmación del `COMMIT` no vuelve a debitar.

**Cómo se verifica.**

- `Test_INV_ECON_002_UnFalloAlInsertarElEdificioNoGastaMadera` (integration) — con un fallo inyectado al
  insertar el edificio (un tipo fuera de `buildings_type_valid`), el stock, la versión y los eventos no cambian.
- `TestUnEncargoDeCasaEsIdempotentePorRequestID`, `TestEntrenarReservaLaPlazaYEsIdempotente` (integration) — un
  encargo repetido con el mismo `requestId` no debita dos veces; `TestUnEncargoConElStockAdelantadoSeRechaza`, un
  encargo cuya versión ya está escrita no inserta su efecto.
- `TestUnEncargoRechazadoDevuelveLaMadera`, `TestUnFalloDefinitivoDelEncargoDevuelveLaReserva`,
  `TestUnEntrenamientoRechazadoDeshaceLaReserva` y `TestElEncargoLlevaLosDepositosSinVolcar` (simulation).

**Violación en runtime.** No hay estado intermedio que detectar: la transacción es la garantía. Un rechazo se
registra con su código y llega al jugador como `system.error`.

---

<a id="inv-econ-003"></a>
## INV-ECON-003 — Una unidad tiene como mucho una tarea, y está `WORKING` si y sólo si su tarea está en `WORKING`

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E1 |
| Política ante violación | REPAIR (al arrancar) |
| Cobertura | **Cubierto** por `TestUnaTransicionDeTareaEscribeTramoUnidadYTarea` (integration), `TestLasTareasIncoherentesSeReparanHaciaElLadoSinTarea` (unit, `internal/game/loadcheck`) y `TestUnGatherNuevoSustituyeLaTarea` (simulation) |

**Enunciado.** Para toda unidad hay como mucho una fila en `unit_tasks`; y `units.status = 'WORKING'` si y sólo
si existe su fila de `unit_tasks` con `phase = 'WORKING'`. Una fila de `unit_tasks` pertenece siempre a una
unidad viva (`status <> 'DEAD'`) que no está `GARRISONED`.

**Razón.** Una unidad con dos tareas haría dos viajes a la vez y depositaría el doble. Una unidad `WORKING` sin
tarea quedaría parada para siempre, sin temporizador que la despierte; una tarea en `WORKING` sobre una unidad
que no trabaja cargaría madera sin que nadie la viera trabajar.

**Cómo se garantiza.**

- `DB` — `unit_tasks.unit_id` es `PRIMARY KEY`: la segunda tarea es estructuralmente imposible (`RN-ECON-006`
  sustituye, no añade).
- `DOMAIN` — la simulación guarda las tareas por unidad, y cada transición de tarea escribe la fase y
  `units.status` en la misma transacción (`EconomyRepo.PersistTask`).
- `DOMAIN` — cancelar el tramo de una unidad (orden de movimiento, `unit.cancel_move`, ruta bloqueada) borra su
  tarea en la misma transacción que cierra el movimiento.
- `DOMAIN` — la validación de carga (`internal/game/loadcheck`) repara: una unidad `WORKING` sin tarea en
  `WORKING` vuelve a `IDLE`; una tarea de una unidad muerta, guarnecida o que no trabaja estando en `WORKING`
  se borra.

**Cómo se verifica.**

- `TestUnaTransicionDeTareaEscribeTramoUnidadYTarea` (integration) — una segunda fila para la misma unidad viola
  `unit_tasks_pkey`, la fase `WORKING` sin vencimiento viola `unit_tasks_working_has_deadline`, y la transición
  escribe tramo, unidad y tarea juntos; `TestUnaOrdenDeMovimientoBorraLaTareaEnSuTransaccion`, el inicio y el
  cierre de un movimiento borran la tarea.
- `TestLasTareasIncoherentesSeReparanHaciaElLadoSinTarea`, `TestUnaUnidadQueTrabajaConSuTareaEsCoherente` (unit,
  `internal/game/loadcheck`).
- `TestUnGatherNuevoSustituyeLaTarea`, `TestCancelarDetieneAUnaUnidadQueTrabaja` (simulation);
  `TestUnaTareaEnWorkingFueraDeSuBosqueTerminaAlArrancar` (recovery).

**Violación en runtime.** Detección en la validación de carga. Log `invariant_violation` con
`inv_id=INV-ECON-003`, `entity=unit:<id>` y la combinación encontrada. Política `REPAIR` con evento
`InvariantRepaired`: se elige siempre el lado que no regala madera.

---

<a id="inv-econ-004"></a>
## INV-ECON-004 — `population_limit` es el de la era más cinco por casa completa

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | E1 |
| Política ante violación | REPAIR (al arrancar) |
| Cobertura | **Cubierto** por `TestElLimiteDePoblacionSumaCincoPorCasaCompleta` (unit), `TestElLimiteDePoblacionCuentaLasCasasCompletas` (`internal/game/loadcheck`), `TestLaCasaSeCompletaEnElTickPrevistoYSubeElLimite` (simulation) y `TestCompletarUnaCasaSumaSuBonoUnaVez` (integration) |

**Enunciado.** Para toda ciudad, `population_limit = eras.population_cap(era) + 5 × (número de filas de
`buildings` de la ciudad con `type = 'HOUSE'` y `status = 'COMPLETE'`)`, tras cada transacción confirmada; salvo
la excepción de [INV-CITY-002](city.md#inv-city-002), que nunca destruye unidades.

**Razón.** Es la instancia concreta de [INV-CITY-003](city.md#inv-city-003), cuyo conjunto de modificadores
de edificios deja de estar vacío con la casa. El límite sigue siendo un valor **derivado**: si se pudiera
escribir suelto, una casa se podría cobrar dos veces o no cobrarse nunca.

**Cómo se garantiza.**

- `DOMAIN` — `building.PopulationLimit` es el punto único del cálculo (`RN-ECON-009`), con el bono de la
  definición del edificio (`building.Definition.PopulationBonus`).
- `DOMAIN` — completar una casa escribe `status = 'COMPLETE'` y `population_limit + 5` en la misma
  transacción, condicionada a que la fila siga `UNDER_CONSTRUCTION`: un reintento no suma dos veces.
- `DOMAIN` — la validación de carga recomputa el límite con la fórmula y lo repara.

**Cómo se verifica.**

- `TestElLimiteDePoblacionSumaCincoPorCasaCompleta`, `TestUnaCasaSoloSeCompletaUnaVez` (unit,
  `internal/domain/building`).
- `TestElLimiteDePoblacionCuentaLasCasasCompletas` (unit, `internal/game/loadcheck`) — una ciudad con dos casas
  completas y el límite de su era vuelve a `cap + 10`; una en obras no cuenta.
- `TestLaCasaSeCompletaEnElTickPrevistoYSubeElLimite` (simulation) — en el tick previsto, una sola vez.
- `TestCompletarUnaCasaSumaSuBonoUnaVez` (integration) — completar dos veces la misma casa sólo suma una.

**Violación en runtime.** Detección en la validación de carga. Log `invariant_violation` con
`inv_id=INV-ECON-004` (`INV-CITY-003` si la ciudad no tiene casas), el límite persistido y el calculado.
Política `REPAIR` con `InvariantRepaired`.

---

<a id="inv-econ-005"></a>
## INV-ECON-005 — Lo que estaba a medias termina, tras un reinicio, en el mismo instante absoluto

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | E1 |
| Política ante violación | REPAIR (la recuperación avanza la máquina de estados) |
| Cobertura | **Cubierto** por los tests de recuperación de `internal/game/simulation/economy_recovery_test.go` |

**Enunciado.** Una tarea, un edificio o una producción a medias en el instante de una caída terminan su fase en
curso en el mismo instante absoluto (`phase_until_ms`, la llegada de su tramo, `completes_at_ms`) en que habría
terminado sin la caída; un vencimiento que cayó durante la caída se aplica en el primer tick con su instante
original, no con el del arranque.

**Razón.** Es la promesa de la polilínea temporizada ([ADR-011](../decisions/ADR-011-movement-timed-polyline.md))
extendida a la economía: el mundo sigue existiendo aunque el proceso no. Un reinicio que retrasara o
adelantara las construcciones regalaría o robaría tiempo de forma desigual entre jugadores.

**Cómo se garantiza.**

- `DOMAIN` — sólo se persisten tiempos absolutos; nada se recalcula al arrancar a partir del reloj.
- `DOMAIN` — `simulation.Hydrate` no cierra el tramo de una tarea cuya llegada venció durante la caída: lo deja
  activo para que la fase 3 del primer tick procese la llegada con su `arrivalTimeMs`. La fase siguiente se
  ancla a ese instante (`phase_until_ms = llegada + workMs`; el tramo siguiente arranca en `phase_until_ms`).
- `DOMAIN` — la fase 5 completa edificios y producciones con `completes_at_ms <= ahora` sin distinguir si
  vencieron antes o después del arranque.
- `DOMAIN` — durante la caída no hay dueños conectados, así que ningún viaje **nuevo** empieza por haber
  vencido estando caído el servidor ([INV-ECON-006](#inv-econ-006)): el viaje en curso se termina.

**Cómo se verifica.** Tests de recuperación (simulation, `economy_recovery_test.go`): `TestReinicioDuranteLaIdaAlBosque`,
`TestReinicioConLaLlegadaAlBosqueVencidaEnLaCaida`, `TestReinicioConLaLlegadaYElTrabajoVencidos`,
`TestReinicioDuranteElTrabajo`, `TestReinicioConLaVueltaVencidaTerminaElViajeSinEmpezarOtro`,
`TestReinicioDuranteLaVueltaConElDuenoConectado`, `TestReinicioDuranteUnaConstruccion` y
`TestReinicioDuranteUnEntrenamiento`, cada uno con el vencimiento antes y después del arranque; y
`TestUnaTareaSinSuTramoTerminaAlArrancar`, `TestUnaProduccionTerminadaNoReservaNada`.

**Violación en runtime.** Una tarea que no puede reanudarse —su tramo desapareció, su unidad dejó el tile— se
termina con la unidad `IDLE` donde está, y se registra.

---

<a id="inv-econ-006"></a>
## INV-ECON-006 — Ningún viaje nuevo empieza con el dueño ausente

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | E1 |
| Política ante violación | REJECT (el viaje no empieza; la tarea termina) |
| Cobertura | **Cubierto** por `TestConElDuenoAusenteElViajeTerminaYNoEmpiezaOtro`, `TestUnaCiudadOnlineSinSesionNoEmpiezaViajesNuevos` (simulation), `TestReinicioConLaVueltaVencidaTerminaElViajeSinEmpezarOtro` (recovery) y el golden `madera` |

**Enunciado.** Una tarea de recolección sólo empieza un viaje nuevo (el tramo `TO_RESOURCE` que sigue a un
depósito) si la ciudad de la unidad está `ONLINE` y su dueño tiene una sesión viva o está dentro del margen de
reconexión.

**Razón.** Es la decisión de producto Q1 de la spec: la ausencia no acumula recursos
([../product/vision.md](../product/vision.md) §7). La segunda mitad del enunciado cubre el caso que la presencia
de ciudad no cubre: tras un reinicio, una ciudad que estaba `ONLINE` se queda `ONLINE` aunque nadie se conecte
([../specs/presence.md](../specs/presence.md) §7.6), y sin ella sus aldeanos recolectarían indefinidamente.

**Cómo se garantiza.**

- `DOMAIN` — la transición «depósito → siguiente viaje» de la simulación pregunta por la presencia viva del
  dueño antes de pedir la ruta; si no la hay, deposita, termina la tarea y deja la unidad `IDLE` en el tile de
  depósito (`RN-ECON-005`).

**Cómo se verifica.** `TestConElDuenoAusenteElViajeTerminaYNoEmpiezaOtro` (simulation): con el dueño
desconectado a mitad del tramo al bosque, la unidad termina el viaje, deposita una vez más y queda `IDLE` en el
depósito, sin ningún tramo nuevo al bosque; `TestUnaCiudadOnlineSinSesionNoEmpiezaViajesNuevos`, con la ciudad
`ONLINE` y nadie conectado, igual; y el golden `madera` (`TestLosEscenariosReproducenSuGolden`), con el depósito
del tick 895 como el último.

**Violación en runtime.** No hay estado que detectar: la guarda es la propia transición.
