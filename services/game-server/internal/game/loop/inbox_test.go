package loop_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loop"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
)

func silencio() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ADR-022: Offer nunca bloquea y, con el buzón lleno, cuenta en
// eo_commands_dropped_total y registra el comando que no cupo: nunca se pierde
// en silencio.
func TestElBuzonLlenoNoBloqueaYCuentaElComandoQueNoCupo(t *testing.T) {
	m := observability.NewMetrics()
	b := loop.NewInbox(1, m, silencio())
	require.True(t, b.Offer(simulation.MovementPersistFailed{UnitID: 1, MovementID: 1}))

	cupo := make(chan bool, 1)
	go func() { cupo <- b.Offer(simulation.CityPresencePersistFailed{CityID: 7}) }()
	select {
	case ok := <-cupo:
		assert.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("Offer bloqueó con el buzón lleno")
	}

	assert.EqualValues(t, 1, testutil.ToFloat64(m.CommandsDropped.WithLabelValues(
		simulation.CommandType(simulation.CityPresencePersistFailed{}))))
	assert.Zero(t, testutil.ToFloat64(m.CommandsDropped.WithLabelValues(
		simulation.CommandType(simulation.MovementPersistFailed{}))), "el que cupo no cuenta")
	assert.Equal(t, 1, b.Len())
}

// ADR-022: en cada tick el loop drena el buzón ANTES que el canal de comandos.
// Un alta depositada en el buzón se aplica antes que el snapshot que ya
// esperaba en el canal, y el snapshot ve la ciudad ONLINE. (En producción el
// alta no viaja por el buzón —INV-CITY-014 exige que comparta canal con el
// snapshot—; aquí se usa porque su efecto es observable sin red.)
func TestElBuzonSeDrenaAntesQueElCanalDeComandos(t *testing.T) {
	const epoch int64 = 1_757_376_000_000
	terrain := make([]byte, 64*64)
	for i := range terrain {
		terrain[i] = byte(world.Grassland)
	}
	w, err := world.New(64, 64, 32, 1, terrain)
	require.NoError(t, err)
	state := simulation.NewState(w)
	playerID := uuid.New()
	desconexion := time.UnixMilli(epoch - 600_000).UTC()
	state.AddCity(&city.City{
		ID: 1, OwnerPlayerID: playerID, Name: "Dormida", CenterX: 10, CenterY: 10, Era: city.EraStone,
		PopulationLimit: 20, PresenceState: city.PresenceProtected, LastOfflineAt: &desconexion,
	})

	clk := clock.NewFakeClock(epoch)
	sim := simulation.New(state, simulation.Deps{
		Clock: clk, Pathfinder: pathfinding.NewAStar(20000, 256), Log: silencio(),
		ProtectionCooldown: 300 * time.Second, DisconnectGrace: 30 * time.Second,
	})
	commands := make(chan simulation.Command, 16)
	inbox := loop.NewInbox(16, nil, silencio())
	l := loop.New(sim, clk, commands, inbox, loop.Config{
		TickDuration: 100 * time.Millisecond, FlushIntervalTicks: 50, MaxCommandsPerTick: 16, EpochMs: epoch,
	}, nil, nil, silencio())

	reply := make(chan simulation.SnapshotResult, 1)
	commands <- simulation.RequestSnapshot{PlayerID: playerID, SessionID: uuid.New(), RadiusChunks: 1, Reply: reply}
	require.True(t, inbox.Offer(simulation.PlayerConnected{PlayerID: playerID, SessionID: uuid.New()}))
	l.Step(clk.NowMs())

	res := <-reply
	require.True(t, res.Found)
	require.Len(t, res.Payload.Cities, 1)
	assert.Equal(t, string(city.PresenceOnline), res.Payload.Cities[0].PresenceState,
		"el alta del buzón se aplicó antes que el snapshot del canal")
	assert.Zero(t, inbox.Len())
	assert.Empty(t, commands)
}
