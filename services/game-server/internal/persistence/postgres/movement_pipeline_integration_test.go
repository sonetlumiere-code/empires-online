//go:build integration

package postgres_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// aceptados registra los unit.move.accepted que emite la simulación.
type aceptados struct {
	simulation.NoopBroadcaster
	mu  sync.Mutex
	ids []int64
}

func (a *aceptados) SendToPlayer(_ uuid.UUID, msgType, _ string, payload any) {
	if p, ok := payload.(protocol.UnitMoveAcceptedPayload); ok && msgType == protocol.TypeUnitMoveAccepted {
		a.mu.Lock()
		a.ids = append(a.ids, p.MovementID)
		a.mu.Unlock()
	}
}

// ADR-013 con la cola REAL, no con un persistidor síncrono. Es el escenario que
// los tests anteriores no veían: con cuatro workers asíncronos, el id aceptado
// tiene que ser el de la fila en disco, y la posición final tiene que estar en
// units nada más cerrarse el movimiento, sin esperar a ningún volcado. Un
// reinicio en ese instante deja la unidad donde llegó.
func TestConLaColaRealElMovimientoLlevaSuIdYCierraConSuPosicion(t *testing.T) {
	store, ctx := newTestStore(t)
	_, cities, units, movements, bootstrapper := newRepos(store)
	alta, err := bootstrapper.Create(ctx, bootstrapRequest("asincrona", world.Tile{X: 20, Y: 20}))
	require.NoError(t, err)
	u := alta.Units[0]
	destino := world.Tile{X: u.X + 2, Y: u.Y}

	clk := clock.NewFakeClock(testEpoch)
	vivas, err := units.ListAlive(ctx)
	require.NoError(t, err)
	ciudades, err := cities.ListAll(ctx)
	require.NoError(t, err)
	state := simulation.NewState(testWorld(t))
	simulation.Hydrate(state, vivas, ciudades, nil, clk.NowMs(), discardLogger())
	maxID, err := movements.MaxID(ctx)
	require.NoError(t, err)
	state.SetMovementIDFloor(maxID)

	q := persistence.NewQueue(persistence.DefaultQueueConfig(), discardLogger(), nil)
	q.Start(ctx)
	rec := &aceptados{}
	sim := simulation.New(state, simulation.Deps{
		Clock: clk, Pathfinder: pathfinding.NewAStar(20000, 256), Broadcaster: rec,
		Persister: q, Repos: persistence.NewGameStore(store, units, cities, movements),
		Log: discardLogger(), ProtectionCooldown: 300 * time.Second, DisconnectGrace: 30 * time.Second,
	})

	sim.Apply(simulation.MoveUnit{PlayerID: alta.Player.ID, RequestID: uuid.NewString(),
		UnitID: u.ID, Target: destino})
	require.Len(t, rec.ids, 1)
	aceptado := rec.ids[0]
	require.NotZero(t, aceptado, "el id aceptado no puede ser 0 con la cola real")

	clk.AdvanceMs(5_000)
	sim.AdvanceMovements(clk.NowMs()) // llega; NO se llama a FlushDirty
	q.Close()

	var estado string
	require.NoError(t, store.Pool().QueryRow(ctx,
		`SELECT status FROM unit_movements WHERE id = $1`, aceptado).Scan(&estado),
		"la fila del movimiento lleva el id que se aceptó")
	assert.Equal(t, "COMPLETED", estado)

	enDisco, err := units.ListByPlayer(ctx, alta.Player.ID)
	require.NoError(t, err)
	var persistida *unit.Unit
	for _, x := range enDisco {
		if x.ID == u.ID {
			persistida = x
		}
	}
	require.NotNil(t, persistida)
	assert.Equal(t, destino, persistida.Tile(), "la posición final está en disco sin ningún volcado")
	assert.Equal(t, unit.StatusIdle, persistida.Status)

	// Reinicio justo ahora: la unidad está donde llegó.
	reiniciada, _ := arrancar(t, ctx, store, testWorld(t), clk)
	tras, ok := reiniciada.State().Unit(u.ID)
	require.True(t, ok)
	assert.Equal(t, destino, tras.Tile())
	assert.Equal(t, unit.StatusIdle, tras.Status)
}
