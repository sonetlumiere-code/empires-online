# Pruebas de simulación

Verificación determinista del game loop: se construye un mundo en un estado inicial conocido, se inyecta `FakeClock`, se avanzan N ticks y se asserta el estado **exacto**. Es el nivel que convierte el determinismo de un principio en una propiedad comprobada.

> **Estado real.** Este nivel **existe y está en verde**: `services/game-server/internal/game/simulation/simulation_test.go`
> contiene el vertical slice completo, el reemplazo y la cancelación de órdenes, los seis rechazos,
> el ciclo de presencia y protección, los tres casos de recuperación en RAM, la reproducibilidad y
> los snapshots. Los casos ✔ llevan su nombre de test real; los ○ están previstos.
> El loop y su orden de fases están en [../architecture/game-loop.md](../architecture/game-loop.md).

---

## 1. El patrón

Un test de simulación tiene siempre la misma forma, y esa uniformidad es deliberada: hace que cualquiera pueda leer uno nuevo sin contexto previo.

```
1. ARRANGE   Mundo en un estado inicial conocido y minimal.
             Grid explícito (mapa ASCII), entidades explícitas, FakeClock en TestEpochMs.
             Nada de "el mundo por defecto": si no aparece en el test, no existe.

2. ACT       Encolar comandos en instantes concretos y avanzar N ticks.
             El tiempo solo avanza porque el test lo avanza.

3. ASSERT    Estado EXACTO: tile exacto, tick exacto, status exacto, secuencia
             exacta de eventos emitidos. Nunca "aproximadamente", nunca "al menos",
             nunca una tolerancia.
```

**Diferencia con los otros niveles.** [unit-tests.md](./unit-tests.md) verifica funciones puras (la polilínea, la posición derivada) sin loop. [integration-tests.md](./integration-tests.md) verifica que eso se persiste. Aquí se verifica lo único que ninguno de los dos puede: que **el loop, ejecutando sus fases en orden, tick tras tick, produce la secuencia de estados correcta**. Es donde aparecen los errores de un tick de desfase, de orden de fases y de eventos emitidos dos veces.

**Sin infraestructura.** Repositorios dobles en memoria, `clock.FakeClock`, `Pathfinder` **real** (es determinista y puro, no hace falta doblarlo). No hay Docker, no hay gate `EO_INTEGRATION=1`, la suite entera termina en menos de 30 s.

### 1.1 La pieza que lo hace posible: `Loop.Step`

El loop expone dos entradas, y la segunda existe **exactamente para este nivel de test**:

```go
// internal/game/loop/loop.go

// Run ejecuta el loop hasta que se cancele el contexto.
func (l *Loop) Run(ctx context.Context) error

// Step ejecuta UN tick completo en el instante indicado.
//
// Está separado de Run a propósito: los tests de simulación lo invocan
// directamente con un FakeClock para avanzar el mundo de forma determinista, sin
// depender de temporizadores reales ni de la velocidad de la máquina.
func (l *Loop) Step(nowMs int64)
```

`Run` es un temporizador con calendario en tiempo absoluto: si un tick se pasa de tiempo el siguiente no se retrasa en cascada, y los ticks perdidos se **saltan** en lugar de ejecutarse en ráfaga para ponerse al día. `Step` es ese mismo tick sin temporizador. El test controla el reloj y llama a `Step`; no hay `time.Sleep`, no hay goroutines de temporización, no hay dependencia de la velocidad de la máquina.

Dentro de `Step`, las fases del canon §6 en orden fijo:

```go
// Fase 1-2: consumir y aplicar comandos.
// Fase 3: avanzar el movimiento.
// Fase 4: resolver simulación (combate — fuera del MVP).
// Fase 5: temporizadores y eventos programados.
// Fase 6-7: el estado del mundo y los deltas ya se emitieron dentro de las
//           fases anteriores, según ocurrían los hechos.
// Fase 8: encolar persistencia. Nunca I/O síncrono aquí.
```

`applyCommand` además **aísla el pánico de un comando concreto**: un bug en una regla no puede tumbar la simulación del mundo entero.

### 1.2 Aritmética del tick

Canon §6. A `EO_TICK_RATE_HZ = 10` el período es **100 ms**.

```
tickTime(n) = epochMs + n * 100
```

De ahí la regla que gobierna casi todas las aserciones de este documento:

> Un evento programado para el instante `T` ocurre en el **primer tick cuyo `tickTime >= T`**,
> es decir en el tick `ceil((T - epochMs) / 100)`.

El loop no interpola dentro del tick ni adelanta eventos: el tiempo del mundo avanza a saltos de 100 ms. Un movimiento que llegara en `t = 2769` se completaría en el tick 28 (`t = 2800`), no en el 27. Esos 31 ms de retraso son una consecuencia declarada del tick discreto, no un error.

El escenario del vertical slice está elegido a propósito para que **la llegada caiga en un múltiplo exacto del período** (3000 ms = tick 30) y las aserciones no dependan de ese redondeo: cuando lo que se quiere verificar es la aritmética del movimiento, mezclarla con la del tick esconde cuál de las dos falló.

---

## 2. El harness real, línea por línea

Todo el fichero se apoya en un `harness` que monta un mundo determinista de 64×64 de hierba con un jugador, su ciudad y tres aldeanos. Éste es el código real:

