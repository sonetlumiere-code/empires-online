package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
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

	PathfindingRequests *prometheus.CounterVec
	PathfindingDuration prometheus.Histogram

	DatabaseLatency prometheus.Histogram
	RedisLatency    prometheus.Histogram
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
	}

	reg.MustRegister(
		m.ConnectedPlayers, m.ConnectedWebsockets, m.ActiveUnits, m.ActiveMovements,
		m.PersistenceQueue, m.TickDuration, m.TickOverruns, m.CommandsTotal,
		m.WSMessages, m.ProtocolErrors, m.PathfindingRequests, m.PathfindingDuration,
		m.DatabaseLatency, m.RedisLatency,
	)
	return m
}

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
)
