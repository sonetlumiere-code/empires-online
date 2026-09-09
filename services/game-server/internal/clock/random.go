package clock

import "math/rand/v2"

// RandomSource aísla al dominio de la aleatoriedad global.
//
// Igual que con el reloj: el dominio nunca llama a math/rand directamente. Una
// fuente inyectada y sembrada explícitamente permite reproducir cualquier partida
// y cualquier bug a partir de su semilla.
type RandomSource interface {
	// Int63n devuelve un entero en [0, n). Pánico si n <= 0.
	Int63n(n int64) int64
	// Float64 devuelve un valor en [0.0, 1.0).
	Float64() float64
}

type pcgRandom struct{ r *rand.Rand }

// NewSeededRandom crea una fuente determinista a partir de una semilla.
// La misma semilla produce siempre exactamente la misma secuencia.
func NewSeededRandom(seed uint64) RandomSource {
	return &pcgRandom{r: rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))}
}

func (p *pcgRandom) Int63n(n int64) int64 { return p.r.Int64N(n) }

func (p *pcgRandom) Float64() float64 { return p.r.Float64() }
