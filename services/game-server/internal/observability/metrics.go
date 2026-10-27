package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
)

// Metrics agrupa los instrumentos del servidor.
//
// Se registran en un registro propio, no en el global: así los tests pueden
// construir un juego de métricas limpio sin colisionar entre sí.
type Metrics struct {
	registry *prometheus.Registry

	ConnectedPlayers    prometheus.Gauge
	ConnectedWebsockets prometheus.Gauge
	ActiveUnits         prometheus.Gauge
	ActiveMovements     prometheus.Gauge
	PersistenceQueue    prometheus.Gauge

	TickDuration   prometheus.Histogram
	TickOverruns   prometheus.Counter
	CommandsTotal  *prometheus.CounterVec
	WSMessages     *prometheus.CounterVec
	ProtocolErrors *prometheus.CounterVec
	// WSConnectionsRejected cuenta los upgrades de /ws rechazados antes de
	// abrir socket, por motivo (ADR-021).
	WSConnectionsRejected *prometheus.CounterVec
	// CommandsDropped cuenta los comandos que no cupieron ni en el canal del
	// loop ni en su buzón, por tipo (ADR-022). Cualquier valor > 0 es un
	// incidente: ese comando no se aplicó nunca.
	CommandsDropped *prometheus.CounterVec

	PathfindingRequests *prometheus.CounterVec
	PathfindingDuration prometheus.Histogram

	DatabaseLatency prometheus.Histogram
	RedisLatency    prometheus.Histogram
	// PersistenceJobsDropped cuenta las escrituras durables que la cola
	// descartó, por trabajo y motivo (ADR-022). Cualquier valor > 0 es un
	// incidente: una escritura descartada es una escritura perdida salvo que
	// su compensación la vuelva a encolar.
	PersistenceJobsDropped *prometheus.CounterVec
}

// NewMetrics construye y registra todos los instrumentos.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	m := &Metrics{
		registry: reg,

		ConnectedPlayers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eo_connected_players",
			Help: "Jugadores distintos con al menos una sesión autenticada.",
		}),
		ConnectedWebsockets: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eo_connected_websockets",
			Help: "Conexiones WebSocket abiertas.",
		}),
		ActiveUnits: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eo_active_units",
			Help: "Unidades vivas en la simulación.",
		}),
		ActiveMovements: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eo_active_movements",
			Help: "Movimientos en curso.",
		}),
		PersistenceQueue: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eo_persistence_queue_depth",
			Help: "Trabajos encolados a la espera de escribirse en PostgreSQL.",
		}),

		TickDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "eo_game_tick_duration_seconds",
			Help: "Duración de un tick del game loop.",
			// Cubetas centradas en el presupuesto de 100 ms de un tick a 10 Hz.
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		}),
		TickOverruns: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "eo_game_tick_overruns_total",
			Help: "Ticks cuya duración superó su período. Sostenido, significa que el servidor va por detrás del mundo.",
		}),
		CommandsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eo_commands_total",
			Help: "Comandos procesados por tipo y resultado.",
		}, []string{"type", "result"}),
		WSMessages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eo_ws_messages_total",
			Help: "Mensajes WebSocket por dirección y tipo.",
		}, []string{"direction", "type"}),
		ProtocolErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eo_protocol_errors_total",
			Help: "Errores de protocolo emitidos, por código estable.",
		}, []string{"code"}),
		WSConnectionsRejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eo_ws_connections_rejected_total",
			Help: "Upgrades de /ws rechazados antes de abrir socket, por motivo: origin, max_connections o max_connections_per_ip.",
		}, []string{"reason"}),
		CommandsDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eo_commands_dropped_total",
			Help: "Comandos que no cupieron en el canal del game loop ni en su buzón y no se aplicaron nunca, por tipo.",
		}, []string{"type"}),

		PathfindingRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eo_pathfinding_requests_total",
			Help: "Consultas de pathfinding por resultado.",
		}, []string{"result"}),
		PathfindingDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "eo_pathfinding_duration_seconds",
			Help:    "Duración de una consulta de pathfinding.",
			Buckets: []float64{0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.05, 0.1},
		}),

		DatabaseLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "eo_database_latency_seconds",
			Help:    "Latencia de las operaciones contra PostgreSQL.",
			Buckets: prometheus.DefBuckets,
		}),
		RedisLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "eo_redis_latency_seconds",
			Help:    "Latencia de las operaciones contra Redis.",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5},
		}),
		PersistenceJobsDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eo_persistence_jobs_dropped_total",
			Help: "Escrituras durables descartadas por la cola de persistencia, por trabajo y motivo: queue_full o attempts_exhausted.",
		}, []string{"job", "reason"}),
	}

	reg.MustRegister(
		m.ConnectedPlayers, m.ConnectedWebsockets, m.ActiveUnits, m.ActiveMovements,
		m.PersistenceQueue, m.TickDuration, m.TickOverruns, m.CommandsTotal,
		m.WSMessages, m.ProtocolErrors, m.WSConnectionsRejected, m.CommandsDropped,
		m.PathfindingRequests, m.PathfindingDuration,
		m.DatabaseLatency, m.RedisLatency, m.PersistenceJobsDropped,
	)
	// Las series de rechazo y de descarte existen a cero desde el arranque: una
	// alerta sobre `rate()` de una serie que aún no existe no dispara nunca.
	for _, reason := range []string{RejectOrigin, RejectMaxConnections, RejectMaxConnectionsPerIP} {
		m.WSConnectionsRejected.WithLabelValues(reason)
	}
	for _, job := range append(simulation.JobNames(), JobUnknown) {
		for _, reason := range []string{DropQueueFull, DropAttemptsExhausted} {
			m.PersistenceJobsDropped.WithLabelValues(job, reason)
		}
	}
	for _, t := range simulation.CommandTypes() {
		m.CommandsDropped.WithLabelValues(t)
	}
	return m
}

