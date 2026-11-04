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

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
)

// descartes lee eo_persistence_jobs_dropped_total{job, reason}.
func descartes(m *observability.Metrics, job, reason string) float64 {
	return testutil.ToFloat64(m.PersistenceJobsDropped.WithLabelValues(job, reason))
}

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

// ADR-022: un trabajo descartado por partición llena se cuenta en
// eo_persistence_jobs_dropped_total con reason=queue_full, además de compensarse;
// la etiqueta `job` es su nombre si está en el catálogo, y `unknown` si no.
func TestUnDescartePorColaLlenaSeCuentaPorTrabajoYMotivo(t *testing.T) {
	m := observability.NewMetrics()
	q := NewQueue(QueueConfig{Capacity: 1, Workers: 1, MaxAttempts: 1, RetryDelay: time.Millisecond},
		slog.New(slog.NewTextHandler(io.Discard, nil)), m) // sin workers: nada consume
	q.Submit(simulation.Job{Name: simulation.JobUnitsFlush, Run: func(context.Context) error { return nil }})

	compensado := false
	q.Submit(simulation.Job{
		Name:               simulation.JobUnitsFlush,
		Run:                func(context.Context) error { return nil },
		OnPermanentFailure: func(error) { compensado = true },
	})
	q.Submit(simulation.Job{Name: "inventado", Run: func(context.Context) error { return nil }})

	assert.True(t, compensado, "la compensación sigue ejecutándose")
	assert.EqualValues(t, 1, descartes(m, simulation.JobUnitsFlush, observability.DropQueueFull))
	assert.Zero(t, descartes(m, simulation.JobUnitsFlush, observability.DropAttemptsExhausted))
	assert.EqualValues(t, 1, descartes(m, observability.JobUnknown, observability.DropQueueFull),
		"un nombre fuera del catálogo no crea una serie nueva")
}

