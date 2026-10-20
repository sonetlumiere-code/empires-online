package loop_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/empires-online/empires-online/services/game-server/internal/game/loop"
)

// ADR-015 / INV-WORLD-004: el tick de arranque es una cota superior de todo
// tick ya emitido, sin depender de que el último apagado fuera ordenado.
func TestElTickSeReanclaAlRelojAlArrancar(t *testing.T) {
	const epoch int64 = 1_757_376_000_000
	const periodo = 100 * time.Millisecond

	t.Run("tras una caída manda el reloj, no lo último guardado", func(t *testing.T) {
		// El último apagado ordenado guardó el tick 500; después el servidor
		// corrió una hora más y cayó. El reloj dice 36 500 períodos.
		tick, atrasado := loop.ResumeTick(500, epoch, epoch+3_650_000, periodo)
		assert.EqualValues(t, 36_500, tick)
		assert.False(t, atrasado)
	})

	t.Run("con el reloj por detrás de lo guardado, manda lo guardado y se señala", func(t *testing.T) {
		tick, atrasado := loop.ResumeTick(36_500, epoch, epoch+3_000_000, periodo)
		assert.EqualValues(t, 36_500, tick)
		assert.True(t, atrasado)
	})

	t.Run("un mundo recién creado arranca en cero", func(t *testing.T) {
		tick, atrasado := loop.ResumeTick(0, epoch, epoch, periodo)
		assert.Zero(t, tick)
		assert.False(t, atrasado)
	})

	t.Run("un reloj anterior al epoch no da un tick negativo", func(t *testing.T) {
		tick, atrasado := loop.ResumeTick(0, epoch, epoch-5000, periodo)
		assert.Zero(t, tick)
		assert.False(t, atrasado)
	})

	t.Run("la cota se sostiene: nada ejecutado antes la alcanza", func(t *testing.T) {
		// Un proceso que arrancó en t0 y ejecutó como mucho un tick por período
		// hasta caer en t1 nunca pasó de ResumeTick(·, t1). El siguiente arranque,
		// en t2 > t1, empieza estrictamente por encima.
		arranque, _ := loop.ResumeTick(0, epoch, epoch+10_000, periodo)
		ejecutados := uint64(50_000 / periodo.Milliseconds()) // 50 s de ticks
		ultimo := arranque + ejecutados
		siguiente, _ := loop.ResumeTick(arranque, epoch, epoch+10_000+50_000+2_000, periodo)
		assert.Greater(t, siguiente, ultimo)
	})
}
