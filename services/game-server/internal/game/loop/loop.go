// Package loop implementa el game loop autoritativo.
//
// Es el único lugar donde se muta el estado del mundo. Todo lo demás —conexiones
// WebSocket, HTTP, workers de persistencia— habla con él por canales.
// Ver ../../../../docs/architecture/game-loop.md
package loop

import (
	"context"
	"log/slog"
	"time"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
)

// Config parametriza el loop.
type Config struct {
	// TickDuration es el período de un tick (100 ms a 10 Hz).
	TickDuration time.Duration
	// FlushIntervalTicks es cada cuántos ticks se vuelca el estado sucio.
	FlushIntervalTicks int
	// MaxCommandsPerTick acota el trabajo de un tick: una avalancha de comandos
	// no puede convertir un tick en un bloqueo. Lo que no entra, espera al siguiente.
	MaxCommandsPerTick int
	// EpochMs es el origen temporal de la simulación.
	EpochMs int64
	// StartTick es el tick desde el que se reanuda tras un reinicio.
	StartTick uint64
}

// Loop ejecuta la simulación a ritmo fijo.
type Loop struct {
	sim      *simulation.Simulation
	clk      clock.Clock
	commands <-chan simulation.Command
	cfg      Config
	metrics  *observability.Metrics
	health   *observability.Health
	log      *slog.Logger

	tick uint64
}

// New crea el loop.
func New(
	sim *simulation.Simulation,
	clk clock.Clock,
	commands <-chan simulation.Command,
	cfg Config,
	metrics *observability.Metrics,
	health *observability.Health,
	log *slog.Logger,
) *Loop {
	if cfg.MaxCommandsPerTick <= 0 {
		cfg.MaxCommandsPerTick = 1024
	}
	if cfg.FlushIntervalTicks <= 0 {
		cfg.FlushIntervalTicks = 50
	}
	l := &Loop{
		sim: sim, clk: clk, commands: commands, cfg: cfg,
		metrics: metrics, health: health, log: log,
		tick: cfg.StartTick,
	}
	sim.State().SetTick(cfg.StartTick)
	return l
}

// Tick devuelve el número de tick actual.
func (l *Loop) Tick() uint64 { return l.tick }

// Run ejecuta el loop hasta que se cancele el contexto.
//
// La cadencia se calcula sobre TIEMPO ABSOLUTO, no acumulando esperas: si un tick
// se pasa de tiempo, el siguiente no se retrasa en cascada. Y si el proceso se
// queda parado (una pausa de GC larga, la máquina suspendida), los ticks perdidos
// se SALTAN en lugar de ejecutarse a toda velocidad para "ponerse al día" —
// correr en espiral es la forma más rápida de convertir un hipo en una caída.
func (l *Loop) Run(ctx context.Context) error {
	period := l.cfg.TickDuration
	start := l.clk.Now()
	l.log.Info("game loop iniciado",
		"tick_duration_ms", period.Milliseconds(),
		"start_tick", l.tick,
		"flush_interval_ticks", l.cfg.FlushIntervalTicks)

	timer := time.NewTimer(period)
	defer timer.Stop()

	var scheduled int64 = 1

	for {
		select {
		case <-ctx.Done():
			l.log.Info("game loop detenido", "tick", l.tick)
			return ctx.Err()
		case <-timer.C:
		}

		l.Step(l.clk.NowMs())

		// Siguiente vencimiento en tiempo absoluto.
		elapsed := l.clk.Now().Sub(start)
		scheduled++
		next := time.Duration(scheduled) * period
		if next <= elapsed {
			// Vamos por detrás: se descartan los ticks perdidos y se reancla el
			// calendario al presente.
			missed := int64(elapsed/period) - scheduled + 1
			if missed > 0 {
				scheduled += missed
				l.log.Warn("ticks perdidos descartados", "missed", missed, "tick", l.tick)
			}
			next = time.Duration(scheduled) * period
			if next <= elapsed {
				next = elapsed + period
			}
		}
		timer.Reset(next - elapsed)
	}
}