```go
// internal/game/simulation/simulation_test.go

// epoch es un instante fijo. Todos los tests parten de aquí: nada depende del
// reloj real de la máquina.
const epoch int64 = 1_757_376_000_000

type harness struct {
	t        *testing.T
	clk      *clock.FakeClock
	state    *simulation.State
	sim      *simulation.Simulation
	loop     *loop.Loop
	rec      *recorder
	repos    *fakeRepos
	commands chan simulation.Command
	playerID uuid.UUID
	cityID   int64
	units    []*unit.Unit
}

func newHarness(t *testing.T) *harness {
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)

	clk := clock.NewFakeClock(epoch)
	rec := &recorder{}
	repos := newFakeRepos()
	state := simulation.NewState(w)

	sim := simulation.New(state, simulation.Deps{
		Clock:              clk,
		Pathfinder:         pathfinding.NewAStar(20000, 256),
		Broadcaster:        rec,
		Persister:          &syncPersister{},
		Repos:              repos,
		Log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		ProtectionCooldown: 300 * time.Second,
		DisconnectGrace:    30 * time.Second,
		PathMaxNodes:       20000,
		PathMaxDistance:    256,
	})

	commands := make(chan simulation.Command, 256)
	gameLoop := loop.New(sim, clk, commands, loop.Config{
		TickDuration:       100 * time.Millisecond,
		FlushIntervalTicks: 50,
		MaxCommandsPerTick: 256,
		EpochMs:            epoch,
	}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// … añade la ciudad en (10,10) y tres aldeanos en (12,10), (8,10) y (10,12)
}
```

Y los dos únicos verbos que necesita un test:

```go
// advance adelanta el reloj y ejecuta los ticks correspondientes.
func (h *harness) advance(d time.Duration) {
	ticks := int(d.Milliseconds() / 100)
	for i := 0; i < ticks; i++ {
		h.clk.AdvanceMs(100)
		h.loop.Step(h.clk.NowMs())
	}
}

// send encola un comando y ejecuta un tick para que se aplique.
func (h *harness) send(cmd simulation.Command) {
	h.commands <- cmd
	h.loop.Step(h.clk.NowMs())
}
```

**Ése es todo el patrón: `AdvanceMs` + `Step`.** Un tick del mundo por iteración, el reloj movido a mano, y ninguna espera. Fíjese en que `send` **no** adelanta el reloj: el comando se aplica en el instante actual, así que `startTimeMs` del movimiento es exactamente el `epoch` del escenario y las aserciones se leen sin aritmética mental.

### 2.1 Los tres dobles de prueba

| Doble | Sustituye a | Qué hace y por qué |
|---|---|---|
| `recorder` | El emisor hacia la red (`BroadcastChunk`, `SendToPlayer`) | Captura **todo** lo emitido con su tipo y su destino. `byType(msgType)` filtra y `reset()` limpia, lo que permite afirmar «tras esta orden se emitió **exactamente un** `unit.movement.cancelled`» sin contar el ruido anterior. Lleva mutex: la suite corre con `-race` |
| `syncPersister` | La cola de persistencia con workers | **Ejecuta el trabajo al instante**, en la misma goroutine. Los tests no deben depender de temporizaciones de workers; lo que se quiere verificar es *qué* se encola y *cuándo*, no cuánto tarda un pool en drenarlo. `names()` devuelve la lista de trabajos encolados por nombre |
| `fakeRepos` | Los repositorios de PostgreSQL | Registra las escrituras durables sin tocar ninguna base de datos, y **emula el identificador que asignaría PostgreSQL** (`m.ID = len(movementsStarted)+1`). Cuenta `positionFlushes` y `unitsFlushed`, que es lo que permite verificar el volcado por lotes |

El `Pathfinder` **no** se dobla: `pathfinding.NewAStar(20000, 256)` es el real. Es determinista y puro, así que doblarlo sólo introduciría una segunda implementación que mantener y una oportunidad de que el test pase con una ruta que el sistema real nunca produciría.

---

## 3. El vertical slice, el test canónico del nivel

`TestVerticalSliceMovimiento` es el test que da sentido a todo el fichero: el mundo arranca en `epoch`, la unidad se mueve de A a B, se avanza el tiempo, y se asserta el estado **exacto** en cada instante.

Escenario: aldeano en `(12,10)`, destino `(17,10)`. Cinco tiles de hierba en ortogonal ⇒ `5 × 600 = 3000 ms` exactos.

```go
h.send(simulation.MoveUnit{
    PlayerID: h.playerID, RequestID: uuid.NewString(),
    UnitID: u.ID, Target: world.Tile{X: 17, Y: 10},
})

accepted := h.rec.byType(protocol.TypeUnitMoveAccepted)
require.Len(t, accepted, 1, "el comando debe aceptarse")

started := h.rec.byType(protocol.TypeUnitMovementStarted)
require.Len(t, started, 1)
payload := started[0].Payload.(protocol.UnitMovementStartedPayload)
require.Len(t, payload.Movement.Path, 6, "origen más cinco pasos")
require.EqualValues(t, 0, payload.Movement.Path[0].TMs)
require.EqualValues(t, 3000, payload.Movement.Path[5].TMs)
require.Equal(t, epoch+3000, payload.Movement.ArrivalTimeMs)
```

### 3.1 Posición instante a instante

Ésta es la parte que ningún otro nivel puede verificar, y la tabla **es** el test:

| `elapsed` | ticks transcurridos | Posición | `unit.Status` | Qué fija |
|---|---|---|---|---|
| 0 | 0 | `(12,10)` | `MOVING` | Aceptar la orden no teletransporta a nadie |
| 600 | 6 | `(13,10)` | `MOVING` | Primer tile alcanzado |
| 1200 | 12 | `(14,10)` | `MOVING` | Segundo |
| 2400 | 24 | `(16,10)` | `MOVING` | **A 2400 ms aún no ha llegado**: el penúltimo tile |
| 3000 | 30 | `(17,10)` | `IDLE` | **Llegada exacta** |

Y al llegar, tres cosas a la vez:

```go
_, stillMoving := h.state.Movement(u.ID)
require.False(t, stillMoving, "el movimiento terminado se retira del mundo")

completed := h.rec.byType(protocol.TypeUnitMovementCompleted)
require.Len(t, completed, 1)
done := completed[0].Payload.(protocol.UnitMovementCompletedPayload)
require.Equal(t, protocol.Tile{X: 17, Y: 10}, done.FinalPosition)
require.Equal(t, m.ID, done.MovementID)
```

