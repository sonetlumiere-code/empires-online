package persistence

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
)

// Queue ejecuta escrituras durables fuera del game loop.
//
// Es la pieza que hace cumplible la regla "el tick nunca hace I/O de PostgreSQL".
// El loop encola y sigue simulando; unos workers escriben a su ritmo.
//
// Orden: cada worker tiene su propio canal, y un trabajo va al worker de su
// clave (simulation.PartitionOf). Así los trabajos de una misma entidad se
// ejecutan en el orden en que se encolaron, reintentos incluidos, y los de
// entidades distintas en paralelo (ADR-013 §2.2). Los trabajos sin clave se
// reparten por turnos.
//
// Contrapresión: si PostgreSQL se ralentiza, la cola crece y la métrica
// eo_persistence_queue_depth lo delata. Si una partición se llena, el trabajo se
// descarta antes que bloquear el mundo — y se registra como error, porque
// descartar escrituras nunca es normal.
type Queue struct {
	parts   []chan simulation.Job
	log     *slog.Logger
	metrics *observability.Metrics

	maxAttempts int
	retryDelay  time.Duration
	turno       atomic.Uint64

	wg     sync.WaitGroup
	closed sync.Once
}

// QueueConfig parametriza la cola.
type QueueConfig struct {
	// Capacity es la capacidad TOTAL; se reparte a partes iguales entre workers.
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
	porParticion := cfg.Capacity / cfg.Workers
	if porParticion < 1 {
		porParticion = 1
	}
	parts := make([]chan simulation.Job, cfg.Workers)
	for i := range parts {
		parts[i] = make(chan simulation.Job, porParticion)
	}
	return &Queue{
		parts:       parts,
		log:         log,
		metrics:     metrics,
		maxAttempts: cfg.MaxAttempts,
		retryDelay:  cfg.RetryDelay,
	}
}

// Start arranca un worker por partición.
func (q *Queue) Start(ctx context.Context) {
	for _, part := range q.parts {
		q.wg.Add(1)
		go q.worker(ctx, part)
	}
}

// Partitions es el número de particiones ordenadas.
func (q *Queue) Partitions() int { return len(q.parts) }

// Submit encola un trabajo. NUNCA bloquea: bloquear aquí sería bloquear el tick.
func (q *Queue) Submit(job simulation.Job) {
	var i int
	if job.Key != "" {
		i = simulation.PartitionOf(job.Key, len(q.parts))
	} else {
		i = int(q.turno.Add(1) % uint64(len(q.parts)))
	}
	select {
	case q.parts[i] <- job:
		q.observeDepth()
	default:
		// Partición llena: se descarta y se grita. Que esto aparezca en los logs
		// significa que la base de datos no sigue el ritmo del mundo.
		q.log.Error("cola de persistencia saturada: trabajo descartado",
			"job", job.Name, "key", job.Key, "partition", i, "capacity", cap(q.parts[i]))
		if job.OnPermanentFailure != nil {
			job.OnPermanentFailure(context.DeadlineExceeded)
		}
	}
}

// Depth es la profundidad actual de la cola, sumando todas las particiones.
func (q *Queue) Depth() int {
	total := 0
	for _, part := range q.parts {
		total += len(part)
	}
	return total
}

func (q *Queue) observeDepth() {
	if q.metrics != nil {
		q.metrics.PersistenceQueue.Set(float64(q.Depth()))
	}
}

// Drain espera a que se vacíen los canales o a que expire el plazo. No ve el
// trabajo que un worker ya está ejecutando: para eso está Close.
// Se usa en el apagado ordenado: no se cierra el proceso con escrituras pendientes.
func (q *Queue) Drain(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if q.Depth() == 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return q.Depth() == 0
}

// Close cierra la cola y espera a que los workers terminen lo pendiente.
func (q *Queue) Close() {
	q.closed.Do(func() {
		for _, part := range q.parts {
			close(part)
		}
	})
	q.wg.Wait()
}

func (q *Queue) worker(ctx context.Context, part <-chan simulation.Job) {
	defer q.wg.Done()

	for job := range part {
		q.observeDepth()
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
			"job", job.Name, "key", job.Key, "attempt", attempt, "max_attempts", q.maxAttempts, "err", err)

		if attempt < q.maxAttempts {
			time.Sleep(q.retryDelay * time.Duration(attempt))
		}
	}

	q.log.Error("escritura durable descartada tras agotar los reintentos",
		"job", job.Name, "key", job.Key, "err", lastErr)
	if job.OnPermanentFailure != nil {
		job.OnPermanentFailure(lastErr)
	}
}

// Comprobación del contrato con la simulación.
var _ simulation.Persister = (*Queue)(nil)
