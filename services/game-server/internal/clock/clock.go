// Package clock aísla al dominio del reloj del sistema y de la aleatoriedad.
//
// Ninguna lógica de dominio puede llamar a time.Now() ni a math/rand directamente.
// Esa disciplina es lo que hace posible que la simulación sea determinista, que los
// tests avancen el tiempo a voluntad y que en el futuro se puedan reproducir replays.
// Ver ../../../../docs/architecture/game-loop.md
package clock

import (
	"sync"
	"time"
)

// Clock es la única fuente de tiempo autorizada dentro del dominio.
type Clock interface {
	// Now devuelve el instante actual.
	Now() time.Time
	// NowMs devuelve el instante actual en epoch milliseconds. Es la unidad
	// canónica de la simulación: toda aritmética temporal del juego usa enteros.
	NowMs() int64
}

// SystemClock es la implementación de producción: delega en el reloj del sistema.
type SystemClock struct{}

// NewSystemClock crea el reloj de producción.
func NewSystemClock() SystemClock { return SystemClock{} }

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) NowMs() int64 { return time.Now().UnixMilli() }

// FakeClock es un reloj controlado manualmente para tests deterministas.
// Es seguro para uso concurrente.
type FakeClock struct {
	mu sync.RWMutex
	ms int64
}

// NewFakeClock crea un reloj detenido en el instante indicado.
func NewFakeClock(startMs int64) *FakeClock {
	return &FakeClock{ms: startMs}
}

func (c *FakeClock) Now() time.Time {
	return time.UnixMilli(c.NowMs()).UTC()
}

func (c *FakeClock) NowMs() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ms
}

// Advance adelanta el reloj la duración indicada.
func (c *FakeClock) Advance(d time.Duration) {
	c.AdvanceMs(d.Milliseconds())
}

// AdvanceMs adelanta el reloj los milisegundos indicados.
func (c *FakeClock) AdvanceMs(ms int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ms += ms
}

// SetMs fija el reloj en un instante absoluto.
func (c *FakeClock) SetMs(ms int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ms = ms
}