Un error de un tick de desfase —el clásico `<` donde debía ir `<=`— mueve exactamente una fila de esa tabla, y el test señala el instante concreto.

### 3.2 Casos ○ previstos sobre el tick

| # | Test | Escenario | Aserción exacta |
|---|---|---|---|
| **M1** | `TestLlegadaConRedondeoHaciaArriba` | Movimiento cuya duración **no** es múltiplo de 100 ms (por ejemplo la polilínea canónica de 2769 ms) | Llegada en el tick 28 (`ceil(2769/100)`), no en el 27. Fija por escrito el redondeo del tick discreto para que nadie lo "arregle" más tarde **Estado (2026-10-21):** ○ sin test. |
| **M2** | `TestUnCaminoLargoNoAcumulaDeriva` | 60 tiles en línea recta de hierba (36 000 ms) | Llegada en el tick 360 exacto; ninguna deriva acumulada. El tiempo se deriva siempre de `epochMs + n*100`, nunca de sumar `+100` a una variable **Estado (2026-10-21):** ○ sin test. |
| **M3** | `TestElNumeroDeTickCreceEstrictamente` | Avanzar 10 s | La secuencia observada de `loop.Tick()` es exactamente `0, 1, …, 100`, sin huecos ni repeticiones **Estado (2026-10-21):** ✔ `TestElTickEsEstrictamenteCreciente` (`recovery_test.go`), con nueve pasos y saltos en vez de 0..100. |
| **M4** | `TestElOrdenDeFasesEsFijo` | Un tick instrumentado | Las fases ocurren en el orden del canon §6, y en particular la persistencia se **encola** en la fase 8, después de haber emitido los deltas **Estado (2026-10-21):** ○ en parte: `TestTerminarElMovimientoDentroDeLaZonaPasaPorIdleYLaOculta` fija que la fase 3 va antes que la 5. |
| **M5** | `TestSinIoBloqueanteDentroDelTick` | Tick instrumentado con un repositorio que registra llamadas | Cero I/O síncrono contra PostgreSQL dentro del tick **Estado (2026-10-21):** ○ sin test; lo más cercano es `TestConLaColaLlenaSubmitNoBloqueaYCompensa` (`persistence`). |
| **M6** | `TestMovimientosSimultaneosSonIndependientes` | 50 unidades con destinos distintos lanzadas en el mismo tick | Cada una llega en su tick calculado; el orden de procesamiento no altera ningún resultado. Hoy `TestSimulacionEsReproducible` cubre parcialmente esto con tres unidades **Estado (2026-10-21):** ○ en parte: `TestSimulacionEsReproducible` y `TestDiezUnidadesMoviendoseNoPasanDelPresupuestoDelTick`. |
| **M7** | `TestUnPanicoEnUnComandoNoTumbaLaSimulacion` | Comando que provoca un pánico dentro de `applyCommand` | El tick continúa, el resto de comandos se aplican, y el pánico queda registrado y contado. Es el comportamiento que `applyCommand` ya implementa y que ningún test verifica todavía **Estado (2026-10-21):** ○ sin test; el código lo hace (`applyCommand` con `recover`). |

---

## 4. Los demás casos ✔ implementados

Todos viven en el mismo `simulation_test.go` y comparten `newHarness`. Los tiempos son los del
harness: mundo 64×64 de `GRASSLAND`, aldeano a 600 ms/tile, tick de 100 ms, `epoch` fijo.

### 4.1 Reemplazo de orden a mitad de camino

`TestNuevaOrdenReemplazaLaAnterior` lleva encima el comentario `// INV-MOVE-001: una unidad tiene
como máximo un movimiento activo.` Escenario: aldeano en `(12,10)`, orden hacia `(20,10)`, y a los
**1200 ms** (dos tiles recorridos, unidad en `(14,10)`) una segunda orden hacia `(14,20)`.

| Aserción exacta | Qué fija |
|---|---|
| Se emite **exactamente un** `unit.movement.cancelled`, con `movementId` del **primer** movimiento | Reemplazar no deja el movimiento anterior colgando |
| `reason == string(movement.ReasonReplaced)`, es decir **`"REPLACED"`** | El enum real; no existe `SUPERSEDED` en este código |
| `stoppedAt == (14,10)` | «La unidad se detiene sobre un tile completo, jamás entre dos» |
| El movimiento nuevo tiene `ID` distinto y `Path.Origin() == (14,10)` | «La ruta nueva parte de donde está realmente la unidad», no del origen del trayecto anterior |
| Tras `advance(6100 ms)`: unidad en `(14,20)` e `IDLE` | Llega al **nuevo** destino, no al viejo (10 tiles ortogonales = 6000 ms) |

### 4.2 Cancelación explícita

`TestCancelacionExplicita`: orden desde `(12,10)` hacia `(22,10)`, `advance(1800 ms)` (tres tiles,
unidad en `(15,10)`) y entonces `simulation.CancelMovement`.

| Aserción exacta | Qué fija |
|---|---|
| Un solo `unit.movement.cancelled` con `reason == string(movement.ReasonCancelledByPlayer)` ⇒ **`"CANCELLED_BY_PLAYER"`** | El valor del enum para la cancelación pedida por el jugador |
| `stoppedAt == (15,10)` | Cancelar deja la unidad en el **último tile alcanzado** |
| `unit.Status == IDLE` y `state.Movement(unitID)` ya no existe | El movimiento cancelado se retira del mundo |
| Tras `advance(5 s)` sigue en `(15,10)` | Cancelar detiene de verdad; no queda una polilínea avanzando de fondo |

