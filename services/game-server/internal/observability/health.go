package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

// Checker es una dependencia cuya salud se comprueba en /ready.
type Checker interface {
	Ping(ctx context.Context) error
}

// Health expone los endpoints de salud.
//
// La distinción entre los dos endpoints es deliberada y operativamente importante:
//
//	/health  (liveness)  — ¿el proceso está vivo? No toca dependencias. Si esto
//	                       falla, hay que reiniciar el proceso.
//	/ready   (readiness) — ¿puede atender tráfico? Comprueba PostgreSQL, Redis y
//	                       que el game loop siga latiendo. Si esto falla, hay que
//	                       sacar el nodo del balanceador, NO reiniciarlo.
//
// Confundirlas provoca reinicios en cadena justo cuando la base de datos va lenta,
// que es exactamente el peor momento para reiniciar nada.
type Health struct {
	deps      map[string]Checker
	loopBeat  atomic.Int64
	loopStale time.Duration
	now       func() time.Time
}

// NewHealth crea el servicio de salud. `loopStale` es el silencio máximo del game
// loop antes de considerarlo colgado.
func NewHealth(loopStale time.Duration, now func() time.Time) *Health {
	h := &Health{
		deps:      make(map[string]Checker),
		loopStale: loopStale,
		now:       now,
	}
	h.loopBeat.Store(now().UnixMilli())
	return h
}

// Register añade una dependencia a la comprobación de readiness.
func (h *Health) Register(name string, c Checker) { h.deps[name] = c }

// BeatLoop lo llama el game loop en cada tick para demostrar que sigue vivo.
func (h *Health) BeatLoop(nowMs int64) { h.loopBeat.Store(nowMs) }

// LivenessHandler responde a /health.
func (h *Health) LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	}
}

// ReadinessHandler responde a /ready.
func (h *Health) ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		checks := make(map[string]string, len(h.deps)+1)
		healthy := true

		for name, dep := range h.deps {
			if err := dep.Ping(ctx); err != nil {
				checks[name] = "error: " + err.Error()
				healthy = false
				continue
			}
			checks[name] = "ok"
		}

		age := h.now().UnixMilli() - h.loopBeat.Load()
		if age > h.loopStale.Milliseconds() {
			checks["game_loop"] = "stale"
			healthy = false
		} else {
			checks["game_loop"] = "ok"
		}

		status := http.StatusOK
		state := "ready"
		if !healthy {
			status = http.StatusServiceUnavailable
			state = "not_ready"
		}
		writeJSON(w, status, map[string]any{"status": state, "checks": checks})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
