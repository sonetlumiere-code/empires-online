package persistence

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
)

func colaDePrueba(capacidad, intentos int) *Queue {
	return NewQueue(QueueConfig{Capacity: capacidad, Workers: 1, MaxAttempts: intentos, RetryDelay: time.Millisecond},
		slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}

// Un fallo transitorio se reintenta y, si el trabajo acaba saliendo bien, la
// compensación no se ejecuta.
func TestUnFalloTransitorioSeReintenta(t *testing.T) {
	q := colaDePrueba(8, 3)
	q.Start(context.Background())

	var intentos atomic.Int32
	compensado := false
	q.Submit(simulation.Job{
		Name: "transitorio",
		Run: func(context.Context) error {
			if intentos.Add(1) < 3 {
				return errors.New("la base de datos no responde")
			}
			return nil
		},
		OnPermanentFailure: func(error) { compensado = true },
	})
	q.Close()

	assert.EqualValues(t, 3, intentos.Load())
	assert.False(t, compensado)
}

// Agotados los intentos, se llama a la compensación con el último error, una
// sola vez.
func TestAgotadosLosIntentosSeCompensaUnaVez(t *testing.T) {
	q := colaDePrueba(8, 3)
	q.Start(context.Background())

	var intentos atomic.Int32
	var compensaciones []error
	q.Submit(simulation.Job{
		Name:               "permanente",
		Run:                func(context.Context) error { intentos.Add(1); return errors.New("fallo permanente") },
		OnPermanentFailure: func(err error) { compensaciones = append(compensaciones, err) },
	})
	q.Close()

	assert.EqualValues(t, 3, intentos.Load())
	require.Len(t, compensaciones, 1)
	assert.EqualError(t, compensaciones[0], "fallo permanente")
}

// Submit nunca bloquea: con la cola llena, el trabajo se descarta y se
// compensa en el acto, en la goroutine del llamante.
func TestConLaColaLlenaSubmitNoBloqueaYCompensa(t *testing.T) {
	q := colaDePrueba(1, 1) // sin workers: nada consume
	q.Submit(simulation.Job{Name: "ocupa", Run: func(context.Context) error { return nil }})

	var compensado error
	hecho := make(chan struct{})
	go func() {
		q.Submit(simulation.Job{
			Name:               "sobra",
			Run:                func(context.Context) error { return nil },
			OnPermanentFailure: func(err error) { compensado = err },
		})
		close(hecho)
	}()
	select {
	case <-hecho:
	case <-time.After(time.Second):
		t.Fatal("Submit bloqueó con la cola llena")
	}
	assert.ErrorIs(t, compensado, context.DeadlineExceeded)
	assert.Equal(t, 1, q.Depth())
}

// Close vacía lo que quedaba en el canal y espera al trabajo que un worker ya
// estaba ejecutando. Drain no ve ese trabajo, porque ya salió del canal: por
// eso el apagado llama a Close después de Drain.
func TestCloseEsperaAlTrabajoEnVueloYALosPendientes(t *testing.T) {
	q := colaDePrueba(8, 1)
	q.Start(context.Background())

	empezado := make(chan struct{})
	soltar := make(chan struct{})
	var terminados atomic.Int32
	q.Submit(simulation.Job{Name: "lento", Run: func(context.Context) error {
		close(empezado)
		<-soltar
		terminados.Add(1)
		return nil
	}})
	<-empezado
	for i := 0; i < 2; i++ {
		q.Submit(simulation.Job{Name: "pendiente", Run: func(context.Context) error {
			terminados.Add(1)
			return nil
		}})
	}

	assert.False(t, q.Drain(20*time.Millisecond), "con el worker ocupado, los pendientes siguen en el canal")
	go func() { time.Sleep(20 * time.Millisecond); close(soltar) }()
	q.Close()
	assert.EqualValues(t, 3, terminados.Load(), "Close no vuelve hasta terminar todo")
}

// La cancelación del contexto del proceso no cancela una escritura en vuelo
// (INV-PERSIST-001): el apagado debe dejarla terminar, no abortarla a medias.
func TestCancelarElProcesoNoCancelaUnaEscrituraEnVuelo(t *testing.T) {
	q := colaDePrueba(8, 1)
	ctx, cancel := context.WithCancel(context.Background())
	q.Start(ctx)

	empezado := make(chan struct{})
	var errDeLaEscritura error
	q.Submit(simulation.Job{Name: "en vuelo", Run: func(c context.Context) error {
		close(empezado)
		time.Sleep(30 * time.Millisecond)
		errDeLaEscritura = c.Err()
		return nil
	}})
	<-empezado
	cancel()
	q.Close()

	assert.NoError(t, errDeLaEscritura, "el contexto de la escritura no hereda la cancelación")
}

// ADR-013 §2.2: los trabajos de una misma clave se ejecutan en el orden en que
// se encolaron aunque el primero tenga que reintentarse, y una clave de otra
// partición no espera a ese reintento.
func TestLaColaOrdenaPorClaveYNoBloqueaOtrasClaves(t *testing.T) {
	q := NewQueue(QueueConfig{Capacity: 64, Workers: 4, MaxAttempts: 3, RetryDelay: 30 * time.Millisecond},
		slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	q.Start(context.Background())

	clave := "unit:1"
	otra := ""
	for i := 2; i < 100 && otra == ""; i++ {
		k := "unit:" + strconv.Itoa(i)
		if simulation.PartitionOf(k, 4) != simulation.PartitionOf(clave, 4) {
			otra = k
		}
	}
	require.NotEmpty(t, otra)

	var mu sync.Mutex
	var orden []string
	anotar := func(s string) { mu.Lock(); orden = append(orden, s); mu.Unlock() }

	var intentos atomic.Int32
	q.Submit(simulation.Job{Name: "inicio", Key: clave, Run: func(context.Context) error {
		if intentos.Add(1) < 3 {
			return errors.New("transitorio")
		}
		anotar("inicio")
		return nil
	}})
	q.Submit(simulation.Job{Name: "cierre", Key: clave, Run: func(context.Context) error {
		anotar("cierre")
		return nil
	}})
	q.Submit(simulation.Job{Name: "ajeno", Key: otra, Run: func(context.Context) error {
		anotar("ajeno")
		return nil
	}})
	q.Close()

	require.Len(t, orden, 3)
	assert.Equal(t, "ajeno", orden[0], "la otra partición no espera a los reintentos")
	assert.Equal(t, []string{"inicio", "cierre"}, orden[1:], "la misma clave, en orden")
}