El enum completo de `movement.CancelReason` es
**`REPLACED | CANCELLED_BY_PLAYER | PATH_BLOCKED | UNIT_DEAD | SERVER`**.

### 4.3 Rechazos del comando `unit.move`

`TestRechazosDeMovimiento` es table-driven con **siete** subcasos (el último, «unidad ajena y muerta», fija el orden de validación) y lleva el comentario
`// INV-PLAYER-001: un jugador sólo puede comandar unidades propias.` Cada subcase construye su
propio harness.

| Subtest | Preparación | `code` esperado |
|---|---|---|
| `unidad inexistente` | `UnitID: 9999` | `UNIT_NOT_FOUND` |
| `unidad ajena` | `PlayerID` de otro jugador sobre la unidad 1 | `UNIT_NOT_OWNED` |
| `destino fuera del mundo` | `Target: (500,500)` en un mundo 64×64 | `TARGET_OUT_OF_BOUNDS` |
| `destino intransitable` | `SetBlocked(30,30,30,30,true)` y destino `(30,30)` | `TARGET_NOT_WALKABLE` |
| `unidad muerta` | `Status = DEAD`, `HP = 0` | `UNIT_DEAD` |
| `unidad guarnecida` | `Status = GARRISONED` | `UNIT_GARRISONED` |

**Los seis** comprueban además `require.Empty(t, h.rec.byType(protocol.TypeUnitMovementStarted))`:
«un comando rechazado no puede producir ningún movimiento». Es la mitad que convierte un test de
código de error en un test de ausencia de efectos secundarios.

Dos casos más, con test propio:

| # | Test | Escenario | Aserción exacta |
|---|---|---|---|
| — | `TestSinRutaPosibleSeRechaza` | Destino `(40,40)` amurallado con `SetBlocked(39,39,41,41,true)` y el centro liberado: transitable pero aislado | `unit.move.rejected` con `code = PATH_NOT_FOUND` |
| — | `TestMoverseAlSitioDondeYaEstas` | `Target` = tile actual de la unidad | Se emite **`unit.move.accepted`** y **ningún** `unit.movement.started` («no se crea una polilínea degenerada de un solo punto»); la unidad sigue `IDLE`. Ordenar lo redundante no es un error del jugador |

### 4.4 Presencia y protección offline

`EO_PRESENCE_TTL_SECONDS = 30` se inyecta como `DisconnectGrace` y
`EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS = 300` como `ProtectionCooldown`. En este nivel la
presencia vive en RAM y la decide el game loop comparando contra `DisconnectGrace`: **no** se lee la
expiración de la clave de Redis. La expiración real de la clave se verificará en
[integration-tests.md](./integration-tests.md) §5.4.

`TestCicloDePresenciaYProteccion` recorre el ciclo completo:

```
connect                       presence_state = ONLINE
disconnect                    sigue ONLINE  — «un corte breve no degrada nada»
+29 s                         sigue ONLINE  — aún dentro del margen
+2 s   (total 31 s > 30 s)    OFFLINE_PENDING, LastOfflineAt != nil
+290 s                        sigue OFFLINE_PENDING — el cooldown aún no venció
+20 s  (total > 300 s)        PROTECTED, IsProtected() == true
connect                       ONLINE otra vez, ProtectionUntil == nil
```

| # | Test | Escenario | Aserción exacta |
|---|---|---|---|
| — | `TestReconexionDentroDelMargenNoDegradaLaCiudad` | Desconexión, 10 s, reconexión, y luego 60 s más | Sigue `ONLINE`: reconectar dentro del margen **cancela** la degradación, no la aplaza |
| — | `TestVariasSesionesDelMismoJugador` | Dos sesiones abiertas; se cierra una y pasan 120 s; después se cierra la otra y pasan 60 s | Con una pestaña abierta sigue `ONLINE`; sólo al cerrar la última llega a `OFFLINE_PENDING`. La presencia es del **jugador**, no de la conexión |
| — | `TestElMovimientoContinuaConElJugadorDesconectado` | Orden hacia `(22,10)`, `advance(600 ms)` ⇒ `(13,10)`, desconexión, `advance(5400 ms)` | La unidad llega a `(22,10)` e `IDLE` **con el jugador desconectado**. Es la prueba directa de «offline ≠ mundo detenido» (canon §1.2) |

### 4.5 Recuperación en RAM (`simulation.Hydrate`)

Estos tres tests **no** usan el harness: construyen el mundo y las entidades a mano y llaman
directamente a `simulation.Hydrate(state, units, cities, movements, nowMs, log)`, que devuelve un
resultado con los contadores `Resumed`, `Arrived`, `Failed` y la lista `FinishedMovements`. Es la
mitad en memoria del nivel `recovery`; la mitad durable —crash y reinicio contra PostgreSQL real—
pertenece a [integration-tests.md](./integration-tests.md) §5.6, y la ejercitan `TestRecuperacionCompletaTrasReinicio` y `TestConLaColaRealElMovimientoLlevaSuIdYCierraConSuPosicion` contra PostgreSQL real.

| # | Test | Escenario | Aserción exacta |
|---|---|---|---|
| **H1** | `TestRecuperacionMovimientoEnCurso` | Polilínea `(10,10)→(14,10)` (`tMs = [0,600,1200,1800,2400]`), el proceso vuelve en `epoch + 1500` | `Resumed == 1`, `Arrived == 0`; la unidad aparece en **`(12,10)`** —último waypoint con `tMs <= 1500`— y sigue `MOVING`; el movimiento continúa activo. «La posición se reconstruye de la polilínea, **sin recalcular la ruta**» |
| **H2** | `TestRecuperacionMovimientoVencidoDuranteLaCaida` | Polilínea `(10,10)→(12,10)` (llegada en `epoch + 1200`), el proceso vuelve **una hora** después (`epoch + 3_600_000`) | `Arrived == 1`, `Resumed == 0`; la unidad aparece en su destino `(12,10)` e `IDLE`; no queda movimiento activo; `FinishedMovements[0] == {MovementID: 77, Status: COMPLETED}` |
| **H3** | `TestRecuperacionConPolilineaInvalida` | El mapa cambió durante la caída: `SetBlocked(11,10,11,10,true)` sobre un tile de la polilínea; se vuelve en `epoch + 300` | `Failed == 1`; la unidad **se queda donde estaba**, `(10,10)`, e `IDLE`; `FinishedMovements[0].Status == FAILED` |

