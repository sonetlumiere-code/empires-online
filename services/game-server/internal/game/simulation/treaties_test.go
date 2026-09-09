package simulation_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/diplomacy"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loop"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
)

// expirerContador registra cuántas veces se le pidió caducar tratados.
type expirerContador struct {
	mu       sync.Mutex
	llamadas int
}

func (e *expirerContador) ExpireDue(context.Context, time.Time, uint64) ([]diplomacy.Treaty, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.llamadas++
	return nil, nil
}

func (e *expirerContador) total() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.llamadas
}

func TestLaCaducidadDeTratadosSeMuestreaYNoOcurreEnCadaTick(t *testing.T) {
	// A 10 Hz, comprobar la caducidad en cada tick serían diez consultas por
	// segundo para descartar casi siempre cero filas. Un tratado que caduca un
	// segundo tarde no cambia nada del juego; ese tráfico contra la base sí.
	h := newHarness(t)
	expirer := &expirerContador{}
	persister := &syncPersister{}

	h.sim = simulation.New(h.state, simulation.Deps{
		Clock:              h.clk,
		Pathfinder:         pathfinding.NewAStar(20000, 256),
		Broadcaster:        h.rec,
		Persister:          persister,
		Repos:              newFakeRepos(),
		Treaties:           expirer,
		Log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		ProtectionCooldown: 300 * time.Second,
		DisconnectGrace:    30 * time.Second,
	})
	h.loop = loop.New(h.sim, h.clk, h.commands, loop.Config{
		TickDuration: 100 * time.Millisecond, FlushIntervalTicks: 50,
		MaxCommandsPerTick: 256, EpochMs: epoch,
	}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 5 segundos = 50 ticks. Con un muestreo por segundo deben salir unas 5
	// consultas, no 50.
	h.advance(5 * time.Second)

	trabajos := 0
	for _, name := range persister.names() {
		if name == "treaty.expire" {
			trabajos++
		}
	}

	assert.LessOrEqual(t, trabajos, 6, "el muestreo debe acotar las consultas")
	assert.GreaterOrEqual(t, trabajos, 4, "pero tiene que ejecutarse de verdad")
	assert.Equal(t, trabajos, expirer.total(),
		"cada trabajo encolado debe traducirse en exactamente una consulta")
}

func TestSinCaducadorConfiguradoElTickNoEncolaNada(t *testing.T) {
	// La dependencia es opcional a propósito: los tests de simulación pura no
	// tienen base de datos, y exigirles un doble sólo para que el tick avance
	// acoplaría el nivel `simulation` a algo que no verifica.
	h := newHarness(t)
	persister := &syncPersister{}

	h.sim = simulation.New(h.state, simulation.Deps{
		Clock:              h.clk,
		Pathfinder:         pathfinding.NewAStar(20000, 256),
		Broadcaster:        h.rec,
		Persister:          persister,
		Repos:              newFakeRepos(),
		Log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		ProtectionCooldown: 300 * time.Second,
		DisconnectGrace:    30 * time.Second,
	})
	h.loop = loop.New(h.sim, h.clk, h.commands, loop.Config{
		TickDuration: 100 * time.Millisecond, FlushIntervalTicks: 50,
		MaxCommandsPerTick: 256, EpochMs: epoch,
	}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	h.advance(3 * time.Second)

	for _, name := range persister.names() {
		assert.NotEqual(t, "treaty.expire", name)
	}
}