// ADR-022: agotar los intentos cuenta con reason=attempts_exhausted, una sola
// vez por trabajo aunque haya habido tres intentos.
func TestAgotarLosIntentosSeCuentaUnaVezPorTrabajo(t *testing.T) {
	m := observability.NewMetrics()
	q := NewQueue(QueueConfig{Capacity: 8, Workers: 1, MaxAttempts: 3, RetryDelay: time.Millisecond},
		slog.New(slog.NewTextHandler(io.Discard, nil)), m)
	q.Start(context.Background())

	var intentos atomic.Int32
	compensado := false
	q.Submit(simulation.Job{
		Name:               simulation.JobCityPresence,
		Run:                func(context.Context) error { intentos.Add(1); return errors.New("PostgreSQL no responde") },
		OnPermanentFailure: func(error) { compensado = true },
	})
	q.Submit(simulation.Job{Name: simulation.JobMovementComplete, Run: func(context.Context) error { return nil }})
	q.Close()

	assert.EqualValues(t, 3, intentos.Load())
	assert.True(t, compensado)
	assert.EqualValues(t, 1, descartes(m, simulation.JobCityPresence, observability.DropAttemptsExhausted))
	assert.Zero(t, descartes(m, simulation.JobCityPresence, observability.DropQueueFull))
	assert.Zero(t, descartes(m, simulation.JobMovementComplete, observability.DropAttemptsExhausted),
		"el trabajo que salió bien no cuenta")
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

// dosClavesDeParticionesDistintas devuelve dos claves de ciudad que caen en
// particiones distintas de una cola de n workers.
func dosClavesDeParticionesDistintas(t *testing.T, n int) (string, string) {
	t.Helper()
	a := simulation.CityKey(1)
	for i := int64(2); i < 200; i++ {
		if b := simulation.CityKey(i); simulation.PartitionOf(b, n) != simulation.PartitionOf(a, n) {
			return a, b
		}
	}
	t.Fatal("no hay dos claves en particiones distintas")
	return "", ""
}

// ADR-032 §3: un trabajo de dos claves espera a que la otra partición llegue a
// él, y lo que se encoló después en cualquiera de las dos espera a que termine.
func TestUnTrabajoDeDosClavesEsperaALasDosParticiones(t *testing.T) {
	q := NewQueue(QueueConfig{Capacity: 64, Workers: 4, MaxAttempts: 3, RetryDelay: time.Millisecond},
		slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	q.Start(context.Background())
	a, b := dosClavesDeParticionesDistintas(t, 4)

	var mu sync.Mutex
	var orden []string
	anotar := func(s string) { mu.Lock(); orden = append(orden, s); mu.Unlock() }

	soltar := make(chan struct{})
	q.Submit(simulation.Job{Name: "volcado B", Key: b, Run: func(context.Context) error {
		<-soltar
		anotar("volcado B")
		return nil
	}})
	q.Submit(simulation.Job{Name: "trato", Key: a, Key2: b, Run: func(context.Context) error {
		anotar("trato")
		return nil
	}})
	q.Submit(simulation.Job{Name: "encargo A", Key: a, Run: func(context.Context) error {
		anotar("encargo A")
		return nil
	}})
	q.Submit(simulation.Job{Name: "encargo B", Key: b, Run: func(context.Context) error {
		anotar("encargo B")
		return nil
	}})

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	assert.Empty(t, orden, "con la partición de B ocupada, ni el trato ni lo que va detrás en A se ejecutan")
	mu.Unlock()

	close(soltar)
	q.Close()

	require.Len(t, orden, 4)
	assert.Equal(t, "volcado B", orden[0])
	assert.Equal(t, "trato", orden[1], "el trato va después de lo anterior de B y antes de lo posterior de A y de B")
	assert.ElementsMatch(t, []string{"encargo A", "encargo B"}, orden[2:])
}

// Dos tratos sobre las mismas dos ciudades, en sentidos opuestos y con
// trabajos de una clave entre medias, terminan los dos y en el orden en que se
// encolaron: no hay bloqueo cruzado.
func TestDosTratosCruzadosNoSeBloquean(t *testing.T) {
	q := NewQueue(QueueConfig{Capacity: 256, Workers: 4, MaxAttempts: 3, RetryDelay: time.Millisecond},
		slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	q.Start(context.Background())
	a, b := dosClavesDeParticionesDistintas(t, 4)

	var mu sync.Mutex
	porClave := map[string][]int{}
	anotar := func(n int, claves ...string) {
		mu.Lock()
		for _, k := range claves {
			porClave[k] = append(porClave[k], n)
		}
		mu.Unlock()
	}

	for n := 0; n < 40; n++ {
		n := n
		switch n % 4 {
		case 0:
			q.Submit(simulation.Job{Name: "trato", Key: a, Key2: b, Run: func(context.Context) error { anotar(n, a, b); return nil }})
		case 1:
			q.Submit(simulation.Job{Name: "trato", Key: b, Key2: a, Run: func(context.Context) error { anotar(n, a, b); return nil }})
		case 2:
			q.Submit(simulation.Job{Name: "volcado", Key: a, Run: func(context.Context) error { anotar(n, a); return nil }})
		default:
			q.Submit(simulation.Job{Name: "volcado", Key: b, Run: func(context.Context) error { anotar(n, b); return nil }})
		}
	}

	terminado := make(chan struct{})
	go func() { q.Close(); close(terminado) }()
	select {
	case <-terminado:
	case <-time.After(5 * time.Second):
		t.Fatal("la cola no terminó: bloqueo cruzado")
	}

	for _, k := range []string{a, b} {
		got := porClave[k]
		require.Len(t, got, 30, "cada clave ve sus 10 volcados y los 20 tratos")
		for i := 1; i < len(got); i++ {
			assert.Less(t, got[i-1], got[i], "la clave %s ve sus trabajos en el orden en que se encolaron", k)
		}
	}
}

// Con las dos claves en la misma partición, el trabajo es uno normal.
func TestUnTrabajoDeDosClavesEnLaMismaParticionEsUnoNormal(t *testing.T) {
	q := colaDePrueba(8, 1)
	q.Start(context.Background())

	var veces atomic.Int32
	q.Submit(simulation.Job{Name: "trato", Key: "city:1", Key2: "city:2", Run: func(context.Context) error {
		veces.Add(1)
		return nil
	}})
	q.Close()

	assert.EqualValues(t, 1, veces.Load())
}

// Si una de las dos particiones está llena, el trabajo no entra en ninguna y
// se compensa una vez; la otra partición no se queda con una mitad huérfana.
func TestUnTrabajoDeDosClavesQueNoCabeNoEntraEnNinguna(t *testing.T) {
	m := observability.NewMetrics()
	q := NewQueue(QueueConfig{Capacity: 4, Workers: 2, MaxAttempts: 1, RetryDelay: time.Millisecond},
		slog.New(slog.NewTextHandler(io.Discard, nil)), m)
	a, b := dosClavesDeParticionesDistintas(t, 2)

	// Sin arrancar los workers: la partición de B se llena con dos trabajos.
	q.Submit(simulation.Job{Name: "relleno", Key: b, Run: func(context.Context) error { return nil }})
	q.Submit(simulation.Job{Name: "relleno", Key: b, Run: func(context.Context) error { return nil }})

	var compensado, ejecutado atomic.Int32
	q.Submit(simulation.Job{
		Name: simulation.JobStocksFlush, Key: a, Key2: b,
		Run:                func(context.Context) error { ejecutado.Add(1); return nil },
		OnPermanentFailure: func(error) { compensado.Add(1) },
	})
	assert.Equal(t, 2, q.Depth(), "la mitad de A no entró")

	q.Start(context.Background())
	q.Close()
	assert.EqualValues(t, 1, compensado.Load())
	assert.EqualValues(t, 0, ejecutado.Load())
	assert.InDelta(t, 1, descartes(m, simulation.JobStocksFlush, observability.DropQueueFull), 0)
}