H3 es el que codifica la regla de fondo: **ante un dato dudoso no se teletransporta a nadie**. Un
movimiento cuya polilínea ya no es válida se cierra como `FAILED` y la unidad conserva su última
posición consolidada.

### 4.6 Reproducibilidad

`TestSimulacionEsReproducible` ejecuta **dos veces** el mismo escenario —tres aldeanos ordenados
hacia `(30,30)` y 30 s de simulación— y compara la lista de tiles finales recorrida con
`state.EachUnit`.

```go
require.Equal(t, run(), run(),
    "la misma secuencia de comandos debe dar el mismo estado final")
```

Los escenarios de §6 aplican la misma comprobación a escala: cada uno se ejecuta dos veces y el
resultado completo —mensajes, escrituras y estado final— tiene que coincidir byte a byte.

`TestSimulacionEsReproducible` es deliberadamente modesto y por eso es fiable: no depende de ningún serializador canónico ni de
ningún fichero de escenario. Detecta lo que tiene que detectar —iteración de un `map` sin ordenar
(canon §8), aleatoriedad no inyectada, `time.Now()` filtrado en el dominio— porque `EachUnit` recorre
las unidades en orden estable y cualquiera de esos fallos rompería ese orden o el resultado.

Casos ○ previstos que refuerzan la propiedad:

| # | Test | Verifica |
|---|---|---|
| **R1** | `TestReproducibleAlAgruparTicks` | Avanzar 100 ticks de golpe y avanzar 100 veces un tick producen el mismo estado final: el loop no puede depender del tamaño del lote **Estado (2026-10-21):** ○ sin test. |
| **R2** | `TestOrdenDeComandosDentroDelTickEsEstable` | 20 comandos encolados en el mismo tick se aplican en orden FIFO; dos ejecuciones dan el mismo resultado **Estado (2026-10-21):** ○ sin test. |
| **R3** | `TestNoSeFiltraElRelojDelSistema` | El escenario ejecutado con el reloj real de la máquina en otro instante da el mismo resultado (regla R3 de [strategy.md](./strategy.md)) **Estado (2026-10-21):** ✔ estático, no en ejecución: `scripts/lint-determinism.mjs` y su test «el árbol real del Game Server está limpio». |
| **R4** | `TestReproducibleConGomaxprocsVariable` | El mismo escenario con `-race` y `GOMAXPROCS` en 1, 2 y 8 ⇒ mismo estado final. Si el resultado depende del paralelismo, hay estado compartido no determinista **Estado (2026-10-21):** ○ sin test. |

El determinismo del **generador de mundo** con la misma semilla no se verifica aquí sino en el nivel
`unit`: `TestGeneracionEsDeterminista` en `world_test.go` (ver [unit-tests.md](./unit-tests.md) §3.1,
caso C10).

### 4.7 El volcado de posiciones ocurre por lotes, no por tick

`TestVolcadoPeriodicoNoOcurreEnCadaTick` reconstruye la simulación con un `syncPersister`
observable, lanza un movimiento y avanza **60 ticks** con `FlushIntervalTicks: 50`.

```go
flushes := 0
for _, name := range persister.names() {
    if name == "units.flush" {
        flushes++
    }
}
require.Equal(t, 1, flushes,
    "se vuelca por lotes cada 50 ticks, jamás una escritura por unidad y por tick")
require.Equal(t, 1, repos.positionFlushes)
```

Es la comprobación de que la fase 8 **encola** y agrupa. Una implementación que escribiera la
posición en cada tick pasaría todos los tests de posición y fallaría éste, que es exactamente el
punto: el coste de persistencia es una propiedad observable del diseño, no un detalle.

### 4.8 Snapshots del área de interés

El snapshot no lo construye la goroutine de la conexión: se pide con un comando y lo responde el
loop. En el test se invoca directamente `sim.BuildSnapshot(chunks, nowMs, includeTerrain)`.

| # | Test | Escenario | Aserción exacta |
|---|---|---|---|
| **S1** | `TestSnapshotDelAreaDeInteres` | `ChunksInRadius((10,10), 2)`, con terreno incluido | `ServerTimeMs` igual al reloj; **3** unidades; **1** ciudad; cada entrada de `Terrain` con `Size == 32` y el blob **no vacío**, «el terreno viaja en base64». Un área lejana (`(60,60)`, radio 0) devuelve cero unidades y cero ciudades |
| **S2** | `TestSnapshotIncluyeElMovimientoEnCurso` | Orden hacia `(20,10)`, `advance(1200 ms)`, snapshot de `ChunksInRadius((14,10), 1)` | La unidad aparece con `Movement != nil` —«debe traer su polilínea para que el cliente interpole»—, `Movement.Target == (20,10)` y **`X == 14`**: «la posición del snapshot es la autoritativa de ahora mismo», no la del inicio del movimiento |

S1 fija además que el snapshot **contiene el conjunto de interés y sólo ése**: es la verificación
funcional del interest management por chunk, con radio por defecto de 2 chunks.

### 4.9 Cruce de frontera de chunk (`transition_test.go`)

