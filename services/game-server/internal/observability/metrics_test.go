package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
)

// ADR-022: las series de descarte existen a cero desde el arranque, una por
// cada combinación del catálogo cerrado. Una alerta sobre `rate()` de una serie
// que aún no existe no dispara nunca, y estas alertas son «cualquier valor > 0».
func TestLasSeriesDeDescartesExistenACeroDesdeElArranque(t *testing.T) {
	m := NewMetrics()

	trabajos := len(simulation.JobNames()) + 1 // más `unknown`
	assert.Equal(t, trabajos*2, testutil.CollectAndCount(m.PersistenceJobsDropped),
		"una serie por trabajo del catálogo y por motivo")
	for _, job := range append(simulation.JobNames(), JobUnknown) {
		for _, reason := range []string{DropQueueFull, DropAttemptsExhausted} {
			assert.Zero(t, testutil.ToFloat64(m.PersistenceJobsDropped.WithLabelValues(job, reason)),
				"%s/%s empieza a cero", job, reason)
		}
	}

	assert.Equal(t, len(simulation.CommandTypes()), testutil.CollectAndCount(m.CommandsDropped),
		"una serie por tipo de comando")
	for _, tipo := range simulation.CommandTypes() {
		assert.Zero(t, testutil.ToFloat64(m.CommandsDropped.WithLabelValues(tipo)), "%s empieza a cero", tipo)
	}
}

// La etiqueta `job` está acotada al catálogo: un nombre que no está en él
// cuenta como `unknown` en lugar de crear una serie nueva.
func TestUnNombreDeTrabajoFueraDelCatalogoCuentaComoUnknown(t *testing.T) {
	for _, name := range simulation.JobNames() {
		require.Equal(t, name, PersistenceJobLabel(name))
	}
	assert.Equal(t, JobUnknown, PersistenceJobLabel("inventado"))
	assert.Equal(t, JobUnknown, PersistenceJobLabel(""))
}

// SPEC-MESSAGES §6: las series de rechazo de mensajes existen a cero desde el
// arranque, una por motivo del catálogo cerrado.
func TestLasSeriesDeRechazoDeMensajesExistenACero(t *testing.T) {
	m := NewMetrics()
	assert.Equal(t, len(MessageRejectReasons()), testutil.CollectAndCount(m.MessagesRejected))
	for _, reason := range MessageRejectReasons() {
		assert.Zero(t, testutil.ToFloat64(m.MessagesRejected.WithLabelValues(reason)), reason)
	}
	assert.Zero(t, testutil.ToFloat64(m.MessagesSent))
}