// Step ejecuta UN tick completo en el instante indicado.
//
// Está separado de Run a propósito: los tests de simulación lo invocan
// directamente con un FakeClock para avanzar el mundo de forma determinista, sin
// depender de temporizadores reales ni de la velocidad de la máquina.
// Ver ../../../../docs/testing/simulation-tests.md
func (l *Loop) Step(nowMs int64) {
	started := time.Now()

	l.tick++
	l.sim.State().SetTick(l.tick)

	// Fase 1-2: consumir y aplicar comandos.
	l.drainCommands()

	// Fase 3: avanzar el movimiento.
	l.sim.AdvanceMovements(nowMs)

	// Fase 4: resolver simulación (combate — fuera del MVP).

	// Fase 5: temporizadores y eventos programados.
	l.sim.ProcessTimers(time.UnixMilli(nowMs).UTC())

	// Fase 6-7: el estado del mundo y los deltas ya se emitieron dentro de las
	// fases anteriores, según ocurrían los hechos.

	// Fase 8: encolar persistencia. Nunca I/O síncrono aquí.
	if l.cfg.FlushIntervalTicks > 0 && l.tick%uint64(l.cfg.FlushIntervalTicks) == 0 {
		l.sim.FlushDirty()
	}

	l.observe(started, nowMs)
}

// drainCommands consume la cola sin bloquearse jamás.
func (l *Loop) drainCommands() {
	for i := 0; i < l.cfg.MaxCommandsPerTick; i++ {
		select {
		case cmd, ok := <-l.commands:
			if !ok {
				return
			}
			l.applyCommand(cmd)
		default:
			return
		}
	}
	l.log.Warn("límite de comandos por tick alcanzado; el resto espera al siguiente",
		"limit", l.cfg.MaxCommandsPerTick, "tick", l.tick)
}

// applyCommand aísla el pánico de un comando concreto.
//
// Un comando malformado o un bug en una regla no puede tumbar la simulación del
// mundo entero: se registra, se cuenta, y el tick continúa.
func (l *Loop) applyCommand(cmd simulation.Command) {
	defer func() {
		if p := recover(); p != nil {
			l.log.Error("pánico al aplicar un comando",
				"panic", p, "tick", l.tick)
			if l.metrics != nil {
				l.metrics.CommandsTotal.WithLabelValues("unknown", observability.ResultFailed).Inc()
			}
		}
	}()
	l.sim.Apply(cmd)
	if l.metrics != nil {
		l.metrics.CommandsTotal.WithLabelValues(commandLabel(cmd), observability.ResultAccepted).Inc()
	}
}

func (l *Loop) observe(started time.Time, nowMs int64) {
	elapsed := time.Since(started)

	if l.health != nil {
		l.health.BeatLoop(nowMs)
	}
	if l.metrics == nil {
		return
	}

	l.metrics.TickDuration.Observe(elapsed.Seconds())
	if elapsed > l.cfg.TickDuration {
		l.metrics.TickOverruns.Inc()
	}
	l.metrics.ActiveUnits.Set(float64(l.sim.State().UnitCount()))
	l.metrics.ActiveMovements.Set(float64(l.sim.State().MovementCount()))

	if calls := l.sim.LastPathfindingCalls; calls > 0 {
		l.metrics.PathfindingRequests.WithLabelValues(observability.ResultFound).Add(float64(calls))
		l.metrics.PathfindingDuration.Observe(l.sim.LastPathfindingTime.Seconds())
		l.sim.LastPathfindingCalls = 0
		l.sim.LastPathfindingTime = 0
	}
}

func commandLabel(cmd simulation.Command) string {
	switch cmd.(type) {
	case simulation.MoveUnit:
		return "unit.move"
	case simulation.CancelMovement:
		return "unit.cancel_move"
	case simulation.PlayerConnected:
		return "player.connected"
	case simulation.PlayerDisconnected:
		return "player.disconnected"
	default:
		return "unknown"
	}
}