Una unidad que pasa del chunk A al B se anuncia a las sesiones que ganan o pierden el chunk con ella
([../architecture/networking.md](../architecture/networking.md) §4.3). El `recorder` captura cada transición
como dos mensajes de `Kind: "transition"`: el spawn, dirigido a quien observa el chunk nuevo y no el viejo, y
el despawn, a quien observa el viejo y no el nuevo; `PlayerID` es el jugador al que se limita, o `uuid.Nil`.
Quién está suscrito a qué lo prueba `hub_transition_test.go` (`internal/websocket`); el camino completo,
`TestUnaUnidadQueCruzaDeChunkApareceYDesapareceEnElAreaDeOtraSesion` (e2e).

| # | Test | Escenario | Aserción exacta |
|---|---|---|---|
| **T1** | `TestCruzarDeChunkAnunciaLaUnidadConSuMovimientoAQuienGanaElChunk` | Unidad en `(30,10)` hacia `(34,10)`: cruza `x = 32` en su segundo paso | El primer paso emite sólo `entity.update`. El segundo, **en este orden**: `entity.spawn` a `(1,0)` menos `(0,0)` con `status: MOVING`, `x = 32` y el `movement` activo con su `movementId`; `entity.despawn OUT_OF_INTEREST` a `(0,0)` menos `(1,0)`; y después el `entity.update`. Ninguna transición más hasta la llegada |
| **T2** | `TestDetenerseJustoTrasCruzarAnunciaLaUnidadYaParada` | `unit.cancel_move` en el tick en que la unidad acaba de cruzar: la fase 2 la detiene antes de que la fase 3 la mueva | `entity.spawn` con `status: IDLE` y sin `movement`, `entity.despawn`, y después el `unit.movement.cancelled` al chunk donde se detuvo |
| **T3** | `TestUnaUnidadOcultaQueCruzaDeChunkSoloSeAnunciaASuDueno` | Bosque a caballo de la frontera; una unidad `HIDDEN` apartada por la muralla de una ciudad nueva al chunk vecino, todavía dentro del bosque | Sigue `HIDDEN`; el spawn y el despawn de la transición llevan `PlayerID` del dueño; ningún mensaje a un chunk entero ni a terceros la menciona (`INV-SAFE-004`) |
| **T4** | `TestSalirDeLaGuarnicionAOtroChunkSoloAnunciaLaTransicionAlDueno` | Ciudad propia con la zona urbana a caballo de la frontera: entra desde `(33,10)` y sale a `(29,8)` | Secuencia exacta: transición limitada al dueño con la unidad ya `IDLE` y sin `garrisonCityId`, la respuesta al dueño, y el `entity.spawn` de la salida a los terceros de `(0,0)` excluyendo al dueño, una sola vez |
| **T5** | `TestUnaSalidaFallidaDevuelveLaUnidadASuChunkSoloParaElDueno` | Como T4 con la escritura de la salida fallando de forma permanente | Cuatro transiciones (ida y vuelta), todas limitadas al dueño; la de vuelta trae la unidad `GARRISONED` con su `garrisonCityId`; los terceros reciben exactamente `entity.spawn` y luego `entity.despawn GARRISONED` |

---

## 5. Casos ○ previstos sobre el loop y los eventos

Ninguno existe con este nombre (comprobado el 2026-10-21; cada fila dice si algo lo cubre en parte). Se listan
para que el mapa de cobertura sea completo y para que quien los escriba no tenga que redescubrir el escenario.

| # | Test | Escenario | Aserción exacta |
|---|---|---|---|
| **M8** | `TestOrdenExactoDeEventosAlReemplazar` | Reemplazo de orden como en §4.1 | Secuencia exacta de tipos emitidos en ese tick: `unit.movement.cancelled` (`reason: "REPLACED"`) → `unit.move.accepted` → `unit.movement.started`, y sus `seq` en ese orden **Estado (2026-10-21):** ○ sin test; el orden está en el código. |
| **M9** | `TestDosOrdenesEnElMismoTick` | Dos `unit.move` para la misma unidad encolados en el mismo tick | Se aplican en orden de llegada; queda un único movimiento activo, el último, y dos cancelados **Estado (2026-10-21):** ○ sin test; con la unidad `IDLE` saldría un único cancelado, no dos. |
| **M10** | `TestNuncaHayDosMovimientosActivos` (`INV-MOVE-001`) | 100 ticks con órdenes solapadas | En **cada** tick, el número de movimientos activos de esa unidad es 0 o 1, nunca 2. Se comprueba tick a tick, no sólo al final **Estado (2026-10-21):** ○ sin test; el estado guarda un movimiento por unidad, y el índice de la base lo prueba `TestIndiceUnicoImpideDosMovimientosActivos`. |
| **M11** | `TestUnMovimientoTerminalNuncaSeReactiva` (`INV-MOVE-007`) | Tras 100 ticks | Ningún movimiento `CANCELLED`, `COMPLETED` o `FAILED` vuelve a `ACTIVE` en ningún tick intermedio **Estado (2026-10-21):** ○ sin test. |
| **M12** | `TestCancelarSinMovimientoActivoNoEsError` | `unit.cancel_move` sobre una unidad `IDLE` | No es error: se responde con la última información conocida y el estado no cambia ([../specs/unit.md](../specs/unit.md) §7) **Estado (2026-10-21):** ○ sin test, y **difiere**: no responde nada. |
| **M13** | `TestCooldownSeLeeDeLaConfiguracion` | `ProtectionCooldown: 60 * time.Second` | La transición a protegida ocurre a los 60 s, no a los 300. Hace cumplir el canon §9: ningún valor de gameplay hardcodeado **Estado (2026-10-21):** ○ sin test: todos los tests usan 300 s. |
| **M14** | `TestLasTransicionesDePresenciaOcurrenEnLaFase5` | Tick instrumentado en los instantes de transición | Las transiciones de presencia ocurren en la fase 5 (temporizadores y eventos programados), no en otra **Estado (2026-10-21):** ○ sin test; `ProcessTimers` es la fase 5. |

