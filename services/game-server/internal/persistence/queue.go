package persistence

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
)

// Queue ejecuta escrituras durables fuera del game loop.
//
// Es la pieza que hace cumplible la regla "el tick nunca hace I/O de PostgreSQL".
// El loop encola y sigue simulando; unos workers escriben a su ritmo.
//
// Contrapresión: si PostgreSQL se ralentiza, la cola crece y la métrica
// eo_persistence_queue_depth lo delata. Si llega a llenarse, se descarta el
// trabajo MENOS crítico antes que bloquear el mundo — y se registra como error,
// porque descartar escrituras nunca es normal.
type Queue struct {
	jobs    chan simulation.Job
	log     *slog.Logger
	metrics *observability.Metrics

	maxAttempts int
	retryDelay  time.Duration

	wg     sync.WaitGroup
	closed sync.Once
}

// QueueConfig parametriza la cola.
type QueueConfig struct {
	Capacity    int
	Workers     int
	MaxAttempts int
	RetryDelay  time.Duration
}

// DefaultQueueConfig son valores razonables para el MVP.
func DefaultQueueConfig() QueueConfig {
	return QueueConfig{Capacity: 4096, Workers: 4, MaxAttempts: 3, RetryDelay: 200 * time.Millisecond}
}

// NewQueue crea la cola de persistencia.
func NewQueue(cfg QueueConfig, log *slog.Logger, metrics *observability.Metrics) *Queue {
	if cfg.Capacity <= 0 {
		cfg.Capacity = 4096
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	return &Queue{
		jobs:        make(chan simulation.Job, cfg.Capacity),
		log:         log,
		metrics:     metrics,
		maxAttempts: cfg.MaxAttempts,
		retryDelay:  cfg.RetryDelay,
	}
}

// Start arranca los workers.
func (q *Queue) Start(ctx context.Context, workers int) {
	if workers <= 0 {
		workers = 4
	}
	for i := 0; i < workers; i++ {
		q.wg.Add(1)
		go q.worker(ctx)
	}
}

// Submit encola un trabajo. NUNCA bloquea: bloquear aquí sería bloquear el tick.
func (q *Queue) Submit(job simulation.Job) {
	select {
	case q.jobs <- job:
		if q.metrics != nil {
			q.metrics.PersistenceQueue.Set(float64(len(q.jobs)))
		}
	default:
		// Cola llena: se descarta y se grita. Que esto aparezca en los logs
		// significa que la base de datos no sigue el ritmo del mundo.
		q.log.Error("cola de persistencia saturada: trabajo descartado",
			"job", job.Name, "capacity", cap(q.jobs))
		if job.OnPermanentFailure != nil {
			job.OnPermanentFailure(context.DeadlineExceeded)
		}
	}
}

// Depth es la profundidad actual de la cola.
func (q *Queue) Depth() int { return len(q.jobs) }

// Drain espera a que se vacíe la cola o a que expire el plazo.
// Se usa en el apagado ordenado: no se cierra el proceso con escrituras pendientes.
func (q *Queue) Drain(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(q.jobs) == 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return len(q.jobs) == 0
}

// Close cierra la cola y espera a los workers.
func (q *Queue) Close() {
	q.closed.Do(func() { close(q.jobs) })
	q.wg.Wait()
}

func (q *Queue) worker(ctx context.Context) {
	defer q.wg.Done()

	for job := range q.jobs {
		if q.metrics != nil {
			q.metrics.PersistenceQueue.Set(float64(len(q.jobs)))
		}
		q.run(ctx, job)
	}
}

func (q *Queue) run(ctx context.Context, job simulation.Job) {
	var lastErr error

	for attempt := 1; attempt <= q.maxAttempts; attempt++ {
		// El contexto de la escritura NO hereda la cancelación del proceso: si el
		// servidor se está apagando, la escritura pendiente debe terminar, no
		// abortarse a medias.
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		start := time.Now()
		err := job.Run(runCtx)
		cancel()

		if q.metrics != nil {
			q.metrics.DatabaseLatency.Observe(time.Since(start).Seconds())
		}
		if err == nil {
			return
		}
		lastErr = err
		q.log.Warn("escritura durable fallida; se reintentará",
			"job", job.Name, "attempt", attempt, "max_attempts", q.maxAttempts, "err", err)

		if attempt < q.maxAttempts {
			time.Sleep(q.retryDelay * time.Duration(attempt))
		}
	}

	q.log.Error("escritura durable descartada tras agotar los reintentos",
		"job", job.Name, "err", lastErr)
	if job.OnPermanentFailure != nil {
		job.OnPermanentFailure(lastErr)
	}
}

// Comprobación del contrato con la simulación.
var _ simulation.Persister = (*Queue)(nil)
