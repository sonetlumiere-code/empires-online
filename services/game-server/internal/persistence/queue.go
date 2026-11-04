package persistence

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
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
// Un trabajo con dos claves (Job.Key2, ADR-032 §3) entra en las dos
// particiones o en ninguna, y se ejecuta cuando los workers de las dos han
// llegado a él: así queda en orden respecto a los de las dos entidades. No hay
// bloqueo cruzado porque Submit es atómico (q.mu) y todos los trabajos de dos
// claves quedan en el mismo orden relativo en todas las particiones: el más
// antiguo sin terminar siempre tiene delante sólo trabajos ya terminados.
//
// Contrapresión: si PostgreSQL se ralentiza, la cola crece y la métrica
// eo_persistence_queue_depth lo delata. Si una partición se llena, el trabajo se
// descarta antes que bloquear el mundo — y se registra como error y se cuenta
// en eo_persistence_jobs_dropped_total, porque descartar escrituras nunca es
// normal (ADR-022).
type Queue struct {
	// mu hace atómico Submit (los dos envíos de un trabajo de dos claves) y lo
	// excluye de Close.
	mu      sync.Mutex
	parts   []chan queued
	log     *slog.Logger
	metrics *observability.Metrics

	maxAttempts int
	retryDelay  time.Duration
	turno       atomic.Uint64

	wg     sync.WaitGroup
	closed sync.Once
}

// queued es un trabajo en el canal de una partición. pair no es nil en las dos
// mitades de un trabajo de dos claves; sólo la que lleva lead lo ejecuta.
type queued struct {
	job  simulation.Job
	pair *pairBarrier
	lead bool
}

// pairBarrier es la cita de los dos workers de un trabajo de dos claves: cada
// uno llega, espera al otro, y el que no lo ejecuta espera a que termine.
type pairBarrier struct {
	arrived sync.WaitGroup
	done    chan struct{}
}

func newPairBarrier() *pairBarrier {
	b := &pairBarrier{done: make(chan struct{})}
	b.arrived.Add(2)
	return b
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
	parts := make([]chan queued, cfg.Workers)
	for i := range parts {
		parts[i] = make(chan queued, porParticion)
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
	q.mu.Lock()
	defer q.mu.Unlock()

	var i int
	if job.Key != "" {
		i = simulation.PartitionOf(job.Key, len(q.parts))
	} else {
		i = int(q.turno.Add(1) % uint64(len(q.parts)))
	}
	if job.Key2 != "" {
		if j := simulation.PartitionOf(job.Key2, len(q.parts)); j != i {
			q.submitPair(job, i, j)
			return
		}
	}
	select {
	case q.parts[i] <- queued{job: job}:
		q.observeDepth()
	default:
		q.dropFull(job, i)
	}
}

// submitPair encola las dos mitades de un trabajo de dos claves, o ninguna.
// Con q.mu tomado nadie más encola, y los workers sólo vacían: si caben las
// dos al comprobarlo, los dos envíos entran sin bloquear.
func (q *Queue) submitPair(job simulation.Job, i, j int) {
	if len(q.parts[i]) >= cap(q.parts[i]) {
		q.dropFull(job, i)
		return
	}
	if len(q.parts[j]) >= cap(q.parts[j]) {
		q.dropFull(job, j)
		return
	}
	b := newPairBarrier()
	q.parts[i] <- queued{job: job, pair: b, lead: true}
	q.parts[j] <- queued{job: job, pair: b}
	q.observeDepth()
}

// dropFull descarta un trabajo que no cabe en su partición y llama a su
// compensación.
func (q *Queue) dropFull(job simulation.Job, i int) {
	// Partición llena: se descarta y se grita. Que esto aparezca en los logs
	// significa que la base de datos no sigue el ritmo del mundo.
	q.log.Error("cola de persistencia saturada: trabajo descartado",
		"job", job.Name, "key", job.Key, "key2", job.Key2, "partition", i, "capacity", cap(q.parts[i]))
	q.countDropped(job.Name, observability.DropQueueFull)
	if job.OnPermanentFailure != nil {
		job.OnPermanentFailure(context.DeadlineExceeded)
	}
}

// countDropped cuenta una escritura descartada. Se cuenta ANTES de llamar a la
// compensación: la métrica dice que la cola la perdió, compense o no.
func (q *Queue) countDropped(name, reason string) {
	if q.metrics != nil {
		q.metrics.PersistenceJobsDropped.WithLabelValues(observability.PersistenceJobLabel(name), reason).Inc()
	}
}

// Depth es la profundidad actual de la cola, sumando todas las particiones. Un
// trabajo de dos claves cuenta dos veces: está en dos canales.
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
		q.mu.Lock()
		defer q.mu.Unlock()
		for _, part := range q.parts {
			close(part)
		}
	})
	q.wg.Wait()
}

func (q *Queue) worker(ctx context.Context, part <-chan queued) {
	defer q.wg.Done()

	for it := range part {
		q.observeDepth()
		if it.pair == nil {
			q.run(ctx, it.job)
			continue
		}
		// Un trabajo de dos claves: se espera al worker de la otra partición,
		// y sólo uno de los dos lo ejecuta (ADR-032 §3).
		it.pair.arrived.Done()
		it.pair.arrived.Wait()
		if it.lead {
			q.run(ctx, it.job)
			close(it.pair.done)
		} else {
			<-it.pair.done
		}
	}
}

func (q *Queue) run(ctx context.Context, job simulation.Job) {
	var lastErr error

	for attempt := 1; attempt <= q.maxAttempts; attempt++ {
		// El contexto de la escritura NO hereda la cancelación del proceso: si el
		// servidor se está apagando, la escritura pendiente debe terminar, no
		// abortarse a medias.
		// El plazo acota la transacción: el tope marcable del registro cuenta con
		// él (ADR-028 §6).
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postgres.WriteAttemptTimeout)
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
	q.countDropped(job.Name, observability.DropAttemptsExhausted)
	if job.OnPermanentFailure != nil {
		job.OnPermanentFailure(lastErr)
	}
}

// Comprobación del contrato con la simulación.
var _ simulation.Persister = (*Queue)(nil)