---

## 6. Escenarios como datos y su *golden file* (`EO-115`)

Un escenario es un fichero que describe un mundo inicial y una secuencia de comandos con su tick. El test
`TestLosEscenariosReproducenSuGolden` (`internal/game/simulation/scenario_test.go`) ejecuta cada uno con el
harness real —`Loop.Step` con `FakeClock`, el A\* real y los dobles de §2.1— y compara el resultado con su
*golden file*. Sirve para tres cosas: fijar el comportamiento observable de la simulación de punta a punta,
de modo que cualquier cambio en lo que el servidor emite o guarda se vea en una revisión; demostrar que el
resultado es reproducible; y convertir un incidente en un test de regresión escribiendo su escenario.

### 6.1 Dónde viven

`services/game-server/internal/game/simulation/testdata/scenarios/`: `<nombre>.json` es el escenario y
`<nombre>.golden.json` su resultado esperado. Es la convención de Go —`go test` ejecuta cada paquete con
su directorio como directorio de trabajo, y `testdata/` no se compila—, y deja el escenario junto a la
simulación que ejercita.

### 6.2 Formato

```json
{
  "name": "trafico_mixto",
  "description": "Qué ejercita, en una frase.",
  "epochMs": 1757376000000,
  "runTicks": 900,
  "config": { "protectionCooldownSec": 20, "disconnectGraceSec": 5 },
  "world": {
    "width": 64, "height": 64, "terrain": "grassland",
    "paint": [ { "terrain": "FOREST", "minX": 30, "minY": 0, "maxX": 33, "maxY": 63 } ]
  },
  "safeZones": [ { "type": "DENSE_FOREST", "minX": 30, "minY": 0, "maxX": 33, "maxY": 63 } ],
  "players": [ { "ref": "p1", "city": { "x": 10, "y": 10 } } ],
  "units":   [ { "ref": "u1", "player": "p1", "x": 12, "y": 10 } ],
  "commands": [
    { "tick": 0,  "type": "unit.move",        "player": "p1", "unit": "u1", "target": { "x": 20, "y": 14 } },
    { "tick": 40, "type": "unit.cancel_move", "player": "p1", "unit": "u1" },
    { "tick": 60, "type": "disconnect",       "player": "p1" },
    { "tick": 500, "type": "connect",         "player": "p1" }
  ]
}
```

Reglas:

- **Ticks, no milisegundos.** El tick `k` (de 0 a `runTicks − 1`) se ejecuta en `epochMs + k × 100`. Los
  comandos de un tick entran en la cola justo antes de él, en el orden del fichero, y se aplican en ese
  mismo tick. Así un `unit.move` del tick 0 empieza exactamente en `epochMs`.
- **Referencias estables.** Jugadores y unidades se nombran con `ref`. El `playerId` es un UUID v5 derivado
  del `ref`, y las unidades y ciudades toman ids correlativos en el orden del fichero: el escenario no
  depende de qué asigne una secuencia. El *golden* escribe `player:<ref>` donde iría el UUID.
- **Mundo.** `terrain` es `grassland` (todo hierba) o `seeded` (`world.Generate` con `seed`, el generador
  real). `paint` pinta rectángulos de terreno encima, en orden. Cada jugador tiene una ciudad `ONLINE` en
  su `city`, con la población de sus unidades y, si el jugador declara `wood`, ese stock de madera inicial; las
  unidades son `VILLAGER` en reposo. Un tile de partida intransitable invalida el escenario.
- **Comandos.** `unit.move`, `unit.cancel_move`, `connect`, `disconnect` y, desde E1, `unit.gather` (unidad y
  `target`), `city.build` (una casa en el tile `target` de la ciudad del jugador) y `city.train` (un aldeano). Los
  de la economía escriben en un `EconomyStore` en memoria que acepta todo y da ids correlativos, de modo que sus
  respuestas vuelven por el buzón como en producción. El `requestId` de cada uno es determinista (su posición
  en la lista).
- **Configuración.** `protectionCooldownSec` y `disconnectGraceSec` acortan la presencia para que un
  escenario la recorra entera en pocos cientos de ticks; por omisión valen lo que en producción (300 y 30).
- Un escenario sin `runTicks`, con un `ref` repetido o desconocido, o con un comando fuera de
  `[0, runTicks)` no es válido, y el test falla nombrando el problema antes de ejecutar nada.

### 6.3 Qué fija el *golden*

Un JSON con tres secciones, todas en orden determinista:

| Sección | Contenido |
|---|---|
| `emitted` | Cada mensaje que la simulación emitió, con su tick, su destino (`chunk`, `chunks`, `chunk-except` o `player`), su tipo, su `requestId` y su payload completo |
| `persisted` | El nombre de cada trabajo durable encolado, en orden |
| `final` | Cada unidad (posición y estado), cada movimiento aún activo y la presencia de cada ciudad al terminar; en los escenarios que usan la economía, además `economy` con la madera, la población y el límite de cada ciudad, las tareas vivas y los edificios (la clave no aparece en los demás, cuyo *golden* no cambia) |

Además, el test ejecuta cada escenario **dos veces** y exige el mismo resultado byte a byte antes de
compararlo con el *golden*: si la simulación dejara de ser determinista, el fallo diría eso y no «el
*golden* cambió».

### 6.4 Cuando el *golden* no coincide

Un *golden* que deja de coincidir no es necesariamente un error: puede ser un cambio de comportamiento
buscado. Se regenera con

```
go test ./internal/game/simulation -run TestLosEscenariosReproducenSuGolden -update-golden
```

y **el diff del *golden* se revisa en el commit como cualquier otro cambio de código**: es la descripción
exacta de qué hace ahora distinto la simulación. Regenerarlo sin leer el diff anula el test.