// PersistenceJobLabel acota la etiqueta `job` de eo_persistence_jobs_dropped_total
// al catálogo cerrado de simulation.JobNames: cualquier otro nombre cuenta como
// `unknown`. El nombre lo escribe el código, no el cliente, pero una errata no
// debe crear una serie nueva en silencio.
func PersistenceJobLabel(name string) string {
	if _, ok := knownJobs[name]; ok {
		return name
	}
	return JobUnknown
}

var knownJobs = func() map[string]struct{} {
	out := make(map[string]struct{})
	for _, name := range simulation.JobNames() {
		out[name] = struct{}{}
	}
	return out
}()

// Registry expone el registro para el handler de /metrics.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// Resultados usados como etiqueta en los contadores.
const (
	ResultAccepted = "accepted"
	ResultRejected = "rejected"
	ResultFailed   = "failed"
	ResultFound    = "found"
	ResultNotFound = "not_found"

	DirectionInbound  = "inbound"
	DirectionOutbound = "outbound"

	// TypeUnknown es la etiqueta `type` de un mensaje entrante cuyo tipo no
	// está en el catálogo del protocolo. El tipo lo escribe el cliente: usarlo
	// tal cual crearía una serie por cada tipo inventado (EO-121).
	TypeUnknown = "unknown"

	// Motivos de eo_ws_connections_rejected_total{reason} (ADR-021). Catálogo
	// cerrado: ninguno lo elige el cliente.
	RejectOrigin              = "origin"
	RejectMaxConnections      = "max_connections"
	RejectMaxConnectionsPerIP = "max_connections_per_ip"

	// Motivos de eo_persistence_jobs_dropped_total{reason} (ADR-022). Catálogo
	// cerrado: la partición de la cola estaba llena al encolar, o el trabajo
	// agotó sus intentos en el worker.
	DropQueueFull         = "queue_full"
	DropAttemptsExhausted = "attempts_exhausted"
	// JobUnknown es la etiqueta `job` de un trabajo cuyo nombre no está en
	// simulation.JobNames.
	JobUnknown = "unknown"
)