### 6.5 Escenarios existentes

| Escenario | Qué ejercita |
|---|---|
| `trafico_mixto` | Dos jugadores en hierba con un bosque seguro y una montaña: movimientos simultáneos, un reemplazo a mitad de camino, una cancelación, rechazos (destino en montaña, unidad ajena), entrada y salida de una Safe Zone, y el ciclo de presencia completo (`ONLINE → OFFLINE_PENDING → PROTECTED → ONLINE`) |
| `madera` | SPEC-ECON-WOOD: un aldeano a diez tiles de un bosque deposita en los ticks 224, 448, 671 y 895 (la línea temporal anclada a los instantes de llegada); otro empieza a recolectar y se le cancela con un `unit.move` a mitad de tramo; la ciudad entrena un aldeano con su madera inicial (nace en el tick 202) y construye una casa con la recolectada (completa en el 830, límite 25); el dueño se desconecta en el 700 y, con la ciudad `OFFLINE_PENDING` desde el 750, el viaje en curso termina con un depósito más y la unidad `IDLE` (`INV-ECON-006`) |

---

## 7. Qué habilita este nivel a futuro

El diseño que hace posibles estos tests es el mismo que hace posibles dos capacidades que **no están en el MVP** y que conviene no perder por el camino.

**Replays.** El estado del mundo en el tick `n` es una función pura de `(EO_WORLD_SEED, world_state.epoch_ms, log ordenado de comandos)`. Un test de simulación **ya es** un replay: carga un guion y lo ejecuta. Persistir el log de comandos con su tick convertiría cualquier ventana de tiempo del servidor real en un escenario ejecutable. La herramienta que graba, almacena y reproduce ese log es **TBD (fuera de MVP)**; la propiedad de la que depende —determinismo verificado— se construye ahora, porque añadirla después obligaría a rediseñar el dominio.

**Depuración de incidentes.** El ciclo objetivo ante un bug de gameplay reportado en producción es:

```
1. Se reporta: "mi aldeano llegó a un tile que no era el que ordené".
2. Se toma el estado inicial y el log de comandos de la ventana afectada.
3. Se convierte en un fichero de internal/game/simulation/testdata/scenarios/ (§6).
4. Se ejecuta como test de simulación: reproduce el bug de forma determinista,
   en milisegundos y sin infraestructura.
5. Se arregla; el escenario queda en la suite como test de regresión permanente.
```

El paso 4 solo es posible si el determinismo es real. Ese es el verdadero producto de este nivel de test: no la lista de aserciones, sino la capacidad de convertir un incidente en un test reproducible. Los pasos 2 y 3 dependen de la herramienta de replay y por tanto son **TBD (fuera de MVP)**; los pasos 1, 4 y 5 ya funcionan hoy escribiendo el escenario a mano.

**Lo que este nivel no hará nunca.** No sustituye a `recovery`: un replay reconstruye desde el log, mientras que la recuperación reconstruye desde el estado durable de PostgreSQL. Son garantías distintas y ambas hacen falta ([integration-tests.md](./integration-tests.md) §5.6).

---

## 8. Ejecución

**No existe un script `test:simulation`.** Este nivel vive dentro de `go test ./...` y se ejecuta con
el mismo script que el resto del lado Go:

```powershell
pnpm run server:test
```

Y para trabajar sólo sobre este paquete, o sobre un test concreto:

```powershell
cd services/game-server; go test -race -count=1 ./internal/game/simulation
cd services/game-server; go test -run TestVerticalSliceMovimiento ./internal/game/simulation
```

**`-race` necesita un compilador de C.** El detector de carreras se apoya en cgo, así que sin gcc en el
`PATH` el primer comando aborta con `-race requires cgo` en lugar de ejecutar nada — que es lo que
ocurre en Windows. La vía verificada es ejecutarlo desde **WSL**; el procedimiento está en
[local-development.md](../operations/local-development.md).

Quitar `-race` para que el comando «funcione» deja los tests pasando y deja de comprobar lo único que
este paquete promete sobre concurrencia. Si lo quitas, que sea a sabiendas.

Sin Docker y sin `EO_INTEGRATION=1`. La suite completa debe terminar en menos de 30 s: incluso los
escenarios de miles de ticks son aritmética entera sobre estructuras en memoria y no esperan nada. Si
algún test de este nivel empieza a tardar segundos, o bien está haciendo I/O que no debería, o bien
pertenece a otro nivel.

En la ejecución nocturna se añade `-shuffle=on` y `-count=10` sobre el `-race` que ya lleva la CI
([strategy.md](./strategy.md) §8.2): la reproducibilidad es una propiedad estadísticamente
verificable y ahí es donde se verifica.

---

## 9. Documentos relacionados

- [strategy.md](./strategy.md) — reglas duras, sobre todo R3 (nada de `time.Now()`) y R4 (nada de iterar mapas).
- [unit-tests.md](./unit-tests.md) — las funciones puras que este nivel encadena, incluido el ejemplo numérico canónico `[0, 600, 1449, 2409, 2769]`.
- [integration-tests.md](./integration-tests.md) — persistencia y recuperación durable tras crash, ejecutada en la CI contra PostgreSQL y Redis reales.
- [contract-tests.md](./contract-tests.md) — forma de los mensajes que aquí se cuentan y se ordenan.
- [../architecture/game-loop.md](../architecture/game-loop.md) — las ocho fases del tick y su orden fijo.
- [../architecture/game-server.md](../architecture/game-server.md) — `Clock`, `RandomSource` y el resto de puertos inyectados.
- [../specs/movement.md](../specs/movement.md) — polilínea temporizada, reemplazo y cancelación.
- [../specs/presence.md](../specs/presence.md) — máquina `ONLINE → OFFLINE_PENDING → PROTECTED`.
